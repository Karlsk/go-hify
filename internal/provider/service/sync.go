// 模型列表同步引擎（SyncModels 真实实现，wiring_sync_spec.md §3）：直连轻量 GET 拉取上游
// 模型目录——与连通性探测同模式（元数据 GET、不产生 token 消费，不走 bulkhead / 熔断）。
// eino 的 ChatModel 契约只有 Generate / Stream、无 discovery 概念；统一 HTTP 是覆盖 5 种 kind
// （含无 SDK 的 openai_compatible）的单一代码路径。
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"gorm.io/gorm"

	"github.com/Karlsk/go-hify/internal/platform/errs"
	providerapi "github.com/Karlsk/go-hify/internal/provider/api"
)

// discoveredModel 归一后的上游模型条目（各 kind 响应解析成同一形态再入库）。
type discoveredModel struct {
	modelID     string // API 标识（ollama 含 tag；gemini 已剥 models/ 前缀）
	displayName string // 展示名（缺省回退 modelID）
}

// SyncModels 自动发现并同步模型列表（只增改不删，不覆盖手编字段）：
// 解密 key → 直连 GET 上游模型目录（probeTarget 组 URL / 认证头，10s 超时）→ 按 kind 解析 →
// 一次载入库内全表做内存 diff 后按变化集落库：新条目批量插入（capability=chat、enabled=false
// 待启用——勾选启用走 PUT /models/:id，sync 不覆盖 enabled，sync_default_disabled_spec.md）、
// discovered 行仅列级刷新 name、manual 行完全跳过；批量插入撞唯一约束（与手工创建竞态）退回
// 逐条、冲突行跳过。
// 有改动才失效 detail 缓存（失效矩阵：model 增删改失效 detail，list 是 providers 快照不涉及）。
//
// 限制（上游列表不含该元数据，wiring_sync_spec.md §3.2）：capability 固定 chat（embedding
// 人工补录）；价格 / 窗口 / extra_params 一律不填充；gemini 只取第一页（不跟 pageToken）。
func (s *modelService) SyncModels(ctx context.Context, req providerapi.SyncModelsReq) (*providerapi.ModelSyncResultSchema, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate sync models: %w", err)
	}
	p, err := s.store.GetProviderByID(ctx, req.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, providerapi.ErrProviderNotFound
		}
		return nil, fmt.Errorf("get provider %d: %w", req.ID, err)
	}
	key := ""
	if enc := p.AuthConfig[apiKeyEncryptedKey]; enc != "" {
		pt, err := decryptAPIKey(s.master, enc)
		if err != nil {
			// 手动同步对配置问题响亮失败（区别于定时探测的去重 WARN）：细节进日志、前端 500。
			return nil, fmt.Errorf("%w: api key decrypt failed (master key rotated? re-enter the key)", errs.ErrInternal)
		}
		key = pt
	}

	models, err := fetchModels(ctx, s.probe, p.Kind, p.BaseURL, key)
	if err != nil {
		return nil, err
	}
	added, updated, err := s.applyDiscovered(ctx, p.ID, models)
	if err != nil {
		return nil, err
	}
	if added+updated > 0 {
		evict(ctx, s.cm, fmt.Sprintf(cacheKeyDetail, p.ID))
	}
	return &providerapi.ModelSyncResultSchema{Added: added, Updated: updated}, nil
}

// fetchModels 拉取上游模型目录：复用探测的 URL / 认证头组装与 10s client。
// 失败路径已包装哨兵：网络错误 / HTTP 非 2xx / 响应不可解析 → ErrServiceUnavailable（503，
// 上游暂不可用）；URL 不含 key（认证走请求头），错误串可安全进日志。
func fetchModels(ctx context.Context, hc probeClient, kind, baseURL, apiKey string) ([]discoveredModel, error) {
	url, headers, err := probeTarget(kind, baseURL, apiKey)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errs.ErrServiceUnavailable, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build models request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: fetch %s: %v", errs.ErrServiceUnavailable, url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: http %d: %s", errs.ErrServiceUnavailable, resp.StatusCode,
			truncateErr(bodySnippet(resp.Body)))
	}
	return parseModels(kind, resp.Body)
}

