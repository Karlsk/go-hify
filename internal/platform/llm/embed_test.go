package llm

// spec 02（backend_spec_02_embedding）单测：embedding 薄 adapter。
// 打桩形态双轨：kind 报文形态用 httptest（真实 HTTP 切面，可断言 URL/鉴权头/请求体）；
// 重试矩阵 / 预算 / 槽位用 roundTripFunc 按调用序号编排（fakeStreamer 同款先例，精确控序）。

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// roundTripFunc 函数式 RoundTripper（重试矩阵打桩：按调用编排响应/错误/阻塞）。
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// newEmbedServer 起 httptest 服务并返回指向它的 Embedder（kind 形态测试用）。
func newEmbedServer(t *testing.T, h http.HandlerFunc) (*Embedder, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewEmbedder(srv.Client().Transport), srv
}

// embedOpts 测试用三件套（openai_compatible 形态，BaseURL 由调用方填 srv.URL）。
func embedOpts(kind ProviderKind, baseURL string) EmbedOptions {
	return EmbedOptions{Kind: kind, BaseURL: baseURL, APIKey: "test-key", Model: "embed-model"}
}

// spec 02 §1：包内常量钉死（独立 4 槽 + 5s 总预算 + 2s 抢槽 + 重试 1 + 2048 上限）。
func TestEmbedConstants(t *testing.T) {
	if embedTimeout != 5*time.Second {
		t.Fatalf("embedTimeout = %v, want 5s", embedTimeout)
	}
	if embedBulkhead != 4 {
		t.Fatalf("embedBulkhead = %d, want 4", embedBulkhead)
	}
	if embedAcquireWait != 2*time.Second {
		t.Fatalf("embedAcquireWait = %v, want 2s", embedAcquireWait)
	}
	if embedMaxRetries != 1 {
		t.Fatalf("embedMaxRetries = %d, want 1", embedMaxRetries)
	}
	if embedMaxInputs != 2048 {
		t.Fatalf("embedMaxInputs = %d, want 2048", embedMaxInputs)
	}
}

// stubTransport 可比较的 RoundTripper 桩（func 类型不可 ==，构造身份断言用）。
type stubTransport struct{}

func (stubTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("stubTransport: no request expected")
}

// TestNewEmbedder 构造走查：内部复用 NewJSONClient（Timeout=embedTimeout 兜底）+ 注入 transport + 独立信号量。
func TestNewEmbedder(t *testing.T) {
	var tr stubTransport
	e := NewEmbedder(tr)
	if e.hc.Timeout != embedTimeout {
		t.Fatalf("client Timeout = %v, want %v", e.hc.Timeout, embedTimeout)
	}
	if e.hc.Transport != http.RoundTripper(tr) {
		t.Fatalf("client Transport 未复用注入的 RoundTripper")
	}
}

