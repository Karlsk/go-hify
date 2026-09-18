# Contract: Execute 接口（HTTP + 进程内共用）

**权威形状**: [api_contract.md §5](../../../docs/changelog/workflow/api_contract.md)（冻结）——本文为消费视角摘要 + 错误语义表，冲突以权威稿为准。

## 路由

`POST /api/v1/workflows/{id}/execute?trial=false`（auth 经 `/api/v1/*` 通用登录门槛；非流式 JSON 一次返回，成功 200）

## 请求（ExecuteWorkflowReq）

```jsonc
// body
{ "input": "查一下我的订单" }        // 必填，≤16384 字符 → vars["input"]
// query
?trial=true                          // 放开 draft/disabled（状态机唯一例外）
// 路径
{ id }                                // 目标 workflow
// 进程内调用方（chat，后续 spec）直传：ID / Input / ConversationID / MessageID / Trial
```

## 响应（RunResultSchema）

```jsonc
{
  "run_id": "123",                    // workflow_runs.id 字符串化；轨迹写入降级时为 ""
  "status": "succeeded",              // succeeded / failed
  "output": "终稿文本",                // end.output 渲染或末节点输出
  "duration_ms": 4200,
  "node_trace": [                     // 数组顺序即执行序；明细查轨迹表
    { "node_key": "classify", "node_type": "llm", "status": "succeeded", "duration_ms": 1800, "error_msg": "" }
  ]
}
```

## 错误语义（O4 二分法，冻结）

| 类别 | 触发 | HTTP | error.code |
|---|---|---|---|
| 把关 | draft / disabled 且非 trial | 503 | `WORKFLOW_NOT_PUBLISHED` |
| 把关 | 目标不存在 | 404 | `WORKFLOW_NOT_FOUND` |
| 请求 | input 缺失 / 超长 | 400 | `VALIDATION_FAILED` |
| 图缺陷 | condition 无命中出边、缺失变量运行期兜底、tool 节点未支持 | 400 | `VALIDATION_FAILED`（message 带 `node <key>:` 前缀） |
| 环境限制 | api 节点 SSRF 拦截、总时长超 5min | 500 | `WORKFLOW_EXECUTION_FAILED`（新哨兵） |
| 下游透传 | MODEL_NOT_FOUND / PROVIDER_BUSY / PROVIDER_UNAVAILABLE / RATE_LIMITED … | 既有映射 | 既有码（message 带 `node <key>:` 前缀） |

保存期新增：R10 模板引用违例 → 400 `VALIDATION_FAILED`（details 带节点 key 与引用名）。

## 行为要点（冻结语义）

- 同步纯函数：ctx 进、结果出；调用方取消 → 立即中止，错误带节点 key。
- 快照语义：执行开始一次性加载整图，进行中不受并发编辑影响。
- 试运行照常落 runs（is_trial=true）与 executions（LLM 成本真实发生）。
- 轨迹两层表收尾统一写（含失败路径）；写入降级不阻断（run_id 置空）。
