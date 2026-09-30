# Chat 前端 spec：工具事件渲染（tool_call / tool_result 帧 + 历史回放）

> 状态：**spec 定稿（2026-09-30 用户批准）**——待实施（排在 workflow spec 016 之后）
> 上位契约：[frontend_page_spec.md](./frontend_page_spec.md)（§11 数据流 / §12 消息加载 / §14 错误与重试 / §15 渲染——本篇为其增量篇，布局 / token / 交互约定沿用不重复）、CLAUDE.md《对话接口》SSE 事件表、[web/README.md](../../../web/README.md)（token 纪律 / v-memo / SSE fetch 约定）、[chat_tool_loop_spec.md](../mcp/chat_tool_loop_spec.md)（工具循环事件语义）。
> 前置依赖：chat 工具循环批已合入（`0d6293f`——后端已发 `tool_call` / `tool_result` 帧、messages 已落 assistant 中间行与 tool 行）。
> 后端与迁移：**零改动**（纯前端消费；SSE 契约、历史端点、messages schema 全部不动）。

## 1. 背景

后端 chat 工具循环已产出 `tool_call` / `tool_result` 两类 SSE 帧（绑定 MCP 工具的 Agent 对话），但前端 `sendMessageStream` 对这两帧是忽略分支，注释「后端 v1 预留不触发」已过期。用户侧体验是黑盒：工具执行期间无 delta，只能看到三点等待动画；历史端点返回的 assistant 中间行（content 空 + tool_calls）与 tool 行也只按防御性弱化文本渲染。本篇打开渲染：工具活动以**折叠卡片**收进所属 assistant 气泡，流式与历史回放两路径同一形态。

## 2. 做什么（范围）

| # | 交付物 | 文件 | 要点 |
|---|---|---|---|
| 1 | 类型修正 + 帧分发补齐 | `web/src/api/chat.ts` | `ToolResultFrame.result` 收敛为 `ToolResultPayload`；`StreamCallbacks` 增可选 `onToolCall` / `onToolResult`；switch 补两 case + 删过期注释 |
| 2 | 状态编排 + 历史分组 | `web/src/composables/useChat.ts` | `ChatMessage.toolActivities` 字段；流式回调；`loadMessages` 行分组（F2） |
| 3 | 工具卡片组件（新） | `web/src/components/chat/ToolActivityCard.vue` | 单卡折叠 / 展开 / 状态图标 / 显示名剥离（F1/F4） |
| 4 | 气泡内渲染 + v-memo | `web/src/components/chat/MessageList.vue` | 气泡顶部渲染卡片列；`v-memo` 增 `msg.toolActivities`；tool 行防御分支注释更新 |
| 5 | 手测文档增补 | `docs/testing/chat-manual-test.md` | 新增「工具事件渲染」小节（§7） |

零后端改动、零新依赖、零新增设计 token。

## 3. 不做什么（边界）

- **卡片无重试 / 重放按钮**：后端无重调端点（重生成语义属 §14.3 重新发送，作用于整轮）。
- **无耗时显示**：SSE 帧不带时间戳，前端不伪造。
- **历史回放不标 is_error**：messages 行未落 is_error（后端 schema 冻结，本篇零后端改动）；错误文本本身可读，回放期卡片一律正常态。补列属未来后端项。
- **workflow 进度事件**：另一遗留项，不混入。
- **一次输出模式（stream:false）UI 不变**：v1 页面纯流式。
- **工作流编辑器 tool 节点面板**：与后端 spec 016 边界一致，另篇。

## 4. 关键决策

### F1 渲染形态：assistant 气泡内的折叠工具卡

工具活动**不占独立消息行**，收进其所属 assistant 气泡顶部（content / 等待动画 / 错误文本之前），按到达序排列；一轮并行多调用即多卡并列。

- 卡片状态机：`running`（tool_call 到达：Loading 旋转图标 + 工具名 + 参数摘要）→ `done`（tool_result 到达：正常=图标转完成态 + 结果首行摘要；`is_error=true`=Warning 图标红态 + 错误首行摘要）。
- 折叠态 = 头部行（状态图标 + 显示名 + 参数键值摘要 + 展开箭头）；点击头部切换展开；展开态显示 args 完整 JSON（等宽 pretty）与 result 全文（等宽 pre-wrap、`max-height` 内部滚动）。默认全部折叠。
- **显示名剥离派生前缀**：`s<数字>__` 前缀（调用名派生规则）剥掉显示（`s1__weather` → `weather`），完整调用名进 `title` tooltip。
- **中断兜底**：流结束（done / error / abort / EOF）后仍处 `running` 的卡片标「（中断）」弱化并停止动画——无 result 不猜结果，与 assistant 行 `passiveInterrupt` 同哲学。

### F2 数据流：不可变更新保 v-memo 身份判定

- `ChatMessage` 增字段 `toolActivities: ToolActivity[]`：

