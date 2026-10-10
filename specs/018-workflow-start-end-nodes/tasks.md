# Tasks: Workflow 画布开始/结束节点与入参默认值（spec 018）

**Input**: Design documents from `/specs/018-workflow-start-end-nodes/`

**Prerequisites**: spec.md ✅（2026-10-09 定稿 + Clarifications）、plan.md ✅、research.md ✅、data-model.md ✅、contracts/jsonb-contract.md ✅、quickstart.md ✅

**Tests**: TDD 强制（spec-dev 纪律）——后端每实现任务先 RED 后 GREEN（新符号未定义的编译失败是正常 RED）；前端任务以 type-check + build 为门禁。

**Organization**: Phase 2 公共契约层（后端 api 契约 + 前端类型）→ US3 默认值链路 → US2 end 输出映射 → US1 开始节点 → US4 收敛与验收。FR 交织说明：FR-010 默认值列被 US1（开始节点面板）与 US3（试运行预填）共用，故 SchemaFieldsEditor 列扩展随 US3 交付、US1 只做画布渲染与入口。

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to
- 红线约束（贯穿所有实现任务）：零迁移（`make migrate-status` 恒 21 applied、migrations/ 零改动）、零新依赖（go.mod / package.json 不动）、零新错误码（行级校验走既有 `errs.ErrValidationFailed` 路径）、chat / store / handler / 组合根 `server.go` 零改动、冻结契约逐字保真（contracts/jsonb-contract.md C1/C2/C3：default 类型恒等于 field type 且 required 禁配、output 与 output_fields 互斥拒、normalizeInputJSON 只补默认不新增拒绝、R10 基名规则不变）；设计 token 只增不改（本篇零新增，tokens.css 不动）。

---

## Phase 1: Setup（基线确认）

**Purpose**: 零迁移假设与基线门禁核实——不动任何代码

- [x] T001 基线确认：`go build ./... && go vet ./... && go test ./... -race -count=1` 全绿；`cd web && npm run type-check && npm run build` 全绿；`make migrate-status` 21 applied、下一号 00022（零迁移假设成立）

---

## Phase 2: Foundational（公共契约层——后端 api 契约 + 前端类型）

**Purpose**: FR-006 / FR-009 契约面与前端类型底座，服务 US2 与 US3 全部后续任务

- [x] T002 RED（契约单元）：`internal/workflow/api/schema_test.go` 增两组用例——① SchemaField default：类型探针匹配（string/number/boolean 各一正向 + 三个负向：string 配 number 值 / number 配 string 值 / null 拒）、required+default 拒（文案含字段名）、序列化 round-trip（无 default 字段 omitempty 不出现键、default 原文保真往返）；② EndConfig 行级：output 与 output_fields 同时非空拒、行 name 空拒、行 name 重名拒、ref 非单一 `{{var}}`（裸文本 / 混排 / 空）拒、ref 点分形态 `{{input.x}}` 合法、output_fields 序列化 round-trip、ParseNodeConfig(NodeEnd) 新键解析透传；运行确认失败（新符号未定义编译红 = 正常 RED）
- [x] T003 GREEN（契约单元）：`internal/workflow/api/schema.go`——SchemaField 增 `Default json.RawMessage \`json:"default,omitempty"\``；ValidateSchemaFields 增两条（default 非 null 且类型探针不匹配 field type 拒、Required && len(Default)>0 拒）；新增 `OutputField{Name string; Ref string}`（json tag name/ref）与包内 `singleVarRE = ^\{\{([^{}]+)\}\}$`；EndConfig 增 `OutputFields []OutputField \`json:"output_fields,omitempty"\`` + Validate 行级校验（互斥 / name 非空不重名 / ref 恰单一 {{var}}）；任务级门禁 `go build ./... && go vet ./... && go test ./internal/workflow/... -race -count=1` 绿
- [x] T004 [P] GREEN（前端类型）：`web/src/api/workflow.ts`——SchemaField 增 `default?: string | number | boolean`；增 EndConfig 形态注释或类型（output_fields?: `{ name: string; ref: string }[]`）挂到既有 LLMNodeConfig 邻近的节点配置类型区；门禁 `cd web && npm run type-check && npm run build` 绿

