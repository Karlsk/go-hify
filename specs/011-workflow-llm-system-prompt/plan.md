# Implementation Plan: 工作流 LLM 节点 system_prompt 支持（011-workflow-llm-system-prompt）

**Branch**: `011-workflow-llm-system-prompt` | **Date**: 2026-09-22 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/011-workflow-llm-system-prompt/spec.md`（含 2026-09-22 Clarifications：三项产品分叉用户已拍板——start 前端伪节点 / **LLM 拆分走本篇后端加法** / api auth 走 headers 预设；默认决策——空串等价缺省、记录键有值才加、system_prompt 渲染先于 prompt）。

## Summary

工作流引擎 LLM 节点支持可选 `system_prompt`：`LLMConfig` 加 `SystemPrompt string`（json `system_prompt,omitempty`），executor `callLLM` 在字段非空时先按 strict 语义渲染、作为 system 消息置于 user 消息（渲染后的 `prompt`）之前发给模型；为空/缺省路径与现状逐字节一致。排障记录（node_in 摘要 + executions 行 Input）有值时带渲染后的 `system_prompt` 键，无值不新增键。**纯加法、零迁移、零跨模块改动、零组合根改动**——全部改动收敛在 `internal/workflow` 的 api/schema.go 与 service/executor.go 两个文件及其同包测试。本篇是对 impl_spec_06 已冻结 LLM 节点契约的加法修订（用户 2026-09-22 显式批准），是 spec 012（前端 LLM 检查器两输入框）的后端前提。

## Technical Context

**Language/Version**: Go 1.26（module `github.com/Karlsk/go-hify`）

**Primary Dependencies**: 全部既有——Gin / GORM / cloudwego eino v0.9.x（`schema.Message` 角色常量与 `schema.SystemMessage` 构造器，chat 模块 turn.go:354 已有使用先例）；**本篇零新增依赖**

**Storage**: 无变更——LLM 节点 config 为 `workflows.graph_config` jsonb 列内容演进，存储层无感知；零迁移（当前 applied 20 条，下一号 00021 本篇不用）

**Testing**: 同包 `*_test.go`：`internal/workflow/api`（schema 序列化往返）、`internal/workflow/service`（executor 行为，stub ChatModel client + stub store）；门禁 `go build ./... && go vet ./... && go test ./... -race -count=1` 全绿 + `internal/workflow` 覆盖率 ≥80% 维持

**Target Platform**: Linux 容器（Docker Compose 单机，Go 单二进制）

**Project Type**: 模块化单体后端（internal/workflow 模块内改动）

**Performance Goals**: 无新指标——单 LLM 节点多渲染一个短模板（strict 渲染为纯内存正则替换），开销可忽略

**Constraints**: 冻结契约逐字对齐 spec FR-001~FR-006；既有测试断言一条不改（存量零差异是验收内容本身，SC-002）；错误链路三层不吞错；service 层无 gin 类型；事务不涉（无 store 改动）

**Scale/Scope**: 2 个源文件改动（api/schema.go + service/executor.go）+ 2 个测试文件增改；无新文件、无导出面变化（字段加在既有导出 struct 上）

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| 原则 | 判定 | 说明 |
|---|---|---|
| I Simplicity First | PASS | 零新依赖零迁移零新文件；加一个字段 + 一个 if 分支，无任何抽象层；「明确不做」清单无新增偏离 |
| II 模块化单体与单向依赖 | PASS | 改动全部在 workflow 模块内；不新增任何 import（eino schema 已在用）；api 包纯契约保持 |
| III platform/llm 单点 | PASS | 消息组装在调用点、调用仍经 platform/llm client；无重试/超时逻辑外泄 |
| IV SSE 流式链路 | PASS | 不涉（workflow LLM 节点为非流式 Generate，现状不变） |
| V 数据库纪律 | PASS | 零迁移零 SQL；config 为 jsonb 内容演进 |
| VI 可观测与成本护栏 | PASS | executions 记录增强（有值带 system_prompt）恰是可观测性要求 FR-005；fail-fast 于上游调用前不浪费额度 |
| VII 统一契约与安全基线 | PASS | 无新路由无新哨兵；ErrValidationFailed 既有映射复用；system_prompt 为普通模板文本，不涉凭据 |

**Phase 1 复查**: 设计未引入新实体/新接口/新文件，各原则维持 PASS。

## Project Structure

### Documentation (this feature)

```text
specs/011-workflow-llm-system-prompt/
├── plan.md              # 本文件（/speckit-plan 命令产出）
├── research.md          # Phase 0：三项实现决策（渲染顺序 / 记录形态 / eino 角色构造）
├── data-model.md        # Phase 1：LLMConfig 字段演进（无表变更）
├── quickstart.md        # Phase 1：验证走查指南（单测 + 手测）
├── contracts/
│   └── llm-node-config.md   # LLM 节点 config 契约（加法修订版）
├── checklists/
│   └── requirements.md  # /speckit-specify 质量清单
└── tasks.md             # /speckit-tasks 产出（本命令不创建）
```

### Source Code (repository root)

```text
internal/workflow/
├── api/
│   └── schema.go            # 改：LLMConfig 加 SystemPrompt string `json:"system_prompt,omitempty"`
│                            #     （置于 Prompt 之前，与字段语义序一致）；Validate 不变
│   └── schema_test.go       # 改：+ LLMConfig 序列化往返用例（有值出键 / 空串无键，FR-006）
└── service/
    ├── executor.go          # 改：callLLM——system_prompt 非空时先 render（失败 fail-fast 同
    │                        #     prompt 语义）→ msgs 组装 [system, user]；setNodeIn 摘要与
    │                        #     recordExecution 的 Input 有值带 system_prompt
    └── executor_test.go     # 改：+ 用例组（消息序 / 模板渲染 / fail-fast / 空串等价 / 嵌套 /
                             #   存量回归不动既有断言）
```

**Structure Decision**: 模块内两文件定点改动，不新增目录/文件/包——与「好维护优先」一致；测试落在被测代码同包（仓规）。

## Complexity Tracking

> 无 Constitution Check 违规项，不需要登记。
