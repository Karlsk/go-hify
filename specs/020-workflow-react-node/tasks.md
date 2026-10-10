# Tasks: workflow react 节点 + ragNode 画布接入 + tool 节点表单（spec 020）

**Input**: Design documents from `/specs/020-workflow-react-node/`

**Prerequisites**: spec.md ✅（2026-10-10 起草 + Clarifications 两条）、plan.md ✅、research.md ✅、data-model.md ✅、contracts/node-contract.md ✅、quickstart.md ✅

**Tests**: TDD 强制（spec-dev 纪律）——mcp 并集解析 / ReactConfig 校验序列化 / R10 新分支 / callReact 图级 / retrieve query 全部先 RED 后 GREEN；前端任务门禁 = `cd web && npm run type-check && npm run build`。

**Organization**: Phase 2 公共底座（迁移 + mcp 并集扩展）→ US1 react 节点全链 → US2 ragNode → US3 tool 表单 → US4 验收收口。

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to
- 红线约束（贯穿所有实现任务）：迁移只增不改（00022 同名 CHECK 重建模式）；零新路由 / 哨兵 / SSE 事件；chat / reactloop / 组合根 server.go / workflow store+handler 零改动；依赖方向 workflow 只 import mcp/rag/provider 的 api 包；设计 token 零新增（三新节点着色 = 既有色族深浅配对）；冻结契约逐字（contracts C1–C7：ReactConfig 键集校验域 / ServerIDs 并集去重 / query 空回退 / 失败分型四路 / nodeIn 含 tool_chain / executions 每轮 Input 形态）。

---

## Phase 1: Setup（基线确认）

**Purpose**: 基线与迁移号核实——不动业务代码

- [x] T001 基线确认：`go build ./... && go vet ./... && go test ./... -race -count=1` 全绿；`make migrate-status` 21 applied（00022 本篇占用）；spec 019 reactloop 在位（`internal/platform/reactloop/loop.go` 的 Run/Config/Hooks/Result 公开面 grep 核实）

---

## Phase 2: Foundational（迁移 00022 + mcp 并集扩展——US1 前置）

**Purpose**: react 节点的 DB 类型位与工具集并集解析能力

- [x] T002 迁移文件：新建 `migrations/00022_workflow_react_node.sql`——`workflow_nodes_type_check` drop+add 重建加 `'react'`（八值，同 00017/00020 模式）+ `workflow_nodes.config` COMMENT 补 `react={model_id,prompt,system_prompt?,temperature?,server_ids?,tool_ids?,max_rounds?}` 与 `knowledge_retrieval` 补 `query?` 形态（goose Up/Down 成对，Down 重建回七值+原 COMMENT）；`make migrate-up && make migrate-status` 22 applied
- [x] T003 RED（mcp 并集单元）：`internal/mcp/store/store_test.go` 增 ListToolsByServerIDs sqlmock 用例（单 SQL `= ANY($1)`、显式列、空入参零查询）；`internal/mcp/service/toolsource_test.go`（或既有测试文件）增 ensure 并集用例组——toolIDs ∪ serverIDs 去重（重叠 tool 只解析一次、调用名唯一）/ server 停用过滤（并集面同语义）/ 只传 ToolIDs 路径逐字不变（chat 零影响证明）/ 只传 ServerIDs 全量解析；运行确认失败
- [x] T004 GREEN（mcp 三处加法）：`internal/mcp/api/schema.go` ToolSourceReq 加 `ServerIDs []uint64`（注释标并集语义）；`internal/mcp/store/store.go` 增 ListToolsByServerIDs + `internal/mcp/service/service.go` Store 接口加法；`internal/mcp/service/toolsource.go` ensure 并集解析（ListToolsByIDs ∪ ListToolsByServerIDs → tool id 去重 → 停用过滤 → 派生调用名，既有逐段语义不变）；任务级门禁 `go build ./... && go vet ./... && go test ./internal/mcp/... -race -count=1` 绿

**Checkpoint**: 并集能力独立可测——`go test ./internal/mcp/... -race -run 'ServerIDs|Union' -v` 全绿 + 22 applied

---

