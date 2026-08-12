package api

import "errors"

// ErrKnowledgeBaseNotFound 知识库不存在（404）。
var ErrKnowledgeBaseNotFound = errors.New("KNOWLEDGE_BASE_NOT_FOUND")
