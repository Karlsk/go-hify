# Implementation Plan: workflow react 节点 + ragNode 画布接入 + tool 节点表单

**Branch**: `020-workflow-react-node` | **Date**: 2026-10-10 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/020-workflow-react-node/spec.md`（2026-10-10 起草 + Clarifications 两条：tool_chain 可观测 / executions Input 形态）

## Summary

三块画布能力收口：① **react 节点**（新 NodeType `react`，迁移 00022）——图内 mini-agent：ReactConfig{model_id, system_prompt?, prompt, temperature?, server_ids?, tool_ids?, max_rounds?}，执行链 = prompts strict 渲染 → llm 解析链 → 工具集并集（mcp `ToolSourceReq` 加 ServerIDs + store 批量查询 + toolsource 去重解析）→ `reactloop.Run` Generate 模式 → 终答落池；每轮 executions 自记 + nodeIn 带 tool_chain 可观测数组；失败分型 fail-fast 同 tool 节点先例。② **ragNode 画布接入 + query 加法键**——KnowledgeRetrievalConfig 加 `query`（空回退 input，O1 加法修订），palette + 检查器表单（KB 下拉 / top_k / query 模板）。③ **tool 节点画布一等公民**——palette + 检查器表单（mcp 工具分组单选 + args 模板行），后端零改动。palette 五扩八；零新哨兵、零新路由、chat / reactloop / 组合根零改动。

## Technical Context

**Language/Version**: Go 1.26（后端）+ Vue 3 / TypeScript / Element Plus / Vue Flow（前端既有栈）

**Primary Dependencies**: `internal/platform/reactloop`（spec 019，Generate 模式首个生产消费者）+ `internal/platform/llm` / `platform/tool` 既有公开面；mcp / rag / provider 模块仅经各自 `api` 包消费。零新增第三方依赖。

**Storage**: PostgreSQL 17——**迁移 00022**（`make migrate-status` 21 applied 已核、本篇占用）：workflow_nodes.type CHECK 重建加 'react'（drop+add 同名约束，00017/00020 模式）+ config 列 COMMENT 补 react / knowledge_retrieval.query 形态；jsonb config 内加法键无其余 DDL。

**Testing**: 后端 `go test ./... -race -count=1`；mcp store sqlmock + toolsource 并集用例；workflow api 校验/序列化/R10 用例 + service callReact 图级用例（stub client factory + stub toolSources）+ retrieve query 用例；workflow 与 mcp 模块覆盖率 ≥80%；chat 全量回归绿（ToolSourceReq 加法零影响）。前端 `cd web && npm run type-check && npm run build`。

**Target Platform**: Docker Compose 单机 + 浏览器 SPA

**Project Type**: 模块化单体（mcp / workflow 两模块 + 前端 workflow 视图族）

**Performance Goals**: react 节点 = N 轮 LLM 调用串行（≤max_rounds+1），墙钟受 workflow 5min 总超时与嵌套每层自包约束（既有）；ListToolsByServerIDs 单 SQL IN 批量（无 N+1）。

**Constraints**: 冻结契约（spec FR-001~010 + Clarifications）：ReactConfig 键集与校验域、ServerIDs 并集去重语义、query 空回退 input、失败分型四路、nodeIn 字段面含 tool_chain、executions 每轮 Input 形态；依赖方向 workflow → mcp/rag/provider 只 import api 包；设计 token 只增不改（三新节点着色 = 既有六色族同族深浅配对：llm/react=primary 浅/深、api/tool=accent 浅/深、workflow/knowledge_retrieval=info 浅/深，实施时按 tokens.css 实际色阶取值）。

**Scale/Scope**: 后端 9 源文件（migrations/00022、mcp api/schema.go、mcp store/store.go、mcp service/service.go、mcp service/toolsource.go、workflow api/schema.go、workflow service/service.go、workflow service/executor.go）+ 4 测试文件；前端 4 文件（api/workflow.ts、graph.ts、CanvasEditor.vue、NodeInspector.vue）。

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| 原则 | 判定 | 依据 |
|---|---|---|
| I. Simplicity First | ✅ PASS | react 节点消费 019 reactloop 零自写循环；palette/检查器复用既有组件模式（模型下拉 / 分组多选 / KV 行均有先例）；不引入 builtin 机制（deferred #9） |
| II. 模块化单体与单向依赖 | ✅ PASS | workflow → mcp 仅经 mcpapi（ToolSourceReq 加法）；mcp store/service 改动为模块内部；rag 仅经 ragapi 既有 Retrieve；无新跨模块方向 |
| III. 统一 LLM 接入层 | ✅ PASS | react 的 LLM 调用经 platform/llm client + reactloop（超时/重试/熔断/bulkhead 不新写）；Generate 模式复用既有槽位与三层超时 |
| IV. SSE 链路 | ✅ PASS | 不涉 SSE（workflow 执行单次返回；react 非流式拍板） |
| V. 数据库纪律 | ✅ PASS | 00022 只增不改、CHECK 重建同 00017/00020 先例、无新表无新索引（mcp_tools 已有 (mcp_server_id) 索引服务 ListToolsByServerIDs） |
| VI. 可观测与成本护栏 | ✅ PASS | executions 逐轮自记（ConversationID=nil 标识）+ nodeIn tool_chain 补 workflow 无 messages 表的排障缺口；max_rounds 1-16 保存期护栏防成本失控 |
| VII. 统一契约与安全基线 | ✅ PASS | 零新错误码（复用 MCP_TOOL_NOT_FOUND / VALIDATION_FAILED / KNOWLEDGE_BASE_NOT_FOUND / 下游哨兵透传）；config 外键保形规则延续（model_id 字符串、id 数组数字形态有 agent 先例） |

## Project Structure

### Documentation (this feature)

```text
specs/020-workflow-react-node/
├── spec.md              # 定稿 Feature Spec（含 Clarifications 2026-10-10 两条）
├── checklists/
│   └── requirements.md  # specify 质量清单（16/16）
├── plan.md              # 本文件
├── research.md          # Phase 0：实现级决策 R1~R10
├── data-model.md        # Phase 1：ReactConfig / ToolSourceReq 扩展 / query 键 / 迁移形态
├── contracts/
│   └── node-contract.md # Phase 1：三节点 jsonb 契约 + 执行/观测面 + 迁移 SQL 形态
├── quickstart.md        # Phase 1：验证场景（含 migrate-up 前置）
└── tasks.md             # Phase 2（/speckit-tasks 产出）
```

### Source Code (repository root)

```text
migrations/
└── 00022_workflow_react_node.sql   # CHECK 重建加 'react' + config COMMENT 补两形态（只增不改）

