package service

// mcpclient.go：MCP 协议客户端封装（per-call 生命周期）——全仓唯一 import go-sdk
// 的地方（SDK 选型决策，docs/changelog/mcp/README.md §1）；go-sdk 类型不泄出本包
// （api 层零 SDK 依赖）。
//
// per-call 语义（server_mgmt_spec.md §4）：每次操作 新建 client → Connect（含
// initialize 握手）→ 操作 → Close。调用方含管理动作（discover / run）与 chat 热
// 路径（工具循环 Invoke）；握手开销经实测不构成优化项——http/sse 本地 ~2ms、
// stdio 见实测记录，会话复用已否决（README §5.2 设计点 2，sse_ctx_fix.md）。

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpapi "github.com/Karlsk/go-hify/internal/mcp/api"
)

// 超时边界（server_mgmt_spec.md §4）：connectTimeout 覆盖连接 + initialize 握手；
// callTimeout 覆盖单次 tools/call 与 discover 的 tools/list 翻页全程（工具执行慢
// 是常态，见 CLAUDE.md SSE 心跳说明）。
const (
	connectTimeout = 10 * time.Second
	callTimeout    = 30 * time.Second
	// maxListPages tools/list 翻页上限（防御性：server 恒返回非空 cursor 时防死循环）。
	maxListPages = 100
)

// clientImpl 握手时上报的客户端实现信息（协议 initialize 字段）。
var clientImpl = &mcp.Implementation{Name: "hify", Version: "0.1.0"}

// mcpDialer 是 MCP 协议调用的窄接口：goSDKDialer 为真身，service 单测在包内
// 构造 stub 注入（*service 同包可直接组装）。
type mcpDialer interface {
	// discover 连接 server → tools/list 按 cursor 翻页拉全 → 返回工具行
	//（MCPServerID 由 service 赋值）与全程耗时（连接起至列完）。
	discover(ctx context.Context, sv *MCPServer) (tools []MCPTool, latency time.Duration, err error)
	// call 连接 server → tools/call → TextContent 多条 "\n" 拼接（非文本块跳过）。
	call(ctx context.Context, sv *MCPServer, toolName string, args json.RawMessage) (string, error)
}

// goSDKDialer mcpDialer 的官方 go-sdk 实现。
type goSDKDialer struct{}

// discover 实现 mcpDialer.discover：连接 → 翻页列工具（callTimeout 界定全程）。
func (goSDKDialer) discover(ctx context.Context, sv *MCPServer) ([]MCPTool, time.Duration, error) {
	start := time.Now()
	session, cancel, err := connect(ctx, sv)
	if err != nil {
		return nil, time.Since(start), err
	}
	defer func() { _ = session.Close(); cancel() }()

	lctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	tools, err := listAllTools(lctx, session)
	if err != nil {
		return nil, time.Since(start), err
	}
	return tools, time.Since(start), nil
}

// call 实现 mcpDialer.call：连接 → 调工具（callTimeout 界定，含工具执行）→
// 结果归一（TextContent 拼接、isError 转错误文本）。
func (goSDKDialer) call(ctx context.Context, sv *MCPServer, toolName string, args json.RawMessage) (string, error) {
	session, cancel, err := connect(ctx, sv)
	if err != nil {
		return "", err
	}
	defer func() { _ = session.Close(); cancel() }()

	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	return callSession(cctx, session, toolName, args)
}

// connect 新建 client → Connect（transport 连接 + initialize 握手）。返回的
// cancel 是会话 ctx 的收尾函数——所有权移交调用方，操作结束时随 Close 一并 defer。
//
// 会话 ctx 以 WithoutCancel 剥离请求侧取消 / 截止再作基座：go-sdk v1.3.1 的
// SSE 传输把 GET /sse 长流绑在 Connect 传入的 ctx 上（sse.go NewRequestWithContext），
// 若沿用「带 timeout 派生 ctx + 返回即 cancel」的写法，读流会在会话交到调用方
// 手里之前被掐死——sse server 的 discover / run 全部失败（回归测试
// mcpclient_connect_test.go；修复记录 sse_ctx_fix.md）。stdio 子进程寿命绑 Close、
// streamable-http 每请求独立 POST，本不受 Connect ctx 取消影响，三传输同走此
// 路径保持行为一致。请求断连仍能及时中止在途操作：ListTools / CallTool 的操作
// ctx 派生自请求 ctx（discover / call 各自的 WithTimeout）。
//
// connectTimeout 语义由看门狗 timer 保留：只约束连接 + 握手段，Connect 返回即
// 撤销（timer.Stop），不把 deadline 挂到会话余生——CallTool 慢工具是常态，单次
// 操作的时间边界是 callTimeout。
func connect(ctx context.Context, sv *MCPServer) (*mcp.ClientSession, context.CancelFunc, error) {
	return connectWithTimeout(ctx, sv, connectTimeout)
}

