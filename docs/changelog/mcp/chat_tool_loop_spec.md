# chat 工具循环与统一工具 / 消息模型（spec 02：chat 批）

> 状态：**定稿并已实施**（2026-09-29）。本文是 mcp changelog 的 chat 批 spec：MCP 工具接入 chat 对话引擎（多轮 ReAct 循环）+ 全局统一工具模型（`platform/tool`）+ LLM 消息模型收敛（`platform/llm` 公开面 Hify 化）。管理批（server 管理 + agent 绑定）见 [server_mgmt_spec.md](server_mgmt_spec.md) / [server_mgmt_impl.md](server_mgmt_impl.md)；SDK 选型与「MCP 层单实现」原则见 [README.md](README.md)。
>
> 用户已确认的三条架构决定（2026-09-29）：
> 1. **统一工具模型是全局的**：`ToolDef` 不只统一 MCP 工具，未来内置工具（builtin tools）也是同一模型；
> 2. **ReAct 循环 / workflow 等业务编排不依赖任何三方模块**：eino（`cloudwego/eino`）收敛到 `platform/llm` 内部，go-sdk 收敛到 `mcp/service` 内部，业务层只见 Hify 自己的接口与模型；
> 3. 统一 tools 定义与 message 定义**写进 CLAUDE.md**（《统一工具与消息模型》节，本批一并落盘）。

## 0. 前置与约束（用户钉死的边界）

- 工具列表为空时（agent 未绑工具），chat 行为与原路径**完全一致**——不查 mcp、不建 registry、`CallOptions.Tools` 为空；
- RAG 注入与工具调用**不冲突**：system prompt 增强（RAG）与 tools 参数（工具）各走各的，同一轮可同时生效；
- 绑定 workflow 的 agent 已在 `runWorkflowTurn` 提前 return，**不进本逻辑**；
- **工具调用失败不抛异常中断对话**：错误文本作为 tool 消息喂回 LLM，由 LLM 告知用户（多轮循环天然自纠——LLM 看到错误可换工具 / 换参数 / 直接道歉）；
- **不改 Controller 层（handler）、不改 SSE 协议帧**：`tool_call` / `tool_result` 事件 v1 已预留（chatapi 事件构造器已存在），本批只是开始发出它们；
- **零迁移**（表结构 00021 已备：messages.role 已含 'tool'、tool_calls jsonb 已有）；前端零改动（事件契约先冻结，前端渲染批次另行处理）。

## 1. 三层架构（本批的核心结构决定）

```
┌─ 消费层 ──────────────────────────────────────────────────────────┐
│ chat/service（ReAct 循环）、workflow/service（LLM 节点）            │
│ 只见：tool.ToolDef / tool.ToolCall / tool.ToolResult / tool.Source │
│       llm.Message / llm.Final / llm.CallOptions                    │
│ 禁止 import：eino、go-sdk（见 §8 grep 规则）                        │
└──────────────┬───────────────────────────────────┬────────────────┘
               │                                   │
┌─ 适配层 ─────▼──────────────────┐  ┌─ 适配层 ─────▼───────────────┐
│ platform/llm                     │  │ mcp/service                  │
│ eino / eino-ext 唯一居所：        │  │ go-sdk 唯一居所（选型决策）： │
│ Message↔schema.Message 转换、    │  │ tool.Source 实现（boundTool-  │
│ ToolDef→ToolInfo、流聚合 Final   │  │ Source：调用名派生 + 路由）    │
│ （保护链 bulkhead/熔断/超时不动） │  │                              │
└──────────────┬──────────────────┘  └──────────────┬───────────────┘
               │                                    │
┌─ 契约层 ─────▼────────────────────────────────────▼───────────────┐
│ platform/tool：ToolDef / ToolCall / ToolResult / Source / Registry │
│ 纯标准库叶子包（同 platform/schema 形态），mcp/api 可 import         │
└───────────────────────────────────────────────────────────────────┘
```

要点：

