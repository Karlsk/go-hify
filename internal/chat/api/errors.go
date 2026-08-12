package api

import "errors"

// ErrModelContextTooLong 模型上下文超长（400）。
// 由 chat service 把 platform/llm 的 InvalidRequest 分类翻译而来——放 chat/api 而非 platform/llm，
// 因 chat service（业务域）做这次翻译，避免 platform/llm 反向依赖 chat。
var ErrModelContextTooLong = errors.New("MODEL_CONTEXT_TOO_LONG")
