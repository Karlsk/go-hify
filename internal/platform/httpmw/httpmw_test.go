package httpmw

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Karlsk/go-hify/internal/platform/traceid"
)

// httpmw 测试：RequestID 注入 ctx + 响应头；AccessLog 字段齐全、慢请求 WARN、
// SSE 流豁免慢判定、SkipPaths 跳过；trace_id 经 ctx 传入日志。

var hexPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// captureHandler 收集 slog 记录与对应 ctx，供断言字段 / 级别 / trace 传递。
type captureHandler struct {
	mu      sync.Mutex
	records []slog.Record
	ctxs    []context.Context
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *captureHandler) Handle(ctx context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	h.ctxs = append(h.ctxs, ctx)
	return nil
}
func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func (h *captureHandler) snapshot() ([]slog.Record, []context.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.records, h.ctxs
}

func useCaptureLogger(t *testing.T) *captureHandler {
	t.Helper()
	cap := &captureHandler{}
	slog.SetDefault(slog.New(cap))
	t.Cleanup(func() { slog.SetDefault(slog.New(slog.DiscardHandler)) })
	return cap
}

func newEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	return gin.New()
}

// attrStr 取记录中指定 key 属性的字符串值；不存在返回空串。
func attrStr(r slog.Record, key string) string {
	v := ""
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			v = a.Value.String()
			return false
		}
		return true
	})
	return v
}

func TestRequestID_HeaderAndCtx(t *testing.T) {
	r := newEngine()
	r.Use(RequestID())
	var got string
	r.GET("/probe", func(c *gin.Context) {
		id, ok := traceid.From(c.Request.Context())
		if !ok {
			t.Error("trace_id must be present in request ctx")
		}
		got = id
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/probe", nil))

	if !hexPattern.MatchString(got) {
		t.Fatalf("ctx trace_id = %q, want 32-char hex", got)
	}
	if rec.Header().Get(traceid.Header) != got {
		t.Fatalf("header %s = %q, want %q (header must mirror ctx trace_id)", traceid.Header, rec.Header().Get(traceid.Header), got)
	}

	// 第二请求必须拿到不同 id（请求级隔离）。
	got = ""
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/probe", nil))
	if got == rec.Header().Get(traceid.Header) {
		t.Fatal("second request must generate a fresh trace_id")
	}
}

func TestAccessLog_FieldsAndTrace(t *testing.T) {
	cap := useCaptureLogger(t)
	r := newEngine()
	r.Use(RequestID(), AccessLog(AccessLogConfig{}))
	r.GET("/items", func(c *gin.Context) { c.String(http.StatusCreated, "ok") })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/items", nil))

	records, ctxs := cap.snapshot()
	if len(records) != 1 {
		t.Fatalf("logged %d records, want 1", len(records))
	}
	recd := records[0]
	if recd.Level != slog.LevelInfo || recd.Message != "http request" {
		t.Fatalf("level/msg = %v/%q, want INFO/http request", recd.Level, recd.Message)
	}
	cases := []struct{ key, want string }{
		{"method", http.MethodGet},
		{"path", "/items"},
		{"status", strconv.Itoa(http.StatusCreated)},
	}
	for _, tc := range cases {
		if got := attrStr(recd, tc.key); got != tc.want {
			t.Errorf("attr %s = %q, want %q", tc.key, got, tc.want)
		}
	}
	if ms, err := strconv.ParseInt(attrStr(recd, "duration_ms"), 10, 64); err != nil || ms < 0 {
		t.Errorf("duration_ms = %q, want non-negative int", attrStr(recd, "duration_ms"))
	}
	if attrStr(recd, "client_ip") == "" {
		t.Error("client_ip must be logged")
	}
	// trace_id 不在 AccessLog 里显式拼字段——经 ctx 传给 handler，由 logging trace 包装追加。
	if id, ok := traceid.From(ctxs[0]); !ok || id != rec.Header().Get(traceid.Header) {
		t.Errorf("log ctx trace_id = (%q,%v), want header value %q", id, ok, rec.Header().Get(traceid.Header))
	}
}

func TestAccessLog_SlowRequestWarns(t *testing.T) {
	cap := useCaptureLogger(t)
	r := newEngine()
	r.Use(AccessLog(AccessLogConfig{SlowThreshold: time.Millisecond}))
	r.GET("/slow", func(c *gin.Context) {
		time.Sleep(5 * time.Millisecond)
		c.Status(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/slow", nil))

	records, _ := cap.snapshot()
	if len(records) != 1 {
		t.Fatalf("logged %d records, want 1", len(records))
	}
	if records[0].Level != slog.LevelWarn || records[0].Message != "slow http request" {
		t.Fatalf("level/msg = %v/%q, want WARN/slow http request", records[0].Level, records[0].Message)
	}
}

func TestAccessLog_SSEStreamExempt(t *testing.T) {
	cap := useCaptureLogger(t)
	r := newEngine()
	r.Use(AccessLog(AccessLogConfig{SlowThreshold: time.Millisecond}))
	r.POST("/chat/stream", func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream") // SSE 设计上长连接 30s–5min
		time.Sleep(5 * time.Millisecond)
		c.Status(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/chat/stream", nil))

	records, _ := cap.snapshot()
	if len(records) != 1 {
		t.Fatalf("logged %d records, want 1", len(records))
	}
	if records[0].Level != slog.LevelInfo {
		t.Fatalf("SSE stream level = %v, want INFO (exempt from slow WARN by design)", records[0].Level)
	}
}

func TestAccessLog_SkipPaths(t *testing.T) {
	cap := useCaptureLogger(t)
	r := newEngine()
	r.Use(AccessLog(AccessLogConfig{SkipPaths: []string{"/health"}}))
	r.GET("/health", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/api", func(c *gin.Context) { c.Status(http.StatusOK) })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if records, _ := cap.snapshot(); len(records) != 0 {
		t.Fatalf("health check logged %d records, want 0 (healthcheck spam)", len(records))
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api", nil))
	if records, _ := cap.snapshot(); len(records) != 1 {
		t.Fatalf("non-skipped path logged %d records, want 1", len(records))
	}
}

func TestDefaultSlowThreshold(t *testing.T) {
	if DefaultSlowThreshold != time.Second {
		t.Fatalf("DefaultSlowThreshold = %v, want 1s", DefaultSlowThreshold)
	}
}
