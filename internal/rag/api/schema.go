// schema.go —— rag 模块请求 / 响应 schema 与跨字段校验（spec 03）。
// 纯契约叶子包：binding tag 管字段格式，Validate 管跨字段规则（CLAUDE.md 模块模板）；
// 不 import gin/gorm/platform-page（page 带 gorm，chat/api 同款叶子包纪律）。
// ID 双约定（agent/api 同款）：请求侧 ID/FK 用数字 uint64（前端把字符串 id 转 Number 后提交），
// 响应侧一律字符串（JS 2^53 精度保护）。
package api

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/Karlsk/go-hify/internal/platform/schema"
)

// 应用层常量（spec 03 §2；与 DB CHECK / 前端约束对齐，漂移即坏）。
const (
	// RequiredEmbeddingDim 嵌入向量维度钉死（vector(1536) 建表不可改，换模型=重建 chunks）。
	RequiredEmbeddingDim = 1536
	// MaxKBNameLen 知识库名称上限（uq(name) 业务约束的应用层镜像）。
	MaxKBNameLen = 128
	// MaxKBDescriptionLen 知识库描述上限。
	MaxKBDescriptionLen = 512
	// MaxDocumentNameLen 文档名上限（空 name 回退文件名时同样受限）。
	MaxDocumentNameLen = 255
	// MaxQueryRunes 检索 query 上限（rune 计，中文友好）。
	MaxQueryRunes = 1024
	// MaxRetrieveKBs 单次检索跨 KB 数上限（EmbeddingModelMismatch 校验的前提规模）。
	MaxRetrieveKBs = 10
	// TopKMin / TopKMax 检索 top_k 请求域（service 侧 0=未设取 cfg.TopK 并 clamp）。
	TopKMin = 1
	TopKMax = 20
)

// AllowedUploadExts 上传扩展名白名单（小写比较；取扩展名而非 mime——mime 可伪造，
// spec 03 §2）。与 documents.file_type CHECK('txt','md') 对齐，二期加 pdf 两处同改。
var AllowedUploadExts = []string{".txt", ".md"}

// CreateKnowledgeBaseReq 建库请求；embedding 模型预检在 service（spec 04 §1）。
type CreateKnowledgeBaseReq struct {
	Name             string `json:"name" binding:"required,min=1,max=128"`
	Description      string `json:"description" binding:"omitempty,max=512"`
	EmbeddingModelID uint64 `json:"embedding_model_id" binding:"required"`
}

// Validate 跨字段校验（无跨字段规则，binding tag 管字段格式）。
func (r CreateKnowledgeBaseReq) Validate() error { return nil }

// GetKnowledgeBaseReq 详情请求（路径参数）。
type GetKnowledgeBaseReq struct {
	ID uint64 `uri:"id" binding:"required"`
}

// Validate 跨字段校验（无）。
func (r GetKnowledgeBaseReq) Validate() error { return nil }

// ListKnowledgeBasesReq 列表请求：偏移分页（KB 是小配置表，仓规 OFFSET 例外）+
// name 可选模糊过滤（ILIKE 小表豁免前导通配红线，总览决策 #17）。
type ListKnowledgeBasesReq struct {
	Page     int    `form:"page" binding:"omitempty,min=1"`
	PageSize int    `form:"page_size" binding:"omitempty,min=1,max=100"`
	Name     string `form:"name" binding:"omitempty,max=128"`
}

// Validate 跨字段校验（无）。
func (r ListKnowledgeBasesReq) Validate() error { return nil }

// UpdateKnowledgeBaseReq 整体更新请求（PUT 语义）。ID 带 json:"-"：handler 先用
// GetKnowledgeBaseReq 绑路径 id 再赋值（provider UpdateProviderReq 同款——
// encoding/json 对无 tag 字段按字段名大小写不敏感匹配，裸 ID 会被 body {"id":999} 悄悄改写）。
// embedding_model_id 不可改（维度冻结，换模型=重建 chunks）：本类型不设该字段，
// 报文中出现即被自然忽略（spec 04 §2 端点 4「忽略」路径）。
type UpdateKnowledgeBaseReq struct {
	ID          uint64 `json:"-"`
	Name        string `json:"name" binding:"required,min=1,max=128"`
	Description string `json:"description" binding:"omitempty,max=512"`
	// Enabled 三态：nil=不变；显式 true/false=翻转（agent Update Enabled *bool 同款）。
	Enabled *bool `json:"enabled"`
}

// Validate 跨字段校验：路径 id 由 handler 赋值后必填。
func (r UpdateKnowledgeBaseReq) Validate() error {
	if r.ID == 0 {
		return fmt.Errorf("id 必填")
	}
	return nil
}

