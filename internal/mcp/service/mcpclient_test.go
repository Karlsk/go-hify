package service

// mcpclient 测试：listAllTools / callSession 经 go-sdk InMemoryTransport 直驱
//（内存 MCP server + 已握手的 client session，无网络 / 子进程——SDK 官方测试同款
// 手法）；newTransport / envSlice / nonNilSchema 为纯函数直测。goSDKDialer 的
// discover / call 全链路（真实连接 + 握手）留 Step 8 手测文档按三种 transport 冒烟。

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcpapi "github.com/Karlsk/go-hify/internal/mcp/api"
)

// memSession 起一个内存 MCP server（register 注册工具）并返回完成 initialize 握手
// 的 client session。SDK 语义：server 必须先 Connect（非阻塞，后台处理消息），
// client Connect 含握手。
func memSession(t *testing.T, register func(*mcp.Server)) *mcp.ClientSession {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "0.0.1"}, nil)
	register(server)
	serverT, clientT := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	_, err := server.Connect(ctx, serverT, nil)
	require.NoError(t, err)
	session, err := mcp.NewClient(&mcp.Implementation{Name: "hify-test", Version: "0.0.0"}, nil).
		Connect(ctx, clientT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// objectSchema AddTool 要求 InputSchema 非空且 type=object 的最小 schema。
func objectSchema() map[string]any { return map[string]any{"type": "object"} }

// ---- callSession ----

func TestCallSession_TextSingle(t *testing.T) {
	session := memSession(t, func(s *mcp.Server) {
		s.AddTool(&mcp.Tool{Name: "echo", Description: "回显", InputSchema: objectSchema()},
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "hello"}}}, nil
			})
	})

	out, err := callSession(context.Background(), session, "echo", json.RawMessage(`{}`))
	require.NoError(t, err)
	assert.Equal(t, "hello", out)
}

func TestCallSession_TextJoin(t *testing.T) {
	// 多条 TextContent "\n" 拼接（server_mgmt_spec.md §4 归一规则）。
	session := memSession(t, func(s *mcp.Server) {
		s.AddTool(&mcp.Tool{Name: "two", InputSchema: objectSchema()},
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{Content: []mcp.Content{
					&mcp.TextContent{Text: "第一行"},
					&mcp.TextContent{Text: "第二行"},
				}}, nil
			})
	})

	out, err := callSession(context.Background(), session, "two", nil)
	require.NoError(t, err)
	assert.Equal(t, "第一行\n第二行", out)
}

func TestCallSession_NonTextSkipped(t *testing.T) {
	// 非文本块（图片等）跳过：文本块之间夹一个 ImageContent 不影响拼接。
	session := memSession(t, func(s *mcp.Server) {
		s.AddTool(&mcp.Tool{Name: "mixed", InputSchema: objectSchema()},
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{Content: []mcp.Content{
					&mcp.TextContent{Text: "before"},
					&mcp.ImageContent{Data: []byte("aGk="), MIMEType: "image/png"},
					&mcp.TextContent{Text: "after"},
				}}, nil
			})
	})

	out, err := callSession(context.Background(), session, "mixed", nil)
	require.NoError(t, err)
	assert.Equal(t, "before\nafter", out, "图片块跳过，两条文本按序拼接")
}

func TestCallSession_IsErrorToError(t *testing.T) {
	// 协议约定：工具自身错误进 Content + IsError（非协议层 error）——转 Go 错误
	// 文本，上层包装 ErrMCPToolCallFailed。
	session := memSession(t, func(s *mcp.Server) {
		s.AddTool(&mcp.Tool{Name: "fail", InputSchema: objectSchema()},
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{
					IsError: true,
					Content: []mcp.Content{&mcp.TextContent{Text: "文件不存在: /etc/shadow"}},
				}, nil
			})
	})

	out, err := callSession(context.Background(), session, "fail", nil)
	require.Error(t, err)
	assert.Empty(t, out)
	assert.Contains(t, err.Error(), "fail")
	assert.Contains(t, err.Error(), "文件不存在")
}

func TestCallSession_UnknownTool(t *testing.T) {
	session := memSession(t, func(s *mcp.Server) {})

	_, err := callSession(context.Background(), session, "nope", nil)
	require.Error(t, err, "协议层 error（工具不存在）")
}

func TestCallSession_NoArgsSemantics(t *testing.T) {
	// args 缺省 / "null" 都不带参数调用（MCP 无参语义，server_mgmt_spec.md §4）。
	var gotArgs []json.RawMessage
	session := memSession(t, func(s *mcp.Server) {
		s.AddTool(&mcp.Tool{Name: "noargs", InputSchema: objectSchema()},
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				gotArgs = append(gotArgs, req.Params.Arguments)
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
			})
	})

	for _, args := range []json.RawMessage{nil, json.RawMessage("null")} {
		out, err := callSession(context.Background(), session, "noargs", args)
		require.NoError(t, err)
		assert.Equal(t, "ok", out)
	}
	for _, a := range gotArgs {
		// SDK 客户端把 nil arguments 在 wire 上归一成空对象（实测 v1.3.1 行为）——
		// 语义等价「无参」，所有 MCP server 均按空对象处理。
		assert.True(t, len(a) == 0 || string(a) == "null" || string(a) == "{}",
			"无参调用不带实质 arguments，得到 %q", a)
	}

	// 带参调用：RawMessage 原样编组进参数（服务端按 wire 原文收到）。
	var raw json.RawMessage
	session2 := memSession(t, func(s *mcp.Server) {
		s.AddTool(&mcp.Tool{Name: "args", InputSchema: objectSchema()},
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				raw = req.Params.Arguments
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
			})
	})
	out, err := callSession(context.Background(), session2, "args", json.RawMessage(`{"path":"/tmp"}`))
	require.NoError(t, err)
	assert.Equal(t, "ok", out)
	assert.JSONEq(t, `{"path":"/tmp"}`, string(raw), "参数按原始 JSON 透传")
}

