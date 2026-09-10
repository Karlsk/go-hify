package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Karlsk/go-hify/internal/platform/errs"
	"github.com/Karlsk/go-hify/internal/platform/llm"
	providerapi "github.com/Karlsk/go-hify/internal/provider/api"
	ragapi "github.com/Karlsk/go-hify/internal/rag/api"
)

// handler 测试：httptest 走绑定函数，验证状态码 + respond 信封结构 + 哨兵映射
//（agent / chat 模式）。fakeSvc 只复现哨兵路径与参数查收，不复现业务规则。

type fakeSvc struct {
	ragapi.KnowledgeBaseService // 内嵌接口：Retrieve（05）不覆写，误调即 panic

	injected error // 非 nil 时所有方法返回它（哨兵 / 500 注入）

	kbs    map[uint64]ragapi.KnowledgeBaseSchema
	docs   map[uint64]ragapi.DocumentDetailSchema
	nextID uint64

	// 参数查收
	gotUpload     *ragapi.UploadDocumentReq
	gotUpdateID   uint64
	gotListName   string
	gotListDocsKB uint64
	gotListDocs   ragapi.ListDocumentsReq
	gotRetrieve   *ragapi.RetrieveReq

	// 检索返回集（spec 05）：空检索用初始化空 slice（非 nil——信封 [] 断言）
	retChunks []ragapi.RetrievedChunk
}

func newFakeSvc() *fakeSvc {
	return &fakeSvc{kbs: map[uint64]ragapi.KnowledgeBaseSchema{}, docs: map[uint64]ragapi.DocumentDetailSchema{}, nextID: 1}
}

func (f *fakeSvc) Create(_ context.Context, req ragapi.CreateKnowledgeBaseReq) (*ragapi.KnowledgeBaseSchema, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	s := ragapi.KnowledgeBaseSchema{Name: req.Name, Enabled: true, EmbeddingModelID: strconv.FormatUint(req.EmbeddingModelID, 10)}
	s.ID = strconv.FormatUint(f.nextID, 10)
	f.nextID++
	f.kbs[1] = s
	return &s, nil
}

func (f *fakeSvc) Get(_ context.Context, req ragapi.GetKnowledgeBaseReq) (*ragapi.KnowledgeBaseSchema, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	s, ok := f.kbs[req.ID]
	if !ok {
		return nil, ragapi.ErrKnowledgeBaseNotFound
	}
	return &s, nil
}

func (f *fakeSvc) List(_ context.Context, req ragapi.ListKnowledgeBasesReq) (*ragapi.KnowledgeBaseListResult, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	f.gotListName = req.Name
	ids := make([]uint64, 0, len(f.kbs))
	for id := range f.kbs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	items := make([]ragapi.KnowledgeBaseListItem, 0, len(ids))
	for _, id := range ids {
		items = append(items, ragapi.KnowledgeBaseListItem{KnowledgeBaseSchema: f.kbs[id]})
	}
	return &ragapi.KnowledgeBaseListResult{Items: items, Page: req.Page, PageSize: req.PageSize, Total: int64(len(items))}, nil
}

func (f *fakeSvc) Update(_ context.Context, req ragapi.UpdateKnowledgeBaseReq) (*ragapi.KnowledgeBaseSchema, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	f.gotUpdateID = req.ID
	s, ok := f.kbs[req.ID]
	if !ok {
		return nil, ragapi.ErrKnowledgeBaseNotFound
	}
	s.Name = req.Name
	if req.Enabled != nil {
		s.Enabled = *req.Enabled
	}
	f.kbs[req.ID] = s
	return &s, nil
}

func (f *fakeSvc) Delete(_ context.Context, req ragapi.DeleteKnowledgeBaseReq) error {
	if f.injected != nil {
		return f.injected
	}
	if _, ok := f.kbs[req.ID]; !ok {
		return ragapi.ErrKnowledgeBaseNotFound
	}
	delete(f.kbs, req.ID)
	return nil
}

