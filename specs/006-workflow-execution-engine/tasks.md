# Tasks: Workflow 执行引擎（workflow-execution-engine）

**Input**: Design documents from `/specs/006-workflow-execution-engine/`

**Prerequisites**: plan.md（落位与约束）、spec.md（US1-US3）、research.md（R1-R12 落位决策）、data-model.md、contracts/execute-api.md

**Tests**: 显式要求（spec §7 验收门 + spec-dev 严格 TDD）——每故事先写测试确认失败（RED），再最小实现（GREEN），测试保护下重构（REFACTOR）。

**Organization**: 按 User Story 分相（US1 正式执行 P1 / US2 试运行 P2 / US3 排障回放 P3），保存期 R10 与文档同步归收尾相。

**权威契约**: 冻结契约逐字对齐 impl_spec_06 §4 / api_contract §5 / db_model §12 + §7 条 11；哨兵文案 = `error.code`。

## Format: `[ID] [P?] [Story] Description`

- **[P]**: 可并行（不同文件、无未完成依赖）
- **[Story]**: US1 / US2 / US3
- 任务内红线（全程适用）：`internal/workflow/` 零 `internal/chat` import；api 包无 gin/gorm；service 无 gin 类型（ctx 恒为 `context.Context`）；明文 API key 只在 ResolveLLMConfig → llm.Manager 调用瞬间（禁入日志/轨迹/缓存/响应）；事务内禁外部调用；错误链 store 原样上抛 → service 翻译哨兵 → handler `errors.Is` 映射。

---

## Phase 1: Setup

- [x] T001 开工确认：`make migrate-status` 18 条 applied（下一号 00019）+ `go build ./... && go vet ./... && go test ./... -race -count=1` 基线全绿（spec-dev 第 1 步已执行，实现期复核一次）

---

## Phase 2: Foundational（阻塞全部故事）

- [x] T002 [P] 迁移 migrations/00019_workflow_runs.sql——DDL 逐字取 db_model.md §12 冻结稿（workflow_runs + workflow_node_runs、CHECK、idx_workflow_runs_wf_created、idx_workflow_node_runs_run_id、UNIQUE(run_id,seq)、COMMENT、Up/Down 成对）；写后 `make migrate-status` 确认 19 条 applied、Down 可回滚
- [x] T003 [P] api 契约扩展 internal/workflow/api/——api.go 增 `Execute(ctx, ExecuteWorkflowReq) (*RunResultSchema, error)` 接口方法；schema.go 增 ExecuteWorkflowReq（Input binding required,max=16384 / ConversationID / MessageID / Trial）+ RunResultSchema（run_id/status/output/duration_ms/node_trace）+ NodeRunSummary（node_key/node_type/status/duration_ms/error_msg，research R11）；errors.go 增哨兵 `ErrWorkflowExecutionFailed`（文案 `WORKFLOW_EXECUTION_FAILED`，逐字）；api 包保持零 gin/gorm import
- [x] T004 [P] model 增实体 internal/workflow/service/model.go——WorkflowRun / WorkflowNodeRun（embed db.BaseAppendOnly，字段逐字对齐 db_model §12 Go model：弱引用 + 快照 + is_trial + trace_id + started_at 等；无 json tag）
- [x] T005 [P] config 增 env knob internal/platform/config/config.go——WORKFLOW_API_BLOCK_PRIVATE（envBool，先例 :110）/ WORKFLOW_RUNS_RETENTION_DAYS（envInt 默认 365，先例 :129）+ 各自测试

**Checkpoint**: 契约与地基就绪，三个故事可开工。

---

## Phase 3: US1 - 正式执行端到端（Priority: P1）🎯 MVP

**Goal**: published 工作流经 `POST /workflows/{id}/execute` 同步执行：六类节点语义、vars 池与模板、条件路由、5min 上限、错误二分法、executions 自记、轨迹落库、结果组装。

