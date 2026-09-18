# Quickstart: Workflow 执行引擎验证指南

验证本特性端到端可用的最小路径。契约细节见 [contracts/execute-api.md](./contracts/execute-api.md)，表结构见 [data-model.md](./data-model.md)。

## 前置

- Go 1.26、本地 PG17 + Redis（`make start ENV=dev` 或既有 dev 环境）
- 前置 spec 01-05 已合入（commit `c06a79b`）；基线全绿

## 1. 迁移（机器验证）

```bash
make migrate-status          # 预期：19 条 applied（00019_workflow_runs 新增）
```

## 2. 全量门禁（机器验证）

```bash
go build ./... && go vet ./... && go test ./... -race -count=1
# 预期：全绿；重点包 internal/workflow/{api,service,store,handler} 全 ok
go test ./internal/workflow/... -race -cover    # 预期：各包 ≥80%
grep -rn "internal/chat" internal/workflow/ && echo "VIOLATION" || echo "OK"   # 预期：OK
```

## 3. 关键行为冒烟（需登录态 + 真实/本地 LLM 配置，人工项）

对照 [docs/testing/workflow-manual-test.md](../../../docs/testing/workflow-manual-test.md) 执行测试小节（随本篇补）：

1. **正式执行**：对 published 工作流 `POST /api/v1/workflows/{id}/execute`，body `{"input":"..."}` → 200 信封，`status=succeeded`、`node_trace` 覆盖全部节点、`run_id` 非空。
2. **试运行**：对同一 draft 态副本 `?trial=true` → 200；不带 trial → 503 `WORKFLOW_NOT_PUBLISHED`。
3. **失败定位**：构造 condition 无命中 / 缺失变量的图 → 400，message 带 `node <key>:` 前缀。
4. **轨迹回放**：PG 查 `workflow_runs` / `workflow_node_runs`——seq 递增、失败 run 的 error_node 非空、trial 行 is_trial=true。
5. **SSRF**：api 节点 url 指向 `http://127.0.0.1/...` → 500 `WORKFLOW_EXECUTION_FAILED`。

## 4. 保留期任务冒烟（可选，人工项）

`.env` 设 `WORKFLOW_RUNS_RETENTION_DAYS=0` 重启 → 启动日志出现 WARN 且不清理；设为 365 → 启动首轮清理无报错。

## 预期结果

上述全过 = 执行引擎交付成立：图可运行、错误可定位、轨迹可回放、依赖红线无违规。
