# Implementation Plan: Workflow 画布开始/结束节点与入参默认值

**Branch**: `018-workflow-start-end-nodes` | **Date**: 2026-10-09 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/018-workflow-start-end-nodes/spec.md`（2026-10-09 定稿 + Clarifications 两问回写：多 end 映射放开、required 自动推导）

## Summary

三块加法交付：① 画布伪开始节点——复用 spec 012 遗留的 `__start__` 哨兵管道恢复画布渲染（固定左侧、不可删不可拖、不进序列化），点击打开既有检查器 schema 面板，左面板「入参/出参」按钮移除；entry 语义（`start_node_key` + 三入口）零改动。② end 输出映射——`EndConfig` 加法键 `output_fields: [{name, ref}]`（与 `output` 互斥拒），`buildOutput` 优先按引用逐字段 lookup 取值、按图级 output_schema 声明类型处理（string 走 json.Marshal 转义 / number+boolean 取原 JSON 文本 / 未声明回退可解析则原样）后整对象 Marshal 产出终稿；多 end 各自映射，图级 output_schema = 并集、required 自动推导（全部映射 end 都产出 = required），类型推导在前端（input_schema / llm output_schema / 子工作流 output_schema 三来源，裸引用 string）；R10 覆盖 output_fields ref 基名。③ SchemaField 加 `default`（json.RawMessage，required 禁配、类型匹配校验），`assembleInputJSON` optional 缺失取默认、顶层 Execute 新增 `normalizeInputJSON` 纯函数归一化（只补默认、非 JSON / 非对象 / 未声明字段照旧放行），试运行表单预填 + 清空「optional 且有 default」的行从提交 JSON 省略（无默认 optional 行空值照传，零回归）。零迁移（恒 21 applied）、零新依赖、零新错误码、chat / store / handler / 组合根零改动。

## Technical Context

**Language/Version**: Go 1.26（后端单二进制）+ Vue 3 / TypeScript / Element Plus / Vue Flow（前端既有栈）

**Primary Dependencies**: Gin + GORM（既有）；`internal/workflow/api` 契约层与 `internal/workflow/service` 执行引擎（本篇唯一后端改动面）。零新增第三方依赖。

**Storage**: PostgreSQL 17——**零迁移**：`workflows.input_schema/output_schema` 与 `workflow_nodes.config` 均为 jsonb，`default` / `output_fields` 是 JSON 内加法键，`make migrate-status` 恒 21 applied（00020 的 config 列注释形态说明以本篇 data-model.md 为准补注，不改迁移文件）。

**Testing**: 后端 `go test ./... -race -count=1`，同包 `*_test.go`，stub 隔离零真实 PG/Redis/网络/LLM；`workflow/api` 与 `workflow/service` 覆盖率 ≥80%。前端 `cd web && npm run type-check && npm run build`（package.json 零改动）。chat 模块全量回归绿（零改动透传证明）。

**Target Platform**: Docker Compose 单机（后端单二进制 alpine）+ 浏览器 SPA

**Project Type**: 模块化单体（workflow 模块 api/service 层 + 前端 workflow 视图族）

**Performance Goals**: 无新热路径——normalizeInputJSON 只在 task 型 + 声明 schema 时一次 json.Unmarshal；buildOutput 每字段一次 lookup（一级下钻即一次 JSON 解析，图 ≤50 节点 / 字段行个位数，成本可忽略）。

**Constraints**: 冻结契约（spec FR-001~011 + Clarifications）：`SchemaField.default` 值类型恒等于 field type 且 required 禁配；`EndConfig = {output?, output_fields?}` 两键互斥；normalizeInputJSON 只补默认不新增拒绝；R10 基名规则不变（`{input}` ∪ 祖先）；设计 token 只增不改（本篇零新增）；依赖方向不变（workflow 不新增任何跨模块 import）。

**Scale/Scope**: 后端 5 源文件（api/schema.go、service/execute.go、service/executor.go、service/execcontext.go 加法字段、service.go R10 分支）+ 4 测试文件加法；前端 8 文件（api/workflow.ts、graph.ts、CanvasEditor.vue、NodeInspector.vue、SchemaFieldsEditor.vue、WorkflowCreate.vue、WorkflowEdit.vue、WorkflowTrialDialog.vue）。

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| 原则 | 判定 | 依据 |
|---|---|---|
| I. Simplicity First | ✅ PASS | 全部为 jsonb 加法键 / 纯函数 / 既有哨兵复用，零迁移零新依赖零新抽象；clarify 修正（多 end 放开）反而删除了原 D3「唯一映射 end」的限制机制，净简化；D1 反转 spec 012 有裁定留痕（spec 裁定反转记录 + Clarifications） |
| II. 模块化单体与单向依赖 | ✅ PASS | 改动全在 workflow 模块内部（api/service 两层）+ 前端视图；零新跨模块 import；service 全程无 gin 类型；execContext 加法字段属模块内部形态 |
| III. 统一 LLM 接入层 | ✅ PASS | 不涉 LLM 调用（buildOutput 是纯内存构造）；llm 节点 output_schema 只读消费其声明类型 |
| IV. SSE 链路 | ✅ PASS | 不涉 SSE（workflow 执行单次返回；chat 管道零改动，终稿语义不变） |
| V. 数据库纪律 | ✅ PASS | 零迁移零新表零 SQL 改动（jsonb 内加法键，DB 模型 Workflow 结构体不动——schema 列本就是 *string 透传） |
| VI. 可观测与成本护栏 | ✅ PASS | 顶层归一化后的 input 进 run 轨迹（实际执行值可追溯）；end 节点 nodeIn 摘要记 output_fields（排障对齐既有 buildOutput 的 `{"output": ...}` 形态） |
| VII. 统一契约与安全基线 | ✅ PASS | 零新错误码（行级校验失败走既有 `errs.ErrValidationFailed` 400 路径，经 errInvalidNodeConfig → handler 既有翻译）；字段 snake_case json tag；bigint ID 序列化不涉 |

## Project Structure

### Documentation (this feature)

```text
specs/018-workflow-start-end-nodes/
├── spec.md              # 定稿 Feature Spec（含 Clarifications 2026-10-09）
├── checklists/
│   └── requirements.md  # specify 质量清单（16/16）
├── plan.md              # 本文件
├── research.md          # Phase 0：实现级设计决策 R1~R10
├── data-model.md        # Phase 1：jsonb 加法契约形态 + 前端形态
├── contracts/
│   └── jsonb-contract.md # Phase 1：对外可观察契约（本篇唯一契约面 = jsonb 加法键 + 行为语义）
├── quickstart.md        # Phase 1：验证场景
└── tasks.md             # Phase 2（/speckit-tasks 产出）
```

### Source Code (repository root)

```text
internal/workflow/api/
└── schema.go               # SchemaField +Default(json.RawMessage)；ValidateSchemaFields 增两条
│                            #（default 类型探针匹配 field type、required 禁配 default）；
│                            # EndConfig +OutputFields []OutputField；OutputField{Name,Ref}；
│                            # EndConfig.Validate 行级校验（name 非空不重名、ref 单一 {{var}}、
│                            #  output 与 output_fields 互斥拒）——ref 形状校验用包内新 singleVarRE

