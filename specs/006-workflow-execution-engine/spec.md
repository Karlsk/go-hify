# Feature Specification: Workflow 执行引擎（workflow-execution-engine）

**Feature Branch**: `006-workflow-execution-engine`

**Created**: 2026-09-18

**Status**: Draft（O1-O7 设计决策已全部拍板并冻结，见 impl spec 06 §5）

**Input**: User description: "workflow spec 06 需求：执行引擎——把已发布的工作流图变成可运行的执行时行为"

## Clarifications

### Session 2026-09-18

- Q: LLM 节点的 executions 落库责任层（spec 06 §4.2 注记「platform/llm 自动完成」与代码现状冲突——executions 实由 chat service 层写入）→ A: **执行器自记**——callLLM 内记 executions 行（ConversationID 置空 = workflow 节点调用，logging model 既有语义），executions 写入缝以窄接口注入 workflow service；复用调用方服务层写入先例，零 platform 层改动、零 chat 触碰。impl spec 06 §4.2 原注记系事实性错误，经用户批准更正。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 控制台执行已发布的工作流并拿到节点级轨迹 (Priority: P1)

控制台用户在 workflow 详情页点「执行」、输入一段测试文本，同步拿到完整结果：终稿、状态、总耗时、各节点轨迹摘要（node_trace：key / type / status / 耗时）。失败时错误信息带失败节点 key 前缀，一眼定位错在哪一步；下游错误（模型不存在、供应商限流等）原样透传既有错误码。

**Why this priority**: 执行是 workflow 模块从「纯配置管理」变成「运行业务」的核心价值入口——三张表存的静态图数据由此第一次产生运行时行为；没有它，前五篇的 CRUD / 校验 / 状态机 / 绑定全是空转。

**Independent Test**: 对一个含 llm → condition → end 的 published 工作流 `POST /workflows/{id}/execute`（stub 下游），断言 200 信封内 run_id / status=succeeded / output=终稿 / node_trace 覆盖全部节点——交付「图可运行、结果可追溯」的完整价值。

**Acceptance Scenarios**:

1. **Given** workflow 42 处于 published 且图为 classify(llm) → router(condition) → reply(llm) → end，**When** 用户 `POST /workflows/42/execute` 携带 `input: "查一下我的订单"`，**Then** 200 返回，`status = "succeeded"`、`output` 为终稿、`node_trace` 按执行顺序列出四个节点及各自耗时，`run_id` 非空。
2. **Given** 同图但 reply 节点引用的模型已被删除，**When** 执行，**Then** 错误信封错误码为下游既有哨兵（如 `MODEL_NOT_FOUND`），message 带 `node reply:` 前缀定位失败节点。
3. **Given** workflow 42 处于 draft 或 disabled，**When** 正式执行（不带 trial），**Then** 503 `WORKFLOW_NOT_PUBLISHED`，文案区分 draft（未发布）与 disabled（已停用）两态。
4. **Given** 请求 `input` 缺失或超过 16384 字符，**When** 提交，**Then** 400 请求校验拒绝，不触达执行器。

---

### User Story 2 - 作者试运行半成品（不发布先测） (Priority: P2)

工作流作者调试 draft / disabled 态的半成品：用 `?trial=true` 试运行直接测，不必先 publish——避免「测试动作污染生产语义」（窗口期内绑定的 Agent 会在 chat 真触发半成品）。试运行照常产生运行轨迹且带 `is_trial` 标记，与真实流量在运行历史里可区分；LLM 成本真实发生（照常记调用明细）。

**Why this priority**: 开发循环效率——三家平台（Dify / n8n / Coze）的开发循环均为「测半成品不发布」；没有试运行，作者每改一版都要走 publish → 测 → 回滚的伪循环。

**Independent Test**: 对 draft 态工作流 `POST /workflows/{id}/execute?trial=true`（stub 下游）断言 200 且结果与正式执行同构；对同一工作流不带 trial 断言 503——交付「半成品可测、生产语义不被污染」的完整价值。

**Acceptance Scenarios**:

1. **Given** workflow 7 处于 draft，**When** `POST /workflows/7/execute?trial=true`，**Then** 执行照常进行，200 返回与正式执行同构的结果。
2. **Given** 同上试运行完成，**When** 查运行轨迹，**Then** 该次 run 行 `is_trial = true`，与正式流量可区分。
3. **Given** workflow 7 处于 disabled，**When** 试运行，**Then** 同样放行（试运行对 draft / disabled 一视同仁，是状态机唯一例外）。
4. **Given** workflow 7 处于 published，**When** 带 `?trial=true` 执行，**Then** 正常执行（published 本就可执行，trial 标记照记）。

---

### User Story 3 - 排障：按 run 回放节点级轨迹 (Priority: P3)

某次执行结果异常，排障者按 workflow 查运行历史列表、按 run 查节点级轨迹：每个节点一行（执行序号 seq、node key / type、入出参摘要、错误、耗时），失败 run 带失败节点 key 与错误信息；trace_id 串起结构化日志与 LLM 调用明细。轨迹在执行结束（成功或失败）时一次性落库，失败路径也落——「运行日志是排障唯一线索」。

**Why this priority**: 可观测护栏落地——执行引擎一旦上线，没有轨迹的失败等于黑盒；但相对 P1/P2 它是伴随性能力（同一执行链路的收尾动作），优先级次之。

**Independent Test**: 构造一次中途失败的执行（stub 下游返回错误），断言 run 行 status=failed、error_node 为失败节点、node_runs 覆盖含失败节点在内的全部已执行节点且 seq 连续——交付「失败可回放」的完整价值。

**Acceptance Scenarios**:

1. **Given** 一次成功执行完成，**When** 查轨迹，**Then** workflow_runs 恰一行（input / output / status / 耗时 / 触发来源），workflow_node_runs 每执行节点一行、seq 递增无重复。
2. **Given** 执行在第三个节点失败，**When** 查轨迹，**Then** run 行 `status = "failed"`、`error_node` 为该节点 key，node_runs 含前两个成功节点与失败节点（其行内带错误信息与耗时）。
3. **Given** 单个节点输出超过 16KB，**When** 落库，**Then** 该值被截断至 16KB 且标记 `truncated: true`（回放时分清「本来就这么短」）。
4. **Given** 轨迹写入持续失败，**When** 执行收尾，**Then** 执行结果照常返回给调用方、结果中 run_id 为空串、错误日志带 trace_id——排障链不断（结构化日志与 LLM 调用明细仍在）。

---

### Edge Cases

