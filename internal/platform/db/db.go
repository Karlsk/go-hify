// Package db 是全仓库唯一的 GORM 装配点：初始化 *gorm.DB、配置连接池、
// 桥接结构化日志。组合根调用 [New] 后，把返回的 *gorm.DB 注入各模块 store.New(db)。
//
// 表结构演进一律走 migrations/ 下的 goose / golang-migrate SQL——本包既不暴露也不调用
// db.AutoMigrate（CLAUDE.md《事务与 DDL》：禁止 GORM AutoMigrate 做表结构演进）。
// 注意：GORM 没有「禁用 AutoMigrate」的配置开关，AutoMigrate 本身是显式方法调用、
// 从不会自动触发；保护靠架构（此处不提供该方法），而非某个 config flag。
//
// pgvector：pgvector.Vector 自带 sql.Scanner / driver.Valuer 实现，GORM 原生识别，
// 无需任何「注册」调用。RAG 模块的 chunks.embedding 直接用 pgvector.Vector 配
// `gorm:"type:vector(N)"` tag 即可（CLAUDE.md《pgvector 索引规范》）。
//
// 底层驱动为 pgx/v5（gorm.io/driver/postgres v1.6 通过 pgx/v5/stdlib），
// 故 DSN 须为 pgx 可解析的格式（URL 或 keyword=value），由 platform/config 产出。
package db

import (
	"fmt"
	"log/slog"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

// Config 是 GORM 装配参数。零值字段由 [Config.withDefaults] 补默认，组合根可只填 DSN。
type Config struct {
	DSN string // PG DSN（pgx 格式）；走 .env / Docker secrets，禁止入 Git（CLAUDE.md §密钥）

	// 连接池（CLAUDE.md §性能瓶颈 P1：「db 初始化设 SetMaxOpenConns ...」，2C4G ~20/5 量级）。
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	// ConnMaxIdleTime 补 ConnMaxLifetime：回收空闲连接，避免复用到已断开 / 变质的空闲连接。
	// database/sql 自 Go 1.15 提供，业界标配；默认短于 ConnMaxLifetime。
	ConnMaxIdleTime time.Duration

	// 结构化日志（见 db_logger.go）：GORM LogMode、慢查询阈值、slog 句柄。
	LogLevel      logger.LogLevel
	SlowThreshold time.Duration
	Logger        *slog.Logger // nil → slog.Default()；后续由 platform/logging 注入
}

// withDefaults 给零值字段补默认值，返回新副本（不改原 Config，符合不可变风格）。
func (c Config) withDefaults() Config {
	if c.MaxOpenConns == 0 {
		c.MaxOpenConns = 20
	}
	if c.MaxIdleConns == 0 {
		c.MaxIdleConns = 5
	}
	if c.ConnMaxLifetime == 0 {
		c.ConnMaxLifetime = time.Hour
	}
	if c.ConnMaxIdleTime == 0 {
		c.ConnMaxIdleTime = 15 * time.Minute
	}
	if c.LogLevel == 0 {
		c.LogLevel = logger.Warn // 仅记错误 + 慢查询，prod 友好
	}
	if c.SlowThreshold == 0 {
		c.SlowThreshold = 200 * time.Millisecond
	}
	return c
}

// New 初始化 *gorm.DB：开库（gorm.Open 默认会 ping，DB 不可达时此处即失败、fail-fast）、
// 配置连接池、装 slog 桥接 Logger。全仓库唯一的 GORM 装配点，之后不再 new 第二个 *gorm.DB。
func New(cfg Config) (*gorm.DB, error) {
	cfg = cfg.withDefaults()

	gormDB, err := gorm.Open(postgres.Open(cfg.DSN), &gorm.Config{
		// 显式声明默认：表名复数（GORM 的 SingularTable 零值即 false = 复数，本就如此，
		// 此处显式写出作文档）。最终表名以各 model 的 TableName() 为准——GORM 的英文复数化
		// 会把缩写词写歪（如 MCPServer → m_c_p_servers），故每个 model 都要显式 TableName()。
		NamingStrategy: schema.NamingStrategy{SingularTable: false},
		// GORM 日志桥接到 slog（见 db_logger.go）；禁止直接打到 stdout。
		Logger: newGormLogger(cfg),
	})
	if err != nil {
		return nil, fmt.Errorf("open gorm: %w", err)
	}

	sqlDB, err := gormDB.DB()
	if err != nil {
		return nil, fmt.Errorf("get underlying *sql.DB: %w", err)
	}
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	return gormDB, nil
}
