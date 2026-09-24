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
	"time"

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
	// listRuns 查收：路径 id / query limit+cursor（spec 015）
	gotListRunsID     uint64
	gotListRunsLimit  int
	gotListRunsCursor string
	// listRuns 结果覆写（nil → 默认一页数据）：空列表 / 归一 limit 形态用例注入
	listRunsRes *workflowapi.RunListResult
	// getRun 查收：两路径参数（spec 015）
	gotGetRunWorkflowID uint64
	gotGetRunRunID      uint64
	// getRun 结果覆写（nil → 默认一 run + 两节点轨迹）
	getRunRes *workflowapi.RunDetailSchema
}

func (f *fakeSvc) Create(_ context.Context, req workflowapi.UpsertReq) (*workflowapi.WorkflowDetailSchema, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	d := &workflowapi.WorkflowDetailSchema{StartNodeKey: req.StartNodeKey}
	d.ID, d.Name, d.Status, d.Type = "42", req.Name, "draft", string(req.Type)
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
	d.ID, d.Name, d.Status, d.Type = "42", "智能客服分流", "published", "task"
	d.InputSchema = []workflowapi.SchemaField{{Name: "query", Type: "string", Required: true}}
	d.OutputSchema = []workflowapi.SchemaField{{Name: "answer", Type: "string"}}
	d.Nodes = []workflowapi.NodeSchema{{Key: "classify", Type: "llm", Config: json.RawMessage(`{"model_id":"3"}`)}}
	d.Edges = []workflowapi.EdgeSchema{}
	return d, nil
}

