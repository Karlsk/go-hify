# Research: 前端 agent 绑定工作流与工作流试运行

**Feature**: [spec.md](spec.md) | **Date**: 2026-09-23 | **Status**: 全部 NEEDS CLARIFICATION 已在取证中消解（零残留）

取证基线：`internal/workflow/api/schema.go` L470-563（execute / RunResult / Summary 契约）、`internal/agent/api/schema.go` L31-32 / L114-117 / L145-146（workflow_id 契约）、`internal/chat/service/turn.go` L255-291（O3 无历史）、`internal/workflow/service/execcontext.go` L40-119（`{{input.x}}` 下钻）、`web/src/utils/request.ts`（axios 封装与拦截器）、`web/src/views/workflow/WorkflowEdit.vue`（脏态守卫）、`web/src/views/agent/AgentList.vue`（表单形态）。

## D1 — execute 请求必须覆盖 axios 默认 30s 超时（关键发现）

**Decision**: `executeWorkflow` 单请求携带 `{ timeout: 300_000 }`（5 分钟）。

**Rationale**: `utils/request.ts` 的 axios 实例默认 `timeout: 30_000`；工作流执行的耗时上界由后端 overall 5min 与 nginx `proxy_read_timeout 300s` 共同决定（含 LLM 节点 TTFT 30s + 多节点累计）。不覆盖则任何耗时 >30s 的试运行必被前端掐断（`ECONNABORTED`），且用户无法分辨「工作流慢」与「请求失败」。300s = nginx 读超时同限，覆盖后前端不再是链路最短板。

**Alternatives considered**: ① 全局调大 axios timeout——影响全部端点，常规 CRUD 不该等 5 分钟，拒绝；② 不处理（沿用 30s）——慢工作流试运行必然误报失败，功能性缺陷，拒绝。

## D2 — 试运行对话框组件化单源（WorkflowTrialDialog）

**Decision**: 新组件 `web/src/views/workflow/WorkflowTrialDialog.vue`，编辑页与详情页共用；props 收 `WorkflowDetail`（`Pick<..., 'id' | 'name' | 'type' | 'input_schema'>` 消费面）+ `v-model` 开关；内部自持入参表单 / 执行 / 结果状态。

**Rationale**: FR-003 明确「共用同一对话框」；两入口的数据源都是已加载的 `WorkflowDetail`（详情页 `detail` ref、编辑页 `detail` ref = GET 落库版本），传引用即满足「试运行跑已落库版本」语义，无需二次请求。放 `views/workflow/` 与 GraphModeEditor / TemplateField 等同域组件同层（先例：spec 012 TemplateField）。

**Alternatives considered**: ① 两页各写一份对话框逻辑——违反单源与 FR-003，拒绝；② 放 `components/` 全局目录——它是 workflow 域专有消费面（依赖 `api/workflow` 类型），不属于跨域复用件，与既有域内组件先例不符，拒绝。

## D3 — 编辑页脏态交互 = 阻断式确认（复用既有 isDirty + ElMessageBox）

**Decision**: 点「试运行」时先 `isDirty()` 判定（既有快照比对函数，含共享 config 引用改动的现场序列化通道）；脏 → `ElMessageBox.confirm('当前有未保存改动，试运行执行的是已保存版本，请先保存。', { confirmButtonText: '去保存', cancelButtonText: '取消' })`，确认 → 调既有 `save()`（保存成功本就跳详情页，用户在那儿再点试运行），取消 → 留页；不脏 → 直接开对话框。

**Rationale**: 「先提示保存，不静默跑旧图」（US2 场景 2）的最直接实现是阻断而非提供「仍要跑旧图」旁路——旁路按钮极易让用户误以为在测当前画布内容。复用 `save()` 不新增保存路径；`save()` 成功后 `router.push` 回详情页是既有行为，链路自然（详情页有同一试运行入口）。

**Alternatives considered**: ① 提供「仍要试运行」选项——语义误导风险（用户以为在测未保存改动），拒绝；② 脏态自动先保存再试——替用户做保存决定（可能覆盖他人并发改动），且 409 / 400 时状态混乱，拒绝。

## D4 — task 型入参组装：值控件按 SchemaField.type 收敛 + JSON.stringify

**Decision**: task 型且 `input_schema` 非空时，每字段一行：`string` → `el-input`、`number` → `el-input-number`、`boolean` → `el-switch`；提交时 `JSON.stringify(对象)` 作 `input`（如 `{"q":"...","top_k":3}`）。必填经 `el-form` 动态 rules（`required: f.required`）+ `scroll-to-error` 定位到行。空 `input_schema` 数组（`[]` 声明为空）与 `null`（未声明）同退化为单文本框直传。

**Rationale**: 后端 `execcontext.go` 把 `vars["input"]` 整体作字符串，`{{input.q}}` 走一级 JSON 下钻——`input` 必须是合法 JSON 对象文本才能被字段引用消费；值控件按声明类型收敛让 `JSON.stringify` 天然产出正确类型（无需字符串解析），boolean 用 switch 免去手打 `"true"` 的 JSON 语法错误。spec FR-004 三态逐字对齐（chat 单文本框必填 ≤16384 / task 有 schema 字段行 / task 无 schema 直传）。`[]` 与 `null` 同退化：后端对空 schema 不做字段校验，直传字符串是唯一可用形态。

