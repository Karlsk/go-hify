// Package store 是 workflow 模块的数据层：实现 service.Store（GORM），只操作本模块
// 声明的三张表（workflows / workflow_nodes / workflow_edges）。错误原样上抛（可用 %w
// 加上下文），业务翻译（哨兵 / 23505）在 service 层；整图写入的原子单元在本层以
// Transaction 包装（api_contract §6）。
package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	workflowsvc "github.com/Karlsk/go-hify/internal/workflow/service"
)

// select* 显式列清单（禁 SELECT *：GORM 默认 Find 发 SELECT * 文本，仓规禁止；
// 列集 = model 字段集，加列须两处同步）。
const (
	selectWorkflow = "id, name, description, start_node_key, status, type, input_schema, output_schema, created_at, updated_at"
	selectNode     = "id, workflow_id, node_key, type, name, config, created_at"
	selectEdge     = "id, workflow_id, source_node_key, target_node_key, condition, created_at"
)

// Store 实现 workflowsvc.Store。
type Store struct{ db *gorm.DB }

// New 创建 Store。
func New(db *gorm.DB) *Store { return &Store{db: db} }

// 编译期断言：Store 实现了 service.Store 接口（spec 03 §2.2）。
var _ workflowsvc.Store = (*Store)(nil)

// GetByID 按主键查；未找到原样上抛 gorm.ErrRecordNotFound。
func (s *Store) GetByID(ctx context.Context, id uint64) (*workflowsvc.Workflow, error) {
	var wf workflowsvc.Workflow
	err := s.db.WithContext(ctx).Select(selectWorkflow).First(&wf, id).Error
	if err != nil {
		return nil, err
	}
	return &wf, nil
}

