package logging

// executions 表分区维护（运行时维护 SQL，非表结构演进——迁移仍是 DDL 唯一通道，
// 这里只管理已迁移分区表的分区）。曾由 backup 容器 cron SQL 承担（已下线）：
// 容器职责回归 pg_dump 单一，且应用内任务在 dev（make start）与 prod（compose）
// 同一条路径生效——修掉「dev 无分区维护、executions 写入 23514」的坑。
//
// 语义（drop 规则）：分区「整体」超过保留期才删——分区结束日 + 保留天数 < now（严格 <）。
// 在线窗口恒 ≥ 保留天数（默认 90 = 实际 90~120 天锯齿）；此前 backup SQL 按「分区起始
// 90 天」判定会让窗口下探到 61 天。规则切换后的首轮会比旧规则多保留约一个月，预期行为。
//
// 并发：不加 advisory lock——单实例部署是既定约束，且 CREATE/DROP IF NOT EXISTS 幂等。

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"time"

	"gorm.io/gorm"
)

// maintainInterval 分区维护轮询间隔（启动首轮 + 此后每轮；测试注入短间隔覆盖）。
const maintainInterval = 24 * time.Hour

// executionsPartitionRE 匹配月分区名 executions_YYYY_MM（年 4 位、月 2 位）。
var executionsPartitionRE = regexp.MustCompile(`^executions_(\d{4})_(\d{2})$`)

// listChildrenSQL 枚举 executions 的子分区（含 default 分区等非月命名——由调用方过滤）。
// 未迁移库（executions 表不存在）时 'executions'::regclass 直接报 42P01，走错误路径不崩。
const listChildrenSQL = `SELECT c.relname
FROM pg_inherits i
JOIN pg_class c ON c.oid = i.inhrelid
WHERE i.inhparent = 'executions'::regclass`

// PartitionMaintainer executions 表分区维护后台任务：每轮建「当月 + 下月」分区、
// 删整体超过保留期的旧分区。StartProber 同款模式——组合根 go Start(appCtx) 启动、
// 随 ctx 取消退出；runOnce 同步单轮供测试直调。
type PartitionMaintainer struct {
	db            *gorm.DB
	retentionDays int
	interval      time.Duration // 测试注入；<=0 时 Start 回退 maintainInterval
}

// NewPartitionMaintainer 构造分区维护器。retentionDays 来自 config.Logging.ExecutionsRetentionDays
// （EXECUTIONS_RETENTION_DAYS，默认 90）；<=0 表示关闭（Start 仅 WARN，不建不删）。
func NewPartitionMaintainer(db *gorm.DB, retentionDays int) *PartitionMaintainer {
	return &PartitionMaintainer{db: db, retentionDays: retentionDays}
}

// Start 启动维护循环：立即跑首轮（监听开始前当月分区就绪），此后每 interval 一轮，
// 随 ctx 取消退出。retentionDays <= 0 时 WARN 并直接返回（组合根正常不会在此情形启动）。
func (m *PartitionMaintainer) Start(ctx context.Context) {
	if m.retentionDays <= 0 {
		slog.WarnContext(ctx, "executions partition maintainer disabled (retention <= 0); partitions are neither created nor dropped")
		return
	}
	interval := m.interval
	if interval <= 0 { // 防御：非构造函数路径的零值 interval 会令 NewTicker panic
		interval = maintainInterval
	}
	slog.InfoContext(ctx, "executions partition maintainer started", "interval", interval.String(), "retention_days", m.retentionDays)
	m.runOnce(ctx, time.Now())
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			slog.InfoContext(ctx, "executions partition maintainer stopped")
			return
		case <-t.C:
			m.runOnce(ctx, time.Now())
		}
	}
}

