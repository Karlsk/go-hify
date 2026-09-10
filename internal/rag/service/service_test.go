package service

// spec 04（backend_spec_04_crud）service 层单测：stubStore（内嵌 Store 接口、只覆写
// 路径需要的方法）+ stub models（provider api 契约）——零真实 DB / 网络；业务翻译
// （哨兵 / 23505 / 23503）在此层覆盖，SQL 形态在 store 层 sqlmock 覆盖。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/Karlsk/go-hify/internal/platform/errs"
	"github.com/Karlsk/go-hify/internal/platform/llm"
	"github.com/Karlsk/go-hify/internal/platform/page"
	providerapi "github.com/Karlsk/go-hify/internal/provider/api"
	ragapi "github.com/Karlsk/go-hify/internal/rag/api"
)

// ---- 测试基建：stub 三件套 ----

// stubStore 内嵌 Store 接口：未覆写的方法被调用即 panic（nil 接口），测试只覆写
// 当前路径需要的方法——覆写清单即该路径的真实依赖面。
type stubStore struct {
	Store

	mu sync.Mutex // 保护记录字段的并发读写（pipeline goroutine 写，test goroutine 读）

	createErr error          // CreateKnowledgeBase 注入错误（23505 / 23503 / 普通错误）
	nextID    uint64         // 模拟 DB RETURNING 回填主键
	created   *KnowledgeBase // 落库实体快照

	// KB 读 / 写路径
	kbByID     map[uint64]*KnowledgeBase // GetKnowledgeBaseByID 数据集（无键 = NotFound）
	getKBCalls int                       // 命中计数（缓存命中断言 store 零调用）
	updateErr  error                     // UpdateKnowledgeBase 注入错误（23505 / 普通）
	updated    *KnowledgeBase            // Save 后实体快照
	deleteErr  error                     // DeleteKnowledgeBase 注入错误（NotFound / 普通）
	deletedID  uint64
	deleteDone bool

	// 计数
	allDocsByKB map[uint64]int64 // CountAllDocumentsByKB（含软删护栏判据）
	docsByKBIDs map[uint64]int64 // CountDocumentsByKBIDs（活跃）
	countIDsGot []uint64         // 查收批量计数的 id 集合

	// 列表
	listKBs  page.OffsetResult[KnowledgeBase]
	listName string // 查收 name 过滤参数（透传断言）

	// 文档路径
	docsByID       map[uint64]*Document // GetDocumentByID 数据集（无键 = NotFound）
	getDocByIDFn   func(ctx context.Context, id uint64) (*Document, error) // 覆写 GetDocumentByID 行为（panic 注入等）
	getDocCalls    int
	createDocErr   error     // CreateDocument 注入错误（23503 / 普通）
	createdDoc     *Document // 落库实体快照
	softDelErr     error     // SoftDeleteDocument 注入错误（NotFound / 普通）
	softDelID      uint64
	softDelDone    bool
	listDocs       []Document // ListDocumentsByKB 固定返回集
	listDocsKB     uint64     // 查收三元组（kb / before / limit）
	listDocsBefore uint64
	listDocsLimit  int
	delChunksErr   error
	resetErr       error
	ops            []string // 操作序列日志（事务顺序断言："delChunks:9" / "reset:9"）
	txCalls        int

	// 检索（spec 05）
	searchHits   []ChunkHit        // SearchChunks 返回集
	searchErr    error             // SearchChunks 注入错误
	searchGotKBs []uint64          // 查收 kbIDs（disabled 剔除断言）
	searchGotQ   []float32         // 查收查询向量
	searchGotLim int               // 查收 limit（TopK 默认 / clamp 断言）
	searchGotEF  int               // 查收 efSearch（cfg 透传断言）
	docMetas     map[uint64]string // GetDocumentMetasByIDs 返回集（无键 = 悬空 ""）
	docMetasErr  error
	docMetasGot  []uint64 // 查收 ids

	// 管线（spec 07）：状态翻转 / chunks 写入 / Recovery 扫描的调用记录。
	markProcessingCalls int      // MarkDocumentProcessing 命中计数
	markProcessingIDs   []uint64 // 查收 id（翻转时机断言）
	markProcessingErr   error    // 注入错误（0 行 ErrRecordNotFound 等）
	markReadyCalls      int
	markReadyIDs        []uint64 // MarkDocumentReady 查收（id, chunkCount）原子对
	markReadyCounts     []int
	markReadyErr        error
	markFailedCalls     int      // MarkDocumentFailed 命中计数
	markFailedIDs       []uint64 // 查收 id
	markFailedMsgs      []string // 查收 message（失败矩阵断言）
	markFailedErr       error
	createChunksCalls   int             // CreateChunks 批次计数（逐批 ≤ EmbedBatchSize）
	createChunksGot     []DocumentChunk // 全量收到的 chunks（内容 / 冗余列断言）
	createChunksErr     error           // 注入错误（事务回滚路径）
	ingestingDocs       []Document      // ListIngestingDocuments 返回集（Recovery 用）
	ingestingErr        error
}

func (s *stubStore) CreateKnowledgeBase(_ context.Context, kb *KnowledgeBase) error {
	if s.createErr != nil {
		return s.createErr
	}
	s.nextID++
	kb.ID = s.nextID
	now := time.Now()
	kb.CreatedAt = now
	kb.UpdatedAt = now
	s.created = kb
	return nil
}

