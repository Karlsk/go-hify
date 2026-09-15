// Package service 是 chat 模块的业务层：实现 api 接口（对话编排——上下文组装、SSE 事件流、
// 一次输出模式、executions 落库）。组合根注入 store、agent / provider 的 api 接口与 platform 组件。
//
// 发消息两模式的编排在 turn.go；本文件是 service 骨架与会话 CRUD。
package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"gorm.io/gorm"

	agentapi "github.com/Karlsk/go-hify/internal/agent/api"
	chatapi "github.com/Karlsk/go-hify/internal/chat/api"
	"github.com/Karlsk/go-hify/internal/platform/authctx"
	"github.com/Karlsk/go-hify/internal/platform/errs"
	"github.com/Karlsk/go-hify/internal/platform/llm"
	"github.com/Karlsk/go-hify/internal/platform/logging"
	"github.com/Karlsk/go-hify/internal/platform/page"
	platformschema "github.com/Karlsk/go-hify/internal/platform/schema"
	providerapi "github.com/Karlsk/go-hify/internal/provider/api"
	ragapi "github.com/Karlsk/go-hify/internal/rag/api"
)

// Store 数据层接口：定义在消费方（本包），store 包实现，组合根注入——依赖倒置。
// v1 无跨表事务：建会话 / 落消息 / 回填标题各是单语句写，故无 WithTx；
// 「落 assistant 消息 + touch 会话排序键」两步允许非原子（touch 失败仅列表排序滞后，记 WARN 不回滚消息）。
type Store interface {
	// ---- conversations ----

	// CreateConversation 插入会话（id / 时间戳由 DB 生成经 RETURNING 回填）。
	CreateConversation(ctx context.Context, c *Conversation) error

	// GetConversationByID 按主键查；未找到返回 gorm.ErrRecordNotFound。
	GetConversationByID(ctx context.Context, id uint64) (*Conversation, error)

	// ListConversationsByCursor 用户会话列表 keyset 分页（updated_at DESC, id DESC）。
	// beforeUpdatedAt / beforeID 是上一页末行排序键（首页传零值，不带比较条件）；
	// limit 已含「多取一条」（service 用 page.FetchN 计算后传入）。
	ListConversationsByCursor(ctx context.Context, userID uint64, beforeUpdatedAt time.Time, beforeID uint64, limit int) ([]Conversation, error)

	// UpdateConversationTitle 回填标题（首条用户消息截断，仅一次）；会话不存在返回 gorm.ErrRecordNotFound。
	UpdateConversationTitle(ctx context.Context, id uint64, title string) error

	// TouchConversation 更新 updated_at（每落一条消息调，列表排序依据）；
	// 会话不存在返回 gorm.ErrRecordNotFound。
	TouchConversation(ctx context.Context, id uint64) error

	// DeleteConversation 删除会话（messages 经 FK 级联删）；未找到返回 gorm.ErrRecordNotFound。
	DeleteConversation(ctx context.Context, id uint64) error

	// ---- messages ----

	// CreateMessage 追加一条消息；ToolCalls 为 nil 时归一为 '[]'（防 jsonb null）。
	CreateMessage(ctx context.Context, m *Message) error

	// ListMessagesAfter 会话内正序取 id > afterID 的 limit 行（历史查询游标，afterID=0 即首页）。
	ListMessagesAfter(ctx context.Context, conversationID uint64, afterID uint64, limit int) ([]Message, error)

	// ListRecentMessages 会话内倒序取最近 limit 行（含 tool 中间行），供 service 按轮整段截断后反转。
	ListRecentMessages(ctx context.Context, conversationID uint64, limit int) ([]Message, error)
}

