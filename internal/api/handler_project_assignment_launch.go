package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/agentcontrols"
	"github.com/hecatehq/hecate/internal/cairnlinebridge"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/chatcontext"
	"github.com/hecatehq/hecate/internal/orchestrator"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/projectworkapp"
	"github.com/hecatehq/hecate/pkg/types"
)

const (
	projectAssignmentLaunchReadinessStatusReady   = "ready"
	projectAssignmentLaunchReadinessStatusBlocked = "blocked"
)

type ProjectAssignmentLaunchReadinessEnvelope struct {
	Object string                                   `json:"object"`
	Data   ProjectAssignmentLaunchReadinessResponse `json:"data"`
}

type ProjectAssignmentLaunchReadinessResponse struct {
	ProjectID        string                                             `json:"project_id"`
	WorkItemID       string                                             `json:"work_item_id"`
	AssignmentID     string                                             `json:"assignment_id"`
	ReadBackend      string                                             `json:"read_backend,omitempty"`
	GeneratedAt      string                                             `json:"generated_at"`
	Ready            bool                                               `json:"ready"`
	Status           string                                             `json:"status"`
	Title            string                                             `json:"title"`
	Detail           string                                             `json:"detail"`
	Blockers         []string                                           `json:"blockers"`
	Warnings         []string                                           `json:"warnings"`
	DriverKind       string                                             `json:"driver_kind"`
	Workspace        string                                             `json:"workspace,omitempty"`
	RootID           string                                             `json:"root_id,omitempty"`
	RootPath         string                                             `json:"root_path,omitempty"`
	Provider         string                                             `json:"provider,omitempty"`
	Model            string                                             `json:"model,omitempty"`
	ExecutionProfile string                                             `json:"execution_profile,omitempty"`
	ProfilePosture   *ProjectAssignmentLaunchProfilePostureResponseItem `json:"profile_posture,omitempty"`
	ExternalAgentID  string                                             `json:"external_agent_id,omitempty"`
	ExternalAgent    string                                             `json:"external_agent,omitempty"`
	SessionTitle     string                                             `json:"session_title,omitempty"`
	ModelReadiness   *ModelReadinessResponseItem                        `json:"model_readiness,omitempty"`
}

type ProjectAssignmentLaunchProfilePostureResponseItem struct {
	ID                  string `json:"id,omitempty"`
	Name                string `json:"name,omitempty"`
	Source              string `json:"source,omitempty"`
	Missing             bool   `json:"missing,omitempty"`
	ToolsEnabled        bool   `json:"tools_enabled"`
	WritesAllowed       bool   `json:"writes_allowed"`
	NetworkAllowed      bool   `json:"network_allowed"`
	ApprovalPolicy      string `json:"approval_policy,omitempty"`
	ProjectMemoryPolicy string `json:"project_memory_policy,omitempty"`
	ContextSourcePolicy string `json:"context_source_policy,omitempty"`
}

func (h *Handler) HandleProjectWorkAssignmentLaunchReadiness(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectID := r.PathValue("id")
	workItemID := r.PathValue("work_item_id")
	assignmentID := r.PathValue("assignment_id")
	if h.projectReadRoutesUseCairnlineReadModel() {
		readiness, err := h.renderCairnlineProjectAssignmentLaunchReadiness(ctx, projectID, workItemID, assignmentID)
		if err != nil {
			WriteError(w, projectAssignmentPreflightHTTPStatus(err), projectAssignmentPreflightErrorCode(err), err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, ProjectAssignmentLaunchReadinessEnvelope{Object: "project_assignment_launch_readiness", Data: readiness})
		return
	}
	if h.projects == nil || h.projectWork == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "project stores are not configured")
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
	role, roleOK, err := h.loadProjectWorkRole(ctx, projectID, assignment.RoleID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	readiness, err := h.renderProjectAssignmentLaunchReadiness(ctx, project, workItem, assignment, role, roleOK)
	if err != nil {
		WriteError(w, projectAssignmentPreflightHTTPStatus(err), projectAssignmentPreflightErrorCode(err), err.Error())
		return
	}
	readiness.ReadBackend = "hecate"
	WriteJSON(w, http.StatusOK, ProjectAssignmentLaunchReadinessEnvelope{Object: "project_assignment_launch_readiness", Data: readiness})
}

func (h *Handler) HandleProjectWorkAssignmentPreflight(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectID := r.PathValue("id")
	workItemID := r.PathValue("work_item_id")
	assignmentID := r.PathValue("assignment_id")
	if h.projectReadRoutesUseCairnlineReadModel() {
		inputs, err := h.cairnlineProjectAssignmentLaunchInputs(ctx, projectID, workItemID, assignmentID)
		if err != nil {
			WriteError(w, projectAssignmentPreflightHTTPStatus(err), projectAssignmentPreflightErrorCode(err), err.Error())
			return
		}
		if !inputs.RoleOK {
			WriteError(w, http.StatusNotFound, errCodeNotFound, "assignment role not found")
			return
		}
		if projectWorkAssignmentIsTerminal(inputs.Assignment.Status) {
			WriteError(w, http.StatusConflict, errCodeConflict, "terminal assignments cannot be started")
			return
		}
		contextPacket, err := h.projectAssignmentPreflightContext(ctx, inputs.Project, inputs.WorkItem, inputs.Assignment, inputs.Role)
		if err != nil {
			WriteError(w, projectAssignmentPreflightHTTPStatus(err), projectAssignmentPreflightErrorCode(err), err.Error())
			return
		}
		writeChatContextPacket(w, contextPacket)
		return
	}
	if h.projects == nil || h.projectWork == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "project stores are not configured")
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
	if projectWorkAssignmentIsTerminal(assignment.Status) {
		WriteError(w, http.StatusConflict, errCodeConflict, "terminal assignments cannot be started")
		return
	}

	contextPacket, err := h.projectAssignmentPreflightContext(ctx, project, workItem, assignment, role)
	if err != nil {
		WriteError(w, projectAssignmentPreflightHTTPStatus(err), projectAssignmentPreflightErrorCode(err), err.Error())
		return
	}
	writeChatContextPacket(w, contextPacket)
}

type projectAssignmentPreflightError struct {
	status  int
	code    string
	message string
}

func (err projectAssignmentPreflightError) Error() string {
	return err.message
}

func newProjectAssignmentPreflightError(status int, code, message string) error {
	return projectAssignmentPreflightError{status: status, code: code, message: message}
}

func projectAssignmentPreflightHTTPStatus(err error) int {
	var preflightErr projectAssignmentPreflightError
	if errors.As(err, &preflightErr) && preflightErr.status != 0 {
		return preflightErr.status
	}
	var launchErr projectworkapp.LaunchPlanError
	if errors.As(err, &launchErr) {
		switch launchErr.Kind {
		case projectworkapp.LaunchPlanInvalidRequest:
			return http.StatusBadRequest
		case projectworkapp.LaunchPlanUnprocessable:
			return http.StatusUnprocessableEntity
		case projectworkapp.LaunchPlanModelNotConfigured:
			return http.StatusUnprocessableEntity
		case projectworkapp.LaunchPlanAdapterNotFound:
			return http.StatusNotFound
		}
	}
	return http.StatusInternalServerError
}

func projectAssignmentPreflightErrorCode(err error) string {
	var preflightErr projectAssignmentPreflightError
	if errors.As(err, &preflightErr) && preflightErr.code != "" {
		return preflightErr.code
	}
	var launchErr projectworkapp.LaunchPlanError
	if errors.As(err, &launchErr) {
		switch launchErr.Kind {
		case projectworkapp.LaunchPlanInvalidRequest:
			return errCodeInvalidRequest
		case projectworkapp.LaunchPlanUnprocessable:
			return errCodeInvalidRequest
		case projectworkapp.LaunchPlanModelNotConfigured:
			return errCodeModelNotConfigured
		case projectworkapp.LaunchPlanAdapterNotFound:
			return errCodeNotFound
		}
	}
	return errCodeGatewayError
}

