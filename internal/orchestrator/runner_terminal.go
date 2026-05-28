package orchestrator

import (
	"context"
	"time"

	"github.com/hecatehq/hecate/internal/profiler"
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

	CancelActiveSteps          bool
	CancelStreamingArtifacts   bool
	CancelPendingApprovals     bool
	EmitTaskUpdated            bool
	SuppressDuplicateEvent     bool
	SkipIfStoredTerminalStatus bool

	UpdateRun  func(*types.TaskRun)
	UpdateTask func(*types.Task)

	EventData map[string]any
}

type terminalRunTransitionResult struct {
	Task    types.Task
	Run     types.TaskRun
	Skipped bool
}

func (r *Runner) applyTerminalRunTransition(ctx context.Context, tr terminalRunTransition) (terminalRunTransitionResult, error) {
	if tr.Now.IsZero() {
		tr.Now = time.Now().UTC()
	}
	emitTerminalEvent := true
	if currentRun, found, err := r.store.GetRun(ctx, tr.Task.ID, tr.Run.ID); err == nil && found {
		if types.IsTerminalTaskRunStatus(currentRun.Status) && currentRun.Status == tr.Status {
			if tr.SkipIfStoredTerminalStatus {
				return terminalRunTransitionResult{Task: tr.Task, Run: currentRun, Skipped: true}, nil
			}
			if tr.SuppressDuplicateEvent {
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
	if emitTerminalEvent {
		data := tr.EventData
		if data == nil {
			data = map[string]any{"error": tr.Message, "status": tr.Status}
		}
		terminalEvent = &taskstate.RunEventSpec{
			EventType: terminalRunEventType(run.Status),
			Data:      data,
			RequestID: tr.RequestID,
			TraceID:   tr.TraceID,
			CreatedAt: tr.Now,
		}
	}
	var taskUpdatedEvent *taskstate.RunEventSpec
	if tr.EmitTaskUpdated {
		taskUpdatedEvent = &taskstate.RunEventSpec{
			EventType: "task.updated",
			RequestID: tr.RequestID,
			TraceID:   tr.TraceID,
			CreatedAt: tr.Now,
		}
	}
	result, err := r.store.ApplyRunTerminalTransition(ctx, taskstate.TerminalRunTransition{
		Task:                          task,
		Run:                           run,
		FinishedAt:                    tr.Now,
		CancelActiveSteps:             tr.CancelActiveSteps,
		ActiveStepError:               tr.Message,
		ActiveStepErrorKind:           "run_cancelled",
		ActiveStepResult:              telemetry.ResultError,
		CancelStreamingArtifacts:      tr.CancelStreamingArtifacts,
		CancelPendingApprovals:        tr.CancelPendingApprovals,
		PendingApprovalStatus:         "cancelled",
		PendingApprovalResolvedBy:     "system",
		PendingApprovalResolutionNote: tr.Message,
		ApprovalResolvedEventType:     "approval.resolved",
		TerminalEvent:                 terminalEvent,
		TaskUpdatedEvent:              taskUpdatedEvent,
	})
	if err != nil {
		return terminalRunTransitionResult{}, err
	}
	for _, approval := range result.CancelledApprovals {
		waitMS := int64(0)
		if !approval.CreatedAt.IsZero() {
			waitMS = approval.ResolvedAt.Sub(approval.CreatedAt).Milliseconds()
		}
		r.recordApprovalResolved(ctx, tr.Trace, result.Task.ID, result.Run.ID, approval, "cancelled", waitMS)
	}

	return terminalRunTransitionResult{
		Task: result.Task,
		Run:  result.Run,
	}, nil
}
