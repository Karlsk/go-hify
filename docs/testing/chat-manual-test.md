# Chat 对话引擎人工冒烟测试

验证对象：`internal/chat` 全链路（api → service → store → handler + 组合根接线 + platform/llm）。
全部为手工步骤，自动化覆盖见各包 `*_test.go`；实现决策见 `docs/changelog/chat/`。

- 接口前缀：`/api/v1/conversations`（5 端点：POST 建会话 / GET 列表 / GET 消息 / DELETE / POST 发消息）
- 响应信封：`{success, data, error, meta}`（`platform/respond`）；ID 一律字符串
- 列表分页：游标分页 `meta = {limit, has_more, next_cursor}`（conversations / messages 走 keyset）
- 发消息两模式：`stream:true`（SSE，缺省）/ `stream:false`（一次输出 JSON 信封）
- 本文档命令在**仓库根目录**执行

## 0. 前置条件

| 依赖 | 用途 | 检查命令 |
|---|---|---|
| Docker | PG / Redis 容器 | `docker version` |
| curl / jq | 接口调用与字段提取 | `curl --version && jq --version` |
| **LLM provider** | 发消息端点需要真实 provider（至少一个 OpenAI/Claude/Ollama） | 见 §3 |

## 1. 启动环境

复用 agent 模块手测的容器 + 迁移流程（如已启动可跳过）：

```bash
docker run -d --name hify-pg-test \
  -e POSTGRES_USER=hify -e POSTGRES_PASSWORD=hify -e POSTGRES_DB=hify \
  -p 5433:5432 pgvector/pgvector:pg17
docker run -d --name hify-redis-test -p 6379:6379 redis:7-alpine
until docker exec hify-pg-test pg_isready -U hify >/dev/null 2>&1; do sleep 1; done && echo "pg ready"
```

`.env` 核对：

```dotenv
SERVER_PORT=8081
PG_DSN=host=localhost user=hify password=hify dbname=hify port=5433 sslmode=disable
REDIS_ADDR=localhost:6379
```

```bash
make migrate-up && make migrate-status   # 预期 ≥8 条 applied
make start                               # 日志落 logs/hify.log
curl -s localhost:8081/health | jq .     # → {"success":true,"data":"Hify is running",...}
```

## 2. 登录

```bash
curl -s -X POST localhost:8081/api/v1/auth/register -H 'Content-Type: application/json' \
  -d '{"username":"chat-tester","password":"smoke-test-123"}' | jq .success     # → true
curl -s -c /tmp/hify-jar -X POST localhost:8081/api/v1/auth/login -H 'Content-Type: application/json' \
  -d '{"username":"chat-tester","password":"smoke-test-123"}' | jq .success     # → true
```

## 3. 造前置数据（provider + model + agent）

```bash
# provider（api_key 填真实 key 或 Ollama 留空）
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/providers -H 'Content-Type: application/json' \
  -d '{"name":"openai-main","kind":"openai","api_key":"sk-test-xxx"}' | jq .data.id   # → "1"

# model
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/models -H 'Content-Type: application/json' \
  -d '{"provider_id":1,"name":"GPT-4o","model_id":"gpt-4o","capability":"chat"}' | jq .data.id  # → "1"

# agent（system_prompt / temperature 用于后续对话验证）
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/agents -H 'Content-Type: application/json' \
  -d '{"name":"对话助手","model_id":1,"system_prompt":"你是 Hify 助手，简洁回答。","temperature":0.7}' \
  | jq .data.id   # → "1"
```

## 4. 创建会话（POST /conversations）

```bash
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/conversations -H 'Content-Type: application/json' \
  -d '{"agent_id":1}' | jq .
```

**预期** 201：`data.id` 非空字符串、`agent_id="1"`、`title=""`（首条消息后回填）、双时间戳。

```bash
# agent 不存在 → 404
curl -s -o /dev/null -w '%{http_code} ' -b /tmp/hify-jar -X POST localhost:8081/api/v1/conversations \
  -H 'Content-Type: application/json' -d '{"agent_id":999}'
# → 404 AGENT_NOT_FOUND

# agent 停用 → 503
curl -s -b /tmp/hify-jar -X PUT localhost:8081/api/v1/agents/1 -H 'Content-Type: application/json' \
  -d '{"name":"对话助手","model_id":1,"enabled":false}' > /dev/null
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/conversations -H 'Content-Type: application/json' \
  -d '{"agent_id":1}' | jq .error.code
# → "AGENT_DISABLED"  503
curl -s -b /tmp/hify-jar -X PUT localhost:8081/api/v1/agents/1 -H 'Content-Type: application/json' \
  -d '{"name":"对话助手","model_id":1,"enabled":true}' > /dev/null   # 恢复

# 缺 agent_id → 400
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/conversations \
  -H 'Content-Type: application/json' -d '{}' | jq .error.code
# → "VALIDATION_FAILED"
```

## 5. 会话列表（GET /conversations，keyset 游标）

