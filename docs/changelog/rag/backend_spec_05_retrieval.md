# RAG spec 05 · 检索（backend_spec_05_retrieval）

> 状态：**实施 spec**（2026-09-07），8 篇之 05；决策依据见总览 [backend_module_spec.md](backend_module_spec.md)。前置依赖：02（embedder）、03（RetrieveReq/RetrievedChunk 契约）、04（service/store/handler 骨架就位）。
> 交付：`Retrieve` 编排（service）+ Store 增补 `SearchChunks` / `GetDocumentMetasByIDs`（含 pgvector 单表召回 SQL）+ handler 端点 11。正确性由 01 的核心不变量保证——**召回 SQL 不手写 status/deleted_at 过滤**。

## 1. service：Retrieve 编排

`Retrieve(ctx, RetrieveReq) ([]RetrievedChunk, error)`：

1. 逐 KB `Get`（任一 404 → `ErrKnowledgeBaseNotFound`）；
2. **静默剔除 disabled**——管理员下架某库，绑它的 Agent 用剩余库继续工作，不报错；剩余为空 → 返回 `[]`（初始化空 slice，不返 nil）；
3. 全部 KB 的 `EmbeddingModelID` 一致，否则 `ErrEmbeddingModelMismatch`（向量空间可比性前提）；
4. `ResolveLLMConfig`（明文凭据，只在调用瞬间存在）→ `EmbedStrings(ctx, opts, []string{query})`（llm 哨兵透传：`ErrEmbeddingUnsupported`/`ErrProviderBusy`/RateLimited 原样上抛）；
5. 返回向量维度 ≠ `RequiredEmbeddingDim`(1536) → 包装 `errs.ErrInternal`（服务端配置错，不暴露细节）；
6. `SearchChunks(kbIDs, vec, TopK, EFSearch)` → `GetDocumentMetasByIDs`（二次小查询）拼 DocumentName；
7. `Similarity = 1 - Distance`；TopK=0 取默认（cfg.TopK）并 clamp 到 [TopKMin, TopKMax]。

## 2. store：增补两个方法

- `GetDocumentMetasByIDs(ctx, ids)`：`SELECT id, name FROM documents WHERE id IN ?`——引用名解析，≤top-k 行，不碰 content。
- `SearchChunks(kbIDs []uint64, query []float32, limit int, efSearch int)`（核心：单表 + 事务内 SET LOCAL）：

  ```sql
  SELECT id, document_id, knowledge_base_id, chunk_index, content, token_count,
         (embedding <=> ?) AS distance
  FROM document_chunks
  WHERE knowledge_base_id IN ?
    AND embedding IS NOT NULL       -- 纯防御：终态事务保证行必带向量
  ORDER BY embedding <=> ?          -- 表达式本体，与 vector_cosine_ops 配对铁律
  LIMIT ?
  ```

  - 正确性不依赖 `status='ready' / deleted_at IS NULL` 过滤——由不变量保证（kb_id 冗余 + 终态事务换来的简化，见 01 §3）。
  - `db.Transaction` 包裹：先 `SET LOCAL hnsw.ef_search = %d`（fmt.Sprintf——**SET 不支持绑定参数**，efSearch 是服务端 config 非用户输入、已 clamp），再 `Raw(sql, v, kbIDs, v, limit).Scan(&hits)`（向量参数传两次；`IN ?` GORM 展开 slice，kbIDs ≤ 10）。
  - Scan 目标 `ChunkHit`（01 定义）；返回 service 前不解析名称。

## 3. handler：端点 11

| # | 方法 + 路径 | 请求参数 | 成功 | 语义 / 主要错误 |
|---|---|---|---|---|
| 11 | POST `/api/v1/knowledge-bases/{id}/retrieve` | body：`query*`(≤1024 rune)、`top_k`(1-20，默认5) | 200 `[]RetrievedChunk` | 检索（非 CRUD 动作子路径，照 test-connection 先例）；KB 404；disabled 静默剔除（全空返回 `[]`）；混嵌入模型 400 `EMBEDDING_MODEL_MISMATCH`；llm 哨兵：`ErrEmbeddingUnsupported` 400 / `ErrProviderBusy` 503 / RateLimited 429 |

- `retrieve`：BindJSON（query/top_k）+ BindUri（kbId → 程序内填 `RetrieveReq.KBIDs`，复用跨 KB 契约）；失败路径进 `failRag` 映射（04 已建，本篇补 llm 三个哨兵分支）。

## 4. 单测与验收门

| 层 | 重点 |
|---|---|
| service/service_test | stubStore + stub embedder：**剔除 disabled（全 disabled → 空 `[]`）**；任一 KB 404；模型不一致→Mismatch；EmbedStrings 哨兵透传；维度≠1536→ErrInternal；TopK 默认与 clamp；**similarity = 1 - distance**；二次查询拼 DocumentName（悬空→""）；kb_ids 数量边界 |
| store/store_test | **SearchChunks 事务序列**（ExpectBegin → `SET LOCAL hnsw.ef_search = 80` → 单表 Query（断言 SQL 无 JOIN / 无 documents 字样 / 向量参数两次）→ Commit）；GetDocumentMetasByIDs 的 IN 展开 |
| handler/handler_test | retrieve 200 数组信封（含空 `[]`）；query 缺失 400；top_k 越界 400；哨兵→状态码（Mismatch 400 / Unsupported 400 / Busy 503）；单 KB 路径参数进 KBIDs |
| 门 | 三层测试绿；与 04 合并后 service 覆盖率 ≥80%（`go test ./internal/rag/... -race -cover`） |
