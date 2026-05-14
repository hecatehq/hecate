package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/hecate/agent-runtime/internal/gateway"
	"github.com/hecate/agent-runtime/internal/providers"
)

const (
	errCodeProviderAuthFailed  = "provider_auth_failed"
	errCodeProviderRateLimited = "provider_rate_limited"
	errCodeProviderUnavailable = "provider_unavailable"
	errCodeRouteImpossible     = "route_impossible"
	errCodeUnsupportedModel    = "unsupported_model"
)

type gatewayHTTPError struct {
	Status        int
	OpenAIType    string
	AnthropicType string
	Message       string
	UserMessage   string
	Action        string
}

func classifyGatewayError(err error) gatewayHTTPError {
	message := gateway.UserFacingMessage(err)
	out := gatewayHTTPError{
		Status:        http.StatusInternalServerError,
		OpenAIType:    errCodeGatewayError,
		AnthropicType: "api_error",
		Message:       message,
	}
	if err == nil {
		out.Message = "gateway error"
		return out
	}

	if gateway.IsRateLimitedError(err) {
		return gatewayHTTPError{
			Status:        http.StatusTooManyRequests,
			OpenAIType:    errCodeRateLimitExceeded,
			AnthropicType: "rate_limit_error",
			Message:       err.Error(),
		}
	}

	var upstreamErr *providers.UpstreamError
	if errors.As(err, &upstreamErr) {
		return classifyUpstreamError(upstreamErr)
	}

	if gateway.IsDeniedError(err) {
		out.Status = http.StatusForbidden
		out.OpenAIType = errCodeForbidden
		out.AnthropicType = "permission_error"
		return out
	}
	if gateway.IsClientError(err) {
		out.Status = http.StatusBadRequest
		out.OpenAIType = classifyClientErrorCode(message)
		out.AnthropicType = "invalid_request_error"
		return out
	}

	lower := strings.ToLower(message)
	switch {
	case isUnsupportedModelMessage(lower):
		out.Status = http.StatusBadRequest
		out.OpenAIType = errCodeUnsupportedModel
		out.AnthropicType = "invalid_request_error"
	case isRouteImpossibleMessage(lower):
		out.Status = http.StatusServiceUnavailable
		out.OpenAIType = errCodeRouteImpossible
		out.AnthropicType = "api_error"
	}
	return out
}

func classifyUpstreamError(err *providers.UpstreamError) gatewayHTTPError {
	status := mapUpstreamStatus(err.StatusCode)
	message := err.Message
	if strings.TrimSpace(message) == "" {
		message = "upstream provider error"
	}
	out := gatewayHTTPError{
		Status:        status,
		OpenAIType:    errCodeUpstreamError,
		AnthropicType: firstNonEmptyString(err.Type, "api_error"),
		Message:       message,
	}

	lower := strings.ToLower(err.Type + " " + message)
	switch {
	case err.StatusCode == http.StatusUnauthorized || err.StatusCode == http.StatusForbidden ||
		strings.Contains(lower, "incorrect api key") ||
		strings.Contains(lower, "invalid api key") ||
		strings.Contains(lower, "authentication"):
		out.Status = http.StatusBadGateway
		out.OpenAIType = errCodeProviderAuthFailed
		out.AnthropicType = "authentication_error"
	case err.StatusCode == http.StatusTooManyRequests:
		out.Status = http.StatusTooManyRequests
		out.OpenAIType = errCodeProviderRateLimited
		out.AnthropicType = "rate_limit_error"
	case err.StatusCode == http.StatusRequestTimeout ||
		err.StatusCode == http.StatusBadGateway ||
		err.StatusCode == http.StatusServiceUnavailable ||
		err.StatusCode == http.StatusGatewayTimeout ||
		err.StatusCode >= 500:
		out.Status = http.StatusBadGateway
		out.OpenAIType = errCodeProviderUnavailable
		out.AnthropicType = "api_error"
	case isUnsupportedModelMessage(lower):
		out.Status = http.StatusBadRequest
		out.OpenAIType = errCodeUnsupportedModel
		out.AnthropicType = "invalid_request_error"
	}
	return out
}

