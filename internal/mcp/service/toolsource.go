package service

// toolsource.go：mcp 侧统一工具来源（platform/tool.Source 的 MCP 实现，
// chat_tool_loop_spec.md §4）。ToolIDs 是 agent 绑定的 mcp_tools.id；真实加载
// （查库）在 Tools / Invoke 的 ctx 上惰性发生，首次成功后单轮内缓存复用。
//
// 元数据来自 mcp_tools DB 行（discover 入库的快照），不实时 list_tools——DB 是
// 绑定关系的锚点（agent_tools.tool_id），运行时拉全量清单反而会漂移。

import (
	"context"
	"fmt"
	"strings"
	"sync"

	mcpapi "github.com/Karlsk/go-hify/internal/mcp/api"
	"github.com/Karlsk/go-hify/internal/platform/tool"
)

const (
	// callNameMaxLen 调用名上限（OpenAI 函数名约束 64 字符；其余供应商等宽或更宽松）。
	callNameMaxLen = 64
	// maxErrTextRunes Invoke 错误文本截断上限（喂回 LLM 的错误不需要长文；chat 侧
	// 对 Content 另有 20000 rune 统一截断，这里是源头侧防御）。
	maxErrTextRunes = 2000
)

// resolvedTool ensure 解析后的工具行：ToolDef（对外契约）+ 路由信息（Invoke 用）。
type resolvedTool struct {
	def      tool.ToolDef
	serverID uint64
	toolName string // 原始工具名（mcp_tools.name，server 内唯一）
}

// boundToolSource 绑定工具子集的 tool.Source 实现：Tools 出清单、Invoke 按调用名
// 路由到所属 server per-call 调用。单 chat 轮内复用一个实例（ensure 缓存）；
// mu 只保护 ensure 的惰性加载（Tools / Invoke 可能并发到达）。
// 绑定面 = toolIDs ∪ serverIDs 并集（spec 020：server 级 = 其全部工具动态全量）。
type boundToolSource struct {
	svc       *service
	toolIDs   []uint64
	serverIDs []uint64

	mu       sync.Mutex
	loaded   bool
	resolved []resolvedTool
	byName   map[string]int // 调用名 → resolved 下标（append 扩容安全，不存元素指针）
}

// 编译期断言：实现统一工具契约。
var _ tool.Source = (*boundToolSource)(nil)

// ToolSourceFor 构造绑定工具来源（工厂方法，无 IO；mcpapi.MCPClientService 面）。
func (s *service) ToolSourceFor(req mcpapi.ToolSourceReq) tool.Source {
	return &boundToolSource{svc: s, toolIDs: req.ToolIDs, serverIDs: req.ServerIDs}
}

// Tools 返回启用 server 下的工具清单（空切片 = 无工具）。停用 server 的工具
// 静默消失；DB 错误原样上抛（由 Registry / chat 降级处理）。
func (b *boundToolSource) Tools(ctx context.Context) ([]tool.ToolDef, error) {
	if err := b.ensure(ctx); err != nil {
		return nil, err
	}
	defs := make([]tool.ToolDef, len(b.resolved))
	for i := range b.resolved {
		defs[i] = b.resolved[i].def
	}
	return defs, nil
}

// Invoke 按调用名路由执行。恒返回结果（tool.Source 契约）：未知名 / 加载失败 /
// 调用失败都是 IsError=true + 错误文本——错误即上下文，LLM 下一轮自纠。
func (b *boundToolSource) Invoke(ctx context.Context, call tool.ToolCall) tool.ToolResult {
	if err := b.ensure(ctx); err != nil {
		return tool.ToolResult{CallID: call.ID, Name: call.Name, IsError: true,
			Content: truncateRunes(fmt.Sprintf("加载工具清单失败: %v", err), maxErrTextRunes)}
	}
	rt, ok := b.byName[call.Name]
	if !ok {
		return tool.ToolResult{CallID: call.ID, Name: call.Name, IsError: true,
			Content: fmt.Sprintf("未知工具 %q：不在可用工具清单内", call.Name)}
	}
	target := b.resolved[rt]
	sv, err := b.svc.getServer(ctx, target.serverID)
	if err != nil {
		return tool.ToolResult{CallID: call.ID, Name: call.Name, IsError: true,
			Content: truncateRunes(fmt.Sprintf("获取工具服务器失败: %v", err), maxErrTextRunes)}
	}
	content, err := b.svc.dialer.call(ctx, sv, target.toolName, call.Arguments)
	if err != nil {
		return tool.ToolResult{CallID: call.ID, Name: call.Name, IsError: true,
			Content: truncateRunes(err.Error(), maxErrTextRunes)}
	}
	return tool.ToolResult{CallID: call.ID, Name: call.Name, Content: content}
}

