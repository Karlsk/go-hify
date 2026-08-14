// Package llm 是 Hify 所有外部 LLM 调用的唯一通道：错误分类、bulkhead、熔断、三层超时、重试
// （CLAUDE.md《外部 LLM 调用设计》）。adapter（eino 各家适配）实现 Streamer 接口注入。
package llm

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Class 统一错误分类（CLAUDE.md《错误分类》）。adapter 负责把各家错误格式翻译过来。
// 分类决定三件事：重不重试、计不计熔断、给用户看什么。
type Class string

const (
	ClassTimeout        Class = "Timeout"
	ClassRateLimited    Class = "RateLimited"
	ClassOverloaded     Class = "Overloaded"
	ClassNetwork        Class = "Network"
	ClassInvalidRequest Class = "InvalidRequest"
	ClassAuth           Class = "Auth"
	ClassProviderDown   Class = "ProviderDown"
)

// classSpec 分类表（CLAUDE.md《错误分类》后两列）：
// retryable 是否可在首 token 前重试；countsTowardBreaker 重试耗尽后是否计入熔断失败。
var classSpec = map[Class]struct{ retryable, countsTowardBreaker bool }{
	ClassTimeout:        {true, true},
	ClassRateLimited:    {true, false}, // 限流 ≠ 故障，不触发熔断
	ClassOverloaded:     {true, true},
	ClassNetwork:        {true, true},
	ClassInvalidRequest: {false, false}, // 重试也不会好
	ClassAuth:           {false, false},
	ClassProviderDown:   {false, true},
}

// Retryable 报告该分类是否可重试。
func (c Class) Retryable() bool { return classSpec[c].retryable }

// CountsTowardBreaker 报告该分类的最终失败是否计入熔断。
func (c Class) CountsTowardBreaker() bool { return classSpec[c].countsTowardBreaker }

// Error 统一 LLM 错误：分类 + 原始错误。adapter 在翻译边界构造；调用方用 Classify 提取分类。
type Error struct {
	Class Class
	Err   error
	// RetryAfter 由 429 的 Retry-After 头解析（>0 有效）；重试逻辑尊重它。
	RetryAfter time.Duration
}

func (e *Error) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("llm %s", e.Class)
	}
	return fmt.Sprintf("llm %s: %v", e.Class, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// Classify 从错误链提取分类（errors.As 递归查找 *Error）。
func Classify(err error) (Class, bool) {
	var le *Error
	if errors.As(err, &le) {
		return le.Class, true
	}
	return "", false
}

var (
	// ErrProviderBusy bulkhead 抢槽失败（fail-fast，HTTP 503）。
	ErrProviderBusy = errors.New("PROVIDER_BUSY")
	// ErrProviderUnavailable 熔断打开 / ProviderDown（HTTP 503）。
	ErrProviderUnavailable = errors.New("PROVIDER_UNAVAILABLE")
	// ErrUnsupportedKind provider kind 不在 adapter 白名单内（factory 构造期拒绝）。
	ErrUnsupportedKind = errors.New("llm: unsupported provider kind")
)

// 三层超时的 ctx cause 哨兵（CLAUDE.md：一律 WithTimeoutCause + context.Cause 区分，
// 排障时能分清"首字慢"还是"中途断流"，是运行日志"错误类"字段的来源）。
var (
	// ErrTTFT 首 token 超时。
	ErrTTFT = errors.New("llm: first token timeout")
	// ErrIdle 流空闲超时（每 chunk 重置的看门狗到点）。
	ErrIdle = errors.New("llm: idle timeout")
	// ErrOverall 整体超时（包住重试在内的全过程）。
	ErrOverall = errors.New("llm: overall timeout")
)

// classifyCtxErr 把 ctx 错误翻译成分类错误：超时类哨兵 → ClassTimeout；
// 客户端断连（context.Canceled）原样返回，让调用方感知取消（省 token 语义）。
// 已分类（adapter 返回）或非 ctx 错误原样返回。
func classifyCtxErr(err error, ctx context.Context) error {
	if err == nil {
		return nil
	}
	if _, ok := Classify(err); ok {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		switch context.Cause(ctx) {
		case ErrTTFT, ErrIdle, ErrOverall:
			return &Error{Class: ClassTimeout, Err: err}
		default:
			return err
		}
	}
	return err
}
