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
    temperature       numeric(3,2) NOT NULL DEFAULT 0.7 CHECK (temperature BETWEEN 0 AND 2),
    max_output_tokens bigint,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    deleted_at        timestamptz
);

CREATE INDEX idx_agents_model ON agents (model_id);
CREATE INDEX idx_agents_fallback_model ON agents (fallback_model_id);
-- 活跃 Agent 列表（软删除过滤走 partial 索引）
CREATE INDEX idx_agents_active ON agents (updated_at, id) WHERE deleted_at IS NULL;

COMMENT ON TABLE agents IS 'Agent 配置：系统提示词 + 主/备用模型 + 运行参数 + 绑定 MCP 工具与知识库；软删除（deleted_at）= 下架语义，不存上下文轮数（归 chat 引擎，token 预算实现）';
COMMENT ON COLUMN agents.model_id IS '主模型（models 表）；RESTRICT 兜底，删除前 provider 模块反查此索引翻译为 MODEL_IN_USE';
COMMENT ON COLUMN agents.fallback_model_id IS '备用模型（供应商降级用，一期留配置位不启用）';
COMMENT ON COLUMN agents.system_prompt IS '角色指令（人设/职责/边界/输出格式）；长度上限走应用层 binding 校验';
COMMENT ON COLUMN agents.temperature IS '采样温度 0.00-2.00；工具调用型 Agent 建议 0-0.3';
COMMENT ON COLUMN agents.max_output_tokens IS '单次回复输出上限；NULL=跟随模型默认（chat 引擎读 nil 则不向 eino 设该 option）';

-- agent_tools：Agent ↔ MCP 工具（多对多关联表；绑定=授权，粒度到工具不到 server）
CREATE TABLE agent_tools (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    agent_id   bigint NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    tool_id    bigint NOT NULL REFERENCES mcp_tools (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX uq_agent_tools_agent_tool ON agent_tools (agent_id, tool_id);
-- 反查：某工具被哪些 Agent 绑定（复合唯一的最左前缀覆盖不了 tool_id 单列查询，不冗余）
CREATE INDEX idx_agent_tools_tool ON agent_tools (tool_id);

COMMENT ON TABLE agent_tools IS '关联表：Agent 绑定的 MCP 工具（多对多，绑定=授权）；任一侧删除级联清绑定；依赖 mcp discover 走 upsert（uq_mcp_tools_server_name）保证 tool id 稳定、绑定不漂移';

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
DROP TABLE IF EXISTS agent_tools;
DROP TABLE IF EXISTS agents;
DROP TABLE IF EXISTS knowledge_bases;
