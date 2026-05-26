package api

import (
	"fmt"
	"strings"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/pkg/types"
)

const chatContextPacketVersion = "chat.context.v1"

func directModelContextPacket(session chat.Session, provider, model, systemPrompt string, history []types.Message) chat.ContextPacket {
	packet := baseChatContextPacket(chat.ExecutionModeDirectModel, provider, model, session.Workspace)
	packet.SystemPromptIncluded = strings.TrimSpace(systemPrompt) != ""
	packet.MessageCount = chatHistoryMessageCount(history)
	if packet.SystemPromptIncluded {
		packet.Sources = append(packet.Sources, chat.ContextSource{
			Kind:     "system_prompt",
			Label:    "System prompt",
			Detail:   "Configured for this direct model turn",
			Trust:    "system",
			Included: true,
		})
	}
	packet.Sources = append(packet.Sources, transcriptContextSource(packet.MessageCount))
	return packet
}

func hecateTaskContextPacket(session chat.Session, provider, model, systemPrompt string, messageCount int, forceNewTask bool) chat.ContextPacket {
	packet := baseChatContextPacket(chat.ExecutionModeHecateTask, provider, model, session.Workspace)
	packet.SystemPromptIncluded = strings.TrimSpace(systemPrompt) != ""
	packet.MessageCount = messageCount
	if packet.SystemPromptIncluded {
		packet.Sources = append(packet.Sources, chat.ContextSource{
			Kind:     "system_prompt",
			Label:    "System prompt",
			Detail:   "Stored on the backing task for this task segment",
			Trust:    "system",
			Included: true,
		})
	}
	if strings.TrimSpace(session.Workspace) != "" {
		packet.Sources = append(packet.Sources, chat.ContextSource{
			Kind:     "workspace",
			Label:    "Workspace",
			Detail:   session.Workspace,
			Trust:    "workspace",
			Included: true,
		})
	}
	taskDetail := "Continuing the existing task-backed agent loop"
	if forceNewTask || strings.TrimSpace(session.TaskID) == "" {
		taskDetail = "Starting a new task-backed agent loop"
	}
	packet.Sources = append(packet.Sources,
		transcriptContextSource(messageCount),
		chat.ContextSource{
			Kind:     "task_runtime",
			Label:    "Hecate task runtime",
			Detail:   taskDetail,
			Trust:    "runtime",
			Included: true,
		},
	)
	return packet
}

func externalAgentContextPacket(session chat.Session, adapterName string) chat.ContextPacket {
	packet := baseChatContextPacket(chat.ExecutionModeExternalAgent, "", "", session.Workspace)
	packet.MessageCount = terminalChatMessageCount(session.Messages) + 1
	if strings.TrimSpace(session.Workspace) != "" {
		packet.Sources = append(packet.Sources, chat.ContextSource{
			Kind:     "workspace",
			Label:    "Workspace",
			Detail:   session.Workspace,
			Trust:    "workspace",
			Included: true,
		})
	}
	if strings.TrimSpace(adapterName) == "" {
		adapterName = "External agent"
	}
	packet.Sources = append(packet.Sources,
		transcriptContextSource(packet.MessageCount),
		chat.ContextSource{
			Kind:     "adapter_session",
			Label:    adapterName + " ACP session",
			Detail:   "The adapter owns model packing inside its native session",
			Trust:    "adapter",
			Included: true,
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
		Kind:     "transcript",
		Label:    "Chat transcript",
		Detail:   detail,
		Trust:    "operator",
		Included: true,
	}
}

func chatHistoryMessageCount(history []types.Message) int {
	count := 0
	for _, message := range history {
		if message.Role == "system" {
			continue
		}
		if strings.TrimSpace(message.Content) == "" {
			continue
		}
		count++
	}
	return count
}

func terminalChatMessageCount(messages []chat.Message) int {
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
