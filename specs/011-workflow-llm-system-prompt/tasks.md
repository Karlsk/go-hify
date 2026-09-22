# Tasks: 工作流 LLM 节点 system_prompt 支持

**Input**: Design documents from `/specs/011-workflow-llm-system-prompt/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/llm-node-config.md, quickstart.md

**Tests**: 本篇验收即测试（SC-001~004 全部单测断言），按 spec-dev 严格 TDD 执行——每个行为任务先写测试跑红（Go 新符号未定义导致的编译失败是正常 RED），再做最小实现转绿。

**Organization**: 按 user story 分组（US1 新行为 / US2 存量兼容验证），Phase 2 契约字段为两 story 共同前提。

## Format: `[ID] [P?] [Story] Description`

- **[P]**: 可并行（不同文件、无未完成依赖）
- **[Story]**: 归属 user story

## Path Conventions

Go 模块根 = 仓库根（`github.com/Karlsk/go-hify`）；改动全部收敛 `internal/workflow/` 两源文件 + 两同包测试文件。

---

## Phase 1: Setup（基线核实）

**Purpose**: 开工门禁——不在脏基线上动手

- [x] T001 基线门禁核实：`go build ./... && go vet ./... && go test ./... -race -count=1` 全绿；`make migrate-status` applied 恰 20 条（本篇零迁移，无编号冲突）；`grep eino go.mod` 确认依赖既有（零新增）

---

## Phase 2: Foundational（契约字段——US1/US2 共同前提）

**Purpose**: LLMConfig 加可选 SystemPrompt 字段（FR-001/FR-006），行为面未动

- [x] T002 [P] RED：`internal/workflow/api/schema_test.go` 增 LLMConfig 序列化往返用例——①带值 marshal 出 `"system_prompt"` 键且 round-trip 保值 ②空串与缺省 marshal 均不出键（FR-006/SC 对应 quickstart 用例 8）；跑 `go test ./internal/workflow/api/` 确认红（新断言失败或编译红）
- [x] T003 GREEN：`internal/workflow/api/schema.go` LLMConfig 加 `SystemPrompt string \`json:"system_prompt,omitempty"\``（置于 Prompt 字段之前，与 data-model.md 字段序一致）；Validate 零改动（system_prompt 可选、prompt 仍必填，FR-001）；`go test ./internal/workflow/api/ -race` 绿

---

## Phase 3: User Story 1 — 带 system_prompt 的 LLM 节点执行（Priority: P1）

**Story goal**: 执行期 system_prompt 非空 → strict 渲染 → [system, user] 消息序；排障记录带值。

**Independent test criteria**: stub 模型 client 的单 LLM 节点图执行，断言消息条数/顺序/内容 + 记录形态（quickstart 用例 1-7）。

- [x] T004 [US1] RED：`internal/workflow/service/executor_test.go` 增用例组（stub client + stub store，零真实网络/LLM）——①带 system_prompt 执行：Generate 收到恰 2 条消息 [system, user]、内容为各自模板渲染后文本（SC-001/FR-003）②system_prompt 含 `{{input.question}}` 合法引用被渲染替换（FR-002）③system_prompt 缺失变量：返回 ErrValidationFailed 且文案含变量名与节点 key、Generate 零调用、executions 零落（SC-003/FR-004）④node_in 摘要与 executions 行 Input 含渲染后 system_prompt 值（FR-005）⑤嵌套子图内 LLM 节点同样生效（US1-4）⑥system_prompt 空串等价缺省：单 user 消息、记录无新键（Edge Cases）；跑 `go test ./internal/workflow/service/ -race` 确认红
- [x] T005 [US1] GREEN：`internal/workflow/service/executor.go` callLLM 最小实现——system_prompt 非空时先 `c.render(cfg.SystemPrompt)`（失败走既有 ErrValidationFailed 路径，fail-fast 于 ResolveLLMConfig 之前）→ msgs 组装 `[{Role: System, ...}?, {Role: User, ...}]`；`setNodeIn` 与 `recordExecution` 的 Input map 有值才加 `system_prompt` 键（research 决策 3；recordExecution 传参按最小 diff 扩展携带渲染后双值）；空串/缺省路径与现状逐字节一致
- [x] T006 [US1] REFACTOR + 局部门禁：测试保护下对齐邻近代码风格（不扩范围）；`go build ./... && go vet ./... && go test ./... -race -count=1` 全绿后勾本任务

---

## Phase 4: User Story 2 — 既有工作流零影响兼容（Priority: P2）

**Story goal**: 存量行为零差异的证据链（SC-002/SC-004）——本 story 是验证型，无独立实现。

**Independent test criteria**: 既有断言零改动 + 全量回归绿 + 覆盖率维持。

- [x] T007 [US2] 存量回归验证：`git diff` 确认既有测试断言行零触碰（只增不改）；`go test ./internal/workflow/... -race -cover` 覆盖率 ≥80% 维持；核对既有 executions.Input 恰 `{"prompt":...}` 断言仍绿（无 system_prompt 键混入）；`git grep -n 'system_prompt' internal/workflow` 确认新键只在有值路径出现

---

## Phase 5: Polish（文档同步与验收收尾）

- [x] T008 [P] 冻结契约文档补录（加法修订注记，注明 2026-09-22 用户批准）：`docs/changelog/workflow/api_contract.md` LLM 节点 config 示例与字段表补 `system_prompt`（约 L131/L137 节）；`docs/changelog/workflow/impl_spec_06_execution_engine.md` callLLM 行为节补消息序与记录形态修订注记
- [x] T009 验收收尾：按 quickstart.md 用例对号表逐项核对落地；交付物存在性核对（git status 仅 4 个 internal 文件 + 文档 + specs/）；产出验收报告（含可选人工项：dev 栈冒烟）

---

## Dependencies

```text
T001 → T002/T003（Phase 2）→ T004 → T005 → T006 → T007 → T008/T009
```

- US1 依赖 Phase 2 字段（T003）；US2 依赖 US1 完成后才有可回归的对象；T008 与 T006 后任意点可并行（不同文件）。
- 跨 story 顺序固定（US1 → US2），无并行 story。

## Parallel Execution Examples

- T002（api 包测试）与 T004 前置阅读无冲突，但 T004 依赖 T003 字段存在才可编译——实际并行点仅 T002 ∥ T008 类跨文件任务；本篇体量小，建议顺序执行。

## Implementation Strategy

**MVP = Phase 2 + Phase 3（US1）**——字段 + 执行行为即完整价值交付；US2 是回归证据链、Polish 是文档治理，随其后收口。全程既有断言零改动；范围红线：不顺手动 callLLM 之外的任何节点类型。
