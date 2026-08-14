package respond

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Karlsk/go-hify/internal/platform/errs"
)

// respond 的绑定/校验测试：BindJSON/BindQuery/BindUri 三通道 + Validate 路径 + tagName。
// respond 此前零测试，本文件为首份。

// --- 测试用 Validatable 结构体（白盒：复用包内 fieldError / validationDetails）---

type nameReq struct {
	Name string `json:"name" binding:"required"`
}

func (nameReq) Validate() error { return nil }

type pageReq struct {
	Page int `form:"page" binding:"omitempty,min=1"`
}

func (pageReq) Validate() error { return nil }

type idReq struct {
	ID string `uri:"id" binding:"required,numeric"`
}

func (idReq) Validate() error { return nil }

// crossReq 的 Validate 做跨字段校验（区别于 binding tag 的单字段校验）。
type crossReq struct {
	Password string `json:"password" binding:"required"`
	Confirm  string `json:"confirm" binding:"required"`
}

func (r crossReq) Validate() error {
	if r.Password != r.Confirm {
		return errors.New("password and confirm must match")
	}
	return nil
}

// --- 测试基建 ---

// newTestEngine 建测试引擎并注册字段名（gin validator 全局单例，幂等）。
func newTestEngine(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	if err := RegisterFieldNames(); err != nil {
		t.Fatalf("register field names: %v", err)
	}
	return gin.New()
}

// envelope 解析统一信封用于断言（Details 延迟到具体测试再解）。
type envelope struct {
	Success bool `json:"success"`
	Error   *struct {
		Code    string          `json:"code"`
		Message string          `json:"message"`
		Details json.RawMessage `json:"details"`
	} `json:"error"`
}

// doRequest 发请求并解析信封（失败路径断言用）。serve / parseEnv 见 respond_test.go。
func doRequest(t *testing.T, r *gin.Engine, method, target, body string) envelope {
	t.Helper()
	rec := serve(t, r, method, target, body)
	var env envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal response %q: %v", rec.Body.String(), err)
	}
	return env
}

// --- BindJSON ---

func TestBindJSON_Invalid(t *testing.T) {
	r := newTestEngine(t)
	r.POST("/x", func(c *gin.Context) {
		var req nameReq
		if !BindJSON(c, &req) {
			return
		}
		c.JSON(http.StatusOK, gin.H{"name": req.Name})
	})

	env := doRequest(t, r, http.MethodPost, "/x", `{}`)
	if env.Success {
		t.Fatal("missing required field should fail")
	}
	if env.Error == nil || env.Error.Code != errs.ErrValidationFailed.Error() {
		t.Fatalf("code = %+v, want %s", env.Error, errs.ErrValidationFailed.Error())
	}
	// 字段名应是 JSON 名 "name"（注册生效），msg 是校验 tag "required"。
	var vd validationDetails
	if err := json.Unmarshal(env.Error.Details, &vd); err != nil {
		t.Fatalf("unmarshal details: %v", err)
	}
	if len(vd.Fields) != 1 || vd.Fields[0].Field != "name" || vd.Fields[0].Msg != "required" {
		t.Fatalf("fields = %+v, want [{name required}]", vd.Fields)
	}
}

func TestBindJSON_Valid(t *testing.T) {
	r := newTestEngine(t)
	r.POST("/x", func(c *gin.Context) {
		var req nameReq
		if !BindJSON(c, &req) {
			return
		}
		c.JSON(http.StatusOK, gin.H{"name": req.Name})
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"name":"alice"}`))
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestBindJSON_MalformedJSON(t *testing.T) {
	r := newTestEngine(t)
	r.POST("/x", func(c *gin.Context) {
		var req nameReq
		if !BindJSON(c, &req) {
			return
		}
		c.JSON(http.StatusOK, gin.H{})
	})

	// JSON 语法错误走 bindingDetails 的非 validator 分支：message 用原始错误、details 为 nil。
	env := doRequest(t, r, http.MethodPost, "/x", `{not json`)
	if env.Success {
		t.Fatal("malformed json should fail")
	}
	if env.Error == nil || env.Error.Code != errs.ErrValidationFailed.Error() {
		t.Fatalf("code = %+v, want %s", env.Error, errs.ErrValidationFailed.Error())
	}
	if strings.Contains(string(env.Error.Details), "fields") {
		t.Fatalf("malformed json should have no field-level details, got %s", env.Error.Details)
	}
}

func TestBindJSON_CrossFieldValidate(t *testing.T) {
	r := newTestEngine(t)
	r.POST("/x", func(c *gin.Context) {
		var req crossReq
		if !BindJSON(c, &req) {
			return
		}
		c.JSON(http.StatusOK, gin.H{})
	})

	// binding tag 都满足（两字段都给了），但 Validate() 跨字段不匹配 → 400。
	env := doRequest(t, r, http.MethodPost, "/x", `{"password":"a","confirm":"b"}`)
	if env.Success {
		t.Fatal("mismatched password/confirm should fail cross-field validate")
	}
	if env.Error == nil || !strings.Contains(env.Error.Message, "must match") {
		t.Fatalf("error = %+v, want message containing 'must match'", env.Error)
	}
}

// --- BindQuery ---

func TestBindQuery(t *testing.T) {
	r := newTestEngine(t)
	r.GET("/q", func(c *gin.Context) {
		var req pageReq
		if !BindQuery(c, &req) {
			return
		}
		c.JSON(http.StatusOK, gin.H{"page": req.Page})
	})

	// 无效：page=-5 触发 min=1（omitempty 只跳过零值，不跳过负数）。
	if env := doRequest(t, r, http.MethodGet, "/q?page=-5", ""); env.Success {
		t.Fatal("page=-5 should fail min=1")
	}

	// 有效：page=3 → BindQuery 返回 true → 200。
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/q?page=3", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

// --- BindUri ---

func TestBindUri(t *testing.T) {
	r := newTestEngine(t)
	r.GET("/u/:id", func(c *gin.Context) {
		var req idReq
		if !BindUri(c, &req) {
			return
		}
		c.JSON(http.StatusOK, gin.H{"id": req.ID})
	})

	// 无效：id=abc 触发 numeric。
	if env := doRequest(t, r, http.MethodGet, "/u/abc", ""); env.Success {
		t.Fatal("id=abc should fail numeric")
	}

	// 有效：id=123 → 200。
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/u/123", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

// --- tagName（纯单测）---

// fieldOf 取 T 的第 0 个字段的 StructField。
func fieldOf[T any]() reflect.StructField {
	return reflect.TypeOf((*T)(nil)).Elem().Field(0)
}

func TestTagName(t *testing.T) {
	tests := []struct {
		name string
		fld  reflect.StructField
		want string
	}{
		{"json", fieldOf[struct {
			X int `json:"x"`
		}](), "x"},
		{"json with omitempty", fieldOf[struct {
			X int `json:"x,omitempty"`
		}](), "x"},
		{"json - skips to form", fieldOf[struct {
			X int `json:"-" form:"page"`
		}](), "page"},
		{"form only", fieldOf[struct {
			X int `form:"page"`
		}](), "page"},
		{"uri fallback", fieldOf[struct {
			X int `uri:"id"`
		}](), "id"},
		{"no tags → Go name", fieldOf[struct {
			UserName int
		}](), "UserName"},
		{"empty json → Go name", fieldOf[struct {
			X int `json:""`
		}](), "X"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tagName(tt.fld); got != tt.want {
				t.Fatalf("tagName = %q, want %q", got, tt.want)
			}
		})
	}
}
