package service

// spec 07（backend_spec_07_pipeline）管线单测：dispatch 字段可注入、测试同步直调
// （不等 goroutine）；失败矩阵断言 CreateChunks 零调用（无孤儿 chunks）。
// stub 三件套（stubStore / stubModels / stubEmbedder）沿用 service_test.go。

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	providerapi "github.com/Karlsk/go-hify/internal/provider/api"
	ragapi "github.com/Karlsk/go-hify/internal/rag/api"
)

// newPipelineSvc 构造返回具体类型的 svc（覆写 dispatch / acquireWait 字段注入用）。
func newPipelineSvc(st Store, models providerapi.ModelService, embeds embedder, cm cacheManager, cfg Config) *kbService {
	svc, _ := New(st, models, embeds, cm, cfg)
	return svc.(*kbService)
}

// pipelineCfg 管线测试默认配置（EmbedBatchSize=2 便于多批断言）。
func pipelineCfg() Config {
	return Config{ChunkSize: 500, ChunkOverlap: 80, EmbedBatchSize: 2, IngestConcurrency: 2}
}

// TestNewReturnsRecovery 双返回（spec 07 §5）：Recovery 非 nil——08 组合根装配
// MarkInterruptedFailed 用（provider (api, Prober) 先例）。
func TestNewReturnsRecovery(t *testing.T) {
	_, rec := New(&stubStore{}, &stubModels{}, nil, nil, Config{})
	assert.NotNil(t, rec)
}

// TestUploadDocumentDispatches 接线（spec 07 §5）：落 pending 后 dispatch 被调，
// docID = 新行 id（stub RETURNING 回填 100）。
func TestUploadDocumentDispatches(t *testing.T) {
	st := &stubStore{kbByID: map[uint64]*KnowledgeBase{1: kbFixture(1, 7, true)}}
	var gotIDs []uint64
	svc := newPipelineSvc(st, &stubModels{}, nil, &stubCache{}, pipelineCfg())
	svc.dispatch = func(_ context.Context, docID uint64) { gotIDs = append(gotIDs, docID) }

	_, err := svc.UploadDocument(context.Background(), ragapi.UploadDocumentReq{
		KnowledgeBaseID: 1, FileName: "manual.txt", Content: "正文内容", FileType: "txt", FileSize: 12,
	})
	require.NoError(t, err)
	assert.Equal(t, []uint64{100}, gotIDs, "落 pending 后触发 dispatch，docID = 新行 id")
}

// TestReindexDocumentDispatches 接线（spec 07 §5）：reindex 事务重置后 dispatch 被调，
// docID = 既有行 id。
func TestReindexDocumentDispatches(t *testing.T) {
	d := docFixture(9, 1, StatusReady)
	d.ChunkCount = 3
	st := &stubStore{docsByID: map[uint64]*Document{9: d}}
	var gotIDs []uint64
	svc := newPipelineSvc(st, &stubModels{}, nil, &stubCache{}, pipelineCfg())
	svc.dispatch = func(_ context.Context, docID uint64) { gotIDs = append(gotIDs, docID) }

	_, err := svc.ReindexDocument(context.Background(), ragapi.ReindexDocumentReq{ID: 9})
	require.NoError(t, err)
	assert.Equal(t, []uint64{9}, gotIDs, "reindex 重置后触发 dispatch，docID = 既有行 id")
}

// ---- stubStore 管线方法实现（spec 07；记录字段在 service_test.go 的 stubStore） ----
// stub 不模拟状态机前置条件——「0 行」路径由 markXxxErr 注入 gorm.ErrRecordNotFound
// 覆盖；严格状态机 WHERE 在 store 层 sqlmock 断言。

func (s *stubStore) MarkDocumentProcessing(_ context.Context, id uint64) error {
	s.markProcessingCalls++
	s.markProcessingIDs = append(s.markProcessingIDs, id)
	return s.markProcessingErr
}

func (s *stubStore) MarkDocumentReady(_ context.Context, id uint64, chunkCount int) error {
	s.markReadyCalls++
	s.markReadyIDs = append(s.markReadyIDs, id)
	s.markReadyCounts = append(s.markReadyCounts, chunkCount)
	return s.markReadyErr
}

