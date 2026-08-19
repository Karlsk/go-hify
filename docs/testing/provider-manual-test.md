# Provider 模块人工冒烟测试

验证对象：`internal/provider` 全链路（api → service → store → handler + 组合根接线 + 定时探测 +
模型同步）。全部为手工步骤，自动化覆盖见各包 `*_test.go`；实现决策见
`docs/changelog/provider/`（db_model / handler_spec / wiring_sync_spec）。

- 接口前缀：`/api/v1`（providers 6 端点 + 嵌套 models 2 端点 + models 4 端点，共 12 条，受登录中间件保护）
- 响应信封：`{success, data, error, meta}`（`platform/respond`）；ID 一律字符串
- 列表分页：偏移分页 `meta = {page, page_size, total}`
- 本文档所有命令在**仓库根目录**执行，服务地址 `http://localhost:8080`

## 0. 前置条件

| 依赖 | 用途 | 检查命令 |
|---|---|---|
| Docker | 临时 PG / Redis 容器 | `docker version` |
| curl / jq | 接口调用与字段提取 | `curl --version && jq --version` |
| python3 | 离线 mock 模型目录桩（§5、§6） | `python3 --version` |

## 1. 启动依赖容器 + 迁移 + 启动服务

```bash
docker run -d --name hify-pg-test --rm \
  -e POSTGRES_USER=hify -e POSTGRES_PASSWORD=hify -e POSTGRES_DB=hify \
  -p 5432:5432 pgvector/pgvector:pg17
docker run -d --name hify-redis-test --rm -p 6379:6379 redis:7-alpine
until docker exec hify-pg-test pg_isready -U hify >/dev/null 2>&1; do sleep 1; done && echo "pg ready"
```

`.env` 核对（`cp .env.example .env` 后改；PG_DSN 本地容器用 `localhost`）：

```dotenv
PG_DSN=host=localhost user=hify password=hify dbname=hify port=5432 sslmode=disable
REDIS_ADDR=localhost:6379
SESSION_SECRET=provider-smoke-secret
PROVIDER_MASTER_KEY=<openssl rand -base64 32 的输出>   # 必填：DB 内 API Key 加密主密钥
AUTH_COOKIE_SECURE=false                                # 本地 http 关（默认即 false）
```

```bash
make build-backend && ./bin/hify migrate up   # 预期逐条 migration applied
./bin/hify &                                   # 后台跑，日志在 stdout；另开终端继续
curl -s localhost:8080/health | jq .
```

**预期**：`{"success":true,"data":"Hify is running",...}`；启动日志含 `hify ready` 与
`provider prober started`（含 interval）。
（graceful shutdown 本批已接：`kill %1` 应输出 `hify shutting down` 与 `provider prober stopped`
后干净退出，无 panic。）

## 2. 登录链（后续所有请求都要带 cookie）

```bash
curl -s -X POST localhost:8080/api/v1/auth/register -H 'Content-Type: application/json' \
  -d '{"username":"tester","password":"smoke-test-123"}' | jq .success    # → true
curl -s -c /tmp/hify-jar -X POST localhost:8080/api/v1/auth/login -H 'Content-Type: application/json' \
  -d '{"username":"tester","password":"smoke-test-123"}' | jq .success    # → true，cookie 存入 jar
curl -s -b /tmp/hify-jar localhost:8080/api/v1/auth/me | jq .data.username  # → "tester"
curl -s localhost:8080/api/v1/providers | jq .error.code                    # → "UNAUTHORIZED"（未带 cookie）
```

## 3. Provider CRUD 走查

### 3.1 创建（三种形态）

```bash
# 形态一：官方 OpenAI（真实 key 路径，§5 有免 key 的离线路径）
curl -s -b /tmp/hify-jar -X POST localhost:8080/api/v1/providers -H 'Content-Type: application/json' \
  -d '{"name":"OpenAI 官方","kind":"openai","api_key":"sk-xxx"}' | jq .

# 形态二：openai_compatible（指向本地 mock，§5 启动；免真实 key）
curl -s -b /tmp/hify-jar -X POST localhost:8080/api/v1/providers -H 'Content-Type: application/json' \
  -d '{"name":"Mock 兼容网关","kind":"openai_compatible","base_url":"http://127.0.0.1:9999"}' | jq .

# 形态三：ollama 本地
curl -s -b /tmp/hify-jar -X POST localhost:8080/api/v1/providers -H 'Content-Type: application/json' \
  -d '{"name":"Ollama 本地","kind":"ollama","base_url":"http://localhost:11434"}' | jq .
```

