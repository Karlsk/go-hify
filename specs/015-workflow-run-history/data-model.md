# Data Model: workflow 运行历史与节点轨迹查询

**Date**: 2026-09-24 | **Spec**: [spec.md](./spec.md) | **Plan**: [plan.md](./plan.md)

**零新表、零迁移、零 model 改动**——两实体为 spec 07/08 已交付的落库实体（model 在 [internal/workflow/service/model.go](../../../internal/workflow/service/model.go)，表 DDL 在迁移 00019/00020，当前 20 条 applied、下一号 00021）。本篇只开**只读查询面**：定义 model → api schema 的映射与摘要/全字段两档暴露。

## 实体 1: WorkflowRun（运行记录）

一次工作流调用的完整存档，append-only（`db.BaseAppendOnly`：id + created_at，无 updated_at 无软删）。表 `workflow_runs`。

| 字段（model） | 列 | 类型 | 查询面归属 | 说明 |
|---|---|---|---|---|
| ID | id | bigint identity | 摘要 + 详情 | JSON 字符串化 `"id,string"`（前端叫「运行编号」） |
| WorkflowID | workflow_id | bigint | （路由参数即来源，不重复出） | 弱引用 workflows，无 FK（spec 07 决策）；查询恒带 `WHERE workflow_id = ?` |
| WorkflowName | workflow_name | text | 仅内部 | 落库快照；本篇查询按路由 id 定位，schema 不透出 |
| TriggerSource | trigger_source | text + CHECK | 摘要 + 详情 | console / chat / workflow 三值 |
| IsTrial | is_trial | boolean | 摘要 + 详情 | 试运行标识 |
| ConversationID | conversation_id | bigint NULL | 详情 | chat 触发时的关联会话；JSON `*string`（null 或字符串 id） |
| MessageID | message_id | bigint NULL | 详情 | chat 触发时的关联消息；同上 |
| TraceID | trace_id | text | 详情 | 链路追踪标识（透传不解释） |
| Status | status | text + CHECK | 摘要 + 详情 | **仅两终态**：succeeded / failed（无 RUNNING——spec 07 决策，本篇不开进行中查询） |
| Input | input | text | **仅详情** | 落库边界 16KB 截断保真（带截断标记）；FR-002 列表禁带 |
| Output | output | text | **仅详情** | 同上 |
| ErrorNode | error_node | text | 摘要 + 详情 | 失败节点 key（空 = 成功运行）；前端据此高亮轨迹行 |
| ErrorMsg | error_msg | text | 摘要 + 详情 | 运行级错误摘要（短文本，非大文本档） |
| DurationMs | duration_ms | int | 摘要 + 详情 | 总耗时（毫秒） |
| StartedAt | started_at | timestamptz | 摘要 + 详情 | 执行起点；列表「调用时间」列展示此值 |
| CreatedAt | created_at | timestamptz | 摘要 + 详情 | 收尾落库时刻 = 列表排序键（D2）；详情两时间均出 |
| ParentRunID | parent_run_id | bigint NULL | 详情 | 嵌套子运行的父 run 编号（插入恒 NULL，UpdateParentRunIDs 回填）；只透出值，不反查子运行列表 |

**身份与唯一**: id 代理主键；无业务唯一键（同一工作流可无限次调用，各一行）。

**生命周期**: 无状态迁移——插入即终态（succeeded/failed 二值随落库写入），本篇零写路径。

## 实体 2: WorkflowNodeRun（节点轨迹记录）

一次运行内按执行序排列的节点步骤。表 `workflow_node_runs`。

| 字段（model） | 列 | 类型 | 查询面归属 | 说明 |
|---|---|---|---|---|
| RunID | run_id | bigint FK → workflow_runs.id ON DELETE CASCADE | （查询键） | 两表间唯一 FK |
| Seq | seq | int | 轨迹 | 执行序号；uq(run_id, seq) 兜底；轨迹表按其 ASC 排序 |
| NodeKey | node_key | text | 轨迹 | 节点 key（失败节点高亮的匹配键 = run.error_node） |
| NodeType | node_type | text | 轨迹 | llm / api / workflow / condition / end |
| Status | status | text + CHECK | 轨迹 | succeeded / failed |
| Input | input | text | 轨迹 | 节点输入摘要（截断保真文本） |
| Output | output | text | 轨迹 | 节点输出摘要（截断保真文本） |
| ErrorMsg | error_msg | text | 轨迹（透出空值） | **落库恒空**（nodeStep 无该字段，既有形态）——错误定位走 run 级 error_msg + error_node 高亮 |
| DurationMs | duration_ms | int | 轨迹 | 节点耗时（毫秒） |
| ID / CreatedAt | id / created_at | 标准表头 | 不出 | 仅内部 |

**关系**: WorkflowRun 1—N WorkflowNodeRun（CASCADE 删除）；轨迹查询 = `WHERE run_id = ? ORDER BY seq ASC`。空轨迹为合法态（落库失败降级路径）→ 空 slice → JSON `[]`。

## 索引与查询形态（全部既有，本篇零新增）

| 索引 | 覆盖查询 |
|---|---|
| idx_workflow_runs_wf_created (workflow_id, created_at DESC) | 列表 keyset：`WHERE workflow_id = ? AND (created_at, id) < (?, ?) ORDER BY created_at DESC, id DESC LIMIT n+1`（行值比较，D2） |
| workflow_node_runs (run_id) + uq (run_id, seq) | 轨迹按执行序 |
| parent_run_id | **无索引**（spec 08 决策：低频排障路径）——与「不反查子运行」边界互为因果 |

## 验证规则（查询面）

- 列表 limit 归一（≤0→20、>100→100）与游标合法性校验在 service 层（D7）；无模型级校验（零写路径）。
- 详情 404 判定：`WHERE workflow_id = ? AND id = ?` 无行 → RUN_NOT_FOUND（D3，不存在与跨工作流不可区分）。

## 状态迁移图

不适用——两实体均为 append-only 终态数据，本篇只读。
