package orchestrator

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/storage"
)

func newSQLiteTestQueue(t *testing.T, lease time.Duration) *SQLiteRunQueue {
	t.Helper()
	dir := t.TempDir()
	client, err := storage.NewSQLiteClient(context.Background(), storage.SQLiteConfig{
		Path:        filepath.Join(dir, "queue.db"),
		TablePrefix: "test",
	})
	if err != nil {
		t.Fatalf("NewSQLiteClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	queue, err := NewSQLiteRunQueue(context.Background(), client, lease)
	if err != nil {
		t.Fatalf("NewSQLiteRunQueue: %v", err)
	}
	return queue
}

func TestSQLiteRunQueue_RejectsNilClient(t *testing.T) {
	t.Parallel()
	if _, err := NewSQLiteRunQueue(context.Background(), nil, time.Second); err == nil {
		t.Fatal("expected error for nil client")
	}
}

func TestSQLiteRunQueue_TimestampFormatSortsWithinSameSecond(t *testing.T) {
	t.Parallel()

	early := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	later := early.Add(time.Nanosecond)

	earlyText := formatSQLiteRunQueueTime(early)
	laterText := formatSQLiteRunQueueTime(later)
	if earlyText >= laterText {
		t.Fatalf("fixed-width sqlite timestamp should sort chronologically: %q >= %q", earlyText, laterText)
	}
	if earlyText == early.Format(time.RFC3339Nano) {
		t.Fatalf("timestamp format must keep fractional seconds at exact second boundary: %q", earlyText)
	}
}

func TestSQLiteRunQueue_EnqueueClaimRoundTrip(t *testing.T) {
	t.Parallel()
	q := newSQLiteTestQueue(t, 5*time.Second)
	ctx := context.Background()

	if err := q.Enqueue(ctx, QueueJob{TaskID: "task_1", RunID: "run_1"}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	depth, err := q.Depth(ctx)
	if err != nil {
		t.Fatalf("depth: %v", err)
	}
	if depth != 1 {
		t.Fatalf("depth = %d, want 1", depth)
	}

	claim, ok, err := q.Claim(ctx, "worker_a", 500*time.Millisecond)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if !ok {
		t.Fatal("expected claim")
	}
	if claim.Job.RunID != "run_1" || claim.Job.TaskID != "task_1" {
		t.Fatalf("unexpected claim: %+v", claim.Job)
	}
	if claim.LeaseUntil.IsZero() {
		t.Fatal("LeaseUntil should be set")
	}

	// Once claimed, depth (pending count) drops to zero.
	depth, err = q.Depth(ctx)
	if err != nil {
		t.Fatalf("depth after claim: %v", err)
	}
	if depth != 0 {
		t.Fatalf("depth after claim = %d, want 0", depth)
	}

	if err := q.Ack(ctx, claim.ClaimID); err != nil {
		t.Fatalf("ack: %v", err)
	}

	// Second claim against an empty queue should time out cleanly.
	_, ok, err = q.Claim(ctx, "worker_a", 50*time.Millisecond)
	if err != nil {
		t.Fatalf("claim empty: %v", err)
	}
	if ok {
		t.Fatal("expected no claim against empty queue")
	}
}

func TestSQLiteRunQueue_EnqueueDedupesByRunID(t *testing.T) {
	t.Parallel()
	q := newSQLiteTestQueue(t, time.Second)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := q.Enqueue(ctx, QueueJob{TaskID: "t", RunID: "run_dup"}); err != nil {
			t.Fatalf("enqueue #%d: %v", i, err)
		}
	}
	depth, err := q.Depth(ctx)
	if err != nil {
		t.Fatalf("depth: %v", err)
	}
	if depth != 1 {
		t.Fatalf("depth = %d, want 1 (ON CONFLICT(run_id) DO NOTHING)", depth)
	}
}

func TestSQLiteRunQueue_ExtendLease(t *testing.T) {
	t.Parallel()
	q := newSQLiteTestQueue(t, 500*time.Millisecond)
	ctx := context.Background()

	if err := q.Enqueue(ctx, QueueJob{TaskID: "t", RunID: "r"}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	claim, ok, err := q.Claim(ctx, "worker_a", 500*time.Millisecond)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	original := claim.LeaseUntil

	// Sleep a hair so the post-extension timestamp is unambiguously
	// later than the original lease.
	time.Sleep(20 * time.Millisecond)
	if err := q.ExtendLease(ctx, claim.ClaimID, 10*time.Second); err != nil {
		t.Fatalf("extend lease: %v", err)
	}

	// Re-read lease_until directly (no public getter — this asserts the
	// row was actually updated rather than just round-tripping the in-
	// memory claim struct).
	var leaseStr string
	if err := q.db.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT lease_until FROM %s WHERE id = ?`, q.table),
		claim.ClaimID,
	).Scan(&leaseStr); err != nil {
		t.Fatalf("re-read lease: %v", err)
	}
	leaseUntil, err := time.Parse(time.RFC3339Nano, leaseStr)
	if err != nil {
		t.Fatalf("parse lease_until: %v", err)
	}
	if !leaseUntil.After(original) {
		t.Fatalf("ExtendLease did not advance lease_until: original=%v new=%v", original, leaseUntil)
	}
}

func TestSQLiteRunQueue_AckRemovesRow(t *testing.T) {
	t.Parallel()
	q := newSQLiteTestQueue(t, time.Second)
	ctx := context.Background()

	if err := q.Enqueue(ctx, QueueJob{TaskID: "t", RunID: "r"}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	claim, ok, err := q.Claim(ctx, "worker_a", 500*time.Millisecond)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if err := q.Ack(ctx, claim.ClaimID); err != nil {
		t.Fatalf("ack: %v", err)
	}

	var count int
	if err := q.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s`, q.table)).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("after Ack: count = %d, want 0", count)
	}
}