// 下游依赖收窄为小接口（Go 小接口惯例，CLAUDE.md《跨模块调用规则》补充机制）：只用到的
// 几个方法，组合根注入的完整实现（agentapi.AgentService / providerapi.ModelService /
// *llm.Manager / *logging.ExecutionStore）凭结构化类型天然满足，注入方式不变。
type (
	// agentGetter chat 用到的 agent 能力（详情 + 启用态 + 生成参数）。
	agentGetter interface {
		Get(ctx context.Context, req agentapi.GetAgentReq) (*agentapi.AgentDetailSchema, error)
	}

	// llmConfigResolver chat 用到的 provider 能力（模型 → 调用配置）。
	llmConfigResolver interface {
		ResolveLLMConfig(ctx context.Context, req providerapi.ResolveLLMConfigReq) (*providerapi.LLMConfig, error)
	}

	// llmClientFactory chat 用到的 llm.Manager 能力（受保护客户端按 provider+model 取/建）。
	llmClientFactory interface {
		Client(key string, opts llm.UpstreamOptions) (*llm.Client, error)
	}

	// executionWriter 运行日志落库（logging.ExecutionStore 的单方法收窄）。
	executionWriter interface {
		Create(ctx context.Context, e *logging.Execution) error
	}

	// ragRetriever chat 用到的 rag 能力（知识库检索注入——按绑定 KB 召回 chunk，
	// ragapi.KnowledgeBaseService 的单方法收窄）。
	ragRetriever interface {
		Retrieve(ctx context.Context, req ragapi.RetrieveReq) ([]ragapi.RetrievedChunk, error)
	}
)

// chatService 实现 chatapi.ChatService。
type chatService struct {
	store     Store
	agents    agentGetter
	providers llmConfigResolver
	clients   llmClientFactory
	execs     executionWriter
	rags      ragRetriever
}

// New 组装 chatService，返回 api 接口；由组合根注入 handler 与上游模块。
func New(store Store, agents agentGetter, providers llmConfigResolver, clients llmClientFactory, execs executionWriter, rags ragRetriever) chatapi.ChatService {
	return &chatService{store: store, agents: agents, providers: providers, clients: clients, execs: execs, rags: rags}
}

// convCursorKey 会话列表 keyset 复合排序键（updated_at DESC, id DESC），经 page 编码为不透明 cursor。
type convCursorKey struct {
	UpdatedAt time.Time `json:"u"`
	ID        uint64    `json:"i"`
}

// ---- 会话 CRUD ----

// CreateConversation 建会话（绑 Agent，中途不换）。
// agent 校验失败（不存在 / 停用）不落库，直接透传哨兵。
func (s *chatService) CreateConversation(ctx context.Context, req chatapi.CreateConversationReq) (*chatapi.ConversationSchema, error) {
	a, err := s.agents.Get(ctx, agentapi.GetAgentReq{ID: req.AgentID})
	if err != nil {
		return nil, fmt.Errorf("load agent %d: %w", req.AgentID, err) // 哨兵透传（ErrAgentNotFound）
	}
	if !a.Enabled {
		return nil, agentapi.ErrAgentDisabled // 停用：保留配置、新会话被拒
	}
	u, ok := authctx.UserFrom(ctx)
	if !ok {
		return nil, fmt.Errorf("%w: chat: user missing from ctx", errs.ErrInternal) // auth 中间件保证注入；缺失 = 装配错误
	}
	c := &Conversation{UserID: u.ID, AgentID: req.AgentID}
	if err := s.store.CreateConversation(ctx, c); err != nil {
		return nil, fmt.Errorf("create conversation: %w", err)
	}
	return toConversationSchema(c), nil
}

// ListConversations 当前用户的会话列表（keyset 游标）。
func (s *chatService) ListConversations(ctx context.Context, req chatapi.ListConversationsReq) (*chatapi.ConversationListResult, error) {
	u, ok := authctx.UserFrom(ctx)
	if !ok {
		return nil, fmt.Errorf("%w: chat: user missing from ctx", errs.ErrInternal)
	}
	params := page.NewCursor(req.Limit, req.Cursor)
	key, err := page.DecodeCursor[convCursorKey](params.Cursor)
	if err != nil {
		return nil, fmt.Errorf("%w: cursor: %v", errs.ErrValidationFailed, err) // 篡改 / 格式不对 → 400
	}
	cs, err := s.store.ListConversationsByCursor(ctx, u.ID, key.UpdatedAt, key.ID, params.FetchN())
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	res, err := page.NewCursorResult(cs, params.Limit, func(c Conversation) convCursorKey {
		return convCursorKey{UpdatedAt: c.UpdatedAt, ID: c.ID}
	})
	if err != nil {
		return nil, fmt.Errorf("build cursor result: %w", err)
	}
	items := make([]chatapi.ConversationSchema, 0, len(res.Items))
	for i := range res.Items {
		items = append(items, *toConversationSchema(&res.Items[i]))
	}
	return &chatapi.ConversationListResult{
		Items:      items, // make 兜底非 nil
		Limit:      res.Limit,
		HasMore:    res.HasMore,
		NextCursor: res.NextCursor,
	}, nil
}

