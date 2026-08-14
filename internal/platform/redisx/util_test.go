package redisx

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// util.go 测试：基础命令包装（Get/Set/Delete/Expire）+ JSON 序列化包装
// （SetStruct/GetStruct）+ 原子计数器（IncrWithExpire）。miniredis 真实切面。

// newTestClient 建 miniredis + go-redis client（本文件各测试共用）。
func newTestClient(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	return rdb, mr
}

// --- Get / Set ---

func TestGetSet(t *testing.T) {
	rdb, mr := newTestClient(t)
	ctx := context.Background()

	// Set 存值 + TTL。
	if err := Set(ctx, rdb, "k1", "v1", 10*time.Second); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got, err := mr.Get("k1"); err != nil || got != "v1" {
		t.Fatalf("raw value = %q (err=%v), want v1", got, err)
	}
	if d := mr.TTL("k1"); d != 10*time.Second {
		t.Fatalf("TTL = %v, want 10s", d)
	}

	// Get 命中。
	if got, err := Get(ctx, rdb, "k1"); err != nil || got != "v1" {
		t.Fatalf("Get = (%q, %v), want (v1, nil)", got, err)
	}

	// Get miss → redis.Nil（调用方按 errors.Is 判 miss）。
	if _, err := Get(ctx, rdb, "missing"); !errors.Is(err, redis.Nil) {
		t.Fatalf("Get missing err = %v, want redis.Nil", err)
	}
}

// --- Delete / Expire ---

func TestDelete(t *testing.T) {
	rdb, _ := newTestClient(t)
	ctx := context.Background()

	if err := Set(ctx, rdb, "d1", "x", time.Minute); err != nil {
		t.Fatalf("Set d1: %v", err)
	}
	if err := Set(ctx, rdb, "d2", "y", time.Minute); err != nil {
		t.Fatalf("Set d2: %v", err)
	}
	// 一次删多个。
	if err := Delete(ctx, rdb, "d1", "d2"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := Get(ctx, rdb, "d1"); !errors.Is(err, redis.Nil) {
		t.Fatalf("d1 after delete err = %v, want redis.Nil", err)
	}
	// 幂等：删不存在的 key 不报错。
	if err := Delete(ctx, rdb, "d1"); err != nil {
		t.Fatalf("Delete non-existent: %v", err)
	}
}

func TestExpire(t *testing.T) {
	rdb, mr := newTestClient(t)
	ctx := context.Background()

	// 直接经 miniredis 放一个无 TTL 的 key，再用 Expire 补。
	mr.Set("e1", "v")
	if d := mr.TTL("e1"); d != 0 {
		t.Fatalf("precondition: TTL = %v, want 0", d)
	}
	if err := Expire(ctx, rdb, "e1", 5*time.Second); err != nil {
		t.Fatalf("Expire: %v", err)
	}
	if d := mr.TTL("e1"); d != 5*time.Second {
		t.Fatalf("TTL after Expire = %v, want 5s", d)
	}
}

// --- SetStruct / GetStruct ---

type testPayload struct {
	ID   uint64
	Name string
}

func TestSetGetStruct(t *testing.T) {
	rdb, mr := newTestClient(t)
	ctx := context.Background()

	// SetStruct 序列化为 JSON 存储。
	want := testPayload{ID: 7, Name: "gpt-4o"}
	if err := SetStruct(ctx, rdb, "s1", want, time.Minute); err != nil {
		t.Fatalf("SetStruct: %v", err)
	}
	if raw, err := mr.Get("s1"); err != nil || raw != `{"ID":7,"Name":"gpt-4o"}` {
		t.Fatalf("raw = %q (err=%v), want JSON", raw, err)
	}

	// GetStruct 命中 → found=true + 写入 dst。
	var got testPayload
	found, err := GetStruct(ctx, rdb, "s1", &got)
	if err != nil || !found {
		t.Fatalf("GetStruct = (%v, %v), want (true, nil)", found, err)
	}
	if got != want {
		t.Fatalf("round-trip = %+v, want %+v", got, want)
	}

	// GetStruct miss → found=false、err=nil（miss 不算错误）。
	var miss testPayload
	found, err = GetStruct(ctx, rdb, "missing", &miss)
	if err != nil || found {
		t.Fatalf("GetStruct missing = (%v, %v), want (false, nil)", found, err)
	}
}

func TestSetStruct_MarshalError(t *testing.T) {
	rdb, _ := newTestClient(t)
	// chan 无法 JSON 序列化 → SetStruct 报 marshal 错。
	if err := SetStruct(context.Background(), rdb, "bad", make(chan int), time.Minute); err == nil {
		t.Fatal("SetStruct(chan) should error")
	}
}

func TestGetStruct_UnmarshalError(t *testing.T) {
	rdb, _ := newTestClient(t)
	ctx := context.Background()
	// 存入非 JSON 原文，GetStruct 反序列化应报错。
	if err := Set(ctx, rdb, "corrupt", "not-json", time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	var dst testPayload
	if _, err := GetStruct(ctx, rdb, "corrupt", &dst); err == nil {
		t.Fatal("GetStruct(corrupt) should error")
	}
}

func TestGetStruct_ConnectionError(t *testing.T) {
	mr := miniredis.RunT(t)
	// MaxRetries=0：断连后一次 dial 即返回，避免 go-redis 默认重试刷屏。
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: 0})
	t.Cleanup(func() { rdb.Close() })
	mr.Close() // 断连 → 命令返回非 Nil 错误。
	var dst testPayload
	if _, err := GetStruct(context.Background(), rdb, "x", &dst); err == nil {
		t.Fatal("GetStruct on closed server should error")
	}
}