internal/workflow/service/
├── execcontext.go          # execContext +outputTypes map[string]string（图级 output_schema
│                            #  声明类型表，Execute/executeChild 装配，buildOutputFields 消费）
├── execute.go              # assembleInputJSON：optional 缺失且有 default → 补默认（无则维持省略）；
│                            # normalizeInputJSON 纯函数（合法 JSON 对象 → 补缺失 optional 默认，
│                            #  其余原样返回）；Execute/executeChild 在 newExecContext 前对 task 型
│                            #  调归一化 + 装配 ec.outputTypes；归一化结果同入 runMeta.input
├── executor.go             # buildOutput：len(OutputFields)>0 优先 buildOutputFields（逐字段 lookup
│                            #  取值 → 声明类型优先处理〔string marshal 转义 / number+boolean 原
│                            #  JSON 文本〕→ 未声明回退「可解析 JSON 则原样否则按字符串」→ 整对象
│                            #  json.Marshal）；缺失变量 strict 图缺陷；nodeIn 记 output_fields 摘要
├── service.go              # validateTemplateRefs R10：EndConfig 分支追加 output_fields 各 ref
│                            #  （基名 ∈ {input} ∪ 祖先，点分基名切分同既有规则）
├── schema_test.go (api)    # 见 api/ 目录实测落位
├── execute_test.go         # assembleInputJSON 补默认组 + normalizeInputJSON 边界组
├── executor_test.go        # buildOutputFields 构造 / 转义 / 类型处理 / 缺失变量组
└── service_test.go         # R10 覆盖 output_fields 用例

web/src/api/workflow.ts     # SchemaField +default?: string|number|boolean；EndConfig 类型
│                            #  +output_fields?: { name: string; ref: string }[]
web/src/views/workflow/
├── graph.ts                # 伪开始节点画布形态辅助（复用 START_NODE_ID/START_NODE_TYPE，
│                            #  固定坐标、不进 nodes/序列化）；deriveOutputSchema 纯函数
│                            #（全部映射 end 字段并集 + 同名同型合并 + 同名不同型冲突标记 +
│                            #  required 推导：全部产出 = required）
├── CanvasEditor.vue        # 伪开始节点渲染（task 型角标入参数 / chat 型只读说明）、点击 →
│                            #  openSchemaPanel（既有）；左面板「入参/出参」按钮移除；entry 三入口不动
├── NodeInspector.vue       # end（task 型）映射模式编辑器：字段行 {name, ref, description} +
│                            #  类型推导只读列（variableGroups 三来源 + 裸引用 string）+ 高级模式
│                            #  互斥切换 + 存量反解析定初始模式（有 output_fields → 映射；
│                            #  仅有 output → 高级；全新 → 映射）；保存时经 deriveOutputSchema
│                            #  同步图级 output_schema
├── SchemaFieldsEditor.vue  # 默认值列（仅 optional 行、按 type 切 input / input-number / switch）
├── WorkflowCreate.vue      # 第一步表单 output_schema 移除（input_schema 保留，含默认值列）
├── WorkflowEdit.vue        # drawer 的 output_schema 表单移除（input_schema 入口收敛至画布
│                            #  开始节点；drawer 保留 name/description）
└── WorkflowTrialDialog.vue # task 型有 schema：表单按 default 预填（可改可清空）；清空的
                             #  optional 行从提交 JSON 省略（清空 = 使用默认）
```

**Structure Decision**: 模块化单体四层不动——后端只碰 workflow/api 契约层与 workflow/service 执行层，store / handler / 组合根零改动（无新查询、无新路由、无新装配）；前端只碰 workflow 视图族与 api 类型层。

## Complexity Tracking

> Constitution Check 全 PASS，无 violation 需登记。
