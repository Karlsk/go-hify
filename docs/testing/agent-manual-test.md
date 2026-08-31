# Agent 模块人工冒烟测试

验证对象：`internal/agent` 全链路（api → service → store → handler + 组合根接线）。
全部为手工步骤，自动化覆盖见各包 `*_test.go`；实现决策见 `docs/changelog/agent/`。

- 接口前缀：`/api/v1/agents`（5 端点：POST / GET 列表 / GET 详情 / PUT / DELETE，受登录中间件保护）
- 响应信封：`{success, data, error, meta}`（`platform/respond`）；ID 一律字符串
- 列表分页：偏移分页 `meta = {page, page_size, total}`（agents 是极小配置表，规范允许）
- 本文档命令在**仓库根目录**执行

> **端口说明（2026-08-31 环境变更）**：本机 8080 / 5432 被另一项目（agentscope）占用，
> hify 本地 dev 临时迁移到 **SERVER_PORT=8081 + PG 5433**（`.env` 临时改动，不入 Git；
> deploy compose 容器内网 8080/5432 不受影响）。端口空出来后改回 `.env` 即可。
> 本文档所有命令按 8081 写；若在标准端口跑，把 `8081` 换 `8080`、容器映射换回 `-p 5432:5432`。

## 0. 前置条件

| 依赖 | 用途 | 检查命令 |
|---|---|---|
| Docker | 临时 PG / Redis 容器 | `docker version` |
| curl / jq | 接口调用与字段提取 | `curl --version && jq --version` |

## 1. 启动依赖容器 + 迁移 + 启动服务

```bash
docker run -d --name hify-pg-test \
  -e POSTGRES_USER=hify -e POSTGRES_PASSWORD=hify -e POSTGRES_DB=hify \
  -p 5433:5432 pgvector/pgvector:pg17          # 宿主 5433 → 容器 5432
docker run -d --name hify-redis-test -p 6379:6379 redis:7-alpine
until docker exec hify-pg-test pg_isready -U hify >/dev/null 2>&1; do sleep 1; done && echo "pg ready"
```

`.env` 核对（本地 dev 临时端口；`PROVIDER_MASTER_KEY` 必填，缺失时 provider 相关接口不可用）：

```dotenv
SERVER_PORT=8081
PG_DSN=host=localhost user=hify password=hify dbname=hify port=5433 sslmode=disable
REDIS_ADDR=localhost:6379
```

```bash
make migrate-up && make migrate-status   # 预期 8 条全部 applied
make start                               # 日志落 logs/hify.log（stdout 也有 banner）
curl -s localhost:8081/health | jq .      # → {"success":true,"data":"Hify is running",...}
```

## 2. 登录链（后续所有请求都要带 cookie）

```bash
curl -s -X POST localhost:8081/api/v1/auth/register -H 'Content-Type: application/json' \
  -d '{"username":"tester","password":"smoke-test-123"}' | jq .success    # → true
curl -s -c /tmp/hify-jar -X POST localhost:8081/api/v1/auth/login -H 'Content-Type: application/json' \
  -d '{"username":"tester","password":"smoke-test-123"}' | jq .success    # → true
curl -s localhost:8081/api/v1/agents | jq .error.code                    # → "UNAUTHORIZED"（未带 cookie）
```

## 3. 造前置数据（provider + model）

`agents.model_id` 有 RESTRICT 外键，先造 provider 与两个 chat 模型（主 + 备用）：

```bash
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/providers -H 'Content-Type: application/json' \
  -d '{"name":"openai-main","kind":"openai","api_key":"sk-test-123"}' | jq .data.id   # → "1"
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/models -H 'Content-Type: application/json' \
  -d '{"provider_id":1,"name":"GPT-4o","model_id":"gpt-4o","capability":"chat"}' | jq .data.id       # → "1"
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/models -H 'Content-Type: application/json' \
  -d '{"provider_id":1,"name":"GPT-4o-mini","model_id":"gpt-4o-mini","capability":"chat"}' | jq .data.id  # → "2"
```

