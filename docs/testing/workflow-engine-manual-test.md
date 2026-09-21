# Workflow 引擎手动冒烟测试（spec 07 + spec 08）

验证对象：
- **Spec 07**：chat 管道接线全链路——**绑定 workflow 的 agent，消息确定性先过工作流，终稿即本轮
  assistant 回复**（`internal/chat` 管道分支 + `internal/workflow` 执行引擎 + 组合根接线）
- **Spec 08**：workflow 分型与子工作流嵌套——**chat/task 两型 + sub-workflow 节点 + R11 嵌套校验 +
  parent_run_id 轨迹**

全部为手工步骤，自动化覆盖见各包 `*_test.go`；实现决策见 `docs/changelog/workflow/`。

> 本文档是 workflow 引擎人工验收的**唯一入口**：环境启动 → 前置数据 → spec 07 管道冒烟 →
> spec 08 嵌套冒烟 → psql 轨迹验证，一篇走完，不需要在多份手测文档间跳转。
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
make migrate-up && make migrate-status   # 预期 20 条全部 applied（00020 含内）
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
    \"type\": \"chat\",
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
echo "$WF" | jq '.data.status, .data.type, .data.id'   # → "draft"、"chat"、非空字符串

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

## 7. 嵌套冒烟（spec 08：chat/task 分型 + sub-workflow）

四组用例：两型创建 / 嵌套保存矩阵（R11）/ 嵌套执行 / psql 查 runs 树。
除 7.3 标注的子把关负向外全部为确定性图（无 llm 节点，不依赖真实 LLM）。

### 7.1 两型创建

```bash
# ⓪ 不带 type → 400（spec 08 起 Create 必填分型）
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/workflows \
  -H 'Content-Type: application/json' \
  -d '{"name":"no-type","start_node_key":"e","nodes":[{"key":"e","type":"end","config":{}}],"edges":[]}' \
  | jq '.error.code, .error.message'
# → "VALIDATION_FAILED"（400），message 含 "type 必填"

# ① task 型：声明 input_schema / output_schema（简化形态 [{name,type,required,description}]）
TASK=$(curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/workflows \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "echo-task",
    "type": "task",
    "input_schema": [{"name":"query","type":"string","required":true,"description":"查询词"}],
    "output_schema": [{"name":"answer","type":"string","required":true,"description":"回声"}],
    "start_node_key": "echo",
    "nodes": [
      {"key":"echo","type":"end","name":"回声",
       "config":{"output":"{\"answer\":\"echo:{{input.query}}\"}"}}
    ],
    "edges": []
  }')
echo "$TASK" | jq '.data.type, .data.input_schema, .data.status'
# → "task"、字段集 round-trip 一致、"draft"
TASK_ID=$(echo "$TASK" | jq -r .data.id)

# ② chat 型零 schema：§4 已建（type 露出见 §4 预期），不重复

# ③ Update 携带 type 即拒（同值也拒——分型不可变，换型 = 删了重建）
curl -s -b /tmp/hify-jar -X PUT "localhost:8081/api/v1/workflows/$TASK_ID" \
  -H 'Content-Type: application/json' \
  -d "$(jq -n --arg id "$TASK_ID" '{
    name:"echo-task", type:"task",
    input_schema:[{name:"query",type:"string",required:true,description:"查询词"}],
    start_node_key:"echo",
    nodes:[{key:"echo",type:"end",name:"回声",
      config:{output:"{\"answer\":\"echo:{{input.query}}\"}"}}],
    edges:[]}')" | jq '.error.code, .error.message'
# → "VALIDATION_FAILED"（400），message 含 "type 不可变"

# ④ chat 型携带非空 schema 拒（强不变量：schema 仅 task 型消费）
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/workflows \
  -H 'Content-Type: application/json' \
  -d '{"name":"chat-with-schema","type":"chat","input_schema":[{"name":"q","type":"string","required":true}],"start_node_key":"e","nodes":[{"key":"e","type":"end","config":{}}],"edges":[]}' \
  | jq '.error.code, .error.message'
# → "VALIDATION_FAILED"（400），message 含 "chat 型不支持 input_schema"
```

> 存量回填（迁移 00020 `DEFAULT 'chat'`）：新起测试库无迁移前存量，直接冒烟不可达——
> 真实升级库跑 `SELECT type, count(*) FROM workflows GROUP BY 1;` 应全为 chat；
> 空测试库可代验列默认：`docker exec hify-pg-test psql -U hify -d hify -c
> "SELECT column_default FROM information_schema.columns WHERE table_name='workflows' AND column_name='type';"`
> → `'chat'::text`。

