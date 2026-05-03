package orchestrator

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/hecate/agent-runtime/internal/storage"
)

// SQLiteRunQueue mirrors the in-memory RunQueue surface with durable
// pending/leased status and lease-based reclaim semantics.
//
// SQLite-specific choices that aren't accidental:
//   - placeholders are `?` rather than `$N`.
//   - id column is INTEGER PRIMARY KEY AUTOINCREMENT (the SQLite idiom).
//   - timestamps are stored as TEXT (RFC3339Nano) rather than TIMESTAMPTZ.
//     SQLite has no native timestamp type; storing strings keeps the
//     ordering correct (RFC3339 sorts lexicographically) and dodges the
//     epoch-vs-iso bikeshed.
//   - lease claim uses BEGIN IMMEDIATE + a SELECT-then-UPDATE pair under
//     the writer lock instead of SELECT ... FOR UPDATE SKIP LOCKED.
//     SQLite has no row-level locks; the database file lock under WAL
//     mode serializes writers, which is what we need for at-most-once
//     claim semantics. BEGIN IMMEDIATE acquires the writer lock up
//     front (vs the default deferred transaction, which upgrades on
//     first write and can deadlock when two readers both try to
//     upgrade).
type SQLiteRunQueue struct {
	db           *sql.DB
	table        string
	leaseFor     time.Duration
	pollInterval time.Duration
}

func NewSQLiteRunQueue(ctx context.Context, client *storage.SQLiteClient, leaseFor time.Duration) (*SQLiteRunQueue, error) {
	if client == nil || client.DB() == nil {
		return nil, fmt.Errorf("sqlite client is required")
	}
	if leaseFor <= 0 {
		leaseFor = 30 * time.Second
	}
	queue := &SQLiteRunQueue{
		db:           client.DB(),
		table:        client.QualifiedTable("task_run_queue"),
		leaseFor:     leaseFor,
		pollInterval: 100 * time.Millisecond,
	}
	if err := queue.migrate(ctx); err != nil {
		return nil, err
	}
	return queue, nil
}

func (q *SQLiteRunQueue) Backend() string { return "sqlite" }

func (q *SQLiteRunQueue) Enqueue(ctx context.Context, job QueueJob) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := q.db.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (task_id, run_id, status, available_at, lease_owner, lease_until, attempts, last_error, updated_at)
		VALUES (?, ?, 'pending', ?, '', NULL, 0, '', ?)
		ON CONFLICT(run_id) DO NOTHING
	`, q.table), job.TaskID, job.RunID, now, now)
	return err
}

func (q *SQLiteRunQueue) Claim(ctx context.Context, workerID string, waitFor time.Duration) (QueueClaim, bool, error) {
	if waitFor <= 0 {
		waitFor = 2 * time.Second
	}
	deadline := time.Now().Add(waitFor)
	for time.Now().Before(deadline) {
		claim, ok, err := q.claimOnce(ctx, workerID)
		if err != nil {
			return QueueClaim{}, false, err
		}
		if ok {
			return claim, true, nil
		}
		select {
		case <-ctx.Done():
			return QueueClaim{}, false, ctx.Err()
		case <-time.After(q.pollInterval):
		}
	}
	return QueueClaim{}, false, nil
}

func (q *SQLiteRunQueue) claimOnce(ctx context.Context, workerID string) (QueueClaim, bool, error) {
	// SQLite's writer lock serializes writes at the database-file level,
	// so the lease-claim semantics fall out naturally: two concurrent
	// claimers funnel through one writer at a time, and the loser's
	// SELECT inside the UPDATE sees the (already-leased) row and
	// matches nothing.
	//
	// We grab a dedicated connection from the pool and run BEGIN
	// IMMEDIATE on it explicitly. database/sql's BeginTx issues a
	// plain BEGIN (deferred), which acquires only a SHARED read lock;
	// upgrading on the UPDATE can race two claimers into SQLITE_BUSY.
	// BEGIN IMMEDIATE acquires RESERVED up front — the second claimer
	// blocks at BEGIN until the first commits, by which point the row
	// it would have picked has flipped to 'leased' and its SELECT
	// matches nothing.
	conn, err := q.db.Conn(ctx)
	if err != nil {
		return QueueClaim{}, false, err
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return QueueClaim{}, false, fmt.Errorf("begin immediate: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339Nano)
	leaseUntil := now.Add(q.leaseFor)
	leaseStr := leaseUntil.Format(time.RFC3339Nano)

	// One-shot UPDATE ... WHERE id = (SELECT ... LIMIT 1) RETURNING.
	// The subquery picks the next claimable row (pending+available, OR
	// a leased row whose lease has expired) and the outer UPDATE flips
	// it to 'leased' atomically.
	var (
		id     int64
		taskID string
		runID  string
	)
	err = conn.QueryRowContext(ctx, fmt.Sprintf(`
		UPDATE %s
		SET status = 'leased',
		    lease_owner = ?,
		    lease_until = ?,
		    attempts = attempts + 1,
		    updated_at = ?
		WHERE id = (
			SELECT id FROM %s
			WHERE (status = 'pending' AND available_at <= ?)
			   OR (status = 'leased' AND lease_until IS NOT NULL AND lease_until < ?)
			ORDER BY id ASC
			LIMIT 1
		)
		RETURNING id, task_id, run_id
	`, q.table, q.table), workerID, leaseStr, nowStr, nowStr, nowStr).Scan(&id, &taskID, &runID)
	if err == sql.ErrNoRows {
		if _, cerr := conn.ExecContext(ctx, "COMMIT"); cerr != nil {
			return QueueClaim{}, false, cerr
		}
		committed = true
		return QueueClaim{}, false, nil
	}
	if err != nil {
		return QueueClaim{}, false, err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return QueueClaim{}, false, err
	}
	committed = true
	return QueueClaim{
		ClaimID:    fmt.Sprintf("%d", id),
		Job:        QueueJob{TaskID: taskID, RunID: runID},
		LeaseUntil: leaseUntil,
	}, true, nil
}

func (q *SQLiteRunQueue) Ack(ctx context.Context, claimID string) error {
	_, err := q.db.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id = ?`, q.table), claimID)
	return err
}

