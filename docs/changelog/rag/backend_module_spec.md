# RAG 后端总览与决策记录（backend_module_spec）

> 状态：**设计定稿，实施 spec 已按能力单元拆分为 8 篇**（2026-09-07，四轮评审收敛：数据模型 → API CRUD → 管线环节与分块策略 → 递归分块与解析槽位定稿）。本文是 rag 后端的**决策记录与索引**（为什么这样做）；实施细节（怎么做）按可独立单测的能力单元拆分，每篇自带单测计划与验收门：
>
> | # | spec | 交付 | 单测焦点 |
> |---|---|---|---|
> | 01 | [模型与数据迁移](backend_spec_01_model_migration.md) | 00011 迁移 + model.go 实体 + 核心不变量定义 + RagCfg/Accepted/NameRag 地基 | config 校验；migrate up/down |
> | 02 | [embedding 能力](backend_spec_02_embedding.md) | platform/llm/embed.go（4 槽 + 5s 总预算 + 重试矩阵） | httptest 按 kind 打桩 |
> | 03 | [接口定义](backend_spec_03_api_contract.md) | api 三文件（12→13 方法契约〔2026-09-14 修订 +Disable/Enable〕+ schema + 6 哨兵） | schema_test 表驱动；api_test 方法集钉死 |
> | 04 | [CRUD](backend_spec_04_crud.md) | KB/文档 CRUD 三层（端点 1-10 + disable/enable〔2026-09-14 修订〕；级联真删/深度停用/reindex 事务重置） | service/store/handler 三层 stub |
> | 05 | [检索](backend_spec_05_retrieval.md) | Retrieve 编排 + SearchChunks 单表 SQL（端点 11） | 召回 SQL 事务序列断言 |
> | 06 | [分块与解析](backend_spec_06_chunker.md) | chunker.go 纯函数（extractText + 递归分割 + token 估算） | 表驱动，零依赖可先行 |
> | 07 | [管道](backend_spec_07_pipeline.md) | dispatch + 状态机 + 终态事务 + Recovery + 接线 | dispatch 注入同步直调 |
> | 08 | [组装与验收](backend_spec_08_assembly.md) | server.go 装配 + 路由 + 骨架冒烟 + 端到端 rag-manual-test | curl 人工验收 |
>
> 执行顺序 = 编号序（01 → 08）；06 纯函数无前置可随时并行。原「骨架波/管线波」两道提交门仍有效：**05 后**做骨架装配冒烟（08 §3，可提交），**07 后**补装配增量过端到端（08 §4）。
>
> **下一阶段再议**（与 agent/chat 集成时定稿，本期契约冻结不动）：chat 检索注入格式（system 尾部追加、fail-open 降级、`[1]` 引用编号）、上下文 token 预算（`RetrieveReq.TokenBudget` / `RetrievedChunk.TokenCount` / `RagCfg.ContextTokenBudget` 已预演）；agent 绑定知识库的 agent 侧 CRUD；前端页面。
> 前置调研见本目录五篇（[concepts](concepts.md) / [vector_db_selection](vector_db_selection.md) / [pgvector_quickstart](pgvector_quickstart.md) / [go_gorm_integration](go_gorm_integration.md) / [embedding_api](embedding_api.md)）；规范依据 CLAUDE.md《代码组织规范》《数据库规范》《接口规范》；表归属见 [docs/design/data-model.md](../../design/data-model.md)；人工验收步骤落地为 [docs/testing/rag-manual-test.md](../../testing/rag-manual-test.md)（08 实现时新建）。

## 1. 决策摘要

