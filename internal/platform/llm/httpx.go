package llm

import (
	"net"
	"net/http"
	"time"
)

// HTTP 出站参数约定（CLAUDE.md《并发控制》《超时》）：
// 连接池参数对齐 bulkhead 槽位（复用 keep-alive 省 TLS 握手）；
// ResponseHeaderTimeout 恒大于任何供应商 TTFT，保证「永远 ctx 先到期、
// 错误类是 context.Cause 的哨兵，而不是 net/http 的模糊错误」。
const (
	transportMaxIdleConns        = 128              // 全局空闲连接上限（4 供应商 × 槽位 + 余量）
	transportIdleConnTimeout     = 90 * time.Second // 空闲 keep-alive 超时
	transportDialTimeout         = 5 * time.Second  // TCP 拨号超时
	transportKeepAlive           = 30 * time.Second // keep-alive 探测间隔（对齐 http.DefaultTransport）
	transportTLSHandshakeTimeout = 10 * time.Second // TLS 握手超时
	transportHeaderGrace         = 5 * time.Second  // ResponseHeaderTimeout = max(TTFT) + 该余量
)

// NewSharedTransport 创建全仓 LLM 出站流量共享的 Transport。
// 连接池按 host:port 键控，一个 Transport 天然隔离各供应商；
// MaxIdleConnsPerHost 对齐默认 bulkhead 槽数（CLAUDE.md 定制 transport 要求）。
// ResponseHeaderTimeout 取各 kind 默认 TTFT 的最大者（Ollama 冷启动 120s）：
// 对更快的供应商 ctx 的 TTFT 会更早到期，「ctx 先到期」不变量不破。
// 注意：DB 把某 provider 的 TTFT 覆盖到超过该值时，此兜底失效（ctx 仍生效，仅 transport 层不再兜底）。
func NewSharedTransport() *http.Transport {
	maxTTFT := ProfileForKind(KindOllama).TTFT // 各 kind 默认 TTFT 的最大者（Ollama 冷加载模型）
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   transportDialTimeout,
			KeepAlive: transportKeepAlive,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          transportMaxIdleConns,
		MaxIdleConnsPerHost:   DefaultProfile().Bulkhead,
		IdleConnTimeout:       transportIdleConnTimeout,
		TLSHandshakeTimeout:   transportTLSHandshakeTimeout,
		ResponseHeaderTimeout: maxTTFT + transportHeaderGrace,
	}
}

// NewStreamClient 流式调用壳：绝不设 Timeout（CLAUDE.md 禁止清单——
// Client.Timeout 覆盖整个 body 读取，会把到点的 SSE 流腰斩）；
// 超时 / 取消全由 ctx 三层（TTFT / idle / overall）与客户端断连驱动。
func NewStreamClient(t http.RoundTripper) *http.Client {
	return &http.Client{Transport: t}
}

// NewJSONClient 非流式调用壳（连通性探测、embedding 等）：带 Timeout 兜底。
func NewJSONClient(t http.RoundTripper, timeout time.Duration) *http.Client {
	return &http.Client{Transport: t, Timeout: timeout}
}
