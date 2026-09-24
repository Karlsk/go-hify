---
description: "Task list: LLM 节点输出字段声明与变量下拉展开"
---

# Tasks: LLM 节点输出字段声明与变量下拉展开

**Input**: Design documents from `/specs/014-workflow-llm-output-schema/`

**Prerequisites**: plan.md（模块落位/宪法检查）、spec.md（US1~US3 + FR-001~007 + SC-001~005）、research.md（D1~D7 冻结决策）、data-model.md、contracts/api.md + contracts/frontend.md、quickstart.md

**Tests**: 后端 TDD 强制（spec 验收标准：go test 全绿）——测试任务先于对应实现任务；前端无单测基建，走 type-check && build 双门禁 + manual-test 人工验收。

**Organization**: 按 spec 用户故事分组（US1 声明+下拉 P1 / US2 运行期校验+注入 P2 / US3 零影响 P3）。

## Format: `[ID] [P?] [Story] Description`

- **[P]**: 可并行（不同文件、无未完成依赖）
- **[Story]**: 所属用户故事（US1/US2/US3）；Setup/Foundational/Polish 无故事标签

## Path Conventions

单仓库：后端 `internal/workflow/`（api/service 同包 *_test.go）；前端 `web/src/`；文档 `docs/`。

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: 开工基线确认（既有仓库，无结构初始化）

- [x] T001 基线与不变量确认：`go build ./... && go vet ./... && go test ./... -race -count=1` 全绿 + `make migrate-status` 20 条 applied、下一号 00021（本篇零迁移，收尾时该状态须不变）
- [x] T002 [P] 改造前 golden 落样：对既有 callLLM 测试路径记录未声明节点的消息序列 / node_in 形态基准（为 US3 逐字节零变化断言提供对照，落 internal/workflow/service/executor_test.go 既有 fixture 风格）——TestCallLLMUndeclaredGolden 双子测试（单 user / system+user）整 map + 整串逐字节断言，全量门禁绿后落样

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: config 契约键先行——US1（表单写）/ US2（执行器读）/ US3（往返零键）共同依赖

- [x] T003 RED：internal/workflow/api/schema_test.go 扩——LLMConfig 序列化往返三态（带值产出 `output_schema` / 空数组零键 / 缺省零键）+ Validate 新键拒绝路径（name 空、节点内重名、type 越枚举）+ 存量无声明 config 零新增拒绝（RED 确认：unknown field OutputSchema 编译失败）
- [x] T004 GREEN：internal/workflow/api/schema.go——LLMConfig 增 `OutputSchema []SchemaField \`json:"output_schema,omitempty"\``，Validate 在非空时调既有 ValidateSchemaFields（依赖 T003）——api 包全绿 + 全量门禁 38 包 ok

**Checkpoint**: config 契约键就位，三个故事可开工。

---

## Phase 3: User Story 1 - 声明输出字段并让下游下拉展开引用 (Priority: P1) 🎯 MVP

**Goal**: llm 检查器声明输出字段（复用 SchemaFieldsEditor），下游变量下拉按声明展开 `{{key.field}}` 条目

**Independent Test**: 声明 code/reason 两字段保存 → 下游节点模板字段下拉出现字段条目并可光标插入；未声明节点仍只有整体条目

### Implementation for User Story 1

- [x] T005 [P] [US1] web/src/api/workflow.ts——llm 节点 config 类型补 `output_schema?: SchemaField[]`（键名对齐后端契约，复用既有 SchemaField 前端形态）——落地为新增 LLMNodeConfig 类型面（config 载荷通道仍是 Record 透传，类型供检查器读写收窄）
- [x] T006 [US1] web/src/views/workflow/NodeInspector.vue——llm 表单 Prompt 区后加「输出字段」区：复用 SchemaFieldsEditor（modelValue + update:model-value patch，与伪节点面板同形态）；读写 config.output_schema（空数组/全空行删键对齐 omitempty）；readonly 态不渲染
- [x] T007 [US1] web/src/views/workflow/NodeInspector.vue——variableGroups upstream 分组：祖先 llm 节点 config.output_schema 非空 → 整体条目 `{{key}}` 之后追加字段条目（label `{key}.{name}（{type}·{必填|可选}）`、insert `{{key.field}}`、group 维持 'upstream'）；未声明 llm 节点与非 llm 祖先条目零变化（同文件依赖 T006）
- [x] T008 [US1] 前端双门禁 `cd web && npm run type-check && npm run build` 全绿 + 冒烟：声明→保存→详情回读声明在（config 键 output_schema）、下游下拉展开插入、未声明节点零回归（依赖 T005~T007）——双门禁全绿（vue-tsc 零错、build ✓；chunk 告警为既有基线）；冒烟三项进 T014 manual-test 增补小节转人工验收

