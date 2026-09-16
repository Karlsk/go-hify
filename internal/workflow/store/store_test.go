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

	workflowsvc "github.com/Karlsk/go-hify/internal/workflow/service"
)

// store 测试：sqlmock 注入 mock DB，验证 GORM 生成的 SQL 形态（显式列 / WHERE /
// ORDER BY / 事务序列 / 多 VALUES INSERT）与错误原样上抛；业务翻译（哨兵 / 23505）
// 在 service 层测试覆盖（spec 03 §5，零真实 PG）。

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

// 列清单（与 store.go 的 select* 常量一致，供 NewRows 用）。
var workflowCols = []string{"id", "name", "description", "start_node_key", "status", "created_at", "updated_at"}

var nodeCols = []string{"id", "workflow_id", "node_key", "type", "name", "config", "created_at"}

var edgeCols = []string{"id", "workflow_id", "source_node_key", "target_node_key", "condition", "created_at"}

// wvals 展开为 workflows 一行（列序 = workflowCols）；基础列填典型值，变体由参数带入。
func wvals(id uint64, name, startKey, status string, now time.Time) []driver.Value {
	return []driver.Value{id, name, "意图识别 → 分支", startKey, status, now, now}
}

// workflowRows 单行 workflows 结果集。
func workflowRows(vals []driver.Value) *sqlmock.Rows {
	return sqlmock.NewRows(workflowCols).AddRow(vals...)
}

const getWorkflowByIDSQL = `SELECT id, name, description, start_node_key, status, created_at, updated_at FROM "workflows" WHERE "workflows"."id" = $1 ORDER BY "workflows"."id" LIMIT $2`

// ---- 读路径 ----

func TestGetByID(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(getWorkflowByIDSQL)).
		WithArgs(uint64(1), 1).
		WillReturnRows(workflowRows(wvals(1, "智能客服分流", "classify", "draft", now)))

	wf, err := s.GetByID(context.Background(), 1)
	assert.NoError(t, err)
	assert.Equal(t, "智能客服分流", wf.Name)
	assert.Equal(t, "classify", wf.StartNodeKey)
	assert.Equal(t, "draft", wf.Status)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetByIDNotFound(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectQuery(regexp.QuoteMeta(getWorkflowByIDSQL)).
		WithArgs(uint64(999), 1).
		WillReturnError(gorm.ErrRecordNotFound)

	_, err := s.GetByID(context.Background(), 999)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound) // 原样上抛，翻译在 service 层
	assert.NoError(t, mock.ExpectationsWereMet())
}

const listNodesSQL = `SELECT id, workflow_id, node_key, type, name, config, created_at FROM "workflow_nodes" WHERE workflow_id = $1 ORDER BY id`

const listEdgesSQL = `SELECT id, workflow_id, source_node_key, target_node_key, condition, created_at FROM "workflow_edges" WHERE workflow_id = $1 ORDER BY id`

func TestListNodes(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(listNodesSQL)).
		WithArgs(uint64(42)).
		WillReturnRows(sqlmock.NewRows(nodeCols).
			AddRow(1, uint64(42), "classify", "llm", "意图识别", `{"model_id":"3","prompt":"判断意图"}`, now).
			AddRow(2, uint64(42), "router", "condition", "意图分流", `{"expression":"{{classify}} == 'ORDER_QUERY'"}`, now))

	nodes, err := s.ListNodes(context.Background(), 42)
	assert.NoError(t, err)
	assert.Len(t, nodes, 2)
	assert.Equal(t, "classify", nodes[0].NodeKey) // id 升序 = 插入序还原
	assert.Equal(t, "router", nodes[1].NodeKey)
	assert.Equal(t, `{"expression":"{{classify}} == 'ORDER_QUERY'"}`, nodes[1].Config) // config JSON 原文直存
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListEdges(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	cond := "true"
	mock.ExpectQuery(regexp.QuoteMeta(listEdgesSQL)).
		WithArgs(uint64(42)).
		WillReturnRows(sqlmock.NewRows(edgeCols).
			AddRow(1, uint64(42), "classify", "router", nil, now).
			AddRow(2, uint64(42), "router", "order_api", &cond, now))

	edges, err := s.ListEdges(context.Background(), 42)
	assert.NoError(t, err)
	assert.Len(t, edges, 2)
	assert.Nil(t, edges[0].Condition, "无条件直走 → nil")
	assert.NotNil(t, edges[1].Condition)
	assert.Equal(t, "true", *edges[1].Condition)
	assert.NoError(t, mock.ExpectationsWereMet())
}

