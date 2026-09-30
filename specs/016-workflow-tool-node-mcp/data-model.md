# Data Model: workflow 工具节点接 MCP（spec 016）

> Phase 1 输出。本篇**零新表、零迁移、零 schema 改动**——数据面只是既有实体在 tool 节点执行路径上的行为映射。

## 实体：ToolConfig（workflow 图节点配置，冻结契约，零改动）

| 字段 | 类型（wire） | 约束 |
|---|---|---|
| `tool_id` | string（Go uint64，`json:",string"`） | 必填非零；指向 mcp_tools.id；保存期不校验存在性（O4 拍板），执行期解析 |
| `args` | `map[string, string>` | 可空；值支持 `{{var}}` 模板；值域仅字符串（D5 拍板 A） |

执行期派生态（不落库，仅轨迹与调用请求）：

- **nodeIn 轨迹值**：`{"tool_id": "<字符串化>", "args": <渲染后按 key 排序的 JSON 对象>}`——对齐 callWorkflow inputs 形态，走既有 wrapTraceJSON（16KB 单值截断）。
- **调用请求**：`tool.ToolCall{ID: <执行器生成锚点>, Name: <ToolSource 供出的派生调用名>, Arguments: <渲染后 JSON>}`。
- **输出**：`tool.ToolResult.Content` 落变量池 key `= 节点 key`（下游 `{{node_key}}` 引用）+ node run `Output`（截断机制既有）。

## 实体：工具来源（tool.Source，消费契约，零改动）

`platform/tool` 既有契约，workflow 作为新消费方接入：

- `Tools(ctx) ([]ToolDef, error)`——空清单语义 = 绑定工具已删或 server 停用（boundToolSource 静默过滤停用）→ workflow 判 404。
- `Invoke(ctx, ToolCall) ToolResult`——恒返回结果；`IsError=true` → workflow 判 500（message = Content）。与 chat 的消费差异（喂回 LLM 自纠）由调用方语义决定，契约本身零改动。

## 实体：节点轨迹（workflow_node_runs，既有表，零改动）

tool 节点每步执行沿用既有落库路径：`node_key` / `node_type="tool"` / `input`（nodeIn JSON）/ `output`（Content）/ `status` / `duration_ms`——无新增列，截断与 wrapTraceJSON 既有。

## 状态转换（tool 节点步级）

```
walk 到达 tool 节点
  → 解析工具（ToolSourceFor → Tools）
  → 渲染 args（严格模板）
  → 调用（Invoke）
  → [IsError] → nodeStep failed → run failed + error_node=本节点
  → [成功]  → nodeStep succeeded（DurationMs）→ Content 落池 → 按 DAG 边继续
```

## 校验规则汇总

| 规则 | 层 | 错误 |
|---|---|---|
| ToolConfig.ToolID 非零 | api Validate（既有，零改动） | VALIDATION_FAILED 400 |
| args 模板变量存在 | 执行期（新增） | VALIDATION_FAILED 400，`args.<k>` 定位 |
| 工具存在且 server 启用 | 执行期（新增，Tools 清单空判定） | MCP_TOOL_NOT_FOUND 404 透传 |
| 工具调用成功 | 执行期（新增，IsError 判定） | MCP_TOOL_CALL_FAILED 500 |
| 装配完整 | 执行期（新增，nil 判定） | MCP_TOOL_CALL_FAILED 500「工具来源未注入」 |
