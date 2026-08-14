package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	demoapi "github.com/Karlsk/go-hify/internal/demo/api"
)

// handler 测试：httptest 走绑定函数，验证状态码 + respond 信封结构 + 哨兵映射。

// fakeSvc 内存版 demoapi.DemoService。
type fakeSvc struct {
	items     map[uint64]*demoapi.DemoItemSchema
	next      uint64
	injectErr error // 非 nil 时所有方法返回该错误（测 500 兜底路径）
}

func newFakeSvc() *fakeSvc {
	return &fakeSvc{items: map[uint64]*demoapi.DemoItemSchema{}, next: 1}
}

func (f *fakeSvc) Create(_ context.Context, req demoapi.CreateReq) (*demoapi.DemoItemSchema, error) {
	if f.injectErr != nil {
		return nil, f.injectErr
	}
	item := &demoapi.DemoItemSchema{ID: strconv.FormatUint(f.next, 10), Name: req.Name, Status: req.Status}
	f.items[f.next] = item
	f.next++
	return item, nil
}

func (f *fakeSvc) Get(_ context.Context, req demoapi.GetReq) (*demoapi.DemoItemSchema, error) {
	if f.injectErr != nil {
		return nil, f.injectErr
	}
	item, ok := f.items[req.ID]
	if !ok {
		return nil, demoapi.ErrDemoItemNotFound
	}
	return item, nil
}

func (f *fakeSvc) List(_ context.Context, req demoapi.ListReq) (*demoapi.ListResult, error) {
	if f.injectErr != nil {
		return nil, f.injectErr
	}
	all := make([]demoapi.DemoItemSchema, 0, len(f.items))
	for _, item := range f.items {
		all = append(all, *item)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID > all[j].ID })
	page, size := req.Page, req.PageSize
	if page < 1 {
		page = 1
	}
	if size <= 0 {
		size = 20
	}
	return &demoapi.ListResult{Items: all, Page: page, PageSize: size, Total: int64(len(all))}, nil
}

func (f *fakeSvc) Update(_ context.Context, req demoapi.UpdateReq) (*demoapi.DemoItemSchema, error) {
	if f.injectErr != nil {
		return nil, f.injectErr
	}
	item, ok := f.items[req.ID]
	if !ok {
		return nil, demoapi.ErrDemoItemNotFound
	}
	item.Name = req.Name
	item.Status = req.Status
	return item, nil
}

func (f *fakeSvc) Delete(_ context.Context, req demoapi.DeleteReq) error {
	if f.injectErr != nil {
		return f.injectErr
	}
	if _, ok := f.items[req.ID]; !ok {
		return demoapi.ErrDemoItemNotFound
	}
	delete(f.items, req.ID)
	return nil
}

