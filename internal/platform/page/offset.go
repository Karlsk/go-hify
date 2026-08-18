package page

import (
	"math"

	"gorm.io/gorm"
)

// OffsetParams 是偏移分页的查询参数（仅极小静态配置表用，见 CLAUDE.md《分页》）。
// Page 从 1 起；PageSize 经 NewOffset 归一化为 [1, MaxPageSize]。
// 仅用于 providers/agents/models/mcp_servers/knowledge_bases/workflows 等行数极小、total 可精确算、
// OFFSET 无性能问题的表——大列表必须用游标分页（CursorParams）。
type OffsetParams struct {
	Page     int
	PageSize int
}

// NewOffset 把前端传入的 page/pageSize 归一化为合法 OffsetParams：
//   - page < 1 → 1（偏移分页从 1 起）；
//   - pageSize <= 0 → DefaultPageSize（20）；
//   - pageSize > MaxPageSize → MaxPageSize（100）。
func NewOffset(page, pageSize int) OffsetParams {
	if page < 1 {
		page = 1
	}
	switch {
	case pageSize <= 0:
		pageSize = DefaultPageSize
	case pageSize > MaxPageSize:
		pageSize = MaxPageSize
	}
	return OffsetParams{Page: page, PageSize: pageSize}
}

// Offset 返回 db.Offset(n) 的 n：(Page-1) * PageSize。Page 从 1 起 → 第一页 offset=0。
// 饱和乘法防溢出：超大 Page 的乘积在定宽整数上回绕（曾致负偏移切片 panic / 非法 SQL），
// 超过 math.MaxInt 即封顶——越界页返回空结果，绝不产生负偏移。
func (p OffsetParams) Offset() int {
	if p.Page <= 1 || p.PageSize <= 0 {
		return 0
	}
	pages, size := int64(p.Page)-1, int64(p.PageSize)
	if pages > int64(math.MaxInt)/size {
		return math.MaxInt
	}
	return int(pages * size)
}

// Limit 返回 db.Limit(n) 的 n，即 PageSize。
func (p OffsetParams) Limit() int {
	return p.PageSize
}

// Apply 把 Offset 与 Limit 一次性链到 *gorm.DB，便于 store 一次写完分页查询：
//
//	p.Apply(db.WithContext(ctx).Where(...)).Find(&xs)
func (p OffsetParams) Apply(db *gorm.DB) *gorm.DB {
	return db.Offset(p.Offset()).Limit(p.Limit())
}

// OffsetResult[T] 是偏移分页的查询结果：列表 + 总数 + 分页信息。
// Items 为 []T（nil 经 NewOffsetResult 兜底为空 slice，满足 CLAUDE.md《空值约定》列表空返回 []）。
type OffsetResult[T any] struct {
	Items    []T
	Page     int
	PageSize int
	Total    int64
}

// NewOffsetResult 组装偏移分页结果：填入归一化后的 Page/PageSize 与精确 Total。
// items 为 nil 时置为 []T{}，避免序列化成 null（CLAUDE.md《空值约定》）。
func NewOffsetResult[T any](items []T, p OffsetParams, total int64) OffsetResult[T] {
	if items == nil {
		items = []T{}
	}
	return OffsetResult[T]{Items: items, Page: p.Page, PageSize: p.PageSize, Total: total}
}

// TotalPages 返回总页数（向上取整）。Total<=0 或 PageSize<=0 → 0。
func (r OffsetResult[T]) TotalPages() int {
	if r.PageSize <= 0 || r.Total <= 0 {
		return 0
	}
	return int((r.Total + int64(r.PageSize) - 1) / int64(r.PageSize))
}