## 4. 创建（POST /agents）

```bash
# 完整字段：备用模型 + 显式 temperature + max_output_tokens + max_context_turns + enabled
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/agents -H 'Content-Type: application/json' \
  -d '{"name":"客服助手","description":"售后问答","model_id":1,"fallback_model_id":2,
       "system_prompt":"你是售后客服","temperature":0.3,"max_output_tokens":4096,
       "max_context_turns":20,"enabled":true}' | jq .
```

**预期** 201 信封：`data.id="1"`（字符串）、`model_id="1"`、`fallback_model_id="2"`、
`temperature=0.3`、`max_context_turns=20`、`enabled=true`、双时间戳 RFC 3339。
注意请求侧 `model_id` 是**数字**（`"model_id":1`），响应侧一律字符串。

```bash
# 最小字段：temperature / max_context_turns / enabled 缺省 0.7 / 10 / true、fallback 为 null
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/agents -H 'Content-Type: application/json' \
  -d '{"name":"裸 Agent","model_id":1}' \
  | jq -c '{temp: .data.temperature, turns: .data.max_context_turns, en: .data.enabled, fb: .data.fallback_model_id}'
# → {"temp":0.7,"turns":10,"en":true,"fb":null}

# 显式停用（enabled=false 是合法显式值，不得被缺省 true 吞掉）
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/agents -H 'Content-Type: application/json' \
  -d '{"name":"停用 Agent","model_id":1,"enabled":false}' | jq -c '.data.enabled'   # → false

# 轮数越界（binding 1-100）
curl -s -o /dev/null -w '%{http_code}\n' -b /tmp/hify-jar -X POST localhost:8081/api/v1/agents \
  -H 'Content-Type: application/json' -d '{"name":"x","model_id":1,"max_context_turns":0}'   # → 400
```

## 5. 创建的失败路径

```bash
t() { curl -s -o /tmp/r.json -w '%{http_code} ' -b /tmp/hify-jar \
      -X POST localhost:8081/api/v1/agents -H 'Content-Type: application/json' -d "$1"; jq -c '{code: .error.code, msg: .error.message}' /tmp/r.json; }
t '{"model_id":1}'                                          # 400 VALIDATION_FAILED（缺 name）
t '{"name":"x","model_id":1,"temperature":3}'               # 400（越界 >2）
t '{"name":"x","model_id":1,"temperature":-0.5}'            # 400（负数）
t '{"name":"x","model_id":1,"fallback_model_id":1}'         # 400（备用=主模型，跨字段 Validate）
t '{"name":"x","model_id":1,"tool_ids":[10,10]}'            # 400（tool_ids 重复，跨字段 Validate）
t '{"name":"x","model_id":999}'                             # 404 MODEL_NOT_FOUND（provider 哨兵透传）
t '{"name":"x","model_id":1,"tool_ids":[999]}'              # 404 TOOL_NOT_FOUND（FK 23503 翻译）
t '{'                                                       # 400（坏 JSON）
```

跨字段错误（备用=主、tool 重复）的 `error.message` 是具体原因；binding 类是
「参数校验失败」+ `details` 字段级信息。

## 6. 详情 + 缓存（GET /agents/:id）

```bash
curl -s -b /tmp/hify-jar localhost:8081/api/v1/agents/1 | jq -c '{id: .data.id, tool_ids: .data.tool_ids}'
# → {"id":"1","tool_ids":[]}    空绑定返 [] 不返 null（接口规范空值约定）
docker exec hify-redis-test redis-cli --scan --pattern 'hify:cache:agent-cache:*'
# → hify:cache:agent-cache:detail:1   （miss 后回填，TTL 30min；再 get 命中不落库）
curl -s -b /tmp/hify-jar localhost:8081/api/v1/agents/999 | jq -c '.error.code'   # → "AGENT_NOT_FOUND" 404
curl -s -o /dev/null -w '%{http_code}\n' -b /tmp/hify-jar localhost:8081/api/v1/agents/abc  # → 400
```

