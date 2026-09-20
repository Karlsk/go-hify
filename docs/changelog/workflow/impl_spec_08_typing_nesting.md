# Workflow 实现 spec 08：workflow 分型与子工作流嵌套（Typing & Nesting）

> 状态：**契约已冻结（2026-09-20）**——方向拍板 2026-09-18、细项 O1-O8 逐项交互冻结完毕 2026-09-20（拍板记录见 §5）；本篇即 rdp-implementation 的消费契约，行级实现细节可在实现期微调，语义以本篇为准，契约变更须用户显式批准。递延项见 [deferred_items.md](./deferred_items.md)。
> 上位契约：[CLAUDE.md](../../../../CLAUDE.md)《数据库规范》（text+CHECK 红利：加值只改 CHECK，禁 PG enum）、《代码组织规范》依赖清单；[impl_spec_06_execution_engine.md](./impl_spec_06_execution_engine.md) §3（嵌套留缝：string→string 设计 + 跨图递归深度上限 + parent_run_id，到触发纯增量——**本篇即触发**）与 Execute 冻结契约 / 错误二分法 O4；[api_contract.md](./api_contract.md)（NodeType 密封、状态机、Execute 契约）；[db_model.md](./db_model.md)（图校验 R1-R10、§12 轨迹表）；[impl_spec_07_chat_pipeline.md](./impl_spec_07_chat_pipeline.md)（管道先行，本篇不改其语义）；工具形态备忘录 [docs/tools/workflow-task-tool-memo.md](../../../tools/workflow-task-tool-memo.md)（Option 2——非本篇交付，仅引用）。冲突时停下来问用户。
> 前置依赖：spec 07 合入。
> 迁移假设：**00020**（动手前 `make migrate-status` 确认 19 条 applied）。

## 1. 背景

管道（spec 07）落地后，workflow 出现两类消费场景：**chat 型**——接管对话轮、终答直接是 assistant 消息（管道消费）；**task 型**——string→string 可组合任务函数（嵌套消费 + 将来的工具消费）。此为 Dify「Chatflow / Workflow」分型的简化版收敛（2026-09-18 用户拍板）。

两条立论判断：

1. **分型本质在消费路径，不在引擎**——string→string 同步纯函数对两型是同一个执行器；`type` 列的真实消费者本篇只有两个：嵌套规则（chat 型不可被嵌）与管理面 CRUD。第三个消费者（工具注册把关）归工具篇。**本篇先不和 agent 关联**（用户拍板）：绑定不按类型把关，spec 07 管道对两型一视同仁。
2. **嵌套规则天然系于分型**——可被嵌的必须是纯函数语义的 task 型；chat 型是对话终答生产者，不可被嵌。spec 06 留缝到本篇触发。

嵌套矩阵（用户拍板，2026-09-18）：

| 父图 ＼ 引用目标 | chat 型 | task 型 |
|---|---|---|
| chat 型 | ✗（chat 不可被嵌） | ✓ |
| task 型 | ✗（chat 不可被嵌） | ✓ |
| 自嵌（A→A）/ 间接环（A→B→A） | ✗ 一律禁止（保存期校验 + 执行期深度兜底） | |

## 2. 做什么（范围）

