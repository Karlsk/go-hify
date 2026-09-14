// service.go —— rag 业务层：实现 ragapi.KnowledgeBaseService 的 KB + 文档 CRUD
// 编排（spec 04）。不变量规则 2（文档删除 / 深度停用同事务硬删 chunks）与规则 3
//（reindex 事务重置）在本层编排 / store 层执行；Upload / Reindex / Enable 落 pending
// 后 dispatch 重跑入库管线（07）。无软删：DELETE=真删，可逆下架走 enabled。
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/sync/semaphore"
	"gorm.io/gorm"

	"github.com/Karlsk/go-hify/internal/platform/cache"
	"github.com/Karlsk/go-hify/internal/platform/errs"
	"github.com/Karlsk/go-hify/internal/platform/llm"
	"github.com/Karlsk/go-hify/internal/platform/page"
	providerapi "github.com/Karlsk/go-hify/internal/provider/api"
	ragapi "github.com/Karlsk/go-hify/internal/rag/api"
)

// Store 数据层接口（依赖倒置：本包定义，store 包实现，组合根注入）。本篇声明 CRUD
// 所需子集（spec 04 §1）——SearchChunks（05）/ 管线方法族（07）同包增量演进。
type Store interface {
	// CreateKnowledgeBase 插入 KB（id / created_at / updated_at 由 DB 生成经 RETURNING 回填）。
	CreateKnowledgeBase(ctx context.Context, kb *KnowledgeBase) error
	// GetKnowledgeBaseByID 按主键查；未找到返回 gorm.ErrRecordNotFound。
	GetKnowledgeBaseByID(ctx context.Context, id uint64) (*KnowledgeBase, error)
	// ListKnowledgeBases 活跃 KB 偏移分页（id 升序）；name 非空时 ILIKE 模糊过滤
	// （传原始关键字，通配符拼接在 store SQL 侧；小表豁免前导通配红线，总览决策 #17）。
	ListKnowledgeBases(ctx context.Context, p page.OffsetParams, name string) (page.OffsetResult[KnowledgeBase], error)
	// UpdateKnowledgeBase 全量 Save（PUT 语义，零值一并覆盖）。
	UpdateKnowledgeBase(ctx context.Context, kb *KnowledgeBase) error
	// DeleteKnowledgeBase 硬删（KB 级联删除事务的最后一步；agent_knowledge_bases
	// 绑定由 FK CASCADE 清理）；RowsAffected=0 返回 gorm.ErrRecordNotFound。
	DeleteKnowledgeBase(ctx context.Context, id uint64) error
	// DeleteChunksByKB 硬删 KB 下全部分块（KB 级联删除事务第一步；0 行合法——空库）。
	DeleteChunksByKB(ctx context.Context, kbID uint64) error
	// DeleteDocumentsByKB 硬删 KB 下全部文档行（KB 级联删除事务第二步；0 行合法）。
	DeleteDocumentsByKB(ctx context.Context, kbID uint64) error
	// CountDocumentsByKBIDs 列表聚合用批量计数（全部文档，含停用）；map 无键 = 0。
	CountDocumentsByKBIDs(ctx context.Context, ids []uint64) (map[uint64]int64, error)

	// GetDocumentByID 按主键查（含 content 原文——大文本仅此详情路径取，禁 SELECT *
	// 的列清单纪律；停用行同样可见）；未找到返回 gorm.ErrRecordNotFound。
	GetDocumentByID(ctx context.Context, id uint64) (*Document, error)
	// CreateDocument 插入文档行（status=pending + enabled=true + 元数据 + 原文）。
	CreateDocument(ctx context.Context, d *Document) error
	// HardDeleteDocument store 内事务：硬删 documents 行 + 其 chunks（不变量规则 2，
	// spec 01 §3）；RowsAffected=0 返回 gorm.ErrRecordNotFound。
	HardDeleteDocument(ctx context.Context, id uint64) error
	// DisableDocument 深度停用事务：严格状态机（enabled 且终态）命中则同事务硬删
	// chunks + enabled=false + chunk_count=0（内容保留）；0 行返回 gorm.ErrRecordNotFound
	//（service 重读区分 404 / 已停用幂等 / 入库中 409）。
	DisableDocument(ctx context.Context, id uint64) error
	// EnableDocument 重新启用：严格状态机（停用且终态）→ enabled=true + 重置 pending
	//（service 随后 dispatch 重跑管线）；0 行返回 gorm.ErrRecordNotFound（同上区分）。
	EnableDocument(ctx context.Context, id uint64) error
	// ListDocumentsByKB KB 下全部文档 keyset 分页（id DESC，停用行可见）；beforeID=0 为首页。
	ListDocumentsByKB(ctx context.Context, kbID, beforeID uint64, limit int) ([]Document, error)
	// DeleteChunksByDocument 硬删文档全部分块（规则 2 / 3 共用；0 行合法——pending 无 chunks）。
	DeleteChunksByDocument(ctx context.Context, documentID uint64) error
	// ResetDocumentForReindex 重置文档供重跑入库管线（规则 3）：status=pending +
	// error_message='' + chunk_count=0；RowsAffected=0 返回 gorm.ErrRecordNotFound。
	ResetDocumentForReindex(ctx context.Context, id uint64) error

	// SearchChunks 跨 KB 单表 ANN 召回（spec 05 §2）：事务内 SET LOCAL ef_search 后
	// 余弦距离排序取 top limit；kbIDs ≤ MaxRetrieveKBs；Scan 目标 ChunkHit，不解析名称。
	SearchChunks(ctx context.Context, kbIDs []uint64, query []float32, limit int, efSearch int) ([]ChunkHit, error)
	// GetDocumentMetasByIDs 批量取文档名（引用名解析，spec 05 §2）：只读 id/name 不碰
	// content；已删 / 不存在的 id 无键（悬空 → ""）；空 ids 返回空 map 不发 SQL。
	GetDocumentMetasByIDs(ctx context.Context, ids []uint64) (map[uint64]string, error)

	// MarkDocumentProcessing pending→processing 翻转（spec 07 §3，严格状态机：
	// WHERE id AND status='pending'）；RowsAffected=0 返回 gorm.ErrRecordNotFound
	//（文档已不在待处理态，管线中止）。
	MarkDocumentProcessing(ctx context.Context, id uint64) error
	// MarkDocumentReady processing→ready 终态（spec 07 §3，严格状态机：WHERE id AND
	// status='processing'），chunk_count 与 ready 原子同写（不变量规则 1）；RowsAffected=0
	// 返回 gorm.ErrRecordNotFound——终态事务回滚，chunks 不落孤儿。
	MarkDocumentReady(ctx context.Context, id uint64, chunkCount int) error
	// MarkDocumentFailed 置 failed + error_message（spec 07 §3，WHERE id AND status IN
	// ('pending','processing')——覆盖 pending 态失败与 Recovery 扫描）；RowsAffected=0
	// 返回 nil 静默：markFailed 是尽力而为的最后一步，文档已终态 / 已删不构成错误。
	MarkDocumentFailed(ctx context.Context, id uint64, message string) error
	// CreateChunks 批量插入分块（spec 07 §3）：切片插入 = 单语句多 VALUES（仓规批量
	// 写；终态事务内逐批调用，每批 ≤ EmbedBatchSize 行）；空切片直返不发 SQL。
	CreateChunks(ctx context.Context, cs []DocumentChunk) error
	// ListIngestingDocuments 扫全部入库中文档（spec 07 §4）：WHERE status IN
	// ('pending','processing') 命中 partial idx idx_documents_ingesting；Recovery 用。
	ListIngestingDocuments(ctx context.Context) ([]Document, error)

	// WithTx 事务包装：fn 拿到共享同一 tx 句柄的 Store（仍以 Store 接口身份传入）。
	WithTx(ctx context.Context, fn func(tx Store) error) error
}

