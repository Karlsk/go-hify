# pgvector 从零上手（pgvector_quickstart）

> 状态：**实测记录**（2026-09-07，本机 Docker `pgvector/pgvector:pg17` + pgvector 扩展 0.8.6）：安装、建表、插入、相似度查询、HNSW 索引全流程跑通，坑已踩。选型理由见 [vector_db_selection.md](vector_db_selection.md)；Go/GORM 集成见 [go_gorm_integration.md](go_gorm_integration.md)；DDL 规范以 CLAUDE.md《数据库规范》为准（禁 AutoMigrate，一切 DDL 进 migrations/）。

## 1. 安装：三条路径

**路径 A —— 全新 Docker（最快，本次实测用的）：**

```bash
docker run -d --name pgvector-demo \
  -e POSTGRES_PASSWORD=demo \
  -p 5433:5432 \
  pgvector/pgvector:pg17     # 官方镜像 = PG17 + 预编译扩展，零编译
```

> macOS arm64 会提示 linux/amd64 平台警告，Rosetta 模拟跑开发环境无碍。

**路径 B —— 已有普通 PG**：装扩展包（Debian/Ubuntu：`apt install postgresql-17-vector`）后 `CREATE EXTENSION` 即可。

**路径 C —— Hify 项目内（真实场景）**：deploy compose 的 PG 已是 `pgvector/pgvector:pg17` 镜像，**安装已完成**，只差迁移文件里 `CREATE EXTENSION`。

## 2. 最小可跑通示例（五步，已实测）

```bash
docker exec -i pgvector-demo psql -U postgres <<'SQL'
-- ① 启用扩展（每库一次；写进第一个 migration）
CREATE EXTENSION IF NOT EXISTS vector;

-- ② 建表：vector(维度) 建表即锁定维度
CREATE TABLE items (
    id        bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kb_id     bigint      NOT NULL DEFAULT 1,
    content   text        NOT NULL,
    embedding vector(3)   NOT NULL      -- 演示用 3 维，真实场景 1536/3072
);

-- ③ 插入：向量就是字符串字面量 '[a, b, c]'
INSERT INTO items (content, embedding) VALUES
    ('猫是一种常见的家庭宠物', '[0.9, 0.1, 0.0]'),
    ('狗忠诚且适合陪伴',       '[0.8, 0.2, 0.1]'),
    ('汽车是四轮交通工具',     '[0.1, 0.0, 0.9]'),
    ('火箭把卫星送入轨道',     '[0.0, 0.1, 0.95]');

-- ④ 相似度查询：<=> 是余弦距离；ORDER BY 必须用同一操作符
SELECT id, content,
       round(1 - (embedding <=> '[0.85, 0.15, 0.05]')::numeric, 4) AS similarity
FROM items
ORDER BY embedding <=> '[0.85, 0.15, 0.05]'
LIMIT 2;

-- ⑤ 建 HNSW 索引：cosine 距离配 vector_cosine_ops
CREATE INDEX idx_items_embedding ON items
    USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);
SQL
```

**实测结果**（查询向量 ≈ "小猫" 的语义位置）：

```
 id |        content         | similarity
  1 | 猫是一种常见的家庭宠物 |    0.9963
  2 | 狗忠诚且适合陪伴       |    0.9956
```

猫、狗排前两名，汽车/火箭被正确排除；带 `WHERE kb_id = 1` 过滤的版本行为一致。

## 3. 三个距离操作符（索引与查询必须配对）

| 操作符 | 距离 | 索引 ops 类 | 什么时候用 |
|---|---|---|---|
| `<->` | 欧氏距离（L2） | `vector_l2_ops` | 图像特征等未归一化场景 |
| `<=>` | **余弦距离** | `vector_cosine_ops` | **文本 embedding 标准选择（Hify 用这个）** |
| `<#>` | 负内积 | `vector_ip_ops` | 向量已归一化时 |

**配对铁律**：索引用 `vector_cosine_ops`，查询就得写 `ORDER BY embedding <=> ...`。写错操作符 = 索引不生效、退化为全表扫，**且不报错**，只能靠 `EXPLAIN` 发现。

## 4. 实测暴露的三个坑

1. **小表不走 HNSW 是正常的**。4 行数据时 `EXPLAIN` 显示 Seq Scan——规划器认为全表扫更便宜，**不是索引坏了**；几千行后自动切 Index Scan（已用 `SET enable_seqscan = off` 验证索引路径本身是通的）。本地开发别被吓到。
2. **维度建表即冻结**。`vector(3)` 改不了；换 embedding 模型（1536 → 3072 维）= 重建表 + 重建索引。这是"嵌入模型绑定知识库"（`knowledge_bases.embedding_model_id`）的物理原因。
3. **`docker exec` 传 SQL 要带 `-i`**——heredoc 进 stdin 必须有 `-i`，否则 psql 静默无输出。

## 5. 落到 Hify 的下一步

- `CREATE EXTENSION vector` + `chunks` 表 DDL（`embedding vector(N)`、HNSW 索引、`(document_id)` btree）写进 **migrations/ SQL 文件**——禁 AutoMigrate（CLAUDE.md 红线）；
- 查询侧 `SET LOCAL hnsw.ef_search = 80`（默认 40）放进召回 SQL，召回率用 golden set 调；
- store 层放 `internal/rag/store/`，service 只见 `Store` 接口——向量库切换成本锁在一个包里（见 [vector_db_selection.md](vector_db_selection.md) §1）。
