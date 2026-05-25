package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/internal/workspacefs"
	"github.com/hecatehq/hecate/pkg/types"
)

func (h *Handler) HandleTaskArtifacts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if h.taskStore == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "task store is not configured")
		return
	}
	task, ok := h.loadAuthorizedTask(ctx, w, r)
	if !ok {
		return
	}

	artifacts, err := h.taskStore.ListArtifacts(ctx, taskstate.ArtifactFilter{TaskID: task.ID})
	if err != nil {
		telemetry.Error(h.logger, ctx, "gateway.tasks.artifacts.list.failed",
			slog.String("event.name", "gateway.tasks.artifacts.list.failed"),
			slog.Any("error", err),
		)
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	items := make([]TaskArtifactItem, 0, len(artifacts))
	for _, artifact := range artifacts {
		items = append(items, renderTaskArtifact(artifact))
	}
	WriteJSON(w, http.StatusOK, TaskArtifactsResponse{
		Object: "task_artifacts",
		Data:   items,
	})
}

func (h *Handler) HandleTaskRunArtifacts(w http.ResponseWriter, r *http.Request) {
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

	artifacts, err := h.taskStore.ListArtifacts(ctx, taskstate.ArtifactFilter{TaskID: task.ID, RunID: runID})
	if err != nil {
		telemetry.Error(h.logger, ctx, "gateway.tasks.run_artifacts.list.failed",
			slog.String("event.name", "gateway.tasks.run_artifacts.list.failed"),
			slog.Any("error", err),
		)
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	items := make([]TaskArtifactItem, 0, len(artifacts))
	for _, artifact := range artifacts {
		items = append(items, renderTaskArtifact(artifact))
	}
	WriteJSON(w, http.StatusOK, TaskArtifactsResponse{
		Object: "task_artifacts",
		Data:   items,
	})
}

func (h *Handler) HandleTaskRunArtifact(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	task, ok := h.loadAuthorizedTask(ctx, w, r)
	if !ok {
		return
	}
	_, ok = h.loadAuthorizedTaskRun(ctx, w, r, task)
	if !ok {
		return
	}
	artifactID := strings.TrimSpace(r.PathValue("artifact_id"))
	if artifactID == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "artifact id is required")
		return
	}
	artifact, found, err := h.taskStore.GetArtifact(ctx, task.ID, artifactID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if !found {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "task artifact not found")
		return
	}
	WriteJSON(w, http.StatusOK, TaskArtifactResponse{
		Object: "task_artifact",
		Data:   renderTaskArtifact(artifact),
	})
}

func (h *Handler) HandleTaskRunPatches(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	task, ok := h.loadAuthorizedTask(ctx, w, r)
	if !ok {
		return
	}
	run, ok := h.loadAuthorizedTaskRun(ctx, w, r, task)
	if !ok {
		return
	}

	artifacts, err := h.taskStore.ListArtifacts(ctx, taskstate.ArtifactFilter{TaskID: task.ID, RunID: run.ID, Kind: "patch"})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	items := make([]TaskPatchItem, 0, len(artifacts))
	for _, artifact := range artifacts {
		items = append(items, renderTaskPatch(artifact))
	}
	WriteJSON(w, http.StatusOK, TaskPatchesResponse{
		Object: "task_patches",
		Data:   items,
	})
}

func (h *Handler) HandleTaskRunPatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_, artifact, ok := h.loadTaskRunPatch(ctx, w, r)
	if !ok {
		return
	}
	WriteJSON(w, http.StatusOK, TaskPatchResponse{
		Object: "task_patch",
		Data:   renderTaskPatch(artifact),
	})
}

func (h *Handler) HandleRevertTaskRunPatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	run, artifact, ok := h.loadTaskRunPatch(ctx, w, r)
	if !ok {
		return
	}
	if artifact.Status == "reverted" {
		WriteError(w, http.StatusConflict, errCodeInvalidRequest, "patch artifact is already reverted")
		return
	}
	before, beforeExisted, err := patchBeforeContent(artifact.ContentText)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	if artifact.Path == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "patch artifact path is empty")
		return
	}
	fsys, rel, err := patchWorkspaceTarget(run.WorkspacePath, artifact.Path)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	if beforeExisted {
		if _, err := fsys.WriteFile(rel, []byte(before), 0o644); err != nil {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
			return
		}
	} else {
		if _, err := fsys.Remove(rel); err != nil && !errors.Is(err, os.ErrNotExist) {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
			return
		}
	}

	artifact.Status = "reverted"
	updated, err := h.taskStore.UpdateArtifact(ctx, artifact)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	_, _ = h.taskStore.AppendRunEvent(ctx, types.TaskRunEvent{
		TaskID:    updated.TaskID,
		RunID:     updated.RunID,
		EventType: "tool.file.reverted",
		Data: map[string]any{
			"artifact_id":     updated.ID,
			"path":            updated.Path,
			"artifact_status": updated.Status,
			"before_existed":  beforeExisted,
		},
		RequestID: RequestIDFromContext(ctx),
		TraceID:   telemetry.TraceIDsFromContext(ctx).TraceID,
		CreatedAt: time.Now().UTC(),
	})
	WriteJSON(w, http.StatusOK, TaskPatchResponse{
		Object: "task_patch",
		Data:   renderTaskPatch(updated),
	})
}

func (h *Handler) HandleApplyTaskRunPatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	run, artifact, ok := h.loadTaskRunPatch(ctx, w, r)
	if !ok {
		return
	}
	if artifact.Status != "proposed" {
		WriteError(w, http.StatusConflict, errCodeInvalidRequest, "only proposed patch artifacts can be applied")
		return
	}
	before, beforeExisted, err := patchBeforeContent(artifact.ContentText)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	after, err := patchAfterContent(artifact.ContentText)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	if artifact.Path == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "patch artifact path is empty")
		return
	}
	fsys, rel, err := patchWorkspaceTarget(run.WorkspacePath, artifact.Path)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	if err := verifyPatchApplyPrecondition(fsys, rel, before, beforeExisted); err != nil {
		WriteError(w, http.StatusConflict, errCodeInvalidRequest, err.Error())
		return
	}
	if _, err := fsys.WriteFile(rel, []byte(after), 0o644); err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}

	artifact.Status = "applied"
	updated, err := h.taskStore.UpdateArtifact(ctx, artifact)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	_, _ = h.taskStore.AppendRunEvent(ctx, types.TaskRunEvent{
		TaskID:    updated.TaskID,
		RunID:     updated.RunID,
		EventType: "tool.file.applied",
		Data: map[string]any{
			"artifact_id":     updated.ID,
			"path":            updated.Path,
			"artifact_status": updated.Status,
		},
		RequestID: RequestIDFromContext(ctx),
		TraceID:   telemetry.TraceIDsFromContext(ctx).TraceID,
		CreatedAt: time.Now().UTC(),
	})
	WriteJSON(w, http.StatusOK, TaskPatchResponse{
		Object: "task_patch",
		Data:   renderTaskPatch(updated),
	})
}

func (h *Handler) loadTaskRunPatch(ctx context.Context, w http.ResponseWriter, r *http.Request) (types.TaskRun, types.TaskArtifact, bool) {
	task, ok := h.loadAuthorizedTask(ctx, w, r)
	if !ok {
		return types.TaskRun{}, types.TaskArtifact{}, false
	}
	run, ok := h.loadAuthorizedTaskRun(ctx, w, r, task)
	if !ok {
		return types.TaskRun{}, types.TaskArtifact{}, false
	}
	artifactID := strings.TrimSpace(r.PathValue("artifact_id"))
	if artifactID == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "artifact id is required")
		return types.TaskRun{}, types.TaskArtifact{}, false
	}
	artifact, found, err := h.taskStore.GetArtifact(ctx, task.ID, artifactID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return types.TaskRun{}, types.TaskArtifact{}, false
	}
	if !found || artifact.RunID != run.ID || artifact.Kind != "patch" {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "task patch not found")
		return types.TaskRun{}, types.TaskArtifact{}, false
	}
	return run, artifact, true
}
func verifyPatchApplyPrecondition(fsys *workspacefs.FS, rel, before string, beforeExisted bool) error {
	current, _, err := fsys.ReadFile(rel)
	if beforeExisted {
		if err != nil {
			return fmt.Errorf("patch target changed before apply: %w", err)
		}
		if string(current) != before {
			return fmt.Errorf("patch target changed before apply")
		}
		return nil
	}
	if err == nil {
		return fmt.Errorf("patch target already exists before apply")
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("patch target cannot be checked before apply: %w", err)
}

func patchWorkspaceTarget(root, path string) (*workspacefs.FS, string, error) {
	root = strings.TrimSpace(root)
	path = strings.TrimSpace(path)
	if root == "" {
		return nil, "", fmt.Errorf("patch artifact workspace is empty")
	}
	fsys, err := workspacefs.New(root)
	if err != nil {
		return nil, "", err
	}
	rel := path
	if filepath.IsAbs(path) {
		rel, err = filepath.Rel(fsys.Root(), filepath.Clean(path))
		if err != nil {
			return nil, "", err
		}
	}
	if _, err := fsys.Resolve(rel); err != nil {
		return nil, "", fmt.Errorf("patch artifact path is outside the run workspace")
	}
	return fsys, rel, nil
}
