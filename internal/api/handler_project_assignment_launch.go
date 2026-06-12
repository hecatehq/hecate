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
	"github.com/hecatehq/hecate/internal/chatcontext"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/orchestrator"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/projectworkapp"
	"github.com/hecatehq/hecate/internal/workspacefs"
	"github.com/hecatehq/hecate/pkg/types"
)

func (h *Handler) HandleProjectWorkAssignmentPreflight(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectID := r.PathValue("id")
	workItemID := r.PathValue("work_item_id")
	assignmentID := r.PathValue("assignment_id")
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
	return http.StatusInternalServerError
}

func projectAssignmentPreflightErrorCode(err error) string {
	var preflightErr projectAssignmentPreflightError
	if errors.As(err, &preflightErr) && preflightErr.code != "" {
		return preflightErr.code
	}
	return errCodeGatewayError
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
	plan, err := h.resolveProjectAssignmentLaunchPlan(ctx, project, role)
	if err != nil {
		return chat.ContextPacket{}, err
	}
	packet := h.projectAssignmentContextPacket(ctx, project, workItem, assignment, role, plan.WorkingDirectory, plan.RequestedProvider, plan.RequestedModel, plan.ExecutionProfile, plan.Profile, plan.ResolvedSkills, plan.PromptContext)
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
	plan, err := h.resolveProjectExternalAgentAssignmentLaunchPlan(ctx, project, workItem, role)
	if err != nil {
		var adapterErr projectAssignmentAdapterNotFoundError
		if errors.As(err, &adapterErr) {
			return chat.ContextPacket{}, newProjectAssignmentPreflightError(http.StatusNotFound, errCodeNotFound, err.Error())
		}
		return chat.ContextPacket{}, err
	}
	packet := h.projectExternalAgentAssignmentContextPacket(ctx, project, workItem, assignment, role, plan, "")
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

	plan, err := h.resolveProjectAssignmentLaunchPlan(ctx, project, role)
	if err != nil {
		WriteError(w, projectAssignmentPreflightHTTPStatus(err), projectAssignmentPreflightErrorCode(err), err.Error())
		return
	}
	contextPacket := h.projectAssignmentContextPacket(ctx, project, workItem, assignment, role, plan.WorkingDirectory, plan.RequestedProvider, plan.RequestedModel, plan.ExecutionProfile, plan.Profile, plan.ResolvedSkills, plan.PromptContext)
	if contextPacket.ID == "" {
		contextPacket.ID = newChatID("ctx")
	}

	result, err := h.projectWorkApplication().StartTaskAssignment(ctx, projectworkapp.StartTaskAssignmentCommand{
		ProjectID:         projectID,
		WorkItemID:        workItemID,
		Assignment:        assignment,
		ContextSnapshotID: contextPacket.ID,
		BuildTask: func(taskID string) (types.Task, error) {
			return h.buildProjectAssignmentTask(taskID, project, workItem, assignment, role, plan.Profile, plan.WorkingDirectory, plan.WorkspaceMode, plan.RequestedProvider, plan.RequestedModel, plan.ExecutionProfile, plan.PromptContext), nil
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

type projectAssignmentLaunchPlan struct {
	WorkingDirectory  string
	WorkspaceMode     string
	RequestedProvider string
	RequestedModel    string
	ExecutionProfile  string
	Profile           resolvedAgentProfile
	ResolvedSkills    resolvedProjectSkills
	PromptContext     projectAssignmentPromptContext
}

func (h *Handler) resolveProjectAssignmentLaunchPlan(ctx context.Context, project projects.Project, role projectwork.AgentRoleProfile) (projectAssignmentLaunchPlan, error) {
	workingDirectory, workspaceMode, err := resolveProjectAssignmentWorkspace(project)
	if err != nil {
		return projectAssignmentLaunchPlan{}, newProjectAssignmentPreflightError(http.StatusBadRequest, errCodeInvalidRequest, err.Error())
	}
	requestedProvider := strings.TrimSpace(firstNonEmpty(role.DefaultProvider, project.DefaultProvider))
	requestedModel := strings.TrimSpace(firstNonEmpty(role.DefaultModel, project.DefaultModel))
	profile, err := h.resolveProjectAssignmentProfile(ctx, role, project)
	if err != nil {
		return projectAssignmentLaunchPlan{}, err
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
		return projectAssignmentLaunchPlan{}, newProjectAssignmentPreflightError(http.StatusUnprocessableEntity, errCodeModelNotConfigured, "project assignment start requires a default model")
	}
	return projectAssignmentLaunchPlan{
		WorkingDirectory:  workingDirectory,
		WorkspaceMode:     workspaceMode,
		RequestedProvider: requestedProvider,
		RequestedModel:    requestedModel,
		ExecutionProfile:  executionProfile,
		Profile:           profile,
		ResolvedSkills:    h.resolveProjectAssignmentSkills(ctx, project.ID, role, profile),
		PromptContext:     h.projectAssignmentPromptContext(ctx, project, profile, workingDirectory),
	}, nil
}

type projectExternalAgentAssignmentLaunchPlan struct {
	Workspace        string
	AdapterID        string
	Adapter          agentadapters.Adapter
	ConfigOptions    []agentcontrols.ConfigOption
	SessionTitle     string
	ExecutionProfile string
	Profile          resolvedAgentProfile
	ResolvedSkills   resolvedProjectSkills
}

type projectAssignmentAdapterNotFoundError struct {
	adapterID string
}

func (err projectAssignmentAdapterNotFoundError) Error() string {
	return "external-agent adapter not found: " + err.adapterID
}

func (err projectAssignmentAdapterNotFoundError) AdapterID() string {
	return err.adapterID
}

func (h *Handler) resolveProjectExternalAgentAssignmentLaunchPlan(ctx context.Context, project projects.Project, workItem projectwork.WorkItem, role projectwork.AgentRoleProfile) (projectExternalAgentAssignmentLaunchPlan, error) {
	workingDirectory, _, err := resolveProjectAssignmentWorkspace(project)
	if err != nil {
		return projectExternalAgentAssignmentLaunchPlan{}, newProjectAssignmentPreflightError(http.StatusBadRequest, errCodeInvalidRequest, err.Error())
	}
	profile, err := h.resolveProjectAssignmentProfile(ctx, role, project)
	if err != nil {
		return projectExternalAgentAssignmentLaunchPlan{}, err
	}
	adapterID := strings.TrimSpace(profile.ExternalAgentKind)
	if adapterID == "" {
		return projectExternalAgentAssignmentLaunchPlan{}, newProjectAssignmentPreflightError(http.StatusUnprocessableEntity, errCodeInvalidRequest, "external-agent assignment requires an agent profile with external_agent_kind")
	}
	adapter, ok := agentadapters.BuiltInByID(adapterID)
	if !ok {
		return projectExternalAgentAssignmentLaunchPlan{}, projectAssignmentAdapterNotFoundError{adapterID: adapterID}
	}
	configOptions, err := projectExternalAgentConfigOptions(adapterID, profile.ExternalAgentOptions)
	if err != nil {
		return projectExternalAgentAssignmentLaunchPlan{}, newProjectAssignmentPreflightError(http.StatusBadRequest, errCodeInvalidRequest, err.Error())
	}
	workspace, err := agentadapters.ValidateWorkspace(workingDirectory)
	if err != nil {
		return projectExternalAgentAssignmentLaunchPlan{}, newProjectAssignmentPreflightError(http.StatusBadRequest, errCodeInvalidRequest, err.Error())
	}
	return projectExternalAgentAssignmentLaunchPlan{
		Workspace:        workspace,
		AdapterID:        adapterID,
		Adapter:          adapter,
		ConfigOptions:    configOptions,
		SessionTitle:     projectExternalAgentAssignmentTitle(workItem, role, adapter),
		ExecutionProfile: firstNonEmptyString(profile.ExecutionProfile, "external_agent_assignment"),
		Profile:          profile,
		ResolvedSkills:   h.resolveProjectAssignmentSkills(ctx, project.ID, role, profile),
	}, nil
}

func (h *Handler) projectExternalAgentAssignmentContextPacket(ctx context.Context, project projects.Project, workItem projectwork.WorkItem, assignment projectwork.Assignment, role projectwork.AgentRoleProfile, plan projectExternalAgentAssignmentLaunchPlan, sessionID string) chat.ContextPacket {
	packet := h.projectAssignmentContextPacket(ctx, project, workItem, assignment, role, plan.Workspace, "", "", plan.ExecutionProfile, plan.Profile, plan.ResolvedSkills, projectAssignmentPromptContext{})
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
	plan, err := h.resolveProjectExternalAgentAssignmentLaunchPlan(ctx, project, workItem, role)
	if err != nil {
		var adapterErr projectAssignmentAdapterNotFoundError
		if errors.As(err, &adapterErr) {
			writeAgentChatAdapterNotFound(w, adapterErr.AdapterID())
			return
		}
		WriteError(w, projectAssignmentPreflightHTTPStatus(err), projectAssignmentPreflightErrorCode(err), err.Error())
		return
	}
	sessionID := newChatID("chat")
	contextPacket := h.projectExternalAgentAssignmentContextPacket(ctx, project, workItem, assignment, role, plan, sessionID)
	contextPacket.ID = firstNonEmptyString(contextPacket.ID, newChatID("ctx"))

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
			writeAgentChatPrepareError(w, plan.Adapter.Name, prepareErr.Unwrap())
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
