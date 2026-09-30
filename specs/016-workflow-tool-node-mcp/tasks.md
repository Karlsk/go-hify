# Tasks: workflow 工具节点接 MCP（统一 tool.Source 执行）

**Input**: Design documents from `/specs/016-workflow-tool-node-mcp/`

**Prerequisites**: plan.md ✅, spec.md ✅, research.md ✅, data-model.md ✅, contracts/errors.md ✅, quickstart.md ✅

**Tests**: TDD 强制（spec-dev 纪律）——每实现任务先 RED 后 GREEN；上位 spec §6 任务清单即 TDD 粒度蓝本。

**Organization**: 按用户故事分组（US1 正常链路 / US2 失败分型 / US3 chat 透传回归），任务内嵌依赖红线与冻结契约约束。

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to
- 红线约束（贯穿所有实现任务）：workflow 只 import `mcp/api`（别名 mcpapi）+ `platform/tool` + `platform/errs`；禁止 `mcp/service`、go-sdk、eino 越界；service 层无 gin 类型；ToolConfig 冻结契约不动（D5 拍板 A：Args 值域仅字符串）。

---

## Phase 1: Setup（基线确认）

**Purpose**: 零迁移假设与基线门禁核实——不动任何代码

- [x] T001 基线确认：`go build ./... && go vet ./... && go test ./... -race -count=1` 全绿；`make migrate-status` 21 applied、下一号 00022（零迁移假设成立，预期不变）

---

## Phase 2: Foundational（测试替身）

**Purpose**: 服务全部用户故事的公共测试基建

- [x] T002 测试替身：`internal/workflow/service/exec_doubles_test.go` 增 `stubToolSourceFactory`——实现 `ToolSourceFor(ctx, mcpapi.ToolSourceReq) tool.Source`，返回可控 stub Source（Tools 清单 / Tools 错误 / Invoke 结果 / Invoke 记录），对齐同文件既有 stubResolve / stubRetrieve / stubClientFactory 记录式模式（§6 任务 1）

---

## Phase 3: User Story 1 - 工具节点真实执行并喂给下游 (Priority: P1) 🎯 MVP

**Goal**: tool 节点从占位 fail-fast 换成真实执行：解析工具 → args 严格渲染 → 单次确定性 Invoke → Content 落变量池，下游节点 `{{tool_key}}` 可引用（US1 / FR-001 / FR-004 渲染 / FR-005 / FR-007 / FR-008）

**Independent Test**: 构造 `llm → tool → end` 三节点图（工具输出进终答 prompt 模板），执行 run 断言终答含工具输出、node_runs 里 tool 节点 succeeded 且 input/output 落轨迹（quickstart §5 场景 2-3 的自动化等价）

### Implementation for User Story 1

