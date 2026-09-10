package store

import (
	"context"
	"database/sql/driver"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/Karlsk/go-hify/internal/platform/page"
	ragsvc "github.com/Karlsk/go-hify/internal/rag/service"
)

// store 测试：sqlmock 注入 mock DB，验证 GORM 生成的 SQL 形态（显式列 / 软删过滤 /
// ILIKE 参数透传 / keyset / 事务序列 / Unscoped 计数）与错误原样上抛；业务翻译
// （哨兵 / 23505 / 23503）在 service 层测试覆盖。

func newMockDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
	t.Helper()
	mockDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: mockDB}), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm open: %v", err)
	}
	t.Cleanup(func() { mockDB.Close() })
	return db, mock
}

// 列清单（与 store.go 的 selectKB / selectDocument 一致，供 NewRows 用）。
var (
	kbCols = []string{"id", "name", "description", "embedding_model_id", "enabled", "created_at", "updated_at"}
	// docCols 列表列（无 content——大文本仅详情路径取）。
	docCols = []string{"id", "knowledge_base_id", "name", "status", "file_type", "file_size", "error_message", "chunk_count", "created_at", "updated_at", "deleted_at"}
)

// kbVals 展开为 knowledge_bases 一行（列序 = kbCols）。
func kbVals(id, modelID uint64, now time.Time) []driver.Value {
	return []driver.Value{id, "产品手册", "内部文档", modelID, true, now, now}
}

// docVals 展开为 documents 一行（列序 = docCols；chunkCount 变体由参数带入）。
func docVals(id, kbID uint64, status string, chunkCount int, now time.Time) []driver.Value {
	return []driver.Value{id, kbID, "manual.txt", status, "txt", int64(24), "", chunkCount, now, now, nil}
}

// 期望 SQL（与 GORM 实际生成逐字比对；QuoteMeta 防正则元字符误匹配）。
const (
	getKBByIDSQL = `SELECT id, name, description, embedding_model_id, enabled, created_at, updated_at FROM "knowledge_bases" WHERE "knowledge_bases"."id" = $1 ORDER BY "knowledge_bases"."id" LIMIT $2`

	listKBsCountSQL = `SELECT count(*) FROM "knowledge_bases"`
	listKBsPageSQL  = `SELECT id, name, description, embedding_model_id, enabled, created_at, updated_at FROM "knowledge_bases" ORDER BY id LIMIT $1`

	// name 过滤：通配符拼接在 SQL 侧，参数 = 原始关键字（用户输入 %/_ 不转义，spec 04 §1）。
	listKBsCountNameSQL = `SELECT count(*) FROM "knowledge_bases" WHERE name ILIKE '%' || $1 || '%'`
	listKBsPageNameSQL  = `SELECT id, name, description, embedding_model_id, enabled, created_at, updated_at FROM "knowledge_bases" WHERE name ILIKE '%' || $1 || '%' ORDER BY id LIMIT $2`

	deleteKBSQL = `DELETE FROM "knowledge_bases" WHERE "knowledge_bases"."id" = $1`

	// Unscoped：含软删行（删除护栏判据），无 deleted_at 过滤。
	countAllDocsSQL = `SELECT count(*) FROM "documents" WHERE knowledge_base_id = $1`

	countDocsByKBIDsSQL = `SELECT knowledge_base_id, COUNT(*) AS cnt FROM "documents" WHERE knowledge_base_id IN ($1,$2) AND "documents"."deleted_at" IS NULL GROUP BY "knowledge_base_id"`

	getDocByIDSQL = `SELECT id, knowledge_base_id, name, content, status, file_type, file_size, error_message, chunk_count, created_at, updated_at, deleted_at FROM "documents" WHERE "documents"."id" = $1 AND "documents"."deleted_at" IS NULL ORDER BY "documents"."id" LIMIT $2`

	listDocsFirstSQL = `SELECT id, knowledge_base_id, name, status, file_type, file_size, error_message, chunk_count, created_at, updated_at, deleted_at FROM "documents" WHERE knowledge_base_id = $1 AND "documents"."deleted_at" IS NULL ORDER BY id DESC LIMIT $2`

	listDocsBeforeSQL = `SELECT id, knowledge_base_id, name, status, file_type, file_size, error_message, chunk_count, created_at, updated_at, deleted_at FROM "documents" WHERE knowledge_base_id = $1 AND id < $2 AND "documents"."deleted_at" IS NULL ORDER BY id DESC LIMIT $3`

	softDeleteDocSQL = `UPDATE "documents" SET "deleted_at"=$1 WHERE "documents"."id" = $2 AND "documents"."deleted_at" IS NULL`

	deleteChunksSQL = `DELETE FROM "document_chunks" WHERE document_id = $1`
)

