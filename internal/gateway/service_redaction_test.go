package gateway

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hecate/agent-runtime/internal/profiler"
	"github.com/hecate/agent-runtime/pkg/types"
)

func TestRedactSensitiveTextMasksCommonSecrets(t *testing.T) {
	t.Parallel()

	input := strings.Join([]string{
		`Authorization: Bearer sk-valid-token-secret-1234567890`,
		`{"api_key":"sk-json-secret-1234567890","password":"correct-horse-battery-staple"}`,
		`OPENAI_API_KEY=sk-env-secret-1234567890`,
		`CLAUDE_CODE_OAUTH_TOKEN=sk-claude-secret-1234567890`,
		`plain token sk-prose-secret-1234567890`,
	}, "\n")

	got := redactSensitiveText(input)
	for _, leaked := range []string{
		"sk-valid-token-secret-1234567890",
		"sk-json-secret-1234567890",
		"correct-horse-battery-staple",
		"sk-env-secret-1234567890",
		"sk-claude-secret-1234567890",
		"sk-prose-secret-1234567890",
	} {
		if strings.Contains(got, leaked) {
			t.Fatalf("redacted output leaked %q:\n%s", leaked, got)
		}
	}
	for _, want := range []string{
		`Authorization: [redacted]`,
		`"api_key":"[redacted]"`,
		`"password":"[redacted]"`,
		`OPENAI_API_KEY=[redacted]`,
		`CLAUDE_CODE_OAUTH_TOKEN=[redacted]`,
		`plain token [redacted]`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("redacted output missing %q:\n%s", want, got)
		}
	}
}

func TestRedactSensitiveTextLeavesOrdinaryContent(t *testing.T) {
	t.Parallel()

	input := "The user asked for a token budget and passwordless login docs."
	if got := redactSensitiveText(input); got != input {
		t.Fatalf("redactSensitiveText() = %q, want %q", got, input)
	}
}

func TestCaptureRequestBodyMetadataModeDoesNotRecordContent(t *testing.T) {
	t.Parallel()

	trace := profiler.NewTrace("req-1", nil)
	service := &Service{
		traceBodyMode:     traceBodyModeMetadata,
		traceBodyMaxBytes: 4096,
	}
	service.captureRequestBody(trace, types.ChatRequest{
		Model: "gpt-test",
		Messages: []types.Message{
			{
				Role:    "user",
				Content: "secret prompt sk-request-secret-1234567890",
				ToolCalls: []types.ToolCall{
					{ID: "call-1"},
				},
			},
		},
	})

	attrs := trace.Events()[0].Attributes
	if attrs["mode"] != traceBodyModeMetadata {
		t.Fatalf("mode = %v, want %q", attrs["mode"], traceBodyModeMetadata)
	}
	rawMessages, ok := attrs["messages"].(string)
	if !ok {
		t.Fatalf("messages attr = %#v, want string", attrs["messages"])
	}
	if strings.Contains(rawMessages, "secret prompt") || strings.Contains(rawMessages, "sk-request-secret") {
		t.Fatalf("metadata mode leaked content: %s", rawMessages)
	}
	var messages []struct {
		Role         string `json:"role"`
		Content      string `json:"content,omitempty"`
		ContentBytes int    `json:"content_bytes,omitempty"`
		ToolCalls    int    `json:"tool_calls,omitempty"`
	}
	if err := json.Unmarshal([]byte(rawMessages), &messages); err != nil {
		t.Fatalf("decode messages: %v", err)
	}
	if len(messages) != 1 || messages[0].Role != "user" || messages[0].Content != "" || messages[0].ContentBytes == 0 || messages[0].ToolCalls != 1 {
		t.Fatalf("messages = %#v, want metadata without content", messages)
	}
}

func TestCaptureResponseBodyRedactedTextModeMasksContent(t *testing.T) {
	t.Parallel()

	trace := profiler.NewTrace("req-1", nil)
	service := &Service{
		traceBodyMode:     traceBodyModeRedactedText,
		traceBodyMaxBytes: 4096,
	}
	service.captureResponseBody(trace, &types.ChatResponse{
		Model: "gpt-test",
		Choices: []types.ChatChoice{
			{
				Message: types.Message{
					Role:    "assistant",
					Content: "use OPENAI_API_KEY=sk-response-secret-1234567890",
				},
				FinishReason: "stop",
			},
		},
	})

	attrs := trace.Events()[0].Attributes
	if attrs["mode"] != traceBodyModeRedactedText {
		t.Fatalf("mode = %v, want %q", attrs["mode"], traceBodyModeRedactedText)
	}
	rawChoices, ok := attrs["choices"].(string)
	if !ok {
		t.Fatalf("choices attr = %#v, want string", attrs["choices"])
	}
	if strings.Contains(rawChoices, "sk-response-secret") {
		t.Fatalf("redacted_text mode leaked secret: %s", rawChoices)
	}
	if !strings.Contains(rawChoices, "OPENAI_API_KEY=[redacted]") {
		t.Fatalf("redacted_text mode missing redacted content: %s", rawChoices)
	}
}
