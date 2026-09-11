-- +goose Up
-- knowledge_bases：切分策略配置（jsonb，一期仅 fixed_length）
ALTER TABLE knowledge_bases ADD COLUMN chunk_strategy jsonb NOT NULL DEFAULT '{}';
COMMENT ON COLUMN knowledge_bases.chunk_strategy IS '切分策略配置（jsonb），type=fixed_length 时含 chunk_size/chunk_overlap/separator；空 {} 降级读全局默认';

-- +goose Down
ALTER TABLE knowledge_bases DROP COLUMN IF EXISTS chunk_strategy;