func (s *stubStore) GetKnowledgeBaseByID(_ context.Context, id uint64) (*KnowledgeBase, error) {
	s.getKBCalls++
	if kb, ok := s.kbByID[id]; ok {
		cp := *kb // 返回副本，模拟 DB 读隔离（service 侧改动不回写数据集）
		return &cp, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (s *stubStore) UpdateKnowledgeBase(_ context.Context, kb *KnowledgeBase) error {
	if s.updateErr != nil {
		return s.updateErr
	}
	kb.UpdatedAt = time.Now() // 模拟 DB autoUpdateTime
	s.updated = kb
	return nil
}

func (s *stubStore) DeleteKnowledgeBase(_ context.Context, id uint64) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	s.deletedID = id
	s.deleteDone = true
	return nil
}

func (s *stubStore) CountAllDocumentsByKB(_ context.Context, kbID uint64) (int64, error) {
	return s.allDocsByKB[kbID], nil
}

func (s *stubStore) CountDocumentsByKBIDs(_ context.Context, ids []uint64) (map[uint64]int64, error) {
	s.countIDsGot = ids
	out := make(map[uint64]int64, len(ids))
	for _, id := range ids {
		out[id] = s.docsByKBIDs[id]
	}
	return out, nil
}

func (s *stubStore) ListKnowledgeBases(_ context.Context, _ page.OffsetParams, name string) (page.OffsetResult[KnowledgeBase], error) {
	s.listName = name
	return s.listKBs, nil
}

func (s *stubStore) GetDocumentByID(ctx context.Context, id uint64) (*Document, error) {
	s.mu.Lock()
	s.getDocCalls++
	s.mu.Unlock()
	if s.getDocByIDFn != nil {
		return s.getDocByIDFn(ctx, id)
	}
	if d, ok := s.docsByID[id]; ok {
		cp := *d // 副本模拟 DB 读隔离
		return &cp, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (s *stubStore) CreateDocument(_ context.Context, d *Document) error {
	if s.createDocErr != nil {
		return s.createDocErr
	}
	d.ID = 100 // 模拟 RETURNING 回填
	now := time.Now()
	d.CreatedAt = now
	d.UpdatedAt = now
	s.createdDoc = d
	return nil
}

func (s *stubStore) SoftDeleteDocument(_ context.Context, id uint64) error {
	if s.softDelErr != nil {
		return s.softDelErr
	}
	if _, ok := s.docsByID[id]; !ok {
		return gorm.ErrRecordNotFound
	}
	s.softDelID = id
	s.softDelDone = true
	s.ops = append(s.ops, fmt.Sprintf("softDel:%d", id))
	return nil
}

func (s *stubStore) ListDocumentsByKB(_ context.Context, kbID, beforeID uint64, limit int) ([]Document, error) {
	s.listDocsKB, s.listDocsBefore, s.listDocsLimit = kbID, beforeID, limit
	return s.listDocs, nil
}

func (s *stubStore) DeleteChunksByDocument(_ context.Context, documentID uint64) error {
	if s.delChunksErr != nil {
		return s.delChunksErr
	}
	s.ops = append(s.ops, fmt.Sprintf("delChunks:%d", documentID))
	return nil
}

func (s *stubStore) ResetDocumentForReindex(_ context.Context, id uint64) error {
	if s.resetErr != nil {
		return s.resetErr
	}
	if d, ok := s.docsByID[id]; ok {
		d.Status, d.ErrorMessage, d.ChunkCount = StatusPending, "", 0 // 模拟 UPDATE 落库
	}
	s.ops = append(s.ops, fmt.Sprintf("reset:%d", id))
	return nil
}

func (s *stubStore) WithTx(_ context.Context, fn func(tx Store) error) error {
	s.txCalls++
	return fn(s) // tx 即同一 stub：序列经 ops 记录
}

// ---- 管线记录字段线程安全读（spec 07：dispatchIngest goroutine 写，test goroutine 读） ----

func (s *stubStore) numGetDocCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getDocCalls
}

func (s *stubStore) numMarkFailedCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.markFailedCalls
}

func (s *stubStore) markFailedSnapshot() (ids []uint64, msgs []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids = append(ids, s.markFailedIDs...)
	msgs = append(msgs, s.markFailedMsgs...)
	return ids, msgs
}

// stubModels 内嵌 providerapi.ModelService，按需覆写（Get 预检 / ListByIDs 聚合 /
// ResolveLLMConfig 检索向量化前置——spec 05）。
type stubModels struct {
	providerapi.ModelService
	model   *providerapi.ModelSchema
	err     error
	byIDs   []providerapi.ModelSchema // ListByIDs 返回集
	listIDs []providerapi.ListModelsByIDsReq
	listErr error

	resolveCfg *providerapi.LLMConfig          // ResolveLLMConfig 返回集
	resolveErr error                           // 注入错误（ModelNotFound 等）
	resolveGot providerapi.ResolveLLMConfigReq // 查收请求（KB 模型 id 断言）
}

func (s *stubModels) Get(_ context.Context, _ providerapi.GetModelReq) (*providerapi.ModelSchema, error) {
	return s.model, s.err
}

func (s *stubModels) ListByIDs(_ context.Context, req providerapi.ListModelsByIDsReq) ([]providerapi.ModelSchema, error) {
	s.listIDs = append(s.listIDs, req)
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.byIDs, nil
}

// ResolveLLMConfig spec 05 Retrieve 的向量化前置（预检不走此路——Get 已单独覆写）。
func (s *stubModels) ResolveLLMConfig(_ context.Context, req providerapi.ResolveLLMConfigReq) (*providerapi.LLMConfig, error) {
	s.resolveGot = req
	if s.resolveErr != nil {
		return nil, s.resolveErr
	}
	return s.resolveCfg, nil
}

// stubCache 实现 cacheManager：JSON 往返模拟真实缓存序列化（载荷形状漂移即暴露）。
type stubCache struct {
	getErr  error
	setErr  error
	delErr  error
	seed    map[string]any // Get 命中数据（key → 载荷）
	stored  map[string]any // Set 收到的载荷
	deleted []string       // Delete 收到的 key
}

func (s *stubCache) Get(_ context.Context, _, key string, dst any) (bool, error) {
	if s.getErr != nil {
		return false, s.getErr
	}
	v, ok := s.seed[key]
	if !ok {
		return false, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(b, dst)
}

func (s *stubCache) Set(_ context.Context, _, key string, val any) error {
	if s.setErr != nil {
		return s.setErr
	}
	if s.stored == nil {
		s.stored = map[string]any{}
	}
	s.stored[key] = val
	return nil
}

func (s *stubCache) Delete(_ context.Context, _, key string) error {
	if s.delErr != nil {
		return s.delErr
	}
	s.deleted = append(s.deleted, key)
	return nil
}

// pgErr 构造 PG 错误码注入（23505 唯一冲突 / 23503 外键违例）。
func pgErr(code string) error { return &pgconn.PgError{Code: code} }

// newSvc 构造被测服务（无缓存路径：Create 等不触缓存的用例）；embeds 本篇仅持有
// 不调用（spec 04 §1），传 nil。
func newSvc(st Store, models providerapi.ModelService) ragapi.KnowledgeBaseService {
	svc, _ := New(st, models, nil, nil, Config{})
	return svc
}

// newSvcWithCache 带缓存构造（Cache-Aside / evict 用例）。
func newSvcWithCache(st Store, models providerapi.ModelService, cm cacheManager) ragapi.KnowledgeBaseService {
	svc, _ := New(st, models, nil, cm, Config{})
	return svc
}

// dimPtr 测试辅助（*int32 构造）。
func dimPtr(d int32) *int32 { return &d }

// embedModel 构造模型快照（capability / dim / enabled 可变体）；id 固定 "5"。
func embedModel(capability string, dim *int32, enabled bool) *providerapi.ModelSchema {
	m := &providerapi.ModelSchema{
		Name:         "text-embedding-x",
		Capability:   capability,
		EmbeddingDim: dim,
		Enabled:      enabled,
	}
	m.ID = "5"
	return m
}

// ---- Create（spec 04 §1：模型预检 + 落库 + 错误翻译） ----

// 预检分支：非 embedding / dim≠1536 → ErrEmbeddingDimMismatch；不存在 / 停用 →
// provider 哨兵透传；能力预检先于 enabled（spec 端点 1 错误列表顺序）。
func TestCreateKnowledgeBasePrecheck(t *testing.T) {
	cases := []struct {
		name    string
		model   *providerapi.ModelSchema
		getErr  error
		wantErr error
	}{
		{"非 embedding 能力", embedModel(providerapi.CapabilityChat, dimPtr(1536), true), nil, ragapi.ErrEmbeddingDimMismatch},
		{"维度为 nil", embedModel(providerapi.CapabilityEmbedding, nil, true), nil, ragapi.ErrEmbeddingDimMismatch},
		{"维度不等于 1536", embedModel(providerapi.CapabilityEmbedding, dimPtr(3072), true), nil, ragapi.ErrEmbeddingDimMismatch},
		{"模型不存在透传", nil, providerapi.ErrModelNotFound, providerapi.ErrModelNotFound},
		{"模型停用透传", embedModel(providerapi.CapabilityEmbedding, dimPtr(1536), false), nil, providerapi.ErrModelDisabled},
		{"能力预检先于 enabled", embedModel(providerapi.CapabilityChat, dimPtr(1536), false), nil, ragapi.ErrEmbeddingDimMismatch},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := newSvc(&stubStore{}, &stubModels{model: c.model, err: c.getErr})
			_, err := svc.Create(context.Background(), ragapi.CreateKnowledgeBaseReq{
				Name:             "产品手册",
				EmbeddingModelID: 5,
			})
			require.Error(t, err)
			assert.ErrorIs(t, err, c.wantErr)
		})
	}
}

