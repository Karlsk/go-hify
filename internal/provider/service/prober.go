// 手动连通性探测引擎（db_model.md §2.3 手动半边）：按 kind 分发探测端点，直连轻量 GET，
// 不走 eino、不占 bulkhead、不触发熔断——探测只回答"端点可达吗、鉴权过吗"，不产生 token 消费。
// 定时探测（StartProber）属后续批次，复用本引擎与状态机。
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/Karlsk/go-hify/internal/platform/llm"
	providerapi "github.com/Karlsk/go-hify/internal/provider/api"
)

// 探测参数（db_model.md §2.3：阈值是包内常量，暂不进配置）。
const (
	probeTimeout       = 10 * time.Second // 整请求兜底超时；探测是非流式 GET，设 Client.Timeout 合法（禁令只针对 SSE 流）
	probeSlowLatency   = 3 * time.Second  // 成功但超过此延迟 → degraded
	probeFailThreshold = 3                // 连续失败达到此数 → down
	probeErrMaxLen     = 200              // error_message 截断长度（schema 注释同步）
	probeBodyLimit     = 4 << 10          // 错误页摘要读取上限（排障线索，无需完整 body）
	probeModelLimit    = 1 << 20          // 模型列表读取上限（/v1/models 可达百条、Ollama tags 更多，1MB 足够）
	anthropicVersion   = "2023-06-01"     // claude 探测必带的协议版本头
	probeInterval      = time.Minute      // 定时探测轮询间隔（db_model §2.3.1：默认 60s，包内常量）
)

// kindDefaultBase 各 kind 的默认 base URL（providers.base_url 空串时使用）。
// openai_compatible 无默认值——必填 base_url 由 api 层 Validate 保证，此处兜底报错。
var kindDefaultBase = map[string]string{
	providerapi.KindOpenAI:           "https://api.openai.com/v1",
	providerapi.KindClaude:           "https://api.anthropic.com/v1",
	providerapi.KindGemini:           "https://generativelanguage.googleapis.com/v1beta",
	providerapi.KindOllama:           "http://localhost:11434",
	providerapi.KindOpenAICompatible: "",
}

// probeResult 单次探测的原始结果（状态机转移前的输入）。
type probeResult struct {
	success    bool
	latency    time.Duration
	modelCount int32
	errMsg     string
}

// probeClient 探测 HTTP 窄接口（小接口惯例）：*http.Client 凭结构化类型满足，测试用 httptest 打桩。
type probeClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// NewProbeClient 构造探测专用 client：出站 transport 与 LLM 层同配置（复用 NewSharedTransport 的
// 连接池参数），但独立实例——探测不走 bulkhead、量极小，独立连接池可接受，也避免与流式调用争池。
// 非 SSE 的短 GET，http.Client.Timeout 在此合法。
func NewProbeClient() probeClient {
	return llm.NewJSONClient(llm.NewSharedTransport(), probeTimeout)
}

