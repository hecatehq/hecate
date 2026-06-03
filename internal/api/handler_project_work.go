package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/orchestrator"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/pkg/types"
)

type createProjectWorkRoleRequest struct {
	ID           string `json:"id,omitempty"`
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	Instructions string `json:"instructions,omitempty"`
}

type updateProjectWorkRoleRequest struct {
	Name         *string `json:"name,omitempty"`
	Description  *string `json:"description,omitempty"`
	Instructions *string `json:"instructions,omitempty"`
}

type createProjectWorkItemRequest struct {
	ID              string   `json:"id,omitempty"`
	Title           string   `json:"title"`
	Brief           string   `json:"brief,omitempty"`
	Status          string   `json:"status,omitempty"`
	Priority        string   `json:"priority,omitempty"`
	OwnerRoleID     string   `json:"owner_role_id,omitempty"`
	ReviewerRoleIDs []string `json:"reviewer_role_ids,omitempty"`
}

type updateProjectWorkItemRequest struct {
	Title           *string   `json:"title,omitempty"`
	Brief           *string   `json:"brief,omitempty"`
	Status          *string   `json:"status,omitempty"`
	Priority        *string   `json:"priority,omitempty"`
	OwnerRoleID     *string   `json:"owner_role_id,omitempty"`
	ReviewerRoleIDs *[]string `json:"reviewer_role_ids,omitempty"`
}

type createProjectWorkAssignmentRequest struct {
	ID                string `json:"id,omitempty"`
	RoleID            string `json:"role_id"`
	DriverKind        string `json:"driver_kind,omitempty"`
	Status            string `json:"status,omitempty"`
	TaskID            string `json:"task_id,omitempty"`
	RunID             string `json:"run_id,omitempty"`
	ChatSessionID     string `json:"chat_session_id,omitempty"`
	MessageID         string `json:"message_id,omitempty"`
	ContextSnapshotID string `json:"context_snapshot_id,omitempty"`
	StartedAt         string `json:"started_at,omitempty"`
	CompletedAt       string `json:"completed_at,omitempty"`
}

type updateProjectWorkAssignmentRequest struct {
	RoleID            *string `json:"role_id,omitempty"`
	DriverKind        *string `json:"driver_kind,omitempty"`
	Status            *string `json:"status,omitempty"`
	TaskID            *string `json:"task_id,omitempty"`
	RunID             *string `json:"run_id,omitempty"`
	ChatSessionID     *string `json:"chat_session_id,omitempty"`
	MessageID         *string `json:"message_id,omitempty"`
	ContextSnapshotID *string `json:"context_snapshot_id,omitempty"`
	StartedAt         *string `json:"started_at,omitempty"`
	CompletedAt       *string `json:"completed_at,omitempty"`
}

type startProjectWorkAssignmentRequest struct {
	DriverKind string `json:"driver_kind,omitempty"`
}

type createProjectWorkArtifactRequest struct {
	ID           string `json:"id,omitempty"`
	AssignmentID string `json:"assignment_id,omitempty"`
	Kind         string `json:"kind"`
	Title        string `json:"title,omitempty"`
	Body         string `json:"body"`
	AuthorRoleID string `json:"author_role_id,omitempty"`
}

type ProjectWorkRolesResponse struct {
	Object string                    `json:"object"`
	Data   []ProjectWorkRoleResponse `json:"data"`
}