- **`platform/tool` 是全局统一工具契约**，不属于 mcp 模块。MCP 工具是实现该契约的第一个 Source；未来内置工具（如时间、计算器）直接再实现 `tool.Source`，chat / workflow 侧零改动。
- **eino 类型止步于 `platform/llm`**：`Streamer` 内部接口保持 eino 类型（不动 eino-ext 适配层），但 `Client` 公开面（Stream/Generate/CallOptions）全部换成 Hify 类型，转换在 Client 边界一次完成。
- **go-sdk 类型止步于 `mcp/service`**（管理批既定），本批新增的 `ToolSourceFor` 返回 `tool.Source`（platform 契约），api 层零 SDK 依赖。

## 2. platform/tool 契约（新包，纯标准库）

```go
// internal/platform/tool/tool.go
package tool

// ToolDef 统一工具定义（全局）：MCP 工具与未来内置工具同用此模型。
// Name 是发给 LLM 的调用名，须在单次调用的工具清单内唯一。
type ToolDef struct {
    Name        string          // 调用名（MCP 侧派生 s{serverID}__{toolName}，见 §4）
    Description string
    InputSchema json.RawMessage // 参数 JSON Schema；顶层须为对象，空为 {}
}

// ToolCall LLM 发起的一次工具调用请求（模型输出，vendor 无关）。
type ToolCall struct {
    ID        string          // 调用 id（LLM 生成；tool 结果消息回填的锚点）
    Name      string          // = ToolDef.Name
    Arguments json.RawMessage // 调用参数；空 / null / {} = 无参
}

// ToolResult 工具执行结果（回喂 LLM 的观察）。
// 失败不通过 error 中断对话——IsError=true 且错误文本在 Content（错误即上下文）。
type ToolResult struct {
    CallID  string // 对应 ToolCall.ID
    Name    string // 对应 ToolCall.Name
    Content string // 归一文本（MCP：TextContent 多条 "\n" 拼接）
    IsError bool   // true = 执行失败（喂回 LLM 自纠，不是异常）
}

// Source 工具来源：供出一组工具并执行调用。实现：mcp/service（MCP）；
// 未来内置工具直接实现本接口。生命周期 = 一次对话轮（chat 每轮构建）。
type Source interface {
    // Tools 供出全部工具定义（空切片 = 无工具）。惰性加载：首次调用时查库。
    Tools(ctx context.Context) ([]ToolDef, error)
    // Invoke 执行一次调用，按 Name 路由回真实工具。恒返回 ToolResult：
    // 未知工具 / 调用失败 / 上游 isError 一律 IsError=true 的结果，不返回 error。
    Invoke(ctx context.Context, call ToolCall) ToolResult
}

// internal/platform/tool/registry.go
// Registry 聚合各来源工具：构建时校验（InputSchema 合法性、调用名唯一），
// Invoke 按调用名路由回来源。chat 每轮对话构建一次。
type Registry struct { ... }
func NewRegistry(ctx context.Context, sources ...Source) (*Registry, error)
func (r *Registry) Defs() []ToolDef          // 拷贝（防调用方改动内部状态）
func (r *Registry) Invoke(ctx context.Context, call ToolCall) ToolResult
```

`NewRegistry` 校验（任一失败即整体失败，调用方降级为无工具）：

- 每个 `ToolDef.InputSchema` 必须是**合法 JSON 且顶层为对象**（`{}` 允许）——垃圾 schema 在这里挡下，而不是流到 LLM 调用里炸；
- 调用名不得重复（重复 = 路由歧义，直接报错）；
- 任何 Source 的 `Tools()` 失败 → 整体失败。

`Registry.Invoke` 对未知调用名返回 `IsError=true` 的结果（"unknown tool: xxx"），不 panic 不 error——LLM 幻觉出未提供的工具名是真实场景。

## 3. platform/llm 公开面重构（eino 收敛）

### 3.1 新公开类型（`types.go`，业务层唯一的 LLM 消息形态）

