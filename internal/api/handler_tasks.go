package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/orchestrator"
	"github.com/hecatehq/hecate/internal/secrets"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
)

// HandleCreateTask gates on requireAny rather than requireAdmin: tasks are
// owned by the local operator (single-user mode); the runtime enforces
// no tenant scoping —
// in context. An admin-only gate would force operators to share the
// admin bearer with every CI/agent invocation just to queue work, which
// defeats per-key auditing.
//
// Downstream surfaces that act on a task ID (run / approve / cancel /
// retry) reuse the same gate; /hecate/v1/mcp/probe inherits it because probing
// runs the same arbitrary command a task would.
func (h *Handler) HandleCreateTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if h.taskStore == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "task store is not configured")
		return
	}

	var req CreateTaskRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	applyExecutionProfileDefaults(&req)

	title := strings.TrimSpace(req.Title)
	prompt := strings.TrimSpace(req.Prompt)
	if title == "" {
		if prompt == "" {
			title = "New task"
		} else {
			title = prompt
			if len(title) > 80 {
				title = strings.TrimSpace(title[:80]) + "..."
			}
		}
	}
	// prompt is required for agent_loop tasks; direct-execution tasks
	// (shell, git, file) carry their work in the command/file fields and
	// don't need a natural-language prompt.
	effectiveKind := strings.TrimSpace(req.ExecutionKind)
	isAgentLoop := effectiveKind == "" || effectiveKind == "agent_loop"
	if prompt == "" && isAgentLoop {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "prompt is required")
		return
	}

	mcpServers, mcpErr := normalizeMCPServerConfigs(req.MCPServers, h.secretCipher, h.config.Server.TaskMaxMCPServersPerTask)
	if mcpErr != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, mcpErr.Error())
		return
	}

	workspaceMode := strings.TrimSpace(req.WorkspaceMode)
	if workspaceMode == "" {
		workspaceMode = "ephemeral"
	}
	priority := strings.TrimSpace(req.Priority)
	if priority == "" {
		priority = "normal"
	}
	projectID := strings.TrimSpace(req.ProjectID)
	if projectID != "" {
		if h.projects == nil {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "project store is not configured")
			return
		}
		if _, ok, err := h.projects.Get(ctx, projectID); err != nil {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
			return
		} else if !ok {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "project not found")
			return
		}
	}

	now := time.Now().UTC()
	task := types.Task{
		ID:                 newTaskID(),
		Title:              title,
		Prompt:             prompt,
		ProjectID:          projectID,
		SystemPrompt:       strings.TrimSpace(req.SystemPrompt),
		ExecutionProfile:   strings.TrimSpace(req.ExecutionProfile),
		Repo:               strings.TrimSpace(req.Repo),
		BaseBranch:         strings.TrimSpace(req.BaseBranch),
		WorkspaceMode:      workspaceMode,
		ExecutionKind:      strings.TrimSpace(req.ExecutionKind),
		ShellCommand:       strings.TrimSpace(req.ShellCommand),
		GitCommand:         strings.TrimSpace(req.GitCommand),
		WorkingDirectory:   strings.TrimSpace(req.WorkingDirectory),
		FileOperation:      strings.TrimSpace(req.FileOperation),
		FilePath:           strings.TrimSpace(req.FilePath),
		FileContent:        req.FileContent,
		SandboxAllowedRoot: strings.TrimSpace(req.SandboxAllowedRoot),
		SandboxReadOnly:    req.SandboxReadOnly,
		SandboxNetwork:     req.SandboxNetwork,
		TimeoutMS:          req.TimeoutMS,
		Status:             "queued",
		Priority:           priority,
		RequestedModel:     strings.TrimSpace(req.RequestedModel),
		RequestedProvider:  strings.TrimSpace(req.RequestedProvider),
		BudgetMicrosUSD:    req.BudgetMicrosUSD,
		MCPServers:         mcpServers,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	created, err := h.taskStore.CreateTask(ctx, task)
	if err != nil {
		telemetry.Error(h.logger, ctx, "gateway.tasks.create.failed",
			slog.String("event.name", "gateway.tasks.create.failed"),
			slog.Any("error", err),
		)
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, TaskResponse{
		Object: "task",
		Data:   buildTaskItem(ctx, h.taskStore, created),
	})
}