func (f *fakeSvc) UploadDocument(_ context.Context, req ragapi.UploadDocumentReq) (*ragapi.DocumentSchema, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	cp := req
	f.gotUpload = &cp
	d := ragapi.DocumentSchema{Name: req.Name, FileType: req.FileType, FileSize: req.FileSize, Status: "pending"}
	d.ID = strconv.FormatUint(f.nextID, 10)
	f.nextID++
	f.docs[1] = ragapi.DocumentDetailSchema{DocumentSchema: d, Content: req.Content}
	return &d, nil
}

func (f *fakeSvc) GetDocument(_ context.Context, req ragapi.GetDocumentReq) (*ragapi.DocumentDetailSchema, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	d, ok := f.docs[req.ID]
	if !ok {
		return nil, ragapi.ErrDocumentNotFound
	}
	return &d, nil
}

func (f *fakeSvc) ListDocuments(_ context.Context, req ragapi.ListDocumentsReq) (*ragapi.DocumentListResult, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	f.gotListDocsKB = req.KnowledgeBaseID
	f.gotListDocs = req
	items := make([]ragapi.DocumentSchema, 0, len(f.docs))
	for _, d := range f.docs {
		items = append(items, d.DocumentSchema)
	}
	return &ragapi.DocumentListResult{Items: items, Limit: req.Limit, HasMore: true, NextCursor: "cur-1"}, nil
}

func (f *fakeSvc) DeleteDocument(_ context.Context, req ragapi.DeleteDocumentReq) error {
	if f.injected != nil {
		return f.injected
	}
	if _, ok := f.docs[req.ID]; !ok {
		return ragapi.ErrDocumentNotFound
	}
	delete(f.docs, req.ID)
	return nil
}

func (f *fakeSvc) ReindexDocument(_ context.Context, req ragapi.ReindexDocumentReq) (*ragapi.DocumentSchema, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	d, ok := f.docs[req.ID]
	if !ok {
		return nil, ragapi.ErrDocumentNotFound
	}
	d.Status = "pending"
	s := d.DocumentSchema
	return &s, nil
}

// Retrieve 检索覆写（spec 05 端点 11）：查收请求（KBIDs 程序内填充断言）+ 返回可配置集。
func (f *fakeSvc) Retrieve(_ context.Context, req ragapi.RetrieveReq) ([]ragapi.RetrievedChunk, error) {
	if f.injected != nil {
		return nil, f.injected
	}
	cp := req
	f.gotRetrieve = &cp
	return f.retChunks, nil
}

func newTestRouter(svc *fakeSvc, maxUploadBytes int) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(svc, maxUploadBytes).RegisterRoutes(r.Group("/api/v1"))
	return r
}

func doReq(t *testing.T, r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// multipartBody 构造上传请求体（file 字段 + 可选 name 字段）。
func multipartBody(t *testing.T, filename, fileContent, docName string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if filename != "" {
		fw, err := w.CreateFormFile("file", filename)
		require.NoError(t, err)
		_, err = io.WriteString(fw, fileContent)
		require.NoError(t, err)
	}
	if docName != "" {
		require.NoError(t, w.WriteField("name", docName))
	}
	require.NoError(t, w.Close())
	return &buf, w.FormDataContentType()
}

func doUpload(t *testing.T, r *gin.Engine, path string, body *bytes.Buffer, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, body)
	req.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// envelope 信封骨架；Data / Meta 延迟解析（载荷与分页 meta 形态随端点不同）。
type envelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Meta json.RawMessage `json:"meta"`
}

func parseEnvelope(t *testing.T, body []byte) envelope {
	t.Helper()
	var e envelope
	require.NoError(t, json.Unmarshal(body, &e), "body: %s", body)
	return e
}