// List 行 + 精确 total：先 COUNT 再取当页（极小静态表，接口规范 B 模式例外，
// 允许 OFFSET 与精确 COUNT）；最近编辑在前。
func (s *Store) List(ctx context.Context, offset, limit int) ([]workflowsvc.Workflow, int64, error) {
	var (
		items []workflowsvc.Workflow
		total int64
	)
	if err := s.db.WithContext(ctx).Model(&workflowsvc.Workflow{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := s.db.WithContext(ctx).
		Select(selectWorkflow).
		Order("updated_at DESC, id DESC").
		Limit(limit).
		Offset(offset).
		Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// ListNodes 按 id 升序稳定还原节点（多 VALUES INSERT 的 id 顺序即请求顺序）。
func (s *Store) ListNodes(ctx context.Context, workflowID uint64) ([]workflowsvc.WorkflowNode, error) {
	var nodes []workflowsvc.WorkflowNode
	err := s.db.WithContext(ctx).
		Select(selectNode).
		Where("workflow_id = ?", workflowID).
		Order("id").
		Find(&nodes).Error
	if err != nil {
		return nil, err
	}
	return nodes, nil
}

// ListEdges 按 id 升序稳定还原连线。
func (s *Store) ListEdges(ctx context.Context, workflowID uint64) ([]workflowsvc.WorkflowEdge, error) {
	var edges []workflowsvc.WorkflowEdge
	err := s.db.WithContext(ctx).
		Select(selectEdge).
		Where("workflow_id = ?", workflowID).
		Order("id").
		Find(&edges).Error
	if err != nil {
		return nil, err
	}
	return edges, nil
}

// insertGraph 回填子表行 FK（workflows 行落库后主键才分配）并批量插入 nodes/edges。
// Create / ReplaceGraph 事务尾段共用；空 edges 切片跳过该语句（spec 03 §3）。
func insertGraph(gtx *gorm.DB, wfID uint64, nodes []workflowsvc.WorkflowNode, edges []workflowsvc.WorkflowEdge) error {
	for i := range nodes {
		nodes[i].WorkflowID = wfID
	}
	for i := range edges {
		edges[i].WorkflowID = wfID
	}
	if err := gtx.Create(&nodes).Error; err != nil {
		return fmt.Errorf("insert workflow %d nodes: %w", wfID, err)
	}
	if len(edges) > 0 {
		if err := gtx.Create(&edges).Error; err != nil {
			return fmt.Errorf("insert workflow %d edges: %w", wfID, err)
		}
	}
	return nil
}

// Create 一事务写三表：INSERT workflows + 批量 INSERT nodes/edges（任一失败整体
// 回滚）。事务内零外部调用；批量走 GORM 切片插入 = 单条多 VALUES INSERT（spec 03 §3）。
func (s *Store) Create(ctx context.Context, wf *workflowsvc.Workflow, nodes []workflowsvc.WorkflowNode, edges []workflowsvc.WorkflowEdge) error {
	return s.db.WithContext(ctx).Transaction(func(gtx *gorm.DB) error {
		if err := gtx.Create(wf).Error; err != nil {
			return fmt.Errorf("create workflow %s: %w", wf.Name, err)
		}
		return insertGraph(gtx, wf.ID, nodes, edges)
	})
}

// ReplaceGraph 整图替换单事务：UPDATE workflows（不含 status——编辑不降级，db_model
// 决策 #6；也不含 type——分型不可变，spec 08，Update 携带即拒归 service guard）→
// DELETE nodes → DELETE edges → 批量 INSERT ×2。schema 列随整图全量替换：请求未
// 携带即置 NULL（PUT 语义，与 nodes/edges 先删后插一致）。affected=0 → false 短路，
// 不再删插，service 翻译 404；先删后插硬删、不做 diff（决策 #8）；updated_at 由
// autoUpdateTime 维护。返回是否命中存在行。
func (s *Store) ReplaceGraph(ctx context.Context, wf *workflowsvc.Workflow, nodes []workflowsvc.WorkflowNode, edges []workflowsvc.WorkflowEdge) (bool, error) {
	moved := false
	err := s.db.WithContext(ctx).Transaction(func(gtx *gorm.DB) error {
		res := gtx.Model(&workflowsvc.Workflow{}).
			Where("id = ?", wf.ID).
			Updates(map[string]interface{}{
				"name":           wf.Name,
				"description":    wf.Description,
				"start_node_key": wf.StartNodeKey,
				"input_schema":   wf.InputSchema,
				"output_schema":  wf.OutputSchema,
			})
		if res.Error != nil {
			return fmt.Errorf("replace graph workflow %d: %w", wf.ID, res.Error)
		}
		if res.RowsAffected == 0 {
			return nil // 不存在：短路（moved=false）
		}
		moved = true
		if err := gtx.Where("workflow_id = ?", wf.ID).Delete(&workflowsvc.WorkflowNode{}).Error; err != nil {
			return fmt.Errorf("replace graph workflow %d: delete nodes: %w", wf.ID, err)
		}
		if err := gtx.Where("workflow_id = ?", wf.ID).Delete(&workflowsvc.WorkflowEdge{}).Error; err != nil {
			return fmt.Errorf("replace graph workflow %d: delete edges: %w", wf.ID, err)
		}
		return insertGraph(gtx, wf.ID, nodes, edges)
	})
	if err != nil {
		return false, err
	}
	return moved, nil
}

// Delete 硬删 workflows；nodes/edges 由 FK CASCADE 同步清理。affected=0 → false
// （不存在，service 翻译 404）。
func (s *Store) Delete(ctx context.Context, id uint64) (bool, error) {
	res := s.db.WithContext(ctx).Delete(&workflowsvc.Workflow{}, id)
	if res.Error != nil {
		return false, fmt.Errorf("delete workflow %d: %w", id, res.Error)
	}
	return res.RowsAffected > 0, nil
}

// UpdateStatus 原子状态迁移：单条 UPDATE ... WHERE id = ? AND status IN (from)，
// 不做 get-then-set 竞态窗口；updated_at 由 autoUpdateTime 维护。affected>0 →
// moved=true（service 据此区分 404 / 幂等 no-op 与真实迁移）。
func (s *Store) UpdateStatus(ctx context.Context, id uint64, from []string, to string) (bool, error) {
	res := s.db.WithContext(ctx).
		Model(&workflowsvc.Workflow{}).
		Where("id = ? AND status IN ?", id, from).
		Updates(map[string]interface{}{"status": to})
	if res.Error != nil {
		return false, fmt.Errorf("update workflow %d status: %w", id, res.Error)
	}
	return res.RowsAffected > 0, nil
}

// CreateRun 执行收尾一事务两批写（spec 06 O7）：INSERT workflow_runs RETURNING
// id / created_at 回填 run → 回填 nodeRuns.RunID → 切片 Create = 单条多 VALUES
// INSERT workflow_node_runs（Postgres 方言自动 RETURNING "id" 回填各行主键）。
// nodeRuns 空则只写 run 行；任一批失败整体回滚（无 RUNNING 态——append-only
// 收尾统一写，事务内零外部调用）。
func (s *Store) CreateRun(ctx context.Context, run *workflowsvc.WorkflowRun, nodeRuns []workflowsvc.WorkflowNodeRun) error {
	return s.db.WithContext(ctx).Transaction(func(gtx *gorm.DB) error {
		if err := gtx.Create(run).Error; err != nil {
			return fmt.Errorf("insert workflow %d run: %w", run.WorkflowID, err)
		}
		for i := range nodeRuns {
			nodeRuns[i].RunID = run.ID // 主键落库后才分配，回填子表 FK
		}
		if len(nodeRuns) > 0 {
			if err := gtx.Create(&nodeRuns).Error; err != nil {
				return fmt.Errorf("insert workflow %d node runs: %w", run.WorkflowID, err)
			}
		}
		return nil
	})
}

// deleteRunsBeforeSQL 保留期批删（FR8）：id 子查询限定 created_at 边界与单批上限
//（防长事务锁累积）；node_runs 随 FK ON DELETE CASCADE 连带删（00019 DDL）。
const deleteRunsBeforeSQL = `DELETE FROM workflow_runs WHERE id IN (
	SELECT id FROM workflow_runs WHERE created_at < $1 LIMIT $2)`

// DeleteRunsBefore 批删保留期外的 run 行，返回实际删除行数（不满一批 = 清完，
// 由调用方 RunsCleaner 循环驱动）。
func (s *Store) DeleteRunsBefore(ctx context.Context, before time.Time, limit int) (int64, error) {
	res := s.db.WithContext(ctx).Exec(deleteRunsBeforeSQL, before, limit)
	if res.Error != nil {
		return 0, fmt.Errorf("delete workflow runs before %s: %w", before.Format(time.RFC3339), res.Error)
	}
	return res.RowsAffected, nil
}

// updateParentRunIDsSQL 子 run 的 parent_run_id 批量回填（spec 08 O5）：按父 run id
// 一次窄 UPDATE——append-only 表的唯一 UPDATE 例外（父收尾统一回填）；id 集合以
// bigint[] 数组字面量 + 显式 cast 绑定（单语句批量、参数类型对齐列类型）。
const updateParentRunIDsSQL = `UPDATE workflow_runs SET parent_run_id = $1 WHERE id = ANY($2::bigint[])`

// buildBigIntArray []uint64 → PG 数组字面量 `{1,2,3}`（配 ANY($1::bigint[])）。
func buildBigIntArray(ids []uint64) string {
	var b strings.Builder
	b.WriteByte('{')
	for i, id := range ids {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatUint(id, 10))
	}
	b.WriteByte('}')
	return b.String()
}

// UpdateParentRunIDs 父 run 落库后回填其直接子 run 的 parent_run_id；childRunIDs
// 空 → 短路零 SQL。错误 %w 包装原样上抛（调用方 WARN 不阻断返回）。
func (s *Store) UpdateParentRunIDs(ctx context.Context, parentRunID uint64, childRunIDs []uint64) error {
	if len(childRunIDs) == 0 {
		return nil
	}
	res := s.db.WithContext(ctx).Exec(updateParentRunIDsSQL, parentRunID, buildBigIntArray(childRunIDs))
	if res.Error != nil {
		return fmt.Errorf("update parent_run_id for run %d: %w", parentRunID, res.Error)
	}
	return nil
}