**Checkpoint**: US1 独立可验（编辑期声明与下拉展开全链路）。

---

## Phase 4: User Story 2 - 运行期按声明校验模型回复 (Priority: P2)

**Goal**: 声明非空节点——渲染后 user 消息末尾自动注入固定 JSON 指令（记录实发）；Generate 后按声明严格校验，不合规节点失败

**Independent Test**: stub 模型分别返回合规 JSON / 缺必填 / 非纯 JSON——前通过入池、后两者节点失败且错误含节点 key 与字段名

### Tests for User Story 2 (TDD——先写并确认 RED)

- [x] T009 [US2] RED：internal/workflow/service/executor_test.go 扩——buildJSONDirective 冻结文案断言（research D2 前缀逐字 + 每字段行 `- {name}（{type}，{必填|可选}）`、description 不进指令）；callLLM 注入位置与记录实发（stub client 收到的 user 消息 HasSuffix 指令全文、node_in["prompt"] == 实发、system_prompt 不被注入）；validateLLMOutput 四分支（合规含多余字段宽容 / 缺必填拒含字段名 / 类型不符拒含字段名与期望类型 / 非 JSON 拒：纯文本、null、数组、markdown 围栏）；未声明零注入零校验、消息序列与 node_in 对照 T002 golden 逐字节一致；嵌套子图内声明节点同注入同校验（依赖 T004）——五组测试落齐，RED 确认（undefined: validateLLMOutput / buildJSONDirective 编译失败）

### Implementation for User Story 2

- [x] T010 [US2] GREEN：internal/workflow/service/executor.go——新纯函数 `buildJSONDirective(fields []api.SchemaField) string` 与 `validateLLMOutput(output string, fields []api.SchemaField) error`（unmarshal 到 map[string]json.RawMessage、required 检查、复用既有 probeSchemaType 探针；错误文案含字段名）（依赖 T009）——纯函数测试绿（probeSchemaType 收 string，json.RawMessage 需 string(raw) 转换）
- [x] T011 [US2] GREEN：internal/workflow/service/executor.go——callLLM 接线：渲染成功后、setNodeIn 之前注入（len(OutputSchema)>0 为门）；client.Generate 成功后、return 前校验；失败包装 errs.ErrValidationFailed 带节点 key 前缀（与既有节点失败链一致，零新增哨兵）（同文件依赖 T010）——service 包全绿 + 全量门禁 38 包 ok + migrate-status 20 applied 不变

**Checkpoint**: US2 独立可验（`go test ./internal/workflow/... -race -count=1` 全绿）。

---

## Phase 5: User Story 3 - 存量工作流零影响 (Priority: P3)

**Goal**: 未声明节点行为逐字节不变——消息序列、执行路径、记录形态、序列化往返零新键

**Independent Test**: 既有全量测试零改动全绿；无声明的存量图序列化往返 diff 为空

- [x] T012 [US3] 零影响复核：全量 `go test ./... -race -count=1` 既有用例零改动全绿；T002 golden 对照断言（未声明节点消息序列/node_in/executions 形态）与 T003 往返零键断言逐项确认；`make migrate-status` 状态与 T001 一致（依赖 T011）——38 包 ok、测试文件 0 删除行（纯新增）、20 applied 不变

**Checkpoint**: 三故事全部独立可验。

---

## Phase 6: Polish & Cross-Cutting Concerns

