package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/projectworkapp"
)

type createProjectWorkRoleRequest struct {
	ID                  string   `json:"id,omitempty"`
	Name                string   `json:"name"`
	Description         string   `json:"description,omitempty"`
	Instructions        string   `json:"instructions,omitempty"`
	DefaultDriverKind   string   `json:"default_driver_kind,omitempty"`
	DefaultProvider     string   `json:"default_provider,omitempty"`
	DefaultModel        string   `json:"default_model,omitempty"`
	DefaultAgentProfile string   `json:"default_agent_profile,omitempty"`
	SkillIDs            []string `json:"skill_ids,omitempty"`
}

type updateProjectWorkRoleRequest struct {
	Name                *string  `json:"name,omitempty"`
	Description         *string  `json:"description,omitempty"`
	Instructions        *string  `json:"instructions,omitempty"`
	DefaultDriverKind   *string  `json:"default_driver_kind,omitempty"`
	DefaultProvider     *string  `json:"default_provider,omitempty"`
	DefaultModel        *string  `json:"default_model,omitempty"`
	DefaultAgentProfile *string  `json:"default_agent_profile,omitempty"`
	SkillIDs            []string `json:"skill_ids,omitempty"`
}

type createProjectWorkItemRequest struct {
	ID              string   `json:"id,omitempty"`
	Title           string   `json:"title"`
	Brief           string   `json:"brief,omitempty"`
	Status          string   `json:"status,omitempty"`
	Priority        string   `json:"priority,omitempty"`
	OwnerRoleID     string   `json:"owner_role_id,omitempty"`
	RootID          string   `json:"root_id,omitempty"`
	ReviewerRoleIDs []string `json:"reviewer_role_ids,omitempty"`
}

type updateProjectWorkItemRequest struct {
	Title           *string   `json:"title,omitempty"`
	Brief           *string   `json:"brief,omitempty"`
	Status          *string   `json:"status,omitempty"`
	Priority        *string   `json:"priority,omitempty"`
	OwnerRoleID     *string   `json:"owner_role_id,omitempty"`
	RootID          *string   `json:"root_id,omitempty"`
	ReviewerRoleIDs *[]string `json:"reviewer_role_ids,omitempty"`
}

type createProjectWorkAssignmentRequest struct {
	ID           string                                     `json:"id,omitempty"`
	RoleID       string                                     `json:"role_id"`
	RootID       string                                     `json:"root_id,omitempty"`
	DriverKind   string                                     `json:"driver_kind,omitempty"`
	Status       string                                     `json:"status,omitempty"`
	ExecutionRef *ProjectWorkAssignmentExecutionRefResponse `json:"execution_ref,omitempty"`
	StartedAt    string                                     `json:"started_at,omitempty"`
	CompletedAt  string                                     `json:"completed_at,omitempty"`
}

type updateProjectWorkAssignmentRequest struct {
	RoleID       *string                                    `json:"role_id,omitempty"`
	RootID       *string                                    `json:"root_id,omitempty"`
	DriverKind   *string                                    `json:"driver_kind,omitempty"`
	Status       *string                                    `json:"status,omitempty"`
	ExecutionRef *ProjectWorkAssignmentExecutionRefResponse `json:"execution_ref,omitempty"`
	StartedAt    *string                                    `json:"started_at,omitempty"`
	CompletedAt  *string                                    `json:"completed_at,omitempty"`
}

type startProjectWorkAssignmentRequest struct {
	DriverKind string `json:"driver_kind,omitempty"`
}

type createProjectWorkArtifactRequest struct {
	ID                     string `json:"id,omitempty"`
	AssignmentID           string `json:"assignment_id,omitempty"`
	Kind                   string `json:"kind"`
	Title                  string `json:"title,omitempty"`
	Body                   string `json:"body"`
	AuthorRoleID           string `json:"author_role_id,omitempty"`
	EvidenceSourceKind     string `json:"evidence_source_kind,omitempty"`
	EvidenceURL            string `json:"evidence_url,omitempty"`
	EvidenceExternalID     string `json:"evidence_external_id,omitempty"`
	EvidenceProvider       string `json:"evidence_provider,omitempty"`
	EvidenceTrustLabel     string `json:"evidence_trust_label,omitempty"`
	ReviewedAssignmentID   string `json:"reviewed_assignment_id,omitempty"`
	ReviewVerdict          string `json:"review_verdict,omitempty"`
	ReviewRisk             string `json:"review_risk,omitempty"`
	ReviewFollowUpRequired bool   `json:"review_follow_up_required,omitempty"`
}

