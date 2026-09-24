package service

// service 测试：stub Store（函数字段覆写 + 调用序列记录）、内嵌接口 stub 下游
// （provider / rag 只覆写 Get）、记录式 cacheManager stub——零真实 PG / Redis / 网络，
// 对齐 agent service_test 模式（spec 04 §7）。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	providerapi "github.com/Karlsk/go-hify/internal/provider/api"
	ragapi "github.com/Karlsk/go-hify/internal/rag/api"
	"github.com/Karlsk/go-hify/internal/platform/errs"
	"github.com/Karlsk/go-hify/internal/platform/page"
	workflowapi "github.com/Karlsk/go-hify/internal/workflow/api"
)

// ---- 下游 stub（内嵌接口、只覆写 Get，小接口惯例）----

// stubModels 覆写 providerapi.ModelService.Get（llm 节点预检）与 ResolveLLMConfig
//（执行引擎 callLLM 链）；未覆写的方法沿用内嵌接口 nil 实现（测试只触达声明路径）。
type stubModels struct {
	providerapi.ModelService
	getFn     func(req providerapi.GetModelReq) (*providerapi.ModelSchema, error)
	resolveFn func(req providerapi.ResolveLLMConfigReq) (*providerapi.LLMConfig, error)
}

func (s *stubModels) Get(ctx context.Context, req providerapi.GetModelReq) (*providerapi.ModelSchema, error) {
	return s.getFn(req)
}

func (s *stubModels) ResolveLLMConfig(ctx context.Context, req providerapi.ResolveLLMConfigReq) (*providerapi.LLMConfig, error) {
	return s.resolveFn(req)
}

// stubKbs 只覆写 ragapi.KnowledgeBaseService.Get（knowledge_retrieval 节点预检）。
type stubKbs struct {
	ragapi.KnowledgeBaseService
	getFn func(req ragapi.GetKnowledgeBaseReq) (*ragapi.KnowledgeBaseSchema, error)
}

func (s *stubKbs) Get(ctx context.Context, req ragapi.GetKnowledgeBaseReq) (*ragapi.KnowledgeBaseSchema, error) {
	return s.getFn(req)
}

// ---- 测试基建 ----

// stubStore 以函数字段覆写 Store 行为；未设置的字段被调用即 panic（测试只应触达
// 声明的路径）。seq 按序记录方法名，供「预检失败不动 store」「Del key 时序」断言。
type stubStore struct {
	getByIDFn      func(id uint64) (*Workflow, error)
	listFn         func(offset, limit int) ([]Workflow, int64, error)
	listNodesFn    func(workflowID uint64) ([]WorkflowNode, error)
	listEdgesFn    func(workflowID uint64) ([]WorkflowEdge, error)
	createFn       func(wf *Workflow, nodes []WorkflowNode, edges []WorkflowEdge) error
	replaceGraphFn func(wf *Workflow, nodes []WorkflowNode, edges []WorkflowEdge) (bool, error)
	deleteFn       func(id uint64) (bool, error)
	updateStatusFn func(id uint64, from []string, to string) (bool, error)
	createRunFn    func(run *WorkflowRun, nodeRuns []WorkflowNodeRun) error
	deleteRunsFn   func(before time.Time, limit int) (int64, error)
	updateParentRunIDsFn func(parentRunID uint64, childRunIDs []uint64) error
	listRunsFn     func(workflowID uint64, beforeCreatedAt time.Time, beforeID uint64, limit int) ([]WorkflowRun, error)
	getRunByIDFn   func(workflowID, runID uint64) (*WorkflowRun, error)
	listNodeRunsFn func(runID uint64) ([]WorkflowNodeRun, error)

	seq      []string // 调用序列（方法名）
	lastFrom []string // UpdateStatus 最近一次 from
	lastTo   string   // UpdateStatus 最近一次 to
	// lastListRuns ListRuns 最近一次收到的参数（归一后 limit / 首页零值游标断言）。
	lastListRuns struct {
		workflowID uint64
		before     time.Time
		beforeID   uint64
		limit      int
	}
	// lastGetRunByID / lastListNodeRuns GetRun 链路最近一次收到的参数（D3 双条件
	// 透传与轨迹以 run 主键查询断言）。
	lastGetRunByID   struct{ workflowID, runID uint64 }
	lastListNodeRuns struct{ runID uint64 }
}

func (s *stubStore) GetByID(ctx context.Context, id uint64) (*Workflow, error) {
	s.seq = append(s.seq, "getByID")
	return s.getByIDFn(id)
}

func (s *stubStore) List(ctx context.Context, offset, limit int) ([]Workflow, int64, error) {
	s.seq = append(s.seq, "list")
	return s.listFn(offset, limit)
}

func (s *stubStore) ListNodes(ctx context.Context, workflowID uint64) ([]WorkflowNode, error) {
	s.seq = append(s.seq, "listNodes")
	return s.listNodesFn(workflowID)
}

func (s *stubStore) ListEdges(ctx context.Context, workflowID uint64) ([]WorkflowEdge, error) {
	s.seq = append(s.seq, "listEdges")
	return s.listEdgesFn(workflowID)
}

func (s *stubStore) Create(ctx context.Context, wf *Workflow, nodes []WorkflowNode, edges []WorkflowEdge) error {
	s.seq = append(s.seq, "create")
	return s.createFn(wf, nodes, edges)
}

func (s *stubStore) ReplaceGraph(ctx context.Context, wf *Workflow, nodes []WorkflowNode, edges []WorkflowEdge) (bool, error) {
	s.seq = append(s.seq, "replaceGraph")
	return s.replaceGraphFn(wf, nodes, edges)
}

func (s *stubStore) Delete(ctx context.Context, id uint64) (bool, error) {
	s.seq = append(s.seq, "delete")
	return s.deleteFn(id)
}

func (s *stubStore) UpdateStatus(ctx context.Context, id uint64, from []string, to string) (bool, error) {
	s.seq = append(s.seq, "updateStatus")
	s.lastFrom, s.lastTo = from, to
	return s.updateStatusFn(id, from, to)
}

func (s *stubStore) CreateRun(ctx context.Context, run *WorkflowRun, nodeRuns []WorkflowNodeRun) error {
	s.seq = append(s.seq, "createRun")
	return s.createRunFn(run, nodeRuns)
}

func (s *stubStore) DeleteRunsBefore(ctx context.Context, before time.Time, limit int) (int64, error) {
	s.seq = append(s.seq, "deleteRunsBefore")
	return s.deleteRunsFn(before, limit)
}

func (s *stubStore) UpdateParentRunIDs(ctx context.Context, parentRunID uint64, childRunIDs []uint64) error {
	s.seq = append(s.seq, "updateParentRunIDs")
	return s.updateParentRunIDsFn(parentRunID, childRunIDs)
}

func (s *stubStore) ListRuns(ctx context.Context, workflowID uint64, beforeCreatedAt time.Time, beforeID uint64, limit int) ([]WorkflowRun, error) {
	s.seq = append(s.seq, "listRuns")
	s.lastListRuns.workflowID = workflowID
	s.lastListRuns.before = beforeCreatedAt
	s.lastListRuns.beforeID = beforeID
	s.lastListRuns.limit = limit
	return s.listRunsFn(workflowID, beforeCreatedAt, beforeID, limit)
}

func (s *stubStore) GetRunByID(ctx context.Context, workflowID, runID uint64) (*WorkflowRun, error) {
	s.seq = append(s.seq, "getRunByID")
	s.lastGetRunByID.workflowID = workflowID
	s.lastGetRunByID.runID = runID
	return s.getRunByIDFn(workflowID, runID)
}

func (s *stubStore) ListNodeRuns(ctx context.Context, runID uint64) ([]WorkflowNodeRun, error) {
	s.seq = append(s.seq, "listNodeRuns")
	s.lastListNodeRuns.runID = runID
	return s.listNodeRunsFn(runID)
}

// recordCache 记录式 cacheManager stub：Get 返回预设（getVal 经 JSON 往返写入 dst，
// 模拟 redisx.GetStruct 语义）；Set / Del 记录 key 进 seq。
type recordCache struct {
	getFound bool
	getErr   error
	setErr   error
	delErr   error
	getVal   any      // 命中时写入 dst 的值（*WorkflowDetailSchema）
	setVals  []string // Set 的 key 集合（断言回填）
	seq      []string // "get:{key}" / "set:{key}" / "del:{key}" 按序
}

func (c *recordCache) Get(ctx context.Context, name, key string, dst any) (bool, error) {
	c.seq = append(c.seq, "get:"+key)
	if c.getErr != nil {
		return false, c.getErr
	}
	if c.getFound && c.getVal != nil {
		b, err := json.Marshal(c.getVal)
		if err != nil {
			return false, err
		}
		if err := json.Unmarshal(b, dst); err != nil {
			return false, err
		}
	}
	return c.getFound, nil
}

func (c *recordCache) Set(ctx context.Context, name, key string, val any) error {
	c.seq = append(c.seq, "set:"+key)
	c.setVals = append(c.setVals, key)
	return c.setErr
}

func (c *recordCache) Delete(ctx context.Context, name, key string) error {
	c.seq = append(c.seq, "del:"+key)
	return c.delErr
}

// ---- T1：骨架与接线 ----

func TestNewWiring(t *testing.T) {
	svc := New(&stubStore{}, nil, nil, &recordCache{}, nil, nil, nil, false)
	assert.NotNil(t, svc)
	_, ok := svc.(workflowapi.WorkflowService)
	assert.True(t, ok, "New 返回值实现 api 接口（组合根注入 handler / 未来执行器）")
}

// ---- T2：转换函数（纯函数表驱动）----

