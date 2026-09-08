// embed.go 是 RAG embedding 统一薄 adapter（spec 02）：手写按 kind 分发，
// 照 prober 直连先例（platform 层不依赖业务模块，provider 配置由调用方解析后传值进来）。
// 独立于 chat 的 bulkhead / 熔断 / 三层超时——embedding 无流式、短平快（~200ms-1s），
// 定位总预算模型：单一 5s ctx 包住整个重试循环（含 sleep），最坏总时长硬封顶 5s。
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"
)

// embedding 包内常量（spec 02 §1 冻结；不走 Profile——上层已有两级粗粒度重试兜底，
// 无调参需求）：独立信号量 4 槽（入库 2 文档 × 1 在途批 + 查询侧 1-2 并发 = 峰值 4）。
const (
	embedTimeout       = 5 * time.Second        // 总预算：包住抢槽后的整个重试循环（含 sleep）
	embedBulkhead      = 4                      // 独立信号量槽位，不占 chat bulkhead
	embedAcquireWait   = 2 * time.Second        // 抢槽超时 → ErrProviderBusy（fail-fast，不重试）
	embedMaxRetries    = 1                      // 重试次数（总尝试 2）：上层有文档级 / 会话级重试兜底
	embedMaxInputs     = 2048                   // 单次输入条目上限（各家 API 上限，分批是调用方的事）
	embedRetryBackoff  = 300 * time.Millisecond // 429 无头 / 5xx / 网络错误的固定退避（单次重试无需满抖动）
	embedRetryAfterCap = 2 * time.Second        // 429 Retry-After 尊重上限，超过直接放弃（内部工具不等长冷却）
	embedBodyLimit     = 64 << 20               // 响应体读取上限（批量向量 JSON 可达数十 MB，不信任外部数据）
)

// ErrEmbeddingUnsupported 该 kind 无 embedding API（claude）——请求都不发，HTTP 400。
var ErrEmbeddingUnsupported = errors.New("EMBEDDING_UNSUPPORTED")

// EmbedOptions 一次 embedding 调用的上游参数，字段与 UpstreamOptions 一一对应；
// 生产路径由 provider 解密解析（ResolveLLMConfig 同款出口），明文凭据只在调用瞬间存在。
type EmbedOptions struct {
	Kind    ProviderKind
	BaseURL string
	APIKey  string
	Model   string
}

// EmbedResult 一次批量 embedding 的结果：向量按 inputs 顺序归位；
// PromptTokens 仅 openai_compatible 填（ollama / gemini 响应无 usage 字段）。
// 维度校验属调用方（spec 05 / 07），本层只透传。
type EmbedResult struct {
	Vectors      [][]float32
	PromptTokens int
}

// Embedder embedding 客户端：NewJSONClient 短超时壳 + 独立信号量（不进 Manager，
// 组合根单例构造；信号量挂实例字段 = 全局 4 槽，同时天然隔离测试）。
type Embedder struct {
	hc  *http.Client
	sem *semaphore.Weighted
}

// NewEmbedder 构造 embedding 客户端（复用共享 Transport，与 chat 流量同池不同闸）。
func NewEmbedder(t http.RoundTripper) *Embedder {
	return &Embedder{
		hc:  NewJSONClient(t, embedTimeout),
		sem: semaphore.NewWeighted(embedBulkhead),
	}
}

