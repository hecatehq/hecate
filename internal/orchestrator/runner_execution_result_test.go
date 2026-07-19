package orchestrator

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/hecatehq/hecate/internal/eventprotocol"
	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/storage"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
)

type failingTerminalTransitionStore struct {
	taskstate.Store
	err error
}

type cancellationSettlementExecutor struct {
	persisted chan struct{}
}

func (e *cancellationSettlementExecutor) Execute(ctx context.Context, spec ExecutionSpec) (*ExecutionResult, error) {
	when := time.Now().UTC()
	step := types.TaskStep{
		ID:         "step-cancelled-model-call",
		TaskID:     spec.Task.ID,
		RunID:      spec.Run.ID,
		Index:      1,
		Kind:       "model",
		Title:      "Completed model call",
		Status:     "completed",
		Phase:      "planning",
		Result:     telemetry.ResultSuccess,
		ToolName:   "builtin.agent_loop_llm",
		Input:      map[string]any{"model_call_index": 1},
		StartedAt:  when,
		FinishedAt: when,
	}
	artifact := types.TaskArtifact{
		ID:          "convo-" + spec.Run.ID,
		TaskID:      spec.Task.ID,
		RunID:       spec.Run.ID,
		Kind:        "agent_conversation",
		Name:        "agent-conversation.json",
		StorageKind: "inline",
		ContentText: `[{"role":"user","content":"work"},{"role":"assistant","content":"completed answer"}]`,
		Status:      "ready",
		CreatedAt:   when,
	}
	if err := spec.UpsertStep(step); err != nil {
		return nil, err
	}
	if err := spec.UpsertArtifact(artifact); err != nil {
		return nil, err
	}
	close(e.persisted)
	<-ctx.Done()
	// Deliberately omit ModelCallCount to exercise reconstruction from the
	// completed durable model Step during cancellation settlement.
	return &ExecutionResult{
		Status:    "cancelled",
		Steps:     []types.TaskStep{step},
		Artifacts: []types.TaskArtifact{artifact},
		LastError: "run cancelled",
	}, nil
}

func (s failingTerminalTransitionStore) ApplyRunTerminalTransition(context.Context, taskstate.TerminalRunTransition) (taskstate.TerminalRunTransitionResult, error) {
	return taskstate.TerminalRunTransitionResult{}, s.err
}

func TestExecutionResultPersister_PersistsStepsArtifactsAndTerminalCounts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	runner, store, trace, task, run := newExecutionResultPersisterFixture(t, ctx, "counts")
	startedAt := time.Now().UTC().Add(-time.Second)
	finishedAt := startedAt.Add(50 * time.Millisecond)
	execution := &ExecutionResult{
		Status: "completed",
		Steps: []types.TaskStep{{
			ID:         "step-counts",
			TaskID:     task.ID,
			RunID:      run.ID,
			Index:      1,
			Kind:       "model",
			Title:      "Complete",
			Status:     "completed",
			Phase:      "execution",
			Result:     telemetry.ResultSuccess,
			ToolName:   "model",
			StartedAt:  startedAt,
			FinishedAt: finishedAt,
			RequestID:  run.RequestID,
			TraceID:    trace.TraceID,
		}},
		Artifacts: []types.TaskArtifact{{
			ID:          "artifact-counts",
			TaskID:      task.ID,
			RunID:       run.ID,
			StepID:      "step-counts",
			Kind:        "summary",
			Name:        "summary.txt",
			StorageKind: "inline",
			ContentText: "done",
			SizeBytes:   4,
			Status:      "ready",
			CreatedAt:   finishedAt,
			RequestID:   run.RequestID,
			TraceID:     trace.TraceID,
		}},
	}

	result, err := newExecutionResultPersister(runner, trace, task, run, run.RequestID).persist(ctx, execution)
	if err != nil {
		t.Fatalf("persist() error = %v", err)
	}

	if result.Run.Status != "completed" || result.Run.StepCount != 1 || result.Run.ArtifactCount != 1 {
		t.Fatalf("run = %+v, want completed with one step and one artifact", result.Run)
	}
	storedStep, found, err := store.GetStep(ctx, run.ID, "step-counts")
	if err != nil || !found {
		t.Fatalf("GetStep() found=%t error=%v", found, err)
	}
	if storedStep.SpanID == "" || storedStep.ParentSpanID != trace.RootSpanID() {
		t.Fatalf("step spans = span:%q parent:%q, want populated under root %q", storedStep.SpanID, storedStep.ParentSpanID, trace.RootSpanID())
	}
	storedArtifact, found, err := store.GetArtifact(ctx, task.ID, "artifact-counts")
	if err != nil || !found {
		t.Fatalf("GetArtifact() found=%t error=%v", found, err)
	}
	if storedArtifact.SpanID == "" {
		t.Fatalf("artifact span id empty: %+v", storedArtifact)
	}
	events, err := store.ListRunEvents(ctx, task.ID, run.ID, 0, 20)
	if err != nil {
		t.Fatalf("ListRunEvents() error = %v", err)
	}
	assertRunEventSubsequence(t, events, []string{"run.finished"})
}

