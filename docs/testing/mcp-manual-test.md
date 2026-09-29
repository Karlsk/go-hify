# MCP 模块人工冒烟测试

验证对象：`internal/mcp` 全链路（api → service → store → handler + 组合根接线）+
agent 模块的 `PUT /agents/{id}/mcp-tools` 绑定端点。
全部为手工步骤，自动化覆盖见各包 `*_test.go`；实现决策见 `docs/changelog/mcp/`。

- 接口前缀：`/api/v1/mcp-servers`（7 端点，受登录中间件保护）+ `/api/v1/agents/{id}/mcp-tools`
- 响应信封：`{success, data, error, meta}`（`platform/respond`）；ID 一律字符串
- 列表分页：偏移分页 `meta = {page, page_size, total}`（mcp_servers 是极小配置表，规范允许）
- 本文档命令在**仓库根目录**执行；端口按本机 dev 实际配置（`.env` `SERVER_PORT`，当前 8081）

> 端口说明同 [agent-manual-test.md](agent-manual-test.md)：本机 8080 被占用期间 dev 用
> `SERVER_PORT=8081` + PG 5433；标准端口跑则把 `8081` 换 `8080`、容器映射换回 `-p 5432:5432`。

## 0. 前置条件

| 依赖 | 用途 | 检查命令 |
|---|---|---|
| Docker | 临时 PG / Redis 容器 | `docker version` |
| curl / jq | 接口调用与字段提取 | `curl --version && jq --version` |
| Node.js ≥18 / npx | stdio 传输冒烟（filesystem MCP server） | `npx --version` |

## 1. 启动依赖容器 + 迁移 + 启动服务

```bash
docker run -d --name hify-pg-test \
  -e POSTGRES_USER=hify -e POSTGRES_PASSWORD=hify -e POSTGRES_DB=hify \
  -p 5433:5432 pgvector/pgvector:pg17          # 宿主 5433 → 容器 5432
docker run -d --name hify-redis-test -p 6379:6379 redis:7-alpine
until docker exec hify-pg-test pg_isready -U hify >/dev/null 2>&1; do sleep 1; done && echo "pg ready"
```

```bash
make migrate-up && make migrate-status   # 预期含 00021_mcp_server_mgmt 全部 applied
make start
curl -s localhost:8081/health | jq .     # → {"success":true,"data":"Hify is running",...}
```

## 2. 登录链（后续所有请求都要带 cookie）

```bash
curl -s -X POST localhost:8081/api/v1/auth/register -H 'Content-Type: application/json' \
  -d '{"username":"tester","password":"smoke-test-123"}' | jq .success    # → true（已注册则忽略报错）
curl -s -c /tmp/hify-jar -X POST localhost:8081/api/v1/auth/login -H 'Content-Type: application/json' \
  -d '{"username":"tester","password":"smoke-test-123"}' | jq .success    # → true
curl -s localhost:8081/api/v1/mcp-servers | jq .error.code               # → "UNAUTHORIZED"（未带 cookie）
```

## 3. 创建（POST /mcp-servers）

```bash
# stdio 传输：command + env（官方 filesystem server，本机 npx 拉起子进程）
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/mcp-servers -H 'Content-Type: application/json' \
  -d '{"name":"filesystem","transport":"stdio","command":"npx -y @modelcontextprotocol/server-filesystem /tmp/mcp-test",
       "env":{"NODE_ENV":"production"}}' | jq .
```

**预期** 201 信封：`data.id="1"`（字符串）、`transport="stdio"`、`command` 原样回显、
`enabled=true`（缺省即启用）、双时间戳 RFC 3339；**env 不回**（list/detail 摘要面不带，
详情才带）。

