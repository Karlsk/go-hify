package logging

// PartitionMaintainer 测试：sqlmock 验证 runOnce 的建/删/容错路径与精确 SQL 字面量
//（边界必须裸日期，见 partition.go 注释）；helper 纯函数表测覆盖跨年/非法名。

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
)

func newPartition(t *testing.T, retentionDays int) (*PartitionMaintainer, sqlmock.Sqlmock) {
	t.Helper()
	db, mock := newMockDB(t)
	return NewPartitionMaintainer(db, retentionDays), mock
}

// expectList 预置分区枚举查询的返回行（names 为空即无分区）。
func expectList(mock sqlmock.Sqlmock, names ...string) {
	rows := sqlmock.NewRows([]string{"relname"})
	for _, n := range names {
		rows.AddRow(n)
	}
	mock.ExpectQuery(regexp.QuoteMeta(listChildrenSQL)).WillReturnRows(rows)
}

func execCreate(mock sqlmock.Sqlmock, name, from, to string) {
	mock.ExpectExec(regexp.QuoteMeta(
		"CREATE TABLE IF NOT EXISTS " + name + " PARTITION OF executions FOR VALUES FROM ('" + from + "') TO ('" + to + "')"))
}

func execDrop(mock sqlmock.Sqlmock, name string) {
	mock.ExpectExec(regexp.QuoteMeta("DROP TABLE IF EXISTS " + name))
}

// ---- 纯函数 helper ----

func TestPartitionHelpers(t *testing.T) {
	// monthStart：带时区的月中时刻 → UTC 月首日
	assert.Equal(t, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		monthStart(time.Date(2026, 9, 18, 23, 59, 0, 0, time.FixedZone("CST", 8*3600))))
	// 月末 / 年末跨界：12 月任意时刻仍归 12-01
	assert.Equal(t, time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
		monthStart(time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)))
	// Dec + 1 月 → 次年 1 月（AddDate 归一化，无需特判）
	assert.Equal(t, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
		monthStart(time.Date(2026, 12, 5, 0, 0, 0, 0, time.UTC)).AddDate(0, 1, 0))

	// partitionName 与 parsePartitionStart 往返
	assert.Equal(t, "executions_2026_12", partitionName(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)))
	p, ok := parsePartitionStart("executions_2026_12")
	assert.True(t, ok)
	assert.Equal(t, time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), p)

	// 非法名：不匹配命名规约 / 月份越界 → ok=false（永不进入建/删判定）
	for _, bad := range []string{
		"executions", "executions_2026", "executions_2026_8",
		"executions_default", "executions_2026_13", "executions_2026_00",
		"messages_2026_01",
	} {
		_, ok := parsePartitionStart(bad)
		assert.False(t, ok, bad)
	}
}

// ---- runOnce：建分区 ----

func TestRunOnceCreatesCurrentAndNext(t *testing.T) {
	m, mock := newPartition(t, 90)
	expectList(mock) // 空库：无任何分区

	execCreate(mock, "executions_2026_09", "2026-09-01", "2026-10-01")
	execCreate(mock, "executions_2026_10", "2026-10-01", "2026-11-01")

	m.runOnce(context.Background(), time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC))
	assert.NoError(t, mock.ExpectationsWereMet()) // 零 DROP
}

func TestRunOnceSkipsExistingAndKeepsFresh(t *testing.T) {
	m, mock := newPartition(t, 90)
	// 当月/下月已存在 → 不建；2026_08 结束 09-01 + 90d = 11-30 > now → 保留
	expectList(mock, "executions_2026_08", "executions_2026_09", "executions_2026_10")

	m.runOnce(context.Background(), time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC))
	assert.NoError(t, mock.ExpectationsWereMet()) // 零 exec 期望 = 无建无删
}

// ---- runOnce：删分区（整体过期语义 + 边界）----

