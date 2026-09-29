// Package service 是 mcp 模块的业务层：实现 api 契约（MCPServerService +
// MCPClientService 同一实例两张面孔，server_mgmt_spec.md §3），数据访问经本包定义的
// Store 接口（依赖倒置：store 包实现、组合根注入），MCP 协议调用经 mcpDialer
// （go-sdk 封装，mcpclient.go）。
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	mcpapi "github.com/Karlsk/go-hify/internal/mcp/api"
	"github.com/Karlsk/go-hify/internal/platform/cache"
	"github.com/Karlsk/go-hify/internal/platform/page"
)

// Store 数据层接口：定义在消费方（本包），store 包实现，组合根注入——依赖倒置。
// 只覆盖本模块声明的表（mcp_servers / mcp_tools）；错误原样上抛，业务翻译
// （哨兵 / 23505 / 23503）在 service 层做。
//
// 无 WithTx：本模块全部写路径均为单语句——批量 upsert 本身是单条
// INSERT ... ON CONFLICT（天然原子），CRUD 均单行写；chat 批次若引入跨语句写再补。
type Store interface {
	// CreateServer 插入（id / created_at / updated_at 由 DB 生成并经 RETURNING 回填）。
	CreateServer(ctx context.Context, s *MCPServer) error
	// GetServerByID 按主键查；未找到返回 gorm.ErrRecordNotFound。
	GetServerByID(ctx context.Context, id uint64) (*MCPServer, error)
	// ListServersByIDs 批量取（agent 绑定校验读所属 server 的 enabled 标志）；
	// 缺失 id 静默不在结果里，存在性判定由调用方比对 id 集合。
	ListServersByIDs(ctx context.Context, ids []uint64) ([]MCPServer, error)
	// ListServers 偏移分页（id 升序；极小配置表走 B 模式）：先精确 Count 再取当页。
	ListServers(ctx context.Context, p page.OffsetParams) (page.OffsetResult[MCPServer], error)
	// UpdateServer 全量 Save（PUT 语义，零值一并覆盖）；前置 s.ID 有效
	// （service 层先 GetServerByID 取得）。
	UpdateServer(ctx context.Context, s *MCPServer) error
	// DeleteServer 硬删；mcp_tools 由 FK 级联删——有 Agent 绑定时被
	// fk_agent_tools_tool_id RESTRICT 挡（PG 23503 → service 翻译 ErrMCPServerInUse）；
	// RowsAffected=0 返回 gorm.ErrRecordNotFound。
	DeleteServer(ctx context.Context, id uint64) error

	// UpsertTools discover 批量 upsert：单条多 VALUES INSERT ... ON CONFLICT
	// (mcp_server_id, name) DO UPDATE，新增行插入、已有行刷新 description /
	// input_schema / updated_at；只增改不删（db_model.md §2.4）；空切片直接返回。
	UpsertTools(ctx context.Context, tools []MCPTool) error
	// ListToolsByServer 某 server 的工具清单（name 升序，详情页展示）。
	ListToolsByServer(ctx context.Context, serverID uint64) ([]MCPTool, error)
	// CountToolsByServerIDs 列表聚合用批量计数（GROUP BY 一条查询覆盖当页，防 N+1）；
	// map 无键 = 0。
	CountToolsByServerIDs(ctx context.Context, ids []uint64) (map[uint64]int64, error)
	// ListToolsByIDs 批量取工具（agent 绑定校验经 mcpapi 间接消费）；缺失 id 静默
	// 不在结果里，存在性判定由调用方比对 id 集合。
	ListToolsByIDs(ctx context.Context, ids []uint64) ([]MCPTool, error)
}

// cacheManager 是 platform/cache 的窄接口：service 只用读 / 写 / 删三个动作，
// 单测可 stub（组合根传 *cache.Cache，结构化类型天然满足）。
type cacheManager interface {
	Get(ctx context.Context, name, key string, dst any) (bool, error)
	Set(ctx context.Context, name, key string, val any) error
	Delete(ctx context.Context, name, key string) error
}