1. **`workflows` 三列**：`type`（`text NOT NULL CHECK (type IN ('chat','task'))`，存量回填 `'chat'`——存量绑定全为管道用法，回填语义精确；Create 必填与否、类型可变性见 O1）；`input_schema` / `output_schema`（jsonb 可空，O7a——简化形态 `[{name, type, required, description}]`，type ∈ string/number/boolean；仅 task 型消费，chat 型置空；Create / Update 校验形态、字段重名与 type 合法性，详见 §4.5）。
2. **新节点类型 sub-workflow**（类型值 `"workflow"`，O2）：config 密封 `{workflow_id, inputs}`——**字段→模板映射**（O7a 修订：键集须逐一覆盖子 input_schema 全部 required 字段、多余字段拒；每个值为 `{{var}}` 模板，R10 天然覆盖）；NodeType 常量 + ParseNodeConfig 分发 + `workflow_nodes.type` CHECK 加值。
3. **保存期图校验 R11**（进 db_model §7 清单，条 12）：引用存在性预检（同模块 store 直查）；被引 workflow 必须 task 型；禁自嵌与间接环（DFS 沿引用链，遇自身 id 即拒）；引用链深度上限（与执行期同值，O4）。
4. **执行引擎 sub-workflow 分支**：逐值渲染 inputs 映射 → 按子 input_schema 组装 JSON 文本入参 → 进程内递归执行子图（同 goroutine、共享父请求 ctx——断连整链取消；**每层自包 5min**，O8）；子终稿过 output schema 校验后落父变量池（node_key 即该节点 key，下游模板可引、JSON 值可下钻）；子图错误带父 node 前缀上抛（错误二分法原样延伸：子图缺陷 → 图缺陷 400；子环境限制 → `WORKFLOW_EXECUTION_FAILED` 500；下游哨兵透传）。
5. **子执行把关**（O6）：`Trial` 跟随父（父试运行 → 子放开 draft）；正式运行子必须 published（否则 `ErrWorkflowNotPublished` 带父 node 前缀）。
6. **子 run 轨迹**（O5 已拍板 A）：独立 run 行 + `trigger_source='workflow'`（CHECK 加值）+ `parent_run_id`（父收尾统一回填；候选对比见 §4.4）。
7. **ConversationID / MessageID 透传**（O7b 已拍板：透传——子 run 行与父相同，因果归属语义；`trigger_source='workflow'` 表达直接触发方式，两列正交）。
8. **管理面 CRUD**：Create / Update 增 `type`；Get / List 暴露 `type`；两型状态机同款（draft/published/disabled 不分叉）；**控制台单次运行测试两型照旧**（execute + trial 与类型无关，零新工作——用户拍板确认项）。
9. **删除语义**（O6）：被嵌 task workflow 删除不扫描引用（jsonb config 引用无法 FK）——保存期预检挡新引用 + 执行期 fail-fast（`ErrWorkflowNotFound` 带父 node 前缀）；引用已删 workflow 的存量图在下次编辑保存时被存在性预检拦截。

## 3. 不做什么（边界）

- **agent 关联 / 工具注册**——task 型虚拟工具（备忘录 Option 2）、绑定按类型把关，全部归工具篇（前置 chat 工具循环 + mcp 执行能力）；本篇 `type` 的消费者只有嵌套规则与管理面。
- **mcp tool 节点执行**（spec 06 既有 fail-fast 语义不变）。
- **工具侧结构化消费**——工具参数 schema 改由 `workflow.input_schema` 生成（备忘录已同步更新）；工具注册、执行与授权归工具篇。引擎侧结构化已在本篇落地（O7a：schema 列 + JSON 文本入参 + 池下钻；Execute 单一 string 签名不变，spec 06 O1 语义不重开）。
- **并行分支 / 合并 / 循环节点**、定时 / 事件触发（既有边界不变）。
- **chat 型管道语义改动**——spec 07 合入后不动；分型不 gate 绑定（chat 型或 task 型绑定都照走管道，直到工具篇拍板把关）。
- **异步执行 + 轮询 / 按节点 SSE 流式**——每层独立 5min 下同步链墙钟仍受 nginx 300s 约束；逃生阀（重开 spec 06 异步边界）触发 = 真实长链需求（deferred_items #8）。
- **前端**（类型选择 UI、嵌套编辑提示归前端篇）。

## 4. 行为语义（草案——冻结对象）

### 4.1 类型生命周期

Create 必填 `type`（`oneof=chat task`，O1 已拍板，2026-09-20）；**类型不可变**（O1 已拍板：Update 拒改——被嵌合法性与消费语义系于类型，中途换型等于暗中改写所有引用方的合法性；确需换型 = 删了重建，嵌套校验自然重跑）。状态机两型同款，发布 / 停用动作不分型。

### 4.2 嵌套校验（保存期 R11）

整图提交时（Create / Update）对每个 sub-workflow 节点：

1. `workflow_id` 存在（store 直查，同模块无跨模块限制）；
2. 被引 workflow `type == 'task'`；
3. 环检测：从被保存图出发 DFS 沿被引图的 sub-workflow 引用链展开，链上出现被保存图自身 id → 拒（自嵌是长度 1 的特例）；
4. 链深超上限（O4）→ 拒。
5. `inputs` 键集覆盖（O7a）：须逐一命中被引图 input_schema 的全部 required 字段、多余字段拒（与存在性预检同批加载被引图 schema；被引图无 schema 时要求映射恰为 `{input}`——回退单一入参语义）。

