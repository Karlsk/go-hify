# 向量数据库选型（vector_db_selection）

> 状态：**决策记录——选定 pgvector**（2026-09-07）。约束前提：Go 技术栈、已有 PostgreSQL（compose 用 `pgvector/pgvector:pg17`）、一期数据量几千~几万分块、基础余弦相似度检索够用、单机 2C4G。与 CLAUDE.md《数据库规范》pgvector 章节、《部署架构》一致。
> 概念背景见 [concepts.md](concepts.md)；选型后的上手与集成实测见 [pgvector_quickstart.md](pgvector_quickstart.md)、[go_gorm_integration.md](go_gorm_integration.md)。

## 1. 结论

**四条约束全部指向 pgvector，没有一条为另外三个加分。** 它是"先选最 boring 的方案，把切换成本锁在一个包里"的典型正确决策——向量召回 SQL 隔离在 rag 模块 `store/` 层（`db.Raw`），service 只依赖 `Store` 接口，真到迁移那天换一个 store 实现即可，业务代码不动。

## 2. 总览对比

| 维度 | pgvector | Milvus | Qdrant | Elasticsearch |
|---|---|---|---|---|
| 本质 | PG 扩展，非独立数据库 | 专用分布式向量库 | 专用单机/集群向量库 | 全文搜索引擎 + 向量能力 |
| 部署成本 | **零新增**（现有 PG 装扩展） | 高：etcd + 对象存储 + 消息队列 | 中：多一个常驻容器 | 高：JVM 内存大户 |
| 几万条规模下表现 | 毫秒级，毫无压力 | 大材小用 | 大材小用 | 大材小用 |
| 规模上限 | 单机百万级开始吃力 | 十亿级、分布式 | 千万级、单机很强 | 千万级 |
| 元数据/过滤 | **SQL 全能力**（JOIN、partial 索引、事务） | payload 过滤，元数据另存 | payload 过滤，内置最强 | 丰富但复杂 |
| 混合检索 | 需自建（tsvector + 手动融合） | 支持 | 支持（稀疏向量） | **最强**（BM25 + kNN） |
| Go 生态 | GORM/db.Raw 原生 SQL | 官方 Go SDK | 官方 Go client | 官方 Go client |
| 备份/事务 | **随 PG**：pg_dump 全覆盖、与业务数据同库同事务 | 独立备份体系 | 独立备份体系 | 独立备份体系（snapshot） |
| 内存 | HNSW 驻留 PG 内存（几万条约 200MB 级） | 独立管理 | 独立管理 | JVM heap + offheap，最重 |
| 运维心智 | **零新增** | 最重：多组件、升级复杂 | 较轻：单二进制 | 重：分片/副本/JVM 调优 |

## 3. 逐项理由

### 3.1 pgvector —— 选中原因

- **零新增基础设施**：现有 PG 容器换镜像装扩展即用，compose 不加服务、卷、监控项。
- **一个事务闭环**：文档记录、chunks 行、向量写入同一 PG 事务——入库失败整体回滚，不存在"元数据在 PG、向量在别处"的双库一致性问题。
- **一个备份故事**：pg_dump 同时覆盖业务数据和向量，灾难恢复只有一条路径。
- **SQL 全能力**：`WHERE knowledge_base_id = $1 AND deleted_at IS NULL` 业务过滤、JOIN documents 拿引用来源、partial 索引——专用向量库做这些都要绕。
- **规模够用**：几万条 1536 维向量，HNSW 索引约 200MB 内存，查询毫秒级；实际能撑到单机百万级才需认真考虑迁移。

已知短板（诚实记录）：

- 过滤默认 postfilter（先 ANN 再过滤），强过滤 + 大索引时召回数不足——几万条规模基本感知不到；兜底方案：过滤后候选集很小时让 `WHERE` 走 `(document_id)` btree 先缩范围（CLAUDE.md 已定）。
- 无分布式、分片、量化压缩——当前用不上。
- ANN 吞吐上限低于专用库——3-5 QPS 检索压力离上限差四个数量级。

### 3.2 Milvus —— 否决

专为超大规模设计（分片、副本、DiskANN、GPU 索引）。但架构依赖 etcd + 对象存储 + 消息队列，standalone 模式也只是打包了这些依赖——**对 2C4G 单机 compose 是灾难**，光跑起来就吃掉大半资源；元数据另存又回到双库一致性。适用边界：向量数过千万、QPS 上千、有专职运维。

### 3.3 Qdrant —— 备胎首选

Rust 单二进制，资源克制、payload 过滤设计优秀（prefilter）、稀疏向量原生支持混合检索、官方 Go client。**若哪天 pgvector 撑不住，它是迁移首选目标**。当前仍是负资产：多一个常驻容器/数据卷/备份流程，双库无跨库事务，换来的能力在几万条规模下兑现不了。

### 3.4 Elasticsearch —— 否决

混合检索最强（BM25 + kNN 融合），但 JVM 要 2G+ heap（整个 compose 预算才 4G）；项目没有现成 ES；向量检索非其核心优化方向；运维复杂度最高。二期要混合检索时，PG 内 `tsvector` + pgvector 手动融合两路召回，成本远低于养一个 ES。

## 4. 约束打分

| 约束 | pgvector | Milvus | Qdrant | ES |
|---|---|---|---|---|
| Go 技术栈 | ✅ 原生 SQL/GORM | ✅ 官方 SDK | ✅ 官方 client | ✅ 官方 client |
| 已有 PostgreSQL | ✅ **完全复用** | ❌ 另起炉灶 | ⚠️ 双库并存 | ❌ 另起炉灶 |
| 几千~几万分块 | ✅ 甜蜜区 | ❌ 杀鸡用牛刀 | ⚠️ 杀鸡用牛刀 | ❌ 杀鸡用牛刀 |
| 基础相似度够用 | ✅ HNSW + cosine 全覆盖 | ⚠️ 能力大量闲置 | ⚠️ 能力闲置 | ❌ 能力大量闲置 |

## 5. 未来重新评估的触发信号（写进监控提前预警）

1. **索引大小逼近内存**：`pg_relation_size('idx_chunks_embedding')` 超过 PG 容器可用内存约 70%——HNSW 换页会让召回延迟暴涨（CLAUDE.md 监控章节已有此 SQL）；
2. 向量数奔千万级，或单机 PG 成为写入瓶颈；
3. 需要复杂强过滤 + 海量向量（Qdrant 的 prefilter 优势区）；
4. 检索 QPS 上千（当前离得无限远）。
