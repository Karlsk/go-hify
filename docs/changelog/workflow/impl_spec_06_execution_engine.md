# Workflow 实现 spec 06：执行引擎（Execution Engine）

> 状态：**契约已冻结（2026-09-18）**。第一轮锁背景 / 做什么 / 不做什么 / 总体架构（§1-§4 主管线）；第二轮落盘实现细节与数据模型：线程模型与代码结构（§4.1-§4.2）、关键取舍（§4.3-§4.5）、轨迹表 DDL（[db_model.md](./db_model.md) §12 + 决策 #14）、execute 契约（[api_contract.md](./api_contract.md) §5）；第三轮逐项拍板 O1-O7（§5 表）；同日冻结收尾——待定字样清零、R10 进 db_model §7 图校验清单（条 11）与决策 #15、错误码 `WORKFLOW_EXECUTION_FAILED` 进 api_contract §8 主表、验收门补齐（§7）。本篇即 rdp-implementation 的消费契约；行级实现细节可在实现期微调，语义以本篇为准。
> 上位契约：[CLAUDE.md](../../../../CLAUDE.md)《代码组织规范》依赖清单（workflow → mcp/rag/provider/agent/platform，**不得依赖 chat**）、《外部 LLM 调用设计》（platform/llm 唯一接入层）；[db_model.md](./db_model.md) §8 引擎消费形态、§9 执行契约（本篇为该两节的展开）；[impl_spec_05_agent_binding.md](./impl_spec_05_agent_binding.md) §1.1 E1-E3（递延到本篇的决策）。冲突时停下来问用户。
> 前置依赖：spec 01-05 全部合入（CRUD + 图校验 + 发布状态机 + agent 绑定，commit `c06a79b`）。

## 1. 背景

workflow 模块现状是**纯配置管理**：7 个 CRUD 端点 + 图校验 R1-R9 + draft/published/disabled
状态机 + agent 可空外键绑定（`agents.workflow_id`），`WorkflowService` 接口明示"本期不含
Execute"。配置本身不做任何事——存的是三张表（workflows / workflow_nodes / workflow_edges）
里的静态图数据。**执行引擎是把这些数据变成运行时行为的组件**：加载图 → 从入口逐节点执行
（llm 真调 LLM、api 真发 HTTP、knowledge_retrieval 真做向量召回）→ 节点输出进变量池供下游
模板引用 → 走到终点返回终稿。

已有地基（本期全部复用，零重复建设）：

| 组件 | 现状 | 执行引擎的用法 |
|---|---|---|
| 图 IO | store `GetByID` + `ListNodes` + `ListEdges`（spec 03） | 快照加载：执行开始一次性两表读进内存 |
| config 强类型解析 | `api.ParseNodeConfig` 六类密封分发（spec 02） | 加载路径与保存路径共用唯一入口，库里图必然可解析 |
| 哨兵错误 | `ErrWorkflowNotPublished` 等 4 个已在 | 发布态把关的错误码零新增（CLAUDE.md 错误码表已登记） |
| llm 节点下游 | `platform/llm` Manager（bulkhead/熔断/三层超时/重试），组合根已装配 | `callLLM` 直调；executions 落库由**执行器自记**（见 §4.2 注记，2026-09-18 拍板更正） |
| knowledge_retrieval 节点下游 | `ragSvc.Retrieve`（pgvector 召回）现成 | `retrieve` 直调 |
| 下游注入 | workflow service 已持有 modelSvc / ragSvc（保存期引用预检用） | 执行期复用同一组下游 api |
| executions 落库 | `logging.NewExecutionStore` | LLM 节点轨迹的既有持久化，执行器不另建 |

**尚未存在的组件**（本期新建）：`Execute` 接口与 handler 路由、执行器游走循环、vars 变量池
与 `{{var}}` 模板渲染（全仓无模板引擎代码，llm prompt / api 的 url+body / end 的 output 三处
消费）、condition 表达式求值器、api 节点出站 HTTP client（SSRF 防护 + timeout/TLS 映射）、
工作流级总时长上限、节点级 slog 轨迹、运行轨迹持久化两层表（`workflow_runs` +
`workflow_node_runs`，2026-09-17 拍板要建——DDL 已冻结于 [db_model.md](./db_model.md) §12
与决策 #14，另见 §2 第 7 项与 §5 O7）。

## 2. 做什么（范围）

