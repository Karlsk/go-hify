# Quickstart: workflow 工具节点接 MCP（spec 016）验证场景

> 自动化验证为主（stub 隔离），人工冒烟见末节。命令均在仓库根执行。

## 前置

```bash
make migrate-status   # 预期 21 applied、下一号 00022（本篇零迁移）
```

## 1. 全量门禁

```bash
go build ./... && go vet ./... && go test ./... -race -count=1
```

预期：全 ok，无 FAIL。

## 2. 只跑本模块 + 覆盖率

```bash
go test ./internal/workflow/... -race -cover
```

预期：workflow/service 覆盖率 ≥80%；tool 节点新用例组全绿（正常链路 / 工具缺失 404 / 调用失败 500 / args 缺失 400 / DB 错 500 / nil 装配 500 / 图级 llm→tool→end / condition 按工具输出路由）。

## 3. 关键回归单测（指定运行）

```bash
go test ./internal/workflow/service/ -race -run 'TestExecute|TestExecutor' -v 2>&1 | grep -i tool
go test ./internal/chat/... -race -count=1
```

预期：chat 全量零改动回归绿（管道透传受益证据）。

## 4. 依赖红线 grep

```bash
grep -rn 'internal/mcp/service' internal/workflow/ ; grep -rn 'modelcontextprotocol/go-sdk' internal/ | grep -v internal/mcp/service ; grep -rn 'cloudwego/eino' internal/ | grep -v platform/llm
```

预期：三条全空（test 除外）。

## 5. 人工冒烟（用户执行，等真实 MCP server）

```bash
make start   # dev 模式起本地栈
```

1. 注册一个真实可达的 MCP server（如文件系统 / fetch server），discover 入库工具。
2. JSON 模式创建 workflow：`start(x) → tool(tool_id, args: {"q": "{{start.x}}"}) → llm(prompt 含 {{tool_key}}) → end`，发布。
3. 直接执行（试运行 / API）或经绑定 Agent 对话触发：断言工具被调用、终答含工具输出、运行详情节点轨迹含 tool 步（input=tool_id+args、output=内容）。
4. 停用该 MCP server 后再执行：断言 404 MCP_TOOL_NOT_FOUND、错误定位到 tool 节点 key。
5. 改 args 引用不存在的变量：断言 400 VALIDATION_FAILED、`args.<参数名>` 定位。