```bash
# http 传输：url 必填、command 必须缺省
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/mcp-servers -H 'Content-Type: application/json' \
  -d '{"name":"remote-http","transport":"http","url":"http://172.17.1.227:8001/mcp"}' | jq .data.id   # → "2"

# 创建即停用（enabled=false 显式合法，指针语义不被缺省 true 吞掉）
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/mcp-servers -H 'Content-Type: application/json' \
  -d '{"name":"disabled-one","transport":"http","url":"http://localhost:9701/mcp","enabled":false}' \
  | jq -c '.data.enabled'   # → false

# 跨字段校验失败（binding tag 管不了按 transport 分支的必填，Validate() 拦）
curl -s -o /dev/null -w '%{http_code}\n' -b /tmp/hify-jar -X POST localhost:8081/api/v1/mcp-servers \
  -H 'Content-Type: application/json' -d '{"name":"bad","transport":"stdio"}'          # → 400（stdio 缺 command）
curl -s -o /dev/null -w '%{http_code}\n' -b /tmp/hify-jar -X POST localhost:8081/api/v1/mcp-servers \
  -H 'Content-Type: application/json' -d '{"name":"bad","transport":"http"}'           # → 400（http 缺 url）
curl -s -o /dev/null -w '%{http_code}\n' -b /tmp/hify-jar -X POST localhost:8081/api/v1/mcp-servers \
  -H 'Content-Type: application/json' -d '{"name":"bad","transport":"grpc"}'           # → 400（transport oneof）

# 重名冲突（uq_mcp_servers_name 23505 → 哨兵翻译）
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/mcp-servers -H 'Content-Type: application/json' \
  -d '{"name":"filesystem","transport":"stdio","command":"npx -y whatever"}' \
  | jq -c '{http: "409?", code: .error.code}'    # → HTTP 409 + MCP_SERVER_NAME_CONFLICT
```

## 4. 列表 + 详情（GET /mcp-servers）

```bash
curl -s -b /tmp/hify-jar 'localhost:8081/api/v1/mcp-servers?page=1&page_size=10' | jq .
```

**预期** 200：`data[]` 三行、id 升序、每行含 `tool_count`（discover 前为 0，批量 IN 计数不逐行查）；
`meta = {page:1, page_size:10, total:3}`。

```bash
curl -s -b /tmp/hify-jar localhost:8081/api/v1/mcp-servers/1 | jq .
```

**预期** 200：`data.env` 回传（编辑动线要）、`data.tools` 空数组 `[]`（未 discover，不是 null）。

```bash
curl -s -o /dev/null -w '%{http_code}\n' -b /tmp/hify-jar localhost:8081/api/v1/mcp-servers/999   # → 404
```

## 5. discover：连通 + 工具发现 + upsert（POST /{id}/discover）

```bash
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/mcp-servers/1/discover | jq .
```

**预期** 200（动作完成即成功响应，成败在字段里）：

```jsonc
{ "success": true, "data": { "success": true, "latency_ms": 1200, "tool_count": 11, "error_message": null } }
```

（filesystem server 首次 npx 拉包较慢，连接超时 10s 内通常够；`tool_count` 以实际版本为准。）

```bash
# 再看详情：tools[] 已入库（name / description / input_schema 原样 JSON）
curl -s -b /tmp/hify-jar localhost:8081/api/v1/mcp-servers/1 | jq '.data.tools[0]'

# 列表 tool_count 变为实际数量
curl -s -b /tmp/hify-jar 'localhost:8081/api/v1/mcp-servers?page=1&page_size=10' \
  | jq '.data[] | select(.id=="1") | .tool_count'

# 重复 discover：upsert 幂等（uq_mcp_tools_server_name 锚点，id 不漂移）
FIRST=$(curl -s -b /tmp/hify-jar localhost:8081/api/v1/mcp-servers/1 | jq -r '.data.tools[0].id')
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/mcp-servers/1/discover >/dev/null
SECOND=$(curl -s -b /tmp/hify-jar localhost:8081/api/v1/mcp-servers/1 | jq -r '.data.tools[0].id')
[ "$FIRST" = "$SECOND" ] && echo "id stable ✓" || echo "ID DRIFTED ✗"

# 连不通的 server：仍是 200，success=false + error_message 摘要（truncate 512 rune）
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/mcp-servers/2/discover | jq '.data'
# → {"success":false,"latency_ms":...,"tool_count":0,"error_message":"…connect…"}
# 且 mcp_tools 不写库、tools 缓存 key 不失效（服务日志无 evict）

curl -s -o /dev/null -w '%{http_code}\n' -b /tmp/hify-jar -X POST localhost:8081/api/v1/mcp-servers/999/discover   # → 404
```

