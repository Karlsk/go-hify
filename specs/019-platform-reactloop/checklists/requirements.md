# Specification Quality Checklist: platform 公共 ReAct 工具循环抽取 + chat 重构换用

**Purpose**: Validate specification completeness and quality before proceeding to planning
**Created**: 2026-10-10
**Feature**: [spec.md](../spec.md)（Draft，待 clarify 定稿）

## Content Quality

- [x] Focused on user value and business needs（US1 零差异换用 / US2 单点维护 / US3 双模式与取消语义）
- [x] All mandatory sections completed（User Scenarios & Testing / Requirements / Key Entities / Success Criteria / Assumptions）
- [x] ~No implementation details~ —— **按仓库惯例豁免并记录**：本仓 spec 定稿即冻结技术契约（回调缝形态、冻结常量族、依赖红线），016/017/018 同款形态；spec-dev 流程「以对齐既有稿为准」优先于通用模板的此项

## Requirement Completeness

- [x] No [NEEDS CLARIFICATION] markers remain（D1=B / 双模式 / 回调缝语义均经用户 2026-10-10 拍板或本会话设计确认）
- [x] Requirements are testable and unambiguous（FR-001~FR-009 逐条可测，MUST 判定词齐备）
- [x] Success criteria are measurable（SC-001~SC-005 对应回归 / 单测 / grep / 门禁四类机器判 + 手测一类人工判）
- [x] All acceptance scenarios are defined（US1 五条 / US2 四条 / US3 四条 Given-When-Then）
- [x] Edge cases are identified（八条：nil 清单 / 回调错误 / opts 不可变 / usage 含收尾轮 / 串行 / 未知名 / MaxRounds 域 / Final 聚合失败）
- [x] Scope is clearly bounded（明确不做六项 + 拆分记录：react 节点 / ragNode / tool 表单归 020）
- [x] Dependencies and assumptions identified（前置 0d6293f、冻结决策承继、降级链留 chat、零迁移零新路由）

## Feature Readiness

- [x] All functional requirements have clear acceptance criteria
- [x] User scenarios cover primary flows（换用零差异 / 单点维护 / 双模式取消）
- [x] Feature meets measurable outcomes defined in Success Criteria
- [x] Constitution 冲突预检：Principle I（重构动机 = 消灭双份维护，复杂度不增反降）通过；Principle II（platform 不依赖业务域、chat 消费 platform 方向合法）通过；Principle III（循环不涉超时/重试/熔断逻辑——LLM 调用仍经 platform/llm client，超时重试熔断留在 llm 层不变）通过；Principle VII（零新错误码 / 零契约变化）通过

## Notes

- clarify 重点预判：回调缝的精确形态（OnToolRoundStart 与 assistant 中间行落库序的耦合、回调错误的 abort 语义映射）、chat 降级链与循环的边界（loadToolRegistry 留 chat）、非流式模式在 020 前的「无生产消费者」正当性。
- 下一步：`/speckit-clarify`。
