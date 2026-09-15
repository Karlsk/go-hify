package service

// 发消息两模式（runTurn）编排测试：上下文组装、事件序列、落库与 executions、
// 错误翻译与 retryable。LLM 上游用真实 llm.Client + 可编程 fake Streamer
//（llm.NewClient 导出，重试 / 槽位 / 熔断 / 看门狗由 client 真实执行）。

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"

	agentapi "github.com/Karlsk/go-hify/internal/agent/api"
	chatapi "github.com/Karlsk/go-hify/internal/chat/api"
	"github.com/Karlsk/go-hify/internal/platform/errs"
	"github.com/Karlsk/go-hify/internal/platform/llm"
	providerapi "github.com/Karlsk/go-hify/internal/provider/api"
	ragapi "github.com/Karlsk/go-hify/internal/rag/api"
)

// happyScript 两个文本 delta + 尾帧 usage/finish；只允许被调一次。
func happyScript(call int) (*schema.StreamReader[*schema.Message], error) {
	if call > 1 {
		return nil, errors.New("happyScript: unexpected second call")
	}
	return chunkStream(delta("你好"), delta("，世界"), tailChunk(12, 6, "stop")), nil
}

// createConv 经 service 建会话（复用属主 / agent 校验），返回数字 id。
func createConv(t *testing.T, s *chatService) uint64 {
	t.Helper()
	c, err := s.CreateConversation(userCtx(), chatapi.CreateConversationReq{AgentID: 3})
	assert.NoError(t, err)
	id, err := parseUint(c.ID)
	assert.NoError(t, err)
	return id
}

// collectEvents 收集事件的 emit（可选失败注入 failOn）。
func collectEvents(out *[]chatapi.StreamEvent, failOn string) func(chatapi.StreamEvent) error {
	return func(e chatapi.StreamEvent) error {
		if failOn != "" && e.Type == failOn {
			return io.EOF // 模拟前端断连
		}
		*out = append(*out, e)
		return nil
	}
}

// ---- 流式：正常链路 ----

func TestStreamHappyPath(t *testing.T) {
	h := newTestService(t, happyScript)
	convID := createConv(t, h.svc)

	var events []chatapi.StreamEvent
	err := h.svc.Stream(userCtx(), chatapi.SendMessageReq{ConversationID: convID, Content: "打个招呼"}, collectEvents(&events, ""))
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, len(events), 3) // delta×2 + done

	assert.Equal(t, chatapi.EventDelta, events[0].Type)
	assert.Equal(t, "你好", events[0].Content)
	assert.Equal(t, "，世界", events[1].Content)
	done := events[len(events)-1]
	assert.Equal(t, chatapi.EventDone, done.Type)
	assert.Equal(t, chatapi.Usage{Input: 12, Output: 6}, *done.Usage)
	assert.Equal(t, "stop", done.FinishReason)
	assert.NotEmpty(t, done.MessageID)

	// 上下文组装：system + 当前 user（历史为空）
	assert.Equal(t, "system:你是 Hify 助手 | user:打个招呼", roles(h.streamer.lastMsgs()))
	// CallOptions：Temperature 恒传（agent 0.7）；MaxOutputTokens nil 不传
	opts := h.streamer.lastOpts()
	assert.NotNil(t, opts)
	if assert.NotNil(t, opts.Temperature) {
		assert.InDelta(t, 0.7, float64(*opts.Temperature), 1e-6)
	}
	assert.Zero(t, opts.MaxTokens)

	// 落库：user + assistant 两条；标题回填；会话被 touch
	ms := h.store.messagesOf(convID)
	assert.Len(t, ms, 2)
	assert.Equal(t, chatapi.RoleUser, ms[0].Role)
	assert.Equal(t, chatapi.RoleAssistant, ms[1].Role)
	assert.Equal(t, "你好，世界", ms[1].Content)
	assert.Equal(t, "打个招呼", h.store.conversation(convID).Title)
	assert.True(t, h.store.conversation(convID).UpdatedAt.After(h.store.conversation(convID).CreatedAt))

	// executions：一行成功记录（tokens / duration / finish / 无 error_class）
	row := h.execs.last()
	if assert.NotNil(t, row) {
		assert.Equal(t, convID, *row.ConversationID)
		assert.Equal(t, "gpt-4o", row.ModelName)
		assert.Equal(t, int64(12), row.PromptTokens)
		assert.Equal(t, int64(6), row.CompletionTokens)
		assert.Equal(t, int64(18), row.TotalTokens)
		assert.Equal(t, "stop", row.FinishReason)
		assert.Nil(t, row.ErrorClass)
		assert.GreaterOrEqual(t, row.DurationMs, int32(0))
	}
}

