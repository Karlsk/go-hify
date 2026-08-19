// Package api 的 Req/Schema 定义。
// 纯叶子包：只 import 标准库（binding tag 是纯字符串，不引入 gin 依赖）。
package api

import (
	"fmt"
	"time"
)

// Status 枚举合法值（text + CHECK，见 migrations/00008_demo_items.sql）。
const (
	StatusDraft    = "draft"
	StatusActive   = "active"
	StatusArchived = "archived"
)

// validStatus 状态枚举白名单，供 Validate 跨字段校验用。
var validStatus = map[string]bool{
	StatusDraft:    true,
	StatusActive:   true,
	StatusArchived: true,
}

// validateNameStatus 创建 / 更新共用的跨字段校验（统一校验）：
// name 非空 + status 必须在枚举白名单内。字段级格式由 binding tag 管，此处管跨字段规则。
func validateNameStatus(name, status string) error {
	if name == "" {
		return fmt.Errorf("name 不能为空")
	}
	if !validStatus[status] {
		return fmt.Errorf("status 必须是 %s / %s / %s 之一", StatusDraft, StatusActive, StatusArchived)
	}
	return nil
}

// CreateReq 创建请求。
type CreateReq struct {
	Name   string `json:"name" binding:"required,min=1,max=128"`
	Status string `json:"status" binding:"required"`
}

// Validate 跨字段校验（字段格式由 binding tag 管）。创建 / 更新统一走 validateNameStatus。
func (r CreateReq) Validate() error { return validateNameStatus(r.Name, r.Status) }

// UpdateReq 整体更新请求（PUT：name + status 均必填）。
// ID 带 json:"-" 且无 binding tag：gin 的 BindUri 会校验整个结构体，与 json 字段两步绑定会
// 互相干扰，故 handler 先用 GetReq 绑路径 id 再赋值给 ID（见 handler.update）。json:"-" 阻止
// body 的 id 键覆盖路径值（encoding/json 对无 tag 字段按字段名大小写不敏感匹配，裸 ID 会被
// {"id":999} 悄悄改写）；ID>0 由 Validate 兜底。
type UpdateReq struct {
	ID     uint64 `json:"-"`
	Name   string `json:"name" binding:"required,min=1,max=128"`
	Status string `json:"status" binding:"required"`
}

// Validate 跨字段校验（字段格式由 binding tag 管）。创建 / 更新统一走 validateNameStatus。
func (r UpdateReq) Validate() error {
	if r.ID == 0 {
		return fmt.Errorf("id 必填")
	}
	return validateNameStatus(r.Name, r.Status)
}

// GetReq / DeleteReq 单条取 / 删请求（路径参数 id）。
type GetReq struct {
	ID uint64 `uri:"id" binding:"required"`
}

// Validate 跨字段校验；当前无跨字段规则。
func (r GetReq) Validate() error { return nil }

// DeleteReq 删除请求（路径参数 id）。
type DeleteReq struct {
	ID uint64 `uri:"id" binding:"required"`
}

// Validate 跨字段校验；当前无跨字段规则。
func (r DeleteReq) Validate() error { return nil }

// ListReq 偏移分页列表请求（page 从 1 起，page_size 上限 100，见 platform/page）。
type ListReq struct {
	Page     int `form:"page" binding:"omitempty,min=1"`
	PageSize int `form:"page_size" binding:"omitempty,min=1,max=100"`
}

// Validate 跨字段校验；当前无跨字段规则（page/page_size 归一化由 platform/page 负责）。
func (r ListReq) Validate() error { return nil }

// DemoItemSchema demo 条目响应（ID 序列化为字符串，防 JS 超 2^53 丢精度）。
type DemoItemSchema struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ListResult 偏移分页结果（= Java 的 PageResult；Go 侧对应 platform/page.OffsetResult，
// service 层把 OffsetResult[DemoItem] 的字段喂给 respond.OKWithOffset）。
type ListResult struct {
	Items    []DemoItemSchema
	Page     int
	PageSize int
	Total    int64
}
