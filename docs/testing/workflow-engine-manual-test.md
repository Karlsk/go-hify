# Workflow 引擎管道手动冒烟测试（chat → workflow，spec 07）

验证对象：chat 管道接线全链路——**绑定 workflow 的 agent，消息确定性先过工作流，终稿即本轮
assistant 回复**（`internal/chat` 管道分支 + `internal/workflow` 执行引擎 + 组合根接线）。
全部为手工步骤，自动化覆盖见各包 `*_test.go`；实现决策见 `docs/changelog/workflow/`。

> 本文档是 spec 07（chat 管道接线）人工验收的**唯一入口**：环境启动 → 前置数据 →
> 冒烟三步走 → psql 两链互溯，一篇走完，不需要在 chat / workflow 两份手测文档间跳转。
> 完整的 chat 模块冒烟见 [chat-manual-test.md](chat-manual-test.md)；完整的 workflow
> 模块 CRUD / 执行端点冒烟见 [workflow-manual-test.md](workflow-manual-test.md)。

- 接口前缀：`/api/v1`（conversations / workflows / providers / models / agents，均受登录中间件保护）
- 响应信封：`{success, data, error, meta}`（`platform/respond`）；ID 一律字符串
- 本文档命令在**仓库根目录**执行

> **端口说明**：本机 `.env` 配置 `SERVER_PORT=8081`、PG `port=5433`（被另一项目占用）。
> 端口空出来后改回 `.env` 即可。本文档所有命令按 8081 写。

## 0. 前置条件

| 依赖 | 用途 | 检查命令 |
|---|---|---|
| Docker | 临时 PG / Redis 容器 | `docker version` |
| curl / jq | 接口调用与字段提取 | `curl --version && jq --version` |
| **LLM provider** | 工作流 llm 节点需要真实 provider（至少一个 OpenAI/Claude/Ollama） | 见 §3 |

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
make migrate-up && make migrate-status   # 预期 19 条全部 applied
make start                               # 日志落 logs/hify.log
curl -s localhost:8081/health | jq .     # → {"success":true,...}
```

## 2. 登录链（后续所有请求都要带 cookie）

```bash
curl -s -X POST localhost:8081/api/v1/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"username":"pipe-tester","password":"smoke-pipe-123"}' | jq .success     # → true

curl -s -c /tmp/hify-jar -X POST localhost:8081/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"pipe-tester","password":"smoke-pipe-123"}' | jq .success     # → true
```

## 3. 造前置数据（provider + model + 未绑 agent）

```bash
# provider（api_key 填真实 key 或 Ollama 留空——llm 节点要真调模型）
PROVIDER_ID=$(curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/providers \
  -H 'Content-Type: application/json' \
  -d '{"name":"pipe-provider","kind":"openai","api_key":"sk-test-xxx"}' | jq -r .data.id)

# model（workflow llm 节点引用）
MODEL_ID=$(curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/models \
  -H 'Content-Type: application/json' \
  -d "{\"provider_id\":${PROVIDER_ID},\"name\":\"GPT-4o\",\"model_id\":\"gpt-4o\",\"capability\":\"chat\"}" \
  | jq -r .data.id)

# 未绑 workflow 的 agent（§5.1 不绑冒烟用）
AGENT_ID=$(curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/agents \
  -H 'Content-Type: application/json' \
  -d "{\"name\":\"对话助手\",\"model_id\":${MODEL_ID},\"system_prompt\":\"你是 Hify 助手，简洁回答。\",\"temperature\":0.7}" \
  | jq -r .data.id)
