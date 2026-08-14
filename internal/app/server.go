// Package app 是 Hify 的组合根装配层（CLAUDE.md §组合根）。
//
// cmd/hify/main.go 仅加载配置 + 调用 [Run] + 处理退出码（~15 行）；
// 全部装配逻辑集中在本包 Run：初始化 platform → 自下游而上游组装业务模块 →
// 构建 gin 引擎 + 中间件 + 路由 → 启动 HTTP 服务。
// 从第一天起保持 main 精简；后续模块充实、装配增长只动本文件，不回流 main.go。
package app

import (
	"fmt"
	"log/slog"

	"github.com/gin-gonic/gin"

	authhandler "github.com/Karlsk/go-hify/internal/auth/handler"
	authsvc "github.com/Karlsk/go-hify/internal/auth/service"
	authstore "github.com/Karlsk/go-hify/internal/auth/store"
	demohandler "github.com/Karlsk/go-hify/internal/demo/handler"
	demosvc "github.com/Karlsk/go-hify/internal/demo/service"
	demostore "github.com/Karlsk/go-hify/internal/demo/store"
	"github.com/Karlsk/go-hify/internal/platform/config"
	"github.com/Karlsk/go-hify/internal/platform/db"
	"github.com/Karlsk/go-hify/internal/platform/logging"
	"github.com/Karlsk/go-hify/internal/platform/redisx"
	"github.com/Karlsk/go-hify/internal/platform/respond"
)

