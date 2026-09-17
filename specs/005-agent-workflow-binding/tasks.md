# Tasks: Agent → Workflow 绑定（agent-workflow-binding）

**Input**: Design documents from `/specs/005-agent-workflow-binding/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/api-contract.md, quickstart.md

**Tests**: TDD 强制（spec-dev 总纪律 + impl_spec_05 §6 TDD 粒度）——每个实现任务先 RED 后 GREEN。

**Organization**: 按用户故事分阶段；阶段内任务与 [impl_spec_05 §6](../../docs/changelog/workflow/impl_spec_05_agent_binding.md) T1-T8 一一对应（映射标注在任务内）。

**冻结契约**: 一律以 impl_spec_05 §4 为准（类型名、签名、SQL、哨兵文案 = error.code），引用条目用 spec 编号。

## Format: `[ID] [P?] [Story] Description`

- **[P]**: 可并行（不同文件、无未完成依赖）
- **[Story]**: 所属用户故事（US1/US2/US3）

## Path Conventions

单仓 Go 模块化单体：`internal/<module>/{api,service,store,handler}`，迁移 `migrations/`，测试与被测代码同包 `*_test.go`。

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: 开工前只读核对（无代码产出）

- [x] T001 开工前核对：`make migrate-status` 确认 17 条 applied、下一号 00018（hify-pg-test 容器 5433 在位）；`go build ./... && go vet ./... && go test ./... -race -count=1` 基线绿

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: 三故事共同依赖的物理列（impl spec T1）

**⚠️ CRITICAL**: 列不存在则 US1/US2/US3 全部无从落地

- [x] T002 [impl T1] 新建 `migrations/00018_agent_workflow_binding.sql`：SQL 逐字对齐 impl_spec_05 §4.1（加列 `workflow_id bigint` + `fk_agents_workflow` RESTRICT + `idx_agents_workflow_id` + `COMMENT ON`，Up/Down 成对）；`make migrate-up && make migrate-status` 确认 18 条 applied

**Checkpoint**: 物理列就位，用户故事实现可开始

---

## Phase 3: User Story 1 - 绑定/解绑/回显 (Priority: P1) 🎯 MVP（与 US2 合为 agent 侧完整增量）

**Goal**: `POST/PUT` 携带 `workflow_id` 完成绑定，PUT 缺省/null 解绑，GET 回显 null/字符串两态。

**Independent Test**: `go test ./internal/agent/api/ ./internal/agent/service/ ./internal/agent/store/ -race` 全绿；序列化 round-trip 与转换用例通过。

### Tests for User Story 1

> **NOTE: Write these tests FIRST, ensure they FAIL before implementation**

- [x] T003 [US1] [impl T2-RED] `internal/agent/api/schema_test.go` 补用例：`workflow_id` 序列化 null / "3" 两态（Req 与 Schema）；binding `gt=0` 传 0 → 绑定校验拒绝；`Validate` 行为不受影响（impl spec §8 第 1 条）
- [x] T004 [US1] [impl T3-RED/1] `internal/agent/service/service_test.go` 补用例：Create/Update 带 `WorkflowID` 落 model 转换；`toSchema` 指针字符串化 null/值两态；PUT `null` 解绑 → model 字段回 nil
- [x] T005 [US1] [impl T4-RED] `internal/agent/store/store_test.go` 补用例：`selectAgent` 期望 SQL 断言含 `workflow_id` 列；CreateAgent/UpdateAgent 语句参数含新列（sqlmock）

### Implementation for User Story 1

- [x] T006 [US1] [impl T2-GREEN] `internal/agent/api/schema.go`：`CreateAgentReq`/`UpdateAgentReq` 增 `WorkflowID *uint64`（`json:"workflow_id" binding:"omitempty,gt=0"`，置于 RAGMinSimilarity 后，两处同款）；`AgentSchema` 增 `WorkflowID *string`；`validateAgent` 签名与规则不动（§4.2 逐字）
- [x] T007 [US1] [impl T3-GREEN/1] `internal/agent/service/model.go` `Agent` 增 `WorkflowID *uint64`（RAGMinSimilarity 后）；`internal/agent/service/service.go` `toModelCreate`/`applyUpdate` 各加一行直赋、`toSchema` 尾部按 `FallbackModelID` 同款指针字符串化（§4.3）
- [x] T008 [US1] [impl T4-GREEN] `internal/agent/store/store.go` `selectAgent` 列清单加 `workflow_id`（§4 交付物 4）

**Checkpoint**: 绑定/解绑/回显链路完整（此时 404 翻译仍是旧逻辑——US2 修正）

---

## Phase 4: User Story 2 - 绑定不存在 workflow 得到 404 (Priority: P2)

**Goal**: 23503 按约束名分发——`fk_agents_workflow` → `agentapi.ErrWorkflowNotFound` 404；model 约束保持 `ErrModelNotFound`（impl spec §4.4 核心行为变更）。

**Independent Test**: `go test ./internal/agent/service/ ./internal/agent/handler/ -race` 分发表驱动全绿；httptest 断言 404 信封。

### Tests for User Story 2

- [x] T009 [US2] [impl T3-RED/2] `internal/agent/service/service_test.go` 补用例：23503 约束名分发表驱动（`fk_agents_workflow` → ErrWorkflowNotFound；`agents_model_id_fkey` → ErrModelNotFound；无约束名 / 非 23503 → 原样包装）；既有 model FK 用例 fixture 补 `ConstraintName` 字段
- [x] T010 [US2] [impl T5-RED] `internal/agent/handler/handler_test.go` httptest 表驱动补一行：ErrWorkflowNotFound → 404 信封

### Implementation for User Story 2

- [x] T011 [US2] [impl T3-GREEN/2] `internal/agent/api/errors.go` 增哨兵 `ErrWorkflowNotFound = errors.New("WORKFLOW_NOT_FOUND")`（注释照 §4.2：与 workflowapi 同码各持一份，KB 先例）；`internal/agent/service/service.go` 增 `fkAgentsWorkflow` 常量 + `translateAgentFK`（§4.4 逐字），替换 Create/Update 事务内 `CreateAgent`/`UpdateAgent` 两处旧 `isFKViolation → ErrModelNotFound`（Tools/KBs 语句翻译不动，`isFKViolation` 保留）
- [x] T012 [US2] [impl T5-GREEN] `internal/agent/handler/handler.go` `failAgent` switch 加 `case errors.Is(err, agentapi.ErrWorkflowNotFound):` → 404（与 ErrToolNotFound / ErrKnowledgeBaseNotFound 同组）

**Checkpoint**: agent 侧完整增量闭环（US1+US2 = spec 05 交付物 1-5）

---

## Phase 5: User Story 3 - 被绑定的 workflow 删除被拦截 409 (Priority: P3)

**Goal**: workflow 侧删除互锁——`ErrWorkflowInUse` 409（与 agent 侧文件零交集，仅共享 T002 迁移）。

**Independent Test**: `go test ./internal/workflow/... -race`：stub 返 23503 → ErrWorkflowInUse；handler 409 信封 `error.code = "WORKFLOW_IN_USE"`；正常删除回归绿。

### Tests for User Story 3

- [x] T013 [US3] [impl T6-RED] `internal/workflow/service/service_test.go` 补用例：store stub 返 `*pgconn.PgError{Code:"23503"}` → ErrWorkflowInUse；正常删除路径回归不受影响；`internal/workflow/handler/handler_test.go` 补 409 信封用例

### Implementation for User Story 3

- [x] T014 [US3] [impl T6-GREEN] `internal/workflow/api/errors.go` 增 `ErrWorkflowInUse = errors.New("WORKFLOW_IN_USE")`（§4.5 逐字）；`internal/workflow/service/service.go` 增 `pgCodeFKViolation = "23503"` 常量 + `isFKViolation` helper（agent/provider 同款）+ `Delete` 的 `err != nil` 分支最前翻译；`internal/workflow/handler/handler.go` `failWorkflow` 加 409 映射

**Checkpoint**: 三故事全部独立可测、闭环

---

## Phase 6: Polish & Cross-Cutting Concerns

- [x] T015 [P] [impl T7] 文档同步（§7 逐条）：`CLAUDE.md` 错误码表 `WORKFLOW_NOT_PUBLISHED` 行后加 `WORKFLOW_IN_USE` 409 行、`WORKFLOW_NOT_FOUND` 行补 agentapi 同码注（照 KNOWLEDGE_BASE_NOT_FOUND 行格式）、索引地图 `agents` 行加 `(workflow_id)`；`docs/design/data-model.md` agents 表 + `workflow_id` 列、关系清单加 `agents N──1 workflows # ON DELETE RESTRICT（spec 05）`；`docs/changelog/workflow/db_model.md` 决策表加 #11（RESTRICT + 双向哨兵 + B2/C2 拍板结论与日期）
- [x] T016 [P]（可选，impl T8）`docs/testing/agent-manual-test.md` 补「workflow 绑定」小节（绑/解绑/404/409 冒烟 curl）
- [x] T017 spec 级验收（quickstart.md 命令块全跑）：`go build ./... && go vet ./... && go test ./... -race -count=1`；`go test ./internal/agent/... ./internal/workflow/... -race -cover` 两模块各 ≥80%；`grep -rn "internal/workflow" internal/agent/` 无输出；`make migrate-status` 18 applied；`git status --porcelain migrations/` 仅新增；§7 文档逐条核对

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: 无依赖，立即开始
- **Foundational (Phase 2 = T002 迁移)**: 依赖 T001；**阻塞全部用户故事**
- **US1 → US2 强顺序**：同文件 `internal/agent/service/service.go`（US2 的 translateAgentFK 替换点在 US1 改过的 Create/Update 路径上），顺序执行 US1 再 US2
- **US3 独立于 US1/US2**：只碰 `internal/workflow/` 三文件，可在 T002 后并行推进（单人开发按 T 顺序串行即可）
- **Polish (Phase 6)**: 依赖全部用户故事完成