// spec 02 §2：claude 无 embedding API，直接哨兵拒绝——请求都不发（slot 也不抢）。
func TestEmbedStrings_ClaudeUnsupported(t *testing.T) {
	t.Parallel()
	called := false
	e, srv := newEmbedServer(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	_, err := e.EmbedStrings(context.Background(), embedOpts(KindClaude, srv.URL), []string{"x"})
	if !errors.Is(err, ErrEmbeddingUnsupported) {
		t.Fatalf("err = %v, want ErrEmbeddingUnsupported", err)
	}
	if called {
		t.Fatal("kind=claude 必须请求都不发")
	}
}

// spec 02 §2：len(inputs) > embedMaxInputs → 参数错误拒绝（分批是调用方的事），不发请求。
func TestEmbedStrings_InputLimit(t *testing.T) {
	t.Parallel()
	called := false
	e, srv := newEmbedServer(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	inputs := make([]string, embedMaxInputs+1)
	_, err := e.EmbedStrings(context.Background(), embedOpts(KindOpenAICompatible, srv.URL), inputs)
	if err == nil {
		t.Fatal("超上限 inputs 应返回错误")
	}
	if errors.Is(err, ErrProviderBusy) {
		t.Fatalf("参数错误不应是 ErrProviderBusy: %v", err)
	}
	if called {
		t.Fatal("超上限 inputs 必须请求都不发")
	}
}

// 空输入：直接返回空结果，不发请求（调用方分批不会发空批，防御性兜底）。
func TestEmbedStrings_EmptyInputs(t *testing.T) {
	t.Parallel()
	called := false
	e, srv := newEmbedServer(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	res, err := e.EmbedStrings(context.Background(), embedOpts(KindOpenAICompatible, srv.URL), nil)
	if err != nil {
		t.Fatalf("空输入不应报错: %v", err)
	}
	if len(res.Vectors) != 0 {
		t.Fatalf("Vectors len = %d, want 0", len(res.Vectors))
	}
	if called {
		t.Fatal("空输入必须请求都不发")
	}
}

// 未知 kind：分发层拒绝（照 ErrUnsupportedKind 语义），不发请求。
func TestEmbedStrings_UnknownKind(t *testing.T) {
	t.Parallel()
	called := false
	e, srv := newEmbedServer(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	_, err := e.EmbedStrings(context.Background(), embedOpts(ProviderKind("xxx"), srv.URL), []string{"x"})
	if err == nil {
		t.Fatal("未知 kind 应返回错误")
	}
	if called {
		t.Fatal("未知 kind 必须请求都不发")
	}
}

// spec 02 §2 openai_compatible：POST {base}/embeddings + Bearer；
// **data[].index 归位**（服务端乱序返回不假设顺序）+ usage.prompt_tokens 计数；
// 请求侧断言 URL / 鉴权头 / 请求体（model + input 数组）；BaseURL 带尾斜杠也应正确拼接。
func TestEmbedStrings_OpenAICompatible(t *testing.T) {
	t.Parallel()
	var gotReq struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
	}
	e, srv := newEmbedServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/embeddings" {
			t.Errorf("path = %s, want /embeddings", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want Bearer test-key", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		if err := json.Unmarshal(body, &gotReq); err != nil {
			t.Errorf("unmarshal body %s: %v", body, err)
			return
		}
		// 乱序返回：index 2 / 0 / 1，靠 data[].index 归位而非顺序。
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": [
				{"index": 2, "embedding": [0.3]},
				{"index": 0, "embedding": [0.1]},
				{"index": 1, "embedding": [0.2]}
			],
			"usage": {"prompt_tokens": 42}
		}`))
	})
	// BaseURL 带尾斜杠：拼接前应 TrimSuffix。
	res, err := e.EmbedStrings(context.Background(), embedOpts(KindOpenAICompatible, srv.URL+"/"), []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("EmbedStrings: %v", err)
	}
	if gotReq.Model != "embed-model" {
		t.Errorf("request model = %q, want embed-model", gotReq.Model)
	}
	if len(gotReq.Input) != 3 || gotReq.Input[0] != "a" || gotReq.Input[1] != "b" || gotReq.Input[2] != "c" {
		t.Errorf("request input = %v, want [a b c]", gotReq.Input)
	}
	if len(res.Vectors) != 3 {
		t.Fatalf("Vectors len = %d, want 3", len(res.Vectors))
	}
	for i, want := range []float32{0.1, 0.2, 0.3} {
		got := res.Vectors[i]
		if len(got) != 1 || got[0] != want {
			t.Errorf("Vectors[%d] = %v, want [%v]（index 归位）", i, got, want)
		}
	}
	if res.PromptTokens != 42 {
		t.Errorf("PromptTokens = %d, want 42", res.PromptTokens)
	}
}

// Dimensions 输出维度参数（Matryoshka 截断，如 Qwen3-Embedding 2560→1536）：
// >0 时 openai_compatible 请求体带 dimensions；=0（零值）不带——老网关对未知字段可能
// 4xx，只在调用方显式要求时才发（rag 侧填 RequiredEmbeddingDim，platform 不 import 业务常量）。
func TestEmbedStrings_OpenAIDimensions(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		dimensions int
		wantSent   bool
	}{
		{"正数带 dimensions", 1536, true},
		{"零值不带 dimensions（向后兼容）", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var raw map[string]json.RawMessage
			e, srv := newEmbedServer(t, func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read body: %v", err)
					return
				}
				if err := json.Unmarshal(body, &raw); err != nil {
					t.Errorf("unmarshal body %s: %v", body, err)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":[{"index":0,"embedding":[0.1]}]}`))
			})
			opts := embedOpts(KindOpenAICompatible, srv.URL)
			opts.Dimensions = tc.dimensions
			if _, err := e.EmbedStrings(context.Background(), opts, []string{"x"}); err != nil {
				t.Fatalf("EmbedStrings: %v", err)
			}
			_, sent := raw["dimensions"]
			if sent != tc.wantSent {
				t.Fatalf("dimensions 字段存在 = %v, want %v（原始请求体字段集: %v）", sent, tc.wantSent, raw)
			}
			if tc.wantSent {
				var got int
				if err := json.Unmarshal(raw["dimensions"], &got); err != nil || got != tc.dimensions {
					t.Fatalf("dimensions = %s, want %d", raw["dimensions"], tc.dimensions)
				}
			}
		})
	}
}