type ProjectWorkRoleResponse struct {
	ID           string `json:"id"`
	ProjectID    string `json:"project_id"`
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	Instructions string `json:"instructions,omitempty"`
	BuiltIn      bool   `json:"built_in"`
	CreatedAt    string `json:"created_at,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
}

type ProjectWorkRoleEnvelope struct {
	Object string                  `json:"object"`
	Data   ProjectWorkRoleResponse `json:"data"`
}

type ProjectWorkItemsResponse struct {
	Object string                    `json:"object"`
	Data   []ProjectWorkItemResponse `json:"data"`
}

type ProjectWorkItemEnvelope struct {
	Object string                  `json:"object"`
	Data   ProjectWorkItemResponse `json:"data"`
}

type ProjectWorkItemResponse struct {
	ID              string   `json:"id"`
	ProjectID       string   `json:"project_id"`
	Title           string   `json:"title"`
	Brief           string   `json:"brief,omitempty"`
	Status          string   `json:"status"`
	Priority        string   `json:"priority"`
	OwnerRoleID     string   `json:"owner_role_id,omitempty"`
	ReviewerRoleIDs []string `json:"reviewer_role_ids,omitempty"`
	CreatedAt       string   `json:"created_at"`
	UpdatedAt       string   `json:"updated_at"`
}

type ProjectWorkAssignmentExecutionResponse struct {
	TaskID               string `json:"task_id,omitempty"`
	RunID                string `json:"run_id,omitempty"`
	TaskStatus           string `json:"task_status,omitempty"`
	RunStatus            string `json:"run_status,omitempty"`
	Status               string `json:"status,omitempty"`
	PendingApprovalCount int    `json:"pending_approval_count,omitempty"`
	StepCount            int    `json:"step_count,omitempty"`
	ApprovalCount        int    `json:"approval_count,omitempty"`
	ArtifactCount        int    `json:"artifact_count,omitempty"`
	Model                string `json:"model,omitempty"`
	Provider             string `json:"provider,omitempty"`
	LastError            string `json:"last_error,omitempty"`
	StartedAt            string `json:"started_at,omitempty"`
	FinishedAt           string `json:"finished_at,omitempty"`
	TraceID              string `json:"trace_id,omitempty"`
	Missing              bool   `json:"missing,omitempty"`
}

type ProjectWorkAssignmentsResponse struct {
	Object string                          `json:"object"`
	Data   []ProjectWorkAssignmentResponse `json:"data"`
}

type ProjectWorkAssignmentEnvelope struct {
	Object string                        `json:"object"`
	Data   ProjectWorkAssignmentResponse `json:"data"`
}

type ProjectWorkAssignmentResponse struct {
	ID                string                                  `json:"id"`
	ProjectID         string                                  `json:"project_id"`
	WorkItemID        string                                  `json:"work_item_id"`
	RoleID            string                                  `json:"role_id"`
	DriverKind        string                                  `json:"driver_kind"`
	Status            string                                  `json:"status"`
	TaskID            string                                  `json:"task_id,omitempty"`
	RunID             string                                  `json:"run_id,omitempty"`
	ChatSessionID     string                                  `json:"chat_session_id,omitempty"`
	MessageID         string                                  `json:"message_id,omitempty"`
	ContextSnapshotID string                                  `json:"context_snapshot_id,omitempty"`
	CreatedAt         string                                  `json:"created_at"`
	UpdatedAt         string                                  `json:"updated_at"`
	StartedAt         string                                  `json:"started_at,omitempty"`
	CompletedAt       string                                  `json:"completed_at,omitempty"`
	Execution         *ProjectWorkAssignmentExecutionResponse `json:"execution,omitempty"`
}

type ProjectWorkArtifactsResponse struct {
	Object string                        `json:"object"`
	Data   []ProjectWorkArtifactResponse `json:"data"`
}

type ProjectWorkArtifactEnvelope struct {
	Object string                      `json:"object"`
	Data   ProjectWorkArtifactResponse `json:"data"`
}

type ProjectWorkArtifactResponse struct {
	ID           string `json:"id"`
	ProjectID    string `json:"project_id"`
	WorkItemID   string `json:"work_item_id"`
	AssignmentID string `json:"assignment_id,omitempty"`
	Kind         string `json:"kind"`
	Title        string `json:"title,omitempty"`
	Body         string `json:"body"`
	AuthorRoleID string `json:"author_role_id,omitempty"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

func (h *Handler) HandleProjectWorkRoles(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if !h.requireProject(w, r, projectID) {
		return
	}
	roles, err := h.projectWork.ListRoles(r.Context(), projectID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	data := make([]ProjectWorkRoleResponse, 0, len(roles))
	for _, role := range roles {
		data = append(data, renderProjectWorkRole(role))
	}
	WriteJSON(w, http.StatusOK, ProjectWorkRolesResponse{Object: "project_roles", Data: data})
}

func (h *Handler) HandleCreateProjectWorkRole(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if !h.requireProject(w, r, projectID) {
		return
	}
	var req createProjectWorkRoleRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	id := strings.TrimSpace(req.ID)
	if id == "" {
		id = newOpaqueTaskResourceID("role")
	}
	role, err := h.projectWork.CreateRole(r.Context(), projectwork.AgentRoleProfile{
		ID:           id,
		ProjectID:    projectID,
		Name:         req.Name,
		Description:  req.Description,
		Instructions: req.Instructions,
	})
	if !writeProjectWorkError(w, err) {
		return
	}
	WriteJSON(w, http.StatusCreated, ProjectWorkRoleEnvelope{Object: "project_role", Data: renderProjectWorkRole(role)})
}

func (h *Handler) HandleUpdateProjectWorkRole(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if !h.requireProject(w, r, projectID) {
		return
	}
	var req updateProjectWorkRoleRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	role, err := h.projectWork.UpdateRole(r.Context(), projectID, r.PathValue("role_id"), func(item *projectwork.AgentRoleProfile) {
		if req.Name != nil {
			item.Name = *req.Name
		}
		if req.Description != nil {
			item.Description = *req.Description
		}
		if req.Instructions != nil {
			item.Instructions = *req.Instructions
		}
	})
	if !writeProjectWorkError(w, err) {
		return
	}
	WriteJSON(w, http.StatusOK, ProjectWorkRoleEnvelope{Object: "project_role", Data: renderProjectWorkRole(role)})
}

func (h *Handler) HandleDeleteProjectWorkRole(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if !h.requireProject(w, r, projectID) {
		return
	}
	if err := h.projectWork.DeleteRole(r.Context(), projectID, r.PathValue("role_id")); !writeProjectWorkError(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) HandleProjectWorkItems(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if !h.requireProject(w, r, projectID) {
		return
	}
	items, err := h.projectWork.ListWorkItems(r.Context(), projectID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	assignments, err := h.projectWork.ListAssignments(r.Context(), projectwork.AssignmentFilter{ProjectID: projectID})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	assignmentsByWorkItem := groupProjectWorkAssignmentsByWorkItem(assignments)
	data := make([]ProjectWorkItemResponse, 0, len(items))
	for _, item := range items {
		projected, err := h.renderProjectedProjectWorkItemWithAssignments(r.Context(), item, assignmentsByWorkItem[item.ID])
		if err != nil {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
			return
		}
		data = append(data, projected)
	}
	WriteJSON(w, http.StatusOK, ProjectWorkItemsResponse{Object: "project_work_items", Data: data})
}

func (h *Handler) HandleCreateProjectWorkItem(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if !h.requireProject(w, r, projectID) {
		return
	}
	var req createProjectWorkItemRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	id := strings.TrimSpace(req.ID)
	if id == "" {
		id = newOpaqueTaskResourceID("work")
	}
	item, err := h.projectWork.CreateWorkItem(r.Context(), projectwork.WorkItem{
		ID:              id,
		ProjectID:       projectID,
		Title:           req.Title,
		Brief:           req.Brief,
		Status:          req.Status,
		Priority:        req.Priority,
		OwnerRoleID:     req.OwnerRoleID,
		ReviewerRoleIDs: req.ReviewerRoleIDs,
	})
	if !writeProjectWorkError(w, err) {
		return
	}
	projected, err := h.renderProjectedProjectWorkItem(r.Context(), item)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusCreated, ProjectWorkItemEnvelope{Object: "project_work_item", Data: projected})
}

func (h *Handler) HandleProjectWorkItem(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if !h.requireProject(w, r, projectID) {
		return
	}
	item, ok, err := h.projectWork.GetWorkItem(r.Context(), projectID, r.PathValue("work_item_id"))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "work item not found")
		return
	}
	projected, err := h.renderProjectedProjectWorkItem(r.Context(), item)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, ProjectWorkItemEnvelope{Object: "project_work_item", Data: projected})
}

