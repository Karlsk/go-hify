# Quickstart: 验证 Agent → Workflow 绑定

**Date**: 2026-09-17 | 验收门权威: [impl_spec_05 §8/§9](../../docs/changelog/workflow/impl_spec_05_agent_binding.md)

## 前置

- 基线绿：`go build ./... && go vet ./... && go test ./... -race -count=1`
- 测试 PG 容器在位（5433）：`docker start hify-pg-test`（若未运行）
- 当前迁移 17 条 applied（`make migrate-status`）

## 机器验证（实现完成后逐条跑）

```bash
# 1. 迁移：18 条全部 applied，既有 00001-00017 未动
make migrate-up && make migrate-status

# 2. 全量门禁
go build ./... && go vet ./... && go test ./... -race -count=1

# 3. 两模块覆盖率各 ≥80%
go test ./internal/agent/... -race -cover
go test ./internal/workflow/... -race -cover

# 4. 依赖方向红线：agent 不 import workflow（应无输出 = OK）
grep -rn "internal/workflow" internal/agent/ && echo "VIOLATION" || echo "OK"

# 5. 迁移只增不改（M = 违规）
git status --porcelain migrations/
```

**预期**: 1 → `18` applied / 无 pending；2/3 → 全绿且两模块 ≥80%；4 → `OK`；5 → 仅 `A`/`??`（新增 00018）。

关键回归单测（对应 impl spec §8）：

```bash
go test ./internal/agent/service/ -race -run 'FK|Workflow' -v   # 23503 约束名分发表驱动
go test ./internal/workflow/... -race -run 'Delete|InUse' -v    # Delete 23503 → ErrWorkflowInUse
```

## 人工冒烟（`make start` 后，可选——对应 T8）

前置：已建 provider/model、workflow（如 id=3）、agent（如 id=1）。

```bash
# 绑定（PUT 全量体携带 workflow_id；name/model_id 等须全量提交）
curl -X PUT localhost:8081/api/v1/agents/1 -H 'Content-Type: application/json' \
  -d '{"name":"客服助手","model_id":1,"workflow_id":3, ...}'

# 回显：GET /agents/1 → data.workflow_id == "3"
curl localhost:8081/api/v1/agents/1

# 绑不存在的 workflow → 404 WORKFLOW_NOT_FOUND
curl -X PUT localhost:8081/api/v1/agents/1 -H 'Content-Type: application/json' \
  -d '{... "workflow_id":999 ...}'

# 删除被绑定的 workflow → 409 WORKFLOW_IN_USE
curl -X DELETE localhost:8081/api/v1/workflows/3

# 解绑后再删 → 成功（nodes/edges 级联清理）
curl -X PUT localhost:8081/api/v1/agents/1 -H 'Content-Type: application/json' \
  -d '{... "workflow_id":null ...}'
curl -X DELETE localhost:8081/api/v1/workflows/3
```

完整冒烟清单（绑/解绑/404/409）落 `docs/testing/agent-manual-test.md`「workflow 绑定」小节（T8，可选）。
