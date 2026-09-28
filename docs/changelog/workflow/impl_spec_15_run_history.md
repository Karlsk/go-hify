# Workflow 实现 spec 15：运行历史与节点轨迹查询

> 状态：**已实施（2026-09-24）**——规划产物 `b68388a`（`specs/015-workflow-run-history/` 七件套）+ 实现 `b390480` 分两笔提交，本篇为回溯性 changelog，实现事实以代码与 specs/015-workflow-run-history/ 为准。
> 上位契约：[api_contract.md](./api_contract.md)（§1 路由总表后两行 / §5「GET runs 运行历史查询」/ §8 RUN_NOT_FOUND / §9 游标回传，均带 2026-09-24 spec 015 注记）、[impl_spec_06_execution_engine.md](./impl_spec_06_execution_engine.md)（workflow_runs / workflow_node_runs 两表落库与截断保真边界）、[impl_spec_13_agent_bind_trial.md](./impl_spec_13_agent_bind_trial.md)（试运行入口，US3 联动对象）。
> 前置依赖：迁移 00019/00020（runs 两表与索引）已 applied；spec 07/08 执行落库链路、spec 013 试运行与详情页已合入。
> 迁移假设：**零迁移**（`make migrate-status` 20 applied、下一号 00021 全程不变）；执行引擎与落库链路零改动、既有 8 端点零变化、零新增第三方依赖。

## 1. 背景

执行侧自 spec 07/08 起每次调用完整落库两表（workflow_runs / workflow_node_runs），但查询面从未打开：无 runs 查询端点，前端试运行对话框的 node_trace 只是当次同步返回的摘要，历史调用无处可查——排障（第 N 次调用哪个节点失败、当时输入输出是什么）与审计缺入口。本篇开两个只读查询端点并在详情页加「运行历史」区块，把已有数据暴露出来：零迁移、执行引擎与落库零改动。

## 2. 做什么（范围）

- `GET /api/v1/workflows/{id}/runs` 运行历史列表：keyset 游标分页（排序键 `(created_at, id)` 双键行值比较、最新在前；limit 缺省 20、≤0 归一 20、>100 封顶 100；篡改 / 解码失败的 cursor → 400 `VALIDATION_FAILED`）；列表项为摘要面 9 字段（id / status / trigger_source / is_trial / duration_ms / error_node / error_msg / started_at / created_at），**不含 input/output 大文本**（store 显式列清单 `selectRunSummary`）；空列表 `[]` 非 null（FR-001/FR-002）。
- `GET /api/v1/workflows/{id}/runs/{runId}` 运行详情：run 全字段（含 input/output 大文本原样透传——截断标记如实返回不加工；conversation_id / message_id / parent_run_id 三可空 id 字符串化）+ `nodes` 节点轨迹按执行序（`seq ASC`，空轨迹 `[]`）；run 不存在与属于其他工作流**同判 404 `RUN_NOT_FOUND`**（store 双条件 `workflow_id = ? AND id = ?`，不泄露存在性——本篇唯一新哨兵）；runId 非数字 → 400（FR-003/FR-004）。
- 前端详情页新增 `WorkflowRunsPanel`「运行历史」卡片（图编排卡片之后）：列表（状态 tag / 触发来源中文 / 试运行 tag / 耗时 / 失败节点 / 错误摘要 tooltip / 调用时间 / 运行编号）+ 加载更多 + 空态 + 保留期说明文案 + 「刷新」按钮（US3：试运行对话框零变化约束下的联动方案，Panel 自治重拉首页）；行点击开 el-drawer 运行详情（按需拉取、skipErrorToast 静默、运行级信息条件字段 / 失败面 error alert + 失败节点危险色 / 输入输出 pre-wrap 限高滚动 / 轨迹表 7 列 + 失败行高亮 = `row.node_key === run.error_node && status=failed` / 404 抽屉内空态「该运行记录已不存在」不弹全局 toast）（FR-005/FR-006）。
- 全链路只读：无修改 / 删除 / 重试 / 取消操作（FR-007）；既有 8 端点、执行引擎、落库行为零变化（FR-008）。

## 3. 不做什么（边界）

- 不做运行中实时进度查询（runs 无 RUNNING 态，既有决策已否决可变态）；不做按 parent_run_id 反查子 run 列表（低频排障路径，详情仅透出父运行编号值）。
- 不做列表筛选 / 搜索 / 时间范围过滤 / 统计聚合；不做跨工作流的全局运行历史页；不做按用户隔离（一期既有约定）。
- 不改执行引擎与落库行为：节点级 error_msg 落库恒空为既有形态（错误定位 = 运行级 error_msg + error_node 高亮）；输入输出 16KB 截断保真为落库边界既有行为，详情如实展示截断文本。
- 保留期清理（默认 365 天可配）导致的空列表 / 404 属正常态非错误（FR-004 数据窗口语义）。

## 4. 关键决策与保真不变量

