package api

import (
	"encoding/json"
	"fmt"

	"github.com/Karlsk/go-hify/internal/platform/schema"
)

// 本文件是 mcp 模块的请求 / 响应契约：binding tag 管字段格式，Validate() 管跨字段规则。
// ID 约定同 agent：请求侧 FK / 路径 id 用数字，响应侧一律字符串（JS 2^53 精度保护）。

// ---- 常量（应用层校验，钉住测试防漂移） ----

const (
	// Transport 三种连接方式，与 migrations 00003 的
	// CHECK (transport IN ('stdio','http','sse')) 对齐。
	TransportStdio = "stdio"
	TransportHTTP  = "http"
	TransportSSE   = "sse"

	// MaxNameLen / MaxCommandLen / MaxURLLen 长度上限（text 列不限长，应用层约束；
	// 与本文件 binding tag 的 max= 取值一致）。
	MaxNameLen    = 128
	MaxCommandLen = 512
	MaxURLLen     = 2048
)

// ---- 响应 Schema ----

// MCPServerSchema MCP Server 响应（创建 / 更新 / 详情内嵌）。env 刻意不在本层：
// 仅详情回传（编辑动线需要）、列表摘要不回（server_mgmt_spec.md §2）——
// 序列化钉住测试见 schema_test.go。
type MCPServerSchema struct {
	schema.BaseSchema        // id 字符串化 + created_at / updated_at
	Name              string `json:"name"`
	Transport         string `json:"transport"`
	Command           string `json:"command"`
	URL               string `json:"url"`
	Enabled           bool   `json:"enabled"` // false=停用（拒绝新绑定与调用）
}

// MCPServerListItem 列表项：+ tool_count（service List 按当页 id 批量计数，防 N+1
// ——provider 踩坑 #3 先例）。
type MCPServerListItem struct {
	MCPServerSchema
	ToolCount int64 `json:"tool_count"` // discover 入库的工具数
}

// MCPServerDetailSchema 详情：+ env + 工具清单（DB 读，不连 server）。
// Env / Tools 由 service 保证非 nil（空返 {} / []，接口规范《空值约定》）。
type MCPServerDetailSchema struct {
	MCPServerSchema
	Env   map[string]string `json:"env"`   // stdio 子进程环境变量；非 stdio 恒 {}
	Tools []ToolSchema      `json:"tools"` // discover 入库的工具（name 升序）
}

// MCPServerListResult 偏移分页结果。mcp_servers 是极小配置表，按接口规范走偏移分页
// （keyset 强制规则的例外表）；Items 由 service 保证非 nil。
type MCPServerListResult struct {
	Items    []MCPServerListItem `json:"items"`
	Page     int                 `json:"page"`
	PageSize int                 `json:"page_size"`
	Total    int64               `json:"total"`
}

// ToolSchema 工具契约（详情内嵌 + 跨模块共用）。不带时间戳：消费方（前端表单 /
// chat 转换）不关心。input_schema 原样透传不加工——转 eino ToolInfo 是 chat 侧
// （下批）的事，mcp 契约零 eino 类型（SDK 选型决策）。
type ToolSchema struct {
	ID          string          `json:"id"`        // 字符串化 id
	MCPServerID string          `json:"server_id"` // 字符串化所属 server
	Name        string          `json:"name"`      // 原始工具名（server 内唯一，uq 兜底）
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"` // 参数 JSON Schema 原样；service 保证非 nil（空为 {}）
}

// DiscoverResultSchema discover 结果（连通测试 + 工具发现一步完成）：动作完成即 200，
// 成败在字段里（provider ConnectionTestSchema 同款——测试是动作不是资源）。
// ToolCount = 本次 upsert 后该 server 的工具总数（失败为 0）。
type DiscoverResultSchema struct {
	Success      bool   `json:"success"`
	LatencyMs    int32  `json:"latency_ms"`
	ToolCount    int64  `json:"tool_count"`
	ErrorMessage string `json:"error_message,omitempty"` // 失败时非空（连接/握手/列工具错误摘要，截断）
}

// CallToolResult 工具调用结果（run 端点响应与跨模块 CallTool 共用）：
// Content = MCP 返回的 TextContent 多条换行拼接。
type CallToolResult struct {
	Content string `json:"content"`
}

// ToolCheck 工具 + 所属 server 的启用标志：agent 绑定校验判定
// 「工具存在且 server 启用」的唯一依据（server_mgmt_spec.md §5）。
type ToolCheck struct {
	ToolSchema
	ServerEnabled bool `json:"server_enabled"`
}

// ---- 请求 Req ----

// CreateMCPServerReq 创建请求。Enabled 指针：未传 = true（创建即启用）；
// Env 仅 stdio 有意义（其他 transport 存而不用，不额外拒）。
type CreateMCPServerReq struct {
	Name      string            `json:"name" binding:"required,min=1,max=128"`
	Transport string            `json:"transport" binding:"required,oneof=stdio http sse"`
	Command   string            `json:"command" binding:"omitempty,max=512"`
	URL       string            `json:"url" binding:"omitempty,max=2048"`
	Env       map[string]string `json:"env"`
	Enabled   *bool             `json:"enabled"`
}

// Validate 跨字段校验：按 transport 分支的必填规则（binding tag 管不了跨字段）。
func (r CreateMCPServerReq) Validate() error {
	return validateMCPServer(r.Transport, r.Command, r.URL)
}

