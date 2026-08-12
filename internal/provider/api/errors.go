package api

import "errors"

// provider 模块业务哨兵（CRUD 侧）。LLM 调用侧的 Busy/Unavailable 见 platform/llm。
// code（Error() 字符串）与 CLAUDE.md《错误处理》错误码表一一对应；handler 用 errors.Is 映射状态码。
var (
	// ErrProviderNotFound 提供商不存在（404）。
	ErrProviderNotFound = errors.New("PROVIDER_NOT_FOUND")
	// ErrProviderNameConflict 提供商名称唯一约束冲突（409）。
	ErrProviderNameConflict = errors.New("PROVIDER_NAME_CONFLICT")
)
