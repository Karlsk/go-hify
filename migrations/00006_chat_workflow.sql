-- +goose Up
-- workflows：简版工作流（JSON 配置，线性 + 条件分支）
CREATE TABLE workflows (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    config      jsonb NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX uq_workflows_name ON workflows (name);

COMMENT ON TABLE workflows IS '简版工作流：JSON 配置（线性 + 条件分支），非可视化编排';
COMMENT ON COLUMN workflows.config IS '节点与边的 JSON 配置（jsonb）；整块存取，不加 GIN';

-- conversations：对话会话（chat 模块，可变表）
CREATE TABLE conversations (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    bigint NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    agent_id   bigint NOT NULL REFERENCES agents (id) ON DELETE RESTRICT,
    title      text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- 会话列表 keyset 分页：WHERE user_id = $1 ORDER BY updated_at DESC, id DESC（CLAUDE.md 分页规范）
CREATE INDEX idx_conversations_user ON conversations (user_id, updated_at DESC, id DESC);
CREATE INDEX idx_conversations_agent ON conversations (agent_id);

COMMENT ON TABLE conversations IS '对话会话：归属用户 + 基于某 Agent；列表按 updated_at 倒序 keyset 分页';

-- messages：多轮消息（chat 模块，append-only）
CREATE TABLE messages (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    conversation_id bigint NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    role            text NOT NULL CHECK (role IN ('user', 'assistant', 'tool')),
    content         text NOT NULL,
    tool_calls      jsonb NOT NULL DEFAULT '[]',
    created_at      timestamptz NOT NULL DEFAULT now()
);

-- 上下文按序取：WHERE conversation_id = $1 AND id > $last_id ORDER BY id
CREATE INDEX idx_messages_conversation ON messages (conversation_id, id);

COMMENT ON TABLE messages IS '多轮消息（append-only）：user/assistant/tool 角色，按 id 正序串成上下文；会话删除级联';
COMMENT ON COLUMN messages.tool_calls IS 'assistant 消息的工具调用参数 [{tool, args}]（jsonb）；调用链明细在 executions.tool_chain';

-- +goose Down
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS conversations;
DROP TABLE IF EXISTS workflows;
