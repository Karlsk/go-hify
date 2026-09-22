# Tasks: 工作流详情 / 编辑前端（010-workflow-frontend-detail-edit）

**Input**: Design documents from `/specs/010-workflow-frontend-detail-edit/`（plan.md / spec.md / research.md / data-model.md / contracts/workflow-frontend-api.md / quickstart.md）

**Prerequisites**: plan.md（文件树与选型）、spec.md（FR-001~FR-017 + 四个 User Story）

**Tests**: 无测试任务——web/ 无前端单测基建且不引入测试框架（spec Assumptions；引入属新第三方依赖须软门禁）；**任务级 DoD = `cd web && npm run type-check && npm run build` 全绿**（每任务完成后跑一次，红了当场修不攒）。人工验收走 docs/testing/workflow-frontend-manual-test.md 增补节（T019 交付）。后端零改动（git 变更禁触 internal/ 与 migrations/）；零新增第三方依赖。

**Organization**: 任务按 User Story 分组，实施顺序 **US1（P1 详情）→ US3（P3 Schema 表单，提前于 US2）→ US2（P2 编辑）→ US4（P4 创建两步式）**——US3 提前的依据：编辑页（US2）的 Schema 抽屉依赖 SchemaFieldsEditor 组件，spec US3 自述「独立组件、创建 / 编辑两处复用，可先行交付」；FR 优先级编号与实施顺序解耦。跨 Story 共享层（api 增量 / graph.ts 纯逻辑 / 双子组件只读态 / GraphModeEditor）落 Phase 2 Foundational。

## Format: `[ID] [P?] [Story] Description`

- **[P]**: 可并行（不同文件、无未完成依赖）
- **[Story]**: 所属 User Story（Setup / Foundational / Polish 无标签）
- 所有路径相对仓库根；视觉约束横切所有 UI 任务：业务代码只引 `--hf-*` 语义 token、禁硬编码色值，空列表 `[]`、空串 `""`（spec Assumptions）
- 实现细节锚点：research.md 十项决策（#编号）、data-model.md（§节）、contracts/workflow-frontend-api.md（§节）——任务描述引用不自造

---

## Phase 1: Setup（共享基础设施）

- [ ] T001 基线核验：分支 `010-workflow-frontend-detail-edit`；`cd web && npm run type-check && npm run build` 全绿；`git status --short` 仅 010 规划产物（specs/010-…/ + CLAUDE.md + .specify/feature.json）——在干净基线上开工（009 交付 c37a89b 后无未预期变更）

---

## Phase 2: Foundational（跨 Story 共享层：api / 纯逻辑 / 共享组件）