func TestStreamNoSystemPrompt(t *testing.T) {
	agent := testAgent(true)
	agent.SystemPrompt = ""
	h := newServiceWithAgent(t, agent, happyScript)
	convID := createConv(t, h.svc)

	err := h.svc.Stream(userCtx(), chatapi.SendMessageReq{ConversationID: convID, Content: "hi"}, func(chatapi.StreamEvent) error { return nil })
	assert.NoError(t, err)
	assert.Equal(t, "user:hi", roles(h.streamer.lastMsgs())) // 无 system：直接 user 开头
}

func TestStreamTitleOnlyOnce(t *testing.T) {
	h := newTestService(t, happyScript)
	convID := createConv(t, h.svc)
	assert.NoError(t, h.store.UpdateConversationTitle(context.Background(), convID, "已有标题"))

	_ = h.svc.Stream(userCtx(), chatapi.SendMessageReq{ConversationID: convID, Content: "第二条"}, func(chatapi.StreamEvent) error { return nil })
	assert.Equal(t, 1, h.store.titleCalls) // 标题只回填一次（创建时未回填、此处手动设过后不再覆盖）
	assert.Equal(t, "已有标题", h.store.conversation(convID).Title)
}

// ---- 上下文组装：整轮截断 ----

func TestStreamContextTruncation(t *testing.T) {
	agent := testAgent(true)
	agent.MaxContextTurns = 2
	h := newServiceWithAgent(t, agent, happyScript)
	convID := createConv(t, h.svc)

	h.store.seedTurn(t, convID, "问1", "答1")
	h.store.seedTurn(t, convID, "问2", "答2")
	h.store.seedTurn(t, convID, "问3", "答3")

	err := h.svc.Stream(userCtx(), chatapi.SendMessageReq{ConversationID: convID, Content: "问4"}, func(chatapi.StreamEvent) error { return nil })
	assert.NoError(t, err)
	// system + 最近 2 整轮（问2 起）+ 当前消息；第 1 轮被整轮丢弃
	assert.Equal(t,
		"system:你是 Hify 助手 | user:问2 | assistant:答2 | user:问3 | assistant:答3 | user:问4",
		roles(h.streamer.lastMsgs()))
}

// ---- 流前失败（标准错误信封路径：零事件、哨兵返回）----

func TestStreamConversationNotFound(t *testing.T) {
	h := newTestService(t, happyScript)
	var events []chatapi.StreamEvent
	err := h.svc.Stream(userCtx(), chatapi.SendMessageReq{ConversationID: 404, Content: "x"}, collectEvents(&events, ""))
	assert.ErrorIs(t, err, chatapi.ErrConversationNotFound)
	assert.Empty(t, events)
}

func TestStreamAgentDisabled(t *testing.T) {
	h := newTestService(t, happyScript)
	convID := createConv(t, h.svc)
	h.svc.agents = &stubAgents{agent: testAgent(false)} // 建会话后停用

	var events []chatapi.StreamEvent
	err := h.svc.Stream(userCtx(), chatapi.SendMessageReq{ConversationID: convID, Content: "x"}, collectEvents(&events, ""))
	assert.ErrorIs(t, err, agentapi.ErrAgentDisabled)
	assert.Empty(t, events)
	assert.Empty(t, h.store.messagesOf(convID)) // 配置失败不留孤儿 user 消息
}

func TestStreamModelResolveFails(t *testing.T) {
	h := newTestService(t, happyScript)
	convID := createConv(t, h.svc)
	h.svc.providers = &stubProviders{err: providerapi.ErrModelNotFound}

	err := h.svc.Stream(userCtx(), chatapi.SendMessageReq{ConversationID: convID, Content: "x"}, func(chatapi.StreamEvent) error { return nil })
	assert.ErrorIs(t, err, providerapi.ErrModelNotFound) // 哨兵透传
	assert.Empty(t, h.store.messagesOf(convID))
}

// ---- LLM 调用失败（client.Stream 直接报错 = 首 token 前，属流前）----

