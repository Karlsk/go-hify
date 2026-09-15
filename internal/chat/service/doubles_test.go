package service

// 测试替身：memStore 实现 Store 接口（内存版，含游标/排序语义）；下游依赖（agent /
// provider / llm client 工厂 / executions 落库）为最小 stub；LLM 上游用真实 llm.Client
// 包一个可编程的 fake Streamer（llm.NewClient 导出，无需再抽一层接口）。

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"

	agentapi "github.com/Karlsk/go-hify/internal/agent/api"
	chatapi "github.com/Karlsk/go-hify/internal/chat/api"
	"github.com/Karlsk/go-hify/internal/platform/authctx"
	"github.com/Karlsk/go-hify/internal/platform/llm"
	"github.com/Karlsk/go-hify/internal/platform/logging"
	providerapi "github.com/Karlsk/go-hify/internal/provider/api"
	ragapi "github.com/Karlsk/go-hify/internal/rag/api"
	"gorm.io/gorm"
)

// ---- memStore ----

// memStore Store 的内存实现：按真实 store 的 SQL 语义排序 / 截断 / 归一，
// 附带故障注入钩子（errXXX 命中即返回，测 WARN 不阻断路径）。
type memStore struct {
	mu     sync.Mutex
	nextID uint64
	now    time.Time

	convs    map[uint64]*Conversation
	messages map[uint64][]Message // conversation_id → 正序

	createConvErr      error
	createMsgErr       error
	createMsgFailAfter int // >0 时：第 N 次 CreateMessage 起失败（N=1 即首条 user 消息失败）
	createMsgCalls     int
	deleteErr          error
	touchErr           error
	titleErr           error
	titleCalls         int
}

func newMemStore() *memStore {
	return &memStore{
		nextID:   100,
		now:      time.Now(),
		convs:    map[uint64]*Conversation{},
		messages: map[uint64][]Message{},
	}
}

func (m *memStore) CreateConversation(_ context.Context, c *Conversation) error {
	if m.createConvErr != nil {
		return m.createConvErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	c.ID = m.nextID
	c.CreatedAt, c.UpdatedAt = m.now, m.now
	cp := *c
	m.convs[c.ID] = &cp
	return nil
}

func (m *memStore) GetConversationByID(_ context.Context, id uint64) (*Conversation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.convs[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	cp := *c
	return &cp, nil
}

func (m *memStore) ListConversationsByCursor(_ context.Context, userID uint64, beforeUpdatedAt time.Time, beforeID uint64, limit int) ([]Conversation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var cs []Conversation
	for _, c := range m.convs {
		if c.UserID != userID {
			continue
		}
		if !(beforeUpdatedAt.IsZero() && beforeID == 0) { // 首页零值不带比较条件
			// 行值比较语义：(updated_at, id) < (before_updated_at, before_id) 才保留
			older := c.UpdatedAt.Before(beforeUpdatedAt)
			sameIDLess := c.UpdatedAt.Equal(beforeUpdatedAt) && c.ID < beforeID
			if !older && !sameIDLess {
				continue
			}
		}
		cs = append(cs, *c)
	}
	sort.Slice(cs, func(i, j int) bool {
		if !cs[i].UpdatedAt.Equal(cs[j].UpdatedAt) {
			return cs[i].UpdatedAt.After(cs[j].UpdatedAt)
		}
		return cs[i].ID > cs[j].ID
	})
	if len(cs) > limit {
		cs = cs[:limit]
	}
	return cs, nil
}

func (m *memStore) UpdateConversationTitle(_ context.Context, id uint64, title string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.titleCalls++
	if m.titleErr != nil {
		return m.titleErr
	}
	c, ok := m.convs[id]
	if !ok {
		return gorm.ErrRecordNotFound
	}
	c.Title = title
	return nil
}

func (m *memStore) TouchConversation(_ context.Context, id uint64) error {
	if m.touchErr != nil {
		return m.touchErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.convs[id]
	if !ok {
		return gorm.ErrRecordNotFound
	}
	c.UpdatedAt = c.UpdatedAt.Add(time.Minute) // 单调推进，测试可感知
	return nil
}

func (m *memStore) DeleteConversation(_ context.Context, id uint64) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.convs[id]; !ok {
		return gorm.ErrRecordNotFound
	}
	delete(m.convs, id)
	delete(m.messages, id)
	return nil
}

func (m *memStore) CreateMessage(_ context.Context, msg *Message) error {
	m.mu.Lock()
	m.createMsgCalls++
	calls := m.createMsgCalls
	m.mu.Unlock()
	if m.createMsgErr != nil || (m.createMsgFailAfter > 0 && calls > m.createMsgFailAfter) {
		if m.createMsgErr != nil {
			return m.createMsgErr
		}
		return errors.New("memStore: create message fail-after reached")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	msg.ID = m.nextID
	msg.CreatedAt = m.now
	if msg.ToolCalls == nil {
		msg.ToolCalls = []map[string]any{}
	}
	m.messages[msg.ConversationID] = append(m.messages[msg.ConversationID], *msg)
	return nil
}

func (m *memStore) ListMessagesAfter(_ context.Context, conversationID uint64, afterID uint64, limit int) ([]Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var ms []Message
	for _, msg := range m.messages[conversationID] {
		if msg.ID > afterID {
			ms = append(ms, msg)
		}
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].ID < ms[j].ID })
	if len(ms) > limit {
		ms = ms[:limit]
	}
	return ms, nil
}

func (m *memStore) ListRecentMessages(_ context.Context, conversationID uint64, limit int) ([]Message, error) {
	ms, err := m.ListMessagesAfter(context.Background(), conversationID, 0, limit)
	if err != nil {
		return nil, err
	}
	// 反转为倒序（最新在前），对齐真实 store 语义
	for i, j := 0, len(ms)-1; i < j; i, j = i+1, j-1 {
		ms[i], ms[j] = ms[j], ms[i]
	}
	return ms, nil
}

// seedTurn 依次落一轮 user+assistant 消息（顺序固定，map 迭代随机不可用）。
func (m *memStore) seedTurn(t *testing.T, conversationID uint64, user, assistant string) {
	t.Helper()
	for _, row := range []struct{ role, content string }{
		{chatapi.RoleUser, user},
		{chatapi.RoleAssistant, assistant},
	} {
		if err := m.CreateMessage(context.Background(), &Message{ConversationID: conversationID, Role: row.role, Content: row.content}); err != nil {
			t.Fatalf("seed %s message: %v", row.role, err)
		}
	}
}

func (m *memStore) messagesOf(conversationID uint64) []Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Message{}, m.messages[conversationID]...)
}

