package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/hecatehq/hecate/internal/orchestrator"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
)

func (h *Handler) HandleTaskApprovals(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if h.taskStore == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "task store is not configured")
		return
	}
	task, ok := h.loadAuthorizedTask(ctx, w, r)
	if !ok {
		return
	}

	approvals, err := h.taskStore.ListApprovals(ctx, task.ID)
	if err != nil {
		telemetry.Error(h.logger, ctx, "gateway.tasks.approvals.list.failed",
			slog.String("event.name", "gateway.tasks.approvals.list.failed"),
			slog.Any("error", err),
		)
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}

	items := make([]TaskApprovalItem, 0, len(approvals))
	for _, approval := range approvals {
		items = append(items, renderTaskApproval(approval))
	}
	WriteJSON(w, http.StatusOK, TaskApprovalsResponse{
		Object: "task_approvals",
		Data:   items,
	})
}

func (h *Handler) HandleTaskApproval(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if h.taskStore == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "task store is not configured")
		return
	}
	task, ok := h.loadAuthorizedTask(ctx, w, r)
	if !ok {
		return
	}
	approvalID := strings.TrimSpace(r.PathValue("approval_id"))
	if approvalID == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "approval id is required")
		return
	}

	approval, found, err := h.taskStore.GetApproval(ctx, task.ID, approvalID)
	if err != nil {
		telemetry.Error(h.logger, ctx, "gateway.tasks.approvals.get.failed",
			slog.String("event.name", "gateway.tasks.approvals.get.failed"),
			slog.Any("error", err),
		)
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if !found {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "task approval not found")
		return
	}

	WriteJSON(w, http.StatusOK, TaskApprovalResponse{
		Object: "task_approval",
		Data:   renderTaskApproval(approval),
	})
}

func (h *Handler) HandleResolveTaskApproval(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if h.taskStore == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "task store is not configured")
		return
	}
	if h.taskRunner == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "task runner is not configured")
		return
	}
	task, ok := h.loadAuthorizedTask(ctx, w, r)
	if !ok {
		return
	}
	approvalID := strings.TrimSpace(r.PathValue("approval_id"))
	if approvalID == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "approval id is required")
		return
	}

	var req ResolveTaskApprovalRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	result, err := h.taskRunner.ResolveTaskApproval(ctx, orchestrator.ResolveApprovalRequest{
		Task:        task,
		ApprovalID:  approvalID,
		Decision:    req.Decision,
		Note:        req.Note,
		ResolvedBy:  "operator",
		RequestID:   RequestIDFromContext(ctx),
		IDGenerator: newOpaqueTaskResourceID,
	})
	if err != nil {
		h.writeResolveTaskApprovalError(w, r, err)
		return
	}
	if result.TraceID != "" {
		w.Header().Set("X-Trace-Id", result.TraceID)
	}
	if result.SpanID != "" {
		w.Header().Set("X-Span-Id", result.SpanID)
	}

	WriteJSON(w, http.StatusOK, TaskApprovalResponse{
		Object: "task_approval",
		Data:   renderTaskApproval(result.Approval),
	})
}

func (h *Handler) writeResolveTaskApprovalError(w http.ResponseWriter, r *http.Request, err error) {
	ctx := r.Context()
	switch {
	case errors.Is(err, orchestrator.ErrInvalidApprovalDecision):
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "decision must be approve or reject")
	case errors.Is(err, orchestrator.ErrApprovalNotFound):
		WriteError(w, http.StatusNotFound, errCodeNotFound, "task approval not found")
	case errors.Is(err, orchestrator.ErrApprovalRunNotFound):
		WriteError(w, http.StatusNotFound, errCodeNotFound, "task run not found")
	case errors.Is(err, orchestrator.ErrApprovalConflict):
		WriteError(w, http.StatusConflict, errCodeInvalidRequest, err.Error())
	default:
		telemetry.Error(h.logger, ctx, "gateway.tasks.approvals.resolve.failed",
			slog.String("event.name", "gateway.tasks.approvals.resolve.failed"),
			slog.Any("error", err),
		)
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
	}
}

func (h *Handler) HandleCancelTaskRun(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if h.taskStore == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "task store is not configured")
		return
	}
	if h.taskRunner == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "task runner is not configured")
		return
	}
	task, ok := h.loadAuthorizedTask(ctx, w, r)
	if !ok {
		return
	}
	run, ok := h.loadAuthorizedTaskRun(ctx, w, r, task)
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	// Body is optional — plain POST with no body (or an empty body) is fine.
	if r.ContentLength > 0 {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}

	run, err := h.taskRunner.CancelRun(ctx, task, run.ID, body.Reason)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, TaskRunResponse{
		Object: "task_run",
		Data:   renderTaskRun(run),
	})
}

func (h *Handler) HandleRetryTaskRun(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	task, ok := h.loadAuthorizedTask(ctx, w, r)
	if !ok {
		return
	}
	run, ok := h.loadAuthorizedTaskRun(ctx, w, r, task)
	if !ok {
		return
	}
	if !types.IsTerminalTaskRunStatus(run.Status) {
		WriteError(w, http.StatusConflict, errCodeInvalidRequest, "run is not retryable until it reaches a terminal state")
		return
	}
	if active, err := taskHasOtherActiveRun(ctx, h.taskStore, task, run.ID); err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	} else if active {
		WriteError(w, http.StatusConflict, errCodeInvalidRequest, "task already has another active run")
		return
	}
	result, err := h.taskRunner.StartTask(ctx, task, newOpaqueTaskResourceID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, TaskRunResponse{Object: "task_run", Data: renderTaskRun(result.Run)})
}

