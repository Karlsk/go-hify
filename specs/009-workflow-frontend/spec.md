# Feature Specification: 工作流管理前端（列表页 + 创建页，JSON 与可视化拖拽双模式）

**Feature Branch**: `009-workflow-frontend`

**Created**: 2026-09-22

**Status**: Draft

**Input**: 用户原始需求（两页：/workflows 列表、/workflows/create 创建；配置支持 json 与拖拽两种形式，预填智能客服分类示例；格式化按钮；JSON 合法性校验；提交成功跳回列表）。范围拍板（2026-09-21）：**完整双模式**——引入可视化拖拽画布（@vue-flow，用户已批准），同步修订 CLAUDE.md《不做什么》与宪法 Principle I 的「不做可视化工作流拖拽编排」条目。后端 API 参考 [docs/testing/workflow-manual-test.md](../../../docs/testing/workflow-manual-test.md)（CRUD 8 端点）与 [workflow-engine-manual-test.md](../../../docs/testing/workflow-engine-manual-test.md)（分型/嵌套矩阵）。前端规约以 [web/README.md](../../../web/README.md) 为准。

## Clarifications

### Session 2026-09-22

- Q: 本期列表操作列是否顺带做「发布/停用」开关？ → A: 加（用户拍板「加发布/停用」）——发布/停用纳入本期交付（FR-014）；执行入口与执行历史仍不做（明确不做清单同步修订）。

默认决策（未问、按用户原始字段结构与仓内惯例定，第 5 步任务比对停点可推翻）：

- JSON 编辑器只管图配置（start_node_key/nodes/edges[/schema]）；name/description/type 由表单控件持有、JSON 不含 type——对齐用户原始字段清单「名称/描述/工作流配置」，避免 type 双入口编辑冲突。
- condition 出边的 condition 标签：选中连线时在右侧面板编辑。
- 拖拽模式起步为空画布（不预置节点）；列表加「类型」列（chat/task，spec 08 起一等维度）。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 管理员在列表页管理工作流（Priority: P1）

管理员登录后从侧边栏进「工作流管理」，表格看到全部工作流的名称、类型（chat/task）、状态（草稿/已发布/已停用三态）、创建时间，偏移分页；删除不用的（二次确认 → 成功后列表刷新），被 Agent 绑定的工作流删除失败收到明确提示（WORKFLOW_IN_USE）；发布/停用切换生命周期（draft→published、published→disabled、disabled 可重新发布）；右上角「新建工作流」跳转创建页。

**Why this priority**: 列表页是工作流管理的最小闭环（查看 + 清理 + 创建入口）——没有它创建页无回环、存量工作流不可见；且不依赖任何编辑器能力，风险最低。

**Independent Test**: 用后端手测文档先造几条工作流数据后，列表页独立完成：三态展示、分页、删除成功、删除 409、发布/停用轮转、跳转创建页。

**Acceptance Scenarios**:

1. **Given** 已登录且存在工作流数据，**When** 进入 /workflows，**Then** 表格展示名称、类型、状态、创建时间，偏移分页可用。
2. **Given** 任意未被引用的工作流，**When** 点删除并在确认框点确认，**Then** 行消失（表格刷新）。
3. **Given** 被 Agent 绑定的工作流，**When** 删除并确认，**Then** 错误提示（WORKFLOW_IN_USE），行保留。
4. **Given** 列表页，**When** 点右上角「新建工作流」，**Then** 跳转 /workflows/create。
5. **Given** draft 态工作流，**When** 点发布并确认，**Then** 状态 tag 变「已发布」（刷新后仍 published）。
6. **Given** published 态工作流，**When** 点停用并确认，**Then** 状态 tag 变「已停用」；disabled 态可再点发布回到「已发布」。

---

### User Story 2 - 管理员用 JSON 模式创建工作流（Priority: P2）

管理员在创建页填名称（必填）、描述（可选）、类型（chat/task 必选，默认 chat；task 型展开 input_schema/output_schema 编辑），JSON 编辑器已预填智能客服分类工作流示例；「格式化」按钮美化缩进（非法 JSON 提示错误且不破坏原文）；提交前做 JSON 合法性校验，非法阻断提交；合法提交成功后提示并跳回列表页。

**Why this priority**: JSON 是后端配置的原生形态，最小成本打通创建链路；P1 + P2 即构成本篇最小可用交付（MVP）。