- [ ] T002 [P] web/src/api/workflow.ts 增量：`WorkflowDetailNode`（key / type 后端 7 类全集字符串 / name 恒串 / config `Record<string, unknown>`）/ `WorkflowDetailEdge`（condition: string | null）/ `WorkflowDetail` / `UpdateWorkflowData`（**无 type 字段**——PUT 携带即拒硬红线的类型层闸）四类型 + `getWorkflowDetail(id)` / `updateWorkflow(id, data)`（复用 request.ts 既有 put helper）两方法；文件头注释更新（009「本篇纯消费不接 GET/PUT」→ 010 已接详情 / 更新）；字段与 contracts/workflow-frontend-api.md §2/§3/§6 逐字对齐（description 恒空串非 null、config 外键值字符串保形）
- [ ] T003 [P] web/src/views/workflow/JsonConfigEditor.vue 加 `readonly?: boolean` prop：textarea `disabled` + 藏「格式化」按钮；`validate` 行为不变（research #10）
- [ ] T004 [P] web/src/views/workflow/CanvasEditor.vue 加 `readonly?: boolean` + `fill?: boolean` 两 prop：readonly = 左侧节点面板与 NodeInspector 整块 v-if 不渲染 + VueFlow `:nodes-draggable="false"` + `:nodes-connectable="false"` + onDrop / onConnect / onNodeDoubleClick 开头 `if (props.readonly) return` 短路（平移缩放保留——FR-005 只禁编辑交互，research #3）；fill = 高度铺满父容器（**缺省保持 420px** 不回归 009 既有表单内嵌形态——US4 重构前 WorkflowCreate 仍在直接使用）
- [ ] T005 web/src/views/workflow/graph.ts 纯逻辑增量（依赖 T002 类型）：`detailToGraphConfig(detail): GraphConfig`（name `""`→省略键、condition null→省略键、config 引用直传——未知键透传的根基；schema 不进 GraphConfig）+ `schemaFieldsError(fields): string | null`（name trim 非空、不重名、type ∈ string/number/boolean，错误文案带行号——对齐后端 ValidateSchemaFields 三规则，SC-004）+ `buildUpdatePayload(form, graph, schemas): UpdateWorkflowData`（form 的 type 仅作 task/chat 分支判定、**不组装进 payload**；task 型带 input/output_schema、chat 型不带；config 引用直传）+ PREFILL 深拷贝辅助（`parseGraphConfig(serializeGraphConfig(PREFILL_GRAPH))`——防画布编辑污染模块级常量，research #7）；**parseSchemaFields 暂留**（T016 重构 WorkflowCreate 时一并退役删除，避免中间态编译断）（data-model §2/§4）
- [ ] T006 web/src/views/workflow/GraphModeEditor.vue 新建（依赖 T003/T004）：收编 009 WorkflowCreate 的双模式逻辑——mode radio（JSON / 拖拽）+ JsonConfigEditor + CanvasEditor 条件渲染 + 切换语义（切画布 = 解析 JSON，非法 notify 阻断并回退；切回 JSON = 画布 getGraph 序列化——SC-002 双模式一致）+ props（`initial: GraphConfig` 挂载一次性、`defaultMode?: 'json' | 'canvas'` 缺省 json、`readonly?`、`fill?`——后三者透传子组件）+ expose `getGraph(): GraphConfig | null`（JSON 非法 notify + null）及宿主页脏态快照所需的当前态访问（JSON 模式文本 / 画布模式序列化，data-model §5/§6，research #2）；**009 WorkflowCreate 本任务不接**——其双模式逻辑在 T016 整体删除，不做双改

**Checkpoint**: 共享层就绪，`npm run type-check && npm run build` 绿。

---

## Phase 3: User Story 1 - 查看工作流详情（Priority: P1）🎯 MVP

**Goal**: /workflows/:id 只读详情闭环——列表「查看」入口 + 基础信息 + task 型 Schema 只读表 + 图编排只读双模式（默认画布可切 JSON）+ 404 回列表

**Independent Test**: 列表任意行点「查看」→ 详情正确渲染基础信息与图编排；两模式切换内容一致；画布拖 / 连 / 双击全无效、平移缩放可用；访问不存在 id → 提示 + 回列表（spec US1 四场景）

### Implementation for User Story 1

- [ ] T007 [US1] web/src/views/workflow/WorkflowDetail.vue 新建：onMounted `getWorkflowDetail(route.params.id)`（loading 态；catch → 拦截器已弹错，页面跳回 /workflows）→ `detailToGraphConfig` → `GraphModeEditor(readonly, defaultMode: 'canvas', :initial, v-if 数据就绪)`；基础信息区（名称 / 描述 / 类型 chat|task / 状态三态映射 / 创建时间——沿用列表页既有映射与 tag 形态）；task 型 input/output 两张只读表（四列 name/type/required/description；chat 型整块不渲染，FR-003）；「编辑」按钮 → `/workflows/:id/edit`（FR-006）
- [ ] T008 [US1] web/src/router/index.ts 新增 `/workflows/:id`（name workflow-detail，meta.title「工作流详情」；vue-router 静态段优先——/workflows/create 不被 :id 吃掉，research #5；登录守卫自动生效）
- [ ] T009 [P] [US1] web/src/views/workflow/WorkflowList.vue 操作列加「查看」「编辑」两个 link 按钮（type primary，分别跳 /workflows/:id 与 /workflows/:id/edit；列宽 180→220，平铺不收纳——同屏最多 4 链接，research #6；本任务覆盖 FR-001 + FR-002 入口侧，编辑链接目标随 T014 路由生效）

**Checkpoint**: US1 独立可用——门禁绿 + 手测 spec US1 四场景（画布只读五禁 + 平移缩放可用 + 404 回列表 + 切 JSON 内容一致）。

---

## Phase 4: User Story 3 - Schema 表单化（Priority: P3，提前于 US2 实施）

**Goal**: SchemaFieldsEditor 四字段行表单组件——创建第一步与编辑页共用的 Schema 编辑面（US2 编辑页 Schema 抽屉依赖本组件，故提前；spec US3 自述可先行交付）

