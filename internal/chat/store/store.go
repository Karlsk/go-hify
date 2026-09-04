// Package store 是 chat 模块的数据层：实现 service.Store（GORM CRUD + keyset 分页）。
// error 原样上抛（含 gorm.ErrRecordNotFound，供 service 层翻译哨兵），业务翻译在 service 层。
package store

import (
	"context"
	"time"

	"gorm.io/gorm"

	chatsvc "github.com/Karlsk/go-hify/internal/chat/service"
)

var _ chatsvc.Store = (*Store)(nil) // 编译期断言：Store 实现了 service.Store

// 显式列清单（禁 SELECT *，CLAUDE.md《SQL 编写规范》；列序与迁移 SQL 一致）。
const (
	selectConversation = "id, user_id, agent_id, title, created_at, updated_at"
	selectMessage      = "id, conversation_id, role, content, tool_calls, created_at"
)

// Store 实现 chatsvc.Store；error 原样上抛，业务翻译在 service 层。
type Store struct{ db *gorm.DB }

// New 创建 Store。
func New(db *gorm.DB) *Store { return &Store{db: db} }

// ---- conversations ----

// CreateConversation 插入会话（id / created_at / updated_at 由 DB 生成并经 RETURNING 回填）。
func (s *Store) CreateConversation(ctx context.Context, c *chatsvc.Conversation) error {
	return s.db.WithContext(ctx).Create(c).Error
}

// GetConversationByID 按主键查；未找到返回 gorm.ErrRecordNotFound。
func (s *Store) GetConversationByID(ctx context.Context, id uint64) (*chatsvc.Conversation, error) {
	var c chatsvc.Conversation
	err := s.db.WithContext(ctx).Select(selectConversation).First(&c, id).Error
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// ListConversationsByCursor 用户会话列表 keyset 查询：
// WHERE user_id = $1 [AND (updated_at, id) < ($2, $3)] ORDER BY updated_at DESC, id DESC LIMIT $n。
// 行值比较走 (updated_at, id) 复合索引 idx_conversations_user，无 OFFSET、无 Sort 节点。
func (s *Store) ListConversationsByCursor(ctx context.Context, userID uint64, beforeUpdatedAt time.Time, beforeID uint64, limit int) ([]chatsvc.Conversation, error) {
	q := s.db.WithContext(ctx).
		Select(selectConversation).
		Where("user_id = ?", userID)
	if !beforeUpdatedAt.IsZero() {
		q = q.Where("(updated_at, id) < (?, ?)", beforeUpdatedAt, beforeID)
	}
	var cs []chatsvc.Conversation
	err := q.Order("updated_at DESC, id DESC").Limit(limit).Find(&cs).Error
	return cs, err
}

// UpdateConversationTitle 回填标题。UpdateColumn 不触发 autoUpdateTime——标题回填不改排序键
// （排序键由同一收尾里的 TouchConversation 维护）；RowsAffected=0 视为会话已不存在。
func (s *Store) UpdateConversationTitle(ctx context.Context, id uint64, title string) error {
	res := s.db.WithContext(ctx).
		Model(&chatsvc.Conversation{}).
		Where("id = ?", id).
		UpdateColumn("title", title)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// TouchConversation 更新 updated_at（列表排序键）。显式传 now 而非依赖 autoUpdateTime：
// UpdateColumn 路径绕过 GORM 的自动时间维护，语义直白；RowsAffected=0 视为会话已不存在。
func (s *Store) TouchConversation(ctx context.Context, id uint64) error {
	res := s.db.WithContext(ctx).
		Model(&chatsvc.Conversation{}).
		Where("id = ?", id).
		UpdateColumn("updated_at", time.Now().UTC())
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// DeleteConversation 删除会话；messages 经 FK ON DELETE CASCADE 级联删（DB 层保证）。
func (s *Store) DeleteConversation(ctx context.Context, id uint64) error {
	res := s.db.WithContext(ctx).Delete(&chatsvc.Conversation{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// ---- messages ----

// CreateMessage 追加一条消息；ToolCalls 为 nil 时归一为空集合（serializer 会把 nil 写成
// json null 落库，绕过列 DEFAULT '[]'）。
func (s *Store) CreateMessage(ctx context.Context, m *chatsvc.Message) error {
	if m.ToolCalls == nil {
		m.ToolCalls = []map[string]any{}
	}
	return s.db.WithContext(ctx).Create(m).Error
}

// ListMessagesAfter 会话内正序取 id > afterID 的 limit 行（历史查询游标）；
// 索引 idx_messages_conversation (conversation_id, id) 全覆盖。
func (s *Store) ListMessagesAfter(ctx context.Context, conversationID uint64, afterID uint64, limit int) ([]chatsvc.Message, error) {
	var ms []chatsvc.Message
	err := s.db.WithContext(ctx).
		Select(selectMessage).
		Where("conversation_id = ? AND id > ?", conversationID, afterID).
		Order("id").
		Limit(limit).
		Find(&ms).Error
	return ms, err
}

// ListRecentMessages 会话内倒序取最近 limit 行（含 tool 中间行）；service 按轮整段截断后反转成时序。
func (s *Store) ListRecentMessages(ctx context.Context, conversationID uint64, limit int) ([]chatsvc.Message, error) {
	var ms []chatsvc.Message
	err := s.db.WithContext(ctx).
		Select(selectMessage).
		Where("conversation_id = ?", conversationID).
		Order("id DESC").
		Limit(limit).
		Find(&ms).Error
	return ms, err
}