func (h *Handler) renderProjectAssignmentLaunchReadiness(ctx context.Context, project projects.Project, workItem projectwork.WorkItem, assignment projectwork.Assignment, role projectwork.AgentRoleProfile, roleOK bool) (ProjectAssignmentLaunchReadinessResponse, error) {
	driverKind := firstNonEmptyString(strings.TrimSpace(assignment.DriverKind), projectwork.AssignmentDriverHecateTask)
	readiness := ProjectAssignmentLaunchReadinessResponse{
		ProjectID:    project.ID,
		WorkItemID:   workItem.ID,
		AssignmentID: assignment.ID,
		GeneratedAt:  formatOptionalTime(time.Now().UTC()),
		Status:       projectAssignmentLaunchReadinessStatusReady,
		Title:        "Ready to start assignment",
		Detail:       "Launch checks are clear. Review the preflight context before starting this assignment.",
		DriverKind:   driverKind,
		Blockers:     []string{},
		Warnings:     []string{},
	}
	if !roleOK {
		role = projectwork.AgentRoleProfile{ID: strings.TrimSpace(assignment.RoleID)}
		readiness.Blockers = append(readiness.Blockers, "Assignment role not found.")
	}
	if status := strings.TrimSpace(assignment.Status); status != "" && status != projectwork.AssignmentStatusQueued {
		if projectWorkAssignmentIsTerminal(status) {
			readiness.Blockers = append(readiness.Blockers, "Terminal assignments cannot be started.")
		} else {
			readiness.Blockers = append(readiness.Blockers, "Only queued assignments can be started.")
		}
	}

	switch driverKind {
	case projectwork.AssignmentDriverHecateTask:
		if h.taskStore == nil {
			readiness.Blockers = append(readiness.Blockers, "Task store is not configured.")
		}
		if h.taskRunner == nil {
			readiness.Blockers = append(readiness.Blockers, "Task runner is not configured.")
		}
		if h.taskStore != nil {
			active, err := projectWorkAssignmentHasActiveExecution(ctx, h.taskStore, assignment)
			if err != nil {
				return ProjectAssignmentLaunchReadinessResponse{}, err
			}
			if active {
				readiness.Blockers = append(readiness.Blockers, "Assignment already has active execution.")
			}
		}
		if len(readiness.Blockers) == 0 {
			if err := h.populateTaskAssignmentLaunchReadiness(ctx, project, workItem, assignment, role, &readiness); err != nil {
				return ProjectAssignmentLaunchReadinessResponse{}, err
			}
		}
	case projectwork.AssignmentDriverExternalAgent:
		if h.agentChat == nil {
			readiness.Blockers = append(readiness.Blockers, "Agent chat store is not configured.")
		}
		if h.agentChatRunner == nil {
			readiness.Blockers = append(readiness.Blockers, "Agent chat runner is not configured.")
		}
		if strings.TrimSpace(assignment.ExecutionRef.ChatSessionID) != "" {
			readiness.Blockers = append(readiness.Blockers, "External Agent assignment already has a prepared chat session.")
		}
		if len(readiness.Blockers) == 0 {
			if err := h.populateExternalAgentAssignmentLaunchReadiness(ctx, project, workItem, assignment, role, &readiness); err != nil {
				return ProjectAssignmentLaunchReadinessResponse{}, err
			}
		}
	default:
		readiness.Blockers = append(readiness.Blockers, fmt.Sprintf("Assignment driver_kind %q is not supported.", driverKind))
	}

	readiness.Blockers = projectworkapp.UniqueReadinessStrings(readiness.Blockers)
	readiness.Warnings = projectworkapp.UniqueReadinessStrings(readiness.Warnings)
	readiness.Ready = len(readiness.Blockers) == 0
	if !readiness.Ready {
		readiness.Status = projectAssignmentLaunchReadinessStatusBlocked
		readiness.Title = "Launch is blocked"
		readiness.Detail = "Resolve the listed launch blockers before starting or preparing this assignment."
	}
	return readiness, nil
}

func (h *Handler) renderCairnlineProjectAssignmentLaunchReadiness(ctx context.Context, projectID, workItemID, assignmentID string) (ProjectAssignmentLaunchReadinessResponse, error) {
	inputs, err := h.cairnlineProjectAssignmentLaunchInputs(ctx, projectID, workItemID, assignmentID)
	if err != nil {
		return ProjectAssignmentLaunchReadinessResponse{}, err
	}
	readiness, err := h.renderProjectAssignmentLaunchReadiness(ctx, inputs.Project, inputs.WorkItem, inputs.Assignment, inputs.Role, inputs.RoleOK)
	if err != nil {
		return ProjectAssignmentLaunchReadinessResponse{}, err
	}
	readiness.ReadBackend = "cairnline"
	return readiness, nil
}

type projectAssignmentLaunchInputs struct {
	Project               projects.Project
	WorkItem              projectwork.WorkItem
	Assignment            projectwork.Assignment
	Role                  projectwork.AgentRoleProfile
	RoleOK                bool
	CairnlineLaunchPacket cairnline.AssignmentLaunchPacket
}

func (h *Handler) cairnlineProjectAssignmentLaunchInputs(ctx context.Context, projectID, workItemID, assignmentID string) (projectAssignmentLaunchInputs, error) {
	view, err := h.cairnlineProjectWorkView(ctx, projectID)
	if errors.Is(err, projects.ErrNotFound) {
		return projectAssignmentLaunchInputs{}, newProjectAssignmentPreflightError(http.StatusNotFound, errCodeNotFound, "project not found")
	}
	if err != nil {
		return projectAssignmentLaunchInputs{}, err
	}
	defer view.Close()
	service, snapshot := view.service, view.snapshot
	if ok, err := cairnlineProjectWorkItemExists(ctx, service, snapshot.Project.ID, workItemID); err != nil {
		return projectAssignmentLaunchInputs{}, err
	} else if !ok {
		return projectAssignmentLaunchInputs{}, newProjectAssignmentPreflightError(http.StatusNotFound, errCodeNotFound, "work item not found")
	}
	launch, err := service.AssignmentLaunchPacket(ctx, snapshot.Project.ID, assignmentID)
	if errors.Is(err, cairnline.ErrNotFound) {
		return projectAssignmentLaunchInputs{}, newProjectAssignmentPreflightError(http.StatusNotFound, errCodeNotFound, "assignment not found")
	}
	if err != nil {
		return projectAssignmentLaunchInputs{}, err
	}
	if strings.TrimSpace(launch.WorkItem.ID) != strings.TrimSpace(workItemID) {
		return projectAssignmentLaunchInputs{}, newProjectAssignmentPreflightError(http.StatusNotFound, errCodeNotFound, "assignment not found")
	}
	project := projectFromCairnline(launch.Project, snapshot.Project)
	workItem := projectWorkItemFromCairnline(launch.WorkItem)
	assignment := projectWorkAssignmentFromCairnline(launch.Assignment)
	assignment, err = h.cairnlineAssignmentWithRuntimeRef(ctx, project.ID, assignment, snapshot.Assignments)
	if err != nil {
		return projectAssignmentLaunchInputs{}, err
	}
	role, roleOK, err := cairnlineLaunchRole(ctx, service, snapshot, launch)
	if err != nil {
		return projectAssignmentLaunchInputs{}, err
	}
	if roleOK {
		role, err = h.projectRoleWithHecateRuntimeOverlay(ctx, role)
		if err != nil {
			return projectAssignmentLaunchInputs{}, err
		}
	}
	return projectAssignmentLaunchInputs{
		Project:    project,
		WorkItem:   workItem,
		Assignment: assignment,
		Role:       role,
		RoleOK:     roleOK,
	}, nil
}