**Independent Test**: 组件挂载即可增删行、每行四字段可编辑、非法历史 type 显示原值；完整交互场景（校验拦截 / 回填 / chat 型不显示）随 US2（T011）与 US4（T016）集成后合并验收（spec US3 四场景）

### Implementation for User Story 3

- [ ] T010 [US3] web/src/views/workflow/SchemaFieldsEditor.vue 新建：`v-model: SchemaField[]` + `disabled?: boolean` prop；每行四控件（name el-input / type el-select 三值 string-number-boolean / required el-switch / description el-input）+ 行删除按钮；底部「添加字段」追加空行 `{ name: '', type: 'string', required: false, description: '' }`；**非法历史数据**（回填值 type 不在三值内）el-select 显示原值字符串不炸（spec Edge Case，research #8）；组件本体不做提交校验——校验由宿主页提交前调 graph.ts `schemaFieldsError`（错误文案含行号由函数产出，SC-004）

**Checkpoint**: 组件就绪（type-check / build 绿）；US3 场景 1~4 的页面级验收在 T011 / T016 的 checkpoint 合并执行。

---

## Phase 5: User Story 2 - 编辑工作流（Priority: P2）

**Goal**: /workflows/:id/edit 整页编辑闭环——GET 回填（工具栏内联 + Schema 抽屉 + GraphModeEditor fill）+ PUT 整图替换保存 + type 禁改 + 脏态守卫双通道 + 失败留页

**Independent Test**: 列表点「编辑」→ 画布改一个节点 Prompt → 保存 → 回详情看到新值；不改直接保存往返一致（SC-003）；409 / 图非法留页内容不丢；type 单选禁用；有修改离开弹确认、无修改直接走（spec US2 六场景）

### Implementation for User Story 2

- [ ] T011 [US2] web/src/views/workflow/WorkflowEdit.vue 页面骨架与回填：onMounted `getWorkflowDetail`（loading 态；404 / 失败 → 回列表）→ `detailToGraphConfig` → `GraphModeEditor(:initial, defaultMode: 'canvas', fill)`（v-if 数据就绪再挂载——initial 挂载一次性契约；画布默认可切 JSON，quickstart C.1）；fullBleed 工具栏（meta.fullBleed → App.vue `app__main--flush` 既有机制，research #9）：返回 + 名称 / 描述内联输入 + type 禁用单选（带「类型不可变，换型需删除重建」提示，FR-010）+ task 型「I/O Schema」按钮开 el-drawer（抽屉内 input/output 两个 SchemaFieldsEditor v-model 页面状态，`detail.input_schema ?? []` 兜底；chat 型无按钮，FR-008）
- [ ] T012 [US2] WorkflowEdit.vue 脏态守卫双通道（FR-011，research #4）：加载完成记基准快照 `JSON.stringify({ name, description, inputSchema, outputSchema, graphText })`（graphText = JSON 模式取编辑器文本 / 画布模式守卫触发时取 getGraph() 序列化）→ dirty computed 逐帧比对（序列化比对覆盖检查器直改共享引用——watch 不到的深层变更）；`onBeforeRouteLeave` dirty 时 `ElMessageBox.confirm('未保存的修改将丢失，确认离开？')`（确认放行 / 取消留下）；`watch(dirty)` 注册 / 注销 `beforeunload`（preventDefault + returnValue = ''，拦浏览器刷新 / 关闭）；保存成功置 `saved` 再跳转（守卫放行）
- [ ] T013 [US2] WorkflowEdit.vue 保存链路（FR-009）：点击保存 → task 型先 `schemaFieldsError`（非 null 则 notify 拦截、不发请求——SC-004）→ `GraphModeEditor.getGraph()`（null 拦截）→ `buildUpdatePayload`（**payload 不出现 type 键**——硬红线的组装层闸，与 T002 类型层双重保证）→ `updateWorkflow` → 成功 notifySuccess + `saved` 置位 + 跳 `/workflows/:id`；失败（409 名称冲突 / 400 图规则）留编辑页、内容不丢（错误弹窗由拦截器承担，页面不重复）
- [ ] T014 [US2] web/src/router/index.ts 新增 `/workflows/:id/edit`（name workflow-edit，meta { title: '编辑工作流', fullBleed: true }；/workflows/create/orchestrate 静态段优先不被 :id/edit 吃掉，research #5）

