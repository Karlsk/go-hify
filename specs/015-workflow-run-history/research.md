# Research: workflow 运行历史与节点轨迹查询

**Date**: 2026-09-24 | **Spec**: [spec.md](./spec.md) | **Plan**: [plan.md](./plan.md)

本篇为纯只读查询加法，无 NEEDS CLARIFICATION 项（clarify 零问题通过）。以下为 plan 阶段核实代码先例后落定的设计决策，全部基于仓库现状（chat 模块游标全链路 + workflow 模块四层现状 + model 全字段），零外部调研。

## D1: api 层不 import platform/page——游标在 api 契约里只是 string

**Decision**: `ListRunsReq` 的游标相关字段只有 `Limit int \`form:"limit"\`` 与 `Cursor string \`form:"cursor"\``；分页归一（page.NewCursor）、游标解码（page.DecodeCursor）全部发生在 service 层。api 层的响应类型为模块自定义 Result 结构（`RunListResult{Items []RunSummarySchema; Limit int; HasMore bool; NextCursor string}`），不含任何 platform/page 类型。

**Rationale**: 逐字对齐 chat 先例（`internal/chat/api/schema.go` 的 `ListConversationsReq` 注释明示「limit/page 约束与游标解码在 service 层（api 包不 import 带 gorm 的 platform/page）」）。api 是纯契约叶子包，只 import 标准库与纯标准库 platform 小包；platform/page 依赖 gorm，进 api 即破坏该纪律。

**Alternatives considered**: ① Req 直接持有 `page.CursorParams`——违反 api 叶子包纪律，否决；② handler 层做游标解码——绑定与业务分离破坏（chat 先例在 service），否决。

**修正记录**: spec-dev 组装的 plan 参数原文写「游标复用 platform/page（chat 先例：ListConversationsByCursor 的 api 层引用形态，plan 时核实其 import 合规性）」——核实结论为 api 层**无** page 引用形态，本决策即为修正后落点。

## D2: 列表排序键 = (created_at DESC, id DESC) 双键 keyset；「调用时间」展示 started_at

**Decision**: 列表「最新在前」按 `created_at DESC, id DESC`；游标键为 service 私有 `runCursorKey{CreatedAt time.Time; ID uint64}`（对齐 chat `convCursorKey`）。列表行的「调用时间」列展示 `started_at`（执行起点），详情两时间（started_at / created_at）均透出。

**Rationale**: workflow_runs 是 append-only（db.BaseAppendOnly，id + created_at），created_at = 收尾写入时刻、started_at = 执行起点（spec Assumption 已裁定该语义）。既有索引 `idx_workflow_runs_wf_created (workflow_id, created_at DESC)` 恰好覆盖 `WHERE workflow_id = ? AND (created_at, id) < (?, ?) ORDER BY created_at DESC, id DESC` 的行值比较 keyset（chat store 同款形态）；id 做决胜列保证游标稳定唯一（constitution V「游标必须稳定唯一」）。用 started_at 排序则无对应索引且语义上不如落库序稳定（并发执行时 started_at 可交错）。

**Alternatives considered**: ① started_at 排序——无索引、并发交错下分页可重可漏，否决；② 单键 id DESC——created_at 同秒多行时索引利用差于双键行值比较，且与 chat 先例不一致，否决。

## D3: GetRunByID 查询带 workflow_id 条件——不存在与跨工作流同判 404

**Decision**: store 的 `GetRunByID(ctx, workflowID, runID)` 查询条件为 `WHERE workflow_id = ? AND id = ?`；run 不存在、或存在但不属于该工作流，同判 `gorm.ErrRecordNotFound` → service 翻译 `ErrRunNotFound`（RUN_NOT_FOUND 404）。

**Rationale**: spec FR-004 与 Edge Cases 明文「不泄露其他工作流的存在性」——两种情形返回同一错误码同一状态码，响应面不可区分。workflow_id 弱引用无 FK（spec 07 决策），条件过滤在应用层做天然合法。对齐既有 GetRunByID-类语义（chat 的 ErrConversationNotFound 同款翻译链）。

**Alternatives considered**: ① 先按 runID 查、再比对 workflow_id 不匹配返回不同码——泄露存在性，违反 FR-004，否决；② 只按 runID 查忽略 workflow_id——越权读取其他工作流运行数据，否决。

## D4: 列表摘要列清单不含 input/output；详情全列

**Decision**: store 新增显式列常量两组：`selectRunSummary`（id, workflow_id, status, trigger_source, is_trial, duration_ms, error_node, error_msg, started_at, created_at——**不含** input/output）用于列表；`selectRun`（含 input/output 在内的 model 全字段）用于详情；`selectNodeRun`（seq, node_key, node_type, status, input, output, duration_ms）用于轨迹。

**Rationale**: FR-002「列表 MUST NOT 携带大文本」+ SC-003「列表响应轻量」——最稳实现是 SQL 层不取该列（TOAST 不解压零成本），而非取了再丢。error_msg 是短文本（错误摘要）保留在摘要面（US1「失败节点与错误摘要可直接读到」）。列常量纪律对齐 store.go 既有 selectWorkflow/selectNode/selectEdge 形态（注释「列集 = model 字段集，加列须两处同步」同款）。

