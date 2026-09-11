// pipeline.go —— 入库管线（spec 07）：dispatch 调度 + processDocument 状态机串联
// + Recovery 服务重启自愈。DB 行即持久队列（无消息队列）；不变量规则 1（终态事务，
// spec 01 §3）的执行者。事务纪律：embedding 永不进事务，终态事务只含纯 DB 写。
package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Karlsk/go-hify/internal/platform/llm"
	providerapi "github.com/Karlsk/go-hify/internal/provider/api"
	"github.com/Karlsk/go-hify/internal/rag/api"
	"github.com/pgvector/pgvector-go"
)

// ingestAcquireWait 抢槽超时（spec 07 §1）：拿不到并发槽 fail-fast 置 failed
// 「系统繁忙」——排队放大超时级联，等 30 秒再成功不如立刻报错让用户重试。
const ingestAcquireWait = 2 * time.Second

// errMessageMaxLen error_message 截断上限（spec 07 §2 环节 9）：防超长错误链
// 撑爆 documents.error_message 列（text 但纪律仍截断）。
const errMessageMaxLen = 500

// dispatchIngest 异步入库调度（spec 07 §1）：context.WithoutCancel 脱钩请求 ctx
// （202 返回后请求 ctx 即取消，直接透传会让 embedding 立刻 abort——最易写错处）
// 但保留 trace 值；semaphore.Weighted 限并发文档数（cfg.IngestConcurrency，
// 默认 2），抢槽 acquireWait 超时置 failed「系统繁忙」；拿到槽后执行
// processDocument，槽位在函数返回时释放。
func (s *kbService) dispatchIngest(ctx context.Context, docID uint64) {
	pctx := context.WithoutCancel(ctx)
	go func() {
		acquireCtx, cancel := context.WithTimeout(pctx, s.acquireWait)
		defer cancel()
		if err := s.sem.Acquire(acquireCtx, 1); err != nil {
			s.markFailed(pctx, docID, "系统繁忙，请稍后重试")
			return
		}
		defer s.sem.Release(1)
		s.processDocument(pctx, docID)
	}()
}

// processDocument 管线主体（spec 07 §2 九环节串联 + 状态管理）；环节逻辑在独立
// 私有函数（loadDocument / resolveEmbedOptions / embedChunks / buildDocumentChunks
// / commitReady），本函数 <50 行。
func (s *kbService) processDocument(ctx context.Context, docID uint64) {
	// 环节 1：panic 兜底（spec 07 §2 环节 1）——未捕获 panic 置 failed 而非静默消失。
	defer func() {
		if r := recover(); r != nil {
			s.markFailed(ctx, docID, fmt.Sprintf("internal panic: %v", r))
		}
	}()

	// 环节 2：loadDocument——not found 只记日志返回（可能已被删）。
	doc, err := s.store.GetDocumentByID(ctx, docID)
	if err != nil {
		slog.ErrorContext(ctx, "pipeline: loadDocument failed", "document_id", docID, "err", err)
		return
	}

	// 环节 3：MarkDocumentProcessing（严格状态机 WHERE status='pending'）。
	if err := s.store.MarkDocumentProcessing(ctx, docID); err != nil {
		slog.ErrorContext(ctx, "pipeline: MarkDocumentProcessing failed", "document_id", docID, "err", err)
		return
	}

	// 环节 4：extractText（TXT/MD 纯文本直取，二进制拒之门外已在 UploadDocument.Validate）。
	text, err := extractText(doc)
	if err != nil {
		slog.ErrorContext(ctx, "pipeline: extractText failed", "document_id", docID, "err", err)
		return
	}

	// 环节 5：SplitChunks（递归段落→句子→硬截三级降级，MD 围栏原子保护）。
	rawChunks := SplitChunks(text, s.cfg.ChunkSize, s.cfg.ChunkOverlap)
	if len(rawChunks) == 0 {
		s.markFailed(ctx, docID, "文档内容为空")
		return
	}

	// 环节 6：resolveEmbedOptions（ResolveLLMConfig → 明文凭据瞬间存在，用后即弃）。
	embedOpts, err := s.resolveEmbedOptions(ctx, doc.KnowledgeBaseID)
	if err != nil {
		slog.ErrorContext(ctx, "pipeline: resolveEmbedOptions failed", "document_id", docID, "err", err)
		return
	}

	// 环节 7：embedChunks（按 EmbedBatchSize 分批 + 每向量校验 len==1536 + 累计
	// prompt_tokens）。embedding 永不进事务——超时/重试/熔断在 platform/llm 内。
	vectors, promptTokens, err := s.embedChunks(ctx, embedOpts, rawChunks)
	if err != nil {
		slog.ErrorContext(ctx, "pipeline: embedChunks failed", "document_id", docID, "err", err)
		return
	}

	// 环节 8：buildDocumentChunks（ChunkIndex 从 1 开始 + KnowledgeBaseID 冗余 +
	// TokenCount=estimateTokens）。
	chunks := buildDocumentChunks(docID, doc.KnowledgeBaseID, rawChunks, vectors)

	// 环节 9：commitReady（终态事务原子：CreateChunks + MarkDocumentReady）。
	if err := s.commitReady(ctx, docID, chunks, promptTokens); err != nil {
		slog.ErrorContext(ctx, "pipeline: commitReady failed", "document_id", docID, "err", err)
		return
	}
}

