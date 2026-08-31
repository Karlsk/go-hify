-- +goose Up
-- pgvector 扩展（幂等）：vector 列类型依赖；扩展是库级对象，新库必须先建
CREATE EXTENSION IF NOT EXISTS vector;

-- documents：上传文档（rag 模块，软删除）
CREATE TABLE documents (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    knowledge_base_id bigint NOT NULL REFERENCES knowledge_bases (id) ON DELETE RESTRICT,
    name              text NOT NULL,
    content           text NOT NULL,
    status            text NOT NULL DEFAULT 'processing' CHECK (status IN ('processing', 'ready', 'failed')),
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    deleted_at        timestamptz
);

CREATE INDEX idx_documents_kb ON documents (knowledge_base_id);
-- partial：只索引处理中的文档（异步分块/向量化任务的扫描队列）
CREATE INDEX idx_documents_processing ON documents (status) WHERE status = 'processing';
-- 软删除过滤：KB 文档列表只列活跃文档
CREATE INDEX idx_documents_active ON documents (knowledge_base_id, id) WHERE deleted_at IS NULL;

COMMENT ON TABLE documents IS '上传文档：TXT/MD 元信息 + 原文（大文本自动 TOAST，查询不要 SELECT *）；软删除（deleted_at）';
COMMENT ON COLUMN documents.status IS '处理状态：processing（分块/向量化中）/ ready（可召回）/ failed';
COMMENT ON COLUMN documents.content IS '文档原文；分块在 chunks 表';

-- chunks：固定长度分块 + pgvector 向量（rag 模块，append-only）
CREATE TABLE chunks (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    document_id bigint NOT NULL REFERENCES documents (id) ON DELETE CASCADE,
    seq         integer NOT NULL,
    content     text NOT NULL,
    embedding   vector(1536),
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_chunks_document ON chunks (document_id, seq);
-- HNSW 向量索引：建表即建（m/ef_construction 为 pgvector 推荐默认；ops 用 cosine 与嵌入模型一致）
CREATE INDEX idx_chunks_embedding ON chunks
    USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);

COMMENT ON TABLE chunks IS '文档固定长度分块：pgvector 向量列 + HNSW 索引，RAG 召回走这张表（append-only）';
COMMENT ON COLUMN chunks.seq IS '分块在文档内的顺序号，自 1 起';
COMMENT ON COLUMN chunks.embedding IS '向量维度 1536（OpenAI text-embedding-3-small）；换嵌入模型需重建本表与索引（CLAUDE.md pgvector 索引规范）';

-- +goose Down
DROP TABLE IF EXISTS chunks;
DROP TABLE IF EXISTS documents;
