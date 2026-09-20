# Workflow 系列遗留事项清单（递延项与已锁方向）

> 性质：**遗留清单**——记录 spec 07/08 规划讨论中「锁方向、缓实现」或显式递延的事项及其拍板上下文，供后续篇直接消费；每项落地后在本文勾销并指向对应 spec。
> 起始：2026-09-18。来源：spec 07（chat 管道接线）与 spec 08（分型与嵌套）的规划与契约冻结讨论。

## 1. memory 开关（管道带历史）——方向已锁，实现递延

- 拍板（2026-09-18，spec 07 O3）：管道先发「每轮独立」（input = 当前消息）；带历史与每轮独立由一个 memory 开关决定。
- **已锁方向**：上下文组装留在 chat/agent 流程——开 memory 时由 chat 把对话历史拼装进 input 字符串；Execute 恒为单轮纯函数，契约不动（spec 06 O1「单一入参终形」不重开）。
- 待拍：开关配置位归属（agent 列 vs workflow 列，涉及迁移）；历史拼装格式；默认值。
- 落点：后续篇纯增量（只改 chat 组 input 的那一行，零返工）。

## 2. 进度事件（onNodeDone → SSE）——spec 07 O7 显式不做

- 拍板（2026-09-18，spec 07 O7）：本篇不做；等待期呈现 = 前端既有 loading 态 + 惰性提交期无字节（与 LLM 首 token 等待同形态）。
- 障碍：onNodeDone 当前是 service 级字段（spec 06 FR9 留缝），无按请求注入路径；做进度事件需改 Execute 冻结契约（增按请求回调参数）+ SSE 新事件类型 + 节流设计。
- 触发时机：工具篇（工具循环天然需要过程反馈）或体验篇；按节点 SSE 流式输出与此同族（见 #8）。

## 3. 工具形态（task 型虚拟工具 Option 2）——已有专门备忘录

- 见 [docs/tools/workflow-task-tool-memo.md](../../tools/workflow-task-tool-memo.md)（工具名 slug 化 / 参数 schema `{input}` / 绑定=授权粒度到 workflow / 与 MCP 同注入链路 / Option 1 否决理由）。
- 前置：chat 工具循环（现状 ToolIDs 读到不执行）+ mcp 执行能力（mcp 模块 api 仍为占位）。

## 4. agent 绑定按类型把关——递延到工具篇

- spec 07 管道对 chat/task 两型一视同仁（spec 08 §3：本篇先不和 agent 关联）；「管道仅 chat 型 / 工具仅 task 型」的把关随工具注册一并拍板。

## 5. 结构化 inputs——已落地（spec 08 O7a，2026-09-20）

- ~~spec 06 O1 单一 string 是终形；演进触发点：工具参数 schema（备忘录）与嵌套子图传参（spec 08 边界）。~~
- 落地内容：`workflows.input_schema` / `output_schema` jsonb（简化形态）+ task 型 JSON 文本入参（Execute 签名不动）+ 池一级下钻 + 子终稿 output 校验（spec 08 §4.5）。
- 剩余消费者：工具篇的参数 schema 生成（备忘录已更新为从 input_schema 生成）。

## 6. budget 接线——roadmap 独立项

- 每用户限流 + 每日预算熔断在 chat 全路径接入（组合根 TODO 未动；spec 07 不碰）。

## 7. 前端——打字机动画与管道 UI

- 终稿单条整段 delta 已定（spec 07 O5），打字机动画纯前端实现；管道分支 UI 提示、分型选择 UI、嵌套编辑提示一并归前端篇。

## 8. 异步执行 + 轮询 / 按节点 SSE 流式——递延（spec 08 O8，2026-09-20）

- 现状：嵌套链每层独立 5min（executeChild 自包 `WithTimeoutCause`），请求 ctx 全程传播；同步路径墙钟仍受 nginx 300s 事实约束——超长同步链会在网关处断（拍板接受）。
- 逃生阀：异步执行 + 提交-轮询模型（**重开 spec 06 异步边界**——同步纯函数契约、append-only 收尾统一写轨迹等前提需重议）；按节点 SSE 流式（与 #2 进度事件同族，onNodeDone 留缝是起点）。
- 触发：真实长链需求出现（单层 5min 不够用的实际场景）。
