# task 型 workflow 工具形态备忘录（Option 2：每个 workflow 一个虚拟工具）

> 性质：**备忘录**（非 spec）——记录已收敛的工具形态候选，供工具篇直接消费；正式冻结归工具篇拍板。
> 日期：2026-09-18。来源：spec 07/08 规划讨论（spec 05 §1.1 E1 触发形态的延伸决策）。

## 背景与前置

- E1 触发形态分两路：**管道形态**已落 spec 07（chat 管道接线——绑定 agent 的消息确定性先过 workflow）；**工具形态**（workflow 包装为模型可调用的虚拟工具，复用 tool_call / tool_result SSE 事件）前置 **chat 工具循环**（现状 ToolIDs 读到不执行）与 **mcp 执行能力**（mcp 模块 api 仍为占位），未排期。
- 分型（spec 08）落地后，仅 **task 型**可注册为虚拟工具（chat 型是对话终答语义，不可工具化）。

## 已收敛候选：Option 2——每个 workflow 一个虚拟工具

- **工具名**：workflow.name slug 化（命名冲突与字符集规则归工具篇拍板）。
- **工具描述**：workflow.description（作者可控模型行为的第一入口）。
- **参数 schema**：由 `workflow.input_schema` 生成（spec 08 O7a 已落地列与简化形态 `[{name, type, required, description}]`，type ∈ string/number/boolean）；未声明 schema 的 workflow 回退 `{input: string}`（spec 06 O1 单一入参终形）；schema 增改时工具 schema 跟着长，纯增量。
- **授权语义**：绑定 = 授权，粒度到 workflow——与 `agent_tools`（粒度到工具不到 server）同构；绑定关系表形态（泛化 agent_tools 或新关联表）归工具篇拍板。
- **注入链路**：agent Tools → `llm.CallOptions.Tools`，与 MCP 工具同一条注入 / 执行 / 授权链路——工具循环只建一次，双生态通吃。

## 被否选项：Option 1——内置单一 task 工具

一个内置 `run_task` 工具（参数 `{workflow: 名字, input}`，描述动态列可用清单）。否决理由：

- 间接寻址（名字 → 目标）是模型幻觉温床，叫错名即失败；
- 权限粒度粗：绑了就能跑**所有** task，违背仓内「绑定 = 授权」原则；
- 动态清单 = schema 不稳定，token 占用换不来确定性。

## 届时一并拍板的关联项

- task 型才可注册为工具（spec 08 `type` 列的第三个消费者）；
- agent 绑定按类型把关（管道仅 chat 型 / 工具仅 task 型——spec 07/08 显式留到工具篇）；
- 嵌套深度与工具调用的组合边界（工具触发的 run 深度计入 spec 08 O4 上限）。
