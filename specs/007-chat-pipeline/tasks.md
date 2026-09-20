# Tasks: Workflow 管道接线（chat-pipeline）

**Input**: Design documents from `/specs/007-chat-pipeline/`

**Prerequisites**: plan.md、spec.md、research.md、data-model.md、contracts/chat-pipeline.md、quickstart.md（全部在场）；前置 spec 01-06 已合入（执行引擎 commit `5d3297e`）

**Tests**: 本篇按 spec-dev 严格 TDD 执行（RED → GREEN → REFACTOR）——impl_spec_07 §7 验收门明确测试清单，测试任务先于对应实现任务。

**Organization**: 按用户故事分组（US1 终稿即回复 / US2 错误呈现 / US3 两链互溯），每故事独立可测。

**DoD（每任务收口）**: `go build ./... && go vet ./... && go test ./... -race -count=1` 全绿后勾任务，红了当场修不攒批。冻结契约逐字保真（impl_spec_07 §4.1 调用序 / §4.2 映射表 / §4.3 事件序列），偏差即软门禁停下。

## Format: `[ID] [P?] [Story] Description`

- **[P]**: 可并行（不同文件、无未完成依赖）
- **[Story]**: 所属用户故事（US1/US2/US3）
- 任务描述含精确文件路径

---

## Phase 1: Setup（基线）

**Purpose**: 开工基线确认——本篇零新文件、零新依赖、零迁移，无结构可建

- [ ] T001 基线门禁确认：`go build ./... && go vet ./... && go test ./... -race -count=1` 全绿；`ls migrations/` 确认 19 条在场（PG 未起时 `make migrate-status` 条数核对记入人工项——本篇零迁移，00020 留给 spec 08 不占）

**Checkpoint**: 基线干净，可开工

---

## Phase 2: Foundational（阻塞性前置）

**Purpose**: 全部故事的共用前置——service 接口缝 + 装配拆半（先于任何管道行为）

- [ ] T002 internal/chat/service/service.go 增 `workflowExecutor` 小接口（单方法 `Execute(ctx context.Context, req workflowapi.ExecuteWorkflowReq) (*workflowapi.RunResultSchema, error)`，放既有小接口 type block，agentGetter 等同款形态，research.md D2）+ `chatService` 增 `workflows` 字段 + `New` 尾参增 `workflows workflowExecutor`（既有六参位次不动，research.md D6）；**同任务**改 internal/app/server.go：`chatsvc.New(...)` 注入 `workflowSvc`（既有变量直传）、删除「chat 本期不消费 workflow，触发接线归后续 chat spec」TODO 注释、chat 依赖方向注释补 `chat → workflow`——两文件必须同任务（New 签名变更不同步 server.go 会破编译，任务收口门禁必须绿）
- [ ] T003 internal/chat/service/turn.go `setupTurn` 拆半（research.md D1，O8 拍板的最小实现）：拆为 `setupConvAgent`（getOwnedConversation → agents.Get → Enabled 检查，产出 conv + agent 前半）与 `setupLLMClient`（ModelID parse 脏数据防御 → ResolveLLMConfig → clients.Client，补全后半）；`llmSetup` struct 保留；原路径 runTurn 依次调两段——行为等价重构，既有 turn_test.go 全绿即证（REFACTOR 于既有测试保护下，零行为变更）

**Checkpoint**: 接口缝与拆半就位、编译绿、既有测试零回归；三个故事可开工

---

## Phase 3: User Story 1 - 绑定 agent 的消息确定性先过工作流，终稿即回复 (Priority: P1) 🎯 MVP

**Goal**: 管道分支 + 终稿两模式呈现（impl_spec_07 §4.1 伪代码直译）

**Independent Test**: stub workflowExecutor，断言绑定 agent 会话发消息 → Execute 被调且入参正确（input=当前消息、会话/消息引用回传、非试运行）→ 终稿两模式呈现正确（流式 delta+done 序列 / 一次输出 reply 全字段）

### Tests for User Story 1（先写、先跑红）

