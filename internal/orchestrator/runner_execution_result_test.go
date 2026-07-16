package orchestrator

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
)

type failingTerminalTransitionStore struct {
	taskstate.Store
	err error
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

func TestExecutionResultPersister_EmitsTurnCostsAndPersistsTotalCost(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	runner, store, trace, task, run := newExecutionResultPersisterFixture(t, ctx, "turn-cost")
	run.PriorCostMicrosUSD = 1_000
	if _, err := store.UpdateRun(ctx, run); err != nil {
		t.Fatalf("UpdateRun() error = %v", err)
	}
	execution := &ExecutionResult{
		Status:        "completed",
		Provider:      "openai",
		ProviderKind:  "openai",
		Model:         "gpt-4.1",
		CostMicrosUSD: 250,
		TurnCosts: []TurnCostRecord{{
			Turn:                2,
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

	if result.Run.TotalCostMicrosUSD != 250 || result.Run.Model != "gpt-4.1" || result.Run.Provider != "openai" {
		t.Fatalf("run accounting/route = %+v, want cost and route persisted", result.Run)
	}
	event := requireRunEvent(t, store, task.ID, run.ID, "turn.completed")
	assertEventData(t, event.Data, "turn_index", 2)
	assertEventData(t, event.Data, "step_id", "step-model-2")
	assertEventData(t, event.Data, "cost_micros_usd", int64(150))
	assertEventData(t, event.Data, "run_cumulative_cost_micros_usd", int64(250))
	assertEventData(t, event.Data, "task_cumulative_cost_micros_usd", int64(1_250))
	assertEventData(t, event.Data, "tool_calls", 3)
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