// Run 是组合根装配入口，返回非 nil error 表示启动失败（由 main.go 决定退出码）。
func Run(cfg *config.Config) error {
	// ── ② platform 初始化（CLAUDE.md §组合根步骤 2）─────────────────
	// logging 最先：全仓 slog.* 与 db/redis 的日志都依赖它；SetDefault 后 Recovery / gormLogger 统一走此句柄。
	logger, err := logging.Init(logging.Config{
		Level:  cfg.Logging.Level,
		Format: cfg.Logging.Format,
		File:   cfg.Logging.File,
	})
	if err != nil {
		return fmt.Errorf("init logging: %w", err)
	}
	defer logging.Close() // graceful shutdown 时关日志文件（r.Run 返回后触发）
	printBanner(cfg)      // 启动 banner 到 stdout（给人看）
	logStartup(cfg)       // 启动事件到 slog（给日志系统）

	gormDB, err := db.New(db.Config{DSN: cfg.PG.DSN, Logger: logger}) // 连接池等走 db 默认值（2C4G ~20/5）
	if err != nil {
		return fmt.Errorf("init db: %w", err)
	}
	rdb, err := redisx.New(redisx.Config{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	if err != nil {
		return fmt.Errorf("init redis: %w", err)
	}
	// TODO: platform/llm（bulkhead 16/供应商 + 熔断 + 三层超时 + 重试）
	// TODO: platform/budget（每用户限流 + 每日预算熔断，fail-open + 80% 告警）

	// ── ③ 业务模块装配（store → service → handler，自下游而上游）─────
	// auth 最先：其 handler 提供登录中间件，挂 v1 后其余模块路由受保护。
	authStore := authstore.New(gormDB)
	authSvc := authsvc.New(authStore, rdb)
	authH := authhandler.New(authSvc, cfg.Auth.CookieSecure)

	// demo：标准 CRUD 参照实现（任务 #6，表见 migrations/00008_demo_items.sql）——
	// 四层结构是真实业务模块的起稿模板，provider 等模块落地后可整体移除。
	demoStore := demostore.New(gormDB)
	demoSvc := demosvc.New(demoStore) // 返回 demoapi.DemoService

	// 其余模块当前为空壳，构造函数待业务实现后按下序填入：
	// 顺序：provider → mcp → agent → rag → workflow → chat（chat 最后，依赖图最外层、零被依赖）。
	//
	//   providerStore := providerstore.New(gormDB)
	//   providerSvc   := providersvc.New(providerStore)                  // 返回 providerapi.ProviderService
	//   mcpStore      := mcpstore.New(gormDB)
	//   mcpSvc        := mcpsvc.New(mcpStore)
	//   agentStore    := agentstore.New(gormDB)
	//   agentSvc      := agentsvc.New(agentStore, providerSvc, mcpSvc)   // 下游 api 接口注入
	//   ... rag / workflow / chat 同理，chat 最后 ...
	//   providerhandler.New(providerSvc).RegisterRoutes(v1)
	//   ...

	// ── ④ gin 引擎 + 中间件 + 路由（§组合根步骤 4）──────────────────
	r := gin.New()
	// 校验错误报 JSON 字段名而非 Go 字段名（填 respond/bind.go 既有 TODO）；失败非致命，回退 Go 名。
	if err := respond.RegisterFieldNames(); err != nil {
		slog.Warn("validator field-name registration failed (errors will report Go field names)", "err", err)
	}
	// Recovery 必须最外层：兜底其后所有中间件 / handler 的 panic（业务错误走返回值，不到这层）。
	r.Use(respond.Recovery())

	// /health 在 /api/v1 之外、不需鉴权（CLAUDE.md §路径与版本）。
	// 一期返回静态就绪信号；最终形态探 PG + Redis 连通性、degraded 返回 503（§部署架构 healthcheck）。
	r.GET("/health", health)

	v1 := r.Group("/api/v1")
	// 中间件必须先于路由注册挂载（gin 的 Use 只对之后注册的路由生效）；
	// login/register 由中间件内部白名单放行。业务 API 一期不按用户隔离，但仍要求登录门槛。
	v1.Use(authH.Middleware())
	authH.RegisterRoutes(v1)
	demohandler.New(demoSvc).RegisterRoutes(v1) // demo 参照实现（受登录中间件保护）
	// TODO: 各模块 handler.RegisterRoutes(v1)（provider / mcp / agent / rag / workflow / chat）

	// ── ⑤ 启动（§组合根步骤 5）──────────────────────────────────────
	slog.Info("hify ready", slog.String("addr", ":"+cfg.Server.Port), slog.String("version", version))
	// TODO: 接 signal 做 graceful shutdown（关 gormDB / rdb 连接池、在途 SSE 收尾）。
	return r.Run(":" + cfg.Server.Port)
}

// version 由构建期 -ldflags 注入（如 -X 'github.com/Karlsk/go-hify/internal/app.version=v1.0.0'），默认 dev。
var version = "dev"

// logStartup 记录启动 banner：版本 / 端口 / 日志级别 / 预算 / 各 provider 是否已配。
// 只记布尔「是否配置」而不记 Key 本身（CLAUDE.md §密钥 / Go 日志规范第 20 条：绝不记录密钥）。
func logStartup(cfg *config.Config) {
	slog.Info("hify starting",
		slog.String("version", version),
		slog.String("port", cfg.Server.Port),
		slog.String("log_level", cfg.Logging.Level),
		slog.Int64("daily_budget_usd_cents", cfg.Budget.DailyBudgetUSDCents),
		slog.Int("user_rpm", cfg.Budget.UserRPM),
		slog.Bool("openai_configured", cfg.LLM.OpenAIKey != ""),
		slog.Bool("claude_configured", cfg.LLM.ClaudeKey != ""),
		slog.Bool("gemini_configured", cfg.LLM.GeminiKey != ""),
		slog.Bool("ollama_configured", cfg.LLM.OllamaBaseURL != ""),
	)
}

// health 处理 GET /health：返回静态就绪信号，供部署探活与冒烟测试。
// TODO: 探 db + redis 连通性，任一不通返回 503（CLAUDE.md §部署架构 healthcheck）。
func health(c *gin.Context) {
	respond.OK(c, "Hify is running")
}
