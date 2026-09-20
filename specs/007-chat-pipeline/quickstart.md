# Quickstart: Workflow 管道接线（chat-pipeline）

> Phase 1 产物——验证指南（自动门禁 + 人工三步走）。契约细节见 [contracts/chat-pipeline.md](./contracts/chat-pipeline.md)，数据承载见 [data-model.md](./data-model.md)。前置：`make start ENV=dev`（本地）或 compose（prod）已起，登录拿到 session cookie。

## 1. 自动门禁（实现完成的定义）

```bash
# 全量门禁（须全绿）
go build ./... && go vet ./... && go test ./... -race -count=1

# 本模块覆盖率（须 ≥80%）
go test ./internal/chat/... -race -cover

# 依赖方向红线（两条都应无输出 / 仅白名单）
grep -rn "hify/internal/workflow" internal/chat/            # 仅 internal/chat/service 允许 import workflow/api
grep -rn "hify/internal/chat" internal/workflow/            # 期望零输出（反向依赖红线）
```

## 2. 人工三步走（§7 验收门人工项）

### ① 不绑 agent 冒烟——原路径行为无变化

1. 建一个**未绑 workflow** 的 agent（正常配模型），开新会话发消息。
2. 期望：流式逐字 delta + done（`finish_reason` 为模型原生终因，如 `stop`），行为与本篇合入前一致；一次输出模式信封正常。

### ② 绑定 agent 冒烟——管道接管

1. 建一个 published 的 workflow（最简：start → llm → end，llm 节点配好模型）。
2. 建一个**绑定该 workflow** 的 agent（模型可故意配坏 / 留默认——管道不解析模型，不应报错），开新会话发消息「查一下订单」。
3. 流式模式期望：SSE 收**单条整段 delta** + `done`（`usage` 全零、`finish_reason="workflow"`、无 citations 事件、等待期无 ping）。
4. 同会话切一次输出模式（`stream:false`）发第二条消息，期望：标准信封 reply，Content=终稿整段、`finish_reason:"workflow"`、`citations:[]`。
5. 负向：把 workflow 改回 draft（撤销发布）再发消息，期望 503 `WORKFLOW_NOT_PUBLISHED` 硬错误（不降级普通对话）。

### ③ 数据库验证——run 行引用回填（两链互溯）

```bash
psql "$DATABASE_URL" -c "SELECT id, workflow_id, trigger_source, is_trial, conversation_id, message_id, status, error_node, duration_ms FROM workflow_runs ORDER BY id DESC LIMIT 5;"
```

对 ② 的管道轮期望：`trigger_source='chat'`、`is_trial=false`、`conversation_id` / `message_id` 非空（= 触发会话与触发 user 消息）、`status='succeeded'`、`error_node=''`。

```bash
psql "$DATABASE_URL" -c "SELECT seq, node_key, node_type, status, duration_ms FROM workflow_node_runs WHERE run_id=<上面查到的 id> ORDER BY seq;"
```

期望：seq 连续升序、llm 节点 succeeded。

交叉验证（对话链 ↔ 执行链）：

```bash
psql "$DATABASE_URL" -c "SELECT id, role, content FROM messages WHERE conversation_id=<run 行 conversation_id> ORDER BY id;"
```

期望：触发 user 行 + assistant 行（content=终稿）；`messages.id` 与 run 行 `message_id` 对得上。

## 3. 已知边界（验证时不要误报 bug）

- 绑定 agent 的模型配置损坏**不影响**管道（管道跳过模型解析）——是设计行为不是缺陷。
- 16KB-32KB 的消息在管道内可用（execute 端点 HTTP 层 16KB 上限不适用于进程内调用）。
- 断连后重试会落**新的** user 消息（与原路径 LLM 失败同款，不回滚不新设规则）。
