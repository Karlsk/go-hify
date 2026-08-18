package service

import (
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	providerapi "github.com/Karlsk/go-hify/internal/provider/api"
)

// ProviderService 用例：CRUD、哨兵翻译、加密落库、缓存命中 / 失效矩阵、内存筛选分页。

func TestProviderCreate_OK(t *testing.T) {
	st, mr, ps, _ := newTestService(t)
	ctx := context.Background()

	s, err := ps.Create(ctx, providerapi.CreateProviderReq{
		Name: "OpenAI", Kind: providerapi.KindOpenAI, APIKey: "sk-plaintext-key-9876",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, s.ID, "id 应已字符串化回填")
	assert.True(t, s.HasAPIKey)
	assert.True(t, s.Enabled, "创建固定 enabled=true")

	// 落库的是密文：不等于明文、能解回明文、轮换时间已记。
	p := st.providers[1]
	require.Contains(t, p.AuthConfig, apiKeyEncryptedKey)
	assert.NotEqual(t, "sk-plaintext-key-9876", p.AuthConfig[apiKeyEncryptedKey])
	pt, err := decryptAPIKey(testMaster, p.AuthConfig[apiKeyEncryptedKey])
	require.NoError(t, err)
	assert.Equal(t, "sk-plaintext-key-9876", pt)
	require.NotNil(t, p.APIKeyRotatedAt)

	// 创建后列表快照应被失效（本例创建前无缓存，断言不残留）
	assert.False(t, mr.Exists(listFullKey))
}

func TestProviderCreate_NoKeyOllama(t *testing.T) {
	st, _, ps, _ := newTestService(t)
	ctx := context.Background()

	s, err := ps.Create(ctx, providerapi.CreateProviderReq{Name: "本机", Kind: providerapi.KindOllama})
	require.NoError(t, err)
	assert.False(t, s.HasAPIKey)

	p := st.providers[1]
	assert.Empty(t, p.AuthConfig, "ollama 无 key：auth_config 为空 map")
	assert.Nil(t, p.APIKeyRotatedAt)
}

func TestProviderCreate_NameConflict(t *testing.T) {
	_, _, ps, _ := newTestService(t)
	ctx := context.Background()
	_, err := ps.Create(ctx, providerapi.CreateProviderReq{Name: "OpenAI", Kind: providerapi.KindOpenAI, APIKey: "sk-x"})
	require.NoError(t, err)

	_, err = ps.Create(ctx, providerapi.CreateProviderReq{Name: "OpenAI", Kind: providerapi.KindOpenAI, APIKey: "sk-y"})
	assert.ErrorIs(t, err, providerapi.ErrProviderNameConflict)
}

// 先查放行后插入撞唯一索引（并发竞态）：23505 兜底翻译。
func TestProviderCreate_UniqueViolationRace(t *testing.T) {
	st, _, ps, _ := newTestService(t)
	st.createProviderErr = pgErr("23505")

	_, err := ps.Create(context.Background(), providerapi.CreateProviderReq{Name: "x", Kind: providerapi.KindOpenAI, APIKey: "sk-x"})
	assert.ErrorIs(t, err, providerapi.ErrProviderNameConflict)
}

func TestProviderCreate_Validation(t *testing.T) {
	st, _, ps, _ := newTestService(t)
	// kind 需 key 但缺 key（service 入口重验，防跨模块调用绕过 handler）
	_, err := ps.Create(context.Background(), providerapi.CreateProviderReq{Name: "x", Kind: providerapi.KindClaude})
	assert.Error(t, err)
	// store 未被触碰
	assert.Empty(t, st.providers)
}

func TestProviderGet_DetailAssembles(t *testing.T) {
	st, _, ps, _ := newTestService(t)
	ctx := context.Background()
	id := seedProviderWithKey(st)
	st.seedModel(&Model{ProviderID: id, Name: "GPT-4o", ModelID: "gpt-4o", Capability: providerapi.CapabilityChat})
	st.seedModel(&Model{ProviderID: id, Name: "text-embedding-3", ModelID: "text-embedding-3-small", Capability: providerapi.CapabilityEmbedding})
	st.seedHealth(&ProviderHealth{ProviderID: id, Status: providerapi.HealthUp})

	d, err := ps.Get(ctx, providerapi.GetProviderReq{ID: id})
	require.NoError(t, err)
	assert.Equal(t, "OpenAI", d.Name)
	require.Len(t, d.Models, 2)
	assert.Equal(t, "gpt-4o", d.Models[0].ModelID)
	require.NotNil(t, d.Health)
	assert.Equal(t, providerapi.HealthUp, d.Health.Status)
	assert.Equal(t, "sk-o…3456", d.APIKeyMasked, "明文 sk-old-key-123456 的打码")
	assert.Equal(t, "1", d.ID)
}

func TestProviderGet_NoHealthNoModels(t *testing.T) {
	st, _, ps, _ := newTestService(t)
	id := seedProviderWithKey(st)

	d, err := ps.Get(context.Background(), providerapi.GetProviderReq{ID: id})
	require.NoError(t, err)
	assert.Empty(t, d.Models, "无模型时为空 slice")
	assert.Nil(t, d.Health, "从未探测：health 为 nil")
}

func TestProviderGet_CachedSecondCall(t *testing.T) {
	st, mr, ps, _ := newTestService(t)
	ctx := context.Background()
	id := seedProviderWithKey(st)

	_, err := ps.Get(ctx, providerapi.GetProviderReq{ID: id})
	require.NoError(t, err)
	calls := st.getProviderByIDCalls
	require.True(t, mr.Exists(fmt.Sprintf(detailFullKeyFmt, id)), "miss 后应回填缓存")

	d2, err := ps.Get(ctx, providerapi.GetProviderReq{ID: id})
	require.NoError(t, err)
	assert.Equal(t, calls, st.getProviderByIDCalls, "二访应命中缓存，不再查 store")
	assert.Equal(t, "OpenAI", d2.Name)
}

func TestProviderGet_NotFound(t *testing.T) {
	_, _, ps, _ := newTestService(t)
	_, err := ps.Get(context.Background(), providerapi.GetProviderReq{ID: 999})
	assert.ErrorIs(t, err, providerapi.ErrProviderNotFound)
}

// 主密钥不匹配（如换密钥后旧数据）：打码降级跳过，读取不失败。
func TestProviderGet_DecryptFailsSkipMask(t *testing.T) {
	st, _, cm := newTestEnv(t)
	id := seedProviderWithKey(st)
	wrong := NewProviderService(st, cm, []byte("99999999999999999999999999999999"))

	d, err := wrong.Get(context.Background(), providerapi.GetProviderReq{ID: id})
	require.NoError(t, err)
	assert.True(t, d.HasAPIKey)
	assert.Empty(t, d.APIKeyMasked, "解密失败不打码")
}

func TestProviderList_FiltersAndPaginates(t *testing.T) {
	st, _, ps, _ := newTestService(t)
	ctx := context.Background()
	st.seed(&Provider{Name: "OpenAI", Kind: providerapi.KindOpenAI, Enabled: true})
	st.seed(&Provider{Name: "Claude", Kind: providerapi.KindClaude, Enabled: false})
	st.seed(&Provider{Name: "vLLM", Kind: providerapi.KindOpenAICompatible, BaseURL: "http://v:8000/v1", Enabled: false})

	// kind 筛选
	res, err := ps.List(ctx, providerapi.ListProvidersReq{Kind: providerapi.KindOpenAI})
	require.NoError(t, err)
	assert.EqualValues(t, 1, res.Total)
	assert.Equal(t, "OpenAI", res.Items[0].Name)

	// enabled=false 筛选（指针区分未传）
	disabled := false
	res, err = ps.List(ctx, providerapi.ListProvidersReq{Enabled: &disabled})
	require.NoError(t, err)
	assert.EqualValues(t, 2, res.Total)

	// 无筛选全量 + 分页窗口（第 2 页、页大小 2 → 只剩 1 条）
	res, err = ps.List(ctx, providerapi.ListProvidersReq{Page: 2, PageSize: 2})
	require.NoError(t, err)
	assert.EqualValues(t, 3, res.Total)
	assert.Len(t, res.Items, 1)
	assert.Equal(t, 2, res.Page)
	assert.Equal(t, 2, res.PageSize)

	// 越界页：空 slice 非 nil
	res, err = ps.List(ctx, providerapi.ListProvidersReq{Page: 9, PageSize: 2})
	require.NoError(t, err)
	assert.NotNil(t, res.Items)
	assert.Empty(t, res.Items)
}

func TestProviderList_CachedSecondCall(t *testing.T) {
	st, mr, ps, _ := newTestService(t)
	ctx := context.Background()
	seedProviderWithKey(st)

	_, err := ps.List(ctx, providerapi.ListProvidersReq{})
	require.NoError(t, err)
	calls := st.listProvidersCalls
	require.True(t, mr.Exists(listFullKey))

	_, err = ps.List(ctx, providerapi.ListProvidersReq{})
	require.NoError(t, err)
	assert.Equal(t, calls, st.listProvidersCalls, "二访命中整表快照，不再查 store")
}

func TestProviderList_KindInvalid(t *testing.T) {
	_, _, ps, _ := newTestService(t)
	_, err := ps.List(context.Background(), providerapi.ListProvidersReq{Kind: "azure"})
	assert.Error(t, err)
}

// 超大 page 的 (page-1)*size 溢出曾致负偏移切片 panic；现应安静返回空页。
func TestProviderList_HugePageNoPanic(t *testing.T) {
	st, _, ps, _ := newTestService(t)
	st.seed(&Provider{Name: "OpenAI", Kind: providerapi.KindOpenAI, Enabled: true})

	res, err := ps.List(context.Background(), providerapi.ListProvidersReq{Page: math.MaxInt, PageSize: 20})
	require.NoError(t, err)
	assert.NotNil(t, res.Items)
	assert.Empty(t, res.Items)
	assert.EqualValues(t, 1, res.Total)
}

// 构造器守卫：非法长度主密钥启动期即 panic（与 config 层双重保险）。
func TestNewProviderService_BadMasterKeyPanics(t *testing.T) {
	st, _, cm := newTestEnv(t)
	assert.Panics(t, func() {
		NewProviderService(st, cm, []byte("too-short"))
	})
}

func TestProviderUpdate_RotatesKeyAndEvicts(t *testing.T) {
	st, mr, ps, _ := newTestService(t)
	ctx := context.Background()
	id := seedProviderWithKey(st)

	// 预热两类缓存
	_, err := ps.Get(ctx, providerapi.GetProviderReq{ID: id})
	require.NoError(t, err)
	_, err = ps.List(ctx, providerapi.ListProvidersReq{})
	require.NoError(t, err)
	require.True(t, mr.Exists(fmt.Sprintf(detailFullKeyFmt, id)))
	require.True(t, mr.Exists(listFullKey))

	oldRotated := st.providers[id].APIKeyRotatedAt
	s, err := ps.Update(ctx, providerapi.UpdateProviderReq{ID: id, Name: "OpenAI", Enabled: true, APIKey: "sk-new-key-654321"})
	require.NoError(t, err)
	assert.True(t, s.HasAPIKey)

	// 轮换：密文可解回新明文；rotated_at 刷新
	pt, err := decryptAPIKey(testMaster, st.providers[id].AuthConfig[apiKeyEncryptedKey])
	require.NoError(t, err)
	assert.Equal(t, "sk-new-key-654321", pt)
	assert.True(t, st.providers[id].APIKeyRotatedAt.After(*oldRotated))

	// 两类缓存都被失效
	assert.False(t, mr.Exists(fmt.Sprintf(detailFullKeyFmt, id)))
	assert.False(t, mr.Exists(listFullKey))
}

func TestProviderUpdate_EmptyKeyKeepsOld(t *testing.T) {
	st, _, ps, _ := newTestService(t)
	ctx := context.Background()
	id := seedProviderWithKey(st)
	before := st.providers[id].AuthConfig[apiKeyEncryptedKey]

	_, err := ps.Update(ctx, providerapi.UpdateProviderReq{ID: id, Name: "OpenAI", Enabled: true})
	require.NoError(t, err)
	assert.Equal(t, before, st.providers[id].AuthConfig[apiKeyEncryptedKey], "api_key 空串 = 不变")
}

func TestProviderUpdate_NotFoundAndConflict(t *testing.T) {
	st, _, ps, _ := newTestService(t)
	ctx := context.Background()
	id := seedProviderWithKey(st)
	st.seed(&Provider{Name: "Claude", Kind: providerapi.KindClaude})

	_, err := ps.Update(ctx, providerapi.UpdateProviderReq{ID: 999, Name: "x"})
	assert.ErrorIs(t, err, providerapi.ErrProviderNotFound)

	// 改名撞已有名
	_, err = ps.Update(ctx, providerapi.UpdateProviderReq{ID: id, Name: "Claude"})
	assert.ErrorIs(t, err, providerapi.ErrProviderNameConflict)

	// 同名自更新放行
	_, err = ps.Update(ctx, providerapi.UpdateProviderReq{ID: id, Name: "OpenAI"})
	assert.NoError(t, err)
}

// 更新时用库内真实 kind 补验：compatible 清空 base_url 应被拒。
func TestProviderUpdate_ValidateWithRealKind(t *testing.T) {
	st, _, ps, _ := newTestService(t)
	ctx := context.Background()
	id := st.seed(&Provider{Name: "vLLM", Kind: providerapi.KindOpenAICompatible, BaseURL: "http://v:8000/v1"}).ID

	_, err := ps.Update(ctx, providerapi.UpdateProviderReq{ID: id, Name: "vLLM"})
	assert.Error(t, err, "openai_compatible 更新缺 base_url 应报错")
}

func TestProviderUpdate_UniqueViolationRace(t *testing.T) {
	st, _, ps, _ := newTestService(t)
	ctx := context.Background()
	id := seedProviderWithKey(st)
	st.updateProviderErr = pgErr("23505")

	_, err := ps.Update(ctx, providerapi.UpdateProviderReq{ID: id, Name: "新名字"})
	assert.ErrorIs(t, err, providerapi.ErrProviderNameConflict)
}

func TestProviderDelete_OKEvictsBoth(t *testing.T) {
	st, mr, ps, _ := newTestService(t)
	ctx := context.Background()
	id := seedProviderWithKey(st)

	_, err := ps.Get(ctx, providerapi.GetProviderReq{ID: id})
	require.NoError(t, err)
	_, err = ps.List(ctx, providerapi.ListProvidersReq{})
	require.NoError(t, err)

	require.NoError(t, ps.Delete(ctx, providerapi.DeleteProviderReq{ID: id}))
	assert.NotContains(t, st.providers, id)
	assert.False(t, mr.Exists(fmt.Sprintf(detailFullKeyFmt, id)))
	assert.False(t, mr.Exists(listFullKey))
}

func TestProviderDelete_NotFound(t *testing.T) {
	_, _, ps, _ := newTestService(t)
	err := ps.Delete(context.Background(), providerapi.DeleteProviderReq{ID: 999})
	assert.ErrorIs(t, err, providerapi.ErrProviderNotFound)
}

// 模型被 agents / knowledge_bases 引用：外键 RESTRICT 挡住删除。
func TestProviderDelete_FKViolation(t *testing.T) {
	st, _, ps, _ := newTestService(t)
	id := seedProviderWithKey(st)
	st.deleteProviderErr = pgErr("23503")

	err := ps.Delete(context.Background(), providerapi.DeleteProviderReq{ID: id})
	assert.ErrorIs(t, err, providerapi.ErrModelInUse)
}

func TestProviderTestConnection_NotImplemented(t *testing.T) {
	_, _, ps, _ := newTestService(t)
	_, err := ps.TestConnection(context.Background(), providerapi.TestConnectionReq{ID: 1})
	assert.ErrorIs(t, err, errNotImplemented)
}
