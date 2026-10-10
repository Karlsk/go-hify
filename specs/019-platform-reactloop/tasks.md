# Tasks: platform 公共 ReAct 工具循环抽取 + chat 重构换用（spec 019）

**Input**: Design documents from `/specs/019-platform-reactloop/`

**Prerequisites**: spec.md ✅（2026-10-10 起草 + Clarifications 四条取证）、plan.md ✅、research.md ✅、data-model.md ✅、contracts/reactloop-api.md ✅、quickstart.md ✅

**Tests**: TDD 强制——循环本体先 RED（loop_test.go 场景族移植自 chat toolloop_test）后 GREEN；chat 换用以「既有 chat 测试零改动全绿」为行为不变证明（SC-001）。

**Organization**: Phase 2 循环本体（US2 的语义单点载体 + US3 的双模式载体先行）→ US1 chat 换用（P1 验收主线）→ US2 单点证明（grep/覆盖率）→ US3 双模式与取消补测 → 验收门。

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to
- 红线约束（贯穿所有实现任务）：零迁移（恒 21 applied）、零新路由 / 哨兵 / 配置项、chat 对外契约零变化（SSE 帧序 / 落库形态 / 路由 / 信封，SC-001 硬红线）、platform 新包 import 面仅 `platform/llm` + `platform/tool` + 标准库（FR-009）、冻结常量文案逐字迁入不改（C3 表）、CallOptions 构造后不可变——摘工具收尾轮标量新建 opts（R3）、工具调用串行 Invoke（chat_tool_loop_spec §10 冻结）。

---

## Phase 1: Setup（基线确认）

**Purpose**: 零迁移假设与基线门禁核实——不动任何代码

- [x] T001 基线确认：`go build ./... && go vet ./... && go test ./... -race -count=1` 全绿；`make migrate-status` 21 applied、下一号 00022（本篇零迁移、020 占用）；chat 循环现状基线 `internal/chat/service/turn.go`（runTurn）+ `tools.go`（runToolRound / 常量族）在位（commit `0d6293f` 后裔）

---

## Phase 2: Foundational（reactloop 循环本体——US2/US3 的公共载体）

**Purpose**: platform 新包承载循环语义（FR-001~FR-007 载体），chat 换用前的独立可测增量

- [x] T002 RED（场景族单测）：新建 `internal/platform/reactloop/loop_test.go`——stub 基建（stub llm 上游应答编排 + stub tool.Source 可控 Invoke/IsError，参照 chat toolloop_test 的 genStreamer/stub 模式自建本包私有辅助）+ 核心场景族移植：① 两轮工具循环 RoundTrip（消息序 assistant+tool 追加正确、终稿/usage 累计）② 轮上限 nudge 收尾（第 9 轮起 nudge 瞬态 + 摘工具 opts 新建 + HitLimit + FinishReason=max_iterations；nudge 不入 msgs 持久面）③ 错误即上下文（Invoke IsError 喂回、循环不中断）④ 参数非法不发起调用（InvalidArgs 直喂、stub Invoke 零调用记录）⑤ 回调错误中止（OnToolCall 返回 error → 剩余调用不执行、Result.Aborted）⑥ Registry nil 与空清单 → 单轮直答零工具事件 ⑦ 截断保真（超 MaxResultRunes 结果：OnToolResult 收到与回喂消息同一份截断产物）；运行确认失败（reactloop 包未定义编译红 = 正常 RED）
- [x] T003 GREEN（循环本体）：新建 `internal/platform/reactloop/loop.go`——冻结常量族五件逐字迁入（C3 表）；`Run(ctx, client, reg, msgs, opts, cfg, hooks)` 按 contracts C1/C2 实现：轮循环 + 判定纪律（只看 len(ToolCalls)>0）+ 轮间 ctx 检查（Cancelled）+ 上限收尾（nudge 仅当轮可见 + 标量新建无工具 opts，R3）+ 工具轮冻结序（解析一次归一 {} → OnToolRoundStart → 逐调用 OnToolCall → Invoke|非法直喂 → 截断单点 → OnToolResult → tool 消息追加）+ 双模式分叉（Stream：Recv 逐 delta OnDelta + Final 聚合；Generate：直取 Final）+ 流中段失败部分产物返回 + msgs 入参副本不修改调用方 slice；任务级门禁 `go build ./... && go vet ./... && go test ./internal/platform/reactloop/ -race -count=1` 绿

**Checkpoint**: 循环独立可测——`go test ./internal/platform/reactloop/ -race -v` 场景族全绿，不 import chat、不依赖业务装配（SC-002 前半）

---

## Phase 3: User Story 1 - chat 换用零感知 (Priority: P1) 🎯 MVP 主线

**Goal**: chat 删除自有循环体与常量、消费 reactloop.Run + 五钩接线，对外契约与落库形态零变化（FR-008 / SC-001）

**Independent Test**: chat 既有全量测试**零改动**通过（行为不变证明）+ 绑定工具 Agent 流式/一次输出/上限/工具失败/断连五场景手测零差异（quickstart 场景 A/B）

### Implementation for User Story 1