1. **Execute 契约与路由**：`WorkflowService` 扩 `Execute(ctx, req) (*RunResultSchema, error)`；
   `POST /api/v1/workflows/{id}/execute`——正式运行仅 published（draft/disabled → 503
   `WORKFLOW_NOT_PUBLISHED`），`?trial=true` 试运行放开 draft/disabled（O3 拍板，状态机
   唯一例外；db_model §10 既有规划路由）。req / ResultSchema 形状已冻结（api_contract §5；O1 单一 input）。
2. **执行器本体**（落位 workflow service 层，遵守模块内四层边界）：快照加载 → 从
   `start_node_key` 起的游走循环 → 六类节点 type switch 分发 → 终止（无出边 = 隐式结束，
   该节点输出即终稿；显式 `end` 节点按 `output` 模板拼终稿）。架构见 §4。
3. **ExecutionContext（vars 池）与模板渲染**：flat string 池、strict 缺失报错、condition
   迷你表达式与模板共用求值底层、R10 保存期引用校验（均已拍板，见 §5 O2）。
4. **api 节点出站 HTTP client**：SSRF 防护（O6 拍板：恒禁 loopback / link-local / ULA，
   RFC1918 默认放行，Dialer.Control 连接时校验防 rebinding，env 一键收紧）、`timeout_sec`
   1-60 映射（默认 10s）、`ssl_verify` 映射 TLS 配置（config 字段已冻结于 schema，执行语义
   归本篇）。
5. **可观测**：节点级 slog 轨迹（node key / type / 耗时 / 输出摘要，**大段 prompt 与 PII
   截断后才入日志**）；错误一律 `fmt.Errorf("node %s: %w", key, err)` 包装——哪个节点炸一眼
   可见；trace_id 从 ctx 自动贯穿。LLM 节点明细沿用 executions（执行器自记，§4.2）。
6. **错误语义**：fail-fast——condition 无命中出边 = 执行错误（记日志，不静默终止）；下游
   哨兵（provider/rag/llm 层错误码）原样上抛 + node key 包装；不重试（重试纪律归
   platform/llm，工作流层不叠加）。
7. **运行轨迹持久化（2026-09-17 拍板：确定要建，可追溯优先级最高）**：两层表
   `workflow_runs`（每次运行一行：input/output/status/耗时/触发来源）+
   `workflow_node_runs`（每节点一行：seq、node key/type、input/output、耗时、错误）——
   CLAUDE.md 护栏「运行日志是排障唯一线索」在 workflow 侧的落地。**键（主键 / FK / 索引）
   设计是本项第一要求**：定稿 DDL 与设计注记已冻结于 [db_model.md](./db_model.md) §12、
   拍板要点入其决策 #14（append-only 收尾统一写无 RUNNING 态、弱引用零跨模块 FK +
   workflow_name 快照、`UNIQUE (run_id, seq)`、`started_at`）；残留三项（写入失败取舍 /
   保留期 / 截断阈值）也已拍板（§5 O7）。
   写入时机：执行收尾统一落（不占节点执行事务，事务最小化，见 §4.5）。与既有两层观测
   并存：slog（全节点日志）+ executions（LLM 调用明细）+ runs/node_runs（工作流级
   结构化轨迹）。

## 3. 不做什么（边界）

- **chat 触发接线**——chat 判 `agent.workflow_id` 后调 `workflowapi.Execute` 的消费侧逻辑，
  连同触发形态（工具形态 vs 管道形态，spec 05 E1）与流式粒度，**后续独立 spec**；本篇只保证
  Execute 接口对进程内调用方可用（跨模块走 api 接口，复用同一 execute）。
- **mcp tool 节点执行**——mcp 模块整体未实现（service 仍是占位 stub），`callTool` 无下游可调。
  **已拍板（2026-09-17 第三轮，O4）：执行期 fail-fast**——保存含 tool 节点的图合法（图校验
  不动），执行游走到 tool 节点即报错终止（node key 包装 + 记轨迹），不动画校验与表结构；
  mcp 建成后执行器接上即通、存量图零迁移（与 db_model 决策 #9 修订"tool_id 存在性推迟
  执行器 fail-fast"同一逻辑的顺延）。错误归类图缺陷 → 400（二分法见 §5 O4）。
- **运行中状态查询 / 轮询**——一期 execute 是同步调用（见下条），runs/node_runs 记录的是
  已完成运行的结构化轨迹，不提供"运行中"实时态；未来 chat 侧 per-node 进度走同步回调
  推送（§4.3），同样不给轮询留口。