func TestStreamInvalidRequest(t *testing.T) {
	h := newTestService(t, func(int) (*schema.StreamReader[*schema.Message], error) {
		return nil, &llm.Error{Class: llm.ClassInvalidRequest, Err: errors.New("context length exceeded")}
	})
	convID := createConv(t, h.svc)

	var events []chatapi.StreamEvent
	err := h.svc.Stream(userCtx(), chatapi.SendMessageReq{ConversationID: convID, Content: "x"}, collectEvents(&events, ""))
	assert.ErrorIs(t, err, chatapi.ErrModelContextTooLong) // InvalidRequest → MODEL_CONTEXT_TOO_LONG（400）
	assert.Empty(t, events)

	// user 消息已落库；executions 记 InvalidRequest；无 assistant
	ms := h.store.messagesOf(convID)
	assert.Len(t, ms, 1)
	row := h.execs.last()
	if assert.NotNil(t, row) {
		assert.Equal(t, "InvalidRequest", *row.ErrorClass)
		assert.Empty(t, row.FinishReason)
	}
}

func TestStreamRateLimited(t *testing.T) {
	h := newTestService(t, func(int) (*schema.StreamReader[*schema.Message], error) {
		return nil, &llm.Error{Class: llm.ClassRateLimited}
	})
	convID := createConv(t, h.svc)

	err := h.svc.Stream(userCtx(), chatapi.SendMessageReq{ConversationID: convID, Content: "x"}, func(chatapi.StreamEvent) error { return nil })
	assert.ErrorIs(t, err, errs.ErrRateLimited) // 429
	row := h.execs.last()
	if assert.NotNil(t, row) {
		assert.Equal(t, "RateLimited", *row.ErrorClass)
	}
}

// ---- 流中失败（已 emit delta 后）：ErrorEvent + 部分内容落库 ----

func TestStreamMidStreamError(t *testing.T) {
	h := newTestService(t, func(int) (*schema.StreamReader[*schema.Message], error) {
		return failAfterStream(delta("半"), &llm.Error{Class: llm.ClassNetwork, Err: errors.New("connection reset")}), nil
	})
	convID := createConv(t, h.svc)

	var events []chatapi.StreamEvent
	err := h.svc.Stream(userCtx(), chatapi.SendMessageReq{ConversationID: convID, Content: "x"}, collectEvents(&events, ""))
	assert.NoError(t, err) // 流已开始：错误经 ErrorEvent，返回 nil
	assert.Len(t, events, 2)
	assert.Equal(t, chatapi.EventDelta, events[0].Type)
	assert.Equal(t, chatapi.EventError, events[1].Type)
	assert.Equal(t, "PROVIDER_UNAVAILABLE", events[1].Code)
	assert.True(t, events[1].Retryable) // Network：用户可重试

	// 部分内容落库（下轮上下文完整）；executions 记 Network
	ms := h.store.messagesOf(convID)
	assert.Len(t, ms, 2)
	assert.Equal(t, "半", ms[1].Content)
	row := h.execs.last()
	if assert.NotNil(t, row) {
		assert.Equal(t, "Network", *row.ErrorClass)
	}
}

// ---- 客户端断连（emit 失败）----

func TestStreamEmitAbort(t *testing.T) {
	h := newTestService(t, func(int) (*schema.StreamReader[*schema.Message], error) {
		return chunkStream(delta("a"), delta("b"), delta("c")), nil
	})
	convID := createConv(t, h.svc)

	var events []chatapi.StreamEvent
	err := h.svc.Stream(userCtx(), chatapi.SendMessageReq{ConversationID: convID, Content: "x"}, collectEvents(&events, chatapi.EventDelta))
	assert.NoError(t, err) // 断连：静默收尾（ErrorEvent 也写不出去）
	assert.Len(t, events, 0)

	// 部分内容仍落库；executions 记 Network（断连近似，CHECK 无 Canceled 值）
	ms := h.store.messagesOf(convID)
	assert.Len(t, ms, 2)
	assert.Equal(t, "a", ms[1].Content)
	row := h.execs.last()
	if assert.NotNil(t, row) {
		assert.Equal(t, "Network", *row.ErrorClass)
	}
}

// ---- 未触达上游的失败：哨兵透传、不落 executions、不留 user 消息 ----

func TestStreamProviderBusyNoExecution(t *testing.T) {
	st := newMemStore()
	factory := &stubClientFactory{err: llm.ErrProviderBusy}
	execs := &execRecorder{}
	svc := New(st, &stubAgents{agent: testAgent(true)}, &stubProviders{cfg: testLLMConfig()}, factory, execs, &stubRags{}).(*chatService)
	convID := createConv(t, svc)

	err := svc.Stream(userCtx(), chatapi.SendMessageReq{ConversationID: convID, Content: "x"}, func(chatapi.StreamEvent) error { return nil })
	assert.ErrorIs(t, err, llm.ErrProviderBusy)
	assert.Nil(t, execs.last())            // 未触达上游：无行可记
	assert.Empty(t, st.messagesOf(convID)) // factory 失败在落 user 消息之前
}