- [ ] T004 [US1] internal/chat/service/doubles_test.go 增 `workflowExecutor` stub（内嵌接口、记录 Execute 入参与收到时的 ctx，doubles 既有风格）+ internal/chat/service/turn_test.go 增管道契约用例：绑定 agent（WorkflowID="42"）发消息——Execute 入参断言（ID=42 / Input=req.Content / ConversationID=&conv.ID / MessageID=触发 user 消息 id / Trial=false）；user 消息在 Execute 调用前已落库；首条消息标题回填（conv.Title=="" 时按 user 内容回填，位置语义同原路径）；流式模式事件序列恰为 delta(终稿整段) → done（usage 全零、finish_reason="workflow"、done 不带 content、无 citations 事件）；一次输出模式 reply 全字段（Content=终稿 / Usage 全零 / FinishReason="workflow" / Citations=[] / MessageID=assistant 行 id 字符串化）
- [ ] T005 [US1] internal/chat/service/turn_test.go 增守护用例：未绑 agent（WorkflowID=nil）→ stub Execute 不被调（原路径零改动守点，模型循环照常）；agent 模型配置失效（ModelID 脏数据 / ResolveLLMConfig 返回哨兵）→ 管道照常执行到终稿（模型解析不发生，O8）；agent.WorkflowID 为非数字字符串 → errs.ErrInternal 包装返回（errors.Is 可判，同 ModelID 既有处理形态）

### Implementation for User Story 1

- [ ] T006 [US1] internal/chat/service/turn.go runTurn 管道分支（impl_spec_07 §4.1 伪代码直译，冻结契约逐字保真）：`setupConvAgent` 后判 `agent.WorkflowID != nil` → parseUint（脏数据 → errs.ErrInternal 包装）→ user 消息落库 → 首条消息标题回填 → `s.workflows.Execute(ctx, ExecuteWorkflowReq{ID, Input: req.Content, ConversationID: &conv.ID, MessageID: &userMsg.ID, Trial: false})`（错误先 `return nil, err`——translateWorkflowError 于 T009 落位后替换出口）→ `persistAssistant(res.Output, citations=[])`（失败仅 WARN 不阻断）→ TouchConversation（失败仅 WARN）→ 流式 `emit(DeltaEvent(res.Output))` + `emit(DoneEvent(assistant.ID, Usage{}, "workflow"))`（emit 失败静默收尾）/ 一次输出返回 reply（Content=终稿 / Usage{0,0} / FinishReason="workflow" / Citations=[]）；替代语义——buildSystemPrompt / RAG 检索 / 模型循环不进入管道路径；未绑定走原路径两段装配零改动

**Checkpoint**: US1 独立可测——管道契约用例全绿、未绑零回归、全量门禁绿

---

## Phase 4: User Story 2 - 管道失败的显式错误呈现（不静默降级） (Priority: P2)

**Goal**: translateWorkflowError 单一事实源 + failChat 哨兵分支（impl_spec_07 §4.2 映射表 + clarify 拍板 MODEL_NOT_FOUND→404 补齐）

**Independent Test**: stub Execute 返回各错误类，断言四类映射（503/400/500/404）两模式一致、未绑路径不受影响

### Tests for User Story 2（先写、先跑红）

