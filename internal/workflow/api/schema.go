// 本文件是 workflow 模块的契约：状态 / 节点类型常量、密封 NodeConfig 与 ParseNodeConfig、
// 请求 / 响应 Schema 与图校验 Validate。binding tag 管字段格式与数量界，Validate() 管
// 跨字段图规则（db_model §7 的纯函数子集 R1-R8）；config 的类型安全解析唯一入口是
// ParseNodeConfig（保存与执行器加载共用，db_model §8）。
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"
)

// ── 状态与节点类型常量（与 DB CHECK 一一对应，加值 = 迁移 + 此处同步）──

// WorkflowStatus 工作流状态（db_model 决策 #5：Dify 式发布心智三态）。
type WorkflowStatus string

const (
	StatusDraft     WorkflowStatus = "draft"
	StatusPublished WorkflowStatus = "published"
	StatusDisabled  WorkflowStatus = "disabled"
)

// WorkflowType 工作流分型（spec 08 §4.1，2026-09-18 拍板）：chat = 对话管道终答 /
// task = string→string 可组合任务函数（可被 sub-workflow 节点引用）。类型不可变
// （Update 携带即拒，换型 = 删了重建）；存量回填 chat。
type WorkflowType string

const (
	WorkflowTypeChat WorkflowType = "chat"
	WorkflowTypeTask WorkflowType = "task"
)

// NodeType 节点类型（db_model §4：七类，config 格式按 type 判别；00016 建四类，
// 00017 加宽补 api/end，00020 补 workflow——sub-workflow 嵌套节点）。
type NodeType string

const (
	NodeLLM                NodeType = "llm"
	NodeTool               NodeType = "tool"
	NodeCondition          NodeType = "condition"
	NodeKnowledgeRetrieval NodeType = "knowledge_retrieval"
	NodeAPI                NodeType = "api"
	NodeEnd                NodeType = "end"
	NodeWorkflow           NodeType = "workflow"
)

// ── NodeConfig 密封接口：实现集封闭本包，引擎 type switch 穷举（db_model §8）──

// NodeConfig 节点 config 的密封接口：七类 config 各自实现 isNodeConfig（非导出方法，
// 包外无法新增实现），执行引擎按具体类型 type switch 消费，无断言无反射。
type NodeConfig interface{ isNodeConfig() }

// LLMConfig 单轮 LLM 调用（无多轮上下文；执行走 platform/llm，不经 chat）。
type LLMConfig struct {
	ModelID      uint64  `json:"model_id,string"`         // models.id；存在性 service 经 provider api 预检
	SystemPrompt string  `json:"system_prompt,omitempty"` // 可选 system 消息模板（spec 011 加法修订）：空串=不发；{{var}} 渲染语义同 prompt
	Prompt       string  `json:"prompt"`                  // 支持 {{var}} 模板
	Temperature  float64 `json:"temperature,omitempty"`   // 0 = 跟随模型默认
}

func (LLMConfig) isNodeConfig() {}

// Validate 必填校验：binding 管不到 jsonb 内部，config 形状由这里兜底。
func (c LLMConfig) Validate() error {
	if c.ModelID == 0 {
		return fmt.Errorf("model_id 必填")
	}
	if c.Prompt == "" {
		return fmt.Errorf("prompt 必填")
	}
	return nil
}

// ToolConfig MCP 工具调用；ToolID 存在性推迟执行器 fail-fast（db_model 决策 #9 修订，
// 2026-09-15 拍板）——但「是否填了」是形状校验，仍在保存期挡（2026-09-16 拍板）。
type ToolConfig struct {
	ToolID uint64            `json:"tool_id,string"` // mcp_tools.id
	Args   map[string]string `json:"args,omitempty"` // 值支持 {{var}} 模板
}

func (ToolConfig) isNodeConfig() {}

// Validate 必填校验（tool_id 非零；args 可空）。
func (c ToolConfig) Validate() error {
	if c.ToolID == 0 {
		return fmt.Errorf("tool_id 必填")
	}
	return nil
}

// ConditionConfig 表达式求值，结果字符串供出边匹配（纯内存求值，零外部调用）。
type ConditionConfig struct {
	Expression string `json:"expression"` // 如 {{classify}} == 'ORDER_QUERY'
}

func (ConditionConfig) isNodeConfig() {}

// Validate 必填校验（expression 非空）。
func (c ConditionConfig) Validate() error {
	if c.Expression == "" {
		return fmt.Errorf("expression 必填")
	}
	return nil
}

