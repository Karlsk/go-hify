# Workflow 实现 spec 09：工作流管理前端（列表页 + 创建页，JSON 与可视化拖拽双模式）

> 状态：**已实施并合入（2026-09-22）**——交付 commit `c37a89b`（规划产物 `d9e2840`）；本篇为回溯性 changelog，实现事实以代码与 [specs/009-workflow-frontend/](../../../../specs/009-workflow-frontend/)（spec/plan/research/data-model/contracts/quickstart/tasks）为准。范围拍板（2026-09-21，用户）：**完整双模式**——引入可视化拖拽画布（@vue-flow，用户已批准），同步修订 CLAUDE.md《不做什么》与宪法 Principle I 的「不做可视化工作流拖拽编排」条目（宪法版本升 MINOR）。
> 上位契约：[CLAUDE.md](../../../../CLAUDE.md)《接口规范》（分页 / 错误码 / 信封）；[api_contract.md](./api_contract.md)（CRUD 8 端点、Create 必填 type、NodeType 全集与 config 形态）；[db_model.md](./db_model.md)（图校验 R1-R11，前端预检只覆盖其中图骨架两条，其余后端 400 兜底）；前端规约 [web/README.md](../../../../web/README.md)（设计系统、请求信封、路由约定）。冲突时停下来问用户。
> 前置依赖：spec 01-08 全部合入（执行引擎 + chat 管道 + 分型嵌套，commit `af22c68`）。
> 迁移假设：**零迁移**——纯前端交付，git 变更禁触 `internal/` 与 `migrations/`。

## 1. 背景

后端 workflow API（spec 01~08）已冻结交付 8 端点，但控制台没有任何工作流管理界面——工作流只能靠 curl 造数。本篇交付最小可用管理前端：列表页（查看 / 清理 / 生命周期）+ 创建页（JSON / 拖拽双模式），把「配置一个工作流」从手写 JSON 降为可视化操作。

用户原始需求两页 + 双模式预填示例；范围拍板时从「仅 JSON」升级为**完整双模式**——这是宪法 Principle I「不做可视化拖拽编排」的首次偏离，经用户显式批准并同步修订 CLAUDE.md 与宪法（后端契约不变，编排产物仍是同一份 JSON 图配置）。

## 2. 做什么（范围）

1. **列表页 `/workflows`**：表格列 = 名称 / 类型（chat/task）/ 状态（draft/published/disabled 三态 tag）/ 创建时间 / 操作；偏移分页（HifyTable + getList）；删除（二次确认，被 Agent 绑定 409 WORKFLOW_IN_USE 拦截器统一弹）；**发布 / 停用**（clarify 拍板纳入本期：draft→published→disabled→published 轮转，停用确认框提示绑定会话将不可用）；「新建工作流」入口。
2. **创建页 `/workflows/create`**：表单 = 名称（必填）/ 描述 / 类型（chat/task 单选默认 chat）；task 型展开 input_schema / output_schema 编辑（JSON 数组形态）；**双模式共享单一图配置数据源**（start_node_key/nodes/edges——type/name/description 与 schema 由表单控件持有，JSON 编辑器不含 type，单一入口避免双处编辑冲突）。
3. **JSON 模式**：预填智能客服分类示例（llm→end 线性图、chat 型，对齐 workflow-manual-test.md §4）；「格式化」按钮（非法 JSON 提示错误位置、原文不变）；提交前合法性校验。
4. **拖拽模式**（@vue-flow/core + background + controls，用户 2026-09-21 批准引入）：左侧节点面板五类（llm/end/condition/api/workflow，对齐后端 NodeType 全集）；拖入 / 连线 / 移动 / 删除；起始节点可视化标识（默认首个放入，可改）；右侧检查器按类型分化（llm = 模型下拉 + Prompt、end = output、condition = expression、api = url/method、workflow = 子工作流下拉仅 task 型 + inputs 映射）；选中连线编辑 condition 标签。
5. **双模式等价**：切画布时解析 JSON（非法阻断切换）；切回 JSON 重新序列化；未知 config 键往返透传保留；节点 config 外键（model_id / workflow_id）**全链路字符串保形**（后端 NodeConfig 带 `,string` tag，数值形 400——〔实现期修正〕原 plan 的「数值化」系契约误读，schema.go 与手测文档三源一致均为字符串）。

