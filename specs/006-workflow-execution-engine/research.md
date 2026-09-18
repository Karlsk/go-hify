# Research: Workflow 执行引擎（Phase 0）

**输入**: impl_spec_06 冻结契约 + 模式对齐扫描（Explore，2026-09-18）+ clarify 拍板（executions 自记）。
本篇无 NEEDS CLARIFICATION 残留——所有决策已在契约冻结轮与 clarify 消化；此处记录**落位级决策**与代码证据（file:line 为扫描时快照，实现期以实际为准）。

## R1 llm 节点调用链（全部既有组件，零新建下游）

- **Decision**: callLLM = `models.ResolveLLMConfig(ctx, model_id)`（providerapi.ModelService，api.go:44-48）→ `llmManager.Client(cfg.ProviderName, llm.UpstreamOptions{Kind, BaseURL, APIKey, Model})` → `client.Generate(ctx, []*schema.Message, opts)` 非流式。prompt 先经 `ec.render`，temperature 进 `llm.CallOptions{Temperature: *float32}`（opts nil 合法）。
- **Rationale**: Generate 即为 workflow 非流式路径预留（client.go:174 注释明示；usage 在 ResponseMeta）；chat 侧同链先例 turn.go:214-227。明文 APIKey 只在该调用链存在（crypto 四不）。
- **Alternatives**: Stream（留给未来 per-node 回调，本篇不用）；绕过 Manager 直连 eino（违反 III 单点，排除）。

## R2 executions 自记（2026-09-18 clarify 拍板）

- **Decision**: callLLM 内经窄接口写入缝（`CreateExecution(ctx, *Execution)` 单方法小接口，service 包内声明）写一行；`ConversationID = nil` 即 workflow 节点调用语义（logging/model.go:12 预留）。组合根注入既有 `logging.NewExecutionStore(db)`（server.go:163 已建实例，直接复用）。
- **Rationale**: 代码现状 platform/llm 不落库、executions 由调用方服务层写（chat recordExecution 先例 turn.go:478-500）；窄接口 = Go 小接口惯例，stub 友好。impl spec 06 §4.2 注记已按拍板更正（五处）。
- **Alternatives**: 下沉 platform/llm（触碰 chat + platform，重复记，爆炸半径大，用户否决）；不记（违反 O7 排障链冻结语义，排除）。

## R3 SSRF 防护实现（标准库，O6 冻结语义）

- **Decision**: `http.Client` + 定制 `net.Dialer.Control`（连接建立时校验 IP，防 DNS rebinding TOCTOU）；`CheckRedirect` 不放行跨协议且每跳建连自然复验 Control；仅允许 http/https scheme。拦截名单：loopback（127/8、::1）、link-local（169.254/16、fe80::/10）、IPv6 ULA（fc00::/7）恒禁；RFC1918 默认放行；`WORKFLOW_API_BLOCK_PRIVATE=true` 时私网全禁（config envBool 先例 config.go:110）。拦截 → `node <key>:` 包装 + 环境限制类 500。
- **Rationale**: O6 拍板场景优先；Dify 教训是只在解析时校验有 TOCTOU 窗口——Control 在建连时拿到真实 IP。零新增依赖。
- **Alternatives**: 解析时校验（TOCTOU，否决）；默认全禁 + 白名单（主场景不可用，否决）；完全依赖出网层（单机 compose 无此层，否决）。

## R4 超时与取消

- **Decision**: Execute 入口 `context.WithTimeoutCause(ctx, 5*time.Minute, ErrWorkflowTimeout)`（服务内私有 cause 哨兵）；游走循环每步查 `ctx.Err()`/`context.Cause`，超时/取消 → 环境限制类 `ErrWorkflowExecutionFailed`（cause 区分进日志 message）；单节点 LLM 三层超时归 platform/llm 既有、api 节点归 timeout_sec。
- **Rationale**: WithTimeoutCause + context.Cause 是仓内既定模式（llm/client.go:80/:183 先例）；5min 与 nginx console 读超时 300s 对齐（O5 拍板）。
- **Alternatives**: 10min / 按调用方分叉（O5 已否决）。

## R5 轨迹收尾写入与降级（O7 冻结）

- **Decision**: `finishResult` 统一收尾——steps 累积转 `[]WorkflowNodeRun`（seq 从 1 递增）+ run 行；`store.CreateRun` 一事务两批（run 1 行 INSERT + node_runs N 行多 VALUES INSERT）。失败路径也走收尾（失败节点入 node_runs，status=failed，error_msg 带 node 前缀原文）。CreateRun 失败重试一次，仍败 → 结果照返、RunID 置空 `""`、slog ERROR 带 trace_id。
- **Rationale**: append-only 收尾统一写（决策 #14，无 RUNNING 态写放大）；降级不阻断（O7 ④：LLM 成本已发生，结果价值高于一行轨迹）。
- **Alternatives**: 每节点前后 UPDATE status（写放大 + 僵尸 RUNNING，否决）；失败不落轨迹（违反排障唯一线索，否决）。

## R6 截断（O7 冻结）

