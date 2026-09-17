# Implementation Plan: Agent → Workflow 绑定（agent-workflow-binding）

**Branch**: `005-workflow-agent-binding` | **Date**: 2026-09-17 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/005-agent-workflow-binding/spec.md`
**权威契约**: 冻结契约以 [impl_spec_05_agent_binding.md](../../docs/changelog/workflow/impl_spec_05_agent_binding.md) §4 为准（本 plan 落位与策略，不重复抄契约）

## Summary

Agent 配置面新增可空的「绑定工作流」：`agents.workflow_id` 可空外键（`fk_agents_workflow`，ON DELETE RESTRICT）。写入经既有 `POST /agents` / `PUT /agents/{id}` 请求体字段 `workflow_id`（PUT 全量语义，缺省/null = 解绑）；存在性由 FK 23503 按约束名分发翻译（`fk_agents_workflow` → `agentapi.ErrWorkflowNotFound` 404，model 约束保持 `ErrModelNotFound`）；workflow 侧 Delete 撞 23503 → `workflowapi.ErrWorkflowInUse` 409 删除互锁。零新路由、零组合根改动、agent 模块零 workflow import（双向哨兵同码各持一份，KB 先例）。

## Technical Context

**Language/Version**: Go 1.26

**Primary Dependencies**: Gin（handler 层）、GORM + pgx 驱动（store 层）、`jackc/pgx/v5/pgconn`（FK 23503 / 唯一 23505 经 `*pgconn.PgError` 判定与分发；既有依赖，零新增）

**Storage**: PostgreSQL 17（`pgvector/pgvector:pg17`）；迁移走 goose SQL 只增不改，下一号 **00018**（动手前 `make migrate-status` 确认 17 条 applied）。禁 AutoMigrate。

**Testing**: `go test ./... -race -count=1`；同包 `*_test.go`，sqlmock（store）/ httptest（handler）/ stub（service 下游与 Store 接口）；零真实 PG / Redis / 网络 / LLM。

**Target Platform**: Linux server（Docker Compose 单机）；本篇纯后端，无前端改动。

**Project Type**: web-service（模块化单体，四层子包 api/service/store/handler）

**Performance Goals**: N/A（内部工具 3-5 QPS 量级；配置表极小，加列 + 单列索引零感知）

**Constraints**:
- 依赖红线：`internal/agent/` 全目录禁止 import `internal/workflow` 任何包（依赖清单只允许 workflow → agent）；workflow 侧同样无需 import agent（Delete 撞 23503 只按错误码翻译，不读对方类型）→ 本篇**双向零新增跨模块 import**
- `validateAgent` 签名与规则不变（拍板 C2 叠加语义，零新增跨字段规则）
- 哨兵文案 = `error.code`（前端直接消费），逐字对齐 impl spec 05 §4
- 迁移只增不改；既有 00001-00017 不动

**Scale/Scope**: 20-50 人内部使用；改动面 = agent 模块四层各一小块 + workflow 模块删除路径 + 1 个迁移 + 3 处文档。

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| # | 原则 | 判定 | 依据 |
|---|---|---|---|
| I | Simplicity First | ✅ 通过 | 加列 + 字段化，零新路由零新抽象零新依赖；复用既有 FK 翻译先例（agent 绑 KB）；被否决备选（SET NULL / 独立子资源端点 / 互斥校验）已在 impl spec 05 §3 记录 |
| II | 模块化单体与单向依赖 | ✅ 通过 | agent 不 import workflow（哨兵 `ErrWorkflowNotFound` 同码各持一份，KB 先例）；跨模块零调用（FK 是存在性唯一校验）；四层落位与仓规模板一致；service 无 gin 类型 |
| III | 统一 LLM 接入层 | ✅ 不涉及 | 本篇无 LLM 调用（执行引擎属后续 spec） |
| IV | SSE 流式链路 | ✅ 不涉及 | 纯 CRUD 路径，无流式接口 |
| V | 数据库纪律 | ✅ 通过 | goose 迁移只增不改（00018 Up/Down 成对）；FK 单独建索引 `idx_agents_workflow_id`；`COMMENT ON` 齐备；外键 RESTRICT 符合「引用关系一律 RESTRICT」；无 AutoMigrate；无 SELECT *（`selectAgent` 显式列清单加列） |
| VI | 可观测与成本护栏 | ✅ 不涉及 | 无 LLM 成本、无新观测面；错误路径经既有 respond 信封 + trace_id |
| VII | 统一契约与安全基线 | ✅ 通过 | 响应经 respond 信封；错误码 = 哨兵 `Error()`（`WORKFLOW_NOT_FOUND` / `WORKFLOW_IN_USE`）；JSON snake_case tag；可空 bigint 外键序列化为 `*string`（null/字符串两态，与 `fallback_model_id` 同款）；新增码先有 api 哨兵再挂 handler 映射 |

**门禁结论**: 全部通过，无违规条目 → Complexity Tracking 为空。

## Project Structure

### Documentation (this feature)

```text
specs/005-agent-workflow-binding/
├── plan.md              # This file (/speckit-plan command output)
├── research.md          # Phase 0 output (/speckit-plan command)
├── data-model.md        # Phase 1 output (/speckit-plan command)
├── quickstart.md        # Phase 1 output (/speckit-plan command)
├── contracts/           # Phase 1 output (/speckit-plan command)
└── tasks.md             # Phase 2 output (/speckit-tasks command - NOT created by /speckit-plan)
```

### Source Code (repository root)

```text
migrations/
└── 00018_agent_workflow_binding.sql      # 加列 + fk_agents_workflow RESTRICT + 索引 + COMMENT（Up/Down）

internal/agent/
├── api/
│   ├── schema.go        # CreateAgentReq/UpdateAgentReq + WorkflowID *uint64；AgentSchema + WorkflowID *string
│   └── errors.go        # + ErrWorkflowNotFound（同码各持一份，KB 先例注释）
├── service/
│   ├── model.go         # Agent + WorkflowID *uint64（RAGMinSimilarity 后）
│   └── service.go       # toModelCreate/applyUpdate/toSchema 转换；+ translateAgentFK 约束名分发，
│   │                    #   替换 Create/Update 事务内 CreateAgent/UpdateAgent 两处旧翻译
├── store/
│   └── store.go         # selectAgent 列清单 + workflow_id
└── handler/
    └── handler.go       # failAgent + ErrWorkflowNotFound → 404

internal/workflow/
├── api/
│   └── errors.go        # + ErrWorkflowInUse
├── service/
│   └── service.go       # + pgCodeFKViolation 常量 + isFKViolation helper；Delete err 分支 → ErrWorkflowInUse
└── handler/
    └── handler.go       # failWorkflow + ErrWorkflowInUse → 409

# 文档同步（T7）：CLAUDE.md（错误码表 + 索引地图）、docs/design/data-model.md、
#   docs/changelog/workflow/db_model.md（决策 #11）
# 组合根 internal/app/server.go：零改动（无新依赖注入）
# 前端 web/：零改动
```

**Structure Decision**: 模块化单体四层子包（仓规既定结构）；本篇改动 = agent 模块四层增量 + workflow 模块删除路径增量 + 单迁移文件，无新包无新目录。

## Complexity Tracking

> **Fill ONLY if Constitution Check has violations that must be justified**

无违规条目（Constitution Check 七项全过）。
