// Package api 是 workflow 模块的契约层（纯包，无实现）：跨模块调用接口、Req/Schema、
// 密封 NodeConfig 与图校验 Validate、哨兵错误。跨模块调用与 HTTP 请求复用同一套接口；
// 实现在 service 包（spec 03/04），由组合根注入 handler 与上游模块（chat 触发执行走本接口）。
package api

import "context"

// WorkflowService 是 workflow 模块对外的唯一接口（本期不含 Execute——执行引擎另有
// spec，届时扩接口 + handler + 装配，不回头改本篇）。方法签名固定
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
}
