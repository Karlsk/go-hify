# MCP 模块 SDK 选型决策（client 只用官方 go-sdk）

> 状态：**选型决策定稿并已落地**（2026-09-29 管理批交付：go-sdk v1.3.1 已引入，`internal/mcp/service/mcpclient.go` 是全仓唯一 import go-sdk 处）：管理批（server 管理 + agent 绑定）实现记录见 [server_mgmt_impl.md](server_mgmt_impl.md)。本文是 mcp changelog 目录的首篇，兼作目录索引——数据模型见 [db_model.md](db_model.md)、管理批接口契约与 client 设计见 [server_mgmt_spec.md](server_mgmt_spec.md)。
> 本文记录 Hify MCP client 的 SDK 选型结论、决策理由、自研桥接范围与随决策定下的设计点；模块分层与依赖规则见 CLAUDE.md《代码组织规范》，agent 侧工具绑定设计见 [../agent/kb_binding_crud.md](../agent/kb_binding_crud.md)（绑 tool 不绑 server 的约定以 agent 模块文档为准）。

## 1. 决策

**MCP client 一律直接依赖官方 SDK `github.com/modelcontextprotocol/go-sdk`，不引入 eino-ext 适配器（`components/tool/mcp` 与 `components/tool/mcp/officialmcp` 均不用），不引入第二个 MCP 协议实现。** MCP 工具 ↔ eino 的桥接（schema 转换 + 调用包装）由 Hify 自研，归属与落地映射见 §5。

原则的精确表述：**「MCP 层单实现 + 不加不受控的桥接层」**——不禁工具库（go-sdk 本身就是三方；依赖树里已有的 `eino-contrib/jsonschema` / `invopop/jsonschema` 可复用作 schema 转换，不算违反本决策）。

## 2. 候选调研快照（2026-09-28 时点）

| 候选 | 定位 | 成熟度 | 结论 |
|---|---|---|---|
| `modelcontextprotocol/go-sdk` | 官方 SDK（Anthropic + Google 协作，四大 Tier 1 之一） | v1.0 → v1.3.1（2026-02）；v1.2+ 支持 spec 2025-11-25；专属文档站 go.sdk.modelcontextprotocol.io | ✅ **采用** |
| `mark3labs/mcp-go` | 社区最早最流行，官方 SDK 出现前的事实标准 | v1.0.0（2026-09-02）→ v1.1.1（2026-09-23）；支持 2025-11-25 + Streamable HTTP | 不采用（功能优势集中在 server 侧，Hify 只做 client；单人主导的社区项目，长期投入已被官方 SDK 分流） |
| eino-ext `components/tool/mcp` | eino 适配器，底层 **mark3labs v0.43.0**（v0 旧版，spec 停在 2025-06-18） | 适配层 tag v0.0.9 | 不采用（版本被钉旧 + §3 架构冲突） |
| eino-ext `components/tool/mcp/officialmcp` | 同一适配器的官方 SDK 变体 | 随 main 滚动 | 不采用（§3 架构冲突；但**留作参考实现**，见 §6） |

版本策略：钉 **v1.3.x 稳定线**，不追 2026-07-28 spec 的 beta（MRTR / 无状态核心对一期无意义）。

## 3. 决策理由

### 3.1 主因：适配器形态与 Hify 模块规则相抵触

适配器的两个核心设计点放进 Hify 的架构规则里检验，两条都撞墙：

| 适配器的假设 | Hify 的规则 | 冲突 |
|---|---|---|
| **调用方持有 `ClientSession`**（`Config.Cli` 由使用方创建并拥有） | session 是有状态资源，归属 `internal/mcp` 模块；跨模块只能经 `mcp/api` 契约调用 | chat 想用 `GetTools` 就必须绕过 mcp 模块自己管连接——mcp 模块失去存在意义 |
| **产出 `[]tool.BaseTool`**（eino 接口类型） | `api/` 是叶子包，只 import 标准库与纯标准库的 platform 小包；跨模块传参只用目标模块 api 里的 schema | `tool.BaseTool` 出不了 `mcp/api` 的边界；要么破坏 api 纯度规则，要么 mcp 模块内部消化后契约里只剩 JSON——适配器就白引了 |

适配器是为「eino 层直接拥有 MCP 连接」的应用形态设计的（其示例即 main 里建连接 → GetTools → 喂 Agent）。Hify 是模块化单体 + 严格 api 契约 + **DB 驱动的工具生命周期**（discover → upsert `mcp_tools` → agent 绑定 → 调用时按绑定子集构建），适配器的 `GetTools`（每次连上去实时拉）不覆盖这个生命周期——**生命周期管理的大头本来就要自己写，适配器只剩 schema 转换 + CallTool 包装可复用，却要为它扭曲模块边界，不划算**。

