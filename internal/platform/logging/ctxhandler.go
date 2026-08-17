package logging

import (
	"context"
	"log/slog"

	"github.com/Karlsk/go-hify/internal/platform/traceid"
)

// AttrTraceID 是 trace_id 日志字段名：traceHandler 与调用方显式传入共用此 key，
// 保证「ctx 自动提取」与「调用方显式带」两种来源在日志里可检索同一字段。
const AttrTraceID = "trace_id"

// traceHandler 包装任意 slog.Handler：Handle 时从 ctx 提取 trace_id 追加为结构化字段，
// 使全仓 slog.*Context 调用自动获得请求级关联（httpmw.RequestID 在请求入口注入 ctx）。
// 调用方零感知；不在请求 ctx 内的日志（启动 / 后台任务）原样输出，不追加。
type traceHandler struct {
	next slog.Handler
}

// newTraceHandler 返回包装 next 的 trace 提取 handler。
func newTraceHandler(next slog.Handler) slog.Handler { return &traceHandler{next: next} }

// Enabled 透传底层 handler 的级别判定。
func (h *traceHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

// Handle 在记录转发底层前追加 trace_id；记录已带同名字段时以调用方为准（不覆盖、不重复）。
func (h *traceHandler) Handle(ctx context.Context, r slog.Record) error {
	if id, ok := traceid.From(ctx); ok && !hasAttr(r, AttrTraceID) {
		r.AddAttrs(slog.String(AttrTraceID, id))
	}
	return h.next.Handle(ctx, r)
}

// WithAttrs 派生 handler 仍保持 trace 包装在最外层，派生链上的日志继续自动带 trace_id。
func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceHandler{next: h.next.WithAttrs(attrs)}
}

// WithGroup 同 [traceHandler.WithAttrs]：包装保持在最外层。
func (h *traceHandler) WithGroup(name string) slog.Handler {
	return &traceHandler{next: h.next.WithGroup(name)}
}

// hasAttr 判断记录是否已含指定 key 的属性。
func hasAttr(r slog.Record, key string) bool {
	found := false
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			found = true
			return false
		}
		return true
	})
	return found
}
