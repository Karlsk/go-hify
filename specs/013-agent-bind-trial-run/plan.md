# Implementation Plan: 前端 agent 绑定工作流与工作流试运行

**Branch**: `013-agent-bind-trial-run` | **Date**: 2026-09-23 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/013-agent-bind-trial-run/spec.md`

## Summary

补齐两处已交付后端契约的前端消费面，后端零改动：① agent 创建 / 编辑表单的「绑定工作流」下拉（仅 chat 型、含清空态，消费 spec 05 `workflow_id` 可空绑定与 PUT 全量解绑语义）；② 工作流试运行 UI（编辑页 + 详情页双入口共用一个对话框组件，消费 spec 06 `POST /workflows/{id}/execute?trial=true` 同步执行与 `RunResultSchema` 节点轨迹）。技术路径 = 在既有 `web/` 前端上加一个 API 客户端函数、一个新对话框组件、三处视图挂载点与两份 manual-test 增补，零新增第三方依赖、零后端 / 迁移 / 组合根改动。

## Technical Context

**Language/Version**: TypeScript（Vue 3 `<script setup>`，spec 009/010/012 既有前端栈）

**Primary Dependencies**: Vue 3 + Element Plus（main.ts 全局注册，el-select / el-dialog / ElMessageBox 直接用）+ axios（`web/src/utils/request.ts` 既有封装）+ `@vue-flow/core`（不触，仅共存）；**零新增第三方依赖**

**Storage**: N/A（纯消费 REST；后端 PostgreSQL 既有，本篇不触）

**Testing**: 前端无单测基建且明确不引入（软门禁约束沿用 009~012 先例）——门禁 = `cd web && npm run type-check && npm run build` 双全绿；人工验收 = `docs/testing/agent-manual-test.md` 与 `docs/testing/workflow-frontend-manual-test.md` 增补小节

**Target Platform**: 浏览器（Vue SPA，经 nginx 服务的静态构建产物）

**Project Type**: web 前端增量（模块化单体的 web/ 侧消费面）

**Performance Goals**: N/A（管理面低频操作；执行耗时由后端 LLM overall 5min 决定，前端仅 loading 呈现）

**Constraints**: 后端契约冻结逐字对齐（见 contracts/）；请求载荷形态不可自造；既有页面零回归（FR-008）

**Scale/Scope**: 6 个交付文件 + 2 份 manual-test 增补；约 +450 / -10 行量级

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| # | 原则 | 判定 | 说明 |
|---|------|------|------|
| I | Simplicity First | PASS | 下拉数据源复用既有 `getWorkflowList`（零新端点）；试运行对话框单组件双入口复用；无新依赖、无新抽象层。唯一复杂度 = task 型入参按 schema 组装 JSON——这是冻结契约（`{{input.x}}` 下钻）的必然要求，非过度设计 |
| II | 模块化单体与单向依赖 | PASS（不触面） | 后端零改动：无 Go import、无组合根、无迁移。前端 `api/agent.ts` 消费 workflow 列表仅经 HTTP 客户端，不引入模块间耦合 |
| III | 统一 LLM 接入层 | N/A | 不触 LLM 调用；试运行经既有 execute 端点，后端侧 LLM 纪律已由 spec 06 交付 |
| IV | SSE 流式链路完整性 | N/A（注意点已记录） | execute 是同步 REST 非 SSE，本篇不触 SSE 链路。派生约束：axios 实例默认 `timeout: 30_000` 与工作流执行最长 5min（nginx `proxy_read_timeout 300s` 同限）冲突 → execute 单请求覆盖 `timeout: 300_000`（见 research.md D1） |
| V | 数据库纪律 | N/A | 零迁移、零 SQL |
| VI | 可观测与成本护栏 | N/A | 不触日志 / 预算；试运行与真实流量区分（is_trial）是后端既有语义 |
| VII | 统一契约与安全基线 | PASS | 请求载荷逐字对齐冻结契约（`workflow_id` 请求体数值 / 响应字符串、execute body 单一 `input` 字段、query `trial=true`）；错误呈现 = respond 信封 `error.message` 经既有拦截器（el-alert 结果区同文案持久化），无裸异常文本；不触密钥面 |

**Post-Phase 1 复核**：contracts/ 三份契约成文后逐条对照第七原则（字段 snake_case、bigint ID 字符串化仅响应面、请求体数值转换）——无新增违例。**无 Complexity Tracking 登记项**（无宪法违例）。

## Project Structure

### Documentation (this feature)

```text
specs/013-agent-bind-trial-run/
├── plan.md              # This file (/speckit-plan command output)
├── research.md          # Phase 0 output (/speckit-plan command)
├── data-model.md        # Phase 1 output (/speckit-plan command)
├── quickstart.md        # Phase 1 output (/speckit-plan command)
├── contracts/           # Phase 1 output (/speckit-plan command)
│   ├── api-client.md    # executeWorkflow / workflow_id 载荷契约
│   ├── workflow-trial-dialog.md # 试运行对话框组件契约
│   └── agent-bind-dropdown.md   # 绑定下拉行为契约
└── tasks.md             # Phase 2 output (/speckit-tasks command - NOT created by /speckit-plan)
```

### Source Code (repository root)

```text
web/src/
├── api/
│   ├── agent.ts                      # [修改] AgentBase +workflow_id（回显）、AgentSaveData +workflow_id?: number
│   └── workflow.ts                   # [修改] +WorkflowRunResult/NodeRunSummary 类型、+executeWorkflow()
├── views/
│   ├── agent/
│   │   └── AgentList.vue             # [修改] 表单 +「绑定工作流」下拉（chat 型过滤 / 清空态 / 回显 / 提交）
│   └── workflow/
│       ├── WorkflowTrialDialog.vue   # [新增] 试运行对话框（三态入参表单 / 执行 / 结果与轨迹）
│       ├── WorkflowEdit.vue          # [修改] 工具栏 +「试运行」按钮（脏态先提示保存）
│       └── WorkflowDetail.vue        # [修改] PageHeader actions +「试运行」按钮
docs/testing/
├── agent-manual-test.md              # [修改] 增补绑定 / 回显 / 解绑人工用例小节
└── workflow-frontend-manual-test.md  # [修改] 增补试运行三态 / 结果 / 错误人工用例小节
```

**Structure Decision**: 单前端项目增量（web/），无后端 / 无新目录层级；对话框落位 `views/workflow/` 与同域组件（GraphModeEditor / NodeInspector / TemplateField）同层，遵循「按功能域组织」既有形态。
