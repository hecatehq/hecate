package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/hecate/agent-runtime/internal/agentchat"
)

func writeAgentChatWorkspaceRequired(w http.ResponseWriter, runtimeKind string) {
	WriteErrorDetails(w, http.StatusBadRequest, errCodeWorkspaceRequired, fmt.Sprintf("workspace is required for %s chat", agentChatRuntimeLabel(runtimeKind)), ErrorDetails{
		UserMessage:    "Choose a workspace before starting this chat mode.",
		OperatorAction: "Use the workspace picker in Chats. Hecate Agent and External Agent sessions need a real workspace path.",
	})
}

func writeAgentChatModelRequired(w http.ResponseWriter, runtimeKind string) {
	WriteErrorDetails(w, http.StatusBadRequest, errCodeModelRequired, fmt.Sprintf("model is required for %s chat", agentChatRuntimeLabel(runtimeKind)), ErrorDetails{
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
	if strings.Contains(message, "not available") || strings.Contains(message, "not configured") {
		WriteErrorDetails(w, http.StatusUnprocessableEntity, errCodeModelNotConfigured, message, ErrorDetails{
			UserMessage:    "The selected model is not available from the selected provider.",
			OperatorAction: "Choose a discovered model, refresh provider status, or open Providers to fix model discovery.",
		})
		return
	}
	WriteError(w, http.StatusInternalServerError, errCodeGatewayError, message)
}

func writeAgentChatRuntimeKindInvalid(w http.ResponseWriter) {
	WriteErrorDetails(w, http.StatusBadRequest, errCodeRuntimeKindInvalid, "runtime_kind must be model, agent, or external_agent", ErrorDetails{
		UserMessage:    "This chat mode is not supported by the current API.",
		OperatorAction: "Use one of: model, agent, or external_agent.",
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
		OperatorAction: "Open Settings and test the external agent adapter, or choose another agent.",
	})
}

func writeAgentChatSessionStopping(w http.ResponseWriter) {
	WriteErrorDetails(w, http.StatusConflict, errCodeSessionStopping, "agent chat session is still stopping", ErrorDetails{
		UserMessage:    "This chat is still stopping.",
		OperatorAction: "Wait a moment, then retry the action.",
	})
}

func writeAgentChatSessionNotRunning(w http.ResponseWriter) {
	WriteErrorDetails(w, http.StatusConflict, errCodeSessionNotRunning, "agent chat session is not running", ErrorDetails{
		UserMessage:    "There is no active run to stop.",
		OperatorAction: "Send a new message if you want to start another run.",
	})
}

func writeHecateAgentBusy(w http.ResponseWriter, session agentchat.Session, runStatus string) {
	WriteJSON(w, http.StatusConflict, map[string]any{
		"error": map[string]any{
			"type":            errCodeAgentSessionBusy,
			"message":         "Hecate Chat is still working on the current task. Wait for it to finish, resolve the approval, or stop it before sending another message.",
			"user_message":    "Hecate Chat is still working on this task.",
			"operator_action": "Open the backing task, resolve the pending approval, or stop the run before sending another message.",
			"task_id":         session.TaskID,
			"latest_run_id":   session.LatestRunID,
			"run_status":      runStatus,
		},
	})
}

func agentChatRuntimeLabel(runtimeKind string) string {
	switch runtimeKind {
	case "agent":
		return "Hecate Agent"
	case "external_agent":
		return "External Agent"
	default:
		return "model"
	}
}
