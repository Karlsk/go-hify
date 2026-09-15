# Workflow 实现 spec 04：service + handler + 组合根装配（CRUD / publish / disable）

> 状态：**待实施**（2026-09-15 定稿；供 rdp-implementation 以 TDD 消费）。
> 上位契约：[api_contract.md](./api_contract.md)（全文——本篇是它的 HTTP 落地）、[db_model.md](./db_model.md) §6 状态机 / §7 图校验。冲突时停下来问用户。
> 前置依赖：spec 01 / 02 / 03 全部合入（本篇消费 model、api 全部类型与 Store 接口）。
> 规范引用（实施逐条对照）：CLAUDE.md《接口规范》——路径与版本（资源复数 / 动作子路径）、统一响应信封、字段命名与类型、空值约定、分页 B 模式、错误处理（哨兵 → 状态码映射表）、认证；《代码组织规范》——handler 薄绑定、service 职责（错误翻译 / schema↔model 转换 / 事务边界语义）、组合根步骤与依赖方向（workflow → provider, rag, platform；**禁依赖 chat**）。

## 1. 交付物

| # | 交付物 | 路径 |
|---|---|---|
| 1 | 业务实现（7 方法 + 图校验条 9 + 缓存 + 错误翻译） | `internal/workflow/service/service.go` |
| 2 | HTTP 层（7 路由 + 绑定函数） | `internal/workflow/handler/handler.go`（替换占位） |
| 3 | 缓存名注册 | `internal/platform/cache/cache.go`：`NameWorkflow = "workflow-cache"` + `DefaultConfig` TTLs 项（DefaultTTL 30min） |
| 4 | 组合根装配 | `internal/app/server.go`：workflow 段（rag 之后、chat 之前）+ 路由注册 + 更新两处「mcp / workflow 后续批次再接入」注释为仅 mcp |

**范围红线**：本期不实现 execute 路由与 `WorkflowService.Execute`（执行引擎后续 spec）；`ErrWorkflowNotPublished` 哨兵与 handler 映射本期登记、路由后续挂。

## 2. 冻结契约

### 2.1 service

```go
// cacheManager 窄接口：方法集与 agent/service/service.go 的 cacheManager 逐字对齐
// （platform/cache.Manager 的读/写/删三动作），测试可整体 stub。
type cacheManager interface { /* Get / Set / Del —— 对齐 agent 同名接口 */ }

type workflowService struct {
    store Store                        // spec 03 接口，store 包实现
    models providerapi.ModelService    // llm 节点 model_id 存在性预检
    kbs    ragapi.KnowledgeBaseService // knowledge_retrieval 节点 KB 预检
    cache  cacheManager
}

// New 返回 api 接口；组合根将返回值注入 handler（及将来执行器/chat 消费方）。
func New(store Store, models providerapi.ModelService, kbs ragapi.KnowledgeBaseService,
    cm cacheManager) workflowapi.WorkflowService
```

import 别名：`workflowapi "github.com/Karlsk/go-hify/internal/workflow/api"`、`providerapi` / `ragapi` 同款；**不出现 gin 类型**。

### 2.2 错误翻译表（service 边界，实现 api 接口处）

| 来源 | 翻译为 | handler 映射 |
|---|---|---|
| `store.GetByID` → `gorm.ErrRecordNotFound` | `workflowapi.ErrWorkflowNotFound` | 404 |
| `store.Create/ReplaceGraph` → PG 23505（`errors.As *pgconn.PgError`，对齐 agent `isUniqueViolation` 写法） | `workflowapi.ErrWorkflowNameConflict` | 409 |
| `store.ReplaceGraph/Delete` 返回 false | `workflowapi.ErrWorkflowNotFound` | 404 |
| 预检 `models.Get` → `providerapi.ErrModelNotFound` | `fmt.Errorf("%w: node %q model_id %d", errs.ErrValidationFailed, key, id)` | FailFromSentinel → 400 |
| 预检 `kbs.Get` → `ragapi.ErrKnowledgeBaseNotFound` | `fmt.Errorf("%w: node %q knowledge_base_id %d", errs.ErrValidationFailed, key, id)` | FailFromSentinel → 400 |
| 其余 | `%w` 包装上抛 | respond.Error → 500 |

## 3. 行为语义（方法编排表）