// ---- 一次输出模式 ----

func TestSendMessageHappyPath(t *testing.T) {
	h := newTestService(t, happyScript)
	convID := createConv(t, h.svc)

	reply, err := h.svc.SendMessage(userCtx(), chatapi.SendMessageReq{ConversationID: convID, Content: "总结一下"})
	assert.NoError(t, err)
	assert.Equal(t, "你好，世界", reply.Content)
	assert.Equal(t, chatapi.Usage{Input: 12, Output: 6}, reply.Usage)
	assert.Equal(t, "stop", reply.FinishReason)
	assert.NotEmpty(t, reply.MessageID)

	ms := h.store.messagesOf(convID)
	assert.Len(t, ms, 2)
	assert.Equal(t, reply.Content, ms[1].Content)
	assert.NotNil(t, h.execs.last())
}

func TestSendMessageMidStreamErrorReturnsSentinel(t *testing.T) {
	h := newTestService(t, func(int) (*schema.StreamReader[*schema.Message], error) {
		return failAfterStream(delta("半"), &llm.Error{Class: llm.ClassNetwork}), nil
	})
	convID := createConv(t, h.svc)

	reply, err := h.svc.SendMessage(userCtx(), chatapi.SendMessageReq{ConversationID: convID, Content: "x"})
	assert.Nil(t, reply)
	assert.ErrorIs(t, err, llm.ErrProviderUnavailable) // 一次输出模式：错误直接返回（标准信封 503）
}

// ---- helper ----

func parseUint(s string) (uint64, error) {
	var n uint64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("bad id")
		}
		n = n*10 + uint64(c-'0')
	}
	return n, nil
}

// ---- setupTurn 失败分支补测 ----

func TestStreamAgentDeleted(t *testing.T) {
	h := newTestService(t, happyScript)
	convID := createConv(t, h.svc)
	h.svc.agents = &stubAgents{err: agentapi.ErrAgentNotFound} // 建会话后删除

	err := h.svc.Stream(userCtx(), chatapi.SendMessageReq{ConversationID: convID, Content: "x"}, func(chatapi.StreamEvent) error { return nil })
	assert.ErrorIs(t, err, agentapi.ErrAgentNotFound) // 已删 agent 的存量会话同样被拒（哨兵透传）
	assert.Empty(t, h.store.messagesOf(convID))
}

func TestStreamAgentBadModelID(t *testing.T) {
	agent := testAgent(true)
	agent.ModelID = "abc" // 字符串化 id 被破坏（脏数据 / 手改库）
	h := newServiceWithAgent(t, agent, happyScript)
	convID := createConv(t, h.svc)

	err := h.svc.Stream(userCtx(), chatapi.SendMessageReq{ConversationID: convID, Content: "x"}, func(chatapi.StreamEvent) error { return nil })
	assert.ErrorIs(t, err, errs.ErrInternal) // 装配错误：500 而非误导性的 404/400
	assert.Empty(t, h.store.messagesOf(convID))
	assert.Nil(t, h.execs.last())
}

// ---- 收尾 WARN 路径（落库故障不阻断对话）----

func TestStreamTitleBackfillWarn(t *testing.T) {
	h := newTestService(t, happyScript)
	h.store.titleErr = errors.New("memStore: title write fail")
	convID := createConv(t, h.svc)

	var events []chatapi.StreamEvent
	err := h.svc.Stream(userCtx(), chatapi.SendMessageReq{ConversationID: convID, Content: "打个招呼"}, collectEvents(&events, ""))
	assert.NoError(t, err) // 标题回填失败只 WARN：对话照常
	assert.Equal(t, chatapi.EventDone, events[len(events)-1].Type)
}

func TestStreamTouchWarn(t *testing.T) {
	h := newTestService(t, happyScript)
	h.store.touchErr = errors.New("memStore: touch fail")
	convID := createConv(t, h.svc)

	var events []chatapi.StreamEvent
	err := h.svc.Stream(userCtx(), chatapi.SendMessageReq{ConversationID: convID, Content: "hi"}, collectEvents(&events, ""))
	assert.NoError(t, err) // touch 失败只 WARN（列表排序滞后，不回滚消息）
	assert.Equal(t, chatapi.EventDone, events[len(events)-1].Type)
	assert.Len(t, h.store.messagesOf(convID), 2)
}