- **异步执行 / 任务队列 / 运行状态查询**——一期 execute 是同步调用（HTTP 或进程内），跑完
  即返；异步化（提交-轮询模型）等真实需求。
- **并行分支 / 合并 / 循环节点 / 子工作流 / 人工审批**——db_model §11 既有边界，R6（非
  condition 出边 ≤1）与 R7（无环）在保存期已挡住，执行器天然线性，不预留调度骨架。
  **子工作流留缝（2026-09-17 评审，O1 拍板附带记录）**：嵌套是合理的未来能力且架构对它
  天然友好——workflow → workflow 同模块调用合法、`Execute` 纯函数即子流程节点的全部
  所需、加节点类型成本低（00017 先例）。将来形态走 **string→string**：节点 config
  `{workflow_id, input_template}`，父 vars 渲染入参 → 递归 Execute → 子终稿落父
  vars[节点key]，与 llm/tool/api 节点同构，零声明体系。届时只补三件小事：跨图递归深度
  上限（A→B→A 环 R7 挡不住——静态校验是单图的，执行期 fail-fast）、runs 加
  `parent_run_id`（弱引用本表，追溯链）、input_template 渲染。声明式输入/输出（Dify 式）
  的真实触发点是 E1 工具形态的参数 schema 或"父流程取子中间输出"，到触发再纯增量。
- **通用 hook / 回调注册体系**——节点轨迹走 slog 直写，不引入监听器机制（一人维护的内部
  工具，过度设计）。
- **发布快照 / 版本化**——执行读实时图（spec 05 拍板 B2），编辑立即生效，无双版本存储。
- **前端 workflow 页面**（列表/编辑/发布/执行测试）——独立前端增量，不阻塞本篇。
- **定时触发 / 事件触发**——execute 只被控制台或 chat 调用（db_model §11）。

## 4. 引擎总体架构流程

**形态定位（本篇最重要的架构切分，2026-09-17 讨论结论）**：执行引擎是**同步纯函数**——
`ctx` 进、结果出，全程不持有任何用户连接、不感知 SSE。呈现归调用方：控制台直接 execute =
普通 respond 信封一次返回（无 SSE）；chat 触发（后续 spec）= chat 持有 SSE 连接，拿到返回值
后自行决定推送粒度。这样切的原因：依赖红线要求 workflow 不得依赖 chat；将来工作流被其他
调用方复用（定时任务、workflow 互相调用）不带流式尾巴。

```
调用方（控制台 HTTP handler ──或── chat 经 workflowapi 进程内调用〔后续 spec〕）
  │  Execute(ctx, req)
  ▼
① 入口把关：workflows.status 必须 published（trial=true 试运行放开 draft/disabled，O3），否则 503 WORKFLOW_NOT_PUBLISHED（哨兵已有）
  ▼
② 快照加载（db_model §9 契约）：
     GetByID + ListNodes + ListEdges → 内存 nodeMap（key→节点）+ 出边表（source→[]edge，
     condition 出边保持声明顺序）；每节点 config 经 ParseNodeConfig 强类型化。
     一次性加载完毕，进行中执行不受并发编辑影响（编辑立即生效是对"下一次"执行而言）。
  ▼
③ ExecutionContext 初始化：vars["input"] = req.Input（O1 终形：单一 input 字符串）
  ▼
④ 游走循环（R6/R7 保证线性且必然终止——无队列无调度，指针游走）：
     current := start_node_key
     for current 是有效节点：
       按 config 具体 type 分发：
         llm                  → callLLM：platform/llm（抢槽/熔断/三层超时/重试），prompt
                                 经模板渲染；单轮无 SSE、不带 chat 多轮上下文；executions
                                 落库执行器自记（§4.2 注记）
         tool                 → callTool：mcp 未建，执行期 fail-fast（O4 已拍板）
         condition            → evaluate：纯内存求值，零外部调用；结果匹配出边 condition
                                 （按声明顺序取首条命中）；无命中出边 = 执行错误 fail-fast
         knowledge_retrieval  → retrieve：ragapi 召回，结果注入 vars
         api                  → callApi：出站 HTTP（SSRF 拦截 + timeout/TLS 映射），全链模板渲染
         end                  → buildOutput：按 output 模板拼终稿，零外部调用
       节点输出 → vars[node_key]（O2 终形：flat string 池）；节点轨迹内存累积（slog 同步记一条，截断）
       选下一条边（condition 匹配 / 唯一无条件出边 / 无出边 = 终止）
       每步检查 ctx——调用方取消（如客户端断连）立即中止，错误带 node key
  ▼
⑤ 终止与返回：无出边（该节点输出即终稿）或 end（buildOutput）→ 执行收尾统一写
     workflow_runs（一行）+ workflow_node_runs（每节点一行，含失败节点）→ ResultSchema 返回调用方；
     轨迹写入不占节点执行事务（事务最小化）；写入失败降级不阻断（O7 ④：CreateRun 重试一次，仍败则结果照返、RunID 置空、ERROR 日志带 trace_id）
  ▼
⑥ 错误路径：任一节点失败 → fail-fast 即停，fmt.Errorf("node %s: %w") 包装上抛；
     下游哨兵（MODEL_NOT_FOUND / PROVIDER_BUSY / PROVIDER_UNAVAILABLE / RATE_LIMITED …）
     原样透传给 handler 映射状态码；LLM 层错误分类已由 platform/llm 记 executions
```

