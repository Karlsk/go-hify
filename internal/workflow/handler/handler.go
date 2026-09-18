// Package handler 是 workflow 模块的 HTTP 层：薄绑定——RegisterRoutes + 8 绑定函数，
// 每个函数只调一个 api 接口方法；错误经 errors.Is 映射状态码（spec 04 §2.2 表 + 执行
// 引擎下游哨兵，spec 06），respond 信封包装。
package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Karlsk/go-hify/internal/platform/llm"
	"github.com/Karlsk/go-hify/internal/platform/respond"
	providerapi "github.com/Karlsk/go-hify/internal/provider/api"
	ragapi "github.com/Karlsk/go-hify/internal/rag/api"
	workflowapi "github.com/Karlsk/go-hify/internal/workflow/api"
)

// Handler 持有本模块 api 接口，组合根注入 service 实现。
type Handler struct{ svc workflowapi.WorkflowService }

// New 创建 Handler。
func New(svc workflowapi.WorkflowService) *Handler { return &Handler{svc: svc} }

// RegisterRoutes 注册 workflows 一组 8 路由（/api/v1 前缀与 auth 中间件由组合根挂）。
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	g := rg.Group("/workflows")
	g.POST("", h.create)                // 201 Created(detail)
	g.GET("", h.list)                   // 200 OKWithOffset(items, page, page_size, total)
	g.GET("/:id", h.get)                // 200 OK(detail)
	g.PUT("/:id", h.update)             // 200 OK(detail)
	g.DELETE("/:id", h.delete)          // 204 无响应体
	g.POST("/:id/publish", h.publish)   // 200 OK(summary)
	g.POST("/:id/disable", h.disable)   // 200 OK(summary)
	g.POST("/:id/execute", h.execute)   // 200 OK(run result)，?trial=true 试运行（spec 06）
}

func (h *Handler) create(c *gin.Context) {
	var req workflowapi.UpsertReq
	if !respond.BindJSON(c, &req) { // 绑定 + R1-R8 图校验合一步，失败已写 400 信封
		return
	}
	d, err := h.svc.Create(c.Request.Context(), req)
	if err != nil {
		failWorkflow(c, err)
		return
	}
	respond.Created(c, d)
}

func (h *Handler) get(c *gin.Context) {
	var req workflowapi.GetWorkflowReq
	if !respond.BindUri(c, &req) {
		return
	}
	d, err := h.svc.Get(c.Request.Context(), req)
	if err != nil {
		failWorkflow(c, err)
		return
	}
	respond.OK(c, d)
}

func (h *Handler) list(c *gin.Context) {
	var req workflowapi.ListWorkflowsReq
	if !respond.BindQuery(c, &req) {
		return
	}
	res, err := h.svc.List(c.Request.Context(), req)
	if err != nil {
		failWorkflow(c, err)
		return
	}
	respond.OKWithOffset(c, res.Items, res.Page, res.PageSize, res.Total)
}

func (h *Handler) update(c *gin.Context) {
	// 路径参数与请求体分开绑定（gin 的 Bind* 校验整个结构体）：先绑 :id 再绑 body
	//（provider.update 同款，UpdateWorkflowReq.ID 无 binding tag 即为此设计）。
	var idReq workflowapi.GetWorkflowReq
	if !respond.BindUri(c, &idReq) {
		return
	}
	var req workflowapi.UpdateWorkflowReq
	req.ID = idReq.ID
	if !respond.BindJSON(c, &req) {
		return
	}
	d, err := h.svc.Update(c.Request.Context(), req)
	if err != nil {
		failWorkflow(c, err)
		return
	}
	respond.OK(c, d)
}

func (h *Handler) delete(c *gin.Context) {
	var req workflowapi.DeleteWorkflowReq
	if !respond.BindUri(c, &req) {
		return
	}
	if err := h.svc.Delete(c.Request.Context(), req); err != nil {
		failWorkflow(c, err)
		return
	}
	c.Status(http.StatusNoContent) // 204 无响应体（agent delete 同款）
}

