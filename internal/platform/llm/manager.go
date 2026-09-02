package llm

import (
	"errors"
	"fmt"
	"sync"
)

// UpstreamOptions 构造上游适配器的配置（adapter 工厂入参）。
type UpstreamOptions struct {
	Kind    ProviderKind
	BaseURL string
	APIKey  string
	Model   string
}

// UpstreamFactory 构造某 provider 某模型的原始适配器（eino adapter 实现，组合根注入）。
type UpstreamFactory func(opts UpstreamOptions) (Streamer, error)

// clientKey Client 缓存的组合键：provider 名 + model 标识（结构体键，无字符串拼接碰撞风险）。
type clientKey struct{ provider, model string }

// Manager 管理受保护 Client 的双层缓存：
//   - gate 按 provider 缓存共享——bulkhead 槽位与熔断状态按 provider 独立（CLAUDE.md 契约：
//     每供应商 16 槽 + 每供应商一个熔断器，不随模型数分裂）；
//   - Client 按 (provider, model) 缓存——各模型持有独立的 eino 上游实例。
//
// 同一 provider 的多个模型返回不同 Client，但共享同一套槽位与熔断。
type Manager struct {
	mu          sync.Mutex
	gates       map[string]*gate      // provider → gate（共享防护设施）
	clients     map[clientKey]*Client // (provider, model) → Client（独立上游实例）
	profiles    map[string]Profile    // provider → Profile 覆盖（DB provider 配置）
	newUpstream UpstreamFactory
}

// NewManager 创建 Manager；newUpstream 为 nil 时（adapter 未实现）Client 调用返回明确错误。
func NewManager(newUpstream UpstreamFactory) *Manager {
	return &Manager{
		gates:       make(map[string]*gate),
		clients:     make(map[clientKey]*Client),
		profiles:    make(map[string]Profile),
		newUpstream: newUpstream,
	}
}

// Client 获取 key（provider 名）+ opts.Model 标识的受保护客户端。
// 首次调用按需创建 gate 与上游实例，后续复用：同 (provider, model) 返回同一 Client，
// 同 provider 不同 model 返回不同 Client 但共享同一 gate。
func (m *Manager) Client(key string, opts UpstreamOptions) (*Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ck := clientKey{provider: key, model: opts.Model}
	if c, ok := m.clients[ck]; ok {
		return c, nil
	}
	if m.newUpstream == nil {
		return nil, errors.New("llm: upstream factory not injected (eino adapter pending)")
	}
	g, ok := m.gates[key]
	if !ok {
		p := ProfileForKind(opts.Kind)
		if override, ok := m.profiles[key]; ok {
			p = override
		}
		g = newGate(key, p)
		m.gates[key] = g
	}
	up, err := m.newUpstream(opts)
	if err != nil {
		return nil, fmt.Errorf("create upstream for %s: %w", key, err)
	}
	c := newClientWithGate(key, g, up)
	m.clients[ck] = c
	return c, nil
}

// SetProfile 覆盖某 provider 的 Profile（DB 配置加载后调用）。
// 只影响之后新建的 gate：已建 gate 上的新 model Client 沿用该 gate 的 profile——
// 避免同 provider 各模型的防护参数不一致。
func (m *Manager) SetProfile(key string, p Profile) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.profiles[key] = p
}
