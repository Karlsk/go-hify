# chat 前端页面 spec

> 状态：**全部章节已定稿**（2026-09-04；布局 §1–§10、动态交互 §11、数据流 §12、API 层 §13、错误重试 §14、渲染 §15、前端手测 §16）。后端 API 事实源：[chat-manual-test.md](../testing/chat-manual-test.md)（5 端点 + 错误矩阵 + SSE 帧样例）。
> 后端契约事实源：[internal/chat/api/schema.go](../../../internal/chat/api/schema.go)、CLAUDE.md《对话接口》；前端约定事实源：[web/README.md](../../../web/README.md)、[docs/design/design-system.md](../../design/design-system.md)。

## 1. 背景与目标

chat 后端 v1（会话 CRUD + 上下文组装 + SSE 两模式 + executions 落库）已交付，本 spec 定义控制台对话页面的前端实现。参考图为 Claude Desktop 风格聊天界面：**左侧会话列表 + 右侧聊天窗口 + 底部固定输入框**；Element Plus 组件实现，视觉全部走 Hify 浅色主题既有 token。

交付节奏（用户定）：**布局先行**——先定稿本章页面结构与区域规格，SSE 客户端、状态管理、API 层、消息渲染等章节按此骨架逐个讨论补充；布局实现（编码批次）在布局章节确认后启动。

## 2. 与参考图的取舍

| 参考图元素 | Hify 取舍 | 理由 |
|---|---|---|
| 左栏 Cowork/Code 顶部切换 | **不做** | 单一对话形态，无模式概念 |
| 左栏项目分组（ collapsible groups） | **不做** | Hify 会话是扁平列表（conversations 表无分组维度） |
| 会话项圆形 bullet + 选中浅灰高亮 | **保留气质** | 扁平列表 + hover/选中浅灰圆角底 |
| 顶栏 branch / expand / run 图标组 | **不做** | 顶栏只留标题 + agent 名 + 删除会话 |
| assistant 文档式排版（无气泡、平铺正文） | **修订**（2026-09-04 随 §11 定稿）：assistant 改**浅色气泡**、user 气泡改**深色底**，双气泡深浅对照 | 团队习惯的聊天语义；气泡内容渲染 Markdown（§15 定稿） |
| 底部 Bypass permissions 黄色 pill / 模型选择器（glm-5.2 · Max） | **不做（v1）** | 工具行内容随 SSE 章节一起定 |
| `Type / for commands` 占位符与命令体系 | **不做** | 纯文本输入 |
| 每条消息的时间戳 / 收藏 meta 行 | **不做（v1）** | 会话列表项已有相对时间；消息级 meta 留渲染章节再议 |
| 参考图独立应用外壳 | **改造** | 页面落在 Hify 既有壳内（深色侧栏 + 顶栏保留），见 §4 |

## 3. 布局总图

```
┌────────┬──────────────┬────────────────────────────────────────────────────────────┐
│ 既有    │  会话列表      │  聊天窗口（flex-1，纵向三段）                                │
│ 导航壳  │  260px        │                                                            │
│ (深色   │              │ ┌────────────────────────────────────────────────────────┐ │
│ 侧栏    │ ┌──────────┐ │ │ 顶栏 48px：会话标题（首轮消息回填） · agent 名 · ⋯ · 删│ │
│ 220px)  │ │ ＋ 新建会话│ │ ├────────────────────────────────────────────────────────┤ │
│        │ └──────────┘ │ │                                                        │ │
│  模型   │ ● 会话A    5m│ │          消息滚动区（内容列居中，max-width 800px）        │ │
│  Agent │ ○ 会话B    2h│ │                                                        │ │
│ ▶对话   │ ○ 会话C…   1d│ │                      ┌────────────────────────┐          │ │
│  知识库 │              │ │                      │ 用户消息（右侧深色气泡）  │          │ │
│  …     │  hover 显示   │ │                      └────────────────────────┘          │ │
│        │  🗑 删除      │ │  ✳ assistant 消息：左侧浅色气泡（深浅对照，流式追加）      │ │
│        │              │ │     流式逐字追加 ▊                                     │ │
│        │  ↓ 触底滚动   │ │                                                        │ │
│        │    加载更多   │ │  ⋮ 历史消息向上加载（游标）/ 新消息自动滚底                │ │
│        │              │ ├────────────────────────────────────────────────────────┤ │
│        │              │ │ 底部固定输入区（同 800px 列宽）                           │ │
│        │              │ │ ╭────────────────────────────────────╮    ┌────────┐    │ │
│        │              │ │ │ 输入框（圆角 12px，textarea 自适应  │    │ ➤ 发送  │    │ │
│        │              │ │ │ 高度；Enter 发送 · Shift+Enter 换行） │    └────────┘    │ │
│        │              │ │ ╰────────────────────────────────────╯                  │ │
│        │              │ │ 工具行：状态信息（内容待 SSE 章节定）                       │ │
└────────┴──────────────┴─┴────────────────────────────────────────────────────────┴─┘
```

