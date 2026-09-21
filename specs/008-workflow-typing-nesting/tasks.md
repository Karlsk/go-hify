# Tasks: workflow 分型与子工作流嵌套（chat/task 两型 + sub-workflow 节点）

**Input**: Design documents from `/specs/008-workflow-typing-nesting/`

**Prerequisites**: plan.md (required), spec.md (required), research.md, data-model.md, contracts/api.md

**Tests**: 测试任务必含（spec §7 验收门明确表驱动用例清单）；严格 TDD——每个测试任务先跑确认 RED，再实现 GREEN。

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g. US1, US2, US3)
- Include exact file paths in descriptions

## Path Conventions

- 后端模块代码：`internal/workflow/{api,service,store,handler}/`（同包 `*_test.go`）
- 迁移：`migrations/`；文档：`docs/`、`CLAUDE.md`

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: 迁移与数据基座——全部故事的 blocking 前提

- [X] T001 编写迁移 `migrations/00020_workflow_typing_nesting.sql`：workflows 加 `type`（text NOT NULL CHECK (type IN ('chat','task'))，存量回填 'chat'）+ `input_schema` / `output_schema` jsonb 可空 + COMMENT ON；`workflow_nodes_type_check` 约束重建加 'workflow'（00017 同名模式，Down 反向）；`workflow_runs_trigger_source_check` 约束重建加 'workflow'（00019 内联 CHECK 的 PG 自动命名，Drop + Add）+ `parent_run_id` bigint 可空 + COMMENT ON。写完 `make migrate-status` 核对 20 条 applied（本地 dev 库）
- [X] T002 [P] `internal/workflow/service/model.go`：Workflow 增 `Type` / `InputSchema` / `OutputSchema` 字段（jsonb 列，GORM tag；model 不打 json tag）
- [X] T003 [P] `internal/workflow/api/schema.go`：`WorkflowTypeChat` / `WorkflowTypeTask` 常量、`NodeTypeWorkflow = "workflow"` 常量、`SchemaField` 类型（name/type/required/description）及其形态校验 helper（重名 / type ∈ string|number|boolean）

**Checkpoint**: 列与类型就位，用户故事可开始。

---

## Phase 2: User Story 1 - 保存期：分型 + sub-workflow 节点 + R11 嵌套校验 (Priority: P1) 🎯 MVP

**Goal**: 管理员能创建 task 型工作流（声明 schema）并在父图保存 sub-workflow 节点；五类非法嵌套在保存期被 400 拦截。

**Independent Test**: `go test ./internal/workflow/... -race`——api 解析 / service 分型 CRUD / R11 矩阵（引 chat 拒、自嵌拒、A→B→A 拒、链深超 3 拒、引用不存在拒、chat⊃task 与 task⊃task 过）全绿。

### Tests for User Story 1 (TDD - 先 RED)

- [X] T004 [P] [US1] `internal/workflow/api/schema_test.go`（或既有测试文件追加）：WorkflowNodeConfig 解析（合法 config / workflow_id 缺失 / inputs 非映射拒）、UpsertReq.Type 必填与 oneof、SchemaField 形态校验（重名拒 / 非法 type 拒）
- [X] T005 [P] [US1] `internal/workflow/service/service_test.go`：分型 CRUD 表驱动——Create type 必填落库、chat 型携带非空 schema 拒（clarify 拍板）、task 型 schema 持久化与回读
- [X] T006 [US1] `internal/workflow/service/service_test.go`：R11 矩阵表驱动——五拒两过（stub Store 提供被引图 fixture；无 schema 子图回退恰 `{input}`）

### Implementation for User Story 1

- [X] T007 [US1] `internal/workflow/api/schema.go`：WorkflowNodeConfig 密封实现 + ParseNodeConfig 分发 'workflow'；UpsertReq 增 Type / InputSchema / OutputSchema 字段与 Validate 规则（T004 GREEN）
- [X] T008 [US1] `internal/workflow/service/service.go`：toModel / toSchema 增三字段转换；schema 形态校验接入 Create 路径；chat 型 schema 强不变量（T005 GREEN）
- [X] T009 [US1] `internal/workflow/store/store.go` + `store_test.go`：type / schema 列读写（Create / Get / List / Update SELECT 显式列）+ 被引 workflow 直查（GetByID 供 R11 用）；sqlmock 断言 SQL 形态
- [X] T010 [US1] `internal/workflow/service/service.go`：R11 保存期校验（①存在性 store 直查 ②task 型 ③DFS 环检测——链上出现被保存图自身 id 拒、visited 防重复展开 ④链深 ≤3 ⑤inputs 键集覆盖）；接入 Create 与 Update 整图提交路径（T006 GREEN）
- [X] T011 [US1] `internal/workflow/handler/handler.go` + `handler_test.go`：既有 create / update 绑定函数适配 type 字段；R11 / schema 校验失败走既有 VALIDATION_FAILED 400 分支；httptest 断言

