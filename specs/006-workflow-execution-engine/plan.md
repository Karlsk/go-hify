# Implementation Plan: Workflow 执行引擎（workflow-execution-engine）

**Branch**: `006-workflow-execution-engine` | **Date**: 2026-09-18 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/006-workflow-execution-engine/spec.md`
**权威契约**: 冻结契约以 [impl_spec_06_execution_engine.md](../../docs/changelog/workflow/impl_spec_06_execution_engine.md) §4（含 §4.2 代码骨架与 2026-09-18 executions 自记更正）、[api_contract.md](../../docs/changelog/workflow/api_contract.md) §5、[db_model.md](../../docs/changelog/workflow/db_model.md) §12 + §7 条 11 为准（本 plan 落位与策略，不重复抄契约）

## Summary

workflow 模块从纯配置管理升级为可运行：`WorkflowService` 扩 `Execute`（同步纯函数，ctx 进、`RunResultSchema` 出，不感知 SSE），`POST /workflows/{id}/execute`（正式仅 published 503 把关；`?trial=true` 放开 draft/disabled，状态机唯一例外）。service 层新建四文件——execcontext.go（扁平 string vars 池 + `{{var}}` strict 渲染 + condition 迷你表达式共用求值）、executor.go（runNode type switch 六类分发；callLLM 走 platform/llm 非流式 Generate 并**执行器自记 executions**〔2026-09-18 clarify 拍板，ConversationID=nil 语义既有〕）、execute.go（把关 / 快照加载 / 线性游走 / 收尾统一写轨迹含降级重试 / 5min WithTimeoutCause）、httpx.go（api 节点出站 client：net.Dialer.Control 建连时 SSRF 校验 + 重定向每跳复验 + timeout/TLS 映射）。轨迹两层表 migration 00019（workflow_runs + workflow_node_runs，DDL 冻结逐字）+ store.CreateRun 一事务两批多 VALUES + 保留期清理任务（WORKFLOW_RUNS_RETENTION_DAYS 默认 365）。保存期新增 R10 模板引用校验。新哨兵 1 个（WORKFLOW_EXECUTION_FAILED 500）、env knob 2 个、零新增第三方依赖。

## Technical Context

**Language/Version**: Go 1.26

**Primary Dependencies**: 全部既有，零新增——Gin（handler）、GORM + pgx（store）、`platform/llm` Manager（`Client(key, UpstreamOptions)` → `Client.Generate(ctx, msgs, opts)` 非流式，client.go:174）、provider `ModelService.ResolveLLMConfig`（api.go:44，明文 key 只在该调用链存在）、rag `Retrieve(ctx, RetrieveReq) ([]RetrievedChunk, error)`（api.go:69）、`platform/logging` ExecutionStore（executions 写入缝，chat 先例 recordExecution turn.go:478）；SSRF 用标准库 `net` / `net/http` / `crypto/tls`

**Storage**: PostgreSQL 17；迁移 goose SQL 只增不改，下一号 **00019**（已 `make migrate-status` 确认 18 条 applied）。禁 AutoMigrate。

**Testing**: `go test ./... -race -count=1`；同包 `*_test.go`，service stub Store + 下游 api（models / ragSvc / llmManager / executions 写入缝），store 用 sqlmock（regexp.QuoteMeta 全 SQL 钉住），handler 用 httptest + fakeSvc 信封断言；零真实 PG / Redis / 网络 / LLM。

**Target Platform**: Linux server（Docker Compose 单机）；本篇纯后端，无前端改动。

**Project Type**: web-service（模块化单体，四层子包 api/service/store/handler）

**Performance Goals**: N/A（内部工具 3-5 QPS 量级）；并发上限不靠新闸门——llm 节点受 platform/llm bulkhead 既有约束、api 节点受 timeout_sec 1-60s 约束；goroutine-per-request 无工作池。

**Constraints**:
- 依赖红线：`internal/workflow/` 全目录禁止 import `internal/chat`（引擎不感知 SSE，呈现归调用方）；跨模块只 import 下游 `api` 包（`providerapi` / `ragapi` 别名）
- `api/` 包保持纯契约：无 gin/gorm import；schema 的 binding 是纯 tag
- service 全程 `ctx context.Context`，无 gin 类型；executions 自记用窄接口（Go 小接口惯例，logging.ExecutionStore 结构化满足）
- 冻结契约逐字保真：类型名 / 方法签名 / SQL / 哨兵文案 = `error.code`；实现不得回改契约，偏差 → 软门禁
- 明文 API key 只活在 ResolveLLMConfig → llm.Manager 调用瞬间，禁入日志 / 轨迹 / 缓存 / 响应（crypto 四不）；slog 大段 prompt 与 PII 先截断（节点轨迹 1KB）
- 事务最小化：CreateRun 是唯一新事务（两批 INSERT，无外部调用）；节点执行不进事务
- 总时长 5min `WithTimeoutCause(ctx, 5*time.Minute, ErrWorkflowTimeout)`；异步清理任务脱钩请求 ctx 用 `context.WithoutCancel`（rag pipeline.go:32 先例），随优雅关停退出（appCtx）

**Scale/Scope**: 20-50 人内部使用；改动面 = workflow 四层（api 3 文件增 / service 5 新建（execcontext / executor / execute / httpx / runscleaner）+ 2 既有增 / store 1 增 / handler 1 增）+ migration 00019 + 组合根装配 + config 2 knob + 3 处文档同步。

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| # | 原则 | 判定 | 依据 |
|---|---|---|---|
| I | Simplicity First | ✅ 通过 | 密封接口 + type switch 而非 Registry（impl spec 06 §4.4 拍板）；零新增第三方；四文件均单职责小文件；被否决备选（Registry、RUNNING 态逐节点写库、独立试运行端点）已在该 spec §4/§5 记录 |
| II | 模块化单体与单向依赖 | ✅ 通过 | workflow → provider/rag/platform 均在依赖清单内；全目录零 chat import（红线 + 验收 grep）；跨模块经 api 接口 + api schema；handler 只认本模块 api；service 无 gin 类型 |
| III | 统一 LLM 接入层 | ✅ 通过 | llm 节点唯一入口 platform/llm Manager.Generate；重试/超时/熔断零新增（全在 llm 层）；executions 自记在调用方服务层（chat recordExecution 先例，2026-09-18 拍板更正注记） |
| IV | SSE 流式链路 | ✅ 不涉及 | execute 非流式 JSON 一次返回；引擎不感知 SSE；per-node 流式回调形态归后续 chat spec（本篇仅留 nil 零开销注入点） |
| V | 数据库纪律 | ✅ 通过 | 00019 goose 只增不改（Up/Down 成对）；DDL 逐字取 db_model §12 冻结稿（IDENTITY/timestamptz/text+CHECK/jsonb/COMMENT 齐备）；node_runs.run_id 是唯一 FK（CASCADE 真子表）且建 idx；弱引用零跨模块 FK 是拍板决策 #14；CreateRun 两批多 VALUES；无 SELECT *；BRIN 留待规模 |
| VI | 可观测与成本护栏 | ✅ 通过 | 两层轨迹表落库（失败路径也落）+ slog 节点轨迹截 1KB + trace_id 贯穿 + executions 自记（LLM 成本真实发生即有明细）；保留期 knob + 后台批量 DELETE（PartitionMaintainer 先例，appCtx 优雅关停） |
| VII | 统一契约与安全基线 | ✅ 通过 | 响应经 respond 信封；新码 WORKFLOW_EXECUTION_FAILED 先有 api 哨兵再挂 handler `errors.Is` → 500；错误二分法（图缺陷 400 带 node 前缀 / 环境限制 500）冻结；JSON snake_case + ID 字符串化 + 列表空 `[]`；SSRF 场景优先防护（恒禁 loopback/link-local/ULA，Dialer.Control 建连时校验防 rebinding，重定向每跳复验）；明文 key 四不 |

**门禁结论**: 全部通过，无违规条目 → Complexity Tracking 为空。

## Project Structure

### Documentation (this feature)

```text
specs/006-workflow-execution-engine/
├── plan.md              # This file (/speckit-plan command output)
├── research.md          # Phase 0 output（模式对齐扫描结论 + 关键实现决策）
├── data-model.md        # Phase 1 output（两张轨迹表，引用 db_model §12 冻结稿）
├── quickstart.md        # Phase 1 output（验证指南）
├── contracts/           # Phase 1 output（execute HTTP + 进程内契约）
├── checklists/
│   └── requirements.md  # /speckit-specify 质量清单（16/16 全过）
└── tasks.md             # Phase 2 output (/speckit-tasks command - NOT created by /speckit-plan)
```

### Source Code (repository root)

```text
migrations/
└── 00019_workflow_runs.sql           # 两张轨迹表（DDL 逐字取 db_model §12：CHECK/索引/COMMENT/Down）