// 成功路径：预检字段原样落库；Enabled 显式置 true；返回快照 id / 外键字符串化。
func TestCreateKnowledgeBase(t *testing.T) {
	st := &stubStore{}
	svc := newSvc(st, &stubModels{model: embedModel(providerapi.CapabilityEmbedding, dimPtr(1536), true)})

	got, err := svc.Create(context.Background(), ragapi.CreateKnowledgeBaseReq{
		Name:             "产品手册",
		Description:      "内部文档",
		EmbeddingModelID: 5,
	})
	require.NoError(t, err)

	// 落库实体：model 无 gorm default tag，创建路径显式设值（provider 踩坑 #1）
	assert.Equal(t, "产品手册", st.created.Name)
	assert.Equal(t, "内部文档", st.created.Description)
	assert.Equal(t, uint64(5), st.created.EmbeddingModelID)
	assert.True(t, st.created.Enabled, "新建 KB 显式置 true")

	// 返回快照：DocumentCount 创建时为 0（spec 03 §2）
	assert.Equal(t, "1", got.ID)
	assert.Equal(t, "产品手册", got.Name)
	assert.Equal(t, "5", got.EmbeddingModelID)
	assert.True(t, got.Enabled)
	assert.Zero(t, got.DocumentCount)
	assert.False(t, got.CreatedAt.IsZero())
}

// 插入期错误翻译：23505 → NameConflict；23503（预检后模型被并发删除）→
// ErrModelNotFound；非哨兵错误 %w 包装上抛。
func TestCreateKnowledgeBaseErrorTranslation(t *testing.T) {
	models := &stubModels{model: embedModel(providerapi.CapabilityEmbedding, dimPtr(1536), true)}
	req := ragapi.CreateKnowledgeBaseReq{Name: "x", EmbeddingModelID: 5}

	svc := newSvc(&stubStore{createErr: pgErr("23505")}, models)
	_, err := svc.Create(context.Background(), req)
	assert.ErrorIs(t, err, ragapi.ErrKnowledgeBaseNameConflict)

	svc = newSvc(&stubStore{createErr: pgErr("23503")}, models)
	_, err = svc.Create(context.Background(), req)
	assert.ErrorIs(t, err, providerapi.ErrModelNotFound)

	boom := errors.New("db down")
	svc = newSvc(&stubStore{createErr: boom}, models)
	_, err = svc.Create(context.Background(), req)
	assert.ErrorIs(t, err, boom)

	// models.Get 普通错误同样包装上抛（不吞、不误译）
	svc = newSvc(&stubStore{}, &stubModels{err: boom})
	_, err = svc.Create(context.Background(), req)
	assert.ErrorIs(t, err, boom)
	assert.NotErrorIs(t, err, ragapi.ErrEmbeddingDimMismatch)
}

// ---- Get / Update / List / Delete（spec 04 §1：Cache-Aside / evict / 聚合 / 挡删） ----

// kbFixture 构造 KB 实体（Get / Update / List 用例的数据集基线）。
func kbFixture(id, modelID uint64, enabled bool) *KnowledgeBase {
	kb := &KnowledgeBase{
		Name:             "产品手册",
		Description:      "内部文档",
		EmbeddingModelID: modelID,
		Enabled:          enabled,
	}
	kb.ID = id
	return kb
}

// 缓存命中：直接返回缓存载荷（含 DocumentCount），store 零调用。
func TestGetKnowledgeBaseCacheHit(t *testing.T) {
	st := &stubStore{}
	cached := ragapi.KnowledgeBaseSchema{Name: "缓存里的库", DocumentCount: 7}
	cached.ID = "1"
	cm := &stubCache{seed: map[string]any{"detail:1": cached}}
	svc := newSvcWithCache(st, &stubModels{}, cm)

	got, err := svc.Get(context.Background(), ragapi.GetKnowledgeBaseReq{ID: 1})
	require.NoError(t, err)
	assert.Equal(t, "缓存里的库", got.Name)
	assert.Equal(t, int64(7), got.DocumentCount)
	assert.Zero(t, st.getKBCalls, "缓存命中不触库")
}

// 缓存 miss：查库 → 计数现读 → Set 回填 → 返回。
func TestGetKnowledgeBaseCacheMiss(t *testing.T) {
	st := &stubStore{
		kbByID:      map[uint64]*KnowledgeBase{1: kbFixture(1, 5, true)},
		docsByKBIDs: map[uint64]int64{1: 3},
	}
	cm := &stubCache{seed: map[string]any{}}
	svc := newSvcWithCache(st, &stubModels{}, cm)

	got, err := svc.Get(context.Background(), ragapi.GetKnowledgeBaseReq{ID: 1})
	require.NoError(t, err)
	assert.Equal(t, "产品手册", got.Name)
	assert.Equal(t, "5", got.EmbeddingModelID)
	assert.Equal(t, int64(3), got.DocumentCount, "miss 后 DocumentCount 现读")
	assert.Equal(t, 1, st.getKBCalls)
	assert.Contains(t, cm.stored, "detail:1", "miss 回填缓存")
}

// 不存在：miss → NotFound 哨兵；无回填。
func TestGetKnowledgeBaseNotFound(t *testing.T) {
	cm := &stubCache{seed: map[string]any{}}
	svc := newSvcWithCache(&stubStore{}, &stubModels{}, cm)

	_, err := svc.Get(context.Background(), ragapi.GetKnowledgeBaseReq{ID: 999})
	assert.ErrorIs(t, err, ragapi.ErrKnowledgeBaseNotFound)
	assert.Empty(t, cm.stored, "失败路径不回填")
}

// 缓存故障降级：Get 出错 → WARN 后照常查库；Set 失败 → 仅 WARN 仍成功返回。
func TestGetKnowledgeBaseCacheDegradation(t *testing.T) {
	st := &stubStore{
		kbByID:      map[uint64]*KnowledgeBase{1: kbFixture(1, 5, true)},
		docsByKBIDs: map[uint64]int64{1: 1},
	}
	cm := &stubCache{getErr: errors.New("redis down"), setErr: errors.New("redis down"), seed: map[string]any{}}
	svc := newSvcWithCache(st, &stubModels{}, cm)

	got, err := svc.Get(context.Background(), ragapi.GetKnowledgeBaseReq{ID: 1})
	require.NoError(t, err, "缓存故障不是业务失败")
	assert.Equal(t, "产品手册", got.Name)
	assert.Equal(t, 1, st.getKBCalls, "降级查库")
	assert.Empty(t, cm.stored, "Set 失败不落载荷")
}

