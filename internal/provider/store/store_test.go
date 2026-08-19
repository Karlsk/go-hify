package store

import (
	"context"
	"database/sql/driver"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/Karlsk/go-hify/internal/platform/page"
	providersvc "github.com/Karlsk/go-hify/internal/provider/service"
)

// store 测试：sqlmock 注入 mock DB，验证 GORM 生成的 SQL 形态（显式列 / WHERE / ORDER BY）
// 与错误原样上抛；业务翻译（哨兵 / 23505 / 23503）在 service 层测试覆盖。

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

// 列清单（与 store.go 的 select 常量一致，供 NewRows 用）。
var (
	providerCols = []string{"id", "name", "kind", "base_url", "auth_config", "api_key_rotated_at", "extra_config", "enabled", "created_at", "updated_at"}
	modelCols    = []string{"id", "provider_id", "name", "model_id", "capability", "context_window", "max_output_tokens", "input_price", "output_price", "embedding_dim", "enabled", "source", "extra_params", "created_at", "updated_at"}
	healthCols   = []string{"provider_id", "status", "last_check_at", "last_success_at", "fail_count", "latency_ms", "error_message", "created_at", "updated_at"}
)

// pvals 展开为 providers 一行（列序 = providerCols）；基础列填零值，变体由参数带入。
func pvals(id uint64, name, kind string, enabled bool, authJSON string, now time.Time) []driver.Value {
	return []driver.Value{id, name, kind, "", authJSON, nil, "{}", enabled, now, now}
}

// mvals 展开为 models 一行（列序 = modelCols）。
func mvals(id, providerID uint64, name, modelID string, now time.Time) []driver.Value {
	return []driver.Value{id, providerID, name, modelID, "chat", nil, nil, nil, nil, nil, true, "manual", "{}", now, now}
}

// providerRows 单行 providers 结果集。
func providerRows(vals []driver.Value) *sqlmock.Rows {
	return sqlmock.NewRows(providerCols).AddRow(vals...)
}

// modelRows 单行 models 结果集。
func modelRows(vals []driver.Value) *sqlmock.Rows {
	return sqlmock.NewRows(modelCols).AddRow(vals...)
}

// healthRows 返回 provider_health 行（列序 = healthCols）。
func healthRows(providerID uint64, status string, now time.Time) *sqlmock.Rows {
	return sqlmock.NewRows(healthCols).
		AddRow(providerID, status, now, now, int32(0), int32(120), "", now, now)
}

const getProviderByIDSQL = `SELECT id, name, kind, base_url, auth_config, api_key_rotated_at, extra_config, enabled, created_at, updated_at FROM "providers" WHERE "providers"."id" = $1 ORDER BY "providers"."id" LIMIT $2`

const getModelByIDSQL = `SELECT id, provider_id, name, model_id, capability, context_window, max_output_tokens, input_price, output_price, embedding_dim, enabled, source, extra_params, created_at, updated_at FROM "models" WHERE "models"."id" = $1 ORDER BY "models"."id" LIMIT $2`

const getModelByUKSQL = `SELECT id, provider_id, name, model_id, capability, context_window, max_output_tokens, input_price, output_price, embedding_dim, enabled, source, extra_params, created_at, updated_at FROM "models" WHERE provider_id = $1 AND model_id = $2 ORDER BY "models"."id" LIMIT $3`

const getModelPageSQL = `SELECT id, provider_id, name, model_id, capability, context_window, max_output_tokens, input_price, output_price, embedding_dim, enabled, source, extra_params, created_at, updated_at FROM "models" WHERE provider_id = $1 ORDER BY id LIMIT $2`

const getHealthSQL = `SELECT provider_id, status, last_check_at, last_success_at, fail_count, latency_ms, error_message, created_at, updated_at FROM "provider_health" WHERE provider_id = $1 ORDER BY "provider_health"."provider_id" LIMIT $2`

// ---- provider ----

