package taskstate

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/storage"
	"github.com/hecatehq/hecate/pkg/types"
)

func newSQLiteTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	dir := t.TempDir()
	client, err := storage.NewSQLiteClient(context.Background(), storage.SQLiteConfig{
		Path:        filepath.Join(dir, "taskstate.db"),
		TablePrefix: "test",
	})
	if err != nil {
		t.Fatalf("NewSQLiteClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	store, err := NewSQLiteStore(context.Background(), client)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	return store
}

func TestSQLiteStoreConformance(t *testing.T) {
	RunConformanceTests(t, "SQLiteStore", func(t *testing.T) Store {
		return newSQLiteTestStore(t)
	})
}

func TestSQLiteScheduleStoreConformance(t *testing.T) {
	RunScheduleStoreConformanceTests(t, "SQLiteStore", func(t *testing.T) scheduleConformanceStore {
		return newSQLiteTestStore(t)
	})
}

func TestSQLiteStore_RejectsNilClient(t *testing.T) {
	_, err := NewSQLiteStore(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil client")
	}
}

func TestSQLiteStore_BackendName(t *testing.T) {
	t.Parallel()
	store := newSQLiteTestStore(t)
	if got := store.Backend(); got != "sqlite" {
		t.Fatalf("Backend() = %q, want %q", got, "sqlite")
	}
}

func TestSQLiteScheduleStore_StoresCanonicalLexicalTimestamps(t *testing.T) {
	t.Parallel()
	store := newSQLiteTestStore(t)
	ctx := context.Background()
	nextRunAt := time.Date(2026, time.July, 20, 10, 0, 0, 123456789, time.UTC)
	createdAt := nextRunAt.Truncate(time.Second).Add(-time.Hour)
	claimedAt := nextRunAt.Add(987654321 * time.Nanosecond)
	if _, err := store.CreateTask(ctx, types.Task{ID: "task-schedule-time"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	mustCreateTaskSchedule(t, store, TaskSchedule{
		ID: "schedule-time", TaskID: "task-schedule-time", Kind: TaskScheduleKindOnce,
		Timezone: "UTC", RunAt: nextRunAt, Enabled: true, NextRunAt: nextRunAt,
		CreatedAt: createdAt, UpdatedAt: createdAt,
	})
	if _, applied, err := store.ClaimTaskScheduleOccurrence(ctx, TaskScheduleOccurrenceClaim{
		OccurrenceID: "occurrence-time", ScheduleID: "schedule-time", ScheduledFor: nextRunAt,
		ExpectedScheduleRevision: 1,
		ClaimOwner:               "worker-time", ClaimedAt: claimedAt,
	}); err != nil || !applied {
		t.Fatalf("ClaimTaskScheduleOccurrence = (%v, %v)", applied, err)
	}

	var runAtText, createdAtText string
	if err := store.db.QueryRowContext(ctx, `
		SELECT CAST(run_at AS TEXT), CAST(created_at AS TEXT)
		FROM `+store.schedulesTable+` WHERE id = ?
	`, "schedule-time").Scan(&runAtText, &createdAtText); err != nil {
		t.Fatalf("read schedule timestamp storage: %v", err)
	}
	if runAtText != nextRunAt.Format(taskScheduleSQLiteTimeLayout) || createdAtText != createdAt.Format(taskScheduleSQLiteTimeLayout) {
		t.Fatalf("stored schedule timestamps = %q/%q", runAtText, createdAtText)
	}
	var scheduledForText, claimedAtText string
	if err := store.db.QueryRowContext(ctx, `
		SELECT CAST(scheduled_for AS TEXT), CAST(claimed_at AS TEXT)
		FROM `+store.occurrencesTable+` WHERE id = ?
	`, "occurrence-time").Scan(&scheduledForText, &claimedAtText); err != nil {
		t.Fatalf("read occurrence timestamp storage: %v", err)
	}
	if scheduledForText != nextRunAt.Format(taskScheduleSQLiteTimeLayout) || claimedAtText != claimedAt.Format(taskScheduleSQLiteTimeLayout) {
		t.Fatalf("stored occurrence timestamps = %q/%q", scheduledForText, claimedAtText)
	}
}

func TestSQLiteStore_DeleteTaskRollsBackEveryChildDeleteOnFailure(t *testing.T) {
	t.Parallel()
	store := newSQLiteTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, time.July, 20, 10, 0, 0, 123456789, time.UTC)
	const (
		taskID     = "task-delete-rollback"
		runID      = "run-delete-rollback"
		scheduleID = "schedule-delete-rollback"
	)

	if _, err := store.CreateTask(ctx, types.Task{ID: taskID, Status: "completed", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := store.CreateRun(ctx, types.TaskRun{
		ID: runID, TaskID: taskID, Status: "completed", StartedAt: now, FinishedAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := store.AppendStep(ctx, types.TaskStep{ID: "step-delete-rollback", TaskID: taskID, RunID: runID, Status: "running", StartedAt: now}); err != nil {
		t.Fatalf("AppendStep: %v", err)
	}
	if _, err := store.CreateApproval(ctx, types.TaskApproval{ID: "approval-delete-rollback", TaskID: taskID, RunID: runID, Status: "pending", CreatedAt: now}); err != nil {
		t.Fatalf("CreateApproval: %v", err)
	}
	if _, err := store.CreateArtifact(ctx, types.TaskArtifact{ID: "artifact-delete-rollback", TaskID: taskID, RunID: runID, Status: "ready", CreatedAt: now}); err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}
	if _, err := store.AppendRunEvent(ctx, types.TaskRunEvent{TaskID: taskID, RunID: runID, EventType: "run.started", CreatedAt: now}); err != nil {
		t.Fatalf("AppendRunEvent: %v", err)
	}
	mustCreateTaskSchedule(t, store, TaskSchedule{
		ID: scheduleID, TaskID: taskID, Kind: TaskScheduleKindOnce, Timezone: "UTC",
		RunAt: now, Enabled: true, NextRunAt: now, CreatedAt: now, UpdatedAt: now,
	})
	if _, applied, err := store.ClaimTaskScheduleOccurrence(ctx, TaskScheduleOccurrenceClaim{
		OccurrenceID: "occurrence-delete-rollback", ScheduleID: scheduleID, ScheduledFor: now,
		ExpectedScheduleRevision: 1,
		ClaimOwner:               "worker-delete-rollback", ClaimedAt: now.Add(time.Second),
	}); err != nil || !applied {
		t.Fatalf("ClaimTaskScheduleOccurrence = (%v, %v)", applied, err)
	}

	if _, err := store.db.ExecContext(ctx, `
		CREATE TRIGGER fail_task_delete
		BEFORE DELETE ON `+store.tasksTable+`
		WHEN OLD.id = 'task-delete-rollback'
		BEGIN
			SELECT RAISE(ABORT, 'forced task delete failure');
		END
	`); err != nil {
		t.Fatalf("create delete trigger: %v", err)
	}
	if err := store.DeleteTask(ctx, taskID); err == nil {
		t.Fatal("DeleteTask succeeded, want forced failure")
	}

	assertCount := func(table, where string, want int) {
		t.Helper()
		var got int
		if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE "+where+" = ?", taskID).Scan(&got); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if got != want {
			t.Fatalf("%s rows after rollback = %d, want %d", table, got, want)
		}
	}
	assertCount(store.tasksTable, "id", 1)
	for _, table := range []string{
		store.runsTable,
		store.stepsTable,
		store.approvalsTable,
		store.artifactsTable,
		store.eventsTable,
		store.schedulesTable,
		store.occurrencesTable,
	} {
		assertCount(table, "task_id", 1)
	}
}

// equalStringSlice / equalStringMap are tiny helpers because
// reflect.DeepEqual treats nil and empty slice/map as different —
// for round-trip tests we want to consider them equivalent (empty
// JSON arrays and missing keys end up as nil after unmarshal).
func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalStringMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
