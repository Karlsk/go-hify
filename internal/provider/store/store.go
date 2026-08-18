// Package store 是 provider 模块的数据层：实现 service.Store（GORM CRUD + jsonb 序列化）。
// error 原样上抛（含 pgconn.PgError，供 service 层按 23505 / 23503 翻译），业务翻译在 service 层。
package store

import (
	"context"

	"gorm.io/gorm"

	"github.com/Karlsk/go-hify/internal/platform/page"
	providersvc "github.com/Karlsk/go-hify/internal/provider/service"
)

var _ providersvc.Store = (*Store)(nil) // 编译期断言：Store 实现了 service.Store

// 显式列清单（禁 SELECT *，CLAUDE.md《SQL 编写规范》；列序与迁移 SQL 一致）。
const (
	selectProvider = "id, name, kind, base_url, auth_config, api_key_rotated_at, extra_config, enabled, created_at, updated_at"
	selectModel    = "id, provider_id, name, model_id, capability, context_window, max_output_tokens, input_price, output_price, embedding_dim, enabled, source, extra_params, created_at, updated_at"
	selectHealth   = "provider_id, status, last_check_at, last_success_at, fail_count, latency_ms, error_message, created_at, updated_at"
)

// Store 实现 providersvc.Store；error 原样上抛，业务翻译在 service 层。
type Store struct{ db *gorm.DB }

// New 创建 Store。
func New(db *gorm.DB) *Store { return &Store{db: db} }

// ---- provider ----

// CreateProvider 插入提供商（id / created_at / updated_at 由 DB 生成并经 RETURNING 回填）。
func (s *Store) CreateProvider(ctx context.Context, p *providersvc.Provider) error {
	return s.db.WithContext(ctx).Create(p).Error
}

// GetProviderByID 按主键查；未找到返回 gorm.ErrRecordNotFound。
func (s *Store) GetProviderByID(ctx context.Context, id uint64) (*providersvc.Provider, error) {
	var p providersvc.Provider
	err := s.db.WithContext(ctx).Select(selectProvider).First(&p, id).Error
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// GetProviderByName 按展示名查（唯一索引 uq_providers_name 支撑）。
func (s *Store) GetProviderByName(ctx context.Context, name string) (*providersvc.Provider, error) {
	var p providersvc.Provider
	err := s.db.WithContext(ctx).
		Where("name = ?", name).
		Select(selectProvider).
		First(&p).Error
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// ListProviders 全表快照（id 升序）；表极小（配置表），内存筛选 / 分页在 service 层。
func (s *Store) ListProviders(ctx context.Context) ([]providersvc.Provider, error) {
	var items []providersvc.Provider
	err := s.db.WithContext(ctx).
		Select(selectProvider).
		Order("id").
		Find(&items).Error
	if err != nil {
		return nil, err
	}
	return items, nil
}

// UpdateProvider 全量 Save：struct 路径保证 auth_config / extra_config 走 serializer:json
// （map 更新路径不序列化，故不用 Updates(map)）；updated_at 由 autoUpdateTime 维护。
// 前置：p.ID 必须有效（service 层先 GetProviderByID 取得）。
func (s *Store) UpdateProvider(ctx context.Context, p *providersvc.Provider) error {
	return s.db.WithContext(ctx).Save(p).Error
}

// DeleteProvider 删除提供商（models / provider_health 由 FK 级联）；RowsAffected=0 返回 gorm.ErrRecordNotFound。
func (s *Store) DeleteProvider(ctx context.Context, id uint64) error {
	res := s.db.WithContext(ctx).Delete(&providersvc.Provider{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// ---- model ----

// CreateModel 插入模型。
func (s *Store) CreateModel(ctx context.Context, m *providersvc.Model) error {
	return s.db.WithContext(ctx).Create(m).Error
}

// GetModelByID 按主键查；未找到返回 gorm.ErrRecordNotFound。
func (s *Store) GetModelByID(ctx context.Context, id uint64) (*providersvc.Model, error) {
	var m providersvc.Model
	err := s.db.WithContext(ctx).Select(selectModel).First(&m, id).Error
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// GetModelByProviderAndModelID 按唯一约束 uq(provider_id, model_id) 查。
func (s *Store) GetModelByProviderAndModelID(ctx context.Context, providerID uint64, modelID string) (*providersvc.Model, error) {
	var m providersvc.Model
	err := s.db.WithContext(ctx).
		Where("provider_id = ? AND model_id = ?", providerID, modelID).
		Select(selectModel).
		First(&m).Error
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// ListModelsByProvider 某提供商下全部模型（id 升序），详情聚合用。
func (s *Store) ListModelsByProvider(ctx context.Context, providerID uint64) ([]providersvc.Model, error) {
	var items []providersvc.Model
	err := s.db.WithContext(ctx).
		Where("provider_id = ?", providerID).
		Select(selectModel).
		Order("id").
		Find(&items).Error
	if err != nil {
		return nil, err
	}
	return items, nil
}

// ListModels 某提供商下模型偏移分页（小表，OFFSET 可接受，见 CLAUDE.md《分页》）：
// 先精确 Count，再 Apply(offset/limit) + ORDER BY id 取本页。
// 每条语句从 base 重derive——GORM 的 *gorm.DB 在 finisher（Count）后复用有连锁状态风险。
func (s *Store) ListModels(ctx context.Context, providerID uint64, p page.OffsetParams) (page.OffsetResult[providersvc.Model], error) {
	var (
		items []providersvc.Model
		total int64
	)
	base := func() *gorm.DB {
		return s.db.WithContext(ctx).Model(&providersvc.Model{}).Where("provider_id = ?", providerID)
	}
	if err := base().Count(&total).Error; err != nil {
		return page.OffsetResult[providersvc.Model]{}, err
	}
	if err := p.Apply(base()).
		Select(selectModel).
		Order("id").
		Find(&items).Error; err != nil {
		return page.OffsetResult[providersvc.Model]{}, err
	}
	return page.NewOffsetResult(items, p, total), nil
}

// UpdateModel 全量 Save（PUT 语义；extra_params 走 serializer:json）。
// 前置：m.ID 必须有效（service 层先 GetModelByID 取得）。
func (s *Store) UpdateModel(ctx context.Context, m *providersvc.Model) error {
	return s.db.WithContext(ctx).Save(m).Error
}

// DeleteModel 删除模型；RowsAffected=0 返回 gorm.ErrRecordNotFound。
func (s *Store) DeleteModel(ctx context.Context, id uint64) error {
	res := s.db.WithContext(ctx).Delete(&providersvc.Model{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// ---- health ----

// GetHealthByProviderID 按主键（provider_id，1:1）查；未找到返回 gorm.ErrRecordNotFound。
func (s *Store) GetHealthByProviderID(ctx context.Context, providerID uint64) (*providersvc.ProviderHealth, error) {
	var h providersvc.ProviderHealth
	err := s.db.WithContext(ctx).
		Select(selectHealth).
		First(&h, "provider_id = ?", providerID).Error
	if err != nil {
		return nil, err
	}
	return &h, nil
}
