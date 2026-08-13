package api

import "errors"

var (
	// ErrUnauthorized 未认证 / 凭据错误（401）。
	ErrUnauthorized = errors.New("UNAUTHORIZED")
	// ErrSessionExpired session 失效（401）。
	ErrSessionExpired = errors.New("SESSION_EXPIRED")
	// ErrUsernameConflict 用户名已存在（409）。
	ErrUsernameConflict = errors.New("USERNAME_CONFLICT")
)
