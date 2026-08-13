-- +goose Up
-- executions：运行日志（platform/logging，append-only，按月分区）
-- 唯一「建表即分区」的表：时间序列 + 90 天保留（drop 旧分区瞬间完成）。
-- 分区自动化由 deploy/backup/partition_maintenance.sql 每日执行（建当月/下月分区、删 >90 天分区）。
CREATE TABLE executions (
    id                bigint GENERATED ALWAYS AS IDENTITY,
    conversation_id   bigint,
    model_id          bigint,
    model_name        text NOT NULL DEFAULT '',
    input             jsonb NOT NULL DEFAULT '{}',
    output            jsonb NOT NULL DEFAULT '{}',
    tool_chain        jsonb NOT NULL DEFAULT '[]',
    prompt_tokens     bigint NOT NULL DEFAULT 0,
    completion_tokens bigint NOT NULL DEFAULT 0,
    total_tokens      bigint NOT NULL DEFAULT 0,
    duration_ms       integer NOT NULL DEFAULT 0,
    finish_reason     text NOT NULL DEFAULT '',
    error_class       text CHECK (error_class IN ('Timeout', 'RateLimited', 'Overloaded', 'Network', 'InvalidRequest', 'Auth', 'ProviderDown')),
    created_at        timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (id, created_at)  -- 分区键必须在主键里
) PARTITION BY RANGE (created_at);

-- 首月分区（当月）；后续月份由 partition_maintenance.sql 维护
CREATE TABLE executions_2026_08 PARTITION OF executions
    FOR VALUES FROM ('2026-08-01') TO ('2026-09-01');

-- 父表建索引自动传播到所有分区
CREATE INDEX idx_executions_conv_time ON executions (conversation_id, created_at);
CREATE INDEX idx_executions_model ON executions (model_id);

COMMENT ON TABLE executions IS '每次 LLM 调用一行：输入/输出/工具链/token/耗时/错误类，排障唯一线索；按月分区保留 90 天';
COMMENT ON COLUMN executions.conversation_id IS '所属会话（弱引用不建 FK：90 天保留期内不得阻碍会话删除）；workflow 节点调用为空';
COMMENT ON COLUMN executions.model_id IS '所用模型（弱引用不建 FK，同 conversation_id）；配合 model_name 冗余快照';
COMMENT ON COLUMN executions.model_name IS '冗余存模型名方便排障（模型删除后记录仍可读），写入时同步';
COMMENT ON COLUMN executions.tool_chain IS '工具调用链（jsonb）：每轮 {tool, args, result}，与 messages.tool_calls 互补';
COMMENT ON COLUMN executions.error_class IS 'Timeout/RateLimited/Overloaded/Network/InvalidRequest/Auth/ProviderDown，见 platform/llm 错误分类；成功为 NULL';
COMMENT ON COLUMN executions.finish_reason IS '结束原因（如 stop/length/tool_use）；失败流为空串';

-- +goose Down
DROP TABLE IF EXISTS executions;
