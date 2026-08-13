package llm

import (
	"context"
	"sync"
	"time"
)

// idleWatchdog 流空闲看门狗（CLAUDE.md《超时》idle 层）：
// 每收一个 chunk 重置计时器；到点 cancel ctx（cause=ErrIdle）。
// 用 time.AfterFunc 而非 channel 版 Timer——无 drain 竞态，Reset 即重调度。
type idleWatchdog struct {
	timer  *time.Timer
	cancel context.CancelCauseFunc
	idle   time.Duration
	once   sync.Once
}

// newIdleWatchdog 启动看门狗；cancel 必须是流 ctx 的 cancel 函数（到点打断上游 Recv）。
func newIdleWatchdog(cancel context.CancelCauseFunc, idle time.Duration) *idleWatchdog {
	w := &idleWatchdog{cancel: cancel, idle: idle}
	w.timer = time.AfterFunc(idle, func() { cancel(ErrIdle) })
	return w
}

// Ping 每收一个 chunk 调用，重置空闲计时。
func (w *idleWatchdog) Ping() {
	if w == nil || w.timer == nil {
		return
	}
	w.timer.Reset(w.idle)
}

// Stop 停止看门狗（幂等）。只停 timer 不 cancel——流的取消由调用方（Stream.Close）负责。
func (w *idleWatchdog) Stop() {
	if w == nil || w.timer == nil {
		return
	}
	w.once.Do(func() { w.timer.Stop() })
}