---

## Phase 3: User Story 3 - 入参默认值三处生效 (Priority: P1) 🎆 MVP 主链路

**Goal**: optional 默认值后端存储 + 子工作流/顶层引擎补全 + 试运行预填（US3 / FR-006 引擎侧 / FR-007 / FR-008 / FR-010 前端列 / FR-011）

**Independent Test**: 声明 `{keyword required} + {limit optional number default 10}` 的 task workflow——试运行只填 keyword 断言 `{{input.limit}}` 渲染 10；作为子工作流、父图 inputs 不给 limit 同样断言补 10（quickstart 场景 C）

### Implementation for User Story 3

- [x] T005 [US3] RED（单元）：`internal/workflow/service/execute_test.go` 增两组——① assembleInputJSON 补默认：optional 缺失且有 default → 键出现且值 = default 原文（number 原文落位不规约）、optional 缺失无 default → 维持省略、required 缺失仍拒（既有）、default 与显式传值并存时显式值优先；② normalizeInputJSON 纯函数边界：合法 JSON 对象缺带默认 optional → 补后重序列化（值原样保真）、字段已在场 → 不动、非合法 JSON / 非对象（数组/数字/带引号字符串）→ 原文返回、未声明字段带默认不管、空 schema → 原文返回；运行确认失败（normalizeInputJSON 未定义）
- [x] T006 [US3] GREEN（单元）：`internal/workflow/service/execute.go`——assembleInputJSON optional `!ok` 分支增 `len(f.Default) > 0 → obj[f.Name] = f.Default`；新增 `normalizeInputJSON(input string, fields []workflowapi.SchemaField) string` 纯函数（map[string]json.RawMessage 补缺失 optional 默认，changed 短路，marshal 失败防御返回原文）；Execute 在 newExecContext 前对 `wf.Type == task && wf.InputSchema != nil` 且 schemaFields 解析成功时归一化（失败 fail-open 原文），归一化产物同入 `runMeta.input`；补 Execute 级用例：顶层 task 型缺默认补全进变量池（`{{input.limit}}` 渲染 10）、chat 型 / 无 schema 零变化；任务级门禁绿
- [x] T007 [P] [US3] GREEN（前端编辑）：`web/src/views/workflow/SchemaFieldsEditor.vue`——行编辑增「默认值」列：仅 required=false 行可填、按 type 切控件（string→el-input / number→el-input-number / boolean→el-switch）、required 切 true 时清空并禁用该列、值写入行对象 default 字段（undefined 时不出键）；门禁 type-check + build 绿
- [x] T008 [US3] GREEN（前端预填）：`web/src/views/workflow/WorkflowTrialDialog.vue`——task 型有 schema 模式打开时按 `input_schema[].default` 预填 fieldValues；提交组装 JSON 时的省略规则**限定「optional 且配置了 default」的行**（清空 = 省略，由顶层归一化补回默认值）；未配 default 的 optional 行维持现状空值照传 `""`（省略会令 strict 渲染报变量未定义，属回归，analyze F2）；required 空值仍前端拦截（既有行为）；门禁 type-check + build 绿

**Checkpoint**: US3 独立可测——`go test ./internal/workflow/service/ -race -run 'AssembleInput|NormalizeInput|Execute' -v` 全绿 + 手测 quickstart 场景 C

---

## Phase 4: User Story 2 - end 输出映射与多 end 终稿 (Priority: P1)

**Goal**: EndConfig.output_fields 引擎构造（转义安全）+ R10 覆盖 + 前端映射编辑器与并集推导（US2 / FR-003 / FR-004 / FR-009 引擎侧）

