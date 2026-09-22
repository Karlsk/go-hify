# Tasks: 工作流管理前端（009-workflow-frontend）

**Input**: Design documents from `/specs/009-workflow-frontend/`（plan.md / spec.md / research.md / data-model.md / contracts/workflow-frontend-api.md / quickstart.md）

**Prerequisites**: plan.md（文件树与选型）、spec.md（FR-001~FR-014 + 三个 User Story）

**Tests**: 无测试任务——web/ 无前端单测基建且不引入测试框架（spec Assumptions）；**任务级 DoD = `cd web && npm run type-check && npm run build` 全绿**（每任务完成后跑一次），人工验收走 docs/testing/workflow-frontend-manual-test.md（T016 交付）。后端零改动（git 变更禁触 internal/ 与 migrations/）。

**Organization**: 任务按 User Story 分组（US1 列表管理 P1 / US2 JSON 创建 P2 / US3 拖拽编排 P3），可独立实现与验证；实现顺序 P1 → P2 → P3（US2 的提交跳转依赖 US1 路由，US3 依赖 US2 的图配置模型与创建页框架）。

## Format: `[ID] [P?] [Story] Description`

- **[P]**: 可并行（不同文件、无未完成依赖）
- **[Story]**: 所属 User Story
- 所有路径相对仓库根；视觉约束横切所有 UI 任务：业务代码只引 `--hf-*` 语义 token、禁硬编码色值（FR-012），空列表 `[]`、空串 `""`

---

## Phase 1: Setup（共享基础设施）

- [ ] T001 安装画布依赖：`cd web && npm install @vue-flow/core @vue-flow/background @vue-flow/controls`，核对 package.json 锁定版本与 plan.md Technical Context 一致（research #1）

---

## Phase 2: Foundational（阻塞前置）

- [ ] T002 创建 web/src/api/workflow.ts：类型（WorkflowItem / WorkflowStatus / WorkflowType / SchemaField / CreateWorkflowData）+ 5 个请求方法（getWorkflowList 偏移分页 / createWorkflow / deleteWorkflow / publishWorkflow / disableWorkflow），签名与风格对齐 contracts/workflow-frontend-api.md §5 与既有 web/src/api/agent.ts（复用 request.ts 的 PageParams/PageResult，不自造平行类型）

**Checkpoint**: api 层就绪，`npm run type-check` 绿

---

## Phase 3: User Story 1 - 列表页管理工作流（Priority: P1）🎯

**Goal**: /workflows 列表页完整闭环——表格（名称/类型/状态/创建时间）+ 删除 + 发布/停用 + 新建入口

**Independent Test**: 后端手测文档先造数据后，独立完成：三态展示、偏移分页、删除成功与 409、发布/停用轮转、跳转创建页（spec US1 六场景）

### Implementation for User Story 1

- [ ] T003 [US1] web/src/router/index.ts 新增 /workflows 路由（component: views/workflow/WorkflowList.vue，meta.title「工作流管理」，挂既有登录守卫）
- [ ] T004 [P] [US1] web/src/App.vue 侧边栏菜单新增「工作流管理」项（el-menu router 模式，default-active 既有机制自动高亮；位置放「Agent 管理」之后的业务区）
- [ ] T005 [US1] 创建 web/src/views/workflow/WorkflowList.vue：PageHeader（标题/描述/右上角「新建工作流」按钮 → router.push('/workflows/create')）+ HifyTable（api=getWorkflowList 偏移分页；列：名称、类型（chat/task 中文 tag）、状态（三态映射 draft→草稿/published→已发布/disabled→已停用，差异化 tag）、创建时间、操作）+ 操作列按钮矩阵（删除恒显、发布非 published 显、停用 published 显）（FR-001/FR-002/FR-003）
- [ ] T006 [US1] WorkflowList.vue 删除操作：useConfirm 二次确认（红色、提示不可恢复）→ deleteWorkflow(id) → 成功 notifySuccess + 表格 refresh()；409 WORKFLOW_IN_USE 由拦截器统一弹错、行保留不重复弹（FR-004）
- [ ] T007 [US1] WorkflowList.vue 发布/停用操作：确认框（停用文案提示「绑定该工作流的 Agent 会话将不可用」）→ publishWorkflow/disableWorkflow → 成功刷新表格、状态 tag 即时更新（后端幂等）（FR-014）

**Checkpoint**: US1 独立可用——`npm run type-check && npm run build` 绿 + 手测 spec US1 六场景（数据：后端手测文档 §4 造 2~3 条不同状态工作流）

---

## Phase 4: User Story 2 - JSON 模式创建工作流（Priority: P2）