func (h *Handler) HandleUpdateProjectWorkItem(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if !h.requireProject(w, r, projectID) {
		return
	}
	var req updateProjectWorkItemRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	item, err := h.projectWork.UpdateWorkItem(r.Context(), projectID, r.PathValue("work_item_id"), func(item *projectwork.WorkItem) {
		if req.Title != nil {
			item.Title = *req.Title
		}
		if req.Brief != nil {
			item.Brief = *req.Brief
		}
		if req.Status != nil {
			item.Status = *req.Status
		}
		if req.Priority != nil {
			item.Priority = *req.Priority
		}
		if req.OwnerRoleID != nil {
			item.OwnerRoleID = *req.OwnerRoleID
		}
		if req.ReviewerRoleIDs != nil {
			item.ReviewerRoleIDs = *req.ReviewerRoleIDs
		}
	})
	if !writeProjectWorkError(w, err) {
		return
	}
	projected, err := h.renderProjectedProjectWorkItem(r.Context(), item)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, ProjectWorkItemEnvelope{Object: "project_work_item", Data: projected})
}

func (h *Handler) HandleDeleteProjectWorkItem(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if !h.requireProject(w, r, projectID) {
		return
	}
	if err := h.projectWork.DeleteWorkItem(r.Context(), projectID, r.PathValue("work_item_id")); !writeProjectWorkError(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) HandleProjectWorkAssignments(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	workItemID := r.PathValue("work_item_id")
	if !h.requireProjectWorkItem(w, r, projectID, workItemID) {
		return
	}
	items, err := h.projectWork.ListAssignments(r.Context(), projectwork.AssignmentFilter{ProjectID: projectID, WorkItemID: workItemID})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	data := make([]ProjectWorkAssignmentResponse, 0, len(items))
	for _, item := range items {
		projected, err := h.renderProjectedProjectWorkAssignment(r.Context(), item)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
			return
		}
		data = append(data, projected)
	}
	WriteJSON(w, http.StatusOK, ProjectWorkAssignmentsResponse{Object: "project_assignments", Data: data})
}

func (h *Handler) HandleCreateProjectWorkAssignment(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	workItemID := r.PathValue("work_item_id")
	if !h.requireProjectWorkItem(w, r, projectID, workItemID) {
		return
	}
	var req createProjectWorkAssignmentRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	startedAt, completedAt, ok := parseProjectWorkRequestTimes(w, req.StartedAt, req.CompletedAt)
	if !ok {
		return
	}
	id := strings.TrimSpace(req.ID)
	if id == "" {
		id = newOpaqueTaskResourceID("asgn")
	}
	item, err := h.projectWork.CreateAssignment(r.Context(), projectwork.Assignment{
		ID:                id,
		ProjectID:         projectID,
		WorkItemID:        workItemID,
		RoleID:            req.RoleID,
		DriverKind:        req.DriverKind,
		Status:            req.Status,
		TaskID:            req.TaskID,
		RunID:             req.RunID,
		ChatSessionID:     req.ChatSessionID,
		MessageID:         req.MessageID,
		ContextSnapshotID: req.ContextSnapshotID,
		StartedAt:         startedAt,
		CompletedAt:       completedAt,
	})
	if !writeProjectWorkError(w, err) {
		return
	}
	projected, err := h.renderProjectedProjectWorkAssignment(r.Context(), item)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusCreated, ProjectWorkAssignmentEnvelope{Object: "project_assignment", Data: projected})
}

func (h *Handler) HandleUpdateProjectWorkAssignment(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	workItemID := r.PathValue("work_item_id")
	assignmentID := r.PathValue("assignment_id")
	if !h.requireProjectAssignment(w, r, projectID, workItemID, assignmentID) {
		return
	}
	var req updateProjectWorkAssignmentRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	startedAt, completedAt, ok := parseProjectWorkOptionalRequestTimes(w, req.StartedAt, req.CompletedAt)
	if !ok {
		return
	}
	item, err := h.projectWork.UpdateAssignment(r.Context(), projectID, assignmentID, func(item *projectwork.Assignment) {
		if req.RoleID != nil {
			item.RoleID = *req.RoleID
		}
		if req.DriverKind != nil {
			item.DriverKind = *req.DriverKind
		}
		if req.Status != nil {
			item.Status = *req.Status
		}
		if req.TaskID != nil {
			item.TaskID = *req.TaskID
		}
		if req.RunID != nil {
			item.RunID = *req.RunID
		}
		if req.ChatSessionID != nil {
			item.ChatSessionID = *req.ChatSessionID
		}
		if req.MessageID != nil {
			item.MessageID = *req.MessageID
		}
		if req.ContextSnapshotID != nil {
			item.ContextSnapshotID = *req.ContextSnapshotID
		}
		if req.StartedAt != nil {
			item.StartedAt = startedAt
		}
		if req.CompletedAt != nil {
			item.CompletedAt = completedAt
		}
	})
	if !writeProjectWorkError(w, err) {
		return
	}
	projected, err := h.renderProjectedProjectWorkAssignment(r.Context(), item)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, ProjectWorkAssignmentEnvelope{Object: "project_assignment", Data: projected})
}

