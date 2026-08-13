package db

import (
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// 行为验证：mixin 的 GORM 行为（自动填充 / 软删）钉进测试，新 model embed 时有保障。

// testMutable 可变表 model（embed BaseMutable）。
type testMutable struct {
	BaseMutable
	Name string
}

func (testMutable) TableName() string { return "test_mutables" }

// testSoft 软删除表 model（embed BaseSoftDelete）。
type testSoft struct {
	BaseSoftDelete
	Name string
}

func (testSoft) TableName() string { return "test_softs" }

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

// TestCreateFillsTimestamps INSERT 时 CreatedAt / UpdatedAt 自动填充。
// GORM 对带 default:now() 的字段跳过 INSERT 列（交给 DB 默认值）、靠 RETURNING 回填
// —— rows 需含时间列，模拟 PG 真实回填行为。
func TestCreateFillsTimestamps(t *testing.T) {
	db, mock := newMockDB(t)
	now := time.Now()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "test_mutables"`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
			AddRow(1, now, now))
	mock.ExpectCommit()

	m := &testMutable{Name: "x"}
	if !m.CreatedAt.IsZero() || !m.UpdatedAt.IsZero() {
		t.Fatal("timestamps should be zero before create")
	}
	if err := db.Create(m).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	if m.CreatedAt.IsZero() {
		t.Fatal("CreatedAt should be auto-filled on insert")
	}
	if m.UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt should be auto-filled on insert")
	}
	if d := time.Since(m.CreatedAt); d < 0 || d > time.Minute {
		t.Fatalf("CreatedAt = %v, want near now", m.CreatedAt)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestUpdateMaintainsUpdatedAt UPDATE 时 UpdatedAt 自动维护为新时间。
func TestUpdateMaintainsUpdatedAt(t *testing.T) {
	db, mock := newMockDB(t)
	now := time.Now()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "test_mutables"`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
			AddRow(1, now, now))
	mock.ExpectCommit()

	m := &testMutable{Name: "x"}
	if err := db.Create(m).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	before := m.UpdatedAt
	time.Sleep(2 * time.Millisecond) // 保证新值可区分

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE "test_mutables" SET`) + `.*`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := db.Model(m).Update("name", "y").Error; err != nil {
		t.Fatalf("update: %v", err)
	}
	if !m.UpdatedAt.After(before) {
		t.Fatalf("UpdatedAt = %v, want after %v", m.UpdatedAt, before)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestSoftDeleteRewriteDeleteAsUpdate 软删模型的 DELETE 改写为 UPDATE deleted_at。
func TestSoftDeleteRewriteDeleteAsUpdate(t *testing.T) {
	db, mock := newMockDB(t)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE "test_softs" SET "deleted_at"=$1 WHERE "test_softs"."id" = $2`)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	s := &testSoft{Name: "x"}
	s.ID = 1
	if err := db.Delete(s).Error; err != nil {
		t.Fatalf("delete: %v", err)
	}
	if s.DeletedAt.Valid == false {
		t.Fatal("DeletedAt should be set by soft delete")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestSoftDeleteQueryFiltersDeleted 软删模型的查询自动带 WHERE deleted_at IS NULL。
func TestSoftDeleteQueryFiltersDeleted(t *testing.T) {
	db, mock := newMockDB(t)
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(`"test_softs"."deleted_at" IS NULL`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at", "deleted_at", "name"}).
			AddRow(1, now, now, nil, "x"))

	var out testSoft
	if err := db.First(&out, 1).Error; err != nil {
		t.Fatalf("first: %v", err)
	}
	if out.ID != 1 {
		t.Fatalf("id = %d", out.ID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestAppendOnlyHasNoUpdatedAt append-only 表头不含 updated_at（编译期断言：字段集固定）。
func TestAppendOnlyHasNoUpdatedAt(t *testing.T) {
	type appendOnly struct {
		BaseAppendOnly
	}
	m := &appendOnly{}
	m.CreatedAt = time.Now()
	if m.CreatedAt.IsZero() {
		t.Fatal("CreatedAt should be settable")
	}
	// 反射断言：BaseAppendOnly 不含 UpdatedAt / DeletedAt 字段
	if _, ok := fieldByName(m, "UpdatedAt"); ok {
		t.Fatal("BaseAppendOnly must not have UpdatedAt")
	}
	if _, ok := fieldByName(m, "DeletedAt"); ok {
		t.Fatal("BaseAppendOnly must not have DeletedAt")
	}
}

// fieldByName 反射查找直接字段（不查嵌入结构体内部，验证表头字段集）。
func fieldByName(v any, name string) (any, bool) {
	t := reflect.TypeOf(v)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	for i := 0; i < t.NumField(); i++ {
		if t.Field(i).Name == name {
			return t.Field(i), true
		}
	}
	return nil, false
}
