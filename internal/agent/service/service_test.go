package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"

	agentapi "github.com/Karlsk/go-hify/internal/agent/api"
	"github.com/Karlsk/go-hify/internal/platform/cache"
	"github.com/Karlsk/go-hify/internal/platform/page"
	"github.com/Karlsk/go-hify/internal/platform/schema"
	providerapi "github.com/Karlsk/go-hify/internal/provider/api"
)

// service 测试：stub 本模块 Store / provider 的 ModelService / cacheManager，
// 覆盖 CRUD 编排、哨兵翻译（含 FK 23503）、缓存读路径与写时删 key。

// ---- stubs ----

// stubStore 按方法注入行为；未注入的方法返回零值。calls 记录调用足迹供断言。
type stubStore struct {
	getByID     func(ctx context.Context, id uint64) (*Agent, error)
	createAgent func(ctx context.Context, a *Agent) error
	listAgents  func(ctx context.Context, p page.OffsetParams) (page.OffsetResult[Agent], error)
	updateAgent func(ctx context.Context, a *Agent) error
	deleteAgent func(ctx context.Context, id uint64) error
	listTools   func(ctx context.Context, agentID uint64) ([]uint64, error)
	deleteTools func(ctx context.Context, agentID uint64) error
	createTools func(ctx context.Context, agentID uint64, toolIDs []uint64) error
	countTools  func(ctx context.Context, ids []uint64) (map[uint64]int64, error)
	listKBs     func(ctx context.Context, agentID uint64) ([]uint64, error)
	deleteKBs   func(ctx context.Context, agentID uint64) error
	createKBs   func(ctx context.Context, agentID uint64, kbIDs []uint64) error
	countKBs    func(ctx context.Context, ids []uint64) (map[uint64]int64, error)

	calls struct {
		getByID, createAgent, updateAgent, deleteAgent int
		deleteTools, createTools                       int
		deleteKBs, createKBs                           int
	}
}

// WithTx 直接执行 fn 并把自身充当 tx 句柄（单线程测试无真实事务语义需求，
// 但保持了「事务内调用都过 tx Store」的代码路径形态）。
func (s *stubStore) WithTx(ctx context.Context, fn func(tx Store) error) error { return fn(s) }

func (s *stubStore) CreateAgent(ctx context.Context, a *Agent) error {
	s.calls.createAgent++
	if s.createAgent != nil {
		return s.createAgent(ctx, a)
	}
	a.ID = 42 // 模拟 DB RETURNING 回填
	return nil
}

func (s *stubStore) GetAgentByID(ctx context.Context, id uint64) (*Agent, error) {
	s.calls.getByID++
	if s.getByID != nil {
		return s.getByID(ctx, id)
	}
	return sampleAgent(id), nil
}

func (s *stubStore) ListAgents(ctx context.Context, p page.OffsetParams) (page.OffsetResult[Agent], error) {
	if s.listAgents != nil {
		return s.listAgents(ctx, p)
	}
	return page.NewOffsetResult([]Agent{}, p, 0), nil
}

func (s *stubStore) UpdateAgent(ctx context.Context, a *Agent) error {
	s.calls.updateAgent++
	if s.updateAgent != nil {
		return s.updateAgent(ctx, a)
	}
	return nil
}

func (s *stubStore) DeleteAgent(ctx context.Context, id uint64) error {
	s.calls.deleteAgent++
	if s.deleteAgent != nil {
		return s.deleteAgent(ctx, id)
	}
	return nil
}

func (s *stubStore) ListToolIDsByAgent(ctx context.Context, agentID uint64) ([]uint64, error) {
	if s.listTools != nil {
		return s.listTools(ctx, agentID)
	}
	return []uint64{10, 12}, nil
}

func (s *stubStore) DeleteToolsByAgent(ctx context.Context, agentID uint64) error {
	s.calls.deleteTools++
	if s.deleteTools != nil {
		return s.deleteTools(ctx, agentID)
	}
	return nil
}

func (s *stubStore) CreateTools(ctx context.Context, agentID uint64, toolIDs []uint64) error {
	s.calls.createTools++
	if s.createTools != nil {
		return s.createTools(ctx, agentID, toolIDs)
	}
	return nil
}

