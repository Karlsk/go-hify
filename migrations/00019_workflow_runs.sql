-- +goose Up
-- 执行轨迹两层表（spec 06）：append-only 收尾统一写——执行结束一次性落库，
-- 无 RUNNING 可变态（决策 #14）。
CREATE TABLE workflow_runs (
    id              bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    workflow_id     bigint      NOT NULL,               -- 弱引用 workflows(id)，不建 FK（见设计注记）
    workflow_name   text        NOT NULL DEFAULT '',    -- 名称快照：workflow 删除后轨迹仍可读
    trigger_source  text        NOT NULL CHECK (trigger_source IN ('console','chat')),
    is_trial        boolean     NOT NULL DEFAULT false,  -- 试运行标记（?trial=true，O3）：区分测试与真实流量
    conversation_id bigint,                              -- chat 触发时的弱引用（可空；console 为 NULL）
    message_id      bigint,                              -- 同上
    trace_id        text        NOT NULL DEFAULT '',    -- 对齐 platform/traceid 日志链
    status          text        NOT NULL CHECK (status IN ('succeeded','failed')),
    input           jsonb       NOT NULL DEFAULT '{}',  -- 执行入参（截断后）
    output          text        NOT NULL DEFAULT '',    -- 终稿（截断后）
    error_node      text        NOT NULL DEFAULT '',    -- 失败节点 key（成功 = ''）
    error_msg       text        NOT NULL DEFAULT '',
    duration_ms     int         NOT NULL DEFAULT 0,
    started_at      timestamptz NOT NULL,               -- 执行起点；created_at = 收尾写入时刻
    created_at      timestamptz NOT NULL DEFAULT now()
);

-- 真实查询路径：按 workflow 列运行历史（最新优先）
CREATE INDEX idx_workflow_runs_wf_created ON workflow_runs (workflow_id, created_at DESC);

CREATE TABLE workflow_node_runs (
    id          bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id      bigint      NOT NULL REFERENCES workflow_runs (id) ON DELETE CASCADE,
    seq         int         NOT NULL,                   -- 执行序号：回放顺序唯一事实源（UNIQUE 兜底）
    node_key    text        NOT NULL,
    node_type   text        NOT NULL,
    status      text        NOT NULL CHECK (status IN ('succeeded','failed')),
    input       jsonb       NOT NULL DEFAULT '{}',      -- 本节点入参摘要（截断），非 ctx 全量快照
    output      jsonb       NOT NULL DEFAULT '{}',      -- 本节点自己的输出（截断）
    error_msg   text        NOT NULL DEFAULT '',
    duration_ms int         NOT NULL DEFAULT 0,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (run_id, seq)
);

-- 真实查询路径：按 run 取全部节点轨迹（seq 升序回放）
CREATE INDEX idx_workflow_node_runs_run_id ON workflow_node_runs (run_id);

COMMENT ON TABLE  workflow_runs IS '工作流执行轨迹（每次运行一行）：append-only 收尾统一写，无 RUNNING 态；弱引用 workflows（无 FK，workflow 删除后轨迹保留）';
COMMENT ON COLUMN workflow_runs.workflow_id IS '弱引用 workflows.id（不建 FK——executions 先例：保留期日志表不阻碍业务删除；配 workflow_name 快照保可读）';
COMMENT ON COLUMN workflow_runs.conversation_id IS 'chat 触发时弱引用 conversations.id（可空，无 FK——workflow 不得依赖 chat）；console 触发为 NULL';
COMMENT ON COLUMN workflow_runs.is_trial IS '试运行（execute ?trial=true，draft/disabled 也可执行）：运行历史过滤测试与真实流量';
COMMENT ON COLUMN workflow_runs.started_at IS '执行起点；created_at 是收尾写入时刻，差值即 duration_ms';
COMMENT ON TABLE  workflow_node_runs IS '工作流节点执行轨迹（每节点一行）：seq 是执行顺序唯一事实源；input/output 为截断摘要，非 ctx 全量快照';

-- +goose Down
DROP TABLE IF EXISTS workflow_node_runs;
DROP TABLE IF EXISTS workflow_runs;