// 缓存 key 形态（全键 = hify:cache:{name}:{以下}，前缀由 cache 包拼接）。
// 失效矩阵（db_model.md §2.5）：server 实体 / tools 清单各一条边——server 行写
// （Update / Delete）失效 server key；tools 行写（discover upsert / Delete 级联）
// 失效 tools key；列表是低频管理页查询不进缓存（agent 同款）。
const (
	cacheKeyServer = "server:%d"
	cacheKeyTools  = "tools:%d"
)

// PG 错误码与约束名（翻译依据）。
const (
	pgCodeUniqueViolation = "23505"
	pgCodeFKViolation     = "23503"
	// fkAgentToolsToolID agent_tools.tool_id 外键约束名（迁移 00021 显式命名），
	// Delete 撞 23503 的分发依据。
	fkAgentToolsToolID = "fk_agent_tools_tool_id"
)

// maxErrMessageLen discover 失败摘要截断上限（装进响应字段，防上游错误刷屏）。
const maxErrMessageLen = 512

// service 实现 mcpapi 两接口（同一实例，组合根按面孔分别注入）。
type service struct {
	store  Store
	cache  cacheManager
	dialer mcpDialer
}

var (
	_ mcpapi.MCPServerService = (*service)(nil)
	_ mcpapi.MCPClientService = (*service)(nil)
)

// New 构造 mcp service：MCPServerService（mcp handler 消费）与 MCPClientService
// （agent / chat 消费）两张面孔同一实例。cm 传 *cache.Cache（单测可 stub）；
// dialer 内置 go-sdk 实现（单测在包内构造 stub 注入 *service）。
func New(store Store, cm cacheManager) (mcpapi.MCPServerService, mcpapi.MCPClientService) {
	s := &service{store: store, cache: cm, dialer: goSDKDialer{}}
	return s, s
}

// ---- MCPServerService（管理面） ----

// Create 创建 MCP Server（创建即启用，enabled 未传 = true）。
// 错误：mcpapi.ErrMCPServerNameConflict（name 撞 uq_mcp_servers_name，23505 翻译）。
func (s *service) Create(ctx context.Context, req mcpapi.CreateMCPServerReq) (*mcpapi.MCPServerSchema, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate create mcp server: %w", err)
	}
	sv := toModelCreate(req)
	if err := s.store.CreateServer(ctx, sv); err != nil {
		if isUniqueViolation(err) {
			return nil, mcpapi.ErrMCPServerNameConflict
		}
		return nil, fmt.Errorf("create mcp server: %w", err)
	}
	out := toServerSchema(sv)
	return &out, nil
}

// Get 详情（含 env 与工具清单，均 DB 读不连 server）：server 走 Cache-Aside，
// tools 单独缓存（server 实体不含 tool 数据，失效矩阵两条边独立）。
// 错误：mcpapi.ErrMCPServerNotFound。
func (s *service) Get(ctx context.Context, req mcpapi.GetMCPServerReq) (*mcpapi.MCPServerDetailSchema, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate get mcp server: %w", err)
	}
	sv, err := s.getServer(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	tools, err := s.listTools(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	return &mcpapi.MCPServerDetailSchema{
		MCPServerSchema: toServerSchema(sv),
		Env:             nonNilEnv(sv.Env),
		Tools:           tools,
	}, nil
}

// List 偏移分页（id 升序）+ 当页 tool_count 批量计数（一条 GROUP BY 覆盖当页，
// 防 N+1——provider 踩坑 #3 先例）；列表低频管理页查询不进缓存。
func (s *service) List(ctx context.Context, req mcpapi.ListMCPServersReq) (*mcpapi.MCPServerListResult, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate list mcp servers: %w", err)
	}
	res, err := s.store.ListServers(ctx, page.NewOffset(req.Page, req.PageSize))
	if err != nil {
		return nil, fmt.Errorf("list mcp servers: %w", err)
	}
	items := make([]mcpapi.MCPServerListItem, 0, len(res.Items)) // 空页返 [] 不返 null
	ids := make([]uint64, len(res.Items))
	for i := range res.Items {
		items = append(items, mcpapi.MCPServerListItem{MCPServerSchema: toServerSchema(&res.Items[i])})
		ids[i] = res.Items[i].ID
	}
	counts, err := s.store.CountToolsByServerIDs(ctx, ids) // 空页零 SQL（store 短路）
	if err != nil {
		return nil, fmt.Errorf("count tools: %w", err)
	}
	for i := range items {
		items[i].ToolCount = counts[res.Items[i].ID]
	}
	return &mcpapi.MCPServerListResult{Items: items, Page: res.Page, PageSize: res.PageSize, Total: res.Total}, nil
}

