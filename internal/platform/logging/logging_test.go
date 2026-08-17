package logging

import (
	"log/slog"
	"path/filepath"
	"testing"
)

// logging.go 测试：级别解析、Init 文件落盘分支、非法级别 fail-fast。

func TestParseLevel(t *testing.T) {
	cases := []struct {
		in      string
		want    slog.Level
		wantErr bool
	}{
		{"debug", slog.LevelDebug, false},
		{"INFO", slog.LevelInfo, false}, // 大小写不敏感
		{"warn", slog.LevelWarn, false},
		{"warning", slog.LevelWarn, false},
		{"error", slog.LevelError, false},
		{"verbose", 0, true},
		{"", 0, true},
	}
	for _, tc := range cases {
		got, err := parseLevel(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseLevel(%q) must fail", tc.in)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("parseLevel(%q) = (%v, %v), want (%v, nil)", tc.in, got, err, tc.want)
		}
	}
}

func TestInit_InvalidLevel(t *testing.T) {
	if _, err := Init(Config{Level: "loud"}); err == nil {
		t.Fatal("Init with invalid level must fail (fail-fast, not silently go live)")
	}
}

func TestInit_FileAndClose(t *testing.T) {
	f := filepath.Join(t.TempDir(), "sub", "hify.log") // 父目录不存在 → 自动创建
	lg, err := Init(Config{Level: "info", Format: "json", File: f})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	lg.Info("file write probe")
	if err := Close(); err != nil { // 收尾文件 writer（组合根 graceful shutdown 同款路径）
		t.Fatalf("Close: %v", err)
	}
}

func TestInit_DefaultsZeroConfig(t *testing.T) {
	if _, err := Init(Config{}); err != nil { // 全零值走默认：info / json / 只 stdout
		t.Fatalf("Init zero config: %v", err)
	}
	t.Cleanup(func() { slog.SetDefault(slog.New(slog.DiscardHandler)) })
}
