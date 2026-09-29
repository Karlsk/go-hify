# MCP Server 管理与 Agent 工具绑定（server_mgmt_spec）

> 状态：**已交付**（2026-09-29 实现落地，验收结果与偏差见 [server_mgmt_impl.md](server_mgmt_impl.md)；契约本文为定稿版）；数据模型与迁移 00021 见 [db_model.md](db_model.md)，SDK 选型（官方 go-sdk v1.3.x，不引适配器）见 [README.md](README.md)。**chat 引擎接入（工具循环 + LLM 函数名前缀）明确不在本批**，下阶段实现。
> 本批范围：mcp 模块四层从占位到落地（管理 CRUD + test / run + 跨模块 `MCPClientService`）、agent 模块新增绑定端点、迁移 00021。

## 1. 路由总表

| 方法 | 路径 | 语义 | 成功码 |
|---|---|---|---|
| POST | `/api/v1/mcp-servers` | 创建 | 201 |
| GET | `/api/v1/mcp-servers` | 偏移分页列表（B 模式，极小配置表；id 升序，摘要含 `tool_count` 批量计数） | 200 |
| GET | `/api/v1/mcp-servers/{id}` | 详情（含 `tools[]`，DB 读） | 200 |
| PUT | `/api/v1/mcp-servers/{id}` | 整体更新（两段绑定 + `json:"-"`） | 200 |
| DELETE | `/api/v1/mcp-servers/{id}` | 硬删；有绑定 409 `MCP_SERVER_IN_USE` | 204 |
| POST | `/api/v1/mcp-servers/{id}/discover` | 连通性测试 + 工具发现：连接 → `tools/list` → upsert `mcp_tools` | 200 |
| POST | `/api/v1/mcp-servers/{id}/run` | 实调一个工具：`{tool_name, arguments}` | 200 |
| PUT | `/api/v1/agents/{id}/mcp-tools` | Agent 工具绑定全量替换（**agent 模块**） | 200 |

> 路由名沿用 CLAUDE.md 资源清单原样（用户定稿 2026-09-28：`/discover`、`/mcp-tools` 更准确，不采用初稿的 `/test`、`/tools`）——CLAUDE.md **路由零同步**；`/discover` 一个动作完成连通测试 + 工具发现 + 入库。

## 2. Schema 要点（mcp/api）

**CreateReq / UpdateReq（同构，Update 的 ID `json:"-"`）：**

- `name`：required，max=128；撞 `uq_mcp_servers_name` → 409 `MCP_SERVER_NAME_CONFLICT`（23505 翻译）
- `transport`：required，oneof `stdio http sse`
- `url`：http/sse 必填、stdio 空串；`command`：stdio 必填、其他空串；`env`：map，仅 stdio 有意义——**跨字段规则走 `Validate()`**（binding tag 管不了按 transport 分支的必填）
- `enabled`：`*bool` 缺省 true（指针防零值吞语义，agent `resolveEnabled` 同款）

**响应 Schema：**

| Schema | 字段 | 说明 |
|---|---|---|
| `MCPServerSchema` | id string + name / transport / command / url / enabled + 双时间戳 | list 摘要 + `tool_count`；**env 不回** |
| `MCPServerDetailSchema` | server + `tools []ToolSchema` | env 回传（编辑动线；敏感性注记见 db_model §4） |
| `ToolSchema` | id string / server_id string / name / description / input_schema（`json.RawMessage` 原样） | 详情与跨模块共用 |
| `DiscoverResultSchema` | success / latency_ms / tool_count / error_message | **连通失败也是 200**（provider `ConnectionTestSchema` 同款：测试动作完成即成功响应，结果携带成败），tool_count = 本次 upsert 后该 server 工具总数 |
| `RunToolSchema` | content（string） | TextContent 多条换行拼接 |

**哨兵 +4**（`mcp/api/errors.go`，沿用已有 `ErrMCPServerNotFound`）：

| 码 | HTTP | 触发 |
|---|---|---|
| `MCP_SERVER_NAME_CONFLICT` | 409 | name 撞唯一约束（23505） |
| `MCP_SERVER_IN_USE` | 409 | Delete 撞 `fk_agent_tools_tool_id`（23503 按约束名分发） |
| `MCP_TOOL_NOT_FOUND` | 404 | run 的 tool_name 不在该 server 的 `mcp_tools` |
| `MCP_TOOL_CALL_FAILED` | 500 | CallTool 执行失败（message 带上游错误摘要；对齐 `WORKFLOW_EXECUTION_FAILED`） |

## 3. 跨模块契约（mcp/api/api.go）

一个 service struct 实现两个接口（组合根分别注入），按消费方拆分：

