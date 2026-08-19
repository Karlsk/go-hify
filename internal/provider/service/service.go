// Package service 是 provider 模块的业务层：实现 api 的 ProviderService / ModelService、
// API Key 加密（crypto.go）、provider-cache Cache-Aside 缓存、schema↔model 转换、错误翻译。
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"github.com/Karlsk/go-hify/internal/platform/cache"
	"github.com/Karlsk/go-hify/internal/platform/errs"
	"github.com/Karlsk/go-hify/internal/platform/page"
	providerapi "github.com/Karlsk/go-hify/internal/provider/api"
)

// Store 数据层接口：定义在消费方（本包），store 包实现，组合根注入——依赖倒置。
// 一个模块一个 store，provider / model 两个服务共享；方法名带实体前缀消歧
// （CLAUDE.md 模板的方法命名按单实体模块给出，双实体模块加前缀是必要偏离）。
// 本批 CRUD 无跨表原子写（级联删由 DB 外键完成），故无 WithTx；探测 / 同步批次按需再补。
type Store interface {
	// provider
	CreateProvider(ctx context.Context, p *Provider) error
	GetProviderByID(ctx context.Context, id uint64) (*Provider, error) // 未找到 → gorm.ErrRecordNotFound
	GetProviderByName(ctx context.Context, name string) (*Provider, error)
	// ListProviders 全表 id 升序；筛选 / 分页在 service 内存做（整表快照缓存形态）。
	ListProviders(ctx context.Context) ([]Provider, error)
	UpdateProvider(ctx context.Context, p *Provider) error
	DeleteProvider(ctx context.Context, id uint64) error // RowsAffected=0 → gorm.ErrRecordNotFound

	// model
	CreateModel(ctx context.Context, m *Model) error
	// CreateModels 批量插入（单条多 VALUES INSERT，CLAUDE.md《事务规范》：批量写禁循环单行）；sync 专用。
	CreateModels(ctx context.Context, ms []*Model) error
	GetModelByID(ctx context.Context, id uint64) (*Model, error)
	GetModelByProviderAndModelID(ctx context.Context, providerID uint64, modelID string) (*Model, error)
	ListModelsByProvider(ctx context.Context, providerID uint64) ([]Model, error) // 详情聚合用，id 升序
	ListModels(ctx context.Context, providerID uint64, p page.OffsetParams) (page.OffsetResult[Model], error)
	UpdateModel(ctx context.Context, m *Model) error
	// UpdateModelName sync 专用列级更新：只写 name / updated_at（WHERE 限定 source='discovered'），
	// 防止 Save 全列回写覆盖并发的手工编辑（价格 / enabled / extra_params）。
	UpdateModelName(ctx context.Context, id uint64, name string) error
	DeleteModel(ctx context.Context, id uint64) error // RowsAffected=0 → gorm.ErrRecordNotFound

	// health
	GetHealthByProviderID(ctx context.Context, providerID uint64) (*ProviderHealth, error) // 无行 → gorm.ErrRecordNotFound（service 归 nil）
	// GetHealthByProviderIDForUpdate 探测状态机事务内的锁定读（FOR UPDATE），串行化并发探测的读-改-写。
	GetHealthByProviderIDForUpdate(ctx context.Context, providerID uint64) (*ProviderHealth, error)
	UpsertHealth(ctx context.Context, h *ProviderHealth) error // 探测写入：无行插入、有行更新
	// WithTx 探测读-算-写事务：锁住 provider_health 行防并发探测的 fail_count 计数竞态
	// （两个并发探测都读到 n、都写 n+1，真实值应为 n+2——连续失败阈值被延迟）。fn 只操作本模块表。
	WithTx(ctx context.Context, fn func(tx Store) error) error
}

// cacheManager 收窄的缓存接口（小接口惯例）：*cache.Cache 凭结构化类型满足；service 持
// 接口而非具体类型，单测可 stub。
type cacheManager interface {
	Get(ctx context.Context, name, key string, dst any) (bool, error)
	Set(ctx context.Context, name, key string, val any) error
	Delete(ctx context.Context, name, key string) error
}

