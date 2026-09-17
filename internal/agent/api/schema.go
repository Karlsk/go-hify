package api

import (
	"fmt"

	"github.com/Karlsk/go-hify/internal/platform/schema"
)

// 本文件是 agent 模块的请求 / 响应契约：binding tag 管字段格式，Validate() 管跨字段规则。
// ID 约定：请求侧 FK 用数字（前端把字符串 id 转 Number 后提交），响应侧一律字符串
// （JS 2^53 精度保护，接口规范《字段命名与类型》）。

// ---- 响应 Schema ----

// AgentSchema Agent 响应（创建 / 更新）。不含绑定工具——tool_ids 需要逐行
// 查询（N+1），绑定明细走 Get 详情（AgentDetailSchema）。
type AgentSchema struct {
	schema.BaseSchema         // id 字符串化 + created_at / updated_at
	Name              string  `json:"name"`
	Description       string  `json:"description"`
	ModelID           string  `json:"model_id"`          // 主模型（字符串化外键）
	FallbackModelID   *string `json:"fallback_model_id"` // null=未设置（一期配置位不启用）
	SystemPrompt      string  `json:"system_prompt"`
	Temperature       float64 `json:"temperature"`
	MaxOutputTokens   *int64  `json:"max_output_tokens"` // null=跟随模型默认
	MaxContextTurns   int     `json:"max_context_turns"` // 多轮对话携带的最大历史轮数
	Enabled           bool    `json:"enabled"`           // false=停用（保留配置，新会话被拒）
	// RAGTopK / RAGMinSimilarity RAG 检索注入参数（chat buildSystemPrompt 读此值）。
	RAGTopK          int     `json:"rag_top_k"`
	RAGMinSimilarity float64 `json:"rag_min_similarity"`
	// WorkflowID 绑定的工作流（null=未绑定，字符串化外键）。
	WorkflowID *string `json:"workflow_id"`
}

// AgentListItem 列表项：AgentSchema + 当页批量现读的聚合列（模型展示名 / 绑定工具数，
// service List 聚合，防 N+1——provider 模块踩坑 #3 先例）。
// ModelName 为空串表示悬空引用（模型已被删），前端 fallback 显示 model_id。
type AgentListItem struct {
	AgentSchema
	ModelName string `json:"model_name"` // 关联模型展示名（悬空引用为 ""）
	ToolCount int64  `json:"tool_count"` // 绑定 MCP 工具数
	KBCount   int64  `json:"kb_count"`   // 绑定知识库数（chat 检索注入的召回范围）
}

// AgentDetailSchema Agent 详情：AgentSchema + 绑定工具 / 知识库 id 列表。
// 嵌入字段 JSON 展平（detail = 全部 agent 字段 + tool_ids + knowledge_base_ids）。
// ToolIDs / KnowledgeBaseIDs 由 service 保证非 nil（空绑定返 []，不返 null，
// 接口规范《空值约定》）。
type AgentDetailSchema struct {
	AgentSchema
	ToolIDs          []string `json:"tool_ids"`           // 绑定的 mcp_tools.id（字符串化）
	KnowledgeBaseIDs []string `json:"knowledge_base_ids"` // 绑定的 knowledge_bases.id（字符串化）
}

// AgentListResult Agent 偏移分页结果。agents 是极小配置表，按接口规范走偏移分页
// （keyset 强制规则的例外表）；Items 由 service 保证非 nil。
type AgentListResult struct {
	Items    []AgentListItem `json:"items"`
	Page     int             `json:"page"`
	PageSize int             `json:"page_size"`
	Total    int64           `json:"total"`
}

// ---- 请求 Req ----