### 7.2 嵌套保存矩阵（R11，全部 400 VALIDATION_FAILED）

```bash
# ① 引 chat 型拒（$WF_ID = §4 的 chat 型客服分流）
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/workflows -H 'Content-Type: application/json' \
  -d '{"name":"nest-bad-chat","type":"task","start_node_key":"sub","nodes":[{"key":"sub","type":"workflow","config":{"workflow_id":"'"$WF_ID"'","inputs":{"input":"{{input}}"}}},{"key":"end","type":"end","config":{}}],"edges":[{"source_node_key":"sub","target_node_key":"end"}]}' \
  | jq '.error.code, .error.message'
# → "VALIDATION_FAILED"，message 含 "为 chat 型（嵌套目标仅 task 型）"

# ② 引用不存在拒
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/workflows -H 'Content-Type: application/json' \
  -d '{"name":"nest-missing","type":"task","start_node_key":"sub","nodes":[{"key":"sub","type":"workflow","config":{"workflow_id":"99999","inputs":{"input":"{{input}}"}}},{"key":"end","type":"end","config":{}}],"edges":[{"source_node_key":"sub","target_node_key":"end"}]}' \
  | jq '.error.code, .error.message'
# → "VALIDATION_FAILED"，message 含 "引用的 workflow 99999 不存在"

# ③ 自嵌拒（PUT 图引用自身 id——创建时无 id，自嵌只能在编辑时发生）
curl -s -b /tmp/hify-jar -X PUT "localhost:8081/api/v1/workflows/$TASK_ID" \
  -H 'Content-Type: application/json' \
  -d '{"name":"echo-task","input_schema":[{"name":"query","type":"string","required":true,"description":"查询词"}],"start_node_key":"sub","nodes":[{"key":"sub","type":"workflow","config":{"workflow_id":"'"$TASK_ID"'","inputs":{"query":"{{input}}"}}},{"key":"end","type":"end","config":{}}],"edges":[{"source_node_key":"sub","target_node_key":"end"}]}' \
  | jq '.error.code, .error.message'
# → "VALIDATION_FAILED"，message 含 "自嵌禁止"

# ④ 缺 required 字段拒（echo-task 声明 required query，inputs 空映射）
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/workflows -H 'Content-Type: application/json' \
  -d '{"name":"nest-missing-field","type":"task","start_node_key":"sub","nodes":[{"key":"sub","type":"workflow","config":{"workflow_id":"'"$TASK_ID"'","inputs":{}}},{"key":"end","type":"end","config":{}}],"edges":[{"source_node_key":"sub","target_node_key":"end"}]}' \
  | jq '.error.code, .error.message'
# → "VALIDATION_FAILED"，message 含 "inputs 缺少 required 字段 [query]"

# ⑤ 多余字段拒（query 之外多带 extra）
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/workflows -H 'Content-Type: application/json' \
  -d '{"name":"nest-extra-field","type":"task","start_node_key":"sub","nodes":[{"key":"sub","type":"workflow","config":{"workflow_id":"'"$TASK_ID"'","inputs":{"query":"{{input}}","extra":"x"}}},{"key":"end","type":"end","config":{}}],"edges":[{"source_node_key":"sub","target_node_key":"end"}]}' \
  | jq '.error.code, .error.message'
# → "VALIDATION_FAILED"，message 含 "inputs 含未声明字段 [extra]"

# ⑥ 无 schema 回退恰 {input}（子图未声明 input_schema 时，键集必须恰为 [input]）
NOSCHEMA_ID=$(curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/workflows -H 'Content-Type: application/json' \
  -d '{"name":"nest-noschema-child","type":"task","start_node_key":"e","nodes":[{"key":"e","type":"end","config":{"output":"{{input}}"}}],"edges":[]}' | jq -r .data.id)
curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/workflows -H 'Content-Type: application/json' \
  -d '{"name":"nest-noschema-bad","type":"task","start_node_key":"sub","nodes":[{"key":"sub","type":"workflow","config":{"workflow_id":"'"$NOSCHEMA_ID"'","inputs":{"query":"{{input}}"}}},{"key":"end","type":"end","config":{}}],"edges":[{"source_node_key":"sub","target_node_key":"end"}]}' \
  | jq '.error.code, .error.message'
# → "VALIDATION_FAILED"，message 含 "键集必须恰为 [input]"
# （合法形态即 inputs {"input":"{{input}}"}——7.3 的父图对有 schema 子图用 {"query":...}，同理）

# ⑦ 链深超上限拒：顶层 + 2 层合法、+ 3 层拒（与执行期兜底同值）
#    链上三图同形：input_schema 声明 query，workflow 节点 inputs 用 {{input.query}} 下钻传递
LEAF_ID=$(curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/workflows -H 'Content-Type: application/json' \
  -d '{"name":"nest-leaf","type":"task","input_schema":[{"name":"query","type":"string","required":true}],"start_node_key":"e","nodes":[{"key":"e","type":"end","config":{"output":"leaf:{{input.query}}"}}],"edges":[]}' | jq -r .data.id)
MID2_ID=$(curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/workflows -H 'Content-Type: application/json' \
  -d '{"name":"nest-mid2","type":"task","input_schema":[{"name":"query","type":"string","required":true}],"start_node_key":"sub","nodes":[{"key":"sub","type":"workflow","config":{"workflow_id":"'"$LEAF_ID"'","inputs":{"query":"{{input.query}}"}}},{"key":"end","type":"end","config":{"output":"{{sub}}"}}],"edges":[{"source_node_key":"sub","target_node_key":"end"}]}' | jq -r .data.id)
MID1_ID=$(curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/workflows -H 'Content-Type: application/json' \
  -d '{"name":"nest-mid1","type":"task","input_schema":[{"name":"query","type":"string","required":true}],"start_node_key":"sub","nodes":[{"key":"sub","type":"workflow","config":{"workflow_id":"'"$MID2_ID"'","inputs":{"query":"{{input.query}}"}}},{"key":"end","type":"end","config":{"output":"{{sub}}"}}],"edges":[{"source_node_key":"sub","target_node_key":"end"}]}' | jq -r .data.id)
# nest-mid1 → mid2 → leaf = 顶层 + 2 层：保存成功（合法上限，留作 7.4 深链执行）

curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/workflows -H 'Content-Type: application/json' \
  -d '{"name":"nest-top","type":"task","start_node_key":"sub","nodes":[{"key":"sub","type":"workflow","config":{"workflow_id":"'"$MID1_ID"'","inputs":{"query":"{{input}}"}}},{"key":"end","type":"end","config":{}}],"edges":[{"source_node_key":"sub","target_node_key":"end"}]}' \
  | jq '.error.code, .error.message'
# → "VALIDATION_FAILED"，message 含 "引用链深度超上限"
```

