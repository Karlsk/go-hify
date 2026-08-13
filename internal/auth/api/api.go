// Package api 是 auth 模块的对外契约：跨模块接口、Req/Schema、哨兵错误。
// 纯叶子包：只 import 标准库（binding tag 是纯字符串，不引入 gin 依赖）。
package api

import "context"

// AuthService 跨模块调用接口；实现在本模块 service 包，由组合根注入。
type AuthService interface {
	// Register 注册新用户（内部工具开放注册，用户名唯一）。
	Register(ctx context.Context, req RegisterReq) (*UserSchema, error)
	// Login 校验凭据，返回 session token 与用户信息。
	Login(ctx context.Context, req LoginReq) (*SessionSchema, error)
	// Logout 使 session 失效（幂等）。
	Logout(ctx context.Context, token string) error
	// Validate 校验 session token 并返回登录用户；供中间件调用。
	Validate(ctx context.Context, token string) (*UserSchema, error)
	// Me 返回 ctx 内的当前登录用户（身份由中间件注入）。
	Me(ctx context.Context) (*UserSchema, error)
}
