# Frontend Contract: 运行历史区块（spec 015）

**Date**: 2026-09-24 | **Spec**: [spec.md](../spec.md) | **Plan**: [plan.md](../plan.md) | **API**: [api.md](api.md)

前端交付面：`web/src/api/workflow.ts`（类型 + 2 方法）+ `web/src/views/workflow/WorkflowRunsPanel.vue`（**新增**组件，D6 裁定）+ `WorkflowDetail.vue` 挂载（一处）。既有区块（基础信息 / Schema / 图编排 / 试运行）**零触碰**（FR-006 / SC-005）；设计 token 全用既有（冻结合规）；零新增第三方依赖。

## 1. web/src/api/workflow.ts

新增类型（与既有同步执行结果类型 `NodeRunSummary` / `WorkflowRunResult` 区分命名——那两个是 execute 当次同步返回，本篇是落库历史查询）：

```ts
/** 运行历史列表项（摘要面，无 input/output——FR-002） */
export interface RunSummary {
  id: string
  status: 'succeeded' | 'failed'
  trigger_source: 'console' | 'chat' | 'workflow'
  is_trial: boolean
  duration_ms: number
  error_node: string
  error_msg: string
  started_at: string   // 「调用时间」列
  created_at: string
}

/** 轨迹行（按执行序 seq ASC） */
export interface NodeRunTrack {
  seq: number
  node_key: string
  node_type: string
  status: 'succeeded' | 'failed'
  input: string
  output: string
  error_msg: string    // 落库恒空，契约面保留
  duration_ms: number
}

/** 运行详情（全字段 + 轨迹） */
export interface RunDetail {
  id: string
  status: 'succeeded' | 'failed'
  trigger_source: 'console' | 'chat' | 'workflow'
  is_trial: boolean
  conversation_id: string | null
  message_id: string | null
  trace_id: string
  parent_run_id: string | null
  input: string
  output: string
  error_node: string
  error_msg: string
  duration_ms: number
  started_at: string
  created_at: string
  nodes: NodeRunTrack[]
}
```

新增方法（复用既有 `getCursorList` 游标约定——chat.ts 同款，meta.has_more / next_cursor 语义）：

```ts
/** 运行历史列表（keyset 游标，最新在前；触底/点击加载回传 cursor） */
export function listWorkflowRuns(id: string, params?: { limit?: number; cursor?: string }) {
  return getCursorList<RunSummary>(`/workflows/${id}/runs`, params)
}

/** 单次运行详情（含节点轨迹） */
export function getWorkflowRun(id: string, runId: string) {
  return get<RunDetail>(`/workflows/${id}/runs/${runId}`)
}
```

## 2. WorkflowRunsPanel.vue（新增组件契约）

**Props**: `workflowId: string`（必填）——组件内部自治：挂载时拉首页、翻页、打开详情、错误处理全自持；不向父 emit（只读展示）。

**区块结构**（详情页单列卡片流同款 `el-card` + `workflow-detail__card` 类）：

```text
el-card「运行历史」（含说明文案：受保留期约束，旧记录可能被清理——FR-004 预期管理）
├── 卡片头右侧「刷新」按钮（自治重拉首页；US3 试运行完成后手动刷新即见新记录——
│   试运行对话框零变化约束下的联动方案，Panel 不监听 TrialDialog 事件）
├── 空态（首次加载后 items 为空）：el-empty 文案「暂无运行记录」，非错误非空白
├── 运行列表 el-table（loading 态；行点击 → 打开详情抽屉）
│   列：状态（el-tag：succeeded 成功色 / failed 危险色——失败行可辨 US1-2）
│      触发来源（console 试运行 / chat 对话 / workflow 子工作流，中文映射）
│      是否试运行（is_trial → tag「试运行」）
│      耗时（duration_ms → 「1,234 ms」格式）
│      失败节点（error_node，空显 —）
│      错误摘要（error_msg，空显 —）
│      调用时间（started_at 可读格式）
│      运行编号（id 等宽字体）
└── 加载更多（hasMore 为 true 时展示按钮；点击回传 nextCursor 追加行；末页隐藏）
```

**运行详情视图 = `el-drawer`**（D6；size 大号，标题「运行 #<id>」）：

```text
├── 运行级信息（descriptions 形态）：状态 / 触发来源 / 是否试运行 / 总耗时 /
│   开始时间 started_at / 记录时间 created_at / 父运行编号（parent_run_id 非空时展示）/ 
│   关联会话与消息（chat 触发且非空时展示 conversation_id / message_id）
├── 错误区（status=failed 时）：错误文案 error_msg + 失败节点 error_node（危险色）
├── 输入 / 输出（大文本）：pre-wrap + 限高滚动；如实展示截断保真文本（US2-3，不加工不报错）
└── 节点轨迹 el-table（US2 核心）：
    列：序号 seq / 节点 key / 类型 / 状态（tag 两态）/ 耗时 / 输入 / 输出
    行按 seq 升序（执行序）；行高亮规则：row.node_key === run.error_node 且 status=failed → 危险色背景
    输入 / 输出单元格：截断显示 + tooltip 全文（或点击展开），空值显 —
    空轨迹（nodes 为 []）：表格区展示空态文案（Edge Case 防御路径，非报错）
```

**交互细则**：

- 打开抽屉时按需拉 `getWorkflowRun`（列表摘要不含大文本）；抽屉自带 loading；拉取失败 → 抽屉内错误态（可重试或关闭），不弹全局 toast 覆盖。
- 「加载更多」请求期间按钮 loading、防重复点击（spec 013 试运行同款纪律）。
- 404 `RUN_NOT_FOUND`（记录刚被保留期清理后点开）：抽屉内展示「该运行记录已不存在」空态，列表保持。

## 3. WorkflowDetail.vue 挂载（最小侵入）

图编排卡片（L76 的 `el-card`）之后追加一张卡片 + import，**其余零改动**：

```vue
<WorkflowRunsPanel :workflow-id="detail.id" />
```

`detail.id` 为既有详情数据（string 类型，与 API 层 id 字符串化对齐）；不新增路由、不改既有 script 逻辑（除 import 与组件注册）。

## 4. 验证面

- 自动门禁：`cd web && npm run type-check && npm run build`（SC-004）。
- 人工验收：`docs/testing/workflow-frontend-manual-test.md` §11 增补小节（列表 / 翻页无重漏 / 详情轨迹 / 失败高亮 / 空态 / 截断文本 / 试运行联动 / 既有区块回归）。
