// Package api 是 workflow 模块的契约层（纯包，无实现）：跨模块调用接口、Req/Schema、
// 密封 NodeConfig 与图校验 Validate、哨兵错误。跨模块调用与 HTTP 请求复用同一套接口；
// 实现在 service 包（spec 03/04/06），由组合根注入 handler 与上游模块（chat 触发执行走本接口）。
package api

import "context"

// WorkflowService 是 workflow 模块对外的唯一接口。方法签名固定
// (ctx context.Context, req XxxReq) (*XxxSchema, error)。
type WorkflowService interface {
	// Create 创建工作流（整图入参，status 置 draft；图校验 + jsonb 引用预检 +
	// 一事务写三表）。错误：ErrWorkflowNameConflict（uq 23505 翻译）、
	// VALIDATION_FAILED（图校验，spec 02 R1-R8）、providerapi.ErrModelNotFound /
	// ragapi.ErrKnowledgeBaseNotFound（引用预检，spec 04）。
	Create(ctx context.Context, req UpsertReq) (*WorkflowDetailSchema, error)

	// Get 详情：三表组装还原整图（nodes + edges）。
	// 错误：ErrWorkflowNotFound。
	Get(ctx context.Context, req GetWorkflowReq) (*WorkflowDetailSchema, error)

	// List 偏移分页（updated_at DESC, id DESC；摘要不带图）。
	List(ctx context.Context, req ListWorkflowsReq) (*WorkflowListResult, error)

	// Update 整图替换（PUT：图校验 + 事务内删了重插；不改 status——编辑不降级，
	// db_model 决策 #6）。错误：ErrWorkflowNotFound、ErrWorkflowNameConflict、
	// VALIDATION_FAILED、引用预检哨兵。
	Update(ctx context.Context, req UpdateWorkflowReq) (*WorkflowDetailSchema, error)

	// Delete 硬删（nodes / edges 由 FK CASCADE 清理；可逆下架走 Disable）。
	// 错误：ErrWorkflowNotFound。
	Delete(ctx context.Context, req DeleteWorkflowReq) error

	// Publish 状态动作 → published（draft/disabled → published；已 published 幂等成功）。
	// 错误：ErrWorkflowNotFound。
	Publish(ctx context.Context, req PublishWorkflowReq) (*WorkflowSummarySchema, error)

	// Disable 状态动作 → disabled（published → disabled；draft/disabled 幂等 no-op）。
	// 错误：ErrWorkflowNotFound。
	Disable(ctx context.Context, req DisableWorkflowReq) (*WorkflowSummarySchema, error)

	// Execute 执行工作流（spec 06：同步纯函数——ctx 进、RunResultSchema 出，不感知
	// SSE，呈现归调用方）。正式仅 published；?trial=true 放开 draft/disabled（状态机
	// 唯一例外，O3）。错误二分法（O4）：图缺陷类（condition 无命中出边 / 缺失变量
	// 运行期兜底 / tool 未支持）→ errs.ErrValidationFailed（400，message 带
	// node <key>: 前缀）；环境限制类（api 节点 SSRF 拦截 / 总时长超 5min）→
	// ErrWorkflowExecutionFailed（500）；下游哨兵（MODEL_NOT_FOUND / PROVIDER_BUSY /
	// RATE_LIMITED …）原样透传。把关：draft/disabled 且非 trial →
	// ErrWorkflowNotPublished；目标不存在 → ErrWorkflowNotFound。
	Execute(ctx context.Context, req ExecuteWorkflowReq) (*RunResultSchema, error)
}
