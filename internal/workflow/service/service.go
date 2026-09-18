// Package service 是 workflow 模块的业务层：实现 api.WorkflowService 接口（CRUD /
// 发布 / 停用的业务编排、条 9 引用存在性预检、Cache-Aside 缓存、schema↔model 转换、
// 哨兵翻译发生在实现 api 接口的边界上）。本包同时定义 Store 数据层接口——定义在
// 消费方（本包）、由 store 包实现、组合根注入，依赖倒置。
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"github.com/Karlsk/go-hify/internal/platform/cache"
	"github.com/Karlsk/go-hify/internal/platform/errs"
	providerapi "github.com/Karlsk/go-hify/internal/provider/api"
	ragapi "github.com/Karlsk/go-hify/internal/rag/api"
	workflowapi "github.com/Karlsk/go-hify/internal/workflow/api"
)

// Store 数据层接口：整图读写的原子单元（Create / ReplaceGraph 内部事务包装，
// api_contract §6——service 不拼 DML，store 不做业务判断，事务内只操作本模块三张表）。
type Store interface {
	// Create 一事务写三表：INSERT workflows + 批量 INSERT nodes/edges（任一失败整体回滚）。
	Create(ctx context.Context, wf *Workflow, nodes []WorkflowNode, edges []WorkflowEdge) error
	// GetByID 按主键查；未找到原样上抛 gorm.ErrRecordNotFound（翻译归 service，spec 04）。
	GetByID(ctx context.Context, id uint64) (*Workflow, error)
	// List 行 + 精确 total（极小静态表，接口规范 B 模式允许 OFFSET 与精确 COUNT）。
	List(ctx context.Context, offset, limit int) ([]Workflow, int64, error)
	// ReplaceGraph 整图替换单事务：UPDATE workflows（affected=0 → false，service 翻译 404）
	// → DELETE nodes → DELETE edges → 批量 INSERT ×2。先删后插硬删，不做 diff（db_model 决策 #8）。
	ReplaceGraph(ctx context.Context, wf *Workflow, nodes []WorkflowNode, edges []WorkflowEdge) (bool, error)
	// Delete 硬删 workflows；nodes/edges 由 FK CASCADE 清理。affected=0 → false。
	Delete(ctx context.Context, id uint64) (bool, error)
	// UpdateStatus 原子状态迁移：UPDATE ... SET status=to WHERE id=? AND status IN (from)。
	// affected>0 → moved=true（service 据此判幂等，不做 get-then-set 竞态窗口）。
	UpdateStatus(ctx context.Context, id uint64, from []string, to string) (bool, error)
	// ListNodes 按 id 升序稳定还原（多 VALUES INSERT 的 id 顺序即请求顺序）。
	ListNodes(ctx context.Context, workflowID uint64) ([]WorkflowNode, error)
	// ListEdges 按 id 升序稳定还原。
	ListEdges(ctx context.Context, workflowID uint64) ([]WorkflowEdge, error)
	// CreateRun 执行收尾一事务两批写：INSERT workflow_runs RETURNING id/created_at →
	// 回填 run 与 nodeRuns 的 RunID → 批量多 VALUES INSERT workflow_node_runs（spec 06
	// O7；nodeRuns 空则只写 run 行）。append-only，无 RUNNING 态。
	CreateRun(ctx context.Context, run *WorkflowRun, nodeRuns []WorkflowNodeRun) error
	// DeleteRunsBefore 保留期清理（FR8）：批删 created_at < before 的 run 行（单批至多
	// limit 行，防长事务），node_runs 随 FK CASCADE 连带删；返回实际删除行数。
	DeleteRunsBefore(ctx context.Context, before time.Time, limit int) (int64, error)
}

// cacheManager 是 platform/cache 的窄接口：service 只用读 / 写 / 删三个动作
// （方法集与 agent/service 的 cacheManager 逐字对齐，spec 04 §2.1），单测可 stub
// （组合根传 *cache.Cache，结构化类型天然满足）。
type cacheManager interface {
	Get(ctx context.Context, name, key string, dst any) (bool, error)
	Set(ctx context.Context, name, key string, val any) error
	Delete(ctx context.Context, name, key string) error
}

