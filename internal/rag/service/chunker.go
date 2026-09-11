// chunker.go —— 分块与解析（spec 06）：解析槽位 + 递归分割 + token 估算，全部纯函数
// （零 IO / 零 goroutine）；消费方 = spec 07 入库管线。
package service

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// extractText 解析环节槽位（spec 06 §1）：一期 txt/md 读 Content 原样返回，二期换
// 文件卷 + PDF 提取实现、签名不变（管道形状即最终形状）。上传边界（BOM/编码/首尾
// 空白规范化）已在 api Validate 完成，这里不再清洗；非 txt/md 返回 error——Validate
// 已拦截，属防御分支（二期 PDF 接入前的误用兜底）。
func extractText(doc *Document) (string, error) {
	switch doc.FileType {
	case "txt", "md":
		return doc.Content, nil
	default:
		return "", fmt.Errorf("extract text: unsupported file type %q", doc.FileType)
	}
}

// estimateTokens token 估算（spec 06 §3）= ceil(ASCII 字符数/4 + 非ASCII 字符数)
// （cl100k 经验值：英文 ≈4 char/token、中文 ≈1-1.5）。写入 DocumentChunk.TokenCount
// （07 组装时调用）；embedding API 只有批量级 usage 无按条计数，估算列够用。
func estimateTokens(content string) int {
	ascii, nonASCII := 0, 0
	for _, r := range content {
		if r < utf8.RuneSelf { // ASCII 码点 < 128
			ascii++
		} else {
			nonASCII++
		}
	}
	return (ascii+3)/4 + nonASCII // (ascii+3)/4 即 ceil(ascii/4)
}

// SplitChunks 递归分割（spec 06 §2）：段落贪心合并 → 单段超长降级句子切 → 无标点
// 硬截，MD 围栏原子保护，块间以 overlap 尾缀衔接；尺寸单位 rune。纯函数、零 IO /
// 零 goroutine。非法参数（size≤0 / overlap<0 / overlap≥size）与空 / 全空白输入返
// 回 nil——config 已校验，此处兜底不可达。
func SplitChunks(content string, size, overlap int) []string {
	if size <= 0 || overlap < 0 || overlap >= size {
		return nil
	}
	if strings.TrimSpace(content) == "" {
		return nil
	}
	content = strings.ReplaceAll(content, "\r\n", "\n") // CRLF 归一（规则 1）
	// 整体不超限：单块直通（唯一块，无 overlap 前缀）。
	if utf8.RuneCountInString(content) <= size {
		return []string{content}
	}
	chunks := packUnits(buildUnits(content, size), size)
	if len(chunks) == 0 {
		return nil
	}
	return applyOverlap(chunks, overlap)
}

// unit 分割原子单元：text 为内容，sep 为并入非空块时拼在前面的分隔符——
// 段落 / 围栏用 "\n\n"（保留文档结构），同一单元降级切出的续片用 ""（拼接还原原文）。
type unit struct {
	text    string
	sep     string
	isFence bool // 围栏原子单元（规则 4）：超长只硬截，不走句子切——标点会切散代码
}

// buildUnits 把已归一的内容切成原子单元序列（规则 4 围栏预扫描 + 规则 1 段落切分），
// 超长单元降级：段落走句子组（规则 2/3），围栏只硬截。
func buildUnits(content string, size int) []unit {
	var units []unit
	for _, u := range splitUnits(content) {
		if runeLen(u.text) <= size {
			units = append(units, u)
			continue
		}
		var pieces []string
		if u.isFence {
			pieces = hardCut(u.text, size)
		} else {
			pieces = packSentences(splitSentences(u.text), size)
		}
		for j, p := range pieces {
			sep := "" // 同一单元的降级续片：拼接还原原文
			if j == 0 {
				sep = u.sep // 首片仍是新段落
			}
			units = append(units, unit{text: p, sep: sep, isFence: u.isFence})
		}
	}
	return units
}

// splitUnits 围栏预扫描（规则 4）：行 TrimSpace 后以 ``` 开头即围栏行（容忍缩进与
// info string），开—闭两行（含内部全部行）构成一个原子围栏单元——块内 \n\n 不触发
// 段落切分；未闭合则其后全部保守视为围栏。围栏外文本走段落切分。
func splitUnits(content string) []unit {
	lines := strings.Split(content, "\n")
	var units []unit
	var plain []string
	fenceStart := -1

	flushPlain := func() {
		if len(plain) == 0 {
			return
		}
		units = append(units, splitParagraphs(strings.Join(plain, "\n"))...)
		plain = plain[:0]
	}

	for i, line := range lines {
		isFenceLine := strings.HasPrefix(strings.TrimSpace(line), "```")
		switch {
		case fenceStart >= 0 && isFenceLine: // 闭合围栏
			units = append(units, unit{text: strings.Join(lines[fenceStart:i+1], "\n"), sep: "\n\n", isFence: true})
			fenceStart = -1
		case fenceStart >= 0: // 围栏内部：整块保留
		case isFenceLine: // 开围栏
			flushPlain()
			fenceStart = i
		default:
			plain = append(plain, line)
		}
	}
	if fenceStart >= 0 { // 未闭合：其后全部保守视为围栏
		units = append(units, unit{text: strings.Join(lines[fenceStart:], "\n"), sep: "\n\n", isFence: true})
	}
	flushPlain()
	return units
}

