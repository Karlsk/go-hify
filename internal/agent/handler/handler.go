// Package handler 是 agent 模块的 HTTP 层：gin 路由与绑定。薄绑定，无业务逻辑——
// 参数校验（binding tag + Validate）、调本模块 api 接口、哨兵 errors.Is → 状态码、
// respond 信封包装。
package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	agentapi "github.com/Karlsk/go-hify/internal/agent/api"
	"github.com/Karlsk/go-hify/internal/platform/respond"
	providerapi "github.com/Karlsk/go-hify/internal/provider/api"
)

// Handler 持本模块 api 接口（组合根注入 service 实现）。
type Handler struct {
	agents agentapi.AgentService
}

// New 创建 Handler。
func New(agents agentapi.AgentService) *Handler {
	return &Handler{agents: agents}
}

// RegisterRoutes 挂 agents 一组 5 条路由（REST 资源路径，接口规范）。
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	g := rg.Group("/agents")
	g.POST("", h.create)
	g.GET("", h.list)
	g.GET("/:id", h.get)
	g.PUT("/:id", h.update)
	g.DELETE("/:id", h.delete)
}

// create 创建 Agent（含 body 内嵌的 tool_ids，事务内一并落库）。
func (h *Handler) create(c *gin.Context) {
	var req agentapi.CreateAgentReq
	if !respond.BindJSON(c, &req) {
		return
	}
	s, err := h.agents.Create(c.Request.Context(), req)
	if err != nil {
		failAgent(c, err)
		return
	}
	respond.Created(c, s)
}

// list 活跃 Agent 偏移分页（配置表，接口规范允许偏移分页）。
func (h *Handler) list(c *gin.Context) {
	var req agentapi.ListAgentsReq
	if !respond.BindQuery(c, &req) {
		return
	}
	res, err := h.agents.List(c.Request.Context(), req)
	if err != nil {
		respond.FailFromSentinel(c, err)
		return
	}
	respond.OKWithOffset(c, res.Items, res.Page, res.PageSize, res.Total)
}

// get 详情（含绑定工具 id）。
func (h *Handler) get(c *gin.Context) {
	var req agentapi.GetAgentReq
	if !respond.BindUri(c, &req) {
		return
	}
	d, err := h.agents.Get(c.Request.Context(), req)
	if err != nil {
		failAgent(c, err)
		return
	}
	respond.OK(c, d)
}

// update 整体更新（PUT 语义）：先绑路径 :id 再绑 body；UpdateAgentReq.ID 带 json:"-"
// 防 body 的 id 键覆盖路径值（provider 模块踩坑 #7）。
func (h *Handler) update(c *gin.Context) {
	var idReq agentapi.GetAgentReq
	if !respond.BindUri(c, &idReq) {
		return
	}
	var req agentapi.UpdateAgentReq
	req.ID = idReq.ID
	if !respond.BindJSON(c, &req) {
		return
	}
	s, err := h.agents.Update(c.Request.Context(), req)
	if err != nil {
		failAgent(c, err)
		return
	}
	respond.OK(c, s)
}

// delete 软删除（204 无返回体；绑定保留、历史会话不动、新会话被拒）。
func (h *Handler) delete(c *gin.Context) {
	var req agentapi.DeleteAgentReq
	if !respond.BindUri(c, &req) {
		return
	}
	if err := h.agents.Delete(c.Request.Context(), req); err != nil {
		failAgent(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// failAgent 映射 agent 侧业务哨兵。agent 操作也可能翻出 provider 哨兵
// （create / update 撞不存在的 model）——service 透传 providerapi.ErrModelNotFound，
// 此处一并映射。import providerapi 只用其哨兵做 errors.Is（agent → provider 是
// 白名单依赖方向、且只 import api 包，符合《跨模块调用规则》；platform/respond
// 的通用兜底不认业务模块哨兵，映射责任在本模块 handler）。
func failAgent(c *gin.Context, err error) {
	switch {
	case errors.Is(err, agentapi.ErrAgentNotFound):
		respond.Fail(c, http.StatusNotFound, agentapi.ErrAgentNotFound.Error(), "Agent 不存在")
	case errors.Is(err, agentapi.ErrToolNotFound):
		respond.Fail(c, http.StatusNotFound, agentapi.ErrToolNotFound.Error(), "绑定的工具不存在")
	case errors.Is(err, providerapi.ErrModelNotFound):
		respond.Fail(c, http.StatusNotFound, providerapi.ErrModelNotFound.Error(), "模型不存在")
	default:
		respond.FailFromSentinel(c, err)
	}
}
