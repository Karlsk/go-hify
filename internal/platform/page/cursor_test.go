package page

import (
	"encoding/base64"
	"reflect"
	"testing"
	"time"
)

// 游标分页的参数归一化、FetchN、编解码 round-trip、NewCursorResult 三态（has_more 截断 / 无更多 / 空集）。

// convKey 模拟 conversations 的游标排序键（updated_at, id）——演示表特定 key 类型。
type convKey struct {
	UpdatedAt time.Time
	ID        uint64
}

func TestNewCursorNormalization(t *testing.T) {
	tests := []struct {
		name      string
		limit     int
		cursor    string
		wantLimit int
	}{
		{"limit below 1", 0, "", DefaultPageSize},
		{"limit negative", -5, "abc", DefaultPageSize},
		{"limit over max", 999, "", MaxPageSize},
		{"valid with cursor", 50, "eyJpZCI6MX0", 50},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewCursor(tt.limit, tt.cursor)
			if got.Limit != tt.wantLimit {
				t.Fatalf("limit = %d, want %d", got.Limit, tt.wantLimit)
			}
			if got.Cursor != tt.cursor {
				t.Fatalf("cursor = %q, want preserved %q", got.Cursor, tt.cursor)
			}
		})
	}
}

func TestCursorFetchN(t *testing.T) {
	p := CursorParams{Limit: 20, Cursor: ""}
	if got := p.FetchN(); got != 21 {
		t.Fatalf("FetchN() = %d, want 21", got)
	}
	if p.Limit != 20 { // 字段直接访问（CursorParams.Limit 是字段，非方法）
		t.Fatalf("Limit = %d, want 20", p.Limit)
	}
}

func TestEncodeDecodeCursorRoundTrip(t *testing.T) {
	now := time.Now()
	key := convKey{UpdatedAt: now, ID: 42}

	encoded, err := EncodeCursor(key)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if encoded == "" {
		t.Fatal("encoded cursor empty")
	}

	decoded, err := DecodeCursor[convKey](encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !decoded.UpdatedAt.Equal(key.UpdatedAt) || decoded.ID != key.ID {
		t.Fatalf("round-trip mismatch: got %+v want %+v", decoded, key)
	}
}

func TestDecodeCursorEmpty(t *testing.T) {
	// 空 cursor 视为首页：返回零值、无错。
	got, err := DecodeCursor[convKey]("")
	if err != nil {
		t.Fatalf("empty decode err = %v", err)
	}
	var zero convKey
	if !reflect.DeepEqual(got, zero) {
		t.Fatalf("empty cursor should yield zero value, got %+v", got)
	}
}

func TestDecodeCursorInvalidBase64(t *testing.T) {
	// '!' 不在 base64 字母表内 → base64 解码失败。
	if _, err := DecodeCursor[convKey]("!!!not-base64!!!"); err == nil {
		t.Fatal("invalid base64 should error")
	}
}

func TestDecodeCursorInvalidJSON(t *testing.T) {
	// 合法 base64 但内容非 convKey 的 JSON → unmarshal 失败。
	bad := base64.RawURLEncoding.EncodeToString([]byte("not-json"))
	if _, err := DecodeCursor[convKey](bad); err == nil {
		t.Fatal("invalid json should error")
	}
}

func TestNewCursorResultHasMore(t *testing.T) {
	// 取回 limit+1 = 4 条 → has_more，截断到 3，next_cursor = encode(第 3 条的 key)。
	now := time.Now()
	items := []convKey{
		{UpdatedAt: now.Add(-1 * time.Hour), ID: 1},
		{UpdatedAt: now.Add(-2 * time.Hour), ID: 2},
		{UpdatedAt: now.Add(-3 * time.Hour), ID: 3},
		{UpdatedAt: now.Add(-4 * time.Hour), ID: 4}, // 多取的，应被截断
	}
	res, err := NewCursorResult(items, 3, func(c convKey) convKey { return c })
	if err != nil {
		t.Fatalf("new result: %v", err)
	}
	if !res.HasMore {
		t.Fatal("HasMore should be true")
	}
	if len(res.Items) != 3 {
		t.Fatalf("items len = %d, want 3", len(res.Items))
	}
	if res.Items[2].ID != 3 {
		t.Fatalf("last kept item = %d, want 3", res.Items[2].ID)
	}
	// next_cursor 应等于截断后最后一条（第 3 条）的 key 编码。
	want, _ := EncodeCursor(items[2])
	if res.NextCursor != want {
		t.Fatalf("next cursor = %q, want %q", res.NextCursor, want)
	}
}

func TestNewCursorResultNoMore(t *testing.T) {
	// 取回 limit = 3 条 → 无更多，next_cursor=""。
	items := []convKey{{ID: 1}, {ID: 2}, {ID: 3}}
	res, err := NewCursorResult(items, 3, func(c convKey) convKey { return c })
	if err != nil {
		t.Fatalf("new result: %v", err)
	}
	if res.HasMore {
		t.Fatal("HasMore should be false")
	}
	if len(res.Items) != 3 {
		t.Fatalf("items len = %d, want 3", len(res.Items))
	}
	if res.NextCursor != "" {
		t.Fatalf("next cursor = %q, want empty", res.NextCursor)
	}
}

func TestNewCursorResultExactlyLimit(t *testing.T) {
	// 恰好 limit 条：边界，HasMore=false。
	items := []convKey{{ID: 1}, {ID: 2}}
	res, _ := NewCursorResult(items, 2, func(c convKey) convKey { return c })
	if res.HasMore {
		t.Fatal("exactly limit should be HasMore=false")
	}
}

func TestNewCursorResultEmpty(t *testing.T) {
	res, err := NewCursorResult[convKey, convKey](nil, 20, func(c convKey) convKey { return c })
	if err != nil {
		t.Fatalf("new result: %v", err)
	}
	if res.HasMore {
		t.Fatal("HasMore should be false on empty")
	}
	if res.Items == nil {
		t.Fatal("nil items must become []")
	}
	if len(res.Items) != 0 {
		t.Fatalf("len = %d, want 0", len(res.Items))
	}
	if res.NextCursor != "" {
		t.Fatalf("next cursor = %q, want empty", res.NextCursor)
	}
}
