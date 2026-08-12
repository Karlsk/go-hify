package respond

import (
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"

	"github.com/Karlsk/go-hify/internal/platform/errs"
)

// Recovery 是兜底 panic 中间件：挂在中间件链最外层（main.go 里 r.Use(respond.Recovery())），
// 捕获其后所有中间件 / handler 的未处理 panic（nil 指针、越界、第三方库 panic 等），
// 记栈 + 写 500 信封 + abort。业务错误走返回值，永远不应 panic 到这一层。
//
// 唯一豁免 http.ErrAbortHandler：net/http 用它表示「主动中断连接」，必须原样 re-panic，
// 与 gin 内置 Recovery 行为一致；除此之外不特殊豁免任何 panic 值。
// panic 值绝不透传给客户端——响应体固定 INTERNAL_ERROR + "internal server error"。
//
// 已开始写响应（headers sent，如 SSE 流中途 panic）时无法改状态码，仅 abort；
// SSE 的流内错误应由 chat handler 自身 defer/recover 发 event:error，本中间件是最后兜底。
//
// 日志走 slog（platform/logging.Init 已 slog.SetDefault）：记 panic 值 + 完整栈。
// TODO: trace_id —— 待 gin 中间件把 trace_id 注入 ctx 后，slog handler 从 ctx 提取（见 platform/logging）。
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				if r == http.ErrAbortHandler {
					panic(r) // 保留 net/http 的中断语义，不记栈、不写响应
				}
				slog.Error("panic recovered",
					slog.Any("panic", r),
					slog.String("stack", string(debug.Stack())),
				)
				noStore(c)
				if c.Writer.Written() {
					// 响应已在途（如 SSE 流），无法改写状态码，仅 abort 中断后续。
					c.Abort()
					return
				}
				c.AbortWithStatusJSON(http.StatusInternalServerError, Result{
					Success: false,
					Error: &ErrorBody{
						Code:    errs.ErrInternal.Error(),
						Message: "internal server error",
					},
				})
			}
		}()
		c.Next()
	}
}
