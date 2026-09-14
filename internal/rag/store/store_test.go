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

// store 测试：sqlmock 注入 mock DB，验证 GORM 生成的 SQL 形态（显式列 / ILIKE 参数
// 透传 / keyset / 事务序列 / 严格状态机 WHERE）与错误原样上抛；业务翻译（哨兵 /
// 23505 / 23503）在 service 层测试覆盖。documents 无软删——SQL 不再有 deleted_at 过滤。

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
	kbCols = []string{"id", "name", "description", "embedding_model_id", "chunk_strategy", "enabled", "created_at", "updated_at"}
	// docCols 列表列（无 content——大文本仅详情路径取；enabled 随行返回——停用行可见）。
	docCols = []string{"id", "knowledge_base_id", "name", "status", "file_type", "file_size", "error_message", "chunk_count", "enabled", "created_at", "updated_at"}
)

// kbVals 展开为 knowledge_bases 一行（列序 = kbCols；chunk_strategy 填 NULL——
// 空策略降级读全局默认，serializer:json 对 NULL 跳过）。
func kbVals(id, modelID uint64, now time.Time) []driver.Value {
	return []driver.Value{id, "产品手册", "内部文档", modelID, nil, true, now, now}
}

// docVals 展开为 documents 一行（列序 = docCols；chunkCount 变体由参数带入）。
// enabled 固定 true（store 层无缺省逻辑，值原样扫描；停用行场景由 service 层测）。
func docVals(id, kbID uint64, status string, chunkCount int, now time.Time) []driver.Value {
	return []driver.Value{id, kbID, "manual.txt", status, "txt", int64(24), "", chunkCount, true, now, now}
}

// 期望 SQL（与 GORM 实际生成逐字比对；QuoteMeta 防正则元字符误匹配）。
const (
	getKBByIDSQL = `SELECT id, name, description, embedding_model_id, chunk_strategy, enabled, created_at, updated_at FROM "knowledge_bases" WHERE "knowledge_bases"."id" = $1 ORDER BY "knowledge_bases"."id" LIMIT $2`

	listKBsCountSQL = `SELECT count(*) FROM "knowledge_bases"`
	listKBsPageSQL  = `SELECT id, name, description, embedding_model_id, chunk_strategy, enabled, created_at, updated_at FROM "knowledge_bases" ORDER BY id LIMIT $1`

	// name 过滤：通配符拼接在 SQL 侧，参数 = 原始关键字（用户输入 %/_ 不转义，spec 04 §1）。
	listKBsCountNameSQL = `SELECT count(*) FROM "knowledge_bases" WHERE name ILIKE '%' || $1 || '%'`
	listKBsPageNameSQL  = `SELECT id, name, description, embedding_model_id, chunk_strategy, enabled, created_at, updated_at FROM "knowledge_bases" WHERE name ILIKE '%' || $1 || '%' ORDER BY id LIMIT $2`

	deleteKBSQL = `DELETE FROM "knowledge_bases" WHERE "knowledge_bases"."id" = $1`

	deleteChunksByKBSQL  = `DELETE FROM "document_chunks" WHERE knowledge_base_id = $1`
	deleteDocsByKBSQL    = `DELETE FROM "documents" WHERE knowledge_base_id = $1`
	hardDeleteDocSQL     = `DELETE FROM "documents" WHERE "documents"."id" = $1`
	countDocsByKBIDsSQL  = `SELECT knowledge_base_id, COUNT(*) AS cnt FROM "documents" WHERE knowledge_base_id IN ($1,$2) GROUP BY "knowledge_base_id"`
	getDocByIDSQL        = `SELECT id, knowledge_base_id, name, content, status, file_type, file_size, error_message, chunk_count, enabled, created_at, updated_at FROM "documents" WHERE "documents"."id" = $1 ORDER BY "documents"."id" LIMIT $2`
	listDocsFirstSQL     = `SELECT id, knowledge_base_id, name, status, file_type, file_size, error_message, chunk_count, enabled, created_at, updated_at FROM "documents" WHERE knowledge_base_id = $1 ORDER BY id DESC LIMIT $2`
	listDocsBeforeSQL    = `SELECT id, knowledge_base_id, name, status, file_type, file_size, error_message, chunk_count, enabled, created_at, updated_at FROM "documents" WHERE knowledge_base_id = $1 AND id < $2 ORDER BY id DESC LIMIT $3`
	deleteChunksSQL      = `DELETE FROM "document_chunks" WHERE document_id = $1`
)