最左列是 App.vue 既有深色侧栏（220px，可折叠 64px），`/chat` 菜单项已存在——**壳不动**（除 §4.2 的 fullBleed 条件类）。对话页面本体 = 中 + 右两栏。

## 4. 页面结构与全高方案

### 4.1 高度链

```
.app（100vh）
└── el-container（纵向）
    ├── header 56px（面包屑 + 用户区，保留）
    └── el-main ── 剩余全高 ── ChatView（height: 100%）
```

- 消息区是页面唯一纵向滚动容器；输入区靠 flex 布局（`flex: 1` 消息区 + 输入区自然沉底）**恒在视口底部**，不用 `position: fixed`。
- ChatView 内部横向两栏：

```
ChatView（flex row，height 100%，bg --hf-bg-container）
├── ConversationSidebar   260px 固定宽（§5）
└── ChatPanel             flex-1（纵向三段）
    ├── 面板顶栏           48px（§6.1）
    ├── MessageList       flex-1，overflow-y: auto（§6.2）
    └── ChatInput         固定高度区（自适应增高，§6.3）
```

### 4.2 fullBleed：el-main 的满血方案

`el-main` 现有 `padding: var(--hf-space-6)`（24px）+ EP 默认 `overflow: auto`，直接塞 ChatView 会得到「带 24px 灰边的缩水聊天窗」并出现双滚动条。方案（ChatView.vue 占位注释预告的"未来全高布局"落地）：

- `RouteMeta` 新增 `fullBleed?: boolean`，`/chat` 路由标记 `meta.fullBleed = true`。
- App.vue 对 `.app__main` 按该 meta 加条件类（如 `app__main--flush`）：`padding: 0` + `overflow: hidden`。
- 壳的深色侧栏与 56px 顶栏**保留**——与全站一致，不做 bare 式沉浸（bare 是登录/注册专用语义）。
- 本条属后续编码批次改动点（router/index.ts + App.vue 各几行），spec 只锁定行为。

### 4.3 组件文件边界（仅规划，接口签名留数据流章节）

```
web/src/views/chat/ChatView.vue                  # 容器：两栏布局 + 面板顶栏 + 编排
web/src/components/chat/ConversationSidebar.vue  # 左栏：新建按钮 + 会话列表
web/src/components/chat/MessageList.vue          # 消息滚动区 + 消息项 + 空态
web/src/components/chat/ChatInput.vue            # 底部输入区
```

ChatView 占位页（PageHeader + el-card）届时整体替换。`components/chat/` 是新目录，命名对齐 `ProviderModelsDrawer` 先例（组件按域归组）。

## 5. 左栏：会话列表（ConversationSidebar）

| 项 | 规格 |
|---|---|
| 宽度 | 260px 固定（4px 基数；壳 220px + 本栏后，1440 屏聊天区余 ~960px） |
| 背景 / 分割 | `--hf-bg-container`；右缘 1px `--hf-border-2` |
| 头部 | padding `--hf-space-3`；「＋ 新建会话」`el-button` `type="primary"` 全宽（弹 dialog 选 agent 建会话——`CreateConversationReq.agent_id`，交互细节留 §12） |
| 列表 | `overflow-y: auto`，横向 padding `--hf-space-2` |
| 列表项 | 单行：标题（flex-1，truncate，`--hf-font-size-sm`）+ 相对时间（`--hf-font-size-xs`、`--hf-text-3`，如 5m / 2h / 1d，取 `updated_at`）；项高 40px、圆角 `--hf-radius-sm`、横向 padding `--hf-space-3` |
| 三态 | 默认文字 `--hf-text-2`；hover 背景 `--hf-bg-subtle`；**选中**背景 `--hf-bg-muted` + 文字 `--hf-text-1` |
| 删除 | hover 时相对时间位置替换为删除 icon-button（`--hf-text-3`，hover 变 `--hf-danger`）；点击走 useConfirm（先例：ProviderList） |
| 加载 | 触底加载下一页（游标 = 上页 `next_cursor`）；加载中列表尾部 spinner |
| 空态 | `el-empty` 简版「暂无会话」 |

列表数据为**扁平正序列表**（最新在前，后端 keyset：`updated_at DESC, id DESC`）；点击项切换右侧会话。

## 6. 右栏：聊天窗口（ChatPanel）

### 6.1 面板顶栏（48px）

- 左：会话标题（`--hf-font-size-md`、`--hf-font-weight-semibold`、`--hf-text-1`，truncate）+ agent 名 `el-tag size="small"`（ConversationSchema 只有 `agent_id`，agent 名由前端 agent 列表映射——数据来源留 §12）。
- 右：删除会话 icon-button（hover `--hf-danger`），点击 useConfirm 后删除并清空右侧。
- 底缘 1px `--hf-border-2`；≤992 时左端额外出现「会话列表」menu icon-button（§8）。

