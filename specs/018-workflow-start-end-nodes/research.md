# Research: Workflow 画布开始/结束节点与入参默认值（spec 018 Phase 0）

**Date**: 2026-10-09 | **Spec**: [spec.md](./spec.md)（含 Clarifications）| **基线**: spec 016（`5af6727`）/ 017（`57a0bfe`）已合入；代码现状已逐文件核实（api/schema.go、service/execute.go、executor.go、execcontext.go、service.go、web workflow 视图族）。

产品级决策 D1–D5 与 clarify 修正见 spec（多 end 放开 + required 自动推导）；本文只记录**实现级**设计决策（Decision / Rationale / Alternatives）。

## R1：`SchemaField.Default` 的 Go 类型 = `json.RawMessage`

- **Decision**: `Default json.RawMessage \`json:"default,omitempty"\``（空 slice/nil = 未配置）。
- **Rationale**: ① 保真——number 默认值（如 `10`、`3.5`）原文落位不经 float64 往返（对齐 assembleInputJSON 既有 `json.Number` 原文落位先例）；② omitempty 对 RawMessage 按「空」判定干净（`any` 型对 `false`/`0` 语义含糊）；③ 校验与消费两侧统一用「原文探针」——api 层新增 probe（unmarshal 到 string / json.Number / bool）判类型匹配，无需中间 Go 类型。
- **Alternatives**: `any`（float64 往返规约数字形态，`omitempty` 对非 nil 值不省略，弃）；`*string`（number/boolean 要二次解析，弃）。
- **注**: jsonb 列在 PG 侧本就规约数字形态（jsonb 存 numeric），「保真」指执行侧组装 / 归一化不引入二次规约。

## R2：required 禁配 default 在 `ValidateSchemaFields` 拒（保存期 400）

- **Decision**: `f.Required && len(f.Default) > 0` → 报错（文案含字段名）；default 非 null 且类型探针不匹配 field type → 报错。
- **Rationale**: 语义自洽——required 字段调用方必传，default 永不生效，配置即矛盾；保存期拦截（VALIDATION_FAILED 400）与既有 name/type 校验同层同路径（api Validate → handler 既有翻译），零新错误码。`null` default 对三型探针皆失败，天然被「类型不匹配」拒绝。
- **Alternatives**: 运行期忽略 required 字段的 default（静默吞矛盾配置，排障困难，弃）；允许配置但执行期才拒（错误暴露过晚，弃）。

## R3：`EndConfig` 两键互斥拒（`output` 与 `output_fields` 同时配置 → 400）

- **Decision**: `EndConfig.Validate` 增：`Output != "" && len(OutputFields) > 0` → 拒；行级校验 name 非空、行内不重名、ref 必须恰为单一 `{{var}}`（api 包内新增 `singleVarRE = ^\{\{([^{}]+)\}\}$`，形态对齐 service 包 condition 的 singleVarRE 先例）。
- **Rationale**: 两键并存 = 终稿语义二义（buildOutput 按 output_fields 优先，output 成死配置）；互斥让契约面无歧义。ref 形状在保存期挡（点分字段名留运行期 strict，与 R10 基名切分规则一致）。
- **Alternatives**: 允许并存按优先级取（死配置不报错，弃）；ref 允许混排文本（映射语义要求值即变量，混排产不出合法 JSON 值，spec FR-003 已禁）。

## R4：output_schema 声明类型经 `execContext.outputTypes` 透传给 buildOutput

- **Decision**: `execContext` 加 `outputTypes map[string]string`（字段名 → 声明类型）；`Execute` / `executeChild` 在 newExecContext 后从 `wf.OutputSchema` 装配（schemaFields 解析失败置 nil——fail-open，与顶层归一化同哲学）。
- **Rationale**: executor 的 `runNode(ctx, key, cfg, ec)` 签名不动、executor 不持有 service / wf（既有分层：编排层装配、执行器消费）；execContext 本就是单次执行状态的承载处，加法字段零侵入。
- **Alternatives**: buildOutput 加参（runNode 分发签名连锁改动，弃）；executor 持有 outputSchema（破坏无状态执行器分层，弃）；buildOutput 不看声明类型、恒走「可解析则原样」回退（裸引用值形似数字的字符串会被当 number，与声明 string 冲突 → 校验误杀正常运行，弃）。

## R5：`normalizeInputJSON` 失败路径全放行（只补默认，D5 边界）

- **Decision**: 纯函数 `normalizeInputJSON(input string, fields []SchemaField) string`——input 可解析为 JSON 对象才进入补全路径（缺失且带 default 的 optional 字段 → 填入）；非合法 JSON / 非对象（数组 / 数字 / 字符串）/ 解析后无变化 → 原文返回；未声明字段带不管、类型不符不管。`Execute` 仅在 `wf.Type == task && wf.InputSchema != nil` 且 schemaFields 解析成功时调用；归一化产物同入 `runMeta.input`（轨迹记实际执行值）。
- **Rationale**: D5 拍板「只补默认不新增拒绝」——顶层 input 从不校验是既有契约（O1 单一 string 终形），本篇纯加法；schema 损坏（存量 jsonb 异常）fail-open 放行原文，模板引用期 strict 渲染自然报错，错误定位不变。
- **Alternatives**: 顺带严格校验（非 JSON 拒 400 / 未声明字段拒——行为变更，明确留给 task 型虚拟工具 spec，弃）；归一化结果不进轨迹（排障时看不到实际执行值，弃）。
- **细节**: map[string]json.RawMessage 重 marshal 会按 key 排序（Go 标准库保证），input 键序可能变化——`{{input.x}}` 下钻是解析型取值，键序无关；轨迹里是规约后 JSON 文本，可复现。

## R6：`buildOutputFields` 值处理 = 声明类型优先、未声明回退