- [x] T013 [P] docs/changelog/workflow/api_contract.md——§3 llm config 键集加法修订注记（新增 `output_schema`，spec 011 system_prompt 同款先例格式）——已追加于 spec 011 注记之后（§4 示例区 L150：注入①/校验②/记录实发/保存期校验/存量逐字节零影响，spec 011 同款 `〔2026-09-24 追加（spec 014，用户批准·加法修订）〕` 格式）
- [x] T014 [P] docs/testing/workflow-frontend-manual-test.md——增补小节：声明编辑 / 下拉展开插入 / 载荷回读（output_schema 键）/ 试运行校验错误文案定位 / 既有下拉与表单回归复测——§10 六小节落齐（10.1 声明编辑含只读不渲染与空删键 / 10.2 下拉展开插入 / 10.3 载荷回读核对 / 10.4 试运行注入与校验含记录保真 / 10.5 存量回归复测 / 10.6 通过标准增补 SC-001~005），照 §9 先例格式；T008 承诺的冒烟三项转 10.1~10.3 人工验收
- [x] T015 quickstart.md 场景 A/B 全量收尾：`go build ./... && go vet ./... && go test ./... -race -count=1` 全绿 + `go test ./internal/workflow/... -race -cover` ≥80% + 前端双门禁全绿；逐项对 SC-001~005 出验收证据（依赖 T012~T014）——实测（2026-09-24）：全量 38 包 ok；workflow 覆盖率 api 97.6% / handler 83.9% / service 88.9% / store 96.6% 全 ≥80%；vue-tsc 零错 + build ✓（chunk 告警为既有基线）；make migrate-status 20 applied 不变（下一号 00021，本篇零迁移）；依赖方向 grep `internal/workflow` 无 chat import（零命中）；SC-001~005 证据见 spec 级验收报告（对话内输出）

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup（Phase 1）**: 无依赖，立即开始
- **Foundational（Phase 2）**: 依赖 T001/T002——**阻塞全部故事**（config 键是三故事共同前置）
- **US1（Phase 3）**: 依赖 T004；前端线与后端 US2 线可并行
- **US2（Phase 4）**: 依赖 T004；T009 RED → T010/T011 GREEN 严格顺序（同文件 + TDD）
- **US3（Phase 5）**: 依赖 T011（零影响断言须在实现后在场复核）
- **Polish（Phase 6）**: 依赖 T012

### Within Each User Story

- 测试先于实现（T003→T004 / T009→T010→T011）；同文件任务严格顺序
- 任务级 DoD：过 `go build ./... && go vet ./... && go test ./... -race -count=1` 再勾选

### Parallel Opportunities

- T002 ∥ T001（Setup 内）；T005 ∥ 后端 Phase 4 全线（不同栈不同文件）
- T013 ∥ T014 ∥ T012（文档与复核互不依赖）

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Phase 1 基线 → 2. Phase 2 config 键 → 3. Phase 3 US1 → **STOP 验证**（声明持久化 + 下拉展开，后端 go test 全绿 + 前端双门禁）
4. 继续 US2（注入+校验）→ US3（零影响复核）→ Polish（文档+验收证据）

---

## Notes

- [P] = 不同文件、无未完成依赖；[Story] 标签映射 spec 用户故事
- 冻结契约逐字对齐 contracts/api.md + contracts/frontend.md + research.md D1~D3（注入文案/校验语义/记录时序）
- **范围外项已裁定前置落地**（2026-09-23 用户批准）：NodeInspector.vue:856 原始 NUL 字节 → `\x00` 转义（运行时逐字节等价），已作为独立小修先行完成——文件回归 UTF-8 文本、无残留 NUL、前端双门禁全绿；commit 等用户明示
- commit 时机由用户明示；建议节点：US1 完成 / US2+US3 完成 / 文档收尾
- **实现后修复（2026-09-24，用户冒烟反馈「添加字段无效」）**：T006 的 `llmOutputSchema` set 原实现把「写时过滤空行」当 omitempty 纪律——受控回路被打断（「添加字段」emit 的空 name 行被过滤、get 回读旧值，新行即消失）。修复：set 原样写回（空行留画布 config）、仅空数组删键；「全空行 → 删键」按契约落点移到组装层——graph.ts 新增 `toPayloadNode`（buildCreatePayload / buildUpdatePayload 共用：llm 且带 output_schema 数组才浅拷贝清理，其余节点 config 引用直传不变量 1 保持）。手测 §10.1.4 空行路径同步改为组装层清理（重名保留后端 400）；前端双门禁复跑全绿。涉及文件超出 T005~T007 清单 +1：web/src/views/workflow/graph.ts（组装层，本篇契约 §2「空数组 / 全空行 → 删键」的保存期落点）
