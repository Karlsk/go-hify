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

// UpstreamFactory 构造某 provider 的原始流式适配器（eino adapter 实现，组合根注入）。
type UpstreamFactory func(opts UpstreamOptions) (Streamer, error)

// Manager 管理每 provider 一套受保护 Client——bulkhead 槽位与熔断状态按 provider 独立。
// 惰性创建 + 缓存：同一 provider 的调用共享同一套槽位与熔断。
type Manager struct {
	mu          sync.Mutex
	clients     map[string]*Client
	profiles    map[string]Profile // key → Profile 覆盖（DB provider 配置）
	newUpstream UpstreamFactory
}

// NewManager 创建 Manager；newUpstream 为 nil 时（adapter 未实现）Client 调用返回明确错误。
func NewManager(newUpstream UpstreamFactory) *Manager {
	return &Manager{
		clients:     make(map[string]*Client),
		profiles:    make(map[string]Profile),
		newUpstream: newUpstream,
	}
}

// Client 获取 key 标识的 provider 的受保护客户端；首次调用创建，后续复用同一套槽位与熔断状态。
func (m *Manager) Client(key string, opts UpstreamOptions) (*Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.clients[key]; ok {
		return c, nil
	}
	if m.newUpstream == nil {
		return nil, errors.New("llm: upstream factory not injected (eino adapter pending)")
	}
	up, err := m.newUpstream(opts)
	if err != nil {
		return nil, fmt.Errorf("create upstream for %s: %w", key, err)
	}
	p := ProfileForKind(opts.Kind)
	if override, ok := m.profiles[key]; ok {
		p = override
	}
	c := NewClient(key, p, up)
	m.clients[key] = c
	return c, nil
}

// SetProfile 覆盖某 provider 的 Profile（DB 配置加载后调用；已创建的 client 不重建，新 key 生效）。
func (m *Manager) SetProfile(key string, p Profile) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.profiles[key] = p
}