// KnowledgeRetrievalConfig 知识库检索（结果注入上下文）；KnowledgeBaseID 存在性
// service 经 rag api 预检（db_model 决策 #9）。
type KnowledgeRetrievalConfig struct {
	KnowledgeBaseID uint64 `json:"knowledge_base_id,string"`
	TopK            int    `json:"top_k,omitempty"` // 0 = 跟随默认；1-20（与 agent RAG 参数同界）
}

func (KnowledgeRetrievalConfig) isNodeConfig() {}

// Validate 必填与界校验（top_k 为 0 或 1-20）。
func (c KnowledgeRetrievalConfig) Validate() error {
	if c.KnowledgeBaseID == 0 {
		return fmt.Errorf("knowledge_base_id 必填")
	}
	if c.TopK < 0 || c.TopK > 20 {
		return fmt.Errorf("top_k 取值 1-20（0 = 跟随默认）")
	}
	return nil
}

// ApiCallConfig 直接 HTTP 调用节点（不经 MCP 注册的轻量出站请求；执行器分发
// callApi(api, context)，spec 05）。url / headers 值 / body 均支持 {{var}} 模板；
// SSRF 防护（内网地址拦截）与出站执行细节归执行器，本层只做形状校验
// （2026-09-16 拍板新增，完整版字段）。
type ApiCallConfig struct {
	URL        string            `json:"url"`                   // 必填，合法 http/https 地址
	Method     string            `json:"method"`                // 必填，GET/POST/PUT/DELETE/PATCH（大写）
	Headers    map[string]string `json:"headers,omitempty"`     // 值支持 {{var}} 模板
	Body       string            `json:"body,omitempty"`        // 请求体模板字符串
	TimeoutSec int               `json:"timeout_sec,omitempty"` // 0 = 默认 10s；1-60
	SSLVerify  bool              `json:"ssl_verify,omitempty"`  // 默认 false = 跳过证书校验（内网自签端点，2026-09-16 拍板）；执行器映射 transport TLSClientConfig
}

func (ApiCallConfig) isNodeConfig() {}

// Validate 形状校验：url 合法 http(s)、method 白名单、timeout_sec 界。
func (c ApiCallConfig) Validate() error {
	if c.URL == "" {
		return fmt.Errorf("url 必填")
	}
	u, err := url.Parse(c.URL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("url 必须是合法的 http/https 地址")
	}
	switch c.Method {
	case "GET", "POST", "PUT", "DELETE", "PATCH":
	default:
		return fmt.Errorf("method 非法: %q（限 GET/POST/PUT/DELETE/PATCH）", c.Method)
	}
	if c.TimeoutSec < 0 || c.TimeoutSec > 60 {
		return fmt.Errorf("timeout_sec 取值 1-60（0 = 默认 10s）")
	}
	return nil
}

// EndConfig 显式终止节点（可选）：buildOutput(end, context) 按 output 模板拼工作流
// 终稿（执行器 spec 05）；空 output = 取最后执行节点的输出，与「无出边 = 隐式结束」
// 现行为一致。end 节点不得有出边（R9）；不强制每图必有 end——既有图不受影响
// （2026-09-16 拍板新增）。
type EndConfig struct {
	Output string `json:"output,omitempty"` // {{var}} 模板，如 "{{reply}}"
}

func (EndConfig) isNodeConfig() {}

// Validate 跨字段校验；空配置即合法（output 可选）。
func (c EndConfig) Validate() error { return nil }

// WorkflowNodeConfig sub-workflow 嵌套节点（spec 08 §4.1 / §4.3）：引用一个 task 型
// 工作流作为可组合任务函数。inputs 是「子 schema 字段 → 父图 {{var}} 模板」映射；
// 键集语义（required 全覆盖、多余拒、无 schema 回退恰 {input}）与 workflow_id 的
// 存在性 / task 型 / 环 / 链深校验全归 service R11——本层只管形状（workflow_id 非零）。
type WorkflowNodeConfig struct {
	WorkflowID uint64            `json:"workflow_id,string"` // workflows.id；存在性与分型由 service R11 校验
	Inputs     map[string]string `json:"inputs,omitempty"`   // 值支持 {{var}} 模板（R10 天然覆盖）
}

func (WorkflowNodeConfig) isNodeConfig() {}

