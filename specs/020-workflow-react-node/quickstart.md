# Quickstart: spec 020 验证场景

**Date**: 2026-10-10 | **Spec**: [spec.md](./spec.md) | **契约**: [contracts/node-contract.md](./contracts/node-contract.md)

## 前置

- 后端基线：`go build ./... && go vet ./... && go test ./... -race -count=1` 全绿（21 applied）
- **迁移**：`make migrate-up` 应用 00022 → `make migrate-status` 22 applied（e2e 与手测前提；单测不依赖）
- 前端基线：`cd web && npm run type-check && npm run build`
- 手测造数：一个 enabled mcp server（≥1 工具）+ 一个 enabled KB（含已入库文档）+ 可用 chat 模型；`make start`

## 自动化验证（SC-004）

```bash
make migrate-up && make migrate-status          # 预期 22 applied
go build ./... && go vet ./... && go test ./... -race -count=1
go test ./internal/workflow/... ./internal/mcp/... -race -cover   # 两模块各 ≥80%
go test ./internal/chat/... -race -count=1      # ToolSourceReq 加法零影响证明
go test ./internal/mcp/store/ -race -run ListToolsByServerIDs -v
go test ./internal/workflow/service/ -race -run 'React|RetrieveQuery' -v
cd web && npm run type-check && npm run build
# 纪律 grep
grep -rn "cloudwego/eino" internal/ --include="*.go" | grep -v platform/llm | grep -v _test   # 空
go list -deps ./internal/workflow/service | grep Karlsk   # 仅依赖清单内模块 api
```

## 端到端场景（对应手测 §13 三节，用户人工过）

### 场景 A：react 节点自主工具调用（US1）

1. 画布拖入 react 节点：模型下拉可选、server 多选勾一个 enabled server、Prompt 填 `任务：{{input}}`、max_rounds 留 0（默认 8）；保存 → `GET` 详情 config 键集对齐 C1（model_id 字符串、server_ids 数字数组、无 max_rounds 键）
2. 试运行触发工具调用 → 终答落池；end 映射 / 下游 llm 引用 `{{reactKey}}` 成功；运行详情 node_runs：react 单行，input 含 tool_chain（逐调用 round/id/name/args/is_error）、output = 终答
3. executions 表：react 内每轮 LLM 各一行（conversation_id NULL、input 含 round 与 tool_calls）
4. max_rounds=1 + 诱导持续调用 → 第 2 轮 nudge 收尾、终答照落（HitLimit 非错误）
5. 停用该 server 再试运行 → 404 `MCP_TOOL_NOT_FOUND`（fail-fast，error_node 定位 react 节点）
6. prompt 引用非祖先变量保存 → 400 带节点 key 与引用名（R10 含 system_prompt 同验）

### 场景 B：ragNode 先提炼后检索（US2）

1. 画布拖入 knowledge_retrieval：KB 下拉（仅 enabled）、top_k 填 3、query 填 `{{llm1.topic}}`；保存试运行 → 检索按渲染问句命中（nodeIn query = 渲染值）
2. 存量无 query 节点（JSON 模式造）→ 试运行 query 回退 input（行为同升级前）
3. query 填 `{{nope}}` 保存 → R10 400；query 渲染为空串（模板 `{{llm1.empty_field}}` 缺字段）→ 运行期 strict 400 带定位

### 场景 C：tool 节点画布闭环（US3）

1. 画布拖入 tool 节点：工具下拉按 enabled server 分组单选 → config.tool_id 字符串保形；args 行填 `q={{input}}` → 保存试运行调用成功、nodeIn args 渲染值
2. 存量 JSON 形态 tool 节点进编辑页 → 表单回显工具名与 args 行如初
3. 空键 args 行不产出；全空删 args 键（序列化核对）

### 场景 D：零回归（US4）

1. chat 绑工具 Agent 多轮工具对话复跑（019 验收同款）→ 帧序 / 落库 / executions 零差异
2. 既存七类节点各跑一次存量图 → 行为零变化；palette 既存五类拖拽 / 起始三入口 / 序列化零泄漏复测

## 预期产出核对

- [ ] 22 applied + 全量门禁绿 + 两模块覆盖率 ≥80% + 前端双门禁绿
- [ ] 场景 A/B/C/D 人工逐条通过（结果记入 workflow-manual-test §13 与 chat-manual-test 复跑记录）