**Goal**: /workflows/create 表单 + JSON 编辑器创建链路——预填示例、格式化、校验、提交跳回

**Independent Test**: 纯 JSON 模式走通「预填示例 → 改名称 → 提交 → 列表见新行」全链路，不碰画布（spec US2 六场景）

### Implementation for User Story 2

- [ ] T008 [P] [US2] 创建 web/src/views/workflow/graph.ts（纯逻辑、无 Vue 依赖）：GraphConfig/GraphNode/GraphEdge 类型 + 五类 NodeType 常量与中文名映射；预填示例常量（智能客服分类，取 docs/testing/workflow-manual-test.md §4 的图配置部分：llm→end、chat 型、不含 type/name/description）；节点 key 生成（`${type}_${n}` 计数器）；起始节点迁移规则（删起始 → 迁移剩余首个；清空 → 置 ''）；JSON 文本 ↔ GraphConfig 解析/序列化（结构校验：顶层对象、nodes/edges 数组、元素必需键）；提交组装（表单字段 + 图配置合并、config.model_id/config.workflow_id 数值化 Number()、chat 型不带 schema 键、未填字段省略）（FR-007/FR-008/FR-011 纯逻辑部分，research #8/#9/#10）
- [ ] T009 [P] [US2] 创建 web/src/views/workflow/JsonConfigEditor.vue：el-input textarea（等宽字体）+ 预填（父组件传入初始值）+ 「格式化」按钮（JSON.stringify(obj, null, 2) 美化；非法 JSON 错误提示含位置、原文不变）+ 对外暴露校验态（合法/非法 + 错误信息）供父组件提交与切模式前判定（FR-008）
- [ ] T010 [US2] 创建 web/src/views/workflow/WorkflowCreate.vue：表单（名称必填去空格、描述可选、类型单选 chat/task 默认 chat；task 型展开 input_schema/output_schema 编辑区——两个小 JSON 文本域，复用 JsonConfigEditor 的校验与格式化逻辑，可不填；chat 型不展示）+ 模式切换（el-radio-group「JSON / 拖拽」；非法 JSON 时阻断切拖拽并提示先修复）+ 返回按钮（回 /workflows）+ 提交（graph.ts 组装 → createWorkflow → 成功 notifySuccess + 跳回 /workflows；失败留在当前页编辑内容不丢）（FR-001 返回/FR-005/FR-006/FR-007/FR-011）
- [ ] T011 [US2] web/src/router/index.ts 新增 /workflows/create 路由（meta.title「新建工作流」）

**Checkpoint**: US1 + US2 均独立可用——门禁绿 + 手测 spec US2 六场景（含名称冲突 409 留页、非法 JSON 三处阻断）

---

## Phase 5: User Story 3 - 拖拽模式可视化编排（Priority: P3）

**Goal**: 画布编排完整形态——左面板拖入五类节点、连线、删除、右侧按类型配置，与 JSON 模式共享单一图配置数据源

**Independent Test**: 切拖拽模式 → 拖入 llm + end → 连线 → 配置 llm 节点 → 切回 JSON 核对等价 → 提交成功（spec US3 五场景 + SC-006 往返一致）

### Implementation for User Story 3

- [ ] T012 [P] [US3] graph.ts 画布扩展：GraphConfig ↔ Vue Flow 节点/边双向转换（key↔id、config 引用共享、edges label↔condition）+ 自动网格布局坐标（x=col*260 / y=row*120，按 nodes 序）+ 位置 Map 语义（同会话往返保留、不序列化进配置）（data-model §3，research #6/#7）
- [ ] T013 [US3] 创建 web/src/views/workflow/CanvasEditor.vue：Vue Flow 画布（core + background 网格 + controls 缩放；样式引入按 web/README.md 既有方式；默认节点 + `hf-wf-node-{type}` class 差异化着色与 `hf-wf-node--start` 起始标识，只引 --hf-* token）+ 左侧节点面板（五类，原生 dragstart 携带类型）+ 画布 drop（useVueFlow().screenToFlowCoordinate 落点换算，官方 DnD 模式）+ connect 连线 + 删除节点/连线 + 起始节点可视标识与切换（默认第一个放入节点）（FR-009，research #2）
- [ ] T014 [P] [US3] 创建 web/src/views/workflow/NodeInspector.vue：选中节点按类型分化表单——llm（模型下拉 + prompt 文本域；数据源走 AgentList 先例：getProviderList 遍历 + getModelList(p.id) 扁平化、capability=chat 过滤、空数据提示先去配模型）/ end（output）/ condition（expression）/ api（url + method 下拉）/ workflow（子工作流下拉——getWorkflowList 过滤 type==='task' + inputs 动态键值映射）；选中连线时切换为该边 condition 标签编辑；表单只改已知字段、未知 config 键不动（透传）（FR-010，research #4/#5）
- [ ] T015 [US3] WorkflowCreate.vue 接入 CanvasEditor：双模式共享单一 GraphConfig 数据源（切画布 = JSON 解析进配置模型 + 画布渲染；切回 JSON = 配置模型重新序列化更新编辑器；往返语义等价、未知键保留）；空画布起步（不预置节点）（FR-007 US3 侧 / SC-006 / Clarifications）