```go
type Role string
const (
    RoleSystem    Role = "system"
    RoleUser      Role = "user"
    RoleAssistant Role = "assistant"
    RoleTool      Role = "tool"
)

// Message 统一消息模型：chat / workflow 组装上下文与消费结果的唯一形态。
// ToolCalls 仅 assistant 消息携带；ToolCallID / ToolName 仅 tool 消息携带。
type Message struct {
    Role       Role
    Content    string
    ToolCalls  []tool.ToolCall
    ToolCallID string
    ToolName   string
}

// Usage token 用量（一次生成的最终计量；多轮循环由 chat 逐轮累加）。
type Usage struct {
    Input  int64
    Output int64
}

// Final 一次生成的终态：流式（Stream.Final，chunk 聚合）与非流式（Generate 返回）
// 同一形态——Content / Usage / FinishReason / ToolCalls。
// FinishReason 供应商原样透传（stop / tool_calls / STOP / …，仅遥测）；
// 是否进入工具轮的判定纪律：只看 len(ToolCalls) > 0，不看 finish_reason
//（各厂字符串不同，vendor 差异留在 adapter）。
type Final struct {
    Content      string
    Usage        Usage
    FinishReason string
    ToolCalls    []tool.ToolCall
}
```

### 3.2 公开签名变化（内部保护链零改动）

| 项 | 原 | 新 |
|---|---|---|
| `Client.Stream` | `(ctx, []*schema.Message, *CallOptions) (*Stream, error)` | `(ctx, []Message, *CallOptions) (*Stream, error)` |
| `Stream.Recv` | `() (*schema.Message, error)` | `() (string, error)`（内容 delta；工具调用增量被内部聚合，不外露） |
| `Stream.Final` | —（无） | `() (Final, error)`（EOF / 错误后可调；聚合失败返回 error，内容降级零值） |
| `Client.Generate` | `(ctx, []*schema.Message, *CallOptions) (*schema.Message, error)` | `(ctx, []Message, *CallOptions) (*Final, error)` |
| `CallOptions.Tools` | `[]*schema.ToolInfo` | `[]tool.ToolDef` |

- `Streamer` 内部接口**保持 eino 类型不动**（eino-ext 适配层零改动；chat 测试的 captureStreamer double 继续可用）；
- 转换（`convert.go`）：`toEinoMessages`（含 tool 消息的 `ToolMessage(content, callID)` + ToolName 回填）、`ToolDef → *schema.ToolInfo`（`json.Unmarshal` 进 `eino-contrib/jsonschema` 的 `*jsonschema.Schema`（其 orderedmap 实现 json.Unmarshaler，嵌套 properties 可解析）→ `schema.NewParamsOneOfByJSONSchema`）、eino 尾帧 `ResponseMeta` / 增量 `ToolCalls` → `Final`；
- `CallOptions.einoOptions()` 增加 error 返回 + **惰性缓存**（转换一次全调用链复用）；`Client.Stream` / `Generate` 入口先调它做校验——**坏的 tool schema 在抢槽 / 熔断之前失败**，不占槽位、不进熔断计数（未分类错误路径不污染 breaker）；
- `Stream` 聚合用 `schema.ConcatMessages`（eino 自带的 chunk 合并：content 拼接、tool_calls 按 Index 合并 / Arguments 拼接、usage 取 max、finish_reason 取最后非空）——聚合语义直接复用库的测试过的实现，不自写合并器；`Recv` 返回的 string 即该 chunk 的 Content；
- 三层超时 / bulkhead / 熔断 / 重试（`client.go` 内部）**一行不动**。

### 3.3 chat 侧的辅助校验出口

`llm.ValidateTools(defs []tool.ToolDef) error`——即 ToolDef→ToolInfo 转换的试跑。chat 在进循环前调用：转换失败的（理论罕见——Registry 已挡 JSON 层）WARN + 摘掉工具照常对话。与 `CallOptions` 惰性缓存共用同一实现，不二次转换。

## 4. mcp 侧 ToolSourceFor（统一 Source 的 MCP 实现）

### 4.1 契约（mcp/api 新增，零 SDK 依赖）

```go
// MCPClientService 新增方法（工厂方法，无 IO，不签名 (ctx, req) 形态）：
// ToolSourceFor 按绑定工具 id 子集返回统一工具来源（platform/tool.Source）。
// 构造无 IO；真实加载（查库）在 Source.Tools / Invoke 的 ctx 上惰性发生。
ToolSourceFor(req ToolSourceReq) tool.Source

type ToolSourceReq struct {
    ToolIDs []uint64 // agent 绑定的 mcp_tools.id（已由 agent 模块校验存在性）
}
```

