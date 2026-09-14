// Package service 是 agent 模块的业务层：实现 api 的 AgentService（CRUD + 工具绑定维护），
// 定义 Store 数据层接口（本包定义、store 包实现、组合根注入——依赖倒置）。
// model（GORM 实体）模块私有，禁止跨模块；schema↔model 转换、哨兵错误翻译
// （含 PG 23503 FK）发生在实现 api 接口的边界上。
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	agentapi "github.com/Karlsk/go-hify/internal/agent/api"
	"github.com/Karlsk/go-hify/internal/platform/cache"
	"github.com/Karlsk/go-hify/internal/platform/page"
	providerapi "github.com/Karlsk/go-hify/internal/provider/api"
)

// Store 是 agent 模块的数据层接口，只操作本模块声明的表（agents / agent_tools）。
// 错误原样上抛（可 %w 加上下文），业务翻译在 service 层；未找到返回 gorm.ErrRecordNotFound。
type Store interface {
	// CreateAgent 插入 Agent（id / created_at / updated_at 由 DB 生成并经 RETURNING 回填）。
	CreateAgent(ctx context.Context, a *Agent) error
	// GetAgentByID 按主键查（无软删——行恒可见）；未找到返回 gorm.ErrRecordNotFound。
	GetAgentByID(ctx context.Context, id uint64) (*Agent, error)
	// ListAgents 偏移分页（id 升序）。agents 是极小配置表，
	// 按接口规范走偏移分页（keyset 强制规则的例外表）。
	ListAgents(ctx context.Context, p page.OffsetParams) (page.OffsetResult[Agent], error)
	// UpdateAgent 全量 Save（PUT 语义，零值一并覆盖）。前置：a.ID 有效
	// （service 先 GetAgentByID 确认存在）。
	UpdateAgent(ctx context.Context, a *Agent) error
	// DeleteAgent 硬删（真 DELETE）：agent_tools / agent_knowledge_bases 绑定由 FK
	// CASCADE 清理；仍有会话时 PG 抛 23503（conversations FK RESTRICT），由调用方
	// （service.Delete）翻译 ErrAgentInUse。RowsAffected=0 返回 gorm.ErrRecordNotFound。
	DeleteAgent(ctx context.Context, id uint64) error
	// ListToolIDsByAgent 读某 Agent 绑定的工具 id 列表（按绑定先后，id 升序）。
	ListToolIDsByAgent(ctx context.Context, agentID uint64) ([]uint64, error)
	// CountToolsByAgentIDs 列表聚合用批量计数：绑定工具数按 Agent 分组
	// （GROUP BY + 聚合，一条查询覆盖当页全部 id，防 N+1——踩坑 #3）。map 无键 = 0。
	CountToolsByAgentIDs(ctx context.Context, ids []uint64) (map[uint64]int64, error)
	// DeleteToolsByAgent 清空某 Agent 的全部绑定（硬删——append-only 表无软删）。
	// 更新路径在事务内先删后插，uq(agent_id, tool_id) 保证幂等。
	DeleteToolsByAgent(ctx context.Context, agentID uint64) error
	// CreateTools 批量绑定（GORM 切片插入 = 单条多 VALUES INSERT）；空列表直接返回。
	CreateTools(ctx context.Context, agentID uint64, toolIDs []uint64) error
	// WithTx 事务包装：fn 拿到共享同一 tx 句柄的 Store（仍以 Store 接口身份传入）。
	// 事务只包必须原子化的写（agent 行 + 绑定行），事务内禁外部调用。
	WithTx(ctx context.Context, fn func(tx Store) error) error
}

// cacheManager 是 platform/cache 的窄接口：service 只用读 / 写 / 删三个动作，
// 单测可 stub（组合根传 *cache.Cache，结构化类型天然满足）。
type cacheManager interface {
	Get(ctx context.Context, name, key string, dst any) (bool, error)
	Set(ctx context.Context, name, key string, val any) error
	Delete(ctx context.Context, name, key string) error
}

// agent-cache 的 key 形态（全键 = hify:cache:agent-cache:{以下}，前缀由 cache 包拼接）。
// 只缓存详情（chat 引擎按 id 读配置的路径）；列表是低频管理页查询，不进缓存——
// 失效矩阵只有一条边（detail:{id}），无需维护 list key。
const cacheKeyDetail = "detail:%d"

// pgCodeFKViolation PG 外键违例错误码（constraint_violation 类）。
const pgCodeFKViolation = "23503"