// provider-cache 的 key 形态（全键 = hify:cache:provider-cache:{以下}，前缀由 cache 包拼接）。
// 失效矩阵：list 只被 provider 写操作失效；detail:{id} 被 provider 更新 / 删除、model 增删改
// （换绑时新旧两处）失效。health 不在缓存载荷里（探测写库无需失效，Get 现读单行主键查询）。
// 缓存里只存 schema——密文 / 明文 key 永不进缓存。
const (
	cacheKeyList   = "list"      // 全表快照（[]ProviderSchema）
	cacheKeyDetail = "detail:%d" // 详情聚合（ProviderDetailSchema）
)

// PG 错误码：store 原样上抛 pgconn.PgError，service 在此翻译成 api 哨兵（边界翻译）。
const (
	pgCodeUniqueViolation = "23505" // 唯一约束：check-then-insert 的竞态兜底
	pgCodeFKViolation     = "23503" // 外键违例：删除被引用行（RESTRICT）
)

// providerService 实现 providerapi.ProviderService。
type providerService struct {
	store    Store
	cm       cacheManager
	master   []byte        // API Key 加密主密钥（32B，来自 config.Provider.MasterKey）
	probe    probeClient   // 连通性探测 HTTP client（NewProbeClient；测试注入 httptest 桩）
	interval time.Duration // 定时探测轮询间隔（默认 probeInterval；测试注入短间隔）

	// decryptFailWarned 已打「解密失败」WARN 的 provider id 集合：定时探测每分钟一轮，主密钥轮换后的
	// 旧密文会永久解密失败，不去重则每 60s 刷一条 WARN。once-per-provider 去重；重新录入 key 产生
	// 可解密密文后不再命中。sync.Map 零值即用，无需构造。
	decryptFailWarned sync.Map // key: providerID(uint64)
}

// NewProviderService 构造提供商服务；cm 由组合根传 *cache.Cache，单测可 stub cacheManager。
// masterKey 必须 32 字节（组合根在启动期调用，非法即 panic fail-fast，与 config 层双重保险）。
// 探测 client 在此内部组装（共享 LLM transport + 10s 超时），无需组合根关心。
// 返回二元组：业务消费方用 api.ProviderService（注入 handler / 上游模块）；组合根另拿
// Prober 起定时探测 goroutine（wiring_sync_spec.md §1.1，澄清 2A）。
func NewProviderService(store Store, cm cacheManager, masterKey []byte) (providerapi.ProviderService, Prober) {
	if len(masterKey) != 32 {
		panic("service: provider master key must be 32 bytes (config PROVIDER_MASTER_KEY)")
	}
	svc := &providerService{store: store, cm: cm, master: masterKey, probe: NewProbeClient(), interval: probeInterval}
	return svc, svc
}

// Prober 定时健康探测窄接口（单方法）：仅组合根使用（go prober.StartProber(appCtx)，随
// appCtx 取消退出）；业务消费方一律用 api.ProviderService，不需要本接口。
type Prober interface {
	StartProber(ctx context.Context)
}

var _ Prober = (*providerService)(nil) // 编译期断言

// modelService 实现 providerapi.ModelService。
type modelService struct {
	store  Store
	cm     cacheManager
	master []byte      // API Key 解密主密钥（SyncModels 拉取上游列表要用；32B，构造期校验）
	probe  probeClient // 模型列表 GET client（复用探测 client：共享 LLM transport + 10s 超时）
}

// NewModelService 构造模型服务；masterKey 供 SyncModels 解密 API Key（32B 校验同上）。
// 模型列表 client 在此内部组装，无需组合根关心。
func NewModelService(store Store, cm cacheManager, masterKey []byte) providerapi.ModelService {
	if len(masterKey) != 32 {
		panic("service: provider master key must be 32 bytes (config PROVIDER_MASTER_KEY)")
	}
	return &modelService{store: store, cm: cm, master: masterKey, probe: NewProbeClient()}
}

// ---- ProviderService ----