// 严格状态机正则（disable / enable）：map Updates 的 SET 列序随迭代不定——通配 SET、
// 锁死 WHERE 形态（enabled 前置条件 + status 终态 IN；占位符用 \$[0-9]+ 防编号耦合，
// 尾锚防 IN 被 = 前缀误匹配）。
var (
	disableDocRe = `UPDATE "documents" SET .* WHERE id = \$[0-9]+ AND enabled = \$[0-9]+ AND status IN \(\$[0-9]+,\$[0-9]+\)$`
	enableDocRe  = `UPDATE "documents" SET .* WHERE id = \$[0-9]+ AND enabled = \$[0-9]+ AND status IN \(\$[0-9]+,\$[0-9]+\)$`
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

// chunk_strategy jsonb 序列化往返：2030b8d 遗漏 serializer:json 时该路径绑参失败
//（纯结构体无 Valuer/Scanner）——钉死 Scan 侧解析出完整策略。
func TestGetKnowledgeBaseByIDChunkStrategy(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	strategyJSON := `{"type":"fixed_length","chunk_size":300,"chunk_overlap":50,"separator":"\n"}`
	mock.ExpectQuery(regexp.QuoteMeta(getKBByIDSQL)).
		WithArgs(uint64(1), 1).
		WillReturnRows(sqlmock.NewRows(kbCols).
			AddRow(1, "产品手册", "内部文档", 5, []byte(strategyJSON), true, now, now))

	kb, err := s.GetKnowledgeBaseByID(context.Background(), 1)
	assert.NoError(t, err)
	assert.Equal(t, ragsvc.ChunkStrategy{Type: "fixed_length", ChunkSize: 300, ChunkOverlap: 50, Separator: "\n"}, kb.ChunkStrategy)
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

// KB 级联删除前两步：按冗余 knowledge_base_id 单表清理（0 行合法，不判 RowsAffected）。
func TestDeleteChunksByKB(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(deleteChunksByKBSQL)).
		WithArgs(uint64(1)).
		WillReturnResult(sqlmock.NewResult(0, 7))
	mock.ExpectCommit()

	err := s.DeleteChunksByKB(context.Background(), 1)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteDocumentsByKB(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(deleteDocsByKBSQL)).
		WithArgs(uint64(1)).
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()

	err := s.DeleteDocumentsByKB(context.Background(), 1)
	assert.NoError(t, err)
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
	rows := sqlmock.NewRows([]string{"id", "knowledge_base_id", "name", "content", "status", "file_type", "file_size", "error_message", "chunk_count", "enabled", "created_at", "updated_at"}).
		AddRow(9, 1, "manual.txt", "正文内容", ragsvc.StatusReady, "txt", int64(24), "", 8, false, now, now)
	mock.ExpectQuery(regexp.QuoteMeta(getDocByIDSQL)).
		WithArgs(uint64(9), 1).
		WillReturnRows(rows)

	d, err := s.GetDocumentByID(context.Background(), 9)
	assert.NoError(t, err)
	assert.Equal(t, "正文内容", d.Content, "详情路径取 content 大文本")
	assert.Equal(t, ragsvc.StatusReady, d.Status)
	assert.Equal(t, 8, d.ChunkCount)
	assert.False(t, d.Enabled, "停用行同样可见（深度停用不是删除）")
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

// 真删事务序列（不变量规则 2）：硬删 documents 行 → 同事务硬删 chunks → 提交。
func TestHardDeleteDocument(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(hardDeleteDocSQL)).
		WithArgs(uint64(9)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(deleteChunksSQL)).
		WithArgs(uint64(9)).
		WillReturnResult(sqlmock.NewResult(0, 5))
	mock.ExpectCommit()

	err := s.HardDeleteDocument(context.Background(), 9)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// 并发已删：DELETE 0 行 → 整笔回滚 + NotFound（chunks 不被误删）。
func TestHardDeleteDocumentNotFound(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(hardDeleteDocSQL)).
		WithArgs(uint64(999)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	err := s.HardDeleteDocument(context.Background(), 999)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// 深度停用事务序列（不变量规则 2）：严格状态机 UPDATE（enabled 且终态）→ 同事务
// 硬删 chunks → 提交（内容保留）。
func TestDisableDocument(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(disableDocRe).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(deleteChunksSQL)).
		WithArgs(uint64(9)).
		WillReturnResult(sqlmock.NewResult(0, 5))
	mock.ExpectCommit()

	err := s.DisableDocument(context.Background(), 9)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// 状态机 0 行（入库中撞并发 / 不存在）：整笔回滚 + NotFound，chunks 不被误删；
// 由 service 重读区分 404 / 幂等 / 409。
func TestDisableDocumentGuardMiss(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(disableDocRe).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	err := s.DisableDocument(context.Background(), 9)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// 重新启用：单条严格状态机 UPDATE（停用且终态 → enabled=true + 重置 pending）；
// 无须删 chunks（停用行本就无 chunks），不包事务。
func TestEnableDocument(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(enableDocRe).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := s.EnableDocument(context.Background(), 9)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// 状态机 0 行（已启用幂等 / 入库中 / 不存在）→ NotFound，由 service 重读区分。
func TestEnableDocumentGuardMiss(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(enableDocRe).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	err := s.EnableDocument(context.Background(), 9)
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

// 重置：status/error_message/chunk_count + updated_at（autoUpdateTime）。
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
// 已删行自然成"悬空"（无键 = ""）。
const getDocMetasSQL = `SELECT id, name FROM "documents" WHERE id IN ($1,$2)`

// TestGetDocumentMetasByIDs IN 展开两参；悬空（已删 / 不存在）无键。
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

// ---- spec 07：管线方法族（状态翻转 / CreateChunks / ListIngestingDocuments） ----

// Mark* 严格状态机（spec 07 §3，实施拍板）：Processing WHERE status='pending'、
// Ready WHERE status='processing'、Failed WHERE status IN ('pending','processing')。
// map Updates 的 SET 列序随迭代不定——正则通配 SET、锁死 WHERE 形态（占位符用
// \$[0-9]+ 防编号耦合）；尾锚防 IN 被 = 前缀误匹配。
var (
	markProcessingRe = `UPDATE "documents" SET .* WHERE id = \$[0-9]+ AND status = \$[0-9]+$`
	markReadyRe      = `UPDATE "documents" SET .* WHERE id = \$[0-9]+ AND status = \$[0-9]+$`
	markFailedRe     = `UPDATE "documents" SET .* WHERE id = \$[0-9]+ AND status IN \(\$[0-9]+,\$[0-9]+\)$`
)

// listIngestingSQL Recovery 扫描（spec 07 §4）：显式列（无 content 大文本）、
// status IN 命中 partial idx idx_documents_ingesting。
const listIngestingSQL = `SELECT id, knowledge_base_id, name, status, file_type, file_size, error_message, chunk_count, enabled, created_at, updated_at FROM "documents" WHERE status IN ($1,$2)`

func TestMarkDocumentProcessing(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(markProcessingRe).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := s.MarkDocumentProcessing(context.Background(), 9)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// 非 pending（0 行）→ ErrRecordNotFound：管线中止（文档已不在待处理态）。
func TestMarkDocumentProcessingNotPending(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(markProcessingRe).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	err := s.MarkDocumentProcessing(context.Background(), 9)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMarkDocumentReady(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(markReadyRe).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := s.MarkDocumentReady(context.Background(), 9, 42)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// 非 processing（0 行）→ ErrRecordNotFound：终态事务回滚，chunks 不落孤儿。
func TestMarkDocumentReadyNotProcessing(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(markReadyRe).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	err := s.MarkDocumentReady(context.Background(), 9, 42)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMarkDocumentFailed(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(markFailedRe).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := s.MarkDocumentFailed(context.Background(), 9, "服务重启中断，请重新索引")
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// 已终态 / 已删（0 行）→ nil 静默：markFailed 是尽力而为的最后一步，不构成错误路径。
func TestMarkDocumentFailedTerminalState(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(markFailedRe).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	err := s.MarkDocumentFailed(context.Background(), 9, "x")
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestCreateChunks 切片插入 = 单语句多 VALUES（仓规批量写），RETURNING 回填
// id / created_at；断言含两组 VALUES（正则 `\),\(`）。
func TestCreateChunks(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "document_chunks".*VALUES.*\),\(.*`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(100, now).AddRow(101, now))
	mock.ExpectCommit()

	cs := []ragsvc.DocumentChunk{
		{DocumentID: 9, KnowledgeBaseID: 1, ChunkIndex: 1, Content: "第一块", TokenCount: 3, Embedding: pgvector.NewVector([]float32{0.1, 0.2})},
		{DocumentID: 9, KnowledgeBaseID: 1, ChunkIndex: 2, Content: "第二块", TokenCount: 3, Embedding: pgvector.NewVector([]float32{0.3, 0.4})},
	}
	err := s.CreateChunks(context.Background(), cs)
	assert.NoError(t, err)
	assert.Equal(t, uint64(100), cs[0].ID, "RETURNING 回填 id")
	assert.Equal(t, uint64(101), cs[1].ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// 空切片直返防御：零 SQL（终态事务逐批调用，空批不发 INSERT）。
func TestCreateChunksEmpty(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)

	err := s.CreateChunks(context.Background(), nil)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet()) // 无期望 = 无 SQL 发出
}

func TestListIngestingDocuments(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(listIngestingSQL)).
		WithArgs(ragsvc.StatusPending, ragsvc.StatusProcessing).
		WillReturnRows(sqlmock.NewRows(docCols).
			AddRow(docVals(9, 1, ragsvc.StatusPending, 0, now)...).
			AddRow(docVals(10, 1, ragsvc.StatusProcessing, 0, now)...))

	docs, err := s.ListIngestingDocuments(context.Background())
	require.NoError(t, err)
	assert.Len(t, docs, 2)
	assert.Equal(t, ragsvc.StatusPending, docs[0].Status)
	assert.Equal(t, ragsvc.StatusProcessing, docs[1].Status)
	assert.NoError(t, mock.ExpectationsWereMet())
}
