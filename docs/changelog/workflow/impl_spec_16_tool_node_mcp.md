# Workflow 实现 spec 16：工具节点接 MCP（统一 tool.Source 执行）

> 状态：**已交付**（2026-09-30 实施完毕；spec 同日批准，D5 拍板 = A 仅字符串）
> 上位契约：[api_contract.md](./api_contract.md)（§4 ToolConfig 冻结契约 / 节点类型全集 7 类）、[impl_spec_06_execution_engine.md](./impl_spec_06_execution_engine.md)（O4 执行期 fail-fast 拍板、错误二分法、运行落库与 16KB 截断保真）、CLAUDE.md《统一工具与消息模型》（platform/tool 契约 + 纪律 grep）、《错误处理》错误码表（MCP_TOOL_NOT_FOUND / MCP_TOOL_CALL_FAILED 既有行）。
> 前置依赖：chat 工具循环批已合入（`0d6293f`——platform/tool 统一契约、mcp `ToolSourceFor` 落地）；[chat_tool_loop_impl.md](../mcp/chat_tool_loop_impl.md) §4 已记录方向「工具节点按统一 tool.Source 模型对齐」。
> 迁移假设：**零迁移**（`make migrate-status` 21 applied、下一号 00022 全程不变）；零新增第三方依赖；零新增路由 / 哨兵 / SSE 事件 / api schema；前端零改动。

## 1. 背景

spec 06 落地执行引擎时 MCP 未建，tool 节点在 `runNode` 分发里是占位 fail-fast（图缺陷 400「mcp 未建」）。chat 批交付统一工具契约后，mcp 模块已能按 ToolID 供出 `tool.Source`（`mcpapi.ToolSourceFor`）——mcp/README.md §7 最后一项即此。本篇把 ToolConfig 分支从占位换成真实执行：工具解析 → args 严格渲染 → 单次确定性调用 → 结果落变量池。存量图零迁移（ToolConfig 契约不动）、chat 管道零改动（管道内嵌 workflow 的工具节点随之打通，`execChild` 路径零感知）。

## 2. 做什么（范围）

| # | 交付物 | 文件 | 要点 |
|---|---|---|---|
| 1 | `toolSourceFactory` 窄接口 + `callTool` 执行分支 | `internal/workflow/service/executor.go` | 替换占位 fail-fast；D1–D4 全部语义 |
| 2 | 注入链扩参 | `internal/workflow/service/service.go` | `New` 增第 9 参 + `newExecutor` 透传；既有构造点同步 |
| 3 | 组合根传参 | `internal/app/server.go` | `workflowsvc.New(..., mcpClientSvc, blockPrivate)`——mcp 在 workflow 前构建，零重排 |
| 4 | 测试替身 | `internal/workflow/service/exec_doubles_test.go` | `stubToolSourceFactory`（可控 Tools/Invoke） |
| 5 | 用例组 | `internal/workflow/service/executor_test.go` + `execute_test.go` | §7 清单；**替换** spec 06 的 tool fail-fast 旧断言（executor_test.go:575 / execute_test.go:257） |

## 3. 不做什么（边界）

- **编辑器 tool 节点面板**：画布 palette 仍 5 类，tool 与 knowledge_retrieval 继续 JSON 手写携带、透传保存（spec 09 先例）；单独前端篇再开。
- **保存期 tool_id 存在性预检**：O4 拍板保真——图校验零改动（保存含 tool 节点的图合法），工具缺失由执行期 404 兜底（D2）。
- **ReAct 语义**：tool 节点 = 图定义的确定性单次调用，结果落池按边游走；LLM 自主决定调工具是 chat 工具循环的能力，不进 workflow。
- **进度事件**（执行中 SSE 粒度推送）：deferred_items #2 维持暂缓（触发条件不变）。
- **executions 落库**：工具调用不是 LLM 调用（executions 表语义 = 每次 LLM 调用一行）；节点轨迹已记 input/output/duration，足够排障。
- **MCP 会话复用**：per-call client 现状不变（独立遗留项，前置 = 真实 server 手测）。

## 4. 关键决策（拍板项）

### D1 执行链路走统一 tool.Source，不直调 CallTool

executor 增窄接口（与 chat `loadToolRegistry` 消费形态一致，Go 结构化类型天然满足 `mcpapi.MCPClientService` 实现）：

```go
// executor.go —— 消费方窄接口（小接口惯例，实为 mcpapi.MCPClientService 子集）
type toolSourceFactory interface {
    ToolSourceFor(ctx context.Context, req mcpapi.ToolSourceReq) tool.Source
}
```

