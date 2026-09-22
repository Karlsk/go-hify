# Quickstart: 工作流 LLM 节点 system_prompt 支持——验证走查

**Date**: 2026-09-22 | **Spec**: [spec.md](spec.md)

## 前置

- Go 1.26 环境就绪；本分支 `011-workflow-llm-system-prompt`。
- 无需数据库/Redis/LLM key——全部验证走同包单测（stub 隔离）；手测项需 `make start ENV=dev` 起本地栈（见末节）。

## 自动化验证（实现完成的定义）

```bash
# 全量门禁（预期：全部 ok，零 FAIL）
go build ./... && go vet ./... && go test ./... -race -count=1

# 本模块覆盖率（预期：≥80%，与合入前持平）
go test ./internal/workflow/... -race -cover

# 关键回归单测（预期：PASS——消息序断言见用例名）
go test ./internal/workflow/service/ -race -run 'TestLLM.*SystemPrompt|TestCallLLM' -v
```

## 用例 ↔ 验收点对号

| 用例组 | 验收点 |
|---|---|
| 带 system_prompt 执行 → stub client 断言收到 [system, user]、内容为渲染后文本 | SC-001 / FR-003 / US1-1 |
| system_prompt 含 `{{input.question}}` 渲染替换 | FR-002 / US1-2 |
| system_prompt 缺失变量 → ErrValidationFailed、文案含变量名与节点 key、Generate 零调用、executions 零落 | SC-003 / FR-004 |
| system_prompt 空串 → 单 user 消息、记录无新键 | FR-003 / US2-1 |
| 无 system_prompt 存量用例（既有断言不改）全绿 | SC-002 / SC-004 / US2 全部 |
| executions.Input 恰 {"prompt":...}（无值路径）/ 含 system_prompt（有值路径） | FR-005 |
| 嵌套子图内 LLM 节点同样生效 | US1-4 |
| LLMConfig 序列化往返：空串不出键 | FR-006 |

## 手测（可选冒烟，dev 栈）

1. `make start ENV=dev` 起本地栈；用既有手测流程（[docs/testing/workflow-engine-manual-test.md](../../../docs/testing/workflow-engine-manual-test.md)）创建一个单 LLM 节点 + end 的工作流，config 里加 `"system_prompt": "你是分类器"`、`"prompt": "{{input.question}}"`。
2. `POST /api/v1/workflows/{id}/execute?trial=true`（body 含 input.question）→ 200，观察回复风格是否符合 system 设定（真实模型侧验证，单测覆盖不到的部分）。
3. 查 `workflow_node_runs.node_in` / `executions` 行：含渲染后的 system_prompt 值。
4. 同图去掉 system_prompt 再执行 → 记录形态回到 {"prompt":...}。

## 预期结果

- 全部命令绿；本篇零迁移、零路由、零哨兵变更——`git diff` 只应见 `internal/workflow/api/schema.go`、`internal/workflow/service/executor.go` 及两个同包测试文件。
