# Workflow 模块人工冒烟测试

验证对象：`internal/workflow` 全链路（api → service → store → handler + 组合根接线）。
全部为手工步骤，自动化覆盖见各包 `*_test.go`；实现决策见 `docs/changelog/workflow/`。

- 接口前缀：`/api/v1/workflows`（8 端点：POST 创建 / GET 列表 / GET 详情 / PUT 更新 / DELETE 删除 / POST publish / POST disable / POST execute，受登录中间件保护）
- 响应信封：`{success, data, error, meta}`（`platform/respond`）；ID 一律字符串
- 列表分页：偏移分页 `meta = {page, page_size, total}`（workflows 是极小配置表，规范允许）
- 本文档命令在**仓库根目录**执行

> **端口说明**：本机 `.env` 配置 `SERVER_PORT=8081`、PG `port=5433`（被另一项目占用）。
> 端口空出来后改回 `.env` 即可。本文档所有命令按 8081 写。

## 0. 前置条件

| 依赖 | 用途 | 检查命令 |
|---|---|---|
| Docker | 临时 PG / Redis 容器 | `docker version` |
| curl / jq | 接口调用与字段提取 | `curl --version && jq --version` |

## 1. 启动依赖容器 + 迁移 + 启动服务

```bash
# 如容器已存在先清理
docker rm -f hify-pg-test hify-redis-test 2>/dev/null

docker run -d --name hify-pg-test \
  -e POSTGRES_USER=hify -e POSTGRES_PASSWORD=hify -e POSTGRES_DB=hify \
  -p 5433:5432 pgvector/pgvector:pg17

docker run -d --name hify-redis-test -p 6379:6379 redis:7-alpine

until docker exec hify-pg-test pg_isready -U hify >/dev/null 2>&1; do sleep 1; done && echo "pg ready"
```

`.env` 核对（本地 dev 端口）：

```dotenv
SERVER_PORT=8081
PG_DSN=host=localhost user=hify password=hify dbname=hify port=5433 sslmode=disable
REDIS_ADDR=localhost:6379
```

```bash
make migrate-up && make migrate-status   # 预期 20 条全部 applied（00020 = spec 08 分型与嵌套）
make start                               # 日志落 logs/hify.log
curl -s localhost:8081/health | jq .     # → {"success":true,...}
```

## 2. 登录链（后续所有请求都要带 cookie）

```bash
curl -s -X POST localhost:8081/api/v1/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"username":"wf-tester","password":"smoke-wf-123"}' | jq .success     # → true

curl -s -c /tmp/hify-jar -X POST localhost:8081/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"wf-tester","password":"smoke-wf-123"}' | jq .success     # → true

# 未认证访问应被拦
curl -s localhost:8081/api/v1/workflows | jq .error.code                    # → "UNAUTHORIZED"
```

## 3. 造前置数据（provider + model）

workflow LLM 节点的 `model_id` 有存在性预检（service 经 `providerapi.ModelService.Get`），
先造 provider 与一个 chat 模型：

```bash
# 创建 provider
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/providers \
  -H 'Content-Type: application/json' \
  -d '{"name":"wf-smoke-provider","kind":"openai_compatible","api_key":"sk-test-wf-123"}' | jq .data.id   # → "1"

# 创建 model（capability=chat，LLM 节点引用）
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/models \
  -H 'Content-Type: application/json' \
  -d '{"provider_id":1,"name":"GPT-4o-mini","model_id":"gpt-4o-mini","capability":"chat"}' | jq .data.id  # → "1"
```

## 4. 创建（POST /workflows）

线性图：`classify`(llm) → `end`(end)，最简合法 graph。

```bash
WF=$(curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/workflows \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "客服分流",
    "description": "根据用户消息分类后终止",
    "type": "chat",
    "start_node_key": "classify",
    "nodes": [
      {"key":"classify","type":"llm","name":"分类节点",
       "config":{"model_id":"1","prompt":"将消息分类为 ORDER_QUERY 或 OTHER: {{input}}"}},
      {"key":"end","type":"end","name":"结束","config":{}}
    ],
    "edges": [
      {"source_node_key":"classify","target_node_key":"end"}
    ]
  }')
echo "$WF" | jq .
```

