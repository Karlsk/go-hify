# RAG 核心概念与技术组件（concepts）

> 状态：**调研学习材料**（2026-09-07）：`internal/rag/` 动工前的概念梳理——RAG 核心思路、完整技术组件清单、与 Hify 架构的映射。模块尚未落码，实现时以本文 + CLAUDE.md《数据库规范》pgvector 章节为基准。
> 相关：向量库选型结论见 [vector_db_selection.md](vector_db_selection.md)；pgvector SQL 上手见 [pgvector_quickstart.md](pgvector_quickstart.md)；Go/GORM 集成见 [go_gorm_integration.md](go_gorm_integration.md)；embedding API 见 [embedding_api.md](embedding_api.md)；表归属总览见 [docs/design/data-model.md](../../design/data-model.md)。

## 1. 核心思路

一句话：**不动模型本身，把私有知识放在外部数据库里，每次提问先检索出最相关的几个片段、拼进 prompt，让模型"开卷考试"**。RAG = Retrieval-Augmented Generation（检索增强生成），本质是把"记忆"从模型参数中解耦出来。

LLM 答不出公司文档内容的根因：知识在训练完成那一刻冻结，内部文档从没进过训练数据。三条解决路径对比：

| 方案 | 做法 | 问题 |
|---|---|---|
| 全部文档塞 prompt | 每次都带整个手册 | 上下文窗口装不下；即便装得下也贵、无关内容稀释注意力 |
| 微调（fine-tuning） | 用文档重新训练 | 成本高；文档更新就要重训；不可溯源、易幻觉 |
| **RAG** | 按需检索相关片段注入 | 更新=重新索引（分钟级）；可追溯到 chunk；模型干它擅长的阅读理解 |

关键洞察：**LLM 擅长"阅读理解 + 组织语言"，不擅长"记忆"**。RAG 把知识查询外包出去，只在生成时喂给模型当下需要的几段内容。

## 2. 工作机制：两条路径

### 2.1 离线索引（文档入库时）

```
产品手册.md
  → 切块(chunking)：固定长度切成小段（相邻段留重叠）
  → embedding 模型：每段 → 高维向量（如 1536 个浮点数）
  → 存向量库（pgvector）：向量 + 原文 + 元数据，建 HNSW 索引
```

向量是文本的"语义指纹"：语义相近的文本向量距离近。用户问"退货要收运费吗"能召回写着"退款流程与运费承担说明"的段落——靠语义相似，不是关键词字面匹配。

### 2.2 在线查询（用户每次提问时）

```
"你们的质保期是多久？"
  → 同一个 embedding 模型 → 问题向量
  → pgvector 余弦相似度 top-k 召回（3~5 个 chunk）
  → 拼 prompt："以下是与问题相关的公司资料：… 请仅基于资料回答，资料中没有的就说不知道"
  → LLM 生成回答（可附带来源 chunk 引用）
```

模型从"凭记忆答"变成"做阅读理解题"——读的是刚检索来的公司文档原文。

## 3. 完整技术组件清单

```
【离线索引】文档上传 → 解析取文本 → 切块 → embedding 向量化 → 向量库 + 元数据入库
【在线查询】用户问题 → embedding → ANN 召回 top-k → (重排) → 拼 prompt → LLM 流式生成 → 带引用返回
【横切】文档生命周期管理 / 召回评估 / 监控
```

### 3.1 离线索引链路

| # | 组件 | 作用 | 关键点 |
|---|---|---|---|
| 1 | 文档解析器 | PDF/Word/HTML/TXT/MD → 纯文本 | 脏活最多的环节（PDF 表格/双栏坑）；**Hify 一期只做 TXT/MD，绕开** |
| 2 | 切块器 | 长文本 → 检索友好的小段 | embedding 输入长度上限、召回粒度、prompt 成本三重约束；**切块质量决定召回上限，后面救不回** |
| 3 | Embedding 模型 | 文本 → 语义向量 | 语义相似度的数学基础；**入库与查询必须同一模型** |
| 4 | 向量存储 + ANN 索引 | 存"向量+原文+元数据"，毫秒级 top-k | HNSW 驻留内存是真实瓶颈，监控索引大小 |
| 5 | 元数据与关系存储 | knowledge_bases/documents/chunks 三层关系、状态机 | 按库过滤、引用溯源、reindex 定位都靠它 |

### 3.2 在线查询链路

| # | 组件 | 作用 | 关键点 |
|---|---|---|---|
| 6 | 查询侧 embedding + 召回器 | 问题 → 向量 → ANN top-k（带 KB 过滤） | 旋钮：`ef_search`（召回率 vs 延迟）、top-k |
| 7 | 重排器（可选） | cross-encoder 粗召回精排 | **一期不做**，纯向量召回够用 |
| 8 | 上下文组装器 | 检索片段拼进 prompt | "资料里没有就说不知道"的指令直接决定幻觉率 |
| 9 | LLM 生成 | 基于资料流式回答 | 走 chat 引擎同一套 SSE/超时/熔断体系 |
| 10 | 引用溯源 | 回答附带来源 chunk | 可信度：能核实答案、能定位是文档过期还是检索错 |

### 3.3 横切组件

- **文档生命周期管理**：上传 / 删除（级联删 chunk）/ reindex（更新后重新切块+向量化）——RAG 相对微调的核心优势（分钟级 vs 重训练）。
- **召回评估与监控**：离线 golden set（"问题 → 已知正确 chunk"样本跑 top-k 命中率，据此调 `ef_search` 与切块参数）；线上命中率与召回延迟。**检索不准是静默失效的**，用户只觉得"答得不对"。

## 4. 组件 → Hify 落地映射

| 组件 | Hify 落地 |
|---|---|
| 解析 + 切块 | `internal/rag/` service：TXT/MD、固定长度分块 |
| Embedding 调用 | 走 provider 模块 api（`rag → provider`），models 表 `capability=embedding` + `embedding_dim` 已预留 |
| 向量存储 + 索引 | PG pgvector：`chunks.embedding vector(N)` + HNSW（`vector_cosine_ops`），建表即建 |
| 召回器 | rag store 原生 SQL（`db.Raw`）+ `SET LOCAL hnsw.ef_search`，按 KB/document_id 过滤 |
| 上下文注入 | chat 引擎 RAG 路径，召回结果拼进多轮上下文 |
| Agent ↔ 知识库绑定 | `agent_knowledge_bases` 表 |
| 生命周期 | `documents` 状态机 + 软删除 + `POST /documents/{id}/reindex` |
| 引用展示 | 对话前端 |
| 评估 | 上线前 golden set 跑 top-k 命中率（CLAUDE.md 明确要求） |

## 5. 一期刻意砍掉的能力（及已知代价）

| 砍掉 | 代价 | 二期动机 |
|---|---|---|
| PDF/Office 解析 | 只支持纯文本文档 | 内部手册多为 MD/TXT，先跑通闭环 |
| 混合检索（BM25 + 向量） | 纯向量召回对精确标识符（产品型号、错误码、政策编号）弱 | 需要时 PG 内 `tsvector` + pgvector 手动融合，不引入 ES |
| 语义切块、rerank | 固定长度 + 不重排 | 用评估数据说话再决定加不加 |

排期钥匙：先打通 1→5（入库）+ 6/8/9（最小查询闭环），跑通后再补评估、引用、reindex 增强件。
