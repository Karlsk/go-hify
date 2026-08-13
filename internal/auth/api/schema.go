// Package api 是 auth 模块的对外契约：Req/Schema 定义。
// 纯叶子包：只 import 标准库（binding tag 是纯字符串，不引入 gin 依赖）。
package api

import "time"

// CookieName session cookie 名（HttpOnly + SameSite=Lax）。
const CookieName = "hify_session"

// SessionTTL session 在 Redis 的存活时长。
const SessionTTL = 7 * 24 * time.Hour

// LoginReq 登录请求。
type LoginReq struct {
	Username string `json:"username" binding:"required,min=1,max=128"`
	Password string `json:"password" binding:"required,min=8,max=128"`
}

// Validate 跨字段校验（字段格式由 binding tag 管）；当前无跨字段规则。
func (r LoginReq) Validate() error { return nil }

// RegisterReq 注册请求。
type RegisterReq struct {
	Username string `json:"username" binding:"required,min=1,max=128"`
	Password string `json:"password" binding:"required,min=8,max=128"`
}

// Validate 跨字段校验（字段格式由 binding tag 管）；当前无跨字段规则。
func (r RegisterReq) Validate() error { return nil }

// UserSchema 用户信息（ID 序列化为字符串，防 JS 超 2^53 丢精度）。
type UserSchema struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
}

// SessionSchema 登录结果：token 由 handler 写入 cookie。
type SessionSchema struct {
	Token string     `json:"token"`
	User  UserSchema `json:"user"`
}