type createProjectHandoffRequest struct {
	ID                    string   `json:"id,omitempty"`
	SourceAssignmentID    string   `json:"source_assignment_id,omitempty"`
	SourceRunID           string   `json:"source_run_id,omitempty"`
	SourceChatSessionID   string   `json:"source_chat_session_id,omitempty"`
	SourceMessageID       string   `json:"source_message_id,omitempty"`
	TargetRoleID          string   `json:"target_role_id,omitempty"`
	TargetAssignmentID    string   `json:"target_assignment_id,omitempty"`
	TargetWorkItemID      string   `json:"target_work_item_id,omitempty"`
	Title                 string   `json:"title"`
	Summary               string   `json:"summary"`
	RecommendedNextAction string   `json:"recommended_next_action"`
	LinkedArtifactIDs     []string `json:"linked_artifact_ids,omitempty"`
	LinkedMemoryIDs       []string `json:"linked_memory_ids,omitempty"`
	ContextRefs           []string `json:"context_refs,omitempty"`
	Status                string   `json:"status,omitempty"`
	ProvenanceKind        string   `json:"provenance_kind,omitempty"`
	TrustLabel            string   `json:"trust_label,omitempty"`
	CreatedByRoleID       string   `json:"created_by_role_id,omitempty"`
}

type updateProjectHandoffRequest struct {
	SourceAssignmentID    *string   `json:"source_assignment_id,omitempty"`
	SourceRunID           *string   `json:"source_run_id,omitempty"`
	SourceChatSessionID   *string   `json:"source_chat_session_id,omitempty"`
	SourceMessageID       *string   `json:"source_message_id,omitempty"`
	TargetRoleID          *string   `json:"target_role_id,omitempty"`
	TargetAssignmentID    *string   `json:"target_assignment_id,omitempty"`
	TargetWorkItemID      *string   `json:"target_work_item_id,omitempty"`
	Title                 *string   `json:"title,omitempty"`
	Summary               *string   `json:"summary,omitempty"`
	RecommendedNextAction *string   `json:"recommended_next_action,omitempty"`
	LinkedArtifactIDs     *[]string `json:"linked_artifact_ids,omitempty"`
	LinkedMemoryIDs       *[]string `json:"linked_memory_ids,omitempty"`
	ContextRefs           *[]string `json:"context_refs,omitempty"`
	Status                *string   `json:"status,omitempty"`
	ProvenanceKind        *string   `json:"provenance_kind,omitempty"`
	TrustLabel            *string   `json:"trust_label,omitempty"`
	CreatedByRoleID       *string   `json:"created_by_role_id,omitempty"`
}

type updateProjectHandoffStatusRequest struct {
	Status string `json:"status"`
}

type ProjectWorkRolesResponse struct {
	Object string                    `json:"object"`
	Data   []ProjectWorkRoleResponse `json:"data"`
}

