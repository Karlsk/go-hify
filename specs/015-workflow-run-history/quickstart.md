# Quickstart: workflow 运行历史与节点轨迹查询

**Date**: 2026-09-24 | **Spec**: [spec.md](./spec.md) | **Plan**: [plan.md](./plan.md) | **Contracts**: [api.md](./contracts/api.md) / [frontend.md](./contracts/frontend.md)

验证目标：spec SC-001~SC-005 的端到端可跑场景。自动化证据由四层 `*_test.go`（stub / sqlmock / httptest 隔离）+ 全量门禁提供；本文是**真实服务起跑后**的 curl / 浏览器验证路径。

## 前置

1. 依赖就绪：PG / Redis 起（`make start ENV=dev`），`make migrate-status` 确认 20 条 applied（本篇零迁移）。curl 端口以 `.env` 的 `SERVER_PORT` 为准（本机 8081）。
2. 登录拿 cookie：`curl -c cookies.txt -X POST http://localhost:8081/api/v1/auth/login -d '{"username":"...","password":"..."}'`（以下 curl 均带 `-b cookies.txt`）。
3. 造数基础：一个可执行的工作流（task 型最简——单 end 节点即可试运行成功，无需真实模型）：
   ```bash
   curl -b cookies.txt -X POST http://localhost:8081/api/v1/workflows \
     -H 'Content-Type: application/json' \
     -d '{"name":"runs-冒烟","type":"task","start_node_key":"end","nodes":[{"key":"end","type":"end","config":{"output":"{{input}}"}}],"edges":[]}'
   ```
4. 造运行记录（每个场景按需）：`curl -b cookies.txt -X POST .../workflows/{id}/execute?trial=true -d '{"input":"hi"}'`——试运行走 trial 放开状态机，draft 态可跑（spec 013）。失败运行用指向不可达地址的 api 节点构造。

## 场景 A: 列表查询与摘要面（US1 / FR-001 / FR-002 / SC-003）

```bash
curl -b cookies.txt 'http://localhost:8081/api/v1/workflows/{id}/runs?limit=20'
```

**预期**：200 信封 `success:true`；`data.items` 数组，每项恰好 9 字段（id / status / trigger_source / is_trial / duration_ms / error_node / error_msg / started_at / created_at）——**无 input / output 键**（FR-002）；`data.limit=20`；`meta.has_more` + `meta.next_cursor` 正确；id 为字符串。空工作流 → `data.items: []`（非 null）。

## 场景 B: 翻页无重漏与 limit 归一（US1 / SC-002 / Edge Cases）

1. 对同一工作流试运行 25 次（循环 execute）→ `?limit=20` 首页 20 条 + `has_more:true`；回传 `&cursor=<next_cursor>` 第二页 5 条 + `has_more:false` + `next_cursor:null`；两页合计 25 条、id 无重复无遗漏、created_at 严格递减（最新在前）。
2. `?limit=0` / `?limit=-5` / `?limit=999` → 分别返回 `limit:20 / 20 / 100`，不报错（归一）。
3. `?cursor=篡改串` → 400 信封 `error.code:"VALIDATION_FAILED"`（游标不透明，原样回传是唯一合法用法）。

## 场景 C: 详情与节点轨迹（US2 / FR-003 / FR-004）

1. 成功运行（单 end 节点）→ `GET .../workflows/{id}/runs/{runId}`：200，全字段在（input / output / conversation_id=null / parent_run_id=null / trace_id / started_at / created_at / duration_ms），`nodes` 按执行序（end 节点一行，seq 递增）。
2. 失败运行（api 节点指向不可达地址）→ 详情 `status:"failed"`、`error_node` 指向 api 节点 key、`error_msg` 可读；`nodes` 只含失败前执行过的节点。
3. `GET .../workflows/{id}/runs/999999` → 404 `error.code:"RUN_NOT_FOUND"`；换**另一个工作流 B 的真实 runId** 查 A → 同样 404 同码（跨工作流不泄露存在性，两响应不可区分）。
4. 截断保真：构造超 16KB 输入试运行 → 详情 `input` 为截断后文本（含截断标记），不报错不空白。

## 场景 D: 前端区块（US1~US3 / FR-005 / FR-006）

浏览器 `http://localhost:5173/workflows/{id}`：

1. 详情页出现「运行历史」卡片（图编排卡片之后）；列表 / 加载更多 / 点行开运行详情抽屉（轨迹表按执行序、失败节点行高亮 = error_node 匹配）。
2. 刚试运行完的记录出现在列表顶部、带「试运行」标识；关闭试运行对话框后点开详情与当次结果一致（US3）。
3. 既有区块回归：基础信息 / Schema / 图编排 / 试运行按钮行为与 spec 013 后完全一致（FR-006 / SC-005）。
4. 人工步骤明细走 `docs/testing/workflow-frontend-manual-test.md` §11。

## 验证命令汇总

```bash
# 全量门禁（自动化证据主体）
go build ./... && go vet ./... && go test ./... -race -count=1
# workflow 模块覆盖率（≥80% 维持）
go test ./internal/workflow/... -race -cover
# 迁移状态不变（零迁移证据）
make migrate-status
# 依赖方向（workflow 无 chat import）
grep -r 'internal/chat' internal/workflow/ || echo OK
# 前端双门禁
cd web && npm run type-check && npm run build
```
