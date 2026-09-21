# Quickstart: workflow 分型与子工作流嵌套验证指南

> 验证对象：`internal/workflow` 分型 CRUD + R11 保存期校验 + sub-workflow 嵌套执行 + 子 run 轨迹。
> 详细人工冒烟（含 psql 树查询）落 [docs/testing/workflow-manual-test.md](../../../docs/testing/workflow-manual-test.md) 嵌套冒烟小节——本篇收尾时新增。

## 前置

```bash
make migrate-status    # 预期 20 条 applied（00020 含内）
go build ./... && go vet ./... && go test ./... -race -count=1   # 全绿
```

## 场景 1：分型 CRUD（人工 curl，需 make start 环境）

1. `POST /api/v1/workflows` 不带 `type` → 400 `VALIDATION_FAILED`。
2. 创建 `{"type":"task", "input_schema":[...], ...}` → 201，`data.type="task"`。
3. `PUT /api/v1/workflows/{id}` 携带 `"type":"task"`（同值）→ 400 拒。
4. 迁移前的存量 workflow → `GET` 返回 `type="chat"`。
5. `type:"chat"` + 非空 `input_schema` → 400。

## 场景 2：R11 嵌套矩阵（人工 curl 或单测复现）

创建 task 子图（声明 input_schema）后，父图（chat 型或 task 型）加 sub-workflow 节点：

| 构造 | 预期 |
|---|---|
| 引用 task 型、inputs 键集恰好覆盖 required | 保存成功 |
| 引用 chat 型 | 400 |
| 自嵌（A 引 A）/ 间接环（A→B→A） | 400 |
| 链深超 3（A→B→C→D） | 400 |
| workflow_id 不存在 | 400 |
| inputs 缺 required / 多余字段；子无 schema 时非 `{input}` | 400 |

## 场景 3：嵌套执行 + 轨迹（人工，需 LLM provider）

1. 发布父图与子图，`POST /api/v1/workflows/{parent}/execute`，input 按子 schema 可渲染。
2. 预期：父终稿含子输出；子 draft 时正式执行 → 503 `WORKFLOW_NOT_PUBLISHED`（带父 node 前缀）；父 trial → 子 draft 放开。
3. psql：`SELECT id, workflow_id, trigger_source, parent_run_id, conversation_id, message_id FROM workflow_runs ORDER BY id DESC;`
   —— 子行 `trigger_source='workflow'`、`parent_run_id` 指向父行、引用列与父一致；`workflow_node_runs` 各 run 内 seq 连续。

## 自动化验证（DoD）

```bash
go test ./internal/workflow/... -race -count=1 -cover   # 各包 ≥80%
grep -rn "internal/chat" internal/workflow/ && echo VIOLATION || echo OK
```
