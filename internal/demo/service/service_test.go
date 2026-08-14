package service

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"testing"
	"time"

	"gorm.io/gorm"

	demoapi "github.com/Karlsk/go-hify/internal/demo/api"
	"github.com/Karlsk/go-hify/internal/platform/page"
)

// service 测试：stub 本模块 Store 接口，验证 CRUD 编排、哨兵错误翻译（边界处）、
// model→schema 转换、分页参数归一化。

// stubStore 内存版 Store（GetByID 返回副本，模拟 DB 行为，避免测试间意外别名共享）。
type stubStore struct {
	items     map[uint64]*DemoItem
	next      uint64
	injectErr error // 非 nil 时所有方法返回该错误（测 store 故障路径）
}

func newStubStore() *stubStore {
	return &stubStore{items: map[uint64]*DemoItem{}, next: 1}
}

func (s *stubStore) Create(_ context.Context, item *DemoItem) error {
	if s.injectErr != nil {
		return s.injectErr
	}
	item.ID = s.next
	s.next++
	now := time.Now() // 模拟 DB 的 now() 默认值
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = now
	}
	cp := *item
	s.items[item.ID] = &cp
	return nil
}

func (s *stubStore) GetByID(_ context.Context, id uint64) (*DemoItem, error) {
	if s.injectErr != nil {
		return nil, s.injectErr
	}
	item, ok := s.items[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	cp := *item
	return &cp, nil
}

func (s *stubStore) List(_ context.Context, p page.OffsetParams) (page.OffsetResult[DemoItem], error) {
	if s.injectErr != nil {
		return page.OffsetResult[DemoItem]{}, s.injectErr
	}
	all := make([]DemoItem, 0, len(s.items))
	for _, item := range s.items {
		all = append(all, *item)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID > all[j].ID }) // ORDER BY id DESC
	total := int64(len(all))
	start, end := p.Offset(), p.Offset()+p.Limit()
	if start > len(all) {
		start = len(all)
	}
	if end > len(all) {
		end = len(all)
	}
	return page.NewOffsetResult(all[start:end], p, total), nil
}

func (s *stubStore) Update(_ context.Context, item *DemoItem) error {
	if s.injectErr != nil {
		return s.injectErr
	}
	if _, ok := s.items[item.ID]; !ok {
		return gorm.ErrRecordNotFound
	}
	item.UpdatedAt = time.Now() // 模拟 GORM autoUpdateTime
	cp := *item
	s.items[item.ID] = &cp
	return nil
}

func (s *stubStore) Delete(_ context.Context, id uint64) error {
	if s.injectErr != nil {
		return s.injectErr
	}
	if _, ok := s.items[id]; !ok {
		return gorm.ErrRecordNotFound
	}
	delete(s.items, id)
	return nil
}

// seed 预置一条记录并返回其 ID。
func seed(t *testing.T, svc demoapi.DemoService, name, status string) string {
	t.Helper()
	item, err := svc.Create(context.Background(), demoapi.CreateReq{Name: name, Status: status})
	if err != nil {
		t.Fatalf("seed create: %v", err)
	}
	return item.ID
}

func parseID(t *testing.T, s string) uint64 {
	t.Helper()
	id, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		t.Fatalf("parse schema id %q: %v", s, err)
	}
	return id
}

func TestCreate(t *testing.T) {
	svc := New(newStubStore())
	item, err := svc.Create(context.Background(), demoapi.CreateReq{Name: "first", Status: demoapi.StatusDraft})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if item.ID == "" || item.Name != "first" || item.Status != demoapi.StatusDraft {
		t.Fatalf("schema = %+v", item)
	}
	if item.CreatedAt.IsZero() || item.UpdatedAt.IsZero() {
		t.Fatalf("timestamps must be filled: %+v", item)
	}
}

func TestCreateStoreError(t *testing.T) {
	st := newStubStore()
	st.injectErr = errors.New("db down")
	svc := New(st)
	_, err := svc.Create(context.Background(), demoapi.CreateReq{Name: "n", Status: demoapi.StatusDraft})
	if err == nil || errors.Is(err, demoapi.ErrDemoItemNotFound) {
		t.Fatalf("err = %v, want wrapped store error (not sentinel)", err)
	}
}

func TestGet(t *testing.T) {
	svc := New(newStubStore())
	id := seed(t, svc, "first", demoapi.StatusDraft)
	item, err := svc.Get(context.Background(), demoapi.GetReq{ID: parseID(t, id)})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if item.ID != id || item.Name != "first" || item.Status != demoapi.StatusDraft {
		t.Fatalf("schema = %+v", item)
	}
}

func TestGetNotFound(t *testing.T) {
	svc := New(newStubStore())
	_, err := svc.Get(context.Background(), demoapi.GetReq{ID: 999})
	if !errors.Is(err, demoapi.ErrDemoItemNotFound) {
		t.Fatalf("err = %v, want ErrDemoItemNotFound", err)
	}
}

func TestGetStoreError(t *testing.T) {
	st := newStubStore()
	st.injectErr = errors.New("db down")
	svc := New(st)
	_, err := svc.Get(context.Background(), demoapi.GetReq{ID: 1})
	if err == nil || errors.Is(err, demoapi.ErrDemoItemNotFound) {
		t.Fatalf("err = %v, want wrapped store error (not sentinel)", err)
	}
}

