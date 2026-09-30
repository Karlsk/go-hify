# Error Contract: tool 节点执行错误分型（spec 016）

> 本篇零新增哨兵——全部复用既有行；本文件钉死 tool 节点路径上每个错误的归类与可观察文案，供 handler 映射（既有，零改动）与测试断言引用。

## 分型表（tool 节点执行路径）

| # | 触发条件 | 哨兵（既有） | HTTP | 归类（spec 06 二分法） | 可观察形态 |
|---|---|---|---|---|---|
| E1 | 工具已被删 / server 已删或停用（`Tools` 清单空） | `mcpapi.ErrMCPToolNotFound` | 404 | 下游哨兵透传 | `MCP_TOOL_NOT_FOUND`，msg 带 `node <key>:` 前缀 |
| E2 | `Tools` DB 查询失败 | 原始错误 `%w` 包装 | 500 | 未预期 | `INTERNAL_ERROR`（细节进日志） |
| E3 | args 模板引用运行时缺失变量 | `errs.ErrValidationFailed` | 400 | **图缺陷**（作者可修） | `VALIDATION_FAILED`，`node <key>: args.<参数名>` 定位 |
| E4 | 工具调用执行失败（`ToolResult.IsError`） | `mcpapi.ErrMCPToolCallFailed` | 500 | 环境限制（执行失败） | `MCP_TOOL_CALL_FAILED`，msg = 工具返回错误文本（截断） |
| E5 | 装配缺位（nil 工具来源，仅测试可达） | `mcpapi.ErrMCPToolCallFailed` | 500 | 环境限制（装配缺陷） | 同码，msg「工具来源未注入」 |

## run 落库联动（既有机制，零改动）

- E1–E5 任一发生：`workflow_runs.status = failed`、`error_msg = 错误文案`、`error_node = 该节点 key`。
- 节点轨迹该步 `status` 置失败（既有 walkOutcome 路径），已渲染 nodeIn 照常落（E3 发生在 setNodeIn 前，无 nodeIn 落库——与 callWorkflow inputs 渲染失败同形）。

## 与 chat 工具循环的语义分野（钉死，勿混）

同一 `tool.Source` 契约，两种消费语义：chat = `IsError` 结果喂回 LLM 自纠（错误即上下文，对话不中断）；workflow = `IsError` 即节点失败、run 终止（确定性图要求节点产出）。判定与转换只发生在各自调用方，契约零改动。
