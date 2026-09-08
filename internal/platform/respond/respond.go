// Package respond 是 Hify 全部 HTTP 接口的统一响应出口：所有 handler 的返回
// 都经此处的信封结构与写入函数落地，一处定义、全接口复用（见 CLAUDE.md《统一响应信封》）。
package respond

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Karlsk/go-hify/internal/platform/errs"
	"github.com/Karlsk/go-hify/internal/platform/traceid"
)

// Result 是所有 HTTP 接口返回的统一信封。
// 成功时 Success=true、Data 载荷、Error=nil；失败时 Success=false、Data=nil、Error 非空。
// Meta 可选，当前仅放分页信息（见 pagination.go）。
type Result struct {
	Success bool       `json:"success"`
	Data    any        `json:"data"`
	Error   *ErrorBody `json:"error"`
	Meta    any        `json:"meta"`
}

// ErrorBody 是信封 error 字段的载荷：
//   - Code：机器可读字符串（如 PROVIDER_NOT_FOUND），与各模块 api/ 包哨兵错误一一对应，
//     供前端分支；HTTP 状态码本身即主信号，不再复制进 body。
//   - Message：人类可读的错误描述。
//   - Details：可选，字段级校验错误等结构化补充（如 {"fields":[...]}）。
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details"`
}

// 通用机器可读码。VALIDATION_FAILED / INTERNAL_ERROR 等的权威定义在 internal/platform/errs
// （哨兵的 Error() 即码），respond 经 errs 引用，避免码字符串散落两处。CodeNotFound 无对应哨兵，
// 是 404 的通用兜底（模块级缺失用各自 api 包哨兵，如 PROVIDER_NOT_FOUND）。
const (
	CodeNotFound = "NOT_FOUND"
)

// noStore 给响应统一打 Cache-Control: no-store（CLAUDE.md：API 响应一律 no-store，
// 静态资源长缓存在 nginx，不经此信封）。
func noStore(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
}

// OK 写 200 + 成功信封（无 meta）。
func OK(c *gin.Context, data any) {
	OKWithMeta(c, data, nil)
}

// Created 写 201 + 成功信封（POST 创建成功，CLAUDE.md《错误处理》状态映射）。
func Created(c *gin.Context, data any) {
	noStore(c)
	c.JSON(http.StatusCreated, Result{Success: true, Data: data, Meta: nil})
}

// Accepted 写 202 + 成功信封（异步受理：请求已入队但结果未就绪——rag 文档上传 /
// 重索引落 pending 后受理即返回，进度由前端轮询资源状态获取）。
func Accepted(c *gin.Context, data any) {
	noStore(c)
	c.JSON(http.StatusAccepted, Result{Success: true, Data: data, Meta: nil})
}

// OKWithMeta 写 200 + 成功信封，附带 meta（如分页信息）。
func OKWithMeta(c *gin.Context, data any, meta any) {
	noStore(c)
	c.JSON(http.StatusOK, Result{Success: true, Data: data, Meta: meta})
}

// Fail 写失败信封：调用方显式指定 HTTP 状态码、机器可读 code、人类可读 message（无 details）。
// 其余失败类函数（FailFromSentinel / BadRequest / NotFound / Error）均经此入口，no-store 在此统一打。
func Fail(c *gin.Context, httpStatus int, code, message string) {
	FailWithDetails(c, httpStatus, code, message, nil)
}

// FailWithDetails 写带 details 的失败信封（如字段级校验错误 details.fields）。
// 所有失败响应的最底入口：no-store 在此统一打；code 必须取自哨兵的 Error() 字符串，禁止裸字符串。
func FailWithDetails(c *gin.Context, httpStatus int, code, message string, details any) {
	noStore(c)
	c.JSON(httpStatus, Result{
		Success: false,
		Error:   &ErrorBody{Code: code, Message: message, Details: details},
	})
}

// BadRequest 写 400 + VALIDATION_FAILED（参数绑定 / 校验失败，无字段级 details）。
// 需要字段级错误用 BindJSON（自动带 details.fields）或直接调 FailWithDetails。
func BadRequest(c *gin.Context, message string) {
	Fail(c, http.StatusBadRequest, errs.ErrValidationFailed.Error(), message)
}

// NotFound 写 404 + NOT_FOUND（通用资源不存在）。
// 模块级缺失应优先用模块哨兵码，如 Fail(c, 404, "PROVIDER_NOT_FOUND", msg)。
func NotFound(c *gin.Context, message string) {
	Fail(c, http.StatusNotFound, CodeNotFound, message)
}

// Error 是未预期错误的统一兜底：写 500 + INTERNAL_ERROR，向前端隐藏细节；
// 原始 err 以 ERROR 级进结构化日志（trace_id 由 logging trace handler 从 ctx 自动追加）——
// 前端拿 INTERNAL_ERROR + trace_id，运维凭 trace_id 追到完整错误链
// （CLAUDE.md §错误处理：500 类不回原始堆栈给前端，只回 INTERNAL_ERROR + trace_id）。
func Error(c *gin.Context, err error) {
	ctx := c.Request.Context()
	slog.ErrorContext(ctx, "internal server error", slog.Any("err", err))
	Fail(c, http.StatusInternalServerError, errs.ErrInternal.Error(), internalMessage(ctx))
}

// internalMessage 组装 500 类 body message：固定文案 +（ctx 带 trace_id 时）trace 后缀，
// 前端可复制给运维对账日志；错误细节永远只进日志、不进 body。
func internalMessage(ctx context.Context) string {
	msg := "internal server error"
	if id, ok := traceid.From(ctx); ok {
		msg += " (trace " + id + ")"
	}
	return msg
}