// workflowService 实现 workflowapi.WorkflowService。
type workflowService struct {
	store  Store                        // spec 03 接口，store 包实现
	models providerapi.ModelService    // llm 节点 model_id 存在性预检 + 执行期 ResolveLLMConfig
	kbs    ragapi.KnowledgeBaseService // knowledge_retrieval 节点 KB 预检 + 执行期检索
	cache  cacheManager
	exec   *executor // 节点执行器（spec 06；models / kbs / clients / execs / rags 窄面注入）
	// onNodeDone 进度回调缝（FR9）：nil = 零开销；非 nil 每节点成功后按执行序回调。
	// 禁止轮询轨迹表；SSE 事件形态归后续 chat spec。
	onNodeDone func(nodeKey, output string)
}

// New 返回 api 接口；组合根将返回值注入 handler（及 chat 消费方）。clients /
// execs / rags 是执行引擎依赖（spec 06）：llm.Manager、logging.ExecutionStore、
// rag 服务按窄接口注入（结构化类型天然满足）；blockPrivate = O6 的
// WORKFLOW_API_BLOCK_PRIVATE。
func New(store Store, models providerapi.ModelService, kbs ragapi.KnowledgeBaseService,
	cm cacheManager, clients llmClientFactory, execs executionWriter, rags ragRetriever,
	blockPrivate bool) workflowapi.WorkflowService {
	return &workflowService{
		store: store, models: models, kbs: kbs, cache: cm,
		exec: newExecutor(models, clients, execs, rags, blockPrivate),
	}
}

// 编译期断言：workflowService 实现了 api 接口（spec 04 §2.1）。
var _ workflowapi.WorkflowService = (*workflowService)(nil)

// toModel 将 UpsertReq 组装为 model 行：Status 恒 draft（服务端定；Update 路径的
// store.ReplaceGraph 不写 status——编辑不降级，此字段被忽略），Config 直存请求 JSON
// 原文（api 层已强校验），Edges.Condition 指针透传（区分没传与空串）。子表 FK 由
// store 事务内回填，组装期不填。
func toModel(req workflowapi.UpsertReq) (*Workflow, []WorkflowNode, []WorkflowEdge) {
	wf := &Workflow{
		Name:         req.Name,
		Description:  req.Description,
		StartNodeKey: req.StartNodeKey,
		Status:       string(workflowapi.StatusDraft),
	}
	nodes := make([]WorkflowNode, 0, len(req.Nodes))
	for _, n := range req.Nodes {
		nodes = append(nodes, WorkflowNode{
			NodeKey: n.Key,
			Type:    string(n.Type),
			Name:    n.Name,
			Config:  string(n.Config),
		})
	}
	edges := make([]WorkflowEdge, 0, len(req.Edges))
	for _, e := range req.Edges {
		edges = append(edges, WorkflowEdge{
			SourceNodeKey: e.SourceNodeKey,
			TargetNodeKey: e.TargetNodeKey,
			Condition:     e.Condition,
		})
	}
	return wf, nodes, edges
}

// toSummarySchema model → 摘要（列表 / 状态动作返回）。
func toSummarySchema(wf *Workflow) workflowapi.WorkflowSummarySchema {
	return workflowapi.WorkflowSummarySchema{
		ID:          strconv.FormatUint(wf.ID, 10),
		Name:        wf.Name,
		Description: wf.Description,
		Status:      wf.Status,
		CreatedAt:   wf.CreatedAt,
		UpdatedAt:   wf.UpdatedAt,
	}
}

// toDetailSchema 三查结果 → 详情；Nodes / Edges 空切片兜底（禁 null，接口规范空值
// 约定），config 原样透传（库里即校验过的原文）。
func toDetailSchema(wf *Workflow, nodes []WorkflowNode, edges []WorkflowEdge) *workflowapi.WorkflowDetailSchema {
	d := &workflowapi.WorkflowDetailSchema{
		WorkflowSummarySchema: toSummarySchema(wf),
		StartNodeKey:          wf.StartNodeKey,
		Nodes:                 make([]workflowapi.NodeSchema, 0, len(nodes)),
		Edges:                 make([]workflowapi.EdgeSchema, 0, len(edges)),
	}
	for _, n := range nodes {
		d.Nodes = append(d.Nodes, workflowapi.NodeSchema{
			Key:    n.NodeKey,
			Type:   n.Type,
			Name:   n.Name,
			Config: json.RawMessage(n.Config),
		})
	}
	for _, e := range edges {
		d.Edges = append(d.Edges, workflowapi.EdgeSchema{
			SourceNodeKey: e.SourceNodeKey,
			TargetNodeKey: e.TargetNodeKey,
			Condition:     e.Condition,
		})
	}
	return d
}