### 3.2 次级理由

- **版本控制权**：直连则 Hify 自己决定跟到哪个 spec 版本；用适配器则被 eino-ext 的发布节奏钉死。
- **单 MCP 实现**：依赖树里只有一处 MCP 协议实现，排障、升级、读源码都只有一处——「一个人开发，好维护优先」。

## 4. Spring 生态互操作结论

Go client 与 Spring server 之间不存在语言层集成，兼容性完全是**协议层互操作**：`initialize` 握手协商共同协议版本 + 共同传输。Spring AI 2.x 走 MCP Java SDK 1.0.x，广告协议版本 2024-11-05 → 2025-11-25，传输支持 stdio / Streamable HTTP（WebMvc / WebFlux）+ legacy SSE。

官方 go-sdk v1.2+ 支持到 2025-11-25 + Streamable HTTP 客户端传输 + OAuth（`auth` / `oauthex` 包）→ **与 Spring AI 2.x 服务端协商结果最优，即插即通**。唯一约束：`mcp_servers` 表里登记的 URL 指向 Streamable HTTP 端点，而非 spec 已废弃的 `/sse`。

## 5. 自研桥接：范围、成本与落地映射

### 5.1 成本清单（约 200–300 行 + 测试）

| 工作 | 量级 | 备注 |
|---|---|---|
| MCP `inputSchema` → eino `schema.ToolInfo`（`ParamsOneOf` 只吃 openapi3） | **最大头** | 复用依赖树已有的 `eino-contrib/jsonschema` / `invopop/jsonschema`（eino 自带，indirect → direct 零新增模块）；$ref / oneOf / 嵌套对象是坑 |
| `InvokeTool`：CallTool + 结果内容块归一 | 小 | MCP 返回 content blocks（text / image / …）+ `isError`；LLM 只吃文本——拼 text 块；`isError` 包装成工具错误喂回模型（工具错误也是模型可推理的上下文，不中断会话） |
| `tools/list` cursor 翻页 | 小 | discover 时翻完 |
| 会话生命周期 | 中 | 见 §5.3 |

### 5.2 随决策定下的三个设计点

1. **工具名跨 server 冲突**：agent 可绑定多个 server 的工具，两个 server 都叫 `search` 时发给 LLM 的函数名必须唯一。决策：**discover upsert 时生成调用名（`server_slug__tool_name` 形态）入库**，api 层暴露唯一调用名，invoke 时反查 server + 原始 tool name。DB 侧 `uq(mcp_server_id, name)` 保留（原始名每 server 内唯一）。此项与 agent 模块「绑 tool 不绑 server」设计联动，实现批次对齐。
   〔2026-09-28 修订〕server 管理批定稿为**运行期派生、不落库**：`callTool` 从绑定的 tool 行派生 `mcpServerId__toolName`（[db_model.md](db_model.md) §2.1），discover upsert 不生成 call_name 列；LLM 函数名前缀在 chat 阶段（下批）由消费方组装。
2. **会话生命周期**：长驻进程对 Streamable HTTP server 保持 `ClientSession` 复用（避免每次调用重付 initialize 握手）；调用出错标记失效、重建用 `golang.org/x/sync/singleflight` 防并发重建风暴（该依赖已在树里）；后台健康探测对齐 provider `StartProber` 模式（组合根 `go Start(appCtx)`，随优雅关停退出）。
   〔2026-09-28 修订〕server 管理批按用户 spec 采用 **per-call client**（每次调用 新建 → initialize → 操作 → Close，[server_mgmt_spec.md](server_mgmt_spec.md) §4）：本批调用方均为低频管理动作（test / run / 绑定校验），握手开销可忽略，无状态实现最简。会话复用 + singleflight 重建推迟到 chat 阶段（每轮对话多次工具调用、延迟敏感时）再评估升级。
3. **discover 时试跑 schema 转换**：转换失败的标记 `enabled=false` + 失败原因入库（可逆下架模式）——管理员加 server 时就暴露问题，而不是用户聊到一半才发现工具坏了。

### 5.3 落地映射（依赖方向无新增）

〔2026-09-29 chat 批修订〕chat 工具循环定稿为**全局统一工具模型**架构（[chat_tool_loop_spec.md](chat_tool_loop_spec.md)），§5.3 初版「chat 侧 toToolInfo」的归属随之修订：转换不再在 chat，而是拆成两段各归其位——MCP→`tool.ToolDef` 在 mcp/service（`ToolSourceFor`），`ToolDef`→eino ToolInfo 在 platform/llm（eino 收敛，chat/workflow 零 eino）。

