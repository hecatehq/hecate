package api

import (
	"context"
	"fmt"
	"strings"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/chatapp"
	"github.com/hecatehq/hecate/internal/chatattachments"
	"github.com/hecatehq/hecate/internal/orchestrator"
	"github.com/hecatehq/hecate/pkg/types"
)

// resolveHecateAgentInput hydrates a Hecate Chat image at the last responsible
// moment. Task state carries only run.InputRef; the binary body remains owned
// by chatattachments and is integrity-checked against immutable transcript
// metadata before it can cross the provider boundary.
func (h *Handler) resolveHecateAgentInput(ctx context.Context, task types.Task, run types.TaskRun) (orchestrator.AgentInput, error) {
	if task.OriginKind != "chat" || strings.TrimSpace(task.OriginID) == "" {
		return orchestrator.AgentInput{}, fmt.Errorf("rich task input is only available to chat-origin runs")
	}
	inputRef := strings.TrimSpace(run.InputRef)
	if inputRef == "" {
		return orchestrator.AgentInput{}, fmt.Errorf("rich task input reference is required")
	}
	sessionResult, err := h.chatApplication().GetSession(ctx, task.OriginID)
	if err != nil {
		return orchestrator.AgentInput{}, fmt.Errorf("load input owner: %w", err)
	}
	session := sessionResult.Session
	if !isHecateChatSession(session) {
		return orchestrator.AgentInput{}, fmt.Errorf("rich task input owner is not a Hecate chat")
	}
	var inputMessage chat.Message
	found := false
	for _, message := range session.Messages {
		if message.ID == inputRef {
			inputMessage = message
			found = true
			break
		}
	}
	if !found || inputMessage.Role != "user" || !inputMessage.ToolsEnabled || inputMessage.ExecutionMode != chat.ExecutionModeHecateTask {
		return orchestrator.AgentInput{}, fmt.Errorf("rich task input does not reference a tools-on Hecate user message")
	}
	if inputMessage.TaskID != "" && inputMessage.TaskID != task.ID {
		return orchestrator.AgentInput{}, fmt.Errorf("rich task input belongs to a different task")
	}
	if len(inputMessage.Attachments) == 0 {
		return orchestrator.AgentInput{}, fmt.Errorf("rich task input has no attachment metadata")
	}

	provider := strings.TrimSpace(run.Provider)
	messageProvider := strings.TrimSpace(inputMessage.Provider)
	if provider == "" {
		provider = messageProvider
	}
	if provider != "" && messageProvider != "" && provider != messageProvider {
		return orchestrator.AgentInput{}, fmt.Errorf("image provider does not match the admitted input route")
	}
	providerInstance := run.InputProviderInstance
	if inputMessage.ProviderInstance.Valid() {
		if providerInstance.Valid() && providerInstance != inputMessage.ProviderInstance {
			return orchestrator.AgentInput{}, fmt.Errorf("image provider instance does not match the admitted input route")
		}
		providerInstance = inputMessage.ProviderInstance
	}
	if provider != "" && !providerInstance.Valid() {
		return orchestrator.AgentInput{}, fmt.Errorf("image provider instance fence is missing")
	}
	model := strings.TrimSpace(run.Model)
	route, err := h.modelApplication().ResolveProviderRoute(ctx, provider, model)
	if err != nil {
		return orchestrator.AgentInput{}, fmt.Errorf("resolve image provider: %w", err)
	}
	if route.Name != "" && route.Name != provider {
		return orchestrator.AgentInput{}, fmt.Errorf("image provider route changed before execution")
	}
	if providerInstance.Valid() && route.Instance != providerInstance {
		return orchestrator.AgentInput{}, fmt.Errorf("image provider instance changed before execution")
	}
	imageCapable, err := h.modelApplication().SupportsImageInput(ctx, provider, model)
	if err != nil {
		return orchestrator.AgentInput{}, fmt.Errorf("resolve image capability: %w", err)
	}
	if !imageCapable {
		return orchestrator.AgentInput{}, fmt.Errorf("selected model route does not declare image-input support")
	}
	if h.chatImageTurnAdmission == nil || !h.chatImageTurnAdmission.Acquire(ctx) {
		return orchestrator.AgentInput{}, fmt.Errorf("image input admission cancelled before execution")
	}
	release := h.chatImageTurnAdmission.Release
	releaseOnError := true
	defer func() {
		if releaseOnError {
			release()
		}
	}()

	attachments := make([]chatattachments.StoredAttachment, 0, len(inputMessage.Attachments))
	for _, metadata := range inputMessage.Attachments {
		attachment, err := h.chatApplication().GetAttachment(ctx, chatapp.AttachmentCommand{
			SessionID:    session.ID,
			AttachmentID: metadata.ID,
		})
		if err != nil {
			return orchestrator.AgentInput{}, fmt.Errorf("load image attachment: %w", err)
		}
		if err := validateStoredChatAttachmentTranscript(session.ID, metadata, attachment); err != nil {
			return orchestrator.AgentInput{}, fmt.Errorf("image attachment metadata mismatch")
		}
		if err := validateStoredChatImageAttachment(attachment); err != nil {
			return orchestrator.AgentInput{}, fmt.Errorf("stored image attachment failed integrity validation")
		}
		attachments = append(attachments, attachment)
	}

	releaseOnError = false
	return orchestrator.AgentInput{
		Message: chatModelMessageWithAttachments(inputMessage.Content, attachments, nil),
		Requirements: types.ChatRequestRequirements{
			ImageInput:         true,
			NoProviderFailover: true,
			ExactProvider:      provider != "",
			ProviderInstance:   providerInstance,
		},
		Release: release,
	}, nil
}