**Independent Test**: stub 下游对 published 图执行，断言 200 信封（status/output/node_trace/run_id）与 runs/node_runs 落库形态；draft 图 503。

### Tests（先写、确认 RED）

- [x] T006 [P] [US1] RED internal/workflow/service/execcontext_test.go——表驱动：render 混合文本多变量；strict 缺失变量报错（错误文案含缺失名）；condition 迷你表达式（裸 `{{var}}`、`{{var}} == 'literal'`、字面量含单引号/空串）；set 落池（仅成功节点）；steps 累积
- [x] T007 [P] [US1] RED internal/workflow/service/httpx_test.go——SSRF 矩阵：127.0.0.1/127.8.8.8/::1 拒、169.254.1.1/fe80::1 拒、fc00::1/fd00::1 拒、10.0.0.1/172.16.0.1/192.168.1.1 放行、BLOCK_PRIVATE=true 时私网全拒、非 http/https scheme 拒、重定向每跳复验（httptest 302 → 禁止目标）；timeout_sec 边界 1-60 与默认 10s；ssl_verify 两种 TLS 行为
- [x] T008 [US1] RED internal/workflow/service/executor_test.go——runNode 六类分发；tool 节点 fail-fast（图缺陷类 400、message 带 `node <key>:`）；callLLM 链 stub（ResolveLLMConfig → Manager.Client → Generate 非流式、prompt 经 render、temperature 透传）+ **executions 自记恰好一行且 ConversationID=nil**（窄接口 stub 记调用）；retrieve 结果格式化为编号段落文本；callAPI url/headers/body 全链渲染 + SSRF 拦截归环境限制类；buildOutput 按 output 模板拼终稿
- [x] T009 [US1] RED internal/workflow/service/execute_test.go——快照三查加载（stub Store）；线性游走到 end / 无出边终止取末节点输出；route 按声明顺序首条命中、无命中 fail-fast（400 带 node 前缀）、唯一无条件边；把关：draft/disabled → ErrWorkflowNotPublished（503，文案区分两态）；目标不存在：stub store 返回 gorm.ErrRecordNotFound → ErrWorkflowNotFound 原样透传（404 既有哨兵）；总时长上限：cause=超时 → ErrWorkflowExecutionFailed；ctx 取消立即中止带 node key；收尾 CreateRun 收到 run 行 + 全部 node_runs（seq 递增、失败节点 status=failed）；RunResultSchema 组装（run_id 字符串化、node_trace 顺序）；onNodeDone 注入缝（§4.3 冻结 `func(nodeKey, output string)`）：回调按节点逐个触发、nil 缝路径正常执行零开销
- [x] T010 [P] [US1] RED internal/workflow/store/store_test.go——CreateRun sqlmock：一事务内 run INSERT 1 行 + node_runs 一条多 VALUES INSERT（N 占位符），regexp.QuoteMeta 钉 SQL 形态；任一批失败整体回滚（Begin/Rollback 断言）；错误原样上抛不翻译
- [x] T011 [P] [US1] RED internal/workflow/handler/handler_test.go——httptest + fakeSvc：200 信封 RunResultSchema 字段齐；input 缺失/超 16384 → 400；draft → 503 WORKFLOW_NOT_PUBLISHED；引擎环境限制 → 500 WORKFLOW_EXECUTION_FAILED；下游哨兵（如 MODEL_NOT_FOUND）经既有映射透传；信封断言解析

### Implementation（GREEN，按依赖序）

