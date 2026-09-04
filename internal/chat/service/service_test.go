package service

// service 层测试（stub Store + stub 下游 api）：会话 CRUD、游标分页、属主校验、
// 整轮截断纯函数。编排链路（发消息两模式）见 turn_test.go。

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"

	agentapi "github.com/Karlsk/go-hify/internal/agent/api"
	chatapi "github.com/Karlsk/go-hify/internal/chat/api"
	"github.com/Karlsk/go-hify/internal/platform/authctx"
	"github.com/Karlsk/go-hify/internal/platform/db"
	"github.com/Karlsk/go-hify/internal/platform/errs"
	"github.com/Karlsk/go-hify/internal/platform/page"
)

// newCRUDService 最小装配（无 LLM 交互；脚本不会被触发）。
func newCRUDService(agents agentGetter, providers llmConfigResolver) (*chatService, *memStore) {
	st := newMemStore()
	svc := New(st, agents, providers,
		&stubClientFactory{client: nil}, &execRecorder{}).(*chatService)
	return svc, st
}

func enabledAgents() agentGetter { return &stubAgents{agent: testAgent(true)} }
func dummyProviders() llmConfigResolver {
	return &stubProviders{cfg: testLLMConfig()}
}

// ---- CreateConversation ----

func TestCreateConversation(t *testing.T) {
	svc, st := newCRUDService(enabledAgents(), dummyProviders())

	c, err := svc.CreateConversation(userCtx(), chatapi.CreateConversationReq{AgentID: 3})
	assert.NoError(t, err)
	assert.Equal(t, "3", c.AgentID) // 字符串化外键
	assert.Empty(t, c.Title)
	assert.NotEmpty(t, c.ID)
	assert.NotZero(t, c.CreatedAt)

	conv := st.conversation(assertConvID(t, c))
	assert.NotNil(t, conv)
	assert.Equal(t, uint64(5), conv.UserID) // ctx 内用户落库
	assert.Equal(t, uint64(3), conv.AgentID)
}

func TestCreateConversationAgentNotFound(t *testing.T) {
	svc, _ := newCRUDService(&stubAgents{err: agentapi.ErrAgentNotFound}, dummyProviders())

	_, err := svc.CreateConversation(userCtx(), chatapi.CreateConversationReq{AgentID: 9})
	assert.ErrorIs(t, err, agentapi.ErrAgentNotFound) // 哨兵透传
}

func TestCreateConversationAgentDisabled(t *testing.T) {
	svc, _ := newCRUDService(&stubAgents{agent: testAgent(false)}, dummyProviders())

	_, err := svc.CreateConversation(userCtx(), chatapi.CreateConversationReq{AgentID: 3})
	assert.ErrorIs(t, err, agentapi.ErrAgentDisabled) // 停用：新会话被拒
}

func TestCreateConversationNoUser(t *testing.T) {
	svc, _ := newCRUDService(enabledAgents(), dummyProviders())

	_, err := svc.CreateConversation(context.Background(), chatapi.CreateConversationReq{AgentID: 3})
	assert.ErrorIs(t, err, errs.ErrInternal) // auth 中间件保证注入；缺失 = 装配错误
}

// ---- ListConversations ----

func seedConversations(t *testing.T, st *memStore) {
	t.Helper()
	for i, title := range []string{"c1", "c2", "c3"} {
		c := &Conversation{UserID: 5, AgentID: 3, Title: title}
		if err := st.CreateConversation(context.Background(), c); err != nil {
			t.Fatalf("seed: %v", err)
		}
		if i > 0 { // c2 / c3 各 touch 一次推进 updated_at；c1 未 touch 最旧
			if err := st.TouchConversation(context.Background(), c.ID); err != nil {
				t.Fatalf("touch: %v", err)
			}
		}
	}
}

func TestListConversationsFirstPage(t *testing.T) {
	svc, st := newCRUDService(enabledAgents(), dummyProviders())
	seedConversations(t, st)

	res, err := svc.ListConversations(userCtx(), chatapi.ListConversationsReq{Limit: 2})
	assert.NoError(t, err)
	assert.Len(t, res.Items, 2)
	assert.True(t, res.HasMore)
	assert.NotEmpty(t, res.NextCursor)
	// updated_at DESC：c3、c2 在前（c1 未 touch 最旧）
	assert.Equal(t, "c3", res.Items[0].Title)
	assert.Equal(t, "c2", res.Items[1].Title)

	// 游标可解码回末行排序键（下一页起点 = c2 的 id）
	key, err := page.DecodeCursor[convCursorKey](res.NextCursor)
	assert.NoError(t, err)
	assert.Equal(t, st.conversation(102).ID, key.ID)
}