// openai_compatible 响应防御：index 越界 / 缺槽（返回条目少于输入）→ 错误（不信任外部数据）。
func TestEmbedStrings_OpenAIBadIndex(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"index 越界", `{"data": [{"index": 3, "embedding": [0.1]}], "usage": {"prompt_tokens": 1}}`},
		{"缺槽", `{"data": [{"index": 0, "embedding": [0.1]}], "usage": {"prompt_tokens": 1}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, srv := newEmbedServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			})
			_, err := e.EmbedStrings(context.Background(), embedOpts(KindOpenAICompatible, srv.URL), []string{"a", "b"})
			if err == nil {
				t.Fatal("畸形响应应返回错误")
			}
		})
	}
}

// spec 02 §2 ollama：POST {base}/api/embed 无鉴权，请求体 {model, input}，
// 响应 {"embeddings"} 顺序数组；无 usage 字段 → PromptTokens=0。
func TestEmbedStrings_Ollama(t *testing.T) {
	t.Parallel()
	var gotReq struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
	}
	e, srv := newEmbedServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Errorf("path = %s, want /api/embed", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("ollama 无鉴权头，got Authorization %q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		if err := json.Unmarshal(body, &gotReq); err != nil {
			t.Errorf("unmarshal body %s: %v", body, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model": "embed-model", "embeddings": [[0.1, -0.1], [0.2, -0.2]]}`))
	})
	res, err := e.EmbedStrings(context.Background(), embedOpts(KindOllama, srv.URL), []string{"a", "b"})
	if err != nil {
		t.Fatalf("EmbedStrings: %v", err)
	}
	if gotReq.Model != "embed-model" || len(gotReq.Input) != 2 {
		t.Errorf("request = model %q input %v, want embed-model [a b]", gotReq.Model, gotReq.Input)
	}
	if len(res.Vectors) != 2 || res.Vectors[0][0] != 0.1 || res.Vectors[1][0] != 0.2 {
		t.Errorf("Vectors = %v, want 顺序数组 [[0.1 -0.1] [0.2 -0.2]]", res.Vectors)
	}
	if res.PromptTokens != 0 {
		t.Errorf("PromptTokens = %d, want 0（ollama 无 usage）", res.PromptTokens)
	}
}