## 7. 列表（GET /agents）

列表项 = Agent 全字段 + 两列**当页批量现读的聚合列**（`model_name` / `tool_count`，防 N+1）；
无 `tool_ids`（绑定走详情）。

```bash
curl -s -b /tmp/hify-jar 'localhost:8081/api/v1/agents?page=1&page_size=10' \
  | jq -c '.data[] | {name, model_name, tool_count, enabled, max_context_turns}'
# → {"name":"客服助手","model_name":"GPT-4o","tool_count":0,"enabled":true,"max_context_turns":10} ...
curl -s -b /tmp/hify-jar 'localhost:8081/api/v1/agents?page=1&page_size=10' | jq -c '{n: (.data|length), .meta}'
# → {"n":2,"meta":{"page":1,"page_size":10,"total":2}}
curl -s -o /dev/null -w '%{http_code}\n' -b /tmp/hify-jar 'localhost:8081/api/v1/agents?page_size=999'  # → 400
```

聚合口径：`model_name` 来自当页去重 model_id 批量查（provider `ListByIDs`），
**模型被删的悬空引用返回空串 `""`**（前端 fallback 显示裸 id）；`tool_count` 来自
agent_tools 按 agent_id 分组计数（本模块 `CountToolsByAgentIDs`，IN 批量——GORM 对
slice 参数按逗号展开，`= ANY($1,$2)` 是非法语法）。

## 8. 更新（PUT /agents/:id，整体覆盖）

```bash
# body 恶意带 id=999：路径 :id=1 必须胜出（json:"-" 防覆盖）；显式 temperature=0 不得被缺省吞掉
curl -s -b /tmp/hify-jar -X PUT localhost:8081/api/v1/agents/1 -H 'Content-Type: application/json' \
  -d '{"id":999,"name":"客服助手v2","model_id":1,"system_prompt":"新提示词","temperature":0}' \
  | jq -c '{id: .data.id, name: .data.name, temp: .data.temperature}'
# → {"id":"1","name":"客服助手v2","temp":0}

# PUT 漏发 enabled / max_context_turns 会被置回缺省 true / 10（PUT 全量语义——前端必须显式提交）
curl -s -b /tmp/hify-jar -X PUT localhost:8081/api/v1/agents/1 -H 'Content-Type: application/json' \
  -d '{"name":"x","model_id":1,"enabled":false}' | jq -c '{en: .data.enabled, turns: .data.max_context_turns}'
# → {"en":false,"turns":10}    （显式 false 生效；未传 turns → 置回 10）

# 写时删 key：update 后缓存键应消失，再 get 拿到新值（并重新回填）
docker exec hify-redis-test redis-cli --scan --pattern 'hify:cache:agent-cache:*' | wc -l   # → 0
curl -s -b /tmp/hify-jar localhost:8081/api/v1/agents/1 | jq -c '{name: .data.name, temp: .data.temperature}'

curl -s -o /dev/null -w '%{http_code}\n' -b /tmp/hify-jar -X PUT localhost:8081/api/v1/agents/999 \
  -H 'Content-Type: application/json' -d '{"name":"x","model_id":1}'                        # → 404
curl -s -b /tmp/hify-jar -X PUT localhost:8081/api/v1/agents/1 -H 'Content-Type: application/json' \
  -d '{"name":"x","model_id":999}' | jq -c '.error.code'                                    # → "MODEL_NOT_FOUND"
```

## 9. 删除（DELETE /agents/:id，软删）

