// Package schema 提供各模块 api 契约共用的响应基类。
// 仅依赖标准库——api 叶子包（不 import gin/gorm）可安全引用，与 platform/db 的 model mixin 对称：
// db mixin 管表头（GORM 侧），本包管响应表头（JSON 侧）。
package schema

import "time"

// BaseSchema 可变表响应基类：ID 序列化为字符串（防 JS 超 2^53 丢精度）+ 双时间戳。
// 各模块 XxxSchema embed 本结构获得统一表头字段。
//
// 不适用的两类表自行声明：主键非代理 id（如 provider_health 的 provider_id）、
// append-only 表（无 updated_at）。
type BaseSchema struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