type ProjectWorkRoleResponse struct {
	ID                  string   `json:"id"`
	ProjectID           string   `json:"project_id"`
	Name                string   `json:"name"`
	Description         string   `json:"description,omitempty"`
	Instructions        string   `json:"instructions,omitempty"`
	DefaultDriverKind   string   `json:"default_driver_kind,omitempty"`
	DefaultProvider     string   `json:"default_provider,omitempty"`
	DefaultModel        string   `json:"default_model,omitempty"`
	DefaultAgentProfile string   `json:"default_agent_profile,omitempty"`
	SkillIDs            []string `json:"skill_ids,omitempty"`
	BuiltIn             bool     `json:"built_in"`
	CreatedAt           string   `json:"created_at,omitempty"`
	UpdatedAt           string   `json:"updated_at,omitempty"`
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
	ID              string                          `json:"id"`
	ProjectID       string                          `json:"project_id"`
	Title           string                          `json:"title"`
	Brief           string                          `json:"brief,omitempty"`
	Status          string                          `json:"status"`
	Priority        string                          `json:"priority"`
	OwnerRoleID     string                          `json:"owner_role_id,omitempty"`
	RootID          string                          `json:"root_id,omitempty"`
	ReviewerRoleIDs []string                        `json:"reviewer_role_ids,omitempty"`
	Assignments     []ProjectWorkAssignmentResponse `json:"assignments,omitempty"`
	CreatedAt       string                          `json:"created_at"`
	UpdatedAt       string                          `json:"updated_at"`
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

type ProjectWorkAssignmentExecutionRefResponse struct {
	Kind                 string `json:"kind"`
	TaskID               string `json:"task_id,omitempty"`
	RunID                string `json:"run_id,omitempty"`
	ChatSessionID        string `json:"chat_session_id,omitempty"`
	MessageID            string `json:"message_id,omitempty"`
	ContextSnapshotID    string `json:"context_snapshot_id,omitempty"`
	Status               string `json:"status,omitempty"`
	PendingApprovalCount int    `json:"pending_approval_count,omitempty"`
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
	ID           string                                     `json:"id"`
	ProjectID    string                                     `json:"project_id"`
	WorkItemID   string                                     `json:"work_item_id"`
	RoleID       string                                     `json:"role_id"`
	RootID       string                                     `json:"root_id,omitempty"`
	DriverKind   string                                     `json:"driver_kind"`
	Status       string                                     `json:"status"`
	CreatedAt    string                                     `json:"created_at"`
	UpdatedAt    string                                     `json:"updated_at"`
	StartedAt    string                                     `json:"started_at,omitempty"`
	CompletedAt  string                                     `json:"completed_at,omitempty"`
	ExecutionRef *ProjectWorkAssignmentExecutionRefResponse `json:"execution_ref,omitempty"`
	Execution    *ProjectWorkAssignmentExecutionResponse    `json:"execution,omitempty"`
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
	ID                     string `json:"id"`
	ProjectID              string `json:"project_id"`
	WorkItemID             string `json:"work_item_id"`
	AssignmentID           string `json:"assignment_id,omitempty"`
	Kind                   string `json:"kind"`
	Title                  string `json:"title,omitempty"`
	Body                   string `json:"body"`
	AuthorRoleID           string `json:"author_role_id,omitempty"`
	EvidenceSourceKind     string `json:"evidence_source_kind,omitempty"`
	EvidenceURL            string `json:"evidence_url,omitempty"`
	EvidenceExternalID     string `json:"evidence_external_id,omitempty"`
	EvidenceProvider       string `json:"evidence_provider,omitempty"`
	EvidenceTrustLabel     string `json:"evidence_trust_label,omitempty"`
	ReviewedAssignmentID   string `json:"reviewed_assignment_id,omitempty"`
	ReviewVerdict          string `json:"review_verdict,omitempty"`
	ReviewRisk             string `json:"review_risk,omitempty"`
	ReviewFollowUpRequired bool   `json:"review_follow_up_required,omitempty"`
	CreatedAt              string `json:"created_at"`
	UpdatedAt              string `json:"updated_at"`
}

type ProjectHandoffsResponse struct {
	Object string                   `json:"object"`
	Data   []ProjectHandoffResponse `json:"data"`
}

type ProjectHandoffEnvelope struct {
	Object string                 `json:"object"`
	Data   ProjectHandoffResponse `json:"data"`
}

type ProjectHandoffResponse struct {
	ID                    string   `json:"id"`
	ProjectID             string   `json:"project_id"`
	WorkItemID            string   `json:"work_item_id"`
	SourceAssignmentID    string   `json:"source_assignment_id,omitempty"`
	SourceRunID           string   `json:"source_run_id,omitempty"`
	SourceChatSessionID   string   `json:"source_chat_session_id,omitempty"`
	SourceMessageID       string   `json:"source_message_id,omitempty"`
	TargetRoleID          string   `json:"target_role_id,omitempty"`
	TargetAssignmentID    string   `json:"target_assignment_id,omitempty"`
	TargetWorkItemID      string   `json:"target_work_item_id,omitempty"`
	Title                 string   `json:"title"`
	Summary               string   `json:"summary"`
	RecommendedNextAction string   `json:"recommended_next_action"`
	LinkedArtifactIDs     []string `json:"linked_artifact_ids,omitempty"`
	LinkedMemoryIDs       []string `json:"linked_memory_ids,omitempty"`
	ContextRefs           []string `json:"context_refs,omitempty"`
	Status                string   `json:"status"`
	ProvenanceKind        string   `json:"provenance_kind"`
	TrustLabel            string   `json:"trust_label"`
	CreatedByRoleID       string   `json:"created_by_role_id,omitempty"`
	CreatedAt             string   `json:"created_at"`
	UpdatedAt             string   `json:"updated_at"`
	StatusChangedAt       string   `json:"status_changed_at"`
}

func (h *Handler) HandleProjectActivity(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if !h.requireProject(w, r, projectID) {
		return
	}
	activity, err := h.renderProjectActivity(r.Context(), projectID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, ProjectActivityEnvelope{Object: "project_activity", Data: activity})
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
	role, err := h.projectWorkApplication().CreateRole(r.Context(), projectID, projectworkapp.CreateRoleCommand{
		ID:                  req.ID,
		Name:                req.Name,
		Description:         req.Description,
		Instructions:        req.Instructions,
		DefaultDriverKind:   req.DefaultDriverKind,
		DefaultProvider:     req.DefaultProvider,
		DefaultModel:        req.DefaultModel,
		DefaultAgentProfile: req.DefaultAgentProfile,
		SkillIDs:            req.SkillIDs,
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
	role, err := h.projectWorkApplication().UpdateRole(r.Context(), projectID, r.PathValue("role_id"), projectworkapp.UpdateRoleCommand{
		Name:                req.Name,
		Description:         req.Description,
		Instructions:        req.Instructions,
		DefaultDriverKind:   req.DefaultDriverKind,
		DefaultProvider:     req.DefaultProvider,
		DefaultModel:        req.DefaultModel,
		DefaultAgentProfile: req.DefaultAgentProfile,
		SkillIDs:            req.SkillIDs,
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
	if err := h.projectWorkApplication().DeleteRole(r.Context(), projectID, r.PathValue("role_id")); !writeProjectWorkError(w, err) {
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
	if !h.requireProjectRootRef(w, r, projectID, req.RootID) {
		return
	}
	item, err := h.projectWorkApplication().CreateWorkItem(r.Context(), projectID, projectworkapp.CreateWorkItemCommand{
		ID:              req.ID,
		Title:           req.Title,
		Brief:           req.Brief,
		Status:          req.Status,
		Priority:        req.Priority,
		OwnerRoleID:     req.OwnerRoleID,
		RootID:          req.RootID,
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
	if req.RootID != nil && !h.requireProjectRootRef(w, r, projectID, *req.RootID) {
		return
	}
	item, err := h.projectWorkApplication().UpdateWorkItem(r.Context(), projectID, r.PathValue("work_item_id"), projectworkapp.UpdateWorkItemCommand{
		Title:           req.Title,
		Brief:           req.Brief,
		Status:          req.Status,
		Priority:        req.Priority,
		OwnerRoleID:     req.OwnerRoleID,
		RootID:          req.RootID,
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
	WriteJSON(w, http.StatusOK, ProjectWorkItemEnvelope{Object: "project_work_item", Data: projected})
}

func (h *Handler) HandleDeleteProjectWorkItem(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if !h.requireProject(w, r, projectID) {
		return
	}
	if err := h.projectWorkApplication().DeleteWorkItem(r.Context(), projectID, r.PathValue("work_item_id")); !writeProjectWorkError(w, err) {
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
	if !h.requireProjectRootRef(w, r, projectID, req.RootID) {
		return
	}
	startedAt, completedAt, ok := parseProjectWorkRequestTimes(w, req.StartedAt, req.CompletedAt)
	if !ok {
		return
	}
	item, err := h.projectWorkApplication().CreateAssignment(r.Context(), projectID, workItemID, projectworkapp.CreateAssignmentCommand{
		ID:           req.ID,
		RoleID:       req.RoleID,
		RootID:       req.RootID,
		DriverKind:   req.DriverKind,
		Status:       req.Status,
		ExecutionRef: projectWorkAssignmentExecutionRefFromRequest(req.ExecutionRef),
		StartedAt:    startedAt,
		CompletedAt:  completedAt,
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
	if req.RootID != nil && !h.requireProjectRootRef(w, r, projectID, *req.RootID) {
		return
	}
	startedAt, completedAt, ok := parseProjectWorkOptionalRequestTimes(w, req.StartedAt, req.CompletedAt)
	if !ok {
		return
	}
	var startedAtPtr *time.Time
	if req.StartedAt != nil {
		startedAtPtr = &startedAt
	}
	var completedAtPtr *time.Time
	if req.CompletedAt != nil {
		completedAtPtr = &completedAt
	}
	item, err := h.projectWorkApplication().UpdateAssignment(r.Context(), projectID, assignmentID, projectworkapp.UpdateAssignmentCommand{
		RoleID:       req.RoleID,
		RootID:       req.RootID,
		DriverKind:   req.DriverKind,
		Status:       req.Status,
		ExecutionRef: projectWorkAssignmentExecutionRefPtrFromRequest(req.ExecutionRef),
		StartedAt:    startedAtPtr,
		CompletedAt:  completedAtPtr,
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
	if err := h.projectWorkApplication().DeleteAssignment(r.Context(), projectID, workItemID, assignmentID); !writeProjectWorkError(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

func projectWorkAssignmentIsTerminal(status string) bool {
	return projectworkapp.AssignmentIsTerminal(status)
}

func cloneStringMap(items map[string]string) map[string]string {
	if len(items) == 0 {
		return nil
	}
	out := make(map[string]string, len(items))
	for key, value := range items {
		out[key] = value
	}
	return out
}

func formatAssignmentHints(items []assignmentHint) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		value := strings.TrimSpace(item.value)
		if value == "" {
			continue
		}
		parts = append(parts, item.label+"="+value)
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

func labelWithID(label, id string) string {
	label = strings.TrimSpace(label)
	id = strings.TrimSpace(id)
	if label != "" && id != "" {
		return label + " (" + id + ")"
	}
	return firstNonEmpty(label, id)
}

func launchContextBullet(label, value string) string {
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n"), "\n")
	if len(lines) == 0 {
		return "- " + label + ": "
	}
	if len(lines) == 1 {
		return "- " + label + ": " + lines[0]
	}
	return "- " + label + ": " + lines[0] + "\n  " + strings.Join(lines[1:], "\n  ")
}

func projectWorkAssignmentStatusFromRun(status string) string {
	return projectworkapp.AssignmentStatusFromRun(status)
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
		ID:                     id,
		ProjectID:              projectID,
		WorkItemID:             workItemID,
		AssignmentID:           req.AssignmentID,
		Kind:                   req.Kind,
		Title:                  req.Title,
		Body:                   req.Body,
		AuthorRoleID:           req.AuthorRoleID,
		EvidenceSourceKind:     req.EvidenceSourceKind,
		EvidenceURL:            req.EvidenceURL,
		EvidenceExternalID:     req.EvidenceExternalID,
		EvidenceProvider:       req.EvidenceProvider,
		EvidenceTrustLabel:     req.EvidenceTrustLabel,
		ReviewedAssignmentID:   req.ReviewedAssignmentID,
		ReviewVerdict:          req.ReviewVerdict,
		ReviewRisk:             req.ReviewRisk,
		ReviewFollowUpRequired: req.ReviewFollowUpRequired,
	})
	if !writeProjectWorkError(w, err) {
		return
	}
	WriteJSON(w, http.StatusCreated, ProjectWorkArtifactEnvelope{Object: "project_collaboration_artifact", Data: renderProjectWorkArtifact(item)})
}

func (h *Handler) HandleProjectHandoffs(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if !h.requireProject(w, r, projectID) {
		return
	}
	filter := projectwork.HandoffFilter{
		ProjectID:  projectID,
		WorkItemID: strings.TrimSpace(r.URL.Query().Get("work_item_id")),
		Status:     strings.TrimSpace(r.URL.Query().Get("status")),
	}
	items, err := h.projectWork.ListHandoffs(r.Context(), filter)
	if !writeProjectWorkError(w, err) {
		return
	}
	data := make([]ProjectHandoffResponse, 0, len(items))
	for _, item := range items {
		data = append(data, renderProjectHandoff(item))
	}
	WriteJSON(w, http.StatusOK, ProjectHandoffsResponse{Object: "project_handoffs", Data: data})
}

func (h *Handler) HandleProjectWorkItemHandoffs(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	workItemID := r.PathValue("work_item_id")
	if !h.requireProjectWorkItem(w, r, projectID, workItemID) {
		return
	}
	items, err := h.projectWork.ListHandoffs(r.Context(), projectwork.HandoffFilter{
		ProjectID:  projectID,
		WorkItemID: workItemID,
		Status:     strings.TrimSpace(r.URL.Query().Get("status")),
	})
	if !writeProjectWorkError(w, err) {
		return
	}
	data := make([]ProjectHandoffResponse, 0, len(items))
	for _, item := range items {
		data = append(data, renderProjectHandoff(item))
	}
	WriteJSON(w, http.StatusOK, ProjectHandoffsResponse{Object: "project_handoffs", Data: data})
}

func (h *Handler) HandleCreateProjectHandoff(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	workItemID := r.PathValue("work_item_id")
	if !h.requireProjectWorkItem(w, r, projectID, workItemID) {
		return
	}
	var req createProjectHandoffRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	item, err := h.projectWorkApplication().CreateHandoff(r.Context(), projectID, workItemID, projectworkapp.CreateHandoffCommand{
		ID:                    req.ID,
		SourceAssignmentID:    req.SourceAssignmentID,
		SourceRunID:           req.SourceRunID,
		SourceChatSessionID:   req.SourceChatSessionID,
		SourceMessageID:       req.SourceMessageID,
		TargetRoleID:          req.TargetRoleID,
		TargetAssignmentID:    req.TargetAssignmentID,
		TargetWorkItemID:      req.TargetWorkItemID,
		Title:                 req.Title,
		Summary:               req.Summary,
		RecommendedNextAction: req.RecommendedNextAction,
		LinkedArtifactIDs:     req.LinkedArtifactIDs,
		LinkedMemoryIDs:       req.LinkedMemoryIDs,
		ContextRefs:           req.ContextRefs,
		Status:                req.Status,
		ProvenanceKind:        req.ProvenanceKind,
		TrustLabel:            req.TrustLabel,
		CreatedByRoleID:       req.CreatedByRoleID,
	})
	if !writeProjectWorkError(w, err) {
		return
	}
	WriteJSON(w, http.StatusCreated, ProjectHandoffEnvelope{Object: "project_handoff", Data: renderProjectHandoff(item)})
}

func (h *Handler) HandleUpdateProjectHandoff(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	workItemID := r.PathValue("work_item_id")
	handoffID := r.PathValue("handoff_id")
	if !h.requireProjectWorkItem(w, r, projectID, workItemID) {
		return
	}
	var req updateProjectHandoffRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	item, err := h.projectWorkApplication().UpdateHandoff(r.Context(), projectID, workItemID, handoffID, projectworkapp.UpdateHandoffCommand{
		SourceAssignmentID:    req.SourceAssignmentID,
		SourceRunID:           req.SourceRunID,
		SourceChatSessionID:   req.SourceChatSessionID,
		SourceMessageID:       req.SourceMessageID,
		TargetRoleID:          req.TargetRoleID,
		TargetAssignmentID:    req.TargetAssignmentID,
		TargetWorkItemID:      req.TargetWorkItemID,
		Title:                 req.Title,
		Summary:               req.Summary,
		RecommendedNextAction: req.RecommendedNextAction,
		LinkedArtifactIDs:     req.LinkedArtifactIDs,
		LinkedMemoryIDs:       req.LinkedMemoryIDs,
		ContextRefs:           req.ContextRefs,
		Status:                req.Status,
		ProvenanceKind:        req.ProvenanceKind,
		TrustLabel:            req.TrustLabel,
		CreatedByRoleID:       req.CreatedByRoleID,
	})
	if !writeProjectWorkError(w, err) {
		return
	}
	WriteJSON(w, http.StatusOK, ProjectHandoffEnvelope{Object: "project_handoff", Data: renderProjectHandoff(item)})
}

func (h *Handler) HandleUpdateProjectHandoffStatus(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	workItemID := r.PathValue("work_item_id")
	handoffID := r.PathValue("handoff_id")
	if !h.requireProjectWorkItem(w, r, projectID, workItemID) {
		return
	}
	var req updateProjectHandoffStatusRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	item, err := h.projectWorkApplication().UpdateHandoff(r.Context(), projectID, workItemID, handoffID, projectworkapp.UpdateHandoffCommand{
		Status: &req.Status,
	})
	if !writeProjectWorkError(w, err) {
		return
	}
	WriteJSON(w, http.StatusOK, ProjectHandoffEnvelope{Object: "project_handoff", Data: renderProjectHandoff(item)})
}

func (h *Handler) HandleDeleteProjectHandoff(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	workItemID := r.PathValue("work_item_id")
	handoffID := r.PathValue("handoff_id")
	if !h.requireProjectWorkItem(w, r, projectID, workItemID) {
		return
	}
	if err := h.projectWork.DeleteHandoff(r.Context(), projectID, workItemID, handoffID); !writeProjectWorkError(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

func (h *Handler) requireProjectRootRef(w http.ResponseWriter, r *http.Request, projectID, rootID string) bool {
	rootID = strings.TrimSpace(rootID)
	if rootID == "" {
		return true
	}
	project, ok, err := h.projects.Get(r.Context(), projectID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return false
	}
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project not found")
		return false
	}
	for _, root := range project.Roots {
		if strings.TrimSpace(root.ID) == rootID {
			return true
		}
	}
	WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "root_id does not match a project root")
	return false
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
	var closeoutErr projectworkapp.WorkItemCloseoutBlockedError
	if errors.As(err, &closeoutErr) {
		WriteErrorDetails(w, http.StatusConflict, errCodeConflict, err.Error(), ErrorDetails{
			OperatorAction: "Resolve closeout readiness blockers, refresh the work item, then retry.",
			Fields: map[string]any{
				"readiness": renderProjectWorkItemReadiness(closeoutErr.Readiness),
			},
		})
		return false
	}
	writeAppErrorWithFallback(w, err, projectWorkErrorMappings, http.StatusInternalServerError, errCodeGatewayError)
	return false
}

var projectWorkErrorMappings = []appErrorMapping{
	sentinelAppErrorMapping(http.StatusBadRequest, errCodeInvalidRequest,
		projectworkapp.ErrStoreNotConfigured,
		projectworkapp.ErrTaskStoreNotConfigured,
		projectworkapp.ErrRunnerNotConfigured,
		projectworkapp.ErrChatStoreNotConfigured,
		projectworkapp.ErrAgentRunnerNotConfigured,
	),
	sentinelAppErrorMapping(http.StatusNotFound, errCodeNotFound, projectwork.ErrNotFound),
	sentinelAppErrorMapping(http.StatusBadRequest, errCodeInvalidRequest, projectwork.ErrInvalid),
	sentinelAppErrorMapping(http.StatusConflict, errCodeConflict,
		projectwork.ErrBuiltInRole,
		projectwork.ErrDuplicateRole,
		projectwork.ErrDuplicate,
	),
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

func pluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
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
		ID:                  item.ID,
		ProjectID:           item.ProjectID,
		Name:                item.Name,
		Description:         item.Description,
		Instructions:        item.Instructions,
		DefaultDriverKind:   item.DefaultDriverKind,
		DefaultProvider:     item.DefaultProvider,
		DefaultModel:        item.DefaultModel,
		DefaultAgentProfile: item.DefaultAgentProfile,
		SkillIDs:            append([]string(nil), item.SkillIDs...),
		BuiltIn:             item.BuiltIn,
		CreatedAt:           formatOptionalTime(item.CreatedAt),
		UpdatedAt:           formatOptionalTime(item.UpdatedAt),
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
		RootID:          item.RootID,
		ReviewerRoleIDs: append([]string(nil), item.ReviewerRoleIDs...),
		CreatedAt:       formatOptionalTime(item.CreatedAt),
		UpdatedAt:       formatOptionalTime(item.UpdatedAt),
	}
}

func renderProjectWorkAssignment(item projectwork.Assignment) ProjectWorkAssignmentResponse {
	response := ProjectWorkAssignmentResponse{
		ID:          item.ID,
		ProjectID:   item.ProjectID,
		WorkItemID:  item.WorkItemID,
		RoleID:      item.RoleID,
		RootID:      item.RootID,
		DriverKind:  item.DriverKind,
		Status:      item.Status,
		CreatedAt:   formatOptionalTime(item.CreatedAt),
		UpdatedAt:   formatOptionalTime(item.UpdatedAt),
		StartedAt:   formatOptionalTime(item.StartedAt),
		CompletedAt: formatOptionalTime(item.CompletedAt),
	}
	response.ExecutionRef = renderProjectWorkAssignmentExecutionRef(projectworkapp.AssignmentExecutionRefFor(item, nil, response.Status))
	return response
}

func projectWorkAssignmentExecutionRefFromRequest(ref *ProjectWorkAssignmentExecutionRefResponse) projectwork.AssignmentExecutionRef {
	if ref == nil {
		return projectwork.AssignmentExecutionRef{}
	}
	return projectwork.NormalizeAssignmentExecutionRef(projectwork.AssignmentExecutionRef{
		Kind:                 ref.Kind,
		TaskID:               ref.TaskID,
		RunID:                ref.RunID,
		ChatSessionID:        ref.ChatSessionID,
		MessageID:            ref.MessageID,
		ContextSnapshotID:    ref.ContextSnapshotID,
		Status:               ref.Status,
		PendingApprovalCount: ref.PendingApprovalCount,
		TraceID:              ref.TraceID,
		Missing:              ref.Missing,
	})
}

func projectWorkAssignmentExecutionRefPtrFromRequest(ref *ProjectWorkAssignmentExecutionRefResponse) *projectwork.AssignmentExecutionRef {
	if ref == nil {
		return nil
	}
	converted := projectWorkAssignmentExecutionRefFromRequest(ref)
	return &converted
}

func renderProjectWorkAssignmentExecutionRef(ref *projectworkapp.AssignmentExecutionRef) *ProjectWorkAssignmentExecutionRefResponse {
	if ref == nil {
		return nil
	}
	return &ProjectWorkAssignmentExecutionRefResponse{
		Kind:                 ref.Kind,
		TaskID:               ref.TaskID,
		RunID:                ref.RunID,
		ChatSessionID:        ref.ChatSessionID,
		MessageID:            ref.MessageID,
		ContextSnapshotID:    ref.ContextSnapshotID,
		Status:               ref.Status,
		PendingApprovalCount: ref.PendingApprovalCount,
		TraceID:              ref.TraceID,
		Missing:              ref.Missing,
	}
}

func renderProjectWorkArtifact(item projectwork.CollaborationArtifact) ProjectWorkArtifactResponse {
	return ProjectWorkArtifactResponse{
		ID:                     item.ID,
		ProjectID:              item.ProjectID,
		WorkItemID:             item.WorkItemID,
		AssignmentID:           item.AssignmentID,
		Kind:                   item.Kind,
		Title:                  item.Title,
		Body:                   item.Body,
		AuthorRoleID:           item.AuthorRoleID,
		EvidenceSourceKind:     item.EvidenceSourceKind,
		EvidenceURL:            item.EvidenceURL,
		EvidenceExternalID:     item.EvidenceExternalID,
		EvidenceProvider:       item.EvidenceProvider,
		EvidenceTrustLabel:     item.EvidenceTrustLabel,
		ReviewedAssignmentID:   item.ReviewedAssignmentID,
		ReviewVerdict:          item.ReviewVerdict,
		ReviewRisk:             item.ReviewRisk,
		ReviewFollowUpRequired: item.ReviewFollowUpRequired,
		CreatedAt:              formatOptionalTime(item.CreatedAt),
		UpdatedAt:              formatOptionalTime(item.UpdatedAt),
	}
}

func renderProjectHandoff(item projectwork.Handoff) ProjectHandoffResponse {
	return ProjectHandoffResponse{
		ID:                    item.ID,
		ProjectID:             item.ProjectID,
		WorkItemID:            item.WorkItemID,
		SourceAssignmentID:    item.SourceAssignmentID,
		SourceRunID:           item.SourceRunID,
		SourceChatSessionID:   item.SourceChatSessionID,
		SourceMessageID:       item.SourceMessageID,
		TargetRoleID:          item.TargetRoleID,
		TargetAssignmentID:    item.TargetAssignmentID,
		TargetWorkItemID:      item.TargetWorkItemID,
		Title:                 item.Title,
		Summary:               item.Summary,
		RecommendedNextAction: item.RecommendedNextAction,
		LinkedArtifactIDs:     append([]string(nil), item.LinkedArtifactIDs...),
		LinkedMemoryIDs:       append([]string(nil), item.LinkedMemoryIDs...),
		ContextRefs:           append([]string(nil), item.ContextRefs...),
		Status:                item.Status,
		ProvenanceKind:        item.ProvenanceKind,
		TrustLabel:            item.TrustLabel,
		CreatedByRoleID:       item.CreatedByRoleID,
		CreatedAt:             formatOptionalTime(item.CreatedAt),
		UpdatedAt:             formatOptionalTime(item.UpdatedAt),
		StatusChangedAt:       formatOptionalTime(item.StatusChangedAt),
	}
}
