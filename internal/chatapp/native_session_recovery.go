package chatapp

import (
	"encoding/json"
	"strings"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/chat"
)

// AuthorizeNativeSessionReplacement derives one-turn replacement authority
// from the persisted pre-turn transcript. Any evidence that an agent response
// or tool completed fails closed because replacing native state could discard
// context or repeat side effects.
func (app *Application) AuthorizeNativeSessionReplacement(session chat.Session) bool {
	if strings.TrimSpace(session.NativeSessionID) == "" || !session.ContextSummary.Empty() {
		return false
	}
	for _, message := range session.Messages {
		switch strings.TrimSpace(message.Role) {
		case "user":
			continue
		case "assistant":
		default:
			return false
		}
		status := strings.TrimSpace(message.Status)
		switch status {
		case "failed":
			content := strings.TrimSpace(message.Content)
			errorText := strings.TrimSpace(message.Error)
			if errorText == "" || content != errorText {
				return false
			}
		case "cancelled":
			if strings.TrimSpace(message.Content) != "" {
				return false
			}
		default:
			return false
		}
		if strings.TrimSpace(message.DiffStat) != "" || strings.TrimSpace(message.Diff) != "" {
			return false
		}
		if !nativeSessionFailureEvidenceSafe(message) {
			return false
		}
	}
	return true
}

func nativeSessionFailureEvidenceSafe(message chat.Message) bool {
	raw := strings.TrimSpace(message.RawOutput)
	status := strings.TrimSpace(message.Status)
	rawKind := ""
	switch status {
	case "cancelled":
		if raw != "" && raw != "context canceled" {
			return false
		}
		rawKind = "cancelled"
	case "failed":
		switch {
		case agentadapters.IsFailedPromptCommandLifecycleRaw(raw):
			rawKind = "prompt_command"
		case agentadapters.IsPrivateACPRawOutputWithheld(raw):
			rawKind = "withheld"
		case isKnownProcessCommandNotFoundRaw(raw):
			rawKind = "process_not_found"
		default:
			return false
		}
	default:
		return false
	}

	toolCalls := 0
	for _, activity := range message.Activities {
		activityType := strings.TrimSpace(activity.Type)
		activityStatus := strings.TrimSpace(activity.Status)
		switch activityType {
		case "running":
			if activityStatus != "running" {
				return false
			}
		case "failed":
			if activityStatus != "failed" {
				return false
			}
		case "cancelled":
			if activityStatus != "cancelled" {
				return false
			}
		case "started", "resumed", "recovered", "session_recovery":
			if activityStatus != "completed" {
				return false
			}
		case "tool_call":
			toolCalls++
			if rawKind == "cancelled" || toolCalls > 1 ||
				!strings.HasPrefix(strings.TrimSpace(activity.ID), "tool:prompt-command-") ||
				strings.TrimSpace(activity.Kind) != "execute" {
				return false
			}
			switch activityStatus {
			case "pending", "running", "failed":
			default:
				return false
			}
			if rawKind == "withheld" && activityStatus != "failed" {
				return false
			}
		default:
			return false
		}
	}
	return rawKind != "withheld" || toolCalls == 1
}

func isKnownProcessCommandNotFoundRaw(raw string) bool {
	var rpcError struct {
		Code    *int   `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Error string `json:"error"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(raw), &rpcError) != nil || rpcError.Code == nil ||
		*rpcError.Code != -32000 || strings.TrimSpace(rpcError.Message) != "prompt command failed" {
		return false
	}
	const prefix = "process command not found: "
	errorText := strings.TrimSpace(rpcError.Data.Error)
	if !strings.HasPrefix(errorText, prefix) {
		return false
	}
	command := strings.TrimPrefix(errorText, prefix)
	if command == "" || len(command) > 128 {
		return false
	}
	for _, char := range command {
		switch {
		case char >= 'a' && char <= 'z':
		case char >= 'A' && char <= 'Z':
		case char >= '0' && char <= '9':
		case strings.ContainsRune("-._+", char):
		default:
			return false
		}
	}
	return true
}
