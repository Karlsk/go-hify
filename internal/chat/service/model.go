package service

import (
	chatapi "github.com/Karlsk/go-hify/internal/chat/api"
	"github.com/Karlsk/go-hify/internal/platform/db"
)

// Conversation 会话头（GORM 实体，模块私有，禁止跨模块）：一行 = 一次持续对话，
// 只存元数据不存内容；列表页只查这张小表。表结构见 migrations/00006_chat_workflow.sql，
// 索引与 CHECK 由迁移 SQL 持有，model 不重复声明。
type Conversation struct {
	db.BaseMutable        // id + created_at + updated_at（updated_at 是列表排序键，每落一条消息 touch）
	UserID         uint64 `gorm:"not null"` // 归属用户，列表按它过滤
	AgentID        uint64 `gorm:"not null"` // 创建时绑定，中途不换
	Title          string `gorm:"not null"` // 取首条用户消息截断生成，列表展示用
}

func (Conversation) TableName() string { return "conversations" }

// Message 对话消息（GORM 实体，模块私有）：append-only，历史不可变——无 updated_at，
// 纠错靠追加新消息不靠改旧行。表结构见 migrations/00006_chat_workflow.sql。
type Message struct {
	db.BaseAppendOnly                    // id 单调递增 = 天然消息顺序 = 翻页游标
	ConversationID    uint64             `gorm:"not null"`
	Role              string             `gorm:"not null"` // user / assistant / tool（CHECK 由迁移持有）
	Content           string             `gorm:"not null"`
	ToolCalls         []map[string]any   `gorm:"type:jsonb;serializer:json"` // assistant 中间行专用：[{id, tool, args}]；v1 无工具循环恒 '[]'
	Citations         []chatapi.Citation `gorm:"type:jsonb;serializer:json"` // assistant 行 RAG 引用来源（buildSystemPrompt 检索命中）；user/tool 行恒 '[]'
}

func (Message) TableName() string { return "messages" }
