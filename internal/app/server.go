// Package app 是 Hify 的组合根装配层（CLAUDE.md §组合根）。
//
// cmd/hify/main.go 仅加载配置 + 调用 [Run] + 处理退出码（~15 行）；
// 全部装配逻辑集中在本包 Run：初始化 platform → 自下游而上游组装业务模块 →
// 构建 gin 引擎 + 中间件 + 路由 → 启动 HTTP 服务。
// 从第一天起保持 main 精简；后续模块充实、装配增长只动本文件，不回流 main.go。
package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	agenthandler "github.com/Karlsk/go-hify/internal/agent/handler"
	agentsvc "github.com/Karlsk/go-hify/internal/agent/service"
	agentstore "github.com/Karlsk/go-hify/internal/agent/store"
	authhandler "github.com/Karlsk/go-hify/internal/auth/handler"
	authsvc "github.com/Karlsk/go-hify/internal/auth/service"
	authstore "github.com/Karlsk/go-hify/internal/auth/store"
	chathandler "github.com/Karlsk/go-hify/internal/chat/handler"
	chatsvc "github.com/Karlsk/go-hify/internal/chat/service"
	chatstore "github.com/Karlsk/go-hify/internal/chat/store"
	demohandler "github.com/Karlsk/go-hify/internal/demo/handler"
	demosvc "github.com/Karlsk/go-hify/internal/demo/service"
	demostore "github.com/Karlsk/go-hify/internal/demo/store"
	"github.com/Karlsk/go-hify/internal/platform/cache"
	"github.com/Karlsk/go-hify/internal/platform/config"
	"github.com/Karlsk/go-hify/internal/platform/db"
	"github.com/Karlsk/go-hify/internal/platform/httpmw"
	"github.com/Karlsk/go-hify/internal/platform/llm"
	"github.com/Karlsk/go-hify/internal/platform/logging"
	"github.com/Karlsk/go-hify/internal/platform/redisx"
	"github.com/Karlsk/go-hify/internal/platform/respond"
	providerhandler "github.com/Karlsk/go-hify/internal/provider/handler"
	providersvc "github.com/Karlsk/go-hify/internal/provider/service"
	providerstore "github.com/Karlsk/go-hify/internal/provider/store"
	raghandler "github.com/Karlsk/go-hify/internal/rag/handler"
	ragsvc "github.com/Karlsk/go-hify/internal/rag/service"
	ragstore "github.com/Karlsk/go-hify/internal/rag/store"
	workflowhandler "github.com/Karlsk/go-hify/internal/workflow/handler"
	workflowsvc "github.com/Karlsk/go-hify/internal/workflow/service"
	workflowstore "github.com/Karlsk/go-hify/internal/workflow/store"
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
	// llm：共享 Transport + 流式 Client 壳注入 eino adapter（定制 transport 见 platform/llm/httpx.go）；
	// Manager 按 provider 惰性创建受保护 Client（bulkhead → 熔断 → 重试 → 三层超时），双层缓存：
	// gate（槽位+熔断）按 provider 共享，Client 按 (provider, model) 独立上游实例。
	llmTransport := llm.NewSharedTransport()
	llmManager := llm.NewManager(llm.NewUpstreamFactory(llm.NewStreamClient(llmTransport)))
	// provider 的探测 / 模型同步走直连轻量 GET（NewJSONClient + 共享 transport），不经 Manager
	// ——元数据请求不产生 token 消费，不占 bulkhead / 熔断；rag 的 embedding 走独立
	// NewEmbedder（4 槽 + 5s 超时，embed.go），与 chat 流量同 Transport 不同闸（装配见 rag 块）。
	// TODO: platform/budget（每用户限流 + 每日预算熔断，fail-open + 80% 告警）

	// appCtx：随 SIGINT / SIGTERM 取消，喂给长生命周期 goroutine（provider 定时探测、executions 分区维护）与关停流程。
	appCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ── ③ 业务模块装配（store → service → handler，自下游而上游）─────
	// auth 最先：其 handler 提供登录中间件，挂 v1 后其余模块路由受保护。
	authStore := authstore.New(gormDB)
	authSvc := authsvc.New(authStore, rdb)
	authH := authhandler.New(authSvc, cfg.Auth.CookieSecure)

	// demo：标准 CRUD 参照实现（任务 #6，表见 migrations/00008_demo_items.sql）——
	// 四层结构是真实业务模块的起稿模板，provider 等模块落地后可整体移除。
	demoStore := demostore.New(gormDB)
	demoSvc := demosvc.New(demoStore) // 返回 demoapi.DemoService

	// provider：双 service 共享一个 store 与 cache（wiring_sync_spec.md §2.1）。
	// NewProviderService 返回 (api, Prober)：业务面注入 handler / 上游模块，Prober 由组合根起 goroutine。
	providerStore := providerstore.New(gormDB)
	providerCache := cache.New(rdb, cache.DefaultConfig()) // provider-cache：TTL 30min + 写时删 key
	providerSvc, prober := providersvc.NewProviderService(providerStore, providerCache, cfg.Provider.MasterKey)
	modelSvc := providersvc.NewModelService(providerStore, providerCache, cfg.Provider.MasterKey)
	go prober.StartProber(appCtx) // 定时健康探测：60s 一轮，随 appCtx 取消退出（db_model §2.3.1）

	// executions 分区维护后台任务（platform/logging/partition.go）：启动首轮 + 每 24h 一轮，
	// 建当月/下月分区、删 分区end+保留期 早于 now 的旧分区（在线窗口恒 ≥ 保留期）。
	// dev 与 prod 同路径生效；单实例部署假设不加锁；首轮毫秒级完成、而 executions 写入发生在
	// LLM 流结束后（秒级），监听竞态窗口实际不可达。<=0 关闭（不建不删，仅此处 WARN）。
	if cfg.Logging.ExecutionsRetentionDays > 0 {
		go logging.NewPartitionMaintainer(gormDB, cfg.Logging.ExecutionsRetentionDays).Start(appCtx)
	} else {
		slog.Warn("executions partition maintenance disabled (EXECUTIONS_RETENTION_DAYS <= 0); partitions are neither created nor dropped")
	}

	// agent：依赖 provider 的 ModelService（主/备用模型存在性预检，api 接口注入）。
	// cache 用独立实例（NameAgent 命名空间隔离）；失效矩阵只有 detail:{id} 一条边——
	// 列表是低频管理页查询，不进缓存（agent/service/service.go cacheKeyDetail 注释）。
	agentStore := agentstore.New(gormDB)
	agentCache := cache.New(rdb, cache.DefaultConfig()) // agent-cache：TTL 30min + 写时删 key
	agentSvc := agentsvc.New(agentStore, modelSvc, agentCache)

	// rag：知识库 + 入库管线 + 检索（spec 08 装配，依赖仅 provider + platform）。
	// 建库/管线用 provider 的 ModelService（嵌入模型预检 + ResolveLLMConfig）；
	// embedding 走 llm.Embedder（共享 Transport、独立 4 槽，不挤占 chat bulkhead）；
	// cache 独立实例（NameRag 命名空间）。New 双返回（provider Prober 先例）：
	// Recovery 启动时扫残留 pending/processing → 置 failed「服务重启中断」（spec 07 §4），
	// 尽力而为——失败仅 WARN 不阻断启动。
	ragStore := ragstore.New(gormDB)
	ragCache := cache.New(rdb, cache.DefaultConfig()) // rag-cache：TTL 30min + 写时删 key
	embedder := llm.NewEmbedder(llmTransport)         // 复用共享 Transport（keep-alive/TLS 复用）
	ragCfg := ragsvc.Config{
		TopK:              cfg.Rag.TopK,
		EFSearch:          cfg.Rag.EFSearch,
		ChunkSize:         cfg.Rag.ChunkSize,
		ChunkOverlap:      cfg.Rag.ChunkOverlap,
		EmbedBatchSize:    cfg.Rag.EmbedBatchSize,
		IngestConcurrency: cfg.Rag.IngestConcurrency,
	}
	ragSvc, ragRecovery := ragsvc.New(ragStore, modelSvc, embedder, ragCache, ragCfg)
	if err := ragRecovery.MarkInterruptedFailed(appCtx); err != nil {
		slog.Warn("rag recovery: mark interrupted documents failed", "err", err)
	}

	// workflow：CRUD + 状态动作 + 执行引擎（spec 06）。依赖既有实例零新建：modelSvc /
	// ragSvc（条 9 预检 + 执行期 ResolveLLMConfig / Retrieve）+ ragCache（Cache 无状态、
	// 按 NameWorkflow 命名空间隔离，spec 04 §5）+ llmManager / execStore（llm 节点走
	// platform/llm 非流式调用并自记 executions，O2）+ WORKFLOW_API_BLOCK_PRIVATE（O6）。
	execStore := logging.NewExecutionStore(gormDB) // executions 表写入（每次 LLM 调用一行）
	workflowStore := workflowstore.New(gormDB)
	workflowSvc := workflowsvc.New(workflowStore, modelSvc, ragSvc, ragCache,
		llmManager, execStore, ragSvc, cfg.Workflow.APIBlockPrivate)
	// workflow_runs 保留期清理（FR8）：WORKFLOW_RUNS_RETENTION_DAYS（默认 365）按
	// created_at 批次 DELETE（node_runs 随 FK CASCADE 连带删）；<=0 由 Start 内 WARN
	// 关闭。同 PartitionMaintainer 形态：启动首轮 + 每 24h 一轮，随 appCtx 退出。
	go workflowsvc.NewRunsCleaner(workflowStore, cfg.Workflow.RunsRetentionDays).Start(appCtx)

	// chat：对话引擎（依赖图最外层，零被依赖——将来可整体拆成独立服务）。
	// 依赖方向：chat → agent（agentGetter）→ provider（llmConfigResolver）→ platform/llm（llmClientFactory）
	//           chat → rag（ragRetriever，KB 检索注入）→ platform/logging（executionWriter）
	//           chat → workflow（workflowExecutor，管道触发——绑定 agent 的消息先过工作流，spec 07）。
	// v1 范围：会话 CRUD + 上下文组装（含 RAG 检索注入）+ SSE 两模式 + executions 落库；ToolIDs 读到不执行。
	chatStore := chatstore.New(gormDB)
	chatSvc := chatsvc.New(chatStore, agentSvc, modelSvc, llmManager, execStore, ragSvc, workflowSvc)

	// mcp 后续批次再接入。

	// ── ④ gin 引擎 + 中间件 + 路由（§组合根步骤 4）──────────────────
	r := gin.New()
	// 校验错误报 JSON 字段名而非 Go 字段名（填 respond/bind.go 既有 TODO）；失败非致命，回退 Go 名。
	if err := respond.RegisterFieldNames(); err != nil {
		slog.Warn("validator field-name registration failed (errors will report Go field names)", "err", err)
	}
	// Recovery 必须最外层：兜底其后所有中间件 / handler 的 panic（业务错误走返回值，不到这层）。
	r.Use(respond.Recovery())
	// RequestID 紧随 Recovery：trace_id 注入 ctx + X-Request-ID 响应头（panic 路径同样可对账日志）；
	// AccessLog 访问日志（method/path/status/耗时，慢请求 WARN、SSE 豁免），/health 被探活不记。
	r.Use(httpmw.RequestID())
	r.Use(httpmw.AccessLog(httpmw.AccessLogConfig{SkipPaths: []string{"/health"}}))

	// /health 在 /api/v1 之外、不需鉴权（CLAUDE.md §路径与版本）。
	// 一期返回静态就绪信号；最终形态探 PG + Redis 连通性、degraded 返回 503（§部署架构 healthcheck）。
	r.GET("/health", health)

	v1 := r.Group("/api/v1")
	// 中间件必须先于路由注册挂载（gin 的 Use 只对之后注册的路由生效）；
	// login/register 由中间件内部白名单放行。业务 API 一期不按用户隔离，但仍要求登录门槛。
	v1.Use(authH.Middleware())
	authH.RegisterRoutes(v1)
	demohandler.New(demoSvc).RegisterRoutes(v1)                            // demo 参照实现（受登录中间件保护）
	providerhandler.New(providerSvc, modelSvc).RegisterRoutes(v1)          // provider：providers + models 双组 12 端点
	agenthandler.New(agentSvc).RegisterRoutes(v1)                          // agent：agents 一组 5 端点
	raghandler.New(ragSvc, int(cfg.Rag.MaxUploadBytes)).RegisterRoutes(v1) // rag：KB + documents 两组 11 端点
	workflowhandler.New(workflowSvc).RegisterRoutes(v1)                    // workflow：workflows 一组 7 端点
	chathandler.New(chatSvc).RegisterRoutes(v1)                            // chat：conversations + messages 5 端点
	// TODO: 其余模块 handler.RegisterRoutes(v1)（mcp）

	// ── ⑤ 启动（§组合根步骤 5）──────────────────────────────────────
	slog.Info("hify ready", slog.String("addr", ":"+cfg.Server.Port), slog.String("version", version))
	// graceful shutdown（wiring_sync_spec.md §2.2）：SIGINT/SIGTERM → http.Server.Shutdown（5s
	// 等在途请求收尾）→ 关 db / redis 连接池 → defer 链关日志。ErrServerCanceled 不上抛。
	srv := &http.Server{Addr: ":" + cfg.Server.Port, Handler: r, ReadHeaderTimeout: 10 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case err := <-errCh: // 启动即失败（端口占用等），未进入服务态
		return fmt.Errorf("http server: %w", err)
	case <-appCtx.Done():
		slog.Info("hify shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil { // 在途请求超 5s 强制返回
			slog.Warn("http shutdown incomplete", "err", err)
		}
	}
	// prober / 分区维护 goroutine 随 appCtx 退出（在途单轮有界，极端未收尾只记一条错误日志，不阻断关停）。
	if sqlDB, err := gormDB.DB(); err != nil {
		slog.Warn("get sql db handle", "err", err)
	} else if err := sqlDB.Close(); err != nil {
		slog.Warn("close db", "err", err)
	}
	if err := rdb.Close(); err != nil {
		slog.Warn("close redis", "err", err)
	}
	return nil
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