func TestStreamExecRecordWarn(t *testing.T) {
	h := newTestService(t, happyScript)
	h.execs.err = errors.New("execRecorder: insert fail")
	convID := createConv(t, h.svc)

	var events []chatapi.StreamEvent
	err := h.svc.Stream(userCtx(), chatapi.SendMessageReq{ConversationID: convID, Content: "hi"}, collectEvents(&events, ""))
	assert.NoError(t, err) // executions 不是业务依赖：落库失败不打断对话
	assert.Equal(t, chatapi.EventDone, events[len(events)-1].Type)
}

func TestStreamPersistAssistantWarn(t *testing.T) {
	h := newTestService(t, happyScript)
	h.store.createMsgFailAfter = 1 // 第 1 条 user 成功、第 2 条（assistant）起失败
	convID := createConv(t, h.svc)

	var events []chatapi.StreamEvent
	err := h.svc.Stream(userCtx(), chatapi.SendMessageReq{ConversationID: convID, Content: "hi"}, collectEvents(&events, ""))
	assert.NoError(t, err)

	done := events[len(events)-1]
	assert.Equal(t, chatapi.EventDone, done.Type)
	assert.Empty(t, done.MessageID) // 降级：done 事件照发，message_id 为空
	assert.Len(t, h.store.messagesOf(convID), 1)
	assert.NotNil(t, h.execs.last()) // executions 不受影响
}

// ---- ctx 取消（服务端上游视角的断连）----

func TestStreamCtxCanceledMidStream(t *testing.T) {
	h := newTestService(t, func(int) (*schema.StreamReader[*schema.Message], error) {
		return failAfterStream(delta("半"), context.Canceled), nil
	})
	convID := createConv(t, h.svc)

	var events []chatapi.StreamEvent
	err := h.svc.Stream(userCtx(), chatapi.SendMessageReq{ConversationID: convID, Content: "x"}, collectEvents(&events, ""))
	assert.NoError(t, err) // 静默收尾：与 emit 断连同路径
	ms := h.store.messagesOf(convID)
	assert.Len(t, ms, 2)
	assert.Equal(t, "半", ms[1].Content) // 部分内容照常落库
	row := h.execs.last()
	if assert.NotNil(t, row) {
		assert.Equal(t, "Network", *row.ErrorClass) // ctx 取消近似 Network（CHECK 无 Canceled）
	}
}

// ---- llmErrorSpec / translateLLMError 全表 ----

func TestLLMErrorSpec(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantCode  string
		wantRetry bool
		wantMsg   string
	}{
		{"invalid_request", &llm.Error{Class: llm.ClassInvalidRequest}, "MODEL_CONTEXT_TOO_LONG", false, "模型上下文超长"},
		{"rate_limited", &llm.Error{Class: llm.ClassRateLimited}, "RATE_LIMITED", true, "供应商限流"},
		{"auth", &llm.Error{Class: llm.ClassAuth}, "PROVIDER_UNAVAILABLE", false, "API Key"},
		{"timeout", &llm.Error{Class: llm.ClassTimeout}, "PROVIDER_UNAVAILABLE", true, "超时"},
		{"overloaded", &llm.Error{Class: llm.ClassOverloaded}, "PROVIDER_UNAVAILABLE", true, "暂时不可用"},
		{"network", &llm.Error{Class: llm.ClassNetwork}, "PROVIDER_UNAVAILABLE", true, "暂时不可用"},
		{"provider_down", &llm.Error{Class: llm.ClassProviderDown}, "PROVIDER_UNAVAILABLE", false, "无法连接"},
		{"busy", llm.ErrProviderBusy, "PROVIDER_BUSY", true, "并发已满"},
		{"unavailable", llm.ErrProviderUnavailable, "PROVIDER_UNAVAILABLE", true, "熔断保护"},
		{"unknown", errors.New("mystery"), "SERVICE_UNAVAILABLE", false, "生成失败"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sentinel, message, retryable := llmErrorSpec(tc.err)
			assert.Equal(t, tc.wantCode, sentinel.Error())
			assert.True(t, strings.Contains(message, tc.wantMsg))
			assert.Equal(t, tc.wantRetry, retryable)
		})
	}
}

