package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentapi "github.com/Karlsk/go-hify/internal/agent/api"
	chatapi "github.com/Karlsk/go-hify/internal/chat/api"
	"github.com/Karlsk/go-hify/internal/platform/errs"
	"github.com/Karlsk/go-hify/internal/platform/llm"
	platformschema "github.com/Karlsk/go-hify/internal/platform/schema"
)

// ---- test double: stub ChatService ----

type stubChatService struct {
	conv   *chatapi.ConversationSchema
	list   *chatapi.ConversationListResult
	msgs   *chatapi.MessageListResult
	reply  *chatapi.AssistantReplySchema
	events []chatapi.StreamEvent // stream 模式按序回调
	err    error                 // 所有方法共用

	// 录参
	gotCreateReq *chatapi.CreateConversationReq
	gotStreamReq *chatapi.SendMessageReq
}

func (s *stubChatService) CreateConversation(_ context.Context, req chatapi.CreateConversationReq) (*chatapi.ConversationSchema, error) {
	s.gotCreateReq = &req
	return s.conv, s.err
}
func (s *stubChatService) ListConversations(_ context.Context, _ chatapi.ListConversationsReq) (*chatapi.ConversationListResult, error) {
	return s.list, s.err
}
func (s *stubChatService) ListMessages(_ context.Context, _ chatapi.ListMessagesReq) (*chatapi.MessageListResult, error) {
	return s.msgs, s.err
}
func (s *stubChatService) DeleteConversation(_ context.Context, _ chatapi.DeleteConversationReq) error {
	return s.err
}
func (s *stubChatService) SendMessage(_ context.Context, _ chatapi.SendMessageReq) (*chatapi.AssistantReplySchema, error) {
	return s.reply, s.err
}
func (s *stubChatService) Stream(_ context.Context, req chatapi.SendMessageReq, emit func(chatapi.StreamEvent) error) error {
	s.gotStreamReq = &req
	if s.err != nil {
		return s.err
	}
	for _, ev := range s.events {
		if err := emit(ev); err != nil {
			return nil // service 内 clientGone → return nil
		}
	}
	return nil
}

// 编译期断言
var _ chatapi.ChatService = (*stubChatService)(nil)

// ---- helpers ----

func setupRouter(svc chatapi.ChatService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	v1 := r.Group("/api/v1")
	New(svc).RegisterRoutes(v1)
	return r
}

func doJSON(t *testing.T, r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func jsonBody(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func convSchema(id string) *chatapi.ConversationSchema {
	return &chatapi.ConversationSchema{BaseSchema: platformschema.BaseSchema{ID: id}, AgentID: "3"}
}

// ---- create ----

func TestCreateConversationOK(t *testing.T) {
	svc := &stubChatService{conv: convSchema("1")}
	w := doJSON(t, setupRouter(svc), http.MethodPost, "/api/v1/conversations", gin.H{"agent_id": 3})
	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, uint64(3), svc.gotCreateReq.AgentID)
}

func TestCreateConversationAgentNotFound(t *testing.T) {
	svc := &stubChatService{err: agentapi.ErrAgentNotFound}
	w := doJSON(t, setupRouter(svc), http.MethodPost, "/api/v1/conversations", gin.H{"agent_id": 99})
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), agentapi.ErrAgentNotFound.Error())
}

func TestCreateConversationAgentDisabled(t *testing.T) {
	svc := &stubChatService{err: agentapi.ErrAgentDisabled}
	w := doJSON(t, setupRouter(svc), http.MethodPost, "/api/v1/conversations", gin.H{"agent_id": 3})
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), agentapi.ErrAgentDisabled.Error())
}

// ---- list ----

func TestListConversationsOK(t *testing.T) {
	svc := &stubChatService{list: &chatapi.ConversationListResult{
		Items: []chatapi.ConversationSchema{*convSchema("1")}, Limit: 20, HasMore: false,
	}}
	w := doJSON(t, setupRouter(svc), http.MethodGet, "/api/v1/conversations", nil)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"has_more":false`)
}

// ---- messages ----

func TestListMessagesOK(t *testing.T) {
	svc := &stubChatService{msgs: &chatapi.MessageListResult{
		Items: []chatapi.MessageSchema{{ID: "10"}}, Limit: 20, HasMore: true, NextAfterID: 10,
	}}
	r := setupRouter(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/conversations/101/messages?after_id=5&limit=20", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"next_cursor":"10"`)
}

