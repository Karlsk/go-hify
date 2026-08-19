package service

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/Karlsk/go-hify/internal/platform/cache"
	"github.com/Karlsk/go-hify/internal/platform/page"
	providerapi "github.com/Karlsk/go-hify/internal/provider/api"
)

// service 层测试：手写内存 Store（含错误注入与调用计数）+ miniredis 跑真实 cache.Cache 路径，
// 覆盖哨兵翻译、加密落库、缓存命中 / 失效矩阵、内存筛选分页。

// memStore 内存 Store 实现：map 模拟三张表，行为对齐 store 包语义
// （未找到 → gorm.ErrRecordNotFound、级联删、id 升序）。
type memStore struct {
	providers map[uint64]*Provider
	models    map[uint64]*Model
	healths   map[uint64]*ProviderHealth
	nextID    uint64

	// 错误注入：模拟 PG 错误类或任意故障（23505 / 23503 / …）。
	createProviderErr error
	updateProviderErr error
	deleteProviderErr error
	createModelErr    error
	updateModelErr    error
	deleteModelErr    error
	upsertHealthErr   error
	listProvidersErr  error

	// 调用计数：缓存命中断言（二访不查 store）。
	getProviderByIDCalls int
	listProvidersCalls   int
	upsertHealthCalls    int

	// mu 保护 healths map 与 upsertHealthCalls：定时探测并发写 health 时防 data race / map 并发写 panic。
	mu sync.Mutex
}

func newMemStore() *memStore {
	return &memStore{
		providers: map[uint64]*Provider{},
		models:    map[uint64]*Model{},
		healths:   map[uint64]*ProviderHealth{},
	}
}

func (m *memStore) CreateProvider(_ context.Context, p *Provider) error {
	if m.createProviderErr != nil {
		return m.createProviderErr
	}
	m.nextID++
	p.ID = m.nextID
	now := time.Now()
	p.CreatedAt, p.UpdatedAt = now, now
	cp := *p
	m.providers[p.ID] = &cp
	return nil
}

