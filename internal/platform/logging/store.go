package logging

import (
	"context"

	"gorm.io/gorm"
)

// ExecutionStore 是 executions 表的数据层：append-only 只写不改（排障查询走 psql，
// 后期需要再补读方法）。消费方：chat（每次 LLM 调用的收尾）、workflow（LLM 节点，后续批次）。
// 写入时机遵循 CLAUDE.md：「执行日志在流结束后的短连接里写」——绝不持长事务、不在流中写。
type ExecutionStore struct{ db *gorm.DB }

// NewExecutionStore 创建 ExecutionStore。
func NewExecutionStore(db *gorm.DB) *ExecutionStore { return &ExecutionStore{db: db} }

// Create 插入一行运行日志；id / created_at 由 DB 生成并经 RETURNING 回填。
// error 原样上抛（可 %w 加上下文，由消费方决定记日志还是上抛——executions 写失败不阻断对话主流程时，
// 消费方记 WARN 即可，故本层不吞错）。
func (s *ExecutionStore) Create(ctx context.Context, e *Execution) error {
	normalizeJSONB(e)
	return s.db.WithContext(ctx).Create(e).Error
}

// normalizeJSONB 把 nil 的 jsonb 字段归一为空集合：serializer:json 会把 nil 序列化成
// json null 显式落库（绕过列 DEFAULT），NOT NULL 语义与排障查询都期望 '{}' / '[]'。
func normalizeJSONB(e *Execution) {
	if e.Input == nil {
		e.Input = map[string]any{}
	}
	if e.Output == nil {
		e.Output = map[string]any{}
	}
	if e.ToolChain == nil {
		e.ToolChain = []map[string]any{}
	}
}