// embedder 下游收窄小接口（chat llmConfigResolver 先例）：Retrieve 的 query 向量化
// （spec 05）经此调用 llm.Embedder；本篇仅持有不调用。
type embedder interface {
	EmbedStrings(ctx context.Context, opts llm.EmbedOptions, inputs []string) (llm.EmbedResult, error)
}

// cacheManager 配置类缓存窄接口（agent / provider service 同款），NameRag 命名空间。
type cacheManager interface {
	Get(ctx context.Context, name, key string, dst any) (bool, error)
	Set(ctx context.Context, name, key string, val any) error
	Delete(ctx context.Context, name, key string) error
}

// Config rag 模块配置（组合根从 platform/config.RagCfg 映射构造，仓内无业务模块
// 直接 import platform/config 的先例）。05 Retrieve 消费 TopK / EFSearch；
// 07（管线：分块与并发）按其 spec 增补。
type Config struct {
	// TopK 检索默认 top_k：请求未设（0）时取此值；最终 clamp 到 [TopKMin, TopKMax]
	// （RAG_TOP_K，默认 5）。
	TopK int
	// EFSearch HNSW 查询时 ef_search（召回率 vs 延迟旋钮；服务端 config 已 clamp，
	// 非用户输入——RAG_EF_SEARCH，默认 80）。
	EFSearch int
	// ChunkSize 分块目标尺寸（rune，递归分割，spec 06 §2；RAG_CHUNK_SIZE，默认 500）。
	ChunkSize int
	// ChunkOverlap 相邻块重叠（上一块尾部前缀，rune；RAG_CHUNK_OVERLAP，默认 80）。
	ChunkOverlap int
	// EmbedBatchSize embedding 单批条数（spec 07 §2 环节 7；RAG_EMBED_BATCH_SIZE，
	// 默认 32）。
	EmbedBatchSize int
	// IngestConcurrency 入库管线并发（每文档一个槽；RAG_INGEST_CONCURRENCY，默认 2）。
	IngestConcurrency int
}

// cacheKeyDetail KB 详情缓存 key 模板（NameRag 命名空间内，agent cacheKeyDetail 同款）。
const cacheKeyDetail = "detail:%d"

