package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestMessagesNonStreamTranslatesRequestAndResponse(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{
		name: "openai",
		response: &types.ChatResponse{
			ID:        "chatcmpl-msgs-1",
			Model:     "gpt-4o-mini-2024-07-18",
			CreatedAt: time.Unix(1_700_000_000, 0).UTC(),
			Choices: []types.ChatChoice{{
				Index: 0,
				Message: types.Message{
					Role:    "assistant",
					Content: "Hello, human.",
				},
				FinishReason: "stop",
			}},
			Usage: types.Usage{PromptTokens: 12, CompletionTokens: 4, TotalTokens: 16},
		},
	}

	handler := newTestHTTPHandler(logger, provider)

	body := `{
		"model": "gpt-4o-mini",
		"max_tokens": 128,
		"system": "You are terse.",
		"messages": [
			{"role": "user", "content": "Hi."}
		]
	}`

	recorder := performRequest(t, handler, http.MethodPost, "/v1/messages", body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", recorder.Code, recorder.Body.String())
	}
	if ct := recorder.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	if got := recorder.Header().Get("X-Runtime-Provider"); got != "openai" {
		t.Fatalf("X-Runtime-Provider = %q, want openai", got)
	}

	var resp AnthropicMessagesResponse
	if err := json.NewDecoder(bytes.NewReader(recorder.Body.Bytes())).Decode(&resp); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if resp.Type != "message" {
		t.Fatalf("type = %q, want message", resp.Type)
	}
	if resp.Role != "assistant" {
		t.Fatalf("role = %q, want assistant", resp.Role)
	}
	if resp.Model != "gpt-4o-mini-2024-07-18" {
		t.Fatalf("model = %q, want resolved model", resp.Model)
	}
	if resp.StopReason != "end_turn" {
		t.Fatalf("stop_reason = %q, want end_turn", resp.StopReason)
	}
	if resp.Usage.InputTokens != 12 || resp.Usage.OutputTokens != 4 {
		t.Fatalf("usage = %+v, want input=12 output=4", resp.Usage)
	}
	if len(resp.Content) != 1 || resp.Content[0].Type != "text" || resp.Content[0].Text != "Hello, human." {
		t.Fatalf("content = %+v, want single text block", resp.Content)
	}
}

func TestMessagesSystemBlockArrayAndStructuredMessages(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	// Capture the request that reaches the provider to assert the system
	// prompt and structured tool_result content were correctly normalised.
	var captured types.ChatRequest
	provider := &recordingProvider{
		fakeProvider: fakeProvider{
			name: "openai",
			response: &types.ChatResponse{
				ID:        "chatcmpl-msgs-2",
				Model:     "gpt-4o-mini",
				CreatedAt: time.Unix(1_700_000_001, 0).UTC(),
				Choices: []types.ChatChoice{{
					Index: 0,
					Message: types.Message{
						Role:    "assistant",
						Content: "Tool complete.",
					},
					FinishReason: "length",
				}},
				Usage: types.Usage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
			},
		},
		captured: &captured,
	}

	handler := newTestHTTPHandler(logger, provider)

	body := `{
		"model": "gpt-4o-mini",
		"max_tokens": 32,
		"system": [
			{"type": "text", "text": "Act as a helpful assistant."},
			{"type": "text", "text": "Answer briefly."}
		],
		"messages": [
			{"role": "user", "content": [
				{"type": "text", "text": "What is 2+2?"}
			]},
			{"role": "assistant", "content": [
				{"type": "tool_use", "id": "toolu_1", "name": "calc", "input": {"a": 2, "b": 2}}
			]},
			{"role": "user", "content": [
				{"type": "tool_result", "tool_use_id": "toolu_1", "content": [
					{"type": "text", "text": "4"}
				]}
			]}
		]
	}`

	recorder := performRequest(t, handler, http.MethodPost, "/v1/messages", body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", recorder.Code, recorder.Body.String())
	}

	var resp AnthropicMessagesResponse
	if err := json.NewDecoder(bytes.NewReader(recorder.Body.Bytes())).Decode(&resp); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if resp.StopReason != "max_tokens" {
		t.Fatalf("stop_reason = %q, want max_tokens", resp.StopReason)
	}

	// Assert the normalised request routed to the provider has a merged system
	// message and a tool-role message carrying the flattened tool_result text.
	if len(captured.Messages) < 4 {
		t.Fatalf("captured messages count = %d, want >=4, got=%+v", len(captured.Messages), captured.Messages)
	}
	if captured.Messages[0].Role != "system" {
		t.Fatalf("messages[0].role = %q, want system", captured.Messages[0].Role)
	}
	if !strings.Contains(captured.Messages[0].Content, "Act as a helpful assistant.") ||
		!strings.Contains(captured.Messages[0].Content, "Answer briefly.") {
		t.Fatalf("system message content = %q, want merged system blocks", captured.Messages[0].Content)
	}
	// Find the tool message.
	var toolMsg *types.Message
	for i := range captured.Messages {
		if captured.Messages[i].Role == "tool" {
			toolMsg = &captured.Messages[i]
			break
		}
	}
	if toolMsg == nil {
		t.Fatalf("no tool-role message in captured messages: %+v", captured.Messages)
	}
	if toolMsg.ToolCallID != "toolu_1" {
		t.Fatalf("tool_call_id = %q, want toolu_1", toolMsg.ToolCallID)
	}
	if !strings.Contains(toolMsg.Content, "4") {
		t.Fatalf("tool content = %q, want to contain 4", toolMsg.Content)
	}
}