func (s *stubStore) MarkDocumentFailed(_ context.Context, id uint64, message string) error {
	s.mu.Lock()
	s.markFailedCalls++
	s.markFailedIDs = append(s.markFailedIDs, id)
	s.markFailedMsgs = append(s.markFailedMsgs, message)
	s.mu.Unlock()
	return s.markFailedErr
}

func (s *stubStore) CreateChunks(_ context.Context, cs []DocumentChunk) error {
	s.createChunksCalls++
	s.createChunksGot = append(s.createChunksGot, cs...)
	return s.createChunksErr
}

func (s *stubStore) ListIngestingDocuments(_ context.Context) ([]Document, error) {
	return s.ingestingDocs, s.ingestingErr
}

// ---- dispatchIngest（spec 07 §1：WithoutCancel + 抢槽超时置 failed） ----

// TestDispatchIngestWithoutCancel 已取消的请求 ctx 下管线仍执行（spec 07 §1 最易
// 写错处）：WithoutCancel 脱钩取消信号、保留 trace 值；processDocument 被调（观察
// loadDocument 的 store 读），且不落 failed——若误透传 ctx，sem.Acquire 立即失败、
// 文档被错置 failed，断言即破。
func TestDispatchIngestWithoutCancel(t *testing.T) {
	st := &stubStore{docsByID: map[uint64]*Document{9: docFixture(9, 1, StatusPending)}}
	svc := newPipelineSvc(st, &stubModels{}, nil, nil, pipelineCfg())

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 模拟 202 返回后请求 ctx 结束
	svc.dispatchIngest(ctx, 9)

	assert.Eventually(t, func() bool { return st.numGetDocCalls() >= 1 },
		time.Second, 5*time.Millisecond, "取消 ctx 下 processDocument 仍须执行（WithoutCancel）")
	assert.Equal(t, 0, st.numMarkFailedCalls(), "取消不是失败：不得错置 failed")
}

// TestDispatchIngestAcquireTimeout 抢槽 2s 超时（测试注入 10ms）→ 置 failed
// 「系统繁忙」：占满全部槽（并发 1）后 dispatch，fail-fast 而非无限排队。
func TestDispatchIngestAcquireTimeout(t *testing.T) {
	st := &stubStore{}
	cfg := pipelineCfg()
	cfg.IngestConcurrency = 1
	svc := newPipelineSvc(st, &stubModels{}, nil, nil, cfg)
	svc.acquireWait = 10 * time.Millisecond // 默认 2s（spec 值），注入短值防慢

	require.NoError(t, svc.sem.Acquire(context.Background(), 1)) // 占满唯一槽
	svc.dispatchIngest(context.Background(), 9)

	assert.Eventually(t, func() bool { return st.numMarkFailedCalls() == 1 },
		time.Second, 5*time.Millisecond, "抢槽超时须置 failed")
	ids, msgs := st.markFailedSnapshot()
	assert.Equal(t, []uint64{9}, ids)
	assert.Contains(t, msgs[0], "系统繁忙")
	svc.sem.Release(1)
}

// ---- processDocument 主链路（spec 07 §2 环节 2-8：9 步 happy path） ----