**预期**：
- HTTP 201
- `data.status` = `"draft"`，`data.type` = `"chat"`（spec 08 起响应带分型）
- `data.id` 为非空字符串
- `data.nodes` 长度 2，`data.edges` 长度 1
- `data.start_node_key` = `"classify"`

```bash
WF_ID=$(echo "$WF" | jq -r .data.id)
echo "workflow id = $WF_ID"
```

### 4.1 创建失败 — 名称冲突

```bash
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/workflows \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "客服分流",
    "type": "chat",
    "start_node_key": "a",
    "nodes": [{"key":"a","type":"end","config":{}}],
    "edges": []
  }' | jq .error.code   # → "WORKFLOW_NAME_CONFLICT"（409）
```

### 4.2 创建失败 — 图校验错误（start_node_key 不存在）

```bash
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/workflows \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "bad-graph",
    "type": "chat",
    "start_node_key": "nonexistent",
    "nodes": [{"key":"a","type":"end","config":{}}],
    "edges": []
  }' | jq .error.code   # → "VALIDATION_FAILED"（400）
```

### 4.3 创建失败 — LLM 节点 model_id 不存在

```bash
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/workflows \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "bad-model",
    "type": "chat",
    "start_node_key": "n",
    "nodes": [{"key":"n","type":"llm","config":{"model_id":"9999","prompt":"hi"}}],
    "edges": []
  }' | jq '.error.code, .error.message'   # → "VALIDATION_FAILED", message 含 node "n" model_id 9999
```

## 5. 获取详情（GET /workflows/{id}）

```bash
curl -s -b /tmp/hify-jar "localhost:8081/api/v1/workflows/$WF_ID" | jq .
```

**预期**：
- HTTP 200
- `data` 字段与创建 round-trip 一致（name / description / nodes / edges / start_node_key）
- `data.status` = `"draft"`
- `data.nodes` 非 null（`[]` 或数组）
- `data.edges` 非 null

### 5.1 获取不存在

```bash
curl -s -b /tmp/hify-jar "localhost:8081/api/v1/workflows/99999" | jq .error.code   # → "WORKFLOW_NOT_FOUND"（404）
```

## 6. 列表（GET /workflows）

```bash
curl -s -b /tmp/hify-jar "localhost:8081/api/v1/workflows?page=1&page_size=20" | jq .
```

**预期**：
- HTTP 200
- `data` 为数组，含刚创建的 workflow
- `meta.page` = 1, `meta.page_size` = 20, `meta.total` ≥ 1

## 7. 发布（POST /workflows/{id}/publish）

```bash
curl -s -b /tmp/hify-jar -X POST "localhost:8081/api/v1/workflows/$WF_ID/publish" | jq .
```

**预期**：
- HTTP 200
- `data.status` = `"published"`

### 7.1 发布幂等（重复调用）

```bash
curl -s -b /tmp/hify-jar -X POST "localhost:8081/api/v1/workflows/$WF_ID/publish" | jq .data.status
# → "published"（幂等，状态不变）
```

## 8. 更新（PUT /workflows/{id}）

```bash
curl -s -b /tmp/hify-jar -X PUT "localhost:8081/api/v1/workflows/$WF_ID" \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "客服分流 v2",
    "description": "更新描述：增加 end 节点说明",
    "start_node_key": "classify",
    "nodes": [
      {"key":"classify","type":"llm","name":"分类节点 v2",
       "config":{"model_id":"1","prompt":"新版提示词: {{input}}"}},
      {"key":"end","type":"end","name":"结束","config":{}}
    ],
    "edges": [
      {"source_node_key":"classify","target_node_key":"end"}
    ]
  }' | jq '.data.name, .data.status, (.data.nodes | length)'
```

**预期**：
- HTTP 200
- name 更新为 `"客服分流 v2"`
- status 仍为 `"published"`（编辑不降级）
- nodes 长度 2
- PUT body 不携带 `type`（合法——spec 08 分型不可变，携带即 400，负向验证见 workflow-engine-manual-test.md §7.1③）