**Independent Test**: 纯 JSON 模式走通「预填示例 → 改名称 → 提交 → 列表见新行」全链路，不碰画布。

**Acceptance Scenarios**:

1. **Given** 进入创建页，**When** 页面加载完成，**Then** 表单（名称/描述/类型）就绪，JSON 编辑器预填智能客服分类示例（llm→end 线性图、chat 型）。
2. **Given** JSON 内容非法，**When** 点「格式化」，**Then** 错误提示，编辑器原文不变。
3. **Given** JSON 内容非法，**When** 提交，**Then** 阻断提交并提示错误位置。
4. **Given** 表单完整且 JSON 合法，**When** 提交，**Then** 成功提示 + 跳回 /workflows + 列表出现新行。
5. **Given** 类型选 task，**When** 表单刷新，**Then** input_schema/output_schema 编辑区出现（可不填）。
6. **Given** 名称与既有工作流重复，**When** 提交，**Then** 409（WORKFLOW_NAME_CONFLICT）提示，留在创建页。

---

### User Story 3 - 管理员用拖拽模式可视化编排（Priority: P3）

管理员切到拖拽模式（JSON 非法时阻断切换、提示先修复），左侧节点面板列出后端支持的节点类型（llm/end/condition/api/workflow），拖节点进画布、拖连线建立执行顺序、删除节点/连线；画布按当前配置渲染既有节点；点击节点在右侧面板配置参数（按类型分化：llm 选模型填 Prompt、end 填输出、condition 填表达式、api 填 URL/方法、workflow 选子工作流填 inputs）；起始节点默认第一个放入的节点、可改；切回 JSON 模式看到等价配置。

**Why this priority**: 可视化编排是本篇的差异化能力（需求简图核心形态），但依赖 P2 建立的配置数据模型与提交链路；排序在数据模型之后。

**Independent Test**: 切拖拽模式 → 拖入 llm + end 两节点 → 连线 → 配置 llm 节点（选模型、填 Prompt）→ 切回 JSON 模式核对等价 → 提交成功。

**Acceptance Scenarios**:

1. **Given** JSON 模式内容合法，**When** 切到拖拽模式，**Then** 画布按当前配置渲染节点与连线。
2. **Given** 画布，**When** 从左侧面板拖入节点并拖拽连线到另一节点，**Then** 画布出现新节点与新连线。
3. **Given** 画布中某节点，**When** 点击，**Then** 右侧面板出现该节点的配置表单（按节点类型分化）。
4. **Given** 画布完成编辑，**When** 切回 JSON 模式，**Then** 编辑器内容反映画布编辑结果（往返语义等价，未知 config 键保留）。
5. **Given** JSON 模式内容非法，**When** 切拖拽模式，**Then** 阻断切换并提示先修复。

---

### Edge Cases

- 删除被 Agent 绑定的工作流：409 WORKFLOW_IN_USE 由请求拦截器统一提示，列表行保留、不误删。
- 创建名称冲突：409 WORKFLOW_NAME_CONFLICT，留在创建页（编辑内容不丢）。
- JSON 非法的三个动作全部阻断且不破坏原文：格式化（提示错误位置）、切拖拽模式（提示先修复）、提交（阻断并提示）。
- 空画布/无节点提交：前端阻断（至少一节点 + 起始节点必有），后端图校验 400 兜底。
- 删除起始节点：start_node_key 迁移到剩余节点中的第一个；画布清空后 start_node_key 置空待重定。
- chat 型隐藏 schema 编辑区（后端强不变量：chat 型携带非空 schema 即 400，前端不给用户制造这个错误的机会）。
- 节点 config 含画布表单未覆盖的额外键（如手写 JSON 加的自定义键）：模式往返透传保留，画布表单只改已知字段。
- 模型下拉数据源为空（未配 provider/model）：llm 节点配置面板提示先去配模型，不阻断整体提交（后端 model_id 存在性预检 400 兜底）。
- 子工作流下拉只列 task 型（后端 R11：嵌套目标仅 task 型）。
- 停用被 Agent 绑定的工作流：后端允许（仅删除受 RESTRICT 挡），停用后绑定会话将 503 WORKFLOW_NOT_PUBLISHED——停用确认框文案提示该后果，属管理员有意行为。
- 未登录访问 /workflows 或 /workflows/create：既有路由守卫跳登录（带 redirect 回跳）。

## 明确不做（边界）

