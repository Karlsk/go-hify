# Implementation Plan: 工作流详情 / 编辑前端（010-workflow-frontend-detail-edit）

**Branch**: `010-workflow-frontend-detail-edit` | **Date**: 2026-09-22 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/010-workflow-frontend-detail-edit/spec.md`（含 2026-09-22 Clarifications：创建第二步初始 = 预填智能客服分类示例图、脏态守卫双通道（路由 + 浏览器刷新/关闭）、详情 task 型 Schema 只读表 chat 型不显示、详情默认画布可切 JSON、并发编辑后保存者整图覆盖）。

## Summary

为 spec 009 交付的工作流前端补三块交互：① `/workflows/:id` 只读详情页（基础信息 + task 型 Schema 只读表 + 图编排双模式浏览，画布只读可平移缩放）；② `/workflows/:id/edit` 整页编辑页（GET 回填 + PUT 整图替换保存、type 禁改、脏态守卫双通道）；③ 创建流程重构两步式（第一步纯表单 → 第二步独立路由整页编排）+ 入参/出参 Schema 从 JSON 文本域改为结构化行表单。核心手段：把 009 落在 WorkflowCreate 里的双模式编排逻辑抽成共享组件 **GraphModeEditor**（创建第二步 / 编辑页 / 详情只读三处复用），CanvasEditor / JsonConfigEditor 加只读态。纯前端：后端零改动、零迁移、零新第三方依赖。

## Technical Context

**Language/Version**: TypeScript 5.9 + Vue 3.5（`<script setup>` + Composition API）

**Primary Dependencies**: 全部既有——Element Plus 2.14（全量）、Vue Router 5.2、Pinia 4.0、axios 1.19、@vue-flow/core ^1.48 + @vue-flow/background ^1.3 + @vue-flow/controls ^1.1（009 已引入）；**本篇零新增第三方依赖**

**Storage**: 无——消费后端 REST；创建草稿 = Pinia 内存 store（刷新即清零，「第二步直访/刷新回第一步」语义天然成立）；画布节点位置仍为会话级 Map（不序列化进配置）

**Testing**: web/ 无前端单测基建且不引入测试框架（引入属新第三方依赖，须软门禁）；验证 = `cd web && npm run type-check && npm run build` 全绿 + 后端回归 `go build ./...` / `go vet ./...` + 人工冒烟 [docs/testing/workflow-frontend-manual-test.md](../../../docs/testing/workflow-frontend-manual-test.md) 增补节（SC-001~SC-006 逐项）

**Target Platform**: 浏览器（内部工具，桌面优先；编辑/编排页按整页画布形态设计，窄屏降级不专门处理——画布交互本就桌面操作）

**Project Type**: web 前端（模块化单体的 web/ 端，Vue SPA）

**Performance Goals**: 20-50 人内部规模，无硬指标；编辑/详情页 GET 回填 ≤50 节点（后端数量界）即席渲染

**Constraints**: 业务代码只引 `--hf-*` 语义 token（已发布 token 只增不改）；后端 API 契约（workflow spec 01~08）零改动零重定义；git 变更禁触 `internal/` 与 `migrations/`；**PUT 请求体不得携带 type 键**（后端携带即拒，同值也拒）

**Scale/Scope**: 3 新页面（Detail / Edit / Orchestrate）+ 2 新组件（GraphModeEditor / SchemaFieldsEditor）+ 1 新 store + 4 个 009 文件改造（Create 重构 / Canvas / JsonConfig / graph.ts）+ api 层 2 方法 + 路由 3 条 + 列表操作列 2 入口 + 2 处文档同步（FR-017）

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| 原则 | 判定 | 说明 |
|---|---|---|
| I Simplicity First | PASS | 零新依赖；009 组件改造复用而非重写（FR-015）；GraphModeEditor 抽取是三页共用的正当 DRY（workflow 域页面私有子组件，不进全局 components/）；「明确不做」清单无新增偏离（拖拽双模式 009 已修宪交付） |
| II 模块化单体与单向依赖 | PASS | 纯 web/ 交付，后端 import 图零改动 |
| III platform/llm 单点 | PASS | 不涉 LLM 调用（配置 CRUD 消费） |
| IV SSE 流式链路 | PASS | 不涉 |
| V 数据库纪律 | PASS | 零迁移、零 SQL、零分页变更 |
| VI 可观测与成本护栏 | PASS | 不涉 |
| VII 统一契约与安全基线 | PASS | 消费既有 respond 信封与错误码（拦截器统一弹，页面不重复）；ID 字符串 / 节点 config 外键字符串保形 / 空值 `[]`/`""` 约定遵循；登录门槛走既有路由守卫 |

## Project Structure

### Documentation (this feature)

```text
specs/010-workflow-frontend-detail-edit/
├── plan.md              # 本文件（/speckit-plan 命令产出）
├── research.md          # Phase 0：选型与模式决策
├── data-model.md        # Phase 1：前端数据模型与转换语义
├── quickstart.md        # Phase 1：验证走查指南
├── contracts/
│   └── workflow-frontend-api.md   # 消费的 API 契约 + api/workflow.ts 导出面
├── checklists/
│   └── requirements.md  # /speckit-specify 质量清单
└── tasks.md             # /speckit-tasks 产出（本命令不创建）
```

### Source Code (repository root)

```text
web/src/
├── api/
│   └── workflow.ts                  # 改：+ WorkflowDetailNode / WorkflowDetailEdge / WorkflowDetail /
│                                    #   UpdateWorkflowData 类型 + getWorkflowDetail / updateWorkflow
│                                    #   两方法（PUT 不带 type）
├── stores/
│   └── workflowCreateDraft.ts       # 新：创建两步式草稿 store（step1 表单字段 + step2 图配置，
│                                    #   Pinia 内存态——刷新清零即「回第一步」语义）
├── views/
│   └── workflow/
│       ├── WorkflowList.vue         # 改：操作列 + 查看 / 编辑入口（平铺链接，见 research #6）
│       ├── WorkflowCreate.vue       # 改造：第一步纯表单（名称/描述/类型 + task 型
│       │                            #   SchemaFieldsEditor×2），画布/模式切换迁出；
│       │                            #   「创建工作流」= 校验 + 存草稿 → 跳第二步
│       ├── WorkflowOrchestrate.vue  # 新：创建第二步整页编排（fullBleed）：上一步（图回写草稿）/
│       │                            #   模式切换 / 保存并创建（一次 POST 成功跳列表 + 清草稿）
│       ├── WorkflowDetail.vue       # 新：只读详情：基础信息 + task 型 Schema 只读表 +
│       │                            #   GraphModeEditor(readonly, 默认画布) + 「编辑」按钮 +
│       │                            #   404/加载失败回列表
│       ├── WorkflowEdit.vue         # 新：整页编辑（fullBleed）：GET 回填（工具栏内联编辑
│       │                            #   名称/描述 + type 禁用单选 + task 型 Schema 抽屉）+
│       │                            #   PUT 保存成功回详情 + 脏态守卫双通道
│       ├── GraphModeEditor.vue      # 新：双模式编排共享组件（模式切换 radio + JsonConfigEditor +
│       │                            #   CanvasEditor + 非法 JSON 阻断切换 + getGraph() 出口；
│       │                            #   props: initial / defaultMode / readonly / fill）
│       ├── SchemaFieldsEditor.vue   # 新：Schema 四字段行表单（v-model SchemaField[]，
│       │                            #   增删行；非法历史 type 显示原值）
│       ├── JsonConfigEditor.vue     # 改：+ readonly prop（textarea 禁用 + 藏格式化按钮）
│       ├── CanvasEditor.vue         # 改：+ readonly prop（藏左面板/检查器、节点禁拖、禁连线、
│       │                            #   drop/双击起始短路；平移缩放保留）+ fill prop（高度铺满）
│       ├── NodeInspector.vue        # 不动（readonly 态下整块不渲染）
│       └── graph.ts                 # 改：+ detailToGraphConfig / schemaFieldsError /
│                                    #   buildUpdatePayload；parseSchemaFields 退役删除
│                                    #   （SchemaFieldsEditor 取代 JSON 文本路径）
├── router/index.ts                  # 改：+ 3 路由（/workflows/create/orchestrate、/workflows/:id、
│                                    #   /workflows/:id/edit；后两条自动过既有登录守卫）
docs/testing/workflow-frontend-manual-test.md   # 改：增补 spec 010 场景节（FR-017）
web/README.md                        # 改：目录结构与技术栈备注补录（FR-017）
```

**Structure Decision**: GraphModeEditor / SchemaFieldsEditor 落 `views/workflow/`（页面私有子组件——虽三页共用，但全部是 workflow 域页面私有形态，与 009 的 CanvasEditor 同级，不进全局 components/）。`stores/workflowCreateDraft.ts` 落 `stores/`（Pinia 惯例位置，与 auth.ts 同级——跨路由向导草稿是 store 的正当用途，非业务实体、无持久化）。整页高度布局复用既有 `meta.fullBleed`（App.vue `app__main--flush`：去 padding + 禁滚动，页面自管全高），编辑页与编排页挂此标记，详情页走常规滚动页。

## Complexity Tracking

> **Fill ONLY if Constitution Check has violations that must be justified**

无违规项——本篇零新依赖、零宪法偏离、零后端改动；全部交付物为 web/ 页面私有组件、既有组件改造与 api 层增量。