func decodeData[T any](t *testing.T, e envelope) T {
	t.Helper()
	var d T
	require.NoError(t, json.Unmarshal(e.Data, &d))
	return d
}

func decodeMeta[T any](t *testing.T, e envelope) T {
	t.Helper()
	var m T
	require.NoError(t, json.Unmarshal(e.Meta, &m), "meta: %s", e.Meta)
	return m
}

// ---- 端点 1：POST /knowledge-bases ----

func TestCreateKB(t *testing.T) {
	svc := newFakeSvc()
	r := newTestRouter(svc, 64)
	w := doReq(t, r, http.MethodPost, "/api/v1/knowledge-bases",
		`{"name":"产品手册","description":"内部文档","embedding_model_id":5}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	e := parseEnvelope(t, w.Body.Bytes())
	require.True(t, e.Success)
	d := decodeData[ragapi.KnowledgeBaseSchema](t, e)
	assert.Equal(t, "产品手册", d.Name)
	assert.Equal(t, "5", d.EmbeddingModelID)
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
}

func TestCreateKB_ValidationFailed(t *testing.T) {
	cases := []struct{ name, body string }{
		{"缺 name", `{"embedding_model_id":5}`},
		{"缺 embedding_model_id", `{"name":"x"}`},
		{"name 超长", `{"name":"` + strings.Repeat("a", 129) + `","embedding_model_id":5}`},
		{"坏 JSON", `{`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRouter(newFakeSvc(), 64)
			w := doReq(t, r, http.MethodPost, "/api/v1/knowledge-bases", tc.body)
			assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			e := parseEnvelope(t, w.Body.Bytes())
			require.NotNil(t, e.Error)
			assert.Equal(t, errs.ErrValidationFailed.Error(), e.Error.Code)
		})
	}
}

// 哨兵 → 状态码全表（spec 04 §3 端点 1 错误列）。
func TestCreateKB_Sentinels(t *testing.T) {
	cases := []struct {
		name     string
		injected error
		wantCode string
		wantHTTP int
	}{
		{"维度不符 400", ragapi.ErrEmbeddingDimMismatch, ragapi.ErrEmbeddingDimMismatch.Error(), http.StatusBadRequest},
		{"模型不存在 404", providerapi.ErrModelNotFound, providerapi.ErrModelNotFound.Error(), http.StatusNotFound},
		{"模型停用 503", providerapi.ErrModelDisabled, providerapi.ErrModelDisabled.Error(), http.StatusServiceUnavailable},
		{"重名 409", ragapi.ErrKnowledgeBaseNameConflict, ragapi.ErrKnowledgeBaseNameConflict.Error(), http.StatusConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newFakeSvc()
			svc.injected = tc.injected
			r := newTestRouter(svc, 64)
			w := doReq(t, r, http.MethodPost, "/api/v1/knowledge-bases", `{"name":"x","embedding_model_id":5}`)
			assert.Equal(t, tc.wantHTTP, w.Code, w.Body.String())
			e := parseEnvelope(t, w.Body.Bytes())
			require.NotNil(t, e.Error)
			assert.Equal(t, tc.wantCode, e.Error.Code)
		})
	}
}

// ---- 端点 2：GET /knowledge-bases ----

func TestListKBs(t *testing.T) {
	svc := newFakeSvc()
	svc.kbs[1] = ragapi.KnowledgeBaseSchema{Name: "a", Enabled: true}
	svc.kbs[2] = ragapi.KnowledgeBaseSchema{Name: "b", Enabled: false}
	r := newTestRouter(svc, 64)

	w := doReq(t, r, http.MethodGet, "/api/v1/knowledge-bases?page=2&page_size=10&name=产品", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	e := parseEnvelope(t, w.Body.Bytes())
	require.True(t, e.Success)
	d := decodeData[[]ragapi.KnowledgeBaseListItem](t, e)
	assert.Len(t, d, 2)
	m := decodeMeta[struct {
		Page     int   `json:"page"`
		PageSize int   `json:"page_size"`
		Total    int64 `json:"total"`
	}](t, e)
	assert.Equal(t, 2, m.Page)
	assert.Equal(t, 10, m.PageSize)
	assert.Equal(t, int64(2), m.Total)
	assert.Equal(t, "产品", svc.gotListName, "name 查询参数透传 service")
}

func TestListKBs_BadPageSize(t *testing.T) {
	r := newTestRouter(newFakeSvc(), 64)
	w := doReq(t, r, http.MethodGet, "/api/v1/knowledge-bases?page_size=999", "")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ---- 端点 3：GET /knowledge-bases/{id} ----

func TestGetKB(t *testing.T) {
	svc := newFakeSvc()
	svc.kbs[1] = ragapi.KnowledgeBaseSchema{Name: "产品手册", DocumentCount: 3}
	r := newTestRouter(svc, 64)

	w := doReq(t, r, http.MethodGet, "/api/v1/knowledge-bases/1", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	d := decodeData[ragapi.KnowledgeBaseSchema](t, parseEnvelope(t, w.Body.Bytes()))
	assert.Equal(t, "产品手册", d.Name)
	assert.Equal(t, int64(3), d.DocumentCount)
}

func TestGetKB_NotFound(t *testing.T) {
	r := newTestRouter(newFakeSvc(), 64)
	w := doReq(t, r, http.MethodGet, "/api/v1/knowledge-bases/999", "")
	assert.Equal(t, http.StatusNotFound, w.Code)
	e := parseEnvelope(t, w.Body.Bytes())
	require.NotNil(t, e.Error)
	assert.Equal(t, ragapi.ErrKnowledgeBaseNotFound.Error(), e.Error.Code)
}

func TestGetKB_BadID(t *testing.T) {
	r := newTestRouter(newFakeSvc(), 64)
	w := doReq(t, r, http.MethodGet, "/api/v1/knowledge-bases/abc", "")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ---- 端点 4：PUT /knowledge-bases/{id} ----

func TestUpdateKB_IDFromPathWinsOverBody(t *testing.T) {
	svc := newFakeSvc()
	svc.kbs[1] = ragapi.KnowledgeBaseSchema{Name: "旧名"}
	r := newTestRouter(svc, 64)

	// body 恶意带 id=999：json:"-" 挡住覆盖，路径 :id=1 生效（provider 踩坑 #7 的回归钉）。
	disabled := false
	w := doReq(t, r, http.MethodPut, "/api/v1/knowledge-bases/1",
		`{"id":999,"name":"新名","description":"d","enabled":`+fmtBool(disabled)+`}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, uint64(1), svc.gotUpdateID)
	d := decodeData[ragapi.KnowledgeBaseSchema](t, parseEnvelope(t, w.Body.Bytes()))
	assert.Equal(t, "新名", d.Name)
	assert.False(t, d.Enabled)
}

func TestUpdateKB_NotFound(t *testing.T) {
	r := newTestRouter(newFakeSvc(), 64)
	w := doReq(t, r, http.MethodPut, "/api/v1/knowledge-bases/999", `{"name":"x"}`)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ---- 端点 5：DELETE /knowledge-bases/{id} ----

func TestDeleteKB_NoContent(t *testing.T) {
	svc := newFakeSvc()
	svc.kbs[1] = ragapi.KnowledgeBaseSchema{}
	r := newTestRouter(svc, 64)

	w := doReq(t, r, http.MethodDelete, "/api/v1/knowledge-bases/1", "")
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Empty(t, w.Body.String(), "204 无返回体")
}

func TestDeleteKB_InUse(t *testing.T) {
	svc := newFakeSvc()
	svc.injected = ragapi.ErrKnowledgeBaseInUse
	r := newTestRouter(svc, 64)

	w := doReq(t, r, http.MethodDelete, "/api/v1/knowledge-bases/1", "")
	assert.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	e := parseEnvelope(t, w.Body.Bytes())
	require.NotNil(t, e.Error)
	assert.Equal(t, ragapi.ErrKnowledgeBaseInUse.Error(), e.Error.Code)
}

// ---- 端点 6：POST /knowledge-bases/{kbId}/documents（multipart 202） ----
// 上限用 1024：multipart 信封自带 ~250B 边界开销，ContentLength 预检对整个报文生效。

func TestUploadDocument(t *testing.T) {
	svc := newFakeSvc()
	r := newTestRouter(svc, 1024)
	body, ct := multipartBody(t, "manual.txt", "正文内容", "使用手册")

	w := doUpload(t, r, "/api/v1/knowledge-bases/1/documents", body, ct)
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	e := parseEnvelope(t, w.Body.Bytes())
	require.True(t, e.Success)
	d := decodeData[ragapi.DocumentSchema](t, e)
	assert.Equal(t, "pending", d.Status, "202 信封 status=pending")
	assert.Equal(t, "txt", d.FileType)
	assert.Equal(t, int64(len("正文内容")), d.FileSize)

	require.NotNil(t, svc.gotUpload, "req 落到 service")
	assert.Equal(t, uint64(1), svc.gotUpload.KnowledgeBaseID)
	assert.Equal(t, "manual.txt", svc.gotUpload.FileName)
	assert.Equal(t, "使用手册", svc.gotUpload.Name)
	assert.Equal(t, "正文内容", svc.gotUpload.Content)
	assert.Equal(t, "txt", svc.gotUpload.FileType, "file_type 取扩展名去点")
	assert.Equal(t, int64(len("正文内容")), svc.gotUpload.FileSize, "file_size = 实读字节数")
}

// name 缺省：原样传空串，回退文件名由 UploadDocumentReq.Validate 归一化（service 侧）。
func TestUploadDocument_NameFallback(t *testing.T) {
	svc := newFakeSvc()
	r := newTestRouter(svc, 1024)
	body, ct := multipartBody(t, "note.md", "# 标题", "")

	w := doUpload(t, r, "/api/v1/knowledge-bases/1/documents", body, ct)
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	require.NotNil(t, svc.gotUpload)
	// name 缺省的回退在 handler 边界就被 Validate 归一化（指针接收器回写），service 收到的已是文件名。
	assert.Equal(t, "note.md", svc.gotUpload.Name)
	assert.Equal(t, "md", svc.gotUpload.FileType)
}

// 超限：ContentLength（含 multipart 开销）先顶到预检。
func TestUploadDocument_TooLarge(t *testing.T) {
	svc := newFakeSvc()
	r := newTestRouter(svc, 8) // 上限 8 字节
	body, ct := multipartBody(t, "manual.txt", "超过八个字节的正文内容", "")

	w := doUpload(t, r, "/api/v1/knowledge-bases/1/documents", body, ct)
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	e := parseEnvelope(t, w.Body.Bytes())
	require.NotNil(t, e.Error)
	assert.Equal(t, errs.ErrValidationFailed.Error(), e.Error.Code)
	assert.Nil(t, svc.gotUpload, "超限不触 service")
}

// 超限兜底：ContentLength 缺失（-1）时由 LimitReader 读后判定（multipart 开销不计，
// 上限约束的是文件本体）。
func TestUploadDocument_LimitReaderOverread(t *testing.T) {
	svc := newFakeSvc()
	r := newTestRouter(svc, 8)
	body, ct := multipartBody(t, "manual.txt", "超过八个字节的正文内容", "")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/knowledge-bases/1/documents", body)
	req.Header.Set("Content-Type", ct)
	req.ContentLength = -1 // 模拟未知长度（chunked）
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Nil(t, svc.gotUpload)
}

// ContentLength 预检：声明超限直接 400，不读 body。
func TestUploadDocument_ContentLengthPrecheck(t *testing.T) {
	svc := newFakeSvc()
	r := newTestRouter(svc, 8)
	body, ct := multipartBody(t, "manual.txt", "短", "")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/knowledge-bases/1/documents", body)
	req.Header.Set("Content-Type", ct)
	req.ContentLength = 1 << 20 // 模拟超大声明
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Nil(t, svc.gotUpload)
}

func TestUploadDocument_BadExt(t *testing.T) {
	svc := newFakeSvc()
	r := newTestRouter(svc, 1024)
	body, ct := multipartBody(t, "shell.pdf", "%PDF-1.4", "")

	w := doUpload(t, r, "/api/v1/knowledge-bases/1/documents", body, ct)
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	e := parseEnvelope(t, w.Body.Bytes())
	require.NotNil(t, e.Error)
	assert.Equal(t, errs.ErrValidationFailed.Error(), e.Error.Code)
}

func TestUploadDocument_MissingFile(t *testing.T) {
	svc := newFakeSvc()
	r := newTestRouter(svc, 1024)
	body, ct := multipartBody(t, "", "", "") // 无 file 字段

	w := doUpload(t, r, "/api/v1/knowledge-bases/1/documents", body, ct)
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	e := parseEnvelope(t, w.Body.Bytes())
	require.NotNil(t, e.Error)
	assert.Equal(t, errs.ErrValidationFailed.Error(), e.Error.Code)
}

func TestUploadDocument_KBNotFound(t *testing.T) {
	svc := newFakeSvc()
	svc.injected = ragapi.ErrKnowledgeBaseNotFound
	r := newTestRouter(svc, 1024)
	body, ct := multipartBody(t, "manual.txt", "正文", "")

	w := doUpload(t, r, "/api/v1/knowledge-bases/1/documents", body, ct)
	assert.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
}

// ---- 端点 7：GET /knowledge-bases/{kbId}/documents（游标） ----

func TestListDocuments(t *testing.T) {
	svc := newFakeSvc()
	svc.docs[3] = ragapi.DocumentDetailSchema{DocumentSchema: ragapi.DocumentSchema{Name: "doc", Status: "ready"}}
	r := newTestRouter(svc, 64)

	w := doReq(t, r, http.MethodGet, "/api/v1/knowledge-bases/1/documents?limit=5&cursor=abc", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	e := parseEnvelope(t, w.Body.Bytes())
	require.True(t, e.Success)
	d := decodeData[[]ragapi.DocumentSchema](t, e)
	assert.Len(t, d, 1)
	m := decodeMeta[struct {
		Limit      int    `json:"limit"`
		HasMore    bool   `json:"has_more"`
		NextCursor string `json:"next_cursor"`
	}](t, e)
	assert.Equal(t, 5, m.Limit)
	assert.True(t, m.HasMore)
	assert.Equal(t, "cur-1", m.NextCursor)

	assert.Equal(t, uint64(1), svc.gotListDocsKB, "路径 :id 落到 KnowledgeBaseID")
	assert.Equal(t, 5, svc.gotListDocs.Limit)
	assert.Equal(t, "abc", svc.gotListDocs.Cursor)
}

func TestListDocuments_KBNotFound(t *testing.T) {
	svc := newFakeSvc()
	svc.injected = ragapi.ErrKnowledgeBaseNotFound
	r := newTestRouter(svc, 64)

	w := doReq(t, r, http.MethodGet, "/api/v1/knowledge-bases/999/documents", "")
	assert.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
}

// ---- 端点 8：GET /documents/{id} ----

func TestGetDocument(t *testing.T) {
	svc := newFakeSvc()
	svc.docs[9] = ragapi.DocumentDetailSchema{
		DocumentSchema: ragapi.DocumentSchema{Name: "manual.txt", Status: "ready", ChunkCount: 8},
		Content:        "正文内容",
	}
	r := newTestRouter(svc, 64)

	w := doReq(t, r, http.MethodGet, "/api/v1/documents/9", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	d := decodeData[ragapi.DocumentDetailSchema](t, parseEnvelope(t, w.Body.Bytes()))
	assert.Equal(t, "正文内容", d.Content)
	assert.Equal(t, 8, d.ChunkCount)
}

func TestGetDocument_NotFound(t *testing.T) {
	r := newTestRouter(newFakeSvc(), 64)
	w := doReq(t, r, http.MethodGet, "/api/v1/documents/999", "")
	assert.Equal(t, http.StatusNotFound, w.Code)
	e := parseEnvelope(t, w.Body.Bytes())
	require.NotNil(t, e.Error)
	assert.Equal(t, ragapi.ErrDocumentNotFound.Error(), e.Error.Code)
}

// ---- 端点 9：DELETE /documents/{id} ----

func TestDeleteDocument_NoContent(t *testing.T) {
	svc := newFakeSvc()
	svc.docs[9] = ragapi.DocumentDetailSchema{}
	r := newTestRouter(svc, 64)

	w := doReq(t, r, http.MethodDelete, "/api/v1/documents/9", "")
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Empty(t, w.Body.String())
}

func TestDeleteDocument_NotFound(t *testing.T) {
	r := newTestRouter(newFakeSvc(), 64)
	w := doReq(t, r, http.MethodDelete, "/api/v1/documents/999", "")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ---- 端点 10：POST /documents/{id}/reindex（202） ----

func TestReindexDocument(t *testing.T) {
	svc := newFakeSvc()
	svc.docs[9] = ragapi.DocumentDetailSchema{DocumentSchema: ragapi.DocumentSchema{Name: "manual.txt", Status: "ready"}}
	r := newTestRouter(svc, 64)

	w := doReq(t, r, http.MethodPost, "/api/v1/documents/9/reindex", "")
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	d := decodeData[ragapi.DocumentSchema](t, parseEnvelope(t, w.Body.Bytes()))
	assert.Equal(t, "pending", d.Status, "202 信封 status=pending")
}

func TestReindexDocument_Processing(t *testing.T) {
	svc := newFakeSvc()
	svc.injected = ragapi.ErrDocumentProcessing
	r := newTestRouter(svc, 64)

	w := doReq(t, r, http.MethodPost, "/api/v1/documents/9/reindex", "")
	assert.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	e := parseEnvelope(t, w.Body.Bytes())
	require.NotNil(t, e.Error)
	assert.Equal(t, ragapi.ErrDocumentProcessing.Error(), e.Error.Code)
}

// fmtBool 布尔字面量（避免引入 fmt 拼 body 的噪音）。
func fmtBool(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// ---- 端点 11：POST /knowledge-bases/:id/retrieve（spec 05 §3） ----

// 200 数组信封：路径 id 进 KBIDs（程序内填充）、query/top_k 透传、字段完整。
func TestRetrieveEndpoint(t *testing.T) {
	svc := newFakeSvc()
	svc.retChunks = []ragapi.RetrievedChunk{{
		ChunkID: "9", DocumentID: "100", KnowledgeBaseID: "1",
		DocumentName: "手册.txt", ChunkIndex: 0, Content: "正文", Similarity: 0.75,
	}}
	r := newTestRouter(svc, 1024)

	w := doReq(t, r, http.MethodPost, "/api/v1/knowledge-bases/1/retrieve", `{"query":"怎么创建 Agent","top_k":8}`)
	require.Equal(t, http.StatusOK, w.Code)

	e := parseEnvelope(t, w.Body.Bytes())
	assert.True(t, e.Success)
	chunks := decodeData[[]ragapi.RetrievedChunk](t, e)
	require.Len(t, chunks, 1)
	assert.Equal(t, "9", chunks[0].ChunkID)
	assert.Equal(t, "手册.txt", chunks[0].DocumentName)
	assert.InDelta(t, 0.75, chunks[0].Similarity, 1e-9)

	// 单 KB 路径参数进 KBIDs；body 的 query / top_k 原样透传（TopK 0=默认合法）。
	require.NotNil(t, svc.gotRetrieve)
	assert.Equal(t, []uint64{1}, svc.gotRetrieve.KBIDs)
	assert.Equal(t, "怎么创建 Agent", svc.gotRetrieve.Query)
	assert.Equal(t, 8, svc.gotRetrieve.TopK)
}

// 空检索 → data 为 []（非 null——前端免空判断）。
func TestRetrieveEmptyArray(t *testing.T) {
	svc := newFakeSvc()
	svc.retChunks = []ragapi.RetrievedChunk{} // service 保证非 nil
	r := newTestRouter(svc, 1024)

	w := doReq(t, r, http.MethodPost, "/api/v1/knowledge-bases/1/retrieve", `{"query":"冷门问题"}`)
	require.Equal(t, http.StatusOK, w.Code)

	e := parseEnvelope(t, w.Body.Bytes())
	assert.True(t, e.Success)
	assert.Equal(t, "[]", strings.TrimSpace(string(e.Data)), "空列表序列化为 []，不返 null")
}

// query 缺失 / top_k 越界 → 400（binding tag）。
func TestRetrieveBindErrors(t *testing.T) {
	r := newTestRouter(newFakeSvc(), 1024)

	w := doReq(t, r, http.MethodPost, "/api/v1/knowledge-bases/1/retrieve", `{"top_k":5}`)
	require.Equal(t, http.StatusBadRequest, w.Code)

	w = doReq(t, r, http.MethodPost, "/api/v1/knowledge-bases/1/retrieve", `{"query":"q","top_k":21}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
	e := parseEnvelope(t, w.Body.Bytes())
	assert.Equal(t, errs.ErrValidationFailed.Error(), e.Error.Code)

	w = doReq(t, r, http.MethodPost, "/api/v1/knowledge-bases/1/retrieve", `{"query":"q","top_k":-1}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

// 哨兵 → 状态码全表（spec 05 §3）：KB 404 / Mismatch 400 / Unsupported 400 /
// Busy 503 / RateLimited 429（*llm.Error 分类，非哨兵）/ ErrInternal 500。
func TestRetrieveSentinelMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code int
		want string
	}{
		{"kb not found", ragapi.ErrKnowledgeBaseNotFound, http.StatusNotFound, "KNOWLEDGE_BASE_NOT_FOUND"},
		{"embedding model mismatch", ragapi.ErrEmbeddingModelMismatch, http.StatusBadRequest, "EMBEDDING_MODEL_MISMATCH"},
		{"embedding unsupported", llm.ErrEmbeddingUnsupported, http.StatusBadRequest, "EMBEDDING_UNSUPPORTED"},
		{"provider busy wrapped", fmt.Errorf("wrap: %w", llm.ErrProviderBusy), http.StatusServiceUnavailable, "PROVIDER_BUSY"},
		{"rate limited class", &llm.Error{Class: llm.ClassRateLimited, Err: errors.New("HTTP 429")}, http.StatusTooManyRequests, "RATE_LIMITED"},
		{"dim mismatch internal", fmt.Errorf("retrieve: dim 1535: %w", errs.ErrInternal), http.StatusInternalServerError, "INTERNAL_ERROR"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newFakeSvc()
			svc.injected = tc.err
			r := newTestRouter(svc, 1024)

			w := doReq(t, r, http.MethodPost, "/api/v1/knowledge-bases/1/retrieve", `{"query":"q"}`)
			require.Equal(t, tc.code, w.Code)
			e := parseEnvelope(t, w.Body.Bytes())
			require.NotNil(t, e.Error)
			assert.Equal(t, tc.want, e.Error.Code)
		})
	}
}