`mcp/api` import `platform/tool`：纯标准库 platform 叶子包，同 `platform/schema` 先例，不破坏 api 纯度规则。

### 4.2 实现（mcp/service/toolsource.go，go-sdk 仍不出包）

```
boundToolSource{svc *service, toolIDs []uint64}
  ├─ ensure(ctx)：ListToolsByIDs + ListServersByIDs → resolved[]（server 过滤
  │   enabled=false 的工具行）→ 生成 ToolDef；首次成功后缓存
  │   （chat 单轮内 Tools 与 Invoke 共用，不重复查库）
  ├─ Tools(ctx)：ensure → []ToolDef
  └─ Invoke(ctx, call)：ensure → 按 call.Name 路由 → 未知名 IsError 结果；
      命中 → svc.getServer（Cache-Aside）→ dialer.call（per-call client）
      → 失败 / isError：ToolResult{IsError:true, Content: 截断后的错误文本}
      → 成功：ToolResult{Content: TextContent 拼接}
```

**调用名派生（防跨 server 工具重名）**：`s{serverID}__{toolName}`——server 前缀保证跨 server 唯一（server 内 uq(name) 兜底原始名唯一）；`toolName` 过滤为 `[a-zA-Z0-9_-]`（其余字符替换 `_`），整体截断 ≤64 字符（对齐 OpenAI 函数名约束；其余供应商等宽或更宽松）。派生规则**运行期计算不落库**（管理批已定，db_model §2.1）；同轮内派生名撞车（极端 sanitize 碰撞）→ Registry 构建失败 → chat 降级无工具。

**元数据来源**：工具清单 / 参数 schema 来自 `mcp_tools` DB 行（discover 入库的快照），**不实时 list_tools**——DB 是绑定关系的锚点（agent_tools.tool_id），运行时拉全量清单反而会漂移。

**enabled 过滤**：server 停用 → 该 server 的工具从 ToolDef 清单静默消失（绑定校验已挡新增绑定；存量绑定在 server 停用后自动失效）。Invoke 不再查 enabled（清单已过滤；中途停用放行最后一次调用无碍）。

## 5. chat 多轮 ReAct 循环（turn.go 重构）

### 5.1 流程（伪代码）

```
runTurn（原路径部分）:
  setupConvAgent → (workflow 分支不变) → setupLLMClient → loadHistory
  → 落 user 消息 + 标题回填（不变）
  → buildSystemPrompt（RAG，不变）
  → registry = loadToolRegistry(agent)          # 新增：nil = 无工具，原路径语义
  → msgs = assembleMessages(sysPrompt, history, content)   # []llm.Message
  → opts = callOptions(agent); if registry != nil { opts.Tools = registry.Defs() }

  for round := 0; ; round++ {
      if ctx.Err() != nil → clientGone 收尾                       # 断连不空转
      if round == maxToolRounds:                                  # 上限触发：优雅收尾
          msgs += {user, nudge「请基于以上工具返回的信息，直接回答用户的问题；不要再调用工具。」}
          opts = callOptions(agent)                               # 无 Tools，逼出文本终答
          finishReason = max_iterations
      stream, err := client.Stream(ctx, msgs, opts)
      err → round0 且未 emit：原路径错误处理（标准信封）；否则 callErr 跳出
      round0 且有引用 → citations 事件（首个 delta 前，不变）
      content, usage, toolCalls = consumeStream(stream, emit)     # delta 逐个 emit + 聚合
      recordExecution(本轮)                                        # 每轮一行
      if len(toolCalls) == 0:                                     # 判定纪律：只看 ToolCalls
          终答 → break 正常收尾
      # ---- 工具轮 ----
      落 assistant 中间行（content + tool_calls jsonb）            # 下轮上下文重放用
      for each call:
          emit tool_call 事件
          result = registry.Invoke(ctx, call)                     # 失败也是 IsError 结果
          result.Content 按 20000 rune 截断（三处共用同一份截断文本：
              回喂 LLM / 落库 tool 行 / SSE tool_result 事件）
          emit tool_result 事件
          落 tool 行（ToolCalls[0]["id"] 存 call id，重放锚点）
      msgs += assistant 消息 + 各 tool 消息                        # 下一轮请求即 ReAct 上下文
  }

  收尾（统一）：终答 / 部分内容落 assistant 行 → touch → recordExecution（错误轮）
    → 错误路径：clientGone 静默 / 已 emit 则 ErrorEvent / 未 emit 标准信封
    → 正常路径：done 事件（message_id + 累计 usage + finish_reason）
```

