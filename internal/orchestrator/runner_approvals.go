package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
)

var (
	ErrApprovalNotFound        = errors.New("task approval not found")
	ErrApprovalRunNotFound     = errors.New("task run not found")
	ErrApprovalConflict        = errors.New("task approval is not actionable")
	ErrInvalidApprovalDecision = errors.New("approval decision must be approve or reject")
)

type approvalConflictError struct {
	message string
}

func (e approvalConflictError) Error() string {
	return e.message
}

func (e approvalConflictError) Is(target error) bool {
	return target == ErrApprovalConflict
}

type ResolveApprovalRequest struct {
	Task        types.Task
	ApprovalID  string
	Decision    string
	Note        string
	ResolvedBy  string
	RequestID   string
	IDGenerator func(prefix string) string
}

type ResolveApprovalResult struct {
	Approval types.TaskApproval
	Task     types.Task
	Run      types.TaskRun
	TraceID  string
	SpanID   string
}

func (r *Runner) ResumeTaskAfterApproval(ctx context.Context, task types.Task, approval types.TaskApproval, idgen func(prefix string) string) (*StartTaskResult, error) {
	if r.store == nil {
		return nil, fmt.Errorf("task store is not configured")
	}
	if idgen == nil {
		return nil, fmt.Errorf("resource id generator is required")
	}
	run, found, err := r.store.GetRun(ctx, task.ID, approval.RunID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("task run %q not found", approval.RunID)
	}
	if run.Status != "awaiting_approval" {
		return nil, fmt.Errorf("task run %q is not awaiting approval", run.ID)
	}

	requestID := strings.TrimSpace(telemetry.RequestIDFromContext(ctx))
	if requestID == "" {
		requestID = idgen("request")
	}

	trace := r.tracer.Start(requestID)
	defer trace.Finalize()

	ctx = telemetry.WithTraceIDs(ctx, trace.TraceID, trace.RootSpanID())
	now := time.Now().UTC()
	approvalWaitMS := int64(0)
	if !approval.CreatedAt.IsZero() {
		resolvedAt := approval.ResolvedAt
		if resolvedAt.IsZero() {
			resolvedAt = time.Now().UTC()
		}
		approvalWaitMS = resolvedAt.Sub(approval.CreatedAt).Milliseconds()
	}
	r.recordApprovalResolved(ctx, trace, task.ID, run.ID, approval, "approved", approvalWaitMS)

	run.Status = "queued"
	run.RequestID = requestID
	run.TraceID = trace.TraceID
	run.RootSpanID = trace.RootSpanID()
	run.LastError = ""
	run.FinishedAt = time.Time{}
	if _, err := r.store.UpdateRun(ctx, run); err != nil {
		return nil, err
	}

	task.Status = "queued"
	task.LatestRunID = run.ID
	task.UpdatedAt = now
	task.FinishedAt = time.Time{}
	task.LastError = ""
	task.LatestTraceID = trace.TraceID
	task.LatestRequestID = requestID
	if _, err := r.store.UpdateTask(ctx, task); err != nil {
		return nil, err
	}

	_, _ = r.emitRunEvent(ctx, task.ID, run.ID, "approval.resolved", requestID, trace.TraceID, approvalResolvedEventData(approval))
	_, _ = r.emitRunEvent(ctx, task.ID, run.ID, "run.queued", requestID, trace.TraceID, map[string]any{"resume": true})

	if err := r.enqueueRun(task.ID, run.ID); err != nil {
		return nil, err
	}

	return &StartTaskResult{
		Task:    task,
		Run:     run,
		TraceID: trace.TraceID,
		SpanID:  trace.RootSpanID(),
	}, nil
}

