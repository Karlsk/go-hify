// Package api 是 agent 模块的契约层（纯包，无实现）：跨模块调用接口、Req/Schema、
// Validate 与哨兵错误。跨模块调用与 HTTP 请求复用同一套接口；实现在 service 包，
// 由组合根注入 handler 与上游模块（chat / workflow）。
package api

import "context"

// AgentService 是 agent 模块对外的唯一接口。方法签名固定
// (ctx context.Context, req XxxReq) (*XxxSchema, error)。
type AgentService interface {
	// Create 创建 Agent（含工具绑定，同一事务内落库：agent 行 + agent_tools 行）。
	// 错误：providerapi.ErrModelNotFound（主/备用模型不存在）、
	// ErrToolNotFound（tool_ids 含不存在的工具，FK 兜底）。
	Create(ctx context.Context, req CreateAgentReq) (*AgentSchema, error)

	// Get 详情（含绑定工具 id）；软删行不可见。
	// 错误：ErrAgentNotFound。
	Get(ctx context.Context, req GetAgentReq) (*AgentDetailSchema, error)

	// List 活跃 Agent 偏移分页（id 升序，软删行不可见）。
	List(ctx context.Context, req ListAgentsReq) (*AgentListResult, error)

	// Update 整体更新（PUT 语义：全量覆盖，绑定在同一事务内先删后插）。
	// 错误：ErrAgentNotFound、providerapi.ErrModelNotFound、ErrToolNotFound。
	Update(ctx context.Context, req UpdateAgentReq) (*AgentSchema, error)

	// Delete 软删除（绑定行保留、历史会话不动、新会话被拒）。
	// 错误：ErrAgentNotFound。
	Delete(ctx context.Context, req DeleteAgentReq) error
}
