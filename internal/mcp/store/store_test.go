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

	mcpsvc "github.com/Karlsk/go-hify/internal/mcp/service"
	"github.com/Karlsk/go-hify/internal/platform/page"
)

// store 测试：sqlmock 注入 mock DB，验证 GORM 生成的 SQL 形态（显式列 / WHERE /
// ORDER BY / ON CONFLICT 目标列）与错误原样上抛；业务翻译（哨兵 / 23505 / 23503）
// 在 service 层测试覆盖。

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
	serverCols = []string{"id", "name", "transport", "command", "url", "env", "enabled", "created_at", "updated_at"}
	toolCols   = []string{"id", "mcp_server_id", "name", "description", "input_schema", "created_at", "updated_at"}
)

// svals 展开为 mcp_servers 一行（列序 = serverCols）；env 传 JSON 字符串
// （provider auth_config 同款：jsonb 列在 mock 行里即 JSON 文本）。
func svals(id uint64, name, transport, command, url, envJSON string, enabled bool, now time.Time) []driver.Value {
	return []driver.Value{id, name, transport, command, url, envJSON, enabled, now, now}
}

// tvals 展开为 mcp_tools 一行（列序 = toolCols）。
func tvals(id, serverID uint64, name, desc, schemaJSON string, now time.Time) []driver.Value {
	return []driver.Value{id, serverID, name, desc, schemaJSON, now, now}
}

const getServerByIDSQL = `SELECT id, name, transport, command, url, env, enabled, created_at, updated_at FROM "mcp_servers" WHERE "mcp_servers"."id" = $1 ORDER BY "mcp_servers"."id" LIMIT $2`

const listServersCountSQL = `SELECT count(*) FROM "mcp_servers"`

const listServersPageSQL = `SELECT id, name, transport, command, url, env, enabled, created_at, updated_at FROM "mcp_servers" ORDER BY id LIMIT $1`

const listServersByIDsSQL = `SELECT id, name, transport, command, url, env, enabled, created_at, updated_at FROM "mcp_servers" WHERE id IN ($1,$2) ORDER BY id`

const deleteServerSQL = `DELETE FROM "mcp_servers" WHERE "mcp_servers"."id" = $1`

const listToolsByServerSQL = `SELECT id, mcp_server_id, name, description, input_schema, created_at, updated_at FROM "mcp_tools" WHERE mcp_server_id = $1 ORDER BY name`

const countToolsSQL = `SELECT mcp_server_id, COUNT(*) AS cnt FROM "mcp_tools" WHERE mcp_server_id IN ($1,$2) GROUP BY "mcp_server_id"`

const listToolsByIDsSQL = `SELECT id, mcp_server_id, name, description, input_schema, created_at, updated_at FROM "mcp_tools" WHERE id IN ($1,$2) ORDER BY id`

// ---- mcp_servers ----

