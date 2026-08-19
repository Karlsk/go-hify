package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	providerapi "github.com/Karlsk/go-hify/internal/provider/api"
)

// 探测引擎用例：kind 分发（URL / 认证头）、HTTP 结果分类、body 解析、DEGRADED 状态机纯函数。
// HTTP 侧全部打 httptest 桩，不打真实外网。

func TestProbeTarget(t *testing.T) {
	cases := []struct {
		name    string
		kind    string
		base    string
		key     string
		wantURL string
		wantErr bool
		headers map[string]string // 期望精确相等的认证头（nil = 不查）
		noAuth  bool              // 期望完全无认证头
	}{
		{
			name: "openai 默认 base", kind: providerapi.KindOpenAI, base: "", key: "sk-x",
			wantURL: "https://api.openai.com/v1/models",
			headers: map[string]string{"Authorization": "Bearer sk-x"},
		},
		{
			name: "openai 自定义 base 尾斜杠容错", kind: providerapi.KindOpenAI, base: "http://gw:8000/v1/", key: "sk-x",
			wantURL: "http://gw:8000/v1/models",
			headers: map[string]string{"Authorization": "Bearer sk-x"},
		},
		{
			name: "compatible 缺 base 报错", kind: providerapi.KindOpenAICompatible, base: "", key: "k",
			wantErr: true,
		},
		{
			name: "compatible 带 base 走 Bearer", kind: providerapi.KindOpenAICompatible, base: "http://vllm:8000/v1", key: "k",
			wantURL: "http://vllm:8000/v1/models",
			headers: map[string]string{"Authorization": "Bearer k"},
		},
		{
			name: "claude 默认 base 带 key", kind: providerapi.KindClaude, base: "", key: "ck",
			wantURL: "https://api.anthropic.com/v1/models",
			headers: map[string]string{"x-api-key": "ck", "anthropic-version": anthropicVersion},
		},
		{
			name: "claude 无 key 也必带版本头（缺头即 400 误判）", kind: providerapi.KindClaude, base: "", key: "",
			wantURL: "https://api.anthropic.com/v1/models",
			headers: map[string]string{"anthropic-version": anthropicVersion},
			noAuth:  true,
		},
		{
			name: "gemini 默认 base", kind: providerapi.KindGemini, base: "", key: "gk",
			wantURL: "https://generativelanguage.googleapis.com/v1beta/models",
			headers: map[string]string{"x-goog-api-key": "gk"},
		},
		{
			name: "ollama 默认 base 无认证", kind: providerapi.KindOllama, base: "", key: "",
			wantURL: "http://localhost:11434/api/tags",
			noAuth:  true,
		},
		{
			name: "未知 kind 报错", kind: "azure", base: "http://x", key: "",
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			url, h, err := probeTarget(c.kind, c.base, c.key)
			if c.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, c.wantURL, url)
			for k, want := range c.headers {
				assert.Equal(t, want, h[k])
			}
			if c.noAuth {
				for _, forbidden := range []string{"Authorization", "x-api-key", "x-goog-api-key"} {
					assert.NotContains(t, h, forbidden, "该 kind 不应带认证头 %s", forbidden)
				}
			}
		})
	}
}

// TestProbe_OK 各 kind 直连 200：URL 路径、认证头、模型计数（data[] / models[] 两种形态）。
func TestProbe_OK(t *testing.T) {
	t.Run("openai data 形态", func(t *testing.T) {
		var gotPath, gotAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o"},{"id":"gpt-4o-mini"}]}`))
		}))
		defer srv.Close()

		res := probe(context.Background(), srv.Client(), providerapi.KindOpenAI, srv.URL, "sk-live")
		assert.True(t, res.success)
		assert.Empty(t, res.errMsg)
		assert.EqualValues(t, 2, res.modelCount)
		assert.GreaterOrEqual(t, res.latency, time.Duration(0))
		assert.Equal(t, "/models", gotPath)
		assert.Equal(t, "Bearer sk-live", gotAuth)
	})

	t.Run("ollama models 形态且无认证头", func(t *testing.T) {
		var gotPath, gotAuth, gotAPIKey string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath, gotAuth, gotAPIKey = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("x-api-key")
			_, _ = w.Write([]byte(`{"models":[{"name":"llama3"},{"name":"qwen2"},{"name":"mistral"}]}`))
		}))
		defer srv.Close()

		res := probe(context.Background(), srv.Client(), providerapi.KindOllama, srv.URL, "")
		assert.True(t, res.success)
		assert.EqualValues(t, 3, res.modelCount)
		assert.Equal(t, "/api/tags", gotPath)
		assert.Empty(t, gotAuth, "ollama 探测不带 Bearer")
		assert.Empty(t, gotAPIKey)
	})

	t.Run("claude 头集完整", func(t *testing.T) {
		var gotKey, gotVersion string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotKey, gotVersion = r.Header.Get("x-api-key"), r.Header.Get("anthropic-version")
			_, _ = w.Write([]byte(`{"data":[{"id":"claude-sonnet-4-5"}]}`))
		}))
		defer srv.Close()

		res := probe(context.Background(), srv.Client(), providerapi.KindClaude, srv.URL, "ck-live")
		assert.True(t, res.success)
		assert.EqualValues(t, 1, res.modelCount)
		assert.Equal(t, "ck-live", gotKey)
		assert.Equal(t, anthropicVersion, gotVersion)
	})
}

