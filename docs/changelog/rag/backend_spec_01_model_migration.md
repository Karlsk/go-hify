# RAG spec 01 · 模型与数据迁移（backend_spec_01_model_migration）

> 状态：**实施 spec**（2026-09-07），8 篇之 01；决策依据与全量决策表见总览 [backend_module_spec.md](backend_module_spec.md)。前置依赖：无（起点）。
> 交付：增量迁移 00011 + `service/model.go` GORM 实体 + 平台地基三件（config.RagCfg / respond.Accepted / cache.NameRag）+ go get pgvector-go。**核心不变量在本篇定义**——后续 CRUD（04）、管线（07）spec 是执行者。

## 1. go.mod

`go get github.com/pgvector/pgvector-go@v0.4.1`（Valuer/Scanner 直通 `[]float32`，见 [go_gorm_integration.md](go_gorm_integration.md)）。

## 2. migrations/00011_rag_schema_review.sql

增量迁移，只增不改 00004/00005：

```sql
-- +goose Up
-- knowledge_bases：启用开关（下架不删数据）
ALTER TABLE knowledge_bases ADD COLUMN enabled boolean NOT NULL DEFAULT true;
COMMENT ON COLUMN knowledge_bases.enabled IS '启用开关：false=检索范围静默剔除（service 层过滤），管理面仍可见可编辑';

-- documents：文件元数据 + 进度字段 + 四态状态机
ALTER TABLE documents ADD COLUMN file_type text NOT NULL DEFAULT 'txt';
ALTER TABLE documents ADD CONSTRAINT ck_documents_file_type CHECK (file_type IN ('txt', 'md'));
ALTER TABLE documents ADD COLUMN file_size bigint NOT NULL DEFAULT 0;
ALTER TABLE documents ADD COLUMN error_message text NOT NULL DEFAULT '';
ALTER TABLE documents ADD COLUMN chunk_count integer NOT NULL DEFAULT 0;
ALTER TABLE documents DROP CONSTRAINT documents_status_check;
ALTER TABLE documents ADD CONSTRAINT ck_documents_status
    CHECK (status IN ('pending', 'processing', 'ready', 'failed'));
ALTER TABLE documents ALTER COLUMN status SET DEFAULT 'pending';
DROP INDEX idx_documents_processing;
CREATE INDEX idx_documents_ingesting ON documents (status)
    WHERE status IN ('pending', 'processing');
COMMENT ON COLUMN documents.file_type IS '文件类型（取上传扩展名，非 mime——可伪造）；二期加 pdf 只改 CHECK';
COMMENT ON COLUMN documents.file_size IS '文件字节数（上传时记录，展示/统计用）';
COMMENT ON COLUMN documents.error_message IS '入库失败原因（status=failed 时非空；应用层截断 500 字符）';
COMMENT ON COLUMN documents.chunk_count IS '分块数量（终态事务与 ready 原子同写；pending/processing/failed 恒 0，单写者保证见 spec 07）';
COMMENT ON COLUMN documents.status IS '四态：pending(已落库未开工)/processing(分块向量化中)/ready(可召回)/failed；前端轮询此字段显示进度';

-- chunks → document_chunks：改名 + 冗余 kb_id + token_count + chunk_index
ALTER TABLE chunks RENAME TO document_chunks;
ALTER TABLE document_chunks RENAME COLUMN seq TO chunk_index;
ALTER TABLE document_chunks ADD COLUMN knowledge_base_id bigint NOT NULL DEFAULT 0;
ALTER TABLE document_chunks ADD COLUMN token_count integer NOT NULL DEFAULT 0;
UPDATE document_chunks dc SET knowledge_base_id = d.knowledge_base_id
  FROM documents d WHERE d.id = dc.document_id AND dc.knowledge_base_id = 0;
ALTER TABLE document_chunks ADD CONSTRAINT fk_document_chunks_kb
    FOREIGN KEY (knowledge_base_id) REFERENCES knowledge_bases (id) ON DELETE RESTRICT;
CREATE INDEX idx_document_chunks_kb ON document_chunks (knowledge_base_id);
ALTER INDEX idx_chunks_document RENAME TO idx_document_chunks_document;
ALTER INDEX idx_chunks_embedding RENAME TO idx_document_chunks_embedding;
COMMENT ON COLUMN document_chunks.knowledge_base_id IS '冗余 KB 归属：文档不可换 KB（无该 API 且语义禁止），确定不变派生值；检索单表过滤免 JOIN';
COMMENT ON COLUMN document_chunks.token_count IS '估算值（ceil(ASCII/4+非ASCII)），非 API 精确计数；供 chat 注入的上下文预算参考';

-- +goose Down
ALTER INDEX idx_document_chunks_embedding RENAME TO idx_chunks_embedding;
ALTER INDEX idx_document_chunks_document RENAME TO idx_chunks_document;
DROP INDEX IF EXISTS idx_document_chunks_kb;
ALTER TABLE document_chunks DROP CONSTRAINT IF EXISTS fk_document_chunks_kb;
ALTER TABLE document_chunks DROP COLUMN IF EXISTS token_count;
ALTER TABLE document_chunks DROP COLUMN IF EXISTS knowledge_base_id;
ALTER TABLE document_chunks RENAME COLUMN chunk_index TO seq;
ALTER TABLE document_chunks RENAME TO chunks;
DROP INDEX IF EXISTS idx_documents_ingesting;
CREATE INDEX idx_documents_processing ON documents (status) WHERE status = 'processing';
ALTER TABLE documents ALTER COLUMN status SET DEFAULT 'processing';
ALTER TABLE documents DROP CONSTRAINT IF EXISTS ck_documents_status;
ALTER TABLE documents ADD CONSTRAINT documents_status_check
    CHECK (status IN ('processing', 'ready', 'failed'));
ALTER TABLE documents DROP COLUMN IF EXISTS chunk_count;
ALTER TABLE documents DROP COLUMN IF EXISTS error_message;
ALTER TABLE documents DROP COLUMN IF EXISTS file_size;
ALTER TABLE documents DROP CONSTRAINT IF EXISTS ck_documents_file_type;
ALTER TABLE documents DROP COLUMN IF EXISTS file_type;
ALTER TABLE knowledge_bases DROP COLUMN IF EXISTS enabled;
```

