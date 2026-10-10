package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	mcpapi "github.com/Karlsk/go-hify/internal/mcp/api"
	"github.com/Karlsk/go-hify/internal/platform/cache"
	"github.com/Karlsk/go-hify/internal/platform/page"
)

// service 测试：stub 本模块 Store / cacheManager / mcpDialer（stubDialer 在
// mcpclient_test.go），覆盖 CRUD 编排、哨兵翻译（23505 / 23503 按约束名分发）、
// 缓存读路径与失效矩阵（server / tools 两条边）、discover 两态语义（连通失败
// 也是 200）与跨模块调用面。

// ---- stubs ----

// stubStore 按方法注入行为；未注入的方法返回合理零值。calls 记录调用足迹供断言。
type stubStore struct {
	createServer       func(ctx context.Context, s *MCPServer) error
	getServerByID      func(ctx context.Context, id uint64) (*MCPServer, error)
	listServersByIDs   func(ctx context.Context, ids []uint64) ([]MCPServer, error)
	listServers        func(ctx context.Context, p page.OffsetParams) (page.OffsetResult[MCPServer], error)
	updateServer       func(ctx context.Context, s *MCPServer) error
	deleteServer       func(ctx context.Context, id uint64) error
	upsertTools        func(ctx context.Context, tools []MCPTool) error
	listToolsByServer  func(ctx context.Context, serverID uint64) ([]MCPTool, error)
	countTools         func(ctx context.Context, ids []uint64) (map[uint64]int64, error)
	listToolsByIDsFunc func(ctx context.Context, ids []uint64) ([]MCPTool, error)
	// spec 020：server 级并集绑定批量查询缝
	listToolsByServerIDsFunc func(ctx context.Context, ids []uint64) ([]MCPTool, error)

	calls struct {
		createServer, getServerByID, updateServer, deleteServer int
		upsertTools, listToolsByServer                          int
		listToolsByIDs, listToolsByServerIDs                    int
	}
}

func (s *stubStore) CreateServer(ctx context.Context, sv *MCPServer) error {
	s.calls.createServer++
	if s.createServer != nil {
		return s.createServer(ctx, sv)
	}
	sv.ID = 42 // 模拟 DB RETURNING 回填
	return nil
}

func (s *stubStore) GetServerByID(ctx context.Context, id uint64) (*MCPServer, error) {
	s.calls.getServerByID++
	if s.getServerByID != nil {
		return s.getServerByID(ctx, id)
	}
	return sampleServer(id), nil
}

func (s *stubStore) ListServersByIDs(ctx context.Context, ids []uint64) ([]MCPServer, error) {
	if s.listServersByIDs != nil {
		return s.listServersByIDs(ctx, ids)
	}
	return nil, nil
}

func (s *stubStore) ListServers(ctx context.Context, p page.OffsetParams) (page.OffsetResult[MCPServer], error) {
	if s.listServers != nil {
		return s.listServers(ctx, p)
	}
	return page.NewOffsetResult([]MCPServer{}, p, 0), nil
}

func (s *stubStore) UpdateServer(ctx context.Context, sv *MCPServer) error {
	s.calls.updateServer++
	if s.updateServer != nil {
		return s.updateServer(ctx, sv)
	}
	return nil
}

func (s *stubStore) DeleteServer(ctx context.Context, id uint64) error {
	s.calls.deleteServer++
	if s.deleteServer != nil {
		return s.deleteServer(ctx, id)
	}
	return nil
}

func (s *stubStore) UpsertTools(ctx context.Context, tools []MCPTool) error {
	s.calls.upsertTools++
	if s.upsertTools != nil {
		return s.upsertTools(ctx, tools)
	}
	return nil
}

func (s *stubStore) ListToolsByServer(ctx context.Context, serverID uint64) ([]MCPTool, error) {
	s.calls.listToolsByServer++
	if s.listToolsByServer != nil {
		return s.listToolsByServer(ctx, serverID)
	}
	return sampleTools(serverID), nil
}

func (s *stubStore) CountToolsByServerIDs(ctx context.Context, ids []uint64) (map[uint64]int64, error) {
	if s.countTools != nil {
		return s.countTools(ctx, ids)
	}
	return map[uint64]int64{}, nil
}