〔交付修订〕落地签名为**无 ctx**：`ToolSourceFor(req mcpapi.ToolSourceReq) tool.Source`——chat 批已交付的 `mcpapi.MCPClientService` 契约构造即无 IO（ctx 在 `Source.Tools` / `Source.Invoke` 方法上），本篇草图的 ctx 形态属当时未对齐代码现状的残留；plan.md 记录的即无 ctx 终形，实现照此。

- 理由：`CallToolReq` 需要 (MCPServerID, ToolName) 二元组，而 ToolConfig 只存 `tool_id`；`ToolSourceFor` 恰好按 ToolID 解析、且与 chat 同链路复用「server 停用即工具消失」的既有语义。README §7 写的「mcpapi.InvokeTool」是旧草图名，实际即此路径。
- 依赖方向不变：workflow → mcp 只 import `mcp/api`（既有清单允许）。

### D2 工具解析与调用语义——workflow 是 fail-fast，不是「错误即上下文」

chat 工具循环把工具失败喂回 LLM 自纠；workflow 图**要求**该节点产出，失败必须终止 run。分型：

| 路径 | 判定 | 结果 |
|---|---|---|
| `Tools(ctx)` 返回空清单 | 工具已删 / server 已删或停用（boundToolSource 静默过滤） | `mcpapi.ErrMCPToolNotFound` 透传（**404**，与 `/run` 端点「实调时工具不在清单」同码同义） |
| `Tools(ctx)` 返回 DB 错误 | ListToolsByIDs / ListServersByIDs 失败 | `%w` 包装上抛（500） |
| `Invoke` 返回 `IsError=true` | 工具执行失败 / 参数校验失败 | `mcpapi.ErrMCPToolCallFailed`（**500**，message = Content 截断文本）→ run failed、`error_node` 定位该节点 |
| 装配缺位（`toolSources == nil`，仅测试可达） | 组合根未注入 | `ErrMCPToolCallFailed` 包装「工具来源未注入」（环境限制类 500，fail-fast 不静默降级——图要求该工具，无 chat 式降级路径） |

前置 `Tools()` 预检正是为中和 `boundToolSource.Invoke` 的吞错路径（加载失败也返回 IsError 结果）；预检通过后 Invoke 的 IsError 只剩真实调用失败一种来源。所有错误经既有 `node <key>:` 前缀机制包装（walk 层，零改动）。

### D3 args 渲染 = 严格模板（图缺陷类 400）

- `ToolConfig.Args`（`map[string]string`，值支持 `{{var}}`）逐值**严格**渲染：缺失变量 → `errs.ErrValidationFailed`（400，定位 `node <key>: args.<参数名>`——对齐 callWorkflow 的 inputs 渲染形态与文案风格）。
- 渲染后按 key 排序 marshal 成 JSON 对象，作为 `tool.ToolCall.Arguments`（确定性序列化，轨迹可复现）。
- 调用名即 `ToolDef.Name`（`s{serverID}__{toolName}` 派生），由 ToolSource 侧给出，executor 不自造。

### D4 节点输入 / 输出与轨迹

- `setNodeIn`：`{"tool_id": "<字符串化>", "args": <渲染后 JSON>}`（对齐 callWorkflow 的 inputs 记录形态）。
- 输出 = `ToolResult.Content` 落变量池（下游 `{{node_key}}` 引用）+ node run `Output`（16KB 截断既有机制自动生效）。
- 计时 / 状态沿用 nodeStep 机制（succeeded + DurationMs）；失败路径 error_node 定位零改动。

### D5 Args 值域仅字符串（拍板 A）

ToolConfig 冻结契约 `Args map[string]string` 决定 workflow 侧工具参数**只能是字符串值**；要求数值 / 布尔类型参数的 MCP 工具可能被 server 端 schema 校验拒绝（表现为 `MCP_TOOL_CALL_FAILED`，错误文本可见可排障）。

**拍板（2026-09-30，用户：维持 A）**。理由：MCP 在 workflow 中的使用以「与 LLM 节点配合取数喂 prompt / 条件路由」为主，图作者手写静态参数与 api 节点（headers / body 全字符串）同哲学；ReACT 形态在 chat 侧（LLM 按 `ToolDef.InputSchema` 生成任意 JSON，不经 `ToolConfig.Args`），将来 workflow 若做 ReACT / agent 型节点同理，天然不受字符串限制。会咬人的仅剩强类型 InputSchema 的工具，选型避开即可。方案 B（拓宽 `map[string]any`，wire 向后兼容、渲染只作用于字符串值）作为未来升级路径记录——真遇到类型瓶颈再议契约修订。

