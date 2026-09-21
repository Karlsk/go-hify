# Implementation Plan: workflow 分型与子工作流嵌套（chat/task 两型 + sub-workflow 节点）

**Branch**: `008-workflow-typing-nesting` | **Date**: 2026-09-20 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `specs/008-workflow-typing-nesting/spec.md`

## Summary

workflow 模块引入 chat/task 两型（type 列不可变，存量回填 chat）与第七种节点类型 sub-workflow（config = workflow_id + inputs 模板映射），实现 task 型工作流的进程内递归嵌套执行：保存期 R11 五项嵌套校验（存在性 / task 型 / 环检测 / 链深 ≤3 / inputs 键集覆盖），执行期 executeChild 递归（trial 跟随、每层自包 5min、子池隔离、子 run 行 trigger_source='workflow' + parent_run_id 父收尾回填、conversation_id/message_id 透传），结构化 I/O（input_schema/output_schema 简化形态、JSON 文本入参、变量池一级下钻）。Execute 签名不动、哨兵零新增（四类复用）、组合根零改动；spec 07 管道语义零改动（分型不 gate 绑定）。

## Technical Context

**Language/Version**: Go 1.26（module `github.com/Karlsk/go-hify`）

**Primary Dependencies**: Gin（HTTP 层）、GORM + pgx（CRUD；唯一冲突 23505 / FK 23503 经 `*pgconn.PgError` 判定与分发）、goose（迁移）、cloudwego/eino（既有 LLM 接入，本篇零新增调用点）

**Storage**: PostgreSQL 17（`pgvector/pgvector:pg17`）；本篇迁移 **00020**（当前 19 条 applied，动手前 `make migrate-status` 复核）

**Testing**: `go test -race`；service stub Store、store sqlmock、handler httptest，零真实 PG / Redis / 网络 / LLM

**Target Platform**: Linux 单机 Docker Compose（hify 容器内跑）

**Project Type**: 模块化单体 web-service（本篇全部改动落 `internal/workflow/` + `migrations/` + 文档）

**Performance Goals**: 嵌套链深度上限 3（顶层 + 2 层）；每层 executeChild 自包 `WithTimeoutCause(5min)`；同步链墙钟仍受 nginx 300s 约束（既有边界，不做异步）

**Constraints**: workflow 依赖清单不变（→ mcp/rag/provider/agent/platform），**不得 import internal/chat**；api 包无 gin/gorm；迁移只增不改、禁 AutoMigrate；事务最小化（事务内禁外部调用——executeChild 的递归执行不在任何事务内）；禁 `SELECT *`、DML 带 WHERE

**Scale/Scope**: 20-50 人内部使用；并发互引竞态窗口接受（单管理员规模），执行期深度兜底

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| # | 原则 | 判定 | 依据 |
|---|---|---|---|
| I | Simplicity First | ✅ PASS | 嵌套执行复用既有 Execute 内部路径（executeChild 收窄入口，非新引擎）；哨兵零新增、组合根零改动、无新第三方依赖；Dify Chatflow/Workflow 分型的一人维护版收敛 |
| II | 模块化单体与单向依赖 | ✅ PASS | 全部改动在 workflow 模块四层内；R11 存在性走同模块 store 直查（无跨模块）；`internal/workflow/` 不 import `internal/chat`（grep 门禁）；service 无 gin 类型 |
| III | 统一 LLM 接入层 | ✅ PASS | sub-workflow 节点自身不调 LLM；子图内 llm 节点照旧经 platform/llm，无重试/超时/熔断逻辑外泄 |
| IV | SSE 流式链路完整性 | ✅ PASS | 本篇零 SSE 改动；spec 07 管道回归由既有测试保障（分型不 gate 绑定） |
| V | 数据库纪律 | ✅ PASS | 00020 goose SQL 只增不改（Up/Down）；type 用 text+CHECK 非 PG enum；CHECK 约束重建沿用 00017 命名模式；parent_run_id 回填为带 WHERE 的批量 UPDATE（append-only 一次窄写例外，登记 db_model.md 决策 #16） |
| VI | 可观测与成本护栏 | ✅ PASS | 子 run 轨迹独立落库（一次查询还原执行树）；run 写失败降级 trace_id 兜底（既有语义） |
| VII | 统一契约与安全基线 | ✅ PASS | 哨兵零新增（四类复用）；JSON snake_case、bigint ID 字符串化；错误码 = 哨兵 `Error()`；无新增密钥/认证面 |

## Project Structure

### Documentation (this feature)

```text
specs/008-workflow-typing-nesting/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/
│   └── api.md           # Phase 1 output（本篇 API 契约增量）
└── tasks.md             # Phase 2 output (/speckit-tasks)
```

### Source Code (repository root)

```text
internal/workflow/
├── api/
│   ├── schema.go        # NodeTypeWorkflow 常量 + WorkflowNodeConfig 密封解析；
│   │                    # UpsertReq / UpdateWorkflowReq 增 Type + InputSchema / OutputSchema；
│   │                    # SchemaField 形态 + 校验；Detail / Summary Schema 增字段
│   ├── api.go           # 接口零改动（8 方法签名不变）
│   └── errors.go        # 零改动（哨兵零新增）
├── service/
│   ├── model.go         # Workflow 增 Type / InputSchema / OutputSchema
│   ├── service.go       # toModel/toSchema、schema 形态校验、Update 拒改 type、
│   │                    # 保存期 R11（存在性 / task 型 / DFS 环 / 链深 / inputs 键集）
│   ├── executor.go      # runNode 增 workflow 分支（渲染 → JSON 组装 → executeChild → output 校验 → 落池）
│   ├── execute.go       # executeChild（把关 / 每层 5min / 子池 / 子 run / 深度计数）、
│   │                    # buildRun trigger_source 显式传参、parent_run_id 回填
│   └── execcontext.go   # 渲染器一级下钻（{{input.x}} / {{node.field}}）
├── store/
│   └── store.go         # type 列读写、UpdateParentRunIDs 批量回填
└── handler/
    └── handler.go       # 无新路由；type 绑定；Update 拒改映射（errs.ErrValidationFailed 400）

migrations/
└── 00020_workflow_typing_nesting.sql   # workflows 三列 + 两个 CHECK 约束重建 + parent_run_id

# 文档同步（非本 feature 目录）
docs/design/data-model.md                       # type + workflows 自引用弱引用
docs/changelog/workflow/db_model.md             # R11 条 12 + 决策 #16
docs/changelog/workflow/api_contract.md         # type 字段 + 节点类型清单 + 嵌套语义
docs/testing/workflow-engine-manual-test.md     # §7 嵌套冒烟小节
CLAUDE.md                                        # 错误码表零新行注记、索引地图
```

**Structure Decision**: 模块化单体既有四层结构，零新目录零新文件名——全部改动落在 workflow 模块既有文件的既有职责内；唯一新文件是迁移 00020。组合根 `internal/app/server.go` 零改动（executor 内部递归，无新注入）。

## Complexity Tracking

> Constitution Check 无违规项，本表为空。两处「看似复杂」的决策的简化理由：
>
> - **CHECK 约束重建（DROP + ADD）而非新增**：PG 无法 ALTER CHECK 加值，重建是唯一路径，且 00017 已验证同模式（约束名不变、Down 反向恢复）。
> - **parent_run_id 收尾回填而非子行创建时写入**：子执行时父 run id 尚未落库（父 run 行在父收尾 WithoutCancel 窗口写入）；一次窄 UPDATE 是「一次查询还原整树」语义下写路径最简实现——append-only 例外登记 db_model.md 决策 #16。
