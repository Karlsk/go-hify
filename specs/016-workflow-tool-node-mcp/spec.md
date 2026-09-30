# Feature Specification: Workflow 工具节点接 MCP（统一 tool.Source 执行）

**Feature Branch**: `016-workflow-tool-node-mcp`

**Created**: 2026-09-30

**Status**: Draft

**Input**: 上位 spec [docs/changelog/workflow/impl_spec_16_tool_node_mcp.md](../../../docs/changelog/workflow/impl_spec_16_tool_node_mcp.md)（2026-09-30 用户批准定稿，D5 拍板 = A 仅字符串）

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 工具节点真实执行并喂给下游（Priority: P1）

图作者在 JSON 配置里写 tool 节点（tool_id + `{{var}}` 模板参数），执行 run 时工具被解析调用，结果落变量池；下游 llm 节点 prompt 用 `{{tool_key}}` 引用工具输出，终答包含工具取回的数据。

**Why this priority**: 这是本篇的核心价值——spec 06 落地执行引擎时 MCP 未建，tool 节点一直是占位 fail-fast（图缺陷 400「mcp 未建」）；打通后 workflow 才具备「调外部工具取数」这一区别于纯 LLM 编排的能力，也是 mcp/README §7 最后一项遗留。

**Independent Test**: 构造 `llm → tool → end` 三节点图（工具输出进终答 prompt 模板），执行 run 断言终答含工具输出、node_runs 里 tool 节点 succeeded 且 input/output 落轨迹。

**Acceptance Scenarios**:

1. **Given** 一个含 tool 节点的已发布 workflow（工具存在且 server 启用），**When** 通过 API 触发执行，**Then** 工具被调用、输出可被下游节点模板引用、run 状态 succeeded，节点轨迹记录入参（tool_id + 渲染后 args）与输出文本
2. **Given** tool 节点参数含 `{{start.x}}` 模板且执行时提供了 x，**When** 执行，**Then** 参数按运行时变量渲染后再调工具
3. **Given** 同一图内 tool 节点之后接 condition 节点，**When** 工具返回不同输出，**Then** 分支按工具输出正确路由

---

### User Story 2 - 工具失效可定位的失败路径（Priority: P2）

维护者在工具被删除或 MCP server 停用后执行旧图：不静默、不误报，错误信息带节点定位，一眼知道哪个节点的哪个工具失效。

**Why this priority**: fail-fast + 精确定位是 workflow 错误二分法的执行期兜底（O4 拍板）；没有它，工具下架后旧图的失败会变成不可排障的运行时黑盒。

**Independent Test**: 对同一图分别注入「Tools 清单空」「Invoke 返回错误」「args 缺失变量」三类故障，断言错误类型、状态码语义与节点定位逐类不同。

**Acceptance Scenarios**:

1. **Given** tool_id 指向的工具已被删除（或其 server 已停用），**When** 执行 run，**Then** 返回「工具不存在」（404 语义，`MCP_TOOL_NOT_FOUND`），错误消息带该节点 key 定位
2. **Given** 工具存在但调用执行失败（工具侧返回错误），**When** 执行 run，**Then** 返回「工具调用失败」（500 语义，`MCP_TOOL_CALL_FAILED`），run 状态 failed 且 error_node 定位该节点
3. **Given** tool 节点参数模板引用了运行时未提供的变量，**When** 执行 run，**Then** 返回图缺陷错误（400 语义，`VALIDATION_FAILED`），定位到具体参数名（`args.<参数名>`）——作者可改图修复

---

### User Story 3 - Agent 对话内嵌 workflow 的工具节点透传受益（Priority: P3）

绑定 workflow 的 Agent 在对话中触发执行时，管道内嵌的工具节点正常跑通——chat 侧零改动零回归。

**Why this priority**: chat 管道是 workflow 的既有下游消费者，本篇不改 chat 任何代码；该故事验证的是「透传受益 + 零回归」，不是新能力。

**Independent Test**: chat 模块全量测试回归绿（零改动前提下跑既有套件即可证明）。

**Acceptance Scenarios**:

1. **Given** Agent 绑定的 workflow 含 tool 节点，**When** 用户对话触发该 workflow 执行，**Then** 工具节点在管道内正常执行、结果进终答（chat 代码零改动）

### Edge Cases