// Update 整体更新（PUT 全量；enabled 未传 = true）。
// 错误：mcpapi.ErrMCPServerNotFound、mcpapi.ErrMCPServerNameConflict。
func (s *service) Update(ctx context.Context, req mcpapi.UpdateMCPServerReq) (*mcpapi.MCPServerSchema, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate update mcp server: %w", err)
	}
	// 写路径直读 DB（不经缓存，agent Update 同款）：缓存实体只服务读路径。
	sv, err := s.store.GetServerByID(ctx, req.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, mcpapi.ErrMCPServerNotFound
		}
		return nil, fmt.Errorf("get mcp server %d: %w", req.ID, err)
	}
	applyUpdate(sv, req)
	if err := s.store.UpdateServer(ctx, sv); err != nil {
		if isUniqueViolation(err) {
			return nil, mcpapi.ErrMCPServerNameConflict
		}
		return nil, fmt.Errorf("update mcp server %d: %w", req.ID, err)
	}
	s.evictServer(ctx, req.ID)
	out := toServerSchema(sv)
	return &out, nil
}

// Delete 真删：mcp_tools 级联删；有 Agent 绑定时被 fk_agent_tools_tool_id
// RESTRICT 挡（23503 → ErrMCPServerInUse，可解绑后删或改停用）。
// 错误：mcpapi.ErrMCPServerNotFound、mcpapi.ErrMCPServerInUse。
func (s *service) Delete(ctx context.Context, req mcpapi.DeleteMCPServerReq) error {
	if err := req.Validate(); err != nil {
		return fmt.Errorf("validate delete mcp server: %w", err)
	}
	if err := s.store.DeleteServer(ctx, req.ID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return mcpapi.ErrMCPServerNotFound
		}
		if sent, ok := translateDeleteFK(err); ok {
			return sent // 有 Agent 绑定挡删
		}
		return fmt.Errorf("delete mcp server %d: %w", req.ID, err)
	}
	// server key + tools key 一并失效：级联删清了 mcp_tools 行，tools 缓存若不删
	// 会在 TTL 窗口内继续供出已死工具（db_model.md §2.5 矩阵的 Delete 行补全）。
	s.evictServer(ctx, req.ID)
	s.evictTools(ctx, req.ID)
	return nil
}

// Discover 连通测试 + 工具发现：事务外连接 + tools/list（踩坑 5：外部调用禁入
// 事务；upsert 本身单语句原子）→ upsert mcp_tools（只增改不删）→ 失效 tools 缓存。
// 连通失败也是 200（成败在 DiscoverResultSchema，provider TestConnection 同款）；
// server 不存在（404）与 DB 故障（500）才是错误。
// 错误：mcpapi.ErrMCPServerNotFound。
func (s *service) Discover(ctx context.Context, req mcpapi.DiscoverReq) (*mcpapi.DiscoverResultSchema, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate discover: %w", err)
	}
	sv, err := s.store.GetServerByID(ctx, req.ID) // 动作前直读：不存在是 404，不是连通失败
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, mcpapi.ErrMCPServerNotFound
		}
		return nil, fmt.Errorf("get mcp server %d: %w", req.ID, err)
	}

	tools, latency, derr := s.dialer.discover(ctx, sv)
	if derr != nil {
		slog.WarnContext(ctx, "mcp discover failed", "server_id", sv.ID, "server", sv.Name,
			"transport", sv.Transport, "err", derr)
		return &mcpapi.DiscoverResultSchema{
			Success:      false,
			LatencyMs:    int32(latency.Milliseconds()),
			ErrorMessage: truncateMessage(derr.Error()),
		}, nil
	}
	for i := range tools {
		tools[i].MCPServerID = sv.ID
	}
	if err := s.store.UpsertTools(ctx, tools); err != nil {
		return nil, fmt.Errorf("upsert tools: %w", err) // DB 故障是真错误，不是连通失败
	}
	s.evictTools(ctx, sv.ID)
	counts, err := s.store.CountToolsByServerIDs(ctx, []uint64{sv.ID})
	if err != nil {
		return nil, fmt.Errorf("count tools: %w", err)
	}
	return &mcpapi.DiscoverResultSchema{
		Success:   true,
		LatencyMs: int32(latency.Milliseconds()),
		ToolCount: counts[sv.ID], // upsert 后该 server 工具总数（含既往 discover 遗留行——只增改不删）
	}, nil
}