```go
// MCPServerService 管理面：mcp handler 消费（CRUD + Test + RunTool）。
type MCPServerService interface { ... } // 签名与路由表一一对应

// MCPClientService 调用面：agent 绑定校验（本批）+ chat 工具循环（下批）消费。
type MCPClientService interface {
    // CallTool 按调用创建 client（连接+initialize+call+close），结果取 TextContent
    // 多条换行拼接。错误：ErrMCPServerNotFound、ErrMCPToolCallFailed。
    // 不检查 enabled——调用方组装工具时自行排除停用 server（绑定校验已挡新增）。
    CallTool(ctx context.Context, req CallToolReq) (*CallToolResult, error)

    // ListTools 读 mcp_tools（DB，不连 server）。错误：ErrMCPServerNotFound。
    ListTools(ctx context.Context, req ListToolsReq) ([]ToolSchema, error)

    // GetToolsByIDs 批量取工具（含所属 server 的 enabled 标志）——agent 绑定校验用。
    GetToolsByIDs(ctx context.Context, req GetToolsByIDsReq) ([]ToolCheck, error)
}
```

- run 端点 = `MCPClientService.CallTool` 的 HTTP 包装（`MCPServerService.RunTool` 内部调同一实现），无第二份调用逻辑。
- `ToolCheck` = ToolSchema + `server_enabled bool`。

## 4. MCP client 设计（mcp/service/mcpclient.go）

- **底层**：官方 go-sdk；transport 三分支构造——`http` → StreamableHTTP、`sse` → SSEClientTransport、`stdio` → 子进程 transport（go-sdk 具体构造函数 Step 5 按实际 API 核对）。
- **per-call 生命周期**（用户 spec）：每次调用 新建 client → Connect + initialize → 操作 → Close。本批调用方均为低频管理动作（discover / run / 绑定校验），握手开销可忽略；无状态实现最简，无会话管理复杂度。
- **超时**（包内常量）：`connectTimeout = 10s`（连接 + initialize）、`callTimeout = 30s`（tools/call，含工具执行——工具慢是常态，见 CLAUDE.md SSE 心跳说明）。
- **discover 事务纪律**（踩坑清单 5）：事务外连接 + `tools/list`（外部调用禁入事务）→ 事务内批量 upsert（`uq(server_id, name)` ON CONFLICT DO UPDATE description + input_schema；只增改不删）。
- **结果归一**：遍历 content blocks 取 TextContent，多条 `"\n"` 拼接；`isError=true` → `ErrMCPToolCallFailed`（message 带工具侧错误文本）；无 TextContent → 空串。
- 禁止：go-sdk 类型不得泄出 api 层（`mcp/api` 保持纯契约）；client 逻辑只写在 mcp/service。

## 5. Agent 绑定端点（agent 模块改动）

- `PUT /api/v1/agents/{id}/mcp-tools`，body `{tool_ids: []}`（id 字符串化）；**空数组 = 全部解绑，合法**。
- `agentService.BindTools(ctx, req)` 实现三步：
  1. **校验**：数量 ≤ `MaxAgentTools`（包内常量 10，防 tools 参数过长影响 LLM 效果）；经 `mcpapi.GetToolsByIDs` 预检——缺失 → `ErrToolNotFound`（404，沿用既有哨兵）；`server_enabled=false` → 400 `VALIDATION_FAILED` + details 列出停用工具 id（不新增哨兵）
  2. **事务**：`WithTx { DeleteToolsByAgent + CreateTools }`（复用 agent Store 既有方法，先删后插全量替换）
  3. FK 23503 兜底并发删除（宽松匹配 → `ErrToolNotFound`，语义不变）
- **既有 Create / Update 的 tool_ids 同步套用同一校验函数**（≤10 + enabled）——行为增强，测试补齐；agent → mcp（api）在允许依赖清单内。

## 6. CLAUDE.md 同步项（实现批次收尾时改，错误码须先有哨兵）

- 错误码表 +4 行（§2 哨兵表）
- 索引地图 `mcp_tools` 行补 `uq(server_id, name)`；路由零同步（§1 注）

## 7. 实现批次顺序（对应 module-delivery Step 2-8）

1. **Step 2**：迁移 00021（先核对约束名）+ `mcp/service/model.go` 两实体 + agent model 注释同步
2. **Step 3**：mcp Store（GetByID / List / Create / Update / Delete / UpsertTools 批量 / ListToolsByServer / CountToolsByServerIDs / GetToolsByIDs，sqlmock）
3. **Step 4 ⏸**：mcp api schema + errors + 钉住测试
4. **Step 5**：mcp service（CRUD + 缓存 + FK 翻译）+ `mcpclient.go`（go-sdk per-call）+ agent `BindTools` 与校验增强
5. **Step 6/7**：两模块 handler + 组合根（agent service 构造注入 mcpapi）
6. **Step 8 ⏸**：手测文档——含真实 MCP server 冒烟（go-sdk 自建 demo server 或现成开源 server，三 transport 各验一条）