func TestCreateProvider(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "providers"`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow(1, now, now))
	mock.ExpectCommit()

	p := &providersvc.Provider{Name: "OpenAI", Kind: "openai", Enabled: true,
		AuthConfig: map[string]string{"api_key_encrypted": "enc"}}
	err := s.CreateProvider(context.Background(), p)
	assert.NoError(t, err)
	assert.Equal(t, uint64(1), p.ID) // RETURNING 回填主键
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateProviderError(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "providers"`)).
		WillReturnError(errors.New("unique violation"))
	mock.ExpectRollback()

	err := s.CreateProvider(context.Background(), &providersvc.Provider{Name: "x", Kind: "openai"})
	assert.Error(t, err) // 原样上抛，翻译在 service 层
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetProviderByID(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(getProviderByIDSQL)).
		WithArgs(uint64(1), 1).
		WillReturnRows(providerRows(pvals(1, "OpenAI", "openai", true, `{"api_key_encrypted":"enc-x"}`, now)))

	p, err := s.GetProviderByID(context.Background(), 1)
	assert.NoError(t, err)
	assert.Equal(t, "OpenAI", p.Name)
	assert.Equal(t, "enc-x", p.AuthConfig["api_key_encrypted"], "jsonb 反序列化进 map")
	assert.NotNil(t, p.ExtraConfig)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetProviderByIDNotFound(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectQuery(regexp.QuoteMeta(getProviderByIDSQL)).
		WithArgs(uint64(999), 1).
		WillReturnError(gorm.ErrRecordNotFound)

	_, err := s.GetProviderByID(context.Background(), 999)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetProviderByName(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, name, kind, base_url, auth_config, api_key_rotated_at, extra_config, enabled, created_at, updated_at FROM "providers" WHERE name = $1 ORDER BY "providers"."id" LIMIT $2`)).
		WithArgs("OpenAI", 1).
		WillReturnRows(providerRows(pvals(1, "OpenAI", "openai", true, "{}", now)))

	p, err := s.GetProviderByName(context.Background(), "OpenAI")
	assert.NoError(t, err)
	assert.Equal(t, uint64(1), p.ID)
	assert.Empty(t, p.AuthConfig, "空 jsonb 归一为空 map")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListProviders(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, name, kind, base_url, auth_config, api_key_rotated_at, extra_config, enabled, created_at, updated_at FROM "providers" ORDER BY id`)).
		WillReturnRows(sqlmock.NewRows(providerCols).
			AddRow(pvals(1, "OpenAI", "openai", true, "{}", now)...).
			AddRow(pvals(2, "Claude", "claude", false, "{}", now)...))

	items, err := s.ListProviders(context.Background())
	assert.NoError(t, err)
	assert.Len(t, items, 2)
	assert.Equal(t, "OpenAI", items[0].Name) // id 升序
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateProvider(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	// Save 全量 UPDATE：struct 路径保证 auth_config / extra_config 走 serializer:json；参数序不定，故不校参。
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE "providers" SET`)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	p := &providersvc.Provider{Name: "OpenAI", Kind: "openai", Enabled: false,
		AuthConfig: map[string]string{"api_key_encrypted": "enc-2"}}
	p.ID = 1
	err := s.UpdateProvider(context.Background(), p)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteProviderNotFound(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM "providers" WHERE "providers"."id" = $1`)).
		WithArgs(999).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	err := s.DeleteProvider(context.Background(), 999)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---- model ----

