---
description: "Task list: workflow 运行历史与节点轨迹查询"
---

# Tasks: workflow 运行历史与节点轨迹查询

**Input**: Design documents from `/specs/015-workflow-run-history/`

**Prerequisites**: plan.md（模块落位/宪法检查/store 包头注释修正注记）、spec.md（US1~US3 + FR-001~008 + SC-001~005）、research.md（D1~D7 冻结决策）、data-model.md（两实体查询面映射）、contracts/api.md + contracts/frontend.md、quickstart.md

**Tests**: 后端 TDD 强制（spec 验收标准：go test 全绿）——测试任务先于对应实现任务，逐层 RED→GREEN（每层 GREEN 收口全量门禁绿）；前端无单测基建，走 type-check && build 双门禁 + manual-test §11 人工验收。

**Organization**: 按 spec 用户故事分组（US1 列表查询 P1 / US2 详情与轨迹 P2 / US3 联动与数据窗口 P3）。

## Format: `[ID] [P?] [Story] Description`

- **[P]**: 可并行（不同文件、无未完成依赖）
- **[Story]**: 所属用户故事（US1/US2/US3）；Setup/Foundational/Polish 无故事标签

## Path Conventions

单仓库：后端 `internal/workflow/`（四层同包 *_test.go）；前端 `web/src/`；文档 `docs/`。

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: 开工基线确认（既有仓库，无结构初始化）

- [ ] T001 基线与不变量确认：`go build ./... && go vet ./... && go test ./... -race -count=1` 全绿 + `make migrate-status` 20 条 applied、下一号 00021（本篇零迁移，收尾时该状态须不变）+ 依赖方向基线 `grep -r 'internal/chat' internal/workflow/` 零命中
- [ ] T002 既有路由回归基线盘点：`go test ./internal/workflow/handler/... -race -count=1` 确认既有 8 路由用例在场全绿（SC-005 存量回归的自动化守护面，本篇零改动它们）

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: api 契约基座——US1（列表 schema）/ US2（详情 schema + 哨兵）共同依赖；纯类型零实现，不触 api.go 接口方法（接口方法与 service 实现绑定，留在各故事阶段避免编译断）

- [ ] T003 RED：internal/workflow/api/schema_test.go 扩——五类型契约面断言：ListRunsReq form tag（limit/cursor）；RunSummarySchema 恰好 9 字段且 JSON 序列化无 `input`/`output` 键（FR-002）+ id 字符串化；GetRunReq 两路由参数；RunDetailSchema 全字段（可空 id 三指针 `*string`：conversation_id/message_id/parent_run_id + nodes 嵌套）；NodeRunSchema 字段面（含恒空 error_msg 契约位）；ErrRunNotFound 存在且 Error() == "RUN_NOT_FOUND"（RED 确认：类型与哨兵未定义编译失败）
- [ ] T004 GREEN：internal/workflow/api/errors.go 增 `ErrRunNotFound = errors.New("RUN_NOT_FOUND")`（大写码 + 中文注释，对齐本文件 5 哨兵形态）+ api/schema.go 增五类型（对齐 contracts/api.md Go 形态逐字段）——api 包全绿 + 全量门禁绿（纯类型零实现零行为）

**Checkpoint**: 查询契约面就位，两故事可开工。

---

## Phase 3: User Story 1 - 运行历史列表 (Priority: P1) 🎯 MVP

**Goal**: `GET /workflows/:id/runs` 全链路（游标 keyset 列表 + 摘要面无大文本）+ 前端详情页列表区块

**Independent Test**: 对执行过若干次的工作流调列表接口：首页 20 条最新在前、翻页无重漏、limit 归一、篡改 cursor 400、列表项无 input/output 键

### Tests for User Story 1 (TDD——先写并确认 RED)

- [ ] T005 [US1] RED：internal/workflow/store/store_test.go 扩——ListRuns sqlmock 断言：SELECT 列 = selectRunSummary 摘要清单（**不含** input/output——D4）；WHERE workflow_id = ? 恒在；`(created_at, id) < (?, ?)` 行值比较仅翻页出现、首页（IsZero）跳过；ORDER BY created_at DESC, id DESC；LIMIT = FetchN()（n+1）（RED 确认：store.ListRuns 未定义编译失败）
- [ ] T007 [US1] RED：internal/workflow/service/service_test.go 扩——ListRuns stub Store 断言：游标解码失败 → `errors.Is(err, errs.ErrValidationFailed)` 且包装含 cursor 上下文（D7）；limit ≤0/>100 归一为 20/100 透传 store；首页零值 cursor 跳过条件；stub 返 21 条 → Items 20 + HasMore true + NextCursor = 末行 key；stub 返 0 条 → RunListResult.Items 为 `[]` 非 nil（空值约定）；toRunSummary 字段面（含 started_at/created_at 双时间、id 字符串化）（RED 确认：接口方法未定义编译失败）
- [ ] T009 [US1] RED：internal/workflow/handler/handler_test.go 扩——GET /workflows/1/runs：200 信封 data.items + meta{limit,has_more,next_cursor}（OKWithCursor 5 参）；?limit=999 → meta.limit=100、?limit=-5 → 20（归一）；?cursor=篡改 → 400 VALIDATION_FAILED；空列表 → data.items == []；既有 8 路由用例零改动全绿（SC-005）