// PG 错误码（agent/service 同款，包内私有）。
const (
	pgCodeUniqueViolation = "23505" // 唯一约束：check-then-insert 的竞态兜底
	pgCodeFKViolation     = "23503" // 外键违例：引用行不存在 / 删除被引用
)

// isUniqueViolation 判断 PG 唯一约束冲突（23505）。
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgCodeUniqueViolation
}

// isFKViolation 判断 PG 外键违例（23503）。
func isFKViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgCodeFKViolation
}

// kbService 实现 ragapi.KnowledgeBaseService。
type kbService struct {
	store  Store
	models providerapi.ModelService // 建库时嵌入模型预检（provider api 注入）
	embeds embedder                 // 检索 / 入库向量化（05 Retrieve 与 07 管线共用）
	cache  cacheManager             // KB 配置缓存（NameRag，Cache-Aside）
	cfg    Config

	// 入库管线（spec 07 §1）：dispatch 可注入（测试同步直调），sem 限并发文档数；
	// acquireWait 抢槽超时（默认 2s 常量，测试注入短值防慢）。
	dispatch    func(ctx context.Context, docID uint64)
	sem         *semaphore.Weighted
	acquireWait time.Duration
}

// New 构造 KnowledgeBaseService 与 Recovery（provider (api, Prober) 双返回先例）：
// api 面注入 handler / 上游模块；*Recovery 由组合根调 MarkInterruptedFailed
// （08 装配）——服务重启把残留 pending/processing 置 failed 的自愈入口。
func New(store Store, models providerapi.ModelService, embeds embedder, cm cacheManager, cfg Config) (ragapi.KnowledgeBaseService, *Recovery) {
	s := &kbService{
		store: store, models: models, embeds: embeds, cache: cm, cfg: cfg,
		sem:         semaphore.NewWeighted(int64(max(cfg.IngestConcurrency, 1))),
		acquireWait: ingestAcquireWait,
	}
	s.dispatch = s.dispatchIngest
	return s, &Recovery{svc: s}
}

// Create 建库：嵌入模型预检（capability / dim / enabled，预检顺序即 spec 端点 1 错误
// 列表顺序）→ 落库 → 快照。预检失败 / 停用透传 provider 哨兵；23505 → NameConflict、
// 23503（预检后模型被并发删除）→ ErrModelNotFound。
func (s *kbService) Create(ctx context.Context, req ragapi.CreateKnowledgeBaseReq) (*ragapi.KnowledgeBaseSchema, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate create knowledge base: %w", err)
	}
	m, err := s.models.Get(ctx, providerapi.GetModelReq{ID: req.EmbeddingModelID})
	if err != nil {
		if errors.Is(err, providerapi.ErrModelNotFound) {
			return nil, err // 哨兵透传（404）
		}
		return nil, fmt.Errorf("precheck embedding model: %w", err)
	}
	if m.Capability != providerapi.CapabilityEmbedding || m.EmbeddingDim == nil || *m.EmbeddingDim != ragapi.RequiredEmbeddingDim {
		return nil, fmt.Errorf("%w: 模型 %s 非 embedding 或维度≠%d（向量维度随模型钉死，换模型 = 重建 chunks）",
			ragapi.ErrEmbeddingDimMismatch, m.Name, ragapi.RequiredEmbeddingDim)
	}
	if !m.Enabled {
		return nil, providerapi.ErrModelDisabled // 哨兵透传（503）
	}

	kb := &KnowledgeBase{
		Name:             req.Name,
		Description:      req.Description,
		EmbeddingModelID: req.EmbeddingModelID,
		Enabled:          true, // 显式设值：布尔无 gorm default tag（provider 踩坑 #1）
	}
	// 切分策略：请求未传时用全局默认（空 ChunkStrategy{}，Resolve 降级读 cfg）
	if req.ChunkStrategy != nil {
		kb.ChunkStrategy = ChunkStrategy{
			Type:         req.ChunkStrategy.Type,
			ChunkSize:    req.ChunkStrategy.ChunkSize,
			ChunkOverlap: req.ChunkStrategy.ChunkOverlap,
			Separator:    req.ChunkStrategy.Separator,
		}
	}
	if err := s.store.CreateKnowledgeBase(ctx, kb); err != nil {
		if isUniqueViolation(err) {
			return nil, ragapi.ErrKnowledgeBaseNameConflict
		}
		if isFKViolation(err) {
			return nil, providerapi.ErrModelNotFound
		}
		return nil, fmt.Errorf("create knowledge base: %w", err)
	}
	schema := toKBSchema(kb, 0)
	return &schema, nil
}