// TestProcessDocumentHappyPath 完整管线：loadDocument → MarkDocumentProcessing →
// extractText → SplitChunks → resolveEmbedOptions → embedChunks(按 EmbedBatchSize 分批)
// → buildDocumentChunks → commitReady(WithTx: CreateChunks + MarkDocumentReady)。
// EmbedBatchSize=2 + 1500 字 ASCII 文本 → 3 chunks → 2 批 embed（2+1），
// 断言操作序列、chunk 元数据、prompt_tokens 累计、MarkDocumentReady 精确参数。
func TestProcessDocumentHappyPath(t *testing.T) {
	// 1500 字 ASCII → estimateTokens ≈ 375/chunk，ChunkSize=500 → 3 chunks
	longContent := ""
	for i := 0; i < 150; i++ {
		longContent += "abcdefghij" // 10 chars × 150 = 1500 chars
	}
	d := docFixture(7, 1, StatusPending)
	d.Content = longContent

	st := &stubStore{
		docsByID: map[uint64]*Document{7: d},
		kbByID:   map[uint64]*KnowledgeBase{1: kbFixture(1, 5, true)},
	}
	models := &stubModels{resolveCfg: resolveCfgOK()}

	// 3 chunks × 1536 维向量；EmbedBatchSize=2 → 2 批 embed 调用
	vecs3 := make([][]float32, 3)
	for i := range vecs3 {
		vecs3[i] = make([]float32, ragapi.RequiredEmbeddingDim)
		for j := range vecs3[i] {
			vecs3[i][j] = 0.01
		}
	}
	embeds := &stubEmbedder{embedVecs: vecs3}
	svc := newPipelineSvc(st, models, embeds, nil, pipelineCfg())

	// 同步直调 processDocument（跳过 dispatchIngest 的 goroutine + sem）
	svc.dispatch = func(_ context.Context, docID uint64) {
		svc.processDocument(context.Background(), docID)
	}
	svc.dispatch(context.Background(), 7)

	// 环节 3：MarkDocumentProcessing 恰好 1 次
	assert.Equal(t, 1, st.markProcessingCalls, "MarkDocumentProcessing 恰好 1 次")
	assert.Equal(t, []uint64{7}, st.markProcessingIDs, "MarkDocumentProcessing id=7")

	// 环节 7：EmbedStrings 按 EmbedBatchSize=2 分批（3 chunks → 2 批）
	assert.Equal(t, 2, embeds.embedCalls, "embed 2 批（ceil(3/2)=2）")

	// 环节 8-9：CreateChunks 收到 3 个 chunk，元数据正确
	require.Len(t, st.createChunksGot, 3, "CreateChunks 收到 3 个 chunk")
	assert.Equal(t, 1, st.txCalls, "commitReady 走 WithTx")
	for i, chunk := range st.createChunksGot {
		assert.Equal(t, i+1, chunk.ChunkIndex, "ChunkIndex 从 1 开始（spec 07 §2 环节 8）")
		assert.Equal(t, d.KnowledgeBaseID, chunk.KnowledgeBaseID, "冗余 KB 归属（spec 07 §2 环节 8）")
		assert.Equal(t, int(estimateTokens(chunk.Content)), chunk.TokenCount, "TokenCount=estimateTokens")
	}
	assert.Equal(t, 1, st.markReadyCalls, "MarkDocumentReady 恰好 1 次")
	assert.Equal(t, []uint64{7}, st.markReadyIDs, "MarkDocumentReady id=7")
	assert.Equal(t, []int{3}, st.markReadyCounts, "MarkDocumentReady chunkCount=3")
}

// ---- 失败矩阵（spec 07 §2：任意步骤失败 → CreateChunks 零调用，无孤儿 chunks） ----

