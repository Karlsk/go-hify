package logging

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/Karlsk/go-hify/internal/platform/traceid"
)

// traceHandler 测试：ctx 带 trace_id → 记录自动追加字段；不带 → 不追加；
// 调用方显式 trace_id 不被覆盖；Enabled / WithAttrs / WithGroup 语义保持；Init 自动接线。

func TestTraceHandler_AddsTraceID(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(newTraceHandler(slog.NewJSONHandler(&buf, nil)))
	ctx := traceid.With(context.Background(), "tid-abc")

	logger.InfoContext(ctx, "request done", "status", 200)

	out := buf.String()
	for _, want := range []string{`"trace_id":"tid-abc"`, `"status":200`, `"msg":"request done"`} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q missing %s", out, want)
		}
	}
}

func TestTraceHandler_NoTraceInCtx(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(newTraceHandler(slog.NewJSONHandler(&buf, nil)))

	logger.Info("no trace in ctx")

	if strings.Contains(buf.String(), "trace_id") {
		t.Errorf("output %q must not contain trace_id when ctx carries none", buf.String())
	}
}

func TestTraceHandler_CallerAttrWins(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(newTraceHandler(slog.NewJSONHandler(&buf, nil)))
	ctx := traceid.With(context.Background(), "from-ctx")

	// 调用方显式带 trace_id（如后台任务自带关联 id）：不被 ctx 值覆盖。
	logger.InfoContext(ctx, "explicit wins", "trace_id", "explicit")

	out := buf.String()
	if !strings.Contains(out, `"trace_id":"explicit"`) {
		t.Errorf("output %q must keep the caller-provided trace_id", out)
	}
	if strings.Contains(out, "from-ctx") {
		t.Errorf("output %q must not duplicate ctx trace_id over caller attr", out)
	}
}

func TestTraceHandler_EnabledPassthrough(t *testing.T) {
	cases := []struct {
		name  string
		level slog.Level
		want  bool
	}{
		{"debug blocked at info level", slog.LevelDebug, false},
		{"info passes at info level", slog.LevelInfo, true},
		{"error passes at info level", slog.LevelError, true},
	}
	h := newTraceHandler(slog.NewJSONHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelInfo}))
	for _, tc := range cases {
		if got := h.Enabled(context.Background(), tc.level); got != tc.want {
			t.Errorf("%s: Enabled = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestTraceHandler_WithAttrsKeepsWrapping(t *testing.T) {
	var buf bytes.Buffer
	base := newTraceHandler(slog.NewJSONHandler(&buf, nil))
	derived := base.WithAttrs([]slog.Attr{slog.String("component", "db")})

	if _, ok := derived.(*traceHandler); !ok {
		t.Fatalf("WithAttrs must keep the trace wrapper outermost, got %T", derived)
	}
	slog.New(derived).InfoContext(traceid.With(context.Background(), "tid-wa"), "derived log")

	out := buf.String()
	if !strings.Contains(out, `"component":"db"`) || !strings.Contains(out, `"trace_id":"tid-wa"`) {
		t.Errorf("output %q must contain both WithAttrs fields and trace_id", out)
	}
}

func TestTraceHandler_WithGroupKeepsWrapping(t *testing.T) {
	var buf bytes.Buffer
	base := newTraceHandler(slog.NewJSONHandler(&buf, nil))
	derived := base.WithGroup("g")

	if _, ok := derived.(*traceHandler); !ok {
		t.Fatalf("WithGroup must keep the trace wrapper outermost, got %T", derived)
	}
	slog.New(derived).InfoContext(traceid.With(context.Background(), "tid-wg"), "grouped log")

	if !strings.Contains(buf.String(), "trace_id") {
		t.Errorf("output %q must still carry trace_id under grouping", buf.String())
	}
}

func TestInit_DefaultLoggerHasTraceHandler(t *testing.T) {
	if _, err := Init(Config{Level: "info", Format: "text"}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { slog.SetDefault(slog.New(slog.DiscardHandler)) })

	if _, ok := slog.Default().Handler().(*traceHandler); !ok {
		t.Fatalf("Init must wrap the default handler with traceHandler, got %T", slog.Default().Handler())
	}
}
