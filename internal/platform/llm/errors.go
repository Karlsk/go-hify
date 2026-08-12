package llm

import "errors"

// platform/llm 在 bulkhead / 熔断处产生的操作哨兵。
//
// 放在 platform/llm 而非 provider/api：bulkhead 与熔断逻辑在本层
// （CLAUDE.md《外部 LLM 调用设计》），本层产生这些错误；而 CLAUDE.md「platform 不依赖任何业务域」
// 禁止 platform/llm import 业务模块的 api 包。
//
// 经 chat / workflow 的 service 用 fmt.Errorf("...: %w") 包装上抛，由各自 handler 用 errors.Is
// 映射到 HTTP 503（见 respond.Fail 与各 handler）。code（Error() 字符串）见 CLAUDE.md《错误处理》。
var (
	// ErrProviderBusy bulkhead 抢槽失败（fail-fast，HTTP 503）。
	ErrProviderBusy = errors.New("PROVIDER_BUSY")
	// ErrProviderUnavailable 熔断打开 / ProviderDown（HTTP 503）。
	ErrProviderUnavailable = errors.New("PROVIDER_UNAVAILABLE")
)
