// Package handler 是 provider 模块的 HTTP 层：gin 路由与绑定。薄绑定，无业务逻辑。
//
// 对应 Java Spring 的 Controller（概念映射见 docs/changelog/provider/handler_spec.md §1）：
// RegisterRoutes 即路由声明，respond.Bind* 合一步完成 @Valid 校验（binding tag 字段格式 +
// Validate 跨字段），统一信封 respond.Result 即 Result<T> 返回体。
package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Karlsk/go-hify/internal/platform/respond"
	providerapi "github.com/Karlsk/go-hify/internal/provider/api"
)

// Handler 持本模块两个 api 接口（组合根注入 service 实现）；一个 Handler 挂全部 12 条路由，
// 避免拆两个类型带来的注册顺序心智负担（handler_spec.md §2）。
type Handler struct {
	providers providerapi.ProviderService
	models    providerapi.ModelService
}

// New 创建 Handler。
func New(providers providerapi.ProviderService, models providerapi.ModelService) *Handler {
	return &Handler{providers: providers, models: models}
}

// RegisterRoutes 挂 providers（含嵌套的模型集合路由）与 models（条目路由）两组。
// 集合作用域挂 provider 下、条目作用域平铺——由 api 契约的 Req 形状反推，见 handler_spec.md §3。
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	p := rg.Group("/providers")
	p.POST("", h.create)
	p.GET("", h.list)
	p.GET("/:id", h.get)
	p.PUT("/:id", h.update)
	p.DELETE("/:id", h.delete)
	p.POST("/:id/test-connection", h.testConnection)
	p.GET("/:id/models", h.listModels)
	p.POST("/:id/models/sync", h.syncModels)

	m := rg.Group("/models")
	m.POST("", h.createModel)
	m.GET("/:id", h.getModel)
	m.PUT("/:id", h.updateModel)
	m.DELETE("/:id", h.deleteModel)
}

// ---- provider 本体 ----

func (h *Handler) create(c *gin.Context) {
	var req providerapi.CreateProviderReq
	if !respond.BindJSON(c, &req) {
		return
	}
	s, err := h.providers.Create(c.Request.Context(), req)
	if err != nil {
		failProvider(c, err)
		return
	}
	respond.Created(c, s)
}

func (h *Handler) list(c *gin.Context) {
	var req providerapi.ListProvidersReq
	if !respond.BindQuery(c, &req) {
		return
	}
	res, err := h.providers.List(c.Request.Context(), req)
	if err != nil {
		respond.FailFromSentinel(c, err)
		return
	}
	respond.OKWithOffset(c, res.Items, res.Page, res.PageSize, res.Total)
}

func (h *Handler) get(c *gin.Context) {
	var req providerapi.GetProviderReq
	if !respond.BindUri(c, &req) {
		return
	}
	d, err := h.providers.Get(c.Request.Context(), req)
	if err != nil {
		failProvider(c, err)
		return
	}
	respond.OK(c, d)
}

func (h *Handler) update(c *gin.Context) {
	// 路径参数与请求体分开绑定（gin 的 Bind* 校验整个结构体）：先绑 :id 再绑 body
	//（同 demo 模式，UpdateProviderReq.ID 无 binding tag 即为此设计）。
	var idReq providerapi.GetProviderReq
	if !respond.BindUri(c, &idReq) {
		return
	}
	var req providerapi.UpdateProviderReq
	req.ID = idReq.ID
	if !respond.BindJSON(c, &req) {
		return
	}
	s, err := h.providers.Update(c.Request.Context(), req)
	if err != nil {
		failProvider(c, err)
		return
	}
	respond.OK(c, s)
}

