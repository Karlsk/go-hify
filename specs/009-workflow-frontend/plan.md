# Implementation Plan: 工作流管理前端（009-workflow-frontend）

**Branch**: `009-workflow-frontend` | **Date**: 2026-09-22 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/009-workflow-frontend/spec.md`（含 2026-09-22 Clarifications：发布/停用纳入本期、JSON 编辑器只管图配置、condition 连线右侧编辑、空画布起步 + 类型列）。

## Summary

为已交付的 workflow 后端（spec 01~08，8 端点）补管理控制台两页：`/workflows` 列表（HifyTable：名称/类型/状态/创建时间 + 删除/发布/停用）与 `/workflows/create` 创建（表单 + **JSON 编辑器与 Vue Flow 拖拽画布双模式**，共享单一图配置数据模型，提交成功跳回列表）。纯前端交付：后端零改动、零迁移、零新哨兵。新第三方依赖 @vue-flow/* 三包（用户 2026-09-21 已批准）。

## Technical Context

**Language/Version**: TypeScript 5.9 + Vue 3.5（`<script setup>` + Composition API）

**Primary Dependencies**: 既有——Element Plus 2.14（全量）、Vue Router 5.2、Pinia 4.0、axios 1.19；新增——@vue-flow/core 1.48.2 + @vue-flow/background 1.3.2 + @vue-flow/controls 1.1.3（用户已批准；安装后以 package.json 锁定为准）

**Storage**: 无——消费后端 REST；画布节点位置仅内存（不序列化进配置）

**Testing**: web/ 无前端单测基建且不引入测试框架（引入属新第三方依赖须软门禁）；验证 = `cd web && npm run type-check && npm run build` 全绿 + 后端回归 `go build ./...` / `go vet ./...` + 人工冒烟 [docs/testing/workflow-frontend-manual-test.md](../../../docs/testing/workflow-frontend-manual-test.md)（SC-001~SC-006 逐项）

**Target Platform**: 浏览器（内部工具，桌面优先；窄屏由 HifyTable `hideBelow` 降级）

**Project Type**: web 前端（模块化单体的 web/ 端，Vue SPA）

**Performance Goals**: 20-50 人内部规模，无硬指标；画布预期节点数 < 30

**Constraints**: 业务代码只引 `--hf-*` 语义 token（已发布 token 只增不改）；后端 API 契约（spec 01~08）零改动零重定义；git 变更禁触 internal/ 与 migrations/

**Scale/Scope**: 2 页面 + 1 api 层 + 6 个新前端文件 + 路由/菜单 2 处小改 + 1 冒烟文档 + 3 处文档同步（FR-013）

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| 原则 | 判定 | 说明 |
|---|---|---|
| I Simplicity First | **偏离（已获用户批准）** | 「不做可视化工作流拖拽编排」经用户 2026-09-21 拍板修订为完整双模式；CLAUDE.md《不做什么》与宪法 Principle I 同步是 FR-013 交付任务（先 CLAUDE.md 后宪法，版本升 MINOR）。画布选久经考验的 Vue Flow 而非手写，符合「现成方案优先」。→ Complexity Tracking 登记 |
| II 模块化单体与单向依赖 | PASS | 纯 web/ 交付，后端 import 图零改动 |
| III platform/llm 单点 | PASS | 不涉 LLM 调用（配置 CRUD 而已） |
| IV SSE 流式链路 | PASS | 不涉 |
| V 数据库纪律 | PASS | 零迁移；偏移分页属 workflows 极小配置表既有例外 |
| VI 可观测与成本护栏 | PASS | 不涉 |
| VII 统一契约与安全基线 | PASS | 消费既有 respond 信封与错误码（拦截器统一弹）；ID 字符串 / 请求体外键数值化 / 空值 `[]`/`""` 约定遵循；登录门槛走既有路由守卫 |

## Project Structure

### Documentation (this feature)

```text
specs/009-workflow-frontend/
├── plan.md              # 本文件
├── research.md          # Phase 0：选型与模式决策
├── data-model.md        # Phase 1：前端数据模型与画布映射
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
│   └── workflow.ts                  # 新增：类型 + 5 个请求方法（getWorkflowList / createWorkflow /
│                                    #   deleteWorkflow / publishWorkflow / disableWorkflow）
├── views/
│   └── workflow/
│       ├── WorkflowList.vue         # 列表页：PageHeader + HifyTable + 删除/发布/停用
│       ├── WorkflowCreate.vue       # 创建页壳：表单（名称/描述/类型/task schema）+ 模式切换 + 提交
│       ├── JsonConfigEditor.vue     # JSON 编辑器：textarea + 预填 + 格式化 + 校验（schema 编辑复用）
│       ├── CanvasEditor.vue         # 画布编排：左节点面板（内联）+ Vue Flow 画布 + NodeInspector
│       ├── NodeInspector.vue        # 右侧面板：节点按类型分化表单 + 连线 condition 编辑
│       └── graph.ts                 # 纯逻辑（无 Vue）：图配置模型、JSON↔画布映射、key 生成、起始节点
├── router/index.ts                  # 改：+ /workflows、/workflows/create 两路由（meta.title）
└── App.vue                          # 改：侧边栏 + 「工作流管理」菜单项
docs/testing/workflow-frontend-manual-test.md   # 新增：前端冒烟文档（SC-004 场景清单）
CLAUDE.md / .specify/memory/constitution.md / web/README.md   # FR-013 文档同步（实现期任务）
```

**Structure Decision**: 画布组件落 `views/workflow/`（页面私有子组件，不进全局 components/——与 rag 模块页面私有形态同级；全局件 HifyTable/PageHeader/useConfirm 等复用不动）。`graph.ts` 收敛全部纯逻辑（解析/序列化/key/起始节点/自动布局），组件只管渲染与交互——可单测的最小面（即便本期无单测基建，逻辑集中仍降低维护成本）。

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| 可视化拖拽画布（宪法 I「明确不做」条目） | 用户 2026-09-21 拍板完整双模式（需求简图核心形态：左面板/中画布/右配置） | 仅 JSON 编辑器（用户明确否决——选择了完整双模式） |
| 新第三方依赖 @vue-flow/* 三包 | 画布拖入/连线/缩放/平移的成熟实现，Vue 3 生态事实标准 | 手写 SVG 画布（零依赖但拖拽+连线+视变换工作量与维护成本一人不可承受，违背「现成方案优先」） |
