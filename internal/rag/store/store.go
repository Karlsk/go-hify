// Package store 是 rag 模块的数据层：实现 ragsvc.Store（GORM CRUD；pgvector 召回的
// db.Raw 在 05 增补），只操作本模块声明的表（knowledge_bases / documents /
// document_chunks）。错误原样上抛，业务翻译在 service 层。
package store

import (
	"context"
	"fmt"

	"github.com/pgvector/pgvector-go"
	"gorm.io/gorm"

	"github.com/Karlsk/go-hify/internal/platform/page"
	ragsvc "github.com/Karlsk/go-hify/internal/rag/service"
)

// 显式列清单（禁 SELECT *）。content 是大文本列（自动 TOAST），仅详情路径取——
// 列表 / 快照路径不取，零 TOAST 解压成本。
const (
	selectKB = "id, name, description, embedding_model_id, enabled, created_at, updated_at"
	// selectDocument 列表列（不含 content）；deleted_at 一并取回（软删 mixin 字段，可见行恒为 NULL）。
	selectDocument = "id, knowledge_base_id, name, status, file_type, file_size, error_message, chunk_count, created_at, updated_at, deleted_at"
	// selectDocumentDetail 详情列 = 列表列 + content 原文。
	selectDocumentDetail = "id, knowledge_base_id, name, content, status, file_type, file_size, error_message, chunk_count, created_at, updated_at, deleted_at"
)

// Store 实现 ragsvc.Store。
type Store struct{ db *gorm.DB }

// New 创建 Store。
func New(db *gorm.DB) *Store { return &Store{db: db} }

// 编译期断言：Store 实现了 service.Store 接口。
var _ ragsvc.Store = (*Store)(nil)

// ---- knowledge_bases ----

// CreateKnowledgeBase 插入 KB（id / created_at / updated_at 由 DB 生成并经 RETURNING 回填）。
func (s *Store) CreateKnowledgeBase(ctx context.Context, kb *ragsvc.KnowledgeBase) error {
	return s.db.WithContext(ctx).Create(kb).Error
}

// GetKnowledgeBaseByID 按主键查（KB 无软删）；未找到返回 gorm.ErrRecordNotFound。
func (s *Store) GetKnowledgeBaseByID(ctx context.Context, id uint64) (*ragsvc.KnowledgeBase, error) {
	var kb ragsvc.KnowledgeBase
	if err := s.db.WithContext(ctx).Select(selectKB).First(&kb, id).Error; err != nil {
		return nil, err
	}
	return &kb, nil
}

// ListKnowledgeBases 活跃 KB 偏移分页（id 升序，小配置表 OFFSET 例外）+ 可选 name
// ILIKE 模糊过滤。通配符拼接在 SQL 侧（'%' || ? || '%'），service 传原始关键字——
// 用户输入的 % / _ 不转义（spec 04 §1：模糊搜索本就宽松，小表豁免）。
func (s *Store) ListKnowledgeBases(ctx context.Context, p page.OffsetParams, name string) (page.OffsetResult[ragsvc.KnowledgeBase], error) {
	var (
		items []ragsvc.KnowledgeBase
		total int64
	)
	base := func() *gorm.DB {
		q := s.db.WithContext(ctx).Model(&ragsvc.KnowledgeBase{})
		if name != "" {
			q = q.Where("name ILIKE '%' || ? || '%'", name)
		}
		return q
	}
	if err := base().Count(&total).Error; err != nil {
		return page.OffsetResult[ragsvc.KnowledgeBase]{}, err
	}
	if err := p.Apply(base()).
		Select(selectKB).
		Order("id").
		Find(&items).Error; err != nil {
		return page.OffsetResult[ragsvc.KnowledgeBase]{}, err
	}
	return page.NewOffsetResult(items, p, total), nil
}