// probe 执行一次探测：按 kind 组 URL 与认证头 → GET → 结果分类。
// apiKey 为解密后的明文，只进请求头，永不进日志 / 结果 / 缓存。
func probe(ctx context.Context, hc probeClient, kind, baseURL, apiKey string) probeResult {
	url, headers, err := probeTarget(kind, baseURL, apiKey)
	if err != nil {
		return probeResult{errMsg: err.Error()}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return probeResult{errMsg: fmt.Sprintf("build request: %v", err)}
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	start := time.Now()
	resp, err := hc.Do(req)
	latency := time.Since(start)
	if err != nil {
		// 网络层错误（连接拒绝 / DNS / 超时）：err 含 URL 但不含请求头，无泄露风险。
		return probeResult{latency: latency, errMsg: truncateErr(err.Error())}
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300, resp.StatusCode == http.StatusTooManyRequests:
		// 2xx 与 429 都算可达（限流 ≠ 不可达，与 LLM 错误分类一致）；429 的 body 非模型列表，计数为 0。
		return probeResult{success: true, latency: latency, modelCount: countModels(resp.Body)}
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return probeResult{latency: latency, errMsg: truncateErr(fmt.Sprintf("鉴权失败（HTTP %d）：%s", resp.StatusCode, bodySnippet(resp.Body)))}
	default:
		return probeResult{latency: latency, errMsg: truncateErr(fmt.Sprintf("HTTP %d：%s", resp.StatusCode, bodySnippet(resp.Body)))}
	}
}

// probeTarget 由 kind 得出探测 URL 与认证头；URL = 有效 base + kind 端点路径（base 尾部 / 容错）。
func probeTarget(kind, baseURL, apiKey string) (string, map[string]string, error) {
	if baseURL == "" {
		baseURL = kindDefaultBase[kind]
	}
	if baseURL == "" {
		return "", nil, fmt.Errorf("kind %s 必须配置 base_url 才能探测", kind)
	}
	base := strings.TrimSuffix(baseURL, "/")
	h := map[string]string{"Accept": "application/json"}
	if apiKey != "" {
		switch kind {
		case providerapi.KindOpenAI, providerapi.KindOpenAICompatible:
			h["Authorization"] = "Bearer " + apiKey
		case providerapi.KindClaude:
			h["x-api-key"] = apiKey
			h["anthropic-version"] = anthropicVersion
		case providerapi.KindGemini:
			h["x-goog-api-key"] = apiKey
		}
	}
	if kind == providerapi.KindClaude {
		h["anthropic-version"] = anthropicVersion // 无 key 也必带：缺版本头直接 400，误判为不可达
	}

	switch kind {
	case providerapi.KindOpenAI, providerapi.KindOpenAICompatible, providerapi.KindClaude, providerapi.KindGemini:
		return base + "/models", h, nil
	case providerapi.KindOllama:
		return base + "/api/tags", h, nil
	default:
		return "", nil, fmt.Errorf("不支持的 kind: %s", kind)
	}
}

// countModels 解析模型列表响应的条数：openai/claude/gemini 是 {"data":[...]}，
// ollama 是 {"models":[...]}；body 非模型列表（429 错误页、HTML）返回 0，不报错。
// 上限用 probeModelLimit（远大于列表实际规模），避免大列表截断导致健康的 provider 误报 0。
func countModels(r io.Reader) int32 {
	var payload struct {
		Data   []json.RawMessage `json:"data"`
		Models []json.RawMessage `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(r, probeModelLimit)).Decode(&payload); err != nil {
		return 0
	}
	if n := len(payload.Data); n > 0 {
		return int32(n)
	}
	return int32(len(payload.Models))
}

// bodySnippet 取失败响应的截断摘要（排障线索）；不可读 / 非文本返回空串。
func bodySnippet(r io.Reader) string {
	b, err := io.ReadAll(io.LimitReader(r, probeBodyLimit))
	if err != nil || len(b) == 0 {
		return ""
	}
	return strings.TrimSpace(strings.Map(func(c rune) rune {
		if c == '\n' || c == '\r' || c == '\t' {
			return ' '
		}
		return c
	}, string(b)))
}

// truncateErr 按字符（rune）截断到 probeErrMaxLen，防超长响应撑爆 error_message 列。
func truncateErr(s string) string {
	r := []rune(s)
	if len(r) <= probeErrMaxLen {
		return s
	}
	return string(r[:probeErrMaxLen])
}

// applyProbeResult 按 DEGRADED 状态机（db_model.md §2.3）转移健康值；纯函数便于表驱动测试。
//   - 成功且 latency ≤ 3s → up（fail_count 清零，刷新 last_success_at）
//   - 成功但 > 3s → degraded（同样清零 / 刷新）
//   - 失败 → fail_count+1；≥3 → down，不足 3 → degraded
//
// old 为 nil 表示首次探测（unknown 起步）；返回新行（不修改 old）。
func applyProbeResult(providerID uint64, old *ProviderHealth, r probeResult, now time.Time) *ProviderHealth {
	h := &ProviderHealth{ProviderID: providerID, Status: providerapi.HealthUnknown}
	if old != nil {
		h.Status = old.Status
		h.FailCount = old.FailCount
		h.LastSuccessAt = old.LastSuccessAt
	}
	h.LastCheckAt = &now
	lat := int32(r.latency.Milliseconds())
	h.LatencyMs = &lat
	h.ErrorMessage = r.errMsg
	h.CreatedAt, h.UpdatedAt = now, now

	switch {
	case r.success && r.latency <= probeSlowLatency:
		h.Status = providerapi.HealthUp
	case r.success: // 可达但慢
		h.Status = providerapi.HealthDegraded
	default:
		h.FailCount++
		if h.FailCount >= probeFailThreshold {
			h.Status = providerapi.HealthDown
		} else {
			h.Status = providerapi.HealthDegraded
		}
	}
	if r.success {
		h.FailCount = 0
		successAt := now
		h.LastSuccessAt = &successAt
	}
	return h
}

// probeOne 单 provider 探测写库核心（手动 TestConnection 与定时 runProbeRound 共用）：
// 解密 key → probe（kind 分发端点，10s 超时）→ 事务内锁定读 → DEGRADED 状态机转移 →
// UpsertHealth；状态翻转为 down 时打 WARN。返回探测结果本体四字段。
// 解密失败（主密钥轮换后的旧密文）是本地配置问题而非供应商故障：不发请求、不动 health，
// 以失败结果返回——避免把配置错误记成供应商 down。
func (s *providerService) probeOne(ctx context.Context, p *Provider) (*providerapi.ConnectionTestSchema, error) {
	key := ""
	if enc := p.AuthConfig[apiKeyEncryptedKey]; enc != "" {
		pt, err := decryptAPIKey(s.master, enc)
		if err != nil {
			// 主密钥轮换后的旧密文每次探测都失败：按 provider 去重只 WARN 一次，避免定时探测每 60s 刷屏。
			if _, dup := s.decryptFailWarned.LoadOrStore(p.ID, struct{}{}); !dup {
				slog.WarnContext(ctx, "decrypt api key failed; abort probe (logged once per provider)", "provider_id", p.ID, "err", err)
			}
			return &providerapi.ConnectionTestSchema{ErrorMessage: "API Key 解密失败（主密钥可能已轮换，请重新录入）"}, nil
		}
		key = pt
	}

	res := probe(ctx, s.probe, p.Kind, p.BaseURL, key)
	now := time.Now()
	flippedToDown := false
	// 事务内锁定读 → 状态机转移 → 写回：把"读 fail_count + 算 +1 + 写"做成原子，否则并发探测
	// 都读到同一个 fail_count，连续失败阈值（≥3 → down）被延迟，破坏"连续失败≥3"契约。
	if err := s.store.WithTx(ctx, func(tx Store) error {
		old, err := tx.GetHealthByProviderIDForUpdate(ctx, p.ID)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("get health of provider %d: %w", p.ID, err)
		}
		nh := applyProbeResult(p.ID, old, res, now)
		if (old == nil || old.Status != providerapi.HealthDown) && nh.Status == providerapi.HealthDown {
			flippedToDown = true
		}
		return tx.UpsertHealth(ctx, nh)
	}); err != nil {
		return nil, fmt.Errorf("upsert health of provider %d: %w", p.ID, err)
	}
	if flippedToDown {
		slog.WarnContext(ctx, "provider flipped to down", "provider_id", p.ID, "kind", p.Kind,
			"latency_ms", res.latency.Milliseconds(), "err", res.errMsg)
	}
	return &providerapi.ConnectionTestSchema{
		Success:      res.success,
		LatencyMs:    int32(res.latency.Milliseconds()),
		ModelCount:   res.modelCount,
		ErrorMessage: res.errMsg,
	}, nil
}

// StartProber 启动定时健康探测循环：每 interval 轮询一轮，随 ctx 取消退出（优雅关闭）。
// 由组合根 go func 启动（接线见 db_model §2.3.1）；只探 enabled=true；探测走直连 HTTP，
// 不占 bulkhead、不触发熔断。time.Ticker 消费端忙时丢 tick（channel 缓冲 1），单轮 10s 上界
// << 60s 间隔，无重叠。
func (s *providerService) StartProber(ctx context.Context) {
	interval := s.interval
	if interval <= 0 { // 防御：非 NewProviderService 构造的零值 interval 会令 NewTicker panic
		interval = probeInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.runProbeRound(ctx)
		}
	}
}

// runProbeRound 单轮定时探测：ListProviders → 过滤 enabled → 每 provider 一个 goroutine 并发
// probeOne（个位数量级；goroutine 代价≈0，无需线程池）。同步方法（WaitGroup 等全部探测收尾），
// 供 StartProber 每轮调用、也供测试直调（不依赖真实 ticker）。
func (s *providerService) runProbeRound(ctx context.Context) {
	ps, err := s.store.ListProviders(ctx)
	if err != nil {
		slog.WarnContext(ctx, "prober: list providers failed; retry next round", "err", err)
		return
	}
	var wg sync.WaitGroup
	for i := range ps {
		p := &ps[i]
		if !p.Enabled {
			continue
		}
		wg.Add(1)
		go func(p *Provider) {
			defer wg.Done()
			if _, err := s.probeOne(ctx, p); err != nil {
				slog.ErrorContext(ctx, "prober: probe provider failed", "provider_id", p.ID, "err", err)
			}
		}(p)
	}
	wg.Wait()
}
