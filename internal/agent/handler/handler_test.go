package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentapi "github.com/Karlsk/go-hify/internal/agent/api"
	"github.com/Karlsk/go-hify/internal/platform/errs"
	providerapi "github.com/Karlsk/go-hify/internal/provider/api"
)

// handler 测试：httptest 走绑定函数，验证状态码 + respond 信封结构 + 哨兵映射
//（同 provider 模式）。fakeSvc 只复现哨兵路径与参数查收，不复现业务规则。

type fakeSvc struct {
	agents   map[uint64]agentapi.AgentDetailSchema
	nextID   uint64
	injected error // 非 nil 时所有方法返回它（哨兵 / 500 注入）

	// 查收两段绑定 / body 内嵌绑定是否正确落到 req
	gotCreateToolIDs []uint64
	gotUpdateID      uint64
	gotUpdateToolIDs []uint64
}

func newFakeSvc() *fakeSvc {
	return &fakeSvc{agents: map[uint64]agentapi.AgentDetailSchema{}, nextID: 1}
}

func (f *fakeSvc) Create(_ context.Context, req agentapi.CreateAgentReq) (*agentapi.AgentSchema, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	f.gotCreateToolIDs = req.ToolIDs
	id := f.nextID
	f.nextID++
	s := agentapi.AgentSchema{Name: req.Name, ModelID: strconv.FormatUint(req.ModelID, 10)}
	s.ID = strconv.FormatUint(id, 10)
	f.agents[id] = agentapi.AgentDetailSchema{AgentSchema: s, ToolIDs: strIDs(req.ToolIDs)}
	return &s, nil
}

func (f *fakeSvc) Get(_ context.Context, req agentapi.GetAgentReq) (*agentapi.AgentDetailSchema, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	d, ok := f.agents[req.ID]
	if !ok {
		return nil, agentapi.ErrAgentNotFound
	}
	return &d, nil
}