func (h *Handler) HandleResumeTaskRun(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	task, ok := h.loadAuthorizedTask(ctx, w, r)
	if !ok {
		return
	}
	run, ok := h.loadAuthorizedTaskRun(ctx, w, r, task)
	if !ok {
		return
	}
	var req ResumeTaskRunRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if run.Status != "failed" && run.Status != "cancelled" {
		WriteError(w, http.StatusConflict, errCodeInvalidRequest, "run is not resumable")
		return
	}
	if active, err := taskHasOtherActiveRun(ctx, h.taskStore, task, run.ID); err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	} else if active {
		WriteError(w, http.StatusConflict, errCodeInvalidRequest, "task already has another active run")
		return
	}
	// Optional ceiling raise — used by the "Raise ceiling and
	// resume" UI affordance after a cost_ceiling_exceeded failure.
	// We persist the new ceiling on the task BEFORE queueing the
	// resumed run so the agent loop's per-task ceiling check
	// (priorCost + costSpent vs Task.BudgetMicrosUSD) sees the
	// raised value on its first turn. The ceiling can only go up
	// here — a request to lower it is rejected, since the obvious
	// failure would be to silently strand a run below its
	// already-spent prior cost.
	if req.BudgetMicrosUSD > 0 {
		if req.BudgetMicrosUSD < task.BudgetMicrosUSD {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "budget_micros_usd cannot be lower than the current task ceiling")
			return
		}
		task.BudgetMicrosUSD = req.BudgetMicrosUSD
		if updated, err := h.taskStore.UpdateTask(ctx, task); err != nil {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
			return
		} else {
			task = updated
		}
	}
	result, err := h.taskRunner.ResumeTask(ctx, task, run, strings.TrimSpace(req.Reason), newOpaqueTaskResourceID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, TaskRunResponse{Object: "task_run", Data: renderTaskRun(result.Run)})
}

func (h *Handler) HandleContinueTaskRun(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	task, ok := h.loadAuthorizedTask(ctx, w, r)
	if !ok {
		return
	}
	run, ok := h.loadAuthorizedTaskRun(ctx, w, r, task)
	if !ok {
		return
	}
	var req ContinueTaskRunRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if active, err := taskHasOtherActiveRun(ctx, h.taskStore, task, run.ID); err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	} else if active {
		WriteError(w, http.StatusConflict, errCodeInvalidRequest, "task already has another active run")
		return
	}
	result, err := h.taskRunner.ContinueAgentTask(ctx, task, run, req.Prompt, newOpaqueTaskResourceID)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "not continuable") {
			WriteError(w, http.StatusConflict, errCodeInvalidRequest, msg)
			return
		}
		if strings.Contains(msg, "not an agent_loop") ||
			strings.Contains(msg, "prompt is required") ||
			strings.Contains(msg, "no agent_conversation") ||
			strings.Contains(msg, "malformed agent_conversation") {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, msg)
			return
		}
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, msg)
		return
	}
	WriteJSON(w, http.StatusOK, TaskRunResponse{Object: "task_run", Data: renderTaskRun(result.Run)})
}

// HandleRetryTaskRunFromTurn re-runs an agent_loop run from turn N,
// preserving the source conversation up to (but not including) that
// turn's assistant message. The new run is a sibling of the source
// (not a child) — it gets its own run number and step indices. Only
// terminal runs are eligible; the source must have produced an
// agent_conversation artifact, and the requested turn must lie within
// the source's completed assistant-turn count.
func (h *Handler) HandleRetryTaskRunFromTurn(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	task, ok := h.loadAuthorizedTask(ctx, w, r)
	if !ok {
		return
	}
	run, ok := h.loadAuthorizedTaskRun(ctx, w, r, task)
	if !ok {
		return
	}
	var req RetryFromTurnRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !types.IsTerminalTaskRunStatus(run.Status) {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "run is not retryable from a turn (must be terminal)")
		return
	}
	if req.Turn < 1 {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "turn must be >= 1")
		return
	}
	if active, err := taskHasOtherActiveRun(ctx, h.taskStore, task, run.ID); err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	} else if active {
		WriteError(w, http.StatusConflict, errCodeInvalidRequest, "task already has another active run")
		return
	}
	result, err := h.taskRunner.RetryTaskFromTurn(ctx, task, run, req.Turn, strings.TrimSpace(req.Reason), newOpaqueTaskResourceID)
	if err != nil {
		// Validation failures (missing conversation, turn out of
		// range, malformed artifact) are user errors — return 400 so
		// the UI can render an actionable message rather than a 500.
		msg := err.Error()
		if strings.Contains(msg, "no agent_conversation") ||
			strings.Contains(msg, "turn") ||
			strings.Contains(msg, "malformed agent_conversation") {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, msg)
			return
		}
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, msg)
		return
	}
	WriteJSON(w, http.StatusOK, TaskRunResponse{Object: "task_run", Data: renderTaskRun(result.Run)})
}
