// Package service 是 workflow 模块的业务层：实现 api.WorkflowService 接口（业务
// 实现、schema↔model 转换、哨兵翻译归实施 spec 04）。本文件当前只定义 Store 数据层
// 接口——定义在消费方（本包）、由 store 包实现、组合根注入，依赖倒置。
package service

import "context"

// Store 数据层接口：整图读写的原子单元（Create / ReplaceGraph 内部事务包装，
// api_contract §6——service 不拼 DML，store 不做业务判断，事务内只操作本模块三张表）。
type Store interface {
	// Create 一事务写三表：INSERT workflows + 批量 INSERT nodes/edges（任一失败整体回滚）。
	Create(ctx context.Context, wf *Workflow, nodes []WorkflowNode, edges []WorkflowEdge) error
	// GetByID 按主键查；未找到原样上抛 gorm.ErrRecordNotFound（翻译归 service，spec 04）。
	GetByID(ctx context.Context, id uint64) (*Workflow, error)
	// List 行 + 精确 total（极小静态表，接口规范 B 模式允许 OFFSET 与精确 COUNT）。
	List(ctx context.Context, offset, limit int) ([]Workflow, int64, error)
	// ReplaceGraph 整图替换单事务：UPDATE workflows（affected=0 → false，service 翻译 404）
	// → DELETE nodes → DELETE edges → 批量 INSERT ×2。先删后插硬删，不做 diff（db_model 决策 #8）。
	ReplaceGraph(ctx context.Context, wf *Workflow, nodes []WorkflowNode, edges []WorkflowEdge) (bool, error)
	// Delete 硬删 workflows；nodes/edges 由 FK CASCADE 清理。affected=0 → false。
	Delete(ctx context.Context, id uint64) (bool, error)
	// UpdateStatus 原子状态迁移：UPDATE ... SET status=to WHERE id=? AND status IN (from)。
	// affected>0 → moved=true（service 据此判幂等，不做 get-then-set 竞态窗口）。
	UpdateStatus(ctx context.Context, id uint64, from []string, to string) (bool, error)
	// ListNodes 按 id 升序稳定还原（多 VALUES INSERT 的 id 顺序即请求顺序）。
	ListNodes(ctx context.Context, workflowID uint64) ([]WorkflowNode, error)
	// ListEdges 按 id 升序稳定还原。
	ListEdges(ctx context.Context, workflowID uint64) ([]WorkflowEdge, error)
}