### 6.2 消息区（MessageList）

- 唯一纵向滚动容器（`flex: 1; overflow-y: auto`）；上下 padding `--hf-space-6`；**内容列居中 `max-width: 800px`**（两侧自适应留白）。
- **user 消息**：右对齐**深色气泡**——背景 `--hf-primary-500`、文字 `--hf-text-inverse`（白字，对比度 4.67:1 AA——tokens.css 主按钮同款组合）、圆角 `--hf-radius-md`、padding 10px `--hf-space-4`、最大宽度 80% 列宽。
- **assistant 气泡三态**（§11 定稿，状态切换时机见 §11.1）：**等待首字**——气泡内三点跳动加载动画（`--hf-text-3`）；**流式中**——正文逐字追加 + 尾部主色光标块 ▊（闪烁动效走既有 duration/ease token，`prefers-reduced-motion` 降级由 main.css 全局处理）；**错误**——正文区显示 `--hf-danger` 红字提示（替换动画/光标）。气泡本体：背景 `--hf-bg-subtle`、文字 `--hf-text-1`、`--hf-font-size-base`、行高 `--hf-leading-relaxed`（1.7）、圆角 `--hf-radius-md`、同 padding，最大宽度 100% 列宽。
- （2026-09-04 修订：原「assistant 文档式无气泡」改为浅色气泡、user 由浅紫底改深色底——随 §11 动态交互定稿调整，双气泡深浅对照。）
- 消息间距 `--hf-space-6`；user/assistant 交替时上方再加 `--hf-space-2` 分组呼吸感。
- 滚动行为：新消息 / delta 到达自动滚底（用户手动上滚时暂停跟滚——判定阈值留 §12）；历史向上翻页（`after_id` 游标，向上加载插入顶部）。
- 消息内容渲染：**assistant 气泡渲染 Markdown**（marked + DOMPurify，代码块等宽字体，§15）；**user 气泡永远纯文本**（white-space: pre-wrap，不走 HTML 渲染——用户输入不可信）。

### 6.3 底部输入区（ChatInput）

- 与消息区同 800px 列宽；上下 padding `--hf-space-4` / `--hf-space-6`（离视口底留呼吸）；**无分割线**（参考图靠留白衔接，输入框自身有边框）。
- 输入框：容器边框 `--hf-border-1`、圆角 `--hf-radius-lg`（12px，参考图大圆角气质）、白底、focus 主色边框（EP 主题映射）；textarea 自适应高度 1–8 行，超出内滚。
- 键盘：Enter 发送、Shift+Enter 换行；空内容（含纯空白）禁用发送。
- 发送按钮：输入框内**右下角** icon-button（参考图形态）；流式进行中**禁用**（§11 定稿：v1 不做「停止生成」按钮，中止走切换会话 / 离开页面的隐式 abort）；**输入框保持可编辑**（可预打下一条），仅发送动作被拦。
- 输入内容上限 32000 字符（对齐后端 `SendMessageReq` binding），超限截断 + 提示。

## 7. 空态

| 场景 | 表现 |
|---|---|
| 未选会话（ChatPanel 无 active） | `el-empty`「从左侧选择会话，或新建一个开始对话」+ 主按钮「新建会话」（与左栏动作同源） |
| 会话列表为空 | 左栏 `el-empty` 简版（§5） |
| 新会话零消息 | 消息区 `el-empty`「发送第一条消息开始对话」 |

## 8. 响应式

| 断点 | 行为 |
|---|---|
| >1200 | 标准三栏（壳 220px + 会话列表 260px + 聊天区） |
| ≤1200（isNarrow） | 壳侧栏自动折叠 64px（App.vue 既有行为）；对话页布局不变 |
| ≤992（isCompact） | 会话列表隐藏；面板顶栏出现 menu icon-button → `el-drawer`（direction ltr，size 280px）内嵌同一 `ConversationSidebar` 组件（复用非复制） |

断点唯一事实源 `BREAKPOINTS`（useBreakpoint.ts），不引入新阈值。

## 9. token 映射表（零新增）

布局全部引用既有 token，**不新增、不改值、不硬编码色值**（token 冻结治理）：

| token | 用途 |
|---|---|
| `--hf-bg-container` | 会话列表 / 聊天窗口底色（整页白） |
| `--hf-bg-subtle` / `--hf-bg-muted` | 列表项 hover / 选中底；assistant 气泡底（subtle）/ Markdown 代码块底（muted，§15） |
| `--hf-primary-500` / `--hf-text-inverse` | user 气泡深底 + 白字（AA 对比组合） |
| `--hf-primary`（500） | 流式光标块 / focus 边框（EP 映射） |
| `--hf-border-1` / `--hf-border-2` | 输入框边框 / 各区分割线 |
| `--hf-text-1` / `--hf-text-2` / `--hf-text-3` | 主文 / 次文 / 时间与弱化 |
| `--hf-danger`（hover 语义） | 删除 icon hover；AI 气泡错误提示文字 |
| `--hf-radius-sm` / `--hf-radius-md` / `--hf-radius-lg` | 列表项 / 气泡 / 输入框 |
| `--hf-font-size-xs` / `sm` / `base` / `md` | 时间 / 列表项 / 正文 / 标题 |
| `--hf-leading-relaxed` | assistant 正文行高 |
| `--hf-font-mono` | Markdown 代码块 / 行内代码（§15） |
| `--hf-text-link` | Markdown 链接（§15） |
| `--hf-space-2` ~ `--hf-space-6` | 各区内边距与消息间距 |
| `--hf-duration-*` / `--hf-ease-*` | hover / 光标闪烁过渡 |