// connectWithTimeout connect 的可测形态：握手超时注入，单测用短超时验证看门狗
// （真实 connectTimeout 10s 量级不宜进测试）。
func connectWithTimeout(ctx context.Context, sv *MCPServer, timeout time.Duration) (*mcp.ClientSession, context.CancelFunc, error) {
	t, err := newTransport(sv)
	if err != nil {
		return nil, nil, err
	}
	sctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	timer := time.AfterFunc(timeout, cancel) // 看门狗：握手超时掐断，Connect 返回即撤销
	session, err := mcp.NewClient(clientImpl, nil).Connect(sctx, t, nil)
	timer.Stop()
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("connect mcp server %q (%s): %w", sv.Name, sv.Transport, err)
	}
	if sctx.Err() != nil {
		// 看门狗恰在握手成功瞬间触发（timer.Stop 返回 false 的窄竞态）：SSE 读流
		// 已随 cancel 死亡，按超时处理，不把死会话交给调用方。
		_ = session.Close()
		cancel()
		return nil, nil, fmt.Errorf("connect mcp server %q (%s): 握手超时（%v）", sv.Name, sv.Transport, timeout)
	}
	return session, cancel, nil
}

// newTransport 按注册的 transport 三分支构造（枚举与 00003 CHECK 对齐）。
// stdio 的 command 按空白切分（不支持带空格的参数——一期管理面够用，需要时再上
// shell -c）；env 在继承当前进程环境后覆盖（后写者胜）。
func newTransport(sv *MCPServer) (mcp.Transport, error) {
	switch sv.Transport {
	case mcpapi.TransportStdio:
		fields := strings.Fields(sv.Command)
		if len(fields) == 0 {
			return nil, fmt.Errorf("stdio transport 的 command 为空")
		}
		cmd := exec.Command(fields[0], fields[1:]...)
		cmd.Env = append(os.Environ(), envSlice(sv.Env)...)
		return &mcp.CommandTransport{Command: cmd}, nil
	case mcpapi.TransportHTTP:
		return &mcp.StreamableClientTransport{Endpoint: sv.URL}, nil
	case mcpapi.TransportSSE:
		return &mcp.SSEClientTransport{Endpoint: sv.URL}, nil
	default:
		return nil, fmt.Errorf("未知 transport %q（须为 stdio / http / sse）", sv.Transport)
	}
}

// envSlice env map → "K=V" 切片（键排序保证确定性，便于测试与日志比对）。
func envSlice(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(env))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}

// listAllTools 在已建立会话上按 cursor 翻页拉全工具清单（spec §5.1：discover 时
// 翻完）。独立成函数供单测用 InMemoryTransport 直接驱动（不经网络 / 子进程）。
func listAllTools(ctx context.Context, session *mcp.ClientSession) ([]MCPTool, error) {
	var tools []MCPTool
	cursor := ""
	for page := 0; ; page++ {
		if page >= maxListPages {
			return nil, fmt.Errorf("tools/list 翻页超过 %d 页，中止（server cursor 异常？）", maxListPages)
		}
		res, err := session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, fmt.Errorf("list tools: %w", err)
		}
		for _, t := range res.Tools {
			tools = append(tools, MCPTool{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: nonNilSchema(t.InputSchema),
			})
		}
		if res.NextCursor == "" {
			return tools, nil
		}
		cursor = res.NextCursor
	}
}

// callSession 在已建立会话上调一个工具并归一结果（TextContent 多条 "\n" 拼接、
// 非文本块跳过、isError 转错误文本）。独立成函数供单测直接驱动。
// args 为空 / "null" 时不带参数调用（MCP 无参语义）。
func callSession(ctx context.Context, session *mcp.ClientSession, toolName string, args json.RawMessage) (string, error) {
	var arguments any
	if len(args) > 0 && string(args) != "null" {
		arguments = args // json.RawMessage 实现 Marshaler，按原始 JSON 编组进参数
	}
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: toolName, Arguments: arguments})
	if err != nil {
		return "", fmt.Errorf("call tool %q: %w", toolName, err)
	}
	// 结果归一：LLM / 管理面只吃文本；isError 时文本即工具侧错误说明（协议约定：
	// 工具错误进 Content + IsError，不是协议层 error——一并带回给上层包装哨兵）。
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			if sb.Len() > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(tc.Text)
		}
	}
	if res.IsError {
		return "", fmt.Errorf("tool %q 执行失败: %s", toolName, sb.String())
	}
	return sb.String(), nil
}

// nonNilSchema SDK 侧 InputSchema（any，正常为 map[string]any）→ map；
// nil / 非对象形态兜底空对象（防 jsonb 写 null / 破坏「顶层恒为对象」约定，
// db_model.md §4 注）。
func nonNilSchema(v any) map[string]any {
	switch s := v.(type) {
	case nil:
		return map[string]any{}
	case map[string]any:
		if s == nil {
			return map[string]any{}
		}
		return s
	default:
		// 协议外的非对象形态（理论不可达）：序列化再解析尽力保真，失败回空对象。
		b, err := json.Marshal(v)
		if err != nil {
			return map[string]any{}
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil || m == nil {
			return map[string]any{}
		}
		return m
	}
}
