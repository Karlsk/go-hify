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

// BindJSON 绑定（binding tag 字段格式）+ 校验（跨字段 Validate()）两步合一步。
// 失败时写 400 + VALIDATION_FAILED 信封（binding 失败带 details.fields 字段级错误）并返回 false；
// 成功返回 true。handler 拿到 false 直接 return，无需再写错误分支：
//
//	if !respond.BindJSON(c, &req) {
//	    return
//	}
//
// 字段名当前取 validator 的 Go 字段名；改用 JSON 名需在 gin 装配处给 validator
// 注册 RegisterTagNameFunc（读 json tag），属 main.go 装配职责，不在此耦合。
func BindJSON(c *gin.Context, req Validatable) bool {
	if err := c.ShouldBindJSON(req); err != nil {
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