// pgCodeUniqueViolation PG 唯一约束错误码（uq_workflows_name）。
const pgCodeUniqueViolation = "23505"

// isUniqueViolation 判断 PG 唯一约束冲突（23505，对齐 provider/service 同名写法）。
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgCodeUniqueViolation
}

// pgCodeFKViolation PG 外键违例错误码（constraint_violation 类；Delete 撞
// fk_agents_workflow RESTRICT，spec 05 §4.5）。
const pgCodeFKViolation = "23503"

// isFKViolation 判断 PG 外键违例（23503，agent/provider 同款写法）。
func isFKViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgCodeFKViolation
}

// Create：R10 模板引用校验（纯内存）→ 条 9 预检 → 组装 model 行 → store.Create
//（一事务三表）→ 三查组装 detail 返回（round-trip 即校验）。不预热缓存（写路径，spec 04 §3）。
func (s *workflowService) Create(ctx context.Context, req workflowapi.UpsertReq) (*workflowapi.WorkflowDetailSchema, error) {
	if err := validateTemplateRefs(req.Nodes, req.Edges); err != nil {
		return nil, err
	}
	if err := s.precheckRefs(ctx, req.Nodes); err != nil {
		return nil, err
	}
	wf, nodes, edges := toModel(req)
	if err := s.store.Create(ctx, wf, nodes, edges); err != nil {
		if isUniqueViolation(err) {
			return nil, workflowapi.ErrWorkflowNameConflict // 409
		}
		return nil, fmt.Errorf("create workflow %s: %w", req.Name, err)
	}
	return s.assembleDetail(ctx, wf.ID)
}

// precheckRefs 条 9 引用存在性预检（Create / Update 共用，db_model §7 条 9）：llm →
// provider api 查 model_id；knowledge_retrieval → rag api 查 knowledge_base_id；tool
// 不查（2026-09-15 拍板：mcp api 未建、jsonb 无 FK 兜底，推迟执行器 fail-fast）。
// api 层已解析过 config，此处为取 ModelID / KnowledgeBaseID 二次解析（≤50 个小 JSON，
// 成本可忽略，换取契约各层单一职责，spec 04 §3）。
func (s *workflowService) precheckRefs(ctx context.Context, nodes []workflowapi.NodeReq) error {
	for _, n := range nodes {
		switch n.Type {
		case workflowapi.NodeLLM:
			cfg, err := workflowapi.ParseNodeConfig(n.Type, n.Config)
			if err != nil {
				return fmt.Errorf("node %q: %w", n.Key, err) // 理论不可达（api Validate 已过）
			}
			llm, ok := cfg.(*workflowapi.LLMConfig)
			if !ok {
				return fmt.Errorf("node %q: unexpected config type %T", n.Key, cfg)
			}
			if _, err := s.models.Get(ctx, providerapi.GetModelReq{ID: llm.ModelID}); err != nil {
				if errors.Is(err, providerapi.ErrModelNotFound) {
					return fmt.Errorf("%w: node %q model_id %d", errs.ErrValidationFailed, n.Key, llm.ModelID)
				}
				return fmt.Errorf("precheck node %q model %d: %w", n.Key, llm.ModelID, err)
			}
		case workflowapi.NodeKnowledgeRetrieval:
			cfg, err := workflowapi.ParseNodeConfig(n.Type, n.Config)
			if err != nil {
				return fmt.Errorf("node %q: %w", n.Key, err)
			}
			kr, ok := cfg.(*workflowapi.KnowledgeRetrievalConfig)
			if !ok {
				return fmt.Errorf("node %q: unexpected config type %T", n.Key, cfg)
			}
			if _, err := s.kbs.Get(ctx, ragapi.GetKnowledgeBaseReq{ID: kr.KnowledgeBaseID}); err != nil {
				if errors.Is(err, ragapi.ErrKnowledgeBaseNotFound) {
					return fmt.Errorf("%w: node %q knowledge_base_id %d", errs.ErrValidationFailed, n.Key, kr.KnowledgeBaseID)
				}
				return fmt.Errorf("precheck node %q knowledge base %d: %w", n.Key, kr.KnowledgeBaseID, err)
			}
		}
	}
	return nil
}

