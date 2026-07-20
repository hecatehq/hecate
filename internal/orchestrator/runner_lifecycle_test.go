package orchestrator

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
)

// TestStartReconcileLoop_RequeuesStaleRunningRun verifies that the periodic
// reconcile loop re-enqueues a run that has been stuck in "running" state
// longer than the stale threshold.
func TestStartReconcileLoop_RequeuesStaleRunningRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	queue := &recordingQueue{}

	// Short interval and lease so the test completes quickly.
	runner := NewRunner(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store,
		nil,
		Config{
			QueueWorkers:      0, // no actual workers — we're testing the loop only
			QueueLeaseSeconds: 1, // stale threshold = 3s
			ReconcileInterval: 20 * time.Millisecond,
		},
	)
	runner.SetQueue(queue)

	task := types.Task{
		ID:        "task-stale",
		Status:    "running",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	staleRun := types.TaskRun{
		ID:        "run-stale",
		TaskID:    task.ID,
		Number:    1,
		Status:    "running",
		StartedAt: time.Now().UTC().Add(-10 * time.Second), // well past 3s threshold
	}
	if _, err := store.CreateRun(ctx, staleRun); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	runner.StartReconcileLoop()

	// Wait up to 500ms for the loop to fire and requeue the run.
	deadline := time.Now().Add(500 * time.Millisecond)
	var requeued bool
	for time.Now().Before(deadline) {
		run, found, err := store.GetRun(ctx, task.ID, staleRun.ID)
		if err != nil || !found {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		if run.Status == "queued" {
			requeued = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	shutdownCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = runner.Shutdown(shutdownCtx)

	if !requeued {
		t.Fatal("stale run was not requeued by reconcile loop")
	}
	if len(queue.enqueued) == 0 {
		t.Fatal("expected run to be enqueued; queue is empty")
	}

	events, err := store.ListRunEvents(ctx, task.ID, staleRun.ID, 0, 50)
	if err != nil {
		t.Fatalf("ListRunEvents: %v", err)
	}
	var found bool
	for _, e := range events {
		if e.EventType == "gap.run_disconnected" {
			if got := e.Data["reason"]; got != "worker_lease_expired" {
				t.Fatalf("reason = %v, want worker_lease_expired", got)
			}
			if got := e.Data["action"]; got != "requeued" {
				t.Fatalf("action = %v, want requeued", got)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatal("missing gap.run_disconnected event")
	}
}

func TestRunnerStartTaskSnapshotsProjectLinkageOnRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	runner := NewRunner(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store,
		nil,
		Config{ApprovalPolicies: []string{"shell_exec"}},
	)
	runner.workspaces = NewWorkspaceManager(t.TempDir())
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = runner.Shutdown(shutdownCtx)
	}()

	idgen := func(prefix string) string {
		return prefix + "_linkage"
	}
	task := types.Task{
		ID:                          "task_project",
		ProjectID:                   "proj_1",
		WorkItemID:                  "work_1",
		AssignmentID:                "asgn_1",
		ExecutionKind:               "agent_loop",
		WorkflowMode:                types.WorkflowModeQA,
		WorkflowVersion:             "v0",
		WorkspaceMode:               "ephemeral",
		SandboxReadOnly:             true,
		WorkspaceSystemPromptPolicy: types.WorkspaceSystemPromptExclude,
		RequestedModel:              "test-model",
		WorkingDirectory:            t.TempDir(),
		Status:                      "queued",
	}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	result, err := runner.StartTask(ctx, task, idgen)
	if err != nil {
		t.Fatalf("StartTask: %v", err)
	}
	if result.Run.ProjectID != "proj_1" || result.Run.WorkItemID != "work_1" || result.Run.AssignmentID != "asgn_1" {
		t.Fatalf("result run linkage = project %q work %q assignment %q, want proj_1/work_1/asgn_1", result.Run.ProjectID, result.Run.WorkItemID, result.Run.AssignmentID)
	}
	if result.Run.WorkflowMode != types.WorkflowModeQA || result.Run.WorkflowVersion != "v0" {
		t.Fatalf("result workflow = %q/%q, want qa/v0", result.Run.WorkflowMode, result.Run.WorkflowVersion)
	}
	storedRun, ok, err := store.GetRun(ctx, task.ID, result.Run.ID)
	if err != nil || !ok {
		t.Fatalf("GetRun: ok=%v err=%v", ok, err)
	}
	if storedRun.ProjectID != "proj_1" || storedRun.WorkItemID != "work_1" || storedRun.AssignmentID != "asgn_1" {
		t.Fatalf("stored run linkage = project %q work %q assignment %q, want proj_1/work_1/asgn_1", storedRun.ProjectID, storedRun.WorkItemID, storedRun.AssignmentID)
	}
	if storedRun.WorkflowMode != types.WorkflowModeQA || storedRun.WorkflowVersion != "v0" {
		t.Fatalf("stored workflow = %q/%q, want qa/v0", storedRun.WorkflowMode, storedRun.WorkflowVersion)
	}
}

func TestRunnerStartTaskPreservesEmptyRouteForAutoRouting(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	runner := NewRunner(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store,
		nil,
		Config{DeferQueueStart: true},
	)
	runner.workspaces = NewWorkspaceManager(t.TempDir())

	task := types.Task{
		ID:               "task_auto_route",
		Prompt:           "Inspect the workspace",
		ExecutionKind:    "agent_loop",
		WorkspaceMode:    "in_place",
		WorkingDirectory: t.TempDir(),
		Status:           "queued",
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	result, err := runner.StartTask(ctx, task, defaultResourceID)
	if err != nil {
		t.Fatalf("StartTask(auto route): %v", err)
	}
	if result.Run.Model != "" || result.Run.Provider != "" {
		t.Fatalf("auto-routed run = provider %q model %q, want both empty before Hecate router resolves it", result.Run.Provider, result.Run.Model)
	}
}

func TestRecordOrchestratorRunStartedIncludesWorkflowMode(t *testing.T) {
	t.Parallel()

	trace := profiler.NewTrace("request-qa-workflow", nil)
	defer trace.Finalize()
	recordOrchestratorRunStarted(trace, "task-qa-workflow", types.TaskRun{
		ID:           "run-qa-workflow",
		Number:       1,
		Status:       "queued",
		WorkflowMode: types.WorkflowModeQA,
	})

	for _, event := range trace.Events() {
		if event.Name != telemetry.EventOrchestratorRunStarted {
			continue
		}
		if got := event.Attributes[telemetry.AttrHecateWorkflowMode]; got != "qa" {
			t.Fatalf("workflow trace attribute = %#v, want qa", got)
		}
		return
	}
	t.Fatal("missing orchestrator run-started event")
}

func TestRecordOrchestratorRunFailedIncludesOnlyCanonicalWorkflowMode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		mode types.WorkflowMode
		want string
	}{
		{name: "qa", mode: types.WorkflowModeQA, want: "qa"},
		{name: "unknown", mode: types.WorkflowMode("future-contract"), want: ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			trace := profiler.NewTrace("request-failed-workflow-"+tc.name, nil)
			defer trace.Finalize()
			recordOrchestratorRunFailed(trace, "task-failed-workflow", types.TaskRun{
				ID:           "run-failed-workflow",
				WorkflowMode: tc.mode,
			}, "executor_failed", context.Canceled)

			for _, event := range trace.Events() {
				if event.Name != telemetry.EventOrchestratorRunFailed {
					continue
				}
				got, found := event.Attributes[telemetry.AttrHecateWorkflowMode]
				if tc.want == "" {
					if found {
						t.Fatalf("unexpected workflow trace attribute = %#v", got)
					}
					return
				}
				if !found || got != tc.want {
					t.Fatalf("workflow trace attribute = %#v, found=%t, want %q", got, found, tc.want)
				}
				return
			}
			t.Fatal("missing orchestrator run-failed event")
		})
	}
}

// TestStartReconcileLoop_SkipsFreshRunningRun verifies that the loop does NOT
// re-enqueue a run that only recently entered "running" state — i.e. an
// active worker is still within its lease window.
func TestStartReconcileLoop_SkipsFreshRunningRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	queue := &recordingQueue{}

	runner := NewRunner(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store,
		nil,
		Config{
			QueueWorkers:      0,
			QueueLeaseSeconds: 60, // stale threshold = 180s; run is fresh
			ReconcileInterval: 20 * time.Millisecond,
		},
	)
	runner.SetQueue(queue)

	task := types.Task{
		ID:        "task-fresh",
		Status:    "running",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	freshRun := types.TaskRun{
		ID:        "run-fresh",
		TaskID:    task.ID,
		Number:    1,
		Status:    "running",
		StartedAt: time.Now().UTC(), // just started
	}
	if _, err := store.CreateRun(ctx, freshRun); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	runner.StartReconcileLoop()

	// Give the loop multiple ticks to (incorrectly) fire.
	time.Sleep(100 * time.Millisecond)

	shutdownCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = runner.Shutdown(shutdownCtx)

	run, found, err := store.GetRun(ctx, task.ID, freshRun.ID)
	if err != nil || !found {
		t.Fatalf("GetRun: found=%t err=%v", found, err)
	}
	if run.Status != "running" {
		t.Fatalf("fresh run status = %q, want running (should not have been requeued)", run.Status)
	}
	if len(queue.enqueued) != 0 {
		t.Fatalf("fresh run was unexpectedly enqueued (%d jobs)", len(queue.enqueued))
	}
}

// TestStartReconcileLoop_SkipsLocalInflightRun verifies that periodic
// stale-run reconciliation does not duplicate work the current runner still
// owns. A long-running task can legitimately outlive the conservative
// StartedAt-based stale threshold; if it is registered in the queue
// coordinator, Shutdown and explicit CancelRun can still reach it, so
// requeueing would create a second worker for the same run.
func TestStartReconcileLoop_SkipsLocalInflightRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	queue := &recordingQueue{}

	runner := NewRunner(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store,
		nil,
		Config{
			QueueWorkers:      0,
			QueueLeaseSeconds: 1, // stale threshold = 3s
			ReconcileInterval: 20 * time.Millisecond,
		},
	)
	runner.SetQueue(queue)

	task := types.Task{
		ID:        "task-inflight",
		Status:    "running",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	run := types.TaskRun{
		ID:        "run-inflight",
		TaskID:    task.ID,
		Number:    1,
		Status:    "running",
		StartedAt: time.Now().UTC().Add(-10 * time.Second),
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	cancelled := false
	runner.registerJob(run.ID, func() { cancelled = true })
	defer runner.unregisterJob(run.ID)
	runner.StartReconcileLoop()

	time.Sleep(100 * time.Millisecond)

	shutdownCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = runner.Shutdown(shutdownCtx)

	gotRun, found, err := store.GetRun(ctx, task.ID, run.ID)
	if err != nil || !found {
		t.Fatalf("GetRun: found=%t err=%v", found, err)
	}
	if gotRun.Status != "running" {
		t.Fatalf("in-flight run status = %q, want running", gotRun.Status)
	}
	if len(queue.enqueued) != 0 {
		t.Fatalf("in-flight run was unexpectedly requeued (%d jobs)", len(queue.enqueued))
	}
	if !cancelled {
		t.Fatal("Shutdown did not cancel the registered in-flight job")
	}
}

// TestStartReconcileLoop_StopsOnShutdown verifies that the reconcile goroutine
// joins the worker wait-group and exits when Shutdown is called. If it leaked,
// Shutdown would block until its context deadline.
func TestStartReconcileLoop_StopsOnShutdown(t *testing.T) {
	t.Parallel()

	runner := NewRunner(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		taskstate.NewMemoryStore(),
		nil,
		Config{
			QueueWorkers:      0,
			ReconcileInterval: 10 * time.Millisecond,
		},
	)
	runner.StartReconcileLoop()

	// Shutdown must complete well within 1s; if the loop goroutine leaks it
	// would hold the coordinator wait group open until the context deadline.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runner.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown returned error (loop may have leaked): %v", err)
	}
}

// TestRunner_FileExecutor_FullLifecycle exercises the full
// start → queue → claim → execute → complete path for a file-write task
// and asserts that events arrive in the required order.
func TestRunner_FileExecutor_FullLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := taskstate.NewMemoryStore()

	runner := NewRunner(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store,
		nil,
		Config{QueueWorkers: 1},
	)

	tempDir := t.TempDir()
	task := types.Task{
		ID:               "task-lifecycle-orch",
		Title:            "lifecycle",
		Prompt:           "write a file",
		ExecutionKind:    "file",
		FileOperation:    "write",
		FilePath:         "out.txt",
		FileContent:      "hello",
		WorkingDirectory: tempDir,
		Status:           "pending",
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	result, err := runner.StartTask(ctx, task, defaultResourceID)
	if err != nil {
		t.Fatalf("StartTask: %v", err)
	}

	// Poll until terminal or timeout.
	deadline := time.Now().Add(10 * time.Second)
	var finalRun types.TaskRun
	for time.Now().Before(deadline) {
		run, found, err := store.GetRun(ctx, task.ID, result.Run.ID)
		if err != nil || !found {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if types.IsTerminalTaskRunStatus(run.Status) {
			finalRun = run
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	shutdownCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = runner.Shutdown(shutdownCtx)

	if finalRun.Status != "completed" {
		t.Fatalf("run status = %q, want completed", finalRun.Status)
	}

	events, err := store.ListRunEvents(ctx, task.ID, result.Run.ID, 0, 50)
	if err != nil {
		t.Fatalf("ListRunEvents: %v", err)
	}

	// Assert the required subsequence: created → queued → started → finished.
	wantOrder := []string{"run.created", "run.queued", "run.started", "run.finished"}
	cursor := 0
	for _, e := range events {
		if cursor >= len(wantOrder) {
			break
		}
		if e.EventType == wantOrder[cursor] {
			cursor++
		}
	}
	if cursor != len(wantOrder) {
		got := make([]string, 0, len(events))
		for _, e := range events {
			got = append(got, e.EventType)
		}
		t.Fatalf("event order missing %v; got %v", wantOrder[cursor:], got)
	}

	// Assert sequences strictly increase.
	var prev int64
	for _, e := range events {
		if e.Sequence <= prev {
			t.Fatalf("sequence %d after %d for %s; want strictly increasing", e.Sequence, prev, e.EventType)
		}
		prev = e.Sequence
	}
}

func TestResumeTaskCarriesForwardContextPacket(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	runner := NewRunner(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store,
		nil,
		Config{QueueWorkers: 0},
	)

	now := time.Now().UTC()
	inputProviderInstance := types.ProviderInstanceIdentity{ID: "runtime-resume-input", Kind: types.ProviderInstanceIdentityRuntime}
	task := types.Task{
		ID:               "task-resume-context",
		Title:            "resume context",
		Prompt:           "resume me",
		ExecutionKind:    "agent_loop",
		RequestedModel:   "test-model",
		WorkingDirectory: t.TempDir(),
		Status:           "completed",
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	run := types.TaskRun{
		ID:                            "run-resume-source",
		TaskID:                        task.ID,
		Number:                        1,
		Status:                        "completed",
		StartedAt:                     now,
		FinishedAt:                    now,
		InputRef:                      "msg_resume_input",
		Provider:                      "vision-resume",
		ProviderKind:                  "cloud",
		Model:                         "resolved-vision-resume",
		InputProviderInstance:         inputProviderInstance,
		InputProviderDispatchRecorded: true,
		ContextPacket: json.RawMessage(`{
			"id":"ctx_old",
			"refs":{"session_id":"chat_1","turn_id":"turn_1","message_id":"message_1","task_id":"task-resume-context","run_id":"run-resume-source"},
			"items":[{"kind":"transcript","trust_level":"runtime_state","origin":"chat.transcript","title":"Chat transcript","included":true}]
		}`),
	}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	result, err := runner.ResumeTask(ctx, task, run, "operator_resume", defaultResourceID)
	if err != nil {
		t.Fatalf("ResumeTask: %v", err)
	}
	stored, found, err := store.GetRun(ctx, task.ID, result.Run.ID)
	if err != nil || !found {
		t.Fatalf("GetRun(%q) found=%v err=%v", result.Run.ID, found, err)
	}
	assertCopiedRunContextPacket(t, stored.ContextPacket, task.ID, result.Run.ID)
	assertRunContextSourceRefs(t, stored.ContextPacket, "chat_1", "turn_1", "message_1")
	if stored.InputRef != run.InputRef {
		t.Fatalf("InputRef = %q, want inherited %q", stored.InputRef, run.InputRef)
	}
	if stored.Provider != run.Provider || stored.ProviderKind != run.ProviderKind || stored.Model != run.Model || stored.InputProviderInstance != inputProviderInstance || !stored.InputProviderDispatchRecorded {
		t.Fatalf("inherited input route = provider %q kind %q model %q instance %+v dispatched=%t, want %q/%q/%q/%+v/true", stored.Provider, stored.ProviderKind, stored.Model, stored.InputProviderInstance, stored.InputProviderDispatchRecorded, run.Provider, run.ProviderKind, run.Model, inputProviderInstance)
	}
}

func TestRetryTaskCarriesForwardOnlySourceContext(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	runner := NewRunner(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store,
		nil,
		Config{QueueWorkers: 0},
	)

	now := time.Now().UTC()
	inputProviderInstance := types.ProviderInstanceIdentity{ID: "runtime-retry-source-input", Kind: types.ProviderInstanceIdentityRuntime}
	task := types.Task{
		ID:                "task-retry-source-context",
		Title:             "retry source context",
		Prompt:            "retry me from scratch",
		ExecutionKind:     "agent_loop",
		RequestedProvider: "fresh-provider",
		RequestedModel:    "fresh-model",
		WorkingDirectory:  t.TempDir(),
		Status:            "failed",
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	run := types.TaskRun{
		ID:                             "run-retry-source-context",
		TaskID:                         task.ID,
		Number:                         1,
		Status:                         "failed",
		StartedAt:                      now,
		FinishedAt:                     now,
		InputRef:                       "msg_prior_input",
		Provider:                       "prior-provider",
		ProviderKind:                   "cloud",
		Model:                          "prior-model",
		InputProviderInstance:          inputProviderInstance,
		InputProviderDispatchRecorded:  true,
		InputProviderDisclosedInstance: inputProviderInstance,
		PriorCostMicrosUSD:             150,
		TotalCostMicrosUSD:             25,
		ContextPacket: json.RawMessage(`{
			"id":"ctx_old_retry",
			"workspace":"/prior/workspace",
			"refs":{
				"session_id":"chat_retry",
				"turn_id":"turn_retry",
				"message_id":"msg_retry",
				"task_id":"task-retry-source-context",
				"run_id":"run-retry-source-context"
			},
			"items":[{"kind":"transcript","trust_level":"runtime_state","origin":"chat.transcript","title":"Chat transcript","included":true}]
		}`),
	}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	result, err := runner.RetryTask(ctx, task, run, defaultResourceID)
	if err != nil {
		t.Fatalf("RetryTask: %v", err)
	}
	stored, found, err := store.GetRun(ctx, task.ID, result.Run.ID)
	if err != nil || !found {
		t.Fatalf("GetRun(%q) found=%v err=%v", result.Run.ID, found, err)
	}
	assertCopiedRunContextPacket(t, stored.ContextPacket, task.ID, result.Run.ID)

	var packet struct {
		Workspace string `json:"workspace"`
		Refs      struct {
			SessionID string `json:"session_id"`
			TurnID    string `json:"turn_id"`
			MessageID string `json:"message_id"`
		} `json:"refs"`
	}
	if err := json.Unmarshal(stored.ContextPacket, &packet); err != nil {
		t.Fatalf("Unmarshal ContextPacket: %v", err)
	}
	if packet.Refs.SessionID != "chat_retry" || packet.Refs.TurnID != "turn_retry" || packet.Refs.MessageID != "msg_retry" {
		t.Fatalf("source refs = %+v, want chat_retry/turn_retry/msg_retry", packet.Refs)
	}
	if packet.Workspace != stored.WorkspacePath || packet.Workspace == "/prior/workspace" {
		t.Fatalf("context workspace = %q, want new Run workspace %q", packet.Workspace, stored.WorkspacePath)
	}
	if stored.InputRef != run.InputRef {
		t.Fatalf("InputRef = %q, want inherited same-input ref %q", stored.InputRef, run.InputRef)
	}
	if stored.Provider != run.Provider || stored.ProviderKind != run.ProviderKind || stored.Model != run.Model || stored.InputProviderInstance != inputProviderInstance || !stored.InputProviderDispatchRecorded {
		t.Fatalf("same-input route = provider %q kind %q model %q admitted %+v dispatched=%t, want %q/%q/%q/%+v/true", stored.Provider, stored.ProviderKind, stored.Model, stored.InputProviderInstance, stored.InputProviderDispatchRecorded, run.Provider, run.ProviderKind, run.Model, inputProviderInstance)
	}
	if stored.InputProviderDisclosedInstance.Valid() {
		t.Fatalf("new retry disclosed instance = %+v, want empty until this Run reaches provider I/O", stored.InputProviderDisclosedInstance)
	}
	if stored.PriorCostMicrosUSD != 0 {
		t.Fatalf("PriorCostMicrosUSD = %d, want fresh cost chain", stored.PriorCostMicrosUSD)
	}

	events, err := store.ListRunEvents(ctx, task.ID, stored.ID, 0, 50)
	if err != nil {
		t.Fatalf("ListRunEvents: %v", err)
	}
	for _, event := range events {
		if event.EventType == "run.resumed_from_event" {
			t.Fatalf("fresh retry emitted %q", event.EventType)
		}
	}
}

func TestRetryTaskFromModelCallCarriesForwardContextPacket(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	runner := NewRunner(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store,
		nil,
		Config{QueueWorkers: 0},
	)

	now := time.Now().UTC()
	inputProviderInstance := types.ProviderInstanceIdentity{ID: "runtime-retry-input", Kind: types.ProviderInstanceIdentityRuntime}
	task := types.Task{
		ID:               "task-retry-context",
		Title:            "retry context",
		Prompt:           "retry me",
		ExecutionKind:    "agent_loop",
		RequestedModel:   "test-model",
		WorkingDirectory: t.TempDir(),
		Status:           "completed",
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	run := types.TaskRun{
		ID:                            "run-retry-source",
		TaskID:                        task.ID,
		Number:                        1,
		Status:                        "completed",
		ModelCallCount:                1,
		StartedAt:                     now,
		FinishedAt:                    now,
		InputRef:                      "msg_retry_input",
		Provider:                      "vision-retry",
		ProviderKind:                  "cloud",
		Model:                         "resolved-vision-retry",
		InputProviderInstance:         inputProviderInstance,
		InputProviderDispatchRecorded: true,
		ContextPacket: json.RawMessage(`{
			"id":"ctx_old_retry",
			"refs":{"session_id":"chat_2","run_id":"run-retry-source"},
			"items":[{"kind":"transcript","trust_level":"runtime_state","origin":"chat.transcript","title":"Chat transcript","included":true}]
		}`),
	}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := store.CreateArtifact(ctx, types.TaskArtifact{
		ID:          "artifact-conversation",
		TaskID:      task.ID,
		RunID:       run.ID,
		Kind:        "agent_conversation",
		StorageKind: "inline",
		ContentText: `[{"role":"user","content":"first"},{"role":"assistant","content":"prior answer"},{"role":"user","content":"continue"},{"role":"assistant","content":"current answer"}]`,
		Status:      "ready",
		CreatedAt:   now,
	}); err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}
	if _, err := runner.RetryTaskFromModelCall(ctx, task, run, 2, "operator_retry", defaultResourceID); err == nil {
		t.Fatal("RetryTaskFromModelCall(model call 2) succeeded for a source Run with model_call_count=1")
	}

	result, err := runner.RetryTaskFromModelCall(ctx, task, run, 1, "operator_retry", defaultResourceID)
	if err != nil {
		t.Fatalf("RetryTaskFromModelCall: %v", err)
	}
	stored, found, err := store.GetRun(ctx, task.ID, result.Run.ID)
	if err != nil || !found {
		t.Fatalf("GetRun(%q) found=%v err=%v", result.Run.ID, found, err)
	}
	assertCopiedRunContextPacket(t, stored.ContextPacket, task.ID, result.Run.ID)
	if stored.InputRef != run.InputRef {
		t.Fatalf("InputRef = %q, want inherited %q", stored.InputRef, run.InputRef)
	}
	if stored.Provider != run.Provider || stored.ProviderKind != run.ProviderKind || stored.Model != run.Model || stored.InputProviderInstance != inputProviderInstance || !stored.InputProviderDispatchRecorded {
		t.Fatalf("inherited input route = provider %q kind %q model %q instance %+v dispatched=%t, want %q/%q/%q/%+v/true", stored.Provider, stored.ProviderKind, stored.Model, stored.InputProviderInstance, stored.InputProviderDispatchRecorded, run.Provider, run.ProviderKind, run.Model, inputProviderInstance)
	}
	checkpoint, err := runner.resumeCheckpointForRun(ctx, task.ID, stored.ID)
	if err != nil {
		t.Fatalf("resumeCheckpointForRun: %v", err)
	}
	if checkpoint == nil {
		t.Fatal("resumeCheckpointForRun returned nil")
	}
	var truncated []types.Message
	if err := json.Unmarshal(checkpoint.AgentConversation, &truncated); err != nil {
		t.Fatalf("checkpoint conversation = %s, want decodable truncated context", checkpoint.AgentConversation)
	}
	if len(truncated) != 3 || truncated[1].Role != "assistant" || truncated[1].Content != "prior answer" || truncated[2].Role != "user" || truncated[2].Content != "continue" {
		t.Fatalf("truncated conversation = %#v, want inherited assistant plus source Run prompt", truncated)
	}
}

func TestResumeCheckpointPrefersOwnRunProgressAndRepairsStreamingPartial(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	runner := NewRunner(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store,
		nil,
		Config{QueueWorkers: 0},
	)
	now := time.Now().UTC()
	task := types.Task{ID: "task-own-checkpoint", ExecutionKind: "agent_loop", Status: "running", CreatedAt: now, UpdatedAt: now}
	run := types.TaskRun{ID: "run-own-checkpoint", TaskID: task.ID, Number: 2, Status: "queued", StartedAt: now}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := store.AppendRunEvent(ctx, types.TaskRunEvent{
		ID:        "event-resumed",
		TaskID:    task.ID,
		RunID:     run.ID,
		EventType: "run.resumed_from_event",
		Data:      map[string]any{"from_run_id": "run-parent", "source_model_call_index": 1},
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("AppendRunEvent: %v", err)
	}
	if _, err := store.AppendStep(ctx, types.TaskStep{
		ID:       "step-model-1",
		TaskID:   task.ID,
		RunID:    run.ID,
		Index:    1,
		Kind:     "model",
		Status:   "completed",
		ToolName: "builtin.agent_loop_llm",
		Input:    map[string]any{"model_call_index": 1},
		OutputSummary: map[string]any{
			"run_cumulative_cost_micros_usd": int64(75),
		},
		StartedAt:  now,
		FinishedAt: now,
	}); err != nil {
		t.Fatalf("AppendStep: %v", err)
	}
	if _, err := store.CreateArtifact(ctx, types.TaskArtifact{
		ID:          "convo-" + run.ID,
		TaskID:      task.ID,
		RunID:       run.ID,
		Kind:        "agent_conversation",
		StorageKind: "inline",
		ContentText: `[{"role":"user","content":"work"},{"role":"assistant","content":"completed call"},{"role":"assistant","content":"partial next call"}]`,
		Status:      "streaming",
		CreatedAt:   now,
	}); err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}
	checkpoint, err := runner.resumeCheckpointForRun(ctx, task.ID, run.ID)
	if err != nil {
		t.Fatalf("resumeCheckpointForRun: %v", err)
	}
	if checkpoint == nil || !checkpoint.SameRun || checkpoint.SourceRunID != run.ID {
		t.Fatalf("checkpoint = %+v, want current-Run checkpoint", checkpoint)
	}
	if checkpoint.ThisRunModelCallCount != 1 || checkpoint.ThisRunCostMicrosUSD != 75 {
		t.Fatalf("checkpoint accounting = calls %d cost %d, want 1/75", checkpoint.ThisRunModelCallCount, checkpoint.ThisRunCostMicrosUSD)
	}
	var messages []types.Message
	if err := json.Unmarshal(checkpoint.AgentConversation, &messages); err != nil {
		t.Fatalf("decode checkpoint conversation: %v", err)
	}
	if len(messages) != 2 || messages[1].Content != "completed call" {
		t.Fatalf("checkpoint conversation = %+v, want completed boundary without partial tail", messages)
	}
	repaired, found, err := store.GetArtifact(ctx, task.ID, "convo-"+run.ID)
	if err != nil || !found {
		t.Fatalf("GetArtifact found=%t err=%v", found, err)
	}
	if repaired.Status != "ready" || strings.Contains(repaired.ContentText, "partial next call") {
		t.Fatalf("repaired artifact = %+v, want ready completed boundary", repaired)
	}
}

func TestOwnRunResumeCheckpointIncludesDurableToolDispatchSteps(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	runner := NewRunner(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store,
		nil,
		Config{QueueWorkers: 0},
	)
	now := time.Now().UTC()
	task := types.Task{ID: "task-dispatch-checkpoint", ExecutionKind: "agent_loop", Status: "running", CreatedAt: now, UpdatedAt: now}
	run := types.TaskRun{ID: "run-dispatch-checkpoint", TaskID: task.ID, Number: 1, Status: "queued", ModelCallCount: 1, StartedAt: now}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	call := agentLoopToolCall("call-1", "shell_exec", `{"command":"effect"}`)
	steps := []types.TaskStep{
		{
			ID: "step-model", TaskID: task.ID, RunID: run.ID, Index: 1,
			Kind: "model", Status: "completed", ToolName: "builtin.agent_loop_llm",
			Input: map[string]any{"model_call_index": 1}, StartedAt: now, FinishedAt: now,
		},
		{
			ID: "step-dispatch", TaskID: task.ID, RunID: run.ID, Index: 2,
			Kind: "tool", Status: "running", ToolName: call.Function.Name,
			Input: map[string]any{
				toolDispatchIntentVersionKey: agentToolDispatchIntentVersion,
				toolDispatchCallIDKey:        call.ID,
				toolDispatchDigestKey:        agentToolCallDigest(call),
				toolDispatchModelCallKey:     1,
			},
			StartedAt: now,
		},
		{
			ID: "step-legacy-tool", TaskID: task.ID, RunID: run.ID, Index: 3,
			Kind: "tool", Status: "completed", ToolName: "legacy", StartedAt: now, FinishedAt: now,
		},
	}
	for _, step := range steps {
		if _, err := store.AppendStep(ctx, step); err != nil {
			t.Fatalf("AppendStep(%s): %v", step.ID, err)
		}
	}
	messages, err := json.Marshal([]types.Message{
		{Role: "user", Content: "run it"},
		makeAssistantMsg("", call),
	})
	if err != nil {
		t.Fatalf("marshal conversation: %v", err)
	}
	if _, err := store.CreateArtifact(ctx, types.TaskArtifact{
		ID: "convo-" + run.ID, TaskID: task.ID, RunID: run.ID,
		Kind: "agent_conversation", StorageKind: "inline", ContentText: string(messages), Status: "ready", CreatedAt: now,
	}); err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}

	checkpoint, err := runner.resumeCheckpointForRun(ctx, task.ID, run.ID)
	if err != nil {
		t.Fatalf("resumeCheckpointForRun: %v", err)
	}
	if checkpoint == nil || !checkpoint.SameRun {
		t.Fatalf("checkpoint = %+v, want same-run checkpoint", checkpoint)
	}
	if len(checkpoint.ToolDispatchSteps) != 1 || checkpoint.ToolDispatchSteps[0].ID != "step-dispatch" {
		t.Fatalf("ToolDispatchSteps = %+v, want only durable dispatch intent", checkpoint.ToolDispatchSteps)
	}
}

func TestCrossRunResumePreservesDispatchEvidenceWhileRetryFromModelCallClearsIt(t *testing.T) {
	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	runner := NewRunner(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store,
		nil,
		Config{QueueWorkers: 0},
	)
	now := time.Now().UTC()
	task := types.Task{ID: "task-cross-run-dispatch", ExecutionKind: "agent_loop", Status: "failed", CreatedAt: now, UpdatedAt: now}
	sourceRun := types.TaskRun{ID: "run-source-dispatch", TaskID: task.ID, Number: 1, Status: "failed", ModelCallCount: 1, StartedAt: now, FinishedAt: now}
	resumeRun := types.TaskRun{ID: "run-ordinary-resume", TaskID: task.ID, Number: 2, Status: "queued", StartedAt: now}
	retryRun := types.TaskRun{ID: "run-model-retry", TaskID: task.ID, Number: 3, Status: "queued", StartedAt: now}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	for _, run := range []types.TaskRun{sourceRun, resumeRun, retryRun} {
		if _, err := store.CreateRun(ctx, run); err != nil {
			t.Fatalf("CreateRun(%s): %v", run.ID, err)
		}
	}
	call := agentLoopToolCall("call-effect", "shell_exec", `{"command":"non-idempotent-effect"}`)
	modelStep := types.TaskStep{
		ID: "step-source-model", TaskID: task.ID, RunID: sourceRun.ID, Index: 1,
		Kind: "model", Status: "completed", ToolName: "builtin.agent_loop_llm",
		Input: map[string]any{"model_call_index": 1}, StartedAt: now, FinishedAt: now,
	}
	dispatchStep := types.TaskStep{
		ID: "step-source-dispatch", TaskID: task.ID, RunID: sourceRun.ID, Index: 2,
		Kind: "tool", Status: "completed", ToolName: call.Function.Name,
		Input: map[string]any{
			toolDispatchIntentVersionKey: agentToolDispatchIntentVersion,
			toolDispatchCallIDKey:        call.ID,
			toolDispatchDigestKey:        agentToolCallDigest(call),
			toolDispatchModelCallKey:     1,
		},
		StartedAt: now, FinishedAt: now,
	}
	for _, step := range []types.TaskStep{modelStep, dispatchStep} {
		if _, err := store.AppendStep(ctx, step); err != nil {
			t.Fatalf("AppendStep(%s): %v", step.ID, err)
		}
	}
	messages, err := json.Marshal([]types.Message{
		{Role: "user", Content: "run it"},
		makeAssistantMsg("", call),
	})
	if err != nil {
		t.Fatalf("marshal source conversation: %v", err)
	}
	if _, err := store.CreateArtifact(ctx, types.TaskArtifact{
		ID: "convo-" + sourceRun.ID, TaskID: task.ID, RunID: sourceRun.ID,
		Kind: "agent_conversation", StorageKind: "inline", ContentText: string(messages), Status: "ready", CreatedAt: now,
	}); err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}
	for _, event := range []types.TaskRunEvent{
		{
			ID: "event-ordinary-resume", TaskID: task.ID, RunID: resumeRun.ID,
			EventType: "run.resumed_from_event", Data: map[string]any{"from_run_id": sourceRun.ID}, CreatedAt: now,
		},
		{
			ID: "event-model-retry", TaskID: task.ID, RunID: retryRun.ID,
			EventType: "run.resumed_from_event", Data: map[string]any{"from_run_id": sourceRun.ID, "source_model_call_index": 1}, CreatedAt: now,
		},
	} {
		if _, err := store.AppendRunEvent(ctx, event); err != nil {
			t.Fatalf("AppendRunEvent(%s): %v", event.ID, err)
		}
	}

	resumeCheckpoint, err := runner.resumeCheckpointForRun(ctx, task.ID, resumeRun.ID)
	if err != nil {
		t.Fatalf("ordinary resume checkpoint: %v", err)
	}
	if resumeCheckpoint == nil || resumeCheckpoint.SameRun || len(resumeCheckpoint.ToolDispatchSteps) != 1 ||
		resumeCheckpoint.ToolDispatchSteps[0].ID != dispatchStep.ID || resumeCheckpoint.ToolDispatchModelCallIndex != 1 {
		t.Fatalf("ordinary resume dispatch evidence = %+v, want source Step/model call", resumeCheckpoint)
	}
	ordinaryShell := &stubExecutor{result: &ExecutionResult{Status: "completed"}}
	ordinaryLLM := &scriptedLLM{responses: []*types.ChatResponse{makeChatResp(makeAssistantMsg("recovery acknowledged"))}}
	ordinaryLoop := NewAgentLoopExecutor(ordinaryLLM, ordinaryShell, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	ordinarySpec := newAgentLoopSpec(t)
	ordinarySpec.Task.ID = task.ID
	ordinarySpec.Run.ID = resumeRun.ID
	ordinarySpec.ResumeCheckpoint = resumeCheckpoint
	ordinaryResult, err := ordinaryLoop.Execute(ctx, ordinarySpec)
	if err != nil {
		t.Fatalf("ordinary resume Execute: %v", err)
	}
	if ordinaryResult.Status != "completed" || len(ordinaryShell.calls) != 0 {
		t.Fatalf("ordinary resume status = %q shell effects = %d, want fail-closed no replay", ordinaryResult.Status, len(ordinaryShell.calls))
	}
	if len(ordinaryLLM.lastReqs) != 1 {
		t.Fatalf("ordinary resume LLM requests = %d, want one", len(ordinaryLLM.lastReqs))
	}
	assertRecoveryToolError(t, ordinaryLLM.lastReqs[0].Messages, call.ID)

	retryCheckpoint, err := runner.resumeCheckpointForRun(ctx, task.ID, retryRun.ID)
	if err != nil {
		t.Fatalf("retry-from-model-call checkpoint: %v", err)
	}
	if retryCheckpoint == nil || len(retryCheckpoint.ToolDispatchSteps) != 0 || retryCheckpoint.ToolDispatchModelCallIndex != 0 {
		t.Fatalf("retry dispatch evidence = %+v, want deliberately cleared", retryCheckpoint)
	}
	retryShell := &stubExecutor{result: &ExecutionResult{Status: "completed"}}
	retryLLM := &scriptedLLM{responses: []*types.ChatResponse{
		makeChatResp(makeAssistantMsg("", call)),
		makeChatResp(makeAssistantMsg("explicit retry complete")),
	}}
	retryLoop := NewAgentLoopExecutor(retryLLM, retryShell, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{})
	retrySpec := newAgentLoopSpec(t)
	retrySpec.Task.ID = task.ID
	retrySpec.Run.ID = retryRun.ID
	retrySpec.ResumeCheckpoint = retryCheckpoint
	retryResult, err := retryLoop.Execute(ctx, retrySpec)
	if err != nil {
		t.Fatalf("retry-from-model-call Execute: %v", err)
	}
	if retryResult.Status != "completed" || len(retryShell.calls) != 1 {
		t.Fatalf("retry status = %q shell effects = %d, want one explicit replay", retryResult.Status, len(retryShell.calls))
	}
}

func TestSameRunPendingToolCallsApprovedRequiresLatestMatchingApproval(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	runner := NewRunner(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store,
		nil,
		Config{QueueWorkers: 0},
	)
	now := time.Now().UTC()
	task := types.Task{ID: "task-pending-approval", Status: "queued", CreatedAt: now, UpdatedAt: now}
	run := types.TaskRun{ID: "run-pending-approval", TaskID: task.ID, Number: 1, Status: "queued", StartedAt: now}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	approved := types.TaskApproval{
		ID: "approval-approved", TaskID: task.ID, RunID: run.ID,
		Kind: "agent_loop_tool_call", Status: "approved", StepID: "step-approved", CreatedAt: now, ResolvedAt: now,
	}
	pending := types.TaskApproval{
		ID: "approval-pending", TaskID: task.ID, RunID: run.ID,
		Kind: "agent_loop_tool_call", Status: "pending", CreatedAt: now,
	}
	for _, approval := range []types.TaskApproval{approved, pending} {
		if _, err := store.CreateApproval(ctx, approval); err != nil {
			t.Fatalf("CreateApproval(%s): %v", approval.ID, err)
		}
	}

	approvedCalls := []types.ToolCall{
		agentLoopToolCall("call-approved", "shell_exec", `{"command":"original"}`),
	}
	approvedStep := types.TaskStep{
		ID: "step-approved", Index: 2, Kind: "approval", ApprovalID: approved.ID,
		Input: map[string]any{toolCallBundleDigestKey: agentToolCallBundleDigest(approvedCalls)},
	}
	got, err := runner.sameRunPendingToolCallsApproved(ctx, task.ID, run.ID, []types.TaskStep{approvedStep}, approvedCalls)
	if err != nil || !got {
		t.Fatalf("approved latest Step = %t, err=%v; want true", got, err)
	}

	got, err = runner.sameRunPendingToolCallsApproved(ctx, task.ID, run.ID, []types.TaskStep{
		{ID: "step-unlinked", Index: 2, Kind: "approval", ApprovalID: approved.ID},
	}, approvedCalls)
	if err != nil || got {
		t.Fatalf("approval linked to another Step = %t, err=%v; want false", got, err)
	}

	mutatedCalls := []types.ToolCall{
		agentLoopToolCall("call-approved", "shell_exec", `{"command":"mutated"}`),
	}
	got, err = runner.sameRunPendingToolCallsApproved(ctx, task.ID, run.ID, []types.TaskStep{approvedStep}, mutatedCalls)
	if err != nil || got {
		t.Fatalf("approval for different call bundle = %t, err=%v; want false", got, err)
	}

	got, err = runner.sameRunPendingToolCallsApproved(ctx, task.ID, run.ID, []types.TaskStep{
		approvedStep,
		{ID: "step-tool", Index: 3, Kind: "tool"},
	}, approvedCalls)
	if err != nil || got {
		t.Fatalf("approved non-latest Step = %t, err=%v; want false", got, err)
	}

	got, err = runner.sameRunPendingToolCallsApproved(ctx, task.ID, run.ID, []types.TaskStep{
		{ID: "step-pending", Index: 2, Kind: "approval", ApprovalID: pending.ID},
	}, approvedCalls)
	if err != nil || got {
		t.Fatalf("pending latest Step = %t, err=%v; want false", got, err)
	}
}

func TestAgentLoop_MutatedApprovedBundleIsRegatedBeforeDispatch(t *testing.T) {
	ctx := context.Background()
	store := taskstate.NewMemoryStore()
	runner := NewRunner(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store,
		nil,
		Config{QueueWorkers: 0},
	)
	now := time.Now().UTC()
	task := types.Task{ID: "task-mutated-approval", Status: "queued", CreatedAt: now, UpdatedAt: now}
	run := types.TaskRun{ID: "run-mutated-approval", TaskID: task.ID, Number: 1, Status: "queued", StartedAt: now}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	originalCalls := []types.ToolCall{
		agentLoopToolCall("call-1", "shell_exec", `{"command":"original"}`),
	}
	mutatedCalls := []types.ToolCall{
		agentLoopToolCall("call-1", "shell_exec", `{"command":"mutated"}`),
	}
	approval := types.TaskApproval{
		ID: "approval-original", TaskID: task.ID, RunID: run.ID, StepID: "step-original",
		Kind: "agent_loop_tool_call", Status: "approved", CreatedAt: now, ResolvedAt: now,
	}
	if _, err := store.CreateApproval(ctx, approval); err != nil {
		t.Fatalf("CreateApproval: %v", err)
	}
	approvalStep := types.TaskStep{
		ID: "step-original", TaskID: task.ID, RunID: run.ID, Index: 2,
		Kind: "approval", Status: "completed", ApprovalID: approval.ID,
		Input: map[string]any{toolCallBundleDigestKey: agentToolCallBundleDigest(originalCalls)},
	}
	approved, err := runner.sameRunPendingToolCallsApproved(ctx, task.ID, run.ID, []types.TaskStep{approvalStep}, mutatedCalls)
	if err != nil {
		t.Fatalf("sameRunPendingToolCallsApproved: %v", err)
	}
	if approved {
		t.Fatal("mutated call bundle inherited approval")
	}

	saved, err := json.Marshal([]types.Message{
		{Role: "user", Content: "run it"},
		makeAssistantMsg("", mutatedCalls...),
	})
	if err != nil {
		t.Fatalf("marshal conversation: %v", err)
	}
	spec := newAgentLoopSpec(t)
	spec.Task.ID = task.ID
	spec.Run.ID = run.ID
	spec.ResumeCheckpoint = &ResumeCheckpoint{
		SourceRunID:              run.ID,
		SameRun:                  true,
		LastStepIndex:            approvalStep.Index,
		AgentConversation:        saved,
		ThisRunModelCallCount:    1,
		PendingToolCallsApproved: approved,
	}
	shell := &stubExecutor{result: &ExecutionResult{Status: "completed"}}
	loop := NewAgentLoopExecutor(&scriptedLLM{}, shell, &stubExecutor{}, &stubExecutor{}, 8, []string{"shell_exec"}, HTTPRequestPolicy{})
	res, err := loop.Execute(ctx, spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "awaiting_approval" || len(shell.calls) != 0 {
		t.Fatalf("result status = %q shell calls = %d, want fresh approval and no dispatch", res.Status, len(shell.calls))
	}
	if len(res.PendingApprovals) != 1 || len(res.Steps) != 1 {
		t.Fatalf("fresh approval result = approvals %+v steps %+v", res.PendingApprovals, res.Steps)
	}
	if got := res.Steps[0].Input[toolCallBundleDigestKey]; got != agentToolCallBundleDigest(mutatedCalls) {
		t.Fatalf("fresh approval digest = %v, want mutated bundle digest", got)
	}
}

func assertCopiedRunContextPacket(t *testing.T, raw json.RawMessage, taskID, runID string) {
	t.Helper()
	if len(raw) == 0 {
		t.Fatal("ContextPacket empty, want copied packet")
	}
	var packet map[string]any
	if err := json.Unmarshal(raw, &packet); err != nil {
		t.Fatalf("Unmarshal ContextPacket: %v", err)
	}
	if got, _ := packet["id"].(string); got == "" || got == "ctx_old" || got == "ctx_old_retry" {
		t.Fatalf("packet id = %q, want fresh context snapshot id", got)
	}
	refs, _ := packet["refs"].(map[string]any)
	if refs == nil {
		t.Fatalf("packet refs = nil, want task/run refs")
	}
	if got, _ := refs["task_id"].(string); got != taskID {
		t.Fatalf("refs.task_id = %q, want %q", got, taskID)
	}
	if got, _ := refs["run_id"].(string); got != runID {
		t.Fatalf("refs.run_id = %q, want %q", got, runID)
	}
}

func assertRunContextSourceRefs(t *testing.T, raw json.RawMessage, sessionID, turnID, messageID string) {
	t.Helper()
	var packet struct {
		Refs struct {
			SessionID string `json:"session_id"`
			TurnID    string `json:"turn_id"`
			MessageID string `json:"message_id"`
		} `json:"refs"`
	}
	if err := json.Unmarshal(raw, &packet); err != nil {
		t.Fatalf("Unmarshal ContextPacket source refs: %v", err)
	}
	if packet.Refs.SessionID != sessionID || packet.Refs.TurnID != turnID || packet.Refs.MessageID != messageID {
		t.Fatalf("source refs = %+v, want %s/%s/%s", packet.Refs, sessionID, turnID, messageID)
	}
}