## 6. run：实调一个工具（POST /{id}/run）

```bash
mkdir -p /tmp/mcp-test && echo "hello mcp" > /tmp/mcp-test/hello.txt

# filesystem server 的 read_file：TextContent 取文本
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/mcp-servers/1/run -H 'Content-Type: application/json' \
  -d '{"tool_name":"read_file","arguments":{"path":"/tmp/mcp-test/hello.txt"}}' | jq .
# → data.content = "hello mcp\n"

# write_file 返回多条 TextContent → 换行拼接（CallToolResult.content 是纯 string）
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/mcp-servers/1/run -H 'Content-Type: application/json' \
  -d '{"tool_name":"write_file","arguments":{"path":"/tmp/mcp-test/new.txt","content":"written by hify"}}' | jq -r '.data.content'

# 工具不在 discover 清单
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/mcp-servers/1/run -H 'Content-Type: application/json' \
  -d '{"tool_name":"not_a_tool"}' | jq -c '{code: .error.code}'          # → 404 MCP_TOOL_NOT_FOUND
# 参数校验
curl -s -o /dev/null -w '%{http_code}\n' -b /tmp/hify-jar -X POST localhost:8081/api/v1/mcp-servers/1/run \
  -H 'Content-Type: application/json' -d '{"tool_name":"read_file","arguments":[1,2]}'   # → 400（arguments 须对象）
```

> run 不检查 `enabled`（管理面诊断语义：停用的 server 也能手动验证工具）；线上调用面
> （`MCPClientService.CallTool`，chat 阶段接入）同样按 id 直调。

## 7. Agent 绑定工具（PUT /agents/{id}/mcp-tools，agent 模块）

前置：一个 Agent（造数见 [agent-manual-test.md](agent-manual-test.md) §3-4，provider + model + agent）。

```bash
# 取 filesystem server 的前两个工具 id
TOOL1=$(curl -s -b /tmp/hify-jar localhost:8081/api/v1/mcp-servers/1 | jq -r '.data.tools[0].id')
TOOL2=$(curl -s -b /tmp/hify-jar localhost:8081/api/v1/mcp-servers/1 | jq -r '.data.tools[1].id')

# 绑定（请求侧 tool_ids 是数字数组，响应侧字符串）
curl -s -b /tmp/hify-jar -X PUT localhost:8081/api/v1/agents/1/mcp-tools -H 'Content-Type: application/json' \
  -d "{\"tool_ids\":[$TOOL1,$TOOL2]}" | jq .    # → 200 data.tool_ids = ["...","..."]

# agent 详情可见绑定
curl -s -b /tmp/hify-jar localhost:8081/api/v1/agents/1 | jq '.data.tool_ids'

# 全量替换语义：再 PUT 只留一个 → 前一个数组整体替换
curl -s -b /tmp/hify-jar -X PUT localhost:8081/api/v1/agents/1/mcp-tools -H 'Content-Type: application/json' \
  -d "{\"tool_ids\":[$TOOL1]}" | jq -c '.data.tool_ids | length'   # → 1

# 空数组 = 解绑全部
curl -s -b /tmp/hify-jar -X PUT localhost:8081/api/v1/agents/1/mcp-tools -H 'Content-Type: application/json' \
  -d '{"tool_ids":[]}' | jq -c '.data.tool_ids'                    # → []
```

失败路径：

