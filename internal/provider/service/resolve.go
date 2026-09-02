// ResolveLLMConfig：跨模块取 LLM 调用配置（model → provider → 解密 key）。
// 单独成文件：service.go 聚焦 CRUD 编排，本文件只服务 chat / workflow 发起调用前的配置解析。
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"gorm.io/gorm"

	"github.com/Karlsk/go-hify/internal/platform/errs"
	providerapi "github.com/Karlsk/go-hify/internal/provider/api"
)

// ResolveLLMConfig 取一次 LLM 调用的 provider 侧配置。
// 不缓存：每次会话开始仅 2 次主键查询，代价可忽略；且明文 key 禁入 Redis（crypto.go 四不）。
// 明文 key 只进返回值（调用方内存内即取即用），不进日志、不进错误串。
func (s *modelService) ResolveLLMConfig(ctx context.Context, req providerapi.ResolveLLMConfigReq) (*providerapi.LLMConfig, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate resolve llm config: %w", errs.ErrValidationFailed)
	}

	m, err := s.store.GetModelByID(ctx, req.ModelID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, providerapi.ErrModelNotFound
		}
		return nil, fmt.Errorf("get model %d: %w", req.ModelID, err)
	}
	if !m.Enabled {
		return nil, providerapi.ErrModelDisabled
	}

	p, err := s.store.GetProviderByID(ctx, m.ProviderID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, providerapi.ErrProviderNotFound // 悬空引用：模型行在、provider 行不在
		}
		return nil, fmt.Errorf("get provider %d: %w", m.ProviderID, err)
	}
	if !p.Enabled {
		return nil, providerapi.ErrProviderDisabled
	}

	key := ""
	if enc := p.AuthConfig[apiKeyEncryptedKey]; enc != "" {
		pt, err := decryptAPIKey(s.master, enc)
		if err != nil {
			// 主密钥轮换后的旧密文：本地配置问题而非供应商故障。只记 provider_id，不记任何 key 材料。
			slog.WarnContext(ctx, "resolve llm config: decrypt api key failed (master key rotated? re-enter the key)", "provider_id", p.ID)
			return nil, fmt.Errorf("%w: api key decrypt failed (master key rotated? re-enter the key)", errs.ErrInternal)
		}
		key = pt
	}

	return &providerapi.LLMConfig{
		ProviderName: p.Name,
		Kind:         p.Kind,
		BaseURL:      p.BaseURL,
		APIKey:       key,
		ModelID:      m.ModelID,
	}, nil
}