func applyExecutionProfileDefaults(req *CreateTaskRequest) {
	if req == nil {
		return
	}
	profile := strings.TrimSpace(req.ExecutionProfile)
	if profile != "repo_local" && profile != "coding_agent" {
		return
	}
	if strings.TrimSpace(req.ExecutionKind) == "" {
		req.ExecutionKind = "agent_loop"
	}
	if strings.TrimSpace(req.WorkspaceMode) == "" {
		req.WorkspaceMode = "persistent"
	}
	if strings.TrimSpace(req.WorkingDirectory) == "" {
		req.WorkingDirectory = "."
	}
	if strings.TrimSpace(req.SandboxAllowedRoot) == "" {
		workingDir := strings.TrimSpace(req.WorkingDirectory)
		repo := strings.TrimSpace(req.Repo)
		switch {
		case filepath.IsAbs(workingDir):
			req.SandboxAllowedRoot = workingDir
		case filepath.IsAbs(repo):
			req.SandboxAllowedRoot = repo
		}
	}
	if req.TimeoutMS <= 0 {
		req.TimeoutMS = 120000
	}
	if profile == "coding_agent" {
		if req.TimeoutMS <= 120000 {
			req.TimeoutMS = 300000
		}
		if strings.TrimSpace(req.SystemPrompt) == "" {
			req.SystemPrompt = codingAgentProfileSystemPrompt
		}
	}
}

const codingAgentProfileSystemPrompt = `You are running inside Hecate's coding-agent runtime.

Use read_file and list_dir before editing. Prefer file_edit for targeted changes and file_write only for new files or full rewrites. Keep changes scoped to the user's request. Explain important tradeoffs in the final answer, and mention files changed when useful.`

func (h *Handler) HandleTasks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if h.taskStore == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "task store is not configured")
		return
	}

	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "limit query parameter must be a non-negative integer")
			return
		}
		if value > 200 {
			value = 200
		}
		limit = value
	}

	filter := taskstate.TaskFilter{
		Status: strings.TrimSpace(r.URL.Query().Get("status")),
		Limit:  limit,
	}
	if rawProjectIDs, ok := r.URL.Query()["project_id"]; ok {
		projectID := strings.TrimSpace("")
		if len(rawProjectIDs) > 0 {
			projectID = strings.TrimSpace(rawProjectIDs[0])
		}
		filter.ProjectID = &projectID
	}
	result, err := h.taskStore.ListTasks(ctx, filter)
	if err != nil {
		telemetry.Error(h.logger, ctx, "gateway.tasks.list.failed",
			slog.String("event.name", "gateway.tasks.list.failed"),
			slog.Any("error", err),
		)
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}

	items := make([]TaskItem, 0, len(result))
	for _, task := range result {
		items = append(items, buildTaskItem(ctx, h.taskStore, task))
	}
	WriteJSON(w, http.StatusOK, TasksResponse{
		Object: "tasks",
		Data:   items,
	})
}

func (h *Handler) HandleTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if h.taskStore == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "task store is not configured")
		return
	}

	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "task id is required")
		return
	}

	task, found, err := h.taskStore.GetTask(ctx, id)
	if err != nil {
		telemetry.Error(h.logger, ctx, "gateway.tasks.get.failed",
			slog.String("event.name", "gateway.tasks.get.failed"),
			slog.Any("error", err),
		)
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if !found {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "task not found")
		return
	}
	WriteJSON(w, http.StatusOK, TaskResponse{
		Object: "task",
		Data:   buildTaskItem(ctx, h.taskStore, task),
	})
}

func (h *Handler) HandleDeleteTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if h.taskStore == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "task store is not configured")
		return
	}

	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "task id is required")
		return
	}

	task, found, err := h.taskStore.GetTask(ctx, id)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if !found {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "task not found")
		return
	}
	if active, err := taskHasActiveRun(ctx, h.taskStore, task); err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	} else if active {
		WriteError(w, http.StatusConflict, errCodeInvalidRequest, "cannot delete a task with an active run; cancel it first")
		return
	}

	if err := h.taskStore.DeleteTask(ctx, id); err != nil {
		telemetry.Error(h.logger, ctx, "gateway.tasks.delete.failed",
			slog.String("event.name", "gateway.tasks.delete.failed"),
			slog.String("task_id", id),
			slog.Any("error", err),
		)
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) HandleStartTask(w http.ResponseWriter, r *http.Request) {
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
	if active, err := taskHasActiveRun(ctx, h.taskStore, task); err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	} else if active {
		WriteError(w, http.StatusConflict, errCodeInvalidRequest, "task already has an active run")
		return
	}
	result, err := h.taskRunner.StartTask(ctx, task, newOpaqueTaskResourceID)
	if err != nil {
		telemetry.Error(h.logger, ctx, "gateway.tasks.start.failed",
			slog.String("event.name", "gateway.tasks.start.failed"),
			slog.Any("error", err),
		)
		if errors.Is(err, orchestrator.ErrAgentLoopMisconfigured) {
			WriteError(w, http.StatusUnprocessableEntity, errCodeModelNotConfigured, err.Error())
			return
		}
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if result.TraceID != "" {
		w.Header().Set("X-Trace-Id", result.TraceID)
	}
	if result.SpanID != "" {
		w.Header().Set("X-Span-Id", result.SpanID)
	}
	WriteJSON(w, http.StatusOK, TaskRunResponse{
		Object: "task_run",
		Data:   renderTaskRun(result.Run),
	})
}

