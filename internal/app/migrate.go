// Package app 的迁移子命令：./hify migrate up|down|status（goose，表结构演进唯一通道，
// CLAUDE.md：禁止 GORM AutoMigrate）。不启动 HTTP 服务。
package app

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib" // pgx driver 注册（goose 走 database/sql）
	"github.com/pressly/goose/v3"

	"github.com/Karlsk/go-hify/internal/platform/config"
)

// RunMigrate 执行迁移子命令：up 应用全部未执行迁移；down 回滚最近一个；status 列出状态。
// 迁移文件从 ./migrations 读取（goose 单文件格式；容器内由 Dockerfile COPY 提供）。
func RunMigrate(args []string) error {
	cfg := config.MustLoad()

	db, err := sql.Open("pgx", cfg.PG.DSN)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return fmt.Errorf("ping db: %w", err)
	}

	provider, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS("migrations"))
	if err != nil {
		return fmt.Errorf("create goose provider: %w", err)
	}

	cmd := "up"
	if len(args) > 0 {
		cmd = args[0]
	}
	ctx := context.Background()
	switch cmd {
	case "up":
		res, err := provider.Up(ctx)
		if err != nil {
			return fmt.Errorf("migrate up: %w", err)
		}
		for _, r := range res {
			slog.Info("migration applied", slog.Int64("version", r.Source.Version), slog.String("path", r.Source.Path))
		}
		if len(res) == 0 {
			fmt.Println("no migrations to apply (already up to date)")
		}
	case "down":
		res, err := provider.Down(ctx)
		if err != nil {
			return fmt.Errorf("migrate down: %w", err)
		}
		if res != nil {
			slog.Info("migration rolled back", slog.Int64("version", res.Source.Version), slog.String("path", res.Source.Path))
		}
	case "status":
		status, err := provider.Status(ctx)
		if err != nil {
			return fmt.Errorf("migrate status: %w", err)
		}
		for _, s := range status {
			appliedAt := "-"
			if !s.AppliedAt.IsZero() {
				appliedAt = s.AppliedAt.Format("2006-01-02 15:04:05")
			}
			fmt.Printf("%-12d %-10s %s\n", s.Source.Version, s.State, appliedAt)
		}
	default:
		return fmt.Errorf("unknown migrate command %q (want up / down / status)", cmd)
	}
	return nil
}
