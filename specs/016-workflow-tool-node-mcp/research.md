# Research: workflow 工具节点接 MCP（spec 016）

> Phase 0 输出。本篇无 NEEDS CLARIFICATION（上位 spec 五项决策已拍板），research 记录决策链与代码现状核实结论，供 plan/tasks 引用。

## D1 执行链路：统一 tool.Source，不直调 CallTool

- **Decision**: executor 定义消费方窄接口 `toolSourceFactory { ToolSourceFor(ctx, mcpapi.ToolSourceReq) tool.Source }`，组合根注入 mcpClientSvc；tool 节点经 `Tools(ctx)` 解析、`Invoke(ctx, call)` 调用。
- **Rationale**: `mcpapi.CallToolReq` 需要 (MCPServerID, ToolName) 二元组，而 ToolConfig 只存 tool_id——直调需先反查二元组（多一次查询 + 新增解析代码）；`ToolSourceFor` 恰好按 ToolID 供源，且与 chat 工具循环同链路，「server 停用即工具消失」语义一份实现。README §7 的「mcpapi.InvokeTool」为旧草图名。
- **Alternatives**: ① CallTool 直调（被否——二元组解析重复造轮子、与 chat 双链路漂移）；② workflow 直接 import mcp/service（被否——违反依赖红线）。
- **代码现状核实**: `internal/mcp/api/api.go:64` `ToolSourceFor(req ToolSourceReq) tool.Source`；`internal/mcp/service/toolsource.go` boundToolSource：`ensure()` 惰性加载（ListToolsByIDs + ListServersByIDs → 过滤停用 → `deriveCallName`），`Invoke` 全路径吞错成 `IsError` 结果（maxErrTextRunes 2000）。

## D2 错误分型：fail-fast（≠ chat 的错误即上下文）

- **Decision**: `Tools()` 返回空清单（工具已删 / server 停用被静默过滤）→ `mcpapi.ErrMCPToolNotFound` 透传 404；`Tools()` DB 错 → `%w` 包装 500；`Invoke` IsError → `mcpapi.ErrMCPToolCallFailed`（500，message = Content）；nil 装配 → 同码「工具来源未注入」。节点失败 → run failed + error_node 定位（既有 walk 机制零改动）。
- **Rationale**: workflow 图**要求**该节点产出，失败必须终止（确定性图语义）；chat 是把错误喂回 LLM 自纠（ReAct），两者语义分野由本决策钉死。前置 `Tools()` 预检中和 boundToolSource.Invoke 的吞错路径——预检通过后 IsError 只剩真实调用失败一种来源。404 与 `/mcp-servers/{id}/run` 端点「实调时工具不在清单」同码同义；工具失效类比 llm 节点 MODEL_NOT_FOUND 执行期透传（spec 06「下游哨兵透传不变」既有规则）。
- **Alternatives**: ① 工具缺失翻译成图缺陷 400（被否——非作者可编辑修复的配置问题，是运行环境缺失，且 23503 系列已有「下游哨兵透传」先例）；② 失败结果照常落池继续走图（被否——静默降级不可排障）。

## D3 args 渲染：严格模板 + 确定性序列化

- **Decision**: `ToolConfig.Args`（`map[string]string`）逐值严格 `{{var}}` 渲染，缺失变量 → `errs.ErrValidationFailed` 带 `args.<参数名>` 定位；渲染后按 key 排序 marshal 成 JSON 对象作 `tool.ToolCall.Arguments`。
- **Rationale**: 对齐 callWorkflow 的 inputs 渲染形态与文案风格（图缺陷 = 作者可修复）；按 key 排序保证轨迹可复现（同一图同一输入恒同一 nodeIn 记录）。
- **Alternatives**: 宽松渲染缺变量传空（被否——静默错数据比 fail-fast 更糟）。
- **代码现状核实**: 渲染工具函数与 callWorkflow 既有实现同文件复用；node 错误统一经 walk 层 `node <key>:` 前缀包装（零改动）。

## D4 轨迹与变量池：沿用既有机制

- **Decision**: `setNodeIn` 记 `{"tool_id": "<字符串化>", "args": <渲染后 JSON>}`；输出 = `ToolResult.Content` 落变量池（下游 `{{node_key}}` 引用）+ node run `Output`（16KB 单值截断既有机制自动生效）；计时/状态沿用 nodeStep。
- **Rationale**: 对齐 callWorkflow 的 inputs 记录形态与所有其他节点的输出落池行为；截断、wrapTraceJSON、DurationMs 全既有，零新机制。工具调用不落 executions（该表 = 每次 LLM 调用一行，工具调用不是 LLM 调用——与 chat 工具循环不落 executions 同语义）。

## D5 Args 值域仅字符串（拍板 A）

- **Decision**: 维持 ToolConfig 冻结契约 `map[string]string` 不动；需要数值/布尔强类型参数的 MCP 工具被 server 端 schema 校验拒绝时表现为 MCP_TOOL_CALL_FAILED（错误文本可见可排障）。
- **Rationale**: 用户拍板（2026-09-30）——MCP 在 workflow 中以「取数喂 LLM prompt / 条件路由」为主，图作者手写静态参数与 api 节点（headers/body 全字符串）同哲学；ReACT 参数生成路径（chat / 未来 agent 节点）不经 ToolConfig.Args，天然不受限。方案 B（拓宽 `map[string]any`，wire 向后兼容）记为升级路径，真遇类型瓶颈再议契约修订。

## 组合根与注入链（现状核实）

- `internal/app/server.go:132` `mcpServerSvc, mcpClientSvc := mcpsvc.New(...)` 构建先于 workflow（:152 附近），直接追加传参零重排。
- `internal/workflow/service/service.go:96-107` 现 `New(store, models, kbs, cm, clients, execs, rags, blockPrivate)` 8 参 → 增第 9 参 `toolSources toolSourceFactory`（rags 后、blockPrivate 前）；`s.exec = newExecutor(models, clients, execs, rags, toolSources, blockPrivate)`。
- `exec_doubles_test.go` 既有 stubResolve / stubRetrieve / stubClientFactory 形态 → 新增 `stubToolSourceFactory` 同模式。

## 测试现状与替换面

- `executor_test.go:575` 与 `execute_test.go:257`：spec 06 的「tool 未支持 → 图缺陷 400」占位断言——本 spec 授权删除，由新用例组取代（六类单元 + 两类图级端到端）。
- chat 模块测试零改动——回归绿即证明管道透传受益（chat 管道经 execChild 触发 workflow Execute，工具节点在 executor 内闭环）。