// TestProbe_FailureClassification 非 2xx / 网络错误的分类。
func TestProbe_FailureClassification(t *testing.T) {
	t.Run("429 算可达 计数 0", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limited"}`))
		}))
		defer srv.Close()

		res := probe(context.Background(), srv.Client(), providerapi.KindOpenAI, srv.URL, "sk")
		assert.True(t, res.success, "限流 ≠ 不可达")
		assert.EqualValues(t, 0, res.modelCount, "429 body 非模型列表")
	})

	t.Run("401 鉴权失败", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid api key"}`))
		}))
		defer srv.Close()

		res := probe(context.Background(), srv.Client(), providerapi.KindOpenAI, srv.URL, "sk-bad")
		assert.False(t, res.success)
		assert.Contains(t, res.errMsg, "401")
		assert.Contains(t, res.errMsg, "鉴权失败")
		assert.Contains(t, res.errMsg, "invalid api key", "失败带截断摘要")
	})

	t.Run("500 带摘要", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("upstream exploded"))
		}))
		defer srv.Close()

		res := probe(context.Background(), srv.Client(), providerapi.KindGemini, srv.URL, "gk")
		assert.False(t, res.success)
		assert.Contains(t, res.errMsg, "500")
		assert.Contains(t, res.errMsg, "upstream exploded")
	})

	t.Run("网络错误（连接拒绝）", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		base := srv.URL
		srv.Close() // 立即关掉，端口不可达

		res := probe(context.Background(), http.DefaultClient, providerapi.KindOllama, base, "")
		assert.False(t, res.success)
		assert.NotEmpty(t, res.errMsg)
		assert.LessOrEqual(t, len([]rune(res.errMsg)), probeErrMaxLen, "错误信息按 200 字符截断")
	})

	t.Run("超时归为失败", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(200 * time.Millisecond) // 超过桩 client 的 50ms 超时
		}))
		defer srv.Close()
		slow := &http.Client{Timeout: 50 * time.Millisecond}

		res := probe(context.Background(), slow, providerapi.KindOpenAI, srv.URL, "sk")
		assert.False(t, res.success)
		assert.NotEmpty(t, res.errMsg)
	})

	t.Run("ctx 已取消", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		res := probe(ctx, http.DefaultClient, providerapi.KindOllama, "http://localhost:11434", "")
		assert.False(t, res.success)
		assert.NotEmpty(t, res.errMsg)
	})
}

func TestCountModels(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int32
	}{
		{"data 形态", `{"data":[{"id":"a"},{"id":"b"},{"id":"c"}]}`, 3},
		{"models 形态", `{"models":[{"name":"a"}]}`, 1},
		{"两者都有 data 优先", `{"data":[{"id":"a"}],"models":[{"name":"b"},{"name":"c"}]}`, 1},
		{"空对象", `{}`, 0},
		{"非 JSON（HTML 错误页）", `<html>bad gateway</html>`, 0},
		{"空 body", ``, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.EqualValues(t, c.want, countModels(strings.NewReader(c.body)))
		})
	}
}

func TestTruncateErr(t *testing.T) {
	assert.Equal(t, "short", truncateErr("short"))
	assert.Equal(t, probeErrMaxLen, len([]rune(truncateErr(strings.Repeat("x", 300)))))
	assert.Equal(t, probeErrMaxLen, len([]rune(truncateErr(strings.Repeat("错", 300)))), "按 rune 截断不劈开多字节字符")
}

