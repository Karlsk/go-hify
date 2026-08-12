// Package errs 定义跨业务域的通用哨兵错误。
//
// 这是 gin 无关、不依赖任何业务模块的叶子包，供三处共用：
//   - platform 基建层（platform/llm 熔断、platform/budget 预算/限流）产生部分哨兵；
//   - HTTP 层（handler 绑定失败、respond.Error 兜底）产生 ErrValidationFailed / ErrInternal；
//   - respond.FailFromSentinel 与各 handler 用 errors.Is 判定后映射状态码。
//
// 约定：每个哨兵的 Error() 字符串即其机器可读 code
// （如 ErrValidationFailed.Error() == "VALIDATION_FAILED"），与 CLAUDE.md《错误处理》错误码表一一对应。
//
// 说明：此处刻意以全大写 code 作为 error 文本，偏离「error 字符串小写」的通用 Go 习惯——
// 因为它们是机器可读 code 而非人类语句；人类可读上下文由调用方 fmt.Errorf("load provider %d: %w", ...)
// 叠加。模块业务哨兵（各 api/errors.go）与 platform/llm 操作哨兵遵循同一约定。
package errs

import "errors"

var (
	// ErrValidationFailed 参数绑定 / 校验失败（400）。
	ErrValidationFailed = errors.New("VALIDATION_FAILED")
	// ErrInternal 未预期错误（500）：细节进结构化日志、不回前端。
	ErrInternal = errors.New("INTERNAL_ERROR")
	// ErrRateLimited 供应商 429 透传 / 用户限流（429）。
	ErrRateLimited = errors.New("RATE_LIMITED")
	// ErrBudgetExhausted 每日预算耗尽，拒绝新会话（429，fail-open 不阻断在线会话）。
	ErrBudgetExhausted = errors.New("BUDGET_EXHAUSTED")
	// ErrServiceUnavailable 服务暂不可用（503，通用兜底）；provider 专属的不可用见 platform/llm。
	ErrServiceUnavailable = errors.New("SERVICE_UNAVAILABLE")
)