// 成功更新：name/desc 覆盖、enabled 翻转、EmbeddingModelID 保持、写时 evict、
// 返回快照含现读计数。
func TestUpdateKnowledgeBase(t *testing.T) {
	st := &stubStore{
		kbByID:      map[uint64]*KnowledgeBase{1: kbFixture(1, 5, true)},
		docsByKBIDs: map[uint64]int64{1: 2},
	}
	cm := &stubCache{}
	svc := newSvcWithCache(st, &stubModels{}, cm)

	disabled := false
	got, err := svc.Update(context.Background(), ragapi.UpdateKnowledgeBaseReq{
		ID:          1,
		Name:        "新名称",
		Description: "新描述",
		Enabled:     &disabled,
	})
	require.NoError(t, err)

	assert.Equal(t, "新名称", st.updated.Name)
	assert.Equal(t, "新描述", st.updated.Description)
	assert.False(t, st.updated.Enabled)
	assert.Equal(t, uint64(5), st.updated.EmbeddingModelID, "embedding_model_id 不可改（维度冻结）")
	assert.Equal(t, []string{"detail:1"}, cm.deleted, "事务提交后写时 evict")
	assert.Equal(t, "新名称", got.Name)
	assert.False(t, got.Enabled)
	assert.Equal(t, int64(2), got.DocumentCount)
}

// enabled 三态：nil = 不变（保持原值）。
func TestUpdateKnowledgeBaseEnabledNil(t *testing.T) {
	st := &stubStore{
		kbByID:      map[uint64]*KnowledgeBase{1: kbFixture(1, 5, true)},
		docsByKBIDs: map[uint64]int64{1: 0},
	}
	svc := newSvcWithCache(st, &stubModels{}, &stubCache{})

	got, err := svc.Update(context.Background(), ragapi.UpdateKnowledgeBaseReq{ID: 1, Name: "仅改名"})
	require.NoError(t, err)
	assert.True(t, st.updated.Enabled, "nil = enabled 不变")
	assert.True(t, got.Enabled)
}

// 更新错误：404 哨兵、23505 → NameConflict、普通错误包装；错误路径不 evict。
func TestUpdateKnowledgeBaseErrors(t *testing.T) {
	svc := newSvcWithCache(&stubStore{}, &stubModels{}, &stubCache{})
	_, err := svc.Update(context.Background(), ragapi.UpdateKnowledgeBaseReq{ID: 999, Name: "x"})
	assert.ErrorIs(t, err, ragapi.ErrKnowledgeBaseNotFound)

	cm := &stubCache{}
	svc = newSvcWithCache(
		&stubStore{kbByID: map[uint64]*KnowledgeBase{1: kbFixture(1, 5, true)}, updateErr: pgErr("23505")},
		&stubModels{}, cm)
	_, err = svc.Update(context.Background(), ragapi.UpdateKnowledgeBaseReq{ID: 1, Name: "重名"})
	assert.ErrorIs(t, err, ragapi.ErrKnowledgeBaseNameConflict)

	boom := errors.New("save failed")
	svc = newSvcWithCache(
		&stubStore{kbByID: map[uint64]*KnowledgeBase{1: kbFixture(1, 5, true)}, updateErr: boom},
		&stubModels{}, cm)
	_, err = svc.Update(context.Background(), ragapi.UpdateKnowledgeBaseReq{ID: 1, Name: "x"})
	assert.ErrorIs(t, err, boom)
	assert.Empty(t, cm.deleted, "错误路径不 evict")
}

// 列表：偏移分页 + 模型名聚合（悬空 ""）+ 批量计数（防 N+1）；name 参数原样透传。
func TestListKnowledgeBases(t *testing.T) {
	kb1, kb2 := kbFixture(1, 5, true), kbFixture(2, 9, false)
	st := &stubStore{
		listKBs:     page.OffsetResult[KnowledgeBase]{Items: []KnowledgeBase{*kb1, *kb2}, Page: 1, PageSize: 20, Total: 2},
		docsByKBIDs: map[uint64]int64{1: 3},
	}
	m := providerapi.ModelSchema{Name: "text-embedding-x"}
	m.ID = "5"
	models := &stubModels{byIDs: []providerapi.ModelSchema{m}}
	svc := newSvcWithCache(st, models, &stubCache{})

	got, err := svc.List(context.Background(), ragapi.ListKnowledgeBasesReq{Name: "产品"})
	require.NoError(t, err)

	assert.Equal(t, "产品", st.listName, "name 参数原样透传 store")
	require.Len(t, got.Items, 2)
	assert.Equal(t, "text-embedding-x", got.Items[0].EmbeddingModelName)
	assert.Equal(t, "", got.Items[1].EmbeddingModelName, "悬空模型名为空串（前端 fallback 显示 id）")
	assert.Equal(t, int64(3), got.Items[0].DocumentCount)
	assert.Equal(t, int64(0), got.Items[1].DocumentCount, "计数 map 无键 = 0")
	assert.Equal(t, 1, got.Page)
	assert.Equal(t, 20, got.PageSize)
	assert.Equal(t, int64(2), got.Total)

	// 聚合批量：ListByIDs 恰一次、ids 去重；计数一次带走当页全部 KB
	require.Len(t, models.listIDs, 1)
	assert.Equal(t, []uint64{5, 9}, models.listIDs[0].IDs)
	assert.Equal(t, []uint64{1, 2}, st.countIDsGot)
}

// 空页：Items 非 nil（前端免空判断）、不发 ListByIDs。
func TestListKnowledgeBasesEmpty(t *testing.T) {
	st := &stubStore{listKBs: page.OffsetResult[KnowledgeBase]{Page: 1, PageSize: 20}}
	models := &stubModels{}
	svc := newSvcWithCache(st, models, &stubCache{})

	got, err := svc.List(context.Background(), ragapi.ListKnowledgeBasesReq{})
	require.NoError(t, err)
	assert.NotNil(t, got.Items, "空页返 [] 不返 null")
	assert.Empty(t, got.Items)
	assert.Empty(t, models.listIDs, "空页不发 ListByIDs")
}

// 挡删：有文档（含软删，Unscoped 计数）→ InUse，不触删除。
func TestDeleteKnowledgeBaseInUse(t *testing.T) {
	st := &stubStore{allDocsByKB: map[uint64]int64{1: 2}}
	svc := newSvcWithCache(st, &stubModels{}, &stubCache{})

	err := svc.Delete(context.Background(), ragapi.DeleteKnowledgeBaseReq{ID: 1})
	assert.ErrorIs(t, err, ragapi.ErrKnowledgeBaseInUse)
	assert.False(t, st.deleteDone, "挡删时不触删除")
}

