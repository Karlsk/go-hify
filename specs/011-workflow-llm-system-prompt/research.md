# Research: 工作流 LLM 节点 system_prompt 支持

**Date**: 2026-09-22 | **Spec**: [spec.md](spec.md)

本篇为小型加法契约修订，Phase 0 仅三个实现级决策，全部可从仓内现状直接裁定，无外部调研需求。

## 决策 1：system 消息的构造方式

- **Decision**: 沿用 eino `schema.Message{Role: schema.System, Content: ...}` 形态组装（与 callLLM 现有 `[]*schema.Message{{Role: schema.User, ...}}` 字面风格一致）。
- **Rationale**: callLLM 现有 msgs 即结构体字面量构造，最小 diff；`schema.SystemMessage()` 便捷构造器与字面量等价，chat 模块 turn.go:354 用的是构造器，executor 用字面量——保持局部风格统一即可。
- **Alternatives**: 引入消息构造辅助函数（否决——只有一处两元素的组装，函数化是无效抽象）。

## 决策 2：渲染顺序与 fail-fast 语义

- **Decision**: `system_prompt` 先渲染、`prompt` 后渲染，任一失败即返回 `ErrValidationFailed`（strict 报首个缺失变量，与现状同错误路径）；两者均成功才进入 ResolveLLMConfig / client 获取 / Generate。
- **Rationale**: spec Clarifications 已固定「system_prompt 渲染先于 prompt」；渲染顺序对可观测行为无影响（任一缺失都 fail-fast），只固定实现顺序避免歧义。渲染全部前置保证「图缺陷不浪费供应商额度」（现状语义，FR-004）。
- **Alternatives**: 仅渲染 prompt、system_prompt 延迟到 msgs 组装时（否决——两个渲染点分裂，错误路径不对称）。

## 决策 3：排障记录形态（node_in 与 executions.Input）

- **Decision**: `setNodeIn` 摘要与 `recordExecution` 的 `Input` map 在 system_prompt 有值时各加 `system_prompt` 键（值为渲染后文本）；无值/空串不加任何键。`recordExecution` 签名相应把 prompt 参数扩展为携带两个渲染后值（struct 或双参数，实现期取最小 diff 形态）。
- **Rationale**: 「有值才带键」保证存量记录形态逐字节不变（SC-004 / FR-005 / FR-006 同一语义的三处落点）；executions 是排障唯一线索，system_prompt 实际生效值必须可读。
- **Alternatives**: 恒定带 `system_prompt: ""`（否决——污染存量形态，违反 SC-004）。

## 依赖核实（写前门禁预演）

- eino `schema.System` 角色常量与消息模型：`go/pkg/mod/github.com/cloudwego/eino@v0.9.x/schema/message.go` 确认存在；仓内 go.mod 已有 eino v0.9.x，无新增依赖。
- `go build ./... && go vet ./... && go test ./... -race -count=1` 基线全绿（2026-09-22 开工前实测）。
- 迁移：`make migrate-status` applied 20 条，本篇零迁移，无编号冲突。