## 5. 行为语义（成功路径时序）

```
runNode(tool) 
  → toolSources==nil? → ErrMCPToolCallFailed「工具来源未注入」(500)
  → src = ToolSourceFor({ToolIDs: [cfg.ToolID]})
  → defs = src.Tools(ctx)          // 空 → ErrMCPToolNotFound(404)；err → %w(500)
  → args = 渲染 cfg.Args            // 缺失变量 → ErrValidationFailed(400, args.<k> 定位)
  → setNodeIn{tool_id, args}
  → res = src.Invoke(ctx, ToolCall{ID: 本地生成锚点, Name: defs[0].Name, Arguments: args})
  → res.IsError? → ErrMCPToolCallFailed(res.Content 截断)(500) → run failed + error_node
  → setNodeOut(res.Content)         // 落变量池 + node run Output（16KB 截断自动）
  → nodeStep{succeeded, DurationMs}
```

## 6. 任务清单（TDD 粒度）

1. `exec_doubles_test.go`：stubToolSourceFactory（可控 Tools 清单 / Tools 错误 / Invoke 结果）。
2. executor_test.go：RED——新增 tool 节点用例组（§7 清单 1–6）+ 替换 :575 旧 fail-fast 断言；GREEN——executor.go `toolSourceFactory` 接口 + `callTool` 实现（D1–D4）。
3. service.go：`New` 签名扩第 9 参（rags 后、blockPrivate 前）+ `newExecutor` 透传；service_test.go 既有构造点机械更新。
4. execute_test.go：图级端到端用例（§7 清单 7）+ 替换 :257 旧断言；GREEN 由 2 已完成。
5. server.go：组合根追加 `mcpClientSvc` 传参。
6. 全量门禁 + 覆盖率 + 依赖 grep（§8）。

## 7. 测试清单（executor_test.go / execute_test.go，stub 隔离零真实网络）

1. 正常链路：args 渲染 → Invoke → Content 落池 → 下游节点 `{{tool_key}}` 可引用；nodeIn 记 `tool_id` + args JSON；node run Output 落值。
2. 工具缺失（Tools 返回空）→ `ErrMCPToolNotFound` 透传 + node key 前缀。
3. Invoke IsError → `ErrMCPToolCallFailed`（message 含结果文本）+ run failed + error_node 定位该节点。
4. args 缺失变量 → `ErrValidationFailed` + `args.<参数名>` 定位（图缺陷 400）。
5. Tools DB 错误 → `%w` 包装 500。
6. `toolSources == nil` → `ErrMCPToolCallFailed`「工具来源未注入」。
7. 图级端到端：`llm → tool → end`（工具输出进终答 prompt 模板）；condition 节点按 tool 输出路由二选一。
8. **替换性回归**（spec 06 旧断言换代，行为变更由本 spec 授权）：executor_test.go:575、execute_test.go:257 的「tool 未支持 → 图缺陷 400」删除，由 2/3 取代。
9. chat 批回归：`internal/chat/...` 全量测试绿（管道内嵌 workflow 的工具节点透传受益，chat 代码零改动）。

## 8. 验收门

- `go build ./... && go vet ./... && go test ./... -race -count=1` 全绿；`workflow/service` 覆盖率 ≥80%。
- 依赖方向 grep：`internal/workflow/` 不 import `internal/mcp/service`；`modelcontextprotocol/go-sdk` 不出 `internal/mcp/service/`；`cloudwego/eino` 不出 `internal/platform/llm/`（test 除外）。
- `make migrate-status` 21 applied 不变；既有迁移文件零改动。
- §2 交付物逐项存在性核对；§7 清单逐项对号。

## 9. 文档同步

- CLAUDE.md《错误处理》表：`MCP_TOOL_NOT_FOUND` / `MCP_TOOL_CALL_FAILED` 行补 workflow tool 节点触发场景注记（既有行加注，不加新行）。
- [mcp/README.md](../mcp/README.md) §7：勾销最后一项；§5.3 落位图注记本篇。
- [chat_tool_loop_impl.md](../mcp/chat_tool_loop_impl.md) §4：workflow 工具节点项指向本篇。
- [deferred_items.md](./deferred_items.md)：#2 进度事件的触发表述更新（工具节点已通，仍暂缓）。

## 可提交节点

```
feat(workflow): tool 节点接 mcp——统一 tool.Source 执行 + fail-fast 错误分型（spec 016）
```