尺寸常量（非 token，组件内注释说明）：会话列表宽 260px、面板顶栏 48px、内容列 800px、列表项高 40px、textarea 上限 8 行——均为一次性布局值，不具主题语义，不进 tokens.css。

## 10. 布局章节实现清单（后续编码批次）

- [x] RouteMeta `fullBleed` + App.vue 条件类（§4.2）
- [x] ChatView 重写为两栏容器 + 面板顶栏（§4 / §6.1）
- [x] ConversationSidebar / MessageList / ChatInput 三组件骨架（§5 / §6.2 / §6.3）
- [x] 空态与 ≤992 drawer（§7 / §8）
- [x] `npm run type-check` + `npm run build` 通过（浏览器走查见 §16）

---

## 11. 动态交互与 SSE 客户端（2026-09-04 定稿）

> 依据：用户提供的发送消息时序图（浏览器 / 网络 SSE / 后端三泳道）+ 文字时间线。定义「页面怎么动」。

### 11.1 发送消息完整时间线

| 步 | 时刻 | 动作 |
|---|---|---|
| ① | 点击发送 / Enter | 校验非空 → **输入框立刻清空**（乐观 UI）、**发送按钮禁用**（输入框保持可编辑，可预打下一条） |
| ② | 同帧 | 消息区底部出现**用户气泡**（靠右、深色底，§6.2）——本地乐观插入，不等落库 |
| ③ | 同帧 | 紧接出现 **AI 气泡**（靠左、浅色底），内容区为空，显示**三点跳动加载动画** |
| ④ | 随即 | 经 `utils/sse.ts` 的 `startSSE` 发起 **fetch POST**（不用 EventSource）：`/api/v1/conversations/{id}/messages`，body `{content, stream: true}`，`credentials: include` |
| ⑤ | 后端 | `sendMessage`：落库用户消息 → 组装上下文 → LLM 调用（前端无感；心跳 `: ping` 由 startSSE 跳过） |
| ⑥ | 流中 | SSE `data:` 帧逐块返回 delta：**首个 delta 到达即加载动画让位于正文**，逐字追加 + 尾部光标 ▊；每帧追加后**自动滚底**（用户手动上滚则暂停跟滚，判定阈值 §12） |
| ⑦ | 流末 | `done` 事件（`message_id` + `usage` + `finish_reason`）→ 光标移除、气泡定稿、**发送按钮恢复可用** |
| ⑧ | 异常 | 两个入口收敛为同一 UI——**AI 气泡正文区 `--hf-danger` 红色错误提示**（替换动画/光标/正文），发送按钮恢复：a) **请求失败**（HTTP 非 2xx，`startSSE` throw；文案取响应信封 `error.message`，如 `BUDGET_EXHAUSTED` / `PROVIDER_BUSY`）；b) **流内 error 事件**（`{code, message, retryable}`——流已 200 开始，状态码不可改） |

细化约定：

- **乐观 user 气泡**：请求失败时本地 user 气泡保留（刷新后以服务端为准）；「重新生成 / 重发」语义留 §14。
- **动画与光标的切换点**：加载动画在**首个 delta** 消失（逐字追加开始后动画与正文不可并存）；done 移除的是流式光标——比「动画持续到 done」更符合直觉。
- `tool_call` / `tool_result` 事件：后端 v1 预留、不会触发；前端收到即忽略（渲染形态留 §15 后续）。

### 11.2 发送状态机（ChatView 内聚）

```
idle ──send()──▶ streaming ──done 事件──▶ idle
                     │
                     ├──error 事件 / HTTP 失败 / 流中断──▶ idle（AI 气泡保留错误态）
                     └──切换会话 / 组件卸载──▶ abort() ──▶ idle（保留已收到的部分正文）
```

- `streaming` 期间发送动作被拦（防重入），Enter 不再发起。
- **AbortController**：每次发送持有一个；切换会话、离开 /chat、页面卸载时 abort——对齐 CLAUDE.md「客户端断连取消上游 LLM 调用（省 token）」。
- **流中断**（连接断 / 未收到 done 即 EOF）：按 ⑧ 错误入口处理，已收到的部分正文保留在气泡内（定稿策略 §14）。

### 11.3 SSE 帧消费细节