// spec 02 §2 gemini：POST {base}/models/{model}:batchEmbedContents + x-goog-api-key，
// 请求体 requests[].model("models/{m}") + content.parts[].text，响应 embeddings[].values 顺序数组。
func TestEmbedStrings_Gemini(t *testing.T) {
	t.Parallel()
	var gotReq struct {
		Requests []struct {
			Model   string `json:"model"`
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"requests"`
	}
	e, srv := newEmbedServer(t, func(w http.ResponseWriter, r *http.Request) {
		if want := "/models/embed-model:batchEmbedContents"; r.URL.Path != want {
			t.Errorf("path = %s, want %s", r.URL.Path, want)
		}
		if got := r.Header.Get("x-goog-api-key"); got != "test-key" {
			t.Errorf("x-goog-api-key = %q, want test-key", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("gemini 不用 Bearer，got Authorization %q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		if err := json.Unmarshal(body, &gotReq); err != nil {
			t.Errorf("unmarshal body %s: %v", body, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"embeddings": [{"values": [0.5]}, {"values": [0.6]}]}`))
	})
	res, err := e.EmbedStrings(context.Background(), embedOpts(KindGemini, srv.URL), []string{"你好", "world"})
	if err != nil {
		t.Fatalf("EmbedStrings: %v", err)
	}
	if len(gotReq.Requests) != 2 {
		t.Fatalf("requests len = %d, want 2", len(gotReq.Requests))
	}
	if gotReq.Requests[0].Model != "models/embed-model" {
		t.Errorf("requests[0].model = %q, want models/embed-model", gotReq.Requests[0].Model)
	}
	if gotReq.Requests[0].Content.Parts[0].Text != "你好" || gotReq.Requests[1].Content.Parts[0].Text != "world" {
		t.Errorf("requests text = %v", gotReq.Requests)
	}
	if len(res.Vectors) != 2 || res.Vectors[0][0] != 0.5 || res.Vectors[1][0] != 0.6 {
		t.Errorf("Vectors = %v, want [[0.5] [0.6]]", res.Vectors)
	}
	if res.PromptTokens != 0 {
		t.Errorf("PromptTokens = %d, want 0（gemini 无 usage）", res.PromptTokens)
	}
}

