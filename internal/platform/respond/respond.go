// Package respond 是 Hify 全部 HTTP 接口的统一响应出口：所有 handler 的返回
// 都经此处的信封结构与写入函数落地，一处定义、全接口复用（见 CLAUDE.md《统一响应信封》）。
package respond

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Karlsk/go-hify/internal/platform/errs"
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

// OKWithMeta 写 200 + 成功信封，附带 meta（如分页信息）。
func OKWithMeta(c *gin.Context, data any, meta any) {
	noStore(c)
	c.JSON(http.StatusOK, Result{Success: true, Data: data, Meta: meta})
}

// Fail 写失败信封：调用方显式指定 HTTP 状态码、机器可读 code、人类可读 message。
// 其余失败类函数（BadRequest / NotFound / Error）均经此入口，no-store 在此统一打。
func Fail(c *gin.Context, httpStatus int, code, message string) {
	noStore(c)
	c.JSON(httpStatus, Result{
		Success: false,
		Error:   &ErrorBody{Code: code, Message: message},
	})
}

// BadRequest 写 400 + VALIDATION_FAILED（参数绑定 / 校验失败）。
// 字段级错误请改用 Fail 自带 details，或在 details 接入后扩展 WithDetails 变体。
func BadRequest(c *gin.Context, message string) {
	Fail(c, http.StatusBadRequest, errs.ErrValidationFailed.Error(), message)
}

// NotFound 写 404 + NOT_FOUND（通用资源不存在）。
// 模块级缺失应优先用模块哨兵码，如 Fail(c, 404, "PROVIDER_NOT_FOUND", msg)。
func NotFound(c *gin.Context, message string) {
	Fail(c, http.StatusNotFound, CodeNotFound, message)
}

// Error 是未预期错误的统一兜底：写 500 + INTERNAL_ERROR，向前端隐藏细节。
// TODO: 接入 platform/logging 后在此记录原始 err（含 trace_id、调用上下文）——
// 当前 logging 模块尚未落地，先不引入其依赖。
func Error(c *gin.Context, err error) {
	Fail(c, http.StatusInternalServerError, errs.ErrInternal.Error(), "internal server error")
}
