package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Karlsk/go-hify/internal/platform/errs"
	"github.com/Karlsk/go-hify/internal/platform/schema"
	providerapi "github.com/Karlsk/go-hify/internal/provider/api"
)

// handler 测试：httptest 走绑定函数，验证状态码 + respond 信封结构 + 哨兵映射
//（同 demo 模式）。fakeSvc 只复现哨兵路径与参数查收，不复现业务规则。

// 双接口方法名重叠（Create/Get/... 签名不同），一个 struct 无法同时实现——
// 共享状态 + 两个包装类型：fakeProviderSvc / fakeModelSvc 各自实现一边。
type fakeSvc struct {
	providers map[uint64]providerapi.ProviderSchema
	detail    map[uint64]providerapi.ProviderDetailSchema // Get 直接返回（含聚合）
	models    map[uint64]providerapi.ModelSchema
	nextP     uint64
	nextM     uint64
	injected  error // 非 nil 时所有方法返回它（哨兵 / 500 注入）

	// 查收两段绑定 / 嵌套路由传参是否正确落到 req
	gotUpdateProviderID uint64
	gotUpdateModelID    uint64
	gotListModelsPID    uint64
}

func newFakeSvc() *fakeSvc {
	return &fakeSvc{
		providers: map[uint64]providerapi.ProviderSchema{},
		detail:    map[uint64]providerapi.ProviderDetailSchema{},
		models:    map[uint64]providerapi.ModelSchema{},
		nextP:     1,
		nextM:     1,
	}
}

type fakeProviderSvc struct{ *fakeSvc }

func (f fakeProviderSvc) Create(_ context.Context, req providerapi.CreateProviderReq) (*providerapi.ProviderSchema, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	s := providerapi.ProviderSchema{Name: req.Name, Kind: req.Kind, Enabled: true}
	s.ID = strconv.FormatUint(f.nextP, 10)
	f.providers[f.nextP] = s
	f.nextP++
	return &s, nil
}

func (f fakeProviderSvc) Get(_ context.Context, req providerapi.GetProviderReq) (*providerapi.ProviderDetailSchema, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	d, ok := f.detail[req.ID]
	if !ok {
		return nil, providerapi.ErrProviderNotFound
	}
	return &d, nil
}

func (f fakeProviderSvc) List(_ context.Context, req providerapi.ListProvidersReq) (*providerapi.ProviderListResult, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	items := make([]providerapi.ProviderSchema, 0, len(f.providers))
	for _, p := range f.providers {
		items = append(items, p)
	}
	page, size := req.Page, req.PageSize
	if page < 1 {
		page = 1
	}
	if size <= 0 {
		size = 20
	}
	return &providerapi.ProviderListResult{Items: items, Page: page, PageSize: size, Total: int64(len(items))}, nil
}

func (f fakeProviderSvc) Update(_ context.Context, req providerapi.UpdateProviderReq) (*providerapi.ProviderSchema, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	f.gotUpdateProviderID = req.ID
	p, ok := f.providers[req.ID]
	if !ok {
		return nil, providerapi.ErrProviderNotFound
	}
	p.Name = req.Name
	p.Enabled = req.Enabled
	f.providers[req.ID] = p
	return &p, nil
}

func (f fakeProviderSvc) Delete(_ context.Context, req providerapi.DeleteProviderReq) error {
	if f.injected != nil {
		return f.injected
	}
	if _, ok := f.providers[req.ID]; !ok {
		return providerapi.ErrProviderNotFound
	}
	delete(f.providers, req.ID)
	delete(f.detail, req.ID)
	return nil
}

// TestConnection 固定返回失败结果：验证「探测失败是业务结果、HTTP 仍 200」的语义。
func (f fakeProviderSvc) TestConnection(_ context.Context, _ providerapi.TestConnectionReq) (*providerapi.ConnectionTestSchema, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	return &providerapi.ConnectionTestSchema{Success: false, LatencyMs: 87, ErrorMessage: "鉴权失败（HTTP 401）：invalid api key"}, nil
}

type fakeModelSvc struct{ *fakeSvc }