| # | 决策点 | 结论 |
|---|---|---|
| 1 | embedding 落位 | `platform/llm/embed.go` 手写薄 adapter（照 prober 直连先例 + `NewJSONClient`），零新依赖；`Embedder.EmbedStrings`，独立 4 槽 + 5s 超时（总预算含 sleep）+ 重试 1 次。否决 eino-ext 新依赖（各家端点差异仍需自抹平） |
| 2 | api 契约 | 单接口 `KnowledgeBaseService`（KB CRUD 5 + 文档 5 + Retrieve），**Retrieve 本期进 api 并开 HTTP**（curl 验证 + 下期 chat 直接复用） |
| 3 | 上传报文 | multipart（`file` + 可选 `name`），handler 手动绑定 + `Validate()` 兜底；ContentLength 预检 + LimitReader 双保险；202 响应 data 含 `status=pending` + file_type/file_size |
| 4 | 异步编排 | goroutine + `semaphore.Weighted`（DB 即持久队列）；**状态机四态** `pending→processing→ready/failed`，启动扫 pending/processing 残留 → 置 failed（不自动重跑，reindex 是显式用户动作） |
| 5 | 分块 | **递归分割** `SplitChunks(content, size, overlap)`（第四轮定稿）：段落(`\n\n`)贪心合并 > 单段超长降级句子边界（中英标点、小数点豁免）> 无标点硬截；MD 围栏(````)原子保护；尺寸 500/80 **rune**；overlap = 上一块尾部前缀。否决固定滑窗（用户升级为递归）；否决 tiktoken 精确 token（词汇表 2-4MB、cl100k 绑 OpenAI 系、跨 provider 不通用——估算 ±20% 下"精确 512"是假精度，token_count 维持估算列） |
| 6 | 召回 SQL | **单表查询 `document_chunks`**（冗余 kb_id 免 JOIN）；`db.Transaction` 内 `SET LOCAL hnsw.ef_search`；正确性由不变量保证（定义见 [spec 01](backend_spec_01_model_migration.md) §3，2026-09-14 修订后 = status='ready' 且 enabled=true），不再手写 status/enabled 过滤 |
| 7 | 哨兵 | rag 新增 6 个 + llm 新增 1 个；文件类型/大小/空内容走 `errs.ErrValidationFailed` |
| 8 | KB 删除 | **硬删 + 有文档（含软删，Unscoped 计数）→ `ErrKnowledgeBaseInUse`(409) 拒绝**（第三轮再次确认，否决草稿的逻辑删除级联——防误删知识资产 + chunks 永不逻辑删）；CASCADE 只清 agent 绑定。**2026-09-14 修订（用户批准，本条挡删语义退役）**：KB Delete = 级联真删单事务（chunks→documents→KB 行），`ErrKnowledgeBaseInUse` 移除；误删兜底 = PG 每日备份 |
| 9 | 模型校验 | 建 KB 时 `ModelService.Get` 预检：capability=embedding + enabled + dim==1536（常量钉死） |
| 10 | KB 启停用 | `enabled boolean NOT NULL DEFAULT true`（不用 1/0 整数，PG 原生布尔）；检索侧 service 解析 KB 列表时**静默剔除 disabled**——管理员下架某库，绑它的 Agent 用剩余库继续工作，不报错；管理面照常可见可编辑 |
| 11 | 文档元数据 | `file_type text + CHECK('txt','md')`（取**扩展名**而非 mime——mime 可伪造；二期加 pdf 只改 CHECK）、`file_size bigint`（字节）、`error_message text`（截 500）、`chunk_count integer`（终态事务原子维护） |
| 12 | 落库策略 | **终态一次性事务**：全量向量算完后一个事务内批量 INSERT chunks + `status→ready` + `chunk_count=N`；失败发生在事务之前 → **failed 文档天然零孤儿向量**；内存峰值被 MaxUploadBytes 钉死（~10MB/文档，并发 2 ≈ 20-50MB，已接受） |
| 13 | 表名/列名 | `chunks → document_chunks`（复数，仓规）；`seq → chunk_index`；新增 `token_count integer`（**估算值**：`ceil(ASCII字符数/4 + 非ASCII字符数)`——embedding API 只有批量级 usage，无按条计数；供 chat 注入的上下文预算，±20% 无碍） |
| 14 | agent 绑 KB | **保留 M:N 关联表 `agent_knowledge_bases`**（用户拍板）：客服 Agent 同时绑「产品手册」+「退货政策」是真实场景；agent_tools 同构先例；双侧 CASCADE 解绑无损；00004 已落库。否决 agent 表单列 FK（封死多库 + ON DELETE SET NULL 隐式副作用） |
| 15 | 引用来源 | top-k 结果拿 document_id 后**二次小查询**（`WHERE id IN`，≤20 行）解析 document_name，不进 ANN SQL |
| 16 | 二期 PDF | 原文出库存文件卷 + 流式上传 + 流式分批提交 + per-doc token 上限（见 §3）；一期只预埋 file_type / token_count 两个钩子 |
| 17 | KB 列表搜索 | `name` 可选 query 参数，`ILIKE '%kw%'` **参数化绑定**（第三轮新增需求）；KB 是个位数~几十行配置表，全扫微秒级——**显式豁免**「LIKE 禁前导通配」红线，代码注释写明豁免理由；用户输入含 `%`/`_` 视作通配符，内部管理搜索不转义（注释说明） |
| 18 | 交付切分 | **骨架先行两波**（第三轮定稿）：骨架波交付 CRUD 全套 + 检索（上传/重索引落 pending 后**不接 dispatch**——代码里尚无该调用行，停 pending 是自洽中间态）；管线波补 chunker + pipeline（dispatch/终态事务/Recovery）+ Upload/Reindex 尾部接线（各一行）+ 端到端验收 |
| 19 | 解析环节槽位 | **`extractText` 显式环节保留**（第四轮定稿）：一期读 `Document.Content` 原样返回（txt/md pass-through，~10 行；上传边界已完成 BOM/编码/空白规范化），二期换文件卷 + PDF 提取实现、签名不变——管道形状从第一天就是最终形态。**文件落盘不做**：上传路径二期反正要改流式（几百 M 进不了内存），一期做零节省；原始字节保全对 txt/md 收益 marginal |
| 20 | 管线代码组织 | `processDocument` **只做串联 + 状态管理**，环节逻辑独立私有函数（loadDocument / resolveEmbedOptions / embedChunks / buildDocumentChunks / commitReady / markFailed），全局规则函数 <50 行 |

