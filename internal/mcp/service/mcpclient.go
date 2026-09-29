package service

// mcpclient.go：MCP 协议客户端封装（per-call 生命周期）——全仓唯一 import go-sdk
// 的地方（SDK 选型决策，docs/changelog/mcp/README.md §1）；go-sdk 类型不泄出本包
// （api 层零 SDK 依赖）。
//
// per-call 语义（server_mgmt_spec.md §4）：每次操作 新建 client → Connect（含
// initialize 握手）→ 操作 → Close。本批调用方均为低频管理动作（discover / run），
// 握手开销可忽略、无状态实现最简；会话复用（singleflight 重建）推迟到 chat 阶段。

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
	session, err := connect(ctx, sv)
	if err != nil {
		return nil, time.Since(start), err
	}
	defer func() { _ = session.Close() }()

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
	session, err := connect(ctx, sv)
	if err != nil {
		return "", err
	}
	defer func() { _ = session.Close() }()

	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	return callSession(cctx, session, toolName, args)
}

// connect 新建 client → Connect（transport 连接 + initialize 握手，connectTimeout
// 界定）。session 的 Close 由调用方 defer（per-call 生命周期）。
func connect(ctx context.Context, sv *MCPServer) (*mcp.ClientSession, error) {
	t, err := newTransport(sv)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	session, err := mcp.NewClient(clientImpl, nil).Connect(cctx, t, nil)
	if err != nil {
		return nil, fmt.Errorf("connect mcp server %q (%s): %w", sv.Name, sv.Transport, err)
	}
	return session, nil
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
