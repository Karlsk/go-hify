# Contract: WorkflowTrialDialog 组件（试运行对话框）

**Feature**: [spec.md](spec.md) | **Date**: 2026-09-23 | **消费**: FR-003 ~ FR-007

## 1. 位置与依赖

`web/src/views/workflow/WorkflowTrialDialog.vue`（新组件，与 GraphModeEditor / NodeInspector / TemplateField 同层）。依赖：`executeWorkflow` / `WorkflowRunResult` / `NodeRunSummary` / `SchemaField` / `WorkflowDetail`（`@/api/workflow`）、`notifyError`（`@/utils/notify`）、Element Plus 组件（全局注册）。零后端依赖、零新第三方依赖。

## 2. Props / Emits

```typescript
defineProps<{
  /** v-model：对话框开关 */
  modelValue: boolean
  /** 试运行目标（已落库版本——两入口的 detail ref 均来自 GET）；null 时对话框内容不渲染 */
  workflow: Pick<WorkflowDetail, 'id' | 'name' | 'type' | 'input_schema'> | null
}>()

defineEmits<{
  'update:modelValue': [value: boolean]
}>()
```

调用形态（两页同一）：`<WorkflowTrialDialog v-model="trialVisible" :workflow="detail" />`

## 3. 行为契约

### 3.1 打开 / 重置

- `watch(modelValue)` 置 true → 重置 `inputText` / `fieldValues` / `result` / `errorMsg`（每次打开全新会话）。
- 打开期间改参再执行**不重置** `result`（US2 场景 4：上一次结果展示中，新执行完成时刷新）。

### 3.2 入参三态（FR-004 逐字）

| 态 | 判定 | UI | input 组装 |
|----|------|-----|-----------|
| A | `type === 'chat'` | 单 textarea，必填，`maxlength=16384` + `show-word-limit`，placeholder「模拟用户消息」 | `inputText` |
| B | `type === 'task'` && `input_schema` 非 null 非空 | `el-form` + 每 `SchemaField` 一行：string→`el-input`、number→`el-input-number`、boolean→`el-switch`；label=字段名，required 字段带 `*` 与 rules；description 作行内提示 | `JSON.stringify({ [f.name]: fieldValues[f.name] })` |
| C | `type === 'task'` && `input_schema` null 或空数组 | 单 textarea，placeholder「输入文本（无入参 Schema，原样传入）」，必填 | `inputText` |

### 3.3 前端校验（全部不发请求）

| 校验 | 态 | 失败行为 |
|------|----|----------|
| 必填（trim 非空） | A / C | `notifyError('请输入消息内容')` |
| 长度 ≤16384 | A / C（输入与提交双拦）；B（组装后 JSON 兜底校验） | `notifyError('输入超出 16384 字符上限')` |
| 必填字段 | B | `el-form.validate` 失败 → `scroll-to-error` 定位到首个缺失行 |

### 3.4 执行（FR-005）

- 按钮「执行」：`running === true` 时 disabled（loading 态），`run()` 入口 `if (running || !workflow) return` 短路——重复点击零并发请求。
- 调 `executeWorkflow(workflow.id, input)`（query `trial=true` 固定；timeout 300s，见 api-client.md §1）。

### 3.5 结果呈现（FR-006 / FR-007）

- **成功**（`status === 'succeeded'`）：输出区（output 文本，pre-wrap 多行保留）+ 轨迹表 `el-table`（列：节点 node_key / 类型 node_type / 状态 status / 耗时 duration_ms——按 node_trace 执行序；失败行 error_msg 红字展示）+ 总耗时（duration_ms）。
- **运行失败**（HTTP 200 + `status === 'failed'`）：`el-alert type="error"`，文案 = `node_trace` 执行序最后一条非空 `error_msg`（无则「执行失败」）；轨迹表照常展示（中断点可见）。
- **请求失败**（信封 reject）：`el-alert type="error"` 持久展示信封 message（`e instanceof Error ? e.message : '执行失败'`）；拦截器 toast 照常弹（toast 瞬时、alert 持久，并存不重复遮蔽）。无裸异常文本 / 堆栈。

### 3.6 边界

- `workflow === null`：对话框模板根 `v-if` 守卫，不渲染表单（防御两入口 detail 未就绪时点开）。
- 执行中关闭对话框：请求不中止（REST 已发出），重开时状态已重置、结果丢弃——可接受语义（单次执行测试无会话状态）。

## 4. 挂载点行为（宿主页契约）

| 宿主 | 入口 | 前置 |
|------|------|------|
| WorkflowEdit.vue | 工具栏「试运行」按钮（「保存」左侧） | `isDirty()` 真 → `ElMessageBox.confirm('当前有未保存改动，试运行执行的是已保存版本，请先保存。', { confirmButtonText: '去保存', cancelButtonText: '取消' })`——确认调既有 `save()`（成功自然跳详情页），取消留页；假 → `trialVisible = true` |
| WorkflowDetail.vue | PageHeader actions「试运行」按钮（「编辑」左侧） | 无前置（只读页无脏态），直接 `trialVisible = true` |
| 两步式创建第二步 | **无入口**（FR-003：未落库无 id，MUST NOT 出现） | — |

两入口传的 `detail` 均为 GET 落库版本（编辑页 `detail` ref 不随画布编辑变化）——「试运行跑已保存版本」由数据源结构性保证，非运行时快照。
