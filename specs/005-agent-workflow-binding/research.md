# Research: Agent → Workflow 绑定（agent-workflow-binding）

**Date**: 2026-09-17 | **Status**: 全部已决（五项拍板 2026-09-17 落定，见 [impl_spec_05 §3](../../docs/changelog/workflow/impl_spec_05_agent_binding.md)）

> 本特性无 NEEDS CLARIFICATION 项——关键技术决策已在 spec 定稿阶段经用户拍板完成，本文按 Decision/Rationale/Alternatives 格式存档，供 plan/tasks 与 review 引用。

## R1. FK 的 ON DELETE 行为（拍板 A）

- **Decision**: `RESTRICT`——被绑定的 workflow 禁删（409 `WORKFLOW_IN_USE`）。
- **Rationale**: 与仓内引用互锁同款（`agents.model_id` → MODEL_IN_USE、`conversations.agent_id` → AGENT_IN_USE）；agent 详情缓存（Cache-Aside 30min）不可能出现悬空 workflow_id；SET NULL 等于 workflow 的 DELETE 跨模块偷偷改写 agents 行，绕过 agent 模块边界与缓存失效；静默解绑比显式 409 难排障；下架场景本该走 disable 而非删除。
- **Alternatives**: SET NULL（否决，2026-09-17）——删 workflow 不被挡、自动解绑，代价是缓存窗口内悬空引用 + 违反「RESTRICT 默认」仓约定。

## R2. 绑定期存在性校验机制（依赖图约束下的必然）

- **Decision**: FK 23503 是 workflow 存在性的**唯一**校验；agent 模块不 import workflow。
- **Rationale**: 依赖清单允许 `workflow → agent`、禁止 `agent → workflow`（Go 编译器强制）；与 agent 绑 KB 完全同款（`agentapi.ErrKnowledgeBaseNotFound` 先例：同码哨兵各持一份，FK 兜存在性含并发窗口）。
- **Alternatives**: agent 调 workflowapi 预检（否决——破坏依赖图）；组合根反向注入接口（否决——为一次存在性检查引入反向依赖接缝）。

## R3. 23503 按约束名分发（本篇核心行为变更）

- **Decision**: `translateAgentFK(err) (error, bool)` 按 `pgErr.ConstraintName` 分发：`fk_agents_workflow`（00018 显式命名）→ `agentapi.ErrWorkflowNotFound`；其余 23503 保持 `providerapi.ErrModelNotFound`（`agents_model_id_fkey` / `agents_fallback_model_id_fkey` 为 00004 内联 REFERENCES 的 PG 自动命名）。
- **Rationale**: 加列后同一条 agents INSERT/UPDATE 可撞**三个** FK，只判错误码无法区分该报 404 WORKFLOW_NOT_FOUND 还是 404 MODEL_NOT_FOUND；显式命名新约束使分发零歧义。替换点仅 Create/Update 事务内 `CreateAgent`/`UpdateAgent` 两处；Tools/KBs 语句翻译不动（各自语句上的 23503 无歧义）。
- **Alternatives**: 全部 FK 显式改名并对齐（否决——动 00004 既有迁移违反只增不改）；写前 SELECT 预检（否决——R2 已定 FK 唯一校验，且预检有并发窗口）。

## R4. 绑定期发布态与执行版本（拍板 B / B2）

- **Decision**: 绑定期不校验发布态（draft/disabled 可绑定）；执行读实时版本——已发布 workflow 被 PUT 编辑后状态保持 published（决策 #6 编辑不降级）、立即对下一次执行生效，无发布快照。
- **Rationale**: 发布态是执行前置（`ErrWorkflowNotPublished` 503），由消费方在执行期 fail-fast；workflows 三表只存一份图（整图事务替换 + R1-R9 全量校验），库里永远是完整合法态。依赖清单也不允许 agent 查 workflow 状态。
- **Alternatives**: 发布快照（Dify 式 draft/published 双图）——否决但留门：将来需要时纯加 `published_graph jsonb` 列，不破坏本篇绑定契约。

## R5. API 形态与字段语义（拍板 C / C2）

- **Decision**: 既有 Create/PUT 字段化——`workflow_id` 可空字段（`binding:"omitempty,gt=0"`），PUT 全量语义（缺省/null = 解绑），零新路由；不与任何现有字段互斥（叠加语义），`validateAgent` 零新增规则。
- **Rationale**: 与仓内 PUT 全量约定一致；对话配置（model/提示词/RAG/工具）照常构成对话循环，workflow 是对话中可触发的附加能力（CLAUDE.md 依赖清单「chat → workflow」的既定语义）。
- **Alternatives**: 独立子资源端点 `PUT /agents/{id}/workflow`（否决——多一组路由 + 局部更新语义，不合仓约定）；互斥校验（否决——需 model_id 改可空 + 对话引擎分叉，改动远超本篇；将来确需纯工作流入口再加 `mode` 字段）。

## R6. workflow 侧删除互锁实现

- **Decision**: workflow service 新增 `pgCodeFKViolation = "23503"` 常量 + `isFKViolation` helper（agent/provider 同款），`Delete` 的 `err != nil` 分支最前翻译为 `workflowapi.ErrWorkflowInUse`；handler 映射 409。**不 import agent**。
- **Rationale**: workflows 行级 DELETE 上的 23503 唯一来源即 `fk_agents_workflow`（nodes/edges 是本模块自有 CASCADE 子表，删除路径先清后删）；按错误码翻译即可，零跨模块类型引用——本篇双向零新增 import。
- **Alternatives**: 删除前 SELECT count agents（否决——跨模块查表违反 store 边界 + 竞态）；CASCADE/SET NULL（R1 已否决）。

## 递延决策（E1/E2/E3，属执行引擎 spec）

chat 消费绑定的触发形态（工具 vs 管道）、试运行能力、运行历史 + 节点轨迹（`workflow_runs` + steps）——已记录于 impl spec 05 §1.1，绑定契约对三种形态均够用，届时逐项拍板。
