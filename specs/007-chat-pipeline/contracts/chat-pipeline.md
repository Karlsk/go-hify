# Contract: chat 管道接线（chat-pipeline）

> Phase 1 产物。对外 HTTP 面**零新增**（复用 `POST /conversations/{id}/messages` 既有契约）；本文件记录三份契约：① 进程内 `workflowExecutor` 消费契约 ② 管道行为契约（SSE / reply / 入参映射） ③ 错误映射契约（handler 消费）。上位权威：impl_spec_07 §4 / api_contract §5 §8，冲突时以上位为准。

## ① 进程内契约：workflowExecutor（chat service → workflow api）

```go
// chat/service/service.go 小接口 type block 增员（agentGetter 等五兄弟同款）
// workflowExecutor chat 用到的 workflow 能力（管道触发——绑定 agent 的消息先过工作流）。
type workflowExecutor interface {
    Execute(ctx context.Context, req workflowapi.ExecuteWorkflowReq) (*workflowapi.RunResultSchema, error)
}
```

- **满足方**：`workflowapi.WorkflowService`（spec 06 交付）凭结构化类型天然满足；组合根把 `workflowsvc.New(...)` 返回值直传 `chatsvc.New` 尾参。
- **调用入参**（chat 侧构造，冻结于 impl_spec_07 §4.1 / O3 / O6）：

| 字段 | 取值 | 依据 |
|---|---|---|
| `ID` | parseUint(agent.WorkflowID)（非数字 → ErrInternal 包装，脏数据防御） | §4.1 |
| `Input` | req.Content（每轮独立，历史不喂） | O3 |
| `ConversationID` | &conv.ID | O6（run 行回填 + trigger_source='chat' 判定） |
| `MessageID` | &userMsg.ID | O6 |
| `Trial` | 恒 false | chat 无试运行语义 |

- **进程内不校验 binding**：`Input` 的 `max=16384` 是 HTTP 层契约（execute 端点 body），chat 侧上限 32KB 由 SendMessageReq 既有 binding 兜底（research.md D2）。
- **ctx 透传**：调用传入的 ctx 即请求 ctx——客户端断连 → 取消传播 → Execute 游走中断（run 行照写是 workflow 侧既有语义）。
- **返回消费**：仅 `RunResultSchema.Output`（终稿）——run id / 节点摘要等 chat 层不消费（排障走运行历史）。

## ② 管道行为契约（绑定 agent 的消息）

**分叉条件**：`AgentDetailSchema.WorkflowID != nil` → 管道路径（替代语义——agent 的 system prompt / RAG 注入 / MCP 工具 / 模型循环不参与本轮）；`nil` → 原路径零改动。

**调用序**（§4.1 冻结）：setupConvAgent（属主 + agent 存在启用）→ 管道分叉 → parseUint → user 消息落库 → 首条消息标题回填 → Execute → translateWorkflowError → persistAssistant（citations=[]）→ touch → emit / reply。

**流式模式事件序列**（§4.3 冻结）：

```text
data: {"type":"delta","content":"<终稿整段>"}     ← 单条整段（唯一内容通道）
data: {"type":"done","message_id":"<assistant id>","usage":{"input":0,"output":0},"finish_reason":"workflow"}
```

无 citations 事件（空引用不发）；等待期无 ping（惰性提交期间 headerWritten=false，心跳跳过——与 LLM 首 token 等待同形态，前端既有 loading 态复用）。

**一次输出模式**：标准 respond 信封返回 `AssistantReplySchema`——Content=终稿 / Usage 全零 / FinishReason=`"workflow"` / Citations=`[]` / MessageID=assistant id 字符串化。

**下游消费者**：前端既有 SSE 渲染零改动消费 delta / done；`finish_reason="workflow"` 为新增终因枚举值（前端可分支，不分支也不破坏渲染）。

## ③ 错误映射契约（failChat 增四分支，标准错误信封）

管道路径全部失败发生在首次 emit 之前（惰性提交窗口内）→ 两模式统一标准错误信封，**SSE error 事件零使用**（O4 冻结）。

| Execute 返回 | HTTP | error.code | 文案方向 | 来源 |
|---|---|---|---|---|
| `workflowapi.ErrWorkflowNotPublished`（draft / disabled 文案已区分） | 503 | WORKFLOW_NOT_PUBLISHED | 工作流未发布或已停用，请联系管理员 | §4.2 |
| `errs.ErrValidationFailed`（图缺陷，message 带 `node <key>:` 前缀） | 400 | VALIDATION_FAILED | 前缀透传，作者可行动 | §4.2（rides FailFromSentinel 既有 400，零新分支） |
| `workflowapi.ErrWorkflowExecutionFailed`（SSRF / 超 5min） | 500 | WORKFLOW_EXECUTION_FAILED | 工作流执行失败，请稍后重试 | §4.2 |
| `workflowapi.ErrWorkflowNotFound` | 404 | WORKFLOW_NOT_FOUND | 防御（RESTRICT 挡删，理论不可达） | §4.2 |
| `providerapi.ErrModelNotFound` | 404 | MODEL_NOT_FOUND | 模型不存在 | **clarify 拍板 2026-09-20**（补齐既有缺口；对齐错误码表与 execute 端点行为） |
| 下游哨兵（`PROVIDER_BUSY` / `PROVIDER_UNAVAILABLE` / `RATE_LIMITED` …） | 既有映射 | 既有码 | 既有文案 | failChat / FailFromSentinel 既有分支 |
| 未识别错误（DB 等） | 500 | INTERNAL_ERROR | 不泄露细节，trace_id 入日志 | FailFromSentinel default（与 execute 端点同形） |

**service 层出口**（两模式单一事实源）：`translateWorkflowError`——哨兵本体原样透传（保留 node 前缀 message 与链路），未识别错误仅 `%w` 补上下文（research.md D3）。

**错误语义注记**：§4.2 表 retryable 列（NotPublished=false / 图缺陷=false / ExecutionFailed=true / NotFound=false）为文档性语义——标准信封无 retryable 字段，SSE error 事件本路径零使用，不序列化。