// sentenceEnds 句子边界标点集（规则 2）：全角 。！？；． + 半角 !?;。
// ASCII '.' 不在集内——小数点 / 缩写豁免（3.14、e.g. 不误切），英文陈述句靠硬截兜底。
const sentenceEnds = "。！？；．!?;"

// isSentenceEnd r 是否句子边界标点。
func isSentenceEnd(r rune) bool {
	return strings.ContainsRune(sentenceEnds, r)
}

// splitSentences 按句子边界切段（规则 2）：切点在标点后——标点归前句；各段为
// 连续切片，join("") 可还原原文。
func splitSentences(text string) []string {
	var sentences []string
	start := 0
	for i, r := range text {
		if !isSentenceEnd(r) {
			continue
		}
		end := i + utf8.RuneLen(r)
		sentences = append(sentences, text[start:end])
		start = end
	}
	if start < len(text) {
		sentences = append(sentences, text[start:])
	}
	return sentences
}

// packSentences 句子贪心合并（规则 2）：连续句子累计 ≤ size 即并入当前组，
// 装不下出组；组内拼接无分隔符（句子本是连续切片）。单句仍超长（无标点长串，
// 规则 3）出组后硬截成独立组，尾片照常参与后续合并。
func packSentences(sentences []string, size int) []string {
	var groups []string
	cur := ""
	for _, s := range sentences {
		if runeLen(s) > size {
			if cur != "" {
				groups = append(groups, cur)
				cur = ""
			}
			groups = append(groups, hardCut(s, size)...)
			continue
		}
		if cur == "" {
			cur = s
			continue
		}
		if runeLen(cur+s) <= size {
			cur += s
			continue
		}
		groups = append(groups, cur)
		cur = s
	}
	if cur != "" {
		groups = append(groups, cur)
	}
	return groups
}

// hardCut 按 rune 硬截（规则 3）：切成 ≤ size 的连续片段；多字节字符按完整
// rune 切、不撕裂字节；恰好整除时不留空尾块。
func hardCut(text string, size int) []string {
	rs := []rune(text)
	pieces := make([]string, 0, (len(rs)+size-1)/size)
	for i := 0; i < len(rs); i += size {
		end := min(i+size, len(rs))
		pieces = append(pieces, string(rs[i:end]))
	}
	return pieces
}

// splitParagraphs 段落切分（规则 1）：按 \n\n 切段、空段丢弃、段落外空白剥离
// （段内换行 / 缩进保留）。
func splitParagraphs(content string) []unit {
	var units []unit
	for _, para := range strings.Split(content, "\n\n") {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		units = append(units, unit{text: para, sep: "\n\n"})
	}
	return units
}

// packUnits 贪心装块（规则 1）：连续单元累计（含分隔符）≤ size 即并入当前块，
// 装不下就出块。约定进入的单元均 ≤ size（超长已在 buildUnits 降级）。
func packUnits(units []unit, size int) []string {
	var chunks []string
	cur := ""
	for _, u := range units {
		if cur == "" {
			cur = u.text
			continue
		}
		if runeLen(cur+u.sep+u.text) <= size {
			cur += u.sep + u.text
			continue
		}
		chunks = append(chunks, cur)
		cur = u.text
	}
	if cur != "" {
		chunks = append(chunks, cur)
	}
	return chunks
}

// runeLen 字符串的 rune 数——分割的尺寸单位（规则 6）。
func runeLen(s string) int { return utf8.RuneCountInString(s) }

// applyOverlap 块间尾缀衔接（规则 5）：除首块外，每块前缀 = 上一块（overlap 应用
// 前的内容）尾部 min(overlap, len) 个 rune。块内容 ≤ size 不变，加前缀后
// ≤ size + overlap；单块或 overlap=0 原样返回。
func applyOverlap(chunks []string, overlap int) []string {
	if overlap <= 0 || len(chunks) <= 1 {
		return chunks
	}
	out := make([]string, len(chunks))
	out[0] = chunks[0]
	for i := 1; i < len(chunks); i++ {
		out[i] = tailRunes(chunks[i-1], min(overlap, runeLen(chunks[i-1]))) + chunks[i]
	}
	return out
}

// tailRunes 取字符串尾部 n 个 rune；n ≥ 全长原样返回。
func tailRunes(s string, n int) string {
	rs := []rune(s)
	if n >= len(rs) {
		return s
	}
	return string(rs[len(rs)-n:])
}