// Get 详情（含 DocumentCount，Cache-Aside：NameRag + detail:{id}）。缓存读失败
// WARN 降级查库；Set 失败仅 WARN（TTL 30min 兜底）——缓存故障不是业务失败。
func (s *kbService) Get(ctx context.Context, req ragapi.GetKnowledgeBaseReq) (*ragapi.KnowledgeBaseSchema, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate get knowledge base: %w", err)
	}
	key := fmt.Sprintf(cacheKeyDetail, req.ID)
	var cached ragapi.KnowledgeBaseSchema
	if found, err := s.cache.Get(ctx, cache.NameRag, key, &cached); err != nil {
		slog.WarnContext(ctx, "read rag cache failed; fallback to db", "key", key, "err", err)
	} else if found {
		return &cached, nil
	}

	kb, err := s.store.GetKnowledgeBaseByID(ctx, req.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ragapi.ErrKnowledgeBaseNotFound
		}
		return nil, fmt.Errorf("get knowledge base %d: %w", req.ID, err)
	}
	counts, err := s.store.CountDocumentsByKBIDs(ctx, []uint64{req.ID})
	if err != nil {
		return nil, fmt.Errorf("count documents of kb %d: %w", req.ID, err)
	}
	schema := toKBSchema(kb, counts[req.ID])
	if err := s.cache.Set(ctx, cache.NameRag, key, schema); err != nil {
		slog.WarnContext(ctx, "set rag cache failed", "key", key, "err", err)
	}
	return &schema, nil
}

// List 偏移分页（KB 小配置表，仓规 OFFSET 例外）+ name ILIKE 模糊过滤（原始关键字
// 透传 store）+ 当页聚合（模型名 / 文档计数，两条 IN 批量查询防 N+1）。
func (s *kbService) List(ctx context.Context, req ragapi.ListKnowledgeBasesReq) (*ragapi.KnowledgeBaseListResult, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate list knowledge bases: %w", err)
	}
	res, err := s.store.ListKnowledgeBases(ctx, page.NewOffset(req.Page, req.PageSize), req.Name)
	if err != nil {
		return nil, fmt.Errorf("list knowledge bases: %w", err)
	}
	items := make([]ragapi.KnowledgeBaseListItem, 0, len(res.Items)) // 空页返 [] 不返 null
	for i := range res.Items {
		items = append(items, ragapi.KnowledgeBaseListItem{KnowledgeBaseSchema: toKBSchema(&res.Items[i], 0)})
	}
	if err := s.withAggregates(ctx, res.Items, items); err != nil {
		return nil, err
	}
	return &ragapi.KnowledgeBaseListResult{Items: items, Page: res.Page, PageSize: res.PageSize, Total: res.Total}, nil
}

// withAggregates 列表当页聚合：文档计数（CountDocumentsByKBIDs）+ 嵌入模型名
// （models.ListByIDs，悬空引用为 ""）——各恰一次批量查询（agent withAggregates 同款）。
func (s *kbService) withAggregates(ctx context.Context, kbs []KnowledgeBase, items []ragapi.KnowledgeBaseListItem) error {
	kbIDs := make([]uint64, len(kbs))
	modelIDs := make([]uint64, 0, len(kbs))
	seen := make(map[uint64]struct{}, len(kbs))
	for i := range kbs {
		kbIDs[i] = kbs[i].ID
		if _, ok := seen[kbs[i].EmbeddingModelID]; !ok {
			seen[kbs[i].EmbeddingModelID] = struct{}{}
			modelIDs = append(modelIDs, kbs[i].EmbeddingModelID)
		}
	}
	counts, err := s.store.CountDocumentsByKBIDs(ctx, kbIDs)
	if err != nil {
		return fmt.Errorf("count documents by kb ids: %w", err)
	}
	names := make(map[string]string, len(modelIDs))
	if len(modelIDs) > 0 {
		models, err := s.models.ListByIDs(ctx, providerapi.ListModelsByIDsReq{IDs: modelIDs})
		if err != nil {
			return fmt.Errorf("list models by ids: %w", err)
		}
		for i := range models {
			names[models[i].ID] = models[i].Name // 双方同为字符串化 id，键无须回转 uint64
		}
	}
	for i := range items {
		items[i].DocumentCount = counts[kbs[i].ID]
		items[i].EmbeddingModelName = names[items[i].EmbeddingModelID]
	}
	return nil
}

// Update 整体更新（PUT 语义）：name / description / enabled（*bool 三态，nil=不变）；
// embedding_model_id 不可改（维度冻结，Req 无该字段，报文出现即自然忽略）。
func (s *kbService) Update(ctx context.Context, req ragapi.UpdateKnowledgeBaseReq) (*ragapi.KnowledgeBaseSchema, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate update knowledge base: %w", err)
	}
	kb, err := s.store.GetKnowledgeBaseByID(ctx, req.ID) // 先读后写：区分 404 与静默不命中，保住 created_at
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ragapi.ErrKnowledgeBaseNotFound
		}
		return nil, fmt.Errorf("get knowledge base %d: %w", req.ID, err)
	}
	kb.Name = req.Name
	kb.Description = req.Description
	if req.Enabled != nil {
		kb.Enabled = *req.Enabled
	}
	if err := s.store.UpdateKnowledgeBase(ctx, kb); err != nil {
		if isUniqueViolation(err) {
			return nil, ragapi.ErrKnowledgeBaseNameConflict
		}
		return nil, fmt.Errorf("update knowledge base %d: %w", req.ID, err)
	}
	// 提交后写时 evict（失败仅 WARN，TTL 兜底）；快照的 DocumentCount 现读。
	s.evict(ctx, fmt.Sprintf(cacheKeyDetail, req.ID))
	counts, err := s.store.CountDocumentsByKBIDs(ctx, []uint64{req.ID})
	if err != nil {
		return nil, fmt.Errorf("count documents of kb %d: %w", req.ID, err)
	}
	schema := toKBSchema(kb, counts[req.ID])
	return &schema, nil
}