// TemperatureBounds 与 maxToolBindings 是应用层校验常量（与 DB CHECK 对齐 / 防提示词膨胀），
// 钉住测试防漂移。
const (
	// TemperatureMin / TemperatureMax 与 migrations 00004 的
	// CHECK (temperature BETWEEN 0 AND 2) 对齐。
	TemperatureMin = 0.0
	TemperatureMax = 2.0
	// DefaultTemperature 创建/更新未传 temperature 时的缺省（与 DB DEFAULT 0.7 对齐）。
	DefaultTemperature = 0.7
	// MaxContextTurnsMin / MaxContextTurnsMax 与 migrations 00009 的
	// CHECK 一致性约定对齐（1-100 轮）；DefaultMaxContextTurns 未传时的缺省
	// （与 DB DEFAULT 10 对齐）。
	MaxContextTurnsMin     = 1
	MaxContextTurnsMax     = 100
	DefaultMaxContextTurns = 10
	// MaxToolBindings 单 Agent 绑定工具数上限（uq 防重复，上限防提示词无界膨胀）。
	MaxToolBindings = 100
	// MaxKBBindings 单 Agent 绑定知识库数上限 = ragapi.MaxRetrieveKBs：检索一次最多
	// 10 个 KB，绑超则 chat 注入的 Retrieve 必撞 kb_ids 上限、运行期才爆——绑定期挡
	//（binding tag max=10 与本常量对齐；数值取等）。
	MaxKBBindings = 10
	// DefaultRAGTopK / DefaultRAGMinSimilarity RAG 检索注入默认参数（与 DB DEFAULT 对齐）。
	// chat buildSystemPrompt 读 Agent 配置值，未传时取此默认。
	DefaultRAGTopK          = 3
	DefaultRAGMinSimilarity = 0.75
)

// CreateAgentReq 创建 Agent 请求：主资源 body 内嵌 tool_ids（db_model.md §4.3），
// 事务内一并落库。Temperature / Enabled / MaxContextTurns 用指针区分「未传（取缺省）」
// 与「显式零值」（temperature=0 严谨模式 / enabled=false 停用）。
type CreateAgentReq struct {
	Name            string   `json:"name" binding:"required,min=1,max=128"`
	Description     string   `json:"description" binding:"omitempty,max=512"`
	ModelID         uint64   `json:"model_id" binding:"required"`
	FallbackModelID *uint64  `json:"fallback_model_id" binding:"omitempty,gt=0"`
	SystemPrompt    string   `json:"system_prompt" binding:"omitempty,max=32000"`
	Temperature     *float64 `json:"temperature" binding:"omitempty,gte=0,lte=2"`
	MaxOutputTokens *int64   `json:"max_output_tokens" binding:"omitempty,min=1"`
	MaxContextTurns *int     `json:"max_context_turns" binding:"omitempty,min=1,max=100"`
	Enabled         *bool    `json:"enabled"` // 未传 = true（创建即启用）
	ToolIDs         []uint64 `json:"tool_ids" binding:"omitempty,max=100,dive,gt=0"`
	// KnowledgeBaseIDs 绑定的知识库（RAG 召回范围，多对多）；kb 不存在时 FK 23503
	// 由 service 翻译 ErrKnowledgeBaseNotFound（agent 不依赖 rag，FK 是唯一校验）。
	KnowledgeBaseIDs []uint64 `json:"knowledge_base_ids" binding:"omitempty,max=10,dive,gt=0"`
	// RAGTopK / RAGMinSimilarity RAG 检索注入参数（可选，nil=取默认 3/0.75）。
	// chat buildSystemPrompt 读此值替代钉死常量。
	RAGTopK          *int     `json:"rag_top_k" binding:"omitempty,min=1,max=20"`
	RAGMinSimilarity *float64 `json:"rag_min_similarity" binding:"omitempty,gte=0,lte=1"`
	// WorkflowID 绑定的工作流（可空；nil = 不绑定 / PUT 全量语义下 = 解绑）。
	// workflow 不存在时 FK 23503 由 service 翻译 ErrWorkflowNotFound（agent 不得
	// 依赖 workflow（依赖清单），FK 是存在性的唯一校验——与 KnowledgeBaseIDs 同款）。
	WorkflowID *uint64 `json:"workflow_id" binding:"omitempty,gt=0"`
}

// Validate 跨字段校验（字段格式由 binding tag 管）。
func (r CreateAgentReq) Validate() error {
	return validateAgent(r.ModelID, r.FallbackModelID, r.ToolIDs, r.KnowledgeBaseIDs)
}

