package service

// toolsource 测试：绑定工具子集的 tool.Source 实现——调用名派生 / enabled 过滤 /
// 惰性加载缓存 / Invoke 路由与错误即上下文（IsError 不中断）。stub 复用
// service_test.go 的 stubStore / stubCache 与 mcpclient_test.go 的 stubDialer。

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcpapi "github.com/Karlsk/go-hify/internal/mcp/api"
	"github.com/Karlsk/go-hify/internal/platform/tool"
)

// toolServerRow 构造启 / 停 server 行（stubStore.ListServersByIDs 用）。
func toolServerRow(id uint64, enabled bool) MCPServer {
	sv := MCPServer{Name: "srv", Transport: mcpapi.TransportHTTP, URL: "http://x", Enabled: enabled}
	sv.ID = id
	return sv
}

// toolRow 构造 mcp_tools 行。
func toolRow(id, serverID uint64, name string) MCPTool {
	t := MCPTool{MCPServerID: serverID, Name: name, Description: name + " 工具",
		InputSchema: map[string]any{"type": "object"}}
	t.ID = id
	return t
}

// newToolSourceSvc 组装带 stub 的 service 并返回 ToolSourceFor 出的 Source。
func newToolSourceSvc(st Store, d mcpDialer, toolIDs []uint64) tool.Source {
	s := &service{store: st, cache: &stubCache{}, dialer: d}
	return s.ToolSourceFor(mcpapi.ToolSourceReq{ToolIDs: toolIDs})
}

// ---- deriveCallName ----

func TestDeriveCallName(t *testing.T) {
	cases := []struct {
		serverID uint64
		name     string
		want     string
	}{
		{7, "read_file", "s7__read_file"},
		{7, "write-file", "s7__write-file"},   // 连字符合法保留
		{7, "a.b/c d", "s7__a_b_c_d"},         // 非法字符替换 _
		{7, "搜索", "s7____"},                 // 中文逐 rune 替换（每 rune 一个 _）
		{1, "", "s1__"},                       // 空名（uq 兜底，理论不达）
		{7, strings.Repeat("x", 100), "s7__" + strings.Repeat("x", 60)}, // ≤64 截断
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, deriveCallName(tc.serverID, tc.name),
			"server=%d name=%q", tc.serverID, tc.name)
	}
	// 全 sanitize 后为 ASCII，len 即字符数
	got := deriveCallName(9, strings.Repeat("y", 200))
	assert.Len(t, got, callNameMaxLen)
}

// ---- Tools ----

func TestToolSource_ToolsFiltersDisabledServers(t *testing.T) {
	st := &stubStore{
		listToolsByIDsFunc: func(_ context.Context, ids []uint64) ([]MCPTool, error) {
			return []MCPTool{toolRow(101, 1, "read_file"), toolRow(102, 2, "fetch_url")}, nil
		},
		listServersByIDs: func(_ context.Context, ids []uint64) ([]MCPServer, error) {
			return []MCPServer{toolServerRow(1, true), toolServerRow(2, false)}, nil // server 2 停用
		},
	}
	src := newToolSourceSvc(st, noDialer(), []uint64{101, 102})

	defs, err := src.Tools(context.Background())
	require.NoError(t, err)
	require.Len(t, defs, 1, "停用 server 的工具静默消失")
	assert.Equal(t, "s1__read_file", defs[0].Name)
	assert.Equal(t, "read_file 工具", defs[0].Description)
	assert.JSONEq(t, `{"type":"object"}`, string(defs[0].InputSchema))
}

func TestToolSource_ToolsCachesAfterFirstLoad(t *testing.T) {
	st := &stubStore{
		listToolsByIDsFunc: func(_ context.Context, _ []uint64) ([]MCPTool, error) {
			return []MCPTool{toolRow(101, 1, "read_file")}, nil
		},
		listServersByIDs: func(_ context.Context, _ []uint64) ([]MCPServer, error) {
			return []MCPServer{toolServerRow(1, true)}, nil
		},
	}
	src := newToolSourceSvc(st, noDialer(), []uint64{101})

	_, err := src.Tools(context.Background())
	require.NoError(t, err)
	_, err = src.Tools(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, st.calls.listToolsByIDs, "首次成功后缓存，第二次不再查库")
}