// Delete 级联真删：单事务 DeleteChunksByKB → DeleteDocumentsByKB → DeleteKnowledgeBase
//（决策修订：挡删退役——DELETE 即按用户意图收尾清空，误删兜底 PG 每日备份）；KB 行
// 0 行（不存在）→ 404 整笔回滚；agent_knowledge_bases 绑定由 FK CASCADE 清理。
func (s *kbService) Delete(ctx context.Context, req ragapi.DeleteKnowledgeBaseReq) error {
	if err := req.Validate(); err != nil {
		return fmt.Errorf("validate delete knowledge base: %w", err)
	}
	if err := s.store.WithTx(ctx, func(tx Store) error {
		if err := tx.DeleteChunksByKB(ctx, req.ID); err != nil {
			return fmt.Errorf("delete chunks of kb %d: %w", req.ID, err)
		}
		if err := tx.DeleteDocumentsByKB(ctx, req.ID); err != nil {
			return fmt.Errorf("delete documents of kb %d: %w", req.ID, err)
		}
		return tx.DeleteKnowledgeBase(ctx, req.ID)
	}); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ragapi.ErrKnowledgeBaseNotFound
		}
		return fmt.Errorf("delete knowledge base %d: %w", req.ID, err)
	}
	s.evict(ctx, fmt.Sprintf(cacheKeyDetail, req.ID)) // 删除同样失效缓存（防幽灵读到 TTL）
	return nil
}

// evict 提交后删缓存 key；失败仅 WARN 不影响业务（TTL 30min 兜底读到旧值的最坏窗口）。
func (s *kbService) evict(ctx context.Context, keys ...string) {
	for _, key := range keys {
		if err := s.cache.Delete(ctx, cache.NameRag, key); err != nil {
			slog.WarnContext(ctx, "evict rag cache failed; ttl fallback", "key", key, "err", err)
		}
	}
}

// UploadDocument 落 pending 行返回快照（status=pending + 元数据 + 原文）；本篇无
// dispatch 接线（07 补异步管线）。Validate 幂等重跑：handler 已归一化，跨模块直调由此兜底。
// 错误：ErrKnowledgeBaseNotFound（预检 404 或 23503 并发删除）。
func (s *kbService) UploadDocument(ctx context.Context, req ragapi.UploadDocumentReq) (*ragapi.DocumentSchema, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate upload document: %w", err)
	}
	if _, err := s.store.GetKnowledgeBaseByID(ctx, req.KnowledgeBaseID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ragapi.ErrKnowledgeBaseNotFound
		}
		return nil, fmt.Errorf("get knowledge base %d: %w", req.KnowledgeBaseID, err)
	}
	d := &Document{
		KnowledgeBaseID: req.KnowledgeBaseID,
		Name:            req.Name,
		Content:         req.Content,
		Status:          StatusPending,
		Enabled:         true, // 显式设值：布尔无 gorm default tag（provider 踩坑 #1）
		FileType:        req.FileType,
		FileSize:        req.FileSize,
	}
	if err := s.store.CreateDocument(ctx, d); err != nil {
		if isFKViolation(err) { // 预检后 KB 被并发删除
			return nil, ragapi.ErrKnowledgeBaseNotFound
		}
		return nil, fmt.Errorf("create document in kb %d: %w", req.KnowledgeBaseID, err)
	}
	// pending 恒 0 chunks / 空 error_message；DocumentCount 变化 → evict KB detail（澄清拍板）
	s.evict(ctx, fmt.Sprintf(cacheKeyDetail, req.KnowledgeBaseID))
	s.dispatch(ctx, d.ID) // 异步入库管线（spec 07 §5 接线；dispatchIngest 内部脱钩请求 ctx）
	schema := toDocumentSchema(d)
	return &schema, nil
}

// GetDocument 详情（含 Content 原文；chunk_count 直读列，非计数查询）。
func (s *kbService) GetDocument(ctx context.Context, req ragapi.GetDocumentReq) (*ragapi.DocumentDetailSchema, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate get document: %w", err)
	}
	d, err := s.store.GetDocumentByID(ctx, req.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ragapi.ErrDocumentNotFound
		}
		return nil, fmt.Errorf("get document %d: %w", req.ID, err)
	}
	return &ragapi.DocumentDetailSchema{DocumentSchema: toDocumentSchema(d), Content: d.Content}, nil
}