func TestListMessagesNotFound(t *testing.T) {
	svc := &stubChatService{err: chatapi.ErrConversationNotFound}
	r := setupRouter(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/conversations/999/messages", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ---- delete ----

func TestDeleteConversationOK(t *testing.T) {
	svc := &stubChatService{}
	r := setupRouter(svc)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/conversations/1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestDeleteConversationNotFound(t *testing.T) {
	svc := &stubChatService{err: chatapi.ErrConversationNotFound}
	r := setupRouter(svc)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/conversations/999", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ---- sendMsg (non-stream) ----

func TestSendMessageOnceOK(t *testing.T) {
	f := false
	svc := &stubChatService{reply: &chatapi.AssistantReplySchema{Content: "你好", MessageID: "5"}}
	w := doJSON(t, setupRouter(svc), http.MethodPost, "/api/v1/conversations/101/messages",
		gin.H{"content": "hi", "stream": &f})
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "你好")
}

func TestSendMessageOnceModelContextTooLong(t *testing.T) {
	f := false
	svc := &stubChatService{err: chatapi.ErrModelContextTooLong}
	w := doJSON(t, setupRouter(svc), http.MethodPost, "/api/v1/conversations/101/messages",
		gin.H{"content": "x", "stream": &f})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), chatapi.ErrModelContextTooLong.Error())
}

// ---- sendMsg (stream SSE) ----

func TestStreamHappyPath(t *testing.T) {
	svc := &stubChatService{events: []chatapi.StreamEvent{
		chatapi.DeltaEvent("你好"),
		chatapi.DoneEvent("5", chatapi.Usage{Input: 10, Output: 2}, "stop"),
	}}
	r := setupRouter(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/conversations/101/messages",
		bytes.NewReader(jsonBody(gin.H{"content": "hi", "stream": true})))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "text/event-stream", w.Header().Get("Content-Type"))
	assert.Equal(t, "no", w.Header().Get("X-Accel-Buffering"))
	body := w.Body.String()
	assert.Contains(t, body, `"type":"delta"`)
	assert.Contains(t, body, "你好")
	assert.Contains(t, body, `"type":"done"`)
	assert.Equal(t, uint64(101), svc.gotStreamReq.ConversationID)
}

func TestStreamPreEmitErrorReturnsEnvelope(t *testing.T) {
	svc := &stubChatService{err: chatapi.ErrConversationNotFound}
	r := setupRouter(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/conversations/999/messages",
		bytes.NewReader(jsonBody(gin.H{"content": "hi", "stream": true})))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// 流未开始（emit 从未调用）→ 标准错误信封 + 正常 HTTP 状态码
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NotContains(t, w.Header().Get("Content-Type"), "text/event-stream")
}

func TestStreamMidStreamErrorSendsErrorEvent(t *testing.T) {
	svc := &stubChatService{events: []chatapi.StreamEvent{
		chatapi.DeltaEvent("半"),
		chatapi.ErrorEvent(chatapi.ErrModelContextTooLong.Error(), "上下文超长", false),
	}}
	r := setupRouter(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/conversations/101/messages",
		bytes.NewReader(jsonBody(gin.H{"content": "hi", "stream": true})))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code) // 流已200开始，状态码不可改
	body := w.Body.String()
	assert.Contains(t, body, `"type":"error"`)
	assert.Contains(t, body, `"retryable":false`)
}

func TestStreamProviderBusy503(t *testing.T) {
	svc := &stubChatService{err: llm.ErrProviderBusy}
	r := setupRouter(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/conversations/101/messages",
		bytes.NewReader(jsonBody(gin.H{"content": "hi", "stream": true})))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), llm.ErrProviderBusy.Error())
}

// ---- failChat 覆盖 ----

func TestFailChatRateLimited(t *testing.T) {
	svc := &stubChatService{err: errs.ErrRateLimited}
	f := false
	w := doJSON(t, setupRouter(svc), http.MethodPost, "/api/v1/conversations/101/messages",
		gin.H{"content": "x", "stream": &f})
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
}

func TestFailChatInternalError(t *testing.T) {
	svc := &stubChatService{err: errors.New("unexpected boom")}
	f := false
	w := doJSON(t, setupRouter(svc), http.MethodPost, "/api/v1/conversations/101/messages",
		gin.H{"content": "x", "stream": &f})
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "INTERNAL_ERROR")
}

func TestFailChatProviderUnavailable(t *testing.T) {
	svc := &stubChatService{err: llm.ErrProviderUnavailable}
	f := false
	w := doJSON(t, setupRouter(svc), http.MethodPost, "/api/v1/conversations/101/messages",
		gin.H{"content": "x", "stream": &f})
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// ---- binding 验证 ----

func TestCreateValidationFail(t *testing.T) {
	svc := &stubChatService{}
	w := doJSON(t, setupRouter(svc), http.MethodPost, "/api/v1/conversations", gin.H{})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), errs.ErrValidationFailed.Error())
}

func TestSendMessageValidationFail(t *testing.T) {
	svc := &stubChatService{}
	// content 缺失（binding:"required"）
	w := doJSON(t, setupRouter(svc), http.MethodPost, "/api/v1/conversations/1/messages",
		gin.H{"stream": false})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}