func (f *fakeSvc) List(_ context.Context, req agentapi.ListAgentsReq) (*agentapi.AgentListResult, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	ids := make([]uint64, 0, len(f.agents))
	for id := range f.agents {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	items := make([]agentapi.AgentListItem, 0, len(ids))
	for _, id := range ids {
		s := f.agents[id].AgentSchema
		items = append(items, agentapi.AgentListItem{AgentSchema: s})
	}
	return &agentapi.AgentListResult{Items: items, Page: req.Page, PageSize: req.PageSize, Total: int64(len(items))}, nil
}

func (f *fakeSvc) Update(_ context.Context, req agentapi.UpdateAgentReq) (*agentapi.AgentSchema, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	f.gotUpdateID = req.ID
	f.gotUpdateToolIDs = req.ToolIDs
	d, ok := f.agents[req.ID]
	if !ok {
		return nil, agentapi.ErrAgentNotFound
	}
	d.Name = req.Name
	d.ToolIDs = strIDs(req.ToolIDs)
	f.agents[req.ID] = d
	s := d.AgentSchema
	return &s, nil
}

func (f *fakeSvc) Delete(_ context.Context, req agentapi.DeleteAgentReq) error {
	if f.injected != nil {
		return f.injected
	}
	if _, ok := f.agents[req.ID]; !ok {
		return agentapi.ErrAgentNotFound
	}
	delete(f.agents, req.ID)
	return nil
}

func strIDs(ids []uint64) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, strconv.FormatUint(id, 10))
	}
	return out
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
	w := doReq(t, r, http.MethodPost, "/api/v1/agents",
		`{"name":"客服助手","model_id":5,"tool_ids":[10,12]}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	e := parseEnvelope(t, w.Body.Bytes())
	require.True(t, e.Success)
	d := decodeData[agentapi.AgentSchema](t, e)
	assert.Equal(t, "客服助手", d.Name)
	assert.Equal(t, "5", d.ModelID)
	assert.Equal(t, []uint64{10, 12}, svc.gotCreateToolIDs, "body 内嵌 tool_ids 落到 req")
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
}

func TestCreate_ValidationFailed(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"缺 name（binding required）", `{"model_id":5}`},
		{"缺 model_id（binding required）", `{"name":"x"}`},
		{"temperature 越界（binding lte）", `{"name":"x","model_id":5,"temperature":3}`},
		{"tool_ids 重复（跨字段 Validate）", `{"name":"x","model_id":5,"tool_ids":[10,10]}`},
		{"备用模型等于主模型（跨字段 Validate）", `{"name":"x","model_id":5,"fallback_model_id":5}`},
		{"workflow_id 传 0（binding gt=0，spec 05）", `{"name":"x","model_id":5,"workflow_id":0}`},
		{"坏 JSON", `{`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRouter(newFakeSvc())
			w := doReq(t, r, http.MethodPost, "/api/v1/agents", tc.body)
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
		{"模型不存在（provider 哨兵透传）", providerapi.ErrModelNotFound, providerapi.ErrModelNotFound.Error(), http.StatusNotFound},
		{"工具不存在（FK 翻译）", agentapi.ErrToolNotFound, agentapi.ErrToolNotFound.Error(), http.StatusNotFound},
		{"知识库不存在（FK 翻译）", agentapi.ErrKnowledgeBaseNotFound, agentapi.ErrKnowledgeBaseNotFound.Error(), http.StatusNotFound},
		{"工作流不存在（FK 23503 约束名分发翻译，spec 05）", agentapi.ErrWorkflowNotFound, agentapi.ErrWorkflowNotFound.Error(), http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newFakeSvc()
			svc.injected = tc.injected
			r := newTestRouter(svc)
			w := doReq(t, r, http.MethodPost, "/api/v1/agents", `{"name":"x","model_id":5}`)
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
	svc.agents[1] = agentapi.AgentDetailSchema{AgentSchema: agentapi.AgentSchema{Name: "a"}}
	svc.agents[2] = agentapi.AgentDetailSchema{AgentSchema: agentapi.AgentSchema{Name: "b"}}
	r := newTestRouter(svc)

	w := doReq(t, r, http.MethodGet, "/api/v1/agents?page=1&page_size=10", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	e := parseEnvelope(t, w.Body.Bytes())
	require.True(t, e.Success)
	d := decodeData[[]agentapi.AgentSchema](t, e)
	assert.Len(t, d, 2)
	require.NotNil(t, e.Meta)
	assert.Equal(t, 1, e.Meta.Page)
	assert.Equal(t, 10, e.Meta.PageSize)
	assert.Equal(t, int64(2), e.Meta.Total)
}

func TestList_BadPageSize(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	w := doReq(t, r, http.MethodGet, "/api/v1/agents?page_size=999", "")
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
}

// ---- get ----

func TestGet_DetailWithToolIDs(t *testing.T) {
	svc := newFakeSvc()
	svc.agents[1] = agentapi.AgentDetailSchema{
		AgentSchema: agentapi.AgentSchema{Name: "客服助手", ModelID: "5"},
		ToolIDs:     []string{"10", "12"},
	}
	r := newTestRouter(svc)

	w := doReq(t, r, http.MethodGet, "/api/v1/agents/1", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	d := decodeData[agentapi.AgentDetailSchema](t, parseEnvelope(t, w.Body.Bytes()))
	assert.Equal(t, "客服助手", d.Name)
	assert.Equal(t, []string{"10", "12"}, d.ToolIDs)
}

func TestGet_NotFound(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	w := doReq(t, r, http.MethodGet, "/api/v1/agents/999", "")
	assert.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
	e := parseEnvelope(t, w.Body.Bytes())
	require.NotNil(t, e.Error)
	assert.Equal(t, agentapi.ErrAgentNotFound.Error(), e.Error.Code)
}

func TestGet_BadID(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	w := doReq(t, r, http.MethodGet, "/api/v1/agents/abc", "")
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
}

// ---- update ----

func TestUpdate_IDFromPathWinsOverBody(t *testing.T) {
	svc := newFakeSvc()
	svc.agents[1] = agentapi.AgentDetailSchema{AgentSchema: agentapi.AgentSchema{Name: "旧名"}}
	r := newTestRouter(svc)

	// body 恶意带 id=999：json:"-" 挡住覆盖，路径 :id=1 生效（provider 踩坑 #7 的回归钉）。
	w := doReq(t, r, http.MethodPut, "/api/v1/agents/1",
		`{"id":999,"name":"新名","model_id":5,"tool_ids":[10]}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, uint64(1), svc.gotUpdateID)
	assert.Equal(t, []uint64{10}, svc.gotUpdateToolIDs)
	d := decodeData[agentapi.AgentSchema](t, parseEnvelope(t, w.Body.Bytes()))
	assert.Equal(t, "新名", d.Name)
}

func TestUpdate_ValidationFailed(t *testing.T) {
	// UpdateAgentReq 与 Create 同款 binding（impl spec 05 §4.2 两处同款）：
	// workflow_id 传 0 被 gt=0 拒绝 → 400。
	r := newTestRouter(newFakeSvc())
	w := doReq(t, r, http.MethodPut, "/api/v1/agents/1",
		`{"name":"x","model_id":5,"workflow_id":0}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	e := parseEnvelope(t, w.Body.Bytes())
	assert.False(t, e.Success)
	require.NotNil(t, e.Error)
	assert.Equal(t, errs.ErrValidationFailed.Error(), e.Error.Code)
}

func TestUpdate_NotFound(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	w := doReq(t, r, http.MethodPut, "/api/v1/agents/999", `{"name":"x","model_id":5}`)
	assert.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
}

// ---- delete ----

func TestDelete_NoContent(t *testing.T) {
	svc := newFakeSvc()
	svc.agents[1] = agentapi.AgentDetailSchema{}
	r := newTestRouter(svc)

	w := doReq(t, r, http.MethodDelete, "/api/v1/agents/1", "")
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Empty(t, w.Body.String(), "204 无返回体")
}

func TestDelete_NotFound(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	w := doReq(t, r, http.MethodDelete, "/api/v1/agents/999", "")
	assert.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
}
