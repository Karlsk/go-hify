# Workflow 实现 spec 03：store 整图读写

> 状态：**待实施**（2026-09-15 定稿；供 rdp-implementation 以 TDD 消费）。
> 上位契约：[api_contract.md](./api_contract.md) §6（Store 接口与分层约定）、[db_model.md](./db_model.md) §7（保存模型）。冲突时停下来问用户。
> 前置依赖：spec 01（model 三结构体）。spec 02 非硬依赖。
> 规范引用（实施逐条对照）：CLAUDE.md《数据库规范》——SQL 编写规范（禁 `SELECT *` / DML 必带 WHERE / 参数类型对齐列类型 / IN 封顶）、事务与 DDL 规范（事务最小化 / 事务内禁外部调用 / 批量写多 VALUES）；《代码组织规范》——store 层职责（实现 service.Store、错误原样上抛不做业务翻译、只操作本模块表）。

## 1. 交付物

| # | 交付物 | 路径 |
|---|---|---|
| 1 | `Store` 接口定义（消费方声明，依赖倒置） | `internal/workflow/service/service.go`（占位文件中先只加接口，业务实现归 spec 04） |
| 2 | GORM 实现（8 方法 + 事务整图写入） | `internal/workflow/store/store.go`（替换占位） |

## 2. 冻结契约

### 2.1 service.Store 接口（定义在 service 包）

```go
// Store 数据层接口：定义在消费方（本包），store 包实现，组合根注入。
type Store interface {
    // Create 一事务写三表：INSERT workflows + 批量 INSERT nodes/edges（任一失败整体回滚）。
    Create(ctx context.Context, wf *Workflow, nodes []WorkflowNode, edges []WorkflowEdge) error
    GetByID(ctx context.Context, id uint64) (*Workflow, error) // 未找到原样上抛 gorm.ErrRecordNotFound
    // List 行 + 精确 total（极小静态表，接口规范 B 模式允许 OFFSET 与精确 COUNT）。
    List(ctx context.Context, offset, limit int) ([]Workflow, int64, error)
    // ReplaceGraph 整图替换单事务：UPDATE workflows（affected=0 → false，service 翻译 404）
    // → DELETE nodes → DELETE edges → 批量 INSERT ×2。先删后插硬删，不做 diff（db_model 决策 #8）。
    ReplaceGraph(ctx context.Context, wf *Workflow, nodes []WorkflowNode, edges []WorkflowEdge) (bool, error)
    // Delete 硬删 workflows；nodes/edges 由 FK CASCADE 清理。affected=0 → false。
    Delete(ctx context.Context, id uint64) (bool, error)
    // UpdateStatus 原子状态迁移：UPDATE ... SET status=to, updated_at=now()
    // WHERE id=? AND status IN (from)。affected>0 → moved=true（service 据此判幂等）。
    UpdateStatus(ctx context.Context, id uint64, from []string, to string) (bool, error)
    ListNodes(ctx context.Context, workflowID uint64) ([]WorkflowNode, error)
    ListEdges(ctx context.Context, workflowID uint64) ([]WorkflowEdge, error)
}
```

> 与 api_contract §6 草图的签名差异（`Delete`/`ReplaceGraph` 增 bool、`UpdateStatus` 增 from）：为让 service 不做 get-then-set 竞态窗口、并能区分 404 与幂等 no-op，属签名精化、语义不变，以本篇为准。

### 2.2 store 实现

```go
var _ providersvc… → var _ service.Store = (*Store)(nil)  // 编译期断言（别名 workflowsvc）
type Store struct{ db *gorm.DB }
func New(db *gorm.DB) *Store { return &Store{db: db} }
```

（import 别名 `workflowsvc "…/internal/workflow/service"`，包名 service 与本包无冲突但别名遵循 `<module><layer>` 约定——与 providersvc / agentsvc / ragsvc 同款三音节缩写。〔2026-09-17 修订：原 `workflowservice` 全拼，实现按仓内惯例用 `workflowsvc`，用户拍板文档对齐实现。〕）

