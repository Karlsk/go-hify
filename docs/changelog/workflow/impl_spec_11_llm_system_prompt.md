# Workflow 实现 spec 11：LLM 节点 system_prompt 支持（加法修订）

> 状态：**已实施并合入（2026-09-22）**——`3c82c3f` 单笔交付（代码 + 测试 + 冻结契约注记 + tasks 勾选）。规划产物 `specs/011-workflow-llm-system-prompt/`。本篇为回溯性 changelog（当时以 api_contract.md 与 impl_spec_06 的加法修订注记记录，无独立篇；应用户要求补齐），实现事实以代码与 specs/011-workflow-llm-system-prompt/ 为准。
> 上位契约：[api_contract.md](./api_contract.md) §3（LLM 节点 config 键集——本篇即其修订对象）、[impl_spec_06_execution_engine.md](./impl_spec_06_execution_engine.md)（callLLM 执行节——本篇修订其实际行为，两处注记均在 `3c82c3f` 内）。
> 前置依赖：spec 06 执行引擎已合入（callLLM / strict 渲染 / executions 自记）；spec 08 分型已合入（LLMConfig 为其既有结构）。
> 迁移假设：**零迁移、零新增第三方依赖、无新路由无新哨兵**——config 为 jsonb 加法键（`omitempty`），错误复用 `errs.ErrValidationFailed`。

## 1. 背景

LLM 节点此前只有 `prompt` 一条 user 消息：角色设定类内容只能拼进 prompt 头部，无法利用模型侧 system 通道；也是 spec 012 前端编辑器 System Prompt 输入（FR-004）的后端前置。性质定为**加法修订**：对既有图、既有测试、既有记录形态逐字节零影响。

## 2. 做什么（范围）

- `LLMConfig` 增可选 `SystemPrompt`（json `system_prompt`，`omitempty`，置于 Prompt 字段前）；`prompt` 仍必填，`Validate` 零新增拒绝路径（FR-001）。
- 模板渲染语义与 `prompt` 完全一致：strict `{{var}}` 替换、`{{base.field}}` 一级下钻、缺失报首个变量、`errs.ErrValidationFailed` 文案含变量名与节点 key（FR-002）。
- 执行期非空 → 渲染后作为 system 消息置于 user 消息之前，模型收到 `[system, user]`；空串 / 缺省 / null 三态合法且行为一致，均为单 user 消息现状（FR-003）。
- 渲染失败 fail-fast 于 `ResolveLLMConfig` 之前（零上游调用、零 executions 落库，pre-attempt 路径）（FR-004）。
- 排障记录有值才落：`node_in` 摘要与 executions 行 `Input` 含渲染后 `system_prompt`，无值不新增键（FR-005）；序列化往返对无值配置不引入该键（FR-006）。

## 3. 不做什么（边界）

- 不做 system 消息数组 / 多条 system 等形态扩展：单可选字符串 = 至多一条 system 消息。
- 不改保存期校验语义（`prompt` 必填不变；`system_prompt` 无必填无格式约束）。
- 不动既有测试断言与存量执行路径（SC-002：既有全量测试零改动全绿）。
- 不触 chat 模块（chat 管道的 system 语义不在本篇；workflow LLM 节点是单轮、不经 chat）。

## 4. 关键决策与保真不变量

| 决策 | 内容 |
|---|---|
| 加法修订而非新契约面 | `omitempty` 使空串 / 缺省 / null 三态收敛为同一行为——存量图天然兼容，无需数据回填、无需迁移。 |
| 渲染顺序 = system 先于 prompt，且都在 `ResolveLLMConfig` 之前 | 图缺陷 fail-fast 不浪费供应商额度（与既有 prompt 渲染同位同语义），明文 key 只在调用链瞬间存在的纪律不变。 |
| 记录有值才加键 | `executions.Input` / `node_in` 无值不引入新键——排障记录形态对存量逐字节不变（SC-004）。 |
| `recordExecution` 最小 diff 扩参 | 携带渲染后的 prompt + system 双值，不为记录形态改函数族结构。 |

**保真不变量**：空 `system_prompt` 的消息序列与现状逐字节一致；`Validate` 无新增拒绝路径；序列化往返零新键；错误码零新增。

## 5. 交付物（均在 `3c82c3f`）

| 层 | 交付物 |
|---|---|
| api | `internal/workflow/api/schema.go`（`LLMConfig.SystemPrompt` 字段；其余 hunks 为邻近结构 gofmt 对齐，无语义变化）；`schema_test.go`（+25：序列化往返带值 / 空串 / 缺省三态用例） |
| service | `internal/workflow/service/executor.go`（callLLM：system 渲染 → `[system, user]` 组装 → `recordExecution` 扩参）；`executor_test.go`（+104：消息序 SC-001、一级下钻 FR-002、fail-fast 零调用零落 SC-003、记录形态 FR-005、嵌套子图生效、空串等价缺省六组用例，stub client + stub store 零真实网络 / LLM） |
| 文档 | `docs/changelog/workflow/api_contract.md`（示例补 `system_prompt` + 加法修订注记）；`impl_spec_06_execution_engine.md`（callLLM 节行为注记）；`specs/011.../tasks.md` 9/9 勾选 |

## 6. 验收门

- 自动化：`go build ./... && go vet ./... && go test ./... -race -count=1` 全绿；workflow 模块覆盖率 ≥80% 维持；既有断言行零触碰（`git diff` 核对只增不改）。
- 冻结契约：`git grep -n 'system_prompt' internal/workflow` 确认新键只在有值路径出现。
- 下游消费：spec 012 FR-004 前端 System Prompt 输入（`9af8602` 已交付，读写同一 `config.system_prompt` 键）。
- 可选人工项：dev 栈冒烟一条带 system_prompt 的工作流，核对 executions 行 Input 含渲染后双键。
