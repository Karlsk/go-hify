// Package traceid 提供请求级 trace_id 的生成与 ctx 注入 / 提取（CLAUDE.md §错误处理：
// 500 类只回 INTERNAL_ERROR + trace_id；全局规则第 19 条：日志贯穿 trace_id）。
//
// 与 authctx 同形态的 gin 无关叶子包：
// httpmw 中间件在请求入口生成并注入，logging 的 trace handler 从 ctx 提取成日志字段，
// respond 在 500 body 带回给前端——业务模块只经 ctx 传递，不感知具体机制。
//
// Java 对应物是 MDC（ThreadLocal 实现）；Go 无 ThreadLocal，惯用替代是 context.Context
// 值传递——ctx 随请求结束自然回收，不存在线程池复用导致的泄漏，无需「请求结束清理」。
package traceid

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

// Header 是携带 trace_id 的响应头名：RequestID 中间件在请求进入时即写入，
// 即使后续 handler panic，响应也带此头，前端 / 运维可据此与日志对账。
const Header = "X-Request-ID"

type ctxKey struct{}

// Generate 生成 16 字节随机 trace_id（32 位 hex，比 UUID 短且无连字符）。
// crypto/rand 失败意味着系统熵源不可用（不可恢复的不变量破坏），fail-fast panic。
func Generate() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("traceid: crypto/rand read failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// With 把 trace_id 注入 ctx；同 key 二次注入时新值遮蔽旧值。
func With(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// From 从 ctx 提取 trace_id；未注入返回 ok=false。
func From(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(ctxKey{}).(string)
	return id, ok
}
