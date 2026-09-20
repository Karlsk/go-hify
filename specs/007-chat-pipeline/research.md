# Research: Workflow 管道接线（chat-pipeline）

> Phase 0 产物。O1-O8 已在 impl_spec_07 §5 全部拍板冻结，本文件只记录**落位级实现决策**（冻结契约之下的行级选择），每条含依据与被否备选。代码位置以 branch `007-chat-pipeline` 基线（spec 06 合入后）为准。

## D1 setupTurn 拆半形态

- **Decision**: `setupTurn`（turn.go:198-228）拆为两段方法：`setupConvAgent`（getOwnedConversation → agents.Get → Enabled 检查，产出 conv + agent）与 `setupLLMClient`（ModelID parse 脏数据防御 → ResolveLLMConfig → clients.Client，补全模型 / client 半边）。`llmSetup` struct 保留——setupConvAgent 产出前半，setupLLMClient 补全后半；原路径 runTurn 依次调两段（行为零改动），管道路径只调前段。
- **Rationale**: 拆半是 O8 拍板「管道跳过模型解析」的最小实现；保留 llmSetup 单 struct 让原路径收尾件（recordExecution 等用 setup.cfg / setup.client）零改动。
- **Alternatives**: 拆成两个独立返回 struct——否决，llmSetup 是包内实现细节，拆两个 struct 是无谓复杂度；在管道路径 if-else 内联判空跳过模型段——否决，隐藏的装配跳过不可测，显式两段方法让「管道只走前段」成为可断言的结构。

## D2 workflowExecutor 小接口与进程内入参

- **Decision**: `workflowExecutor` 单方法接口（`Execute(ctx context.Context, req workflowapi.ExecuteWorkflowReq) (*workflowapi.RunResultSchema, error)`）加入 service.go 既有小接口 type block（agentGetter / llmConfigResolver / llmClientFactory / executionWriter / ragRetriever 同款）；`workflowapi.WorkflowService` 凭结构化类型天然满足，组合根直传 `workflowSvc`。进程内调用**不过** binding 校验——`ExecuteWorkflowReq.Input` 的 `max=16384` 仅 HTTP 层生效，chat 直传 `req.Content`（上限 32KB 由 SendMessageReq 既有 binding 兜底），不加额外校验。
- **Rationale**: 消费方小接口收窄是 CLAUDE.md《跨模块调用规则》补充机制 + 模块内既有形态（五兄弟同款）；契约伪代码 Input=req.Content 无校验步，binding tag 是 HTTP 契约不是进程内契约，chat 侧已有自己的输入约束。
- **Alternatives**: 直接持有完整 `workflowapi.WorkflowService`——否决，Go 小接口惯例（只用到 1 个方法）且 stub 更薄；进程内补一道 input≤16384 校验——否决，会对 16KB-32KB 的合法 chat 消息产生伪 400，且契约无此要求。

## D3 translateWorkflowError 形态（错误出口）

- **Decision**: 形态对齐 `translateLLMError`（turn.go:551）：Execute 返回的**哨兵本体原样返回**（保留 `node <key>:` 前缀 message 与错误链——errors.Is 全程可用），**非哨兵**（DB 等未识别错误）仅 `%w` 补上下文包装（"workflow execute: ..."），交 failChat 兜底 `FailFromSentinel` default → 500 INTERNAL_ERROR + 记日志。不引入 retryable 到标准信封。
- **Rationale**: Execute 错误契约已是外部形态（spec 06 O4 透传），chat 无需再翻译——translate 的价值是补日志上下文 + 作为两模式单一出口（未来加逻辑只动一处）；未知错误落 500 与 workflow execute 端点对未知错误的既有默认一致（同错同码，两前门不漂移）；retryable 是 SSE error 事件字段，管道路径零 SSE error 事件（O4），标准信封无此字段。
- **Alternatives**: 未知错误翻译成 `errs.ErrServiceUnavailable`（503 可重试，llmErrorSpec default 同款）——否决，同错不同码（execute 端点 500 / chat 管道 503），违背两前门一致性；每个哨兵显式重包装——否决，破坏 message 的 node 前缀透传契约（§4.2 要求 message 带前缀透传）。