| 方法 | 步骤 | 缓存动作 |
|---|---|---|
| Create | Validate（api 已做 R1-R8）→ **条 9 预检**（见 §3.1）→ 组装 model 行（Status=`draft`、Config=原文 string、Edges.Condition 指针透传）→ `store.Create` → 复用 Get 组装 detail 返回 | 不回填（写路径不预热缓存） |
| Get | 读 `hify:workflow:{id}` 命中 → 反序列化 detail 直接返回；未命中 → GetByID + ListNodes + ListEdges（404 翻译）→ 组装（空切片兜底）→ 回填（TTL 由 cache 包 NameWorkflow 配置管） | 命中/回填 |
| List | 归一化分页（page<1→1；pageSize<1→20；>100→100）→ `store.List` → summaries + `WorkflowListResult` | 不走缓存 |
| Update | Validate → 条 9 预检 → `store.ReplaceGraph`（false → 404；23505 → 409）→ 复用 Get 组装 detail | 成功后 **Del key** |
| Delete | `store.Delete`（false → 404） | 成功后 **Del key** |
| Publish | GetByID（404 判定，走缓存也不影响正确性）→ `store.UpdateStatus(id, [draft,disabled,published], published)` → 无论 moved 与否 → Del key → Get 组装 summary（moved=false ⇔ 已 published，幂等 200） | Del + 回源 |
| Disable | GetByID → `store.UpdateStatus(id, [published,disabled], disabled)`（draft 不在 from → moved=false 且状态保持 draft，幂等 no-op）→ Del key → Get 组装真实状态 summary | Del + 回源 |

- **条 9 预检**（保存路径，Create/Update 共用，db_model §7 条 9）：遍历已解析节点——`llm` → `models.Get(ctx, providerapi.GetModelReq{ID: ModelID})`；`knowledge_retrieval` → `kbs.Get(ctx, ragapi.GetKnowledgeBaseReq{ID: KnowledgeBaseID})`；**`tool` 不查**（2026-09-15 拍板：mcp api 未建、jsonb 无 FK 兜底，推迟执行器 fail-fast；spec 02 已保证 ToolID≠0）。
- api 层 R2 已解析过 config；service 为取 ModelID/KnowledgeBaseID **二次解析**（≤50 个小 JSON，成本可忽略，换取契约各层单一职责）。
- 状态动作的 GetByID→UpdateStatus 顺序存在竞态窗口（Get 后被删 → UpdateStatus 0 行 → 返回按 Get 数据组装）：内部工具可接受，注释注明。

## 4. handler（薄绑定）

```go
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
    g := rg.Group("/workflows")
    g.POST("", h.create)          // 201 respond.Created(detail)
    g.GET("", h.list)             // 200 respond.OKWithOffset(items, page, pageSize, total)
    g.GET("/:id", h.get)          // 200 respond.OK(detail)
    g.PUT("/:id", h.update)       // 200 respond.OK(detail)
    g.DELETE("/:id", h.delete)    // 204（对齐 agent handler delete 的 204 写法）
    g.POST("/:id/publish", h.publish) // 200 respond.OK(summary)
    g.POST("/:id/disable", h.disable) // 200 respond.OK(summary)
}
```

- 绑定函数 7 个（create/get/list/update/delete/publish/disable）；一个绑定函数只调一个接口方法（《代码组织规范》handler 规则）。
- create/update：`respond.BindJSON(c, &req)`（UpsertReq 实现 Validate，400 信封已写好直接 return）；update 再 `BindUri` 取 id 赋入（provider 同款）。
- get/delete/publish/disable：`respond.BindUri`。
- list：`respond.BindQuery`。
- 错误映射：§2.2 表 + `respond.FailFromSentinel(c, err)` 兜底；哨兵先 `errors.Is` 显式映射（`respond.Fail(c, 状态码, 哨兵.Error(), err.Error())`），未识别走 `respond.Error` 500。

## 5. 组合根（server.go）

在 §3 装配段、**rag 之后 chat 之前**插入（CLAUDE.md 组合根顺序 provider → mcp → agent → rag → workflow → chat；chat 本期不消费 workflow，执行器 spec 再注入 chat 侧依赖）：