// CountToolsByAgentIDs 默认空 map（无绑定）；列表聚合测试按需注入。
func (s *stubStore) CountToolsByAgentIDs(ctx context.Context, ids []uint64) (map[uint64]int64, error) {
	if s.countTools != nil {
		return s.countTools(ctx, ids)
	}
	return map[uint64]int64{}, nil
}

// ListKBIDsByAgent 默认无绑定（nil）；Get 详情测试按需注入。
func (s *stubStore) ListKBIDsByAgent(ctx context.Context, agentID uint64) ([]uint64, error) {
	if s.listKBs != nil {
		return s.listKBs(ctx, agentID)
	}
	return nil, nil
}

// CountKBsByAgentIDs 默认空 map。
func (s *stubStore) CountKBsByAgentIDs(ctx context.Context, ids []uint64) (map[uint64]int64, error) {
	if s.countKBs != nil {
		return s.countKBs(ctx, ids)
	}
	return map[uint64]int64{}, nil
}

func (s *stubStore) DeleteKBsByAgent(ctx context.Context, agentID uint64) error {
	s.calls.deleteKBs++
	if s.deleteKBs != nil {
		return s.deleteKBs(ctx, agentID)
	}
	return nil
}

func (s *stubStore) CreateKBs(ctx context.Context, agentID uint64, kbIDs []uint64) error {
	s.calls.createKBs++
	if s.createKBs != nil {
		return s.createKBs(ctx, agentID, kbIDs)
	}
	return nil
}

// stubModels 覆写 Get（存在性预检）与 ListByIDs（列表聚合名映射）；
// 其余方法由内嵌接口兜底（本 service 只用这两个）。
type stubModels struct {
	providerapi.ModelService
	get       func(ctx context.Context, req providerapi.GetModelReq) (*providerapi.ModelSchema, error)
	listByIDs func(ctx context.Context, req providerapi.ListModelsByIDsReq) ([]providerapi.ModelSchema, error)
}

func (s *stubModels) Get(ctx context.Context, req providerapi.GetModelReq) (*providerapi.ModelSchema, error) {
	return s.get(ctx, req)
}

