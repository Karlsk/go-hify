// Package handler 是 demo 模块的 HTTP 层：gin 路由与绑定。薄绑定，无业务逻辑。
package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	demoapi "github.com/Karlsk/go-hify/internal/demo/api"
	"github.com/Karlsk/go-hify/internal/platform/respond"
)

// Handler 持有本模块 api 接口（组合根注入 service 实现）。
type Handler struct {
	svc demoapi.DemoService
}

// New 创建 Handler。
func New(svc demoapi.DemoService) *Handler { return &Handler{svc: svc} }

// RegisterRoutes 挂载 demo-items 路由（REST 动词命名）。
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	g := rg.Group("/demo-items")
	g.POST("", h.create)
	g.GET("", h.list)
	g.GET("/:id", h.get)
	g.PUT("/:id", h.update)
	g.DELETE("/:id", h.delete)
}

func (h *Handler) create(c *gin.Context) {
	var req demoapi.CreateReq
	if !respond.BindJSON(c, &req) {
		return
	}
	item, err := h.svc.Create(c.Request.Context(), req)
	if err != nil {
		respond.FailFromSentinel(c, err)
		return
	}
	respond.Created(c, item)
}

func (h *Handler) get(c *gin.Context) {
	var req demoapi.GetReq
	if !respond.BindUri(c, &req) {
		return
	}
	item, err := h.svc.Get(c.Request.Context(), req)
	if err != nil {
		if errors.Is(err, demoapi.ErrDemoItemNotFound) {
			respond.Fail(c, http.StatusNotFound, demoapi.ErrDemoItemNotFound.Error(), "demo 条目不存在")
			return
		}
		respond.FailFromSentinel(c, err)
		return
	}
	respond.OK(c, item)
}

func (h *Handler) list(c *gin.Context) {
	var req demoapi.ListReq
	if !respond.BindQuery(c, &req) {
		return
	}
	res, err := h.svc.List(c.Request.Context(), req)
	if err != nil {
		respond.FailFromSentinel(c, err)
		return
	}
	respond.OKWithOffset(c, res.Items, res.Page, res.PageSize, res.Total)
}

func (h *Handler) update(c *gin.Context) {
	// gin 的 BindUri / BindJSON 都会校验整个结构体，路径参数与请求体必须分开绑定：
	// 先用纯 uri 结构 GetReq 绑 :id，再用 UpdateReq 绑 body（此时 ID 已赋值，Validate 校验 ID>0）。
	var idReq demoapi.GetReq
	if !respond.BindUri(c, &idReq) {
		return
	}
	var req demoapi.UpdateReq
	req.ID = idReq.ID
	if !respond.BindJSON(c, &req) {
		return
	}
	item, err := h.svc.Update(c.Request.Context(), req)
	if err != nil {
		if errors.Is(err, demoapi.ErrDemoItemNotFound) {
			respond.Fail(c, http.StatusNotFound, demoapi.ErrDemoItemNotFound.Error(), "demo 条目不存在")
			return
		}
		respond.FailFromSentinel(c, err)
		return
	}
	respond.OK(c, item)
}

func (h *Handler) delete(c *gin.Context) {
	var req demoapi.DeleteReq
	if !respond.BindUri(c, &req) {
		return
	}
	if err := h.svc.Delete(c.Request.Context(), req); err != nil {
		if errors.Is(err, demoapi.ErrDemoItemNotFound) {
			respond.Fail(c, http.StatusNotFound, demoapi.ErrDemoItemNotFound.Error(), "demo 条目不存在")
			return
		}
		respond.FailFromSentinel(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