**Independent Test**: `llm1（声明 output_schema summary/score）→ end1` 映射两字段试运行，断言终稿合法 JSON 对象、类型正确、summary 含引号/换行不破坏 JSON；condition 分流双 end 各自产出（quickstart 场景 B）

### Implementation for User Story 2

- [x] T009 [US2] RED（单元）：`internal/workflow/service/executor_test.go` 增 buildOutputFields 用例组——① 基本构造：池含 llm1 JSON 对象输出，映射 summary→{{llm1.summary}} + score→{{llm1.score}}，断言终稿为合法 JSON 对象且字段值正确；② 转义：string 声明字段值含 `"` 与换行 → 终稿仍合法 JSON（Marshal 转义）；③ 类型处理：声明 string 而值形似数字 → 落位带引号字符串；声明 number/boolean → 原 JSON 文本落位；字段不在 outputTypes → 回退（可解析 JSON 原样 / 否则字符串）；声明 number 而值不可解析 → 图缺陷 ErrValidationFailed；④ 缺失变量：ref 池中无值 → strict 报错含变量名；⑤ 优先级：output_fields 非空时忽略 output；⑥ nodeIn 记录 output_fields 摘要；运行确认失败
- [x] T010 [US2] GREEN（单元）：`internal/workflow/service/execcontext.go` 增 `outputTypes map[string]string` 字段；`internal/workflow/service/executor.go` buildOutput 增 `len(cfg.OutputFields) > 0` 优先分支 → 新增 buildOutputFields（逐字段 lookup → 按 ec.outputTypes 声明类型处理〔string→Go string 落位 / number+boolean→json.Valid 探针原文 RawMessage 落位、失败报 ErrValidationFailed〕/ 未声明回退 → json.Marshal 整对象；缺失变量 strict ErrValidationFailed）；`internal/workflow/service/execute.go` Execute 与 executeChild 在 newExecContext 后从 wf.OutputSchema 装配 outputTypes（schemaFields 解析失败置 nil，fail-open）；任务级门禁绿
- [x] T011 [US2] RED→GREEN（R10）：`internal/workflow/service/service_test.go` validateTemplateRefs 增用例——end 节点 output_fields 引用祖先 key / input 合法、引用非祖先 key 拒（VALIDATION_FAILED，文案含节点 key 与 ref）、点分 ref 按基名判定（`{{input.x}}` 合法）；GREEN：`internal/workflow/service/service.go` nodeTemplateRefs 的 EndConfig 分支追加 `for _, of := range v.OutputFields { refs = append(refs, templateRefs(of.Ref)...) }`；任务级门禁绿
- [x] T012 [P] [US2] GREEN（前端推导纯函数）：`web/src/views/workflow/graph.ts` 增 `deriveOutputSchema(nodes: GraphNode[], inputSchema?: SchemaField[], subflowSchemas?: Record<string, SchemaField[]>): { schema: SchemaField[]; conflict?: string }`——扫全部 type=end 节点 config.output_fields，同名同型合并、同名不同型返回 conflict（调用方保存期拦截）、required 推导（全部映射 end 都产出 = true）、type 推导（`{{input.x}}`→inputSchema 同名字段、`{{llmKey.field}}`→该节点 config.output_schema 同名字段、`{{workflowKey.field}}`→subflowSchemas 查表（键 = workflow_id 字符串，NodeInspector 编辑期喂入既有 subflowSchemaCache）、裸引用与查不到来源→string）、description 取首个产出该字段的映射行值、无映射 end 时返回空 schema；**推导只在 NodeInspector 编辑时执行**（analyze F1：节点删除/更名不自动重推导——host 侧拿不到 subflow 缓存会把正确类型退化 string，stale schema 由运行期 validateOutputSchema 兜底、重开映射编辑器即刷新）；类型注解齐全；门禁 type-check + build 绿
- [x] T013 [US2] GREEN（前端编辑器）：`web/src/views/workflow/NodeInspector.vue` end 节点（hostType=task）检查器改双模式——映射模式：字段行 {name, ref(变量下拉，整值限单一 {{ref}}), description} + 类型推导只读列（复用既有 variableGroups 数据源 + deriveOutputSchema 同源推导规则）+ 行增删；高级模式：既有单一 output TemplateField；模式互斥切换（切换清另一模式数据前确认）；初始模式反解析（config.output_fields 非空→映射 / 仅 output→高级 / 全新→映射）；chat 型 end 保持现状单一 output 模板；保存时（config 写出 + host 图级 output_schema 更新事件）经 deriveOutputSchema 同步并集 schema、conflict 时阻止保存并提示；门禁 type-check + build 绿