### 5.2 关键参数与语义

| 项 | 值 | 说明 |
|---|---|---|
| `maxToolRounds` | **8**（chat 引擎常量，不进 DB） | 8 轮工具调用后强制收尾；每轮可并行多个调用（一次 assistant 消息的多 ToolCalls） |
| 上限触发的收尾 | nudge + **无工具**最后一轮 | 追加 user 短消息引导作答；opts.Tools 置空逼出文本；`finish_reason = "max_iterations"` |
| nudge 消息 | 不落库（瞬态） | 只存在于本轮 LLM 请求；下轮 loadHistory 从库里取，天然不含 |
| 工具结果截断 | 20000 rune | 回喂 LLM / 落库 / SSE 事件三处共用同一份截断文本（先截断再分发，三处一致） |
| args 解析 | call.Arguments 反序列化一次 | 非法 JSON → 不发起调用，直接 `IsError` 结果「工具参数不是合法 JSON 对象」喂回 LLM |
| usage | 逐轮累加 | done 事件 / SendMessage 返回的是整轮累计；executions 每轮各记各的 |
| `rowsPerTurnEstimate` | 5 → **8** | 工具轮一行变多行（assistant 中间行 + tool 行），窗口估计放宽；截断仍以 user 锚点整轮判定 |
| 中间行落库 | 两模式一致 | 一次输出模式（emit=nil）同样落 assistant(tool_calls) / tool 行（下轮上下文完整），只是不推送事件 |

### 5.3 错误路径（比 v1 多了「轮次」维度）

| 失败点 | 处理 |
|---|---|
| round 0、首次 emit 前 | 原路径不变：pre-attempt（抢槽 / 熔断 / 工厂）不落 executions 直接翻译；其余 record + translate，标准错误信封 |
| round ≥ 1 的 Stream 失败 / 流中失败 | 已 emit（SSE 200 已提交）→ `error` 事件（code + retryable）关流；一次输出模式 → 标准信封。已落的中间行保留（下轮上下文完整，assistant(tool_calls) 后必有 tool 行，重放合法） |
| 工具调用失败 | **不中断**：IsError 结果喂回 LLM（§2 契约），下一轮 LLM 自行决策（换参数 / 换工具 / 道歉） |
| 客户端断连 | emit 失败或 ctx 取消 → 静默收尾（部分内容照常落库；不再进下一轮） |

### 5.4 降级链（mirror KB 先例：工具不是对话硬依赖）

| 场景 | 行为 |
|---|---|
| agent 未绑工具（ToolIDs 空） | `registry = nil`，不查 mcp、`opts.Tools` 为空——**与原路径完全一致** |
| ToolIDs 含非数字（脏数据防御） | WARN + 无工具照常对话 |
| Registry 构建失败（schema 垃圾 / 名字撞车 / DB 错） | WARN + 无工具照常对话 |
| 绑定工具的 server 已停用 | 该 server 的工具静默从清单消失（其余工具照常） |
| 工具全部被过滤后清单为空 | registry 置 nil，同无工具路径 |

## 6. workflow 机械替换（executor.go）

`callLLM` 是纯单轮调用（System? + User → Generate → 校验非空），无工具、无 SSE：

- `[]*schema.Message` → `[]llm.Message`；`*schema.Message` 返回 → `*llm.Final`；
- `reply.ResponseMeta.Usage` → `reply.Usage`；`reply.Content` 不变；
- 删除 eino import。语义零变化（工具节点走 mcpapi 是后续 workflow 批的事，本批不动）。

## 7. 组合根