- 模板引用了不存在的变量（错字 / 引用了非祖先节点）→ 保存期被静态校验拦截（400）；绕过保存期的运行期兜底为 strict 执行错误（400，message 带 `node <key>:` 前缀）。
- condition 求值结果不匹配任何出边 → 执行错误 fail-fast（400 图缺陷类），不静默终止。
- 游走到 tool 节点 → 执行错误 fail-fast（400 图缺陷类，mcp 模块未建；存量图零迁移，mcp 建成后接上即通）。
- api 节点目标为回环 / 链路本地 / IPv6 ULA 地址 → 拦截（500 环境限制类）；目标为 RFC1918 内网 → 默认放行（内网自签服务是主场景）；环境变量一键收紧后私网全禁。
- api 节点遭遇重定向 → 每一跳重新做地址校验（防 DNS rebinding 绕过）；非 http / https 协议 → 拒绝。
- 工作流总时长超过 5 分钟 → 引擎主动终止（500 环境限制类），留下干净的失败轨迹而非等到网关读超时。
- 调用方中途取消（客户端断连）→ 执行立即中止，错误带节点 key；已发生成本的调用明细不丢。
- 执行开始后图被并发编辑 → 进行中执行不受影响（启动时快照），编辑对「下一次」执行生效。
- 执行目标 workflow 不存在 → 404（既有哨兵）。
- 保留期配置 ≤0 → 清理任务关闭并告警日志（「永不删除」用超大值表达，无第三态）。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: 系统 MUST 提供 `POST /workflows/{id}/execute` 执行接口：正式运行仅允许 published 状态（draft / disabled → 503 `WORKFLOW_NOT_PUBLISHED`，文案区分两态）；入参为单一 `input` 字符串（必填，≤16384 字符）；同步返回 run_id / status / output / duration_ms / node_trace。
- **FR-002**: 系统 MUST 支持试运行：`?trial=true` 放开 draft / disabled 的执行限制（状态机唯一例外，正式路径 503 语义不变）；试运行照常落轨迹并带 `is_trial` 标记，LLM 调用成本照常记录。
- **FR-003**: 执行 MUST 以快照语义加载整图：执行开始一次性读入内存，进行中执行不受并发编辑影响；从入口节点线性游走，必然终止（既有图校验保证无环与节点数上限）。
- **FR-004**: 终止语义 MUST 为：当前节点无出边 → 该节点输出即工作流终稿；显式 end 节点 → 按其 output 模板渲染终稿。
- **FR-005**: 执行期 MUST 维护扁平字符串变量池（`input` + 各节点 key → 输出）；llm.prompt、api 节点 url / headers / body、end.output、condition.expression 中的 `{{var}}` 引用 MUST 被渲染；引用缺失变量 MUST 报执行错误（message 带 `node <key>:` 前缀）。
- **FR-006**: 保存期 MUST 静态校验模板引用：引用名 ∈ {input} ∪ 该节点的祖先节点 key 集（整图提交可计算）；违例 → 400，details 带节点 key 与引用名。
- **FR-007**: condition 节点 MUST 求值为字符串（迷你表达式：`{{var}}` 或 `{{var}} == 'literal'`），出边按声明顺序取首条条件匹配者；无命中出边 MUST 报执行错误（400 图缺陷类）。
- **FR-008**: 六类节点执行语义 MUST 满足：llm → 统一 LLM 接入层非流式调用（调用明细由执行器记入既有 executions 观测链，调用上下文标识置空以区分 workflow 节点调用）；knowledge_retrieval → 知识库检索并格式化为编号段落文本落池；api → 出站 HTTP（超时 1-60s 默认 10s、可跳过证书校验）；tool → 执行期 fail-fast（400 图缺陷类）；end → 拼终稿；condition → 纯内存求值。
- **FR-009**: api 节点出站调用 MUST 实现场景优先的 SSRF 防护：恒禁回环 / 链路本地 / IPv6 ULA；RFC1918 默认放行；地址校验发生在建连时（防 DNS rebinding）；重定向每跳复验；仅允许 http / https；提供环境开关一键禁全部私网。拦截 → 500 环境限制类。
- **FR-010**: 引擎错误语义 MUST 二分：图缺陷类（condition 无命中、缺失变量运行期兜底、tool 未支持）→ 400 `VALIDATION_FAILED`（message 带 `node <key>:` 前缀）；环境限制类（SSRF 拦截、总时长超限）→ 500 `WORKFLOW_EXECUTION_FAILED`；下游模块哨兵错误原样透传。
- **FR-011**: 单次执行 MUST 受总时长上限 5 分钟约束：超时主动终止并归入环境限制类错误（500），留下完整失败轨迹。
- **FR-012**: 每次执行 MUST 落两层轨迹：run 行（工作流、触发来源、试运行标记、入参 / 终稿、状态、失败节点、耗时、trace_id）+ 每执行节点一行（seq 执行序号、key / type、入出参摘要、错误、耗时）；失败路径也落；执行结束一次性写入（无「运行中」可变态）。
- **FR-013**: 轨迹大文本 MUST 截断：单值超 16KB 截断并带 `truncated: true` 标记；节点级结构化日志截 1KB；截断只影响轨迹存储，不影响执行结果本身。
- **FR-014**: 轨迹写入失败 MUST 降级不阻断：重试一次仍败 → 执行结果照常返回、结果中 run_id 置空、错误日志带 trace_id。
- **FR-015**: 系统 MUST 提供轨迹保留期配置（默认 365 天）与配套后台批量清理任务（应用启动即运行、随优雅关停退出；配置 ≤0 关闭并告警）。
- **FR-016**: 执行器 MUST 预留节点级进度回调注入点（未注入时零开销）；MUST NOT 提供运行中状态轮询接口（进度推送归后续 chat 触发 spec）。

### Key Entities