// ---- listAllTools ----

func TestListAllTools(t *testing.T) {
	session := memSession(t, func(s *mcp.Server) {
		s.AddTool(&mcp.Tool{Name: "read_file", Description: "读文件", InputSchema: objectSchema()},
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{}, nil
			})
		s.AddTool(&mcp.Tool{Name: "write_file", Description: "写文件",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}}},
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{}, nil
			})
	})

	tools, err := listAllTools(context.Background(), session)
	require.NoError(t, err)
	require.Len(t, tools, 2)
	assert.Equal(t, "read_file", tools[0].Name)
	assert.Equal(t, "读文件", tools[0].Description)
	assert.Equal(t, map[string]any{"type": "object"}, tools[0].InputSchema, "客户端侧 schema 已是 map")
	assert.Equal(t, "write_file", tools[1].Name)
	assert.Equal(t, map[string]any{
		"type":       "object",
		"properties": map[string]any{"path": map[string]any{"type": "string"}},
	}, tools[1].InputSchema)
	assert.Empty(t, tools[0].MCPServerID, "server 归属由 service 层赋值，dialer 不掺和")
}

// ---- newTransport / envSlice / nonNilSchema ----

func TestNewTransport(t *testing.T) {
	// stdio：command 按空白切分 + env 继承当前进程后覆盖。
	sv := &MCPServer{
		Name:      "fs",
		Transport: mcpapi.TransportStdio,
		Command:   "npx -y @modelcontextprotocol/server-filesystem /tmp",
		Env:       map[string]string{"FOO": "bar"},
	}
	tt, err := newTransport(sv)
	require.NoError(t, err)
	ct, ok := tt.(*mcp.CommandTransport)
	require.True(t, ok, "stdio → CommandTransport")
	require.NotNil(t, ct.Command)
	assert.Equal(t, "npx", ct.Command.Args[0], "command 按空白切分")
	assert.Equal(t, []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"}, ct.Command.Args[1:])
	found := false
	for _, e := range ct.Command.Env {
		if e == "FOO=bar" {
			found = true
		}
	}
	assert.True(t, found, "env 覆盖注入子进程")

	// stdio 空 command → 错误（存了垃圾数据的防御）。
	_, err = newTransport(&MCPServer{Transport: mcpapi.TransportStdio, Command: "   "})
	assert.Error(t, err)

	// http / sse → 对应 transport。
	tt, err = newTransport(&MCPServer{Transport: mcpapi.TransportHTTP, URL: "http://localhost:3000/mcp"})
	require.NoError(t, err)
	_, ok = tt.(*mcp.StreamableClientTransport)
	assert.True(t, ok, "http → StreamableClientTransport")

	tt, err = newTransport(&MCPServer{Transport: mcpapi.TransportSSE, URL: "http://localhost:3000/sse"})
	require.NoError(t, err)
	_, ok = tt.(*mcp.SSEClientTransport)
	assert.True(t, ok, "sse → SSEClientTransport")

	// 未知 transport → 错误（DB CHECK 兜底之上的应用层防御）。
	_, err = newTransport(&MCPServer{Transport: "grpc", URL: "x"})
	assert.Error(t, err)
}

func TestEnvSlice(t *testing.T) {
	// 键排序保证确定性；空 / nil → nil（append 语义下零长度）。
	assert.Nil(t, envSlice(nil))
	assert.Nil(t, envSlice(map[string]string{}))
	assert.Equal(t, []string{"A=1", "B=2", "C=3"}, envSlice(map[string]string{"C": "3", "A": "1", "B": "2"}))
}

func TestNonNilSchema(t *testing.T) {
	assert.Equal(t, map[string]any{}, nonNilSchema(nil))
	assert.Equal(t, map[string]any{}, nonNilSchema(map[string]any(nil)))
	m := map[string]any{"type": "object"}
	assert.Equal(t, m, nonNilSchema(m))
	// 协议外非对象形态：尽力保真，失败兜底空对象（防 jsonb 写坏 / null）。
	assert.Equal(t, map[string]any{}, nonNilSchema("oops"), "字符串 marshal 后非对象 → 兜底 {}")
}

// ---- 常量钉住（超时边界与翻页护栏防漂移） ----

func TestTimeoutConstants(t *testing.T) {
	assert.Equal(t, 10*time.Second, connectTimeout, "连接 + 握手 10s")
	assert.Equal(t, 30*time.Second, callTimeout, "单次调用（含工具执行）30s")
	assert.Equal(t, 100, maxListPages, "tools/list 翻页护栏")
}

// goSDKDialer 编译期接口断言（防重构时签名漂移导致测试 stub 与真身脱节）。
var _ mcpDialer = goSDKDialer{}

// stubDialer service 测试用的 mcpDialer 桩（service_test.go 消费）。
type stubDialer struct {
	discoverFn func(ctx context.Context, sv *MCPServer) ([]MCPTool, time.Duration, error)
	callFn     func(ctx context.Context, sv *MCPServer, toolName string, args json.RawMessage) (string, error)
}

func (d *stubDialer) discover(ctx context.Context, sv *MCPServer) ([]MCPTool, time.Duration, error) {
	return d.discoverFn(ctx, sv)
}

func (d *stubDialer) call(ctx context.Context, sv *MCPServer, toolName string, args json.RawMessage) (string, error) {
	return d.callFn(ctx, sv, toolName, args)
}

// 确保 stubDialer 确实实现接口（编译期断言放在这里，service_test 无需重复）。
var _ mcpDialer = (*stubDialer)(nil)
