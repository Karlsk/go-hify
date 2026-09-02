package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/sony/gobreaker"
)

// Streamer 上游适配器的窄接口（Go 小接口惯例）。eino 各家 adapter 实现；
// Stream 返回的 StreamReader 生命周期与传入 ctx 绑定，ctx 取消即打断 Recv。
// opts 可为 nil（空选项）；工具与生成参数按调用传入（eino 调用时选项）。
type Streamer interface {
	Stream(ctx context.Context, msgs []*schema.Message, opts *CallOptions) (*schema.StreamReader[*schema.Message], error)
	Generate(ctx context.Context, msgs []*schema.Message, opts *CallOptions) (*schema.Message, error)
}

// gate provider 级共享防护设施：bulkhead 槽位 + 熔断器 + profile。
// CLAUDE.md 契约：每供应商 16 槽、每供应商一个熔断器——同一 provider 的所有模型 Client
// 共享同一 gate；Manager 按此粒度缓存（见 manager.go 双层缓存）。
type gate struct {
	name    string // provider 名：熔断器标识与错误归属
	profile Profile
	bh      *bulkhead
	cb      *gobreaker.CircuitBreaker
}

func newGate(name string, p Profile) *gate {
	return &gate{name: name, profile: p, bh: newBulkhead(p), cb: newBreaker(p, name)}
}

// Client 受保护的 LLM 调用客户端（每 provider+model 一个；gate 按 provider 共享）。
// 调用链（CLAUDE.md《调用链》）：抢槽位 → 过熔断器 → 重试循环（仅首 token 前）→ 三层超时 → 受保护调用。
type Client struct {
	name     string // 调用标识（provider 名）
	profile  Profile
	upstream Streamer
	g        *gate // 共享防护设施（槽位 + 熔断），同 provider 的各模型 Client 指向同一 gate
}

// NewClient 创建受保护客户端（自建独立 gate）。name 用于熔断器与错误标识（provider 名）。
func NewClient(name string, p Profile, up Streamer) *Client {
	return newClientWithGate(name, newGate(name, p), up)
}

// newClientWithGate 注入共享 gate 构造 Client（Manager 双层缓存用：同 provider 多模型
// 共享槽位与熔断）；profile 取 gate 的（构造时快照）。
func newClientWithGate(name string, g *gate, up Streamer) *Client {
	return &Client{name: name, profile: g.profile, upstream: up, g: g}
}

// streamResult 熔断 Execute 的结果载体：不计熔断的错误经它返回，gobreaker 只看到"计熔断"的失败。
type streamResult struct {
	stream *Stream
	err    error
}

// generateResult 同 streamResult，Generate 路径的结果载体。
type generateResult struct {
	msg *schema.Message
	err error
}

// Stream 对外入口：返回受保护的流；错误已被分类（调用方用 Classify / errors.Is 判定）。
// opts 可为 nil（空选项）。
func (c *Client) Stream(ctx context.Context, msgs []*schema.Message, opts *CallOptions) (*Stream, error) {
	// 1. 抢槽（5s fail-fast，不无限排队）
	release, err := c.g.bh.acquire(ctx)
	if err != nil {
		return nil, err // errors.Is → ErrProviderBusy
	}

	// 2. overall 超时最外层（包住重试全过程；cancel 挂到 Stream 上，流结束才释放）
	overallCtx, overallCancel := context.WithTimeoutCause(ctx, c.profile.Overall, ErrOverall)

	// 3. 过熔断器：整个重试循环在 Execute 内（重试过程中的失败不逐次计数）
	res, cbErr := c.g.cb.Execute(func() (any, error) {
		s, err := c.doStream(overallCtx, msgs, opts)
		if err == nil {
			return streamResult{stream: s}, nil
		}
		if class, ok := Classify(err); ok && !class.CountsTowardBreaker() {
			return streamResult{err: err}, nil // 429 等不计熔断：走结果通道
		}
		return streamResult{err: err}, err // 计熔断的最终失败
	})
	if cbErr != nil {
		release()
		overallCancel()
		// 熔断已打开 / 半开并发超限 → 秒拒语义；其余（触发熔断的那次最终失败）保留原分类
		if errors.Is(cbErr, gobreaker.ErrOpenState) || errors.Is(cbErr, gobreaker.ErrTooManyRequests) {
			return nil, fmt.Errorf("%w: %s: %v", ErrProviderUnavailable, c.name, cbErr)
		}
		return nil, cbErr
	}

	r := res.(streamResult)
	if r.err != nil {
		release()
		overallCancel()
		return nil, r.err
	}
	r.stream.release = release
	r.stream.overallCancel = overallCancel
	return r.stream, nil
}

