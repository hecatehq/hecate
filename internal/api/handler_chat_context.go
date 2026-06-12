package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/hecatehq/hecate/internal/agentprofiles"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/chatcontext"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/projectworkapp"
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
	contextSectionSkills       = "skills"
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
		writeChatContextPacket(w, chatcontext.Normalize(message.Context, chatcontext.ChatMessageRefs(sessionID, messageID, session.ProjectID)))
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
	writeChatContextPacket(w, chatcontext.Normalize(packet, chatcontext.TaskRunRefs(task.ID, run.ID, task.ProjectID)))
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
	writeChatContextPacket(w, chatcontext.Normalize(packet, chatcontext.MergeRefs(
		chatcontext.ProjectAssignmentRefs(projectID, workItemID, assignmentID, assignment.RoleID),
		chatcontext.TaskRunRefs(assignment.ExecutionRef.TaskID, assignment.ExecutionRef.RunID, projectID),
		chatcontext.ChatMessageRefs(assignment.ExecutionRef.ChatSessionID, assignment.ExecutionRef.MessageID, projectID),
	)))
}

func (h *Handler) contextPacketForTaskRun(ctx context.Context, task types.Task, run types.TaskRun) (chat.ContextPacket, bool, error) {
	if packet, ok, err := chatcontext.FromTaskRun(run); err != nil || ok {
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
		packet, found := chatcontext.FromSessionRun(session, task.ID, run.ID)
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
		if packet, ok := chatcontext.FromSessionRun(session, task.ID, run.ID); ok {
			return packet, true, nil
		}
	}
	return chat.ContextPacket{}, false, nil
}