// agentService 实现 agentapi.AgentService。
type agentService struct {
	store  Store
	models providerapi.ModelService // 主/备用模型存在性预检（provider 模块 api 注入）
	cache  cacheManager             // 配置类 Cache-Aside（NameAgent，TTL 由 cache 包统一）
}

// New 构造 AgentService；store / models 由组合根注入，cm 传 *cache.Cache（单测可 stub）。
func New(store Store, models providerapi.ModelService, cm cacheManager) agentapi.AgentService {
	return &agentService{store: store, models: models, cache: cm}
}

// ---- AgentService ----

// Create 创建 Agent：模型存在性预检（经 provider api）→ 同一事务内落 agent 行 + 绑定行。
// 错误：providerapi.ErrModelNotFound（主/备用模型不存在，含预检后被并发删除的 FK 兜底）、
// agentapi.ErrToolNotFound（tool_ids 含不存在的工具，FK 23503 翻译）。
func (s *agentService) Create(ctx context.Context, req agentapi.CreateAgentReq) (*agentapi.AgentSchema, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate create agent: %w", err)
	}
	if err := s.checkModels(ctx, req.ModelID, req.FallbackModelID); err != nil {
		return nil, err
	}

	a := toModelCreate(req)
	err := s.store.WithTx(ctx, func(tx Store) error {
		if err := tx.CreateAgent(ctx, a); err != nil {
			if isFKViolation(err) {
				return providerapi.ErrModelNotFound // 预检后被并发删除，FK 兜底
			}
			return fmt.Errorf("create agent: %w", err)
		}
		if err := tx.CreateTools(ctx, a.ID, req.ToolIDs); err != nil {
			if isFKViolation(err) {
				return agentapi.ErrToolNotFound // 工具存在性的唯一校验（mcp 模块未建，db_model.md §4.1）
			}
			return fmt.Errorf("bind tools: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	schema := toSchema(a)
	return &schema, nil
}

// Get 详情（含绑定工具 id）：先查缓存，miss 落库并回填。
// 错误：agentapi.ErrAgentNotFound。
func (s *agentService) Get(ctx context.Context, req agentapi.GetAgentReq) (*agentapi.AgentDetailSchema, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate get agent: %w", err)
	}
	key := fmt.Sprintf(cacheKeyDetail, req.ID)
	var cached agentapi.AgentDetailSchema
	if found, err := s.cache.Get(ctx, cache.NameAgent, key, &cached); err != nil {
		slog.WarnContext(ctx, "read agent cache failed; fallback to db", "key", key, "err", err)
	} else if found {
		return &cached, nil
	}

	a, err := s.store.GetAgentByID(ctx, req.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, agentapi.ErrAgentNotFound
		}
		return nil, fmt.Errorf("get agent %d: %w", req.ID, err)
	}
	toolIDs, err := s.store.ListToolIDsByAgent(ctx, req.ID)
	if err != nil {
		return nil, fmt.Errorf("list agent tools %d: %w", req.ID, err)
	}
	detail := toDetailSchema(a, toolIDs)
	if err := s.cache.Set(ctx, cache.NameAgent, key, detail); err != nil {
		slog.WarnContext(ctx, "set agent cache failed", "key", key, "err", err)
	}
	return &detail, nil
}

// List 活跃 Agent 偏移分页（id 升序）+ 当页聚合（模型展示名 / 工具数）；
// 绑定明细 tool_ids 不进列表（N+1，走 Get 详情）。
func (s *agentService) List(ctx context.Context, req agentapi.ListAgentsReq) (*agentapi.AgentListResult, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate list agents: %w", err)
	}
	res, err := s.store.ListAgents(ctx, page.NewOffset(req.Page, req.PageSize))
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	items := make([]agentapi.AgentListItem, 0, len(res.Items)) // 空页返 [] 不返 null
	for i := range res.Items {
		items = append(items, agentapi.AgentListItem{AgentSchema: toSchema(&res.Items[i])})
	}
	if err := s.withAggregates(ctx, res.Items, items); err != nil {
		return nil, err
	}
	return &agentapi.AgentListResult{
		Items:    items,
		Page:     res.Page,
		PageSize: res.PageSize,
		Total:    res.Total,
	}, nil
}