func taskHasActiveRun(ctx context.Context, store taskstate.Store, task types.Task) (bool, error) {
	latestRunID := strings.TrimSpace(task.LatestRunID)
	if latestRunID != "" && store != nil {
		run, found, err := store.GetRun(ctx, task.ID, latestRunID)
		if err != nil {
			return false, err
		}
		if found {
			return !types.IsTerminalTaskRunStatus(run.Status), nil
		}
	}
	return latestRunID != "" && !types.IsTerminalTaskRunStatus(task.Status), nil
}

func taskHasOtherActiveRun(ctx context.Context, store taskstate.Store, task types.Task, currentRunID string) (bool, error) {
	latestRunID := strings.TrimSpace(task.LatestRunID)
	if latestRunID == "" || latestRunID == strings.TrimSpace(currentRunID) {
		return false, nil
	}
	return taskHasActiveRun(ctx, store, task)
}

func (h *Handler) HandleTaskRuns(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if h.taskStore == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "task store is not configured")
		return
	}
	task, ok := h.loadAuthorizedTask(ctx, w, r)
	if !ok {
		return
	}

	runs, err := h.taskStore.ListRuns(ctx, task.ID)
	if err != nil {
		telemetry.Error(h.logger, ctx, "gateway.tasks.runs.list.failed",
			slog.String("event.name", "gateway.tasks.runs.list.failed"),
			slog.Any("error", err),
		)
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}

	items := make([]TaskRunItem, 0, len(runs))
	for _, run := range runs {
		items = append(items, renderTaskRun(run))
	}
	WriteJSON(w, http.StatusOK, TaskRunsResponse{
		Object: "task_runs",
		Data:   items,
	})
}

func (h *Handler) HandleTaskRun(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if h.taskStore == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "task store is not configured")
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

	WriteJSON(w, http.StatusOK, TaskRunResponse{
		Object: "task_run",
		Data:   renderTaskRun(run),
	})
}

func (h *Handler) HandleTaskRunSteps(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if h.taskStore == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "task store is not configured")
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
	runID := run.ID

	steps, err := h.taskStore.ListSteps(ctx, runID)
	if err != nil {
		telemetry.Error(h.logger, ctx, "gateway.tasks.steps.list.failed",
			slog.String("event.name", "gateway.tasks.steps.list.failed"),
			slog.Any("error", err),
		)
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	items := make([]TaskStepItem, 0, len(steps))
	for _, step := range steps {
		items = append(items, renderTaskStep(step))
	}
	WriteJSON(w, http.StatusOK, TaskStepsResponse{
		Object: "task_steps",
		Data:   items,
	})
}

func (h *Handler) HandleTaskRunStep(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if h.taskStore == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "task store is not configured")
		return
	}
	task, ok := h.loadAuthorizedTask(ctx, w, r)
	if !ok {
		return
	}
	stepID := strings.TrimSpace(r.PathValue("step_id"))
	if stepID == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "step id is required")
		return
	}
	run, ok := h.loadAuthorizedTaskRun(ctx, w, r, task)
	if !ok {
		return
	}
	runID := run.ID
	step, found, err := h.taskStore.GetStep(ctx, runID, stepID)
	if err != nil {
		telemetry.Error(h.logger, ctx, "gateway.tasks.steps.get.failed",
			slog.String("event.name", "gateway.tasks.steps.get.failed"),
			slog.Any("error", err),
		)
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if !found {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "task step not found")
		return
	}
	WriteJSON(w, http.StatusOK, TaskStepResponse{
		Object: "task_step",
		Data:   renderTaskStep(step),
	})
}

// HandleRetryTaskRunFromTurn re-runs an agent_loop run from turn N,
// preserving the source conversation up to (but not including) that
// turn's assistant message. The new run is a sibling of the source
// (not a child) — it gets its own run number and step indices. Only
// terminal runs are eligible; the source must have produced an
// agent_conversation artifact, and the requested turn must lie within
// the source's completed assistant-turn count.