func TestToModel(t *testing.T) {
	cond := "true"
	req := workflowapi.UpsertReq{
		Name:         "智能客服分流",
		Description:  "意图识别 → 分支",
		Type:         workflowapi.WorkflowTypeChat,
		StartNodeKey: "classify",
		Nodes: []workflowapi.NodeReq{
			{Key: "classify", Type: workflowapi.NodeLLM, Name: "意图识别", Config: json.RawMessage(`{"model_id":"3","prompt":"判断意图"}`)},
			{Key: "router", Type: workflowapi.NodeCondition, Config: json.RawMessage(`{"expression":"{{classify}} == 'ORDER_QUERY'"}`)},
		},
		Edges: []workflowapi.EdgeReq{
			{SourceNodeKey: "classify", TargetNodeKey: "router"},
			{SourceNodeKey: "router", TargetNodeKey: "order_api", Condition: &cond},
		},
	}

	wf, nodes, edges, err := toModel(req)
	require.NoError(t, err)

	assert.Equal(t, "智能客服分流", wf.Name)
	assert.Equal(t, "意图识别 → 分支", wf.Description)
	assert.Equal(t, "classify", wf.StartNodeKey)
	assert.Equal(t, "draft", wf.Status, "Status 恒 draft（服务端定，不看请求体）")
	assert.Equal(t, "chat", wf.Type, "分型随请求落 model（spec 08）")
	assert.Nil(t, wf.InputSchema, "chat 型 / 未声明 schema → NULL")
	assert.Nil(t, wf.OutputSchema)

	assert.Len(t, nodes, 2)
	assert.Equal(t, "classify", nodes[0].NodeKey)
	assert.Equal(t, "llm", nodes[0].Type)
	assert.Equal(t, "意图识别", nodes[0].Name)
	assert.Equal(t, `{"model_id":"3","prompt":"判断意图"}`, nodes[0].Config, "config JSON 原文直存")
	assert.Zero(t, nodes[0].WorkflowID, "FK 由 store 事务内回填，组装期不填")

	assert.Len(t, edges, 2)
	assert.Nil(t, edges[0].Condition, "无条件 → nil")
	assert.NotNil(t, edges[1].Condition)
	assert.Equal(t, "true", *edges[1].Condition, "Condition 指针透传（区分没传与空串）")
}

func TestToModelEmptyEdges(t *testing.T) {
	_, _, edges, err := toModel(workflowapi.UpsertReq{
		Name:         "纯线性图",
		Type:         workflowapi.WorkflowTypeChat,
		StartNodeKey: "a",
		Nodes:        []workflowapi.NodeReq{{Key: "a", Type: workflowapi.NodeLLM, Config: json.RawMessage(`{"model_id":"1","prompt":"p"}`)}},
		Edges:        []workflowapi.EdgeReq{},
	})
	require.NoError(t, err)
	assert.NotNil(t, edges, "空 edges → 空切片兜底（store 据此跳过该语句）")
	assert.Empty(t, edges)
}

func TestToSchemas(t *testing.T) {
	now := time.Now()
	cond := "true"
	wf := &Workflow{Name: "智能客服分流", Description: "意图识别 → 分支", StartNodeKey: "classify", Status: "published",
		Type:        "chat",
		InputSchema: strPtr(`[{"name":"query","type":"string","required":true}]`)}
	wf.ID, wf.CreatedAt, wf.UpdatedAt = 42, now, now

	sum, err := toSummarySchema(wf)
	require.NoError(t, err)
	assert.Equal(t, "42", sum.ID, "ID 字符串化")
	assert.Equal(t, "智能客服分流", sum.Name)
	assert.Equal(t, "published", sum.Status)
	assert.Equal(t, now, sum.CreatedAt)
	assert.Equal(t, now, sum.UpdatedAt)
	assert.Equal(t, "chat", sum.Type, "分型随摘要暴露（spec 08）")
	assert.Equal(t, []workflowapi.SchemaField{{Name: "query", Type: "string", Required: true}}, sum.InputSchema,
		"jsonb 文本 → api schema 往返")
	assert.Nil(t, sum.OutputSchema, "NULL → nil（JSON null）")

	nodes := []WorkflowNode{
		{NodeKey: "classify", Type: "llm", Name: "意图识别", Config: `{"model_id":"3","prompt":"判断意图"}`},
	}
	edges := []WorkflowEdge{
		{SourceNodeKey: "classify", TargetNodeKey: "router", Condition: &cond},
	}
	detail, err := toDetailSchema(wf, nodes, edges)
	require.NoError(t, err)
	assert.Equal(t, "42", detail.ID)
	assert.Equal(t, "classify", detail.StartNodeKey)
	assert.Len(t, detail.Nodes, 1)
	assert.Equal(t, "llm", detail.Nodes[0].Type)
	assert.Equal(t, json.RawMessage(`{"model_id":"3","prompt":"判断意图"}`), detail.Nodes[0].Config, "config 原样透传")
	assert.Len(t, detail.Edges, 1)
	assert.Equal(t, "router", detail.Edges[0].TargetNodeKey)
	assert.Equal(t, "true", *detail.Edges[0].Condition)

	empty, err := toDetailSchema(wf, nil, nil)
	require.NoError(t, err)
	assert.NotNil(t, empty.Nodes, "空节点 → [] 不 null（接口规范空值约定）")
	assert.NotNil(t, empty.Edges)
	assert.Empty(t, empty.Nodes)
	assert.Empty(t, empty.Edges)
}

// ---- T3：Create（条 9 预检 + 23505 翻译 + 组装返回）----

// llmUpsertReq 含 llm 节点的合法创建请求（model_id=3，条 9 预检走 provider 分支）。
// Type=chat：spec 08 起 Create 必填分型，存量语义即 chat。
func llmUpsertReq() workflowapi.UpsertReq {
	return workflowapi.UpsertReq{
		Name:         "智能客服分流",
		Description:  "意图识别 → 分支",
		Type:         workflowapi.WorkflowTypeChat,
		StartNodeKey: "classify",
		Nodes: []workflowapi.NodeReq{
			{Key: "classify", Type: workflowapi.NodeLLM, Name: "意图识别", Config: json.RawMessage(`{"model_id":"3","prompt":"判断意图"}`)},
		},
		Edges: []workflowapi.EdgeReq{},
	}
}

func TestCreate(t *testing.T) {
	st := &stubStore{
		createFn: func(wf *Workflow, nodes []WorkflowNode, edges []WorkflowEdge) error {
			wf.ID = 42 // 模拟 RETURNING 回填
			assert.Equal(t, "draft", wf.Status)
			assert.Equal(t, "llm", nodes[0].Type)
			assert.Equal(t, `{"model_id":"3","prompt":"判断意图"}`, nodes[0].Config, "Config 原文落 model")
			return nil
		},
		getByIDFn: func(id uint64) (*Workflow, error) {
			wf := &Workflow{Name: "智能客服分流", Description: "意图识别 → 分支", StartNodeKey: "classify", Status: "draft"}
			wf.ID = id
			return wf, nil
		},
		listNodesFn: func(workflowID uint64) ([]WorkflowNode, error) {
			return []WorkflowNode{{NodeKey: "classify", Type: "llm", Name: "意图识别", Config: `{"model_id":"3","prompt":"判断意图"}`}}, nil
		},
		listEdgesFn: func(workflowID uint64) ([]WorkflowEdge, error) { return nil, nil },
	}
	cm := &recordCache{}
	models := &stubModels{getFn: func(req providerapi.GetModelReq) (*providerapi.ModelSchema, error) {
		assert.Equal(t, uint64(3), req.ID, "llm 节点 model_id 预检")
		return &providerapi.ModelSchema{}, nil
	}}
	svc := New(st, models, nil, cm, nil, nil, nil, false)

	d, err := svc.Create(context.Background(), llmUpsertReq())
	assert.NoError(t, err)
	assert.Equal(t, "42", d.ID)
	assert.Equal(t, "draft", d.Status)
	assert.Equal(t, "classify", d.StartNodeKey)
	assert.Len(t, d.Nodes, 1, "组装回读 round-trip")
	assert.NotNil(t, d.Edges, "nil edges → 组装兜底空切片")
	assert.Empty(t, cm.seq, "写路径不预热缓存（不读不写）")
	assert.Equal(t, []string{"create", "getByID", "listNodes", "listEdges"}, st.seq)
}

func TestCreateModelPrecheckFail(t *testing.T) {
	st := &stubStore{}
	models := &stubModels{getFn: func(req providerapi.GetModelReq) (*providerapi.ModelSchema, error) {
		return nil, providerapi.ErrModelNotFound
	}}
	svc := New(st, models, nil, &recordCache{}, nil, nil, nil, false)

	_, err := svc.Create(context.Background(), llmUpsertReq())
	assert.ErrorIs(t, err, errs.ErrValidationFailed, "预检 404 翻译 VALIDATION_FAILED")
	assert.Contains(t, err.Error(), `node "classify"`, "文案带节点 key 定位")
	assert.Contains(t, err.Error(), "model_id 3")
	assert.Empty(t, st.seq, "预检失败不动 store")
}

func TestCreateKBPrecheckFail(t *testing.T) {
	st := &stubStore{}
	kbs := &stubKbs{getFn: func(req ragapi.GetKnowledgeBaseReq) (*ragapi.KnowledgeBaseSchema, error) {
		return nil, ragapi.ErrKnowledgeBaseNotFound
	}}
	svc := New(st, nil, kbs, &recordCache{}, nil, nil, nil, false)

	req := workflowapi.UpsertReq{
		Name:         "知识问答",
		Type:         workflowapi.WorkflowTypeChat,
		StartNodeKey: "retrieve",
		Nodes: []workflowapi.NodeReq{
			{Key: "retrieve", Type: workflowapi.NodeKnowledgeRetrieval, Config: json.RawMessage(`{"knowledge_base_id":"7","top_k":5}`)},
		},
		Edges: []workflowapi.EdgeReq{},
	}
	_, err := svc.Create(context.Background(), req)
	assert.ErrorIs(t, err, errs.ErrValidationFailed, "KB 预检 404 翻译 VALIDATION_FAILED")
	assert.Contains(t, err.Error(), `node "retrieve"`)
	assert.Contains(t, err.Error(), "knowledge_base_id 7")
	assert.Empty(t, st.seq, "预检失败不动 store")
}

func TestCreateNameConflict(t *testing.T) {
	st := &stubStore{createFn: func(wf *Workflow, nodes []WorkflowNode, edges []WorkflowEdge) error {
		return &pgconn.PgError{Code: "23505", ConstraintName: "uq_workflows_name"}
	}}
	svc := New(st, &stubModels{getFn: func(providerapi.GetModelReq) (*providerapi.ModelSchema, error) {
		return &providerapi.ModelSchema{}, nil
	}}, nil, &recordCache{}, nil, nil, nil, false)

	_, err := svc.Create(context.Background(), llmUpsertReq())
	assert.ErrorIs(t, err, workflowapi.ErrWorkflowNameConflict, "23505 → 409 哨兵")
}

func TestCreateStoreError(t *testing.T) {
	boom := errors.New("insert failed")
	st := &stubStore{createFn: func(wf *Workflow, nodes []WorkflowNode, edges []WorkflowEdge) error {
		return boom
	}}
	svc := New(st, &stubModels{getFn: func(providerapi.GetModelReq) (*providerapi.ModelSchema, error) {
		return &providerapi.ModelSchema{}, nil
	}}, nil, &recordCache{}, nil, nil, nil, false)

	_, err := svc.Create(context.Background(), llmUpsertReq())
	assert.ErrorIs(t, err, boom, "其余错误 %w 包装上抛（handler 500）")
}