```bash
# 首页
curl -s -b /tmp/hify-jar 'localhost:8081/api/v1/conversations?limit=20' | jq .
# → data[0] 是最新会话，meta.has_more / meta.next_cursor

# 翻页（next_cursor 原样回传）
NEXT=$(curl -s -b /tmp/hify-jar 'localhost:8081/api/v1/conversations?limit=1' | jq -r '.meta.next_cursor')
curl -s -b /tmp/hify-jar "localhost:8081/api/v1/conversations?limit=1&cursor=${NEXT}" | jq '.data[0].id'
```

## 6. 发消息——流式模式（POST /conversations/:id/messages, stream:true）

这是**核心链路**（SSE 流式对话），验证前确保 LLM provider 可用：

```bash
CONV_ID=1   # 替换为 §4 创建的会话 id

# 流式请求（fetch + ReadableStream 的 curl 等价写法）
curl -sN -b /tmp/hify-jar -X POST "localhost:8081/api/v1/conversations/${CONV_ID}/messages" \
  -H 'Content-Type: application/json' \
  -d '{"content":"你好，简单介绍一下自己","stream":true}'
```

**预期** SSE 输出（逐字流）：

```
data: {"type":"delta","content":"你好","retryable":false}

data: {"type":"delta","content":"！我是","retryable":false}

...

data: {"type":"done","message_id":"3","usage":{"input":42,"output":28},"finish_reason":"stop","retryable":false}
```

- 首帧之前可能有几秒延迟（TTFT），空闲 >15s 会收到 `: ping` 注释行
- `message_id` 非空（assistant 消息已落库）
- `usage.input/output` 非零（token 计数）

### 6.1 会话不存在 → 标准错误信封（非 SSE）

```bash
curl -s -b /tmp/hify-jar -X POST "localhost:8081/api/v1/conversations/999/messages" \
  -H 'Content-Type: application/json' -d '{"content":"hi","stream":true}' | jq .
# → 404 CONVERSATION_NOT_FOUND（emit 前失败，200 头未写，走标准信封）
```

### 6.2 软删 agent 的存量会话 → 404

```bash
# 建会话 → 软删 agent → 发消息
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/conversations -H 'Content-Type: application/json' \
  -d '{"agent_id":1}' | jq -r .data.id   # 假设得到 "2"
curl -s -b /tmp/hify-jar -X DELETE localhost:8081/api/v1/agents/1 > /dev/null
curl -s -b /tmp/hify-jar -X POST "localhost:8081/api/v1/conversations/2/messages" \
  -H 'Content-Type: application/json' -d '{"content":"hi","stream":true}' | jq .error.code
# → "AGENT_NOT_FOUND"  404
```

### 6.3 mid-stream error → SSE error 事件

模拟场景：超长输入导致 `MODEL_CONTEXT_TOO_LONG`（需要 agent 有 `max_context_turns` 限制，
灌满历史后发一条超长消息；或直接短 max_context_turns + 长消息）：

```bash
# 极端场景：发一条超长 content（接近 32000 字上限，context 溢出时触发）
LONG=$(python3 -c "print('这是一段很长的文本。' * 2000)")
curl -sN -b /tmp/hify-jar -X POST "localhost:8081/api/v1/conversations/1/messages" \
  -H 'Content-Type: application/json' \
  -d "{\"content\":\"${LONG}\",\"stream\":true}"
# 预期：delta 事件后出现 data: {"type":"error","code":"MODEL_CONTEXT_TOO_LONG",...,"retryable":false}
```

## 7. 发消息——一次输出模式（POST /conversations/:id/messages, stream:false）

```bash
curl -s -b /tmp/hify-jar -X POST "localhost:8081/api/v1/conversations/1/messages" \
  -H 'Content-Type: application/json' \
  -d '{"content":"1+1等于几？","stream":false}' | jq .
```

**预期** 200 信封：`data.content` 非空、`data.message_id` 非空、`data.usage` 有值。

## 8. 管道冒烟——绑定 workflow 的 Agent（spec 07 E1 形态）

绑定 workflow 的 agent，消息**确定性先过工作流**，终稿整段即本轮 assistant 回复
（agent 的 system prompt / RAG / 模型循环不参与本轮）。

完整步骤（环境启动 → workflow 前置 → 冒烟三步走 → psql 两链互溯）已统一至
[workflow-engine-manual-test.md](workflow-engine-manual-test.md)——该文档是 spec 07
人工验收的唯一入口，本文档不再重复维护管道冒烟细节。

## 9. 历史消息（GET /conversations/:id/messages）

```bash
# 首页（after_id=0 或不传）
curl -s -b /tmp/hify-jar "localhost:8081/api/v1/conversations/1/messages?limit=20" | jq .
# → data 包含 user + assistant 消息，正序（最旧在前）

# 翻页
NEXT_AFTER=$(curl -s -b /tmp/hify-jar "localhost:8081/api/v1/conversations/1/messages?limit=2" \
  | jq -r '.meta.next_cursor')
curl -s -b /tmp/hify-jar "localhost:8081/api/v1/conversations/1/messages?after_id=${NEXT_AFTER}&limit=20" | jq '.data[0].role'
```