- **Decision**: 逐字段 `lookup(ref)` 取池值（含一级下钻）→ 查 `ec.outputTypes[name]`：声明 string → 值按 Go string 落位（json.Marshal 转义，**这是 D2 采用后端构造的根因**）；声明 number/boolean → 值须可解析为对应 JSON 类型（原文落位 json.RawMessage），解析失败报图缺陷（声明与实际值不符，保存期挡不住运行期第一现场）；未声明 → 回退「可解析为 JSON 则原样（number/bool/对象/数组）、否则按字符串」。缺失变量 strict 报错归 `errs.ErrValidationFailed`（对齐 render）。整对象 `json.Marshal` 产出。
- **Rationale**: 声明类型优先消除「形似数字的字符串」歧义（R4 alternatives 已论证）；转义正确性由 Marshal 结构性保证，模板渲染无转义的缺陷（字符串含引号即碎）从根上消除；nodeIn 摘要记 `output_fields` 渲染前形态（排障对齐既有 `{"output": ...}` 形态）。
- **Alternatives**: 前端生成 output 模板（spec 起草期已否决——无转义，正确性缺陷）；恒按字符串处理（number/boolean 产出带引号，类型校验必炸，弃）。

## R7：R10 覆盖 `output_fields` 各 ref（基名规则不变）

- **Decision**: `service.go nodeTemplateRefs` 的 `EndConfig` 分支在既有 `templateRefs(v.Output)` 后追加各 `of.Ref` 的 refs；基名切分（`strings.Cut(ref, ".")`）与祖先判定逻辑零改动。
- **Rationale**: 新模板字段必须进 R10 冻结清单（db_model §7 条 11），否则非祖先引用漏到运行期才炸；`OutputField` 是有序 slice，天然确定性（无需 map 排序）。
- **Alternatives**: 只靠运行期 strict（保存期漏检，与 R10 存在动机相悖，弃）。

## R8：伪开始节点 = 哨兵复用 + 固定坐标渲染（纯前端）

- **Decision**: CanvasEditor 渲染一个 id=`__start__` 的 Vue Flow 节点（复用既有 `START_NODE_ID` / `START_NODE_TYPE` / `isStartContextNode` / `openSchemaPanel` 管道），固定坐标（左侧锚位，不随画布数据持久化——positions 本就是 session-only）、`draggable: false` 不可删不可连边；点击选中即开既有检查器 schema 面板；task 型节点副标签显示入参数（如「3 入参」）、chat 型显示「单一入参 {{input}}」；左面板「入参/出参」按钮移除。序列化面零改动（哨兵永不进 `nodes`——既有 `isStartContextNode` 删除守卫已保证）。
- **Rationale**: spec 012 删除的只是画布渲染，哨兵管道全在——本篇恢复渲染是最小增量；固定坐标避免与真实节点 positions 序列化 / 迁移逻辑纠缠（不画边同理，FR-001 拍板）。
- **Alternatives**: 后端真 start 节点类型（ NodeType/CHECK 迁移/R 规则连锁，db_model 决策 #7 哲学冲突，spec 已明确不做）；画 start→首节点 边（序列化过滤 + 误删防护成本 > 方向感收益，弃）。

## R9：图级 output_schema 并集同步与 required 推导 = 前端纯函数（graph.ts）

- **Decision**: `deriveOutputSchema(nodes, inputSchema): { schema: SchemaField[]; conflict?: string }`——扫全部 end 节点的 `config.output_fields`，同名同型合并、同名不同型返回冲突标记（保存期 UI 拦截）；每字段 required = 是否**所有**含该字段的映射 end 都产出（等价：该字段产自全部映射 end）；type 按 spec 推导规则（`{{input.x}}` → input_schema、`{{llmKey.field}}` → llm output_schema、`{{workflowKey.field}}` → 子工作流 output_schema、裸 `{{nodeKey}}` → string；类型不可得一律 string 兜底）。保存（create/update payload 组装）时以该函数产出覆盖 `output_schema`；JSON 模式直改 output_schema 时以原文为准（power-user 路径，运行期 validateOutputSchema 兜底）。
- **Rationale**: 并集推导是纯函数、可单测（graph.ts 是纯逻辑模块的既有定位）；后端不做跨 end 校验（类型信息在 ref→推导链上，后端无推导能力，运行期校验兜底——spec FR-004 拍板）。
- **细节**: 多 end 各自映射不同 ref 到同名字段、类型一致 → 合法合并（运行期按实际路径取值，类型一致即契约一致）；required 推导依赖「映射 end 全集」，某 end 删除映射后 required 随之重算。
- **Alternatives**: 后端存每 end 独立 schema（契约膨胀，弃）；required 手工开关（clarify Q2 已否决）。

## R10：试运行预填与「清空 = 使用默认」的提交语义

- **Decision**: WorkflowTrialDialog（task 型有 schema）打开时按 `input_schema[].default` 预填表单值（可改可清空）；提交组装 JSON 时的省略规则**限定「optional 且配置了 default」的行**——清空则该行从对象省略，缺失字段由顶层归一化补回默认，「清空」的用户语义 = 用默认。未配 default 的 optional 行维持现状（空值照传 `""`）：analyze F2 修正——无差别省略会把「传空串」变「缺失」，strict 渲染报变量未定义，属存量回归。required 空值仍前端拦截（既有行为）。
- **Rationale**: spec Edge Case 拍板的清空语义（analyze 修订后范围）；省略限定带默认行使两种意图（用默认 vs 传空串）在「有默认」行上可区分，无默认行行为零变化（US4）。
- **Alternatives**: 清空传空串（空串覆盖默认且与「未填」不可区分，弃）；预填不可改（丧失试运行灵活性，弃）。
