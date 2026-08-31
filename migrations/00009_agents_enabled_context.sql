-- +goose Up
-- agents 补两列：启用开关（区别于软删下架——停用保留配置且新会话被拒）与上下文轮数（chat 引擎读）
ALTER TABLE agents ADD COLUMN enabled boolean NOT NULL DEFAULT true;
ALTER TABLE agents ADD COLUMN max_context_turns int NOT NULL DEFAULT 10;

COMMENT ON COLUMN agents.enabled IS '是否启用：false=停用（保留配置，新会话被拒），区别于软删下架';
COMMENT ON COLUMN agents.max_context_turns IS '多轮对话携带的最大历史轮数，默认 10';

-- +goose Down
ALTER TABLE agents DROP COLUMN max_context_turns;
ALTER TABLE agents DROP COLUMN enabled;
