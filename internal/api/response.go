package api

import (
	"encoding/json"
	"net/http"
	"reflect"
)

const (
	errCodeUnauthorized            = "unauthorized"
	errCodeInvalidRequest          = "invalid_request"
	errCodeProjectSetupNoInputs    = "project_setup_no_inputs"
	errCodeForbidden               = "forbidden"
	errCodeGatewayError            = "gateway_error"
	errCodeInternalError           = "internal_error"
	errCodeUpstreamError           = "upstream_error"
	errCodeNotFound                = "not_found"
	errCodeConflict                = "conflict"
	errCodeRateLimitExceeded       = "rate_limit_exceeded"
	errCodeSessionLimitExceeded    = "chat.session_limit_exceeded"
	errCodeSessionDurationLimit    = "chat.session_duration_limit_exceeded"
	errCodeSessionIdleTimeout      = "chat.session_idle_timeout"
	errCodeAgentSessionBusy        = "chat.agent_session_busy"
	errCodeModelCapability         = "chat.model_capability_required"
	errCodeModelNotConfigured      = "model_not_configured"
	errCodeWorkspaceRequired       = "chat.workspace_required"
	errCodeModelRequired           = "chat.model_required"
	errCodeAgentIDInvalid          = "chat.agent_id_invalid"
	errCodeExecutionModeInvalid    = "chat.execution_mode_invalid"
	errCodeRuntimeMismatch         = "chat.runtime_mismatch"
	errCodeAgentAdapterNotFound    = "chat.adapter_not_found"
	errCodeAgentAdapterUnavailable = "chat.adapter_unavailable"
	errCodeSessionStopping         = "chat.session_stopping"
	errCodeSessionNotRunning       = "chat.session_not_running"
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
	Fields         map[string]any
}

func WriteErrorDetails(w http.ResponseWriter, status int, code, message string, details ErrorDetails) {
	details = enrichErrorDetails(code, details)
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
	for key, value := range details.Fields {
		if key == "" || !isSafeErrorField(value) || isReservedErrorField(key) {
			continue
		}
		errorObject[key] = value
	}
	WriteJSON(w, status, map[string]any{"error": errorObject})
}

func isReservedErrorField(key string) bool {
	switch key {
	case "type", "message", "user_message", "operator_action", "request_id", "trace_id":
		return true
	default:
		return false
	}
}

func isNilErrorField(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func isSafeErrorField(value any) bool {
	if isNilErrorField(value) {
		return false
	}
	_, err := json.Marshal(value)
	return err == nil
}

func enrichErrorDetails(code string, details ErrorDetails) ErrorDetails {
	if details.UserMessage == "" {
		details.UserMessage = defaultErrorUserMessage(code)
	}
	if details.OperatorAction == "" {
		details.OperatorAction = defaultErrorAction(code)
	}
	return details
}

func defaultErrorUserMessage(code string) string {
	switch code {
	case errCodeInvalidRequest:
		return "The request is invalid."
	case errCodeProjectSetupNoInputs:
		return "Project setup found no guidance or skills to apply."
	case errCodeForbidden:
		return "The request was blocked."
	case errCodeUnauthorized:
		return "Authentication is required."
	case errCodeGatewayError:
		return gatewayErrorUserMessage(code)
	case errCodeProviderAuthFailed, errCodeProviderRateLimited,
		errCodeProviderUnavailable, errCodeRouteImpossible, errCodeUnsupportedModel, errCodeRateLimitExceeded:
		return gatewayErrorUserMessage(code)
	case errCodeNotFound:
		return "The requested resource was not found."
	case errCodeConflict:
		return "The requested change conflicts with the current state."
	case errCodeSessionLimitExceeded:
		return "This chat has reached its turn limit."
	case errCodeSessionDurationLimit:
		return "This chat has reached its wall-clock limit."
	case errCodeSessionIdleTimeout:
		return "This chat session expired after being idle."
	case errCodeAgentSessionBusy:
		return "Hecate Chat is still working on this task."
	case errCodeModelCapability:
		return "This model is not marked as tool-capable."
	case errCodeModelNotConfigured:
		return "The selected model is not available from the selected provider."
	case errCodeWorkspaceRequired:
		return "Choose a workspace before starting this chat mode."
	case errCodeModelRequired:
		return "Choose a model before sending this message."
	case errCodeAgentIDInvalid, errCodeExecutionModeInvalid:
		return "This chat mode is not supported by the current API."
	case errCodeRuntimeMismatch:
		return "This message belongs to a different chat runtime."
	case errCodeAgentAdapterNotFound:
		return "The selected external agent is not configured."
	case errCodeSessionStopping:
		return "This chat is still stopping."
	case errCodeSessionNotRunning:
		return "There is no active run to stop."
	default:
		return ""
	}
}

func defaultErrorAction(code string) string {
	switch code {
	case errCodeInvalidRequest:
		return "Check the request body and retry."
	case errCodeProjectSetupNoInputs:
		return "Create the first work item, or add setup inputs and retry."
	case errCodeForbidden:
		return "Review policy, same-origin, or permission settings before retrying."
	case errCodeUnauthorized:
		return "Provide valid credentials and retry."
	case errCodeGatewayError:
		return gatewayErrorAction(code)
	case errCodeProviderAuthFailed, errCodeProviderRateLimited,
		errCodeProviderUnavailable, errCodeRouteImpossible, errCodeUnsupportedModel, errCodeRateLimitExceeded:
		return gatewayErrorAction(code)
	case errCodeNotFound:
		return "Refresh the view or verify the resource id before retrying."
	case errCodeConflict:
		return "Refresh the resource, resolve the active state, then retry."
	case errCodeSessionLimitExceeded:
		return "Start a new chat session to continue."
	case errCodeSessionDurationLimit:
		return "Start a new chat session to continue."
	case errCodeSessionIdleTimeout:
		return "Start a new chat session to continue."
	case errCodeAgentSessionBusy:
		return "Open the backing task, resolve the pending approval, or stop the run before sending another message."
	case errCodeModelCapability:
		return "Send as direct model chat, or choose a model that reports tool-calling support."
	case errCodeModelNotConfigured:
		return "Choose a discovered model, refresh provider status, or open Connections to fix model discovery."
	case errCodeWorkspaceRequired:
		return "Use the workspace picker in Chats. Task-backed Hecate Chat and External Agent sessions need a real workspace path."
	case errCodeModelRequired:
		return "Use the model picker in the chat header, or add a provider that reports at least one model."
	case errCodeAgentIDInvalid, errCodeExecutionModeInvalid:
		return "Use agent_id hecate or a registered external agent id. For execution_mode, use hecate_task or external_agent."
	case errCodeRuntimeMismatch:
		return "Start a new chat or switch back to the runtime that created this session."
	case errCodeAgentAdapterNotFound:
		return "Open Connections and test the external agent adapter, or choose another agent."
	case errCodeSessionStopping:
		return "Wait a moment, then retry the action."
	case errCodeSessionNotRunning:
		return "Send a new message if you want to start another run."
	default:
		return ""
	}
}