func (h *Handler) HandleDeleteProjectWorkAssignment(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	workItemID := r.PathValue("work_item_id")
	assignmentID := r.PathValue("assignment_id")
	if !h.requireProjectAssignment(w, r, projectID, workItemID, assignmentID) {
		return
	}
	if err := h.projectWork.DeleteAssignment(r.Context(), projectID, workItemID, assignmentID); !writeProjectWorkError(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) HandleStartProjectWorkAssignment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectID := r.PathValue("id")
	workItemID := r.PathValue("work_item_id")
	assignmentID := r.PathValue("assignment_id")
	if h.taskStore == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "task store is not configured")
		return
	}
	if h.taskRunner == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "task runner is not configured")
		return
	}
	if h.projects == nil || h.projectWork == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "project stores are not configured")
		return
	}
	req, ok := decodeOptionalProjectWorkAssignmentStartRequest(w, r)
	if !ok {
		return
	}

	project, ok, err := h.projects.Get(ctx, projectID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project not found")
		return
	}
	workItem, ok, err := h.projectWork.GetWorkItem(ctx, projectID, workItemID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "work item not found")
		return
	}
	assignment, ok, err := h.loadProjectWorkAssignment(ctx, projectID, workItemID, assignmentID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "assignment not found")
		return
	}
	role, ok, err := h.loadProjectWorkRole(ctx, projectID, assignment.RoleID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "assignment role not found")
		return
	}
	if driver := strings.TrimSpace(req.DriverKind); driver != "" && driver != assignment.DriverKind {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, fmt.Sprintf("assignment driver_kind is %q, not %q", assignment.DriverKind, driver))
		return
	}
	if assignment.DriverKind != projectwork.AssignmentDriverHecateTask {
		WriteError(w, http.StatusConflict, errCodeConflict, fmt.Sprintf("assignment driver_kind %q is not supported by native start; V1 supports %q only", assignment.DriverKind, projectwork.AssignmentDriverHecateTask))
		return
	}
	if projectWorkAssignmentIsTerminal(assignment.Status) {
		projected, projectErr := h.renderProjectedProjectWorkAssignment(ctx, assignment)
		if projectErr != nil {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, projectErr.Error())
			return
		}
		WriteJSON(w, http.StatusConflict, ProjectWorkAssignmentEnvelope{Object: "project_assignment", Data: projected})
		return
	}
	active, err := projectWorkAssignmentHasActiveExecution(ctx, h.taskStore, assignment)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if active {
		projected, projectErr := h.renderProjectedProjectWorkAssignment(ctx, assignment)
		if projectErr != nil {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, projectErr.Error())
			return
		}
		WriteJSON(w, http.StatusConflict, ProjectWorkAssignmentEnvelope{Object: "project_assignment", Data: projected})
		return
	}

	workingDirectory, workspaceMode, err := resolveProjectAssignmentWorkspace(project)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	requestedModel := strings.TrimSpace(firstNonEmpty(project.DefaultModel, h.config.Router.DefaultModel))
	if requestedModel == "" {
		WriteError(w, http.StatusUnprocessableEntity, errCodeModelNotConfigured, "project assignment start requires a default model")
		return
	}

	taskID := newTaskID()
	claimRejected := false
	assignment, err = h.projectWork.UpdateAssignment(ctx, projectID, assignmentID, func(item *projectwork.Assignment) {
		if item.TaskID != "" || item.RunID != "" || projectWorkAssignmentIsTerminal(item.Status) || item.DriverKind != projectwork.AssignmentDriverHecateTask {
			claimRejected = true
			return
		}
		item.TaskID = taskID
		item.Status = projectwork.AssignmentStatusQueued
		if item.StartedAt.IsZero() {
			item.StartedAt = time.Now().UTC()
		}
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if claimRejected {
		projected, projectErr := h.renderProjectedProjectWorkAssignment(ctx, assignment)
		if projectErr != nil {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, projectErr.Error())
			return
		}
		WriteJSON(w, http.StatusConflict, ProjectWorkAssignmentEnvelope{Object: "project_assignment", Data: projected})
		return
	}

	task, err := h.createProjectAssignmentTask(ctx, taskID, project, workItem, assignment, role, workingDirectory, workspaceMode, requestedModel)
	if err != nil {
		assignment, updateErr := h.projectWork.UpdateAssignment(ctx, projectID, assignmentID, func(item *projectwork.Assignment) {
			if item.TaskID == taskID && item.RunID == "" {
				item.TaskID = ""
				item.Status = projectwork.AssignmentStatusQueued
				item.StartedAt = time.Time{}
				item.CompletedAt = time.Time{}
			}
		})
		if updateErr != nil {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, fmt.Sprintf("%s; assignment status update failed: %v", err.Error(), updateErr))
			return
		}
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, fmt.Sprintf("task could not be created for assignment %s: %s", assignment.ID, err.Error()))
		return
	}

	result, err := h.taskRunner.StartTask(ctx, task, newOpaqueTaskResourceID)
	if err != nil {
		assignment, updateErr := h.projectWork.UpdateAssignment(ctx, projectID, assignmentID, func(item *projectwork.Assignment) {
			item.TaskID = task.ID
			item.Status = projectwork.AssignmentStatusFailed
			item.CompletedAt = time.Now().UTC()
		})
		if updateErr != nil {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, fmt.Sprintf("%s; assignment status update failed: %v", err.Error(), updateErr))
			return
		}
		if errors.Is(err, orchestrator.ErrAgentLoopMisconfigured) {
			WriteError(w, http.StatusUnprocessableEntity, errCodeModelNotConfigured, err.Error())
			return
		}
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, fmt.Sprintf("task %s was created but start failed: %s", assignment.TaskID, err.Error()))
		return
	}
	if result.TraceID != "" {
		w.Header().Set("X-Trace-Id", result.TraceID)
	}
	if result.SpanID != "" {
		w.Header().Set("X-Span-Id", result.SpanID)
	}
	assignment, err = h.projectWork.UpdateAssignment(ctx, projectID, assignmentID, func(item *projectwork.Assignment) {
		item.TaskID = result.Task.ID
		item.RunID = result.Run.ID
		item.Status = projectWorkAssignmentStatusFromRun(result.Run.Status)
		if item.StartedAt.IsZero() {
			item.StartedAt = time.Now().UTC()
		}
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	projected, err := h.renderProjectedProjectWorkAssignment(ctx, assignment)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, ProjectWorkAssignmentEnvelope{Object: "project_assignment", Data: projected})
}