## 3. 行为语义（SQL 形态冻结要点）

- **事务**：`Create` / `ReplaceGraph` 用 `db.WithContext(ctx).Transaction(...)` 包住全部语句；**事务内零外部调用**（无 Redis / HTTP / LLM——《事务与 DDL 规范》红线）。
- **批量 INSERT**：nodes / edges 各一条多 VALUES INSERT（GORM 对 slice 的 `Create`；空 edges 切片跳过该语句）；禁循环单行 INSERT（《SQL 编写规范》批量写纪律）。
- **DML 必带 WHERE**：DELETE nodes / edges 一律 `WHERE workflow_id = ?`（bigint 参数对齐列类型）。
- **禁 `SELECT *` 文本**：查询列由 model 字段集显式决定（GORM struct 列）；`List` 排序 `ORDER BY updated_at DESC, id DESC` + `LIMIT ? OFFSET ?`，另发一条 `COUNT(*)` 取 total。
- **ListNodes / ListEdges**：`WHERE workflow_id = ? ORDER BY id ASC`——按插入序稳定还原（多 VALUES INSERT 的 id 顺序即请求顺序）。
- **UpdateStatus**：`from` 为等值小列表（≤3 值），GORM `IN (?)` 展开合规（IN 封顶 1000）；`updated_at` 由 GORM `autoUpdateTime` 维护，不手写 now()。
- **错误链路**：所有 error 用 `%w` 加上下文上抛（如 `replace graph workflow %d: %w`）；`gorm.ErrRecordNotFound`、PG 23505 等一律**不翻译**——哨兵翻译是 service 职责（层职责表）。
- **只操作本模块三张表**；不 JOIN 他模块表（《跨模块调用规则》禁止清单）。

## 4. 任务清单与实施顺序（TDD 粒度）

1. service.Store 接口定义（随 1 个方法一起 RED）；
2. GetByID / ListNodes / ListEdges（读路径，sqlmock RED→GREEN）；
3. List（两查询拼接）；
4. Create（事务三表成功 / 中途失败 Rollback）；
5. ReplaceGraph（UPDATE 0 行 → false；替换序列）；
6. UpdateStatus（moved / not-moved）；
7. Delete（affected 0/1）。

每步门禁：`go build ./... && go vet ./... && go test ./... -race -count=1`。

## 5. 测试清单（sqlmock 表驱动，同包 `store_test.go`，零真实 PG）

- Create：Begin → INSERT workflows → INSERT nodes → INSERT edges → Commit 期望序列；edges 中途失败 → Rollback 且错误上抛链完整（`errors.Is` 原因可判）。
- ReplaceGraph：UPDATE affected=1 + DELETE×2 + INSERT×2 → true；UPDATE affected=0 → false（不再执行后续 DELETE）。
- UpdateStatus：affected=1 → (true, nil)；affected=0 → (false, nil)；SQL 断言含 `WHERE id = ? AND status IN (?,?)`。
- Delete：affected=1 → true；affected=0 → false。
- GetByID：命中行回填；无行 → `gorm.ErrRecordNotFound` 上抛。
- List：rows 查询 + COUNT 查询两次 ExpectQuery，LIMIT/OFFSET 参数正确。
- ListNodes / ListEdges：多行回填按 id 升序；Condition 指针 nil / 非 nil 两态。

## 6. 验收门

- [ ] `go test ./internal/workflow/store/... -race -cover` ≥ **80%**
- [ ] `go build ./... && go vet ./... && go test ./... -race -count=1` 全绿
- [ ] 代码评审对照《SQL 编写规范》七条逐条过（禁 SELECT * / WHERE / 参数类型 / IN / 批量 VALUES）

## 7. 可提交节点

`feat(workflow): store 整图读写（事务替换 + 状态原子迁移）`。