func (s *stubStore) ListToolsByIDs(ctx context.Context, ids []uint64) ([]MCPTool, error) {
	s.calls.listToolsByIDs++
	if s.listToolsByIDsFunc != nil {
		return s.listToolsByIDsFunc(ctx, ids)
	}
	return nil, nil
}

// ListToolsByServerIDs spec 020 server 级并集绑定批量查询 stub。
func (s *stubStore) ListToolsByServerIDs(ctx context.Context, ids []uint64) ([]MCPTool, error) {
	s.calls.listToolsByServerIDs++
	if s.listToolsByServerIDsFunc != nil {
		return s.listToolsByServerIDsFunc(ctx, ids)
	}
	return nil, nil
}

// stubCache 按方法注入行为；Delete 默认记录 key 后成功。
type stubCache struct {
	get    func(ctx context.Context, name, key string, dst any) (bool, error)
	set    func(ctx context.Context, name, key string, val any) error
	delete func(ctx context.Context, name, key string) error

	deletedKeys []string
}

func (s *stubCache) Get(ctx context.Context, name, key string, dst any) (bool, error) {
	if s.get == nil {
		return false, nil
	}
	return s.get(ctx, name, key, dst)
}

func (s *stubCache) Set(ctx context.Context, name, key string, val any) error {
	if s.set == nil {
		return nil
	}
	return s.set(ctx, name, key, val)
}

func (s *stubCache) Delete(ctx context.Context, name, key string) error {
	s.deletedKeys = append(s.deletedKeys, key)
	if s.delete != nil {
		return s.delete(ctx, name, key)
	}
	return nil
}

// ---- 测试辅助 ----

func uniqueErr() error {
	return fmt.Errorf("insert server: %w", &pgconn.PgError{Code: "23505"})
}

// fkErrNamed 带约束名的 23503（Delete 按约束名分发：fk_agent_tools_tool_id 挡删）。
func fkErrNamed(constraint string) error {
	return fmt.Errorf("delete server: %w", &pgconn.PgError{Code: "23503", ConstraintName: constraint})
}

func sampleServer(id uint64) *MCPServer {
	sv := &MCPServer{
		Name:      "filesystem",
		Transport: mcpapi.TransportStdio,
		Command:   "npx -y @modelcontextprotocol/server-filesystem /tmp",
		Enabled:   true,
	}
	sv.ID = id
	return sv
}

// sampleTools 两条工具：read_file 带 schema、write_file schema 留 nil（→ "{}" 兜底）。
func sampleTools(serverID uint64) []MCPTool {
	t1 := MCPTool{MCPServerID: serverID, Name: "read_file", Description: "读文件",
		InputSchema: map[string]any{"type": "object"}}
	t1.ID = 101
	t2 := MCPTool{MCPServerID: serverID, Name: "write_file", Description: "写文件"}
	t2.ID = 102
	return []MCPTool{t1, t2}
}

// newSvc 直接组装 service（同包），注入 stub dialer；两张面孔同一实例。
func newSvc(st Store, cm cacheManager, d mcpDialer) (mcpapi.MCPServerService, mcpapi.MCPClientService) {
	s := &service{store: st, cache: cm, dialer: d}
	return s, s
}

// noDialer 不会被调到的桩（断言「不触达外部」的用例用它，调用即 panic 暴露）。
func noDialer() *stubDialer {
	return &stubDialer{
		discoverFn: func(ctx context.Context, sv *MCPServer) ([]MCPTool, time.Duration, error) {
			panic("discover should not be called")
		},
		callFn: func(ctx context.Context, sv *MCPServer, toolName string, args json.RawMessage) (string, error) {
			panic("call should not be called")
		},
	}
}

// ---- Create（MCPServerService） ----