func (s *stubModels) ListByIDs(ctx context.Context, req providerapi.ListModelsByIDsReq) ([]providerapi.ModelSchema, error) {
	return s.listByIDs(ctx, req)
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

func fkErr() error {
	return fmt.Errorf("insert bindings: %w", &pgconn.PgError{Code: "23503"})
}

// fkErrNamed 带约束名的 23503（spec 05 §4.4 起 agents 行级写语句按约束名分发）。
func fkErrNamed(constraint string) error {
	return fmt.Errorf("write agent: %w", &pgconn.PgError{Code: "23503", ConstraintName: constraint})
}

func sampleAgent(id uint64) *Agent {
	a := &Agent{
		Name:             "客服助手",
		Description:      "售后问答",
		ModelID:          5,
		SystemPrompt:     "你是售后客服",
		Temperature:      0.7,
		RAGTopK:          3,
		RAGMinSimilarity: 0.75,
	}
	a.ID = id
	return a
}

func okModels() *stubModels {
	return &stubModels{
		get: func(ctx context.Context, req providerapi.GetModelReq) (*providerapi.ModelSchema, error) {
			return nil, nil
		},
		listByIDs: func(ctx context.Context, req providerapi.ListModelsByIDsReq) ([]providerapi.ModelSchema, error) {
			return []providerapi.ModelSchema{}, nil // 空返回 → 悬空引用路径，ModelName 全 ""
		},
	}
}

func newSvc(st Store, models providerapi.ModelService, cm cacheManager) agentapi.AgentService {
	return New(st, models, cm)
}

// ---- Create ----

func TestCreate_Happy(t *testing.T) {
	st := &stubStore{}
	cm := &stubCache{}
	svc := newSvc(st, okModels(), cm)

	temp := 0.3
	fb := uint64(6)
	resp, err := svc.Create(context.Background(), agentapi.CreateAgentReq{
		Name: "客服助手", ModelID: 5, FallbackModelID: &fb,
		SystemPrompt: "你是售后客服", Temperature: &temp, ToolIDs: []uint64{10, 12},
		KnowledgeBaseIDs: []uint64{7, 8},
	})
	assert.NoError(t, err)
	assert.Equal(t, "42", resp.ID, "RETURNING 回填的 id 字符串化")
	assert.Equal(t, "5", resp.ModelID)
	assert.Equal(t, "6", *resp.FallbackModelID)
	assert.Equal(t, 0.3, resp.Temperature)
	assert.Equal(t, 1, st.calls.createAgent)
	assert.Equal(t, 1, st.calls.createTools, "绑定同事务落库")
	assert.Equal(t, 1, st.calls.createKBs, "KB 绑定同事务落库")
	assert.Empty(t, cm.deletedKeys, "新建无旧缓存可失效")
}

func TestCreate_TemperatureDefaults(t *testing.T) {
	st := &stubStore{}
	var captured *Agent
	st.createAgent = func(ctx context.Context, a *Agent) error {
		captured = a
		return nil
	}
	svc := newSvc(st, okModels(), &stubCache{})

	// 未传 temperature → 缺省 0.7。
	_, err := svc.Create(context.Background(), agentapi.CreateAgentReq{Name: "a", ModelID: 5})
	assert.NoError(t, err)
	assert.Equal(t, 0.7, captured.Temperature)

	// 显式传 0（严谨模式）是合法零值，不得被缺省吞掉（provider 踩坑 #1 的数值变体）。
	zero := 0.0
	_, err = svc.Create(context.Background(), agentapi.CreateAgentReq{Name: "a", ModelID: 5, Temperature: &zero})
	assert.NoError(t, err)
	assert.Equal(t, 0.0, captured.Temperature)
}

func TestCreate_EnabledAndContextDefaults(t *testing.T) {
	st := &stubStore{}
	var captured *Agent
	st.createAgent = func(ctx context.Context, a *Agent) error {
		captured = a
		return nil
	}
	svc := newSvc(st, okModels(), &stubCache{})

	// 未传 enabled / max_context_turns → true / 10（与 DB DEFAULT 对齐）。
	resp, err := svc.Create(context.Background(), agentapi.CreateAgentReq{Name: "a", ModelID: 5})
	assert.NoError(t, err)
	assert.True(t, captured.Enabled)
	assert.Equal(t, 10, captured.MaxContextTurns)
	assert.True(t, resp.Enabled)
	assert.Equal(t, 10, resp.MaxContextTurns)

	// 显式传 false / 20：false 是停用（合法显式值），不得被缺省吞掉（踩坑 #1 布尔变体）。
	off, turns := false, 20
	resp, err = svc.Create(context.Background(), agentapi.CreateAgentReq{
		Name: "a", ModelID: 5, Enabled: &off, MaxContextTurns: &turns,
	})
	assert.NoError(t, err)
	assert.False(t, captured.Enabled)
	assert.Equal(t, 20, captured.MaxContextTurns)
	assert.False(t, resp.Enabled)
	assert.Equal(t, 20, resp.MaxContextTurns)
}

func TestCreate_ModelNotFound(t *testing.T) {
	st := &stubStore{}
	models := &stubModels{get: func(ctx context.Context, req providerapi.GetModelReq) (*providerapi.ModelSchema, error) {
		return nil, providerapi.ErrModelNotFound
	}}
	svc := newSvc(st, models, &stubCache{})

	_, err := svc.Create(context.Background(), agentapi.CreateAgentReq{Name: "a", ModelID: 999})
	assert.ErrorIs(t, err, providerapi.ErrModelNotFound, "provider 哨兵透传")
	assert.Equal(t, 0, st.calls.createAgent, "预检失败不落库")
}

func TestCreate_FKOnTools(t *testing.T) {
	st := &stubStore{createTools: func(ctx context.Context, id uint64, ids []uint64) error { return fkErr() }}
	svc := newSvc(st, okModels(), &stubCache{})

	_, err := svc.Create(context.Background(), agentapi.CreateAgentReq{Name: "a", ModelID: 5, ToolIDs: []uint64{999}})
	assert.ErrorIs(t, err, agentapi.ErrToolNotFound, "FK 23503 → 工具不存在")
}

func TestCreate_FKOnKBs(t *testing.T) {
	st := &stubStore{createKBs: func(ctx context.Context, id uint64, ids []uint64) error { return fkErr() }}
	svc := newSvc(st, okModels(), &stubCache{})

	_, err := svc.Create(context.Background(), agentapi.CreateAgentReq{Name: "a", ModelID: 5, KnowledgeBaseIDs: []uint64{999}})
	assert.ErrorIs(t, err, agentapi.ErrKnowledgeBaseNotFound, "FK 23503 → 知识库不存在（agent 不依赖 rag，FK 是唯一校验）")
}

func TestCreate_FKOnAgent(t *testing.T) {
	// fixture 补约束名（spec 05 §8）：model 约束是 00004 内联 REFERENCES 的 PG 自动命名。
	st := &stubStore{createAgent: func(ctx context.Context, a *Agent) error { return fkErrNamed("agents_model_id_fkey") }}
	svc := newSvc(st, okModels(), &stubCache{})

	_, err := svc.Create(context.Background(), agentapi.CreateAgentReq{Name: "a", ModelID: 999})
	assert.ErrorIs(t, err, providerapi.ErrModelNotFound, "预检后模型被并发删除 → FK 兜底翻译")
}

func TestCreate_FKDispatchByConstraint(t *testing.T) {
	// spec 05 §4.4 核心行为变更：agents 行级写语句上的 23503 按约束名分发——
	// fk_agents_workflow → ErrWorkflowNotFound；model 侧约束与无约束名兜底保持
	// ErrModelNotFound；非 23503 / 非 PG 错误原样包装（不吞错、不误译）。
	cases := []struct {
		name   string
		inject error
		want   error // 非 nil 断言 ErrorIs；nil 断言不是任何哨兵（原样包装上抛）
	}{
		{"workflow 约束名 → ErrWorkflowNotFound", fkErrNamed("fk_agents_workflow"), agentapi.ErrWorkflowNotFound},
		{"model 约束名 → ErrModelNotFound", fkErrNamed("agents_model_id_fkey"), providerapi.ErrModelNotFound},
		{"fallback model 约束名 → ErrModelNotFound", fkErrNamed("agents_fallback_model_id_fkey"), providerapi.ErrModelNotFound},
		{"无约束名 → 兜底 ErrModelNotFound", fkErr(), providerapi.ErrModelNotFound},
		{"非 23503 → 原样包装不翻译", fmt.Errorf("create agent: %w", &pgconn.PgError{Code: "23505"}), nil},
		{"非 PG 错误 → 原样包装", errors.New("conn reset"), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := &stubStore{createAgent: func(ctx context.Context, a *Agent) error { return tc.inject }}
			svc := newSvc(st, okModels(), &stubCache{})

			_, err := svc.Create(context.Background(), agentapi.CreateAgentReq{Name: "a", ModelID: 5})
			assert.Error(t, err)
			if tc.want != nil {
				assert.ErrorIs(t, err, tc.want)
			} else {
				assert.NotErrorIs(t, err, agentapi.ErrWorkflowNotFound)
				assert.NotErrorIs(t, err, providerapi.ErrModelNotFound)
			}
		})
	}
}