〔2026-09-30 spec 016 交付〕workflow 工具节点落地：executor 经 `toolSourceFactory` 窄面（`ToolSourceFor` 按 ToolID 解析）消费 `tool.Source`，单次确定性调用 + fail-fast 错误分型，组合根注入 `mcpClientSvc`——实现记录见 [../workflow/impl_spec_16_tool_node_mcp.md](../workflow/impl_spec_16_tool_node_mcp.md)。

```
internal/platform/tool    ← 全局统一工具契约（纯标准库）：ToolDef/ToolCall/
                             ToolResult/Source/Registry——MCP 与未来内置工具同一模型
internal/mcp/service      ← 唯一 import go-sdk 的地方；tool.Source 实现
                            （ToolSourceFor：调用名派生 s{id}__{name} + 路由 + per-call 调用）
internal/mcp/api          ← 纯契约，零 eino / SDK 类型（可 import 纯标准库的 platform/tool）
internal/platform/llm     ← 唯一 import eino 的地方：Message/Final 公开面 +
                             ToolDef→ToolInfo 转换（保护链 bulkhead/熔断/超时不动）
internal/chat/service     ← ReAct 多轮循环：只见 tool.* 与 llm.*，零 eino / SDK
internal/workflow         ← LLM 节点经 llm.Message/Final；工具节点经 toolSourceFactory
                             窄面接 mcpapi.ToolSourceFor（spec 016 已交付）
```

契约草图（方向性，非终稿；终稿随 api 契约 spec 定）：

```go
// internal/mcp/api —— 纯契约，零 eino 类型
type ToolSchema struct {
    ID          uint64          `json:"id,string"`
    Name        string          `json:"name"`       // 原始 tool name
    CallName    string          `json:"call_name"`  // LLM 请求用唯一名（防跨 server 冲突）
    Description string          `json:"description"`
    InputSchema json.RawMessage `json:"input_schema"` // 原始 JSON Schema 原样透传
}

type MCPService interface {
    Discover(ctx context.Context, req DiscoverReq) (*DiscoverResult, error)
    ListToolsByAgent(ctx context.Context, agentID uint64) ([]ToolSchema, error)
    InvokeTool(ctx context.Context, req InvokeReq) (*InvokeResult, error)
}
```

chat 侧 import eino 的 `schema` 包属库依赖，不受业务域依赖清单约束（清单管业务域）；eino 类型止步于消费方，不进 mcp 契约。

## 6. 参考作业

不依赖 eino-ext `officialmcp`，但**把它的源码与 README 当参考实现读**：`DescriptionPolicy` / `ResultPolicy` / `MaxToolPages` / `ToolCallResultHandlerV2` / session 重连子包，每项都是真实踩坑的结晶。自研桥接对照这份清单核对功能面，可省一轮自己踩坑。

## 7. 待办（实现批次入口）

> 2026-09-28：第一批（MCP Server 管理 + Agent 工具绑定）spec 已定稿——[db_model.md](db_model.md)（迁移 00021）+ [server_mgmt_spec.md](server_mgmt_spec.md)（契约与 client 设计）；下列前 3 项由该批承接，后 2 项留待 chat / workflow 批次。
> 2026-09-29：前 3 项已交付（实现记录 [server_mgmt_impl.md](server_mgmt_impl.md)）。
> 2026-09-29：chat 批（统一工具/消息模型 + ReAct 工具循环）spec 定稿并实施——[chat_tool_loop_spec.md](chat_tool_loop_spec.md)。
> 2026-09-30：chat 批已交付（实现记录 [chat_tool_loop_impl.md](chat_tool_loop_impl.md)）：platform/tool 统一契约、llm 公开面 Hify 化、mcp `ToolSourceFor`、chat 多轮工具循环；workflow LLM 节点同步换型。零迁移、handler/SSE 零改动、未绑工具路径零变化。
> 2026-09-30：workflow 批（spec 016）已交付——tool 节点经 `ToolSourceFor` 真实执行（实现记录 [../workflow/impl_spec_16_tool_node_mcp.md](../workflow/impl_spec_16_tool_node_mcp.md)）；下项旧草图名 `mcpapi.InvokeTool` 实即 `ToolSourceFor` 路径（D1 拍板）。

- [x] go.mod 引入 `github.com/modelcontextprotocol/go-sdk` v1.3.x（v1.3.1）
- [x] `mcp/api` 契约定稿（§5.3 草图基础上补 Req/Schema 与哨兵）
- [x] `mcp/service` client + discover 翻页 upsert（§5.2 设计点 1/2 按修订执行）
- [x] chat 侧工具循环接线（chat 批：`platform/tool` 统一模型 + `ToolSourceFor` + ReAct 多轮循环 + llm 公开面 Hify 化；转换归属按 §5.3 修订执行）
- [x] workflow 工具节点接 `mcpapi`（spec 016：`ToolSourceFor` 按 ToolID 解析 + 统一 `tool.Source` 执行，fail-fast 错误分型）
