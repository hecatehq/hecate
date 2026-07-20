package taskstate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/storage"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestPostgresStoreConformance(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("HECATE_POSTGRES_TEST_URL"))
	if databaseURL == "" {
		t.Skip("set HECATE_POSTGRES_TEST_URL to run Postgres task-state conformance")
	}

	var sequence atomic.Uint64
	RunConformanceTests(t, "PostgresStore", func(t *testing.T) Store {
		t.Helper()
		prefix := fmt.Sprintf("ts_test_%d_%d", time.Now().UnixNano(), sequence.Add(1))
		client, err := storage.NewPostgresClient(context.Background(), storage.PostgresConfig{
			DatabaseURL: databaseURL,
			TablePrefix: prefix,
		})
		if err != nil {
			t.Fatalf("NewPostgresClient: %v", err)
		}
		store, err := NewPostgresStore(context.Background(), client)
		if err != nil {
			_ = client.Close()
			t.Fatalf("NewPostgresStore: %v", err)
		}
		t.Cleanup(func() {
			dropPostgresTaskStateTables(client)
			_ = client.Close()
		})
		return store
	})
}

func TestPostgresScheduleStoreConformance(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("HECATE_POSTGRES_TEST_URL"))
	if databaseURL == "" {
		t.Skip("set HECATE_POSTGRES_TEST_URL to run Postgres task-schedule conformance")
	}

	var sequence atomic.Uint64
	RunScheduleStoreConformanceTests(t, "PostgresStore", func(t *testing.T) scheduleConformanceStore {
		t.Helper()
		prefix := fmt.Sprintf("ts_schedule_test_%d_%d", time.Now().UnixNano(), sequence.Add(1))
		client, err := storage.NewPostgresClient(context.Background(), storage.PostgresConfig{
			DatabaseURL: databaseURL,
			TablePrefix: prefix,
		})
		if err != nil {
			t.Fatalf("NewPostgresClient: %v", err)
		}
		store, err := NewPostgresStore(context.Background(), client)
		if err != nil {
			_ = client.Close()
			t.Fatalf("NewPostgresStore: %v", err)
		}
		t.Cleanup(func() {
			dropPostgresTaskStateTables(client)
			_ = client.Close()
		})
		return store
	})
}

func TestPostgresScheduleClaimLocksTaskBeforeScheduleDelete(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("HECATE_POSTGRES_TEST_URL"))
	if databaseURL == "" {
		t.Skip("set HECATE_POSTGRES_TEST_URL to run PostgreSQL task-schedule lock-order coverage")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	client, err := storage.NewPostgresClient(ctx, storage.PostgresConfig{
		DatabaseURL: databaseURL,
		TablePrefix: fmt.Sprintf("ts_schedule_lock_%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("NewPostgresClient: %v", err)
	}
	store, err := NewPostgresStore(ctx, client)
	if err != nil {
		_ = client.Close()
		t.Fatalf("NewPostgresStore: %v", err)
	}
	t.Cleanup(func() {
		dropPostgresTaskStateTables(client)
		_ = client.Close()
	})

	const taskID = "schedule-task-claim-delete-lock-order"
	mustCreateScheduleTask(t, store, taskID)
	base := scheduleTestTime()
	schedule := mustCreateTaskSchedule(t, store, TaskSchedule{
		ID: "schedule-claim-delete-lock-order", TaskID: taskID,
		Kind: TaskScheduleKindOnce, Timezone: "UTC", RunAt: base,
		Enabled: true, NextRunAt: base,
	})

	// Hold the Schedule row so the claim remains in flight after taking its
	// parent Task lock. With the old Schedule-before-Task ordering, the claim
	// never reached the Task lock and a concurrent DeleteTask formed a cycle.
	scheduleBlocker, err := client.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx(schedule blocker): %v", err)
	}
	defer func() { _ = scheduleBlocker.Rollback() }()
	var blockedScheduleID string
	if err := scheduleBlocker.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT id FROM %s WHERE id = ? FOR UPDATE
	`, client.QualifiedTable("task_state_schedules")), schedule.ID).Scan(&blockedScheduleID); err != nil {
		t.Fatalf("lock Schedule row: %v", err)
	}

	type claimResult struct {
		occurrence TaskScheduleOccurrence
		applied    bool
		err        error
	}
	claimDone := make(chan claimResult, 1)
	go func() {
		occurrence, applied, err := store.ClaimTaskScheduleOccurrence(ctx, TaskScheduleOccurrenceClaim{
			OccurrenceID: "occurrence-claim-delete-lock-order",
			ScheduleID:   schedule.ID, ExpectedScheduleRevision: schedule.Revision,
			ScheduledFor: schedule.NextRunAt, ClaimOwner: "claim-delete-owner", ClaimedAt: base,
		})
		claimDone <- claimResult{occurrence: occurrence, applied: applied, err: err}
	}()

	if err := waitForPostgresTaskRowLock(ctx, client, taskID); err != nil {
		_ = scheduleBlocker.Rollback()
		t.Fatal(err)
	}
	deleteDone := make(chan error, 1)
	go func() { deleteDone <- store.DeleteTask(ctx, taskID) }()

	if err := scheduleBlocker.Rollback(); err != nil {
		t.Fatalf("release Schedule blocker: %v", err)
	}
	var claimed claimResult
	select {
	case claimed = <-claimDone:
	case <-ctx.Done():
		t.Fatalf("Schedule claim did not finish: %v", ctx.Err())
	}
	if claimed.err != nil || !claimed.applied || claimed.occurrence.TaskID != taskID {
		t.Fatalf("Schedule claim = (%+v, %v, %v), want applied before deletion", claimed.occurrence, claimed.applied, claimed.err)
	}
	select {
	case err := <-deleteDone:
		if err != nil {
			t.Fatalf("DeleteTask after claim: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("DeleteTask deadlocked with Schedule claim: %v", ctx.Err())
	}
	if _, found, err := store.GetTask(ctx, taskID); err != nil || found {
		t.Fatalf("Task after claim/delete = (found %v, error %v), want absent", found, err)
	}
	if _, found, err := store.GetTaskSchedule(ctx, schedule.ID); err != nil || found {
		t.Fatalf("Schedule after claim/delete = (found %v, error %v), want absent", found, err)
	}
}

func waitForPostgresTaskRowLock(ctx context.Context, client *storage.PostgresClient, taskID string) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var lockedTaskID string
		err := client.DB().QueryRowContext(ctx, fmt.Sprintf(`
			SELECT id FROM %s WHERE id = ? FOR UPDATE NOWAIT
		`, client.QualifiedTable("task_state_tasks")), taskID).Scan(&lockedTaskID)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "55P03" {
			return nil
		}
		if err != nil {
			return fmt.Errorf("probe Task row lock: %w", err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("Schedule claim did not lock parent Task before Schedule: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func dropPostgresTaskStateTables(client *storage.PostgresClient) {
	for _, table := range []string{
		"task_state_schedule_occurrences",
		"task_state_schedules",
		"task_state_run_events",
		"task_state_artifacts",
		"task_state_approvals",
		"task_state_steps",
		"task_state_runs",
		"task_state_tasks",
	} {
		_, _ = client.DB().ExecContext(
			context.Background(),
			"DROP TABLE IF EXISTS "+client.QualifiedTable(table),
		)
	}
}
