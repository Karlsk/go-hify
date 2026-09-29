-- +goose Up
-- MCP Server 管理批（docs/changelog/mcp/db_model.md §3，两处修订）：
-- ① mcp_servers 补 enabled：可逆下架开关（00013 软删退役后的统一模式，对齐 providers/agents）
--    ——停用拒绝新绑定与调用；DELETE 才是真删，可逆下架不再需要软删。
-- ② agent_tools.tool_id FK 语义修订 CASCADE → RESTRICT：有 Agent 绑定的工具不允许被
--    （级联）删除——server 删除 → mcp_tools 级联删被此约束拦下 → 23503 → 409
--    MCP_SERVER_IN_USE；agent 侧删除仍走 agent_id CASCADE 清绑定。
--    约束显式命名 fk_agent_tools_tool_id：mcp service 侧 23503 按约束名分发翻译。
ALTER TABLE mcp_servers
    ADD COLUMN enabled boolean NOT NULL DEFAULT true;

COMMENT ON COLUMN mcp_servers.enabled IS '可逆下架开关：停用拒绝新绑定与调用；DELETE 才是真删（有绑定时被 fk_agent_tools_tool_id RESTRICT 挡）';

ALTER TABLE agent_tools DROP CONSTRAINT agent_tools_tool_id_fkey;
ALTER TABLE agent_tools
    ADD CONSTRAINT fk_agent_tools_tool_id FOREIGN KEY (tool_id)
        REFERENCES mcp_tools (id) ON DELETE RESTRICT;

COMMENT ON CONSTRAINT fk_agent_tools_tool_id ON agent_tools IS '工具被 Agent 绑定时挡删（RESTRICT）：server 删除 → mcp_tools 级联删被此约束拦下 → 23503 → MCP_SERVER_IN_USE；agent 侧删除仍走 agent_id CASCADE 清绑定';
COMMENT ON TABLE agent_tools IS '关联表：Agent 绑定的 MCP 工具（多对多，绑定=授权）；agent 删除级联清绑定、tool 侧 RESTRICT 挡删；依赖 mcp discover 走 upsert（uq_mcp_tools_server_name）保证 tool id 稳定、绑定不漂移';

-- +goose Down
ALTER TABLE agent_tools DROP CONSTRAINT fk_agent_tools_tool_id;
ALTER TABLE agent_tools
    ADD CONSTRAINT agent_tools_tool_id_fkey FOREIGN KEY (tool_id)
        REFERENCES mcp_tools (id) ON DELETE CASCADE;
COMMENT ON TABLE agent_tools IS '关联表：Agent 绑定的 MCP 工具（多对多，绑定=授权）；任一侧删除级联清绑定；依赖 mcp discover 走 upsert（uq_mcp_tools_server_name）保证 tool id 稳定、绑定不漂移';
ALTER TABLE mcp_servers DROP COLUMN IF EXISTS enabled;
