# Workflow 实现 spec 07：chat 管道接线（Chat Pipeline）

> 状态：**契约已冻结（2026-09-18）**——O1-O8 逐项交互冻结完毕（拍板记录见 §5）；本篇即 rdp-implementation 的消费契约，行级实现细节可在实现期微调，语义以本篇为准，契约变更须用户显式批准。递延项与已锁方向见 [deferred_items.md](./deferred_items.md)。
> 上位契约：[CLAUDE.md](../../../../CLAUDE.md)《代码组织规范》依赖清单（**chat → workflow**「对话中可触发工作流执行」，预授权）、《接口规范》对话接口（SSE 事件契约）；[impl_spec_05_agent_binding.md](./impl_spec_05_agent_binding.md) §1.1 E1（触发形态递延到本篇拍板）与拍板 C2（叠加语义——绑定校验层面）；[impl_spec_06_execution_engine.md](./impl_spec_06_execution_engine.md)（Execute 冻结契约、错误二分法 O4、onNodeDone 留缝、00019 轨迹表）；[api_contract.md](./api_contract.md) §5（ExecuteWorkflowReq / RunResultSchema）；chat 侧 [data_flow_and_model.md](../chat/data_flow_and_model.md)（v1 范围与错误约定）。冲突时停下来问用户。
> 前置依赖：spec 01-06 全部合入（执行引擎，commit `5d3297e`；迁移 19 条 applied）。
> 迁移假设：**零迁移**——chat 侧无新表新列（messages 既有 assistant 行承载终稿）；workflow 侧复用 00019 既有列（conversation_id / message_id / trigger_source CHECK 已含 `'chat'`）。下一迁移号 00020 留给 spec 08。

## 1. 背景

spec 05 递延的 E1 触发形态在本篇闭环：**管道形态**——消息进对话循环前，若 agent 绑定了 workflow，则确定性先过 workflow，终稿直接作为本轮 assistant 回复（2026-09-18 用户拍板）。工具形态（workflow 包装为模型可调用的虚拟工具）前置 chat 工具循环与 mcp 执行能力，已定候选记入备忘录（[docs/tools/workflow-task-tool-memo.md](../../../tools/workflow-task-tool-memo.md)），归后续工具篇。

底座已备（本篇零重复建设）：

| 组件 | 现状 | 本篇用法 |
|---|---|---|
| Execute 进程内接口 | `workflowapi.WorkflowService.Execute`（同步纯函数，不感知 SSE，spec 06 冻结） | chat 持小接口（单方法收窄，消费方小接口惯例），组合根注入 api 实现 |
| run 行引用列 | 00019：`trigger_source='chat'` 由 `ConversationID != nil` 判定（已实现）+ conversation_id / message_id 弱引用 | chat 传两个字段即得触发来源与回溯锚 |
| agent 绑定 | `AgentDetailSchema.WorkflowID *string`（spec 05）；chat setupTurn 每轮已加载、未消费 | 判空分叉点即此字段，agent 侧零改动 |
| SSE 惰性提交 | chat handler 首次 emit 才写 200 头；心跳仅流开始后发 | 管道路径全部失败发生在首 emit 之前 → 两模式统一标准错误信封 |
| 超时对齐 | workflow 总时长 5min = nginx SSE 读超时 300s（spec 06 O5） | 管道嵌进一轮 SSE 的等待上限天然有界 |
| 等待期形态 | 惰性提交期间无字节无 ping，与 LLM 首 token 等待同形态 | 前端既有 loading 态复用，零新呈现机制 |

**分型时序**：本篇先于 spec 08（分型）——本篇无类型概念，绑定即管道；spec 08 引入 `type` 后**本篇语义不变**（绑定不按类型把关，归工具篇，见 spec 08 §3）。

## 2. 做什么（范围）