- [x] T012 [US1] GREEN internal/workflow/service/execcontext.go——vars map[string]string（"input"+各 node_key）+ render strict（{{}} 扫描，与 condition 求值共用底层）+ 迷你表达式求值 + set + steps（骨架逐字对齐 impl_spec_06 §4.2）
- [x] T013 [US1] GREEN internal/workflow/service/httpx.go——出站 client：net.Dialer.Control 建连时 IP 校验（防 rebinding）、CheckRedirect 每跳自然复验、仅 http/https、timeout_sec 1-60 默认 10s 映射、ssl_verify TLS 配置、BLOCK_PRIVATE 全禁私网（research R3）
- [x] T014 [US1] GREEN internal/workflow/service/executor.go——runNode type switch 六类 + callLLM（链路 per research R1 + executions 自记窄接口写入缝，R2）/ evaluate / retrieve / callAPI（经 httpx）/ buildOutput；default 臂防御性报错
- [x] T015 [US1] GREEN internal/workflow/service/execute.go——把关（published）→ 快照三查（ListNodes/ListEdges + ParseNodeConfig）→ execContext 初始化 vars["input"] → 游走循环（ctx.Err 检查、slog 节点轨迹一条、每节点完成后调 onNodeDone 注入缝——§4.3 冻结签名 `func(nodeKey, output string)`，nil 安全跳过即控制台路径零开销；缝为 service 内部字段，api Execute 公签冻结无回调参）→ route → finishResult（CreateRun + RunResultSchema；降级重试 US3 补）→ 5min WithTimeoutCause(ctx, ErrWorkflowTimeout)；错误 `fmt.Errorf("node %s: %w")` 包装（骨架逐字 §4.2）
- [x] T016 [US1] GREEN internal/workflow/store/store.go——CreateRun：Transaction 内两批多 VALUES INSERT（T010 测试钉住的形态）
- [x] T017 [US1] GREEN 接线——internal/workflow/service/service.go 注入扩容（llmManager + executions 写入缝窄接口）+ internal/workflow/handler/handler.go 增 `POST /workflows/:id/execute` 薄绑定（BindUri+BindJSON+Query trial 解析→req.Trial；一个绑定函数只调一个接口方法）+ internal/app/server.go workflowsvc.New 追加 llmManager 与 execStore 注入

**Checkpoint**: US1 MVP 成立——published 图 stub 端到端可执行、可验收（quickstart §3.1/3.3/3.4 场景就绪）。

---

## Phase 4: US2 - 试运行（Priority: P2）

**Goal**: `?trial=true` 放开 draft/disabled（状态机唯一例外），试运行落 is_trial=true，正式路径 503 语义不变。

**Independent Test**: 同一 draft 图——带 trial 200、不带 trial 503；trial run 行 is_trial=true。

- [x] T018 [US2] RED 测试增例 internal/workflow/service/execute_test.go + handler/handler_test.go——draft+trial 200、disabled+trial 200、published+trial 200 且 is_trial=true、draft 无 trial 503（文案区分 draft/disabled）、trial run 行 is_trial 落库断言
- [x] T019 [US2] GREEN internal/workflow/service/execute.go + handler——把关改 `published || req.Trial`；run 行 IsTrial=req.Trial；（handler trial query 解析已在 T017 就位，此处补服务端语义）

**Checkpoint**: US1+US2 各自独立可测（O3 全场景）。

---

## Phase 5: US3 - 排障回放（Priority: P3）

**Goal**: 轨迹截断与标记、降级不阻断、trace_id 贯穿、保留期清理任务——「运行日志是排障唯一线索」完整落地。

**Independent Test**: 16KB 输出截断带 truncated 标记；CreateRun 持续失败时结果照返、run_id 空；retention 任务行为。

- [x] T020 [US3] RED internal/workflow/service/execute_test.go（或独立截断测试文件）——16KB 边界（恰 16384 保留 / 16385 截断+`truncated:true` jsonb 标记）、slog 节点轨迹 1KB、input/output 三处落库值均过截断
- [x] T021 [US3] GREEN internal/workflow/service/execute.go——truncate helper + finishResult 落库值接截断与标记包装；slog 轨迹 1KB 截断
- [x] T022 [US3] RED internal/workflow/service/execute_test.go——写入降级：stub CreateRun 持续失败 → 重试恰好一次、结果照返、RunResultSchema.run_id=""、ERROR 日志带 trace_id
- [x] T023 [US3] GREEN internal/workflow/service/execute.go——CreateRun 失败重试一次仍败走降级分支；trace_id 从 ctx 取出落 run 行（platform/traceid 提取）
- [x] T024 [US3] RED internal/workflow/service/runscleaner_test.go——首轮立即执行 + ticker 周期、retention<=0 不启动（WARN）、DELETE 按 created_at 批次带 WHERE、appCtx 取消即退出（research R8）
- [x] T025 [US3] GREEN internal/workflow/service/runscleaner.go + internal/app/server.go——清理任务实现（PartitionMaintainer 形态）+ 组合根 `go cleaner.Start(appCtx)` 接线

