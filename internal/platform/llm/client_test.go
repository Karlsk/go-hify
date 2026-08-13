package llm

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
)

// ---- fake 上游 ----

type fakeStreamer struct {
	calls atomic.Int64
	fn    func(call int, ctx context.Context) (*schema.StreamReader[*schema.Message], error)
}

func (f *fakeStreamer) Stream(ctx context.Context, _ []*schema.Message) (*schema.StreamReader[*schema.Message], error) {
	return f.fn(int(f.calls.Add(1)), ctx)
}

func msg(content string) *schema.Message {
	return &schema.Message{Role: schema.Assistant, Content: content}
}

// okStreamer 每次调用返回含给定 chunks 的流（发完正常 EOF）。
func okStreamer(chunks ...*schema.Message) *fakeStreamer {
	return &fakeStreamer{fn: func(int, context.Context) (*schema.StreamReader[*schema.Message], error) {
		r, w := schema.Pipe[*schema.Message](len(chunks) + 1)
		go func() {
			for _, c := range chunks {
				w.Send(c, nil)
			}
			w.Close()
		}()
		return r, nil
	}}
}

// errStreamer 每次调用直接返回错误。
func errStreamer(err error) *fakeStreamer {
	return &fakeStreamer{fn: func(int, context.Context) (*schema.StreamReader[*schema.Message], error) {
		return nil, err
	}}
}

// hangStreamer 流建立后不发任何 chunk，ctx 取消时 Recv 返回 ctx.Err()（模拟 eino adapter 行为）。
func hangStreamer() *fakeStreamer {
	return &fakeStreamer{fn: func(_ int, ctx context.Context) (*schema.StreamReader[*schema.Message], error) {
		r, w := schema.Pipe[*schema.Message](1)
		go func() {
			<-ctx.Done()
			w.Send(nil, ctx.Err())
			w.Close()
		}()
		return r, nil
	}}
}

// chunkThenHangStreamer 先发 chunks 再挂住（idle 场景）。
func chunkThenHangStreamer(chunks ...*schema.Message) *fakeStreamer {
	return &fakeStreamer{fn: func(_ int, ctx context.Context) (*schema.StreamReader[*schema.Message], error) {
		r, w := schema.Pipe[*schema.Message](len(chunks) + 1)
		go func() {
			for _, c := range chunks {
				w.Send(c, nil)
			}
			<-ctx.Done()
			w.Send(nil, ctx.Err())
			w.Close()
		}()
		return r, nil
	}}
}

// shortProfile 测试用短超时参数（不影响语义断言）。
func shortProfile() Profile {
	p := DefaultProfile()
	p.TTFT = 80 * time.Millisecond
	p.Idle = 80 * time.Millisecond
	p.AcquireTimeout = 80 * time.Millisecond
	p.BackoffBase = 10 * time.Millisecond
	p.BackoffCap = 40 * time.Millisecond
	p.BreakerCooldown = 80 * time.Millisecond
	return p
}

func requireClass(t *testing.T, err error, want Class) {
	t.Helper()
	class, ok := Classify(err)
	if !ok {
		t.Fatalf("error not classified (want %s): %v", want, err)
	}
	if class != want {
		t.Fatalf("class = %s, want %s (err: %v)", class, want, err)
	}
}

func drain(t *testing.T, s *Stream) {
	t.Helper()
	for {
		_, err := s.Recv()
		if err != nil {
			return
		}
	}
}

// ---- 测试 ----

func TestBulkheadFailFast(t *testing.T) {
	p := shortProfile()
	p.Bulkhead = 1
	c := NewClient("p", p, chunkThenHangStreamer(msg("hi")))

	s1, err := c.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("first stream: %v", err)
	}
	defer s1.Close()

	_, err = c.Stream(context.Background(), nil)
	if !errors.Is(err, ErrProviderBusy) {
		t.Fatalf("second stream err = %v, want ErrProviderBusy", err)
	}
}

func TestSlotReleasedOnClose(t *testing.T) {
	p := shortProfile()
	p.Bulkhead = 1
	c := NewClient("p", p, okStreamer(msg("hi"), msg("bye")))

	s1, err := c.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("first stream: %v", err)
	}
	s1.Close()

	s2, err := c.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("second stream after close: %v", err)
	}
	drain(t, s2)
}

func TestSlotAutoReleasedOnEOF(t *testing.T) {
	p := shortProfile()
	p.Bulkhead = 1
	c := NewClient("p", p, okStreamer(msg("only")))

	s1, err := c.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("first stream: %v", err)
	}
	drain(t, s1) // 读尽（EOF 自动清理）

	s2, err := c.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("second stream after EOF: %v", err)
	}
	drain(t, s2)
}

