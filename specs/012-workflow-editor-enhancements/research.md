# Research: 工作流拖拽编辑器八项增强

**Feature**: [spec.md](./spec.md) | **Branch**: `012-workflow-editor-enhancements` | **Date**: 2026-09-22

本文记录 plan 阶段的技术决策。全部决策建立在 spec 009/010 交付的既有代码形态上（勘察结论），后端零改动。

---

## 决策 1：伪「开始」节点——不入画布节点数组，独立渲染层

**Decision**: 伪开始节点**不进入** Vue Flow 的 `nodes`/`applyNodeChanges` 数据流，作为画布左端的独立渲染元素（绝对定位区块）+ 从其引出一条指向当前起始节点的装饰连线（SVG overlay 或独立 Edge 渲染，不进 `edges` 数组）。点击它 = 选中「伪节点上下文」，检查器切 schema 面板模式。

**Rationale**:
- 序列化不变量天然成立：`getGraph`/`serialize` 只遍历 `nodes`/`edges`，伪节点不在其中则零过滤逻辑、零泄漏可能（优于「放进数组再到处过滤」——那要求每条遍历路径都记得排除，一处遗漏即脏载荷）。
- 不吃掉 `onNodesChange` 的 remove/drag 事件流：伪节点若在数组内，Backspace 删除、拖拽都会命中它，需要额外拦截。
- 起始指向的视觉表达是纯装饰（伪节点 → startKey 节点的箭头），无需进图模型。

**Alternatives**:
- A. 伪节点作为带 `__start__` 固定 id 的真实 node 加入数组，所有遍历处过滤——被否：过滤点分散（getGraph/serialize/ancestors/删除级联/变量源），不变量靠约定不靠结构。
- B. schema 状态放 GraphConfig 聚合字段由画布持有——被否：schema 单源已定在宿主页（spec Assumptions），画布持第二份引入同步 bug 面。

---

## 决策 2：schema 同源绑定——props 下行、emits 上行，宿主页单源

**Decision**: 数据流单一方向：宿主页（`WorkflowEdit` 的 `inputSchema`/`outputSchema` ref，或 `WorkflowOrchestrate` 的 store 字段）→ `GraphModeEditor` 透传 props → `CanvasEditor` → 伪节点 schema 面板；面板编辑 emit 上行（`update:inputSchema` / `update:outputSchema`）逐层回传宿主页改源。复用既有 `SchemaFieldsEditor` 行组件渲染两段表单（语义/校验与抽屉、两步式第一步完全一致）。chat 型：面板只读展示「本工作流为 chat 型，仅暴露 `{{input}}` 单一入参」说明。

**Rationale**: 与 Vue 单向数据流一致；宿主页既有脏态守卫（snapshot 比对）自动覆盖伪节点面板的编辑——一处改动另一处同步是「同一个 ref」的自然结果，非任何同步代码。readonly 态：面板换只读展示（或复用 SchemaFieldsEditor 的禁用态）。

**Alternatives**: 双向 v-model 深层穿透 + provide/inject——被否：组件层级仅三层，props/emits 显式可追，不值得 inject。

---

## 决策 3：key 改名级联——graph.ts 纯函数 `renameNodeKey`

**Decision**: 新增纯函数 `renameNodeKey(canvasState, oldKey, newKey)`（graph.ts），一次返回新画布状态：nodes（id=key 语义下重建条目）、edges（source/target 替换）、startKey（相等则替换）、positions（键迁移）、selected（指向新 key）。非法输入（空/超 64/与现存 key 冲突）在 NodeInspector 编辑点前端拦截（校验规则与 JSON 模式既有校验对齐），不进函数。触发点：检查器 Key 输入框 blur/change 时经 CanvasEditor 应用。

**Rationale**: 级联是纯数据变换，与既有 `migrateStartKey`、remove 级联同族，收敛 graph.ts 可被两宿主（编辑页/两步式）无差别复用；不可变更新（返回新对象）符合仓库全局编码规则。**不改写模板文本** `{{old_key}}`（spec Assumptions 已定：R10 保存期后端校验兜底）。

**Alternatives**: 在 NodeInspector 内散写各处替换——被否：级联点五处（id/edges/start/positions/selected），散写必漏；且两宿主各自复制一份。

---

## 决策 4：变量源——`ancestorsOf` 纯函数 + 子工作流 output_schema 会话缓存

**Decision**:
- 祖先集合：graph.ts 新增纯函数 `ancestorsOf(nodes, edges, nodeKey)`（沿 edges 反向 BFS，输入 key 即起点排除自身）。
- 变量条目三分组（FR-007）：①「入参」：`{{input}}` 恒在；task 型再按本图 input_schema 展开 `{{input.x}}`（消费宿主页 schema ref）②「上游节点」：祖先 key → `{{key}}` ③「上游子流程出参」：祖先中 workflow 类型节点，按其绑定子流程的 output_schema 展开 `{{key.field}}`——选中节点变化时，NodeInspector 对**祖先 workflow 节点**调既有 `getWorkflowDetail` 拉详情，模块级 `Map<string, SchemaField[]>` 会话缓存（同 id 不重复请求），加载中下拉分组显示 loading。
- 条目计算在 NodeInspector 层（有图上下文与 api 访问），TemplateField 只收渲染好的条目数组。

**Rationale**: 祖先而非全图节点——非祖先引用必被 R10 拒，提前收窄是纯收益且实现成本相同（一次 BFS）。子流程出参只在需要时（下拉打开/节点选中）拉取，避免每次编辑字段都打接口；会话级缓存够用（output_schema 编辑期罕见变化，重进页面即刷新）。