func decodeOptionalProjectWorkAssignmentStartRequest(w http.ResponseWriter, r *http.Request) (startProjectWorkAssignmentRequest, bool) {
	var req startProjectWorkAssignmentRequest
	if r.Body == nil || r.Body == http.NoBody {
		return req, true
	}
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&req); err != nil {
		if errors.Is(err, io.EOF) {
			return req, true
		}
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "request body must be valid JSON")
		return startProjectWorkAssignmentRequest{}, false
	}
	return req, true
}

func (h *Handler) loadProjectWorkAssignment(ctx context.Context, projectID, workItemID, assignmentID string) (projectwork.Assignment, bool, error) {
	items, err := h.projectWork.ListAssignments(ctx, projectwork.AssignmentFilter{ProjectID: projectID, WorkItemID: workItemID})
	if err != nil {
		return projectwork.Assignment{}, false, err
	}
	for _, item := range items {
		if item.ID == strings.TrimSpace(assignmentID) {
			return item, true, nil
		}
	}
	return projectwork.Assignment{}, false, nil
}

func (h *Handler) loadProjectWorkRole(ctx context.Context, projectID, roleID string) (projectwork.AgentRoleProfile, bool, error) {
	roles, err := h.projectWork.ListRoles(ctx, projectID)
	if err != nil {
		return projectwork.AgentRoleProfile{}, false, err
	}
	roleID = strings.TrimSpace(roleID)
	for _, role := range roles {
		if role.ID == roleID {
			return role, true, nil
		}
	}
	return projectwork.AgentRoleProfile{}, false, nil
}

func projectWorkAssignmentIsTerminal(status string) bool {
	switch strings.TrimSpace(status) {
	case projectwork.AssignmentStatusCompleted, projectwork.AssignmentStatusFailed, projectwork.AssignmentStatusCancelled:
		return true
	default:
		return false
	}
}

func projectWorkAssignmentHasActiveExecution(ctx context.Context, store taskRunLookupStore, assignment projectwork.Assignment) (bool, error) {
	if strings.TrimSpace(assignment.RunID) != "" && strings.TrimSpace(assignment.TaskID) != "" && store != nil {
		run, found, err := store.GetRun(ctx, assignment.TaskID, assignment.RunID)
		if err != nil {
			return false, err
		}
		if found {
			return !types.IsTerminalTaskRunStatus(run.Status), nil
		}
	}
	switch strings.TrimSpace(assignment.Status) {
	case projectwork.AssignmentStatusRunning, projectwork.AssignmentStatusAwaitingApproval:
		return true, nil
	case projectwork.AssignmentStatusQueued:
		return strings.TrimSpace(assignment.TaskID) != "" || strings.TrimSpace(assignment.RunID) != "", nil
	default:
		return false, nil
	}
}

type taskRunLookupStore interface {
	GetRun(ctx context.Context, taskID, runID string) (types.TaskRun, bool, error)
}

func resolveProjectAssignmentWorkspace(project projects.Project) (string, string, error) {
	root, ok := selectProjectAssignmentRoot(project)
	if !ok {
		return "", "", fmt.Errorf("project has no workspace root; add a project root before starting an assignment")
	}
	path := strings.TrimSpace(root.Path)
	if path == "" {
		return "", "", fmt.Errorf("project root %q has no path", root.ID)
	}
	if !filepath.IsAbs(path) {
		return "", "", fmt.Errorf("project root %q path must be absolute", root.ID)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", "", fmt.Errorf("project root %q is not accessible: %w", root.ID, err)
	}
	if !info.IsDir() {
		return "", "", fmt.Errorf("project root %q is not a directory", root.ID)
	}
	workspaceMode := strings.TrimSpace(project.DefaultWorkspaceMode)
	if workspaceMode == "" {
		workspaceMode = "ephemeral"
	}
	return path, workspaceMode, nil
}

func selectProjectAssignmentRoot(project projects.Project) (projects.Root, bool) {
	defaultRootID := strings.TrimSpace(project.DefaultRootID)
	if defaultRootID != "" {
		for _, root := range project.Roots {
			if root.ID == defaultRootID {
				return root, true
			}
		}
	}
	for _, root := range project.Roots {
		if root.Active {
			return root, true
		}
	}
	if len(project.Roots) > 0 {
		return project.Roots[0], true
	}
	return projects.Root{}, false
}