// EmbedStrings 批量向量化（spec 02 §2/§3）：前置校验 → 抢槽 → 总预算内重试循环。
// 幂等论证：embedding 是确定性映射、无服务端副作用，向量落库在终态事务一次完成——
// 重试期间不存在半写状态（与 chat 流式「首 token 后绝不重试」的本质区别）。
func (e *Embedder) EmbedStrings(ctx context.Context, opts EmbedOptions, inputs []string) (EmbedResult, error) {
	if opts.Kind == KindClaude {
		return EmbedResult{}, ErrEmbeddingUnsupported
	}
	if len(inputs) > embedMaxInputs {
		return EmbedResult{}, fmt.Errorf("llm: embed: too many inputs %d > %d", len(inputs), embedMaxInputs)
	}
	if len(inputs) == 0 {
		return EmbedResult{}, nil
	}
	tgt, err := embedTarget(opts, inputs)
	if err != nil {
		return EmbedResult{}, err
	}
	// 单一 5s 总预算包住整个重试循环（含 sleep）——不学 chat 的每次 attempt 独立超时：
	// embedding 定位短平快，拖到 10s+ 违背短超时初衷；最坏总时长硬封顶 5s。
	budgetCtx, cancel := context.WithTimeout(ctx, embedTimeout)
	defer cancel()
	// 抢槽（spec 02 §1：独立 4 槽不占 chat bulkhead；2s 拿不到 fail-fast ErrProviderBusy，
	// 不重试——排队放大超时级联）。槽位持有跨重试：defer 在整个循环外层，重试不额外占槽。
	release, err := e.acquire(budgetCtx)
	if err != nil {
		return EmbedResult{}, err
	}
	defer release()
	var lastErr error
	for attempt := 0; attempt <= embedMaxRetries; attempt++ {
		if err := budgetCtx.Err(); err != nil {
			return EmbedResult{}, classifyBudgetErr(err)
		}
		res, aerr := e.attempt(budgetCtx, tgt)
		if aerr == nil {
			return res, nil
		}
		lastErr = aerr
		// ctx 取消/超时：调用方已放弃，不再消耗剩余尝试。
		if budgetCtx.Err() != nil {
			return EmbedResult{}, classifyBudgetErr(budgetCtx.Err())
		}
		class, ok := Classify(aerr)
		if !ok || !class.Retryable() || attempt == embedMaxRetries {
			break
		}
		backoff := embedRetryBackoff
		if class == ClassRateLimited {
			var le *Error
			if errors.As(aerr, &le) && le.RetryAfter > 0 {
				if le.RetryAfter > embedRetryAfterCap {
					// 内部工具不等长冷却：直接放弃，原样返回（RetryAfter 随错误透传供上游决策）。
					return EmbedResult{}, aerr
				}
				backoff = le.RetryAfter
			}
		}
		if !sleepCtx(budgetCtx, backoff) {
			return EmbedResult{}, classifyBudgetErr(budgetCtx.Err())
		}
	}
	return EmbedResult{}, lastErr
}

// acquire 抢 embedding 独立槽位：embedAcquireWait 内拿不到 → ErrProviderBusy
// （bulkhead.acquire 同款形态；fail-fast 是设计不是缺陷——等 30 秒再成功不如立刻报错）。
func (e *Embedder) acquire(ctx context.Context) (func(), error) {
	acquireCtx, cancel := context.WithTimeout(ctx, embedAcquireWait)
	defer cancel()
	if err := e.sem.Acquire(acquireCtx, 1); err != nil {
		return nil, fmt.Errorf("%w: embed slot: %v", ErrProviderBusy, err)
	}
	var once sync.Once
	return func() {
		once.Do(func() { e.sem.Release(1) })
	}, nil
}

// attempt 发一次请求并解析响应；失败翻译成 *Error 分类（adapter 职责——errors.go 预留路径）。
func (e *Embedder) attempt(ctx context.Context, tgt embedReq) (EmbedResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tgt.url, bytes.NewReader(tgt.body))
	if err != nil {
		return EmbedResult{}, &Error{Class: ClassInvalidRequest, Err: fmt.Errorf("llm: embed: build request: %w", err)}
	}
	for k, v := range tgt.headers {
		req.Header.Set(k, v)
	}
	resp, err := e.hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return EmbedResult{}, &Error{Class: ClassTimeout, Err: fmt.Errorf("llm: embed: %w", err)}
		}
		return EmbedResult{}, &Error{Class: ClassNetwork, Err: fmt.Errorf("llm: embed: %w", err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return EmbedResult{}, classifyEmbedStatus(resp)
	}
	res, err := tgt.parse(io.LimitReader(resp.Body, embedBodyLimit))
	if err != nil {
		// 畸形 200 响应不在重试矩阵内（重试无保证），原样上抛（未分类 → 循环处 break）。
		return EmbedResult{}, err
	}
	return res, nil
}

