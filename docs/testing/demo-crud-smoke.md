# Demo CRUD 人工冒烟测试（任务 #6）

验证对象：`internal/demo` 标准 CRUD 参照实现（api → service → store → handler 四层 + 组合根装配 +
`migrations/00008_demo_items.sql`）。全部为手工步骤，自动化覆盖见各包 `*_test.go`。

- 接口前缀：`/api/v1/demo-items`（受登录中间件保护）
- 响应信封：`{success, data, error, meta}`（`platform/respond`）
- 列表分页：偏移分页 `meta = {page, page_size, total}`（= Java PageResult 的 Go 映射）

## 0. 前置条件

| 依赖 | 用途 | 检查命令 |
|---|---|---|
| Docker | 临时 PG / Redis 容器 | `docker version` |
| curl | 接口调用 | `curl --version` |
| jq | 提取响应字段 | `jq --version` |
| 宿主机 5432 / 6379 端口空闲 | 容器端口映射 | `lsof -i :5432 -i :6379` 应无输出 |

> 所有命令默认在**仓库根目录**执行（goose 从当前目录读 `migrations/`，二进制路径为 `./bin/hify`）。

## 1. 启动依赖容器

```bash
docker run -d --name hify-pg-test --rm \
  -e POSTGRES_USER=hify -e POSTGRES_PASSWORD=hify -e POSTGRES_DB=hify \
  -p 5432:5432 pgvector/pgvector:pg17

docker run -d --name hify-redis-test --rm -p 6379:6379 redis:7-alpine
```

等待 PG 就绪（约 2-5 秒）：

```bash
until docker exec hify-pg-test pg_isready -U hify >/dev/null 2>&1; do sleep 1; done && echo "pg ready"
```

**预期**：输出 `pg ready`。

## 2. 配置 .env（本地开发）

`.env` 已存在则核对以下四项；不存在则 `cp .env.example .env` 后修改：

```dotenv
PG_DSN=host=localhost user=hify password=hify dbname=hify port=5432 sslmode=disable
REDIS_ADDR=localhost:6379
SESSION_SECRET=smoke-test-secret-any-non-empty-string   # 必填，缺失启动即 panic
AUTH_COOKIE_SECURE=false                                # 本地 http 必须 false（默认即 false，可不写）
```

> 注意：`.env.example` 里 `PG_DSN` 的 host 是 compose 网络名 `postgres`，本地容器冒烟必须改为 `localhost`。

## 3. 构建二进制 + 执行迁移

```bash
make build-backend        # 产出 ./bin/hify
./bin/hify migrate up
```

**预期**：逐条输出 `migration applied`（含 `version=8 path=00008_demo_items.sql`）；
若此前已迁移过则输出 `no migrations to apply (already up to date)`。

确认状态与表结构：

```bash
./bin/hify migrate status     # 00008 应为 Applied
docker exec hify-pg-test psql -U hify -d hify -c '\d demo_items'
```

**预期**：`demo_items` 含 `id`（bigint identity）、`name`、`status`（带 CHECK）、`created_at`、`updated_at`。

## 4. 启动服务 + 健康检查

新开一个终端（前台运行，冒烟结束 Ctrl+C）：

```bash
./bin/hify
```

**预期**：日志出现 `hify ready addr=:8080`。

原终端验证健康检查（无需登录）：

```bash
curl -s localhost:8080/health
```

**预期**：

```json
{"success":true,"data":"Hify is running","error":null,"meta":null}
```

## 5. 注册 + 登录（拿 cookie）

```bash
# 注册（开放注册）→ 201
curl -s -X POST localhost:8080/api/v1/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"username":"smoke","password":"smoke12345"}'

# 登录 → 200 + Set-Cookie，cookie 存 cookies.txt 供后续步骤复用
curl -s -c cookies.txt -X POST localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"smoke","password":"smoke12345"}'
```

**预期**：

- 注册返回 `201`，信封 `success=true`，`data.username="smoke"`，`data.id` 为**字符串**。
- 登录返回 `200`，响应头含 `Set-Cookie: hify_session=<64位hex>; Path=/; Max-Age=604800; HttpOnly; SameSite=Lax`（本地无 Secure）。
- `cat cookies.txt` 能看到 `hify_session` 条目。

## 6. 鉴权负例：未登录访问 → 401

```bash
curl -s -o /dev/null -w '%{http_code}\n' localhost:8080/api/v1/demo-items
curl -s localhost:8080/api/v1/demo-items
```

**预期**：`401`，信封 `error.code="UNAUTHORIZED"`。

## 7. 创建（POST → 201）

```bash
curl -s -b cookies.txt -X POST localhost:8080/api/v1/demo-items \
  -H 'Content-Type: application/json' \
  -d '{"name":"第一个条目","status":"draft"}' | tee create.json
```

**预期**（201 + `Cache-Control: no-store`）：

```json
{"success":true,"data":{"id":"1","name":"第一个条目","status":"draft","created_at":"...Z","updated_at":"...Z"},"error":null,"meta":null}
```

提取 ID 供后续步骤使用：

