package llm

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
)

// Generate 路径测试：与 Stream 同链路（抢槽 → overall → 熔断 → 重试），差异点是无
// TTFT / idle 看门狗、槽位随函数返回释放。

// genStreamer 构造 Generate 路径 fake（Stream 被意外调用时报错）。
func genStreamer(fn func(call int, ctx context.Context) (*schema.Message, error)) *fakeStreamer {
	return &fakeStreamer{
		fn: func(int, context.Context) (*schema.StreamReader[*schema.Message], error) {
			return nil, errors.New("unexpected Stream call in generate test")
		},
		genFn: fn,
	}
}

func errGenStreamer(err error) *fakeStreamer {
	return genStreamer(func(int, context.Context) (*schema.Message, error) { return nil, err })
}

// hangGenStreamer 阻塞到 ctx 取消（overall 超时 / 抢槽测试用）。
func hangGenStreamer() *fakeStreamer {
	return genStreamer(func(_ int, ctx context.Context) (*schema.Message, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
}

func TestGenerateSuccessReleasesSlot(t *testing.T) {
	p := shortProfile()
	p.Bulkhead = 1
	c := NewClient("p", p, genStreamer(func(int, context.Context) (*schema.Message, error) {
		return msg("answer"), nil
	}))

	for i := 0; i < 2; i++ { // 第二次能立即成功 = 槽位已随返回释放
		m, err := c.Generate(context.Background(), nil, nil)
		if err != nil {
			t.Fatalf("generate #%d: %v", i+1, err)
		}
		if m.Content != "answer" {
			t.Fatalf("content = %q, want answer", m.Content)
		}
	}
}

func TestGenerateRetriesThenSucceeds(t *testing.T) {
	p := shortProfile()
	fs := genStreamer(func(call int, _ context.Context) (*schema.Message, error) {
		if call < 3 {
			return nil, &Error{Class: ClassNetwork, Err: errors.New("reset")}
		}
		return msg("ok"), nil
	})
	c := NewClient("p", p, fs)

	m, err := c.Generate(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if m.Content != "ok" || fs.calls.Load() != 3 {
		t.Fatalf("content=%q calls=%d, want ok/3（2 次重试）", m.Content, fs.calls.Load())
	}
}

func TestGenerateRetryExhausted(t *testing.T) {
	p := shortProfile()
	p.MaxRetries = 2
	fs := errGenStreamer(&Error{Class: ClassOverloaded, Err: errors.New("529")})
	c := NewClient("p", p, fs)

	_, err := c.Generate(context.Background(), nil, nil)
	requireClass(t, err, ClassOverloaded)
	if got := fs.calls.Load(); got != 3 {
		t.Fatalf("upstream calls = %d, want 3 (MaxRetries=2)", got)
	}
}

func TestGenerateNonRetryable(t *testing.T) {
	p := shortProfile()
	fs := errGenStreamer(&Error{Class: ClassInvalidRequest, Err: errors.New("400")})
	c := NewClient("p", p, fs)

	_, err := c.Generate(context.Background(), nil, nil)
	requireClass(t, err, ClassInvalidRequest)
	if got := fs.calls.Load(); got != 1 {
		t.Fatalf("upstream calls = %d, want 1", got)
	}
}

func TestGenerateBreakerTrips(t *testing.T) {
	p := shortProfile()
	p.BreakerAfter = 2
	p.MaxRetries = 0
	fs := errGenStreamer(&Error{Class: ClassProviderDown, Err: errors.New("refused")})
	c := NewClient("p", p, fs)

	for i := 0; i < 2; i++ {
		_, err := c.Generate(context.Background(), nil, nil)
		requireClass(t, err, ClassProviderDown)
	}
	if _, err := c.Generate(context.Background(), nil, nil); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("err = %v, want ErrProviderUnavailable（熔断打开秒拒）", err)
	}
	if got := fs.calls.Load(); got != 2 {
		t.Fatalf("upstream calls = %d, want 2", got)
	}
}

func TestGenerate429DoesNotTripBreaker(t *testing.T) {
	p := shortProfile()
	p.BreakerAfter = 2
	p.MaxRetries = 0
	fs := errGenStreamer(&Error{Class: ClassRateLimited, Err: errors.New("429")})
	c := NewClient("p", p, fs)

	for i := 0; i < 4; i++ {
		_, err := c.Generate(context.Background(), nil, nil)
		requireClass(t, err, ClassRateLimited)
	}
	if got := fs.calls.Load(); got != 4 {
		t.Fatalf("upstream calls = %d, want 4 (429 不得触发熔断)", got)
	}
}

func TestGenerateOverallTimeout(t *testing.T) {
	p := shortProfile()
	p.Overall = 50 * time.Millisecond
	c := NewClient("p", p, hangGenStreamer())

	_, err := c.Generate(context.Background(), nil, nil)
	requireClass(t, err, ClassTimeout)
}

func TestGenerateBulkheadFailFast(t *testing.T) {
	p := shortProfile()
	p.Bulkhead = 1
	p.Overall = 500 * time.Millisecond // 挂起 goroutine 的退出上界（默认 5min 会拖死测试）
	fs := hangGenStreamer()
	c := NewClient("p", p, fs)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // 占住唯一的槽，直到 overall 超时
		defer wg.Done()
		_, _ = c.Generate(context.Background(), nil, nil)
	}()
	waitFor(t, func() bool { return fs.calls.Load() >= 1 })

	if _, err := c.Generate(context.Background(), nil, nil); !errors.Is(err, ErrProviderBusy) {
		t.Fatalf("second generate err = %v, want ErrProviderBusy", err)
	}
	wg.Wait()
}

func TestGenerateOptionsPassthrough(t *testing.T) {
	p := shortProfile()
	fs := genStreamer(func(int, context.Context) (*schema.Message, error) { return msg("ok"), nil })
	c := NewClient("p", p, fs)
	temp := float32(0.3)
	opts := &CallOptions{Temperature: &temp, MaxTokens: 512}

	if _, err := c.Generate(context.Background(), nil, opts); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if fs.lastGenOpts != opts {
		t.Fatalf("opts 透传失败: %v", fs.lastGenOpts)
	}
	if fs.lastOpts != nil {
		t.Fatalf("Stream 不应被调用")
	}
}

// waitFor 轮询等待条件成立（genFn 进入阻塞需要一瞬间）。
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not met within 2s")
}