// resolveEmbedOptions 环节 6：ResolveLLMConfig → EmbedOptions（明文凭据
// 用后即弃，不入日志/缓存）。KB 的 embedding_model_id 绑定嵌入模型——
// Retrieve 与 Pipeline 共用同一条 resolve 路径。
func (s *kbService) resolveEmbedOptions(ctx context.Context, kbID uint64) (llm.EmbedOptions, error) {
	kb, err := s.store.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return llm.EmbedOptions{}, fmt.Errorf("load kb %d for embed opts: %w", kbID, err)
	}
	cfg, err := s.models.ResolveLLMConfig(ctx, providerapi.ResolveLLMConfigReq{ModelID: kb.EmbeddingModelID})
	if err != nil {
		return llm.EmbedOptions{}, fmt.Errorf("resolve llm config for model %d: %w", kb.EmbeddingModelID, err)
	}
	return llm.EmbedOptions{
		Kind:    llm.ProviderKind(cfg.Kind),
		BaseURL: cfg.BaseURL,
		APIKey:  cfg.APIKey,
		Model:   cfg.ModelID,
	}, nil
}

// embedChunks 环节 7：按 EmbedBatchSize 分批调用 EmbedStrings + 每向量校验
// len==1536 + 累计 prompt_tokens。embedding 永不进事务（超时/重试/熔断在
// platform/llm 内）。
func (s *kbService) embedChunks(ctx context.Context, opts llm.EmbedOptions, texts []string) ([][]float32, int, error) {
	var allVecs [][]float32
	var totalPromptTokens int
	batchSize := s.cfg.EmbedBatchSize
	if batchSize <= 0 {
		batchSize = 10
	}
	for i := 0; i < len(texts); i += batchSize {
		end := min(i+batchSize, len(texts))
		batch := texts[i:end]
		res, err := s.embeds.EmbedStrings(ctx, opts, batch)
		if err != nil {
			return nil, 0, fmt.Errorf("embed batch [%d:%d]: %w", i, end, err)
		}
		// 环节 7b：每向量维度校验（spec 07 §2 环节 7）——建库预检过 dim，
		// 走到这说明配置漂移；包装 errs.ErrInternal，细节只进日志。
		for j, v := range res.Vectors {
			if len(v) != api.RequiredEmbeddingDim {
				return nil, 0, fmt.Errorf("embed chunk %d: dim %d != %d", i+j, len(v), api.RequiredEmbeddingDim)
			}
		}
		allVecs = append(allVecs, res.Vectors...)
		totalPromptTokens += res.PromptTokens
	}
	return allVecs, totalPromptTokens, nil
}