**预期**：三条均 201；`data.kind` 回显、`data.has_api_key`：形态一 true / 二三 false；
**任何响应都不出现 `api_key` / 密文字段**（只有 `has_api_key` + 详情里的打码 `api_key_masked`）。
记录形态二的 `id`（下文以 `$PID` 代称，如 `2`）。

### 3.2 列表（分页 + 筛选）

```bash
curl -s -b /tmp/hify-jar 'localhost:8080/api/v1/providers?page=1&page_size=2' | jq '.meta, (.data|length)'
curl -s -b /tmp/hify-jar 'localhost:8080/api/v1/providers?kind=ollama' | jq '.data[].name'
curl -s -b /tmp/hify-jar 'localhost:8080/api/v1/providers?enabled=false' | jq '.data|length'   # → 0（新建默认 enabled）
```

**预期**：`meta = {"page":1,"page_size":2,"total":3}`；kind 筛选只回 Ollama。

### 3.3 详情（聚合 models + health）

```bash
curl -s -b /tmp/hify-jar localhost:8080/api/v1/providers/$PID | jq '.data.models, .data.health'
```

**预期**：`models` 为 `[]`（还没模型）、`health` 为 `null`（从未探测）——若已过一轮定时探测则
`health.status` 为 `"up"`（mock 正常应答时）。

### 3.4 更新（改名 / 轮换 key / 停用）

```bash
curl -s -b /tmp/hify-jar -X PUT localhost:8080/api/v1/providers/$PID -H 'Content-Type: application/json' \
  -d '{"name":"Mock 网关改名","base_url":"http://127.0.0.1:9999","enabled":true}' | jq '.data.name'
curl -s -b /tmp/hify-jar -X PUT localhost:8080/api/v1/providers/$PID -H 'Content-Type: application/json' \
  -d '{"id":99999,"name":"恶意 id 覆盖","base_url":"http://127.0.0.1:9999","enabled":true}' | jq '.data.id'
```

**预期**：第一条改名生效；第二条仍改的是 `$PID`（body 的 `id` 被忽略——防覆盖回归样例）。
对形态一（带 key 的）再 PUT 带 `"api_key":"sk-new"`，详情的 `api_key_rotated_at` 应更新。

### 3.5 手动连通性探测

```bash
curl -s -b /tmp/hify-jar -X POST localhost:8080/api/v1/providers/$PID/test-connection | jq .
```

**预期**：mock 桩在线时 200 + `{"success":true,"latency_ms":<个位数>,"model_count":2}`；
桩关掉后仍 **HTTP 200** 但 `success:false` + `error_message`（探测失败是业务结果不是 HTTP 错误）。
服务日志对应一条 `provider probed`（`source:"manual"`、`status`、`latency_ms`）。
随后 `GET /providers/$PID` 的 `health.status` 变为 `"up"`（或桩关掉时 `"degraded"`，首次失败）。

### 3.6 删除

```bash
curl -s -i -b /tmp/hify-jar -X DELETE localhost:8080/api/v1/providers/3 | head -1   # → HTTP/1.1 204
```

## 4. Model 手动管理

```bash
# 创建（capability=embedding 必须带 embedding_dim）
curl -s -b /tmp/hify-jar -X POST localhost:8080/api/v1/models -H 'Content-Type: application/json' \
  -d '{"provider_id":'"$PID"',"name":"自定义模型","model_id":"my-model","capability":"chat"}' | jq '.data.source'  # → "manual"

# embedding 缺 dim → 400 VALIDATION_FAILED（details.fields 有字段级原因）
curl -s -b /tmp/hify-jar -X POST localhost:8080/api/v1/models -H 'Content-Type: application/json' \
  -d '{"provider_id":'"$PID"',"name":"坏 embedding","model_id":"bad-emb","capability":"embedding"}' | jq '.error.code'

# 单查 / 改 / 列表
curl -s -b /tmp/hify-jar localhost:8080/api/v1/models/1 | jq '.data.model_id'
curl -s -b /tmp/hify-jar -X PUT localhost:8080/api/v1/models/1 -H 'Content-Type: application/json' \
  -d '{"id":999,"provider_id":'"$PID"',"name":"改名","model_id":"my-model","capability":"chat"}' | jq '.data.name'
curl -s -b /tmp/hify-jar 'localhost:8080/api/v1/providers/'$PID'/models?page=1&page_size=10' | jq '.meta.total'
curl -s -i -b /tmp/hify-jar -X DELETE localhost:8080/api/v1/models/1 | head -1   # → 204
```

**预期**：PUT 的 body `id:999` 不生效（改的是路径上的 1）；列表 `meta.total` 与实际一致。

## 5. 模型同步 SyncModels（离线 mock 桩，免真实 key）