func (h *Handler) createProjectAssignmentTask(ctx context.Context, taskID string, project projects.Project, workItem projectwork.WorkItem, assignment projectwork.Assignment, role projectwork.AgentRoleProfile, workingDirectory, workspaceMode, requestedModel string) (types.Task, error) {
	now := time.Now().UTC()
	task := types.Task{
		ID:                 taskID,
		Title:              projectAssignmentTaskTitle(workItem, role),
		Prompt:             projectAssignmentPrompt(project, workItem, assignment, role),
		SystemPrompt:       projectAssignmentSystemPrompt(project, role),
		ExecutionKind:      "agent_loop",
		ExecutionProfile:   "project_assignment",
		OriginKind:         "project_work_item",
		OriginID:           workItem.ID,
		WorkspaceMode:      workspaceMode,
		WorkingDirectory:   workingDirectory,
		SandboxAllowedRoot: workingDirectory,
		Status:             "queued",
		Priority:           firstNonEmpty(workItem.Priority, "normal"),
		RequestedProvider:  strings.TrimSpace(project.DefaultProvider),
		RequestedModel:     requestedModel,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	return h.taskStore.CreateTask(ctx, task)
}

func projectAssignmentTaskTitle(workItem projectwork.WorkItem, role projectwork.AgentRoleProfile) string {
	title := strings.TrimSpace(workItem.Title)
	roleName := strings.TrimSpace(role.Name)
	switch {
	case title != "" && roleName != "":
		return title + " - " + roleName
	case title != "":
		return title
	case roleName != "":
		return roleName + " assignment"
	default:
		return "Project work assignment"
	}
}

func projectAssignmentPrompt(project projects.Project, workItem projectwork.WorkItem, assignment projectwork.Assignment, role projectwork.AgentRoleProfile) string {
	sections := []string{
		"Project: " + firstNonEmpty(project.Name, project.ID),
		"Work item: " + firstNonEmpty(workItem.Title, workItem.ID),
	}
	if brief := strings.TrimSpace(workItem.Brief); brief != "" {
		sections = append(sections, "Work item brief:\n"+brief)
	}
	if roleName := strings.TrimSpace(role.Name); roleName != "" {
		sections = append(sections, "Assigned role: "+roleName)
	}
	if description := strings.TrimSpace(role.Description); description != "" {
		sections = append(sections, "Role description:\n"+description)
	}
	if instructions := strings.TrimSpace(role.Instructions); instructions != "" {
		sections = append(sections, "Role instructions:\n"+instructions)
	}
	sections = append(sections,
		"Assignment ID: "+assignment.ID,
		"Execute this assignment as a native Hecate agent_loop task. Keep outputs and artifacts linked to this work item.",
	)
	return strings.Join(sections, "\n\n")
}

func projectAssignmentSystemPrompt(project projects.Project, role projectwork.AgentRoleProfile) string {
	var parts []string
	if prompt := strings.TrimSpace(project.DefaultSystemPrompt); prompt != "" {
		parts = append(parts, prompt)
	}
	if instructions := strings.TrimSpace(role.Instructions); instructions != "" {
		parts = append(parts, instructions)
	} else if role.Name != "" {
		parts = append(parts, "Act as the "+strings.TrimSpace(role.Name)+" for this project work assignment.")
	}
	return strings.Join(parts, "\n\n")
}

func projectWorkAssignmentStatusFromRun(status string) string {
	switch strings.TrimSpace(status) {
	case "awaiting_approval":
		return projectwork.AssignmentStatusAwaitingApproval
	case "running":
		return projectwork.AssignmentStatusRunning
	case "completed":
		return projectwork.AssignmentStatusCompleted
	case "failed":
		return projectwork.AssignmentStatusFailed
	case "cancelled":
		return projectwork.AssignmentStatusCancelled
	default:
		return projectwork.AssignmentStatusQueued
	}
}

func (h *Handler) HandleProjectWorkArtifacts(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	workItemID := r.PathValue("work_item_id")
	if !h.requireProjectWorkItem(w, r, projectID, workItemID) {
		return
	}
	items, err := h.projectWork.ListArtifacts(r.Context(), projectwork.ArtifactFilter{ProjectID: projectID, WorkItemID: workItemID})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	data := make([]ProjectWorkArtifactResponse, 0, len(items))
	for _, item := range items {
		data = append(data, renderProjectWorkArtifact(item))
	}
	WriteJSON(w, http.StatusOK, ProjectWorkArtifactsResponse{Object: "project_collaboration_artifacts", Data: data})
}

func (h *Handler) HandleCreateProjectWorkArtifact(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	workItemID := r.PathValue("work_item_id")
	if !h.requireProjectWorkItem(w, r, projectID, workItemID) {
		return
	}
	var req createProjectWorkArtifactRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	id := strings.TrimSpace(req.ID)
	if id == "" {
		id = newOpaqueTaskResourceID("art")
	}
	item, err := h.projectWork.CreateArtifact(r.Context(), projectwork.CollaborationArtifact{
		ID:           id,
		ProjectID:    projectID,
		WorkItemID:   workItemID,
		AssignmentID: req.AssignmentID,
		Kind:         req.Kind,
		Title:        req.Title,
		Body:         req.Body,
		AuthorRoleID: req.AuthorRoleID,
	})
	if !writeProjectWorkError(w, err) {
		return
	}
	WriteJSON(w, http.StatusCreated, ProjectWorkArtifactEnvelope{Object: "project_collaboration_artifact", Data: renderProjectWorkArtifact(item)})
}

func (h *Handler) requireProject(w http.ResponseWriter, r *http.Request, projectID string) bool {
	_, ok, err := h.projects.Get(r.Context(), projectID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return false
	}
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project not found")
		return false
	}
	return true
}

func (h *Handler) requireProjectWorkItem(w http.ResponseWriter, r *http.Request, projectID, workItemID string) bool {
	if !h.requireProject(w, r, projectID) {
		return false
	}
	_, ok, err := h.projectWork.GetWorkItem(r.Context(), projectID, workItemID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return false
	}
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "work item not found")
		return false
	}
	return true
}

func (h *Handler) requireProjectAssignment(w http.ResponseWriter, r *http.Request, projectID, workItemID, assignmentID string) bool {
	if !h.requireProjectWorkItem(w, r, projectID, workItemID) {
		return false
	}
	assignments, err := h.projectWork.ListAssignments(r.Context(), projectwork.AssignmentFilter{ProjectID: projectID, WorkItemID: workItemID})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return false
	}
	for _, item := range assignments {
		if item.ID == assignmentID {
			return true
		}
	}
	WriteError(w, http.StatusNotFound, errCodeNotFound, "assignment not found")
	return false
}

func writeProjectWorkError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return true
	}
	switch {
	case errors.Is(err, projectwork.ErrNotFound):
		WriteError(w, http.StatusNotFound, errCodeNotFound, err.Error())
	case errors.Is(err, projectwork.ErrInvalid):
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
	case errors.Is(err, projectwork.ErrBuiltInRole), errors.Is(err, projectwork.ErrDuplicateRole), errors.Is(err, projectwork.ErrDuplicate):
		WriteError(w, http.StatusConflict, errCodeConflict, err.Error())
	default:
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
	}
	return false
}

func parseProjectWorkRequestTimes(w http.ResponseWriter, startedRaw, completedRaw string) (time.Time, time.Time, bool) {
	startedAt, err := parseOptionalProjectWorkTime(startedRaw, "started_at")
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return time.Time{}, time.Time{}, false
	}
	completedAt, err := parseOptionalProjectWorkTime(completedRaw, "completed_at")
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return time.Time{}, time.Time{}, false
	}
	return startedAt, completedAt, true
}

