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

	agentsvc "github.com/Karlsk/go-hify/internal/agent/service"
	"github.com/Karlsk/go-hify/internal/platform/page"
)

// store 测试：sqlmock 注入 mock DB，验证 GORM 生成的 SQL 形态（显式列 / 软删过滤 /
// WHERE / ORDER BY）与错误原样上抛；业务翻译（哨兵 / 23503）在 service 层测试覆盖。

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

// 列清单（与 store.go 的 selectAgent 一致，供 NewRows 用）。
var agentCols = []string{"id", "name", "description", "model_id", "fallback_model_id", "system_prompt", "temperature", "max_output_tokens", "created_at", "updated_at", "deleted_at"}

// avals 展开为 agents 一行（列序 = agentCols）；基础列填典型值，变体由参数带入。
func avals(id, modelID uint64, fallback, maxTok *int64, now time.Time) []driver.Value {
	return []driver.Value{id, "客服助手", "回答售后问题", modelID, fallback, "你是售后客服", 0.7, maxTok, now, now, nil}
}

// agentRows 单行 agents 结果集。
func agentRows(vals []driver.Value) *sqlmock.Rows {
	return sqlmock.NewRows(agentCols).AddRow(vals...)
}

const getAgentByIDSQL = `SELECT id, name, description, model_id, fallback_model_id, system_prompt, temperature, max_output_tokens, created_at, updated_at, deleted_at FROM "agents" WHERE "agents"."id" = $1 AND "agents"."deleted_at" IS NULL ORDER BY "agents"."id" LIMIT $2`

const listAgentsCountSQL = `SELECT count(*) FROM "agents" WHERE "agents"."deleted_at" IS NULL`

const listAgentsPageSQL = `SELECT id, name, description, model_id, fallback_model_id, system_prompt, temperature, max_output_tokens, created_at, updated_at, deleted_at FROM "agents" WHERE "agents"."deleted_at" IS NULL ORDER BY id LIMIT $1`

const deleteAgentSQL = `UPDATE "agents" SET "deleted_at"=$1 WHERE "agents"."id" = $2 AND "agents"."deleted_at" IS NULL`

const listToolIDsSQL = `SELECT "tool_id" FROM "agent_tools" WHERE agent_id = $1 ORDER BY id`

const deleteToolsSQL = `DELETE FROM "agent_tools" WHERE agent_id = $1`

// ---- agent ----

func TestCreateAgent(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "agents"`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow(1, now, now))
	mock.ExpectCommit()

	a := &agentsvc.Agent{Name: "客服助手", ModelID: 5, SystemPrompt: "你是售后客服", Temperature: 0.7}
	err := s.CreateAgent(context.Background(), a)
	assert.NoError(t, err)
	assert.Equal(t, uint64(1), a.ID) // RETURNING 回填主键
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateAgentError(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "agents"`)).
		WillReturnError(errors.New("fk violation"))
	mock.ExpectRollback()

	err := s.CreateAgent(context.Background(), &agentsvc.Agent{Name: "x", ModelID: 5})
	assert.Error(t, err) // 原样上抛，翻译在 service 层
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetAgentByID(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	maxTok := int64(4096)
	mock.ExpectQuery(regexp.QuoteMeta(getAgentByIDSQL)).
		WithArgs(uint64(1), 1).
		WillReturnRows(agentRows(avals(1, 5, nil, &maxTok, now)))

	a, err := s.GetAgentByID(context.Background(), 1)
	assert.NoError(t, err)
	assert.Equal(t, "客服助手", a.Name)
	assert.Equal(t, uint64(5), a.ModelID)
	assert.Nil(t, a.FallbackModelID, "未设置备用模型 → nil")
	assert.Equal(t, int64(4096), *a.MaxOutputTokens)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetAgentByIDNotFound(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectQuery(regexp.QuoteMeta(getAgentByIDSQL)).
		WithArgs(uint64(999), 1).
		WillReturnError(gorm.ErrRecordNotFound)

	_, err := s.GetAgentByID(context.Background(), 999)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListAgents(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(listAgentsCountSQL)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectQuery(regexp.QuoteMeta(listAgentsPageSQL)).
		WillReturnRows(sqlmock.NewRows(agentCols).
			AddRow(avals(1, 5, nil, nil, now)...).
			AddRow(avals(2, 6, nil, nil, now)...))

	res, err := s.ListAgents(context.Background(), page.NewOffset(1, 20))
	assert.NoError(t, err)
	assert.Len(t, res.Items, 2)
	assert.Equal(t, int64(2), res.Total)
	assert.Equal(t, "客服助手", res.Items[0].Name) // id 升序
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListAgentsCountError(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectQuery(regexp.QuoteMeta(listAgentsCountSQL)).
		WillReturnError(errors.New("count failed"))

	_, err := s.ListAgents(context.Background(), page.NewOffset(1, 20))
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateAgent(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	// Save 全量 UPDATE（PUT 语义，零值一并覆盖）；参数序不定，故不校参。
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE "agents" SET`)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	a := &agentsvc.Agent{Name: "改名的助手", ModelID: 6, SystemPrompt: "新提示词", Temperature: 0.2}
	a.ID = 1
	err := s.UpdateAgent(context.Background(), a)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteAgent(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	// 软删除：DELETE 被改写为 UPDATE deleted_at，且 WHERE 带软删过滤（已软删的行不再命中）。
	mock.ExpectExec(regexp.QuoteMeta(deleteAgentSQL)).
		WithArgs(sqlmock.AnyArg(), uint64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := s.DeleteAgent(context.Background(), 1)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteAgentNotFound(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(deleteAgentSQL)).
		WithArgs(sqlmock.AnyArg(), uint64(999)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	err := s.DeleteAgent(context.Background(), 999)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---- agent_tools（绑定） ----

func TestListToolIDsByAgent(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectQuery(regexp.QuoteMeta(listToolIDsSQL)).
		WithArgs(uint64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"tool_id"}).AddRow(10).AddRow(12))

	ids, err := s.ListToolIDsByAgent(context.Background(), 1)
	assert.NoError(t, err)
	assert.Equal(t, []uint64{10, 12}, ids)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteToolsByAgent(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(deleteToolsSQL)).
		WithArgs(uint64(1)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	err := s.DeleteToolsByAgent(context.Background(), 1)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateTools(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	mock.ExpectBegin()
	// 切片插入 = 单条多 VALUES INSERT（两条 VALUES 一次往返）。
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "agent_tools"`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(1, now).AddRow(2, now))
	mock.ExpectCommit()

	err := s.CreateTools(context.Background(), 1, []uint64{10, 12})
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateToolsEmpty(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)

	err := s.CreateTools(context.Background(), 1, nil)
	assert.NoError(t, err)                        // 空列表零 SQL
	assert.NoError(t, mock.ExpectationsWereMet()) // 且不产生任何期望
}

func TestWithTx(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(deleteToolsSQL)).
		WithArgs(uint64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	// fn 内拿到的 tx Store 仍是 service.Store 身份（先删后插的更新路径形态）。
	err := s.WithTx(context.Background(), func(tx agentsvc.Store) error {
		return tx.DeleteToolsByAgent(context.Background(), 1)
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

	err := s.WithTx(context.Background(), func(tx agentsvc.Store) error {
		return boom // fn 出错整笔回滚（agent 行 + 绑定行同生共死）
	})
	assert.ErrorIs(t, err, boom)
	assert.NoError(t, mock.ExpectationsWereMet())
}