> 间接环（A→B→A）在 ③ 自嵌 + ⑦ 链深的组合路径下同理被拦（DFS 链上出现自身 id 即拒），
> 手册不单独造双图互引用例——单测已覆盖（service 测试 A→B→A 拒）。

### 7.3 嵌套执行（确定性图，零 LLM 依赖）

```bash
# 父图（chat 型）：workflow 节点渲染 inputs → 子图执行 → 子终稿落父池（{{sub.answer}} 一级下钻）
PARENT=$(curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/workflows \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "嵌套-父图",
    "type": "chat",
    "start_node_key": "sub",
    "nodes": [
      {"key":"sub","type":"workflow","name":"调子任务",
       "config":{"workflow_id":"'"$TASK_ID"'","inputs":{"query":"{{input}}"}}},
      {"key":"end","type":"end","name":"结束","config":{"output":"{{sub.answer}}"}}
    ],
    "edges": [{"source_node_key":"sub","target_node_key":"end"}]
  }')
PARENT_ID=$(echo "$PARENT" | jq -r .data.id)
curl -s -b /tmp/hify-jar -X POST "localhost:8081/api/v1/workflows/$PARENT_ID/publish" >/dev/null
curl -s -b /tmp/hify-jar -X POST "localhost:8081/api/v1/workflows/$TASK_ID/publish" >/dev/null

curl -s -b /tmp/hify-jar -X POST "localhost:8081/api/v1/workflows/$PARENT_ID/execute" \
  -H 'Content-Type: application/json' -d '{"input":"你好"}' \
  | jq '.data.status, .data.output, (.data.node_trace | length), .data.run_id'
# → "succeeded"、"echo:你好"（子终稿 {"answer":"echo:你好"} 落池后下钻）、2（sub + end）、run_id 非空

# 子把关：正式执行子必须 published（draft 子 → 503 带父 node 前缀）
TASK2_ID=$(curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/workflows -H 'Content-Type: application/json' \
  -d '{"name":"echo-task-draft","type":"task","input_schema":[{"name":"query","type":"string","required":true}],"start_node_key":"echo","nodes":[{"key":"echo","type":"end","config":{"output":"{\"answer\":\"{{input.query}}\"}"}}],"edges":[]}' | jq -r .data.id)
PARENT2_ID=$(curl -s -b /tmp/hify-jar -X POST localhost:8081/api/v1/workflows -H 'Content-Type: application/json' \
  -d '{"name":"嵌套-父图-draft子","type":"chat","start_node_key":"sub","nodes":[{"key":"sub","type":"workflow","config":{"workflow_id":"'"$TASK2_ID"'","inputs":{"query":"{{input}}"}}},{"key":"end","type":"end","config":{}}],"edges":[{"source_node_key":"sub","target_node_key":"end"}]}' | jq -r .data.id)
curl -s -b /tmp/hify-jar -X POST "localhost:8081/api/v1/workflows/$PARENT2_ID/publish" >/dev/null

curl -s -b /tmp/hify-jar -X POST "localhost:8081/api/v1/workflows/$PARENT2_ID/execute" \
  -H 'Content-Type: application/json' -d '{"input":"hi"}' | jq '.error.code, .error.message'
# → "WORKFLOW_NOT_PUBLISHED"（503），message 以 "node sub:" 开头

# trial 跟随父：父试运行 → 子放开 draft（状态机唯一例外延伸到子图）
curl -s -b /tmp/hify-jar -X POST "localhost:8081/api/v1/workflows/$PARENT2_ID/execute?trial=true" \
  -H 'Content-Type: application/json' -d '{"input":"hi"}' | jq '.data.status'
# → "succeeded"
```