**Alternatives considered**: ① task 型也用单文本框让用户手写 JSON——必填拦截与类型错误无法前端判定，体验差且 FR-004 明确要字段行，拒绝；② 用 `el-form` 之外手写校验循环——丢失「定位到行」能力（scroll-to-error），拒绝。

## D5 — chat-only 过滤实现位 = 前端下拉数据源

**Decision**: `AgentList.vue` 新增 `loadWorkflowOptions()`：`getWorkflowList({ page: 1, page_size: 100 })` 后 `.filter(w => w.type === 'chat')`，与既有 `loadModelOptions` / `loadKbOptions` 同策略（弹窗打开时现拉、拦截器兜错、空选项即反馈）。后端零改动。

**Rationale**: 用户 2026-09-23 拍板（spec Assumptions）：chat 型 input = 纯文本消息语义匹配；task 型入参是 JSON 对象文本，聊天纯文本对不上。后端绑定本就不限 kind（仅 FK 存在性校验），语义过滤纯属前端呈现决策。`WorkflowItem` 已声明 `type` 字段，零类型改动。

**Alternatives considered**: ① 后端加 kind 校验——违反「后端零改动」冻结边界，拒绝；② 不过滤全列——task 型绑上后会话必然解析失败，给用户埋雷，拒绝。

## D6 — workflow_id 双形态：请求体数值、响应字符串（踩坑 #8 同型）

**Decision**: `AgentBase` 增 `workflow_id: string | null`（回显面，后端 `AgentSchema.WorkflowID *string`）；`AgentSaveData` 增 `workflow_id?: number`；`AgentList.vue` 表单模型用 `workflowId: string`（`''` = 不绑定），提交 `workflow_id: form.workflowId === '' ? undefined : Number(form.workflowId)`（JSON.stringify 丢弃 undefined 键 → 后端 `*uint64` 收 nil → 创建不绑定 / PUT 解绑，`binding:"omitempty,gt=0"` 兼容）。

**Rationale**: 后端请求结构 `WorkflowID *uint64` 无 `,string` tag（agent/api/schema.go L114-117/L145-146），字符串会 400——与 `model_id` 完全同型（agent.ts 文件头踩坑 #8）；响应 `AgentSchema.WorkflowID *string`（L31-32，bigint 防精度字符串化）。PUT 全量语义（缺省 = 解绑）正好被 undefined 省键满足。

**Alternatives considered**: ① 请求也传字符串——后端 400，直接错误；② 提交 `null` 而非省键——两者后端等价（均 nil），省键与 `AgentSaveData` 既有可选字段风格（`max_output_tokens ?? undefined`）一致，取省键。

## D7 — 结果区双失败面：HTTP 信封错误 catch 后持久化展示 + status=failed 取轨迹尾部 error_msg

**Decision**: 对话框执行 catch 后把错误 message（`e instanceof Error ? e.message : '执行失败'`）写入结果区 `el-alert type="error"` 持久展示（拦截器 toast 照常弹——toast 瞬时、结果区持久，两者并存不冲突）；HTTP 200 但 `result.status === 'failed'` 时，结果区显示失败 alert，文案取 `node_trace` 执行序**最后一条** `error_msg` 非空的节点（执行停在该节点），轨迹表照常展示。

**Rationale**: US3 场景 3 要求「查看结果区，错误文案可读」——仅靠拦截器 toast（数秒消失）不满足「结果区可读」；spec 06 的 execute 失败语义分两层：请求级错误（404 / 503 / 500…）走信封 reject，运行级失败（节点错误）走 200 + `status:"failed"`，两层都要落到结果区。后 `node_trace` 尾部 = 执行中断点（引擎遇错即停），比首条更准。

**Alternatives considered**: ① 只依赖拦截器 toast——不满足结果区可读，拒绝；② failed 时只显示轨迹表不提炼文案——「卡在哪一步」要用户自己扫表拼结论，可读性差，拒绝。

## D8 — 16384 长度上限：maxlength 硬拦 + 提交前校验软拦

**Decision**: chat / task-无-schema 的单文本框（`el-input type="textarea"`）设 `maxlength="16384"` + `show-word-limit`（硬拦输入面）；提交前 `input.length > 16384` 再校验一次（防程序化赋值 / 粘贴边界）并 `notifyError`。task-有-schema 路径组装后的 JSON 文本同样做长度校验（极端多字段场景兜底）。

**Rationale**: 后端 `Input` 带 `binding:"required,max=16384"`，超限 400 且信封文案不直观（字段校验错误 details）；前端先拦（Edge Cases 明确「前端拦截提示，不发请求」）。`show-word-limit` 让用户在接近上限时有感知。

**Alternatives considered**: 仅提交前校验不加 maxlength——用户可以打 2 万字再被拦，体验差；仅 maxlength 不做提交校验——组装路径（JSON.stringify）绕过输入框限制。两者都要。

## 汇总：外部研究任务

无外部技术选型研究（零新增依赖、全部既有栈）；所有 unknown 均经仓库内代码取证消解（上表 D1-D8）。
