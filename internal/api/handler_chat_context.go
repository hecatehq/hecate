package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/pkg/types"
)

const chatContextPacketVersion = "chat.context.v1"

const (
	contextTrustSystemInstruction = "system_instruction"
	contextTrustOperatorMemory    = "operator_memory"
	contextTrustProject           = "project"
	contextTrustWorkspaceGuidance = "workspace_guidance"
	contextTrustRuntimeState      = "runtime_state"
)

const (
	contextSectionProfile      = "profile"
	contextSectionInstructions = "instructions"
	contextSectionMemory       = "memory"
	contextSectionWorkspace    = "workspace"
	contextSectionProject      = "project"
	contextSectionProjectWork  = "project_work"
	contextSectionSources      = "sources"
	contextSectionRuntime      = "runtime"
)

func (h *Handler) HandleChatMessageContext(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("id"))
	messageID := strings.TrimSpace(r.PathValue("message_id"))
	if sessionID == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "session id is required")
		return
	}
	if messageID == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "message id is required")
		return
	}
	session, ok, err := h.agentChat.Get(r.Context(), sessionID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "agent chat session not found")
		return
	}
	for _, message := range session.Messages {
		if message.ID != messageID {
			continue
		}
		writeChatContextPacket(w, normalizeContextPacket(message.Context, chat.ContextRefs{
			SessionID: sessionID,
			MessageID: messageID,
			ProjectID: session.ProjectID,
		}))
		return
	}
	WriteError(w, http.StatusNotFound, errCodeNotFound, "agent chat message not found")
}

func (h *Handler) HandleTaskRunContext(w http.ResponseWriter, r *http.Request) {
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
	packet, ok, err := h.contextPacketForTaskRun(ctx, task, run)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "task run context packet not found; the run may predate context snapshots or have no linked run/chat packet")
		return
	}
	writeChatContextPacket(w, normalizeContextPacket(packet, chat.ContextRefs{
		TaskID: task.ID,
		RunID:  run.ID,
	}))
}

func writeChatContextPacket(w http.ResponseWriter, packet chat.ContextPacket) {
	rendered := renderChatContextPacket(packet)
	if rendered == nil {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "context packet not found")
		return
	}
	WriteJSON(w, http.StatusOK, ChatContextPacketResponse{
		Object: "context_packet",
		Data:   *rendered,
	})
}

func (h *Handler) HandleProjectWorkAssignmentContext(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if h.projectWork == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "project work store is not configured")
		return
	}
	projectID := strings.TrimSpace(r.PathValue("id"))
	workItemID := strings.TrimSpace(r.PathValue("work_item_id"))
	assignmentID := strings.TrimSpace(r.PathValue("assignment_id"))
	assignment, ok, err := h.loadProjectWorkAssignment(ctx, projectID, workItemID, assignmentID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "assignment not found")
		return
	}
	packet, ok, err := h.contextPacketForProjectAssignment(ctx, assignment)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project assignment context packet not found")
		return
	}
	writeChatContextPacket(w, normalizeContextPacket(packet, chat.ContextRefs{
		ProjectID:    projectID,
		WorkItemID:   workItemID,
		AssignmentID: assignmentID,
		RoleID:       assignment.RoleID,
		TaskID:       assignment.TaskID,
		RunID:        assignment.RunID,
		SessionID:    assignment.ChatSessionID,
		MessageID:    assignment.MessageID,
	}))
}

func (h *Handler) contextPacketForTaskRun(ctx context.Context, task types.Task, run types.TaskRun) (chat.ContextPacket, bool, error) {
	if packet, ok, err := contextPacketFromRun(run); err != nil || ok {
		return packet, ok, err
	}
	if h == nil || h.agentChat == nil {
		return chat.ContextPacket{}, false, nil
	}
	if task.OriginKind == "chat" && strings.TrimSpace(task.OriginID) != "" {
		session, ok, err := h.agentChat.Get(ctx, task.OriginID)
		if err != nil || !ok {
			return chat.ContextPacket{}, false, err
		}
		packet, found := contextPacketFromSessionRun(session, task.ID, run.ID)
		return packet, found, nil
	}
	sessions, err := h.agentChat.List(ctx)
	if err != nil {
		return chat.ContextPacket{}, false, err
	}
	for _, session := range sessions {
		session, ok, err := h.agentChat.Get(ctx, session.ID)
		if err != nil {
			return chat.ContextPacket{}, false, err
		}
		if !ok {
			continue
		}
		if packet, ok := contextPacketFromSessionRun(session, task.ID, run.ID); ok {
			return packet, true, nil
		}
	}
	return chat.ContextPacket{}, false, nil
}