func parseProjectWorkOptionalRequestTimes(w http.ResponseWriter, startedRaw, completedRaw *string) (time.Time, time.Time, bool) {
	var startedAt time.Time
	var completedAt time.Time
	if startedRaw != nil {
		parsed, err := parseOptionalProjectWorkTime(*startedRaw, "started_at")
		if err != nil {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
			return time.Time{}, time.Time{}, false
		}
		startedAt = parsed
	}
	if completedRaw != nil {
		parsed, err := parseOptionalProjectWorkTime(*completedRaw, "completed_at")
		if err != nil {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
			return time.Time{}, time.Time{}, false
		}
		completedAt = parsed
	}
	return startedAt, completedAt, true
}

func parseOptionalProjectWorkTime(value, field string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, errors.New(field + " must be RFC3339 timestamp")
	}
	return parsed.UTC(), nil
}

func (h *Handler) renderProjectedProjectWorkItem(ctx context.Context, item projectwork.WorkItem) (ProjectWorkItemResponse, error) {
	if h == nil || h.projectWork == nil {
		return renderProjectWorkItem(item), nil
	}
	assignments, err := h.projectWork.ListAssignments(ctx, projectwork.AssignmentFilter{
		ProjectID:  item.ProjectID,
		WorkItemID: item.ID,
	})
	if err != nil {
		return ProjectWorkItemResponse{}, err
	}
	return h.renderProjectedProjectWorkItemWithAssignments(ctx, item, assignments)
}

func (h *Handler) renderProjectedProjectWorkItemWithAssignments(ctx context.Context, item projectwork.WorkItem, assignments []projectwork.Assignment) (ProjectWorkItemResponse, error) {
	response := renderProjectWorkItem(item)
	if len(assignments) == 0 {
		return response, nil
	}
	projected := make([]ProjectWorkAssignmentResponse, 0, len(assignments))
	for _, assignment := range assignments {
		projectedAssignment, err := h.renderProjectedProjectWorkAssignment(ctx, assignment)
		if err != nil {
			return ProjectWorkItemResponse{}, err
		}
		projected = append(projected, projectedAssignment)
	}
	response.Status = projectWorkItemStatusFromAssignments(item.Status, projected)
	return response, nil
}

func (h *Handler) renderProjectedProjectWorkAssignment(ctx context.Context, item projectwork.Assignment) (ProjectWorkAssignmentResponse, error) {
	response := renderProjectWorkAssignment(item)
	projection, err := h.projectWorkAssignmentExecution(ctx, item)
	if err != nil {
		return ProjectWorkAssignmentResponse{}, err
	}
	if projection == nil {
		return response, nil
	}
	response.Execution = &projection.Execution
	if projection.Status != "" {
		response.Status = projection.Status
	}
	if response.StartedAt == "" && !projection.StartedAt.IsZero() {
		response.StartedAt = formatOptionalTime(projection.StartedAt)
	}
	if response.CompletedAt == "" && !projection.CompletedAt.IsZero() {
		response.CompletedAt = formatOptionalTime(projection.CompletedAt)
	}
	return response, nil
}

type projectWorkAssignmentExecutionProjection struct {
	Execution   ProjectWorkAssignmentExecutionResponse
	Status      string
	StartedAt   time.Time
	CompletedAt time.Time
}

func (h *Handler) projectWorkAssignmentExecution(ctx context.Context, assignment projectwork.Assignment) (*projectWorkAssignmentExecutionProjection, error) {
	taskID := strings.TrimSpace(assignment.TaskID)
	runID := strings.TrimSpace(assignment.RunID)
	if taskID == "" {
		return nil, nil
	}
	projection := &projectWorkAssignmentExecutionProjection{
		Status:    assignment.Status,
		StartedAt: assignment.StartedAt,
		Execution: ProjectWorkAssignmentExecutionResponse{
			TaskID: taskID,
			RunID:  runID,
		},
	}
	if h == nil || h.taskStore == nil {
		projection.Execution.Missing = true
		return projection, nil
	}

	var task types.Task
	if taskID != "" {
		foundTask, found, err := h.taskStore.GetTask(ctx, taskID)
		if err != nil {
			return nil, err
		}
		if !found {
			projection.Execution.Missing = true
			return projection, nil
		}
		task = foundTask
		projection.Execution.TaskStatus = task.Status
		if runID == "" {
			runID = strings.TrimSpace(task.LatestRunID)
			projection.Execution.RunID = runID
		}
	}

	if runID == "" {
		status := projectWorkAssignmentStatusFromRun(task.Status)
		projection.Execution.Status = status
		projection.Status = projectWorkProjectedAssignmentStatus(assignment, status, task.UpdatedAt)
		return projection, nil
	}

	run, found, err := h.taskStore.GetRun(ctx, taskID, runID)
	if err != nil {
		return nil, err
	}
	if !found {
		projection.Execution.Missing = true
		return projection, nil
	}

	status := projectWorkAssignmentStatusFromRun(run.Status)
	pendingApprovalCount := 0
	if status == projectwork.AssignmentStatusAwaitingApproval {
		pendingCount, err := projectWorkPendingApprovalCount(ctx, h.taskStore, taskID, runID)
		if err != nil {
			return nil, err
		}
		pendingApprovalCount = pendingCount
	}
	projection.Status = projectWorkProjectedAssignmentStatus(assignment, status, projectWorkRunProjectionTime(run))
	projection.StartedAt = firstNonZeroTime(assignment.StartedAt, run.StartedAt)
	if types.IsTerminalTaskRunStatus(run.Status) {
		projection.CompletedAt = firstNonZeroTime(assignment.CompletedAt, run.FinishedAt)
	} else {
		projection.CompletedAt = assignment.CompletedAt
	}
	projection.Execution = ProjectWorkAssignmentExecutionResponse{
		TaskID:               taskID,
		RunID:                runID,
		TaskStatus:           task.Status,
		RunStatus:            run.Status,
		Status:               status,
		PendingApprovalCount: pendingApprovalCount,
		StepCount:            run.StepCount,
		ApprovalCount:        run.ApprovalCount,
		ArtifactCount:        run.ArtifactCount,
		Model:                run.Model,
		Provider:             run.Provider,
		LastError:            run.LastError,
		StartedAt:            formatOptionalTime(run.StartedAt),
		FinishedAt:           formatOptionalTime(run.FinishedAt),
		TraceID:              run.TraceID,
	}
	return projection, nil
}

