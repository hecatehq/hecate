package orchestrator

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/taskstate"
)

func TestRunQueueCoordinator_OwnsQueueAndInFlightJobs(t *testing.T) {
	t.Parallel()

	runner := &Runner{
		logger:   slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store:    taskstate.NewMemoryStore(),
		policies: make(map[string]struct{}),
	}
	firstQueue := NewMemoryRunQueue(4, time.Second)
	attachTestQueueCoordinator(runner, firstQueue)
	if got := runner.getQueue(); got != firstQueue {
		t.Fatalf("initial queue = %T, want first queue", got)
	}

	secondQueue := NewMemoryRunQueue(4, time.Second)
	runner.SetQueue(secondQueue)
	if got := runner.getQueue(); got != secondQueue {
		t.Fatalf("swapped queue = %T, want second queue", got)
	}

	cancelled := false
	runner.registerJob("run-coordinator", func() { cancelled = true })
	if got := runner.inFlightJobCount(); got != 1 {
		t.Fatalf("in-flight jobs = %d, want 1", got)
	}
	runner.cancelInFlightJob("run-coordinator")
	if !cancelled {
		t.Fatal("registered job was not cancelled")
	}
	runner.unregisterJob("run-coordinator")
	if got := runner.inFlightJobCount(); got != 0 {
		t.Fatalf("in-flight jobs after unregister = %d, want 0", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runner.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func TestRunnerQueueNilSafeDelegatesDoNotLazyInitializeCoordinator(t *testing.T) {
	t.Parallel()

	runner := &Runner{}
	if got := runner.getQueue(); got != nil {
		t.Fatalf("getQueue() = %T, want nil", got)
	}
	if got := runner.inFlightJobCount(); got != 0 {
		t.Fatalf("inFlightJobCount() = %d, want 0", got)
	}
	runner.cancelInFlightJob("run-missing")
	if runner.queueCoordinator != nil {
		t.Fatal("nil-safe queue delegates lazily initialized coordinator")
	}
}
