package llm

import (
	"testing"
	"time"
)

// httpx 测试：传输参数与 CLAUDE.md《并发控制 / 超时》对齐；
// 流式壳无 Timeout（否则腰斩 SSE 流），JSON 壳带 Timeout。

func TestNewSharedTransport(t *testing.T) {
	tr := NewSharedTransport()

	cases := []struct {
		name string
		got  any
		want any
	}{
		{"MaxIdleConnsPerHost 对齐 bulkhead 槽数", tr.MaxIdleConnsPerHost, DefaultProfile().Bulkhead},
		{"MaxIdleConns 全局上限", tr.MaxIdleConns, transportMaxIdleConns},
		{"IdleConnTimeout", tr.IdleConnTimeout, 90 * time.Second},
		{"TLSHandshakeTimeout", tr.TLSHandshakeTimeout, 10 * time.Second},
		{"ResponseHeaderTimeout = max(TTFT)+5s", tr.ResponseHeaderTimeout, ProfileForKind(KindOllama).TTFT + transportHeaderGrace},
		{"ForceAttemptHTTP2", tr.ForceAttemptHTTP2, true},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %v, want %v", tc.name, tc.got, tc.want)
		}
	}
	if tr.DialContext == nil {
		t.Error("DialContext must be set (dial timeout 5s)")
	}
	// 不变量：ResponseHeaderTimeout 大于任何 kind 的 TTFT → 永远 ctx 先到期、错误类是我们的。
	for _, kind := range []ProviderKind{KindOpenAI, KindClaude, KindGemini, KindOllama} {
		if tr.ResponseHeaderTimeout <= ProfileForKind(kind).TTFT {
			t.Errorf("ResponseHeaderTimeout %v <= TTFT(%s) %v, ctx-first invariant broken",
				tr.ResponseHeaderTimeout, kind, ProfileForKind(kind).TTFT)
		}
	}
}

func TestNewStreamClient_NoTimeout(t *testing.T) {
	tr := NewSharedTransport()
	c := NewStreamClient(tr)
	// CLAUDE.md 禁止清单：流式 client 绝不设 Client.Timeout（会把到点的流腰斩）。
	if c.Timeout != 0 {
		t.Fatalf("stream client Timeout = %v, want 0 (ctx-driven)", c.Timeout)
	}
	if c.Transport != tr {
		t.Fatal("stream client must reuse the injected transport")
	}
}

func TestNewJSONClient_WithTimeout(t *testing.T) {
	tr := NewSharedTransport()
	c := NewJSONClient(tr, 5*time.Second)
	if c.Timeout != 5*time.Second {
		t.Fatalf("Timeout = %v, want 5s", c.Timeout)
	}
	if c.Transport != tr {
		t.Fatal("json client must reuse the injected transport")
	}
}
