# Data Model: Workflow 管道接线（chat-pipeline）

> Phase 1 产物。**零迁移**——本篇无新表新列新索引（19 条 applied 不变，下一号 00020 留给 spec 08）；本文件记录管道路径触碰的既有承载与引用关系，供 tasks / 实现对照。

## 实体关系（全部既有）

```text
agents (spec 05)                     conversations (既有)
│ workflow_id ──可空 FK RESTRICT──▶ workflows
│                                      ▲
│ chat 每轮加载 AgentDetailSchema      │ 触发轮
│ WorkflowID 判空 → 管道分支            │
└──────────────┐                       │
               ▼                       ▼
            messages（既有） ◀── user 行先落（Execute 前）+ assistant 行后落（终稿）
               ▲
               │ message_id 弱引用（执行时 assistant 尚不存在，锚定触发侧——O6）
               │
workflow_runs (00019 既有) ◀── conversation_id 弱引用（同上）
```

## 既有承载（本篇用法）

### workflow_runs（00019，零改动）

| 列 | 管道路径取值 | 说明 |
|---|---|---|
| `conversation_id` | 触发会话 id（非空） | chat 经 ExecuteWorkflowReq.ConversationID 回传；**非空即 trigger_source='chat'**（workflow 侧判定已实现） |
| `message_id` | 触发 user 消息 id（非空） | 同上回传；执行时 assistant 行尚不存在，锚定触发侧（O6 拍板） |
| `trigger_source` | `'chat'`（由 ConversationID != nil 判定） | CHECK 已含该值，零 DDL |
| `is_trial` | `false` | 管道恒正式执行（chat 无试运行语义） |
| `trace_id` | 请求 trace_id | httpmw.RequestID 注入 → ctx → Execute（串 slog 与两链） |

> 写入方是 workflow 模块（spec 06 收尾统一写，WithoutCancel 脱钩请求 ctx）——chat 只负责传参，不触碰该表。

### messages（既有，零改动）

| 列 | 管道路径取值 | 说明 |
|---|---|---|
| user 行 | role='user'、content=req.Content | Execute **之前**落库（位置语义同原路径：全部校验通过后落；Execute 失败 user 已落——与原路径 LLM 失败同款，不回滚不新设规则） |
| assistant 行 | role='assistant'、content=终稿、citations='[]' | persistAssistant 复用；usage 不入列（messages 本无 usage 列，用量在节点 executions） |

### agents（spec 05，零改动）

`workflow_id`（可空、FK RESTRICT）——绑定的 workflow 删除被挡（`WORKFLOW_IN_USE` 409），故管道路径 ErrWorkflowNotFound 仅脏数据防御态可达。chat 侧经 `AgentDetailSchema.WorkflowID *string`（字符串化）判空分叉。

## API 载荷（chat api 零改动，复用既有 schema）

| Schema | 管道路径取值 |
|---|---|
| `AssistantReplySchema` | Content=终稿 / Usage 全零（RunResultSchema 无用量，token 在节点 executions）/ FinishReason=`"workflow"`（新增终因值，前端分支用）/ Citations=`[]` / MessageID=assistant 行 id 字符串化 |
| `StreamEvent`（delta） | 单条整段 Content=终稿（唯一内容通道） |
| `StreamEvent`（done） | MessageID + Usage 全零 + FinishReason=`"workflow"`；不带 content（O5） |

## 校验规则（本篇新增的行为约束）

- `WorkflowID` 非数字字符串 → `errs.ErrInternal` 包装（脏数据防御，同 ModelID 既有处理 turn.go:210-213 形态）。
- 其余校验全部复用既有：会话属主 / agent 存在启用（setupConvAgent 段）；req.Content binding（SendMessageReq 既有）。
