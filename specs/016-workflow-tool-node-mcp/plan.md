# Implementation Plan: workflow 工具节点接 MCP（统一 tool.Source 执行）

**Branch**: `016-workflow-tool-node-mcp` | **Date**: 2026-09-30 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/016-workflow-tool-node-mcp/spec.md`；上位实现 spec [docs/changelog/workflow/impl_spec_16_tool_node_mcp.md](../../docs/changelog/workflow/impl_spec_16_tool_node_mcp.md)（D1–D5 已拍板）

## Summary

把 executor 里 tool 节点的占位 fail-fast（图缺陷 400「mcp 未建」，spec 06 时代产物）换成真实执行：新增消费方窄接口 `toolSourceFactory`（`ToolSourceFor(mcpapi.ToolSourceReq) tool.Source`——Go 结构化类型天然满足 mcpapi.MCPClientService，组合根注入），`callTool` 分支按 ToolID 解析工具 → args 逐值严格 `{{var}}` 渲染（缺失 = 图缺陷 400 带 `args.<k>` 定位）→ 单次确定性 Invoke → `ToolResult.Content` 落变量池 + 节点轨迹。错误分型 fail-fast（区别于 chat 的错误即上下文）：工具缺失/server 停用 → `mcpapi.ErrMCPToolNotFound` 404 透传；调用失败 → `mcpapi.ErrMCPToolCallFailed` 500（message=Content 截断）+ run failed + error_node；装配缺位 → 同码「工具来源未注入」。零迁移（21 applied 不变）、零新哨兵/路由/SSE 事件、chat 零改动（管道透传受益）；spec 06 旧 fail-fast 断言（executor_test.go:575 / execute_test.go:257）由本篇授权替换。

## Technical Context

**Language/Version**: Go 1.26（后端单二进制）

**Primary Dependencies**: Gin + GORM（既有）；`internal/platform/tool` 统一工具契约与 `internal/mcp/api` 公开契约（前置批 `0d6293f` 已交付）。零新增第三方依赖。

**Storage**: PostgreSQL 17——零迁移（`make migrate-status` 21 applied、下一号 00022 留给后续；mcp_tools / mcp_servers 表由 mcp 批 00021 落地，本篇只读消费不碰）。工具调用不落 executions（非 LLM 调用），节点轨迹沿用 workflow_node_runs 既有 16KB 单值截断。

**Testing**: `go test ./... -race -count=1`，同包 `*_test.go`；stub 隔离（exec_doubles_test.go 增 stubToolSourceFactory，零真实网络 / PG / MCP server）；`workflow/service` 覆盖率 ≥80%；chat 模块全量回归绿（零改动证明透传受益）。

**Target Platform**: Docker Compose 单机（后端单二进制 alpine）

**Project Type**: 模块化单体（workflow 模块 service 层 + 组合根）

**Performance Goals**: 单 tool 节点每次执行 = ToolSourceFor + Tools（ListToolsByIDs + ListServersByIDs 两查询）+ 一次 MCP 调用——已知成本，workflow 执行频率极低（对话触发），不做缓存（Config 语义不同，勿乱加）

**Constraints**: 依赖红线——workflow 只 import `mcp/api`（依赖清单既有项），禁止 `mcp/service`；go-sdk 不出 mcp/service、eino 不出 platform/llm（既有纪律 grep）；service 全程无 gin 类型；ToolConfig 冻结契约不动（D5 拍板 A：Args 值域仅字符串）

**Scale/Scope**: 3 源文件改动（executor.go / service.go / server.go）+ 2 测试文件改动（executor_test.go / execute_test.go）+ 1 测试替身文件追加（exec_doubles_test.go）

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| 原则 | 判定 | 依据 |
|---|---|---|
| I. Simplicity First | ✅ PASS | 复用 chat 批已落地的 tool.Source 消费模式（窄接口 + 组合根注入），零新抽象零新依赖；不做清单（编辑器面板/进度事件/会话复用/executions 落库）在 spec Assumptions 留痕；nil 装配走 fail-fast 而非静默降级——最小且诚实 |
| II. 模块化单体与单向依赖 | ✅ PASS | workflow → mcp 是 CLAUDE.md 依赖清单既有项，且只 import `mcp/api`；跨模块传参只用 mcpapi 契约类型（ToolSourceReq）与 platform/tool 契约（ToolDef/ToolCall/ToolResult）；无环、无反向 import；service 无 gin 类型 |
| III. 统一 LLM 接入层 | ✅ PASS | 不涉新 LLM 调用（工具调用经 tool.Source，非 LLM）；不新增超时/重试逻辑（MCP 调用侧超时属 mcp 模块内部既有行为，workflow 不越界包） |
| IV. SSE 链路 | ✅ PASS | 不涉 SSE（执行引擎单次返回；chat 管道既有 SSE 零改动） |
| V. 数据库纪律 | ✅ PASS | 零迁移零新表；无 SQL 改动（工具解析经 mcpapi 既有查询）；不落 executions（非 LLM 调用，与 chat 工具循环同语义） |
| VI. 可观测与成本护栏 | ✅ PASS | 节点轨迹记 input/output/duration（既有机制）；错误分类沿用哨兵链路（store→service→handler 三层不吞错不变） |
| VII. 统一契约 | ✅ PASS | 零新错误码——MCP_TOOL_NOT_FOUND / MCP_TOOL_CALL_FAILED 均为既有哨兵（mcp/api），workflow 仅透传；ToolConfig 冻结契约逐字不动；nodeIn 记 `{"tool_id","args"}` 对齐 callWorkflow inputs 形态 |

## Project Structure

### Documentation (this feature)

```text
specs/016-workflow-tool-node-mcp/
├── plan.md              # 本文件
├── research.md          # Phase 0：五项设计决策记录（对齐上位 spec D1–D5）
├── data-model.md        # Phase 1：零新实体——ToolConfig/工具来源/节点轨迹行为面
├── quickstart.md        # Phase 1：验证场景
├── contracts/           # Phase 1：errors.md（错误分型契约——本篇唯一对外可观察面）
└── tasks.md             # Phase 2（/speckit-tasks 产出）
```

### Source Code (repository root)

```text
internal/workflow/service/
├── executor.go               # toolSourceFactory 窄接口 + callTool 分支（替换占位 fail-fast）：
│                              #   Tools 空 → ErrMCPToolNotFound 透传 / DB 错 %w /
│                              #   args 严格渲染（缺失 → ErrValidationFailed, args.<k> 定位）/
│                              #   渲染后按 key 排序 marshal / Invoke IsError → ErrMCPToolCallFailed /
│                              #   成功 setNodeIn+落池+Output / nil 装配 → ErrMCPToolCallFailed「工具来源未注入」
├── service.go                 # New 增第 9 参 toolSources（rags 后 blockPrivate 前）+ newExecutor 透传
├── exec_doubles_test.go       # stubToolSourceFactory（可控 Tools 清单/错误、Invoke 结果）
├── executor_test.go           # tool 用例组六类 + 替换 :575 旧断言
└── execute_test.go             # 图级端到端（llm→tool→end / condition 路由）+ 替换 :257 旧断言

internal/app/
└── server.go                  # workflowSvc.New 追加 mcpClientSvc（零重排——mcp 构建先于 workflow）
```

**Structure Decision**: 模块化单体四层不动——本篇只碰 workflow/service 执行器 + 组合根装配；store/handler/api 层零改动（无新查询、无新路由、无新契约类型）。

## Complexity Tracking

> Constitution Check 全 PASS，无 violation 需登记。
