# Implementation Plan: Chat 前端工具事件渲染（tool_call / tool_result 折叠卡片 + 历史回放分组）

| | |
|---|---|
| **Branch** | `017-frontend-tool-events` |
| **Date** | 2026-09-30 |
| **Spec** | [specs/017-frontend-tool-events/spec.md](spec.md) |

**Input**:

- 上位 spec（冻结契约 F1–F5，2026-09-30 用户批准）：[docs/changelog/chat/tool_events_frontend_spec.md](../../../docs/changelog/chat/tool_events_frontend_spec.md)
- 前端页面约定（沿用）：[docs/changelog/chat/frontend_page_spec.md](../../../docs/changelog/chat/frontend_page_spec.md) §11.3（五类 SSE 帧）/ §12.2–12.3（消息加载与乐观双气泡）/ §14.4–14.5（中断与错误表现）/ §15.1（assistant markdown 渲染）
- 宪法：[.specify/memory/constitution.md](../../.specify/memory/constitution.md) v1.1.0（技术约束：Vue 3 + TS + Element Plus + Vite、SSE 渲染层手写、已发布设计 token 只增不改）

## Summary

后端 chat 工具循环批（`0d6293f`）已交付 tool_call / tool_result SSE 帧与历史中间行 / tool 行，前端当前对两帧走忽略分支——工具执行期是黑盒（只有三点等待动画），历史中间行 / tool 行只按防御性弱化文本渲染。本篇打开渲染：工具活动以折叠卡片收进所属 assistant 气泡顶部，流式与历史回放两路径同一形态。改动收敛在前端五处——api/chat.ts 类型修正与帧分发（F4）、useChat.ts 状态编排与历史分组（F2）、新组件 ToolActivityCard.vue（F1/F5）、MessageList.vue 气泡内渲染与 v-memo 追加（F1/F3）、手测文档新增小节；三零边界：零后端改动、零新依赖（package.json 不动）、零新增设计 token（tokens.css 不动）。SSE 帧契约与历史行结构为冻结上游，前端只消费不定义。

## Technical Context

| 项 | 值 |
|---|---|
| **Language / Version** | TypeScript 5.x + Vue 3（`<script setup>` + Composition API），Vite 构建 |
| **Primary Dependencies** | Element Plus + `@element-plus/icons-vue`（按文件具名 import）、`marked` / `dompurify`（既有，本篇不触碰）——全部既有，**零新增** |
| **Storage** | N/A（纯前端消费既有后端契约；PG / Redis / SSE 后端全冻结不动） |
| **Testing** | 无前端单测框架——自动化验收门 = `cd web && npm run type-check && npm run build` 全绿；人工手测六条按 `docs/testing/chat-manual-test.md` 新增小节（用户执行） |
| **Target Platform** | 浏览器 SPA（nginx 静态托管，dev 本地 Vite） |
| **Project Type** | web-app 前端增量篇（Vue SPA，仓内 `web/`） |
| **Performance Goals** | v-memo 行级渲染跳过：依赖数组追加 `msg.toolActivities`，卡片变化（不可变整数组替换）只触发受影响消息行重渲染；长会话滚动不受影响 |
| **Constraints** | 零后端改动（SSE 契约 / 历史端点 / messages schema 冻结只消费）；零新依赖（package.json 零改动）；零新增设计 token（tokens.css 零改动，只用既有语义 token）；卡片数据更新不可变纪律（整数组替换）；props / emits 全 type-based 声明 |
| **Scale / Scope** | 2 前端源文件改动（api/chat.ts、composables/useChat.ts）+ 1 新组件（ToolActivityCard.vue）+ 1 组件改动（MessageList.vue）+ 2 文档（chat-manual-test.md 新增小节、web/README.md 组件目录补行）+ 1 changelog 指向（chat_tool_loop_impl.md §4） |

## Constitution Check

**GATE**（design 前评估，Phase 1 后复评）——宪法 v1.1.0 七原则逐条：