- 后端协议是 **`data:` 帧 + 帧内 `type` 判别**（不用 `event:` 命名行）→ `startSSE` 的 `onEvent(event, data)` 回调里 `JSON.parse(data)` 后按 `type` 分支（`event` 参数恒为默认值 `message`，仅作协议兼容）。
- 五类 `type` 字段对齐 [schema.go](../../../internal/chat/api/schema.go) 的 `StreamEvent`：`delta`(content) / `done`(message_id, usage, finish_reason) / `error`(code, message, retryable) / `tool_call`(id, tool, args) / `tool_result`(id, tool, result)。
- `startSSE` 现有实现已满足 POST + cookie + 逐行解析 + 心跳跳过；缺口一项：**HTTP 非 2xx 时解析信封 `error.message` 再 throw**（当前只抛状态码，编码批次补）。

### 11.4 实现要点清单（并入编码批次）

- [x] ChatView 发送编排：清空 + 禁用 + 乐观双气泡 + `startSSE` + 事件分支（§11.1；编排落在 `composables/useChat.ts`，ChatView 消费）
- [x] MessageList 三态：加载动画（三点跳动）/ 流式光标 / 错误态
- [x] AbortController 生命周期（切换会话 / 组件卸载 / beforeunload）
- [x] `startSSE` 增强：非 2xx 时读信封错误文案（附带 401 跳登录 + `SSEError` 携带 code，供重发按钮判定）
- [x] 自动滚底跟随（含「用户上滚暂停」判定，阈值 80px §12.5）

## 12. 数据流与状态管理（2026-09-04 定稿）

### 12.1 状态清单（页面级，不进 Pinia）

chat 状态是页面私有态，不建全局 store（对齐「一人维护、页面态不进全局」；现 Pinia 仅 auth）。编排逻辑抽 `composables/useChat.ts`，ChatView 消费：

| 状态 | 类型 | 说明 |
|---|---|---|
| conversations | `ConversationItem[]` + `nextCursor` + `hasMore` + `listLoading` | 左栏数据；游标触底加载（§5） |
| activeId | `string \| null` | 当前会话；null → 右侧空态（§7） |
| messages | `ChatMessage[]`（正序） | 服务端行 + 本地乐观行（12.3）；流式状态挂在行上 |
| streaming | `boolean` + `AbortController \| null` | §11.2 状态机 |
| agentOptions | `AgentItem[]` | 进入页面一次加载（`page_size=100`，agent 极小表）；新建 dialog 数据源 + agent 名映射（`Map<agent_id, name>` computed 派生，顶栏 tag 共用） |

### 12.2 加载流

- **进入页面**：会话首页（`limit=20`）+ agent 列表并行加载；**不自动选中**任何会话——右侧空态引导（§7），与参考图「新建优先」心智一致。
- **点击会话**：若 streaming 中先 `abort()`（§11.2）→ 切 activeId → 加载消息。
- **消息加载策略**（受后端约束：历史查询只有**正序 after_id 游标**，无倒序 / before_id）：**循环翻页直至 `has_more=false`**（每页 `limit=100`），全量渲染后滚底；上限兜底 1000 条，超出截断并在顶部提示「仅显示最近 N 条」。内部工具会话量级（几十~几百条）内不触达；**后端补倒序端点列为演进项**（触达上限或会话普遍超长时提）。
- **发送结束**（done / error）：静默重拉会话首页——对齐 title 回填与 `updated_at` 排序（后端首轮消息后回填标题，手测 §4/§6 已验证）；不整页刷新、不清空当前消息区。

### 12.3 乐观追加与服务端对齐

- 发送（§11.1 ②③）：push 本地 user 行（`local: true`，临时 id）+ assistant 占位行（`state: 'loading'`）。
- delta：追加 assistant 行 `content`（`state: 'streaming'`）；done：写入 `message_id`、`state: 'done'`；error：`state: 'error'` + 错误文案。
- **对齐时机**：切换会话再切回 / 页面刷新 → 重新 GET messages，乐观行丢弃以服务端为准（用户消息在流开始前已落库，正常情况下服务端行与乐观行内容一致）。

### 12.4 会话操作

- **新建**：dialog 内 `el-select` 选 agent（选项 = agentOptions，显示名）→ `createConversation(Number(agentId))` → unshift 列表顶部并选中 → 右侧「发送第一条消息」空态。
- **删除**：useConfirm（§5）→ DELETE 204 → 列表移除；若删除的是 active 会话 → activeId 置 null、右侧回空态。
- **agent 被删/停用的存量会话**：列表仍显示；发消息时报 `AGENT_NOT_FOUND` / `AGENT_DISABLED`（§14.5），不做前置拦截（后端是事实源）。

### 12.5 自动滚底判定（钉 §11 遗留阈值）

- 判定「贴底」= 滚动容器 `scrollTop + clientHeight ≥ scrollHeight − 80px`；贴底时 delta 跟滚，用户上滚离开 80px 带宽后暂停跟滚。
- 「回到底部」浮动按钮：**v1 不做**（可选增强，留实现后按手感补）。

