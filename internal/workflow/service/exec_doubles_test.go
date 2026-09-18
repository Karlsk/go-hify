package service

// 执行引擎测试替身（chat doubles_test 同款模式）：真实 llm.NewClient 包可编程的
// fake Streamer（workflow 走 Generate 非流式）、记录式 ResolveLLMConfig / executions
// 写入缝 / rag 检索 stub。零真实 LLM / 网络（spec 06 §7）。

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/Karlsk/go-hify/internal/platform/llm"
	"github.com/Karlsk/go-hify/internal/platform/logging"
	providerapi "github.com/Karlsk/go-hify/internal/provider/api"
	ragapi "github.com/Karlsk/go-hify/internal/rag/api"
)

// stubResolve 记录式 ResolveLLMConfig stub（providerapi.ModelService 的窄面）。
type stubResolve struct {
	cfg     *providerapi.LLMConfig
	err     error
	calls   int
	lastReq providerapi.ResolveLLMConfigReq
}

func (s *stubResolve) ResolveLLMConfig(_ context.Context, req providerapi.ResolveLLMConfigReq) (*providerapi.LLMConfig, error) {
	s.calls++
	s.lastReq = req
	if s.err != nil {
		return nil, s.err
	}
	return s.cfg, nil
}

// testResolveCfg 可解析的典型配置：openai 主力 + gpt-4o。
func testResolveCfg() *providerapi.LLMConfig {
	return &providerapi.LLMConfig{ProviderName: "openai主力", Kind: "openai", ModelID: "gpt-4o", APIKey: "sk-test"}
}

// genStreamer 可编程 fake 上游：Generate 直出既定消息或错误（按调用序编排）；
// ctx 已取消时立即返回（超时 / 取消路径不必真等）。Stream 未编排（workflow 非流式）。
type genStreamer struct {
	mu    sync.Mutex
	calls int
	msgs  [][]*schema.Message
	opts  []*llm.CallOptions
	gen   func(ctx context.Context, call int) (*schema.Message, error)
}

func (f *genStreamer) Stream(context.Context, []*schema.Message, *llm.CallOptions) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("genStreamer: Stream not scripted")
}

func (f *genStreamer) Generate(ctx context.Context, msgs []*schema.Message, opts *llm.CallOptions) (*schema.Message, error) {
	f.mu.Lock()
	f.calls++
	call := f.calls
	f.msgs = append(f.msgs, msgs)
	f.opts = append(f.opts, opts)
	f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return f.gen(ctx, call)
}

func (f *genStreamer) lastMsgs() []*schema.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.msgs) == 0 {
		return nil
	}
	return f.msgs[len(f.msgs)-1]
}

func (f *genStreamer) lastOpts() *llm.CallOptions {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.opts) == 0 {
		return nil
	}
	return f.opts[len(f.opts)-1]
}

// execRecorder 记录 executions 落库行（可注入失败测非阻断路径）。
type execRecorder struct {
	mu   sync.Mutex
	rows []*logging.Execution
	err  error
}

func (e *execRecorder) Create(_ context.Context, x *logging.Execution) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err != nil {
		return e.err
	}
	e.rows = append(e.rows, x)
	return nil
}

func (e *execRecorder) last() *logging.Execution {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.rows) == 0 {
		return nil
	}
	return e.rows[len(e.rows)-1]
}

// stubRetrieve 记录式 rag 检索 stub。
type stubRetrieve struct {
	resp    []ragapi.RetrievedChunk
	err     error
	calls   int
	lastReq ragapi.RetrieveReq
}

func (s *stubRetrieve) Retrieve(_ context.Context, req ragapi.RetrieveReq) ([]ragapi.RetrievedChunk, error) {
	s.calls++
	s.lastReq = req
	if s.err != nil {
		return nil, s.err
	}
	return s.resp, nil
}

// fastProfile 测试用快 Profile：单次尝试（MaxRetries 0）、毫秒级超时与退避。
func fastProfile() llm.Profile {
	p := llm.DefaultProfile()
	p.MaxRetries = 0
	p.TTFT, p.Idle, p.Overall = 500*time.Millisecond, 500*time.Millisecond, 2*time.Second
	p.AcquireTimeout, p.BackoffBase, p.BackoffCap = 100*time.Millisecond, time.Millisecond, 5*time.Millisecond
	p.BreakerAfter, p.BreakerCooldown = 3, 100*time.Millisecond
	return p
}

// stubClientFactory 记录 key/opts 并返回预置 client。
type stubClientFactory struct {
	client *llm.Client
	err    error

	gotKey  string
	gotOpts llm.UpstreamOptions
}

func (s *stubClientFactory) Client(key string, opts llm.UpstreamOptions) (*llm.Client, error) {
	s.gotKey, s.gotOpts = key, opts
	if s.err != nil {
		return nil, s.err
	}
	return s.client, nil
}