func TestToolSource_ToolsErrorNotCached(t *testing.T) {
	fail := true
	st := &stubStore{
		listToolsByIDsFunc: func(_ context.Context, _ []uint64) ([]MCPTool, error) {
			if fail {
				return nil, errors.New("db down")
			}
			return []MCPTool{toolRow(101, 1, "read_file")}, nil
		},
		listServersByIDs: func(_ context.Context, _ []uint64) ([]MCPServer, error) {
			return []MCPServer{toolServerRow(1, true)}, nil
		},
	}
	src := newToolSourceSvc(st, noDialer(), []uint64{101})

	_, err := src.Tools(context.Background())
	require.Error(t, err, "DB 错误上抛（Registry / chat 降级处理）")

	fail = false
	defs, err := src.Tools(context.Background())
	require.NoError(t, err, "失败不缓存，下次重试")
	assert.Len(t, defs, 1)
}

func TestToolSource_ToolsEmptyIDs(t *testing.T) {
	st := &stubStore{} // 未注入即 nil 行为，但空 IDs 短路不触库
	src := newToolSourceSvc(st, noDialer(), nil)
	defs, err := src.Tools(context.Background())
	require.NoError(t, err)
	assert.Empty(t, defs)
	assert.Zero(t, st.calls.listToolsByIDs, "空 IDs 短路")
}

// ---- Invoke ----

func TestToolSource_InvokeRoutesAndReturnsResult(t *testing.T) {
	st := &stubStore{
		listToolsByIDsFunc: func(_ context.Context, _ []uint64) ([]MCPTool, error) {
			return []MCPTool{toolRow(101, 1, "read_file")}, nil
		},
		listServersByIDs: func(_ context.Context, _ []uint64) ([]MCPServer, error) {
			return []MCPServer{toolServerRow(1, true)}, nil
		},
	}
	var gotServer string
	var gotArgs json.RawMessage
	st.getServerByID = func(_ context.Context, id uint64) (*MCPServer, error) {
		sv := toolServerRow(id, true)
		return &sv, nil
	}
	d := &stubDialer{callFn: func(_ context.Context, sv *MCPServer, toolName string, args json.RawMessage) (string, error) {
		gotServer, gotArgs = sv.Name, args
		return "line1\nline2", nil
	}}
	src := newToolSourceSvc(st, d, []uint64{101})

	res := src.Invoke(context.Background(), tool.ToolCall{
		ID: "call_1", Name: "s1__read_file", Arguments: json.RawMessage(`{"path":"/a"}`),
	})
	assert.False(t, res.IsError)
	assert.Equal(t, "call_1", res.CallID)
	assert.Equal(t, "s1__read_file", res.Name)
	assert.Equal(t, "line1\nline2", res.Content)
	assert.Equal(t, "srv", gotServer, "经缓存路由到正确 server")
	assert.JSONEq(t, `{"path":"/a"}`, string(gotArgs), "参数原样透传")
}

func TestToolSource_InvokeUnknownNameIsError(t *testing.T) {
	st := &stubStore{
		listToolsByIDsFunc: func(_ context.Context, _ []uint64) ([]MCPTool, error) {
			return []MCPTool{toolRow(101, 1, "read_file")}, nil
		},
		listServersByIDs: func(_ context.Context, _ []uint64) ([]MCPServer, error) {
			return []MCPServer{toolServerRow(1, true)}, nil
		},
	}
	src := newToolSourceSvc(st, noDialer(), []uint64{101})

	res := src.Invoke(context.Background(), tool.ToolCall{ID: "c", Name: "s9__nope"})
	assert.True(t, res.IsError, "未知名：IsError 结果喂回 LLM，不中断")
	assert.Contains(t, res.Content, "s9__nope")
}

func TestToolSource_InvokeDialerErrorIsError(t *testing.T) {
	st := &stubStore{
		listToolsByIDsFunc: func(_ context.Context, _ []uint64) ([]MCPTool, error) {
			return []MCPTool{toolRow(101, 1, "read_file")}, nil
		},
		listServersByIDs: func(_ context.Context, _ []uint64) ([]MCPServer, error) {
			return []MCPServer{toolServerRow(1, true)}, nil
		},
	}
	d := &stubDialer{callFn: func(_ context.Context, _ *MCPServer, _ string, _ json.RawMessage) (string, error) {
		return "", errors.New("connect mcp server \"srv\" (http): refused")
	}}
	src := newToolSourceSvc(st, d, []uint64{101})

	res := src.Invoke(context.Background(), tool.ToolCall{ID: "c", Name: "s1__read_file"})
	assert.True(t, res.IsError, "调用失败：错误即上下文")
	assert.Contains(t, res.Content, "refused")
}

