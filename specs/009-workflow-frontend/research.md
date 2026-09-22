# Research: 工作流管理前端（009-workflow-frontend）

**Date**: 2026-09-22 | **Status**: Complete | **Plan**: [plan.md](plan.md)

本文记录 Phase 0 选型与模式决策。所有决策对齐 [spec.md](spec.md) FR 与 Clarifications（2026-09-22 会话），不引入 spec 外概念。

## 1. 画布库：@vue-flow/core + background + controls

- **Decision**: 引入 @vue-flow/core 1.48.2、@vue-flow/background 1.3.2、@vue-flow/controls 1.11.3（实现期以 package.json 锁定为准），core 负责节点/连线/拖拽/视变换，background 提供网格背景（空间参照感），controls 提供缩放/适应/居中按钮。配套引 core 的默认样式（`@vue-flow/core/dist/style.css` + `theme-default.css`，按 web/README.md 既有样式引入方式挂 main.ts）。
- **Rationale**: Vue 3 生态事实标准（reactflow 的 Vue 移植），MIT；声明式 `v-model:nodes` / `v-model:edges` 与 Vue 3.5 响应式天然契合；官方 DnD 示例成熟；节点数 <30 的规模远低于其性能边界。
- **Alternatives**: AntV X6（编辑器内核定位、上手与定制成本高）；LogicFlow（Dify 同款但 Vue 3 绑定弱、以原生 DOM 事件为中心）；手写 SVG（零依赖，但拖拽 + 贝塞尔连线 + 缩放平移视变换的工作量与维护成本一人不可承受——已被用户 2026-09-21 拍板的「完整双模式 + 引入成熟库」否决）。

## 2. DnD 交互：官方 Drag & Drop 示例路径

- **Decision**: 左侧节点面板项用原生 HTML5 drag（`dragstart` 时 `dataTransfer` 携带节点类型）；画布容器 `drop` 事件里取 `useVueFlow().screenToFlowCoordinate(event)` 换算落点坐标后 `addNodes`；连线用 Vue Flow 内建 handle 拖拽（`connect` 事件）。
- **Rationale**: 官方示例同款路径，避免自研坐标换算；`screenToFlowCoordinate` 天然处理缩放/平移后的坐标变换。实现期以 @vue-flow 官方文档为准核对 API 名（版本若微调以锁定版本文档为准），此处只钉模式不钉签名。
- **Alternatives**: 点击面板项在画布中心新增节点（无拖拽语义，背离需求简图）；pointer 事件自研拖拽（重复造轮子）。

## 3. JSON 编辑器：原生 textarea + monospace

- **Decision**: `el-input type="textarea"` + 等宽字体 token，`JSON.parse` 校验、`JSON.stringify(obj, null, 2)` 格式化；非法时 ElMessage 错误（含 SyntaxError 位置信息可用则带）且不改动原文。
- **Rationale**: 需求只有「编辑 + 格式化 + 合法性校验」三件事，textarea 全覆盖；内部工具不需要语法高亮/行号。
- **Alternatives**: CodeMirror 6（语法高亮体验好，但新依赖 + 按需集成成本，超出需求——若引入须再过软门禁）；contenteditable div（自研光标/选区管理，不可维护）。

## 4. llm 节点模型下拉数据源：AgentList 先例复用

- **Decision**: 复用 [web/src/views/agent/AgentList.vue](../../../web/src/views/agent/AgentList.vue) 的 `loadModelOptions` 模式——`getProviderList()` 遍历 + 逐个 `getModelList(p.id, { page: 1, page_size: 100 })`，结果扁平化为 `{providerId, model}` 列表，`capability === 'chat'` 过滤后供 llm 节点选择（下拉项显示 `providerName / modelName`）。
- **Rationale**: 后端无全局模型列表端点（模型归属 provider），前端零改动约束下只能走既有先例；AgentList 已验证该模式可行。
- **Alternatives**: 新增后端全局模型端点（违背纯前端交付红线）；只列第一个 provider 的模型（功能残缺）。

## 5. workflow 节点子工作流下拉：前端过滤 task 型