**超时层级**：单节点 LLM 调用由 platform/llm 三层超时管（TTFT/idle/overall）；api 节点由
`timeout_sec` 管（1-60s）；**工作流级总时长上限 5min**（O5 拍板，2026-09-17）——多节点
累计的兜底，`Execute` 入口 `WithTimeoutCause(ctx, 5*time.Minute, ErrWorkflowTimeout)`，
与 nginx console 读超时对齐；超时归环境限制类 → `WORKFLOW_EXECUTION_FAILED`（O4）。

### 4.1 线程模型（讨论结论：goroutine-per-request，无工作池）

每请求一 goroutine，请求结束即回收——execute 是同步调用，调用栈从 handler（或 chat 进程
内调用）直达节点执行，无队列无 worker pool。并发上限不靠线程池约束，而在两处既有闸门：
LLM 节点受 platform/llm bulkhead（每供应商 16 槽）管；api 节点受自身 `timeout_sec` 管。
取消走 ctx 链：客户端断连 → gin ctx 取消 → 游走循环每步检查 `ctx.Err()` → 中止上抛
（node key 包装）。R6/R7 保证节点数 ≤50 且无环，单 goroutine 游走无饥饿问题——工作池 /
调度器在这个量级是纯复杂度（拒绝理由同 CLAUDE.md「不用 worker pool 限制 LLM 并发」）。

### 4.2 代码结构：四部件与落位（冻结）

新建于 workflow 模块四层内，**不建子包**——节点执行器不需要独立编译单元，密封接口 +
type switch 已是扩展边界（§4.4）：

| 部件 | 落位 | 职责 |
|---|---|---|
| ExecutionContext | `service/execcontext.go` | vars 池（黑板，Dify VariablePool 模式）：入参落池、节点输出落池、`{{var}}` 求值；condition 表达式共用同一求值入口（O2） |
| 执行器 | `service/executor.go` | 持下游依赖（models / llmManager / rag…）+ `runNode` type switch 分发 + 各 callXxx 私有方法 |
| 编排入口 | `service/execute.go` | `Execute`：把关 → 快照加载 → ctx 初始化 → 游走循环 → 收尾写轨迹 → 组装 ResultSchema |
| 出站 HTTP | `service/httpx.go` | api 节点 client：SSRF 拦截、timeout / TLS 映射（O6） |

代码骨架（冻结——行级细节实现期可微调，语义以本篇为准）：

