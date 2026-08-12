package respond

import "github.com/gin-gonic/gin"

// CursorMeta 是大列表游标分页的 meta（默认模式，见 CLAUDE.md《分页》）。
// 用于 conversations / messages / executions 等会增长的列表，配套 keyset 查询、禁用 OFFSET。
//
// JSON 约定：has_more=false 时 next_cursor 为 null（不是空串）—— NextCursor 用 *string
// 承载，OKWithCursor 会把空串自动转成 nil。
type CursorMeta struct {
	Limit      int     `json:"limit"`
	HasMore    bool    `json:"has_more"`
	NextCursor *string `json:"next_cursor"`
}

// OffsetMeta 仅用于极小静态配置表的偏移分页（见 CLAUDE.md《分页》）。
// 用于 providers / agents / models / mcp_servers / knowledge_bases / workflows 等
// 行数极小、total 可精确算的表，原生适配 Element Plus 分页组件。
type OffsetMeta struct {
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
	Total    int64 `json:"total"`
}

// OKWithCursor 写 200 + 列表 + 游标分页 meta。
// 调用方按 keyset 查询结果传入：limit=本次页大小、hasMore=是否还有下一页、
// nextCursor=下一页游标（base64 排序键；无下一页传 ""，将序列化为 null）。
func OKWithCursor(c *gin.Context, list any, limit int, hasMore bool, nextCursor string) {
	var nc *string
	if nextCursor != "" {
		nc = &nextCursor
	}
	OKWithMeta(c, list, CursorMeta{Limit: limit, HasMore: hasMore, NextCursor: nc})
}

// OKWithOffset 写 200 + 列表 + 偏移分页 meta（仅极小静态配置表用）。
// page 从 1 起，pageSize 为本次页大小，total 为精确总数。
func OKWithOffset(c *gin.Context, list any, page, pageSize int, total int64) {
	OKWithMeta(c, list, OffsetMeta{Page: page, PageSize: pageSize, Total: total})
}