// 超长错误文本按 rune 截断（源头侧防御；chat 侧另有 20000 rune 统一截断）。
func TestToolSource_InvokeErrorTruncated(t *testing.T) {
	st := &stubStore{
		listToolsByIDsFunc: func(_ context.Context, _ []uint64) ([]MCPTool, error) {
			return []MCPTool{toolRow(101, 1, "read_file")}, nil
		},
		listServersByIDs: func(_ context.Context, _ []uint64) ([]MCPServer, error) {
			return []MCPServer{toolServerRow(1, true)}, nil
		},
	}
	d := &stubDialer{callFn: func(_ context.Context, _ *MCPServer, _ string, _ json.RawMessage) (string, error) {
		return "", errors.New(strings.Repeat("错", maxErrTextRunes+500))
	}}
	src := newToolSourceSvc(st, d, []uint64{101})

	res := src.Invoke(context.Background(), tool.ToolCall{ID: "c", Name: "s1__read_file"})
	assert.True(t, res.IsError)
	assert.Len(t, []rune(res.Content), maxErrTextRunes+1) // 截断 + 省略号
}

// server 在清单加载后被删（getServer 404）：IsError 结果，不 panic 不中断。
func TestToolSource_InvokeServerVanished(t *testing.T) {
	st := &stubStore{
		listToolsByIDsFunc: func(_ context.Context, _ []uint64) ([]MCPTool, error) {
			return []MCPTool{toolRow(101, 1, "read_file")}, nil
		},
		listServersByIDs: func(_ context.Context, _ []uint64) ([]MCPServer, error) {
			return []MCPServer{toolServerRow(1, true)}, nil
		},
		getServerByID: func(_ context.Context, id uint64) (*MCPServer, error) {
			return nil, mcpapi.ErrMCPServerNotFound
		},
	}
	src := newToolSourceSvc(st, noDialer(), []uint64{101})

	res := src.Invoke(context.Background(), tool.ToolCall{ID: "c", Name: "s1__read_file"})
	assert.True(t, res.IsError)
	assert.Contains(t, res.Content, "获取工具服务器失败")
}

// ensure 失败时 Invoke 也恒返回结果（不 panic）。
func TestToolSource_InvokeEnsureFailureIsError(t *testing.T) {
	st := &stubStore{
		listToolsByIDsFunc: func(_ context.Context, _ []uint64) ([]MCPTool, error) {
			return nil, errors.New("db down")
		},
	}
	src := newToolSourceSvc(st, noDialer(), []uint64{101})

	res := src.Invoke(context.Background(), tool.ToolCall{ID: "c", Name: "s1__read_file"})
	assert.True(t, res.IsError)
	assert.Contains(t, res.Content, "加载工具清单失败")
}

// 并发 Tools / Invoke：mu 保护 ensure，查库只发生一次。
func TestToolSource_ConcurrentEnsureSingleLoad(t *testing.T) {
	st := &stubStore{
		listToolsByIDsFunc: func(_ context.Context, _ []uint64) ([]MCPTool, error) {
			return []MCPTool{toolRow(101, 1, "read_file")}, nil
		},
		listServersByIDs: func(_ context.Context, _ []uint64) ([]MCPServer, error) {
			return []MCPServer{toolServerRow(1, true)}, nil
		},
	}
	src := newToolSourceSvc(st, noDialer(), []uint64{101})

	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			_, _ = src.Tools(context.Background())
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
	assert.Equal(t, 1, st.calls.listToolsByIDs, "并发下 ensure 只查库一次")
}

// ---- spec 020：ServerIDs 并集解析（FR-002 / C4）----

// newToolSourceSvcWithServers 带 ServerIDs 的 Source 装配（stub store 记录两路查询）。
func newToolSourceSvcWithServers(st Store, d mcpDialer, toolIDs, serverIDs []uint64) tool.Source {
	s := &service{store: st, cache: &stubCache{}, dialer: d}
	return s.ToolSourceFor(mcpapi.ToolSourceReq{ToolIDs: toolIDs, ServerIDs: serverIDs})
}