- [ ] T007 [P] [US2] internal/chat/service/turn_test.go 增 translateWorkflowError 用例（research.md D3 形态）：workflowapi.ErrWorkflowNotPublished / errs.ErrValidationFailed（message 带 `node <key>:` 前缀）/ workflowapi.ErrWorkflowExecutionFailed / workflowapi.ErrWorkflowNotFound / providerapi.ErrModelNotFound 各一例——哨兵本体原样返回（errors.Is 全程可判、node 前缀 message 与错误链保真）；非哨兵错误（如 store 层 DB 错误）→ `%w` 上下文包装（"workflow execute: ..." 语义，内层错误仍可 unwrapped）
- [ ] T008 [P] [US2] internal/chat/handler/handler_test.go 增 failChat 用例（httptest，stub svc 返回各哨兵）：ErrWorkflowNotPublished → 503 `WORKFLOW_NOT_PUBLISHED`；errs.ErrValidationFailed → 400 `VALIDATION_FAILED` 且 message 透传 `node <key>:` 前缀（经 FailFromSentinel 既有分支，零新码）；ErrWorkflowExecutionFailed → 500 `WORKFLOW_EXECUTION_FAILED`；ErrWorkflowNotFound → 404 `WORKFLOW_NOT_FOUND`；providerapi.ErrModelNotFound → 404 `MODEL_NOT_FOUND`（clarify 拍板补齐——原路径与管道路径同分支修复）；每例断言 respond 信封（success=false、error.code=哨兵 Error()）；流式路径错误发生于首 emit 前 → 标准 JSON 信封返回、SSE 头未写（零 SSE error 事件，O4）

### Implementation for User Story 2

- [ ] T009 [US2] internal/chat/service/turn.go 增 `translateWorkflowError`（形态对齐既有 translateLLMError，research.md D3：哨兵原样透传 / 非哨兵 `%w` 补上下文），并替换 T006 管道分支的 Execute 错误出口（两模式单一事实源）
- [ ] T010 [P] [US2] internal/chat/handler/handler.go failChat 增四哨兵分支：workflowapi.ErrWorkflowNotPublished → 503 / workflowapi.ErrWorkflowExecutionFailed → 500 / workflowapi.ErrWorkflowNotFound → 404 / providerapi.ErrModelNotFound → 404（均 `respond.Fail(c, 状态码, 哨兵.Error(), err.Error())`，error.code=哨兵文案）；default `FailFromSentinel` 兜底不变（未识别 → 500 INTERNAL_ERROR，与 workflow execute 端点同形）

**Checkpoint**: US1+US2 独立可测——错误四类映射 + 哨兵透传全绿、MODEL_NOT_FOUND 两前门同码

---

## Phase 5: User Story 3 - 排障：对话链与执行链可互溯 (Priority: P3)

**Goal**: 两链互溯约束收口（引用回填已由 US1 入参契约达成；本故事验证取消/断连语义与消息状态）

**Independent Test**: 断言 Execute 入参含会话/消息引用（US1 已覆盖）；断连场景断言 user 已落、assistant 无、ctx 取消透传、emit 失败静默收尾

### Tests for User Story 3（先写、先跑红）

- [ ] T011 [US3] internal/chat/service/turn_test.go 增断连与状态用例：请求 ctx 已取消 → stub 断言 Execute 收到的 ctx 感知取消（透传不吞）；emit 返回错误（前端已断连）→ Stream 正常收尾不返回错误、assistant 已落库（静默收尾）；Execute 返回错误 → user 消息已落、assistant 行无（messages 恰一行 user，两链状态与原路径 LLM 失败同款）

### Implementation for User Story 3

- [ ] T012 [US3] 依 T011 红绿判定：红 → 修 internal/chat/service/turn.go 管道分支至绿（预期零改动或极小——ctx 透传、落库序、静默收尾已在 T006 契约实现内）；绿 → 直接标记完成（约束已满足即证，不为绿测试改实现）

**Checkpoint**: 三故事全部独立可测；trace_id 串两链与 run 行落库为 workflow 侧既有语义（零行为改动），psql 互溯验证归人工项（quickstart §2③）

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: 文档同步 + 终局门禁

- [ ] T013 [P] 文档同步四处：docs/changelog/chat/data_flow_and_model.md 路线图更新（E1 管道形态已接线）；docs/testing/chat-manual-test.md 增「管道冒烟」小节（对应 quickstart §2 ①②：不绑冒烟零变化 + 绑定冒烟 delta+done / 一次输出信封 / draft 负向 503）；docs/testing/workflow-manual-test.md 增 `trigger_source='chat'` 验证项（对应 quickstart §2③：run 行引用回填两链互溯）；CLAUDE.md 错误码表**仅核对零新行**（MODEL_NOT_FOUND 行既有、补实现即准确——不编辑该表）
- [ ] T014 终局验收门禁（quickstart §1 命令块全量执行）：`go build ./... && go vet ./... && go test ./... -race -count=1` 全绿；`go test ./internal/chat/... -race -cover` ≥80%；依赖方向 grep——`grep -rn "go-hify/internal/workflow" internal/chat/` 仅 internal/chat/service 的 workflowapi import（白名单内）、`grep -rn "go-hify/internal/chat" internal/workflow/` 零输出；`git diff --stat` 确认 internal/workflow/ 零触碰（workflow 模块零行为改动）

