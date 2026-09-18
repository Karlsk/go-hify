package handler

import (
	"context"
	"encoding/json"
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
	"github.com/Karlsk/go-hify/internal/platform/llm"
	providerapi "github.com/Karlsk/go-hify/internal/provider/api"
	ragapi "github.com/Karlsk/go-hify/internal/rag/api"
	workflowapi "github.com/Karlsk/go-hify/internal/workflow/api"
)

// fmtValidationErr 复现 service 条 9 预检的错误形态（errs.ErrValidationFailed 包装）。
func fmtValidationErr() error {
	return fmt.Errorf("%w: node %q model_id %d", errs.ErrValidationFailed, "classify", 3)
}

// errBoom 未识别错误（非哨兵）→ 兜底 500。
type errBoom struct{}

func (errBoom) Error() string { return "boom" }

// handler 测试：httptest 走绑定函数，验证状态码 + respond 信封结构 + 哨兵映射
//（agent / provider 同款）。fakeSvc 只复现哨兵路径与参数查收，不复现业务规则。

// fakeSvc 实现 workflowapi.WorkflowService：injected 非 nil 时所有方法返回它
//（哨兵 / 500 注入）。
type fakeSvc struct {
	injected error

	// 查收两段绑定是否正确落到 req（update 的路径 id）
	gotUpdateID uint64
	// execute 查收：路径 id / body input / query trial
	gotExecuteID    uint64
	gotExecuteInput string
	gotExecuteTrial bool
}

func (f *fakeSvc) Create(_ context.Context, req workflowapi.UpsertReq) (*workflowapi.WorkflowDetailSchema, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	d := &workflowapi.WorkflowDetailSchema{StartNodeKey: req.StartNodeKey}
	d.ID, d.Name, d.Status = "42", req.Name, "draft"
	d.Nodes = make([]workflowapi.NodeSchema, 0, len(req.Nodes))
	for _, n := range req.Nodes {
		d.Nodes = append(d.Nodes, workflowapi.NodeSchema{Key: n.Key, Type: string(n.Type), Config: n.Config})
	}
	d.Edges = make([]workflowapi.EdgeSchema, 0)
	return d, nil
}

func (f *fakeSvc) Get(_ context.Context, req workflowapi.GetWorkflowReq) (*workflowapi.WorkflowDetailSchema, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	if req.ID != 42 {
		return nil, workflowapi.ErrWorkflowNotFound
	}
	d := &workflowapi.WorkflowDetailSchema{StartNodeKey: "classify"}
	d.ID, d.Name, d.Status = "42", "智能客服分流", "published"
	d.Nodes = []workflowapi.NodeSchema{{Key: "classify", Type: "llm", Config: json.RawMessage(`{"model_id":"3"}`)}}
	d.Edges = []workflowapi.EdgeSchema{}
	return d, nil
}

func (f *fakeSvc) List(_ context.Context, req workflowapi.ListWorkflowsReq) (*workflowapi.WorkflowListResult, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	item := workflowapi.WorkflowSummarySchema{ID: "42", Name: "智能客服分流", Status: "draft"}
	return &workflowapi.WorkflowListResult{
		Items: []workflowapi.WorkflowSummarySchema{item}, Page: 1, PageSize: 20, Total: 1,
	}, nil
}

func (f *fakeSvc) Update(_ context.Context, req workflowapi.UpdateWorkflowReq) (*workflowapi.WorkflowDetailSchema, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	f.gotUpdateID = req.ID
	d := &workflowapi.WorkflowDetailSchema{StartNodeKey: req.StartNodeKey}
	d.ID, d.Name, d.Status = strconv.FormatUint(req.ID, 10), req.Name, "published"
	d.Nodes = []workflowapi.NodeSchema{}
	d.Edges = []workflowapi.EdgeSchema{}
	return d, nil
}

func (f *fakeSvc) Delete(_ context.Context, req workflowapi.DeleteWorkflowReq) error {
	if f.injected != nil {
		return f.injected
	}
	if req.ID != 42 {
		return workflowapi.ErrWorkflowNotFound
	}
	return nil
}

func (f *fakeSvc) Publish(_ context.Context, req workflowapi.PublishWorkflowReq) (*workflowapi.WorkflowSummarySchema, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	s := workflowapi.WorkflowSummarySchema{ID: strconv.FormatUint(req.ID, 10), Status: "published"}
	return &s, nil
}

