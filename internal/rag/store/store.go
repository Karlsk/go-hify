// Package store 是 rag 模块的数据层：实现 ragsvc.Store（GORM CRUD；pgvector 召回的
// db.Raw 在 05 增补），只操作本模块声明的表（knowledge_bases / documents /
// document_chunks）。错误原样上抛，业务翻译在 service 层。
package store

import (
	"context"

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
