package service

// turn：发消息两模式（stream:true / stream:false）共用的编排——校验装配 → 落 user 消息 →
// 组装多轮上下文 → platform/llm 流式调用 → 收尾落库（assistant 消息 / touch / executions）。
//
// 错误约定（与 api.ChatService.Stream 注释一致）：
//   - 任何 emit 之前的失败以 error 返回——流式模式由 handler 回标准错误信封（未写 200 头）；
//   - 流建立后的失败：流式模式经 ErrorEvent emit 后返回 nil（状态码不可改），
//     一次输出模式直接返回翻译后的哨兵错误；
//   - 客户端断连（emit 失败 / ctx 取消）：取消上游（省 token）、已生成部分照常落库、静默收尾。
//
// budget 护栏（每用户限流 + 每日预算）后置批次接线：届时在 setupTurn 之前检查、
// 熔断时透传 errs.ErrRateLimited / errs.ErrBudgetExhausted。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
	"log/slog"

	agentapi "github.com/Karlsk/go-hify/internal/agent/api"
	chatapi "github.com/Karlsk/go-hify/internal/chat/api"
	"github.com/Karlsk/go-hify/internal/platform/errs"
	"github.com/Karlsk/go-hify/internal/platform/llm"
	"github.com/Karlsk/go-hify/internal/platform/logging"
	providerapi "github.com/Karlsk/go-hify/internal/provider/api"
)

const (
	// rowsPerTurnEstimate 一轮对话的行数估计（user + assistant(tool_calls) + tool 中间行 + 终答），
	// 用于「倒序取最近 N 行」的窗口宽度；截断以 user 锚点整轮判定，估计偏小只会少带整轮、不会带半轮。
	rowsPerTurnEstimate = 5
	// titleMaxRunes 会话标题截断长度（首条用户消息生成）。
	titleMaxRunes = 30
)

// llmSetup setupTurn 装配出的一轮调用配置快照。
type llmSetup struct {
	conv     *Conversation
	agent    *agentapi.AgentDetailSchema
	cfg      *providerapi.LLMConfig
	modelNum uint64 // agent.ModelID（字符串化）解析回数字，executions.model_id 用
	client   *llm.Client
}

// SendMessage 一次输出模式（stream:false）：跑同一条编排，生成完毕返回最终 assistant 消息；
// 错误走标准错误信封 + 正常 HTTP 状态码。
func (s *chatService) SendMessage(ctx context.Context, req chatapi.SendMessageReq) (*chatapi.AssistantReplySchema, error) {
	return s.runTurn(ctx, req, nil)
}

// Stream 流式模式（stream:true）：产出 StreamEvent 逐个调 emit（handler 负责 SSE 帧 + Flush）。
func (s *chatService) Stream(ctx context.Context, req chatapi.SendMessageReq, emit func(chatapi.StreamEvent) error) error {
	_, err := s.runTurn(ctx, req, emit)
	return err
}

