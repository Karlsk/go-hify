# Feature Specification: workflow react 节点 + ragNode 画布接入 + tool 节点表单

**Feature Branch**: `020-workflow-react-node`

**Created**: 2026-10-10

**Status**: Draft（待 clarify / 定稿）

**Input**: 2026-10-10 用户拍板（reactNode 关联模型 + mcp server∪tools 并集、轮数默认 8 可配、builtin 剔除、ragNode 取 B 案接画布 + query 模板、tool 节点接画布表单）；前置 [specs/019-platform-reactloop/spec.md](../../019-platform-reactloop/spec.md)（`a01b3c7`，reactloop Generate 模式待生产消费者）；上位冻结 [docs/changelog/mcp/chat_tool_loop_spec.md](../../../docs/changelog/mcp/chat_tool_loop_spec.md) 与 [docs/changelog/workflow/](../../../docs/changelog/workflow/) 系列（db_model §7 图校验 / api_contract 执行契约 / impl_spec_06 O1 query 固定 input 的加法修订）。

## Clarifications

### Session 2026-10-10

- Q: react 节点内部工具调用链的可观测载体？（workflow 无 messages 表，chat 的 tool 行落库在 workflow 无对应物；仅记「工具调用数」则排障黑盒） → A: nodeIn 增 `tool_chain` 数组——逐调用 `{round, id, name, args, is_error}`（OnToolCall/OnToolResult 闭包累积；结果文本不入，体积留给 node_runs.input 的 16KB wrap 兜底）；FR-003 nodeIn 字段面相应扩展。
- Q: react 每轮 executions 的 Input 形态？ → A: 对齐 llm 节点形状 + 轮次维度：`{prompt: 渲染后初始 prompt, system_prompt?, round: N, tool_calls: 本轮调用数}`（不记全量 sent 消息——workflow 无 summarizeMessages 先例，体积与排障收益不匹配）；Output = 该轮 content。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - react 节点：图内自主工具调用（Priority: P1）

用户在 task 型 workflow 画布拖入「ReAct 节点」：选模型、绑工具集（mcp server 的全部工具 ∪ 单独勾选的 mcp tools，并集）、写 System Prompt / Prompt（{{var}} 模板）、可调轮数上限；试运行中节点内 LLM 自主多轮调用工具后给出终答文本，落变量池供下游 `{{reactKey}}` 引用；end 输出映射 / 下游 llm 节点正常消费。

**Why this priority**: 本篇核心新能力——任务型 workflow 从「线性编排」升级到「节点内自主决策」；reactloop（019）的首个生产消费者。

**Independent Test**: 画 `llm 分类 → react（绑模型 + 一个 enabled server）→ end 映射` 图，试运行触发 react 内 ≥1 轮工具调用后终答；断言终稿映射引用 react 输出成功、node_trace 含 react 单行、executions 含 react 内每轮 LLM 行（手测 §13.1）。

**Acceptance Scenarios**:

1. **Given** react 节点绑定模型 + 一个 enabled mcp server，**When** 试运行触发工具调用，**Then** 节点内多轮自主调用（server 全量工具可见）后终答落池；下游 `{{reactKey}}` 整体引用 = 终答文本
2. **Given** react 节点同时绑 server 与单独 tools（含跨 server），**When** 执行，**Then** 工具集 = 并集（按 tool id 去重），停用 server 的工具静默消失（既有语义）
3. **Given** react 节点 server_ids 与 tool_ids 均空，**When** 执行，**Then** 纯推理单轮直答（零 mcp 交互，同 llm 节点语义 + 循环单轮形态）
4. **Given** 绑定的 server 停用 / 工具全被过滤，**When** 执行，**Then** fail-fast 404 `MCP_TOOL_NOT_FOUND` 透传（同 tool 节点先例，不静默降级）
5. **Given** react 节点配 max_rounds=2，**When** 模型持续幻觉调用，**Then** 第 3 轮 nudge 摘工具收尾、终答落池（HitLimit 非错误）、nodeIn 记轮数与上限
6. **Given** react 节点 prompt 引用非祖先变量，**When** 保存，**Then** 400 `VALIDATION_FAILED` 带节点 key 与引用名（R10 覆盖 prompt 与 system_prompt 两模板）

