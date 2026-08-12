package db

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// slogGormLogger 实现 gorm logger.Interface，把 GORM 日志桥接到 log/slog：
//   - Info/Warn/Error → 对应 slog 级别；
//   - Trace（每条 SQL 的回调）按「错误 / 慢查询 / 正常」分别落到 Error/Warn/Debug，
//     附 elapsed、sql、rows、err 属性，便于排障。
//
// 绝不直接打 stdout——slog 句柄由 platform/logging 配置（持久化到 logs 卷，CLAUDE.md《部署架构》logging）。
// GORM 的 fc() 有格式化开销，仅在确定要记日志的分支内才调用（惰性求值）。
type slogGormLogger struct {
	log           *slog.Logger
	level         logger.LogLevel
	slowThreshold time.Duration
}

// newGormLogger 按 Config 构造桥接 Logger；cfg.Logger 为 nil 时退回 slog.Default()。
func newGormLogger(cfg Config) logger.Interface {
	l := cfg.Logger
	if l == nil {
		l = slog.Default()
	}
	return &slogGormLogger{
		log:           l.With(slog.String("component", "gorm")),
		level:         cfg.LogLevel,
		slowThreshold: cfg.SlowThreshold,
	}
}

// LogMode 返回切换日志级别后的副本（GORM 内部会在事务等场景临时切级别）。
func (l *slogGormLogger) LogMode(level logger.LogLevel) logger.Interface {
	cp := *l
	cp.level = level
	return &cp
}

func (l *slogGormLogger) Info(ctx context.Context, msg string, args ...any) {
	if l.level >= logger.Info {
		l.log.InfoContext(ctx, fmt.Sprintf(msg, args...))
	}
}

func (l *slogGormLogger) Warn(ctx context.Context, msg string, args ...any) {
	if l.level >= logger.Warn {
		l.log.WarnContext(ctx, fmt.Sprintf(msg, args...))
	}
}

func (l *slogGormLogger) Error(ctx context.Context, msg string, args ...any) {
	if l.level >= logger.Error {
		l.log.ErrorContext(ctx, fmt.Sprintf(msg, args...))
	}
}

// Trace 是每条 SQL 执行后的回调：按 err / 耗时分级记日志。
// gorm.ErrRecordNotFound 不计错误（业务层翻译为哨兵 ErrXxxNotFound），降为 Debug。
func (l *slogGormLogger) Trace(ctx context.Context, begin time.Time, fc func() (sql string, rowsAffected int64), err error) {
	if l.level <= logger.Silent {
		return
	}
	elapsed := time.Since(begin)

	switch {
	case err != nil && !errors.Is(err, gorm.ErrRecordNotFound) && l.level >= logger.Error:
		sql, rows := fc()
		l.log.ErrorContext(ctx, "gorm query error",
			slog.String("sql", sql),
			slog.Int64("rows", rows),
			slog.Duration("elapsed", elapsed),
			slog.Any("err", err),
		)
	case l.slowThreshold > 0 && elapsed > l.slowThreshold && l.level >= logger.Warn:
		sql, rows := fc()
		l.log.WarnContext(ctx, "gorm slow query",
			slog.String("sql", sql),
			slog.Int64("rows", rows),
			slog.Duration("elapsed", elapsed),
			slog.Duration("threshold", l.slowThreshold),
		)
	case l.level >= logger.Info:
		sql, rows := fc()
		// 正常查询归 Debug（避免 Info 级刷屏）；要看全部 SQL 把 slog 级别调到 Debug。
		l.log.DebugContext(ctx, "gorm query",
			slog.String("sql", sql),
			slog.Int64("rows", rows),
			slog.Duration("elapsed", elapsed),
		)
	}
}