// Validate 必填校验（workflow_id 非零；inputs 键集语义归 R11，可空）。
func (c WorkflowNodeConfig) Validate() error {
	if c.WorkflowID == 0 {
		return fmt.Errorf("workflow_id 必填")
	}
	return nil
}

// errInvalidNodeConfig config 强校验失败的包内哨兵：对外错误统一由 handler 翻成
// errs.ErrValidationFailed（400），本哨兵只做包内错误链判别（db_model §8）。
var errInvalidNodeConfig = errors.New("INVALID_NODE_CONFIG")

// validatableConfig 密封接口 + Validate 的私有复合约束：密封面（NodeConfig）冻结为
// 只有 isNodeConfig，本接口仅供 ParseNodeConfig 内部断言调用 Validate 用。
type validatableConfig interface {
	NodeConfig
	Validate() error
}

// ParseNodeConfig 按 type 把 config 原始 JSON 解析为强类型（密封接口封闭实现集）。
// 保存路径（UpsertReq.Validate）与执行器加载路径共用的唯一强校验入口：未知类型、
// 坏 JSON、校验失败统一报错且文案带类型与原因（上层再拼节点 key 定位，db_model §8）。
func ParseNodeConfig(t NodeType, raw json.RawMessage) (NodeConfig, error) {
	var cfg NodeConfig
	switch t {
	case NodeLLM:
		cfg = &LLMConfig{}
	case NodeTool:
		cfg = &ToolConfig{}
	case NodeCondition:
		cfg = &ConditionConfig{}
	case NodeKnowledgeRetrieval:
		cfg = &KnowledgeRetrievalConfig{}
	case NodeAPI:
		cfg = &ApiCallConfig{}
	case NodeEnd:
		cfg = &EndConfig{}
	case NodeWorkflow:
		cfg = &WorkflowNodeConfig{}
	default:
		return nil, fmt.Errorf("%w: 未知节点类型 %q", errInvalidNodeConfig, t)
	}
	if err := json.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("%w: 节点类型 %s 配置非法: %v", errInvalidNodeConfig, t, err)
	}
	if err := cfg.(validatableConfig).Validate(); err != nil {
		return nil, fmt.Errorf("%w: 节点类型 %s: %v", errInvalidNodeConfig, t, err)
	}
	return cfg, nil
}

// ── 分型结构化契约（spec 08 §4.5：task 型简化 I/O schema）──

// SchemaField task 型结构化 I/O 契约的字段（spec 08 §4.5 简化形态）：
// [{name, type, required, description}]，type ∈ string / number / boolean；
// 仅 task 型消费（chat 型携带非空 schema 由 service 强不变量拒）。
type SchemaField struct {
	Name        string `json:"name"`
	Type        string `json:"type"` // string / number / boolean
	Required    bool   `json:"required"`
	Description string `json:"description"`
}

// ValidateSchemaFields 校验 schema 字段集形态（service 保存路径 Create / Update 调用，
// HTTP 与直调共用；字段级规则全归此处，不打 binding tag——与节点 config 同理，
// jsonb 形状由代码兜底）：name 非空不重名、type ∈ string/number/boolean。
func ValidateSchemaFields(fields []SchemaField) error {
	seen := make(map[string]bool, len(fields))
	for _, f := range fields {
		if f.Name == "" {
			return fmt.Errorf("schema 字段 name 必填")
		}
		if seen[f.Name] {
			return fmt.Errorf("schema 字段重名: %s", f.Name)
		}
		seen[f.Name] = true
		switch f.Type {
		case "string", "number", "boolean":
		default:
			return fmt.Errorf("schema 字段 %s type 非法: %q（限 string/number/boolean）", f.Name, f.Type)
		}
	}
	return nil
}

// ── 请求（api_contract §3 冻结；config 延迟解析）──

// UpsertReq 创建 / 整图替换共用请求（POST / PUT 同构）。请求体不含 status——状态
// 只能经 publish / disable 动作改变（编辑不降级，db_model 决策 #6）。Type 必填
// oneof 只在 Validate 管（spec 08）：Update 复用本结构但不携带 type（携带即拒归
// UpdateWorkflowReq），binding tag 会误伤嵌入空值，故不打。数量界（节点 1-50 /
// 边 0-100）由 binding tag 管，Validate 只管跨字段图规则，两层不重复。
type UpsertReq struct {
	Name         string        `json:"name" binding:"required,max=128"`
	Description  string        `json:"description"`
	Type         WorkflowType  `json:"type"`          // chat / task（spec 08）；Create 必填，Update 不携带
	InputSchema  []SchemaField `json:"input_schema"`  // 仅 task 型；chat 型携带非空由 service 强不变量拒
	OutputSchema []SchemaField `json:"output_schema"` // 仅 task 型
	StartNodeKey string        `json:"start_node_key" binding:"required"`
	Nodes        []NodeReq     `json:"nodes" binding:"required,min=1,max=50"`
	Edges        []EdgeReq     `json:"edges" binding:"required,max=100"` // 纯线性可传 []
}

