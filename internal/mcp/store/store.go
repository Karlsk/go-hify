// Package store 是 mcp 模块的数据层：实现 service.Store（GORM），只操作本模块声明的表
// （mcp_servers / mcp_tools）。错误原样上抛，业务翻译（哨兵 / 23505 / 23503）在 service 层。
package store

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	mcpsvc "github.com/Karlsk/go-hify/internal/mcp/service"
	"github.com/Karlsk/go-hify/internal/platform/page"
)

// selectServer / selectTool 显式列清单（禁 SELECT *，仓规 + 防加列耦合）。
const (
	selectServer = "id, name, transport, command, url, env, enabled, created_at, updated_at"
	selectTool   = "id, mcp_server_id, name, description, input_schema, created_at, updated_at"
)

// Store 实现 mcpsvc.Store。
type Store struct{ db *gorm.DB }

// New 创建 Store。
func New(db *gorm.DB) *Store { return &Store{db: db} }

// 编译期断言：Store 实现了 service.Store 接口。
var _ mcpsvc.Store = (*Store)(nil)

// ---- mcp_servers ----

// CreateServer 插入（id / created_at / updated_at 由 DB 生成并经 RETURNING 回填）。
func (s *Store) CreateServer(ctx context.Context, sv *mcpsvc.MCPServer) error {
	return s.db.WithContext(ctx).Create(sv).Error
}

// GetServerByID 按主键查；未找到返回 gorm.ErrRecordNotFound。
func (s *Store) GetServerByID(ctx context.Context, id uint64) (*mcpsvc.MCPServer, error) {
	var sv mcpsvc.MCPServer
	err := s.db.WithContext(ctx).Select(selectServer).First(&sv, id).Error
	if err != nil {
		return nil, err
	}
	return &sv, nil
}

// ListServersByIDs 批量取（绑定校验读 enabled 标志）；缺失 id 静默不在结果里。
// IN 而非 ANY：GORM 对 slice 参数按逗号展开（agent CountToolsByAgentIDs 同款；
// 绑定工具 ≤10 在 IN 上限内）。
func (s *Store) ListServersByIDs(ctx context.Context, ids []uint64) ([]mcpsvc.MCPServer, error) {
	if len(ids) == 0 {
		return []mcpsvc.MCPServer{}, nil
	}
	var items []mcpsvc.MCPServer
	err := s.db.WithContext(ctx).
		Select(selectServer).
		Where("id IN ?", ids).
		Order("id").
		Find(&items).Error
	if err != nil {
		return nil, err
	}
	return items, nil
}

// ListServers 偏移分页（id 升序）：先 Count 再取当页；每条语句从 base 重 derive——
// GORM 的 *gorm.DB 在 finisher（Count）后复用有连锁状态风险（provider ListModels 同款）。
func (s *Store) ListServers(ctx context.Context, p page.OffsetParams) (page.OffsetResult[mcpsvc.MCPServer], error) {
	var (
		items []mcpsvc.MCPServer
		total int64
	)
	base := func() *gorm.DB {
		return s.db.WithContext(ctx).Model(&mcpsvc.MCPServer{})
	}
	if err := base().Count(&total).Error; err != nil {
		return page.OffsetResult[mcpsvc.MCPServer]{}, err
	}
	if err := p.Apply(base()).
		Select(selectServer).
		Order("id").
		Find(&items).Error; err != nil {
		return page.OffsetResult[mcpsvc.MCPServer]{}, err
	}
	return page.NewOffsetResult(items, p, total), nil
}

// UpdateServer 全量 Save（PUT 语义，零值一并覆盖；env 走 serializer:json，
// map 更新路径不序列化，故不用 Updates(map)）。前置：sv.ID 必须有效
// （service 层先 GetServerByID 取得）。
func (s *Store) UpdateServer(ctx context.Context, sv *mcpsvc.MCPServer) error {
	return s.db.WithContext(ctx).Save(sv).Error
}

// DeleteServer 硬删。mcp_tools 由 FK 级联删；有 Agent 绑定时级联被
// fk_agent_tools_tool_id RESTRICT 拦下（PG 23503，service 翻译 ErrMCPServerInUse）；
// RowsAffected=0（不存在）返回 gorm.ErrRecordNotFound。
func (s *Store) DeleteServer(ctx context.Context, id uint64) error {
	res := s.db.WithContext(ctx).Delete(&mcpsvc.MCPServer{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// ---- mcp_tools ----

// UpsertTools discover 批量 upsert：GORM 切片 Create = 单条多 VALUES INSERT，
// ON CONFLICT (mcp_server_id, name) DO UPDATE 刷新 description / input_schema /
// updated_at（= EXCLUDED 值，autoUpdateTime 已在插入侧填好）；单条语句天然原子，
// 无需事务包装。只增改不删（db_model.md §2.4：消失的工具不删不标记，绑定稳定性优先）；
// 空切片直接返回（server 0 工具是合法状态，不写库）。
func (s *Store) UpsertTools(ctx context.Context, tools []mcpsvc.MCPTool) error {
	if len(tools) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "mcp_server_id"}, {Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"description", "input_schema", "updated_at",
		}),
	}).Create(&tools).Error
}

// ListToolsByServer 某 server 的工具清单（name 升序，详情页展示）。
func (s *Store) ListToolsByServer(ctx context.Context, serverID uint64) ([]mcpsvc.MCPTool, error) {
	var items []mcpsvc.MCPTool
	err := s.db.WithContext(ctx).
		Select(selectTool).
		Where("mcp_server_id = ?", serverID).
		Order("name").
		Find(&items).Error
	if err != nil {
		return nil, err
	}
	return items, nil
}

// CountToolsByServerIDs 列表聚合用批量计数：工具数按 server 分组（GROUP BY 一条
// 查询覆盖当页，防 N+1）。map 无键 = 0；IN 写法理由同 ListServersByIDs。
func (s *Store) CountToolsByServerIDs(ctx context.Context, ids []uint64) (map[uint64]int64, error) {
	counts := make(map[uint64]int64, len(ids))
	if len(ids) == 0 {
		return counts, nil
	}
	var rows []struct {
		MCPServerID uint64
		Cnt         int64
	}
	err := s.db.WithContext(ctx).
		Model(&mcpsvc.MCPTool{}).
		Select("mcp_server_id, COUNT(*) AS cnt").
		Where("mcp_server_id IN ?", ids).
		Group("mcp_server_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		counts[r.MCPServerID] = r.Cnt
	}
	return counts, nil
}

// ListToolsByIDs 批量取工具（agent 绑定校验经 mcpapi 间接消费）；缺失 id 静默不在
// 结果里，存在性判定由调用方比对 id 集合。
func (s *Store) ListToolsByIDs(ctx context.Context, ids []uint64) ([]mcpsvc.MCPTool, error) {
	if len(ids) == 0 {
		return []mcpsvc.MCPTool{}, nil
	}
	var items []mcpsvc.MCPTool
	err := s.db.WithContext(ctx).
		Select(selectTool).
		Where("id IN ?", ids).
		Order("id").
		Find(&items).Error
	if err != nil {
		return nil, err
	}
	return items, nil
}
