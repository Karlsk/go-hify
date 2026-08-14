// Package service 是 demo 模块的业务层：实现 api 接口、定义 Store 接口、
// schema↔model 转换、错误翻译（gorm.ErrRecordNotFound → api 哨兵）。
package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"gorm.io/gorm"

	demoapi "github.com/Karlsk/go-hify/internal/demo/api"
	"github.com/Karlsk/go-hify/internal/platform/page"
)

// Store 数据层接口：定义在消费方（本包），store 包实现，组合根注入——依赖倒置。
// demo 是单表 CRUD、无跨表原子操作，故不需要 WithTx（事务边界规范见 CLAUDE.md《模块内部结构》）。
type Store interface {
	Create(ctx context.Context, item *DemoItem) error
	GetByID(ctx context.Context, id uint64) (*DemoItem, error)
	List(ctx context.Context, p page.OffsetParams) (page.OffsetResult[DemoItem], error)
	Update(ctx context.Context, item *DemoItem) error
	Delete(ctx context.Context, id uint64) error
}

type demoService struct {
	store Store
}

// New 返回 api 接口类型：组合根拿到后可直接注入 handler。
func New(store Store) demoapi.DemoService { return &demoService{store: store} }

// Create 创建 demo 条目。
func (s *demoService) Create(ctx context.Context, req demoapi.CreateReq) (*demoapi.DemoItemSchema, error) {
	item := &DemoItem{Name: req.Name, Status: req.Status}
	if err := s.store.Create(ctx, item); err != nil {
		return nil, fmt.Errorf("create demo item: %w", err)
	}
	return toSchema(item), nil
}

// Get 取单条；不存在翻译为哨兵 ErrDemoItemNotFound（边界处错误翻译）。
func (s *demoService) Get(ctx context.Context, req demoapi.GetReq) (*demoapi.DemoItemSchema, error) {
	item, err := s.store.GetByID(ctx, req.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, demoapi.ErrDemoItemNotFound
		}
		return nil, fmt.Errorf("get demo item %d: %w", req.ID, err)
	}
	return toSchema(item), nil
}

// List 偏移分页列表：参数归一化（page.NewOffset）→ store 查询 → model 批量转 schema。
func (s *demoService) List(ctx context.Context, req demoapi.ListReq) (*demoapi.ListResult, error) {
	p := page.NewOffset(req.Page, req.PageSize)
	res, err := s.store.List(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("list demo items: %w", err)
	}
	items := make([]demoapi.DemoItemSchema, 0, len(res.Items))
	for i := range res.Items {
		items = append(items, *toSchema(&res.Items[i]))
	}
	return &demoapi.ListResult{Items: items, Page: res.Page, PageSize: res.PageSize, Total: res.Total}, nil
}

// Update 整体更新：先取（不存在 → 哨兵）→ 覆盖 name/status → 落库。
// updated_at 由 GORM autoUpdateTime 维护（见 platform/db.BaseMutable）。
func (s *demoService) Update(ctx context.Context, req demoapi.UpdateReq) (*demoapi.DemoItemSchema, error) {
	item, err := s.store.GetByID(ctx, req.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, demoapi.ErrDemoItemNotFound
		}
		return nil, fmt.Errorf("get demo item %d: %w", req.ID, err)
	}
	item.Name = req.Name
	item.Status = req.Status
	if err := s.store.Update(ctx, item); err != nil {
		return nil, fmt.Errorf("update demo item %d: %w", req.ID, err)
	}
	return toSchema(item), nil
}

// Delete 硬删除；不存在翻译为哨兵 ErrDemoItemNotFound。
func (s *demoService) Delete(ctx context.Context, req demoapi.DeleteReq) error {
	if err := s.store.Delete(ctx, req.ID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return demoapi.ErrDemoItemNotFound
		}
		return fmt.Errorf("delete demo item %d: %w", req.ID, err)
	}
	return nil
}

// toSchema model → schema 转换（边界处；ID 字符串化防 JS 超 2^53 丢精度）。
func toSchema(item *DemoItem) *demoapi.DemoItemSchema {
	return &demoapi.DemoItemSchema{
		ID:        strconv.FormatUint(item.ID, 10),
		Name:      item.Name,
		Status:    item.Status,
		CreatedAt: item.CreatedAt,
		UpdatedAt: item.UpdatedAt,
	}
}