### 7.4 psql 查 runs 树（parent_run_id 关联 + 深链执行）

```bash
# 深链执行：nest-mid1 → mid2 → leaf（7.2 ⑦ 留下的合法上限链），一次执行 3 个 run 行
curl -s -b /tmp/hify-jar -X POST "localhost:8081/api/v1/workflows/$MID1_ID/publish" >/dev/null
curl -s -b /tmp/hify-jar -X POST "localhost:8081/api/v1/workflows/$MID2_ID/publish" >/dev/null
curl -s -b /tmp/hify-jar -X POST "localhost:8081/api/v1/workflows/$LEAF_ID/publish" >/dev/null
TOP_RUN_ID=$(curl -s -b /tmp/hify-jar -X POST "localhost:8081/api/v1/workflows/$MID1_ID/execute" \
  -H 'Content-Type: application/json' -d '{"input":"{\"query\":\"深链\"}"}' | jq -r .data.run_id)

# run 行全景：子行 trigger_source='workflow'、parent_run_id 逐层指向上层
docker exec hify-pg-test psql -U hify -d hify -c \
  "SELECT id, workflow_id, trigger_source, parent_run_id, status, is_trial FROM workflow_runs ORDER BY id DESC LIMIT 6;"
# 预期：深链 3 行（mid1 顶层 trigger_source='console' parent_run_id=NULL
#        + mid2 / leaf 各 1 行 trigger_source='workflow' parent_run_id=上层 id）
#       + 7.3 父图执行 2 行（父 console + 子 workflow）、draft 拒路径无子行

# 递归 CTE 还原整棵执行树
docker exec hify-pg-test psql -U hify -d hify -c "
WITH RECURSIVE tree AS (
  SELECT id, workflow_id, parent_run_id, status, 0 AS lvl
  FROM workflow_runs WHERE id = $TOP_RUN_ID
  UNION ALL
  SELECT r.id, r.workflow_id, r.parent_run_id, r.status, t.lvl + 1
  FROM workflow_runs r JOIN tree t ON r.parent_run_id = t.id
)
SELECT * FROM tree ORDER BY lvl;"
# 预期：3 行，lvl 0/1/2（mid1 → mid2 → leaf），parent_run_id 逐层指向上行 id

# 各 run 内 seq 顺序（每 run 的节点轨迹独立编号，从 1 严格递增）
docker exec hify-pg-test psql -U hify -d hify -c \
  "SELECT run_id, seq, node_key, node_type, status FROM workflow_node_runs
   WHERE run_id IN (SELECT id FROM workflow_runs WHERE parent_run_id = $TOP_RUN_ID OR id = $TOP_RUN_ID)
   ORDER BY run_id, seq;"
# 预期：每个 run 的 seq 从 1 起（子图 vars 池全新起步，节点编号不跨 run 连续）
```

## 8. 走查结论

（待手测后填写）