// ensure 惰性加载绑定工具：toolIDs ∪ serverIDs 并集（按 tool id 去重，toolIDs 序
// 优先）+ ListServersByIDs → 过滤停用 server → 派生调用名 → 生成 ToolDef。首次成功
// 后缓存（单轮 Tools 与 Invoke 不重复查库）；失败不缓存（下次调用重试）。缺失 id
// 静默跳过（agent 绑定校验已挡新增绑定，行删除走 mcp_tools 级联 + agent_tools 同删，
// 理论不达）；server 级面 = 执行期动态全量（discover 新增工具自动纳入）。
func (b *boundToolSource) ensure(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.loaded {
		return nil
	}
	if len(b.toolIDs) == 0 && len(b.serverIDs) == 0 {
		b.resolved = nil
		b.byName = map[string]int{}
		b.loaded = true
		return nil
	}
	var tools []MCPTool
	if len(b.toolIDs) > 0 {
		byIDs, err := b.svc.store.ListToolsByIDs(ctx, b.toolIDs)
		if err != nil {
			return fmt.Errorf("list bound tools: %w", err)
		}
		tools = byIDs
	}
	if len(b.serverIDs) > 0 {
		byServer, err := b.svc.store.ListToolsByServerIDs(ctx, b.serverIDs)
		if err != nil {
			return fmt.Errorf("list server tools: %w", err)
		}
		tools = unionToolsByID(tools, byServer)
	}
	serverIDs := make([]uint64, 0, len(tools))
	seen := make(map[uint64]struct{}, len(tools))
	for i := range tools {
		if _, dup := seen[tools[i].MCPServerID]; !dup {
			seen[tools[i].MCPServerID] = struct{}{}
			serverIDs = append(serverIDs, tools[i].MCPServerID)
		}
	}
	servers, err := b.svc.store.ListServersByIDs(ctx, serverIDs)
	if err != nil {
		return fmt.Errorf("list tool servers: %w", err)
	}
	enabled := make(map[uint64]bool, len(servers))
	for i := range servers {
		enabled[servers[i].ID] = servers[i].Enabled
	}
	b.resolved = make([]resolvedTool, 0, len(tools))
	b.byName = make(map[string]int, len(tools))
	for i := range tools {
		if !enabled[tools[i].MCPServerID] {
			continue // server 停用：工具静默消失（存量绑定自动失效）
		}
		rt := resolvedTool{
			def: tool.ToolDef{
				Name:        deriveCallName(tools[i].MCPServerID, tools[i].Name),
				Description: tools[i].Description,
				InputSchema: marshalSchema(tools[i].InputSchema),
			},
			serverID: tools[i].MCPServerID,
			toolName: tools[i].Name,
		}
		b.byName[rt.def.Name] = len(b.resolved) // 同名撞车由 Registry 构建期校验挡（后写者胜仅是过渡态）
		b.resolved = append(b.resolved, rt)
	}
	b.loaded = true
	return nil
}

// unionToolsByID 并集去重（spec 020 C4）：base 序优先，extra 中仅补 base 未含的
// tool id（server 级面与 tool 级面重叠时只解析一次，调用名唯一性不变）。
func unionToolsByID(base, extra []MCPTool) []MCPTool {
	if len(extra) == 0 {
		return base
	}
	seen := make(map[uint64]struct{}, len(base))
	for i := range base {
		seen[base[i].ID] = struct{}{}
	}
	out := append([]MCPTool(nil), base...)
	for i := range extra {
		if _, dup := seen[extra[i].ID]; dup {
			continue
		}
		seen[extra[i].ID] = struct{}{}
		out = append(out, extra[i])
	}
	return out
}

// deriveCallName 派生 LLM 调用名：s{serverID}__{toolName}——server 前缀保证跨
// server 唯一（server 内 uq(name) 兜底原始名唯一）；toolName 逐字符过滤为
// [a-zA-Z0-9_-]（其余替换 _），整体截断 ≤64。运行期计算不落库（db_model §2.1）。
func deriveCallName(serverID uint64, toolName string) string {
	var sb strings.Builder
	sb.Grow(len(toolName) + 12)
	fmt.Fprintf(&sb, "s%d__", serverID)
	for _, r := range toolName {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			sb.WriteRune(r)
		default:
			sb.WriteByte('_')
		}
	}
	name := sb.String()
	if len(name) > callNameMaxLen { // sanitize 后全 ASCII，字节截断即字符截断
		name = name[:callNameMaxLen]
	}
	return name
}

// truncateRunes rune 边界安全截断。
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
