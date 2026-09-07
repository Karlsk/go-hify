# OpenAI Embedding API 参考（embedding_api）

> 状态：**调研记录**（2026-09-07，经 web 检索确认官方口径未变）：端点/报文、与 Chat Completion 差异、模型选择、限制与坑。Hify 接入路径：provider 模块统一管 Key 与端点（models 表已预留 `capability=embedding` + `embedding_dim`，见 [provider/db_model.md](../provider/db_model.md)）；Go 侧向量落库见 [go_gorm_integration.md](go_gorm_integration.md)。
>
> 参考来源：[OpenAI 定价页](https://developers.openai.com/api/docs/pricing)、[Embeddings 指南](https://developers.openai.com/api/docs/guides/embeddings)、[社区限流讨论](https://community.openai.com/t/rate-limit-reached-with-large-documents/358525)。

## 1. 端点与报文

独立端点：`POST https://api.openai.com/v1/embeddings`——与 Chat Completion 是**两个端点**，不共用。

```bash
curl https://api.openai.com/v1/embeddings \
  -H "Authorization: Bearer $OPENAI_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "text-embedding-3-small",
    "input": ["Hify 怎么创建 Agent？", "退货政策是什么？"]
  }'
```

`input` 传**字符串数组即批量**（一次 embedding 多个 chunk，入库的正确姿势）。响应：

```jsonc
{
  "object": "list",
  "data": [
    { "object": "embedding", "index": 0, "embedding": [0.0123, -0.0456, ...] },
    { "object": "embedding", "index": 1, "embedding": [...] }
  ],
  "model": "text-embedding-3-small",
  "usage": { "prompt_tokens": 25, "total_tokens": 25 }
}
```

`data[].index` 对应输入顺序；`usage.prompt_tokens` 计入预算护栏与 executions 日志。

## 2. Go 调用（标准库；真实项目走 provider + eino 适配）

```go
type embedReq struct {
    Model string   `json:"model"`
    Input []string `json:"input"`
}
type embedResp struct {
    Data []struct {
        Index     int       `json:"index"`
        Embedding []float32 `json:"embedding"`
    } `json:"data"`
    Usage struct {
        PromptTokens int `json:"prompt_tokens"`
    } `json:"usage"`
}

body, _ := json.Marshal(embedReq{Model: "text-embedding-3-small", Input: chunks})
req, _ := http.NewRequestWithContext(ctx, "POST",
    "https://api.openai.com/v1/embeddings", bytes.NewReader(body))
req.Header.Set("Authorization", "Bearer "+apiKey)
req.Header.Set("Content-Type", "application/json")

resp, err := http.DefaultClient.Do(req)
if err != nil {
    return nil, fmt.Errorf("embed: %w", err)
}
defer resp.Body.Close()

if resp.StatusCode != http.StatusOK { // 读 body 里 error.message；429 按 Retry-After 退避
    ...
}
var out embedResp
if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
    return nil, err
}
```

## 3. 与 Chat Completion 的区别

| 维度 | Chat Completion | Embeddings |
|---|---|---|
| 端点 | `/v1/chat/completions` | `/v1/embeddings` |
| 输入 | `messages[]`（system/user/assistant 角色） | `input`（字符串 / 字符串数组） |
| 输出 | 生成的文本，逐 token 流式 | 固定长度向量（1536/3072 float），一次性返回 |
| 确定性 | 概率采样（temperature、top_p） | 确定性映射——同样输入永远同样输出，无采样参数 |
| 流式 | SSE | ❌ 没有，也不需要 |
| usage | prompt + completion tokens | **只有 prompt tokens** |
| 价格 | GPT 级 | $0.02/1M tokens（3-small） |
| RAG 里角色 | 生成回答（读侧） | 入库向量化 + 查询向量化（写读两侧） |

一句话：**Chat 是"写文章"，Embedding 是"查字典"**。

## 4. 模型选择

| 模型 | 维度 | 价格 / 1M tokens | 建议 |
|---|---|---|---|
| `text-embedding-3-small` | 1536（可 `dimensions` 缩小） | $0.02（Batch ~$0.01） | **默认选择**：100 万字手册全量入库约 ¥1 量级 |
| `text-embedding-3-large` | 3072（可缩小） | $0.13（Batch ~$0.065） | 精度略高，存储 ×2、HNSW 内存 ×2；内部文档场景增益不明显 |
| `text-embedding-ada-002` | 1536 固定 | $0.10 | 2022 遗留模型，更贵更弱，新项目不用 |

## 5. 限制与坑

1. **每条输入 ≤ 8191 token**，超了直接报错——Hify 固定长度分块（几百字）远在安全区；这也是"分块"存在的物理原因之一。
2. **批量数组条目上限 2048 条**、整批 token 总量受限——大批量入库按几百条一批最稳，配合失败重试。
3. **维度与模型一经使用即冻结**：换模型、改 `dimensions`，新旧向量语义空间不兼容，必须全量 reindex（`/documents/{id}/reindex` 存在的理由）。`dimensions` 参数（MRL 缩维省内存）一期不开——少一个自由度，排障少一个变量。
4. **限流按组织层级、与 chat 分开计**（RPM/TPM）；大批量一次性灌库可走 Batch API（半价 + 不占在线限流额度）。
5. `encoding_format: "base64"` 响应体积减半，量大时值得开（Go 侧解码回 `[]float32`）。
6. v3 系向量已归一化（模长 1），余弦与内积等价——pgvector 用 `vector_cosine_ops` 即可。
7. **无流式**：调用短平快（~200ms-1s），超时设短——CLAUDE.md 已定：embedding **独立槽位**（不挤占 chat 的 bulkhead）+ **5s 短超时** + **失败降级**为普通对话（不命中缓存直接走 LLM），缓存/召回不做成硬依赖。

## 6. 落到 Hify

- **接入**：provider 模块统一接入（models 表 `capability=embedding` + `embedding_dim` 已预留），走 eino embedding 适配器；Key 加密存储。
- **入库**：解析 → 分块 → 批量 embedding（数组请求）→ 多 VALUES INSERT 进 chunks。
- **查询**：单条 embedding → `<=>` 召回；CLAUDE.md 已定"RAG 召回与语义缓存共享同一 query 向量，一次 embedding 喂两处"——查询路径延迟优化的关键。
- **成本护栏**：`usage.prompt_tokens` 进 executions 日志与 budget 计数（embedding 成本相对 chat 生成几乎可忽略，但照计数）。
