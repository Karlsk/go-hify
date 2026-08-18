# Provider 模块 Model + Schema 实施 Spec

> 状态：**待人工确认**（2026-08-18）。确认后按本文件实施；数据模型决策背景见 [db_model.md](db_model.md)。
> 已确认的三项决策：① Go 类型 `Model`、表 `models`；② 新建 `internal/platform/schema` 承载 Base Schema；③ 范围 = api 层（接口 / schema / 哨兵）+ service/model.go + 重写 00002。

## 1. 范围

| 做 | 不做（下一批） |
|---|---|
| 新建 `internal/platform/schema`（BaseSchema） | service 实现（业务方法 / crypto.go / StartProber / sync） |
| `provider/service/model.go`：Provider / Model / ProviderHealth 三实体 | store 层（GORM CRUD） |
| `provider/api/`：schema.go（Req/Schema/Validate）、api.go（接口）、errors.go（哨兵补全） | handler 层与路由挂载 |
| 重写 `migrations/00002_provider.sql` 为最终态三张表 | app 组合根装配 |
| `provider/api/schema_test.go`（Validate 用例） | 前端对接 |
| 文档同步：CLAUDE.md ×3 处、data-model.md provider 小节 | |

前提核实：`internal/app` 尚未引用 provider 模块（仅 banner 读 env），本批代码独立可编译；**不引入任何新第三方依赖**（价格列用 string 承载 numeric）。

## 2. 新建 internal/platform/schema

```go
// Package schema 提供各模块 api 契约共用的响应基类。
// 仅依赖标准库——api 叶子包（不 import gin/gorm）可安全引用，与 platform/db 的 model mixin 对称。
package schema

import "time"

// BaseSchema 可变表响应基类：ID 字符串化（防 JS 超 2^53 丢精度）+ 双时间戳。
// 各模块 XxxSchema embed 本结构获得统一表头；主键非代理 id（如 provider_health）或
// append-only 表（无 updated_at）不适用，自行声明。
type BaseSchema struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
```

CLAUDE.md 同步（2 处）：

- 《包结构》platform 清单加一行：`schema/ # api 契约共用响应基类（仅标准库；BaseSchema）`
- 《模块内部结构》api 规则措辞：`api/ 是叶子包，只 import 标准库` → `api/ 是叶子包，不 import gin/gorm（仅标准库 + platform 纯标准库小包如 schema）`

## 3. provider/service/model.go（占位 → 实现）

```go
package service

import (
	"time"

	"github.com/Karlsk/go-hify/internal/platform/db"
)

// Provider 提供商配置（GORM 实体，模块私有）；表结构见 migrations/00002_provider.sql。
type Provider struct {
	db.BaseMutable // id + created_at + updated_at
	Name            string            `gorm:"not null"`            // 展示名，唯一（uq 由迁移持有）
	Kind            string            `gorm:"not null"`            // openai/claude/gemini/ollama/openai_compatible
	BaseURL         string            // 空串 = kind 默认地址（常量表在 service）
	AuthConfig      map[string]string `gorm:"type:jsonb;serializer:json"` // 密文只在 api_key_encrypted 键
	APIKeyRotatedAt *time.Time
	ExtraConfig     map[string]any `gorm:"type:jsonb;serializer:json"`   // bulkhead/ttft_seconds/keep_alive
	Enabled         bool           `gorm:"not null;default:true"`
}
func (Provider) TableName() string { return "providers" }

// Model 提供商下的模型；注意 ModelID 是 API 标识字符串，agents.model_id 等外键指向本表 id。
type Model struct {
	db.BaseMutable
	ProviderID      uint64 `gorm:"not null"`
	Name            string `gorm:"not null"` // 展示名
	ModelID         string `gorm:"not null"` // API 标识；uq(provider_id, model_id) 由迁移持有
	Capability      string `gorm:"not null"` // chat / embedding
	ContextWindow   *int64
	MaxOutputTokens *int64
	InputPrice      *string `gorm:"type:numeric(12,4)"` // USD/1M token；nil = 不计费
	OutputPrice     *string `gorm:"type:numeric(12,4)"`
	EmbeddingDim    *int32  // 仅 capability=embedding
	Enabled         bool    `gorm:"not null;default:true"`
	Source          string  `gorm:"not null;default:'manual'"`
	ExtraParams     map[string]any `gorm:"type:jsonb;serializer:json"` // think_level 等
}
func (Model) TableName() string { return "models" }

// ProviderHealth 供应商健康（1:1）。无代理主键，不 embed 带 id 的 mixin，自声明表头。
type ProviderHealth struct {
	ProviderID    uint64     `gorm:"primaryKey"`
	Status        string     `gorm:"not null;default:'unknown'"` // unknown/up/degraded/down
	LastCheckAt   *time.Time
	LastSuccessAt *time.Time
	FailCount     int32      `gorm:"not null;default:0"`
	LatencyMs     *int32
	ErrorMessage  string
	CreatedAt     time.Time `gorm:"type:timestamptz;not null;default:now()"`
	UpdatedAt     time.Time `gorm:"type:timestamptz;not null;default:now();autoUpdateTime"`
}
func (ProviderHealth) TableName() string { return "provider_health" }
```