func TestUpdate_FKDispatchByConstraint(t *testing.T) {
	// Update 语句同样按约束名分发（spec 05 §4.4 替换 Create/Update 两处）。
	st := &stubStore{updateAgent: func(ctx context.Context, a *Agent) error {
		return fkErrNamed("fk_agents_workflow")
	}}
	svc := newSvc(st, okModels(), &stubCache{})

	_, err := svc.Update(context.Background(), agentapi.UpdateAgentReq{ID: 1, Name: "x", ModelID: 5})
	assert.ErrorIs(t, err, agentapi.ErrWorkflowNotFound, "重绑撞不存在的 workflow → 404 哨兵")
}

func TestCreate_WorkflowIDToModel(t *testing.T) {
	// spec 05 US1：Create 带 workflow_id 落 model 直赋；toSchema 指针字符串化回显。
	st := &stubStore{}
	var captured *Agent
	st.createAgent = func(ctx context.Context, a *Agent) error {
		captured = a
		return nil
	}
	svc := newSvc(st, okModels(), &stubCache{})

	wid := uint64(3)
	resp, err := svc.Create(context.Background(), agentapi.CreateAgentReq{
		Name: "a", ModelID: 5, WorkflowID: &wid,
	})
	assert.NoError(t, err)
	if assert.NotNil(t, captured.WorkflowID, "workflow_id 落 model") {
		assert.Equal(t, uint64(3), *captured.WorkflowID)
	}
	if assert.NotNil(t, resp.WorkflowID, "已绑定响应字符串化") {
		assert.Equal(t, "3", *resp.WorkflowID)
	}
}

