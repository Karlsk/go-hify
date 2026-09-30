// Package api 是 mcp 模块的契约层（纯包，无实现）：跨模块调用接口、Req/Schema、
// Validate 与哨兵错误。跨模块调用与 HTTP 请求复用同一套接口；实现在 service 包
// （同一实例实现 MCPServerService 与 MCPClientService 两张面孔，
// server_mgmt_spec.md §3），由组合根分别注入 handler 与上游模块（agent / chat）。
package api

import (
	"context"

	"github.com/Karlsk/go-hify/internal/platform/tool"
)

// MCPServerService 管理面接口：mcp handler 消费（CRUD + Discover + RunTool）。
// 方法签名固定 (ctx context.Context, req XxxReq) (*XxxSchema, error)。
type MCPServerService interface {
	// Create 创建 MCP Server。错误：ErrMCPServerNameConflict（name 撞唯一约束）。
	Create(ctx context.Context, req CreateMCPServerReq) (*MCPServerSchema, error)

	// Get 详情（含 env 与工具清单，DB 读不连 server）。错误：ErrMCPServerNotFound。
	Get(ctx context.Context, req GetMCPServerReq) (*MCPServerDetailSchema, error)

	// List 偏移分页（id 升序，摘要含 tool_count 批量计数）。
	List(ctx context.Context, req ListMCPServersReq) (*MCPServerListResult, error)

	// Update 整体更新（PUT 语义）。错误：ErrMCPServerNotFound、ErrMCPServerNameConflict。
	Update(ctx context.Context, req UpdateMCPServerReq) (*MCPServerSchema, error)

	// Delete 真删：mcp_tools 级联删。错误：ErrMCPServerNotFound、ErrMCPServerInUse
	//（有 Agent 绑定，FK RESTRICT 挡删——可解绑后删或改停用）。
	Delete(ctx context.Context, req DeleteMCPServerReq) error

	// Discover 连通测试 + 工具发现：连接 server → initialize → tools/list →
	// upsert mcp_tools（只增改不删）。结果恒 200，成败在 DiscoverResultSchema
	//（provider TestConnection 同款）；server 不存在才 404。
	// 错误：ErrMCPServerNotFound。
	Discover(ctx context.Context, req DiscoverReq) (*DiscoverResultSchema, error)

	// RunTool 实调一个工具（管理面诊断；内部复用 MCPClientService.CallTool）。
	// 错误：ErrMCPServerNotFound、ErrMCPToolNotFound（工具不在该 server）、
	// ErrMCPToolCallFailed（连接 / 调用失败或 isError）。
	RunTool(ctx context.Context, req RunToolReq) (*CallToolResult, error)
}

// MCPClientService 调用面接口：agent 绑定校验（本批）与 chat 工具循环（下批）消费。
type MCPClientService interface {
	// CallTool 按调用创建 client（连接 + initialize + tools/call + Close），
	// 结果取 TextContent 多条换行拼接（per-call 生命周期，server_mgmt_spec.md §4）。
	// 不检查 enabled——调用方组装工具时自行排除停用 server（绑定校验已挡新增绑定）。
	// 错误：ErrMCPServerNotFound、ErrMCPToolCallFailed。
	CallTool(ctx context.Context, req CallToolReq) (*CallToolResult, error)

	// ListTools 读某 server 的工具清单（DB 读，不连 server）。错误：ErrMCPServerNotFound。
	ListTools(ctx context.Context, req ListToolsReq) ([]ToolSchema, error)

	// GetToolsByIDs 批量取工具 + 所属 server 启用标志（缺失 id 静默不在结果里，
	// 存在性判定由调用方比对 id 集合）——agent 绑定校验「存在且 server 启用」的依据。
	GetToolsByIDs(ctx context.Context, req GetToolsByIDsReq) ([]ToolCheck, error)

	// ToolSourceFor 按绑定工具 id 子集返回统一工具来源（platform/tool.Source）。
	// 工厂方法：构造无 IO，真实加载（查库）在返回的 Source.Tools / Invoke 的 ctx
	// 上惰性发生；chat 每轮对话取一次、单轮内缓存复用（chat_tool_loop_spec.md §4）。
	// 停用 server 的工具在 Tools() 清单里静默消失；Invoke 恒返回结果（失败也是
	// IsError，错误即上下文喂回 LLM，不中断对话）。
	ToolSourceFor(req ToolSourceReq) tool.Source
}