func (f fakeModelSvc) Create(_ context.Context, req providerapi.CreateModelReq) (*providerapi.ModelSchema, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	s := providerapi.ModelSchema{ProviderID: strconv.FormatUint(req.ProviderID, 10), Name: req.Name, ModelID: req.ModelID, Capability: req.Capability}
	s.ID = strconv.FormatUint(f.nextM, 10)
	f.models[f.nextM] = s
	f.nextM++
	return &s, nil
}

func (f fakeModelSvc) Get(_ context.Context, req providerapi.GetModelReq) (*providerapi.ModelSchema, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	s, ok := f.models[req.ID]
	if !ok {
		return nil, providerapi.ErrModelNotFound
	}
	return &s, nil
}

func (f fakeModelSvc) List(_ context.Context, req providerapi.ListModelsReq) (*providerapi.ModelListResult, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	f.gotListModelsPID = req.ProviderID
	items := make([]providerapi.ModelSchema, 0, len(f.models))
	for _, m := range f.models {
		items = append(items, m)
	}
	page, size := req.Page, req.PageSize
	if page < 1 {
		page = 1
	}
	if size <= 0 {
		size = 20
	}
	return &providerapi.ModelListResult{Items: items, Page: page, PageSize: size, Total: int64(len(items))}, nil
}

func (f fakeModelSvc) Update(_ context.Context, req providerapi.UpdateModelReq) (*providerapi.ModelSchema, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	f.gotUpdateModelID = req.ID
	s, ok := f.models[req.ID]
	if !ok {
		return nil, providerapi.ErrModelNotFound
	}
	s.Name = req.Name
	f.models[req.ID] = s
	return &s, nil
}

func (f fakeModelSvc) Delete(_ context.Context, req providerapi.DeleteModelReq) error {
	if f.injected != nil {
		return f.injected
	}
	if _, ok := f.models[req.ID]; !ok {
		return providerapi.ErrModelNotFound
	}
	delete(f.models, req.ID)
	return nil
}

// SyncModels 镜像真实 service 行为：返回 {added, updated} 计数。
func (f fakeModelSvc) SyncModels(_ context.Context, _ providerapi.SyncModelsReq) (*providerapi.ModelSyncResultSchema, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	return &providerapi.ModelSyncResultSchema{Added: 2, Updated: 1}, nil
}

// 编译期钉住：包装类型各自满足接口。
var (
	_ providerapi.ProviderService = fakeProviderSvc{}
	_ providerapi.ModelService    = fakeModelSvc{}
)

// ---- 测试基建 ----

