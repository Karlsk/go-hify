package respond

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Karlsk/go-hify/internal/platform/errs"
	"github.com/Karlsk/go-hify/internal/platform/traceid"
)

// 500 路径 trace_id 测试（CLAUDE.md §错误处理：500 类只回 INTERNAL_ERROR + trace_id，
// 细节进结构化日志）：Error / Recovery 的 body message 固定文案 + trace 后缀；
// 原始 err / panic 栈以 ERROR 级进日志；无 trace ctx 时 message 不带后缀。

// logCapture 收集 slog 记录，供断言「细节进日志」。
type logCapture struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *logCapture) Enabled(context.Context, slog.Level) bool { return true }
func (h *logCapture) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}
func (h *logCapture) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *logCapture) WithGroup(string) slog.Handler      { return h }

func (h *logCapture) snapshot() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.records
}

func captureLogs(t *testing.T) *logCapture {
	t.Helper()
	cap := &logCapture{}
	slog.SetDefault(slog.New(cap))
	t.Cleanup(func() { slog.SetDefault(slog.New(slog.DiscardHandler)) })
	return cap
}

// withTrace 模拟 httpmw.RequestID 注入 trace_id（respond 不依赖 httpmw，共享 traceid 包）。
func withTrace(id string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request = c.Request.WithContext(traceid.With(c.Request.Context(), id))
		c.Next()
	}
}

// attrString 取记录中指定 key 属性的字符串值；不存在返回空串。
func attrString(r slog.Record, key string) string {
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

func TestError_TraceInMessageAndLog(t *testing.T) {
	cap := captureLogs(t)
	r := newTestEngine(t)
	r.Use(withTrace("tid-err"))
	r.GET("/x", func(c *gin.Context) { Error(c, errors.New("root cause")) })

	rec := serve(t, r, http.MethodGet, "/x", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	env := parseEnv(t, rec)
	if env.Error == nil || env.Error.Code != errs.ErrInternal.Error() {
		t.Fatalf("error = %+v, want code %s", env.Error, errs.ErrInternal.Error())
	}
	if want := "internal server error (trace tid-err)"; env.Error.Message != want {
		t.Fatalf("message = %q, want %q (CLAUDE.md：500 回 INTERNAL_ERROR + trace_id)", env.Error.Message, want)
	}
	// 原始 err 必须进日志（细节不进 body 进日志）。
	found := false
	for _, recd := range cap.snapshot() {
		if recd.Message == "internal server error" && recd.Level == slog.LevelError &&
			strings.Contains(attrString(recd, "err"), "root cause") {
			found = true
		}
	}
	if !found {
		t.Fatal("original err must be logged at ERROR level with err field")
	}
}

func TestError_NoTraceKeepsPlainMessage(t *testing.T) {
	captureLogs(t) // 收走 Error 的日志输出，避免测试噪音
	r := newTestEngine(t)
	r.GET("/x", func(c *gin.Context) { Error(c, errors.New("boom")) })

	env := parseEnv(t, serve(t, r, http.MethodGet, "/x", ""))
	if env.Error == nil || env.Error.Message != "internal server error" {
		t.Fatalf("message = %q, want plain %q when ctx has no trace", env.Error.Message, "internal server error")
	}
}

func TestRecovery_PanicTraceInMessageAndLog(t *testing.T) {
	cap := captureLogs(t)
	r := newTestEngine(t)
	r.Use(withTrace("tid-panic"), Recovery())
	r.GET("/boom", func(c *gin.Context) { panic("kaboom") })

	rec := serve(t, r, http.MethodGet, "/boom", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	env := parseEnv(t, rec)
	if want := "internal server error (trace tid-panic)"; env.Error == nil || env.Error.Message != want {
		t.Fatalf("message = %q, want %q", env.Error.Message, want)
	}
	// panic 值 + 栈进日志；panic 值绝不进 body。
	found := false
	for _, recd := range cap.snapshot() {
		if recd.Message == "panic recovered" && recd.Level == slog.LevelError &&
			strings.Contains(attrString(recd, "stack"), "goroutine") {
			found = true
		}
	}
	if !found {
		t.Fatal("panic must be logged at ERROR level with stack")
	}
	if strings.Contains(rec.Body.String(), "kaboom") {
		t.Fatal("panic value must never reach the client body")
	}
}