// 删除：成功 + evict；并发已删（RowsAffected=0）→ 哨兵翻译。
func TestDeleteKnowledgeBase(t *testing.T) {
	st := &stubStore{allDocsByKB: map[uint64]int64{1: 0}}
	cm := &stubCache{}
	svc := newSvcWithCache(st, &stubModels{}, cm)

	err := svc.Delete(context.Background(), ragapi.DeleteKnowledgeBaseReq{ID: 1})
	require.NoError(t, err)
	assert.True(t, st.deleteDone)
	assert.Equal(t, uint64(1), st.deletedID)
	assert.Equal(t, []string{"detail:1"}, cm.deleted, "删除后失效缓存（防幽灵读）")

	svc = newSvcWithCache(
		&stubStore{allDocsByKB: map[uint64]int64{1: 0}, deleteErr: gorm.ErrRecordNotFound},
		&stubModels{}, &stubCache{})
	err = svc.Delete(context.Background(), ragapi.DeleteKnowledgeBaseReq{ID: 1})
	assert.ErrorIs(t, err, ragapi.ErrKnowledgeBaseNotFound)
}

// ---- 文档五方法（spec 04 §1：pending 快照 / 规则 2 / 规则 3 / 游标组装） ----

// docFixture 构造文档实体基线。
func docFixture(id, kbID uint64, status string) *Document {
	d := &Document{
		KnowledgeBaseID: kbID,
		Name:            "manual.txt",
		Content:         "正文内容",
		Status:          status,
		FileType:        "txt",
		FileSize:        24,
	}
	d.ID = id
	return d
}

// validUploadReq 合法上传请求基线（handler 已归一化；service 侧 Validate 幂等重跑）。
func validUploadReq(kbID uint64) ragapi.UploadDocumentReq {
	return ragapi.UploadDocumentReq{
		KnowledgeBaseID: kbID,
		FileName:        "manual.txt",
		Name:            "使用手册",
		Content:         "正文内容",
		FileType:        "txt",
		FileSize:        12,
	}
}

// KB 不存在 → 哨兵；不落库。
func TestUploadDocumentKBNotFound(t *testing.T) {
	svc := newSvcWithCache(&stubStore{}, &stubModels{}, &stubCache{})

	_, err := svc.UploadDocument(context.Background(), validUploadReq(1))
	assert.ErrorIs(t, err, ragapi.ErrKnowledgeBaseNotFound)
}

// 上传成功：pending + 元数据 + 原文落库；快照回 202 载荷；evict KB detail key。
// 本篇无 dispatch 接线（07）——无事务 / 无异步调用可断言（ops / txCalls 恒空）。
func TestUploadDocument(t *testing.T) {
	st := &stubStore{kbByID: map[uint64]*KnowledgeBase{1: kbFixture(1, 5, true)}}
	cm := &stubCache{}
	svc := newSvcWithCache(st, &stubModels{}, cm)

	got, err := svc.UploadDocument(context.Background(), validUploadReq(1))
	require.NoError(t, err)

	assert.Equal(t, StatusPending, st.createdDoc.Status)
	assert.Equal(t, "使用手册", st.createdDoc.Name)
	assert.Equal(t, "txt", st.createdDoc.FileType)
	assert.Equal(t, int64(12), st.createdDoc.FileSize)
	assert.Equal(t, "正文内容", st.createdDoc.Content)
	assert.Zero(t, st.createdDoc.ChunkCount, "pending 恒 0")
	assert.Empty(t, st.createdDoc.ErrorMessage)

	assert.Equal(t, StatusPending, got.Status)
	assert.Equal(t, "txt", got.FileType)
	assert.Equal(t, int64(12), got.FileSize)

	assert.Equal(t, []string{"detail:1"}, cm.deleted, "文档计数变化 → 写时 evict（澄清拍板）")
	assert.Empty(t, st.ops, "本篇无 dispatch / 事务调用")
	assert.Zero(t, st.txCalls)
}

// 插入期错误：23503（KB 被并发删除）→ NotFound；普通错误包装。
func TestUploadDocumentErrors(t *testing.T) {
	kb := map[uint64]*KnowledgeBase{1: kbFixture(1, 5, true)}
	svc := newSvcWithCache(&stubStore{kbByID: kb, createDocErr: pgErr("23503")}, &stubModels{}, &stubCache{})
	_, err := svc.UploadDocument(context.Background(), validUploadReq(1))
	assert.ErrorIs(t, err, ragapi.ErrKnowledgeBaseNotFound)

	boom := errors.New("insert failed")
	svc = newSvcWithCache(&stubStore{kbByID: kb, createDocErr: boom}, &stubModels{}, &stubCache{})
	_, err = svc.UploadDocument(context.Background(), validUploadReq(1))
	assert.ErrorIs(t, err, boom)
}

// 详情：Content 原文 + chunk_count 直读列 + 状态回显；404 哨兵。
func TestGetDocument(t *testing.T) {
	d := docFixture(9, 1, StatusReady)
	d.ChunkCount = 8
	svc := newSvcWithCache(&stubStore{docsByID: map[uint64]*Document{9: d}}, &stubModels{}, &stubCache{})

	got, err := svc.GetDocument(context.Background(), ragapi.GetDocumentReq{ID: 9})
	require.NoError(t, err)
	assert.Equal(t, "正文内容", got.Content)
	assert.Equal(t, 8, got.ChunkCount, "chunk_count 直读列（非计数查询）")
	assert.Equal(t, StatusReady, got.Status)
	assert.Equal(t, "txt", got.FileType)

	_, err = svc.GetDocument(context.Background(), ragapi.GetDocumentReq{ID: 999})
	assert.ErrorIs(t, err, ragapi.ErrDocumentNotFound)
}

// 文档列表：先验 KB；keyset 参数透传（首页 before=0 / 次页回传 cursor）；FetchN =
// limit+1 取数；has_more / next_cursor 组装（next_cursor = 当页末行 id）。
func TestListDocuments(t *testing.T) {
	kb := map[uint64]*KnowledgeBase{1: kbFixture(1, 5, true)}
	st := &stubStore{
		kbByID:   kb,
		listDocs: []Document{*docFixture(3, 1, StatusReady), *docFixture(2, 1, StatusReady)},
	}
	svc := newSvcWithCache(st, &stubModels{}, &stubCache{})

	got, err := svc.ListDocuments(context.Background(), ragapi.ListDocumentsReq{KnowledgeBaseID: 1, Limit: 1})
	require.NoError(t, err)
	assert.Equal(t, uint64(1), st.listDocsKB)
	assert.Zero(t, st.listDocsBefore, "首页 before=0")
	assert.Equal(t, 2, st.listDocsLimit, "FetchN = limit+1")
	assert.True(t, got.HasMore)
	require.Len(t, got.Items, 1)
	assert.Equal(t, "3", got.Items[0].ID, "id DESC 首行")
	assert.Equal(t, 1, got.Limit)
	key, err := page.DecodeCursor[docCursorKey](got.NextCursor)
	require.NoError(t, err)
	assert.Equal(t, uint64(3), key.ID, "next_cursor 指向当页末行")

	// 次页：cursor 原样回传 → beforeID = 上页末行 id
	got2, err := svc.ListDocuments(context.Background(), ragapi.ListDocumentsReq{KnowledgeBaseID: 1, Limit: 1, Cursor: got.NextCursor})
	require.NoError(t, err)
	assert.Equal(t, uint64(3), st.listDocsBefore)
	assert.True(t, got2.HasMore) // stub 固定返 2 行 > limit=1

	// 坏 cursor → 400（ErrValidationFailed 包装）
	_, err = svc.ListDocuments(context.Background(), ragapi.ListDocumentsReq{KnowledgeBaseID: 1, Cursor: "!!!bad"})
	assert.ErrorIs(t, err, errs.ErrValidationFailed)

	// KB 不存在 → 404
	svc = newSvcWithCache(&stubStore{}, &stubModels{}, &stubCache{})
	_, err = svc.ListDocuments(context.Background(), ragapi.ListDocumentsReq{KnowledgeBaseID: 999})
	assert.ErrorIs(t, err, ragapi.ErrKnowledgeBaseNotFound)
}