func TestCreate_WorkflowIDNilToModel(t *testing.T) {
	// 缺省 = 不绑定（常态）：model 字段 nil，响应回 null。
	st := &stubStore{}
	var captured *Agent
	st.createAgent = func(ctx context.Context, a *Agent) error {
		captured = a
		return nil
	}
	svc := newSvc(st, okModels(), &stubCache{})

	resp, err := svc.Create(context.Background(), agentapi.CreateAgentReq{Name: "a", ModelID: 5})
	assert.NoError(t, err)
	assert.Nil(t, captured.WorkflowID, "缺省 = 不绑定")
	assert.Nil(t, resp.WorkflowID, "未绑定响应 null")
}

// ---- Get ----

func TestGet_CacheHit(t *testing.T) {
	st := &stubStore{}
	cm := &stubCache{get: func(ctx context.Context, name, key string, dst any) (bool, error) {
		assert.Equal(t, cache.NameAgent, name)
		assert.Equal(t, "detail:1", key)
		d := dst.(*agentapi.AgentDetailSchema)
		d.ID = "1"
		d.Name = "缓存里的助手"
		d.ToolIDs = []string{"10"}
		return true, nil
	}}
	svc := newSvc(st, okModels(), cm)

	resp, err := svc.Get(context.Background(), agentapi.GetAgentReq{ID: 1})
	assert.NoError(t, err)
	assert.Equal(t, "缓存里的助手", resp.Name)
	assert.Equal(t, 0, st.calls.getByID, "命中缓存不落库")
}

func TestGet_CacheMiss(t *testing.T) {
	st := &stubStore{listKBs: func(ctx context.Context, agentID uint64) ([]uint64, error) { return []uint64{7, 8}, nil }}
	var setKey string
	var setVal any
	cm := &stubCache{set: func(ctx context.Context, name, key string, val any) error {
		setKey, setVal = key, val
		return nil
	}}
	svc := newSvc(st, okModels(), cm)

	resp, err := svc.Get(context.Background(), agentapi.GetAgentReq{ID: 1})
	assert.NoError(t, err)
	assert.Equal(t, "客服助手", resp.Name)
	assert.Equal(t, []string{"10", "12"}, resp.ToolIDs, "绑定 id 字符串化")
	assert.Equal(t, []string{"7", "8"}, resp.KnowledgeBaseIDs, "KB 绑定 id 字符串化")
	assert.Equal(t, "detail:1", setKey)
	detail, ok := setVal.(agentapi.AgentDetailSchema)
	assert.True(t, ok)
	assert.Equal(t, []string{"10", "12"}, detail.ToolIDs, "缓存载荷含绑定明细")
	assert.Equal(t, []string{"7", "8"}, detail.KnowledgeBaseIDs, "缓存载荷含 KB 绑定（chat 检索注入读此缓存）")
}

func TestGet_CacheMissEmptyTools(t *testing.T) {
	st := &stubStore{listTools: func(ctx context.Context, agentID uint64) ([]uint64, error) { return nil, nil }}
	svc := newSvc(st, okModels(), &stubCache{})

	resp, err := svc.Get(context.Background(), agentapi.GetAgentReq{ID: 1})
	assert.NoError(t, err)
	assert.Equal(t, []string{}, resp.ToolIDs, "空绑定返 [] 不返 null")
	assert.Equal(t, []string{}, resp.KnowledgeBaseIDs, "空 KB 绑定同样返 [] 不返 null")
}

func TestGet_CacheReadErrorFallsBack(t *testing.T) {
	st := &stubStore{}
	cm := &stubCache{get: func(ctx context.Context, name, key string, dst any) (bool, error) {
		return false, errors.New("redis down")
	}}
	svc := newSvc(st, okModels(), cm)

	resp, err := svc.Get(context.Background(), agentapi.GetAgentReq{ID: 1})
	assert.NoError(t, err, "缓存读失败降级 DB，不失败请求")
	assert.Equal(t, "客服助手", resp.Name)
}

