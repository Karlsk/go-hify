package redisx

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

// redisx.go 测试：Config.withDefaults 补默认 + New 初始化（连通 / 鉴权 / 不可达 fail-fast）。

func TestWithDefaults_Zero(t *testing.T) {
	// 全零 Config → 每个字段补包内默认。
	c := Config{}.withDefaults()
	if c.Addr != "localhost:6379" {
		t.Fatalf("Addr = %q, want localhost:6379", c.Addr)
	}
	if c.PoolSize != 20 {
		t.Fatalf("PoolSize = %d, want 20", c.PoolSize)
	}
	if c.MinIdleConns != 5 {
		t.Fatalf("MinIdleConns = %d, want 5", c.MinIdleConns)
	}
	if c.DialTimeout != 5*time.Second {
		t.Fatalf("DialTimeout = %v, want 5s", c.DialTimeout)
	}
	if c.ReadTimeout != 3*time.Second {
		t.Fatalf("ReadTimeout = %v, want 3s", c.ReadTimeout)
	}
	if c.WriteTimeout != 3*time.Second {
		t.Fatalf("WriteTimeout = %v, want 3s", c.WriteTimeout)
	}
}

func TestWithDefaults_Preserve(t *testing.T) {
	// 非零字段不被覆盖。
	in := Config{
		Addr:         "redis:7000",
		PoolSize:     3,
		MinIdleConns: 1,
		DialTimeout:  time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 4 * time.Second,
	}
	c := in.withDefaults()
	if c != in {
		t.Fatalf("withDefaults modified non-zero fields: %+v → %+v", in, c)
	}
}

func TestNew_Success(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb, err := New(Config{Addr: mr.Addr()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer rdb.Close()

	// 返回的 client 可用（Ping 已在 New 内通过；这里验真实读写）。
	ctx := context.Background()
	if err := rdb.Set(ctx, "k", "v", time.Minute).Err(); err != nil {
		t.Fatalf("Set via new client: %v", err)
	}
	if got, err := rdb.Get(ctx, "k").Result(); err != nil || got != "v" {
		t.Fatalf("Get via new client = (%q, %v), want (v, nil)", got, err)
	}
}

func TestNew_Auth(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.RequireAuth("secret")

	// 正确密码 → 成功。
	ok, err := New(Config{Addr: mr.Addr(), Password: "secret"})
	if err != nil {
		t.Fatalf("New with correct password: %v", err)
	}
	ok.Close()

	// 错误密码 → Ping 失败，错误带 "ping redis" 上下文，且不泄漏原始密码。
	bad, err := New(Config{Addr: mr.Addr(), Password: "wrong"})
	if err == nil {
		bad.Close()
		t.Fatal("New with wrong password should fail")
	}
	if !strings.Contains(err.Error(), "ping redis") {
		t.Fatalf("err = %v, want wrapped with 'ping redis'", err)
	}
}

func TestNew_Unreachable(t *testing.T) {
	// 不可达地址 → DialTimeout 内 fail-fast（短超时避免拖测试）。
	start := time.Now()
	_, err := New(Config{Addr: "127.0.0.1:1", DialTimeout: 500 * time.Millisecond})
	if err == nil {
		t.Fatal("New to unreachable addr should fail")
	}
	if !strings.Contains(err.Error(), "ping redis") {
		t.Fatalf("err = %v, want wrapped with 'ping redis'", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("fail-fast took %v, want bounded by DialTimeout", elapsed)
	}
}
