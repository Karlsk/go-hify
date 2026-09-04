package logging

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
)

// ExecutionStore 测试：sqlmock 注入 mock DB，验证插入路径、jsonb nil 归一与错误上抛
// （对齐 provider 模块 store_test 的模式）。
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

func TestExecutionStoreCreate(t *testing.T) {
	db, mock := newMockDB(t)
	s := NewExecutionStore(db)
	now := time.Now()
	convID, modelID := uint64(7), uint64(3)
	errCls := "Timeout"

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "executions"`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(uint64(42), now))
	mock.ExpectCommit()

	e := &Execution{
		ConversationID: &convID,
		ModelID:        &modelID,
		ModelName:      "gpt-4o",
		PromptTokens:   120,
		DurationMs:     3400,
		FinishReason:   "stop",
		ErrorClass:     &errCls,
	}
	err := s.Create(context.Background(), e)
	assert.NoError(t, err)
	assert.Equal(t, uint64(42), e.ID) // RETURNING 回填主键
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestExecutionStoreCreateNormalizesNilJSONB(t *testing.T) {
	db, mock := newMockDB(t)
	s := NewExecutionStore(db)
	now := time.Now()

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "executions"`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(uint64(1), now))
	mock.ExpectCommit()

	e := &Execution{ModelName: "m"} // 三个 jsonb 字段全 nil
	err := s.Create(context.Background(), e)
	assert.NoError(t, err)
	// 归一为空集合而非 json null（serializer 会把 nil 显式写成 null 绕过列 DEFAULT）
	assert.Equal(t, map[string]any{}, e.Input)
	assert.Equal(t, map[string]any{}, e.Output)
	assert.Equal(t, []map[string]any{}, e.ToolChain)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestExecutionStoreCreateError(t *testing.T) {
	db, mock := newMockDB(t)
	s := NewExecutionStore(db)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "executions"`)).
		WillReturnError(errors.New("insert failed"))
	mock.ExpectRollback()

	err := s.Create(context.Background(), &Execution{ModelName: "m"})
	assert.Error(t, err) // 原样上抛，不翻译
	assert.NoError(t, mock.ExpectationsWereMet())
}
