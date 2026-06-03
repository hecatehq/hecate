package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/projectwork"
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
	Status            *string `json:"status,omitempty"`
	TaskID            *string `json:"task_id,omitempty"`
	RunID             *string `json:"run_id,omitempty"`
	ChatSessionID     *string `json:"chat_session_id,omitempty"`
	MessageID         *string `json:"message_id,omitempty"`
	ContextSnapshotID *string `json:"context_snapshot_id,omitempty"`
	StartedAt         *string `json:"started_at,omitempty"`
	CompletedAt       *string `json:"completed_at,omitempty"`
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

type ProjectWorkAssignmentsResponse struct {
	Object string                          `json:"object"`
	Data   []ProjectWorkAssignmentResponse `json:"data"`
}

type ProjectWorkAssignmentEnvelope struct {
	Object string                        `json:"object"`
	Data   ProjectWorkAssignmentResponse `json:"data"`
}

type ProjectWorkAssignmentResponse struct {
	ID                string `json:"id"`
	ProjectID         string `json:"project_id"`
	WorkItemID        string `json:"work_item_id"`
	RoleID            string `json:"role_id"`
	Status            string `json:"status"`
	TaskID            string `json:"task_id,omitempty"`
	RunID             string `json:"run_id,omitempty"`
	ChatSessionID     string `json:"chat_session_id,omitempty"`
	MessageID         string `json:"message_id,omitempty"`
	ContextSnapshotID string `json:"context_snapshot_id,omitempty"`
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
	StartedAt         string `json:"started_at,omitempty"`
	CompletedAt       string `json:"completed_at,omitempty"`
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
	data := make([]ProjectWorkItemResponse, 0, len(items))
	for _, item := range items {
		data = append(data, renderProjectWorkItem(item))
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
	WriteJSON(w, http.StatusCreated, ProjectWorkItemEnvelope{Object: "project_work_item", Data: renderProjectWorkItem(item)})
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
	WriteJSON(w, http.StatusOK, ProjectWorkItemEnvelope{Object: "project_work_item", Data: renderProjectWorkItem(item)})
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
	WriteJSON(w, http.StatusOK, ProjectWorkItemEnvelope{Object: "project_work_item", Data: renderProjectWorkItem(item)})
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
		data = append(data, renderProjectWorkAssignment(item))
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
	WriteJSON(w, http.StatusCreated, ProjectWorkAssignmentEnvelope{Object: "project_assignment", Data: renderProjectWorkAssignment(item)})
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
	WriteJSON(w, http.StatusOK, ProjectWorkAssignmentEnvelope{Object: "project_assignment", Data: renderProjectWorkAssignment(item)})
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
	case errors.Is(err, projectwork.ErrBuiltInRole), errors.Is(err, projectwork.ErrDuplicateRole):
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