func TestCreate_Happy(t *testing.T) {
	st := &stubStore{}
	var captured *MCPServer
	st.createServer = func(ctx context.Context, sv *MCPServer) error {
		captured = sv
		sv.ID = 42
		return nil
	}
	svc, _ := newSvc(st, &stubCache{}, noDialer())

	resp, err := svc.Create(context.Background(), mcpapi.CreateMCPServerReq{
		Name: "filesystem", Transport: mcpapi.TransportStdio,
		Command: "npx -y @modelcontextprotocol/server-filesystem /tmp",
		Env:     map[string]string{"ROOT": "/data"},
	})
	require.NoError(t, err)
	assert.Equal(t, "42", resp.ID, "RETURNING 回填的 id 字符串化")
	assert.Equal(t, "filesystem", resp.Name)
	assert.True(t, resp.Enabled, "未传 enabled → 创建即启用")
	assert.Equal(t, map[string]string{"ROOT": "/data"}, captured.Env)
}

func TestCreate_DefaultsGuardZeroValues(t *testing.T) {
	// provider 踩坑 #1 回归：显式 false（停用）不得被缺省吞掉；Env nil → 空 map 防 jsonb null。
	st := &stubStore{}
	var captured *MCPServer
	st.createServer = func(ctx context.Context, sv *MCPServer) error {
		captured = sv
		return nil
	}
	svc, _ := newSvc(st, &stubCache{}, noDialer())

	off := false
	resp, err := svc.Create(context.Background(), mcpapi.CreateMCPServerReq{
		Name: "s", Transport: mcpapi.TransportSSE, URL: "http://x/sse", Enabled: &off,
	})
	require.NoError(t, err)
	assert.False(t, resp.Enabled)
	assert.False(t, captured.Enabled)
	assert.Equal(t, map[string]string{}, captured.Env, "nil env 兜底空 map")
}

func TestCreate_NameConflict(t *testing.T) {
	st := &stubStore{createServer: func(ctx context.Context, sv *MCPServer) error { return uniqueErr() }}
	svc, _ := newSvc(st, &stubCache{}, noDialer())

	_, err := svc.Create(context.Background(), mcpapi.CreateMCPServerReq{
		Name: "重名", Transport: mcpapi.TransportSSE, URL: "http://x/sse",
	})
	assert.ErrorIs(t, err, mcpapi.ErrMCPServerNameConflict, "23505 → name 唯一冲突")
}

func TestCreate_Validation(t *testing.T) {
	// 跨字段校验（binding tag 只在 HTTP 层生效，直调方走 Validate）：stdio 缺 command。
	svc, _ := newSvc(&stubStore{}, &stubCache{}, noDialer())
	_, err := svc.Create(context.Background(), mcpapi.CreateMCPServerReq{Name: "s", Transport: mcpapi.TransportStdio})
	assert.Error(t, err)
}

// ---- Get ----

func TestGet_CacheHitBoth(t *testing.T) {
	st := &stubStore{}
	cm := &stubCache{get: func(ctx context.Context, name, key string, dst any) (bool, error) {
		switch name {
		case cache.NameMCPServer:
			assert.Equal(t, "server:1", key)
			*(dst.(*MCPServer)) = *sampleServer(1)
			dst.(*MCPServer).Name = "缓存里的server"
		case cache.NameMCPTool:
			assert.Equal(t, "tools:1", key)
			*(dst.(*[]mcpapi.ToolSchema)) = []mcpapi.ToolSchema{
				{ID: "101", MCPServerID: "1", Name: "cached_tool"},
			}
		default:
			return false, nil
		}
		return true, nil
	}}
	svc, _ := newSvc(st, cm, noDialer())

	resp, err := svc.Get(context.Background(), mcpapi.GetMCPServerReq{ID: 1})
	require.NoError(t, err)
	assert.Equal(t, "缓存里的server", resp.Name)
	assert.Equal(t, []mcpapi.ToolSchema{{ID: "101", MCPServerID: "1", Name: "cached_tool"}}, resp.Tools)
	assert.Equal(t, 0, st.calls.getServerByID, "server 命中缓存不落库")
	assert.Equal(t, 0, st.calls.listToolsByServer, "tools 命中缓存不落库")
}