internal/workflow/
├── api/
│   ├── api.go                        # WorkflowService 扩 Execute(ctx, ExecuteWorkflowReq) (*RunResultSchema, error)
│   ├── schema.go                     # + ExecuteWorkflowReq / RunResultSchema / NodeRunSummary
│   └── errors.go                     # + ErrWorkflowExecutionFailed（WORKFLOW_EXECUTION_FAILED）
├── service/
│   ├── execcontext.go                # 新建：vars 池 + render（strict）+ condition 求值 + steps 累积
│   ├── executor.go                   # 新建：runNode type switch + callLLM/evaluate/retrieve/callAPI/buildOutput
│   │                                 #   + callLLM 内 executions 自记（窄接口写入缝）
│   ├── execute.go                    # 新建：把关(trial) + 快照加载 + 游走循环 + finishResult(降级重试)
│   │                                 #   + 5min WithTimeoutCause + route + slog 轨迹
│   ├── httpx.go                      # 新建：api 节点出站 client（SSRF Dialer.Control / 重定向复验 /
│   │                                 #   timeout 1-60s / ssl_verify TLS / BLOCK_PRIVATE）
│   ├── runscleaner.go                # 新建：保留期后台批量 DELETE 任务（retention knob，appCtx 关停）
│   ├── service.go                    # 修改：+ R10 保存期模板引用校验（图校验条 11）；注入项扩容
│   └── model.go                      # 修改：+ WorkflowRun / WorkflowNodeRun（BaseAppendOnly）
├── store/
│   └── store.go                      # 修改：+ CreateRun（一事务 run 1 行 + node_runs N 行多 VALUES）
└── handler/
    └── handler.go                    # 修改：+ POST /workflows/:id/execute（?trial=true → req.Trial）

internal/platform/config/
└── config.go                         # 修改：+ WORKFLOW_API_BLOCK_PRIVATE / WORKFLOW_RUNS_RETENTION_DAYS

internal/app/
└── server.go                         # 修改：workflowsvc.New 注入 llmManager + executions 写入缝；
                                      #   清理任务 go 启动（appCtx）

# 文档同步：CLAUDE.md（错误码表 +WORKFLOW_EXECUTION_FAILED、索引地图 +两表行）、
#   docs/design/data-model.md（增两表与弱引用关系）、
#   docs/testing/workflow-manual-test.md（增执行测试小节）
# 前端 web/：零改动
```

**Structure Decision**: 模块化单体四层子包（仓规既定结构）；引擎四部件不建子包（impl spec 06 §4.2 冻结）；清理任务独立 runscleaner.go 对齐 PartitionMaintainer 形态；无新包无新目录层级。

## Complexity Tracking

> **Fill ONLY if Constitution Check has violations that must be justified**

无违规条目（Constitution Check 七项全过）。