```

## 4. 创建并发布 workflow（最简线性图 llm → end）

```bash
WF=$(curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/workflows \
  -H 'Content-Type: application/json' \
  -d "{
    \"name\": \"管道冒烟\",
    \"description\": \"spec 07 管道接线验证用\",
    \"start_node_key\": \"llm\",
    \"nodes\": [
      {\"key\":\"llm\",\"type\":\"llm\",\"name\":\"生成节点\",
       \"config\":{\"model_id\":\"${MODEL_ID}\",\"prompt\":\"用户消息：{{input}}\"}},
      {\"key\":\"end\",\"type\":\"end\",\"name\":\"结束\",\"config\":{}}
    ],
    \"edges\": [
      {\"source_node_key\":\"llm\",\"target_node_key\":\"end\"}
    ]
  }")
WF_ID=$(echo "$WF" | jq -r .data.id)
echo "$WF" | jq '.data.status, .data.id'   # → "draft"、非空字符串

# 发布（管道只认 published）
curl -s -b /tmp/hify-jar -X POST "localhost:8081/api/v1/workflows/${WF_ID}/publish" | jq .data.status
# → "published"
```

## 5. 冒烟三步走（spec 07 §7 人工项）

### 5.1 ① 不绑 agent 冒烟——原路径零变化

用 §3 的未绑 agent 开新会话发消息，确认管道合入后**原路径行为无变化**：

```bash
CONV_ID=$(curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/conversations \
  -H 'Content-Type: application/json' -d "{\"agent_id\":${AGENT_ID}}" | jq -r .data.id)

# 流式：逐字 delta + done（finish_reason 为模型原生终因，如 "stop"）
curl -sN -b /tmp/hify-jar -X POST "localhost:8081/api/v1/conversations/${CONV_ID}/messages" \
  -H 'Content-Type: application/json' \
  -d '{"content":"你好，简单介绍一下自己","stream":true}'

# 一次输出：标准信封正常（content 非空、usage 有值、finish_reason="stop"）
curl -s -b /tmp/hify-jar -X POST "localhost:8081/api/v1/conversations/${CONV_ID}/messages" \
  -H 'Content-Type: application/json' \
  -d '{"content":"1+1等于几？","stream":false}' | jq '.data.finish_reason, .data.usage'
```

**通过标准**：流式逐字出字、done 的 `usage.input/output` 非零、`finish_reason` 非
`"workflow"`——与管道合入前一致即通过。

### 5.2 ② 绑定 agent 冒烟——管道接管

建**绑定该 workflow** 的 agent（模型正常配置即可——「模型配坏不挡管道」由单测覆盖，
人工冒烟不强求），开会话发消息：

```bash
WF_AGENT_ID=$(curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/agents \
  -H 'Content-Type: application/json' \
  -d "{\"name\":\"管道客服\",\"model_id\":${MODEL_ID},\"workflow_id\":${WF_ID}}" | jq -r .data.id)

CONV_WF=$(curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/conversations \
  -H 'Content-Type: application/json' -d "{\"agent_id\":${WF_AGENT_ID}}" | jq -r .data.id)
```

**a) 流式模式**：

```bash
curl -sN -b /tmp/hify-jar -X POST "localhost:8081/api/v1/conversations/${CONV_WF}/messages" \
  -H 'Content-Type: application/json' \
  -d '{"content":"查一下订单","stream":true}'
```

**预期** SSE 恰好两帧（无 citations / tool_call 帧，等待期无 `: ping`）：

```
data: {"type":"delta","content":"<终稿整段>","retryable":false}

data: {"type":"done","message_id":"5","usage":{"input":0,"output":0},"finish_reason":"workflow","retryable":false}
```

断言点：单条整段 delta（唯一内容通道）、`usage` 全零、`finish_reason="workflow"`、
done 不带 content、`message_id` 非空（assistant 已落库）。

**b) 一次输出模式**（同会话发第二条）：

```bash
curl -s -b /tmp/hify-jar -X POST "localhost:8081/api/v1/conversations/${CONV_WF}/messages" \
  -H 'Content-Type: application/json' \
  -d '{"content":"再查一次","stream":false}' | jq .
```

**预期** 200 信封：`data.content` = 终稿整段、`data.usage` = `{input:0,output:0}`、
`data.finish_reason` = `"workflow"`、`data.citations` = `[]`。

**c) 负向——workflow 停用 → 503 硬错误（不降级普通对话）**：

```bash
curl -s -b /tmp/hify-jar -X POST "localhost:8081/api/v1/workflows/${WF_ID}/disable" > /dev/null   # 撤回为 draft

# 流式模式同样收标准错误信封（首 emit 前失败，非 SSE error 事件）
curl -s -b /tmp/hify-jar -X POST "localhost:8081/api/v1/conversations/${CONV_WF}/messages" \
  -H 'Content-Type: application/json' -d '{"content":"hi","stream":true}' | jq '.error.code'
# → "WORKFLOW_NOT_PUBLISHED"  503

curl -s -b /tmp/hify-jar -X POST "localhost:8081/api/v1/workflows/${WF_ID}/publish" > /dev/null   # 恢复发布
```

### 5.3 ③ 数据库验证——run 行引用回填（对话链 ↔ 执行链互溯）

```bash
docker exec hify-pg-test psql -U hify -d hify -c \
  "SELECT id, workflow_id, trigger_source, is_trial, conversation_id, message_id, status, error_node, duration_ms
   FROM workflow_runs ORDER BY id DESC LIMIT 5;"
```

**预期**（对 5.2 的管道轮）：
- `trigger_source` = `'chat'`（区别于 execute 端点的 `'console'`）
- `is_trial` = `false`（管道恒非试运行）
- `conversation_id` / `message_id` 非空（= 触发会话与触发 user 消息，两链互溯的锚点）
- `status` = `'succeeded'`、`error_node` 为空

```bash
# 节点轨迹：seq 连续升序、llm 节点 succeeded（run_id 换上行查到的 id）
docker exec hify-pg-test psql -U hify -d hify -c \
  "SELECT seq, node_key, node_type, status, duration_ms FROM workflow_node_runs WHERE run_id=<id> ORDER BY seq;"

# 交叉验证：触发 user 行 + assistant 行（content=终稿），messages.id 与 run 行 message_id 对得上
docker exec hify-pg-test psql -U hify -d hify -c \
  "SELECT id, role, content FROM messages WHERE conversation_id=<run 行 conversation_id> ORDER BY id;"
```

## 6. 已知边界（验证时不要误报 bug）

- 绑定 agent 的模型配置损坏**不影响**管道（管道跳过模型解析）——是设计行为不是缺陷。
- 16KB-32KB 的消息在管道内可用（execute 端点 HTTP 层 16KB 上限不适用于进程内调用）。
- 断连后重试会落**新的** user 消息（与原路径 LLM 失败同款，不回滚不新设规则）。
- 管道路径 chat 层不记 executions（workflow 内部 llm 节点自记，conversation_id 置空）——
  `executions` 表里查不到管道轮的 chat 侧行是正常的。

## 7. 走查结论

（待手测后填写）
