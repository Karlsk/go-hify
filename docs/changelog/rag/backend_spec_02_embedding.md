# RAG spec 02 · embedding 能力（backend_spec_02_embedding）

> 状态：**实施 spec**（2026-09-07），8 篇之 02；决策依据见总览 [backend_module_spec.md](backend_module_spec.md)。前置依赖：无硬依赖（可与 01 并行）；消费方 = spec 05（查询向量化）与 spec 07（入库向量化）。
> 交付：`internal/platform/llm/embed.go`——embedding 统一薄 adapter。**独立单测**（httptest 按 kind 打桩，不依赖 rag 任何代码）。

## 1. 类型与构造

- `EmbedOptions{Kind, BaseURL, APIKey, Model}`、`EmbedResult{Vectors [][]float32, PromptTokens int}`、哨兵 `ErrEmbeddingUnsupported`（claude 无 embedding API，HTTP 400）。
- `Embedder`：`NewEmbedder(t http.RoundTripper)`（内部 `NewJSONClient(t, 5s)`——复用 llm 包既有 JSON client）。
- 包内常量：`embedTimeout=5s`、`embedBulkhead=4`（**独立信号量，不占 chat bulkhead**）、`embedAcquireWait=2s`（超时→既有 `llm.ErrProviderBusy`）、`embedMaxRetries=1`、`embedMaxInputs=2048`。

## 2. 按 kind 分发（照 prober `probeTarget` 形态）

| kind | 端点 | 鉴权 | 响应解析 |
|---|---|---|---|
| openai_compatible | `POST {base}/embeddings` | Bearer | `data[].index` **归位**（不假设返回顺序）+ `usage.prompt_tokens` |
| ollama | `POST {base}/api/embed` | 无 | `{"embeddings"}` 数组 |
| gemini | `POST {base}/models/{model}:batchEmbedContents` | x-goog-api-key | `embeddings[].values` |
| claude | — | — | 直接 `ErrEmbeddingUnsupported`，请求都不发 |

- `EmbedStrings(ctx, opts, inputs []string) (EmbedResult, error)`：`len(inputs) > embedMaxInputs` → 参数错误拒绝（分批是调用方的事）。
- 槽位=4 论证：入库并发默认 2 文档 × 每文档 1 在途批 + 查询侧 1-2 并发 = 峰值 4；embedding 200ms-1s 无流式，余量充足。

## 3. 重试纪律（总预算模型）

单一 5s ctx 包住整个重试循环（**含 sleep**）——最坏总时长硬封顶 5s，不学 chat 的每次 attempt 独立超时（embedding 定位短平快，拖到 10s+ 违背短超时初衷）；sleep 用 `select { <-timer / <-ctx.Done() }`，ctx 到期立即放弃剩余尝试；槽位**持有跨重试**（`defer` 释放，重试不额外占槽）；抢槽 2s 拿不到 → `ErrProviderBusy` **不重试**（fail-fast 是设计，不是缺陷）。

| 错误 | 重试（共 2 次尝试） | 退避 |
|---|---|---|
| 429 + Retry-After | ✅ | `min(Retry-After, 2s)`；**>2s 直接放弃返回 RateLimited**——内部工具不等长冷却 |
| 429 无头 / 解析失败 | ✅ | 300ms 固定 |
| 5xx（含 529） | ✅ | 300ms 固定（单次重试、并发 ≤4，无需满抖动） |
| 网络错误（拒绝/reset/DNS） | ✅ | 300ms |
| 400/401/403/404 | ❌ | 模型名错 / Key 错 / input 超限，重试无意义 |
| ctx 取消/超时 | ❌ | 调用方已放弃 |
| kind=claude | ❌ | `ErrEmbeddingUnsupported` |

- **幂等论证**：embedding 是确定性映射、无服务端副作用，向量落库在终态事务一次完成——重试期间不存在半写状态；这是它与 chat 流式「首 token 后绝不重试」的本质区别。
- 为什么只重试 1 次（chat 是 2 次）：上层已有两级粗粒度重试——入库侧「批失败 → 文档 failed → reindex 全量重跑」（spec 07），查询侧「失败 → 降级普通对话」（chat 集成，下一阶段定稿）。

## 4. 单测与验收门

| 项 | 内容 |
|---|---|
| embed_test（httptest 按 kind 打桩） | openai **乱序 index 归位**+usage 计数；ollama / gemini 报文形态；claude→`ErrEmbeddingUnsupported`；**429+Retry-After 重试一次成功**；429 无头重试；500 重试；**400 不重试**；**5s 总预算含 sleep**（sleep 中 ctx 到期放弃剩余尝试）；并发打满 4 槽→`ErrProviderBusy`；input > 2048 拒；返回维度校验属调用方（05/07），本层只透传 |
| 门 | `go test ./internal/platform/llm/ -run Embed -race` 全绿；不触碰 rag 包 |