```ts
interface ToolActivity {
  id: string          // 调用 id（tool_call / tool_result 配对锚点）
  tool: string        // 调用名（含派生前缀，展示层剥离）
  args: Record<string, unknown>
  result?: string     // tool_result 到达 / 历史回放填充
  isError?: boolean
  state: 'running' | 'done' | 'interrupted'
}
```

- **更新纪律**：任何卡片变化（新增 / 置结果 / 中断标注）都替换整个数组（新数组引用），配合 `v-memo` 依赖数组追加 `msg.toolActivities`——引用身份变化触发该行重渲染，其余行不动（沿用现有 v-memo 性能模式）。
- **流式路径**：`send()` 回调增 `onToolCall`（push 新卡）与 `onToolResult`（按 id 匹配置 result/isError、state→done）；流收尾（done / error / 被动中断 / 主动 abort 的 `settleAborted`）统一把残留 `running` 卡置 `interrupted`。
- **历史路径**：`loadMessages` 拉全行后**分组**再入 `messages`：
  - 连续的 assistant 中间行（content 空 + `tool_calls` 非空）与其后的 tool 行，归并进「其后首个 assistant 终答行」的 `toolActivities`（id 匹配配对：中间行供 args / 工具名，tool 行供 result）；
  - 后面没有 assistant 行的错误中断轮：工具卡独立成一个尾部 assistant 气泡（仅卡片无正文），保住失败轮留痕可查（§14 同哲学）；
  - 分组产出行数 ≤ 原始行数，`MESSAGE_MAX` 语义仍按原始行数截断（保尾部最新）。

### F3 组件拆分（小文件原则）

新组件 `ToolActivityCard.vue` 收单卡全部逻辑（折叠本地态、状态图标、显示名剥离、args / result 展示）；`MessageList.vue` 只做卡片列循环与位置。MessageList 的 tool 历史行防御分支保留（分组后正常不产出独立 tool 行；数据异常孤立行降级旧渲染），注释更新为「分组异常兜底」。

### F4 类型修正（api/chat.ts）

- `ToolResultFrame.result: unknown` → `ToolResultPayload { content: string; is_error: boolean }`（对齐后端 SSE 帧）。
- `ToolCall` interface 注释修正（后端已触发，不再是「仅为历史行完整性」）。
- `sendMessageStream` 顶部过期注释删除；switch 补 `tool_call` / `tool_result` 分发。

### F5 视觉（零新增 token）

- 卡片：`--hf-bg-muted` 底 + `--hf-radius-sm` + 内边距走既有间距 token；args / result 用 `--hf-font-mono` + `--hf-font-size-sm`。
- 状态色：正常态 `--hf-text-3` 辅助色；错误态 `--hf-danger`（既有语义 token，不加新值）。
- 图标：`@element-plus/icons-vue`（Loading 已在用；完成 / 警告用 CircleCheck / Warning）。

## 5. 任务清单（type-check / build 驱动，逐文件交付）

1. `api/chat.ts`：类型修正 + 分发补齐（F4）→ `npm run type-check` 过。
2. `useChat.ts`：`ToolActivity` 类型 + `toolActivities` 字段 + 流式回调 + 历史分组函数（F2）→ type-check 过。
3. `ToolActivityCard.vue` 新组件（F1/F5）。
4. `MessageList.vue`：卡片列渲染 + `v-memo` 追加依赖 + 防御分支注释更新 → `npm run type-check && npm run build` 过。
5. `docs/testing/chat-manual-test.md`：新增工具事件渲染小节。
6. 手测（§7 人工项）。

## 6. 验收门（前端适配模式）

- `cd web && npm run type-check && npm run build` 全绿；零新依赖（package.json 不动）。
- §2 交付物逐项存在性核对。
- 人工项（等用户过）：§7 手测清单。

## 7. 手测清单（docs/testing/chat-manual-test.md 新增小节）

1. 选绑定 MCP 工具的 Agent 对话提一个问题触发工具：卡片经历 running→done 状态流转，终答到达后卡片折叠、正文在下。
2. 同轮触发多工具（并行调用）：多卡并列，各自独立完成。
3. 工具返回错误（is_error）：卡片红色警示态、展开可见错误文本，对话不中断（后端喂回 LLM 自纠，后续轮正常）。
4. 刷新页面 / 重进会话：历史回放分组正确（卡片进对应 assistant 气泡、args / result 完整、终答正常渲染）。
5. 工具执行中主动停止 / 断网：残留 running 卡标「（中断）」，assistant 行既有中断语义不变。
6. 未绑工具的 Agent 对话：零卡片、现有行为零回归。

## 8. 文档同步

- [web/README.md](../../../web/README.md) 组件目录：补 `ToolActivityCard.vue` 一行（components/chat/ 分组或通用组件清单，按现状目录形态落位）。
- [chat_tool_loop_impl.md](../mcp/chat_tool_loop_impl.md) §4：前端工具事件渲染项指向本篇。

## 可提交节点

```
feat(chat-web): 工具事件渲染——tool_call/tool_result 折叠卡片 + 历史回放分组
```