## Phase 3: User Story 1 - react 节点全链 (Priority: P1) 🎯 MVP 主线

**Goal**: 新 NodeType react 从契约到执行到画布表单的完整链路（FR-001~FR-004 / FR-007）

**Independent Test**: 画 `react（绑模型+server）→ end` 图试运行：两轮工具调用后终答落池、nodeIn 含 tool_chain、executions 逐轮留痕、上限收尾与 404 fail-fast 分型正确（quickstart 场景 A）

### Implementation for User Story 1

- [x] T005 [US1] RED（契约单元）：`internal/workflow/api/schema_test.go` 增 ReactConfig 用例组——Validate（model_id 零拒 / prompt 空拒 / max_rounds 17 与 -1 拒、0 与 1-16 过 / server_ids 或 tool_ids 含 0 元素拒）+ 序列化 round-trip（omitempty 三态：无 max_rounds / 空数组不出键；model_id 字符串保形；ids 数字数组形态）+ ParseNodeConfig(NodeReact) 透传与坏 config 拒；运行确认失败
- [x] T006 [US1] GREEN（契约单元）：`internal/workflow/api/schema.go` 增 `NodeReact NodeType = "react"` + `ReactConfig` 七键（C1 形态）+ Validate 域校验 + ParseNodeConfig 分支；任务级门禁绿
- [x] T007 [US1] RED→GREEN（保存期）：`internal/workflow/service/service_test.go` 增用例——precheckRefs react 分支（model_id 不存在 404 哨兵透传 / 存在过）+ R10 react 分支（prompt 与 system_prompt 非祖先引用拒、祖先与 input 过）；GREEN：`internal/workflow/service/service.go` precheckRefs 加 NodeReact（同 llm 分支形态）+ nodeTemplateRefs 加 NodeReact 分支（refs(prompt)+refs(system_prompt)）；任务级门禁绿
- [x] T008 [US1] RED（执行图级）：`internal/workflow/service/execute_test.go`（或 executor_test.go 既有 react 用例位）增 callReact 用例组——① 两轮工具循环终答落池（stub client factory 脚本 Generate 两轮 + stub toolSources 并集请求断言 ServerIDs/ToolIDs 透传）② 绑定全空 = 纯推理单轮（toolSources 零调用）③ 并集过滤后空清单 → ErrMCPToolNotFound 404 透传 + error_node 定位 ④ max_rounds=1 透传 reactloop（共 2 次 LLM 调用：1 工具轮 + 第 2 轮 nudge 收尾、HitLimit 非错误、终答落池）⑤ nodeIn 含 prompt/max_rounds/rounds/tool_calls/tool_chain（逐调用 round/id/name/args/is_error）⑥ executions 逐轮行（ConversationID=nil、Input 含 round 与 tool_calls）⑦ prompt 渲染缺失变量 → ErrValidationFailed 400 带 node 前缀；运行确认失败
- [x] T009 [US1] GREEN（执行）：`internal/workflow/service/executor.go` 增 callReact（strict 渲染 system_prompt+prompt → ResolveLLMConfig → client → 并集空 reg=nil / 非空 ToolSourceFor+NewRegistry fail-fast 分型 C2 → reactloop.Run Generate + Hooks{OnRound→recordReactExecution、OnToolCall/OnToolResult→tool_chain 累积} → setNodeIn → res.Content 落池）+ recordReactExecution（与 recordExecution 共享 Execution builder，Input={prompt,system_prompt?,round,tool_calls}）+ runNode type switch 加 ReactConfig 分支；任务级门禁绿
- [x] T010 [P] [US1] GREEN（前端 react）：`web/src/api/workflow.ts` 增 ReactNodeConfig 类型面；`web/src/views/workflow/graph.ts` PALETTE 扩（+react，序 llm/react/tool/knowledge_retrieval/condition/api/workflow/end）+ NODE_TYPE_NAMES；`CanvasEditor.vue` palette 色点 css + 节点着色 deep 选择器（primary 深档，tokens.css 实有档取值）；`NodeInspector.vue` react 分支表单（模型下拉复用 modelGroups / server 多选 enabled 过滤 / 工具分组多选 loadToolOptions 先例 / SystemPrompt+Prompt TemplateField / 温度 / max_rounds el-input-number 0-16 占位「0 = 默认 8」；config 写读 useStrConfigField 家族 + ids 数组 computed 数字转换）；门禁 type-check + build 绿

