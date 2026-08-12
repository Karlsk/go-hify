package api

import "errors"

var (
	// ErrUnauthorized 未认证 / 未登录（401）。
	ErrUnauthorized = errors.New("UNAUTHORIZED")
	// ErrSessionExpired session 失效（401）。
	ErrSessionExpired = errors.New("SESSION_EXPIRED")
)