func TestGet_NotFound(t *testing.T) {
	st := &stubStore{getByID: func(ctx context.Context, id uint64) (*Agent, error) {
		return nil, gorm.ErrRecordNotFound
	}}
	svc := newSvc(st, okModels(), &stubCache{})

	_, err := svc.Get(context.Background(), agentapi.GetAgentReq{ID: 999})
	assert.ErrorIs(t, err, agentapi.ErrAgentNotFound)
}

// ---- List ----

func TestList(t *testing.T) {
	a1, a2 := sampleAgent(1), sampleAgent(2)
	a2.ModelID = 5 // 两行同模型 → 去重后一次 ListByIDs
	a3 := sampleAgent(3)
	a3.ModelID = 999
	st := &stubStore{
		listAgents: func(ctx context.Context, p page.OffsetParams) (page.OffsetResult[Agent], error) {
			return page.NewOffsetResult([]Agent{*a1, *a2, *a3}, p, 3), nil
		},
		countTools: func(ctx context.Context, ids []uint64) (map[uint64]int64, error) {
			assert.Equal(t, []uint64{1, 2, 3}, ids, "当页 id 一次批量计数")
			return map[uint64]int64{1: 2, 3: 1}, nil // agent 2 无绑定 → map 无键 = 0
		},
		countKBs: func(ctx context.Context, ids []uint64) (map[uint64]int64, error) {
			assert.Equal(t, []uint64{1, 2, 3}, ids, "KB 计数同样当页一次批量")
			return map[uint64]int64{3: 2}, nil // agent 1/2 无 KB 绑定 → 0
		},
	}
	models := &stubModels{
		get: func(ctx context.Context, req providerapi.GetModelReq) (*providerapi.ModelSchema, error) {
			return nil, nil
		},
		listByIDs: func(ctx context.Context, req providerapi.ListModelsByIDsReq) ([]providerapi.ModelSchema, error) {
			assert.Equal(t, []uint64{5, 999}, req.IDs, "model_id 去重后批量取名")
			return []providerapi.ModelSchema{{BaseSchema: schema.BaseSchema{ID: "5"}, Name: "gpt-4o"}}, nil // 999 缺行 = 悬空引用
		},
	}
	svc := newSvc(st, models, &stubCache{})

	resp, err := svc.List(context.Background(), agentapi.ListAgentsReq{Page: 1, PageSize: 20})
	assert.NoError(t, err)
	assert.Equal(t, int64(3), resp.Total)
	assert.Len(t, resp.Items, 3)
	// 聚合列：模型名映射 + 工具/KB 计数（缺行 / 无绑定归零值）。
	assert.Equal(t, "gpt-4o", resp.Items[0].ModelName)
	assert.Equal(t, int64(2), resp.Items[0].ToolCount)
	assert.Equal(t, int64(0), resp.Items[0].KBCount)
	assert.Equal(t, "gpt-4o", resp.Items[1].ModelName, "同模型去重不影响映射")
	assert.Equal(t, int64(0), resp.Items[1].ToolCount)
	assert.Equal(t, int64(0), resp.Items[1].KBCount)
	assert.Equal(t, "", resp.Items[2].ModelName, "悬空引用 → 空串，前端 fallback model_id")
	assert.Equal(t, int64(1), resp.Items[2].ToolCount)
	assert.Equal(t, int64(2), resp.Items[2].KBCount)
}

func TestList_AggregateErrors(t *testing.T) {
	// 工具计数失败 → 整列表失败（聚合是列表契约一部分，不静默降级）。
	st := &stubStore{
		listAgents: func(ctx context.Context, p page.OffsetParams) (page.OffsetResult[Agent], error) {
			return page.NewOffsetResult([]Agent{*sampleAgent(1)}, p, 1), nil
		},
		countTools: func(ctx context.Context, ids []uint64) (map[uint64]int64, error) {
			return nil, errors.New("count failed")
		},
	}
	svc := newSvc(st, okModels(), &stubCache{})
	_, err := svc.List(context.Background(), agentapi.ListAgentsReq{})
	assert.Error(t, err)

	// 模型名查询失败同理。
	st2 := &stubStore{
		listAgents: st.listAgents,
		countTools: func(ctx context.Context, ids []uint64) (map[uint64]int64, error) {
			return map[uint64]int64{}, nil
		},
	}
	models := &stubModels{
		get: okModels().get,
		listByIDs: func(ctx context.Context, req providerapi.ListModelsByIDsReq) ([]providerapi.ModelSchema, error) {
			return nil, errors.New("provider down")
		},
	}
	svc2 := newSvc(st2, models, &stubCache{})
	_, err = svc2.List(context.Background(), agentapi.ListAgentsReq{})
	assert.Error(t, err)
}

