# MCP 模块数据模型（db_model）

> 状态：**决策定稿，未实现**（2026-09-28）：00003（`mcp_servers` / `mcp_tools`）与 00004（`agent_tools`）沿用零重写；迁移 **00021** 两项修订（`mcp_servers.enabled` 新增 + `agent_tools.tool_id` FK CASCADE→RESTRICT）已定稿待落。接口与 client 设计见 [server_mgmt_spec.md](server_mgmt_spec.md)，SDK 选型（官方 go-sdk）见 [README.md](README.md)。
> 本文记录 mcp 模块数据模型的最终结论与决策理由；表归属总览见 [docs/design/data-model.md](../../design/data-model.md)，建表通用规范见 CLAUDE.md《数据库规范》。

## 1. 现有表沿用（00003 / 00004，零结构重写）

本批需求涉及的三张表均已存在，结构满足，唯一缺口是 `enabled` 列与 FK 语义：

```
mcp_servers (00003)                        mcp_tools (00003)
  name        text  uq                        mcp_server_id  FK CASCADE
  transport   text  CHECK(stdio/http/sse)     name           text
  command     text  (stdio 启动命令)           description    text
  url         text  (http/sse 服务地址)        input_schema   jsonb
  env         jsonb (stdio 子进程环境变量)      uq(mcp_server_id, name) + idx(mcp_server_id)
  + enabled   (00021 新增)

agent_tools (00004)
  id PK / agent_id FK CASCADE / tool_id FK(00021: CASCADE→RESTRICT) / created_at
  uq(agent_id, tool_id) + idx(tool_id)
```

对照说明：

- 需求中的「mcp_tool 表」即 `mcp_tools`（name / description / inputSchema 已在；表名按全仓复数规范）。
- 需求中的「新建 agent_tool 关联表」**无需新建**——提供的 MySQL DDL（`AUTO_INCREMENT` / `DATETIME` / `uk_agent_tool`）逐项翻译为 PG 规范后与 00004 现有定义一致（IDENTITY 主键 / timestamptz / uq(agent_id, tool_id) + 反查索引），且 00004 已随 memory「agent_tools 表定稿」冻结。

## 2. 决策记录

### 2.1 工具引用形式：tool_id FK 存储，复合格式只是运行期寻址（用户思考题，2026-09-28 定）

「toolId 走单独的 tool 表，还是 mcpServerId__toolName 形式」——两者不是二选一，是两个层面各用各的：

| 层面 | 形式 | 理由 |
|---|---|---|
| 存储（`agent_tools`） | `tool_id bigint` FK → `mcp_tools.id` | 00004 已定稿；FK 完整性兜底（23503 → `ErrToolNotFound`）、`uq(agent_id, tool_id)` 防重、计数与 ≤10 上限校验自然；discover 按 `uq(server_id, name)` upsert 保证 tool id 稳定、绑定不漂移 |
| 运行期寻址 | `mcpServerId__toolName` 复合格式，**不落库** | `callTool(serverID, toolName)` 从绑定的 tool 行派生；chat 阶段（下批）同款格式做 LLM 函数名前缀，防跨 server 工具撞名（[README.md](README.md) §5.2-1 call_name 语义） |

否决字符串存储：放弃 DB 完整性、与已定稿表结构冲突、server 重新 discover 后漂移无法拦截。

### 2.2 删除语义：硬删 + FK RESTRICT 挡（用户确认，2026-09-28）

| 方案 | 结论 | 理由 |
|---|---|---|
| **硬删 + tool_id FK RESTRICT**（采纳） | DELETE 真删；有绑定时 server → mcp_tools 级联删被 RESTRICT 拦下 → 23503 → 409 `MCP_SERVER_IN_USE` | 对齐 00013（软删全面退役）与 `WORKFLOW_IN_USE` / `AGENT_IN_USE` / `MODEL_IN_USE` 先例；删除检查由 DB 层完成，mcp 模块零耦合（依赖图禁止 mcp → agent，store 禁查他模块表）；可逆下架由 `enabled` 承担（本批新增） |
| 逻辑删除（deleted_at） | 否决 | 违背 00013 全仓决策；「有绑定拒绝删除」的检查在 mcp 模块架构上不可实现（不能查 agent_tools、不能反向依赖 agent）；所有列表查询需加 deleted_at 过滤 |
| 保持 00004 现状 CASCADE | 否决 | server 删除时 Agent 绑定**静默消失**，Agent 无感知丢失工具能力，与需求「有绑定拒绝删除」直接相悖 |

副作用：00004 的 agent_tools 表注释「任一侧删除级联清绑定」随 00021 修订——agent 侧删除仍级联清绑定，tool 侧改为 RESTRICT 挡删。

### 2.3 `enabled` 列（00021 新增）

- 语义：可逆下架开关（全仓统一模式，对齐 `providers.enabled` / `agents.enabled`）。停用 → 拒绝新绑定（400 + details 停用工具清单）；chat 阶段调用时同样受拒（下批实现）；**discover / run 诊断端点不受限**（先验证再启用的管理动线）。
- 布尔列不带 GORM `default` tag（踩坑清单 1：`default:true` 零值陷阱），创建路径显式设值，DB `DEFAULT true` 只作直插 SQL 兜底。

### 2.4 discover upsert：只增改不删（provider sync 先例）