### 12.6 实现要点清单（并入编码批次）

- [x] `composables/useChat.ts`：状态 + 加载流 + 发送编排 + abort 生命周期
- [x] 消息循环翻页加载（limit=100 至 has_more=false，上限 1000）
- [x] done/error 后静默重拉会话首页（title 回填生效）
- [x] agent 名映射（agentOptions → computed Map）

## 13. API 层：api/chat.ts（2026-09-04 定稿）

### 13.1 前置：request.ts 补游标 helper

现有 `getList` 只解偏移 meta（total/page）；游标端点需要 `has_more` / `next_cursor`（`ResultMeta` 已含字段）。新增（对齐 `getList` 先例）：

```ts
// types/index.ts
export interface CursorResult<T> { list: T[]; hasMore: boolean; nextCursor: string }

// utils/request.ts
export async function getCursorList<T>(url: string, params?: PageQuery): Promise<CursorResult<T>>
```

### 13.2 类型（对齐 schema.go + 手测帧样例）

```ts
export interface ConversationItem { id: string; agent_id: string; title: string; created_at: string; updated_at: string }
export interface ToolCall { id: string; tool: string; args: Record<string, unknown> }
export interface MessageItem { id: string; role: 'user' | 'assistant' | 'tool'; content: string; tool_calls: ToolCall[]; created_at: string }
export interface Usage { input: number; output: number }
export interface AssistantReply { message_id: string; content: string; usage: Usage; finish_reason: string }
```

SSE `data:` 帧按 `type` 判别联合；注意 **`retryable` 无 omitempty，所有帧都携带该字段**（delta 帧也带 `retryable:false`，手测 §6 样例可见）——TS 类型如实定义，勿当 done/error 专属。

### 13.3 方法

| 方法 | 实现 | 备注 |
|---|---|---|
| `createConversation(agentId: number)` | `post<ConversationItem>('/conversations', { agent_id })` | body FK 数值（先例 provider）；URL id 字符串 |
| `getConversationList(params?: { limit?, cursor? })` | `getCursorList<ConversationItem>('/conversations', params)` | 触底加载回传 `cursor` |
| `getConversationMessages(id, params?: { after_id?, limit? })` | `getCursorList<MessageItem>(\`/conversations/${id}/messages\`, params)` | **回传参数名是 `after_id`**（值 = 上页 `next_cursor`，handler 已把 next_after_id 转成字符串游标） |
| `deleteConversation(id)` | `del<void>(\`/conversations/${id}\`)` | 204 无返回体 |
| `sendMessageOnce(id, data)` | `post<AssistantReply>(\`/conversations/${id}/messages\`, { content, stream: false })` | v1 页面不用（纯流式），API 完整性保留 |
| `sendMessageStream(id, content, callbacks, signal)` | 薄封装 `startSSE`：url `/api/v1/conversations/${id}/messages`、body `{ content, stream: true }`、onEvent 内 `JSON.parse` → 按 type 分发 | §11.3 |

### 13.4 SSE 路径与 401

- `startSSE` 不经 axios（无 baseURL）：路径写全 `/api/v1/...`，fetch 相对路径按页面 origin 解析——dev（:5173）走 Vite `/api` 代理、prod 走 nginx 同路径，两态一致。
- **401 盲区**：axios 拦截器的「清身份 + 跳登录」钩子覆盖不到 fetch 路径；`startSSE` 增强时同步处理——HTTP 401 时复用同款动态 import 逻辑（auth store + router 跳登录带 redirect）。列入 §14.6 清单。

## 14. 错误与重试（2026-09-04 定稿）

### 14.1 错误文案来源

- **流内 error 事件** / **emit 前信封错误**：优先显示后端 `error.message`——`failChat` 已给中文（「会话不存在」「供应商并发已满，请稍后重试」等，[handler.go](../../../internal/chat/handler/handler.go) failChat）；无 message（网络层断连）fallback「连接失败，请检查网络」。
- code 不在前端再做一份映射表（后端 message 即文案事实源）；`error.code` 只用于逻辑分支（14.2 / 14.5）。

### 14.2 retryable 语义（对齐 CLAUDE.md《对话接口》）

| retryable | 含义 | AI 气泡错误态 UI |
|---|---|---|
| `true`（首 token 前 429 / 超时 / 网络错误） | 重试可能成功 | 红字提示 + **「重新发送」按钮** |
| `false`（MODEL_CONTEXT_TOO_LONG / AUTH / AGENT_* 等） | 重试不会好 | 仅红字提示，引导修改输入或联系管理员 |

### 14.3 「重新发送」v1 语义

- 动作 = **将原 content 重新 POST 一轮**（新 user 消息 + 新 assistant 回复；失败轮的 user 消息与部分 assistant 内容留痕可查——后端 v1 无 regenerate 端点，不做「替换式重新生成」）。`done.message_id` 的反馈锚定等 regenerate 端点落地后再用。
- 后端演进项：`POST /conversations/:id/messages/:message_id/regenerate`（替换式重生成）。

