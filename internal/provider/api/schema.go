// Package api 的 Req/Schema 定义。
// 纯叶子包：不 import gin/gorm（binding tag 是纯字符串；platform 纯标准库小包 schema 除外）。
// 数据模型决策记录见 docs/changelog/provider/db_model.md。
package api

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/Karlsk/go-hify/internal/platform/schema"
)

// Kind 提供商类型枚举（text + CHECK，见 migrations/00002_provider.sql）。
const (
	KindOpenAI           = "openai"
	KindClaude           = "claude"
	KindGemini           = "gemini"
	KindOllama           = "ollama"
	KindOpenAICompatible = "openai_compatible"
)

// validKind kind 白名单，供 Validate 跨字段校验用（binding oneof 之外的服务层防线）。
var validKind = map[string]bool{
	KindOpenAI:           true,
	KindClaude:           true,
	KindGemini:           true,
	KindOllama:           true,
	KindOpenAICompatible: true,
}

// kindNeedsAPIKey 需要鉴权密钥的 kind（ollama 无鉴权、openai_compatible 可选）。
var kindNeedsAPIKey = map[string]bool{
	KindOpenAI: true,
	KindClaude: true,
	KindGemini: true,
}

// Capability 模型能力类型。
const (
	CapabilityChat      = "chat"
	CapabilityEmbedding = "embedding"
)

// Source 模型来源。
const (
	SourceDiscovered = "discovered"
	SourceManual     = "manual"
)

// Health 健康状态（provider_health.status；DEGRADED 状态机见 db_model.md §2.3）。
const (
	HealthUnknown  = "unknown"
	HealthUp       = "up"
	HealthDegraded = "degraded"
	HealthDown     = "down"
)

// thinkLevelValues extra_params.think_level 合法值（模型级思考档位）。
var thinkLevelValues = map[string]bool{
	"off": true, "low": true, "medium": true, "high": true,
}

// priceRe 价格字符串格式：1-8 位整数 + 可选 1-4 位小数（对齐 numeric(12,4)，值 < 1e8）。
var priceRe = regexp.MustCompile(`^\d{1,8}(\.\d{1,4})?$`)

// ---- 跨字段校验（创建 / 更新共用；字段级格式由 binding tag 管） ----

// validateProvider 提供商创建 / 更新共用的跨字段校验。
// kind 传空串时跳过 kind 相关规则（更新请求不含 kind——创建后不可改，取库内现值由 service 兜底）。
// requireKey：创建时为 true（按 kind 校验 api_key 必填）；更新时 false（空串 = 保持不变）。
func validateProvider(name, kind, baseURL, apiKey string, extra map[string]any, requireKey bool) error {
	if name == "" {
		return fmt.Errorf("name 不能为空")
	}
	if kind != "" {
		if !validKind[kind] {
			return fmt.Errorf("kind 必须是 %s / %s / %s / %s / %s 之一",
				KindOpenAI, KindClaude, KindGemini, KindOllama, KindOpenAICompatible)
		}
		if requireKey && kindNeedsAPIKey[kind] && apiKey == "" {
			return fmt.Errorf("kind %s 必须提供 api_key", kind)
		}
		if kind == KindOpenAICompatible && baseURL == "" {
			return fmt.Errorf("kind %s 必须提供 base_url", KindOpenAICompatible)
		}
	}
	if baseURL != "" && !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		return fmt.Errorf("base_url 必须以 http:// 或 https:// 开头")
	}
	return validateExtraConfig(kind, extra)
}

// validateExtraConfig provider 级白名单校验（JSON 数字解码为 float64）。
// kind 为空串时跳过 keep_alive 的 kind 限定（service 取实体后兜底）。
func validateExtraConfig(kind string, m map[string]any) error {
	for k, v := range m {
		switch k {
		case "bulkhead":
			if err := checkIntRange(v, 1, 128, "extra_config.bulkhead"); err != nil {
				return err
			}
		case "ttft_seconds":
			if err := checkIntRange(v, 1, 600, "extra_config.ttft_seconds"); err != nil {
				return err
			}
		case "keep_alive":
			if _, ok := v.(string); !ok {
				return fmt.Errorf("extra_config.keep_alive 必须是字符串")
			}
			if kind != "" && kind != KindOllama {
				return fmt.Errorf("extra_config.keep_alive 仅 %s 支持", KindOllama)
			}
		default:
			return fmt.Errorf("extra_config 不支持的键 %q（白名单：bulkhead / ttft_seconds / keep_alive）", k)
		}
	}
	return nil
}

// checkIntRange 校验 map[string]any 中的整数值落在 [min, max]。
func checkIntRange(v any, min, max int64, field string) error {
	f, ok := v.(float64)
	if !ok || f != math.Trunc(f) || f < float64(min) || f > float64(max) {
		return fmt.Errorf("%s 必须是 %d-%d 的整数", field, min, max)
	}
	return nil
}

