package store

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	chatapi "github.com/Karlsk/go-hify/internal/chat/api"
	chatsvc "github.com/Karlsk/go-hify/internal/chat/service"
)

// store 测试：sqlmock 注入 mock DB，验证 GORM 生成的 SQL 形态（显式列 / keyset 行值比较 / 排序）。
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
	t.Cleanup(func() {
		mockDB.Close()
		_ = mock.ExpectationsWereMet()
	})
	return db, mock
}

// 列清单（与 store.go 的 select 常量一致，供 NewRows 用）。
var (
	conversationCols = []string{"id", "user_id", "agent_id", "title", "created_at", "updated_at"}
	messageCols      = []string{"id", "conversation_id", "role", "content", "tool_calls", "citations", "created_at"}
)

func conversationRows(id, userID, agentID uint64, title string, now time.Time) *sqlmock.Rows {
	return sqlmock.NewRows(conversationCols).AddRow(id, userID, agentID, title, now, now)
}

func messageRows(id, conversationID uint64, role, content, toolCallsJSON, citationsJSON string, now time.Time) *sqlmock.Rows {
	return sqlmock.NewRows(messageCols).AddRow(id, conversationID, role, content, toolCallsJSON, citationsJSON, now)
}

const getConversationSQL = `SELECT id, user_id, agent_id, title, created_at, updated_at FROM "conversations" WHERE "conversations"."id" = $1 ORDER BY "conversations"."id" LIMIT $2`

const listConversationsFirstSQL = `SELECT id, user_id, agent_id, title, created_at, updated_at FROM "conversations" WHERE user_id = $1 ORDER BY updated_at DESC, id DESC LIMIT $2`

const listConversationsCursorSQL = `SELECT id, user_id, agent_id, title, created_at, updated_at FROM "conversations" WHERE user_id = $1 AND (updated_at, id) < ($2, $3) ORDER BY updated_at DESC, id DESC LIMIT $4`

const listMessagesAfterSQL = `SELECT id, conversation_id, role, content, tool_calls, citations, created_at FROM "messages" WHERE conversation_id = $1 AND id > $2 ORDER BY id LIMIT $3`

const listRecentMessagesSQL = `SELECT id, conversation_id, role, content, tool_calls, citations, created_at FROM "messages" WHERE conversation_id = $1 ORDER BY id DESC LIMIT $2`

// ---- conversations ----