### User Story 2 - ragNode 画布接入 + query 模板（Priority: P1）

用户在画布拖入「知识检索节点」：KB 下拉（enabled 过滤）、top_k、**query 模板**（{{var}}，空 = 回退 workflow input 既有语义）；典型编排 = llm 提炼问句 → ragNode 检索 → llm 据结果作答。存量 JSON 形态（无 query 键）行为零变化。

**Why this priority**: 既有后端节点终于画布可用 + query 模板修正「检索问句恒为整段 input」的编排限制（先提炼后检索是 RAG 编排正确形态）；与 US1 同批画布收口。

**Independent Test**: 画 `llm1（提炼 topic）→ knowledge_retrieval（query={{llm1.topic}}）→ llm2` 图试运行，断言检索命中与 llm2 作答；存量无 query 图试运行行为与升级前一致（手测 §13.2）。

**Acceptance Scenarios**:

1. **Given** knowledge_retrieval 节点配 query 模板，**When** 执行，**Then** 检索 query = 渲染后模板值（nodeIn 记渲染值）；模板缺失变量 → 图缺陷 400 带定位
2. **Given** 存量节点无 query 键（或空串），**When** 执行，**Then** query 回退 workflow input（spec 06 O1 现状零变化）
3. **Given** 画布拖入 ragNode，**When** 打开检查器，**Then** KB 下拉（enabled 过滤）+ top_k（0-20，0=默认）+ query TemplateField（占位说明空=回退 input）
4. **Given** query 引用非祖先变量，**When** 保存，**Then** R10 400 拒（新键入 R10 清单）

### User Story 3 - tool 节点画布一等公民（Priority: P2）

用户在画布拖入「工具节点」：检查器分组单选 mcp 工具（按 enabled server 分组，value=tool_id 字符串保形）+ args 键值行（值域 {{var}} 模板）；保存试运行调用成功。存量 JSON 形态 tool 节点回显表单如初。后端零改动。

**Why this priority**: 后端完备（spec 016）仅前端缺表单——补齐即画布闭环；无新契约，故排 P2。

**Independent Test**: palette 拖入 tool 节点 → 选工具 + 填 args 模板 → 保存试运行断言调用成功与 nodeIn args 渲染值；存量 JSON tool 节点编辑页回显如初（手测 §13.3）。

**Acceptance Scenarios**:

1. **Given** tool 节点检查器，**When** 打开工具下拉，**Then** 按 enabled server 分组列出其工具（disabled server 不出现）；选中后 config.tool_id 字符串保形
2. **Given** args 键值行值域填 `{{input.x}}`，**When** 保存试运行，**Then** 渲染值进工具调用参数（既有执行语义零变化）
3. **Given** 存量 JSON 形态 tool 节点（tool_id + args），**When** 进编辑页，**Then** 表单回显工具名与 args 行如初

### User Story 4 - 零回归与迁移（Priority: P2）

chat 工具对话（reactloop 换用后二次确认）、llm/end/condition/api/workflow 节点、既有 knowledge_retrieval JSON 形态、ToolSourceReq 只传 ToolIDs 的调用方（chat）——全部零回归；迁移 00022 生效（22 applied）且只增不改。

**Acceptance Scenarios**:

1. **Given** chat 绑工具 Agent 对话，**When** 多轮工具调用，**Then** 帧序 / 落库 / executions 与 019 验收时一致（手测零差异复跑）
2. **Given** 迁移应用后，**When** 存量 workflow（七类节点）加载与执行，**Then** 零行为变化；`workflow_nodes.type` CHECK 含 'react'
3. **Given** mcp ToolSourceFor 只传 ToolIDs（chat 路径），**When** 构造来源，**Then** 解析行为与扩展前一致（ServerIDs 空 = 既有路径）

---

### Edge Cases