func newTestRouter(svc *fakeSvc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(fakeProviderSvc{svc}, fakeModelSvc{svc}).RegisterRoutes(r.Group("/api/v1"))
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

// ---- provider 本体 ----

func TestCreateProvider(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	w := doReq(t, r, http.MethodPost, "/api/v1/providers",
		`{"name":"OpenAI","kind":"openai","api_key":"sk-x"}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	e := parseEnvelope(t, w.Body.Bytes())
	require.True(t, e.Success)
	d := decodeData[providerapi.ProviderSchema](t, e)
	assert.Equal(t, "OpenAI", d.Name)
	assert.Equal(t, providerapi.KindOpenAI, d.Kind)
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
}

func TestCreateProvider_ValidationFailed(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"坏 kind（binding oneof）", `{"name":"x","kind":"azure"}`},
		{"缺 name（binding required）", `{"kind":"openai"}`},
		{"claude 缺 api_key（跨字段 Validate）", `{"name":"x","kind":"claude"}`},
		{"compatible 缺 base_url（跨字段 Validate）", `{"name":"x","kind":"openai_compatible"}`},
		{"坏 JSON", `{`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRouter(newFakeSvc())
			w := doReq(t, r, http.MethodPost, "/api/v1/providers", tc.body)
			assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			e := parseEnvelope(t, w.Body.Bytes())
			assert.False(t, e.Success)
			require.NotNil(t, e.Error)
			assert.Equal(t, errs.ErrValidationFailed.Error(), e.Error.Code)
		})
	}
}

func TestCreateProvider_NameConflict(t *testing.T) {
	svc := newFakeSvc()
	svc.injected = providerapi.ErrProviderNameConflict
	r := newTestRouter(svc)
	w := doReq(t, r, http.MethodPost, "/api/v1/providers", `{"name":"dup","kind":"ollama"}`)
	assert.Equal(t, http.StatusConflict, w.Code)
	e := parseEnvelope(t, w.Body.Bytes())
	require.NotNil(t, e.Error)
	assert.Equal(t, "PROVIDER_NAME_CONFLICT", e.Error.Code)
}

func TestListProviders(t *testing.T) {
	svc := newFakeSvc()
	svc.providers[1] = providerapi.ProviderSchema{BaseSchema: schema.BaseSchema{ID: "1"}, Name: "OpenAI"}
	svc.providers[2] = providerapi.ProviderSchema{BaseSchema: schema.BaseSchema{ID: "2"}, Name: "Claude"}
	r := newTestRouter(svc)

	w := doReq(t, r, http.MethodGet, "/api/v1/providers?page=1&page_size=20", "")
	require.Equal(t, http.StatusOK, w.Code)
	e := parseEnvelope(t, w.Body.Bytes())
	require.True(t, e.Success)
	require.NotNil(t, e.Meta)
	assert.Equal(t, 1, e.Meta.Page)
	assert.Equal(t, 20, e.Meta.PageSize)
	assert.EqualValues(t, 2, e.Meta.Total)
	items := decodeData[[]providerapi.ProviderSchema](t, e)
	assert.Len(t, items, 2)
}

func TestListProviders_EmptyArray(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	w := doReq(t, r, http.MethodGet, "/api/v1/providers", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"data":[]`, "空列表 data 必须是 [] 非 null")
}

func TestListProviders_BadPageSize(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	w := doReq(t, r, http.MethodGet, "/api/v1/providers?page_size=101", "")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetProvider_DetailAggregates(t *testing.T) {
	svc := newFakeSvc()
	svc.detail[1] = providerapi.ProviderDetailSchema{
		ProviderSchema: providerapi.ProviderSchema{BaseSchema: schema.BaseSchema{ID: "1"}, Name: "OpenAI", Kind: providerapi.KindOpenAI},
		Models:         []providerapi.ModelSchema{{BaseSchema: schema.BaseSchema{ID: "1"}, ModelID: "gpt-4o"}},
		Health:         &providerapi.ProviderHealthSchema{Status: providerapi.HealthUp},
	}
	r := newTestRouter(svc)

	w := doReq(t, r, http.MethodGet, "/api/v1/providers/1", "")
	require.Equal(t, http.StatusOK, w.Code)
	e := parseEnvelope(t, w.Body.Bytes())
	require.True(t, e.Success)
	d := decodeData[providerapi.ProviderDetailSchema](t, e)
	assert.Equal(t, "OpenAI", d.Name)
	assert.Len(t, d.Models, 1)
	assert.Equal(t, "gpt-4o", d.Models[0].ModelID)
	require.NotNil(t, d.Health)
	assert.Equal(t, providerapi.HealthUp, d.Health.Status)
}

func TestGetProvider_NotFound(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	w := doReq(t, r, http.MethodGet, "/api/v1/providers/999", "")
	assert.Equal(t, http.StatusNotFound, w.Code)
	e := parseEnvelope(t, w.Body.Bytes())
	require.NotNil(t, e.Error)
	assert.Equal(t, "PROVIDER_NOT_FOUND", e.Error.Code)
}

func TestGetProvider_BadID(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	w := doReq(t, r, http.MethodGet, "/api/v1/providers/abc", "")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateProvider_IDFromPath(t *testing.T) {
	svc := newFakeSvc()
	svc.providers[1] = providerapi.ProviderSchema{BaseSchema: schema.BaseSchema{ID: "1"}, Name: "old"}
	r := newTestRouter(svc)

	// body 携带 id:999 试图覆盖路径值——ID 带 json:"-"，路径必须保持权威。
	w := doReq(t, r, http.MethodPut, "/api/v1/providers/1", `{"id":999,"name":"new","enabled":true}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	e := parseEnvelope(t, w.Body.Bytes())
	require.True(t, e.Success)
	d := decodeData[providerapi.ProviderSchema](t, e)
	assert.Equal(t, "new", d.Name)
	assert.Equal(t, uint64(1), svc.gotUpdateProviderID, "两段绑定：uri id 应进入 req.ID 且不被 body 覆盖")
}

func TestUpdateProvider_NotFound(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	w := doReq(t, r, http.MethodPut, "/api/v1/providers/999", `{"name":"n"}`)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// service 层 ValidateWithKind 包装后的 ErrValidationFailed（handler 预校验不到的 kind 规则）
// 应经 FailFromSentinel 映射 400，而非落 500。
func TestUpdateProvider_KindValidationFromService(t *testing.T) {
	svc := newFakeSvc()
	svc.injected = fmt.Errorf("%w: kind openai_compatible 必须提供 base_url", errs.ErrValidationFailed)
	r := newTestRouter(svc)

	w := doReq(t, r, http.MethodPut, "/api/v1/providers/1", `{"name":"vLLM"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	e := parseEnvelope(t, w.Body.Bytes())
	require.NotNil(t, e.Error)
	assert.Equal(t, "VALIDATION_FAILED", e.Error.Code)
}

func TestDeleteProvider(t *testing.T) {
	svc := newFakeSvc()
	svc.providers[1] = providerapi.ProviderSchema{BaseSchema: schema.BaseSchema{ID: "1"}}
	r := newTestRouter(svc)

	w := doReq(t, r, http.MethodDelete, "/api/v1/providers/1", "")
	require.Equal(t, http.StatusNoContent, w.Code)
	assert.Empty(t, w.Body.String(), "204 无响应体")
}

func TestDeleteProvider_InUse(t *testing.T) {
	svc := newFakeSvc()
	svc.injected = providerapi.ErrModelInUse
	r := newTestRouter(svc)
	w := doReq(t, r, http.MethodDelete, "/api/v1/providers/1", "")
	assert.Equal(t, http.StatusConflict, w.Code)
	e := parseEnvelope(t, w.Body.Bytes())
	require.NotNil(t, e.Error)
	assert.Equal(t, "MODEL_IN_USE", e.Error.Code)
}

// 探测失败是业务结果：success=false + error_message 走 data，HTTP 仍 200。
func TestTestConnection_FailureStill200(t *testing.T) {
	svc := newFakeSvc()
	svc.providers[1] = providerapi.ProviderSchema{BaseSchema: schema.BaseSchema{ID: "1"}}
	r := newTestRouter(svc)

	w := doReq(t, r, http.MethodPost, "/api/v1/providers/1/test-connection", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	e := parseEnvelope(t, w.Body.Bytes())
	require.True(t, e.Success)
	d := decodeData[providerapi.ConnectionTestSchema](t, e)
	assert.False(t, d.Success)
	assert.Contains(t, d.ErrorMessage, "401")
}

func TestTestConnection_NotFound(t *testing.T) {
	svc := newFakeSvc()
	svc.injected = providerapi.ErrProviderNotFound
	r := newTestRouter(svc)
	w := doReq(t, r, http.MethodPost, "/api/v1/providers/999/test-connection", "")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ---- 模型 ----

func TestListModels_ProviderIDFromPath(t *testing.T) {
	svc := newFakeSvc()
	svc.models[1] = providerapi.ModelSchema{BaseSchema: schema.BaseSchema{ID: "1"}, ProviderID: "7", ModelID: "gpt-4o"}
	r := newTestRouter(svc)

	w := doReq(t, r, http.MethodGet, "/api/v1/providers/7/models?page=1&page_size=20", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, uint64(7), svc.gotListModelsPID, "嵌套路由：路径 id 应进入 req.ProviderID")
	e := parseEnvelope(t, w.Body.Bytes())
	require.NotNil(t, e.Meta)
	assert.EqualValues(t, 1, e.Meta.Total)
	items := decodeData[[]providerapi.ModelSchema](t, e)
	assert.Len(t, items, 1)
}

func TestListModels_BadProviderID(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	w := doReq(t, r, http.MethodGet, "/api/v1/providers/abc/models", "")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSyncModels(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	w := doReq(t, r, http.MethodPost, "/api/v1/providers/1/models/sync", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	e := parseEnvelope(t, w.Body.Bytes())
	require.True(t, e.Success)
	d := decodeData[providerapi.ModelSyncResultSchema](t, e)
	assert.Equal(t, 2, d.Added)
	assert.Equal(t, 1, d.Updated)
}

func TestCreateModel(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	w := doReq(t, r, http.MethodPost, "/api/v1/models",
		`{"provider_id":1,"name":"GPT-4o","model_id":"gpt-4o","capability":"chat"}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	e := parseEnvelope(t, w.Body.Bytes())
	require.True(t, e.Success)
	d := decodeData[providerapi.ModelSchema](t, e)
	assert.Equal(t, "gpt-4o", d.ModelID)
	assert.Equal(t, "1", d.ProviderID)
}

func TestCreateModel_ValidationFailed(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	// embedding 缺 embedding_dim（跨字段 Validate）
	w := doReq(t, r, http.MethodPost, "/api/v1/models",
		`{"provider_id":1,"name":"E","model_id":"e","capability":"embedding"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateModel_ProviderNotFound(t *testing.T) {
	svc := newFakeSvc()
	svc.injected = providerapi.ErrProviderNotFound
	r := newTestRouter(svc)
	w := doReq(t, r, http.MethodPost, "/api/v1/models",
		`{"provider_id":999,"name":"M","model_id":"m","capability":"chat"}`)
	assert.Equal(t, http.StatusNotFound, w.Code)
	e := parseEnvelope(t, w.Body.Bytes())
	require.NotNil(t, e.Error)
	assert.Equal(t, "PROVIDER_NOT_FOUND", e.Error.Code)
}

func TestGetModel_NotFound(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	w := doReq(t, r, http.MethodGet, "/api/v1/models/999", "")
	assert.Equal(t, http.StatusNotFound, w.Code)
	e := parseEnvelope(t, w.Body.Bytes())
	require.NotNil(t, e.Error)
	assert.Equal(t, "MODEL_NOT_FOUND", e.Error.Code)
}

func TestUpdateModel_IDFromPath(t *testing.T) {
	svc := newFakeSvc()
	svc.models[1] = providerapi.ModelSchema{BaseSchema: schema.BaseSchema{ID: "1"}, ModelID: "gpt-4o"}
	r := newTestRouter(svc)

	w := doReq(t, r, http.MethodPut, "/api/v1/models/1",
		`{"id":999,"provider_id":1,"name":"GPT-4o 2024","model_id":"gpt-4o","capability":"chat"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, uint64(1), svc.gotUpdateModelID, "两段绑定：uri id 应进入 req.ID 且不被 body 覆盖")
}

func TestUpdateModel_IDConflict(t *testing.T) {
	svc := newFakeSvc()
	svc.injected = providerapi.ErrModelIDConflict
	r := newTestRouter(svc)
	w := doReq(t, r, http.MethodPut, "/api/v1/models/1",
		`{"provider_id":1,"name":"M","model_id":"dup","capability":"chat"}`)
	assert.Equal(t, http.StatusConflict, w.Code)
	e := parseEnvelope(t, w.Body.Bytes())
	require.NotNil(t, e.Error)
	assert.Equal(t, "MODEL_ID_CONFLICT", e.Error.Code)
}

func TestDeleteModel_InUse(t *testing.T) {
	svc := newFakeSvc()
	svc.models[1] = providerapi.ModelSchema{BaseSchema: schema.BaseSchema{ID: "1"}}
	svc.injected = providerapi.ErrModelInUse
	r := newTestRouter(svc)
	w := doReq(t, r, http.MethodDelete, "/api/v1/models/1", "")
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestDeleteModel_NoContent(t *testing.T) {
	svc := newFakeSvc()
	svc.models[1] = providerapi.ModelSchema{BaseSchema: schema.BaseSchema{ID: "1"}}
	r := newTestRouter(svc)
	w := doReq(t, r, http.MethodDelete, "/api/v1/models/1", "")
	require.Equal(t, http.StatusNoContent, w.Code)
	assert.Empty(t, w.Body.String())
}

// 未识别错误 → 500 + INTERNAL_ERROR（FailFromSentinel 兜底路径）。
func TestUnexpectedErrorFallsBackTo500(t *testing.T) {
	svc := newFakeSvc()
	svc.injected = errors.New("boom")
	r := newTestRouter(svc)
	w := doReq(t, r, http.MethodGet, "/api/v1/providers/1", "")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	e := parseEnvelope(t, w.Body.Bytes())
	require.NotNil(t, e.Error)
	assert.Equal(t, "INTERNAL_ERROR", e.Error.Code)
}
