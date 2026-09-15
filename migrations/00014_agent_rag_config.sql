-- +goose Up
-- Agent 级 RAG 检索参数：rag_top_k（召回数）/ rag_min_similarity（过滤阈值）。
-- 需求来源：rag_injection_spec.md §2 D5 拍板可配置；chat buildSystemPrompt 读此值替代钉死常量。
-- 默认值 3 / 0.750 保持向后兼容（既有 Agent 行走默认，行为不变）。
ALTER TABLE agents
    ADD COLUMN rag_top_k        smallint        NOT NULL DEFAULT 3      CHECK (rag_top_k BETWEEN 1 AND 20),
    ADD COLUMN rag_min_similarity numeric(4,3)  NOT NULL DEFAULT 0.750  CHECK (rag_min_similarity BETWEEN 0 AND 1);

COMMENT ON COLUMN agents.rag_top_k        IS 'RAG 检索注入取回片段数（1-20，默认 3；chat buildSystemPrompt 读此值）';
COMMENT ON COLUMN agents.rag_min_similarity IS 'RAG 检索注入相似度过滤阈值（0-1，默认 0.750；RetrievedChunk.Similarity = 1 - 余弦距离）';

-- +goose Down
ALTER TABLE agents
    DROP COLUMN rag_top_k,
    DROP COLUMN rag_min_similarity;