const listWorkflowsCountSQL = `SELECT count(*) FROM "workflows"`

const listWorkflowsPageSQL = `SELECT id, name, description, start_node_key, status, created_at, updated_at FROM "workflows" ORDER BY updated_at DESC, id DESC LIMIT $1 OFFSET $2`

func TestList(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(listWorkflowsCountSQL)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	// offset>0 钉住 LIMIT ? OFFSET ? 形态；GORM 对 offset=0 省略 OFFSET 子句
	// （agent store listAgentsPageSQL 同款行为），语义等价。
	mock.ExpectQuery(regexp.QuoteMeta(listWorkflowsPageSQL)).
		WithArgs(10, 20).
		WillReturnRows(sqlmock.NewRows(workflowCols).
			AddRow(wvals(2, "查单流程", "classify", "published", now)...).
			AddRow(wvals(1, "智能客服分流", "classify", "draft", now)...))

	items, total, err := s.List(context.Background(), 20, 10)
	assert.NoError(t, err)
	assert.Len(t, items, 2)
	assert.Equal(t, int64(2), total)
	assert.Equal(t, uint64(2), items[0].ID) // 最近编辑在前（updated_at DESC, id DESC）
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListCountError(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectQuery(regexp.QuoteMeta(listWorkflowsCountSQL)).
		WillReturnError(errors.New("count failed"))

	_, _, err := s.List(context.Background(), 0, 20)
	assert.Error(t, err) // 原样上抛，不吞错
	assert.NoError(t, mock.ExpectationsWereMet())
}

// testGraph 构造一份最小合法图（两节点一条件边），供整图写入测试复用。
func testGraph() (*workflowsvc.Workflow, []workflowsvc.WorkflowNode, []workflowsvc.WorkflowEdge) {
	wf := &workflowsvc.Workflow{
		Name: "智能客服分流", Description: "意图识别 → 分支", StartNodeKey: "classify", Status: "draft",
	}
	cond := "true"
	nodes := []workflowsvc.WorkflowNode{
		{WorkflowID: 0, NodeKey: "classify", Type: "llm", Name: "意图识别", Config: `{"model_id":"3","prompt":"判断意图"}`},
		{WorkflowID: 0, NodeKey: "router", Type: "condition", Name: "意图分流", Config: `{"expression":"{{classify}} == 'ORDER_QUERY'"}`},
	}
	edges := []workflowsvc.WorkflowEdge{
		{SourceNodeKey: "classify", TargetNodeKey: "router"},
		{SourceNodeKey: "router", TargetNodeKey: "order_api", Condition: &cond},
	}
	return wf, nodes, edges
}

// ---- Create：一事务写三表 ----

func TestCreate(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	wf, nodes, edges := testGraph()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "workflows"`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow(42, now, now))
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "workflow_nodes"`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(1, now).AddRow(2, now))
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "workflow_edges"`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(1, now).AddRow(2, now))
	mock.ExpectCommit()

	err := s.Create(context.Background(), wf, nodes, edges)
	assert.NoError(t, err)
	assert.Equal(t, uint64(42), wf.ID) // RETURNING 回填主键
	assert.Equal(t, uint64(42), nodes[0].WorkflowID)
	assert.Equal(t, uint64(42), edges[0].WorkflowID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateEdgesFailRollback(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	wf, nodes, edges := testGraph()
	boom := errors.New("insert edges failed")
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "workflows"`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow(42, now, now))
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "workflow_nodes"`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(1, now).AddRow(2, now))
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "workflow_edges"`)).
		WillReturnError(boom)
	mock.ExpectRollback()

	err := s.Create(context.Background(), wf, nodes, edges)
	assert.ErrorIs(t, err, boom) // %w 链保留，原因可判（spec 03 §5）
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateEmptyEdges(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	wf, nodes, _ := testGraph()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "workflows"`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow(42, now, now))
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "workflow_nodes"`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(1, now).AddRow(2, now))
	mock.ExpectCommit()

	err := s.Create(context.Background(), wf, nodes, nil) // 空 edges 跳过该语句
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

const (
	deleteNodesSQL = `DELETE FROM "workflow_nodes" WHERE workflow_id = $1`
	deleteEdgesSQL = `DELETE FROM "workflow_edges" WHERE workflow_id = $1`
)

// replaceWfSQL 全文钉死：map Updates 键按字母序 + updated_at 由 autoUpdateTime
// 追加尾列——status 不在 SET 内即「编辑不降级」（决策 #6）被形态级断言。
const replaceWfSQL = `UPDATE "workflows" SET "description"=$1,"name"=$2,"start_node_key"=$3,"updated_at"=$4 WHERE id = $5`