func (h *Handler) contextPacketForProjectAssignment(ctx context.Context, assignment projectwork.Assignment) (chat.ContextPacket, bool, error) {
	if h != nil && h.taskStore != nil && strings.TrimSpace(assignment.TaskID) != "" && strings.TrimSpace(assignment.RunID) != "" {
		task, ok, err := h.taskStore.GetTask(ctx, assignment.TaskID)
		if err != nil {
			return chat.ContextPacket{}, false, err
		}
		if ok {
			run, ok, err := h.taskStore.GetRun(ctx, assignment.TaskID, assignment.RunID)
			if err != nil {
				return chat.ContextPacket{}, false, err
			}
			if ok {
				packet, found, err := h.contextPacketForTaskRun(ctx, task, run)
				if err != nil || found {
					return packet, found, err
				}
			}
		}
	}
	if len(assignment.ContextPacket) > 0 {
		var packet chat.ContextPacket
		if err := json.Unmarshal(assignment.ContextPacket, &packet); err != nil {
			return chat.ContextPacket{}, false, fmt.Errorf("decode project assignment context packet: %w", err)
		}
		if !packet.Empty() {
			return packet, true, nil
		}
	}
	if h == nil || h.agentChat == nil || strings.TrimSpace(assignment.ChatSessionID) == "" || strings.TrimSpace(assignment.MessageID) == "" {
		return chat.ContextPacket{}, false, nil
	}
	session, ok, err := h.agentChat.Get(ctx, assignment.ChatSessionID)
	if err != nil || !ok {
		return chat.ContextPacket{}, false, err
	}
	for _, message := range session.Messages {
		if message.ID != strings.TrimSpace(assignment.MessageID) {
			continue
		}
		if message.Context.Empty() {
			return chat.ContextPacket{}, false, nil
		}
		return message.Context, true, nil
	}
	return chat.ContextPacket{}, false, nil
}

func contextPacketFromSessionRun(session chat.Session, taskID, runID string) (chat.ContextPacket, bool) {
	taskID = strings.TrimSpace(taskID)
	runID = strings.TrimSpace(runID)
	for _, message := range session.Messages {
		if strings.TrimSpace(message.TaskID) != taskID || strings.TrimSpace(message.RunID) != runID {
			continue
		}
		if message.Context.Empty() {
			return chat.ContextPacket{}, false
		}
		return message.Context, true
	}
	return chat.ContextPacket{}, false
}

func contextPacketFromRun(run types.TaskRun) (chat.ContextPacket, bool, error) {
	if len(run.ContextPacket) == 0 {
		return chat.ContextPacket{}, false, nil
	}
	var packet chat.ContextPacket
	if err := json.Unmarshal(run.ContextPacket, &packet); err != nil {
		return chat.ContextPacket{}, false, fmt.Errorf("decode task run context packet: %w", err)
	}
	if packet.Empty() {
		return chat.ContextPacket{}, false, nil
	}
	return packet, true, nil
}

func (h *Handler) directModelContextPacket(ctx context.Context, session chat.Session, provider, model, systemPrompt string) chat.ContextPacket {
	packet := baseChatContextPacket(chat.ExecutionModeHecateTask, provider, model, session.Workspace)
	packet.SystemPromptIncluded = strings.TrimSpace(systemPrompt) != ""
	packet.MessageCount = chatTranscriptMessageCount(session.Messages) + 1
	populateProjectRefs(&packet, session.ProjectID)
	appendProjectSummary(&packet, h.projectSummary(ctx, session.ProjectID), true, "Project linked to this chat session")
	if packet.SystemPromptIncluded {
		appendContextPacketSourceWithSection(&packet, contextSectionInstructions, chat.ContextSource{
			Kind:   "system_prompt",
			Label:  "System prompt",
			Detail: "Configured for this direct model turn",
			Trust:  "system",
		}, chat.ContextItem{
			Kind:            "system_prompt",
			TrustLevel:      contextTrustSystemInstruction,
			Origin:          "chat.system_prompt",
			Title:           "System prompt",
			BodyRef:         "chat_system_prompt",
			Included:        true,
			InclusionReason: "Configured for this direct model turn",
		})
	}
	appendProjectMemory(&packet, h.projectMemoryEntries(ctx, session))
	if strings.TrimSpace(session.Workspace) != "" {
		appendContextPacketSourceWithSection(&packet, contextSectionWorkspace, chat.ContextSource{
			Kind:   "workspace",
			Label:  "Workspace",
			Detail: session.Workspace,
			Trust:  "workspace",
		}, chat.ContextItem{
			Kind:            "workspace",
			TrustLevel:      contextTrustWorkspaceGuidance,
			Origin:          strings.TrimSpace(session.Workspace),
			Title:           "Workspace",
			BodyRef:         strings.TrimSpace(session.Workspace),
			Included:        true,
			InclusionReason: "Workspace path selected for this direct model turn",
		})
	}
	appendProjectContextSources(&packet, h.projectContextSources(ctx, session))
	appendTranscriptContext(&packet)
	return packet
}