// ---- T026：R10 保存期模板引用校验（spec 06 FR3 / db_model §7 条 11）----

// r10UpsertReq 覆盖全部模板字段位的合法引用图：classify(llm) → router(condition) →
// order_api(api) → final(end)，各字段只引 input / 祖先。表驱动用例在其上覆写单字段
// 构造违例变体。
func r10UpsertReq() workflowapi.UpsertReq {
	cond := "true"
	return workflowapi.UpsertReq{
		Name:         "查单流程",
		Type:         workflowapi.WorkflowTypeChat,
		StartNodeKey: "classify",
		Nodes: []workflowapi.NodeReq{
			{Key: "classify", Type: workflowapi.NodeLLM, Name: "意图识别",
				Config: json.RawMessage(`{"model_id":"3","prompt":"判断意图：{{input}}"}`)},
			{Key: "router", Type: workflowapi.NodeCondition, Name: "意图分流",
				Config: json.RawMessage(`{"expression":"{{classify}} == 'ORDER_QUERY'"}`)},
			{Key: "order_api", Type: workflowapi.NodeAPI, Name: "查单接口",
				Config: json.RawMessage(`{"url":"https://api.example.com/orders?q={{classify}}","method":"GET","headers":{"Authorization":"Bearer {{classify}}","X-Route":"{{router}}"},"body":"{\"q\":\"{{classify}}\",\"raw\":\"{{input}}\"}"}`)},
			{Key: "final", Type: workflowapi.NodeEnd, Name: "终稿",
				Config: json.RawMessage(`{"output":"查单结果：{{order_api}}"}`)},
		},
		Edges: []workflowapi.EdgeReq{
			{SourceNodeKey: "classify", TargetNodeKey: "router"},
			{SourceNodeKey: "router", TargetNodeKey: "order_api", Condition: &cond},
			{SourceNodeKey: "order_api", TargetNodeKey: "final"},
		},
	}
}

// overrideNodeConfig 覆写指定节点的 config 并返回 req（就地改 Nodes 的共享底层数组，
// 调用方须传入新建的图——表驱动每例都经 r10UpsertReq() 现建）。
func overrideNodeConfig(req workflowapi.UpsertReq, key, config string) workflowapi.UpsertReq {
	for i := range req.Nodes {
		if req.Nodes[i].Key == key {
			req.Nodes[i].Config = json.RawMessage(config)
		}
	}
	return req
}

// createOkStore Create 全链成功的 store stub（R10 缺失的 RED 阶段 Create 会走完
// store 路径，预填避免 nil fn panic）。
func createOkStore() *stubStore {
	return &stubStore{
		createFn:  func(wf *Workflow, nodes []WorkflowNode, edges []WorkflowEdge) error { wf.ID = 42; return nil },
		getByIDFn: func(id uint64) (*Workflow, error) {
			wf := &Workflow{Name: "查单流程", StartNodeKey: "classify", Status: "draft"}
			wf.ID = id
			return wf, nil
		},
		listNodesFn: func(workflowID uint64) ([]WorkflowNode, error) { return nil, nil },
		listEdgesFn: func(workflowID uint64) ([]WorkflowEdge, error) { return nil, nil },
	}
}

// 全字段位引用 input / 祖先 → 通过（照常落 store）。
func TestCreateTemplateRefsPass(t *testing.T) {
	st := createOkStore()
	svc := New(st, okModels(), nil, &recordCache{}, nil, nil, nil, false)

	d, err := svc.Create(context.Background(), r10UpsertReq())
	assert.NoError(t, err)
	assert.Equal(t, "42", d.ID)
	assert.Equal(t, []string{"create", "getByID", "listNodes", "listEdges"}, st.seq)
}

// 引用非祖先 / 未知 key → 400 VALIDATION_FAILED（details 带节点 key 与引用名），
// 不动 store（R10 纯内存校验先于条 9 IO 预检）。
func TestCreateTemplateRefsReject(t *testing.T) {
	tests := []struct {
		name   string
		key    string // 覆写节点
		config string // 覆写 config（含一个非祖先引用）
		ref    string // 期望报错携带的引用名
	}{
		{"llm.prompt 引用下游节点", "classify", `{"model_id":"3","prompt":"分类：{{final}}"}`, "final"},
		{"api.url 引用下游节点", "order_api", `{"url":"https://api.example.com/{{final}}","method":"GET","headers":{},"body":""}`, "final"},
		{"api.headers 值引用下游节点", "order_api", `{"url":"https://api.example.com/o","method":"GET","headers":{"Authorization":"{{final}}"},"body":""}`, "final"},
		{"api.body 引用下游节点", "order_api", `{"url":"https://api.example.com/o","method":"GET","headers":{},"body":"{\"x\":\"{{final}}\"}"}`, "final"},
		{"end.output 引用错字 key", "final", `{"output":"结果：{{clasify}}"}`, "clasify"},
		{"condition 左侧引用下游节点", "router", `{"expression":"{{final}} == 'ORDER_QUERY'"}`, "final"},
		{"引用名含空格不剥离（与执行期 render 同 tokenizer）", "classify", `{"model_id":"3","prompt":"{{ classify }}"}`, " classify "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := createOkStore()
			svc := New(st, okModels(), nil, &recordCache{}, nil, nil, nil, false)

			_, err := svc.Create(context.Background(), overrideNodeConfig(r10UpsertReq(), tt.key, tt.config))
			require.ErrorIs(t, err, errs.ErrValidationFailed, "非祖先/未知引用 → 400 VALIDATION_FAILED")
			assert.Contains(t, err.Error(), fmt.Sprintf("%q", tt.key), "details 带节点 key")
			assert.Contains(t, err.Error(), fmt.Sprintf("%q", tt.ref), "details 带引用名")
			assert.Empty(t, st.seq, "R10 拒绝不动 store")
		})
	}
}

// 基名判定（spec 08 FR9）：点分引用 {{node.field}} 按点号前基名过 R10——祖先基名
// 下钻过、错字 / 下游基名拒（报错带完整点分名）；字段名留运行期 strict。纯函数直测，
// 零 IO。
func TestValidateTemplateRefsBaseName(t *testing.T) {
	llmCfg := func(prompt string) workflowapi.NodeReq {
		return workflowapi.NodeReq{Key: "classify", Type: workflowapi.NodeLLM,
			Config: json.RawMessage(fmt.Sprintf(`{"model_id":"3","prompt":%q}`, prompt))}
	}
	endCfg := func(output string) workflowapi.NodeReq {
		return workflowapi.NodeReq{Key: "final", Type: workflowapi.NodeEnd,
			Config: json.RawMessage(fmt.Sprintf(`{"output":%q}`, output))}
	}
	graph := func(classify, final workflowapi.NodeReq) ([]workflowapi.NodeReq, []workflowapi.EdgeReq) {
		return []workflowapi.NodeReq{classify, final},
			[]workflowapi.EdgeReq{{SourceNodeKey: "classify", TargetNodeKey: "final"}}
	}

	t.Run("祖先基名下钻过", func(t *testing.T) {
		nodes, edges := graph(llmCfg("意图"), endCfg("城市：{{classify.city}}"))
		assert.NoError(t, validateTemplateRefs(nodes, edges))
	})
	t.Run("input 基名下钻过（字段名留运行期 strict）", func(t *testing.T) {
		nodes, edges := graph(llmCfg("城市：{{input.city}}"), endCfg("终稿"))
		assert.NoError(t, validateTemplateRefs(nodes, edges))
	})
	t.Run("错字基名拒（报错带完整点分名）", func(t *testing.T) {
		nodes, edges := graph(llmCfg("意图"), endCfg("城市：{{clasify.city}}"))
		err := validateTemplateRefs(nodes, edges)
		require.ErrorIs(t, err, errs.ErrValidationFailed)
		assert.Contains(t, err.Error(), "clasify.city")
	})
	t.Run("下游基名拒", func(t *testing.T) {
		nodes, edges := graph(llmCfg("预取：{{final.city}}"), endCfg("终稿"))
		err := validateTemplateRefs(nodes, edges)
		require.ErrorIs(t, err, errs.ErrValidationFailed)
		assert.Contains(t, err.Error(), "final.city")
	})
}

// condition 比较式右侧 'literal' 是字面量非引用、不查：右侧占位符形态（下游 key）
// 也不当作引用（与执行期 evalCondition 同一切分规则）。
func TestCreateConditionLiteralNotScanned(t *testing.T) {
	st := createOkStore()
	svc := New(st, okModels(), nil, &recordCache{}, nil, nil, nil, false)

	req := overrideNodeConfig(r10UpsertReq(), "router", `{"expression":"{{classify}} == '{{order_api}}'"}`)
	_, err := svc.Create(context.Background(), req)
	assert.NoError(t, err, "右侧 '{{order_api}}' 是字面量的一部分，不查")
	assert.Equal(t, []string{"create", "getByID", "listNodes", "listEdges"}, st.seq)
}

// 纯线性图祖先链正确：a(llm) → b(llm) → c(end)——b 可引 a（直接祖先）、c 可引 a
//（跨两跳祖先）；反向 a 引 b（下游）拒。
func TestCreateLinearAncestors(t *testing.T) {
	llmNode := func(key, prompt string) workflowapi.NodeReq {
		return workflowapi.NodeReq{Key: key, Type: workflowapi.NodeLLM,
			Config: json.RawMessage(fmt.Sprintf(`{"model_id":"3","prompt":%q}`, prompt))}
	}
	endNode := func(output string) workflowapi.NodeReq {
		return workflowapi.NodeReq{Key: "c", Type: workflowapi.NodeEnd,
			Config: json.RawMessage(fmt.Sprintf(`{"output":%q}`, output))}
	}
	linearReq := func(promptA, promptB, outputC string) workflowapi.UpsertReq {
		return workflowapi.UpsertReq{
			Name: "线性链", Type: workflowapi.WorkflowTypeChat, StartNodeKey: "a",
			Nodes: []workflowapi.NodeReq{llmNode("a", promptA), llmNode("b", promptB), endNode(outputC)},
			Edges: []workflowapi.EdgeReq{
				{SourceNodeKey: "a", TargetNodeKey: "b"},
				{SourceNodeKey: "b", TargetNodeKey: "c"},
			},
		}
	}

	t.Run("下游引用祖先链（含跨两跳）通过", func(t *testing.T) {
		st := createOkStore()
		svc := New(st, okModels(), nil, &recordCache{}, nil, nil, nil, false)

		_, err := svc.Create(context.Background(), linearReq("首步", "细化：{{a}}", "终稿：{{a}}"))
		assert.NoError(t, err)
		assert.Equal(t, []string{"create", "getByID", "listNodes", "listEdges"}, st.seq)
	})
	t.Run("上游引用下游拒", func(t *testing.T) {
		st := createOkStore()
		svc := New(st, okModels(), nil, &recordCache{}, nil, nil, nil, false)

		_, err := svc.Create(context.Background(), linearReq("预取结果：{{b}}", "细化", "终稿"))
		require.ErrorIs(t, err, errs.ErrValidationFailed)
		assert.Contains(t, err.Error(), `"a"`, "details 带节点 key")
		assert.Contains(t, err.Error(), `"b"`, "details 带引用名")
		assert.Empty(t, st.seq)
	})
}

