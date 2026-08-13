package llm

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"
)

// bulkhead 每供应商在途调用槽位（CLAUDE.md《并发控制》）。
// 不用 worker pool：LLM 调用是纯网络等待，goroutine 阻塞代价≈0；
// 限的是"对供应商的在途调用数"，不是 goroutine 数量。
type bulkhead struct {
	sem            *semaphore.Weighted
	acquireTimeout time.Duration
}

// newBulkhead 创建容量为 profile.Bulkhead 的槽位池。
func newBulkhead(p Profile) *bulkhead {
	return &bulkhead{
		sem:            semaphore.NewWeighted(int64(p.Bulkhead)),
		acquireTimeout: p.AcquireTimeout,
	}
}

// acquire 抢槽：ctx 感知，超时 fail-fast 返回 ErrProviderBusy（不无限排队——
// 排队放大超时级联，且等 30 秒再成功不如立刻报错让用户重试）。
// 返回的 release 幂等（调用方多 Close 安全）。
func (b *bulkhead) acquire(ctx context.Context) (release func(), err error) {
	acquireCtx, cancel := context.WithTimeout(ctx, b.acquireTimeout)
	defer cancel()
	if err := b.sem.Acquire(acquireCtx, 1); err != nil {
		return nil, fmt.Errorf("%w: acquire slot: %v", ErrProviderBusy, err)
	}
	var once sync.Once
	return func() {
		once.Do(func() { b.sem.Release(1) })
	}, nil
}