**Checkpoint**: 三 Story 全可用——门禁绿 + 手测 spec US3 五场景 + SC-006 往返一致（含手写 JSON 加未知键往返保留）

---

## Phase 6: Polish & Cross-Cutting（文档同步与全量验收）

- [ ] T016 [P] 创建 docs/testing/workflow-frontend-manual-test.md：覆盖 SC-004 全场景清单——两模式创建、三态展示、删除（含 409）、发布/停用轮转、409 双场景、非法 JSON 三处阻断、11 项 Edge Cases 走查步骤（前置数据准备含「被 Agent 绑定的工作流」）（FR-013④）
- [ ] T017 [P] web/README.md 目录结构补录：views/workflow/ 页与画布组件、api/workflow.ts、@vue-flow 依赖说明（FR-013③）
- [ ] T018 [P] CLAUDE.md《不做什么》修订「不做可视化工作流拖拽编排」条目（改为已交付 spec 009 双模式、用户 2026-09-21 批准）+ .specify/memory/constitution.md Principle I 同条目同步（版本 1.0.0 → 1.1.0 MINOR、Last Amended 更新、Sync Impact Report 注释按宪法治理格式）（FR-013①②）
- [ ] T019 全量验收门：`cd web && npm run type-check && npm run build` 绿 + `go build ./... && go vet ./...` 回归绿 + `git status --short` 无 internal/ 与 migrations/ 路径（SC-001/SC-002）+ 新增前端源码 grep 无硬编码色值（SC-003）+ 未登录访问 /workflows 跳登录（SC-005）；人工项（冒烟走查 SC-004/SC-006）移交用户并列入验收报告

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup（Phase 1）**: 无依赖，立即开始
- **Foundational（Phase 2）**: 依赖 Phase 1；阻塞全部 User Story（api 层是三个 Story 共同前置）
- **US1（Phase 3）**: 依赖 Phase 2
- **US2（Phase 4）**: 依赖 Phase 2 + US1 的 /workflows 路由（提交成功跳回目标）；建议按优先级顺序在 US1 后
- **US3（Phase 5）**: 依赖 US2（graph.ts 基础模型 + WorkflowCreate 表单/模式切换框架）
- **Polish（Phase 6）**: 依赖全部 Story 完成（T016 冒烟文档描述的是终态行为；T019 全量验收）

### Within Each User Story

- 纯逻辑（graph.ts）→ 子组件（编辑器/面板）→ 页面壳（组装与路由）→ 集成
- 每任务完成即跑 `cd web && npm run type-check && npm run build`（任务级 DoD）

### Parallel Opportunities

- T003 ∥ T004（router 与 App.vue 不同文件）
- T008 ∥ T009（graph.ts 与 JsonConfigEditor.vue 不同文件、互不依赖）
- T012 ∥ T014（画布转换与检查器面板不同文件；T014 依赖 T008 类型、T012 依赖 T008 模型，两者互不依赖）
- T016 ∥ T017 ∥ T018（三个文档任务不同文件）

---

## Implementation Strategy

### MVP First（US1 + US2）

1. Phase 1 + Phase 2 → api 层就绪
2. Phase 3（US1）→ 独立验证列表闭环
3. Phase 4（US2）→ **MVP 达成**（spec 定义 P1+P2 = 本篇最小可用交付：列表 + JSON 创建完整回环）
4. Phase 5（US3）→ 差异化能力（可视化编排）
5. Phase 6 → 文档同步 + 全量验收

单实现者按 P1 → P2 → P3 顺序串行推进；每个 Checkpoint 可停下独立验证。

---

## Notes

- 后端零改动红线：全部任务不触 internal/ 与 migrations/；错误提示一律由 request.ts 拦截器承担（页面不重复弹错）
- 视觉纪律（FR-012）横切 T005/T009/T010/T013/T014：只引 `--hf-*` token、列表空 `[]`、字符串空 `""`
- 提交时机由用户明示（全程不自动 commit）；建议可提交节点：MVP（US1+US2）一处、US3+文档同步一处
