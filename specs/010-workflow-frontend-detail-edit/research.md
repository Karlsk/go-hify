# Research: 工作流详情 / 编辑前端（010-workflow-frontend-detail-edit）

Phase 0 决策记录。全部决策依据 = 仓库现状代码（009 交付物只读核实）+ Vue / Vue Router / Element Plus / Vue Flow 既有用法，零外部新调研、零新依赖。

---

## 1. 两步式跨路由状态：Pinia 内存 store（`stores/workflowCreateDraft.ts`）

- **Decision**: 创建草稿存 Pinia store（state：name / description / type / inputSchema / outputSchema / graph），第一步「创建工作流」写表单字段，第二步挂载时读、离开时回写图；POST 成功后 clear。
- **Rationale**: spec 只锁「上一步内容保留、直访/刷新回第一步」，传递方式属 plan 层。Pinia 是本仓既有惯例（`stores/auth.ts`），devtools 可观测、无需序列化；**内存态刷新即清零**——「第二步直访/刷新 → 回第一步」的判定（`name === ''`）与语义天然成立，零额外代码。直访判定即「表单未填过」，与「填过但清空名称」不可区分——后者本就被第一步必填校验拦截，不会到达第二步。
- **Alternatives**: sessionStorage（要手写序列化 + 过期语义，刷新反而保留——与「刷新回第一步」相悖，否决）；history.state（容量小、语义晦涩，否决）；组件 keep-alive（路由形态下脆弱，否决）。

## 2. GraphModeEditor 抽取形态：initial-at-mount + expose getGraph

- **Decision**: 新组件 `GraphModeEditor.vue` 收编 009 WorkflowCreate 的双模式逻辑：mode radio（JSON / 拖拽）+ JsonConfigEditor + CanvasEditor + 切换语义（切画布 = 解析 JSON，非法则回退并提示；切回 JSON = 画布序列化）+ 出口 `getGraph(): GraphConfig | null`（JSON 模式解析失败 notify + null；画布模式经 CanvasEditor.getGraph）。Props：`initial: GraphConfig`（**挂载时一次性读取**，同 CanvasEditor 既有 `props.config` 契约）、`defaultMode?: 'json' | 'canvas'`（缺省 json）、`readonly?: boolean`、`fill?: boolean`。父组件以 `v-if="数据就绪"` + 可选 `:key` 控制挂载时机。
- **Rationale**: 三页（编排第二步 / 编辑 / 详情）共用同一编排形态是 FR-015 的字面要求；逻辑收敛一处后，非法 JSON 阻断切换、往返等价、未知键透传只在一份代码里维护。initial-at-mount 与 CanvasEditor 既有契约一致（「挂载时从 config 初始化，此后画布为源」），不引入响应式重置的复杂度。
- **Alternatives**: 纯 composable `useGraphModes()`（仍要在三页重复拼模板，收益减半，否决）；三页各持一份逻辑（重复 ~80 行 × 3，违背 FR-015「改造而非重写」精神，否决）。

## 3. Vue Flow 只读态：props 降能 + handler 短路 + 面板隐藏

- **Decision**: CanvasEditor 加 `readonly?: boolean` prop：① 左侧节点面板与 NodeInspector 整块不渲染（v-if）；② VueFlow 绑定 `:nodes-draggable="false"` + `:nodes-connectable="false"`；③ `onDrop` / `onConnect` / `onNodeDoubleClick` 处理器开头 `if (props.readonly) return` 短路。平移缩放（panOnDrag / zoomOnScroll 默认开）与 fit-view-on-init 保留——spec FR-005 只禁编辑交互，浏览交互必须可用。
- **Rationale**: Vue Flow 官方只读即 props 降能组合（`nodes-draggable` / `nodes-connectable` 是 @vue-flow/core 组件props）；handler 短路是双保险（防 drop 事件从外部拖入）。选中态事件保留无害（无检查器可更新）。
- **Alternatives**: 单独写 ReadOnlyCanvas.vue（重复渲染代码，违背改造复用，否决）；`edges-updatable` 一并关（本就未开，无需）。

## 4. 脏态守卫双通道：onBeforeRouteLeave + beforeunload + 快照对比