| 决策 | 内容 |
|---|---|
| D1~D7 冻结（research.md） | ① api 层游标只是 string，不 import platform/page（page 工具全在 service 层）；② 排序键 `(created_at DESC, id DESC)` 双键 keyset；③ GetRunByID 双条件同判 404 不泄露存在性；④ 显式列两档（`selectRunSummary` 10 列 / `selectRun` 17 列 / `selectNodeRun` 11 列，禁 SELECT \*）；⑤ 轨迹 `(run_id, seq ASC)`、空轨迹 `[]`；⑥ 前端独立子组件 + el-drawer；⑦ limit 归一（≤0→20、>100→100）+ DecodeCursor 失败 → 400。 |
| 404 固定文案 | handler 对 ErrRunNotFound 用 `respond.Fail(404, "RUN_NOT_FOUND", "运行记录不存在")` 固定中文 message——偏离 failWorkflow 既有 `err.Error()` case 是 tasks.md 明文要求（跨工作流与不存在响应逐字节不可区分，chat 先例同款）。 |
| US3 联动方案 | 「刷新」按钮（Panel 自治重拉）而非 TrialDialog emit——FR-006 试运行区块零变化的严格实现；loadFirstPage 头部 `if (loading) return` 防重。 |
| 契约偏差（1 处） | `getWorkflowRun(id, runId, config?)` 实际签名带可选第三参 `AxiosRequestConfig`——契约 frontend.md 方法签名无此参，但「不弹全局 toast」交互细则在 request.ts 架构下必须透传 skip config（auth.ts 同款先例）；REST 契约与类型形态不动。 |
| store 包头注释修正 | 「三张表」→「五张表」（spec 06/08 已扩五表、注释滞后，本篇触碰该文件时最小同步，纯注释对齐非契约改动）。 |

**保真不变量**：既有 8 路由 handler 用例零改动全绿（SC-005 自动化守护面）；`grep -r 'internal/chat' internal/workflow/` 零命中；详情页既有区块（基础信息 / Schema / 图编排 / 试运行入口）仅 +4 行（import + 挂 Panel），其余零变化。

## 5. 交付物

| 层 | 交付物 |
|---|---|
| api | `internal/workflow/api/errors.go`（`ErrRunNotFound`，本篇唯一新哨兵）；`api.go`（接口增 `ListRuns` / `GetRun`）；`schema.go`（五查询类型：ListRunsReq / GetRunReq / RunSummarySchema / RunDetailSchema / NodeRunSchema）+ `schema_test.go`（+112：九字段摘要面 JSON 无 input/output 键等契约断言） |
| service | `internal/workflow/service/service.go`（两查询实现：游标归一 / 解码、NotFound 翻译 ErrRunNotFound、run + 轨迹分次组装不 JOIN、model→schema 转换）+ `service_test.go`（+363：21 条 has_more 边界 / 404 短路不查轨迹 / 空列表 []） |
| store | `internal/workflow/store/store.go`（`selectRunSummary` / `selectRun` / `selectNodeRun` 显式列常量 + ListRuns keyset / GetRunByID 双条件 / ListNodeRuns seq ASC；包头注释三表→五表修正）+ `store_test.go`（+174：sqlmock 显式列与 WHERE 断言） |
| handler | `internal/workflow/handler/handler.go`（2 条 GET 路由 + getRun 两路由参数 strconv 注入 + ErrRunNotFound→404 固定中文 message）+ `handler_test.go`（+188：200 信封与 CursorMeta / limit 归一 / 篡改 cursor 400 / 404 码与文案断言） |
| 前端 | `web/src/views/workflow/WorkflowRunsPanel.vue`（**新组件**，378 行）；`WorkflowDetail.vue`（+4 挂载）；`web/src/api/workflow.ts`（RunSummary / NodeRunTrack / RunDetail 类型 + listWorkflowRuns / getWorkflowRun） |
| 文档 | `api_contract.md` §1/§5/§8/§9 四处 spec 015 注记；CLAUDE.md 资源清单 workflows 行 + 错误码表 RUN_NOT_FOUND 行；`docs/testing/workflow-frontend-manual-test.md` §11 增补（46 行，六小节 + 通过标准）；`specs/015.../tasks.md` 28/28 勾选；quickstart 端口勘误（8080→8081，与 `.env` SERVER_PORT 对齐） |

## 6. 验收门

- 自动化：`go build ./... && go vet ./... && go test ./... -race -count=1` 全绿（零 FAIL 行）；workflow 模块覆盖率四包全 ≥80%（api 97.6% / handler 86.8% / service 89.1% / store 97.0%）；`make migrate-status` 20 applied 不变；依赖方向 grep 零命中；前端 `npm run type-check && npm run build` 双门禁零报错。
- 人工：quickstart 场景 A~D（列表摘要面 / 翻页无重漏与 limit 归一 / 详情轨迹与跨工作流 404 不可区分 / 前端区块）+ manual-test §11 走查。
- 待人工项：`make start` 冒烟走 manual-test §11（列表 / 翻页 / 失败高亮 / 抽屉轨迹 / 404 空态 / 试运行联动刷新 / 既有区块回归）；场景 C.4 超 16KB 截断保真需真实起跑。
- 实施期排障实录（2026-09-24）：`make start` 旧进程占端口导致健康检查假阳性、新后端静默退出，浏览器请求打到 spec 014 时代旧二进制报 runs 路由 404——清理三个孤儿进程（周一旧后端 + 两个旧 vite）重启后验证通过；start.sh 端口占用检测缺陷已另列改进项，不在本篇范围。
