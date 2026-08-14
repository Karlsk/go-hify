package respond

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
)

// pagination.go 的 OKWithCursor / OKWithOffset 测试。

type cursorMetaJSON struct {
	Limit      int     `json:"limit"`
	HasMore    bool    `json:"has_more"`
	NextCursor *string `json:"next_cursor"`
}

type offsetMetaJSON struct {
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
	Total    int64 `json:"total"`
}

func TestOKWithCursor_HasNext(t *testing.T) {
	r := newTestEngine(t)
	r.GET("/x", func(c *gin.Context) {
		OKWithCursor(c, []string{"a", "b"}, 20, true, "eyJpZCI6MTIzfQ")
	})

	rec := serve(t, r, http.MethodGet, "/x", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	env := parseEnv(t, rec)
	var m cursorMetaJSON
	if err := json.Unmarshal(env.Meta, &m); err != nil {
		t.Fatalf("unmarshal meta: %v", err)
	}
	if m.Limit != 20 || !m.HasMore || m.NextCursor == nil || *m.NextCursor != "eyJpZCI6MTIzfQ" {
		t.Fatalf("meta = %+v", m)
	}
}

func TestOKWithCursor_NoNext(t *testing.T) {
	r := newTestEngine(t)
	r.GET("/x", func(c *gin.Context) {
		OKWithCursor(c, []string{"a"}, 20, false, "") // 空 nextCursor → 序列化为 null
	})

	env := parseEnv(t, serve(t, r, http.MethodGet, "/x", ""))
	var m cursorMetaJSON
	if err := json.Unmarshal(env.Meta, &m); err != nil {
		t.Fatalf("unmarshal meta: %v", err)
	}
	if m.HasMore {
		t.Fatal("has_more should be false")
	}
	if m.NextCursor != nil {
		t.Fatalf("next_cursor = %v, want null (空串转 nil)", m.NextCursor)
	}
}

func TestOKWithOffset(t *testing.T) {
	r := newTestEngine(t)
	r.GET("/x", func(c *gin.Context) {
		OKWithOffset(c, []string{"a", "b"}, 2, 20, 47)
	})

	env := parseEnv(t, serve(t, r, http.MethodGet, "/x", ""))
	var m offsetMetaJSON
	if err := json.Unmarshal(env.Meta, &m); err != nil {
		t.Fatalf("unmarshal meta: %v", err)
	}
	if m.Page != 2 || m.PageSize != 20 || m.Total != 47 {
		t.Fatalf("meta = %+v, want {Page:2 PageSize:20 Total:47}", m)
	}
}