func TestApplyProbeResult(t *testing.T) {
	now := time.Now()
	ok := func(d time.Duration) probeResult { return probeResult{success: true, latency: d} }
	fail := probeResult{errMsg: "HTTP 500"}

	t.Run("首次成功快 → up", func(t *testing.T) {
		h := applyProbeResult(1, nil, ok(100*time.Millisecond), now)
		assert.Equal(t, providerapi.HealthUp, h.Status)
		assert.EqualValues(t, 0, h.FailCount)
		require.NotNil(t, h.LastSuccessAt)
		require.NotNil(t, h.LastCheckAt)
		assert.Equal(t, now, *h.LastSuccessAt)
	})

	t.Run("首次成功慢 → degraded", func(t *testing.T) {
		h := applyProbeResult(1, nil, ok(probeSlowLatency+time.Second), now)
		assert.Equal(t, providerapi.HealthDegraded, h.Status)
		assert.EqualValues(t, 0, h.FailCount, "慢成功同样清零")
	})

	t.Run("首次失败 → degraded fc=1", func(t *testing.T) {
		h := applyProbeResult(1, nil, fail, now)
		assert.Equal(t, providerapi.HealthDegraded, h.Status)
		assert.EqualValues(t, 1, h.FailCount)
		assert.Nil(t, h.LastSuccessAt)
	})

	t.Run("连续失败三次 → down", func(t *testing.T) {
		h := applyProbeResult(1, nil, fail, now)
		h = applyProbeResult(1, h, fail, now)
		assert.Equal(t, providerapi.HealthDegraded, h.Status)
		assert.EqualValues(t, 2, h.FailCount)
		h = applyProbeResult(1, h, fail, now)
		assert.Equal(t, providerapi.HealthDown, h.Status)
		assert.EqualValues(t, 3, h.FailCount)
	})

	t.Run("down 后恢复 → up 清零", func(t *testing.T) {
		old := &ProviderHealth{Status: providerapi.HealthDown, FailCount: 5}
		h := applyProbeResult(1, old, ok(50*time.Millisecond), now)
		assert.Equal(t, providerapi.HealthUp, h.Status)
		assert.EqualValues(t, 0, h.FailCount)
	})

	t.Run("失败保留 last_success_at 不改 old", func(t *testing.T) {
		t0 := now.Add(-time.Hour)
		old := &ProviderHealth{Status: providerapi.HealthUp, FailCount: 0, LastSuccessAt: &t0}
		h := applyProbeResult(1, old, fail, now)
		assert.Equal(t, providerapi.HealthDegraded, h.Status)
		require.NotNil(t, h.LastSuccessAt)
		assert.Equal(t, t0, *h.LastSuccessAt, "失败不清 last_success_at")
		assert.EqualValues(t, 0, old.FailCount, "纯函数：不就地修改 old")
		assert.Equal(t, providerapi.HealthUp, old.Status)
	})

	t.Run("latency 与错误信息落列", func(t *testing.T) {
		h := applyProbeResult(9, nil, probeResult{errMsg: "HTTP 503：boom"}, now)
		require.NotNil(t, h.LatencyMs)
		assert.EqualValues(t, 0, *h.LatencyMs)
		assert.Equal(t, "HTTP 503：boom", h.ErrorMessage)
		assert.Equal(t, uint64(9), h.ProviderID)
	})
}

// NewProbeClient 应返回带整请求超时的可用 client（*http.Client 满足 probeClient 窄接口）。
func TestNewProbeClient(t *testing.T) {
	hc := NewProbeClient()
	require.NotNil(t, hc)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()
	res := probe(context.Background(), hc, providerapi.KindOllama, srv.URL, "")
	assert.True(t, res.success)
}

var _ probeClient = (*http.Client)(nil) // 编译期钉住窄接口形态

// ---- 定时探测（runProbeRound / StartProber）----

// newSchedulerService 建带注入探测桩 client 与可配轮询间隔的 providerService（定时探测测试用）。
func newSchedulerService(st *memStore, cm cacheManager, hc probeClient, interval time.Duration) *providerService {
	return &providerService{store: st, cm: cm, master: testMaster, probe: hc, interval: interval}
}

