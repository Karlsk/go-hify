# Data Model: Chat 前端工具事件渲染

> 纯前端篇：数据形态全部为前端内存态（Vue ref 内），零库表、零迁移、零后端 schema 改动。
> 后端契约（SSE 帧 / 历史行）为冻结上游，此处仅列出消费锚点，不定义。

## 实体

### ToolActivity（工具活动卡，新数据形态）

挂在 assistant 消息行上的单次工具调用展示单元。定义在 `web/src/composables/useChat.ts`。

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | `string` | 调用 id——流式帧配对（tool_call ↔ tool_result）与历史行配对（中间行 ↔ tool 行）的共同锚点 |
| `tool` | `string` | 完整调用名（含 `s<数字>__` 派生前缀，如 `s1__weather`）；展示层剥离前缀、完整名进 title |
| `args` | `Record<string, unknown>` | 参数对象；空为 `{}`（折叠头部摘要为空、展开态显示 `{}`） |
| `result?` | `string` | 结果文本（tool_result.content 或历史 tool 行结果）；running / interrupted 态无此字段——不伪造 |
| `isError?` | `boolean` | 错误标志（tool_result.is_error）；历史回放不落此字段（messages 行未存 is_error，后端 schema 冻结） |
| `state` | `'running' \| 'done' \| 'interrupted'` | 卡片状态机，见下 |

### ChatMessage（既有消息行，扩展）

`web/src/composables/useChat.ts` 既有接口增一字段，其余字段与既有乐观行 / 服务端行结构不变：

| 字段 | 类型 | 说明 |
|---|---|---|
| `toolActivities` | `ToolActivity[]` | 本消息行的工具卡清单（按到达序）；默认 `[]`（未绑工具对话恒空）——**不可变更新纪律：任何变化替换整个数组引用**（v-memo 身份判定的前提） |

## 状态机（ToolActivity.state）

```
                tool_call 帧到达（send() onToolCall）
                      │
                      ▼
                  ┌────────┐   tool_result 帧到达（onToolResult，id 匹配）
                  │running │ ─────────────────────────────────┐
                  └────────┘                                  ▼
                      │  流收尾：done / error / 被动中断 EOF / 主动 abort
                      │  （残留 running 统一置 interrupted）
                      ▼                                     done
                 ┌────────────┐                    （isError=true 为红警示态；
                 │interrupted │                     无匹配卡的结果帧忽略不建卡）
                 └────────────┘
```

- `running → done`：id 匹配的 tool_result 到达，置 `result` + `isError`。
- `running → interrupted`：流结束时仍 running（四种收尾路径统一）；弱化停动画、标「（中断）」、无 result 不伪造。
- `interrupted` / `done` 为终态，无后续迁移。
- 历史回放路径产出的卡直接为 `done` 态（不重演 running）；不标 `isError`（后端行未存）。

## 消费的上游契约（冻结，只读）

| 契约 | 形态 | 消费点 |
|---|---|---|
| SSE tool_call 帧 | `{type:'tool_call', id, tool, args}` | api/chat.ts switch → onToolCall → 建 running 卡 |
| SSE tool_result 帧 | `{type:'tool_result', id, tool, result:{content, is_error}}` | api/chat.ts switch → onToolResult → 按 id 置结果转 done |
| 历史 assistant 中间行 | `content` 空 + `tool_calls[0] = {id, tool, args}` | loadMessages 分组判定 + 供 args / 工具名 |
| 历史 tool 行 | 与中间行同构 id 锚点，含结果文本 | loadMessages 分组配对，供 result |

## 校验规则（from requirements）

- id 无匹配卡的结果帧：忽略不建卡不崩（FR-004 防御）。
- 中间行判定仅「content 空 + tool_calls 非空」；正文非空的行按普通消息行渲染（边界防御）。
- 分组产出行数 ≤ 原始行数；截断按原始行数计、保尾部最新（FR-005）。
- args JSON 与 result 文本以纯文本展示（等宽 / pre-wrap），不经 v-html（安全基线）。
