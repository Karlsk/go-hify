# Quickstart: 工作流管理前端验证走查（009-workflow-frontend）

**Date**: 2026-09-22 | **Status**: Complete | **Plan**: [plan.md](plan.md)

本篇实现后的端到端验证指南。详细人工场景清单在 [docs/testing/workflow-frontend-manual-test.md](../../../docs/testing/workflow-frontend-manual-test.md)（FR-013④，实现期交付），此处给最短路径冒烟。

## 前置

1. **后端**：`make start ENV=dev`（依赖 PG + Redis 起）；确认 `curl -s localhost:8080/health` 正常。
2. **测试数据**：至少 1 个 provider + 1 个 chat 模型（llm 节点下拉数据源需要）；按 [docs/testing/workflow-manual-test.md](../../../docs/testing/workflow-manual-test.md) §4 既有示例可先造 1~2 条工作流（列表页数据）。被 Agent 绑定的工作流一条（测删除 409 WORKFLOW_IN_USE）——无则该场景以 curl 验证 409 响应形态。
3. **前端**：`cd web && npm install && npm run dev`（@vue-flow/* 三包为新增依赖，首次需安装）。

## 门禁命令（SC-001 / SC-002）

```bash
cd web && npm run type-check && npm run build   # 预期：零错误退出
go build ./... && go vet ./...                  # 预期：全绿（回归，后端零改动）
git status --short                              # 预期：不出现 internal/ 与 migrations/ 路径
```

## 最短冒烟（SC-004 核心路径）

1. **登录** → 侧边栏出现「工作流管理」→ 进入 `/workflows`：表格列出名称/类型/状态/创建时间，既有数据行三态正确（草稿/已发布/已停用 tag）。
2. **JSON 模式创建**：点「新建工作流」→ 创建页表单就绪、JSON 编辑器预填智能客服分类示例 → 改名称 → 提交 → 成功提示 + 跳回列表 + 新行出现（draft）。
3. **非法 JSON 阻断**：编辑器输入非法 JSON → 点「格式化」→ 错误提示且原文不变；直接提交 → 阻断；非法状态下切「拖拽模式」→ 阻断并提示先修复。
4. **拖拽模式创建**：切拖拽模式（空画布）→ 左面板拖入 llm + end 节点 → 拖连线 → 点 llm 节点右侧面板选模型、填 Prompt → 切回 JSON 模式核对等价配置 → 提交成功。
5. **生命周期**：列表对新行点「发布」确认 → tag 变「已发布」；点「停用」确认 → 变「已停用」；再「发布」→ 回「已发布」。
6. **删除**：未绑定行删除确认 → 行消失；被绑定行删除 → WORKFLOW_IN_USE 提示、行保留。

预期结果：全部场景通过 = SC-004/SC-006 人工项过；SC-005（未登录跳登录）随手可验（退出后访问 /workflows）。

## 详细场景

完整 11 项 Edge Cases 走查（起始节点删除迁移、chat 型隐藏 schema、未知键透传、模型下拉空提示、子工作流只列 task 型、停用后果文案、409 双场景）见 [docs/testing/workflow-frontend-manual-test.md](../../../docs/testing/workflow-frontend-manual-test.md)。

## 验收门（对齐 spec SC-001~SC-006）

| SC | 验证方式 |
|---|---|
| SC-001 | 门禁命令第一行全绿 |
| SC-002 | 门禁命令后两行（go 回归 + git status 无后端路径） |
| SC-003 | `grep -rn '#[0-9a-fA-F]\{3,8\}\|rgb(' web/src/views/workflow/ web/src/api/workflow.ts` 无硬编码色值 |
| SC-004 | 本文档冒烟 + 详细场景走查 |
| SC-005 | 未登录访问 /workflows、/workflows/create 跳登录；既有路由抽查 |
| SC-006 | 冒烟步骤 4 的往返核对（含手写 JSON 加未知键后往返保留） |
