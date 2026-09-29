package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcpapi "github.com/Karlsk/go-hify/internal/mcp/api"
	"github.com/Karlsk/go-hify/internal/platform/errs"
)

// handler 测试：httptest 走绑定函数，验证状态码 + respond 信封结构 + 哨兵映射
//（同 provider / agent 模式）。fakeSvc 只复现哨兵路径与参数查收，不复现业务规则。

type fakeSvc struct {
	servers  map[uint64]mcpapi.MCPServerDetailSchema
	nextID   uint64
	injected error // 非 nil 时所有方法返回它（哨兵 / 500 注入）

	// 查收请求是否正确落到 req（含两段绑定的路径 id）
	gotCreateReq mcpapi.CreateMCPServerReq
	gotUpdateID  uint64
	gotRunTool   mcpapi.RunToolReq
	gotDiscover  uint64
}

func newFakeSvc() *fakeSvc {
	return &fakeSvc{servers: map[uint64]mcpapi.MCPServerDetailSchema{}, nextID: 1}
}

func (f *fakeSvc) Create(_ context.Context, req mcpapi.CreateMCPServerReq) (*mcpapi.MCPServerSchema, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	f.gotCreateReq = req
	id := f.nextID
	f.nextID++
	s := mcpapi.MCPServerSchema{Name: req.Name, Transport: req.Transport, Command: req.Command, URL: req.URL, Enabled: true}
	s.ID = strconv.FormatUint(id, 10)
	f.servers[id] = mcpapi.MCPServerDetailSchema{MCPServerSchema: s, Env: req.Env, Tools: []mcpapi.ToolSchema{}}
	return &s, nil
}

func (f *fakeSvc) Get(_ context.Context, req mcpapi.GetMCPServerReq) (*mcpapi.MCPServerDetailSchema, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	d, ok := f.servers[req.ID]
	if !ok {
		return nil, mcpapi.ErrMCPServerNotFound
	}
	return &d, nil
}

func (f *fakeSvc) List(_ context.Context, req mcpapi.ListMCPServersReq) (*mcpapi.MCPServerListResult, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	ids := make([]uint64, 0, len(f.servers))
	for id := range f.servers {
		ids = append(ids, id)
	}
	items := make([]mcpapi.MCPServerListItem, 0, len(ids))
	for _, id := range ids {
		items = append(items, mcpapi.MCPServerListItem{MCPServerSchema: f.servers[id].MCPServerSchema})
	}
	return &mcpapi.MCPServerListResult{Items: items, Page: req.Page, PageSize: req.PageSize, Total: int64(len(items))}, nil
}

func (f *fakeSvc) Update(_ context.Context, req mcpapi.UpdateMCPServerReq) (*mcpapi.MCPServerSchema, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	f.gotUpdateID = req.ID
	d, ok := f.servers[req.ID]
	if !ok {
		return nil, mcpapi.ErrMCPServerNotFound
	}
	d.Name = req.Name
	f.servers[req.ID] = d
	s := d.MCPServerSchema
	return &s, nil
}

func (f *fakeSvc) Delete(_ context.Context, req mcpapi.DeleteMCPServerReq) error {
	if f.injected != nil {
		return f.injected
	}
	if _, ok := f.servers[req.ID]; !ok {
		return mcpapi.ErrMCPServerNotFound
	}
	delete(f.servers, req.ID)
	return nil
}

// Discover 复现「动作完成即 200」语义：成功 / 失败都在 DiscoverResultSchema 里。
func (f *fakeSvc) Discover(_ context.Context, req mcpapi.DiscoverReq) (*mcpapi.DiscoverResultSchema, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	f.gotDiscover = req.ID
	if _, ok := f.servers[req.ID]; !ok {
		return nil, mcpapi.ErrMCPServerNotFound
	}
	return &mcpapi.DiscoverResultSchema{Success: true, LatencyMs: 42, ToolCount: 3}, nil
}

