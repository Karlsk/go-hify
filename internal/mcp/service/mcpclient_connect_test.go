package service

// mcpclient_connect_test.go：connect 的 ctx 生命周期回归测试。
//
// 背景（sse_ctx_fix.md）：go-sdk v1.3.1 的 SSE 传输把 GET /sse 长流绑在 Connect
// 传入的 ctx 上（sse.go http.NewRequestWithContext）。若沿用「带 timeout 的派生
// ctx + Connect 返回即 cancel」的写法，会话在读到调用方手里之前读流已被掐死，
// sse server 的 discover / run 全部失败。本文件用 httptest 造最小 MCP-over-SSE
// server 复现该形态（InMemoryTransport 没有 ctx 绑定行为，复现不了——这是该 bug
// 漏过单测的原因）。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fakeSSEServer 最小 MCP-over-SSE server：GET /sse 先发 endpoint 事件后保持长流；
// POST /messages 收 JSON-RPC 请求，应答从 SSE 流回写。hang=true 时请求收下但永不
// 回写响应（复现握手挂起，验证看门狗超时）。
type fakeSSEServer struct {
	sseURL string
	// ch POST 应答 → SSE 长流的推送通道；构造期定值、之后只读写不换引用，
	// GET / POST 两个 handler 并发使用无数据竞争。
	ch   chan []byte
	hang bool
	srv  *httptest.Server
}

func newFakeSSEServer(t *testing.T, hang bool) *fakeSSEServer {
	t.Helper()
	f := &fakeSSEServer{ch: make(chan []byte, 16), hang: hang}
	mux := http.NewServeMux()
	mux.HandleFunc("/sse", f.handleSSE)
	mux.HandleFunc("/messages", f.handleMessages)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	f.sseURL = f.srv.URL + "/sse"
	return f
}

// handleSSE MCP SSE 握手：首个事件必须是 endpoint（go-sdk sse.go 硬校验），
// data 为消息端点相对路径（按 SSE 端点 URL 解析）；此后保持长流推送应答。
func (f *fakeSSEServer) handleSSE(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	fmt.Fprint(w, "event: endpoint\ndata: /messages\n\n")
	fl.Flush()
	for {
		select {
		case msg := <-f.ch:
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", msg)
			fl.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// handleMessages JSON-RPC over POST：notification（无 id）202 即完成；请求应答
// 从 SSE 流回写（POST 本身只回 202，协议约定应答走事件流）。
func (f *fakeSSEServer) handleMessages(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     *json.RawMessage `json:"id"`
		Method string           `json:"method"`
		Params json.RawMessage  `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.ID == nil || f.hang {
		w.WriteHeader(http.StatusAccepted) // notification 完成 / hang：收下但不应答
		return
	}
	var result any
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p) // 回显客户端请求的协议版本（必然兼容）
		result = rpcResult{JSONRPC: "2.0", ID: *req.ID, Result: map[string]any{
			"protocolVersion": p.ProtocolVersion,
			"capabilities":    map[string]any{},
			"serverInfo":      map[string]any{"name": "fake-sse", "version": "0.0.1"},
		}}
	case "tools/list":
		result = rpcResult{JSONRPC: "2.0", ID: *req.ID, Result: map[string]any{
			"tools": []map[string]any{{
				"name":        "echo",
				"description": "回显输入",
				"inputSchema": map[string]any{"type": "object"},
			}},
		}}
	default:
		http.Error(w, "unexpected method "+req.Method, http.StatusNotImplemented)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	b, err := json.Marshal(result)
	if err != nil {
		return
	}
	select {
	case f.ch <- b:
	case <-r.Context().Done():
	}
}

// rpcResult JSON-RPC 成功应答（错误路径本 server 不产生）。
type rpcResult struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result"`
}

// TestDiscoverSSEAfterConnectHandoff 回归：connect 返回后会话必须仍可用。
// 旧代码（Connect 用带 timeout 派生 ctx、返回即 cancel）在 sse 传输上必失败——
// 读流随 cancel 死亡，ListTools 拿不到应答；修复后会话 ctx 所有权移交调用方，
// discover 正常返回工具清单。外层 5s 上限兜住旧代码的失败时长（新代码毫秒级）。
func TestDiscoverSSEAfterConnectHandoff(t *testing.T) {
	f := newFakeSSEServer(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tools, _, err := goSDKDialer{}.discover(ctx, &MCPServer{
		Name: "fake-sse", Transport: "sse", URL: f.sseURL,
	})
	if err != nil {
		t.Fatalf("sse discover: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v，want [echo]", tools)
	}
}

// TestConnectWatchdogEnforcesHandshakeTimeout 看门狗语义：握手挂起（server 收下
// initialize 请求但永不应答）时，connect 必须在注入的超时点掐断——而不是把
// deadline 丢给会话余生或挂到 callTimeout。超时注入 50ms 验证，真实 connectTimeout
// 10s 不进测试。
func TestConnectWatchdogEnforcesHandshakeTimeout(t *testing.T) {
	f := newFakeSSEServer(t, true)

	start := time.Now()
	_, _, err := connectWithTimeout(context.Background(), &MCPServer{
		Name: "fake-sse", Transport: "sse", URL: f.sseURL,
	}, 50*time.Millisecond)
	if err == nil {
		t.Fatal("握手挂起时应超时失败，got nil error")
	}
	if el := time.Since(start); el > 2*time.Second {
		t.Fatalf("看门狗未在超时点掐断：%v", el)
	}
}
