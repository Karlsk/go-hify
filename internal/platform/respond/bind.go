package respond

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"

	"github.com/Karlsk/go-hify/internal/platform/errs"
)

// Validatable 是带跨字段校验的请求体契约：各模块 api/schema.go 的 Req 实现 Validate()。
// 绑定（binding tag 管字段格式）与校验（Validate 管跨字段规则）见 CLAUDE.md《模块内部结构》。
type Validatable interface {
	Validate() error
}

// fieldError 是校验失败的单个字段，对应 CLAUDE.md《错误处理》details.fields 的元素。
type fieldError struct {
	Field string `json:"field"`
	Msg   string `json:"msg"`
}

// validationDetails 是 VALIDATION_FAILED 的 details 载荷：字段级错误列表。
type validationDetails struct {
	Fields []fieldError `json:"fields"`
}

// BindJSON 绑定 JSON body（binding tag 字段格式）+ 校验（跨字段 Validate()）两步合一步。
// 失败时写 400 + VALIDATION_FAILED 信封（binding 失败带 details.fields 字段级错误）并返回 false；
// 成功返回 true。handler 拿到 false 直接 return，无需再写错误分支：
//
//	if !respond.BindJSON(c, &req) {
//	    return
//	}
//
// 校验错误报请求侧字段名（json tag）而非 Go 字段名——需在 gin 装配处调一次 RegisterFieldNames
// （见 app/server.go）；未注册则回退 Go 字段名。
func BindJSON(c *gin.Context, req Validatable) bool {
	return bind(c, req, c.ShouldBindJSON)
}

// BindQuery 绑定 query string（form tag 字段格式，如 ?page=1&page_size=20）+ 校验。
// 语义与 BindJSON 一致，仅数据来源不同（c.ShouldBindQuery）。用于列表分页等 query 参数接口：
//
//	var req ListReq
//	if !respond.BindQuery(c, &req) {
//	    return
//	}
func BindQuery(c *gin.Context, req Validatable) bool {
	return bind(c, req, c.ShouldBindQuery)
}

// BindUri 绑定路径参数（uri tag，如 /providers/:id）+ 校验。语义同 BindJSON，来源 c.ShouldBindUri：
//
//	var req GetReq // { ID string `uri:"id" binding:"required,numeric"` }
//	if !respond.BindUri(c, &req) {
//	    return
//	}
func BindUri(c *gin.Context, req Validatable) bool {
	return bind(c, req, c.ShouldBindUri)
}

// bind 是 BindJSON/BindQuery/BindUri 的共享实现：bindFn 做请求→结构体的绑定（含 binding tag 校验），
// 随后跑跨字段 Validate()。失败统一写 400 + VALIDATION_FAILED；成功返回 true。
// 三通道共用同一套错误格式——binding 失败带 details.fields 字段级错误，Validate 失败带其 message。
func bind(c *gin.Context, req Validatable, bindFn func(any) error) bool {
	if err := bindFn(req); err != nil {
		msg, details := bindingDetails(err)
		FailWithDetails(c, http.StatusBadRequest, errs.ErrValidationFailed.Error(), msg, details)
		return false
	}
	if err := req.Validate(); err != nil {
		FailWithDetails(c, http.StatusBadRequest, errs.ErrValidationFailed.Error(), err.Error(), nil)
		return false
	}
	return true
}

// bindingDetails 把绑定错误翻译成 details 载荷：
//   - validator.ValidationErrors → 字段级 {fields:[{field,msg}]}，msg 取校验 tag（required/max/...）；
//   - 其余（JSON 语法错误等）无字段级信息，message 用原始错误、details 为 nil。
func bindingDetails(err error) (message string, details any) {
	var verrs validator.ValidationErrors
	if errors.As(err, &verrs) {
		fields := make([]fieldError, 0, len(verrs))
		for _, fe := range verrs {
			fields = append(fields, fieldError{Field: fe.Field(), Msg: fe.Tag()})
		}
		return "参数校验失败", validationDetails{Fields: fields}
	}
	return err.Error(), nil
}