discover 连通成功 → `tools/list` 结果按 `uq(mcp_server_id, name)` upsert（新增行 / 刷新 description + input_schema）；**消失的工具不删不标记**——FK RESTRICT 挡删 + 绑定稳定性优先。stale 工具风险记 deferred（chat 阶段可加 `last_seen_at` 或调用期校验，本批不做）。

### 2.5 缓存：Cache-Aside（用户确认）

`mcp_servers` / `mcp_tools` 走 `platform/cache`（TTL + 写时删 key，对齐 provider / agent 先例），命名空间 `cache.NameMCPServer` / `cache.NameMCPTool`。失效矩阵：

| 写操作 | 删 key |
|---|---|
| mcp_servers Create / Update / Delete | 该 server 实体 key |
| discover 成功 upsert tools | 该 server 的 tools key（server 实体不含 tool 数据，不失效） |

## 3. 迁移 00021 DDL（草稿，Step 2 落地）

```sql
-- +goose Up
-- ① mcp_servers 补 enabled：可逆下架开关（00013 软删退役后的统一模式）
ALTER TABLE mcp_servers
    ADD COLUMN enabled boolean NOT NULL DEFAULT true;
COMMENT ON COLUMN mcp_servers.enabled IS '可逆下架开关：停用拒绝新绑定与调用；DELETE 才是真删（有绑定时被 fk_agent_tools_tool_id RESTRICT 挡）';

-- ② agent_tools.tool_id FK 语义修订：CASCADE → RESTRICT
-- 有 Agent 绑定的工具不允许被（级联）删除——server 删除被挡 → MCP_SERVER_IN_USE
ALTER TABLE agent_tools DROP CONSTRAINT agent_tools_tool_id_fkey;
ALTER TABLE agent_tools
    ADD CONSTRAINT fk_agent_tools_tool_id
    FOREIGN KEY (tool_id) REFERENCES mcp_tools (id) ON DELETE RESTRICT;
COMMENT ON CONSTRAINT fk_agent_tools_tool_id ON agent_tools IS '工具被 Agent 绑定时挡删（RESTRICT）：server 删除 → mcp_tools 级联删被此约束拦下 → 23503 → MCP_SERVER_IN_USE；agent 侧删除仍走 agent_id CASCADE 清绑定';
COMMENT ON TABLE agent_tools IS '关联表：Agent 绑定的 MCP 工具（多对多，绑定=授权）；agent 删除级联清绑定、tool 侧 RESTRICT 挡删；依赖 mcp discover 走 upsert（uq_mcp_tools_server_name）保证 tool id 稳定、绑定不漂移';

-- +goose Down
ALTER TABLE agent_tools DROP CONSTRAINT fk_agent_tools_tool_id;
ALTER TABLE agent_tools
    ADD CONSTRAINT agent_tools_tool_id_fkey
    FOREIGN KEY (tool_id) REFERENCES mcp_tools (id) ON DELETE CASCADE;
ALTER TABLE mcp_servers DROP COLUMN IF EXISTS enabled;
```

注意事项：

- 00004 内联 `REFERENCES` 生成的 PG 默认约束名为 `agent_tools_tool_id_fkey`；Step 2 落地时先 `\d agent_tools` 核对实际名再写死（若不同仅改 DROP 一行）。
- agent service 的 23503 分发不受改名影响：`translateAgentFK` 只管 agents 行级写语句（workflow / model 侧），`CreateTools` 路径为宽松匹配（agent_tools 插入撞 23503 → `ErrToolNotFound`，语义不变）；mcp 模块 Delete 路径按新约束名 `fk_agent_tools_tool_id` 显式匹配 → `ErrMCPServerInUse`。

## 4. Go model（internal/mcp/service/model.go，草稿）

```go
type MCPServer struct {
    db.BaseMutable // id + created_at + updated_at
    Name      string            `gorm:"not null"` // 展示名，唯一
    Transport string            `gorm:"not null"` // stdio / http / sse
    Command   string            `gorm:"not null"` // stdio 启动命令；其他空串
    URL       string            `gorm:"not null"` // http/sse 地址；stdio 空串
    Env       map[string]string `gorm:"type:jsonb;serializer:json"` // stdio 子进程环境变量
    Enabled   bool              `gorm:"not null"` // 无 default tag（踩坑清单 1）
}
func (MCPServer) TableName() string { return "mcp_servers" }

type MCPTool struct {
    db.BaseMutable
    MCPServerID uint64         `gorm:"not null"`
    Name        string         `gorm:"not null"`
    Description string         `gorm:"not null"`
    InputSchema map[string]any `gorm:"type:jsonb;serializer:json"` // JSON Schema 顶层恒为对象
}
func (MCPTool) TableName() string { return "mcp_tools" }
```

- `Env` / `InputSchema` 写库前 nonNilAny 兜底 nil → 空集合（防 jsonb 写成 `null`）；api 层 `ToolSchema.InputSchema` 用 `json.RawMessage` 原样透传（model 的 map 序列化即原始 schema，不二次加工）。
- 敏感性注记：`env` 是管理员自填的子进程环境变量（00003 即明文 jsonb 设计，非平台托管密钥），**detail 回传**（编辑动线需要）、**list 摘要不回**；与 provider `auth_config`（平台加密托管、永不外泄）的敏感性级别不同，不套用打码方案。
