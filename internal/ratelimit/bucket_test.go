package ratelimit_test

import (
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/ratelimit"
)

func TestStoreAllowConsumesToken(t *testing.T) {
	// 5 capacity, 5/min refill — each Allow should decrement remaining.
	s := ratelimit.NewStore(5, 5)
	limit, remaining, _, err := s.Allow("key1")
	if err != nil {
		t.Fatalf("unexpected error on first Allow: %v", err)
	}
	if limit != 5 {
		t.Errorf("limit = %d, want 5", limit)
	}
	if remaining != 4 {
		t.Errorf("remaining = %d, want 4", remaining)
	}
}

func TestStoreAllowExhaustsAndReturnsError(t *testing.T) {
	s := ratelimit.NewStore(3, 60)
	for i := 0; i < 3; i++ {
		if _, _, _, err := s.Allow("key2"); err != nil {
			t.Fatalf("call %d: unexpected error: %v", i+1, err)
		}
	}
	_, remaining, _, err := s.Allow("key2")
	if err == nil {
		t.Fatal("expected ExceededError after exhausting bucket, got nil")
	}
	if remaining != 0 {
		t.Errorf("remaining = %d, want 0", remaining)
	}
	var exceeded *ratelimit.ExceededError
	if ok := isExceededError(err, &exceeded); !ok {
		t.Fatalf("error type = %T, want *ratelimit.ExceededError", err)
	}
	if exceeded.Limit != 3 {
		t.Errorf("ExceededError.Limit = %d, want 3", exceeded.Limit)
	}
}

func TestStorePerKeyIsolation(t *testing.T) {
	s := ratelimit.NewStore(1, 60)
	// Exhaust key "a"
	if _, _, _, err := s.Allow("a"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, _, _, err := s.Allow("a"); err == nil {
		t.Fatal("expected rate limit for key 'a' after exhaustion")
	}
	// key "b" should be unaffected
	if _, _, _, err := s.Allow("b"); err != nil {
		t.Fatalf("key 'b' unexpectedly rate-limited: %v", err)
	}
}

func TestStoreResetAtInFuture(t *testing.T) {
	s := ratelimit.NewStore(1, 60)
	// Exhaust the bucket.
	s.Allow("k") //nolint:errcheck
	_, _, resetAt, _ := s.Allow("k")
	if resetAt.Before(time.Now()) {
		t.Errorf("resetAt %v is in the past; expected a future time when bucket refills", resetAt)
	}
}

func TestNewStoreDefaults(t *testing.T) {
	// Zero/negative values should use defaults (capacity=60, rpm=60).
	s := ratelimit.NewStore(0, 0)
	limit, _, _, err := s.Allow("x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if limit != 60 {
		t.Errorf("default limit = %d, want 60", limit)
	}
}

func TestExceededErrorMessage(t *testing.T) {
	e := &ratelimit.ExceededError{Limit: 10, ResetAt: time.Now().Add(30 * time.Second)}
	msg := e.Error()
	if msg == "" {
		t.Error("ExceededError.Error() returned empty string")
	}
}

// isExceededError is a type-assertion helper (avoids importing errors in test).
func isExceededError(err error, target **ratelimit.ExceededError) bool {
	e, ok := err.(*ratelimit.ExceededError)
	if ok {
		*target = e
	}
	return ok
}