func TestBreakerOpensAfterConsecutiveFailures(t *testing.T) {
	p := shortProfile()
	p.BreakerAfter = 3
	p.MaxRetries = 0 // 不重试，1 次调用 = 1 次最终失败
	fs := errStreamer(&Error{Class: ClassProviderDown, Err: errors.New("conn refused")})
	c := NewClient("p", p, fs)

	for i := 0; i < 3; i++ {
		_, err := c.Stream(context.Background(), nil)
		requireClass(t, err, ClassProviderDown)
	}
	// 熔断已打开：秒拒，上游不再被调用
	_, err := c.Stream(context.Background(), nil)
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("err = %v, want ErrProviderUnavailable", err)
	}
	if got := fs.calls.Load(); got != 3 {
		t.Fatalf("upstream calls = %d, want 3 (breaker should reject)", got)
	}
}

func TestRateLimitedDoesNotTripBreaker(t *testing.T) {
	p := shortProfile()
	p.BreakerAfter = 2
	p.MaxRetries = 0
	fs := errStreamer(&Error{Class: ClassRateLimited, Err: errors.New("429")})
	c := NewClient("p", p, fs)

	for i := 0; i < 5; i++ {
		_, err := c.Stream(context.Background(), nil)
		requireClass(t, err, ClassRateLimited) // 仍是 429，不是 unavailable
	}
	if got := fs.calls.Load(); got != 5 {
		t.Fatalf("upstream calls = %d, want 5 (429 不得触发熔断)", got)
	}
}

func TestBreakerConsecutiveResetsOnSuccess(t *testing.T) {
	p := shortProfile()
	p.BreakerAfter = 3
	p.MaxRetries = 0
	calls := 0
	fs := &fakeStreamer{fn: func(call int, ctx context.Context) (*schema.StreamReader[*schema.Message], error) {
		calls++
		if call%2 == 1 { // 失败、成功、失败、成功…
			return nil, &Error{Class: ClassProviderDown, Err: errors.New("down")}
		}
		r, w := schema.Pipe[*schema.Message](1)
		w.Send(msg("ok"), nil)
		w.Close()
		return r, nil
	}}
	c := NewClient("p", p, fs)

	for i := 0; i < 6; i++ {
		s, err := c.Stream(context.Background(), nil)
		if err == nil {
			drain(t, s)
		}
	}
	// 无连续 3 次失败：熔断始终未打开，6 次都到达上游
	if calls != 6 {
		t.Fatalf("upstream calls = %d, want 6", calls)
	}
}

func TestTTFTTimeout(t *testing.T) {
	p := shortProfile()
	c := NewClient("p", p, hangStreamer())

	_, err := c.Stream(context.Background(), nil)
	requireClass(t, err, ClassTimeout)
}

func TestIdleTimeoutAfterFirstChunk(t *testing.T) {
	p := shortProfile()
	c := NewClient("p", p, chunkThenHangStreamer(msg("hi")))

	s, err := c.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	m, err := s.Recv() // 首 chunk 正常
	if err != nil || m.Content != "hi" {
		t.Fatalf("first chunk = (%v, %v)", m, err)
	}
	_, err = s.Recv() // 挂住 → idle 看门狗到点
	requireClass(t, err, ClassTimeout)
}

func TestRetryBeforeFirstToken(t *testing.T) {
	p := shortProfile()
	fs := &fakeStreamer{fn: func(call int, ctx context.Context) (*schema.StreamReader[*schema.Message], error) {
		if call < 3 {
			return nil, &Error{Class: ClassNetwork, Err: errors.New("reset")}
		}
		r, w := schema.Pipe[*schema.Message](2)
		go func() {
			w.Send(msg("ok"), nil)
			w.Close()
		}()
		return r, nil
	}}
	c := NewClient("p", p, fs)

	s, err := c.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	drain(t, s)
	if got := fs.calls.Load(); got != 3 {
		t.Fatalf("upstream calls = %d, want 3 (2 retries)", got)
	}
}

func TestRetryExhaustedReturnsClassified(t *testing.T) {
	p := shortProfile()
	p.MaxRetries = 2
	fs := errStreamer(&Error{Class: ClassOverloaded, Err: errors.New("529")})
	c := NewClient("p", p, fs)

	_, err := c.Stream(context.Background(), nil)
	requireClass(t, err, ClassOverloaded)
	if got := fs.calls.Load(); got != 3 {
		t.Fatalf("upstream calls = %d, want 3 (MaxRetries=2)", got)
	}
}

func TestNoRetryAfterFirstToken(t *testing.T) {
	p := shortProfile()
	p.MaxRetries = 3
	// 首 chunk 正常，随后流错误——不得重试
	fs := &fakeStreamer{fn: func(call int, ctx context.Context) (*schema.StreamReader[*schema.Message], error) {
		r, w := schema.Pipe[*schema.Message](2)
		go func() {
			w.Send(msg("hi"), nil)
			w.Send(nil, errors.New("mid-stream failure"))
			w.Close()
		}()
		return r, nil
	}}
	c := NewClient("p", p, fs)

	s, err := c.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	drain(t, s)
	if got := fs.calls.Load(); got != 1 {
		t.Fatalf("upstream calls = %d, want 1 (首 token 后失败不重试)", got)
	}
}

func TestNonRetryableNoRetry(t *testing.T) {
	p := shortProfile()
	fs := errStreamer(&Error{Class: ClassInvalidRequest, Err: errors.New("400 context too long")})
	c := NewClient("p", p, fs)

	_, err := c.Stream(context.Background(), nil)
	requireClass(t, err, ClassInvalidRequest)
	if got := fs.calls.Load(); got != 1 {
		t.Fatalf("upstream calls = %d, want 1", got)
	}
}

