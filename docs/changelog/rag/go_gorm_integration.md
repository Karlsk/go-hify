# pgvector × Go × GORM 集成（go_gorm_integration）

> 状态：**实测记录**（2026-09-07，本机验证：pgvector-go v0.4.1 + GORM v1.31.2 + gorm.io/driver/postgres v1.6.2，连 `pgvector/pgvector:pg17`）：GORM 直接操作 vector 字段、读回、Raw 相似度查询全部跑通。SQL 基础见 [pgvector_quickstart.md](pgvector_quickstart.md)；对应 CLAUDE.md《代码组织规范》"GORM（CRUD）+ pgvector（向量召回走 `db.Raw` 原生 SQL）"的分工。

## 1. 核心结论

**GORM 原生不认识 `vector` 类型，但通过官方库 `pgvector-go` 可以把向量当普通结构体字段用——CRUD 全走 GORM；唯独相似度召回的 `<=>` 操作符 GORM DSL 表达不了，必须 `db.Raw`。** 这正是 CLAUDE.md 定的分工。

实测结论汇总：

| 问题 | 结论 |
|---|---|
| GORM 能插入 vector 字段吗 | ✅ `db.Create(&items)` 直接可用，自动生成批量多 VALUES + `RETURNING "id"` 回填主键 |
| GORM 能读回 vector 吗 | ✅ `First` / `Scan` 自动解析成 `pgvector.Vector` |
| Raw 查询参数要 `::vector` cast 吗 | ✅ **两种都行**（实测 err 均为 nil）：`embedding <=> ?` 直接传 `pgvector.Vector`；加 `?::vector` 也对，与传字符串等价 |
| 生成的 SQL 干净吗 | ✅ 就是 `embedding <=> '[0.85,0.15,0.05]'`，无多余包装 |

## 2. 机制：Valuer / Scanner

GORM 底层走 `database/sql` 接口，任何类型只要实现这两个接口就被当成"自定义标量类型"：

- `driver.Valuer`（写路径）：`pgvector.Vector` 序列化成 `'[0.9,0.1,0]'` 字符串发给 PG；
- `sql.Scanner`（读路径）：把 PG 返回的文本解析回 `[]float32`。

GORM 对它的体验和对 `time.Time` 没区别——这就是"能直接操作"的准确含义。

## 3. 验证过的最小代码

```go
import (
    "github.com/pgvector/pgvector-go"
    "gorm.io/driver/postgres"
    "gorm.io/gorm"
)

// 模型：embedding 就是普通字段；gorm tag 声明列类型（AutoMigrate 才用，Hify 禁用）
type Item struct {
    ID        int64           `gorm:"primaryKey"`
    Content   string          `gorm:"not null"`
    Embedding pgvector.Vector `gorm:"type:vector(3);not null"`
}
func (Item) TableName() string { return "items" }

db, _ := gorm.Open(postgres.Open(dsn), nil)

// ① 插入：批量 Create，生成多 VALUES，id 自动回填
items := []Item{
    {Content: "猫是一种常见的家庭宠物", Embedding: pgvector.NewVector([]float32{0.9, 0.1, 0})},
    {Content: "狗忠诚且适合陪伴",       Embedding: pgvector.NewVector([]float32{0.8, 0.2, 0.1})},
    {Content: "汽车是四轮交通工具",     Embedding: pgvector.NewVector([]float32{0.1, 0, 0.9})},
}
db.Create(&items)

// ② 读回：自动 Scan
var got Item
db.First(&got, items[0].ID)
got.Embedding.Slice()   // []float32{0.9, 0.1, 0}

// ③ 相似度召回：必须 db.Raw（GORM 没有 <=> 的 DSL）
query := pgvector.NewVector([]float32{0.85, 0.15, 0.05})
var hits []struct {
    ID       int64
    Content  string
    Distance float64
}
db.Raw(`SELECT id, content, embedding <=> ? AS distance
        FROM items
        ORDER BY embedding <=> ?
        LIMIT 2`, query, query).Scan(&hits)
// → 猫(dist 0.0037)、狗(0.0044)，汽车被正确排除
```

## 4. 三条集成路线

| 方式 | 依赖 | 适用 |
|---|---|---|
| **A. `pgvector-go` 类型化字段**（上文） | +1 个官方小库 | **推荐**：模型字段、参数、Scan 全类型化，`[]float32` 直通 |
| B. 字符串 + `?::vector` cast | 零依赖 | `[]float64` 转字符串（`strconv.FormatFloat` + `strings.Join` 拼 `"[a,b,c]"`）后按参数传；不想引库的 demo/小场景 |
| C. 自定义 Valuer/Scanner | 零依赖 | A 的手写版——`type Vector []float32` 实现两个接口，~20 行，与 A 等价但自己维护 |

## 5. GORM 能力边界

**能：**

- struct CRUD（`Create` / `First` / `Find` / `Delete`），含批量插入与 id 回填；
- `Scan` 到结构体（Raw 结果里的 vector 列自动解析）；
- 事务（`Transaction` / `WithTx`）照常包住向量写入。

**不能：**

- **DSL 表达相似度**：`Where/Order` 写不出 `ORDER BY embedding <=> ?`——距离操作符是 pgvector 私有语法，`db.Raw` 是合理边界而非缺陷；
- **AutoMigrate 管 vector DDL**：`gorm:"type:vector(3)"` 能让 AutoMigrate 生成正确列类型，但 Hify 红线禁 AutoMigrate——`CREATE TABLE`、HNSW、`(document_id)` btree 全部进 migrations/ SQL，model 的 gorm tag 只是声明性标注。

## 6. Hify 落地注意点

1. **`pgvector.Vector` 内部是 `[]float32`**——embedding API 返回的正是 float32，直通；业务代码若有 `[]float64` 中间态，在边界转一次。
2. **`vector(N)` 的 N 建表即冻结**：chunks 表维度跟部署选定的 embedding 模型走，migration 里的数字要与实际模型对齐（见 [embedding_api.md](embedding_api.md) §3 维度表）。
3. store 层按四层结构放 `internal/rag/store/`，召回 SQL（含 `SET LOCAL hnsw.ef_search`）集中在此；service 定义 `Store` 接口（依赖倒置），sqlmock 测试。