func TestCreateModel(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "models"`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow(2, now, now))
	mock.ExpectCommit()

	m := &providersvc.Model{ProviderID: 1, Name: "GPT-4o", ModelID: "gpt-4o", Capability: "chat", Enabled: true}
	err := s.CreateModel(context.Background(), m)
	assert.NoError(t, err)
	assert.Equal(t, uint64(2), m.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// 批量插入 = 单条多 VALUES INSERT，RETURNING 按行回填 id / 时间戳。
func TestCreateModels(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "models"`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
			AddRow(2, now, now).AddRow(3, now, now))
	mock.ExpectCommit()

	ms := []*providersvc.Model{
		{ProviderID: 1, Name: "GPT-4o", ModelID: "gpt-4o", Capability: "chat", Enabled: true},
		{ProviderID: 1, Name: "GPT-4o mini", ModelID: "gpt-4o-mini", Capability: "chat", Enabled: true},
	}
	err := s.CreateModels(context.Background(), ms)
	assert.NoError(t, err)
	assert.Equal(t, uint64(2), ms[0].ID)
	assert.Equal(t, uint64(3), ms[1].ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// 空切片直接返回，不发 SQL。
func TestCreateModelsEmpty(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	err := s.CreateModels(context.Background(), nil)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateModelsError(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "models"`)).
		WillReturnError(errors.New("unique violation"))
	mock.ExpectRollback()

	ms := []*providersvc.Model{{ProviderID: 1, Name: "GPT-4o", ModelID: "gpt-4o"}}
	err := s.CreateModels(context.Background(), ms)
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetModelByID(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(getModelByIDSQL)).
		WithArgs(uint64(2), 1).
		WillReturnRows(modelRows(mvals(2, 1, "GPT-4o", "gpt-4o", now)))

	m, err := s.GetModelByID(context.Background(), 2)
	assert.NoError(t, err)
	assert.Equal(t, "gpt-4o", m.ModelID)
	assert.Equal(t, "manual", m.Source)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetModelByIDNotFound(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectQuery(regexp.QuoteMeta(getModelByIDSQL)).
		WithArgs(uint64(999), 1).
		WillReturnError(gorm.ErrRecordNotFound)

	_, err := s.GetModelByID(context.Background(), 999)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetModelByProviderAndModelID(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(getModelByUKSQL)).
		WithArgs(uint64(1), "gpt-4o", 1).
		WillReturnRows(modelRows(mvals(2, 1, "GPT-4o", "gpt-4o", now)))

	m, err := s.GetModelByProviderAndModelID(context.Background(), 1, "gpt-4o")
	assert.NoError(t, err)
	assert.Equal(t, uint64(2), m.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListModelsByProvider(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, provider_id, name, model_id, capability, context_window, max_output_tokens, input_price, output_price, embedding_dim, enabled, source, extra_params, created_at, updated_at FROM "models" WHERE provider_id = $1 ORDER BY id`)).
		WithArgs(uint64(1)).
		WillReturnRows(sqlmock.NewRows(modelCols).
			AddRow(mvals(2, 1, "A", "m-a", now)...).
			AddRow(mvals(3, 1, "B", "m-b", now)...))

	items, err := s.ListModelsByProvider(context.Background(), 1)
	assert.NoError(t, err)
	assert.Len(t, items, 2)
	assert.Equal(t, "m-a", items[0].ModelID) // id 升序
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListModels(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	// 两段 SQL：先精确 Count（带 provider 过滤），再 LIMIT（第 1 页 offset=0 不生成 OFFSET 子句）。
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT count(*) FROM "models" WHERE provider_id = $1`)).
		WithArgs(uint64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))
	mock.ExpectQuery(regexp.QuoteMeta(getModelPageSQL)).
		WithArgs(uint64(1), 2).
		WillReturnRows(sqlmock.NewRows(modelCols).
			AddRow(mvals(2, 1, "A", "m-a", now)...).
			AddRow(mvals(3, 1, "B", "m-b", now)...))

	res, err := s.ListModels(context.Background(), 1, page.NewOffset(1, 2))
	assert.NoError(t, err)
	assert.Equal(t, int64(3), res.Total)
	assert.Len(t, res.Items, 2)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListModelsPageTwoHasOffset(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT count(*) FROM "models" WHERE provider_id = $1`)).
		WithArgs(uint64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))
	mock.ExpectQuery(regexp.QuoteMeta(getModelPageSQL+` OFFSET $3`)).
		WithArgs(uint64(1), 2, 2).
		WillReturnRows(modelRows(mvals(3, 1, "B", "m-b", time.Now())))

	res, err := s.ListModels(context.Background(), 1, page.NewOffset(2, 2))
	assert.NoError(t, err)
	assert.Len(t, res.Items, 1)
	assert.Equal(t, 2, res.Page)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListModelsCountError(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT count(*) FROM "models" WHERE provider_id = $1`)).
		WillReturnError(errors.New("count failed"))

	_, err := s.ListModels(context.Background(), 1, page.NewOffset(1, 20))
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateModel(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE "models" SET`)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	m := &providersvc.Model{ProviderID: 1, Name: "GPT-4o mini", ModelID: "gpt-4o", Capability: "chat", Enabled: true}
	m.ID = 2
	err := s.UpdateModel(context.Background(), m)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// 列级更新：只写 name / updated_at（区别于全列 Save），WHERE 带 source 守卫；