// UpdateKnowledgeBase 全量 Save（PUT 语义，零值一并覆盖）；updated_at 由 autoUpdateTime 维护。
// 前置：kb.ID 必须有效（service 层先 GetKnowledgeBaseByID 取得；0 行静默不命中由 uq 保护兜底）。
func (s *Store) UpdateKnowledgeBase(ctx context.Context, kb *ragsvc.KnowledgeBase) error {
	return s.db.WithContext(ctx).Save(kb).Error
}

// DeleteKnowledgeBase 硬删（KB 无软删；agent 绑定由 FK CASCADE 清理）；RowsAffected=0
// 返回 gorm.ErrRecordNotFound。
func (s *Store) DeleteKnowledgeBase(ctx context.Context, id uint64) error {
	res := s.db.WithContext(ctx).Delete(&ragsvc.KnowledgeBase{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// CountAllDocumentsByKB KB 下文档总数（Unscoped 含软删行——KB 删除护栏判据：有过
// 文档就不许硬删，软删行也占 uq 命名空间且可恢复）。
func (s *Store) CountAllDocumentsByKB(ctx context.Context, kbID uint64) (int64, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&ragsvc.Document{}).
		Unscoped().
		Where("knowledge_base_id = ?", kbID).
		Count(&n).Error
	return n, err
}

// CountDocumentsByKBIDs 列表聚合用批量计数（活跃文档）：GROUP BY + 聚合一条查询
// 覆盖当页全部 id（agent CountToolsByAgentIDs 同款；IN 而非 ANY——GORM 对 slice
// 参数按逗号展开，页大小 ≤100 在 IN 上限内）。map 无键 = 0。
func (s *Store) CountDocumentsByKBIDs(ctx context.Context, ids []uint64) (map[uint64]int64, error) {
	counts := make(map[uint64]int64, len(ids))
	if len(ids) == 0 {
		return counts, nil
	}
	var rows []struct {
		KnowledgeBaseID uint64
		Cnt             int64
	}
	err := s.db.WithContext(ctx).Model(&ragsvc.Document{}).
		Select("knowledge_base_id, COUNT(*) AS cnt").
		Where("knowledge_base_id IN ?", ids).
		Group("knowledge_base_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		counts[r.KnowledgeBaseID] = r.Cnt
	}
	return counts, nil
}

// ---- documents ----

// GetDocumentByID 按主键查（含 content 原文——大文本仅此详情路径取）；gorm.DeletedAt
// 自动过滤软删行；未找到返回 gorm.ErrRecordNotFound。
func (s *Store) GetDocumentByID(ctx context.Context, id uint64) (*ragsvc.Document, error) {
	var d ragsvc.Document
	if err := s.db.WithContext(ctx).Select(selectDocumentDetail).First(&d, id).Error; err != nil {
		return nil, err
	}
	return &d, nil
}

// CreateDocument 插入文档行（id / created_at / updated_at 由 DB 生成并经 RETURNING 回填）。
func (s *Store) CreateDocument(ctx context.Context, d *ragsvc.Document) error {
	return s.db.WithContext(ctx).Create(d).Error
}

// ListDocumentsByKB KB 下活跃文档 keyset 分页（id DESC；软删行被 DeletedAt 过滤）；
// beforeID=0 为首页（无 id < 条件）；limit 由调用方传 FetchN()（limit+1 判 has_more）。
func (s *Store) ListDocumentsByKB(ctx context.Context, kbID, beforeID uint64, limit int) ([]ragsvc.Document, error) {
	var items []ragsvc.Document
	q := s.db.WithContext(ctx).Model(&ragsvc.Document{}).Where("knowledge_base_id = ?", kbID)
	if beforeID > 0 {
		q = q.Where("id < ?", beforeID)
	}
	err := q.Select(selectDocument).
		Order("id DESC").
		Limit(limit).
		Find(&items).Error
	return items, err
}

// SoftDeleteDocument 事务（不变量规则 2，spec 01 §3）：软删 documents 行 + 同事务
// 硬删其全部 chunks（元信息软删保底，向量物理删省 HNSW 内存）。软删 0 行
// （不存在或已删）→ 整笔回滚 + gorm.ErrRecordNotFound，chunks 不被误删。
func (s *Store) SoftDeleteDocument(ctx context.Context, id uint64) error {
	return s.db.WithContext(ctx).Transaction(func(gtx *gorm.DB) error {
		res := gtx.Delete(&ragsvc.Document{}, id) // DeletedAt 改写为 UPDATE deleted_at
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return (&Store{db: gtx}).DeleteChunksByDocument(ctx, id)
	})
}

// DeleteChunksByDocument 硬删文档全部分块（规则 2 / 3 共用；0 行合法——pending 无
// chunks，ErrorsAffected 不判）。append-only 表无软删语义。
func (s *Store) DeleteChunksByDocument(ctx context.Context, documentID uint64) error {
	return s.db.WithContext(ctx).
		Where("document_id = ?", documentID).
		Delete(&ragsvc.DocumentChunk{}).Error
}

// ResetDocumentForReindex 重置文档供重跑入库管线（不变量规则 3）：status=pending +
// error_message='' + chunk_count=0（updated_at 由 autoUpdateTime 维护）；软删行不可见；
// RowsAffected=0 返回 gorm.ErrRecordNotFound。
func (s *Store) ResetDocumentForReindex(ctx context.Context, id uint64) error {
	res := s.db.WithContext(ctx).Model(&ragsvc.Document{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":        ragsvc.StatusPending,
			"error_message": "",
			"chunk_count":   0,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// WithTx 事务包装：fn 拿到包装了 tx 句柄的 Store（仍以 service.Store 接口身份传入）。
func (s *Store) WithTx(ctx context.Context, fn func(tx ragsvc.Store) error) error {
	return s.db.WithContext(ctx).Transaction(func(gtx *gorm.DB) error {
		return fn(&Store{db: gtx})
	})
}

// ---- 检索（spec 05） ----

// searchChunksSQL 跨 KB 单表 ANN 召回（spec 05 §2 冻结形态）：显式列 + 余弦距离；
// kbIDs IN 展开；embedding IS NOT NULL 纯防御（不变量：终态事务保证行必带向量）；
// 正确性不依赖 status/deleted_at 过滤——01 §3 不变量换来的简化。ORDER BY 用表达式
// 本体（与 vector_cosine_ops 索引配对铁律，改写 distance 别名会绕开索引）；向量参数
// 传两次（SELECT 列 + ORDER BY 各一）。
const searchChunksSQL = `SELECT id, document_id, knowledge_base_id, chunk_index, content, token_count, (embedding <=> ?) AS distance
FROM document_chunks
WHERE knowledge_base_id IN ?
  AND embedding IS NOT NULL
ORDER BY embedding <=> ?
LIMIT ?`

// GetDocumentMetasByIDs 批量取文档名（引用名解析，spec 05 §2）：只读 id/name 不碰
// content（大文本 TOAST 零成本）；软删 / 不存在的 id 无键（悬空 → ""）；空 ids 返回
// 空 map 不发 SQL（IN () 非法）。
func (s *Store) GetDocumentMetasByIDs(ctx context.Context, ids []uint64) (map[uint64]string, error) {
	metas := make(map[uint64]string, len(ids))
	if len(ids) == 0 {
		return metas, nil
	}
	var rows []struct {
		ID   uint64
		Name string
	}
	err := s.db.WithContext(ctx).Model(&ragsvc.Document{}).
		Select("id, name").
		Where("id IN ?", ids).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		metas[r.ID] = r.Name
	}
	return metas, nil
}

// SearchChunks 跨 KB 单表 ANN 召回（kbIDs ≤ MaxRetrieveKBs）：db.Transaction 包裹——
// 先 SET LOCAL hnsw.ef_search（SET 不支持绑定参数只能拼接；efSearch 来自服务端 config
// 已 clamp，非用户输入），再 Raw 执行召回（向量以 pgvector.Vector 传参，GORM 展开
// IN slice 与 ? → $N）。Scan 目标 ChunkHit，返回 service 前不解析名称。
func (s *Store) SearchChunks(ctx context.Context, kbIDs []uint64, query []float32, limit int, efSearch int) ([]ragsvc.ChunkHit, error) {
	v := pgvector.NewVector(query)
	var hits []ragsvc.ChunkHit
	err := s.db.WithContext(ctx).Transaction(func(gtx *gorm.DB) error {
		if err := gtx.Exec(fmt.Sprintf("SET LOCAL hnsw.ef_search = %d", efSearch)).Error; err != nil {
			return err
		}
		return gtx.Raw(searchChunksSQL, v, kbIDs, v, limit).Scan(&hits).Error
	})
	if err != nil {
		return nil, err
	}
	return hits, nil
}

// ---- 管线（spec 07） ----

// MarkDocumentProcessing pending→processing 翻转（严格状态机：WHERE 含 status='pending'
// 前置条件）；RowsAffected=0（非 pending / 不存在）返回 gorm.ErrRecordNotFound——
// 管线中止。updated_at 由 autoUpdateTime 维护。
func (s *Store) MarkDocumentProcessing(ctx context.Context, id uint64) error {
	res := s.db.WithContext(ctx).Model(&ragsvc.Document{}).
		Where("id = ?", id).
		Where("status = ?", ragsvc.StatusPending).
		Update("status", ragsvc.StatusProcessing)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// MarkDocumentReady processing→ready 终态（严格状态机：WHERE 含 status='processing'
// 前置条件），chunk_count 与 ready 原子同写（不变量规则 1：终态事务内调用）；0 行
// 返回 gorm.ErrRecordNotFound——事务回滚，已插入的 chunks 不落孤儿。
func (s *Store) MarkDocumentReady(ctx context.Context, id uint64, chunkCount int) error {
	res := s.db.WithContext(ctx).Model(&ragsvc.Document{}).
		Where("id = ?", id).
		Where("status = ?", ragsvc.StatusProcessing).
		Updates(map[string]any{
			"status":      ragsvc.StatusReady,
			"chunk_count": chunkCount,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// MarkDocumentFailed 置 failed + error_message（WHERE status IN ('pending','processing')
// ——覆盖 pending 态失败与 Recovery 扫描）；0 行返回 nil 静默：markFailed 是尽力而为
// 的最后一步，文档已终态 / 已删不构成错误路径。
func (s *Store) MarkDocumentFailed(ctx context.Context, id uint64, message string) error {
	res := s.db.WithContext(ctx).Model(&ragsvc.Document{}).
		Where("id = ?", id).
		Where("status IN ?", []string{ragsvc.StatusPending, ragsvc.StatusProcessing}).
		Updates(map[string]any{
			"status":        ragsvc.StatusFailed,
			"error_message": message,
		})
	return res.Error // 0 行静默（spec 07 §3 拍板）
}

// CreateChunks 批量插入分块：db.Create 切片 = 单语句多 VALUES（仓规批量写）；空切片
// 直返防御（终态事务逐批调用，空批不发 INSERT）。append-only 表无 updated_at。
func (s *Store) CreateChunks(ctx context.Context, cs []ragsvc.DocumentChunk) error {
	if len(cs) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Create(&cs).Error
}

// ListIngestingDocuments 扫全部入库中文档（Recovery 用，spec 07 §4）：WHERE status IN
// ('pending','processing') 命中 partial idx idx_documents_ingesting；软删行被
// DeletedAt 过滤；无分页（单实例内部工具量级可控）。
func (s *Store) ListIngestingDocuments(ctx context.Context) ([]ragsvc.Document, error) {
	var docs []ragsvc.Document
	err := s.db.WithContext(ctx).Model(&ragsvc.Document{}).
		Where("status IN ?", []string{ragsvc.StatusPending, ragsvc.StatusProcessing}).
		Select(selectDocument).
		Find(&docs).Error
	return docs, err
}