### Implementation for User Story 1

- [ ] T006 [US1] GREEN：internal/workflow/store/store.go——增 `selectRunSummary` 显式列常量（id, workflow_id, status, trigger_source, is_trial, duration_ms, error_node, error_msg, started_at, created_at——无 input/output）+ `ListRuns(ctx, workflowID, beforeCreatedAt, beforeID, limit)` 实现（Where workflow_id + IsZero 跳行值比较 + Order + Limit，对齐 chat store 先例）+ **包头注释修正**：三张表 → 五张表（workflows/workflow_nodes/workflow_edges/workflow_runs/workflow_node_runs，plan 注记项）——全量门禁绿（依赖 T005）
- [ ] T008 [US1] GREEN：internal/workflow/api/api.go 增 `ListRuns(ctx, ListRunsReq) (*RunListResult, error)` 接口方法（doc 注释列错误哨兵，对齐本接口既有风格）+ internal/workflow/service/service.go——Store 接口增 ListRuns（T006 签名）+ 私有 `runCursorKey{CreatedAt, ID}`（对齐 chat convCursorKey）+ ListRuns 实现（page.NewCursor → DecodeCursor 失败 `fmt.Errorf("%w: cursor: %v", errs.ErrValidationFailed, err)` → store → page.NewCursorResult → RunListResult 组装 make 兜底）+ toRunSummary 转换——全量门禁绿（依赖 T007）
- [ ] T010 [US1] GREEN：internal/workflow/handler/handler.go——RegisterRoutes 增 `g.GET("/:id/runs", h.listRuns)` + listRuns 绑定函数（路由 id strconv 注入 req.WorkflowID + respond.BindQuery + respond.OKWithCursor 5 参 + respond.FailFromSentinel 兜底）——全量门禁绿 + `go test ./internal/workflow/... -race -cover` 各包 ≥80%（依赖 T009）
- [ ] T011 [P] [US1] web/src/api/workflow.ts——增 RunSummary 接口类型（contracts/frontend.md 形态）+ `listWorkflowRuns(id, params?)` 走既有 getCursorList（chat.ts 同款游标约定）
- [ ] T012 [US1] web/src/views/workflow/WorkflowRunsPanel.vue 新增——「运行历史」卡片：挂载拉首页 + 列表 el-table（状态 tag 成功/危险两态、触发来源中文映射、is_trial tag、耗时格式、失败节点/错误摘要空显 —、调用时间 started_at、运行编号等宽）+ 加载更多（hasMore 控制、请求中 loading 防重）+ el-empty 空态 + 保留期说明文案；web/src/views/workflow/WorkflowDetail.vue 图编排卡片后挂 `<WorkflowRunsPanel :workflow-id="detail.id" />` + import（其余零改动，FR-006）（依赖 T011）
- [ ] T013 [US1] 前端双门禁 `cd web && npm run type-check && npm run build` 全绿 + 冒烟转人工项登记（列表渲染 / 翻页 / 空态 → manual-test §11）（依赖 T012）

**Checkpoint**: US1 独立可验——curl 列表 + 详情页区块列表可视。

---

## Phase 4: User Story 2 - 运行详情与节点轨迹 (Priority: P2)

**Goal**: `GET /workflows/:id/runs/:runId` 全链路（404 不泄露 + 全字段 + 轨迹按执行序）+ 前端运行详情抽屉

**Independent Test**: 成功/失败各一次运行后调详情接口：全字段 + nodes 按执行序、失败运行 error_node/error_msg 可读、不存在与跨工作流同判 404 RUN_NOT_FOUND

### Tests for User Story 2 (TDD——先写并确认 RED)

