# Research: workflow react 节点 + ragNode + tool 表单（spec 020 Phase 0）

**Date**: 2026-10-10 | **Spec**: [spec.md](./spec.md) | **基线**: spec 019 reactloop（`a01b3c7`）、mcp toolsource ensure（toolsource.go:95-151）、workflow 七类节点契约（api/schema.go）、迁移重建先例（00017/00020）。

产品级拍板见 spec Input（react 并集绑定 / 轮数可配 / builtin 剔除 / ragNode B 案 / tool 表单）；本文只记实现级决策。

## R1：ReactConfig 键集与校验域

- **Decision**: `{model_id(uint64 ,string), system_prompt?, prompt, temperature?, server_ids?[]uint64, tool_ids?[]uint64, max_rounds?int}`；Validate：model_id 非零、prompt 非空、max_rounds ∈ {0} ∪ [1,16]、两 ids 数组元素 >0（Validate 层挡，binding 管不到 jsonb）。
- **Rationale**: 键集对齐 llm 节点先例（model_id/system_prompt/prompt/temperature 同形）+ 并集绑定两数组 + 轮数护栏；id 数组用 JSON 数字形态（agent 请求 `ToolIDs []uint64` 先例，内部规模不触 2^53）。
- **Alternatives**: ids 用字符串数组（与 model_id 保形规则不一致——保形规则针对 `,string` tag 标量，数组无此 tag 先例，弃）；max_rounds 上界放 reactloop（platform 不该承载编排护栏，spec 已拍板归 workflow，弃）。

## R2：ToolSourceReq.ServerIDs 并集解析位点

- **Decision**: `ensure()` 内：`ListToolsByIDs(toolIDs)` ∪ `ListToolsByServerIDs(serverIDs)` → 按 tool id map 去重（toolIDs 优先保留序）→ 收集 serverIDs → `ListServersByIDs` 停用过滤 → 派生调用名——既有逐段语义不变，仅工具集来源扩并集。store 新方法单 SQL `WHERE mcp_server_id = ANY($1)`（mcp_tools 已有 `(mcp_server_id)` 索引）。
- **Rationale**: 去重在过滤前（同 tool 被 server 与 id 双绑只解析一次，调用名唯一性由 Registry 构建期兜底不变）；批量查询免 N+1；只传 ToolIDs 时 serverIDs 空 = 既有路径零变化（chat 证明面）。
- **Alternatives**: service 层先展开 server→tool ids 再走 ToolIDs（多一次查询 + 展开快照语义丢失 discover 新增工具——server 级绑定要动态全量，弃）。

## R3：callReact 执行链与失败分型

- **Decision**: strict 渲染 system_prompt（非空时）+ prompt（缺失变量 → `errs.ErrValidationFailed` 带 `node <key>:` 前缀，同 callLLM）→ ResolveLLMConfig → client → 绑定并集空 = `reg nil`（零 mcp 交互）；非空 = `ToolSourceFor({ToolIDs, ToolIDs∪ServerIDs 面})` + `tool.NewRegistry`（构建失败 `%w` 500；`Defs()` 空 → `mcpapi.ErrMCPToolNotFound` 404 透传，fail-fast 同 callTool 先例）→ `reactloop.Run(ctx, client, reg, msgs, opts, Config{MaxRounds, Stream:false}, hooks)` → `res.Content` 落池；`res.HitLimit` 非错误（终答照落）；LLM 调用错误原样透传（下游哨兵 / llm 错误类同 callLLM）。
- **Rationale**: 分型对齐 O4 二分法（渲染缺失 = 图缺陷 400 / Registry 构建失败 = 环境 500 / 清单空 = 404 哨兵 / 下游透传）；reactloop 的 Cancelled（ctx）由 walk 层既有 5min 归一捕获，不特判。
- **Alternatives**: 空清单降级纯推理（绑定即意图——静默降级让作者误以为工具生效，弃，与 callTool fail-fast 先例一致）。

## R4：executions 与 nodeIn 观测面（Clarifications 落地）

- **Decision**: OnRound 闭包调 executor 新增 `recordReactExecution`（与 recordExecution 共享 Execution 行 builder：Input = `{prompt, system_prompt?, round, tool_calls}`、Output = 该轮 content、ConversationID=nil、ModelID/ModelName 同 llm 节点）；OnToolCall/OnToolResult 闭包累积 `tool_chain []{round,id,name,args,is_error}`（args = 渲染前原 Arguments 文本？→ **渲染后不存在——react 的工具参数由模型产出非模板渲染**，故记模型原 Arguments；截断 200 rune 防膨胀），收尾 `setNodeIn({prompt, system_prompt?, max_rounds, rounds, tool_calls, tool_chain})`。
- **Rationale**: llm 节点 Input 形状零变化（recordExecution 不动）；react 轮次维度进 Input 是排障必需（哪轮调了什么）；tool_chain 补 workflow 无 messages 表的调用链缺口（Clarifications Q1）。
- **Alternatives**: tool_chain 入 executions（每轮行已记 tool_calls 数，链明细重复落库膨胀，弃）。