func (m *memStore) conversation(id uint64) *Conversation {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.convs[id]; ok {
		cp := *c
		return &cp
	}
	return nil
}

// ---- 下游 stub ----

type stubAgents struct {
	agent *agentapi.AgentDetailSchema
	err   error
	gotID uint64
}

func (s *stubAgents) Get(_ context.Context, req agentapi.GetAgentReq) (*agentapi.AgentDetailSchema, error) {
	s.gotID = req.ID
	if s.err != nil {
		return nil, s.err
	}
	return s.agent, nil
}

func testAgent(enabled bool) *agentapi.AgentDetailSchema {
	return &agentapi.AgentDetailSchema{
		AgentSchema: agentapi.AgentSchema{
			ModelID:          "3",
			SystemPrompt:     "你是 Hify 助手",
			Temperature:      0.7,
			MaxContextTurns:  10,
			Enabled:          enabled,
			RAGTopK:          3,
			RAGMinSimilarity: 0.75,
		},
		ToolIDs: []string{},
	}
}

type stubProviders struct {
	cfg *providerapi.LLMConfig
	err error
}

func (s *stubProviders) ResolveLLMConfig(context.Context, providerapi.ResolveLLMConfigReq) (*providerapi.LLMConfig, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.cfg, nil
}

// stubRags rag 检索注入 stub：按需注入 resp / err；记录调用次数与最近一次请求
// （buildSystemPrompt 分支测试断言 TopK / KBIDs / 是否被调）。
type stubRags struct {
	resp []ragapi.RetrievedChunk
	err  error

	calls   int
	lastReq ragapi.RetrieveReq
}

func (s *stubRags) Retrieve(_ context.Context, req ragapi.RetrieveReq) ([]ragapi.RetrievedChunk, error) {
	s.calls++
	s.lastReq = req
	if s.err != nil {
		return nil, s.err
	}
	return s.resp, nil
}

func testLLMConfig() *providerapi.LLMConfig {
	return &providerapi.LLMConfig{ProviderName: "openai主力", Kind: "openai", ModelID: "gpt-4o"}
}

// execRecorder 记录 executions 落库行（可注入失败测 WARN 路径）。
type execRecorder struct {
	mu   sync.Mutex
	rows []*logging.Execution
	err  error
}

func (e *execRecorder) Create(_ context.Context, x *logging.Execution) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err != nil {
		return e.err
	}
	e.rows = append(e.rows, x)
	return nil
}

func (e *execRecorder) last() *logging.Execution {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.rows) == 0 {
		return nil
	}
	return e.rows[len(e.rows)-1]
}

// ---- LLM 上游 fake ----

// fastProfile 测试用快 Profile：单次尝试（MaxRetries 0）、毫秒级超时与退避。
func fastProfile() llm.Profile {
	p := llm.DefaultProfile()
	p.MaxRetries = 0
	p.TTFT, p.Idle, p.Overall = 500*time.Millisecond, 500*time.Millisecond, 2*time.Second
	p.AcquireTimeout, p.BackoffBase, p.BackoffCap = 100*time.Millisecond, time.Millisecond, 5*time.Millisecond
	p.BreakerAfter, p.BreakerCooldown = 3, 100*time.Millisecond
	return p
}