- [ ] T014 [US2] RED：internal/workflow/store/store_test.go 扩——GetRunByID sqlmock 断言：SELECT 列 = selectRun 全列（含 input/output）+ WHERE `workflow_id = ? AND id = ?` 双条件（D3——不存在与跨工作流同判）；ListNodeRuns：WHERE run_id = ? + ORDER BY seq ASC + selectNodeRun 列（RED 确认：两方法未定义编译失败）
- [ ] T016 [US2] RED：internal/workflow/service/service_test.go 扩——GetRun stub Store 断言：GetRunByID 返 gorm.ErrRecordNotFound → errors.Is(err, ErrRunNotFound)（跨工作流与不存在同判——stub 只有一种 NotFound，语义由 store 双条件保证）；run + nodes 两查询组装（nodes 进 detail.nodes）；空轨迹 → nodes 为 `[]` 非 nil；toRunDetail 全字段转换（三可空 id nil→null / 有值→字符串指针；input/output 原样透传含截断标记；trace_id）；toNodeRun 字段面（seq/node_key/node_type/status/input/output/error_msg/duration_ms）（RED 确认：接口方法未定义编译失败）
- [ ] T018 [US2] RED：internal/workflow/handler/handler_test.go 扩——GET /workflows/1/runs/999：404 信封 error.code == "RUN_NOT_FOUND" + 中文 message；GET 存在 runId：200 data 全字段 + nodes 数组；runId 非数字 → 400；既有路由零回归

### Implementation for User Story 2

- [ ] T015 [US2] GREEN：internal/workflow/store/store.go——增 `selectRun`（model 全列）/ `selectNodeRun` 列常量 + GetRunByID（双条件）+ ListNodeRuns（seq ASC）实现——全量门禁绿（依赖 T014）
- [ ] T017 [US2] GREEN：internal/workflow/api/api.go 增 `GetRun(ctx, GetRunReq) (*RunDetailSchema, error)` 接口方法 + internal/workflow/service/service.go——Store 接口增 GetRunByID / ListNodeRuns + GetRun 实现（run 查询 → gorm.ErrRecordNotFound 翻译 ErrRunNotFound → ListNodeRuns 分次查询组装、make 兜底）+ toRunDetail / toNodeRun 转换——全量门禁绿（依赖 T016）
- [ ] T019 [US2] GREEN：internal/workflow/handler/handler.go——RegisterRoutes 增 `g.GET("/:id/runs/:runId", h.getRun)` + getRun 绑定函数（两路由参数 strconv 注入 + respond.OK + `errors.Is(err, api.ErrRunNotFound)` → respond.Fail(404, 哨兵.Error(), "运行记录不存在") + FailFromSentinel 兜底）——全量门禁绿 + workflow 覆盖率 ≥80% 维持（依赖 T018）
- [ ] T020 [P] [US2] web/src/api/workflow.ts——增 NodeRunTrack / RunDetail 接口类型 + `getWorkflowRun(id, runId)` 走既有 get
- [ ] T021 [US2] web/src/views/workflow/WorkflowRunsPanel.vue 扩——行点击开 el-drawer 运行详情：按需拉 getWorkflowRun（抽屉 loading / 拉取失败抽屉内错误态）；运行级信息（状态/触发来源/试运行/总耗时/started_at/created_at/父运行编号非空展示/会话消息关联 chat 触发且非空展示）；失败面（error_msg + error_node 危险色）；输入输出 pre-wrap 限高滚动如实展示截断文本；节点轨迹 el-table（seq 升序 + 状态 tag + 输入输出截断显示 + 空值 — + 空轨迹空态文案 + 失败行高亮 = row.node_key === run.error_node 且 status=failed）；404 RUN_NOT_FOUND 抽屉内「该运行记录已不存在」（同文件依赖 T020、T012）
- [ ] T022 [US2] 前端双门禁全绿 + US2 checkpoint（curl 详情 + 抽屉轨迹可视）（依赖 T021）

**Checkpoint**: US2 独立可验——排障动线（列表 → 详情 → 失败节点高亮）闭环。

---

## Phase 5: User Story 3 - 试运行联动与数据窗口 (Priority: P3)

**Goal**: 试运行后可刷新见新记录 + 保留期窗口预期管理（零新端点零后端改动）

**Independent Test**: 详情页试运行一次 → 点运行历史「刷新」→ 顶部出现该记录带试运行标识 → 点开与当次结果一致；空/变短列表展示空态无错误

- [ ] T023 [US3] web/src/views/workflow/WorkflowRunsPanel.vue 扩——卡片头右侧「刷新」按钮（自治重拉首页、loading 防重；TrialDialog 零变化约束下的联动方案，见 contracts/frontend.md 区块结构）；重拉后列表回到首页态（游标重置）
- [ ] T024 [US3] US3 验收复核：`go test ./... -race -count=1` 全绿 + 前端双门禁全绿 + quickstart 场景 D 步骤 2 语义对照（试运行 → 刷新 → 顶部记录 + is_trial；关闭对话框后再点开核对输入输出与轨迹）——后端零改动零新测试为预期态（数据窗口语义是展示与文案层，已在 T012 空态/说明文案覆盖）

**Checkpoint**: 三故事全部独立可验。

---

## Phase 6: Polish & Cross-Cutting Concerns