```bash
# 工具不存在（mcp 预检）
curl -s -b /tmp/hify-jar -X PUT localhost:8081/api/v1/agents/1/mcp-tools -H 'Content-Type: application/json' \
  -d '{"tool_ids":[99999]}' | jq -c '{code: .error.code}'     # → 404 MCP_TOOL_NOT_FOUND（agentapi.ErrToolNotFound 同码）

# 绑定后删除被绑 server → 409 挡删（RESTRICT）
curl -s -b /tmp/hify-jar -X PUT localhost:8081/api/v1/agents/1/mcp-tools -H 'Content-Type: application/json' \
  -d "{\"tool_ids\":[$TOOL1]}" >/dev/null
curl -s -b /tmp/hify-jar -X DELETE localhost:8081/api/v1/mcp-servers/1 | jq -c '{code: .error.code}'
# → 409 MCP_SERVER_IN_USE（fk_agent_tools_tool_id 23503 按约束名分发）

# 解绑后可删
curl -s -b /tmp/hify-jar -X PUT localhost:8081/api/v1/agents/1/mcp-tools -H 'Content-Type: application/json' \
  -d '{"tool_ids":[]}' >/dev/null
curl -s -o /dev/null -w '%{http_code}\n' -b /tmp/hify-jar -X DELETE localhost:8081/api/v1/mcp-servers/1   # → 204
curl -s -o /dev/null -w '%{http_code}\n' -b /tmp/hify-jar localhost:8081/api/v1/mcp-servers/1              # → 404
```

## 8. 更新 + 删除收尾（PUT / DELETE /mcp-servers）

```bash
# 更新：body 恶意带 id 不生效（json:"-" 两段绑定，provider 踩坑 #7 回归）
curl -s -b /tmp/hify-jar -X PUT localhost:8081/api/v1/mcp-servers/2 -H 'Content-Type: application/json' \
  -d '{"id":999,"name":"remote-http-v2","transport":"http","url":"http://localhost:9702/mcp"}' \
  | jq -c '{id: .data.id, name: .data.name}'    # → {"id":"2","name":"remote-http-v2"}
curl -s -b /tmp/hify-jar localhost:8081/api/v1/mcp-servers/2 | jq -c '.data.url'   # → "http://localhost:9702/mcp"

# 更新不存在的 → 404
curl -s -o /dev/null -w '%{http_code}\n' -b /tmp/hify-jar -X PUT localhost:8081/api/v1/mcp-servers/999 \
  -H 'Content-Type: application/json' -d '{"name":"x","transport":"http","url":"http://x/mcp"}'   # → 404

# 无绑定直接删 → 204 无返回体
curl -s -o /dev/null -w '%{http_code}\n' -b /tmp/hify-jar -X DELETE localhost:8081/api/v1/mcp-servers/2   # → 204
```

## 9. 边界场景

| 场景 | 命令要点 | 预期 |
|---|---|---|
| 未登录访问 | `curl -s localhost:8081/api/v1/mcp-servers` | 401 `UNAUTHORIZED` |
| id 非数字 | `GET /mcp-servers/abc` | 400 `VALIDATION_FAILED` |
| 列表 page_size 越界 | `?page_size=999` | 400（binding max=100） |
| stdio command 带参数 | `"command":"npx -y @modelcontextprotocol/server-filesystem /tmp/mcp-test"` | 按空白切分 argv，整体一条 command 字段 |
| discover 时 server 停用 | 对 `enabled=false` 的 server POST discover | 正常执行（管理面诊断不检查 enabled） |
| 绑定停用 server 的工具 | 对 disabled-one discover 后绑定 | 400 `VALIDATION_FAILED`（预检：所属 server 已停用） |
| tool_ids 超 10 | 数组塞 11 个 id | 400（binding max=10） |
| tool_ids 重复 | `[101,101]` | 400（Validate 去重，撞 uq 前友好提示） |
| Redis 停了再查详情 | `docker stop hify-redis-test` 后 GET /1 | 仍 200（缓存失败降级 DB，日志 WARN），测完 `docker start` |

## 10. 日志观测点

```bash
tail -f logs/hify.log | grep -E 'mcp|discover|run'
```

- discover 失败：`WARN` 一条（含 server id + 错误摘要，不刷屏）；成功无 INFO（结果在响应里）
- run 失败：`ERROR` 一条（server id + tool_name + 错误）
- 409/404 哨兵路径：handler 层无额外日志（预期错误不上 ERROR）
- 缓存降级：Redis 不可用时 `WARN cache ... degraded`，接口不 500
