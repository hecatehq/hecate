package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/pkg/types"
)

const chatContextPacketVersion = "chat.context.v1"

const (
	contextTrustSystemInstruction = "system_instruction"
	contextTrustOperatorMemory    = "operator_memory"
	contextTrustWorkspaceGuidance = "workspace_guidance"
	contextTrustRuntimeState      = "runtime_state"
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
		writeChatContextPacket(w, message.Context)
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
		WriteError(w, http.StatusNotFound, errCodeNotFound, "task run context packet not found; only task-backed Hecate Chat runs currently expose context snapshots")
		return
	}
	writeChatContextPacket(w, packet)
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

func (h *Handler) contextPacketForTaskRun(ctx context.Context, task types.Task, run types.TaskRun) (chat.ContextPacket, bool, error) {
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

func (h *Handler) directModelContextPacket(ctx context.Context, session chat.Session, provider, model, systemPrompt string) chat.ContextPacket {
	packet := baseChatContextPacket(chat.ExecutionModeHecateTask, provider, model, session.Workspace)
	packet.SystemPromptIncluded = strings.TrimSpace(systemPrompt) != ""
	packet.MessageCount = chatTranscriptMessageCount(session.Messages) + 1
	if packet.SystemPromptIncluded {
		appendContextPacketSource(&packet, chat.ContextSource{
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
		appendContextPacketSource(&packet, chat.ContextSource{
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
	if packet.SystemPromptIncluded {
		appendContextPacketSource(&packet, chat.ContextSource{
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
		appendContextPacketSource(&packet, chat.ContextSource{
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
	appendContextPacketSource(&packet, chat.ContextSource{
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
	appendProjectMemory(&packet, h.projectMemoryEntries(ctx, session))
	if strings.TrimSpace(session.Workspace) != "" {
		appendContextPacketSource(&packet, chat.ContextSource{
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
	appendContextPacketSource(&packet, chat.ContextSource{
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

func (h *Handler) projectContextSources(ctx context.Context, session chat.Session) []chat.ContextSource {
	if h == nil || h.projects == nil || strings.TrimSpace(session.ProjectID) == "" {
		return nil
	}
	project, ok, err := h.projects.Get(ctx, session.ProjectID)
	if err != nil || !ok {
		return nil
	}
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
		sources = append(sources, chat.ContextSource{
			Kind:   projectContextSourceKind(source.Kind),
			Label:  label,
			Detail: strings.TrimSpace(source.Path),
			Trust:  "project",
		})
	}
	return sources
}

func (h *Handler) projectMemoryEntries(ctx context.Context, session chat.Session) []memory.Entry {
	if h == nil || h.memory == nil || strings.TrimSpace(session.ProjectID) == "" {
		return nil
	}
	items, err := h.memory.List(ctx, memory.Filter{ProjectID: session.ProjectID})
	if err != nil {
		return nil
	}
	return items
}

func appendProjectMemory(packet *chat.ContextPacket, entries []memory.Entry) {
	for _, entry := range entries {
		trust := strings.TrimSpace(entry.TrustLabel)
		if trust == "" {
			trust = contextTrustOperatorMemory
		}
		sourceDetail := strings.TrimSpace(entry.SourceKind)
		if sourceID := strings.TrimSpace(entry.SourceID); sourceID != "" {
			sourceDetail = firstNonEmptyString(sourceDetail, "operator") + ":" + sourceID
		}
		appendContextPacketSource(packet, chat.ContextSource{
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
			Included:        true,
			InclusionReason: "Enabled project memory entry",
		})
	}
}

func appendProjectContextSources(packet *chat.ContextPacket, sources []chat.ContextSource) {
	for _, source := range sources {
		item := chat.ContextItem{
			Kind:            source.Kind,
			TrustLevel:      contextTrustWorkspaceGuidance,
			Origin:          firstNonEmptyString(source.Detail, source.Label),
			Title:           source.Label,
			BodyRef:         source.Detail,
			Included:        true,
			InclusionReason: "Enabled project context source metadata",
		}
		appendContextPacketSource(packet, source, item)
	}
}

func appendTranscriptContext(packet *chat.ContextPacket) {
	source := transcriptContextSource(packet.MessageCount)
	appendContextPacketSource(packet, source, chat.ContextItem{
		Kind:            "transcript",
		TrustLevel:      contextTrustRuntimeState,
		Origin:          "chat.transcript",
		Title:           "Chat transcript",
		Body:            source.Detail,
		Included:        true,
		InclusionReason: "Visible terminal transcript count for this turn",
	})
}

func appendContextPacketSource(packet *chat.ContextPacket, source chat.ContextSource, item chat.ContextItem) {
	packet.Sources = append(packet.Sources, source)
	packet.Items = append(packet.Items, item)
}

func projectContextSourceKind(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	switch kind {
	case "", "doc":
		// Operator-configured docs should render beside native workspace
		// sources; other project kinds stay namespaced to avoid collisions.
		return "workspace_doc"
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