## 9. 停用（POST /workflows/{id}/disable）

```bash
curl -s -b /tmp/hify-jar -X POST "localhost:8081/api/v1/workflows/$WF_ID/disable" | jq .data.status
# → "disabled"
```

### 9.1 停用幂等（再调一次）

```bash
curl -s -b /tmp/hify-jar -X POST "localhost:8081/api/v1/workflows/$WF_ID/disable" | jq .data.status
# → "disabled"（幂等，已停用再调幂等——moved=false 返回 summary）
```

## 10. 编辑后状态保持（PUT 时 status 不降级）

```bash
curl -s -b /tmp/hify-jar -X PUT "localhost:8081/api/v1/workflows/$WF_ID" \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "客服分流 v3",
    "start_node_key": "classify",
    "nodes": [
      {"key":"classify","type":"llm","name":"分类",
       "config":{"model_id":"1","prompt":"分类: {{input}}"}},
      {"key":"end","type":"end","name":"结束","config":{}}
    ],
    "edges": [{"source_node_key":"classify","target_node_key":"end"}]
  }' | jq '.data.name, .data.status'
```

**预期**：
- name = `"客服分流 v3"`
- status 仍为 `"disabled"`（PUT 不改变 status，编辑不降级）

## 11. 重新发布（disabled → published）

```bash
curl -s -b /tmp/hify-jar -X POST "localhost:8081/api/v1/workflows/$WF_ID/publish" | jq .data.status
# → "published"（disabled 可以直接发布）
```

## 12. 执行（POST /workflows/{id}/execute，spec 06）

对应 [quickstart §3](../../specs/006-workflow-execution-engine/quickstart.md) 五场景：
正式执行 / 试运行 / 失败定位 / 轨迹回放 / SSRF。`$WF_ID` 复用上文（§11 后为 published 态）。

> **前置**：12.1 / 12.2 真调 LLM——§3 造的假 provider（`sk-test-wf-123`）打不通上游，
> 先把 provider 换成真实 key 或本地 Ollama 再跑；12.3 / 12.4 / 12.5 的图不含 llm 节点（或保存期即拦），不依赖。

### 12.1 正式执行（published）

```bash
curl -s -b /tmp/hify-jar -X POST "localhost:8081/api/v1/workflows/$WF_ID/execute" \
  -H 'Content-Type: application/json' \
  -d '{"input":"我的订单到哪了"}' \
  | jq '.data.status, .data.run_id, (.data.node_trace | length), .data.duration_ms'
```

**预期**：
- HTTP 200
- `data.status` = `"succeeded"`，`data.run_id` 为非空字符串
- `data.node_trace` 覆盖全部节点（本图 2 个），每项含 node_key / node_type / status / duration_ms
- `data.duration_ms` > 0（end 空 output = 取末节点输出，`data.output` 即 classify 的回复）

### 12.2 试运行（draft + ?trial=true）

```bash
# 另建一个 draft 副本（不 publish）
WF_TRIAL=$(curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/workflows \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "客服分流-trial",
    "type": "chat",
    "start_node_key": "classify",
    "nodes": [
      {"key":"classify","type":"llm","name":"分类",
       "config":{"model_id":"1","prompt":"将消息分类为 ORDER_QUERY 或 OTHER: {{input}}"}},
      {"key":"end","type":"end","name":"结束","config":{}}
    ],
    "edges": [{"source_node_key":"classify","target_node_key":"end"}]
  }' | jq -r .data.id)

# draft 直接执行 → 503（disabled 同理）
curl -s -b /tmp/hify-jar -X POST "localhost:8081/api/v1/workflows/$WF_TRIAL/execute" \
  -H 'Content-Type: application/json' -d '{"input":"hi"}' | jq .error.code
# → "WORKFLOW_NOT_PUBLISHED"

# ?trial=true 放开（状态机唯一例外）
curl -s -b /tmp/hify-jar -X POST "localhost:8081/api/v1/workflows/$WF_TRIAL/execute?trial=true" \
  -H 'Content-Type: application/json' -d '{"input":"hi"}' | jq '.data.status, .data.run_id'
# → "succeeded"、run_id 非空
```

