// Package handler 是 rag 模块的 HTTP 层（spec 04 端点 1-10 + spec 05 端点 11 retrieve）：
// gin 路由与绑定。薄绑定，无业务逻辑——参数校验（binding tag + Validate）、调本模块
// api 接口、哨兵 errors.Is → 状态码、respond 信封包装。
package handler

import (
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Karlsk/go-hify/internal/platform/errs"
	"github.com/Karlsk/go-hify/internal/platform/llm"
	"github.com/Karlsk/go-hify/internal/platform/respond"
	providerapi "github.com/Karlsk/go-hify/internal/provider/api"
	ragapi "github.com/Karlsk/go-hify/internal/rag/api"
)

// Handler 持本模块 api 接口（组合根注入 service 实现）+ 上传大小上限（auth handler
// 持 CookieSecure 先例；来自 RagCfg.MaxUploadBytes，08 组合根注入）。
type Handler struct {
	kbs            ragapi.KnowledgeBaseService
	maxUploadBytes int
}

// New 创建 Handler。
func New(kbs ragapi.KnowledgeBaseService, maxUploadBytes int) *Handler {
	return &Handler{kbs: kbs, maxUploadBytes: maxUploadBytes}
}

// RegisterRoutes 两组路由分开注册（spec 04 §3）：/knowledge-bases/:id/... 与
// /documents/:id——组内无 :id 与静态段同级冲突。
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	kbs := rg.Group("/knowledge-bases")
	kbs.POST("", h.createKB)
	kbs.GET("", h.listKBs)
	kbs.GET("/:id", h.getKB)
	kbs.PUT("/:id", h.updateKB)
	kbs.DELETE("/:id", h.deleteKB)
	kbs.POST("/:id/documents", h.uploadDocument)
	kbs.GET("/:id/documents", h.listDocuments)
	kbs.POST("/:id/retrieve", h.retrieve)

	docs := rg.Group("/documents")
	docs.GET("/:id", h.getDocument)
	docs.DELETE("/:id", h.deleteDocument)
	docs.POST("/:id/disable", h.disableDocument)
	docs.POST("/:id/enable", h.enableDocument)
	docs.POST("/:id/reindex", h.reindexDocument)
}

// ---- knowledge_bases（端点 1-5） ----

// createKB 建库（模型预检在 service：非 embedding / dim≠1536 → 400，模型 404 / 503）。
func (h *Handler) createKB(c *gin.Context) {
	var req ragapi.CreateKnowledgeBaseReq
	if !respond.BindJSON(c, &req) {
		return
	}
	s, err := h.kbs.Create(c.Request.Context(), req)
	if err != nil {
		failRag(c, err)
		return
	}
	respond.Created(c, s)
}

// listKBs 偏移分页（小配置表例外）+ name ILIKE 模糊过滤透传。
func (h *Handler) listKBs(c *gin.Context) {
	var req ragapi.ListKnowledgeBasesReq
	if !respond.BindQuery(c, &req) {
		return
	}
	res, err := h.kbs.List(c.Request.Context(), req)
	if err != nil {
		failRag(c, err)
		return
	}
	respond.OKWithOffset(c, res.Items, res.Page, res.PageSize, res.Total)
}

// getKB 详情（含 DocumentCount，Cache-Aside 在 service）。
func (h *Handler) getKB(c *gin.Context) {
	var req ragapi.GetKnowledgeBaseReq
	if !respond.BindUri(c, &req) {
		return
	}
	s, err := h.kbs.Get(c.Request.Context(), req)
	if err != nil {
		failRag(c, err)
		return
	}
	respond.OK(c, s)
}

// updateKB 整体更新（PUT 语义）：先绑路径 :id 再绑 body；UpdateKnowledgeBaseReq.ID
// 带 json:"-" 防 body 的 id 键覆盖路径值（provider 踩坑 #7）。
func (h *Handler) updateKB(c *gin.Context) {
	var idReq ragapi.GetKnowledgeBaseReq
	if !respond.BindUri(c, &idReq) {
		return
	}
	var req ragapi.UpdateKnowledgeBaseReq
	req.ID = idReq.ID
	if !respond.BindJSON(c, &req) {
		return
	}
	s, err := h.kbs.Update(c.Request.Context(), req)
	if err != nil {
		failRag(c, err)
		return
	}
	respond.OK(c, s)
}

