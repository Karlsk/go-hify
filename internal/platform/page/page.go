// Package page 提供 Hify 两种分页模式的后端查询参数与结果组装（见 CLAUDE.md《分页》）。
//
// 两种模式：
//   - 偏移分页（OffsetParams / OffsetResult）：仅极小静态配置表用（providers/agents/
//     models/mcp_servers/knowledge_bases/workflows），page 从 1 起、total 精确算、OFFSET 可接受。
//   - 游标分页（CursorParams / CursorResult）：大列表默认模式（conversations/messages/
//     executions），keyset 查询、禁用 OFFSET、has_more 用 LIMIT n+1 判定。
//
// 本包是 GORM 查询侧的封装，与 platform/respond（HTTP 信封 + 分页 meta）解耦：respond 负责把
// 结果写成 JSON 信封（OKWithOffset / OKWithCursor），本包只管参数归一化与结果组装；handler 层做
// 薄映射把 page.Result 的字段喂给 respond.OKWith*。本包不 import respond，避免 gorm 依赖反向污染
// 信封包（respond 当前零 gorm 依赖，应保持）。
package page

const (
	// DefaultPageSize 默认页大小，两种模式共用（CLAUDE.md《分页》：默认 20）。
	DefaultPageSize = 20
	// MaxPageSize 页大小上限，两种模式共用（CLAUDE.md《分页》：上限 100）。
	MaxPageSize = 100
)