func TestListConversationsSecondPage(t *testing.T) {
	svc, st := newCRUDService(enabledAgents(), dummyProviders())
	seedConversations(t, st)

	first, err := svc.ListConversations(userCtx(), chatapi.ListConversationsReq{Limit: 2})
	assert.NoError(t, err)
	assert.Equal(t, "c2", first.Items[1].Title)

	second, err := svc.ListConversations(userCtx(), chatapi.ListConversationsReq{Limit: 2, Cursor: first.NextCursor})
	assert.NoError(t, err)
	assert.Len(t, second.Items, 1)
	assert.False(t, second.HasMore)
	assert.Empty(t, second.NextCursor)
	assert.Equal(t, "c1", second.Items[0].Title)
}

func TestListConversationsBadCursor(t *testing.T) {
	svc, _ := newCRUDService(enabledAgents(), dummyProviders())

	_, err := svc.ListConversations(userCtx(), chatapi.ListConversationsReq{Cursor: "!!not-base64!!"})
	assert.ErrorIs(t, err, errs.ErrValidationFailed) // 篡改游标 → 400
}

// ---- ListMessages / DeleteConversation ----

func TestListMessagesPagination(t *testing.T) {
	svc, st := newCRUDService(enabledAgents(), dummyProviders())
	c, err := svc.CreateConversation(userCtx(), chatapi.CreateConversationReq{AgentID: 3})
	assert.NoError(t, err)
	convID := assertConvID(t, c)
	for i := 0; i < 3; i++ {
		st.seedTurn(t, convID, "问", "答")
	}

	// 首页 4 条（6 条历史），next_after_id = 第 4 条 id
	res, err := svc.ListMessages(userCtx(), chatapi.ListMessagesReq{ConversationID: convID, Limit: 4})
	assert.NoError(t, err)
	assert.Len(t, res.Items, 4)
	assert.True(t, res.HasMore)
	assert.NotZero(t, res.NextAfterID)
	assert.Equal(t, chatapi.RoleUser, res.Items[0].Role) // 正序
	assert.Equal(t, "答", res.Items[3].Content)
	assert.NotNil(t, res.Items[0].ToolCalls) // nil 归一为 []，前端免空判断

	// 次页从 after_id 续
	res2, err := svc.ListMessages(userCtx(), chatapi.ListMessagesReq{ConversationID: convID, AfterID: res.NextAfterID, Limit: 4})
	assert.NoError(t, err)
	assert.Len(t, res2.Items, 2)
	assert.False(t, res2.HasMore)
	assert.Zero(t, res2.NextAfterID)
}

func TestListMessagesConversationNotFound(t *testing.T) {
	svc, _ := newCRUDService(enabledAgents(), dummyProviders())

	_, err := svc.ListMessages(userCtx(), chatapi.ListMessagesReq{ConversationID: 404})
	assert.ErrorIs(t, err, chatapi.ErrConversationNotFound)
}

func TestListMessagesNotOwner(t *testing.T) {
	svc, _ := newCRUDService(enabledAgents(), dummyProviders())
	c, err := svc.CreateConversation(userCtx(), chatapi.CreateConversationReq{AgentID: 3})
	assert.NoError(t, err)

	_, err = svc.ListMessages(userCtxOther(), chatapi.ListMessagesReq{ConversationID: assertConvID(t, c)})
	assert.ErrorIs(t, err, chatapi.ErrConversationNotFound) // 他人会话视同不存在（不泄露存在性）
}

