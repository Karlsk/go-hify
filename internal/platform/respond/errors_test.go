package respond

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Karlsk/go-hify/internal/platform/errs"
)

// errors.go 的 FailFromSentinel 测试：各通用哨兵 → 状态码映射、未识别 → 500、
// 以及 %w 包装链 errors.Is 仍命中哨兵（哨兵错误链的核心语义）。

func TestFailFromSentinel(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"validation", errs.ErrValidationFailed, http.StatusBadRequest, errs.ErrValidationFailed.Error()},
		{"rate limited", errs.ErrRateLimited, http.StatusTooManyRequests, errs.ErrRateLimited.Error()},
		{"budget exhausted", errs.ErrBudgetExhausted, http.StatusTooManyRequests, errs.ErrBudgetExhausted.Error()},
		{"service unavailable", errs.ErrServiceUnavailable, http.StatusServiceUnavailable, errs.ErrServiceUnavailable.Error()},
		{"unknown → 500", errors.New("something broke"), http.StatusInternalServerError, errs.ErrInternal.Error()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestEngine(t)
			r.GET("/e", func(c *gin.Context) { FailFromSentinel(c, tc.err) })

			rec := serve(t, r, http.MethodGet, "/e", "")
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			env := parseEnv(t, rec)
			if env.Error == nil || env.Error.Code != tc.wantCode {
				t.Fatalf("code = %+v, want %s", env.Error, tc.wantCode)
			}
		})
	}
}

func TestFailFromSentinel_WrappedChain(t *testing.T) {
	// fmt.Errorf %w 包装后 errors.Is 仍命中哨兵 → 429（业务层包装错误后映射不断链）。
	wrapped := fmt.Errorf("call provider: %w", errs.ErrRateLimited)
	r := newTestEngine(t)
	r.GET("/e", func(c *gin.Context) { FailFromSentinel(c, wrapped) })

	rec := serve(t, r, http.MethodGet, "/e", "")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 (wrapped error should still match sentinel)", rec.Code)
	}
	env := parseEnv(t, rec)
	if env.Error == nil || env.Error.Code != errs.ErrRateLimited.Error() {
		t.Fatalf("code = %+v", env.Error)
	}
}