**Checkpoint**: US2 独立可测——`go test ./internal/workflow/service/ -race -run 'BuildOutput|TemplateRefs' -v` 全绿 + 手测 quickstart 场景 B（含多 end 分流）

---

## Phase 5: User Story 1 - 画布开始节点 (Priority: P1)

**Goal**: 伪开始节点画布渲染与点击配置入口，entry 语义零改动（US1 / FR-001 / FR-002）

**Independent Test**: task 型画布断言开始节点默认渲染、角标入参数正确、点击开 schema 面板、保存序列化零泄漏；chat 型只读说明（quickstart 场景 A）

### Implementation for User Story 1

- [x] T014 [US1] GREEN（画布渲染）：`web/src/views/workflow/CanvasEditor.vue`——Vue Flow 画布增固定伪开始节点（id=START_NODE_ID、data.nodeType=START_NODE_TYPE，复用既有哨兵与 isStartContextNode 守卫）：固定坐标（左侧锚位，min-x 不与真实节点冲突时可取真实节点最小 x - 偏移）、`draggable: false` / `connectable: false`、删除/连线守卫维持既有 isStartContextNode 分支、点击 → 既有 openSchemaPanel（选中哨兵即检查器 schema 面板）；节点视觉用既有 hf-wf-node 家族 class + 副标签（task 型「N 入参」/ chat 型「单一入参 {{input}}」，N = host inputSchema 长度，随 schema 更新响应式）；左面板「入参 / 出参」按钮移除（openSchemaPanel 入口由节点承担；chat 型同样可经节点查看只读说明——沿用既有面板 chat 分支）；确认序列化函数（toPayload/graph 序列化路径）零改动即零泄漏（伪节点不在 nodes 数组）；门禁 type-check + build 绿

**Checkpoint**: US1 独立可测——手测 quickstart 场景 A

---

## Phase 6: User Story 4 + Polish - 入口收敛与全量验收

**Goal**: output_schema 编辑入口收敛 + 零回归证明 + 验收门 + 文档（US4 / FR-005 / SC-001~SC-006）