**Checkpoint**: 全部故事独立可测；排障链完整（slog + executions + runs/node_runs + trace_id）。

---

## Phase 6: Polish & Cross-Cutting

- [x] T026 RED internal/workflow/service/service_test.go 增例——R10 保存期校验：llm.prompt/api.url+headers+body/end.output 引用祖先或 input 通过；引用非祖先 key → 400 VALIDATION_FAILED（details 带节点 key 与引用名）；condition `== 'literal'` 右侧字面量不查；纯线性图祖先链正确；与 {{}} 扫描共用 tokenizer
- [x] T027 GREEN internal/workflow/service/service.go——validateGraph 增条 11（R10）：祖先集沿 edges 反向 BFS，模板字段提取按 config 类型分发（db_model §7 条 11 逐字语义）
- [x] T028 [P] 文档同步——CLAUDE.md 错误码表增 `WORKFLOW_EXECUTION_FAILED | 500` 行 + 索引地图增 workflow_runs / workflow_node_runs 行；docs/design/data-model.md 增两表与弱引用关系；docs/testing/workflow-manual-test.md 增执行测试小节（quickstart §3 五场景）
- [x] T029 全量验收门——`go build ./... && go vet ./... && go test ./... -race -count=1` 全绿；`go test ./internal/workflow/... -race -cover` 各包 ≥80%；依赖 grep：`grep -rn "internal/chat" internal/workflow/` 零命中、api 包无 gin/gorm import；`make migrate-status` 19 条 applied 且 00001-00018 未动；对照 impl_spec_06 §7 关键回归点逐项勾验

---

## Dependencies & Execution Order

### Phase Dependencies

- Phase 1 → Phase 2（基线绿才动契约）；Phase 2 阻塞全部故事（T006/T008/T009 依赖 T003 类型；T010 依赖 T004 model；T002 独立可并行）
- Phase 3（US1）→ Phase 4（US2 在把关逻辑上叠加）→ Phase 5（US3 在 finishResult 上叠加）——本篇引擎同体，故事按优先级**串行**最稳（同一组合文件 execute.go 被三相触碰）
- Phase 6 收尾：T026/T027 与故事无文件冲突外的依赖（service.go 校验路径独立，但与 T017 同文件 → 放故事后）；T029 最后

### Within Each Story

- 测试（RED）先于实现（GREEN）；T006-T011 之间互不依赖可并行（T008/T009 共享 stub 设施，建议 T006/T007/T010/T011 并行、T008→T009 串行）
- 每任务收尾跑 `go build ./... && go vet ./... && go test ./... -race -count=1`（任务级 DoD）

### Parallel Opportunities

- Phase 2 全部 [P]（四文件互不相交）
- US1 测试相位：T006 ∥ T007 ∥ T010 ∥ T011
- T028 文档同步可与 Phase 5 并行

---

## Implementation Strategy

### MVP First（US1 only）

Phase 1 → 2 → 3 后停：published 图端到端可执行可验收——已是可演示增量（控制台执行 + 轨迹落库）；US2/US3 依次叠加，每相独立可测。

### Notes

- 全程不自动 commit——按 spec-dev 纪律，commit 时机由用户明示（任务勾选 ≠ 提交）
- 冻结契约偏差（类型名/签名/SQL/哨兵文案对不上）→ 停下报告（软门禁），不回改契约
- 「顺手改进」冲动记录待问，不直接做