### 12.3 失败定位

```bash
# ① R10 保存期拦截：prompt 引用非祖先 key（{{clasify}} 错字）→ 创建即 400
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/workflows \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "bad-ref",
    "type": "chat",
    "start_node_key": "classify",
    "nodes": [
      {"key":"classify","type":"llm","name":"分类",
       "config":{"model_id":"1","prompt":"分类: {{clasify}}"}},
      {"key":"end","type":"end","name":"结束","config":{}}
    ],
    "edges": [{"source_node_key":"classify","target_node_key":"end"}]
  }' | jq '.error.code, .error.message'
# → "VALIDATION_FAILED"（400），message 含 node "classify" 与引用名 "clasify"

# ② condition 无命中出边 → 执行期 400，message 带失败节点定位（图不含 llm 节点）
WF_NOMATCH=$(curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/workflows \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "no-match",
    "type": "chat",
    "start_node_key": "r",
    "nodes": [
      {"key":"r","type":"condition","name":"路由","config":{"expression":"{{input}}"}},
      {"key":"end","type":"end","name":"结束","config":{}}
    ],
    "edges": [{"source_node_key":"r","target_node_key":"end","condition":"A"}]
  }' | jq -r .data.id)

curl -s -b /tmp/hify-jar -X POST "localhost:8081/api/v1/workflows/$WF_NOMATCH/publish" >/dev/null
curl -s -b /tmp/hify-jar -X POST "localhost:8081/api/v1/workflows/$WF_NOMATCH/execute" \
  -H 'Content-Type: application/json' -d '{"input":"x"}' | jq '.error.code, .error.message'
# → "VALIDATION_FAILED"（400），message 以 "node r:" 开头（no outgoing edge matched "x"）
```

> 运行期缺失变量兜底在 R10 落地后正常途径已不可达（错字保存时即拦）；
> 执行期可触发的图缺陷是 condition 无命中这类依赖运行时取值的缺陷。

### 12.4 轨迹回放（PG 查 workflow_runs / workflow_node_runs）

```bash
docker exec hify-pg-test psql -U hify -d hify -c \
  "SELECT id, workflow_id, status, is_trial, error_node, duration_ms FROM workflow_runs ORDER BY id DESC LIMIT 5;"
# 预期：12.1 行 is_trial=false；12.2 试运行行 is_trial=true；12.3② 失败行 status='failed' 且 error_node='r'

RUN_ID=<上一查询任一 id>
docker exec hify-pg-test psql -U hify -d hify -c \
  "SELECT seq, node_key, node_type, status, duration_ms FROM workflow_node_runs WHERE run_id = $RUN_ID ORDER BY seq;"
# 预期：seq 从 1 严格递增（回放顺序唯一事实源）；失败 run 的节点行 status='failed'
```

### 12.5 SSRF（api 节点 loopback → 500）

```bash
# api 节点指向 loopback，建连即被拒（图不含 llm 节点；draft + trial 免 publish）
WF_SSRF=$(curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/workflows \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "ssrf-probe",
    "type": "chat",
    "start_node_key": "probe",
    "nodes": [
      {"key":"probe","type":"api","name":"探测",
       "config":{"url":"http://127.0.0.1:8081/health","method":"GET"}}
    ],
    "edges": []
  }' | jq -r .data.id)

curl -s -b /tmp/hify-jar -X POST "localhost:8081/api/v1/workflows/$WF_SSRF/execute?trial=true" \
  -H 'Content-Type: application/json' -d '{"input":"x"}' | jq '.error.code, .error.message'
# → "WORKFLOW_EXECUTION_FAILED"（500），message 以 "node probe:" 开头（loopback 恒禁）
```

### 12.6 chat 触发（trigger_source='chat'，spec 07 管道接线）

绑定该 workflow 的 agent 会话里发消息即触发（管道冒烟），run 行引用回填（对话链 ↔ 执行链
互溯）的完整触发与验证步骤已统一至
[workflow-engine-manual-test.md](workflow-engine-manual-test.md) §5.3，本文档不再重复维护。