- 组合根未注入工具来源（装配缺位，仅测试可达）：tool 节点按「工具调用失败」fail-fast（500 语义），消息写明工具来源未注入——不静默降级（图要求该工具，没有 chat 式「无工具继续」路径）
- 工具输出超长：节点轨迹沿用既有 16KB 单值截断标记（`truncated:true`），不新增机制
- 参数值含特殊字符 / 非法模板语法：按既有严格渲染语义处理，渲染失败即图缺陷错误，不静默传原文
- 一轮工具调用返回错误内容但 run 须继续：不存在此路径——workflow 是确定性图语义，工具失败即 run failed（区别于 chat 的「错误即上下文喂回 LLM 自纠」）

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: tool 节点执行 MUST 经统一工具契约解析工具（按 ToolID），且依赖只经 mcp 模块公开契约注入（组合根装配），禁止 workflow 依赖 mcp 内部实现
- **FR-002**: 工具解析失败（工具已删 / server 已停用被静默过滤）MUST 返回 `MCP_TOOL_NOT_FOUND`（404 语义）并透传下游哨兵，错误消息带节点 key 定位
- **FR-003**: 工具调用执行失败（工具侧错误返回）MUST 返回 `MCP_TOOL_CALL_FAILED`（500 语义，message 含工具返回的错误文本），run 置 failed、error_node 定位该节点——fail-fast 终止，不得把错误文本当成功输出继续走图
- **FR-004**: tool 节点参数 MUST 逐值严格渲染模板变量；缺失变量 MUST 返回 `VALIDATION_FAILED`（400 语义，图缺陷类）并定位到具体参数名
- **FR-005**: 节点轨迹 MUST 记录入参（tool_id + 渲染后参数）与输出（工具结果文本），复用既有截断与计时机制；输出 MUST 落变量池供下游节点引用
- **FR-006**: 装配缺位（工具来源未注入）时 tool 节点 MUST fail-fast 返回 `MCP_TOOL_CALL_FAILED` 环境限制类错误，不得静默跳过或降级
- **FR-007**: 工具调用为确定性单次执行（非 ReACT 多轮），调用轮次由图结构决定
- **FR-008**: 存量图 MUST 零迁移——ToolConfig 契约（tool_id + 字符串参数模板）不变；已保存含 tool 节点的图在工具就绪后直接可执行

### Key Entities *(include if feature involves data)*

- **ToolConfig（图节点配置，冻结契约）**: tool_id（MCP 工具 id）+ args（字符串参数，值支持模板）；参数值域仅字符串为已拍板限制（升级路径记录于上位 spec D5）
- **工具来源（执行期解析产物）**: 按绑定工具 id 供出工具定义与调用能力；server 停用即工具在该来源中消失
- **节点轨迹（既有表，零 schema 改动）**: tool 节点每步落 input/output/status/耗时，沿用既有单值截断上限

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 含 tool 节点的已发布 workflow，执行端到端跑通（工具输出进下游 prompt 与终答），全量自动化测试绿
- **SC-002**: 工具缺失 / 调用失败 / 参数渲染失败三类故障，错误类型与节点定位逐类可判——排障者从错误消息即可定位到节点与参数
- **SC-003**: workflow 模块测试覆盖率 ≥80%；chat 模块全量测试零改动前提下回归绿
- **SC-004**: 零迁移、零新增路由 / 错误码 / SSE 事件、零前端改动——本篇改动面收敛在 workflow 执行器与组合根装配
- **SC-005**: 依赖红线 grep 全空：workflow 不 import mcp 内部实现包；三方 MCP SDK 不越过 mcp 内部实现包；eino 不越出 platform/llm

## Assumptions

- 前置：chat 工具循环批（`0d6293f`）已合入——统一工具契约（platform/tool）与 mcp 按工具 id 供出工具来源的能力已落地并核实
- 迁移：零新增（21 applied 保持；下一号 00022 留给后续篇）
- 工具参数值域维持仅字符串（上位 spec D5 拍板 A）；需要数值/布尔强类型参数的工具属选型约束，不在本篇解决
- 图校验零改动（O4 拍板保真）：保存期不校验 tool_id 存在性，缺失由执行期 404 兜底；编辑器 tool 节点面板、执行进度事件、工具调用运行日志落库（executions）、MCP 会话复用均不在本篇范围
- 人工验收（`make start` 冒烟，真实 MCP server 绑工具的 workflow 手测）由用户执行，自动化门禁不覆盖真实 server 侧行为
