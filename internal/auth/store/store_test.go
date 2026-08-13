package store

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	authsvc "github.com/Karlsk/go-hify/internal/auth/service"
)

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

func TestCreate(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "users"`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
			AddRow(1, now, now))
	mock.ExpectCommit()

	err := s.Create(context.Background(), &authsvc.User{Username: "alice", PasswordHash: "hash"})
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetByUsername(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "users" WHERE username = $1 ORDER BY "users"."id" LIMIT $2`)).
		WithArgs("alice", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "username", "password_hash", "created_at", "updated_at"}).
			AddRow(1, "alice", "hash", now, now))

	u, err := s.GetByUsername(context.Background(), "alice")
	assert.NoError(t, err)
	assert.Equal(t, "alice", u.Username)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetByUsernameNotFound(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "users" WHERE username = $1 ORDER BY "users"."id" LIMIT $2`)).
		WithArgs("ghost", 1).
		WillReturnError(gorm.ErrRecordNotFound)

	_, err := s.GetByUsername(context.Background(), "ghost")
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetByID(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "users" WHERE "users"."id" = $1 ORDER BY "users"."id" LIMIT $2`)).
		WithArgs(uint64(1), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "username", "password_hash", "created_at", "updated_at"}).
			AddRow(1, "alice", "hash", now, now))

	u, err := s.GetByID(context.Background(), 1)
	assert.NoError(t, err)
	assert.Equal(t, uint64(1), u.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}
