package respond

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Karlsk/go-hify/internal/platform/errs"
)

// FailFromSentinel 把 error 自动映射为失败响应：依次用 errors.Is 匹配通用哨兵
// （定义在 internal/platform/errs），命中即写对应 HTTP 状态码 + code（= 哨兵的 Error()）；
// 未命中任何通用哨兵时退回 Error(c, err)——500 INTERNAL_ERROR + 记日志
// （ErrInternal 与所有未识别错误均走此分支）。
//
// 适用：handler 捕获 service 上抛的「通用」错误时一行兜底，例如：
//
//	if err := h.svc.Foo(ctx, req); err != nil {
//	    respond.FailFromSentinel(c, err)
//	    return
//	}
//
// 局限：模块业务哨兵（providerapi.ErrProviderNotFound 等）与 platform/llm 操作哨兵
// （llm.ErrProviderBusy 等）不在此自动映射——respond 不能 import 业务域，也不应耦合 llm。
// 这些哨兵由各 handler 先显式 errors.Is → respond.Fail(...) 映射，再回退到本函数。
func FailFromSentinel(c *gin.Context, err error) {
	switch {
	case errors.Is(err, errs.ErrValidationFailed):
		Fail(c, http.StatusBadRequest, errs.ErrValidationFailed.Error(), err.Error())
	case errors.Is(err, errs.ErrRateLimited):
		Fail(c, http.StatusTooManyRequests, errs.ErrRateLimited.Error(), err.Error())
	case errors.Is(err, errs.ErrBudgetExhausted):
		Fail(c, http.StatusTooManyRequests, errs.ErrBudgetExhausted.Error(), err.Error())
	case errors.Is(err, errs.ErrServiceUnavailable):
		Fail(c, http.StatusServiceUnavailable, errs.ErrServiceUnavailable.Error(), err.Error())
	default:
		Error(c, err) // 500 INTERNAL_ERROR；ErrInternal 与所有未识别错误均走此分支
	}
}
