# Tasks: 工作流拖拽编辑器八项增强

**Input**: Design documents from `/specs/012-workflow-editor-enhancements/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/frontend-components.md, quickstart.md

**Tests**: 前端无单测基建且明确不引入测试框架（软门禁约束）——无测试任务。每个实现任务的 DoD = 类型检查通过（`cd web && npm run type-check`）；全篇完成定义 = 双门禁全绿（type-check + build）+ quickstart.md 人工场景（SC-001~005）。

**Organization**: 按 user story 分组（P1 删除/key 改名 → P2 变量组件 → P3 LLM/API 表单 → P4 子工作流渲染 → P5 entry 下拉与 schema 面板 → P6 回归收口）；graph.ts 纯函数为跨 story 前提置 Phase 2。

## Format: `[ID] [P?] [Story] Description`

- **[P]**: 可并行（不同文件、无未完成依赖）
- **[Story]**: 归属 user story

## Path Conventions

前端篇：全部路径相对仓库根，改动收敛 `web/src/views/workflow/`（7 文件，其中 TemplateField.vue 新建）+ `web/src/api/workflow.ts`（预期零改动）+ `docs/testing/`。规范以 web/README.md 为准。

---

## Phase 1: Setup（基线门禁）

**Purpose**: 开工门禁——不在脏基线上动手

- [X] T001 基线门禁核实：`cd web && npm run type-check && npm run build` 全绿；`git status` 确认工作区仅 specs/012 规划产物；核对 `web/src/views/workflow/` 既有 6 文件与 `web/src/api/workflow.ts` 的 `WorkflowDetail` 类型含 input_schema/output_schema（spec 008 前提）

---

## Phase 2: Foundational（graph.ts 纯函数——US1/US2 共同前提）

**Purpose**: 跨 story 的纯逻辑层扩展，数据契约见 contracts/frontend-components.md §4

- [X] T002 `web/src/views/workflow/graph.ts` 新增 `renameNodeKey(state, oldKey, newKey)` 纯函数：不可变更新，一次返回新画布状态——nodes 条目替换（key=id）、edges source/target 替换、startKey 相等替换、positions 键迁移、selected 指向新 key；不改写模板文本 `{{old_key}}`（spec Assumptions）；幂等（newKey==oldKey 返回等值状态）
- [X] T003 `web/src/views/workflow/graph.ts` 新增 `ancestorsOf(nodes, edges, nodeKey)` 纯函数：沿 edges 反向 BFS 求祖先 key 集合（不含自身）；`getGraph`/`serialize` 零触碰（图主体只含真实节点由结构保证，research 决策 1）

---

## Phase 3: User Story 1 — 删除入口与 key 改名 (Priority: P1) 🎯 MVP

**Story goal**: 检查器可见「删除节点/删除连线」按钮 + Key 编辑框（校验+级联），消除 Backspace 唯一入口与 key 不可改。

**Independent Test**: quickstart 场景 1——拖入 3 节点删除中间节点核对级联；改 key 切 JSON 核对旧 key 零残留；冲突/清空被拦截。

- [X] T004 [US1] `web/src/views/workflow/CanvasEditor.vue` 新增选中连线态：`selectedEdge` ref（`onEdgeClick` 置值与节点选中互斥、`onPaneClick` 清空）、prop 透出至检查器容器；readonly 态连线不可选
- [X] T005 [P] [US1] `web/src/views/workflow/NodeInspector.vue` 新增：①「删除节点」danger 按钮（emit `delete-node`，schema 面板上下文/readonly 不显示）②选中连线模式：连线信息 + 「删除连线」按钮（emit `delete-edge`）③Key 编辑输入框——非空 / ≤64 / 不与画布现存 key 冲突前端拦截（对齐 JSON 模式既有校验），合法经 emit `rename-node-key` 提交；readonly 禁用
- [X] T006 [US1] `web/src/views/workflow/CanvasEditor.vue` 事件接线：`delete-node` 构造与 Backspace 相同的 remove changes 走既有 `onNodesChange` 级联链（悬挂边/positions/migrateStartKey/选中清空零新逻辑）；`delete-edge` 走 `onEdgesChange` remove；`rename-node-key` 调 T002 `renameNodeKey` 应用返回状态

**Checkpoint**: 删除与改名全链路可用，既有级联语义不变。

---

## Phase 4: User Story 2 — 变量引用下拉与可写模板字段 (Priority: P2)

**Story goal**: TemplateField 通用组件（文本域+分组下拉+光标插入）落地并接入既有模板字段——US3/US4 新字段的公共底座。

**Independent Test**: quickstart 场景 2——task 型图 prompt 字段下拉三分组齐全；光标中间插入；手写混排；chat 型无入参展开组。

- [X] T007 [US2] 新建 `web/src/views/workflow/TemplateField.vue` 哑组件（contracts §1 契约逐字实现）：props = modelValue/variables/disabled/placeholder/rows；emit `update:modelValue`；textarea 主体 + 分组下拉（el-select group 或等价分组形态），选中按 `selectionStart` 光标位置插入 `option.insert` 完整 `{{…}}` 文本后整体 emit 并恢复焦点；手写零干预；disabled 全禁用（SC-004）
- [X] T008 [US2] `web/src/views/workflow/NodeInspector.vue` 变量源计算与既有字段接入：①`VariableOption`/`VariableGroups` 类型（data-model §2）与条目计算——input 分组（`{{input}}` 恒在 + task 型按宿主 input_schema 展开 `{{input.x}}`）、upstream 分组（T003 ancestorsOf 的祖先 key）、subflow-output 分组（祖先 workflow 节点 × 其子流程 output_schema 展开 `{{key.field}}`，详情走既有 `getWorkflowDetail` + 模块级 `Map<id, SchemaField[]>` 会话缓存，加载中分组 loading）②既有模板字段替换为 TemplateField：llm prompt / end output / condition expression / api url（data-model §3 矩阵前四行）③readonly 透传 disabled

**Checkpoint**: 变量下拉在既有四类模板字段可用；US3/US4 新字段可直接复用组件。

---

## Phase 5: User Story 3 — LLM 双输入框与 API 全要素 (Priority: P3)

**Story goal**: LLM System Prompt + Prompt 双文本域（消费 spec 011 system_prompt，commit 3c82c3f）；API Headers KV + Auth 预设 + Body 模板。

**Independent Test**: quickstart 场景 3——切 JSON 核对 config.system_prompt/prompt/headers/body 键形态；Auth 切换无残留；保存回读推导正确。

- [X] T009 [US3] `web/src/views/workflow/NodeInspector.vue` LLM 表单：System Prompt（TemplateField，可选，空=不携带键）+ Prompt（TemplateField，必填提示）读写 config.system_prompt/config.prompt（沿用 useStrConfigField 模式，system_prompt 空串序列化时剔除对齐后端 omitempty）
- [X] T010 [US3] `web/src/views/workflow/NodeInspector.vue` API 表单：①Headers KV 行编辑（增删行、值域 TemplateField；空键行不产出 config 条目、重复键后写覆盖）②Auth 预设区——类型推导自 `config.headers.Authorization` 现值（Bearer /Basic 前缀 / 无），选 Bearer 写 `Bearer <token>`、选 Basic 写 `Basic <base64(utf8(user:pass))>`（UTF-8 安全编码），切预设先删旧 Authorization 行再注入（research 决策 6 映射表）③Body 模板域（TemplateField，Method=POST 时显示）读写 config.body

**Checkpoint**: API POST 场景全要素可配，config 键集零越界（SC-003）。

---

## Phase 6: User Story 4 — 子工作流入参自动渲染 (Priority: P4)

**Story goal**: 选中子流程后按其 input_schema 自动渲染入参行，字段名锁定、值为模板、切换保留同名值。

**Independent Test**: quickstart 场景 4——两子流程切换核对重建与 question 同名保留；无 schema 提示；编辑态下拉排除自身。

- [X] T011 [US4] `web/src/views/workflow/NodeInspector.vue` 子工作流入参渲染（research 决策 8）：watch 选中节点 `config.workflow_id` → 详情（会话缓存）取 input_schema 渲染入参行（字段名只读标签 + TemplateField 值域，读写 `config.inputs[field]`）；切换重建时按新 schema 字段名保留旧值同名字段、其余丢弃；无 schema 显示提示（el-empty 类）；子工作流下拉排除工作流自身（编辑态有 id 时）；加载中 loading；readonly 行禁用

**Checkpoint**: inputs 不再手写字段名，错字在编辑期消除。

---

## Phase 7: User Story 5 — entry 下拉指定与画布内 schema 配置 (Priority: P5)

**Story goal**: 左面板「起始节点」下拉直接指定 entry（选项 `key（类型名）`），双击节点与检查器「设为起始」按钮等价入口；「入参 / 出参」按钮开检查器 schema 面板，与宿主页表单同源同步。（2026-09-23 裁定：取消画布伪「开始」节点，本相位按裁定后形态记录。）

**Independent Test**: quickstart 场景 5——两宿主分别核对面板 ↔ 第一步/抽屉双向同步；下拉/双击/按钮切换起始核对边框迁移与 start_node_key 随动；chat 型说明态；readonly 无交互。

- [X] T012 [US5] `web/src/views/workflow/CanvasEditor.vue` entry 指定与面板入口：左面板「起始节点」下拉（选项 = 画布现存节点 key，`key（类型名）` 形态）直改 startKey 单源并重刷起始 class；双击节点（onNodeDoubleClick）与检查器「设为起始」按钮为等价入口（readonly 守卫）；「入参 / 出参」按钮置伪节点上下文（sentinel，**不进 nodes 数组**）通知检查器切 schema 面板；新增 inputSchema/outputSchema/graphKind props 与 update:inputSchema/update:outputSchema emits（contracts §3）
- [X] T013 [US5] `web/src/views/workflow/NodeInspector.vue` schema 面板模式：伪节点上下文（sentinel）时——task 型渲染入参/出参两段 SchemaFieldsEditor（复用既有行组件语义与校验，emit 同源上行）；chat 型只读展示「仅暴露单一入参 `{{input}}`」说明；readonly 面板禁用
- [X] T014 [US5] `web/src/views/workflow/GraphModeEditor.vue` schema 同源透传：inputSchema/outputSchema props 下行至 CanvasEditor、update 事件上行至宿主（contracts §5 契约）
- [X] T015 [US5] `web/src/views/workflow/WorkflowEdit.vue` 与 `web/src/views/workflow/WorkflowOrchestrate.vue` 两宿主接线：编辑页 inputSchema/outputSchema 既有页面 ref 即单源（抽屉表单与 schema 面板绑同一 ref，脏态守卫 snapshot 自动覆盖面板编辑）；两步式第一步 store 字段为单源（saveInputSchema/saveOutputSchema action 写回）、第二步画布同源接线并核对 store 回写

**Checkpoint**: schema 画布内可配且单源不破；保存载荷形态不变（SC-003）。

---

## Phase 8: User Story 6 + Polish — 回归收口与验收

**Purpose**: 存量零回归证据链 + 文档同步 + 验收报告

- [X] T016 [US6] readonly 全面核对：详情态逐一走查全部新增交互（删除/改名/Key 编辑/入参出参面板/Auth/Headers 行/子流程下拉与入参行/变量下拉/起始三入口）确认禁用或不可达（SC-004）；未知 config 键往返透传抽查（`timeout_sec` 手写键拖拽↔JSON 不丢）
- [X] T017 [US6] `docs/testing/workflow-frontend-manual-test.md` 增补 spec 012 小节：quickstart.md 六组场景落为正式人工用例（八项缺口逐项 + EC-1~23 全数复测引用 + 载荷回读核对项）
- [X] T018 验收收尾：`cd web && npm run type-check && npm run build` 双门禁全绿（SC-005）；`git status --short` 核对改动面 = web/src/views/workflow/ 7 文件 + docs/testing/ 1 文件（api/workflow.ts 预期零改动，若被动过停下核对）；产出验收报告（quickstart SC 对号 + 剩余人工项清单交用户）

---

## Dependencies & Execution Order

```text
T001 → T002 → T003 → US1(T004→T005→T006) → US2(T007→T008) → US3(T009→T010) → US4(T011) → US5(T012→T013→T014→T015) → US6(T016→T017→T018)
```

- Phase 2 两纯函数同文件顺序执行；US1 依赖 T002（renameNodeKey），US2 依赖 T003（ancestorsOf）。
- US2 必须先于 US3/US4：TemplateField（T007）与其变量源（T008）是后两 story 新字段的底座（spec P2 优先级论证）。
- story 内任务多同为 NodeInspector.vue 改动——同文件顺序执行，无真实并行点（T004/T005 异文件为 story 内唯一可并行对）。
- US5 依赖 US1 的选中态扩展与 US2 的 input_schema 消费模式（面板复用既有接线习惯）。
- US6 收口必须最后（回归验证对象是全部增强落定后的终态）。

## Parallel Example: User Story 1

```bash
# T004（CanvasEditor.vue 连线选中态）与 T005（NodeInspector.vue 删除/改名 UI）异文件可并行：
Task: T004 CanvasEditor selectedEdge 态
Task: T005 NodeInspector 删除按钮/Key 编辑
# T006 接线须待两者完成后执行
```

## Implementation Strategy

**MVP = Phase 2 + Phase 3（US1）**——删除与 key 改名是编辑器地基（用户反馈第一条），独立可用。增量交付：US2 组件底座 → US3/US4 表单补全 → US5 伪节点 → US6 回归收口。范围红线：不触 JSON 模式编辑器、不加 API timeout_sec/ssl_verify 表单、不做 tool/knowledge_retrieval 表单、零新增依赖零后端改动；「顺手改进」冲动记下问用户不直接做。