**价格列用 `*string` 的理由**：金额禁浮点；PG `numeric` 与 Go string 经 pgx 文本协议可靠互转（pgx 对 string 参数用 unknown OID，由服务端按列上下文推导 numeric），零新依赖。budget 模块落地时如需十进制运算再评估 shopspring/decimal。

## 4. provider/api/schema.go

### Schema（3 个）

- `ProviderSchema`：embed `schema.BaseSchema` + Name / Kind / BaseURL / `HasAPIKey bool` / `APIKeyMasked string`（`json:"api_key_masked,omitempty"`，仅详情填充）/ APIKeyRotatedAt / ExtraConfig / Enabled。**永不含密文**。
- `ModelSchema`：embed `schema.BaseSchema` + ProviderID（字符串化）/ Name / ModelID / Capability / ContextWindow / MaxOutputTokens / InputPrice / OutputPrice（`*string`）/ EmbeddingDim / Enabled / Source / ExtraParams。
- `ProviderHealthSchema`：**不 embed BaseSchema**（主键是 provider_id）——ProviderID（字符串化）/ Status / LastCheckAt / LastSuccessAt / FailCount / LatencyMs / ErrorMessage / UpdatedAt。
- 列表结果沿 demo 模式：`ProviderListResult{Items []ProviderSchema; Page; PageSize; Total int64}`、`ModelListResult{...}` 同构。

### Req（binding tag 管字段格式，Validate 管跨字段）

| Req | 字段与规则 |
|---|---|
| `CreateProviderReq` | Name（required,1-128）/ Kind（required, `oneof=openai claude gemini ollama openai_compatible`）/ BaseURL（omitempty,max=512）/ APIKey（明文入参，service 加密）/ ExtraConfig |
| `UpdateProviderReq` | ID + 同上；**不含 Kind——创建后不可改**（改 kind 会使已存 models / auth_config 语义错位）；APIKey 空串 = 保持不变，非空 = 轮换并刷新 rotated_at |
| `GetProviderReq` / `DeleteProviderReq` | ID（uri, required） |
| `ListProvidersReq` | Page / PageSize（form，偏移分页——配置表例外条款） |
| `TestConnectionReq` | ID（uri, required）——返回 `ProviderHealthSchema` |
| `CreateModelReq` | ProviderID（required）/ Name（required,1-128）/ ModelID（required,1-128）/ Capability（required, `oneof=chat embedding`）/ ContextWindow、MaxOutputTokens（omitempty,min=1）/ InputPrice、OutputPrice（omitempty，数值字符串）/ EmbeddingDim（omitempty,min=1）/ Enabled / ExtraParams |
| `UpdateModelReq` | ID + 同上 |
| `GetModelReq` / `DeleteModelReq` | ID（uri, required） |
| `ListModelsReq` | ProviderID（uri, required）+ Page / PageSize |
| `SyncModelsReq` | ID（uri, required）——返回 `ModelSyncResultSchema{Added, Updated int}` |

### Validate 跨字段规则（本 spec 新定细则）

1. **Provider**：kind ∈ {openai, claude, gemini} ⇒ APIKey 必填；kind=openai_compatible ⇒ BaseURL 必填且 http(s):// 前缀；BaseURL 非空时校验前缀（尾斜杠归一化在 service）。ExtraConfig 白名单：`bulkhead`（int，1-128）/ `ttft_seconds`（int，1-600）/ `keep_alive`（string，仅 ollama）；未知键拒绝。
2. **Model**：capability=embedding ⇒ EmbeddingDim 必填；capability=chat ⇒ EmbeddingDim 必须为空。价格格式 `^\d+(\.\d{1,4})?$` 且 < 10^8。ExtraParams 白名单：`think_level`（`oneof=off low medium high`）；未知键拒绝。
3. 全部 Req 实现 `Validate() error`（满足 `respond.Validatable`）。

