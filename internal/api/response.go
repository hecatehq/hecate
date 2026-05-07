package api

import (
	"encoding/json"
	"net/http"
)

const (
	errCodeUnauthorized         = "unauthorized"
	errCodeInvalidRequest       = "invalid_request"
	errCodeForbidden            = "forbidden"
	errCodeGatewayError         = "gateway_error"
	errCodeUpstreamError        = "upstream_error"
	errCodeNotFound             = "not_found"
	errCodeConflict             = "conflict"
	errCodeSessionLimitExceeded = "agent_chat.session_limit_exceeded"
	errCodeSessionDurationLimit = "agent_chat.session_duration_limit_exceeded"
	errCodeSessionIdleTimeout   = "agent_chat.session_idle_timeout"
	errCodeAgentSessionBusy     = "agent_chat.agent_session_busy"
	errCodeModelCapability      = "agent_chat.model_capability_required"
	errCodeModelNotConfigured   = "model_not_configured"
	errCodeWorkspaceRequired    = "agent_chat.workspace_required"
	errCodeModelRequired        = "agent_chat.model_required"
	errCodeRuntimeKindInvalid   = "agent_chat.runtime_kind_invalid"
	errCodeRuntimeMismatch      = "agent_chat.runtime_mismatch"
	errCodeAgentAdapterNotFound = "agent_chat.adapter_not_found"
	errCodeSessionStopping      = "agent_chat.session_stopping"
	errCodeSessionNotRunning    = "agent_chat.session_not_running"
)

func WriteJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteErrorDetails(w, status, code, message, ErrorDetails{})
}

type ErrorDetails struct {
	UserMessage    string
	OperatorAction string
	RequestID      string
	TraceID        string
}

func WriteErrorDetails(w http.ResponseWriter, status int, code, message string, details ErrorDetails) {
	errorObject := map[string]any{
		"type":    code,
		"message": message,
	}
	if details.UserMessage != "" {
		errorObject["user_message"] = details.UserMessage
	}
	if details.OperatorAction != "" {
		errorObject["operator_action"] = details.OperatorAction
	}
	if details.RequestID != "" {
		errorObject["request_id"] = details.RequestID
	}
	if details.TraceID != "" {
		errorObject["trace_id"] = details.TraceID
	}
	WriteJSON(w, status, map[string]any{"error": errorObject})
}
