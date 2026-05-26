package api

import (
	"fmt"
	"strings"

	"github.com/hecatehq/hecate/internal/chat"
)

const chatContextPacketVersion = "chat.context.v1"

func directModelContextPacket(session chat.Session, provider, model, systemPrompt string) chat.ContextPacket {
	packet := baseChatContextPacket(chat.ExecutionModeDirectModel, provider, model, session.Workspace)
	packet.SystemPromptIncluded = strings.TrimSpace(systemPrompt) != ""
	packet.MessageCount = chatTranscriptMessageCount(session.Messages) + 1
	if packet.SystemPromptIncluded {
		packet.Sources = append(packet.Sources, chat.ContextSource{
			Kind:   "system_prompt",
			Label:  "System prompt",
			Detail: "Configured for this direct model turn",
			Trust:  "system",
		})
	}
	packet.Sources = append(packet.Sources, transcriptContextSource(packet.MessageCount))
	return packet
}

func hecateTaskContextPacket(session chat.Session, provider, model, systemPrompt string, forceNewTask bool) chat.ContextPacket {
	packet := baseChatContextPacket(chat.ExecutionModeHecateTask, provider, model, session.Workspace)
	packet.SystemPromptIncluded = strings.TrimSpace(systemPrompt) != ""
	packet.MessageCount = chatTranscriptMessageCount(session.Messages) + 1
	if packet.SystemPromptIncluded {
		packet.Sources = append(packet.Sources, chat.ContextSource{
			Kind:   "system_prompt",
			Label:  "System prompt",
			Detail: "Stored on the backing task for this task segment",
			Trust:  "system",
		})
	}
	if strings.TrimSpace(session.Workspace) != "" {
		packet.Sources = append(packet.Sources, chat.ContextSource{
			Kind:   "workspace",
			Label:  "Workspace",
			Detail: session.Workspace,
			Trust:  "workspace",
		})
	}
	taskDetail := "Continuing the existing task-backed agent loop"
	if forceNewTask || strings.TrimSpace(session.TaskID) == "" {
		taskDetail = "Starting a new task-backed agent loop"
	}
	packet.Sources = append(packet.Sources,
		transcriptContextSource(packet.MessageCount),
		chat.ContextSource{
			Kind:   "task_runtime",
			Label:  "Hecate task runtime",
			Detail: taskDetail,
			Trust:  "runtime",
		},
	)
	return packet
}

func externalAgentContextPacket(session chat.Session, adapterName string) chat.ContextPacket {
	packet := baseChatContextPacket(chat.ExecutionModeExternalAgent, "", "", session.Workspace)
	packet.MessageCount = chatTranscriptMessageCount(session.Messages) + 1
	if strings.TrimSpace(session.Workspace) != "" {
		packet.Sources = append(packet.Sources, chat.ContextSource{
			Kind:   "workspace",
			Label:  "Workspace",
			Detail: session.Workspace,
			Trust:  "workspace",
		})
	}
	if strings.TrimSpace(adapterName) == "" {
		adapterName = "External agent"
	}
	packet.Sources = append(packet.Sources,
		transcriptContextSource(packet.MessageCount),
		chat.ContextSource{
			Kind:   "adapter_session",
			Label:  adapterName + " ACP session",
			Detail: "The adapter owns model packing inside its native session",
			Trust:  "adapter",
		},
	)
	return packet
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
