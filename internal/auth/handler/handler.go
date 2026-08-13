// Package handler 是 auth 的 HTTP 层：路由绑定 + 登录中间件。薄绑定，无业务逻辑。
package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	authapi "github.com/Karlsk/go-hify/internal/auth/api"
	"github.com/Karlsk/go-hify/internal/platform/authctx"
	"github.com/Karlsk/go-hify/internal/platform/respond"
)

// Handler 持有本模块 api 接口（组合根注入 service 实现）。
type Handler struct {
	svc          authapi.AuthService
	cookieSecure bool
}

// New 创建 Handler。cookieSecure=true 时 session cookie 加 Secure 属性（生产 HTTPS）。
func New(svc authapi.AuthService, cookieSecure bool) *Handler {
	return &Handler{svc: svc, cookieSecure: cookieSecure}
}

// RegisterRoutes 挂载 auth 路由（/auth/login、/auth/register 在中间件白名单内）。
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	g := rg.Group("/auth")
	g.POST("/login", h.login)
	g.POST("/register", h.register)
	g.POST("/logout", h.logout)
	g.GET("/me", h.me)
}

// Middleware 登录校验中间件（CLAUDE.md《认证》）：/auth/login、/auth/register 放行，
// 其余 /api/v1/* 校验 session cookie，通过则把用户身份注入 ctx（authctx.WithUser）。
// 业务 API 一期不按用户隔离，但仍要求登录门槛；身份供 budget 计数与后续 user_id 落库用。
func (h *Handler) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.URL.Path {
		case "/api/v1/auth/login", "/api/v1/auth/register":
			c.Next()
			return
		}

		token, err := c.Cookie(authapi.CookieName)
		if err != nil || token == "" {
			respond.Fail(c, http.StatusUnauthorized, authapi.ErrUnauthorized.Error(), "未登录")
			c.Abort()
			return
		}
		u, err := h.svc.Validate(c.Request.Context(), token)
		if err != nil {
			if errors.Is(err, authapi.ErrSessionExpired) {
				respond.Fail(c, http.StatusUnauthorized, authapi.ErrSessionExpired.Error(), "会话已过期，请重新登录")
				c.Abort()
				return
			}
			respond.Fail(c, http.StatusUnauthorized, authapi.ErrUnauthorized.Error(), "登录校验失败")
			c.Abort()
			return
		}
		id, _ := strconv.ParseUint(u.ID, 10, 64)
		c.Request = c.Request.WithContext(authctx.WithUser(c.Request.Context(), &authctx.User{
			ID:       id,
			Username: u.Username,
		}))
		c.Next()
	}
}

func (h *Handler) login(c *gin.Context) {
	var req authapi.LoginReq
	if !respond.BindJSON(c, &req) {
		return
	}
	sess, err := h.svc.Login(c.Request.Context(), req)
	if err != nil {
		if errors.Is(err, authapi.ErrUnauthorized) {
			respond.Fail(c, http.StatusUnauthorized, authapi.ErrUnauthorized.Error(), "用户名或密码错误")
			return
		}
		respond.FailFromSentinel(c, err)
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     authapi.CookieName,
		Value:    sess.Token,
		Path:     "/",
		MaxAge:   int(authapi.SessionTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   h.cookieSecure,
	})
	respond.OK(c, sess.User)
}

func (h *Handler) register(c *gin.Context) {
	var req authapi.RegisterReq
	if !respond.BindJSON(c, &req) {
		return
	}
	u, err := h.svc.Register(c.Request.Context(), req)
	if err != nil {
		if errors.Is(err, authapi.ErrUsernameConflict) {
			respond.Fail(c, http.StatusConflict, authapi.ErrUsernameConflict.Error(), "用户名已存在")
			return
		}
		respond.FailFromSentinel(c, err)
		return
	}
	respond.Created(c, u)
}

func (h *Handler) logout(c *gin.Context) {
	token, _ := c.Cookie(authapi.CookieName)
	if err := h.svc.Logout(c.Request.Context(), token); err != nil {
		respond.FailFromSentinel(c, err)
		return
	}
	// 清 cookie（MaxAge<0 立即过期）
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     authapi.CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   h.cookieSecure,
	})
	c.Status(http.StatusNoContent)
}

func (h *Handler) me(c *gin.Context) {
	u, err := h.svc.Me(c.Request.Context())
	if err != nil {
		respond.FailFromSentinel(c, err)
		return
	}
	respond.OK(c, u)
}