- 不做已有 workflow 的编辑页（PUT 端点本期前端不接，列表无编辑入口）
- 不做执行的操作入口与执行历史查看（execute 端点本期前端不接；发布/停用经 clarify 拍板纳入本期，见 FR-014）
- 不做列表页跳详情、画布只读回显已有 workflow
- 不做画布高级能力：撤销/重做、小地图、自动布局、框选、对齐（仅拖入/移动/连线/删除节点/删除连线）
- 不做画布状态持久化草稿（刷新丢失可接受，配置数据以 JSON 模式序列化形态为准）
- 不改任何后端代码与契约（纯前端交付）

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: 列表页路由 /workflows + 侧边栏菜单「工作流管理」入口 + 创建页路由 /workflows/create + 创建页返回按钮；两页 meta.title 进面包屑与 document.title（对齐 web/README.md 路由约定）。
- **FR-002**: 列表表格列：名称、类型（chat/task，spec 08 起一等维度）、状态、创建时间、操作（删除/发布/停用）；偏移分页（page/page_size，默认 20，HifyTable 数据源 getList）。
- **FR-003**: 状态三态映射：draft→草稿、published→已发布、disabled→已停用，差异化 tag 展示（后端实际返回三态小写值 [internal/workflow/api/schema.go](../../../internal/workflow/api/schema.go)——用户原始描述只列 DRAFT/PUBLISHED，以 API 为准补全 disabled）。
- **FR-004**: 删除流程：二次确认（红色确认、提示不可恢复）→ 成功刷新表格；被 Agent 绑定时 409 WORKFLOW_IN_USE 由拦截器统一弹错（业务层不重复弹）。
- **FR-005**: 创建表单：名称（必填、非空去空格，长度上限交后端校验）、描述（可选）、类型（chat/task 单选必填、默认 chat——spec 08 起后端 Create 必填 type，用户原始字段清单未列、以 API 为准补入）。
- **FR-006**: task 型展开 input_schema / output_schema 编辑（JSON 数组形态，字段 [{name,type,required,description}]），复用 JSON 校验与格式化；可不填（后端允许无 schema 的 task 型）；chat 型不展示。
- **FR-007**: 双模式共享单一图配置数据源（start_node_key/nodes/edges）：type/name/description 由表单控件持有、JSON 编辑器不含 type（单一入口，避免双处编辑冲突——对齐用户原始字段清单「名称/描述/工作流配置」）；input_schema/output_schema 同样由表单区独立编辑持有、不进 JSON 编辑器文本（见 FR-006）；JSON 模式 = 图配置（start_node_key/nodes/edges）的序列化形态；切画布时解析 JSON 为结构模型（非法即阻断）；切回 JSON 时重新序列化更新编辑器；未知 config 键往返透传保留。
- **FR-008**: JSON 模式：textarea 类编辑器预填智能客服分类工作流示例（对齐 workflow-manual-test.md §4：llm→end 线性图、chat 型）；「格式化」按钮美化缩进（非法 JSON 提示错误且原文不变）；提交前合法性校验，非法阻断提交并提示。
- **FR-009**: 拖拽模式画布：左侧节点面板五类（llm/end/condition/api/workflow，对齐后端 NodeType 全集）；拖入画布、拖拽连线、删除节点/删除连线、节点可移动；起始节点可视化标识（默认第一个放入的节点，可改），对应后端必填 start_node_key。
- **FR-010**: 节点配置面板（点击节点展开，按类型分化）：llm = 模型下拉（数据源：模型列表接口，capability=chat）+ prompt 文本域；end = output 文本域；condition = expression 文本域；api = url + method；workflow = 子工作流下拉（数据源：workflow 列表接口，仅 task 型）+ inputs 键值映射编辑；选中连线时右侧面板可编辑该边的 condition 标签（condition 节点出边用）。
- **FR-011**: 提交组装：请求体 {name, description?, type, start_node_key, nodes, edges, [input_schema, output_schema]}；节点 config 外键（model_id / workflow_id）**保持字符串形态**（后端 NodeConfig 字段带 `,string` tag，值须为字符串——〔2026-09-22 实现期修正〕原「数值化 Number()」系对后端契约的误读，schema.go 与两份手测文档三源一致均为字符串形如 `"model_id":"1"`，数值形反而 400）；成功 notifySuccess + 跳回 /workflows；失败留在当前页（拦截器弹错）。
- **FR-012**: 视觉纪律与空值约定：业务代码只引用 --hf-* 语义 token，禁硬编码色值/圆角/阴影；列表空 []、字符串空 ""（web/README.md 设计系统与字段命名节）。
- **FR-013**: 文档同步：① CLAUDE.md《不做什么》修订「不做可视化工作流拖拽编排」条目（改为：可视化拖拽编排已交付 spec 009 双模式；用户 2026-09-21 已批准）；② 宪法 Principle I 同条目随动同步（宪法治理条款：先 CLAUDE.md 后宪法，版本升 MINOR——范围调整非原则删除）；③ web/README.md 目录结构补 workflow 页与画布组件；④ docs/testing/ 新增 workflow-frontend-manual-test.md 冒烟文档。
- **FR-014**: 列表行生命周期操作：发布（draft/disabled → published）与停用（published → disabled），各带确认框（停用文案提示绑定 agent 的会话将不可用），调用 POST /workflows/{id}/publish 与 /disable（后端幂等）；成功后刷新表格、状态 tag 即时更新。