1. **chat service 持 workflow 小接口**：`workflowExecutor`（单方法 `Execute`），组合根把 workflowSvc 注入 `chatsvc.New`。
2. **runTurn 管道分支**（turn.go）：conv/agent 装配后判 `agent.WorkflowID != nil` → 管道路径（§4.1）；为 nil → 原路径**零改动**。
3. **setupTurn 拆半**（O8 候选）：`setupConvAgent`（会话属主 + agent 存在且启用）与 `setupLLMClient`（模型解析 + client 构造）两段；管道路径只走前段。
4. **错误翻译与映射**：`translateWorkflowError`（chat service，两模式单一事实源，对齐 `llmErrorSpec` 形态）+ handler `failChat` 补三个 workflow 哨兵映射（§4.2）。
5. **文档同步**：data_flow_and_model.md 路线图更新、manual-test chat 冒烟小节 + workflow 冒烟小节增 `trigger_source='chat'` 项；CLAUDE.md 错误码表零新行（哨兵全部复用，仅新增消费点）；遗留事项清单 [deferred_items.md](./deferred_items.md)（memory 开关 / 进度事件等递延项与已锁方向）。

## 3. 不做什么（边界）

- **工具形态**（task 型虚拟工具、tool_call / tool_result 事件生产）——前置 chat 工具循环（现状 ToolIDs 读到不执行）+ mcp 执行能力；候选 Option 2 已记备忘录，归工具篇。
- **workflow 分型与嵌套**——spec 08（`type` 列、sub-workflow 节点、嵌套矩阵）。
- **memory 开关（管道带历史）**——方向已锁（2026-09-18）：上下文组装在 chat/agent 侧，开 memory 时由 chat 拼历史进 input 字符串，Execute 契约不动（spec 06 O1 单一入参终形不重开）；本篇唯一行为为每轮独立，开关落地（配置位归属与拼装格式）归后续篇纯增量。
- **进度事件**（onNodeDone → SSE 事件、节流）——onNodeDone 继续留缝不动（spec 06 FR9；service 级字段无按请求注入路径，动它需改 Execute 契约）；归工具 / 体验篇。
- **流式终稿**——Execute 流式变体不做（引擎冻结契约不动）；终稿单条整段 delta 一次性呈现（O5），打字机动画归前端。
- **NotPublished 降级普通对话**——候选 O4 拍板硬错误（显式报错可排障；静默降级会让「workflow 被停用 / 未发布」数周无人发现）。
- **budget 接线**——roadmap 独立项（组合根 TODO 未动）。
- **chat 侧 executions**——管道路径 chat 层零 LLM 调用，不记 executions；workflow 内部 llm 节点自记（conversation_id 置空契约不变，spec 06 更正注记），trace_id 串两链。
- **前端**——打字机动画、管道分支 UI 提示归前端篇。

## 4. 行为语义（已冻结，2026-09-18）

### 4.1 插入点与调用序

```text
runTurn(ctx, req, emit):
  conv, agent := setupConvAgent(ctx, req.ConversationID)     // 拆半①：属主 + agent 存在且启用
  if agent.WorkflowID != nil:                                 // 管道分支（O2 替代语义）
      wfID := parseUint(agent.WorkflowID)                      // 脏数据防御：非数字 → ErrInternal（同 ModelID 处理）
      userMsg := persist user message                         // 位置语义与原路径一致：全部校验通过后落库
      backfill title if conv.Title == ""                      // 首条消息回填标题，同原路径
      res, err := workflows.Execute(ctx, ExecuteWorkflowReq{
          ID: wfID, Input: req.Content,
          ConversationID: &conv.ID, MessageID: &userMsg.ID,   // O6；Trial 恒 false
      })
      if err != nil: return nil, translateWorkflowError(err)  // O4：首 emit 前失败 → 两模式统一标准信封
      assistant := persistAssistant(res.Output, citations=[]) // 失败只 WARN 不阻断（同原路径）
      touch conversation
      if emit != nil:
          emit(DeltaEvent(res.Output))                        // O5：单条整段——done 不带 content，delta 是唯一内容通道
          emit(DoneEvent(assistant.ID, Usage{0,0}, "workflow"))
      return reply                                            // Content / Usage{0,0} / FinishReason="workflow" / Citations=[]
  // ── 以下原路径零改动 ──
  setupLLMClient(...) → loadHistory → persist user → buildSystemPrompt → stream → 收尾
```