// TestRunProbeRound_OnlyEnabled 单轮只探 enabled=true，disabled 跳过不写 health。
func TestRunProbeRound_OnlyEnabled(t *testing.T) {
	st, _, cm := newTestEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()
	svc := newSchedulerService(st, cm, srv.Client(), time.Minute)

	enabled1 := seedProbeProvider(st, providerapi.KindOpenAI, srv.URL)
	enabled2 := seedProbeProvider(st, providerapi.KindOpenAI, srv.URL)
	disabled := seedProbeProvider(st, providerapi.KindClaude, srv.URL)
	st.providers[disabled].Enabled = false

	svc.runProbeRound(context.Background())

	assert.Equal(t, 2, st.upsertHealthCalls, "只探 2 个 enabled")
	assert.Contains(t, st.healths, enabled1)
	assert.Contains(t, st.healths, enabled2)
	assert.NotContains(t, st.healths, disabled, "disabled 不写 health")
}

// TestRunProbeRound_ProbeUpdatesHealth 单轮探测成功后 provider_health 落库 up。
func TestRunProbeRound_ProbeUpdatesHealth(t *testing.T) {
	st, _, cm := newTestEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o"}]}`))
	}))
	defer srv.Close()
	svc := newSchedulerService(st, cm, srv.Client(), time.Minute)
	id := seedProbeProvider(st, providerapi.KindOpenAI, srv.URL)

	svc.runProbeRound(context.Background())

	h, ok := st.healths[id]
	require.True(t, ok, "探测结果应落 provider_health")
	assert.Equal(t, providerapi.HealthUp, h.Status)
	assert.EqualValues(t, 0, h.FailCount)
	require.NotNil(t, h.LastCheckAt)
}

// TestRunProbeRound_ListError 列表查询失败时单轮安静返回（记日志），不 panic、不写 health。
func TestRunProbeRound_ListError(t *testing.T) {
	st, _, cm := newTestEnv(t)
	svc := newSchedulerService(st, cm, http.DefaultClient, time.Minute)
	st.listProvidersErr = errors.New("db down")

	svc.runProbeRound(context.Background())
	assert.Empty(t, st.healths, "列表失败不应写任何 health")
}

// TestRunProbeRound_DecryptFailure_NoHealthWrite 密文解密失败（主密钥轮换后的旧密文）：
// 不发请求、不动 health，且按 provider 去重只记一次（防每轮刷屏）。
func TestRunProbeRound_DecryptFailure_NoHealthWrite(t *testing.T) {
	st, _, cm := newTestEnv(t)
	svc := newSchedulerService(st, cm, http.DefaultClient, time.Minute)
	bad := st.seed(&Provider{
		Name: "坏密文", Kind: providerapi.KindOpenAI, BaseURL: "http://unused", Enabled: true,
		AuthConfig: map[string]string{apiKeyEncryptedKey: "!!!not-valid-ciphertext!!!"},
	}).ID

	svc.runProbeRound(context.Background())
	svc.runProbeRound(context.Background())

	assert.Equal(t, 0, st.upsertHealthCalls, "解密失败不动 provider_health")
	assert.NotContains(t, st.healths, bad)
	_, dup := svc.decryptFailWarned.Load(bad)
	assert.True(t, dup, "解密失败按 provider 去重只记一次")
}

// TestStartProber_ExitsOnCancel 预取消 ctx：StartProber 立即返回（优雅关闭路径，不依赖真实 ticker）。
func TestStartProber_ExitsOnCancel(t *testing.T) {
	st, _, cm := newTestEnv(t)
	svc := newSchedulerService(st, cm, http.DefaultClient, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() { svc.StartProber(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("StartProber 应在 ctx 已取消时立即返回")
	}
}

// TestStartProber_TicksThenCancel 短间隔触发至少一轮探测后取消：探测被驱动、health 落库、优雅退出。
func TestStartProber_TicksThenCancel(t *testing.T) {
	st, _, cm := newTestEnv(t)
	called := make(chan struct{}, 8) // 每轮每 enabled provider 一次，缓冲避免 handler 阻塞探测
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called <- struct{}{}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()
	svc := newSchedulerService(st, cm, srv.Client(), 10*time.Millisecond)
	id := seedProbeProvider(st, providerapi.KindOpenAI, srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { svc.StartProber(ctx); close(done) }()

	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("StartProber 未在短间隔内触发探测")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("StartProber 未随 ctx 取消退出")
	}

	require.Contains(t, st.healths, id, "探测应写 provider_health")
}