func (h *Handler) hecateTaskContextPacket(ctx context.Context, session chat.Session, provider, model, systemPrompt string, forceNewTask bool) chat.ContextPacket {
	packet := baseChatContextPacket(chat.ExecutionModeHecateTask, provider, model, session.Workspace)
	packet.SystemPromptIncluded = strings.TrimSpace(systemPrompt) != ""
	packet.MessageCount = chatTranscriptMessageCount(session.Messages) + 1
	packet.ExecutionProfile = "chat_agent"
	populateProjectRefs(&packet, session.ProjectID)
	appendProjectSummary(&packet, h.projectSummary(ctx, session.ProjectID), true, "Project linked to this chat session")
	if packet.SystemPromptIncluded {
		appendContextPacketSourceWithSection(&packet, contextSectionInstructions, chat.ContextSource{
			Kind:   "system_prompt",
			Label:  "System prompt",
			Detail: "Stored on the backing task for this task segment",
			Trust:  "system",
		}, chat.ContextItem{
			Kind:            "system_prompt",
			TrustLevel:      contextTrustSystemInstruction,
			Origin:          "task.system_prompt",
			Title:           "System prompt",
			BodyRef:         "task_system_prompt",
			Included:        true,
			InclusionReason: "Stored on the backing task for this task segment",
		})
	}
	appendProjectMemory(&packet, h.projectMemoryEntries(ctx, session))
	if strings.TrimSpace(session.Workspace) != "" {
		appendContextPacketSourceWithSection(&packet, contextSectionWorkspace, chat.ContextSource{
			Kind:   "workspace",
			Label:  "Workspace",
			Detail: session.Workspace,
			Trust:  "workspace",
		}, chat.ContextItem{
			Kind:            "workspace",
			TrustLevel:      contextTrustWorkspaceGuidance,
			Origin:          strings.TrimSpace(session.Workspace),
			Title:           "Workspace",
			BodyRef:         strings.TrimSpace(session.Workspace),
			Included:        true,
			InclusionReason: "Workspace path selected for this task-backed turn",
		})
	}
	appendProjectContextSources(&packet, h.projectContextSources(ctx, session))
	taskDetail := "Continuing the existing task-backed agent loop"
	if forceNewTask || strings.TrimSpace(session.TaskID) == "" {
		taskDetail = "Starting a new task-backed agent loop"
	}
	appendTranscriptContext(&packet)
	appendContextPacketSourceWithSection(&packet, contextSectionRuntime, chat.ContextSource{
		Kind:   "task_runtime",
		Label:  "Hecate task runtime",
		Detail: taskDetail,
		Trust:  "runtime",
	}, chat.ContextItem{
		Kind:            "task_runtime",
		TrustLevel:      contextTrustRuntimeState,
		Origin:          "hecate.task_runtime",
		Title:           "Hecate task runtime",
		Body:            taskDetail,
		Included:        true,
		InclusionReason: "Task-backed Hecate Chat turn",
	})
	return packet
}

func (h *Handler) externalAgentContextPacket(ctx context.Context, session chat.Session, adapterName string) chat.ContextPacket {
	packet := baseChatContextPacket(chat.ExecutionModeExternalAgent, "", "", session.Workspace)
	packet.MessageCount = chatTranscriptMessageCount(session.Messages) + 1
	populateProjectRefs(&packet, session.ProjectID)
	appendProjectSummary(&packet, h.projectSummary(ctx, session.ProjectID), true, "Project linked to this chat session")
	appendProjectMemory(&packet, h.projectMemoryEntries(ctx, session))
	if strings.TrimSpace(session.Workspace) != "" {
		appendContextPacketSourceWithSection(&packet, contextSectionWorkspace, chat.ContextSource{
			Kind:   "workspace",
			Label:  "Workspace",
			Detail: session.Workspace,
			Trust:  "workspace",
		}, chat.ContextItem{
			Kind:            "workspace",
			TrustLevel:      contextTrustWorkspaceGuidance,
			Origin:          strings.TrimSpace(session.Workspace),
			Title:           "Workspace",
			BodyRef:         strings.TrimSpace(session.Workspace),
			Included:        true,
			InclusionReason: "Workspace path selected for this external-agent session",
		})
	}
	appendProjectContextSources(&packet, h.projectContextSources(ctx, session))
	if strings.TrimSpace(adapterName) == "" {
		adapterName = "External agent"
	}
	appendTranscriptContext(&packet)
	appendContextPacketSourceWithSection(&packet, contextSectionRuntime, chat.ContextSource{
		Kind:   "adapter_session",
		Label:  adapterName + " ACP session",
		Detail: "The adapter owns model packing inside its native session",
		Trust:  "adapter",
	}, chat.ContextItem{
		Kind:            "external_agent_session",
		TrustLevel:      contextTrustRuntimeState,
		Origin:          "adapter:" + adapterName,
		Title:           adapterName + " ACP session",
		Body:            "Hecate can show adapter/session metadata and transcript rows it receives, but cannot inspect the external agent's private prompt or packed model context.",
		Included:        true,
		InclusionReason: "Visible external-agent metadata for this turn",
	})
	return packet
}

