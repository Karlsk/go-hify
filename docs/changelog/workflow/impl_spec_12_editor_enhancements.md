# Workflow 实现 spec 12：拖拽编辑器八项增强与 entry 指定

> 状态：**已实施（2026-09-23）**——首版代码 `9af8602`（七文件，+1243/-79）、人工测试与任务勾选 `feee5b8` 已提交；2026-09-23 用户裁定「都修改」后的遗留修订（entry 三入口并存、删起始迁移语义收紧、readonly 起始角标、检查器禁用补齐、contracts 按实际签名补录）随本批提交。规划产物 `specs/012-workflow-editor-enhancements/`（`fe73fb5`）。本篇为回溯性 changelog，实现事实以代码与 specs/012-workflow-editor-enhancements/ 为准。
> 上位契约：[impl_spec_09_frontend.md](./impl_spec_09_frontend.md)（画布 / 检查器 / graph.ts 基座）、[impl_spec_10_frontend_detail_edit.md](./impl_spec_10_frontend_detail_edit.md)（双模式编辑器三宿主与 Schema 行表单）、[api_contract.md](./api_contract.md) §3（config 键集——本篇只读消费零改动）。
> 前置依赖：spec 011 已合入（`3c82c3f`，LLM `system_prompt` 后端）；spec 010 已合入（双模式编辑器 / 两步式创建）。
> 迁移假设：**零迁移、零后端改动、零新增第三方依赖**——git 变更禁触 `internal/` 与 `migrations/`。

## 1. 背景

spec 009/010 交付的拖拽编辑器只有骨架：编辑节点只能进 JSON 模式、LLM/API 节点缺一半配置面（System Prompt / Headers / Auth / Body 均无处可填）、entry 只能改 JSON、key 改名要走删除重建。本篇把拖拽模式补成可独立完成编排全流程的编辑面。

## 2. 做什么（范围）

- 检查器可见删除入口：节点 / 连线选中即出删除按钮，走画布既有 remove 级联链（FR-001）。
- 画布内 schema 配置：左面板「入参 / 出参」按钮置伪节点上下文 → 检查器切 schema 面板（task 两段行表单 / chat 只读说明），与宿主页表单同源（FR-002）。
- entry 指定三入口并存（2026-09-23 裁定后最终形态）：左面板「起始节点」下拉（选项 `key（类型名）`）/ 双击节点 / 检查器「设为起始」按钮，全部汇画布 `startKey` 单源；readonly 态主色边框 + 纯 CSS「起始」角标（FR-002）。
- key 改名：检查器输入框 + 非空 / ≤64 / 不冲突校验拦截，`renameNodeKey` 纯函数五级联应用（FR-003）。
- LLM 双文本域：System Prompt（可选）+ Prompt，消费 spec 011 后端（FR-004）。
- API 全要素：Method / URL / Headers KV 行 / Auth 预设（Bearer / Basic → Authorization 行）/ Body（POST）（FR-005）。
- 子工作流入参自动渲染：input_schema 字段行 + 切换保留同名值 + 下拉排除自身（FR-006）。
- 变量引用下拉分组（input / 祖先输出 / 子流程出参）+ `TemplateField` 哑组件统一接入五类模板字段（FR-007/008）。

## 3. 不做什么（边界）

- 不改提交载荷契约：图主体（start_node_key/nodes/edges）形态零变化，config 新增读写键（`system_prompt`/`headers`/`body`/`inputs`）均为 api_contract §3 既有键。
- 不做 key 改名的模板文本追改：`{{old_key}}` 引用由保存期后端 R10 兜底（spec Assumptions 既定）。
- 不做 readonly 态任何编辑入口（面板 / 检查器不渲染，三入口均守卫）。
- 不做子流程 schema 的跨会话缓存（NodeInspector 模块级 `Map` 会话级缓存，不进 store）。

## 4. 关键决策与保真不变量

