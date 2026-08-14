package page

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// CursorParams 是游标分页的查询参数（大列表默认模式，见 CLAUDE.md《分页》）。
// Cursor 是上一页返回的 next_cursor（不透明 base64 排序键），首页为空。
// 用于 conversations/messages/executions 等会增长的列表，配套 keyset 查询、禁用 OFFSET。
type CursorParams struct {
	Limit  int
	Cursor string
}

// NewCursor 把前端传入的 limit/cursor 归一化为合法 CursorParams：
//   - limit <= 0 → DefaultPageSize（20）；
//   - limit > MaxPageSize → MaxPageSize（100）；
//   - cursor 原样保留（首页为空串）。
func NewCursor(limit int, cursor string) CursorParams {
	switch {
	case limit <= 0:
		limit = DefaultPageSize
	case limit > MaxPageSize:
		limit = MaxPageSize
	}
	return CursorParams{Limit: limit, Cursor: cursor}
}

// FetchN 返回实际应向 DB 取的行数：Limit + 1。多取的一条用于判定 has_more（取到 limit+1 行 →
// 还有下一页），随后在 NewCursorResult 截断。这比额外发一次 COUNT 更省——大表 COUNT 昂贵，
// CLAUDE.md《分页》大列表不算精确 COUNT。
func (p CursorParams) FetchN() int {
	return p.Limit + 1
}

// EncodeCursor 把排序键编码为不透明 base64 字符串，作为 next_cursor 由前端原样回传。
// key 是表特定的排序键（如 conversations 的 {updated_at, id}），JSON 序列化后用 RawURLEncoding
// （无填充、URL 安全）base64，cursor 进 query string 无需转义。
func EncodeCursor[T any](key T) (string, error) {
	b, err := json.Marshal(key)
	if err != nil {
		return "", fmt.Errorf("marshal cursor key: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// DecodeCursor 把不透明游标解码回排序键。空 cursor 视为首页（返回零值、无错）。
// base64 或 JSON 解析失败返回错误（cursor 被篡改 / 格式不对）。
func DecodeCursor[T any](cursor string) (T, error) {
	var zero T
	if cursor == "" {
		return zero, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return zero, fmt.Errorf("decode cursor base64: %w", err)
	}
	var key T
	if err := json.Unmarshal(b, &key); err != nil {
		return zero, fmt.Errorf("unmarshal cursor key: %w", err)
	}
	return key, nil
}

// CursorResult[T] 是游标分页的查询结果：列表 + has_more + 下一页游标。
// HasMore=false 时 NextCursor 为 ""（respond.OKWithCursor 会把它序列化为 null）。
type CursorResult[T any] struct {
	Items      []T
	Limit      int
	HasMore    bool
	NextCursor string
}

// NewCursorResult 从"多取一条"的查询结果组装游标分页结果。items 由 store 用 FetchN() 取回，
// 可能含 Limit+1 条：
//   - len(items) > Limit → HasMore=true，截断到 Limit 条；
//   - keyFn 从截断后的最后一条提取排序键，编码为 NextCursor（下一页的起点）；
//   - HasMore=false 或无数据时 NextCursor=""。
//
// keyFn 是"表特定排序键"的接缝：conversations 传 func(c){return {c.UpdatedAt,c.ID}}，
// messages 传 func(m){return {m.ID}}。本包不关心排序键结构，只负责编码与截断。
func NewCursorResult[T any, K any](items []T, limit int, keyFn func(T) K) (CursorResult[T], error) {
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit] // 截掉多取的那条
	}
	var nextCursor string
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		encoded, err := EncodeCursor(keyFn(last))
		if err != nil {
			return CursorResult[T]{}, fmt.Errorf("encode next cursor: %w", err)
		}
		nextCursor = encoded
	}
	if items == nil {
		items = []T{}
	}
	return CursorResult[T]{Items: items, Limit: limit, HasMore: hasMore, NextCursor: nextCursor}, nil
}