func TestDeleteConversation(t *testing.T) {
	svc, st := newCRUDService(enabledAgents(), dummyProviders())
	c, err := svc.CreateConversation(userCtx(), chatapi.CreateConversationReq{AgentID: 3})
	assert.NoError(t, err)
	convID := assertConvID(t, c)

	assert.NoError(t, svc.DeleteConversation(userCtx(), chatapi.DeleteConversationReq{ID: convID}))
	assert.Nil(t, st.conversation(convID))
	assert.Empty(t, st.messagesOf(convID))

	// 删过的再删：404
	assert.ErrorIs(t, svc.DeleteConversation(userCtx(), chatapi.DeleteConversationReq{ID: convID}), chatapi.ErrConversationNotFound)
	// 他人删：视同不存在
	c2, _ := svc.CreateConversation(userCtx(), chatapi.CreateConversationReq{AgentID: 3})
	assert.ErrorIs(t, svc.DeleteConversation(userCtxOther(), chatapi.DeleteConversationReq{ID: assertConvID(t, c2)}), chatapi.ErrConversationNotFound)
}

func TestGetConversationNotFound(t *testing.T) {
	svc, _ := newCRUDService(enabledAgents(), dummyProviders())

	_, err := svc.getOwnedConversation(userCtx(), 999)
	assert.ErrorIs(t, err, chatapi.ErrConversationNotFound)
	assert.NotErrorIs(t, err, gorm.ErrRecordNotFound) // 已翻译，gorm 哨兵不出 service 层
}

func assertConvID(t *testing.T, c *chatapi.ConversationSchema) uint64 {
	t.Helper()
	id, err := strconv.ParseUint(c.ID, 10, 64)
	assert.NoError(t, err)
	return id
}

func userCtxOther() context.Context {
	return authctx.WithUser(context.Background(), &authctx.User{ID: 6, Username: "u6"})
}

// ---- truncateTurns 纯函数（整轮截断，user 锚点）----

func msgRow(id uint64, role, content string) Message {
	return Message{BaseAppendOnly: db.BaseAppendOnly{ID: id}, Role: role, Content: content}
}

func TestTruncateTurns(t *testing.T) {
	// 倒序输入：u3/a3 | u2/a2 | u1/a1（最新在前）
	desc := []Message{
		msgRow(6, chatapi.RoleAssistant, "a3"),
		msgRow(5, chatapi.RoleUser, "u3"),
		msgRow(4, chatapi.RoleAssistant, "a2"),
		msgRow(3, chatapi.RoleUser, "u2"),
		msgRow(2, chatapi.RoleAssistant, "a1"),
		msgRow(1, chatapi.RoleUser, "u1"),
	}

	// 携带 2 轮：从第 2 个 user 锚点（u2）起整轮保留，反转回时序
	got := truncateTurns(desc, 2)
	assert.Equal(t, []string{"user:u2", "assistant:a2", "user:u3", "assistant:a3"}, roleContent(got))

	// 全量携带（轮数充足）
	got = truncateTurns(desc, 10)
	assert.Len(t, got, 6)

	// 窗口截断产生孤儿残行：最新 5 行（u1 被 LIMIT 截掉），a1 无锚点 → 首部丢弃
	got = truncateTurns(desc[:5], 10)
	assert.Equal(t, []string{"user:u2", "assistant:a2", "user:u3", "assistant:a3"}, roleContent(got))

	// 窗口起点落在半轮（最老侧是 a2）：残行 a2 无锚点被丢弃，只保留完整一轮
	got = truncateTurns(desc[2:], 1)
	assert.Equal(t, []string{"user:u2", "assistant:a2"}, roleContent(got))

	// 带工具中间行的轮（u1 a1(tool_calls) t1 a1' | u2 a2）不截断中间行
	desc2 := []Message{
		msgRow(6, chatapi.RoleAssistant, "a2"),
		msgRow(5, chatapi.RoleUser, "u2"),
		msgRow(4, chatapi.RoleAssistant, "a1'"),
		msgRow(3, chatapi.RoleTool, "t1"),
		msgRow(2, chatapi.RoleAssistant, "a1"),
		msgRow(1, chatapi.RoleUser, "u1"),
	}
	got = truncateTurns(desc2, 2)
	assert.Equal(t,
		[]string{"user:u1", "assistant:a1", "tool:t1", "assistant:a1'", "user:u2", "assistant:a2"},
		roleContent(got))
}

func roleContent(ms []Message) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Role+":"+m.Content)
	}
	return out
}

// ---- 转换与纯函数补测 ----

func TestTableNames(t *testing.T) {
	assert.Equal(t, "conversations", Conversation{}.TableName())
	assert.Equal(t, "messages", Message{}.TableName())
}