- **Decision**: `getWorkflowList({ page: 1, page_size: 100 })` 拉全量后前端过滤 `type === 'task'`（后端 R11：嵌套目标仅 task 型）；下拉项显示工作流名。
- **Rationale**: workflows 是极小配置表（偏移分页例外表），单页 100 足够；避免为过滤加后端参数。
- **Alternatives**: 后端加 type 过滤参数（零改动红线）；不过滤靠后端 400 兜底（用户体验差，明知非法还给选）。

## 6. 画布布局与节点位置：自动列布局 + 会话内存

- **Decision**: JSON→画布时无位置信息，按 nodes 数组序自动网格布局（`x = col * 260, y = row * 120`，自左向右换行）；拖动后的位置存组件内 `Map<key, {x, y}>`，同会话内 JSON↔画布往返保留位置；刷新即丢（spec 明确不做草稿持久化）；位置**不序列化**进配置 JSON（后端契约无此字段）。
- **Rationale**: 后端 config 不含位置字段，序列化进去即契约违规；往返保留避免每次切换模式节点跳回原位。
- **Alternatives**: 位置写进节点 config（污染后端契约、未知键透传语义混淆）；localStorage 持久化（spec 明确不做）。

## 7. 节点渲染：默认节点 + 类型 class

- **Decision**: 用 Vue Flow 默认节点（DefaultNode）+ 每节点挂 `class: 'hf-wf-node-{type}'`，CSS 按 class 差异化边框/头部色（只引 `--hf-*` token）；label 显示节点 name（缺省显示类型中文名）；起始节点额外 class `hf-wf-node--start` 加视觉标识（边框强调）。
- **Rationale**: 五类节点配置面板才是差异化主体，画布节点本身用默认形状 + class 着色已满足辨识需求；自定义节点组件是画布高级能力（spec 明确不做的范围精神）。
- **Alternatives**: CustomNode 组件每类型一个（工作量 ×5，收益边际）。

## 8. 外键数值化时点：提交组装统一转换

- **Decision**: 画布编辑与 JSON 文本中 `config.model_id` / `config.workflow_id` 保持字符串形态（与后端响应一致）；仅在提交组装请求体时 deep-walk nodes 统一 `Number()` 转数值（FR-011）。
- **Rationale**: 单一转换点避免模式切换时来回转换的精度/状态问题；字符串↔数值边界只出现在 api 层组装处。
- **Alternatives**: 选择即转数值（双模式共享模型里 JSON 序列化会出现数字，与后端 GET 返回的字符串形态不一致，往返比对困惑）。

## 9. 图配置纯逻辑收敛：graph.ts

- **Decision**: [web/src/views/workflow/graph.ts](../../../web/src/views/workflow/graph.ts) 收敛全部纯逻辑（无 Vue 依赖）：`GraphConfig` 类型、预填示例常量、节点 key 生成（`${type}_${n}` 计数器保证唯一）、起始节点迁移规则（删起始节点 → 迁移到剩余首节点；清空 → 置空）、JSON↔画布模型双向转换、自动布局坐标计算、提交组装（含外键数值化）。
- **Rationale**: 解析/序列化/规则是本篇全部业务逻辑，与渲染解耦后组件只剩交互绑定；未来若引入前端测试，此文件是天然单测面。
- **Alternatives**: 逻辑散在组件里（双向转换逻辑重复、无法集中审查往返一致性 SC-006）。

## 10. 预填示例：手动测试文档 §4 的图配置形态

- **Decision**: 预填「智能客服分类」示例取自 [docs/testing/workflow-manual-test.md](../../../docs/testing/workflow-manual-test.md) §4 的创建示例（llm→end 线性图，chat 型，model_id "1"，prompt 含 `{{input}}` 模板）——只取图配置部分（start_node_key/nodes/edges），name/description 由表单控件默认值（名称空、描述空）持有，type 默认 chat（spec Clarifications：JSON 不含 type）。
- **Rationale**: 与后端手测文档同源，冒烟时前后端示例对得上；对齐用户原始字段清单。
- **Alternatives**: 自造示例（与手测文档脱节，冒烟核对成本高）；预填含 type 的完整请求体（双入口冲突，已被 Clarifications 否决）。