// DeleteKnowledgeBaseReq 删除请求（路径参数；硬删，有文档挡删在 service）。
type DeleteKnowledgeBaseReq struct {
	ID uint64 `uri:"id" binding:"required"`
}

// Validate 跨字段校验（无）。
func (r DeleteKnowledgeBaseReq) Validate() error { return nil }

// UploadDocumentReq 上传请求：全部 json:"-" 程序内构造（spec 03 §2）——multipart
// 由 handler 解析后填入，绝不经 JSON body 绑定；Validate 负责归一化回写（BOM/空白/
// Name 回退），故用指针接收器让变异落到调用方变量。
type UploadDocumentReq struct {
	KnowledgeBaseID uint64 `json:"-"`
	// FileName 原始文件名（取扩展名判类型；Name 回退时取 Base 去路径）。
	FileName string `json:"-"`
	// Name 展示名；空则回退文件名，rune 计 ≤255。
	Name    string `json:"-"`
	Content string `json:"-"`
	// FileType 必须等于小写扩展名去点（与 documents.file_type CHECK('txt','md') 对齐）。
	FileType string `json:"-"`
	FileSize int64  `json:"-"`
}

// Validate 跨字段校验与归一化：KB 必填 → 扩展名白名单（小写比较，取扩展名而非
// mime——mime 可伪造）→ FileType 一致性 → Name 回退 ≤255 → Content 去 BOM/首尾空白
// 非空且合法 UTF-8（回写归一化结果）→ FileSize 必为正。
func (r *UploadDocumentReq) Validate() error {
	if r.KnowledgeBaseID == 0 {
		return fmt.Errorf("knowledge_base_id 必填")
	}
	ext := strings.ToLower(filepath.Ext(r.FileName))
	if !slices.Contains(AllowedUploadExts, ext) {
		return fmt.Errorf("file_name 扩展名不在白名单 %v", AllowedUploadExts)
	}
	if r.FileType != strings.TrimPrefix(ext, ".") {
		return fmt.Errorf("file_type 与扩展名不一致")
	}
	if r.Name == "" {
		r.Name = filepath.Base(r.FileName)
	}
	if n := utf8.RuneCountInString(r.Name); n > MaxDocumentNameLen {
		return fmt.Errorf("name 超 %d 字符上限（当前 %d）", MaxDocumentNameLen, n)
	}
	r.Content = strings.TrimSpace(strings.TrimPrefix(r.Content, "\uFEFF"))
	if r.Content == "" {
		return fmt.Errorf("content 不能为空")
	}
	if !utf8.ValidString(r.Content) {
		return fmt.Errorf("content 非法 UTF-8")
	}
	if r.FileSize <= 0 {
		return fmt.Errorf("file_size 必为正")
	}
	return nil
}

// GetDocumentReq 文档详情请求（路径参数）。
type GetDocumentReq struct {
	ID uint64 `uri:"id" binding:"required"`
}

// Validate 跨字段校验（无）。
func (r GetDocumentReq) Validate() error { return nil }

// ListDocumentsReq 文档列表请求：KB 归属由 handler 绑路径后赋值（json:"-" 防覆盖）；
// 游标分页（文档随 KB 增长，keyset 仓规）。
type ListDocumentsReq struct {
	KnowledgeBaseID uint64 `json:"-"`
	Limit           int    `form:"limit" binding:"omitempty,min=1,max=100"`
	Cursor          string `form:"cursor"`
}

// Validate 跨字段校验：KB 归属必填（handler 装配遗漏即在此暴露）。
func (r ListDocumentsReq) Validate() error {
	if r.KnowledgeBaseID == 0 {
		return fmt.Errorf("knowledge_base_id 必填")
	}
	return nil
}

// DeleteDocumentReq 文档删除请求（路径参数；软删文档同事务硬删其 chunks——spec 04）。
type DeleteDocumentReq struct {
	ID uint64 `uri:"id" binding:"required"`
}

// Validate 跨字段校验（无）。
func (r DeleteDocumentReq) Validate() error { return nil }

// ReindexDocumentReq 重建索引请求（路径参数；pending/processing 撞并发在 service 挡）。
type ReindexDocumentReq struct {
	ID uint64 `uri:"id" binding:"required"`
}

// Validate 跨字段校验（无）。
func (r ReindexDocumentReq) Validate() error { return nil }

// ---- 响应 Schema 族（spec 03 §2；ID/外键一律字符串——JS 2^53 精度保护） ----