func (r *Runner) ResolveTaskApproval(ctx context.Context, req ResolveApprovalRequest) (*ResolveApprovalResult, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("task store is not configured")
	}
	if req.Task.ID == "" {
		return nil, fmt.Errorf("task id is required")
	}
	approvalID := strings.TrimSpace(req.ApprovalID)
	if approvalID == "" {
		return nil, fmt.Errorf("approval id is required")
	}
	decision, err := normalizeApprovalDecision(req.Decision)
	if err != nil {
		return nil, err
	}
	if req.IDGenerator == nil {
		return nil, fmt.Errorf("resource id generator is required")
	}

	approval, found, err := r.store.GetApproval(ctx, req.Task.ID, approvalID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrApprovalNotFound
	}
	if approval.Status != "pending" {
		return nil, approvalConflictError{message: fmt.Sprintf("task approval is not pending (status %s)", approval.Status)}
	}

	run, found, err := r.store.GetRun(ctx, req.Task.ID, approval.RunID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrApprovalRunNotFound
	}
	if run.Status != "awaiting_approval" {
		return nil, approvalConflictError{message: fmt.Sprintf("task approval is no longer actionable because run is %s", run.Status)}
	}

	now := time.Now().UTC()
	approval.Status = decision
	approval.ResolutionNote = strings.TrimSpace(req.Note)
	approval.ResolvedBy = strings.TrimSpace(req.ResolvedBy)
	if approval.ResolvedBy == "" {
		approval.ResolvedBy = "operator"
	}
	approval.ResolvedAt = now

	updatedApproval, updated, err := r.store.UpdatePendingApprovalForAwaitingRun(ctx, approval)
	if err != nil {
		return nil, err
	}
	if !updated {
		return nil, approvalConflictError{message: "task approval is not pending or run is no longer awaiting approval"}
	}

	requestID := firstNonEmpty(strings.TrimSpace(req.RequestID), telemetry.RequestIDFromContext(ctx), updatedApproval.RequestID, run.RequestID)
	if requestID == "" && req.IDGenerator != nil {
		requestID = req.IDGenerator("request")
	}
	ctx = telemetry.WithRequestID(ctx, requestID)

	var started *StartTaskResult
	switch decision {
	case "approved":
		started, err = r.ResumeTaskAfterApproval(ctx, req.Task, updatedApproval, req.IDGenerator)
	case "rejected":
		started, err = r.RejectTaskAfterApproval(ctx, req.Task, updatedApproval, req.IDGenerator)
	default:
		err = ErrInvalidApprovalDecision
	}
	if err != nil {
		return nil, err
	}

	return &ResolveApprovalResult{
		Approval: updatedApproval,
		Task:     started.Task,
		Run:      started.Run,
		TraceID:  started.TraceID,
		SpanID:   started.SpanID,
	}, nil
}

func normalizeApprovalDecision(decision string) (string, error) {
	switch strings.TrimSpace(strings.ToLower(decision)) {
	case "approve", "approved":
		return "approved", nil
	case "reject", "rejected":
		return "rejected", nil
	case "deny", "denied":
		return "rejected", nil
	default:
		return "", ErrInvalidApprovalDecision
	}
}

func approvalResolvedEventData(approval types.TaskApproval) map[string]any {
	return map[string]any{
		"approval_id": approval.ID,
		"decision":    approval.Status,
		"by":          approval.ResolvedBy,
		"comment":     approval.ResolutionNote,
		"scope":       "once",
		"kind":        approval.Kind,
		"status":      approval.Status,
	}
}

func (r *Runner) RejectTaskAfterApproval(ctx context.Context, task types.Task, approval types.TaskApproval, idgen func(prefix string) string) (*StartTaskResult, error) {
	if r.store == nil {
		return nil, fmt.Errorf("task store is not configured")
	}
	if idgen == nil {
		return nil, fmt.Errorf("resource id generator is required")
	}
	run, found, err := r.store.GetRun(ctx, task.ID, approval.RunID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("task run %q not found", approval.RunID)
	}
	if run.Status != "awaiting_approval" {
		return nil, fmt.Errorf("task run %q is not awaiting approval", run.ID)
	}

	requestID := strings.TrimSpace(telemetry.RequestIDFromContext(ctx))
	if requestID == "" {
		requestID = firstNonEmpty(approval.RequestID, run.RequestID)
	}
	if requestID == "" {
		requestID = idgen("request")
	}

	trace := r.tracer.Start(requestID)
	defer trace.Finalize()

	ctx = telemetry.WithTraceIDs(ctx, trace.TraceID, trace.RootSpanID())
	rejectWaitMS := int64(0)
	if !approval.CreatedAt.IsZero() {
		resolvedAt := approval.ResolvedAt
		if resolvedAt.IsZero() {
			resolvedAt = time.Now().UTC()
		}
		rejectWaitMS = resolvedAt.Sub(approval.CreatedAt).Milliseconds()
	}
	r.recordApprovalResolved(ctx, trace, task.ID, run.ID, approval, "rejected", rejectWaitMS)

	run, err = r.cancelRunWithMessage(ctx, task, run, "approval rejected", requestID, trace.TraceID)
	if err != nil {
		return nil, err
	}
	task, _, err = r.store.GetTask(ctx, task.ID)
	if err != nil {
		return nil, err
	}
	_, _ = r.emitRunEvent(ctx, task.ID, run.ID, "approval.resolved", requestID, trace.TraceID, approvalResolvedEventData(approval))
	return &StartTaskResult{
		Task:    task,
		Run:     run,
		TraceID: trace.TraceID,
		SpanID:  trace.RootSpanID(),
	}, nil
}

func (r *Runner) cancelPendingApprovalsForRun(ctx context.Context, trace *profiler.Trace, task types.Task, run types.TaskRun, message, requestID, traceID string, now time.Time) {
	approvals, err := r.store.ListApprovals(ctx, task.ID)
	if err != nil {
		return
	}
	for _, approval := range approvals {
		if approval.RunID != run.ID || approval.Status != "pending" {
			continue
		}
		approval.Status = "cancelled"
		approval.ResolutionNote = message
		approval.ResolvedBy = "system"
		approval.ResolvedAt = now
		updated, ok, err := r.store.UpdatePendingApproval(ctx, approval)
		if err != nil {
			continue
		}
		if !ok {
			continue
		}
		waitMS := int64(0)
		if !updated.CreatedAt.IsZero() {
			waitMS = updated.ResolvedAt.Sub(updated.CreatedAt).Milliseconds()
		}
		r.recordApprovalResolved(ctx, trace, task.ID, run.ID, updated, "cancelled", waitMS)
		_, _ = r.emitRunEvent(ctx, task.ID, run.ID, "approval.resolved", requestID, traceID, approvalResolvedEventData(updated))
	}
}

