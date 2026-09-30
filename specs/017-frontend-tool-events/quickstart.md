# Quickstart: Chat 前端工具事件渲染（spec 017）验证场景

> 纯前端篇：自动化验证 = 类型检查 + 构建门禁；行为验证 = 人工手测（用户执行，见末节）。
> 命令均在仓库根执行；手测前置 = 后端栈已起且有一个绑定 MCP 工具的 Agent。

## 前置

```bash
git status --short web/package.json   # 预期无改动（零新依赖验收点）
```

## 1. 自动化门禁（实现完成的定义）

```bash
cd web && npm run type-check && npm run build
```

预期：vue-tsc 零错误、构建成功（`✓ built in ...`）。既有 >500kB chunk 提示是历史噪音非失败。

## 2. 逐文件交付节奏（实现期每步过门禁）

```bash
cd web && npm run type-check   # 每完成一个交付物跑一次
```

顺序：api/chat.ts（类型修正 + switch 分发）→ composables/useChat.ts（状态编排 + 历史分组）→ components/chat/ToolActivityCard.vue（新组件）→ components/chat/MessageList.vue（卡片列 + v-memo）→ docs（手测小节 + README + changelog 指向）。

## 3. 三零边界核对

```bash
git status --short | grep -v -E '^(.. )?(specs/|docs/|web/src/|\.specify/)'
```

预期：无 Go 源文件 / migrations / go.mod / package.json / tokens.css 改动（零后端 / 零新依赖 / 零新 token）。

## 4. 关键回归点（手测覆盖，代码侧核对）

- 未绑工具 Agent：`toolActivities` 恒 `[]` → 零卡片、零卡片相关重渲染。
- v-memo 依赖数组 = 既有五项 + `msg.toolActivities`（六项）。
- 历史 tool 行防御分支保留（注释更新为「分组异常兜底」，分支不删）。

## 5. 人工手测（用户执行，六条场景）

后端栈：`make start`（dev）或 `make start ENV=prod`；浏览器打开控制台。

| # | 场景 | 断言 |
|---|---|---|
| 1 | 绑工具 Agent 对话触发一次工具调用 | 气泡顶部 running 卡（旋转图标 + 剥离前缀工具名 + 参数摘要）→ 工具返回转 done、结果首行进折叠头；展开可见 args 完整 JSON 与 result 全文；终答到达后卡片折叠、正文在下 |
| 2 | 同轮并行多工具（提示词诱导） | 多卡并列、各自独立 running→done、按到达序 |
| 3 | 工具返回错误（is_error） | 红警示图标 + 错误摘要、展开可见全文；对话不中断、后续轮正常 |
| 4 | 刷新 / 重进含工具轮的会话 | 中间行 + tool 行归并进终答气泡卡片（不占独立消息行）、args / result 完整可展开；无终答错误轮的卡片独立成尾部气泡 |
| 5 | 工具执行中主动停止 / 断网 | 残留 running 卡标「（中断）」弱化停动画、无伪造结果；主动 abort 同标中断 |
| 6 | 未绑工具 Agent 完整对话 + 刷新 | 零卡片；三态 / 中断标注 / citations / 错误重发等既有行为全部不变 |

手测脚本与预期细节以 `docs/testing/chat-manual-test.md` 新增小节为准（本篇交付物）。