// doStream 重试循环（仅首 token 前）。每次尝试前检查 ctx：客户端断连/overall 到期不再重试。
func (c *Client) doStream(ctx context.Context, msgs []*schema.Message, opts *CallOptions) (*Stream, error) {
	var lastErr error
	for attempt := 0; attempt <= c.profile.MaxRetries; attempt++ {
		if attempt > 0 {
			if err := c.backoff(ctx, attempt-1, lastErr); err != nil {
				return nil, classifyCtxErr(err, ctx)
			}
		}
		s, err := c.tryAttempt(ctx, msgs, opts)
		if err == nil {
			return s, nil
		}
		lastErr = err
		if !c.shouldRetry(ctx, attempt, err) {
			return nil, err
		}
	}
	return nil, lastErr
}

// tryAttempt 单次尝试：建立流 + 等首 chunk（TTFT 阶段），成功则返回带 idle 看门狗的 Stream。
//
// TTFT 用 AfterFunc + cancel cause（而非 WithTimeoutCause）：首 chunk 后要"解除" TTFT 计时，
// 但不能 cancel 流 ctx——eino StreamReader 绑定创建时的 ctx，cancel 会杀掉整个流。
func (c *Client) tryAttempt(ctx context.Context, msgs []*schema.Message, opts *CallOptions) (*Stream, error) {
	attemptCtx, attemptCancel := context.WithCancelCause(ctx)
	ttftTimer := time.AfterFunc(c.profile.TTFT, func() { attemptCancel(ErrTTFT) })

	reader, err := c.upstream.Stream(attemptCtx, msgs, opts)
	if err != nil {
		ttftTimer.Stop()
		attemptCancel(nil)
		return nil, classifyCtxErr(err, attemptCtx)
	}

	// 等首 chunk（阻塞在 Recv，TTFT timer 保护）
	first, err := reader.Recv()
	if err != nil {
		ttftTimer.Stop()
		reader.Close()
		attemptCancel(nil)
		return nil, classifyCtxErr(err, attemptCtx)
	}
	ttftTimer.Stop() // 首 chunk 到达：TTFT 失效，切换 idle 看门狗（每 chunk 重置）

	watchdog := newIdleWatchdog(attemptCancel, c.profile.Idle)
	return &Stream{
		reader:   reader,
		first:    first,
		watchdog: watchdog,
		ctx:      attemptCtx,
		cancel:   attemptCancel,
	}, nil
}

// Generate 对外入口：非流式单次生成（workflow LLM 节点等无 SSE 场景）。错误已分类。
// 与 Stream 同一调用链（抢槽 → overall → 熔断 → 重试），但无 TTFT / idle 看门狗——
// 非流式单次返回，overall 一层超时足够；usage 在返回消息的 ResponseMeta 里。
// 槽位在函数返回时释放（无 wrapStream 概念）。opts 可为 nil。
func (c *Client) Generate(ctx context.Context, msgs []*schema.Message, opts *CallOptions) (*schema.Message, error) {
	// 1. 抢槽（5s fail-fast，不无限排队）；非流式无流对象，返回即释放
	release, err := c.g.bh.acquire(ctx)
	if err != nil {
		return nil, err // errors.Is → ErrProviderBusy
	}
	defer release()

	// 2. overall 超时包住重试全过程
	overallCtx, overallCancel := context.WithTimeoutCause(ctx, c.profile.Overall, ErrOverall)
	defer overallCancel()

	// 3. 过熔断器（429 等不计熔断，同 Stream 的结果载体模式）
	res, cbErr := c.g.cb.Execute(func() (any, error) {
		m, err := c.doGenerate(overallCtx, msgs, opts)
		if err == nil {
			return generateResult{msg: m}, nil
		}
		if class, ok := Classify(err); ok && !class.CountsTowardBreaker() {
			return generateResult{err: err}, nil
		}
		return generateResult{err: err}, err
	})
	if cbErr != nil {
		// 熔断已打开 / 半开并发超限 → 秒拒语义；其余（触发熔断的那次最终失败）保留原分类
		if errors.Is(cbErr, gobreaker.ErrOpenState) || errors.Is(cbErr, gobreaker.ErrTooManyRequests) {
			return nil, fmt.Errorf("%w: %s: %v", ErrProviderUnavailable, c.name, cbErr)
		}
		return nil, cbErr
	}

	r := res.(generateResult)
	return r.msg, r.err
}

