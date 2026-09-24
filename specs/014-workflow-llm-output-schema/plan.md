# Implementation Plan: LLM 节点输出字段声明与变量下拉展开

**Branch**: `014-workflow-llm-output-schema` | **Date**: 2026-09-23 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/014-workflow-llm-output-schema/spec.md`

## Summary

给 llm 节点 config 增可选 `output_schema` 加法键（复用 task 型 SchemaField 形态），三面交付：① 后端执行器——渲染后 user 消息末尾自动追加固定 JSON 输出指令（记录实发文本）、Generate 后按声明严格校验回复（语义逐字对齐 validateOutputSchema 家族）；② 前端编辑器——llm 表单复用 SchemaFieldsEditor 声明输出字段、变量下拉对已声明祖先 llm 节点展开 `{{key.field}}` 条目；③ 存量零影响——未声明节点消息序列 / 记录 / 序列化逐字节不变。三项拍板（2026-09-23）：严格校验、自动注入、完整字段形态。

## Technical Context

**Language/Version**: Go 1.26（后端）；Vue 3 + TypeScript + Element Plus（前端，spec 009/010/012 既有栈）

**Primary Dependencies**: Gin + GORM（pgx）；Vue Flow + Element Plus。零新增第三方依赖。

**Storage**: PostgreSQL 17（workflows.config jsonb 加法键）。**零迁移**——动手前 `make migrate-status` 确认 20 条 applied、下一号 00021（本篇不新增）。

**Testing**: `go test ./... -race -count=1`（同包 *_test.go，stub llm client / stub store，零真实网络 / LLM / PG）；前端 `cd web && npm run type-check && npm run build`（无单测基建，不引入）。

**Target Platform**: Docker Compose 单机（后端单二进制 + nginx 托管前端产物）

**Project Type**: 模块化单体（workflow 模块四层 + web/ 前端）

**Performance Goals**: 无新热路径（校验与注入为每 llm 节点一次 O(字段数) 纯函数调用）

**Constraints**: 未声明节点行为逐字节不变（SC-003）；workflow 模块覆盖率 ≥80% 维持；前端设计 token 冻结合规

**Scale/Scope**: 后端 2 文件 + 测试 2 文件；前端 2 文件 + 文档 2 文件

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| 原则 | 判定 | 依据 |
|---|---|---|
| I. Simplicity First | ✅ PASS | 复用 SchemaField / ValidateSchemaFields / SchemaFieldsEditor / probeSchemaType，零新抽象零新依赖零新项目 |
| II. 模块化单体与单向依赖 | ✅ PASS | 改动全在 workflow 模块（api/service）+ web/，无跨模块 import |
| 迁移纪律（禁 AutoMigrate、SQL 只增不改） | ✅ PASS | 零迁移（jsonb 加法键） |
| 无软删 / FK RESTRICT 默认 | ✅ PASS | 不触 |
| 错误链路三层不吞错 | ✅ PASS | 校验失败 service 层包装既有 `errs.ErrValidationFailed`，零新增哨兵 |
| 明文凭据只在调用瞬间 | ✅ PASS | 不涉（注入文本为系统固定文案） |
| 前端 token 冻结 | ✅ PASS | 新表单区全用既有 token 与既有组件 |

## Project Structure

### Documentation (this feature)

```text
specs/014-workflow-llm-output-schema/
├── plan.md              # 本文件
├── research.md          # Phase 0：六项设计决策
├── data-model.md        # Phase 1：声明实体 + 前端条目形态
├── quickstart.md        # Phase 1：验证场景
├── contracts/           # Phase 1：api.md（后端契约面）+ frontend.md（组件面）
└── tasks.md             # Phase 2（/speckit-tasks 产出）
```

### Source Code (repository root)

```text
internal/workflow/
├── api/
│   ├── schema.go          # LLMConfig.OutputSchema []SchemaField（omitempty）+ Validate 挂 ValidateSchemaFields
│   └── schema_test.go     # 序列化往返三态 + Validate 新键拒绝路径
└── service/
    ├── executor.go        # callLLM：buildJSONDirective 注入 + validateLLMOutput 校验
    └── executor_test.go   # 注入位置文案 / 校验四分支 / 未声明逐字节零变化 / 嵌套 / 记录实发

web/src/
├── api/workflow.ts        # llm config 前端类型补 output_schema?: SchemaField[]
└── views/workflow/
    └── NodeInspector.vue  # llm 表单输出字段区（SchemaFieldsEditor）+ variableGroups llm 字段展开

docs/
├── changelog/workflow/api_contract.md                      # §3 加法修订注记
└── testing/workflow-frontend-manual-test.md                # 增补小节
```

**Structure Decision**: 单仓库既有结构，后端 workflow 四层内 2 文件、前端 2 文件、文档 2 文件；无新目录无组合根改动。

## Complexity Tracking

> 无 Constitution Check 违例，无需登记。

**已知旁路缺陷（非本篇范围，待用户裁定）**：`web/src/views/workflow/NodeInspector.vue:856` 模板字符串含原始 NUL 字节（subflowSelection 缓存键分隔符；HEAD 与工作区均有）——运行时无害（JS 字符串合法分隔符），但源文件被判二进制、污染 grep/diff 工具链，且 spec 014 将大改该文件。建议前置一笔单行修复：原始 0x00 字节替换为 `\x00` 转义序列（运行时零变化、文件回归纯文本），在 tasks 软停点一并确认。
