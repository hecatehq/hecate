package providers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestMemoryHealthTrackerOpensAndRecoversAfterCooldown(t *testing.T) {
	t.Parallel()

	tracker := NewMemoryHealthTracker(2, 10*time.Second)
	now := time.Date(2026, 4, 21, 1, 0, 0, 0, time.UTC)
	tracker.now = func() time.Time { return now }

	tracker.RecordFailure("openai", context.DeadlineExceeded)
	if state := tracker.State("openai"); !state.Available || state.ConsecutiveFailures != 1 {
		t.Fatalf("state after first failure = %#v, want available with one failure", state)
	} else if state.Status != HealthStatusDegraded || state.Timeouts != 1 {
		t.Fatalf("state after first failure = %#v, want degraded with timeout tracked", state)
	}

	tracker.RecordFailure("openai", errors.New("temporary failure"))
	state := tracker.State("openai")
	if state.Available {
		t.Fatalf("state.Available = true, want false after threshold")
	}
	if state.Status != HealthStatusOpen {
		t.Fatalf("state.Status = %q, want %q", state.Status, HealthStatusOpen)
	}
	if state.ConsecutiveFailures != 2 {
		t.Fatalf("state.ConsecutiveFailures = %d, want 2", state.ConsecutiveFailures)
	}

	now = now.Add(11 * time.Second)
	state = tracker.State("openai")
	if !state.Available {
		t.Fatalf("state.Available = false, want true after cooldown")
	}
	if state.Status != HealthStatusHalfOpen {
		t.Fatalf("state.Status = %q, want %q after cooldown", state.Status, HealthStatusHalfOpen)
	}
	if !state.OpenUntil.IsZero() {
		t.Fatalf("state.OpenUntil = %v, want zero after cooldown", state.OpenUntil)
	}

	tracker.RecordSuccess("openai")
	state = tracker.State("openai")
	if state.ConsecutiveFailures != 0 {
		t.Fatalf("state.ConsecutiveFailures = %d, want 0 after success", state.ConsecutiveFailures)
	}
	if state.LastError != "" {
		t.Fatalf("state.LastError = %q, want empty after success", state.LastError)
	}
	if state.Status != HealthStatusHealthy {
		t.Fatalf("state.Status = %q, want %q after success", state.Status, HealthStatusHealthy)
	}
}

func TestMemoryHealthTrackerRateLimitOpensImmediately(t *testing.T) {
	t.Parallel()

	tracker := NewMemoryHealthTracker(3, 15*time.Second)
	now := time.Date(2026, 4, 29, 7, 0, 0, 0, time.UTC)
	tracker.now = func() time.Time { return now }

	tracker.RecordFailure("openai", &UpstreamError{StatusCode: http.StatusTooManyRequests, Type: "rate_limit"})
	state := tracker.State("openai")
	if state.Available {
		t.Fatalf("state.Available = true, want false after first rate limit")
	}
	if state.Status != HealthStatusOpen {
		t.Fatalf("state.Status = %q, want %q", state.Status, HealthStatusOpen)
	}
	if state.RateLimits != 1 {
		t.Fatalf("state.RateLimits = %d, want 1", state.RateLimits)
	}
	if state.ConsecutiveFailures != 1 {
		t.Fatalf("state.ConsecutiveFailures = %d, want 1", state.ConsecutiveFailures)
	}
	if state.OpenUntil.IsZero() {
		t.Fatalf("state.OpenUntil = zero, want cooldown deadline")
	}

	now = now.Add(16 * time.Second)
	state = tracker.State("openai")
	if !state.Available {
		t.Fatalf("state.Available = false, want true after cooldown")
	}
	if state.Status != HealthStatusHalfOpen {
		t.Fatalf("state.Status = %q, want %q after cooldown", state.Status, HealthStatusHalfOpen)
	}
}

func TestMemoryHealthTrackerRedactsInlineImageErrors(t *testing.T) {
	t.Parallel()

	payload := strings.Repeat("A", 128)
	tracker := NewMemoryHealthTracker(3, time.Minute)
	tracker.RecordFailure("openai", errors.New("bad data:image/png;base64,"+payload))
	lastError := tracker.State("openai").LastError
	if strings.Contains(lastError, payload) || !strings.Contains(lastError, "[redacted inline image]") {
		t.Fatalf("LastError = %q", lastError)
	}
}

func TestMemoryHealthTrackerMarksSlowSuccessAsDegraded(t *testing.T) {
	t.Parallel()

	tracker := NewMemoryHealthTrackerWithLatency(3, 15*time.Second, 500*time.Millisecond)
	now := time.Date(2026, 4, 29, 8, 0, 0, 0, time.UTC)
	tracker.now = func() time.Time { return now }

	tracker.Observe("openai", HealthObservation{Duration: 800 * time.Millisecond})
	state := tracker.State("openai")
	if !state.Available {
		t.Fatalf("state.Available = false, want true for slow-but-usable provider")
	}
	if state.Status != HealthStatusDegraded {
		t.Fatalf("state.Status = %q, want %q", state.Status, HealthStatusDegraded)
	}
	if state.LastErrorClass != "latency" {
		t.Fatalf("state.LastErrorClass = %q, want latency", state.LastErrorClass)
	}
	if state.LastLatency != 800*time.Millisecond {
		t.Fatalf("state.LastLatency = %v, want 800ms", state.LastLatency)
	}

	tracker.Observe("openai", HealthObservation{Duration: 120 * time.Millisecond})
	state = tracker.State("openai")
	if state.Status != HealthStatusHealthy {
		t.Fatalf("state.Status = %q, want %q after faster recovery", state.Status, HealthStatusHealthy)
	}
	if state.LastErrorClass != "" {
		t.Fatalf("state.LastErrorClass = %q, want empty after faster recovery", state.LastErrorClass)
	}
}
