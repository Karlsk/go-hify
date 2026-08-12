package redisx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// 本文件是 go-redis 之上的薄封装：基础命令统一 Result()/Err() 处理 + JSON 序列化包装
// + 原子计数器。业务代码调 redisx.SetStruct 等即可，不必每次手写 json.Marshal / redis.Nil 判空
// （CLAUDE.md：让业务代码不必每次手写）。
//
// GetStruct 的 found bool、IncrWithExpire 的 Lua 原子性，是 go-redis 原生 API 没直接给的增值部分。

// Get 取 String 原始值。key 不存在返回 redis.Nil——调用方按 errors.Is(err, redis.Nil) 判 miss。
func Get(ctx context.Context, rdb *redis.Client, key string) (string, error) {
	return rdb.Get(ctx, key).Result()
}

// Set 存 String 值 + TTL。所有 key 必须有 TTL（CLAUDE.md：不留永久 key）。
func Set(ctx context.Context, rdb *redis.Client, key, val string, ttl time.Duration) error {
	return rdb.Set(ctx, key, val, ttl).Err()
}

// Delete 删 key（可多个）。配置类缓存更新路径用「更新 DB → 删 key」，而非「更新 DB → 写 key」
// （CLAUDE.md §缓存策略：写时删 key，避免并发下脏缓存）。
func Delete(ctx context.Context, rdb *redis.Client, keys ...string) error {
	return rdb.Del(ctx, keys...).Err()
}

// Expire 给已存在的 key 单独设 TTL。
func Expire(ctx context.Context, rdb *redis.Client, key string, ttl time.Duration) error {
	return rdb.Expire(ctx, key, ttl).Err()
}

// SetStruct 把任意值 JSON 序列化后存（对应 Java RedisTemplate 的 Jackson 序列化器）。
func SetStruct(ctx context.Context, rdb *redis.Client, key string, val any, ttl time.Duration) error {
	b, err := json.Marshal(val)
	if err != nil {
		return fmt.Errorf("redisx.SetStruct marshal: %w", err)
	}
	return rdb.Set(ctx, key, b, ttl).Err()
}

// GetStruct 取出 + JSON 反序列化到 dst。
// found=false 表示 key 不存在（miss 不计错误）；found=true 表示命中且已写入 dst。
func GetStruct(ctx context.Context, rdb *redis.Client, key string, dst any) (bool, error) {
	s, err := rdb.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal([]byte(s), dst); err != nil {
		return false, fmt.Errorf("redisx.GetStruct unmarshal: %w", err)
	}
	return true, nil
}

// incrExpireScript 原子「自增 + 首次设 TTL」：INCR 后若结果为 1（窗口内首次），EXPIRE。
// 之所以用 Lua：INCR 与 EXPIRE 是两条命令，分开发送不是真原子——进程在两步之间崩溃会留下
// 无 TTL 的永久 key（CLAUDE.md 明令不留永久 key），且会令计数器永不复位、限流失效。
// 单条 Lua 由 Redis 单线程原子执行，规避此竞态（CLAUDE.md：「INCR + 过期 原子操作」）。
const incrExpireScript = `
local c = redis.call('INCR', KEYS[1])
if c == 1 then
  redis.call('EXPIRE', KEYS[1], ARGV[1])
end
return c
`

// IncrWithExpire 原子自增，并在窗口内首次自增时设 TTL（固定窗口计数器：限流 / 预算计数的基础原语）。
// ttl 按秒取整下传给 EXPIRE，故应 ≥ 1s（<1s 会取整为 0，Redis 视 EXPIRE 0 为立即删除）。
// 返回当前窗口内累计计数（调用方据此判是否超阈值）。
func IncrWithExpire(ctx context.Context, rdb *redis.Client, key string, ttl time.Duration) (int64, error) {
	return rdb.Eval(ctx, incrExpireScript, []string{key}, int64(ttl.Seconds())).Int64()
}

// 业务约定 TTL / 窗口（CLAUDE.md §缓存策略）。
//
// 注意（关注点分离）：语义缓存、限流是特定业务域的语义，其常量本应归对应模块所有。
// 当前 chat/rag、platform/budget 模块尚未建，暂居本 infra 层；模块落地时迁回各自归属处。
const (
	// ConfigCacheTTL 配置类 Cache-Aside 默认 TTL（CLAUDE.md：「TTL 5 分钟 + 写时删 key」），
	// 跨模块通用的 infra 约定，留在本层合理。
	ConfigCacheTTL = 5 * time.Minute
	// SemanticCacheTTL 语义缓存答案 TTL（暂定 24h）→ 归属 chat/rag。
	SemanticCacheTTL = 24 * time.Hour
	// RateLimitWindow 限流计数固定窗口（暂定 60s）→ 归属 platform/budget。
	RateLimitWindow = 60 * time.Second
)