// validateModel 模型创建 / 更新共用的跨字段校验。
func validateModel(capability string, dim *int32, in, out *string, extra map[string]any) error {
	if capability == CapabilityEmbedding {
		if dim == nil || *dim < 1 {
			return fmt.Errorf("capability %s 必须提供 embedding_dim", CapabilityEmbedding)
		}
	} else if capability == CapabilityChat && dim != nil {
		return fmt.Errorf("capability %s 不应提供 embedding_dim", CapabilityChat)
	}
	for _, p := range []struct {
		name string
		v    *string
	}{{"input_price", in}, {"output_price", out}} {
		if p.v != nil && !priceRe.MatchString(*p.v) {
			return fmt.Errorf("%s 格式非法（非负十进制字符串，最多 8 位整数 + 4 位小数）", p.name)
		}
	}
	return validateExtraParams(extra)
}

// validateExtraParams 模型级白名单校验。
func validateExtraParams(m map[string]any) error {
	for k, v := range m {
		switch k {
		case "think_level":
			s, ok := v.(string)
			if !ok || !thinkLevelValues[s] {
				return fmt.Errorf("extra_params.think_level 必须是 off / low / medium / high 之一")
			}
		default:
			return fmt.Errorf("extra_params 不支持的键 %q（白名单：think_level）", k)
		}
	}
	return nil
}

// ---- Provider Req ----

// CreateProviderReq 创建提供商请求。APIKey 为明文入参，service 加密后入库（schema/model 永不携带密文）。
// Enabled 不在创建请求中：新提供商固定 enabled=true，停用走更新。
type CreateProviderReq struct {
	Name        string         `json:"name" binding:"required,min=1,max=128"`
	Kind        string         `json:"kind" binding:"required,oneof=openai claude gemini ollama openai_compatible"`
	BaseURL     string         `json:"base_url" binding:"omitempty,max=512"`
	APIKey      string         `json:"api_key" binding:"omitempty,max=512"`
	ExtraConfig map[string]any `json:"extra_config"`
}

// Validate 跨字段校验（字段格式由 binding tag 管）。
func (r CreateProviderReq) Validate() error {
	return validateProvider(r.Name, r.Kind, r.BaseURL, r.APIKey, r.ExtraConfig, true)
}

// UpdateProviderReq 整体更新请求。Kind 不可改（改 kind 会使已存 models / auth_config 语义错位）；
// APIKey 空串 = 保持不变，非空 = 轮换并刷新 rotated_at；Enabled 全量提交（PUT 语义）。
// ID 无 binding tag：handler 先用 GetProviderReq 绑路径 id 再赋值（同 demo 模式），ID>0 由 Validate 兜底。
type UpdateProviderReq struct {
	ID          uint64
	Name        string         `json:"name" binding:"required,min=1,max=128"`
	BaseURL     string         `json:"base_url" binding:"omitempty,max=512"`
	APIKey      string         `json:"api_key" binding:"omitempty,max=512"`
	Enabled     bool           `json:"enabled"`
	ExtraConfig map[string]any `json:"extra_config"`
}

// Validate 跨字段校验；更新不含 kind，kind 相关规则由 service 取实体后兜底。
func (r UpdateProviderReq) Validate() error {
	if r.ID == 0 {
		return fmt.Errorf("id 必填")
	}
	return validateProvider(r.Name, "", r.BaseURL, r.APIKey, r.ExtraConfig, false)
}

// ValidateWithKind 用库内真实 kind 补全跨字段校验（service 取实体后调用，也保护绕过
// handler 的跨模块调用方）：openai_compatible 必填 base_url、extra_config.keep_alive 仅 ollama。
func (r UpdateProviderReq) ValidateWithKind(kind string) error {
	if r.ID == 0 {
		return fmt.Errorf("id 必填")
	}
	return validateProvider(r.Name, kind, r.BaseURL, r.APIKey, r.ExtraConfig, false)
}

// GetProviderReq / DeleteProviderReq 单条取 / 删请求（路径参数 id）。
type GetProviderReq struct {
	ID uint64 `uri:"id" binding:"required"`
}

// Validate 跨字段校验；当前无跨字段规则。
func (r GetProviderReq) Validate() error { return nil }

// DeleteProviderReq 删除提供商请求（级联删 models 与 provider_health；在用的模型被 agents 的
// RESTRICT 外键挡住并报错提示先解绑）。
type DeleteProviderReq struct {
	ID uint64 `uri:"id" binding:"required"`
}

// Validate 跨字段校验；当前无跨字段规则。
func (r DeleteProviderReq) Validate() error { return nil }