func TestTranslateLLMErrorSentinelPassthrough(t *testing.T) {
	// 哨兵本体直接返回（保留调用链上下文，不再包一层）
	err := translateLLMError(llm.ErrProviderBusy)
	assert.Equal(t, llm.ErrProviderBusy, err)

	// 分类错误：包成 %w 链，errors.Is 双向可达
	err = translateLLMError(&llm.Error{Class: llm.ClassAuth, Err: errors.New("401")})
	assert.ErrorIs(t, err, llm.ErrProviderUnavailable)
	assert.NotErrorIs(t, err, errs.ErrServiceUnavailable) // Auth 走分类表，不落 SERVICE_UNAVAILABLE 兜底
}

// ---- RAG 检索注入（rag_injection_spec.md §4） ----

// ragChunks 三个片段：两个 ≥0.75 过阈值、一个 0.74 被过滤（DocumentID 供引用清单断言）。
func ragChunks() []ragapi.RetrievedChunk {
	return []ragapi.RetrievedChunk{
		{DocumentID: "101", Content: "退货需在签收后 7 天内申请", Similarity: 0.93, DocumentName: "退货政策.md"},
		{DocumentID: "102", Content: "运费由买家承担", Similarity: 0.76, DocumentName: "运费说明.txt"},
		{DocumentID: "103", Content: "低于阈值的片段", Similarity: 0.74, DocumentName: "低分文档.md"},
	}
}

func TestAugmentSystemPrompt(t *testing.T) {
	cases := []struct {
		name   string
		base   string
		chunks []ragapi.RetrievedChunk
		want   string
	}{
		{
			name: "base + 命中片段 + 文档名",
			base: "你是售后客服",
			chunks: []ragapi.RetrievedChunk{
				{Content: "片段一", DocumentName: "退货政策.md"},
				{Content: "片段二", DocumentName: "运费说明.txt"},
			},
			want: "你是售后客服\n\n请基于以下参考资料回答用户问题。\n" +
				"如果资料中没有相关信息，直接说“我没有找到相关资料”，不要编造。\n\n" +
				"【参考资料】\n[1] 片段一 (来源: 退货政策.md)\n[2] 片段二 (来源: 运费说明.txt)",
		},
		{
			name:   "D3：base 为空 → 资料段起头",
			base:   "",
			chunks: []ragapi.RetrievedChunk{{Content: "片段", DocumentName: "doc.md"}},
			want: "请基于以下参考资料回答用户问题。\n" +
				"如果资料中没有相关信息，直接说“我没有找到相关资料”，不要编造。\n\n" +
				"【参考资料】\n[1] 片段 (来源: doc.md)",
		},
		{
			name:   "文档名为空：不加来源后缀",
			base:   "b",
			chunks: []ragapi.RetrievedChunk{{Content: "片段"}},
			want: "b\n\n请基于以下参考资料回答用户问题。\n" +
				"如果资料中没有相关信息，直接说“我没有找到相关资料”，不要编造。\n\n" +
				"【参考资料】\n[1] 片段",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, augmentSystemPrompt(tc.base, tc.chunks))
		})
	}
}