- **Decision**: 单值 16KB（runs.output / node_runs.input / node_runs.output）——helper `truncate(s) (string, bool)`；落库值为 jsonb 文本，截断时在外层对象加 `"truncated": true` 键（input/output 本身是 jsonb，包一层 `{"value": "...", "truncated": true}` 形态，回放端可判）。slog 节点轨迹截 1KB。
- **Rationale**: db_model §12 设计注记冻结值；truncated 标记让回放分清「本来就这么短」。jsonb 包裹形态是 §12「jsonb 内 truncated: true 标记」的直接实现。
- **Alternatives**: 不截断（TOAST 膨胀 + 排障噪音）；截断不留标记（回放歧义，均否决）。

## R7 R10 保存期模板引用校验（O2 拍板）

- **Decision**: validateGraph 增条 11——对每节点按 config 类型提取模板字段（llm.prompt / api.url+headers 值+body / end.output / condition.expression），`{{name}}` 引用名 ∈ {input} ∪ 该节点祖先 key 集（沿 edges 反向 BFS；R7 无环 ⇒ 可算）；condition 比较式右侧 `'literal'` 是字面量不查。违例 → VALIDATION_FAILED 400，details 带节点 key 与引用名。
- **Rationale**: 整图提交无「先引用后建节点」窗口，保存期可静态算全；错字保存时挡（n8n 静默 undefined 教训）。引用名提取器与运行期 render 共用 `{{...}}` 扫描逻辑（一个 tokenizer 两处消费）。
- **Alternatives**: 只靠运行期 strict 兜底（排障晚一拍，O2 已否决）。

## R8 保留期清理任务（O7 拍板）

- **Decision**: `runscleaner.go`——`Start(appCtx, store, retentionDays)`：启动立即首轮 + ticker 周期（对齐 PartitionMaintainer 形态 partition.go:41-77）；批量 `DELETE FROM workflow_runs WHERE created_at < now()-retention`（node_runs 随 FK CASCADE 连带删）；retention ≤0 → 不启动 + WARN。组合根 `go cleaner.Start(appCtx)`。
- **Rationale**: 非分区表按 created_at 批删即可（增长远慢于 executions）；appCtx 来自 signal.NotifyContext（server.go:91），优雅关停天然生效。DELETE 带 WHERE、LIMIT 批次化防长事务。
- **Alternatives**: 复用 executions PartitionMaintainer（表不分区，形态不符）；cron 容器方案（dev 模式失效教训，db_model 已记录）。

## R9 trial 与把关（O3 冻结）

- **Decision**: handler `c.Query("trial") == "true"` → req.Trial（进程内调用方直传字段）；service 把关：`status == published || req.Trial` 否则 `ErrWorkflowNotPublished`（文案区分 draft/disabled 两态——哨兵 message 单一，区分靠 details/日志；既有 failWorkflow 已映射 503 handler.go:135-148）。trial 照常落 runs（is_trial=true）与 executions。
- **Rationale**: O3 拍板 query 参数形态；handler 薄绑定 query 解析归 handler（非 CRUD 动作绑定先例：BindQuery list handler.go:61-72）。
- **Alternatives**: 独立端点 / 不做（O3 均已否决）。

## R10 快照加载与路由

- **Decision**: Get 走既有缓存（service.Get 返回 Detail 含图）或三查组装——实现取三查直读（execute 要的是 nodes/edges 原始行 + ParseNodeConfig 强类型化，与 Detail schema 形态不同，直接 ListNodes/ListEdges 复用 store 既有方法更省一层转换）；nodeMap（key→parsed config）+ outEdges（source→[]edge 声明序）；`route(edges, out)`：condition 节点按声明顺序首条 `condition` 匹配；非 condition 唯一出边；无出边终止。
- **Rationale**: db_model §9 快照契约；ParseNodeConfig 保存/加载共用唯一入口（库里图必然可解析，解析失败属数据损坏 → 500 兜底）。
- **Alternatives**: 读 Detail 缓存（省两查但多一轮 schema→model 逆向转换，不值）。

## R11 NodeRunSummary 形状（api_contract §5 注释的落位）

- **Decision**: `NodeRunSummary{NodeKey, NodeType, Status string; DurationMs int; ErrorMsg string}`——即注释「key/type/status/耗时」+ failed 时的 error_msg（node_trace 供执行测试页失败定位，api_contract §9）；seq 隐含在数组顺序中，明细查轨迹表。
- **Rationale**: §5 注释枚举 + §9 前端消费点（失败定位）最小并集；数组顺序即 seq，不重复暴露。
- **Alternatives**: 全字段镜像 node_runs（结果体膨胀，明细有表查，否决）。

## R12 handler 绑定形态

- **Decision**: execute 绑定函数：BindUri（id）+ BindJSON（body：input）+ `c.Query("trial")`；一个绑定函数只调一个接口方法（Execute）。错误映射：NotPublished → 503（既有）、ErrWorkflowExecutionFailed → 500、下游哨兵走 FailFromSentinel 既有表。
- **Rationale**: 仓内 bind.go 三件套（BindJSON/BindQuery/BindUri :40-79）+ handler 既有 failWorkflow 分发模式；ExecuteWorkflowReq.ID 由 handler 填。
- **Alternatives**: trial 进 body（冻结契约是 query，排除）。