// ollama / gemini 响应防御：embeddings 条目数 ≠ 输入数 → 错误（不信任外部数据）。
func TestEmbedStrings_CountMismatch(t *testing.T) {
	cases := []struct {
		name string
		kind ProviderKind
		body string
	}{
		{"ollama 数量不匹配", KindOllama, `{"embeddings": [[0.1]]}`},
		{"gemini 数量不匹配", KindGemini, `{"embeddings": [{"values": [0.1]}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, srv := newEmbedServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			})
			_, err := e.EmbedStrings(context.Background(), embedOpts(tc.kind, srv.URL), []string{"a", "b"})
			if err == nil {
				t.Fatal("数量不匹配应返回错误")
			}
		})
	}
}

// scriptResp 构造桩响应（重试矩阵打桩；retryAfter 空串表示不带该头）。
func scriptResp(status int, retryAfter, body string) *http.Response {
	h := http.Header{"Content-Type": []string{"application/json"}}
	if retryAfter != "" {
		h.Set("Retry-After", retryAfter)
	}
	return &http.Response{
		StatusCode: status,
		Header:     h,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// openaiOKBody 成功桩响应体（openai 形态，1 条输入）。
const openaiOKBody = `{"data": [{"index": 0, "embedding": [0.1]}], "usage": {"prompt_tokens": 7}}`

// newScriptedEmbedder 按调用序号编排响应的 Embedder（元素 *http.Response 或 error）。
func newScriptedEmbedder(t *testing.T, scripted ...any) (*Embedder, func() int) {
	t.Helper()
	calls := 0
	e := NewEmbedder(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls > len(scripted) {
			t.Errorf("unexpected HTTP call #%d (scripted %d)", calls, len(scripted))
			return nil, errors.New("no more scripted responses")
		}
		switch v := scripted[calls-1].(type) {
		case *http.Response:
			v.Request = r
			return v, nil
		case error:
			return nil, v
		default:
			t.Fatalf("bad scripted type %T", v)
			return nil, nil
		}
	}))
	return e, func() int { return calls }
}

// scriptedOpts 打桩用 opts（BaseURL 不会被真实拨号，RoundTripper 已截获）。
func scriptedOpts() EmbedOptions { return embedOpts(KindOpenAICompatible, "http://stub") }

// spec 02 §3：429 + Retry-After → 重试一次成功，退避尊重 Retry-After（min(1s, 2s)=1s）。
// 重试计时用例各自独享 Embedder（独立信号量），t.Parallel 压缩 spec 冻结常量的串行睡眠链。
func TestEmbedStrings_RetryOn429WithRetryAfter(t *testing.T) {
	t.Parallel()
	e, calls := newScriptedEmbedder(t,
		scriptResp(http.StatusTooManyRequests, "1", `{"error": "rate limited"}`),
		scriptResp(http.StatusOK, "", openaiOKBody),
	)
	start := time.Now()
	res, err := e.EmbedStrings(context.Background(), scriptedOpts(), []string{"a"})
	if err != nil {
		t.Fatalf("EmbedStrings: %v", err)
	}
	if calls() != 2 {
		t.Fatalf("calls = %d, want 2（重试一次）", calls())
	}
	if d := time.Since(start); d < time.Second {
		t.Fatalf("应按 Retry-After=1s 退避，elapsed %v < 1s", d)
	}
	if len(res.Vectors) != 1 || res.PromptTokens != 7 {
		t.Fatalf("res = %+v, want 1 vector + 7 tokens", res)
	}
}

// spec 02 §3：429 无头 → 300ms 固定退避后重试成功。
func TestEmbedStrings_RetryOn429NoHeader(t *testing.T) {
	t.Parallel()
	e, calls := newScriptedEmbedder(t,
		scriptResp(http.StatusTooManyRequests, "", `{"error": "rate limited"}`),
		scriptResp(http.StatusOK, "", openaiOKBody),
	)
	start := time.Now()
	if _, err := e.EmbedStrings(context.Background(), scriptedOpts(), []string{"a"}); err != nil {
		t.Fatalf("EmbedStrings: %v", err)
	}
	if calls() != 2 {
		t.Fatalf("calls = %d, want 2", calls())
	}
	if d := time.Since(start); d < 300*time.Millisecond {
		t.Fatalf("应固定退避 300ms，elapsed %v", d)
	}
}

// spec 02 §3：429 无头（Retry-After 解析失败同路径）→ 300ms 固定退避。
func TestEmbedStrings_RetryOn429BadHeader(t *testing.T) {
	t.Parallel()
	e, calls := newScriptedEmbedder(t,
		scriptResp(http.StatusTooManyRequests, "not-a-number", `{"error": "rate limited"}`),
		scriptResp(http.StatusOK, "", openaiOKBody),
	)
	if _, err := e.EmbedStrings(context.Background(), scriptedOpts(), []string{"a"}); err != nil {
		t.Fatalf("EmbedStrings: %v", err)
	}
	if calls() != 2 {
		t.Fatalf("calls = %d, want 2（解析失败按无头处理）", calls())
	}
}

// spec 02 §3：Retry-After > 2s → 直接放弃返回 RateLimited（带 RetryAfter），不 sleep。
func TestEmbedStrings_RetryAfterOverCapAbandons(t *testing.T) {
	e, calls := newScriptedEmbedder(t,
		scriptResp(http.StatusTooManyRequests, "3", `{"error": "rate limited"}`),
	)
	start := time.Now()
	_, err := e.EmbedStrings(context.Background(), scriptedOpts(), []string{"a"})
	if err == nil {
		t.Fatal("Retry-After 超上限应放弃")
	}
	if calls() != 1 {
		t.Fatalf("calls = %d, want 1（不重试）", calls())
	}
	if d := time.Since(start); d >= time.Second {
		t.Fatalf("不应等待长冷却，elapsed %v", d)
	}
	class, ok := Classify(err)
	if !ok || class != ClassRateLimited {
		t.Fatalf("class = %s %v, want RateLimited", class, ok)
	}
	var le *Error
	if !errors.As(err, &le) || le.RetryAfter != 3*time.Second {
		t.Fatalf("RetryAfter 应透传 3s，got %+v", le)
	}
}

// spec 02 §3 重试矩阵（表驱动）：成功路径（5xx / 529 / 网络错误 → 重试一次成功）。
func TestEmbedStrings_RetrySuccessPaths(t *testing.T) {
	cases := []struct {
		name     string
		scripted []any
	}{
		{"500 重试成功", []any{scriptResp(500, "", `internal error`), scriptResp(200, "", openaiOKBody)}},
		{"529 overloaded 重试成功", []any{scriptResp(529, "", `overloaded`), scriptResp(200, "", openaiOKBody)}},
		{"网络错误重试成功", []any{errors.New("dial tcp: connection refused"), scriptResp(200, "", openaiOKBody)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e, calls := newScriptedEmbedder(t, tc.scripted...)
			res, err := e.EmbedStrings(context.Background(), scriptedOpts(), []string{"a"})
			if err != nil {
				t.Fatalf("EmbedStrings: %v", err)
			}
			if calls() != 2 {
				t.Fatalf("calls = %d, want 2", calls())
			}
			if len(res.Vectors) != 1 || res.PromptTokens != 7 {
				t.Fatalf("res = %+v", res)
			}
		})
	}
}

// spec 02 §3 重试矩阵（表驱动）：失败路径——400/401/404 不重试；429/5xx/网络重试耗尽的最终分类。
func TestEmbedStrings_RetryFailureMatrix(t *testing.T) {
	cases := []struct {
		name           string
		scripted       []any
		wantCalls      int
		wantClass      Class
		wantRetryAfter time.Duration
	}{
		{"400 不重试", []any{scriptResp(400, "", `{"error": {"message": "input too long"}}`)}, 1, ClassInvalidRequest, 0},
		{"401 不重试", []any{scriptResp(401, "", `{"error": "bad key"}`)}, 1, ClassAuth, 0},
		{"403 不重试", []any{scriptResp(403, "", `{"error": "forbidden"}`)}, 1, ClassAuth, 0},
		{"404 不重试", []any{scriptResp(404, "", `{"error": "no such model"}`)}, 1, ClassInvalidRequest, 0},
		{"429 重试耗尽", []any{
			scriptResp(429, "", `{"error": "rate limited"}`),
			scriptResp(429, "2", `{"error": "rate limited"}`),
		}, 2, ClassRateLimited, 2 * time.Second},
		{"500 重试耗尽", []any{scriptResp(500, "", `err`), scriptResp(500, "", `err`)}, 2, ClassOverloaded, 0},
		{"网络错误重试耗尽", []any{
			errors.New("dial tcp: connection refused"),
			errors.New("connection reset"),
		}, 2, ClassNetwork, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e, calls := newScriptedEmbedder(t, tc.scripted...)
			_, err := e.EmbedStrings(context.Background(), scriptedOpts(), []string{"a"})
			if err == nil {
				t.Fatal("应返回错误")
			}
			if calls() != tc.wantCalls {
				t.Fatalf("calls = %d, want %d", calls(), tc.wantCalls)
			}
			class, ok := Classify(err)
			if !ok || class != tc.wantClass {
				t.Fatalf("class = %s (ok=%v), want %s", class, ok, tc.wantClass)
			}
			var le *Error
			if errors.As(err, &le) && le.RetryAfter != tc.wantRetryAfter {
				t.Fatalf("RetryAfter = %v, want %v", le.RetryAfter, tc.wantRetryAfter)
			}
		})
	}
}

// spec 02 §3：5s 总预算含 sleep——sleep 中 ctx 到期立即放弃剩余尝试（返回 Timeout）。
func TestEmbedStrings_BudgetExhaustedDuringSleep(t *testing.T) {
	t.Parallel()
	e, calls := newScriptedEmbedder(t,
		scriptResp(http.StatusTooManyRequests, "1", `{"error": "rate limited"}`), // 退避 1s
		scriptResp(http.StatusOK, "", openaiOKBody),                              // 不应到达
	)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := e.EmbedStrings(ctx, scriptedOpts(), []string{"a"})
	if err == nil {
		t.Fatal("预算耗尽应返回错误")
	}
	if calls() != 1 {
		t.Fatalf("calls = %d, want 1（sleep 中放弃，不再尝试）", calls())
	}
	if d := time.Since(start); d >= time.Second {
		t.Fatalf("ctx 到期应立即返回，elapsed %v", d)
	}
	if class, ok := Classify(err); !ok || class != ClassTimeout {
		t.Fatalf("class = %s (ok=%v), want Timeout", class, ok)
	}
}

// classifyBudgetErr 直测：DeadlineExceeded → ClassTimeout；调用方取消原样上抛（未分类，
// 让调用方感知取消——classifyCtxErr 同款语义）。CancelledCtx 端到端用例在抢槽处提前
// 返回 Busy，覆盖不到此处，单独钉死。
func TestClassifyBudgetErr(t *testing.T) {
	derr := classifyBudgetErr(context.DeadlineExceeded)
	class, ok := Classify(derr)
	if !ok || class != ClassTimeout {
		t.Fatalf("DeadlineExceeded class = %s (ok=%v), want Timeout", class, ok)
	}

	cerr := classifyBudgetErr(context.Canceled)
	if _, ok := Classify(cerr); ok {
		t.Fatalf("Canceled 不应带分类（原样上抛），got classifiable: %v", cerr)
	}
	if !errors.Is(cerr, context.Canceled) {
		t.Fatalf("Canceled 应保留错误链: %v", cerr)
	}
}

// 调用方已取消的 ctx：立即返回错误，请求都不发。
func TestEmbedStrings_CancelledCtx(t *testing.T) {
	e, calls := newScriptedEmbedder(t, scriptResp(200, "", openaiOKBody))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := e.EmbedStrings(ctx, scriptedOpts(), []string{"a"})
	if err == nil {
		t.Fatal("已取消 ctx 应返回错误")
	}
	if calls() != 0 {
		t.Fatalf("calls = %d, want 0", calls())
	}
}

// spec 02 §1/§3：并发打满 4 槽 → 第 5 路 ErrProviderBusy（fail-fast，不排队不重试）；
// 占槽调用结束后 defer 释放槽位，新调用恢复成功。第 5 路用短父 ctx 控制测试时长
// （比 embedAcquireWait 先到期，语义等价：抢不到槽就是抢不到）。
func TestEmbedStrings_BulkheadBusy(t *testing.T) {
	t.Parallel()
	var inflight atomic.Int32
	release := make(chan struct{})
	e := NewEmbedder(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		inflight.Add(1)
		select {
		case <-release:
			return scriptResp(http.StatusOK, "", openaiOKBody), nil
		case <-r.Context().Done():
			// ctx 感知（真实 transport 语义）：RED 阶段第 5 路若误入传输层，靠 ctx 脱困而非死锁。
			return nil, r.Context().Err()
		}
	}))

	var wg sync.WaitGroup
	for i := 0; i < embedBulkhead; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = e.EmbedStrings(context.Background(), scriptedOpts(), []string{"a"})
		}()
	}
	// 等 4 路全部在途（RoundTrip 阻塞中 = 4 槽已占满）。
	deadline := time.Now().Add(2 * time.Second)
	for inflight.Load() < int32(embedBulkhead) && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if inflight.Load() < int32(embedBulkhead) {
		t.Fatal("4 路未全部在途，测试前置失败")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := e.EmbedStrings(ctx, scriptedOpts(), []string{"a"})
	if !errors.Is(err, ErrProviderBusy) {
		t.Fatalf("err = %v, want ErrProviderBusy", err)
	}
	if d := time.Since(start); d >= embedAcquireWait {
		t.Fatalf("短父 ctx 应先于抢槽超时返回，elapsed %v", d)
	}

	// 释放槽位后恢复。
	close(release)
	wg.Wait()
	if _, err := e.EmbedStrings(context.Background(), scriptedOpts(), []string{"a"}); err != nil {
		t.Fatalf("槽位释放后应恢复: %v", err)
	}
}