// buildDocumentChunks 环节 8：raw 内容 + 向量 → DocumentChunk 切片（ChunkIndex
// 从 1 开始、KnowledgeBaseID 冗余、TokenCount=estimateTokens）。
func buildDocumentChunks(docID, kbID uint64, texts []string, vectors [][]float32) []DocumentChunk {
	chunks := make([]DocumentChunk, len(texts))
	for i, text := range texts {
		chunks[i] = DocumentChunk{
			DocumentID:      docID,
			KnowledgeBaseID: kbID,
			ChunkIndex:      i + 1, // 从 1 开始（spec 07 §2 环节 8）
			Content:         text,
			TokenCount:      estimateTokens(text),
			Embedding:       pgvector.NewVector(vectors[i]),
		}
	}
	return chunks
}

// commitReady 环节 9：终态事务原子（spec 07 §2 环节 9 + 不变量规则 1）——CreateChunks
// 逐批写入（每批 ≤ EmbedBatchSize）+ MarkDocumentReady（status + chunk_count 原子）。
// 事务内无外部调用（纯 DB 写）。
func (s *kbService) commitReady(ctx context.Context, docID uint64, chunks []DocumentChunk, promptTokens int) error {
	return s.store.WithTx(ctx, func(tx Store) error {
		// 逐批写入（每批 ≤ EmbedBatchSize，仓规批量写多 VALUES INSERT）。
		batchSize := s.cfg.EmbedBatchSize
		if batchSize <= 0 {
			batchSize = 10
		}
		for i := 0; i < len(chunks); i += batchSize {
			end := min(i+batchSize, len(chunks))
			if err := tx.CreateChunks(ctx, chunks[i:end]); err != nil {
				return fmt.Errorf("create chunks batch [%d:%d]: %w", i, end, err)
			}
		}
		// MarkDocumentReady（严格状态机 WHERE status='processing'）；
		// RowsAffected=0 终态事务回滚，chunks 不落孤儿。
		if err := tx.MarkDocumentReady(ctx, docID, len(chunks)); err != nil {
			return fmt.Errorf("mark document ready: %w", err)
		}
		return nil
	})
}

// markFailed 统一失败出口（spec 07 §2 环节 9）：MarkDocumentFailed（尽力而为，
// 0 行静默语义在 store 层）+ 消息截断 500 + slog.ErrorContext 记 err 链。
func (s *kbService) markFailed(ctx context.Context, docID uint64, msg string) {
	if err := s.store.MarkDocumentFailed(ctx, docID, truncateRunes(msg, errMessageMaxLen)); err != nil {
		slog.ErrorContext(ctx, "mark document failed", "document_id", docID, "err", err)
	}
}

// truncateRunes 按 rune 截断到 n（多字节字符不撕裂字节；n 超过全长原样返回）。
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// Recovery 服务重启自愈（spec 07 §4）：扫残留 pending/processing 置 failed
// （error_message=「服务重启中断，请重新索引」）；由组合根在启动时调用
// （08 装配），返回 error 仅记 WARN 不阻断启动。不自动重跑——reindex 是显式
// 用户动作；终态事务原子性保证残留文档恒无 chunks，无需清理向量。
type Recovery struct {
	svc *kbService
}

// restartFailMessage 服务重启中断的统一失败消息（spec 07 §4）。
const restartFailMessage = "服务重启中断，请重新索引"

// MarkInterruptedFailed 扫描全部入库中文档（ListIngestingDocuments）→ 逐个
// MarkDocumentFailed。尽力而为：单个失败不阻断其余文档；返回首个非 nil error
// 供调用方记 WARN。
func (r *Recovery) MarkInterruptedFailed(ctx context.Context) error {
	docs, err := r.svc.store.ListIngestingDocuments(ctx)
	if err != nil {
		return fmt.Errorf("list ingesting documents: %w", err)
	}
	var firstErr error
	for _, doc := range docs {
		if err := r.svc.store.MarkDocumentFailed(ctx, doc.ID, restartFailMessage); err != nil {
			slog.ErrorContext(ctx, "recovery: mark document failed", "document_id", doc.ID, "err", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}
