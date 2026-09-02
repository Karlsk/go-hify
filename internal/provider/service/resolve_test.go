package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Karlsk/go-hify/internal/platform/errs"
	providerapi "github.com/Karlsk/go-hify/internal/provider/api"
)

// ResolveLLMConfig 用例：跨模块配置解析的哨兵矩阵 + 解密 round-trip + 无缓存证明。

// seedResolveFixture 建 openai provider（带加密 key）+ 启用 chat 模型，返回 model id。
func seedResolveFixture(st *memStore) uint64 {
	pid := seedProviderWithKey(st)
	m := st.seedModel(&Model{
		ProviderID: pid,
		Name:       "GPT-4o",
		ModelID:    "gpt-4o",
		Capability: providerapi.CapabilityChat,
		Enabled:    true,
	})
	return m.ID
}

func TestResolveLLMConfig_OK(t *testing.T) {
	st, _, _, ms := newTestService(t)
	mid := seedResolveFixture(st)

	cfg, err := ms.ResolveLLMConfig(context.Background(), providerapi.ResolveLLMConfigReq{ModelID: mid})
	require.NoError(t, err)
	assert.Equal(t, "OpenAI", cfg.ProviderName)
	assert.Equal(t, providerapi.KindOpenAI, cfg.Kind)
	assert.Equal(t, "gpt-4o", cfg.ModelID, "ModelID 是 API 标识而非主键")
	assert.Equal(t, "sk-old-key-123456", cfg.APIKey, "解密 round-trip 应还原明文")
}

func TestResolveLLMConfig_OllamaNoKey(t *testing.T) {
	st, _, _, ms := newTestService(t)
	pid := st.seed(&Provider{Name: "Local", Kind: providerapi.KindOllama, BaseURL: "http://localhost:11434", Enabled: true}).ID
	m := st.seedModel(&Model{ProviderID: pid, Name: "llama3.1", ModelID: "llama3.1", Capability: providerapi.CapabilityChat, Enabled: true})

	cfg, err := ms.ResolveLLMConfig(context.Background(), providerapi.ResolveLLMConfigReq{ModelID: m.ID})
	require.NoError(t, err)
	assert.Equal(t, "", cfg.APIKey, "ollama 无鉴权 → 空串")
	assert.Equal(t, "http://localhost:11434", cfg.BaseURL)
}

func TestResolveLLMConfig_ModelNotFound(t *testing.T) {
	_, _, _, ms := newTestService(t)
	_, err := ms.ResolveLLMConfig(context.Background(), providerapi.ResolveLLMConfigReq{ModelID: 999})
	assert.ErrorIs(t, err, providerapi.ErrModelNotFound)
}

func TestResolveLLMConfig_ModelDisabled(t *testing.T) {
	st, _, _, ms := newTestService(t)
	mid := seedResolveFixture(st)
	st.models[mid].Enabled = false

	_, err := ms.ResolveLLMConfig(context.Background(), providerapi.ResolveLLMConfigReq{ModelID: mid})
	assert.ErrorIs(t, err, providerapi.ErrModelDisabled)
}

func TestResolveLLMConfig_ProviderDangling(t *testing.T) {
	st, _, _, ms := newTestService(t)
	mid := seedResolveFixture(st)
	st.models[mid].ProviderID = 999 // 悬空引用（FK 由 DB 挡，内存直改模拟历史脏数据）

	_, err := ms.ResolveLLMConfig(context.Background(), providerapi.ResolveLLMConfigReq{ModelID: mid})
	assert.ErrorIs(t, err, providerapi.ErrProviderNotFound)
}

func TestResolveLLMConfig_ProviderDisabled(t *testing.T) {
	st, _, _, ms := newTestService(t)
	mid := seedResolveFixture(st)
	st.providers[st.models[mid].ProviderID].Enabled = false

	_, err := ms.ResolveLLMConfig(context.Background(), providerapi.ResolveLLMConfigReq{ModelID: mid})
	assert.ErrorIs(t, err, providerapi.ErrProviderDisabled)
}

func TestResolveLLMConfig_DecryptFails(t *testing.T) {
	st, _, _, ms := newTestService(t)
	mid := seedResolveFixture(st)
	pid := st.models[mid].ProviderID
	st.providers[pid].AuthConfig[apiKeyEncryptedKey] = "not-valid-base64!" // 篡改密文

	_, err := ms.ResolveLLMConfig(context.Background(), providerapi.ResolveLLMConfigReq{ModelID: mid})
	assert.True(t, errors.Is(err, errs.ErrInternal), "err = %v, want errs.ErrInternal", err)
}

func TestResolveLLMConfig_ValidateModelID(t *testing.T) {
	_, _, _, ms := newTestService(t)
	_, err := ms.ResolveLLMConfig(context.Background(), providerapi.ResolveLLMConfigReq{})
	assert.ErrorIs(t, err, errs.ErrValidationFailed)
}

func TestResolveLLMConfig_NotCached(t *testing.T) {
	st, _, _, ms := newTestService(t)
	mid := seedResolveFixture(st)

	before := st.getProviderByIDCalls
	for i := 0; i < 2; i++ {
		_, err := ms.ResolveLLMConfig(context.Background(), providerapi.ResolveLLMConfigReq{ModelID: mid})
		require.NoError(t, err)
	}
	assert.Equal(t, before+2, st.getProviderByIDCalls, "每次解析都查 store（明文 key 禁入缓存，无缓存命中）")
}
