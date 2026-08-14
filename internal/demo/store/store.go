// Package store 是 demo 模块的数据层：实现 service.Store（GORM CRUD）。
// error 原样上抛，业务翻译在 service 层。
package store

import (
	"context"

	"gorm.io/gorm"

	demosvc "github.com/Karlsk/go-hify/internal/demo/service"
	"github.com/Karlsk/go-hify/internal/platform/page"
)

var _ demosvc.Store = (*Store)(nil) // 编译期断言：Store 实现了 service.Store

// selectColumns 显式列清单（禁 SELECT *，CLAUDE.md《SQL 编写规范》）。
const selectColumns = "id, name, status, created_at, updated_at"

// Store 实现 demosvc.Store（GORM CRUD）；error 原样上抛，业务翻译在 service 层。
type Store struct{ db *gorm.DB }

// New 创建 Store。
func New(db *gorm.DB) *Store { return &Store{db: db} }

// Create 插入 demo 条目（id / created_at / updated_at 由 DB 生成并经 RETURNING 回填）。
func (s *Store) Create(ctx context.Context, item *demosvc.DemoItem) error {
	return s.db.WithContext(ctx).Create(item).Error
}

// GetByID 按 ID 查询；未找到返回 gorm.ErrRecordNotFound。
func (s *Store) GetByID(ctx context.Context, id uint64) (*demosvc.DemoItem, error) {
	var item demosvc.DemoItem
	err := s.db.WithContext(ctx).Select(selectColumns).First(&item, id).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// List 偏移分页（demo 是小表，OFFSET 可接受，见 CLAUDE.md《分页》）：
// 先精确 Count 总数，再 Apply(offset/limit) + ORDER BY id DESC 取本页。
func (s *Store) List(ctx context.Context, p page.OffsetParams) (page.OffsetResult[demosvc.DemoItem], error) {
	var (
		items []demosvc.DemoItem
		total int64
	)
	db := s.db.WithContext(ctx)
	if err := db.Model(&demosvc.DemoItem{}).Count(&total).Error; err != nil {
		return page.OffsetResult[demosvc.DemoItem]{}, err
	}
	if err := p.Apply(db.Model(&demosvc.DemoItem{})).
		Select(selectColumns).
		Order("id DESC").
		Find(&items).Error; err != nil {
		return page.OffsetResult[demosvc.DemoItem]{}, err
	}
	return page.NewOffsetResult(items, p, total), nil
}

// Update 显式列更新 name / status（map 更新不受零值影响）；updated_at 由 autoUpdateTime 自动维护。
// 前置：item.ID 必须有效（service 层先 GetByID 取得）。
func (s *Store) Update(ctx context.Context, item *demosvc.DemoItem) error {
	return s.db.WithContext(ctx).Model(item).Updates(map[string]any{
		"name":   item.Name,
		"status": item.Status,
	}).Error
}

// Delete 硬删除；RowsAffected=0 时返回 gorm.ErrRecordNotFound（service 翻译为业务哨兵）。
func (s *Store) Delete(ctx context.Context, id uint64) error {
	res := s.db.WithContext(ctx).Delete(&demosvc.DemoItem{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