---

## Dependencies & Execution Order

### Phase Dependencies

- **Phase 1（基线）** → **Phase 2（接口缝 + 拆半，阻塞所有故事）** → **Phase 3/4/5（故事）** → **Phase 6（收尾）**
- Phase 2 的 T002（接口缝）与 T003（拆半）为全部管道行为前置；T003 依赖 T002 无关（不同改动面，但同文件 turn.go 在 T006 才动——T002/T003 可先后执行）

### User Story Dependencies

- **US1（P1）**：依赖 Phase 2；MVP 即本故事
- **US2（P2）**：service 侧（T009）依赖 US1 的管道分支（T006）——替换其错误出口；handler 侧（T008/T010）仅依赖哨兵定义，可与 US1 并行
- **US3（P3）**：依赖 US1 入参契约（引用回填断言在 T004 已含）；T011/T012 为约束验证性质
- **推荐执行序**：严格 P1 → P2 → P3 串行——turn.go 是主战场（T006/T009/T012 连续增量），并行收益低冲突高

### Within Each User Story

- 测试先写、先跑红（RED）→ 最小实现（GREEN）→ 对齐邻近风格（REFACTOR）
- 每任务收口跑全量门禁（见文件头 DoD）

### Parallel Opportunities

- T007（turn_test.go）与 T008（handler_test.go）不同文件，RED 阶段可并行
- T009（turn.go）与 T010（handler.go）不同文件，GREEN 阶段可并行（各自先有 RED 测试）
- T013（文档）与 T011/T012 无文件交集，可穿插执行

---

## Parallel Example: User Story 2

```bash
# RED 阶段两路并行：
Task: T007 translateWorkflowError 用例（internal/chat/service/turn_test.go）
Task: T008 failChat 哨兵分支用例（internal/chat/handler/handler_test.go）
# GREEN 阶段两路并行：
Task: T009 translateWorkflowError + 管道错误出口接线（internal/chat/service/turn.go）
Task: T010 failChat 四哨兵分支（internal/chat/handler/handler.go）
```

---

## Implementation Strategy

### MVP First（User Story 1 Only）

1. Phase 1 基线 → Phase 2 接口缝（T002）+ 拆半（T003）
2. Phase 3 US1：T004/T005 RED → T006 GREEN → 全量门禁绿
3. **STOP and VALIDATE**：US1 独立验证（绑定即接管、终稿即回复、未绑零回归）
4. MVP 达成——P2/P3 在此之上纯增量

### Incremental Delivery

1. 基线 + 接口缝 + 拆半 → 2. US1 终稿即回复（MVP）→ 3. US2 错误呈现 → 4. US3 互溯约束收口 → 5. 文档同步 + 终局门禁

---

## Notes

- 冻结契约保真：§4.1 调用序 / §4.2 映射表 / §4.3 事件序列逐字对齐（引用条目用 spec 编号不自造同义词），偏差 → 软门禁停下问用户
- 反作弊：不删断言、不 t.Skip、不调低覆盖率门槛；未全绿不勾任务
- 范围红线：只做本篇清单内内容；「顺手改进」冲动记录待问不直接做
- 全程不自动 commit / push——commit 时机由用户明示；可提交节点 message：`feat(chat): workflow 管道接线——绑定 agent 的消息确定性先过工作流（spec 07）`
- 人工项（不产码，归验收报告清单）：`make start` 冒烟三步走（quickstart §2）、PG 起后 `make migrate-status` 条数核对