// Create 创建提供商：名称查重（竞态由 uq_providers_name 兜底）→ API Key 加密入库 → 失效列表快照。
// 新提供商固定 enabled=true（停用走更新）；kind 创建后不可改（Update 请求无此字段）。
func (s *providerService) Create(ctx context.Context, req providerapi.CreateProviderReq) (*providerapi.ProviderSchema, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate create provider: %w", err)
	}
	if _, err := s.store.GetProviderByName(ctx, req.Name); err == nil {
		return nil, providerapi.ErrProviderNameConflict
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("check provider name %s: %w", req.Name, err)
	}

	p := &Provider{
		Name:        req.Name,
		Kind:        req.Kind,
		BaseURL:     req.BaseURL,
		AuthConfig:  map[string]string{},
		ExtraConfig: nonNilAny(req.ExtraConfig),
		Enabled:     true,
	}
	if req.APIKey != "" { // ollama / openai_compatible 可无 key；给了就加密存
		enc, err := encryptAPIKey(s.master, req.APIKey)
		if err != nil {
			return nil, fmt.Errorf("encrypt api key: %w", err)
		}
		p.AuthConfig[apiKeyEncryptedKey] = enc
		now := time.Now()
		p.APIKeyRotatedAt = &now
	}
	if err := s.store.CreateProvider(ctx, p); err != nil {
		if isUniqueViolation(err) {
			return nil, providerapi.ErrProviderNameConflict
		}
		return nil, fmt.Errorf("create provider: %w", err)
	}
	evict(ctx, s.cm, cacheKeyList)
	return toProviderSchema(p), nil
}

// Get 取详情聚合（provider + models + health），仅此接口填 api_key_masked（解密 → 打码；
// 解密失败降级为只给 has_api_key）。缓存载荷不含 health（探测随时写库，进缓存就会读到旧值），
// 命中缓存后仍现读 provider_health 单行（主键查询，代价可忽略）填充。
func (s *providerService) Get(ctx context.Context, req providerapi.GetProviderReq) (*providerapi.ProviderDetailSchema, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate get provider: %w", err)
	}
	key := fmt.Sprintf(cacheKeyDetail, req.ID)
	var cached providerapi.ProviderDetailSchema
	if found, err := s.cm.Get(ctx, cache.NameProvider, key, &cached); err != nil {
		slog.WarnContext(ctx, "read provider cache failed; fallback to db", "key", key, "err", err)
	} else if found {
		if err := s.fillHealth(ctx, req.ID, &cached); err != nil {
			return nil, err
		}
		return &cached, nil
	}

	p, err := s.store.GetProviderByID(ctx, req.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, providerapi.ErrProviderNotFound
		}
		return nil, fmt.Errorf("get provider %d: %w", req.ID, err)
	}
	detail, err := s.assembleDetail(ctx, p)
	if err != nil {
		return nil, err
	}
	if err := s.cm.Set(ctx, cache.NameProvider, key, detail); err != nil {
		slog.WarnContext(ctx, "set provider cache failed", "key", key, "err", err)
	}
	if err := s.fillHealth(ctx, p.ID, detail); err != nil {
		return nil, err
	}
	return detail, nil
}

// assembleDetail 组装详情：模型列表 + 打码 key。不含 health——那是探测的高频写路径，
// 从缓存载荷剔除后探测写库无需失效 detail（写时删 key 的失效矩阵少一条边）。
func (s *providerService) assembleDetail(ctx context.Context, p *Provider) (*providerapi.ProviderDetailSchema, error) {
	ms, err := s.store.ListModelsByProvider(ctx, p.ID)
	if err != nil {
		return nil, fmt.Errorf("list models of provider %d: %w", p.ID, err)
	}
	models := make([]providerapi.ModelSchema, 0, len(ms))
	for i := range ms {
		models = append(models, *toModelSchema(&ms[i]))
	}
	detail := &providerapi.ProviderDetailSchema{
		ProviderSchema: *toProviderSchema(p),
		Models:         models,
	}
	if enc := p.AuthConfig[apiKeyEncryptedKey]; enc != "" {
		if pt, err := decryptAPIKey(s.master, enc); err == nil {
			detail.APIKeyMasked = maskAPIKey(pt)
		} else {
			slog.WarnContext(ctx, "decrypt api key failed; skip mask", "provider_id", p.ID, "err", err)
		}
	}
	return detail, nil
}

