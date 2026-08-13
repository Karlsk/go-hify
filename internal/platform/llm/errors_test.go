package llm

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestClassSpecTable(t *testing.T) {
	tests := []struct {
		class     Class
		retryable bool
		breaker   bool
	}{
		{ClassTimeout, true, true},
		{ClassRateLimited, true, false},
		{ClassOverloaded, true, true},
		{ClassNetwork, true, true},
		{ClassInvalidRequest, false, false},
		{ClassAuth, false, false},
		{ClassProviderDown, false, true},
	}
	for _, tt := range tests {
		if got := tt.class.Retryable(); got != tt.retryable {
			t.Errorf("%s Retryable = %v, want %v", tt.class, got, tt.retryable)
		}
		if got := tt.class.CountsTowardBreaker(); got != tt.breaker {
			t.Errorf("%s CountsTowardBreaker = %v, want %v", tt.class, got, tt.breaker)
		}
	}
}

func TestClassifyThroughWrapping(t *testing.T) {
	base := &Error{Class: ClassAuth, Err: errors.New("bad key")}
	wrapped := errors.Join(errors.New("outer"), base)

	class, ok := Classify(wrapped)
	if !ok {
		t.Fatal("Classify should find class through wrapped error")
	}
	if class != ClassAuth {
		t.Fatalf("class = %s, want %s", class, ClassAuth)
	}
}

func TestClassifyUnclassified(t *testing.T) {
	if _, ok := Classify(errors.New("plain")); ok {
		t.Fatal("plain error should not be classified")
	}
}

func TestErrorRetryAfterCarried(t *testing.T) {
	e := &Error{Class: ClassRateLimited, RetryAfter: 7 * time.Second, Err: errors.New("429")}
	var got *Error
	if !errors.As(e, &got) {
		t.Fatal("errors.As failed")
	}
	if got.RetryAfter != 7*time.Second {
		t.Fatalf("RetryAfter = %v", got.RetryAfter)
	}
}

func TestClassifyCtxErrTimeoutCauses(t *testing.T) {
	for name, cause := range map[string]error{
		"ttft":    ErrTTFT,
		"idle":    ErrIdle,
		"overall": ErrOverall,
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(context.Background())
			cancel(cause)
			class, ok := Classify(classifyCtxErr(context.Canceled, ctx))
			if !ok || class != ClassTimeout {
				t.Fatalf("got (%s, %v), want ClassTimeout", class, ok)
			}
		})
	}
}

func TestClassifyCtxErrClientCancelKept(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(context.Canceled)
	got := classifyCtxErr(context.Canceled, ctx)
	if errors.Is(got, context.Canceled) == false {
		t.Fatalf("client cancel should pass through: %v", got)
	}
	if _, ok := Classify(got); ok {
		t.Fatal("client cancel must not be classified")
	}
}

func TestClassifyCtxErrClassifiedPassThrough(t *testing.T) {
	orig := &Error{Class: ClassInvalidRequest, Err: errors.New("400")}
	got := classifyCtxErr(orig, context.Background())
	if got != orig {
		t.Fatal("classified error should pass through unchanged")
	}
}
