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
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/agentcontrols"
	"github.com/hecatehq/hecate/internal/agentprofiles"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/orchestrator"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/projectworkapp"
	"github.com/hecatehq/hecate/internal/workspacefs"
	"github.com/hecatehq/hecate/pkg/types"
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

type ProjectActivityEnvelope struct {
	Object string                      `json:"object"`
	Data   ProjectActivityDataResponse `json:"data"`
}

type ProjectActivityDataResponse struct {
	ProjectID string                         `json:"project_id"`
	Summary   ProjectActivitySummaryResponse `json:"summary"`
	Buckets   ProjectActivityBucketsResponse `json:"buckets"`
	Recent    []ProjectActivityItemResponse  `json:"recent"`
}

type ProjectActivitySummaryResponse struct {
	WorkItemCount   int `json:"work_item_count"`
	AssignmentCount int `json:"assignment_count"`
	ActiveCount     int `json:"active_count"`
	BlockedCount    int `json:"blocked_count"`
	CompletedCount  int `json:"completed_count"`
	RecentCount     int `json:"recent_count"`
}

type ProjectActivityBucketsResponse struct {
	Active    []ProjectActivityItemResponse `json:"active"`
	Blocked   []ProjectActivityItemResponse `json:"blocked"`
	Completed []ProjectActivityItemResponse `json:"completed"`
	Recent    []ProjectActivityItemResponse `json:"recent"`
}

type ProjectActivityItemResponse struct {
	ID              string                                 `json:"id"`
	ProjectID       string                                 `json:"project_id"`
	WorkItem        ProjectActivityWorkItemResponse        `json:"work_item"`
	Assignment      ProjectWorkAssignmentResponse          `json:"assignment"`
	Role            ProjectWorkRoleResponse                `json:"role"`
	Status          string                                 `json:"status"`
	BlockingSignal  string                                 `json:"blocking_signal"`
	StatusSummary   string                                 `json:"status_summary"`
	LinkedTaskID    string                                 `json:"linked_task_id,omitempty"`
	LinkedRunID     string                                 `json:"linked_run_id,omitempty"`
	LinkedChatID    string                                 `json:"linked_chat_id,omitempty"`
	LinkedChat      *ProjectActivityLinkedChatResponse     `json:"linked_chat,omitempty"`
	LinkedMessageID string                                 `json:"linked_message_id,omitempty"`
	RecentArtifacts []ProjectWorkArtifactResponse          `json:"recent_artifacts,omitempty"`
	ArtifactSummary ProjectActivityArtifactSummaryResponse `json:"artifact_summary"`
	RecentHandoffs  []ProjectHandoffResponse               `json:"recent_handoffs,omitempty"`
	HandoffSummary  ProjectActivityHandoffSummaryResponse  `json:"handoff_summary"`
	UpdatedAt       string                                 `json:"updated_at"`
}

type ProjectActivityLinkedChatResponse struct {
	ID              string `json:"id"`
	Title           string `json:"title,omitempty"`
	AgentID         string `json:"agent_id,omitempty"`
	DriverKind      string `json:"driver_kind,omitempty"`
	NativeSessionID string `json:"native_session_id,omitempty"`
	Status          string `json:"status,omitempty"`
	LatestMessageID string `json:"latest_message_id,omitempty"`
	LatestRole      string `json:"latest_role,omitempty"`
	LatestStatus    string `json:"latest_status,omitempty"`
	LatestError     string `json:"latest_error,omitempty"`
	MessageCount    int    `json:"message_count,omitempty"`
	CreatedAt       string `json:"created_at,omitempty"`
	UpdatedAt       string `json:"updated_at,omitempty"`
	Missing         bool   `json:"missing,omitempty"`
}

type ProjectActivityWorkItemResponse struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Priority string `json:"priority"`
}

type ProjectActivityArtifactSummaryResponse struct {
	Count        int    `json:"count"`
	LatestKind   string `json:"latest_kind,omitempty"`
	LatestTitle  string `json:"latest_title,omitempty"`
	LatestAt     string `json:"latest_at,omitempty"`
	AssignmentID string `json:"assignment_id,omitempty"`
}

type ProjectActivityHandoffSummaryResponse struct {
	Count          int    `json:"count"`
	PendingCount   int    `json:"pending_count,omitempty"`
	AcceptedCount  int    `json:"accepted_count,omitempty"`
	LatestStatus   string `json:"latest_status,omitempty"`
	LatestTitle    string `json:"latest_title,omitempty"`
	LatestAt       string `json:"latest_at,omitempty"`
	AssignmentID   string `json:"assignment_id,omitempty"`
	TargetRoleID   string `json:"target_role_id,omitempty"`
	TargetWorkItem string `json:"target_work_item_id,omitempty"`
}

