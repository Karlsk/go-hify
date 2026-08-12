// Package app 是 Hify 的组合根装配层（CLAUDE.md §组合根）。
//
// cmd/hify/main.go 仅加载配置 + 调用 [Run] + 处理退出码（~15 行）；
// 全部装配逻辑集中在本包 Run：初始化 platform → 自下游而上游组装业务模块 →
// 构建 gin 引擎 + 中间件 + 路由 → 启动 HTTP 服务。
// 从第一天起保持 main 精简；后续模块充实、装配增长只动本文件，不回流 main.go。
package app

import (
	"fmt"

	"github.com/gin-gonic/gin"

	"github.com/Karlsk/go-hify/internal/platform/config"
	"github.com/Karlsk/go-hify/internal/platform/db"
	"github.com/Karlsk/go-hify/internal/platform/redisx"
	"github.com/Karlsk/go-hify/internal/platform/respond"
)

// Run 是组合根装配入口，返回非 nil error 表示启动失败（由 main.go 决定退出码）。
func Run(cfg *config.Config) error {
	// ── ② platform 初始化（CLAUDE.md §组合根步骤 2）─────────────────
	gormDB, err := db.New(db.Config{DSN: cfg.PG.DSN}) // 连接池等走 db 默认值（2C4G ~20/5）
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
	// TODO: platform/logging（结构化日志，slog.SetDefault 兜住 Recovery / gormLogger 占位）

	// ── ③ 业务模块装配（store → service → handler，自下游而上游）─────
	// 顺序：provider → mcp → agent → rag → workflow → chat（chat 最后，依赖图最外层、零被依赖）。
	// 各模块当前为空壳（任务六），构造函数待业务实现后按下序填入：
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
	//
	// gormDB / rdb 注入前暂无消费方（下方显式标注，过编译期未用检查）：
	_ = gormDB // → 各模块 store.New(gormDB)
	_ = rdb    // → 配置缓存 / 语义缓存 / 限流预算计数模块

	// ── ④ gin 引擎 + 中间件 + 路由（§组合根步骤 4）──────────────────
	r := gin.New()
	// Recovery 必须最外层：兜底其后所有中间件 / handler 的 panic（业务错误走返回值，不到这层）。
	r.Use(respond.Recovery())

	// /health 在 /api/v1 之外、不需鉴权（CLAUDE.md §路径与版本）。
	// 一期返回静态就绪信号；最终形态探 PG + Redis 连通性、degraded 返回 503（§部署架构 healthcheck）。
	r.GET("/health", health)

	v1 := r.Group("/api/v1")
	// TODO: auth 中间件挂 v1（除 /auth/login 外）—— auth 模块落地后
	// TODO: 各模块 handler.RegisterRoutes(v1)（provider / mcp / agent / rag / workflow / chat / auth）
	_ = v1 // 路由组已建、待 auth 中间件与各模块 RegisterRoutes 挂载（骨架阶段过未用检查）

	// ── ⑤ 启动（§组合根步骤 5）──────────────────────────────────────
	// TODO: 接 signal 做 graceful shutdown（关 gormDB / rdb 连接池、在途 SSE 收尾）。
	return r.Run(":" + cfg.Server.Port)
}

// health 处理 GET /health：返回静态就绪信号，供部署探活与冒烟测试。
// TODO: 探 db + redis 连通性，任一不通返回 503（CLAUDE.md §部署架构 healthcheck）。
func health(c *gin.Context) {
	respond.OK(c, "Hify is running")
}
