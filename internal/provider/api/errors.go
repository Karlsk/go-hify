package api

import "errors"

// provider 模块业务哨兵（CRUD 侧）。LLM 调用侧的 Busy/Unavailable 见 platform/llm。
// code（Error() 字符串）与 CLAUDE.md《错误处理》错误码表一一对应；handler 用 errors.Is 映射状态码。
var (
	// ErrProviderNotFound 提供商不存在（404）。
	ErrProviderNotFound = errors.New("PROVIDER_NOT_FOUND")
	// ErrProviderNameConflict 提供商名称唯一约束冲突（409）。
	ErrProviderNameConflict = errors.New("PROVIDER_NAME_CONFLICT")
	// ErrModelNotFound 模型不存在（404）。
	ErrModelNotFound = errors.New("MODEL_NOT_FOUND")
	// ErrModelIDConflict 同一提供商下模型标识唯一约束冲突（409，uq(provider_id, model_id)）。
	ErrModelIDConflict = errors.New("MODEL_ID_CONFLICT")
	// ErrModelInUse 模型被 agents / knowledge_bases 引用，删除被外键挡住（409，提示先解绑）。
	ErrModelInUse = errors.New("MODEL_IN_USE")
	// ErrProviderDisabled 提供商已停用，拒绝新调用（503）。
	ErrProviderDisabled = errors.New("PROVIDER_DISABLED")
)