func TestGet_CacheMiss(t *testing.T) {
	st := &stubStore{}
	setCalls := map[string]int{}
	var serverVal, toolsVal any
	cm := &stubCache{set: func(ctx context.Context, name, key string, val any) error {
		setCalls[name]++
		switch name {
		case cache.NameMCPServer:
			serverVal = val
		case cache.NameMCPTool:
			toolsVal = val
		}
		return nil
	}}
	svc, _ := newSvc(st, cm, noDialer())

	resp, err := svc.Get(context.Background(), mcpapi.GetMCPServerReq{ID: 1})
	require.NoError(t, err)
	assert.Equal(t, "filesystem", resp.Name)
	assert.NotNil(t, resp.Env, "nil env 详情回 {} 不回 null")
	assert.Empty(t, resp.Env)
	require.Len(t, resp.Tools, 2)
	assert.Equal(t, "101", resp.Tools[0].ID)
	assert.Equal(t, "1", resp.Tools[0].MCPServerID)
	assert.JSONEq(t, `{"type":"object"}`, string(resp.Tools[0].InputSchema))
	assert.JSONEq(t, `{}`, string(resp.Tools[1].InputSchema), "nil schema 兜底 {}")
	assert.Equal(t, 1, setCalls[cache.NameMCPServer], "server 实体回填缓存")
	assert.Equal(t, 1, setCalls[cache.NameMCPTool], "tools 清单回填缓存")
	assert.IsType(t, &MCPServer{}, serverVal, "server 缓存载荷是 model 指针（CallTool 需要 env）")
	assert.IsType(t, []mcpapi.ToolSchema{}, toolsVal)
}

func TestGet_CacheReadErrorFallsBack(t *testing.T) {
	st := &stubStore{}
	cm := &stubCache{get: func(ctx context.Context, name, key string, dst any) (bool, error) {
		return false, errors.New("redis down")
	}}
	svc, _ := newSvc(st, cm, noDialer())

	resp, err := svc.Get(context.Background(), mcpapi.GetMCPServerReq{ID: 1})
	assert.NoError(t, err, "缓存读失败降级 DB，不失败请求")
	assert.Equal(t, "filesystem", resp.Name)
}

func TestGet_NotFound(t *testing.T) {
	st := &stubStore{getServerByID: func(ctx context.Context, id uint64) (*MCPServer, error) {
		return nil, gorm.ErrRecordNotFound
	}}
	cm := &stubCache{}
	svc, _ := newSvc(st, cm, noDialer())

	_, err := svc.Get(context.Background(), mcpapi.GetMCPServerReq{ID: 999})
	assert.ErrorIs(t, err, mcpapi.ErrMCPServerNotFound)
	assert.Empty(t, cm.deletedKeys)
}

// ---- List ----

func TestList_ToolCountAggregate(t *testing.T) {
	st := &stubStore{
		listServers: func(ctx context.Context, p page.OffsetParams) (page.OffsetResult[MCPServer], error) {
			return page.NewOffsetResult([]MCPServer{*sampleServer(1), *sampleServer(2)}, p, 2), nil
		},
		countTools: func(ctx context.Context, ids []uint64) (map[uint64]int64, error) {
			assert.Equal(t, []uint64{1, 2}, ids, "当页 id 一次批量计数（防 N+1）")
			return map[uint64]int64{1: 3}, nil // server 2 无工具 → map 无键 = 0
		},
	}
	svc, _ := newSvc(st, &stubCache{}, noDialer())

	resp, err := svc.List(context.Background(), mcpapi.ListMCPServersReq{Page: 1, PageSize: 20})
	require.NoError(t, err)
	assert.Equal(t, int64(2), resp.Total)
	require.Len(t, resp.Items, 2)
	assert.Equal(t, int64(3), resp.Items[0].ToolCount)
	assert.Equal(t, int64(0), resp.Items[1].ToolCount)
	assert.Equal(t, 1, resp.Page)
	assert.Equal(t, 20, resp.PageSize)
}

func TestList_EmptyPageNotEmptyArray(t *testing.T) {
	svc, _ := newSvc(&stubStore{}, &stubCache{}, noDialer())

	resp, err := svc.List(context.Background(), mcpapi.ListMCPServersReq{})
	require.NoError(t, err)
	assert.NotNil(t, resp.Items, "空页 items 返 [] 不返 null")
	assert.Empty(t, resp.Items)
}

// ---- Update ----

