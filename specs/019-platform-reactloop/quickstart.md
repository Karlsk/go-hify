# Quickstart: spec 019 验证场景

**Date**: 2026-10-10 | **Spec**: [spec.md](./spec.md) | **契约**: [contracts/reactloop-api.md](./contracts/reactloop-api.md)

## 前置

- 基线全绿：`go build ./... && go vet ./... && go test ./... -race -count=1`；`make migrate-status` 21 applied
- 本地起服务：`make start`（chat 手测需真实 LLM provider 与一个可用 MCP server——沿用 chat-manual-test 工具批前置造数）

## 自动化验证（SC-002~SC-005）

```bash
# 全量门禁
go build ./... && go vet ./... && go test ./... -race -count=1

# 循环单测场景族（platform 包独立，不 import chat）
go test ./internal/platform/reactloop/ -race -v

# 覆盖率（两包各 ≥80%）
go test ./internal/platform/reactloop/ ./internal/chat/service/ -race -cover

# chat 全量回归（零行为变化证明：既有测试零改动通过）
go test ./internal/chat/... -race -count=1

# grep 三件（SC-003 / SC-005）
grep -rn "maxToolRounds\|maxToolResultRunes\|maxToolRoundsNudge\|invalidToolArgsMsg\|finishReasonMaxIterations" internal/chat/   # 预期：零命中
grep -rn "cloudwego/eino" internal/ | grep -v platform/llm    # 预期：空（test 除外）
grep -rn "modelcontextprotocol/go-sdk" internal/ | grep -v mcp/service   # 预期：空
go list -deps ./internal/platform/reactloop | grep hify       # 预期：仅 platform/llm 与 platform/tool
```

## 端到端场景（SC-001，人工过——chat-manual-test.md 工具批小节零差异复测）

### 场景 A：多轮工具对话帧序与落库

1. 绑定 MCP 工具的 Agent 发一条触发两轮工具调用的消息（流式）→ 断言 SSE 帧序 delta* → tool_call → tool_result → delta* → tool_call → tool_result → delta* → done，usage 为各轮累计
2. 查 messages 表：assistant 中间行（正文+tool_calls）先于 tool 行、ToolCalls[0] 锚点形态与重构前一致；刷新会话历史回放工具卡正常（spec 017 面零回归）
3. 同消息改一次输出模式重发 → 信封一次性返回终答、messages 中间行/tool 行照常落库

### 场景 B：上限与失败语义

1. 构造诱导模型连续调用工具的 prompt（或临时把 MaxRounds 经单测注入小值验证）→ 断言 nudge 收尾：done 事件 finish_reason = max_iterations、nudge 文本不出现在任何落库行与 SSE 正文
2. 工具返回错误（MCP server 侧制造 isError）→ 对话不中断、错误文本喂回后模型自纠终答
3. 对话中途关闭浏览器（断连）→ 服务端循环停止剩余工具调用、无 error 事件风暴、日志静默路径同现状

### 场景 C：非流式模式（单测面，020 前置）

- `loop_test.go` 双模式一致用例：同图景 Stream/Generate 两跑，断言 Content/Usage/HitLimit/工具轮消息序一致、仅 delta 回调有无之差

## 预期产出核对

- [ ] 全量门禁绿 + reactloop/chat-service 覆盖率 ≥80%
- [ ] grep 三件全空 + chat 常量零残留
- [ ] chat 既有测试零改动全绿
- [ ] 场景 A/B 人工零差异复测通过