// 兄弟分支引用拒：分支 A 节点引用分支 B 节点——既非祖先也非 input（n8n 静默
// undefined 教训的核心场景：对侧分支未执行时变量必缺失）。
func TestCreateSiblingBranchRefReject(t *testing.T) {
	st := createOkStore()
	svc := New(st, okModels(), nil, &recordCache{}, nil, nil, nil, false)

	condFalse := "false"
	req := r10UpsertReq()
	req.Nodes = append(req.Nodes, workflowapi.NodeReq{Key: "notify", Type: workflowapi.NodeLLM,
		Config: json.RawMessage(`{"model_id":"3","prompt":"通知：{{classify}}"}`)})
	req.Edges = append(req.Edges, workflowapi.EdgeReq{SourceNodeKey: "router", TargetNodeKey: "notify", Condition: &condFalse})
	req.Nodes[3].Config = json.RawMessage(`{"output":"结果：{{notify}}"}`) // final 引用兄弟分支节点

	_, err := svc.Create(context.Background(), req)
	require.ErrorIs(t, err, errs.ErrValidationFailed)
	assert.Contains(t, err.Error(), `"final"`)
	assert.Contains(t, err.Error(), `"notify"`)
	assert.Empty(t, st.seq)
}

// Update 路径同校验：非祖先引用 → VALIDATION_FAILED，不动 store。
func TestUpdateTemplateRefsReject(t *testing.T) {
	st := createOkStore()
	svc := New(st, okModels(), nil, &recordCache{}, nil, nil, nil, false)

	req := overrideNodeConfig(r10UpsertReq(), "final", `{"output":"结果：{{clasify}}"}`)
	_, err := svc.Update(context.Background(), workflowapi.UpdateWorkflowReq{ID: 42, UpsertReq: req})
	require.ErrorIs(t, err, errs.ErrValidationFailed)
	assert.Contains(t, err.Error(), `"final"`)
	assert.Contains(t, err.Error(), `"clasify"`)
	assert.Empty(t, st.seq, "R10 拒绝不动 store（ReplaceGraph 未触达）")
}

// ---- T4：Get（Cache-Aside：命中 / 回源回填 / 404 / 容错）----

// detailStore 三查全命中返回 id=42 的 published 工作流（Get 回源断言用）。
func detailStore() *stubStore {
	return &stubStore{
		getByIDFn: func(id uint64) (*Workflow, error) {
			wf := &Workflow{Name: "智能客服分流", StartNodeKey: "classify", Status: "published"}
			wf.ID = id
			return wf, nil
		},
		listNodesFn: func(workflowID uint64) ([]WorkflowNode, error) {
			return []WorkflowNode{{NodeKey: "classify", Type: "llm", Config: `{"model_id":"3","prompt":"p"}`}}, nil
		},
		listEdgesFn: func(workflowID uint64) ([]WorkflowEdge, error) { return nil, nil },
	}
}

func TestGetCacheHit(t *testing.T) {
	st := &stubStore{}
	cached := &workflowapi.WorkflowDetailSchema{
		WorkflowSummarySchema: workflowapi.WorkflowSummarySchema{ID: "7", Status: "published"},
		StartNodeKey:          "classify",
	}
	cm := &recordCache{getFound: true, getVal: cached}
	svc := New(st, nil, nil, cm, nil, nil, nil, false)

	d, err := svc.Get(context.Background(), workflowapi.GetWorkflowReq{ID: 7})
	assert.NoError(t, err)
	assert.Equal(t, "7", d.ID)
	assert.Equal(t, "published", d.Status)
	assert.Empty(t, st.seq, "命中零 store 调用")
	assert.Equal(t, []string{"get:7"}, cm.seq, "只读不写")
}

func TestGetCacheMiss(t *testing.T) {
	st := detailStore()
	cm := &recordCache{}
	svc := New(st, nil, nil, cm, nil, nil, nil, false)

	d, err := svc.Get(context.Background(), workflowapi.GetWorkflowReq{ID: 42})
	assert.NoError(t, err)
	assert.Equal(t, "42", d.ID)
	assert.Len(t, d.Nodes, 1)
	assert.Equal(t, []string{"getByID", "listNodes", "listEdges"}, st.seq, "未命中三查回源")
	assert.Equal(t, []string{"get:42", "set:42"}, cm.seq, "回源后回填")
}

func TestGetNotFound(t *testing.T) {
	st := &stubStore{getByIDFn: func(id uint64) (*Workflow, error) {
		return nil, gorm.ErrRecordNotFound
	}}
	cm := &recordCache{}
	svc := New(st, nil, nil, cm, nil, nil, nil, false)

	_, err := svc.Get(context.Background(), workflowapi.GetWorkflowReq{ID: 999})
	assert.ErrorIs(t, err, workflowapi.ErrWorkflowNotFound, "404 翻译")
	assert.Equal(t, []string{"get:999"}, cm.seq, "404 不回填")
}

func TestGetCacheReadErrFallback(t *testing.T) {
	st := detailStore()
	cm := &recordCache{getErr: errors.New("redis down")}
	svc := New(st, nil, nil, cm, nil, nil, nil, false)

	d, err := svc.Get(context.Background(), workflowapi.GetWorkflowReq{ID: 42})
	assert.NoError(t, err, "缓存读失败视为 miss 回源（agent Get 同款）")
	assert.Equal(t, "42", d.ID)
	assert.Equal(t, []string{"getByID", "listNodes", "listEdges"}, st.seq)
}

func TestGetCacheSetErrNonFatal(t *testing.T) {
	st := detailStore()
	cm := &recordCache{setErr: errors.New("redis down")}
	svc := New(st, nil, nil, cm, nil, nil, nil, false)

	d, err := svc.Get(context.Background(), workflowapi.GetWorkflowReq{ID: 42})
	assert.NoError(t, err, "回填失败仅 WARN 不影响业务")
	assert.Equal(t, "42", d.ID)
	assert.Equal(t, []string{"get:42", "set:42"}, cm.seq)
}

// ---- T5：List（分页归一化 + 摘要组装）----

func TestListNormalize(t *testing.T) {
	tests := []struct {
		name             string
		page, pageSize   int
		wantOffset       int
		wantPage, wantPS int
	}{
		{"零值缺省 page=1/pageSize=20", 0, 0, 0, 1, 20},
		{"page<1 归一", -3, 10, 0, 1, 10},
		{"常规第 3 页", 3, 10, 20, 3, 10},
		{"pageSize>100 封顶", 1, 200, 0, 1, 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotOffset, gotLimit int
			st := &stubStore{listFn: func(offset, limit int) ([]Workflow, int64, error) {
				gotOffset, gotLimit = offset, limit
				return nil, 0, nil
			}}
			svc := New(st, nil, nil, &recordCache{}, nil, nil, nil, false)

			res, err := svc.List(context.Background(), workflowapi.ListWorkflowsReq{Page: tt.page, PageSize: tt.pageSize})
			assert.NoError(t, err)
			assert.Equal(t, tt.wantOffset, gotOffset)
			assert.Equal(t, tt.wantPS, gotLimit)
			assert.Equal(t, tt.wantPage, res.Page)
			assert.Equal(t, tt.wantPS, res.PageSize)
			assert.NotNil(t, res.Items, "空页 → [] 不 null")
		})
	}
}

func TestListAssemble(t *testing.T) {
	now := time.Now()
	st := &stubStore{listFn: func(offset, limit int) ([]Workflow, int64, error) {
		wf1 := &Workflow{Name: "查单流程", Status: "published"}
		wf1.ID, wf1.CreatedAt, wf1.UpdatedAt = 2, now, now
		wf2 := &Workflow{Name: "智能客服分流", Status: "draft"}
		wf2.ID, wf2.CreatedAt, wf2.UpdatedAt = 1, now, now
		return []Workflow{*wf1, *wf2}, 2, nil
	}}
	svc := New(st, nil, nil, &recordCache{}, nil, nil, nil, false)

	res, err := svc.List(context.Background(), workflowapi.ListWorkflowsReq{Page: 1, PageSize: 20})
	assert.NoError(t, err)
	assert.Equal(t, int64(2), res.Total)
	assert.Len(t, res.Items, 2)
	assert.Equal(t, "2", res.Items[0].ID, "ID 字符串化")
	assert.Equal(t, "published", res.Items[0].Status)
	assert.Equal(t, "draft", res.Items[1].Status)
}

func TestListError(t *testing.T) {
	boom := errors.New("count failed")
	st := &stubStore{listFn: func(offset, limit int) ([]Workflow, int64, error) { return nil, 0, boom }}
	svc := New(st, nil, nil, &recordCache{}, nil, nil, nil, false)

	_, err := svc.List(context.Background(), workflowapi.ListWorkflowsReq{})
	assert.ErrorIs(t, err, boom)
}

// ---- T6：Update（预检 + 整图替换 + Del key）----

// okModels 恒通过预检的 provider stub。
func okModels() *stubModels {
	return &stubModels{getFn: func(req providerapi.GetModelReq) (*providerapi.ModelSchema, error) {
		return &providerapi.ModelSchema{}, nil
	}}
}

func TestUpdate(t *testing.T) {
	var gotWf *Workflow
	st := &stubStore{
		replaceGraphFn: func(wf *Workflow, nodes []WorkflowNode, edges []WorkflowEdge) (bool, error) {
			gotWf = wf
			return true, nil
		},
		getByIDFn: func(id uint64) (*Workflow, error) {
			wf := &Workflow{Name: "智能客服分流改", StartNodeKey: "classify", Status: "disabled"}
			wf.ID = id
			return wf, nil
		},
		listNodesFn: func(workflowID uint64) ([]WorkflowNode, error) { return nil, nil },
		listEdgesFn: func(workflowID uint64) ([]WorkflowEdge, error) { return nil, nil },
	}
	cm := &recordCache{}
	svc := New(st, okModels(), nil, cm, nil, nil, nil, false)

	d, err := svc.Update(context.Background(), workflowapi.UpdateWorkflowReq{ID: 42, UpsertReq: llmUpsertReq()})
	assert.NoError(t, err)
	assert.Equal(t, uint64(42), gotWf.ID, "路径 id 赋入替换行")
	assert.Equal(t, "42", d.ID)
	assert.Equal(t, "disabled", d.Status, "status 由库回读（编辑不降级，不触碰）")
	assert.Equal(t, []string{"replaceGraph", "getByID", "listNodes", "listEdges"}, st.seq)
	assert.Equal(t, []string{"del:42"}, cm.seq, "成功后 Del key")
}