func TestRetryAfterCapExceededGivesUp(t *testing.T) {
	p := shortProfile()
	p.RetryAfterCap = 10 * time.Second
	fs := errStreamer(&Error{Class: ClassRateLimited, RetryAfter: 40 * time.Second, Err: errors.New("429")})
	c := NewClient("p", p, fs)

	_, err := c.Stream(context.Background(), nil)
	requireClass(t, err, ClassRateLimited)
	if got := fs.calls.Load(); got != 1 {
		t.Fatalf("upstream calls = %d, want 1 (Retry-After 超封顶放弃)", got)
	}
}

func TestRetryAfterRespected(t *testing.T) {
	p := shortProfile()
	p.RetryAfterCap = 10 * time.Second
	fs := &fakeStreamer{fn: func(call int, ctx context.Context) (*schema.StreamReader[*schema.Message], error) {
		if call == 1 {
			return nil, &Error{Class: ClassRateLimited, RetryAfter: 30 * time.Millisecond, Err: errors.New("429")}
		}
		r, w := schema.Pipe[*schema.Message](2)
		go func() {
			w.Send(msg("ok"), nil)
			w.Close()
		}()
		return r, nil
	}}
	c := NewClient("p", p, fs)

	start := time.Now()
	s, err := c.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	drain(t, s)
	if elapsed := time.Since(start); elapsed < 30*time.Millisecond {
		t.Fatalf("elapsed = %v, want ≥ Retry-After 30ms", elapsed)
	}
	if got := fs.calls.Load(); got != 2 {
		t.Fatalf("upstream calls = %d, want 2", got)
	}
}

func TestClientDisconnectStopsRetry(t *testing.T) {
	p := shortProfile()
	ctx, cancel := context.WithCancel(context.Background())
	fs := &fakeStreamer{fn: func(call int, _ context.Context) (*schema.StreamReader[*schema.Message], error) {
		if call == 1 {
			cancel() // 首败后客户端断连
		}
		return nil, &Error{Class: ClassNetwork, Err: errors.New("reset")}
	}}
	c := NewClient("p", p, fs)

	_, err := c.Stream(ctx, nil)
	if err == nil {
		t.Fatal("want error")
	}
	if got := fs.calls.Load(); got != 1 {
		t.Fatalf("upstream calls = %d, want 1 (断连后不再重试)", got)
	}
}

func TestOverallTimeoutWrapsRetries(t *testing.T) {
	p := shortProfile()
	p.Overall = 50 * time.Millisecond
	// 429 固定 Retry-After 200ms（确定性路径，满抖动退避在 [0,cap) 无法保证必超 overall）
	p.RetryAfterCap = 10 * time.Second
	fs := errStreamer(&Error{Class: ClassRateLimited, RetryAfter: 200 * time.Millisecond, Err: errors.New("429")})
	c := NewClient("p", p, fs)

	_, err := c.Stream(context.Background(), nil)
	requireClass(t, err, ClassTimeout)
	if got := fs.calls.Load(); got != 1 {
		t.Fatalf("upstream calls = %d, want 1", got)
	}
}

func TestStreamEOF(t *testing.T) {
	p := shortProfile()
	c := NewClient("p", p, okStreamer(msg("hi"), msg("bye")))

	s, err := c.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	got := 0
	for {
		_, err := s.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		got++
	}
	if got != 2 {
		t.Fatalf("chunks = %d, want 2", got)
	}
}

// ---- Manager ----

func TestManagerCachesClients(t *testing.T) {
	m := NewManager(func(opts UpstreamOptions) (Streamer, error) {
		return okStreamer(msg("x")), nil
	})
	c1, err := m.Client("p1", UpstreamOptions{Kind: KindOpenAI})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	c2, err := m.Client("p1", UpstreamOptions{Kind: KindOpenAI})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	if c1 != c2 {
		t.Fatal("same key should return cached client（共享槽位与熔断状态）")
	}
	c3, _ := m.Client("p2", UpstreamOptions{Kind: KindOpenAI})
	if c1 == c3 {
		t.Fatal("different key should create new client")
	}
}

func TestManagerNilFactory(t *testing.T) {
	m := NewManager(nil)
	if _, err := m.Client("p1", UpstreamOptions{Kind: KindOpenAI}); err == nil {
		t.Fatal("want error when factory not injected")
	}
}

func TestManagerSetProfileAppliedToNewClients(t *testing.T) {
	m := NewManager(func(opts UpstreamOptions) (Streamer, error) {
		return okStreamer(msg("x")), nil
	})
	override := DefaultProfile()
	override.Bulkhead = 3
	m.SetProfile("p1", override)

	c, err := m.Client("p1", UpstreamOptions{Kind: KindOpenAI})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	if c.profile.Bulkhead != 3 {
		t.Fatalf("profile.Bulkhead = %d, want 3 (SetProfile 覆盖生效)", c.profile.Bulkhead)
	}
}