// doGenerate 非流式重试循环。非流式不存在"首 token 后"——调用失败即未产出任何 token，
// 可重试判定与 doStream 一致（分类表可重试 ∧ ctx 未取消 ∧ Retry-After 未超封顶）。
func (c *Client) doGenerate(ctx context.Context, msgs []*schema.Message, opts *CallOptions) (*schema.Message, error) {
	var lastErr error
	for attempt := 0; attempt <= c.profile.MaxRetries; attempt++ {
		if attempt > 0 {
			if err := c.backoff(ctx, attempt-1, lastErr); err != nil {
				return nil, classifyCtxErr(err, ctx)
			}
		}
		m, err := c.upstream.Generate(ctx, msgs, opts)
		if err == nil {
			return m, nil
		}
		err = classifyCtxErr(err, ctx)
		lastErr = err
		if !c.shouldRetry(ctx, attempt, err) {
			return nil, err
		}
	}
	return nil, lastErr
}

// shouldRetry 可重试判定（CLAUDE.md《重试纪律》）= 分类表"可重试"列
// ∧ 尚未吐出任何 token ∧ 客户端未断连 ∧ Retry-After 未超封顶。
func (c *Client) shouldRetry(ctx context.Context, attempt int, err error) bool {
	if attempt >= c.profile.MaxRetries {
		return false
	}
	if ctx.Err() != nil {
		return false // 客户端断连/overall 到期：不再重试（省 token）
	}
	class, ok := Classify(err)
	if !ok || !class.Retryable() {
		return false
	}
	var le *Error
	if errors.As(err, &le) && le.RetryAfter > c.profile.RetryAfterCap {
		return false // Retry-After 超封顶：直接放弃，报"供应商限流，请稍后再试"
	}
	return true
}

// backoff 重试前睡眠：429 尊重 Retry-After（解析失败回退退避）；
// 其余指数 + 满抖动 rand(0, min(cap, base×2^attempt))。ctx 取消立即返回。
func (c *Client) backoff(ctx context.Context, attempt int, err error) error {
	var d time.Duration
	var le *Error
	if errors.As(err, &le) && le.Class == ClassRateLimited && le.RetryAfter > 0 {
		d = le.RetryAfter // ≤ RetryAfterCap 已由 shouldRetry 保证
	} else {
		cap := c.profile.BackoffBase << attempt
		if cap > c.profile.BackoffCap || cap <= 0 {
			cap = c.profile.BackoffCap
		}
		d = time.Duration(rand.Int64N(int64(cap)))
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Stream 受保护的流式结果：Recv 读 chunk（首 chunk 已缓存）；Close 取消上游 + 释放槽位。
// 流走到终点（EOF / 错误）自动清理一次——防调用方忘记 Close 泄漏槽位（16 槽耗尽 = 503 风暴）。
type Stream struct {
	reader   *schema.StreamReader[*schema.Message]
	first    *schema.Message
	watchdog *idleWatchdog
	ctx      context.Context         // attemptCtx：终点错误分类用（Cause 区分 TTFT/idle/断连）
	cancel   context.CancelCauseFunc // 取消上游（客户端断连 → 省 token）
	// 由 Client.Stream 在返回前注入：
	release       func() // 槽位释放（幂等）
	overallCancel context.CancelFunc
	once          sync.Once
}

// Recv 返回下一个 chunk；io.EOF 表示流正常结束。
// 首 chunk 后的错误同样经 classifyCtxErr 分类（idle 超时 → ClassTimeout），调用方统一用 Classify 判定。
func (s *Stream) Recv() (*schema.Message, error) {
	if s.first != nil {
		m := s.first
		s.first = nil
		return m, nil
	}
	m, err := s.reader.Recv()
	if err == nil {
		s.watchdog.Ping()
		return m, nil
	}
	s.close() // 终点自动清理（槽位回到池子）
	return nil, classifyCtxErr(err, s.ctx)
}

// Close 主动关闭：停止看门狗、取消上游调用（省 token）、释放槽位。幂等。
func (s *Stream) Close() error {
	s.close()
	return nil
}

func (s *Stream) close() {
	s.once.Do(func() {
		if s.watchdog != nil {
			s.watchdog.Stop()
		}
		s.cancel(context.Canceled)
		s.reader.Close()
		if s.release != nil {
			s.release()
		}
		if s.overallCancel != nil {
			s.overallCancel()
		}
	})
}

// 编译期断言：Recv 结束后调用方无需再 Close（io.EOF 语义由 eino 保证）。
var _ io.Closer = (*Stream)(nil)