// classifyEmbedStatus 把非 200 状态翻译成分类错误（spec 02 §3 矩阵）：
// 429 → RateLimited（解析 Retry-After，>0 随错误透传）；5xx 含 529 → Overloaded；
// 401/403 → Auth；其余 4xx（400/404/…）→ InvalidRequest。body 摘要（≤1KB）进 Err 便于排障。
func classifyEmbedStatus(resp *http.Response) *Error {
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	err := fmt.Errorf("llm: embed: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return &Error{Class: ClassRateLimited, RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")), Err: err}
	case resp.StatusCode >= 500: // 含 529 overloaded
		return &Error{Class: ClassOverloaded, Err: err}
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return &Error{Class: ClassAuth, Err: err}
	default:
		return &Error{Class: ClassInvalidRequest, Err: err}
	}
}

// parseRetryAfter 解析 Retry-After 头：整数秒；解析失败 / <=0 按无头处理
// （spec 02 §3「429 无头 / 解析失败」同路径；HTTP-date 形态不支持）。
func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	secs, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}

// classifyBudgetErr 预算 ctx 到期的错误形态：DeadlineExceeded → ClassTimeout；
// 调用方主动取消原样上抛（classifyCtxErr 同款语义——让调用方感知取消）。
func classifyBudgetErr(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return &Error{Class: ClassTimeout, Err: fmt.Errorf("llm: embed: budget exhausted: %w", err)}
	}
	return fmt.Errorf("llm: embed: %w", err)
}

// sleepCtx 可取消睡眠（spec 02 §3：sleep 用 select，ctx 到期立即放弃剩余尝试）。
func sleepCtx(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// embedReq 按 kind 构造出的请求目标与响应解析器（照 prober probeTarget 形态）。
type embedReq struct {
	url     string
	headers map[string]string
	body    []byte
	parse   func(io.Reader) (EmbedResult, error)
}

// embedTarget 分发（单一分发点）：claude 在 EmbedStrings 前置拒绝，此处只处理有 embedding API 的家。
func embedTarget(opts EmbedOptions, inputs []string) (embedReq, error) {
	base := strings.TrimSuffix(opts.BaseURL, "/")
	switch opts.Kind {
	case KindOpenAICompatible:
		return openaiEmbedTarget(base, opts, inputs)
	case KindOllama:
		return ollamaEmbedTarget(base, opts, inputs)
	case KindGemini:
		return geminiEmbedTarget(base, opts, inputs)
	default:
		return embedReq{}, fmt.Errorf("llm: embed: unsupported kind %q", opts.Kind)
	}
}

// openaiEmbedResp openai_compatible 响应：data[].index 对应输入顺序（不假设返回顺序，归位），
// usage.prompt_tokens 计入预算护栏与 executions 日志。
type openaiEmbedResp struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
	} `json:"usage"`
}

// openaiEmbedTarget POST {base}/embeddings + Bearer（调研 embedding_api.md §1）。
func openaiEmbedTarget(base string, opts EmbedOptions, inputs []string) (embedReq, error) {
	body, err := json.Marshal(map[string]any{"model": opts.Model, "input": inputs})
	if err != nil {
		return embedReq{}, fmt.Errorf("llm: embed: marshal openai body: %w", err)
	}
	n := len(inputs)
	return embedReq{
		url:     base + "/embeddings",
		headers: map[string]string{"Authorization": "Bearer " + opts.APIKey, "Content-Type": "application/json"},
		body:    body,
		parse: func(r io.Reader) (EmbedResult, error) {
			var resp openaiEmbedResp
			if err := json.NewDecoder(r).Decode(&resp); err != nil {
				return EmbedResult{}, fmt.Errorf("llm: embed: decode openai resp: %w", err)
			}
			vectors := make([][]float32, n)
			for _, d := range resp.Data {
				if d.Index < 0 || d.Index >= n {
					return EmbedResult{}, fmt.Errorf("llm: embed: openai resp index %d out of range [0,%d)", d.Index, n)
				}
				if vectors[d.Index] != nil {
					return EmbedResult{}, fmt.Errorf("llm: embed: openai resp duplicate index %d", d.Index)
				}
				vectors[d.Index] = d.Embedding
			}
			for i, v := range vectors {
				if v == nil {
					return EmbedResult{}, fmt.Errorf("llm: embed: openai resp missing embedding for input %d", i)
				}
			}
			return EmbedResult{Vectors: vectors, PromptTokens: resp.Usage.PromptTokens}, nil
		},
	}, nil
}