- [x] T004 [US1] 换用接线：`internal/chat/service/turn.go` runTurn 轮循环体删除 → 组装 Config/Hooks 调 `reactloop.Run`：OnDelta → 既有 SSE delta emit；OnRound → recordExecution（isPreAttemptErr 判定留此闭包）；OnToolRoundStart → assistant 中间行落库（midPersisted 闭包态）；OnToolCall → ToolCallEvent emit（emit 错误 = 钩子错误 → Aborted 静默）；OnToolResult → tool 行落库（midPersisted 跳过语义）+ ToolResultEvent emit；Result.Cancelled/Aborted → 既有 clientGone 静默路径；Result.HitLimit/FinishReason → done 事件同现状；一次输出模式 = 同 Run 非 emit 钩子集（OnDelta/OnToolCall 置 nil，落库钩子在）；`internal/chat/service/tools.go` 删除 runToolRound / parseToolArgs / truncateToolContent / consumeStream / 常量族五件（loadToolRegistry 降级链保留）；toolloop 相关旧测试随场景族已移植 T002 删除
- [x] T005 [US1] 回归证明：`go test ./internal/chat/... -race -count=1` **既有测试零改动**全绿（SSE 帧序 / 落库行序 / executions / 降级链断言原样通过 = 行为不变证据）；红了只修接线不改断言（反作弊红线）；任务级全量门禁绿

**Checkpoint**: MVP 成立——循环单点（reactloop）+ chat 消费（换用完成）；`go test ./... -race -count=1` 全绿

---

## Phase 4: User Story 2 - 循环语义单点维护 (Priority: P1)

**Goal**: 语义常量与轮循环体在 chat 零残留、唯一家在 platform（SC-003）

- [x] T006 [US2] 单点证明：grep 三件落地核对——`grep -rn "maxToolRounds\|maxToolResultRunes\|maxToolRoundsNudge\|invalidToolArgsMsg\|finishReasonMaxIterations" internal/chat/` 零命中；`grep -rn "for round :=" internal/chat/service/` 零轮循环体残留；常量唯一家 `internal/platform/reactloop/loop.go` 核对五件文案与 chat_tool_loop_spec 冻结逐字一致；`go list -deps ./internal/platform/reactloop | grep hify` 仅 llm/tool（FR-009）

---

## Phase 5: User Story 3 - 双模式与取消语义 (Priority: P2)

**Goal**: 非流式模式与取消/中止区分单测完备（020 react 节点消费前置，FR-006 / FR-007）

- [x] T007 [US3] 补测：`internal/platform/reactloop/loop_test.go` 增三组——① 双模式一致（同图景 Stream/Generate 两跑：Content/Usage/HitLimit/工具轮消息序一致、仅 OnDelta 有无之差）② 轮间取消（首轮后 cancel ctx → 二轮零 LLM 调用、Result.Cancelled、nil error）③ 流中段失败（第二轮 Recv 中途上游错误 → 部分 Content/Usage 返回 + err 上抛 + 首轮产物完整）；任务级门禁绿

---

## Phase 6: Polish & Cross-Cutting Concerns

- [x] T008 全量验收门（SC-004/SC-005）：`go build ./... && go vet ./... && go test ./... -race -count=1` 全绿；`go test ./internal/platform/reactloop/ ./internal/chat/service/ -race -cover` 两包各 ≥80%；`make migrate-status` 21 applied 不变、migrations/ 零改动；eino/go-sdk grep 纪律两条全空
- [x] T009 [P] 文档同步：`docs/testing/chat-manual-test.md` 工具批小节头部加注「循环本体 2026-10-10 起归 internal/platform/reactloop（spec 019），手测面零变化」；`docs/changelog/workflow/deferred_items.md` 增补 builtin 工具机制剔除留痕（2026-10-10 用户拍板，react 节点篇 020 亦不含）

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: 无依赖，立即开始
- **Foundational (Phase 2)**: 依赖 Phase 1；T002 → T003 严格 RED→GREEN
- **US1 (Phase 3)**: 依赖 T003（Run 公开面在位）；T004 → T005 严格顺序（接线 → 回归证明）
- **US2 (Phase 4)**: 依赖 T004（chat 常量随换用删除后才可 grep 证明）
- **US3 (Phase 5)**: 依赖 T003；与 Phase 3/4 可并行（仅加 reactloop 测试，不碰 chat）
- **Polish (Phase 6)**: 依赖全部；T008 ∥ T009 可并行

### User Story Dependencies

- **US1 (P1)**: Foundational 后即可开始——MVP 主线
- **US2 (P1)**: 依赖 US1 换用完成（grep 证明的前提是 chat 已删常量）
- **US3 (P2)**: 独立于 US1/US2（reactloop 包内补测），可与 US1 并行

### Parallel Opportunities

- T007 ∥ T004/T005（不同文件：loop_test.go vs chat 两文件）；T008 ∥ T009

---

## Implementation Strategy

### MVP First（Phase 2 + US1）

1. Phase 1 基线 → Phase 2 循环本体（T002 RED → T003 GREEN）
2. Phase 3 US1 换用（T004 接线 → T005 零改动回归证明）
3. **STOP and VALIDATE**: `go test ./... -race -count=1` 全绿 + quickstart 场景 A/B 手测零差异
4. Phase 4 US2 单点证明 → Phase 5 US3 补测 → Phase 6 验收门 + 文档

### 增量交付说明

Phase 2 交付即循环独立可测（SC-002）；+ US1 = 单点维护事实成立（MVP）；US2/US3 是证明面与 020 前置补测。TDD 纪律：T002 先写场景族运行确认失败再实现；T005 的「既有 chat 测试零改动」是反作弊红线本体——任何改断言让它绿的冲动 = 停下报告。

---

## Notes

- 任务级 DoD：`go build ./... && go vet ./... && go test ./... -race -count=1` 过门禁再勾任务；红了当场修不攒批
- 反作弊红线：不得改 chat 既有断言 / t.Skip / 调低覆盖率门槛；chat 回归红 = 接线错，修接线
- 冻结契约逐字保真：C1 Run 签名、C2 钩子序与中止语义、C3 常量文案、C4 承继清单、C5 chat 红线
- commit 由用户明示；可提交节点 message：`refactor(platform): ReAct 工具循环下沉 platform/reactloop + chat 换用——循环语义单点维护（spec 019）`
