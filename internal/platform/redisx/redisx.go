// Package redisx 初始化 go-redis 客户端（本文件）并提供通用操作工具（util.go）。
//
// 全仓库唯一的 Redis 客户端装配点：[New] 返回的 *redis.Client 注入各模块 store/service；
// handler 不直接持有客户端——Redis 调用是 platform 层职责（CLAUDE.md《部署架构》）。
// 这与 db 包一致：platform 返回具体客户端，业务侧自行定义小接口做 mock 切面。
package redisx

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Config 是 go-redis 客户端装配参数；零值字段由 [Config.withDefaults] 补默认（与 db.Config 风格一致）。
// 密码走 .env / Docker secrets，禁止入 Git（CLAUDE.md §密钥）。
type Config struct {
	Addr         string // host:port
	Username     string // Redis 6 ACL 用户名（可选）
	Password     string
	DB           int
	PoolSize     int // 连接池上限；2C 机器默认 20
	MinIdleConns int // 最小空闲连接，保温避免突发建连
	DialTimeout  time.Duration
	// ReadTimeout 单命令读超时。Hify 不用 BLPOP / SUBSCRIBE 等阻塞命令
	// （SSE 长连接走 LLM，不经 Redis），故可设短，避免 Redis 卡住拖垮调用方。
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

// withDefaults 给零值字段补默认值，返回新副本（不改原 Config）。
func (c Config) withDefaults() Config {
	if c.Addr == "" {
		c.Addr = "localhost:6379"
	}
	if c.PoolSize == 0 {
		c.PoolSize = 20
	}
	if c.MinIdleConns == 0 {
		c.MinIdleConns = 5
	}
	if c.DialTimeout == 0 {
		c.DialTimeout = 5 * time.Second
	}
	if c.ReadTimeout == 0 {
		c.ReadTimeout = 3 * time.Second
	}
	if c.WriteTimeout == 0 {
		c.WriteTimeout = 3 * time.Second
	}
	return c
}

// New 初始化 *redis.Client：建池 + Ping。go-redis 默认懒连接（NewClient 不立即建连），
// Ping 强制建连——Redis 不可达 / 密码错 / DB 号非法时此处即失败、fail-fast，不拖到首次查询。
func New(cfg Config) (*redis.Client, error) {
	cfg = cfg.withDefaults()

	rdb := redis.NewClient(&redis.Options{
		Addr:         cfg.Addr,
		Username:     cfg.Username,
		Password:     cfg.Password,
		DB:           cfg.DB,
		PoolSize:     cfg.PoolSize,
		MinIdleConns: cfg.MinIdleConns,
		DialTimeout:  cfg.DialTimeout,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	})

	ctx, cancel := context.WithTimeout(context.Background(), cfg.DialTimeout)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return rdb, nil
}