### 14.4 流中断的部分内容

连接断 / 未收到 done 即 EOF：assistant 行保留已收到的部分正文 + 追加弱化标注「（连接中断）」（`--hf-text-3` 小字，非红字——内容可用）；发送按钮恢复。切换会话导致的主动 abort：内容保留、不加标注（用户主动行为）。

### 14.5 chat 相关错误码 → 前端表现（对齐手测 §11 矩阵）

| code | HTTP | 前端表现 |
|---|---|---|
| `UNAUTHORIZED` / `SESSION_EXPIRED` | 401 | SSE 路径走 13.4 的跳登录；axios 路径既有钩子 |
| `CONVERSATION_NOT_FOUND` | 404 | AI 气泡红字；会话列表同步重拉（可能已被他处删除） |
| `AGENT_NOT_FOUND` / `AGENT_DISABLED` | 404 / 503 | AI 气泡红字，无重发按钮 |
| `MODEL_CONTEXT_TOO_LONG` | 400 | AI 气泡红字，提示精简输入或新开会话 |
| `PROVIDER_BUSY` / `PROVIDER_UNAVAILABLE` | 503 | AI 气泡红字 + 重发按钮 |
| `RATE_LIMITED` / `BUDGET_EXHAUSTED` | 429 | AI 气泡红字；BUDGET 提示今日预算耗尽 |
| `VALIDATION_FAILED` | 400 | 输入侧拦截为主（非空 / 长度），命中时红字 |

### 14.6 实现要点清单（并入编码批次）

- [x] error 事件 / HTTP 失败统一进 AI 气泡错误态（含 message 取值链）
- [x] retryable → 「重新发送」按钮（重发原 content 一轮；HTTP 失败按状态码映射——503 可重试、429 仅 RATE_LIMITED 可重试、网络错误可重试）
- [x] 流中断部分内容 + 「（连接中断）」标注；主动 abort 不标注
- [x] `startSSE` 增强：非 2xx 读信封 message + **401 跳登录**（13.4）

## 15. 消息内容渲染（2026-09-04 定稿）

> 依据：用户定稿——AI 消息气泡的 content 用 marked 渲染成 HTML，代码块等宽字体，其他样式保持。

### 15.1 渲染边界

| 消息来源 | 渲染方式 | 理由 |
|---|---|---|
| assistant（含流式中） | **Markdown → HTML**（marked） | LLM 输出的自然格式（代码 / 列表 / 表格） |
| user | **纯文本**（white-space: pre-wrap），永不走 HTML 渲染 | 用户输入不可信，渲染成 HTML = 存储型 XSS 入口 |
| tool_call / tool_result | v1 不触发（后端预留）；后续如渲染，args / result 按 JSON 代码块处理 | |

### 15.2 依赖与安全（新增两个 npm 依赖）

- **marked**：`marked.parse(content, { gfm: true, breaks: true })`——GFM（表格 / 删除线 / 任务列表）+ 单换行即换行（聊天语义）。
- **DOMPurify**：`DOMPurify.sanitize(html)` 净化后才进 `v-html`。**marked 官方声明不做输出净化**，LLM 输出可能夹带工具结果 / 网页里的注入载荷——CLAUDE.md《安全》XSS 净化是硬性要求，非可选项；不影响视觉。链接经 afterSanitizeAttributes hook 补 `target="_blank" rel="noopener noreferrer"`。
- **流式渲染**：每个 delta 到达后对**当前气泡全文重渲染**（marked 同步解析、单条消息量级小，3-5 QPS 内部工具足够）；未闭合的代码围栏在流式中短暂显示为普通文本，收尾自愈——不做增量解析优化。
- 两者自带 TS 类型，无需 @types；版本取安装时稳定版。

### 15.3 样式（零新增 token）

`.md-content`（assistant 气泡内，scoped deep）：

| 元素 | 规格 |
|---|---|
| `pre > code` 代码块 | `--hf-font-mono`、背景 `--hf-bg-muted`、圆角 `--hf-radius-sm`、padding `--hf-space-3`、`overflow-x: auto`、字号 `--hf-font-size-sm` |
| 行内 `code` | `--hf-font-mono`、背景 `--hf-bg-muted`、padding 2px `--hf-space-1`、圆角 `--hf-radius-xs` |
| 其余元素（标题 / 列表 / 表格 / 引用） | 继承气泡既有排版（`--hf-text-1` / `--hf-leading-relaxed`），只做必要的间距归一，不额外上色——「其他样式保持」 |
| `a` | `--hf-text-link` |

### 15.4 实现要点清单（并入编码批次）

