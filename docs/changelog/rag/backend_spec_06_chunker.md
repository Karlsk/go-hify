# RAG spec 06 · 分块与解析（backend_spec_06_chunker）

> 状态：**实施 spec**（2026-09-07），8 篇之 06；决策依据见总览 [backend_module_spec.md](backend_module_spec.md)。前置依赖：无硬依赖（**纯函数，零外部交互，可独立先行开发与单测**——这正是单独成篇的原因）；消费方 = spec 07 管线。
> 交付：`internal/rag/service/chunker.go`——解析槽位 + 递归分割 + token 估算，全部纯函数。

## 1. extractText —— 解析环节槽位

`extractText(doc *Document) (string, error)`：一期 txt/md 读 `doc.Content` 原样返回，~10 行；二期换文件卷 + PDF 提取实现，**签名不变**（管道形状即最终形状）。上传边界（BOM/编码/首尾空白规范化）已在 api Validate 完成，这里不再清洗。

## 2. SplitChunks —— 递归分割（定稿）

`SplitChunks(content string, size, overlap int) []string`——LangChain `RecursiveCharacterTextSplitter` 的 Go 等价物，~150-230 行：

1. **段落贪心合并**：按 `\n\n` 切段（CRLF 归一、空段丢弃），连续段落累计 ≤ size 即入块，装不下就出块；
2. **单段超长降级**：按句子边界切（中英标点 `。！？；．!?;`，小数点/缩写豁免——`3.14`、`e.g.` 不误切），句子贪心合并；
3. **无标点硬截**：单句仍 > size（URL/长串）按 rune 硬截；
4. **MD 围栏原子保护**：预扫描 ``` 围栏块标记为不可分割单元（块内 `\n\n` 不触发切段），围栏块自身超长才硬截——MD 是主要语料，代码块切散伤召回；
5. **overlap**：下一块前缀 = 上一块尾部 overlap 个 rune（字符尾缀实现，可断言）；
6. 尺寸单位 rune（500/80）；空/全空白→nil；非法参数（size≤0、overlap<0、overlap≥size）→nil（config 已校验，兜底不可达）。

否决记录：tiktoken 精确 token（词汇表 2-4MB、cl100k 绑 OpenAI 系、跨 provider 不通用——估算 ±20% 下"精确 512"是假精度）；固定滑窗（用户升级为递归）。

## 3. estimateTokens —— token 估算（纯函数）

`estimateTokens(content string) int = ceil(ASCII字符数/4 + 非ASCII字符数)`（cl100k 经验值：英文 ≈4 char/token、中文 ≈1-1.5）。写入 `DocumentChunk.TokenCount`（07 组装时调用）。embedding API 只有批量级 usage、无按条计数——估算列够用（±20% 对上下文预算无碍）。

## 4. 待办（实施时）

CLAUDE.md「降级做」中"固定长度分块"表述与递归分割定稿有偏差——实施时经用户确认后顺手更新。

## 5. 单测与验收门

| 项 | 内容 |
|---|---|
| service/chunker_test（纯函数表驱动，**先写测试**） | 空串/全空白→nil；非法参数→nil；**段落合并边界**（恰好=size / 装不下出块）；**单段超长降级句子**；**无标点硬截**（URL 长串）；**围栏原子**（围栏内 `\n\n` 不切段；围栏自身超长硬截）；**句子边界不误切**（`3.14` / `e.g.`）；CRLF 归一；**overlap 后缀不变式**（chunk[i+1] 前 overlap rune == chunk[i] 后 overlap rune）；`extractText` pass-through；`estimateTokens` 公式（纯 ASCII / 纯中文 / 混合） |
| 门 | `go test ./internal/rag/service/ -run Chunks -race` 全绿；零 goroutine / 零 IO，测试即文档 |
