package cache

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// cache 包测试：TTL 按名解析、key 前缀格式、Get/Set/Delete 行为、按名 TTL 真实生效。
// 用 miniredis 做真实 Redis 切面（可查 TTL / 原始 key / 原始值）。

// widget 测试用缓存载荷。
type widget struct {
	ID   uint64
	Name string
}

// newTestCache 建 miniredis + go-redis client + 指定配置的 Cache。
func newTestCache(t *testing.T, cfg Config) (*Cache, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	return New(rdb, cfg), mr
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.DefaultTTL != DefaultTTL {
		t.Fatalf("DefaultTTL = %v, want %v", cfg.DefaultTTL, DefaultTTL)
	}
	// provider-cache / agent-cache / rag-cache 均显式注册为 30 min。
	if cfg.TTLs[NameProvider] != DefaultTTL || cfg.TTLs[NameAgent] != DefaultTTL || cfg.TTLs[NameRag] != DefaultTTL {
		t.Fatalf("TTLs = %+v, want all %v", cfg.TTLs, DefaultTTL)
	}
	if NameRag != "rag-cache" {
		t.Fatalf("NameRag = %q, want rag-cache", NameRag)
	}
}

func TestTTL_Resolution(t *testing.T) {
	c, _ := newTestCache(t, Config{
		DefaultTTL: 20 * time.Minute,
		TTLs: map[string]time.Duration{
			"short-cache": 1 * time.Minute, // 覆盖项
		},
	})

	cases := []struct {
		name string
		want time.Duration
	}{
		{"short-cache", 1 * time.Minute},     // 覆盖优先
		{"provider-cache", 20 * time.Minute}, // 无覆盖 → 默认
		{"anything-else", 20 * time.Minute},  // 无覆盖 → 默认
	}
	for _, tc := range cases {
		if got := c.TTL(tc.name); got != tc.want {
			t.Fatalf("TTL(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestTTL_DefaultTTLZero(t *testing.T) {
	// DefaultTTL=0 时回退到包默认 30 min。
	c, _ := newTestCache(t, Config{})
	if got := c.TTL("anything"); got != DefaultTTL {
		t.Fatalf("TTL = %v, want default %v", got, DefaultTTL)
	}
}

func TestKeyPrefix(t *testing.T) {
	c, _ := newTestCache(t, DefaultConfig())
	// 白盒：直接验 key 拼接（hify:cache:{name}:{key}）。
	if got := c.key(NameProvider, "42"); got != "hify:cache:provider-cache:42" {
		t.Fatalf("key = %q, want hify:cache:provider-cache:42", got)
	}
}

// TestSetGet_RoundTrip Set 后 Get 命中且值正确；Get 未命中返回 found=false。
func TestSetGet_RoundTrip(t *testing.T) {
	c, _ := newTestCache(t, DefaultConfig())
	ctx := context.Background()

	// miss：未写先取。
	var got widget
	if found, err := c.Get(ctx, NameAgent, "a1", &got); err != nil || found {
		t.Fatalf("Get before Set: found=%v err=%v, want false/nil", found, err)
	}

	// Set 后命中。
	want := widget{ID: 7, Name: "gpt-4o"}
	if err := c.Set(ctx, NameAgent, "a1", want); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if found, err := c.Get(ctx, NameAgent, "a1", &got); err != nil || !found {
		t.Fatalf("Get after Set: found=%v err=%v, want true/nil", found, err)
	}
	if got != want {
		t.Fatalf("round-trip = %+v, want %+v", got, want)
	}
}

// TestDelete_AfterSet Set 后 Delete，再 Get 应 miss（写时删 key 路径）。
func TestDelete_AfterSet(t *testing.T) {
	c, _ := newTestCache(t, DefaultConfig())
	ctx := context.Background()

	if err := c.Set(ctx, NameProvider, "p1", widget{ID: 1, Name: "openai"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := c.Delete(ctx, NameProvider, "p1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	var got widget
	if found, err := c.Get(ctx, NameProvider, "p1", &got); err != nil || found {
		t.Fatalf("Get after Delete: found=%v err=%v, want false/nil", found, err)
	}
}

// TestTTL_AppliedAsExpected 按名 TTL 真实写入 Redis：默认名落 30 min、覆盖名落其值。
func TestTTL_AppliedAsExpected(t *testing.T) {
	c, mr := newTestCache(t, Config{
		DefaultTTL: 30 * time.Minute,
		TTLs: map[string]time.Duration{
			"quick-cache": 10 * time.Second, // 覆盖
		},
	})
	ctx := context.Background()

	// 默认名：30 min。
	if err := c.Set(ctx, "provider-cache", "k1", widget{ID: 1}); err != nil {
		t.Fatalf("Set default: %v", err)
	}
	assertTTL(t, mr, "hify:cache:provider-cache:k1", 30*time.Minute)

	// 覆盖名：10 s。
	if err := c.Set(ctx, "quick-cache", "k2", widget{ID: 2}); err != nil {
		t.Fatalf("Set override: %v", err)
	}
	assertTTL(t, mr, "hify:cache:quick-cache:k2", 10*time.Second)
}

// assertTTL 断言 miniredis 中 key 的 TTL 在期望值附近（≤ expected 且 > expected-1s，防漂移误判）。
func assertTTL(t *testing.T, mr *miniredis.Miniredis, key string, expected time.Duration) {
	t.Helper()
	d := mr.TTL(key)
	if d <= 0 {
		t.Fatalf("TTL(%q) = %v, want a positive TTL", key, d)
	}
	if d > expected {
		t.Fatalf("TTL(%q) = %v, want ≤ %v", key, d, expected)
	}
	if slack := expected - d; slack > time.Second {
		t.Fatalf("TTL(%q) = %v, want ≈ %v (slack %v)", key, d, expected, slack)
	}
}

// TestSetStoresAtPrefixedKey 验证 Set 把数据落到 hify:cache: 前缀的 key（白盒查 miniredis 原始 key）。
func TestSetStoresAtPrefixedKey(t *testing.T) {
	c, mr := newTestCache(t, DefaultConfig())
	ctx := context.Background()

	if err := c.Set(ctx, NameProvider, "99", widget{ID: 99, Name: "claude"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	raw, err := mr.Get("hify:cache:provider-cache:99")
	if err != nil {
		t.Fatalf("expected key hify:cache:provider-cache:99 to exist: %v", err)
	}
	// 值是 JSON 序列化（含 ID/Name）。
	if want := `{"ID":99,"Name":"claude"}`; raw != want {
		t.Fatalf("raw value = %s, want %s", raw, want)
	}
}