要点：

- **替代语义**（O2，已拍板）：绑定即接管本轮——agent 的 system prompt / RAG 检索注入 / MCP 工具 / 模型循环全部不参与；spec 05 C2「叠加」是绑定校验层面（model / RAG / 工具照常可配），运行时形态在本篇拍板为替代。Execute 恒为单轮纯函数，上下文组装归 chat/agent 流程。
- **多轮语义**（O3，已拍板）：`input = req.Content`（spec 06 O1 单一入参终形），历史不喂 workflow——每轮独立执行；messages 照常落库，下轮历史可见前几轮 user/assistant 对，但 workflow 感知不到。带历史的 memory 开关锁方向缓实现（§3）。
- **取消透传**：Execute ctx 感知——客户端断连 / 请求取消 → 游走中断；run 行照写（轨迹写入 WithoutCancel 脱钩请求 ctx，spec 06 O7 既有语义）；user 消息已落、assistant 无，与原路径 LLM 流中失败同款（重试落新 user 消息，不新设规则）。
- **emit 失败**（delta / done 时前端已断连）：静默收尾（assistant 已落库无碍），同原路径断连处理。

### 4.2 错误呈现与映射（O4，已拍板）

管道路径全部失败发生在**首次 emit 之前**（惰性提交窗口内）——流式与一次输出两模式统一走**标准错误信封**，SSE error 事件零使用：

| Execute 返回 | HTTP | retryable | 文案方向 |
|---|---|---|---|
| `workflowapi.ErrWorkflowNotPublished`（draft / disabled 文案已区分） | 503 | false | 工作流未发布或已停用，请联系管理员 |
| `errs.ErrValidationFailed`（图缺陷，message 带 `node <key>:`） | 400 | false | 工作流配置缺陷（node 前缀透传，作者可行动） |
| `workflowapi.ErrWorkflowExecutionFailed`（SSRF / 超 5min） | 500 | true | 工作流执行失败，请稍后重试 |
| `workflowapi.ErrWorkflowNotFound` | 404 | false | 防御（被绑 workflow RESTRICT 挡删除，理论不可达） |
| 下游哨兵（`MODEL_NOT_FOUND` / `PROVIDER_BUSY` / `RATE_LIMITED` / `PROVIDER_UNAVAILABLE` …） | 沿用既有映射 | | Execute 原样透传（spec 06 O4），failChat 既有分支覆盖 |

### 4.3 SSE 事件序列

流式模式管道路径完整序列：`delta(终稿整段)` → `done`。无 citations 事件（空引用不发，既有规则）；等待期无 ping（惰性提交期间 headerWritten=false，心跳跳过——与 LLM 首 token 等待同形态）。

## 5. 拍板项（O1-O8 已全部拍板，2026-09-18）