竞态（A、B 并发互引成环）：保存期两笔都过、环在库中成形——接受窗口（单管理员内部规模），执行期深度上限兜底拒绝。R10 模板校验对 `input` 模板天然生效（引用名 ∈ {input} ∪ 祖先 node_key）。

### 4.3 sub-workflow 节点执行

```text
runNode("workflow", cfg, ec):
  fields := {k: ec.render(v) for k, v in cfg.Inputs}  // 字段→模板映射逐值渲染（strict，缺失即图缺陷）
  input  := assembleJSON(fields, child.InputSchema)   // 按 schema 组装 JSON 文本（required 缺失保存期已挡）
  child  := executeChild(ctx, cfg.WorkflowID, input, parent{depth, trial, convRef})
  return child.Output                                 // 子终稿（已过 output schema 校验）落父变量池
```

- `executeChild`：把关（trial 跟随；正式须 published）→ **每层自包 5min**（`WithTimeoutCause(5min)`，O8；请求 ctx 全程传播——断连整链取消、run 行照写）→ 子 vars 池全新起步（只含自身 input，**不可引用父 vars**——O7 隔离）→ 收尾落子 run 行（`trigger_source='workflow'`、conversation_id / message_id 透传——O7b）。
- 子终稿 output schema 校验失败 → 图缺陷 400（父 node 前缀）——子作者契约（O7a，规则见 §4.5）。
- 深度计数随 executeChild 传递，超上限 → 图缺陷 400（父 node 前缀）——竞态窗口与防御性兜底。
- 子游走失败 → 错误上抛（前缀链 `node a: node b: ...` 可读定位）→ 父记失败 step（sub-workflow 节点行 failed），父 run 行 error_node 定位到父节点。
- 子 run 写入失败降级同 spec 06 O7③（重试一次，仍败照常返回，链断处 trace_id 兜底）。

### 4.4 子 run 轨迹形态（O5，已拍板 A）

| | A：独立行 + parent_run_id | B：仅 trace_id 关联 | C：并入父 node_runs 展开 |
|---|---|---|---|
| 形态 | 子 run 独立落库；父收尾 UPDATE 回填 parent_run_id | 子 run 独立落库，零新列 | 子图节点轨迹并入父 run 的 node_runs |
| 查询 | `WHERE parent_run_id = X` 显式树 | 靠 trace_id 聚合（混入 chat 侧整条日志链） | 单 run 全展开 |
| 代价 | append-only 一次窄 UPDATE 例外（须注记进决策表） | 零契约改动但查询弱、无显式父子 | 无独立 run 统计、node_key 需前缀防撞 |
| 建议 | ✓（显式可查、复用 run 行全部字段与保留期清理） | | |

### 4.5 结构化 input/output 契约（O7a，已拍板）

- **列与形态**：`workflows.input_schema` / `output_schema` jsonb 可空；简化 schema = 字段数组 `[{name, type, required, description}]`，type ∈ `string` / `number` / `boolean`；仅 task 型消费，chat 型置空。
- **Execute 签名不动**：入参仍单一 string——task 型入参 = 按 input_schema 组装的 JSON 文本（spec 06 O1「单一入参终形」语义保持，不重开）；引擎侧检测入参为合法 JSON 对象且声明了 input_schema 时按对象解析入池，否则整串落 `input`（原行为）。
- **池一级下钻**：`{{input.x}}` / `{{node.field}}`——池值为 JSON 时可下钻一层字段（string 值行为不变，深度一层为止）；R10 保存期只校验基名（点号前 ∈ {input} ∪ 祖先 node_key），字段名运行期 strict（缺失 / 非 JSON → 图缺陷 400 带 node 前缀）。
- **输出校验**：task 型终稿（末节点输出 / end.output 渲染结果）为 JSON 时按 output_schema 校验——required 缺失或类型不符 → 图缺陷 400（嵌套时带父 node 前缀，顶层无前缀）；声明了 output_schema 而终稿非合法 JSON → 同拒。
- **保存期联动**：R11 第 5 条（inputs 键集覆盖，见 §4.2）——嵌套传参契约错误在画图时暴露，不留到运行。