## 3. 核心不变量（定义处；执行者在 04/07）

> **`document_chunks` 有行 ⟺ 所属文档 `status='ready'` 且未软删。**

三条维护规则，任何新增代码路径不得例外：

1. **终态事务**（执行：spec 07）：入库全部向量在内存组装完成后，一个事务内批量 INSERT + `MarkDocumentReady(id, N)` 原子翻转——事务前任何失败 = 零 chunks，无孤儿向量；
2. **软删文档 ⇒ 同事务硬删其 chunks**（执行：spec 04）：文档行软删保底可恢复元信息+原文，向量物理删除省 HNSW 内存；恢复 = 重新 reindex（~$0.02/1M tokens 可忽略）；
3. **reindex ⇒ 事务内删 chunks + 置 pending（清 error_message，chunk_count=0）** 后重跑 pipeline（执行：spec 04 事务重置 + spec 07 dispatch）。

单写者保证（chunk_count / chunks 无需乐观锁）：pipeline 信号量串行 + reindex 被 `ErrDocumentProcessing` 挡并发——不存在两个 goroutine 写同一文档的窗口。

## 4. service/model.go（GORM 实体，模块私有）

- `KnowledgeBase`（embed `db.BaseMutable`；**+Enabled bool**；无软删，删除即硬删）。
- `Document`（embed `db.BaseSoftDelete`；Status 常量 `StatusPending/StatusProcessing/StatusReady/StatusFailed`；**+FileType/FileSize/ErrorMessage/ChunkCount**）。
- `DocumentChunk`（embed `db.BaseAppendOnly`；`DocumentID/KnowledgeBaseID/ChunkIndex/Content/TokenCount` + `Embedding pgvector.Vector \`gorm:"type:vector(1536)"\` 声明性标注，DDL 在 migrations）；`TableName() = "document_chunks"`。
- `ChunkHit`（Raw Scan 目标：id/document_id/knowledge_base_id/chunk_index/content/token_count/distance——**无 name，名称走二次查询**）。
- 纪律：字段一律不加 gorm default tag；显式 `TableName()`；model 不打 json tag（序列化是 api/schema 的事）。

## 5. 平台地基三件

- **platform/config**：`RagCfg`——

  | 字段 | 默认 | env |
  |---|---|---|
  | ChunkSize | 500 | `RAG_CHUNK_SIZE` |
  | ChunkOverlap | 80 | `RAG_CHUNK_OVERLAP` |
  | EmbedBatchSize | 32 | `RAG_EMBED_BATCH_SIZE` |
  | TopK | 5 | `RAG_TOP_K` |
  | EFSearch | 80 | `RAG_EF_SEARCH` |
  | MaxUploadBytes | 2MB | `RAG_MAX_UPLOAD_BYTES` |
  | IngestConcurrency | 2 | `RAG_INGEST_CONCURRENCY` |

  `mustValidate` 非法组合 panic（fail-fast，照 PROVIDER_MASTER_KEY 惯例）：ChunkSize≤0 / Overlap<0 / Overlap≥ChunkSize / EmbedBatchSize≤0 / TopK 越界 / EFSearch≤0 / MaxUploadBytes≤0 / IngestConcurrency≤0。
- **platform/respond**：加 `Accepted(c, data)`（202，与 Created 同构，no-store）。
- **platform/cache**：`NameRag = "rag-cache"` 常量 + DefaultConfig 注册（TTL 30min）。

## 6. 单测与验收门

| 项 | 内容 |
|---|---|
| platform/config 测试 | RagCfg 默认值钉死 + 非法组合 panic（t.Setenv 逐项） |
| 迁移验证 | `make migrate-up` → 00011 applied；`\d document_chunks` 确认改名+新列；`make migrate-down` 1 步可回滚（dev 库无数据，空跑安全） |
| 门 | `go build ./...` 过 + config 测试绿；本篇无业务行为，不动 handler/service 编排 |