func TestRunOnceDropsExpiredIgnoresGapsAndBadNames(t *testing.T) {
	m, mock := newPartition(t, 90)
	expectList(mock,
		"executions_2026_03", // end 04-01 + 90d = 06-30 < now → 删
		"executions_2026_05", // end 06-01 + 90d = 08-30 < now → 删
		"executions_2026_06", // end 07-01 + 90d = 09-30 > now → 保留（旧规则按起始判定会删，新规则整体判定保留）
		"executions_2026_08",
		"executions_2026_09",
		"executions_default", // 非月命名：跳过，永不删
		"executions_2026_13", // 月份越界：跳过
	)

	execCreate(mock, "executions_2026_10", "2026-10-01", "2026-11-01") // 下月缺失
	execDrop(mock, "executions_2026_03")                               // 排序后确定性顺序
	execDrop(mock, "executions_2026_05")

	m.runOnce(context.Background(), time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRunOnceRetentionBoundary(t *testing.T) {
	// 分区 2026_06（end 07-01）、retention 65：end + 65d = 09-04 00:00Z
	cases := []struct {
		name string
		now  time.Time
		drop bool
	}{
		{"恰好到期当天保留", time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC), false}, // 严格 Before：相等不算过期
		{"过期一秒即删", time.Date(2026, 9, 4, 0, 0, 1, 0, time.UTC), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, mock := newPartition(t, 65)
			expectList(mock, "executions_2026_06", "executions_2026_09", "executions_2026_10")
			if tc.drop {
				execDrop(mock, "executions_2026_06")
			}
			m.runOnce(context.Background(), tc.now)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// ---- runOnce：禁用与容错 ----

func TestRunOnceDisabledNoOp(t *testing.T) {
	m, mock := newPartition(t, 0) // <=0：不建不删，零 SQL
	m.runOnce(context.Background(), time.Now())
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRunOnceListError(t *testing.T) {
	m, mock := newPartition(t, 90)
	mock.ExpectQuery(regexp.QuoteMeta(listChildrenSQL)).WillReturnError(errors.New("42P01")) // 未迁移库等
	m.runOnce(context.Background(), time.Now())                                              // 整轮放弃、不 panic、无后续 SQL
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRunOnceCreateErrorContinues(t *testing.T) {
	m, mock := newPartition(t, 90)
	expectList(mock)
	mock.ExpectExec(regexp.QuoteMeta(
		"CREATE TABLE IF NOT EXISTS executions_2026_09 PARTITION OF executions FOR VALUES FROM ('2026-09-01') TO ('2026-10-01')")).
		WillReturnError(errors.New("create failed"))
	execCreate(mock, "executions_2026_10", "2026-10-01", "2026-11-01")

	m.runOnce(context.Background(), time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC))
	assert.NoError(t, mock.ExpectationsWereMet()) // 首个失败不阻断第二个
}

func TestRunOnceDropErrorContinues(t *testing.T) {
	m, mock := newPartition(t, 90)
	expectList(mock, "executions_2026_03", "executions_2026_05", "executions_2026_09", "executions_2026_10")
	mock.ExpectExec(regexp.QuoteMeta("DROP TABLE IF EXISTS executions_2026_03")).WillReturnError(errors.New("drop failed"))
	execDrop(mock, "executions_2026_05")

	m.runOnce(context.Background(), time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---- Start 循环 ----

func TestStartExitsOnCancel(t *testing.T) {
	m, mock := newPartition(t, 90)
	m.interval = time.Millisecond
	expectList(mock, "executions_2026_09", "executions_2026_10") // 首轮零 DDL；后续轮次枚举失败走 WARN 重试

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		m.Start(ctx)
		close(done)
	}()
	time.Sleep(30 * time.Millisecond) // 覆盖至少一次 ticker 触发
	cancel()
	select {
	case <-done: // 随 ctx 取消退出，无泄漏
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not exit after ctx cancel")
	}
}

func TestStartDisabledReturnsImmediately(t *testing.T) {
	m, mock := newPartition(t, 0)
	m.Start(context.Background()) // 同步返回（WARN 禁用），零 SQL
	assert.NoError(t, mock.ExpectationsWereMet())
}
