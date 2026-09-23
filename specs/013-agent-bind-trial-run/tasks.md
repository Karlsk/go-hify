# Tasks: 前端 agent 绑定工作流与工作流试运行

**Input**: Design documents from `/specs/013-agent-bind-trial-run/`

**Prerequisites**: plan.md (required), spec.md (required for user stories), research.md, data-model.md, contracts/

**Tests**: 前端无单测基建且明确不引入（软门禁约束，009~012 先例）——本篇无测试任务；验证面 = 双门禁（`cd web && npm run type-check && npm run build`）+ docs/testing/ 两份 manual-test 增补（人工验收项）。

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3)
- Include exact file paths in descriptions

## Path Conventions

- 纯前端篇：全部路径在 `web/src/` 与 `docs/testing/` 下；后端零改动（无 internal/、无迁移、无组合根）

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: 干净基线确认（零项目初始化——既有前端工程、零新增依赖）

- [x] T001 基线门禁确认：`cd web && npm run type-check && npm run build` 全绿后再开工（不绿停下报告，不在脏基线上动工）

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: US2 / US3 共用的执行 API 客户端（US1 的 agent.ts 类型增量只服务 US1，放 US1 相位）

- [x] T002 [P] web/src/api/workflow.ts：新增 `NodeRunSummary`（node_key / node_type / status / duration_ms / error_msg——json 键逐字对齐后端 `NodeRunSummary`，不自造）与 `WorkflowRunResult`（run_id / status / output / duration_ms / node_trace）类型；新增 `executeWorkflow(id, input)`：`POST /workflows/{id}/execute?trial=true`、body 单一 `{ input }` 键、`{ timeout: 300_000 }` 覆盖 axios 默认 30s（contracts/api-client.md §1 逐字对齐；research D1）

**Checkpoint**: 执行客户端就绪，US2 / US3 可开工

---

## Phase 3: User Story 1 - 管理员为 agent 绑定 / 解绑对话型工作流 (Priority: P1) 🎯 MVP

**Goal**: agent 创建 / 编辑表单出现「绑定工作流」下拉（仅 chat 型、含清空态），保存 / 回显 / 解绑全链路走通

**Independent Test**: 创建 agent 绑定某 chat 型工作流 → 保存 → 重开回显；清空再保存 → 解绑。全程不触碰工作流页面（spec US1）

### Implementation for User Story 1

- [x] T003 [P] [US1] web/src/api/agent.ts：`AgentBase` 增 `workflow_id: string | null`（响应面，后端 `AgentSchema.WorkflowID *string`）；`AgentSaveData` 增 `workflow_id?: number`（请求面数值——后端 `*uint64` 无 `,string` tag，同 model_id 踩坑 #8；contracts/api-client.md §2）
- [x] T004 [US1] web/src/views/agent/AgentList.vue：新增 `loadWorkflowOptions()`（`getWorkflowList({ page: 1, page_size: 100 })` → `.filter(w => w.type === 'chat')`，与 model / kb 选项同策略现拉）；「基础配置」tab 模型下拉后新增 `el-form-item label="绑定工作流"`——el-select clearable + 空态提示「暂无对话型工作流；可先到工作流管理创建」+ 说明文字「绑定后该 Agent 的对话将直接由工作流处理」；`AgentForm` 增 `workflowId: string`（`''` = 不绑定）、`emptyForm` 增 `workflowId: ''`；openCreate / openEdit 并列拉取（contracts/agent-bind-dropdown.md）
- [x] T005 [US1] web/src/views/agent/AgentList.vue：`toForm` 增 `workflowId: d.workflow_id ?? ''` 回显；`onSubmit` payload 增 `workflow_id: form.workflowId === '' ? undefined : Number(form.workflowId)`（省键 = 创建不绑定 / PUT 全量解绑；数值 = 绑定）；既有字段与校验零变化（FR-008）

**Checkpoint**: US1 独立可测——绑定 / 回显 / 解绑 / chat-only / 空态 / 404 留页（spec US1 场景 1-5）

---

## Phase 4: User Story 2 - 编排者从编辑页 / 详情页发起试运行 (Priority: P2)

**Goal**: 双入口「试运行」按钮 + 共用对话框：三态入参表单、执行、成功结果基本呈现；编辑页脏态先提示保存

**Independent Test**: 对已落库工作流（draft 或 published）在详情页点「试运行」→ 填入参 → 执行 → 看到结果，不依赖编辑页（spec US2）

### Implementation for User Story 2