```go
// execcontext.go —— vars 黑板：入参 + 各节点输出；R7 无环 ⇒ 每 key 恰写一次，结构性 append-only
type execContext struct {
    vars  map[string]string // "input" + 各 node_key → 输出
    steps []nodeStep        // 节点轨迹内存累积（收尾统一落库，§4.5）
}
func (c *execContext) render(tpl string) (string, error) // {{var}} 求值；strict——缺失变量即错误（O2 终形；保存期 R10 先挡，此处运行期兜底）
func (c *execContext) set(key, val string)               // 节点输出落池

// executor.go —— 六类节点分发：api 层密封接口 + type switch（db_model §8 既有形态）
func (e *executor) runNode(ctx context.Context, key string, cfg api.NodeConfig, c *execContext) (string, error) {
    switch cfg := cfg.(type) {
    case api.LLMConfig:                return e.callLLM(ctx, cfg, c)
    case api.ConditionConfig:          return e.evaluate(cfg, c)
    case api.KnowledgeRetrievalConfig: return e.retrieve(ctx, cfg, c)
    case api.ApiCallConfig:            return e.callAPI(ctx, cfg, c)
    case api.EndConfig:                return e.buildOutput(cfg, c)
    case api.ToolConfig:               return "", fmt.Errorf("node %s: tool node unsupported until mcp module lands", key) // O4 拍板：mcp 落地前 fail-fast（图缺陷类 → 400）
    default:                           return "", fmt.Errorf("node %s: unhandled config %T", key, cfg)
    }
}

// execute.go —— 游走循环（R6/R7 ⇒ 线性且必然终止，指针游走无调度）
func (s *workflowService) Execute(ctx context.Context, req workflowapi.ExecuteWorkflowReq) (*workflowapi.RunResultSchema, error) {
    started := time.Now()
    // ① 把关（published）+ ② 快照加载：Get 走缓存，ListNodes/ListEdges 直查 → nodeMap + outEdges
    // ③ execContext 初始化：vars["input"] = req.Input ……
    current := wf.StartNodeKey
    for current != "" {
        if err := ctx.Err(); err != nil {
            return nil, s.finishFailed(ctx, run, current, fmt.Errorf("node %s: %w", current, err))
        }
        out, err := s.exec.runNode(ctx, current, nodeMap[current], ec)
        // 轨迹内存累积 + slog 同步一条（截断）……
        if err != nil {
            return s.finishResult(ctx, run, ec, started, current, err) // 收尾统一写 runs + node_runs（含失败节点）
        }
        ec.set(current, out) // R7 ⇒ 每 key 恰写一次（仅成功节点落池）
        next, ok := route(outEdges[current], out) // condition 首条命中 / 唯一无条件边 / 无出边终止
        if !ok {
            break
        }
        current = next
    }
    return s.finishResult(ctx, run, ec, started, "", nil) // 终稿 = end.output 渲染或末节点输出
}
```

callLLM 链（llm 节点，全部既有组件）：`models.ResolveLLMConfig(model_id)` →
`llmManager.Client(providerName, opts)` → `client.Generate(ctx, msgs, opts)`（非流式，
client.go 既有；`Stream` 留给未来 per-node 流式回调）→ prompt 先经 `ec.render`，
temperature 透传；executions 落库**执行器自记**——callLLM 内经 executions 写入缝（窄接口，
组合根注入 platform/logging 既有实例）写一行，`ConversationID` 置空 = workflow 节点调用
（logging/model.go 既有预留语义；先例：chat service 层 recordExecution）。
〔2026-09-18 用户拍板更正：本注记原写「executions 落库由 platform/llm 自动完成，执行器不另记」，
经代码现状核对系事实性错误——platform/llm 只管调用不落库，executions 由调用方服务层写入；更正不改变
任何行为语义（workflow LLM 调用进 executions 的要求不变），仅修正责任层表述。〕
〔2026-09-22 追加（spec 011，用户批准·加法修订）：`LLMConfig` 增可选 `system_prompt`（`omitempty`）。
非空时**先于 `prompt`** strict 渲染（fail-fast 语义同 `prompt`：缺失变量报 `errs.ErrValidationFailed`、
发生于 ResolveLLMConfig 之前，零上游调用零落库），消息序组装为 `[system, user]`；空串 / 缺省保持单
user 消息现状（存量路径逐字节不变）。记录形态：`node_in` 摘要与 executions 行 `Input` 在有值时多记
一个 `system_prompt` 键（渲染后文本），无值不引入新键。〕

配套增注：`api/schema.go` 增 `ExecuteWorkflowReq` / `RunResultSchema`（已冻结，见
api_contract.md §5）；`store.go` 增 `CreateRun`（一事务 run 1 行 +
node_runs N 行多 VALUES，db_model §12）；组合根 `workflowsvc.New(...)` 追加 llmManager
（platform/llm）注入。

### 4.3 节点进度：回调推送，不轮询（讨论结论）

未来 chat 的 per-node 流式（用户看到"正在查询订单…"逐节点推进）走**同步回调**：引擎留
`onNodeDone func(nodeKey, output string)` 注入点（nil = 无回调，控制台路径零开销），chat
在 execute 调用栈内收到回调即推 SSE 帧。否决轮询：chat 本来就阻塞在 execute 调用上，
轮询 get-run + status 等于拿 DB 当消息队列，还得为它造 RUNNING 可变态与写放大
（db_model 决策 #14 已否决）。回调接缝的具体形态（SSE 事件类型、节流）归 chat 触发
spec（spec 05 E1）。

