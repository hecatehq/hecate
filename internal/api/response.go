package api

import (
	"encoding/json"
	"net/http"
)

const (
	errCodeUnauthorized   = "unauthorized"
	errCodeInvalidRequest = "invalid_request"
	errCodeForbidden      = "forbidden"
	errCodeGatewayError   = "gateway_error"
	errCodeUpstreamError  = "upstream_error"
	errCodeNotFound       = "not_found"
	errCodeConflict       = "conflict"
)

func WriteJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, map[string]any{
		"error": map[string]any{
			"type":    code,
			"message": message,
		},
	})
}