// 空页：Items 非 nil、has_more=false、next_cursor 空。
func TestListDocumentsEmpty(t *testing.T) {
	st := &stubStore{kbByID: map[uint64]*KnowledgeBase{1: kbFixture(1, 5, true)}}
	svc := newSvcWithCache(st, &stubModels{}, &stubCache{})

	got, err := svc.ListDocuments(context.Background(), ragapi.ListDocumentsReq{KnowledgeBaseID: 1, Limit: 20})
	require.NoError(t, err)
	assert.NotNil(t, got.Items, "空页返 [] 不返 null")
	assert.Empty(t, got.Items)
	assert.False(t, got.HasMore)
	assert.Empty(t, got.NextCursor)
}

// 软删：取 KB 归属后走 store 事务（规则 2 在 store 内）；evict 所属 KB detail key。
func TestDeleteDocument(t *testing.T) {
	st := &stubStore{docsByID: map[uint64]*Document{9: docFixture(9, 1, StatusReady)}}
	cm := &stubCache{}
	svc := newSvcWithCache(st, &stubModels{}, cm)

	err := svc.DeleteDocument(context.Background(), ragapi.DeleteDocumentReq{ID: 9})
	require.NoError(t, err)
	assert.True(t, st.softDelDone)
	assert.Equal(t, uint64(9), st.softDelID)
	assert.Equal(t, []string{"detail:1"}, cm.deleted, "evict 文档所属 KB 的 detail key")

	err = svc.DeleteDocument(context.Background(), ragapi.DeleteDocumentReq{ID: 999})
	assert.ErrorIs(t, err, ragapi.ErrDocumentNotFound)

	svc = newSvcWithCache(
		&stubStore{docsByID: map[uint64]*Document{9: docFixture(9, 1, StatusReady)}, softDelErr: gorm.ErrRecordNotFound},
		&stubModels{}, &stubCache{})
	err = svc.DeleteDocument(context.Background(), ragapi.DeleteDocumentReq{ID: 9})
	assert.ErrorIs(t, err, ragapi.ErrDocumentNotFound, "并发已删（RowsAffected=0）→ 哨兵翻译")
}

// reindex：pending / processing 挡并发；ready / failed 走规则 3 事务序列
// （删 chunks → 置 pending + 清 error_message + chunk_count=0）；快照重读。
func TestReindexDocument(t *testing.T) {
	for _, status := range []string{StatusPending, StatusProcessing} {
		st := &stubStore{docsByID: map[uint64]*Document{9: docFixture(9, 1, status)}}
		svc := newSvcWithCache(st, &stubModels{}, &stubCache{})
		_, err := svc.ReindexDocument(context.Background(), ragapi.ReindexDocumentReq{ID: 9})
		assert.ErrorIs(t, err, ragapi.ErrDocumentProcessing, "状态 %s 挡并发重入", status)
		assert.Zero(t, st.txCalls, "挡下时不进事务")
	}

	// ready → 事务序列顺序钉死；快照 status=pending / chunk_count=0 / error_message 空
	d := docFixture(9, 1, StatusReady)
	d.ChunkCount = 8
	d.ErrorMessage = "上次失败原因"
	st := &stubStore{docsByID: map[uint64]*Document{9: d}}
	svc := newSvcWithCache(st, &stubModels{}, &stubCache{})
	got, err := svc.ReindexDocument(context.Background(), ragapi.ReindexDocumentReq{ID: 9})
	require.NoError(t, err)
	assert.Equal(t, []string{"delChunks:9", "reset:9"}, st.ops, "规则 3 事务序列（先删 chunks 后重置）")
	assert.Equal(t, 1, st.txCalls)
	assert.Equal(t, StatusPending, got.Status)
	assert.Zero(t, got.ChunkCount)
	assert.Empty(t, got.ErrorMessage)

	// failed 同样可重跑
	st2 := &stubStore{docsByID: map[uint64]*Document{9: docFixture(9, 1, StatusFailed)}}
	svc = newSvcWithCache(st2, &stubModels{}, &stubCache{})
	got2, err := svc.ReindexDocument(context.Background(), ragapi.ReindexDocumentReq{ID: 9})
	require.NoError(t, err)
	assert.Equal(t, StatusPending, got2.Status)

	// 404；事务失败包装上抛
	svc = newSvcWithCache(&stubStore{}, &stubModels{}, &stubCache{})
	_, err = svc.ReindexDocument(context.Background(), ragapi.ReindexDocumentReq{ID: 999})
	assert.ErrorIs(t, err, ragapi.ErrDocumentNotFound)

	boom := errors.New("tx failed")
	svc = newSvcWithCache(
		&stubStore{docsByID: map[uint64]*Document{9: docFixture(9, 1, StatusReady)}, resetErr: boom},
		&stubModels{}, &stubCache{})
	_, err = svc.ReindexDocument(context.Background(), ragapi.ReindexDocumentReq{ID: 9})
	assert.ErrorIs(t, err, boom)
	assert.NotErrorIs(t, err, ragapi.ErrDocumentNotFound)
}

// ---- 检索 Retrieve（spec 05） ----

// stubStore 检索两方法覆写（spec 05 §1 六/七步）。
func (s *stubStore) SearchChunks(_ context.Context, kbIDs []uint64, query []float32, limit, efSearch int) ([]ChunkHit, error) {
	s.searchGotKBs = kbIDs
	s.searchGotQ = query
	s.searchGotLim = limit
	s.searchGotEF = efSearch
	if s.searchErr != nil {
		return nil, s.searchErr
	}
	return s.searchHits, nil
}

func (s *stubStore) GetDocumentMetasByIDs(_ context.Context, ids []uint64) (map[uint64]string, error) {
	s.docMetasGot = ids
	if s.docMetasErr != nil {
		return nil, s.docMetasErr
	}
	return s.docMetas, nil
}

// stubEmbedder 实现 embedder 窄接口：记录收到的 opts/inputs，返回可配置结果。
type stubEmbedder struct {
	embedErr   error
	embedVecs  [][]float32
	embedCalls int
	gotOpts    llm.EmbedOptions
	gotInputs  []string
}

func (s *stubEmbedder) EmbedStrings(_ context.Context, opts llm.EmbedOptions, inputs []string) (llm.EmbedResult, error) {
	s.embedCalls++
	s.gotOpts = opts
	s.gotInputs = inputs
	if s.embedErr != nil {
		return llm.EmbedResult{}, s.embedErr
	}
	return llm.EmbedResult{Vectors: s.embedVecs}, nil
}

// newRetrieveSvc 检索路径构造（embeds 非 nil；无缓存路径）。
func newRetrieveSvc(st Store, models providerapi.ModelService, embeds embedder, cfg Config) ragapi.KnowledgeBaseService {
	svc, _ := New(st, models, embeds, nil, cfg)
	return svc
}

