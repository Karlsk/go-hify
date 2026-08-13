package llm

import (
	"testing"
	"time"
)

func TestDefaultProfileValues(t *testing.T) {
	p := DefaultProfile()
	tests := []struct {
		name string
		got  any
		want any
	}{
		{"Bulkhead", p.Bulkhead, 16},
		{"AcquireTimeout", p.AcquireTimeout, 5 * time.Second},
		{"TTFT", p.TTFT, 30 * time.Second},
		{"Idle", p.Idle, 30 * time.Second},
		{"Overall", p.Overall, 5 * time.Minute},
		{"MaxRetries", p.MaxRetries, 2},
		{"BackoffBase", p.BackoffBase, 500 * time.Millisecond},
		{"BackoffCap", p.BackoffCap, 8 * time.Second},
		{"RetryAfterCap", p.RetryAfterCap, 10 * time.Second},
		{"BreakerAfter", p.BreakerAfter, 5},
		{"BreakerCooldown", p.BreakerCooldown, 30 * time.Second},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %v, want %v", tt.name, tt.got, tt.want)
		}
	}
}

func TestProfileForKindOllamaTTFT(t *testing.T) {
	if got := ProfileForKind(KindOllama).TTFT; got != 120*time.Second {
		t.Fatalf("ollama TTFT = %v, want 120s", got)
	}
	// 其余 kind 走默认
	if got := ProfileForKind(KindOpenAI).TTFT; got != 30*time.Second {
		t.Fatalf("openai TTFT = %v, want 30s", got)
	}
}
