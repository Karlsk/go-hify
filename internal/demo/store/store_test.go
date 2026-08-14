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

	demosvc "github.com/Karlsk/go-hify/internal/demo/service"
	"github.com/Karlsk/go-hify/internal/platform/page"
)

// store 测试：sqlmock 注入 mock DB，验证 GORM 生成的 SQL 形态与错误原样上抛。

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

// rows 返回 demo_items 的完整行（列序 = selectColumns）。
func rows(id uint64, name, status string, createdAt, updatedAt time.Time) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "name", "status", "created_at", "updated_at"}).
		AddRow(id, name, status, createdAt, updatedAt)
}

func TestCreate(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "demo_items"`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
			AddRow(1, now, now))
	mock.ExpectCommit()

	item := &demosvc.DemoItem{Name: "first", Status: "draft"}
	err := s.Create(context.Background(), item)
	assert.NoError(t, err)
	assert.Equal(t, uint64(1), item.ID) // RETURNING 回填主键
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateError(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "demo_items"`)).
		WillReturnError(errors.New("insert failed"))
	mock.ExpectRollback()

	err := s.Create(context.Background(), &demosvc.DemoItem{Name: "x", Status: "draft"})
	assert.Error(t, err) // error 原样上抛，业务翻译在 service 层
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetByID(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	// 显式列清单（禁 SELECT *）。
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, name, status, created_at, updated_at FROM "demo_items" WHERE "demo_items"."id" = $1 ORDER BY "demo_items"."id" LIMIT $2`)).
		WithArgs(uint64(1), 1).
		WillReturnRows(rows(1, "first", "draft", now, now))

	item, err := s.GetByID(context.Background(), 1)
	assert.NoError(t, err)
	assert.Equal(t, "first", item.Name)
	assert.Equal(t, "draft", item.Status)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetByIDNotFound(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, name, status, created_at, updated_at FROM "demo_items" WHERE "demo_items"."id" = $1`)).
		WithArgs(uint64(999), 1).
		WillReturnError(gorm.ErrRecordNotFound)

	_, err := s.GetByID(context.Background(), 999)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound) // 未找到原样返回 gorm 哨兵
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestList(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	// 偏移分页两段 SQL：先精确 Count，再 LIMIT（第 1 页 offset=0 不生成 OFFSET 子句）。
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT count(*) FROM "demo_items"`)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, name, status, created_at, updated_at FROM "demo_items" ORDER BY id DESC LIMIT $1`)).
		WithArgs(2).
		WillReturnRows(rows(3, "c", "archived", now, now).AddRow(2, "b", "active", now, now))

	res, err := s.List(context.Background(), page.NewOffset(1, 2))
	assert.NoError(t, err)
	assert.Equal(t, int64(3), res.Total)
	assert.Equal(t, 1, res.Page)
	assert.Equal(t, 2, res.PageSize)
	assert.Len(t, res.Items, 2)
	assert.Equal(t, uint64(3), res.Items[0].ID) // ORDER BY id DESC
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListPageTwoHasOffset(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT count(*) FROM "demo_items"`)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, name, status, created_at, updated_at FROM "demo_items" ORDER BY id DESC LIMIT $1 OFFSET $2`)).
		WithArgs(2, 2).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "status", "created_at", "updated_at"}))

	res, err := s.List(context.Background(), page.NewOffset(2, 2))
	assert.NoError(t, err)
	assert.Equal(t, int64(3), res.Total)
	assert.Len(t, res.Items, 0)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListCountError(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT count(*) FROM "demo_items"`)).
		WillReturnError(errors.New("count failed"))

	_, err := s.List(context.Background(), page.NewOffset(1, 20))
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdate(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	// map 更新显式列（name/status），updated_at 由 GORM autoUpdateTime 注入；参数序不定，故不校参。
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE "demo_items" SET`)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	before := time.Now().Add(-time.Hour)
	item := &demosvc.DemoItem{Name: "new", Status: "active"}
	item.ID = 1
	item.UpdatedAt = before
	err := s.Update(context.Background(), item)
	assert.NoError(t, err)
	// GORM autoUpdateTime 回写 model（callbacks/update.go 的 assignValue），响应可带新 updated_at。
	assert.True(t, item.UpdatedAt.After(before), "UpdatedAt must be refreshed by GORM")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateError(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE "demo_items" SET`)).
		WillReturnError(errors.New("update failed"))
	mock.ExpectRollback()

	item := &demosvc.DemoItem{Name: "n", Status: "draft"}
	item.ID = 1
	err := s.Update(context.Background(), item)
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDelete(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM "demo_items" WHERE "demo_items"."id" = $1`)).
		WithArgs(1).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := s.Delete(context.Background(), 1)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteNotFound(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM "demo_items" WHERE "demo_items"."id" = $1`)).
		WithArgs(999).
		WillReturnResult(sqlmock.NewResult(0, 0)) // RowsAffected=0
	mock.ExpectCommit()

	err := s.Delete(context.Background(), 999)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound) // service 层翻译为业务哨兵
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteError(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM "demo_items"`)).
		WillReturnError(errors.New("delete failed"))
	mock.ExpectRollback()

	err := s.Delete(context.Background(), 1)
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