// --- IncrWithExpire ---

func TestIncrWithExpire(t *testing.T) {
	rdb, mr := newTestClient(t)
	ctx := context.Background()
	const key = "counter"

	// 窗口内首次：计数 1 + 设 TTL。
	n, err := IncrWithExpire(ctx, rdb, key, 10*time.Second)
	if err != nil || n != 1 {
		t.Fatalf("first incr = (%d, %v), want (1, nil)", n, err)
	}
	if d := mr.TTL(key); d != 10*time.Second {
		t.Fatalf("TTL after first incr = %v, want 10s", d)
	}

	// 窗口推进 3s 后第二次：计数 2，且 TTL 不重置（仍是剩余 7s，不是回满 10s）。
	mr.FastForward(3 * time.Second)
	n, err = IncrWithExpire(ctx, rdb, key, 10*time.Second)
	if err != nil || n != 2 {
		t.Fatalf("second incr = (%d, %v), want (2, nil)", n, err)
	}
	if d := mr.TTL(key); d != 7*time.Second {
		t.Fatalf("TTL after second incr = %v, want 7s (must not reset)", d)
	}
}

func TestIncrWithExpire_KeysIndependent(t *testing.T) {
	rdb, _ := newTestClient(t)
	ctx := context.Background()

	if n, _ := IncrWithExpire(ctx, rdb, "a", time.Minute); n != 1 {
		t.Fatalf("a first = %d, want 1", n)
	}
	if n, _ := IncrWithExpire(ctx, rdb, "b", time.Minute); n != 1 {
		t.Fatalf("b first = %d, want 1 (独立计数)", n)
	}
	if n, _ := IncrWithExpire(ctx, rdb, "a", time.Minute); n != 2 {
		t.Fatalf("a second = %d, want 2", n)
	}
}

func TestIncrWithExpire_SubSecondTTLTruncatesToZero(t *testing.T) {
	rdb, _ := newTestClient(t)
	ctx := context.Background()

	// ttl < 1s 取整为 EXPIRE 0 → Redis 立即删 key（util.go 文档钉住的行为）。
	// 故应传 ≥ 1s 的 TTL；此测试钉住 <1s 的后果：自增成功但 key 不复存在。
	if n, err := IncrWithExpire(ctx, rdb, "sub", 500*time.Millisecond); err != nil || n != 1 {
		t.Fatalf("sub-second incr = (%d, %v), want (1, nil)", n, err)
	}
	if _, err := Get(ctx, rdb, "sub"); !errors.Is(err, redis.Nil) {
		t.Fatalf("key after EXPIRE 0 err = %v, want redis.Nil (key deleted)", err)
	}
}