func TestCreateConversation(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "conversations"`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow(uint64(1), now, now))
	mock.ExpectCommit()

	c := &chatsvc.Conversation{UserID: 5, AgentID: 2, Title: ""}
	err := s.CreateConversation(context.Background(), c)
	assert.NoError(t, err)
	assert.Equal(t, uint64(1), c.ID) // RETURNING 回填主键
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetConversationByID(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()

	mock.ExpectQuery(regexp.QuoteMeta(getConversationSQL)).
		WithArgs(uint64(9), 1).
		WillReturnRows(conversationRows(9, 5, 2, "查订单", now))

	c, err := s.GetConversationByID(context.Background(), 9)
	assert.NoError(t, err)
	assert.Equal(t, "查订单", c.Title)
	assert.Equal(t, uint64(2), c.AgentID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetConversationByIDNotFound(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)

	mock.ExpectQuery(regexp.QuoteMeta(getConversationSQL)).
		WithArgs(uint64(404), 1).
		WillReturnError(gorm.ErrRecordNotFound)

	_, err := s.GetConversationByID(context.Background(), 404)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound) // 原样上抛，翻译在 service 层
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListConversationsByCursorFirstPage(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	earlier := now.Add(-time.Minute)

	mock.ExpectQuery(regexp.QuoteMeta(listConversationsFirstSQL)).
		WithArgs(uint64(5), 21). // FetchN = limit+1 判 has_more
		WillReturnRows(conversationRows(9, 5, 2, "a", now).
			AddRow(8, 5, 2, "b", earlier, earlier))

	cs, err := s.ListConversationsByCursor(context.Background(), 5, time.Time{}, 0, 21)
	assert.NoError(t, err)
	assert.Len(t, cs, 2)
	assert.Equal(t, uint64(9), cs[0].ID) // updated_at DESC, id DESC
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListConversationsByCursorWithCursor(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	before := time.Now()

	mock.ExpectQuery(regexp.QuoteMeta(listConversationsCursorSQL)).
		WithArgs(uint64(5), before, uint64(8), 21).
		WillReturnRows(conversationRows(7, 5, 2, "c", before.Add(-time.Minute)))

	cs, err := s.ListConversationsByCursor(context.Background(), 5, before, 8, 21)
	assert.NoError(t, err)
	assert.Len(t, cs, 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateConversationTitle(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE "conversations" SET "title"=$1 WHERE id = $2`)).
		WithArgs("查一下", uint64(9)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := s.UpdateConversationTitle(context.Background(), 9, "查一下")
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateConversationTitleNotFound(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE "conversations" SET "title"=$1 WHERE id = $2`)).
		WithArgs("x", uint64(404)).
		WillReturnResult(sqlmock.NewResult(0, 0)) // 0 行受影响
	mock.ExpectCommit()

	err := s.UpdateConversationTitle(context.Background(), 404, "x")
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestTouchConversation(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE "conversations" SET "updated_at"=$1 WHERE id = $2`)).
		WithArgs(sqlmock.AnyArg(), uint64(9)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := s.TouchConversation(context.Background(), 9)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteConversation(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM "conversations" WHERE "conversations"."id" = $1`)).
		WithArgs(uint64(9)).
		WillReturnResult(sqlmock.NewResult(0, 1)) // messages 级联删由 FK 保证，无需额外语句
	mock.ExpectCommit()

	err := s.DeleteConversation(context.Background(), 9)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---- messages ----

func TestCreateMessage(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "messages"`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(uint64(101), now))
	mock.ExpectCommit()

	m := &chatsvc.Message{ConversationID: 9, Role: "user", Content: "查下异常订单"}
	err := s.CreateMessage(context.Background(), m)
	assert.NoError(t, err)
	assert.Equal(t, uint64(101), m.ID)
	assert.Equal(t, []map[string]any{}, m.ToolCalls)   // nil 归一为空集合，防 jsonb null
	assert.Equal(t, []chatapi.Citation{}, m.Citations) // 同款归一（user 行无引用）
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListMessagesAfter(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()

	mock.ExpectQuery(regexp.QuoteMeta(listMessagesAfterSQL)).
		WithArgs(uint64(9), uint64(100), 50).
		WillReturnRows(messageRows(101, 9, "user", "问", "[]", "[]", now).
			AddRow(102, 9, "assistant", "答", "[]",
				`[{"document_id":"7","document_name":"deploy.md","similarity":0.87}]`, now))

	ms, err := s.ListMessagesAfter(context.Background(), 9, 100, 50)
	assert.NoError(t, err)
	assert.Len(t, ms, 2)
	assert.Equal(t, uint64(101), ms[0].ID) // 正序
	assert.Equal(t, "deploy.md", ms[1].Citations[0].DocumentName, "citations 须在列清单内（漏列 → 恒空，历史消息看不到引用来源）")
	assert.Equal(t, 0.87, ms[1].Citations[0].Similarity)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListRecentMessages(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()

	mock.ExpectQuery(regexp.QuoteMeta(listRecentMessagesSQL)).
		WithArgs(uint64(9), 40).
		WillReturnRows(messageRows(104, 9, "assistant", "答", "[]", "[]", now).
			AddRow(103, 9, "tool", "{...}", "[]", "[]", now.Add(-time.Second)).
			AddRow(102, 9, "assistant", "", `[{"id":"call_1","tool":"query_orders","args":{}}]`, "[]", now.Add(-2*time.Second)).
			AddRow(101, 9, "user", "问", "[]", "[]", now.Add(-3*time.Second)))

	ms, err := s.ListRecentMessages(context.Background(), 9, 40)
	assert.NoError(t, err)
	assert.Len(t, ms, 4)
	assert.Equal(t, uint64(104), ms[0].ID) // 倒序：最新在前
	assert.Equal(t, "query_orders", ms[2].ToolCalls[0]["tool"])
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListRecentMessagesError(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)

	mock.ExpectQuery(regexp.QuoteMeta(listRecentMessagesSQL)).
		WithArgs(uint64(9), 40).
		WillReturnError(errors.New("db down"))

	_, err := s.ListRecentMessages(context.Background(), 9, 40)
	assert.Error(t, err) // 原样上抛
	assert.NoError(t, mock.ExpectationsWereMet())
}