func TestSQLiteRunQueue_NackRequeues(t *testing.T) {
	t.Parallel()
	q := newSQLiteTestQueue(t, 5*time.Second)
	ctx := context.Background()

	if err := q.Enqueue(ctx, QueueJob{TaskID: "t", RunID: "r"}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	claim, ok, err := q.Claim(ctx, "worker_a", 500*time.Millisecond)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if err := q.Nack(ctx, claim.ClaimID, "boom"); err != nil {
		t.Fatalf("nack: %v", err)
	}

	// Nack adds a 200ms backoff so the row isn't immediately reclaimed.
	// Wait long enough to clear it.
	time.Sleep(300 * time.Millisecond)

	again, ok, err := q.Claim(ctx, "worker_b", time.Second)
	if err != nil || !ok {
		t.Fatalf("re-claim after nack: ok=%v err=%v", ok, err)
	}
	if again.Job.RunID != "r" {
		t.Fatalf("unexpected re-claim run id: %s", again.Job.RunID)
	}
}

func TestSQLiteRunQueue_ExpiredLeaseCanBeReclaimed(t *testing.T) {
	t.Parallel()
	q := newSQLiteTestQueue(t, 20*time.Millisecond)
	ctx := context.Background()

	if err := q.Enqueue(ctx, QueueJob{TaskID: "task", RunID: "reclaim"}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	claim, ok, err := q.Claim(ctx, "worker_a", 50*time.Millisecond)
	if err != nil || !ok {
		t.Fatalf("first claim: ok=%v err=%v", ok, err)
	}
	if claim.Job.RunID != "reclaim" {
		t.Fatalf("first claim run id = %q, want reclaim", claim.Job.RunID)
	}

	time.Sleep(35 * time.Millisecond)

	reclaimed, ok, err := q.Claim(ctx, "worker_b", 100*time.Millisecond)
	if err != nil || !ok {
		t.Fatalf("reclaim claim: ok=%v err=%v", ok, err)
	}
	if reclaimed.Job.RunID != "reclaim" {
		t.Fatalf("reclaimed run id = %q, want reclaim", reclaimed.Job.RunID)
	}
	if reclaimed.ClaimID != claim.ClaimID {
		t.Fatalf("sqlite reuses row claim id = %q, want %q", reclaimed.ClaimID, claim.ClaimID)
	}
}

// TestSQLiteRunQueue_ConcurrentClaim is the load-bearing test for the
// SQLite lease semantics. Without the BEGIN IMMEDIATE / WAL writer-lock
// serialization, two goroutines racing to claim the same row would
// both succeed, which would in turn double-execute a task. We assert
// every successful claim has a unique id (and a unique run id), and
// that the total successful-claim count exactly matches the number of
// enqueued jobs.
func TestSQLiteRunQueue_ConcurrentClaim(t *testing.T) {
	t.Parallel()
	q := newSQLiteTestQueue(t, 5*time.Second)
	ctx := context.Background()

	const jobs = 32
	for i := 0; i < jobs; i++ {
		if err := q.Enqueue(ctx, QueueJob{
			TaskID: fmt.Sprintf("task_%d", i),
			RunID:  fmt.Sprintf("run_%d", i),
		}); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}

	const workers = 8
	type result struct {
		claimID string
		runID   string
	}
	results := make(chan result, jobs*2)

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			// Each worker keeps claiming until the queue drains.
			// Empty-queue claim attempts return ok=false after a
			// short waitFor, so the loop terminates naturally
			// once all jobs have been pulled.
			for {
				claim, ok, err := q.Claim(ctx, fmt.Sprintf("worker_%d", workerID), 200*time.Millisecond)
				if err != nil {
					t.Errorf("worker %d claim: %v", workerID, err)
					return
				}
				if !ok {
					return
				}
				results <- result{claimID: claim.ClaimID, runID: claim.Job.RunID}
				// Ack so we don't keep stealing this row's lease — though
				// the SELECT predicate excludes leased rows whose lease
				// hasn't expired, an Ack also exercises the delete path.
				if err := q.Ack(ctx, claim.ClaimID); err != nil {
					t.Errorf("worker %d ack: %v", workerID, err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(results)

	seenClaim := make(map[string]struct{}, jobs)
	seenRun := make(map[string]struct{}, jobs)
	for r := range results {
		if _, dup := seenClaim[r.claimID]; dup {
			t.Errorf("duplicate claim id %q — two goroutines claimed the same row", r.claimID)
		}
		seenClaim[r.claimID] = struct{}{}
		if _, dup := seenRun[r.runID]; dup {
			t.Errorf("duplicate run id %q — two goroutines claimed the same job", r.runID)
		}
		seenRun[r.runID] = struct{}{}
	}
	if len(seenClaim) != jobs {
		t.Fatalf("claimed %d distinct rows, want %d", len(seenClaim), jobs)
	}
}
