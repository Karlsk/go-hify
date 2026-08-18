package page

import (
	"math"
	"reflect"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// 偏移分页的参数归一化、Offset/Limit 数学、Apply 链、TotalPages、nil→[] 兜底。

func TestNewOffsetNormalization(t *testing.T) {
	tests := []struct {
		name             string
		page, pageSize   int
		wantPage, wantPS int
	}{
		{"both zero → defaults", 0, 0, 1, DefaultPageSize},
		{"page below 1", -3, 20, 1, 20},
		{"pageSize below 1", 2, -5, 2, DefaultPageSize},
		{"pageSize over max", 1, 999, 1, MaxPageSize},
		{"valid passthrough", 3, 50, 3, 50},
		{"page 1 min size 1", 1, 1, 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewOffset(tt.page, tt.pageSize)
			if got.Page != tt.wantPage || got.PageSize != tt.wantPS {
				t.Fatalf("NewOffset(%d,%d) = %+v, want {Page:%d PageSize:%d}",
					tt.page, tt.pageSize, got, tt.wantPage, tt.wantPS)
			}
		})
	}
}

func TestOffsetMath(t *testing.T) {
	tests := []struct {
		page, pageSize, wantOffset, wantLimit int
	}{
		{1, 20, 0, 20},
		{2, 20, 20, 20},
		{3, 20, 40, 20},
		{2, 50, 50, 50},
		{10, 100, 900, 100},
	}
	for _, tt := range tests {
		p := OffsetParams{Page: tt.page, PageSize: tt.pageSize}
		if got := p.Offset(); got != tt.wantOffset {
			t.Errorf("page %d size %d: Offset()=%d want %d", tt.page, tt.pageSize, got, tt.wantOffset)
		}
		if got := p.Limit(); got != tt.wantLimit {
			t.Errorf("page %d size %d: Limit()=%d want %d", tt.page, tt.pageSize, got, tt.wantLimit)
		}
	}
}

// 超大 Page 的乘积在 int 上溢出为负（曾致切片 panic）；Offset() 必须夹进 [0, MaxInt]。
func TestOffsetOverflowNeverNegative(t *testing.T) {
	cases := []struct {
		page, pageSize int
	}{
		{math.MaxInt, 20},
		{math.MaxInt / 2, 100},
		{1 << 62, 1 << 10},
	}
	for _, c := range cases {
		if got := (OffsetParams{Page: c.page, PageSize: c.pageSize}).Offset(); got < 0 {
			t.Errorf("page=%d size=%d: Offset()=%d, want >= 0", c.page, c.pageSize, got)
		}
	}
}

// dummyOffset 是 Apply 测试用的临时 model。
type dummyOffset struct {
	ID   uint64 `gorm:"primaryKey"`
	Name string
}

func (dummyOffset) TableName() string { return "dummies" }

func newMockDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
	t.Helper()
	mockDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: mockDB}), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm open: %v", err)
	}
	t.Cleanup(func() { mockDB.Close() })
	return db, mock
}

// TestOffsetApply 验证 Apply 链出正确的 LIMIT / OFFSET（page=3 size=20 → limit=20 offset=40）。
// GORM postgres 驱动把 limit/offset 参数化为 LIMIT $1 OFFSET $2，用 WithArgs 断言数值。
func TestOffsetApply(t *testing.T) {
	db, mock := newMockDB(t)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "dummies" LIMIT $1 OFFSET $2`)).
		WithArgs(20, 40).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "x"))

	var xs []dummyOffset
	p := OffsetParams{Page: 3, PageSize: 20}
	if err := p.Apply(db).Find(&xs).Error; err != nil {
		t.Fatalf("find: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestOffsetResultTotalPages(t *testing.T) {
	tests := []struct {
		total    int64
		pageSize int
		want     int
	}{
		{0, 20, 0},
		{1, 20, 1},
		{20, 20, 1},
		{21, 20, 2},
		{40, 20, 2},
		{47, 20, 3},
		{100, 20, 5},
		{50, 0, 0}, // 防御：pageSize<=0 不 panic
	}
	for _, tt := range tests {
		r := OffsetResult[int]{Items: []int{}, Page: 1, PageSize: tt.pageSize, Total: tt.total}
		if got := r.TotalPages(); got != tt.want {
			t.Errorf("total=%d size=%d: TotalPages()=%d want %d", tt.total, tt.pageSize, got, tt.want)
		}
	}
}

func TestNewOffsetResultNilToEmpty(t *testing.T) {
	r := NewOffsetResult[int](nil, OffsetParams{Page: 1, PageSize: 20}, 0)
	if r.Items == nil {
		t.Fatal("nil items must become []")
	}
	if len(r.Items) != 0 {
		t.Fatalf("len = %d, want 0", len(r.Items))
	}
	if r.Total != 0 || r.Page != 1 || r.PageSize != 20 {
		t.Fatalf("fields = %+v", r)
	}
}

func TestNewOffsetResultPassthrough(t *testing.T) {
	items := []int{1, 2, 3}
	r := NewOffsetResult(items, OffsetParams{Page: 2, PageSize: 3}, 10)
	if !reflect.DeepEqual(r.Items, items) {
		t.Fatalf("items = %v", r.Items)
	}
	if r.Page != 2 || r.PageSize != 3 || r.Total != 10 {
		t.Fatalf("fields = %+v", r)
	}
}