// validateTemplateRefs R10 保存期模板引用校验（db_model §7 条 11，2026-09-17 O2 拍板）：
// llm.prompt / api.url + headers + body / end.output / condition.expression 内 {{var}}
// 引用名 ∈ {input} ∪ 该节点祖先 node_key 集（R7 无环 ⇒ DAG 祖先可算）。违例 →
// VALIDATION_FAILED 400，details 带节点 key 与引用名；执行期 strict 渲染兜底不变。
// 纯内存校验（无 IO），Create / Update 均先于条 9 预检调用——图缺陷不浪费下游查询。
func validateTemplateRefs(nodes []workflowapi.NodeReq, edges []workflowapi.EdgeReq) error {
	// 反向邻接表：target → sources；沿其 BFS 即祖先集。
	inEdges := make(map[string][]string, len(edges))
	for _, e := range edges {
		inEdges[e.TargetNodeKey] = append(inEdges[e.TargetNodeKey], e.SourceNodeKey)
	}
	for _, n := range nodes {
		ancestors := ancestorsOf(n.Key, inEdges)
		refs, err := nodeTemplateRefs(n)
		if err != nil {
			return err
		}
		for _, ref := range refs {
			if ref != "input" && !ancestors[ref] {
				return fmt.Errorf("%w: node %q 引用未定义变量 %q（可用：input 与祖先节点 key）",
					errs.ErrValidationFailed, n.Key, ref)
			}
		}
	}
	return nil
}

// ancestorsOf 沿反向边 BFS 求节点祖先集（R7 保证无环；环 / 未知 key 由访问集兜底
// 不死循环）。图 ≤50 节点，逐节点重算成本可忽略（precheckRefs 二次解析同款取舍）。
func ancestorsOf(key string, inEdges map[string][]string) map[string]bool {
	seen := make(map[string]bool)
	queue := append([]string(nil), inEdges[key]...)
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if seen[cur] {
			continue
		}
		seen[cur] = true
		queue = append(queue, inEdges[cur]...)
	}
	return seen
}

// nodeTemplateRefs 按节点类型提取冻结清单内的模板字段引用（db_model §7 条 11）；
// tool / knowledge_retrieval 无模板字段。condition 比较式仅取 == 左侧——右侧
// 'literal' 是字面量非引用、不查（与执行期 evalCondition 同一切分规则）。
func nodeTemplateRefs(n workflowapi.NodeReq) ([]string, error) {
	switch n.Type {
	case workflowapi.NodeTool, workflowapi.NodeKnowledgeRetrieval:
		return nil, nil
	}
	cfg, err := workflowapi.ParseNodeConfig(n.Type, n.Config)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", n.Key, err) // 理论不可达（api Validate 已过）
	}
	var refs []string
	switch v := cfg.(type) {
	case *workflowapi.LLMConfig:
		refs = templateRefs(v.Prompt)
	case *workflowapi.ConditionConfig:
		expr := strings.TrimSpace(v.Expression)
		if idx := strings.Index(expr, "=="); idx >= 0 {
			expr = expr[:idx] // 右侧 'literal' 不查
		}
		refs = templateRefs(expr)
	case *workflowapi.ApiCallConfig:
		refs = templateRefs(v.URL)
		refs = append(refs, templateRefs(v.Body)...)
		hdrKeys := make([]string, 0, len(v.Headers))
		for k := range v.Headers {
			hdrKeys = append(hdrKeys, k)
		}
		sort.Strings(hdrKeys) // 确定性：多 header 违例时报错可复现
		for _, k := range hdrKeys {
			refs = append(refs, templateRefs(v.Headers[k])...)
		}
	case *workflowapi.EndConfig:
		refs = templateRefs(v.Output)
	default:
		return nil, fmt.Errorf("node %q: unexpected config type %T", n.Key, cfg)
	}
	return refs, nil
}

// templateRefs 提取模板内全部 {{var}} 引用名——与执行期 render 共用 placeholderRE
// tokenizer（execcontext.go），含空格的引用名两侧语义一致（不剥离、必不匹配）。
func templateRefs(tpl string) []string {
	matches := placeholderRE.FindAllStringSubmatch(tpl, -1)
	refs := make([]string, 0, len(matches))
	for _, m := range matches {
		refs = append(refs, m[1])
	}
	return refs
}