先起 mock 桩（模拟 openai_compatible 的 `/models` 目录）：

```bash
python3 - <<'EOF' &
from http.server import BaseHTTPRequestHandler, HTTPServer
import json
class H(BaseHTTPRequestHandler):
    def do_GET(self):
        body = json.dumps({"data": [
            {"id": "mock-chat"},
            {"id": "mock-embed"}]}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def log_message(self, *a): pass
HTTPServer(("127.0.0.1", 9999), H).serve_forever()
EOF
```

```bash
curl -s -b /tmp/hify-jar -X POST localhost:8080/api/v1/providers/$PID/models/sync | jq '.data'
# → {"added":2,"updated":0}
curl -s -b /tmp/hify-jar 'localhost:8080/api/v1/providers/'$PID'/models' | jq '.data[]|{model_id,name,source,capability}'
```

**预期**：两条入库，`source:"discovered"`、`capability:"chat"`（上游列表无能力元数据的默认值）、
`name` 回退 model_id、**`enabled:false`（导入默认待启用）**。

**勾选启用（sync_default_disabled_spec.md）**：

```bash
# 从上面列表输出拿到 mock-chat 的 id（下文以 $MID 代称），PUT 翻 enabled
curl -s -b /tmp/hify-jar -X PUT localhost:8080/api/v1/models/$MID -H 'Content-Type: application/json' \
  -d '{"provider_id":'"$PID"',"name":"mock-chat","model_id":"mock-chat","capability":"chat","enabled":true}' | jq '.data.enabled'  # → true
# 再 sync 一轮（同目录），勾选不被打回
curl -s -b /tmp/hify-jar -X POST localhost:8080/api/v1/providers/$PID/models/sync | jq '.data'   # → {"added":0,"updated":0}
curl -s -b /tmp/hify-jar 'localhost:8080/api/v1/providers/'$PID'/models' | jq '.data[]|select(.model_id=="mock-chat")|.enabled'  # → true
```

**手编保护**：手动 PUT 改 `mock-chat` 的 name 为 "我改过的"，把桩重启为不同目录
（如 `{"data":[{"id":"mock-chat"},{"id":"mock-embed"},{"id":"mock-new"}]}`）再 sync：

```bash
curl -s -b /tmp/hify-jar -X POST localhost:8080/api/v1/providers/$PID/models/sync | jq '.data'
# → {"added":1,"updated":0}——只有 mock-new 新增，手编行（manual 来源）完全不被触碰
```

**上游失败**：把桩 kill 掉再 sync → HTTP 503 + `error.code:"SERVICE_UNAVAILABLE"`；
把桩改成返回 401（脚本里 `self.send_response(401)`）→ 同样 503。

> 真实 key 路径（可选）：对 §3.1 形态一的官方 OpenAI provider 直接 sync，
> 预期 added 为该 key 可见的模型数（数十条），无需人工干预。

## 6. 定时探测（StartProber，60s 一轮）

前提：`$PID` 指向 mock 桩且 enabled=true。

日志观测（确认 ticker 存活的最直接线索，每分钟各一条）：
`provider probe round done`（probed/ok/failed/duration_ms 轮汇总）+ 每个 enabled provider 的
`provider probed`（`source:"scheduled"`、status、latency_ms、model_count）。

1. **up**：桩在线，等 60-70s 后 `GET /providers/$PID` → `health.status:"up"`、`last_check_at` 每分钟刷新。
2. **degraded → down**：kill 桩，约 1 分钟后 status 变 `degraded`（fail_count=1..2），
   连续 3 轮失败（约 3-4 分钟）后变 `down`，服务日志出现一条 `provider flipped to down` WARN
   （同轮 `provider probed` 的 `status:"down"`）。
3. **恢复**：重启桩，下一轮回到 `up`（fail_count 清零）。
4. **停用豁免**：PUT `enabled:false` 后即使桩关闭，health 不再变化
   （轮汇总 `probed` 计数减一，该 provider 不再有 `provider probed` 行）。

> 观察加速：也可以每轮手动 `POST .../test-connection` 复用同一状态机，但定时轮验的就是
> "没人戳也会自己探测"，建议至少等满一轮确认 ticker 在跑。

## 7. 错误分支回归表

