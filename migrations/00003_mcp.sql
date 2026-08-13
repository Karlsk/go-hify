-- +goose Up
-- mcp_servers：MCP 工具服务器配置（mcp 模块）
CREATE TABLE mcp_servers (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name       text NOT NULL,
    transport  text NOT NULL CHECK (transport IN ('stdio', 'http', 'sse')),
    command    text NOT NULL DEFAULT '',
    url        text NOT NULL DEFAULT '',
    env        jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX uq_mcp_servers_name ON mcp_servers (name);

COMMENT ON TABLE mcp_servers IS 'MCP 工具服务器配置：transport + 连接参数';
COMMENT ON COLUMN mcp_servers.transport IS '连接方式：stdio（本地进程）/ http / sse';
COMMENT ON COLUMN mcp_servers.command IS 'stdio 模式的启动命令；其他模式为空串';
COMMENT ON COLUMN mcp_servers.url IS 'http/sse 模式的服务地址；stdio 模式为空串';
COMMENT ON COLUMN mcp_servers.env IS '子进程环境变量键值对（jsonb），如 {"API_KEY":"..."}';

-- mcp_tools：各 server 暴露的工具（discover 后缓存）
CREATE TABLE mcp_tools (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    mcp_server_id bigint NOT NULL REFERENCES mcp_servers (id) ON DELETE CASCADE,
    name          text NOT NULL,
    description   text NOT NULL DEFAULT '',
    input_schema  jsonb NOT NULL DEFAULT '{}',
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_mcp_tools_server ON mcp_tools (mcp_server_id);
-- 业务唯一键：同一 server 下工具名唯一
CREATE UNIQUE INDEX uq_mcp_tools_server_name ON mcp_tools (mcp_server_id, name);

COMMENT ON TABLE mcp_tools IS 'MCP server 暴露的工具清单：discover 时刷新，Agent 绑定的是这张表';
COMMENT ON COLUMN mcp_tools.input_schema IS '工具参数 JSON Schema（jsonb），供前端表单与校验';

-- +goose Down
DROP TABLE IF EXISTS mcp_tools;
DROP TABLE IF EXISTS mcp_servers;