### Key Entities

- **WorkflowItem（列表行）**: id（字符串）/ name / description / type / status（draft|published|disabled）/ created_at——响应 snake_case，ID 字符串化。
- **WorkflowConfig（配置数据模型，双模式共享）**: type / start_node_key / nodes[{key,type,name,config}] / edges[{source_node_key,target_node_key,condition?}] / input_schema? / output_schema?——**聚合形态**（表单持有字段与图配置的并集，提交组装用）：type/name/description 与 input_schema/output_schema 由表单控件持有（见 FR-005/FR-006）；JSON 编辑器文本只序列化图配置部分（start_node_key/nodes/edges），画布是图配置的图形形态。
- **节点类型五类**: llm（config: model_id + prompt）/ end（config: output）/ condition（config: expression + 出边 condition 标签）/ api（config: url + method）/ workflow（config: workflow_id + inputs 映射）——对齐后端 NodeType 全集与 config 形态。
- **画布模型 ↔ 配置模型映射**: 画布节点/连线 ↔ WorkflowConfig.nodes/edges 双向转换；节点 key 由前端生成并保持唯一。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 前端门禁全绿：`cd web && npm run type-check && npm run build` 零错误。
- **SC-002**: 后端零改动：git 变更不触及 internal/ 与 migrations/；`go build ./...` / `go vet ./...` 回归绿。
- **SC-003**: 视觉纪律可检：新增前端源码（tokens.css / theme-element-plus.css 除外）grep 无硬编码色值。
- **SC-004**: 人工冒烟通过（docs/testing/workflow-frontend-manual-test.md）：JSON 模式创建成功、拖拽模式创建成功、列表三态展示、删除成功、发布/停用轮转（draft→published→disabled→published）、409 双场景（WORKFLOW_IN_USE / WORKFLOW_NAME_CONFLICT）、非法 JSON 三处阻断。
- **SC-005**: 既有能力回归：未登录访问 /workflows 跳登录（守卫）；既有页面路由不受影响。
- **SC-006**: 双模式往返一致性：JSON→画布→JSON 配置语义等价，未知 config 键保留。

## Assumptions

- 后端 workflow API（spec 01~08）已交付当前分支：8 端点、Create 必填 type、三态 status、ID 字符串化、请求体外键数值化、R11 嵌套校验矩阵；本篇纯前端交付，零后端改动。
- 新第三方依赖 @vue-flow/core 1.48.2（peer vue ^3.3.0，兼容 Vue 3.5）+ @vue-flow/background 1.3.2 / @vue-flow/controls 1.1.3——用户 2026-09-21 已批准引入（plan 阶段钉死最终依赖集）。
- CLAUDE.md 与宪法「不做可视化拖拽编排」条目修订已获用户批准（2026-09-21）；实际编辑在实现期文档同步任务完成，宪法版本升 MINOR。
- web/ 无前端单测基建（package.json 无 test script）——本篇验收 = 类型检查 + 构建 + 人工冒烟文档（spec-dev 前端适配模式，go 覆盖率与迁移项跳过）。
- 画布不做持久化草稿：刷新丢失可接受，配置数据以共享数据模型为准。
- 名称/字段长度等细节校验以后端 binding 为准，前端仅做必填与非空校验（后端 400 兜底）。
- 本 feature 分支自 008-workflow-typing-nesting 头上开出（008 未合 main，前端依赖其 spec 08 API）。