**用户拍板**：迭代范围 = 仅 rag 后端模块；文档处理 = 后台异步；agent 绑 KB 保留关联表；终态事务的 20-50MB 内存峰值接受；KB 删除 = 硬删 + 有文档挡删（否决逻辑删除级联）〔2026-09-14 修订：挡删退役 → 级联真删 + enabled 可逆下架〕；上传默认 2MB（`RAG_MAX_UPLOAD_BYTES` 可调，否决草稿 10MB）；name 模糊 = ILIKE 小表豁免；解析环节读 DB（一期 pass-through，文件落盘留二期）；分块 = 递归分割 rune 单位（否决固定滑窗与 tiktoken）。

> **2026-09-14 修订（用户批准）**：软删除全面退役（迁移 00013）——可逆下架统一由业务 `enabled` 布尔承担（KB/Agent 轻量停用、Document 深度停用 = 删向量保内容、重新启用自动重跑管线），DELETE = 真删（级联清理，误删兜底 PG 每日备份）。受影响：决策 #2/#6/#8 与 spec 01/03/04/07/08 各修订标注。

## 2. 交付波次与门（8 篇 spec 的两道提交门）

| 提交门 | 覆盖 spec | 门内容 |
|---|---|---|
| **骨架闭环（可提交）** | 01 → 02 → 03 → 04 → 05，随后 08 §2-§3 骨架装配 + 冒烟 | CRUD 全通 / 上传 202 停 pending / 检索空库 `[]` |
| **完整闭环（模块完结）** | 06 → 07，随后 08 §1 双返回/Recovery/接线增量 + §4 端到端 | pending→processing→ready 全链路 + 检索命中 + rag-manual-test.md 落盘 |

波次说明：骨架期的「停 pending」是自洽中间态——上传/重索引照常返回 202，文档留 pending 等管线接入（04 的 Upload/Reindex **无 dispatch 调用行**，重启不误伤）；Retrieve 随骨架交付（embed.go 在 02 就位，空库返回 `[]` 完全可测）。api 契约（03）一次定义全量 12 方法；**Store 接口随各篇增量演进**（04 声明 CRUD 子集，05 补 SearchChunks/GetDocumentMetasByIDs，07 补管线方法族）——每篇只加自己的方法，单测边界清晰。

## 3. 二期 PDF 演进预留（本期不做，决策已定）

单位经济学（为什么"几百 M 文档"的真瓶颈是 HNSW 内存而非钱）：

| 指标 | 每 100 万提取 token | 300MB 纯文本 PDF（≈1-1.5 亿 token） |
|---|---|---|
| chunks（500 rune/块） | ~2,000 | 20-30 万行 |
| HNSW 索引内存（~6.4KB/块） | ~13MB | **1.3-1.9GB**（PG 容器限 768m，超 2 倍+） |
| 嵌入成本（$0.02/1M） | ~$0.02 | ~$2-3（最便宜） |

**单机 2C4G + pgvector 的全库硬预算 ≈ 500MB 索引内存（768m × 70% 告警线）≈ 4,000 万提取 token。** 二期支持 PDF 时一次做四件事，不推翻一期表结构：

- **a. 原文出库**：原始 PDF 存文件卷（DB 只存路径+元数据），`content` 列只存提取后纯文本——TOAST 大对象拖垮查询 + pg_dump 备份卷爆炸（14 天 × N 篇几百 M）；上传改流式 `io.Copy` 落盘。
- **b. pipeline 流式化**：逐页提取 → 增量分块 → 分批 embed → **分批提交**（每批一小事务），内存 O(一批)；不变量放宽为「有 chunks ⟺ ready 或 processing」，检索加回文档状态过滤（service 解析 ready doc_ids → `document_id = ANY($1::bigint[])`，改动锁在 store 一个方法）；分批提交天然是断点，续跑可选（重嵌一篇才 $3，内部工具置 failed + 手动 reindex 够用）。
- **c. 容量护栏（按 HNSW 内存设计）**：`RAG_MAX_DOC_TOKENS`（按提取后 token 计，上传时用估算器预估，超限拒+提示拆分）；全库监控沿用 `pg_relation_size` 70% 告警；sha256 去重从可选升级为必做。
- **d. file_type CHECK 加 'pdf'**：一行迁移，即本期 file_type 列预留的钩子。

一期预埋的钩子：file_type 列（d）、token_count 列与估算公式（c 的估算器）、MaxUploadBytes（内存峰值的钉子）。