// embedVec 1536 维查询向量（维度校验的 happy 路径）。
func embedVec() []float32 {
	v := make([]float32, ragapi.RequiredEmbeddingDim)
	for i := range v {
		v[i] = 0.01
	}
	return v
}

// embedVecsN 生成 n 个1536 维向量（管线失败矩阵等需要足量向量的测试）。
func embedVecsN(n int) [][]float32 {
	vecs := make([][]float32, n)
	for i := range vecs {
		vecs[i] = embedVec()
	}
	return vecs
}

// resolveCfgOK stub LLMConfig（明文四不：stub 值不涉真实凭据）。
func resolveCfgOK() *providerapi.LLMConfig {
	return &providerapi.LLMConfig{ProviderName: "openai 主力", Kind: "openai_compatible", BaseURL: "https://api.example.com/v1", APIKey: "sk-test", ModelID: "text-embedding-3-small"}
}

func TestRetrieveHappyPath(t *testing.T) {
	q := embedVec()
	st := &stubStore{
		kbByID: map[uint64]*KnowledgeBase{1: kbFixture(1, 5, true), 2: kbFixture(2, 5, true)},
		searchHits: []ChunkHit{
			{ID: 9, DocumentID: 100, KnowledgeBaseID: 1, ChunkIndex: 0, Content: "正文一", TokenCount: 12, Distance: 0.25},
			{ID: 10, DocumentID: 101, KnowledgeBaseID: 2, ChunkIndex: 3, Content: "正文二", TokenCount: 10, Distance: 0.5},
		},
		docMetas: map[uint64]string{100: "手册.txt", 101: "faq.md"},
	}
	models := &stubModels{resolveCfg: resolveCfgOK()}
	embeds := &stubEmbedder{embedVecs: [][]float32{q}}
	svc := newRetrieveSvc(st, models, embeds, Config{TopK: 5, EFSearch: 80})

	chunks, err := svc.Retrieve(context.Background(), ragapi.RetrieveReq{Query: "怎么创建 Agent", KBIDs: []uint64{1, 2}})
	require.NoError(t, err)
	require.Len(t, chunks, 2)

	// 编排断言：KB 直读 → ResolveLLMConfig（KB 的模型 id）→ EmbedStrings（query 单条）→
	// SearchChunks（enabled kbIDs + 向量 + limit + efSearch）→ GetDocumentMetasByIDs → 组装。
	assert.Equal(t, providerapi.ResolveLLMConfigReq{ModelID: 5}, models.resolveGot)
	assert.Equal(t, 1, embeds.embedCalls)
	assert.Equal(t, []string{"怎么创建 Agent"}, embeds.gotInputs)
	assert.Equal(t, llm.ProviderKind("openai_compatible"), embeds.gotOpts.Kind)
	assert.Equal(t, "text-embedding-3-small", embeds.gotOpts.Model)
	assert.Equal(t, []uint64{1, 2}, st.searchGotKBs)
	assert.Equal(t, q, st.searchGotQ)
	assert.Equal(t, 5, st.searchGotLim)
	assert.Equal(t, 80, st.searchGotEF)
	assert.Equal(t, []uint64{100, 101}, st.docMetasGot)

	// RetrievedChunk 组装：id 字符串化 + DocumentName 解析 + similarity = 1 - distance。
	assert.Equal(t, "9", chunks[0].ChunkID)
	assert.Equal(t, "100", chunks[0].DocumentID)
	assert.Equal(t, "1", chunks[0].KnowledgeBaseID)
	assert.Equal(t, "手册.txt", chunks[0].DocumentName)
	assert.Equal(t, 0, chunks[0].ChunkIndex)
	assert.Equal(t, "正文一", chunks[0].Content)
	assert.InDelta(t, 0.75, chunks[0].Similarity, 1e-9)
	assert.InDelta(t, 0.5, chunks[1].Similarity, 1e-9)
}

// disabled 静默剔除：部分剔除继续检索；全部 disabled → 空 []（非 nil）、零 embedding 调用。
func TestRetrieveDisabledPruned(t *testing.T) {
	st := &stubStore{
		kbByID:     map[uint64]*KnowledgeBase{1: kbFixture(1, 5, true), 2: kbFixture(2, 5, false)},
		searchHits: []ChunkHit{{ID: 9, DocumentID: 100, KnowledgeBaseID: 1, ChunkIndex: 0, Content: "正文", Distance: 0.2}},
		docMetas:   map[uint64]string{100: "手册.txt"},
	}
	embeds := &stubEmbedder{embedVecs: [][]float32{embedVec()}}
	svc := newRetrieveSvc(st, &stubModels{resolveCfg: resolveCfgOK()}, embeds, Config{TopK: 5, EFSearch: 80})

	chunks, err := svc.Retrieve(context.Background(), ragapi.RetrieveReq{Query: "q", KBIDs: []uint64{1, 2}})
	require.NoError(t, err)
	require.Len(t, chunks, 1)
	assert.Equal(t, []uint64{1}, st.searchGotKBs, "disabled KB 剔除后进 ANN")

	// 全 disabled → 空 [] 非 nil，不触 embedding / ANN。
	st2 := &stubStore{kbByID: map[uint64]*KnowledgeBase{1: kbFixture(1, 5, false)}}
	embeds2 := &stubEmbedder{embedVecs: [][]float32{embedVec()}}
	svc2 := newRetrieveSvc(st2, &stubModels{resolveCfg: resolveCfgOK()}, embeds2, Config{TopK: 5, EFSearch: 80})
	got, err := svc2.Retrieve(context.Background(), ragapi.RetrieveReq{Query: "q", KBIDs: []uint64{1}})
	require.NoError(t, err)
	assert.NotNil(t, got, "空结果初始化空 slice，不返 nil")
	assert.Empty(t, got)
	assert.Zero(t, embeds2.embedCalls)
	assert.Nil(t, st2.searchGotKBs)
}

// 任一 KB 404 → ErrKnowledgeBaseNotFound（先于 disabled 剔除）。
func TestRetrieveKBNotFound(t *testing.T) {
	st := &stubStore{kbByID: map[uint64]*KnowledgeBase{1: kbFixture(1, 5, true)}}
	svc := newRetrieveSvc(st, &stubModels{}, &stubEmbedder{}, Config{})

	_, err := svc.Retrieve(context.Background(), ragapi.RetrieveReq{Query: "q", KBIDs: []uint64{1, 999}})
	assert.ErrorIs(t, err, ragapi.ErrKnowledgeBaseNotFound)
}

// 混嵌入模型 → ErrEmbeddingModelMismatch（向量空间不可比）。
func TestRetrieveEmbeddingModelMismatch(t *testing.T) {
	st := &stubStore{kbByID: map[uint64]*KnowledgeBase{1: kbFixture(1, 5, true), 2: kbFixture(2, 6, true)}}
	embeds := &stubEmbedder{}
	svc := newRetrieveSvc(st, &stubModels{}, embeds, Config{})

	_, err := svc.Retrieve(context.Background(), ragapi.RetrieveReq{Query: "q", KBIDs: []uint64{1, 2}})
	assert.ErrorIs(t, err, ragapi.ErrEmbeddingModelMismatch)
	assert.Zero(t, embeds.embedCalls, "不一致即止，不发 embedding")
}