func TestBuildSystemPrompt(t *testing.T) {
	ctx := context.Background()
	kbAgent := testAgent(true)
	kbAgent.KnowledgeBaseIDs = []string{"7", "8"}

	t.Run("无 KB 绑定：不调 Retrieve，原样返回、无引用", func(t *testing.T) {
		rags := &stubRags{err: errors.New("不应被调")}
		svc := &chatService{rags: rags}
		a := testAgent(true)
		a.KnowledgeBaseIDs = nil
		got, cites := svc.buildSystemPrompt(ctx, a, "退货政策是什么")
		assert.Equal(t, "你是 Hify 助手", got)
		assert.Empty(t, cites)
		assert.Equal(t, 0, rags.calls, "空绑定零检索调用（需求：没有就跳过）")
	})

	t.Run("有绑定：TopK=3 + KB id 数值化传参，过滤后拼接 + 引用清单", func(t *testing.T) {
		rags := &stubRags{resp: ragChunks()}
		svc := &chatService{rags: rags}
		got, cites := svc.buildSystemPrompt(ctx, kbAgent, "退货政策是什么")
		assert.Equal(t, 1, rags.calls)
		assert.Equal(t, 3, rags.lastReq.TopK)
		assert.Equal(t, []uint64{7, 8}, rags.lastReq.KBIDs)
		assert.Equal(t, "退货政策是什么", rags.lastReq.Query)
		assert.Contains(t, got, "你是 Hify 助手\n\n请基于以下参考资料回答用户问题。")
		assert.Contains(t, got, "[1] 退货需在签收后 7 天内申请 (来源: 退货政策.md)")
		assert.Contains(t, got, "[2] 运费由买家承担 (来源: 运费说明.txt)")
		assert.NotContains(t, got, "低于阈值的片段", "0.74 < 0.75 被过滤")
		assert.NotContains(t, got, "[3]")
		// 引用清单：过滤后的两个命中（按 document_id 升序），被滤片段不出现
		assert.Equal(t, []chatapi.Citation{
			{DocumentID: "101", DocumentName: "退货政策.md", Similarity: 0.93},
			{DocumentID: "102", DocumentName: "运费说明.txt", Similarity: 0.76},
		}, cites)
	})

	t.Run("检索失败：降级照常对话（D2），原样返回、无引用", func(t *testing.T) {
		rags := &stubRags{err: ragapi.ErrEmbeddingModelMismatch}
		svc := &chatService{rags: rags}
		got, cites := svc.buildSystemPrompt(ctx, kbAgent, "q")
		assert.Equal(t, "你是 Hify 助手", got)
		assert.Empty(t, cites)
	})

	t.Run("命中全部低于阈值：不加资料段，原样返回、无引用", func(t *testing.T) {
		rags := &stubRags{resp: []ragapi.RetrievedChunk{{Content: "低分", Similarity: 0.1}}}
		svc := &chatService{rags: rags}
		got, cites := svc.buildSystemPrompt(ctx, kbAgent, "q")
		assert.Equal(t, "你是 Hify 助手", got)
		assert.Empty(t, cites)
	})

	t.Run("空 system_prompt 有命中：仍注入资料段（D3）", func(t *testing.T) {
		rags := &stubRags{resp: []ragapi.RetrievedChunk{{Content: "资料", Similarity: 0.9}}}
		svc := &chatService{rags: rags}
		a := testAgent(true)
		a.SystemPrompt = ""
		a.KnowledgeBaseIDs = []string{"7"}
		got, cites := svc.buildSystemPrompt(ctx, a, "q")
		assert.True(t, strings.HasPrefix(got, "请基于以下参考资料回答用户问题。"), "资料段起头无前导空行")
		assert.Contains(t, got, "[1] 资料")
		assert.Len(t, cites, 1)
	})

	t.Run("自定义 RAGTopK / RAGMinSimilarity 透传给 Retrieve", func(t *testing.T) {
		rags := &stubRags{resp: []ragapi.RetrievedChunk{
			{DocumentID: "1", Content: "高分", Similarity: 0.9},
			{DocumentID: "2", Content: "中分", Similarity: 0.65},
		}}
		svc := &chatService{rags: rags}
		a := testAgent(true)
		a.RAGTopK = 5
		a.RAGMinSimilarity = 0.6
		a.KnowledgeBaseIDs = []string{"7"}
		got, cites := svc.buildSystemPrompt(ctx, a, "q")
		assert.Equal(t, 5, rags.lastReq.TopK, "RAGTopK=5 透传给 Retrieve")
		assert.Contains(t, got, "[1] 高分", "0.9 >= 0.6 通过")
		assert.Contains(t, got, "[2] 中分", "0.65 >= 0.6 通过（默认 0.75 会过滤）")
		assert.Len(t, cites, 2)
	})
}

func TestDedupeCitations(t *testing.T) {
	// 同文档多块命中只留最高相似度；输出按 document_id 升序稳定排序。
	kept := []ragapi.RetrievedChunk{
		{DocumentID: "102", DocumentName: "b.md", Similarity: 0.8},
		{DocumentID: "101", DocumentName: "a.md", Similarity: 0.9},
		{DocumentID: "101", DocumentName: "a.md", Similarity: 0.95}, // 同文档更高分
		{DocumentID: "101", DocumentName: "a.md", Similarity: 0.85}, // 同文档更低分
	}
	assert.Equal(t, []chatapi.Citation{
		{DocumentID: "101", DocumentName: "a.md", Similarity: 0.95},
		{DocumentID: "102", DocumentName: "b.md", Similarity: 0.8},
	}, dedupeCitations(kept))
}