internal/mcp/
├── api/schema.go                   # ToolSourceReq +ServerIDs []uint64（加法，注释标并集语义）
├── store/store.go                  # +ListToolsByServerIDs（WHERE mcp_server_id = ANY($1)，显式列禁 *）
└── service/
    ├── service.go                  # Store 接口 +ListToolsByServerIDs（依赖倒置同既有）
    └── toolsource.go               # ensure 并集解析：toolIDs ∪ serverIDs 按 tool id 去重 →
                                    #   停用 server 过滤 → 派生调用名（既有语义逐项保持）

internal/workflow/
├── api/schema.go                   # +NodeReact 常量 + ReactConfig（七键 + Validate 域校验）
│                                   #   + ParseNodeConfig 分支；KnowledgeRetrievalConfig +Query
└── service/
    ├── service.go                  # precheckRefs +NodeReact（model_id 预检同 llm）；
    │                               #   nodeTemplateRefs +react（prompt+system_prompt）/ kr（query）分支
    └── executor.go                 # +callReact（渲染→解析→Registry fail-fast→reactloop.Run Generate
                                    #   →OnRound 记 executions→tool_chain 累积→nodeIn→终答落池）；
                                    #   retrieve 改造（query 渲染 / 空回退 input / nodeIn 记渲染值）

web/src/
├── api/workflow.ts                 # +ReactNodeConfig / KnowledgeRetrievalNodeConfig / ToolNodeConfig 类型面
└── views/workflow/
    ├── graph.ts                    # PALETTE 五扩八 + NODE_TYPE_NAMES 三新增
    ├── CanvasEditor.vue            # palette 三新入口 + 色点 css 三新增 + 节点着色 deep 选择器三新增
    └── NodeInspector.vue           # 三分支表单：react（模型/server 多选/工具分组多选/双 TemplateField/
                                    #   温度/max_rounds）· knowledge_retrieval（KB 下拉/top_k/query）·
                                    #   tool（工具分组单选/args KV 行）
```

**Structure Decision**: 四层不动——mcp 改动在 api/store/service 三层内部加法；workflow 改动在 api 契约层与 service 执行层（store/handler 零改动：config jsonb 透传、无新查询）；组合根零改动（react 复用既有 toolSources 注入位）。

## Complexity Tracking

> Constitution Check 全 PASS，无 violation 需登记。
