package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/runtimeevents"
	"github.com/hecatehq/hecate/internal/taskstate"
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
	started, _, err := r.resumeTaskAfterApproval(ctx, task, approval, idgen)
	return started, err
}

func (r *Runner) resumeTaskAfterApproval(ctx context.Context, task types.Task, approval types.TaskApproval, idgen func(prefix string) string) (*StartTaskResult, types.TaskApproval, error) {
	if r.store == nil {
		return nil, types.TaskApproval{}, fmt.Errorf("task store is not configured")
	}
	if idgen == nil {
		return nil, types.TaskApproval{}, fmt.Errorf("resource id generator is required")
	}
	if approval.Status != "approved" {
		return nil, types.TaskApproval{}, fmt.Errorf("approval status must be approved")
	}
	run, found, err := r.store.GetRun(ctx, task.ID, approval.RunID)
	if err != nil {
		return nil, types.TaskApproval{}, err
	}
	if !found {
		return nil, types.TaskApproval{}, fmt.Errorf("task run %q not found", approval.RunID)
	}
	if run.Status != "awaiting_approval" {
		return nil, types.TaskApproval{}, approvalConflictError{message: fmt.Sprintf("task approval is no longer actionable because run is %s", run.Status)}
	}

	requestID := strings.TrimSpace(telemetry.RequestIDFromContext(ctx))
	if requestID == "" {
		requestID = idgen("request")
	}

	trace := r.tracer.Start(requestID)
	defer trace.Finalize()

	ctx = telemetry.WithTraceIDs(ctx, trace.TraceID, trace.RootSpanID())
	now := time.Now().UTC()
	if approval.ResolvedAt.IsZero() {
		approval.ResolvedAt = now
	}
	if strings.TrimSpace(approval.ResolvedBy) == "" {
		approval.ResolvedBy = "operator"
	}

	run.Status = "queued"
	run.RequestID = requestID
	run.TraceID = trace.TraceID
	run.RootSpanID = trace.RootSpanID()
	run.LastError = ""
	run.FinishedAt = time.Time{}

	task.Status = "queued"
	task.LatestRunID = run.ID
	task.UpdatedAt = now
	task.FinishedAt = time.Time{}
	task.LastError = ""
	task.LatestTraceID = trace.TraceID
	task.LatestRequestID = requestID
	transition, err := r.store.ApplyRunStateTransition(ctx, taskstate.RunStateTransition{
		Task:                task,
		Run:                 run,
		ExpectedRunStatuses: []string{"awaiting_approval"},
		ApprovalResolution:  pendingApprovalResolution(approval, requestID, trace.TraceID),
	})
	if err != nil {
		return nil, types.TaskApproval{}, err
	}
	if !transition.Applied {
		return nil, transition.Approval, approvalConflictError{message: fmt.Sprintf("task approval is no longer actionable because run is %s", transition.Run.Status)}
	}
	task = transition.Task
	run = transition.Run
	approval = transition.Approval
	approvalWaitMS := approvalWaitMilliseconds(approval.CreatedAt, approval.ResolvedAt)
	r.recordApprovalResolved(ctx, trace, task.ID, run.ID, approval, "approved", approvalWaitMS)
	if err := r.enqueueRun(task.ID, run.ID); err != nil {
		return nil, approval, err
	}

	return &StartTaskResult{
		Task:    task,
		Run:     run,
		TraceID: trace.TraceID,
		SpanID:  trace.RootSpanID(),
	}, approval, nil
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
	originLease, err := r.beginOriginRunMutation(ctx, req.Task)
	if err != nil {
		return nil, err
	}
	if originLease != nil {
		defer originLease.Release()
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

	requestID := firstNonEmpty(strings.TrimSpace(req.RequestID), telemetry.RequestIDFromContext(ctx), approval.RequestID, run.RequestID)
	if requestID == "" && req.IDGenerator != nil {
		requestID = req.IDGenerator("request")
	}
	ctx = telemetry.WithRequestID(ctx, requestID)

	var started *StartTaskResult
	var resolvedApproval types.TaskApproval
	switch decision {
	case "approved":
		started, resolvedApproval, err = r.resumeTaskAfterApproval(ctx, req.Task, approval, req.IDGenerator)
	case "rejected":
		started, resolvedApproval, err = r.rejectTaskAfterApproval(ctx, req.Task, approval, req.IDGenerator)
	default:
		err = ErrInvalidApprovalDecision
	}
	if err != nil {
		return nil, err
	}
	if started == nil {
		return nil, approvalConflictError{message: "task approval resolution did not produce a runnable state"}
	}

	return &ResolveApprovalResult{
		Approval: resolvedApproval,
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

func (r *Runner) RejectTaskAfterApproval(ctx context.Context, task types.Task, approval types.TaskApproval, idgen func(prefix string) string) (*StartTaskResult, error) {
	started, _, err := r.rejectTaskAfterApproval(ctx, task, approval, idgen)
	return started, err
}

func (r *Runner) rejectTaskAfterApproval(ctx context.Context, task types.Task, approval types.TaskApproval, idgen func(prefix string) string) (*StartTaskResult, types.TaskApproval, error) {
	if r.store == nil {
		return nil, types.TaskApproval{}, fmt.Errorf("task store is not configured")
	}
	if idgen == nil {
		return nil, types.TaskApproval{}, fmt.Errorf("resource id generator is required")
	}
	if approval.Status != "rejected" {
		return nil, types.TaskApproval{}, fmt.Errorf("approval status must be rejected")
	}
	run, found, err := r.store.GetRun(ctx, task.ID, approval.RunID)
	if err != nil {
		return nil, types.TaskApproval{}, err
	}
	if !found {
		return nil, types.TaskApproval{}, fmt.Errorf("task run %q not found", approval.RunID)
	}
	if run.Status != "awaiting_approval" {
		return nil, types.TaskApproval{}, approvalConflictError{message: fmt.Sprintf("task approval is no longer actionable because run is %s", run.Status)}
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
	now := time.Now().UTC()
	if approval.ResolvedAt.IsZero() {
		approval.ResolvedAt = now
	}
	if strings.TrimSpace(approval.ResolvedBy) == "" {
		approval.ResolvedBy = "operator"
	}
	terminal := cancelRunTerminalTransition(task, run, "approval rejected", requestID, trace.TraceID, trace, now)
	terminal.ApprovalResolution = pendingApprovalResolution(approval, requestID, trace.TraceID)
	transition, err := r.applyTerminalRunTransition(ctx, terminal)
	if err != nil {
		return nil, types.TaskApproval{}, err
	}
	if transition.Skipped {
		return nil, transition.Approval, approvalConflictError{message: fmt.Sprintf("task approval is no longer actionable because run is %s", transition.Run.Status)}
	}
	approval = transition.Approval
	rejectWaitMS := approvalWaitMilliseconds(approval.CreatedAt, approval.ResolvedAt)
	r.recordApprovalResolved(ctx, trace, transition.Task.ID, transition.Run.ID, approval, "rejected", rejectWaitMS)

	// The resolution and cancellation winner are durable before the executor
	// is signalled. A losing resolver never closes or drains executor state.
	r.cancelInFlightJob(run.ID)
	if closer, ok := r.agent.(agentTerminalRunCloser); ok {
		closer.CloseTerminalsForRun(ctx, run.ID)
	}
	if err := r.cancelAndWaitForInFlightJob(ctx, run.ID); err != nil {
		return nil, approval, fmt.Errorf("wait for rejected run %q executor exit: %w", run.ID, err)
	}
	run, err = r.cleanupCancelledRunAfterDrain(ctx, transition.Task, transition.Run, trace)
	if err != nil {
		return nil, approval, err
	}
	task, _, err = r.store.GetTask(ctx, task.ID)
	if err != nil {
		return nil, approval, err
	}
	return &StartTaskResult{
		Task:    task,
		Run:     run,
		TraceID: trace.TraceID,
		SpanID:  trace.RootSpanID(),
	}, approval, nil
}

func pendingApprovalResolution(approval types.TaskApproval, requestID, traceID string) *taskstate.PendingApprovalResolution {
	return &taskstate.PendingApprovalResolution{
		ApprovalID:     approval.ID,
		Status:         approval.Status,
		ResolvedBy:     approval.ResolvedBy,
		ResolutionNote: approval.ResolutionNote,
		ResolvedAt:     approval.ResolvedAt,
		RequestID:      requestID,
		TraceID:        traceID,
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

func approvalWaitMilliseconds(createdAt, resolvedAt time.Time) int64 {
	if createdAt.IsZero() || resolvedAt.IsZero() || !resolvedAt.After(createdAt) {
		return 0
	}
	return resolvedAt.Sub(createdAt).Milliseconds()
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
	// A tools-disabled agent loop has no executable sandbox capability: it
	// sends no tool catalog, does not start MCP clients, and fail-closes any
	// unexpected tool call. SandboxNetwork can still be true on the immutable
	// preset snapshot, but there is no network-capable action to approve.
	if task.SandboxNetwork && !(task.ExecutionKind == "agent_loop" && agentPresetDisablesTools(task)) {
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
	_, _ = r.emitRunEvent(ctx, task.ID, run.ID, runtimeevents.EventApprovalRequested.String(), requestID, trace.TraceID, runtimeevents.ApprovalRequested(approval))
	return approval, nil
}