**Checkpoint**: US1 独立可测——`go test ./internal/workflow/... -race -run 'React' -v` 全绿 + 手测 quickstart 场景 A

---

## Phase 4: User Story 2 - ragNode 画布接入 + query 模板 (Priority: P1)

**Goal**: knowledge_retrieval 节点画布一等公民 + query 加法键（FR-005 / FR-008）

**Independent Test**: `llm1 提炼 → kr（query={{llm1.topic}}）→ llm2` 试运行检索按渲染问句命中；存量无 query 图回退 input 零变化（quickstart 场景 B）

### Implementation for User Story 2

- [x] T011 [US2] RED（契约+执行单元）：`internal/workflow/api/schema_test.go` 增 KnowledgeRetrievalConfig query 序列化用例（omitempty 不出键 / 往返保真 / 既有两键校验零变化）；`internal/workflow/service/executor_test.go` 增 retrieve query 用例（非空渲染值进 RetrieveReq.Query + nodeIn 记渲染值 / 空与缺省回退 vars["input"] / 渲染缺失变量 → ErrValidationFailed 带 node 前缀）；`service_test.go` 增 R10 kr 分支用例（query 非祖先拒 / 空 query 不扫）；运行确认失败
- [x] T012 [US2] GREEN：`internal/workflow/api/schema.go` KnowledgeRetrievalConfig 加 `Query string json:"query,omitempty"`；`internal/workflow/service/executor.go` retrieve 改造（query 渲染 / 空回退 / nodeIn 渲染值）；`service.go` nodeTemplateRefs kr 分支扫 query；任务级门禁绿
- [x] T013 [P] [US2] GREEN（前端 kr）：graph.ts PALETTE +knowledge_retrieval（info 深档着色）+ NODE_TYPE_NAMES「知识检索节点」；NodeInspector kr 分支表单（KB 下拉 getKnowledgeBaseList enabled 过滤 / top_k el-input-number 0-20 / query TemplateField 占位「空 = 检索整个入参」）；api/workflow.ts 增 KnowledgeRetrievalNodeConfig 类型面；门禁 type-check + build 绿

**Checkpoint**: US2 独立可测——`go test ./internal/workflow/... -race -run 'Retrieve|Knowledge' -v` 全绿 + 手测场景 B

---

## Phase 5: User Story 3 - tool 节点画布一等公民 (Priority: P2)

**Goal**: tool 节点 palette + 检查器表单（后端零改动，FR-009）

**Independent Test**: palette 拖入 tool → 分组单选工具 + args 模板行 → 保存试运行调用成功；存量 JSON 形态回显如初（quickstart 场景 C）

- [x] T014 [US3] GREEN（前端 tool 表单）：graph.ts PALETTE +tool（accent 深档着色）+ NODE_TYPE_NAMES「工具节点」；NodeInspector tool 分支表单（mcp 工具分组单选——loadToolOptions 先例、value=tool_id 字符串保形写 config.tool_id / args KV 行：键输入 + 值域 TemplateField，空键行不产出、全空删 args 键——headers 先例同款 sync 模式）；api/workflow.ts 增 ToolNodeConfig 类型面；存量 JSON tool 节点回显验证（表单读 config.tool_id/args 重建行）；门禁 type-check + build 绿

---

## Phase 6: User Story 4 + Polish - 零回归与验收收口 (Priority: P2)