| 决策 | 内容 |
|---|---|
| entry 方案演进 | 伪「开始」节点（连线指定）→ 2026-09-23 裁定取消 → 下拉单入口 → 用户「都修改」裁定恢复双击 + 按钮成三入口并存。双击 / 按钮经画布 `onSetStart`（readonly 守卫 + 改值 + 重刷 class）；下拉保持 `v-model` 直写 + `@change` 刷 class——v-model 先赋值会触发 `onSetStart` 同值守卫短路，不可走同一函数。 |
| 删起始迁移 | `migrateStartKey(startKey, removedKey, remainingKeys, remainingEdges)`：新起始 = 剩余中首个无入边者（Set 收集 remainingEdges 的 target）；全有入边（成环）回退剩余首个；空集 → `''`。 |
| 伪节点 sentinel | `START_NODE_ID`（`__start__`）+ `START_NODE_TYPE` 双条件判定，不进 nodes 数组、不进图主体序列化。 |
| Auth 无独立键 | `config.headers.Authorization` 行承载；`parseAuthorization`/`formatAuthorization` 纯函数编解码，切换预设清旧行零残留。 |
| 变量源 | `ancestorsOf` 反向 BFS 祖先集 ∪ input 展开 ∪ 子流程 output_schema 展开；子流程 schema 会话级 `Map` 缓存。 |
| readonly 角标纯 CSS | `canvas-editor--readonly` 容器 class + 节点 wrapper `::after`「起始」——不改 label（`getGraph` 序列化含 label，改了会泄进只读 JSON 文本）；样式全用既有 token（冻结合规）。 |

**保真不变量**：`getGraph`/`serialize` 输出形态零变化（伪节点结构上不可能泄入）；config 键集 ⊆ api_contract §3；未知 config 键透传不受影响；POST/PUT 双轨隔离与 PREFILL 深拷贝维持。

## 5. 交付物

| 批次 | 交付物 |
|---|---|
| 首版 `9af8602` | `web/src/views/workflow/TemplateField.vue`（新组件：textarea + 变量下拉光标插入）；`NodeInspector.vue`（动作区 / 五类节点表单 / 伪节点 schema 面板 / 变量计算，+763 行）；`CanvasEditor.vue`（entry 下拉 / 伪节点上下文 / 删除与改名接线）；`GraphModeEditor.vue`（schema 同源透传）；`WorkflowEdit.vue` / `WorkflowOrchestrate.vue`（宿主单源接线）；`graph.ts`（`renameNodeKey`/`ancestorsOf`/Auth 编解码/`migrateStartKey`，+167 行） |
| 首版 `feee5b8` | `docs/testing/workflow-frontend-manual-test.md` §8 增补（六场景 + 八项缺口 + 载荷回读 + EC 全数复测）；`specs/012.../tasks.md` 勾选 |
| 修订批（随本批提交） | `CanvasEditor.vue`（双击入口 + `startOptionLabel` + readonly 角标 CSS + `migrateStartKey` 新语义调用点）；`NodeInspector.vue`（「设为起始」按钮 + `set-start` emit + 模型 / method / 子工作流三处 `:disabled` 补齐）；`WorkflowOrchestrate.vue` + `stores/workflowCreateDraft.ts`（schema 写回 action 化：`saveInputSchema`/`saveOutputSchema`）；`specs/012.../spec.md`·`tasks.md`·`contracts/frontend-components.md` 同步（Revision 行三入口裁定、contracts 按实际签名补录） |

## 6. 验收门

- 自动化：`cd web && npm run type-check && npm run build` 全绿（首版与修订批各过一次）。
- 人工：manual-test §8 六场景 + EC-1~23 全数复测 + 载荷回读逐键核对（SC-003/SC-006）。
- 修订批新行为的人工复验项：§8.1 步骤 2 删起始迁移语义（A→B→C 删 A 起始 = B）、§8.5 步骤 1 下拉选项 `key（类型名）` 形态、§8.5 步骤 5 readonly 边框 + 角标、三入口等价性（§5 EC-5）。