// withAggregates 当页聚合（批量现读，防 N+1，provider withAggregates 先例）：
// 工具数走本模块 agent_tools 分组计数；模型展示名走 provider api（model_id 去重后
// ListByIDs，去重后 ≤ 页大小 100 不超其上限）。缺行（模型被删的悬空引用）不报错，
// ModelName 留零值 ""——前端 fallback 显示 model_id。
func (s *agentService) withAggregates(ctx context.Context, agents []Agent, items []agentapi.AgentListItem) error {
	agentIDs := make([]uint64, len(agents))
	modelIDs := make([]uint64, 0, len(agents))
	seen := make(map[uint64]struct{}, len(agents))
	for i := range agents {
		agentIDs[i] = agents[i].ID
		if _, dup := seen[agents[i].ModelID]; !dup {
			seen[agents[i].ModelID] = struct{}{}
			modelIDs = append(modelIDs, agents[i].ModelID)
		}
	}
	toolCounts, err := s.store.CountToolsByAgentIDs(ctx, agentIDs)
	if err != nil {
		return fmt.Errorf("count agent tools: %w", err)
	}
	names := make(map[string]string, len(modelIDs))
	if len(modelIDs) > 0 {
		models, err := s.models.ListByIDs(ctx, providerapi.ListModelsByIDsReq{IDs: modelIDs})
		if err != nil {
			return fmt.Errorf("list models by ids: %w", err)
		}
		for i := range models {
			names[models[i].ID] = models[i].Name // 双方同为字符串化 id，键无须回转 uint64
		}
	}
	for i := range items {
		items[i].ToolCount = toolCounts[agents[i].ID]
		items[i].ModelName = names[items[i].ModelID]
	}
	return nil
}