// ---- ReplaceGraph：整图替换单事务 ----

func TestReplaceGraph(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	wf, nodes, edges := testGraph()
	wf.ID = 42
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(replaceWfSQL)).
		WithArgs("意图识别 → 分支", "智能客服分流", "classify", sqlmock.AnyArg(), uint64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(deleteNodesSQL)).
		WithArgs(uint64(42)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(regexp.QuoteMeta(deleteEdgesSQL)).
		WithArgs(uint64(42)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "workflow_nodes"`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(3, now).AddRow(4, now))
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "workflow_edges"`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(3, now).AddRow(4, now))
	mock.ExpectCommit()

	moved, err := s.ReplaceGraph(context.Background(), wf, nodes, edges)
	assert.NoError(t, err)
	assert.True(t, moved)
	assert.Equal(t, uint64(42), nodes[0].WorkflowID) // 子表行 FK 由 store 回填
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestReplaceGraphNotFound(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	wf, nodes, edges := testGraph()
	wf.ID = 999
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(replaceWfSQL)).
		WithArgs("意图识别 → 分支", "智能客服分流", "classify", sqlmock.AnyArg(), uint64(999)).
		WillReturnResult(sqlmock.NewResult(0, 0)) // UPDATE 0 行
	mock.ExpectCommit()

	moved, err := s.ReplaceGraph(context.Background(), wf, nodes, edges)
	assert.NoError(t, err)
	assert.False(t, moved, "affected=0 → false，service 翻译 404")
	assert.NoError(t, mock.ExpectationsWereMet()) // 且不再执行后续 DELETE/INSERT
}