## 5. provider/api/api.go（接口声明，实现下一批）

```go
// ProviderService 提供商配置契约；实现在 service 包，组合根注入。
type ProviderService interface {
	Create(ctx context.Context, req CreateProviderReq) (*ProviderSchema, error)
	Get(ctx context.Context, req GetProviderReq) (*ProviderSchema, error)
	List(ctx context.Context, req ListProvidersReq) (*ProviderListResult, error)
	Update(ctx context.Context, req UpdateProviderReq) (*ProviderSchema, error)
	Delete(ctx context.Context, req DeleteProviderReq) error
	// TestConnection 手动连通性探测，写 provider_health 后回读（DEGRADED 状态机见 db_model.md §2.3）。
	TestConnection(ctx context.Context, req TestConnectionReq) (*ProviderHealthSchema, error)
}

// ModelService 模型目录契约（含自动发现同步）。
type ModelService interface {
	Create(ctx context.Context, req CreateModelReq) (*ModelSchema, error)
	Get(ctx context.Context, req GetModelReq) (*ModelSchema, error)
	List(ctx context.Context, req ListModelsReq) (*ModelListResult, error)
	Update(ctx context.Context, req UpdateModelReq) (*ModelSchema, error)
	Delete(ctx context.Context, req DeleteModelReq) error
	// SyncModels 自动发现并 upsert（只增改不删，不覆盖手编字段：价格/enabled/display_name/extra_params）。
	SyncModels(ctx context.Context, req SyncModelsReq) (*ModelSyncResultSchema, error)
}
```

跨模块 RuntimeConfig 出口（chat/workflow 构造 LLM 调用用）随 service 实现批次加入。

## 6. provider/api/errors.go（哨兵补全）

```go
ErrModelNotFound    = errors.New("MODEL_NOT_FOUND")     // 404
ErrModelIDConflict  = errors.New("MODEL_ID_CONFLICT")   // 409，uq(provider_id, model_id)
ErrProviderDisabled = errors.New("PROVIDER_DISABLED")   // 503，停用后拒绝新调用
```

（既有 `ErrProviderNotFound` / `ErrProviderNameConflict` 不动；跨字段校验失败走 `respond.BindJSON` → 400 VALIDATION_FAILED，无需新哨兵。）

## 7. migrations/00002_provider.sql 重写

**以 [db_model.md](db_model.md) §3 的 DDL 逐字为准**落地（providers / models / provider_health 三张表 + 全部 COMMENT + goose Up/Down），本文不重复粘贴、避免两份漂移。

操作步骤（开发库重置）：`./hify migrate down` 逐级回退到 00001 之前（或直接 drop 重建 dev 库）→ `make migrate-up` 验证。此后迁移恢复"只增不改"。

## 8. 测试

`internal/provider/api/schema_test.go`（table-driven，覆盖 Validate 分支）：

- Provider：kind×APIKey 必填矩阵（5 类 kind）；openai_compatible 缺 base_url 拒绝；base_url 非 http(s) 拒绝；ExtraConfig 白名单（bulkhead 越界 0/129、未知键、ollama 之外的 keep_alive）
- Model：embedding 缺 EmbeddingDim 拒绝、chat 带 EmbeddingDim 拒绝；价格格式（`1.23456` 拒、`-1` 拒、`abc` 拒、`0` 允许）；think_level 非法值拒
- 合法用例各一条全绿

## 9. 文档同步

| 文件 | 改动 |
|---|---|
| CLAUDE.md | ① 包结构加 `platform/schema` 行；② api 叶子包规则措辞放宽（§2）；③ 错误码表加 MODEL_NOT_FOUND / MODEL_ID_CONFLICT / PROVIDER_DISABLED 三行 |
| docs/design/data-model.md | provider 小节同步：五类 kind、auth_config、provider_health 表加入表清单与关系图 |

## 10. 验收标准

- [ ] `go build ./...`、`go vet ./...` 通过
- [ ] `go test ./internal/provider/... ./internal/platform/schema/...` 通过（Validate 全分支）
- [ ] 迁移重放：dev 库重置后 `make migrate-up` 成功，`\d providers/models/provider_health` 与 db_model.md §3 一致
- [ ] `go.mod` 无新增依赖
- [ ] grep 确认 schema/Model 中无密文明文字段、无 json 序列化的 api_key