// runTurn 两模式共用的发消息编排；emit 为 nil 即一次输出模式。
// 返回的 error 只可能产生在任何 emit 之前（流式模式 handler 尚可回标准错误信封）。
func (s *chatService) runTurn(ctx context.Context, req chatapi.SendMessageReq, emit func(chatapi.StreamEvent) error) (*chatapi.AssistantReplySchema, error) {
	setup, err := s.setupTurn(ctx, req.ConversationID)
	if err != nil {
		return nil, err
	}

	// 历史先取（不含本轮 user 消息），保证「截断窗口 + 当前消息」以 user 开头
	history, err := s.loadHistory(ctx, setup.conv.ID, setup.agent.MaxContextTurns)
	if err != nil {
		return nil, err
	}

	// 落 user 消息：全部校验 / 配置通过之后（配置失败不留孤儿消息，重试不重复落库）
	userMsg := &Message{ConversationID: setup.conv.ID, Role: chatapi.RoleUser, Content: req.Content}
	if err := s.store.CreateMessage(ctx, userMsg); err != nil {
		return nil, fmt.Errorf("persist user message: %w", err)
	}
	if setup.conv.Title == "" {
		if terr := s.store.UpdateConversationTitle(ctx, setup.conv.ID, makeTitle(req.Content)); terr != nil {
			slog.WarnContext(ctx, "chat: backfill title failed", "conversation_id", setup.conv.ID, "err", terr)
		}
	}

	msgs := assembleMessages(setup.agent, history, req.Content)
	start := time.Now()
	stream, err := setup.client.Stream(ctx, msgs, callOptions(setup.agent))
	if err != nil {
		// 未触达上游（抢槽 / 熔断秒拒 / 工厂失败）不落 executions：error_class 七类 CHECK 无对应值，
		// 且无上游交互可记；503 侧的结构化日志足够排障。
		if isPreAttemptErr(err) {
			return nil, translateLLMError(err)
		}
		s.recordExecution(ctx, setup, msgs, start, "", chatapi.Usage{}, "", err)
		return nil, translateLLMError(err)
	}

	var (
		content      strings.Builder
		usage        chatapi.Usage
		finishReason string
		consumeErr   error
		clientGone   bool
	)
	for {
		m, rerr := stream.Recv()
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			consumeErr = rerr // 流中失败（首 token 后不重试，见 llm 重试纪律）
			break
		}
		if m == nil {
			continue // eino 约定错误帧返回 nil，正常流不该出现；防御跳过
		}
		if m.Content != "" {
			content.WriteString(m.Content)
			if emit != nil {
				if eerr := emit(chatapi.DeltaEvent(m.Content)); eerr != nil {
					clientGone = true // 前端断连：取消上游（省 token）、保留已生成内容
					break
				}
			}
		}
		if m.ResponseMeta != nil { // usage / finish 通常在尾帧
			if u := m.ResponseMeta.Usage; u != nil {
				usage = chatapi.Usage{Input: int64(u.PromptTokens), Output: int64(u.CompletionTokens)}
			}
			if m.ResponseMeta.FinishReason != "" {
				finishReason = m.ResponseMeta.FinishReason
			}
		}
	}
	_ = stream.Close() // 幂等：EOF 已自动收尾，错误 / 断连路径显式释放槽位

	reply := &chatapi.AssistantReplySchema{Content: content.String(), Usage: usage, FinishReason: finishReason}

	// ---- 收尾（顺序：assistant 落库 → touch → executions → done / error 事件）----

	// 已生成内容一律落库（含失败 / 断连的部分内容）：下轮上下文完整是硬需求
	if assistant := s.persistAssistant(ctx, setup.conv.ID, reply.Content); assistant != nil {
		reply.MessageID = strconv.FormatUint(assistant.ID, 10)
	}
	if terr := s.store.TouchConversation(ctx, setup.conv.ID); terr != nil {
		slog.WarnContext(ctx, "chat: touch conversation failed", "conversation_id", setup.conv.ID, "err", terr)
	}

	if consumeErr != nil || clientGone {
		callErr := consumeErr
		if callErr == nil {
			callErr = errClientGone
		}
		s.recordExecution(ctx, setup, msgs, start, reply.Content, usage, finishReason, callErr)
		if clientGone || errors.Is(consumeErr, context.Canceled) {
			return nil, nil // 客户端已断：ErrorEvent 也写不出去，静默收尾
		}
		if emit != nil {
			_ = emit(errorEventOf(consumeErr)) // 断连时写失败忽略
			return nil, nil
		}
		return nil, translateLLMError(consumeErr) // 一次输出模式：标准错误信封
	}

	s.recordExecution(ctx, setup, msgs, start, reply.Content, usage, finishReason, nil)
	if emit != nil {
		_ = emit(chatapi.DoneEvent(reply.MessageID, reply.Usage, reply.FinishReason))
	}
	return reply, nil
}