- **Decision**: WorkflowEdit 内：① `onBeforeRouteLeave`（vue-router 组合式 API）——dirty 时 `ElMessageBox.confirm('未保存的修改将丢失，确认离开？')`，确认放行 / 取消留下；② `watch(dirty)` 注册/注销 `window.beforeunload`（`preventDefault()` + `returnValue = ''`，拦浏览器刷新/关闭）；③ dirty 判定 = 加载完成时的**快照字符串**（`JSON.stringify({ name, description, inputSchema, outputSchema, graphText })`）与当前值逐帧比对（computed）——JSON 模式取编辑器文本、画布模式取 `getGraph()` 序列化；④ 保存成功置 `saved = true` 后再跳转（守卫放行）。
- **Rationale**: clarify 已拍板双通道均拦。快照对比比逐控件 watch 可靠（覆盖画布深层 config 变更——检查器直改共享引用，watch 不到，序列化比对才看得到）；computed 响应 graphText / 表单 / schema 的响应式变更，画布模式在守卫触发时即时取值。`beforeunload` 只能弹浏览器原生确认（无法自定义 UI，行业常态）。
- **Alternatives**: 仅路由守卫（不满足 clarify 双通道拍板，否决）；深比较函数（与 JSON 序列化等价但多写代码，否决）；Pinia 存基线（无必要，页内快照即够，否决）。

## 5. 路由：三条新增 + vue-router 静态段优先排序

- **Decision**: `/workflows/create/orchestrate`（name `workflow-create-orchestrate`，meta `{ title: '编排工作流', fullBleed: true }`）、`/workflows/:id`（`workflow-detail`，`{ title: '工作流详情' }`）、`/workflows/:id/edit`（`workflow-edit`，`{ title: '编辑工作流', fullBleed: true }`）。挂在既有 `/workflows/create` 之后、`/knowledge-bases` 之前。
- **Rationale**: vue-router 4+ 按 segment 评分排序，**静态段恒优先于动态段**：`/workflows/create` 不会被 `/workflows/:id` 吃掉、`/workflows/create/orchestrate` 不会被 `/workflows/:id/edit` 吃掉，与声明顺序无关；仍按可读性将 create 系列排在 `:id` 系列前。`:id` 参数原样字符串传（bigint 字符串化约定）。fullBleed 语义见 plan《Structure Decision》（App.vue `app__main--flush` 既有机制）。登录守卫 `beforeEach` 对新路由自动生效（无 bare/public 标记）。
- **Alternatives**: 第二步路由 `/workflows/create?step=2`（同组件内切步——与「独立路由整页」拍板相悖，否决）；嵌套路由 children（两步无共享壳，为嵌套而嵌套，否决）。

## 6. 列表操作列：平铺链接（查看 / 编辑 / 发布或停用 / 删除）

- **Decision**: 操作列加「查看」「编辑」两个 link 按钮（type primary），跳 `/workflows/:id` / `/workflows/:id/edit`；发布/停用本就互斥（v-if/v-else 恒显其一），**同屏最多 4 个链接**，列宽 180 → 220 即可容纳，不做下拉收纳。
- **Rationale**: spec Assumptions 明确「收纳与否由 plan 视觉层定」。4 个两字 link ≈ 200px，无溢出；下拉收纳（el-dropdown「更多」）要为不存在的拥挤买单——一人维护优先选平铺（直觉可见、无二次点击）。spec Edge Case「操作列不过载（必要时收纳）」的「必要时」经测算不成立。
- **Alternatives**: el-dropdown 收纳次要操作（多一层交互，当前无必要，否决）；查看整行可点（与既有列表页行为不一致，否决）。

## 7. 详情 → 图配置转换：`detailToGraphConfig` + PREFILL 深拷贝

- **Decision**: graph.ts 加 `detailToGraphConfig(detail: WorkflowDetail): GraphConfig`：nodes 的 `name === ''` 省略键（后端 `Name string` 恒序列化，空串是合法值）、`config` 引用直传（含未知键透传）、edges 的 `condition === null` 省略键（GraphEdge 可选键语义）。创建第二步初始图 = `store.graph ?? 深拷贝(PREFILL_GRAPH)`——深拷贝用 `parseGraphConfig(serializeGraphConfig(PREFILL_GRAPH))`（既有一轮往返，顺带校验）。
- **Rationale**: SC-003 编辑往返一致（含未知 config 键、连线条件标签）依赖这条转换的保真性：config 整对象引用共享 → 画布/检查器改已知键、未知键原样保留；condition null ↔ 键省略 双向对齐后端 `*string` 语义。**PREFILL_GRAPH 是模块级常量，画布节点 data.config 与配置节点共享引用——直接传入会让画布编辑污染常量**（009 用 serialize→parse 间接拷贝规避了此坑，本篇显式沿用）。
- **Alternatives**: structuredClone（可用但多一种拷贝路径；serialize→parse 与 009 同构且已在测试中验证语义，选定后者）。

## 8. Schema 表单：`SchemaFieldsEditor` + `schemaFieldsError` 校验对齐