// updated_at 由 Go 侧 time.Now 生成，参数用 AnyArg。
func TestUpdateModelName(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE "models" SET "name"`)).
		WithArgs("GPT-4o 改名", sqlmock.AnyArg(), uint64(2), "discovered").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := s.UpdateModelName(context.Background(), 2, "GPT-4o 改名")
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// 行不存在或非 discovered（manual / 并发删除）→ RowsAffected=0，按"跳过"返回 nil 不报错。
func TestUpdateModelNameNoop(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE "models" SET "name"`)).
		WithArgs("改名", sqlmock.AnyArg(), uint64(9), "discovered").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	err := s.UpdateModelName(context.Background(), 9, "改名")
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteModelNotFound(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM "models" WHERE "models"."id" = $1`)).
		WithArgs(999).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	err := s.DeleteModel(context.Background(), 999)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---- health ----

func TestGetHealthByProviderID(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(getHealthSQL)).
		WithArgs(uint64(1), 1).
		WillReturnRows(healthRows(1, "up", now))

	h, err := s.GetHealthByProviderID(context.Background(), 1)
	assert.NoError(t, err)
	assert.Equal(t, "up", h.Status)
	assert.NotNil(t, h.LatencyMs)
	assert.Equal(t, int32(120), *h.LatencyMs)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetHealthByProviderIDNotFound(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectQuery(regexp.QuoteMeta(getHealthSQL)).
		WithArgs(uint64(999), 1).
		WillReturnError(gorm.ErrRecordNotFound)

	_, err := s.GetHealthByProviderID(context.Background(), 999)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpsertHealth(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	lat := int32(45)
	h := &providersvc.ProviderHealth{
		ProviderID: 7, Status: "up", LastCheckAt: &now, LastSuccessAt: &now,
		LatencyMs: &lat, CreatedAt: now, UpdatedAt: now,
	}
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "provider_health".+ON CONFLICT \("provider_id"\) DO UPDATE`).
		WillReturnRows(sqlmock.NewRows([]string{"provider_id", "created_at", "updated_at"}).AddRow(uint64(7), now, now))
	mock.ExpectCommit()

	err := s.UpsertHealth(context.Background(), h)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpsertHealthError(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "provider_health"`).
		WillReturnError(errors.New("write failed"))
	mock.ExpectRollback()

	err := s.UpsertHealth(context.Background(), &providersvc.ProviderHealth{ProviderID: 1, Status: "up"})
	assert.Error(t, err) // 原样上抛
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetHealthByProviderIDForUpdate(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	// FOR UPDATE 会追加 FOR UPDATE 子句
	mock.ExpectQuery(regexp.QuoteMeta(getHealthSQL)+` FOR UPDATE`).
		WithArgs(uint64(1), 1).
		WillReturnRows(healthRows(1, "degraded", now))

	h, err := s.GetHealthByProviderIDForUpdate(context.Background(), 1)
	assert.NoError(t, err)
	assert.Equal(t, "degraded", h.Status)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// WithTx：事务句柄内锁定读 + upsert，验证 BEGIN/COMMIT 与回调收到的是事务 Store。
func TestWithTx(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(getHealthSQL)+` FOR UPDATE`).
		WithArgs(uint64(2), 1).
		WillReturnRows(healthRows(2, "up", now))
	mock.ExpectQuery(`INSERT INTO "provider_health".+ON CONFLICT \("provider_id"\) DO UPDATE`).
		WillReturnRows(sqlmock.NewRows([]string{"provider_id", "created_at", "updated_at"}).AddRow(uint64(2), now, now))
	mock.ExpectCommit()

	err := s.WithTx(context.Background(), func(tx providersvc.Store) error {
		h, err := tx.GetHealthByProviderIDForUpdate(context.Background(), 2)
		if err != nil {
			return err
		}
		if h.Status != "up" {
			t.Fatalf("unexpected status %s", h.Status)
		}
		lat := int32(40)
		return tx.UpsertHealth(context.Background(), &providersvc.ProviderHealth{
			ProviderID: 2, Status: "up", LastCheckAt: &now, LastSuccessAt: &now,
			LatencyMs: &lat, CreatedAt: now, UpdatedAt: now,
		})
	})
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// WithTx 回调出错回滚。
func TestWithTxRollback(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(getHealthSQL)+` FOR UPDATE`).
		WithArgs(uint64(3), 1).
		WillReturnError(gorm.ErrRecordNotFound)
	mock.ExpectRollback()

	err := s.WithTx(context.Background(), func(tx providersvc.Store) error {
		_, err := tx.GetHealthByProviderIDForUpdate(context.Background(), 3)
		return err
	})
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