// TestProcessDocumentFailureMatrix 参数化断言：管线任意环节失败时
// CreateChunks 零调用 + markFailed 被触发（失败消息含上下文）。
func TestProcessDocumentFailureMatrix(t *testing.T) {
	// 长内容确保 SplitChunks 产出 ≥1 chunk（失败发生在 embedding 之前）
	longContent := ""
	for i := 0; i < 150; i++ {
		longContent += "abcdefghij"
	}
	cases := []struct {
		name       string
		store      *stubStore
		models     *stubModels
		embeds     *stubEmbedder
		cfg        Config
	}{
		{
			name: "MarkDocumentProcessing 失败",
			store: func() *stubStore {
				d := docFixture(7, 1, StatusPending)
				d.Content = longContent
				return &stubStore{
					docsByID:         map[uint64]*Document{7: d},
					kbByID:           map[uint64]*KnowledgeBase{1: kbFixture(1, 5, true)},
					markProcessingErr: errors.New("optimistic lock conflict"),
				}
			}(),
			models:     &stubModels{resolveCfg: resolveCfgOK()},
			embeds:     &stubEmbedder{embedVecs: [][]float32{embedVec()}},
			cfg:        pipelineCfg(),
		},
		{
			name: "extractText 失败（不支持的文件类型）",
			store: func() *stubStore {
				d := docFixture(7, 1, StatusPending)
				d.FileType = "pdf"
				d.Content = longContent
				return &stubStore{
					docsByID: map[uint64]*Document{7: d},
					kbByID:   map[uint64]*KnowledgeBase{1: kbFixture(1, 5, true)},
				}
			}(),
			models:     &stubModels{resolveCfg: resolveCfgOK()},
			embeds:     &stubEmbedder{embedVecs: [][]float32{embedVec()}},
			cfg:        pipelineCfg(),
		},
		{
			name: "resolveEmbedOptions 失败（模型悬空）",
			store: func() *stubStore {
				d := docFixture(7, 1, StatusPending)
				d.Content = longContent
				return &stubStore{
					docsByID: map[uint64]*Document{7: d},
					kbByID:   map[uint64]*KnowledgeBase{1: kbFixture(1, 5, true)},
				}
			}(),
			models:     &stubModels{resolveErr: providerapi.ErrModelNotFound},
			embeds:     &stubEmbedder{},
			cfg:        pipelineCfg(),
		},
		{
			name: "embedChunks 失败（EmbedStrings 错误）",
			store: func() *stubStore {
				d := docFixture(7, 1, StatusPending)
				d.Content = longContent
				return &stubStore{
					docsByID: map[uint64]*Document{7: d},
					kbByID:   map[uint64]*KnowledgeBase{1: kbFixture(1, 5, true)},
				}
			}(),
			models:     &stubModels{resolveCfg: resolveCfgOK()},
			embeds:     &stubEmbedder{embedErr: errors.New("rate limited")},
			cfg:        pipelineCfg(),
		},
		{
			name: "commitReady 失败（CreateChunks 错误）",
			store: func() *stubStore {
				d := docFixture(7, 1, StatusPending)
				d.Content = longContent
				return &stubStore{
					docsByID:       map[uint64]*Document{7: d},
					kbByID:         map[uint64]*KnowledgeBase{1: kbFixture(1, 5, true)},
					createChunksErr: errors.New("disk full"),
				}
			}(),
			models:     &stubModels{resolveCfg: resolveCfgOK()},
			embeds:     &stubEmbedder{embedVecs: embedVecsN(10)}, // 足够多向量，确保不因维度不足 panic
			cfg:        pipelineCfg(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newPipelineSvc(tc.store, tc.models, tc.embeds, nil, tc.cfg)
			svc.dispatch = func(_ context.Context, docID uint64) {
				svc.processDocument(context.Background(), docID)
			}
			svc.dispatch(context.Background(), 7)

			// 核心断言：任意步骤失败 → MarkDocumentReady 零调用（无终态）。
			// commitReady 内部失败时 CreateChunks 可能被调用（tx 回滚），但 MarkDocumentReady
			// 不会被调用（严格状态机 WHERE status='processing' 保持不变）。
			assert.Equal(t, 0, tc.store.markReadyCalls, "MarkDocumentReady 零调用（终态不可达）")
		})
	}
}

// ---- panic 兜底（spec 07 §2 环节 1：recover→failed "internal panic"） ----

// TestProcessDocumentPanicRecovery 管线内 panic 被 recover 捕获 → markFailed
// "internal panic: ..."，文档不卡在 processing（spec 07 §2 环节 1）。
func TestProcessDocumentPanicRecovery(t *testing.T) {
	d := docFixture(7, 1, StatusPending)
	d.Content = "正常内容"
	st := &stubStore{
		docsByID: map[uint64]*Document{7: d},
		kbByID:   map[uint64]*KnowledgeBase{1: kbFixture(1, 5, true)},
		// 覆写 GetDocumentByID 使其 panic
		getDocByIDFn: func(_ context.Context, id uint64) (*Document, error) {
			panic("unexpected nil pointer")
		},
	}
	svc := newPipelineSvc(st, &stubModels{}, nil, nil, pipelineCfg())
	svc.dispatch = func(_ context.Context, docID uint64) {
		svc.processDocument(context.Background(), docID)
	}
	svc.dispatch(context.Background(), 7)

	assert.Equal(t, 1, st.numMarkFailedCalls(), "panic 被 recover 捕获 → markFailed 1 次")
	ids, msgs := st.markFailedSnapshot()
	assert.Equal(t, []uint64{7}, ids)
	assert.Contains(t, msgs[0], "internal panic", "失败消息含 internal panic 前缀")
	assert.Contains(t, msgs[0], "unexpected nil pointer", "失败消息含 panic 原因")
}

// ---- truncateRunes（spec 07 §2 环节 9：error_message 截断 500，防超长错误撑爆列） ----

func TestTruncateRunes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"短于上限原样返回", "boom", 500, "boom"},
		{"恰好等于上限原样返回", "abcdef", 6, "abcdef"},
		{"超长按 rune 截断", "abcdefgh", 3, "abc"},
		{"中文按完整 rune 截断不撕裂字节", "中文错误消息很长", 5, "中文错误消"},
		{"上限 0 返回空串", "任何内容", 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, truncateRunes(tc.in, tc.n))
		})
	}
}