func (h *Handler) projectAssignmentContextPacket(ctx context.Context, project projects.Project, workItem projectwork.WorkItem, assignment projectwork.Assignment, role projectwork.AgentRoleProfile, workingDirectory, provider, model, executionProfile string, profile resolvedAgentProfile, skills resolvedProjectSkills) chat.ContextPacket {
	packet := baseChatContextPacket(chat.ExecutionModeHecateTask, provider, model, workingDirectory)
	driverKind := firstNonEmptyString(strings.TrimSpace(assignment.DriverKind), projectwork.AssignmentDriverHecateTask)
	includedReason := "Included in the native project assignment launch context"
	if driverKind == projectwork.AssignmentDriverExternalAgent {
		includedReason = "Included in the external-agent assignment launch context"
	}
	packet.ID = newChatID("ctx")
	packet.ExecutionProfile = strings.TrimSpace(executionProfile)
	packet.SystemPromptIncluded = strings.TrimSpace(projectAssignmentSystemPrompt(project, role, profile)) != ""
	packet.Refs = &chat.ContextRefs{
		TaskID:       strings.TrimSpace(assignment.TaskID),
		RunID:        strings.TrimSpace(assignment.RunID),
		ProjectID:    strings.TrimSpace(project.ID),
		WorkItemID:   strings.TrimSpace(workItem.ID),
		AssignmentID: strings.TrimSpace(assignment.ID),
		RoleID:       strings.TrimSpace(role.ID),
	}

	appendProjectSummary(&packet, &project, true, includedReason)
	appendContextPacketSourceWithSection(&packet, contextSectionProjectWork, chat.ContextSource{
		Kind:   "work_item",
		Label:  firstNonEmptyString(strings.TrimSpace(workItem.Title), strings.TrimSpace(workItem.ID)),
		Detail: strings.TrimSpace(workItem.ID),
		Trust:  "project",
	}, chat.ContextItem{
		Kind:            "work_item",
		TrustLevel:      contextTrustRuntimeState,
		Origin:          strings.TrimSpace(workItem.ID),
		Title:           firstNonEmptyString(strings.TrimSpace(workItem.Title), strings.TrimSpace(workItem.ID)),
		Body:            firstNonEmptyString(strings.TrimSpace(workItem.Brief), "No brief recorded."),
		Included:        true,
		InclusionReason: includedReason,
	})
	appendContextPacketSourceWithSection(&packet, contextSectionProjectWork, chat.ContextSource{
		Kind:   "assignment",
		Label:  firstNonEmptyString(strings.TrimSpace(assignment.ID), "Assignment"),
		Detail: driverKind,
		Trust:  "runtime",
	}, chat.ContextItem{
		Kind:            "assignment",
		TrustLevel:      contextTrustRuntimeState,
		Origin:          strings.TrimSpace(assignment.ID),
		Title:           "Assignment",
		Body:            fmt.Sprintf("Status: %s\nDriver: %s", firstNonEmptyString(strings.TrimSpace(assignment.Status), projectwork.AssignmentStatusQueued), driverKind),
		Included:        true,
		InclusionReason: includedReason,
	})
	appendContextPacketSourceWithSection(&packet, contextSectionProjectWork, chat.ContextSource{
		Kind:   "role",
		Label:  firstNonEmptyString(strings.TrimSpace(role.Name), strings.TrimSpace(role.ID)),
		Detail: strings.TrimSpace(role.ID),
		Trust:  "runtime",
	}, chat.ContextItem{
		Kind:       "role",
		TrustLevel: contextTrustRuntimeState,
		Origin:     strings.TrimSpace(role.ID),
		Title:      firstNonEmptyString(strings.TrimSpace(role.Name), strings.TrimSpace(role.ID)),
		Body: strings.Join([]string{
			"Description: " + firstNonEmptyString(strings.TrimSpace(role.Description), "No description recorded."),
			"Instructions: " + firstNonEmptyString(strings.TrimSpace(role.Instructions), "No role instructions recorded."),
		}, "\n"),
		Included:        true,
		InclusionReason: includedReason,
	})
	appendContextPacketSourceWithSection(&packet, contextSectionProjectWork, chat.ContextSource{
		Kind:   "execution_hints",
		Label:  "Execution hints",
		Detail: strings.TrimSpace(executionProfile),
		Trust:  "runtime",
	}, chat.ContextItem{
		Kind:       "execution_hints",
		TrustLevel: contextTrustRuntimeState,
		Origin:     "project_assignment.execution_hints",
		Title:      "Execution hints",
		Body: strings.Join([]string{
			"Driver: " + driverKind,
			"Provider: " + firstNonEmptyString(strings.TrimSpace(provider), "auto"),
			"Model: " + firstNonEmptyString(strings.TrimSpace(model), "project/runtime default"),
			"Profile: " + firstNonEmptyString(strings.TrimSpace(executionProfile), "none"),
			"Role defaults: " + formatAssignmentHints([]assignmentHint{
				{"driver", role.DefaultDriverKind},
				{"provider", role.DefaultProvider},
				{"model", role.DefaultModel},
				{"profile", role.DefaultAgentProfile},
			}),
			"Project defaults: " + formatAssignmentHints([]assignmentHint{
				{"provider", project.DefaultProvider},
				{"model", project.DefaultModel},
				{"profile", project.DefaultAgentProfile},
				{"workspace_mode", project.DefaultWorkspaceMode},
			}),
		}, "\n"),
		Included:        true,
		InclusionReason: includedReason,
	})
	appendResolvedAgentProfile(&packet, profile)
	appendResolvedProjectSkills(&packet, skills)
	if packet.SystemPromptIncluded {
		promptOrigin := "task.system_prompt"
		promptBodyRef := "task_system_prompt"
		promptReason := "Stored on the native assignment task"
		if driverKind == projectwork.AssignmentDriverExternalAgent {
			promptOrigin = "external_agent.assignment_instructions"
			promptBodyRef = "external_agent_assignment_instructions"
			promptReason = "Stored on the external-agent assignment context packet"
		}
		appendContextPacketSourceWithSection(&packet, contextSectionInstructions, chat.ContextSource{
			Kind:   "system_prompt",
			Label:  "System prompt",
			Detail: promptReason,
			Trust:  "system",
		}, chat.ContextItem{
			Kind:            "system_prompt",
			TrustLevel:      contextTrustSystemInstruction,
			Origin:          promptOrigin,
			Title:           "System prompt",
			BodyRef:         promptBodyRef,
			Included:        true,
			InclusionReason: promptReason,
		})
	}
	if strings.TrimSpace(workingDirectory) != "" {
		appendContextPacketSourceWithSection(&packet, contextSectionWorkspace, chat.ContextSource{
			Kind:   "workspace",
			Label:  "Workspace",
			Detail: strings.TrimSpace(workingDirectory),
			Trust:  "workspace",
		}, chat.ContextItem{
			Kind:            "workspace",
			TrustLevel:      contextTrustWorkspaceGuidance,
			Origin:          strings.TrimSpace(workingDirectory),
			Title:           "Workspace",
			BodyRef:         strings.TrimSpace(workingDirectory),
			Included:        true,
			InclusionReason: "Selected as the project assignment workspace",
		})
	}
	appendProjectMemoryWithInclusion(&packet, h.enabledProjectMemoryEntries(ctx, project.ID), false, "Stored project memory is not injected into project assignment launch context v1")
	appendProjectContextSourcesWithInclusion(&packet, projectContextSourcesFromProject(project), false, "Stored project sources are not injected into project assignment launch context v1")
	appendProjectAssignmentHandoffs(&packet, h.assignmentRelevantHandoffs(ctx, assignment, role.ID), false, "Handoff references are inspectable metadata only in project assignment launch context v1")
	appendProjectAssignmentArtifacts(&packet, h.assignmentRelevantArtifacts(ctx, assignment), false, "Artifact references are inspectable metadata only in project assignment launch context v1")
	return packet
}