// ListDocuments KB 下全部文档游标分页（keyset id DESC，停用行可见——深度停用不是
// 删除）：先验 KB 归属；坏 cursor → ErrValidationFailed 包装（400）。
func (s *kbService) ListDocuments(ctx context.Context, req ragapi.ListDocumentsReq) (*ragapi.DocumentListResult, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate list documents: %w", err)
	}
	if _, err := s.store.GetKnowledgeBaseByID(ctx, req.KnowledgeBaseID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ragapi.ErrKnowledgeBaseNotFound
		}
		return nil, fmt.Errorf("get knowledge base %d: %w", req.KnowledgeBaseID, err)
	}
	p := page.NewCursor(req.Limit, req.Cursor)
	key, err := page.DecodeCursor[docCursorKey](p.Cursor) // 空 cursor = 首页零值
	if err != nil {
		return nil, fmt.Errorf("%w: 文档游标不合法: %v", errs.ErrValidationFailed, err)
	}
	docs, err := s.store.ListDocumentsByKB(ctx, req.KnowledgeBaseID, key.ID, p.FetchN())
	if err != nil {
		return nil, fmt.Errorf("list documents of kb %d: %w", req.KnowledgeBaseID, err)
	}
	res, err := page.NewCursorResult(docs, p.Limit, func(d Document) docCursorKey { return docCursorKey{ID: d.ID} })
	if err != nil {
		return nil, fmt.Errorf("build document list cursor: %w", err)
	}
	items := make([]ragapi.DocumentSchema, 0, len(res.Items)) // 空页返 [] 不返 null
	for i := range res.Items {
		items = append(items, toDocumentSchema(&res.Items[i]))
	}
	return &ragapi.DocumentListResult{Items: items, Limit: res.Limit, HasMore: res.HasMore, NextCursor: res.NextCursor}, nil
}

// DeleteDocument 真删：documents 行 + 全部 chunks 同事务硬删（不变量规则 2 的执行在
// store；内容不可恢复，兜底走 PG 备份）；evict 所属 KB 的 detail key（DocumentCount 变化）。
func (s *kbService) DeleteDocument(ctx context.Context, req ragapi.DeleteDocumentReq) error {
	if err := req.Validate(); err != nil {
		return fmt.Errorf("validate delete document: %w", err)
	}
	d, err := s.store.GetDocumentByID(ctx, req.ID) // 取 KB 归属（evict 目标）+ 区分 404
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ragapi.ErrDocumentNotFound
		}
		return fmt.Errorf("get document %d: %w", req.ID, err)
	}
	if err := s.store.HardDeleteDocument(ctx, req.ID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ragapi.ErrDocumentNotFound // 并发已删（RowsAffected=0）
		}
		return fmt.Errorf("hard delete document %d: %w", req.ID, err)
	}
	s.evict(ctx, fmt.Sprintf(cacheKeyDetail, d.KnowledgeBaseID))
	return nil
}

// DisableDocument 深度停用：事务内硬删全部向量分块 + enabled=false（内容与 status
// 保留，HNSW 内存即时回收）。store 严格状态机 0 行 → explainDocumentGuardMiss 重读
// 区分。不 evict——DocumentCount 含停用文档不变，detail 缓存仍准确。
func (s *kbService) DisableDocument(ctx context.Context, req ragapi.DisableDocumentReq) error {
	if err := req.Validate(); err != nil {
		return fmt.Errorf("validate disable document: %w", err)
	}
	if err := s.store.DisableDocument(ctx, req.ID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return s.explainDocumentGuardMiss(ctx, req.ID, false)
		}
		return fmt.Errorf("disable document %d: %w", req.ID, err)
	}
	return nil
}

// EnableDocument 重新启用：置 enabled=true + 重置 pending，提交后 dispatch 重跑入库
// 管线重建向量（停用时向量已物理删）。幂等路径（已启用）返回现快照且不重跑——
// 防误花 embedding 费。不 evict（DocumentCount 不变）。
func (s *kbService) EnableDocument(ctx context.Context, req ragapi.EnableDocumentReq) (*ragapi.DocumentSchema, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate enable document: %w", err)
	}
	err := s.store.EnableDocument(ctx, req.ID)
	switch {
	case err == nil:
		s.dispatch(ctx, req.ID) // 命中状态机：事务已提交，dispatch 重跑管线（内部脱钩请求 ctx）
	case errors.Is(err, gorm.ErrRecordNotFound):
		if miss := s.explainDocumentGuardMiss(ctx, req.ID, true); miss != nil {
			return nil, miss
		}
		// 已启用（幂等）：落到下方现读快照，不重跑管线
	default:
		return nil, fmt.Errorf("enable document %d: %w", req.ID, err)
	}
	fresh, err := s.store.GetDocumentByID(ctx, req.ID) // 现读快照（enable 路径行已 pending）
	if err != nil {
		return nil, fmt.Errorf("reload document %d after enable: %w", req.ID, err)
	}
	schema := toDocumentSchema(fresh)
	return &schema, nil
}