func (f *fakeSvc) Disable(_ context.Context, req workflowapi.DisableWorkflowReq) (*workflowapi.WorkflowSummarySchema, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	s := workflowapi.WorkflowSummarySchema{ID: strconv.FormatUint(req.ID, 10), Status: "disabled"}
	return &s, nil
}

func (f *fakeSvc) Execute(_ context.Context, req workflowapi.ExecuteWorkflowReq) (*workflowapi.RunResultSchema, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	f.gotExecuteID = req.ID
	f.gotExecuteInput = req.Input
	f.gotExecuteTrial = req.Trial
	return &workflowapi.RunResultSchema{
		RunID: "7", Status: "succeeded", Output: "订单已发货", DurationMs: 120,
		NodeTrace: []workflowapi.NodeRunSummary{
			{NodeKey: "classify", NodeType: "llm", Status: "succeeded", DurationMs: 80},
			{NodeKey: "finish", NodeType: "end", Status: "succeeded", DurationMs: 1},
		},
	}, nil
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

// upsertBody 最小合法整图请求体（含 llm 节点）。
const upsertBody = `{
	"name":"智能客服分流","description":"意图识别 → 分支","start_node_key":"classify",
	"nodes":[{"key":"classify","type":"llm","name":"意图识别","config":{"model_id":"3","prompt":"判断意图"}}],
	"edges":[]
}`

// ---- 状态码矩阵（spec 04 §7）----

func TestCreateRoute(t *testing.T) {
	r := newTestRouter(&fakeSvc{})
	w := doReq(t, r, http.MethodPost, "/api/v1/workflows", upsertBody)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	e := parseEnvelope(t, w.Body.Bytes())
	assert.True(t, e.Success)
	d := struct {
		ID     string                    `json:"id"`
		Status string                    `json:"status"`
		Nodes  []workflowapi.NodeSchema  `json:"nodes"`
		Edges  []workflowapi.EdgeSchema  `json:"edges"`
		Config json.RawMessage           `json:"-"`
	}{}
	require.NoError(t, json.Unmarshal(e.Data, &d))
	assert.Equal(t, "42", d.ID)
	assert.Equal(t, "draft", d.Status)
	assert.Len(t, d.Nodes, 1)
	assert.Equal(t, "classify", d.Nodes[0].Key)
	assert.NotNil(t, d.Edges, "空 edges → [] 不 null")
}

func TestCreateRouteBindFail(t *testing.T) {
	r := newTestRouter(&fakeSvc{})
	w := doReq(t, r, http.MethodPost, "/api/v1/workflows", `{"description":"缺 name"}`)
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	e := parseEnvelope(t, w.Body.Bytes())
	assert.Equal(t, errs.ErrValidationFailed.Error(), e.Error.Code, "绑定失败 → VALIDATION_FAILED 信封")
}

func TestCreateRouteNameConflict(t *testing.T) {
	r := newTestRouter(&fakeSvc{injected: workflowapi.ErrWorkflowNameConflict})
	w := doReq(t, r, http.MethodPost, "/api/v1/workflows", upsertBody)
	assert.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	e := parseEnvelope(t, w.Body.Bytes())
	assert.Equal(t, "WORKFLOW_NAME_CONFLICT", e.Error.Code)
}

func TestCreateRoutePrecheckValidation(t *testing.T) {
	// service 条 9 预检翻译：errs.ErrValidationFailed 包装 → FailFromSentinel 400
	r := newTestRouter(&fakeSvc{injected: fmtValidationErr()})
	w := doReq(t, r, http.MethodPost, "/api/v1/workflows", upsertBody)
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	e := parseEnvelope(t, w.Body.Bytes())
	assert.Equal(t, "VALIDATION_FAILED", e.Error.Code)
	assert.Contains(t, e.Error.Message, `node "classify"`)
}

func TestGetRoute(t *testing.T) {
	r := newTestRouter(&fakeSvc{})
	w := doReq(t, r, http.MethodGet, "/api/v1/workflows/42", "")
	require.Equal(t, http.StatusOK, w.Code)
	e := parseEnvelope(t, w.Body.Bytes())
	assert.True(t, e.Success)
	assert.Contains(t, string(e.Data), `"status":"published"`)
}

func TestGetRouteNotFound(t *testing.T) {
	r := newTestRouter(&fakeSvc{})
	w := doReq(t, r, http.MethodGet, "/api/v1/workflows/999", "")
	assert.Equal(t, http.StatusNotFound, w.Code)
	e := parseEnvelope(t, w.Body.Bytes())
	assert.Equal(t, "WORKFLOW_NOT_FOUND", e.Error.Code)
}

func TestGetRouteNotPublishedMapping(t *testing.T) {
	// 503 映射本期登记（execute 路由归执行器 spec，经注入验证映射存在）
	r := newTestRouter(&fakeSvc{injected: workflowapi.ErrWorkflowNotPublished})
	w := doReq(t, r, http.MethodGet, "/api/v1/workflows/42", "")
	assert.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
	e := parseEnvelope(t, w.Body.Bytes())
	assert.Equal(t, "WORKFLOW_NOT_PUBLISHED", e.Error.Code)
}

func TestListRoute(t *testing.T) {
	r := newTestRouter(&fakeSvc{})
	w := doReq(t, r, http.MethodGet, "/api/v1/workflows?page=1&page_size=20", "")
	require.Equal(t, http.StatusOK, w.Code)
	e := parseEnvelope(t, w.Body.Bytes())
	require.NotNil(t, e.Meta, "list 必带分页 meta（B 模式）")
	assert.Equal(t, 1, e.Meta.Page)
	assert.Equal(t, 20, e.Meta.PageSize)
	assert.Equal(t, int64(1), e.Meta.Total)
	assert.Contains(t, string(e.Data), `"id":"42"`)
}

func TestUpdateRoute(t *testing.T) {
	svc := &fakeSvc{}
	r := newTestRouter(svc)
	w := doReq(t, r, http.MethodPut, "/api/v1/workflows/7", upsertBody)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, uint64(7), svc.gotUpdateID, "两段绑定：路径 id 赋入 UpdateWorkflowReq.ID")
	e := parseEnvelope(t, w.Body.Bytes())
	assert.Contains(t, string(e.Data), `"id":"7"`)
}

