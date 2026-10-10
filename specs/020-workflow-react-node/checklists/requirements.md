# Specification Quality Checklist: workflow react 节点 + ragNode 画布接入 + tool 节点表单

**Purpose**: Validate specification completeness and quality before proceeding to planning
**Created**: 2026-10-10
**Feature**: [spec.md](../spec.md)（Draft，待 clarify 定稿）

## Content Quality

- [x] Focused on user value and business needs（四个 User Story 均为画布可感知能力）
- [x] All mandatory sections completed（User Scenarios & Testing / Requirements / Key Entities / Success Criteria / Assumptions）
- [x] ~No implementation details~ —— **按仓库惯例豁免并记录**：本仓 spec 定稿即冻结技术契约（jsonb 加法键、迁移号、R10 清单、palette 形态），016/017/018/019 同款；spec-dev「以对齐既有稿为准」优先
- [x] Written for the audience of this repo（一人维护技术型 spec）

## Requirement Completeness

- [x] No [NEEDS CLARIFICATION] markers remain（react 形态 / 轮数可配 / builtin 剔除 / ragNode B 案 / tool 表单均经用户 2026-10-10 拍板）
- [x] Requirements are testable and unambiguous（FR-001~FR-010 逐条可测，MUST 语义齐备；值域 1-16 / 0-20 / 并集去重等边界明确）
- [x] Success criteria are measurable（SC-001~SC-005 对应手测三场景 + 门禁 + 零回归复跑）
- [x] All acceptance scenarios are defined（US1 六条 / US2 四条 / US3 三条 / US4 三条 Given-When-Then）
- [x] Edge cases are identified（八条：server 停用 fail-fast / 并集去重 / max_rounds 越界 / 嵌套 / query 空串 / KB 停用 / 下拉加载失败 / id 数组形态）
- [x] Scope is clearly bounded（明确不做六项）
- [x] Dependencies and assumptions identified（前置 019、迁移 00022、server 级绑定动态语义、值域护栏归属）

## Feature Readiness

- [x] All functional requirements have clear acceptance criteria
- [x] User scenarios cover primary flows（react 自主调用 / rag 先提炼后检索 / tool 画布闭环 / 零回归）
- [x] Feature meets measurable outcomes defined in Success Criteria
- [x] Constitution 冲突预检：Principle I（复用 019 循环零自写、palette 复用既有组件模式）通过；Principle II（workflow → mcp/rag/provider 均依赖清单既有方向，只 import api 包）通过；Principle III（react 的 LLM 调用经 platform/llm client、循环经 reactloop，超时重试熔断不新写）通过；Principle V（迁移 00022 只增不改、CHECK 重建同 00017/00020 先例、无新表）通过；Principle VII（零新错误码——复用 MCP_TOOL_NOT_FOUND / VALIDATION_FAILED / KNOWLEDGE_BASE_NOT_FOUND 既有哨兵）通过

## Notes

- clarify 预判核对面：react 节点 executions 记录形态（OnRound 闭包 vs llm 节点 recordExecution 复用）、并集去重在 toolsource 的实现位（ensure 内 union）、max_rounds 上界校验归属（workflow Validate，reactloop 只做 ≤0 回退）。
- 下一步：`/speckit-clarify`。
