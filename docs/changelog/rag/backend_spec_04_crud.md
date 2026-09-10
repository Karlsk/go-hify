# RAG spec 04 · CRUD（backend_spec_04_crud）

> 状态：**实施 spec**（2026-09-07），8 篇之 04；决策依据见总览 [backend_module_spec.md](backend_module_spec.md)。前置依赖：01（model/基建）、02（embedder 类型）、03（api 契约）。
> 交付：`service/service.go`（KB + 文档 CRUD 编排）+ `store/store.go`（CRUD 数据访问）+ `handler/handler.go`（端点 1-10）。**Store 接口本篇只声明 CRUD 所需子集**——SearchChunks（05）/ 管线方法族（07）各自增补，同包增量演进。**不变量规则 2/3 的执行者**（软删联动删 chunks / reindex 事务重置）；Upload/Reindex 落 pending 后**不接 dispatch**（接线在 07）。

## 1. service/service.go

- `Store` 接口（本篇子集）：
  - **KB 7**：CRUD + `CountAllDocumentsByKB`【Unscoped 含软删——删除护栏】+ `CountDocumentsByKBIDs`【活跃——列表聚合】；List 带 `name ILIKE '%kw%'` 参数化过滤（小表豁免前导通配红线，注释说明；用户输入 `%`/`_` 视作通配符不转义）；
  - **documents 6**：`GetDocumentByID` / `CreateDocument`（pending + 元数据）/ `SoftDeleteDocument`【事务内联动删 chunks】/ keyset `ListDocumentsByKB` / `DeleteChunksByDocument`【规则 2/3 共用】/ `ResetDocumentForReindex`【规则 3：置 pending + 清 error_message + chunk_count=0，软删行不可见】；reindex 事务 = `WithTx{ DeleteChunksByDocument; ResetDocumentForReindex }`（实施澄清：重置是独立 UPDATE 方法，非纯组合）；
  - `WithTx`。
- 下游收窄小接口（chat `llmConfigResolver` 先例）：`embedder`（单方法 EmbedStrings，本篇仅持有不调用——Retrieve 在 05）+ `cacheManager`（Get/Set/Delete）。
- `New(store, models providerapi.ModelService, embeds embedder, cm cacheManager, cfg Config) ragapi.KnowledgeBaseService`——**本篇单返回**；07 加 pipeline 后改 `(api, *Recovery)` 双返回（照 provider `(api, Prober)` 先例）。
- 编排要点：
  - `Create` 模型预检（`models.Get`：capability/dim/enabled；`!Enabled`/不存在透传 provider 哨兵）→ 23505→NameConflict、23503→ModelNotFound；
  - `Get` Cache-Aside（`NameRag` + `detail:%d`，Set 失败仅 WARN）；`Update`（name/desc/enabled）→ Save → 写时 evict（含 enabled 翻转）；
  - `List` 偏移分页 + `models.ListByIDs` 聚合模型名 + `CountDocumentsByKBIDs` 批量计数（防 N+1）；
  - `Delete`：`CountAllDocumentsByKB>0` → InUse（硬删交给 store CASCADE 清 agent 绑定）；
  - `UploadDocument`：`CreateDocument`（pending + FileType/FileSize/Content）→ evict KB detail（DocumentCount 变化，写时失效——实施澄清拍板）→ 返回快照；**无 dispatch 调用行**；
  - `DeleteDocument`：`SoftDeleteDocument`（store 内事务：软删 + DeleteChunksByDocument——不变量规则 2）；
  - `ReindexDocument`：Get（404）→ status ∈ {pending, processing} → ErrDocumentProcessing → `WithTx{ DeleteChunksByDocument; 置 pending + 清 error_message + chunk_count=0 }`（规则 3）→ 返回快照；**无 dispatch 调用行**。
- `isFKViolation`/`isUniqueViolation`（23503/23505）照 agent/service/service.go:305 复制，包内私有。

## 2. store/store.go

- `var _ ragsvc.Store = (*Store)(nil)` 编译期断言（随接口子集增长）；显式列清单 const（**content 大文本仅详情取**，禁 SELECT *）。
- CRUD 照 agent store 模式：RowsAffected==0 → `gorm.ErrRecordNotFound`；`WithTx` 同款。
- `SoftDeleteDocument`：`WithTx{ 软删 documents 行; DeleteChunksByDocument }`——不变量规则 2。
- `ListDocumentsByKB`（keyset）：`Where(kb).Where(id < lastID).Order(id DESC).Limit(FetchN())`。
- `ListKB` 的 name 过滤：`WHERE name ILIKE '%' || ? || '%'` 参数化（或 `ILIKE ?` 由 service 拼 `%kw%`，取其一，测试断言参数形态）。

## 3. handler/handler.go（端点 1-10；11 retrieve 属 05）