func (h *Handler) delete(c *gin.Context) {
	var req providerapi.DeleteProviderReq
	if !respond.BindUri(c, &req) {
		return
	}
	if err := h.providers.Delete(c.Request.Context(), req); err != nil {
		failProvider(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// testConnection 连通性探测：HTTP 200 = 探测已执行；success=false（不可达 / 鉴权失败 /
// 解密失败）是业务结果、走 data 载荷，不是 HTTP 错误——只有 NotFound 与写库失败走错误分支
// （handler_spec.md §4）。
func (h *Handler) testConnection(c *gin.Context) {
	var req providerapi.TestConnectionReq
	if !respond.BindUri(c, &req) {
		return
	}
	res, err := h.providers.TestConnection(c.Request.Context(), req)
	if err != nil {
		failProvider(c, err)
		return
	}
	respond.OK(c, res)
}

// ---- 模型 ----

// listModels 混合来源绑定：路径 id（uri tag）与分页（form tag）分两步绑——
// ShouldBindQuery 不解析 uri tag，反之亦然；Validate 为空实现，跑两遍无害。
func (h *Handler) listModels(c *gin.Context) {
	var req providerapi.ListModelsReq
	if !respond.BindUri(c, &req) {
		return
	}
	if !respond.BindQuery(c, &req) {
		return
	}
	res, err := h.models.List(c.Request.Context(), req)
	if err != nil {
		failModel(c, err)
		return
	}
	respond.OKWithOffset(c, res.Items, res.Page, res.PageSize, res.Total)
}

// syncModels 模型自动发现（service 占位期返回 503，模型发现批次落地后自然变 200）。
func (h *Handler) syncModels(c *gin.Context) {
	var req providerapi.SyncModelsReq
	if !respond.BindUri(c, &req) {
		return
	}
	res, err := h.models.SyncModels(c.Request.Context(), req)
	if err != nil {
		failModel(c, err)
		return
	}
	respond.OK(c, res)
}

func (h *Handler) createModel(c *gin.Context) {
	var req providerapi.CreateModelReq
	if !respond.BindJSON(c, &req) {
		return
	}
	s, err := h.models.Create(c.Request.Context(), req)
	if err != nil {
		failModel(c, err)
		return
	}
	respond.Created(c, s)
}

func (h *Handler) getModel(c *gin.Context) {
	var req providerapi.GetModelReq
	if !respond.BindUri(c, &req) {
		return
	}
	s, err := h.models.Get(c.Request.Context(), req)
	if err != nil {
		failModel(c, err)
		return
	}
	respond.OK(c, s)
}

func (h *Handler) updateModel(c *gin.Context) {
	// 两段绑定同 provider.update：先绑 :id 再绑 body（UpdateModelReq.ID 无 binding tag）。
	var idReq providerapi.GetModelReq
	if !respond.BindUri(c, &idReq) {
		return
	}
	var req providerapi.UpdateModelReq
	req.ID = idReq.ID
	if !respond.BindJSON(c, &req) {
		return
	}
	s, err := h.models.Update(c.Request.Context(), req)
	if err != nil {
		failModel(c, err)
		return
	}
	respond.OK(c, s)
}

func (h *Handler) deleteModel(c *gin.Context) {
	var req providerapi.DeleteModelReq
	if !respond.BindUri(c, &req) {
		return
	}
	if err := h.models.Delete(c.Request.Context(), req); err != nil {
		failModel(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ---- 哨兵映射 ----

// failProvider 映射 provider 侧业务哨兵（handler_spec.md §4）；未命中的通用哨兵
// （含 service 包装的 ErrValidationFailed → 400）与未知错误走 FailFromSentinel 兜底。
func failProvider(c *gin.Context, err error) {
	switch {
	case errors.Is(err, providerapi.ErrProviderNotFound):
		respond.Fail(c, http.StatusNotFound, providerapi.ErrProviderNotFound.Error(), "提供商不存在")
	case errors.Is(err, providerapi.ErrProviderNameConflict):
		respond.Fail(c, http.StatusConflict, providerapi.ErrProviderNameConflict.Error(), "提供商名称已存在")
	case errors.Is(err, providerapi.ErrModelInUse):
		respond.Fail(c, http.StatusConflict, providerapi.ErrModelInUse.Error(), "模型已被 Agent / 知识库引用，请先解绑")
	default:
		respond.FailFromSentinel(c, err)
	}
}

// failModel 映射 model 侧业务哨兵；model 操作也可能翻出 provider 哨兵
// （create / update 撞不存在的 provider），一并覆盖。
func failModel(c *gin.Context, err error) {
	switch {
	case errors.Is(err, providerapi.ErrModelNotFound):
		respond.Fail(c, http.StatusNotFound, providerapi.ErrModelNotFound.Error(), "模型不存在")
	case errors.Is(err, providerapi.ErrModelIDConflict):
		respond.Fail(c, http.StatusConflict, providerapi.ErrModelIDConflict.Error(), "同一提供商下模型标识已存在")
	case errors.Is(err, providerapi.ErrModelInUse):
		respond.Fail(c, http.StatusConflict, providerapi.ErrModelInUse.Error(), "模型已被 Agent / 知识库引用，请先解绑")
	case errors.Is(err, providerapi.ErrProviderNotFound):
		respond.Fail(c, http.StatusNotFound, providerapi.ErrProviderNotFound.Error(), "提供商不存在")
	default:
		respond.FailFromSentinel(c, err)
	}
}