## 13. 删除（DELETE /workflows/{id}）

```bash
# 先停用（published 状态也可直接删，此处展示完整流程）
curl -s -b /tmp/hify-jar -X POST "localhost:8081/api/v1/workflows/$WF_ID/disable" >/dev/null

# 删除
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -b /tmp/hify-jar \
  -X DELETE "localhost:8081/api/v1/workflows/$WF_ID")
echo "DELETE status: $HTTP_CODE"   # → 204
```

### 13.1 删除后查 404

```bash
curl -s -b /tmp/hify-jar "localhost:8081/api/v1/workflows/$WF_ID" | jq .error.code
# → "WORKFLOW_NOT_FOUND"（404）
```

### 13.2 workflow_nodes / workflow_edges 随 CASCADE 清空

```bash
# 直接查 DB（需 psql 或 docker exec）
docker exec hify-pg-test psql -U hify -d hify -c \
  "SELECT count(*) FROM workflow_nodes WHERE workflow_id = $WF_ID;"   # → 0
docker exec hify-pg-test psql -U hify -d hify -c \
  "SELECT count(*) FROM workflow_edges WHERE workflow_id = $WF_ID;"   # → 0
```

### 13.3 删除不存在

```bash
curl -s -b /tmp/hify-jar -X DELETE "localhost:8081/api/v1/workflows/99999" | jq .error.code
# → "WORKFLOW_NOT_FOUND"（404）
```

## 14. 无操作数据清理

```bash
# 停服务（Ctrl+C 或 kill）后清理容器
docker rm -f hify-pg-test hify-redis-test 2>/dev/null
```

## 完整命令速查（一键复制）

```bash
# === 环境 ===
docker rm -f hify-pg-test hify-redis-test 2>/dev/null
docker run -d --name hify-pg-test -e POSTGRES_USER=hify -e POSTGRES_PASSWORD=hify -e POSTGRES_DB=hify -p 5433:5432 pgvector/pgvector:pg17
docker run -d --name hify-redis-test -p 6379:6379 redis:7-alpine
until docker exec hify-pg-test pg_isready -U hify >/dev/null 2>&1; do sleep 1; done
make migrate-up && make start

# === 认证 ===
curl -s -X POST localhost:8081/api/v1/auth/register -H 'Content-Type: application/json' -d '{"username":"wf-tester","password":"smoke-wf-123"}'
curl -s -c /tmp/hify-jar -X POST localhost:8081/api/v1/auth/login -H 'Content-Type: application/json' -d '{"username":"wf-tester","password":"smoke-wf-123"}'

# === 前置数据 ===
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/providers -H 'Content-Type: application/json' -d '{"name":"wf-smoke-provider","kind":"openai_compatible","api_key":"sk-test-wf-123"}'
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/models -H 'Content-Type: application/json' -d '{"provider_id":1,"name":"GPT-4o-mini","model_id":"gpt-4o-mini","capability":"chat"}'

# === CRUD ===
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/workflows -H 'Content-Type: application/json' -d '{"name":"客服分流","description":"根据用户消息分类后终止","type":"chat","start_node_key":"classify","nodes":[{"key":"classify","type":"llm","name":"分类节点","config":{"model_id":"1","prompt":"将消息分类为 ORDER_QUERY 或 OTHER: {{input}}"}},{"key":"end","type":"end","name":"结束","config":{}}],"edges":[{"source_node_key":"classify","target_node_key":"end"}]}'
# 提取 id 后 GET / PUT / DELETE / publish / disable 见上文各步骤
# 执行（12.1/12.2 需真实 LLM）：POST /workflows/{id}/execute，body {"input":"..."}，试运行加 ?trial=true，详见 §12
```

## 13. spec 020 增补：react 节点 / ragNode 画布接入 / tool 节点表单（三场景 + 零回归）