func TestList_EmptyPageNotEmptyArray(t *testing.T) {
	svc := newSvc(&stubStore{}, okModels(), &stubCache{})

	resp, err := svc.List(context.Background(), agentapi.ListAgentsReq{})
	assert.NoError(t, err)
	assert.NotNil(t, resp.Items, "空页 items 返 [] 不返 null")
	assert.Empty(t, resp.Items)
}

// ---- Update ----

func TestUpdate_Happy(t *testing.T) {
	st := &stubStore{}
	cm := &stubCache{}
	svc := newSvc(st, okModels(), cm)

	resp, err := svc.Update(context.Background(), agentapi.UpdateAgentReq{
		ID: 1, Name: "改名", ModelID: 6, SystemPrompt: "新提示词", ToolIDs: []uint64{12},
		KnowledgeBaseIDs: []uint64{8},
	})
	assert.NoError(t, err)
	assert.Equal(t, "1", resp.ID)
	assert.Equal(t, "6", resp.ModelID)
	assert.Equal(t, 1, st.calls.updateAgent)
	assert.Equal(t, 1, st.calls.deleteTools, "绑定先删")
	assert.Equal(t, 1, st.calls.createTools, "后插")
	assert.Equal(t, 1, st.calls.deleteKBs, "KB 绑定先删")
	assert.Equal(t, 1, st.calls.createKBs, "后插")
	assert.Equal(t, []string{"detail:1"}, cm.deletedKeys, "提交后写时删 key")
}

func TestUpdate_NotFound(t *testing.T) {
	st := &stubStore{getByID: func(ctx context.Context, id uint64) (*Agent, error) {
		return nil, gorm.ErrRecordNotFound
	}}
	svc := newSvc(st, okModels(), &stubCache{})

	_, err := svc.Update(context.Background(), agentapi.UpdateAgentReq{ID: 999, Name: "x", ModelID: 5})
	assert.ErrorIs(t, err, agentapi.ErrAgentNotFound)
}

func TestUpdate_EnabledAndContextApplied(t *testing.T) {
	st := &stubStore{}
	var captured *Agent
	st.updateAgent = func(ctx context.Context, a *Agent) error {
		captured = a
		return nil
	}
	svc := newSvc(st, okModels(), &stubCache{})

	// PUT 全量未传 → 置回缺省（true / 10）——前端必须显式提交 enabled。
	off := false
	_, err := svc.Update(context.Background(), agentapi.UpdateAgentReq{ID: 1, Name: "x", ModelID: 5, Enabled: &off})
	assert.NoError(t, err)
	assert.False(t, captured.Enabled, "显式 false 停用")
	assert.Equal(t, 10, captured.MaxContextTurns, "未传轮数 → 缺省 10")

	turns := 20
	_, err = svc.Update(context.Background(), agentapi.UpdateAgentReq{ID: 1, Name: "x", ModelID: 5, MaxContextTurns: &turns})
	assert.NoError(t, err)
	assert.True(t, captured.Enabled, "未传 enabled → 置回 true")
	assert.Equal(t, 20, captured.MaxContextTurns)
}

