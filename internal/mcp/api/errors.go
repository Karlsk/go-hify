package api

import "errors"

// mcp 模块哨兵错误：code = Error() 字符串，与 CLAUDE.md 错误码表命名空间一致
// （MODULE_REASON）。跨模块消费方与 handler 一律用 errors.Is 判断。
var (
	// ErrMCPServerNotFound MCP 服务器不存在（404）。
	ErrMCPServerNotFound = errors.New("MCP_SERVER_NOT_FOUND")

	// ErrMCPServerNameConflict 名称已存在（409；uq_mcp_servers_name 唯一约束 23505 翻译）。
	ErrMCPServerNameConflict = errors.New("MCP_SERVER_NAME_CONFLICT")

	// ErrMCPServerInUse 有 Agent 绑定该 server 的工具，无法删除（409；
	// fk_agent_tools_tool_id RESTRICT 的 23503 翻译，迁移 00021）。可解绑后删，
	// 或改用停用（enabled=false，可逆下架）。
	ErrMCPServerInUse = errors.New("MCP_SERVER_IN_USE")

	// ErrMCPToolNotFound 工具不存在（404；run 的 tool_name 不在该 server 的
	// mcp_tools 行内）。
	ErrMCPToolNotFound = errors.New("MCP_TOOL_NOT_FOUND")

	// ErrMCPToolCallFailed 工具调用失败（500；连接 / 握手 / tools/call 出错或
	// isError=true，message 带上游错误摘要——环境/执行限制类，对齐
	// WORKFLOW_EXECUTION_FAILED 的定位）。首 token 语义不适用于工具调用，
	// 不区分 retryable。
	ErrMCPToolCallFailed = errors.New("MCP_TOOL_CALL_FAILED")
)