// setupTurn 校验与装配：会话属主 → agent（存在且启用）→ 模型调用配置 → 受保护 client。
// 全部通过后才落 user 消息；任何失败原样 / 翻译后返回。
func (s *chatService) setupTurn(ctx context.Context, conversationID uint64) (*llmSetup, error) {
	conv, err := s.getOwnedConversation(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	a, err := s.agents.Get(ctx, agentapi.GetAgentReq{ID: conv.AgentID})
	if err != nil {
		return nil, fmt.Errorf("load agent %d: %w", conv.AgentID, err) // 软删 agent 的存量会话同样被拒
	}
	if !a.Enabled {
		return nil, agentapi.ErrAgentDisabled
	}
	modelNum, err := strconv.ParseUint(a.ModelID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%w: agent %d model_id %q not numeric", errs.ErrInternal, conv.AgentID, a.ModelID)
	}
	cfg, err := s.providers.ResolveLLMConfig(ctx, providerapi.ResolveLLMConfigReq{ModelID: modelNum})
	if err != nil {
		return nil, fmt.Errorf("resolve llm config (model %d): %w", modelNum, err) // 哨兵透传
	}
	client, err := s.clients.Client(cfg.ProviderName, llm.UpstreamOptions{
		Kind:    llm.ProviderKind(cfg.Kind),
		BaseURL: cfg.BaseURL,
		APIKey:  cfg.APIKey, // 明文仅此链路持有，不落日志 / executions / 缓存（crypto 四不）
		Model:   cfg.ModelID,
	})
	if err != nil {
		return nil, fmt.Errorf("llm client for provider %q: %w", cfg.ProviderName, err)
	}
	return &llmSetup{conv: conv, agent: a, cfg: cfg, modelNum: modelNum, client: client}, nil
}

// loadHistory 取多轮上下文：倒序拉最近窗口、按 user 锚点整轮截断、反转为时序。
// v1 直读 PG（(conversation_id, id) 索引 keyset，~1ms 量级，远小于 TTFT）——
// 不做历史缓存（决策记录见 docs/changelog/chat/context_and_history_storage.md）。
func (s *chatService) loadHistory(ctx context.Context, conversationID uint64, maxTurns int) ([]Message, error) {
	rows, err := s.store.ListRecentMessages(ctx, conversationID, fetchRowsForTurns(maxTurns))
	if err != nil {
		return nil, fmt.Errorf("load history (conversation %d): %w", conversationID, err)
	}
	return truncateTurns(rows, maxTurns), nil
}

// fetchRowsForTurns 倒序拉取的窗口行数：轮数 × 每轮估计 + 一轮余量（窗口起点落半轮时
// 靠锚点截断兜底）；maxTurns 非法值（<1，DB CHECK 1-100 的防御）按 1 处理。
func fetchRowsForTurns(maxTurns int) int {
	if maxTurns < 1 {
		maxTurns = 1
	}
	return maxTurns*rowsPerTurnEstimate + rowsPerTurnEstimate
}

// truncateTurns 整轮截断：ms 为倒序（最新在前）的最近消息行。以 user 行作轮锚点——
// 收集到第 maxTurns 个锚点即止（锚点自身保留，更老的行属更早轮、整轮丢弃）；
// 窗口最老侧被 LIMIT 截掉锚点的残行（孤儿 assistant / tool 行）在最后丢弃——
// 发给 LLM 的历史永远以完整轮的 user 行开头（空历史除外）。返回时序（最旧在前）。
func truncateTurns(ms []Message, maxTurns int) []Message {
	if maxTurns < 1 {
		maxTurns = 1
	}
	kept := make([]Message, 0, len(ms))
	users := 0
	for _, m := range ms {
		kept = append(kept, m)
		if m.Role == chatapi.RoleUser {
			users++
			if users >= maxTurns {
				break // 已到第 maxTurns 个锚点：desc 中其后的行都更老，属被丢弃的轮
			}
		}
	}
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	// 丢弃头部残行：无锚点的半个轮（窗口截断产物）
	start := 0
	for start < len(kept) && kept[start].Role != chatapi.RoleUser {
		start++
	}
	return kept[start:]
}

// assembleMessages 组装发给 LLM 的消息序列：[system（agent 现取）] + 截断后历史 + 当前用户消息。
// system prompt 不落库（每次由 agent 配置拼装，改提示词下一轮立即生效——data_flow 决策）。
func assembleMessages(a *agentapi.AgentDetailSchema, history []Message, content string) []*schema.Message {
	msgs := make([]*schema.Message, 0, len(history)+2)
	if a.SystemPrompt != "" {
		msgs = append(msgs, schema.SystemMessage(a.SystemPrompt))
	}
	for i := range history {
		msgs = append(msgs, toEinoMessage(&history[i]))
	}
	msgs = append(msgs, schema.UserMessage(content))
	return msgs
}

// toEinoMessage 单条历史消息 → eino 消息。v1 历史只有 user / assistant；tool 中间行与
// assistant.tool_calls 是工具循环批次的落库形态，此处一并转换（灰度后老会话可直接重放）。
func toEinoMessage(m *Message) *schema.Message {
	switch m.Role {
	case chatapi.RoleUser:
		return schema.UserMessage(m.Content)
	case chatapi.RoleTool:
		return schema.ToolMessage(m.Content, firstToolCallID(m.ToolCalls))
	default: // assistant（未知 role 按 assistant 兜底，DB CHECK 之外不设防）
		am := &schema.Message{Role: schema.Assistant, Content: m.Content}
		if len(m.ToolCalls) > 0 {
			am.ToolCalls = make([]schema.ToolCall, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				id, _ := tc["id"].(string)
				tool, _ := tc["tool"].(string)
				am.ToolCalls = append(am.ToolCalls, schema.ToolCall{
					ID:   id,
					Type: "function",
					Function: schema.FunctionCall{
						Name:      tool,
						Arguments: marshalArgs(tc["args"]),
					},
				})
			}
		}
		return am
	}
}

// firstToolCallID tool 角色行的调用标识存在 ToolCalls[0]["id"]（messages 表无独立列，见 db 设计）。
func firstToolCallID(toolCalls []map[string]any) string {
	if len(toolCalls) == 0 {
		return ""
	}
	id, _ := toolCalls[0]["id"].(string)
	return id
}

// marshalArgs eino FunctionCall.Arguments 是 JSON 字符串；空 / 非法值返 ""。
func marshalArgs(v any) string {
	m, ok := v.(map[string]any)
	if !ok || len(m) == 0 {
		return ""
	}
	b, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	return string(b)
}

// callOptions agent 配置 → LLM 调用时选项。Temperature 恒传（agent 有 DB DEFAULT 0.7，
// 0 是合法的「严谨模式」值，不能用零值判断「未设置」）；MaxOutputTokens nil = 跟随模型默认；
// TopP agent 无此配置不传。Tools 工具循环批次注入。
func callOptions(a *agentapi.AgentDetailSchema) *llm.CallOptions {
	temp := float32(a.Temperature)
	opts := &llm.CallOptions{Temperature: &temp}
	if a.MaxOutputTokens != nil {
		opts.MaxTokens = *a.MaxOutputTokens
	}
	return opts
}

// persistAssistant 落 assistant 消息；失败只 WARN 不阻断——流式 token 已发出无法撤回，
// 消息丢失只影响下轮上下文（done 事件以空 message_id 降级）。
func (s *chatService) persistAssistant(ctx context.Context, conversationID uint64, content string) *Message {
	m := &Message{ConversationID: conversationID, Role: chatapi.RoleAssistant, Content: content}
	if err := s.store.CreateMessage(ctx, m); err != nil {
		slog.WarnContext(ctx, "chat: persist assistant message failed", "conversation_id", conversationID, "err", err)
		return nil
	}
	return m
}

// errClientGone emit 失败（前端断连）的内部标记，进 executions 时近似 Network。
var errClientGone = errors.New("chat: client gone")

// recordExecution 写运行日志（每次 LLM 调用一行，含失败——排障唯一线索）；
// 失败只 WARN：executions 不是业务依赖，落库故障不应打断对话。
func (s *chatService) recordExecution(ctx context.Context, setup *llmSetup, msgs []*schema.Message, start time.Time, output string, usage chatapi.Usage, finishReason string, callErr error) {
	e := &logging.Execution{
		ConversationID:   &setup.conv.ID,
		ModelID:          &setup.modelNum,
		ModelName:        setup.cfg.ModelID,
		Input:            map[string]any{"messages": summarizeMessages(msgs)},
		Output:           map[string]any{"content": output},
		PromptTokens:     usage.Input,
		CompletionTokens: usage.Output,
		TotalTokens:      usage.Input + usage.Output,
		DurationMs:       int32(time.Since(start).Milliseconds()),
		FinishReason:     finishReason,
	}
	if callErr != nil {
		if class := executionErrorClass(callErr); class != "" {
			c := string(class)
			e.ErrorClass = &c
		}
	}
	if err := s.execs.Create(ctx, e); err != nil {
		slog.WarnContext(ctx, "chat: record execution failed", "conversation_id", setup.conv.ID, "err", err)
	}
}

// executionErrorClass 错误 → executions.error_class（七类 CHECK，见 migrations/00007）。
// 客户端断连近似 Network（CHECK 无 Canceled 值，注释即决策）；未分类的意外错误同样
// Network 兜底——能走到这里说明上游交互已发生。
func executionErrorClass(err error) llm.Class {
	if class, ok := llm.Classify(err); ok {
		return class
	}
	return llm.ClassNetwork
}

// isPreAttemptErr 未真正发起上游调用的失败（bulkhead 抢槽 fail-fast / 熔断秒拒 /
// 不支持的 kind 工厂失败）——不落 executions。
func isPreAttemptErr(err error) bool {
	return errors.Is(err, llm.ErrProviderBusy) ||
		errors.Is(err, llm.ErrProviderUnavailable) ||
		errors.Is(err, llm.ErrUnsupportedKind)
}

// llmErrorSpec 错误 → 对外哨兵 + 用户消息 + 用户视角可否重试。单一事实源：
// translateLLMError（流前 / 一次输出模式的标准信封）与 errorEventOf（流中 ErrorEvent）共用。
// InvalidRequest 语义化为 MODEL_CONTEXT_TOO_LONG（400）；限流有专属码；
// 其余上游瞬断统一 PROVIDER_UNAVAILABLE（503）。
func llmErrorSpec(err error) (sentinel error, message string, retryable bool) {
	if class, ok := llm.Classify(err); ok {
		switch class {
		case llm.ClassInvalidRequest:
			return chatapi.ErrModelContextTooLong, "模型上下文超长或请求被拒绝，请修改输入或开启新会话", false
		case llm.ClassRateLimited:
			return errs.ErrRateLimited, "供应商限流，请稍后再试", true
		case llm.ClassAuth:
			return llm.ErrProviderUnavailable, "供应商鉴权失败，请检查 API Key 配置", false
		case llm.ClassTimeout:
			return llm.ErrProviderUnavailable, "供应商响应超时，请稍后重试", true
		case llm.ClassOverloaded, llm.ClassNetwork:
			return llm.ErrProviderUnavailable, "供应商暂时不可用，请稍后重试", true
		case llm.ClassProviderDown:
			return llm.ErrProviderUnavailable, "供应商无法连接，请稍后重试", false
		}
	}
	switch {
	case errors.Is(err, llm.ErrProviderBusy):
		return llm.ErrProviderBusy, "供应商并发已满，请稍后重试", true
	case errors.Is(err, llm.ErrProviderUnavailable):
		return llm.ErrProviderUnavailable, "供应商暂不可用（熔断保护中），请稍后重试", true
	}
	return errs.ErrServiceUnavailable, "生成失败，请稍后重试", false
}

// translateLLMError 流前 / 一次输出模式：LLM 错误 → 对外哨兵（保留原始错误文本供排障）。
func translateLLMError(err error) error {
	sentinel, _, _ := llmErrorSpec(err)
	if errors.Is(err, sentinel) {
		return err // 哨兵本体（busy / unavailable 等已是对外形态），保留调用链上下文
	}
	return fmt.Errorf("%w (%v)", sentinel, err)
}

// errorEventOf 流中失败 → SSE error 事件（code = 哨兵 Error()，机器可读）。
func errorEventOf(err error) chatapi.StreamEvent {
	sentinel, message, retryable := llmErrorSpec(err)
	return chatapi.ErrorEvent(sentinel.Error(), message, retryable)
}

// makeTitle 首条用户消息 → 会话标题：压平空白（含换行）、按 rune 截断（中文不裂半字）。
func makeTitle(content string) string {
	flat := strings.Join(strings.Fields(content), " ")
	r := []rune(flat)
	if len(r) > titleMaxRunes {
		return string(r[:titleMaxRunes])
	}
	return flat
}

// summarizeMessages LLM 请求侧快照（executions.input）：[{role, content}]——排障需要
// 真实输入；大文本依赖 TOAST，查询侧禁 SELECT *（数据库规范《大表处理策略》）。
func summarizeMessages(msgs []*schema.Message) []map[string]any {
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, map[string]any{"role": string(m.Role), "content": m.Content})
	}
	return out
}