**Alternatives considered**: ① 列表查全列再置空——白付 16KB×20 行的 IO 与解压，否决；② 摘要面砍掉 created_at——翻页 keyset 需要它做行值比较，否决。

## D5: 节点轨迹按 (run_id, seq ASC) 查询；空轨迹为合法态

**Decision**: `ListNodeRuns(ctx, runID)` 为 `WHERE run_id = ? ORDER BY seq ASC`，走既有 idx_workflow_runs_nodes (run_id) + uq (run_id, seq)。「轨迹为空的运行」返回空 slice（service 层 make 兜底 → JSON `[]`），详情正常组装，前端轨迹区展示空态文案。

**Rationale**: seq 是执行序（spec 07 决策，uq(run_id,seq) 兜底唯一）；FR-003「按执行序」。Edge Case「节点轨迹为空的运行（防御路径）」——落库失败降级路径下可能只有 run 行没有 node 行，查询面不当作错误。node 级 error_msg 落库恒空（既有形态，spec Assumption 裁定）——错误定位 = run 级 error_msg + error_node 前端高亮，NodeRunSchema 仍带该字段位（透出空值，契约面完整）。

**Alternatives considered**: ① 轨迹并入 GetRunByID 一条 JOIN 查询——违反「JOIN ≤ 3 且每 JOIN ON 列有索引」的保守面且两表生命周期独立，分次查询 + service 组装对齐「跨模块数据在 service 层分次查询后组装」精神，否决；② 轨迹为空返回 404——把合法态当错误，违反 Edge Case，否决。

## D6: 前端形态——独立子组件 WorkflowRunsPanel.vue + 运行详情用 el-drawer

**Decision**: 「运行历史」区块抽独立子组件 `web/src/views/workflow/WorkflowRunsPanel.vue`（props 收 workflowId；内部自治：列表加载/翻页/详情打开全自持）；WorkflowDetail.vue 仅在卡片流末尾追加一个挂载点。运行详情视图用 `el-drawer`（Element Plus 既有组件）承载：run 级信息 + input/output 大文本（pre-wrap 限高滚动）+ 节点轨迹表（el-table：节点/类型/状态/耗时/输入/输出，失败节点行按 error_node 匹配高亮）。

**Rationale**: spec Assumption「具体组件形态在规划阶段定，不改既有区块结构」+ FR-006「详情页既有区块零变化」。三个依据：① WorkflowDetail.vue 已承载基础信息/Schema/图编排/试运行多区块（spec 010/013 迭代产物），运行历史自成一体（列表状态机 + 抽屉 + 轨迹表约 300 行）塞进去会让单文件失控，独立组件对齐 spec 013 WorkflowTrialDialog.vue 先例与「多个小文件」规则；② 抽屉形态给大文本与宽轨迹表足够的横向空间，不挤压卡片流（FR-006 的结构性保证——挂载点之外零触碰）；③ 关闭抽屉即回列表原位，符合「点开→核对→返回」的排障动线。

**Alternatives considered**: ① 内联展开区（el-collapse）——大文本与六列表格在半宽卡片里塞不下，且展开状态管理纠缠列表滚动位置，否决；② 详情抽成独立路由页——离开详情页上下文、需处理返回态，交互成本高于抽屉，否决。

## D7: limit 归一与篡改 cursor 的错误翻译链

**Decision**: handler `BindQuery` 绑定 `ListRunsReq` 后原样透传 service；service 首行 `page.NewCursor(req.Limit, req.Cursor)` 归一（≤0→20、>100→100——不报错），随后 `page.DecodeCursor[runCursorKey]` 失败时 `fmt.Errorf("%w: cursor: %v", errs.ErrValidationFailed, err)` → handler 经 respond.FailFromSentinel 映射 400 VALIDATION_FAILED。

**Rationale**: 逐字对齐 chat `ListConversations` 的实现链（Edge Case「limit 传 0/负/超上限→归一不报错」与「篡改 cursor→400」两行为的既有成熟解法）。归一在 NewCursor 内完成，Edge Case「非数字」由 gin form 绑定 400 兜底（BindQuery 失败即 400，同 chat）。

**Alternatives considered**: ① handler 里做归一——分页知识泄漏到 HTTP 层，与 chat 先例不一致，否决；② 非法 cursor 静默回首页——掩盖异常输入、游标不透明契约变含糊，违反 Edge Case 明文 400，否决。

## 汇总

| # | 决策 | 对齐 |
|---|---|---|
| D1 | api 层游标只是 string，page 工具全在 service | chat api 先例 + api 叶子包纪律 |
| D2 | (created_at DESC, id DESC) keyset；展示 started_at | idx_workflow_runs_wf_created + constitution V |
| D3 | GetRunByID 带 workflow_id 条件，不存在/跨工作流同判 404 | FR-004 不泄露存在性 |
| D4 | selectRunSummary 无大文本 / selectRun 全列 / selectNodeRun | 显式列纪律 + FR-002 |
| D5 | 轨迹 (run_id, seq ASC)；空轨迹合法态 `[]` | FR-003 + Edge Case |
| D6 | WorkflowRunsPanel.vue 独立组件 + el-drawer 详情 | FR-005/006 + spec 013 先例 |
| D7 | NewCursor 归一 + DecodeCursor 失败→ErrValidationFailed 400 | chat service 先例 + Edge Cases |
