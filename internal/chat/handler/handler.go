// Package handler 是 chat 模块的 HTTP 层：gin 路由与绑定。薄绑定，无业务逻辑——
// 参数校验（binding tag + Validate）、调本模块 api 接口、哨兵 errors.Is → 状态码、
// respond 信封包装；SSE 流式走 emit 回调 + 心跳保活。
package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	agentapi "github.com/Karlsk/go-hify/internal/agent/api"
	chatapi "github.com/Karlsk/go-hify/internal/chat/api"
	"github.com/Karlsk/go-hify/internal/platform/llm"
	"github.com/Karlsk/go-hify/internal/platform/respond"
)

const (
	// pingInterval SSE 心跳间隔（防中间层掐静默连接：Ollama TTFT 120s / 思考模型首字久 / 工具执行慢）。
	pingInterval = 15 * time.Second
)

// idReq 路径参数绑定专用（ShouldBindUri 会触发所有 binding tag 校验，
// 与 body 的 required 字段放在同一 struct 会导致 URI 绑定时 body 字段校验失败）。
type idReq struct {
	ID uint64 `uri:"id" binding:"required"`
}

func (r idReq) Validate() error { return nil }

// Handler 持本模块 api 接口（组合根注入 service 实现）。
type Handler struct {
	svc chatapi.ChatService
}

// New 创建 Handler。
func New(svc chatapi.ChatService) *Handler {
	return &Handler{svc: svc}
}

// RegisterRoutes 挂 chat 的路由（REST 资源路径，接口规范）。
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	g := rg.Group("/conversations")
	g.POST("", h.create)               // 建会话
	g.GET("", h.list)                  // 会话列表
	g.GET("/:id/messages", h.messages) // 历史消息
	g.DELETE("/:id", h.del)            // 删除会话
	g.POST("/:id/messages", h.sendMsg) // 发消息（stream / non-stream 两模式）
}

// create 建会话（绑 Agent）。
func (h *Handler) create(c *gin.Context) {
	var req chatapi.CreateConversationReq
	if !respond.BindJSON(c, &req) {
		return
	}
	s, err := h.svc.CreateConversation(c.Request.Context(), req)
	if err != nil {
		failChat(c, err)
		return
	}
	respond.Created(c, s)
}

// list 会话列表（keyset 游标）。
func (h *Handler) list(c *gin.Context) {
	var req chatapi.ListConversationsReq
	if !respond.BindQuery(c, &req) {
		return
	}
	res, err := h.svc.ListConversations(c.Request.Context(), req)
	if err != nil {
		respond.FailFromSentinel(c, err)
		return
	}
	respond.OKWithCursor(c, res.Items, res.Limit, res.HasMore, res.NextCursor)
}

// messages 历史消息（after_id 游标，正序）。
func (h *Handler) messages(c *gin.Context) {
	var req chatapi.ListMessagesReq
	if !respond.BindUri(c, &req) {
		return
	}
	if !respond.BindQuery(c, &req) {
		return
	}
	res, err := h.svc.ListMessages(c.Request.Context(), req)
	if err != nil {
		if errors.Is(err, chatapi.ErrConversationNotFound) {
			respond.Fail(c, http.StatusNotFound, chatapi.ErrConversationNotFound.Error(), "会话不存在")
			return
		}
		respond.FailFromSentinel(c, err)
		return
	}
	// after_id 游标：uint64 → string，前端原样回传 after_id 参数。
	var nextCursor string
	if res.HasMore && res.NextAfterID > 0 {
		nextCursor = strconv.FormatUint(res.NextAfterID, 10)
	}
	respond.OKWithCursor(c, res.Items, res.Limit, res.HasMore, nextCursor)
}