func TestUpdate_WorkflowIDBindAndUnbind(t *testing.T) {
	// spec 05 US1：PUT 全量语义——带 workflow_id 绑定 / 重绑；缺省（= null）解绑回 nil。
	st := &stubStore{}
	var captured *Agent
	st.updateAgent = func(ctx context.Context, a *Agent) error {
		captured = a
		return nil
	}
	svc := newSvc(st, okModels(), &stubCache{})

	wid := uint64(3)
	resp, err := svc.Update(context.Background(), agentapi.UpdateAgentReq{
		ID: 1, Name: "x", ModelID: 5, WorkflowID: &wid,
	})
	assert.NoError(t, err)
	if assert.NotNil(t, captured.WorkflowID, "PUT 带 workflow_id 落 model") {
		assert.Equal(t, uint64(3), *captured.WorkflowID)
	}
	if assert.NotNil(t, resp.WorkflowID) {
		assert.Equal(t, "3", *resp.WorkflowID)
	}

	// 解绑：已绑定实体（workflow 9），PUT 体缺省 workflow_id → model 字段回 nil。
	st2 := &stubStore{}
	bound := sampleAgent(1)
	nine := uint64(9)
	bound.WorkflowID = &nine
	st2.getByID = func(ctx context.Context, id uint64) (*Agent, error) { return bound, nil }
	var captured2 *Agent
	st2.updateAgent = func(ctx context.Context, a *Agent) error {
		captured2 = a
		return nil
	}
	svc2 := newSvc(st2, okModels(), &stubCache{})

	resp2, err := svc2.Update(context.Background(), agentapi.UpdateAgentReq{ID: 1, Name: "x", ModelID: 5})
	assert.NoError(t, err)
	assert.Nil(t, captured2.WorkflowID, "PUT 缺省 workflow_id = 解绑（applyUpdate 直赋 nil）")
	assert.Nil(t, resp2.WorkflowID, "解绑后响应回 null")
}

func TestUpdate_FKOnTools(t *testing.T) {
	st := &stubStore{createTools: func(ctx context.Context, id uint64, ids []uint64) error { return fkErr() }}
	cm := &stubCache{}
	svc := newSvc(st, okModels(), cm)

	_, err := svc.Update(context.Background(), agentapi.UpdateAgentReq{ID: 1, Name: "x", ModelID: 5, ToolIDs: []uint64{999}})
	assert.ErrorIs(t, err, agentapi.ErrToolNotFound)
	assert.Empty(t, cm.deletedKeys, "事务失败不失效缓存（DB 未变，缓存仍有效）")
}

func TestUpdate_FKOnKBs(t *testing.T) {
	st := &stubStore{createKBs: func(ctx context.Context, id uint64, ids []uint64) error { return fkErr() }}
	cm := &stubCache{}
	svc := newSvc(st, okModels(), cm)

	_, err := svc.Update(context.Background(), agentapi.UpdateAgentReq{
		ID: 1, Name: "x", ModelID: 5, KnowledgeBaseIDs: []uint64{999},
	})
	assert.ErrorIs(t, err, agentapi.ErrKnowledgeBaseNotFound, "FK 23503 → 知识库不存在")
	assert.Empty(t, cm.deletedKeys, "事务失败不失效缓存")
}

// ---- Delete ----

func TestDelete_Happy(t *testing.T) {
	st := &stubStore{}
	cm := &stubCache{}
	svc := newSvc(st, okModels(), cm)

	err := svc.Delete(context.Background(), agentapi.DeleteAgentReq{ID: 1})
	assert.NoError(t, err)
	assert.Equal(t, 1, st.calls.deleteAgent)
	assert.Equal(t, []string{"detail:1"}, cm.deletedKeys)
}

func TestDelete_NotFound(t *testing.T) {
	st := &stubStore{deleteAgent: func(ctx context.Context, id uint64) error { return gorm.ErrRecordNotFound }}
	svc := newSvc(st, okModels(), &stubCache{})

	err := svc.Delete(context.Background(), agentapi.DeleteAgentReq{ID: 999})
	assert.ErrorIs(t, err, agentapi.ErrAgentNotFound)
}

func TestDelete_InUse(t *testing.T) {
	// 有历史会话：conversations.agent_id FK RESTRICT 抛 23503 → 挡删 ErrAgentInUse
	st := &stubStore{deleteAgent: func(ctx context.Context, id uint64) error { return fkErr() }}
	cm := &stubCache{}
	svc := newSvc(st, okModels(), cm)

	err := svc.Delete(context.Background(), agentapi.DeleteAgentReq{ID: 1})
	assert.ErrorIs(t, err, agentapi.ErrAgentInUse)
	assert.Empty(t, cm.deletedKeys, "删除被挡 DB 未变，不失效缓存")
}

func TestEvict_FailureDoesNotFailRequest(t *testing.T) {
	cm := &stubCache{delete: func(ctx context.Context, name, key string) error { return errors.New("redis down") }}
	svc := newSvc(&stubStore{}, okModels(), cm)

	err := svc.Delete(context.Background(), agentapi.DeleteAgentReq{ID: 1})
	assert.NoError(t, err, "缓存删失败仅 WARN，业务照常成功")
}