func TestToMessageSchemaToolCalls(t *testing.T) {
	// nil → []（空值约定）
	s := toMessageSchema(&Message{BaseAppendOnly: db.BaseAppendOnly{ID: 7}, Role: chatapi.RoleAssistant, Content: "x"})
	assert.Equal(t, []chatapi.ToolCall{}, s.ToolCalls)

	// jsonb map → ToolCall；args 缺失归一为 {}
	s = toMessageSchema(&Message{
		BaseAppendOnly: db.BaseAppendOnly{ID: 8},
		Role:           chatapi.RoleAssistant,
		Content:        "",
		ToolCalls: []map[string]any{
			{"id": "call_1", "tool": "query_orders", "args": map[string]any{"day": "yesterday"}},
			{"id": "call_2", "tool": "no_args"},
		},
	})
	assert.Len(t, s.ToolCalls, 2)
	assert.Equal(t, "call_1", s.ToolCalls[0].ID)
	assert.Equal(t, "query_orders", s.ToolCalls[0].Tool)
	assert.Equal(t, map[string]any{"day": "yesterday"}, s.ToolCalls[0].Args)
	assert.Equal(t, map[string]any{}, s.ToolCalls[1].Args)
}

func TestToEinoMessageVariants(t *testing.T) {
	// user
	assert.Equal(t, "user:hi", roles([]*schema.Message{toEinoMessage(&Message{Role: chatapi.RoleUser, Content: "hi"})}))

	// tool 行：调用标识取 ToolCalls[0].id
	tm := toEinoMessage(&Message{
		Role:      chatapi.RoleTool,
		Content:   "3 rows",
		ToolCalls: []map[string]any{{"id": "call_9", "tool": "q"}},
	})
	assert.Equal(t, schema.Tool, tm.Role)
	assert.Equal(t, "call_9", tm.ToolCallID)

	// assistant + tool_calls → eino ToolCall（Arguments 为 JSON 字符串）
	am := toEinoMessage(&Message{
		Role:      chatapi.RoleAssistant,
		Content:   "",
		ToolCalls: []map[string]any{{"id": "c1", "tool": "q", "args": map[string]any{"a": 1}}},
	})
	if assert.Len(t, am.ToolCalls, 1) {
		assert.Equal(t, "q", am.ToolCalls[0].Function.Name)
		assert.JSONEq(t, `{"a":1}`, am.ToolCalls[0].Function.Arguments)
	}

	// 未知 role 按 assistant 兜底
	um := toEinoMessage(&Message{Role: "strange", Content: "x"})
	assert.Equal(t, schema.Assistant, um.Role)
}

func TestFetchRowsForTurns(t *testing.T) {
	assert.Equal(t, 10, fetchRowsForTurns(1)) // 1×5+5
	assert.Equal(t, 15, fetchRowsForTurns(2))
	assert.Equal(t, 10, fetchRowsForTurns(0)) // 非法值按 1 处理
	assert.Equal(t, 10, fetchRowsForTurns(-3))
	assert.Equal(t, 505, fetchRowsForTurns(100))
}

func TestMakeTitle(t *testing.T) {
	assert.Equal(t, "查一下 异常订单", makeTitle("查一下\n异常订单")) // 压平换行
	assert.Len(t, []rune(makeTitle("这是一条特别特别特别长的首条消息需要被截断到三十个字符以内的标题")), 30)
	assert.Equal(t, "", makeTitle("   ")) // 纯空白
}

func TestDeleteConversationStoreError(t *testing.T) {
	svc, st := newCRUDService(enabledAgents(), dummyProviders())
	c, _ := svc.CreateConversation(userCtx(), chatapi.CreateConversationReq{AgentID: 3})
	st.deleteErr = errors.New("memStore: delete fail")

	// store 层非 NotFound 失败原样 %w 上抛（500 路径），不吞、不误翻 404
	err := svc.DeleteConversation(userCtx(), chatapi.DeleteConversationReq{ID: assertConvID(t, c)})
	assert.ErrorContains(t, err, "delete conversation")
	assert.ErrorIs(t, err, st.deleteErr)
	assert.NotErrorIs(t, err, chatapi.ErrConversationNotFound)
}