// parseModels 按 kind 解析模型列表响应：openai / openai_compatible / claude 是 {"data":[...]}，
// gemini / ollama 是 {"models":[...]}；条目字段各家不同（claude display_name、gemini
// "models/" 前缀 + displayName、ollama name 含 tag + model 基础名），此处归一。
func parseModels(kind string, r io.Reader) ([]discoveredModel, error) {
	var payload struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"` // claude
		} `json:"data"`
		Models []struct {
			Name        string `json:"name"`        // gemini "models/gemini-..."；ollama "llama3:latest"
			Model       string `json:"model"`       // ollama 基础名
			DisplayName string `json:"displayName"` // gemini
		} `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(r, probeModelLimit)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("%w: decode models response: %v", errs.ErrServiceUnavailable, err)
	}

	out := make([]discoveredModel, 0, len(payload.Data)+len(payload.Models))
	switch kind {
	case providerapi.KindOpenAI, providerapi.KindOpenAICompatible, providerapi.KindClaude:
		for _, d := range payload.Data {
			if d.ID == "" {
				continue
			}
			out = append(out, discoveredModel{modelID: d.ID, displayName: firstNonEmpty(d.DisplayName, d.ID)})
		}
	case providerapi.KindGemini:
		for _, m := range payload.Models {
			id := strings.TrimPrefix(m.Name, "models/")
			if id == "" {
				continue
			}
			out = append(out, discoveredModel{modelID: id, displayName: firstNonEmpty(m.DisplayName, id)})
		}
	case providerapi.KindOllama:
		for _, m := range payload.Models {
			if m.Name == "" {
				continue
			}
			out = append(out, discoveredModel{modelID: m.Name, displayName: firstNonEmpty(m.Model, m.Name)})
		}
	default:
		return nil, fmt.Errorf("%w: unsupported kind %s", errs.ErrServiceUnavailable, kind)
	}
	return out, nil
}

// applyDiscovered 把上游目录落到库内，返回 (added, updated)。一次 ListModelsByProvider 载入
// 全表做内存 diff（消 N+1：300 模型从 ~600 次往返降到 1 读 + k 写），再按变化集发写：
//   - 新条目（库内无 model_id）→ CreateModels 批量插入（单条多 VALUES，enabled=false 待启用：
//     上游目录会带回几十上百个模型，导入默认停用、按需勾选启用，UpdateModelName 列级更新保证
//     勾选状态不被后续 sync 打回）；整批撞唯一约束（与手工创建竞态）时退回逐条插入、冲突行跳过；
//   - discovered 行且 name 与上游不一致 → UpdateModelName 列级 UPDATE（只写 name /
//     updated_at，WHERE 限定 source）——不用全列 Save，防止并发手工编辑被旧快照覆盖；
//   - manual 行完全跳过（手编保护，含 display_name）。
func (s *modelService) applyDiscovered(ctx context.Context, providerID uint64, models []discoveredModel) (int, int, error) {
	existing, err := s.store.ListModelsByProvider(ctx, providerID)
	if err != nil {
		return 0, 0, fmt.Errorf("list models of provider %d: %w", providerID, err)
	}
	byModelID := make(map[string]Model, len(existing))
	for i := range existing {
		byModelID[existing[i].ModelID] = existing[i]
	}

	toInsert := make([]*Model, 0, len(models))
	toRename := make([]struct {
		id   uint64
		name string
	}, 0)
	for _, dm := range models {
		if old, ok := byModelID[dm.modelID]; ok {
			if old.Source == providerapi.SourceDiscovered && old.Name != dm.displayName {
				toRename = append(toRename, struct {
					id   uint64
					name string
				}{id: old.ID, name: dm.displayName})
			}
			continue // 已存在（manual 保护 / name 未变）：不动
		}
		toInsert = append(toInsert, &Model{
			ProviderID:  providerID,
			Name:        dm.displayName,
			ModelID:     dm.modelID,
			Capability:  providerapi.CapabilityChat,
			Enabled:     false, // 待启用：勾选走 PUT /models/:id；sync 列级更新不碰 enabled
			Source:      providerapi.SourceDiscovered,
			ExtraParams: map[string]any{},
		})
	}

	added := 0
	if len(toInsert) > 0 {
		if err := s.store.CreateModels(ctx, toInsert); err != nil {
			if !isUniqueViolation(err) {
				return 0, 0, fmt.Errorf("create discovered models of provider %d: %w", providerID, err)
			}
			// 批量撞唯一约束：退回逐条，冲突行（与手工创建竞态）跳过
			for _, m := range toInsert {
				if err := s.store.CreateModel(ctx, m); err != nil {
					if isUniqueViolation(err) {
						continue
					}
					return 0, 0, fmt.Errorf("create discovered model %s: %w", m.ModelID, err)
				}
				added++
			}
		} else {
			added = len(toInsert)
		}
	}

	updated := 0
	for _, r := range toRename {
		if err := s.store.UpdateModelName(ctx, r.id, r.name); err != nil {
			return 0, 0, fmt.Errorf("refresh discovered model %d: %w", r.id, err)
		}
		updated++
	}
	return added, updated, nil
}

// firstNonEmpty 返回第一个非空串（展示名缺省回退 model id）。
func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