func TestUpdate_Happy(t *testing.T) {
	st := &stubStore{}
	var captured *MCPServer
	st.updateServer = func(ctx context.Context, sv *MCPServer) error {
		captured = sv
		return nil
	}
	cm := &stubCache{}
	svc, _ := newSvc(st, cm, noDialer())

	off := false
	resp, err := svc.Update(context.Background(), mcpapi.UpdateMCPServerReq{
		ID: 1, Name: "改名", Transport: mcpapi.TransportHTTP, URL: "http://x/mcp", Enabled: &off,
	})
	require.NoError(t, err)
	assert.Equal(t, "1", resp.ID)
	require.NotNil(t, captured)
	assert.Equal(t, uint64(1), captured.ID, "实体取自 DB，保住主键")
	assert.Equal(t, "改名", captured.Name)
	assert.Equal(t, mcpapi.TransportHTTP, captured.Transport)
	assert.Equal(t, "http://x/mcp", captured.URL)
	assert.False(t, captured.Enabled, "显式 false 停用（踩坑 #1 回归）")
	assert.Equal(t, map[string]string{}, captured.Env, "PUT 未带 env → 空覆盖（全量语义）")
	assert.Equal(t, []string{"server:1"}, cm.deletedKeys, "写时只失效 server key（tools 行未动）")
}

func TestUpdate_NotFound(t *testing.T) {
	st := &stubStore{getServerByID: func(ctx context.Context, id uint64) (*MCPServer, error) {
		return nil, gorm.ErrRecordNotFound
	}}
	svc, _ := newSvc(st, &stubCache{}, noDialer())

	_, err := svc.Update(context.Background(), mcpapi.UpdateMCPServerReq{ID: 999, Name: "x", Transport: mcpapi.TransportSSE, URL: "http://x/sse"})
	assert.ErrorIs(t, err, mcpapi.ErrMCPServerNotFound)
}

func TestUpdate_NameConflict(t *testing.T) {
	st := &stubStore{updateServer: func(ctx context.Context, sv *MCPServer) error { return uniqueErr() }}
	svc, _ := newSvc(st, &stubCache{}, noDialer())

	_, err := svc.Update(context.Background(), mcpapi.UpdateMCPServerReq{ID: 1, Name: "重名", Transport: mcpapi.TransportSSE, URL: "http://x/sse"})
	assert.ErrorIs(t, err, mcpapi.ErrMCPServerNameConflict)
}

// ---- Delete ----

func TestDelete_HappyEvictsBothKeys(t *testing.T) {
	st := &stubStore{}
	cm := &stubCache{}
	svc, _ := newSvc(st, cm, noDialer())

	err := svc.Delete(context.Background(), mcpapi.DeleteMCPServerReq{ID: 1})
	require.NoError(t, err)
	assert.Equal(t, 1, st.calls.deleteServer)
	// server + tools 双 key 失效：级联删清了 mcp_tools 行，tools 缓存不删会在 TTL
	// 窗口内继续供出已死工具（db_model.md §2.5 矩阵的 Delete 行）。
	assert.Equal(t, []string{"server:1", "tools:1"}, cm.deletedKeys)
}

func TestDelete_NotFound(t *testing.T) {
	st := &stubStore{deleteServer: func(ctx context.Context, id uint64) error { return gorm.ErrRecordNotFound }}
	svc, _ := newSvc(st, &stubCache{}, noDialer())

	err := svc.Delete(context.Background(), mcpapi.DeleteMCPServerReq{ID: 999})
	assert.ErrorIs(t, err, mcpapi.ErrMCPServerNotFound)
}

func TestDelete_InUse(t *testing.T) {
	// 有 Agent 绑定该 server 的工具：agent_tools.tool_id FK RESTRICT 抛 23503
	//（约束名 fk_agent_tools_tool_id，迁移 00021 显式命名）→ 挡删。
	st := &stubStore{deleteServer: func(ctx context.Context, id uint64) error {
		return fkErrNamed("fk_agent_tools_tool_id")
	}}
	cm := &stubCache{}
	svc, _ := newSvc(st, cm, noDialer())

	err := svc.Delete(context.Background(), mcpapi.DeleteMCPServerReq{ID: 1})
	assert.ErrorIs(t, err, mcpapi.ErrMCPServerInUse)
	assert.Empty(t, cm.deletedKeys, "删除被挡 DB 未变，不失效缓存")
}

