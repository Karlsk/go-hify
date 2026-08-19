package service

import (
	"time"

	"github.com/Karlsk/go-hify/internal/platform/db"
)

// Provider 提供商配置（GORM 实体，模块私有，禁止跨模块）；表结构见 migrations/00002_provider.sql。
// 唯一约束（uq_providers_name）与 CHECK 由迁移 SQL 持有，model 不重复声明。
type Provider struct {
	db.BaseMutable                    // id + created_at + updated_at
	Name            string            `gorm:"not null"` // 展示名，唯一
	Kind            string            `gorm:"not null"` // openai/claude/gemini/ollama/openai_compatible
	BaseURL         string            // 空串 = kind 默认地址（默认值常量表在 service 层）
	AuthConfig      map[string]string `gorm:"type:jsonb;serializer:json"` // 鉴权材料；密文只出现在 api_key_encrypted 键
	APIKeyRotatedAt *time.Time        // 密钥最近一次轮换时间
	ExtraConfig     map[string]any    `gorm:"type:jsonb;serializer:json"` // 白名单：bulkhead / ttft_seconds / keep_alive
	Enabled         bool              `gorm:"not null"` // 停用后不发新请求；无 default tag——带 default:true 时 GORM 会把零值替换成 true（create.go 默认值写回），停用无法落库；创建路径均显式设值，DB 列默认 true 兜底直插 SQL
}

func (Provider) TableName() string { return "providers" }

// Model 提供商下的模型（GORM 实体，模块私有）。表结构见 migrations/00002_provider.sql。
// 注意：ModelID 是调用时传给供应商 API 的标识字符串；agents.model_id 等外键指向本表 id，语义不同。
type Model struct {
	db.BaseMutable
	ProviderID      uint64         `gorm:"not null"`
	Name            string         `gorm:"not null"` // 展示名，如 GPT-4o
	ModelID         string         `gorm:"not null"` // API 标识，如 gpt-4o；uq(provider_id, model_id) 由迁移持有
	Capability      string         `gorm:"not null"` // chat / embedding
	ContextWindow   *int64         // 上下文窗口（token）；nil = 未知
	MaxOutputTokens *int64         // 生成上限；Claude 协议 max_tokens 必填
	InputPrice      *string        `gorm:"type:numeric(12,4)"` // USD/百万 token；nil = 不计费
	OutputPrice     *string        `gorm:"type:numeric(12,4)"`
	EmbeddingDim    *int32         // 仅 capability=embedding；建 KB 时校验，vector(维度) 不可改
	Enabled         bool           `gorm:"not null"` // 模型级停用；sync 不覆盖。无 default tag——sync 导入 Enabled=false 必须显式落库，带 default:true 时 GORM 会把零值替换成 true（create.go 默认值写回），"默认停用"静默失效；DB 列默认 true 兜底直插 SQL
	Source          string         `gorm:"not null;default:'manual'"`  // discovered / manual
	ExtraParams     map[string]any `gorm:"type:jsonb;serializer:json"` // 白名单：think_level 等
}

func (Model) TableName() string { return "models" }

// ProviderHealth 供应商健康（1:1，GORM 实体）。无代理主键，不能 embed 带 id 的 mixin，自声明表头。
// 只记录探测结果（定时 + 手动）；熔断/槽位等运行时状态在 platform/llm 内存，不落库。
type ProviderHealth struct {
	ProviderID    uint64 `gorm:"primaryKey"`
	Status        string `gorm:"not null;default:'unknown'"` // unknown / up / degraded / down
	LastCheckAt   *time.Time
	LastSuccessAt *time.Time
	FailCount     int32     `gorm:"not null;default:0"` // 连续失败次数，成功清零
	LatencyMs     *int32    // 最近探测往返延迟
	ErrorMessage  string    // 最近失败原因（截断，不含敏感信息）
	CreatedAt     time.Time `gorm:"type:timestamptz;not null;default:now()"`
	UpdatedAt     time.Time `gorm:"type:timestamptz;not null;default:now();autoUpdateTime"`
}

func (ProviderHealth) TableName() string { return "provider_health" }