// runOnce 单轮维护：枚举子分区 → 建缺失的当月/下月 → 删整体超期的旧分区。
// 任何 SQL 错误只记日志、不 panic、不阻断本轮其余操作（枚举失败则整轮放弃等下轮重试）。
func (m *PartitionMaintainer) runOnce(ctx context.Context, now time.Time) {
	if m.retentionDays <= 0 { // 防御：组合根关闭时不应到达
		return
	}
	start := time.Now()
	now = now.UTC()

	var names []string
	if err := m.db.WithContext(ctx).Raw(listChildrenSQL).Scan(&names).Error; err != nil {
		slog.WarnContext(ctx, "executions partition maintenance: list partitions failed; retry next round", "err", err)
		return
	}

	existing := make(map[string]time.Time, len(names)) // 分区名 → 月首日
	for _, n := range names {
		if p, ok := parsePartitionStart(n); ok {
			existing[n] = p
		} // 非月命名（default 分区等）静默跳过：不参与建/删判定
	}

	// 建当月 + 下月（缺失才建；幂等）。名字与日期由本包 helper 生成、非外部输入，
	// Sprintf 拼接安全；边界字面量必须是裸日期（与 migrations/00007 同形、由服务器时区
	// 解释）——写成显式 +00 偏移会与既有分区边界错位产生 gap/overlap。
	created := 0
	for _, p := range [2]time.Time{monthStart(now), monthStart(now).AddDate(0, 1, 0)} {
		name := partitionName(p)
		if _, ok := existing[name]; ok {
			continue
		}
		sql := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s PARTITION OF executions FOR VALUES FROM ('%s') TO ('%s')",
			name, p.Format("2006-01-02"), p.AddDate(0, 1, 0).Format("2006-01-02"))
		if err := m.db.WithContext(ctx).Exec(sql).Error; err != nil {
			slog.ErrorContext(ctx, "create executions partition failed", "partition", name, "err", err)
			continue
		}
		created++
		slog.InfoContext(ctx, "created executions partition", "partition", name)
	}

	// 删整体超期的分区：结束日 + 保留天数 < now（严格 <，恰好到期当天保留）。
	dropped := 0
	expired := make([]string, 0, len(existing))
	for name, p := range existing {
		if p.AddDate(0, 1, m.retentionDays).Before(now) {
			expired = append(expired, name)
		}
	}
	sort.Strings(expired) // 确定性删除顺序，日志可复现
	for _, name := range expired {
		if err := m.db.WithContext(ctx).Exec(fmt.Sprintf("DROP TABLE IF EXISTS %s", name)).Error; err != nil {
			slog.ErrorContext(ctx, "drop executions partition failed", "partition", name, "err", err)
			continue
		}
		dropped++
		slog.InfoContext(ctx, "dropped executions partition", "partition", name)
	}

	slog.InfoContext(ctx, "executions partition round done",
		"scanned", len(names), "created", created, "dropped", dropped,
		"retention_days", m.retentionDays, "duration_ms", time.Since(start).Milliseconds())
}

// monthStart 返回 t 所在月份首日 00:00 UTC（月份计算统一 UTC，命名与宿主时区无关；
// 宿主与 PG 的时钟偏差只会让「下月分区」提前或延后数小时建成，当月+下月双建覆盖之）。
func monthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// partitionName 月首日 → 分区名 executions_YYYY_MM（与 migrations/00007 命名一致）。
func partitionName(start time.Time) string {
	return fmt.Sprintf("executions_%04d_%02d", start.Year(), int(start.Month()))
}

// parsePartitionStart 分区名 → 月首日 UTC；名字不匹配命名规约或月份不在 1..12 时 ok=false。
func parsePartitionStart(name string) (time.Time, bool) {
	m := executionsPartitionRE.FindStringSubmatch(name)
	if m == nil {
		return time.Time{}, false
	}
	var year, month int
	if _, err := fmt.Sscanf(m[1], "%4d", &year); err != nil {
		return time.Time{}, false
	}
	if _, err := fmt.Sscanf(m[2], "%2d", &month); err != nil || month < 1 || month > 12 {
		return time.Time{}, false
	}
	return time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC), true
}