func (r *Runner) recordApprovalResolved(ctx context.Context, trace *profiler.Trace, taskID, runID string, approval types.TaskApproval, decision string, waitMS int64) {
	attrs := map[string]any{
		telemetry.AttrHecatePhase:          "approval",
		telemetry.AttrHecateResult:         telemetry.ResultSuccess,
		telemetry.AttrHecateTaskID:         taskID,
		telemetry.AttrHecateRunID:          runID,
		telemetry.AttrHecateApprovalID:     approval.ID,
		telemetry.AttrHecateApprovalKind:   approval.Kind,
		telemetry.AttrHecateApprovalStatus: approval.Status,
	}
	if waitMS > 0 {
		attrs[telemetry.AttrHecateApprovalWaitMS] = waitMS
	}
	if trace != nil {
		trace.Record(telemetry.EventOrchestratorApprovalResolved, attrs)
	}
	if r.metrics != nil {
		r.metrics.RecordApproval(ctx, telemetry.ApprovalMetricsRecord{
			TaskID:       taskID,
			RunID:        runID,
			ApprovalKind: approval.Kind,
			Decision:     decision,
			WaitMS:       waitMS,
		})
	}
}

func (r *Runner) approvalRequiredForTask(task types.Task) bool {
	_, reason := r.approvalSpecForTask(task)
	return reason != ""
}

func (r *Runner) approvalSpecForTask(task types.Task) (kind string, reason string) {
	if task.ExecutionKind == "shell" && strings.TrimSpace(task.ShellCommand) != "" {
		if r.hasPolicy("shell_exec") || r.hasPolicy("all_tools") {
			return "shell_command", "Shell execution requires approval before execution."
		}
	}
	if task.ExecutionKind == "git" && strings.TrimSpace(task.GitCommand) != "" {
		if r.hasPolicy("git_exec") || r.hasPolicy("all_tools") {
			return "git_exec", "Git execution requires approval before execution."
		}
	}
	if task.ExecutionKind == "file" && strings.TrimSpace(task.FilePath) != "" {
		if r.hasPolicy("file_write") || r.hasPolicy("all_tools") {
			return "file_write", "File writes require approval before execution."
		}
	}
	if task.SandboxNetwork {
		if r.hasPolicy("network_egress") || r.hasPolicy("all_tools") {
			return "network_egress", "Network-enabled tasks require approval before execution."
		}
	}
	return "", ""
}

func (r *Runner) createApprovalForTask(ctx context.Context, trace *profiler.Trace, task types.Task, run types.TaskRun, requestID string, createdAt time.Time, idgen func(prefix string) string) (types.TaskApproval, error) {
	kind, reason := r.approvalSpecForTask(task)
	approval := types.TaskApproval{
		ID:          idgen("approval"),
		TaskID:      task.ID,
		RunID:       run.ID,
		Kind:        kind,
		Status:      "pending",
		Reason:      reason,
		RequestedBy: "operator",
		CreatedAt:   createdAt,
		RequestID:   requestID,
		TraceID:     trace.TraceID,
	}
	trace.Record(telemetry.EventOrchestratorApprovalRequested, map[string]any{
		telemetry.AttrHecatePhase:        "approval",
		telemetry.AttrHecateResult:       telemetry.ResultSuccess,
		telemetry.AttrHecateTaskID:       task.ID,
		telemetry.AttrHecateRunID:        run.ID,
		telemetry.AttrHecateApprovalID:   approval.ID,
		telemetry.AttrHecateApprovalKind: approval.Kind,
		telemetry.AttrHecateShellCommand: task.ShellCommand,
	})
	approval.SpanID = spanIDByName(trace, "orchestrator.approval")
	approval, err := r.store.CreateApproval(ctx, approval)
	if err != nil {
		trace.Record(telemetry.EventOrchestratorApprovalFailed, map[string]any{
			telemetry.AttrHecatePhase:      "approval",
			telemetry.AttrHecateResult:     telemetry.ResultError,
			telemetry.AttrHecateErrorKind:  "approval_create_failed",
			telemetry.AttrErrorType:        "approval_create_failed",
			telemetry.AttrErrorMessage:     err.Error(),
			telemetry.AttrHecateTaskID:     task.ID,
			telemetry.AttrHecateRunID:      run.ID,
			telemetry.AttrHecateApprovalID: approval.ID,
		})
		return types.TaskApproval{}, err
	}
	_, _ = r.emitRunEvent(ctx, task.ID, run.ID, "approval.requested", requestID, trace.TraceID, approvalRequestedEventData(approval))
	return approval, nil
}