// loadGraph 三查加载整图快照（GetByID + ListNodes + ListEdges），404 在此翻译成
// 哨兵。assembleDetail（缓存回源路径）与 Execute（实时图直读）共用；缓存读写由
// 调用方决定——Execute 不经 Get / 缓存，执行读实时图（FR2）由直读本函数钉死。
func (s *workflowService) loadGraph(ctx context.Context, id uint64) (*Workflow, []WorkflowNode, []WorkflowEdge, error) {
	wf, err := s.store.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil, workflowapi.ErrWorkflowNotFound // 404
		}
		return nil, nil, nil, fmt.Errorf("get workflow %d: %w", id, err)
	}
	nodes, err := s.store.ListNodes(ctx, id)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("list workflow %d nodes: %w", id, err)
	}
	edges, err := s.store.ListEdges(ctx, id)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("list workflow %d edges: %w", id, err)
	}
	return wf, nodes, edges, nil
}

// assembleDetail 整图快照组装详情（loadGraph + model→schema）；Get 未命中缓存的
// 回源与 Create 的组装返回复用同一份；缓存读写由调用方决定。
func (s *workflowService) assembleDetail(ctx context.Context, id uint64) (*workflowapi.WorkflowDetailSchema, error) {
	wf, nodes, edges, err := s.loadGraph(ctx, id)
	if err != nil {
		return nil, err
	}
	return toDetailSchema(wf, nodes, edges), nil
}

// cacheKey workflow 详情的缓存 key（全键 = hify:cache:workflow-cache:{id}，前缀由
// cache 包拼接；api_contract §7 的 hify:workflow:{id} 为简写）。只缓存详情一种形态，
// 无需 agent 式 detail: 前缀。
func cacheKey(id uint64) string { return strconv.FormatUint(id, 10) }

// Get：Cache-Aside 读——命中直接返回（零 store 调用）；未命中三查回源（404 翻译）
// 并回填（TTL 由 cache 包 NameWorkflow 配置管）。缓存读失败视为 miss 回源、回填失败
// 仅 WARN（agent Get 同款容错）。
func (s *workflowService) Get(ctx context.Context, req workflowapi.GetWorkflowReq) (*workflowapi.WorkflowDetailSchema, error) {
	key := cacheKey(req.ID)
	var cached workflowapi.WorkflowDetailSchema
	if found, err := s.cache.Get(ctx, cache.NameWorkflow, key, &cached); err != nil {
		slog.WarnContext(ctx, "read workflow cache failed; fallback to store", "key", key, "err", err)
	} else if found {
		return &cached, nil
	}
	d, err := s.assembleDetail(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if err := s.cache.Set(ctx, cache.NameWorkflow, key, d); err != nil {
		slog.WarnContext(ctx, "fill workflow cache failed; ttl absent for this key", "key", key, "err", err)
	}
	return d, nil
}

// evict 事务提交后删缓存 key；失败仅 WARN 不影响业务（TTL 30min 兜底读到旧值的
// 最坏窗口，agent evict 同款）。配置与状态都在缓存对象里，写路径一律删 key。
func (s *workflowService) evict(ctx context.Context, ids ...uint64) {
	for _, id := range ids {
		key := cacheKey(id)
		if err := s.cache.Delete(ctx, cache.NameWorkflow, key); err != nil {
			slog.WarnContext(ctx, "evict workflow cache failed; ttl fallback", "key", key, "err", err)
		}
	}
}

// List：分页归一化（page<1→1；pageSize<1→20；>100→100，接口规范 B 模式）→
// store.List（行 + 精确 total）→ 摘要组装。不走缓存（极小静态表）。
func (s *workflowService) List(ctx context.Context, req workflowapi.ListWorkflowsReq) (*workflowapi.WorkflowListResult, error) {
	page, pageSize := req.Page, req.PageSize
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	items, total, err := s.store.List(ctx, (page-1)*pageSize, pageSize)
	if err != nil {
		return nil, fmt.Errorf("list workflows: %w", err)
	}
	summaries := make([]workflowapi.WorkflowSummarySchema, 0, len(items))
	for i := range items {
		summaries = append(summaries, toSummarySchema(&items[i]))
	}
	return &workflowapi.WorkflowListResult{Items: summaries, Page: page, PageSize: pageSize, Total: total}, nil
}

// Update：R10 模板引用校验（纯内存）→ 条 9 预检 → store.ReplaceGraph 整图替换单事务
//（affected=0 → 404 哨兵；23505 → 409）→ 事务提交后 evict 删 key → 三查组装 detail
// 返回（status 库里回读，编辑不降级——ReplaceGraph 不写 status）。
func (s *workflowService) Update(ctx context.Context, req workflowapi.UpdateWorkflowReq) (*workflowapi.WorkflowDetailSchema, error) {
	if err := validateTemplateRefs(req.Nodes, req.Edges); err != nil {
		return nil, err
	}
	if err := s.precheckRefs(ctx, req.Nodes); err != nil {
		return nil, err
	}
	wf, nodes, edges := toModel(req.UpsertReq)
	wf.ID = req.ID
	moved, err := s.store.ReplaceGraph(ctx, wf, nodes, edges)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, workflowapi.ErrWorkflowNameConflict // 409
		}
		return nil, fmt.Errorf("update workflow %d: %w", req.ID, err)
	}
	if !moved {
		return nil, workflowapi.ErrWorkflowNotFound // 404
	}
	s.evict(ctx, req.ID)
	return s.assembleDetail(ctx, req.ID)
}