### 4.4 type switch 而非 Registry（讨论结论，2026-09-17）

评审中提出 Java 式 Registry（每节点一个 Executor 类 + 注册表 + adapter），维持 db_model §8
的密封接口 + type switch，理由：

- **加节点类型的真实成本不在分发**：00017 加宽先例——加 `api` / `end` 改了 CHECK 迁移、
  ParseNodeConfig、图校验规则、（将来）前端编辑器；switch 只是其中一行。Registry 把一行
  改成"新类 + 注册调用"，总改动量不降反升。
- **泛型节点才是扩展口**：真要"不动核心代码加能力"，路径是 tool（mcp 工具）与 api
  （任意 HTTP）——外部能力注册进 mcp / 配置进 api 节点，图变能力变，核心六类不动。
- **Go 无热加载**：注册表插件化换不来不停机升级——新节点类型必然带新代码，单二进制
  进程只能重启生效；"Registry 方便将来插件化"在 Go 落不了地（真插件化是独立进程 + RPC，
  另一个量级的架构，不在本期视野）。
- **诚实修正一条早前论据**：Go type switch **无编译期穷举检查**（不像 Rust enum match），
  漏 case 不会编译失败——防线是 default 臂运行时报错 + `ParseNodeConfig` 已挡未知类型
  进库（新类型在最早处暴露）+ 表驱动测试。Registry 同样只有运行时检查，安全性打平，
  分发形式只按可读性与改动面裁决。

### 4.5 轨迹写入：append-only 收尾统一写（讨论结论）

否决"每节点执行前后各写一次库（先 RUNNING 后终态）"：50 节点图 = 100+ 次写、每次两条
UPDATE，写放大且把节点执行拖进数据库往返；R6/R7 无断点续跑场景，RUNNING 态无人消费
（§4.3 已定进度走回调）。定稿形态：节点轨迹内存累积（execContext.steps）+ slog 同步记
一条（排障即时可见），执行收尾一次事务写 `workflow_runs`（1 行）+
`workflow_node_runs`（N 行，含失败节点）——失败路径也写，这是"排障唯一线索"的核心
诉求。写入失败的取舍已拍板（O7 ④，2026-09-18）：**降级不阻断**——CreateRun 先重试一次，
仍失败则结果照返、RunID 置空、ERROR 日志带 trace_id；slog 节点轨迹与 executions 在，
排障链不断。键设计与定稿 DDL 见 db_model.md §12。

## 5. 决策记录（O1-O7 全部拍板，2026-09-17/18；本篇已冻结）