func TestList(t *testing.T) {
	svc := New(newStubStore())
	seed(t, svc, "a", demoapi.StatusDraft)
	seed(t, svc, "b", demoapi.StatusActive)
	seed(t, svc, "c", demoapi.StatusArchived)

	// 第 1 页（size=2）：2 条，id 降序（3, 2），total=3。
	res, err := svc.List(context.Background(), demoapi.ListReq{Page: 1, PageSize: 2})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if res.Total != 3 || res.Page != 1 || res.PageSize != 2 {
		t.Fatalf("meta = page:%d size:%d total:%d", res.Page, res.PageSize, res.Total)
	}
	if len(res.Items) != 2 || res.Items[0].Name != "c" || res.Items[1].Name != "b" {
		t.Fatalf("items = %+v", res.Items)
	}

	// 第 2 页：剩 1 条。
	res, err = svc.List(context.Background(), demoapi.ListReq{Page: 2, PageSize: 2})
	if err != nil {
		t.Fatalf("list page2: %v", err)
	}
	if len(res.Items) != 1 || res.Items[0].Name != "a" {
		t.Fatalf("page2 items = %+v", res.Items)
	}
}

func TestListEmptyReturnsEmptySlice(t *testing.T) {
	svc := New(newStubStore())
	res, err := svc.List(context.Background(), demoapi.ListReq{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// 空列表返回 [] 不返回 null（CLAUDE.md《空值约定》）。
	if res.Items == nil || len(res.Items) != 0 {
		t.Fatalf("items = %v, want empty non-nil slice", res.Items)
	}
	// page/page_size 缺省归一化（page.NewOffset：1 / 20）。
	if res.Page != 1 || res.PageSize != page.DefaultPageSize {
		t.Fatalf("normalized = page:%d size:%d, want 1/%d", res.Page, res.PageSize, page.DefaultPageSize)
	}
}

func TestListStoreError(t *testing.T) {
	st := newStubStore()
	st.injectErr = errors.New("db down")
	svc := New(st)
	_, err := svc.List(context.Background(), demoapi.ListReq{})
	if err == nil {
		t.Fatal("list with store error should fail")
	}
}

func TestUpdate(t *testing.T) {
	svc := New(newStubStore())
	id := seed(t, svc, "old", demoapi.StatusDraft)
	updated, err := svc.Update(context.Background(), demoapi.UpdateReq{
		ID: parseID(t, id), Name: "new", Status: demoapi.StatusActive,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Name != "new" || updated.Status != demoapi.StatusActive {
		t.Fatalf("updated = %+v", updated)
	}
	// 再取一次确认落库。
	got, err := svc.Get(context.Background(), demoapi.GetReq{ID: parseID(t, id)})
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got.Name != "new" || got.Status != demoapi.StatusActive {
		t.Fatalf("persisted = %+v", got)
	}
}

func TestUpdateNotFound(t *testing.T) {
	svc := New(newStubStore())
	_, err := svc.Update(context.Background(), demoapi.UpdateReq{ID: 999, Name: "n", Status: demoapi.StatusDraft})
	if !errors.Is(err, demoapi.ErrDemoItemNotFound) {
		t.Fatalf("err = %v, want ErrDemoItemNotFound", err)
	}
}

func TestUpdateStoreErrorOnGet(t *testing.T) {
	st := newStubStore()
	st.injectErr = errors.New("db down")
	svc := New(st)
	_, err := svc.Update(context.Background(), demoapi.UpdateReq{ID: 1, Name: "n", Status: demoapi.StatusDraft})
	if err == nil || errors.Is(err, demoapi.ErrDemoItemNotFound) {
		t.Fatalf("err = %v, want wrapped store error (not sentinel)", err)
	}
}

func TestDelete(t *testing.T) {
	svc := New(newStubStore())
	id := seed(t, svc, "gone", demoapi.StatusDraft)
	if err := svc.Delete(context.Background(), demoapi.DeleteReq{ID: parseID(t, id)}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.Get(context.Background(), demoapi.GetReq{ID: parseID(t, id)}); !errors.Is(err, demoapi.ErrDemoItemNotFound) {
		t.Fatalf("get after delete err = %v, want ErrDemoItemNotFound", err)
	}
}

func TestDeleteNotFound(t *testing.T) {
	svc := New(newStubStore())
	if err := svc.Delete(context.Background(), demoapi.DeleteReq{ID: 999}); !errors.Is(err, demoapi.ErrDemoItemNotFound) {
		t.Fatalf("err = %v, want ErrDemoItemNotFound", err)
	}
}

func TestDeleteStoreError(t *testing.T) {
	st := newStubStore()
	st.injectErr = errors.New("db down")
	svc := New(st)
	if err := svc.Delete(context.Background(), demoapi.DeleteReq{ID: 1}); err == nil || errors.Is(err, demoapi.ErrDemoItemNotFound) {
		t.Fatalf("err = %v, want wrapped store error (not sentinel)", err)
	}
}