func TestUpdatePrecheckFail(t *testing.T) {
	st := &stubStore{}
	models := &stubModels{getFn: func(req providerapi.GetModelReq) (*providerapi.ModelSchema, error) {
		return nil, providerapi.ErrModelNotFound
	}}
	svc := New(st, models, nil, &recordCache{}, nil, nil, nil, false)

	_, err := svc.Update(context.Background(), workflowapi.UpdateWorkflowReq{ID: 42, UpsertReq: llmUpsertReq()})
	assert.ErrorIs(t, err, errs.ErrValidationFailed)
	assert.Empty(t, st.seq, "预检失败不动 store")
}

// TestUpdateTypeImmutable Update 携带 type 即拒（spec 08 §4.1，clarify 拍板：同值 /
// 异值均拒，不比对当前值——分型不可变，换型 = 删了重建）。拒在一切预检前，零 store 访问
//（防绕过 handler 直调 service 的调用方）。
func TestUpdateTypeImmutable(t *testing.T) {
	st := &stubStore{}
	svc := New(st, okModels(), nil, &recordCache{}, nil, nil, nil, false)

	for _, v := range []string{"chat", "task"} {
		typ := v
		_, err := svc.Update(context.Background(), workflowapi.UpdateWorkflowReq{ID: 42, UpsertReq: llmUpsertReq(), Type: &typ})
		assert.ErrorIs(t, err, errs.ErrValidationFailed, "携带 type=%s 即拒（不比对当前值）", typ)
	}
	assert.Empty(t, st.seq, "拒改不动 store")
}

func TestUpdateNotFound(t *testing.T) {
	st := &stubStore{replaceGraphFn: func(wf *Workflow, nodes []WorkflowNode, edges []WorkflowEdge) (bool, error) {
		return false, nil
	}}
	cm := &recordCache{}
	svc := New(st, okModels(), nil, cm, nil, nil, nil, false)

	_, err := svc.Update(context.Background(), workflowapi.UpdateWorkflowReq{ID: 999, UpsertReq: llmUpsertReq()})
	assert.ErrorIs(t, err, workflowapi.ErrWorkflowNotFound, "affected=0 → 404")
	assert.Empty(t, cm.seq, "404 不删 key")
}

func TestUpdateNameConflict(t *testing.T) {
	st := &stubStore{replaceGraphFn: func(wf *Workflow, nodes []WorkflowNode, edges []WorkflowEdge) (bool, error) {
		return false, &pgconn.PgError{Code: "23505", ConstraintName: "uq_workflows_name"}
	}}
	svc := New(st, okModels(), nil, &recordCache{}, nil, nil, nil, false)

	_, err := svc.Update(context.Background(), workflowapi.UpdateWorkflowReq{ID: 42, UpsertReq: llmUpsertReq()})
	assert.ErrorIs(t, err, workflowapi.ErrWorkflowNameConflict, "改名撞 uq → 409")
}

func TestUpdateStoreError(t *testing.T) {
	boom := errors.New("replace failed")
	st := &stubStore{replaceGraphFn: func(wf *Workflow, nodes []WorkflowNode, edges []WorkflowEdge) (bool, error) {
		return false, boom
	}}
	svc := New(st, okModels(), nil, &recordCache{}, nil, nil, nil, false)

	_, err := svc.Update(context.Background(), workflowapi.UpdateWorkflowReq{ID: 42, UpsertReq: llmUpsertReq()})
	assert.ErrorIs(t, err, boom)
}

// ---- T7：Delete + Publish + Disable（幂等三态）----

func TestDelete(t *testing.T) {
	st := &stubStore{deleteFn: func(id uint64) (bool, error) { return true, nil }}
	cm := &recordCache{}
	svc := New(st, nil, nil, cm, nil, nil, nil, false)

	err := svc.Delete(context.Background(), workflowapi.DeleteWorkflowReq{ID: 42})
	assert.NoError(t, err)
	assert.Equal(t, []string{"del:42"}, cm.seq, "成功后 Del key")
}

func TestDeleteNotFound(t *testing.T) {
	st := &stubStore{deleteFn: func(id uint64) (bool, error) { return false, nil }}
	cm := &recordCache{}
	svc := New(st, nil, nil, cm, nil, nil, nil, false)

	err := svc.Delete(context.Background(), workflowapi.DeleteWorkflowReq{ID: 999})
	assert.ErrorIs(t, err, workflowapi.ErrWorkflowNotFound)
	assert.Empty(t, cm.seq, "404 不删 key")
}

func TestDeleteError(t *testing.T) {
	boom := errors.New("delete failed")
	st := &stubStore{deleteFn: func(id uint64) (bool, error) { return false, boom }}
	svc := New(st, nil, nil, &recordCache{}, nil, nil, nil, false)

	err := svc.Delete(context.Background(), workflowapi.DeleteWorkflowReq{ID: 42})
	assert.ErrorIs(t, err, boom)
}

func TestDeleteInUse(t *testing.T) {
	// spec 05 US3：被 agent 绑定的 workflow 删除被 fk_agents_workflow RESTRICT 挡下
	//（23503）→ ErrWorkflowInUse 409；DB 未变，不删缓存 key。
	st := &stubStore{deleteFn: func(id uint64) (bool, error) {
		return false, &pgconn.PgError{Code: "23503", ConstraintName: "fk_agents_workflow"}
	}}
	cm := &recordCache{}
	svc := New(st, nil, nil, cm, nil, nil, nil, false)

	err := svc.Delete(context.Background(), workflowapi.DeleteWorkflowReq{ID: 42})
	assert.ErrorIs(t, err, workflowapi.ErrWorkflowInUse, "agents FK RESTRICT 23503 → 409 挡删")
	assert.Empty(t, cm.seq, "删除被挡 DB 未变，不删 key")
}

// statusStore 状态动作用 store：第 N 次 GetByID 返回 statuses[N-1]（首查 404 判定、
// 回源二次读各取一），UpdateStatus 恒 moved。
func statusStore(moved bool, statuses ...string) *stubStore {
	calls := 0
	return &stubStore{
		getByIDFn: func(id uint64) (*Workflow, error) {
			if calls >= len(statuses) {
				return nil, gorm.ErrRecordNotFound
			}
			status := statuses[calls]
			calls++
			wf := &Workflow{Name: "智能客服分流", StartNodeKey: "classify", Status: status}
			wf.ID = id
			return wf, nil
		},
		listNodesFn: func(workflowID uint64) ([]WorkflowNode, error) { return nil, nil },
		listEdgesFn: func(workflowID uint64) ([]WorkflowEdge, error) { return nil, nil },
		updateStatusFn: func(id uint64, from []string, to string) (bool, error) {
			return moved, nil
		},
	}
}

func TestPublish(t *testing.T) {
	st := statusStore(true, "draft", "published") // 首查 draft → 迁移 → 回源读 published
	cm := &recordCache{}
	svc := New(st, nil, nil, cm, nil, nil, nil, false)

	sum, err := svc.Publish(context.Background(), workflowapi.PublishWorkflowReq{ID: 7})
	assert.NoError(t, err)
	assert.Equal(t, "published", sum.Status, "回源取迁移后状态")
	assert.Equal(t, "7", sum.ID)
	assert.Equal(t, []string{"draft", "disabled"}, st.lastFrom, "from 排除目标态（严格幂等）")
	assert.Equal(t, "published", st.lastTo)
	assert.Equal(t, []string{"del:7", "get:7", "set:7"}, cm.seq, "Del → 回源 Get → 回填")
}

func TestPublishIdempotent(t *testing.T) {
	st := statusStore(false, "published", "published") // 已 published：0 行幂等
	cm := &recordCache{}
	svc := New(st, nil, nil, cm, nil, nil, nil, false)

	sum, err := svc.Publish(context.Background(), workflowapi.PublishWorkflowReq{ID: 7})
	assert.NoError(t, err, "已 published 幂等 200")
	assert.Equal(t, "published", sum.Status)
	assert.Equal(t, []string{"del:7", "get:7", "set:7"}, cm.seq, "无论 moved 与否 Del + 回源")
}

func TestPublishNotFound(t *testing.T) {
	st := &stubStore{getByIDFn: func(id uint64) (*Workflow, error) {
		return nil, gorm.ErrRecordNotFound
	}}
	cm := &recordCache{}
	svc := New(st, nil, nil, cm, nil, nil, nil, false)

	_, err := svc.Publish(context.Background(), workflowapi.PublishWorkflowReq{ID: 999})
	assert.ErrorIs(t, err, workflowapi.ErrWorkflowNotFound)
	assert.Equal(t, []string{"getByID"}, st.seq, "404 判定后不再迁移")
	assert.Empty(t, cm.seq)
}

func TestDisable(t *testing.T) {
	st := statusStore(true, "published", "disabled")
	cm := &recordCache{}
	svc := New(st, nil, nil, cm, nil, nil, nil, false)

	sum, err := svc.Disable(context.Background(), workflowapi.DisableWorkflowReq{ID: 7})
	assert.NoError(t, err)
	assert.Equal(t, "disabled", sum.Status)
	assert.Equal(t, []string{"published"}, st.lastFrom, "disable 仅 published 迁移")
	assert.Equal(t, "disabled", st.lastTo)
	assert.Equal(t, []string{"del:7", "get:7", "set:7"}, cm.seq)
}

func TestDisableDraftNoop(t *testing.T) {
	st := statusStore(false, "draft", "draft") // draft 不在 from：0 行且状态保持
	cm := &recordCache{}
	svc := New(st, nil, nil, cm, nil, nil, nil, false)

	sum, err := svc.Disable(context.Background(), workflowapi.DisableWorkflowReq{ID: 7})
	assert.NoError(t, err, "draft 幂等 no-op")
	assert.Equal(t, "draft", sum.Status, "状态保持 draft（回源取真实状态）")
}

