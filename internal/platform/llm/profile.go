package llm

import "time"

// Profile 每供应商一份的并发与时间参数（CLAUDE.md《每供应商 Profile 与行为差异》）。
// 默认值可被 DB 里的 provider 配置覆盖；kind 级差异在 ProfileForKind。
type Profile struct {
	Bulkhead        int           // 每供应商在途调用槽位
	AcquireTimeout  time.Duration // 抢槽超时，拿不到 fail-fast
	TTFT            time.Duration // 首 token 超时
	Idle            time.Duration // 流空闲看门狗（每 chunk 重置）
	Overall         time.Duration // 整体超时（包住重试全过程）
	MaxRetries      int           // 首 token 前最大重试次数（总尝试 = MaxRetries+1）
	BackoffBase     time.Duration // 退避基数
	BackoffCap      time.Duration // 退避封顶
	RetryAfterCap   time.Duration // 429 Retry-After 尊重上限，超过直接放弃重试
	BreakerAfter    int           // 连续最终失败次数 → 熔断打开
	BreakerCooldown time.Duration // 熔断打开时长
}

// DefaultProfile 返回 CLAUDE.md 定义的默认值（bulkhead 16、TTFT 30s、idle 30s、overall 5min、重试 2、退避 500ms/8s、Retry-After 10s、熔断 5/30s）。
func DefaultProfile() Profile {
	return Profile{
		Bulkhead:        16,
		AcquireTimeout:  5 * time.Second,
		TTFT:            30 * time.Second,
		Idle:            30 * time.Second,
		Overall:         5 * time.Minute,
		MaxRetries:      2,
		BackoffBase:     500 * time.Millisecond,
		BackoffCap:      8 * time.Second,
		RetryAfterCap:   10 * time.Second,
		BreakerAfter:    5,
		BreakerCooldown: 30 * time.Second,
	}
}

// ProviderKind 提供商类型，与 providers.kind 的 CHECK 取值对齐。
type ProviderKind string

const (
	// KindOpenAICompatible OpenAI 兼容端点：官方 OpenAI（base_url 空 → adapter 默认
	// https://api.openai.com/v1）、代理/镜像、国产兼容端点（mimo 等）统一走此 kind。
	// 不设独立的「openai」kind——协议同一套，只差 base_url。
	// 预留：openai_response（OpenAI Responses API）待 eino-ext 底层 SDK 迁到官方
	// openai-go 后再开（当前 acl/openai 基于 sashabaranov fork，仅实现 chat completions）。
	KindOpenAICompatible ProviderKind = "openai_compatible"
	KindClaude           ProviderKind = "claude"
	KindGemini           ProviderKind = "gemini"
	KindOllama           ProviderKind = "ollama"
)

// ProfileForKind 返回该供应商的默认 Profile（CLAUDE.md《每供应商 Profile 与行为差异》）：
// Ollama 本地冷启动要加载模型，TTFT 放宽到 120s（配合 OLLAMA_KEEP_ALIVE 保温）。
func ProfileForKind(kind ProviderKind) Profile {
	p := DefaultProfile()
	if kind == KindOllama {
		p.TTFT = 120 * time.Second
	}
	return p
}
