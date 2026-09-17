-- +goose Up
-- Agent → workflow 绑定（spec 05）：agents.workflow_id 可空外键。
-- RESTRICT（拍板 A）：被绑定的 workflow 禁删（409 WORKFLOW_IN_USE），与
-- agents.model_id / conversations.agent_id 同款互锁；可空 = 不绑定是常态。
-- 约束显式命名 fk_agents_workflow：service 侧 23503 按约束名分发翻译（§4.4）。
ALTER TABLE agents
    ADD COLUMN workflow_id bigint,
    ADD CONSTRAINT fk_agents_workflow FOREIGN KEY (workflow_id)
        REFERENCES workflows (id) ON DELETE RESTRICT;

CREATE INDEX idx_agents_workflow_id ON agents (workflow_id);

COMMENT ON COLUMN agents.workflow_id IS '绑定的 workflows.id（可空=未绑定；ON DELETE RESTRICT，被绑定的 workflow 删除被挡 409 WORKFLOW_IN_USE）';

-- +goose Down
ALTER TABLE agents DROP COLUMN workflow_id;  -- 列上的索引与 FK 随列级联删除
