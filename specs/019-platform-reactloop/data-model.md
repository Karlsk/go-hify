# Data Model: reactloop 循环形态（spec 019 Phase 1）

**Date**: 2026-10-10 | **Spec**: [spec.md](./spec.md) | **API 面**: [contracts/reactloop-api.md](./contracts/reactloop-api.md)

零 DB 实体、零迁移——本篇形态全为内存契约（配置 / 产物 / 回调 / 常量 / 消息流）。

## 1. 循环内部状态（非公开）

| 状态 | 形态 | 说明 |
|---|---|---|
| 消息序列 | `[]llm.Message`（入参副本上 append） | 初始 = 调用方 msgs；工具轮追加 assistant（Content+ToolCalls）与 tool（Content+ToolCallID+ToolName）消息；nudge 消息仅收尾轮可见、不入序列 |
| 轮计数 | int | 0 起；`atLimit = round >= 生效上限`；生效上限 = cfg.MaxRounds>0 ? 之 : DefaultMaxRounds |
| usage 累计 | llm.Usage | 逐轮 Final.Usage 累加（含收尾轮） |
| 终稿 | string | 末轮 Final.Content（流中段失败 = 已消费部分） |

## 2. 单轮执行形态（双模式分叉点）

| 模式 | 单轮调用 | delta 面 | 终态面 |
|---|---|---|---|
| Stream（chat） | `client.Stream(ctx, roundMsgs, roundOpts)` | Recv 循环逐 delta → OnDelta | `stream.Final()` 聚合（content 拼接 / tool_calls 按 Index 合并 / usage max / finish_reason 末非空） |
| Generate（020 react 节点） | `client.Generate(ctx, roundMsgs, roundOpts)` | 无 | 返回 Final 直用 |

分叉收敛在单轮 helper；轮循环 / 工具轮 / 收尾 / 取消检查两模式共享。

## 3. 工具轮形态（冻结序）

```
解析全部调用参数（一次解析：合法性判定 + 归一 {} 形态）
→ OnToolRoundStart(round, assistantContent, []CallView)      # chat：assistant 中间行
→ 逐调用：OnToolCall(view)                                    # chat：ToolCallEvent（非法参数也发）
          → ArgsValid ? reg.Invoke : 直喂 IsError{InvalidArgsText}
          → result.Content 截断（MaxResultRunes，单点）
          → OnToolResult(view, result)                        # chat：tool 行 + ToolResultEvent
          → 追加 tool 消息进序列
（任一钩子 error → 停剩余调用、Result.Aborted + 该错误返回）
```

## 4. 常量族（唯一家 = reactloop，C3 表）

DefaultMaxRounds=8 / NudgeText / FinishReasonMaxIterations / MaxResultRunes=20000 / InvalidArgsText——自 chat/service/tools.go 逐字迁入；chat 侧删除（SC-003 grep 面）。

## 5. chat 侧保留 / 删除面（R6）

| 保留（chat） | 删除（chat → 迁入 reactloop） |
|---|---|
| loadToolRegistry 装配 + 五级降级链 | runTurn 轮循环体 |
| recordExecution + isPreAttemptErr 判定（OnRound 闭包） | runToolRound |
| SSE emit 链（五钩闭包接线） | parseToolArgs（解析归循环，CallView 传产物） |
| messages / executions 落库（钩子闭包） | truncateToolContent（截断归循环） |
| setup / 历史 / 系统提示组装 | 常量族五件 |
| consumeStream 的 clientGone 语义 → Result.Cancelled/Aborted 映射 | consumeStream 本体（语义入循环 Stream 分支） |

## 6. 兼容性矩阵

| 现状行为 | 换用后 |
|---|---|
| 无工具 Agent 对话（reg=nil） | 单轮直答，零工具事件（C4 降级行） |
| 多工具同轮 | 事件序 call→result→call→result 交错不变（C2 钩子序） |
| 8 轮打满 | nudge + 摘工具收尾 + done 带 max_iterations 不变 |
| 工具 IsError | 喂回自纠、对话不中断不变 |
| 前端中途断连 | emit 错误 → 钩子错误 → Aborted → chat 静默收尾不变 |
| 一次输出模式 | 同循环非 SSE 路径（emit=nil 现语义 = Hooks 全 nil 除 OnRound/落库钩子）不变 |