- [x] T015 [US4] 全量验收门（SC-004/SC-005）：`go build ./... && go vet ./... && go test ./... -race -count=1` 全绿；`go test ./internal/workflow/... ./internal/mcp/... -race -cover` 两模块各 ≥80%；`go test ./internal/chat/... -race -count=1` 绿（ToolSourceReq 加法零影响）；`make migrate-status` 22 applied 且 migrations/ 仅新增 00022；grep 纪律（eino 不出 platform/llm、go-sdk 不出 mcp/service、`go list -deps ./internal/workflow/service` 依赖清单内）；前端 type-check + build 绿 + tokens.css / package.json / go.mod 零改动（git status 核对）；chat / reactloop / server.go / workflow store+handler 零触碰核对
- [x] T016 [P] [US4] 文档同步：`docs/testing/workflow-manual-test.md` 新增 §13 三场景小节（react 自主工具 / rag 先提炼后检索 / tool 画布闭环 + 零回归复测清单，quickstart 场景 A–D 逐条可勾选）；`docs/changelog/workflow/deferred_items.md` 增注：tool args 入 R10 存量 gap 维持 deferred（本篇不修，理由 = 保存期锁死存量坏图）；CLAUDE.md《资源清单》workflow 行无需改（路由零新增）但《模块内部结构》workflow config 注记若列节点形态则补 react/query（有则改、无则跳过）

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: 无依赖，立即开始
- **Foundational (Phase 2)**: 依赖 Phase 1；T002 ∥ T003 可并行（迁移文件与 mcp 测试不同文件）；T004 依赖 T003（RED 先行）
- **US1 (Phase 3)**: 依赖 T004（并集解析在位）+ T002（react 类型 e2e 需 CHECK——单测不需要，故 T005–T009 可与 T002 并行起步、e2e/手测前 T002 必须完成）；内部顺序 T005→T006→T007→T008→T009（契约→保存期→执行 RED→GREEN）；T010 依赖 T009（config 键集定稿）
- **US2 (Phase 4)**: 独立于 US1（kr 链路与 react 无交集）；T011→T012 严格 RED→GREEN；T013 依赖 T012
- **US3 (Phase 5)**: 完全独立（后端零改动）——可与 Phase 3/4 全程并行
- **US4/Polish (Phase 6)**: 依赖全部；T015 ∥ T016 可并行

### User Story Dependencies

- **US1 (P1)**: Foundational 后开始——MVP 主线
- **US2 (P1)**: 无跨故事依赖，可与 US1 并行
- **US3 (P2)**: 无依赖，全程可并行
- **US4 (P2)**: 收口，依赖全部

### Parallel Opportunities

- T002 ∥ T003；T010 ∥ T011/T012（前端 react vs 后端 kr）；T013 ∥ T014 ∥ T015 前置链；Phase 3 ∥ Phase 4 ∥ Phase 5 三线并行（文件面不重叠：react=executor/api react 面、kr=query 面、tool=纯前端）

---

## Implementation Strategy

### MVP First（Phase 2 + US1）

1. Phase 1 基线 → Phase 2 底座（T002 迁移 ∥ T003 RED → T004 GREEN）
2. Phase 3 US1 全链（T005–T010）
3. **STOP and VALIDATE**: `go test ./internal/workflow/... ./internal/mcp/... -race -run 'React|ServerIDs|Union' -v` + 手测场景 A
4. Phase 4 US2（T011–T013）→ Phase 5 US3（T014）→ Phase 6 验收门 + 文档

### 增量交付说明

US1 交付即 react 节点端到端可用（MVP：图内自主工具调用）；US2/US3 是画布收口（存量节点一等公民化）；US4 零回归证明。TDD 纪律：T003/T005/T007/T008/T011 先写测试运行确认失败再实现；迁移 T002 应用后不可回改（只增不改纪律）。

---

## Notes

- 任务级 DoD：后端任务过 `go build ./... && go vet ./... && go test ./... -race -count=1`；前端任务过 web 双门禁；红了当场修不攒批
- 反作弊红线：不得删断言 / t.Skip / 调低覆盖率门槛；chat 既有测试零改动是 US4 证明本体
- 冻结契约逐字保真：C1 ReactConfig 键集与校验域、C2 失败分型四路、C3 观测面（tool_chain / executions Input）、C4 ServerIDs 并集去重、C5 query 空回退、C6 不变量守护、C7 迁移形态
- commit 由用户明示；可提交节点 message：`feat(workflow): react 节点 + ragNode 画布接入 + tool 节点表单——图内自主工具调用与画布收口（spec 020）`
