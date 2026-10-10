# Quickstart: spec 018 验证场景

**Date**: 2026-10-09 | **Spec**: [spec.md](./spec.md) | **契约**: [contracts/jsonb-contract.md](./contracts/jsonb-contract.md)

## 前置

- 后端基线：`go build ./... && go vet ./... && go test ./... -race -count=1` 全绿（21 applied 迁移不变）
- 前端基线：`cd web && npm run type-check && npm run build` 全绿
- 本地起服务：`make start`（dev 模式；workflow 编辑页 = 控制台「工作流」菜单）

## 自动化验证（SC-005）

```bash
# 全量门禁
go build ./... && go vet ./... && go test ./... -race -count=1

# 本模块 + 覆盖率（≥80%）
go test ./internal/workflow/... -race -cover

# 关键回归（新用例锚点）
go test ./internal/workflow/api/ -race -run 'SchemaField|EndConfig' -v          # default 校验 / 行级校验 / 序列化 round-trip
go test ./internal/workflow/service/ -race -run 'AssembleInput|NormalizeInput' -v # 补默认 / 顶层归一化边界
go test ./internal/workflow/service/ -race -run 'BuildOutput' -v               # 构造 / 转义 / 类型处理 / 缺失变量
go test ./internal/workflow/service/ -race -run 'TemplateRefs' -v              # R10 覆盖 output_fields

# chat 零改动回归
go test ./internal/chat/... -race -count=1

# 前端门禁
cd web && npm run type-check && npm run build
```

## 端到端场景（对应 spec 手测 §1–§4，用户人工过）

### 场景 A：开始节点可视化（US1）

1. 新建 task 型 workflow 进画布 → 左侧固定「开始」节点默认渲染、不可拖/删/连边。
2. 点击 → 检查器出现 input_schema 表单（含默认值列）；加 `{keyword, string, required}` + `{limit, number, optional, default: 10}`；保存重进 → 角标「2 入参」。
3. chat 型 workflow → 开始节点点击显示只读「单一入参 {{input}}」。
4. 保存后 `GET /workflows/:id` → nodes 数组无 `__start__`（序列化零泄漏）。

### 场景 B：end 输出映射 + 多 end（US2）

1. 画 `llm1（声明 output_schema: summary/score）→ end1`；end1 检查器映射模式：`summary→{{llm1.summary}}`（推导 string）、`score→{{llm1.score}}`（推导 number）；保存。
2. 试运行 → 终稿为合法 JSON 对象 `{"summary":"...","score":...}`；构造 summary 含 `"` / 换文的输入再跑 → 终稿仍合法（转义正确）。
3. 加 condition 分流到 end2（映射 `route→{{input.route}}`）→ 试运行两分支：终稿分别为各自 end 产出；图级 output_schema = 并集（`score` 仅 end1 产出 → optional；两 end 共有字段 → required）。
4. 既有自由模板 end（存量 workflow）→ 打开检查器默认高级模式、原文呈现、行为不变。

### 场景 C：默认值三处生效（US3）

1. 场景 A 的 workflow 试运行 → 表单预填 `limit=10`；清空 limit 只填 keyword → 运行成功（`{{input.limit}}` 渲染 10，顶层归一化补回）。
2. 父 workflow 放子工作流节点（inputs 不给 limit）→ 子执行成功（assembleInputJSON 补默认）。
3. 配 required + default 保存 → 400 拦截。

### 场景 D：零回归（US4）

1. 存量 workflow（无 default / 自由模板 end / chat 型）编辑、试运行、发布、运行历史全部不变。
2. JSON 模式查看映射模式保存的 end → output_fields 原文可见可编辑；改 JSON 后切回画布按反解析正确呈现。
3. `make migrate-status` 恒 21 applied；`git status` 无 migrations/ 改动。

## 预期产出核对

- [ ] 后端全量门禁绿 + workflow 覆盖率 ≥80%
- [ ] 前端 type-check + build 绿（package.json / tokens.css 零改动）
- [ ] 上述四场景人工逐条通过（结果记入 docs/testing/ workflow 手测文档对应小节）