### Within Each User Story

- 测试任务（RED）先于对应实现任务（GREEN），GREEN 前确认 RED 失败（新符号未定义导致的编译失败是 Go 的正常 RED）
- 每任务完成即过门禁：`go build ./... && go vet ./... && go test ./... -race -count=1`，红了当场修

### Parallel Opportunities

- T015 与 T016（不同文档文件）可并行
- US3（workflow 模块）与 US1/US2（agent 模块）文件零交集，多人时可并行

---

## Implementation Strategy

### MVP First（US1 + US2 = agent 侧完整增量）

1. T001 → T002（列就位）
2. US1（T003-T008）→ 独立验证绑定/解绑/回显
3. US2（T009-T012）→ 修正 404 翻译，agent 侧闭环
4. US3（T013-T014）→ workflow 侧互锁
5. Phase 6（T015-T017）→ 文档 + spec 级验收

> MVP 边界说明：模板默认 MVP = US1 单故事；本特性 US1 单独交付会留**错误哨兵回归**（workflow FK 撞 23503 被旧逻辑译成 `ErrModelNotFound`，impl spec §4.4 明确此为核心行为变更），故 MVP 取 US1+US2 合并闭环。

### Incremental Delivery

US1+US2（agent 侧）→ US3（workflow 侧）→ Polish；每个 Checkpoint 独立可测，不破坏前序故事。

---

## Notes

- 冻结契约逐字对齐 impl_spec_05 §4；引用条目用 spec 编号（如「§4.4」）不自造同义词
- 仓规红线每任务对照：agent 禁 import workflow、service 无 gin、迁移只增不改、禁 SELECT *（selectAgent 显式列）、错误链路三层不吞错
- 全程不自动 commit / push；可提交节点与建议 message 见 impl_spec_05 §10
- 顺手改进冲动记录待问，不直接做
