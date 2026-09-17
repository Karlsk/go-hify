// Package cache 提供 Redis 配置类缓存管理器（Cache-Aside 语义）。
//
// 对应 Java Spring 的 RedisCacheManager：按缓存名注册不同 TTL、统一 key 前缀。
// 与 Spring @Cacheable 注解驱动的区别：Go 无 AOP，缓存是显式 Cache-Aside——
// service 层手动 Get → miss 则从 DB 加载 → Set；写入路径手动 Delete（写时删 key）。
//
// key 形如 hify:cache:{name}:{key}（前缀由 redisx.Key 统一拼接）。session 不走本包
// （session 是认证事实源，TTL 是业务语义「登录保持时长」，非缓存兜底窗口）。
package cache

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Karlsk/go-hify/internal/platform/redisx"
)

// 缓存名（配置类缓存，静态低频变更）。业务模块用对应常量调 Get/Set/Delete。
const (
	// NameProvider provider 配置缓存。
	NameProvider = "provider-cache"
	// NameAgent agent 配置缓存。
	NameAgent = "agent-cache"
	// NameRag rag 配置缓存（知识库配置，静态低频变更）。
	NameRag = "rag-cache"
	// NameWorkflow workflow 配置缓存（整图详情，静态低频变更）。
	NameWorkflow = "workflow-cache"
)

// TTL 约定（CLAUDE.md《部署架构》：配置类 Cache-Aside，TTL 30 分钟 + 写时删 key）。
const (
	// DefaultTTL 配置类缓存默认（及 provider / agent 缓存）TTL。
	// 写时删 key 保证强一致，TTL 只是删 key 失败时的最终一致兜底窗口。
	DefaultTTL = 30 * time.Minute
)

// Config 装配参数：默认 TTL + 按缓存名覆盖。DefaultTTL 为 0 时用包默认 30 min。
type Config struct {
	DefaultTTL time.Duration
	TTLs       map[string]time.Duration
}

// DefaultConfig 返回带已知配置类缓存名 TTL 的默认装配配置
// （provider-cache / agent-cache / rag-cache / workflow-cache 均 30 min，等于默认）。业务模块若需不同 TTL，覆盖对应项。
func DefaultConfig() Config {
	return Config{
		DefaultTTL: DefaultTTL,
		TTLs: map[string]time.Duration{
			NameProvider: DefaultTTL,
			NameAgent:    DefaultTTL,
			NameRag:      DefaultTTL,
			NameWorkflow: DefaultTTL,
		},
	}
}

// Cache 配置类缓存管理器：持有 *redis.Client，按名解析 TTL，序列化/反序列化委托 redisx。
type Cache struct {
	rdb    *redis.Client
	defTTL time.Duration
	ttls   map[string]time.Duration
}

// New 构造缓存管理器。
func New(rdb *redis.Client, cfg Config) *Cache {
	if cfg.DefaultTTL == 0 {
		cfg.DefaultTTL = DefaultTTL
	}
	return &Cache{rdb: rdb, defTTL: cfg.DefaultTTL, ttls: cfg.TTLs}
}

// TTL 返回该缓存名的 TTL（无覆盖则用默认）。
func (c *Cache) TTL(name string) time.Duration {
	if t, ok := c.ttls[name]; ok {
		return t
	}
	return c.defTTL
}

// key 拼该缓存项的全键：hify:cache:{name}:{key}。
func (c *Cache) key(name, key string) string {
	return redisx.Key("cache", name, key)
}

// Get 取缓存；found=false 表示 miss（不计错误），found=true 表示命中且已写入 dst。
func (c *Cache) Get(ctx context.Context, name, key string, dst any) (bool, error) {
	return redisx.GetStruct(ctx, c.rdb, c.key(name, key), dst)
}

// Set 写缓存，TTL 自动取该缓存名的 TTL。
func (c *Cache) Set(ctx context.Context, name, key string, val any) error {
	return redisx.SetStruct(ctx, c.rdb, c.key(name, key), val, c.TTL(name))
}

// Delete 删缓存（写时删 key：DB 更新后调，避免并发下脏缓存）。
func (c *Cache) Delete(ctx context.Context, name, key string) error {
	return redisx.Delete(ctx, c.rdb, c.key(name, key))
}
