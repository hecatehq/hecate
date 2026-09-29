package api

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/chatapp"
	"github.com/hecatehq/hecate/internal/chatattachments"
	"github.com/hecatehq/hecate/internal/modelcaps"
	"github.com/hecatehq/hecate/internal/modelprobe"
	"github.com/hecatehq/hecate/internal/orchestrator"
	"github.com/hecatehq/hecate/pkg/types"
)

// resolveHecateAgentInput hydrates images and provides body-free text-file
// references. Task state carries only run.InputRef; scoped text reads hydrate
// later, with transcript integrity and the final provider route rechecked.
func (h *Handler) resolveHecateAgentInput(ctx context.Context, task types.Task, run types.TaskRun) (orchestrator.AgentInput, error) {
	if task.OriginKind != "chat" || strings.TrimSpace(task.OriginID) == "" {
		return orchestrator.AgentInput{}, fmt.Errorf("rich task input is only available to chat-origin runs")
	}
	inputRef := strings.TrimSpace(run.InputRef)
	if inputRef == "" {
		return orchestrator.AgentInput{}, fmt.Errorf("rich task input reference is required")
	}
	if h.agentChatLive == nil {
		return orchestrator.AgentInput{}, chatapp.ErrNativeAttachmentUnavailable
	}
	sessionID := strings.TrimSpace(task.OriginID)
	lifecycle := h.agentChatLive.snapshotLifecycle(sessionID)
	keepLifecycle := false
	defer func() {
		if !keepLifecycle {
			lifecycle.release()
		}
	}()
	sessionResult, err := h.chatApplication().GetSession(ctx, sessionID)
	if err != nil {
		return orchestrator.AgentInput{}, fmt.Errorf("load input owner: %w", err)
	}
	session := sessionResult.Session
	if session.ID != sessionID || !isHecateChatSession(session) {
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
		return orchestrator.AgentInput{}, fmt.Errorf("attachment provider does not match the admitted input route")
	}
	providerInstance := run.InputProviderInstance
	if inputMessage.ProviderInstance.Valid() {
		if providerInstance.Valid() && providerInstance != inputMessage.ProviderInstance {
			return orchestrator.AgentInput{}, fmt.Errorf("attachment provider instance does not match the admitted input route")
		}
		providerInstance = inputMessage.ProviderInstance
	}
	if provider != "" && !providerInstance.Valid() {
		return orchestrator.AgentInput{}, fmt.Errorf("attachment provider instance fence is missing")
	}
	model := strings.TrimSpace(run.Model)
	route, err := h.modelApplication().ResolveProviderRoute(ctx, provider, model)
	if err != nil {
		return orchestrator.AgentInput{}, fmt.Errorf("resolve attachment provider: %w", err)
	}
	if route.Name != "" && route.Name != provider {
		return orchestrator.AgentInput{}, fmt.Errorf("attachment provider route changed before execution")
	}
	if providerInstance.Valid() && route.Instance != providerInstance {
		return orchestrator.AgentInput{}, fmt.Errorf("attachment provider instance changed before execution")
	}
	hasImages := false
	for _, metadata := range inputMessage.Attachments {
		if metadata.MediaType != "text/plain" {
			hasImages = true
		}
	}
	if hasImages {
		imageCapable, err := h.modelApplication().SupportsImageInput(ctx, provider, model)
		if err != nil {
			return orchestrator.AgentInput{}, fmt.Errorf("resolve image capability: %w", err)
		}
		if !imageCapable {
			return orchestrator.AgentInput{}, fmt.Errorf("selected model route does not declare image-input support")
		}
	}
	capabilities, err := h.resolveModelCapabilities(ctx, provider, model)
	if err != nil {
		return orchestrator.AgentInput{}, fmt.Errorf("resolve tool capability verification: %w", err)
	}
	// A manual proof can relax only the unknown tool gate. Rich input resolves
	// the proof again from the current exact route; its provider, model,
	// generation, and expiry are then checked at every final dispatch.
	toolCallingVerified := capabilities.ToolCalling == modelcaps.ToolCallingBasic &&
		capabilities.ToolVerification != nil &&
		capabilities.ToolVerification.Status == modelprobe.StatusSupported &&
		capabilities.ToolCallingVerificationApplied
	toolCallingVerifiedModel := ""
	toolCallingVerifiedUntil := time.Time{}
	if toolCallingVerified {
		toolCallingVerifiedModel = model
		toolCallingVerifiedUntil = capabilities.ToolVerification.ExpiresAt
	}
	if h.chatImageTurnAdmission == nil || !h.chatImageTurnAdmission.Acquire(ctx) {
		return orchestrator.AgentInput{}, fmt.Errorf("attachment input admission cancelled before execution")
	}
	release := h.chatImageTurnAdmission.Release
	releaseOnError := true
	defer func() {
		if releaseOnError {
			release()
		}
	}()
	// Waiting for hydration capacity must not keep a destructive close from
	// cancelling this run. Admit the counted operation only after that wait;
	// the earlier lifecycle snapshot rejects any intervening close or delete.
	releaseOperation, accepted := h.agentChatLive.beginLifecycleOperation(lifecycle)
	if !accepted {
		return orchestrator.AgentInput{}, chatapp.ErrNativeAttachmentUnavailable
	}
	defer releaseOperation()

	attachments := make([]chatattachments.StoredAttachment, 0, len(inputMessage.Attachments))
	textAttachments := make(map[string]chat.MessageAttachment)
	for _, metadata := range inputMessage.Attachments {
		if metadata.MediaType == "text/plain" {
			if metadata.ID == "" || metadata.SizeBytes <= 0 || metadata.SizeBytes > chatapp.MaxNativeTextAttachmentBytes {
				return orchestrator.AgentInput{}, chatapp.ErrNativeAttachmentUnavailable
			}
			if _, duplicate := textAttachments[metadata.ID]; duplicate {
				return orchestrator.AgentInput{}, chatapp.ErrNativeAttachmentUnavailable
			}
			textAttachments[metadata.ID] = metadata
			continue
		}
		attachment, err := h.chatApplication().GetAttachment(ctx, chatapp.AttachmentCommand{
			SessionID:    session.ID,
			AttachmentID: metadata.ID,
		})
		if err != nil {
			return orchestrator.AgentInput{}, fmt.Errorf("load attachment: %w", err)
		}
		if err := validateStoredChatAttachmentTranscript(session.ID, metadata, attachment); err != nil {
			return orchestrator.AgentInput{}, fmt.Errorf("attachment metadata mismatch")
		}
		attachments = append(attachments, attachment)
	}
	if _, err := validateStoredNativeChatAttachments(attachments); err != nil {
		return orchestrator.AgentInput{}, fmt.Errorf("stored chat attachment failed integrity validation")
	}

	reader := &chatAgentAttachmentReader{
		handler: h, ctx: ctx, lifecycle: lifecycle, taskID: task.ID, runID: run.ID, inputRef: inputRef,
		provider: provider, instance: providerInstance, metadata: textAttachments,
	}
	message := chatModelMessageWithAttachments(inputMessage.Content, attachments, nil)
	if len(textAttachments) > 0 {
		if len(message.ContentBlocks) == 0 && message.Content != "" {
			message.ContentBlocks = append(message.ContentBlocks, types.ContentBlock{Type: "text", Text: message.Content})
		}
		for _, metadata := range inputMessage.Attachments {
			if metadata.MediaType == "text/plain" {
				message.ContentBlocks = append(message.ContentBlocks, nativeAttachmentReferenceBlock(metadata))
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return orchestrator.AgentInput{}, err
	}
	releaseOnError, keepLifecycle = false, true
	var attachmentReader orchestrator.AgentAttachmentReader
	if len(textAttachments) > 0 {
		attachmentReader = reader
	}
	return orchestrator.AgentInput{
		Message:                message,
		AttachmentReader:       attachmentReader,
		AttachmentContextBytes: chatapp.NativeAttachmentToolContextBytes(capabilities.MaxContextTokens),
		Requirements: types.ChatRequestRequirements{
			ImageInput:               hasImages,
			ToolCallingVerified:      toolCallingVerified,
			ToolCallingVerifiedModel: toolCallingVerifiedModel,
			ToolCallingVerifiedUntil: toolCallingVerifiedUntil,
			NoProviderFailover:       true,
			ExactProvider:            provider != "",
			ProviderInstance:         providerInstance,
		},
		Release: reader.Release,
	}, nil
}