func TestUpdateRouteBindFail(t *testing.T) {
	r := newTestRouter(&fakeSvc{})
	w := doReq(t, r, http.MethodPut, "/api/v1/workflows/7", `{"name":"缺图"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
}

func TestDeleteRoute(t *testing.T) {
	r := newTestRouter(&fakeSvc{})
	w := doReq(t, r, http.MethodDelete, "/api/v1/workflows/42", "")
	assert.Equal(t, http.StatusNoContent, w.Code, "204 无响应体（agent delete 同款）")
	assert.Empty(t, w.Body.String())
}

func TestDeleteRouteNotFound(t *testing.T) {
	r := newTestRouter(&fakeSvc{})
	w := doReq(t, r, http.MethodDelete, "/api/v1/workflows/999", "")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestDeleteRouteInUse(t *testing.T) {
	// spec 05 US3：被 agent 绑定 → 409 WORKFLOW_IN_USE 信封（双向删除互锁的
	// workflow 侧出口）。
	r := newTestRouter(&fakeSvc{injected: workflowapi.ErrWorkflowInUse})
	w := doReq(t, r, http.MethodDelete, "/api/v1/workflows/42", "")
	assert.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	e := parseEnvelope(t, w.Body.Bytes())
	require.NotNil(t, e.Error)
	assert.Equal(t, "WORKFLOW_IN_USE", e.Error.Code)
}

func TestPublishDisableRoutes(t *testing.T) {
	r := newTestRouter(&fakeSvc{})
	w := doReq(t, r, http.MethodPost, "/api/v1/workflows/42/publish", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"status":"published"`)

	w = doReq(t, r, http.MethodPost, "/api/v1/workflows/42/disable", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"status":"disabled"`)
}

func TestRouteInternalError(t *testing.T) {
	r := newTestRouter(&fakeSvc{injected: errBoom{}})
	w := doReq(t, r, http.MethodGet, "/api/v1/workflows/42", "")
	assert.Equal(t, http.StatusInternalServerError, w.Code, "未识别错误兜底 500")
}

// ---- execute 路由（spec 06 api_contract §5）----

func TestExecuteRoute(t *testing.T) {
	svc := &fakeSvc{}
	r := newTestRouter(svc)
	w := doReq(t, r, http.MethodPost, "/api/v1/workflows/42/execute", `{"input":"查订单"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	e := parseEnvelope(t, w.Body.Bytes())
	assert.True(t, e.Success)
	d := workflowapi.RunResultSchema{}
	require.NoError(t, json.Unmarshal(e.Data, &d))
	assert.Equal(t, "7", d.RunID)
	assert.Equal(t, "succeeded", d.Status)
	assert.Equal(t, "订单已发货", d.Output)
	assert.Equal(t, 120, d.DurationMs)
	require.Len(t, d.NodeTrace, 2)
	assert.Equal(t, "classify", d.NodeTrace[0].NodeKey)
	assert.Equal(t, "llm", d.NodeTrace[0].NodeType)
	// 两段绑定 + trial 缺省 false
	assert.Equal(t, uint64(42), svc.gotExecuteID)
	assert.Equal(t, "查订单", svc.gotExecuteInput)
	assert.False(t, svc.gotExecuteTrial)
}

// ?trial=true 才算试运行（O3）：trial=1 / 缺省均为 false。
func TestExecuteRouteTrialQuery(t *testing.T) {
	svc := &fakeSvc{}
	r := newTestRouter(svc)
	w := doReq(t, r, http.MethodPost, "/api/v1/workflows/42/execute?trial=true", `{"input":"x"}`)
	require.Equal(t, http.StatusOK, w.Code)
	assert.True(t, svc.gotExecuteTrial, "?trial=true → req.Trial=true")

	svc1 := &fakeSvc{}
	r1 := newTestRouter(svc1)
	w1 := doReq(t, r1, http.MethodPost, "/api/v1/workflows/42/execute?trial=1", `{"input":"x"}`)
	require.Equal(t, http.StatusOK, w1.Code)
	assert.False(t, svc1.gotExecuteTrial, "仅字面 true 生效（trial=1 不算）")
}

func TestExecuteRouteInputMissing(t *testing.T) {
	r := newTestRouter(&fakeSvc{})
	w := doReq(t, r, http.MethodPost, "/api/v1/workflows/42/execute", `{}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	e := parseEnvelope(t, w.Body.Bytes())
	assert.Equal(t, "VALIDATION_FAILED", e.Error.Code)
}

func TestExecuteRouteInputTooLong(t *testing.T) {
	r := newTestRouter(&fakeSvc{})
	body := `{"input":"` + strings.Repeat("a", 16385) + `"}`
	w := doReq(t, r, http.MethodPost, "/api/v1/workflows/42/execute", body)
	assert.Equal(t, http.StatusBadRequest, w.Code, "input 超 16384 上限（O1）")
	e := parseEnvelope(t, w.Body.Bytes())
	assert.Equal(t, "VALIDATION_FAILED", e.Error.Code)
}

func TestExecuteRouteNotPublished(t *testing.T) {
	r := newTestRouter(&fakeSvc{injected: workflowapi.ErrWorkflowNotPublished})
	w := doReq(t, r, http.MethodPost, "/api/v1/workflows/42/execute", `{"input":"x"}`)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	e := parseEnvelope(t, w.Body.Bytes())
	assert.Equal(t, "WORKFLOW_NOT_PUBLISHED", e.Error.Code)
}

// 环境限制类 → 500 WORKFLOW_EXECUTION_FAILED（O4 二分法的执行侧出口）。
func TestExecuteRouteExecutionFailed(t *testing.T) {
	r := newTestRouter(&fakeSvc{injected: workflowapi.ErrWorkflowExecutionFailed})
	w := doReq(t, r, http.MethodPost, "/api/v1/workflows/42/execute", `{"input":"x"}`)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	e := parseEnvelope(t, w.Body.Bytes())
	assert.Equal(t, "WORKFLOW_EXECUTION_FAILED", e.Error.Code)
}

// 下游哨兵原样透传（errors.Is 链）：模型 / KB 不存在 404，供应商忙 / 不可用 503。
func TestExecuteRouteDownstreamSentinels(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code int
		want string
	}{
		{"模型不存在（包装链）", fmt.Errorf("node reply: %w", providerapi.ErrModelNotFound), http.StatusNotFound, "MODEL_NOT_FOUND"},
		{"KB 不存在", fmt.Errorf("node kb: %w", ragapi.ErrKnowledgeBaseNotFound), http.StatusNotFound, "KNOWLEDGE_BASE_NOT_FOUND"},
		{"供应商忙", llm.ErrProviderBusy, http.StatusServiceUnavailable, "PROVIDER_BUSY"},
		{"供应商不可用", llm.ErrProviderUnavailable, http.StatusServiceUnavailable, "PROVIDER_UNAVAILABLE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRouter(&fakeSvc{injected: tc.err})
			w := doReq(t, r, http.MethodPost, "/api/v1/workflows/42/execute", `{"input":"x"}`)
			assert.Equal(t, tc.code, w.Code, w.Body.String())
			e := parseEnvelope(t, w.Body.Bytes())
			require.NotNil(t, e.Error)
			assert.Equal(t, tc.want, e.Error.Code)
		})
	}
}