| # | 事项 | 状态与候选 |
|---|---|---|
| O1 | E1 触发形态 | **✅ 已拍板（2026-09-18，用户）：管道形态先行**——工具形态前置工具循环，候选记备忘录另立篇 |
| O2 | 管道-替代语义 | **✅ 已拍板（2026-09-18，用户）：替代**——绑定即接管，prompt / RAG / 工具 / 模型循环不参与本轮；上下文组装归 chat/agent 流程，Execute 恒单轮纯函数。备选「增强」（终稿后再过模型循环）否决——双倍 LLM 成本 + 两次调用的错误面，简版不值 |
| O3 | 多轮语义 | **✅ 已拍板（2026-09-18，用户）：每轮独立**——input = 当前消息，历史不喂；messages 照落供前端展示。memory 开关（带历史）锁方向缓实现：组装在 chat 侧拼入 input、Execute 契约不动，归后续篇纯增量 |
| O4 | 错误呈现 | **✅ 已拍板（2026-09-18，用户）：统一标准信封 + NotPublished 硬错误**——首 emit 前失败，两模式一致（零 SSE error 事件）；未发布 / 停用直接 503 不降级普通对话（显式报错可排障） |
| O5 | SSE 终稿呈现 | **✅ 已拍板（2026-09-18，用户）：单条整段 delta + usage 全零 + finish_reason="workflow"**——done 不带 content，delta 是唯一内容通道（SSE 契约不改）；usage 零（RunResultSchema 无 usage，token 在节点 executions）；citations 不发；打字机动画归前端 |
| O6 | run 行引用回填 | **✅ 已拍板（2026-09-18，用户）：ConversationID=conv.ID、MessageID=触发 user 消息 id、Trial 恒 false**——Execute 返回时 assistant 行尚不存在，等其落库再回填会新增 UPDATE 写路径且失败路径语义分裂（否决）；都不传则引用列白设（否决） |
| O7 | 进度事件 | **✅ 已拍板（2026-09-18，用户）：本篇不做**——onNodeDone 留缝不动（动它需改 Execute 冻结契约）；递延上下文记入 [deferred_items.md](./deferred_items.md)，触发时机 = 工具 / 体验篇 |
| O8 | 模型解析跳过 | **✅ 已拍板（2026-09-18，用户）：拆半，管道跳过**——管道路径只走「属主 + agent 启用」检查，不解模型不建 client（模型这轮用不上，agent 模型配坏不挡管道）；原路径零改动 |

## 6. 交付物（已定稿）

| 层 | 交付物 |
|---|---|
| chat api | 零改动（复用 `SendMessage` / `Stream`；`AssistantReplySchema` 字段现成） |
| chat service | `turn.go` 管道分支 + setupTurn 拆半 + `workflowExecutor` 小接口 + `translateWorkflowError`；收尾复用 `persistAssistant` / `TouchConversation` |
| chat handler | `failChat` 补 workflow 三哨兵映射（503 / 500 / 404） |
| 组合根 | `chatsvc.New` 增注入 workflowSvc（api 实现满足小接口，结构化类型天然满足） |
| 文档 | data_flow_and_model.md 路线图更新；manual-test chat 冒烟小节 + workflow 小节 `trigger_source='chat'` 项；CLAUDE.md 错误码表零新行；遗留事项清单 deferred_items.md |

## 7. 验收门（spec 01-06 同款门禁）

- `go build ./...` / `go vet ./...` / `go test ./... -race -count=1` 全绿；chat 包覆盖率 ≥80%（workflow 模块零行为改动）。
- 依赖方向 grep：`internal/chat` 允许 import `workflow/api`（依赖清单白名单）；`internal/workflow/` 下无 `internal/chat` import（既有红线不破）。
- 零真实依赖同包测试：service stub `workflowExecutor`（doubles 既有风格）、handler httptest。
- 重点表驱动用例：未绑 agent 原路径零回归（既有测试全绿即证）；绑定 → Execute 被调且入参断言（ID / Input / ConversationID / MessageID / Trial=false）；终稿组装（一次输出 reply 全字段 / 流式 delta+done 序列）；错误映射四类（NOT_PUBLISHED 503 / 图缺陷 400 / EXECUTION_FAILED 500 / NOT_FOUND 404）+ 下游哨兵透传；断连（emit 失败静默收尾、ctx 取消透传 Execute）；首条消息标题回填。
- 人工项（增量三步走，2026-09-18 用户定）：① 不绑 agent 冒烟——原路径行为无变化；② 绑定 agent 冒烟——SSE 收 delta+done、一次输出模式收信封；③ psql 查 `workflow_runs`（`trigger_source='chat'`、conversation_id / message_id 回填、is_trial=false）+ `workflow_node_runs`（seq 顺序、input/output 为 16KB 截断摘要 jsonb，全量只在 API 返回值）。

## 可提交节点（草案）

`feat(chat): workflow 管道接线——绑定 agent 的消息确定性先过工作流（spec 07）`
