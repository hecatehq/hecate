package taskstate

import (
	"context"
	"testing"
	"time"

	"github.com/hecatehq/hecate/pkg/types"
)

// StoreFactory builds a fresh Store for one conformance subtest.
// Each subtest gets its own factory invocation so backends with
// per-instance state (sqlite file under t.TempDir, fresh memory
// map) start clean. The factory is t.Helper()-friendly and may use
// t.Cleanup for teardown.
type StoreFactory func(t *testing.T) Store

// RunConformanceTests exercises every Store-interface contract the
// memory + sqlite backends both implement against the backend the
// factory produces. New backends added later only need to supply a
// factory + one entry-point test, not duplicate every case body.
//
// Per-backend tests that exercise something the contract doesn't
// describe (sqlite constructor validation, sqlite Backend() name
// assertion) stay as standalone tests in their backend's _test.go.
func RunConformanceTests(t *testing.T, name string, factory StoreFactory) {
	t.Helper()
	t.Run(name+"/TaskRunStepRoundTrip", func(t *testing.T) {
		t.Parallel()
		runStoreTaskRunStepRoundTrip(t, factory(t))
	})
	t.Run(name+"/ListTasksFilterAndLimit", func(t *testing.T) {
		t.Parallel()
		runStoreListTasksFilterAndLimit(t, factory(t))
	})
	t.Run(name+"/ApprovalRoundTrip", func(t *testing.T) {
		t.Parallel()
		runStoreApprovalRoundTrip(t, factory(t))
	})
	t.Run(name+"/ArtifactRoundTrip", func(t *testing.T) {
		t.Parallel()
		runStoreArtifactRoundTrip(t, factory(t))
	})
	t.Run(name+"/RunEventsAppendAndList", func(t *testing.T) {
		t.Parallel()
		runStoreRunEventsAppendAndList(t, factory(t))
	})
	t.Run(name+"/ListEventsCrossRunFilters", func(t *testing.T) {
		t.Parallel()
		runStoreListEventsCrossRunFilters(t, factory(t))
	})
	t.Run(name+"/ApplyRunTerminalTransition", func(t *testing.T) {
		t.Parallel()
		runStoreApplyRunTerminalTransition(t, factory(t))
	})
	t.Run(name+"/ApplyRunTerminalTransitionPreservesChildrenWithoutCancelFlags", func(t *testing.T) {
		t.Parallel()
		runStoreApplyRunTerminalTransitionPreservesChildrenWithoutCancelFlags(t, factory(t))
	})
	t.Run(name+"/ApplyRunTerminalTransitionRequiresStoredRunTaskMatch", func(t *testing.T) {
		t.Parallel()
		runStoreApplyRunTerminalTransitionRequiresStoredRunTaskMatch(t, factory(t))
	})
	t.Run(name+"/ListRunsByFilterStatusSet", func(t *testing.T) {
		t.Parallel()
		runStoreListRunsByFilterStatusSet(t, factory(t))
	})
	t.Run(name+"/DeleteTaskCascades", func(t *testing.T) {
		t.Parallel()
		runStoreDeleteTaskCascades(t, factory(t))
	})
	t.Run(name+"/TaskMCPServersRoundTrip", func(t *testing.T) {
		t.Parallel()
		runStoreTaskMCPServersRoundTrip(t, factory(t))
	})
}

