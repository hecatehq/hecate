package taskstate

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
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

func TestSQLiteStore_WorkspaceOwnerIndexDoesNotKeyWorkspacePath(t *testing.T) {
	t.Parallel()
	store := newSQLiteTestStore(t)
	indexName := storage.BoundedIdentifier(strings.Trim(store.runsTable, `"`) + "_workspace_owner_id_idx")
	var definition string
	if err := store.db.QueryRowContext(t.Context(), `
		SELECT sql
		FROM sqlite_master
		WHERE type = 'index' AND name = ?
	`, indexName).Scan(&definition); err != nil {
		t.Fatalf("read workspace owner index: %v", err)
	}
	keyDefinition := definition
	if before, _, ok := strings.Cut(definition, " WHERE "); ok {
		keyDefinition = before
	}
	if strings.Contains(strings.ToLower(keyDefinition), "workspace_path") {
		t.Fatalf("workspace owner index keys unbounded path: %s", definition)
	}
	if !strings.Contains(strings.ToLower(keyDefinition), "(id asc)") {
		t.Fatalf("workspace owner index = %s, want id-only cursor key", definition)
	}
}

func TestSQLiteStore_BackfillsLegacyRunWorkspaceProjection(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	client, err := storage.NewSQLiteClient(ctx, storage.SQLiteConfig{
		Path:        filepath.Join(t.TempDir(), "legacy-taskstate.db"),
		TablePrefix: "legacy_owner",
	})
	if err != nil {
		t.Fatalf("NewSQLiteClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	runsTable := client.QualifiedTable("task_state_runs")
	if _, err := client.DB().ExecContext(ctx, `
		CREATE TABLE `+runsTable+` (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			number INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT '',
			started_at TIMESTAMP NOT NULL DEFAULT '',
			payload TEXT NOT NULL
		)
	`); err != nil {
		t.Fatalf("create legacy runs table: %v", err)
	}
	run := types.TaskRun{
		ID:            "run-legacy-owner",
		TaskID:        "task-legacy-owner",
		Status:        "running",
		WorkspacePath: "/workspace/legacy-owner",
	}
	payload, err := json.Marshal(run)
	if err != nil {
		t.Fatalf("marshal legacy run: %v", err)
	}
	if _, err := client.DB().ExecContext(ctx, `
		INSERT INTO `+runsTable+` (id, task_id, number, status, started_at, payload)
		VALUES (?, ?, ?, ?, ?, ?)
	`, run.ID, run.TaskID, run.Number, run.Status, run.StartedAt, string(payload)); err != nil {
		t.Fatalf("insert legacy run: %v", err)
	}

	store, err := NewSQLiteStore(ctx, client)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	for _, trigger := range store.runWorkspaceProjectionTriggerNames() {
		if _, err := store.db.ExecContext(ctx, `DROP TRIGGER "`+trigger+`"`); err != nil {
			t.Fatalf("drop workspace projection trigger %s: %v", trigger, err)
		}
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE `+store.runsTable+` SET payload = '{' WHERE id = ?`, run.ID); err != nil {
		t.Fatalf("corrupt hydrated payload fixture: %v", err)
	}
	got, err := store.ListWorkspaceOwnerSummaries(ctx, "", 1)
	if err != nil {
		t.Fatalf("ListWorkspaceOwnerSummaries: %v", err)
	}
	if len(got) != 1 || got[0] != (WorkspaceOwnerSummary{ID: run.ID, Status: run.Status, WorkspacePath: run.WorkspacePath}) {
		t.Fatalf("legacy workspace owner summary = %+v", got)
	}
}

func TestSQLiteStore_WorkspaceProjectionTracksLegacyPayloadWrites(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := newSQLiteTestStore(t)
	run := types.TaskRun{
		ID:            "run-legacy-writer",
		TaskID:        "task-legacy-writer",
		Status:        "running",
		WorkspacePath: "/workspace/legacy-insert",
	}
	payload, err := json.Marshal(run)
	if err != nil {
		t.Fatalf("marshal inserted run: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO `+store.runsTable+` (id, task_id, number, status, started_at, payload)
		VALUES (?, ?, ?, ?, ?, ?)
	`, run.ID, run.TaskID, run.Number, run.Status, run.StartedAt, string(payload)); err != nil {
		t.Fatalf("raw legacy run insert: %v", err)
	}
	owners, err := store.ListWorkspaceOwnerSummaries(ctx, "", 2)
	if err != nil {
		t.Fatalf("ListWorkspaceOwnerSummaries(after insert): %v", err)
	}
	if len(owners) != 1 || owners[0].WorkspacePath != run.WorkspacePath {
		t.Fatalf("owners after raw insert = %+v, want projected workspace", owners)
	}
	run.WorkspacePath = "/workspace/legacy-update"
	payload, err = json.Marshal(run)
	if err != nil {
		t.Fatalf("marshal updated run: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE `+store.runsTable+` SET payload = ? WHERE id = ?`, string(payload), run.ID); err != nil {
		t.Fatalf("raw legacy run update: %v", err)
	}
	owners, err = store.ListWorkspaceOwnerSummaries(ctx, "", 2)
	if err != nil {
		t.Fatalf("ListWorkspaceOwnerSummaries(after update): %v", err)
	}
	if len(owners) != 1 || owners[0].WorkspacePath != run.WorkspacePath {
		t.Fatalf("owners after raw update = %+v, want refreshed workspace", owners)
	}
}

func TestSQLiteStore_LegacyRunWorkspaceProjectionMigrationIsAtomic(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	client, err := storage.NewSQLiteClient(ctx, storage.SQLiteConfig{
		Path:        filepath.Join(t.TempDir(), "invalid-legacy-taskstate.db"),
		TablePrefix: "invalid_legacy_owner",
	})
	if err != nil {
		t.Fatalf("NewSQLiteClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	runsTable := client.QualifiedTable("task_state_runs")
	if _, err := client.DB().ExecContext(ctx, `
		CREATE TABLE `+runsTable+` (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			number INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT '',
			started_at TIMESTAMP NOT NULL DEFAULT '',
			payload TEXT NOT NULL
		)
	`); err != nil {
		t.Fatalf("create legacy runs table: %v", err)
	}
	if _, err := client.DB().ExecContext(ctx, `
		INSERT INTO `+runsTable+` (id, task_id, status, payload)
		VALUES (?, ?, ?, ?)
	`, "run-invalid-payload", "task-invalid-payload", "running", "{"); err != nil {
		t.Fatalf("insert invalid legacy run: %v", err)
	}

	if _, err := NewSQLiteStore(ctx, client); err == nil || !strings.Contains(err.Error(), "backfill sqlite task run workspace projection") {
		t.Fatalf("NewSQLiteStore error = %v, want backfill failure", err)
	}
	exists, err := storage.ColumnExists(ctx, client, client.TableName("task_state_runs"), "workspace_path")
	if err != nil {
		t.Fatalf("ColumnExists: %v", err)
	}
	if exists {
		t.Fatal("workspace_path column survived failed transactional backfill")
	}
	for _, trigger := range []string{
		storage.BoundedIdentifier(client.TableName("task_state_runs") + "_workspace_path_v1_insert"),
		storage.BoundedIdentifier(client.TableName("task_state_runs") + "_workspace_path_v1_update"),
	} {
		var count int
		if err := client.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name = ?`, trigger).Scan(&count); err != nil {
			t.Fatalf("inspect rolled-back trigger %s: %v", trigger, err)
		}
		if count != 0 {
			t.Fatalf("workspace projection trigger %s survived failed transactional backfill", trigger)
		}
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