func (m *memStore) GetProviderByID(_ context.Context, id uint64) (*Provider, error) {
	m.getProviderByIDCalls++
	p, ok := m.providers[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	cp := *p
	return &cp, nil
}

func (m *memStore) GetProviderByName(_ context.Context, name string) (*Provider, error) {
	for _, p := range m.providers {
		if p.Name == name {
			cp := *p
			return &cp, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *memStore) ListProviders(_ context.Context) ([]Provider, error) {
	if m.listProvidersErr != nil {
		return nil, m.listProvidersErr
	}
	m.listProvidersCalls++
	ids := make([]uint64, 0, len(m.providers))
	for id := range m.providers {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]Provider, 0, len(ids))
	for _, id := range ids {
		out = append(out, *m.providers[id])
	}
	return out, nil
}

func (m *memStore) UpdateProvider(_ context.Context, p *Provider) error {
	if m.updateProviderErr != nil {
		return m.updateProviderErr
	}
	if _, ok := m.providers[p.ID]; !ok {
		return gorm.ErrRecordNotFound
	}
	p.UpdatedAt = time.Now()
	cp := *p
	m.providers[p.ID] = &cp
	return nil
}

func (m *memStore) DeleteProvider(_ context.Context, id uint64) error {
	if m.deleteProviderErr != nil {
		return m.deleteProviderErr
	}
	if _, ok := m.providers[id]; !ok {
		return gorm.ErrRecordNotFound
	}
	delete(m.providers, id)
	// 级联删（对齐 DB ON DELETE CASCADE）
	for mid, mo := range m.models {
		if mo.ProviderID == id {
			delete(m.models, mid)
		}
	}
	delete(m.healths, id)
	return nil
}

func (m *memStore) CreateModel(_ context.Context, mo *Model) error {
	if m.createModelErr != nil {
		return m.createModelErr
	}
	m.nextID++
	mo.ID = m.nextID
	now := time.Now()
	mo.CreatedAt, mo.UpdatedAt = now, now
	cp := *mo
	m.models[mo.ID] = &cp
	return nil
}

func (m *memStore) GetModelByID(_ context.Context, id uint64) (*Model, error) {
	mo, ok := m.models[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	cp := *mo
	return &cp, nil
}

func (m *memStore) GetModelByProviderAndModelID(_ context.Context, providerID uint64, modelID string) (*Model, error) {
	for _, mo := range m.models {
		if mo.ProviderID == providerID && mo.ModelID == modelID {
			cp := *mo
			return &cp, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *memStore) modelsByProviderSorted(providerID uint64) []Model {
	ids := make([]uint64, 0, len(m.models))
	for id, mo := range m.models {
		if mo.ProviderID == providerID {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]Model, 0, len(ids))
	for _, id := range ids {
		out = append(out, *m.models[id])
	}
	return out
}

func (m *memStore) ListModelsByProvider(_ context.Context, providerID uint64) ([]Model, error) {
	return m.modelsByProviderSorted(providerID), nil
}

func (m *memStore) ListModels(_ context.Context, providerID uint64, p page.OffsetParams) (page.OffsetResult[Model], error) {
	all := m.modelsByProviderSorted(providerID)
	start, end := clampWindow(p.Offset(), p.Limit(), len(all))
	return page.NewOffsetResult(all[start:end], p, int64(len(all))), nil
}

func (m *memStore) UpdateModel(_ context.Context, mo *Model) error {
	if m.updateModelErr != nil {
		return m.updateModelErr
	}
	if _, ok := m.models[mo.ID]; !ok {
		return gorm.ErrRecordNotFound
	}
	mo.UpdatedAt = time.Now()
	cp := *mo
	m.models[mo.ID] = &cp
	return nil
}

func (m *memStore) DeleteModel(_ context.Context, id uint64) error {
	if m.deleteModelErr != nil {
		return m.deleteModelErr
	}
	if _, ok := m.models[id]; !ok {
		return gorm.ErrRecordNotFound
	}
	delete(m.models, id)
	return nil
}

func (m *memStore) GetHealthByProviderID(_ context.Context, providerID uint64) (*ProviderHealth, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.healths[providerID]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	cp := *h
	return &cp, nil
}

func (m *memStore) GetHealthByProviderIDForUpdate(ctx context.Context, providerID uint64) (*ProviderHealth, error) {
	// 定时探测并发写 healths（map 由 mu 保护）；无行级锁语义，锁定读退化为普通读（并发语义由 store 层 sqlmock 覆盖）
	return m.GetHealthByProviderID(ctx, providerID)
}

func (m *memStore) WithTx(_ context.Context, fn func(tx Store) error) error {
	return fn(m) // 内存 store 无真实事务，直接同 store 回调（并发语义由 store 层 sqlmock 覆盖）
}

func (m *memStore) UpsertHealth(_ context.Context, h *ProviderHealth) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.upsertHealthErr != nil {
		return m.upsertHealthErr
	}
	m.upsertHealthCalls++
	if old, ok := m.healths[h.ProviderID]; ok {
		h.CreatedAt = old.CreatedAt // 更新路径保留原 created_at（对齐 DO UPDATE 不碰该列）
	}
	m.healths[h.ProviderID] = h
	return nil
}

// ---- 种子与构造 helper ----

// seed 种子一条 provider（分配 id / 时间戳 / 归一 map）；与其他方法一致存副本，
// 避免调用方残留指针与库内状态串改。
func (m *memStore) seed(p *Provider) *Provider {
	m.nextID++
	p.ID = m.nextID
	now := time.Now()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	if p.AuthConfig == nil {
		p.AuthConfig = map[string]string{}
	}
	if p.ExtraConfig == nil {
		p.ExtraConfig = map[string]any{}
	}
	cp := *p
	m.providers[p.ID] = &cp
	return &cp
}

// seedModel 种子一条 model。
func (m *memStore) seedModel(mo *Model) *Model {
	m.nextID++
	mo.ID = m.nextID
	now := time.Now()
	if mo.CreatedAt.IsZero() {
		mo.CreatedAt = now
	}
	mo.UpdatedAt = now
	if mo.ExtraParams == nil {
		mo.ExtraParams = map[string]any{}
	}
	if mo.Source == "" {
		mo.Source = providerapi.SourceManual
	}
	m.models[mo.ID] = mo
	return mo
}

// seedHealth 种子一条 health。
func (m *memStore) seedHealth(h *ProviderHealth) *ProviderHealth {
	now := time.Now()
	if h.CreatedAt.IsZero() {
		h.CreatedAt = now
	}
	h.UpdatedAt = now
	m.healths[h.ProviderID] = h
	return h
}

// newTestEnv 建 miniredis + 真实 cache.Cache + 内存 Store（服务构造由调用方自选主密钥）。
func newTestEnv(t *testing.T) (*memStore, *miniredis.Miniredis, cacheManager) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	cm := cache.New(rdb, cache.DefaultConfig())
	return newMemStore(), mr, cm
}

// newTestService 建 st + 标准主密钥的两服务。
func newTestService(t *testing.T) (*memStore, *miniredis.Miniredis, providerapi.ProviderService, providerapi.ModelService) {
	t.Helper()
	st, mr, cm := newTestEnv(t)
	return st, mr, NewProviderService(st, cm, testMaster), NewModelService(st, cm)
}

// 缓存原始 key（与 cache 包的拼接约定一致，用于失效断言）。
const (
	listFullKey      = "hify:cache:provider-cache:list"
	detailFullKeyFmt = "hify:cache:provider-cache:detail:%d"
)

// pgErr 构造 PG 错误（错误码注入）。
func pgErr(code string) error { return &pgconn.PgError{Code: code} }

// seedProviderWithKey 建带加密 key 的 openai provider，返回 id（rotated_at 置为 1 小时前，供轮换断言比较）。
func seedProviderWithKey(st *memStore) uint64 {
	enc, _ := encryptAPIKey(testMaster, "sk-old-key-123456")
	oldRotated := time.Now().Add(-time.Hour)
	return st.seed(&Provider{
		Name:            "OpenAI",
		Kind:            providerapi.KindOpenAI,
		AuthConfig:      map[string]string{apiKeyEncryptedKey: enc},
		APIKeyRotatedAt: &oldRotated,
		Enabled:         true,
	}).ID
}