func (h *Handler) publish(c *gin.Context) {
	var req workflowapi.PublishWorkflowReq
	if !respond.BindUri(c, &req) {
		return
	}
	s, err := h.svc.Publish(c.Request.Context(), req)
	if err != nil {
		failWorkflow(c, err)
		return
	}
	respond.OK(c, s)
}

func (h *Handler) disable(c *gin.Context) {
	var req workflowapi.DisableWorkflowReq
	if !respond.BindUri(c, &req) {
		return
	}
	s, err := h.svc.Disable(c.Request.Context(), req)
	if err != nil {
		failWorkflow(c, err)
		return
	}
	respond.OK(c, s)
}

// execute 执行工作流（spec 06 FR1）：两段绑定（:id 路径 + input body，update 同款）
// → ?trial=true 透传试运行标记（仅字面 true 生效，O3）→ 同步拿结果一次返回。
func (h *Handler) execute(c *gin.Context) {
	var idReq workflowapi.GetWorkflowReq
	if !respond.BindUri(c, &idReq) {
		return
	}
	var req workflowapi.ExecuteWorkflowReq
	req.ID = idReq.ID
	if !respond.BindJSON(c, &req) {
		return
	}
	req.Trial = c.Query("trial") == "true"
	res, err := h.svc.Execute(c.Request.Context(), req)
	if err != nil {
		failWorkflow(c, err)
		return
	}
	respond.OK(c, res)
}

// failWorkflow 模块哨兵映射（spec 04 §2.2 表 + spec 06 执行侧新增）：显式 errors.Is →
// 状态码 + code（= 哨兵 Error()）。ErrWorkflowExecutionFailed → 500 环境限制类（O4）；
// 下游哨兵（模型 / KB 不存在、供应商忙 / 熔断）原样透传 errors.Is 链命中；其余走
// FailFromSentinel（通用哨兵自动映射——VALIDATION_FAILED → 400 等——兜底 500）。
func failWorkflow(c *gin.Context, err error) {
	switch {
	case errors.Is(err, workflowapi.ErrWorkflowNotFound):
		respond.Fail(c, http.StatusNotFound, workflowapi.ErrWorkflowNotFound.Error(), err.Error())
	case errors.Is(err, workflowapi.ErrWorkflowNameConflict):
		respond.Fail(c, http.StatusConflict, workflowapi.ErrWorkflowNameConflict.Error(), err.Error())
	case errors.Is(err, workflowapi.ErrWorkflowInUse): // 409：被 agent 绑定挡删（spec 05）
		respond.Fail(c, http.StatusConflict, workflowapi.ErrWorkflowInUse.Error(), err.Error())
	case errors.Is(err, workflowapi.ErrWorkflowNotPublished):
		respond.Fail(c, http.StatusServiceUnavailable, workflowapi.ErrWorkflowNotPublished.Error(), err.Error())
	case errors.Is(err, workflowapi.ErrWorkflowExecutionFailed): // 500：环境限制类（O4 二分法）
		respond.Fail(c, http.StatusInternalServerError, workflowapi.ErrWorkflowExecutionFailed.Error(), err.Error())
	case errors.Is(err, providerapi.ErrModelNotFound): // 404：llm 节点 model_id 解析失败（透传）
		respond.Fail(c, http.StatusNotFound, providerapi.ErrModelNotFound.Error(), err.Error())
	case errors.Is(err, ragapi.ErrKnowledgeBaseNotFound): // 404：检索节点 KB 不存在（透传）
		respond.Fail(c, http.StatusNotFound, ragapi.ErrKnowledgeBaseNotFound.Error(), err.Error())
	case errors.Is(err, llm.ErrProviderBusy): // 503：bulkhead fail-fast（透传）
		respond.Fail(c, http.StatusServiceUnavailable, llm.ErrProviderBusy.Error(), err.Error())
	case errors.Is(err, llm.ErrProviderUnavailable): // 503：熔断打开（透传）
		respond.Fail(c, http.StatusServiceUnavailable, llm.ErrProviderUnavailable.Error(), err.Error())
	default:
		respond.FailFromSentinel(c, err)
	}
}
