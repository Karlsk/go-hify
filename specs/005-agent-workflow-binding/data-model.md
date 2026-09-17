# Data Model: Agent → Workflow 绑定

**Date**: 2026-09-17 | **权威 DDL**: [impl_spec_05 §4.1](../../docs/changelog/workflow/impl_spec_05_agent_binding.md)（迁移 00018，goose Up/Down 成对，只增不改）

## 实体变更

### agents（可变表，既有）

| 列 | 类型 | 约束 | 说明 |
|---|---|---|---|
| `workflow_id`（新增） | `bigint` | 可空；`fk_agents_workflow` → `workflows(id)` ON DELETE **RESTRICT** | 绑定的工作流；NULL = 未绑定（常态） |

- 索引：`idx_agents_workflow_id ON agents (workflow_id)`（每个外键必须单独建索引）
- 注释：`COMMENT ON COLUMN agents.workflow_id`（语义 + RESTRICT 互锁说明，见 impl spec §4.1）
- Down：`ALTER TABLE agents DROP COLUMN workflow_id`（列上索引与 FK 随列级联删除）

### workflows（无结构变更）

仅获得删除互锁语义：被绑定时行级 DELETE 撞 `fk_agents_workflow` 23503 → 409 `WORKFLOW_IN_USE`。

## 关系

```text
agents N──1 workflows   # agents.workflow_id，ON DELETE RESTRICT（spec 05 拍板 A）
```

- 单向单值：绑定存储在 agent 侧；workflow 侧不感知、不维护绑定方列表（无反查端点、无绑定计数）
- N:1 允许多个 agent 绑同一 workflow（无唯一约束）
- 删除方向：删 agent → 绑定随行消失（无孤儿）；删 workflow → 被绑定时 409 拦截，解绑后可删（nodes/edges 随既有 CASCADE 清理）

## 校验规则（数据侧）

- 存在性：由 FK 保证（含绑定瞬间并发删除的窗口兜底）——无应用层预检
- 取值：`workflow_id ≤ 0` 由请求绑定校验拒绝（`omitempty,gt=0`），不触达 DB
- 发布态：绑定期不校验（draft/disabled 可绑定；拍板 B）

## 生命周期

- 绑定/解绑 = agents 行 UPDATE（PUT 全量语义），随既有事务与缓存失效矩阵（Create/Update/Delete 写时删 key）走，无新增缓存动作
- RESTRICT 保证库内不出现悬空 workflow_id → 缓存窗口内引用恒有效（拍板 A 第二收益）
