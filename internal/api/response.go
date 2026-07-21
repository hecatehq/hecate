package api

import (
	"encoding/json"
	"net/http"
	"reflect"
)

const (
	errCodeUnauthorized              = "unauthorized"
	errCodeInvalidRequest            = "invalid_request"
	errCodeProjectSetupNoInputs      = "project_setup_no_inputs"
	errCodeForbidden                 = "forbidden"
	errCodeGatewayError              = "gateway_error"
	errCodeInternalError             = "internal_error"
	errCodeUpstreamError             = "upstream_error"
	errCodeNotFound                  = "not_found"
	errCodeConflict                  = "conflict"
	errCodeRateLimitExceeded         = "rate_limit_exceeded"
	errCodeRequestTooLarge           = "request_too_large"
	errCodeRequestBodyTimeout        = "request_body_timeout"
	errCodeSessionLimitExceeded      = "chat.session_limit_exceeded"
	errCodeSessionDurationLimit      = "chat.session_duration_limit_exceeded"
	errCodeSessionIdleTimeout        = "chat.session_idle_timeout"
	errCodeAgentSessionBusy          = "chat.agent_session_busy"
	errCodeSessionCreateConflict     = "chat.session_create_conflict"
	errCodeClientRequestConflict     = "chat.client_request_conflict"
	errCodeModelCapability           = "chat.model_capability_required"
	errCodeModelToolProbeUnavailable = "model_tool_probe_unavailable"
	errCodeModelNotConfigured        = "model_not_configured"
	errCodeProviderAmbiguous         = "provider_ambiguous"
	errCodeWorkspaceRequired         = "chat.workspace_required"
	errCodeModelRequired             = "chat.model_required"
	errCodeAgentIDInvalid            = "chat.agent_id_invalid"
	errCodeExecutionModeInvalid      = "chat.execution_mode_invalid"
	errCodeRuntimeMismatch           = "chat.runtime_mismatch"
	errCodeAgentAdapterNotFound      = "chat.adapter_not_found"
	errCodeAgentAdapterUnavailable   = "chat.adapter_unavailable"
	errCodeSessionStopping           = "chat.session_stopping"
	errCodeSessionNotRunning         = "chat.session_not_running"
	errCodeAttachmentInvalid         = "chat.attachment_invalid"
	errCodeAttachmentTooLarge        = "chat.attachment_too_large"
	errCodeAttachmentNotFound        = "chat.attachment_not_found"
	errCodeAttachmentUnsupported     = "chat.attachment_unsupported"
	errCodeAttachmentInUse           = "chat.attachment_in_use"
	errCodeAttachmentDraftQuota      = "chat.attachment_draft_quota_exceeded"
	errCodeAttachmentSessionQuota    = "chat.attachment_session_quota_exceeded"
	errCodeAttachmentTotalQuota      = "chat.attachment_total_quota_exceeded"
	errCodeAttachmentUploadBusy      = "chat.attachment_upload_busy"
	errCodeAttachmentUploadTimeout   = "chat.attachment_upload_timeout"
	errCodeAttachmentContentBusy     = "chat.attachment_content_busy"
	errCodeImageCapability           = "chat.image_capability_required"
	errCodeImageTurnBusy             = "chat.image_turn_busy"
	errCodeExternalFileTurnBusy      = "chat.external_file_turn_busy"
	errCodeDictationInvalid          = "dictation.invalid"
	errCodeDictationTooLarge         = "dictation.too_large"
	errCodeDictationUnsupported      = "dictation.unsupported_media"
	errCodeDictationBusy             = "dictation.busy"
	errCodeDictationBodyTimeout      = "dictation.body_timeout"
	errCodeDictationUnavailable      = "dictation.unavailable"
	errCodeDictationRouteUnavailable = "dictation.route_unavailable"
	errCodeDictationRouteChanged     = "dictation.route_changed"
	errCodeDictationTimeout          = "dictation.timeout"
	errCodeDictationUpstream         = "dictation.upstream_failure"
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
	case errCodeRequestTooLarge:
		return "The inference request is too large."
	case errCodeRequestBodyTimeout:
		return "The inference request took too long to upload."
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
	case errCodeSessionCreateConflict:
		return "Chat creation overlapped project deletion."
	case errCodeClientRequestConflict:
		return "This queued chat request id is already bound to a different message."
	case errCodeSessionLimitExceeded:
		return "This chat has reached its turn limit."
	case errCodeSessionDurationLimit:
		return "This chat has reached its wall-clock limit."
	case errCodeSessionIdleTimeout:
		return "This Chat expired after being idle."
	case errCodeAgentSessionBusy:
		return "Hecate Chat is still working on this task."
	case errCodeModelCapability:
		return "This model is not marked as tool-capable."
	case errCodeImageCapability:
		return "This model is not marked as image-capable."
	case errCodeImageTurnBusy:
		return "Image processing is busy."
	case errCodeAttachmentInvalid:
		return "The attachment is invalid."
	case errCodeAttachmentTooLarge:
		return "The attachment is too large."
	case errCodeAttachmentNotFound:
		return "The attachment was not found in this chat."
	case errCodeAttachmentUnsupported:
		return "Attachments are unavailable for this format or chat mode."
	case errCodeAttachmentInUse:
		return "This attachment is already part of the chat transcript."
	case errCodeAttachmentDraftQuota:
		return "This chat has too many unlinked attachment drafts."
	case errCodeAttachmentSessionQuota:
		return "This chat has reached its retained attachment storage limit."
	case errCodeAttachmentTotalQuota:
		return "Hecate has reached its retained attachment storage limit."
	case errCodeAttachmentUploadBusy:
		return "Attachment upload validation is busy."
	case errCodeAttachmentUploadTimeout:
		return "The attachment upload took too long to read."
	case errCodeAttachmentContentBusy:
		return "Attachment downloads are busy."
	case errCodeModelNotConfigured:
		return "The selected model is not available from the selected provider."
	case errCodeProviderAmbiguous:
		return "The selected provider alias matches multiple configured providers."
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
		return "There is no active turn to stop."
	case errCodeDictationInvalid:
		return "The dictation request is invalid."
	case errCodeDictationTooLarge:
		return "The dictation recording is too large."
	case errCodeDictationUnsupported:
		return "This audio format is not supported for dictation."
	case errCodeDictationBusy:
		return "Dictation is busy."
	case errCodeDictationBodyTimeout:
		return "The dictation recording took too long to upload."
	case errCodeDictationUnavailable:
		return "Dictation is not configured."
	case errCodeDictationRouteUnavailable:
		return "The selected dictation provider is unavailable."
	case errCodeDictationRouteChanged:
		return "The selected dictation provider changed before transcription started."
	case errCodeDictationTimeout:
		return "The dictation provider took too long to respond."
	case errCodeDictationUpstream:
		return "The dictation provider could not transcribe this recording."
	default:
		return ""
	}
}