## 3. 不做什么（边界）

- 已有 workflow 的**编辑页**与**详情页**（PUT / GET detail 端点本期不接）——归 spec 010。
- 执行入口与执行历史查看（execute 端点前端不接）。
- 画布高级能力：撤销/重做、小地图、自动布局、框选、对齐。
- 画布状态持久化草稿（刷新丢失可接受，配置以 JSON 序列化形态为准）。
- 任何后端代码与契约改动。

## 4. 关键决策

| # | 事项 | 拍板 |
|---|---|---|
| 1 | 拖拽编排引入 | **做**（2026-09-21 用户批准 @vue-flow）——宪法 Principle I 偏离登记，CLAUDE.md《不做什么》同步修订 |
| 2 | 发布/停用 | **纳入本期**（clarify 拍板）——原「不做」清单修订；执行入口仍不做 |
| 3 | 图配置单一数据源 | type/name/description/schema 由表单持有，JSON 编辑器只管 start_node_key/nodes/edges——避免双入口编辑冲突 |
| 4 | 外键形态 | config 内 model_id / workflow_id 全链路字符串（后端 `,string` tag），零转换 |
| 5 | 未知 config 键 | 模式往返透传保留，检查器表单只改已知字段（SC-006） |
| 6 | 非法 JSON | 三处阻断且不破坏原文：格式化 / 切拖拽 / 提交 |

## 5. 交付物

| 层 | 交付物 |
|---|---|
| api | `web/src/api/workflow.ts`——类型 + 列表/创建/删除/发布/停用（对齐后端 api 契约，唯一事实源） |
| 视图 | `WorkflowList.vue`（三态 tag / 删除 / 发布停用 / HifyTable 偏移分页）；`WorkflowCreate.vue`（双模式创建） |
| 图编排组件 | `graph.ts`（纯逻辑：类型 / 解析序列化 / 画布双向转换 / 提交组装）；`GraphModeEditor.vue`（JSON ↔ 拖拽容器）；`JsonConfigEditor.vue`（textarea + 格式化 + 校验态）；`CanvasEditor.vue`（Vue Flow + 五类节点面板）；`NodeInspector.vue`（按类型分化表单 + 连线 condition 编辑） |
| 依赖 | @vue-flow/core ^1.48 + background ^1.3 + controls ^1.1（package.json 新增，用户已批准） |
| 文档 | CLAUDE.md《不做什么》修订 + 宪法 Principle I 同条目（版本升 MINOR）；web/README.md 目录结构；`docs/testing/workflow-frontend-manual-test.md` 新建 |

## 6. 验收门

- 自动化门：`cd web && npm run type-check && npm run build` 全绿；`go build ./... && go vet ./...` 回归绿；git 变更零触 internal/ 与 migrations/。
- 视觉纪律：新增前端源码 grep 无硬编码色值（业务代码只引 `--hf-*` 语义 token）。
- 人工冒烟（workflow-frontend-manual-test.md）：JSON 模式创建、拖拽模式创建、列表三态、删除、发布/停用轮转、409 双场景（WORKFLOW_IN_USE / WORKFLOW_NAME_CONFLICT）、非法 JSON 三处阻断、双模式往返一致（含未知键保留，SC-006）、Edge Cases 11 项。
- 无前端单测基建（不引入测试框架）——验收 = 门禁 + 人工冒烟（spec-dev 前端适配模式，go 覆盖率与迁移项跳过）。
