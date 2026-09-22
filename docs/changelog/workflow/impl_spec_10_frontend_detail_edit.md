# Workflow 实现 spec 10：工作流详情 / 编辑前端（查看 + 更新 + 创建两步式重构 + Schema 表单化）

> 状态：**已实施并合入（2026-09-22）**——三笔交付：`3e06117`（US1+US3 详情只读页与 Schema 行编辑器）/ `f4cdd8c`（US2 编辑页）/ `8a47b60`（US4 创建两步式重构 + 文档同步）；规划产物 `4c129a8`。本篇为回溯性 changelog，实现事实以代码与 [specs/010-workflow-frontend-detail-edit/](../../../../specs/010-workflow-frontend-detail-edit/)（spec/plan/research/data-model/contracts/quickstart/tasks）为准。
> 上位契约：[impl_spec_09_frontend.md](./impl_spec_09_frontend.md)（画布 / 检查器 / graph.ts / api 层，本篇改造复用而非重写）；[api_contract.md](./api_contract.md)（GET /workflows/:id 详情契约、PUT /workflows/:id 整图替换语义——**type 不可变携带即拒、编辑不降级 status**，后端已冻结，本篇只读核实零改动）；前端规约 [web/README.md](../../../../web/README.md)。冲突时停下来问用户。
> 前置依赖：spec 009 合入（commit `c37a89b`）。
> 迁移假设：**零迁移、零后端改动、零新增第三方依赖**——git 变更禁触 `internal/` 与 `migrations/`。

## 1. 背景

spec 009 交付后用户验收反馈五项交互缺口：① 无查看已有工作流的入口；② 创建选拖拽时应点「创建」后进整页编排（画布嵌在表单里太挤）；③ 需支持更新与查看详细编排（更新同样双模式）；④ 已有配置需能按需渲染成 JSON 与画布；⑤ 入参/出参 Schema 应表单化而非只填 JSON 字符串。

用户拍板四项方向：**详情与编辑分离**（`/workflows/:id` 只读 + `/workflows/:id/edit` 整页）、整页编排用**独立路由页**（创建第二步与编辑页共用同一形态）、Schema **纯表单**（不加 JSON 模式）、走完整 spec 流程。后端 GET/PUT 契约已冻结，本篇纯前端接入。

## 2. 做什么（范围）

1. **详情页 `/workflows/:id`（只读）**：基础信息（名称/描述/类型/状态/创建时间）+ task 型入参/出参 Schema 只读表（chat 型不显示）+ 图编排只读双模式（默认画布可切 JSON 美化文本，两模式内容一致）；画布只读 = 平移缩放可用，拖入/删除/连线/双击设起始/检查器编辑全禁；「编辑」按钮；404 → 错误提示回列表。
2. **编辑页 `/workflows/:id/edit`（整页 fullBleed）**：GET 回填（基础信息 + 图编排双模式 + task 型 Schema 表单）→ PUT 整图替换保存 → 成功回详情页、失败留页内容不丢；**type 单选禁用态**（「类型不可变，换型需删除重建」tooltip——PUT 请求体不得携带 type 键，三重闸：类型层 / 组装层 / 消费层）；**脏态守卫双通道**：路由跳转守卫 + beforeunload，仅编辑内容有变化时拦（规范化比对出口：基准与当前都走 parse→serialize，格式差异不计 dirty）。
3. **Schema 表单化**：四字段行编辑器（name / type 下拉 string-number-boolean / required 开关 / description）+ 增删行；前端校验对齐后端 ValidateSchemaFields（name 非空不重名、type 限三值），错误文案带行号、非法拦截不发请求；非法历史数据（type 越界）行内下拉显示原值兜底、保存前拦截；创建第一步与编辑页共用同一组件。
4. **创建流程两步式重构**：第一步 `/workflows/create` 纯表单（名称/描述/类型 + task 型 Schema 表单）→ 第二步 `/workflows/create/orchestrate` 整页编排（JSON/拖拽切换，初始 = 预填智能客服分类示例图）→「保存并创建」一次 POST（**带 type——与 PUT 不带 type 双轨隔离**）成功跳列表；「上一步」回第一步内容保留（Pinia 内存态草稿 store 双向回写）；直访/刷新第二步 → 回第一步（中间态不持久化）。
5. **复用与改造**：009 的 CanvasEditor 增只读态与整页形态、JsonConfigEditor 复用只读美化、GraphModeEditor 容器三处共用（创建第二步 / 详情只读 / 编辑回填）；graph.ts 增详情转换（detailToGraphConfig）、Schema 校验（schemaFieldsError）、更新组装（buildUpdatePayload）。