- [x] T003 [US1] RED（单元）：`internal/workflow/service/executor_test.go` 增 tool 节点正常链路用例（§7 清单 1：args 模板渲染 → Invoke 收到渲染后按 key 排序 JSON Arguments → Content 落变量池 → 下游 `{{node_key}}` 可引用 → nodeIn 记 `{"tool_id","args"}` → node run Output 落值 + DurationMs）；同文件替换 :575 起 `TestRunNodeTool` 旧 fail-fast 断言（「mcp 未建 → 图缺陷 400」删除，spec §7 清单 8 授权）；`newTestExecutor` 辅助函数与 :141 既有构造点随 newExecutor 扩参机械更新；运行确认失败（§6 任务 2 前半）
- [x] T004 [US1] GREEN（单元）：`internal/workflow/service/executor.go` 增 `toolSourceFactory` 窄接口（D1 冻结签名：`ToolSourceFor(ctx context.Context, req mcpapi.ToolSourceReq) tool.Source`）+ `callTool` 执行分支（D3/D4 成功侧）：nil toolSources 判空 → `ToolSourceFor({ToolIDs:[cfg.ToolID]})` → `Tools(ctx)` 预检 → args 逐值严格 `{{var}}` 渲染（复用 callWorkflow 同源严格渲染，缺失变量 → `errs.ErrValidationFailed` 带 `args.<k>` 定位）→ 按 key 排序 marshal → `Invoke(ctx, ToolCall{ID: 本地生成锚点, Name: defs[0].Name, Arguments})` → `setNodeIn` + `setNodeOut(res.Content)`；newExecutor 增 toolSources 参数（§6 任务 2 后半）
- [x] T005 [US1] 注入链：`internal/workflow/service/service.go` `New` 增第 9 参 `toolSources`（rags 后、blockPrivate 前，:96 签名与 :101 newExecutor 透传）；`service_test.go` 既有构造点机械更新；任务级门禁 `go build ./... && go vet ./... && go test ./internal/workflow/... -race -count=1` 绿（§6 任务 3）
- [x] T006 [US1] 组合根：`internal/app/server.go` :170 `workflowsvc.New(...)` 追加 `mcpClientSvc` 传参（mcp 在 :132 前于 workflow 构建，零重排）；任务级门禁全量绿（§6 任务 5）
- [x] T007 [US1] RED→GREEN（图级）：`internal/workflow/service/execute_test.go` 增图级端到端用例（§7 清单 7）：`llm → tool → end`（工具输出进终答 prompt 模板）+ condition 节点按 tool 输出路由二选一；同文件替换 :257 附近「tool 未支持 → 图缺陷 400」旧断言（spec §7 清单 8 授权）；GREEN 由 T004 已完成——用例落地即应通过（§6 任务 4）

**Checkpoint**: US1 独立可测——`go test ./internal/workflow/service/ -race -run 'Tool' -v` 全绿

---

## Phase 4: User Story 2 - 工具失效可定位的失败路径 (Priority: P2)

**Goal**: 三类故障 + 两类环境异常的 fail-fast 分型，错误消息带节点与参数定位（US2 / FR-002 / FR-003 / FR-004 缺失变量 / FR-006）

**Independent Test**: 对同一图分别注入「Tools 清单空」「Invoke 返回错误」「args 缺失变量」三类故障，断言错误类型、状态码语义与节点定位逐类不同（contracts/errors.md E1–E5）

### Implementation for User Story 2

- [x] T008 [US2] RED：`internal/workflow/service/executor_test.go` 增失败用例组（§7 清单 2–6）：Tools 返回空 → `mcpapi.ErrMCPToolNotFound` 透传 + `node <key>:` 前缀；Tools DB 错误 → `%w` 包装；args 缺失变量 → `errs.ErrValidationFailed` + `args.<参数名>` 定位；Invoke IsError → `mcpapi.ErrMCPToolCallFailed`（message 含 Content）+ run failed + error_node 定位该节点；toolSources == nil → `ErrMCPToolCallFailed`「工具来源未注入」；运行确认失败（§6 任务 2 RED 补全）
- [x] T009 [US2] GREEN：`internal/workflow/service/executor.go` `callTool` 失败分型补全（D2 冻结语义）：Tools 空清单 → `ErrMCPToolNotFound` 直接透传（不加壳）；Tools DB 错 → `%w` 包装；Invoke IsError → `fmt.Errorf("%w: %s", mcpapi.ErrMCPToolCallFailed, Content 截断文本)`；nil 装配 → 同码「工具来源未注入」——全部经既有 walk 层 `node <key>:` 前缀机制（零改动），run failed + error_node 落库由既有 walkOutcome 路径自动生效；任务级门禁绿

**Checkpoint**: US2 独立可测——五类失败断言逐类对号（contracts/errors.md 分型表 E1–E5）

---

## Phase 5: User Story 3 - Agent 对话内嵌 workflow 透传受益 (Priority: P3)

**Goal**: chat 侧零改动零回归——管道内嵌工具节点随本篇打通（US3 / FR-001 依赖注入形态验证）

**Independent Test**: chat 模块全量测试回归绿（零改动前提下跑既有套件即证明，§7 清单 9）

- [x] T010 [US3] 回归验证：`go test ./internal/chat/... -race -count=1` 全绿（chat 代码零改动；组合根 T006 已把 mcpClientSvc 注入 chat 既有参数位，此处验证透传路径无回归）

