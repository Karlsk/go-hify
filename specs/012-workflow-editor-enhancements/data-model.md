# Data Model: 工作流拖拽编辑器八项增强

**Feature**: [spec.md](./spec.md) | **Date**: 2026-09-22

本篇纯前端、后端零改动——**无数据库实体、无迁移**。本文定义前端层的数据形态：伪开始节点（canvas-only 实体）、变量引用条目、模板字段接入矩阵、Auth 预设映射。既有实体（GraphConfig 聚合形态、WorkflowDetail）不改动，仅列消费关系。

---

## 1. 伪开始节点（canvas-only 实体）

| 属性 | 值 / 类型 | 说明 |
|---|---|---|
| id | 常量 `__start__` | 仅作渲染标识与点击判定，永不进 nodes 数组 / 序列化 |
| label | 「开始」 | 画布左端固定位置渲染（不可拖动） |
| 指向 | `startKey: string \| null` | 装饰箭头指向当前起始节点，随起始切换/删除迁移（migrateStartKey）随动 |
| 点击行为 | 选中伪节点上下文 | 检查器切 schema 面板模式（task 型）或说明模式（chat 型） |

**不变量**（由结构保证，见 research 决策 1）：
- `getGraph()` 输出（start_node_key / nodes / edges）**恒不含**伪节点任何痕迹。
- 伪节点不参与：onNodesChange（remove/drag）、ancestorsOf、变量源、key 冲突校验。
- 空画布仍常驻渲染（Edge Case）。

### schema 面板数据流（同源绑定）

```text
宿主页（单源）                      伪节点面板
WorkflowEdit:   inputSchema/outputSchema refs（抽屉同源）
WorkflowOrchestrate: store 字段（两步式第一步同源）
        │ props（GraphModeEditor → CanvasEditor 透传）
        ▼
  伪节点 schema 面板（复用 SchemaFieldsEditor 渲染）
        │ emit('update:inputSchema' | 'update:outputSchema', rows)
        ▼
  宿主页更新源 → 抽屉/第一步表单同步（同一 ref 的自然结果）
```

- task 型：入参 SchemaFieldsEditor + 出参 SchemaFieldsEditor 两段；chat 型：只读说明「chat 型工作流仅暴露单一入参 `{{input}}`」，无编辑。

## 2. 变量引用条目（TemplateField 下拉单元）

```ts
/** 一条可插入的变量引用——TemplateField 只渲染，不计算（research 决策 4/5） */
interface VariableOption {
  /** 插入文本，含完整 {{}} 包裹，如 "{{input}}"、"{{classify}}"、"{{fetch.result}}" */
  insert: string
  /** 展示名（下拉行主文案），如 "input.q"、classify、fetch.result */
  label: string
  /** 来源分组：入参 / 上游节点 / 子流程出参 */
  group: 'input' | 'upstream' | 'subflow-output'
}

/** 分组下拉数据源 */
type VariableGroups = Array<{ group: string; options: VariableOption[] }>
```

| 分组 | 内容 | 条件 |
|---|---|---|
| input | `{{input}}`；task 型再展开 `{{input.<字段名>}}` 逐字段一行 | `{{input}}` 恒在；展开仅 task 型且有 input_schema |
| upstream | 祖先节点（ancestorsOf，排除自身）每 key 一行 `{{key}}` | 图中有祖先 |
| subflow-output | 祖先中 workflow 节点 × 其子流程 output_schema 字段 → `{{key.field}}` | 祖先含 workflow 节点（详情经会话缓存拉取） |

**不校验**：下拉只是输入辅助，选中/手写均不触发引用合法性校验（R10 保存期后端兜底）。

## 3. 模板字段接入矩阵（FR-008：TemplateField 统一替换）

| 字段 | 节点类型 | config 键 | 变量源消费 | 变化 |
|---|---|---|---|---|
| System Prompt | llm | `system_prompt`（可选，空=不携带） | 三分组 | **新增字段**（spec 011 前端消费） |
| Prompt | llm | `prompt` | 三分组 | 既有 textarea → TemplateField |
| Output | end | `output` | 三分组 | 既有 → TemplateField |
| Expression | condition | `expression` | 三分组 | 既有 → TemplateField |
| URL | api | `url` | 三分组 | 既有 → TemplateField |
| Body | api | `body` | 三分组 | **新增字段**（POST 场景） |
| Header 值 | api | `headers.<k>` | 三分组 | **新增**（Headers KV 行的值域） |
| 子流程入参值 | workflow | `inputs.<字段名>` | 三分组 | **新增**（schema 驱动行） |

非模板字段保持既有控件：model_id（下拉）、workflow_id（下拉）、method（下拉）、Key（改名输入框）。

## 4. Auth 预设（API 节点，无独立存储形态）

推导与写回规则（research 决策 6；`config.headers: Record<string, string>` 是唯一事实源）：

| 预设 | 推导规则（读） | 序列化（写） |
|---|---|---|
| 无 | headers 无 `Authorization` 键 | 移除 Authorization 行 |
| Bearer Token | 值以 `Bearer ` 前缀 | `headers.Authorization = "Bearer " + token`（token 明文，既有契约形态） |
| Basic | 值以 `Basic ` 前缀 | `headers.Authorization = "Basic " + base64(utf8(user:pass))` |

- 切换预设：先删旧 Authorization 行再注入新行（无残留）；Basic 编码用 UTF-8 安全形态（中文账密可用）。
- 用户在 Headers KV 区手输 `Authorization` 行视同直接覆盖，Auth 区推导随动（单源语义）。

## 5. key 改名（结构性级联，graph.ts 纯函数）

`renameNodeKey(state, oldKey, newKey)` 一次返回新状态（不可变更新）：

| 级联点 | 行为 |
|---|---|
| nodes | key=id 条目替换（config 等其余字段原样） |
| edges | source/target 等于 oldKey 的端点替换为 newKey |
| startKey | 等于 oldKey → newKey |
| positions | 键 oldKey → newKey（值坐标保留） |
| selected | 指向 newKey |

**不改写**：其他节点模板文本中的 `{{old_key}}`（spec Assumptions；R10 兜底）。

**校验**（NodeInspector 编辑点拦截，与 JSON 模式既有校验对齐）：非空 / ≤64 / 不与画布现存 key 冲突；同名幂等（newKey == oldKey 无变化）。

## 6. 既有数据形态（只读消费，零改动）

| 形态 | 消费点 |
|---|---|
| `GraphConfig`（graph.ts 聚合） | 画布 parse/serialize 主形态不变；新增函数只是消费者 |
| `WorkflowDetail.input_schema / output_schema`（api/workflow.ts，spec 008 交付） | 子流程入参渲染（决策 8）、变量源 subflow-output 展开（决策 4）——详情经会话级 `Map<id, SchemaField[]>` 缓存 |
| `SchemaField` 行（SchemaFieldsEditor 既有） | 伪节点面板复用渲染 |
| `config` 键集（api_contract.md §3 冻结） | 本篇读写全部落在既有键：system_prompt / prompt / output / expression / url / method / headers / body / inputs / workflow_id / model_id；**零新增键** |
