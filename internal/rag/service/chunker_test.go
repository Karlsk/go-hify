package service

// spec 06（backend_spec_06_chunker）纯函数表驱动测试：零 goroutine / 零 IO。
// SplitChunks 用例按 T3a-f 渐进补入（本文件随任务增长）。

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- extractText（spec 06 §1：一期 pass-through，二期换 PDF 实现签名不变） ----

func TestExtractText(t *testing.T) {
	cases := []struct {
		name    string
		ft      string
		content string
		want    string
		wantErr bool
	}{
		{"txt 原样返回", "txt", "正文内容", "正文内容", false},
		{"md 原样返回", "md", "# 标题\n\n正文", "# 标题\n\n正文", false},
		{"空内容照返", "txt", "", "", false},
		{"不做清洗", "txt", "  前后空白保留  ", "  前后空白保留  ", false},
		{"pdf 返回 error（二期槽位）", "pdf", "%PDF-1.4", "", true},
		{"未知类型返回 error", "docx", "x", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := extractText(&Document{FileType: tc.ft, Content: tc.content})
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// ---- SplitChunks（spec 06 §2：递归分割六规则；用例按 T3a-f 渐进补入） ----

// TestSplitChunksValidationAndEmpty 非法参数与空输入（spec 06 §2 规则 6）：
// size≤0 / overlap<0 / overlap≥size / 空串 / 全空白 → nil；config 已校验，此处兜底。
func TestSplitChunksValidationAndEmpty(t *testing.T) {
	cases := []struct {
		name    string
		content string
		size    int
		overlap int
	}{
		{"size 为 0", "正文", 0, 0},
		{"size 为负", "正文", -10, 0},
		{"overlap 为负", "正文", 100, -1},
		{"overlap 等于 size", "正文", 100, 100},
		{"overlap 大于 size", "正文", 100, 120},
		{"空串", "", 100, 20},
		{"全空白（空格/换行/制表）", "  \n\t \n  ", 100, 20},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Nil(t, SplitChunks(tc.content, tc.size, tc.overlap))
		})
	}

	t.Run("短文本单块原样返回", func(t *testing.T) {
		assert.Equal(t, []string{"hello 世界"}, SplitChunks("hello 世界", 100, 20))
	})
}

// TestSplitChunksParagraphMerge 段落贪心合并（spec 06 §2 规则 1）：\n\n 切段、
// 分隔符计入合并判据、CRLF 归一、空段丢弃、段落外空白剥离。overlap=0 隔离变量。
func TestSplitChunksParagraphMerge(t *testing.T) {
	cases := []struct {
		name    string
		content string
		size    int
		want    []string
	}{
		{"累计恰好=size 仍并入（分隔符计数）", "AAAA\n\nBBBB\n\nC", 10, []string{"AAAA\n\nBBBB", "C"}},
		{"段落恰好=size 独占一块", "AAAA\n\nBBBB", 4, []string{"AAAA", "BBBB"}},
		{"装不下出块后继续合并", "AAAAAA\n\nBB\n\nCC", 6, []string{"AAAAAA", "BB\n\nCC"}},
		{"CRLF 归一为 LF", "AAAA\r\n\r\nBBBB\r\n\r\nCC", 10, []string{"AAAA\n\nBBBB", "CC"}},
		{"空段丢弃", "A\n\n\n\nB\n\nC", 6, []string{"A\n\nB", "C"}},
		{"段落外空白剥离", "  A  \n\n  B  ", 8, []string{"A\n\nB"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, SplitChunks(tc.content, tc.size, 0))
		})
	}
}

// TestSplitChunksSentenceFallback 单段超长降级句子切（spec 06 §2 规则 2）：边界集
// 全角 。！？；． + 半角 !?;（ASCII '.' 不切——3.14 / e.g. 豁免）、标点归前句、
// 句子贪心合并、降级续片与后续段落同块。
func TestSplitChunksSentenceFallback(t *testing.T) {
	cases := []struct {
		name    string
		content string
		size    int
		want    []string
	}{
		{"句子贪心合并进组", "这是第一句。这是第二句。这是第三句。", 13,
			[]string{"这是第一句。这是第二句。", "这是第三句。"}},
		{"降级续片与后续段落同块", "第一句。第二句。第三句。\n\nBB", 10,
			[]string{"第一句。第二句。", "第三句。\n\nBB"}},
		{"标点归前句（组尾带标点）", "问句？感叹！分号；句号。", 8,
			[]string{"问句？感叹！", "分号；句号。"}},
		{"3.14 与 e.g. 不误切", "Pi is 3.14! Also e.g. fruit!", 17,
			[]string{"Pi is 3.14!", " Also e.g. fruit!"}},
		{"半角 ? ; 也是边界", "Hello? Yes; No", 11,
			[]string{"Hello? Yes;", " No"}},
		{"全角 ． 也是边界", "全角点．也算边界", 5,
			[]string{"全角点．", "也算边界"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, SplitChunks(tc.content, tc.size, 0))
		})
	}
}