```bash
ITEM_ID=$(jq -r '.data.id' create.json)
echo "ITEM_ID=$ITEM_ID"
```

### 校验负例（统一校验：创建 / 更新同规则）→ 400

```bash
# 非法 status（跨字段 Validate）
curl -s -b cookies.txt -X POST localhost:8080/api/v1/demo-items \
  -H 'Content-Type: application/json' -d '{"name":"x","status":"bogus"}'

# 缺 name（binding required，details.fields 报 json 字段名）
curl -s -b cookies.txt -X POST localhost:8080/api/v1/demo-items \
  -H 'Content-Type: application/json' -d '{"status":"draft"}'
```

**预期**：均 `400`，`error.code="VALIDATION_FAILED"`；前者 message 提示 status 必须是
draft / active / archived 之一，后者 `details.fields=[{"field":"name","msg":"required"}]`。

## 8. 详情（GET /:id）

```bash
curl -s -b cookies.txt localhost:8080/api/v1/demo-items/$ITEM_ID        # 200
curl -s -b cookies.txt localhost:8080/api/v1/demo-items/999999         # 404 哨兵
curl -s -o /dev/null -w '%{http_code}\n' -b cookies.txt localhost:8080/api/v1/demo-items/abc   # 400 非数字
```

**预期**：

- 200：`data.id=$ITEM_ID`、`data.name="第一个条目"`。
- 404：`error.code="DEMO_ITEM_NOT_FOUND"`、`error.message="demo 条目不存在"`。
- 400：路径参数非数字，`VALIDATION_FAILED`。

## 9. 列表（GET，偏移分页 = PageResult）

再造一条数据后查列表：

```bash
curl -s -b cookies.txt -X POST localhost:8080/api/v1/demo-items \
  -H 'Content-Type: application/json' -d '{"name":"第二个条目","status":"active"}' >/dev/null

curl -s -b cookies.txt 'localhost:8080/api/v1/demo-items?page=1&page_size=20'
```

**预期**：`200`，`data` 为 2 条数组（`id` 降序，新建在前），`meta` 为：

```json
{"page":1,"page_size":20,"total":2}
```

再验分页越界拒绝：

```bash
curl -s -o /dev/null -w '%{http_code}\n' -b cookies.txt 'localhost:8080/api/v1/demo-items?page_size=101'
```

**预期**：`400`（page_size 上限 100）。

## 10. 更新（PUT /:id，整体更新）

```bash
curl -s -b cookies.txt -X PUT localhost:8080/api/v1/demo-items/$ITEM_ID \
  -H 'Content-Type: application/json' -d '{"name":"更新后的条目","status":"archived"}'
```

**预期**：`200`，`data.name="更新后的条目"`、`data.status="archived"`，`updated_at` 晚于创建时。

负例：

```bash
# 不存在的 id → 404 哨兵
curl -s -b cookies.txt -X PUT localhost:8080/api/v1/demo-items/999999 \
  -H 'Content-Type: application/json' -d '{"name":"n","status":"draft"}'

# 非法 status（与创建共用同一校验）→ 400
curl -s -b cookies.txt -X PUT localhost:8080/api/v1/demo-items/$ITEM_ID \
  -H 'Content-Type: application/json' -d '{"name":"n","status":"bogus"}'
```

## 11. 删除（DELETE /:id → 204）

```bash
curl -s -o /dev/null -w '%{http_code}\n' -b cookies.txt -X DELETE localhost:8080/api/v1/demo-items/$ITEM_ID
curl -s -o /dev/null -w '%{http_code}\n' -b cookies.txt localhost:8080/api/v1/demo-items/$ITEM_ID   # 删后取 → 404
curl -s -o /dev/null -w '%{http_code}\n' -b cookies.txt -X DELETE localhost:8080/api/v1/demo-items/999999  # 删不存在 → 404
```

**预期**：`204`（空响应体）→ `404` → `404`。

## 12. DB 直查 + CHECK 约束验证

```bash
# 剩余数据（应只有「第二个条目」，status 列只可能出现三个合法值）
docker exec hify-pg-test psql -U hify -d hify \
  -c 'SELECT id, name, status, created_at FROM demo_items ORDER BY id;'

# CHECK 约束负例：非法 status 必须被 PG 拒绝（23514 check_violation）
docker exec hify-pg-test psql -U hify -d hify \
  -c "INSERT INTO demo_items (name, status) VALUES ('bad', 'bogus');"
```

**预期**：第二条报 `ERROR: new row for relation "demo_items" violates check constraint ...`。

## 13. 请求追踪与访问日志验证（platform/traceid + httpmw + logging）

前置：服务仍在运行（第 14 步才停）。本地 dev `.env` 为 `LOG_FORMAT=text`、`LOG_FILE=` 空 → 日志只走 stdout（即运行 `./bin/hify` 的终端）。

### 13.1 响应头 + 访问日志（任意请求）