// Update 整体更新（PUT 语义）：先取实体（区分 404 与静默不命中，且保住 created_at 等
// DB 生成列）→ 模型预检 → 同一事务内 Save + 绑定先删后插 → 提交后失效缓存。
// 错误：agentapi.ErrAgentNotFound、providerapi.ErrModelNotFound、agentapi.ErrToolNotFound。
func (s *agentService) Update(ctx context.Context, req agentapi.UpdateAgentReq) (*agentapi.AgentSchema, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate update agent: %w", err)
	}
	a, err := s.store.GetAgentByID(ctx, req.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, agentapi.ErrAgentNotFound
		}
		return nil, fmt.Errorf("get agent %d: %w", req.ID, err)
	}
	if err := s.checkModels(ctx, req.ModelID, req.FallbackModelID); err != nil {
		return nil, err
	}

	applyUpdate(a, req)
	err = s.store.WithTx(ctx, func(tx Store) error {
		if err := tx.UpdateAgent(ctx, a); err != nil {
			if isFKViolation(err) {
				return providerapi.ErrModelNotFound
			}
			return fmt.Errorf("update agent %d: %w", req.ID, err)
		}
		if err := tx.DeleteToolsByAgent(ctx, req.ID); err != nil {
			return fmt.Errorf("unbind tools: %w", err)
		}
		if err := tx.CreateTools(ctx, req.ID, req.ToolIDs); err != nil {
			if isFKViolation(err) {
				return agentapi.ErrToolNotFound
			}
			return fmt.Errorf("bind tools: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// 事务提交后失效缓存（写时删 key；失败仅 WARN，TTL 兜底）。
	s.evict(ctx, fmt.Sprintf(cacheKeyDetail, req.ID))
	schema := toSchema(a)
	return &schema, nil
}

// Delete 真删（决策 #9 修订：软删退役）：agent_tools / agent_knowledge_bases 绑定由
// FK CASCADE 同步清理；有历史会话（conversations.agent_id FK RESTRICT → 23503）→
// ErrAgentInUse 挡删，可先删会话或改停用（enabled=false）。
// 错误：agentapi.ErrAgentNotFound、agentapi.ErrAgentInUse。
func (s *agentService) Delete(ctx context.Context, req agentapi.DeleteAgentReq) error {
	if err := req.Validate(); err != nil {
		return fmt.Errorf("validate delete agent: %w", err)
	}
	if err := s.store.DeleteAgent(ctx, req.ID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return agentapi.ErrAgentNotFound
		}
		if isFKViolation(err) {
			return agentapi.ErrAgentInUse // 仍有会话引用，挡删
		}
		return fmt.Errorf("delete agent %d: %w", req.ID, err)
	}
	s.evict(ctx, fmt.Sprintf(cacheKeyDetail, req.ID))
	return nil
}

// ---- 内部辅助 ----

// checkModels 校验主/备用模型存在（provider 模块哨兵直接透传）。只挡「引用不存在的
// 模型」；能力（chat/embedding）与启用状态由 chat 引擎运行时把关——配置期不越权判断。
func (s *agentService) checkModels(ctx context.Context, modelID uint64, fallback *uint64) error {
	if _, err := s.models.Get(ctx, providerapi.GetModelReq{ID: modelID}); err != nil {
		if errors.Is(err, providerapi.ErrModelNotFound) {
			return err
		}
		return fmt.Errorf("check model %d: %w", modelID, err)
	}
	if fallback != nil {
		if _, err := s.models.Get(ctx, providerapi.GetModelReq{ID: *fallback}); err != nil {
			if errors.Is(err, providerapi.ErrModelNotFound) {
				return err
			}
			return fmt.Errorf("check fallback model %d: %w", *fallback, err)
		}
	}
	return nil
}

// evict 事务提交后删缓存 key；失败仅 WARN 不影响业务（TTL 30min 兜底读到旧值的最坏窗口）。
func (s *agentService) evict(ctx context.Context, keys ...string) {
	for _, key := range keys {
		if err := s.cache.Delete(ctx, cache.NameAgent, key); err != nil {
			slog.WarnContext(ctx, "evict agent cache failed; ttl fallback", "key", key, "err", err)
		}
	}
}

// isFKViolation 判断 PG 外键违例（23503）。
func isFKViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgCodeFKViolation
}

// resolveTemperature 未传 → 缺省 0.7；显式 0（严谨模式）合法，指针区分两者。
func resolveTemperature(t *float64) float64 {
	if t == nil {
		return agentapi.DefaultTemperature
	}
	return *t
}

// resolveMaxContextTurns 未传 → 缺省 10；指针区分「未传」与显式值。
func resolveMaxContextTurns(t *int) int {
	if t == nil {
		return agentapi.DefaultMaxContextTurns
	}
	return *t
}

// resolveEnabled 未传 → true（创建即启用；PUT 全量未传视为启用——
// 显式传 false 才停用，指针防 Go 零值吞掉该语义）。
func resolveEnabled(e *bool) bool {
	if e == nil {
		return true
	}
	return *e
}

// toModelCreate 创建请求 → model（id / 时间戳由 DB 生成）。
func toModelCreate(req agentapi.CreateAgentReq) *Agent {
	return &Agent{
		Name:            req.Name,
		Description:     req.Description,
		ModelID:         req.ModelID,
		FallbackModelID: req.FallbackModelID,
		SystemPrompt:    req.SystemPrompt,
		Temperature:     resolveTemperature(req.Temperature),
		MaxOutputTokens: req.MaxOutputTokens,
		MaxContextTurns: resolveMaxContextTurns(req.MaxContextTurns),
		Enabled:         resolveEnabled(req.Enabled),
	}
}

// applyUpdate 在已取实体上就地覆盖业务字段（保住 ID / created_at 等 DB 生成列；
// Go 惯用的指针接收器变更，模块内私有实体无需不可变拷贝）。
func applyUpdate(a *Agent, req agentapi.UpdateAgentReq) {
	a.Name = req.Name
	a.Description = req.Description
	a.ModelID = req.ModelID
	a.FallbackModelID = req.FallbackModelID
	a.SystemPrompt = req.SystemPrompt
	a.Temperature = resolveTemperature(req.Temperature)
	a.MaxOutputTokens = req.MaxOutputTokens
	a.MaxContextTurns = resolveMaxContextTurns(req.MaxContextTurns)
	a.Enabled = resolveEnabled(req.Enabled)
}

// toSchema model → 响应 schema（id / 外键字符串化，接口规范）。
func toSchema(a *Agent) agentapi.AgentSchema {
	s := agentapi.AgentSchema{
		Name:            a.Name,
		Description:     a.Description,
		ModelID:         strconv.FormatUint(a.ModelID, 10),
		SystemPrompt:    a.SystemPrompt,
		Temperature:     a.Temperature,
		MaxOutputTokens: a.MaxOutputTokens,
		MaxContextTurns: a.MaxContextTurns,
		Enabled:         a.Enabled,
	}
	s.ID = strconv.FormatUint(a.ID, 10)
	s.CreatedAt = a.CreatedAt
	s.UpdatedAt = a.UpdatedAt
	if a.FallbackModelID != nil {
		fb := strconv.FormatUint(*a.FallbackModelID, 10)
		s.FallbackModelID = &fb
	}
	return s
}

// toDetailSchema model + 绑定 id → 详情 schema；ToolIDs 用 make 初始化
// （空绑定序列化成 [] 而非 null，接口规范《空值约定》）。
func toDetailSchema(a *Agent, toolIDs []uint64) agentapi.AgentDetailSchema {
	d := agentapi.AgentDetailSchema{
		AgentSchema: toSchema(a),
		ToolIDs:     make([]string, 0, len(toolIDs)),
	}
	for _, id := range toolIDs {
		d.ToolIDs = append(d.ToolIDs, strconv.FormatUint(id, 10))
	}
	return d
}
