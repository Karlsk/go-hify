# Contracts: react / knowledge_retrieval / tool 节点契约（spec 020 Phase 1）

**Date**: 2026-10-10 | **Spec**: [spec.md](./spec.md) | **形态**: [data-model.md](../data-model.md)

对外可观察面 = 一个新节点类型的 jsonb 契约 + mcp 一个加法字段 + 一个加法配置键 + 一条迁移。**零新 REST 路由、零新哨兵错误、零新 SSE 事件**。

## C1. ReactConfig（jsonb，type=react）

```jsonc
{
  "model_id": "3",                 // 字符串保形（,string tag）
  "system_prompt": "你是执行代理…{{input.style}}",  // 可选，{{var}} 模板
  "prompt": "任务：{{input}}",      // 必填，{{var}} 模板
  "temperature": 0.3,              // 可选，0=跟随模型
  "server_ids": [1, 2],            // 可选，数字数组；server 级绑定=动态全量
  "tool_ids": [7],                 // 可选，数字数组；与 server_ids 并集去重
  "max_rounds": 4                  // 可选，0/缺省=8，1-16 合法
}
```

保存期校验（`ReactConfig.Validate`）：model_id 非零 / prompt 非空 / max_rounds ∈ {0}∪[1,16] / 两数组元素 >0。R10 扫 prompt + system_prompt。precheckRefs 预检 model_id。

## C2. react 执行与失败分型（O4 二分法对齐）

| 失败点 | 分型 | 码/路径 |
|---|---|---|
| prompt / system_prompt 渲染缺失变量 | 图缺陷 400 | `errs.ErrValidationFailed` + `node <key>:` 前缀 |
| Registry 构建失败（DB 错 / 撞车） | 环境 500 | `%w` 包装上抛 |
| 并集过滤后空清单（server 停用 / 工具删） | 404 | `mcpapi.ErrMCPToolNotFound` 透传（fail-fast 同 tool 节点） |
| LLM 调用失败 | 透传 | 下游哨兵 / llm 错误类（同 callLLM） |
| ctx 超时 / 取消 | 透传 | walk 层 5min 归一（既有） |
| HitLimit 收尾 | **非错误** | 终答照落池 |

## C3. 观测面（node_runs / executions）

- executions 每轮一行：Input `{prompt, system_prompt?, round, tool_calls}`、Output `{content}`、ConversationID=nil
- node_runs.input：`{prompt, system_prompt?, max_rounds, rounds, tool_calls, tool_chain:[{round,id,name,args,is_error}]}`（args = 模型原 Arguments 截 200 rune）
- node_runs.output：终答文本

## C4. ToolSourceReq.ServerIDs（mcp 加法）

`{tool_ids 既有语义} ∪ {server_ids → 该 server 全部工具（执行期动态）}`，按 tool id 去重 → 停用 server 过滤 → 派生调用名。只传 ToolIDs = 既有路径逐字不变。store 新增 `ListToolsByServerIDs`（单 SQL `= ANY($1)`，显式列）。

## C5. KnowledgeRetrievalConfig.query（加法键）

`query` 非空 → strict 渲染作检索 query（缺失变量 = 图缺陷 400）；空 / 缺省 / 渲染空串 → 回退 `vars["input"]`。nodeIn query 记**渲染后实际值**。R10 扫 query。

## C6. 不变量守护（零回归面）

| 不变量 | 守护 |
|---|---|
| 七类存量节点行为 | CHECK 加值不影响存量行；executor 既有分支零改动 |
| chat ToolSourceFor 路径 | ServerIDs 空 = 既有解析逐字 |
| llm 节点 executions 形态 | recordExecution 不动；react 独立 recordReactExecution |
| O1 query 固定 input | 空回退保持（加法修订非替换） |
| tool 节点执行语义 | 后端零改动（仅前端表单） |
| 设计 token 只增不改 | 三新节点着色 = 既有色族深浅配对 |

## C7. 迁移 00022

CHECK 同名重建加 `'react'`（00017/00020 模式）+ config COMMENT 补 react / query 形态；goose Up/Down 成对；只增不改。
