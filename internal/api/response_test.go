package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteErrorAddsDefaultOperatorMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		code string
	}{
		{name: "invalid request", code: errCodeInvalidRequest},
		{name: "not found", code: errCodeNotFound},
		{name: "conflict", code: errCodeConflict},
		{name: "gateway error", code: errCodeGatewayError},
		{name: "rate limit", code: errCodeRateLimitExceeded},
		{name: "chat busy", code: errCodeAgentSessionBusy},
		{name: "model not configured", code: errCodeModelNotConfigured},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			WriteError(rec, http.StatusBadRequest, tt.code, "runtime detail")

			var payload struct {
				Error struct {
					Type           string `json:"type"`
					Message        string `json:"message"`
					UserMessage    string `json:"user_message"`
					OperatorAction string `json:"operator_action"`
				} `json:"error"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if payload.Error.Type != tt.code {
				t.Fatalf("error.type = %q, want %q", payload.Error.Type, tt.code)
			}
			if payload.Error.Message != "runtime detail" {
				t.Fatalf("error.message = %q, want runtime detail", payload.Error.Message)
			}
			if payload.Error.UserMessage == "" {
				t.Fatal("error.user_message = empty")
			}
			if payload.Error.OperatorAction == "" {
				t.Fatal("error.operator_action = empty")
			}
		})
	}
}

func TestWriteErrorDetailsPreservesExplicitMetadataAndFields(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	WriteErrorDetails(rec, http.StatusConflict, errCodeAgentSessionBusy, "busy detail", ErrorDetails{
		UserMessage:    "Custom user message.",
		OperatorAction: "Custom action.",
		RequestID:      "req_123",
		TraceID:        "trace_456",
		Fields: map[string]any{
			"task_id":       "task_123",
			"latest_run_id": "run_456",
			"run_status":    "running",
		},
	})

	var payload struct {
		Error struct {
			Type           string `json:"type"`
			Message        string `json:"message"`
			UserMessage    string `json:"user_message"`
			OperatorAction string `json:"operator_action"`
			RequestID      string `json:"request_id"`
			TraceID        string `json:"trace_id"`
			TaskID         string `json:"task_id"`
			LatestRunID    string `json:"latest_run_id"`
			RunStatus      string `json:"run_status"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload.Error.UserMessage != "Custom user message." {
		t.Fatalf("error.user_message = %q", payload.Error.UserMessage)
	}
	if payload.Error.OperatorAction != "Custom action." {
		t.Fatalf("error.operator_action = %q", payload.Error.OperatorAction)
	}
	if payload.Error.RequestID != "req_123" || payload.Error.TraceID != "trace_456" {
		t.Fatalf("correlation = request %q trace %q", payload.Error.RequestID, payload.Error.TraceID)
	}
	if payload.Error.TaskID != "task_123" || payload.Error.LatestRunID != "run_456" || payload.Error.RunStatus != "running" {
		t.Fatalf("runtime fields missing: %+v", payload.Error)
	}
}

func TestWriteErrorDetailsSkipsReservedRuntimeFields(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	WriteErrorDetails(rec, http.StatusConflict, errCodeAgentSessionBusy, "canonical message", ErrorDetails{
		UserMessage:    "Canonical user message.",
		OperatorAction: "Canonical action.",
		RequestID:      "req_123",
		TraceID:        "trace_456",
		Fields: map[string]any{
			"type":            "mutated_type",
			"message":         "mutated message",
			"user_message":    "mutated user message",
			"operator_action": "mutated action",
			"request_id":      "mutated_req",
			"trace_id":        "mutated_trace",
			"task_id":         "task_123",
		},
	})

	var payload struct {
		Error struct {
			Type           string `json:"type"`
			Message        string `json:"message"`
			UserMessage    string `json:"user_message"`
			OperatorAction string `json:"operator_action"`
			RequestID      string `json:"request_id"`
			TraceID        string `json:"trace_id"`
			TaskID         string `json:"task_id"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload.Error.Type != errCodeAgentSessionBusy {
		t.Fatalf("error.type = %q, want %q", payload.Error.Type, errCodeAgentSessionBusy)
	}
	if payload.Error.Message != "canonical message" {
		t.Fatalf("error.message = %q, want canonical message", payload.Error.Message)
	}
	if payload.Error.UserMessage != "Canonical user message." {
		t.Fatalf("error.user_message = %q", payload.Error.UserMessage)
	}
	if payload.Error.OperatorAction != "Canonical action." {
		t.Fatalf("error.operator_action = %q", payload.Error.OperatorAction)
	}
	if payload.Error.RequestID != "req_123" || payload.Error.TraceID != "trace_456" {
		t.Fatalf("correlation fields were overwritten: %+v", payload.Error)
	}
	if payload.Error.TaskID != "task_123" {
		t.Fatalf("non-reserved runtime field was not preserved: %+v", payload.Error)
	}
}

func TestWriteErrorDetailsSkipsNilLikeRuntimeFields(t *testing.T) {
	t.Parallel()

	type diagnostic struct {
		Message string `json:"message"`
	}
	var typedPointer *diagnostic
	var typedMap map[string]string
	var typedSlice []string
	var typedInterface any

	rec := httptest.NewRecorder()
	WriteErrorDetails(rec, http.StatusBadRequest, errCodeInvalidRequest, "invalid", ErrorDetails{
		Fields: map[string]any{
			"plain_nil":     nil,
			"typed_pointer": typedPointer,
			"typed_map":     typedMap,
			"typed_slice":   typedSlice,
			"typed_iface":   typedInterface,
			"provider":      "ollama",
		},
	})

	var payload struct {
		Error map[string]any `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	for _, key := range []string{"plain_nil", "typed_pointer", "typed_map", "typed_slice", "typed_iface"} {
		if _, ok := payload.Error[key]; ok {
			t.Fatalf("error.%s should be omitted for nil-like runtime field: %#v", key, payload.Error)
		}
	}
	if payload.Error["provider"] != "ollama" {
		t.Fatalf("error.provider = %#v, want ollama", payload.Error["provider"])
	}
}

func TestWriteErrorDetailsSkipsNonMarshalableRuntimeFields(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	WriteErrorDetails(rec, http.StatusBadRequest, errCodeInvalidRequest, "invalid", ErrorDetails{
		Fields: map[string]any{
			"callback":      func() {},
			"events":        make(chan string),
			"complex_value": complex64(1 + 2i),
			"nested_bad": map[string]any{
				"callback": func() {},
			},
			"provider": "ollama",
		},
	})

	var payload struct {
		Error map[string]any `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	for _, key := range []string{"callback", "events", "complex_value", "nested_bad"} {
		if _, ok := payload.Error[key]; ok {
			t.Fatalf("error.%s should be omitted for non-marshalable runtime field: %#v", key, payload.Error)
		}
	}
	if payload.Error["provider"] != "ollama" {
		t.Fatalf("error.provider = %#v, want ollama", payload.Error["provider"])
	}
}

func TestWriteErrorUsesGatewayCanonicalMetadata(t *testing.T) {
	t.Parallel()

	for _, code := range []string{errCodeGatewayError, errCodeRateLimitExceeded} {
		t.Run(code, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			WriteError(rec, http.StatusInternalServerError, code, "gateway exploded")

			var payload struct {
				Error struct {
					UserMessage    string `json:"user_message"`
					OperatorAction string `json:"operator_action"`
				} `json:"error"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if payload.Error.UserMessage != gatewayErrorUserMessage(code) {
				t.Fatalf("error.user_message = %q, want %q", payload.Error.UserMessage, gatewayErrorUserMessage(code))
			}
			if payload.Error.OperatorAction != gatewayErrorAction(code) {
				t.Fatalf("error.operator_action = %q, want %q", payload.Error.OperatorAction, gatewayErrorAction(code))
			}
		})
	}
}