```go
// workflow：CRUD + 状态动作（执行引擎后续批次）。
workflowStore := workflowstore.New(db)
workflowSvc := workflowsvc.New(workflowStore, modelSvc, ragSvc, cacheManager)
workflowhandler.New(workflowSvc).RegisterRoutes(v1)
```

- `modelSvc` / `ragSvc` / `cacheManager` 为组合根既有实例（provider/rag 段产物），零新建。
- `internal/platform/cache`：补 `NameWorkflow` 常量 + `DefaultConfig().TTLs` 项（DefaultTTL 30min——写时删 key 强一致，TTL 只是删 key 失败的兜底窗口，cache 包既有注释口径）。
- 缓存 key：`hify:workflow:{id}`（`redisx.Key` 拼前缀）；序列化对象 = `WorkflowDetailSchema`。

## 6. 任务清单与实施顺序（TDD 粒度）

1. cacheManager 窄接口 + workflowService 骨架 + New（RED：方法未实现）；
2. toModel/toSchema 转换函数（纯函数表驱动）；
3. Create（条 9 预检两分支 + 23505 翻译 + 404）；
4. Get（缓存命中 / 未命中回填 / 404）；
5. List（归一化 + 组装）；
6. Update（预检 + ReplaceGraph false→404 + Del key）；
7. Delete + Publish + Disable（幂等三态）；
8. handler 7 路由（httptest 状态码矩阵）；
9. platform/cache NameWorkflow + server.go 装配 + 冒烟。

每步门禁：`go build ./... && go vet ./... && go test ./... -race -count=1`。

## 7. 测试清单

**service（stub Store + 内嵌接口 stub 下游 + 记录式 cacheManager stub，对齐 agent service_test 模式）**：

- Create：happy（Status=draft、Config 原文、多节点落 model）；model 预检 404 → VALIDATION_FAILED 包装（errors.Is 可判 + 文案含 node key）；KB 预检同；store 23505 模拟 → ErrWorkflowNameConflict。
- Get：缓存命中零 store 调用；未命中三查 + 回填；ErrRecordNotFound → ErrWorkflowNotFound。
- Update：预检失败不动 store；ReplaceGraph false → 404；成功后 Del key 断言。
- Delete：false → 404；成功 Del key。
- Publish：from 含 draft/disabled/published；moved=false（已发布）仍 200；Del + 回源断言。
- Disable：published→disabled；disabled 幂等 no-op；draft 幂等且状态保持。
- 转换函数：model↔schema 字段逐一、Condition 指针两态、空切片非 nil。

**handler（httptest）**：7 路由状态码矩阵——201/200×4/204；400（绑定 + 图校验信封 VALIDATION_FAILED）；404 / 409 / 503 映射各一；list 的 meta 分页字段。

## 8. 验收门

- [ ] `go test ./internal/workflow/... -race -cover` 四包总覆盖率 ≥ **80%**
- [ ] `go build ./... && go vet ./... && go test ./... -race -count=1` 全绿
- [ ] `make start` 启动无报错，`curl /health` 200
- [ ] 冒烟序列（照 api_contract §4 示例图）：
  1. `POST /api/v1/workflows` → 201，data.status=`draft`，nodes/edges 与提交一致
  2. `GET /api/v1/workflows/{id}` → 200 round-trip 一致
  3. `POST /api/v1/workflows/{id}/publish` → 200 status=`published`；重复调用 → 200 幂等
  4. `GET /api/v1/workflows?page=1&page_size=20` → 200 meta.total=1
  5. `POST /api/v1/workflows/{id}/disable` → 200 status=`disabled`
  6. `PUT /api/v1/workflows/{id}`（改 description）→ 200 且 status 仍 `disabled`（编辑不降级）
  7. `DELETE /api/v1/workflows/{id}` → 204；再 GET → 404；`workflow_nodes`/`workflow_edges` 随 CASCADE 清空
- [ ] 实施完成时的文档同步（属交付物）：CLAUDE.md 错误码表补 `WORKFLOW_NAME_CONFLICT`(409) / `WORKFLOW_NOT_PUBLISHED`(503)；索引地图 workflows 行更新为三表；data-model.md workflow 段更新三表结构

## 9. 可提交节点

`feat(workflow): service + handler + 装配，CRUD/发布/停用全链路`（可与 spec 01-03 合并为一枚 feat 提交，或按四篇分层拆四枚——用户届时拍板）。