// ---- knowledge_bases ----

func TestCreateKnowledgeBase(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "knowledge_bases"`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow(1, now, now))
	mock.ExpectCommit()

	kb := &ragsvc.KnowledgeBase{Name: "产品手册", EmbeddingModelID: 5, Enabled: true}
	err := s.CreateKnowledgeBase(context.Background(), kb)
	assert.NoError(t, err)
	assert.Equal(t, uint64(1), kb.ID) // RETURNING 回填主键
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateKnowledgeBaseError(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "knowledge_bases"`)).
		WillReturnError(errors.New("unique violation"))
	mock.ExpectRollback()

	err := s.CreateKnowledgeBase(context.Background(), &ragsvc.KnowledgeBase{Name: "x"})
	assert.Error(t, err) // 原样上抛，翻译在 service 层
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetKnowledgeBaseByID(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(getKBByIDSQL)).
		WithArgs(uint64(1), 1).
		WillReturnRows(sqlmock.NewRows(kbCols).AddRow(kbVals(1, 5, now)...))

	kb, err := s.GetKnowledgeBaseByID(context.Background(), 1)
	assert.NoError(t, err)
	assert.Equal(t, "产品手册", kb.Name)
	assert.Equal(t, uint64(5), kb.EmbeddingModelID)
	assert.True(t, kb.Enabled)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetKnowledgeBaseByIDNotFound(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectQuery(regexp.QuoteMeta(getKBByIDSQL)).
		WithArgs(uint64(999), 1).
		WillReturnError(gorm.ErrRecordNotFound)

	_, err := s.GetKnowledgeBaseByID(context.Background(), 999)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListKnowledgeBases(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(listKBsCountSQL)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectQuery(regexp.QuoteMeta(listKBsPageSQL)).
		WithArgs(20).
		WillReturnRows(sqlmock.NewRows(kbCols).
			AddRow(kbVals(1, 5, now)...).
			AddRow(kbVals(2, 6, now)...))

	res, err := s.ListKnowledgeBases(context.Background(), page.NewOffset(1, 20), "")
	assert.NoError(t, err)
	assert.Len(t, res.Items, 2)
	assert.Equal(t, int64(2), res.Total)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// name 过滤：ILIKE 通配符在 SQL 侧拼接，参数 = 原始关键字原样透传（含 % 也原样）。
func TestListKnowledgeBasesNameFilter(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(listKBsCountNameSQL)).
		WithArgs("%产品").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(regexp.QuoteMeta(listKBsPageNameSQL)).
		WithArgs("%产品", 20).
		WillReturnRows(sqlmock.NewRows(kbCols).AddRow(kbVals(1, 5, now)...))

	res, err := s.ListKnowledgeBases(context.Background(), page.NewOffset(1, 20), "%产品")
	assert.NoError(t, err)
	assert.Len(t, res.Items, 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateKnowledgeBase(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	// Save 全量 UPDATE（PUT 语义，零值一并覆盖）；参数序不定，故不校参。
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE "knowledge_bases" SET`)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	kb := &ragsvc.KnowledgeBase{Name: "改名", Description: "新描述", EmbeddingModelID: 5, Enabled: false}
	kb.ID = 1
	err := s.UpdateKnowledgeBase(context.Background(), kb)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteKnowledgeBase(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	// 硬删（KB 无软删）：普通 DELETE，无 deleted_at 过滤。
	mock.ExpectExec(regexp.QuoteMeta(deleteKBSQL)).
		WithArgs(uint64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := s.DeleteKnowledgeBase(context.Background(), 1)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteKnowledgeBaseNotFound(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(deleteKBSQL)).
		WithArgs(uint64(999)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	err := s.DeleteKnowledgeBase(context.Background(), 999)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCountAllDocumentsByKB(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	// Unscoped：SQL 不带 deleted_at 过滤（含软删行——挡删判据）。
	mock.ExpectQuery(regexp.QuoteMeta(countAllDocsSQL)).
		WithArgs(uint64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))

	n, err := s.CountAllDocumentsByKB(context.Background(), 1)
	assert.NoError(t, err)
	assert.Equal(t, int64(3), n)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCountDocumentsByKBIDs(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectQuery(regexp.QuoteMeta(countDocsByKBIDsSQL)).
		WithArgs(uint64(1), uint64(2)).
		WillReturnRows(sqlmock.NewRows([]string{"knowledge_base_id", "cnt"}).AddRow(1, 3))

	counts, err := s.CountDocumentsByKBIDs(context.Background(), []uint64{1, 2})
	assert.NoError(t, err)
	assert.Equal(t, map[uint64]int64{1: 3}, counts) // 无键 = 0
	assert.NoError(t, mock.ExpectationsWereMet())
}

// 空 id 列表零 SQL（IN () 非法）。
func TestCountDocumentsByKBIDsEmpty(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)

	counts, err := s.CountDocumentsByKBIDs(context.Background(), nil)
	assert.NoError(t, err)
	assert.Empty(t, counts)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---- documents ----

func TestCreateDocument(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "documents"`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow(100, now, now))
	mock.ExpectCommit()

	d := &ragsvc.Document{KnowledgeBaseID: 1, Name: "使用手册", Content: "正文内容", Status: ragsvc.StatusPending, FileType: "txt", FileSize: 12}
	err := s.CreateDocument(context.Background(), d)
	assert.NoError(t, err)
	assert.Equal(t, uint64(100), d.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetDocumentByID(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	rows := sqlmock.NewRows([]string{"id", "knowledge_base_id", "name", "content", "status", "file_type", "file_size", "error_message", "chunk_count", "created_at", "updated_at", "deleted_at"}).
		AddRow(9, 1, "manual.txt", "正文内容", ragsvc.StatusReady, "txt", int64(24), "", 8, now, now, nil)
	mock.ExpectQuery(regexp.QuoteMeta(getDocByIDSQL)).
		WithArgs(uint64(9), 1).
		WillReturnRows(rows)

	d, err := s.GetDocumentByID(context.Background(), 9)
	assert.NoError(t, err)
	assert.Equal(t, "正文内容", d.Content, "详情路径取 content 大文本")
	assert.Equal(t, ragsvc.StatusReady, d.Status)
	assert.Equal(t, 8, d.ChunkCount)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetDocumentByIDNotFound(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectQuery(regexp.QuoteMeta(getDocByIDSQL)).
		WithArgs(uint64(999), 1).
		WillReturnError(gorm.ErrRecordNotFound)

	_, err := s.GetDocumentByID(context.Background(), 999)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListDocumentsByKBFirstPage(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	// 首页（beforeID=0）：无 id < 条件；id DESC；FetchN = limit+1。
	mock.ExpectQuery(regexp.QuoteMeta(listDocsFirstSQL)).
		WithArgs(uint64(1), 21).
		WillReturnRows(sqlmock.NewRows(docCols).
			AddRow(docVals(3, 1, ragsvc.StatusReady, 8, now)...).
			AddRow(docVals(2, 1, ragsvc.StatusPending, 0, now)...))

	docs, err := s.ListDocumentsByKB(context.Background(), 1, 0, 21)
	assert.NoError(t, err)
	assert.Len(t, docs, 2)
	assert.Equal(t, uint64(3), docs[0].ID, "id DESC 首行")
	assert.Equal(t, "", docs[0].Content, "列表路径不取 content（TOAST 零成本）")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListDocumentsByKBBeforeID(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(listDocsBeforeSQL)).
		WithArgs(uint64(1), uint64(2), 21).
		WillReturnRows(sqlmock.NewRows(docCols).AddRow(docVals(1, 1, ragsvc.StatusReady, 5, now)...))

	docs, err := s.ListDocumentsByKB(context.Background(), 1, 2, 21)
	assert.NoError(t, err)
	assert.Len(t, docs, 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// 软删事务序列：软删 documents 行 → 同事务硬删 chunks → 提交（不变量规则 2）。
func TestSoftDeleteDocument(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(softDeleteDocSQL)).
		WithArgs(sqlmock.AnyArg(), uint64(9)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(deleteChunksSQL)).
		WithArgs(uint64(9)).
		WillReturnResult(sqlmock.NewResult(0, 5))
	mock.ExpectCommit()

	err := s.SoftDeleteDocument(context.Background(), 9)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// 并发已删：软删 0 行 → 整笔回滚 + NotFound（chunks 不被误删）。
func TestSoftDeleteDocumentNotFound(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(softDeleteDocSQL)).
		WithArgs(sqlmock.AnyArg(), uint64(999)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	err := s.SoftDeleteDocument(context.Background(), 999)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteChunksByDocument(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(deleteChunksSQL)).
		WithArgs(uint64(9)).
		WillReturnResult(sqlmock.NewResult(0, 5))
	mock.ExpectCommit()

	err := s.DeleteChunksByDocument(context.Background(), 9)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// 重置：status/error_message/chunk_count + updated_at（autoUpdateTime）；软删行不可见。
func TestResetDocumentForReindex(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	// map Updates 列序不定，不校参。
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE "documents" SET`)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := s.ResetDocumentForReindex(context.Background(), 9)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestResetDocumentForReindexNotFound(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE "documents" SET`)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	err := s.ResetDocumentForReindex(context.Background(), 999)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestWithTx(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(deleteChunksSQL)).
		WithArgs(uint64(9)).
		WillReturnResult(sqlmock.NewResult(0, 5))
	mock.ExpectCommit()

	// fn 内拿到的 tx Store 仍是 service.Store 身份（reindex 的删 chunks 步骤形态）。
	err := s.WithTx(context.Background(), func(tx ragsvc.Store) error {
		return tx.DeleteChunksByDocument(context.Background(), 9)
	})
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestWithTxRollback(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	boom := errors.New("fn failed")
	mock.ExpectBegin()
	mock.ExpectRollback()

	err := s.WithTx(context.Background(), func(tx ragsvc.Store) error {
		return boom // fn 出错整笔回滚（删 chunks + 重置同生共死）
	})
	assert.ErrorIs(t, err, boom)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---- spec 05：检索（SearchChunks / GetDocumentMetasByIDs） ----

// chunkHitCols SearchChunks 返回列（与 ChunkHit 字段 snake_case 对应）。
var chunkHitCols = []string{"id", "document_id", "knowledge_base_id", "chunk_index", "content", "token_count", "distance"}

// getDocMetasSQL 引用名解析（spec 05 §2）：只取 id/name，不碰 content；
// 软删行被 DeletedAt 过滤自然成"悬空"（无键 = ""）。
const getDocMetasSQL = `SELECT id, name FROM "documents" WHERE id IN ($1,$2) AND "documents"."deleted_at" IS NULL`

// TestGetDocumentMetasByIDs IN 展开两参；悬空（软删 / 不存在）无键。
func TestGetDocumentMetasByIDs(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectQuery(regexp.QuoteMeta(getDocMetasSQL)).
		WithArgs(uint64(9), uint64(10)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(9, "manual.txt"))

	metas, err := s.GetDocumentMetasByIDs(context.Background(), []uint64{9, 10})
	assert.NoError(t, err)
	assert.Equal(t, map[uint64]string{9: "manual.txt"}, metas, "10 号悬空无键")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestGetDocumentMetasByIDsEmpty 空 id 列表零 SQL（IN () 非法）。
func TestGetDocumentMetasByIDsEmpty(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)

	metas, err := s.GetDocumentMetasByIDs(context.Background(), nil)
	assert.NoError(t, err)
	assert.Empty(t, metas)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// searchChunksWantSQL spec 05 §2 冻结形态：单表（无 JOIN / 无 documents 表）、显式列 +
// 余弦距离、kbIDs IN 展开、embedding IS NOT NULL 纯防御、ORDER BY 表达式本体（与
// vector_cosine_ops 配对铁律）、LIMIT。向量参数传两次（SELECT 列 + ORDER BY）。
const searchChunksWantSQL = `SELECT id, document_id, knowledge_base_id, chunk_index, content, token_count, (embedding <=> $1) AS distance FROM document_chunks WHERE knowledge_base_id IN ($2,$3) AND embedding IS NOT NULL ORDER BY embedding <=> $4 LIMIT $5`

// searchChunkSQLFrozen 冻结断言：SQL 无 JOIN、无 documents 表字样（单表召回）。
func TestSearchChunkSQLFrozen(t *testing.T) {
	assert.NotContains(t, searchChunksWantSQL, "JOIN", "单表查询，不 JOIN documents")
	assert.NotContains(t, searchChunksWantSQL, "documents", "documents 表不进 ANN SQL（kb_id 冗余免 JOIN）")
}

// TestSearchChunks 事务序列（spec 05 §2 / §4）：Begin → SET LOCAL ef_search（Exec）→
// 单表 ANN Query（向量参数两次、kbIDs IN 展开、LIMIT）→ Commit。
func TestSearchChunks(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	q := []float32{0.1, 0.2}
	vec, err := pgvector.NewVector(q).Value() // 传参形态：driver.Valuer → "[0.1,0.2]"
	require.NoError(t, err)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`SET LOCAL hnsw.ef_search = 80`)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta(searchChunksWantSQL)).
		WithArgs(vec, uint64(1), uint64(2), vec, 5).
		WillReturnRows(sqlmock.NewRows(chunkHitCols).
			AddRow(9, 100, 1, 0, "正文一", 12, 0.25).
			AddRow(10, 100, 1, 1, "正文二", 10, 0.5))
	mock.ExpectCommit()

	hits, err := s.SearchChunks(context.Background(), []uint64{1, 2}, q, 5, 80)
	assert.NoError(t, err)
	require.Len(t, hits, 2)
	assert.Equal(t, uint64(9), hits[0].ID)
	assert.Equal(t, uint64(100), hits[0].DocumentID)
	assert.Equal(t, "正文一", hits[0].Content)
	assert.InDelta(t, 0.25, hits[0].Distance, 1e-9, "distance 直达 ChunkHit")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestSearchChunksSetLocalError SET LOCAL 失败 → 整笔回滚，错误原样上抛（翻译在 service）。
func TestSearchChunksSetLocalError(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	boom := errors.New("set local failed")
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`SET LOCAL hnsw.ef_search = 80`)).
		WillReturnError(boom)
	mock.ExpectRollback()

	_, err := s.SearchChunks(context.Background(), []uint64{1}, []float32{0.1}, 5, 80)
	assert.ErrorIs(t, err, boom)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestSearchChunksQueryError ANN 查询失败 → 回滚，错误原样上抛（向量参数仍传两次）。
func TestSearchChunksQueryError(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	q := []float32{0.1}
	vec, err := pgvector.NewVector(q).Value()
	require.NoError(t, err)
	boom := errors.New("ann query failed")
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`SET LOCAL hnsw.ef_search = 80`)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta(searchChunksWantSQL)).
		WithArgs(vec, uint64(1), uint64(2), vec, 5).
		WillReturnError(boom)
	mock.ExpectRollback()

	_, err = s.SearchChunks(context.Background(), []uint64{1, 2}, q, 5, 80)
	assert.ErrorIs(t, err, boom)
	assert.NoError(t, mock.ExpectationsWereMet())
}