交付面：后端 `internal/platform/reactloop`（spec 019 循环，Generate 模式首个消费者）+ `internal/workflow/`（ReactConfig 新节点类型 / KnowledgeRetrievalConfig.query 加法键 / R10 与 precheck 新分支）+ `internal/mcp/`（ToolSourceReq.ServerIDs 并集 + ListToolsByServerIDs）+ 迁移 00022；前端画布 palette 五扩八与三新检查器表单。行为依据：[specs/020-workflow-react-node/spec.md](../../specs/020-workflow-react-node/spec.md)（FR-001~FR-010、SC-001~SC-005）与 [quickstart.md](../../specs/020-workflow-react-node/quickstart.md) 场景 A–D。前置同 §0，追加造数：enabled mcp server（≥1 工具）+ enabled KB（含已入库文档）+ 可用 chat 模型；`make migrate-up` 至 22 applied。

### 13.1 场景 A（US1）：react 节点自主工具调用

1. 画布拖入「ReAct 节点」：模型下拉 / MCP 服务多选 / MCP 工具分组多选 / 双模板 / 温度 / 轮数上限（0 = 默认 8）表单齐备；保存后详情 config 键集对齐 C1（model_id 字符串、server_ids 数字数组、无 max_rounds 键当留 0）
2. `llm 分类 → react（绑一个 server）→ end 映射` 试运行：react 内 ≥1 轮工具调用后终答；end 终稿引用 `{{agent}}` 成功
3. 运行详情：node_trace react 单行；node_runs react 行 input 含 `tool_chain`（逐调用 round/id/name/args/is_error）与 rounds / max_rounds；executions 表 react 内每轮 LLM 各一行（conversation_id NULL）
4. max_rounds=1 + 诱导持续调用 → 第 2 轮 nudge 收尾、终答照落（不报错）
5. 停用所绑 server 再试运行 → 失败 alert `MCP_TOOL_NOT_FOUND`、error_node = react 节点（fail-fast 不静默降级）
6. prompt 填 `{{nope}}` 保存 → 400 带节点 key 与引用名（R10 双模板：system_prompt 同验）

### 13.2 场景 B（US2）：ragNode 先提炼后检索

1. 画布拖入「知识检索节点」：KB 下拉（仅 enabled）/ Top K / 检索问句 Query 表单齐备
2. `llm1（提炼 topic）→ kr（query={{llm1.topic}}）→ llm2` 试运行：kr 的 nodeIn query = 渲染后问句（运行详情节点 input 可见）、llm2 据检索段落作答
3. 存量无 query 的 kr 节点（JSON 模式造）试运行 → query 回退整段 input（升级前行为）
4. query 填 `{{nope}}` 保存 → R10 400；query 渲染空串 → 回退 input 不报错

### 13.3 场景 C（US3）：tool 节点画布闭环

1. 画布拖入「工具节点」：工具下拉按 enabled server 分组单选；选中后 config.tool_id 字符串保形（JSON 模式核对）
2. args 行 `q={{input}}` 保存试运行 → 工具收到渲染值；nodeIn args 同值
3. 空键 args 行不产出、全空删 args 键（JSON 模式核对）
4. 存量 JSON 形态 tool 节点进编辑页 → 表单回显工具名与 args 行如初

### 13.4 场景 D（US4）：零回归

1. chat 绑工具 Agent 多轮工具对话复跑（chat-manual-test §14 同款）→ 帧序 / 卡片 / 落库零差异（reactloop 换用 + ToolSourceReq 加法双证明）
2. 既存七类节点存量图各跑一次 → 行为零变化；palette 既存类拖拽 / 起始三入口 / 开始锚点 / end 映射复测
3. `make migrate-status` 22 applied；migrations/ 仅新增 00022

### 13.5 通过标准增补（SC-001~SC-005）

- [ ] react 全链（表单 / 并集 / 终答 / tool_chain / executions / 上限 / fail-fast / R10）（§13.1）
- [ ] ragNode 表单 / query 渲染与回退 / R10（§13.2）
- [ ] tool 表单 / 保形 / args 行 / 回显（§13.3）
- [ ] chat 零差异 + 存量零回归 + 22 applied（§13.4）
- [ ] 门禁：go 全量 + workflow/mcp 模块覆盖率 ≥80% + web type-check/build + grep 纪律（eino/go-sdk/依赖面）