// UpdateAgentReq 整体更新请求（PUT 语义：全量提交，缺省字段按零值覆盖）。
// ID 带 json:"-"：handler 先用 GetAgentReq 绑路径 id 再赋值，防 body 的 {"id":999}
// 大小写不敏感匹配悄悄改写路径值（provider 模块踩坑 #7）。
type UpdateAgentReq struct {
	ID              uint64   `json:"-"`
	Name            string   `json:"name" binding:"required,min=1,max=128"`
	Description     string   `json:"description" binding:"omitempty,max=512"`
	ModelID         uint64   `json:"model_id" binding:"required"`
	FallbackModelID *uint64  `json:"fallback_model_id" binding:"omitempty,gt=0"`
	SystemPrompt    string   `json:"system_prompt" binding:"omitempty,max=32000"`
	Temperature     *float64 `json:"temperature" binding:"omitempty,gte=0,lte=2"`
	MaxOutputTokens *int64   `json:"max_output_tokens" binding:"omitempty,min=1"`
	MaxContextTurns *int     `json:"max_context_turns" binding:"omitempty,min=1,max=100"`
	Enabled         *bool    `json:"enabled"` // 未传 = true（PUT 全量；前端漏发会被置回启用）
	ToolIDs         []uint64 `json:"tool_ids" binding:"omitempty,max=100,dive,gt=0"`
	// KnowledgeBaseIDs 同 CreateAgentReq（PUT 全量覆盖：绑定事务内先删后插）。
	KnowledgeBaseIDs []uint64 `json:"knowledge_base_ids" binding:"omitempty,max=10,dive,gt=0"`
	// RAGTopK / RAGMinSimilarity 同 CreateAgentReq。
	RAGTopK          *int     `json:"rag_top_k" binding:"omitempty,min=1,max=20"`
	RAGMinSimilarity *float64 `json:"rag_min_similarity" binding:"omitempty,gte=0,lte=1"`
	// WorkflowID 同 CreateAgentReq（PUT 全量语义：缺省 / null = 解绑）。
	WorkflowID *uint64 `json:"workflow_id" binding:"omitempty,gt=0"`
}

// Validate 跨字段校验；ID>0 由本方法兜底（防绕过 handler 的调用方）。
func (r UpdateAgentReq) Validate() error {
	if r.ID == 0 {
		return fmt.Errorf("id 必填")
	}
	return validateAgent(r.ModelID, r.FallbackModelID, r.ToolIDs, r.KnowledgeBaseIDs)
}

// GetAgentReq / DeleteAgentReq 单条取 / 删请求（路径参数 id）。
type GetAgentReq struct {
	ID uint64 `uri:"id" binding:"required"`
}

// Validate 跨字段校验；当前无跨字段规则。
func (r GetAgentReq) Validate() error { return nil }

// DeleteAgentReq 删除请求（真删）：绑定行由 FK CASCADE 清理；有历史会话 → 409
// ErrAgentInUse 挡删，可先删会话或改停用（决策 #9 修订）。
type DeleteAgentReq struct {
	ID uint64 `uri:"id" binding:"required"`
}

// Validate 跨字段校验；当前无跨字段规则。
func (r DeleteAgentReq) Validate() error { return nil }

// ListAgentsReq 偏移分页列表请求（配置表，无筛选——需要时再加，不预留）。
type ListAgentsReq struct {
	Page     int `form:"page" binding:"omitempty,min=1"`
	PageSize int `form:"page_size" binding:"omitempty,min=1,max=100"`
}

// Validate 跨字段校验；当前无跨字段规则。
func (r ListAgentsReq) Validate() error { return nil }

// validateAgent 创建/更新共用的跨字段规则：备用模型不得等于主模型；tool_ids /
// knowledge_base_ids 不得重复（重复会撞 uq / 复合 PK，提前 400 比落库报错友好）。
func validateAgent(modelID uint64, fallback *uint64, toolIDs, kbIDs []uint64) error {
	if fallback != nil && *fallback == modelID {
		return fmt.Errorf("fallback_model_id 不得等于 model_id（备用须是另一个模型）")
	}
	seen := make(map[uint64]struct{}, len(toolIDs))
	for _, id := range toolIDs {
		if _, dup := seen[id]; dup {
			return fmt.Errorf("tool_ids 含重复 id %d", id)
		}
		seen[id] = struct{}{}
	}
	seenKB := make(map[uint64]struct{}, len(kbIDs))
	for _, id := range kbIDs {
		if _, dup := seenKB[id]; dup {
			return fmt.Errorf("knowledge_base_ids 含重复 id %d", id)
		}
		seenKB[id] = struct{}{}
	}
	return nil
}
