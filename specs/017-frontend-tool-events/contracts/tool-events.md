# Contracts: Chat 前端工具事件渲染

> 本篇无后端对外契约（零后端改动）。此处记录两类前端契约：
> ① 冻结上游的消费契约（SSE 帧 / 历史行——只消费不定义）；
> ② 本篇新引入的内部接口契约（api 回调、组件 props / emits）——tasks 与实现的对照锚点。

## 1 冻结上游消费契约（不改动，逐字对齐后端现状）

### SSE 帧（后端 StreamEvent，json tag）

| 帧 | 载荷 | 前端行为 |
|---|---|---|
| `tool_call` | `{type:'tool_call', id, tool, args}` | onToolCall：按 id 建 running 卡（tool / args 入卡） |
| `tool_result` | `{type:'tool_result', id, tool, result:{content, is_error}}` | onToolResult：按 id 置 result + isError → done；id 无匹配卡忽略 |

后端保证同一调用的 tool_call 帧先于 tool_result 帧；前端不做乱序兼容（孤儿结果帧忽略是边界行为非缺陷）。

### 历史行（messages 端点返回）

| 行 | 形态 | 前端行为 |
|---|---|---|
| assistant 中间行 | `content` 空 + `tool_calls[0] = {id, tool, args}` | 分组判定：供 args / 工具名 |
| tool 行 | 同构 id 锚点，含结果文本 | 分组配对：供 result |
| assistant 终答行 | 正文非空的 assistant 行 | 中间行 / tool 行归并目标 |

## 2 api 层契约（web/src/api/chat.ts）

```ts
// 工具结果帧载荷——对齐后端 ToolResultPayload（json tag）
interface ToolResultPayload {
  content: string
  is_error: boolean
}

// ToolResultFrame.result 类型收敛为 ToolResultPayload（原 unknown）

// 帧分发回调（全部可选——未传的调用点零改动）
interface StreamCallbacks {
  // ...既有 onDelta / onDone / onError / onCitations 不变...
  onToolCall?: (frame: ToolCallFrame) => void      // tool_call 帧
  onToolResult?: (frame: ToolResultFrame) => void  // tool_result 帧
}
```

switch 分发补 `tool_call` / `tool_result` 两 case，回调缺省时忽略（与既有 case 的缺省行为一致）。

## 3 组件契约（web/src/components/chat/ToolActivityCard.vue）

```ts
// props / emits 全 type-based
defineProps<{
  activity: ToolActivity   // 单卡数据（useChat 定义的类型，import type 引入）
}>()
// emits：无——纯展示组件，折叠/展开是组件本地 ref 态（默认折叠）
```

视觉约束（F5，零新增 token）：

| 用途 | token |
|---|---|
| 卡片底 / 展开区底 | `--hf-bg-muted` |
| 圆角 | `--hf-radius-sm` |
| 等宽字体（args JSON / result） | `--hf-font-mono` |
| 字号 | `--hf-font-size-sm` |
| 正常态辅助文本 / 弱化（含「（中断）」） | `--hf-text-3` |
| 错误态警示 | `--hf-danger` |

状态图标（`@element-plus/icons-vue` 具名 import，既有库零新增）：running=`Loading`（class `is-loading`）/ done=`CircleCheck` / done+isError=`Warning`（红态）/ interrupted=无图标（停动画，文本「（中断）」弱化）。

## 4 消费面（useChat 公开行为）

- `ChatMessage.toolActivities: ToolActivity[]` 默认 `[]`；未绑工具对话恒空数组（零卡片零回归的前提）。
- `MessageList.vue` v-memo 依赖数组追加 `msg.toolActivities`（现状五项 → 六项）。
- 历史 tool 行防御渲染分支保留（「分组异常兜底」），孤立行降级旧弱化渲染。