## 5. 拍板项（O1-O8 已全部拍板：方向 2026-09-18，细项 2026-09-20）

| # | 事项 | 状态与候选 |
|---|---|---|
| O1 | type 枚举、Create 语义与可变性 | **✅ 已拍板（2026-09-20，用户）：必填 + 不可变**——`'chat'/'task'`，Create 必填（oneof 校验，内部前端同步改无兼容负担）；Update 拒改类型（换型 = 删了重建，嵌套校验自然重跑）；存量回填 chat |
| O2 | sub-workflow 节点类型值与 config | **✅ 已拍板（2026-09-20，用户；同日随 O7a 修订）**——类型值 `"workflow"`（与既有六类值同风格）；config 密封 `{workflow_id: string（字符串化外键）, inputs: 字段→模板映射}`——**O7a 修订**：原单一 `input` 模板改为映射，键集须逐一覆盖子 input_schema 全部 required 字段、多余字段拒，每个值为 `{{var}}` 模板（R10 天然覆盖）；NodeType 常量 + 密封解析 + CHECK 加值 |
| O3 | 嵌套矩阵与校验落点 | **✅ 矩阵已拍板（2026-09-18，用户，见 §1）；落点已拍板（2026-09-20，用户）**：保存期 R11（存在性 + task 型 + 环 DFS + 链深）+ 执行期深度兜底；并发互引竞态窗口接受（单管理员内部规模）。仅执行期校验（错误图能入库）与删除期反向扫描（jsonb 遍历成本不成比例）均否决 |
| O4 | 递归深度上限值 | **✅ 已拍板（2026-09-20，用户）：3**——顶层 + 2 层嵌套（A→B→C ✓、A→B→C→D ✗）；保存期链深与执行期兜底同值；过深图是该拆的信号 |
| O5 | 子 run 轨迹形态 | **✅ 已拍板（2026-09-20，用户）：A 独立行 + parent_run_id 父收尾回填**——子 run 复用 run 行全部字段（统计 / 保留期清理 / 错误定位），`WHERE parent_run_id = X` 显式树查询；append-only 一次窄 UPDATE 例外注记进 db_model 决策表。B（trace_id 弱关联查询）、C（无独立 run 统计）否决 |
| O6 | 子执行把关与删除语义 | **✅ 已拍板（2026-09-20，用户）**——Trial 跟随父（父试运行 → 子放开 draft/disabled）；正式运行子必须 published（否则 `ErrWorkflowNotPublished` 带父 node 前缀）；被引 workflow 删除不扫描：保存期存在性预检挡新引用 + 执行期 fail-fast（`ErrWorkflowNotFound` 带父 node 前缀）。恒放开 / 恒严格 / 删除期扫描均否决 |
| O7 | 子 run 隔离、结构化 input/output 与引用透传 | **✅ 已拍板（2026-09-20，用户）**——**①隔离（subagent 语义）**：子 vars 池全新起步（只含自身 input），不可引用父 vars，父也不能引用子内部 var；父池只进该节点子的终稿、只见子 output schema；父子仅经 input/output 通信。**②结构化（简化 schema + 池下钻）**：workflows 增 input_schema / output_schema jsonb（仅 task 型；简化形态 name/type（string、number、boolean 三选一）/required/description）；Execute 签名不动（入参仍单一 string，task 型 = 按 schema 组装的 JSON 文本）；池一级下钻 `{{input.x}}` / `{{node.field}}`；子终稿过 output schema 校验，不过 → 图缺陷 400 带父 node 前缀（详见 §4.5）。**③引用透传**：子 run 行 conversation_id / message_id 与父相同（因果归属语义，一次查询还原整棵执行树；层级区分靠 `trigger_source='workflow'`；不透传 = 消息侧回溯到父即断链且对隔离零增益，否决） |
| O8 | 子执行超时语义 | **✅ 已拍板（2026-09-20，用户）：每层独立**——每个 executeChild 自包 `WithTimeoutCause(5min)`；请求 ctx 全程传播（客户端断连 → 整链取消、游走中断、run 行照写）；同步路径墙钟仍受 nginx 300s 事实约束（两层最坏 10min > 300s 会在网关处断——接受，逃生阀是异步）；共享父预算（原候选）与逐层叠乘均否决。**递延**：异步执行 + 轮询（重开 spec 06 异步边界）与按节点 SSE 流式，触发 = 真实长链需求（deferred_items #8） |