// TestSplitChunksHardCut 无标点硬截（spec 06 §2 规则 3）：单句 / 单元仍超 size
// （URL、长串）按 rune 硬截——多字节字符不撕裂，恰好整除不留尾块。
func TestSplitChunksHardCut(t *testing.T) {
	cases := []struct {
		name    string
		content string
		size    int
		want    []string
	}{
		{"URL 长串硬截", "https://example.com/very/long/path/a/b/c/d/e", 10,
			[]string{"https://ex", "ample.com/", "very/long/", "path/a/b/c", "/d/e"}},
		{"恰好整除不留尾块", "abcdefghijklmnopqrst", 10,
			[]string{"abcdefghij", "klmnopqrst"}},
		{"中文按 rune 截不撕裂字节", "一二三四五六七八九十一二三四五六七八九十", 6,
			[]string{"一二三四五六", "七八九十一二", "三四五六七八", "九十"}},
		{"硬截片段与后续句段合块", "aaaaaaaaaabbbb。短。\n\nZ", 10,
			[]string{"aaaaaaaaaa", "bbbb。短。\n\nZ"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, SplitChunks(tc.content, tc.size, 0))
		})
	}
}

// TestSplitChunksFenceAtomic MD 围栏原子保护（spec 06 §2 规则 4）：围栏行 =
// TrimSpace 后以 ``` 开头（容忍缩进与 info string）；块内 \n\n 不切段；围栏自身
// 超长只硬截不走句子切；未闭合围栏其后全部保守视为围栏。
func TestSplitChunksFenceAtomic(t *testing.T) {
	cases := []struct {
		name    string
		content string
		size    int
		want    []string
	}{
		{"围栏内空行不切段", "前\n\n```\na\n\nb\n```\n\n后", 13,
			[]string{"前", "```\na\n\nb\n```", "后"}},
		{"围栏与邻段合并", "A\n\n```\nx\n```\n\nBBBB", 12,
			[]string{"A\n\n```\nx\n```", "BBBB"}},
		{"围栏超长硬截不走句子切", "```\nx = f(a)!\ny = g(b)!\n```", 10,
			[]string{"```\nx = f(", "a)!\ny = g(", "b)!\n```"}},
		{"info string 围栏识别", "```py\nx=1\n\ny=2\n```", 15,
			[]string{"```py\nx=1\n\ny=2\n", "```"}},
		{"未闭合围栏保守整体", "A\n\n```\ncode\n\nmore", 15,
			[]string{"A", "```\ncode\n\nmore"}},
		{"缩进围栏识别", "前\n\n  ```\n  a\n\n  b\n  ```\n\n后", 25,
			[]string{"前\n\n  ```\n  a\n\n  b\n  ```", "后"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, SplitChunks(tc.content, tc.size, 0))
		})
	}
}

// TestSplitChunksOverlapInvariant overlap 后缀不变式（spec 06 §2 规则 5）：除首块
// 外每块前缀 = 上一块尾部 min(overlap, len) 个 rune；块内容 ≤ size、含前缀
// ≤ size+overlap；对各形态（段落 / 句子 / 围栏 / 硬截 / 混排）统一成立。
func TestSplitChunksOverlapInvariant(t *testing.T) {
	t.Run("两段两块的字面尾缀", func(t *testing.T) {
		assert.Equal(t, []string{"AAAA", "AABBBB"}, SplitChunks("AAAA\n\nBBBB", 6, 2))
	})

	fixtures := []struct {
		name    string
		content string
		size    int
		overlap int
	}{
		{"段落形态", "AAAAAA\n\nBB\n\nCC", 6, 2},
		{"句子形态", "这是第一句。这是第二句。这是第三句。", 13, 4},
		{"围栏形态", "前\n\n```\na\n\nb\n```\n\n后", 13, 5},
		{"硬截形态", "https://example.com/very/long/path/a/b/c/d/e", 10, 3},
		{"降级与段落混排", "第一句。第二句。第三句。\n\nBB", 10, 3},
	}
	for _, f := range fixtures {
		t.Run(f.name, func(t *testing.T) {
			raw := SplitChunks(f.content, f.size, 0)
			out := SplitChunks(f.content, f.size, f.overlap)
			require.NotEmpty(t, raw)
			require.Len(t, out, len(raw))
			assert.Equal(t, raw[0], out[0]) // 首块无前缀
			for i := 1; i < len(out); i++ {
				n := min(f.overlap, runeLen(raw[i-1]))
				// 前缀 = 上一块（未应用 overlap 的内容）尾部 n rune
				assert.Equal(t, tailRunes(raw[i-1], n)+raw[i], out[i])
				assert.LessOrEqual(t, runeLen(out[i]), f.size+f.overlap)
				// spec §5 表述的不变式：下一块前 overlap rune == 上一块后 overlap rune
				//（上一块内容长于 overlap 时逐字成立；短于时 min 截断，上面断言已覆盖）
				if runeLen(raw[i-1]) >= f.overlap {
					assert.Equal(t, tailRunes(out[i-1], f.overlap), string([]rune(out[i])[:f.overlap]))
				}
			}
		})
	}
}

// ---- estimateTokens（spec 06 §3：ceil(ASCII/4 + 非ASCII)，cl100k 经验值） ----

func TestEstimateTokens(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    int
	}{
		{"空串", "", 0},
		{"纯 ASCII 整除", "abcdefgh", 2}, // 8/4
		{"纯 ASCII 非整除进位", "abcde", 2}, // ceil(5/4)
		{"单 ASCII 字符", "a", 1},        // ceil(1/4)
		{"纯中文一字一 token", "中文五个字", 5},  // 非ASCII 逐字计
		{"混合", "abc中文", 1 + 2},        // ceil(3/4)=1 + 2
		{"空白换行计入 ASCII", "a b\nc", 2}, // 5 ASCII → ceil(5/4)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, estimateTokens(tc.content))
		})
	}
}