func TestMessagesRejectsMissingMaxTokens(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{name: "openai", response: &types.ChatResponse{}}
	handler := newTestHTTPHandler(logger, provider)

	body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`
	recorder := performRequest(t, handler, http.MethodPost, "/v1/messages", body)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestTranslateOpenAIToAnthropicSSE(t *testing.T) {
	t.Parallel()

	input := strings.Join([]string{
		`data: {"id":"chatcmpl-x","model":"gpt-4o-mini","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		``,
		`data: {"id":"chatcmpl-x","model":"gpt-4o-mini","choices":[{"index":0,"delta":{"content":"Hel"},"finish_reason":null}]}`,
		``,
		`data: {"id":"chatcmpl-x","model":"gpt-4o-mini","choices":[{"index":0,"delta":{"content":"lo"},"finish_reason":null}]}`,
		``,
		`data: {"id":"chatcmpl-x","model":"gpt-4o-mini","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":2,"total_tokens":9}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")

	var buf bytes.Buffer
	if err := translateOpenAIToAnthropicSSE(context.Background(), "gpt-4o-mini", "gpt-4o-mini", strings.NewReader(input), &buf); err != nil {
		t.Fatalf("translateOpenAIToAnthropicSSE() error = %v", err)
	}

	out := buf.String()
	for _, want := range []string{
		"event: message_start",
		"event: content_block_start",
		`"type":"text"`,
		"event: content_block_delta",
		`"type":"text_delta"`,
		`"text":"Hel"`,
		`"text":"lo"`,
		"event: content_block_stop",
		"event: message_delta",
		`"stop_reason":"end_turn"`,
		`"output_tokens":2`,
		"event: message_stop",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in stream output:\n%s", want, out)
		}
	}
}

func TestMessagesCacheControlPreservedInContentBlocks(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	var captured types.ChatRequest
	provider := &recordingProvider{
		fakeProvider: fakeProvider{
			name: "openai",
			response: &types.ChatResponse{
				ID:    "chatcmpl-cc",
				Model: "gpt-4o-mini",
				Choices: []types.ChatChoice{{
					Index:        0,
					Message:      types.Message{Role: "assistant", Content: "4"},
					FinishReason: "stop",
				}},
				Usage: types.Usage{PromptTokens: 8, CompletionTokens: 1, TotalTokens: 9},
			},
		},
		captured: &captured,
	}

	handler := newTestHTTPHandler(logger, provider)

	body := `{
		"model":      "gpt-4o-mini",
		"max_tokens": 64,
		"system": [
			{"type": "text", "text": "You are a calculator.", "cache_control": {"type": "ephemeral"}}
		],
		"messages": [
			{"role": "user", "content": [
				{"type": "text", "text": "What is 2+2?", "cache_control": {"type": "ephemeral"}}
			]}
		]
	}`

	recorder := performRequest(t, handler, http.MethodPost, "/v1/messages", body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", recorder.Code, recorder.Body.String())
	}

	// System message must carry ContentBlocks with cache_control.
	if len(captured.Messages) == 0 {
		t.Fatal("no messages captured")
	}
	sysMsg := captured.Messages[0]
	if sysMsg.Role != "system" {
		t.Fatalf("messages[0].role = %q, want system", sysMsg.Role)
	}
	if len(sysMsg.ContentBlocks) == 0 {
		t.Fatal("system message has no ContentBlocks")
	}
	if len(sysMsg.ContentBlocks[0].CacheControl) == 0 {
		t.Fatal("system ContentBlocks[0] missing CacheControl")
	}

	// User message must carry ContentBlocks with cache_control.
	var userMsg *types.Message
	for i := range captured.Messages {
		if captured.Messages[i].Role == "user" {
			userMsg = &captured.Messages[i]
			break
		}
	}
	if userMsg == nil {
		t.Fatal("no user message captured")
	}
	if len(userMsg.ContentBlocks) == 0 {
		t.Fatal("user message has no ContentBlocks")
	}
	if len(userMsg.ContentBlocks[0].CacheControl) == 0 {
		t.Fatal("user ContentBlocks[0] missing CacheControl")
	}
	// Content string must also be populated (used by OpenAI provider).
	if !strings.Contains(userMsg.Content, "2+2") {
		t.Fatalf("user.Content = %q, want text of the block", userMsg.Content)
	}
}

// recordingProvider wraps fakeProvider and captures the last request.
type recordingProvider struct {
	fakeProvider
	captured *types.ChatRequest
}

func (p *recordingProvider) Chat(ctx context.Context, req types.ChatRequest) (*types.ChatResponse, error) {
	if p.captured != nil {
		*p.captured = req
	}
	return p.fakeProvider.Chat(ctx, req)
}

func TestNormalizeAnthropicRequestPassesThinking(t *testing.T) {
	t.Parallel()
	thinking := json.RawMessage(`{"type":"enabled","budget_tokens":5000}`)
	betas := []string{"interleaved-thinking-2025-02-19"}
	req := AnthropicMessagesRequest{
		Model:     "claude-opus-4-5",
		MaxTokens: 1024,
		Messages:  []AnthropicInboundMessage{{Role: "user", Content: json.RawMessage(`"hello"`)}},
		Thinking:  thinking,
		Betas:     betas,
	}
	internal, err := normalizeAnthropicRequest(req, "req-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(internal.Thinking) != string(thinking) {
		t.Errorf("Thinking = %s, want %s", internal.Thinking, thinking)
	}
	if len(internal.Betas) != 1 || internal.Betas[0] != betas[0] {
		t.Errorf("Betas = %v, want %v", internal.Betas, betas)
	}
}

// ---------------------------------------------------------------------------
// Feature 5: thinking/redacted_thinking blocks survive inbound conversion
// ---------------------------------------------------------------------------

// TestConvertAnthropicInboundMessagePreservesToolResultIsError pins
// the round-trip: when an inbound /v1/messages caller hands us a
// tool_result with is_error=true (e.g. they're proxying through
// Hecate to upstream Anthropic), the flag must land on the
// internal types.Message.ToolError so the outbound provider can
// re-emit it. Pre-fix the field was silently dropped at this
// boundary.
func TestConvertAnthropicInboundMessagePreservesToolResultIsError(t *testing.T) {
	t.Parallel()
	content := `[
		{"type":"tool_result","tool_use_id":"toolu_a","content":"command not found","is_error":true}
	]`
	msg := AnthropicInboundMessage{
		Role:    "user",
		Content: json.RawMessage(content),
	}
	msgs, err := convertAnthropicInboundMessage(msg)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	if msgs[0].Role != "tool" {
		t.Fatalf("role = %q, want tool", msgs[0].Role)
	}
	if !msgs[0].ToolError {
		t.Errorf("ToolError = false, want true (is_error must round-trip through the gateway)")
	}
	if msgs[0].ToolCallID != "toolu_a" {
		t.Errorf("ToolCallID = %q, want toolu_a", msgs[0].ToolCallID)
	}
}

func TestConvertAnthropicInboundMessageThinkingBlocks(t *testing.T) {
	t.Parallel()
	content := `[
		{"type":"thinking","thinking":"let me think","signature":"sig123"},
		{"type":"redacted_thinking","data":"opaque"},
		{"type":"text","text":"answer"}
	]`
	msg := AnthropicInboundMessage{
		Role:    "assistant",
		Content: json.RawMessage(content),
	}
	msgs, err := convertAnthropicInboundMessage(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	blocks := msgs[0].ContentBlocks
	if len(blocks) != 3 {
		t.Fatalf("got %d content blocks, want 3: %+v", len(blocks), blocks)
	}
	if blocks[0].Type != "thinking" || blocks[0].Thinking != "let me think" || blocks[0].Signature != "sig123" {
		t.Errorf("block[0] = %+v, want thinking block", blocks[0])
	}
	if blocks[1].Type != "redacted_thinking" || blocks[1].Data != "opaque" {
		t.Errorf("block[1] = %+v, want redacted_thinking block", blocks[1])
	}
	if blocks[2].Type != "text" || blocks[2].Text != "answer" {
		t.Errorf("block[2] = %+v, want text block", blocks[2])
	}
}

func TestRenderAnthropicMessagesResponseThinkingBlocks(t *testing.T) {
	t.Parallel()
	resp := &types.ChatResponse{
		ID:    "msg-think-1",
		Model: "claude-opus-4-5",
		Choices: []types.ChatChoice{
			{
				Message: types.Message{
					Role: "assistant",
					ContentBlocks: []types.ContentBlock{
						{Type: "thinking", Thinking: "my reasoning", Signature: "sig-abc"},
						{Type: "redacted_thinking", Data: "opaque-blob"},
						{Type: "text", Text: "The answer is 42."},
					},
				},
				FinishReason: "end_turn",
			},
		},
	}

	out := renderAnthropicMessagesResponse(resp)
	if len(out.Content) != 3 {
		t.Fatalf("got %d content blocks, want 3: %+v", len(out.Content), out.Content)
	}
	if out.Content[0].Type != "thinking" || out.Content[0].Thinking != "my reasoning" || out.Content[0].Signature != "sig-abc" {
		t.Errorf("block[0] = %+v, want thinking block", out.Content[0])
	}
	if out.Content[1].Type != "redacted_thinking" || out.Content[1].Data != "opaque-blob" {
		t.Errorf("block[1] = %+v, want redacted_thinking block", out.Content[1])
	}
	if out.Content[2].Type != "text" || out.Content[2].Text != "The answer is 42." {
		t.Errorf("block[2] = %+v, want text block", out.Content[2])
	}
}

func TestTranslateOpenAIToAnthropicSSEWithThinking(t *testing.T) {
	t.Parallel()
	// Simulate OpenAI SSE chunks that carry x_thinking extension fields
	// (as emitted by translateAnthropicSSE when routing via Anthropic provider).
	chunks := []string{
		`data: {"id":"c1","model":"claude-opus-4-5","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		`data: {"id":"c1","model":"claude-opus-4-5","choices":[{"index":0,"delta":{"x_thinking":"reasoning here"},"finish_reason":null}]}`,
		`data: {"id":"c1","model":"claude-opus-4-5","choices":[{"index":0,"delta":{"x_thinking_signature":"sig-xyz"},"finish_reason":null}]}`,
		`data: {"id":"c1","model":"claude-opus-4-5","choices":[{"index":0,"delta":{"content":"final answer"},"finish_reason":null}]}`,
		`data: {"id":"c1","model":"claude-opus-4-5","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	}
	input := strings.Join(chunks, "\n") + "\n"

	var buf bytes.Buffer
	err := translateOpenAIToAnthropicSSE(context.Background(), "claude-opus-4-5", "claude-opus-4-5",
		strings.NewReader(input), &buf)
	if err != nil {
		t.Fatalf("translateOpenAIToAnthropicSSE error: %v", err)
	}

	output := buf.String()

	// Should contain a thinking content_block_start
	if !strings.Contains(output, `"thinking"`) {
		t.Errorf("output missing thinking block:\n%s", output)
	}
	// Should contain thinking_delta event
	if !strings.Contains(output, "thinking_delta") {
		t.Errorf("output missing thinking_delta:\n%s", output)
	}
	// Should contain signature_delta event
	if !strings.Contains(output, "signature_delta") {
		t.Errorf("output missing signature_delta:\n%s", output)
	}
	// Should contain the text content
	if !strings.Contains(output, "final answer") {
		t.Errorf("output missing text content:\n%s", output)
	}
	// Should end with message_stop
	if !strings.Contains(output, "message_stop") {
		t.Errorf("output missing message_stop:\n%s", output)
	}
}

// ---------------------------------------------------------------------------
// Handler error envelopes — every error path must produce the Anthropic-shaped
// {"type":"error","error":{...}} body, not the OpenAI-shaped {"error":{...}}
// shape used by /v1/chat/completions. SDK clients pointed at
// ANTHROPIC_BASE_URL parse the Anthropic envelope; a regression that leaked
// the OpenAI shape would surface to operators as "unexpected response from
// Anthropic" without any actionable detail.
// ---------------------------------------------------------------------------
func TestMessagesReturns429OnRateLimit(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{
		name: "openai", defaultModel: "gpt-4o-mini",
		response: &types.ChatResponse{
			ID: "chatcmpl-rl", Model: "gpt-4o-mini",
			Choices: []types.ChatChoice{{Message: types.Message{Role: "assistant", Content: "ok"}, FinishReason: "stop"}},
		},
	}
	// burst=1 → second request returns 429 with the Anthropic envelope.
	handler := newTestHTTPHandlerWithConfig(logger, provider, config.Config{
		Server: config.ServerConfig{
			RateLimit: config.RateLimitConfig{Enabled: true, RequestsPerMinute: 60, BurstSize: 1},
		},
	})

	body := `{"model":"gpt-4o-mini","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`
	first := performRequest(t, handler, http.MethodPost, "/v1/messages", body)
	if first.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200; body=%s", first.Code, first.Body.String())
	}
	second := performRequest(t, handler, http.MethodPost, "/v1/messages", body)
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want 429; body=%s", second.Code, second.Body.String())
	}
	// 429 from the rate limiter is written by checkRateLimit, which uses the
	// shared OpenAI-shaped WriteError. Anthropic clients see the same body
	// shape used by /v1/chat/completions for this one path — documented here
	// rather than diverged-and-forgotten. If the handler is later refactored
	// to emit the Anthropic envelope from checkRateLimit, this test should
	// flip to assert that contract.
	var payload struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.NewDecoder(second.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload.Error.Type != "rate_limit_exceeded" {
		t.Errorf("error.type = %q, want rate_limit_exceeded", payload.Error.Type)
	}
}

func TestMessagesMapsUpstreamErrorWithAnthropicEnvelope(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{
		name: "openai",
		err: &providers.UpstreamError{
			StatusCode: http.StatusTooManyRequests,
			Message:    "upstream rate limit exceeded",
			Type:       "rate_limit_error",
		},
	}
	handler := newTestHTTPHandler(logger, provider)

	body := `{"model":"gpt-4o-mini","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`
	rec := performRequest(t, handler, http.MethodPost, "/v1/messages", body)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Type  string `json:"type"`
		Error struct {
			Type           string `json:"type"`
			Message        string `json:"message"`
			UserMessage    string `json:"user_message"`
			OperatorAction string `json:"operator_action"`
			RequestID      string `json:"request_id"`
			TraceID        string `json:"trace_id"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload.Type != "error" {
		t.Errorf("envelope type = %q, want error", payload.Type)
	}
	if payload.Error.Type != "rate_limit_error" {
		t.Errorf("error.type = %q, want rate_limit_error (carried from UpstreamError)", payload.Error.Type)
	}
	if payload.Error.Message != "upstream rate limit exceeded" {
		t.Errorf("error.message = %q, want upstream message verbatim", payload.Error.Message)
	}
	if payload.Error.UserMessage == "" {
		t.Fatal("error.user_message = empty, want operator-facing summary")
	}
	if payload.Error.OperatorAction == "" {
		t.Fatal("error.operator_action = empty, want next-step guidance")
	}
	if payload.Error.RequestID == "" || payload.Error.RequestID != rec.Header().Get("X-Request-Id") {
		t.Fatalf("error.request_id = %q, header = %q", payload.Error.RequestID, rec.Header().Get("X-Request-Id"))
	}
	if payload.Error.TraceID == "" || payload.Error.TraceID != rec.Header().Get("X-Trace-Id") {
		t.Fatalf("error.trace_id = %q, header = %q", payload.Error.TraceID, rec.Header().Get("X-Trace-Id"))
	}
}

func TestMessagesDeniedReturns403WithUserFacingMessage(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{name: "openai", response: &types.ChatResponse{}}

	handler := newTestHTTPHandlerWithConfig(logger, provider, config.Config{
		Governor: config.GovernorConfig{DenyAll: true},
	})

	body := `{"model":"gpt-4o-mini","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`
	rec := performRequest(t, handler, http.MethodPost, "/v1/messages", body)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if strings.HasPrefix(payload.Error.Message, "request denied: ") {
		t.Errorf("error.message = %q, want classification prefix stripped", payload.Error.Message)
	}
}

// failingMessagesStreamProvider wraps fakeProvider and overrides
// ChatStream to return a configurable upstream error mid-stream.
// Used by the mid-stream-error tests.
type failingMessagesStreamProvider struct {
	fakeProvider
	streamErr error
}

func (p *failingMessagesStreamProvider) ChatStream(_ context.Context, _ types.ChatRequest, _ io.Writer) error {
	return p.streamErr
}

var (
	_ providers.Streamer = (*failingMessagesStreamProvider)(nil)
	_ providers.Provider = (*failingMessagesStreamProvider)(nil)
)

// TestMessagesStreamMidStreamErrorEventBodyIsValidJSON pins the
// Anthropic error-event JSON across special characters. Same hazard
// as the OpenAI SSE counterpart — a regression to naive concat would
// silently corrupt the body when an upstream message contains quotes
// or newlines, and Anthropic SDKs that parse `event: error\ndata: {...}`
// would surface a JSON parse error instead of the actual reason.
func TestMessagesStreamMidStreamErrorEventBodyIsValidJSON(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &failingMessagesStreamProvider{
		fakeProvider: fakeProvider{name: "openai", defaultModel: "gpt-4o-mini"},
		streamErr: &providers.UpstreamError{
			StatusCode: http.StatusBadGateway,
			Message:    `failed: "bad" model` + "\n" + `path=\x`,
			Type:       "server_error",
		},
	}
	handler := newTestHTTPHandler(logger, provider)

	body := `{"model":"gpt-4o-mini","max_tokens":32,"stream":true,"messages":[{"role":"user","content":"hi"}]}`
	rec := performRequest(t, handler, http.MethodPost, "/v1/messages", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()

	// The Anthropic SSE shape is:
	//   event: error
	//   data: {"type":"error","error":{"type":"api_error","message":"..."}}
	// Find the `data:` line that follows `event: error` and JSON.parse it.
	idx := strings.Index(out, "event: error")
	if idx < 0 {
		t.Fatalf("body missing 'event: error'; got=%q", out)
	}
	rest := out[idx:]
	dataIdx := strings.Index(rest, "data: ")
	if dataIdx < 0 {
		t.Fatalf("body missing 'data:' line after error event; got=%q", out)
	}
	rest = rest[dataIdx+len("data: "):]
	end := strings.Index(rest, "\n")
	if end < 0 {
		end = len(rest)
	}
	dataLine := rest[:end]
	var payload struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(dataLine), &payload); err != nil {
		t.Fatalf("anthropic error frame is not valid JSON: %v\nframe=%q", err, dataLine)
	}
	want := `failed: "bad" model` + "\n" + `path=\x`
	if payload.Error.Message != want {
		t.Errorf("parsed message = %q, want %q", payload.Error.Message, want)
	}
}

func TestMessagesStreamMidStreamErrorEmitsAnthropicErrorEvent(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &failingMessagesStreamProvider{
		fakeProvider: fakeProvider{name: "openai", defaultModel: "gpt-4o-mini"},
		streamErr: &providers.UpstreamError{
			StatusCode: http.StatusBadGateway,
			Message:    "upstream connection reset",
			Type:       "server_error",
		},
	}
	handler := newTestHTTPHandler(logger, provider)

	body := `{"model":"gpt-4o-mini","max_tokens":32,"stream":true,"messages":[{"role":"user","content":"hi"}]}`
	rec := performRequest(t, handler, http.MethodPost, "/v1/messages", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (mid-stream errors keep the headers we already sent); body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	out := rec.Body.String()
	if !strings.Contains(out, "event: error") {
		t.Errorf("body missing 'event: error' line; got=%q", out)
	}
	if !strings.Contains(out, "upstream connection reset") {
		t.Errorf("body missing upstream message; got=%q", out)
	}
}