// fillHealth 现读 provider_health 填充详情的 Health（无行为 nil，保持 schema 约定）。
func (s *providerService) fillHealth(ctx context.Context, providerID uint64, d *providerapi.ProviderDetailSchema) error {
	h, err := s.store.GetHealthByProviderID(ctx, providerID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			d.Health = nil
			return nil
		}
		return fmt.Errorf("get health of provider %d: %w", providerID, err)
	}
	d.Health = toHealthSchema(h)
	return nil
}

// List 偏移分页列表：整表快照缓存（list）命中后在内存做 kind / enabled 筛选与分页。
// providers 是极小配置表——全表 + 内存筛选比逐查询缓存 key 的模式失效简单且命中率高。
func (s *providerService) List(ctx context.Context, req providerapi.ListProvidersReq) (*providerapi.ProviderListResult, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate list providers: %w", err)
	}
	var cached []providerapi.ProviderSchema
	if found, err := s.cm.Get(ctx, cache.NameProvider, cacheKeyList, &cached); err != nil {
		slog.WarnContext(ctx, "read provider list cache failed; fallback to db", "err", err)
	} else if found {
		return filterPaginateProviders(cached, req), nil
	}

	ps, err := s.store.ListProviders(ctx)
	if err != nil {
		return nil, fmt.Errorf("list providers: %w", err)
	}
	all := make([]providerapi.ProviderSchema, 0, len(ps))
	for i := range ps {
		all = append(all, *toProviderSchema(&ps[i]))
	}
	if err := s.cm.Set(ctx, cache.NameProvider, cacheKeyList, all); err != nil {
		slog.WarnContext(ctx, "set provider list cache failed", "err", err)
	}
	return filterPaginateProviders(all, req), nil
}

// filterPaginateProviders 内存筛选（kind / enabled）+ 偏移分页；Total = 筛选后总数。
func filterPaginateProviders(all []providerapi.ProviderSchema, req providerapi.ListProvidersReq) *providerapi.ProviderListResult {
	filtered := make([]providerapi.ProviderSchema, 0, len(all))
	for _, p := range all {
		if req.Kind != "" && p.Kind != req.Kind {
			continue
		}
		if req.Enabled != nil && p.Enabled != *req.Enabled {
			continue
		}
		filtered = append(filtered, p)
	}
	p := page.NewOffset(req.Page, req.PageSize)
	start, end := clampWindow(p.Offset(), p.Limit(), len(filtered))
	return &providerapi.ProviderListResult{
		Items:    filtered[start:end],
		Page:     p.Page,
		PageSize: p.PageSize,
		Total:    int64(len(filtered)),
	}
}

// Update 整体更新（PUT）：kind 不可改；api_key 空串 = 不变，非空 = 轮换并刷新 rotated_at。
// kind 相关校验用库内真实 kind 补全（ValidateWithKind），保护绕过 handler 的调用方。
func (s *providerService) Update(ctx context.Context, req providerapi.UpdateProviderReq) (*providerapi.ProviderSchema, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate update provider: %w", err)
	}
	p, err := s.store.GetProviderByID(ctx, req.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, providerapi.ErrProviderNotFound
		}
		return nil, fmt.Errorf("get provider %d: %w", req.ID, err)
	}
	if err := req.ValidateWithKind(p.Kind); err != nil {
		// 包 ErrValidationFailed（而非裸 Validate 错误）：kind 规则只有取到库内 kind 才能验
		//（handler 预校验做不到），经 FailFromSentinel 映射 400 而非误报 500。
		return nil, fmt.Errorf("%w: %s", errs.ErrValidationFailed, err)
	}
	if p.Name != req.Name {
		if _, err := s.store.GetProviderByName(ctx, req.Name); err == nil {
			return nil, providerapi.ErrProviderNameConflict
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("check provider name %s: %w", req.Name, err)
		}
	}

	p.Name = req.Name
	p.BaseURL = req.BaseURL
	p.Enabled = req.Enabled
	p.ExtraConfig = nonNilAny(req.ExtraConfig)
	if req.APIKey != "" {
		enc, err := encryptAPIKey(s.master, req.APIKey)
		if err != nil {
			return nil, fmt.Errorf("encrypt api key: %w", err)
		}
		// 合并非替换：auth_config 是键白名单容器，只覆盖密文键，保留其余鉴权材料。
		ac := make(map[string]string, len(p.AuthConfig)+1)
		for k, v := range p.AuthConfig {
			ac[k] = v
		}
		ac[apiKeyEncryptedKey] = enc
		p.AuthConfig = ac
		now := time.Now()
		p.APIKeyRotatedAt = &now
	}
	if err := s.store.UpdateProvider(ctx, p); err != nil {
		if isUniqueViolation(err) {
			return nil, providerapi.ErrProviderNameConflict
		}
		return nil, fmt.Errorf("update provider %d: %w", req.ID, err)
	}
	evict(ctx, s.cm, fmt.Sprintf(cacheKeyDetail, req.ID), cacheKeyList)
	return toProviderSchema(p), nil
}