func defaultErrorAction(code string) string {
	switch code {
	case errCodeInvalidRequest:
		return "Check the request body and retry."
	case errCodeRequestTooLarge:
		return "Reduce the encoded request body to 32 MiB or less and retry."
	case errCodeRequestBodyTimeout:
		return "Retry on a stable connection that can upload the request within 60 seconds."
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
	case errCodeSessionCreateConflict:
		return "Refresh projects, project work, and chats, then explicitly retry the operation."
	case errCodeClientRequestConflict:
		return "Review the authoritative transcript. Do not retry a changed payload with the same key; use a new client_request_id only if another turn is still needed."
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
	case errCodeImageCapability:
		return "Choose a model that reports image-input support, or remove the images."
	case errCodeImageTurnBusy:
		return "Wait briefly, then retry the message."
	case errCodeAttachmentInvalid:
		return "Choose a valid file; Hecate direct-model chats accept PNG, JPEG, or WebP images."
	case errCodeAttachmentTooLarge:
		return "Choose a file no larger than 5 MiB and retry."
	case errCodeAttachmentNotFound:
		return "Remove the stale attachment draft or upload it to this chat again."
	case errCodeAttachmentUnsupported:
		return "Use an External Agent chat for files, or Hecate Chat with Tools off for a supported PNG, JPEG, or WebP image."
	case errCodeAttachmentInUse:
		return "Keep the transcript attachment, or delete the owning chat session."
	case errCodeAttachmentDraftQuota:
		return "Remove unused drafts and retry. Drafts older than 24 hours are reclaimed when a later upload runs, or delete the chat to remove them immediately."
	case errCodeAttachmentSessionQuota:
		return "Delete this chat to remove its retained attachments, or start a new chat."
	case errCodeAttachmentTotalQuota:
		return "Delete chats with retained attachments before uploading another file."
	case errCodeAttachmentUploadBusy:
		return "Wait briefly, then retry the attachment upload."
	case errCodeAttachmentUploadTimeout:
		return "Retry the attachment upload on a stable connection."
	case errCodeAttachmentContentBusy:
		return "Wait briefly, then retry loading the attachment."
	case errCodeModelNotConfigured:
		return "Choose a discovered model, refresh provider status, or open Connections to fix model discovery."
	case errCodeProviderAmbiguous:
		return "Choose the provider by its canonical runtime name, or remove the conflicting alias in Connections."
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
		return "Send a new message if you want to start another turn."
	case errCodeDictationInvalid:
		return "Record a new clip, select a configured provider, and retry."
	case errCodeDictationTooLarge:
		return "Record a shorter clip under 10 MiB and retry."
	case errCodeDictationUnsupported:
		return "Record in WebM, Ogg, M4A/MP4, MP3, or WAV format and retry."
	case errCodeDictationBusy:
		return "Wait briefly, then retry the recording."
	case errCodeDictationBodyTimeout:
		return "Retry on a stable connection that can upload the recording within 60 seconds."
	case errCodeDictationUnavailable:
		return "Configure an OpenAI, Groq, or LocalAI transcription route in Connections."
	case errCodeDictationRouteUnavailable:
		return "Refresh the dictation providers, choose an available route, and retry."
	case errCodeDictationRouteChanged:
		return "Review the selected provider and retry so Hecate can re-confirm the audio destination."
	case errCodeDictationTimeout:
		return "Retry, or choose another configured transcription provider."
	case errCodeDictationUpstream:
		return "Check the selected provider connection, then retry or choose another provider."
	default:
		return ""
	}
}
