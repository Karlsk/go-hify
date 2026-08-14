// Package timex 统一 Hify 的时间序列化约定。
//
// 时间序列化约定（CLAUDE.md《字段命名与类型》）：
//
//   - datetime 字段继续用标准库 time.Time。Go 的 time.Time.MarshalJSON 默认输出
//     RFC 3339 字符串（ISO 8601 的 profile），且永远不会输出 Unix 时间戳——Java 里
//     「关掉 WRITE_DATES_AS_TIMESTAMPS」在 Go 是天然满足的 no-op，无需任何开关。
//     来自 PG timestamptz 的时间存的是 UTC，序列化出来天然带 Z。
//
//   - 纯日期字段用本包的 Date（无时分秒），序列化为 "2006-01-02"；可空日期用 *Date
//     （nil → JSON null，非 nil → Date 的输出）。Go 没有内置 Date 类型，本类型补这块空白。
//
// 本包只 import 标准库（time / fmt / encoding/json），不引入 gin / gorm，
// 保持可被各模块的 api/ 纯叶子包安全 import。
package timex

import (
	"encoding/json"
	"fmt"
	"time"
)

// 布局常量。
const (
	// DateTimeLayout 是 datetime 字段的规范布局（RFC 3339，秒精度，带时区）。
	// schema 的 datetime 字段用 time.Time，其 MarshalJSON 默认即按 RFC 3339 输出——
	// 本常量仅供手写格式化 / 解析时参考，schema 无需引用。
	DateTimeLayout = time.RFC3339 // "2006-01-02T15:04:05Z07:00"

	// DateLayout 是纯日期字段的布局（yyyy-MM-dd）。
	DateLayout = "2006-01-02"
)

// Date 是纯日期（无时分秒），底层是 time.Time。
// datetime 字段不要用 Date，继续用 time.Time（标准库默认 RFC 3339）。
type Date time.Time

// NewDate 由 time.Time 构造 Date（保留 t 自身的时区；日期边界取决于该时区）。
func NewDate(t time.Time) Date { return Date(t) }

// ParseDate 解析 "2006-01-02" 字符串为 Date（按 UTC 解析，时分秒为零）。
func ParseDate(s string) (Date, error) {
	t, err := time.Parse(DateLayout, s)
	if err != nil {
		return Date{}, err
	}
	return Date(t), nil
}

// Time 返回底层的 time.Time。
func (d Date) Time() time.Time { return time.Time(d) }

// String 返回 "2006-01-02" 格式字符串（零值 Date → "0001-01-01"）。
func (d Date) String() string { return d.Time().Format(DateLayout) }

// MarshalJSON 输出带引号的 "2006-01-02"。
func (d Date) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf("%q", d.String())), nil
}

// UnmarshalJSON 解析 "2006-01-02"。非字符串或格式非法均返回 error。
func (d *Date) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("timex.Date: expect JSON string, got %s: %w", b, err)
	}
	t, err := time.Parse(DateLayout, s)
	if err != nil {
		return fmt.Errorf("timex.Date: invalid date %q (want %s): %w", s, DateLayout, err)
	}
	*d = Date(t)
	return nil
}

// MarshalText 输出 "2006-01-02"（无引号），让 Date 可用于 form / query / uri 绑定。
func (d Date) MarshalText() ([]byte, error) {
	return []byte(d.String()), nil
}

// UnmarshalText 解析 "2006-01-02"，让 Date 可用于 form / query / uri 绑定（gin 文本解码）。
func (d *Date) UnmarshalText(text []byte) error {
	t, err := time.Parse(DateLayout, string(text))
	if err != nil {
		return fmt.Errorf("timex.Date: invalid date %q (want %s): %w", text, DateLayout, err)
	}
	*d = Date(t)
	return nil
}