// RunTool 实调一个工具（管理面诊断）：DB 工具清单先行校验（discover 后才有行），
// 经 mcpDialer per-call 调用。
// 错误：mcpapi.ErrMCPServerNotFound、mcpapi.ErrMCPToolNotFound（工具不在该
// server 的 mcp_tools 行内）、mcpapi.ErrMCPToolCallFailed（连接 / 调用失败或
// isError，message 带上游摘要）。
func (s *service) RunTool(ctx context.Context, req mcpapi.RunToolReq) (*mcpapi.CallToolResult, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate run tool: %w", err)
	}
	sv, err := s.getServer(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	tools, err := s.listTools(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	var known bool
	for _, t := range tools {
		if t.Name == req.ToolName {
			known = true
			break
		}
	}
	if !known {
		return nil, fmt.Errorf("%w: %s", mcpapi.ErrMCPToolNotFound, req.ToolName)
	}
	content, err := s.dialer.call(ctx, sv, req.ToolName, req.Arguments)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", mcpapi.ErrMCPToolCallFailed, err)
	}
	return &mcpapi.CallToolResult{Content: content}, nil
}

// ---- MCPClientService（调用面：agent 绑定校验本批、chat 工具循环下批） ----

// CallTool 跨模块工具调用：server 经缓存取（chat 阶段的热路径），per-call client
// 调用。不检查 enabled——调用方组装工具时自行排除停用 server（绑定校验已挡新增
// 绑定，server_mgmt_spec.md §3）。
// 错误：mcpapi.ErrMCPServerNotFound、mcpapi.ErrMCPToolCallFailed。
func (s *service) CallTool(ctx context.Context, req mcpapi.CallToolReq) (*mcpapi.CallToolResult, error) {
	sv, err := s.getServer(ctx, req.MCPServerID)
	if err != nil {
		return nil, err
	}
	content, err := s.dialer.call(ctx, sv, req.ToolName, req.Arguments)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", mcpapi.ErrMCPToolCallFailed, err)
	}
	return &mcpapi.CallToolResult{Content: content}, nil
}

// ListTools 读某 server 的工具清单（DB 读不连 server，Cache-Aside）。
// 错误：mcpapi.ErrMCPServerNotFound。
func (s *service) ListTools(ctx context.Context, req mcpapi.ListToolsReq) ([]mcpapi.ToolSchema, error) {
	if _, err := s.getServer(ctx, req.MCPServerID); err != nil {
		return nil, err // ErrMCPServerNotFound
	}
	return s.listTools(ctx, req.MCPServerID)
}