| 原则 | 判定 | 依据 |
|---|---|---|
| I. Simplicity First（一人维护、砍到最小） | ✅ PASS | 复用既有模式（本地 ref 折叠先例 App.vue、`is-loading` 图标类、v-memo、不可变更新既有写法）；新组件单一职责（单卡渲染 + 本地折叠态）；三零边界（零后端 / 零依赖 / 零新 token）排除一切架构性新面 |
| II. 模块化单体单向依赖 | ✅ PASS | 纯前端改动，不涉后端模块依赖图；改动落在 web/src 既有分层（api → composables → components），方向不变 |
| III. platform/llm 单点 | ✅ PASS | 不涉 LLM 调用；SSE 帧只是后端既有输出，前端消费 |
| IV. SSE 链路完整性 | ✅ PASS | fetch + ReadableStream + cookie 鉴权链路（utils/sse.ts、startSSE）零改动，只补帧消费分支；三层超时 / 心跳 / 断连取消均为后端职责不在本篇 |
| V. 数据库纪律 | ✅ PASS | 零后端改动、零迁移；历史行结构为冻结上游契约 |
| VI. 可观测与成本护栏 | ✅ PASS | 不涉后端观测与预算护栏；前端不加任何遥测 |
| VII. 统一契约与安全基线 | ✅ PASS | 帧类型对齐后端 StreamEvent json tag 与 ToolResultPayload{content, is_error}（已核实）；markdown 渲染路径（marked + dompurify）不触碰，args JSON 以纯文本等宽展示不经 v-html；设计 token 只增不改（本篇零新增） |

## Project Structure

**Documentation**:

```
docs/
├── changelog/chat/tool_events_frontend_spec.md     # 上位 spec（冻结契约，本篇不改）
├── changelog/mcp/chat_tool_loop_impl.md            # §4 前端项指向本篇（文档同步）
├── testing/chat-manual-test.md                     # 新增「工具事件渲染」小节：六条手测场景
web/
└── README.md                                       # 组件目录补 components/chat/ 分组四行（见下）
```

**Source Code**（全部改动文件）:

```
web/src/
├── api/
│   └── chat.ts                                      # F4：ToolResultFrame.result 收敛为 ToolResultPayload{content,is_error}；
│                                                   #     StreamCallbacks 增可选 onToolCall/onToolResult；
│                                                   #     帧分发 switch 补 tool_call/tool_result 两 case；
│                                                   #     删「后端 v1 预留不触发」过期注释、修正 ToolCall 过时注释
├── composables/
│   └── useChat.ts                                   # F2：ToolActivity 类型 + ChatMessage.toolActivities 字段；
│                                                   #     send() onToolCall 建 running 卡 / onToolResult 按 id 置 result+isError→done；
│                                                   #     流收尾（done/error/被动中断/主动 abort）统一把残留 running 卡置 interrupted；
│                                                   #     loadMessages 历史分组（中间行+tool 行 id 配对归并终答行 / 错误轮独立尾部气泡 /
│                                                   #     MESSAGE_MAX 按原始行数截断）；全部不可变整数组替换
└── components/chat/
    ├── ToolActivityCard.vue                         # 新组件（F1/F5）：单卡折叠/展开（本地 ref、默认折叠）、
    │                                                 # 状态图标（Loading is-loading / CircleCheck / Warning 红态 / interrupted 停动画标「（中断）」）、
    │                                                 # 显示名剥离 s<数字>__ 前缀（完整名进 title）、
    │                                                 # 折叠头部参数摘要 + 结果首行摘要、展开态 args JSON 等宽 pretty + result 全文限高滚动；
    │                                                 # 样式只用 --hf-bg-muted/--hf-radius-sm/--hf-font-mono/--hf-font-size-sm/--hf-text-3/--hf-danger
    └── MessageList.vue                               # F1/F3：assistant 气泡顶部（三分支链之前）渲染卡片列 v-for；
                                                      #     v-memo 依赖数组追加 msg.toolActivities；
                                                      #     tool 历史行防御分支注释更新为「分组异常兜底」（不删）
```

**Documentation（同步项，非交付主体）**:

```
web/README.md                                        # 组件目录补 components/chat/ 分组（ToolActivityCard 新增；ChatInput/
                                                     # ConversationSidebar/MessageList 按现状目录形态补齐落位——README 树当前缺整个 chat/ 子组）
docs/testing/chat-manual-test.md                     # 新增「工具事件渲染」小节：上位 spec §7 六条手测场景照搬
docs/changelog/mcp/chat_tool_loop_impl.md            # §4 前端工具事件项指向本篇（spec §8 文档同步）
```

**Structure Decision**: 全部改动落在前端既有三层（api 契约消费 → composables 状态编排 → components 渲染），不新增目录、不新增抽象层；ToolActivityCard.vue 按「多个小文件」原则从 MessageList.vue 拆出（单卡职责，MessageList 只做卡片列编排）。

## Complexity Tracking

全 PASS，无 violation 需登记。