func (f *fakeSvc) List(_ context.Context, req workflowapi.ListWorkflowsReq) (*workflowapi.WorkflowListResult, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	item := workflowapi.WorkflowSummarySchema{ID: "42", Name: "智能客服分流", Type: "task", Status: "draft",
		InputSchema: []workflowapi.SchemaField{{Name: "query", Type: "string", Required: true}}}
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

// ListRuns 桩（spec 015）：查收绑定注入；默认固定一页数据，listRunsRes 可覆写
//（空列表 / limit 归一形态）。
func (f *fakeSvc) ListRuns(_ context.Context, req workflowapi.ListRunsReq) (*workflowapi.RunListResult, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	f.gotListRunsID = req.WorkflowID
	f.gotListRunsLimit = req.Limit
	f.gotListRunsCursor = req.Cursor
	if f.listRunsRes != nil {
		return f.listRunsRes, nil
	}
	ts := time.Date(2026, 9, 24, 8, 0, 0, 0, time.UTC)
	return &workflowapi.RunListResult{
		Items: []workflowapi.RunSummarySchema{
			{ID: "7", Status: "succeeded", TriggerSource: "console", IsTrial: true, DurationMs: 120,
				StartedAt: ts, CreatedAt: ts},
		},
		Limit: 20, HasMore: true, NextCursor: "next-cursor",
	}, nil
}

// GetRun 桩（spec 015）：查收两路由参数注入；injected 优先（404 / 500 注入），
// getRunRes 可覆写详情数据，默认固定一 run + 两节点轨迹。
func (f *fakeSvc) GetRun(_ context.Context, req workflowapi.GetRunReq) (*workflowapi.RunDetailSchema, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	f.gotGetRunWorkflowID = req.WorkflowID
	f.gotGetRunRunID = req.RunID
	if f.getRunRes != nil {
		return f.getRunRes, nil
	}
	convID, msgID, parentID := "900", "901", "3"
	ts := time.Date(2026, 9, 24, 8, 0, 0, 0, time.UTC)
	return &workflowapi.RunDetailSchema{
		ID: "7", Status: "failed", TriggerSource: "chat", IsTrial: false,
		ConversationID: &convID, MessageID: &msgID, TraceID: "trace-abc", ParentRunID: &parentID,
		Input: `{"query":"查订单"}`, Output: "", ErrorNode: "order_api",
		ErrorMsg: "node order_api: connection refused", DurationMs: 3000,
		StartedAt: ts, CreatedAt: ts.Add(2 * time.Second),
		Nodes: []workflowapi.NodeRunSchema{
			{Seq: 1, NodeKey: "classify", NodeType: "llm", Status: "succeeded",
				Input: `{"q":"查订单"}`, Output: `{"intent":"ORDER"}`, DurationMs: 800},
			{Seq: 2, NodeKey: "order_api", NodeType: "tool", Status: "failed", Input: `{"id":"A1"}`, DurationMs: 120},
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

// createBody 最小合法创建请求体（spec 08 起 type 必填，仅 Create 路径携带）。
const createBody = `{
	"name":"智能客服分流","description":"意图识别 → 分支","type":"chat","start_node_key":"classify",
	"nodes":[{"key":"classify","type":"llm","name":"意图识别","config":{"model_id":"3","prompt":"判断意图"}}],
	"edges":[]
}`

// upsertBody 最小合法整图替换请求体（PUT：不携带 type——分型不可变，spec 08；
// 缺 type 也用作 Create 路径「type 必填拒」的 fixture）。
const upsertBody = `{
	"name":"智能客服分流","description":"意图识别 → 分支","start_node_key":"classify",
	"nodes":[{"key":"classify","type":"llm","name":"意图识别","config":{"model_id":"3","prompt":"判断意图"}}],
	"edges":[]
}`

// ---- 状态码矩阵（spec 04 §7）----

func TestCreateRoute(t *testing.T) {
	r := newTestRouter(&fakeSvc{})
	w := doReq(t, r, http.MethodPost, "/api/v1/workflows", createBody)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	e := parseEnvelope(t, w.Body.Bytes())
	assert.True(t, e.Success)
	d := struct {
		ID     string                    `json:"id"`
		Status string                    `json:"status"`
		Type   string                    `json:"type"`
		Nodes  []workflowapi.NodeSchema  `json:"nodes"`
		Edges  []workflowapi.EdgeSchema  `json:"edges"`
		Config json.RawMessage           `json:"-"`
	}{}
	require.NoError(t, json.Unmarshal(e.Data, &d))
	assert.Equal(t, "42", d.ID)
	assert.Equal(t, "draft", d.Status)
	assert.Equal(t, "chat", d.Type, "type 随请求绑定并回显（spec 08）")
	assert.Len(t, d.Nodes, 1)
	assert.Equal(t, "classify", d.Nodes[0].Key)
	assert.NotNil(t, d.Edges, "空 edges → [] 不 null")
}

// 分型守卫经既有 VALIDATION_FAILED 400 分支（spec 08 §4.1）：type 缺失 / 非法值。
func TestCreateRouteTypeGate(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"type 缺失拒", upsertBody},
		{"type 非法值拒", strings.Replace(createBody, `"type":"chat"`, `"type":"bogus"`, 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRouter(&fakeSvc{})
			w := doReq(t, r, http.MethodPost, "/api/v1/workflows", tc.body)
			assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			e := parseEnvelope(t, w.Body.Bytes())
			assert.Equal(t, "VALIDATION_FAILED", e.Error.Code)
		})
	}
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
	w := doReq(t, r, http.MethodPost, "/api/v1/workflows", createBody)
	assert.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	e := parseEnvelope(t, w.Body.Bytes())
	assert.Equal(t, "WORKFLOW_NAME_CONFLICT", e.Error.Code)
}

func TestCreateRoutePrecheckValidation(t *testing.T) {
	// service 条 9 / R11 校验翻译：errs.ErrValidationFailed 包装 → FailFromSentinel 400
	r := newTestRouter(&fakeSvc{injected: fmtValidationErr()})
	w := doReq(t, r, http.MethodPost, "/api/v1/workflows", createBody)
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

// TestUpdateRouteTypeImmutable Update body 携带 type → 400 VALIDATION_FAILED 信封
//（spec 08 §4.1：携带即拒——同值 / 异值均拒；Validate guard 经 BindJSON 拦，
// service 不被触达）。
func TestUpdateRouteTypeImmutable(t *testing.T) {
	svc := &fakeSvc{}
	r := newTestRouter(svc)
	for _, tc := range []struct{ name, body string }{
		{"同值也拒", strings.Replace(upsertBody, `"start_node_key"`, `"type":"chat","start_node_key"`, 1)},
		{"异值拒", strings.Replace(upsertBody, `"start_node_key"`, `"type":"task","start_node_key"`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := doReq(t, r, http.MethodPut, "/api/v1/workflows/7", tc.body)
			assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			e := parseEnvelope(t, w.Body.Bytes())
			assert.Equal(t, "VALIDATION_FAILED", e.Error.Code)
		})
	}
	assert.Zero(t, svc.gotUpdateID, "Validate 层即拒，service 不被触达")
}

// TestGetListRouteTypeExposure Get / List 暴露 type 与 schema 字段（spec 08 §2.1
// 管理面可见分型；schema 未声明 / chat 型序列化为 null）。
func TestGetListRouteTypeExposure(t *testing.T) {
	r := newTestRouter(&fakeSvc{})

	w := doReq(t, r, http.MethodGet, "/api/v1/workflows/42", "")
	require.Equal(t, http.StatusOK, w.Code)
	e := parseEnvelope(t, w.Body.Bytes())
	d := struct {
		Type         string                    `json:"type"`
		InputSchema  []workflowapi.SchemaField `json:"input_schema"`
		OutputSchema []workflowapi.SchemaField `json:"output_schema"`
	}{}
	require.NoError(t, json.Unmarshal(e.Data, &d))
	assert.Equal(t, "task", d.Type)
	assert.Equal(t, []workflowapi.SchemaField{{Name: "query", Type: "string", Required: true}}, d.InputSchema)
	assert.Equal(t, []workflowapi.SchemaField{{Name: "answer", Type: "string"}}, d.OutputSchema)

	w = doReq(t, r, http.MethodGet, "/api/v1/workflows?page=1&page_size=20", "")
	require.Equal(t, http.StatusOK, w.Code)
	e = parseEnvelope(t, w.Body.Bytes())
	assert.Contains(t, string(e.Data), `"type":"task"`)
	assert.Contains(t, string(e.Data), `"input_schema"`)
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

// ---- runs 列表路由（spec 015，api_contract §5）----

// TestListRunsRoute 200 信封：data = RunSummarySchema 数组（摘要面无 input/output
// 大文本，FR-002）+ 游标 meta（A 模式 limit/has_more/next_cursor）；两段绑定——
// 路径 :id 注入 req.WorkflowID、query limit/cursor 缺省零值透传。
func TestListRunsRoute(t *testing.T) {
	svc := &fakeSvc{}
	r := newTestRouter(svc)
	w := doReq(t, r, http.MethodGet, "/api/v1/workflows/1/runs", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	e := parseEnvelope(t, w.Body.Bytes())
	assert.True(t, e.Success)
	items := []workflowapi.RunSummarySchema{}
	require.NoError(t, json.Unmarshal(e.Data, &items))
	require.Len(t, items, 1)
	assert.Equal(t, "7", items[0].ID)
	assert.Equal(t, "succeeded", items[0].Status)
	assert.Equal(t, "console", items[0].TriggerSource)
	assert.True(t, items[0].IsTrial)
	assert.NotContains(t, string(e.Data), `"input"`, "列表面无大文本键（FR-002）")
	assert.NotContains(t, string(e.Data), `"output"`, "列表面无大文本键（FR-002）")
	assert.Contains(t, w.Body.String(), `"limit":20,"has_more":true,"next_cursor":"next-cursor"`,
		"游标 meta（chat 先例同款断言形态）")
	// 两段绑定查收：路径 id 注入 + query 缺省零值
	assert.Equal(t, uint64(1), svc.gotListRunsID)
	assert.Zero(t, svc.gotListRunsLimit)
	assert.Empty(t, svc.gotListRunsCursor)
}

// TestListRunsRouteQueryBinding query limit/cursor 绑定透传（归一在 service，T007 已测，
// 此处验证 handler 忠实透传 service 的归一结果到 meta + 尾页 next_cursor 序列化 null）。
func TestListRunsRouteQueryBinding(t *testing.T) {
	svc := &fakeSvc{}
	r := newTestRouter(svc)
	w := doReq(t, r, http.MethodGet, "/api/v1/workflows/1/runs?limit=999&cursor=abc", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, 999, svc.gotListRunsLimit, "?limit 原样透传（归一归 service）")
	assert.Equal(t, "abc", svc.gotListRunsCursor, "?cursor 原样透传")

	// service 归一后（limit=100、尾页无游标）→ meta 忠实反映 + next_cursor null
	svc1 := &fakeSvc{listRunsRes: &workflowapi.RunListResult{
		Items: []workflowapi.RunSummarySchema{}, Limit: 100, HasMore: false, NextCursor: "",
	}}
	r1 := newTestRouter(svc1)
	w1 := doReq(t, r1, http.MethodGet, "/api/v1/workflows/1/runs?limit=-5", "")
	require.Equal(t, http.StatusOK, w1.Code, w1.Body.String())
	assert.Equal(t, -5, svc1.gotListRunsLimit)
	assert.Contains(t, w1.Body.String(), `"limit":100,"has_more":false,"next_cursor":null`)
}

// TestListRunsRouteBadCursor 篡改 cursor → service 翻译 errs.ErrValidationFailed 包装
// → failWorkflow 无模块哨兵命中、FailFromSentinel 通用映射 400。
func TestListRunsRouteBadCursor(t *testing.T) {
	r := newTestRouter(&fakeSvc{injected: fmt.Errorf("%w: cursor: illegal base64", errs.ErrValidationFailed)})
	w := doReq(t, r, http.MethodGet, "/api/v1/workflows/1/runs?cursor=%E7%AF%A1%E6%94%B9", "")
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	e := parseEnvelope(t, w.Body.Bytes())
	assert.Equal(t, "VALIDATION_FAILED", e.Error.Code)
}

// TestListRunsRouteEmptyList 空列表 → data 为 [] 非 null（空值约定）。
func TestListRunsRouteEmptyList(t *testing.T) {
	r := newTestRouter(&fakeSvc{listRunsRes: &workflowapi.RunListResult{
		Items: []workflowapi.RunSummarySchema{}, Limit: 20,
	}})
	w := doReq(t, r, http.MethodGet, "/api/v1/workflows/1/runs", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"data":[]`, "空列表 → [] 非 null")
}

// TestListRunsRouteBadID 路径 :id 非数字 → BindUri 失败 400。
func TestListRunsRouteBadID(t *testing.T) {
	r := newTestRouter(&fakeSvc{})
	w := doReq(t, r, http.MethodGet, "/api/v1/workflows/abc/runs", "")
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
}

// ---- getRun：运行详情路由（spec 015，D3 同判 404 + 轨迹按执行序）----

func TestGetRunRoute(t *testing.T) {
	svc := &fakeSvc{}
	r := newTestRouter(svc)
	w := doReq(t, r, http.MethodGet, "/api/v1/workflows/1/runs/7", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	e := parseEnvelope(t, w.Body.Bytes())
	assert.True(t, e.Success)
	d := workflowapi.RunDetailSchema{}
	require.NoError(t, json.Unmarshal(e.Data, &d))
	assert.Equal(t, "7", d.ID)
	assert.Equal(t, "failed", d.Status)
	assert.Equal(t, "chat", d.TriggerSource)
	assert.Equal(t, "order_api", d.ErrorNode, "失败节点可读")
	assert.Equal(t, `{"query":"查订单"}`, d.Input, "详情面携带大文本")
	require.NotNil(t, d.ParentRunID)
	assert.Equal(t, "3", *d.ParentRunID)
	require.Len(t, d.Nodes, 2)
	assert.Equal(t, 1, d.Nodes[0].Seq)
	assert.Equal(t, 2, d.Nodes[1].Seq, "轨迹按执行序（seq ASC）")
	assert.Equal(t, "order_api", d.Nodes[1].NodeKey)
	// 两路径参数注入查收（GetRunReq 无 uri tag，handler 手动注入）
	assert.Equal(t, uint64(1), svc.gotGetRunWorkflowID)
	assert.Equal(t, uint64(7), svc.gotGetRunRunID)
}

// 不存在与跨工作流同判 404（D3）：code 机器可读 + 中文 message（chat 会话 404 同款）。
func TestGetRunRouteNotFound(t *testing.T) {
	r := newTestRouter(&fakeSvc{injected: workflowapi.ErrRunNotFound})
	w := doReq(t, r, http.MethodGet, "/api/v1/workflows/1/runs/999", "")
	require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
	e := parseEnvelope(t, w.Body.Bytes())
	require.NotNil(t, e.Error)
	assert.Equal(t, "RUN_NOT_FOUND", e.Error.Code)
	assert.Equal(t, "运行记录不存在", e.Error.Message)
}

func TestGetRunRouteBadRunID(t *testing.T) {
	r := newTestRouter(&fakeSvc{})
	w := doReq(t, r, http.MethodGet, "/api/v1/workflows/1/runs/abc", "")
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
}

func TestGetRunRouteBadWorkflowID(t *testing.T) {
	r := newTestRouter(&fakeSvc{})
	w := doReq(t, r, http.MethodGet, "/api/v1/workflows/abc/runs/7", "")
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
}
