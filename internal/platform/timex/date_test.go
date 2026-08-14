package timex

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// timex.Date 的行为测试：JSON / Text 往返、格式精确、非法输入报错、
// 嵌入 struct 与可空 *Date（nil→null）的序列化、构造与解析函数。

// mustDate 构造测试用 Date（解析失败直接 Fatal）。
func mustDate(t *testing.T, s string) Date {
	t.Helper()
	d, err := ParseDate(s)
	if err != nil {
		t.Fatalf("ParseDate(%q): %v", s, err)
	}
	return d
}

func TestMarshalJSON_FormatExact(t *testing.T) {
	d := mustDate(t, "1990-05-20")
	b, err := d.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	if got := string(b); got != `"1990-05-20"` {
		t.Fatalf("MarshalJSON = %s, want %q", got, `"1990-05-20"`)
	}
}

func TestMarshalJSON_ZeroValue(t *testing.T) {
	// 零值 Date（未设置）序列化为 "0001-01-01"（time.Time 零值的日期）。
	var zero Date
	b, err := zero.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	if got := string(b); got != `"0001-01-01"` {
		t.Fatalf("zero MarshalJSON = %s, want %q", got, `"0001-01-01"`)
	}
}

func TestUnmarshalJSON(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		var d Date
		if err := d.UnmarshalJSON([]byte(`"2026-08-14"`)); err != nil {
			t.Fatalf("UnmarshalJSON: %v", err)
		}
		// 解析按 UTC，时分秒为零；只比对日期（格式化输出）。
		if got := d.String(); got != "2026-08-14" {
			t.Fatalf("got %s, want 2026-08-14", got)
		}
	})

	t.Run("invalid format", func(t *testing.T) {
		var d Date
		err := d.UnmarshalJSON([]byte(`"2026/08/14"`)) // 错误分隔符
		if err == nil {
			t.Fatal("invalid date should error")
		}
	})

	t.Run("not a string", func(t *testing.T) {
		var d Date
		err := d.UnmarshalJSON([]byte(`20260814`)) // 裸数字
		if err == nil {
			t.Fatal("non-string JSON should error")
		}
	})
}

// TestJSONRoundTrip 钉住 Marshal → Unmarshal 往返等价（含嵌入 struct 的 encoding/json 编解码路径）。
func TestJSONRoundTrip(t *testing.T) {
	type payload struct {
		BirthDate Date `json:"birth_date"`
	}
	in := payload{BirthDate: mustDate(t, "2000-01-02")}

	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"birth_date":"2000-01-02"}`
	if got := string(b); got != want {
		t.Fatalf("marshal = %s, want %s", got, want)
	}

	var out payload
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !out.BirthDate.Time().Equal(in.BirthDate.Time()) {
		t.Fatalf("round-trip mismatch: got %v, want %v", out.BirthDate, in.BirthDate)
	}
}

// TestNullableDate_PointerNilToNull 可空日期：*Date 为 nil 时序列化为 JSON null、非 nil 时正常输出。
func TestNullableDate_PointerNilToNull(t *testing.T) {
	t.Run("nil → null", func(t *testing.T) {
		type p struct {
			D *Date `json:"d"`
		}
		b, err := json.Marshal(p{D: nil})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if got := string(b); got != `{"d":null}` {
			t.Fatalf("nil pointer marshal = %s, want {\"d\":null}", got)
		}
	})

	t.Run("set → date", func(t *testing.T) {
		type p struct {
			D *Date `json:"d"`
		}
		d := mustDate(t, "2010-12-31")
		b, err := json.Marshal(p{D: &d})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if got := string(b); got != `{"d":"2010-12-31"}` {
			t.Fatalf("set pointer marshal = %s, want {\"d\":\"2010-12-31\"}", got)
		}
	})
}

func TestTextMarshalUnmarshal(t *testing.T) {
	// MarshalText 无引号。
	d := mustDate(t, "2026-08-14")
	b, err := d.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText: %v", err)
	}
	if got := string(b); got != "2026-08-14" {
		t.Fatalf("MarshalText = %s, want 2026-08-14", got)
	}

	// UnmarshalText 往返。
	var got Date
	if err := got.UnmarshalText([]byte("2026-08-14")); err != nil {
		t.Fatalf("UnmarshalText: %v", err)
	}
	if !got.Time().Equal(d.Time()) {
		t.Fatalf("text round-trip: got %v, want %v", got, d)
	}

	// 非法文本报错。
	var bad Date
	if err := bad.UnmarshalText([]byte("not-a-date")); err == nil {
		t.Fatal("invalid text should error")
	}
}

func TestStringAndTime(t *testing.T) {
	d := mustDate(t, "1999-12-31")
	if got := d.String(); got != "1999-12-31" {
		t.Fatalf("String = %s, want 1999-12-31", got)
	}
	// Time() 返回底层；解析按 UTC，故 Location == UTC。
	if loc := d.Time().Location(); loc != time.UTC {
		t.Fatalf("Location = %v, want UTC", loc)
	}
}

func TestNewDate(t *testing.T) {
	// NewDate 保留传入 time 的时区与日期。
	tz, _ := time.LoadLocation("Asia/Shanghai")
	src := time.Date(2026, 8, 14, 23, 0, 0, 0, tz) // 北京时间 8/14 23:00
	d := NewDate(src)
	// 在 Asia/Shanghai 时区下仍是 8/14。
	if got, want := d.String(), "2026-08-14"; got != want {
		t.Fatalf("NewDate from Shanghai time = %s, want %s", got, want)
	}
}

func TestParseDate_Invalid(t *testing.T) {
	if _, err := ParseDate("bad"); err == nil {
		t.Fatal("ParseDate should error on invalid input")
	}
	// ParseDate 成功路径已在 mustDate 间接覆盖；这里补一条直接断言。
	d, err := ParseDate("2020-02-29") // 闰年合法
	if err != nil {
		t.Fatalf("ParseDate(2020-02-29): %v", err)
	}
	if d.String() != "2020-02-29" {
		t.Fatalf("got %s", d)
	}
	// 非法闰年。
	if _, err := ParseDate("2021-02-29"); err == nil {
		t.Fatal("2021-02-29 should be invalid")
	}
}

// TestConstants 断言布局常量值钉死（被外部格式化引用时不能漂移）。
func TestConstants(t *testing.T) {
	if DateTimeLayout != time.RFC3339 {
		t.Fatalf("DateTimeLayout = %q, want RFC3339", DateTimeLayout)
	}
	if DateLayout != "2006-01-02" {
		t.Fatalf("DateLayout = %q", DateLayout)
	}
}

// TestErrorsWrapUnwrap 确保错误链可被 errors.Is/As 识别（%w 包装不吞错）。
func TestErrorsWrapUnwrap(t *testing.T) {
	var d Date
	err := d.UnmarshalJSON([]byte(`"bad"`))
	if err == nil {
		t.Fatal("expected error")
	}
	// 底层是 time.ParseError，%w 包装后应能 As 出来。
	var pe *time.ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("error should wrap time.ParseError, got %T: %v", err, err)
	}
}