func newTestRouter(svc demoapi.DemoService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(svc).RegisterRoutes(r.Group("/api/v1"))
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

// envelope 解出统一信封的关键字段。
type envelope struct {
	Success bool `json:"success"`
	Data    struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"data"`
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details any    `json:"details"`
	} `json:"error"`
	Meta struct {
		Page     int   `json:"page"`
		PageSize int   `json:"page_size"`
		Total    int64 `json:"total"`
	} `json:"meta"`
}

func parseEnvelope(t *testing.T, body []byte) envelope {
	t.Helper()
	var e envelope
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("unmarshal envelope %s: %v", body, err)
	}
	return e
}

func TestCreate(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	w := doReq(t, r, http.MethodPost, "/api/v1/demo-items", `{"name":"first","status":"draft"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("code = %d, want 201 (body: %s)", w.Code, w.Body.String())
	}
	e := parseEnvelope(t, w.Body.Bytes())
	if !e.Success || e.Data.ID == "" || e.Data.Name != "first" || e.Data.Status != "draft" {
		t.Fatalf("envelope = %+v", e)
	}
	// API 响应一律 no-store（CLAUDE.md《统一响应信封》）。
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

func TestCreateValidationFailed(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	cases := []struct {
		name string
		body string
	}{
		{"非法 status（跨字段 Validate）", `{"name":"n","status":"bogus"}`},
		{"缺 name（binding required）", `{"status":"draft"}`},
		{"坏 JSON", `{`},
	}
	for _, tc := range cases {
		w := doReq(t, r, http.MethodPost, "/api/v1/demo-items", tc.body)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: code = %d, want 400 (body: %s)", tc.name, w.Code, w.Body.String())
		}
		e := parseEnvelope(t, w.Body.Bytes())
		if e.Success || e.Error.Code != "VALIDATION_FAILED" {
			t.Fatalf("%s: envelope = %+v", tc.name, e)
		}
	}
}

func TestGet(t *testing.T) {
	svc := newFakeSvc()
	r := newTestRouter(svc)
	created := doReq(t, r, http.MethodPost, "/api/v1/demo-items", `{"name":"first","status":"draft"}`)
	id := parseEnvelope(t, created.Body.Bytes()).Data.ID

	w := doReq(t, r, http.MethodGet, "/api/v1/demo-items/"+id, "")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	e := parseEnvelope(t, w.Body.Bytes())
	if !e.Success || e.Data.ID != id || e.Data.Name != "first" {
		t.Fatalf("envelope = %+v", e)
	}
}

func TestGetNotFound(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	w := doReq(t, r, http.MethodGet, "/api/v1/demo-items/999", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404 (body: %s)", w.Code, w.Body.String())
	}
	e := parseEnvelope(t, w.Body.Bytes())
	if e.Success || e.Error.Code != "DEMO_ITEM_NOT_FOUND" {
		t.Fatalf("envelope = %+v", e)
	}
}

func TestGetBadID(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	w := doReq(t, r, http.MethodGet, "/api/v1/demo-items/abc", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400 (body: %s)", w.Code, w.Body.String())
	}
}

func TestList(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	doReq(t, r, http.MethodPost, "/api/v1/demo-items", `{"name":"a","status":"draft"}`)
	doReq(t, r, http.MethodPost, "/api/v1/demo-items", `{"name":"b","status":"active"}`)

	w := doReq(t, r, http.MethodGet, "/api/v1/demo-items?page=1&page_size=20", "")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	// 列表响应 data 是数组，用专属结构解（envelope.Data 是对象结构，不适用）。
	var body struct {
		Success bool                     `json:"success"`
		Data    []demoapi.DemoItemSchema `json:"data"`
		Meta    struct {
			Page     int   `json:"page"`
			PageSize int   `json:"page_size"`
			Total    int64 `json:"total"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal list body: %v", err)
	}
	if !body.Success || body.Meta.Total != 2 || body.Meta.Page != 1 || body.Meta.PageSize != 20 {
		t.Fatalf("body = %+v", body)
	}
	if len(body.Data) != 2 {
		t.Fatalf("data len = %d, want 2", len(body.Data))
	}
}

func TestListEmptyReturnsEmptyArray(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	w := doReq(t, r, http.MethodGet, "/api/v1/demo-items", "")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	// 空列表 data 必须是 [] 不是 null（CLAUDE.md《空值约定》）。
	if !strings.Contains(w.Body.String(), `"data":[]`) {
		t.Fatalf("body = %s, want data:[]", w.Body.String())
	}
}

func TestListBadPageSize(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	// page_size 超上限 100 → binding 拒绝（400）。
	w := doReq(t, r, http.MethodGet, "/api/v1/demo-items?page_size=101", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400 (body: %s)", w.Code, w.Body.String())
	}
}

func TestUpdate(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	created := doReq(t, r, http.MethodPost, "/api/v1/demo-items", `{"name":"old","status":"draft"}`)
	id := parseEnvelope(t, created.Body.Bytes()).Data.ID

	w := doReq(t, r, http.MethodPut, "/api/v1/demo-items/"+id, `{"name":"new","status":"active"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	e := parseEnvelope(t, w.Body.Bytes())
	if !e.Success || e.Data.Name != "new" || e.Data.Status != "active" {
		t.Fatalf("envelope = %+v", e)
	}
}

func TestUpdateNotFound(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	w := doReq(t, r, http.MethodPut, "/api/v1/demo-items/999", `{"name":"n","status":"draft"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404 (body: %s)", w.Code, w.Body.String())
	}
	e := parseEnvelope(t, w.Body.Bytes())
	if e.Error.Code != "DEMO_ITEM_NOT_FOUND" {
		t.Fatalf("envelope = %+v", e)
	}
}

func TestUpdateValidationFailed(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	created := doReq(t, r, http.MethodPost, "/api/v1/demo-items", `{"name":"old","status":"draft"}`)
	id := parseEnvelope(t, created.Body.Bytes()).Data.ID
	w := doReq(t, r, http.MethodPut, "/api/v1/demo-items/"+id, `{"name":"n","status":"bogus"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400 (body: %s)", w.Code, w.Body.String())
	}
}

func TestDelete(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	created := doReq(t, r, http.MethodPost, "/api/v1/demo-items", `{"name":"gone","status":"draft"}`)
	id := parseEnvelope(t, created.Body.Bytes()).Data.ID

	w := doReq(t, r, http.MethodDelete, "/api/v1/demo-items/"+id, "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("code = %d, want 204 (body: %s)", w.Code, w.Body.String())
	}
	// 删后再取 → 404。
	w = doReq(t, r, http.MethodGet, "/api/v1/demo-items/"+id, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("get after delete code = %d, want 404", w.Code)
	}
}

func TestDeleteNotFound(t *testing.T) {
	r := newTestRouter(newFakeSvc())
	w := doReq(t, r, http.MethodDelete, "/api/v1/demo-items/999", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404 (body: %s)", w.Code, w.Body.String())
	}
}

func TestServiceUnexpectedErrorFallsBackTo500(t *testing.T) {
	svc := newFakeSvc()
	svc.injectErr = errors.New("boom")
	r := newTestRouter(svc)
	w := doReq(t, r, http.MethodGet, "/api/v1/demo-items/1", "")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d, want 500 (body: %s)", w.Code, w.Body.String())
	}
	e := parseEnvelope(t, w.Body.Bytes())
	if e.Error.Code != "INTERNAL_ERROR" {
		t.Fatalf("envelope = %+v", e)
	}
}