func TestStatusActionUpdateError(t *testing.T) {
	boom := errors.New("update status failed")
	st := &stubStore{
		getByIDFn: func(id uint64) (*Workflow, error) {
			wf := &Workflow{Status: "draft"}
			wf.ID = id
			return wf, nil
		},
		updateStatusFn: func(id uint64, from []string, to string) (bool, error) { return false, boom },
	}
	svc := New(st, nil, nil, &recordCache{}, nil, nil, nil, false)

	_, err := svc.Publish(context.Background(), workflowapi.PublishWorkflowReq{ID: 7})
	assert.ErrorIs(t, err, boom)
}

// ---- spec 08 T005：分型 CRUD（type 必填 / chat 强不变量 / task schema 持久化回读）----

// strPtr 字符串取址（schema jsonb 文本 fixture 用）。
func strPtr(s string) *string { return &s }

func TestCreateTaskTypingRoundTrip(t *testing.T) {
	t.Run("task 型 schema 持久化与回读", func(t *testing.T) {
		var gotWf *Workflow
		var gotNodes []WorkflowNode
		st := &stubStore{
			createFn: func(wf *Workflow, nodes []WorkflowNode, edges []WorkflowEdge) error {
				gotWf, gotNodes = wf, nodes
				wf.ID = 42 // 模拟 RETURNING 回填
				return nil
			},
			getByIDFn: func(id uint64) (*Workflow, error) {
				wf := *gotWf // 回读返回落库行（ID 已回填）
				return &wf, nil
			},
			listNodesFn: func(workflowID uint64) ([]WorkflowNode, error) { return gotNodes, nil },
			listEdgesFn: func(workflowID uint64) ([]WorkflowEdge, error) { return nil, nil },
		}
		svc := New(st, okModels(), nil, &recordCache{}, nil, nil, nil, false)

		schema := []workflowapi.SchemaField{
			{Name: "query", Type: "string", Required: true, Description: "查询词"},
			{Name: "top", Type: "number"},
		}
		req := llmUpsertReq()
		req.Type = workflowapi.WorkflowTypeTask
		req.InputSchema = schema
		req.OutputSchema = schema[:1]

		d, err := svc.Create(context.Background(), req)
		require.NoError(t, err)

		assert.Equal(t, "task", gotWf.Type, "type 必填落库")
		require.NotNil(t, gotWf.InputSchema, "schema 序列化 jsonb 文本")
		assert.JSONEq(t, `[{"name":"query","type":"string","required":true,"description":"查询词"},{"name":"top","type":"number","required":false,"description":""}]`, *gotWf.InputSchema,
			"零值字段不省略（SchemaField 无 omitempty，同 Hify 空值约定）")
		require.NotNil(t, gotWf.OutputSchema)
		assert.JSONEq(t, `[{"name":"query","type":"string","required":true,"description":"查询词"}]`, *gotWf.OutputSchema)

		assert.Equal(t, "task", d.Type, "回读暴露分型")
		assert.Equal(t, schema, d.InputSchema, "jsonb 文本 → api schema 往返")
		assert.Equal(t, schema[:1], d.OutputSchema)
	})
	t.Run("chat 型空数组 schema 等价未声明", func(t *testing.T) {
		var gotWf *Workflow
		st := &stubStore{
			createFn: func(wf *Workflow, nodes []WorkflowNode, edges []WorkflowEdge) error {
				gotWf = wf
				wf.ID = 42
				return nil
			},
			getByIDFn:   func(id uint64) (*Workflow, error) { return gotWf, nil },
			listNodesFn: func(workflowID uint64) ([]WorkflowNode, error) { return nil, nil },
			listEdgesFn: func(workflowID uint64) ([]WorkflowEdge, error) { return nil, nil },
		}
		svc := New(st, okModels(), nil, &recordCache{}, nil, nil, nil, false)

		req := llmUpsertReq() // Type=chat
		req.InputSchema = []workflowapi.SchemaField{}

		_, err := svc.Create(context.Background(), req)
		require.NoError(t, err, "空 schema 与 nil 等价（len 0 → NULL），不触 chat 强不变量")
		assert.Nil(t, gotWf.InputSchema)
	})
}

func TestCreateTypingRejects(t *testing.T) {
	schema := []workflowapi.SchemaField{{Name: "query", Type: "string", Required: true}}
	tests := []struct {
		name   string
		mutate func(*workflowapi.UpsertReq)
		contains string
	}{
		{"type 缺失拒", func(r *workflowapi.UpsertReq) { r.Type = "" }, "type"},
		{"type 非法值拒", func(r *workflowapi.UpsertReq) { r.Type = "bogus" }, "type"},
		{"chat 型携带非空 input_schema 拒（强不变量）", func(r *workflowapi.UpsertReq) { r.InputSchema = schema }, "chat"},
		{"chat 型携带非空 output_schema 拒（强不变量）", func(r *workflowapi.UpsertReq) { r.OutputSchema = schema }, "chat"},
		{"task 型 schema 字段重名拒", func(r *workflowapi.UpsertReq) {
			r.Type = workflowapi.WorkflowTypeTask
			r.InputSchema = []workflowapi.SchemaField{{Name: "q", Type: "string"}, {Name: "q", Type: "number"}}
		}, "重名"},
		{"task 型 schema 非法 type 拒", func(r *workflowapi.UpsertReq) {
			r.Type = workflowapi.WorkflowTypeTask
			r.InputSchema = []workflowapi.SchemaField{{Name: "q", Type: "integer"}}
		}, "type"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := createOkStore()
			svc := New(st, okModels(), nil, &recordCache{}, nil, nil, nil, false)

			req := llmUpsertReq()
			tt.mutate(&req)
			_, err := svc.Create(context.Background(), req)
			require.ErrorIs(t, err, errs.ErrValidationFailed, "分型校验 → 400 VALIDATION_FAILED")
			assert.Contains(t, err.Error(), tt.contains)
			assert.NotContains(t, st.seq, "create", "校验失败不动 store")
		})
	}
}

// ---- spec 08 T006：R11 保存期嵌套矩阵（五拒两过）----

// taskWFRow 已存 task 型工作流行（inputSchema 空串 = 未声明 → NULL）。
func taskWFRow(id uint64, inputSchema string) *Workflow {
	wf := &Workflow{Name: "子任务", StartNodeKey: "only", Status: "published", Type: "task"}
	wf.ID = id
	if inputSchema != "" {
		wf.InputSchema = strPtr(inputSchema)
	}
	return wf
}

// chatWFRow 已存 chat 型工作流行（不可被嵌）。
func chatWFRow(id uint64) *Workflow {
	wf := &Workflow{Name: "聊天图", StartNodeKey: "only", Status: "published", Type: "chat"}
	wf.ID = id
	return wf
}

// llmNodeRow 终点节点行（无嵌套引用，DFS 链在此终止）。
func llmNodeRow(key string) WorkflowNode {
	return WorkflowNode{NodeKey: key, Type: "llm", Config: `{"model_id":"3","prompt":"p"}`}
}

// wfNodeRow 已存图里的 sub-workflow 节点行（config 指向 childID，inputs 为 raw JSON）。
func wfNodeRow(key string, childID uint64, inputs string) WorkflowNode {
	return WorkflowNode{NodeKey: key, Type: "workflow",
		Config: fmt.Sprintf(`{"workflow_id":%q,"inputs":%s}`, strconv.FormatUint(childID, 10), inputs)}
}

// r11Req 父图保存请求：单 workflow 节点引用 childID（inputs 为 raw JSON 映射文本）。
func r11Req(parentType workflowapi.WorkflowType, childID uint64, inputs string) workflowapi.UpsertReq {
	return workflowapi.UpsertReq{
		Name: "父图", Type: parentType, StartNodeKey: "call",
		Nodes: []workflowapi.NodeReq{{Key: "call", Type: workflowapi.NodeWorkflow,
			Config: json.RawMessage(fmt.Sprintf(`{"workflow_id":%q,"inputs":%s}`, strconv.FormatUint(childID, 10), inputs))}},
		Edges: []workflowapi.EdgeReq{},
	}
}

// r11Store R11 矩阵用 store：wfs / nodes 按 id 返回（未命中 404）；create / replaceGraph
// 恒成功（createFn 回填 ID=42，父图自身回读由 wfs[42] 预置）。
func r11Store(wfs map[uint64]*Workflow, nodes map[uint64][]WorkflowNode) *stubStore {
	return &stubStore{
		getByIDFn: func(id uint64) (*Workflow, error) {
			wf, ok := wfs[id]
			if !ok {
				return nil, gorm.ErrRecordNotFound
			}
			return wf, nil
		},
		listNodesFn:    func(workflowID uint64) ([]WorkflowNode, error) { return nodes[workflowID], nil },
		listEdgesFn:    func(workflowID uint64) ([]WorkflowEdge, error) { return nil, nil },
		createFn:       func(wf *Workflow, ns []WorkflowNode, es []WorkflowEdge) error { wf.ID = 42; return nil },
		replaceGraphFn: func(wf *Workflow, ns []WorkflowNode, es []WorkflowEdge) (bool, error) { return true, nil },
	}
}