**Checkpoint**: 全部用户故事独立功能就绪

---

## Phase 6: Polish & Cross-Cutting Concerns

- [x] T011 全量验收门（§8 / quickstart §1–4）：`go build ./... && go vet ./... && go test ./... -race -count=1` 全绿；`go test ./internal/workflow/... -race -cover` workflow/service ≥80%；依赖 grep 三条全空（`internal/workflow/` 无 `internal/mcp/service` import；`modelcontextprotocol/go-sdk` 不出 `internal/mcp/service/`；`cloudwego/eino` 不出 `internal/platform/llm/`，test 除外）；`make migrate-status` 21 applied 不变、既有迁移文件零改动（§6 任务 6）
- [x] T012 [P] 文档同步（spec §9，全部为既有文件加注）：CLAUDE.md《错误处理》表 `MCP_TOOL_NOT_FOUND` / `MCP_TOOL_CALL_FAILED` 两行补 workflow tool 节点触发场景注记（不加新行）；`docs/changelog/mcp/README.md` §7 勾销最后一项；`docs/changelog/mcp/chat_tool_loop_impl.md` §4 workflow 工具节点项指向本篇；`docs/changelog/workflow/deferred_items.md` #2 进度事件触发表述更新（工具节点已通，仍暂缓）

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: 无依赖，立即开始
- **Foundational (Phase 2)**: 依赖 Phase 1——stubToolSourceFactory 是 T003 起所有 tool 用例的编译前提
- **US1 (Phase 3)**: 依赖 Phase 2；内部严格顺序 T003 → T004 → T005 → T006 → T007（RED→GREEN→注入链→组合根→图级）
- **US2 (Phase 4)**: 依赖 T004（callTool 骨架已存在）；T008 → T009 严格 RED→GREEN
- **US3 (Phase 5)**: 依赖 T006（组合根装配完成）
- **Polish (Phase 6)**: 依赖全部用户故事完成；T011 ∥ T012 可并行

### User Story Dependencies

- **US1 (P1)**: Foundational 后即可开始，无跨故事依赖
- **US2 (P2)**: 复用 US1 的 executor 骨架与 stub（T004 后开始），独立可测
- **US3 (P3)**: 复用 US1 的组合根装配（T006 后开始），纯回归验证

### Parallel Opportunities

- T011 ∥ T012（验收门与文档同步互不依赖）
- US2 的 T008 用例编写可与 US1 的 T007 图级用例并行（不同测试文件）——但 T009 依赖 T004 完成后的 GREEN 骨架

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Phase 1 基线确认 → Phase 2 测试替身
2. Phase 3 US1：T003–T007（TDD 单元 → 注入链 → 组合根 → 图级）
3. **STOP and VALIDATE**: `go test ./internal/workflow/service/ -race -run 'Tool' -v` 独立验证
4. Phase 4 US2（失败分型）→ Phase 5 US3（chat 回归）→ Phase 6 验收门 + 文档同步

### 增量交付说明

US1 交付即可端到端跑通正常链路（MVP）；US2 补齐排障面（错误分型是本篇验收门 §8 明确项，紧随 MVP 不可跳）；US3 是零改动回归证明。TDD 纪律：T003/T008 写测试先运行确认失败再实现（Go 新符号未定义的编译失败是正常 RED）。

---

## Notes

- 任务级 DoD：每个 [T] 完成前过 `go build ./... && go vet ./... && go test ./... -race -count=1`；红了当场修不攒批
- 反作弊红线：不得删断言 / t.Skip / 调低覆盖率门槛——唯一授权替换面 = spec §7 清单 8（:575 / :257 旧 fail-fast 断言由新用例组取代）
- 冻结契约逐字保真：D1 接口签名、D2 分型语义、D3 渲染与序列化、D4 轨迹形态（哨兵文案 = error.code，前端直接消费）
- commit 由用户明示；可提交节点 message：`feat(workflow): tool 节点接 mcp——统一 tool.Source 执行 + fail-fast 错误分型（spec 016）`
