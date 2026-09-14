# RAG spec 03 · 接口定义（backend_spec_03_api_contract）

> 状态：**实施 spec**（2026-09-07；2026-09-14 修订：软删退役——`Delete` 改级联真删（挡删 InUse 哨兵移除）、`DeleteDocument` 改真删、增补 `DisableDocument`/`EnableDocument`，12 → 13 方法），8 篇之 03；决策依据见总览 [backend_module_spec.md](backend_module_spec.md)。前置依赖：01（schema 引用 status 四态等语义）。
> 交付：`internal/rag/api/` 三文件填实（占位文件已在）——**纯契约，api 先行，独立单测**（不依赖 service/store/handler）。

## 1. api/api.go —— `KnowledgeBaseService` 接口（13 方法；2026-09-14 修订 12 → 13）

签名恒 `(ctx context.Context, req XxxReq) (*XxxSchema, error)`：

- **KB**：`Create`（embedding 模型预检）/ `Get`（详情含文档计数，Cache-Aside）/ `List`（偏移分页 + name ILIKE 模糊过滤 + 聚合）/ `Update`（name/description/**enabled**（`*bool`，nil 不变）；**embedding_model_id 不可改**——维度冻结，换模型=重建 chunks）/ `Delete`（级联真删：单事务 chunks→documents→KB 行；2026-09-14 修订，挡删 InUse 退役）。
- **文档**：`UploadDocument`（落 pending 行后异步 pipeline，返回快照含 file_type/file_size）/ `GetDocument`（含原文）/ `ListDocuments`（KB 下游标分页 keyset id DESC，停用行可见）/ `DeleteDocument`（**真删：documents 行 + chunks 同事务**，不变量规则 2）/ `DisableDocument`（深度停用：删向量保内容、chunk_count=0；幂等）/ `EnableDocument`（重新启用：重置 pending + 自动重跑管线；幂等路径不重跑防误花 embedding 费）/ `ReindexDocument`（事务内删 chunks + 置 pending + 重跑；进行中→ErrDocumentProcessing；已停用→400）。文档语义均为 2026-09-14 修订。
- **检索**：`Retrieve(ctx, RetrieveReq) ([]RetrievedChunk, error)`——query 向量化 → pgvector 余弦 top-k，跨多 KB（为 chat/workflow 预留；HTTP 单 KB 路由复用）。

## 2. api/schema.go

- 常量：`RequiredEmbeddingDim=1536`、`MaxKBNameLen=128`、`MaxKBDescriptionLen=512`、`MaxDocumentNameLen=255`、`MaxQueryRunes=1024`、`MaxRetrieveKBs=10`、`TopKMin/Max=1/20`、上传扩展名白名单 `.txt/.md`（小写比较）。
- 响应：`KnowledgeBaseSchema`（embed `schema.BaseSchema`，外键字符串化，**+Enabled**）、`KnowledgeBaseListItem`（+EmbeddingModelName 悬空为 ""、DocumentCount 批量计数防 N+1）、`DocumentSchema`（**status 四态 / error_message / file_type / file_size / chunk_count**）、`DocumentDetailSchema`（+Content；chunk_count 直读列，不再计数查询）、`DocumentListResult`（api 包自声明不引 page，喂 OKWithCursor）、`RetrievedChunk{ChunkID/DocumentID/KnowledgeBaseID 字符串化, DocumentName, ChunkIndex, Content, Similarity}`（Similarity = 1 - 余弦距离；DocumentName 由二次查询解析）。
- 请求：`Create/UpdateKnowledgeBaseReq`（binding + Validate；Update 含 `Enabled *bool`；`UpdateReq.ID json:"-"` 防路径覆盖）；`ListKnowledgeBasesReq`（BindQuery：page/page_size + name 可选模糊）；`UploadDocumentReq`（全部 `json:"-"` 程序内构造——multipart 由 handler 解析，**含 FileType/FileSize**；Validate：扩展名白名单、Name 空则取文件名 ≤255、Content 去 BOM/首尾空白非空且 utf8.ValidString）；`ListDocumentsReq`（limit 1-100 + cursor）；`RetrieveReq`（query ≤1024 rune、top_k 可选默认 5；kb_ids 1..10 程序内必经）。

## 3. api/errors.go（新增哨兵，不变）

| 哨兵 | HTTP | 场景 |
|---|---|---|
| `ErrKnowledgeBaseNameConflict` | 409 | uq(name) 23505 翻译 |
| ~~`ErrKnowledgeBaseInUse`~~ | ~~409~~ | **2026-09-14 修订移除**：KB Delete 改级联真删，挡删语义退役 |
| `ErrDocumentNotFound` | 404 | |
| `ErrDocumentProcessing` | 409 | reindex 撞 pending/processing |
| `ErrEmbeddingModelMismatch` | 400 | 多 KB 检索嵌入模型不一致（向量空间可比性前提） |
| `ErrEmbeddingDimMismatch` | 400 | capability 非 embedding 或 dim≠1536（`%w` 带上下文） |

文件类型/大小/空内容/非法 UTF-8 走 `errs.ErrValidationFailed`（400 + details），不设哨兵。disabled KB 参与检索时**静默剔除**，不是错误。

## 4. 单测与验收门

| 项 | 内容 |
|---|---|
| api/schema_test | Validate 表驱动：扩展名白名单（大小写 / .pdf 拒）/ 空内容 / Name 回退文件名 / query 与 kb_ids 边界 / 常量钉死 1536；Update 的 `Enabled *bool` nil 语义 |
| api/api_test | **方法集钉死测试**（reflect 逐字比对名称 × 签名，恰好 13 个——2026-09-14 修订后）；增删方法必须先走 spec 修订 |
| 门 | api 包独立编译 + schema_test 绿（`go test ./internal/rag/api/`）；不 import gin/gorm（叶子包纪律） |
