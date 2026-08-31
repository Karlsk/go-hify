package service

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	providerapi "github.com/Karlsk/go-hify/internal/provider/api"
)

// ModelService 用例：CRUD、来源固定 manual、详情缓存失效（含换绑双失效）、哨兵翻译。

// seedTwoProviders 建 openai（id=1）与 claude（id=2）两个 provider，返回两个 id。
func seedTwoProviders(st *memStore) (openai uint64, claude uint64) {
	openai = st.seed(&Provider{Name: "OpenAI", Kind: providerapi.KindOpenAI, Enabled: true}).ID
	claude = st.seed(&Provider{Name: "Claude", Kind: providerapi.KindClaude, Enabled: true}).ID
	return openai, claude
}

func TestModelCreate_OK(t *testing.T) {
	st, mr, ps, ms := newTestService(t)
	ctx := context.Background()
	pid, _ := seedTwoProviders(st)

	// 预热详情缓存：创建模型后应失效
	st.seedModel(&Model{ProviderID: pid, Name: "GPT-4o", ModelID: "gpt-4o", Capability: providerapi.CapabilityChat})
	require.True(t, warmDetail(t, ctx, ps, mr, pid))

	s, err := ms.Create(ctx, providerapi.CreateModelReq{
		ProviderID: pid, Name: "嵌入", ModelID: "text-embedding-3-small",
		Capability: providerapi.CapabilityEmbedding, EmbeddingDim: int32Ptr(1536),
	})
	require.NoError(t, err)
	assert.Equal(t, providerapi.SourceManual, s.Source, "手建来源固定 manual")
	assert.True(t, s.Enabled, "enabled 缺省 true")
	assert.Equal(t, "1", s.ProviderID)

	// 手建模型的 extra_params 归一为空 map（jsonb NOT NULL）
	var mo *Model
	for _, m := range st.models {
		if m.ModelID == "text-embedding-3-small" {
			mo = m
		}
	}
	require.NotNil(t, mo, "新模型应已落库")
	assert.NotNil(t, mo.ExtraParams)
	assert.Empty(t, mo.ExtraParams)

	assert.False(t, mr.Exists(fmt.Sprintf(detailFullKeyFmt, pid)), "模型写入应失效所属 provider 详情缓存")
}

func TestModelCreate_EnabledFalse(t *testing.T) {
	st, _, _, ms := newTestService(t)
	pid, _ := seedTwoProviders(st)
	off := false

	s, err := ms.Create(context.Background(), providerapi.CreateModelReq{
		ProviderID: pid, Name: "x", ModelID: "m-x", Capability: providerapi.CapabilityChat, Enabled: &off,
	})
	require.NoError(t, err)
	assert.False(t, s.Enabled)
}

func TestModelCreate_ProviderMissing(t *testing.T) {
	_, _, _, ms := newTestService(t)
	_, err := ms.Create(context.Background(), providerapi.CreateModelReq{
		ProviderID: 999, Name: "x", ModelID: "m-x", Capability: providerapi.CapabilityChat,
	})
	assert.ErrorIs(t, err, providerapi.ErrProviderNotFound)
}