func TestExecutionResultPersister_CountsAllDurableRecoveryChildren(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	runner, store, trace, task, run := newExecutionResultPersisterFixture(t, ctx, "recovery-children")
	when := time.Now().UTC()
	if _, err := store.AppendStep(ctx, types.TaskStep{
		ID: "step-before-recovery", TaskID: task.ID, RunID: run.ID, Index: 1,
		Kind: "model", Status: "completed", ToolName: "builtin.agent_loop_llm",
		Input: map[string]any{"model_call_index": 1}, StartedAt: when, FinishedAt: when,
	}); err != nil {
		t.Fatalf("AppendStep() error = %v", err)
	}
	if _, err := store.CreateArtifact(ctx, types.TaskArtifact{
		ID: "convo-" + run.ID, TaskID: task.ID, RunID: run.ID,
		Kind: "agent_conversation", StorageKind: "inline", Status: "ready", CreatedAt: when,
		ContentText: `[{"role":"user","content":"work"},{"role":"assistant","content":"saved final"}]`,
	}); err != nil {
		t.Fatalf("CreateArtifact() error = %v", err)
	}
	finalAnswerID := agentLoopFinalAnswerArtifactID(run.ID)
	if _, err := store.CreateArtifact(ctx, types.TaskArtifact{
		ID: finalAnswerID, TaskID: task.ID, RunID: run.ID,
		Kind: "summary", Name: "agent-final-answer.txt", StorageKind: "inline", Status: "ready", CreatedAt: when,
		ContentText: "saved final",
	}); err != nil {
		t.Fatalf("CreateArtifact(final answer) error = %v", err)
	}

	result, err := newExecutionResultPersister(runner, trace, task, run, run.RequestID).persist(ctx, &ExecutionResult{
		Status: "completed",
		Artifacts: []types.TaskArtifact{{
			ID: finalAnswerID, TaskID: task.ID, RunID: run.ID,
			Kind: "summary", Name: "agent-final-answer.txt", StorageKind: "inline",
			ContentText: "saved final", Status: "ready", CreatedAt: when,
		}},
	})
	if err != nil {
		t.Fatalf("persist() error = %v", err)
	}
	if result.Run.StepCount != 1 || result.Run.ArtifactCount != 2 || result.Run.ModelCallCount != 1 {
		t.Fatalf("terminal counts = steps:%d artifacts:%d model_calls:%d, want 1/2/1 from all durable children", result.Run.StepCount, result.Run.ArtifactCount, result.Run.ModelCallCount)
	}
}

