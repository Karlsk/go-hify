# Data Model: react 节点 / ToolSourceReq 扩展 / query 加法键（spec 020 Phase 1）

**Date**: 2026-10-10 | **Spec**: [spec.md](./spec.md) | **决策依据**: [research.md](./research.md)

一新节点类型 + 两处 jsonb/契约加法 + 一条迁移；无新表、无新索引（mcp_tools 既有 `(mcp_server_id)` 索引服务批量查询）。

## 1. ReactConfig（workflow_nodes.config，type=react，迁移 00022 加值）

| 键 | Go 类型 | JSON | 规则 |
|---|---|---|---|
| model_id | uint64 | `model_id,string` | 必填；保存期 precheckRefs 存在性预检（同 llm） |
| system_prompt | string | `system_prompt,omitempty` | 可选 {{var}} 模板；R10 扫（react 新节点从严） |
| prompt | string | `prompt` | 必填 {{var}} 模板；R10 扫 |
| temperature | float64 | `temperature,omitempty` | 0 = 跟随模型默认 |
| server_ids | []uint64 | `server_ids,omitempty` | 元素 >0；server 级绑定 = 执行期动态全量（跟随 discover / 停用过滤） |
| tool_ids | []uint64 | `tool_ids,omitempty` | 元素 >0；与 server_ids 并集（按 tool id 去重） |
| max_rounds | int | `max_rounds,omitempty` | 0/缺省 = 8（reactloop DefaultMaxRounds）；1-16 合法、越界保存期 400 |

绑定并集均空 = 纯推理（reg=nil 单轮语义，零 mcp 交互）；非空但执行期过滤后空清单 = `MCP_TOOL_NOT_FOUND` 404 fail-fast。

## 2. ToolSourceReq（mcp api 加法扩展）

```go
type ToolSourceReq struct {
    ToolIDs   []uint64 // 既有：按工具 id 绑定（chat / workflow tool 节点）
    ServerIDs []uint64 // 新增（spec 020）：按 server 绑定 = 其全部工具动态全量
}
```

解析（toolsource.ensure）：`ListToolsByIDs(ToolIDs)` ∪ `ListToolsByServerIDs(ServerIDs)`（单 SQL `= ANY($1)`）→ 按 tool id 去重 → 收集 server → 停用过滤 → 派生调用名 `s{serverID}__{toolName}`。只传 ToolIDs = 既有路径逐字不变（chat 零影响）。

## 3. KnowledgeRetrievalConfig（加法键）

| 键 | 既有/新增 | 语义 |
|---|---|---|
| knowledge_base_id / top_k | 既有 | 零变化 |
| **query** | **新增 `query,omitempty`** | {{var}} 模板；非空 strict 渲染作检索 query；空/缺省/渲染空串 = 回退 `vars["input"]`（O1 存量语义）；R10 扫 |

## 4. 观测面形态（Clarifications 落地）

- **executions（react 每轮一行）**: Input = `{prompt, system_prompt?, round, tool_calls}`；Output = `{content}`；ConversationID=nil（workflow 调用标识）；ModelID/ModelName 同 llm 节点
- **node_runs.input（react 节点行）**: `{prompt, system_prompt?, max_rounds, rounds, tool_calls, tool_chain:[{round,id,name,args,is_error}]}`——tool_chain 的 args = 模型原 Arguments 文本截 200 rune；结果文本不入（16KB wrap 兜底体积）
- **node_runs.output**: 终答文本（HitLimit 时亦为终答，非错误）

## 5. 迁移 00022（只增不改）

```sql
-- +goose Up
ALTER TABLE workflow_nodes DROP CONSTRAINT workflow_nodes_type_check;
ALTER TABLE workflow_nodes ADD CONSTRAINT workflow_nodes_type_check
    CHECK (type IN ('llm','tool','condition','knowledge_retrieval','api','end','workflow','react'));
COMMENT ON COLUMN workflow_nodes.config IS '…既有多形态… / react={model_id,prompt,system_prompt?,temperature?,server_ids?,tool_ids?,max_rounds?} / knowledge_retrieval 补 query?（00022 加宽）；…';
-- +goose Down（重建回七值 + 原 COMMENT）
```

## 6. 前端形态

- **palette 八类**（序）：llm / react / tool / knowledge_retrieval / condition / api / workflow / end；着色同族深浅配对（react=primary 深、tool=accent 深、kr=info 深；零新增 token）
- **config 保形**：react.model_id 字符串；server_ids/tool_ids 数字数组；tool.tool_id 字符串；kr.knowledge_base_id 字符串（既有 `,string` tag 面）
- **序列化零泄漏面不变**：palette 只影响拖入能力，nodes/edges 序列化路径零改动

## 7. 兼容性矩阵

| 存量形态 | 本篇后行为 |
|---|---|
| 七类节点图 | 零变化（CHECK 加值不影响存量行） |
| knowledge_retrieval 无 query 键 | 回退 input（O1 现状） |
| ToolSourceReq 只传 ToolIDs（chat / tool 节点） | 解析路径逐字不变 |
| llm 节点 executions Input 形态 | 零变化（recordExecution 不动；react 走 recordReactExecution） |
| tool 节点 JSON 形态（tool_id+args） | 执行零变化 + 新增画布表单回显 |
