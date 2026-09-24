# Implementation Plan: workflow 运行历史与节点轨迹查询

**Branch**: `015-workflow-run-history` | **Date**: 2026-09-24 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/015-workflow-run-history/spec.md`

## Summary

纯只读查询篇，把 spec 07/08 已落库的 workflow_runs / workflow_node_runs 两表暴露出来：后端 2 条 GET 路由（`GET /workflows/:id/runs` 游标分页列表 + `GET /workflows/:id/runs/:runId` 运行详情含节点轨迹），逐层照抄 chat 模块游标全链路先例（api 层 cursor 只是 string 不 import platform/page / service 层 DecodeCursor + 私有 keyset 键 / store 层行值比较 / handler 层 OKWithCursor）；唯一新哨兵 `RUN_NOT_FOUND`（404——run 不存在或不属于所查工作流同判，不泄露存在性）。前端 workflow 详情页增「运行历史」区块（抽独立子组件 WorkflowRunsPanel.vue，裁定见 research D6）：列表分页加载 + 点行开运行详情抽屉（run 级信息与大文本 + 节点轨迹表、失败节点按 error_node 高亮）。零迁移（表在 00019/00020）、执行引擎与落库零改动、既有 8 端点零变化。

## Technical Context

**Language/Version**: Go 1.26（后端）；Vue 3 + TypeScript + Element Plus（前端，spec 009/010/012/013 既有栈）

**Primary Dependencies**: Gin + GORM（pgx）；Element Plus。零新增第三方依赖。

**Storage**: PostgreSQL 17——只读查询 workflow_runs（idx_workflow_runs_wf_created = (workflow_id, created_at DESC)）与 workflow_node_runs（idx (run_id) + uq (run_id, seq)，00019 已建）。**零迁移**——动手前 `make migrate-status` 确认 20 条 applied、下一号 00021（本篇不新增）。

**Testing**: `go test ./... -race -count=1`（同包 *_test.go：api schema_test / service stub Store / store sqlmock / handler httptest，零真实 PG / Redis / 网络 / LLM）；前端 `cd web && npm run type-check && npm run build`（无单测基建，不引入）+ manual-test §11 人工验收。

**Target Platform**: Docker Compose 单机（后端单二进制 + nginx 托管前端产物）

**Project Type**: 模块化单体（workflow 模块四层 + web/ 前端）

**Performance Goals**: 列表页响应轻量（摘要列不含 input/output 大文本，SC-003——TOAST 不解压零成本）；keyset 游标任意深度 O(1)（禁 OFFSET）

**Constraints**: 只读（无任何写操作）；既有 8 workflow 端点、执行引擎、落库链路零变化（SC-005）；workflow 模块覆盖率 ≥80% 维持；前端设计 token 冻结合规

**Scale/Scope**: 后端 6 文件（4 层源 + 测试同文件追加）；前端 3 文件（1 新增组件）；文档 3 文件

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| 原则 | 判定 | 依据 |
|---|---|---|
| I. Simplicity First | ✅ PASS | 复用 platform/page 游标工具与 chat 全链路先例形态，零新抽象零新依赖；明确不做清单（实时进度/反查子 run/筛选聚合/写操作/全局历史页）已在 spec Assumptions 留痕 |
| II. 模块化单体与单向依赖 | ✅ PASS | 改动全在 workflow 模块四层 + web/，无跨模块 import、无组合根改动（service 构造签名不变，新路由在本模块 RegisterRoutes 内追加）；store 只读本模块两表 |
| III. platform 单点 | ✅ PASS | 游标分页复用 platform/page（不重写）；api 层不 import 带 gorm 的 platform/page（chat 先例：cursor 在 api 只是 string） |
| IV. SSE 链路 | ✅ PASS | 不涉（纯 REST 查询） |
| V. 数据库纪律 | ✅ PASS | 零迁移；keyset 游标默认（created_at,id 双键行值比较，走既有索引）；禁 SELECT *（显式列常量，摘要列不含大文本）；无软删；查询零事务 |
| VI. 可观测与成本护栏 | ✅ PASS | 不涉新 LLM 调用；错误经既有 respond 链路带 trace_id |
| VII. 统一契约 | ✅ PASS | 信封 respond.*；新码 RUN_NOT_FOUND 先有 api 哨兵再挂 handler 映射；JSON snake_case；bigint ID 字符串化（RunSummarySchema/RunDetailSchema `"id,string"`）；空列表 `[]` 非 null（make 兜底） |
| 前端 token 冻结 | ✅ PASS | 新区块全用既有 token 与既有 Element Plus 组件（el-drawer / el-table / el-tag） |

## Project Structure

### Documentation (this feature)

```text
specs/015-workflow-run-history/
├── plan.md              # 本文件
├── research.md          # Phase 0：七项设计决策
├── data-model.md        # Phase 1：两实体查询面映射（零新表零迁移）
├── quickstart.md        # Phase 1：验证场景
├── contracts/           # Phase 1：api.md（两端点契约）+ frontend.md（区块契约）
└── tasks.md             # Phase 2（/speckit-tasks 产出）
```

### Source Code (repository root)

```text
internal/workflow/
├── api/
│   ├── api.go               # WorkflowService 接口增 ListRuns / GetRun 两方法
│   ├── errors.go            # 新哨兵 ErrRunNotFound（RUN_NOT_FOUND）
│   ├── schema.go            # ListRunsReq / GetRunReq / RunSummarySchema / RunDetailSchema / NodeRunSchema
│   └── schema_test.go       # Req 归一与字段面
├── service/
│   ├── service.go           # Store 接口增 ListRuns / GetRunByID / ListNodeRuns + 实现两查询方法
│   │                        #   （runCursorKey 私有 keyset 键 + model→schema 转换 + gorm.ErrRecordNotFound → ErrRunNotFound）
│   └── service_test.go      # keyset 语义 / has_more 边界 / 404 翻译 / 摘要面与全字段转换
├── store/
│   ├── store.go             # selectRunSummary（摘要列，无 input/output）/ selectRun（全列）/ selectNodeRun 列常量
│   │                        #   + ListRuns（行值比较）/ GetRunByID（带 workflow_id 条件）/ ListNodeRuns（seq ASC）
│   └── store_test.go        # SQL 形态断言（sqlmock）
└── handler/
    ├── handler.go           # RegisterRoutes 增 2 GET 路由 + listRuns / getRun 绑定函数
    │                        #   （BindQuery + OKWithCursor 5 参 / OK；ErrRunNotFound → 404；篡改 cursor → 400）
    └── handler_test.go      # 两路由信封与 CursorMeta / limit 归一 / 404 / 400 / 空列表 [] / 既有 8 路由零回归