// ListProvidersReq 偏移分页列表请求（配置表，按接口规范"极小静态表用偏移分页"）。
// Kind / Enabled 可选筛选；Enabled 用指针区分「未传」与「显式 false」。
type ListProvidersReq struct {
	Page     int    `form:"page" binding:"omitempty,min=1"`
	PageSize int    `form:"page_size" binding:"omitempty,min=1,max=100"`
	Kind     string `form:"kind" binding:"omitempty,max=32"`
	Enabled  *bool  `form:"enabled"`
}

// Validate 跨字段校验：kind 非空时必须是五类之一；分页归一化由 platform/page 负责。
func (r ListProvidersReq) Validate() error {
	if r.Kind != "" && !validKind[r.Kind] {
		return fmt.Errorf("kind 必须是 %s / %s / %s / %s / %s 之一",
			KindOpenAI, KindClaude, KindGemini, KindOllama, KindOpenAICompatible)
	}
	return nil
}

// TestConnectionReq 手动连通性探测请求（按 kind 选探测端点，写 provider_health 后返回结果本体）。
type TestConnectionReq struct {
	ID uint64 `uri:"id" binding:"required"`
}

// Validate 跨字段校验；当前无跨字段规则。
func (r TestConnectionReq) Validate() error { return nil }

// ConnectionTestSchema 连通性测试结果（HTTP 探测的响应视图；状态机转移后的 health 快照另经 Get 现读）。
// ErrorMessage 只含 HTTP 状态码 / 网络错误类与截断的响应摘要（响应体片段）。探测请求头从不回显到本字段，
// 故不含 API Key；唯一例外是错配网关把请求头反射进错误页体——属运维配置问题，非本契约可防御。
type ConnectionTestSchema struct {
	Success      bool   `json:"success"`
	LatencyMs    int32  `json:"latency_ms"`
	ModelCount   int32  `json:"model_count"`             // 响应可解析的模型数；不可解析（如 429）为 0
	ErrorMessage string `json:"error_message,omitempty"` // 失败时非空；截断 200 字符
}

// ---- Model Req ----

// CreateModelReq 创建模型请求（手动录入，source=manual；自动发现走 SyncModels）。
// Enabled 省略 = true（新模型默认可用）。
type CreateModelReq struct {
	ProviderID      uint64         `json:"provider_id" binding:"required"`
	Name            string         `json:"name" binding:"required,min=1,max=128"`
	ModelID         string         `json:"model_id" binding:"required,min=1,max=128"`
	Capability      string         `json:"capability" binding:"required,oneof=chat embedding"`
	ContextWindow   *int64         `json:"context_window" binding:"omitempty,min=1"`
	MaxOutputTokens *int64         `json:"max_output_tokens" binding:"omitempty,min=1"`
	InputPrice      *string        `json:"input_price" binding:"omitempty,max=16"`
	OutputPrice     *string        `json:"output_price" binding:"omitempty,max=16"`
	EmbeddingDim    *int32         `json:"embedding_dim" binding:"omitempty,min=1"`
	Enabled         *bool          `json:"enabled"`
	ExtraParams     map[string]any `json:"extra_params"`
}

// Validate 跨字段校验（embedding 维度规则、价格格式、extra_params 白名单）。
func (r CreateModelReq) Validate() error {
	return validateModel(r.Capability, r.EmbeddingDim, r.InputPrice, r.OutputPrice, r.ExtraParams)
}

// UpdateModelReq 整体更新请求。ID 无 binding tag：handler 先用 GetModelReq 绑路径 id 再赋值。
type UpdateModelReq struct {
	ID              uint64
	ProviderID      uint64         `json:"provider_id" binding:"required"`
	Name            string         `json:"name" binding:"required,min=1,max=128"`
	ModelID         string         `json:"model_id" binding:"required,min=1,max=128"`
	Capability      string         `json:"capability" binding:"required,oneof=chat embedding"`
	ContextWindow   *int64         `json:"context_window" binding:"omitempty,min=1"`
	MaxOutputTokens *int64         `json:"max_output_tokens" binding:"omitempty,min=1"`
	InputPrice      *string        `json:"input_price" binding:"omitempty,max=16"`
	OutputPrice     *string        `json:"output_price" binding:"omitempty,max=16"`
	EmbeddingDim    *int32         `json:"embedding_dim" binding:"omitempty,min=1"`
	Enabled         bool           `json:"enabled"`
	ExtraParams     map[string]any `json:"extra_params"`
}

// Validate 跨字段校验；ID>0 兜底。
func (r UpdateModelReq) Validate() error {
	if r.ID == 0 {
		return fmt.Errorf("id 必填")
	}
	return validateModel(r.Capability, r.EmbeddingDim, r.InputPrice, r.OutputPrice, r.ExtraParams)
}