func TestDelete_OtherFKNotTranslated(t *testing.T) {
	// 其余 23503（理论不可达，mcp_servers 无其他引用方）不翻译，原样包装上抛。
	st := &stubStore{deleteServer: func(ctx context.Context, id uint64) error {
		return fkErrNamed("some_other_fk")
	}}
	svc, _ := newSvc(st, &stubCache{}, noDialer())

	err := svc.Delete(context.Background(), mcpapi.DeleteMCPServerReq{ID: 1})
	require.Error(t, err)
	assert.NotErrorIs(t, err, mcpapi.ErrMCPServerInUse)
}

// ---- Discover ----

func TestDiscover_Ok(t *testing.T) {
	st := &stubStore{}
	var upserted []MCPTool
	st.upsertTools = func(ctx context.Context, tools []MCPTool) error {
		upserted = tools
		return nil
	}
	st.countTools = func(ctx context.Context, ids []uint64) (map[uint64]int64, error) {
		return map[uint64]int64{1: 2}, nil
	}
	cm := &stubCache{}
	d := &stubDialer{discoverFn: func(ctx context.Context, sv *MCPServer) ([]MCPTool, time.Duration, error) {
		assert.Equal(t, uint64(1), sv.ID)
		return []MCPTool{{Name: "read_file", Description: "读文件"}, {Name: "write_file"}}, 150 * time.Millisecond, nil
	}}
	svc, _ := newSvc(st, cm, d)

	resp, err := svc.Discover(context.Background(), mcpapi.DiscoverReq{ID: 1})
	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Equal(t, int32(150), resp.LatencyMs)
	assert.Equal(t, int64(2), resp.ToolCount, "upsert 后该 server 工具总数")
	require.Len(t, upserted, 2)
	assert.Equal(t, uint64(1), upserted[0].MCPServerID, "server 归属由 service 赋值")
	assert.Equal(t, uint64(1), upserted[1].MCPServerID)
	assert.Equal(t, []string{"tools:1"}, cm.deletedKeys, "upsert 后失效 tools key")
	assert.Equal(t, 1, st.calls.upsertTools)
}

func TestDiscover_DialFailStill200(t *testing.T) {
	// 连通失败也是 200：成败在 DiscoverResultSchema（provider TestConnection 同款），
	// 不 upsert、不失效缓存。
	st := &stubStore{}
	cm := &stubCache{}
	d := &stubDialer{discoverFn: func(ctx context.Context, sv *MCPServer) ([]MCPTool, time.Duration, error) {
		return nil, 75 * time.Millisecond, errors.New("connect mcp server \"filesystem\" (stdio): connection refused")
	}}
	svc, _ := newSvc(st, cm, d)

	resp, err := svc.Discover(context.Background(), mcpapi.DiscoverReq{ID: 1})
	require.NoError(t, err, "连通失败不是接口错误")
	assert.False(t, resp.Success)
	assert.Equal(t, int32(75), resp.LatencyMs)
	assert.Contains(t, resp.ErrorMessage, "connection refused")
	assert.Equal(t, 0, st.calls.upsertTools, "失败不落库")
	assert.Empty(t, cm.deletedKeys)
}

func TestDiscover_ServerNotFound(t *testing.T) {
	st := &stubStore{getServerByID: func(ctx context.Context, id uint64) (*MCPServer, error) {
		return nil, gorm.ErrRecordNotFound
	}}
	svc, _ := newSvc(st, &stubCache{}, noDialer())

	_, err := svc.Discover(context.Background(), mcpapi.DiscoverReq{ID: 999})
	assert.ErrorIs(t, err, mcpapi.ErrMCPServerNotFound, "server 不存在是 404，不是连通失败")
}

func TestDiscover_ErrorMessageTruncated(t *testing.T) {
	// 600 rune 中文错误 → rune 边界安全截断到 512 + 省略号（防上游错误刷屏响应字段）。
	d := &stubDialer{discoverFn: func(ctx context.Context, sv *MCPServer) ([]MCPTool, time.Duration, error) {
		return nil, time.Millisecond, errors.New(strings.Repeat("连", 600))
	}}
	svc, _ := newSvc(&stubStore{}, &stubCache{}, d)

	resp, err := svc.Discover(context.Background(), mcpapi.DiscoverReq{ID: 1})
	require.NoError(t, err)
	assert.Len(t, []rune(resp.ErrorMessage), 513, "512 rune + 省略号")
}