// Delete 删除提供商（DB 级联删 models 与 provider_health）；模型被 agents /
// knowledge_bases 引用时外键挡住（ErrModelInUse，提示先解绑）。
func (s *providerService) Delete(ctx context.Context, req providerapi.DeleteProviderReq) error {
	if err := req.Validate(); err != nil {
		return fmt.Errorf("validate delete provider: %w", err)
	}
	if err := s.store.DeleteProvider(ctx, req.ID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return providerapi.ErrProviderNotFound
		}
		if isFKViolation(err) {
			return providerapi.ErrModelInUse
		}
		return fmt.Errorf("delete provider %d: %w", req.ID, err)
	}
	evict(ctx, s.cm, fmt.Sprintf(cacheKeyDetail, req.ID), cacheKeyList)
	return nil
}

// TestConnection 手动连通性探测：校验 + 取实体后委托 probeOne（与定时探测共用同一探测写库逻辑）。
func (s *providerService) TestConnection(ctx context.Context, req providerapi.TestConnectionReq) (*providerapi.ConnectionTestSchema, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate test connection: %w", err)
	}
	p, err := s.store.GetProviderByID(ctx, req.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, providerapi.ErrProviderNotFound
		}
		return nil, fmt.Errorf("get provider %d: %w", req.ID, err)
	}
	return s.probeOne(ctx, p)
}

// ---- ModelService ----

// Create 手动录入模型（source=manual）：先验 provider 存在，model_id 同提供商下唯一
// （竞态由 uq_models_provider_model_id 兜底）。详情聚合含 models，需失效对应 detail。
func (s *modelService) Create(ctx context.Context, req providerapi.CreateModelReq) (*providerapi.ModelSchema, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate create model: %w", err)
	}
	if _, err := s.store.GetProviderByID(ctx, req.ProviderID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, providerapi.ErrProviderNotFound
		}
		return nil, fmt.Errorf("get provider %d: %w", req.ProviderID, err)
	}
	if _, err := s.store.GetModelByProviderAndModelID(ctx, req.ProviderID, req.ModelID); err == nil {
		return nil, providerapi.ErrModelIDConflict
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("check model %s of provider %d: %w", req.ModelID, req.ProviderID, err)
	}

	m := &Model{
		ProviderID:      req.ProviderID,
		Name:            req.Name,
		ModelID:         req.ModelID,
		Capability:      req.Capability,
		ContextWindow:   req.ContextWindow,
		MaxOutputTokens: req.MaxOutputTokens,
		InputPrice:      req.InputPrice,
		OutputPrice:     req.OutputPrice,
		EmbeddingDim:    req.EmbeddingDim,
		Enabled:         req.Enabled == nil || *req.Enabled, // 省略 = 默认可用
		Source:          providerapi.SourceManual,
		ExtraParams:     nonNilAny(req.ExtraParams),
	}
	if err := s.store.CreateModel(ctx, m); err != nil {
		if isUniqueViolation(err) {
			return nil, providerapi.ErrModelIDConflict
		}
		return nil, fmt.Errorf("create model: %w", err)
	}
	evict(ctx, s.cm, fmt.Sprintf(cacheKeyDetail, req.ProviderID))
	return toModelSchema(m), nil
}

// Get 取单个模型；不存在返回 ErrModelNotFound。
func (s *modelService) Get(ctx context.Context, req providerapi.GetModelReq) (*providerapi.ModelSchema, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate get model: %w", err)
	}
	m, err := s.store.GetModelByID(ctx, req.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, providerapi.ErrModelNotFound
		}
		return nil, fmt.Errorf("get model %d: %w", req.ID, err)
	}
	return toModelSchema(m), nil
}