// KnowledgeBaseSchema KB 响应（创建 / 更新 / 详情）。DocumentCount 详情端点 3 也带
// （创建时为 0）；EmbeddingModelID 为字符串化外键。
type KnowledgeBaseSchema struct {
	schema.BaseSchema
	Name             string `json:"name"`
	Description      string `json:"description"`
	EmbeddingModelID string `json:"embedding_model_id"`
	Enabled          bool   `json:"enabled"`
	DocumentCount    int64  `json:"document_count"`
}

// KnowledgeBaseListItem 列表项：KnowledgeBaseSchema + 当页批量现读的聚合列，
// service List 聚合防 N+1（agent/api AgentListItem 同款）。EmbeddingModelName 为空串
// 表示悬空引用（模型已被删），前端 fallback 显示 id。
type KnowledgeBaseListItem struct {
	KnowledgeBaseSchema
	EmbeddingModelName string `json:"embedding_model_name"`
}

// KnowledgeBaseListResult KB 偏移分页结果。KB 是小配置表，按接口规范走偏移分页
// （keyset 强制规则的例外表）；Items 由 service 保证非 nil。
type KnowledgeBaseListResult struct {
	Items    []KnowledgeBaseListItem `json:"items"`
	Page     int                     `json:"page"`
	PageSize int                     `json:"page_size"`
	Total    int64                   `json:"total"`
}

// DocumentSchema 文档响应（上传 202 / 列表 / reindex 202）。status 四态
// pending/processing/ready/failed 与 DB CHECK 对齐；error_message 仅 failed 非空。
type DocumentSchema struct {
	schema.BaseSchema
	Name         string `json:"name"`
	FileType     string `json:"file_type"`
	FileSize     int64  `json:"file_size"`
	Status       string `json:"status"`
	ChunkCount   int    `json:"chunk_count"`
	ErrorMessage string `json:"error_message"`
}

// DocumentDetailSchema 文档详情：DocumentSchema + Content 原文；chunk_count 直读列，
// 不再计数查询（spec 03 §2）。
type DocumentDetailSchema struct {
	DocumentSchema
	Content string `json:"content"`
}

// DocumentListResult 文档游标分页结果（api 包自声明，不引 platform/page——page 带
// gorm，叶子包纪律）；喂 respond.OKWithCursor。Items 由 service 保证非 nil。
type DocumentListResult struct {
	Items      []DocumentSchema `json:"items"`
	Limit      int             `json:"limit"`
	HasMore    bool            `json:"has_more"`
	NextCursor string          `json:"next_cursor"`
}

// RetrievedChunk 检索命中片段。Similarity = 1 - 余弦距离（越大越近）；DocumentName
// 由二次查询解析，悬空（文档已删）为 ""。
type RetrievedChunk struct {
	ChunkID         string  `json:"chunk_id"`
	DocumentID      string  `json:"document_id"`
	KnowledgeBaseID string  `json:"knowledge_base_id"`
	DocumentName    string  `json:"document_name"`
	ChunkIndex      int     `json:"chunk_index"`
	Content         string  `json:"content"`
	Similarity      float64 `json:"similarity"`
}

// RetrieveReq 检索请求（端点 11 + 跨模块 chat 注入共用）。KBIDs 程序内必经
// （handler 绑路径填单元素；chat 注入 agent 绑定的多 KB），body 注入不生效。
// TopK 0=未设，service 侧取 cfg.TopK 默认并 clamp 到 [TopKMin, TopKMax]。
type RetrieveReq struct {
	Query string   `json:"query" binding:"required,max=1024"`
	TopK  int      `json:"top_k" binding:"omitempty,min=1,max=20"`
	KBIDs []uint64 `json:"-"`
}

// Validate 跨字段校验：query trim 后非空且 rune 计 ≤1024（中文友好）；KBIDs 1..10。
// 无归一化回写（对比 UploadDocumentReq），值接收器即可。
func (r RetrieveReq) Validate() error {
	if strings.TrimSpace(r.Query) == "" {
		return fmt.Errorf("query 不能为空")
	}
	if n := utf8.RuneCountInString(r.Query); n > MaxQueryRunes {
		return fmt.Errorf("query 超 %d 字符上限（当前 %d）", MaxQueryRunes, n)
	}
	if len(r.KBIDs) == 0 {
		return fmt.Errorf("kb_ids 必填（程序内填充）")
	}
	if len(r.KBIDs) > MaxRetrieveKBs {
		return fmt.Errorf("kb_ids 一次最多 %d 个知识库（当前 %d）", MaxRetrieveKBs, len(r.KBIDs))
	}
	return nil
}
