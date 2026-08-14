package respond

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Karlsk/go-hify/internal/platform/errs"
)

// recovery.go 的 panic 兜底测试：普通 panic → 500、ErrAbortHandler 原样 re-panic、
// 响应已在途时不覆盖。

func newRecoveryEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Recovery())
	return r
}

func TestRecovery_PanicWrites500(t *testing.T) {
	r := newRecoveryEngine()
	r.GET("/boom", func(c *gin.Context) { panic("kaboom") })

	rec := serve(t, r, http.MethodGet, "/boom", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	env := parseEnv(t, rec)
	if env.Success {
		t.Fatal("success should be false")
	}
	if env.Error == nil || env.Error.Code != errs.ErrInternal.Error() {
		t.Fatalf("error = %+v, want %s", env.Error, errs.ErrInternal.Error())
	}
	if env.Error.Message != "internal server error" {
		t.Fatalf("message = %q, must not leak panic value to client", env.Error.Message)
	}
}

func TestRecovery_AbortHandlerRepanic(t *testing.T) {
	r := newRecoveryEngine()
	r.GET("/abort", func(c *gin.Context) { panic(http.ErrAbortHandler) })

	// ErrAbortHandler 被 Recovery 原样 re-panic（保留 net/http 中断语义）：在此 recover 断言其值。
	req := httptest.NewRequest(http.MethodGet, "/abort", nil)
	rec := httptest.NewRecorder()
	got := func() (recovered any) {
		defer func() { recovered = recover() }()
		r.ServeHTTP(rec, req)
		return
	}()
	if got != http.ErrAbortHandler {
		t.Fatalf("recovered = %v, want http.ErrAbortHandler", got)
	}
}

func TestRecovery_AlreadyWritten(t *testing.T) {
	r := newRecoveryEngine()
	r.GET("/sse", func(c *gin.Context) {
		c.Writer.Write([]byte("partial")) // 标记 Written()=true（SSE 流中途 panic 场景）
		panic("mid-stream")
	})

	rec := serve(t, r, http.MethodGet, "/sse", "")
	// Written()=true 分支：不覆盖已写内容，body 仍是 "partial"，不是 500 信封。
	if rec.Body.String() != "partial" {
		t.Fatalf("body = %q, want partial (in-flight response must not be overwritten)", rec.Body.String())
	}
}
