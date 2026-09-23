# Contract: Agent 绑定工作流下拉（AgentList.vue）

**Feature**: [spec.md](spec.md) | **Date**: 2026-09-23 | **消费**: FR-001 / FR-002 / FR-008

## 1. 位置

`web/src/views/agent/AgentList.vue`——HifyFormDialog「基础配置」tab 内、「模型」下拉之后新增 `el-form-item label="绑定工作流"`（页内表单既有形态，非 el-dialog 直挂）。

## 2. UI 契约

```text
el-select（clearable，placeholder「选择对话型工作流（可选）」）
  ├─ 选项：chat 型工作流（label = name，value = id 字符串）
  ├─ 清空态：clearable 的 × 即「不绑定」（无「不绑定」占位 option——清空 = ''）
  └─ 空态（chat 型工作流数为 0）：el-text info「暂无对话型工作流；可先到工作流管理创建」
说明文字（form-item 下 hint，field-hint 既有样式）：
  「绑定后该 Agent 的对话将直接由工作流处理（不绑定为普通模型对话）」
```

- 数据源 `loadWorkflowOptions()`：`getWorkflowList({ page: 1, page_size: 100 })` → `.filter(w => w.type === 'chat')` → `{ value: id, label: name }`；**chat-only 过滤只在此处**（research D5）。
- 拉取时机：`openCreate` / `openEdit` 与 `loadModelOptions` / `loadKbOptions` 并列现拉（管理页低频、保持新鲜——既有策略）；失败静默（拦截器已 toast，空选项即反馈）。
- 必填性：**无 required 规则**（绑定恒可选，不阻断表单其余字段——US1 场景 5 / FR-008）。

## 3. 表单模型与提交契约

| 环节 | 契约 |
|------|------|
| 表单模型 | `AgentForm.workflowId: string`（`''` = 不绑定）；`emptyForm()` 增 `workflowId: ''` |
| 回显 | `toForm(detail)` → `workflowId: d.workflow_id ?? ''`（`AgentBase.workflow_id: string \| null`，FR-002 回显要求） |
| 提交转换 | `onSubmit` payload 增 `workflow_id: form.workflowId === '' ? undefined : Number(form.workflowId)`——省键 = 创建不绑定 / PUT 解绑（全量语义）；数值 = 绑定（后端 `*uint64` 无 `,string` tag，踩坑 #8 同型） |
| 既有回归 | payload 其余键、rules、HifyFormDialog 提交流程零变化；编辑态 enabled 开关等既有行为不动（FR-008） |

## 4. 错误契约（既有信封，消费面验证点）

- 绑定目标被并发删除 → 404 `WORKFLOW_NOT_FOUND`：拦截器 toast 信封 message，`done(false)` 弹窗保持打开、表单内容不丢（US1 场景 4）。
- 后端对绑定无 kind 限制（仅 FK 存在性校验）——前端过滤是呈现决策，不构成后端契约的一部分（改过滤范围不动后端）。

## 5. 数据流

```text
openCreate/openEdit
  → loadWorkflowOptions()（与 model/kb 并列）
  → 弹窗打开，el-select 渲染 chat 型选项
  → 用户选择（或清空 = ''）
  → onSubmit：'' → 省键；否则 Number() → AgentSaveData.workflow_id
  → createAgent / updateAgent（既有函数，签名不变）
```