func TestCreateServer(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	mock.ExpectBegin()
	// INSERT 列清单须含 enabled（provider 踩坑 #1 零值陷阱的列在位钉住：
	// enabled=false 是合法显式值，必须随 INSERT 落库）。
	mock.ExpectQuery(`INSERT INTO "mcp_servers".*enabled`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow(1, now, now))
	mock.ExpectCommit()

	sv := &mcpsvc.MCPServer{Name: "天气", Transport: "http", URL: "http://mcp.local/mcp", Enabled: false}
	err := s.CreateServer(context.Background(), sv)
	assert.NoError(t, err)
	assert.Equal(t, uint64(1), sv.ID) // RETURNING 回填主键
	assert.False(t, sv.Enabled, "显式 false 不被吞")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateServerError(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "mcp_servers"`)).
		WillReturnError(errors.New("uq violation"))
	mock.ExpectRollback()

	err := s.CreateServer(context.Background(), &mcpsvc.MCPServer{Name: "x", Transport: "http"})
	assert.Error(t, err) // 原样上抛，翻译在 service 层
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetServerByID(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(getServerByIDSQL)).
		WithArgs(uint64(1), 1).
		WillReturnRows(sqlmock.NewRows(serverCols).AddRow(svals(1, "天气", "stdio", "uvx mcp-weather", "", `{"API_KEY":"k"}`, false, now)...))

	sv, err := s.GetServerByID(context.Background(), 1)
	assert.NoError(t, err)
	assert.Equal(t, "天气", sv.Name)
	assert.Equal(t, "stdio", sv.Transport)
	assert.Equal(t, map[string]string{"API_KEY": "k"}, sv.Env, "env jsonb 扫描回 map")
	assert.False(t, sv.Enabled, "enabled=false 须在列清单内（漏列 → 恒 true，停用状态读不出）")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetServerByIDNotFound(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectQuery(regexp.QuoteMeta(getServerByIDSQL)).
		WithArgs(uint64(9), 1).
		WillReturnRows(sqlmock.NewRows(serverCols))

	_, err := s.GetServerByID(context.Background(), 9)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListServersByIDs(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(listServersByIDsSQL)).
		WithArgs(uint64(1), uint64(2)).
		WillReturnRows(sqlmock.NewRows(serverCols).
			AddRow(svals(1, "天气", "http", "", "http://a/mcp", "{}", true, now)...).
			AddRow(svals(2, "搜索", "stdio", "uvx mcp-search", "", "{}", false, now)...))

	items, err := s.ListServersByIDs(context.Background(), []uint64{1, 2})
	assert.NoError(t, err)
	assert.Len(t, items, 2)
	assert.False(t, items[1].Enabled, "绑定校验依赖 enabled 标志")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListServersByIDsEmpty(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)

	items, err := s.ListServersByIDs(context.Background(), nil)
	assert.NoError(t, err)                        // 空列表零 SQL
	assert.Empty(t, items)                        // 且不产生任何期望
	assert.NoError(t, mock.ExpectationsWereMet()) // 防生成 IN () 非法语法
}

func TestListServers(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(listServersCountSQL)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))
	mock.ExpectQuery(regexp.QuoteMeta(listServersPageSQL)).
		WillReturnRows(sqlmock.NewRows(serverCols).
			AddRow(svals(1, "天气", "http", "", "http://a/mcp", "{}", true, now)...).
			AddRow(svals(2, "搜索", "stdio", "uvx mcp-search", "", "{}", true, now)...))

	res, err := s.ListServers(context.Background(), page.NewOffset(1, 20))
	assert.NoError(t, err)
	assert.Len(t, res.Items, 2)
	assert.Equal(t, int64(3), res.Total)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateServer(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE "mcp_servers"`)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	sv := &mcpsvc.MCPServer{Name: "天气2", Transport: "http", Enabled: true}
	sv.ID = 1 // Save 前置：主键有效（service 层先 GetServerByID 取得）
	err := s.UpdateServer(context.Background(), sv)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteServer(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(deleteServerSQL)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	assert.NoError(t, s.DeleteServer(context.Background(), 1))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteServerNotFound(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(deleteServerSQL)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	err := s.DeleteServer(context.Background(), 9)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteServerFKBlocked(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	// 23503：有 Agent 绑定，RESTRICT 拦下级联删——原样上抛，翻译在 service 层。
	mock.ExpectExec(regexp.QuoteMeta(deleteServerSQL)).
		WillReturnError(errors.New(`pq: update or delete on table "mcp_servers" violates foreign key constraint "fk_agent_tools_tool_id" on table "agent_tools"`))
	mock.ExpectRollback()

	err := s.DeleteServer(context.Background(), 1)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "fk_agent_tools_tool_id")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---- mcp_tools ----

func TestUpsertTools(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	// 冲突目标必须是 (mcp_server_id, name)（= uq_mcp_tools_server_name，discover
	// 幂等刷新的锚点），且 input_schema 在 DO UPDATE 集合内（漏列 → schema 永不刷新；
	// GORM 渲染为 "excluded"."input_schema"）。
	mock.ExpectQuery(`INSERT INTO "mcp_tools".+ON CONFLICT \("mcp_server_id",\s?"name"\) DO UPDATE SET .+"excluded"\."input_schema"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1).AddRow(2))
	mock.ExpectCommit()

	tools := []mcpsvc.MCPTool{
		{MCPServerID: 7, Name: "search", Description: "搜索", InputSchema: map[string]any{"type": "object"}},
		{MCPServerID: 7, Name: "fetch", Description: "抓取", InputSchema: map[string]any{"type": "object"}},
	}
	err := s.UpsertTools(context.Background(), tools)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpsertToolsEmpty(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)

	err := s.UpsertTools(context.Background(), nil)
	assert.NoError(t, err)                        // 空列表零 SQL（server 0 工具合法）
	assert.NoError(t, mock.ExpectationsWereMet()) // 且不产生任何期望
}

func TestListToolsByServer(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(listToolsByServerSQL)).
		WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows(toolCols).
			AddRow(tvals(1, 7, "fetch", "抓取", `{"type":"object","properties":{"q":{"type":"string"}}}`, now)...).
			AddRow(tvals(2, 7, "search", "搜索", `{"type":"object"}`, now)...))

	items, err := s.ListToolsByServer(context.Background(), 7)
	assert.NoError(t, err)
	assert.Len(t, items, 2)
	assert.Equal(t, "fetch", items[0].Name, "name 升序")
	assert.Equal(t, map[string]any{"type": "object"}, items[1].InputSchema, "input_schema jsonb 扫描回 map")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCountToolsByServerIDs(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectQuery(regexp.QuoteMeta(countToolsSQL)).
		WithArgs(uint64(1), uint64(2)).
		WillReturnRows(sqlmock.NewRows([]string{"mcp_server_id", "cnt"}).
			AddRow(uint64(1), int64(3)).
			AddRow(uint64(2), int64(0)))

	counts, err := s.CountToolsByServerIDs(context.Background(), []uint64{1, 2})
	assert.NoError(t, err)
	assert.Equal(t, map[uint64]int64{1: 3, 2: 0}, counts)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCountToolsByServerIDsEmpty(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)

	counts, err := s.CountToolsByServerIDs(context.Background(), nil)
	assert.NoError(t, err) // 空列表零 SQL，返回空 map
	assert.Empty(t, counts)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListToolsByIDs(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(listToolsByIDsSQL)).
		WithArgs(uint64(1), uint64(2)).
		WillReturnRows(sqlmock.NewRows(toolCols).
			AddRow(tvals(1, 7, "fetch", "抓取", `{"type":"object"}`, now)...).
			AddRow(tvals(2, 8, "search", "搜索", `{"type":"object"}`, now)...))

	items, err := s.ListToolsByIDs(context.Background(), []uint64{1, 2})
	assert.NoError(t, err)
	assert.Len(t, items, 2)
	assert.Equal(t, uint64(7), items[0].MCPServerID, "绑定校验依赖 server_id 关联")
	assert.NoError(t, mock.ExpectationsWereMet())
}