// ---- RunTool ----

func TestRunTool_Happy(t *testing.T) {
	var gotTool string
	var gotArgs json.RawMessage
	d := &stubDialer{callFn: func(ctx context.Context, sv *MCPServer, toolName string, args json.RawMessage) (string, error) {
		gotTool, gotArgs = toolName, args
		return "文件内容", nil
	}}
	svc, _ := newSvc(&stubStore{}, &stubCache{}, d)

	resp, err := svc.RunTool(context.Background(), mcpapi.RunToolReq{
		ID: 1, ToolName: "read_file", Arguments: []byte(`{"path":"/tmp/a.txt"}`),
	})
	require.NoError(t, err)
	assert.Equal(t, "文件内容", resp.Content)
	assert.Equal(t, "read_file", gotTool)
	assert.JSONEq(t, `{"path":"/tmp/a.txt"}`, string(gotArgs), "参数原样透传")
}

func TestRunTool_ToolNotInServer(t *testing.T) {
	// 工具不在该 server 的 mcp_tools 行内（未 discover 或名字错）→ 404 哨兵，不触达上游。
	svc, _ := newSvc(&stubStore{}, &stubCache{}, noDialer())

	_, err := svc.RunTool(context.Background(), mcpapi.RunToolReq{ID: 1, ToolName: "no_such_tool"})
	assert.ErrorIs(t, err, mcpapi.ErrMCPToolNotFound)
}

func TestRunTool_CallFailed(t *testing.T) {
	d := &stubDialer{callFn: func(ctx context.Context, sv *MCPServer, toolName string, args json.RawMessage) (string, error) {
		return "", errors.New("tool \"read_file\" 执行失败: 权限不足")
	}}
	svc, _ := newSvc(&stubStore{}, &stubCache{}, d)

	_, err := svc.RunTool(context.Background(), mcpapi.RunToolReq{ID: 1, ToolName: "read_file"})
	assert.ErrorIs(t, err, mcpapi.ErrMCPToolCallFailed, "连接 / 调用失败 / isError 统一包装哨兵")
	assert.Contains(t, err.Error(), "权限不足")
}

func TestRunTool_ServerNotFound(t *testing.T) {
	st := &stubStore{getServerByID: func(ctx context.Context, id uint64) (*MCPServer, error) {
		return nil, gorm.ErrRecordNotFound
	}}
	svc, _ := newSvc(st, &stubCache{}, noDialer())

	_, err := svc.RunTool(context.Background(), mcpapi.RunToolReq{ID: 999, ToolName: "read_file"})
	assert.ErrorIs(t, err, mcpapi.ErrMCPServerNotFound)
}

// ---- MCPClientService（跨模块调用面） ----

func TestCallTool_CrossModule(t *testing.T) {
	var gotServer *MCPServer
	d := &stubDialer{callFn: func(ctx context.Context, sv *MCPServer, toolName string, args json.RawMessage) (string, error) {
		gotServer = sv
		return "ok", nil
	}}
	// 停用 server 照常调用：CallTool 不检查 enabled（调用方组装工具时自行排除，
	// server_mgmt_spec.md §3）。
	disabled := sampleServer(1)
	disabled.Enabled = false
	st := &stubStore{getServerByID: func(ctx context.Context, id uint64) (*MCPServer, error) {
		return disabled, nil
	}}
	_, clientSvc := newSvc(st, &stubCache{}, d)

	resp, err := clientSvc.CallTool(context.Background(), mcpapi.CallToolReq{
		MCPServerID: 1, ToolName: "read_file", Arguments: json.RawMessage(`{}`),
	})
	require.NoError(t, err)
	assert.Equal(t, "ok", resp.Content)
	assert.False(t, gotServer.Enabled)
}