- [x] `npm i marked dompurify`
- [x] MessageList assistant 气泡：`mdRender(content)` 工具函数（`utils/markdown.ts`，marked → DOMPurify → v-html）+ `.md-content` 样式
- [x] 流式重渲染接入 §11.1 ⑥（delta 追加后；`v-memo` 钉 content/state，流式只重渲染当前行）
- [ ] 手测：代码块 / 表格 / 链接正常；`<script>`、`onerror` 注入样例被净化为纯文本显示（§16 走查项）

## 16. 前端手测清单（对齐后端 chat-manual-test.md 矩阵）

后端 5 端点已由 [chat-manual-test.md](../testing/chat-manual-test.md) curl 走查；前端在浏览器按同一矩阵过一遍（`make start` dev 热重载；前置数据同手测 §0–§3：真实 provider + model + agent）：

- [ ] **空态**：登录 → `/chat`：左栏列表 / 空态、右侧引导空态；不自动选中会话
- [ ] **新建会话**：dialog 选 agent → 列表顶部出现（title 为空）→ 自动选中、右侧「发送第一条消息」
- [ ] **流式对话**：Enter 发送 → 输入框清空、按钮禁用、user 深色气泡 + AI 气泡三点动画 → 首个 delta 起逐字追加 + 滚底 → done：光标消失、按钮恢复；Markdown 正常（代码块等宽 / 表格 / 链接）
- [ ] **标题回填**：done 后左栏标题从空变为首轮消息摘要（§12.2 静默重拉生效）
- [ ] **流中切换会话**：新会话历史加载正常、旧流中止（可对照后端日志 execution `error_class=Network`）
- [ ] **错误-Agent 停用**：停用 agent 后在其存量会话发消息 → AI 气泡红字「Agent 已停用」、无重发按钮（retryable=false）
- [ ] **错误-会话不存在**：一处删除会话后另一处再发 → 红字提示 + 列表重拉
- [ ] **错误-可重试**：制造 PROVIDER_BUSY（或断网）→ 红字 + 「重新发送」按钮，点击后新一轮完成
- [ ] **XSS 样例**：历史灌入 `<img src=x onerror=alert(1)>` → 渲染为纯文本、无脚本执行
- [ ] **401**：清 cookie 后发消息 → SSE fetch 路径触发跳登录（§13.4）
- [ ] **删除会话**：useConfirm → 列表移除；删除当前会话 → 右侧回空态
- [ ] **响应式**：窗口缩至 ≤992：会话列表收进 drawer，顶栏按钮可开合

---

## 变更记录

| 日期 | 变更 |
|---|---|
| 2026-09-04 | 首版：布局章节（§1–§10）定稿；§11–§15 占位骨架 |
| 2026-09-04 | §11 动态交互与 SSE 客户端定稿（发送时序图）；§2/§3/§6.2/§6.3/§9 同步修订——user 气泡改深色底（primary-500 + 白字）、assistant 由文档式无气泡改浅色气泡（bg-subtle） |
| 2026-09-04 | §15 消息内容渲染定稿：marked（GFM + breaks）+ DOMPurify（XSS 净化硬性要求）、代码块等宽 `--hf-font-mono`、其余样式保持；user 气泡永远纯文本 |
| 2026-09-04 | §12–§14 定稿（数据流 / API 层 / 错误重试，对齐 chat-manual-test.md 后端 API 面）+ 新增 §16 前端手测清单；过程中钉住两个事实——消息列表 meta 也是 `next_cursor`（handler 已转字符串）、SSE 走 fetch 不经 axios 是 401 盲区（须自处理跳登录） |
| 2026-09-04 | **编码批次落地**：基建层（`getCursorList`/`CursorResult`、sse.ts 增强、`utils/markdown.ts`、`api/chat.ts`）+ fullBleed 壳 + `useChat` + `components/chat/` 三组件 + ChatView 重写；type-check / build 通过；实现清单 §10 / §11.4 / §12.6 / §14.6 / §15.4 已勾，§16 浏览器走查待过。实现备注——发送编排收在 `composables/useChat.ts`（ChatView 消费）；`startSSE` 的 HTTP 失败抛 `SSEError`（带 status/code）供重发按钮判定；MessageList 用 `v-memo` 钉 content/state 防流式全列表 Markdown 重解析 |
| 2026-09-04 | **布局修订 v2（用户参考图，配色不动）**：左栏头部改「对话列表」标题 + 右侧紧凑「＋ 新建」、列表项两行（标题+时间 / agent 名预览——后端列表无 last_message 字段，消息预览列演进项）+ 选中主色竖条 + 中文相对时间；消息加 AI/我 32px 渐变圆头像、气泡贴合内容贴边分布（去 800px 居中列，max-width 封顶 AI 800 / user 640）、消息区顶部留白提至 space-10；输入区通栏 + 顶部分隔线 + 外置「发送」文字按钮；面板头 56px 只留标题（agent 名挪列表项第二行，删除入口收归左栏 hover）。AI 气泡仍 bg-subtle（参考图白底+边框属配色调整，按「配色不动」未采纳） |