## 6. 交付物（草案，随冻结定稿）

| 层 | 交付物 |
|---|---|
| api | `NodeTypeWorkflow = "workflow"` + 密封 config 解析（字段→模板映射）；`UpsertReq.Type` / `InputSchema` / `OutputSchema`、`WorkflowDetailSchema.Type` 及 schema 字段、`WorkflowSummarySchema.Type`（schema 形态校验）；R11 进 db_model §7（条 12，含 inputs 键集覆盖子条）；**哨兵零新增**（四类复用：NOT_FOUND / NOT_PUBLISHED / VALIDATION_FAILED / EXECUTION_FAILED） |
| service | executor 增 sub-workflow 分支 + `executeChild`（把关 / 每层自包 5min / 子池全新起步 / 落子 run / 深度计数）；JSON 组装与 output schema 校验；变量池一级下钻（渲染器扩展）；保存期嵌套校验（DFS + 存在性 + 环 + 链深 + inputs 键集覆盖）；buildRun 增父关联与子 run id 累积；深度上限常量 |
| store | type 列读写；parent_run_id 回填（O5 A：按父 run id 批量 UPDATE，一次窄写）；被引图加载复用 loadGraph |
| handler | 无新路由；type 字段绑定；Update 拒改 type 的映射 |
| 迁移 | **00020**：`workflows` 加 type 列 + CHECK + 存量回填 chat，加 `input_schema` / `output_schema` jsonb 可空 + COMMENT；`workflow_nodes.type` CHECK 加 `'workflow'`（约束重建，实现期核对 00016/00017 约束名）；`workflow_runs.trigger_source` CHECK 加 `'workflow'` + `parent_run_id` 列 + COMMENT |
| 组合根 | 零改动预期 |
| 文档 | CLAUDE.md（错误码表零新行注记、索引地图）；data-model.md（type + workflows 自引用弱引用关系）；db_model.md（R11 条 12 + 决策 #16：分型 / 嵌套矩阵 / parent_run_id 例外）；api_contract.md（type 字段 + 节点类型清单 + 嵌套语义）；manual-test 嵌套冒烟小节 |

## 7. 验收门（同款门禁）

- `go build ./...` / `go vet ./...` / `go test ./... -race -count=1` 全绿；workflow 包覆盖率 ≥80%。
- 依赖方向 grep 不变（workflow 无 chat import；api 包无 gin / gorm）。
- 零真实依赖同包测试：service stub Store（嵌套校验与子执行走 stub 图数据）、store sqlmock、handler httptest。
- 重点表驱动用例：分型 CRUD（type 必填 / Update 拒改 / 存量回填 chat）；R11 矩阵（引 chat 型拒 / 自嵌拒 / A→B→A 拒 / 链深超 3 拒 / 引用不存在拒 / 合法 chat⊃task 与 task⊃task 过）；sub-workflow 执行（input 渲染 / 子终稿落池 / 下游模板可引 / 前缀链错误定位）；子把关（trial 跟随 / 正式子 draft 拒）；执行期深度兜底（stub 构造深链）；O5 A 方案（子 run 行 `trigger_source='workflow'` + parent_run_id 回填 / 父写失败跳过回填 trace_id 兜底）；引用透传（conversation_id / message_id 与父一致，一次查询还原整棵树）；**结构化契约**（schema 形态校验拒非法 type 与重名字段 / R11 inputs 键集缺 required 拒、多余拒、无 schema 回退 `{input}` / JSON 组装 / 池下钻 `{{input.x}}` 与 `{{node.field}}`、string 值行为不变 / output 校验失败 400 带父前缀、终稿非 JSON 拒 / 子池隔离——子图引用父 vars 报缺失）；**spec 07 管道回归**（分型后绑定不 gate，两型绑定都照走管道）；既有 spec 06 引擎用例全绿。
- 人工项：控制台两型单次运行测试照旧（execute + trial）；嵌套图执行冒烟 + psql 查 runs 树（parent_run_id 关联、各 run seq 顺序）。

## 可提交节点（草案）

`feat(workflow): 分型（chat/task）与子工作流嵌套——sub-workflow 节点 + R11 嵌套校验 + parent_run_id（spec 08）`