// deleteKB 级联真删（204 无返回体）：单事务清空 KB 下全部文档与分块后删 KB 行
//（挡删已退役）；agent 绑定由 FK CASCADE 清理。
func (h *Handler) deleteKB(c *gin.Context) {
	var req ragapi.DeleteKnowledgeBaseReq
	if !respond.BindUri(c, &req) {
		return
	}
	if err := h.kbs.Delete(c.Request.Context(), req); err != nil {
		failRag(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ---- documents（端点 6-10） ----

// uploadDocument 上传文档（spec 04 §3：唯一无 BindJSON 先例的端点）：
// BindUri → ContentLength 预检 → FormFile → io.LimitReader 读（超限 400）→
// PostForm("name") → 扩展名得 file_type、实读字节数得 file_size → 程序内构造 req →
// Validate 失败手写 400 信封 → 202 Accepted（不是 201——异步入库语义）。
func (h *Handler) uploadDocument(c *gin.Context) {
	var idReq ragapi.GetKnowledgeBaseReq
	if !respond.BindUri(c, &idReq) {
		return
	}
	if c.Request.ContentLength > int64(h.maxUploadBytes) {
		respond.Fail(c, http.StatusBadRequest, errs.ErrValidationFailed.Error(), "上传文件超过大小上限")
		return
	}
	fh, err := c.FormFile("file")
	if err != nil {
		respond.Fail(c, http.StatusBadRequest, errs.ErrValidationFailed.Error(), "缺少 file 字段")
		return
	}
	f, err := fh.Open()
	if err != nil {
		respond.Fail(c, http.StatusBadRequest, errs.ErrValidationFailed.Error(), "文件不可读")
		return
	}
	defer f.Close() //nolint:errcheck // 只读句柄，Close 失败无需处理
	// 读上限+1 字节：多读 1 字节即判定超限——ContentLength 是整个 multipart 报文
	//（含边界开销）且可缺失（-1），真正的文件大小上限由此兜底。
	buf, err := io.ReadAll(io.LimitReader(f, int64(h.maxUploadBytes)+1))
	if err != nil {
		respond.Fail(c, http.StatusBadRequest, errs.ErrValidationFailed.Error(), "读取文件失败")
		return
	}
	if len(buf) > h.maxUploadBytes {
		respond.Fail(c, http.StatusBadRequest, errs.ErrValidationFailed.Error(), "上传文件超过大小上限")
		return
	}
	req := ragapi.UploadDocumentReq{
		KnowledgeBaseID: idReq.ID,
		FileName:        fh.Filename,
		Name:            c.PostForm("name"),
		Content:         string(buf),
		FileType:        strings.TrimPrefix(strings.ToLower(filepath.Ext(fh.Filename)), "."),
		FileSize:        int64(len(buf)),
	}
	if err := req.Validate(); err != nil {
		respond.Fail(c, http.StatusBadRequest, errs.ErrValidationFailed.Error(), err.Error())
		return
	}
	d, err := h.kbs.UploadDocument(c.Request.Context(), req)
	if err != nil {
		failRag(c, err)
		return
	}
	respond.Accepted(c, d)
}

// listDocuments KB 下文档游标分页：先绑路径 :id（KB 归属），query 只绑 limit/cursor
//（KnowledgeBaseID 无 form tag，query 无法覆盖路径值）。
func (h *Handler) listDocuments(c *gin.Context) {
	var idReq ragapi.GetKnowledgeBaseReq
	if !respond.BindUri(c, &idReq) {
		return
	}
	req := ragapi.ListDocumentsReq{KnowledgeBaseID: idReq.ID}
	if !respond.BindQuery(c, &req) {
		return
	}
	res, err := h.kbs.ListDocuments(c.Request.Context(), req)
	if err != nil {
		failRag(c, err)
		return
	}
	respond.OKWithCursor(c, res.Items, res.Limit, res.HasMore, res.NextCursor)
}

// getDocument 文档详情（含 Content 原文 + chunk_count + error_message）。
func (h *Handler) getDocument(c *gin.Context) {
	var req ragapi.GetDocumentReq
	if !respond.BindUri(c, &req) {
		return
	}
	d, err := h.kbs.GetDocument(c.Request.Context(), req)
	if err != nil {
		failRag(c, err)
		return
	}
	respond.OK(c, d)
}

// deleteDocument 真删：文档行 + 全部 chunks 同事务硬删（不变量规则 2，204 无返回体；
// 内容不可恢复，兜底走 PG 备份）。
func (h *Handler) deleteDocument(c *gin.Context) {
	var req ragapi.DeleteDocumentReq
	if !respond.BindUri(c, &req) {
		return
	}
	if err := h.kbs.DeleteDocument(c.Request.Context(), req); err != nil {
		failRag(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// disableDocument 深度停用（204 同步返回：事务硬删全部向量分块 + enabled=false，
// 内容保留）；入库中撞并发 → 409；已停用幂等成功。
func (h *Handler) disableDocument(c *gin.Context) {
	var req ragapi.DisableDocumentReq
	if !respond.BindUri(c, &req) {
		return
	}
	if err := h.kbs.DisableDocument(c.Request.Context(), req); err != nil {
		failRag(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// enableDocument 重新启用（202：置 pending + 异步重跑入库管线重建向量）；已启用
// 幂等返回现快照且不重跑。
func (h *Handler) enableDocument(c *gin.Context) {
	var req ragapi.EnableDocumentReq
	if !respond.BindUri(c, &req) {
		return
	}
	d, err := h.kbs.EnableDocument(c.Request.Context(), req)
	if err != nil {
		failRag(c, err)
		return
	}
	respond.Accepted(c, d)
}

// reindexDocument 重建索引（202：事务删 chunks + 置 pending 重跑管线）；已停用
// 文档 → 400 须先启用；pending/processing 撞并发 → 409。
func (h *Handler) reindexDocument(c *gin.Context) {
	var req ragapi.ReindexDocumentReq
	if !respond.BindUri(c, &req) {
		return
	}
	d, err := h.kbs.ReindexDocument(c.Request.Context(), req)
	if err != nil {
		failRag(c, err)
		return
	}
	respond.Accepted(c, d)
}

// failRag 映射 rag 侧业务哨兵。KB 操作也可能翻出 provider 哨兵（建库撞不存在 /
// 停用的嵌入模型）、retrieve 翻出 llm 哨兵（spec 05 §3：Unsupported 400 / Busy 503 /
// RateLimited 429）——service 透传，此处一并映射（agent failAgent 同款；rag →
// provider 是白名单依赖方向且只 import api 包）。errs.ErrValidationFailed 等通用
// 哨兵由兜底 FailFromSentinel 自动映射。
func failRag(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ragapi.ErrKnowledgeBaseNotFound):
		respond.Fail(c, http.StatusNotFound, ragapi.ErrKnowledgeBaseNotFound.Error(), "知识库不存在")
	case errors.Is(err, ragapi.ErrKnowledgeBaseNameConflict):
		respond.Fail(c, http.StatusConflict, ragapi.ErrKnowledgeBaseNameConflict.Error(), "知识库名称已存在")
	case errors.Is(err, ragapi.ErrEmbeddingDimMismatch):
		respond.Fail(c, http.StatusBadRequest, ragapi.ErrEmbeddingDimMismatch.Error(), "嵌入模型须为 embedding 能力且维度 1536")
	case errors.Is(err, ragapi.ErrEmbeddingModelMismatch):
		respond.Fail(c, http.StatusBadRequest, ragapi.ErrEmbeddingModelMismatch.Error(), "所选知识库绑定了不同的嵌入模型")
	case errors.Is(err, ragapi.ErrDocumentNotFound):
		respond.Fail(c, http.StatusNotFound, ragapi.ErrDocumentNotFound.Error(), "文档不存在")
	case errors.Is(err, ragapi.ErrDocumentProcessing):
		respond.Fail(c, http.StatusConflict, ragapi.ErrDocumentProcessing.Error(), "文档正在入库，请稍后再试")
	case errors.Is(err, providerapi.ErrModelNotFound):
		respond.Fail(c, http.StatusNotFound, providerapi.ErrModelNotFound.Error(), "模型不存在")
	case errors.Is(err, providerapi.ErrModelDisabled):
		respond.Fail(c, http.StatusServiceUnavailable, providerapi.ErrModelDisabled.Error(), "模型已停用")
	case errors.Is(err, llm.ErrEmbeddingUnsupported):
		respond.Fail(c, http.StatusBadRequest, llm.ErrEmbeddingUnsupported.Error(), "该模型提供商不支持 embedding")
	case errors.Is(err, llm.ErrProviderBusy):
		respond.Fail(c, http.StatusServiceUnavailable, llm.ErrProviderBusy.Error(), "供应商忙，请稍后再试")
	default:
		// RateLimited 不是哨兵（*llm.Error 分类错误）：Classify 判类后映射通用
		// errs.ErrRateLimited 429（供应商 429 透传，接口规范错误码表）。
		if class, ok := llm.Classify(err); ok && class == llm.ClassRateLimited {
			respond.Fail(c, http.StatusTooManyRequests, errs.ErrRateLimited.Error(), "供应商限流，请稍后再试")
			return
		}
		respond.FailFromSentinel(c, err)
	}
}

// retrieve 检索（端点 11，spec 05 §3）：BindUri 绑 KB id → 程序内预填 KBIDs 单元素 →
// BindJSON query/top_k（Validate 时 KBIDs 已就位；KBIDs 带 json:"-"，body 无法注入或
// 覆盖——跨 KB 契约由 chat 注入复用）→ 200 数组信封（service 保证空检索也返 [] 非 null）。
func (h *Handler) retrieve(c *gin.Context) {
	var idReq ragapi.GetKnowledgeBaseReq
	if !respond.BindUri(c, &idReq) {
		return
	}
	req := ragapi.RetrieveReq{KBIDs: []uint64{idReq.ID}}
	if !respond.BindJSON(c, &req) {
		return
	}
	chunks, err := h.kbs.Retrieve(c.Request.Context(), req)
	if err != nil {
		failRag(c, err)
		return
	}
	respond.OK(c, chunks)
}
