package traceid

import (
	"context"
	"regexp"
	"testing"
)

// traceid 测试：生成格式（32 位 hex）与唯一性、ctx 注入 / 提取往返、未注入返回 false。

var hexPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

func TestGenerate_FormatAndUniqueness(t *testing.T) {
	seen := make(map[string]struct{}, 1000)
	for range 1000 {
		id := Generate()
		if !hexPattern.MatchString(id) {
			t.Fatalf("Generate() = %q, want 32-char lowercase hex", id)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate trace_id %q in 1000 generations", id)
		}
		seen[id] = struct{}{}
	}
}

func TestWithFrom_RoundTrip(t *testing.T) {
	ctx := With(context.Background(), "abc123")
	id, ok := From(ctx)
	if !ok || id != "abc123" {
		t.Fatalf("From = (%q, %v), want (abc123, true)", id, ok)
	}
}

func TestFrom_Missing(t *testing.T) {
	_, ok := From(context.Background())
	if ok {
		t.Fatal("From on bare ctx must return ok=false")
	}
}

func TestWith_Overwrite(t *testing.T) {
	// 二次注入覆盖前值（RequestID 中间件每请求只注入一次，此行为仅作语义固化）。
	ctx := With(context.Background(), "first")
	ctx = With(ctx, "second")
	id, _ := From(ctx)
	if id != "second" {
		t.Fatalf("From = %q, want second", id)
	}
}