func (h *Handler) projectContextSources(ctx context.Context, session chat.Session) []chat.ContextSource {
	project := h.projectSummary(ctx, session.ProjectID)
	if project == nil {
		return nil
	}
	return projectContextSourcesFromProject(*project)
}

func (h *Handler) projectSummary(ctx context.Context, projectID string) *projects.Project {
	if h == nil || h.projects == nil || strings.TrimSpace(projectID) == "" {
		return nil
	}
	project, ok, err := h.projects.Get(ctx, projectID)
	if err != nil || !ok {
		return nil
	}
	return &project
}

func projectContextSourcesFromProject(project projects.Project) []chat.ContextSource {
	sources := make([]chat.ContextSource, 0, len(project.ContextSources))
	for _, source := range project.ContextSources {
		if !source.Enabled {
			continue
		}
		label := strings.TrimSpace(source.Title)
		if label == "" {
			label = strings.TrimSpace(source.Path)
		}
		if label == "" {
			continue
		}
		trust := firstNonEmptyString(strings.TrimSpace(source.TrustLabel), contextTrustProject)
		sources = append(sources, chat.ContextSource{
			Kind:   projectContextSourceKind(source.Kind),
			Label:  label,
			Detail: strings.TrimSpace(source.Path),
			Trust:  trust,
		})
	}
	return sources
}

func (h *Handler) projectMemoryEntries(ctx context.Context, session chat.Session) []memory.Entry {
	return h.enabledProjectMemoryEntries(ctx, session.ProjectID)
}

func (h *Handler) enabledProjectMemoryEntries(ctx context.Context, projectID string) []memory.Entry {
	if h == nil || h.memory == nil || strings.TrimSpace(projectID) == "" {
		return nil
	}
	items, err := h.memory.List(ctx, memory.Filter{ProjectID: projectID})
	if err != nil {
		return nil
	}
	return items
}

