// Package logging 统一管理 Hify 的结构化日志（log/slog）。
//
// 调用 [Init] 后：JSON（或 text）输出到 stdout + 可选文件（按大小 rotate，lumberjack），
// 并 slog.SetDefault 全局生效——Recovery / gormLogger / 全仓 slog.* 调用统一走此句柄。
// 句柄外层统一包一层 trace 提取（见 ctxhandler.go）：slog.*Context 调用自动从 ctx 提取
// 请求级 trace_id（httpmw.RequestID 在请求入口注入）成为日志字段，调用方零感知。
// 组合根（internal/app）把 [Init] 放在 platform 初始化第一步，其余一切（db/redis/业务）才都有日志可用。
//
// executions 运行日志（LLM 调用审计：provider/model/token/耗时/错误类）的 model 与 store
// 也落在本包（见 model.go / store.go）：platform 定位使 chat 与将来的 workflow 都能写
// （workflow 禁止依赖 chat，executions 不能归 chat 模块）；「执行日志在流结束后的短连接里写」
// 由消费方保证，store 只提供 append-only 写入。
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/natefinch/lumberjack.v2"
)

// Config 是日志装配参数；零值走 [Config.withDefaults] 的合理默认。
type Config struct {
	Level  string // debug/info/warn/error，默认 info
	Format string // json（默认，生产机器可读）/ text（开发可读）
	File   string // 日志文件路径；空 = 只 stdout、不落文件
	// 以下仅 File 非空时生效（lumberjack rotate 参数）
	MaxSizeMB  int // 单文件上限 MB，超过即 rotate，默认 100
	MaxBackups int // 保留旧文件数，默认 7
	MaxAgeDays int // 旧文件保留天数，默认 30
}

func (c Config) withDefaults() Config {
	if c.Level == "" {
		c.Level = "info"
	}
	if c.Format == "" {
		c.Format = "json"
	}
	if c.MaxSizeMB == 0 {
		c.MaxSizeMB = 100
	}
	if c.MaxBackups == 0 {
		c.MaxBackups = 7
	}
	if c.MaxAgeDays == 0 {
		c.MaxAgeDays = 30
	}
	return c
}

// fileCloser 持有文件 writer（lumberjack），供 [Close] 收尾；nil 表示无文件（只 stdout）。
// logging 是进程级单例（一次 Init），用包级变量收尾是 slog.SetDefault 同一全局语义。
var fileCloser io.Closer

// Init 装配 slog 句柄：stdout（+ 可选文件 rotate）+ 指定级别与格式，并 slog.SetDefault 全局生效。
// 返回的 *slog.Logger 供需要显式注入的组件（如 db.Config.Logger）。File 父目录不存在时自动创建。
func Init(cfg Config) (*slog.Logger, error) {
	cfg = cfg.withDefaults()

	level, err := parseLevel(cfg.Level)
	if err != nil {
		return nil, err
	}

	w := io.Writer(os.Stdout)
	if cfg.File != "" {
		if err := os.MkdirAll(filepath.Dir(cfg.File), 0o755); err != nil {
			return nil, fmt.Errorf("create log dir %s: %w", filepath.Dir(cfg.File), err)
		}
		lj := &lumberjack.Logger{
			Filename:   cfg.File,
			MaxSize:    cfg.MaxSizeMB,
			MaxBackups: cfg.MaxBackups,
			MaxAge:     cfg.MaxAgeDays,
			Compress:   true,
		}
		fileCloser = lj
		w = io.MultiWriter(os.Stdout, lj) // 同时落 stdout（docker logs 可见）+ 文件（持久化到 logs 卷）
	}

	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler
	if strings.EqualFold(cfg.Format, "text") {
		h = slog.NewTextHandler(w, opts)
	} else {
		h = slog.NewJSONHandler(w, opts) // 默认 json（生产可检索）
	}
	// trace 包装在最外层：全仓 slog.*Context 调用自动带请求级 trace_id（见 ctxhandler.go）。
	logger := slog.New(newTraceHandler(h))
	slog.SetDefault(logger)
	return logger, nil
}

// parseLevel 解析级别字符串；非法返回错误（启动期 fail-fast，不让错误级别上线）。
func parseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid log level %q (want debug/info/warn/error)", s)
	}
}

// Close 关闭日志文件 writer（组合根在 graceful shutdown 时调）；无文件则 no-op。
func Close() error {
	if fileCloser == nil {
		return nil
	}
	return fileCloser.Close()
}
