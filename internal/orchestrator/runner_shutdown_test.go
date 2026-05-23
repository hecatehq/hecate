package orchestrator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/taskstate"
)

// newRunnerForShutdownTest is a thin wrapper around NewRunner that
// keeps the per-test boilerplate (logger / store / workers count) out
// of every shutdown test below. We use NewRunner rather than the
// minimal helpers in runner_race_test.go because shutdown is what
// NewRunner uniquely sets up — workerCtx, workerCancel, workerWg —
// and we want to pin those wirings, not bypass them.
func newRunnerForShutdownTest(t *testing.T, workers int) *Runner {
	t.Helper()
	return NewRunner(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		taskstate.NewMemoryStore(),
		nil,
		Config{QueueWorkers: workers},
	)
}

// TestRunner_Shutdown_NoInflight_ReturnsQuickly: with no jobs running,
// Shutdown only waits on the queue-worker goroutines, which observe
// the cancelled workerCtx on their next Claim attempt and return.
// Should finish well inside one second; we test for it because a slow
// path here would compound on real shutdowns where the runner sits
// behind the HTTP server's drain.
func TestRunner_Shutdown_NoInflight_ReturnsQuickly(t *testing.T) {
	t.Parallel()
	runner := newRunnerForShutdownTest(t, 2)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	if err := runner.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Shutdown took %v with no in-flight work, expected <1s", elapsed)
	}

	// Idempotent: a second Shutdown after the first must not panic and
	// must not block. Calling shutdown twice is a real risk in main.go
	// — a panic during normal shutdown could trigger a deferred
	// shutdown that re-enters this code.
	if err := runner.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown (second call): %v", err)
	}
}

// TestRunner_Shutdown_CancelsInflightJobs pins the core invariant:
// Shutdown propagates cancellation through workerCtx into every
// in-flight job's context so the agent loop's deferred cleanup
// (Pool.Close → MCP subprocess teardown) actually runs.
//
// The test goroutine mimics processQueuedRun's wiring: parent its
// context off runner.workerCtx, register the cancel func in r.jobs,
// and count itself via workerWg. That way the test exercises the same
// cancellation cascade the real worker uses, not a hand-rolled
// approximation.
func TestRunner_Shutdown_CancelsInflightJobs(t *testing.T) {
	t.Parallel()
	runner := newRunnerForShutdownTest(t, 1)

	jobStarted := make(chan struct{})
	jobErr := make(chan error, 1)

	runner.workerWg.Add(1)
	go func() {
		defer runner.workerWg.Done()
		jobCtx, jobCancel := context.WithCancel(runner.workerCtx)
		defer jobCancel()
		runner.registerJob("run-shutdown-test", jobCancel)
		defer runner.unregisterJob("run-shutdown-test")
		close(jobStarted)
		<-jobCtx.Done()
		jobErr <- jobCtx.Err()
	}()
	<-jobStarted

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	if err := runner.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Shutdown took %v while draining one in-flight job, expected <1s", elapsed)
	}

	select {
	case err := <-jobErr:
		// We only require *cancellation* — DeadlineExceeded would also
		// indicate an upstream timeout we'd want to know about.
		if !errors.Is(err, context.Canceled) {
			t.Errorf("job ctx err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("job did not unblock after Shutdown returned")
	}
}

// TestRunner_Shutdown_DeadlineExceeded covers the bad-citizen path: a
// job ignores cancellation. Shutdown must NOT block forever — it
// returns the caller's ctx error so main.go can decide whether to
// force-exit. The stuck goroutine continues in the background; that's
// the price of a tight deadline, and orphaned goroutines are
// preferable to a wedged shutdown.
func TestRunner_Shutdown_DeadlineExceeded(t *testing.T) {
	t.Parallel()
	runner := newRunnerForShutdownTest(t, 1)

	// Spawn a goroutine that ignores cancellation. We hold it open via
	// a release channel so the test can clean up at the end without
	// leaking the goroutine into the rest of the package's tests.
	release := make(chan struct{})
	runner.workerWg.Add(1)
	go func() {
		defer runner.workerWg.Done()
		<-release
	}()
	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := runner.Shutdown(ctx)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Shutdown err = %v, want context.DeadlineExceeded", err)
	}
	// Sanity-check that Shutdown waited for the deadline rather than
	// returning instantly (which would suggest the wait path was
	// short-circuited and we lost the drain guarantee on the happy
	// path too).
	if elapsed < 50*time.Millisecond {
		t.Errorf("Shutdown returned in %v, expected ~deadline", elapsed)
	}
}

// TestRunner_Shutdown_StopsClaimingNewWork: even with no in-flight
// jobs, the queue workers themselves are goroutines that count
// against workerWg. Shutdown's drain wait must include them, otherwise
// a worker mid-Claim could keep running past Shutdown's return and
// pick up one final job after main() thinks the runner is done. We
// test by enqueueing a job AFTER Shutdown has fired and verifying it
// is never claimed.
func TestRunner_Shutdown_StopsClaimingNewWork(t *testing.T) {
	t.Parallel()
	runner := newRunnerForShutdownTest(t, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := runner.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	// Enqueue after shutdown — the worker is gone, so the claim will
	// sit there forever. We don't assert on Enqueue's error (the
	// in-memory queue accepts it; what we care about is that nothing
	// claims it within a reasonable window).
	q := runner.getQueue()
	if q == nil {
		t.Skip("no queue wired; nothing to assert")
	}
	if err := q.Enqueue(context.Background(), QueueJob{TaskID: "task-x", RunID: "run-x"}); err != nil {
		// Some queue impls reject after close; that's also a valid
		// "stopped claiming new work" outcome.
		return
	}

	// Give a defunct worker a chance to claim — it shouldn't.
	time.Sleep(100 * time.Millisecond)

	depth, err := q.Depth(context.Background())
	if err != nil {
		t.Fatalf("Depth: %v", err)
	}
	if depth == 0 {
		t.Errorf("queue depth = 0 after post-shutdown enqueue, want 1 (work was claimed by a still-running worker)")
	}
}
