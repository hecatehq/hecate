package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/hecatehq/hecate/internal/orchestrator"
	"github.com/hecatehq/hecate/internal/telemetry"
)

func (h *Handler) HandleTaskApprovals(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	task, ok := h.loadAuthorizedTask(ctx, w, r)
	if !ok {
		return
	}

	approvals, err := h.taskApplication().ListTaskApprovals(ctx, task)
	if err != nil {
		if errors.Is(err, errTaskStoreNotConfigured) {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
			return
		}
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
	task, ok := h.loadAuthorizedTask(ctx, w, r)
	if !ok {
		return
	}
	approvalID := strings.TrimSpace(r.PathValue("approval_id"))
	if approvalID == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "approval id is required")
		return
	}

	approval, err := h.taskApplication().GetTaskApproval(ctx, task, approvalID)
	if err != nil {
		if errors.Is(err, errTaskStoreNotConfigured) {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
			return
		}
		if isTaskValidationError(err) {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
			return
		}
		if errors.Is(err, errTaskApprovalNotFound) {
			WriteError(w, http.StatusNotFound, errCodeNotFound, "task approval not found")
			return
		}
		telemetry.Error(h.logger, ctx, "gateway.tasks.approvals.get.failed",
			slog.String("event.name", "gateway.tasks.approvals.get.failed"),
			slog.Any("error", err),
		)
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, TaskApprovalResponse{
		Object: "task_approval",
		Data:   renderTaskApproval(approval),
	})
}

func (h *Handler) HandleResolveTaskApproval(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := h.taskApplication().RequireRunner(); err != nil {
		writeTaskRuntimePreflightError(w, err)
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

	result, err := h.taskApplication().ResolveTaskApproval(ctx, orchestrator.ResolveApprovalRequest{
		Task:       task,
		ApprovalID: approvalID,
		Decision:   req.Decision,
		Note:       req.Note,
		RequestID:  RequestIDFromContext(ctx),
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
	case errors.Is(err, errTaskStoreNotConfigured), errors.Is(err, errTaskRunnerNotConfigured):
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
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
	if err := h.taskApplication().RequireRunner(); err != nil {
		writeTaskRuntimePreflightError(w, err)
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

	run, err := h.taskApplication().CancelTaskRun(ctx, task, run, body.Reason)
	if err != nil {
		if errors.Is(err, errTaskStoreNotConfigured) || errors.Is(err, errTaskRunnerNotConfigured) {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
			return
		}
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
	result, err := h.taskApplication().RetryTaskRun(ctx, task, run)
	if err != nil {
		if h.writeTaskLifecycleAppError(w, err) {
			return
		}
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
	result, err := h.taskApplication().ResumeTaskRun(ctx, task, run, req)
	if err != nil {
		if h.writeTaskLifecycleAppError(w, err) {
			return
		}
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
	result, err := h.taskApplication().ContinueTaskRun(ctx, task, run, req.Prompt)
	if err != nil {
		if h.writeTaskLifecycleAppError(w, err) {
			return
		}
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
	result, err := h.taskApplication().RetryTaskRunFromTurn(ctx, task, run, req)
	if err != nil {
		if h.writeTaskLifecycleAppError(w, err) {
			return
		}
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

func (h *Handler) writeTaskLifecycleAppError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, errTaskStoreNotConfigured), errors.Is(err, errTaskRunnerNotConfigured):
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
	case isTaskValidationError(err):
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
	case errors.Is(err, errTaskHasActiveRun), errors.Is(err, errTaskHasOtherActiveRun), errors.Is(err, errTaskRunNotRetryable), errors.Is(err, errTaskRunNotResumable):
		WriteError(w, http.StatusConflict, errCodeInvalidRequest, err.Error())
	case errors.Is(err, errTaskRunNotTurnRetryable), errors.Is(err, errTaskBudgetLower):
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
	default:
		return false
	}
	return true
}

func writeTaskRuntimePreflightError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errTaskStoreNotConfigured), errors.Is(err, errTaskRunnerNotConfigured):
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
	default:
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
	}
}
