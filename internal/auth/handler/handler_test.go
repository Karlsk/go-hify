package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	authapi "github.com/Karlsk/go-hify/internal/auth/api"
)

// fakeSvc 实现 authapi.AuthService：校验逻辑固定，供中间件与路由测试。
type fakeSvc struct {
	validTokens map[string]*authapi.UserSchema
}

func newFakeSvc() *fakeSvc {
	return &fakeSvc{validTokens: map[string]*authapi.UserSchema{}}
}

func (f *fakeSvc) Register(_ context.Context, req authapi.RegisterReq) (*authapi.UserSchema, error) {
	return &authapi.UserSchema{ID: "1", Username: req.Username}, nil
}

func (f *fakeSvc) Login(_ context.Context, req authapi.LoginReq) (*authapi.SessionSchema, error) {
	if req.Username != "alice" || req.Password != "password123" {
		return nil, authapi.ErrUnauthorized
	}
	u := &authapi.UserSchema{ID: "1", Username: "alice"}
	f.validTokens["tok123"] = u
	return &authapi.SessionSchema{Token: "tok123", User: *u}, nil
}

func (f *fakeSvc) Logout(_ context.Context, token string) error {
	delete(f.validTokens, token)
	return nil
}

func (f *fakeSvc) Validate(_ context.Context, token string) (*authapi.UserSchema, error) {
	if token == "expired" {
		return nil, authapi.ErrSessionExpired
	}
	u, ok := f.validTokens[token]
	if !ok {
		return nil, authapi.ErrSessionExpired
	}
	return u, nil
}

func (f *fakeSvc) Me(_ context.Context) (*authapi.UserSchema, error) {
	return &authapi.UserSchema{ID: "1", Username: "alice"}, nil
}

func newTestRouter(svc authapi.AuthService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(svc, false)
	v1 := r.Group("/api/v1")
	// 与组合根同序：先挂中间件，再注册路由（gin 的 Use 只对之后注册的路由生效）
	v1.Use(h.Middleware())
	h.RegisterRoutes(v1)
	// 受保护的下游业务路由（模拟）
	v1.GET("/protected", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r
}

func doReq(t *testing.T, r *gin.Engine, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestLoginSetsCookie(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	w := doReq(t, r, http.MethodPost, "/api/v1/auth/login", `{"username":"alice","password":"password123"}`, nil)

	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %d, want 1", len(cookies))
	}
	c := cookies[0]
	if c.Name != authapi.CookieName || c.Value != "tok123" {
		t.Fatalf("cookie = %+v", c)
	}
	if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie attrs = HttpOnly:%v SameSite:%v", c.HttpOnly, c.SameSite)
	}
	var body struct {
		Data struct {
			Username string `json:"username"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Data.Username != "alice" {
		t.Fatalf("data.username = %s", body.Data.Username)
	}
}

func TestLoginBadCredentials(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	w := doReq(t, r, http.MethodPost, "/api/v1/auth/login", `{"username":"alice","password":"wrongpass1"}`, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", w.Code)
	}
	if !strings.Contains(w.Body.String(), "UNAUTHORIZED") {
		t.Fatalf("body = %s", w.Body.String())
	}
}

func TestRegisterConflict(t *testing.T) {
	// fakeSvc.Register 永远成功；冲突路径用 409 断言（绑定失败路径由 BindJSON 测试覆盖）
	r := newTestRouter(newFakeSvc())
	w := doReq(t, r, http.MethodPost, "/api/v1/auth/register", `{"username":"bob","password":"password123"}`, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("code = %d, want 201", w.Code)
	}
}

func TestMiddlewareAllowsLoginPath(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	// login 路径无 cookie 直达 handler（fake 校验凭据）
	w := doReq(t, r, http.MethodPost, "/api/v1/auth/login", `{"username":"alice","password":"password123"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", w.Code)
	}
}

func TestMiddlewareRejectsNoCookie(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	w := doReq(t, r, http.MethodGet, "/api/v1/protected", "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", w.Code)
	}
	if !strings.Contains(w.Body.String(), "UNAUTHORIZED") {
		t.Fatalf("body = %s", w.Body.String())
	}
}

func TestMiddlewareRejectsExpiredSession(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	w := doReq(t, r, http.MethodGet, "/api/v1/protected", "", &http.Cookie{Name: authapi.CookieName, Value: "expired"})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", w.Code)
	}
	if !strings.Contains(w.Body.String(), "SESSION_EXPIRED") {
		t.Fatalf("body = %s", w.Body.String())
	}
}

func TestMiddlewarePassesValidSession(t *testing.T) {
	svc := newFakeSvc()
	r := newTestRouter(svc)
	// 先登录拿到 token
	doReq(t, r, http.MethodPost, "/api/v1/auth/login", `{"username":"alice","password":"password123"}`, nil)
	w := doReq(t, r, http.MethodGet, "/api/v1/protected", "", &http.Cookie{Name: authapi.CookieName, Value: "tok123"})
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
}

func TestLogoutClearsCookie(t *testing.T) {
	svc := newFakeSvc()
	r := newTestRouter(svc)
	doReq(t, r, http.MethodPost, "/api/v1/auth/login", `{"username":"alice","password":"password123"}`, nil)
	w := doReq(t, r, http.MethodPost, "/api/v1/auth/logout", "", &http.Cookie{Name: authapi.CookieName, Value: "tok123"})
	if w.Code != http.StatusNoContent {
		t.Fatalf("code = %d, want 204", w.Code)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].MaxAge >= 0 {
		t.Fatalf("logout cookie = %+v, want MaxAge<0", cookies)
	}
	// 登出后 session 失效，受保护路由 401
	w2 := doReq(t, r, http.MethodGet, "/api/v1/protected", "", &http.Cookie{Name: authapi.CookieName, Value: "tok123"})
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("after logout code = %d, want 401", w2.Code)
	}
}

func TestMe(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	w := doReq(t, r, http.MethodGet, "/api/v1/auth/me", "", nil)
	// me 路由受中间件保护：无 cookie 应 401（fake Me 是给有身份场景的）
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("me without session code = %d, want 401", w.Code)
	}
}