func classifyClientErrorCode(message string) string {
	if isUnsupportedModelMessage(strings.ToLower(message)) {
		return errCodeUnsupportedModel
	}
	return errCodeInvalidRequest
}

func isUnsupportedModelMessage(message string) bool {
	return strings.Contains(message, "unsupported model") ||
		strings.Contains(message, "does not support explicit model") ||
		strings.Contains(message, "no provider supports explicit model") ||
		(strings.Contains(message, "model") &&
			(strings.Contains(message, "does not exist") ||
				strings.Contains(message, "not found") ||
				strings.Contains(message, "do not have access")))
}

func isRouteImpossibleMessage(message string) bool {
	return strings.Contains(message, "no model available for routing") ||
		strings.Contains(message, "no provider available") ||
		strings.Contains(message, "has no default model for routing") ||
		strings.Contains(message, "provider ") && strings.Contains(message, " not found")
}

func writeOpenAIGatewayError(w http.ResponseWriter, classified gatewayHTTPError, details ErrorDetails) {
	details = enrichGatewayErrorDetails(classified, details)
	if details.TraceID != "" {
		w.Header().Set("X-Trace-Id", details.TraceID)
	}
	WriteErrorDetails(w, classified.Status, classified.OpenAIType, classified.Message, details)
}

func writeAnthropicGatewayError(w http.ResponseWriter, classified gatewayHTTPError, details ErrorDetails) {
	details = enrichGatewayErrorDetails(classified, details)
	if details.TraceID != "" {
		w.Header().Set("X-Trace-Id", details.TraceID)
	}
	errorObject := map[string]any{
		"type":    classified.AnthropicType,
		"message": classified.Message,
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
	WriteJSON(w, classified.Status, map[string]any{
		"type":  "error",
		"error": errorObject,
	})
}

func enrichGatewayErrorDetails(classified gatewayHTTPError, details ErrorDetails) ErrorDetails {
	if details.UserMessage == "" {
		details.UserMessage = firstNonEmptyString(classified.UserMessage, gatewayErrorUserMessage(classified.OpenAIType), classified.Message)
	}
	if details.OperatorAction == "" {
		details.OperatorAction = firstNonEmptyString(classified.Action, gatewayErrorAction(classified.OpenAIType))
	}
	return details
}

func gatewayErrorUserMessage(code string) string {
	switch code {
	case errCodeProviderAuthFailed:
		return "Provider credentials failed."
	case errCodeProviderRateLimited:
		return "The selected provider is rate limited."
	case errCodeProviderUnavailable:
		return "The selected provider is unavailable."
	case errCodeRouteImpossible:
		return "No configured provider can serve this request."
	case errCodeUnsupportedModel:
		return "No configured provider supports the requested model."
	case errCodeForbidden:
		return "The request was blocked by policy."
	case errCodeRateLimitExceeded:
		return "Hecate's local gateway rate limit was exceeded."
	default:
		return "Gateway request failed."
	}
}

func gatewayErrorAction(code string) string {
	switch code {
	case errCodeProviderAuthFailed:
		return "Update the provider credentials, then use Connections to test readiness again."
	case errCodeProviderRateLimited:
		return "Wait for the provider cooldown, reduce concurrency, or choose another provider/model."
	case errCodeProviderUnavailable:
		return "Check provider health, endpoint URL, local server status, or fail over to another provider."
	case errCodeRouteImpossible:
		return "Open Connections to inspect readiness checks, discover models, or enable a routable provider."
	case errCodeUnsupportedModel:
		return "Choose a discovered model for the selected provider, or switch provider routing back to Auto."
	case errCodeForbidden:
		return "Review policy rules, provider/model allowlists, and routing settings."
	case errCodeRateLimitExceeded:
		return "Wait for the bucket to refill or adjust the local gateway rate limit."
	default:
		return "Open Observability for the request trace and inspect route/provider diagnostics."
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
