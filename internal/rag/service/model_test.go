package service

import (
	"testing"

	"github.com/pgvector/pgvector-go"
)

// spec 01 §4：三实体表名显式声明（GORM 复数化不可靠，全模块显式 TableName）；
// ChunkHit 是 Raw Scan 目标非 GORM 实体，无 TableName。
func TestModelTableNames(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"KnowledgeBase", KnowledgeBase{}.TableName(), "knowledge_bases"},
		{"Document", Document{}.TableName(), "documents"},
		{"DocumentChunk", DocumentChunk{}.TableName(), "document_chunks"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s.TableName() = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

// 文档四态常量钉死（与迁移 00011 的 ck_documents_status CHECK 逐字对齐，
// status 是前端轮询进度的契约值，漂移即坏）。
func TestDocumentStatusConstants(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"StatusPending", StatusPending, "pending"},
		{"StatusProcessing", StatusProcessing, "processing"},
		{"StatusReady", StatusReady, "ready"},
		{"StatusFailed", StatusFailed, "failed"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

// 构造走查：实体字段齐备（编译期契约，字段缺失/改名即编译失败）；
// Embedding 为 pgvector.Vector（Valuer/Scanner 直通 []float32）。
func TestModelConstruction(t *testing.T) {
	kb := KnowledgeBase{
		Name:             "产品手册",
		Description:      "内部知识库",
		EmbeddingModelID: 1,
		Enabled:          true,
	}
	if !kb.Enabled {
		t.Error("KnowledgeBase.Enabled should be settable")
	}

	doc := Document{
		KnowledgeBaseID: 1,
		Name:            "manual.txt",
		Content:         "正文",
		Status:          StatusPending,
		FileType:        "txt",
		FileSize:        6,
		ErrorMessage:    "",
		ChunkCount:      0,
	}
	if doc.Status != StatusPending {
		t.Errorf("Document.Status = %q, want pending", doc.Status)
	}

	chunk := DocumentChunk{
		DocumentID:      1,
		KnowledgeBaseID: 1,
		ChunkIndex:      1,
		Content:         "块内容",
		TokenCount:      4,
		Embedding:       pgvector.NewVector([]float32{0.1, 0.2, 0.3}),
	}
	if len(chunk.Embedding.Slice()) != 3 {
		t.Errorf("Embedding len = %d, want 3", len(chunk.Embedding.Slice()))
	}

	hit := ChunkHit{
		ID:              1,
		DocumentID:      1,
		KnowledgeBaseID: 1,
		ChunkIndex:      1,
		Content:         "命中块",
		TokenCount:      4,
		Distance:        0.125,
	}
	if hit.Distance != 0.125 {
		t.Errorf("ChunkHit.Distance = %v, want 0.125", hit.Distance)
	}
}