// NodeReq 节点：config 保持 RawMessage 延迟解析——绑定阶段还不知道类型，
// Validate（R2）时才按 type 经 ParseNodeConfig 分发（db_model §8）。
type NodeReq struct {
	Key    string          `json:"key" binding:"required,max=64"`
	Type   NodeType        `json:"type" binding:"required"`
	Name   string          `json:"name" binding:"omitempty,max=128"`
	Config json.RawMessage `json:"config" binding:"required"`
}

// EdgeReq 连线；Condition 指针区分「没传」（nil = 无条件直走）与空串。
type EdgeReq struct {
	SourceNodeKey string  `json:"source_node_key" binding:"required,max=64"`
	TargetNodeKey string  `json:"target_node_key" binding:"required,max=64"`
	Condition     *string `json:"condition" binding:"omitempty,max=128"` // nil = 无条件
}

// Validate 整图校验入口：分型 oneof（spec 08，Create 必填）+ 图规则委托
// validateGraph。Update 路径不经此处（UpdateWorkflowReq.Validate 只走图规则，
// type 不可变、携带即拒）。
func (r UpsertReq) Validate() error {
	switch r.Type {
	case WorkflowTypeChat, WorkflowTypeTask:
	default:
		return fmt.Errorf("type 必填且限 chat/task")
	}
	return r.validateGraph()
}

// validateGraph 图规则（db_model §7 的纯函数子集，spec 02 §3.2）：
// R1 key 唯一 → R2 config 强校验 → R3 start 存在 → R4 悬挂边 → R5 出边匹配值 →
// R6 非 condition 出边数 → R7 无环 → R8 无不可达 → R9 end 禁出边。数量界归
// binding tag，jsonb 引用存在性归 service（spec 04）——三层各管一段。
func (r UpsertReq) validateGraph() error {
	// R1 节点 key 请求内唯一（uq_workflow_nodes_wf_key 的前置早暴露）。
	nodes := make(map[string]NodeReq, len(r.Nodes))
	for _, n := range r.Nodes {
		if _, dup := nodes[n.Key]; dup {
			return fmt.Errorf("节点 key 重复: %s", n.Key)
		}
		nodes[n.Key] = n
	}
	// R2 逐节点 config 经 ParseNodeConfig 强校验，错误包装出节点 key 定位。
	for _, n := range r.Nodes {
		if _, err := ParseNodeConfig(n.Type, n.Config); err != nil {
			return fmt.Errorf("节点 %s: %w", n.Key, err)
		}
	}
	// R3 start_node_key 在节点集合内。
	if _, ok := nodes[r.StartNodeKey]; !ok {
		return fmt.Errorf("start_node_key %q 不在节点集合内", r.StartNodeKey)
	}
	// R4 每条边 source / target 指向本图存在的节点；顺带建邻接表。
	out := make(map[string][]EdgeReq, len(r.Nodes))
	for _, e := range r.Edges {
		if _, ok := nodes[e.SourceNodeKey]; !ok {
			return fmt.Errorf("边的 source 节点 %s 不存在", e.SourceNodeKey)
		}
		if _, ok := nodes[e.TargetNodeKey]; !ok {
			return fmt.Errorf("边的 target 节点 %s 不存在", e.TargetNodeKey)
		}
		out[e.SourceNodeKey] = append(out[e.SourceNodeKey], e)
	}
	// R5 出边匹配值规则 + R6 非 condition 节点出边 ≤ 1（一期无并行分支）
	// + R9 end 节点禁出边（显式终止，2026-09-16 拍板）。
	for key, edges := range out {
		if nodes[key].Type == NodeEnd {
			return fmt.Errorf("end 节点 %s 不得有出边", key)
		}
		if nodes[key].Type != NodeCondition {
			if len(edges) > 1 {
				return fmt.Errorf("非 condition 节点 %s 出边超过 1 条", key)
			}
			for _, e := range edges {
				if e.Condition != nil {
					return fmt.Errorf("非 condition 节点 %s 的出边不得带 condition", key)
				}
			}
			continue
		}
		for _, e := range edges {
			if e.Condition == nil || *e.Condition == "" {
				return fmt.Errorf("condition 节点 %s 的出边缺少 condition 匹配值", key)
			}
		}
	}
	// R7 无环 + R8 无不可达：三色 DFS——路径上重访 = 环；分支汇流（多条路径同汇点，
	// 如 api_contract §4 示例的 reply）合法，已完结子图直接跳过。图 ≤50 节点，递归无忧。
	visited := make(map[string]bool, len(r.Nodes))
	onPath := make(map[string]bool, len(r.Nodes))
	var walk func(key string) error
	walk = func(key string) error {
		if onPath[key] {
			return fmt.Errorf("存在环：节点 %s 被路径重访", key)
		}
		if visited[key] {
			return nil
		}
		onPath[key] = true
		visited[key] = true
		for _, e := range out[key] {
			if err := walk(e.TargetNodeKey); err != nil {
				return err
			}
		}
		onPath[key] = false
		return nil
	}
	if err := walk(r.StartNodeKey); err != nil {
		return err
	}
	// R8 从入口走不到的节点 = 脏配置，早暴露。
	for key := range nodes {
		if !visited[key] {
			return fmt.Errorf("节点 %s 从入口不可达", key)
		}
	}
	return nil
}

