package service

// runscleaner：workflow_runs 保留期清理后台任务（spec 06 FR8 / research R8）。
// 非分区表按 created_at 批量 DELETE（node_runs 随 FK CASCADE 连带删，00019 DDL）；
// 单批带上限防长事务锁累积。形态对齐 platform/logging PartitionMaintainer：
// 组合根 go Start(appCtx) 启动、随优雅关停退出；runOnce 同步单轮供测试直调。

import (
	"context"
	"log/slog"
	"time"
)

// deleteBatchSize 单批 DELETE 行数上限；不满一批 = 本轮清完。
const deleteBatchSize = 500

// runsCleanInterval 默认轮询间隔（启动首轮 + 此后每轮）。
const runsCleanInterval = 24 * time.Hour

// runDeleter 保留期清理的窄面（Store 实现之一，小接口惯例）。
type runDeleter interface {
	// DeleteRunsBefore 批删 created_at < before 的 run 行（单批至多 limit 行），
	// 返回实际删除行数。
	DeleteRunsBefore(ctx context.Context, before time.Time, limit int) (int64, error)
}

// RunsCleaner 保留期清理器：每轮批次删除保留期外的 run 行。
type RunsCleaner struct {
	deleter       runDeleter
	retentionDays int
	interval      time.Duration // 测试注入；<=0 时 Start 回退 runsCleanInterval
}

// NewRunsCleaner 构造清理器。retentionDays 来自 config.Workflow.RunsRetentionDays
//（WORKFLOW_RUNS_RETENTION_DAYS，默认 365）；<=0 表示关闭（Start 仅 WARN，不删）。
func NewRunsCleaner(deleter runDeleter, retentionDays int) *RunsCleaner {
	return &RunsCleaner{deleter: deleter, retentionDays: retentionDays}
}

// Start 启动清理循环：立即跑首轮，此后每 interval 一轮，随 ctx 取消退出。
// retentionDays <= 0 时 WARN 并直接返回（「永不删除」用超大值表达，不设第三态）。
func (c *RunsCleaner) Start(ctx context.Context) {
	if c.retentionDays <= 0 {
		slog.WarnContext(ctx, "workflow runs cleaner disabled (retention <= 0); old runs are never deleted")
		return
	}
	interval := c.interval
	if interval <= 0 { // 防御：零值 interval 会令 NewTicker panic
		interval = runsCleanInterval
	}
	slog.InfoContext(ctx, "workflow runs cleaner started",
		"interval", interval.String(), "retention_days", c.retentionDays)
	c.runOnce(ctx, time.Now())
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			slog.InfoContext(ctx, "workflow runs cleaner stopped")
			return
		case <-t.C:
			c.runOnce(ctx, time.Now())
		}
	}
}

// runOnce 单轮清理：批次循环删 created_at < now - retentionDays 的 run 行，
// 不满一批即本轮清完。任何错误只记日志、不 panic、放弃本轮等下轮重试。
func (c *RunsCleaner) runOnce(ctx context.Context, now time.Time) {
	before := now.AddDate(0, 0, -c.retentionDays)
	for {
		n, err := c.deleter.DeleteRunsBefore(ctx, before, deleteBatchSize)
		if err != nil {
			slog.WarnContext(ctx, "workflow runs cleanup failed; retry next round", "err", err)
			return
		}
		if n < int64(deleteBatchSize) {
			return
		}
	}
}
