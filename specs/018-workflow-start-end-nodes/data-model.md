# Data Model: Workflow 画布开始/结束节点与入参默认值（spec 018 Phase 1）

**Date**: 2026-10-09 | **Spec**: [spec.md](./spec.md) | **决策依据**: [research.md](./research.md)

零新表、零迁移、零 SQL 改动——全部为 jsonb 内加法契约 + 前端形态。DB model（`service/model.go` 的 `Workflow.InputSchema/OutputSchema *string`、`WorkflowNode.Config`）结构不动，jsonb 内容按下表扩展。

## 1. SchemaField（加法扩展——workflows.input_schema / output_schema 共用）

| 字段 | Go 类型 | JSON | 既有/新增 | 规则 |
|---|---|---|---|---|
| name | string | `name` | 既有 | 非空、集合内不重名 |
| type | string | `type` | 既有 | ∈ string / number / boolean |
| required | bool | `required` | 既有 | output_schema 侧由前端按「全部映射 end 都产出」自动推导写入；input_schema 侧手工配置（现状不变） |
| description | string | `description` | 既有 | 可空 |
| **default** | **json.RawMessage** | **`default`（omitempty）** | **新增（FR-006）** | 仅 optional 字段可配（required + default → 保存期 400）；值类型探针须匹配 type（string→JSON string、number→JSON number、boolean→JSON bool；`null` 不匹配任何型被拒）；生效点 = assembleInputJSON（子工作流传参，FR-007）+ normalizeInputJSON（顶层，FR-008）+ 试运行表单预填（FR-011） |

## 2. OutputField / EndConfig（加法扩展——workflow_nodes.config，type=end）

```go
type OutputField struct {
    Name string `json:"name"` // 终稿 JSON 对象字段名；节点内非空不重名
    Ref  string `json:"ref"`  // 单一 {{var}} 引用（点分一级下钻合法），禁混排文本
}

type EndConfig struct {
    Output       string        `json:"output,omitempty"`        // 高级模式：自由模板（既有）
    OutputFields []OutputField `json:"output_fields,omitempty"` // 映射模式（FR-009 新增）
}
```

行级校验（`EndConfig.Validate`，保存期 400）：① `output` 与 `output_fields` 互斥拒（二义终稿）；② 行 name 非空、节点内不重名；③ ref 恰为单一 `{{var}}`（包内 `singleVarRE`）。**无跨 end 规则**（多 end 各自映射是合法形态，clarify 2026-10-09）。

## 3. 图级 output_schema 与多 end 映射的关系（前端推导，后端兜底）

- **并集**：全部映射 end 的 output_fields 字段按名合并——同名同型合并为一项、同名不同型 = 前端保存期拦截（运行期 validateOutputSchema 兜底）。
- **required 自动推导**：字段 required = 全部映射 end 都产出该字段；部分产出 = optional。映射行不设手工 required 开关。
- **type 推导**：`{{input.x}}` → input_schema 声明型；`{{llmKey.field}}` → 该 llm 节点 output_schema 声明型；`{{workflowKey.field}}` → 子工作流 output_schema 声明型；裸 `{{nodeKey}}` 与类型不可得来源 → string。
- 推导纯函数落 `web/src/views/workflow/graph.ts`（deriveOutputSchema），保存 payload 组装时统一调用；JSON 模式直改 output_schema 以原文为准。

## 4. 执行期形态（service 内部，非契约）

| 位置 | 形态 | 消费点 |
|---|---|---|
| `execContext.outputTypes map[string]string` | 图级 output_schema 字段名 → 声明类型；Execute / executeChild 从 `wf.OutputSchema` 装配（解析失败置 nil，fail-open） | `buildOutputFields` 值处理（R4/R6） |
| `normalizeInputJSON(input, fields) string` | 纯函数：合法 JSON 对象 → 补缺失 optional 默认；其余原文返回 | Execute（task 型 + InputSchema 非空）、newExecContext 之前；产物同入 runMeta.input（轨迹记实际执行值） |
| `buildOutputFields` 值处理 | 声明 string → Go string（Marshal 转义）；声明 number/boolean → 值须可解析为对应 JSON 型（原文落位），失败 = 图缺陷；未声明 → 可解析 JSON 则原样、否则按字符串 | end 节点终稿构造；缺失变量 strict 报错（ErrValidationFailed） |

## 5. 前端形态（非持久化）

| 形态 | 位置 | 说明 |
|---|---|---|
| 伪开始节点 | CanvasEditor 渲染层 | id=`__start__` 哨兵（复用既有常量），固定坐标、draggable=false、不可删不可连边、**永不进 nodes / 序列化**；task 型副标签「N 入参」、chat 型「单一入参 {{input}}」；点击 → 既有 openSchemaPanel |
| 映射模式编辑器状态 | NodeInspector（end 节点 task 型） | mode: mapping \| advanced；初始模式反解析：有 output_fields → mapping、仅有 output → advanced、全新 → mapping；两模式互斥切换 |
| 试运行预填 | WorkflowTrialDialog | 打开时按 input_schema[].default 预填；提交时清空「optional 且有 default」的行从对象省略（清空 = 使用默认；无默认 optional 行空值照传，零回归） |

## 6. 兼容性矩阵

| 存量形态 | 本篇后行为 |
|---|---|
| 无 default 的 schema | 零变化（omitempty 不出现键；assembleInputJSON/normalizeInputJSON 跳过） |
| end 仅 output 自由模板 | 高级模式呈现原文，行为零变化（buildOutput 走既有分支） |
| output_fields 与 output 均空 | 零变化（空 output = 末节点输出） |
| chat 型 workflow | 全部新行为不生效（schema 强制 NULL；end 检查器无映射模式；normalize 跳过） |
| 顶层 input 非 JSON / 非对象 | 原样放行（R5 只补默认不新增拒绝） |