| # | 方法 + 路径 | 请求参数 | 成功 | 语义 / 主要错误 |
|---|---|---|---|---|
| 1 | POST `/api/v1/knowledge-bases` | body：`name*`(≤128)、`embedding_model_id*`(字符串)、`description`(≤512) | 201 `KnowledgeBaseSchema` | 建库；模型预检（非 embedding/dim≠1536→400 `EMBEDDING_DIM_MISMATCH`，模型不存在→404，disabled→503）；重名 409 `KNOWLEDGE_BASE_NAME_CONFLICT` |
| 2 | GET `/api/v1/knowledge-bases` | query：`page`(默认1)、`page_size`(默认20上限100)、`name`(可选，ILIKE 模糊) | 200 `[]KnowledgeBaseListItem` + meta `{page,page_size,total}` | 偏移分页（小配置表例外）；聚合 EmbeddingModelName / DocumentCount / Enabled |
| 3 | GET `/api/v1/knowledge-bases/{id}` | — | 200 `KnowledgeBaseSchema`(+DocumentCount) | 404 `KNOWLEDGE_BASE_NOT_FOUND` |
| 4 | PUT `/api/v1/knowledge-bases/{id}` | body：`name`、`description`、`enabled`(*bool，nil=不变) | 200 `KnowledgeBaseSchema` | **embedding_model_id 不可改**（忽略或 400）；404 / 重名 409 |
| 5 | DELETE `/api/v1/knowledge-bases/{id}` | — | **204** | **硬删**；有文档（含软删）→ 409 `KNOWLEDGE_BASE_IN_USE`；CASCADE 清 agent 绑定 |
| 6 | POST `/api/v1/knowledge-bases/{kbId}/documents` | multipart：`file*`(.txt/.md，≤2MB)、`name`(可选 ≤255，空则取文件名) | **202** `DocumentSchema`(status=pending + file_type/file_size) | 类型/大小/非法 UTF-8/空内容→400 `VALIDATION_FAILED`；KB 不存在 404 |
| 7 | GET `/api/v1/knowledge-bases/{kbId}/documents` | query：`limit`(默认20上限100)、`cursor` | 200 `[]DocumentSchema` + meta `{limit,has_more,next_cursor}` | **游标分页**（keyset id DESC）；KB 404 |
| 8 | GET `/api/v1/documents/{id}` | — | 200 `DocumentDetailSchema`(content + chunk_count + error_message) | 404 `DOCUMENT_NOT_FOUND` |
| 9 | DELETE `/api/v1/documents/{id}` | — | **204** | 软删文档 + **同事务硬删 chunks**（不变量规则 2） |
| 10 | POST `/api/v1/documents/{id}/reindex` | — | **202** `DocumentSchema`(status=pending) | pending/processing 中→409 `DOCUMENT_PROCESSING`；事务删 chunks 重跑管线 |

通用约定：`respond.Result` 信封；**id 一律字符串**；时间 RFC 3339 UTC；列表空返 `[]`；上传 202（`respond.Accepted`）不是 201。否决项存档见总览决策表。

handler 要点：

- 两组路由分开注册（`/knowledge-bases/:id/...` 与 `/documents/:id`），组内无 `:id` 与静态段同级冲突。
- `New(kbs ragapi.KnowledgeBaseService, maxUploadBytes int)`——构造参数持上传上限（auth handler 持 CookieSecure 先例）。
- `uploadDocument`（唯一无 BindJSON 先例的端点）：BindUri → ContentLength 预检 → `c.FormFile("file")` → `io.LimitReader` 读（超限 400）→ `c.PostForm("name")` → 从**扩展名**得 file_type、字节数得 file_size → 程序内构造 `UploadDocumentReq` → `Validate()` 失败手写 400 信封 → `respond.Accepted`。
- `failRag` 哨兵显式映射（rag 哨兵 + `errs.ErrValidationFailed`），兜底 `respond.FailFromSentinel`；import 只含本模块 api + platform（respond/errs）+ providerapi（哨兵透传：ModelNotFound 404 / ModelDisabled 503）+ gin + net/http。

## 4. 单测与验收门

| 层 | 重点 |
|---|---|
| service/service_test（stubStore + stub models） | 预检三分支（非 embedding/dim≠1536/disabled）；23505→NameConflict、23503→ModelNotFound 翻译；缓存命中/miss 回填/Set 失败仅 WARN；Delete 计数→InUse；Update evict（含 enabled）；List 聚合（模型名悬空 ""）；**UploadDocument 落 pending 含 FileType/FileSize、不触发任何 dispatch**；Reindex 状态挡（pending/processing→ErrDocumentProcessing）+ 事务序列（删 chunks→置 pending→清 error_message→chunk_count=0）；ListDocuments cursor 组装（has_more/next_cursor）；List name 模糊参数透传 |
| store/store_test（sqlmock 原文 SQL + QuoteMeta） | CRUD SQL 形态（显式列/keyset/新列 file_type~chunk_count）；**ListKB 的 ILIKE 参数**；SoftDeleteDocument 事务序列（软删 + DeleteChunksByDocument 同事务）；RowsAffected==0 → ErrRecordNotFound |
| handler/handler_test（httptest + fakeSvc） | multipart 构造；**202 信封含 status=pending + file_type/file_size**；超大小/坏扩展名/缺 file 400；哨兵→状态码全表（上表错误列逐行）；偏移分页 meta 与 cursor meta；KB 列表 name 查询参数透传 |
| 门 | 三层测试绿（`go test ./internal/rag/... -race`）；`curl` 冒烟留待 08 组装后 |