字段校验：
- `role` 是 `"user"` / `"assistant"` / `"tool"` 之一
- `tool_calls` 为空数组 `[]`（非 null）
- `content` 非空（user/assistant）；assistant 断连时内容可能部分

```bash
# 会话不存在 → 404
curl -s -b /tmp/hify-jar "localhost:8081/api/v1/conversations/999/messages" | jq .error.code
# → "CONVERSATION_NOT_FOUND"
```

## 10. 删除会话（DELETE /conversations/:id）

```bash
curl -s -o /dev/null -w '%{http_code}\n' -b /tmp/hify-jar -X DELETE "localhost:8081/api/v1/conversations/1"
# → 204（无返回体）
curl -s -o /dev/null -w '%{http_code}\n' -b /tmp/hify-jar -X DELETE "localhost:8081/api/v1/conversations/1"
# → 404（重复删）
curl -s -o /dev/null -w '%{http_code}\n' -b /tmp/hify-jar -X DELETE "localhost:8081/api/v1/conversations/999"
# → 404
```

DB 校验：messages 经 FK CASCADE 级联删。

```bash
docker exec hify-pg-test psql -U hify -d hify -tc \
  "SELECT count(*) FROM messages WHERE conversation_id = 1;"
# → 0
```

## 11. Executions 表校验

每次 LLM 调用（含失败）应有一行 execution 记录：

```bash
docker exec hify-pg-test psql -U hify -d hify -c \
  "SELECT id, conversation_id, model_name, prompt_tokens, completion_tokens, error_class, finish_reason
   FROM executions ORDER BY id DESC LIMIT 5;"
```

**预期**：
- `model_name` = provider 的 model_id（如 `gpt-4o`）
- `prompt_tokens` / `completion_tokens` > 0（成功时）
- `error_class` 为 NULL（成功）或七类之一（Timeout/RateLimited/Overloaded/Network/InvalidRequest/Auth/ProviderDown）
- `finish_reason` = `stop`（正常结束）

## 12. 边界与错误矩阵

| 场景 | HTTP | error.code | 触发方式 |
|---|---|---|---|
| 未登录 | 401 | `UNAUTHORIZED` | 不带 cookie |
| 会话不存在 | 404 | `CONVERSATION_NOT_FOUND` | 发消息 / 查消息 / 删不存在的 id |
| Agent 不存在 | 404 | `AGENT_NOT_FOUND` | agent_id 指向不存在 / 软删后 |
| Agent 停用 | 503 | `AGENT_DISABLED` | agent.enabled=false |
| 模型上下文超长 | 400 | `MODEL_CONTEXT_TOO_LONG` | 超长 content + 历史溢出 |
| 供应商并发满 | 503 | `PROVIDER_BUSY` | bulkhead 16 槽占满 |
| 供应商限流 | 429 | `RATE_LIMITED` | 429 透传 |
| 供应商不可用 | 503 | `PROVIDER_UNAVAILABLE` | 熔断打开 / 连接拒绝 |
| 工作流未发布/停用 | 503 | `WORKFLOW_NOT_PUBLISHED` | 绑定 agent 的 workflow 撤回 draft（步骤见 [workflow-engine-manual-test.md](workflow-engine-manual-test.md) §5.2c）|
| 工作流图缺陷 | 400 | `VALIDATION_FAILED` | message 带 `node <key>:` 前缀透传（作者可行动）|
| 工作流执行失败 | 500 | `WORKFLOW_EXECUTION_FAILED` | api 节点 SSRF 拦截 / 总时长超 5min |
| 工作流不存在 | 404 | `WORKFLOW_NOT_FOUND` | 防御（被绑 workflow RESTRICT 挡删，理论不可达）|
| 模型不存在 | 404 | `MODEL_NOT_FOUND` | 防御（被 agents 引用的模型删除被 `MODEL_IN_USE` 挡，理论不可达）|
| content 缺失 | 400 | `VALIDATION_FAILED` | body 不含 content |
| agent_id 缺失 | 400 | `VALIDATION_FAILED` | body 不含 agent_id |

## 13. 日志观测点

| 观测点 | 位置 | 预期 |
|---|---|---|
| 对话请求 | `logs/hify.log` | `"msg":"http request"` 带 POST /api/v1/conversations/:id/messages |
| LLM 调用 | `logs/hify.log` | execution 落库 WARN（如 `chat: record execution failed`，不影响对话） |
| 标题回填 | `logs/hify.log` | `"chat: backfill title failed"`（WARN，仅当 store 写失败时） |
| 客户端断连 | `logs/hify.log` | 部分内容落库、execution error_class=Network |

## 14. 走查结论

（待手测后填写）
