// Package api 是 provider 模块的对外契约：提供商 / 模型管理接口、Req/Schema、哨兵错误。
// 纯叶子包：不 import gin/gorm（binding tag 是纯字符串；platform 纯标准库小包 schema 除外）。
//
// 数据模型与决策记录见 docs/changelog/provider/db_model.md；本批仅声明契约，service 实现见后续批次。
package api

import "context"

// ProviderService 提供商配置契约；实现在本模块 service 包，由组合根注入。
// 跨模块调用与 HTTP 请求复用同一套接口（CLAUDE.md《跨模块调用规则》）。
type ProviderService interface {
	// Create 创建提供商；名称唯一冲突返回 ErrProviderNameConflict。
	Create(ctx context.Context, req CreateProviderReq) (*ProviderSchema, error)
	// Get 取详情聚合（provider + models + health，含 api_key_masked）；不存在返回 ErrProviderNotFound。
	Get(ctx context.Context, req GetProviderReq) (*ProviderDetailSchema, error)
	// List 偏移分页列表。
	List(ctx context.Context, req ListProvidersReq) (*ProviderListResult, error)
	// Update 整体更新（kind 不可改；api_key 空串=不变，非空=轮换）；不存在返回 ErrProviderNotFound。
	Update(ctx context.Context, req UpdateProviderReq) (*ProviderSchema, error)
	// Delete 删除（级联删 models 与 provider_health；被 agent 引用的模型会挡住并报错）。
	Delete(ctx context.Context, req DeleteProviderReq) error
	// TestConnection 手动连通性探测（按 kind 分发端点）；按 DEGRADED 状态机写 provider_health，
	// 返回探测结果本体（health 快照经 Get 现读，不缓存）。
	TestConnection(ctx context.Context, req TestConnectionReq) (*ConnectionTestSchema, error)
}

// ModelService 模型目录契约（含自动发现同步）。
type ModelService interface {
	// Create 手动录入模型（source=manual）；标识唯一冲突返回 ErrModelIDConflict。
	Create(ctx context.Context, req CreateModelReq) (*ModelSchema, error)
	// Get 取单个；不存在返回 ErrModelNotFound。
	Get(ctx context.Context, req GetModelReq) (*ModelSchema, error)
	// List 某提供商下的模型（偏移分页）。
	List(ctx context.Context, req ListModelsReq) (*ModelListResult, error)
	// ListByIDs 按 id 集合批量取模型（id 升序）；跨模块列表聚合用。
	// 缺行不算错（悬空引用由调用方处理），仅 ids 为空或超限时校验失败。
	ListByIDs(ctx context.Context, req ListModelsByIDsReq) ([]ModelSchema, error)
	// Update 整体更新；不存在返回 ErrModelNotFound。
	Update(ctx context.Context, req UpdateModelReq) (*ModelSchema, error)
	// Delete 删除；被 agents / knowledge_bases 引用时外键挡住，返回 ErrModelInUse（service 层翻译）。
	Delete(ctx context.Context, req DeleteModelReq) error
	// SyncModels 自动发现并 upsert（只增改不删；不覆盖价格 / enabled / display_name / extra_params）。
	SyncModels(ctx context.Context, req SyncModelsReq) (*ModelSyncResultSchema, error)
	// ResolveLLMConfig 取一次 LLM 调用的 provider 侧配置（model → provider → 解密 key）。
	// 跨模块专用（chat / workflow 发起调用前）；不走 HTTP、不进缓存（明文 key 禁入缓存）。
	// 模型不存在 / 已停用返回 ErrModelNotFound / ErrModelDisabled；provider 悬空 / 停用
	// 返回 ErrProviderNotFound / ErrProviderDisabled；解密失败包装 errs.ErrInternal。
	ResolveLLMConfig(ctx context.Context, req ResolveLLMConfigReq) (*LLMConfig, error)
}