// UpdateWorkflowReq：ID 由 handler BindUri 后赋值（provider UpdateModelReq 同款，body 不含 id）。
// Type 遮蔽嵌入 UpsertReq.Type（encoding/json 浅字段优先）：body 的 type 落指针、
// 不渗入嵌入字段——nil = 未携带（合法），非 nil = 携带即拒。
type UpdateWorkflowReq struct {
	ID   uint64  `json:"-"`
	Type *string `json:"type"` // 携带即拒（spec 08 §4.1：分型不可变，同值 / 异值均拒）
	UpsertReq
}

// Validate：ID 兜底（防绕过 handler 的调用方）+ type 携带即拒 + 图规则委托
// validateGraph。不走 UpsertReq.Validate——type 不可变（spec 08）：Update body
// 不携带 type，必填检查会误伤；携带即拒（clarify 拍板：不比对当前值，同值也拒）。
func (r UpdateWorkflowReq) Validate() error {
	if r.ID == 0 {
		return fmt.Errorf("id 必填")
	}
	if r.Type != nil {
		return fmt.Errorf("type 不可变：Update 不得携带 type（当前携带 %q；换型 = 删了重建）", *r.Type)
	}
	return r.UpsertReq.validateGraph()
}

// GetWorkflowReq 详情请求（路径参数 id）。
type GetWorkflowReq struct {
	ID uint64 `uri:"id" binding:"required"`
}

// Validate 跨字段校验；当前无跨字段规则。
func (r GetWorkflowReq) Validate() error { return nil }

// DeleteWorkflowReq 硬删请求（nodes / edges 由 FK CASCADE 清理）。
type DeleteWorkflowReq struct {
	ID uint64 `uri:"id" binding:"required"`
}

// Validate 跨字段校验；当前无跨字段规则。
func (r DeleteWorkflowReq) Validate() error { return nil }

// PublishWorkflowReq 发布动作请求（draft/disabled → published）。
type PublishWorkflowReq struct {
	ID uint64 `uri:"id" binding:"required"`
}

// Validate 跨字段校验；当前无跨字段规则。
func (r PublishWorkflowReq) Validate() error { return nil }

// DisableWorkflowReq 停用动作请求（published → disabled）。
type DisableWorkflowReq struct {
	ID uint64 `uri:"id" binding:"required"`
}

// Validate 跨字段校验；当前无跨字段规则。
func (r DisableWorkflowReq) Validate() error { return nil }

// ListWorkflowsReq 偏移分页列表请求（极小静态配置表，接口规范 B 模式例外；
// 参数归一化由 service 做）。
type ListWorkflowsReq struct {
	Page     int `form:"page"`
	PageSize int `form:"page_size"`
}

// Validate 跨字段校验；当前无跨字段规则。
func (r ListWorkflowsReq) Validate() error { return nil }