// 两过：chat⊃task 与 task⊃task 均合法（嵌套矩阵只看被引方是 task；父型不限），
// 无 schema 子图回退恰 {input}。
func TestR11NestingPass(t *testing.T) {
	tests := []struct {
		name       string
		parentType workflowapi.WorkflowType
		child      *Workflow
		inputs     string
	}{
		{"chat⊃task 过（schema 全覆盖）", workflowapi.WorkflowTypeChat,
			taskWFRow(7, `[{"name":"query","type":"string","required":true}]`), `{"query":"{{input}}"}`},
		{"task⊃task 过（schema 全覆盖）", workflowapi.WorkflowTypeTask,
			taskWFRow(7, `[{"name":"query","type":"string","required":true},{"name":"top","type":"number"}]`), `{"query":"{{input}}","top":"3"}`},
		{"无 schema 子图恰 {input} 过（单一入参回退）", workflowapi.WorkflowTypeChat,
			taskWFRow(7, ""), `{"input":"{{input}}"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := r11Store(map[uint64]*Workflow{7: tt.child, 42: taskWFRow(42, "")},
				map[uint64][]WorkflowNode{7: {llmNodeRow("only")}})
			svc := New(st, nil, nil, &recordCache{}, nil, nil, nil, false)

			d, err := svc.Create(context.Background(), r11Req(tt.parentType, 7, tt.inputs))
			require.NoError(t, err)
			assert.Equal(t, "42", d.ID)
			assert.Contains(t, st.seq, "create", "合法嵌套照常落库")
		})
	}
}

// 五拒（Create 路径）：引 chat 型 / 引用不存在 / 缺 required / 多余字段 /
// 无 schema 非 {input} / 链深超 3——全部 400 VALIDATION_FAILED 且不动 store 写路径。
func TestR11NestingReject(t *testing.T) {
	querySchema := `[{"name":"query","type":"string","required":true},{"name":"top","type":"number"}]`
	tests := []struct {
		name     string
		wfs      map[uint64]*Workflow
		nodes    map[uint64][]WorkflowNode
		childID  uint64
		inputs   string
		contains string
	}{
		{"引用 chat 型拒", map[uint64]*Workflow{7: chatWFRow(7)}, nil, 7, `{"input":"{{input}}"}`, "task"},
		{"引用不存在拒", map[uint64]*Workflow{}, nil, 999, `{"input":"{{input}}"}`, "999"},
		{"inputs 缺 required 拒", map[uint64]*Workflow{7: taskWFRow(7, querySchema)}, nil, 7, `{"top":"3"}`, "query"},
		{"inputs 多余字段拒", map[uint64]*Workflow{7: taskWFRow(7, querySchema)}, nil, 7, `{"query":"{{input}}","extra":"x"}`, "extra"},
		{"无 schema 子图非 {input} 拒", map[uint64]*Workflow{7: taskWFRow(7, "")}, nil, 7, `{"query":"{{input}}"}`, "input"},
		{"链深超 3 拒（父→7→8→9）", map[uint64]*Workflow{7: taskWFRow(7, "")},
			map[uint64][]WorkflowNode{
				7: {wfNodeRow("c1", 8, `{"input":"{{input}}"}`)},
				8: {wfNodeRow("c2", 9, `{"input":"{{input}}"}`)},
			}, 7, `{"input":"{{input}}"}`, "深度"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := r11Store(tt.wfs, tt.nodes)
			svc := New(st, nil, nil, &recordCache{}, nil, nil, nil, false)

			_, err := svc.Create(context.Background(), r11Req(workflowapi.WorkflowTypeChat, tt.childID, tt.inputs))
			require.ErrorIs(t, err, errs.ErrValidationFailed, "R11 保存期拦截 → 400")
			assert.Contains(t, err.Error(), `"call"`, "错误带父节点 key 定位")
			assert.Contains(t, err.Error(), tt.contains)
			assert.NotContains(t, st.seq, "create", "R11 拒绝不动写路径")
		})
	}
}

// 自嵌拒（Update 路径）：保存图引用自身 id——长度 1 的环特例。
func TestR11SelfNestingReject(t *testing.T) {
	st := r11Store(map[uint64]*Workflow{42: taskWFRow(42, "")}, nil)
	svc := New(st, nil, nil, &recordCache{}, nil, nil, nil, false)

	_, err := svc.Update(context.Background(), workflowapi.UpdateWorkflowReq{
		ID: 42, UpsertReq: r11Req(workflowapi.WorkflowTypeTask, 42, `{"input":"{{input}}"}`),
	})
	require.ErrorIs(t, err, errs.ErrValidationFailed)
	assert.Contains(t, err.Error(), `"call"`)
	assert.Contains(t, err.Error(), "自嵌")
	assert.NotContains(t, st.seq, "replaceGraph", "拒绝不动 store")
}

// 间接环拒（Update 路径）：保存 G=42 引用 7，而 7 的存量图引用 42——链上出现自身 id。
func TestR11IndirectCycleReject(t *testing.T) {
	st := r11Store(map[uint64]*Workflow{7: taskWFRow(7, "")},
		map[uint64][]WorkflowNode{7: {wfNodeRow("inner", 42, `{"input":"{{input}}"}`)}})
	svc := New(st, nil, nil, &recordCache{}, nil, nil, nil, false)

	_, err := svc.Update(context.Background(), workflowapi.UpdateWorkflowReq{
		ID: 42, UpsertReq: r11Req(workflowapi.WorkflowTypeTask, 7, `{"input":"{{input}}"}`),
	})
	require.ErrorIs(t, err, errs.ErrValidationFailed)
	assert.Contains(t, err.Error(), "环")
	assert.NotContains(t, st.seq, "replaceGraph")
}

// ---- ListRuns：运行历史列表（spec 015，游标 keyset + limit 归一 + 摘要面）----

// seedRuns 造 n 行 run（id 与 created_at 同步递减——最新行 id 最大，keyset 双键
// 排序下「最新在前」）；started_at 各偏移 1 分钟做双时间区分。
func seedRuns(n int, t0 time.Time) []WorkflowRun {
	runs := make([]WorkflowRun, 0, n)
	for i := 0; i < n; i++ { // i=0 最新（id=n），i=n-1 最旧（id=1）
		r := WorkflowRun{
			WorkflowID: 42, WorkflowName: "智能客服分流", TriggerSource: "console", IsTrial: true,
			Status: "succeeded", DurationMs: 100 + i, ErrorNode: "", ErrorMsg: "",
			StartedAt: t0.Add(-time.Duration(i) * time.Minute),
		}
		r.ID = uint64(n - i)
		r.CreatedAt = t0.Add(-time.Duration(i) * time.Second)
		runs = append(runs, r)
	}
	return runs
}

func TestListRunsFirstPageHasMore(t *testing.T) {
	t0 := time.Date(2026, 9, 24, 8, 0, 0, 0, time.UTC)
	all := seedRuns(21, t0)
	st := &stubStore{listRunsFn: func(uint64, time.Time, uint64, int) ([]WorkflowRun, error) {
		return all[:21], nil // FetchN=21 全回（store 层不做切片）
	}}
	svc := New(st, nil, nil, &recordCache{}, nil, nil, nil, false)

	res, err := svc.ListRuns(context.Background(), workflowapi.ListRunsReq{WorkflowID: 42, Limit: 20})
	require.NoError(t, err)
	assert.Equal(t, uint64(42), st.lastListRuns.workflowID)
	assert.Equal(t, 21, st.lastListRuns.limit, "FetchN()：limit 20 → 取 21 判 has_more")
	assert.True(t, st.lastListRuns.before.IsZero(), "首页零值 cursor → 行值比较跳过")
	assert.Len(t, res.Items, 20)
	assert.True(t, res.HasMore)
	assert.Equal(t, 20, res.Limit)
	assert.Equal(t, "21", res.Items[0].ID, "最新在前 + id 字符串化")
	assert.Equal(t, t0, res.Items[0].StartedAt, "调用时间 = 执行起点（D2）")
	assert.Equal(t, t0, res.Items[0].CreatedAt, "排序键落库时刻（D2）")

	// next_cursor 可解码回末行（第 20 行）排序键——下一页起点
	key, err := page.DecodeCursor[runCursorKey](res.NextCursor)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), key.ID)
	assert.Equal(t, all[19].CreatedAt, key.CreatedAt)
}

// keysetStub 模拟 store 的行值比较语义：首页（before 零值）回全量，翻页只回
// (created_at, id) < (before, beforeID) 的行——service 层测试据此验证游标续拉。
func keysetStub(all []WorkflowRun) *stubStore {
	return &stubStore{listRunsFn: func(_ uint64, before time.Time, beforeID uint64, limit int) ([]WorkflowRun, error) {
		if before.IsZero() {
			return all, nil
		}
		var rest []WorkflowRun
		for _, r := range all {
			if r.CreatedAt.Before(before) || (r.CreatedAt.Equal(before) && r.ID < beforeID) {
				rest = append(rest, r)
			}
		}
		if len(rest) > limit {
			rest = rest[:limit]
		}
		return rest, nil
	}}
}

func TestListRunsSecondPage(t *testing.T) {
	t0 := time.Date(2026, 9, 24, 8, 0, 0, 0, time.UTC)
	all := seedRuns(21, t0)
	st := keysetStub(all)
	svc := New(st, nil, nil, &recordCache{}, nil, nil, nil, false)

	first, err := svc.ListRuns(context.Background(), workflowapi.ListRunsReq{WorkflowID: 42, Limit: 20})
	require.NoError(t, err)

	second, err := svc.ListRuns(context.Background(), workflowapi.ListRunsReq{WorkflowID: 42, Limit: 20, Cursor: first.NextCursor})
	require.NoError(t, err)
	assert.Len(t, second.Items, 1)
	assert.False(t, second.HasMore)
	assert.Empty(t, second.NextCursor, "has_more=false → 空 cursor（meta 序列化 null）")
	assert.Equal(t, "1", second.Items[0].ID, "最旧行补尾页")
	assert.Equal(t, uint64(2), st.lastListRuns.beforeID, "行值比较起点 = 上页末行 id")
	assert.Equal(t, all[19].CreatedAt, st.lastListRuns.before, "行值比较起点 = 上页末行 created_at")
}

// limit 归一不拒请求（D7）：≤0 → 20、>100 → 100，透传 store 为归一后 +1。
func TestListRunsLimitNormalize(t *testing.T) {
	tests := []struct {
		name       string
		reqLimit   int
		wantLimit  int
		wantFetchN int
	}{
		{"零值归 20", 0, 20, 21},
		{"负值归 20", -5, 20, 21},
		{"越界归 100", 999, 100, 101},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := &stubStore{listRunsFn: func(uint64, time.Time, uint64, int) ([]WorkflowRun, error) {
				return nil, nil
			}}
			svc := New(st, nil, nil, &recordCache{}, nil, nil, nil, false)

			res, err := svc.ListRuns(context.Background(), workflowapi.ListRunsReq{WorkflowID: 42, Limit: tt.reqLimit})
			require.NoError(t, err)
			assert.Equal(t, tt.wantFetchN, st.lastListRuns.limit)
			assert.Equal(t, tt.wantLimit, res.Limit, "meta.limit = 归一值")
			assert.NotNil(t, res.Items, "空结果 Items 为 [] 非 nil（空值约定）")
			assert.Empty(t, res.Items)
			assert.False(t, res.HasMore)
		})
	}
}

func TestListRunsBadCursor(t *testing.T) {
	svc := New(&stubStore{}, nil, nil, &recordCache{}, nil, nil, nil, false)

	_, err := svc.ListRuns(context.Background(), workflowapi.ListRunsReq{WorkflowID: 42, Cursor: "!!not-base64!!"})
	require.ErrorIs(t, err, errs.ErrValidationFailed, "篡改游标 → 400（D7）")
	assert.Contains(t, err.Error(), "cursor", "包装带 cursor 上下文")
}

func TestListRunsStoreError(t *testing.T) {
	boom := errors.New("q failed")
	st := &stubStore{listRunsFn: func(uint64, time.Time, uint64, int) ([]WorkflowRun, error) {
		return nil, boom
	}}
	svc := New(st, nil, nil, &recordCache{}, nil, nil, nil, false)

	_, err := svc.ListRuns(context.Background(), workflowapi.ListRunsReq{WorkflowID: 42})
	assert.ErrorIs(t, err, boom, "store 错误 %w 链保留")
}

// toRunSummary 转换字段面：摘要 9 字段全映射、id 字符串化、失败行错误两字段。
func TestToRunSummary(t *testing.T) {
	started := time.Date(2026, 9, 24, 7, 30, 0, 0, time.UTC)
	created := started.Add(2 * time.Second)
	r := WorkflowRun{
		WorkflowID: 42, WorkflowName: "智能客服分流", TriggerSource: "chat", IsTrial: false,
		Status: "failed", Input: `{"input":"查订单"}`, Output: "", ErrorNode: "order_api",
		ErrorMsg: "node order_api: connection refused", DurationMs: 3000, StartedAt: started,
		TraceID: "trace-abc",
	}
	r.ID = 7
	r.CreatedAt = created

	s := toRunSummary(&r)
	assert.Equal(t, "7", s.ID)
	assert.Equal(t, "failed", s.Status)
	assert.Equal(t, "chat", s.TriggerSource)
	assert.False(t, s.IsTrial)
	assert.Equal(t, 3000, s.DurationMs)
	assert.Equal(t, "order_api", s.ErrorNode)
	assert.Equal(t, "node order_api: connection refused", s.ErrorMsg)
	assert.Equal(t, started, s.StartedAt)
	assert.Equal(t, created, s.CreatedAt)
}

// ---- GetRun：运行详情与节点轨迹（spec 015，D3 同判 404 + D5 组装）----

// seedRun 一行 chat 触发的失败 run：三可空 id 两有一无（conversation/message 有值、
// parent nil——顶层 run 无父），input/output 带截断标记形态文本，覆盖详情档字段面。
func seedRun(t0 time.Time) *WorkflowRun {
	convID, msgID := uint64(900), uint64(901)
	r := &WorkflowRun{
		WorkflowID: 42, WorkflowName: "智能客服分流", TriggerSource: "chat", IsTrial: false,
		ConversationID: &convID, MessageID: &msgID, TraceID: "trace-abc",
		Status: "failed", Input: `{"query":"查订单"}`, Output: "",
		ErrorNode: "order_api", ErrorMsg: "node order_api: connection refused",
		DurationMs: 3000, StartedAt: t0,
	}
	r.ID = 7
	r.CreatedAt = t0.Add(2 * time.Second) // started_at 与排序键双时间区分
	return r
}

// 不存在与跨工作流同判 404（D3）：stub 只有一种 NotFound，语义由 store 双条件保证
//（service 只做哨兵翻译）；run 未命中不再查轨迹。
func TestGetRunNotFound(t *testing.T) {
	st := &stubStore{
		getRunByIDFn: func(uint64, uint64) (*WorkflowRun, error) {
			return nil, gorm.ErrRecordNotFound
		},
		listNodeRunsFn: func(uint64) ([]WorkflowNodeRun, error) {
			t.Fatal("run 未命中不应再查轨迹")
			return nil, nil
		},
	}
	svc := New(st, nil, nil, &recordCache{}, nil, nil, nil, false)

	_, err := svc.GetRun(context.Background(), workflowapi.GetRunReq{WorkflowID: 42, RunID: 999})
	assert.ErrorIs(t, err, workflowapi.ErrRunNotFound)
}

func TestGetRunAssemble(t *testing.T) {
	t0 := time.Date(2026, 9, 24, 8, 0, 0, 0, time.UTC)
	run := seedRun(t0)
	st := &stubStore{
		getRunByIDFn: func(workflowID, runID uint64) (*WorkflowRun, error) {
			return run, nil
		},
		listNodeRunsFn: func(runID uint64) ([]WorkflowNodeRun, error) {
			return []WorkflowNodeRun{
				{RunID: 7, Seq: 1, NodeKey: "classify", NodeType: "llm", Status: "succeeded",
					Input: `{"q":"查订单"}`, Output: `{"intent":"ORDER"}`, DurationMs: 800},
				{RunID: 7, Seq: 2, NodeKey: "order_api", NodeType: "tool", Status: "failed",
					Input: `{"id":"A1"}`, DurationMs: 120},
			}, nil
		},
	}
	svc := New(st, nil, nil, &recordCache{}, nil, nil, nil, false)

	d, err := svc.GetRun(context.Background(), workflowapi.GetRunReq{WorkflowID: 42, RunID: 7})
	require.NoError(t, err)
	// 双条件透传（D3）与轨迹按 run 主键分次查询（跨模块不 JOIN 同款组装）
	assert.Equal(t, uint64(42), st.lastGetRunByID.workflowID)
	assert.Equal(t, uint64(7), st.lastGetRunByID.runID)
	assert.Equal(t, uint64(7), st.lastListNodeRuns.runID)

	assert.Equal(t, "7", d.ID)
	assert.Equal(t, "failed", d.Status)
	assert.Equal(t, "chat", d.TriggerSource)
	assert.False(t, d.IsTrial)
	require.NotNil(t, d.ConversationID)
	assert.Equal(t, "900", *d.ConversationID)
	require.NotNil(t, d.MessageID)
	assert.Equal(t, "901", *d.MessageID)
	assert.Nil(t, d.ParentRunID, "顶层 run 无父 → null")
	assert.Equal(t, "trace-abc", d.TraceID)
	assert.Equal(t, `{"query":"查订单"}`, d.Input, "input 原样透传（含截断标记文本）")
	assert.Empty(t, d.Output)
	assert.Equal(t, "order_api", d.ErrorNode)
	assert.Equal(t, "node order_api: connection refused", d.ErrorMsg)
	assert.Equal(t, 3000, d.DurationMs)
	assert.Equal(t, t0, d.StartedAt)
	assert.Equal(t, t0.Add(2*time.Second), d.CreatedAt)

	require.Len(t, d.Nodes, 2)
	assert.Equal(t, 1, d.Nodes[0].Seq)
	assert.Equal(t, "classify", d.Nodes[0].NodeKey)
	assert.Equal(t, 2, d.Nodes[1].Seq, "轨迹按执行序（seq ASC 回放）")
	assert.Equal(t, "order_api", d.Nodes[1].NodeKey)
	assert.Equal(t, "failed", d.Nodes[1].Status)
	assert.Empty(t, d.Nodes[1].ErrorMsg, "node 级 error_msg 落库恒空（既有形态）")
}

func TestGetRunEmptyNodes(t *testing.T) {
	t0 := time.Date(2026, 9, 24, 8, 0, 0, 0, time.UTC)
	st := &stubStore{
		getRunByIDFn: func(uint64, uint64) (*WorkflowRun, error) { return seedRun(t0), nil },
		listNodeRunsFn: func(uint64) ([]WorkflowNodeRun, error) {
			return nil, nil // store 返 nil → service make 兜底
		},
	}
	svc := New(st, nil, nil, &recordCache{}, nil, nil, nil, false)

	d, err := svc.GetRun(context.Background(), workflowapi.GetRunReq{WorkflowID: 42, RunID: 7})
	require.NoError(t, err)
	require.NotNil(t, d.Nodes, "空轨迹 nodes 为 [] 非 nil（空值约定，D5）")
	assert.Empty(t, d.Nodes)
}

func TestGetRunStoreError(t *testing.T) {
	boom := errors.New("q failed")
	st := &stubStore{getRunByIDFn: func(uint64, uint64) (*WorkflowRun, error) { return nil, boom }}
	svc := New(st, nil, nil, &recordCache{}, nil, nil, nil, false)

	_, err := svc.GetRun(context.Background(), workflowapi.GetRunReq{WorkflowID: 42, RunID: 7})
	assert.ErrorIs(t, err, boom, "store 错误 %w 链保留")
}

// toRunDetail 转换字段面：详情 16 字段（三可空 id nil→null / 有值→字符串指针）。
func TestToRunDetail(t *testing.T) {
	t0 := time.Date(2026, 9, 24, 8, 0, 0, 0, time.UTC)
	d := toRunDetail(seedRun(t0), nil)

	assert.Equal(t, "7", d.ID)
	assert.Equal(t, "failed", d.Status)
	assert.Equal(t, "chat", d.TriggerSource)
	assert.False(t, d.IsTrial)
	require.NotNil(t, d.ConversationID)
	assert.Equal(t, "900", *d.ConversationID)
	require.NotNil(t, d.MessageID)
	assert.Equal(t, "901", *d.MessageID)
	assert.Nil(t, d.ParentRunID)
	assert.Equal(t, "trace-abc", d.TraceID)
	assert.Equal(t, `{"query":"查订单"}`, d.Input)
	assert.Empty(t, d.Output)
	assert.Equal(t, "order_api", d.ErrorNode)
	assert.Equal(t, "node order_api: connection refused", d.ErrorMsg)
	assert.Equal(t, 3000, d.DurationMs)
	assert.Equal(t, t0, d.StartedAt)
	assert.Equal(t, t0.Add(2*time.Second), d.CreatedAt)
}

// 三可空 id 的另一侧：子工作流嵌套 run——parent 有值、conversation/message nil。
func TestToRunDetailNestedRun(t *testing.T) {
	parent := uint64(3)
	r := &WorkflowRun{WorkflowID: 42, TriggerSource: "workflow", Status: "succeeded", ParentRunID: &parent}
	r.ID = 8

	d := toRunDetail(r, nil)
	assert.Nil(t, d.ConversationID, "嵌套 run 无会话关联 → null")
	assert.Nil(t, d.MessageID)
	require.NotNil(t, d.ParentRunID)
	assert.Equal(t, "3", *d.ParentRunID)
}

// toNodeRun 转换字段面：轨迹 8 字段（seq/node_key/node_type/status/input/output/
// error_msg/duration_ms）。
func TestToNodeRun(t *testing.T) {
	n := WorkflowNodeRun{RunID: 7, Seq: 2, NodeKey: "order_api", NodeType: "tool", Status: "failed",
		Input: `{"id":"A1"}`, Output: "", ErrorMsg: "", DurationMs: 120}

	s := toNodeRun(n)
	assert.Equal(t, 2, s.Seq)
	assert.Equal(t, "order_api", s.NodeKey)
	assert.Equal(t, "tool", s.NodeType)
	assert.Equal(t, "failed", s.Status)
	assert.Equal(t, `{"id":"A1"}`, s.Input)
	assert.Empty(t, s.Output)
	assert.Empty(t, s.ErrorMsg)
	assert.Equal(t, 120, s.DurationMs)
}