**Checkpoint**: US1 + US2 + US3 独立可用——门禁绿 + 手测 spec US2 六场景 + US3 场景 1~4（编辑侧）+ SC-003 往返一致（含未知 config 键、连线条件标签不改丢）。

---

## Phase 6: User Story 4 - 创建流程重构为两步式（Priority: P4）

**Goal**: 创建拆两步——第一步纯表单（草稿 store）→ 第二步 /workflows/create/orchestrate 整页编排（预填示例图、上一步内容保留、直访 / 刷新回第一步）→「保存并创建」一次 POST

**Independent Test**: 创建 → 填表（task 型含 Schema 行）→ 下一步整页画布拖两节点连线 → 上一步内容保留 → 再进第二步图仍在 → 保存并创建 → 列表见新行、点查看配置正确；直访 / 刷新第二步回第一步（spec US4 五场景 + SC-005）

### Implementation for User Story 4

- [ ] T015 [US4] web/src/stores/workflowCreateDraft.ts 新建：Pinia options API（对齐 stores/auth.ts 惯例）——state { name, description, type, inputSchema, outputSchema, graph: GraphConfig | null }；actions `saveForm(partial)` / `saveGraph(g)` / `clear()`；getter `hasForm`（name !== ''——直访 / 刷新判定；内存态刷新即清零 = 「回第一步」语义天然成立，research #1 / data-model §3）
- [ ] T016 [US4] web/src/views/workflow/WorkflowCreate.vue 重构为第一步纯表单：表单初始值从草稿 store 读（hasForm 时回填——「上一步内容保留」的第一步侧，FR-014）；保留名称 / 描述 / 类型单选；task 型两个 SchemaFieldsEditor 取代 JSON 文本域（chat 型不显示，FR-013 第一步侧）；**删除**画布 / 模式切换 / onModeChange / currentGraph / PREFILL 引用与 JsonConfigEditor / CanvasEditor import；graph.ts `parseSchemaFields` 退役删除（唯一调用方消失，data-model §2）；「创建工作流」= name 必填 + task 型 `schemaFieldsError` 校验（拦截不发请求）→ `saveForm` → `router.push('/workflows/create/orchestrate')`
- [ ] T017 [US4] web/src/views/workflow/WorkflowOrchestrate.vue 新建（第二步整页）：挂载判定 `hasForm`（false → 回 /workflows/create，FR-013 直访语义）；初始图 = `store.graph ?? PREFILL 深拷贝`（T005 辅助；Clarifications 预填智能客服分类示例图）；fullBleed 工具栏：「上一步」（回写 store + 回第一步，不弹确认）+ 名称 / 类型只读展示（修改走上一步，research #9）+「保存并创建」；`GraphModeEditor(:initial, defaultMode: 'canvas', fill)`（画布默认可切 JSON，quickstart A.2）；保存 = `getGraph()`（null 拦）→ `buildCreatePayload`（既有——**POST 带 type**，与 PUT 双轨隔离，data-model 不变量 5）→ `createWorkflow` → 成功 notifySuccess + `store.clear()` + 跳 /workflows（失败留第二步内容不丢）；`onBeforeRouteLeave` 把 `getGraph()` 回写 store（JSON 非法跳过、保留上一份合法图——侧边栏误点不丢图，research #9）
- [ ] T018 [US4] web/src/router/index.ts 新增 `/workflows/create/orchestrate`（name workflow-create-orchestrate，meta { title: '编排工作流', fullBleed: true }；挂 /workflows/create 之后、:id 系列之前——静态段优先与声明顺序无关，按可读性排列，research #5）

**Checkpoint**: 全四 Story 可用——门禁绿 + 手测 spec US4 五场景 + SC-005 全链路计时 + SC-002 三场景双模式一致性抽查。

---

## Phase 7: Polish & Cross-Cutting（文档同步与全量验收）