func TestStreamRAGInjection(t *testing.T) {
	// 端到端：agent 绑 KB → 发消息 → 检索注入增强 system prompt → 发给 LLM 的首条消息。
	agent := testAgent(true)
	agent.KnowledgeBaseIDs = []string{"7"}
	h := newServiceWithAgent(t, agent, happyScript)
	h.rags.resp = []ragapi.RetrievedChunk{{Content: "退货需在签收后 7 天内申请", Similarity: 0.9, DocumentName: "退货政策.md"}}
	convID := createConv(t, h.svc)

	var events []chatapi.StreamEvent
	err := h.svc.Stream(userCtx(), chatapi.SendMessageReq{ConversationID: convID, Content: "退货政策"}, collectEvents(&events, ""))
	assert.NoError(t, err)
	assert.Equal(t, 1, h.rags.calls, "每条 user 消息独立检索一次")

	msgs := h.streamer.lastMsgs()
	if assert.NotEmpty(t, msgs) {
		sys := msgs[0]
		assert.Equal(t, schema.System, sys.Role)
		assert.Contains(t, sys.Content, "你是 Hify 助手\n\n请基于以下参考资料回答用户问题。")
		assert.Contains(t, sys.Content, "[1] 退货需在签收后 7 天内申请 (来源: 退货政策.md)")
	}
}

func TestStreamRAGInjectionCitations(t *testing.T) {
	// 端到端：检索命中 → SSE citations 事件（首个 delta 前）+ assistant 行落库引用 +
	// 一次输出模式 reply.Citations。同文档两块命中去重取最高分。
	// 可重复 script：本测试发两条消息（Stream + SendMessage），happyScript 是单次的。
	script := func(int) (*schema.StreamReader[*schema.Message], error) {
		return chunkStream(delta("你好"), delta("，世界"), tailChunk(12, 6, "stop")), nil
	}
	agent := testAgent(true)
	agent.KnowledgeBaseIDs = []string{"7"}
	h := newServiceWithAgent(t, agent, script)
	h.rags.resp = []ragapi.RetrievedChunk{
		{DocumentID: "101", DocumentName: "退货政策.md", Similarity: 0.93, Content: "退货需在签收后 7 天内申请"},
		{DocumentID: "101", DocumentName: "退货政策.md", Similarity: 0.88, Content: "7 天内联系客服"},
		{DocumentID: "102", DocumentName: "运费说明.txt", Similarity: 0.76, Content: "运费由买家承担"},
	}
	convID := createConv(t, h.svc)

	var events []chatapi.StreamEvent
	err := h.svc.Stream(userCtx(), chatapi.SendMessageReq{ConversationID: convID, Content: "退货政策"}, collectEvents(&events, ""))
	assert.NoError(t, err)

	// citations 事件是首事件（先于所有 delta），去重 + 升序
	assert.Equal(t, chatapi.EventCitations, events[0].Type)
	assert.Equal(t, []chatapi.Citation{
		{DocumentID: "101", DocumentName: "退货政策.md", Similarity: 0.93},
		{DocumentID: "102", DocumentName: "运费说明.txt", Similarity: 0.76},
	}, events[0].Citations)
	assert.Equal(t, chatapi.EventDelta, events[1].Type)

	// assistant 行引用随消息落库（历史消息接口可回放）
	ms := h.store.messagesOf(convID)
	if assert.Len(t, ms, 2) {
		assert.Equal(t, events[0].Citations, ms[1].Citations)
	}

	// 一次输出模式：引用经 reply 返回（空态为 [] 不为 null）
	reply, err := h.svc.SendMessage(userCtx(), chatapi.SendMessageReq{ConversationID: convID, Content: "再问一次"})
	if assert.NoError(t, err) && assert.NotNil(t, reply) {
		assert.Equal(t, events[0].Citations, reply.Citations)
	}
}

func TestStreamRAGNoCitationsEvent(t *testing.T) {
	// 未命中（全滤）：不发 citations 事件、assistant 行引用为空 []（不返 null）。
	agent := testAgent(true)
	agent.KnowledgeBaseIDs = []string{"7"}
	h := newServiceWithAgent(t, agent, happyScript)
	h.rags.resp = []ragapi.RetrievedChunk{{DocumentID: "103", DocumentName: "低分文档.md", Similarity: 0.1}}
	convID := createConv(t, h.svc)

	var events []chatapi.StreamEvent
	err := h.svc.Stream(userCtx(), chatapi.SendMessageReq{ConversationID: convID, Content: "q"}, collectEvents(&events, ""))
	assert.NoError(t, err)
	assert.Equal(t, chatapi.EventDelta, events[0].Type, "空引用不发 citations 事件，首事件直接是 delta")

	ms := h.store.messagesOf(convID)
	if assert.Len(t, ms, 2) {
		assert.Equal(t, []chatapi.Citation{}, ms[1].Citations, "空引用落库为 []（store 归一）")
	}
}
