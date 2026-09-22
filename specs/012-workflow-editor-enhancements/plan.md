# Implementation Plan: 工作流拖拽编辑器八项增强

**Branch**: `012-workflow-editor-enhancements` | **Date**: 2026-09-22 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/012-workflow-editor-enhancements/spec.md`

**Note**: This template is filled in by the `/speckit-plan` command. See `.specify/templates/plan-template.md` for the execution workflow.

## Summary

spec 009/010 交付的 JSON/拖拽双模式工作流编辑器存在八项可用性缺口（用户 2026-09-22 反馈）。本篇纯前端（web/，后端零改动）补齐：①检查器可见删除入口 ②伪「开始」节点承载 task 型入参/出参 schema 配置（拍板：前端伪节点）③节点 key 画布内改名级联 ④LLM 检查器 System Prompt + Prompt 双输入框（消费 spec 011 后端 system_prompt，commit 3c82c3f）⑤API 检查器 Headers KV + Auth 预设（拍板：纯前端序列化进 config.headers.Authorization）+ Body 模板域 ⑥子工作流按子流程 input_schema 自动渲染入参行 ⑦模板字段通用组件（文本域 + 变量引用下拉：input 下钻 / 上游祖先 key / 上游 workflow 节点 output_schema 下钻，光标处插入，手写自由）⑧全部模板字段统一接入。技术路径：复用既有 `useStrConfigField` 单源写入模式、`SchemaFieldsEditor` 行表单、`onNodesChange` remove 级联链与 `migrateStartKey` 语义；新增逻辑收敛 graph.ts 纯函数（key 级联、祖先计算）+ 新组件 TemplateField.vue；schema 状态单源保持在宿主页表单（编辑页抽屉 / 两步式第一步），伪节点面板经 props/emits 同源接线。

## Technical Context

**Language/Version**: TypeScript（web/ 既有 vue-tsc 校验）+ Vue 3 组合式 API

**Primary Dependencies**: Element Plus、@vue-flow/core、Vite（全部 spec 009/010 既有，**零新增**）

**Storage**: N/A（纯前端编辑器；数据经既有 workflow 8 端点读写后端，本篇不触后端与迁移）

**Testing**: 前端无单测基建且明确不引入测试框架（软门禁约束）。验证面 = `cd web && npm run type-check && npm run build` 双门禁 + `docs/testing/workflow-frontend-manual-test.md` 增补 spec 012 小节人工项

**Target Platform**: 浏览器（管理控制台，Vue SPA）

**Project Type**: web 前端（模块化单体的 web/ 目录，规范以 web/README.md 为准）

**Performance Goals**: 编辑器交互即时响应（变量下拉展开、光标插入、key 改名级联均无感知延迟）；子流程详情拉取有 loading 态且会话级缓存（同一子流程不重复请求）

**Constraints**: 零新增第三方依赖；后端零改动（无路由/字段/迁移）；提交载荷形态不变（图主体无伪节点、config 键集对齐 api_contract.md §3 既有键）；设计 token 只增不改（本篇零新 token，全部用 Element Plus 既有控件）

**Scale/Scope**: 6 user story / FR-001~FR-009；改动收敛 `web/src/views/workflow/`（6 个既有文件 + 1 新组件）+ `web/src/api/workflow.ts`（预期零改动）+ 1 份测试文档增补

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| 宪法原则 | 判定 | 说明 |
|---|---|---|
| I. Simplicity First | ✅ PASS | 伪 start 节点 = 前端伪节点非新数据模型（用户拍板）；变量下拉复用拓扑纯函数；零新依赖、零新抽象层，无 Complexity Tracking 项 |
| II. 模块化单体与单向依赖 | ✅ PASS（n/a） | 后端零改动，不触依赖图与跨模块规则 |
| III. 统一 LLM 接入层 | ✅ PASS（n/a） | 不触 LLM 调用链 |
| IV. SSE 流式链路完整性 | ✅ PASS（n/a） | 不触流式链路 |
| V. 数据库纪律 | ✅ PASS（n/a） | 零迁移、零 DDL |
| VI. 可观测与成本护栏 | ✅ PASS（n/a） | 不触 executions/预算/日志 |
| VII. 统一契约与安全基线 | ✅ PASS（含注记） | config 键集严格对齐 api_contract.md §3 既有键（system_prompt/headers/body/inputs），无新增键；API 消费走 web/src/utils 既有 request 封装。**注记**：Auth 预设的 token 经 config.headers 明文提交并存后端 jsonb——属 api 节点既有契约设计（内部工具、与 ssl_verify=false 同哲学，O6 拍板语境），本篇不改变该安全面，仅提供录入表单 |
| 技术与架构约束（前端条款） | ✅ PASS | 目录结构/设计 token 以 web/README.md 为准；本篇全部用既有视图目录与既有控件 |
| 开发工作流与质量门 | ✅ PASS（前端适配） | 前端无单测基建，质量门 = type-check + build 双绿（spec-dev 前端适配模式既定）；人工验收项入 docs/testing/ |

Phase 1 设计后复核：无新增违规（见 research.md 决策 1-8，全部为既有模式复用或纯函数收敛）。

## Project Structure

### Documentation (this feature)

```text
specs/012-workflow-editor-enhancements/
├── plan.md              # This file (/speckit-plan command output)
├── research.md          # Phase 0 output (/speckit-plan command)
├── data-model.md        # Phase 1 output (/speckit-plan command)
├── quickstart.md        # Phase 1 output (/speckit-plan command)
├── contracts/           # Phase 1 output (/speckit-plan command)
│   └── frontend-components.md
└── tasks.md             # Phase 2 output (/speckit-tasks command - NOT created by /speckit-plan)
```

### Source Code (repository root)

```text
web/src/
├── api/
│   └── workflow.ts                      # 预期零改动：仅消费既有 getWorkflowDetail 类型
└── views/workflow/
    ├── graph.ts                         # 纯逻辑层：renameNodeKey 级联纯函数、ancestorsOf 祖先计算、伪节点不变量
    ├── TemplateField.vue                # 【新】模板字段通用组件（文本域+变量下拉+光标插入）
    ├── CanvasEditor.vue                 # 伪「开始」节点渲染/点击、删除入口接线、renameNode 应用、readonly
    ├── NodeInspector.vue                # 删除/改名/LLM 双域/API 表单/子工作流 schema 渲染/组件接入
    ├── GraphModeEditor.vue              # schema props/emits 同源接线（宿主 ↔ 伪节点面板）
    ├── WorkflowEdit.vue                 # 编辑页宿主：抽屉 schema ↔ 伪节点面板同源
    └── WorkflowOrchestrate.vue          # 两步式宿主：第一步 schema ↔ 伪节点面板同源（store 回写核对）

docs/testing/
└── workflow-frontend-manual-test.md     # spec 012 增补小节（人工验收项）
```

**Structure Decision**: 复用 spec 009/010 既有 `web/src/views/workflow/` 视图目录与分层（graph.ts 纯逻辑 / 画布 / 检查器 / 双模式编排 / 两宿主），唯一新增文件是通用组件 TemplateField.vue（无图依赖、可被检查器各类型字段复用）；不建子目录、不引入状态管理新库（两步式沿用既有 store）。

## Complexity Tracking

> **Fill ONLY if Constitution Check has violations that must be justified**

无 violations——本篇零新增依赖、零新抽象层、零后端改动；伪节点与变量下拉均为既有模式的收敛复用，不登记。
