# Feature Specification: Agent → Workflow 绑定（agent-workflow-binding）

**Feature Branch**: `005-workflow-agent-binding`

**Created**: 2026-09-17

**Status**: Draft（五项设计决策已于 2026-09-17 拍板落定，见 impl spec 05 §3）

**Input**: User description: "workflow spec 05 需求：agent → workflow 绑定——agent 配置面绑定一个工作流（可空外键 RESTRICT），绑定期 FK 唯一校验 + 双向删除互锁"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 管理员为 Agent 绑定 / 解绑工作流 (Priority: P1)

管理员（平台使用者）在创建或编辑 Agent 时，通过请求体携带 `workflow_id` 字段完成绑定——Agent 的模型、提示词、RAG、工具等对话配置照常生效，工作流是对话中**可触发的附加能力**（叠加语义，不互斥）。解绑同样走编辑接口：全量提交体中该字段缺省或为 `null` 即解绑。多个 Agent 可绑定同一个工作流（N:1）。

**Why this priority**: 绑定是本特性的核心价值入口——没有写入面，后续执行引擎（触发工作流执行）无从谈起；它是 Dify 式 Agent 编排的最小形态的第一块拼图。

**Independent Test**: 可通过 `POST /agents`（带 workflow_id）与 `PUT /agents/{id}`（带 / 缺省 workflow_id）后 `GET /agents/{id}` 回显验证绑定状态，交付「Agent 配置面可管理工作流绑定」的完整价值。

**Acceptance Scenarios**:

1. **Given** 已存在 id=3 的 workflow，**When** 管理员 `POST /agents` 请求体携带 `workflow_id: 3`，**Then** 创建成功，详情响应回显 `workflow_id: "3"`。
2. **Given** Agent 已绑定 workflow 3，**When** 管理员 `PUT /agents/{id}` 全量体中 `workflow_id` 为 `null` 或缺省，**Then** 更新成功，详情回显 `workflow_id: null`（解绑）。
3. **Given** Agent A、B 均可绑定同一 workflow 3，**When** A 绑定后 B 也绑定，**Then** 两者均成功，互不影响。
4. **Given** Agent 绑定了 workflow 3 且配置了 model / 工具 / 知识库，**When** 保存，**Then** 全部字段照常生效（无互斥校验、无字段清空）。

---

### User Story 2 - 绑定不存在的 Workflow 得到明确报错 (Priority: P2)

管理员绑定时手误填了不存在（或刚被他人删除）的 workflow id，系统必须拒绝写入并返回 404 `WORKFLOW_NOT_FOUND`，而不是让非法引用静默落库。

**Why this priority**: 数据一致性防呆——悬空的 workflow_id 会让后续执行引擎在运行期才暴露问题，排障成本远高于写入期拦截。

**Independent Test**: `POST /agents` / `PUT /agents/{id}` 携带一个不存在的 workflow id，断言 404 信封与错误码；绑定期不校验发布态（draft / disabled 的 workflow 可正常绑定）同场景可一并验证。

**Acceptance Scenarios**:

1. **Given** id=999 的 workflow 不存在，**When** 管理员创建 Agent 时携带 `workflow_id: 999`，**Then** 返回 404，`error.code = "WORKFLOW_NOT_FOUND"`，Agent 未被创建。
2. **Given** workflow 3 处于 draft（未发布）状态，**When** 管理员绑定它，**Then** 绑定成功（发布态校验属执行期，由后续执行器负责）。
3. **Given** 请求体 `workflow_id: 0`，**When** 提交，**Then** 请求校验拒绝（400），不触达业务层。

---

### User Story 3 - 被绑定的 Workflow 删除被拦截 (Priority: P3)

管理员删除一个仍被 Agent 绑定的 workflow 时，系统返回 409 `WORKFLOW_IN_USE` 拦截；先解绑（或删除 Agent）后方可删除。反之，删除 Agent 时其绑定自然消失，不产生孤儿数据。

**Why this priority**: 双向互锁的另一半——保护「绑定」这一引用关系的完整性，也保护 Agent 详情缓存窗口内引用恒有效（被绑 workflow 不可能先于绑定消失）。

**Independent Test**: 绑定后 `DELETE /workflows/{id}` 断言 409；解绑后再删断言成功（其节点 / 边随既有级联清理）。

**Acceptance Scenarios**:

1. **Given** workflow 3 被 Agent 1 绑定，**When** `DELETE /workflows/3`，**Then** 返回 409，`error.code = "WORKFLOW_IN_USE"`，workflow 及其节点 / 边完好。
2. **Given** Agent 1 已解绑 workflow 3（或 Agent 1 被删除），**When** `DELETE /workflows/3`，**Then** 删除成功，节点 / 边级联清理。
3. **Given** workflow 3 被绑定，**When** 管理员 PUT 编辑它或发布 / 停用，**Then** 操作照常成功（编辑不降级、绑定不影响 workflow 既有生命周期——执行时读的是实时版本）。

---

### Edge Cases

