// Package handler 是 mcp 模块的 HTTP 层：gin 路由与绑定。薄绑定，无业务逻辑——
// 参数校验（binding tag + Validate）、调本模块 api 接口（MCPServerService 管理面）、
// 哨兵 errors.Is → 状态码、respond 信封包装。
package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	mcpapi "github.com/Karlsk/go-hify/internal/mcp/api"
	"github.com/Karlsk/go-hify/internal/platform/respond"
)

// Handler 持本模块 api 接口（组合根注入 service 实现的管理面面孔）。
type Handler struct {
	servers mcpapi.MCPServerService
}

// New 创建 Handler。
func New(servers mcpapi.MCPServerService) *Handler {
	return &Handler{servers: servers}
}

// RegisterRoutes 挂 mcp-servers 一组 7 条路由（资源名与 CLAUDE.md 资源清单一致；
// 连通测试与工具发现合一走 /discover，实调工具走 /run）。
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	g := rg.Group("/mcp-servers")
	g.POST("", h.create)
	g.GET("", h.list)
	g.GET("/:id", h.get)
	g.PUT("/:id", h.update)
	g.DELETE("/:id", h.delete)
	g.POST("/:id/discover", h.discover)
	g.POST("/:id/run", h.runTool)
}

// create 创建 MCP Server（创建即启用，enabled 未传 = true）。
func (h *Handler) create(c *gin.Context) {
	var req mcpapi.CreateMCPServerReq
	if !respond.BindJSON(c, &req) {
		return
	}
	s, err := h.servers.Create(c.Request.Context(), req)
	if err != nil {
		failMCPServer(c, err)
		return
	}
	respond.Created(c, s)
}

// list 偏移分页（极小配置表，接口规范允许偏移分页）。
func (h *Handler) list(c *gin.Context) {
	var req mcpapi.ListMCPServersReq
	if !respond.BindQuery(c, &req) {
		return
	}
	res, err := h.servers.List(c.Request.Context(), req)
	if err != nil {
		respond.FailFromSentinel(c, err)
		return
	}
	respond.OKWithOffset(c, res.Items, res.Page, res.PageSize, res.Total)
}

// get 详情（含 env 与工具清单，DB 读不连 server）。
func (h *Handler) get(c *gin.Context) {
	var req mcpapi.GetMCPServerReq
	if !respond.BindUri(c, &req) {
		return
	}
	d, err := h.servers.Get(c.Request.Context(), req)
	if err != nil {
		failMCPServer(c, err)
		return
	}
	respond.OK(c, d)
}

// update 整体更新（PUT 语义）：先绑路径 :id 再绑 body；UpdateMCPServerReq.ID 带
// json:"-" 防 body 的 id 键覆盖路径值（provider 模块踩坑 #7）。
func (h *Handler) update(c *gin.Context) {
	var idReq mcpapi.GetMCPServerReq
	if !respond.BindUri(c, &idReq) {
		return
	}
	var req mcpapi.UpdateMCPServerReq
	req.ID = idReq.ID
	if !respond.BindJSON(c, &req) {
		return
	}
	s, err := h.servers.Update(c.Request.Context(), req)
	if err != nil {
		failMCPServer(c, err)
		return
	}
	respond.OK(c, s)
}

// delete 真删（204 无返回体）：mcp_tools 级联删；有 Agent 绑定 → 409 InUse。
func (h *Handler) delete(c *gin.Context) {
	var req mcpapi.DeleteMCPServerReq
	if !respond.BindUri(c, &req) {
		return
	}
	if err := h.servers.Delete(c.Request.Context(), req); err != nil {
		failMCPServer(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// discover 连通测试 + 工具发现（一个动作完成）：动作完成即 200，成败在
// DiscoverResultSchema 字段（provider TestConnection 同款）；server 不存在才 404。
func (h *Handler) discover(c *gin.Context) {
	var req mcpapi.DiscoverReq
	if !respond.BindUri(c, &req) {
		return
	}
	res, err := h.servers.Discover(c.Request.Context(), req)
	if err != nil {
		failMCPServer(c, err)
		return
	}
	respond.OK(c, res)
}

// runTool 实调一个工具（管理面诊断）：两段绑定——先路径 :id 再 body
// （tool_name / arguments）；RunToolReq.ID 带 json:"-" 同防 body 改写。
func (h *Handler) runTool(c *gin.Context) {
	var idReq mcpapi.GetMCPServerReq
	if !respond.BindUri(c, &idReq) {
		return
	}
	var req mcpapi.RunToolReq
	req.ID = idReq.ID
	if !respond.BindJSON(c, &req) {
		return
	}
	res, err := h.servers.RunTool(c.Request.Context(), req)
	if err != nil {
		failMCPServer(c, err)
		return
	}
	respond.OK(c, res)
}

// failMCPServer 映射 mcp 侧业务哨兵（HTTP 码与 api/errors.go 各哨兵注释一致）；
// 通用哨兵与未识别错误走 FailFromSentinel（errs.ErrValidationFailed → 400 等，
// 兜底 500 + 记日志）。
func failMCPServer(c *gin.Context, err error) {
	switch {
	case errors.Is(err, mcpapi.ErrMCPServerNotFound):
		respond.Fail(c, http.StatusNotFound, mcpapi.ErrMCPServerNotFound.Error(), "MCP Server 不存在")
	case errors.Is(err, mcpapi.ErrMCPServerNameConflict):
		respond.Fail(c, http.StatusConflict, mcpapi.ErrMCPServerNameConflict.Error(), "同名 MCP Server 已存在")
	case errors.Is(err, mcpapi.ErrMCPServerInUse):
		respond.Fail(c, http.StatusConflict, mcpapi.ErrMCPServerInUse.Error(), "有 Agent 绑定了该 Server 的工具，无法删除；可先解绑或改为停用")
	case errors.Is(err, mcpapi.ErrMCPToolNotFound):
		respond.Fail(c, http.StatusNotFound, mcpapi.ErrMCPToolNotFound.Error(), "工具不存在（未 discover 入库或名字有误）")
	case errors.Is(err, mcpapi.ErrMCPToolCallFailed):
		respond.Fail(c, http.StatusInternalServerError, mcpapi.ErrMCPToolCallFailed.Error(), "工具调用失败："+err.Error())
	default:
		respond.FailFromSentinel(c, err)
	}
}
