# MCP Chat 批实现记录（chat_tool_loop_impl）

> 状态：**已交付**（2026-09-30）。spec 见 [chat_tool_loop_spec.md](chat_tool_loop_spec.md)（2026-09-29 用户批准）；
> 架构总纲（统一工具与消息模型）已固化进 CLAUDE.md《统一工具与消息模型》，本文只记落地细节与偏差。
> 前序批次：server 管理批 [server_mgmt_impl.md](server_mgmt_impl.md)。

## 1. 交付清单

按依赖方向自底向上（每层编译期验证：`go build ./...` 全绿）：

| 层 | 文件 | 内容 |
|---|---|---|
| **platform/tool**（新包，纯标准库） | `tool.go` | 全局统一工具契约：`ToolDef` / `ToolCall` / `ToolResult`（IsError 即上下文，失败不走 error 中断）/ `Source` 接口 |
| | `registry.go` | `Registry`：构建期校验（JSON Schema 顶层对象、调用名唯一）+ 按名路由 Invoke；未知名返回 IsError 结果 |
| **platform/llm**（公开面 Hify 化，保护链零改动） | `types.go` | `Message`（Role/Content/ToolCalls/ToolCallID/ToolName）/ `Usage` / `Final`——chat/workflow 组装上下文的唯一消息形态 |
| | `convert.go` | eino ↔ Hify 唯一转换点：`toEinoMessages`（出向，tool 消息回填 ToolCallID/ToolName）、`toolDefsToEino`（ToolDef→ToolInfo，InputSchema 经 eino-contrib/jsonschema 解析）、`einoFinal`（尾帧/聚合 → Final，工具调用增量合并） |
| | `client.go` | `Stream(ctx, []Message, *CallOptions)` / `Generate` 公开面换 Hify 类型；`Stream.Recv()` 返回内容 delta、内部顺手收集全部 eino chunk，`Stream.Final()` 经 `schema.ConcatMessages` 聚合（content 拼接 / 工具调用按 Index 合并 / usage 取 max / finish 取最后非空）——工具调用增量不外露 |
| | `options.go` | `CallOptions.Tools []tool.ToolDef`（不再是 eino ToolInfo）；转换惰性缓存在 opts 内；`ValidateTools` 试跑转换供 chat 预检 |
| | `factory.go` + 各 adapter | 内部 `Streamer` 接口签名同步（eino 类型只在本包与 adapter 间流动） |
| **mcp**（api 契约 + service 桥接） | `api/api.go` | `MCPClientService` 增 `ToolSourceFor(ToolSourceReq) tool.Source`（组合根注入 chat；api 只 import 纯标准库的 platform/tool，零 SDK/eino 类型） |
| | `service/toolsource.go` | `tool.Source` 实现：按绑定 tool id 子集查库 → 调用名派生 `s{serverID}__{toolName}`（sanitize + ≤64）→ Tools() 惰性出 ToolDef；Invoke 经 per-call client（沿管理批形态）执行 CallTool，content blocks 归一拼接、isError 透传为 `ToolResult.IsError` |
| **chat**（工具循环本体） | `service/tools.go`（新） | 工具侧编排：`loadToolRegistry`（降级链：空绑定零 mcp 交互 / 工厂缺位 / id 脏数据 / Registry 失败 / 清单全滤 / eino 转换试跑失败 → 一律 WARN 后照常对话）、`runToolRound`（assistant 中间行 + tool 行落库、tool_call/tool_result 事件、结果截断三处共用一份、args 一次解析三处消费）、`parseToolArgs` / `truncateToolContent` |
| | `service/turn.go`（重写） | 多轮 ReAct 循环：判定纪律**只看 `len(ToolCalls) > 0`**；上限 `maxToolRounds=8` 后追加 nudge（瞬态不落库）+ 摘工具逼出文本终答（`finish_reason=max_iterations`）；轮次 ≥1 失败走 error 事件（SSE 200 已提交）；断连不空转（`round>0` 才检查 ctx）；usage 整轮累计进 done、executions 每轮各记 |
| | `service/service.go` | `toolSourceFactory` 小接口（`ToolSourceFor` 单方法收窄）+ `New` 第 8 参；agent 未绑工具时全程不触碰 |
| | `service/model.go` | ToolCalls 注释更新（assistant 中间行 / tool 行锚点两种形态） |
| | `api/schema.go` | `ToolResultPayload{Content, IsError}`（tool_result 事件载荷，Content 与落库/回喂同一份截断文本）；`ToolCallEvent`/`ToolResultEvent` 启用 |
| **workflow**（机械换型，零逻辑变化） | `service/executor.go` | LLM 节点 `[]*schema.Message` → `[]llm.Message`、`*schema.Message` → `*llm.Final`；eino import 移除 |
| **组合根** | `app/server.go` | `chatsvc.New(..., mcpClientSvc)`——第 8 参注入 mcp 工具来源工厂 |