## R5：retrieve query 加法修订（O1 加法）

- **Decision**: `query := cfg.Query`；非空 → `c.render` strict（缺失变量图缺陷 400 带定位）；空/缺省 → `c.vars["input"]`；`setNodeIn({"query": 渲染后值, ...})`（现状 nodeIn 记 query，语义从「恒 input」变「渲染后实际值」——排障增强、无契约破坏）。
- **Rationale**: 空回退保 O1 存量零回归；渲染后值入 nodeIn 让「检索了什么」可读。
- **Alternatives**: query 非空但渲染为空串 → 视同未配回退 input（spec Edge Case 已钉）。

## R6：R10 新分支与存量 gap 边界

- **Decision**: nodeTemplateRefs 增 `NodeReact`（refs(prompt) + refs(system_prompt)）与 `NodeKnowledgeRetrieval`（refs(query)，空串不扫）；**tool args 仍不入 R10**（存量 gap：既有已存图 args 带非祖先引用保存期通过、执行期 strict 拒——加入 R10 会让存量坏图无法再保存，回归风险 > 收益；deferred 留痕）。llm system_prompt 同理不动。
- **Rationale**: 新键 / 新节点无存量包袱可从严；存量键从严 = 保存期行为变更（软门禁 3 类风险）。
- **Alternatives**: 一并修存量 gap（保存期锁死存量坏图编辑路径，弃）。

## R7：precheckRefs react 分支

- **Decision**: `NodeReact` 与 `NodeLLM` 同分支预检 model_id（provider api ResolveLLMConfig 存在性链）；server_ids/tool_ids 不预检（执行期 fail-fast，同 tool 先例——server 停用 / 工具删除是运行期常态面，保存期预检会给作者「保存时好的」错觉）。
- **Rationale**: 与既有 precheck 哲学一致（model/KB 预检因删除即硬坏；tool 存在性推迟执行器是 db_model 决策 #9 修订先例）。

## R8：迁移 00022 形态

- **Decision**: `ALTER TABLE workflow_nodes DROP CONSTRAINT workflow_nodes_type_check; ADD CONSTRAINT ... CHECK (type IN ('llm','tool','condition','knowledge_retrieval','api','end','workflow','react'));` + `COMMENT ON COLUMN workflow_nodes.config` 补 `react={model_id,prompt,system_prompt?,temperature?,server_ids?,tool_ids?,max_rounds?}` 与 `knowledge_retrieval={knowledge_base_id,top_k?,query?}`；goose Up/Down（Down 重建回七值）。
- **Rationale**: 00017/00020 同名重建模式（PG CHECK 不可加值）；COMMENT 同步是 db_model 注释纪律。
- **Alternatives**: 新约束名（历史先例均同名重建，grep 面一致，弃）。

## R9：前端三分支表单与数据源

- **Decision**: react 表单 = 模型下拉（复用 modelGroups）+ server 多选（`getMcpServerList` enabled 过滤，value=id 字符串→提交转数字数组）+ 工具分组多选（agent loadToolOptions 先例：server 列表 → 逐详情 tools 分组；**与 server 多选并集语义在 UI 上并存不互斥**——勾了 server 又勾其下工具 = 去重无副作用）+ SystemPrompt/Prompt TemplateField + 温度 + max_rounds（el-input-number 0-16，placeholder「0 = 默认 8」）；kr 表单 = KB 下拉（getKnowledgeBaseList enabled 过滤）+ top_k（0-20）+ query TemplateField；tool 表单 = 工具分组**单选**（value=tool_id 字符串保形写 config.tool_id）+ args KV 行（headers 先例：空键行不产出、全空删键）。
- **Rationale**: 全部复用既有组件与数据源模式，零新组件；并集去重在后端解析层（R2），UI 不做互斥限制（避免假约束）。
- **Alternatives**: server 与 tool 二选一互斥 UI（与并集语义矛盾，弃）。

## R10：palette 扩类与着色（token 只增不改约束内）

- **Decision**: PALETTE 五扩八（+react / knowledge_retrieval / tool，顺序 llm / react / tool / knowledge_retrieval / condition / api / workflow / end 按语义分组）；着色复用既有六色族同族深浅配对——react = primary 深档（llm 浅档既有）、tool = accent 深档（api 浅档既有）、knowledge_retrieval = info 深档（workflow 浅档既有）；palette 色点同族深档；具体色阶值实施时按 tokens.css 实有档取（-600/-700 border + -100 bg 优先，缺档回退 -border/-soft 组合）。
- **Rationale**: 同族深浅 = 语义亲和（react 是 llm 的自主增强、tool 是 api 类出站、kr 与 workflow 同属引用外部资产）；零新增 token（宪法技术约束）。
- **Alternatives**: 新增色族 token（宪法「只增不改」允许增——但三节点不值得三新族，视觉噪音，弃）。
