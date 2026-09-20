# Implementation Plan: Workflow 管道接线（chat-pipeline）

**Branch**: `007-chat-pipeline` | **Date**: 2026-09-20 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/007-chat-pipeline/spec.md`

**权威契约**: 冻结契约以 [impl_spec_07_chat_pipeline.md](../../docs/changelog/workflow/impl_spec_07_chat_pipeline.md) §4（§4.1 插入点伪代码 / §4.2 错误映射表 / §4.3 SSE 事件序列）、[api_contract.md](../../docs/changelog/workflow/api_contract.md) §5（ExecuteWorkflowReq 定稿形状）与 §8（错误表）为准（本 plan 只落位与策略，不重复抄契约）。

## Summary

chat 模块消费 workflow 执行引擎（spec 06 交付）：绑定 agent 的消息确定性先过工作流——**管道形态、替代语义**（agent 的系统提示词 / RAG / MCP 工具 / 模型循环不参与本轮），工作流终稿整段一次性作为 assistant 回复（流式单条整段 delta + done，usage 全零、`finish_reason="workflow"`；一次输出完整 reply 信封）。改动全部落在既有文件：`chat/service/turn.go`（setupTurn 拆半——管道路径只走「属主 + agent 启用」段，跳过模型解析；runTurn 管道分支；`translateWorkflowError` 错误出口）、`chat/service/service.go`（`workflowExecutor` 小接口 + New 增参）、`chat/handler/handler.go`（failChat 补四哨兵分支：NOT_PUBLISHED 503 / EXECUTION_FAILED 500 / WORKFLOW_NOT_FOUND 404 / MODEL_NOT_FOUND 404〔2026-09-20 clarify 拍板补齐〕）、组合根 `app/server.go`（chatsvc.New 增注入 workflowSvc，chat 保持装配最后）。**零迁移**（19 条 applied 不变）、零新哨兵、零新增第三方依赖、chat api 零改动、workflow 模块零行为改动；未绑 agent 原路径零改动（既有测试全绿即证）。文档同步四处（data_flow 路线图、两份 manual-test、CLAUDE.md 零新行）。

## Technical Context

**Language/Version**: Go 1.26

**Primary Dependencies**: 全部既有，零新增——`workflowapi.WorkflowService.Execute`（spec 06 冻结，同步纯函数、ctx 进 `RunResultSchema` 出、不感知 SSE）、`agentapi.AgentDetailSchema.WorkflowID *string`（spec 05 交付，chat 每轮已加载未消费）、`workflowapi.ExecuteWorkflowReq`（schema.go:386-392：ID / Input / ConversationID \*uint64 / MessageID \*uint64 / Trial）、收尾复用件 `persistAssistant` / `TouchConversation` / `makeTitle`（turn.go 既有）、handler 既有 emit + 惰性提交（headerWritten）+ 心跳门（流未开始跳过 ping）。

**Storage**: PostgreSQL 17，**零迁移**——19 条 applied 不变（下一号 00020 留给 spec 08）；run 行引用列复用 00019 既有（conversation_id / message_id 弱引用、trigger_source CHECK 已含 `'chat'` 且由 `ConversationID != nil` 判定——workflow 侧已实现）；messages 既有 assistant 行承载终稿。禁 AutoMigrate。

**Testing**: `go test ./... -race -count=1`；同包 `*_test.go`，service stub `workflowExecutor`（doubles 既有风格）断言入参与两模式终稿组装，handler httptest 断言 failChat 新分支状态码与 respond 信封；零真实 PG / Redis / 网络 / LLM。chat 模块覆盖率 ≥80%。

**Target Platform**: Linux server（Docker Compose 单机）；纯后端，无前端改动。

**Project Type**: web-service（模块化单体，四层子包 api/service/store/handler）。

**Performance Goals**: N/A（内部工具 3-5 QPS 量级）；workflow 总时长 5min = nginx SSE 读超时 300s（spec 06 O5 对齐），管道嵌进一轮 SSE 等待上限天然有界，本篇零新闸门。

**Constraints**:

- 依赖红线：`chat → workflow` 为 CLAUDE.md 依赖清单预授权；`internal/workflow/` 下不得 import `internal/chat`（既有红线，验收 grep）；跨模块只 import `workflow/api`（别名 `workflowapi`）。
- service 全程 `ctx context.Context` 无 gin 类型；handler 只认本模块 api 接口。
- 冻结契约逐字保真：插入点调用序、错误映射表、SSE 事件序列以 impl_spec_07 §4 为准，哨兵文案 = `error.code`（前端直接消费），偏差 → 软门禁。
- 哨兵全部复用既有（零新码）：workflowapi 三哨兵 + errs.ErrValidationFailed（rides FailFromSentinel 既有 400）+ providerapi.ErrModelNotFound（新消费点，码既有）。
- 进程内调用不过 binding 校验：`ExecuteWorkflowReq.Input` 的 `max=16384` 仅 HTTP 层生效，chat 进程内直传 `req.Content`（上限 32KB 由 SendMessageReq 既有 binding 兜底）——不加额外校验（冻结契约 Input=req.Content 无校验步，见 research.md D2）。
- 管道路径 chat 层零 LLM 调用：不记 executions、不解模型不建 client；workflow 内部 llm 节点自记（conversation_id 置空契约不变），trace_id 串两链。
- ctx 取消透传 Execute（客户端断连 → 游走中断，run 行照写是 workflow 侧既有语义——chat 侧只保证 ctx 透传不吞）。

**Scale/Scope**: 20-50 人内部使用；改动面 = chat service 2 文件 + chat handler 1 文件 + 组合根 1 文件 + 文档 4 处；**无新文件无新包**（最小接线篇）。

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| # | 原则 | 判定 | 依据 |
|---|---|---|---|
| I | Simplicity First | ✅ 通过 | 管道分支单函数直排（§4.1 伪代码直译，无新抽象层）；`workflowExecutor` 单方法收窄（消费方小接口惯例）；零新增第三方；setupTurn 拆半是行为等价重构；被否决备选（增强语义 / 流式终稿 / 进度事件 / memory 开关）已在 impl_spec_07 §5 O1-O8 记录 |
| II | 模块化单体与单向依赖 | ✅ 通过 | chat → workflow 在依赖清单白名单内（预授权）；只 import `workflow/api`；小接口收窄 + 组合根注入（结构化类型天然满足）；handler→api、service→api 既有方向不动；chat 装配保持最后（依赖图最外层，零被依赖不破） |
| III | 统一 LLM 接入层 | ✅ 通过 | 管道路径 chat 层零 LLM 调用（不解模型不建 client，O8）；workflow 内部 llm 节点走 platform/llm 既有链路——本篇零触碰超时 / 重试 / 熔断逻辑 |
| IV | SSE 流式链路完整性 | ✅ 通过 | 惰性提交窗口内全部失败 → 两模式统一标准错误信封（O4，零 SSE error 事件）；单条整段 delta + done 不带 content（O5）；等待期无 ping（headerWritten=false 既有跳过，与 LLM 首 token 等待同形态）；客户端断连 ctx 取消透传 Execute（省 token 延续）；流已 200 后契约零变化 |
| V | 数据库纪律 | ✅ 通过 | 零迁移（19 条 applied 不变）；messages 既有行承载终稿（persistAssistant 复用，无新 SQL）；run 行引用回填靠调用传参（workflow 侧 00019 既有列），无 DDL 无新索引 |
| VI | 可观测与成本护栏 | ✅ 通过 | 管道路径 chat 层零 LLM 调用不记 executions（无调用可记，非省略护栏）；workflow 内部 llm 节点自记（spec 06 更正注记契约不变）；run 行 trigger_source='chat' + 引用回填串起对话 / 执行两链；trace_id 贯穿（httpmw.RequestID 既有） |
| VII | 统一契约与安全基线 | ✅ 通过 | 响应经 respond 信封复用；错误码全部既有哨兵（CLAUDE.md 错误码表零新行）；MODEL_NOT_FOUND→404 补齐对齐错误码表与 workflow execute 端点既有行为（2026-09-20 clarify 拍板，见 spec.md Clarifications）；500 类不泄露细节（failChat 兜底 FailFromSentinel→Error 记日志回 INTERNAL_ERROR）；无新密钥面（明文 key 链路本篇不触碰） |

**门禁结论**: 全部通过，无违规条目 → Complexity Tracking 为空。

## Project Structure

### Documentation (this feature)

```text
specs/007-chat-pipeline/
├── plan.md              # This file (/speckit-plan command output)
├── research.md          # Phase 0 output（拆半形态 / 接口形态 / 错误出口等关键实现决策）
├── data-model.md        # Phase 1 output（零迁移——既有承载与引用回填关系）
├── quickstart.md        # Phase 1 output（验证指南：自动门禁 + 人工三步走）
├── contracts/           # Phase 1 output（进程内契约 + 管道行为契约 + 错误映射）
├── checklists/
│   └── requirements.md  # /speckit-specify 质量清单（20/20 全过 + clarify 更新）
└── tasks.md             # Phase 2 output (/speckit-tasks command - NOT created by /speckit-plan)
```

### Source Code (repository root)

```text
internal/chat/
├── api/                          # 零改动（SendMessage / Stream / AssistantReplySchema 全复用）
├── service/
│   ├── service.go                # 修改：小接口 type block 增 workflowExecutor（单方法收窄）；
│   │                              #   chatService 增 workflows 字段；New 尾参增注入
│   └── turn.go                    # 修改：setupTurn 拆半（setupConvAgent + setupLLMClient）；
│                                  #   runTurn 管道分支（§4.1 伪代码直译）；
│                                  #   translateWorkflowError（管道路径错误出口）
├── store/                        # 零改动（persistAssistant / TouchConversation 复用）
└── handler/
    └── handler.go                # 修改：failChat 增四哨兵分支（ErrWorkflowNotPublished 503 /
                                   #   ErrWorkflowExecutionFailed 500 / ErrWorkflowNotFound 404 /
                                   #   ErrModelNotFound 404〔clarify 拍板〕）

internal/app/
└── server.go                     # 修改：chatsvc.New 增注入 workflowSvc（chat 装配保持最后）；
                                   #   删除 156 行「chat 本期不消费 workflow」TODO 注释并更新
                                   #   chat 依赖方向注释（+ workflow）

# 文档同步：docs/changelog/chat/data_flow_and_model.md（路线图更新）、
#   docs/testing/chat-manual-test.md（管道冒烟小节）、
#   docs/testing/workflow-manual-test.md（trigger_source='chat' 验证项）；
#   CLAUDE.md 错误码表零新行（MODEL_NOT_FOUND 行既有、补实现即准确）
# 前端 web/：零改动
```

**Structure Decision**: 模块化单体四层子包（仓规既定）；全部改动落既有文件（无新文件无新包——最小接线篇）；`workflowExecutor` 小接口放 service.go 既有 type block（agentGetter 等五兄弟同款形态，模块内小接口统一收口）；管道分支与 translateWorkflowError 放 turn.go（两模式编排单文件既有约定）。

## Complexity Tracking

> **Fill ONLY if Constitution Check has violations that must be justified**

无违规条目（Constitution Check 七项全过）。