- `workflow_id` 传 0 或负数 → 请求绑定校验拒绝（400），不进入业务层。
- 绑定瞬间目标 workflow 被并发删除 → 数据库外键兜底，仍返回 404（不依赖应用层预检的时序）。
- 同一 workflow 被 N 个 Agent 绑定 → 允许，无唯一约束；删除该 workflow 前须全部解绑。
- Agent 未绑定 → 详情响应 `workflow_id` 为 `null`，不返回 `0` 或空字符串。
- 既有存量 Agent（迁移前创建）→ 绑定状态一律为「未绑定」，行为与显式解绑后一致。
- 绑定的 workflow 被 PUT 编辑 → 绑定关系不动、编辑立即生效（无发布快照，状态保持已发布不降级）。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: 系统 MUST 允许在创建 Agent（`POST /agents`）与全量更新（`PUT /agents/{id}`）请求体中携带可选字段 `workflow_id` 完成绑定；PUT 全量语义下字段缺省或 `null` 即解绑。
- **FR-002**: 系统 MUST 拒绝非法 `workflow_id` 取值（≤0），返回 400 请求校验错误。
- **FR-003**: 绑定不存在的 workflow 时，系统 MUST 返回 404，错误码 `WORKFLOW_NOT_FOUND`；存在性由数据库外键保证（含并发窗口兜底），不做应用层预检。
- **FR-004**: 删除被任何 Agent 绑定的 workflow 时，系统 MUST 返回 409，错误码 `WORKFLOW_IN_USE`；全部解绑或删除绑定方 Agent 后删除方可成功。
- **FR-005**: Agent 的详情与列表响应 MUST 回显当前绑定状态：未绑定为 `null`，已绑定为该 workflow 的 id（字符串形式，与既有外键字段序列化约定一致）。
- **FR-006**: `workflow_id` MUST NOT 与 Agent 现有任何配置字段（model_id、fallback_model_id、tool_ids、knowledge_base_ids 等）互斥——叠加语义，对话配置照常生效。
- **FR-007**: 绑定期 MUST NOT 校验 workflow 发布态：draft / disabled 状态的 workflow 均可被绑定；发布态校验（`WORKFLOW_NOT_PUBLISHED`）属执行期行为，由后续执行器负责。
- **FR-008**: 删除 Agent 时其绑定 MUST 随之自然消失（绑定存储在 Agent 侧，无孤儿数据、无反向清理动作）。
- **FR-009**: 存量数据 MUST 平滑兼容：迁移上线后既有 Agent 一律为未绑定状态，既有 workflow 的增删改查行为不变。

### Key Entities

- **Agent**：新增可选属性「绑定的工作流」（0 或 1 个）；删除 Agent 时绑定随之消失。
- **Workflow**：可被 0..N 个 Agent 绑定；被绑定期内删除被拦截（引用保护），编辑 / 发布 / 停用不受绑定影响。
- **绑定关系**：单向单值（Agent → Workflow），存储在 Agent 侧；workflow 侧不感知、不维护绑定方列表。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 绑定 / 解绑 / 回显链路完整：`POST`/`PUT` 携带与缺省 `workflow_id` 后，`GET` Agent 详情准确反映当前绑定状态（null 或 id 字符串）。
- **SC-002**: 既有功能零回归：Agent 与 Workflow 两模块既有测试全绿，模块测试覆盖率各 ≥80%。
- **SC-003**: 错误路径行为确定且机器可判：`WORKFLOW_NOT_FOUND` → 404、`WORKFLOW_IN_USE` → 409、非法取值 → 400，错误码即哨兵文案，前端可直接分支。
- **SC-004**: 以实现 spec 验收门为准：`docs/changelog/workflow/impl_spec_05_agent_binding.md` §8/§9 全部满足（全量构建 / vet / 测试门禁全绿、迁移 18 条 applied、依赖方向 grep 无违规——agent 模块不 import workflow 模块）。

## Assumptions

- 前置 spec 01-04（workflow CRUD / 状态机 / 整图读写 / service+handler）已合入（commit `25231d6`），下一迁移号为 00018。
- 本特性无下游消费者：chat 会话期消费绑定、工作流执行引擎、试运行、运行历史与节点轨迹均为后续独立 spec（E1/E2/E3 递延决策已记录于 impl spec 05 §1.1）。
- 五项设计决策已拍板（2026-09-17）：A=外键 RESTRICT（删除互锁）/ B=绑定期不校验发布态 / B2=执行读实时版本（编辑立即生效，无发布快照）/ C=既有 Create/PUT 字段化（零新路由）/ C2=不互斥（叠加语义）。
- 数据完整性由数据库外键约束兜底，应用层不做 workflow 存在性预检（架构约束：agent 模块不得依赖 workflow 模块，同「agent 绑 KB」先例）。
- 冻结契约（迁移 SQL、字段与哨兵文案、错误翻译行为）以 `docs/changelog/workflow/impl_spec_05_agent_binding.md` §4 为唯一权威，本文件只描述 WHAT/WHY。
- 无组合根（装配）改动、无前端改动、无新增第三方依赖。