func (h *Handler) cairnlineAssignmentWithRuntimeRef(ctx context.Context, projectID string, assignment projectwork.Assignment, nativeAssignments []projectwork.Assignment) (projectwork.Assignment, error) {
	if native, ok := projectWorkAssignmentsByID(nativeAssignments)[assignment.ID]; ok {
		overlayProjectAssignmentRuntimeShadow(&assignment, native)
	}
	if h != nil {
		overlaid, err := h.projectWorkApplication().ApplyAssignmentRuntime(ctx, assignment)
		if err != nil {
			return assignment, err
		}
		assignment = overlaid
	}
	if h == nil || h.projectWork == nil {
		return assignment, nil
	}
	native, ok, err := h.loadProjectWorkAssignmentForCairnlineMirror(ctx, projectID, assignment.ID)
	if err != nil {
		return assignment, err
	}
	if !ok {
		return assignment, nil
	}
	overlayProjectAssignmentRuntimeShadow(&assignment, native)
	return h.projectWorkApplication().ApplyAssignmentRuntime(ctx, assignment)
}

func cairnlineProjectWorkItemExists(ctx context.Context, service *cairnline.Service, projectID, workItemID string) (bool, error) {
	items, err := service.ListWorkItems(ctx, projectID)
	if err != nil {
		return false, err
	}
	workItemID = strings.TrimSpace(workItemID)
	for _, item := range items {
		if strings.TrimSpace(item.ID) == workItemID {
			return true, nil
		}
	}
	return false, nil
}

func cairnlineLaunchRole(ctx context.Context, service *cairnline.Service, snapshot cairnlinebridge.Snapshot, launch cairnline.AssignmentLaunchPacket) (projectwork.AgentRoleProfile, bool, error) {
	if launch.Role == nil {
		return projectwork.AgentRoleProfile{ID: strings.TrimSpace(launch.Assignment.RoleID)}, false, nil
	}
	role := projectWorkRoleFromCairnline(*launch.Role, projectWorkRolesByID(snapshot.Roles)[launch.Role.ID])
	if strings.TrimSpace(role.ProjectID) == "" {
		role.ProjectID = strings.TrimSpace(firstNonEmpty(launch.Role.ProjectID, launch.Assignment.ProjectID, launch.Project.ID))
	}
	if strings.TrimSpace(role.DefaultDriverKind) == "" {
		role.DefaultDriverKind = projectWorkAssignmentDriverFromCairnline(launch.Assignment.ExecutionMode)
	}
	return role, true, nil
}

func (h *Handler) populateTaskAssignmentLaunchReadiness(ctx context.Context, project projects.Project, workItem projectwork.WorkItem, assignment projectwork.Assignment, role projectwork.AgentRoleProfile, readiness *ProjectAssignmentLaunchReadinessResponse) error {
	plan, err := h.projectWorkApplication().ResolveTaskAssignmentLaunchPlan(ctx, project, workItem, assignment, role)
	if err != nil {
		readiness.Blockers = append(readiness.Blockers, projectAssignmentLaunchPlanBlocker(err))
		return nil
	}
	readiness.Workspace = plan.WorkingDirectory
	readiness.RootID = plan.Root.ID
	readiness.RootPath = plan.Root.Path
	readiness.Provider = plan.RequestedProvider
	readiness.Model = plan.RequestedModel
	readiness.ExecutionProfile = plan.ExecutionProfile
	readiness.ProfilePosture = renderProjectAssignmentLaunchProfilePosture(plan.Profile)
	readiness.Warnings = append(readiness.Warnings, projectAssignmentLaunchPlanWarnings(plan.Profile, plan.ResolvedSkills)...)
	if h.service == nil {
		return nil
	}
	result, err := h.service.ProviderModelReadiness(ctx, plan.RequestedProvider, plan.RequestedModel)
	if err != nil {
		return err
	}
	modelReadiness := renderModelReadiness(result.Readiness.ToModelReadiness())
	readiness.ModelReadiness = &modelReadiness
	if !modelReadiness.Ready {
		readiness.Blockers = append(readiness.Blockers, projectAssignmentModelReadinessBlocker(modelReadiness))
	}
	return nil
}

func (h *Handler) populateExternalAgentAssignmentLaunchReadiness(ctx context.Context, project projects.Project, workItem projectwork.WorkItem, assignment projectwork.Assignment, role projectwork.AgentRoleProfile, readiness *ProjectAssignmentLaunchReadinessResponse) error {
	plan, err := h.projectWorkApplication().ResolveExternalAgentAssignmentLaunchPlan(ctx, project, workItem, assignment, role)
	if err != nil {
		readiness.Blockers = append(readiness.Blockers, projectAssignmentLaunchPlanBlocker(err))
		return nil
	}
	readiness.Workspace = plan.Workspace
	readiness.RootID = plan.Root.ID
	readiness.RootPath = plan.Root.Path
	readiness.ExecutionProfile = plan.ExecutionProfile
	readiness.ProfilePosture = renderProjectAssignmentLaunchProfilePosture(plan.Profile)
	readiness.ExternalAgentID = plan.AdapterID
	readiness.ExternalAgent = firstNonEmptyString(plan.Adapter.Name, plan.AdapterID)
	readiness.SessionTitle = plan.SessionTitle
	readiness.Title = "Ready to prepare External Agent chat"
	readiness.Detail = "Launch checks are clear. Review the preflight context before preparing this supervised External Agent chat."
	readiness.Warnings = append(readiness.Warnings, projectAssignmentLaunchPlanWarnings(plan.Profile, plan.ResolvedSkills)...)
	return nil
}

func renderProjectAssignmentLaunchProfilePosture(profile projectworkapp.ResolvedAgentProfile) *ProjectAssignmentLaunchProfilePostureResponseItem {
	return &ProjectAssignmentLaunchProfilePostureResponseItem{
		ID:                  profile.ID,
		Name:                profile.Name,
		Source:              profile.Source,
		Missing:             profile.Missing,
		ToolsEnabled:        profile.ToolsEnabled,
		WritesAllowed:       profile.WritesAllowed,
		NetworkAllowed:      profile.NetworkAllowed,
		ApprovalPolicy:      profile.ApprovalPolicy,
		ProjectMemoryPolicy: profile.ProjectMemoryPolicy,
		ContextSourcePolicy: profile.ContextSourcePolicy,
	}
}

func projectAssignmentLaunchPlanBlocker(err error) string {
	var launchErr projectworkapp.LaunchPlanError
	if errors.As(err, &launchErr) && strings.TrimSpace(launchErr.Message) != "" {
		return launchErr.Message
	}
	if strings.TrimSpace(err.Error()) != "" {
		return err.Error()
	}
	return "Launch plan could not be resolved."
}

func projectAssignmentLaunchPlanWarnings(profile projectworkapp.ResolvedAgentProfile, skills projectworkapp.ResolvedProjectSkills) []string {
	warnings := append([]string(nil), profile.Warnings...)
	warnings = append(warnings, skills.Warnings...)
	for _, skipped := range skills.Skipped {
		if skipped.ID == "" {
			continue
		}
		warnings = append(warnings, "Project skill "+skipped.ID+" was not included: "+firstNonEmptyString(skipped.Reason, "unavailable"))
	}
	return warnings
}

func projectAssignmentModelReadinessBlocker(readiness ModelReadinessResponseItem) string {
	return firstNonEmptyString(
		strings.TrimSpace(readiness.Message),
		strings.TrimSpace(readiness.OperatorAction),
		strings.TrimSpace(readiness.Reason),
		"The selected provider/model cannot be routed for this assignment.",
	)
}

func (h *Handler) projectAssignmentPreflightContext(ctx context.Context, project projects.Project, workItem projectwork.WorkItem, assignment projectwork.Assignment, role projectwork.AgentRoleProfile) (chat.ContextPacket, error) {
	if status := strings.TrimSpace(assignment.Status); status != "" && status != projectwork.AssignmentStatusQueued {
		return chat.ContextPacket{}, newProjectAssignmentPreflightError(http.StatusConflict, errCodeConflict, "only queued assignments can be preflighted before launch")
	}
	switch assignment.DriverKind {
	case projectwork.AssignmentDriverHecateTask, "":
		return h.projectHecateTaskAssignmentPreflightContext(ctx, project, workItem, assignment, role)
	case projectwork.AssignmentDriverExternalAgent:
		return h.projectExternalAgentAssignmentPreflightContext(ctx, project, workItem, assignment, role)
	default:
		return chat.ContextPacket{}, newProjectAssignmentPreflightError(http.StatusConflict, errCodeConflict, fmt.Sprintf("assignment driver_kind %q is not supported; V1 supports %q and %q", assignment.DriverKind, projectwork.AssignmentDriverHecateTask, projectwork.AssignmentDriverExternalAgent))
	}
}

