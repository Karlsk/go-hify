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