```bash
curl -s -o /dev/null -w '%{http_code}\n' -b /tmp/hify-jar -X DELETE localhost:8081/api/v1/agents/2  # → 204 无返回体
curl -s -o /dev/null -w '%{http_code}\n' -b /tmp/hify-jar localhost:8081/api/v1/agents/2           # → 404（软删不可见）
curl -s -b /tmp/hify-jar 'localhost:8081/api/v1/agents?page=1&page_size=10' | jq -c '.meta.total'   # → 1
curl -s -o /dev/null -w '%{http_code}\n' -b /tmp/hify-jar -X DELETE localhost:8081/api/v1/agents/2  # → 404（重复删）

# DB 侧：软删行保留（下架语义），deleted_at 非空
docker exec hify-pg-test psql -U hify -d hify -tc "SELECT id, deleted_at IS NOT NULL FROM agents ORDER BY id;"

# delete 也失效缓存：先 get 回填、再 delete、键应清零
curl -s -o /dev/null -b /tmp/hify-jar localhost:8081/api/v1/agents/1
docker exec hify-redis-test redis-cli --scan --pattern 'hify:cache:agent-cache:*'          # → detail:1
curl -s -o /dev/null -w '%{http_code}\n' -b /tmp/hify-jar -X DELETE localhost:8081/api/v1/agents/1  # → 204
docker exec hify-redis-test redis-cli --scan --pattern 'hify:cache:agent-cache:*' | wc -l  # → 0
```

## 10. 日志观测点

| 观测点 | 位置 | 预期 |
|---|---|---|
| 启动事件 | `logs/hify.log` | `"msg":"hify starting","port":"8081",...` + `"hify ready"`；只记各 provider 是否配置，不记 Key |
| 访问日志 | `logs/hify.log` | `"msg":"http request"` 带 method/path/status/duration_ms/client_ip/**trace_id**；X-Request-ID 响应头同值 |
| 缓存异常 | `logs/hify.log` | Redis 故障时 `read agent cache failed; fallback to db` / `evict agent cache failed; ttl fallback`（WARN，业务不失败） |
| provider 探测轮末 | `logs/hify.log` | `"provider probe round done","probed":N,"ok":x,"failed":y`（agent 不探测，仅确认不干扰） |
| 优雅关停 | stdout | `kill` 后 `hify shutting down` 干净退出 |

## 11. 走查结论（2026-08-31，全部通过）

- 5 端点成功路径：201 创建（含缺省值/显式 0 温度两态）、200 详情（空绑定 `[]`）、列表分页 meta、
  200 更新（body id 注入被路径值压住）、204 删除
- 失败路径：400（binding 5 类 + 跨字段 Validate 2 类 + 坏 JSON + 非法 :id）、
  401（未登录）、404（AGENT_NOT_FOUND / MODEL_NOT_FOUND / TOOL_NOT_FOUND，FK 23503 实测触发）
- 缓存三态：miss 回填 → 命中 → update/delete 写时删 key（TTL 30min 兜底）
- 软删语义：DB 行保留、get/list 不可见、重复删 404

### 二次走查（2026-08-31 下午，enabled / max_context_turns + 列表聚合，全部通过）

- 迁移 00009 两列上线：创建缺省 `true` / `10`，显式 `false` / 自定义轮数生效，越界 400
- PUT 全量语义：漏发 enabled / turns 置回 `true` / `10`（前端编辑必须显式提交两字段）
- 列表聚合：`model_name` 批量映射正确（悬空引用 `""`）、`tool_count` 分组计数正确；
  走查中发现并修复 `= ANY(?)` + GORM slice 展开导致的 SQL 语法错误（改 `IN ?`）
- 前端链路（经 5173 Vite 代理，与浏览器同路径）：列表 / 模型下拉（provider→models 过滤
  enabled chat）/ 创建（数值转换载荷）/ 更新 / 详情回填 / 删除 204 全通；
  Vite 代理目标改为随 `SERVER_PORT` 环境变量（start.sh export），本地 8081 端口不再写死