// GetToolsByIDs 批量取工具 + 所属 server 启用标志（agent 绑定校验「存在且
// server 启用」的依据）；缺失 id 静默不在结果里，存在性判定由调用方比对
// id 集合。直读 DB（管理动线，不进缓存）。
func (s *service) GetToolsByIDs(ctx context.Context, req mcpapi.GetToolsByIDsReq) ([]mcpapi.ToolCheck, error) {
	if len(req.IDs) == 0 {
		return []mcpapi.ToolCheck{}, nil
	}
	tools, err := s.store.ListToolsByIDs(ctx, req.IDs)
	if err != nil {
		return nil, fmt.Errorf("list tools by ids: %w", err)
	}
	serverIDs := make([]uint64, 0, len(tools))
	seen := make(map[uint64]struct{}, len(tools))
	for i := range tools {
		if _, dup := seen[tools[i].MCPServerID]; !dup {
			seen[tools[i].MCPServerID] = struct{}{}
			serverIDs = append(serverIDs, tools[i].MCPServerID)
		}
	}
	servers, err := s.store.ListServersByIDs(ctx, serverIDs)
	if err != nil {
		return nil, fmt.Errorf("list servers by ids: %w", err)
	}
	enabled := make(map[uint64]bool, len(servers))
	for i := range servers {
		enabled[servers[i].ID] = servers[i].Enabled
	}
	checks := make([]mcpapi.ToolCheck, 0, len(tools))
	for i := range tools {
		checks = append(checks, mcpapi.ToolCheck{
			ToolSchema:    toToolSchema(&tools[i]),
			ServerEnabled: enabled[tools[i].MCPServerID], // server 行缺失（理论不可达，FK 兜底）→ false
		})
	}
	return checks, nil
}

// ---- 内部辅助 ----

// getServer 按 id 取 server 实体（Cache-Aside，NameMCPServer；Get / RunTool /
// CallTool / ListTools 共用）。错误：mcpapi.ErrMCPServerNotFound。
func (s *service) getServer(ctx context.Context, id uint64) (*MCPServer, error) {
	key := fmt.Sprintf(cacheKeyServer, id)
	var cached MCPServer
	if found, err := s.cache.Get(ctx, cache.NameMCPServer, key, &cached); err != nil {
		slog.WarnContext(ctx, "read mcp server cache failed; fallback to db", "key", key, "err", err)
	} else if found {
		return &cached, nil
	}
	sv, err := s.store.GetServerByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, mcpapi.ErrMCPServerNotFound
		}
		return nil, fmt.Errorf("get mcp server %d: %w", id, err)
	}
	if err := s.cache.Set(ctx, cache.NameMCPServer, key, sv); err != nil {
		slog.WarnContext(ctx, "set mcp server cache failed", "key", key, "err", err)
	}
	return sv, nil
}

// listTools 某 server 的工具清单（Cache-Aside，NameMCPTool）；非 nil（空返 []）。
func (s *service) listTools(ctx context.Context, serverID uint64) ([]mcpapi.ToolSchema, error) {
	key := fmt.Sprintf(cacheKeyTools, serverID)
	var cached []mcpapi.ToolSchema
	if found, err := s.cache.Get(ctx, cache.NameMCPTool, key, &cached); err != nil {
		slog.WarnContext(ctx, "read mcp tools cache failed; fallback to db", "key", key, "err", err)
	} else if found {
		return cached, nil
	}
	tools, err := s.store.ListToolsByServer(ctx, serverID)
	if err != nil {
		return nil, fmt.Errorf("list tools by server %d: %w", serverID, err)
	}
	out := make([]mcpapi.ToolSchema, 0, len(tools))
	for i := range tools {
		out = append(out, toToolSchema(&tools[i]))
	}
	if err := s.cache.Set(ctx, cache.NameMCPTool, key, out); err != nil {
		slog.WarnContext(ctx, "set mcp tools cache failed", "key", key, "err", err)
	}
	return out, nil
}

// evictServer / evictTools 写时删 key（失败仅 WARN，TTL 30min 兜底读到旧值的
// 最坏窗口）。
func (s *service) evictServer(ctx context.Context, id uint64) {
	if err := s.cache.Delete(ctx, cache.NameMCPServer, fmt.Sprintf(cacheKeyServer, id)); err != nil {
		slog.WarnContext(ctx, "evict mcp server cache failed; ttl fallback", "id", id, "err", err)
	}
}

