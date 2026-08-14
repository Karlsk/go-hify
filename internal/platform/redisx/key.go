package redisx

import "strings"

// 全仓库 Redis key 命名空间约定（CLAUDE.md《部署架构》：统一 hify: 前缀）。
//
// 所有业务 key 必须经 [Key] 拼接，避免 Redis 被多系统共享时键冲突 / 误删：
//
//   - session：      Key("session", token)        → hify:session:{token}
//   - 配置类缓存：    Key("cache", name, key)       → hify:cache:{name}:{key}（platform/cache 包封装）
//   - 限流/预算计数： Key("ratelimit", ...)         → hify:ratelimit:...
//   - 语义缓存：      Key("semantic", ...)          → hify:semantic:...

// KeyPrefix 全仓库 Redis key 统一前缀。
const KeyPrefix = "hify"

// Key 拼接带统一前缀的 Redis key：Key("session", token) → "hify:session:{token}"。
// 不传 parts 时仅返回前缀 "hify"。
func Key(parts ...string) string {
	return strings.Join(append([]string{KeyPrefix}, parts...), ":")
}