## 3. 不做什么（边界）

- 工作流执行 / 试运行 UI、版本历史 / 配置 diff。
- 详情页画布的节点交互编辑（仅渲染 + 平移缩放）。
- Schema 的 JSON 文本模式（纯表单）。
- 并发编辑冲突检测（后端整图替换语义：后保存者覆盖，不做提示）。
- 任何后端契约改动、任何新增第三方依赖。

## 4. 关键决策与保真不变量

六条保真不变量（编辑往返 SC-003 的根基）：① 未知 config 键引用直传透传；② condition null ↔ 键省略双向等价；③ 外键字符串保形零转换；④ nodes[].name 空串 ↔ 键省略双向等价；⑤ **type 双轨隔离**（POST 带 / PUT 不带）；⑥ PREFILL 深拷贝防画布编辑污染模块常量。

| # | 事项 | 拍板 |
|---|---|---|
| 1 | 创建第二步初始图 | 预填智能客服分类示例图（clarify 用户选 A），不需要时清空重编 |
| 2 | 脏态守卫实现 | on-demand 现场序列化比对（baseline snapshot + isDirty() 触发时点比对）——弃 `watch(dirty)` 方案（检查器深改共享 config 引用 watch 不到，stale dirty 会漏拦刷新） |
| 3 | 创建草稿传递 | Pinia 内存态 store（不落 localStorage）——刷新/直访第二步 state 归零 → 守卫回第一步，语义天然成立 |
| 4 | 路由形态 | fullBleed 整页（route meta → App.vue el-main 去 padding）；`/workflows/create/orchestrate` 静态段优先于 `:id` 不被吃掉 |
| 5 | 操作列 | 查看/编辑/发布或停用/删除四链接平铺，列宽 220（不收纳） |

## 5. 交付物

| 层 | 交付物 |
|---|---|
| api | `workflow.ts` 增 WorkflowDetail / UpdateWorkflowData 类型 + `getWorkflowDetail` / `updateWorkflow`（PUT 不带 type） |
| 视图 | `WorkflowDetail.vue`（新建，只读）；`WorkflowEdit.vue`（新建，整页编辑 + 脏态守卫）；`SchemaFieldsEditor.vue`（新建，行编辑器）；`WorkflowCreate.vue`（重写 293→~130 行，第一步纯表单）；`WorkflowOrchestrate.vue`（新建，第二步整页）；`WorkflowList.vue`（操作列加查看/编辑） |
| 状态 | `stores/workflowCreateDraft.ts`（两步式草稿，内存态） |
| 图逻辑 | `graph.ts`：detailToGraphConfig / schemaFieldsError / buildUpdatePayload 增；parseSchemaFields 删（JSON 文本域退役）；buildCreatePayload 空数组收紧（`?.length` 判空不携带） |
| 路由 | 三条新路由：`/workflows/:id`、`/workflows/:id/edit`（fullBleed）、`/workflows/create/orchestrate`（fullBleed） |
| 文档 | 手测文档增补 §7（四场景 + EC-12~EC-23 十二项，编号接续 009 的 EC-1~11）；web/README.md 目录结构 |

## 6. 验收门

- 自动化门：`cd web && npm run type-check && npm run build` 全绿；`go build ./... && go vet ./...` 回归绿；git 变更零触 internal/ 与 migrations/；package.json 全程未动。
- 人工冒烟（workflow-frontend-manual-test.md §7）：创建两步式全链路（含 Schema 校验行号拦截 / store 回写 / 直访回第一步）、详情只读双模式、编辑往返一致（不改直存配置不变，含未知键与条件标签）、409 / 图非法 400 留页、type 禁改、脏态守卫双通道、已发布编辑不降级、双模式三处一致性抽查（SC-002）、Edge Cases 12 项。
- 遗留人工项（实现期无法机器判）：EC-16 非法历史 type 需 DB 直改造数（后端 ValidateSchemaFields 会拒 API 造数）；beforeunload 原生确认弹窗实测；009 创建链路在两步式重构后的运行时回归。
