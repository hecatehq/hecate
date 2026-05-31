package orchestrator

import (
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestTerminalRunTransitionBuilders_CancelRun(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	trace := profiler.NewInMemoryTracer(nil).Start("request-cancel-builder")
	defer trace.Finalize()
	task := types.Task{ID: "task-cancel"}
	run := types.TaskRun{ID: "run-cancel", TaskID: task.ID}

	transition := cancelRunTerminalTransition(task, run, "run cancelled: operator stop", trace.RequestID, trace.TraceID, trace, now)

	if transition.Task.ID != task.ID || transition.Run.ID != run.ID {
		t.Fatalf("transition task/run = %q/%q, want %q/%q", transition.Task.ID, transition.Run.ID, task.ID, run.ID)
	}
	if transition.Status != "cancelled" || transition.Message != "run cancelled: operator stop" {
		t.Fatalf("transition status/message = %q/%q, want cancelled/operator reason", transition.Status, transition.Message)
	}
	if transition.RequestID != trace.RequestID || transition.TraceID != trace.TraceID || transition.Trace != trace {
		t.Fatalf("transition trace wiring = request:%q trace:%q ptr:%p, want request:%q trace:%q ptr:%p", transition.RequestID, transition.TraceID, transition.Trace, trace.RequestID, trace.TraceID, trace)
	}
	if !transition.Now.Equal(now) {
		t.Fatalf("transition now = %s, want %s", transition.Now, now)
	}
	if !transition.CancelActiveSteps || !transition.CancelStreamingArtifacts || !transition.CancelPendingApprovals || !transition.EmitTaskUpdated {
		t.Fatalf("cancel flags = steps:%t artifacts:%t approvals:%t task_updated:%t, want all true", transition.CancelActiveSteps, transition.CancelStreamingArtifacts, transition.CancelPendingApprovals, transition.EmitTaskUpdated)
	}
	if got := transition.EventData["reason"]; got != "run cancelled: operator stop" {
		t.Fatalf("reason event data = %v, want operator reason", got)
	}
	if transition.SkipIfStoredTerminalStatus || transition.SuppressDuplicateEvent {
		t.Fatalf("duplicate flags = skip:%t suppress:%t, want false", transition.SkipIfStoredTerminalStatus, transition.SuppressDuplicateEvent)
	}
}

func TestTerminalRunTransitionBuilders_FailedRun(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	trace := profiler.NewInMemoryTracer(nil).Start("request-failed-builder")
	defer trace.Finalize()
	task := types.Task{ID: "task-failed"}
	run := types.TaskRun{ID: "run-failed", TaskID: task.ID}

	transition := failedRunTerminalTransition(task, run, trace.RequestID, "failed", "executor boom", trace, now)

	if transition.Task.ID != task.ID || transition.Run.ID != run.ID {
		t.Fatalf("transition task/run = %q/%q, want %q/%q", transition.Task.ID, transition.Run.ID, task.ID, run.ID)
	}
	if transition.Status != "failed" || transition.Message != "executor boom" {
		t.Fatalf("transition status/message = %q/%q, want failed/executor boom", transition.Status, transition.Message)
	}
	if transition.RequestID != trace.RequestID || transition.TraceID != trace.TraceID || transition.Trace != trace {
		t.Fatalf("transition trace wiring = request:%q trace:%q ptr:%p, want request:%q trace:%q ptr:%p", transition.RequestID, transition.TraceID, transition.Trace, trace.RequestID, trace.TraceID, trace)
	}
	if !transition.Now.Equal(now) {
		t.Fatalf("transition now = %s, want %s", transition.Now, now)
	}
	if !transition.SkipIfStoredTerminalStatus {
		t.Fatalf("SkipIfStoredTerminalStatus = false, want true")
	}
	if transition.CancelActiveSteps || transition.CancelStreamingArtifacts || transition.CancelPendingApprovals || transition.EmitTaskUpdated || transition.EventData != nil {
		t.Fatalf("unexpected cancel/event settings: steps=%t artifacts=%t approvals=%t task_updated=%t data=%+v", transition.CancelActiveSteps, transition.CancelStreamingArtifacts, transition.CancelPendingApprovals, transition.EmitTaskUpdated, transition.EventData)
	}
}

func TestTerminalRunTransitionBuilders_ExecutionResult(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	trace := profiler.NewInMemoryTracer(nil).Start("request-result-builder")
	defer trace.Finalize()
	task := types.Task{ID: "task-result"}
	run := types.TaskRun{
		ID:                 "run-result",
		TaskID:             task.ID,
		Provider:           "fallback-provider",
		ProviderKind:       "fallback-kind",
		Model:              "fallback-model",
		TotalCostMicrosUSD: 99,
		OtelStatusCode:     "old",
		OtelStatusMessage:  "old message",
		PriorCostMicrosUSD: 10,
		WorkspacePath:      "/tmp/workspace",
	}
	execution := &ExecutionResult{
		Status:            "completed",
		ProviderKind:      "openai",
		Model:             "gpt-4.1",
		CostMicrosUSD:     250,
		OtelStatusCode:    "ok",
		OtelStatusMessage: "fine",
	}
	steps := []types.TaskStep{{ID: "step-1"}, {ID: "step-2"}}
	artifacts := []types.TaskArtifact{{ID: "artifact-1"}}

	transition := executionResultTerminalTransition(executionResultTerminalTransitionInput{
		Task:               task,
		Run:                run,
		Execution:          execution,
		PersistedSteps:     steps,
		PersistedArtifacts: artifacts,
		RequestID:          trace.RequestID,
		Trace:              trace,
		FinishedAt:         now,
	})

	if run.Status != "" || run.Provider != "fallback-provider" || run.TotalCostMicrosUSD != 99 {
		t.Fatalf("input run mutated = %+v", run)
	}
	if transition.Task.ID != task.ID || transition.Run.ID != run.ID {
		t.Fatalf("transition task/run = %q/%q, want %q/%q", transition.Task.ID, transition.Run.ID, task.ID, run.ID)
	}
	if transition.Status != "completed" || transition.Message != "" || transition.RequestID != trace.RequestID || transition.TraceID != trace.TraceID {
		t.Fatalf("transition basics = status:%q message:%q request:%q trace:%q", transition.Status, transition.Message, transition.RequestID, transition.TraceID)
	}
	if !transition.SuppressDuplicateEvent {
		t.Fatalf("SuppressDuplicateEvent = false, want true")
	}
	if transition.Run.Provider != "fallback-provider" || transition.Run.ProviderKind != "openai" || transition.Run.Model != "gpt-4.1" {
		t.Fatalf("route = provider:%q kind:%q model:%q, want fallback provider + execution kind/model", transition.Run.Provider, transition.Run.ProviderKind, transition.Run.Model)
	}
	if transition.Run.StepCount != 2 || transition.Run.ArtifactCount != 1 || transition.Run.TotalCostMicrosUSD != 250 {
		t.Fatalf("counts/cost = steps:%d artifacts:%d cost:%d, want 2/1/250", transition.Run.StepCount, transition.Run.ArtifactCount, transition.Run.TotalCostMicrosUSD)
	}
	if !transition.Run.FinishedAt.Equal(now) || transition.Run.OtelStatusCode != "ok" || transition.Run.OtelStatusMessage != "fine" {
		t.Fatalf("terminal fields = finished:%s code:%q message:%q", transition.Run.FinishedAt, transition.Run.OtelStatusCode, transition.Run.OtelStatusMessage)
	}

	targetRun := types.TaskRun{}
	transition.UpdateRun(&targetRun)
	if targetRun.Provider != transition.Run.Provider || targetRun.ProviderKind != transition.Run.ProviderKind || targetRun.Model != transition.Run.Model {
		t.Fatalf("UpdateRun route = %+v, want route from transition run %+v", targetRun, transition.Run)
	}
	if targetRun.StepCount != transition.Run.StepCount || targetRun.ArtifactCount != transition.Run.ArtifactCount || targetRun.TotalCostMicrosUSD != transition.Run.TotalCostMicrosUSD {
		t.Fatalf("UpdateRun counts/cost = %+v, want transition values %+v", targetRun, transition.Run)
	}
	targetTask := types.Task{}
	transition.UpdateTask(&targetTask)
	if targetTask.RootTraceID != trace.TraceID {
		t.Fatalf("UpdateTask root trace = %q, want %q", targetTask.RootTraceID, trace.TraceID)
	}
}

func TestTerminalRunTransitionBuilders_ExecutionResultPreservesExistingTotalCostOnZeroCost(t *testing.T) {
	t.Parallel()

	trace := profiler.NewInMemoryTracer(nil).Start("request-result-zero-cost")
	defer trace.Finalize()
	run := types.TaskRun{
		ID:                 "run-result-zero-cost",
		TaskID:             "task-result-zero-cost",
		TotalCostMicrosUSD: 99,
	}

	transition := executionResultTerminalTransition(executionResultTerminalTransitionInput{
		Task:       types.Task{ID: "task-result-zero-cost"},
		Run:        run,
		Execution:  &ExecutionResult{},
		RequestID:  trace.RequestID,
		Trace:      trace,
		FinishedAt: time.Now().UTC(),
	})

	if transition.Run.Status != "completed" {
		t.Fatalf("status = %q, want completed default", transition.Run.Status)
	}
	if transition.Run.TotalCostMicrosUSD != 99 {
		t.Fatalf("total cost = %d, want preserved 99", transition.Run.TotalCostMicrosUSD)
	}
	if transition.Run.OtelStatusCode != "ok" {
		t.Fatalf("otel status code = %q, want ok default", transition.Run.OtelStatusCode)
	}
}