func (h *Handler) contextPacketForProjectAssignment(ctx context.Context, assignment projectwork.Assignment) (chat.ContextPacket, bool, error) {
	ref := projectwork.NormalizeAssignmentExecutionRef(assignment.ExecutionRef)
	if h != nil && h.taskStore != nil && strings.TrimSpace(ref.TaskID) != "" && strings.TrimSpace(ref.RunID) != "" {
		task, ok, err := h.taskStore.GetTask(ctx, ref.TaskID)
		if err != nil {
			return chat.ContextPacket{}, false, err
		}
		if ok {
			run, ok, err := h.taskStore.GetRun(ctx, ref.TaskID, ref.RunID)
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
	if packet, ok, err := chatcontext.FromProjectAssignmentPayload(assignment.ContextPacket); err != nil || ok {
		return packet, ok, err
	}
	if h == nil || h.agentChat == nil || strings.TrimSpace(ref.ChatSessionID) == "" || strings.TrimSpace(ref.MessageID) == "" {
		return chat.ContextPacket{}, false, nil
	}
	session, ok, err := h.agentChat.Get(ctx, ref.ChatSessionID)
	if err != nil || !ok {
		return chat.ContextPacket{}, false, err
	}
	for _, message := range session.Messages {
		if message.ID != strings.TrimSpace(ref.MessageID) {
			continue
		}
		packet, found := chatcontext.FromSessionMessage(session, message.ID)
		return packet, found, nil
	}
	return chat.ContextPacket{}, false, nil
}

func (h *Handler) directModelContextPacket(ctx context.Context, session chat.Session, provider, model, systemPrompt string) chat.ContextPacket {
	packet := baseChatContextPacket(chat.ExecutionModeHecateTask, provider, model, session.Workspace)
	packet.SystemPromptIncluded = strings.TrimSpace(systemPrompt) != ""
	packet.MessageCount = chatTranscriptMessageCount(session.Messages) + 1
	populateProjectRefs(&packet, session.ProjectID)
	appendProjectSummary(&packet, h.projectSummary(ctx, session.ProjectID), true, "Project linked to this chat session")
	appendProjectChatSkills(&packet, h.projectChatEnabledSkills(ctx, session.ProjectID), true, "Enabled project skill metadata for this direct model turn; skill bodies are not injected")
	appendProjectChatWork(&packet, h.projectChatWorkSnapshot(ctx, session.ProjectID), true, "Current project work metadata for this direct model turn")
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
	appendProjectChatSkills(&packet, h.projectChatEnabledSkills(ctx, session.ProjectID), true, "Enabled project skill metadata for this task-backed turn; skill bodies are not injected")
	appendProjectChatWork(&packet, h.projectChatWorkSnapshot(ctx, session.ProjectID), true, "Current project work metadata for this task-backed turn")
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

func (h *Handler) projectAssignmentContextPacket(ctx context.Context, project projects.Project, workItem projectwork.WorkItem, assignment projectwork.Assignment, role projectwork.AgentRoleProfile, workingDirectory, provider, model, executionProfile string, profile projectworkapp.ResolvedAgentProfile, skills projectworkapp.ResolvedProjectSkills, promptContext projectworkapp.AssignmentPromptContext) chat.ContextPacket {
	packet := baseChatContextPacket(chat.ExecutionModeHecateTask, provider, model, workingDirectory)
	driverKind := firstNonEmptyString(strings.TrimSpace(assignment.DriverKind), projectwork.AssignmentDriverHecateTask)
	includedReason := "Included in the native project assignment launch context"
	if driverKind == projectwork.AssignmentDriverExternalAgent {
		includedReason = "Included in the external-agent assignment launch context"
	}
	packet.ID = newChatID("ctx")
	packet.ExecutionProfile = strings.TrimSpace(executionProfile)
	packet.SystemPromptIncluded = strings.TrimSpace(projectworkapp.AssignmentSystemPrompt(project, role, profile, promptContext)) != ""
	ref := projectwork.NormalizeAssignmentExecutionRef(assignment.ExecutionRef)
	packet.Refs = &chat.ContextRefs{
		TaskID:       strings.TrimSpace(ref.TaskID),
		RunID:        strings.TrimSpace(ref.RunID),
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
	appendProjectAssignmentPromptContext(&packet, promptContext)
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
	appendProjectMemoryForProfilePolicy(&packet, h.enabledProjectMemoryEntries(ctx, project.ID), profile)
	appendProjectContextSourcesForProfilePolicy(&packet, projectContextSourcesFromProject(project), profile)
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

func appendProjectChatSkills(packet *chat.ContextPacket, skills []projectskills.Skill, included bool, reason string) {
	body := projectChatSkillHintText(skills, projectChatPromptSkillMaxItems)
	if body == "" {
		return
	}
	appendContextPacketSourceWithSection(packet, contextSectionSkills, chat.ContextSource{
		Kind:   "project_skills",
		Label:  "Project skills",
		Detail: fmt.Sprintf("%d enabled", len(skills)),
		Trust:  projectskills.TrustWorkspaceSkill,
	}, chat.ContextItem{
		Kind:            "project_skills",
		TrustLevel:      projectskills.TrustWorkspaceSkill,
		Origin:          "project_skills",
		Title:           "Project skills",
		Body:            body,
		Included:        included,
		InclusionReason: reason,
	})
}

func appendProjectChatWork(packet *chat.ContextPacket, snapshot projectChatWorkSnapshot, included bool, reason string) {
	body := projectChatWorkHintText(snapshot, projectChatPromptWorkMaxItems, projectChatPromptAssignmentMaxItems)
	if body == "" {
		return
	}
	appendContextPacketSourceWithSection(packet, contextSectionProjectWork, chat.ContextSource{
		Kind:   "project_work",
		Label:  "Project work",
		Detail: fmt.Sprintf("%d active work items, %d active assignments", len(snapshot.WorkItems), len(snapshot.Assignments)),
		Trust:  contextTrustProject,
	}, chat.ContextItem{
		Kind:            "project_work",
		TrustLevel:      contextTrustProject,
		Origin:          "project_work",
		Title:           "Project work",
		Body:            body,
		Included:        included,
		InclusionReason: reason,
	})
}

func appendProjectMemoryForProfilePolicy(packet *chat.ContextPacket, entries []memory.Entry, profile projectworkapp.ResolvedAgentProfile) {
	policy := effectiveProjectMemoryPolicy(profile.ProjectMemoryPolicy)
	switch policy {
	case agentprofiles.MemoryExclude:
		return
	case agentprofiles.MemoryInclude:
		appendProjectMemoryWithInclusion(packet, entries, true, "Activated by agent profile project_memory_policy=include")
	default:
		appendProjectMemoryWithInclusion(packet, entries, false, "Visible only by agent profile project_memory_policy="+firstNonEmptyString(strings.TrimSpace(profile.ProjectMemoryPolicy), agentprofiles.MemoryInherit))
	}
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

func appendProjectContextSourcesForProfilePolicy(packet *chat.ContextPacket, sources []chat.ContextSource, profile projectworkapp.ResolvedAgentProfile) {
	policy := effectiveContextSourcePolicy(profile.ContextSourcePolicy)
	switch policy {
	case agentprofiles.ContextExclude:
		return
	case agentprofiles.ContextIncludeEnabled:
		appendProjectContextSourcesWithInclusion(packet, sources, true, "Activated by agent profile context_source_policy=include_enabled; eligible source bodies may be loaded into the native assignment prompt")
	default:
		appendProjectContextSourcesWithInclusion(packet, sources, false, "Visible only by agent profile context_source_policy="+firstNonEmptyString(strings.TrimSpace(profile.ContextSourcePolicy), agentprofiles.ContextInherit))
	}
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

func effectiveProjectMemoryPolicy(policy string) string {
	switch strings.TrimSpace(policy) {
	case agentprofiles.MemoryInclude:
		return agentprofiles.MemoryInclude
	case agentprofiles.MemoryExclude:
		return agentprofiles.MemoryExclude
	case agentprofiles.MemoryVisibleOnly:
		return agentprofiles.MemoryVisibleOnly
	default:
		return agentprofiles.MemoryVisibleOnly
	}
}

func effectiveContextSourcePolicy(policy string) string {
	switch strings.TrimSpace(policy) {
	case agentprofiles.ContextIncludeEnabled:
		return agentprofiles.ContextIncludeEnabled
	case agentprofiles.ContextExclude:
		return agentprofiles.ContextExclude
	case agentprofiles.ContextVisibleOnly:
		return agentprofiles.ContextVisibleOnly
	default:
		return agentprofiles.ContextVisibleOnly
	}
}

func appendResolvedAgentProfile(packet *chat.ContextPacket, profile projectworkapp.ResolvedAgentProfile) {
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

func appendResolvedProjectSkills(packet *chat.ContextPacket, skills projectworkapp.ResolvedProjectSkills) {
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
	appendContextPacketSourceWithSection(packet, contextSectionSkills, chat.ContextSource{
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

func appendProjectAssignmentPromptContext(packet *chat.ContextPacket, promptContext projectworkapp.AssignmentPromptContext) {
	if len(promptContext.Sections) == 0 && len(promptContext.Warnings) == 0 {
		return
	}
	body := []string{
		fmt.Sprintf("Included project memory entries: %d", promptContext.IncludedMemory),
		fmt.Sprintf("Included workspace instruction sources: %d", promptContext.IncludedSources),
		fmt.Sprintf("Truncated prompt context items: %d", promptContext.Truncated),
	}
	if len(promptContext.Warnings) > 0 {
		body = append(body, "Warnings: "+strings.Join(promptContext.Warnings, " "))
	}
	appendContextPacketSourceWithSection(packet, contextSectionInstructions, chat.ContextSource{
		Kind:   "prompt_context",
		Label:  "Prompt context policy",
		Detail: "project assignment profile policies",
		Trust:  contextTrustRuntimeState,
	}, chat.ContextItem{
		Kind:            "prompt_context",
		TrustLevel:      contextTrustRuntimeState,
		Origin:          "project_assignment.prompt_context",
		Title:           "Prompt context policy",
		Body:            strings.Join(body, "\n"),
		Included:        len(promptContext.Sections) > 0,
		InclusionReason: "Profile memory/source policies applied to the native assignment prompt",
	})
}

func boolLabel(v bool) string {
	if v {
		return "true"
	}
	return "false"
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
