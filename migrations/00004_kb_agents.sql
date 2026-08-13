-- +goose Up
-- knowledge_bases：知识库（rag 模块）
CREATE TABLE knowledge_bases (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name               text NOT NULL,
    description        text NOT NULL DEFAULT '',
    embedding_model_id bigint NOT NULL REFERENCES models (id) ON DELETE RESTRICT,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX uq_knowledge_bases_name ON knowledge_bases (name);
CREATE INDEX idx_knowledge_bases_embedding_model ON knowledge_bases (embedding_model_id);

COMMENT ON TABLE knowledge_bases IS '知识库：一期只支持 TXT/MD 纯文本文档，固定长度分块，pgvector 召回';
COMMENT ON COLUMN knowledge_bases.embedding_model_id IS '嵌入模型（capability=embedding 的 models 行）；向量维度由它决定，换模型需重建 chunks（CLAUDE.md pgvector 索引规范）';

-- agents：Agent 配置（agent 模块，软删除）
CREATE TABLE agents (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name              text NOT NULL,
    description       text NOT NULL DEFAULT '',
    model_id          bigint NOT NULL REFERENCES models (id) ON DELETE RESTRICT,
    fallback_model_id bigint REFERENCES models (id) ON DELETE RESTRICT,
    system_prompt     text NOT NULL DEFAULT '',
    temperature       numeric(3,2) NOT NULL DEFAULT 0.7,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    deleted_at        timestamptz
);

CREATE INDEX idx_agents_model ON agents (model_id);
CREATE INDEX idx_agents_fallback_model ON agents (fallback_model_id);
-- 活跃 Agent 列表（软删除过滤走 partial 索引）
CREATE INDEX idx_agents_active ON agents (updated_at, id) WHERE deleted_at IS NULL;

COMMENT ON TABLE agents IS 'Agent 配置：系统提示词 + 主/备用模型 + 绑定 MCP 工具与知识库；软删除（deleted_at）';
COMMENT ON COLUMN agents.fallback_model_id IS '备用模型（供应商降级用，一期留配置位不启用）';
COMMENT ON COLUMN agents.temperature IS '采样温度 0.0-2.0';

-- agent_mcp_tools：Agent ↔ MCP 工具（多对多关联表）
CREATE TABLE agent_mcp_tools (
    agent_id    bigint NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    mcp_tool_id bigint NOT NULL REFERENCES mcp_tools (id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (agent_id, mcp_tool_id)
);

-- 反查：某工具被哪些 Agent 绑定
CREATE INDEX idx_agent_mcp_tools_tool ON agent_mcp_tools (mcp_tool_id);

COMMENT ON TABLE agent_mcp_tools IS '关联表：Agent 绑定的 MCP 工具（多对多）；Agent 删除级联清绑定';

-- agent_knowledge_bases：Agent ↔ 知识库（多对多关联表）
CREATE TABLE agent_knowledge_bases (
    agent_id          bigint NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    knowledge_base_id bigint NOT NULL REFERENCES knowledge_bases (id) ON DELETE CASCADE,
    created_at        timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (agent_id, knowledge_base_id)
);

-- 反查：某知识库被哪些 Agent 引用
CREATE INDEX idx_agent_kbs_kb ON agent_knowledge_bases (knowledge_base_id);

COMMENT ON TABLE agent_knowledge_bases IS '关联表：Agent 关联的知识库（多对多，RAG 召回范围）；Agent 删除级联清关联';

-- +goose Down
DROP TABLE IF EXISTS agent_knowledge_bases;
DROP TABLE IF EXISTS agent_mcp_tools;
DROP TABLE IF EXISTS agents;
DROP TABLE IF EXISTS knowledge_bases;