## D4 MODEL_NOT_FOUND → 404 补齐（clarify 拍板 2026-09-20）

- **Decision**: failChat 补第四分支 `providerapi.ErrModelNotFound → 404`（"模型不存在"），同时修复原路径（setupTurn → ResolveLLMConfig 哨兵透传落 500）与管道路径（workflow llm 节点模型解析）两处缺口。
- **Rationale**: CLAUDE.md 错误码表 MODEL_NOT_FOUND→404 是上位权威；workflow execute 端点对同错误已是显式 404（workflow/handler/handler.go:172-173，spec 06 交付）；OpenAI / Anthropic 对 model 不存在均返 404（域先例，虽其 model 为客户端指定而 Hify 为存储配置悬空——但 404 的「被请求资源不存在」语义经本仓库错误码表已定）；500 方案会使同一 workflow 同一错误两前门不同码（execute 404 / chat 500 INTERNAL_ERROR），且哨兵码丢失。
- **Alternatives**: 维持 500（B）——否决，同错不同码 + 码退化；chat 与 workflow handler 都改 500（B+）——否决，动 spec 06 已交付行为 + 违 api_contract §8「映射上表现有行」冻结句。完整讨论记录见 spec.md Clarifications Session 2026-09-20。

## D5 done 事件与 reply 的 MessageID

- **Decision**: `persistAssistant` 返回的 assistant.ID 经 `strconv.FormatUint` 字符串化进 `DoneEvent.MessageID` 与 `AssistantReplySchema.MessageID`（原路径 turn.go:166-168 同款）；`DoneEvent` 三参签名不动（usage 传零值 `chatapi.Usage{}`、finishReason 传 "workflow"），done 不带 content（delta 是唯一内容通道，O5）。
- **Rationale**: 复用既有构造函数零 api 改动；MessageID 供前端重新生成 / 反馈锚定（对话接口契约）。
- **Alternatives**: 新增 done 变体构造函数——否决，参数已可表达；assistant 落库失败时 done 带 MessageID=""——按 §4.1「失败只 WARN 不阻断」语义，落库失败 reply.MessageID 为空串（原路径同款行为，不 special-case）。

## D6 组合根装配

- **Decision**: `chatsvc.New(chatStore, agentSvc, modelSvc, llmManager, execStore, ragSvc, workflowSvc)`——尾参增 `workflows workflowExecutor`（既有六参位次不动）；server.go:156「chat 本期不消费 workflow，触发接线归后续 chat spec」注释删除，166-168 chat 依赖方向注释补 `chat → workflow（workflowExecutor，管道触发）`。
- **Rationale**: 尾参追加让既有调用位次稳定可 diff；注释更新让装配注释与实际依赖一致（注释是排障线索）。
- **Alternatives**: 插参到语义位置（agent 后）——否决，纯位置偏好换 diff 噪音，无收益。

## D7 管道路径收尾顺序与断连语义

- **Decision**: Execute 成功后顺序：`persistAssistant(res.Output, citations=[])` → `TouchConversation`（失败仅 WARN）→ 流式模式 `emit(DeltaEvent(res.Output))` + `emit(DoneEvent(...))`（emit 失败静默收尾，assistant 已落库无碍）→ 返回 reply（Content=终稿 / Usage{0,0} / FinishReason="workflow" / Citations=[]）。citations 恒 `[]chatapi.Citation{}`（空数组非 nil，接口规范空值约定）。user 消息落库与标题回填在 Execute **之前**（位置语义同原路径：全部校验通过后落库；Execute 失败 user 消息已落——与原路径 LLM 失败同款，重试落新 user 消息不新设规则）。
- **Rationale**: §4.1 伪代码直译；断连 / 落库失败语义全部沿用原路径既有约定（spec 07 明确「不新设规则」）。
- **Alternatives**: Execute 失败回滚 user 消息——否决，无事务回滚先例且违背「与原路径同款」拍板；touch 失败阻断——否决，原路径 touch 失败仅 WARN（列表排序滞后非数据丢失）。