web/src/
├── api/workflow.ts                       # RunSummary / RunDetail / NodeRunTrack 类型 + listWorkflowRuns / getWorkflowRun
└── views/workflow/
    ├── WorkflowRunsPanel.vue             # 新增：运行历史区块（列表 + 翻页 + 运行详情抽屉 + 轨迹表）
    └── WorkflowDetail.vue                # 详情页卡片流挂「运行历史」区块（既有区块零触碰）

docs/
├── changelog/workflow/api_contract.md    # runs 查询两端点契约节（§9 追加）
└── testing/workflow-frontend-manual-test.md  # §11 增补小节

CLAUDE.md                                 # 《接口规范》资源清单 workflows 行补 runs 子路由；《错误处理》错误码表增 RUN_NOT_FOUND 行
```

**Structure Decision**: 单仓库既有结构——后端 workflow 四层各 1 文件追加 + 测试同包追加；前端 1 新增组件 + 2 既有文件追加；文档 3 文件 + CLAUDE.md 两处。无新目录、无组合根改动。

**注记（触碰文件的最小注释同步）**: `internal/workflow/store/store.go` 包头注释仍写「只操作本模块声明的三张表（workflows / workflow_nodes / workflow_edges）」——实际 spec 06/08 已扩至五张（+ workflow_runs / workflow_node_runs），本篇在该文件加 runs 读路径时一并把包头注释改正为五张表。纯注释与代码现状对齐，非契约改动。

## Complexity Tracking

> 无 Constitution Check 违例，无需登记。
