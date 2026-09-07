# RAG spec 07 · 管道（backend_spec_07_pipeline）

> 状态：**实施 spec**（2026-09-07），8 篇之 07；决策依据见总览 [backend_module_spec.md](backend_module_spec.md)。前置依赖：02（embedder）、04（service/store 骨架 + Upload/Reindex 落 pending）、06（chunker 纯函数）。
> 交付：`service/pipeline.go`——dispatch 调度 + processDocument 状态机串联 + 终态事务 + Recovery + Store 增补管线方法族 + 接线（各一行）。**不变量规则 1（终态事务）的执行者**（定义见 01 §3）。

## 1. dispatch 调度（dispatchIngest）

- `go` + `semaphore.Weighted`（cfg.IngestConcurrency，默认 2）——DB 行即持久队列，无消息队列。
- **`context.WithoutCancel(ctx)`** 脱钩请求 ctx（202 返回后请求 ctx 即取消，直接透传会让 embedding 立刻 abort——最易写错处）但保留 trace 值。
- 抢槽 2s 超时 → 置 failed"系统繁忙"。
- `dispatch func(ctx, docID)` 字段可注入，测试同步直调（不等 goroutine）。

## 2. processDocument（只做串联 + 状态管理，九环节）

环节逻辑独立私有函数（loadDocument / resolveEmbedOptions / embedChunks / buildDocumentChunks / commitReady / markFailed），函数 <50 行；事务纪律：**embedding 永不进事务**：

1. `defer recover()` → failed "internal panic"；
2. `loadDocument`：Get 文档 + KB（not found 只记日志返回——可能已被删）；
3. `MarkDocumentProcessing`（pending→processing，真在做；前端轮询区分"排队中/处理中"）；
4. `extractText(doc)`（06 的解析槽位，一期 pass-through 读 Content）；
5. `SplitChunks`（06 的递归分割；空→failed"文档内容为空"）；
6. `resolveEmbedOptions`：`ResolveLLMConfig(kb.EmbeddingModelID)`（哨兵→failed + 消息）；
7. `embedChunks`：按 EmbedBatchSize 分批 `EmbedStrings`（每向量校验 len==1536，≠→failed）→ `buildDocumentChunks` **全量向量在内存组装** `[]DocumentChunk`（KnowledgeBaseID 冗余、ChunkIndex 自 1、TokenCount=estimateTokens、`pgvector.NewVector`）；累计 prompt_tokens；
8. `commitReady` **终态事务** `WithTx{ 逐批 CreateChunks（多语句单事务）；MarkDocumentReady(id, len(chunks)) }`——不变量规则 1；
9. 成功 `slog.InfoContext` 记 chunks / prompt_tokens（usage 记日志，[embedding_api.md](embedding_api.md) §6）；失败统一 `markFailed`：`MarkDocumentFailed` + `truncateRunes(err, 500)` + `slog.ErrorContext`。

- **批次失败不跳过**：某批 `EmbedStrings` 重试耗尽（embed 层内重试 1 次已尽，见 02 §3）→ **整文档 failed**（error_message 带批次号），不静默丢批——丢块 = 召回缺口，比失败更难发现；用户 reindex 全量重跑（嵌入 ~$0.02/1M，便宜）。
- 内存峰值论证：2MB 文档 ≈ 1600 chunks × 6KB ≈ 10MB，并发 2 ≈ 20-50MB——被 MaxUploadBytes 钉死，容器 512m-1g 内接受（用户已确认）；二期 PDF 换流式分批提交（总览 §3）。

## 3. Store 增补（管线方法族，纯 SQL）

- 状态翻转：`MarkDocumentProcessing(id)` / `MarkDocumentReady(id, chunkCount)` / `MarkDocumentFailed(id, message)`——UPDATE 带 WHERE（id + 可选状态前置条件），RowsAffected 语义记入测试；
- `CreateChunks(ctx, cs []DocumentChunk)`：`db.Create(&cs)` 切片插入 = 多 VALUES（终态事务内逐批调用，每批 ≤ EmbedBatchSize 行）；空切片直返防御；
- `ListIngestingDocuments(ctx)`：`WHERE status IN ('pending','processing')`（partial idx `idx_documents_ingesting` 命中）——Recovery 用。

## 4. Recovery（服务重启自愈）

`Recovery.MarkInterruptedFailed(ctx)`：扫 `ListIngestingDocuments` 置 failed（error_message="服务重启中断，请重新索引"）；终态事务原子性保证残留文档恒无 chunks，无需清理向量；返回 error 仅记 WARN 不阻断启动（不自动重跑，reindex 是显式用户动作）。

## 5. 接线（本篇全部增量代码点）

- `service.New` 改双返回 `(ragapi.KnowledgeBaseService, *Recovery)`（照 provider `(api, Prober)` 先例）；
- `UploadDocument` / `ReindexDocument` 尾部各加一行 dispatch 调用（04 刻意留白的接线点）；
- server.go 装配一行（`MarkInterruptedFailed`）属 08。

## 6. 单测与验收门

| 层 | 重点 |
|---|---|
| service/pipeline_test（**dispatch 注入同步直调**） | `MarkDocumentProcessing` 翻转时机；终态事务断言（CreateChunks 逐批 + MarkDocumentReady(id, 总数) 原子）；**失败矩阵任何一步 → CreateChunks 零调用**（无孤儿：loadDocument 失败 / SplitChunks 空 / ResolveLLMConfig 哨兵 / 维度≠1536 / 某批 embed 失败带批次号）；recover→failed；reindex 接线后 dispatch 被调；Recovery 扫 pending OR processing 置 failed；prompt_tokens 累计与日志字段 |
| store/store_test | 状态翻转方法族 SQL（WHERE 含 id）；CreateChunks 多 VALUES（含 knowledge_base_id/token_count/chunk_index/embedding）；ListIngestingDocuments 命中 partial 条件 |
| 门 | `go test ./internal/rag/... -race -cover` 全绿（≥80%）；端到端验证在 08 |

## 7. 风险（本篇）

1. **`context.WithoutCancel` 必须用**——直接透传请求 ctx 会让 embedding 立刻 abort。
2. 不变量维护责任：任何让文档离开 ready 的新路径必须同事务删 chunks（04 已实现软删/reindex 两处，本篇终态事务是第三处闭环）。
3. embedding 永不进事务；终态事务只含纯 DB 写。
4. 内存峰值被 MaxUploadBytes 钉死；二期放开上限前必须先改流式分批提交。
5. 单写者假设（信号量 + ErrDocumentProcessing）——将来引入并发写文档路径需补乐观锁。