- [ ] T019 [P] docs/testing/workflow-frontend-manual-test.md 增补 spec 010 场景节：quickstart.md 四场景（A 创建两步式 5 步 / B 详情只读 5 步 / C 编辑保存 8 步 / D 双模式一致性抽查）+ spec Edge Cases 12 项的走查步骤与预期（含 SC-004 拦截、脏态双通道、published 不降级、非法历史 type 标注）（FR-017①，SC-006）
- [ ] T020 [P] web/README.md 目录结构补录：views/workflow/ 三新页（Detail / Edit / Orchestrate）+ GraphModeEditor / SchemaFieldsEditor 组件、stores/workflowCreateDraft.ts、api/workflow.ts 的 getWorkflowDetail / updateWorkflow 与四新类型（FR-017②）
- [ ] T021 全量验收门（SC-001）：`cd web && npm run type-check && npm run build` 绿 + `go build ./... && go vet ./...` 回归绿 + `git status --short` 无 internal/ 与 migrations/ 路径 + 009 既有路径快速回归（列表 / 删除 / 发布停用链路不回归；WorkflowCreate 重构后创建链路整体重走）；人工项（SC-002~SC-006 冒烟走查）移交用户列入验收报告

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup（Phase 1）**: 无依赖，立即开始
- **Foundational（Phase 2）**: 依赖 Phase 1；T002 ∥ T003 ∥ T004 → T005（需 T002 类型）∥ T006（需 T003/T004）；阻塞全部 User Story
- **US1（Phase 3）**: 依赖 Phase 2；页（T007）→ 路由（T008）→ 列表入口（T009）——路由挂载需页面文件先在，保任务级 DoD 绿
- **US3（Phase 4）**: 依赖 Phase 2（schemaFieldsError 在 T005）；仅组件创建，无页面依赖
- **US2（Phase 5）**: 依赖 Phase 2 + Phase 4（编辑页 Schema 抽屉内嵌 SchemaFieldsEditor）；同文件三任务 T011 → T012 → T013 串行
- **US4（Phase 6）**: 依赖 Phase 2（GraphModeEditor / PREFILL 深拷贝）+ Phase 4（第一步 SchemaFieldsEditor）；T015 → T016 → T017 → T018
- **Polish（Phase 7）**: 依赖全部 Story（T019 描述终态行为；T021 全量验收）

### Within Each User Story

- 纯逻辑 / api 层 → 子组件（编辑器 / 表单）→ 页面壳（组装）→ 路由 → 入口 / 集成
- 每任务完成即跑 `cd web && npm run type-check && npm run build`（任务级 DoD），红了当场修

### Parallel Opportunities

- T002 ∥ T003 ∥ T004（三个不同文件、互不依赖）
- T005 ∥ T006（graph.ts 与 GraphModeEditor.vue 不同文件；T005 需 T002、T006 需 T003/T004，彼此无依赖）
- T009 与 T007 后段可并行（不同文件；「查看」链接运行时依赖 T008 路由、编辑链接依赖 T014）
- T019 ∥ T020（两个文档任务不同文件）

---

## Implementation Strategy

### MVP First（US1）

1. Phase 1 + Phase 2 → 共享层就绪
2. Phase 3（US1）→ **MVP 达成**（spec P1 = 本篇最小可用交付：查看入口 + 只读详情闭环，独立交付即有完整价值）
3. Phase 4（US3 组件）→ Phase 5（US2）→ 编辑闭环
4. Phase 6（US4）→ 创建两步式
5. Phase 7 → 文档同步 + 全量验收

单实现者按上述顺序串行推进；每个 Checkpoint 可停下独立验证。

---

## Notes

- **后端零改动红线**：全部任务不触 internal/ 与 migrations/；错误提示一律由 request.ts 拦截器承担（页面不重复弹错，页面只做 catch 后跳转）
- **PUT 不带 type 硬红线**（后端携带即拒、同值也拒）：T002 类型层（UpdateWorkflowData 无 type）+ T005 组装层（buildUpdatePayload 不组装）+ T013 消费层三重保证；POST 带 type（buildCreatePayload 既有）——两套 payload 双轨隔离（data-model 不变量 5）
- **保真不变量**（data-model §4 六条）：未知 config 键引用直传（T005/T013）、condition null ↔ 键省略（T005）、外键字符串保形（全链路零转换）、PREFILL 深拷贝防污染（T005/T017）
- 视觉纪律横切全部 UI 任务：只引 `--hf-*` token、空列表 `[]`、空串 `""`
- 提交时机由用户明示（全程不自动 commit）；建议可提交节点：MVP（US1）一处、US2+US3 一处、US4+文档同步一处——最终由用户定
