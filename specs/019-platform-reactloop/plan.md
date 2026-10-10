# Implementation Plan: platform 公共 ReAct 工具循环抽取 + chat 重构换用

**Branch**: `019-platform-reactloop` | **Date**: 2026-10-10 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/019-platform-reactloop/spec.md`（2026-10-10 起草 + Clarifications 四条代码取证）；上位冻结 [docs/changelog/mcp/chat_tool_loop_spec.md](../../docs/changelog/mcp/chat_tool_loop_spec.md)

## Summary

把 chat 引擎内的多轮工具循环本体（轮循环 / 8 轮上限 nudge 收尾 / 错误即上下文 / 20k 截断 / 判定纪律 / 冻结常量族）逐字下沉为新 platform 包 `internal/platform/reactloop`（无状态 `Run` 函数：消费 `*llm.Client` + `*tool.Registry` + 初始消息 + 调用选项 + 回调缝五钩），chat 删除自有循环体与常量、经回调接线保持 SSE / 落库 / executions 全部现行为（对外契约零变化）。循环双模式（Stream 逐 delta / Generate 整体终态）同一本体——非流式模式本篇单测证明、spec 020 的 workflow react 节点消费。零迁移、零新路由、零新哨兵、零组合根装配（Run 纯函数式消费，无需注入）。

## Technical Context

**Language/Version**: Go 1.26（后端单二进制）

**Primary Dependencies**: 既有 `internal/platform/llm`（Client.Stream / Client.Generate / CallOptions.Tools / Final.ToolCalls / Usage）与 `internal/platform/tool`（Registry / Invoke / ToolCall / ToolResult）公开面；零新增第三方依赖。

**Storage**: 零迁移（`make migrate-status` 恒 21 applied；循环不落库——持久化全在 chat 回调闭包内）。

**Testing**: `go test ./... -race -count=1`；`internal/platform/reactloop/loop_test.go` 移植 chat toolloop_test 场景族十组（stub llm client + stub tool.Source，零真实 LLM/MCP/PG）；chat 既有测试全量回归零改动绿（零行为变化证明）；platform/reactloop 与 chat/service 覆盖率各 ≥80%。

**Target Platform**: Docker Compose 单机（后端单二进制 alpine）

**Project Type**: 模块化单体（platform 共享基建包 + chat 模块内部重构）

**Performance Goals**: 无新热路径——循环即现状 chat 循环的搬家；LLM 调用次数 / bulkhead 占用 / 超时层级全不变（超时重试熔断仍在 llm client 内部）。

**Constraints**: 冻结常量文案逐字迁入不改（nudge / 非法参数文案 / max_iterations 标识 / 20000 rune / 默认 8 轮）；chat 对外契约零变化为硬红线（SSE 帧序、messages 与 executions 落库形态、路由、信封）；platform 新包 import 面 = llm + tool + 标准库；eino / go-sdk grep 纪律不变；CallOptions 构造后不可变（eino 转换缓存前提）——摘工具轮必须新建 opts。

**Scale/Scope**: 新增 1 包 2 文件（loop.go + loop_test.go）；chat 侧 turn.go / tools.go 循环体删除与回调接线（约 -200/+150 行量级）；chat/api、handler、store、mcp、workflow、app/server.go、go.mod 零改动。

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| 原则 | 判定 | 依据 |
|---|---|---|
| I. Simplicity First | ✅ PASS | 动机即消灭双份维护（020 react 节点否则复制循环）；搬家不增抽象层——单 Run 函数 + 回调结构体，无接口无注册表；chat 净删循环体 |
| II. 模块化单体与单向依赖 | ✅ PASS | platform 不依赖业务域（reactloop 只 import llm/tool/标准库）；chat → platform 方向合法（依赖清单既有）；无环 |
| III. 统一 LLM 接入层 | ✅ PASS | 循环只消费 llm.Client 公开面（Stream/Generate）；不新增超时 / 重试 / 熔断逻辑（bulkhead 与三层超时留在 llm client 内部，循环不碰）；判定纪律只看 len(ToolCalls) |
| IV. SSE 链路 | ✅ PASS | SSE 推送全在 chat 回调闭包（OnDelta/OnToolCall/OnToolResult → 既有 emit 链），事件形态零变化；断连中止语义经回调错误=中止信号保持 |
| V. 数据库纪律 | ✅ PASS | 零迁移零新表；落库全在 chat 既有 store 调用（经回调保持行序与 midPersisted 语义） |
| VI. 可观测与成本护栏 | ✅ PASS | executions 逐轮记录经 OnRound 回调保持（ConversationID=nil 语义不变）；usage 累计语义不变 |
| VII. 统一契约与安全基线 | ✅ PASS | 零新路由零新哨兵零新配置项；chat 对外信封 / SSE 事件 / 落库形态零变化 |

## Project Structure

### Documentation (this feature)

```text
specs/019-platform-reactloop/
├── spec.md              # 定稿 Feature Spec（含 Clarifications 2026-10-10 四条取证）
├── checklists/
│   └── requirements.md  # specify 质量清单（16/16）
├── plan.md              # 本文件
├── research.md          # Phase 0：实现级决策 R1~R8
├── data-model.md        # Phase 1：循环配置/产物/回调/常量族形态（无 DB 实体）
├── contracts/
│   └── reactloop-api.md # Phase 1：Run 签名 + Hooks + Config + Result 冻结面
├── quickstart.md        # Phase 1：验证场景
└── tasks.md             # Phase 2（/speckit-tasks 产出）
```

### Source Code (repository root)

```text
internal/platform/reactloop/          # 新增（platform 共享基建）
├── loop.go            # Run 循环本体：轮循环 + 判定纪律 + 上限收尾（nudge 瞬态 + 摘工具
│                      #   新建 opts）+ 错误即上下文 + 截断单点 + 双模式（Stream/Generate）
│                      #   + 回调缝五钩 + 取消语义；冻结常量族逐字迁入（DefaultMaxRounds=8 /
│                      #   NudgeText / FinishReasonMaxIterations / MaxResultRunes=20000 /
│                      #   InvalidArgsText）
└── loop_test.go       # 场景族十组：轮上限 / nudge / 错误喂回 / 参数非法不发起调用 /
                       #   回调错误中止 / Registry nil 与空清单 / 截断保真 / 双模式一致 /
                       #   取消区分 / 流中段失败部分产物

internal/chat/service/
├── turn.go            # runTurn 循环体删除 → 消费 reactloop.Run；回调接线：OnDelta → SSE
│                      #   delta emit、OnRound → recordExecution（isPreAttemptErr 判定留此）、
│                      #   OnToolRoundStart → assistant 中间行（midPersisted 闭包态）、
│                      #   OnToolCall → ToolCallEvent emit、OnToolResult → tool 行落库 +
│                      #   ToolResultEvent emit；clientGone 静默路径映射 Result.Cancelled/Aborted
└── tools.go           # loadToolRegistry 降级链保留（装配属业务）；runToolRound / parseToolArgs /
                       #   truncateToolContent / 常量族删除（迁入 reactloop）；toolloop_test 场景
                       #   移植至 reactloop 后本文件对应测试删除
```

**Structure Decision**: platform 新包单文件承载循环（<400 行门槛内）；chat 侧只留装配（Registry 降级链）与回调接线（SSE/落库/executions）——循环语义单点、业务副作用单点。

## Complexity Tracking

> Constitution Check 全 PASS，无 violation 需登记。
