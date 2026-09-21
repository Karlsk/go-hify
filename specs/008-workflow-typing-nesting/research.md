# Research: workflow 分型与子工作流嵌套

> 输入：冻结契约 impl_spec_08_typing_nesting.md（O1-O8 全拍板）+ 本仓 spec 06/07 交付现状。
> Technical Context 无 NEEDS CLARIFICATION——以下为实现层决策记录（Decision / Rationale / Alternatives）。

## D1: trigger_source CHECK 加值的迁移写法

- **Decision**: 00020 中 `DROP CONSTRAINT workflow_runs_trigger_source_check`（00019 内联 CHECK 的 PG 自动命名）后 `ADD CONSTRAINT workflow_runs_trigger_source_check CHECK (trigger_source IN ('console','chat','workflow'))`，沿用 00017 对 `workflow_nodes_type_check` 的同名重建模式；Down 反向恢复两值。
- **Rationale**: PG 无法向既有 CHECK 追加值，重建是唯一路径；保持约束名稳定让 00017 先例可抄、Down 可逆。实现期动手前用 `\d workflow_runs` 或 `pg_constraint` 复核自动命名与预期一致（契约 §6 交付物表已注明「实现期核对约束名」）。
- **Alternatives**: 新增第二条件约束 `trigger_source IN ('workflow') OR ...` —— 语义混乱、拆成两个约束难维护，否决。

## D2: buildRun 的 trigger_source 改显式传参

- **Decision**: `buildRun` 现由 `req.ConversationID != nil` 推导 trigger_source（execute.go:213-215）；O7b 透传后子 run 也携带 ConversationID，判定不再成立。改为执行入口显式传入 trigger 源（'console' / 'chat' / 'workflow'），子执行传 'workflow'。
- **Rationale**: O7b 拍板 conversation_id/message_id 透传（因果归属）与 trigger_source（直接触发方式）两列正交——推导式无法表达正交性。属实现细节非契约变更（行为输出不变：console/chat 路径照旧）。
- **Alternatives**: 子 run 不透传 ConversationID、维持推导 —— 直接违反 O7b 拍板，否决。

## D3: 保存期 R11 的环检测与链深算法

- **Decision**: 保存图 G 时对每个 sub-workflow 节点沿被引图递归展开：加载被引图（复用 service 侧 loadGraph，store 只做存在性 + type 直查）→ 取其 sub-workflow 节点 → 递归。**环检测**：DFS 路径集合中出现 G 自身 id 即拒（自嵌 = 长度 1 特例，天然覆盖）；visited 集合防重复展开（B 引用 C 两次只展开一次）。**链深**：以「从 G 出发的嵌套层数」计数，> 2（即总链 > 3：顶层 + 2 层）拒。
- **Rationale**: 契约 §4.2 原文即 DFS 沿引用链展开；图规模极小（每图节点数个位到十位数），递归无性能顾虑。执行期深度兜底用同值常量（`maxNestingDepth = 3` 总链长）。
- **Alternatives**: 拓扑排序判环 —— 对「链上出现自身 id」这一不对称判定（不是一般环检测，是 reachability-to-self）更绕，否决。

## D4: 结构化入参与池下钻

- **Decision**: executeChild 组装 JSON 文本入参 = 按子 input_schema 逐字段渲染 `{"name":"{{tpl}}",...}` 成 JSON 对象后 marshal（字符串原样、number/boolean 按声明类型写入——渲染结果均为 string，number/boolean 字段做显式转换，转换失败即图缺陷）。引擎入口检测：入参为合法 JSON 对象 **且** 该 workflow 声明了 input_schema → 解析入池（`input` 键 = 原始 JSON 文本 + 各顶层字段独立入池）；否则整串落 `input`（原行为）。模板 `{{input.x}}` / `{{node.field}}`：基名解析后若池值为合法 JSON 对象且含该字段则取字段值（marshal 回文本），string 值行为不变，**深度一层为止**（字段值再为 JSON 不再下钻）。R10 保存期只校验基名（点号前 ∈ {input} ∪ 祖先 node_key），字段名运行期 strict。
- **Rationale**: 契约 §4.5 逐条对应；「检测而非强制」保住 chat 型与无 schema task 型的原行为零回归。
- **Alternatives**: 为结构化另开 Execute 方法或入参结构体 —— 违反 spec 06 O1 单一 string 入参终形（契约明确不重开），否决。

## D5: output schema 校验时机

- **Decision**: executeChild 内、子终稿返回父节点之前校验（子声明 output_schema 且终稿为合法 JSON 对象时逐 required 字段查存在与类型；声明了 schema 而终稿非合法 JSON 同拒）。失败 = 图缺陷 400（子作者契约），带父 node 前缀。
- **Rationale**: 契约 §4.3 / FR-006；在父侧校验而非子图内部，错误前缀链自然形成 `node a: node b: ...`（子内部错误本就带一层前缀）。
- **Alternatives**: 子图 buildOutput 内自校验 —— 子无法区分「作为顶层执行」与「被嵌执行」的 schema 消费差异，且前缀语义割裂，否决。

## D6: parent_run_id 回填实现

- **Decision**: 父 run 行落库后（既有 WithoutCancel 写入路径），按父 run id 对本链子 run 行做一次 `UPDATE workflow_runs SET parent_run_id = $1 WHERE id = ANY($2)`（executeChild 过程累积子 run id 集合）。父 run 写失败（重试后仍败）→ 跳过回填，trace_id 兜底（既有降级语义，链断处可溯）。
- **Rationale**: 子执行时父 run id 未知（父行尚未写入），回填是 O5 A 方案的必然形态；一次窄 UPDATE 而非逐行，符合「批量写」纪律。DML 带 WHERE、参数类型对齐 bigint[]。
- **Alternatives**: 子行创建时预分配父 run id —— 需要提前生成父 id 并改 CreateRun 契约，改动面大且引入「父行可能永不落库的孤儿 id」，否决。

## D7: Update 拒改 type 的错误映射

- **Decision**: UpdateWorkflowReq 增 `Type` 字段（`*string`，携带即拒——同值异值均算，clarify 拍板）；service 校验非 nil → `errs.ErrValidationFailed`（details 带字段级信息），handler 既有 VALIDATION_FAILED→400 分支覆盖，零新哨兵。
- **Rationale**: 契约「哨兵零新增」；拒改属请求校验语义，与图缺陷共用 errs.ErrValidationFailed 不产生歧义（message 区分）。
- **Alternatives**: 新哨兵 WORKFLOW_TYPE_IMMUTABLE —— 违反零新增拍板，否决。

## D8: chat 型 schema 强不变量

- **Decision**: Create/Update 校验——type=chat 且 InputSchema/OutputSchema 非空 → `errs.ErrValidationFailed`（clarify 拍板）；task 型 schema 可空（无 schema = 单一入参语义）。
- **Rationale**: 「chat 型置空」作为强不变量防止脏数据入库被工具篇误消费；显式 400 可测可排障。
- **Alternatives**: 静默丢弃置空 —— 前端误以为声明成功，排障困难，否决。

## 结论

全部决策有契约条款或 clarify 拍板背书，无开放问题；第三方依赖零新增（go.mod 不动）。