func TestCallTool_CrossModuleNotFound(t *testing.T) {
	st := &stubStore{getServerByID: func(ctx context.Context, id uint64) (*MCPServer, error) {
		return nil, gorm.ErrRecordNotFound
	}}
	_, clientSvc := newSvc(st, &stubCache{}, noDialer())

	_, err := clientSvc.CallTool(context.Background(), mcpapi.CallToolReq{MCPServerID: 999, ToolName: "x"})
	assert.ErrorIs(t, err, mcpapi.ErrMCPServerNotFound)
}

func TestListTools_CrossModule(t *testing.T) {
	_, clientSvc := newSvc(&stubStore{}, &stubCache{}, noDialer())

	tools, err := clientSvc.ListTools(context.Background(), mcpapi.ListToolsReq{MCPServerID: 1})
	require.NoError(t, err)
	require.Len(t, tools, 2)
	assert.Equal(t, "read_file", tools[0].Name)
}

func TestListTools_CrossModuleNotFound(t *testing.T) {
	st := &stubStore{getServerByID: func(ctx context.Context, id uint64) (*MCPServer, error) {
		return nil, gorm.ErrRecordNotFound
	}}
	_, clientSvc := newSvc(st, &stubCache{}, noDialer())

	_, err := clientSvc.ListTools(context.Background(), mcpapi.ListToolsReq{MCPServerID: 999})
	assert.ErrorIs(t, err, mcpapi.ErrMCPServerNotFound)
}

func TestGetToolsByIDs(t *testing.T) {
	// 两条工具分属两个 server：server 1 启用、server 2 停用 → ToolCheck 标志逐工具判定。
	st := &stubStore{
		listToolsByIDsFunc: func(ctx context.Context, ids []uint64) ([]MCPTool, error) {
			tools := sampleTools(1)
			t2 := MCPTool{MCPServerID: 2, Name: "search", Description: "搜索"}
			t2.ID = 201
			return append(tools, t2), nil
		},
		listServersByIDs: func(ctx context.Context, ids []uint64) ([]MCPServer, error) {
			assert.Equal(t, []uint64{1, 2}, ids, "server id 去重后批量取")
			s1 := sampleServer(1)
			s2 := sampleServer(2)
			s2.Enabled = false
			return []MCPServer{*s1, *s2}, nil
		},
	}
	_, clientSvc := newSvc(st, &stubCache{}, noDialer())

	checks, err := clientSvc.GetToolsByIDs(context.Background(), mcpapi.GetToolsByIDsReq{IDs: []uint64{101, 102, 201}})
	require.NoError(t, err)
	require.Len(t, checks, 3)
	assert.True(t, checks[0].ServerEnabled)
	assert.True(t, checks[1].ServerEnabled)
	assert.False(t, checks[2].ServerEnabled, "停用 server 的工具 → false（agent 绑定校验拒）")
	assert.Equal(t, "101", checks[0].ID)
}

func TestGetToolsByIDs_Empty(t *testing.T) {
	st := &stubStore{}
	_, clientSvc := newSvc(st, &stubCache{}, noDialer())

	checks, err := clientSvc.GetToolsByIDs(context.Background(), mcpapi.GetToolsByIDsReq{})
	require.NoError(t, err)
	assert.Equal(t, []mcpapi.ToolCheck{}, checks, "空入参返空切片，不落库")
	assert.Equal(t, 0, st.calls.listToolsByIDs)
}

func TestGetToolsByIDs_ServerRowMissing(t *testing.T) {
	// server 行缺失（理论不可达，FK 兜底）→ ServerEnabled false，不报错。
	st := &stubStore{
		listToolsByIDsFunc: func(ctx context.Context, ids []uint64) ([]MCPTool, error) {
			return sampleTools(9)[:1], nil // 单条工具，聚焦 server 行缺失分支
		},
		listServersByIDs: func(ctx context.Context, ids []uint64) ([]MCPServer, error) {
			return nil, nil // server 9 不在
		},
	}
	_, clientSvc := newSvc(st, &stubCache{}, noDialer())

	checks, err := clientSvc.GetToolsByIDs(context.Background(), mcpapi.GetToolsByIDsReq{IDs: []uint64{101}})
	require.NoError(t, err)
	require.Len(t, checks, 1)
	assert.False(t, checks[0].ServerEnabled)
}
