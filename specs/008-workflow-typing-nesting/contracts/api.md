# API Contract 增量: workflow 分型与子工作流嵌套

> 上位契约：docs/changelog/workflow/api_contract.md（8 路由 + §5 Execute + §8 错误表）。
> 本篇：无新路由、接口方法签名零改动、哨兵零新增；以下为既有请求/响应体的字段增量与行为语义。
> 冻结依据：impl_spec_08_typing_nesting.md §4 / §5；两处 clarify 拍板（2026-09-20）已标注。

## 1. 请求体增量

### POST /api/v1/workflows（UpsertReq）

```jsonc
{
  "name": "查订单任务", "description": "...", "type": "task",   // 必填，oneof: chat | task
  "input_schema":  [{"name":"order_id","type":"string","required":true,"description":"订单号"}],
  "output_schema": [{"name":"summary","type":"string","required":true,"description":"摘要"}],
  "start_node_key": "...", "nodes": [...], "edges": [...]
}
```

- `type`：Create 必填（缺省/非法值 → 400 VALIDATION_FAILED）。
- `input_schema` / `output_schema`：可空数组；简化形态 `[{name, type, required, description}]`，`type ∈ string | number | boolean`；字段重名 / 非法 type → 400。
- **chat 型携带任一非空 schema → 400**（强不变量，clarify 拍板）。

### PUT /api/v1/workflows/{id}（UpdateWorkflowReq）

- 增同款 `input_schema` / `output_schema` 字段（语义同 Create）。
- 增 `type` 字段：**请求携带 type（同值异值均算）→ 400 拒绝**（不可变，clarify 拍板）。

### nodes[]（NodeReq）——第七种节点类型

```jsonc
{ "key": "sub1", "type": "workflow", "name": "查订单子流程",
  "config": { "workflow_id": "7", "inputs": { "order_id": "{{input.order}}" } } }
```

- `type: "workflow"`（与既有六类值同风格）。
- `config` 密封：`workflow_id`（字符串化外键）+ `inputs`（字段→`{{var}}` 模板映射）。
- 保存期 R11 五项校验（存在性 / task 型 / 环 / 链深 ≤3 / inputs 键集覆盖——无 schema 子图回退恰 `{input}`），任一不过 → 400 VALIDATION_FAILED（message 带 node key 定位）。

## 2. 响应体增量

WorkflowSummarySchema / WorkflowDetailSchema 增：

```jsonc
"type": "task",
"input_schema":  [ ... ],   // 可空：null（chat 型或未声明）
"output_schema": [ ... ]
```

## 3. 行为语义（冻结，契约 §4）

### 3.1 类型生命周期（§4.1）

Create 必填 → 不可变（换型 = 删了重建）；两型状态机同款；Get/List 暴露 type；控制台 execute + trial 两型照旧；**绑定不 gate**——chat 型或 task 型绑 agent 都照走 spec 07 管道。

### 3.2 执行：sub-workflow 节点（§4.3）

父图走到 sub-workflow 节点：逐值渲染 inputs（strict）→ 按子 input_schema 组装 JSON 文本 → 进程内递归执行子图（同 goroutine、共享父请求 ctx——断连整链取消；每层自包 5min；深度计数超 3 → 图缺陷 400 带前缀）→ 子终稿过 output_schema 校验 → 落父变量池（node_key 可引、JSON 值可一级下钻）。

- **隔离**：子 vars 池全新起步（只含自身 input）；父子仅经 input/output 通信。
- **把关**：Trial 跟随父（父试运行 → 子放开 draft/disabled）；正式运行子必须 published（否则 WORKFLOW_NOT_PUBLISHED 带父 node 前缀）。
- **结构化**（§4.5）：入参为合法 JSON 对象且子声明 input_schema → 解析入池，否则整串落 `input`（原行为）；池一级下钻 `{{input.x}}` / `{{node.field}}`（string 值不变、深度一层为止）；R10 保存期只校验基名，字段名运行期 strict。

### 3.3 子 run 轨迹（O5 A / O7b）

子 run 独立落库：`trigger_source='workflow'`、`conversation_id` / `message_id` 与父相同（透传）、`is_trial` 跟随父；父收尾按父 run id 批量回填 `parent_run_id`——一次查询还原整棵执行树。子 run 写入失败降级同既有语义（重试一次，仍败照常返回，trace_id 兜底）。

## 4. 错误语义（零新哨兵，§4.6 二分法延伸）

| 场景 | HTTP | error.code | message 形态 |
|---|---|---|---|
| 保存期 R11 任一项不过 / schema 形态非法 / chat 型带 schema / Update 携带 type | 400 | `VALIDATION_FAILED`（errs） | 带 node key / 字段定位 |
| 子图内部图缺陷（含 output 校验失败、运行期字段缺失、深度超限） | 400 | `VALIDATION_FAILED`（errs） | `node a: node b: ...` 前缀链 |
| 正式运行子未 published | 503 | `WORKFLOW_NOT_PUBLISHED` | 带父 node 前缀 |
| 执行期引用已删 workflow | 404 | `WORKFLOW_NOT_FOUND` | 带父 node 前缀 |
| 子环境限制（api 节点 SSRF / 层超时累计语义不变） | 500 | `WORKFLOW_EXECUTION_FAILED` | 前缀链 |
| 下游哨兵（MODEL_NOT_FOUND / PROVIDER_BUSY / ...） | 既有映射 | 原样透传 | Execute 错误二分法既有行为 |
