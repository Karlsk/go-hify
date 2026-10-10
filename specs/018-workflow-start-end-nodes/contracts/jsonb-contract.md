# Contracts: jsonb 加法契约与行为语义（spec 018 Phase 1）

**Date**: 2026-10-09 | **Spec**: [spec.md](./spec.md) | **数据形态**: [data-model.md](../data-model.md)

本篇对外可观察面 = **两个 jsonb 加法键 + 三条行为语义**。零新路由、零新哨兵错误、零新 SSE 事件、零迁移；REST 面完全复用既有 `/api/v1/workflows` CRUD + `/:id/execute`（UpsertReq / NodeSchema config 透传 / WorkflowSummarySchema schema 字段序列化自动携带新键）。

## C1. `SchemaField.default`（jsonb 加法键）

```jsonc
// workflows.input_schema / output_schema 数组元素；llm 节点 config.output_schema 同型兼容（default 在 llm 侧不消费、校验同样生效）
{ "name": "limit", "type": "number", "required": false, "description": "条数", "default": 10 }
```

- 值形态 = field type 对应的 JSON 值（string → `"x"`、number → `10`、boolean → `false`）；`null` 非法。
- `required: true` 携带 default → 保存期 400（`VALIDATION_FAILED`，文案含字段名）。
- 类型探针不匹配 → 保存期 400（同上）。
- 生效语义（三处一致，缺一即另一处补）：
  - **子工作流传参**（`assembleInputJSON`）：父图 inputs 未提供该 optional 字段 → 组装 JSON 补 default；无 default 维持现状（省略该键）。
  - **顶层执行**（`normalizeInputJSON`）：task 型 + 声明 input_schema + input 为合法 JSON 对象 → 补缺失 optional 默认后进变量池；**input 非合法 JSON / 非对象 / 含未声明字段 / 类型不符 → 原样放行不新增拒绝**（strict 校验留给运行期模板渲染与未来虚拟工具 spec）。
  - **试运行表单**（前端）：打开预填 default；清空「optional 且配置了 default」的行 → 该行从提交对象省略（清空语义 = 使用默认，与顶层归一化闭环）；未配 default 的 optional 行空值照传 `""`（现状不变，零回归）。

## C2. `EndConfig.output_fields`（jsonb 加法键）

```jsonc
// workflow_nodes.config（type=end）
{ "output_fields": [ { "name": "summary", "ref": "{{llm1.summary}}"}, { "name": "score", "ref": "{{llm1.score}}" } ] }
```

- 与既有 `output` **互斥**：同时非空 → 保存期 400。
- 行级：name 非空、节点内不重名；ref 恰为单一 `{{var}}`（点分下钻合法：`{{input.x}}` / `{{llmKey.field}}` / `{{workflowKey.field}}` / 裸 `{{nodeKey}}`）。
- R10 保存期静态校验覆盖各 ref **基名** ∈ `{input}` ∪ 该节点祖先 node_key（点分字段名留运行期 strict——与既有规则同一切分）。
- **终稿构造语义**（`buildOutput`，`output_fields` 非空时优先于 `output`）：
  1. 逐字段 `lookup(ref)` 取池值（缺失 → 执行失败，图缺陷 400 语义，`node <key>:` 前缀定位）；
  2. 按图级 output_schema 声明类型处理值：声明 string → 字符串落位（`json.Marshal` 转义——含引号/换行安全）；声明 number/boolean → 值须可解析为对应 JSON 型（原文落位，解析失败 = 执行失败图缺陷）；字段不在 schema → 回退「可解析为 JSON 则原样、否则按字符串」；
  3. 整对象 `json.Marshal` 产出终稿 → 既有 `validateOutputSchema` 兜底校验（required 缺失/类型不符 → run failed）。
- **多 end 语义**（clarify 2026-10-09）：各 end 可各自配映射；一次 run 线性游走恰好终止于一个 end，终稿 = 该 end 的映射产出；图级 output_schema 由前端同步为**全部映射 end 字段并集**（同名同型合并 / 同名不同型前端保存期拦截），字段 required 自动推导（全部映射 end 都产出 = required）。

## C3. 既有契约的不变量（本篇显式守护）

| 不变量 | 守护方式 |
|---|---|
| ExecuteReq 单一 string input 终形（O1） | normalizeInputJSON 不改签名不改拒绝行为，纯文本变换 |
| `{{var}}` 模板语法 / 一级下钻 / R10 基名规则 | output_fields ref 复用同一 regex 家族与切分规则，无新语法 |
| 隐式终止（无出边 = 末节点输出） | end 未配 output_fields 且无 output 时行为零变化 |
| chat 型 schema 强制 NULL / chat 管道单 string 透传 | 全部新路径以 task 型为前提；chat 零改动（回归测试证明） |
| 错误码面 | 零新增——全部走 `errs.ErrValidationFailed`（400）与既有执行失败路径 |