**Alternatives**: 列出全部节点 key——被否：引导用户选到后端必拒的引用。每次下拉打开都重新拉详情——被否：同一子流程反复请求无意义。

---

## 决策 5：TemplateField 组件——无图依赖的哑组件

**Decision**: 新组件 `TemplateField.vue`：props = `modelValue: string`、`variables: VariableOption[]`（分组条目，见 data-model）、`disabled`、`placeholder`、`rows`；emit `update:modelValue`。结构 = el-input textarea（v-model 主体）+ 前缀/后缀插槽位的 el-select 分组下拉；选中条目时按**光标位置**（textarea 的 `selectionStart`）拼插 `{{…}}` 文本后整体 emit，随后恢复焦点与光标。手写完全自由（下拉不校验、不格式化既有文本）。

**Rationale**: 组件无图依赖、无 api 依赖——被 LLM/end/condition/API/子工作流七类字段统一复用（FR-008）；光标插入是唯一状态性逻辑，用原生 textarea 属性实现，不引第三方编辑器。readonly 由 `disabled` 透传。

**Alternatives**: 引入富文本/代码片段插件——被否：软门禁（plan 未列依赖，零新增已定）；纯文本模板 `{{}}` 语法不需要结构化编辑。

---

## 决策 6：Auth 预设——推导自 headers，写回单键

**Decision**: API 检查器 Auth 区不设独立状态源：预设类型**推导**自 `config.headers.Authorization` 现值（`Bearer ` 前缀 / `Basic ` 前缀 / 无）。选 Bearer → 写 `Authorization: Bearer <token>`；选 Basic → `Authorization: Basic <base64(user:pass)>`（UTF-8 安全编码：`btoa(unescape(encodeURIComponent(...)))` 形态）；切预设先移除旧 Authorization 行再注入新行（US3-3 无残留）。Bearer/Basic 的 token/账密输入框在预设选定后内联出现；预设之外的 headers 行照常 KV 编辑，程序只保证 Authorization 键归 Auth 区管（用户手输 Authorization KV 行视同覆盖，推导区随动）。

**Rationale**: 单一事实源 = `config.headers`（后端契约形态），无独立 auth 字段即无双写不一致；回读详情时预设自动正确推导，零迁移逻辑。Basic 的 base64 在前端完成后端原样透传——后端 headers 值即最终 HTTP 头值，这是既有契约语义。

**Alternatives**: 独立 `config.auth` 对象——被否：显式违反拍板（无新增 config 键）。

---

## 决策 7：删除入口——复用 remove 级联链 + 新增选中连线态

**Decision**:
- 节点删除：NodeInspector 底部 danger 按钮 → emit `delete` → CanvasEditor 构造与 Backspace 完全相同的 remove changes（走既有 `onNodesChange` 级联：悬挂边清理、positions 清理、migrateStartKey、选中清空）——同一代码路径，零新级联逻辑。
- 连线删除：CanvasEditor 新增 `selectedEdge` ref（`onEdgeClick` 选中、`onPaneClick` 清空）；选中连线时检查器显示连线信息 + 删除按钮 → 走 `onEdgesChange` remove（既有 change 流）。
- 伪节点不可删除（无删除入口）。

**Rationale**: FR-001 要求级联语义不变——最稳妥的「不变」是复用同一入口；选中连线态是既有节点选中模式的对称扩展。

**Alternatives**: 每个节点上悬浮 × 按钮——被否：小节点上命中区拥挤，检查器入口与 spec 场景（「选中节点 → 检查器删除」）一致。

---

## 决策 8：子工作流入参渲染——watch workflow_id，重建保留同名值

**Decision**: NodeInspector 内 watch 选中节点的 `config.workflow_id`：变化即（缓存命中或）`getWorkflowDetail` 拉子流程 → 取 `input_schema` 渲染入参行（字段名只读标签 + TemplateField 值域）；行值读写 `config.inputs[field]`。切换时先按新 schema 字段名从旧 `config.inputs` 摘同名字段保留，其余丢弃（Edge Case「无同名字段可保留 → 丢弃」）；无 schema 显示 el-empty 类提示；下拉选项 = 既有 workflowOptions 排除自身（编辑态有 id 时）。加载中行区域显示 loading 骨架。

**Rationale**: 消费既有详情端点（spec 008 交付 input_schema），与决策 4 共用会话缓存；「保留同名值」把切换子流程的编辑损失降到最低。

**Alternatives**: 入参行由用户手加字段名——被否：正是用户反馈要消除的易错点（FR-006）。

---

## 依赖与前提核实（2026-09-22）

| 前提 | 证据 |
|---|---|
| spec 011 system_prompt 后端已合入 | commit 3c82c3f；executor.go callLLM 已消费（`[system,user]` 消息序） |
| 详情 GET 返回 input_schema/output_schema | spec 008 交付；api/workflow.ts `WorkflowDetail` 类型已含 |
| onNodesChange remove 级联链存在 | spec 009 交付（悬挂边清理/positions 清理/migrateStartKey） |
| SchemaFieldsEditor 可复用 | spec 010 编辑页抽屉在用 |
| useStrConfigField 单源写入模式 | spec 009 交付，NodeInspector 现有字段全走此模式 |
| 零新增 npm 依赖 | package.json 既有 Element Plus/@vue-flow/core 覆盖全部需求 |