```bash
# ① 任何响应都带 X-Request-ID（服务端生成、每请求唯一；连 /health 也有）
curl -s -D - -o /dev/null http://localhost:8080/health | grep -i x-request-id
curl -s -D - -o /dev/null http://localhost:8080/health | grep -i x-request-id   # 第二次 id 必须不同

# ② 访问日志：发一个真实业务请求，观察服务端终端日志
curl -s -b cookies.txt http://localhost:8080/api/v1/demo-items > /dev/null
```

**预期**：

- ① 响应头含 `X-Request-ID: <32 位 hex>`，两次请求的 id 不同。
- ② 服务端终端出现一行 `level=INFO msg="http request" method=GET path=/api/v1/demo-items status=200 duration_ms=… client_ip=… trace_id=…`，且 `trace_id` = 该响应头 `X-Request-ID` 的值。
- `/health` 请求**不产生**访问日志（healthcheck 探活豁免），但响应头仍带 X-Request-ID。

### 13.2 业务错误：只记访问日志、无 ERROR（复用第 6 / 7 步负例）

```bash
# 400 校验失败 / 404 不存在 / 401 未登录
curl -s -b cookies.txt -X POST http://localhost:8080/api/v1/demo-items -H 'Content-Type: application/json' -d '{}'
curl -s -b cookies.txt http://localhost:8080/api/v1/demo-items/999999
curl -s http://localhost:8080/api/v1/demo-items          # 不带 cookie
```

**预期**：

- 信封分别为 `VALIDATION_FAILED` / `DEMO_ITEM_NOT_FOUND` / `UNAUTHORIZED`，三个响应头仍各带 X-Request-ID。
- 服务端对每条请求**只多一行**访问日志（`status=400/404/401`），**没有任何 ERROR 日志**——业务错误经哨兵映射、不进 500 兜底；ERROR 级只留给未预期错误（级别语义：WARN 可疑 / ERROR 失败）。

### 13.3 慢请求 WARN（>1s）：docker pause 构造（无损）

```bash
# 终端 A：冻结 PG——请求会卡在查询上，duration 超阈值；pause 不删容器，解冻即复原
docker pause hify-pg-test

# 终端 B：发一个 CRUD 请求（阻塞直到解冻）
curl -s -b cookies.txt http://localhost:8080/api/v1/demo-items > /dev/null

# 终端 A：等 2–3 秒后解冻
docker unpause hify-pg-test
```

**预期**：服务端终端出现 `level=WARN msg="slow http request" … duration_ms≥2000 trace_id=…`；解冻后该请求正常返回 200。
⚠️ 勿用 `docker stop` 构造故障——测试容器带 `--rm`，停止即删除、数据全丢。

### 13.4 CRUD 触发不了的项（自动化测试兜底）

| 项 | 原因 | 覆盖 |
|---|---|---|
| 500 body 带 `(trace …)` + 原始 err 进 ERROR 日志 | demo handler 无自然 500 路径（错误全被哨兵映射） | `go test -race ./internal/platform/respond/` |
| panic 兜底（Recovery） | 同上 | 同上（`TestRecovery_*`） |
| SSE 流豁免慢判定 | 尚无 SSE 接口（chat 模块未建） | `go test -race ./internal/platform/httpmw/` |

## 14. 清理

```bash
# 停止服务（运行 ./bin/hify 的终端 Ctrl+C）
docker stop hify-pg-test hify-redis-test   # --rm 容器停止即自动删除
rm -f cookies.txt create.json
```

如需连表一起清掉（demo 是参照实现，可随时移除）：

```bash
./bin/hify migrate down    # 回滚最近一个迁移（00008，DROP demo_items）
```

## 附：接口结果速查表

| # | 请求 | 预期状态 | 信封关键字段 |
|---|---|---|---|
| 0 | 任意请求 | — | 响应头 `X-Request-ID`（32 位 hex、每请求唯一） |
| 1 | `GET /health` | 200 | `data="Hify is running"`（不产生访问日志） |
| 2 | `POST /auth/register` | 201 | `data.id` 字符串 |
| 3 | `POST /auth/login` | 200 | `Set-Cookie: hify_session` |
| 4 | `GET /demo-items`（无 cookie） | 401 | `error.code=UNAUTHORIZED` |
| 5 | `POST /demo-items` 合法 | 201 | `data.id` 字符串 + `Cache-Control: no-store` |
| 6 | `POST /demo-items` 非法 status / 缺 name | 400 | `error.code=VALIDATION_FAILED` |
| 7 | `GET /demo-items/:id` 存在 | 200 | 全字段 |
| 8 | `GET /demo-items/:id` 不存在 | 404 | `error.code=DEMO_ITEM_NOT_FOUND` |
| 9 | `GET /demo-items/:id` 非数字 | 400 | `VALIDATION_FAILED` |
| 10 | `GET /demo-items?page&page_size` | 200 | `meta={page,page_size,total}`、`data` 数组（空为 `[]`） |
| 11 | `PUT /demo-items/:id` 合法 | 200 | name/status 已更新 |
| 12 | `DELETE /demo-items/:id` 存在 | 204 | 空响应体 |
| 13 | `DELETE /demo-items/:id` 不存在 | 404 | `DEMO_ITEM_NOT_FOUND` |