func TestRunnerCancellationSettlementPreservesSQLiteModelCallCountForRetry(t *testing.T) {
	ctx := context.Background()
	client, err := storage.NewSQLiteClient(ctx, storage.SQLiteConfig{
		Path:        filepath.Join(t.TempDir(), "cancellation-settlement.db"),
		TablePrefix: "cancellation_settlement",
	})
	if err != nil {
		t.Fatalf("NewSQLiteClient() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	store, err := taskstate.NewSQLiteStore(ctx, client)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	runner := newClaimedRunProcessorTestRunner(store, &recordingQueue{})
	runner.workspaces = NewWorkspaceManager("")
	executor := &cancellationSettlementExecutor{persisted: make(chan struct{})}
	runner.agent = executor

	now := time.Now().UTC().Add(-time.Second)
	workspace := t.TempDir()
	task := types.Task{
		ID: "task-cancellation-settlement", Title: "Cancellation settlement", Prompt: "work",
		Status: "running", ExecutionKind: "agent_loop", RequestedModel: "test-model",
		WorkingDirectory: workspace, SandboxAllowedRoot: workspace,
		CreatedAt: now, UpdatedAt: now, StartedAt: now,
	}
	run := types.TaskRun{
		ID: "run-cancellation-settlement", TaskID: task.ID, Number: 1,
		Status: "running", Model: "test-model", StartedAt: now,
		RequestID: "request-cancellation-settlement",
	}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	trace := runner.tracer.Start(run.RequestID)
	defer trace.Finalize()

	jobCtx, cancelJob := context.WithCancel(ctx)
	type executeOutcome struct {
		result *StartTaskResult
		err    error
	}
	executed := make(chan executeOutcome, 1)
	go func() {
		result, err := runner.executeRun(jobCtx, trace, task, run, run.RequestID, nil)
		executed <- executeOutcome{result: result, err: err}
	}()
	select {
	case <-executor.persisted:
	case <-time.After(3 * time.Second):
		t.Fatal("executor did not persist its completed model call")
	}

	winner, err := runner.applyTerminalRunTransition(ctx, cancelRunTerminalTransition(
		task, run, "run cancelled: operator stop", run.RequestID, trace.TraceID, trace, time.Now().UTC(),
	))
	if err != nil {
		t.Fatalf("apply cancellation winner error = %v", err)
	}
	if winner.Run.Status != "cancelled" {
		t.Fatalf("winner status = %q, want cancelled", winner.Run.Status)
	}
	cancelJob()
	var outcome executeOutcome
	select {
	case outcome = <-executed:
	case <-time.After(3 * time.Second):
		t.Fatal("executor settlement did not finish after cancellation")
	}
	if outcome.err != nil {
		t.Fatalf("executeRun() settlement error = %v", outcome.err)
	}
	if outcome.result == nil || outcome.result.Run.Status != "cancelled" {
		t.Fatalf("executeRun() result = %+v, want durable cancellation winner", outcome.result)
	}

	storedRun, found, err := store.GetRun(ctx, task.ID, run.ID)
	if err != nil || !found {
		t.Fatalf("GetRun() found=%t error=%v", found, err)
	}
	if storedRun.ModelCallCount != 1 || storedRun.StepCount != 1 || storedRun.ArtifactCount != 1 {
		t.Fatalf("stored cancellation accounting = model_calls:%d steps:%d artifacts:%d, want 1/1/1", storedRun.ModelCallCount, storedRun.StepCount, storedRun.ArtifactCount)
	}
	storedTask, found, err := store.GetTask(ctx, task.ID)
	if err != nil || !found {
		t.Fatalf("GetTask() found=%t error=%v", found, err)
	}
	if _, err := runner.RetryTaskFromModelCall(ctx, storedTask, storedRun, 1, "operator_retry", defaultResourceID); err != nil {
		t.Fatalf("RetryTaskFromModelCall() error = %v", err)
	}
}

func TestExecutionResultPersister_EmitsModelCallCostsAndPersistsTotalCost(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	runner, store, trace, task, run := newExecutionResultPersisterFixture(t, ctx, "model-call-cost")
	run.PriorCostMicrosUSD = 1_000
	if _, err := store.UpdateRun(ctx, run); err != nil {
		t.Fatalf("UpdateRun() error = %v", err)
	}
	execution := &ExecutionResult{
		Status:         "completed",
		Provider:       "openai",
		ProviderKind:   "openai",
		Model:          "gpt-4.1",
		CostMicrosUSD:  250,
		ModelCallCount: 2,
		ModelCallCosts: []ModelCallCostRecord{{
			ModelCall:           2,
			StepID:              "step-model-2",
			CostMicrosUSD:       150,
			CumulativeMicrosUSD: 250,
			ToolCallCount:       3,
		}},
	}

	result, err := newExecutionResultPersister(runner, trace, task, run, run.RequestID).persist(ctx, execution)
	if err != nil {
		t.Fatalf("persist() error = %v", err)
	}

	if result.Run.TotalCostMicrosUSD != 250 || result.Run.ModelCallCount != 2 || result.Run.Model != "gpt-4.1" || result.Run.Provider != "openai" {
		t.Fatalf("run accounting/route = %+v, want cost and route persisted", result.Run)
	}
	event := requireRunEvent(t, store, task.ID, run.ID, "model.call.completed")
	assertEventData(t, event.Data, "model_call_index", 2)
	assertEventData(t, event.Data, "step_id", "step-model-2")
	assertEventData(t, event.Data, "cost_micros_usd", int64(150))
	assertEventData(t, event.Data, "run_cumulative_cost_micros_usd", int64(250))
	assertEventData(t, event.Data, "task_cumulative_cost_micros_usd", int64(1_250))
	assertEventData(t, event.Data, "tool_calls", 3)
}

func TestExecutionResultPersister_FailedFirstModelCallNormalizesZeroCompletedModelCalls(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	runner, store, trace, task, run := newExecutionResultPersisterFixture(t, ctx, "failed-first-model-call")
	execution := &ExecutionResult{
		Status:            "failed",
		LastError:         "model call 1 failed: upstream unavailable",
		OtelStatusCode:    "error",
		OtelStatusMessage: "upstream unavailable",
	}

	result, err := newExecutionResultPersister(runner, trace, task, run, run.RequestID).persist(ctx, execution)
	if err != nil {
		t.Fatalf("persist() error = %v", err)
	}
	if result.Run.ModelCallCount != 0 {
		t.Fatalf("completed model calls = %d, want 0", result.Run.ModelCallCount)
	}

	event := requireRunEvent(t, store, task.ID, run.ID, "run.failed")
	if got, ok := event.Data["model_call_count"]; !ok || got != 0 {
		t.Fatalf("raw model_call_count = %T(%v), present=%t, want explicit int(0); data=%+v", got, got, ok, event.Data)
	}
	for _, legacyKey := range []string{"turns", "turn_count"} {
		if _, ok := event.Data[legacyKey]; ok {
			t.Fatalf("raw terminal event contains legacy key %q: %+v", legacyKey, event.Data)
		}
	}

	envelope := eventprotocol.FromTaskRunEvent(event)
	if got, ok := envelope.Data["model_call_count"]; !ok || got != 0 {
		t.Fatalf("normalized model_call_count = %T(%v), present=%t, want explicit int(0); data=%+v", got, got, ok, envelope.Data)
	}
}

func TestExecutionResultPersister_ReportsDurableCancellationWinner(t *testing.T) {
	ctx := t.Context()
	runner, store, trace, task, run := newExecutionResultPersisterFixture(t, ctx, "durable-winner-metrics")
	metrics, reader := newMetricsForTest(t)
	runner.SetMetrics(metrics)
	winnerFinishedAt := run.StartedAt.Add(250 * time.Millisecond)
	winner, err := runner.applyTerminalRunTransition(ctx, cancelRunTerminalTransition(
		task,
		run,
		"run cancelled: operator winner",
		run.RequestID,
		trace.TraceID,
		trace,
		winnerFinishedAt,
	))
	if err != nil {
		t.Fatalf("apply cancellation winner: %v", err)
	}
	if winner.Run.Status != "cancelled" {
		t.Fatalf("winner run status = %q, want cancelled", winner.Run.Status)
	}

	result, err := newExecutionResultPersister(runner, trace, task, run, run.RequestID).persist(ctx, &ExecutionResult{
		Status:        "completed",
		Provider:      "actual-provider",
		ProviderKind:  "openai",
		Model:         "actual-model",
		CostMicrosUSD: 325,
	})
	if err != nil {
		t.Fatalf("persist completed loser: %v", err)
	}
	if result.Run.Status != "cancelled" || result.Run.Model != "actual-model" || result.Run.TotalCostMicrosUSD != 325 ||
		!result.Run.FinishedAt.Equal(winnerFinishedAt) {
		t.Fatalf("durable result run = %+v, want cancellation winner enriched with executor metadata", result.Run)
	}
	storedTask, found, err := store.GetTask(ctx, task.ID)
	if err != nil || !found || storedTask.Status != "cancelled" {
		t.Fatalf("stored task = %+v found=%t err=%v, want cancelled", storedTask, found, err)
	}

	var collected metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &collected); err != nil {
		t.Fatalf("Collect metrics: %v", err)
	}
	var runs metricdata.Sum[int64]
	var duration metricdata.Histogram[int64]
	for _, scope := range collected.ScopeMetrics {
		for _, metric := range scope.Metrics {
			switch metric.Name {
			case telemetry.MetricOrchestratorRunsTotal:
				runs, _ = metric.Data.(metricdata.Sum[int64])
			case telemetry.MetricOrchestratorRunDuration:
				duration, _ = metric.Data.(metricdata.Histogram[int64])
			}
		}
	}
	if len(runs.DataPoints) != 1 || runs.DataPoints[0].Value != 1 {
		t.Fatalf("run counter = %+v, want one durable terminal record", runs.DataPoints)
	}
	if got := metricAttribute(runs.DataPoints[0].Attributes, telemetry.AttrHecateRunStatus); got != "cancelled" {
		t.Fatalf("run metric status = %q, want cancelled", got)
	}
	if got := metricAttribute(runs.DataPoints[0].Attributes, telemetry.AttrGenAIRequestModel); got != "actual-model" {
		t.Fatalf("run metric model = %q, want actual-model", got)
	}
	if len(duration.DataPoints) != 1 || duration.DataPoints[0].Sum != 250 {
		t.Fatalf("run duration = %+v, want durable winner duration 250ms", duration.DataPoints)
	}

	runFinished := 0
	taskFinished := 0
	for _, event := range trace.Events() {
		switch event.Name {
		case telemetry.EventOrchestratorRunFinished:
			runFinished++
			if event.Attributes[telemetry.AttrHecateResult] != telemetry.ResultError ||
				event.Attributes[telemetry.AttrHecateRunDurationMS] != int64(250) {
				t.Fatalf("run finished trace = %+v, want cancelled winner result/duration", event)
			}
		case telemetry.EventOrchestratorTaskFinished:
			taskFinished++
			if event.Attributes[telemetry.AttrHecateResult] != telemetry.ResultError {
				t.Fatalf("task finished trace = %+v, want error result", event)
			}
		}
	}
	if runFinished != 1 || taskFinished != 1 {
		t.Fatalf("terminal trace events = run:%d task:%d, want one each", runFinished, taskFinished)
	}
	events, err := store.ListRunEvents(ctx, task.ID, run.ID, 0, 20)
	if err != nil {
		t.Fatalf("ListRunEvents: %v", err)
	}
	for _, event := range events {
		if event.EventType == "run.finished" {
			t.Fatalf("completed loser emitted terminal event: %+v", event)
		}
	}
}

func TestExecutionResultPersister_TransitionErrorEmitsNoTerminalTelemetry(t *testing.T) {
	ctx := t.Context()
	runner, store, trace, task, run := newExecutionResultPersisterFixture(t, ctx, "terminal-error-metrics")
	metrics, reader := newMetricsForTest(t)
	runner.SetMetrics(metrics)
	terminalErr := errors.New("terminal transition failed")
	runner.store = failingTerminalTransitionStore{Store: store, err: terminalErr}

	_, err := newExecutionResultPersister(runner, trace, task, run, run.RequestID).persist(ctx, &ExecutionResult{Status: "completed"})
	if !errors.Is(err, terminalErr) {
		t.Fatalf("persist error = %v, want %v", err, terminalErr)
	}
	for _, event := range trace.Events() {
		if event.Name == telemetry.EventOrchestratorRunFinished || event.Name == telemetry.EventOrchestratorTaskFinished {
			t.Fatalf("failed terminal transition emitted terminal trace event: %+v", event)
		}
	}
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &collected); err != nil {
		t.Fatalf("Collect metrics: %v", err)
	}
	for _, scope := range collected.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name != telemetry.MetricOrchestratorRunsTotal {
				continue
			}
			if runs, ok := metric.Data.(metricdata.Sum[int64]); ok && len(runs.DataPoints) != 0 {
				t.Fatalf("failed terminal transition emitted run metric: %+v", runs.DataPoints)
			}
		}
	}
}