| 场景 | 请求 | 预期 |
|---|---|---|
| 资源不存在 | `GET /providers/9999` | 404 `PROVIDER_NOT_FOUND` |
| 坏路径 id | `GET /providers/abc` | 400 `VALIDATION_FAILED` |
| 名称冲突 | 重复 POST 同 name | 409 `PROVIDER_NAME_CONFLICT` |
| 缺参数 | POST providers 不带 kind | 400 + `details.fields` |
| 跨字段校验 | kind=openai_compatible 无 base_url | 400 `VALIDATION_FAILED` |
| kind 非法 | kind=foo | 400（binding oneof） |
| model 冲突 | 同 provider 重复 model_id | 409 `MODEL_ID_CONFLICT` |
| model 不存在 | `GET /models/9999` | 404 `MODEL_NOT_FOUND` |
| 删除被引用 | 删被 agents 引用的 model（需手工向 agents 表插行，可跳过） | 409 `MODEL_IN_USE` |
| sync 上游挂 | 桩关闭后 sync | 503 `SERVICE_UNAVAILABLE` |
| 未登录 | 不带 cookie 的任意 /api/v1 | 401 `UNAUTHORIZED` |
| 删除成功 | `DELETE /providers/:id` | 204 无响应体 |

## 8. 回归清单（后续批次跑 provider 回归时过一遍）

- [ ] 12 端点全部 2xx/预期码走查（§3-§5）
- [ ] 任何响应与日志中无 API Key 明文 / 密文（`grep -i 'sk-\|api_key' 日志文件` 无命中）
- [ ] `PUT` body 带 `id` 不覆盖路径 id（§3.4、§4）
- [ ] sync：added/updated 计数、manual 行保护、幂等（同目录二次 sync 0/0）
- [ ] sync 上游失败 503；成功后 detail 缓存失效（改完立刻 GET 详情可见新 models）
- [ ] 定时探测 up → degraded → down → up 时间线（§6）
- [ ] `kill`（SIGTERM）优雅退出：`hify shutting down` 日志、进程退出码 0
- [ ] 重启后缓存为空但功能正常（PG 是唯一事实源，Redis 只缓存）

## 9. 前端联调走查（ProviderList + ProviderModelsDrawer，已接真实 API）

前置：`cd web && npm run dev`（:5173，/api 代理到 :8080）；后端按 §1 起好、§2 登录（浏览器走 `/login` 页）。

| # | 场景 | 操作 | 预期 |
|---|---|---|---|
| 1 | 列表加载 | 进入「模型提供商」页 | 信封拆包正常，行数据来自 GET /providers；分页器 total 正确 |
| 2 | 健康状态列 | 未探测的新行 / §6 探测过的行 | 未探测显示灰 tag「未探测」；探测过的显示 正常(绿)/降级(黄)/故障(红) + 延迟 ms 小字 |
| 3 | 模型数列 | §5 sync 过的 provider | 数字 = enabled=true 计数（sync 新增是 enabled=false，不计数）；点击数字打开模型抽屉 |
| 4 | 连通性测试 | 点行内「测试」 | 按钮转 loading ≤10s；成功 toast `连接成功 · Xms · N 个模型`；失败 toast 含 error_message；结束后列表自动刷新、健康列更新 |
| 5 | 新增提供商 | 「新增提供商」→ 填名称/类型 | openai/claude/gemini 不填 Key 无法提交（表单校验）；选「兼容网关」时 Base URL 变必填；创建成功 toast + 列表刷新 |
| 6 | 编辑提供商 | 编辑任一行 | kind 下拉禁用（创建后不可改）；Key placeholder「已设置：留空保留」；「启用」开关回显当前状态；改名保存后列表刷新 |
| 7 | PUT enabled 陷阱回归 | 停用某 provider → 编辑只改名保存 | enabled 保持停用（开关随行状态回显，PUT 全量提交不清掉） |
| 8 | 删除提供商 | 删除 mock 桩 provider | 确认框红按钮；成功 toast + 行消失（models/health 级联） |
| 9 | 抽屉·列表 | 点模型数打开抽屉 | 标题「{provider 名} · 模型」；能力/来源 tag、上下文 K、启用开关渲染正确；分页可用 |
| 10 | 抽屉·同步 | 点「同步模型」 | toast `同步完成：新增 X，更新 Y`；新行 enabled=false（开关灰） |
| 11 | 抽屉·启停开关 | 打开某模型开关 | 行内即时翻转；失败（如 embedding 缺 dim 的行）自动回滚 + 拦截器弹错 |
| 12 | 抽屉·手动新增 | 「手动新增」 | 名称留空提交 → 库里 name=model_id；能力选 embedding 时出现「嵌入维度」必填项；chat 选 embedding 维度不发送 |
| 13 | 抽屉·删除 | 删除自动发现的模型 | 确认 → 行消失；409 MODEL_IN_USE（被引用）时拦截器弹后端消息 |
| 14 | 错误信封 | 断开后端点「测试」 | 拦截器统一 ElMessage.error，页面不崩、loading 复位 |

走查后回归：§8 全项仍过。
