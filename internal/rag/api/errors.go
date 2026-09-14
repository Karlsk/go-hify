package api

import "errors"

// rag 模块哨兵错误：code = Error() 字符串（MODULE_REASON 机器码，进响应 error.code），
// 一律用 errors.Is 判断（CLAUDE.md《接口规范》）。
var (
	// ErrKnowledgeBaseNotFound 知识库不存在（404）。
	ErrKnowledgeBaseNotFound = errors.New("KNOWLEDGE_BASE_NOT_FOUND")
	// ErrKnowledgeBaseNameConflict 知识库名称唯一约束冲突（409，uq(name) 23505 翻译）。
	ErrKnowledgeBaseNameConflict = errors.New("KNOWLEDGE_BASE_NAME_CONFLICT")
	// ErrDocumentNotFound 文档不存在（404）。
	ErrDocumentNotFound = errors.New("DOCUMENT_NOT_FOUND")
	// ErrDocumentProcessing 文档入库进行中（pending/processing），reindex / 停用 / 启用撞并发（409）。
	ErrDocumentProcessing = errors.New("DOCUMENT_PROCESSING")
	// ErrEmbeddingModelMismatch 多 KB 检索嵌入模型不一致（400，向量空间可比性前提）。
	ErrEmbeddingModelMismatch = errors.New("EMBEDDING_MODEL_MISMATCH")
	// ErrEmbeddingDimMismatch 嵌入模型 capability 非 embedding 或 dim≠1536（400，%w 带上下文）。
	ErrEmbeddingDimMismatch = errors.New("EMBEDDING_DIM_MISMATCH")
)