- [x] T015 [US4] GREEN（入口收敛）：`web/src/views/workflow/WorkflowCreate.vue` 第一步表单移除 output_schema 行（input_schema 保留含默认值列）；`web/src/views/workflow/WorkflowEdit.vue` drawer 移除 output_schema 与 input_schema 编辑（保留 name/description，schema 编辑统一走画布开始节点 / end 映射；GraphModeEditor 的 update:inputSchema/update:outputSchema 事件链保持——JSON 模式契约不变）；`WorkflowOrchestrate.vue` / workflowCreateDraft 草稿 store 持第一步 schema 的路径改为仅透传（草稿保留 input_schema、output_schema 随收敛移除）；门禁 type-check + build 绿
- [x] T016 全量验收门（SC-005）：`go build ./... && go vet ./... && go test ./... -race -count=1` 全绿；`go test ./internal/workflow/... -race -cover` workflow/api 与 workflow/service ≥80%；`go test ./internal/chat/... -race -count=1` 绿（零改动透传证明）；`make migrate-status` 21 applied 不变、migrations/ 零改动（`git status` 核对）；`cd web && npm run type-check && npm run build` 绿 + package.json / tokens.css 零改动；红线 grep：`git diff --stat` 确认 internal/chat、internal/workflow/store、internal/workflow/handler、internal/app/server.go 零触碰
- [x] T017 [P] 文档同步（手测落位）：`docs/testing/` 既有 workflow 手测文档新增 spec 018 小节（无则新建 workflow-manual-test.md）——quickstart 场景 A/B/C/D 四节逐条可勾选；spec.md 状态头若标 Draft 遗留则确认 Approved；CLAUDE.md 工作流相关节无新契约需同步（零新错误码/零新路由——仅《模块内部结构》workflow config 注记可选加一行 output_fields 形态，加则同步 docs/design/data-model.md 不涉）

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: 无依赖，立即开始
- **Foundational (Phase 2)**: 依赖 Phase 1；内部顺序 T002 → T003（严格 RED→GREEN），T004 与 T003 可并行（前端类型只依赖冻结契约文档）
- **US3 (Phase 3)**: 依赖 T003（SchemaField.Default 已存在）；T005 → T006 严格 RED→GREEN；T007 ∥ T008 ∥ T006 可并行（不同文件）
- **US2 (Phase 4)**: 依赖 T003；T009 → T010 严格 RED→GREEN；T011 依赖 T003（EndConfig 新键）；T012 ∥ T010 可并行；T013 依赖 T012（deriveOutputSchema）
- **US1 (Phase 5)**: 依赖 T007（开始节点面板含默认值列）；与 Phase 4 可并行（不同文件族）
- **US4/Polish (Phase 6)**: 依赖全部；T015 → T016 → T017 顺序（收敛后跑验收门）；T017 ∥ T016 可并行

### User Story Dependencies

- **US3 (P1)**: Foundational 后即可开始，无跨故事依赖——默认值链路端到端独立可测
- **US2 (P1)**: 复用 Foundational 契约（EndConfig 键），与 US3 完全并行
- **US1 (P1)**: 仅依赖 US3 的 T007（schema 面板默认值列），画布渲染本身独立
- **US4 (P2)**: 收敛 + 验收，依赖全部故事完成

### Parallel Opportunities

- T004 ∥ T003；T007 ∥ T008 ∥ T006；T012 ∥ T010；Phase 3 ∥ Phase 4（后端不同函数族 + 前端不同文件）；T017 ∥ T016

---

## Implementation Strategy

### MVP First（US3 + US1）

1. Phase 1 基线 → Phase 2 契约层（T002–T004）
2. Phase 3 US3 默认值链路（T005–T008）——先交付：默认值三处生效即 quickstart 场景 A/C 可人工验
3. **STOP and VALIDATE**: `go test ./internal/workflow/service/ -race -run 'AssembleInput|NormalizeInput' -v` + 场景 C 手测
4. Phase 4 US2（end 映射引擎 + 前端编辑器）→ Phase 5 US1（开始节点）→ Phase 6 收敛 + 验收门 + 文档

### 增量交付说明

US3+US1 构成 MVP（开始节点可见 + 默认值闭环，任务型 workflow 入参体验完整）；US2 补结构化输出（终稿 JSON 可编程消费，与 US3 无耦合）；US4 是收敛与零回归证明（验收门明确项，不可跳）。TDD 纪律：T002/T005/T009/T011 先写测试运行确认失败再实现。

---

## Notes

- 任务级 DoD：后端任务过 `go build ./... && go vet ./... && go test ./... -race -count=1`；前端任务过 `cd web && npm run type-check && npm run build`；红了当场修不攒批
- 反作弊红线：不得删断言 / t.Skip / 调低覆盖率门槛；实现错修实现，认为 spec/测试错走软门禁停下报告
- 冻结契约逐字保真：C1（default 三处生效语义 / required 禁配）、C2（互斥拒 / ref 单一 {{var}} / buildOutput 构造语义 / 多 end 并集）、C3（O1 单 string 不变量 / R10 基名规则 / 隐式终止 / chat 零改动）
- commit 由用户明示；可提交节点 message：`feat(workflow): 画布开始/结束节点与入参默认值——伪 start 节点 + end 输出映射 + SchemaField default（spec 018）`