- react 节点绑定的 server 在保存后停用 / 工具被 discover 删除：执行期空清单 → 404 fail-fast（不静默降级成纯推理——绑定即意图）
- react 与 tool 同绑一个工具（并集去重面）：server∪tools 重叠时按 tool id 去重，调用名唯一（Registry 构建期撞车校验兜底）
- max_rounds 越界（<0 或 >16）：保存期 400；0/缺省 = 默认 8（reactloop DefaultMaxRounds）
- react 节点在子工作流内（嵌套）：允许（普通节点语义），深度 / 超时沿用既有嵌套规则（每层 5min 自包）
- query 模板渲染为空串：视同未配 → 回退 input（空串与缺省同语义）
- KB 被停用 / 删除：执行期既有哨兵 `KNOWLEDGE_BASE_NOT_FOUND` 透传（现状零变化）
- tool 节点下拉数据源加载失败：拦截器已提示，下拉空 + 保存期后端 tool_id 必填校验兜底
- 前端 server_ids/tool_ids 数字数组：JSON 数字形态（agent 请求先例），不触 2^53 精度问题（内部规模）；model_id 保持字符串保形

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: 新 NodeType `react` + `ReactConfig{model_id(string tag), system_prompt?, prompt, temperature?, server_ids?[]uint64, tool_ids?[]uint64, max_rounds?int}`：model_id / prompt 必填；max_rounds 0/缺省 = 默认 8、1-16 合法、越界保存期拒；ids 元素 >0（binding/Validate 双闸）；server_ids 与 tool_ids 均空 = 纯推理（不查 mcp、reg=nil）
- **FR-002**: mcp 加法扩展：`ToolSourceReq` 加 `ServerIDs []uint64`；mcp store 增 `ListToolsByServerIDs` 批量查询（单 SQL，IN 列表）；toolsource 解析 = toolIDs ∪ serverIDs 全量工具（按 tool id 去重）→ 停用 server 过滤 → 派生调用名，既有语义逐项保持；只传 ToolIDs 的调用方零影响
- **FR-003**: react 执行链：prompts strict 渲染（缺失变量图缺陷 400 带 `node <key>:` 定位）→ 模型解析链同 llm 节点（ResolveLLMConfig → client）→ 工具集非空时 `ToolSourceFor({ToolIDs, ServerIDs})` + `NewRegistry`（构建失败 `%w` 500 / 过滤后空清单 `ErrMCPToolNotFound` 404 透传）→ `reactloop.Run` Generate 模式（MaxRounds 透传、Stream=false）→ 终答 Content 落池；HitLimit 非错误；每轮经 OnRound 自记 executions（ConversationID=nil 同 llm 节点标识；Input = `{prompt, system_prompt?, round, tool_calls}`、Output = 该轮 content）；nodeIn 记 prompt / system_prompt / max_rounds / 实际轮数 / 工具调用数 / **tool_chain 数组（逐调用 `{round, id, name, args, is_error}`，结果文本不入）**
- **FR-004**: react 保存期：model_id 存在性预检（precheckRefs 加 NodeReact 分支，同 llm 先例）；server_ids / tool_ids 不预检（执行期 fail-fast，同 tool 先例）；R10 扫 react 的 prompt 与 system_prompt 两模板（新节点无存量包袱）
- **FR-005**: ragNode query 加法键：`KnowledgeRetrievalConfig` 加 `Query string json:"query,omitempty"`；执行期非空 strict 渲染作检索 query、空/缺省 = 回退 `vars["input"]`（O1 现状）；nodeIn query 记渲染后值；R10 的 knowledge_retrieval 分支扫 query（现状该分支 return nil——新键入清单）
- **FR-006**: 迁移 00022：`workflow_nodes.type` CHECK 重建加 `'react'`（drop+add 同名约束模式，同 00017/00020）；`workflow_nodes.config` COMMENT 补 react 与 knowledge_retrieval.query 形态；只增不改、顺序幂等
- **FR-007**: 前端 react 节点：palette 新入口（palette 自五类扩至八类：+react / knowledge_retrieval / tool）+ 检查器表单——模型下拉（复用 llm 模式：enabled provider → enabled chat 模型分组）/ server 多选（enabled 过滤）/ 工具分组多选（按 enabled server 分组，同 agent 绑定先例）/ SystemPrompt + Prompt TemplateField / 温度 / max_rounds 数字输入（0 或 1-16，占位说明默认 8）；config 外键保形：model_id 字符串、server_ids/tool_ids 数字数组
- **FR-008**: 前端 ragNode：palette 入口 + 检查器表单——KB 下拉（enabled 过滤）/ top_k 数字输入（0-20，0=默认）/ query TemplateField（占位「空 = 检索整个入参 {{input}}」）；既有 JSON 形态回显如初
- **FR-009**: 前端 tool 节点：palette 入口 + 检查器表单——mcp 工具分组单选（按 enabled server 分组、value=tool_id 字符串保形）/ args 键值行（键输入 + 值域 TemplateField；空键行不产出、全空删 args 键，同 headers 先例）；后端零改动
- **FR-010**: 零回归：chat / llm / end / condition / api / workflow 节点与既有 knowledge_retrieval JSON 形态行为零变化；query 空回退 input；ToolSourceReq 只传 ToolIDs 的调用方（chat）解析行为零变化；palette 既存五类拖拽 / 检查器 / 序列化零变化

