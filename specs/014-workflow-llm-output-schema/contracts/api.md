# API Contract: LLM 节点输出字段声明与变量下拉展开

**Feature**: [spec.md](./spec.md) | **Date**: 2026-09-23 | **Phase 1**

本篇零新增 REST 端点、零新增哨兵错误、零迁移；契约面 = `LLMConfig` 加法键 + 执行器两函数行为语义。冻结条目逐字对齐 [spec.md](../spec.md) FR-001~007 / SC-001~005 与 [research.md](../research.md) D1~D3。

## 1. LLMConfig 加法键（config jsonb 契约）

- 键名：`output_schema`（与 task 型 workflow 级同名对齐）；`omitempty`——缺省/空数组序列化零键，存量图往返不变。
- 值形态：`SchemaField[]`（api 包既有：`name`（string）/ `type`（string|number|boolean）/ `required`（boolean）/ `description`（string，可空））。
- 保存期校验（FR-002）：`LLMConfig.Validate` 在 `OutputSchema` 非空时调既有 `ValidateSchemaFields`——name 非空不重名、type 枚举；**仅新键新增拒绝路径**，存量无声明路径零新增拒绝。
- 前端类型（web/src/api/workflow.ts）：llm config 增 `output_schema?: SchemaField[]`，键名逐字对齐。

## 2. 执行器注入语义（FR-006 / SC-005）

`service` 包新纯函数 `buildJSONDirective(fields []api.SchemaField) string`：

- 触发门：`len(cfg.OutputSchema) > 0`；未声明节点不进此路径（逐字节不变）。
- 位置与时机：strict 渲染成功后、`setNodeIn` 之前，追加到渲染后 user 消息末尾；不碰 system_prompt、不做模板渲染、不含用户变量。
- 文案（research D2 冻结）：前缀 `\n\n请只输出一个 JSON 对象（不要使用 markdown 代码块，不要包含 JSON 以外的任何文本），对象包含以下字段：\n` + 每字段一行 `- {name}（{type}，{必填|可选}）`；description 不进指令。
- 记录保真：追加后的完整 user 消息即 `node_in["prompt"]`、消息组装、executions 记录的实际发送文本。

## 3. 执行器校验语义（FR-005 / SC-002）

`service` 包新纯函数 `validateLLMOutput(output string, fields []api.SchemaField) error`——语义逐字对齐 execute.go:477 `validateOutputSchema` 家族：

| 分支 | 输入 | 结果 |
|---|---|---|
| 未声明 | `len(fields)==0` | 零校验，原样通过 |
| 非 JSON 对象 | 纯文本 / null / 数组 / 围栏包裹 | 拒（unmarshal 到 `map[string]json.RawMessage` 失败或 nil） |
| 缺必填 | required 字段缺失 | 拒，文案含字段名 |
| 类型不符 | `probeSchemaType`（同包复用：string→string / number→json.Number / boolean→bool）不符 | 拒，文案含字段名与期望类型 |
| 多余字段 | 回复含未声明字段 | 宽容通过 |

失败包装：callLLM 处带节点 key 前缀包装 `errs.ErrValidationFailed`（零新增哨兵，HTTP 面 = 既有 VALIDATION_FAILED 映射）；run 落 failed、节点轨迹记录失败态。校验在 `client.Generate` 成功后、`return` 前执行；trial 试运行同路径同校验。

## 4. 不变量（SC-003，红线）

未声明节点：消息序列、node_in 形态、executions 形态、序列化往返——与本篇引入前**逐字节一致**；注入与校验均以 `len(OutputSchema) > 0` 为门，无其他隐式触发。