// ListMessages 会话内历史消息（正序 after_id 游标；after_id=0 即首页）。
func (s *chatService) ListMessages(ctx context.Context, req chatapi.ListMessagesReq) (*chatapi.MessageListResult, error) {
	if _, err := s.getOwnedConversation(ctx, req.ConversationID); err != nil {
		return nil, err
	}
	params := page.NewCursor(req.Limit, "")
	ms, err := s.store.ListMessagesAfter(ctx, req.ConversationID, req.AfterID, params.FetchN())
	if err != nil {
		return nil, fmt.Errorf("list messages (conversation %d): %w", req.ConversationID, err)
	}
	hasMore := len(ms) > params.Limit
	if hasMore {
		ms = ms[:params.Limit]
	}
	var nextAfterID uint64
	if hasMore && len(ms) > 0 {
		nextAfterID = ms[len(ms)-1].ID // 下一页起点 = 本页末行 id（单列游标，无需 base64）
	}
	items := make([]chatapi.MessageSchema, 0, len(ms))
	for i := range ms {
		items = append(items, *toMessageSchema(&ms[i]))
	}
	return &chatapi.MessageListResult{Items: items, Limit: params.Limit, HasMore: hasMore, NextAfterID: nextAfterID}, nil
}

// DeleteConversation 删除会话（messages 经 FK 级联删）。
func (s *chatService) DeleteConversation(ctx context.Context, req chatapi.DeleteConversationReq) error {
	if _, err := s.getOwnedConversation(ctx, req.ID); err != nil {
		return err
	}
	if err := s.store.DeleteConversation(ctx, req.ID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return chatapi.ErrConversationNotFound
		}
		return fmt.Errorf("delete conversation %d: %w", req.ID, err)
	}
	return nil
}

// getOwnedConversation 取会话并校验属主：非本人会话视同不存在（不泄露存在性）。
// 一期不做权限体系，但会话是用户私有数据——URL 猜 id 不应读到他人对话。
func (s *chatService) getOwnedConversation(ctx context.Context, id uint64) (*Conversation, error) {
	c, err := s.store.GetConversationByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, chatapi.ErrConversationNotFound // 边界处翻译：gorm 哨兵不出本层
		}
		return nil, fmt.Errorf("get conversation %d: %w", id, err)
	}
	u, ok := authctx.UserFrom(ctx)
	if !ok {
		return nil, fmt.Errorf("%w: chat: user missing from ctx", errs.ErrInternal)
	}
	if c.UserID != u.ID {
		return nil, chatapi.ErrConversationNotFound
	}
	return c, nil
}

// ---- schema 转换 ----

func toConversationSchema(c *Conversation) *chatapi.ConversationSchema {
	return &chatapi.ConversationSchema{
		BaseSchema: platformschema.BaseSchema{
			ID:        strconv.FormatUint(c.ID, 10),
			CreatedAt: c.CreatedAt,
			UpdatedAt: c.UpdatedAt,
		},
		AgentID: strconv.FormatUint(c.AgentID, 10),
		Title:   c.Title,
	}
}

func toMessageSchema(m *Message) *chatapi.MessageSchema {
	s := &chatapi.MessageSchema{
		ID:        strconv.FormatUint(m.ID, 10),
		Role:      m.Role,
		Content:   m.Content,
		CreatedAt: m.CreatedAt,
	}
	if len(m.ToolCalls) == 0 {
		s.ToolCalls = []chatapi.ToolCall{} // 空返 [] 不返 null（接口规范《空值约定》）
	} else {
		tcs := make([]chatapi.ToolCall, 0, len(m.ToolCalls))
		for _, tc := range m.ToolCalls {
			id, _ := tc["id"].(string)
			tool, _ := tc["tool"].(string)
			args, _ := tc["args"].(map[string]any)
			if args == nil {
				args = map[string]any{}
			}
			tcs = append(tcs, chatapi.ToolCall{ID: id, Tool: tool, Args: args})
		}
		s.ToolCalls = tcs
	}
	if len(m.Citations) == 0 {
		s.Citations = []chatapi.Citation{} // 同款空态约定
	} else {
		s.Citations = m.Citations // model 与 schema 同用 chatapi.Citation，直传
	}
	return s
}
