# MCP Server 管理批实现记录（server_mgmt_impl）

> 状态：**已交付**（2026-09-29）：后端 7 端点 + agent 绑定端点 + 前端两页全部落地，手测 26 项全过、浏览器走查通过。
> 契约与设计定稿见 [server_mgmt_spec.md](server_mgmt_spec.md)，数据模型与迁移 00021 见 [db_model.md](db_model.md)，SDK 选型见 [README.md](README.md)。本文记录实现落地结果、与 spec 的偏差及验收证据；手测步骤见 [../../testing/mcp-manual-test.md](../../testing/mcp-manual-test.md)。

## 1. 交付清单

| 层 | 内容 |
|---|---|
| 迁移 | `migrations/00021_mcp_server_mgmt.sql`：`mcp_servers.enabled`（可逆下架）+ `agent_tools.tool_id` FK 改 RESTRICT（约束名 `fk_agent_tools_tool_id`，23503 按名分发翻译） |
| mcp/api | 8 个 Req + 6 个 Schema + 哨兵 ×4（`MCP_SERVER_NAME_CONFLICT` / `MCP_SERVER_IN_USE` / `MCP_TOOL_NOT_FOUND` / `MCP_TOOL_CALL_FAILED`）；`MCPServerService`（管理面）+ `MCPClientService`（调用面）双接口同 struct 实现 |
| mcp/service | CRUD（Cache-Aside：`mcp-server-cache` / `mcp-tool-cache` 双命名空间，写时删 key）+ discover（事务外连接 → 事务内批量 upsert）+ `mcpclient.go`（go-sdk v1.3.1 per-call client，**全仓唯一 import go-sdk 处**） |
| mcp/store | GORM CRUD + `db.Raw` 批量 upsert + `GetToolsByIDs`（含 server enabled 联查）+ `CountToolsByServerIDs`（列表 tool_count 批量计数，防 N+1） |
| mcp/handler | 7 端点薄绑定；Update 两段绑定 + `json:"-"`（provider 踩坑 #7 回归钉） |
| agent | `PUT /agents/{id}/mcp-tools` 全量替换绑定（`checkTools`：≤10 + 存在 + server 启用）；Create / Update 的 tool_ids 套用同一校验（行为增强） |
| 组合根 | `mcpsvc.New` 双返回注入 handler 与 agent；`mcphandler.RegisterRoutes` 挂 v1 |
| 前端 | `web/src/api/mcp.ts`（类型 + 7 方法）；`views/mcp/McpServerList.vue`（列表 + 表单 + 发现）；`components/McpToolsDrawer.vue`（工具抽屉 + 运行弹窗）；AgentList 工具绑定 tab 真实数据（按 server 分组多选，limit 10）；路由 `/mcp-servers` + 侧边栏菜单 |

## 2. 与 spec 的偏差

| spec 原文 | 实际落地 | 说明 |
|---|---|---|
| §2 响应表 `RunToolSchema` | `CallToolResult` | run 端点与跨模块 `CallTool` 共用同一结果类型，命名对齐复用语义（spec §3 本就要求"无第二份调用逻辑"） |
| §4 超时"包内常量" | 同 | connectTimeout 10s / callTimeout 30s，mcpclient.go 内常量，未上 config——管理面低频动作，暂无调参需求 |

其余全部按 spec 落地；路由名 `/discover`、`/mcp-tools` 维持用户定稿（spec §1 注）。

## 3. 测试与验收

**自动化**（`go test ./internal/mcp/... ./internal/agent/... -count=1 -race` 全绿）：

| 包 | 覆盖率 |
|---|---|
| mcp/api（钉住 + 序列化安全） | 79.2% |
| mcp/handler（httptest ×7 端点 + agent bindTools） | 89.9% |
| mcp/service（stub Store + mcpclient 真子进程） | 77.8% |
| mcp/store（sqlmock） | 87.0% |
| agent/api / handler / service / store | 89.3% / 91.5% / 86.1% / 77.9% |

mcpclient 的真实网络路径（连接失败 / 握手 / NDJSON 解析）由手测覆盖（`mcpclient_test.go` 用真 stdio 子进程覆盖 happy path 与超时），mock 不进协议层。

**手测**（[mcp-manual-test.md](../../testing/mcp-manual-test.md)，26 项全过）：真实 filesystem MCP server（npx stdio）端到端——discover 14 工具、失败路径 200 语义、幂等 id 稳定；run read_file / write_file 多条拼接；agent 绑定闭环（绑定 / 替换 / 解绑 / 超限 / 缺失 / 停用 400 / in-use 409 → 解绑后删）；更新两段绑定；边界（坏 id / page_size 越界 / Redis 降级）。

**前端**（Step 10/11）：vue-tsc + vite build 通过；dev 栈经 Vite 代理实测全部读写路径（含表单真实 payload 形态——http 创建带空 command、stdio 带 env、`enabled:false` 指针语义）；浏览器走查通过（管理页 CRUD / 抽屉 / 运行、Agent 工具绑定分组多选）。

## 4. 下阶段（chat 接入，明确不在本批）

- 工具循环：`toToolInfo(mcpapi.ToolSchema)` 归属 chat/service（README §5.3 落地映射）
- LLM 函数名前缀 `mcpServerId__toolName`（运行期派生、不落库——README §5.2 修订）
- 会话复用 + singleflight 重建（per-call → 长驻 `ClientSession`，延迟敏感时升级——README §5.2 修订）
- stale tool 处理（绑定后 server 停用 / 工具消失，chat 侧组装时静默剔除）