// List 某提供商下模型列表（偏移分页）；provider 不存在返回 ErrProviderNotFound。
func (s *modelService) List(ctx context.Context, req providerapi.ListModelsReq) (*providerapi.ModelListResult, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate list models: %w", err)
	}
	if _, err := s.store.GetProviderByID(ctx, req.ProviderID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, providerapi.ErrProviderNotFound
		}
		return nil, fmt.Errorf("get provider %d: %w", req.ProviderID, err)
	}
	res, err := s.store.ListModels(ctx, req.ProviderID, page.NewOffset(req.Page, req.PageSize))
	if err != nil {
		return nil, fmt.Errorf("list models of provider %d: %w", req.ProviderID, err)
	}
	items := make([]providerapi.ModelSchema, 0, len(res.Items))
	for i := range res.Items {
		items = append(items, *toModelSchema(&res.Items[i]))
	}
	return &providerapi.ModelListResult{Items: items, Page: res.Page, PageSize: res.PageSize, Total: res.Total}, nil
}

// Update 整体更新；支持换绑 provider（目标 provider 须存在，目标下 model_id 唯一）。
// 换绑时新旧 provider 的 detail 缓存都要失效（详情聚合含 models）。
func (s *modelService) Update(ctx context.Context, req providerapi.UpdateModelReq) (*providerapi.ModelSchema, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate update model: %w", err)
	}
	m, err := s.store.GetModelByID(ctx, req.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, providerapi.ErrModelNotFound
		}
		return nil, fmt.Errorf("get model %d: %w", req.ID, err)
	}
	// 目标 provider 存在性预检：换绑到不存在的 provider 应 404，而非落到 FK 违例的 500。
	if _, err := s.store.GetProviderByID(ctx, req.ProviderID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, providerapi.ErrProviderNotFound
		}
		return nil, fmt.Errorf("get provider %d: %w", req.ProviderID, err)
	}
	if m.ProviderID != req.ProviderID || m.ModelID != req.ModelID {
		if _, err := s.store.GetModelByProviderAndModelID(ctx, req.ProviderID, req.ModelID); err == nil {
			return nil, providerapi.ErrModelIDConflict
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("check model %s of provider %d: %w", req.ModelID, req.ProviderID, err)
		}
	}

	oldProviderID := m.ProviderID
	m.ProviderID = req.ProviderID
	m.Name = req.Name
	m.ModelID = req.ModelID
	m.Capability = req.Capability
	m.ContextWindow = req.ContextWindow
	m.MaxOutputTokens = req.MaxOutputTokens
	m.InputPrice = req.InputPrice
	m.OutputPrice = req.OutputPrice
	m.EmbeddingDim = req.EmbeddingDim
	m.Enabled = req.Enabled
	m.ExtraParams = nonNilAny(req.ExtraParams)
	if err := s.store.UpdateModel(ctx, m); err != nil {
		if isUniqueViolation(err) {
			return nil, providerapi.ErrModelIDConflict
		}
		if isFKViolation(err) {
			return nil, providerapi.ErrProviderNotFound // 目标 provider 在预检后被并发删除
		}
		return nil, fmt.Errorf("update model %d: %w", req.ID, err)
	}
	if oldProviderID != m.ProviderID {
		evict(ctx, s.cm, fmt.Sprintf(cacheKeyDetail, oldProviderID))
	}
	evict(ctx, s.cm, fmt.Sprintf(cacheKeyDetail, m.ProviderID))
	return toModelSchema(m), nil
}

// Delete 删除模型；被 agents / knowledge_bases 引用时外键挡住（ErrModelInUse）。
// 先取实体拿 provider_id 供 detail 缓存失效（顺带 404 哨兵）。
func (s *modelService) Delete(ctx context.Context, req providerapi.DeleteModelReq) error {
	if err := req.Validate(); err != nil {
		return fmt.Errorf("validate delete model: %w", err)
	}
	m, err := s.store.GetModelByID(ctx, req.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return providerapi.ErrModelNotFound
		}
		return fmt.Errorf("get model %d: %w", req.ID, err)
	}
	if err := s.store.DeleteModel(ctx, req.ID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return providerapi.ErrModelNotFound
		}
		if isFKViolation(err) {
			return providerapi.ErrModelInUse
		}
		return fmt.Errorf("delete model %d: %w", req.ID, err)
	}
	evict(ctx, s.cm, fmt.Sprintf(cacheKeyDetail, m.ProviderID))
	return nil
}