- **Decision**: 新组件 `SchemaFieldsEditor.vue`（v-model `SchemaField[]`）：每行四控件（name 输入 / type 下拉三值 / required 开关 / description 输入）+ 行删除；底部「添加字段」。校验函数 `schemaFieldsError(fields): string | null` 落 graph.ts（纯逻辑）：name trim 非空、不重名、type ∈ 三值，错误文案带行号与原因（对齐后端 ValidateSchemaFields 的三条规则）。**非法历史数据**（老数据 type 越界）：el-select 值无匹配 option 时显示原值字符串，校验报「第 N 行类型非法 "xxx"」——满足 Edge Case「显示原值并标注非法、保存前要求修正」。`parseSchemaFields`（JSON 文本路径）退役删除。
- **Rationale**: spec US3/FR-012 字面要求；校验放 graph.ts 与既有 `graphSubmitError` 同位（纯逻辑可独立演进）；el-select 对未知值的默认展示恰好满足「显示原值」。后端规则三条前端全可判 → SC-004 拦截率 100%（前端拦不住的只剩后端图规则 R1-R9，那是图编排的职责）。
- **Alternatives**: 保留 JSON 文本模式并存（用户已明确否决——「不要只支持填 json str」拍板纯表单）；校验塞组件内（不可复用、编辑/创建两处要重复，否决）。

## 9. 编辑页布局：fullBleed 工具栏 + Schema 抽屉；编排页同构

- **Decision**: WorkflowEdit（与 WorkflowOrchestrate 同构）：顶部工具栏（返回 + 名称内联输入 + 描述内联输入 + type 禁用单选带提示「类型不可变，换型需删除重建」+ task 型「I/O Schema」按钮开 el-drawer（内含入参/出参两个 SchemaFieldsEditor）+ 保存主按钮）+ GraphModeEditor(fill) 铺满剩余高度。编排页工具栏：上一步 + 名称/类型只读展示 + 保存并创建。编排页离开路由时把当前图回写 store（不弹确认——spec 未要求第二步脏态守卫，但内容不无故丢失；JSON 非法时跳过回写保留上一份合法图）。
- **Rationale**: FR-007「顶部信息与操作栏 + 图编排编辑区铺满」的字面落地；fullBleed 是 /chat 已验证的整页机制。Schema 用抽屉而非内联：工具栏一行放不下两套四字段行编辑，抽屉是 EP 既有形态（ProviderModelsDrawer 先例）、编辑结果直接 v-model 回页面状态计入脏态快照。编排页只读展示基础信息（修改走「上一步」，职责单一）。回写 store 让「第二步 → 侧边栏离开 → 返回第二步」图不丢（刷新仍回第一步，语义不变）。
- **Alternatives**: Schema 折叠面板内联在工具栏下（挤压画布高度，否决）；编辑页基础信息也用抽屉（名称/描述是高频编辑对象，藏起来反直觉，否决）；第二步不回写 store（侧边栏误点即丢图，一回事的成本换更好的体验，否决）。

## 10. 详情页 JSON 模式：JsonConfigEditor 加 readonly 复用

- **Decision**: JsonConfigEditor 加 `readonly?: boolean` prop：textarea `disabled` + 藏「格式化」按钮（详情页不需要改写文本）。详情页 JSON 模式 = `serializeGraphConfig(当前配置)` 美化文本只读展示，与画布模式同一数据源（GraphModeEditor 内部已保证两模式一致性）。
- **Rationale**: 详情页 FR-004 要求「JSON 美化文本、两模式内容一致」——复用 JsonConfigEditor 拿到等宽字体与既有样式，readonly 是两行改动；不另写 `<pre>` 展示（视觉不一致，且 GraphModeEditor 内部已统一）。
- **Alternatives**: `<pre>` 块（样式分叉，否决）；详情页独立 JSON 查看组件（重复，否决）。

---

## 依赖核实汇总

| 项 | 核实结果 |
|---|---|
| @vue-flow/core / background / controls | package.json 已锁定（009 引入），零新增 |
| put helper（api 层） | `utils/request.ts` 已导出 `put<T>`（web/README.md 请求约定） |
| fullBleed 布局机制 | `App.vue` `app__main--flush` 既有（/chat 在用） |
| 后端 GET /workflows/:id | `internal/workflow/api/schema.go` WorkflowDetailSchema（含图组装）+ handler 路由已冻结交付 |
| 后端 PUT /workflows/:id | UpdateWorkflowReq：body 不含 type（携带即拒）、整图替换、编辑不降级 status |
| Element Plus 组件 | el-drawer / el-switch / el-select / ElMessageBox 均为全量引入内的既有用法 |