`internal/app/server.go`：`chatsvc.New(...)` 追加 `mcpClientSvc`（mcp `New` 已返回的第二张面孔，注入为 chat 的 `toolSourceFactory` 窄接口）。其余装配不变。

## 8. grep 可验证的边界规则（同时写入 CLAUDE.md）

- eino（`github.com/cloudwego/eino`）只允许出现在 `internal/platform/llm/`：
  `grep -rn 'cloudwego/eino' internal/ | grep -v 'platform/llm' | grep -v '_test'` 须为空；
- go-sdk（`modelcontextprotocol/go-sdk`）只允许出现在 `internal/mcp/service/`（管理批既定）；
- `tool.ToolDef` 是全局唯一工具模型：禁止任何模块自定义「发给 LLM 的工具」结构再自行转换；
- 工具轮判定只看 `len(ToolCalls) > 0`，禁止 switch finish_reason 字符串（vendor 差异）。

## 9. 测试计划

- **platform/tool**：Registry 构建（合法 / 垃圾 schema / 重名 / Source 失败）、Invoke 路由（命中 / 未知名）、Defs 拷贝语义；
- **platform/llm**：types 转换（含 tool 消息回填、ToolDef→ToolInfo 嵌套 schema）、options 惰性缓存与校验时机（坏 schema 不占槽——断言 bulkhead 未消耗）、Stream 聚合（delta 流 + 尾帧 usage/finish、tool_calls 增量合并、EOF 后 Final、错误后 Final 部分聚合）、Generate → Final；既有保护链测试（606 行 client_test 等）适配新签名后全数保留通过；
- **mcp/service**：boundToolSource（调用名派生 / sanitize / 截断 64、enabled 过滤、未知名 IsError、调用失败 IsError 截断文本、ensure 缓存）；
- **chat/service**（用户给定的手测逻辑对应到单测）：
  1. **无工具回归**：ToolIDs 空的 agent 全链路与改造前行为一致（delta / done / usage / 落库行数）；RAG 注入与 workflow 分支回归；
  2. **工具触发**：脚本化 captureStreamer 首轮吐 tool_calls → 断言 tool_call/tool_result 事件序、中间行落库（assistant(tool_calls) + tool 行）、第二轮上下文含 tool 消息、终答 done；
  3. **多轮迭代**：连续两轮 tool_calls 后终答；上限路径（8 轮后 nudge、无 Tools、finish_reason=max_iterations、nudge 不落库）；
  4. **工具失败**：Invoke 返回 IsError → 喂回 LLM（下一轮请求里 tool 消息含错误文本）、对话不中断；
  5. **降级**：Registry 构建失败 → 无工具照常对话；
  6. **一次输出模式**：中间行落库、无事件推送、标准信封错误。
- **全量**：`go test -race ./...` + 覆盖率 ≥ 80%。

## 10. 明确不做（本批边界）

- 前端工具事件的 UI 渲染（契约先冻结，前端批次接）；
- workflow 工具节点接 mcpapi.InvokeTool（workflow 批）；
- 会话级 MCP ClientSession 复用 / singleflight（README §5.2 设计点 2 推迟项，按延迟实测再评估）；
- 供应商原生并行工具调用的并发执行（本轮串行 Invoke，≤10 个工具绑定下收益小、错误面大）。

## 11. 变更清单

| 层 | 文件 | 动作 |
|---|---|---|
| platform | `internal/platform/tool/{tool.go, registry.go}` + 测试 | 新增 |
| platform/llm | `types.go` / `convert.go` 新增；`client.go` / `options.go` 公开面改签名；测试适配 | 修改 |
| mcp | `api/{api.go, schema.go}`（ToolSourceFor + ToolSourceReq）；`service/toolsource.go` + 测试 | 修改 / 新增 |
| chat | `service/{service.go, turn.go, model.go 注释}`；`service/tools.go` 新增；`doubles_test.go` / `turn_test.go` 扩 | 修改 / 新增 |
| workflow | `service/executor.go` 机械替换 | 修改 |
| 组合根 | `internal/app/server.go` 注入 mcpClientSvc | 修改 |
| 文档 | CLAUDE.md《统一工具与消息模型》节 + platform 树 + README §5.3/§7 | 修改 |