// del 删除会话（204 无返回体）。
func (h *Handler) del(c *gin.Context) {
	var req chatapi.DeleteConversationReq
	if !respond.BindUri(c, &req) {
		return
	}
	if err := h.svc.DeleteConversation(c.Request.Context(), req); err != nil {
		if errors.Is(err, chatapi.ErrConversationNotFound) {
			respond.Fail(c, http.StatusNotFound, chatapi.ErrConversationNotFound.Error(), "会话不存在")
			return
		}
		respond.FailFromSentinel(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// sendMsg 发消息入口：按 stream 字段分支到流式 / 一次输出模式。
// 路由统一走 POST（SSE 要带 body + 鉴权头），绝不用 GET / EventSource。
//
// 注意：URI 绑定（ShouldBindUri）会触发所有 binding tag 的校验——如果 ConversationID 和
// Content 在同一个 struct 上，URI 绑定时 Content 的 required 就会失败。
// 所以先用 uriReq 只绑路径参数，再用 BindJSON 绑 body 到完整 req。
func (h *Handler) sendMsg(c *gin.Context) {
	var uriReq idReq
	if !respond.BindUri(c, &uriReq) {
		return
	}
	var req chatapi.SendMessageReq
	req.ConversationID = uriReq.ID
	if !respond.BindJSON(c, &req) {
		return
	}
	if req.WantStream() {
		h.streamSSE(c, req)
		return
	}
	h.sendOnce(c, req)
}

// sendOnce 一次输出模式（stream=false）：标准 respond 信封。
func (h *Handler) sendOnce(c *gin.Context, req chatapi.SendMessageReq) {
	reply, err := h.svc.SendMessage(c.Request.Context(), req)
	if err != nil {
		failChat(c, err)
		return
	}
	respond.OK(c, reply)
}

// streamSSE 流式模式（stream=true）：200 + text/event-stream → emit 回调逐帧写出。
//
// 帧格式：data: JSON\n\n，type 字段判别（fetch ReadableStream 解析比 EventSource 少一层 event 命名行）；
// 空闲期间每 15s 发一行 SSE 注释（: ping）防中间层掐静默连接。
//
// 错误约定（与 api.ChatService.Stream 注释一致）：
//   - emit 之前的失败（会话不存在、agent 停用、provider 限流…）：200 头尚未写出，
//     handler 回标准错误信封（正常 HTTP 状态码）；
//   - 流已开始后的失败：service 内部经 ErrorEvent emit 后返回 nil，HTTP 状态码不可改。
func (h *Handler) streamSSE(c *gin.Context, req chatapi.SendMessageReq) {
	ctx := c.Request.Context()

	// 惰性提交：首次 emit 调用时才写 200 头 + Content-Type；在此之前 service 返回的 error
	// 可以走标准错误信封（正常的 HTTP 状态码）。
	headerWritten := false
	writeHeader := func() {
		if headerWritten {
			return
		}
		headerWritten = true
		hdr := c.Writer.Header()
		hdr.Set("Content-Type", "text/event-stream")
		hdr.Set("Cache-Control", "no-cache")
		hdr.Set("Connection", "keep-alive")
		hdr.Set("X-Accel-Buffering", "no") // nginx 不缓冲 SSE
		c.Writer.WriteHeader(http.StatusOK)
	}

	// 心跳 goroutine：仅在流开始后启动，流结束后停止。
	// stop channel 由主 goroutine 关闭，通知心跳退出；goroutine 的 defer 保证 handler 返回前回收。
	stop := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(pingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !headerWritten {
					continue // 流尚未开始：service 还在 setupTurn / loadHistory / 等 LLM 首 token
				}
				fmt.Fprint(c.Writer, ": ping\n\n")
				c.Writer.Flush()
			}
		}
	}()

	emit := func(ev chatapi.StreamEvent) error {
		writeHeader()
		data, err := json.Marshal(ev)
		if err != nil {
			return err
		}
		fmt.Fprintf(c.Writer, "data: %s\n\n", data)
		c.Writer.Flush()
		return nil
	}

	// service.Stream 返回 error 只发生在 emit 之前（200 头尚未写出，可以走标准信封）。
	if err := h.svc.Stream(ctx, req, emit); err != nil {
		failChat(c, err)
	}

	// 流结束：停止心跳并等待 goroutine 退出（确保无残留 goroutine 泄漏）
	close(stop)
	<-stopped
	// err == nil：要么正常结束（done 事件已发），要么流中失败（error 事件已发），
	// 要么客户端断连（静默收尾）。三种情况均无需额外动作。
}

// failChat 映射 chat 侧业务哨兵与透传的上游哨兵。
// service 调用链产生的错误经 translateLLMError 翻译后，会携带 llm / provider / agent 哨兵；
// 响应侧统一在 handler 映射（respond 不能 import 业务域）。
func failChat(c *gin.Context, err error) {
	switch {
	case errors.Is(err, chatapi.ErrConversationNotFound):
		respond.Fail(c, http.StatusNotFound, chatapi.ErrConversationNotFound.Error(), "会话不存在")
	case errors.Is(err, chatapi.ErrModelContextTooLong):
		respond.Fail(c, http.StatusBadRequest, chatapi.ErrModelContextTooLong.Error(), "模型上下文超长或请求被拒")
	case errors.Is(err, agentapi.ErrAgentNotFound):
		respond.Fail(c, http.StatusNotFound, agentapi.ErrAgentNotFound.Error(), "Agent 不存在")
	case errors.Is(err, agentapi.ErrAgentDisabled):
		respond.Fail(c, http.StatusServiceUnavailable, agentapi.ErrAgentDisabled.Error(), "Agent 已停用")
	case errors.Is(err, llm.ErrProviderBusy):
		respond.Fail(c, http.StatusServiceUnavailable, llm.ErrProviderBusy.Error(), "供应商并发已满，请稍后重试")
	case errors.Is(err, llm.ErrProviderUnavailable):
		respond.Fail(c, http.StatusServiceUnavailable, llm.ErrProviderUnavailable.Error(), "供应商暂不可用，请稍后重试")
	default:
		respond.FailFromSentinel(c, err) // 通用哨兵（限流 / 预算 / 验证 / 服务不可用）兜底
	}
}