// ollamaEmbedResp ollama 响应：{"embeddings"} 按输入顺序返回（无 index 字段、无 usage）。
type ollamaEmbedResp struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// ollamaEmbedTarget POST {base}/api/embed，无鉴权（本地 NDJSON 生态，prober 同款免鉴权）。
func ollamaEmbedTarget(base string, opts EmbedOptions, inputs []string) (embedReq, error) {
	body, err := json.Marshal(map[string]any{"model": opts.Model, "input": inputs})
	if err != nil {
		return embedReq{}, fmt.Errorf("llm: embed: marshal ollama body: %w", err)
	}
	n := len(inputs)
	return embedReq{
		url:     base + "/api/embed",
		headers: map[string]string{"Content-Type": "application/json"},
		body:    body,
		parse: func(r io.Reader) (EmbedResult, error) {
			var resp ollamaEmbedResp
			if err := json.NewDecoder(r).Decode(&resp); err != nil {
				return EmbedResult{}, fmt.Errorf("llm: embed: decode ollama resp: %w", err)
			}
			if len(resp.Embeddings) != n {
				return EmbedResult{}, fmt.Errorf("llm: embed: ollama resp has %d embeddings, want %d", len(resp.Embeddings), n)
			}
			return EmbedResult{Vectors: resp.Embeddings}, nil
		},
	}, nil
}

// geminiEmbedResp gemini 响应：embeddings[].values 按输入顺序（无 index 字段、无 usage）。
type geminiEmbedResp struct {
	Embeddings []struct {
		Values []float32 `json:"values"`
	} `json:"embeddings"`
}

// geminiEmbedTarget POST {base}/models/{model}:batchEmbedContents + x-goog-api-key；
// 请求体逐条 requests[]{model:"models/{m}", content.parts[].text}（Google API 要求
// model 同时出现在 URL 与每个 request 内）。
func geminiEmbedTarget(base string, opts EmbedOptions, inputs []string) (embedReq, error) {
	type part struct {
		Text string `json:"text"`
	}
	type content struct {
		Parts []part `json:"parts"`
	}
	type request struct {
		Model   string  `json:"model"`
		Content content `json:"content"`
	}
	modelRef := "models/" + opts.Model
	reqs := make([]request, len(inputs))
	for i, s := range inputs {
		reqs[i] = request{Model: modelRef, Content: content{Parts: []part{{Text: s}}}}
	}
	body, err := json.Marshal(map[string]any{"requests": reqs})
	if err != nil {
		return embedReq{}, fmt.Errorf("llm: embed: marshal gemini body: %w", err)
	}
	n := len(inputs)
	return embedReq{
		url: base + "/models/" + opts.Model + ":batchEmbedContents",
		headers: map[string]string{
			"x-goog-api-key": opts.APIKey,
			"Content-Type":   "application/json",
		},
		body: body,
		parse: func(r io.Reader) (EmbedResult, error) {
			var resp geminiEmbedResp
			if err := json.NewDecoder(r).Decode(&resp); err != nil {
				return EmbedResult{}, fmt.Errorf("llm: embed: decode gemini resp: %w", err)
			}
			if len(resp.Embeddings) != n {
				return EmbedResult{}, fmt.Errorf("llm: embed: gemini resp has %d embeddings, want %d", len(resp.Embeddings), n)
			}
			vectors := make([][]float32, n)
			for i, e := range resp.Embeddings {
				vectors[i] = e.Values
			}
			return EmbedResult{Vectors: vectors}, nil
		},
	}, nil
}
