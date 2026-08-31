// Package store 是 agent 模块的数据层：实现 service.Store（GORM），只操作
// 本模块声明的表（agents / agent_tools）。错误原样上抛，业务翻译在 service 层。
package store

import (
	"context"

	"gorm.io/gorm"

	agentsvc "github.com/Karlsk/go-hify/internal/agent/service"
	"github.com/Karlsk/go-hify/internal/platform/page"
)

// selectAgent 显式列清单（禁 SELECT *）：agents 无大文本列，此处主要为对齐全仓规范
// 与防加列耦合。deleted_at 一并取回（软删 mixin 字段，可见行恒为 NULL）。
const selectAgent = "id, name, description, model_id, fallback_model_id, system_prompt, temperature, max_output_tokens, created_at, updated_at, deleted_at"

// Store 实现 agentsvc.Store。
type Store struct{ db *gorm.DB }

// New 创建 Store。
func New(db *gorm.DB) *Store { return &Store{db: db} }

// 编译期断言：Store 实现了 service.Store 接口。
var _ agentsvc.Store = (*Store)(nil)

// ---- agent ----

// CreateAgent 插入 Agent（id / created_at / updated_at 由 DB 生成并经 RETURNING 回填）。
func (s *Store) CreateAgent(ctx context.Context, a *agentsvc.Agent) error {
	return s.db.WithContext(ctx).Create(a).Error
}

// GetAgentByID 按主键查（gorm.DeletedAt 自动加 WHERE deleted_at IS NULL，软删行不可见）；
// 未找到返回 gorm.ErrRecordNotFound。
func (s *Store) GetAgentByID(ctx context.Context, id uint64) (*agentsvc.Agent, error) {
	var a agentsvc.Agent
	err := s.db.WithContext(ctx).Select(selectAgent).First(&a, id).Error
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// ListAgents 活跃 Agent 偏移分页（id 升序）：先 Count 再取当页（软删行两个查询都被过滤）。
func (s *Store) ListAgents(ctx context.Context, p page.OffsetParams) (page.OffsetResult[agentsvc.Agent], error) {
	var (
		items []agentsvc.Agent
		total int64
	)
	base := func() *gorm.DB {
		return s.db.WithContext(ctx).Model(&agentsvc.Agent{})
	}
	if err := base().Count(&total).Error; err != nil {
		return page.OffsetResult[agentsvc.Agent]{}, err
	}
	if err := p.Apply(base()).
		Select(selectAgent).
		Order("id").
		Find(&items).Error; err != nil {
		return page.OffsetResult[agentsvc.Agent]{}, err
	}
	return page.NewOffsetResult(items, p, total), nil
}

// UpdateAgent 全量 Save（PUT 语义，零值一并覆盖）；updated_at 由 autoUpdateTime 维护。
// 前置：a.ID 必须有效（service 层先 GetAgentByID 取得；软删行 WHERE 不命中即静默 0 行）。
func (s *Store) UpdateAgent(ctx context.Context, a *agentsvc.Agent) error {
	return s.db.WithContext(ctx).Save(a).Error
}

// DeleteAgent 软删除：gorm.DeletedAt 把 DELETE 改写为 UPDATE deleted_at（绑定行保留）；
// RowsAffected=0（不存在或已软删）返回 gorm.ErrRecordNotFound。
func (s *Store) DeleteAgent(ctx context.Context, id uint64) error {
	res := s.db.WithContext(ctx).Delete(&agentsvc.Agent{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// ---- agent_tools（绑定） ----

// ListToolIDsByAgent 读绑定工具 id 列表（id 升序 = 绑定先后）。
func (s *Store) ListToolIDsByAgent(ctx context.Context, agentID uint64) ([]uint64, error) {
	var ids []uint64
	err := s.db.WithContext(ctx).
		Model(&agentsvc.AgentTool{}).
		Where("agent_id = ?", agentID).
		Order("id").
		Pluck("tool_id", &ids).Error
	if err != nil {
		return nil, err
	}
	return ids, nil
}

// DeleteToolsByAgent 清空绑定（硬删，append-only 表无软删语义）。
func (s *Store) DeleteToolsByAgent(ctx context.Context, agentID uint64) error {
	return s.db.WithContext(ctx).
		Where("agent_id = ?", agentID).
		Delete(&agentsvc.AgentTool{}).Error
}

// CreateTools 批量绑定：GORM 切片插入 = 单条多 VALUES INSERT；空列表直接返回。
// tool_id 不存在时 FK 23503 由 service 层翻译（本期工具存在性的唯一校验机制）。
func (s *Store) CreateTools(ctx context.Context, agentID uint64, toolIDs []uint64) error {
	if len(toolIDs) == 0 {
		return nil
	}
	rows := make([]agentsvc.AgentTool, 0, len(toolIDs))
	for _, tid := range toolIDs {
		rows = append(rows, agentsvc.AgentTool{AgentID: agentID, ToolID: tid})
	}
	return s.db.WithContext(ctx).Create(&rows).Error
}

// WithTx 事务包装：fn 拿到包装了 tx 句柄的 Store（仍以 service.Store 接口身份传入）。
func (s *Store) WithTx(ctx context.Context, fn func(tx agentsvc.Store) error) error {
	return s.db.WithContext(ctx).Transaction(func(gtx *gorm.DB) error {
		return fn(&Store{db: gtx})
	})
}