// llm 哨兵透传（spec 05 §1 第 4 步）：Unsupported / Busy（wrapped）/ RateLimited（*llm.Error）。
func TestRetrieveLLMSentinelPassthrough(t *testing.T) {
	busy := fmt.Errorf("wrap: %w", llm.ErrProviderBusy)
	rateLimited := &llm.Error{Class: llm.ClassRateLimited, Err: errors.New("HTTP 429")}
	cases := []struct {
		name string
		err  error
		is   error      // errors.Is 断言目标（哨兵）
		as   *llm.Error // errors.As 断言目标（分类错误）
		want llm.Class
	}{
		{"unsupported", llm.ErrEmbeddingUnsupported, llm.ErrEmbeddingUnsupported, nil, ""},
		{"busy wrapped", busy, llm.ErrProviderBusy, nil, ""},
		{"rate limited", rateLimited, nil, rateLimited, llm.ClassRateLimited},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := &stubStore{kbByID: map[uint64]*KnowledgeBase{1: kbFixture(1, 5, true)}}
			svc := newRetrieveSvc(st, &stubModels{resolveCfg: resolveCfgOK()}, &stubEmbedder{embedErr: tc.err}, Config{})

			_, err := svc.Retrieve(context.Background(), ragapi.RetrieveReq{Query: "q", KBIDs: []uint64{1}})
			require.Error(t, err)
			if tc.is != nil {
				assert.ErrorIs(t, err, tc.is)
			}
			if tc.as != nil {
				var le *llm.Error
				assert.ErrorAs(t, err, &le)
				assert.Equal(t, tc.want, le.Class)
			}
		})
	}
}

// 维度 ≠ 1536 → 包装 errs.ErrInternal（服务端配置错，不暴露细节）。
func TestRetrieveDimMismatch(t *testing.T) {
	st := &stubStore{kbByID: map[uint64]*KnowledgeBase{1: kbFixture(1, 5, true)}}
	embeds := &stubEmbedder{embedVecs: [][]float32{make([]float32, 1535)}} // 差一维
	svc := newRetrieveSvc(st, &stubModels{resolveCfg: resolveCfgOK()}, embeds, Config{})

	_, err := svc.Retrieve(context.Background(), ragapi.RetrieveReq{Query: "q", KBIDs: []uint64{1}})
	assert.ErrorIs(t, err, errs.ErrInternal)
	assert.NotErrorIs(t, err, errs.ErrValidationFailed, "服务端配置错不是用户输入错")
}

// TopK=0 取 cfg.TopK；cfg 越界 clamp 到 [TopKMin, TopKMax]。
func TestRetrieveTopKDefaultAndClamp(t *testing.T) {
	cases := []struct {
		name    string
		reqTopK int
		cfgTopK int
		wantLim int
	}{
		{"req 0 → cfg 默认", 0, 5, 5},
		{"cfg 超 max clamp 20", 0, 25, ragapi.TopKMax},
		{"cfg 低于 min clamp 1", 0, 0, ragapi.TopKMin},
		{"显式 req 直传（域内）", 12, 5, 12},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := &stubStore{
				kbByID:     map[uint64]*KnowledgeBase{1: kbFixture(1, 5, true)},
				searchHits: []ChunkHit{{ID: 9, DocumentID: 100, KnowledgeBaseID: 1, ChunkIndex: 0, Content: "正文", Distance: 0.2}},
				docMetas:   map[uint64]string{100: "a.txt"},
			}
			svc := newRetrieveSvc(st, &stubModels{resolveCfg: resolveCfgOK()}, &stubEmbedder{embedVecs: [][]float32{embedVec()}}, Config{TopK: tc.cfgTopK, EFSearch: 80})
			_, err := svc.Retrieve(context.Background(), ragapi.RetrieveReq{Query: "q", TopK: tc.reqTopK, KBIDs: []uint64{1}})
			require.NoError(t, err)
			assert.Equal(t, tc.wantLim, st.searchGotLim)
		})
	}
}

// 悬空文档（软删 / 不存在）DocumentName = ""；零命中返回空非 nil。
func TestRetrieveDanglingNameAndEmptyHits(t *testing.T) {
	st := &stubStore{
		kbByID:     map[uint64]*KnowledgeBase{1: kbFixture(1, 5, true)},
		searchHits: []ChunkHit{{ID: 9, DocumentID: 777, KnowledgeBaseID: 1, ChunkIndex: 0, Content: "正文", Distance: 0.2}},
		docMetas:   map[uint64]string{}, // 777 悬空
	}
	svc := newRetrieveSvc(st, &stubModels{resolveCfg: resolveCfgOK()}, &stubEmbedder{embedVecs: [][]float32{embedVec()}}, Config{TopK: 5, EFSearch: 80})

	chunks, err := svc.Retrieve(context.Background(), ragapi.RetrieveReq{Query: "q", KBIDs: []uint64{1}})
	require.NoError(t, err)
	require.Len(t, chunks, 1)
	assert.Equal(t, "", chunks[0].DocumentName)

	// 零命中（空 KB 检索）→ 空 [] 非 nil。
	st2 := &stubStore{kbByID: map[uint64]*KnowledgeBase{1: kbFixture(1, 5, true)}}
	svc2 := newRetrieveSvc(st2, &stubModels{resolveCfg: resolveCfgOK()}, &stubEmbedder{embedVecs: [][]float32{embedVec()}}, Config{TopK: 5, EFSearch: 80})
	got, err := svc2.Retrieve(context.Background(), ragapi.RetrieveReq{Query: "q", KBIDs: []uint64{1}})
	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.Empty(t, got)
}

// 请求校验：query 空 / KBIDs 空 / KBIDs 超 MaxRetrieveKBs。
func TestRetrieveValidate(t *testing.T) {
	svc := newRetrieveSvc(&stubStore{}, &stubModels{}, &stubEmbedder{}, Config{})

	_, err := svc.Retrieve(context.Background(), ragapi.RetrieveReq{KBIDs: []uint64{1}})
	assert.Error(t, err, "query 空")

	_, err = svc.Retrieve(context.Background(), ragapi.RetrieveReq{Query: "q"})
	assert.Error(t, err, "kb_ids 空")

	ids := make([]uint64, ragapi.MaxRetrieveKBs+1)
	for i := range ids {
		ids[i] = uint64(i + 1)
	}
	_, err = svc.Retrieve(context.Background(), ragapi.RetrieveReq{Query: "q", KBIDs: ids})
	assert.Error(t, err, "kb_ids 超 10")
}

// ResolveLLMConfig 失败（模型悬空）原样上抛（provider 哨兵由 handler 映射）。
func TestRetrieveResolveError(t *testing.T) {
	st := &stubStore{kbByID: map[uint64]*KnowledgeBase{1: kbFixture(1, 5, true)}}
	svc := newRetrieveSvc(st, &stubModels{resolveErr: providerapi.ErrModelNotFound}, &stubEmbedder{}, Config{})

	_, err := svc.Retrieve(context.Background(), ragapi.RetrieveReq{Query: "q", KBIDs: []uint64{1}})
	assert.ErrorIs(t, err, providerapi.ErrModelNotFound)
}
