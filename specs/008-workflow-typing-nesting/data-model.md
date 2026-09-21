# Data Model: workflow 分型与子工作流嵌套

> 契约依据：impl_spec_08_typing_nesting.md §2（范围）、§4（行为语义）、§5（O1-O8）；
> 上位 DDL 见 docs/changelog/workflow/db_model.md（本篇为其增量，迁移 00020）。

## 实体变更（三表加列，零新表）

### Workflow（表 workflows，既有）

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| `type` | `text` | `NOT NULL CHECK (type IN ('chat','task'))`，存量回填 `'chat'` | 不可变（Update 携带即拒 400）；消费者 = 嵌套规则（chat 型不可被嵌）+ 管理面 |
| `input_schema` | `jsonb` | 可空 | 简化形态 `[{name, type, required, description}]`，type ∈ string/number/boolean；仅 task 型消费，chat 型强制 NULL |
| `output_schema` | `jsonb` | 可空 | 同上；executeChild 收尾校验子终稿 |

校验规则（service 层，Create/Update 均跑）：

- schema 形态：name 非空且不重名、type ∈ {string, number, boolean}、required 为布尔。
- chat 型携带非空 schema → 400（强不变量，clarify 拍板）。
- R11（见下）对图中每个 sub-workflow 节点跑五项检查。

### WorkflowNode（表 workflow_nodes，既有）

- `type` CHECK 加值 `'workflow'`（第七种节点类型，约束 `workflow_nodes_type_check` 重建——00017 同名模式）。
- config（jsonb，密封 `{workflow_id, inputs}`）：
  - `workflow_id`：字符串化外键（弱引用，无 FK 约束——jsonb config 引用无法 FK，删除语义见下）。
  - `inputs`：字段→模板映射；键集须逐一覆盖被引图 input_schema 全部 required 字段、多余拒；被引图无 schema 时映射恰为 `{input}`；每个值为 `{{var}}` 模板（R10 天然覆盖，保存期只校验基名）。

### WorkflowRun（表 workflow_runs，既有，00019 建的轨迹表）

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| `trigger_source` | `text` | CHECK 重建加 `'workflow'`（原 `('console','chat')`，约束 `workflow_runs_trigger_source_check`） | 直接触发方式；与 conversation_id/message_id（因果归属）正交（O7b） |
| `parent_run_id` | `bigint` | 可空，弱引用 workflow_runs.id（无 FK） | 父收尾统一回填（一次窄 `UPDATE ... WHERE id = ANY($1)`）；append-only 例外登记 db_model.md 决策 #16 |

子 run 行其余列语义：conversation_id / message_id 与父相同（透传）；is_trial = 父的 trial（跟随）；status / error_node / duration_ms 等照既有语义。

## 关系

```text
workflows.type ──决定──▶ 可否被嵌（仅 task 型可被 sub-workflow 节点引用）
workflow_nodes(config->workflow_id) ──弱引用──▶ workflows.id（无 FK；删除不扫描引用）
workflow_runs.parent_run_id ──弱引用──▶ workflow_runs.id（自引用树，父收尾回填）
```

## 状态机

- 两型同款（draft / published / disabled 不分叉），发布/停用动作不分型。
- 类型生命周期：Create 必填 → 永不可变（换型 = 删了重建，嵌套校验自然重跑）。

## R11 保存期嵌套校验（五项，进 db_model §7 清单条 12）

1. workflow_id 存在（store 直查，同模块）。
2. 被引 workflow 必须 task 型。
3. 环检测：DFS 沿被引图 sub-workflow 引用链展开，链上出现被保存图自身 id 即拒（自嵌 = 长度 1 特例）。
4. 引用链深度上限 3（顶层 + 2 层，与执行期兜底同值）。
5. inputs 键集覆盖（全部 required 覆盖、多余拒；无 schema 子图回退恰 `{input}`）。

并发互引竞态窗口接受（单管理员内部规模）；执行期深度兜底同值拦截。

## 删除语义

被嵌 task workflow 删除**不扫描引用**（jsonb config 引用无法 FK）：保存期预检挡新引用 + 执行期 fail-fast（ErrWorkflowNotFound 带父 node 前缀）；引用已删 workflow 的存量图在下次编辑保存时被存在性预检拦截。