func (h *Handler) projectHecateTaskAssignmentPreflightContext(ctx context.Context, project projects.Project, workItem projectwork.WorkItem, assignment projectwork.Assignment, role projectwork.AgentRoleProfile) (chat.ContextPacket, error) {
	if h.taskStore == nil {
		return chat.ContextPacket{}, newProjectAssignmentPreflightError(http.StatusBadRequest, errCodeInvalidRequest, "task store is not configured")
	}
	if h.taskRunner == nil {
		return chat.ContextPacket{}, newProjectAssignmentPreflightError(http.StatusBadRequest, errCodeInvalidRequest, "task runner is not configured")
	}
	active, err := projectWorkAssignmentHasActiveExecution(ctx, h.taskStore, assignment)
	if err != nil {
		return chat.ContextPacket{}, err
	}
	if active {
		return chat.ContextPacket{}, newProjectAssignmentPreflightError(http.StatusConflict, errCodeConflict, "assignment already has active execution")
	}
	plan, err := h.projectWorkApplication().ResolveTaskAssignmentLaunchPlan(ctx, project, workItem, assignment, role)
	if err != nil {
		return chat.ContextPacket{}, err
	}
	packet := h.projectAssignmentContextPacket(ctx, project, workItem, assignment, role, plan.Root, plan.WorkingDirectory, plan.RequestedProvider, plan.RequestedModel, plan.ExecutionProfile, plan.Profile, plan.ResolvedSkills, plan.PromptContext)
	if err := h.appendProjectAssignmentLaunchReadiness(ctx, &packet, plan.RequestedProvider, plan.RequestedModel); err != nil {
		return chat.ContextPacket{}, err
	}
	h.appendCairnlineAssignmentLaunchPacketEvidence(ctx, &packet, assignment)
	appendProjectAssignmentLaunchPreflight(&packet, projectwork.AssignmentDriverHecateTask, []string{
		"Task: created on start",
		"Run: created on start",
		"Workspace: " + plan.WorkingDirectory,
	})
	return chatcontext.Normalize(packet, chatcontext.ProjectAssignmentRefs(project.ID, workItem.ID, assignment.ID, role.ID)), nil
}

func (h *Handler) projectExternalAgentAssignmentPreflightContext(ctx context.Context, project projects.Project, workItem projectwork.WorkItem, assignment projectwork.Assignment, role projectwork.AgentRoleProfile) (chat.ContextPacket, error) {
	if h.agentChat == nil {
		return chat.ContextPacket{}, newProjectAssignmentPreflightError(http.StatusInternalServerError, errCodeGatewayError, "agent chat store is not configured")
	}
	if h.agentChatRunner == nil {
		return chat.ContextPacket{}, newProjectAssignmentPreflightError(http.StatusInternalServerError, errCodeGatewayError, "agent chat runner is not configured")
	}
	if strings.TrimSpace(assignment.ExecutionRef.ChatSessionID) != "" {
		return chat.ContextPacket{}, newProjectAssignmentPreflightError(http.StatusConflict, errCodeConflict, "external-agent assignment already has a prepared chat session")
	}
	plan, err := h.projectWorkApplication().ResolveExternalAgentAssignmentLaunchPlan(ctx, project, workItem, assignment, role)
	if err != nil {
		var launchErr projectworkapp.LaunchPlanError
		if errors.As(err, &launchErr) && launchErr.Kind == projectworkapp.LaunchPlanAdapterNotFound {
			return chat.ContextPacket{}, newProjectAssignmentPreflightError(http.StatusNotFound, errCodeNotFound, err.Error())
		}
		return chat.ContextPacket{}, err
	}
	packet := h.projectExternalAgentAssignmentContextPacket(ctx, project, workItem, assignment, role, plan, "")
	h.appendCairnlineAssignmentLaunchPacketEvidence(ctx, &packet, assignment)
	appendProjectAssignmentLaunchPreflight(&packet, projectwork.AssignmentDriverExternalAgent, []string{
		"External agent: " + firstNonEmptyString(plan.Adapter.Name, plan.AdapterID),
		"Adapter ID: " + plan.AdapterID,
		"Chat session: created when the assignment is prepared",
		"Session title: " + plan.SessionTitle,
		"Workspace: " + plan.Workspace,
		"Config options: " + formatProjectExternalAgentConfigOptions(plan.ConfigOptions),
	})
	return chatcontext.Normalize(packet, chatcontext.ProjectAssignmentRefs(project.ID, workItem.ID, assignment.ID, role.ID)), nil
}