// ExecuteWorkflowReq 执行请求（api_contract §5 冻结）：控制台与进程内调用方（chat）
// 共用。ID 由 handler BindUri 后赋值（UpdateWorkflowReq 同款）；Trial 由 handler 从
// query `?trial=true` 绑定（进程内调用方直传）；ConversationID / MessageID 是 chat
// 触发时的调用方引用（弱引用落 run 行，HTTP 调用不传）。
type ExecuteWorkflowReq struct {
	ID             uint64  // 路径参数（handler 绑定）
	Input          string  `json:"input" binding:"required,max=16384"` // 工作流入参 → vars["input"]（O1 拍板：单一 input，终形）
	ConversationID *uint64 // chat 触发时的调用方引用（弱引用落 run 行；HTTP 调用不传）
	MessageID      *uint64
	Trial          bool // ?trial=true 试运行（O3）：放开 draft/disabled，状态机唯一例外
}

// Validate ID 兜底（防绕过 handler 的调用方）；input 格式归 binding tag。
func (r ExecuteWorkflowReq) Validate() error {
	if r.ID == 0 {
		return fmt.Errorf("id 必填")
	}
	return nil
}

// ── 响应 Schema（api_contract §3 冻结）──

// WorkflowSummarySchema 摘要（列表用，不带图）。Type / schema 字段 spec 08 起
// 暴露：schema 未声明（含 chat 型恒空）序列化为 null。
type WorkflowSummarySchema struct {
	ID           string        `json:"id"` //〔2026-09-16 修订〕原 `json:"id,string"` 系笔误：,string 只用于数字字段，挂在 string 字段上会双重编码
	Name         string        `json:"name"`
	Description  string        `json:"description"`
	Type         string        `json:"type"`          // chat/task（spec 08；存量回填 chat）
	Status       string        `json:"status"`        // draft/published/disabled
	InputSchema  []SchemaField `json:"input_schema"`  // task 型入参契约；null = 未声明
	OutputSchema []SchemaField `json:"output_schema"` // task 型出参契约；null = 未声明
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
}

// WorkflowDetailSchema 详情（创建/更新/详情接口返回）；结构与创建入参一致
// （api_contract §4 round-trip）。Nodes / Edges 由 service 保证非 nil（空返 []）。
type WorkflowDetailSchema struct {
	WorkflowSummarySchema
	StartNodeKey string       `json:"start_node_key"`
	Nodes        []NodeSchema `json:"nodes"`
	Edges        []EdgeSchema `json:"edges"`
}

// NodeSchema config 原样透传：库里存的就是校验过的 JSON 原文，出参不重新序列化。
type NodeSchema struct {
	Key    string          `json:"key"`
	Type   string          `json:"type"`
	Name   string          `json:"name"`
	Config json.RawMessage `json:"config"`
}

// EdgeSchema 连线；Condition nil = null = 无条件直走。
type EdgeSchema struct {
	SourceNodeKey string  `json:"source_node_key"`
	TargetNodeKey string  `json:"target_node_key"`
	Condition     *string `json:"condition"`
}

// WorkflowListResult 偏移分页结果：handler 经 respond.OKWithOffset 拆封
// （Items → data，Page/PageSize/Total → meta），本结构不直接序列化。
type WorkflowListResult struct {
	Items    []WorkflowSummarySchema
	Page     int
	PageSize int
	Total    int64
}

// ── 执行结果（api_contract §5 冻结，spec 06）──

// RunResultSchema 一次执行的结果（非流式）：同步返回，呈现归调用方（控制台 respond
// 信封一次返回；chat 拿到返回值自行决定推送粒度）。
type RunResultSchema struct {
	RunID      string           `json:"run_id"` // workflow_runs.id（字符串化）；轨迹写入降级时置空（O7 ④：结果照返）
	Status     string           `json:"status"` // succeeded / failed
	Output     string           `json:"output"` // 终稿（end.output 渲染或末节点输出）
	DurationMs int              `json:"duration_ms"`
	NodeTrace  []NodeRunSummary `json:"node_trace"` // 节点轨迹摘要（key/type/status/耗时），明细查轨迹表
}

// NodeRunSummary 节点轨迹摘要（RunResultSchema.node_trace 元素；数组顺序即执行序，
// 明细查 workflow_node_runs）。ErrorMsg 成功 = ""（空值约定）。
type NodeRunSummary struct {
	NodeKey    string `json:"node_key"`
	NodeType   string `json:"node_type"`
	Status     string `json:"status"` // succeeded / failed
	DurationMs int    `json:"duration_ms"`
	ErrorMsg   string `json:"error_msg"`
}