// 并集：toolIDs ∪ serverIDs 全量，重叠 tool 按 id 去重只解析一次。
func TestToolSourceServerIDsUnion(t *testing.T) {
	st := &stubStore{
		listToolsByIDsFunc: func(_ context.Context, ids []uint64) ([]MCPTool, error) {
			return []MCPTool{toolRow(7, 1, "fetch")}, nil
		},
		listToolsByServerIDsFunc: func(_ context.Context, ids []uint64) ([]MCPTool, error) {
			assert.Equal(t, []uint64{1}, ids)
			return []MCPTool{toolRow(7, 1, "fetch"), toolRow(8, 1, "search")}, nil // 7 与 toolIDs 重叠
		},
		listServersByIDs: func(_ context.Context, ids []uint64) ([]MCPServer, error) {
			return []MCPServer{toolServerRow(1, true)}, nil
		},
	}
	src := newToolSourceSvcWithServers(st, &stubDialer{}, []uint64{7}, []uint64{1})
	defs, err := src.Tools(context.Background())
	require.NoError(t, err)
	assert.Len(t, defs, 2, "重叠 tool 去重：并集 = {fetch, search}")
	names := []string{defs[0].Name, defs[1].Name}
	assert.Contains(t, names, "s1__fetch")
	assert.Contains(t, names, "s1__search")
}

// 只传 ServerIDs：server 全量工具动态解析（不依赖 toolIDs 快照）。
func TestToolSourceServerIDsOnly(t *testing.T) {
	st := &stubStore{
		listToolsByServerIDsFunc: func(_ context.Context, ids []uint64) ([]MCPTool, error) {
			return []MCPTool{toolRow(7, 2, "a"), toolRow(8, 2, "b")}, nil
		},
		listServersByIDs: func(_ context.Context, ids []uint64) ([]MCPServer, error) {
			return []MCPServer{toolServerRow(2, true)}, nil
		},
	}
	src := newToolSourceSvcWithServers(st, &stubDialer{}, nil, []uint64{2})
	defs, err := src.Tools(context.Background())
	require.NoError(t, err)
	assert.Len(t, defs, 2)
	assert.Equal(t, 0, st.calls.listToolsByIDs, "只传 ServerIDs 零 toolIDs 查询")
}

// 并集面停用 server 过滤同语义：server 停用 → 其工具（含 serverIDs 面）静默消失。
func TestToolSourceServerIDsDisabledFilter(t *testing.T) {
	st := &stubStore{
		listToolsByServerIDsFunc: func(_ context.Context, ids []uint64) ([]MCPTool, error) {
			return []MCPTool{toolRow(7, 3, "a")}, nil
		},
		listToolsByIDsFunc: func(_ context.Context, ids []uint64) ([]MCPTool, error) {
			return []MCPTool{toolRow(9, 4, "b")}, nil
		},
		listServersByIDs: func(_ context.Context, ids []uint64) ([]MCPServer, error) {
			return []MCPServer{toolServerRow(3, false), toolServerRow(4, true)}, nil
		},
	}
	src := newToolSourceSvcWithServers(st, &stubDialer{}, []uint64{9}, []uint64{3})
	defs, err := src.Tools(context.Background())
	require.NoError(t, err)
	require.Len(t, defs, 1, "停用 server 的工具静默消失（并集面同语义）")
	assert.Equal(t, "s4__b", defs[0].Name)
}

// 只传 ToolIDs 路径逐字不变（chat 零影响证明）：serverIDs 空 = 零 server 批量查询。
func TestToolSourceToolIDsOnlyUnchanged(t *testing.T) {
	st := &stubStore{
		listToolsByIDsFunc: func(_ context.Context, ids []uint64) ([]MCPTool, error) {
			return []MCPTool{toolRow(7, 1, "fetch")}, nil
		},
		listServersByIDs: func(_ context.Context, ids []uint64) ([]MCPServer, error) {
			return []MCPServer{toolServerRow(1, true)}, nil
		},
	}
	src := newToolSourceSvc(st, &stubDialer{}, []uint64{7})
	defs, err := src.Tools(context.Background())
	require.NoError(t, err)
	assert.Len(t, defs, 1)
	assert.Equal(t, 0, st.calls.listToolsByServerIDs, "只传 ToolIDs 零 server 批量查询")
}
