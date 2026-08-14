package respond

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
)

// 本文件填 bind.go 的既有 TODO：让 validator 的校验错误报请求侧字段名（json/form/uri tag）
// 而非 Go 字段名，前端才能按 JSON 名匹配字段。在 gin 引擎创建后调一次 RegisterFieldNames（见 app/server.go）。

// RegisterFieldNames 在 gin 全局 validator 上注册字段名解析：校验错误的 field 报请求侧名字
// （json → form → uri 优先级，见 tagName），而非 Go 字段名。
//
// 必须在 gin 引擎创建后、处理请求前调用一次（app/server.go 装配）：
//
//	if err := respond.RegisterFieldNames(); err != nil {
//	    slog.Warn("validator field-name registration failed", "err", err)
//	}
//
// 失败非致命：gin validator 引擎类型断言失败时返回错误，校验本身不受影响（仅回退 Go 字段名）。
// 注册是幂等的，重复调用无副作用（测试与生产可各自调一次）。
func RegisterFieldNames() error {
	v, ok := binding.Validator.Engine().(*validator.Validate)
	if !ok {
		return fmt.Errorf("gin validator engine is %T, not *validator.Validate", binding.Validator.Engine())
	}
	v.RegisterTagNameFunc(tagName)
	return nil
}

// tagName 返回结构体字段的请求侧名字（用于校验错误的 field 名）：
// 优先 json tag，其次 form、uri（处理 "name,omitempty" 取逗号前部分）；均无或为 "-" 回退 Go 字段名。
//
// RegisterTagNameFunc 只影响 FieldError.Field()（错误里的字段名），不影响 gin 用哪个 tag 取值
// （取值仍由 json/form/uri tag 决定），所以注册它零副作用。
func tagName(fld reflect.StructField) string {
	for _, tag := range []string{"json", "form", "uri"} {
		raw := fld.Tag.Get(tag)
		if raw == "" || raw == "-" {
			continue
		}
		name := strings.SplitN(raw, ",", 2)[0]
		if name != "" && name != "-" {
			return name
		}
	}
	return fld.Name
}