func (h *Handler) HandleStartProjectWorkAssignment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectID := r.PathValue("id")
	workItemID := r.PathValue("work_item_id")
	assignmentID := r.PathValue("assignment_id")
	strictEmbeddedRead := h.projectReadRoutesUseCairnlineReadModel() && h.requiresEmbeddedCairnlineProjectReads()
	if !strictEmbeddedRead && (h.projects == nil || h.projectWork == nil) {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "project stores are not configured")
		return
	}
	req, ok := decodeOptionalProjectWorkAssignmentStartRequest(w, r)
	if !ok {
		return
	}

	inputs, err := h.projectAssignmentStartInputs(ctx, projectID, workItemID, assignmentID, strictEmbeddedRead)
	if err != nil {
		WriteError(w, projectAssignmentPreflightHTTPStatus(err), projectAssignmentPreflightErrorCode(err), err.Error())
		return
	}
	project, workItem, assignment, role := inputs.Project, inputs.WorkItem, inputs.Assignment, inputs.Role
	strictEmbeddedRuntime := h.projectAssignmentStartUsesStrictEmbeddedCairnlineRuntime(strictEmbeddedRead)
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
		if strictEmbeddedRead && !strictEmbeddedRuntime && h.projectWork != nil {
			inputs.Assignment = assignment
			shadowedAssignment, err := h.shadowCairnlineAssignmentStartInputsToHecate(ctx, inputs)
			if err != nil {
				WriteError(w, projectAssignmentPreflightHTTPStatus(err), projectAssignmentPreflightErrorCode(err), err.Error())
				return
			}
			assignment = shadowedAssignment
		}
		h.startProjectExternalAgentAssignment(w, r, project, workItem, assignment, role)
		return
	}
	if assignment.DriverKind != projectwork.AssignmentDriverHecateTask {
		WriteError(w, http.StatusConflict, errCodeConflict, fmt.Sprintf("assignment driver_kind %q is not supported; V1 supports %q and %q", assignment.DriverKind, projectwork.AssignmentDriverHecateTask, projectwork.AssignmentDriverExternalAgent))
		return
	}
	if h.taskStore == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "task store is not configured")
		return
	}
	if h.taskRunner == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "task runner is not configured")
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

	plan, err := h.projectWorkApplication().ResolveTaskAssignmentLaunchPlan(ctx, project, workItem, assignment, role)
	if err != nil {
		WriteError(w, projectAssignmentPreflightHTTPStatus(err), projectAssignmentPreflightErrorCode(err), err.Error())
		return
	}
	contextPacket := h.projectAssignmentContextPacket(ctx, project, workItem, assignment, role, plan.Root, plan.WorkingDirectory, plan.RequestedProvider, plan.RequestedModel, plan.ExecutionProfile, plan.Profile, plan.ResolvedSkills, plan.PromptContext)
	if err := h.appendProjectAssignmentLaunchReadiness(ctx, &contextPacket, plan.RequestedProvider, plan.RequestedModel); err != nil {
		h.logProjectAssignmentLaunchReadinessError(ctx, err)
	}
	h.appendCairnlineAssignmentLaunchPacketEvidence(ctx, &contextPacket, assignment)
	if contextPacket.ID == "" {
		contextPacket.ID = newChatID("ctx")
	}

	if strictEmbeddedRuntime {
		result, err := h.startStrictEmbeddedCairnlineTaskAssignment(ctx, project, workItem, assignment, role, plan, contextPacket)
		h.writeProjectTaskAssignmentStartResult(w, ctx, assignment, result, err, false)
		return
	}
	if strictEmbeddedRead {
		inputs.Assignment = assignment
		shadowedAssignment, err := h.shadowCairnlineAssignmentStartInputsToHecate(ctx, inputs)
		if err != nil {
			WriteError(w, projectAssignmentPreflightHTTPStatus(err), projectAssignmentPreflightErrorCode(err), err.Error())
			return
		}
		assignment = shadowedAssignment
	}

	startClaimedBy := projectAssignmentStartClaimedBy(assignment)
	claimedAssignment, cairnlineStartClaimed, err := h.claimProjectAssignmentStartInCairnlineAuthority(ctx, project, assignment, startClaimedBy)
	if err != nil {
		if errors.Is(err, projectworkapp.ErrAssignmentStartConflict) {
			projected, projectErr := h.renderProjectedProjectWorkAssignment(ctx, claimedAssignment)
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
	result, err := h.projectWorkApplication().StartTaskAssignment(ctx, projectworkapp.StartTaskAssignmentCommand{
		ProjectID:         projectID,
		WorkItemID:        workItemID,
		Assignment:        assignment,
		ContextSnapshotID: contextPacket.ID,
		BuildTask: func(taskID string) (types.Task, error) {
			return projectworkapp.NewAssignmentTask(taskID, project, workItem, assignment, role, plan, time.Now().UTC()), nil
		},
		OnTaskCreated: func(task types.Task) {
			contextPacket.Refs.TaskID = task.ID
		},
		InitializeRun: func(task types.Task, run *types.TaskRun) {
			contextPacket.Refs.RunID = run.ID
			run.ContextPacket = chatcontext.Marshal(chatcontext.Normalize(contextPacket, chatcontext.MergeRefs(
				chatcontext.TaskRunRefs(task.ID, run.ID, project.ID),
				chatcontext.ProjectAssignmentRefs(project.ID, workItem.ID, assignment.ID, role.ID),
			)))
		},
	})
	if err != nil && cairnlineStartClaimed {
		if result == nil || result.Assignment.ID == "" {
			h.releaseProjectAssignmentStartInCairnlineAuthority(ctx, assignment, startClaimedBy)
		}
	}
	h.writeProjectTaskAssignmentStartResult(w, ctx, assignment, result, err, true)
}

func (h *Handler) projectAssignmentStartUsesStrictEmbeddedCairnlineRuntime(strictEmbeddedRead bool) bool {
	if !strictEmbeddedRead {
		return false
	}
	return h.projectWork == nil || h.projectCairnlineEmbeddedReplacementModeArmed()
}

func (h *Handler) writeProjectTaskAssignmentStartResult(w http.ResponseWriter, ctx context.Context, assignment projectwork.Assignment, result *projectworkapp.StartTaskAssignmentResult, err error, mirrorStartResult bool) {
	if err != nil {
		resultAssignment := assignment
		if result != nil && result.Assignment.ID != "" {
			resultAssignment = result.Assignment
			if mirrorStartResult && h.projectWork != nil {
				h.mirrorProjectAssignmentStartResultToCairnline(ctx, result.Assignment)
			}
		}
		var launchErr projectAssignmentPreflightError
		if errors.As(err, &launchErr) {
			WriteError(w, projectAssignmentPreflightHTTPStatus(err), projectAssignmentPreflightErrorCode(err), err.Error())
			return
		}
		if errors.Is(err, projectworkapp.ErrAssignmentStartConflict) {
			projected, projectErr := h.renderProjectedProjectWorkAssignment(ctx, resultAssignment)
			if projectErr != nil {
				WriteError(w, http.StatusInternalServerError, errCodeGatewayError, projectErr.Error())
				return
			}
			if !mirrorStartResult {
				projected.ReadBackend = "cairnline"
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
	if mirrorStartResult && h.projectWork != nil {
		h.mirrorProjectAssignmentStartResultToCairnline(ctx, result.Assignment)
	}
	projected, err := h.renderProjectedProjectWorkAssignment(ctx, result.Assignment)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if !mirrorStartResult {
		projected.ReadBackend = "cairnline"
	}
	WriteJSON(w, http.StatusOK, ProjectWorkAssignmentEnvelope{Object: "project_assignment", Data: projected})
}

func (h *Handler) startStrictEmbeddedCairnlineTaskAssignment(ctx context.Context, project projects.Project, workItem projectwork.WorkItem, assignment projectwork.Assignment, role projectwork.AgentRoleProfile, plan projectworkapp.TaskAssignmentLaunchPlan, contextPacket chat.ContextPacket) (*projectworkapp.StartTaskAssignmentResult, error) {
	claimed, err := h.claimStrictEmbeddedCairnlineAssignment(ctx, assignment)
	if err != nil {
		return &projectworkapp.StartTaskAssignmentResult{Assignment: assignment}, err
	}
	assignment = claimed
	taskID := newOpaqueTaskResourceID("task")
	task := projectworkapp.NewAssignmentTask(taskID, project, workItem, assignment, role, plan, time.Now().UTC())
	task, err = h.taskStore.CreateTask(ctx, task)
	if err != nil {
		return &projectworkapp.StartTaskAssignmentResult{Assignment: assignment}, errors.Join(err, h.releaseStrictEmbeddedCairnlineAssignmentClaim(context.Background(), assignment.ProjectID, assignment.ID))
	}
	contextPacket.Refs.TaskID = task.ID
	result, err := h.taskRunner.StartTaskWithRunInitializer(ctx, task, newOpaqueTaskResourceID, func(run *types.TaskRun) {
		contextPacket.Refs.RunID = run.ID
		run.ContextPacket = chatcontext.Marshal(chatcontext.Normalize(contextPacket, chatcontext.MergeRefs(
			chatcontext.TaskRunRefs(task.ID, run.ID, project.ID),
			chatcontext.ProjectAssignmentRefs(project.ID, workItem.ID, assignment.ID, role.ID),
		)))
	})
	if err != nil {
		failed := assignment
		failed.ExecutionRef = projectwork.AssignmentExecutionRef{
			Kind:   projectwork.AssignmentExecutionKindTaskRun,
			TaskID: task.ID,
			Status: projectwork.AssignmentStatusFailed,
		}
		failed.Status = projectwork.AssignmentStatusFailed
		failed.CompletedAt = time.Now().UTC()
		failed, updateErr := h.persistStrictEmbeddedCairnlineAssignmentRuntime(ctx, failed)
		if updateErr != nil {
			err = errors.Join(err, updateErr)
		}
		return &projectworkapp.StartTaskAssignmentResult{Assignment: failed, Task: task}, err
	}
	status := projectworkapp.AssignmentStatusFromRun(result.Run.Status)
	running := assignment
	running.ExecutionRef = projectwork.NormalizeAssignmentExecutionRef(projectwork.AssignmentExecutionRef{
		Kind:              projectwork.AssignmentExecutionKindTaskRun,
		TaskID:            result.Task.ID,
		RunID:             result.Run.ID,
		ContextSnapshotID: contextPacket.ID,
		Status:            status,
		TraceID:           result.Run.TraceID,
	})
	running.ContextPacket = append([]byte(nil), result.Run.ContextPacket...)
	running.Status = status
	if running.StartedAt.IsZero() {
		running.StartedAt = time.Now().UTC()
	}
	running, err = h.persistStrictEmbeddedCairnlineAssignmentRuntime(ctx, running)
	if err != nil {
		return &projectworkapp.StartTaskAssignmentResult{Task: result.Task, Run: result.Run, TraceID: result.TraceID, SpanID: result.SpanID}, err
	}
	return &projectworkapp.StartTaskAssignmentResult{
		Assignment: running,
		Task:       result.Task,
		Run:        result.Run,
		TraceID:    result.TraceID,
		SpanID:     result.SpanID,
	}, nil
}

func (h *Handler) claimStrictEmbeddedCairnlineAssignment(ctx context.Context, assignment projectwork.Assignment) (projectwork.Assignment, error) {
	var claimed cairnline.Assignment
	err := h.withCairnlineEmbeddedService(ctx, func(service *cairnline.Service) error {
		item, err := service.ClaimAssignment(ctx, assignment.ProjectID, assignment.ID, "hecate")
		if err != nil {
			return err
		}
		claimed = item
		return nil
	})
	if errors.Is(err, cairnline.ErrConflict) {
		return assignment, projectworkapp.ErrAssignmentStartConflict
	}
	if errors.Is(err, cairnline.ErrNotFound) {
		return assignment, newProjectAssignmentPreflightError(http.StatusNotFound, errCodeNotFound, "assignment not found")
	}
	if err != nil {
		return assignment, err
	}
	out := projectWorkAssignmentFromCairnline(claimed)
	out.Status = projectwork.AssignmentStatusQueued
	return out, nil
}

func (h *Handler) releaseStrictEmbeddedCairnlineAssignmentClaim(ctx context.Context, projectID, assignmentID string) error {
	return h.withCairnlineEmbeddedService(ctx, func(service *cairnline.Service) error {
		_, err := service.ReleaseAssignment(ctx, projectID, assignmentID, "hecate")
		if errors.Is(err, cairnline.ErrConflict) || errors.Is(err, cairnline.ErrNotFound) {
			return nil
		}
		return err
	})
}

func (h *Handler) persistStrictEmbeddedCairnlineAssignmentRuntime(ctx context.Context, assignment projectwork.Assignment) (projectwork.Assignment, error) {
	var recorded projectwork.Assignment
	err := h.withCairnlineEmbeddedService(ctx, func(service *cairnline.Service) error {
		var err error
		recorded, err = h.writeStrictEmbeddedCairnlineAssignmentRuntime(ctx, service, assignment)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return assignment, err
	}
	h.shadowProjectAssignmentRuntimeToHecate(ctx, "project_assignment_start_cairnline_runtime", recorded)
	return h.projectWorkApplication().ApplyAssignmentRuntime(ctx, recorded)
}

func (h *Handler) writeStrictEmbeddedCairnlineAssignmentRuntime(ctx context.Context, service *cairnline.Service, assignment projectwork.Assignment) (projectwork.Assignment, error) {
	existing, err := service.GetAssignment(ctx, assignment.ProjectID, assignment.ID)
	if err != nil {
		return assignment, err
	}
	existing.Status = cairnlinebridge.AssignmentStatus(assignment.Status)
	existing.ExecutionRef = cairnlinebridge.ExecutionRef(assignment.ExecutionRef)
	existing.ContextSnapshotID = strings.TrimSpace(assignment.ExecutionRef.ContextSnapshotID)
	existing.StartedAt = assignment.StartedAt
	existing.CompletedAt = assignment.CompletedAt
	written, err := service.UpdateAssignment(ctx, existing)
	if err != nil {
		return assignment, err
	}
	return projectWorkAssignmentFromCairnlineAuthority(written, assignment), nil
}

func (h *Handler) projectAssignmentStartInputs(ctx context.Context, projectID, workItemID, assignmentID string, strictEmbeddedRead bool) (projectAssignmentLaunchInputs, error) {
	if strictEmbeddedRead {
		inputs, err := h.cairnlineProjectAssignmentLaunchInputs(ctx, projectID, workItemID, assignmentID)
		if err != nil {
			return projectAssignmentLaunchInputs{}, err
		}
		if !inputs.RoleOK {
			return projectAssignmentLaunchInputs{}, newProjectAssignmentPreflightError(http.StatusNotFound, errCodeNotFound, "assignment role not found")
		}
		return inputs, nil
	}
	project, ok, err := h.projects.Get(ctx, projectID)
	if err != nil {
		return projectAssignmentLaunchInputs{}, err
	}
	if !ok {
		return projectAssignmentLaunchInputs{}, newProjectAssignmentPreflightError(http.StatusNotFound, errCodeNotFound, "project not found")
	}
	workItem, ok, err := h.projectWork.GetWorkItem(ctx, projectID, workItemID)
	if err != nil {
		return projectAssignmentLaunchInputs{}, err
	}
	if !ok {
		return projectAssignmentLaunchInputs{}, newProjectAssignmentPreflightError(http.StatusNotFound, errCodeNotFound, "work item not found")
	}
	assignment, ok, err := h.loadProjectWorkAssignment(ctx, projectID, workItemID, assignmentID)
	if err != nil {
		return projectAssignmentLaunchInputs{}, err
	}
	if !ok {
		return projectAssignmentLaunchInputs{}, newProjectAssignmentPreflightError(http.StatusNotFound, errCodeNotFound, "assignment not found")
	}
	role, ok, err := h.loadProjectWorkRole(ctx, projectID, assignment.RoleID)
	if err != nil {
		return projectAssignmentLaunchInputs{}, err
	}
	if !ok {
		return projectAssignmentLaunchInputs{}, newProjectAssignmentPreflightError(http.StatusNotFound, errCodeNotFound, "assignment role not found")
	}
	return projectAssignmentLaunchInputs{
		Project:    project,
		WorkItem:   workItem,
		Assignment: assignment,
		Role:       role,
		RoleOK:     true,
	}, nil
}

func (h *Handler) shadowCairnlineAssignmentStartInputsToHecate(ctx context.Context, inputs projectAssignmentLaunchInputs) (projectwork.Assignment, error) {
	if h == nil || h.projectWork == nil {
		return projectwork.Assignment{}, projectworkapp.ErrStoreNotConfigured
	}
	if inputs.RoleOK {
		if _, ok := h.shadowProjectRoleToHecate(ctx, "project_assignment_start_cairnline_shadow", inputs.Role); !ok {
			return projectwork.Assignment{}, errors.New("assignment role could not be shadowed for Hecate launch")
		}
	}
	h.shadowProjectWorkItemToHecate(ctx, "project_assignment_start_cairnline_shadow", inputs.WorkItem)
	if _, ok, err := h.projectWork.GetWorkItem(ctx, inputs.WorkItem.ProjectID, inputs.WorkItem.ID); err != nil {
		return projectwork.Assignment{}, err
	} else if !ok {
		return projectwork.Assignment{}, errors.New("work item could not be shadowed for Hecate launch")
	}
	h.shadowProjectAssignmentToHecate(ctx, "project_assignment_start_cairnline_shadow", inputs.Assignment)
	assignment, ok, err := h.loadProjectWorkAssignment(ctx, inputs.Assignment.ProjectID, inputs.Assignment.WorkItemID, inputs.Assignment.ID)
	if err != nil {
		return projectwork.Assignment{}, err
	}
	if !ok {
		return projectwork.Assignment{}, errors.New("assignment could not be shadowed for Hecate launch")
	}
	return assignment, nil
}

func (h *Handler) projectExternalAgentAssignmentContextPacket(ctx context.Context, project projects.Project, workItem projectwork.WorkItem, assignment projectwork.Assignment, role projectwork.AgentRoleProfile, plan projectworkapp.ExternalAgentAssignmentLaunchPlan, sessionID string) chat.ContextPacket {
	packet := h.projectAssignmentContextPacket(ctx, project, workItem, assignment, role, plan.Root, plan.Workspace, "", "", plan.ExecutionProfile, plan.Profile, plan.ResolvedSkills, projectworkapp.AssignmentPromptContext{})
	packet.ExecutionMode = chat.ExecutionModeExternalAgent
	packet.Provider = ""
	packet.Model = ""
	packet.Workspace = plan.Workspace
	packet.Refs.SessionID = strings.TrimSpace(sessionID)
	packet.Refs.TaskID = ""
	packet.Refs.RunID = ""
	return packet
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
	if strings.TrimSpace(assignment.ExecutionRef.ChatSessionID) != "" {
		projected, projectErr := h.renderProjectedProjectWorkAssignment(ctx, assignment)
		if projectErr != nil {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, projectErr.Error())
			return
		}
		WriteJSON(w, http.StatusConflict, ProjectWorkAssignmentEnvelope{Object: "project_assignment", Data: projected})
		return
	}
	plan, err := h.projectWorkApplication().ResolveExternalAgentAssignmentLaunchPlan(ctx, project, workItem, assignment, role)
	if err != nil {
		var launchErr projectworkapp.LaunchPlanError
		if errors.As(err, &launchErr) && launchErr.Kind == projectworkapp.LaunchPlanAdapterNotFound {
			writeAgentChatAdapterNotFound(w, launchErr.AdapterID)
			return
		}
		WriteError(w, projectAssignmentPreflightHTTPStatus(err), projectAssignmentPreflightErrorCode(err), err.Error())
		return
	}
	sessionID := newChatID("chat")
	contextPacket := h.projectExternalAgentAssignmentContextPacket(ctx, project, workItem, assignment, role, plan, sessionID)
	contextPacket.ID = firstNonEmptyString(contextPacket.ID, newChatID("ctx"))
	h.appendCairnlineAssignmentLaunchPacketEvidence(ctx, &contextPacket, assignment)

	session := chat.Session{
		ID:              sessionID,
		Title:           plan.SessionTitle,
		ProjectID:       project.ID,
		AgentID:         plan.AdapterID,
		DriverKind:      agentadapters.DriverKindACP,
		Workspace:       plan.Workspace,
		WorkspaceBranch: workspaceGitBranch(plan.Workspace),
		ConfigOptions:   plan.ConfigOptions,
	}
	contextPacket.Refs.SessionID = session.ID
	contextPacket = chatcontext.Normalize(contextPacket, chatcontext.MergeRefs(
		chatcontext.ChatMessageRefs(session.ID, "", project.ID),
		chatcontext.ProjectAssignmentRefs(project.ID, workItem.ID, assignment.ID, role.ID),
	))
	packetBytes := chatcontext.Marshal(contextPacket)
	strictEmbeddedRead := h.projectReadRoutesUseCairnlineReadModel() && h.requiresEmbeddedCairnlineProjectReads()
	if h.projectAssignmentStartUsesStrictEmbeddedCairnlineRuntime(strictEmbeddedRead) {
		result, err := h.startStrictEmbeddedCairnlineExternalAgentAssignment(ctx, assignment, session, contextPacket.ID, packetBytes)
		h.writeProjectExternalAgentAssignmentStartResult(w, ctx, assignment, plan.Adapter.Name, result, err, false)
		return
	}
	startClaimedBy := projectAssignmentStartClaimedBy(assignment)
	claimedAssignment, cairnlineStartClaimed, err := h.claimProjectAssignmentStartInCairnlineAuthority(ctx, project, assignment, startClaimedBy)
	if err != nil {
		if errors.Is(err, projectworkapp.ErrAssignmentStartConflict) {
			projected, projectErr := h.renderProjectedProjectWorkAssignment(ctx, claimedAssignment)
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
	result, err := h.projectWorkApplication().StartExternalAgentAssignment(ctx, projectworkapp.StartExternalAgentAssignmentCommand{
		ProjectID:         project.ID,
		Assignment:        assignment,
		Session:           session,
		ContextSnapshotID: contextPacket.ID,
		ContextPacket:     packetBytes,
	})
	if err != nil && cairnlineStartClaimed {
		if result == nil || result.Assignment.ID == "" {
			h.releaseProjectAssignmentStartInCairnlineAuthority(ctx, assignment, startClaimedBy)
		}
	}
	h.writeProjectExternalAgentAssignmentStartResult(w, ctx, assignment, plan.Adapter.Name, result, err, true)
}

func (h *Handler) startStrictEmbeddedCairnlineExternalAgentAssignment(ctx context.Context, assignment projectwork.Assignment, session chat.Session, contextSnapshotID string, packetBytes []byte) (*projectworkapp.StartExternalAgentAssignmentResult, error) {
	if strings.TrimSpace(assignment.ExecutionRef.ChatSessionID) != "" ||
		projectWorkAssignmentIsTerminal(assignment.Status) ||
		assignment.DriverKind != projectwork.AssignmentDriverExternalAgent {
		return &projectworkapp.StartExternalAgentAssignmentResult{Assignment: assignment}, projectworkapp.ErrAssignmentStartConflict
	}
	session, err := h.agentChat.Create(ctx, session)
	if err != nil {
		return nil, err
	}
	prepareCtx := ctx
	cancel := func() {}
	if agentChatPrepareTimeout > 0 {
		prepareCtx, cancel = context.WithTimeout(ctx, agentChatPrepareTimeout)
	}
	prepared, prepareErr := h.agentChatRunner.PrepareSession(prepareCtx, agentadapters.PrepareSessionRequest{
		SessionID:     session.ID,
		AdapterID:     session.AgentID,
		Workspace:     session.Workspace,
		ConfigOptions: session.ConfigOptions,
		MCPServers:    session.MCPServers,
	})
	cancel()
	if prepareErr != nil {
		_ = h.agentChat.Delete(context.Background(), session.ID)
		return &projectworkapp.StartExternalAgentAssignmentResult{Session: session}, projectworkapp.ExternalAgentPrepareError{Err: prepareErr}
	}

	session, err = h.agentChat.UpdateSession(ctx, session.ID, func(item *chat.Session) {
		item.DriverKind = prepared.DriverKind
		item.NativeSessionID = prepared.NativeSessionID
		item.AgentInfo = prepared.AgentInfo
		item.ConfigOptions = prepared.ConfigOptions
		if prepared.AvailableCommandsKnown {
			item.AvailableCommands = prepared.AvailableCommands
		}
	})
	if err != nil {
		h.cleanupStrictEmbeddedExternalAgentSession(session.ID)
		return &projectworkapp.StartExternalAgentAssignmentResult{Session: session}, err
	}

	claimed, err := h.claimStrictEmbeddedCairnlineAssignment(ctx, assignment)
	if err != nil {
		h.cleanupStrictEmbeddedExternalAgentSession(session.ID)
		return &projectworkapp.StartExternalAgentAssignmentResult{Assignment: claimed, Session: session}, err
	}
	assignment = claimed
	running := assignment
	running.ExecutionRef = projectwork.NormalizeAssignmentExecutionRef(projectwork.AssignmentExecutionRef{
		Kind:              projectwork.AssignmentExecutionKindChatSession,
		ChatSessionID:     session.ID,
		ContextSnapshotID: strings.TrimSpace(contextSnapshotID),
		Status:            projectwork.AssignmentStatusRunning,
	})
	running.ContextPacket = append([]byte(nil), packetBytes...)
	running.Status = projectwork.AssignmentStatusRunning
	if running.StartedAt.IsZero() {
		running.StartedAt = time.Now().UTC()
	}
	running, err = h.persistStrictEmbeddedCairnlineAssignmentRuntime(ctx, running)
	if err != nil {
		h.cleanupStrictEmbeddedExternalAgentSession(session.ID)
		err = errors.Join(err, h.releaseStrictEmbeddedCairnlineAssignmentClaim(context.Background(), assignment.ProjectID, assignment.ID))
		return &projectworkapp.StartExternalAgentAssignmentResult{Assignment: assignment, Session: session}, err
	}
	return &projectworkapp.StartExternalAgentAssignmentResult{Assignment: running, Session: session}, nil
}

func (h *Handler) cleanupStrictEmbeddedExternalAgentSession(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	if h.agentChatRunner != nil {
		cleanupCtx := context.Background()
		cancel := func() {}
		if agentChatPrepareTimeout > 0 {
			cleanupCtx, cancel = context.WithTimeout(cleanupCtx, agentChatPrepareTimeout)
		}
		_ = h.agentChatRunner.DeleteSession(cleanupCtx, sessionID)
		cancel()
	}
	if h.agentChat != nil {
		_ = h.agentChat.Delete(context.Background(), sessionID)
	}
}

func (h *Handler) writeProjectExternalAgentAssignmentStartResult(w http.ResponseWriter, ctx context.Context, assignment projectwork.Assignment, adapterName string, result *projectworkapp.StartExternalAgentAssignmentResult, err error, mirrorStartResult bool) {
	if err != nil {
		var prepareErr projectworkapp.ExternalAgentPrepareError
		if errors.As(err, &prepareErr) {
			writeAgentChatPrepareError(w, adapterName, prepareErr.Unwrap())
			return
		}
		resultAssignment := assignment
		if result != nil && result.Assignment.ID != "" {
			resultAssignment = result.Assignment
			if mirrorStartResult && h.projectWork != nil {
				h.mirrorProjectAssignmentStartResultToCairnline(ctx, result.Assignment)
			}
		}
		if errors.Is(err, projectworkapp.ErrAssignmentStartConflict) {
			projected, projectErr := h.renderProjectedProjectWorkAssignment(ctx, resultAssignment)
			if projectErr != nil {
				WriteError(w, http.StatusInternalServerError, errCodeGatewayError, projectErr.Error())
				return
			}
			if !mirrorStartResult {
				projected.ReadBackend = "cairnline"
			}
			WriteJSON(w, http.StatusConflict, ProjectWorkAssignmentEnvelope{Object: "project_assignment", Data: projected})
			return
		}
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if mirrorStartResult && h.projectWork != nil {
		h.mirrorProjectAssignmentStartResultToCairnline(ctx, result.Assignment)
	}
	projected, err := h.renderProjectedProjectWorkAssignment(ctx, result.Assignment)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if !mirrorStartResult {
		projected.ReadBackend = "cairnline"
	}
	WriteJSON(w, http.StatusOK, ProjectWorkAssignmentEnvelope{Object: "project_assignment", Data: projected})
}

func (h *Handler) mirrorProjectAssignmentStartResultToCairnline(ctx context.Context, assignment projectwork.Assignment) {
	if strings.TrimSpace(assignment.ID) == "" {
		return
	}
	h.recordCairnlineMirrorResult(ctx, cairnlineMirrorFamilyAssignments, "project_assignment_start_result", assignment.ProjectID, h.writeProjectAssignmentToCairnline(ctx, assignment))
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

func projectWorkAssignmentHasActiveExecution(ctx context.Context, store taskRunLookupStore, assignment projectwork.Assignment) (bool, error) {
	return projectworkapp.AssignmentHasActiveExecution(ctx, store, assignment)
}

type taskRunLookupStore interface {
	GetRun(ctx context.Context, taskID, runID string) (types.TaskRun, bool, error)
}

type assignmentHint struct {
	label string
	value string
}

func appendProjectAssignmentLaunchPreflight(packet *chat.ContextPacket, driverKind string, details []string) {
	var body []string
	body = append(body,
		"Driver: "+firstNonEmptyString(strings.TrimSpace(driverKind), projectwork.AssignmentDriverHecateTask),
		"Preview only: no task, run, chat session, memory entry, artifact, or assignment update has been created.",
	)
	for _, detail := range details {
		if detail = strings.TrimSpace(detail); detail != "" {
			body = append(body, detail)
		}
	}
	appendContextPacketSourceWithSection(packet, contextSectionRuntime, chat.ContextSource{
		Kind:   "launch_preflight",
		Label:  "Launch preflight",
		Detail: firstNonEmptyString(strings.TrimSpace(driverKind), projectwork.AssignmentDriverHecateTask),
		Trust:  contextTrustRuntimeState,
	}, chat.ContextItem{
		Kind:            "launch_preflight",
		TrustLevel:      contextTrustRuntimeState,
		Origin:          "project_assignment.preflight",
		Title:           "Launch preflight",
		Body:            strings.Join(body, "\n"),
		Included:        false,
		InclusionReason: "Preflight metadata for operator review before assignment start",
	})
}

func (h *Handler) appendProjectAssignmentLaunchReadiness(ctx context.Context, packet *chat.ContextPacket, provider, model string) error {
	if h.service == nil {
		return nil
	}
	result, err := h.service.ProviderModelReadiness(ctx, provider, model)
	if err != nil {
		return err
	}
	appendProjectAssignmentLaunchReadinessItem(packet, result.Readiness.ToModelReadiness())
	return nil
}

func (h *Handler) logProjectAssignmentLaunchReadinessError(ctx context.Context, err error) {
	if h == nil || h.logger == nil || err == nil {
		return
	}
	h.logger.WarnContext(ctx, "project_assignment.launch_readiness.failed", "error", err)
}

func appendProjectAssignmentLaunchReadinessItem(packet *chat.ContextPacket, readiness types.ModelReadiness) {
	status := firstNonEmptyString(strings.TrimSpace(readiness.Status), "unknown")
	body := []string{
		fmt.Sprintf("Ready: %t", readiness.Ready),
		"Status: " + status,
		"Provider: " + firstNonEmptyString(strings.TrimSpace(readiness.Provider), "auto"),
		"Model: " + firstNonEmptyString(strings.TrimSpace(readiness.Model), "none"),
	}
	if readiness.MatchedProvider != "" {
		body = append(body, "Matched provider: "+readiness.MatchedProvider)
	}
	if readiness.Reason != "" {
		body = append(body, "Reason: "+readiness.Reason)
	}
	if readiness.Message != "" {
		body = append(body, "Message: "+readiness.Message)
	}
	if readiness.OperatorAction != "" {
		body = append(body, "Operator action: "+readiness.OperatorAction)
	}
	if readiness.ProviderStatus != "" {
		body = append(body, "Provider status: "+readiness.ProviderStatus)
	}
	if readiness.ProviderBlockedReason != "" {
		body = append(body, "Provider blocked reason: "+readiness.ProviderBlockedReason)
	}
	if len(readiness.SuggestedModels) > 0 {
		body = append(body, "Suggested models: "+strings.Join(readiness.SuggestedModels, ", "))
	}
	appendContextPacketSourceWithSection(packet, contextSectionRuntime, chat.ContextSource{
		Kind:   "launch_readiness",
		Label:  "Launch readiness",
		Detail: status,
		Trust:  contextTrustRuntimeState,
	}, chat.ContextItem{
		Kind:            "launch_readiness",
		TrustLevel:      contextTrustRuntimeState,
		Origin:          "project_assignment.launch_readiness",
		Title:           "Launch readiness",
		Body:            strings.Join(body, "\n"),
		Included:        false,
		InclusionReason: "Provider/model readiness metadata for operator review before assignment start",
		Metadata:        projectAssignmentLaunchReadinessMetadata(readiness, status),
	})
}

func projectAssignmentLaunchReadinessMetadata(readiness types.ModelReadiness, status string) map[string]string {
	metadata := map[string]string{
		"ready":         fmt.Sprintf("%t", readiness.Ready),
		"routing_ready": fmt.Sprintf("%t", readiness.RoutingReady),
		"status":        strings.TrimSpace(status),
		"provider":      firstNonEmptyString(strings.TrimSpace(readiness.Provider), "auto"),
		"model":         strings.TrimSpace(readiness.Model),
	}
	setMetadata := func(key, value string) {
		if value = strings.TrimSpace(value); value != "" {
			metadata[key] = value
		}
	}
	setMetadata("matched_provider", readiness.MatchedProvider)
	setMetadata("reason", readiness.Reason)
	setMetadata("message", readiness.Message)
	setMetadata("operator_action", readiness.OperatorAction)
	setMetadata("provider_status", readiness.ProviderStatus)
	setMetadata("provider_blocked_reason", readiness.ProviderBlockedReason)
	if len(readiness.SuggestedModels) > 0 {
		setMetadata("suggested_models", strings.Join(readiness.SuggestedModels, ", "))
	}
	return metadata
}

func formatProjectExternalAgentConfigOptions(options []agentcontrols.ConfigOption) string {
	if len(options) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(options))
	for _, option := range options {
		id := strings.TrimSpace(option.ID)
		if id == "" {
			continue
		}
		value := strings.TrimSpace(option.CurrentValue)
		if option.CurrentBool != nil {
			value = boolLabel(*option.CurrentBool)
		}
		parts = append(parts, id+"="+firstNonEmptyString(value, "set"))
	}
	if len(parts) == 0 {
		return "none"
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}
