package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/modelapp"
	"github.com/hecatehq/hecate/pkg/types"
)

func writeAgentChatWorkspaceRequired(w http.ResponseWriter, mode string) {
	WriteErrorDetails(w, http.StatusBadRequest, errCodeWorkspaceRequired, fmt.Sprintf("workspace is required for %s chat", chatExecutionModeLabel(mode)), ErrorDetails{
		UserMessage:    "Choose a workspace before starting this chat mode.",
		OperatorAction: "Use the workspace picker in Chats. Task-backed Hecate Chat and External Agent sessions need a real workspace path.",
	})
}

func writeAgentChatModelRequired(w http.ResponseWriter, mode string) {
	WriteErrorDetails(w, http.StatusBadRequest, errCodeModelRequired, fmt.Sprintf("model is required for %s chat", chatExecutionModeLabel(mode)), ErrorDetails{
		UserMessage:    "Choose a model before sending this message.",
		OperatorAction: "Use the model picker in the chat header, or add a provider that reports at least one model.",
	})
}

func writeAgentChatModelResolutionError(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	message := err.Error()
	if strings.Contains(message, "model is required") {
		writeAgentChatModelRequired(w, "model")
		return
	}
	if errors.Is(err, modelapp.ErrProviderAmbiguous) {
		details := ErrorDetails{
			UserMessage:    "The selected provider alias matches multiple configured providers.",
			OperatorAction: "Choose the provider by its canonical runtime name, or remove the conflicting alias in Connections.",
		}
		var ambiguityErr modelapp.ProviderAmbiguityError
		if errors.As(err, &ambiguityErr) {
			details.Fields = map[string]any{"provider": ambiguityErr.Provider}
		}
		WriteErrorDetails(w, http.StatusUnprocessableEntity, errCodeProviderAmbiguous, message, details)
		return
	}
	if strings.Contains(message, "not available") || strings.Contains(message, "not configured") {
		details := ErrorDetails{
			UserMessage:    "The selected model is not available from the selected provider.",
			OperatorAction: "Choose a discovered model, refresh provider status, or open Connections to fix model discovery.",
		}
		var readinessErr modelapp.ReadinessError
		if errors.As(err, &readinessErr) {
			details = modelReadinessErrorDetails(readinessErr.Readiness)
		}
		WriteErrorDetails(w, http.StatusUnprocessableEntity, errCodeModelNotConfigured, message, ErrorDetails{
			UserMessage:    details.UserMessage,
			OperatorAction: details.OperatorAction,
			Fields:         details.Fields,
		})
		return
	}
	WriteError(w, http.StatusInternalServerError, errCodeGatewayError, message)
}

func modelReadinessErrorDetails(readiness types.ModelReadiness) ErrorDetails {
	details := ErrorDetails{
		UserMessage:    "The selected model is not ready for this chat.",
		OperatorAction: readiness.OperatorAction,
		Fields: map[string]any{
			"provider":                readiness.Provider,
			"matched_provider":        readiness.MatchedProvider,
			"model":                   readiness.Model,
			"reason":                  readiness.Reason,
			"routing_ready":           readiness.RoutingReady,
			"provider_status":         readiness.ProviderStatus,
			"provider_blocked_reason": readiness.ProviderBlockedReason,
			"suggested_models":        readiness.SuggestedModels,
		},
	}
	if readiness.Message != "" {
		details.UserMessage = readiness.Message
	}
	if details.OperatorAction == "" {
		details.OperatorAction = "Choose a discovered model, refresh provider status, or open Connections to fix model discovery."
	}
	return details
}

func writeChatAgentIDInvalid(w http.ResponseWriter) {
	WriteErrorDetails(w, http.StatusBadRequest, errCodeAgentIDInvalid, "agent_id must be hecate or a configured external agent id", ErrorDetails{
		UserMessage:    "This chat agent is not supported by the current API.",
		OperatorAction: "Use hecate, codex, claude_code, or cursor_agent.",
	})
}

func writeChatExecutionModeInvalid(w http.ResponseWriter) {
	WriteErrorDetails(w, http.StatusBadRequest, errCodeExecutionModeInvalid, "execution_mode must be hecate_task or external_agent", ErrorDetails{
		UserMessage:    "This chat mode is not supported by the current API.",
		OperatorAction: "Use one of: hecate_task or external_agent.",
	})
}

func writeAgentChatRuntimeMismatch(w http.ResponseWriter, message string) {
	WriteErrorDetails(w, http.StatusBadRequest, errCodeRuntimeMismatch, message, ErrorDetails{
		UserMessage:    "This message belongs to a different chat runtime.",
		OperatorAction: "Start a new chat or switch back to the runtime that created this session.",
	})
}

func writeAgentChatAdapterNotFound(w http.ResponseWriter, adapterID string) {
	WriteErrorDetails(w, http.StatusBadRequest, errCodeAgentAdapterNotFound, fmt.Sprintf("agent adapter %q not found", adapterID), ErrorDetails{
		UserMessage:    "The selected external agent is not configured.",
		OperatorAction: "Refresh agent discovery in Connections, install the selected app if needed, or choose another agent.",
	})
}

func writeChatSessionStopping(w http.ResponseWriter) {
	WriteErrorDetails(w, http.StatusConflict, errCodeSessionStopping, "agent chat session is still stopping", ErrorDetails{
		UserMessage:    "This chat is still stopping.",
		OperatorAction: "Wait a moment, then retry the action.",
	})
}

func writeChatSessionNotRunning(w http.ResponseWriter) {
	WriteErrorDetails(w, http.StatusConflict, errCodeSessionNotRunning, "agent chat session is not running", ErrorDetails{
		UserMessage:    "There is no active turn to stop.",
		OperatorAction: "Send a new message if you want to start another turn.",
	})
}

func writeHecateAgentBusy(w http.ResponseWriter, session chat.Session, runStatus string) {
	WriteErrorDetails(w, http.StatusConflict, errCodeAgentSessionBusy, "Hecate Chat is still working on the current task. Wait for it to finish, resolve the approval, or stop it before sending another message.", ErrorDetails{
		Fields: map[string]any{
			"task_id":       session.TaskID,
			"latest_run_id": session.LatestRunID,
			"run_status":    runStatus,
		},
	})
}

func chatExecutionModeLabel(mode string) string {
	switch mode {
	case chat.ExecutionModeHecateTask:
		return "task-backed Hecate Chat"
	case chat.ExecutionModeExternalAgent:
		return "External Agent"
	default:
		return "model"
	}
}