// explainDocumentGuardMiss 严格状态机 0 行后的重读区分（disable / enable 共用）：
// 不存在 → ErrDocumentNotFound；已处于目标态 → nil（幂等成功）；否则状态非终态
//（入库中撞并发）→ ErrDocumentProcessing。
func (s *kbService) explainDocumentGuardMiss(ctx context.Context, id uint64, wantEnabled bool) error {
	d, err := s.store.GetDocumentByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ragapi.ErrDocumentNotFound // 不存在（或并发已真删）
		}
		return fmt.Errorf("reload document %d after guard miss: %w", id, err)
	}
	if d.Enabled == wantEnabled {
		return nil // 已处于目标态：幂等成功
	}
	return fmt.Errorf("%w: 文档 %d 处于 %s 态", ragapi.ErrDocumentProcessing, id, d.Status)
}

// ReindexDocument 事务内删全部 chunks + 置 pending 重跑入库管线（不变量规则 3）；
// pending / processing 中挡并发重入；已停用文档须先启用（400——重跑会给停用文档
// 产 chunks，违反不变量规则 4）；返回重置后的真实快照。
func (s *kbService) ReindexDocument(ctx context.Context, req ragapi.ReindexDocumentReq) (*ragapi.DocumentSchema, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate reindex document: %w", err)
	}
	d, err := s.store.GetDocumentByID(ctx, req.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ragapi.ErrDocumentNotFound
		}
		return nil, fmt.Errorf("get document %d: %w", req.ID, err)
	}
	if !d.Enabled {
		return nil, fmt.Errorf("%w: 文档 %d 已停用，请先启用", errs.ErrValidationFailed, req.ID)
	}
	if d.Status == StatusPending || d.Status == StatusProcessing {
		return nil, fmt.Errorf("%w: 文档 %d 处于 %s 态", ragapi.ErrDocumentProcessing, req.ID, d.Status)
	}
	if err := s.store.WithTx(ctx, func(tx Store) error {
		if err := tx.DeleteChunksByDocument(ctx, req.ID); err != nil {
			return fmt.Errorf("delete chunks of document %d: %w", req.ID, err)
		}
		return tx.ResetDocumentForReindex(ctx, req.ID)
	}); err != nil {
		return nil, fmt.Errorf("reindex document %d: %w", req.ID, err)
	}
	fresh, err := s.store.GetDocumentByID(ctx, req.ID) // 重读快照（事务提交后的真实状态）
	if err != nil {
		return nil, fmt.Errorf("reload document %d after reindex: %w", req.ID, err)
	}
	s.dispatch(ctx, req.ID) // 重跑入库管线（spec 07 §5 接线）
	schema := toDocumentSchema(fresh)
	return &schema, nil
}

