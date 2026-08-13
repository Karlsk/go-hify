// Package authctx 提供登录用户身份的 ctx 注入与提取（CLAUDE.md《认证》）：
// 用户身份随 ctx 注入下游，跨模块调用与 HTTP 复用同一套接口；
// 限流 / 预算在 platform 层按 ctx 内用户计数。
//
// 放在 platform 而非 auth/api：业务模块（chat / agent 等）不得依赖 auth
// （CLAUDE.md 依赖清单），身份类型必须下沉到 platform。
package authctx

import "context"

// User 登录用户的最小身份（各模块按需提取；禁止塞入会膨胀的字段）。
type User struct {
	ID       uint64
	Username string
}

type ctxKey struct{}

// WithUser 把用户身份注入 ctx。
func WithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

// UserFrom 从 ctx 提取用户身份；未注入返回 false。
func UserFrom(ctx context.Context) (*User, bool) {
	u, ok := ctx.Value(ctxKey{}).(*User)
	return u, ok
}