func groupProjectWorkAssignmentsByWorkItem(assignments []projectwork.Assignment) map[string][]projectwork.Assignment {
	if len(assignments) == 0 {
		return map[string][]projectwork.Assignment{}
	}
	grouped := make(map[string][]projectwork.Assignment, len(assignments))
	for _, assignment := range assignments {
		grouped[assignment.WorkItemID] = append(grouped[assignment.WorkItemID], assignment)
	}
	return grouped
}

func projectWorkProjectedAssignmentStatus(assignment projectwork.Assignment, projectedStatus string, projectedAt time.Time) string {
	projectedStatus = strings.TrimSpace(projectedStatus)
	if projectedStatus == "" {
		return assignment.Status
	}
	if projectWorkAssignmentIsTerminal(assignment.Status) && assignment.Status != projectedStatus {
		if projectedAt.IsZero() || !projectedAt.After(assignment.UpdatedAt) {
			return assignment.Status
		}
	}
	return projectedStatus
}

func projectWorkRunProjectionTime(run types.TaskRun) time.Time {
	if !run.FinishedAt.IsZero() {
		return run.FinishedAt
	}
	return run.StartedAt
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

func projectWorkPendingApprovalCount(ctx context.Context, store taskRunApprovalStore, taskID, runID string) (int, error) {
	if store == nil {
		return 0, nil
	}
	approvals, err := store.ListApprovals(ctx, taskID)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, approval := range approvals {
		if approval.RunID == runID && approval.Status == "pending" {
			count++
		}
	}
	return count, nil
}

type taskRunApprovalStore interface {
	ListApprovals(ctx context.Context, taskID string) ([]types.TaskApproval, error)
}

func projectWorkItemStatusFromAssignments(storedStatus string, assignments []ProjectWorkAssignmentResponse) string {
	if len(assignments) == 0 {
		return storedStatus
	}
	allCompleted := true
	allCancelled := true
	hasFailedOrCancelled := false
	for _, assignment := range assignments {
		switch assignment.Status {
		case projectwork.AssignmentStatusRunning, projectwork.AssignmentStatusAwaitingApproval:
			return projectwork.WorkItemStatusRunning
		case projectwork.AssignmentStatusQueued:
			if assignment.Execution != nil && !assignment.Execution.Missing && (assignment.Execution.RunID != "" || assignment.Execution.TaskID != "") {
				return projectwork.WorkItemStatusRunning
			}
			allCompleted = false
			allCancelled = false
		case projectwork.AssignmentStatusCompleted:
			allCancelled = false
		case projectwork.AssignmentStatusFailed:
			allCompleted = false
			allCancelled = false
			hasFailedOrCancelled = true
		case projectwork.AssignmentStatusCancelled:
			allCompleted = false
			hasFailedOrCancelled = true
		default:
			allCompleted = false
			allCancelled = false
		}
	}
	switch {
	case allCompleted:
		return projectwork.WorkItemStatusDone
	case allCancelled:
		return projectwork.WorkItemStatusCancelled
	case hasFailedOrCancelled:
		return projectwork.WorkItemStatusBlocked
	default:
		return storedStatus
	}
}

func renderProjectWorkRole(item projectwork.AgentRoleProfile) ProjectWorkRoleResponse {
	return ProjectWorkRoleResponse{
		ID:           item.ID,
		ProjectID:    item.ProjectID,
		Name:         item.Name,
		Description:  item.Description,
		Instructions: item.Instructions,
		BuiltIn:      item.BuiltIn,
		CreatedAt:    formatOptionalTime(item.CreatedAt),
		UpdatedAt:    formatOptionalTime(item.UpdatedAt),
	}
}

func renderProjectWorkItem(item projectwork.WorkItem) ProjectWorkItemResponse {
	return ProjectWorkItemResponse{
		ID:              item.ID,
		ProjectID:       item.ProjectID,
		Title:           item.Title,
		Brief:           item.Brief,
		Status:          item.Status,
		Priority:        item.Priority,
		OwnerRoleID:     item.OwnerRoleID,
		ReviewerRoleIDs: append([]string(nil), item.ReviewerRoleIDs...),
		CreatedAt:       formatOptionalTime(item.CreatedAt),
		UpdatedAt:       formatOptionalTime(item.UpdatedAt),
	}
}

func renderProjectWorkAssignment(item projectwork.Assignment) ProjectWorkAssignmentResponse {
	return ProjectWorkAssignmentResponse{
		ID:                item.ID,
		ProjectID:         item.ProjectID,
		WorkItemID:        item.WorkItemID,
		RoleID:            item.RoleID,
		DriverKind:        item.DriverKind,
		Status:            item.Status,
		TaskID:            item.TaskID,
		RunID:             item.RunID,
		ChatSessionID:     item.ChatSessionID,
		MessageID:         item.MessageID,
		ContextSnapshotID: item.ContextSnapshotID,
		CreatedAt:         formatOptionalTime(item.CreatedAt),
		UpdatedAt:         formatOptionalTime(item.UpdatedAt),
		StartedAt:         formatOptionalTime(item.StartedAt),
		CompletedAt:       formatOptionalTime(item.CompletedAt),
	}
}

func renderProjectWorkArtifact(item projectwork.CollaborationArtifact) ProjectWorkArtifactResponse {
	return ProjectWorkArtifactResponse{
		ID:           item.ID,
		ProjectID:    item.ProjectID,
		WorkItemID:   item.WorkItemID,
		AssignmentID: item.AssignmentID,
		Kind:         item.Kind,
		Title:        item.Title,
		Body:         item.Body,
		AuthorRoleID: item.AuthorRoleID,
		CreatedAt:    formatOptionalTime(item.CreatedAt),
		UpdatedAt:    formatOptionalTime(item.UpdatedAt),
	}
}