- **WorkflowRun（运行轨迹）**: 一次执行一行；弱引用工作流（含名称快照，工作流删除后轨迹仍可读）；记录触发来源（console / chat）、试运行标记、入参 / 终稿（截断后）、状态、失败节点、耗时、trace_id、执行起点。
- **WorkflowNodeRun（节点轨迹）**: 每执行节点一行；seq 为执行顺序唯一事实源（run 内唯一）；随所属 run 级联删除；入出参为截断摘要非全量快照。
- **变量池（执行上下文）**: 单次执行内的扁平字符串黑板——`input` 与各节点输出；模板渲染与条件求值共用同一求值入口；节点成功才落池。
- **RunResult（执行结果）**: 同步返回给调用方的载荷——run_id（降级时空）、状态、终稿、总耗时、节点轨迹摘要。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 已发布工作流可端到端执行：六类节点各自语义正确、条件分支按声明顺序首条命中、终稿正确返回、node_trace 覆盖全部执行节点。
- **SC-002**: 错误路径行为确定且机器可判：图缺陷类 → 400（`node <key>:` 前缀定位）、环境限制类 → 500 `WORKFLOW_EXECUTION_FAILED`、下游哨兵透传既有码、未发布 → 503；错误码即哨兵文案，前端可直接分支。
- **SC-003**: 轨迹完整可回放：成功 / 失败路径均落两层轨迹、seq 连续唯一、16KB 截断带标记、写入降级时结果照返且 run_id 置空。
- **SC-004**: 既有功能零回归：workflow 模块既有测试全绿（含保存期新增的模板引用校验），模块测试覆盖率 ≥80%；依赖红线成立（workflow 不 import chat）。
- **SC-005**: 以实现 spec 验收门为准：`docs/changelog/workflow/impl_spec_06_execution_engine.md` §7 全部满足（全量构建 / vet / 测试门禁全绿、迁移 19 条 applied、零真实依赖的同包测试）。

## Assumptions

### 范围边界（明确不做，照搬 impl spec 06 §3）

- chat 触发接线（含触发形态与 per-node 流式的 SSE 事件形态）——后续独立 chat spec；本篇只保证 Execute 对进程内调用方可用。
- mcp tool 节点实际执行——mcp 模块未建，执行期 fail-fast（保存含 tool 节点的图仍合法）。
- 运行中状态查询 / 轮询、异步执行 / 任务队列 / 提交-轮询模型——一期 execute 是同步调用。
- 并行分支 / 合并 / 循环节点 / 子工作流——既有图校验挡住；子工作流留缝已记录（string→string 设计 + 递归深度上限 + parent_run_id，到触发再纯增量）。
- 通用 hook / 回调注册体系——节点轨迹结构化日志直写；仅留单一进度回调注入点。
- 发布快照 / 版本化——执行读实时图，编辑立即对下一次执行生效。
- 前端 workflow 页面（列表 / 编辑 / 发布 / 执行测试）——独立前端增量，不阻塞本篇。
- 定时触发 / 事件触发——execute 只被控制台或 chat 调用。

### 其余假设

- 前置 spec 01-05 已合入（commit `c06a79b`：CRUD + 图校验 R1-R9 + 状态机 + agent 绑定）；当前迁移 18 条 applied，本篇新增 00019（两张轨迹表，DDL 已冻结于 db_model.md §12）。
- O1-O7 设计决策已全部拍板并冻结（2026-09-17/18，impl spec 06 §5）：单一 input、扁平字符串变量池 + strict + 保存期静态校验、`?trial=true` 试运行、错误二分法 + tool 节点执行期 fail-fast、总时长 5min、SSRF 场景优先、两层轨迹表 + 收尾统一写 + 降级不阻断 + 365 天保留期 + 16KB 截断。
- 下游消费者：控制台 HTTP 调用方（本篇交付路由）；chat 触发接线（含 per-node 流式回调的具体形态）为后续独立 spec——本篇只保证 Execute 对进程内调用方可用。
- llm 节点调用明细由**执行器自记** executions（2026-09-18 clarify 拍板）：复用调用方服务层写入先例，调用上下文标识置空 = workflow 节点调用（logging model 既有预留语义）；executions 写入缝以窄接口注入 workflow service，组合根既有实例直用。impl spec 06 §4.2 原「platform/llm 自动完成、执行器不另记」系事实性错误注记，已经用户批准同步更正。
- 冻结契约（接口形状、DDL、哨兵文案、错误语义、代码骨架）以 `docs/changelog/workflow/impl_spec_06_execution_engine.md` 与 api_contract.md §5 / db_model.md §12 为唯一权威，本文件只描述 WHAT/WHY。
- 无前端改动（workflow 页面独立增量）；无新增第三方依赖（SSRF 校验用标准库网络能力）。