// ---- 共用工具 ----

// evict 失效 provider-cache 的 key；失败仅告警不阻断业务——写时删 key 失败时由
// TTL 30min 兜底最终一致（CLAUDE.md《缓存》），不值得让已落库的写操作回滚。
func evict(ctx context.Context, cm cacheManager, keys ...string) {
	for _, k := range keys {
		if err := cm.Delete(ctx, cache.NameProvider, k); err != nil {
			slog.WarnContext(ctx, "evict provider cache failed; ttl fallback", "key", k, "err", err)
		}
	}
}

// isUniqueViolation 判断 PG 唯一约束冲突（23505）。
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgCodeUniqueViolation
}

// isFKViolation 判断 PG 外键违例（23503）。
func isFKViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgCodeFKViolation
}

// clampWindow 把 [start, start+limit) 夹进 [0, n]：越界页返回空窗口而非 panic。
// 先判 start 越界再做加法，保证 start+limit 永不溢出。
func clampWindow(start, limit, n int) (int, int) {
	if start < 0 || start >= n {
		return n, n // 空 slice（filtered[n:n] 非 nil，满足空值约定）
	}
	end := start + limit
	if end > n {
		end = n
	}
	return start, end
}

// nonNilAny nil map 归一为空 map：jsonb 列 NOT NULL，nil 经 serializer 会写成 json null
// 绕过列 DEFAULT，读回时 map 变 nil。
func nonNilAny(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// toProviderSchema model → schema（边界处）；不填 APIKeyMasked——仅详情接口解密后填。
func toProviderSchema(p *Provider) *providerapi.ProviderSchema {
	s := &providerapi.ProviderSchema{
		Name:            p.Name,
		Kind:            p.Kind,
		BaseURL:         p.BaseURL,
		HasAPIKey:       p.AuthConfig[apiKeyEncryptedKey] != "",
		APIKeyRotatedAt: p.APIKeyRotatedAt,
		ExtraConfig:     nonNilAny(p.ExtraConfig),
		Enabled:         p.Enabled,
	}
	s.ID = strconv.FormatUint(p.ID, 10)
	s.CreatedAt = p.CreatedAt
	s.UpdatedAt = p.UpdatedAt
	return s
}

// toModelSchema model → schema（边界处）。
func toModelSchema(m *Model) *providerapi.ModelSchema {
	s := &providerapi.ModelSchema{
		ProviderID:      strconv.FormatUint(m.ProviderID, 10),
		Name:            m.Name,
		ModelID:         m.ModelID,
		Capability:      m.Capability,
		ContextWindow:   m.ContextWindow,
		MaxOutputTokens: m.MaxOutputTokens,
		InputPrice:      m.InputPrice,
		OutputPrice:     m.OutputPrice,
		EmbeddingDim:    m.EmbeddingDim,
		Enabled:         m.Enabled,
		Source:          m.Source,
		ExtraParams:     nonNilAny(m.ExtraParams),
	}
	s.ID = strconv.FormatUint(m.ID, 10)
	s.CreatedAt = m.CreatedAt
	s.UpdatedAt = m.UpdatedAt
	return s
}

// toHealthSchema model → schema；主键 provider_id 字符串化。
func toHealthSchema(h *ProviderHealth) *providerapi.ProviderHealthSchema {
	return &providerapi.ProviderHealthSchema{
		ProviderID:    strconv.FormatUint(h.ProviderID, 10),
		Status:        h.Status,
		LastCheckAt:   h.LastCheckAt,
		LastSuccessAt: h.LastSuccessAt,
		FailCount:     h.FailCount,
		LatencyMs:     h.LatencyMs,
		ErrorMessage:  h.ErrorMessage,
		UpdatedAt:     h.UpdatedAt,
	}
}
