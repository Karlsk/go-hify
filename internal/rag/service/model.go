// rag 模块 GORM 实体（模块私有，禁止跨模块传递；json 序列化是 api/schema 的事，model 不打 json tag）。
// 表结构 DDL 由 migrations/ 持有（00004 建表 + 00011 rag 评审增量），本包从不 AutoMigrate。
package service

import (
	"github.com/pgvector/pgvector-go"

	"github.com/Karlsk/go-hify/internal/platform/db"
)

// KnowledgeBase 知识库：绑定一个嵌入模型（维度随模型钉死，vector(1536) 不可改）。
// 无软删——删除即硬删，且被文档（含软删，Unscoped 计数）占用时挡删（ErrKnowledgeBaseInUse）。
type KnowledgeBase struct {
	db.BaseMutable
	Name             string `gorm:"not null"`
	Description      string `gorm:"not null"`
	EmbeddingModelID uint64 `gorm:"not null"`
	// Enabled 启用开关：false = 检索范围静默剔除（service 层过滤），管理面仍可见可编辑。
	// 布尔/数值字段一律不加 gorm default tag（provider 模块踩坑 #1：零值会被 GORM 默认值写回替换）。
	Enabled bool `gorm:"not null"`
}

// TableName 显式表名（全模块约定：GORM 复数化不可靠，一律显式声明）。
func (KnowledgeBase) TableName() string { return "knowledge_bases" }

// 文档四态状态机（spec 01 §2；与 00011 的 ck_documents_status CHECK 逐字对齐）：
// pending(已落库未开工) → processing(分块向量化中) → ready(可召回) / failed(入库失败)。
// 前端轮询此字段显示进度；reindex 显式重置回 pending。
const (
	StatusPending    = "pending"
	StatusProcessing = "processing"
	StatusReady      = "ready"
	StatusFailed     = "failed"
)

// Document 知识库文档：软删保底可恢复元信息 + 原文，向量物理删除省 HNSW 内存（恢复 = 重新 reindex）。
type Document struct {
	db.BaseSoftDelete
	KnowledgeBaseID uint64 `gorm:"not null"`
	Name            string `gorm:"not null"`
	Content         string `gorm:"not null"`
	Status          string `gorm:"not null"`
	// FileType 取上传扩展名（非 mime——可伪造）；一期 txt/md，二期加 pdf 只改 CHECK。
	FileType string `gorm:"not null"`
	// FileSize 文件字节数（上传时记录，展示/统计用）。
	FileSize int64 `gorm:"not null"`
	// ErrorMessage 入库失败原因（status=failed 时非空；应用层截断 500 字符）。
	ErrorMessage string `gorm:"not null"`
	// ChunkCount 分块数量（终态事务与 ready 原子同写；pending/processing/failed 恒 0——单写者保证见 spec 07）。
	ChunkCount int `gorm:"not null"`
}

// TableName 显式表名。
func (Document) TableName() string { return "documents" }

// DocumentChunk 文档分块（append-only）。
//
// 核心不变量（spec 01 §3 定义处，执行者在 spec 04 / 07）：document_chunks 有行
// ⟺ 所属文档 status='ready' 且未软删。三条维护规则：
//  1. 终态事务：入库向量内存组装完成后，一个事务内批量 INSERT + MarkDocumentReady(id, N) 原子翻转；
//  2. 软删文档 ⇒ 同事务硬删其 chunks（元信息软删保底，向量物理删省 HNSW 内存）；
//  3. reindex ⇒ 事务内删 chunks + 置 pending（清 error_message，chunk_count=0）后重跑 pipeline。
//
// KnowledgeBaseID 是冗余 KB 归属（文档不可换 KB，确定不变派生值），检索单表过滤免 JOIN。
type DocumentChunk struct {
	db.BaseAppendOnly
	DocumentID      uint64 `gorm:"not null"`
	KnowledgeBaseID uint64 `gorm:"not null"`
	ChunkIndex      int    `gorm:"not null"`
	Content         string `gorm:"not null"`
	// TokenCount 估算值（ceil(ASCII/4+非ASCII)），非 API 精确计数；供 chat 注入的上下文预算参考。
	TokenCount int `gorm:"not null"`
	// Embedding 向量（Valuer/Scanner 直通 []float32）；DDL 在 migrations，type tag 仅为声明性标注。
	// 维度 1536 随嵌入模型钉死（建 KB 时校验模型 dim==1536）。
	Embedding pgvector.Vector `gorm:"type:vector(1536)"`
}

// TableName 显式表名。
func (DocumentChunk) TableName() string { return "document_chunks" }

// ChunkHit 是向量召回（db.Raw + Scan）的目标行，非 GORM 实体：
// id/document_id/knowledge_base_id/chunk_index/content/token_count/distance。
// 无 name——文档名称走二次小查询（WHERE id IN）解析，不进 ANN SQL。
type ChunkHit struct {
	ID              uint64
	DocumentID      uint64
	KnowledgeBaseID uint64
	ChunkIndex      int
	Content         string
	TokenCount      int
	// Distance 余弦距离（<=>），越小越相似。
	Distance float64
}