func (h *Handler) projectWorkApplication() *projectworkapp.Application {
	if h == nil {
		return projectworkapp.New(projectworkapp.Options{})
	}
	return projectworkapp.New(projectworkapp.Options{
		Store:          h.projectWork,
		TaskStore:      h.taskStore,
		Runner:         h.taskRunner,
		ChatStore:      h.agentChat,
		AgentRunner:    h.agentChatRunner,
		PrepareTimeout: agentChatPrepareTimeout,
		IDGenerator:    newOpaqueTaskResourceID,
	})
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
	item, err := h.projectWorkApplication().CreateWorkItem(r.Context(), projectID, projectworkapp.CreateWorkItemCommand{
		ID:              req.ID,
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
	item, err := h.projectWorkApplication().UpdateWorkItem(r.Context(), projectID, r.PathValue("work_item_id"), projectworkapp.UpdateWorkItemCommand{
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
	startedAt, completedAt, ok := parseProjectWorkRequestTimes(w, req.StartedAt, req.CompletedAt)
	if !ok {
		return
	}
	item, err := h.projectWorkApplication().CreateAssignment(r.Context(), projectID, workItemID, projectworkapp.CreateAssignmentCommand{
		ID:                req.ID,
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
	var startedAtPtr *time.Time
	if req.StartedAt != nil {
		startedAtPtr = &startedAt
	}
	var completedAtPtr *time.Time
	if req.CompletedAt != nil {
		completedAtPtr = &completedAt
	}
	item, err := h.projectWorkApplication().UpdateAssignment(r.Context(), projectID, assignmentID, projectworkapp.UpdateAssignmentCommand{
		RoleID:            req.RoleID,
		DriverKind:        req.DriverKind,
		Status:            req.Status,
		TaskID:            req.TaskID,
		RunID:             req.RunID,
		ChatSessionID:     req.ChatSessionID,
		MessageID:         req.MessageID,
		ContextSnapshotID: req.ContextSnapshotID,
		StartedAt:         startedAtPtr,
		CompletedAt:       completedAtPtr,
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
	if projectWorkAssignmentIsTerminal(assignment.Status) {
		projected, projectErr := h.renderProjectedProjectWorkAssignment(ctx, assignment)
		if projectErr != nil {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, projectErr.Error())
			return
		}
		WriteJSON(w, http.StatusConflict, ProjectWorkAssignmentEnvelope{Object: "project_assignment", Data: projected})
		return
	}
	if assignment.DriverKind == projectwork.AssignmentDriverExternalAgent {
		h.startProjectExternalAgentAssignment(w, r, project, workItem, assignment, role)
		return
	}
	if assignment.DriverKind != projectwork.AssignmentDriverHecateTask {
		WriteError(w, http.StatusConflict, errCodeConflict, fmt.Sprintf("assignment driver_kind %q is not supported; V1 supports %q and %q", assignment.DriverKind, projectwork.AssignmentDriverHecateTask, projectwork.AssignmentDriverExternalAgent))
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
	requestedProvider := strings.TrimSpace(firstNonEmpty(role.DefaultProvider, project.DefaultProvider))
	requestedModel := strings.TrimSpace(firstNonEmpty(role.DefaultModel, project.DefaultModel))
	profile, err := h.resolveProjectAssignmentProfile(ctx, role, project)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	executionProfile := strings.TrimSpace(firstNonEmpty(profile.ExecutionProfile, role.DefaultAgentProfile, project.DefaultAgentProfile, "project_assignment"))
	if profile.ProviderHint != "" && requestedProvider == "" {
		requestedProvider = profile.ProviderHint
	}
	if profile.ModelHint != "" && requestedModel == "" {
		requestedModel = profile.ModelHint
	}
	requestedModel = strings.TrimSpace(firstNonEmpty(requestedModel, h.config.Router.DefaultModel))
	if requestedModel == "" {
		WriteError(w, http.StatusUnprocessableEntity, errCodeModelNotConfigured, "project assignment start requires a default model")
		return
	}
	resolvedSkills := h.resolveProjectAssignmentSkills(ctx, project.ID, role, profile)
	promptContext := h.projectAssignmentPromptContext(ctx, project, profile, workingDirectory)
	contextPacket := h.projectAssignmentContextPacket(ctx, project, workItem, assignment, role, workingDirectory, requestedProvider, requestedModel, executionProfile, profile, resolvedSkills, promptContext)
	if contextPacket.ID == "" {
		contextPacket.ID = newChatID("ctx")
	}

	result, err := h.projectWorkApplication().StartTaskAssignment(ctx, projectworkapp.StartTaskAssignmentCommand{
		ProjectID:         projectID,
		WorkItemID:        workItemID,
		Assignment:        assignment,
		ContextSnapshotID: contextPacket.ID,
		BuildTask: func(taskID string) (types.Task, error) {
			return h.buildProjectAssignmentTask(taskID, project, workItem, assignment, role, profile, workingDirectory, workspaceMode, requestedProvider, requestedModel, executionProfile, promptContext), nil
		},
		OnTaskCreated: func(task types.Task) {
			contextPacket.Refs.TaskID = task.ID
		},
		InitializeRun: func(task types.Task, run *types.TaskRun) {
			contextPacket.Refs.RunID = run.ID
			run.ContextPacket = marshalContextPacket(normalizeContextPacket(contextPacket, chat.ContextRefs{
				TaskID:       task.ID,
				RunID:        run.ID,
				ProjectID:    project.ID,
				WorkItemID:   workItem.ID,
				AssignmentID: assignment.ID,
				RoleID:       role.ID,
			}))
		},
	})
	if err != nil {
		resultAssignment := assignment
		if result != nil && result.Assignment.ID != "" {
			resultAssignment = result.Assignment
		}
		if errors.Is(err, projectworkapp.ErrAssignmentStartConflict) {
			projected, projectErr := h.renderProjectedProjectWorkAssignment(ctx, resultAssignment)
			if projectErr != nil {
				WriteError(w, http.StatusInternalServerError, errCodeGatewayError, projectErr.Error())
				return
			}
			WriteJSON(w, http.StatusConflict, ProjectWorkAssignmentEnvelope{Object: "project_assignment", Data: projected})
			return
		}
		if errors.Is(err, orchestrator.ErrAgentLoopMisconfigured) {
			WriteError(w, http.StatusUnprocessableEntity, errCodeModelNotConfigured, err.Error())
			return
		}
		if result != nil && result.Task.ID != "" {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, fmt.Sprintf("task %s was created but start failed: %s", result.Task.ID, err.Error()))
			return
		}
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, fmt.Sprintf("task could not be created for assignment %s: %s", resultAssignment.ID, err.Error()))
		return
	}
	if result.TraceID != "" {
		w.Header().Set("X-Trace-Id", result.TraceID)
	}
	if result.SpanID != "" {
		w.Header().Set("X-Span-Id", result.SpanID)
	}
	projected, err := h.renderProjectedProjectWorkAssignment(ctx, result.Assignment)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, ProjectWorkAssignmentEnvelope{Object: "project_assignment", Data: projected})
}

func (h *Handler) startProjectExternalAgentAssignment(w http.ResponseWriter, r *http.Request, project projects.Project, workItem projectwork.WorkItem, assignment projectwork.Assignment, role projectwork.AgentRoleProfile) {
	ctx := r.Context()
	if h.agentChat == nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, "agent chat store is not configured")
		return
	}
	if h.agentChatRunner == nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, "agent chat runner is not configured")
		return
	}
	if strings.TrimSpace(assignment.ChatSessionID) != "" {
		projected, projectErr := h.renderProjectedProjectWorkAssignment(ctx, assignment)
		if projectErr != nil {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, projectErr.Error())
			return
		}
		WriteJSON(w, http.StatusConflict, ProjectWorkAssignmentEnvelope{Object: "project_assignment", Data: projected})
		return
	}
	workingDirectory, _, err := resolveProjectAssignmentWorkspace(project)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	profile, err := h.resolveProjectAssignmentProfile(ctx, role, project)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	adapterID := strings.TrimSpace(profile.ExternalAgentKind)
	if adapterID == "" {
		WriteError(w, http.StatusUnprocessableEntity, errCodeInvalidRequest, "external-agent assignment requires an agent profile with external_agent_kind")
		return
	}
	adapter, ok := agentadapters.BuiltInByID(adapterID)
	if !ok {
		writeAgentChatAdapterNotFound(w, adapterID)
		return
	}
	configOptions, err := projectExternalAgentConfigOptions(adapterID, profile.ExternalAgentOptions)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	workspace, err := agentadapters.ValidateWorkspace(workingDirectory)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	sessionID := newChatID("chat")
	resolvedSkills := h.resolveProjectAssignmentSkills(ctx, project.ID, role, profile)
	contextPacket := h.projectAssignmentContextPacket(ctx, project, workItem, assignment, role, workspace, "", "", firstNonEmptyString(profile.ExecutionProfile, "external_agent_assignment"), profile, resolvedSkills, projectAssignmentPromptContext{})
	contextPacket.ID = firstNonEmptyString(contextPacket.ID, newChatID("ctx"))
	contextPacket.ExecutionMode = chat.ExecutionModeExternalAgent
	contextPacket.Provider = ""
	contextPacket.Model = ""
	contextPacket.Workspace = workspace
	contextPacket.Refs.SessionID = sessionID
	contextPacket.Refs.TaskID = ""
	contextPacket.Refs.RunID = ""

	session := chat.Session{
		ID:              sessionID,
		Title:           projectExternalAgentAssignmentTitle(workItem, role, adapter),
		ProjectID:       project.ID,
		AgentID:         adapterID,
		DriverKind:      agentadapters.DriverKindACP,
		Workspace:       workspace,
		WorkspaceBranch: workspaceGitBranch(workspace),
		ConfigOptions:   configOptions,
	}
	contextPacket.Refs.SessionID = session.ID
	contextPacket = normalizeContextPacket(contextPacket, chat.ContextRefs{
		SessionID:    session.ID,
		ProjectID:    project.ID,
		WorkItemID:   workItem.ID,
		AssignmentID: assignment.ID,
		RoleID:       role.ID,
	})
	packetBytes := marshalContextPacket(contextPacket)
	result, err := h.projectWorkApplication().StartExternalAgentAssignment(ctx, projectworkapp.StartExternalAgentAssignmentCommand{
		ProjectID:         project.ID,
		Assignment:        assignment,
		Session:           session,
		ContextSnapshotID: contextPacket.ID,
		ContextPacket:     packetBytes,
	})
	if err != nil {
		var prepareErr projectworkapp.ExternalAgentPrepareError
		if errors.As(err, &prepareErr) {
			writeAgentChatPrepareError(w, adapter.Name, prepareErr.Unwrap())
			return
		}
		resultAssignment := assignment
		if result != nil && result.Assignment.ID != "" {
			resultAssignment = result.Assignment
		}
		if errors.Is(err, projectworkapp.ErrAssignmentStartConflict) {
			projected, projectErr := h.renderProjectedProjectWorkAssignment(ctx, resultAssignment)
			if projectErr != nil {
				WriteError(w, http.StatusInternalServerError, errCodeGatewayError, projectErr.Error())
				return
			}
			WriteJSON(w, http.StatusConflict, ProjectWorkAssignmentEnvelope{Object: "project_assignment", Data: projected})
			return
		}
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	projected, err := h.renderProjectedProjectWorkAssignment(ctx, result.Assignment)
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
	return projectworkapp.AssignmentIsTerminal(status)
}

func projectWorkAssignmentHasActiveExecution(ctx context.Context, store taskRunLookupStore, assignment projectwork.Assignment) (bool, error) {
	return projectworkapp.AssignmentHasActiveExecution(ctx, store, assignment)
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

func (h *Handler) buildProjectAssignmentTask(taskID string, project projects.Project, workItem projectwork.WorkItem, assignment projectwork.Assignment, role projectwork.AgentRoleProfile, profile resolvedAgentProfile, workingDirectory, workspaceMode, requestedProvider, requestedModel, executionProfile string, promptContext projectAssignmentPromptContext) types.Task {
	now := time.Now().UTC()
	return types.Task{
		ID:                          taskID,
		Title:                       projectAssignmentTaskTitle(workItem, role),
		Prompt:                      projectAssignmentPrompt(project, workItem, assignment, role),
		ProjectID:                   project.ID,
		SystemPrompt:                projectAssignmentSystemPrompt(project, role, profile, promptContext),
		WorkspaceSystemPromptPolicy: types.WorkspaceSystemPromptExclude,
		ExecutionKind:               "agent_loop",
		ExecutionProfile:            executionProfile,
		OriginKind:                  "project_work_item",
		OriginID:                    workItem.ID,
		WorkspaceMode:               workspaceMode,
		WorkingDirectory:            workingDirectory,
		SandboxAllowedRoot:          workingDirectory,
		Status:                      "queued",
		Priority:                    firstNonEmpty(workItem.Priority, "normal"),
		RequestedProvider:           requestedProvider,
		RequestedModel:              requestedModel,
		CreatedAt:                   now,
		UpdatedAt:                   now,
	}
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
	provider := firstNonEmpty(role.DefaultProvider, project.DefaultProvider, "auto")
	model := firstNonEmpty(role.DefaultModel, project.DefaultModel, "project/runtime default")
	profile := firstNonEmpty(role.DefaultAgentProfile, project.DefaultAgentProfile, "none")
	driver := firstNonEmpty(assignment.DriverKind, role.DefaultDriverKind, projectwork.AssignmentDriverHecateTask)
	sections := []string{
		"Launch context",
		"Project: " + labelWithID(project.Name, project.ID),
		strings.Join([]string{
			"Work item:",
			"- Title: " + firstNonEmpty(workItem.Title, workItem.ID),
			launchContextBullet("Brief", firstNonEmpty(workItem.Brief, "No brief recorded.")),
			"- Status: " + firstNonEmpty(workItem.Status, "unknown"),
			"- Priority: " + firstNonEmpty(workItem.Priority, "normal"),
		}, "\n"),
		strings.Join([]string{
			"Assignment:",
			"- ID: " + assignment.ID,
			"- Status: " + firstNonEmpty(assignment.Status, projectwork.AssignmentStatusQueued),
			"- Driver: " + driver,
		}, "\n"),
		strings.Join([]string{
			"Role:",
			"- Name: " + firstNonEmpty(role.Name, assignment.RoleID),
			launchContextBullet("Description", firstNonEmpty(role.Description, "No description recorded.")),
			launchContextBullet("Instructions", firstNonEmpty(role.Instructions, "No role instructions recorded.")),
		}, "\n"),
		strings.Join([]string{
			"Execution hints:",
			"- Driver: " + driver,
			"- Provider: " + provider,
			"- Model: " + model,
			"- Profile: " + profile,
			"- Role defaults: " + formatAssignmentHints([]assignmentHint{
				{"driver", role.DefaultDriverKind},
				{"provider", role.DefaultProvider},
				{"model", role.DefaultModel},
				{"profile", role.DefaultAgentProfile},
			}),
			"- Project defaults: " + formatAssignmentHints([]assignmentHint{
				{"provider", project.DefaultProvider},
				{"model", project.DefaultModel},
				{"profile", project.DefaultAgentProfile},
				{"workspace_mode", project.DefaultWorkspaceMode},
			}),
		}, "\n"),
		"Request:\nExecute this assignment as a native agent_loop task. Keep outputs and artifacts linked to this work item.",
	}
	return strings.Join(sections, "\n\n")
}

func projectAssignmentSystemPrompt(project projects.Project, role projectwork.AgentRoleProfile, profile resolvedAgentProfile, promptContext projectAssignmentPromptContext) string {
	var parts []string
	if prompt := strings.TrimSpace(project.DefaultSystemPrompt); prompt != "" {
		parts = append(parts, "Project system prompt:\n"+prompt)
	}
	if instructions := strings.TrimSpace(profile.Instructions); instructions != "" && !profile.Missing {
		parts = append(parts, "Agent profile instructions:\n"+instructions)
	}
	if instructions := strings.TrimSpace(role.Instructions); instructions != "" {
		parts = append(parts, "Role instructions:\n"+instructions)
	} else if role.Name != "" {
		parts = append(parts, "Act as the "+strings.TrimSpace(role.Name)+" for this project work assignment.")
	}
	if contextText := promptContext.SystemPrompt(); contextText != "" {
		parts = append(parts, contextText)
	}
	return strings.Join(parts, "\n\n")
}

const (
	projectAssignmentPromptContextMaxBytes       = 12 * 1024
	projectAssignmentPromptContextMemoryMaxBytes = 2 * 1024
	projectAssignmentPromptContextSourceMaxBytes = 8 * 1024
	projectAssignmentPromptContextMaxWarnings    = 8
)

type projectAssignmentPromptContext struct {
	Sections        []string
	IncludedMemory  int
	IncludedSources int
	Truncated       int
	Warnings        []string
}

func (ctx projectAssignmentPromptContext) SystemPrompt() string {
	if len(ctx.Sections) == 0 {
		return ""
	}
	return strings.Join(ctx.Sections, "\n\n")
}

func (h *Handler) projectAssignmentPromptContext(ctx context.Context, project projects.Project, profile resolvedAgentProfile, workingDirectory string) projectAssignmentPromptContext {
	builder := projectPromptContextBuilder{Remaining: projectAssignmentPromptContextMaxBytes}
	if effectiveProjectMemoryPolicy(profile.ProjectMemoryPolicy) == agentprofiles.MemoryInclude {
		builder.AppendMemory(h.enabledProjectMemoryEntries(ctx, project.ID))
	}
	if effectiveContextSourcePolicy(profile.ContextSourcePolicy) == agentprofiles.ContextIncludeEnabled {
		builder.AppendSources(project, workingDirectory)
	}
	return builder.Result()
}

type projectPromptContextBuilder struct {
	Remaining int
	ResultCtx projectAssignmentPromptContext
}

func (builder *projectPromptContextBuilder) AppendMemory(entries []memory.Entry) {
	for _, entry := range entries {
		if builder.Remaining <= 0 {
			builder.Warn("project memory prompt context budget exhausted; remaining memory entries were skipped")
			return
		}
		title := firstNonEmptyString(strings.TrimSpace(entry.Title), strings.TrimSpace(entry.ID))
		body := strings.TrimSpace(entry.Body)
		if body == "" {
			continue
		}
		header := fmt.Sprintf("Project memory: %s\nID: %s\nTrust: %s", title, strings.TrimSpace(entry.ID), firstNonEmptyString(strings.TrimSpace(entry.TrustLabel), contextTrustOperatorMemory))
		section, truncated := boundedPromptContextSection(header, body, projectAssignmentPromptContextMemoryMaxBytes, &builder.Remaining)
		if section == "" {
			builder.Warn("project memory prompt context budget exhausted before " + strings.TrimSpace(entry.ID))
			return
		}
		if truncated {
			builder.ResultCtx.Truncated++
			builder.Warn("project memory " + strings.TrimSpace(entry.ID) + " was truncated for prompt context")
		}
		builder.ResultCtx.IncludedMemory++
		builder.ResultCtx.Sections = append(builder.ResultCtx.Sections, section)
	}
}

func (builder *projectPromptContextBuilder) AppendSources(project projects.Project, workingDirectory string) {
	for _, source := range project.ContextSources {
		if !source.Enabled {
			continue
		}
		if builder.Remaining <= 0 {
			builder.Warn("project source prompt context budget exhausted; remaining sources were skipped")
			return
		}
		if !projectContextSourcePromptEligible(source) {
			if strings.TrimSpace(source.Path) != "" {
				builder.Warn("project source " + strings.TrimSpace(source.Path) + " is metadata-only for Hecate prompt context")
			}
			continue
		}
		rootPath := projectContextSourceRootPath(project, source, workingDirectory)
		if strings.TrimSpace(rootPath) == "" {
			builder.Warn("project source " + strings.TrimSpace(source.Path) + " could not resolve an active root")
			continue
		}
		fsys, err := workspacefs.New(rootPath)
		if err != nil {
			builder.Warn("project source " + strings.TrimSpace(source.Path) + " could not open its workspace root")
			continue
		}
		raw, _, err := fsys.ReadFile(source.Path)
		if err != nil {
			builder.Warn("project source " + strings.TrimSpace(source.Path) + " could not be read for prompt context")
			continue
		}
		body := strings.TrimSpace(string(raw))
		if body == "" {
			continue
		}
		title := firstNonEmptyString(strings.TrimSpace(source.Title), strings.TrimSpace(source.Path))
		header := fmt.Sprintf("Workspace instruction: %s\nPath: %s\nTrust: %s", title, strings.TrimSpace(source.Path), firstNonEmptyString(strings.TrimSpace(source.TrustLabel), contextTrustWorkspaceGuidance))
		section, truncated := boundedPromptContextSection(header, body, projectAssignmentPromptContextSourceMaxBytes, &builder.Remaining)
		if section == "" {
			builder.Warn("project source prompt context budget exhausted before " + strings.TrimSpace(source.Path))
			return
		}
		if truncated {
			builder.ResultCtx.Truncated++
			builder.Warn("project source " + strings.TrimSpace(source.Path) + " was truncated for prompt context")
		}
		builder.ResultCtx.IncludedSources++
		builder.ResultCtx.Sections = append(builder.ResultCtx.Sections, section)
	}
}

func (builder *projectPromptContextBuilder) Warn(warning string) {
	warning = strings.TrimSpace(warning)
	if warning == "" || len(builder.ResultCtx.Warnings) >= projectAssignmentPromptContextMaxWarnings {
		return
	}
	builder.ResultCtx.Warnings = append(builder.ResultCtx.Warnings, warning)
}

func (builder projectPromptContextBuilder) Result() projectAssignmentPromptContext {
	return builder.ResultCtx
}

func projectContextSourcePromptEligible(source projects.ContextSource) bool {
	return strings.TrimSpace(source.Kind) == "workspace_instruction" && strings.TrimSpace(source.Format) == "agents_md"
}

func projectContextSourceRootPath(project projects.Project, source projects.ContextSource, fallback string) string {
	rootID := ""
	if source.Metadata != nil {
		rootID = strings.TrimSpace(source.Metadata["root_id"])
	}
	if rootID != "" {
		for _, root := range project.Roots {
			if root.Active && strings.TrimSpace(root.ID) == rootID {
				return strings.TrimSpace(root.Path)
			}
		}
		return ""
	}
	return strings.TrimSpace(fallback)
}

func boundedPromptContextSection(header, body string, itemMaxBytes int, remaining *int) (string, bool) {
	if remaining == nil || *remaining <= 0 {
		return "", false
	}
	header = strings.TrimSpace(header)
	body = strings.TrimSpace(body)
	if header == "" || body == "" {
		return "", false
	}
	limit := itemMaxBytes
	if *remaining < limit {
		limit = *remaining
	}
	text := header + "\n" + body
	text, truncated := truncatePromptContextText(text, limit)
	if text == "" {
		return "", truncated
	}
	*remaining -= len(text)
	return text, truncated
}

func truncatePromptContextText(text string, maxBytes int) (string, bool) {
	text = strings.TrimSpace(text)
	if maxBytes <= 0 {
		return "", text != ""
	}
	if len(text) <= maxBytes {
		return text, false
	}
	if maxBytes <= len("\n[truncated]") {
		return "", true
	}
	cut := maxBytes - len("\n[truncated]")
	raw := []byte(text)
	for cut > 0 && !utf8.Valid(raw[:cut]) {
		cut--
	}
	if cut <= 0 {
		return "", true
	}
	return strings.TrimSpace(string(raw[:cut])) + "\n[truncated]", true
}

type assignmentHint struct {
	label string
	value string
}

type resolvedAgentProfile struct {
	ID                   string
	Name                 string
	Source               string
	Instructions         string
	Missing              bool
	Surface              string
	ProviderHint         string
	ModelHint            string
	ExecutionProfile     string
	ToolsEnabled         bool
	WritesAllowed        bool
	NetworkAllowed       bool
	ApprovalPolicy       string
	ProjectMemoryPolicy  string
	ContextSourcePolicy  string
	SkillIDs             []string
	ExternalAgentKind    string
	ExternalAgentOptions map[string]string
	Warnings             []string
}

func (h *Handler) resolveProjectAssignmentProfile(ctx context.Context, role projectwork.AgentRoleProfile, project projects.Project) (resolvedAgentProfile, error) {
	for _, candidate := range []struct {
		id     string
		source string
	}{
		{strings.TrimSpace(role.DefaultAgentProfile), "role_default"},
		{strings.TrimSpace(project.DefaultAgentProfile), "project_default"},
	} {
		if candidate.id == "" {
			continue
		}
		if h != nil && h.agentProfiles != nil {
			profile, ok, err := h.agentProfiles.Get(ctx, candidate.id)
			if err != nil {
				return resolvedAgentProfile{}, err
			}
			if ok {
				return resolvedProfileFromStore(profile, candidate.source), nil
			}
		}
		return resolvedAgentProfile{
			ID:                  candidate.id,
			Name:                candidate.id,
			Source:              candidate.source,
			Missing:             true,
			ExecutionProfile:    candidate.id,
			ApprovalPolicy:      agentprofiles.ApprovalInherit,
			ProjectMemoryPolicy: agentprofiles.MemoryInherit,
			ContextSourcePolicy: agentprofiles.ContextInherit,
			Warnings:            []string{fmt.Sprintf("Referenced agent profile %q was not found; using stored profile id as execution_profile hint.", candidate.id)},
		}, nil
	}
	return resolvedAgentProfile{
		ID:                  "project_assignment",
		Name:                "Project Assignment",
		Source:              "built_in_fallback",
		Surface:             agentprofiles.SurfaceHecateTask,
		ExecutionProfile:    "project_assignment",
		ToolsEnabled:        true,
		WritesAllowed:       true,
		ApprovalPolicy:      agentprofiles.ApprovalInherit,
		ProjectMemoryPolicy: agentprofiles.MemoryVisibleOnly,
		ContextSourcePolicy: agentprofiles.ContextVisibleOnly,
	}, nil
}

func resolvedProfileFromStore(profile agentprofiles.Profile, source string) resolvedAgentProfile {
	return resolvedAgentProfile{
		ID:                   profile.ID,
		Name:                 profile.Name,
		Source:               source,
		Instructions:         profile.Instructions,
		Surface:              profile.Surface,
		ProviderHint:         profile.ProviderHint,
		ModelHint:            profile.ModelHint,
		ExecutionProfile:     firstNonEmptyString(profile.ExecutionProfile, profile.ID),
		ToolsEnabled:         profile.ToolsEnabled,
		WritesAllowed:        profile.WritesAllowed,
		NetworkAllowed:       profile.NetworkAllowed,
		ApprovalPolicy:       profile.ApprovalPolicy,
		ProjectMemoryPolicy:  profile.ProjectMemoryPolicy,
		ContextSourcePolicy:  profile.ContextSourcePolicy,
		SkillIDs:             append([]string(nil), profile.SkillIDs...),
		ExternalAgentKind:    profile.ExternalAgentKind,
		ExternalAgentOptions: cloneStringMap(profile.ExternalAgentOptions),
	}
}

func projectExternalAgentConfigOptions(adapterID string, options map[string]string) ([]agentcontrols.ConfigOption, error) {
	if len(options) == 0 {
		return nil, nil
	}
	out := make([]agentcontrols.ConfigOption, 0, len(options))
	for key, value := range options {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		option, ok := agentadapters.LaunchConfigOptionForSet(adapterID, key, value)
		if !ok {
			return nil, fmt.Errorf("external_agent_options.%s is not a launch option for %s", key, adapterID)
		}
		out = append(out, option)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func projectExternalAgentAssignmentTitle(workItem projectwork.WorkItem, role projectwork.AgentRoleProfile, adapter agentadapters.Adapter) string {
	parts := []string{}
	if title := strings.TrimSpace(workItem.Title); title != "" {
		parts = append(parts, title)
	}
	if roleName := strings.TrimSpace(role.Name); roleName != "" {
		parts = append(parts, roleName)
	}
	if adapter.Name != "" {
		parts = append(parts, adapter.Name)
	}
	if len(parts) == 0 {
		return "External Agent assignment"
	}
	return strings.Join(parts, " - ")
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
	id := strings.TrimSpace(req.ID)
	if id == "" {
		id = newOpaqueTaskResourceID("handoff")
	}
	item, err := h.projectWork.CreateHandoff(r.Context(), projectwork.Handoff{
		ID:                    id,
		ProjectID:             projectID,
		WorkItemID:            workItemID,
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
	item, err := h.projectWork.UpdateHandoff(r.Context(), projectID, workItemID, handoffID, func(item *projectwork.Handoff) {
		applyProjectHandoffPatch(item, req)
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
	item, err := h.projectWork.UpdateHandoff(r.Context(), projectID, workItemID, handoffID, func(item *projectwork.Handoff) {
		item.Status = req.Status
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

func applyProjectHandoffPatch(item *projectwork.Handoff, req updateProjectHandoffRequest) {
	if req.SourceAssignmentID != nil {
		item.SourceAssignmentID = *req.SourceAssignmentID
	}
	if req.SourceRunID != nil {
		item.SourceRunID = *req.SourceRunID
	}
	if req.SourceChatSessionID != nil {
		item.SourceChatSessionID = *req.SourceChatSessionID
	}
	if req.SourceMessageID != nil {
		item.SourceMessageID = *req.SourceMessageID
	}
	if req.TargetRoleID != nil {
		item.TargetRoleID = *req.TargetRoleID
	}
	if req.TargetAssignmentID != nil {
		item.TargetAssignmentID = *req.TargetAssignmentID
	}
	if req.TargetWorkItemID != nil {
		item.TargetWorkItemID = *req.TargetWorkItemID
	}
	if req.Title != nil {
		item.Title = *req.Title
	}
	if req.Summary != nil {
		item.Summary = *req.Summary
	}
	if req.RecommendedNextAction != nil {
		item.RecommendedNextAction = *req.RecommendedNextAction
	}
	if req.LinkedArtifactIDs != nil {
		item.LinkedArtifactIDs = *req.LinkedArtifactIDs
	}
	if req.LinkedMemoryIDs != nil {
		item.LinkedMemoryIDs = *req.LinkedMemoryIDs
	}
	if req.ContextRefs != nil {
		item.ContextRefs = *req.ContextRefs
	}
	if req.Status != nil {
		item.Status = *req.Status
	}
	if req.ProvenanceKind != nil {
		item.ProvenanceKind = *req.ProvenanceKind
	}
	if req.TrustLabel != nil {
		item.TrustLabel = *req.TrustLabel
	}
	if req.CreatedByRoleID != nil {
		item.CreatedByRoleID = *req.CreatedByRoleID
	}
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
	writeAppErrorWithFallback(w, err, projectWorkErrorMappings, http.StatusInternalServerError, errCodeGatewayError)
	return false
}

var projectWorkErrorMappings = []appErrorMapping{
	{
		Match: func(err error) bool {
			return errors.Is(err, projectworkapp.ErrStoreNotConfigured) ||
				errors.Is(err, projectworkapp.ErrTaskStoreNotConfigured) ||
				errors.Is(err, projectworkapp.ErrRunnerNotConfigured) ||
				errors.Is(err, projectworkapp.ErrChatStoreNotConfigured) ||
				errors.Is(err, projectworkapp.ErrAgentRunnerNotConfigured)
		},
		Status: http.StatusBadRequest,
		Code:   errCodeInvalidRequest,
	},
	{
		Match: func(err error) bool {
			return errors.Is(err, projectwork.ErrNotFound)
		},
		Status: http.StatusNotFound,
		Code:   errCodeNotFound,
	},
	{
		Match: func(err error) bool {
			return errors.Is(err, projectwork.ErrInvalid)
		},
		Status: http.StatusBadRequest,
		Code:   errCodeInvalidRequest,
	},
	{
		Match: func(err error) bool {
			return errors.Is(err, projectwork.ErrBuiltInRole) ||
				errors.Is(err, projectwork.ErrDuplicateRole) ||
				errors.Is(err, projectwork.ErrDuplicate)
		},
		Status: http.StatusConflict,
		Code:   errCodeConflict,
	},
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
	response.Assignments = projected
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

func (h *Handler) projectActivityLinkedChats(ctx context.Context, projectID string, assignments []projectwork.Assignment) map[string]*ProjectActivityLinkedChatResponse {
	linked := make(map[string]*ProjectActivityLinkedChatResponse)
	if h == nil || h.agentChat == nil {
		return linked
	}
	for _, assignment := range assignments {
		chatID := strings.TrimSpace(assignment.ChatSessionID)
		if chatID == "" {
			continue
		}
		session, ok, err := h.agentChat.Get(ctx, chatID)
		if err != nil || !ok || strings.TrimSpace(session.ProjectID) != strings.TrimSpace(projectID) {
			linked[assignment.ID] = missingProjectActivityLinkedChat(chatID)
			continue
		}
		linked[assignment.ID] = renderProjectActivityLinkedChat(session)
	}
	return linked
}

func missingProjectActivityLinkedChat(chatID string) *ProjectActivityLinkedChatResponse {
	return &ProjectActivityLinkedChatResponse{
		ID:      chatID,
		Missing: true,
	}
}

func renderProjectActivityLinkedChat(session chat.Session) *ProjectActivityLinkedChatResponse {
	item := &ProjectActivityLinkedChatResponse{
		ID:              session.ID,
		Title:           session.Title,
		AgentID:         renderChatAgentID(session),
		DriverKind:      session.DriverKind,
		NativeSessionID: session.NativeSessionID,
		Status:          session.Status,
		MessageCount:    len(session.Messages),
		CreatedAt:       formatOptionalTime(session.CreatedAt),
		UpdatedAt:       formatOptionalTime(session.UpdatedAt),
	}
	if latest := latestProjectActivityChatMessage(session.Messages); latest != nil {
		item.LatestMessageID = latest.ID
		item.LatestRole = latest.Role
		item.LatestStatus = latest.Status
		item.LatestError = latest.Error
		if !latest.CompletedAt.IsZero() {
			item.UpdatedAt = formatOptionalTime(latest.CompletedAt)
		} else if !latest.StartedAt.IsZero() {
			item.UpdatedAt = formatOptionalTime(latest.StartedAt)
		} else if !latest.CreatedAt.IsZero() {
			item.UpdatedAt = formatOptionalTime(latest.CreatedAt)
		}
	}
	return item
}

func latestProjectActivityChatMessage(messages []chat.Message) *chat.Message {
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if strings.TrimSpace(message.ID) != "" {
			return &messages[i]
		}
	}
	return nil
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

func (h *Handler) renderProjectActivity(ctx context.Context, projectID string) (ProjectActivityDataResponse, error) {
	roles, err := h.projectWork.ListRoles(ctx, projectID)
	if err != nil {
		return ProjectActivityDataResponse{}, err
	}
	workItems, err := h.projectWork.ListWorkItems(ctx, projectID)
	if err != nil {
		return ProjectActivityDataResponse{}, err
	}
	assignments, err := h.projectWork.ListAssignments(ctx, projectwork.AssignmentFilter{ProjectID: projectID})
	if err != nil {
		return ProjectActivityDataResponse{}, err
	}
	artifacts, err := h.projectWork.ListArtifacts(ctx, projectwork.ArtifactFilter{ProjectID: projectID})
	if err != nil {
		return ProjectActivityDataResponse{}, err
	}
	handoffs, err := h.projectWork.ListHandoffs(ctx, projectwork.HandoffFilter{ProjectID: projectID})
	if err != nil {
		return ProjectActivityDataResponse{}, err
	}

	roleByID := make(map[string]projectwork.AgentRoleProfile, len(roles))
	for _, role := range roles {
		roleByID[role.ID] = role
	}
	assignmentsByWorkItem := groupProjectWorkAssignmentsByWorkItem(assignments)
	linkedChats := h.projectActivityLinkedChats(ctx, projectID, assignments)
	projectedWorkItems := make(map[string]ProjectWorkItemResponse, len(workItems))
	for _, item := range workItems {
		projected, err := h.renderProjectedProjectWorkItemWithAssignments(ctx, item, assignmentsByWorkItem[item.ID])
		if err != nil {
			return ProjectActivityDataResponse{}, err
		}
		projectedWorkItems[item.ID] = projected
	}

	artifactsByAssignment, artifactsByWorkItem := groupProjectActivityArtifacts(artifacts)
	handoffsByAssignment, handoffsByWorkItem := groupProjectActivityHandoffs(handoffs)
	items := make([]ProjectActivityItemResponse, 0, len(assignments))
	for _, workItem := range projectedWorkItems {
		for _, projected := range workItem.Assignments {
			activityArtifacts := artifactsByAssignment[projected.ID]
			if len(activityArtifacts) == 0 {
				activityArtifacts = artifactsByWorkItem[projected.WorkItemID]
			}
			activityHandoffs := handoffsByAssignment[projected.ID]
			if len(activityHandoffs) == 0 {
				activityHandoffs = handoffsByWorkItem[projected.WorkItemID]
			}
			role, _ := roleByID[projected.RoleID]
			items = append(items, renderProjectActivityItem(workItem, projected, role, activityArtifacts, activityHandoffs, linkedChats[projected.ID]))
		}
	}
	sortProjectActivityItems(items)

	response := ProjectActivityDataResponse{
		ProjectID: projectID,
		Recent:    boundedProjectActivityItems(items, 20),
	}
	response.Summary.WorkItemCount = len(workItems)
	response.Summary.AssignmentCount = len(assignments)
	for _, item := range items {
		switch projectActivityBucket(item) {
		case "active":
			response.Buckets.Active = append(response.Buckets.Active, item)
			response.Summary.ActiveCount++
		case "blocked":
			response.Buckets.Blocked = append(response.Buckets.Blocked, item)
			response.Summary.BlockedCount++
		case "completed":
			response.Buckets.Completed = append(response.Buckets.Completed, item)
			response.Summary.CompletedCount++
		}
	}
	response.Buckets.Recent = response.Recent
	response.Summary.RecentCount = len(response.Recent)
	response.Buckets.Active = boundedProjectActivityItems(response.Buckets.Active, 20)
	response.Buckets.Blocked = boundedProjectActivityItems(response.Buckets.Blocked, 20)
	response.Buckets.Completed = boundedProjectActivityItems(response.Buckets.Completed, 20)
	return response, nil
}

func renderProjectActivityItem(workItem ProjectWorkItemResponse, assignment ProjectWorkAssignmentResponse, role projectwork.AgentRoleProfile, artifacts []projectwork.CollaborationArtifact, handoffs []projectwork.Handoff, linkedChat *ProjectActivityLinkedChatResponse) ProjectActivityItemResponse {
	artifactSummary, recentArtifacts := renderProjectActivityArtifactSignals(artifacts)
	handoffSummary, recentHandoffs := renderProjectActivityHandoffSignals(handoffs)
	status := strings.TrimSpace(projectActivityProjectedStatus(assignment, linkedChat))
	if status == "" {
		status = "unknown"
	}
	signal := projectActivityBlockingSignal(assignment, linkedChat)
	return ProjectActivityItemResponse{
		ID:              assignment.ID,
		ProjectID:       assignment.ProjectID,
		WorkItem:        renderProjectActivityWorkItem(workItem),
		Assignment:      assignment,
		Role:            renderProjectWorkRole(role),
		Status:          status,
		BlockingSignal:  signal,
		StatusSummary:   projectActivityStatusSummary(assignment, linkedChat, signal, artifactSummary.Count),
		LinkedTaskID:    firstNonEmpty(projectActivityExecutionTaskID(assignment), assignment.TaskID),
		LinkedRunID:     firstNonEmpty(projectActivityExecutionRunID(assignment), assignment.RunID),
		LinkedChatID:    assignment.ChatSessionID,
		LinkedChat:      linkedChat,
		LinkedMessageID: assignment.MessageID,
		RecentArtifacts: recentArtifacts,
		ArtifactSummary: artifactSummary,
		RecentHandoffs:  recentHandoffs,
		HandoffSummary:  handoffSummary,
		UpdatedAt:       projectActivityUpdatedAt(workItem, assignment, linkedChat, artifactSummary, handoffSummary),
	}
}

func renderProjectActivityWorkItem(item ProjectWorkItemResponse) ProjectActivityWorkItemResponse {
	return ProjectActivityWorkItemResponse{
		ID:       item.ID,
		Title:    item.Title,
		Status:   item.Status,
		Priority: item.Priority,
	}
}

func renderProjectActivityArtifactSignals(artifacts []projectwork.CollaborationArtifact) (ProjectActivityArtifactSummaryResponse, []ProjectWorkArtifactResponse) {
	if len(artifacts) == 0 {
		return ProjectActivityArtifactSummaryResponse{}, nil
	}
	items := append([]projectwork.CollaborationArtifact(nil), artifacts...)
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if !left.UpdatedAt.Equal(right.UpdatedAt) {
			return left.UpdatedAt.After(right.UpdatedAt)
		}
		if !left.CreatedAt.Equal(right.CreatedAt) {
			return left.CreatedAt.After(right.CreatedAt)
		}
		return left.ID < right.ID
	})
	latest := items[0]
	summary := ProjectActivityArtifactSummaryResponse{
		Count:        len(items),
		LatestKind:   latest.Kind,
		LatestTitle:  firstNonEmpty(latest.Title, latest.ID),
		LatestAt:     formatOptionalTime(firstNonZeroTime(latest.UpdatedAt, latest.CreatedAt)),
		AssignmentID: latest.AssignmentID,
	}
	limit := len(items)
	if limit > 3 {
		limit = 3
	}
	recent := make([]ProjectWorkArtifactResponse, 0, limit)
	for _, artifact := range items[:limit] {
		recent = append(recent, renderProjectWorkArtifact(artifact))
	}
	return summary, recent
}

func groupProjectActivityArtifacts(artifacts []projectwork.CollaborationArtifact) (map[string][]projectwork.CollaborationArtifact, map[string][]projectwork.CollaborationArtifact) {
	byAssignment := make(map[string][]projectwork.CollaborationArtifact)
	byWorkItem := make(map[string][]projectwork.CollaborationArtifact)
	for _, artifact := range artifacts {
		if artifact.AssignmentID != "" {
			byAssignment[artifact.AssignmentID] = append(byAssignment[artifact.AssignmentID], artifact)
			continue
		}
		byWorkItem[artifact.WorkItemID] = append(byWorkItem[artifact.WorkItemID], artifact)
	}
	return byAssignment, byWorkItem
}

func renderProjectActivityHandoffSignals(handoffs []projectwork.Handoff) (ProjectActivityHandoffSummaryResponse, []ProjectHandoffResponse) {
	if len(handoffs) == 0 {
		return ProjectActivityHandoffSummaryResponse{}, nil
	}
	items := append([]projectwork.Handoff(nil), handoffs...)
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if !left.UpdatedAt.Equal(right.UpdatedAt) {
			return left.UpdatedAt.After(right.UpdatedAt)
		}
		if !left.CreatedAt.Equal(right.CreatedAt) {
			return left.CreatedAt.After(right.CreatedAt)
		}
		return left.ID < right.ID
	})
	latest := items[0]
	summary := ProjectActivityHandoffSummaryResponse{
		Count:          len(items),
		LatestStatus:   latest.Status,
		LatestTitle:    firstNonEmpty(latest.Title, latest.ID),
		LatestAt:       formatOptionalTime(firstNonZeroTime(latest.UpdatedAt, latest.CreatedAt)),
		AssignmentID:   latest.SourceAssignmentID,
		TargetRoleID:   latest.TargetRoleID,
		TargetWorkItem: latest.TargetWorkItemID,
	}
	for _, item := range items {
		switch item.Status {
		case projectwork.HandoffStatusPending:
			summary.PendingCount++
		case projectwork.HandoffStatusAccepted:
			summary.AcceptedCount++
		}
	}
	limit := len(items)
	if limit > 3 {
		limit = 3
	}
	recent := make([]ProjectHandoffResponse, 0, limit)
	for _, handoff := range items[:limit] {
		recent = append(recent, renderProjectHandoff(handoff))
	}
	return summary, recent
}

func groupProjectActivityHandoffs(handoffs []projectwork.Handoff) (map[string][]projectwork.Handoff, map[string][]projectwork.Handoff) {
	byAssignment := make(map[string][]projectwork.Handoff)
	byWorkItem := make(map[string][]projectwork.Handoff)
	for _, handoff := range handoffs {
		attached := false
		if handoff.SourceAssignmentID != "" {
			byAssignment[handoff.SourceAssignmentID] = append(byAssignment[handoff.SourceAssignmentID], handoff)
			attached = true
		}
		if handoff.TargetAssignmentID != "" && handoff.TargetAssignmentID != handoff.SourceAssignmentID {
			byAssignment[handoff.TargetAssignmentID] = append(byAssignment[handoff.TargetAssignmentID], handoff)
			attached = true
		}
		if !attached {
			byWorkItem[handoff.WorkItemID] = append(byWorkItem[handoff.WorkItemID], handoff)
		}
	}
	return byAssignment, byWorkItem
}

func sortProjectActivityItems(items []ProjectActivityItemResponse) {
	sort.SliceStable(items, func(i, j int) bool {
		left, right := parseProjectActivityTime(items[i].UpdatedAt), parseProjectActivityTime(items[j].UpdatedAt)
		if !left.Equal(right) {
			return left.After(right)
		}
		return items[i].ID < items[j].ID
	})
}

func boundedProjectActivityItems(items []ProjectActivityItemResponse, limit int) []ProjectActivityItemResponse {
	if len(items) == 0 {
		return []ProjectActivityItemResponse{}
	}
	if limit > 0 && len(items) > limit {
		return append([]ProjectActivityItemResponse(nil), items[:limit]...)
	}
	return append([]ProjectActivityItemResponse(nil), items...)
}

func projectActivityBucket(item ProjectActivityItemResponse) string {
	switch item.BlockingSignal {
	case "awaiting_approval", "failed", "cancelled", "not_started", "stale_unknown":
		return "blocked"
	case "completed":
		return "completed"
	case "running":
		return "active"
	default:
		return "active"
	}
}

func projectActivityBlockingSignal(assignment ProjectWorkAssignmentResponse, linkedChat *ProjectActivityLinkedChatResponse) string {
	if linkedChat != nil && linkedChat.Missing {
		return "stale_unknown"
	}
	status := strings.TrimSpace(projectActivityProjectedStatus(assignment, linkedChat))
	switch status {
	case projectwork.AssignmentStatusAwaitingApproval:
		return "awaiting_approval"
	case projectwork.AssignmentStatusFailed:
		return "failed"
	case projectwork.AssignmentStatusCancelled, "closed":
		return "cancelled"
	case projectwork.AssignmentStatusCompleted:
		return "completed"
	case projectwork.AssignmentStatusRunning, projectwork.AssignmentStatusQueued:
		if assignment.Execution == nil && assignment.TaskID == "" && assignment.RunID != "" {
			return "stale_unknown"
		}
		if assignment.Execution != nil && assignment.Execution.Missing {
			return "stale_unknown"
		}
		if status == projectwork.AssignmentStatusQueued && assignment.TaskID == "" && assignment.RunID == "" && assignment.ChatSessionID == "" {
			return "not_started"
		}
		return "running"
	default:
		if assignment.Execution != nil && assignment.Execution.Missing {
			return "stale_unknown"
		}
		return "stale_unknown"
	}
}

func projectActivityStatusSummary(assignment ProjectWorkAssignmentResponse, linkedChat *ProjectActivityLinkedChatResponse, signal string, artifactCount int) string {
	switch signal {
	case "awaiting_approval":
		count := 0
		if assignment.Execution != nil {
			count = assignment.Execution.PendingApprovalCount
		}
		if count > 0 {
			return fmt.Sprintf("%d approval pending", count)
		}
		return "awaiting approval"
	case "failed":
		if linkedChat != nil && linkedChat.LatestError != "" {
			return linkedChat.LatestError
		}
		if assignment.Execution != nil && assignment.Execution.LastError != "" {
			return assignment.Execution.LastError
		}
		if linkedChat != nil {
			return "linked chat failed"
		}
		return "failed run"
	case "cancelled":
		if linkedChat != nil {
			return "linked chat cancelled"
		}
		return "cancelled"
	case "not_started":
		return "not started"
	case "running":
		if linkedChat != nil {
			return projectActivityLinkedChatSummary(linkedChat)
		}
		return "running"
	case "completed":
		if artifactCount > 0 {
			return fmt.Sprintf("completed with %d artifact%s", artifactCount, pluralSuffix(artifactCount))
		}
		return "completed"
	default:
		if linkedChat != nil && linkedChat.Missing {
			return "linked chat missing"
		}
		return "stale or unknown"
	}
}

func projectActivityProjectedStatus(assignment ProjectWorkAssignmentResponse, linkedChat *ProjectActivityLinkedChatResponse) string {
	if linkedChat != nil {
		if linkedChat.Missing {
			return "stale_unknown"
		}
		if status := strings.TrimSpace(linkedChat.Status); status != "" && status != "idle" {
			return status
		}
		if status := strings.TrimSpace(linkedChat.LatestStatus); status != "" {
			return status
		}
		if strings.TrimSpace(linkedChat.Status) == "idle" {
			return firstNonEmpty(projectActivityExecutionStatus(assignment), assignment.Status, projectwork.AssignmentStatusRunning)
		}
	}
	return firstNonEmpty(projectActivityExecutionStatus(assignment), assignment.Status)
}

func projectActivityLinkedChatSummary(linkedChat *ProjectActivityLinkedChatResponse) string {
	if linkedChat == nil {
		return ""
	}
	parts := []string{"linked chat"}
	if linkedChat.Status != "" {
		parts = append(parts, linkedChat.Status)
	}
	if linkedChat.LatestRole != "" && linkedChat.LatestStatus != "" {
		parts = append(parts, linkedChat.LatestRole+" "+linkedChat.LatestStatus)
	}
	if linkedChat.MessageCount > 0 {
		parts = append(parts, fmt.Sprintf("%d message%s", linkedChat.MessageCount, pluralSuffix(linkedChat.MessageCount)))
	}
	return strings.Join(parts, " · ")
}

func projectActivityExecutionStatus(assignment ProjectWorkAssignmentResponse) string {
	if assignment.Execution == nil {
		return ""
	}
	return assignment.Execution.Status
}

func projectActivityExecutionTaskID(assignment ProjectWorkAssignmentResponse) string {
	if assignment.Execution == nil {
		return ""
	}
	return assignment.Execution.TaskID
}

func projectActivityExecutionRunID(assignment ProjectWorkAssignmentResponse) string {
	if assignment.Execution == nil {
		return ""
	}
	return assignment.Execution.RunID
}

func projectActivityUpdatedAt(workItem ProjectWorkItemResponse, assignment ProjectWorkAssignmentResponse, linkedChat *ProjectActivityLinkedChatResponse, artifacts ProjectActivityArtifactSummaryResponse, handoffs ProjectActivityHandoffSummaryResponse) string {
	latest := parseProjectActivityTime(firstNonEmpty(assignment.CompletedAt, assignment.StartedAt, assignment.UpdatedAt, assignment.CreatedAt))
	workUpdated := parseProjectActivityTime(workItem.UpdatedAt)
	chatUpdated := time.Time{}
	if linkedChat != nil {
		chatUpdated = parseProjectActivityTime(linkedChat.UpdatedAt)
	}
	artifactUpdated := parseProjectActivityTime(artifacts.LatestAt)
	handoffUpdated := parseProjectActivityTime(handoffs.LatestAt)
	if workUpdated.After(latest) {
		latest = workUpdated
	}
	if chatUpdated.After(latest) {
		latest = chatUpdated
	}
	if artifactUpdated.After(latest) {
		latest = artifactUpdated
	}
	if handoffUpdated.After(latest) {
		latest = handoffUpdated
	}
	return formatOptionalTime(latest)
}

func parseProjectActivityTime(value string) time.Time {
	if strings.TrimSpace(value) == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
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
