package orchestrator

import (
	"context"
	"time"

	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/runtimeevents"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
)

type terminalRunTransition struct {
	Task      types.Task
	Run       types.TaskRun
	Status    string
	Message   string
	RequestID string
	TraceID   string
	Trace     *profiler.Trace
	Now       time.Time

	CancelActiveSteps              bool
	CancelStreamingArtifacts       bool
	CancelPendingApprovals         bool
	EmitTaskUpdated                bool
	PreserveTaskProjection         bool
	SuppressDuplicateEvent         bool
	SkipIfStoredTerminalStatus     bool
	TrustedSupplementalRunMetadata *taskstate.TerminalRunSupplementalMetadata
	ApprovalResolution             *taskstate.PendingApprovalResolution

	UpdateRun  func(*types.TaskRun)
	UpdateTask func(*types.Task)

	EventData map[string]any
}

type terminalRunTransitionResult struct {
	Task     types.Task
	Run      types.TaskRun
	Approval types.TaskApproval
	Skipped  bool
}

func (r *Runner) applyTerminalRunTransition(ctx context.Context, tr terminalRunTransition) (terminalRunTransitionResult, error) {
	if tr.Now.IsZero() {
		tr.Now = time.Now().UTC()
	}
	emitTerminalEvent := true
	if currentRun, found, err := r.store.GetRun(ctx, tr.Task.ID, tr.Run.ID); err == nil && found {
		if types.IsTerminalTaskRunStatus(currentRun.Status) {
			if currentRun.Status != tr.Status {
				if tr.TrustedSupplementalRunMetadata == nil {
					currentTask := tr.Task
					if storedTask, taskFound, taskErr := r.store.GetTask(ctx, tr.Task.ID); taskErr == nil && taskFound {
						currentTask = storedTask
					}
					return terminalRunTransitionResult{Task: currentTask, Run: currentRun, Skipped: true}, nil
				}
				// Execution finalization still needs to account for the route and
				// cost it observed after another terminal status won. Let the store
				// atomically merge only that trusted metadata and suppress the
				// loser's terminal event.
				emitTerminalEvent = false
			}
			if currentRun.Status == tr.Status && tr.SkipIfStoredTerminalStatus {
				return terminalRunTransitionResult{Task: tr.Task, Run: currentRun, Skipped: true}, nil
			}
			if currentRun.Status == tr.Status && tr.SuppressDuplicateEvent {
				emitTerminalEvent = false
			}
		}
	}

	run := tr.Run
	run.Status = tr.Status
	run.LastError = tr.Message
	run.FinishedAt = tr.Now
	if tr.Status == "completed" {
		run.OtelStatusCode = firstNonEmpty(run.OtelStatusCode, "ok")
	} else {
		run.OtelStatusCode = "error"
		run.OtelStatusMessage = tr.Message
	}
	if tr.RequestID != "" {
		run.RequestID = tr.RequestID
	}
	if tr.TraceID != "" {
		run.TraceID = tr.TraceID
	}
	if tr.UpdateRun != nil {
		tr.UpdateRun(&run)
	}
	task := tr.Task
	task.Status = tr.Status
	task.LatestRunID = run.ID
	task.LastError = tr.Message
	if task.StartedAt.IsZero() {
		task.StartedAt = run.StartedAt
	}
	task.FinishedAt = tr.Now
	task.UpdatedAt = tr.Now
	if tr.RequestID != "" {
		task.LatestRequestID = tr.RequestID
	}
	if tr.TraceID != "" {
		task.LatestTraceID = tr.TraceID
	}
	if tr.UpdateTask != nil {
		tr.UpdateTask(&task)
	}

	var terminalEvent *taskstate.RunEventSpec
	if emitTerminalEvent && tr.ApprovalResolution == nil {
		data := terminalRunEventData(tr.EventData, run, tr.Status, tr.Message)
		terminalEvent = &taskstate.RunEventSpec{
			EventType: terminalRunEventType(run.Status),
			Data:      data,
			RequestID: tr.RequestID,
			TraceID:   tr.TraceID,
			CreatedAt: tr.Now,
		}
	}
	var taskUpdatedEvent *taskstate.RunEventSpec
	if tr.EmitTaskUpdated && tr.ApprovalResolution == nil {
		taskUpdatedEvent = &taskstate.RunEventSpec{
			EventType: runtimeevents.EventTaskUpdated.String(),
			RequestID: tr.RequestID,
			TraceID:   tr.TraceID,
			CreatedAt: tr.Now,
		}
	}
	result, err := r.store.ApplyRunTerminalTransition(ctx, taskstate.TerminalRunTransition{
		Task:                           task,
		Run:                            run,
		FinishedAt:                     tr.Now,
		ApprovalResolution:             tr.ApprovalResolution,
		TrustedSupplementalRunMetadata: tr.TrustedSupplementalRunMetadata,
		PreserveTaskProjection:         tr.PreserveTaskProjection,
		CancelActiveSteps:              tr.CancelActiveSteps,
		ActiveStepError:                tr.Message,
		ActiveStepErrorKind:            "run_cancelled",
		ActiveStepResult:               telemetry.ResultError,
		CancelStreamingArtifacts:       tr.CancelStreamingArtifacts,
		CancelPendingApprovals:         tr.CancelPendingApprovals,
		PendingApprovalStatus:          "cancelled",
		PendingApprovalResolvedBy:      "system",
		PendingApprovalResolutionNote:  tr.Message,
		ApprovalResolvedEventType:      runtimeevents.EventApprovalResolved.String(),
		TerminalEvent:                  terminalEvent,
		TaskUpdatedEvent:               taskUpdatedEvent,
	})
	if err != nil {
		return terminalRunTransitionResult{}, err
	}
	if !result.Applied {
		return terminalRunTransitionResult{Task: result.Task, Run: result.Run, Approval: result.Approval, Skipped: true}, nil
	}
	for _, approval := range result.CancelledApprovals {
		waitMS := approvalWaitMilliseconds(approval.CreatedAt, approval.ResolvedAt)
		r.recordApprovalResolved(ctx, tr.Trace, result.Task.ID, result.Run.ID, approval, "cancelled", waitMS)
	}

	return terminalRunTransitionResult{
		Task:     result.Task,
		Run:      result.Run,
		Approval: result.Approval,
	}, nil
}

func terminalRunEventData(data map[string]any, run types.TaskRun, status, message string) map[string]any {
	out := make(map[string]any, len(data)+3)
	for key, value := range data {
		out[key] = value
	}
	if _, ok := out["status"]; !ok {
		out["status"] = status
	}
	if _, ok := out["error"]; !ok && message != "" {
		out["error"] = message
	}
	// ModelCallCount counts completed provider round-trips. Keep an explicit
	// zero when the first model request fails before producing a response.
	out["model_call_count"] = run.ModelCallCount
	return out
}