func (f *fakeSvc) RunTool(_ context.Context, req mcpapi.RunToolReq) (*mcpapi.CallToolResult, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	f.gotRunTool = req
	if _, ok := f.servers[req.ID]; !ok {
		return nil, mcpapi.ErrMCPServerNotFound
	}
	return &mcpapi.CallToolResult{Content: "文件内容"}, nil
}

func newTestRouter(svc *fakeSvc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(svc).RegisterRoutes(r.Group("/api/v1"))
	return r
}

func doReq(t *testing.T, r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// envelope 信封骨架；Data 延迟解析（载荷形态随端点不同）。
type envelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Meta *struct {
		Page     int   `json:"page"`
		PageSize int   `json:"page_size"`
		Total    int64 `json:"total"`
	} `json:"meta"`
}

func parseEnvelope(t *testing.T, body []byte) envelope {
	t.Helper()
	var e envelope
	require.NoError(t, json.Unmarshal(body, &e), "body: %s", body)
	return e
}

func decodeData[T any](t *testing.T, e envelope) T {
	t.Helper()
	var d T
	require.NoError(t, json.Unmarshal(e.Data, &d))
	return d
}

// ---- create ----

func TestCreate(t *testing.T) {
	svc := newFakeSvc()
	r := newTestRouter(svc)
	w := doReq(t, r, http.MethodPost, "/api/v1/mcp-servers",
		`{"name":"filesystem","transport":"stdio","command":"npx -y fs-server","env":{"ROOT":"/data"}}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	e := parseEnvelope(t, w.Body.Bytes())
	require.True(t, e.Success)
	d := decodeData[mcpapi.MCPServerSchema](t, e)
	assert.Equal(t, "filesystem", d.Name)
	assert.Equal(t, "stdio", d.Transport)
	assert.Equal(t, "npx -y fs-server", svc.gotCreateReq.Command)
	assert.Equal(t, map[string]string{"ROOT": "/data"}, svc.gotCreateReq.Env)
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
}

func TestCreate_ValidationFailed(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"缺 name（binding required）", `{"transport":"stdio","command":"x"}`},
		{"transport 非法（binding oneof）", `{"name":"x","transport":"grpc"}`},
		{"stdio 缺 command（跨字段 Validate）", `{"name":"x","transport":"stdio"}`},
		{"sse 缺 url（跨字段 Validate）", `{"name":"x","transport":"sse"}`},
		{"坏 JSON", `{`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRouter(newFakeSvc())
			w := doReq(t, r, http.MethodPost, "/api/v1/mcp-servers", tc.body)
			assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			e := parseEnvelope(t, w.Body.Bytes())
			assert.False(t, e.Success)
			require.NotNil(t, e.Error)
			assert.Equal(t, errs.ErrValidationFailed.Error(), e.Error.Code)
		})
	}
}

func TestCreate_Sentinels(t *testing.T) {
	cases := []struct {
		name     string
		injected error
		wantCode string
		wantHTTP int
	}{
		{"重名冲突", mcpapi.ErrMCPServerNameConflict, mcpapi.ErrMCPServerNameConflict.Error(), http.StatusConflict},
		{"内部错误", errs.ErrInternal, errs.ErrInternal.Error(), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newFakeSvc()
			svc.injected = tc.injected
			r := newTestRouter(svc)
			w := doReq(t, r, http.MethodPost, "/api/v1/mcp-servers", `{"name":"x","transport":"sse","url":"http://x/sse"}`)
			assert.Equal(t, tc.wantHTTP, w.Code, w.Body.String())
			e := parseEnvelope(t, w.Body.Bytes())
			require.NotNil(t, e.Error)
			assert.Equal(t, tc.wantCode, e.Error.Code)
		})
	}
}

// ---- list ----

func TestList(t *testing.T) {
	svc := newFakeSvc()
	svc.servers[1] = mcpapi.MCPServerDetailSchema{MCPServerSchema: mcpapi.MCPServerSchema{Name: "a"}}
	svc.servers[2] = mcpapi.MCPServerDetailSchema{MCPServerSchema: mcpapi.MCPServerSchema{Name: "b"}}
	r := newTestRouter(svc)

	w := doReq(t, r, http.MethodGet, "/api/v1/mcp-servers?page=1&page_size=10", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	e := parseEnvelope(t, w.Body.Bytes())
	require.True(t, e.Success)
	d := decodeData[[]mcpapi.MCPServerListItem](t, e)
	assert.Len(t, d, 2)
	require.NotNil(t, e.Meta)
	assert.Equal(t, 1, e.Meta.Page)
	assert.Equal(t, 10, e.Meta.PageSize)
	assert.Equal(t, int64(2), e.Meta.Total)
}

// ---- get ----

func TestGet_DetailWithEnvAndTools(t *testing.T) {
	svc := newFakeSvc()
	svc.servers[1] = mcpapi.MCPServerDetailSchema{
		MCPServerSchema: mcpapi.MCPServerSchema{Name: "filesystem"},
		Env:             map[string]string{"ROOT": "/data"},
		Tools:           []mcpapi.ToolSchema{{ID: "101", MCPServerID: "1", Name: "read_file"}},
	}
	r := newTestRouter(svc)

	w := doReq(t, r, http.MethodGet, "/api/v1/mcp-servers/1", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	d := decodeData[mcpapi.MCPServerDetailSchema](t, parseEnvelope(t, w.Body.Bytes()))
	assert.Equal(t, "filesystem", d.Name)
	assert.Equal(t, map[string]string{"ROOT": "/data"}, d.Env)
	require.Len(t, d.Tools, 1)
	assert.Equal(t, "read_file", d.Tools[0].Name)
}

func TestGet_NotFound(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	w := doReq(t, r, http.MethodGet, "/api/v1/mcp-servers/999", "")
	assert.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
	e := parseEnvelope(t, w.Body.Bytes())
	require.NotNil(t, e.Error)
	assert.Equal(t, mcpapi.ErrMCPServerNotFound.Error(), e.Error.Code)
}

func TestGet_BadID(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	w := doReq(t, r, http.MethodGet, "/api/v1/mcp-servers/abc", "")
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
}

// ---- update ----

func TestUpdate_IDFromPathWinsOverBody(t *testing.T) {
	svc := newFakeSvc()
	svc.servers[1] = mcpapi.MCPServerDetailSchema{MCPServerSchema: mcpapi.MCPServerSchema{Name: "旧名"}}
	r := newTestRouter(svc)

	// body 恶意带 id=999：json:"-" 挡住覆盖，路径 :id=1 生效（provider 踩坑 #7 的回归钉）。
	w := doReq(t, r, http.MethodPut, "/api/v1/mcp-servers/1",
		`{"id":999,"name":"新名","transport":"sse","url":"http://x/sse"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, uint64(1), svc.gotUpdateID)
	d := decodeData[mcpapi.MCPServerSchema](t, parseEnvelope(t, w.Body.Bytes()))
	assert.Equal(t, "新名", d.Name)
}

func TestUpdate_NotFound(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	w := doReq(t, r, http.MethodPut, "/api/v1/mcp-servers/999", `{"name":"x","transport":"sse","url":"http://x/sse"}`)
	assert.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
}

// ---- delete ----

func TestDelete_NoContent(t *testing.T) {
	svc := newFakeSvc()
	svc.servers[1] = mcpapi.MCPServerDetailSchema{}
	r := newTestRouter(svc)

	w := doReq(t, r, http.MethodDelete, "/api/v1/mcp-servers/1", "")
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Empty(t, w.Body.String(), "204 无返回体")
}

func TestDelete_Sentinels(t *testing.T) {
	cases := []struct {
		name     string
		injected error
		wantCode string
		wantHTTP int
	}{
		{"不存在", mcpapi.ErrMCPServerNotFound, mcpapi.ErrMCPServerNotFound.Error(), http.StatusNotFound},
		{"有 Agent 绑定挡删", mcpapi.ErrMCPServerInUse, mcpapi.ErrMCPServerInUse.Error(), http.StatusConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newFakeSvc()
			svc.injected = tc.injected
			r := newTestRouter(svc)
			w := doReq(t, r, http.MethodDelete, "/api/v1/mcp-servers/1", "")
			assert.Equal(t, tc.wantHTTP, w.Code, w.Body.String())
			e := parseEnvelope(t, w.Body.Bytes())
			require.NotNil(t, e.Error)
			assert.Equal(t, tc.wantCode, e.Error.Code)
		})
	}
}

// ---- discover ----

func TestDiscover(t *testing.T) {
	svc := newFakeSvc()
	svc.servers[1] = mcpapi.MCPServerDetailSchema{}
	r := newTestRouter(svc)

	w := doReq(t, r, http.MethodPost, "/api/v1/mcp-servers/1/discover", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	d := decodeData[mcpapi.DiscoverResultSchema](t, parseEnvelope(t, w.Body.Bytes()))
	assert.True(t, d.Success)
	assert.Equal(t, int64(3), d.ToolCount)
	assert.Equal(t, uint64(1), svc.gotDiscover)
}

func TestDiscover_NotFound(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	w := doReq(t, r, http.MethodPost, "/api/v1/mcp-servers/999/discover", "")
	assert.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
}

// ---- run ----

func TestRunTool(t *testing.T) {
	svc := newFakeSvc()
	svc.servers[1] = mcpapi.MCPServerDetailSchema{}
	r := newTestRouter(svc)

	w := doReq(t, r, http.MethodPost, "/api/v1/mcp-servers/1/run",
		`{"tool_name":"read_file","arguments":{"path":"/tmp/a.txt"}}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	d := decodeData[mcpapi.CallToolResult](t, parseEnvelope(t, w.Body.Bytes()))
	assert.Equal(t, "文件内容", d.Content)
	assert.Equal(t, uint64(1), svc.gotRunTool.ID, "路径 :id 两段绑定进 req")
	assert.Equal(t, "read_file", svc.gotRunTool.ToolName)
	assert.JSONEq(t, `{"path":"/tmp/a.txt"}`, string(svc.gotRunTool.Arguments))
}

func TestRunTool_ValidationFailed(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"缺 tool_name", `{}`},
		{"arguments 是数组（须对象）", `{"tool_name":"x","arguments":[1,2]}`},
		{"arguments 非 JSON", `{"tool_name":"x","arguments":not-json}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRouter(newFakeSvc())
			w := doReq(t, r, http.MethodPost, "/api/v1/mcp-servers/1/run", tc.body)
			assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		})
	}
}

func TestRunTool_Sentinels(t *testing.T) {
	cases := []struct {
		name     string
		injected error
		wantCode string
		wantHTTP int
	}{
		{"server 不存在", mcpapi.ErrMCPServerNotFound, mcpapi.ErrMCPServerNotFound.Error(), http.StatusNotFound},
		{"工具不在清单", mcpapi.ErrMCPToolNotFound, mcpapi.ErrMCPToolNotFound.Error(), http.StatusNotFound},
		{"调用失败", mcpapi.ErrMCPToolCallFailed, mcpapi.ErrMCPToolCallFailed.Error(), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newFakeSvc()
			svc.injected = tc.injected
			r := newTestRouter(svc)
			w := doReq(t, r, http.MethodPost, "/api/v1/mcp-servers/1/run", `{"tool_name":"x"}`)
			assert.Equal(t, tc.wantHTTP, w.Code, w.Body.String())
			e := parseEnvelope(t, w.Body.Bytes())
			require.NotNil(t, e.Error)
			assert.Equal(t, tc.wantCode, e.Error.Code)
		})
	}
}