func TestReplaceGraphEmptyEdges(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	now := time.Now()
	wf, nodes, _ := testGraph()
	wf.ID = 42
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(replaceWfSQL)).
		WithArgs("意图识别 → 分支", "智能客服分流", "classify", sqlmock.AnyArg(), uint64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(deleteNodesSQL)).
		WithArgs(uint64(42)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(regexp.QuoteMeta(deleteEdgesSQL)).
		WithArgs(uint64(42)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "workflow_nodes"`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(3, now).AddRow(4, now))
	mock.ExpectCommit()

	moved, err := s.ReplaceGraph(context.Background(), wf, nodes, nil) // 空 edges：删了不插
	assert.NoError(t, err)
	assert.True(t, moved)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// updateStatusSQL 全文钉死：单键 map + autoUpdateTime 追加 updated_at；
// WHERE 同时钉 id 与 status IN ——「原子状态迁移、无 get-then-set 竞态窗口」的形态级断言。
const updateStatusSQL = `UPDATE "workflows" SET "status"=$1,"updated_at"=$2 WHERE id = $3 AND status IN ($4,$5)`

// ---- UpdateStatus / Delete ----

func TestUpdateStatusMoved(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(updateStatusSQL)).
		WithArgs("published", sqlmock.AnyArg(), uint64(1), "draft", "disabled").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	moved, err := s.UpdateStatus(context.Background(), 1, []string{"draft", "disabled"}, "published")
	assert.NoError(t, err)
	assert.True(t, moved, "affected>0 → 发生迁移（service 据此判幂等）")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateStatusNotMoved(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	// 单元素 from：IN 展开为 ($4)，与双元素 ($4,$5) 各钉一例。
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE "workflows" SET "status"=$1,"updated_at"=$2 WHERE id = $3 AND status IN ($4)`)).
		WithArgs("disabled", sqlmock.AnyArg(), uint64(2), "published").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	moved, err := s.UpdateStatus(context.Background(), 2, []string{"published"}, "disabled")
	assert.NoError(t, err)
	assert.True(t, moved)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateStatusIdempotentNoop(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE "workflows" SET "status"=$1,"updated_at"=$2 WHERE id = $3 AND status IN ($4,$5)`)).
		WithArgs("published", sqlmock.AnyArg(), uint64(3), "draft", "disabled").
		WillReturnResult(sqlmock.NewResult(0, 0)) // 已 published：IN 不命中，0 行
	mock.ExpectCommit()

	moved, err := s.UpdateStatus(context.Background(), 3, []string{"draft", "disabled"}, "published")
	assert.NoError(t, err)
	assert.False(t, moved, "幂等 no-op：状态未变（service 不报错）")
	assert.NoError(t, mock.ExpectationsWereMet())
}

const deleteWorkflowSQL = `DELETE FROM "workflows" WHERE "workflows"."id" = $1`

func TestDelete(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	// 硬删；nodes/edges 由 FK CASCADE 同步清理（PG 侧行为，不在此断言）。
	mock.ExpectExec(regexp.QuoteMeta(deleteWorkflowSQL)).
		WithArgs(uint64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	deleted, err := s.Delete(context.Background(), 42)
	assert.NoError(t, err)
	assert.True(t, deleted)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteNotFound(t *testing.T) {
	db, mock := newMockDB(t)
	s := New(db)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(deleteWorkflowSQL)).
		WithArgs(uint64(999)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	deleted, err := s.Delete(context.Background(), 999)
	assert.NoError(t, err)
	assert.False(t, deleted, "affected=0 → false，service 翻译 404")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---- SQL 错误路径：原样上抛 + %w 链保留（spec 03 §3 错误链路）----

func TestStoreSQLErrors(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name   string
		expect func(sqlmock.Sqlmock) error // 返回该用例的原因错误（nil = 无原因断言）
		run    func(*Store) error
	}{
		{
			name: "ListNodes 查询失败",
			expect: func(m sqlmock.Sqlmock) error {
				m.ExpectQuery(regexp.QuoteMeta(listNodesSQL)).WithArgs(uint64(1)).WillReturnError(errors.New("q failed"))
				return nil
			},
			run: func(s *Store) error { _, err := s.ListNodes(context.Background(), 1); return err },
		},
		{
			name: "ListEdges 查询失败",
			expect: func(m sqlmock.Sqlmock) error {
				m.ExpectQuery(regexp.QuoteMeta(listEdgesSQL)).WithArgs(uint64(1)).WillReturnError(errors.New("q failed"))
				return nil
			},
			run: func(s *Store) error { _, err := s.ListEdges(context.Background(), 1); return err },
		},
		{
			name: "List 当页查询失败",
			expect: func(m sqlmock.Sqlmock) error {
				m.ExpectQuery(regexp.QuoteMeta(listWorkflowsCountSQL)).
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
				m.ExpectQuery(regexp.QuoteMeta(listWorkflowsPageSQL)).
					WithArgs(10, 20).WillReturnError(errors.New("q failed"))
				return nil
			},
			run: func(s *Store) error { _, _, err := s.List(context.Background(), 20, 10); return err },
		},
		{
			name: "Create nodes 中途失败 Rollback",
			expect: func(m sqlmock.Sqlmock) error {
				boom := errors.New("insert nodes failed")
				m.ExpectBegin()
				m.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "workflows"`)).
					WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow(42, now, now))
				m.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "workflow_nodes"`)).
					WillReturnError(boom)
				m.ExpectRollback()
				return boom
			},
			run: func(s *Store) error {
				wf, nodes, _ := testGraph()
				return s.Create(context.Background(), wf, nodes, nil)
			},
		},
		{
			name: "ReplaceGraph UPDATE 失败 Rollback",
			expect: func(m sqlmock.Sqlmock) error {
				boom := errors.New("update failed")
				m.ExpectBegin()
				m.ExpectExec(regexp.QuoteMeta(replaceWfSQL)).WillReturnError(boom)
				m.ExpectRollback()
				return boom
			},
			run: func(s *Store) error {
				wf, nodes, edges := testGraph()
				wf.ID = 42
				_, err := s.ReplaceGraph(context.Background(), wf, nodes, edges)
				return err
			},
		},
		{
			name: "Delete SQL 失败",
			expect: func(m sqlmock.Sqlmock) error {
				boom := errors.New("delete failed")
				m.ExpectBegin()
				m.ExpectExec(regexp.QuoteMeta(deleteWorkflowSQL)).WillReturnError(boom)
				m.ExpectRollback()
				return boom
			},
			run: func(s *Store) error {
				_, err := s.Delete(context.Background(), 42)
				return err
			},
		},
		{
			name: "UpdateStatus SQL 失败",
			expect: func(m sqlmock.Sqlmock) error {
				boom := errors.New("update status failed")
				m.ExpectBegin()
				m.ExpectExec(regexp.QuoteMeta(updateStatusSQL)).WillReturnError(boom)
				m.ExpectRollback()
				return boom
			},
			run: func(s *Store) error {
				_, err := s.UpdateStatus(context.Background(), 1, []string{"draft", "disabled"}, "published")
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := newMockDB(t)
			s := New(db)
			cause := tt.expect(mock)
			err := tt.run(s)
			assert.Error(t, err) // 原样上抛，不吞错
			if cause != nil {
				assert.ErrorIs(t, err, cause) // %w 链保留
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
