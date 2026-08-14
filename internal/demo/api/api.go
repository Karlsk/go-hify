// Package api 是 demo 模块的对外契约：CRUD 接口、Req/Schema、哨兵错误。
// 纯叶子包：只 import 标准库（binding tag 是纯字符串，不引入 gin 依赖）。
//
// demo 是 Hify 的标准 CRUD 参照实现（表见 migrations/00008_demo_items.sql），非业务数据；
// provider / agent 等真实模块起稿时参照本模块的四层结构，真实模块就绪后可整体移除。
package api

import "context"

// DemoService demo 模块 CRUD 契约；实现在本模块 service 包，由组合根注入。
type DemoService interface {
	// Create 创建 demo 条目。
	Create(ctx context.Context, req CreateReq) (*DemoItemSchema, error)
	// Get 取单条；不存在返回 ErrDemoItemNotFound。
	Get(ctx context.Context, req GetReq) (*DemoItemSchema, error)
	// List 偏移分页列表（page 从 1 起；demo 是小表，按接口规范"极小表用偏移分页"）。
	List(ctx context.Context, req ListReq) (*ListResult, error)
	// Update 整体更新（name + status 均必填）；不存在返回 ErrDemoItemNotFound。
	Update(ctx context.Context, req UpdateReq) (*DemoItemSchema, error)
	// Delete 硬删除；不存在返回 ErrDemoItemNotFound。
	Delete(ctx context.Context, req DeleteReq) error
}
