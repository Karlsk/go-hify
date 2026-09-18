package service

// service 测试：stub Store（函数字段覆写 + 调用序列记录）、内嵌接口 stub 下游
// （provider / rag 只覆写 Get）、记录式 cacheManager stub——零真实 PG / Redis / 网络，
// 对齐 agent service_test 模式（spec 04 §7）。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	providerapi "github.com/Karlsk/go-hify/internal/provider/api"
	ragapi "github.com/Karlsk/go-hify/internal/rag/api"
	"github.com/Karlsk/go-hify/internal/platform/errs"
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

	seq      []string // 调用序列（方法名）
	lastFrom []string // UpdateStatus 最近一次 from
	lastTo   string   // UpdateStatus 最近一次 to
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

	wf, nodes, edges := toModel(req)

	assert.Equal(t, "智能客服分流", wf.Name)
	assert.Equal(t, "意图识别 → 分支", wf.Description)
	assert.Equal(t, "classify", wf.StartNodeKey)
	assert.Equal(t, "draft", wf.Status, "Status 恒 draft（服务端定，不看请求体）")

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
	_, _, edges := toModel(workflowapi.UpsertReq{
		Name:         "纯线性图",
		StartNodeKey: "a",
		Nodes:        []workflowapi.NodeReq{{Key: "a", Type: workflowapi.NodeLLM, Config: json.RawMessage(`{"model_id":"1","prompt":"p"}`)}},
		Edges:        []workflowapi.EdgeReq{},
	})
	assert.NotNil(t, edges, "空 edges → 空切片兜底（store 据此跳过该语句）")
	assert.Empty(t, edges)
}

func TestToSchemas(t *testing.T) {
	now := time.Now()
	cond := "true"
	wf := &Workflow{Name: "智能客服分流", Description: "意图识别 → 分支", StartNodeKey: "classify", Status: "published"}
	wf.ID, wf.CreatedAt, wf.UpdatedAt = 42, now, now

	sum := toSummarySchema(wf)
	assert.Equal(t, "42", sum.ID, "ID 字符串化")
	assert.Equal(t, "智能客服分流", sum.Name)
	assert.Equal(t, "published", sum.Status)
	assert.Equal(t, now, sum.CreatedAt)
	assert.Equal(t, now, sum.UpdatedAt)

	nodes := []WorkflowNode{
		{NodeKey: "classify", Type: "llm", Name: "意图识别", Config: `{"model_id":"3","prompt":"判断意图"}`},
	}
	edges := []WorkflowEdge{
		{SourceNodeKey: "classify", TargetNodeKey: "router", Condition: &cond},
	}
	detail := toDetailSchema(wf, nodes, edges)
	assert.Equal(t, "42", detail.ID)
	assert.Equal(t, "classify", detail.StartNodeKey)
	assert.Len(t, detail.Nodes, 1)
	assert.Equal(t, "llm", detail.Nodes[0].Type)
	assert.Equal(t, json.RawMessage(`{"model_id":"3","prompt":"判断意图"}`), detail.Nodes[0].Config, "config 原样透传")
	assert.Len(t, detail.Edges, 1)
	assert.Equal(t, "router", detail.Edges[0].TargetNodeKey)
	assert.Equal(t, "true", *detail.Edges[0].Condition)

	empty := toDetailSchema(wf, nil, nil)
	assert.NotNil(t, empty.Nodes, "空节点 → [] 不 null（接口规范空值约定）")
	assert.NotNil(t, empty.Edges)
	assert.Empty(t, empty.Nodes)
	assert.Empty(t, empty.Edges)
}

// ---- T3：Create（条 9 预检 + 23505 翻译 + 组装返回）----

// llmUpsertReq 含 llm 节点的合法创建请求（model_id=3，条 9 预检走 provider 分支）。
func llmUpsertReq() workflowapi.UpsertReq {
	return workflowapi.UpsertReq{
		Name:         "智能客服分流",
		Description:  "意图识别 → 分支",
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
			Name: "线性链", StartNodeKey: "a",
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
