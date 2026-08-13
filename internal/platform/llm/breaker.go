package llm

import (
	"github.com/sony/gobreaker"
)

// newBreaker 创建每供应商的熔断器（CLAUDE.md《熔断》，不手写状态机）。
//
// 计数语义：整个重试循环包在 Execute 内——重试过程中的失败不逐次计数，
// 只有重试耗尽后的"最终失败"记一次；连续 BreakerAfter 次最终失败 → 打开 BreakerCooldown。
//
// 分类过滤（429 不计熔断）由 client 层负责：不计熔断的最终错误经结果通道返回，
// gobreaker 看到的只有"计熔断"的失败与成功。
func newBreaker(p Profile, name string) *gobreaker.CircuitBreaker {
	return gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        name,
		MaxRequests: 1,                 // 半开放：1 个探测请求
		Interval:    0,                 // 计数不周期性清零，保持"连续失败"语义
		Timeout:     p.BreakerCooldown, // 打开时长
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= uint32(p.BreakerAfter)
		},
	})
}
