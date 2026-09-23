# Data Model: 前端 agent 绑定工作流与工作流试运行

**Feature**: [spec.md](spec.md) | **Date**: 2026-09-23

本篇零数据库改动、零后端模型改动——数据模型 = 前端 API 类型增量 + 视图状态模型。全部字段名对齐后端 JSON snake_case 契约（键名不自造）。

## 1. API 类型增量（web/src/api/）

### 1.1 workflow.ts —— 执行结果类型（对齐 internal/workflow/api/schema.go RunResultSchema / NodeRunSummary，spec 06 冻结契约）

```typescript
/** 节点执行摘要（后端 NodeRunSummary；json 键逐字对齐，不自造） */
export interface NodeRunSummary {
  node_key: string
  /** 后端 NodeType 全集字符串（与节点 config 的 type 同域） */
  node_type: string
  /** succeeded / failed */
  status: 'succeeded' | 'failed'
  duration_ms: number
  /** 失败原因；成功节点恒空串 */
  error_msg: string
}

/** 单次执行结果（后端 RunResultSchema）；status=failed 时 output 可能为空、错误在 node_trace */
export interface WorkflowRunResult {
  /** 运行轨迹 id（轨迹落库降级时置空串） */
  run_id: string
  status: 'succeeded' | 'failed'
  output: string
  duration_ms: number
  /** 逐节点执行序摘要（引擎遇错即停） */
  node_trace: NodeRunSummary[]
}
```

> 既有类型复用：下拉过滤用 `WorkflowItem.type`（已声明）；对话框入参表单用既有 `SchemaField`（name / type: 'string'|'number'|'boolean' / required / description）与 `WorkflowDetail.input_schema: SchemaField[] | null`。

### 1.2 agent.ts —— 绑定字段（对齐 internal/agent/api/schema.go L31-32 / L114-117 / L145-146，spec 05 冻结契约）

```typescript
// AgentBase 增（响应面：后端 AgentSchema.WorkflowID *string —— bigint 字符串化）
workflow_id: string | null   // null = 未绑定；回显 / 提交转换的源

// AgentSaveData 增（请求面：后端 *uint64 无 ,string tag —— 数值，同 model_id 踩坑 #8）
workflow_id?: number         // 省键 / undefined = 不绑定（创建）/ 解绑（PUT 全量语义）
```

## 2. 视图状态模型

### 2.1 AgentList.vue —— 绑定下拉（FR-001 / FR-002）

| 状态 | 类型 | 语义 |
|------|------|------|
| `workflowOptions` | `ref<Array<{ value: string; label: string }>>` | chat 型工作流选项（id / name）；弹窗打开时现拉（与 model / kb 选项同策略） |
| `workflowLoading` | `ref<boolean>` | 下拉加载态 |
| `AgentForm.workflowId` | `string` | `''` = 不绑定（清空态）；否则为工作流 id 字符串（el-select clearable） |

**回显**：`toForm(detail)` → `workflowId: d.workflow_id ?? ''`。
**提交转换**（`onSubmit`）：`workflow_id: form.workflowId === '' ? undefined : Number(form.workflowId)`——省键即后端 nil（创建不绑定 / PUT 解绑）。
**必填性**：绑定项恒可选（无 required 规则），不改变既有字段校验（FR-008）。

### 2.2 WorkflowTrialDialog.vue —— 试运行对话框（FR-004 ~ FR-007）

| 状态 | 类型 | 语义 |
|------|------|------|
| `inputText` | `ref<string>` | chat 型 / task-无-schema 的单文本框值（必填、≤16384） |
| `fieldValues` | `ref<Record<string, string \| number \| boolean>>` | task-有-schema 的字段行值（键 = SchemaField.name，值类型按声明收敛） |
| `running` | `ref<boolean>` | 执行 loading（防重复触发：`run()` 入口短路） |
| `result` | `ref<WorkflowRunResult \| null>` | 最近一次执行结果（成功 / failed 均存；打开对话框时重置） |
| `errorMsg` | `ref<string>` | 请求级失败文案（catch 后写入，结果区 el-alert 展示） |

**入参三态**（由 props `workflow.type` × `workflow.input_schema` 判定，打开时定型）：

| 态 | 条件 | 表单 | input 组装 |
|----|------|------|-----------|
| A 消息文本 | `type === 'chat'` | 单 textarea（placeholder「模拟用户消息」） | `inputText` 原文 |
| B 字段行 | `type === 'task'` 且 `input_schema` 非 null 非空数组 | 每 SchemaField 一行（string→el-input / number→el-input-number / boolean→el-switch；required 动态 rules + scroll-to-error） | `JSON.stringify(按字段名组装的对象)` |
| C 直传文本 | `type === 'task'` 且 `input_schema` 为 null 或 `[]` | 单 textarea（placeholder「输入文本（无入参 Schema，原样传入）」） | `inputText` 原文 |

**状态流转**：

```text
打开（v-model=true，watch 重置 inputText/fieldValues/result/errorMsg）
  → 填参 → 执行（running=true；前端校验不过 → notifyError 不发请求）
      → HTTP 200 → result=result（status=succeeded：输出+轨迹；failed：错误 alert 取 node_trace 尾部 error_msg + 轨迹）
      → reject → errorMsg=信封 message（结果区 el-alert；拦截器 toast 照常）→ running=false
  → 改参再执行（US2 场景 4：不关对话框，result 随新执行刷新）
```

### 2.3 挂载点状态（两页各一）

| 页面 | 状态 | 语义 |
|------|------|------|
| WorkflowEdit.vue | `trialVisible: ref<boolean>` | 工具栏「试运行」→ `isDirty()` 真：ElMessageBox「去保存 / 取消」（去保存 → 既有 `save()`）；假：置 true |
| WorkflowDetail.vue | `trialVisible: ref<boolean>` | PageHeader actions「试运行」→ 直接置 true（详情页只读无脏态） |

两页传同一 props 形态：`<WorkflowTrialDialog v-model="trialVisible" :workflow="detail" />`（`detail` 均为 GET 落库版本 → 「试运行跑已保存版本」由数据源保证）。

## 3. 校验规则汇总（全部前端拦截，不发请求）

| 规则 | 作用面 | 行为 |
|------|--------|------|
| chat 入参必填 | 态 A | trim 后空 → notifyError「请输入消息内容」 |
| 长度 ≤16384 | 态 A/C 输入框 + 态 B 组装后 JSON | maxlength 硬拦 + 提交前校验 notifyError |
| 必填字段 | 态 B | el-form rules + scroll-to-error 定位到行 |
| 重复触发 | 执行按钮 | `running` 短路（US3 场景 1） |

## 4. 实体关系（本篇涉及面，均既有）

```text
Agent (workflow_id: string | null) ──可空引用──> Workflow (type: 'chat'|'task')
                                                  └─ input_schema: SchemaField[] | null（chat 恒 null）
WorkflowRunResult ──node_trace[]──> NodeRunSummary（执行序，引擎遇错即停）
```

无新增实体、无状态机改动（workflow 三态状态机的 trial 例外是后端既有语义，前端仅携带 query 标记）。
