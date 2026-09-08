// Package api 是 rag 模块的契约层（纯包，无实现）：跨模块调用接口、Req/Schema、
// Validate 与哨兵错误。跨模块调用与 HTTP 请求复用同一套接口；实现在 service 包，
// 由组合根注入 handler 与上游模块（chat / workflow 的检索注入）。
package api

import "context"

// KnowledgeBaseService 是 rag 模块对外的唯一接口（spec 03 §1，11 方法）。方法签名固定
// (ctx context.Context, req XxxReq) (*XxxSchema, error)。
type KnowledgeBaseService interface {
	// Create 建库（embedding 模型预检：非 embedding / dim≠1536 → ErrEmbeddingDimMismatch）。
	// 错误：providerapi.ErrModelNotFound、providerapi.ErrModelInUse 之外的模型侧哨兵透传、
	// ErrKnowledgeBaseNameConflict（uq(name) 23505）。
	Create(ctx context.Context, req CreateKnowledgeBaseReq) (*KnowledgeBaseSchema, error)

	// Get 详情（含 DocumentCount，Cache-Aside）。
	// 错误：ErrKnowledgeBaseNotFound。
	Get(ctx context.Context, req GetKnowledgeBaseReq) (*KnowledgeBaseSchema, error)

	// List 偏移分页（小配置表例外）+ name ILIKE 模糊过滤 + 聚合
	// （EmbeddingModelName 批量现读防 N+1，悬空为 ""）。
	List(ctx context.Context, req ListKnowledgeBasesReq) (*KnowledgeBaseListResult, error)

	// Update 整体更新（PUT 语义）：name / description / enabled（*bool，nil 不变）；
	// embedding_model_id 不可改（维度冻结，换模型 = 重建 chunks），报文出现即忽略。
	// 错误：ErrKnowledgeBaseNotFound、ErrKnowledgeBaseNameConflict。
	Update(ctx context.Context, req UpdateKnowledgeBaseReq) (*KnowledgeBaseSchema, error)

	// Delete 硬删；KB 下仍有文档（含软删）→ 挡删。
	// 错误：ErrKnowledgeBaseNotFound、ErrKnowledgeBaseInUse。
	Delete(ctx context.Context, req DeleteKnowledgeBaseReq) error

	// UploadDocument 落 pending 行后异步跑入库管线，返回快照（status=pending +
	// file_type / file_size；UploadDocumentReq.Validate 已在 handler 侧归一化）。
	// 错误：ErrKnowledgeBaseNotFound。
	UploadDocument(ctx context.Context, req UploadDocumentReq) (*DocumentSchema, error)

	// GetDocument 详情（含 Content 原文；chunk_count 直读列）。
	// 错误：ErrDocumentNotFound。
	GetDocument(ctx context.Context, req GetDocumentReq) (*DocumentDetailSchema, error)

	// ListDocuments KB 下文档游标分页（keyset id DESC；软删行不可见）。
	// 错误：ErrKnowledgeBaseNotFound。
	ListDocuments(ctx context.Context, req ListDocumentsReq) (*DocumentListResult, error)

	// DeleteDocument 软删文档 + 同事务硬删其 chunks（核心不变量规则 2，spec 01 §3）。
	// 错误：ErrDocumentNotFound。
	DeleteDocument(ctx context.Context, req DeleteDocumentReq) error

	// ReindexDocument 事务内删 chunks + 置 pending 重跑入库管线；
	// pending / processing 中 → 挡并发。
	// 错误：ErrDocumentNotFound、ErrDocumentProcessing。
	ReindexDocument(ctx context.Context, req ReindexDocumentReq) (*DocumentSchema, error)

	// Retrieve 检索：query 向量化 → pgvector 余弦 top-k，跨多 KB（为 chat / workflow
	// 预留；HTTP 单 KB 路由复用同一契约）。disabled KB 静默剔除（全剔除返回 []）；
	// 混嵌入模型 → ErrEmbeddingModelMismatch；embedding 侧哨兵原样透传
	// （llm.ErrEmbeddingUnsupported / ErrProviderBusy / RateLimited 等）。
	Retrieve(ctx context.Context, req RetrieveReq) ([]RetrievedChunk, error)
}
