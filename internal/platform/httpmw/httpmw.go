// Package httpmw 提供全仓共享的 gin 请求中间件（横切层，无业务逻辑）：
//   - [RequestID]：每请求生成 trace_id 注入 ctx 与 X-Request-ID 响应头
//     （Java 对应物是请求入口 Filter 里 MDC.put(traceId)；Go 用 ctx 替代 MDC，
//     请求结束 ctx 自然回收，无需 MDC.clear）；
//   - [AccessLog]：访问日志——method/path/status/耗时/client_ip，慢请求 WARN。
//
// trace_id 的日志字段追加不在本包拼接：logging 的 trace handler 从 ctx 自动提取，
// 本包只负责把 ctx 传给 slog.*Context——一处机制，全仓生效。
package httpmw

import (
	"log/slog"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Karlsk/go-hify/internal/platform/traceid"
)

// DefaultSlowThreshold 请求耗时超过该值记 WARN（slow http request）。
const DefaultSlowThreshold = time.Second

// RequestID 为每个请求生成 trace_id 并注入：
//   - ctx：后续 slog.*Context / respond 500 body / 业务日志经 logging trace handler 自动关联；
//   - X-Request-ID 响应头：在 c.Next 之前写，即使后续 panic，前端 / 运维也能凭此头与日志对账
//     （CLAUDE.md §错误处理：500 类回 INTERNAL_ERROR + trace_id）。
//
// 一律服务端生成、不信任客户端传入值：防伪造与日志注入（内部工具唯一入口是 nginx，无上游透传需求）。
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := traceid.Generate()
		c.Request = c.Request.WithContext(traceid.With(c.Request.Context(), id))
		c.Header(traceid.Header, id)
		c.Next()
	}
}

// AccessLogConfig 是 [AccessLog] 的调优参数；零值取合理默认。
type AccessLogConfig struct {
	// SlowThreshold 慢请求阈值：耗时超过记 WARN；<=0 取 [DefaultSlowThreshold]（1s）。
	SlowThreshold time.Duration
	// SkipPaths 精确匹配即不记访问日志的路径（如被 healthcheck 周期探活的 /health，避免刷屏）。
	SkipPaths []string
}

// AccessLog 访问日志中间件：请求完成后记 method / path / status / duration_ms / client_ip，
// trace_id 经 ctx 由 logging trace handler 自动追加。
//
// 两条降噪规则（不对齐会产生淹没真问题的日志噪音）：
//   - SSE 流（响应 Content-Type: text/event-stream）设计上长连接 30s–5min，豁免慢请求 WARN，
//     仍记 INFO——按响应类型判定，未来任何 SSE 路由自动豁免；
//   - SkipPaths（如 /health）整条跳过——Docker healthcheck 周期探活不应进访问日志。
func AccessLog(cfg AccessLogConfig) gin.HandlerFunc {
	if cfg.SlowThreshold <= 0 {
		cfg.SlowThreshold = DefaultSlowThreshold
	}
	skip := make(map[string]struct{}, len(cfg.SkipPaths))
	for _, p := range cfg.SkipPaths {
		skip[p] = struct{}{}
	}
	return func(c *gin.Context) {
		if _, ok := skip[c.Request.URL.Path]; ok {
			c.Next()
			return
		}
		start := time.Now()
		c.Next()
		latency := time.Since(start)

		ctx := c.Request.Context()
		attrs := []any{
			slog.String("method", c.Request.Method),
			slog.String("path", c.Request.URL.Path),
			slog.Int("status", c.Writer.Status()),
			slog.Int64("duration_ms", latency.Milliseconds()),
			slog.String("client_ip", c.ClientIP()),
		}
		if latency > cfg.SlowThreshold && !isSSEResponse(c) {
			slog.WarnContext(ctx, "slow http request", attrs...)
			return
		}
		slog.InfoContext(ctx, "http request", attrs...)
	}
}

// isSSEResponse 判断响应是否为 SSE 流（chat/stream 等长连接）。
func isSSEResponse(c *gin.Context) bool {
	return strings.HasPrefix(c.Writer.Header().Get("Content-Type"), "text/event-stream")
}
