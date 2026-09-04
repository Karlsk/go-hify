package api

import "errors"

// 哨兵错误：Error() 即机器可读错误码（MODULE_REASON，接口规范《错误处理》）。
// handler 用 errors.Is 映射 HTTP 状态码；新增码须同步 CLAUDE.md 错误码表。
var (
	// ErrConversationNotFound 会话不存在（404）。
	ErrConversationNotFound = errors.New("CONVERSATION_NOT_FOUND")

	// ErrModelContextTooLong 模型上下文超长（400，不可重试——改输入或开新会话）。
	// 由 chat service 把 platform/llm 的 InvalidRequest 分类翻译而来——放 chat/api 而非 platform/llm，
	// 因 chat service（业务域）做这次翻译，避免 platform/llm 反向依赖 chat。
	ErrModelContextTooLong = errors.New("MODEL_CONTEXT_TOO_LONG")
)
