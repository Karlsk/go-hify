package respond

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Karlsk/go-hify/internal/platform/errs"
)

// respond.go 信封 writers 测试：OK / Created / OKWithMeta / Fail / FailWithDetails /
// BadRequest / NotFound / Error——覆盖状态码、信封结构、no-store 头。
// 本文件还定义包内共享的 serve / parseEnv（被 pagination/recovery/errors 的 *_test.go 复用）。

// serve 发请求并返回 recorder（本包各 *_test.go 共用）。
func serve(t *testing.T, r *gin.Engine, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// fullEnvelope 是带 Data/Meta 的完整信封（用于断言成功响应的载荷）。
type fullEnvelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   *struct {
		Code    string          `json:"code"`
		Message string          `json:"message"`
		Details json.RawMessage `json:"details"`
	} `json:"error"`
	Meta json.RawMessage `json:"meta"`
}

// parseEnv 解析 recorder body 为 fullEnvelope。
func parseEnv(t *testing.T, rec *httptest.ResponseRecorder) fullEnvelope {
	t.Helper()
	var env fullEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal %q: %v", rec.Body.String(), err)
	}
	return env
}

// isJSONNull 报 RawMessage 是否为 JSON null（Go nil 字段序列化为 "null"）。
func isJSONNull(raw json.RawMessage) bool { return string(raw) == "null" || len(raw) == 0 }

func TestOK(t *testing.T) {
	r := newTestEngine(t)
	r.GET("/x", func(c *gin.Context) { OK(c, "hi") })

	rec := serve(t, r, http.MethodGet, "/x", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	env := parseEnv(t, rec)
	if !env.Success {
		t.Fatal("success should be true")
	}
	var s string
	if err := json.Unmarshal(env.Data, &s); err != nil || s != "hi" {
		t.Fatalf("data = %s, want \"hi\"", env.Data)
	}
	if !isJSONNull(env.Meta) {
		t.Fatalf("meta = %s, want null", env.Meta)
	}
}

func TestCreated(t *testing.T) {
	r := newTestEngine(t)
	r.GET("/x", func(c *gin.Context) { Created(c, gin.H{"id": 1}) })

	rec := serve(t, r, http.MethodGet, "/x", "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	env := parseEnv(t, rec)
	if !env.Success {
		t.Fatal("success should be true")
	}
}

func TestOKWithMeta(t *testing.T) {
	r := newTestEngine(t)
	r.GET("/x", func(c *gin.Context) { OKWithMeta(c, []int{1, 2}, gin.H{"page": 1}) })

	rec := serve(t, r, http.MethodGet, "/x", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	env := parseEnv(t, rec)
	if isJSONNull(env.Meta) {
		t.Fatal("meta should be present")
	}
	if !strings.Contains(string(env.Meta), "page") {
		t.Fatalf("meta = %s, want page field", env.Meta)
	}
}

func TestFail(t *testing.T) {
	r := newTestEngine(t)
	r.GET("/x", func(c *gin.Context) { Fail(c, http.StatusConflict, "CONFLICT", "dup name") })

	rec := serve(t, r, http.MethodGet, "/x", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	env := parseEnv(t, rec)
	if env.Success {
		t.Fatal("success should be false")
	}
	if env.Error == nil || env.Error.Code != "CONFLICT" || env.Error.Message != "dup name" {
		t.Fatalf("error = %+v", env.Error)
	}
	if !isJSONNull(env.Error.Details) {
		t.Fatalf("details = %s, want null", env.Error.Details)
	}
}

func TestFailWithDetails(t *testing.T) {
	r := newTestEngine(t)
	r.GET("/x", func(c *gin.Context) {
		FailWithDetails(c, http.StatusBadRequest, "X", "bad", gin.H{"fields": "info"})
	})

	rec := serve(t, r, http.MethodGet, "/x", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
	env := parseEnv(t, rec)
	if env.Error == nil || env.Error.Code != "X" {
		t.Fatalf("error = %+v", env.Error)
	}
	if !strings.Contains(string(env.Error.Details), "fields") {
		t.Fatalf("details = %s, want fields", env.Error.Details)
	}
}

func TestBadRequest(t *testing.T) {
	r := newTestEngine(t)
	r.GET("/x", func(c *gin.Context) { BadRequest(c, "bad input") })

	rec := serve(t, r, http.MethodGet, "/x", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	env := parseEnv(t, rec)
	if env.Error == nil || env.Error.Code != errs.ErrValidationFailed.Error() {
		t.Fatalf("error = %+v", env.Error)
	}
}

func TestNotFound(t *testing.T) {
	r := newTestEngine(t)
	r.GET("/x", func(c *gin.Context) { NotFound(c, "missing") })

	rec := serve(t, r, http.MethodGet, "/x", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	env := parseEnv(t, rec)
	if env.Error == nil || env.Error.Code != CodeNotFound {
		t.Fatalf("error = %+v", env.Error)
	}
}

func TestError(t *testing.T) {
	r := newTestEngine(t)
	r.GET("/x", func(c *gin.Context) { Error(c, errors.New("boom")) })

	rec := serve(t, r, http.MethodGet, "/x", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	env := parseEnv(t, rec)
	if env.Error == nil || env.Error.Code != errs.ErrInternal.Error() {
		t.Fatalf("error = %+v", env.Error)
	}
	if env.Error.Message != "internal server error" {
		t.Fatalf("message = %q, must not leak internal detail", env.Error.Message)
	}
}