// captureStreamer 可编程 fake 上游：记录每次收到的消息与选项，按 script 出流 / 出错。
type captureStreamer struct {
	mu     sync.Mutex
	calls  int
	msgs   [][]*schema.Message
	opts   []*llm.CallOptions
	script func(call int) (*schema.StreamReader[*schema.Message], error)
}

func (f *captureStreamer) Stream(_ context.Context, msgs []*schema.Message, opts *llm.CallOptions) (*schema.StreamReader[*schema.Message], error) {
	f.mu.Lock()
	f.calls++
	call := f.calls
	f.msgs = append(f.msgs, msgs)
	f.opts = append(f.opts, opts)
	f.mu.Unlock()
	return f.script(call)
}

func (f *captureStreamer) Generate(_ context.Context, _ []*schema.Message, _ *llm.CallOptions) (*schema.Message, error) {
	return nil, errors.New("captureStreamer: Generate not scripted")
}

func (f *captureStreamer) lastMsgs() []*schema.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.msgs) == 0 {
		return nil
	}
	return f.msgs[len(f.msgs)-1]
}

func (f *captureStreamer) lastOpts() *llm.CallOptions {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.opts) == 0 {
		return nil
	}
	return f.opts[len(f.opts)-1]
}

// chunkStream 把既定 chunks 包装成 eino 流（发完即 EOF），对齐 llm/client_test 的 okStreamer。
func chunkStream(chunks ...*schema.Message) *schema.StreamReader[*schema.Message] {
	r, w := schema.Pipe[*schema.Message](len(chunks) + 1)
	go func() {
		for _, c := range chunks {
			w.Send(c, nil)
		}
		w.Close()
	}()
	return r
}

// failAfterStream 发出 first 后流中报错（测 mid-stream 失败：ErrorEvent / 部分落库）。
func failAfterStream(first *schema.Message, err error) *schema.StreamReader[*schema.Message] {
	r, w := schema.Pipe[*schema.Message](2)
	go func() {
		w.Send(first, nil)
		w.Send(nil, err)
		w.Close()
	}()
	return r
}

// 文本 delta chunk（快捷构造）。
func delta(s string) *schema.Message {
	return &schema.Message{Role: schema.Assistant, Content: s}
}

// 尾 chunk：usage + finish_reason（供应商通常在最后一帧携带 ResponseMeta）。
func tailChunk(prompt, completion int, finish string) *schema.Message {
	return &schema.Message{
		Role:         schema.Assistant,
		ResponseMeta: &schema.ResponseMeta{FinishReason: finish, Usage: &schema.TokenUsage{PromptTokens: prompt, CompletionTokens: completion}},
	}
}

// ---- 其他公共 helper ----

func userCtx() context.Context {
	return authctx.WithUser(context.Background(), &authctx.User{ID: 5, Username: "u5"})
}

type stubClientFactory struct {
	client *llm.Client
	err    error

	gotKey  string
	gotOpts llm.UpstreamOptions
}

func (s *stubClientFactory) Client(key string, opts llm.UpstreamOptions) (*llm.Client, error) {
	s.gotKey, s.gotOpts = key, opts
	if s.err != nil {
		return nil, s.err
	}
	return s.client, nil
}

// newTestService 常规装配：memStore + 启用 agent + 可解析配置 + 可编程 LLM 上游。
func newTestService(t *testing.T, script func(call int) (*schema.StreamReader[*schema.Message], error)) *chatServiceForTest {
	t.Helper()
	return newServiceWithAgent(t, testAgent(true), script)
}

// newServiceWithAgent 指定 agent 形态装配（停用 / 无提示词 / 自定义轮数等场景）。
func newServiceWithAgent(t *testing.T, agent *agentapi.AgentDetailSchema, script func(call int) (*schema.StreamReader[*schema.Message], error)) *chatServiceForTest {
	t.Helper()
	st := newMemStore()
	fs := &captureStreamer{script: script}
	factory := &stubClientFactory{client: llm.NewClient("test", fastProfile(), fs)}
	execs := &execRecorder{}
	rags := &stubRags{}
	svc := New(st,
		&stubAgents{agent: agent},
		&stubProviders{cfg: testLLMConfig()},
		factory,
		execs,
		rags,
	)
	return &chatServiceForTest{svc: svc.(*chatService), store: st, streamer: fs, factory: factory, execs: execs, rags: rags}
}

// chatServiceForTest 聚合句柄，方便断言内部替身状态。
type chatServiceForTest struct {
	svc      *chatService
	store    *memStore
	streamer *captureStreamer
	factory  *stubClientFactory
	execs    *execRecorder
	rags     *stubRags
}

// roles 把消息序列压成 "role:content" 串，断言上下文组装最直观。
func roles(msgs []*schema.Message) string {
	var b strings.Builder
	for i, m := range msgs {
		if i > 0 {
			b.WriteString(" | ")
		}
		b.WriteString(string(m.Role))
		b.WriteString(":")
		b.WriteString(m.Content)
	}
	return b.String()
}