func buildTaskItem(ctx context.Context, store taskstate.Store, task types.Task) TaskItem {
	item := renderTaskItem(task)
	counts := loadTaskItemCounts(ctx, store, task.ID)
	item.PendingApprovalCount = counts.PendingApprovalCount
	item.StepCount = counts.StepCount
	item.ArtifactCount = counts.ArtifactCount
	// Fetch the latest run so the task list can show what model +
	// provider actually ran (vs. what the operator requested, which
	// may have been "auto"). One extra GetRun per task — same store
	// hit pattern as loadTaskItemCounts already incurs. Cheap on
	// memory/sqlite and avoids adding a dedicated task-list join.
	if strings.TrimSpace(task.LatestRunID) != "" {
		if run, found, err := store.GetRun(ctx, task.ID, task.LatestRunID); err == nil && found {
			item.LatestModel = run.Model
			item.LatestProvider = run.Provider
		}
	}
	return item
}

func buildTaskActivityItems(steps []TaskStepItem, artifacts []TaskArtifactItem, approvals []TaskApprovalItem, run types.TaskRun) []TaskActivityItem {
	items := make([]TaskActivityItem, 0, len(steps))
	approvalStatusByID := make(map[string]string, len(approvals))
	for _, approval := range approvals {
		approvalStatusByID[approval.ID] = approval.Status
	}
	for _, step := range steps {
		itemType := "step"
		switch {
		case step.ApprovalID != "" || step.Kind == "approval":
			itemType = "approval"
		case step.Kind == "model":
			itemType = "thinking"
		case step.Kind == "tool" || step.Kind == "shell" || step.Kind == "git" || step.Kind == "file" || step.ToolName != "":
			itemType = "tool_call"
		}
		status := step.Status
		needsAction := step.ApprovalID != "" && step.Status == "awaiting_approval"
		if approvalStatus := approvalStatusByID[step.ApprovalID]; approvalStatus != "" {
			status = approvalStatus
			needsAction = approvalStatus == "pending"
		}
		summary := cloneActivitySummary(step.OutputSummary)
		addShellDebugSummary(summary, step.Input)
		items = append(items, TaskActivityItem{
			ID:          "step:" + step.ID,
			Type:        itemType,
			Status:      status,
			Title:       step.Title,
			StepID:      step.ID,
			ApprovalID:  step.ApprovalID,
			ToolName:    step.ToolName,
			Kind:        step.Kind,
			Summary:     summary,
			OccurredAt:  firstNonEmpty(step.StartedAt, step.FinishedAt),
			NeedsAction: needsAction,
			Terminal:    step.Status == "completed" || step.Status == "failed" || step.Status == "cancelled" || (step.ApprovalID != "" && !needsAction),
		})
	}
	for _, artifact := range artifacts {
		itemType := "artifact"
		switch artifact.Kind {
		case "patch":
			itemType = "patch"
		case "git_summary":
			itemType = "changed_files"
		case "summary":
			itemType = "final_answer"
		}
		summary := map[string]any{
			"size_bytes": artifact.SizeBytes,
			"mime_type":  artifact.MimeType,
		}
		if preview := taskActivityArtifactContentPreview(artifact); preview != "" {
			summary["content_preview"] = preview
		}
		items = append(items, TaskActivityItem{
			ID:         "artifact:" + artifact.ID,
			Type:       itemType,
			Status:     artifact.Status,
			Title:      artifact.Name,
			StepID:     artifact.StepID,
			ArtifactID: artifact.ID,
			Kind:       artifact.Kind,
			Path:       artifact.Path,
			Summary:    summary,
			OccurredAt: artifact.CreatedAt,
			Terminal:   artifact.Status == "ready" || artifact.Status == "applied" || artifact.Status == "reverted",
		})
	}
	for _, approval := range approvals {
		items = append(items, TaskActivityItem{
			ID:          "approval:" + approval.ID,
			Type:        "approval",
			Status:      approval.Status,
			Title:       approval.Kind,
			ApprovalID:  approval.ID,
			Kind:        approval.Kind,
			Summary:     map[string]any{"reason": approval.Reason},
			OccurredAt:  approval.CreatedAt,
			NeedsAction: approval.Status == "pending",
			Terminal:    approval.Status != "pending",
		})
	}
	if types.IsTerminalTaskRunStatus(run.Status) {
		items = append(items, TaskActivityItem{
			ID:         "run:" + run.ID + ":terminal",
			Type:       "run_result",
			Status:     run.Status,
			Title:      firstNonEmpty(run.LastError, "Run "+run.Status),
			OccurredAt: formatOptionalTime(run.FinishedAt),
			Terminal:   true,
		})
	}
	sortTaskActivityItems(items)
	return items
}