func (q *SQLiteRunQueue) Nack(ctx context.Context, claimID, reason string) error {
	now := time.Now().UTC()
	// Back off briefly so a flapping job doesn't immediately re-claim
	// itself in a tight loop.
	availableAt := now.Add(200 * time.Millisecond).Format(time.RFC3339Nano)
	nowStr := now.Format(time.RFC3339Nano)
	_, err := q.db.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s
		SET status = 'pending',
		    available_at = ?,
		    lease_owner = '',
		    lease_until = NULL,
		    last_error = ?,
		    updated_at = ?
		WHERE id = ?
	`, q.table), availableAt, reason, nowStr, claimID)
	return err
}

func (q *SQLiteRunQueue) ExtendLease(ctx context.Context, claimID string, leaseFor time.Duration) error {
	if leaseFor <= 0 {
		leaseFor = q.leaseFor
	}
	now := time.Now().UTC()
	leaseUntil := now.Add(leaseFor).Format(time.RFC3339Nano)
	nowStr := now.Format(time.RFC3339Nano)
	_, err := q.db.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s
		SET lease_until = ?,
		    updated_at = ?
		WHERE id = ? AND status = 'leased'
	`, q.table), leaseUntil, nowStr, claimID)
	return err
}

func (q *SQLiteRunQueue) Depth(ctx context.Context) (int, error) {
	var count int
	err := q.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE status = 'pending'`, q.table)).Scan(&count)
	return count, err
}

// Capacity reports zero because SQLite has no in-memory channel-style
// cap — depth is only bounded by disk. The orchestrator
// uses this signal solely to gate the bounded MemoryRunQueue.
func (q *SQLiteRunQueue) Capacity() int {
	return 0
}

func (q *SQLiteRunQueue) migrate(ctx context.Context) error {
	_, err := q.db.ExecContext(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT NOT NULL,
			run_id TEXT NOT NULL UNIQUE,
			status TEXT NOT NULL DEFAULT 'pending',
			available_at TEXT NOT NULL,
			lease_owner TEXT NOT NULL DEFAULT '',
			lease_until TEXT NULL,
			attempts INTEGER NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL
		)
	`, q.table))
	if err != nil {
		return fmt.Errorf("migrate sqlite run queue: %w", err)
	}
	// Index name is unquoted by convention (see history_sqlite.go for
	// the rationale on quoting). The target table stays quoted.
	indexName := strings.Trim(q.table, `"`) + "_status_available_idx"
	_, err = q.db.ExecContext(ctx, fmt.Sprintf(`
		CREATE INDEX IF NOT EXISTS "%s"
		ON %s (status, available_at, lease_until)
	`, indexName, q.table))
	if err != nil {
		return fmt.Errorf("index sqlite run queue: %w", err)
	}
	return nil
}