| # | 事项 | 拍板结论 |
|---|---|---|
| O1 | Execute 入参与变量映射 | **✅ 已拍板（2026-09-17 第三轮）：单一 `input` 字符串**——console 测试文本 / chat 用户消息都落 `vars["input"]`，与冻结的 `{{input}}` 文档（db_model §4、api_contract §4）零冲突，零 schema 管理；`ExecuteWorkflowReq.Input` 即终形。拒绝 Dify 式声明 inputs（无画布、无结构化调用方，简版定位不符）与可选 variables map（本期无消费者）——两者的真实触发点（E1 工具形态参数 schema、嵌套结构化传参）到时纯增量，嵌套留缝见 §3。ResultSchema 终形（run_id / status / output / duration_ms / node_trace，api_contract §5 已冻结） |
| O2 | vars 池与模板语义 | **✅ 已拍板（2026-09-17 第三轮，三项全按推荐）**：① 池形态 = flat `map[string]string`，键 = `"input"` + node_key（否决 outputVariable 字段与结构化值——消费者全是字符串拼接场景）；retrieve 落池前格式化为编号段落文本、api 节点存原始 body——所有节点 string 进 string 出，与嵌套 v1 同构；② 缺失变量 = **strict 执行错误** fail-fast（`node <key>:` 包装 + 记轨迹；拒绝宽松空串——n8n 教训：错字被静默吞掉、输出莫名变差难排查），另加 **R10 保存期静态校验**：模板引用名 ∈ {input} ∪ 该节点祖先 keys（DAG 祖先可算、整图提交无先引用后建节点窗口），错字保存时即 400，随冻结契约同步进 db_model §7 图校验清单；③ condition = **迷你表达式**：`{{var}}` 或 `{{var}} == 'literal'`，求值结果恒为字符串、出边 condition 存匹配值——多值（裸 var 多路分支）与布尔（比较式 true/false 二路）一个语义全覆盖，api_contract §4 冻结示例零改动，解析器约 20 行；condition 与模板共用 `execContext.render` 底层 |
| O3 | 试运行能力（spec 05 E2） | **✅ 已拍板（2026-09-17 第三轮）：`?trial=true` query 参数**——draft/disabled 也可执行（状态机唯一例外，仅 trial 路径；正式路径 503 语义不变）。试运行照常落 runs（`is_trial = true`，db_model §12 已加列——追溯区分测试与真实流量）与 executions（LLM 成本真实发生）。拒绝独立端点（多一组路由，语义完全相同）；拒绝不做（测试半成品须先 publish——窗口期内绑定的 agent 在 chat 真触发半成品，测试动作污染生产语义；三家开发循环均为"测半成品不发布"） |
| O4 | tool 节点执行期行为 | **✅ 已拍板（2026-09-17 第三轮）：执行期 fail-fast**（§3 已更新）；拒绝保存期禁 tool 节点（回翻 #9 修订 + mcp 建成时删规则）。**附带拍板：引擎自身错误二分法**——图缺陷类（condition 无命中出边、模板缺失变量运行时兜底、tool 节点未支持）→ `errs.ErrValidationFailed` 400，message 带 `node <key>:` 前缀，作者可行动；环境限制类（api 节点 SSRF 拦截、工作流总时长超限）→ 新哨兵 `workflowapi.ErrWorkflowExecutionFailed`（码 `WORKFLOW_EXECUTION_FAILED`，500）+ trace_id 排障。下游哨兵透传不变；冻结时哨兵进 `workflow/api/errors.go`、api_contract §8 与 CLAUDE.md 错误码表 |
| O5 | 工作流级总时长上限默认值 | **✅ 已拍板（2026-09-17 第三轮）：5min 单常量**——与 nginx console 读超时 300s 对齐（超过它的 console 请求注定失败，不如引擎提前终止并留下干净 failed run + `WORKFLOW_EXECUTION_FAILED`）；单常量无调用方分叉（E1 形态未定，不预支分叉）；拒绝 10min（以 LLM overall 病理串联做设计基准不划算——正常调用秒到分钟级）与按调用方区分（两个常量 + 语义分叉）。实现：`Execute` 入口 `context.WithTimeoutCause(ctx, 5*time.Minute, ErrWorkflowTimeout)`，超时归环境限制类（O4 二分法）。合理超 5min 的图是该拆的信号，不是放宽上限的信号 |
| O6 | api 节点 SSRF 防护策略 | **✅ 已拍板（2026-09-17 第三轮）：场景优先**——恒禁 loopback（127/8、::1——hify 容器 localhost 即自身，禁了零损失）+ link-local（169.254/16，含云元数据——上云保险，零成本）+ IPv6 ULA（fc00::/7）；**RFC1918 默认放行**（api 节点主场景 = 内网自签服务，与 `ssl_verify=false` 同一哲学：内部工具、便利优先、风险已知悉；compose 内服务可达为接受的残余风险——调用者是已登录内部用户，非匿名公网流量）；**`net.Dialer.Control` 连接时校验**防 DNS rebinding（Dify 教训：只在解析时校验有 TOCTOU 窗口，校验必须发生在建连时；重定向每跳建连自然复验）；仅 http/https；env 一键收紧 `WORKFLOW_API_BLOCK_PRIVATE=true` 禁全部私网。拒绝默认全禁 + 白名单（默认状态主场景不可用，与 compose 一键起冲突）与完全不拦（云元数据/自探测零防护，上云即雷） |
| O7 | 运行轨迹表 `workflow_runs` + `workflow_node_runs` 的键设计（spec 05 E3） | **已拍板（2026-09-17）：确定要建，可追溯优先级最高，键（主键 / FK / 索引）设计是第一要求**；第二轮讨论已收敛——DDL 与设计注记已冻结于 [db_model.md](./db_model.md) §12、拍板要点入其决策 #14：弱引用零跨模块 FK（workflow_id / conversation_id / message_id，run 冗余 workflow_name 快照 + trace_id 对齐日志链）；append-only 收尾统一写，**无 RUNNING 态**（原要点 ⑤ 已决：不需要，§4.5）；`UNIQUE (run_id, seq)` 排序键（seq = 执行顺序唯一事实源）；`started_at` 起点（created_at = 收尾写入时刻）；索引按真实查询路径（workflow_id + created_at DESC 列运行历史、run_id 关联 node_runs）；steps 更名 `workflow_node_runs`；唯一 FK 是 node_runs.run_id（同模块真子表 CASCADE）。**残留三项已拍板（2026-09-18）**：④ 写入失败 = **降级不阻断**——CreateRun 先重试一次，仍失败则结果照返、`RunID` 置空、ERROR 日志带 trace_id（LLM 成本已发生，结果价值高于一行轨迹；slog + executions 兜底排障链不断）；保留期 = **knob `WORKFLOW_RUNS_RETENTION_DAYS` 默认 365 天**（新增后台批量 DELETE 任务：workflow 模块内 goroutine、组合根 `go` 启动、随优雅关停，对齐 PartitionMaintainer 先例；非分区表按 created_at 批量删，规模上来后加 `BRIN (created_at)`）；截断 = **单值 16KB + jsonb 内 `truncated: true` 标记**（runs.output / node_runs.input / node_runs.output；slog 节点轨迹截 1KB）。**O1-O7 全部拍板；本篇已冻结（2026-09-18），消费方 rdp-implementation** |