func cloneActivitySummary(summary map[string]any) map[string]any {
	if len(summary) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(summary)+2)
	for key, value := range summary {
		out[key] = value
	}
	return out
}

func addShellDebugSummary(summary map[string]any, input map[string]any) {
	if len(input) == 0 {
		return
	}
	if value, ok := input[telemetry.AttrHecateSandboxRTKEnabled]; ok {
		summary[telemetry.AttrHecateSandboxRTKEnabled] = value
	}
	if value, ok := input[telemetry.AttrHecateSandboxRTKCommandBefore]; ok {
		summary[telemetry.AttrHecateSandboxRTKCommandBefore] = value
	}
	if value, ok := input[telemetry.AttrHecateSandboxRTKCommandAfter]; ok {
		summary[telemetry.AttrHecateSandboxRTKCommandAfter] = value
	}
	if value, ok := input["argv"]; ok {
		summary["argv"] = value
	}
}

const taskActivityArtifactPreviewMaxBytes = 2000
const taskActivityArtifactPreviewTruncatedSuffix = "\n...[truncated]"

func taskActivityArtifactContentPreview(artifact TaskArtifactItem) string {
	if artifact.Kind != "stdout" && artifact.Kind != "stderr" {
		return ""
	}
	maxBytes := taskActivityArtifactPreviewMaxBytes
	content := strings.TrimRight(artifact.ContentText, "\r\n")
	if content == "" {
		return ""
	}
	if len(content) <= maxBytes {
		return content
	}
	budget := maxBytes - len(taskActivityArtifactPreviewTruncatedSuffix)
	if budget <= 0 {
		return taskActivityArtifactPreviewTruncatedSuffix[:maxBytes]
	}
	end := truncateStringByteIndex(content, budget)
	return content[:end] + taskActivityArtifactPreviewTruncatedSuffix
}

func truncateStringByteIndex(content string, maxBytes int) int {
	if len(content) <= maxBytes {
		return len(content)
	}
	end := 0
	for index := range content {
		if index > maxBytes {
			break
		}
		end = index
	}
	return end
}

func sortTaskActivityItems(items []TaskActivityItem) {
	sort.SliceStable(items, func(i, j int) bool {
		left := items[i].OccurredAt
		right := items[j].OccurredAt
		if left == "" || right == "" || left == right {
			return items[i].ID < items[j].ID
		}
		return left < right
	})
}

func formatOptionalTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

type taskItemCounts struct {
	PendingApprovalCount int
	StepCount            int
	ArtifactCount        int
}