**Checkpoint**: US1 独立可测——保存期拦截全量落地。

---

## Phase 3: User Story 2 - 执行期：executeChild 递归 + 结构化契约 + 子 run 轨迹 (Priority: P2)

**Goal**: 嵌套图可执行——inputs 渲染、JSON 组装、子图把关与隔离、output 校验落父池、子 run 行 + parent_run_id 回填。

**Independent Test**: `go test ./internal/workflow/... -race`——executor / execute / execcontext 新用例全绿（trial 跟随 / 正式子 draft 拒 / 深度兜底 / trigger_source='workflow' / 引用透传 / 池下钻 / 子池隔离 / 前缀链）。

### Tests for User Story 2 (TDD - 先 RED)

- [X] T012 [P] [US2] `internal/workflow/service/execcontext_test.go`：池一级下钻表驱动——`{{input.x}}` / `{{node.field}}`（池值 JSON 时取字段）、string 值行为不变、深度一层为止、字段缺失运行期 strict
- [X] T013 [P] [US2] `internal/workflow/service/execute_test.go`：executeChild 表驱动——trial 跟随（父 trial 子 draft 放开）、正式子 draft 拒（NotPublished 带父 node 前缀）、深度计数超限图缺陷 400、trigger_source='workflow' + conversation_id / message_id 透传、子 run 写失败重试一次降级、父写失败跳过 parent_run_id 回填
- [X] T014 [US2] `internal/workflow/service/executor_test.go`：runNode workflow 分支表驱动——inputs 逐值渲染（strict）、JSON 文本组装（number / boolean 类型转换）、子终稿落父池 node_key、下游模板可引、output schema 校验失败 400 带父前缀、声明 schema 终稿非 JSON 拒、子图错误前缀链 `node a: node b:`、子池隔离（子图引用父 vars 报缺失）
- [X] T015 [P] [US2] `internal/workflow/store/store_test.go`：UpdateParentRunIDs sqlmock——`UPDATE workflow_runs SET parent_run_id WHERE id = ANY($1)` 批量一次窄写

### Implementation for User Story 2

- [X] T016 [US2] `internal/workflow/service/execcontext.go`：渲染器一级下钻（基名解析后池值为合法 JSON 对象且含字段则取值；string 不变；深度一层）（T012 GREEN）
- [X] T017 [US2] `internal/workflow/service/execute.go`：executeChild（把关 trial 跟随 / published；`WithTimeoutCause(5min)` 每层自包；子 vars 池全新起步只含自身 input；同 goroutine 共享父 ctx；深度计数传递超限拒）+ buildRun trigger_source 显式传参（console / chat / workflow——O7b 透传后 ConversationID 判定退役，research D2）+ 子 run id 累积 + 入口结构化入参检测（合法 JSON 对象且声明 input_schema → 解析入池，否则整串落 input）（T013 GREEN）
- [X] T018 [US2] `internal/workflow/store/store.go`：UpdateParentRunIDs（按父 run id 批量 UPDATE，DML 带 WHERE、参数类型 bigint[]）（T015 GREEN）
- [X] T019 [US2] `internal/workflow/service/executor.go`：runNode 增 'workflow' 分支——渲染 inputs → 按子 input_schema 组装 JSON 文本 → executeChild → output schema 校验 → 落父变量池 node_key；错误统一过既有 `node %s: %w` 前缀包装点（T014 GREEN）
- [X] T020 [US2] 父收尾回填接线：父 run 行落库（既有 WithoutCancel 路径）后调 UpdateParentRunIDs；父写失败（重试后仍败）跳过回填 trace_id 兜底

**Checkpoint**: US1 + US2 联合可测——嵌套执行与轨迹全量落地。

---

## Phase 4: User Story 3 - 管理面：Update 拒改 + 存量回填 + 管道回归 (Priority: P3)

**Goal**: 分型管理语义闭环——Update 携带 type 即 400、存量回填 chat 可见、Get/List 暴露 type、spec 07 管道零回归。

**Independent Test**: handler httptest Update 携带 type（同值 / 异值）均 400；Get/List 返回 type；`go test ./... -race` 全绿（chat 模块零改动回归）。