// Delete：store.Delete 硬删（nodes / edges 由 FK CASCADE 清理）；被 agent 绑定
//（agents.workflow_id FK RESTRICT → 23503）→ ErrWorkflowInUse 挡删；affected=0 →
// 404 哨兵。成功后 evict 删 key。
// 错误：workflowapi.ErrWorkflowNotFound、workflowapi.ErrWorkflowInUse。
func (s *workflowService) Delete(ctx context.Context, req workflowapi.DeleteWorkflowReq) error {
	deleted, err := s.store.Delete(ctx, req.ID)
	if err != nil {
		if isFKViolation(err) {
			return workflowapi.ErrWorkflowInUse // 409：被 agent 绑定，先解绑或删 agent
		}
		return fmt.Errorf("delete workflow %d: %w", req.ID, err)
	}
	if !deleted {
		return workflowapi.ErrWorkflowNotFound // 404
	}
	s.evict(ctx, req.ID)
	return nil
}

// Publish：draft / disabled → published；已 published 严格幂等（0 行、updated_at
// 不动）。from 列表排除目标态——db_model §6 状态机 / api_contract §5「无状态变化」；
// spec 04 §3 括号把三种输入态都写进 from 属笔误（用户 2026-09-16 拍板按本语义）。
func (s *workflowService) Publish(ctx context.Context, req workflowapi.PublishWorkflowReq) (*workflowapi.WorkflowSummarySchema, error) {
	return s.changeStatus(ctx, req.ID,
		[]string{string(workflowapi.StatusDraft), string(workflowapi.StatusDisabled)},
		string(workflowapi.StatusPublished))
}

// Disable：仅 published → disabled；disabled / draft 幂等 no-op（状态保持）。
func (s *workflowService) Disable(ctx context.Context, req workflowapi.DisableWorkflowReq) (*workflowapi.WorkflowSummarySchema, error) {
	return s.changeStatus(ctx, req.ID,
		[]string{string(workflowapi.StatusPublished)},
		string(workflowapi.StatusDisabled))
}

// changeStatus 状态动作共用编排：GetByID（404 判定）→ UpdateStatus 原子迁移 →
// 无论 moved 与否 evict 删 key → Get 回源（miss 三查回填，缓存即时刷新）取真实
// 状态组装 summary。GetByID→UpdateStatus 存在竞态窗口（Get 后被删 → 迁移 0 行 →
// 回源 Get 404）：内部工具可接受（spec 04 §3）。
func (s *workflowService) changeStatus(ctx context.Context, id uint64, from []string, to string) (*workflowapi.WorkflowSummarySchema, error) {
	if _, err := s.store.GetByID(ctx, id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, workflowapi.ErrWorkflowNotFound // 404
		}
		return nil, fmt.Errorf("get workflow %d: %w", id, err)
	}
	if _, err := s.store.UpdateStatus(ctx, id, from, to); err != nil {
		return nil, fmt.Errorf("update workflow %d status to %s: %w", id, to, err)
	}
	s.evict(ctx, id)
	d, err := s.Get(ctx, workflowapi.GetWorkflowReq{ID: id})
	if err != nil {
		return nil, err
	}
	return &d.WorkflowSummarySchema, nil
}
