package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Karlsk/go-hify/internal/platform/errs"
	providerapi "github.com/Karlsk/go-hify/internal/provider/api"
)

// SyncModels 用例：5 kind 解析与端点分派、只增改不删（manual 保护 / discovered name 刷新 /
// 23505 竞态跳过）、缓存失效时机、错误路径（解密失败 → ErrInternal；上游失败 → 503）。
// 上游用 httptest 桩（body / 状态码可变），模型列表 client 是真实的（NewModelService 内组装）。

// stubUpstream 可变桩：记录请求路径与认证头，按当前 status / body 应答。
type stubUpstream struct {
	status int
	body   string
	path   string
	auth   string
}

func (s *stubUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.path = r.URL.Path
	s.auth = r.Header.Get("Authorization")
	w.Header().Set("Content-Type", "application/json")
	if s.status == 0 {
		s.status = http.StatusOK
	}
	w.WriteHeader(s.status)
	_, _ = w.Write([]byte(s.body))
}

// newSyncStub 建桩服务器（测试结束自动关闭）。
func newSyncStub(t *testing.T, body string) (*stubUpstream, string) {
	t.Helper()
	stub := &stubUpstream{body: body}
	ts := httptest.NewServer(stub)
	t.Cleanup(ts.Close)
	return stub, ts.URL
}