### Key Entities *(include if feature involves data)*

- **ReactConfig（新节点契约）**: model_id / system_prompt? / prompt / temperature? / server_ids?[] / tool_ids?[] / max_rounds?——jsonb 加法新类型，迁移 00022 CHECK 加值
- **ToolSourceReq（mcp 加法扩展）**: + ServerIDs []uint64——server 级绑定执行期动态全量（跟随 discover 新工具）
- **KnowledgeRetrievalConfig（加法键）**: + query（omitempty，{{var}} 模板，空回退 input）
- **mcp store ListToolsByServerIDs**: 批量查询新方法（service.Store 接口加法，store 实现）
- **前端形态**: palette 八类（+react/knowledge_retrieval/tool）；NodeInspector 三分支表单（react / knowledge_retrieval / tool）；api 层 ReactNodeConfig / KnowledgeRetrievalNodeConfig / ToolNodeConfig 类型面

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: react 节点端到端可用——绑 server 试运行自主工具调用后终答落池、下游引用成功、executions 逐轮留痕、上限收尾可控（手测 §13.1 通过）
- **SC-002**: ragNode 画布可用 + query 模板生效——先提炼后检索编排跑通、存量无 query 图零变化（手测 §13.2 通过）
- **SC-003**: tool 节点画布可用——选工具 + args 模板保存试运行成功、存量回显如初（手测 §13.3 通过）
- **SC-004**: 自动化门禁全绿——go build/vet/test 全量 + workflow 与 mcp 模块覆盖率 ≥80% + `make migrate-status` 22 applied + 前端 type-check/build 绿 + eino/go-sdk grep 纪律不变 + 依赖方向 grep（workflow 只 import mcp/api 等下游 api）
- **SC-005**: 零回归——chat 工具对话手测零差异复跑（019 换用后二次确认）；既存七类节点与只传 ToolIDs 调用方行为零变化（US4 场景全过）

## Assumptions

- 前置：spec 019 已合入（`a01b3c7`）——reactloop.Run Generate 模式与冻结常量族在位；迁移下一号 00022（21 applied 已核）
- react 节点终答为纯文本（无 output_schema 声明）——下游整体引用 `{{reactKey}}` 或经 llm 节点再结构化；需要结构化输出时编排侧加 llm 节点（明确不做 react output_schema）
- server 级绑定语义 = 执行期动态全量（discover 新增工具自动纳入、停用 server 工具静默消失）——与 tool 级绑定的「清单快照」语义分野沿用 mcp 既有设计
- max_rounds 值域 1-16 为编排侧护栏（防成本失控），默认 8 与 chat 对齐；reactloop 只做 ≤0 回退不做上界校验（上界归 workflow 保存期）
- 明确不做：builtin 工具机制（deferred_items #9）；react 流式推送；react output_schema；tool args 进 R10（存量 gap 留 deferred）；llm system_prompt 进 R10（存量保存期行为不变）；ragNode 多 KB
- 人工验收 = workflow-manual-test 增补 §13 三场景 + chat 工具手测零差异复跑（用户执行）