### Tests for User Story 3 (TDD - 先 RED)

- [X] T021 [P] [US3] `internal/workflow/api/schema_test.go` + `internal/workflow/service/service_test.go`：UpdateWorkflowReq 携带 Type（同值 / 异值）→ errs.ErrValidationFailed（clarify 拍板：携带即拒，不比对当前值）
- [X] T022 [P] [US3] `internal/workflow/handler/handler_test.go`：update 绑定函数携带 type → 400 VALIDATION_FAILED 信封；get / list 响应含 `type`（及 schema 字段）

### Implementation for User Story 3

- [X] T023 [US3] `internal/workflow/api/schema.go` + `internal/workflow/service/service.go`：UpdateWorkflowReq 增 `Type *string` 携带即拒 + Update 路径 guard（T021 GREEN）
- [X] T024 [US3] `internal/workflow/handler/handler.go`：update 绑定函数拒改映射（既有 VALIDATION_FAILED 分支覆盖，确认无新哨兵）（T022 GREEN）
- [X] T025 [US3] 回归验证：`go build ./... && go vet ./... && go test ./... -race -count=1` 全绿（含 spec 06 引擎既有用例 + spec 07 chat 管道用例——分型不 gate 绑定零行为改动）；依赖方向 grep（`internal/workflow/` 无 `internal/chat` import；api 包无 gin / gorm）

**Checkpoint**: 全部故事独立可测——功能面收口。

---

## Phase 5: Polish & Cross-Cutting Concerns

**Purpose**: 文档同步、覆盖率、人工验收清单

- [X] T026 文档同步：`CLAUDE.md`（错误码表零新行注记 + 索引地图 workflows / workflow_runs 相关行）、`docs/design/data-model.md`（type + workflows 自引用弱引用关系）、`docs/changelog/workflow/db_model.md`（§7 R11 条 12 + 决策 #16：分型 / 嵌套矩阵 / parent_run_id append-only 例外）、`docs/changelog/workflow/api_contract.md`（type 字段 + 节点类型清单 + 嵌套语义）、`docs/testing/workflow-engine-manual-test.md`（§7 嵌套冒烟：两型创建 / 嵌套保存矩阵 / 嵌套执行 / psql 查 runs 树）
- [X] T027 覆盖率核验：`go test ./internal/workflow/... -race -count=1 -cover` 各包 ≥80%（api 97.5% / handler 83.9% / service 88.3% / store 96.6%）；不足补测（禁删断言 / t.Skip / 调门槛）
- [X] T028 迁移只增不改核验：`git status --porcelain migrations/` 仅新增 00020；`make migrate-status` 20 条 applied
- [X] T029 quickstart.md 场景走查准备 + 剩余人工项清单输出（真 provider 嵌套执行冒烟、psql 树查询——归用户人工验收）

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: 无依赖，立即开始；T002 / T003 可并行（不同文件）
- **US1 (Phase 2)**: 依赖 Phase 1（列与常量就位）
- **US2 (Phase 3)**: 依赖 US1（R11 与 workflow 节点解析是执行分支的前置）
- **US3 (Phase 4)**: 依赖 US1（Update 路径已接 R11 后再加拒改 guard）；回归验证依赖 US1+US2 完成
- **Polish (Phase 5)**: 依赖全部故事完成

### Within Each User Story

- 测试任务先跑确认 RED（新符号未定义的编译失败是 Go 正常 RED）
- api → service → store → handler 层序；同文件任务串行

### Parallel Opportunities

- T002 / T003（model.go 与 api/schema.go 不同文件）
- T004 / T005（api 与 service 测试不同包）；T012 / T013 / T015（execcontext / execute / store 测试不同文件）
- T021 / T022（api+service 与 handler 测试）

---

## Implementation Strategy

### MVP First (User Story 1 Only)

Phase 1 → US1 完成即得到「保存期拦截」独立价值（非法嵌套图进不了库）；US2 加执行；US3 加管理面闭环与回归；Phase 5 收口文档与证据。

### Notes

- 冻结契约逐字保真：哨兵文案 = `error.code`（四类复用零新增）；Execute 签名不动；R11 / executeChild 语义以 impl_spec_08 §4 为准
- 每任务 DoD：`go build ./... && go vet ./... && go test ./... -race -count=1` 过门禁再勾
- 全程不自动 commit——攒到 spec「可提交节点」由用户明示
- 范围红线：只做本清单内容；「顺手改进」记下问用户