func (h *Handler) assignmentRelevantArtifacts(ctx context.Context, assignment projectwork.Assignment) []projectwork.CollaborationArtifact {
	if h == nil || h.projectWork == nil {
		return nil
	}
	items, err := h.projectWork.ListArtifacts(ctx, projectwork.ArtifactFilter{
		ProjectID:  assignment.ProjectID,
		WorkItemID: assignment.WorkItemID,
	})
	if err != nil {
		return nil
	}
	filtered := make([]projectwork.CollaborationArtifact, 0, len(items))
	for _, item := range items {
		if item.AssignmentID == "" || item.AssignmentID == assignment.ID {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func (h *Handler) assignmentRelevantHandoffs(ctx context.Context, assignment projectwork.Assignment, roleID string) []projectwork.Handoff {
	if h == nil || h.projectWork == nil {
		return nil
	}
	items, err := h.projectWork.ListHandoffs(ctx, projectwork.HandoffFilter{
		ProjectID:  assignment.ProjectID,
		WorkItemID: assignment.WorkItemID,
	})
	if err != nil {
		return nil
	}
	filtered := make([]projectwork.Handoff, 0, len(items))
	for _, item := range items {
		if item.SourceAssignmentID == assignment.ID || item.TargetAssignmentID == assignment.ID || item.TargetRoleID == roleID {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func appendProjectMemory(packet *chat.ContextPacket, entries []memory.Entry) {
	appendProjectMemoryWithInclusion(packet, entries, true, "Enabled project memory entry")
}

func appendProjectMemoryWithInclusion(packet *chat.ContextPacket, entries []memory.Entry, included bool, reason string) {
	for _, entry := range entries {
		trust := strings.TrimSpace(entry.TrustLabel)
		if trust == "" {
			trust = contextTrustOperatorMemory
		}
		sourceDetail := strings.TrimSpace(entry.SourceKind)
		if sourceID := strings.TrimSpace(entry.SourceID); sourceID != "" {
			sourceDetail = firstNonEmptyString(sourceDetail, "operator") + ":" + sourceID
		}
		appendContextPacketSourceWithSection(packet, contextSectionMemory, chat.ContextSource{
			Kind:   "memory",
			Label:  entry.Title,
			Detail: sourceDetail,
			Trust:  trust,
		}, chat.ContextItem{
			Kind:            "memory",
			TrustLevel:      trust,
			Origin:          entry.ID,
			Title:           entry.Title,
			Body:            entry.Body,
			Included:        included,
			InclusionReason: reason,
		})
	}
}

func appendProjectContextSources(packet *chat.ContextPacket, sources []chat.ContextSource) {
	appendProjectContextSourcesWithInclusion(packet, sources, true, "Enabled project context source metadata")
}

func appendProjectContextSourcesWithInclusion(packet *chat.ContextPacket, sources []chat.ContextSource, included bool, reason string) {
	for _, source := range sources {
		item := chat.ContextItem{
			Kind:            source.Kind,
			TrustLevel:      contextTrustWorkspaceGuidance,
			Origin:          firstNonEmptyString(source.Detail, source.Label),
			Title:           source.Label,
			BodyRef:         source.Detail,
			Included:        included,
			InclusionReason: reason,
		}
		appendContextPacketSourceWithSection(packet, contextSectionSources, source, item)
	}
}

func appendResolvedAgentProfile(packet *chat.ContextPacket, profile resolvedAgentProfile) {
	if strings.TrimSpace(profile.ID) == "" {
		return
	}
	body := []string{
		"ID: " + profile.ID,
		"Name: " + firstNonEmptyString(profile.Name, profile.ID),
		"Source: " + firstNonEmptyString(profile.Source, "unknown"),
		"Surface: " + firstNonEmptyString(profile.Surface, "any"),
		"Execution profile: " + firstNonEmptyString(profile.ExecutionProfile, profile.ID),
		"Provider hint: " + firstNonEmptyString(profile.ProviderHint, "inherit"),
		"Model hint: " + firstNonEmptyString(profile.ModelHint, "inherit"),
		"Tools enabled: " + boolLabel(profile.ToolsEnabled),
		"Writes allowed: " + boolLabel(profile.WritesAllowed),
		"Network allowed: " + boolLabel(profile.NetworkAllowed),
		"Approval policy: " + firstNonEmptyString(profile.ApprovalPolicy, "inherit"),
		"Project memory policy: " + firstNonEmptyString(profile.ProjectMemoryPolicy, "inherit"),
		"Context source policy: " + firstNonEmptyString(profile.ContextSourcePolicy, "inherit"),
	}
	if instructions := strings.TrimSpace(profile.Instructions); instructions != "" && !profile.Missing {
		body = append(body, "Instructions:\n"+instructions)
	}
	if len(profile.SkillIDs) > 0 {
		body = append(body, "Skills: "+strings.Join(profile.SkillIDs, ", "))
	}
	if externalAgent := strings.TrimSpace(profile.ExternalAgentKind); externalAgent != "" {
		body = append(body, "External agent: "+externalAgent)
	}
	if len(profile.Warnings) > 0 {
		body = append(body, "Warnings: "+strings.Join(profile.Warnings, " "))
	}
	appendContextPacketSourceWithSection(packet, contextSectionProfile, chat.ContextSource{
		Kind:   "agent_profile",
		Label:  firstNonEmptyString(profile.Name, profile.ID),
		Detail: profile.ID,
		Trust:  contextTrustRuntimeState,
	}, chat.ContextItem{
		Kind:            "agent_profile",
		TrustLevel:      contextTrustRuntimeState,
		Origin:          profile.ID,
		Title:           firstNonEmptyString(profile.Name, profile.ID),
		Body:            strings.Join(body, "\n"),
		Included:        !profile.Missing,
		InclusionReason: firstNonEmptyString(profile.Source, "resolved profile"),
	})
	for _, warning := range profile.Warnings {
		appendContextPacketSourceWithSection(packet, contextSectionProfile, chat.ContextSource{
			Kind:   "profile_warning",
			Label:  "Profile warning",
			Detail: profile.ID,
			Trust:  contextTrustRuntimeState,
		}, chat.ContextItem{
			Kind:            "profile_warning",
			TrustLevel:      contextTrustRuntimeState,
			Origin:          profile.ID,
			Title:           "Profile warning",
			Body:            warning,
			Included:        false,
			InclusionReason: "Profile resolution warning",
		})
	}
}

type resolvedProjectSkills struct {
	Requested []string
	Resolved  []projectskills.Skill
	Skipped   []resolvedProjectSkillSkip
	Warnings  []string
}

type resolvedProjectSkillSkip struct {
	ID     string
	Reason string
	Status string
}

func (h *Handler) resolveProjectAssignmentSkills(ctx context.Context, projectID string, role projectwork.AgentRoleProfile, profile resolvedAgentProfile) resolvedProjectSkills {
	requested := normalizeContextStringList(append(append([]string(nil), role.SkillIDs...), profile.SkillIDs...))
	result := resolvedProjectSkills{Requested: requested}
	if len(requested) == 0 {
		return result
	}
	if h == nil || h.projectSkills == nil {
		result.Warnings = []string{"Project skills store is not configured; skill references were not resolved."}
		for _, id := range requested {
			result.Skipped = append(result.Skipped, resolvedProjectSkillSkip{ID: id, Reason: "store_unavailable"})
		}
		return result
	}
	items, err := h.projectSkills.List(ctx, projectID)
	if err != nil {
		result.Warnings = []string{"Project skills could not be loaded; skill references were not resolved."}
		for _, id := range requested {
			result.Skipped = append(result.Skipped, resolvedProjectSkillSkip{ID: id, Reason: "store_error"})
		}
		return result
	}
	byID := make(map[string]projectskills.Skill, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}
	for _, id := range requested {
		item, ok := byID[id]
		switch {
		case !ok:
			result.Skipped = append(result.Skipped, resolvedProjectSkillSkip{ID: id, Reason: "missing", Status: projectskills.StatusMissing})
		case !item.Enabled:
			result.Skipped = append(result.Skipped, resolvedProjectSkillSkip{ID: id, Reason: "disabled", Status: item.Status})
		case item.Status != projectskills.StatusAvailable:
			result.Skipped = append(result.Skipped, resolvedProjectSkillSkip{ID: id, Reason: item.Status, Status: item.Status})
		default:
			result.Resolved = append(result.Resolved, item)
		}
	}
	return result
}

func appendResolvedProjectSkills(packet *chat.ContextPacket, skills resolvedProjectSkills) {
	if len(skills.Requested) == 0 {
		return
	}
	body := []string{
		"Requested: " + strings.Join(skills.Requested, ", "),
	}
	if len(skills.Resolved) > 0 {
		var resolved []string
		for _, skill := range skills.Resolved {
			resolved = append(resolved, fmt.Sprintf("%s (%s)", skill.ID, skill.Path))
		}
		body = append(body, "Resolved enabled skills: "+strings.Join(resolved, ", "))
	} else {
		body = append(body, "Resolved enabled skills: none")
	}
	if len(skills.Skipped) > 0 {
		var skipped []string
		for _, item := range skills.Skipped {
			skipped = append(skipped, fmt.Sprintf("%s:%s", item.ID, item.Reason))
		}
		body = append(body, "Skipped skills: "+strings.Join(skipped, ", "))
	}
	if len(skills.Warnings) > 0 {
		body = append(body, "Warnings: "+strings.Join(skills.Warnings, " "))
	}
	appendContextPacketSourceWithSection(packet, contextSectionProfile, chat.ContextSource{
		Kind:   "project_skills",
		Label:  "Project skills",
		Detail: strings.Join(skills.Requested, ","),
		Trust:  projectskills.TrustWorkspaceSkill,
	}, chat.ContextItem{
		Kind:            "project_skills",
		TrustLevel:      projectskills.TrustWorkspaceSkill,
		Origin:          "project_skills",
		Title:           "Project skills",
		Body:            strings.Join(body, "\n"),
		Included:        len(skills.Resolved) > 0,
		InclusionReason: "Skill metadata resolved for this assignment; skill bodies are not injected",
	})
}

func boolLabel(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func normalizeContextStringList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func appendProjectAssignmentHandoffs(packet *chat.ContextPacket, items []projectwork.Handoff, included bool, reason string) {
	for _, item := range items {
		trust := firstNonEmptyString(strings.TrimSpace(item.TrustLabel), contextTrustRuntimeState)
		label := firstNonEmptyString(strings.TrimSpace(item.Title), strings.TrimSpace(item.ID))
		appendContextPacketSourceWithSection(packet, contextSectionProjectWork, chat.ContextSource{
			Kind:   "handoff",
			Label:  label,
			Detail: strings.TrimSpace(item.ID),
			Trust:  trust,
		}, chat.ContextItem{
			Kind:            "handoff",
			TrustLevel:      trust,
			Origin:          strings.TrimSpace(item.ID),
			Title:           label,
			Body:            strings.TrimSpace(item.Summary),
			BodyRef:         firstNonEmptyString(strings.TrimSpace(item.SourceMessageID), strings.TrimSpace(item.SourceRunID)),
			Included:        included,
			InclusionReason: reason,
		})
	}
}

func appendProjectAssignmentArtifacts(packet *chat.ContextPacket, items []projectwork.CollaborationArtifact, included bool, reason string) {
	for _, item := range items {
		label := firstNonEmptyString(strings.TrimSpace(item.Title), strings.TrimSpace(item.ID))
		appendContextPacketSourceWithSection(packet, contextSectionProjectWork, chat.ContextSource{
			Kind:   "artifact_ref",
			Label:  label,
			Detail: strings.TrimSpace(item.Kind),
			Trust:  "project",
		}, chat.ContextItem{
			Kind:            "artifact_ref",
			TrustLevel:      contextTrustRuntimeState,
			Origin:          strings.TrimSpace(item.ID),
			Title:           label,
			BodyRef:         strings.TrimSpace(item.ID),
			Included:        included,
			InclusionReason: reason,
		})
	}
}

func appendTranscriptContext(packet *chat.ContextPacket) {
	source := transcriptContextSource(packet.MessageCount)
	appendContextPacketSourceWithSection(packet, contextSectionRuntime, source, chat.ContextItem{
		Kind:            "transcript",
		TrustLevel:      contextTrustRuntimeState,
		Origin:          "chat.transcript",
		Title:           "Chat transcript",
		Body:            source.Detail,
		Included:        true,
		InclusionReason: "Visible terminal transcript count for this turn",
	})
}

func appendProjectSummary(packet *chat.ContextPacket, project *projects.Project, included bool, reason string) {
	if project == nil || packet == nil {
		return
	}
	populateProjectRefs(packet, project.ID)
	label := strings.TrimSpace(project.Name)
	if label == "" {
		label = strings.TrimSpace(project.ID)
	}
	appendContextPacketSourceWithSection(packet, contextSectionProject, chat.ContextSource{
		Kind:   "project",
		Label:  label,
		Detail: strings.TrimSpace(project.ID),
		Trust:  "project",
	}, chat.ContextItem{
		Kind:            "project",
		TrustLevel:      contextTrustRuntimeState,
		Origin:          strings.TrimSpace(project.ID),
		Title:           label,
		Included:        included,
		InclusionReason: reason,
	})
}

func appendContextPacketSourceWithSection(packet *chat.ContextPacket, section string, source chat.ContextSource, item chat.ContextItem) {
	item.Section = firstNonEmptyString(strings.TrimSpace(item.Section), section)
	appendContextPacketSource(packet, source, item)
}

func appendContextPacketSource(packet *chat.ContextPacket, source chat.ContextSource, item chat.ContextItem) {
	packet.Sources = append(packet.Sources, source)
	packet.Items = append(packet.Items, item)
}

func populateProjectRefs(packet *chat.ContextPacket, projectID string) {
	if packet == nil || strings.TrimSpace(projectID) == "" {
		return
	}
	if packet.Refs == nil {
		packet.Refs = &chat.ContextRefs{}
	}
	if packet.Refs.ProjectID == "" {
		packet.Refs.ProjectID = strings.TrimSpace(projectID)
	}
}

func projectContextSourceKind(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	switch kind {
	case "", "doc":
		// Operator-configured docs should render beside native workspace
		// sources; other project kinds stay namespaced to avoid collisions.
		return "workspace_doc"
	case "workspace_instruction", "host_instruction", "path_instruction", "host_rule", "host_command", "host_agent_definition":
		return kind
	default:
		return "project_" + strings.NewReplacer(" ", "_", "-", "_").Replace(kind)
	}
}

func normalizeContextPacket(packet chat.ContextPacket, refs chat.ContextRefs) chat.ContextPacket {
	packet = cloneContextPacket(packet)
	if packet.Refs == nil && !contextRefsEmpty(refs) {
		packet.Refs = &chat.ContextRefs{}
	}
	if packet.Refs != nil {
		packet.Refs.SessionID = firstNonEmptyString(packet.Refs.SessionID, refs.SessionID)
		packet.Refs.MessageID = firstNonEmptyString(packet.Refs.MessageID, refs.MessageID)
		packet.Refs.TaskID = firstNonEmptyString(packet.Refs.TaskID, refs.TaskID)
		packet.Refs.RunID = firstNonEmptyString(packet.Refs.RunID, refs.RunID)
		packet.Refs.ProjectID = firstNonEmptyString(packet.Refs.ProjectID, refs.ProjectID)
		packet.Refs.WorkItemID = firstNonEmptyString(packet.Refs.WorkItemID, refs.WorkItemID)
		packet.Refs.AssignmentID = firstNonEmptyString(packet.Refs.AssignmentID, refs.AssignmentID)
		packet.Refs.RoleID = firstNonEmptyString(packet.Refs.RoleID, refs.RoleID)
		if contextRefsEmpty(*packet.Refs) {
			packet.Refs = nil
		}
	}
	for idx := range packet.Items {
		if strings.TrimSpace(packet.Items[idx].Section) == "" {
			packet.Items[idx].Section = defaultContextItemSection(packet.Items[idx].Kind)
		}
	}
	return packet
}

func cloneContextPacket(packet chat.ContextPacket) chat.ContextPacket {
	if packet.Refs != nil {
		refs := *packet.Refs
		packet.Refs = &refs
	}
	if len(packet.Sources) > 0 {
		packet.Sources = append([]chat.ContextSource(nil), packet.Sources...)
	}
	if len(packet.Items) > 0 {
		packet.Items = append([]chat.ContextItem(nil), packet.Items...)
	}
	return packet
}

func marshalContextPacket(packet chat.ContextPacket) json.RawMessage {
	if packet.Empty() {
		return nil
	}
	data, err := json.Marshal(packet)
	if err != nil {
		return nil
	}
	return data
}

func contextRefsEmpty(refs chat.ContextRefs) bool {
	return refs.SessionID == "" &&
		refs.MessageID == "" &&
		refs.TaskID == "" &&
		refs.RunID == "" &&
		refs.ProjectID == "" &&
		refs.WorkItemID == "" &&
		refs.AssignmentID == "" &&
		refs.RoleID == ""
}

func defaultContextItemSection(kind string) string {
	switch {
	case kind == "system_prompt":
		return contextSectionInstructions
	case kind == "memory":
		return contextSectionMemory
	case kind == "workspace":
		return contextSectionWorkspace
	case kind == "project":
		return contextSectionProject
	case kind == "work_item" || kind == "assignment" || kind == "role" || kind == "execution_hints" || kind == "handoff" || kind == "artifact_ref":
		return contextSectionProjectWork
	case kind == "transcript" || kind == "task_runtime" || kind == "external_agent_session":
		return contextSectionRuntime
	case strings.HasPrefix(kind, "workspace_") || strings.HasPrefix(kind, "project_"):
		return contextSectionSources
	default:
		return contextSectionRuntime
	}
}

func baseChatContextPacket(mode, provider, model, workspace string) chat.ContextPacket {
	return chat.ContextPacket{
		Version:       chatContextPacketVersion,
		ExecutionMode: mode,
		Provider:      strings.TrimSpace(provider),
		Model:         strings.TrimSpace(model),
		Workspace:     strings.TrimSpace(workspace),
	}
}

func transcriptContextSource(count int) chat.ContextSource {
	detail := "Current user message"
	if count > 1 {
		detail = fmt.Sprintf("%d chat messages including this turn", count)
	}
	return chat.ContextSource{
		Kind:   "transcript",
		Label:  "Chat transcript",
		Detail: detail,
		Trust:  "operator",
	}
}

// chatTranscriptMessageCount intentionally counts visible, terminal transcript
// messages. It is an operator-facing history count, not a promise that every
// counted byte was packed into the provider or adapter prompt.
func chatTranscriptMessageCount(messages []chat.Message) int {
	count := 0
	for _, message := range messages {
		if message.Role != "user" && message.Role != "assistant" {
			continue
		}
		if message.Role == "assistant" && !isTerminalAgentChatStatus(message.Status) {
			continue
		}
		if strings.TrimSpace(message.Content) == "" {
			continue
		}
		count++
	}
	return count
}