- [ ] T025 [P] docs/changelog/workflow/api_contract.md——spec 015 追加注记（spec 011/014 同款 `〔2026-09-24 追加（spec 015）〕` 格式）：§1 路由总表增 2 行（GET runs 列表 / GET runs 详情）+ §5 后运行查询行为明细（分页语义/摘要面/404 语义，对齐 contracts/api.md）+ §8 错误码汇总增 RUN_NOT_FOUND 行 + §9 前端对接要点补游标回传约定
- [ ] T026 [P] CLAUDE.md 两处——《接口规范》资源清单 workflows 行补 runs 子资源代表路由（`/workflows/{id}/runs`、`/workflows/{id}/runs/{runId}`）；《错误处理》错误码表增 `RUN_NOT_FOUND` 404 行（哨兵位置 workflowapi.ErrRunNotFound，含「不存在与跨工作流同判不泄露」注）
- [ ] T027 [P] docs/testing/workflow-frontend-manual-test.md——§11 增补小节（照 §9/§10 先例格式）：列表展示与翻页无重漏 / 失败行标红 / 空态 / 详情轨迹序与失败高亮 / 截断文本如实展示 / 试运行联动刷新 / 既有区块回归复测引用 / 双门禁
- [ ] T028 quickstart.md 场景 A/B/C/D 全量收尾：`go build ./... && go vet ./... && go test ./... -race -count=1` 全绿 + `go test ./internal/workflow/... -race -cover` ≥80% + 前端双门禁全绿 + `make migrate-status` 20 applied 不变 + 依赖 grep；逐项对 SC-001~005 出验收证据（依赖 T024~T027）

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup（Phase 1）**: 无依赖，立即开始
- **Foundational（Phase 2）**: 依赖 T001——**阻塞两故事**（五类型契约是查询面公共前置）
- **US1（Phase 3）**: 依赖 T004；层内严格顺序 T005 RED → T006 → T007 RED → T008 → T009 RED → T010（store 先于 service：Store 接口扩方法与 store 实现必须同编译单元，否则 `var _` 断言红）；前端 T011 ∥ 后端全线（不同栈不同文件），T012 → T013 顺序
- **US2（Phase 4）**: 依赖 T008/T010（ListRuns 链路就位——同文件追加顺序）；层内 T014 → T015 → T016 → T017 → T018 → T019；前端 T020 ∥ 后端，T021 → T022
- **US3（Phase 5）**: 依赖 T012（Panel 存在）+ T021（详情抽屉在场，复核用）
- **Polish（Phase 6）**: 依赖 T024；T025 ∥ T026 ∥ T027（互不依赖不同文件）→ T028 收尾

### Within Each User Story

- 测试先于实现（T005→T006 / T007→T008 / T009→T010 / T014→T015 / T016→T017 / T018→T019）；同文件任务严格顺序
- 任务级 DoD：过 `go build ./... && go vet ./... && go test ./... -race -count=1` 再勾选

### Parallel Opportunities

- T002 ∥ T001 复核（Setup 内）；T011 ∥ T005~T010、T020 ∥ T014~T019（前端类型/组件与后端不同栈）；T025 ∥ T026 ∥ T027（文档三件互不依赖）

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Phase 1 基线 → 2. Phase 2 契约基座 → 3. Phase 3 US1 列表全链 → **STOP 验证**（curl 列表语义 + 详情页区块可视）
4. 继续 US2（详情 + 轨迹 + 抽屉）→ US3（刷新联动复核）→ Polish（契约文档 + CLAUDE.md + manual-test + 验收证据）

---

## Notes

- [P] = 不同文件、无未完成依赖；[Story] 标签映射 spec 用户故事
- 冻结契约逐字对齐 contracts/api.md（五类型 Go 形态/错误码/分层约定）+ contracts/frontend.md（组件契约）+ research.md D1~D7（api 层不 import page / keyset 双键 / 404 同判 / 显式列两档 / 空轨迹 / 组件形态 / 归一与 400 链）
- **store 包头注释修正**（T006 内，plan 注记项）：store.go 包头「三张表」改「五张表」——spec 06/08 已扩五表、注释滞后，本篇触碰该文件时最小同步，纯注释对齐非契约改动
- **US3 联动方案裁定**：刷新按钮（Panel 自治重拉）而非 TrialDialog emit——FR-006 试运行区块零变化的严格实现；契约已补进 contracts/frontend.md 区块结构
- 既有 8 路由用例零改动是 SC-005 自动化证据（T009/T018 断言面 + Phase 1 基线对照）；执行引擎与落库链路零触碰（FR-008——本篇不 import executor 侧任何写路径）
- commit 时机由用户明示；建议节点：US1 完成 / US2+US3 完成 / 文档收尾