约束达成（用户钉死的边界）：

- **不改 Controller 层、不改 SSE 逻辑**：handler / SSE 帧格式零改动，`tool_call` / `tool_result` 事件类型沿用 v1 契约预留值；
- **工具列表为空时与原逻辑一致**：`loadToolRegistry` 返 nil → 循环体只走一轮，既有 turn_test.go 全部用例（未注入工具来源）原样通过；
- **RAG 与工具不冲突**：一个改 system prompt、一个进 `CallOptions.Tools`，同一轮可同时生效；
- **workflowId 不为空时已 return**：管道分支在循环之前，零变化；
- **工具失败不中断对话**：`ToolResult{IsError:true}` 落库 / 喂回 / 事件三路一致，LLM 下轮自纠；
- **零迁移、前端零改动**：messages 表 `role='tool'` + `tool_calls` jsonb 既有结构直接复用。

## 2. 与 spec 的偏差

仅一处（行为增强，非偏离语义）：

1. **v1 遗留槽位泄漏修复**：原 citations 事件 emit 失败路径（前端断连）直接 `return nil, nil`，未关流——bulkhead 槽位要等 overall 5min 超时才经后台回收（实测无后台释放 goroutine，`Stream` 清理只发生在 Recv 终点或 Close）。新代码在该 return 前补 `_ = stream.Close()`：与「客户端断连取消上游（省 token）」的既有契约对齐，外部可观察行为不变（同样静默收尾、user 消息已落库）。round 0 的建流失败路径本就无流可关，不受影响。

其余按 spec §5 逐条落地：中间行落库时序（assistant 中间行先行、失败则跳过 tool 行防孤儿）、20000 rune 截断三处共用、args 一次解析、上限 nudge 瞬态、`max_iterations`、`rowsPerTurnEstimate` 5→8。

## 3. 测试与验收

- `go build ./...` 全绿；`go test -race ./...` 全绿（含既有全部用例回归）。
- 覆盖率（touched 包）：chat/service **92.6%**、platform/tool **96.8%**、platform/llm **93.2%**、workflow/service 89.1%、mcp/service 82.0%——全部 ≥ 80%。
- 新增 `internal/chat/service/toolloop_test.go`（spec §9 场景）：
  - **触发链路**：round trip（事件序列 tool_call→tool_result→delta×2→done、中间行 + 终答落库、第二轮上下文含 assistant(tool_calls)+tool、usage 两轮累计、executions 每轮一行、ToolSourceFor 按绑定 id 子集构造）；
  - **失败即上下文**：invoke IsError 照常喂回；参数非法不发起调用（`工具参数不是合法 JSON 对象`）；
  - **上限**：8 轮后第 9 轮摘工具 + nudge（瞬态不落库）+ 强制 `max_iterations`（上限轮仍幻觉调用也收尾）；
  - **降级链**：Registry 构建失败 / 工厂缺位 → 单轮照常对话；
  - **错误轮**：轮次 ≥1 建流失败 → error 事件 + 无终答行 + executions 带 error_class；工具阶段断连 → 静默收尾（不执行剩余调用）；
  - **一次输出模式**：中间行照常落库、终答标准信封返回；
  - 纯函数：`parseToolArgs` / `truncateToolContent` 表驱动。
- 既有测试的 tool-less 回归由 turn_test.go 全体承担（toolSources 恒 nil）。
- 边界 grep（CLAUDE.md《纪律》）：
  - `grep -rn 'cloudwego/eino' internal/ | grep -v platform/llm | grep -v _test` → **空**；
  - `grep -rn 'modelcontextprotocol/go-sdk' internal/ | grep -v mcp/service` → **空**；
  - 工具轮判定全仓无 `switch finish_reason`（grep `FinishReason` 只出现在遥测透传与执行记录）。
- 测试替身策略：`captureStreamer` 保持 eino 类型（测试断言转换后的 eino 报文，隐式覆盖 llm 转换层）；工具来源用 `stubToolSource` / `errToolSource` 可编程 stub。

## 4. 下阶段（明确不在本批）

- **workflow 工具节点接 `mcpapi`**：已交付（spec 016，2026-09-30）——tool 节点经 `toolSourceFactory` 窄面（`ToolSourceFor` 按 ToolID 解析）消费统一 `tool.Source`，fail-fast 错误分型；实现记录 [../workflow/impl_spec_16_tool_node_mcp.md](../workflow/impl_spec_16_tool_node_mcp.md)。README §7 旧草图名 `mcpapi.InvokeTool` 实即 `ToolSourceFor` 路径。
- **前端 tool_call / tool_result 事件渲染**：本批前端零改动（契约预留值已生效）；控制台展示工具调用过程（折叠卡片样式）留待前端批。
- **MCP 会话复用升级**（README §5.2 设计点 2）：chat 每轮对话多次工具调用、per-call 握手开销是否可忽略，待真实 MCP server 手测后再评估 singleflight 重建。