func renderTaskItem(task types.Task) TaskItem {
	item := TaskItem{
		ID:                          task.ID,
		Title:                       task.Title,
		Prompt:                      task.Prompt,
		ProjectID:                   task.ProjectID,
		SystemPrompt:                task.SystemPrompt,
		WorkspaceSystemPromptPolicy: task.WorkspaceSystemPromptPolicy,
		ExecutionProfile:            task.ExecutionProfile,
		OriginKind:                  task.OriginKind,
		OriginID:                    task.OriginID,
		Repo:                        task.Repo,
		BaseBranch:                  task.BaseBranch,
		WorkspaceMode:               task.WorkspaceMode,
		ExecutionKind:               task.ExecutionKind,
		ShellCommand:                task.ShellCommand,
		GitCommand:                  task.GitCommand,
		WorkingDirectory:            task.WorkingDirectory,
		FileOperation:               task.FileOperation,
		FilePath:                    task.FilePath,
		FileContent:                 task.FileContent,
		SandboxAllowedRoot:          task.SandboxAllowedRoot,
		SandboxReadOnly:             task.SandboxReadOnly,
		SandboxNetwork:              task.SandboxNetwork,
		TimeoutMS:                   task.TimeoutMS,
		Status:                      task.Status,
		Priority:                    task.Priority,
		RequestedModel:              task.RequestedModel,
		RequestedProvider:           task.RequestedProvider,
		BudgetMicrosUSD:             task.BudgetMicrosUSD,
		LatestRunID:                 task.LatestRunID,
		LastError:                   task.LastError,
		RootTraceID:                 task.RootTraceID,
		LatestTraceID:               task.LatestTraceID,
		LatestRequestID:             task.LatestRequestID,
		MCPServers:                  renderMCPServerConfigs(task.MCPServers),
	}
	if !task.CreatedAt.IsZero() {
		item.CreatedAt = task.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	if !task.UpdatedAt.IsZero() {
		item.UpdatedAt = task.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	if !task.StartedAt.IsZero() {
		item.StartedAt = task.StartedAt.UTC().Format(time.RFC3339Nano)
	}
	if !task.FinishedAt.IsZero() {
		item.FinishedAt = task.FinishedAt.UTC().Format(time.RFC3339Nano)
	}
	return item
}

// normalizeMCPServerConfigs converts the wire shape into the internal
// types.MCPServerConfig slice used by the orchestrator. Trims whitespace
// on string fields, enforces non-empty Name, rejects duplicate names,
// validates that exactly one of command or url is set per entry, and
// caps the number of entries.
//
// maxEntries is the per-task cap. Zero or negative disables the cap
// (used by tests that don't care about the limit). When exceeded the
// caller surfaces this as a 400 — a malformed task configuring 1000
// servers would otherwise burn N initialize handshakes and N file
// descriptors before the run even started.
//
// Env and Header values are stored in three forms depending on cipher
// availability:
//   - "$VAR_NAME" references are stored verbatim — resolved from the
//     Hecate process environment at subprocess spawn time.
//   - Literal values are encrypted with cipher when available →
//     stored as "enc:<base64>". When cipher is nil they are stored
//     as-is; operators should prefer $VAR_NAME references in that case.
//
// Returns nil for an empty input (the agent loop skips MCP-host
// startup when MCPServers is nil/empty).
func normalizeMCPServerConfigs(items []MCPServerConfigItem, cipher secrets.Cipher, maxEntries int) ([]types.MCPServerConfig, error) {
	if len(items) == 0 {
		return nil, nil
	}
	if maxEntries > 0 && len(items) > maxEntries {
		return nil, fmt.Errorf("mcp_servers: %d entries exceeds per-task cap of %d (raise HECATE_TASK_MAX_MCP_SERVERS_PER_TASK if you genuinely need more)", len(items), maxEntries)
	}
	out := make([]types.MCPServerConfig, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for i, item := range items {
		name := strings.TrimSpace(item.Name)
		command := strings.TrimSpace(item.Command)
		rawURL := strings.TrimSpace(item.URL)
		if name == "" {
			return nil, fmt.Errorf("mcp_servers[%d]: name is required", i)
		}
		if command != "" && rawURL != "" {
			return nil, fmt.Errorf("mcp_servers[%d] (%s): command and url are mutually exclusive", i, name)
		}
		if command == "" && rawURL == "" {
			return nil, fmt.Errorf("mcp_servers[%d] (%s): either command or url is required", i, name)
		}
		if _, dup := seen[name]; dup {
			return nil, fmt.Errorf("mcp_servers[%d]: duplicate name %q", i, name)
		}
		seen[name] = struct{}{}
		policy := strings.TrimSpace(item.ApprovalPolicy)
		if !types.IsValidMCPApprovalPolicy(policy) {
			return nil, fmt.Errorf("mcp_servers[%d] (%s): approval_policy %q is invalid; must be one of \"auto\", \"require_approval\", \"block\"", i, name, policy)
		}
		// Defensive copies — callers may reuse the request struct.
		args := append([]string(nil), item.Args...)
		env, err := storeSecretMap(item.Env, cipher, fmt.Sprintf("mcp_servers[%d] (%s): env", i, name))
		if err != nil {
			return nil, err
		}
		headers, err := storeSecretMap(item.Headers, cipher, fmt.Sprintf("mcp_servers[%d] (%s): headers", i, name))
		if err != nil {
			return nil, err
		}
		out = append(out, types.MCPServerConfig{
			Name:           name,
			Command:        command,
			Args:           args,
			Env:            env,
			URL:            rawURL,
			Headers:        headers,
			ApprovalPolicy: policy,
		})
	}
	return out, nil
}

// storeSecretMap applies storeMCPEnvValue to every value in m, returning
// a new defensive copy. Returns nil for a nil or empty input. errPrefix
// is prepended to any encryption error for context (e.g. "mcp_servers[0] (github): env").
func storeSecretMap(m map[string]string, cipher secrets.Cipher, errPrefix string) (map[string]string, error) {
	if len(m) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		stored, err := storeMCPEnvValue(v, cipher)
		if err != nil {
			return nil, fmt.Errorf("%s %q: %w", errPrefix, k, err)
		}
		out[k] = stored
	}
	return out, nil
}

// storeMCPEnvValue prepares a single env value for storage:
//   - "$VAR_NAME" references are stored verbatim.
//   - Already "enc:"-prefixed values pass through (idempotent on re-create).
//   - Literal values are encrypted with cipher when non-nil, else stored as-is.
func storeMCPEnvValue(v string, cipher secrets.Cipher) (string, error) {
	if isMCPEnvRef(v) || strings.HasPrefix(v, types.MCPEnvEncPrefix) {
		return v, nil
	}
	if cipher == nil {
		return v, nil
	}
	ct, err := cipher.Encrypt(v)
	if err != nil {
		return "", fmt.Errorf("encrypt: %w", err)
	}
	return types.MCPEnvEncPrefix + ct, nil
}

// isMCPEnvRef reports whether v is a $VAR_NAME reference. Mirrors the
// same check in the orchestrator's resolveEnvValue so that both the
// storage path (api) and the spawn path (orchestrator) agree on what
// counts as a reference vs. a value that should be redacted or encrypted.
func isMCPEnvRef(v string) bool {
	if len(v) < 2 || v[0] != '$' {
		return false
	}
	for i, c := range v[1:] {
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c == '_':
			// valid in any position
		case c >= '0' && c <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// renderMCPServerConfigs is the inverse of normalizeMCPServerConfigs:
// internal slice → wire shape on TaskItem responses. Env and Headers
// values are selectively redacted:
//   - "$VAR_NAME" references are returned verbatim (they name a variable,
//     not the secret itself).
//   - "enc:<base64>" encrypted values and bare literals are replaced with
//     "[redacted]" so stored tokens never leak through the task API.
func renderMCPServerConfigs(configs []types.MCPServerConfig) []MCPServerConfigItem {
	if len(configs) == 0 {
		return nil
	}
	out := make([]MCPServerConfigItem, 0, len(configs))
	for _, c := range configs {
		args := append([]string(nil), c.Args...)
		out = append(out, MCPServerConfigItem{
			Name:           c.Name,
			Command:        c.Command,
			Args:           args,
			Env:            redactSecretMap(c.Env),
			URL:            c.URL,
			Headers:        redactSecretMap(c.Headers),
			ApprovalPolicy: c.ApprovalPolicy,
		})
	}
	return out
}

// redactSecretMap returns a copy of m where non-$VAR_NAME values are
// replaced with "[redacted]". Returns nil for nil/empty input.
func redactSecretMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		if isMCPEnvRef(v) {
			out[k] = v
		} else {
			out[k] = "[redacted]"
		}
	}
	return out
}

func loadTaskItemCounts(ctx context.Context, store taskstate.Store, taskID string) taskItemCounts {
	if store == nil {
		return taskItemCounts{}
	}

	counts := taskItemCounts{}
	runs, err := store.ListRuns(ctx, taskID)
	if err == nil {
		for _, run := range runs {
			counts.StepCount += run.StepCount
			counts.ArtifactCount += run.ArtifactCount
		}
	}

	approvals, err := store.ListApprovals(ctx, taskID)
	if err == nil {
		for _, approval := range approvals {
			if approval.Status == "pending" {
				counts.PendingApprovalCount++
			}
		}
	}

	return counts
}

func renderTaskRun(run types.TaskRun) TaskRunItem {
	item := TaskRunItem{
		ID:                 run.ID,
		TaskID:             run.TaskID,
		Number:             run.Number,
		Status:             run.Status,
		Orchestrator:       run.Orchestrator,
		Model:              run.Model,
		Provider:           run.Provider,
		ProviderKind:       run.ProviderKind,
		WorkspaceID:        run.WorkspaceID,
		WorkspacePath:      run.WorkspacePath,
		StepCount:          run.StepCount,
		ApprovalCount:      run.ApprovalCount,
		ArtifactCount:      run.ArtifactCount,
		TotalCostMicrosUSD: run.TotalCostMicrosUSD,
		PriorCostMicrosUSD: run.PriorCostMicrosUSD,
		LastError:          run.LastError,
		RequestID:          run.RequestID,
		TraceID:            run.TraceID,
		RootSpanID:         run.RootSpanID,
		OtelStatusCode:     run.OtelStatusCode,
		OtelStatusMessage:  run.OtelStatusMessage,
	}
	if !run.StartedAt.IsZero() {
		item.StartedAt = run.StartedAt.UTC().Format(time.RFC3339Nano)
	}
	if !run.FinishedAt.IsZero() {
		item.FinishedAt = run.FinishedAt.UTC().Format(time.RFC3339Nano)
	}
	return item
}

func renderTaskStep(step types.TaskStep) TaskStepItem {
	item := TaskStepItem{
		ID:            step.ID,
		TaskID:        step.TaskID,
		RunID:         step.RunID,
		ParentStepID:  step.ParentStepID,
		Index:         step.Index,
		Kind:          step.Kind,
		Title:         step.Title,
		Status:        step.Status,
		Phase:         step.Phase,
		Result:        step.Result,
		ToolName:      step.ToolName,
		Input:         step.Input,
		OutputSummary: step.OutputSummary,
		ExitCode:      step.ExitCode,
		Error:         step.Error,
		ErrorKind:     step.ErrorKind,
		ApprovalID:    step.ApprovalID,
		RequestID:     step.RequestID,
		TraceID:       step.TraceID,
		SpanID:        step.SpanID,
		ParentSpanID:  step.ParentSpanID,
	}
	if !step.StartedAt.IsZero() {
		item.StartedAt = step.StartedAt.UTC().Format(time.RFC3339Nano)
	}
	if !step.FinishedAt.IsZero() {
		item.FinishedAt = step.FinishedAt.UTC().Format(time.RFC3339Nano)
	}
	return item
}

func renderTaskApproval(approval types.TaskApproval) TaskApprovalItem {
	item := TaskApprovalItem{
		ID:             approval.ID,
		TaskID:         approval.TaskID,
		RunID:          approval.RunID,
		StepID:         approval.StepID,
		Kind:           approval.Kind,
		Status:         approval.Status,
		Reason:         approval.Reason,
		RequestedBy:    approval.RequestedBy,
		ResolvedBy:     approval.ResolvedBy,
		ResolutionNote: approval.ResolutionNote,
		RequestID:      approval.RequestID,
		TraceID:        approval.TraceID,
		SpanID:         approval.SpanID,
	}
	if !approval.CreatedAt.IsZero() {
		item.CreatedAt = approval.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	if !approval.ResolvedAt.IsZero() {
		item.ResolvedAt = approval.ResolvedAt.UTC().Format(time.RFC3339Nano)
	}
	return item
}

func renderTaskArtifact(artifact types.TaskArtifact) TaskArtifactItem {
	item := TaskArtifactItem{
		ID:          artifact.ID,
		TaskID:      artifact.TaskID,
		RunID:       artifact.RunID,
		StepID:      artifact.StepID,
		Kind:        artifact.Kind,
		Name:        artifact.Name,
		Description: artifact.Description,
		MimeType:    artifact.MimeType,
		StorageKind: artifact.StorageKind,
		Path:        artifact.Path,
		ContentText: artifact.ContentText,
		ObjectRef:   artifact.ObjectRef,
		SizeBytes:   artifact.SizeBytes,
		SHA256:      artifact.SHA256,
		Status:      artifact.Status,
		RequestID:   artifact.RequestID,
		TraceID:     artifact.TraceID,
		SpanID:      artifact.SpanID,
	}
	if !artifact.CreatedAt.IsZero() {
		item.CreatedAt = artifact.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	return item
}

func renderTaskPatch(artifact types.TaskArtifact) TaskPatchItem {
	_, beforeExisted, _ := patchBeforeContent(artifact.ContentText)
	return TaskPatchItem{
		Artifact:      renderTaskArtifact(artifact),
		Diff:          artifact.ContentText,
		Status:        artifact.Status,
		Path:          artifact.Path,
		BeforeExisted: beforeExisted,
	}
}

func patchBeforeContent(diff string) (string, bool, error) {
	content, beforeExisted, err := patchContent(diff, "-")
	return content, beforeExisted, err
}

func patchAfterContent(diff string) (string, bool, error) {
	return patchContent(diff, "+")
}

func patchContent(diff, prefix string) (string, bool, error) {
	lines := strings.Split(diff, "\n")
	if len(lines) < 3 {
		return "", false, fmt.Errorf("patch artifact diff is malformed")
	}
	if !strings.HasPrefix(lines[0], "--- ") || !strings.HasPrefix(lines[1], "+++ ") || !strings.HasPrefix(lines[2], "@@ ") {
		return "", false, fmt.Errorf("patch artifact diff is malformed")
	}
	existed := !strings.HasPrefix(lines[0], "--- /dev/null")
	if prefix == "+" {
		existed = !strings.HasPrefix(lines[1], "+++ /dev/null")
	}
	contentLines := make([]string, 0)
	for _, line := range lines[3:] {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, prefix) {
			contentLines = append(contentLines, strings.TrimPrefix(line, prefix))
		}
	}
	if len(contentLines) == 0 {
		return "", existed, nil
	}
	return strings.Join(contentLines, "\n") + "\n", existed, nil
}

func newTaskID() string {
	return newOpaqueTaskResourceID("task")
}

func newOpaqueTaskResourceID(prefix string) string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return prefix + "_" + time.Now().UTC().Format("20060102150405.000000000")
	}
	return prefix + "_" + hex.EncodeToString(buf)
}