// GetModelReq / DeleteModelReq 单条取 / 删请求（路径参数 id）。
type GetModelReq struct {
	ID uint64 `uri:"id" binding:"required"`
}

// Validate 跨字段校验；当前无跨字段规则。
func (r GetModelReq) Validate() error { return nil }

// DeleteModelReq 删除模型请求（被 agents / knowledge_bases 引用时 RESTRICT 挡住）。
type DeleteModelReq struct {
	ID uint64 `uri:"id" binding:"required"`
}

// Validate 跨字段校验；当前无跨字段规则。
func (r DeleteModelReq) Validate() error { return nil }

// ListModelsReq 某提供商下模型列表（路径 id 即 provider_id；偏移分页）。
type ListModelsReq struct {
	ProviderID uint64 `uri:"id" binding:"required"`
	Page       int    `form:"page" binding:"omitempty,min=1"`
	PageSize   int    `form:"page_size" binding:"omitempty,min=1,max=100"`
}

// Validate 跨字段校验；当前无跨字段规则。
func (r ListModelsReq) Validate() error { return nil }

// SyncModelsReq 自动发现并同步模型请求（只增改不删，不覆盖手编字段：价格 / enabled / display_name / extra_params）。
type SyncModelsReq struct {
	ID uint64 `uri:"id" binding:"required"`
}

// Validate 跨字段校验；当前无跨字段规则。
func (r SyncModelsReq) Validate() error { return nil }

// ---- Schema（响应；永不携带密文） ----

// ProviderSchema 提供商响应：列表给 HasAPIKey，详情另填 APIKeyMasked（解密后打码）。
type ProviderSchema struct {
	schema.BaseSchema                // id 字符串化 + created_at / updated_at
	Name              string         `json:"name"`
	Kind              string         `json:"kind"`
	BaseURL           string         `json:"base_url"`
	HasAPIKey         bool           `json:"has_api_key"`
	APIKeyMasked      string         `json:"api_key_masked,omitempty"` // 仅详情接口填充
	APIKeyRotatedAt   *time.Time     `json:"api_key_rotated_at"`
	ExtraConfig       map[string]any `json:"extra_config"`
	Enabled           bool           `json:"enabled"`
}

// ModelSchema 模型响应。
type ModelSchema struct {
	schema.BaseSchema
	ProviderID      string         `json:"provider_id"`
	Name            string         `json:"name"`
	ModelID         string         `json:"model_id"` // API 标识；注意与外键 model_id（指向 models.id）语义不同
	Capability      string         `json:"capability"`
	ContextWindow   *int64         `json:"context_window"`
	MaxOutputTokens *int64         `json:"max_output_tokens"`
	InputPrice      *string        `json:"input_price"` // USD/百万 token，字符串金额（JS 浮点安全）
	OutputPrice     *string        `json:"output_price"`
	EmbeddingDim    *int32         `json:"embedding_dim"`
	Enabled         bool           `json:"enabled"`
	Source          string         `json:"source"`
	ExtraParams     map[string]any `json:"extra_params"`
}

// ProviderHealthSchema 健康响应。主键是 provider_id（非代理 id），不 embed schema.BaseSchema。
type ProviderHealthSchema struct {
	ProviderID    string     `json:"provider_id"`
	Status        string     `json:"status"`
	LastCheckAt   *time.Time `json:"last_check_at"`
	LastSuccessAt *time.Time `json:"last_success_at"`
	FailCount     int32      `json:"fail_count"`
	LatencyMs     *int32     `json:"latency_ms"`
	ErrorMessage  string     `json:"error_message"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// ProviderDetailSchema 详情聚合：provider 本体（内嵌，JSON 扁平展开）+ 该提供商模型列表 + 健康状态。
// 仅 Get 详情接口使用；列表不聚合（N+1 无意义）。health 无行（从未探测）为 null，models 空为 []。
type ProviderDetailSchema struct {
	ProviderSchema
	Models []ModelSchema         `json:"models"`
	Health *ProviderHealthSchema `json:"health"`
}

// ModelSyncResultSchema 模型自动发现结果。
type ModelSyncResultSchema struct {
	Added   int `json:"added"`
	Updated int `json:"updated"`
}

// ProviderListResult / ModelListResult 偏移分页结果（喂给 respond.OKWithOffset）。
type ProviderListResult struct {
	Items    []ProviderSchema `json:"items"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
	Total    int64            `json:"total"`
}

// ModelListResult 模型偏移分页结果。
type ModelListResult struct {
	Items    []ModelSchema `json:"items"`
	Page     int           `json:"page"`
	PageSize int           `json:"page_size"`
	Total    int64         `json:"total"`
}