func metricAttribute(attributes attribute.Set, key string) string {
	value, ok := attributes.Value(attribute.Key(key))
	if !ok {
		return ""
	}
	return value.AsString()
}

func TestExecutionResultPersister_PersistsPendingApprovalAndRequestedEvent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	runner, store, trace, task, run := newExecutionResultPersisterFixture(t, ctx, "approval")
	approval := types.TaskApproval{
		ID:          "approval-result",
		TaskID:      task.ID,
		RunID:       run.ID,
		StepID:      "step-awaiting",
		Kind:        "agent_loop_tool_call",
		Status:      "pending",
		Reason:      "tool requires approval",
		RequestedBy: "agent",
		CreatedAt:   time.Now().UTC(),
		RequestID:   run.RequestID,
		TraceID:     trace.TraceID,
	}
	execution := &ExecutionResult{
		Status:           "awaiting_approval",
		PendingApprovals: []types.TaskApproval{approval},
	}

	result, err := newExecutionResultPersister(runner, trace, task, run, run.RequestID).persist(ctx, execution)
	if err != nil {
		t.Fatalf("persist() error = %v", err)
	}
	if result.Run.Status != "awaiting_approval" {
		t.Fatalf("run status = %q, want awaiting_approval", result.Run.Status)
	}
	storedApproval, found, err := store.GetApproval(ctx, task.ID, approval.ID)
	if err != nil || !found {
		t.Fatalf("GetApproval() found=%t error=%v", found, err)
	}
	if storedApproval.SpanID != trace.RootSpanID() {
		t.Fatalf("approval span id = %q, want root span %q", storedApproval.SpanID, trace.RootSpanID())
	}
	event := requireRunEvent(t, store, task.ID, run.ID, "approval.requested")
	assertEventData(t, event.Data, "approval_id", approval.ID)
	assertEventData(t, event.Data, "step_id", approval.StepID)
	assertEventData(t, event.Data, "requested_by", "agent")
}