- [x] T006 [P] [US2] web/src/views/workflow/WorkflowTrialDialog.vue（新组件）：props `modelValue` + `workflow: Pick<WorkflowDetail, 'id' | 'name' | 'type' | 'input_schema'> | null`、emits `update:modelValue`；`watch(modelValue)` 置 true 重置表单 / 结果态；入参三态（A chat 单 textarea 必填 maxlength 16384 + show-word-limit / B task 有 schema 按 SchemaField 行：string→el-input、number→el-input-number、boolean→el-switch，required 动态 rules + scroll-to-error / C task 无 schema 单 textarea 直传）；执行 `run()` 入口 `running` 短路防重复、调 `executeWorkflow`、成功渲染 output（pre-wrap）+ 轨迹表（node_key / node_type / status / duration_ms 执行序）；改参再执行结果刷新（contracts/workflow-trial-dialog.md §2-§4）
- [x] T007 [P] [US2] web/src/views/workflow/WorkflowDetail.vue：PageHeader actions「编辑」左侧增「试运行」按钮 + `trialVisible` ref + `<WorkflowTrialDialog v-model="trialVisible" :workflow="detail" />`（只读页无脏态，直接打开）
- [x] T008 [US2] web/src/views/workflow/WorkflowEdit.vue：工具栏「保存」左侧增「试运行」按钮；点击先 `isDirty()`（既有快照比对）——脏 → `ElMessageBox.confirm('当前有未保存改动，试运行执行的是已保存版本，请先保存。', { confirmButtonText: '去保存', cancelButtonText: '取消' })`，确认调既有 `save()`、取消留页（对话框不开）；不脏 → `trialVisible = true`；两步式创建第二步（WorkflowOrchestrate.vue）不加入口（FR-003 MUST NOT）

**Checkpoint**: US2 独立可测——三态入参 / 脏态阻断 / 干净直开 / 两步式无入口（spec US2 场景 1-5）

---

## Phase 5: User Story 3 - 试运行结果与错误可读呈现 (Priority: P3)

**Goal**: 失败两面（运行失败 / 请求失败）可读落结果区，轨迹表含错误列，防重复触发

**Independent Test**: 构造含条件分支的工作流试运行两次（不同入参命中不同分支），对比输出与轨迹表差异（spec US3）

### Implementation for User Story 3

- [x] T009 [US3] web/src/views/workflow/WorkflowTrialDialog.vue：结果呈现完整面——`status === 'failed'` 时 `el-alert type="error"` 文案取 `node_trace` 执行序最后一条非空 `error_msg`（无则「执行失败」）且轨迹表照常展示（中断点可见）；请求失败 catch 写 `errorMsg`（`e instanceof Error ? e.message : '执行失败'`）结果区 el-alert 持久展示（拦截器 toast 照常并存）；轨迹表失败行 `error_msg` 红字 + 总耗时（duration_ms）展示；无裸异常文本（contracts/workflow-trial-dialog.md §3.5）

**Checkpoint**: 全故事独立可测——loading 防重复（T006 run 短路）/ 输出 + 轨迹 / 可读错误 / 分支差异（spec US3 场景 1-4）

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: 人工验收文档增补 + 收官门禁

- [x] T010 [P] docs/testing/agent-manual-test.md：增补「绑定工作流」小节——chat-only 选项核对、绑定 / 回显 / 解绑全链路、并发删除 404 留页、空态提示、既有字段回归复测（quickstart 场景 1/4 映射）
- [x] T011 [P] docs/testing/workflow-frontend-manual-test.md：增补「试运行」小节——三态入参、载荷 Network 逐键核对（body 单一 input / query trial=true / workflow_id 数值与省键，SC-003）、loading 防重复、输出 + 轨迹、失败文案、分支差异、脏态阻断、两步式无入口（quickstart 场景 2/3 映射）
- [x] T012 收官门禁：`cd web && npm run type-check && npm run build` 全绿（SC-005）；对照 quickstart.md 场景 4 核对既有回归人工项清单完整性

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: 无依赖，立即可跑（基线门禁）
- **Foundational (Phase 2)**: 依赖 Phase 1；阻塞 US2 / US3（executeWorkflow 客户端）
- **US1 (Phase 3)**: 依赖 Phase 1 即可开工（agent.ts / AgentList.vue 不依赖 workflow.ts 客户端）——与 Phase 2 可并行推进
- **US2 (Phase 4)**: 依赖 T002（executeWorkflow）+ T006（对话框组件先于两页挂载）
- **US3 (Phase 5)**: 依赖 T006（同文件增量）
- **Polish (Phase 6)**: 依赖全部实现任务（T010 / T011 仅依赖对应故事完成，可提前；T012 收官最后）

### Within Each User Story

- T004 → T005 同文件顺序执行（UI 先行、回显 / 提交随后）
- T007 / T008 不同文件、均只依赖 T006 → 可并行
- 无跨故事文件冲突（US1 触 agent 域两文件；US2/US3 触 workflow 域三文件）

### Parallel Opportunities

- T002 ∥ T003 ∥（T001 后即可）
- T007 ∥ T008（T006 完成后）
- T010 ∥ T011（对应故事完成后任意时点）

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. T001 基线 → T003-T005 US1 → T012 门禁
2. **STOP and VALIDATE**: 绑定 / 回显 / 解绑独立人工验证（quickstart 场景 1）

### Incremental Delivery

1. US1（绑定）→ 2. US2（试运行入口与执行）→ 3. US3（呈现精细化）→ 4. 文档增补 + 收官门禁
5. 每步双门禁保持全绿（任务级 DoD：改完即跑 type-check + build）

---

## Notes

- [P] tasks = different files, no dependencies
- [Story] label maps task to specific user story for traceability
- 冻结契约保真点集中在 T002 / T003 / T005（json 键名、payload 形态、数值转换）——实现时逐字对照 contracts/
- US3 场景 1（loading 防重复）由 T006 的 run() 入口短路承载（执行链路属 US2 交付面），T009 补呈现面——比对表已注明
- 本篇零后端 / 零迁移 / 零新依赖：任何触 internal/、migrations/、package.json 的冲动都是范围越界，停下问用户