func (s *service) evictTools(ctx context.Context, id uint64) {
	if err := s.cache.Delete(ctx, cache.NameMCPTool, fmt.Sprintf(cacheKeyTools, id)); err != nil {
		slog.WarnContext(ctx, "evict mcp tools cache failed; ttl fallback", "id", id, "err", err)
	}
}

// isUniqueViolation 判断 PG 唯一约束违例（23505）。
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgCodeUniqueViolation
}

// translateDeleteFK Delete 的 23503 按约束名分发：fk_agent_tools_tool_id（有
// Agent 绑定挡删）→ ErrMCPServerInUse；其余 23503 不翻译（mcp_servers 无其他
// 引用方，理论不达），由调用方原样包装上抛。非 23503 返回 false。
func translateDeleteFK(err error) (error, bool) {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != pgCodeFKViolation {
		return nil, false
	}
	if pgErr.ConstraintName == fkAgentToolsToolID {
		return mcpapi.ErrMCPServerInUse, true
	}
	return nil, false
}

// resolveEnabled 未传 → true（创建即启用 / PUT 全量未传视为启用；显式传 false
// 才停用，指针防 Go 零值吞掉该语义）。
func resolveEnabled(e *bool) bool {
	if e == nil {
		return true
	}
	return *e
}

// truncateMessage rune 边界安全截断（发现失败摘要装进 DiscoverResultSchema）。
func truncateMessage(msg string) string {
	r := []rune(msg)
	if len(r) <= maxErrMessageLen {
		return msg
	}
	return string(r[:maxErrMessageLen]) + "…"
}

// toModelCreate 创建请求 → model（id / 时间戳由 DB 生成）。
func toModelCreate(req mcpapi.CreateMCPServerReq) *MCPServer {
	return &MCPServer{
		Name:      req.Name,
		Transport: req.Transport,
		Command:   req.Command,
		URL:       req.URL,
		Env:       nonNilEnv(req.Env),
		Enabled:   resolveEnabled(req.Enabled),
	}
}

// applyUpdate 在已取实体上就地覆盖业务字段（PUT 全量；保住 ID / created_at 等
// DB 生成列——Go 惯用的指针接收器变更，模块内私有实体）。
func applyUpdate(sv *MCPServer, req mcpapi.UpdateMCPServerReq) {
	sv.Name = req.Name
	sv.Transport = req.Transport
	sv.Command = req.Command
	sv.URL = req.URL
	sv.Env = nonNilEnv(req.Env)
	sv.Enabled = resolveEnabled(req.Enabled)
}

// nonNilEnv nil map 兜底空 map（防 jsonb 写 null，db_model.md §4 注）。
func nonNilEnv(env map[string]string) map[string]string {
	if env == nil {
		return map[string]string{}
	}
	return env
}

// toServerSchema model → 响应 Schema（摘要面：无 env——仅详情回传）。
func toServerSchema(sv *MCPServer) mcpapi.MCPServerSchema {
	out := mcpapi.MCPServerSchema{
		Name:      sv.Name,
		Transport: sv.Transport,
		Command:   sv.Command,
		URL:       sv.URL,
		Enabled:   sv.Enabled,
	}
	out.ID = strconv.FormatUint(sv.ID, 10)
	out.CreatedAt = sv.CreatedAt
	out.UpdatedAt = sv.UpdatedAt
	return out
}

// toToolSchema model → 工具契约（input_schema 原样透传，序列化是 map 的天然产物）。
func toToolSchema(t *MCPTool) mcpapi.ToolSchema {
	return mcpapi.ToolSchema{
		ID:          strconv.FormatUint(t.ID, 10),
		MCPServerID: strconv.FormatUint(t.MCPServerID, 10),
		Name:        t.Name,
		Description: t.Description,
		InputSchema: marshalSchema(t.InputSchema),
	}
}

// marshalSchema map → RawMessage（nil → {}；序列化失败兜底 {}——map 值均来自
// JSON 反序列化，实际不可达）。
func marshalSchema(m map[string]any) json.RawMessage {
	if m == nil {
		return json.RawMessage("{}")
	}
	b, err := json.Marshal(m)
	if err != nil {
		return json.RawMessage("{}")
	}
	return b
}
