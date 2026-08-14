package api

import "errors"

var (
	// ErrDemoItemNotFound demo 条目不存在（404）。
	ErrDemoItemNotFound = errors.New("DEMO_ITEM_NOT_FOUND")
)