// Retrieve 检索编排（spec 05 §1 七步）：校验 → 逐 KB 直读预检（任一 404 → NotFound；
// disabled 静默剔除）→ 嵌入模型一致性 → ResolveLLMConfig + query 向量化（llm 哨兵
// 原样上抛）→ 维度校验 → 单表 ANN 召回 → 引用名解析组装（Similarity = 1 - Distance）。
func (s *kbService) Retrieve(ctx context.Context, req ragapi.RetrieveReq) ([]ragapi.RetrievedChunk, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate retrieve: %w", err)
	}
	// ① 逐 KB store 直读（不走 api Get 缓存——UploadDocument 预检同款）；② disabled
	// 静默剔除：管理员下架某库，绑它的 Agent 用剩余库继续工作，不报错。
	kbs := make([]*KnowledgeBase, 0, len(req.KBIDs))
	for _, id := range req.KBIDs {
		kb, err := s.store.GetKnowledgeBaseByID(ctx, id)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, ragapi.ErrKnowledgeBaseNotFound
			}
			return nil, fmt.Errorf("get knowledge base %d: %w", id, err)
		}
		if !kb.Enabled {
			continue
		}
		kbs = append(kbs, kb)
	}
	if len(kbs) == 0 {
		return []ragapi.RetrievedChunk{}, nil // 初始化空 slice，不返 nil（信封 []）
	}
	// ③ 全部 KB 同一嵌入模型（向量空间可比性前提）。
	modelID := kbs[0].EmbeddingModelID
	kbIDs := make([]uint64, len(kbs))
	for i, kb := range kbs {
		if kb.EmbeddingModelID != modelID {
			return nil, ragapi.ErrEmbeddingModelMismatch
		}
		kbIDs[i] = kb.ID
	}
	// ④ 明文凭据只在调用瞬间存在（ResolveLLMConfig → EmbedOptions，用后即弃，
	// 不入日志 / 缓存）。llm 哨兵（ErrEmbeddingUnsupported / ErrProviderBusy）与
	// RateLimited（*llm.Error）原样上抛，handler 负责映射。
	cfg, err := s.models.ResolveLLMConfig(ctx, providerapi.ResolveLLMConfigReq{ModelID: modelID})
	if err != nil {
		return nil, fmt.Errorf("resolve llm config for model %d: %w", modelID, err) // %w 保 provider 哨兵链
	}
	res, err := s.embeds.EmbedStrings(ctx, llm.EmbedOptions{
		Kind:    llm.ProviderKind(cfg.Kind),
		BaseURL: cfg.BaseURL,
		APIKey:  cfg.APIKey,
		Model:   cfg.ModelID,
		// 与管线 resolveEmbedOptions 同款：输出维度截断到向量列 1536（端到端实测
		// 回归——漏带时 Qwen3-Embedding 返原生 2560，步骤⑤维度校验 500）。
		Dimensions: ragapi.RequiredEmbeddingDim,
	}, []string{req.Query})
	if err != nil {
		return nil, err
	}
	// ⑤ 维度 ≠ 1536 = 服务端配置错（KB 建库预检过 dim，走到这说明配置漂移）——
	// 包装 errs.ErrInternal，细节只进日志不给前端。
	dim := 0
	if len(res.Vectors) > 0 {
		dim = len(res.Vectors[0])
	}
	if dim != ragapi.RequiredEmbeddingDim {
		return nil, fmt.Errorf("retrieve: embedding dim %d != %d: %w", dim, ragapi.RequiredEmbeddingDim, errs.ErrInternal)
	}
	// ⑥ TopK=0 取默认（cfg.TopK）并 clamp 到 [TopKMin, TopKMax]（binding 只护 HTTP
	// 路径，跨模块调用方不经 binding，clamp 是 service 层防线）。
	topK := req.TopK
	if topK == 0 {
		topK = s.cfg.TopK
	}
	topK = max(ragapi.TopKMin, min(ragapi.TopKMax, topK))
	hits, err := s.store.SearchChunks(ctx, kbIDs, res.Vectors[0], topK, s.cfg.EFSearch)
	if err != nil {
		return nil, fmt.Errorf("search chunks in kbs %v: %w", kbIDs, err)
	}
	// ⑦ 引用名解析（二次小查询，去重后 ≤ top-k 行）+ 组装。
	chunks := make([]ragapi.RetrievedChunk, 0, len(hits)) // 空 slice 非 nil
	if len(hits) == 0 {
		return chunks, nil
	}
	docIDs := make([]uint64, 0, len(hits))
	seen := make(map[uint64]struct{}, len(hits))
	for _, h := range hits {
		if _, ok := seen[h.DocumentID]; !ok {
			seen[h.DocumentID] = struct{}{}
			docIDs = append(docIDs, h.DocumentID)
		}
	}
	metas, err := s.store.GetDocumentMetasByIDs(ctx, docIDs)
	if err != nil {
		return nil, fmt.Errorf("get document metas %v: %w", docIDs, err)
	}
	for _, h := range hits {
		chunks = append(chunks, ragapi.RetrievedChunk{
			ChunkID:         strconv.FormatUint(h.ID, 10),
			DocumentID:      strconv.FormatUint(h.DocumentID, 10),
			KnowledgeBaseID: strconv.FormatUint(h.KnowledgeBaseID, 10),
			DocumentName:    metas[h.DocumentID], // 悬空（文档已删）无键 = ""
			ChunkIndex:      h.ChunkIndex,
			Content:         h.Content,
			Similarity:      1 - h.Distance, // 余弦距离 → 相似度（越大越近）
		})
	}
	return chunks, nil
}

// toKBSchema KB model → schema（id / 外键字符串化——JS 2^53 精度保护）。
func toKBSchema(kb *KnowledgeBase, documentCount int64) ragapi.KnowledgeBaseSchema {
	s := ragapi.KnowledgeBaseSchema{
		Name:             kb.Name,
		Description:      kb.Description,
		EmbeddingModelID: strconv.FormatUint(kb.EmbeddingModelID, 10),
		ChunkStrategy: ragapi.ChunkStrategy{
			Type:         kb.ChunkStrategy.Type,
			ChunkSize:    kb.ChunkStrategy.ChunkSize,
			ChunkOverlap: kb.ChunkStrategy.ChunkOverlap,
			Separator:    kb.ChunkStrategy.Separator,
		},
		Enabled:       kb.Enabled,
		DocumentCount: documentCount,
	}
	s.ID = strconv.FormatUint(kb.ID, 10)
	s.CreatedAt = kb.CreatedAt
	s.UpdatedAt = kb.UpdatedAt
	return s
}

// docCursorKey 文档列表游标排序键（messages 同款单列 id；id DESC 下决胜列即排序列）。
type docCursorKey struct {
	ID uint64 `json:"i"`
}

// toDocumentSchema Document model → schema（id 字符串化；Content 原文不进列表 / 快照 schema，
// 仅 DocumentDetailSchema 携带）。
func toDocumentSchema(d *Document) ragapi.DocumentSchema {
	s := ragapi.DocumentSchema{
		Name:         d.Name,
		FileType:     d.FileType,
		FileSize:     d.FileSize,
		Status:       d.Status,
		Enabled:      d.Enabled,
		ChunkCount:   d.ChunkCount,
		ErrorMessage: d.ErrorMessage,
	}
	s.ID = strconv.FormatUint(d.ID, 10)
	s.CreatedAt = d.CreatedAt
	s.UpdatedAt = d.UpdatedAt
	return s
}