// UpdateMCPServerReq 整体更新请求（PUT 语义：全量提交，缺省字段按零值覆盖）。
// ID 带 json:"-"：handler 先用 GetMCPServerReq 绑路径 id 再赋值，防 body 的
// {"id":999} 大小写不敏感匹配改写路径值（provider 踩坑 #7）。
type UpdateMCPServerReq struct {
	ID        uint64            `json:"-"`
	Name      string            `json:"name" binding:"required,min=1,max=128"`
	Transport string            `json:"transport" binding:"required,oneof=stdio http sse"`
	Command   string            `json:"command" binding:"omitempty,max=512"`
	URL       string            `json:"url" binding:"omitempty,max=2048"`
	Env       map[string]string `json:"env"`
	Enabled   *bool             `json:"enabled"` // PUT 全量：未传 = true
}

// Validate 跨字段校验；ID>0 由本方法兜底（防绕过 handler 的调用方）。
func (r UpdateMCPServerReq) Validate() error {
	if r.ID == 0 {
		return fmt.Errorf("id 必填")
	}
	return validateMCPServer(r.Transport, r.Command, r.URL)
}

// GetMCPServerReq / DeleteMCPServerReq / DiscoverReq 单条取 / 删 / 连通发现请求
// （路径参数 id，无 body）。
type GetMCPServerReq struct {
	ID uint64 `uri:"id" binding:"required"`
}

// Validate 跨字段校验；当前无跨字段规则。
func (r GetMCPServerReq) Validate() error { return nil }

// DeleteMCPServerReq 删除请求（真删）：mcp_tools 级联删；有 Agent 绑定 →
// 409 ErrMCPServerInUse 挡删（迁移 00021 RESTRICT），可解绑后删或改停用。
type DeleteMCPServerReq struct {
	ID uint64 `uri:"id" binding:"required"`
}

// Validate 跨字段校验；当前无跨字段规则。
func (r DeleteMCPServerReq) Validate() error { return nil }

// DiscoverReq 连通测试 + 工具发现请求（POST /mcp-servers/{id}/discover）：
// 连接 server → initialize → tools/list → upsert mcp_tools，一个动作完成
// （连通与发现合一，路由名沿用 CLAUDE.md 资源清单）。
type DiscoverReq struct {
	ID uint64 `uri:"id" binding:"required"`
}

// Validate 跨字段校验；当前无跨字段规则。
func (r DiscoverReq) Validate() error { return nil }

// RunToolReq 实调工具请求（POST /mcp-servers/{id}/run）。两段绑定：handler 先
// BindUri 取路径 id 再赋入 ID（json:"-" 防 body 改写）；body 只带工具名与参数。
type RunToolReq struct {
	ID        uint64          `json:"-"`
	ToolName  string          `json:"tool_name" binding:"required,min=1,max=128"`
	Arguments json.RawMessage `json:"arguments"` // 可选；缺省 / null / {} = 无参
}

// Validate：ID 兜底 + Arguments 须为 JSON 对象（MCP tools/call 的参数形态；
// 提前 400 比调用上游后才报错友好）。
func (r RunToolReq) Validate() error {
	if r.ID == 0 {
		return fmt.Errorf("id 必填")
	}
	if len(r.Arguments) > 0 && string(r.Arguments) != "null" {
		var v any
		if err := json.Unmarshal(r.Arguments, &v); err != nil {
			return fmt.Errorf("arguments 须为合法 JSON: %w", err)
		}
		if _, ok := v.(map[string]any); !ok {
			return fmt.Errorf("arguments 须为 JSON 对象")
		}
	}
	return nil
}

// ListMCPServersReq 偏移分页列表请求（配置表，无筛选——需要时再加，不预留）。
type ListMCPServersReq struct {
	Page     int `form:"page" binding:"omitempty,min=1"`
	PageSize int `form:"page_size" binding:"omitempty,min=1,max=100"`
}

// Validate 跨字段校验；当前无跨字段规则。
func (r ListMCPServersReq) Validate() error { return nil }

// validateMCPServer 创建/更新共用的跨字段规则：按 transport 分支必填——
// http/sse 必填 url，stdio 必填 command（存了不用的字段不额外拒，编辑动线宽容）。
func validateMCPServer(transport, command, url string) error {
	switch transport {
	case TransportStdio:
		if command == "" {
			return fmt.Errorf("stdio 模式 command 必填")
		}
	case TransportHTTP, TransportSSE:
		if url == "" {
			return fmt.Errorf("%s 模式 url 必填", transport)
		}
	default:
		// binding tag oneof 已挡 HTTP 层非法值；default 分支覆盖直调 service 的调用方。
		return fmt.Errorf("transport 须为 stdio / http / sse")
	}
	return nil
}

// ---- 跨模块 Req（MCPClientService；不含 binding tag——不经 HTTP 绑定） ----

// CallToolReq 跨模块工具调用（chat 工具循环 / run 端点内部复用）。
type CallToolReq struct {
	MCPServerID uint64          // 目标 server
	ToolName    string          // 原始工具名（server 内唯一）
	Arguments   json.RawMessage // 调用参数；缺省 / null / {} = 无参
}

// ListToolsReq 跨模块读工具清单（DB 读，不连 server）。
type ListToolsReq struct {
	MCPServerID uint64
}

// GetToolsByIDsReq 批量取工具 + server 启用标志（agent 绑定校验）。
type GetToolsByIDsReq struct {
	IDs []uint64
}

// ToolSourceReq 统一工具来源构造请求（chat 工具循环消费，chat_tool_loop_spec.md §4）。
type ToolSourceReq struct {
	ToolIDs []uint64 // agent 绑定的 mcp_tools.id（存在性已由 agent 模块校验）
}
