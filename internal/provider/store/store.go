// Package store 是 provider 模块的数据层：实现 service.Store（GORM CRUD + jsonb 序列化）。
// error 原样上抛（含 pgconn.PgError，供 service 层按 23505 / 23503 翻译），业务翻译在 service 层。
package store

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Karlsk/go-hify/internal/platform/page"
	providerapi "github.com/Karlsk/go-hify/internal/provider/api"
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

// CreateModels 批量插入（GORM 切片插入 = 单条多 VALUES INSERT）；空切片直接返回。
// 撞唯一约束整批失败，由 service 层退回逐条插入并跳过冲突行（与手工创建的竞态兜底）。
func (s *Store) CreateModels(ctx context.Context, ms []*providersvc.Model) error {
	if len(ms) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Create(&ms).Error
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

// ListModelsByIDs 按 id 集合批量取模型（id 升序）；跨模块列表聚合用（agent 列表 model_name 映射）。
// 缺行不算错——悬空引用由调用方归零值处理。IN 而非 ANY：GORM 对 slice 参数按逗号展开
// （`= ANY($1,$2)` 非法）；调用方限 1-100 个 id，在 IN 上限内。
func (s *Store) ListModelsByIDs(ctx context.Context, ids []uint64) ([]providersvc.Model, error) {
	var items []providersvc.Model
	err := s.db.WithContext(ctx).
		Where("id IN ?", ids).
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

// UpdateModelName sync 专用列级更新：只写 name / updated_at，WHERE 限定 source='discovered'。
// 与全列 Save 的区别：并发的手工编辑（价格 / enabled / extra_params）不会被 sync 的旧快照覆盖；
// 行不存在或非 discovered 时 RowsAffected=0，按"跳过"返回 nil（调用方已按内存 diff 决定要更新）。
func (s *Store) UpdateModelName(ctx context.Context, id uint64, name string) error {
	return s.db.WithContext(ctx).Model(&providersvc.Model{}).
		Where("id = ? AND source = ?", id, providerapi.SourceDiscovered).
		Updates(map[string]any{"name": name, "updated_at": time.Now()}).Error
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

// ListHealthByProviderIDs 列表聚合用批量读：按当页 id 集合 IN 查询，只回存在的行
// （无行 provider 的 health 由 service 归 nil）。主键 IN，页大小 ≤100。
func (s *Store) ListHealthByProviderIDs(ctx context.Context, ids []uint64) ([]providersvc.ProviderHealth, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var hs []providersvc.ProviderHealth
	err := s.db.WithContext(ctx).
		Select(selectHealth).
		Where("provider_id IN ?", ids).
		Find(&hs).Error
	return hs, err
}

// CountEnabledModelsByProviderIDs 列表聚合用批量计数：已启用模型按提供商分组
// （GROUP BY + 聚合，一条查询覆盖当页全部 id）。map 无键 = 0。
func (s *Store) CountEnabledModelsByProviderIDs(ctx context.Context, ids []uint64) (map[uint64]int32, error) {
	counts := make(map[uint64]int32, len(ids))
	if len(ids) == 0 {
		return counts, nil
	}
	var rows []struct {
		ProviderID uint64
		Cnt        int32
	}
	err := s.db.WithContext(ctx).
		Model(&providersvc.Model{}).
		Select("provider_id, COUNT(*) AS cnt").
		Where("provider_id IN ? AND enabled = ?", ids, true).
		Group("provider_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		counts[r.ProviderID] = r.Cnt
	}
	return counts, nil
}

// GetHealthByProviderIDForUpdate 事务内锁定读（SELECT ... FOR UPDATE）：串行化并发探测对同一
// provider_health 行的读-改-写，防 fail_count 计数竞态。仅在 WithTx 回调内调用才有意义。
func (s *Store) GetHealthByProviderIDForUpdate(ctx context.Context, providerID uint64) (*providersvc.ProviderHealth, error) {
	var h providersvc.ProviderHealth
	err := s.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Select(selectHealth).
		First(&h, "provider_id = ?", providerID).Error
	if err != nil {
		return nil, err
	}
	return &h, nil
}

// WithTx 事务：fn 拿到包装了 tx 句柄的 Store（仍以 service.Store 接口身份传入）。
func (s *Store) WithTx(ctx context.Context, fn func(tx providersvc.Store) error) error {
	return s.db.WithContext(ctx).Transaction(func(gtx *gorm.DB) error {
		return fn(&Store{db: gtx})
	})
}

// UpsertHealth 写入探测结果：无行插入、有行更新（ON CONFLICT (provider_id) DO UPDATE）。
// 探测可能高频写，upsert 免去先读后写的竞态窗口。
func (s *Store) UpsertHealth(ctx context.Context, h *providersvc.ProviderHealth) error {
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "provider_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"status", "last_check_at", "last_success_at", "fail_count", "latency_ms", "error_message", "updated_at",
		}),
	}).Create(h).Error
}
