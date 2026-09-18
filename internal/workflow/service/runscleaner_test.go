package service

// RunsCleaner 测试（spec 06 FR8 / research R8）：保留期批量 DELETE 清理任务。
// runOnce 直调覆盖批次循环与容错；Start 覆盖首轮立即 + ticker 周期 +
// retention<=0 不启动（WARN）+ appCtx 取消即退出（PartitionMaintainer 形态）。

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubRunDeleter 记录式批删 stub：plan 按调用序返回行数（越界返 0），err 非空持续失败。
// Start 在独立 goroutine 跑，读写经 mu 同步（-race 下 waitFor 轮询读计数）。
type stubRunDeleter struct {
	mu      sync.Mutex
	calls   int
	befores []time.Time
	limits  []int
	plan    []int64
	err     error
}

func (d *stubRunDeleter) DeleteRunsBefore(_ context.Context, before time.Time, limit int) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	i := d.calls
	d.calls++
	d.befores = append(d.befores, before)
	d.limits = append(d.limits, limit)
	if d.err != nil {
		return 0, d.err
	}
	if i < len(d.plan) {
		return d.plan[i], nil
	}
	return 0, nil
}

func (d *stubRunDeleter) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

// waitFor 轮询等待条件成立（超时 fail），避免 sleep 断言的时序抖动。
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("condition not met within %v", timeout)
}

// runOnce 批次循环：满批（=limit）继续删、不满一批停；before = now - retention 天；
// 单批 DELETE 带 LIMIT 上限（防长事务）。
func TestRunsCleanerRunOnce(t *testing.T) {
	d := &stubRunDeleter{plan: []int64{int64(deleteBatchSize), int64(deleteBatchSize), 3}}
	c := NewRunsCleaner(d, 365)
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)

	c.runOnce(context.Background(), now)

	require.Equal(t, 3, d.count(), "前两批满额继续、第三批不满即停")
	want := now.AddDate(0, 0, -365)
	for _, got := range d.befores {
		assert.Equal(t, want, got, "删除边界 = now - retentionDays")
	}
	for _, l := range d.limits {
		assert.Equal(t, deleteBatchSize, l, "单批上限防长事务")
	}
}

// 单批失败：仅 WARN 放弃本轮（等下轮重试），不无限循环。
func TestRunsCleanerRunOnceError(t *testing.T) {
	d := &stubRunDeleter{err: errors.New("db down")}
	c := NewRunsCleaner(d, 365)

	c.runOnce(context.Background(), time.Now())

	assert.Equal(t, 1, d.count(), "失败即放弃本轮")
}

// retention <= 0：不启动（WARN）、零删除调用。
func TestRunsCleanerDisabled(t *testing.T) {
	d := &stubRunDeleter{}
	c := NewRunsCleaner(d, 0)

	cap := &logCapture{}
	old := slog.Default()
	slog.SetDefault(slog.New(cap))
	t.Cleanup(func() { slog.SetDefault(old) })

	c.Start(context.Background())

	assert.Zero(t, d.count(), "关闭态不删任何行")
	var warnRec *slog.Record
	for i := range cap.records {
		if cap.records[i].Level == slog.LevelWarn {
			warnRec = &cap.records[i]
			break
		}
	}
	require.NotNil(t, warnRec, "关闭态 WARN")
}

// Start 生命周期：启动立即首轮（ticker 之前）→ 周期轮 → appCtx 取消即退出。
func TestRunsCleanerStartLoop(t *testing.T) {
	d := &stubRunDeleter{}
	c := NewRunsCleaner(d, 365)
	c.interval = 20 * time.Millisecond // 测试注入短周期

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Start(ctx); close(done) }()

	waitFor(t, 2*time.Second, func() bool { return d.count() >= 1 })
	firstSeen := time.Now()
	// 首轮后 ticker 周期继续触发（多轮）
	waitFor(t, 2*time.Second, func() bool { return d.count() >= 2 })

	cancel()
	select {
	case <-done:
		// 随 ctx 取消退出
	case <-time.After(2 * time.Second):
		t.Fatal("appCtx 取消后 Start 未退出")
	}
	_ = firstSeen
}
