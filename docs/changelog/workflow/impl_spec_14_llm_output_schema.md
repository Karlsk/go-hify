# Workflow 实现 spec 14：LLM 节点输出字段声明与运行期 JSON 校验

> 状态：**已实施（2026-09-24，`cfea19e`）**——规划产物与实现同笔提交（`specs/014-workflow-llm-output-schema/` 七件套 + 代码），本篇为回溯性 changelog，实现事实以代码与 specs/014-workflow-llm-output-schema/ 为准。
> 上位契约：[impl_spec_06_execution_engine.md](./impl_spec_06_execution_engine.md)（llm 节点执行语义：消息序列 / strict 渲染 / node_in 记录实发）、[impl_spec_08_typing_nesting.md](./impl_spec_08_typing_nesting.md)（`{{node_key.field}}` 一级下钻 FR9 / task 型 output_schema 校验家族语义）、[impl_spec_11_llm_system_prompt.md](./impl_spec_11_llm_system_prompt.md)（llm config 加法修订先例）。
> 前置依赖：spec 011/012/013 已合入；spec 08 引擎一级下钻与 task 型校验家族已交付。
> 迁移假设：**零迁移、零新增哨兵错误、零新增第三方依赖**——llm config 加法键走 jsonb，校验失败复用 `VALIDATION_FAILED`。

## 1. 背景

用户原始诉求：「模型输出 JSON 给 llm 节点加个输出约定声明并让下拉也能展开」。引擎 `{{node_key.field}}` 一级下钻（spec 08 FR9）早已支持，但编辑期三缺：llm 节点无输出字段声明入口、下游变量下拉只有整体引用条目（`{{llm1}}`）、运行期对模型回复无任何保证——下拉展开若只做编辑期提示，对运行期是「善意谎言」。本篇补齐「编辑期可知 + 运行期有保证」两环，spec 011 同款纯加法修订。

## 2. 做什么（范围）

- llm 节点 config 新增可选键 `output_schema`（字段形态与 task 型 schema 字段一致：name / type / required / description；`omitempty`，未声明 / 空 = 现状零变化）；保存期对新键做形态校验（name 非空不重名、type ∈ string/number/boolean），存量无声明路径零新增拒绝（FR-001/FR-002）。
- 前端编辑器 llm 节点检查器新增输出字段声明区（复用 task 型 SchemaFieldsEditor 行编辑形态；readonly 态不渲染）；变量下拉对声明了输出字段的祖先 llm 节点按声明展开 `{{node_key.field}}` 条目（标注类型与必填、光标处插入），未声明节点维持整体引用条目不变（FR-003/FR-004）。
- 运行期严格校验：声明了字段的 llm 节点，模型回复在输出入池前按声明校验——须为合法 JSON 对象（null / 数组 / 纯文本 / markdown 围栏均拒，不剥围栏不纠偏重试）、required 缺失拒、类型探针不符拒、多余字段宽容；不合规即节点失败，错误含节点定位与字段名，run 落 failed（FR-005）。
- 声明非空时，strict 渲染完成后在 user 消息末尾自动追加系统固定 JSON 输出指令（逐一列出声明字段的名称 / 类型 / 必填性；固定文案不做模板渲染、不含用户变量；`system_prompt` 不注入）；追加后的完整 user 消息即实际发送与实际记录文本（node_in 与 executions 所见即所发）（FR-006/FR-007）。

## 3. 不做什么（边界）

- 零迁移 / 零新哨兵 / 零新依赖：config 为 jsonb 加法键；校验失败复用 `VALIDATION_FAILED` 图缺陷类，错误链带节点 key 前缀。
- 不做纠偏重试、不剥 markdown 围栏（注入提高合规率，不合规即失败）。
- 声明改名 / 删除后不追改下游既有 `{{llm1.旧字段}}` 引用（与 key 改名同策略，保存期 R10 兜底）。
- 字段名含点号不限制（与引擎一级下钻「整体匹配不递归」语义一致，作者自担语义）。
- 嵌套子图内声明同样生效（子图独立游走，声明不跨图）；trial 试运行同样走校验（试运行即真实执行路径）。
- 运行历史查询不在本篇——另立 spec 015 逐篇过各自的验收门。

## 4. 关键决策与保真不变量

| 决策 | 内容 |
|---|---|
| 三项拍板（2026-09-23） | ① 严格校验（对齐 task 型 output_schema 家族语义，声明即契约）；② 自动注入 JSON 输出指令（渲染后追加固定文案，记录实际发送文本，排障所见即所发）；③ 完整字段形态（复用 task 型 schema 字段形态与编辑组件，不为 llm 另造简版）。 |
| 注入位置 | strict 渲染成功后在 user 消息末尾追加——指令不参与模板渲染，`system_prompt` 与作者 prompt 中的自行说明与其共存不冲突。 |
| 纯加法 | 未声明节点的消息序列 / 记录形态 / 序列化往返逐字节不变（spec 011 同款先例）；校验与注入只发生在显式声明的节点上。 |
| 冒烟修复（实施期暴露） | ① 声明区受控回路：set 原样写回（避免输入过程被格式化打断）；② 组装层 `toPayloadNode` 空行清理（无声明节点序列化往返不引入新键）。 |

**保真不变量**：未声明 llm 节点的存量图（含嵌套子图、chat 型）保存 / 执行 / 序列化行为与引入前逐字节一致；既有变量下拉分组、既有表单与校验零回归；既有全量自动化测试零改动全绿（SC-003）。

## 5. 交付物

| 层 | 交付物 |
|---|---|
| api | `internal/workflow/api/schema.go`（llm config 增 `output_schema` 加法键 + 字段集形态校验复用）+ `schema_test.go`（+71） |
| service | `internal/workflow/service/executor.go`（渲染后注入固定 JSON 指令 + 回复按声明严格校验，VALIDATION_FAILED 带节点 / 字段定位）+ `executor_test.go`（+201：合规 / 缺必填 / 非纯 JSON / 围栏 / 多余字段宽容 / 未声明零影响） |
| 前端 | `web/src/views/workflow/NodeInspector.vue`（llm 声明区，复用 SchemaFieldsEditor）；`web/src/views/workflow/graph.ts`（变量下拉 `{{key.field}}` 展开）；`web/src/api/workflow.ts`（类型） |
| 文档 | `docs/changelog/workflow/api_contract.md` spec 014 注记；`docs/testing/workflow-frontend-manual-test.md` §10 增补（44 行）；CLAUDE.md llm config 契约行；`specs/014.../tasks.md` 全勾选 |

## 6. 验收门

- 自动化：`go build ./... && go vet ./... && go test ./... -race -count=1` 全绿；前端 `npm run type-check && npm run build` 双门禁零报错。
- 人工：manual-test §10 走查（声明区编辑 / 下拉展开插入 / 校验失败定位 / 存量零回归）。
- 待人工项：`make start` 冒烟走 manual-test §10（真实模型试运行 §10.4、凭据排查）。