func TestExecutionResultPersister_PolicyDeniedStepIsNotExecutionFailureTelemetry(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	runner, _, trace, task, run := newExecutionResultPersisterFixture(t, ctx, "policy-denied")
	newExecutionResultPersister(runner, trace, task, run, run.RequestID).recordStep(ctx, types.TaskStep{
		ID:       "step-policy-denied",
		TaskID:   task.ID,
		RunID:    run.ID,
		Index:    1,
		Kind:     "tool",
		Title:    "http_request (blocked)",
		Status:   "completed",
		Phase:    "policy",
		Result:   telemetry.ResultDenied,
		ToolName: "http_request",
	})

	var completed *types.TraceEvent
	for _, event := range trace.Events() {
		if event.Name == telemetry.EventOrchestratorStepFailed {
			t.Fatalf("policy denial emitted execution failure telemetry: %+v", event)
		}
		if event.Name == telemetry.EventOrchestratorStepCompleted {
			eventCopy := event
			completed = &eventCopy
		}
	}
	if completed == nil || completed.Attributes[telemetry.AttrHecateResult] != telemetry.ResultDenied {
		t.Fatalf("completed policy telemetry = %+v, want result=%q", completed, telemetry.ResultDenied)
	}
}

func newExecutionResultPersisterFixture(t *testing.T, ctx context.Context, suffix string) (*Runner, taskstate.Store, *profiler.Trace, types.Task, types.TaskRun) {
	t.Helper()
	store := taskstate.NewMemoryStore()
	runner := newClaimedRunProcessorTestRunner(store, &recordingQueue{})
	trace := runner.tracer.Start("request-" + suffix)
	now := time.Now().UTC().Add(-time.Second)
	task := types.Task{
		ID:            "task-" + suffix,
		Title:         "Execution result",
		Prompt:        "persist",
		Status:        "running",
		ExecutionKind: "agent_loop",
		CreatedAt:     now,
		UpdatedAt:     now,
		StartedAt:     now,
	}
	run := types.TaskRun{
		ID:         "run-" + suffix,
		TaskID:     task.ID,
		Number:     1,
		Status:     "running",
		StartedAt:  now,
		RequestID:  trace.RequestID,
		TraceID:    trace.TraceID,
		RootSpanID: trace.RootSpanID(),
	}
	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	return runner, store, trace, task, run
}

func requireRunEvent(t *testing.T, store taskstate.Store, taskID, runID, eventType string) types.TaskRunEvent {
	t.Helper()
	events, err := store.ListRunEvents(context.Background(), taskID, runID, 0, 50)
	if err != nil {
		t.Fatalf("ListRunEvents() error = %v", err)
	}
	for _, event := range events {
		if event.EventType == eventType {
			return event
		}
	}
	got := make([]string, 0, len(events))
	for _, event := range events {
		got = append(got, event.EventType)
	}
	t.Fatalf("missing event %q; got %v", eventType, got)
	return types.TaskRunEvent{}
}

func assertEventData(t *testing.T, data map[string]any, key string, want any) {
	t.Helper()
	if got := data[key]; got != want {
		t.Fatalf("event data[%q] = %T(%v), want %T(%v); data=%+v", key, got, got, want, want, data)
	}
}
