# Data Model: Workflow 执行引擎（Phase 1）

**权威 DDL**: [db_model.md §12](../../docs/changelog/workflow/db_model.md)（冻结稿，migration 00019 逐字取用）——本文不重复 DDL，只列实体要点与校验规则。

## 实体

### WorkflowRun（workflow_runs，每次运行一行，append-only）

| 字段 | 说明 |
|---|---|
| id | IDENTITY PK；字符串化暴露为 `run_id` |
| workflow_id | **弱引用** workflows（无 FK——保留期日志表不阻碍业务删除；决策 #14） |
| workflow_name | 名称快照：workflow 删除后轨迹仍可读 |
| trigger_source | `console` / `chat`（CHECK） |
| is_trial | 试运行标记（`?trial=true`，O3）：区分测试与真实流量 |
| conversation_id / message_id | chat 触发时弱引用（可空）；console 为 NULL |
| trace_id | 对齐 platform/traceid 日志链 |
| status | `succeeded` / `failed`（CHECK；无 RUNNING 态——收尾统一写） |
| input / output | jsonb / text，单值截断 16KB + truncated 标记 |
| error_node / error_msg | 失败节点 key / 错误信息（成功 = ''） |
| duration_ms / started_at | 耗时与执行起点（created_at = 收尾写入时刻，差值即耗时） |

### WorkflowNodeRun（workflow_node_runs，每执行节点一行，append-only）

| 字段 | 说明 |
|---|---|
| run_id | **唯一 FK** REFERENCES workflow_runs ON DELETE CASCADE（同模块真子表；其余引用全弱） |
| seq | 执行序号，`UNIQUE (run_id, seq)`——执行顺序唯一事实源，run 内从 1 递增 |
| node_key / node_type | 节点标识与六类类型之一 |
| status | `succeeded` / `failed`（含失败节点自身的行） |
| input / output | jsonb 截断摘要（非 ctx 全量快照） |
| error_msg / duration_ms | 失败原因 / 节点耗时 |

## 索引（随 00019 落地）

- `idx_workflow_runs_wf_created (workflow_id, created_at DESC)` —— 按 workflow 列运行历史
- `idx_workflow_node_runs_run_id (run_id)` —— 按 run 取全部节点轨迹（seq 升序回放）

## 生命周期与写入规则

- **收尾统一写**：执行结束（成功/失败）一事务两批（run 1 行 + node_runs N 行多 VALUES）；失败路径也写。
- **写入降级**（O7 ④）：CreateRun 重试一次仍败 → 结果照返、RunID 置空、ERROR 日志带 trace_id。
- **保留期**：`WORKFLOW_RUNS_RETENTION_DAYS`（默认 365）后台批量 DELETE（node_runs 级联）；≤0 关闭 + WARN。
- **Go model**：`WorkflowRun` / `WorkflowNodeRun` embed `db.BaseAppendOnly`（service/model.go，无 json tag，序列化归 api/schema）。

## 保存期校验新增（R10，图校验条 11）

模板引用 `{{name}}` ∈ {input} ∪ 该节点祖先 node_key 集；适用字段 llm.prompt / api.url + headers 值 + body / end.output / condition.expression（比较式右侧字面量不查）；违例 → VALIDATION_FAILED 400，details 带节点 key 与引用名。运行期 strict render 兜底不变。

## 既有实体不变

workflows / workflow_nodes / workflow_edges 三表零改动（00001-00018 不动；tool 节点保存仍合法，执行期 fail-fast）。
