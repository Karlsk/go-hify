package db

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"gorm.io/gorm"
)

// 本文件给 store 层两件分页工具，配合 CLAUDE.md《分页查询规范》《接口规范·分页》：
//   - [Paginate]：offset 分页作用域，仅给极小静态配置表用（providers/agents/models/...）；
//   - [EncodeCursor] / [DecodeCursor]：keyset 游标编解码，给大列表（conversations/messages/executions）。
//
// respond 包的 CursorMeta / OffsetMeta 负责 HTTP 响应形状；本文件负责 store 侧取数与游标构造。

// Paginate 返回一个 GORM 作用域，对【极小静态配置表】做 offset 分页。
// 仅用于 providers / agents / models / mcp_servers / knowledge_bases / workflows 等
// 行数极小、OFFSET 无性能问题的表（CLAUDE.md《分页》：OFFSET 仅例外表）。
// 大列表必须走 keyset（见 EncodeCursor），禁止用本作用域。
//
// page 从 1 起、pageSize 默认 20、上限 100（与 CLAUDE.md《接口规范·分页》一致）：
//
//	db.Scopes(db.Paginate(req.Page, req.PageSize)).Find(&items)
func Paginate(page, pageSize int) func(*gorm.DB) *gorm.DB {
	return func(tx *gorm.DB) *gorm.DB {
		if page < 1 {
			page = 1
		}
		switch {
		case pageSize < 1:
			pageSize = 20
		case pageSize > 100:
			pageSize = 100
		}
		offset := (page - 1) * pageSize
		return tx.Offset(offset).Limit(pageSize)
	}
}

// EncodeCursor 把游标载荷（keyset 排序键，如 {UpdatedAt time.Time; ID uint64}）
// 编码成对前端不透明的 base64 字符串，填入 respond.CursorMeta.NextCursor。
// 用 RawURLEncoding（URL 安全、无 padding），前端原样回传、无需理解内容。
func EncodeCursor(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("encode cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// DecodeCursor 把前端原样回传的 cursor 解回游标载荷。首页（无 cursor）不调用。
// 任一步失败返回包装错误，store 层按 InvalidRequest / 参数非法处理。
func DecodeCursor(s string, v any) error {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return fmt.Errorf("decode cursor: %w", err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("unmarshal cursor: %w", err)
	}
	return nil
}
