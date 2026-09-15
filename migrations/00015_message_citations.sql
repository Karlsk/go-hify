-- +goose Up
-- assistant 消息的 RAG 引用来源：chat buildSystemPrompt 检索命中的文档
-- （按 document_id 去重、相似度取最高），SSE citations 事件 + 历史消息共用。
-- 与 tool_calls 同构（jsonb NOT NULL DEFAULT '[]'，常量默认瞬时回填存量行）。
ALTER TABLE messages
    ADD COLUMN citations jsonb NOT NULL DEFAULT '[]';

COMMENT ON COLUMN messages.citations IS 'assistant 消息的 RAG 引用来源 [{document_id, document_name, similarity}]（jsonb）；user/tool 行恒 []，检索注入的文档清单（chat buildSystemPrompt 产出）';

-- +goose Down
ALTER TABLE messages
    DROP COLUMN citations;