func TestModelCreate_IDConflict(t *testing.T) {
	st, _, _, ms := newTestService(t)
	ctx := context.Background()
	pid, claudeID := seedTwoProviders(st)
	st.seedModel(&Model{ProviderID: pid, Name: "GPT-4o", ModelID: "gpt-4o", Capability: providerapi.CapabilityChat})

	// 同 provider 下 model_id 重复（预检查径）
	_, err := ms.Create(ctx, providerapi.CreateModelReq{
		ProviderID: pid, Name: "again", ModelID: "gpt-4o", Capability: providerapi.CapabilityChat,
	})
	assert.ErrorIs(t, err, providerapi.ErrModelIDConflict)

	// 同 model_id 挂到另一 provider 放行（唯一约束是 uq(provider_id, model_id)）
	s, err := ms.Create(ctx, providerapi.CreateModelReq{
		ProviderID: claudeID, Name: "claude 同名", ModelID: "gpt-4o", Capability: providerapi.CapabilityChat,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, s.ID)
}

// 先查放行后插入撞唯一索引（并发竞态）：23505 兜底翻译。
func TestModelCreate_UniqueViolationRace(t *testing.T) {
	st, _, _, ms := newTestService(t)
	pid, _ := seedTwoProviders(st)
	st.createModelErr = pgErr("23505")

	_, err := ms.Create(context.Background(), providerapi.CreateModelReq{
		ProviderID: pid, Name: "x", ModelID: "m-x", Capability: providerapi.CapabilityChat,
	})
	assert.ErrorIs(t, err, providerapi.ErrModelIDConflict)
}

func TestModelGet_OKAndNotFound(t *testing.T) {
	st, _, _, ms := newTestService(t)
	pid, _ := seedTwoProviders(st)
	id := st.seedModel(&Model{ProviderID: pid, Name: "GPT-4o", ModelID: "gpt-4o", Capability: providerapi.CapabilityChat}).ID

	s, err := ms.Get(context.Background(), providerapi.GetModelReq{ID: id})
	require.NoError(t, err)
	assert.Equal(t, "gpt-4o", s.ModelID)
	assert.Equal(t, providerapi.SourceManual, s.Source)

	_, err = ms.Get(context.Background(), providerapi.GetModelReq{ID: 999})
	assert.ErrorIs(t, err, providerapi.ErrModelNotFound)
}

func TestModelList_Paginates(t *testing.T) {
	st, _, _, ms := newTestService(t)
	ctx := context.Background()
	pid, _ := seedTwoProviders(st)
	for i := 1; i <= 3; i++ {
		st.seedModel(&Model{ProviderID: pid, Name: fmt.Sprintf("m%d", i), ModelID: fmt.Sprintf("m-%d", i), Capability: providerapi.CapabilityChat})
	}

	res, err := ms.List(ctx, providerapi.ListModelsReq{ProviderID: pid, Page: 2, PageSize: 2})
	require.NoError(t, err)
	assert.EqualValues(t, 3, res.Total)
	require.Len(t, res.Items, 1)
	assert.Equal(t, "m-3", res.Items[0].ModelID, "id 升序窗口")
	assert.Equal(t, 2, res.Page)

	// provider 不存在 → 404 哨兵
	_, err = ms.List(ctx, providerapi.ListModelsReq{ProviderID: 999})
	assert.ErrorIs(t, err, providerapi.ErrProviderNotFound)
}

func TestModelUpdate_RenameOK(t *testing.T) {
	st, _, _, ms := newTestService(t)
	ctx := context.Background()
	pid, _ := seedTwoProviders(st)
	id := st.seedModel(&Model{ProviderID: pid, Name: "GPT-4o", ModelID: "gpt-4o", Capability: providerapi.CapabilityChat}).ID

	s, err := ms.Update(ctx, providerapi.UpdateModelReq{
		ID: id, ProviderID: pid, Name: "GPT-4o mini", ModelID: "gpt-4o", Capability: providerapi.CapabilityChat, Enabled: true,
	})
	require.NoError(t, err)
	assert.Equal(t, "GPT-4o mini", s.Name)
	assert.Equal(t, "GPT-4o mini", st.models[id].Name, "更新走全量 Save")
}

// 换绑 provider：旧 provider 与新 provider 的详情缓存都要失效。
func TestModelUpdate_RebindEvictsBothDetails(t *testing.T) {
	st, mr, ps, ms := newTestService(t)
	ctx := context.Background()
	pid1, pid2 := seedTwoProviders(st)
	id := st.seedModel(&Model{ProviderID: pid1, Name: "GPT-4o", ModelID: "gpt-4o", Capability: providerapi.CapabilityChat}).ID

	require.True(t, warmDetail(t, ctx, ps, mr, pid1))
	require.True(t, warmDetail(t, ctx, ps, mr, pid2))

	_, err := ms.Update(ctx, providerapi.UpdateModelReq{
		ID: id, ProviderID: pid2, Name: "GPT-4o", ModelID: "gpt-4o", Capability: providerapi.CapabilityChat, Enabled: true,
	})
	require.NoError(t, err)
	assert.Equal(t, pid2, st.models[id].ProviderID)
	assert.False(t, mr.Exists(fmt.Sprintf(detailFullKeyFmt, pid1)), "旧 provider 详情失效")
	assert.False(t, mr.Exists(fmt.Sprintf(detailFullKeyFmt, pid2)), "新 provider 详情失效")
}

func TestModelUpdate_ConflictAndNotFound(t *testing.T) {
	st, _, _, ms := newTestService(t)
	ctx := context.Background()
	pid, _ := seedTwoProviders(st)
	st.seedModel(&Model{ProviderID: pid, Name: "A", ModelID: "m-a", Capability: providerapi.CapabilityChat})
	idB := st.seedModel(&Model{ProviderID: pid, Name: "B", ModelID: "m-b", Capability: providerapi.CapabilityChat}).ID

	// b 改成与 a 相同的 model_id → 冲突
	_, err := ms.Update(ctx, providerapi.UpdateModelReq{
		ID: idB, ProviderID: pid, Name: "B", ModelID: "m-a", Capability: providerapi.CapabilityChat, Enabled: true,
	})
	assert.ErrorIs(t, err, providerapi.ErrModelIDConflict)

	_, err = ms.Update(ctx, providerapi.UpdateModelReq{
		ID: 999, ProviderID: pid, Name: "x", ModelID: "m-x", Capability: providerapi.CapabilityChat, Enabled: true,
	})
	assert.ErrorIs(t, err, providerapi.ErrModelNotFound)
}

// 换绑到不存在的 provider：应 404（预检），而非 FK 违例落成 500。
func TestModelUpdate_RebindToMissingProvider(t *testing.T) {
	st, _, _, ms := newTestService(t)
	ctx := context.Background()
	pid, _ := seedTwoProviders(st)
	id := st.seedModel(&Model{ProviderID: pid, Name: "A", ModelID: "m-a", Capability: providerapi.CapabilityChat}).ID

	_, err := ms.Update(ctx, providerapi.UpdateModelReq{
		ID: id, ProviderID: 999, Name: "A", ModelID: "m-a", Capability: providerapi.CapabilityChat, Enabled: true,
	})
	assert.ErrorIs(t, err, providerapi.ErrProviderNotFound)

	// 预检后被并发删（23503）同样翻译为 404 而非 500。
	st.updateModelErr = pgErr("23503")
	_, err = ms.Update(ctx, providerapi.UpdateModelReq{
		ID: id, ProviderID: pid, Name: "A", ModelID: "m-a2", Capability: providerapi.CapabilityChat, Enabled: true,
	})
	assert.ErrorIs(t, err, providerapi.ErrProviderNotFound)
}

func TestModelUpdate_UniqueViolationRace(t *testing.T) {
	st, _, _, ms := newTestService(t)
	pid, _ := seedTwoProviders(st)
	id := st.seedModel(&Model{ProviderID: pid, Name: "A", ModelID: "m-a", Capability: providerapi.CapabilityChat}).ID
	st.updateModelErr = pgErr("23505")

	_, err := ms.Update(context.Background(), providerapi.UpdateModelReq{
		ID: id, ProviderID: pid, Name: "A", ModelID: "m-a2", Capability: providerapi.CapabilityChat, Enabled: true,
	})
	assert.ErrorIs(t, err, providerapi.ErrModelIDConflict)
}

func TestModelDelete_OKEvictsDetail(t *testing.T) {
	st, mr, ps, ms := newTestService(t)
	ctx := context.Background()
	pid, _ := seedTwoProviders(st)
	id := st.seedModel(&Model{ProviderID: pid, Name: "A", ModelID: "m-a", Capability: providerapi.CapabilityChat}).ID
	require.True(t, warmDetail(t, ctx, ps, mr, pid))

	require.NoError(t, ms.Delete(ctx, providerapi.DeleteModelReq{ID: id}))
	assert.NotContains(t, st.models, id)
	assert.False(t, mr.Exists(fmt.Sprintf(detailFullKeyFmt, pid)))
}

func TestModelDelete_NotFound(t *testing.T) {
	_, _, _, ms := newTestService(t)
	err := ms.Delete(context.Background(), providerapi.DeleteModelReq{ID: 999})
	assert.ErrorIs(t, err, providerapi.ErrModelNotFound)
}

// 被 agents / knowledge_bases 引用：外键 RESTRICT 挡住删除。
func TestModelDelete_FKViolation(t *testing.T) {
	st, _, _, ms := newTestService(t)
	pid, _ := seedTwoProviders(st)
	id := st.seedModel(&Model{ProviderID: pid, Name: "A", ModelID: "m-a", Capability: providerapi.CapabilityChat}).ID
	st.deleteModelErr = pgErr("23503")

	err := ms.Delete(context.Background(), providerapi.DeleteModelReq{ID: id})
	assert.ErrorIs(t, err, providerapi.ErrModelInUse)
}

// SyncModels 的行为测试见 sync_test.go（真实实现：拉取 / 解析 / upsert / 缓存失效）。
func TestModelSyncModels_NotFound(t *testing.T) {
	_, _, _, ms := newTestService(t)
	_, err := ms.SyncModels(context.Background(), providerapi.SyncModelsReq{ID: 999})
	assert.ErrorIs(t, err, providerapi.ErrProviderNotFound)
}

// warmDetail 经 providerService.Get 预热某 provider 的详情缓存，返回是否成功落 key。
func warmDetail(t *testing.T, ctx context.Context, ps providerapi.ProviderService, mr *miniredis.Miniredis, providerID uint64) bool {
	t.Helper()
	if _, err := ps.Get(ctx, providerapi.GetProviderReq{ID: providerID}); err != nil {
		t.Fatalf("warm detail cache: %v", err)
	}
	return mr.Exists(fmt.Sprintf(detailFullKeyFmt, providerID))
}

// int32Ptr 便捷取指针。
func int32Ptr(v int32) *int32 { return &v }

func TestModelListByIDs(t *testing.T) {
	st, _, _, ms := newTestService(t)
	ctx := context.Background()
	pid, cid := seedTwoProviders(st)
	gpt := st.seedModel(&Model{ProviderID: pid, Name: "GPT-4o", ModelID: "gpt-4o", Capability: providerapi.CapabilityChat})
	sonnet := st.seedModel(&Model{ProviderID: cid, Name: "Sonnet", ModelID: "claude-sonnet", Capability: providerapi.CapabilityChat})

	// 含悬空 id（模型被删）：跳过不报错；返回按 id 升序
	items, err := ms.ListByIDs(ctx, providerapi.ListModelsByIDsReq{IDs: []uint64{sonnet.ID, 9999, gpt.ID}})
	require.NoError(t, err)
	require.Len(t, items, 2)
	assert.Equal(t, strconv.FormatUint(gpt.ID, 10), items[0].ID, "id 升序（字符串化）")
	assert.Equal(t, strconv.FormatUint(sonnet.ID, 10), items[1].ID)

	// 空 ids：binding 校验失败
	_, err = ms.ListByIDs(ctx, providerapi.ListModelsByIDsReq{})
	assert.Error(t, err)
}
