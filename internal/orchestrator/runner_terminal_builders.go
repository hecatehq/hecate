package orchestrator

import (
	"time"

	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/pkg/types"
)

type executionResultTerminalTransitionInput struct {
	Task               types.Task
	Run                types.TaskRun
	Execution          *ExecutionResult
	PersistedSteps     []types.TaskStep
	PersistedArtifacts []types.TaskArtifact
	RequestID          string
	Trace              *profiler.Trace
	FinishedAt         time.Time
}

func cancelRunTerminalTransition(task types.Task, run types.TaskRun, message, requestID, traceID string, trace *profiler.Trace, now time.Time) terminalRunTransition {
	return terminalRunTransition{
		Task:                     task,
		Run:                      run,
		Status:                   "cancelled",
		Message:                  message,
		RequestID:                requestID,
		TraceID:                  traceID,
		Trace:                    trace,
		Now:                      now,
		CancelActiveSteps:        true,
		CancelStreamingArtifacts: true,
		CancelPendingApprovals:   true,
		EmitTaskUpdated:          true,
		EventData:                map[string]any{"reason": message},
	}
}

func failedRunTerminalTransition(task types.Task, run types.TaskRun, requestID, status, message string, trace *profiler.Trace, now time.Time) terminalRunTransition {
	return terminalRunTransition{
		Task:                       task,
		Run:                        run,
		Status:                     status,
		Message:                    message,
		RequestID:                  requestID,
		TraceID:                    trace.TraceID,
		Trace:                      trace,
		Now:                        now,
		SkipIfStoredTerminalStatus: true,
	}
}

func executionResultTerminalTransition(input executionResultTerminalTransitionInput) terminalRunTransition {
	run := input.Run
	run.Provider = firstNonEmpty(input.Execution.Provider, run.Provider)
	run.ProviderKind = firstNonEmpty(input.Execution.ProviderKind, run.ProviderKind)
	run.Model = firstNonEmpty(input.Execution.Model, run.Model)
	run.Status = firstNonEmpty(input.Execution.Status, "completed")
	run.StepCount = len(input.PersistedSteps)
	run.ArtifactCount = len(input.PersistedArtifacts)
	run.FinishedAt = input.FinishedAt
	run.LastError = input.Execution.LastError
	run.OtelStatusCode = firstNonEmpty(input.Execution.OtelStatusCode, "ok")
	run.OtelStatusMessage = input.Execution.OtelStatusMessage
	if input.Execution.CostMicrosUSD > 0 {
		// Agent loop accumulates per-turn LLM cost and surfaces the
		// total here. Other executors don't talk to the LLM and leave
		// CostMicrosUSD zero, so preserve any existing total.
		run.TotalCostMicrosUSD = input.Execution.CostMicrosUSD
	}

	return terminalRunTransition{
		Task:                   input.Task,
		Run:                    run,
		Status:                 run.Status,
		Message:                input.Execution.LastError,
		RequestID:              input.RequestID,
		TraceID:                input.Trace.TraceID,
		Trace:                  input.Trace,
		Now:                    input.FinishedAt,
		SuppressDuplicateEvent: true,
		UpdateRun: func(target *types.TaskRun) {
			target.Provider = run.Provider
			target.ProviderKind = run.ProviderKind
			target.Model = run.Model
			target.StepCount = run.StepCount
			target.ArtifactCount = run.ArtifactCount
			target.TotalCostMicrosUSD = run.TotalCostMicrosUSD
			target.OtelStatusCode = run.OtelStatusCode
			target.OtelStatusMessage = run.OtelStatusMessage
		},
		UpdateTask: func(target *types.Task) {
			target.RootTraceID = input.Trace.TraceID
		},
	}
}