func runStoreTaskRunStepRoundTrip(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()

	task := types.Task{
		ID:     "task-1",
		Title:  "demo",
		Status: "queued",
	}
	saved, err := store.CreateTask(ctx, task)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if saved.CreatedAt.IsZero() {
		t.Fatal("CreateTask did not stamp CreatedAt")
	}

	got, ok, err := store.GetTask(ctx, "task-1")
	if err != nil || !ok {
		t.Fatalf("GetTask: ok=%v err=%v", ok, err)
	}
	if got.Title != "demo" {
		t.Fatalf("GetTask round-trip mismatch: %+v", got)
	}

	run := types.TaskRun{
		ID:        "run-1",
		TaskID:    "task-1",
		Number:    1,
		Status:    "running",
		StartedAt: time.Now().UTC(),
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	gotRun, ok, err := store.GetRun(ctx, "task-1", "run-1")
	if err != nil || !ok {
		t.Fatalf("GetRun: ok=%v err=%v", ok, err)
	}
	if gotRun.Status != "running" || gotRun.Number != 1 {
		t.Fatalf("GetRun round-trip mismatch: %+v", gotRun)
	}

	for i, status := range []string{"running", "completed"} {
		step := types.TaskStep{
			ID:        "step-" + status,
			TaskID:    "task-1",
			RunID:     "run-1",
			Index:     i,
			Status:    status,
			StartedAt: time.Now().UTC(),
		}
		if _, err := store.AppendStep(ctx, step); err != nil {
			t.Fatalf("AppendStep(%s): %v", status, err)
		}
	}
	steps, err := store.ListSteps(ctx, "run-1")
	if err != nil {
		t.Fatalf("ListSteps: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("ListSteps len = %d, want 2", len(steps))
	}
	// step_index ASC ordering: index 0 first.
	if steps[0].Index != 0 || steps[1].Index != 1 {
		t.Fatalf("ListSteps ordering: %+v", steps)
	}
}

func runStoreListTasksFilterAndLimit(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()

	// Three tasks with staggered updated_at so ordering is
	// deterministic.
	now := time.Now().UTC()
	for i, spec := range []struct {
		id     string
		status string
		ts     time.Time
	}{
		{"t-a1", "queued", now.Add(-3 * time.Minute)},
		{"t-a2", "running", now.Add(-2 * time.Minute)},
		{"t-b1", "queued", now.Add(-1 * time.Minute)},
	} {
		_, err := store.CreateTask(ctx, types.Task{
			ID:        spec.id,
			Status:    spec.status,
			CreatedAt: spec.ts,
			UpdatedAt: spec.ts,
		})
		if err != nil {
			t.Fatalf("CreateTask[%d]: %v", i, err)
		}
	}

	all, err := store.ListTasks(ctx, TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks(all): %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("ListTasks all len = %d, want 3", len(all))
	}
	// updated_at DESC: t-b1 first.
	if all[0].ID != "t-b1" {
		t.Fatalf("ListTasks ordering: got first %q, want t-b1", all[0].ID)
	}

	limited, err := store.ListTasks(ctx, TaskFilter{Limit: 2})
	if err != nil {
		t.Fatalf("ListTasks(limit): %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("ListTasks limit len = %d, want 2", len(limited))
	}

	statused, err := store.ListTasks(ctx, TaskFilter{Status: "queued"})
	if err != nil {
		t.Fatalf("ListTasks(status): %v", err)
	}
	if len(statused) != 2 {
		t.Fatalf("ListTasks status len = %d, want 2", len(statused))
	}
}

func runStoreApprovalRoundTrip(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()

	if _, err := store.CreateTask(ctx, types.Task{ID: "task-ap", Status: "running"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	approval := types.TaskApproval{
		ID:          "ap-1",
		TaskID:      "task-ap",
		RunID:       "run-ap",
		Kind:        "shell",
		Status:      "pending",
		RequestedBy: "agent",
		CreatedAt:   time.Now().UTC(),
	}
	if _, err := store.CreateApproval(ctx, approval); err != nil {
		t.Fatalf("CreateApproval: %v", err)
	}

	got, ok, err := store.GetApproval(ctx, "task-ap", "ap-1")
	if err != nil || !ok {
		t.Fatalf("GetApproval: ok=%v err=%v", ok, err)
	}
	if got.Status != "pending" || got.Kind != "shell" {
		t.Fatalf("GetApproval round-trip mismatch: %+v", got)
	}

	// Resolve.
	got.Status = "approved"
	got.ResolvedBy = "operator"
	got.ResolvedAt = time.Now().UTC()
	got.ResolutionNote = "looks fine"
	if _, err := store.UpdateApproval(ctx, got); err != nil {
		t.Fatalf("UpdateApproval: %v", err)
	}

	resolved, ok, err := store.GetApproval(ctx, "task-ap", "ap-1")
	if err != nil || !ok {
		t.Fatalf("GetApproval after resolve: ok=%v err=%v", ok, err)
	}
	if resolved.Status != "approved" || resolved.ResolvedBy != "operator" || resolved.ResolutionNote != "looks fine" {
		t.Fatalf("resolution not persisted: %+v", resolved)
	}

	approvals, err := store.ListApprovals(ctx, "task-ap")
	if err != nil {
		t.Fatalf("ListApprovals: %v", err)
	}
	if len(approvals) != 1 || approvals[0].Status != "approved" {
		t.Fatalf("ListApprovals: %+v", approvals)
	}
}

func runStoreArtifactRoundTrip(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()

	artifact := types.TaskArtifact{
		ID:          "art-1",
		TaskID:      "task-art",
		RunID:       "run-art",
		StepID:      "step-art",
		Kind:        "log",
		Name:        "build.log",
		MimeType:    "text/plain",
		StorageKind: "inline",
		ContentText: "hello world",
		SizeBytes:   11,
		Status:      "ready",
		CreatedAt:   time.Now().UTC(),
	}
	if _, err := store.CreateArtifact(ctx, artifact); err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}

	got, ok, err := store.GetArtifact(ctx, "task-art", "art-1")
	if err != nil || !ok {
		t.Fatalf("GetArtifact: ok=%v err=%v", ok, err)
	}
	if got.ContentText != "hello world" || got.MimeType != "text/plain" {
		t.Fatalf("GetArtifact round-trip mismatch: %+v", got)
	}

	listed, err := store.ListArtifacts(ctx, ArtifactFilter{TaskID: "task-art"})
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	if len(listed) != 1 || listed[0].ContentText != "hello world" {
		t.Fatalf("ListArtifacts: %+v", listed)
	}

	// Filter by kind that doesn't match — should be empty.
	missing, err := store.ListArtifacts(ctx, ArtifactFilter{TaskID: "task-art", Kind: "trace"})
	if err != nil {
		t.Fatalf("ListArtifacts(kind=trace): %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("ListArtifacts(kind=trace) len = %d, want 0", len(missing))
	}
}

func runStoreRunEventsAppendAndList(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_, err := store.AppendRunEvent(ctx, types.TaskRunEvent{
			TaskID:    "task-evt",
			RunID:     "run-evt",
			EventType: "step.completed",
			Data:      map[string]any{"i": i},
			RequestID: "req-evt",
		})
		if err != nil {
			t.Fatalf("AppendRunEvent[%d]: %v", i, err)
		}
	}

	events, err := store.ListRunEvents(ctx, "task-evt", "run-evt", 0, 100)
	if err != nil {
		t.Fatalf("ListRunEvents: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("ListRunEvents len = %d, want 3", len(events))
	}
	// sequence ASC, so the first event has the smallest sequence.
	if events[0].Sequence >= events[2].Sequence {
		t.Fatalf("sequence ordering: %+v", events)
	}
	// Cursor: afterSequence skips earlier rows.
	tail, err := store.ListRunEvents(ctx, "task-evt", "run-evt", events[0].Sequence, 100)
	if err != nil {
		t.Fatalf("ListRunEvents(cursor): %v", err)
	}
	if len(tail) != 2 {
		t.Fatalf("ListRunEvents(cursor) len = %d, want 2", len(tail))
	}
}

func runStoreListEventsCrossRunFilters(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()

	// Three tasks/runs producing four events of varying types.
	// We want to confirm: (1) cross-run listing returns everything,
	// (2) event_type filter narrows correctly, (3) task_ids filter
	// narrows correctly, (4) afterSequence cursor works the same as
	// the per-run lister.
	mustAppend := func(taskID, runID, eventType string) types.TaskRunEvent {
		evt, err := store.AppendRunEvent(ctx, types.TaskRunEvent{
			TaskID: taskID, RunID: runID, EventType: eventType,
		})
		if err != nil {
			t.Fatalf("AppendRunEvent: %v", err)
		}
		return evt
	}
	e1 := mustAppend("t-A", "r-A", "turn.completed")
	e2 := mustAppend("t-A", "r-A", "run.finished")
	e3 := mustAppend("t-B", "r-B", "turn.completed")
	e4 := mustAppend("t-C", "r-C", "approval.requested")
	_ = e1
	_ = e2
	_ = e3
	_ = e4

	t.Run("no filter returns all events globally ordered", func(t *testing.T) {
		events, err := store.ListEvents(ctx, EventFilter{})
		if err != nil {
			t.Fatalf("ListEvents: %v", err)
		}
		if len(events) != 4 {
			t.Fatalf("len = %d, want 4", len(events))
		}
		for i := 1; i < len(events); i++ {
			if events[i].Sequence <= events[i-1].Sequence {
				t.Errorf("not sequence-ascending at %d: %+v", i, events)
			}
		}
	})

	t.Run("event_type filter matches OR semantics", func(t *testing.T) {
		events, err := store.ListEvents(ctx, EventFilter{EventTypes: []string{"turn.completed"}})
		if err != nil {
			t.Fatalf("ListEvents: %v", err)
		}
		if len(events) != 2 {
			t.Fatalf("len = %d, want 2 (two turn.completed)", len(events))
		}
		for _, e := range events {
			if e.EventType != "turn.completed" {
				t.Errorf("unexpected type %q", e.EventType)
			}
		}
	})

	t.Run("task_ids filter restricts to listed tasks", func(t *testing.T) {
		events, err := store.ListEvents(ctx, EventFilter{TaskIDs: []string{"t-A", "t-C"}})
		if err != nil {
			t.Fatalf("ListEvents: %v", err)
		}
		if len(events) != 3 {
			t.Fatalf("len = %d, want 3 (t-A: 2, t-C: 1)", len(events))
		}
		for _, e := range events {
			if e.TaskID != "t-A" && e.TaskID != "t-C" {
				t.Errorf("unexpected task %q in result", e.TaskID)
			}
		}
	})

	t.Run("after_sequence cursor skips older rows", func(t *testing.T) {
		events, err := store.ListEvents(ctx, EventFilter{AfterSequence: e2.Sequence})
		if err != nil {
			t.Fatalf("ListEvents: %v", err)
		}
		if len(events) != 2 {
			t.Fatalf("len = %d, want 2 (e3, e4)", len(events))
		}
		if events[0].Sequence != e3.Sequence {
			t.Errorf("first sequence = %d, want %d", events[0].Sequence, e3.Sequence)
		}
	})

	t.Run("combined filters AND together", func(t *testing.T) {
		events, err := store.ListEvents(ctx, EventFilter{
			EventTypes: []string{"turn.completed"},
			TaskIDs:    []string{"t-B"},
		})
		if err != nil {
			t.Fatalf("ListEvents: %v", err)
		}
		if len(events) != 1 || events[0].TaskID != "t-B" {
			t.Errorf("expected one event from t-B, got %+v", events)
		}
	})

	t.Run("limit caps the response size", func(t *testing.T) {
		events, err := store.ListEvents(ctx, EventFilter{Limit: 2})
		if err != nil {
			t.Fatalf("ListEvents: %v", err)
		}
		if len(events) != 2 {
			t.Fatalf("len = %d, want 2 (limit honored)", len(events))
		}
	})
}

func runStoreApplyRunTerminalTransition(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Minute)
	finishedAt := now.Add(time.Minute)
	task := types.Task{
		ID:        "task-terminal",
		Status:    "awaiting_approval",
		CreatedAt: now,
		UpdatedAt: now,
		StartedAt: now,
	}
	run := types.TaskRun{
		ID:        "run-terminal",
		TaskID:    task.ID,
		Number:    1,
		Status:    "awaiting_approval",
		StartedAt: now,
		RequestID: "req-terminal",
		TraceID:   "trace-terminal",
	}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	awaitingStep := types.TaskStep{
		ID:        "step-awaiting",
		TaskID:    task.ID,
		RunID:     run.ID,
		Index:     1,
		Status:    "awaiting_approval",
		StartedAt: now,
	}
	doneStep := types.TaskStep{
		ID:        "step-done",
		TaskID:    task.ID,
		RunID:     run.ID,
		Index:     2,
		Status:    "completed",
		StartedAt: now,
	}
	if _, err := store.AppendStep(ctx, awaitingStep); err != nil {
		t.Fatalf("AppendStep(awaiting): %v", err)
	}
	if _, err := store.AppendStep(ctx, doneStep); err != nil {
		t.Fatalf("AppendStep(done): %v", err)
	}
	approval := types.TaskApproval{
		ID:          "approval-terminal",
		TaskID:      task.ID,
		RunID:       run.ID,
		StepID:      awaitingStep.ID,
		Kind:        "tool_call",
		Status:      "pending",
		RequestedBy: "agent",
		CreatedAt:   now,
	}
	if _, err := store.CreateApproval(ctx, approval); err != nil {
		t.Fatalf("CreateApproval: %v", err)
	}
	streamingArtifact := types.TaskArtifact{
		ID:        "artifact-streaming",
		TaskID:    task.ID,
		RunID:     run.ID,
		Status:    "streaming",
		CreatedAt: now,
	}
	readyArtifact := types.TaskArtifact{
		ID:        "artifact-ready",
		TaskID:    task.ID,
		RunID:     run.ID,
		Status:    "ready",
		CreatedAt: now,
	}
	if _, err := store.CreateArtifact(ctx, streamingArtifact); err != nil {
		t.Fatalf("CreateArtifact(streaming): %v", err)
	}
	if _, err := store.CreateArtifact(ctx, readyArtifact); err != nil {
		t.Fatalf("CreateArtifact(ready): %v", err)
	}

	run.Status = "cancelled"
	run.LastError = "run cancelled: operator stop"
	run.FinishedAt = finishedAt
	run.OtelStatusCode = "error"
	run.OtelStatusMessage = run.LastError
	task.Status = "cancelled"
	task.LatestRunID = run.ID
	task.LastError = run.LastError
	task.FinishedAt = finishedAt
	task.UpdatedAt = finishedAt
	result, err := store.ApplyRunTerminalTransition(ctx, TerminalRunTransition{
		Task:                          task,
		Run:                           run,
		FinishedAt:                    finishedAt,
		CancelActiveSteps:             true,
		ActiveStepResult:              "error",
		ActiveStepError:               run.LastError,
		ActiveStepErrorKind:           "run_cancelled",
		CancelStreamingArtifacts:      true,
		CancelPendingApprovals:        true,
		PendingApprovalStatus:         "cancelled",
		PendingApprovalResolvedBy:     "system",
		PendingApprovalResolutionNote: run.LastError,
		ApprovalResolvedEventType:     "approval.resolved",
		TerminalEvent: &RunEventSpec{
			EventType: "run.cancelled",
			Data:      map[string]any{"reason": run.LastError},
			RequestID: run.RequestID,
			TraceID:   run.TraceID,
			CreatedAt: finishedAt,
		},
		TaskUpdatedEvent: &RunEventSpec{
			EventType: "task.updated",
			RequestID: run.RequestID,
			TraceID:   run.TraceID,
			CreatedAt: finishedAt,
		},
	})
	if err != nil {
		t.Fatalf("ApplyRunTerminalTransition: %v", err)
	}
	if result.Run.Status != "cancelled" || result.Task.Status != "cancelled" {
		t.Fatalf("result statuses task=%q run=%q, want cancelled/cancelled", result.Task.Status, result.Run.Status)
	}
	if len(result.CancelledApprovals) != 1 || result.CancelledApprovals[0].Status != "cancelled" {
		t.Fatalf("cancelled approvals = %+v, want one cancelled", result.CancelledApprovals)
	}
	if result.CancelledApprovals[0].ResolvedBy != "system" || result.CancelledApprovals[0].ResolutionNote != run.LastError {
		t.Fatalf("cancelled approval resolution = %+v", result.CancelledApprovals[0])
	}

	storedRun, found, err := store.GetRun(ctx, task.ID, run.ID)
	if err != nil || !found {
		t.Fatalf("GetRun: found=%v err=%v", found, err)
	}
	if storedRun.Status != "cancelled" || storedRun.LastError != run.LastError {
		t.Fatalf("stored run = %+v, want cancelled with last_error", storedRun)
	}
	storedTask, found, err := store.GetTask(ctx, task.ID)
	if err != nil || !found {
		t.Fatalf("GetTask: found=%v err=%v", found, err)
	}
	if storedTask.Status != "cancelled" || storedTask.LastError != run.LastError {
		t.Fatalf("stored task = %+v, want cancelled with last_error", storedTask)
	}
	storedAwaitingStep, found, err := store.GetStep(ctx, run.ID, awaitingStep.ID)
	if err != nil || !found {
		t.Fatalf("GetStep(awaiting): found=%v err=%v", found, err)
	}
	if storedAwaitingStep.Status != "cancelled" || storedAwaitingStep.ErrorKind != "run_cancelled" {
		t.Fatalf("awaiting step = %+v, want cancelled run_cancelled", storedAwaitingStep)
	}
	storedDoneStep, found, err := store.GetStep(ctx, run.ID, doneStep.ID)
	if err != nil || !found {
		t.Fatalf("GetStep(done): found=%v err=%v", found, err)
	}
	if storedDoneStep.Status != "completed" {
		t.Fatalf("done step status = %q, want completed", storedDoneStep.Status)
	}
	storedStreamingArtifact, found, err := store.GetArtifact(ctx, task.ID, streamingArtifact.ID)
	if err != nil || !found {
		t.Fatalf("GetArtifact(streaming): found=%v err=%v", found, err)
	}
	if storedStreamingArtifact.Status != "cancelled" {
		t.Fatalf("streaming artifact status = %q, want cancelled", storedStreamingArtifact.Status)
	}
	storedReadyArtifact, found, err := store.GetArtifact(ctx, task.ID, readyArtifact.ID)
	if err != nil || !found {
		t.Fatalf("GetArtifact(ready): found=%v err=%v", found, err)
	}
	if storedReadyArtifact.Status != "ready" {
		t.Fatalf("ready artifact status = %q, want ready", storedReadyArtifact.Status)
	}

	events, err := store.ListRunEvents(ctx, task.ID, run.ID, 0, 10)
	if err != nil {
		t.Fatalf("ListRunEvents: %v", err)
	}
	wantTypes := []string{"approval.resolved", "run.cancelled", "task.updated"}
	if len(events) != len(wantTypes) {
		t.Fatalf("events len = %d, want %d: %+v", len(events), len(wantTypes), events)
	}
	for i, want := range wantTypes {
		if events[i].EventType != want {
			t.Fatalf("event[%d] type = %q, want %q", i, events[i].EventType, want)
		}
		if events[i].Sequence <= 0 {
			t.Fatalf("event[%d] sequence = %d, want positive", i, events[i].Sequence)
		}
		assertNoTerminalEventSnapshot(t, events[i])
	}
	if got := events[0].Data; got["approval_id"] != approval.ID || got["decision"] != "cancelled" || got["status"] != "cancelled" || got["by"] != "system" {
		t.Fatalf("approval.resolved data = %+v, want compact cancelled approval data", got)
	}
	if got := events[1].Data; got["reason"] != run.LastError {
		t.Fatalf("run.cancelled data = %+v, want reason %q", got, run.LastError)
	}
	if events[2].Data != nil && len(events[2].Data) != 0 {
		t.Fatalf("task.updated data = %+v, want no extra keys", events[2].Data)
	}
}

func assertNoTerminalEventSnapshot(t *testing.T, event types.TaskRunEvent) {
	t.Helper()
	for _, key := range []string{"run", "steps", "artifacts", "snapshot"} {
		if _, ok := event.Data[key]; ok {
			t.Fatalf("%s event carried %q snapshot data: %+v", event.EventType, key, event.Data)
		}
	}
}

func runStoreApplyRunTerminalTransitionPreservesChildrenWithoutCancelFlags(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Minute)
	finishedAt := now.Add(time.Minute)
	task := types.Task{
		ID:        "task-terminal-preserve",
		Status:    "running",
		CreatedAt: now,
		UpdatedAt: now,
		StartedAt: now,
	}
	run := types.TaskRun{
		ID:        "run-terminal-preserve",
		TaskID:    task.ID,
		Number:    1,
		Status:    "running",
		StartedAt: now,
		RequestID: "req-terminal-preserve",
		TraceID:   "trace-terminal-preserve",
	}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	runningStep := types.TaskStep{
		ID:        "step-running",
		TaskID:    task.ID,
		RunID:     run.ID,
		Index:     1,
		Status:    "running",
		StartedAt: now,
	}
	awaitingStep := types.TaskStep{
		ID:        "step-awaiting",
		TaskID:    task.ID,
		RunID:     run.ID,
		Index:     2,
		Status:    "awaiting_approval",
		StartedAt: now,
	}
	if _, err := store.AppendStep(ctx, runningStep); err != nil {
		t.Fatalf("AppendStep(running): %v", err)
	}
	if _, err := store.AppendStep(ctx, awaitingStep); err != nil {
		t.Fatalf("AppendStep(awaiting): %v", err)
	}
	approval := types.TaskApproval{
		ID:          "approval-preserved",
		TaskID:      task.ID,
		RunID:       run.ID,
		StepID:      awaitingStep.ID,
		Kind:        "tool_call",
		Status:      "pending",
		RequestedBy: "agent",
		CreatedAt:   now,
	}
	if _, err := store.CreateApproval(ctx, approval); err != nil {
		t.Fatalf("CreateApproval: %v", err)
	}
	artifact := types.TaskArtifact{
		ID:        "artifact-preserved",
		TaskID:    task.ID,
		RunID:     run.ID,
		Status:    "streaming",
		CreatedAt: now,
	}
	if _, err := store.CreateArtifact(ctx, artifact); err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}

	run.Status = "completed"
	run.FinishedAt = finishedAt
	run.OtelStatusCode = "ok"
	task.Status = "completed"
	task.LatestRunID = run.ID
	task.FinishedAt = finishedAt
	task.UpdatedAt = finishedAt
	result, err := store.ApplyRunTerminalTransition(ctx, TerminalRunTransition{
		Task:       task,
		Run:        run,
		FinishedAt: finishedAt,
		TerminalEvent: &RunEventSpec{
			EventType: "run.finished",
			Data:      map[string]any{"status": "completed"},
			RequestID: run.RequestID,
			TraceID:   run.TraceID,
			CreatedAt: finishedAt,
		},
	})
	if err != nil {
		t.Fatalf("ApplyRunTerminalTransition: %v", err)
	}
	if result.Run.Status != "completed" || result.Task.Status != "completed" {
		t.Fatalf("result statuses task=%q run=%q, want completed/completed", result.Task.Status, result.Run.Status)
	}
	if len(result.CancelledApprovals) != 0 {
		t.Fatalf("cancelled approvals = %+v, want none without cancel flag", result.CancelledApprovals)
	}

	storedApproval, found, err := store.GetApproval(ctx, task.ID, approval.ID)
	if err != nil || !found {
		t.Fatalf("GetApproval: found=%v err=%v", found, err)
	}
	if storedApproval.Status != "pending" || !storedApproval.ResolvedAt.IsZero() {
		t.Fatalf("stored approval = %+v, want pending/unresolved", storedApproval)
	}
	storedRunningStep, found, err := store.GetStep(ctx, run.ID, runningStep.ID)
	if err != nil || !found {
		t.Fatalf("GetStep(running): found=%v err=%v", found, err)
	}
	if storedRunningStep.Status != "running" {
		t.Fatalf("running step status = %q, want running", storedRunningStep.Status)
	}
	storedAwaitingStep, found, err := store.GetStep(ctx, run.ID, awaitingStep.ID)
	if err != nil || !found {
		t.Fatalf("GetStep(awaiting): found=%v err=%v", found, err)
	}
	if storedAwaitingStep.Status != "awaiting_approval" {
		t.Fatalf("awaiting step status = %q, want awaiting_approval", storedAwaitingStep.Status)
	}
	storedArtifact, found, err := store.GetArtifact(ctx, task.ID, artifact.ID)
	if err != nil || !found {
		t.Fatalf("GetArtifact: found=%v err=%v", found, err)
	}
	if storedArtifact.Status != "streaming" {
		t.Fatalf("artifact status = %q, want streaming", storedArtifact.Status)
	}

	events, err := store.ListRunEvents(ctx, task.ID, run.ID, 0, 10)
	if err != nil {
		t.Fatalf("ListRunEvents: %v", err)
	}
	if len(events) != 1 || events[0].EventType != "run.finished" {
		t.Fatalf("events = %+v, want only run.finished", events)
	}
	assertNoTerminalEventSnapshot(t, events[0])
	if events[0].Data["status"] != "completed" {
		t.Fatalf("run.finished data = %+v, want compact status", events[0].Data)
	}
}

func runStoreApplyRunTerminalTransitionRequiresStoredRunTaskMatch(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	task := types.Task{
		ID:        "task-terminal-mismatch",
		Status:    "running",
		CreatedAt: now,
		UpdatedAt: now,
	}
	otherTask := types.Task{
		ID:        "task-terminal-owner",
		Status:    "running",
		CreatedAt: now,
		UpdatedAt: now,
	}
	run := types.TaskRun{
		ID:        "run-terminal-mismatch",
		TaskID:    otherTask.ID,
		Number:    1,
		Status:    "running",
		StartedAt: now,
	}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask(task): %v", err)
	}
	if _, err := store.CreateTask(ctx, otherTask); err != nil {
		t.Fatalf("CreateTask(other): %v", err)
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	mismatchedRun := run
	mismatchedRun.TaskID = task.ID
	mismatchedRun.Status = "cancelled"
	mismatchedRun.LastError = "wrong owner"
	_, err := store.ApplyRunTerminalTransition(ctx, TerminalRunTransition{
		Task: task,
		Run:  mismatchedRun,
		TerminalEvent: &RunEventSpec{
			EventType: "run.cancelled",
			Data:      map[string]any{"reason": mismatchedRun.LastError},
		},
	})
	if err == nil {
		t.Fatal("ApplyRunTerminalTransition succeeded for a run owned by another task")
	}

	storedTask, found, err := store.GetTask(ctx, task.ID)
	if err != nil || !found {
		t.Fatalf("GetTask(task): found=%v err=%v", found, err)
	}
	if storedTask.Status != "running" || storedTask.LastError != "" {
		t.Fatalf("task after failed transition = %+v, want unchanged running task", storedTask)
	}
	storedRun, found, err := store.GetRun(ctx, otherTask.ID, run.ID)
	if err != nil || !found {
		t.Fatalf("GetRun(owner): found=%v err=%v", found, err)
	}
	if storedRun.TaskID != otherTask.ID || storedRun.Status != "running" || storedRun.LastError != "" {
		t.Fatalf("run after failed transition = %+v, want unchanged owner/running run", storedRun)
	}
	events, err := store.ListRunEvents(ctx, task.ID, run.ID, 0, 10)
	if err != nil {
		t.Fatalf("ListRunEvents(wrong task): %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events for failed transition = %+v, want none", events)
	}
}

func runStoreListRunsByFilterStatusSet(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()

	now := time.Now().UTC()
	for i, status := range []string{"queued", "running", "completed", "failed"} {
		_, err := store.CreateRun(ctx, types.TaskRun{
			ID:        "run-" + status,
			TaskID:    "task-rfilter",
			Number:    i + 1,
			Status:    status,
			StartedAt: now.Add(time.Duration(i) * time.Second),
		})
		if err != nil {
			t.Fatalf("CreateRun(%s): %v", status, err)
		}
	}

	got, err := store.ListRunsByFilter(ctx, RunFilter{
		TaskID:   "task-rfilter",
		Statuses: []string{"running", "completed"},
	})
	if err != nil {
		t.Fatalf("ListRunsByFilter: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListRunsByFilter len = %d, want 2", len(got))
	}
	for _, run := range got {
		if run.Status != "running" && run.Status != "completed" {
			t.Fatalf("unexpected status in filtered set: %q", run.Status)
		}
	}

	// Limit clamps the result.
	limited, err := store.ListRunsByFilter(ctx, RunFilter{TaskID: "task-rfilter", Limit: 2})
	if err != nil {
		t.Fatalf("ListRunsByFilter(limit): %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("ListRunsByFilter(limit) len = %d, want 2", len(limited))
	}
}

func runStoreDeleteTaskCascades(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()

	if _, err := store.CreateTask(ctx, types.Task{ID: "task-del", Status: "queued"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := store.CreateRun(ctx, types.TaskRun{ID: "run-del", TaskID: "task-del", Status: "running", StartedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := store.AppendStep(ctx, types.TaskStep{ID: "step-del", TaskID: "task-del", RunID: "run-del", Status: "running"}); err != nil {
		t.Fatalf("AppendStep: %v", err)
	}

	if err := store.DeleteTask(ctx, "task-del"); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	if _, ok, _ := store.GetTask(ctx, "task-del"); ok {
		t.Fatal("task still present after delete")
	}
	if _, ok, _ := store.GetRun(ctx, "task-del", "run-del"); ok {
		t.Fatal("run still present after delete")
	}
	steps, err := store.ListSteps(ctx, "run-del")
	if err != nil {
		t.Fatalf("ListSteps: %v", err)
	}
	if len(steps) != 0 {
		t.Fatalf("steps still present after delete: %d", len(steps))
	}
}

// runStoreTaskMCPServersRoundTrip pins that a Task with a
// fully-populated MCPServers slice survives a round-trip through
// the backend's storage path. The pkg/types/task JSON-round-trip
// test pins the marshaling contract on the type itself; this test
// pins the actual storage layer (write, read, compare).
//
// Catches: a regression where someone changes a Task field tag,
// adds an unmarshal hook that mishandles a default value, or
// makes a column type incompatible with the existing JSON shape
// would silently corrupt every persisted MCP config; this test
// fails first.
func runStoreTaskMCPServersRoundTrip(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()

	task := types.Task{
		ID:            "task-mcp",
		Title:         "MCP store round-trip",
		Status:        "queued",
		ExecutionKind: "agent_loop",
		MCPServers: []types.MCPServerConfig{
			{
				Name:    "fs",
				Command: "bunx",
				Args:    []string{"--bun", "@modelcontextprotocol/server-filesystem", "/workspace"},
				Env: map[string]string{
					"DEBUG_TOKEN": "$DEBUG_TOKEN",
					"AUTH":        "enc:abc123base64=",
					"NODE_ENV":    "production",
				},
				ApprovalPolicy: types.MCPApprovalAuto,
			},
			{
				Name: "github",
				URL:  "https://api.example.com/mcp",
				Headers: map[string]string{
					"Authorization": "Bearer $GITHUB_TOKEN",
					"X-Trace":       "on",
				},
				ApprovalPolicy: types.MCPApprovalRequireApproval,
			},
			{
				Name:           "blocked",
				Command:        "npx",
				Args:           []string{"@vendor/dangerous"},
				ApprovalPolicy: types.MCPApprovalBlock,
			},
		},
	}

	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	got, ok, err := store.GetTask(ctx, task.ID)
	if err != nil || !ok {
		t.Fatalf("GetTask: ok=%v err=%v", ok, err)
	}

	// Field-by-field check on the MCP slice rather than reflect.DeepEqual
	// on the whole Task: the store stamps CreatedAt / UpdatedAt /
	// other timestamps, so a whole-Task DeepEqual would fail on
	// fields the test doesn't care about.
	if len(got.MCPServers) != len(task.MCPServers) {
		t.Fatalf("MCPServers count: got %d, want %d", len(got.MCPServers), len(task.MCPServers))
	}
	for i, want := range task.MCPServers {
		gotEntry := got.MCPServers[i]
		if gotEntry.Name != want.Name {
			t.Errorf("[%d] Name = %q, want %q", i, gotEntry.Name, want.Name)
		}
		if gotEntry.Command != want.Command {
			t.Errorf("[%d] Command = %q, want %q", i, gotEntry.Command, want.Command)
		}
		if gotEntry.URL != want.URL {
			t.Errorf("[%d] URL = %q, want %q", i, gotEntry.URL, want.URL)
		}
		if gotEntry.ApprovalPolicy != want.ApprovalPolicy {
			t.Errorf("[%d] ApprovalPolicy = %q, want %q", i, gotEntry.ApprovalPolicy, want.ApprovalPolicy)
		}
		if !equalStringSlice(gotEntry.Args, want.Args) {
			t.Errorf("[%d] Args = %+v, want %+v", i, gotEntry.Args, want.Args)
		}
		if !equalStringMap(gotEntry.Env, want.Env) {
			t.Errorf("[%d] Env = %+v, want %+v", i, gotEntry.Env, want.Env)
		}
		if !equalStringMap(gotEntry.Headers, want.Headers) {
			t.Errorf("[%d] Headers = %+v, want %+v", i, gotEntry.Headers, want.Headers)
		}
	}
}