## 6. 交付物（已冻结）

| 层 | 交付物 |
|---|---|
| api | `Execute` 接口方法 + `ExecuteWorkflowReq` / `RunResultSchema`（已冻结，见 api_contract.md §5） |
| service | `execcontext.go`（vars 池 + 模板渲染）、`executor.go`（runNode type switch + callLLM / evaluate / retrieve / callAPI / buildOutput）、`execute.go`（把关 / 快照 / 游走循环 / 收尾写轨迹 / 组装结果）、`httpx.go`（api 节点出站 client：SSRF + timeout / TLS） |
| store | `CreateRun`（一事务 run 1 行 + node_runs N 行多 VALUES INSERT，db_model §12） |
| handler | `POST /workflows/{id}/execute` 薄绑定 |
| 迁移 | **00019**：`workflow_runs` + `workflow_node_runs`（定稿 DDL 见 db_model.md §12，已冻结；00018 已被 spec 05 占用，动手前 `make migrate-status` 确认 18 条 applied） |
| 组合根 | `workflowsvc.New` 追加 llmManager（platform/llm）与 executions 写入缝（platform/logging ExecutionStore 窄接口）注入 |
| 保留期 | `WORKFLOW_RUNS_RETENTION_DAYS`（默认 365）+ 后台批量 DELETE 清理任务（workflow service 内 goroutine，组合根 `go` 启动，随优雅关停——对齐 PartitionMaintainer 先例；O7 拍板） |
| 文档 | CLAUDE.md 错误码表（新增 `WORKFLOW_EXECUTION_FAILED` 500，O4 拍板）与索引地图、data-model.md 增两表、db_model 决策表（#14/#15 已记、§7 条 11 已加） |

slog 节点轨迹与收尾统一写已含在 service 交付内（§4.5）。

## 7. 验收门（spec 01-05 同款门禁）

- `go build ./...` / `go vet ./...` / `go test ./... -race -count=1` 全绿；workflow 包覆盖率 ≥80%。
- 依赖方向 grep：`internal/workflow/` 下无 `internal/chat` import（依赖红线）；api 包无 gin / gorm import。
- 零真实依赖的同包测试：store 用 sqlmock、handler 用 httptest、service stub 下游 api（models / ragSvc / llmManager）与 Store；零真实 PG / Redis / 网络 / LLM。
- 重点表驱动用例：render strict 缺失变量报错；condition 迷你表达式（裸 var / `==` 比较 / 字面量含引号）；route 按声明顺序首条命中 + 无命中 fail-fast；tool 节点 fail-fast（400 图缺陷类）；SSRF 拦截矩阵（loopback / link-local / ULA 拒，RFC1918 放行，`WORKFLOW_API_BLOCK_PRIVATE=true` 全禁，重定向每跳复验）；总时长超限 → `WORKFLOW_EXECUTION_FAILED`；截断 16KB + `truncated: true` 标记；`?trial=true` 放开 draft/disabled、正式路径 503；CreateRun 一事务两批多 VALUES；写入降级（stub 持续失败 → 结果照返、RunID 置空）；R10 保存期校验（引用非祖先 key → 400）。
- 手动冒烟：docs/testing/workflow-manual-test.md 增执行测试小节（迁移 19 条 applied 后过用例）。