// TestParseModels 按 kind 归一解析（纯函数表驱动）：标识与展示名抽取、空条目跳过、
// 坏 JSON / 未知 kind 报 ErrServiceUnavailable。
func TestParseModels(t *testing.T) {
	cases := []struct {
		name    string
		kind    string
		body    string
		want    []discoveredModel
		wantErr error
	}{
		{
			name: "openai 无 display_name 回退 id",
			kind: providerapi.KindOpenAI,
			body: `{"data":[{"id":"gpt-4o"},{"id":"gpt-4o-mini"}]}`,
			want: []discoveredModel{{modelID: "gpt-4o", displayName: "gpt-4o"}, {modelID: "gpt-4o-mini", displayName: "gpt-4o-mini"}},
		},
		{
			name: "claude 带 display_name",
			kind: providerapi.KindClaude,
			body: `{"data":[{"id":"claude-sonnet-4-5","display_name":"Claude Sonnet 4.5"}]}`,
			want: []discoveredModel{{modelID: "claude-sonnet-4-5", displayName: "Claude Sonnet 4.5"}},
		},
		{
			name: "gemini 剥 models/ 前缀",
			kind: providerapi.KindGemini,
			body: `{"models":[{"name":"models/gemini-2.5-flash","displayName":"Gemini 2.5 Flash"}]}`,
			want: []discoveredModel{{modelID: "gemini-2.5-flash", displayName: "Gemini 2.5 Flash"}},
		},
		{
			name: "ollama 含 tag 的 model_id",
			kind: providerapi.KindOllama,
			body: `{"models":[{"name":"llama3:latest","model":"llama3"}]}`,
			want: []discoveredModel{{modelID: "llama3:latest", displayName: "llama3"}},
		},
		{
			name: "空条目跳过",
			kind: providerapi.KindOpenAICompatible,
			body: `{"data":[{"id":""},{"id":"ok"}]}`,
			want: []discoveredModel{{modelID: "ok", displayName: "ok"}},
		},
		{
			name: "空列表",
			kind: providerapi.KindOpenAI,
			body: `{"data":[]}`,
			want: []discoveredModel{},
		},
		{
			name:    "坏 JSON（HTML 错误页）",
			kind:    providerapi.KindOpenAI,
			body:    `<html>Bad Gateway</html>`,
			wantErr: errs.ErrServiceUnavailable,
		},
		{
			name:    "未知 kind",
			kind:    "unknown",
			body:    `{}`,
			wantErr: errs.ErrServiceUnavailable,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseModels(tc.kind, strings.NewReader(tc.body))
			if tc.wantErr != nil {
				assert.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestSyncModels_Kinds 端到端（真实 HTTP client + httptest 桩）：各 kind 走对端点路径
// （复用 probeTarget）、入库默认字段（capability=chat / enabled=true / source=discovered）。
func TestSyncModels_Kinds(t *testing.T) {
	cases := []struct {
		name     string
		kind     string
		body     string
		wantPath string
		wantID   string
		wantName string
	}{
		{
			name:     "openai_compatible",
			kind:     providerapi.KindOpenAICompatible,
			body:     `{"data":[{"id":"gpt-4o"}]}`,
			wantPath: "/models",
			wantID:   "gpt-4o",
			wantName: "gpt-4o",
		},
		{
			name:     "claude",
			kind:     providerapi.KindClaude,
			body:     `{"data":[{"id":"claude-sonnet-4-5","display_name":"Claude Sonnet 4.5"}]}`,
			wantPath: "/models",
			wantID:   "claude-sonnet-4-5",
			wantName: "Claude Sonnet 4.5",
		},
		{
			name:     "gemini",
			kind:     providerapi.KindGemini,
			body:     `{"models":[{"name":"models/gemini-2.5-flash","displayName":"Gemini 2.5 Flash"}]}`,
			wantPath: "/models",
			wantID:   "gemini-2.5-flash",
			wantName: "Gemini 2.5 Flash",
		},
		{
			name:     "ollama",
			kind:     providerapi.KindOllama,
			body:     `{"models":[{"name":"llama3:latest","model":"llama3"}]}`,
			wantPath: "/api/tags",
			wantID:   "llama3:latest",
			wantName: "llama3",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub, base := newSyncStub(t, tc.body)
			st, _, _, ms := newTestService(t)
			ctx := context.Background()
			pid := st.seed(&Provider{Name: tc.name, Kind: tc.kind, BaseURL: base, Enabled: true}).ID

			res, err := ms.SyncModels(ctx, providerapi.SyncModelsReq{ID: pid})
			require.NoError(t, err)
			assert.Equal(t, 1, res.Added)
			assert.Equal(t, 0, res.Updated)
			assert.Equal(t, tc.wantPath, stub.path, "kind 应分派到正确端点（复用 probeTarget）")

			mo, err := st.GetModelByProviderAndModelID(ctx, pid, tc.wantID)
			require.NoError(t, err)
			assert.Equal(t, tc.wantName, mo.Name)
			assert.Equal(t, providerapi.CapabilityChat, mo.Capability, "上游列表无能力元数据，默认 chat")
			assert.False(t, mo.Enabled, "导入默认待启用，勾选走 PUT /models/:id")
			assert.Equal(t, providerapi.SourceDiscovered, mo.Source)
		})
	}
}

// TestSyncModels_ManualProtectedAndDiscoveredRefresh 只增改不删规则：
// manual 行完全跳过（手编保护）；discovered 行仅在上游 display_name 变化时刷新 name；同 body 幂等。
func TestSyncModels_ManualProtectedAndDiscoveredRefresh(t *testing.T) {
	stub, base := newSyncStub(t, `{"data":[{"id":"m-1","display_name":"Old Name"},{"id":"m-manual","display_name":"Upstream Name"}]}`)
	st, _, _, ms := newTestService(t)
	ctx := context.Background()
	pid := st.seed(&Provider{Name: "Claude", Kind: providerapi.KindClaude, BaseURL: base, Enabled: true}).ID
	// 预置 manual 行，model_id 与上游条目重叠：同步不得触碰
	st.seedModel(&Model{ProviderID: pid, Name: "My Custom", ModelID: "m-manual",
		Capability: providerapi.CapabilityChat, Source: providerapi.SourceManual})

	res, err := ms.SyncModels(ctx, providerapi.SyncModelsReq{ID: pid})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Added, "仅 m-1 新增，manual 行跳过")
	assert.Equal(t, 0, res.Updated)

	// 上游改 display_name → 只有 discovered 行 name 刷新
	stub.body = `{"data":[{"id":"m-1","display_name":"New Name"},{"id":"m-manual","display_name":"Upstream Name"}]}`
	res, err = ms.SyncModels(ctx, providerapi.SyncModelsReq{ID: pid})
	require.NoError(t, err)
	assert.Equal(t, 0, res.Added)
	assert.Equal(t, 1, res.Updated)

	m1, err := st.GetModelByProviderAndModelID(ctx, pid, "m-1")
	require.NoError(t, err)
	assert.Equal(t, "New Name", m1.Name, "discovered 行 name 跟上游刷新")
	mm, err := st.GetModelByProviderAndModelID(ctx, pid, "m-manual")
	require.NoError(t, err)
	assert.Equal(t, "My Custom", mm.Name, "manual 行 name 不被覆盖")
	assert.Equal(t, providerapi.SourceManual, mm.Source)

	// 第三次同 body：幂等 0/0
	res, err = ms.SyncModels(ctx, providerapi.SyncModelsReq{ID: pid})
	require.NoError(t, err)
	assert.Equal(t, 0, res.Added)
	assert.Equal(t, 0, res.Updated)
}

// TestSyncModels_EnableChoiceSurvivesResync 勾选启用不被 sync 打回（sync_default_disabled_spec.md）：
// 导入默认 enabled=false → PUT 启用 → 上游改名后再 sync → enabled 仍 true 且 name 跟进刷新。
// 覆盖两条链路：Update 保留 source=discovered（后续 sync 仍认领 name 刷新）、
// UpdateModelName 列级更新不碰 enabled（勾选状态不被幂等重跑重置）。
func TestSyncModels_EnableChoiceSurvivesResync(t *testing.T) {
	stub, base := newSyncStub(t, `{"data":[{"id":"gpt-4o","display_name":"GPT-4o"}]}`)
	st, _, _, ms := newTestService(t)
	ctx := context.Background()
	pid := st.seed(&Provider{Name: "Compat", Kind: providerapi.KindOpenAICompatible, BaseURL: base, Enabled: true}).ID

	res, err := ms.SyncModels(ctx, providerapi.SyncModelsReq{ID: pid})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Added)
	mo, err := st.GetModelByProviderAndModelID(ctx, pid, "gpt-4o")
	require.NoError(t, err)
	assert.False(t, mo.Enabled, "导入默认待启用")

	// 勾选启用：走现有 Update（PUT 语义，前端勾选框的落点）
	upd, err := ms.Update(ctx, providerapi.UpdateModelReq{ID: mo.ID, ProviderID: pid,
		Name: mo.Name, ModelID: mo.ModelID, Capability: providerapi.CapabilityChat, Enabled: true})
	require.NoError(t, err)
	assert.True(t, upd.Enabled)

	// 上游改名后再 sync：name 刷新生效、勾选保持
	stub.body = `{"data":[{"id":"gpt-4o","display_name":"GPT-4o 2026"}]}`
	res, err = ms.SyncModels(ctx, providerapi.SyncModelsReq{ID: pid})
	require.NoError(t, err)
	assert.Equal(t, 0, res.Added)
	assert.Equal(t, 1, res.Updated)

	mo, err = st.GetModelByProviderAndModelID(ctx, pid, "gpt-4o")
	require.NoError(t, err)
	assert.True(t, mo.Enabled, "勾选启用不被 sync 打回")
	assert.Equal(t, "GPT-4o 2026", mo.Name, "name 仍跟进上游改名")
	assert.Equal(t, providerapi.SourceDiscovered, mo.Source, "PUT 启用后仍归 discovered")
}

// TestSyncModels_EvictsDetailOnChangeOnly 缓存失效时机：有改动（added/updated>0）失效
// detail:{id}；无改动不失效（避免无谓的缓存重建）。
func TestSyncModels_EvictsDetailOnChangeOnly(t *testing.T) {
	_, base := newSyncStub(t, `{"data":[{"id":"gpt-4o"},{"id":"gpt-4o-mini"}]}`)
	st, mr, ps, ms := newTestService(t)
	ctx := context.Background()
	pid := st.seed(&Provider{Name: "Compat", Kind: providerapi.KindOpenAICompatible, BaseURL: base, Enabled: true}).ID
	detailKey := fmt.Sprintf(detailFullKeyFmt, pid)

	require.True(t, warmDetail(t, ctx, ps, mr, pid), "预热 detail 缓存")
	res, err := ms.SyncModels(ctx, providerapi.SyncModelsReq{ID: pid})
	require.NoError(t, err)
	assert.Equal(t, 2, res.Added)
	assert.False(t, mr.Exists(detailKey), "有新增 → detail 缓存失效")

	require.True(t, warmDetail(t, ctx, ps, mr, pid), "再次预热")
	res, err = ms.SyncModels(ctx, providerapi.SyncModelsReq{ID: pid})
	require.NoError(t, err)
	assert.Equal(t, 0, res.Added+res.Updated)
	assert.True(t, mr.Exists(detailKey), "无改动 → 不失效")
}

// TestSyncModels_SendsBearerFromDecryptedKey 解密后的明文 key 进请求头（且仅进请求头）。
func TestSyncModels_SendsBearerFromDecryptedKey(t *testing.T) {
	stub, base := newSyncStub(t, `{"data":[{"id":"gpt-4o"}]}`)
	st, _, _, ms := newTestService(t)
	enc, err := encryptAPIKey(testMaster, "sk-sync-test-key")
	require.NoError(t, err)
	pid := st.seed(&Provider{Name: "OpenAI", Kind: providerapi.KindOpenAI, BaseURL: base,
		AuthConfig: map[string]string{apiKeyEncryptedKey: enc}, Enabled: true}).ID

	_, err = ms.SyncModels(context.Background(), providerapi.SyncModelsReq{ID: pid})
	require.NoError(t, err)
	assert.Equal(t, "Bearer sk-sync-test-key", stub.auth)
}

// TestSyncModels_UniqueViolationRaceSkip 插入撞唯一约束（与手工创建竞态）按已存在跳过，不报错。
func TestSyncModels_UniqueViolationRaceSkip(t *testing.T) {
	_, base := newSyncStub(t, `{"data":[{"id":"gpt-4o"}]}`)
	st, _, _, ms := newTestService(t)
	st.createModelErr = pgErr("23505")
	pid := st.seed(&Provider{Name: "Compat", Kind: providerapi.KindOpenAICompatible, BaseURL: base, Enabled: true}).ID

	res, err := ms.SyncModels(context.Background(), providerapi.SyncModelsReq{ID: pid})
	require.NoError(t, err)
	assert.Equal(t, 0, res.Added+res.Updated, "23505 竞态按已存在跳过")
}

// TestSyncModels_UpstreamUnavailable 上游失败（HTTP 401 / 网络错误）→ ErrServiceUnavailable（503）。
func TestSyncModels_UpstreamUnavailable(t *testing.T) {
	t.Run("http 401", func(t *testing.T) {
		stub, base := newSyncStub(t, `{"error":"invalid key"}`)
		stub.status = http.StatusUnauthorized
		st, _, _, ms := newTestService(t)
		pid := st.seed(&Provider{Name: "Compat", Kind: providerapi.KindOpenAICompatible, BaseURL: base, Enabled: true}).ID

		_, err := ms.SyncModels(context.Background(), providerapi.SyncModelsReq{ID: pid})
		assert.ErrorIs(t, err, errs.ErrServiceUnavailable)
	})
	t.Run("network refused", func(t *testing.T) {
		stub := &stubUpstream{body: `{}`}
		ts := httptest.NewServer(stub)
		base := ts.URL
		ts.Close() // 先关：连接拒绝
		st, _, _, ms := newTestService(t)
		pid := st.seed(&Provider{Name: "Compat", Kind: providerapi.KindOpenAICompatible, BaseURL: base, Enabled: true}).ID

		_, err := ms.SyncModels(context.Background(), providerapi.SyncModelsReq{ID: pid})
		assert.ErrorIs(t, err, errs.ErrServiceUnavailable)
	})
}

// TestSyncModels_DecryptFails 密文损坏（主密钥轮换后的旧数据）→ ErrInternal（手动同步响亮失败）。
func TestSyncModels_DecryptFails(t *testing.T) {
	_, base := newSyncStub(t, `{"data":[]}`)
	st, _, _, ms := newTestService(t)
	pid := st.seed(&Provider{Name: "OpenAI", Kind: providerapi.KindOpenAI, BaseURL: base,
		AuthConfig: map[string]string{apiKeyEncryptedKey: "garbage-not-a-cipher"}, Enabled: true}).ID

	_, err := ms.SyncModels(context.Background(), providerapi.SyncModelsReq{ID: pid})
	assert.ErrorIs(t, err, errs.ErrInternal)
}
