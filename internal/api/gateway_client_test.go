package api

// gateway_client_test.go exercises the gateway mode endpoints the same way
// Codex and Claude Code do: using a real http.Client against an
// httptest.NewServer so every byte of the HTTP surface gets covered —
// headers, body encoding, SSE framing, and auth.
//
// Two layers of tests:
//
//  1. In-process fake provider (fast) — the gateway routes to the existing
//     fakeProvider / sseStreamingProvider.  These verify the full
//     request-normalisation → gateway → response-serialisation path.
//
//  2. Fake upstream server (real provider) — an httptest.NewServer plays the
//     role of the upstream OpenAI/Anthropic API. Real provider adapters point
//     at it so their own HTTP client code is exercised end-to-end.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/catalog"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/gateway"
	"github.com/hecatehq/hecate/internal/governor"
	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/internal/router"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
)

// ---------------------------------------------------------------------------
// Streaming fake provider
// ---------------------------------------------------------------------------

// sseStreamingProvider wraps fakeProvider and adds ChatStream so the gateway's
// streaming path is exercised.  It serialises fakeProvider.response as a
// minimal but well-formed OpenAI SSE body.
type sseStreamingProvider struct {
	fakeProvider
}

func (p *sseStreamingProvider) ChatStream(_ context.Context, _ types.ChatRequest, w io.Writer) error {
	resp := p.fakeProvider.response
	if resp == nil {
		return fmt.Errorf("sseStreamingProvider: response is nil")
	}

	id := resp.ID
	if id == "" {
		id = "chatcmpl-stream-test"
	}
	model := resp.Model
	if model == "" {
		model = "gpt-4o-mini"
	}

	// role chunk
	roleChunk := fmt.Sprintf(
		`data: {"id":%q,"object":"chat.completion.chunk","model":%q,"choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		id, model,
	)
	if _, err := fmt.Fprintln(w, roleChunk); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}

	// content chunks — one word per chunk so tests can verify delta slicing
	content := ""
	if len(resp.Choices) > 0 {
		content = resp.Choices[0].Message.Content
	}
	words := strings.Fields(content)
	if len(words) == 0 {
		words = []string{""}
	}
	for _, word := range words {
		chunk := fmt.Sprintf(
			`data: {"id":%q,"object":"chat.completion.chunk","model":%q,"choices":[{"index":0,"delta":{"content":%q},"finish_reason":null}]}`,
			id, model, word+" ",
		)
		if _, err := fmt.Fprintln(w, chunk); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}

	// finish chunk
	finishReason := "stop"
	if len(resp.Choices) > 0 && resp.Choices[0].FinishReason != "" {
		finishReason = resp.Choices[0].FinishReason
	}
	finishChunk := fmt.Sprintf(
		`data: {"id":%q,"object":"chat.completion.chunk","model":%q,"choices":[{"index":0,"delta":{},"finish_reason":%q}]}`,
		id, model, finishReason,
	)
	if _, err := fmt.Fprintln(w, finishChunk); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}

	// SSE terminator
	_, err := fmt.Fprint(w, "data: [DONE]\n\n")
	return err
}

// ---------------------------------------------------------------------------
// Helper: newGatewayServer — spins up a real httptest.Server wrapping the
// gateway handler built from the given providers.
// ---------------------------------------------------------------------------

func newGatewayServer(t *testing.T, prov providers.Provider, cfg config.Config) *httptest.Server {
	t.Helper()
	return httptest.NewServer(newTestHTTPHandlerWithConfig(slog.New(slog.NewJSONHandler(io.Discard, nil)), prov, cfg))
}

// ---------------------------------------------------------------------------
// Helper: gatewayPost — sends a JSON POST to the given URL and returns the
// *http.Response (caller must close Body).
// ---------------------------------------------------------------------------

func gatewayPost(t *testing.T, url, body string, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	return resp
}

// readBody drains and closes resp.Body and returns the bytes.
func readBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	return b
}

// ---------------------------------------------------------------------------
// SSE reader (for streaming integration tests)
// ---------------------------------------------------------------------------

type streamEvent struct {
	Event string
	Data  string
}

// readSSEFromResponse reads all SSE events from resp.Body and returns them.
// It closes resp.Body when done.
func readSSEFromResponse(t *testing.T, resp *http.Response) []streamEvent {
	t.Helper()
	defer resp.Body.Close()
	var events []streamEvent
	scanner := bufio.NewScanner(resp.Body)
	var cur streamEvent
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			if cur.Data != "" || cur.Event != "" {
				events = append(events, cur)
				cur = streamEvent{}
			}
		case strings.HasPrefix(line, "event: "):
			cur.Event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			cur.Data = strings.TrimPrefix(line, "data: ")
		}
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		t.Fatalf("SSE scanner error: %v", err)
	}
	return events
}

// ===========================================================================
// Layer 1 — Codex client (POST /v1/chat/completions)
// ===========================================================================

// TestCodexClientNonStreaming verifies that a Codex-style non-streaming
// request sent over a real TCP connection produces a well-formed OpenAI
// chat-completion response and the expected runtime headers.
func TestCodexClientNonStreaming(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		name: "openai",
		response: &types.ChatResponse{
			ID:        "chatcmpl-codex-1",
			Model:     "gpt-4o-mini-2024-07-18",
			CreatedAt: time.Unix(1_700_000_000, 0).UTC(),
			Choices: []types.ChatChoice{{
				Index:        0,
				Message:      types.Message{Role: "assistant", Content: "4"},
				FinishReason: "stop",
			}},
			Usage: types.Usage{PromptTokens: 10, CompletionTokens: 1, TotalTokens: 11},
		},
	}
	srv := newGatewayServer(t, provider, config.Config{})
	defer srv.Close()

	body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"What is 2+2?"}]}`
	resp := gatewayPost(t, srv.URL+"/v1/chat/completions", body, map[string]string{
		"Authorization": "Bearer unused", // Codex sends Bearer; gateway has no auth configured here
	})
	raw := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, raw)
	}

	// Runtime headers that Codex consumers may inspect
	for _, hdr := range []string{
		"X-Request-Id",
		"X-Trace-Id",
		"X-Span-Id",
		"X-Runtime-Provider",
		"X-Runtime-Model",
		"X-Runtime-Route-Reason",
		"X-Runtime-Cost-USD",
	} {
		if v := resp.Header.Get(hdr); v == "" {
			t.Errorf("header %s = empty, want non-empty", hdr)
		}
	}
	if got := resp.Header.Get("X-Runtime-Provider"); got != "openai" {
		t.Errorf("X-Runtime-Provider = %q, want openai", got)
	}

	// Response body must be a valid OpenAI completion
	var completion OpenAIChatCompletionResponse
	if err := json.Unmarshal(raw, &completion); err != nil {
		t.Fatalf("Unmarshal() error = %v, body=%s", err, raw)
	}
	if completion.Object != "chat.completion" {
		t.Errorf("object = %q, want chat.completion", completion.Object)
	}
	if len(completion.Choices) != 1 {
		t.Fatalf("choices len = %d, want 1", len(completion.Choices))
	}
	if got := completion.Choices[0].Message.Content.AsString(); got != "4" {
		t.Errorf("choices[0].message.content = %q, want \"4\"", got)
	}
	if completion.Choices[0].FinishReason != "stop" {
		t.Errorf("finish_reason = %q, want stop", completion.Choices[0].FinishReason)
	}
	if completion.Usage.PromptTokens != 10 {
		t.Errorf("usage.prompt_tokens = %d, want 10", completion.Usage.PromptTokens)
	}
}

// TestCodexClientStreaming verifies that Codex-style streaming over a real TCP
// connection returns valid OpenAI SSE chunks terminated by "data: [DONE]".
func TestCodexClientStreaming(t *testing.T) {
	t.Parallel()

	provider := &sseStreamingProvider{
		fakeProvider: fakeProvider{
			name: "openai",
			response: &types.ChatResponse{
				ID:    "chatcmpl-stream-codex",
				Model: "gpt-4o-mini",
				Choices: []types.ChatChoice{{
					Message:      types.Message{Role: "assistant", Content: "Hello world"},
					FinishReason: "stop",
				}},
				Usage: types.Usage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7},
			},
		},
	}
	srv := newGatewayServer(t, provider, config.Config{})
	defer srv.Close()

	body := `{"model":"gpt-4o-mini","stream":true,"messages":[{"role":"user","content":"Say hello"}]}`
	resp := gatewayPost(t, srv.URL+"/v1/chat/completions", body, nil)

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, b)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	events := readSSEFromResponse(t, resp)
	if len(events) == 0 {
		t.Fatal("no SSE events received")
	}

	// All data lines before [DONE] must be valid JSON completion chunks.
	foundDone := false
	contentSeen := false
	for _, ev := range events {
		if ev.Data == "[DONE]" {
			foundDone = true
			continue
		}
		var chunk map[string]json.RawMessage
		if err := json.Unmarshal([]byte(ev.Data), &chunk); err != nil {
			t.Errorf("SSE event data is not valid JSON: %s", ev.Data)
			continue
		}
		if _, ok := chunk["choices"]; !ok {
			t.Errorf("SSE chunk missing 'choices' key: %s", ev.Data)
		}
		// Look for a content delta
		var choices []map[string]json.RawMessage
		if err := json.Unmarshal(chunk["choices"], &choices); err == nil {
			for _, c := range choices {
				var delta map[string]string
				if err := json.Unmarshal(c["delta"], &delta); err == nil {
					if delta["content"] != "" {
						contentSeen = true
					}
				}
			}
		}
	}
	if !foundDone {
		t.Error("SSE stream did not end with 'data: [DONE]'")
	}
	if !contentSeen {
		t.Error("no content delta found in SSE stream")
	}
}

// TestCodexClientRateLimitHeaders verifies that Codex receives X-RateLimit-*
// headers on each response and a 429 when the bucket is exhausted.
func TestCodexClientRateLimitHeaders(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		name: "openai",
		response: &types.ChatResponse{
			ID:    "chatcmpl-rl",
			Model: "gpt-4o-mini",
			Choices: []types.ChatChoice{{
				Message:      types.Message{Role: "assistant", Content: "ok"},
				FinishReason: "stop",
			}},
			Usage: types.Usage{PromptTokens: 3, CompletionTokens: 1, TotalTokens: 4},
		},
	}
	srv := newGatewayServer(t, provider, config.Config{
		Server: config.ServerConfig{
			RateLimit: config.RateLimitConfig{
				Enabled:           true,
				RequestsPerMinute: 2,
				BurstSize:         2,
			},
		},
	})
	defer srv.Close()

	body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`

	for i := 0; i < 2; i++ {
		resp := gatewayPost(t, srv.URL+"/v1/chat/completions", body, nil)
		raw := readBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("call %d: status = %d, want 200, body=%s", i+1, resp.StatusCode, raw)
		}
		if resp.Header.Get("X-RateLimit-Limit") != "2" {
			t.Errorf("call %d: X-RateLimit-Limit = %q, want 2", i+1, resp.Header.Get("X-RateLimit-Limit"))
		}
		if resp.Header.Get("X-RateLimit-Remaining") == "" {
			t.Errorf("call %d: X-RateLimit-Remaining = empty", i+1)
		}
		if resp.Header.Get("X-RateLimit-Reset") == "" {
			t.Errorf("call %d: X-RateLimit-Reset = empty", i+1)
		}
	}

	// Third call should be rate-limited.
	resp := gatewayPost(t, srv.URL+"/v1/chat/completions", body, nil)
	raw := readBody(t, resp)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("exhausted bucket: status = %d, want 429, body=%s", resp.StatusCode, raw)
	}
}

// ===========================================================================
// Layer 1 — Claude Code client (POST /v1/messages)
// ===========================================================================

// TestClaudeCodeClientNonStreaming verifies that a Claude Code-style request
// (Anthropic Messages API format, x-api-key auth) sent over a real TCP
// connection is correctly normalised and re-serialised in Anthropic wire format.
func TestClaudeCodeClientNonStreaming(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		name: "anthropic",
		capabilities: providers.Capabilities{
			Name:         "anthropic",
			Kind:         providers.KindCloud,
			DefaultModel: "claude-sonnet-4-20250514",
			Models:       []string{"claude-sonnet-4-20250514"},
		},
		response: &types.ChatResponse{
			ID:        "msg_claudecode_1",
			Model:     "claude-sonnet-4-20250514",
			CreatedAt: time.Unix(1_700_000_100, 0).UTC(),
			Choices: []types.ChatChoice{{
				Index:        0,
				Message:      types.Message{Role: "assistant", Content: "The capital of France is Paris."},
				FinishReason: "end_turn",
			}},
			Usage: types.Usage{PromptTokens: 15, CompletionTokens: 8, TotalTokens: 23},
		},
	}
	srv := newGatewayServer(t, provider, config.Config{
		Router: config.RouterConfig{DefaultModel: "claude-sonnet-4-20250514"},
	})
	defer srv.Close()

	// This is exactly what the Anthropic SDK (used by Claude Code) sends.
	reqBody := `{
		"model": "claude-sonnet-4-20250514",
		"max_tokens": 256,
		"system": "You are a helpful geography assistant.",
		"messages": [
			{"role": "user", "content": "What is the capital of France?"}
		]
	}`
	resp := gatewayPost(t, srv.URL+"/v1/messages", reqBody, map[string]string{
		"x-api-key":         "sk-ant-unused",
		"anthropic-version": "2023-06-01",
	})
	raw := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, raw)
	}

	// Content-Type must be application/json for non-streaming
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	// Runtime headers expected on every Anthropic response
	for _, hdr := range []string{
		"X-Request-Id",
		"X-Trace-Id",
		"X-Runtime-Provider",
		"X-Runtime-Model",
	} {
		if resp.Header.Get(hdr) == "" {
			t.Errorf("header %s = empty, want non-empty", hdr)
		}
	}

	// Response body must be valid Anthropic Messages API format
	var msg AnthropicMessagesResponse
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("Unmarshal() error = %v, body=%s", err, raw)
	}
	if msg.Type != "message" {
		t.Errorf("type = %q, want message", msg.Type)
	}
	if msg.Role != "assistant" {
		t.Errorf("role = %q, want assistant", msg.Role)
	}
	if msg.StopReason != "end_turn" {
		t.Errorf("stop_reason = %q, want end_turn", msg.StopReason)
	}
	if msg.Usage.InputTokens != 15 {
		t.Errorf("usage.input_tokens = %d, want 15", msg.Usage.InputTokens)
	}
	if msg.Usage.OutputTokens != 8 {
		t.Errorf("usage.output_tokens = %d, want 8", msg.Usage.OutputTokens)
	}
	if len(msg.Content) != 1 {
		t.Fatalf("content len = %d, want 1", len(msg.Content))
	}
	if msg.Content[0].Type != "text" {
		t.Errorf("content[0].type = %q, want text", msg.Content[0].Type)
	}
	if !strings.Contains(msg.Content[0].Text, "Paris") {
		t.Errorf("content[0].text = %q, want text containing Paris", msg.Content[0].Text)
	}
}

// TestClaudeCodeClientStreamingAnthropicSSEFormat verifies that a streaming
// Claude Code request receives a well-formed Anthropic event-stream:
// message_start → content_block_start → ≥1 content_block_delta →
// content_block_stop → message_delta → message_stop.
func TestClaudeCodeClientStreamingAnthropicSSEFormat(t *testing.T) {
	t.Parallel()

	provider := &sseStreamingProvider{
		fakeProvider: fakeProvider{
			name: "anthropic",
			capabilities: providers.Capabilities{
				Name:         "anthropic",
				Kind:         providers.KindCloud,
				DefaultModel: "claude-sonnet-4-20250514",
				Models:       []string{"claude-sonnet-4-20250514"},
			},
			response: &types.ChatResponse{
				ID:    "msg_stream_1",
				Model: "claude-sonnet-4-20250514",
				Choices: []types.ChatChoice{{
					Message:      types.Message{Role: "assistant", Content: "Hello from Claude"},
					FinishReason: "end_turn",
				}},
				Usage: types.Usage{PromptTokens: 8, CompletionTokens: 3, TotalTokens: 11},
			},
		},
	}
	srv := newGatewayServer(t, provider, config.Config{
		Router: config.RouterConfig{DefaultModel: "claude-sonnet-4-20250514"},
	})
	defer srv.Close()

	reqBody := `{
		"model": "claude-sonnet-4-20250514",
		"max_tokens": 256,
		"stream": true,
		"messages": [{"role": "user", "content": "Say hello."}]
	}`
	resp := gatewayPost(t, srv.URL+"/v1/messages", reqBody, map[string]string{
		"x-api-key":         "sk-ant-unused",
		"anthropic-version": "2023-06-01",
	})

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, b)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	events := readSSEFromResponse(t, resp)

	// Build a map of event names → count for assertion.
	seen := make(map[string]int)
	for _, ev := range events {
		seen[ev.Event]++
	}

	// Anthropic SSE contract: these event types must appear.
	required := []string{
		"message_start",
		"content_block_start",
		"content_block_delta",
		"content_block_stop",
		"message_delta",
		"message_stop",
	}
	for _, name := range required {
		if seen[name] == 0 {
			t.Errorf("Anthropic SSE event %q not found in stream; seen events: %v", name, keys(seen))
		}
	}

	// message_start must contain a valid message object with role=assistant
	for _, ev := range events {
		if ev.Event != "message_start" {
			continue
		}
		var wrapper struct {
			Type    string `json:"type"`
			Message struct {
				ID   string `json:"id"`
				Type string `json:"type"`
				Role string `json:"role"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(ev.Data), &wrapper); err != nil {
			t.Errorf("message_start data parse error: %v, data=%s", err, ev.Data)
			continue
		}
		if wrapper.Message.Role != "assistant" {
			t.Errorf("message_start message.role = %q, want assistant", wrapper.Message.Role)
		}
	}

	// At least one content_block_delta must contain a text_delta
	deltaFound := false
	for _, ev := range events {
		if ev.Event != "content_block_delta" {
			continue
		}
		var wrapper struct {
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(ev.Data), &wrapper); err == nil {
			if wrapper.Delta.Type == "text_delta" && wrapper.Delta.Text != "" {
				deltaFound = true
			}
		}
	}
	if !deltaFound {
		t.Error("no text_delta found in content_block_delta events")
	}
}

// TestClaudeCodeClientMultiTurnConversation verifies that the gateway correctly
// handles a multi-turn messages array (user/assistant/user).
func TestClaudeCodeClientMultiTurnConversation(t *testing.T) {
	t.Parallel()

	var capturedReq types.ChatRequest
	provider := &recordingProvider{
		captured: &capturedReq,
		fakeProvider: fakeProvider{
			name: "anthropic",
			capabilities: providers.Capabilities{
				Name:         "anthropic",
				Kind:         providers.KindCloud,
				DefaultModel: "claude-sonnet-4-20250514",
				Models:       []string{"claude-sonnet-4-20250514"},
			},
			response: &types.ChatResponse{
				ID:    "msg_multiturn",
				Model: "claude-sonnet-4-20250514",
				Choices: []types.ChatChoice{{
					Message:      types.Message{Role: "assistant", Content: "Nice to meet you too!"},
					FinishReason: "end_turn",
				}},
				Usage: types.Usage{PromptTokens: 20, CompletionTokens: 6, TotalTokens: 26},
			},
		},
	}
	srv := newGatewayServer(t, provider, config.Config{
		Router: config.RouterConfig{DefaultModel: "claude-sonnet-4-20250514"},
	})
	defer srv.Close()

	reqBody := `{
		"model": "claude-sonnet-4-20250514",
		"max_tokens": 128,
		"messages": [
			{"role": "user",      "content": "Hello!"},
			{"role": "assistant", "content": "Hi there!"},
			{"role": "user",      "content": "Nice to meet you."}
		]
	}`
	resp := gatewayPost(t, srv.URL+"/v1/messages", reqBody, map[string]string{
		"x-api-key":         "sk-ant-unused",
		"anthropic-version": "2023-06-01",
	})
	raw := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, raw)
	}

	// The provider should have received all three turns.
	if len(capturedReq.Messages) != 3 {
		t.Errorf("provider received %d messages, want 3", len(capturedReq.Messages))
	}
	if capturedReq.Messages[0].Role != "user" {
		t.Errorf("message[0].role = %q, want user", capturedReq.Messages[0].Role)
	}
	if capturedReq.Messages[1].Role != "assistant" {
		t.Errorf("message[1].role = %q, want assistant", capturedReq.Messages[1].Role)
	}
	if capturedReq.Messages[2].Role != "user" {
		t.Errorf("message[2].role = %q, want user", capturedReq.Messages[2].Role)
	}
}

// TestClaudeCodeClientSystemPromptPassedThrough verifies that the Anthropic
// system field is correctly normalised into the internal request's system
// prompt and visible to the provider.
func TestClaudeCodeClientSystemPromptPassedThrough(t *testing.T) {
	t.Parallel()

	var capturedReq types.ChatRequest
	provider := &recordingProvider{
		captured: &capturedReq,
		fakeProvider: fakeProvider{
			name: "anthropic",
			capabilities: providers.Capabilities{
				Name:         "anthropic",
				Kind:         providers.KindCloud,
				DefaultModel: "claude-sonnet-4-20250514",
				Models:       []string{"claude-sonnet-4-20250514"},
			},
			response: &types.ChatResponse{
				ID:    "msg_sys",
				Model: "claude-sonnet-4-20250514",
				Choices: []types.ChatChoice{{
					Message:      types.Message{Role: "assistant", Content: "Bonjour!"},
					FinishReason: "end_turn",
				}},
				Usage: types.Usage{PromptTokens: 12, CompletionTokens: 2, TotalTokens: 14},
			},
		},
	}
	srv := newGatewayServer(t, provider, config.Config{
		Router: config.RouterConfig{DefaultModel: "claude-sonnet-4-20250514"},
	})
	defer srv.Close()

	reqBody := `{
		"model": "claude-sonnet-4-20250514",
		"max_tokens": 64,
		"system": "You only respond in French.",
		"messages": [{"role": "user", "content": "Hello."}]
	}`
	resp := gatewayPost(t, srv.URL+"/v1/messages", reqBody, map[string]string{
		"x-api-key": "sk-ant-unused",
	})
	raw := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body=%s", resp.StatusCode, raw)
	}
	// The normaliser injects the Anthropic system field as a role:"system"
	// message prepended to the messages array.
	systemFound := ""
	for _, m := range capturedReq.Messages {
		if m.Role == "system" {
			systemFound = m.Content
			break
		}
	}
	if systemFound == "" {
		t.Error("system prompt was not forwarded to provider as a system message")
	}
	if !strings.Contains(systemFound, "French") {
		t.Errorf("system message content = %q, want text containing 'French'", systemFound)
	}
}

// TestClaudeCodeClientToolUseResponseShape verifies that when the underlying
// provider returns a tool-call, the gateway serialises it as an Anthropic
// tool_use content block.
func TestClaudeCodeClientToolUseResponseShape(t *testing.T) {
	t.Parallel()

	// The internal type uses ToolCalls on the Choice message.
	provider := &fakeProvider{
		name: "anthropic",
		capabilities: providers.Capabilities{
			Name:         "anthropic",
			Kind:         providers.KindCloud,
			DefaultModel: "claude-sonnet-4-20250514",
			Models:       []string{"claude-sonnet-4-20250514"},
		},
		response: &types.ChatResponse{
			ID:    "msg_tooluse",
			Model: "claude-sonnet-4-20250514",
			Choices: []types.ChatChoice{{
				Message: types.Message{
					Role:    "assistant",
					Content: "",
					ToolCalls: []types.ToolCall{{
						ID:   "toolu_01",
						Type: "function",
						Function: types.ToolCallFunction{
							Name:      "get_weather",
							Arguments: `{"location":"Paris"}`,
						},
					}},
				},
				FinishReason: "tool_use",
			}},
			Usage: types.Usage{PromptTokens: 25, CompletionTokens: 10, TotalTokens: 35},
		},
	}
	srv := newGatewayServer(t, provider, config.Config{
		Router: config.RouterConfig{DefaultModel: "claude-sonnet-4-20250514"},
	})
	defer srv.Close()

	reqBody := `{
		"model": "claude-sonnet-4-20250514",
		"max_tokens": 256,
		"tools": [{
			"name": "get_weather",
			"description": "Get the current weather.",
			"input_schema": {"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}
		}],
		"messages": [{"role": "user", "content": "What is the weather in Paris?"}]
	}`
	resp := gatewayPost(t, srv.URL+"/v1/messages", reqBody, map[string]string{
		"x-api-key": "sk-ant-unused",
	})
	raw := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body=%s", resp.StatusCode, raw)
	}

	var msg AnthropicMessagesResponse
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("Unmarshal() error = %v, body=%s", err, raw)
	}
	if msg.StopReason != "tool_use" {
		t.Errorf("stop_reason = %q, want tool_use", msg.StopReason)
	}

	// The response must contain a tool_use content block.
	foundToolUse := false
	for _, block := range msg.Content {
		if block.Type == "tool_use" {
			foundToolUse = true
			if block.Name != "get_weather" {
				t.Errorf("tool_use block name = %q, want get_weather", block.Name)
			}
		}
	}
	if !foundToolUse {
		t.Errorf("no tool_use content block found; got blocks: %+v", msg.Content)
	}
}

// ===========================================================================
// Layer 2 — Fake upstream server: real providers.OpenAICompatibleProvider
// ===========================================================================

// chatCompletionsRoute is the shared scaffold for fake OpenAI upstreams.
// It handles GET /…/models with a canned model list, rejects anything
// that isn't POST /…/chat/completions, and delegates the response body
// to handle.
//
// All three test-helper constructors below — non-streaming success,
// non-streaming error, streaming SSE — wrap this. Without the
// extraction the path-suffix + method-guard prelude was repeated three
// times verbatim.
func chatCompletionsRoute(handle func(w http.ResponseWriter, r *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models") {
			writeFakeOpenAIModels(w)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		handle(w, r)
	}
}

// newFakeOpenAIUpstream starts an httptest.Server that responds to
// POST /v1/chat/completions with a canned OpenAI chat completion response.
// The caller must defer upstream.Close().
func newFakeOpenAIUpstream(t *testing.T, responseBody string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(chatCompletionsRoute(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, responseBody)
	}))
}

func newFakeOpenAIErrorUpstream(t *testing.T, status int, responseBody string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(chatCompletionsRoute(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, responseBody)
	}))
}

// newFakeOpenAIStreamingUpstream starts an httptest.Server that streams
// well-formed OpenAI SSE chunks for a given content string.
func newFakeOpenAIStreamingUpstream(t *testing.T, id, model, content string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(chatCompletionsRoute(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, _ := w.(http.Flusher)

		writeChunk := func(data string) {
			fmt.Fprintf(w, "data: %s\n\n", data)
			if flusher != nil {
				flusher.Flush()
			}
		}

		writeChunk(fmt.Sprintf(
			`{"id":%q,"object":"chat.completion.chunk","model":%q,"choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
			id, model,
		))
		for _, word := range strings.Fields(content) {
			writeChunk(fmt.Sprintf(
				`{"id":%q,"object":"chat.completion.chunk","model":%q,"choices":[{"index":0,"delta":{"content":%q},"finish_reason":null}]}`,
				id, model, word+" ",
			))
		}
		writeChunk(fmt.Sprintf(
			`{"id":%q,"object":"chat.completion.chunk","model":%q,"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			id, model,
		))
		writeChunk("[DONE]")
	}))
}

func writeFakeOpenAIModels(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"object":"list","data":[{"id":"gpt-4o-mini","object":"model","owned_by":"test"},{"id":"claude-sonnet-4-20250514","object":"model","owned_by":"test"}]}`)
}

// newGatewayServerWithRealProvider builds a gateway httptest.Server wired to a
// real providers.OpenAICompatibleProvider pointing at upstreamURL.
func newGatewayServerWithRealProvider(t *testing.T, upstreamURL, providerName, defaultModel string) *httptest.Server {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	providerCfg := config.OpenAICompatibleProviderConfig{
		Name:         providerName,
		Kind:         "cloud",
		BaseURL:      upstreamURL,
		DefaultModel: defaultModel,
		APIKey:       "fake-upstream-key",
		Enabled:      true,
	}
	realProvider := providers.NewOpenAICompatibleProvider(providerCfg, logger)

	registry := providers.NewRegistry(realProvider)
	healthTracker := providers.NewMemoryHealthTracker(0, 0)
	providerCatalog := catalog.NewRegistryCatalog(registry, healthTracker)
	usageStore := governor.NewMemoryUsageStore()

	governorCfg := config.GovernorConfig{
		MaxPromptTokens: 64_000,
		UsageBackend:    "memory",
		UsageKey:        "global",
		UsageScope:      "global",
	}
	svc := gateway.NewService(gateway.Dependencies{
		Logger:    logger,
		Router:    router.NewRuleRouter(defaultModel, providerCatalog),
		Catalog:   providerCatalog,
		Governor:  governor.NewStaticGovernor(governorCfg, usageStore, usageStore),
		Providers: registry,
		Tracer:    profiler.NewInMemoryTracer(nil),
		Metrics:   telemetry.NewMetrics(),
	})

	cfg := config.Config{
		Router: config.RouterConfig{DefaultModel: defaultModel},
	}
	return httptest.NewServer(NewServer(logger, NewHandler(cfg, logger, svc, nil, nil, nil)))
}

// TestGatewayViaRealProviderNonStreaming wires a real OpenAICompatibleProvider
// to a fake upstream HTTP server and sends a Codex-style non-streaming request.
// This exercises the provider's own HTTP client, header forwarding, and
// response deserialisation end-to-end.
func TestGatewayViaRealProviderNonStreaming(t *testing.T) {
	t.Parallel()

	// Fake upstream returns a well-formed OpenAI completion.
	upstreamResp := `{
		"id": "chatcmpl-real-1",
		"object": "chat.completion",
		"model": "gpt-4o-mini",
		"choices": [{
			"index": 0,
			"message": {"role": "assistant", "content": "The answer is 42."},
			"finish_reason": "stop"
		}],
		"usage": {"prompt_tokens": 12, "completion_tokens": 5, "total_tokens": 17}
	}`
	upstream := newFakeOpenAIUpstream(t, upstreamResp)
	defer upstream.Close()

	gatewaySrv := newGatewayServerWithRealProvider(t, upstream.URL, "openai", "gpt-4o-mini")
	defer gatewaySrv.Close()

	body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"What is the answer?"}]}`
	resp := gatewayPost(t, gatewaySrv.URL+"/v1/chat/completions", body, nil)
	raw := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, raw)
	}

	var completion OpenAIChatCompletionResponse
	if err := json.Unmarshal(raw, &completion); err != nil {
		t.Fatalf("Unmarshal() error = %v, body=%s", err, raw)
	}
	if completion.Object != "chat.completion" {
		t.Errorf("object = %q, want chat.completion", completion.Object)
	}
	if len(completion.Choices) == 0 {
		t.Fatal("choices is empty")
	}
	if got := completion.Choices[0].Message.Content.AsString(); !strings.Contains(got, "42") {
		t.Errorf("content = %q, want text containing 42", got)
	}

	// Gateway must set X-Runtime-Provider even when using the real provider.
	if got := resp.Header.Get("X-Runtime-Provider"); got != "openai" {
		t.Errorf("X-Runtime-Provider = %q, want openai", got)
	}
	if resp.Header.Get("X-Trace-Id") == "" {
		t.Error("X-Trace-Id = empty, want trace id")
	}
}

// TestGatewayViaRealProviderStreamingCodexClient wires a real
// OpenAICompatibleProvider to a streaming fake upstream and sends a Codex
// streaming request.  It asserts that SSE chunks are forwarded intact,
// including "data: [DONE]".
func TestGatewayViaRealProviderStreamingCodexClient(t *testing.T) {
	t.Parallel()

	upstream := newFakeOpenAIStreamingUpstream(t, "chatcmpl-real-stream", "gpt-4o-mini", "Paris is lovely")
	defer upstream.Close()

	gatewaySrv := newGatewayServerWithRealProvider(t, upstream.URL, "openai", "gpt-4o-mini")
	defer gatewaySrv.Close()

	body := `{"model":"gpt-4o-mini","stream":true,"messages":[{"role":"user","content":"Tell me about Paris."}]}`
	resp := gatewayPost(t, gatewaySrv.URL+"/v1/chat/completions", body, nil)

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("status = %d, body=%s", resp.StatusCode, b)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	events := readSSEFromResponse(t, resp)
	if len(events) == 0 {
		t.Fatal("no SSE events received from gateway")
	}

	foundDone := false
	gotContent := false
	for _, ev := range events {
		if ev.Data == "[DONE]" {
			foundDone = true
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(ev.Data), &chunk); err == nil {
			for _, c := range chunk.Choices {
				if strings.TrimSpace(c.Delta.Content) != "" {
					gotContent = true
				}
			}
		}
	}
	if !foundDone {
		t.Error("SSE stream did not end with data: [DONE]")
	}
	if !gotContent {
		t.Error("no content delta found in forwarded SSE stream")
	}
}

// TestGatewayViaRealProviderClaudeCodeClient wires a real
// OpenAICompatibleProvider to a fake OpenAI upstream and sends a Claude Code
// request to POST /v1/messages.  It asserts the Anthropic response shape is
// preserved end-to-end through the full normalisation + provider + serialisation
// path.
func TestGatewayViaRealProviderClaudeCodeClient(t *testing.T) {
	t.Parallel()

	upstreamResp := `{
		"id": "chatcmpl-real-cc",
		"object": "chat.completion",
		"model": "claude-sonnet-4-20250514",
		"choices": [{
			"index": 0,
			"message": {"role": "assistant", "content": "Bonjour!"},
			"finish_reason": "stop"
		}],
		"usage": {"prompt_tokens": 18, "completion_tokens": 3, "total_tokens": 21}
	}`
	upstream := newFakeOpenAIUpstream(t, upstreamResp)
	defer upstream.Close()

	gatewaySrv := newGatewayServerWithRealProvider(t, upstream.URL, "anthropic", "claude-sonnet-4-20250514")
	defer gatewaySrv.Close()

	reqBody := `{
		"model": "claude-sonnet-4-20250514",
		"max_tokens": 128,
		"system": "Respond only in French.",
		"messages": [{"role": "user", "content": "Hello."}]
	}`
	resp := gatewayPost(t, gatewaySrv.URL+"/v1/messages", reqBody, map[string]string{
		"x-api-key":         "sk-ant-unused",
		"anthropic-version": "2023-06-01",
	})
	raw := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, raw)
	}

	var msg AnthropicMessagesResponse
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("Unmarshal() error = %v, body=%s", err, raw)
	}
	if msg.Type != "message" {
		t.Errorf("type = %q, want message", msg.Type)
	}
	if msg.Role != "assistant" {
		t.Errorf("role = %q, want assistant", msg.Role)
	}
	// stop_reason "stop" → "end_turn" in Anthropic shape
	if msg.StopReason != "end_turn" {
		t.Errorf("stop_reason = %q, want end_turn", msg.StopReason)
	}
	if len(msg.Content) == 0 || msg.Content[0].Text == "" {
		t.Errorf("content = %+v, want non-empty text block", msg.Content)
	}
}

func TestGatewayViaRealAnthropicProviderPreservesImageSources(t *testing.T) {
	t.Parallel()

	const model = "claude-sonnet-4-20250514"
	var captured map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/v1/models"):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":[{"id":%q}]}`, model)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/v1/messages"):
			if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"id":"msg_image_roundtrip","type":"message","role":"assistant","model":%q,"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":20,"output_tokens":1}}`, model)
		default:
			http.Error(w, "unexpected upstream route", http.StatusNotFound)
		}
	}))
	defer upstream.Close()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	realProvider := providers.NewAnthropicProvider(config.OpenAICompatibleProviderConfig{
		Name:                   "anthropic",
		Kind:                   "cloud",
		Protocol:               "anthropic",
		BaseURL:                upstream.URL,
		APIKey:                 "fake-upstream-key",
		DefaultModel:           model,
		Enabled:                true,
		AnthropicCacheDisabled: true,
	}, logger)
	gatewaySrv := newGatewayServer(t, realProvider, config.Config{
		Router: config.RouterConfig{DefaultModel: model},
	})
	defer gatewaySrv.Close()

	reqBody := `{
		"model":"claude-sonnet-4-20250514",
		"max_tokens":128,
		"messages":[
			{"role":"user","content":[
				{"type":"text","text":"Inspect this image."},
				{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AQID"},"cache_control":{"type":"ephemeral"}}
			]},
			{"role":"assistant","content":[
				{"type":"tool_use","id":"toolu_image","name":"capture_frame","input":{}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"toolu_image","content":[
					{"type":"text","text":"Captured frame"},
					{"type":"image","source":{"type":"url","url":"https://images.example.test/tool-output.webp"},"cache_control":{"type":"ephemeral","ttl":"5m"}}
				]}
			]}
		]
	}`
	resp := gatewayPost(t, gatewaySrv.URL+"/v1/messages", reqBody, map[string]string{
		"x-api-key":         "sk-ant-unused",
		"anthropic-version": "2023-06-01",
	})
	raw := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, raw)
	}

	messages, ok := captured["messages"].([]any)
	if !ok || len(messages) != 3 {
		t.Fatalf("upstream messages = %#v, want three messages", captured["messages"])
	}
	first, ok := messages[0].(map[string]any)
	if !ok {
		t.Fatalf("upstream messages[0] = %#v, want object", messages[0])
	}
	firstContent, ok := first["content"].([]any)
	if !ok || len(firstContent) != 2 {
		t.Fatalf("upstream messages[0].content = %#v, want text and image", first["content"])
	}
	base64Image := requireGatewayClientObject(t, firstContent[1], "messages[0].content[1]")
	base64Source := requireGatewayClientObject(t, base64Image["source"], "messages[0].content[1].source")
	if base64Image["type"] != "image" || base64Source["type"] != "base64" || base64Source["media_type"] != "image/png" || base64Source["data"] != "AQID" {
		t.Fatalf("base64 image = %#v, want preserved Anthropic image source", base64Image)
	}
	base64Cache := requireGatewayClientObject(t, base64Image["cache_control"], "messages[0].content[1].cache_control")
	if base64Cache["type"] != "ephemeral" {
		t.Fatalf("base64 image cache_control = %#v, want caller marker", base64Cache)
	}

	toolResultMessage := requireGatewayClientObject(t, messages[2], "messages[2]")
	toolResultContent, ok := toolResultMessage["content"].([]any)
	if !ok || len(toolResultContent) != 1 {
		t.Fatalf("upstream messages[2].content = %#v, want one tool_result", toolResultMessage["content"])
	}
	toolResult := requireGatewayClientObject(t, toolResultContent[0], "messages[2].content[0]")
	nested, ok := toolResult["content"].([]any)
	if !ok || len(nested) != 2 {
		t.Fatalf("tool_result.content = %#v, want text and image", toolResult["content"])
	}
	urlImage := requireGatewayClientObject(t, nested[1], "messages[2].content[0].content[1]")
	urlSource := requireGatewayClientObject(t, urlImage["source"], "messages[2].content[0].content[1].source")
	if urlImage["type"] != "image" || urlSource["type"] != "url" || urlSource["url"] != "https://images.example.test/tool-output.webp" {
		t.Fatalf("tool-result image = %#v, want preserved URL source", urlImage)
	}
	urlCache := requireGatewayClientObject(t, urlImage["cache_control"], "messages[2].content[0].content[1].cache_control")
	if urlCache["type"] != "ephemeral" || urlCache["ttl"] != "5m" {
		t.Fatalf("tool-result image cache_control = %#v, want caller marker and ttl", urlCache)
	}

	// The OpenAI compatibility endpoint accepts URI schemes
	// case-insensitively. Routing that accepted DATA: image to an Anthropic
	// provider must still translate it into Anthropic's base64 source shape,
	// rather than forwarding the pseudo-URL as source.type=url.
	openAIReqBody := `{
		"model":"claude-sonnet-4-20250514",
		"messages":[{"role":"user","content":[
			{"type":"text","text":"Inspect this image."},
			{"type":"image_url","image_url":{"url":"DATA:image/png;base64,AQID"}}
		]}]
	}`
	resp = gatewayPost(t, gatewaySrv.URL+"/v1/chat/completions", openAIReqBody, nil)
	raw = readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("OpenAI compatibility status = %d, want 200, body=%s", resp.StatusCode, raw)
	}

	messages, ok = captured["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("cross-provider messages = %#v, want one message", captured["messages"])
	}
	first = requireGatewayClientObject(t, messages[0], "cross-provider messages[0]")
	firstContent, ok = first["content"].([]any)
	if !ok || len(firstContent) != 2 {
		t.Fatalf("cross-provider messages[0].content = %#v, want text and image", first["content"])
	}
	base64Image = requireGatewayClientObject(t, firstContent[1], "cross-provider messages[0].content[1]")
	base64Source = requireGatewayClientObject(t, base64Image["source"], "cross-provider messages[0].content[1].source")
	if base64Source["type"] != "base64" || base64Source["media_type"] != "image/png" || base64Source["data"] != "AQID" {
		t.Fatalf("cross-provider image source = %#v, want parsed base64 DATA: URI", base64Source)
	}
	if _, ok := base64Source["url"]; ok {
		t.Fatalf("cross-provider image source = %#v, want no pseudo-URL", base64Source)
	}
}

func requireGatewayClientObject(t *testing.T, value any, path string) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s = %#v, want object", path, value)
	}
	return object
}

// TestGatewayViaRealProviderUpstreamUnavailable verifies that when the upstream
// is unreachable the gateway returns a 5xx error rather than hanging or
// panicking.
func TestGatewayViaRealProviderUpstreamUnavailable(t *testing.T) {
	t.Parallel()

	// Point provider at an address that is not listening.
	gatewaySrv := newGatewayServerWithRealProvider(t, "http://127.0.0.1:1", "openai", "gpt-4o-mini")
	defer gatewaySrv.Close()

	body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`

	// Timeout the client so the test doesn't hang if backoff is unexpectedly long.
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		gatewaySrv.URL+"/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 500 || resp.StatusCode > 599 {
		b, _ := io.ReadAll(resp.Body)
		t.Errorf("status = %d, want 5xx for unreachable upstream, body=%s", resp.StatusCode, b)
	}
}

func TestGatewayViaRealProviderAuthFailureContract(t *testing.T) {
	t.Parallel()

	upstream := newFakeOpenAIErrorUpstream(t, http.StatusUnauthorized, `{
		"error": {
			"message": "Incorrect API key provided",
			"type": "invalid_request_error",
			"code": "invalid_api_key"
		}
	}`)
	defer upstream.Close()

	gatewaySrv := newGatewayServerWithRealProvider(t, upstream.URL, "openai", "gpt-4o-mini")
	defer gatewaySrv.Close()

	body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`
	resp := gatewayPost(t, gatewaySrv.URL+"/v1/chat/completions", body, nil)
	raw := readBody(t, resp)

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502, body=%s", resp.StatusCode, raw)
	}
	assertOpenAIErrorType(t, raw, errCodeProviderAuthFailed)
	if resp.Header.Get("X-Request-Id") == "" {
		t.Error("X-Request-Id = empty")
	}
}

func TestCodexClientUnsupportedModelContract(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		name:         "openai",
		defaultModel: "gpt-4o-mini",
		capabilities: providers.Capabilities{
			Name:         "openai",
			Kind:         providers.KindCloud,
			DefaultModel: "gpt-4o-mini",
			Models:       []string{"gpt-4o-mini"},
		},
		response: &types.ChatResponse{},
	}
	srv := newGatewayServer(t, provider, config.Config{})
	defer srv.Close()

	body := `{"model":"does-not-exist","messages":[{"role":"user","content":"hi"}]}`
	resp := gatewayPost(t, srv.URL+"/v1/chat/completions", body, nil)
	raw := readBody(t, resp)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", resp.StatusCode, raw)
	}
	assertOpenAIErrorType(t, raw, errCodeUnsupportedModel)
	if provider.CallCount() != 0 {
		t.Fatalf("provider call count = %d, want 0 because router should reject unsupported model", provider.CallCount())
	}
} // ===========================================================================
// Retry, failover, betas, and trace endpoint
// ===========================================================================

// TestCodexClientRetryReportsAttemptHeaders verifies that when the primary
// provider returns a retryable 503 the gateway retries, succeeds on the second
// attempt, and exposes X-Runtime-Attempts=2 / X-Runtime-Retries=1.
func TestCodexClientRetryReportsAttemptHeaders(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		name: "openai",
		errSequence: []error{
			&providers.UpstreamError{StatusCode: http.StatusServiceUnavailable, Message: "blip", Type: "server_error"},
			nil,
		},
		response: &types.ChatResponse{
			ID:    "chatcmpl-retry-int",
			Model: "gpt-4o-mini",
			Choices: []types.ChatChoice{{
				Message:      types.Message{Role: "assistant", Content: "recovered"},
				FinishReason: "stop",
			}},
			Usage: types.Usage{PromptTokens: 8, CompletionTokens: 2, TotalTokens: 10},
		},
	}
	srv := newGatewayServer(t, provider, config.Config{
		Provider: config.ProviderConfig{
			MaxAttempts:     2,
			RetryBackoff:    time.Millisecond,
			FailoverEnabled: true,
		},
	})
	defer srv.Close()

	body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`
	resp := gatewayPost(t, srv.URL+"/v1/chat/completions", body, nil)
	raw := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, raw)
	}
	if got := resp.Header.Get("X-Runtime-Attempts"); got != "2" {
		t.Errorf("X-Runtime-Attempts = %q, want 2", got)
	}
	if got := resp.Header.Get("X-Runtime-Retries"); got != "1" {
		t.Errorf("X-Runtime-Retries = %q, want 1", got)
	}
	if got := resp.Header.Get("X-Runtime-Fallback-From"); got != "" {
		t.Errorf("X-Runtime-Fallback-From = %q, want empty (retry, not failover)", got)
	}
}

// TestCodexClientFailoverReportsHeaders verifies that when the primary local
// provider fails, the gateway fails over to the cloud provider, and
// X-Runtime-Fallback-From names the failed provider.
func TestCodexClientFailoverReportsHeaders(t *testing.T) {
	t.Parallel()

	localProvider := &fakeProvider{
		name:         "ollama",
		defaultModel: "llama3.1:8b",
		capabilities: providers.Capabilities{
			Name:         "ollama",
			Kind:         providers.KindLocal,
			DefaultModel: "llama3.1:8b",
			Models:       []string{"llama3.1:8b"},
		},
		errSequence: []error{
			&providers.UpstreamError{StatusCode: http.StatusBadGateway, Message: "unavailable", Type: "server_error"},
		},
		response: &types.ChatResponse{
			ID:    "chatcmpl-local-fail",
			Model: "llama3.1:8b",
			Choices: []types.ChatChoice{{
				Message:      types.Message{Role: "assistant", Content: "local"},
				FinishReason: "stop",
			}},
			Usage: types.Usage{PromptTokens: 5, CompletionTokens: 1, TotalTokens: 6},
		},
	}
	cloudProvider := &fakeProvider{
		name:         "openai",
		defaultModel: "gpt-4o-mini",
		capabilities: providers.Capabilities{
			Name:         "openai",
			Kind:         providers.KindCloud,
			DefaultModel: "gpt-4o-mini",
			Models:       []string{"gpt-4o-mini"},
		},
		response: &types.ChatResponse{
			ID:    "chatcmpl-cloud-fallback",
			Model: "gpt-4o-mini",
			Choices: []types.ChatChoice{{
				Message:      types.Message{Role: "assistant", Content: "cloud fallback"},
				FinishReason: "stop",
			}},
			Usage: types.Usage{PromptTokens: 8, CompletionTokens: 3, TotalTokens: 11},
		},
	}

	srv := httptest.NewServer(newTestHTTPHandlerForProviders(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		[]providers.Provider{localProvider, cloudProvider},
		config.Config{
			Provider: config.ProviderConfig{
				MaxAttempts:     1,
				RetryBackoff:    time.Millisecond,
				FailoverEnabled: true,
			},
			// DefaultModel not set so newTestHTTPHandlerForProviders uses
			// items[0].DefaultModel() = "llama3.1:8b".  The request omits model
			// so the router uses defaultCandidates, picks ollama first (it
			// fails), then falls over to openai.
		},
	))
	defer srv.Close()

	// No "model" field → default routing so the router picks ollama first.
	body := `{"messages":[{"role":"user","content":"hi"}]}`
	resp := gatewayPost(t, srv.URL+"/v1/chat/completions", body, nil)
	raw := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, raw)
	}
	if got := resp.Header.Get("X-Runtime-Fallback-From"); got != "ollama" {
		t.Errorf("X-Runtime-Fallback-From = %q, want ollama", got)
	}
	if got := resp.Header.Get("X-Runtime-Provider"); got != "openai" {
		t.Errorf("X-Runtime-Provider = %q, want openai (the fallback)", got)
	}
	if got := resp.Header.Get("X-Runtime-Attempts"); got != "2" {
		t.Errorf("X-Runtime-Attempts = %q, want 2", got)
	}
}

func TestCompatibilityImageRequestsDoNotFailOverAcrossProviders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		body string
	}{
		{
			name: "OpenAI compatibility",
			path: "/v1/chat/completions",
			body: `{"model":"shared-vision","messages":[{"role":"user","content":[
				{"type":"text","text":"describe"},
				{"type":"image_url","image_url":{"url":"https://images.example.test/private.png"}}
			]}]}`,
		},
		{
			name: "Anthropic compatibility",
			path: "/v1/messages",
			body: `{"model":"shared-vision","max_tokens":64,"messages":[{"role":"user","content":[
				{"type":"text","text":"describe"},
				{"type":"image","source":{"type":"url","url":"https://images.example.test/private.png"}}
			]}]}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			primary := &fakeProvider{
				name:         "cloud-vision",
				defaultModel: "shared-vision",
				capabilities: providers.Capabilities{
					Name:         "cloud-vision",
					Kind:         providers.KindCloud,
					DefaultModel: "shared-vision",
					Models:       []string{"shared-vision"},
				},
				err: &providers.UpstreamError{
					StatusCode: http.StatusBadGateway,
					Message:    "primary unavailable",
					Type:       "server_error",
				},
			}
			secondary := &fakeProvider{
				name:         "local-vision",
				defaultModel: "shared-vision",
				capabilities: providers.Capabilities{
					Name:         "local-vision",
					Kind:         providers.KindLocal,
					DefaultModel: "shared-vision",
					Models:       []string{"shared-vision"},
				},
				response: &types.ChatResponse{
					ID:    "chatcmpl-should-not-run",
					Model: "shared-vision",
					Choices: []types.ChatChoice{{
						Message:      types.Message{Role: "assistant", Content: "unexpected fallback"},
						FinishReason: "stop",
					}},
				},
			}
			server := httptest.NewServer(newTestHTTPHandlerForProviders(
				slog.New(slog.NewJSONHandler(io.Discard, nil)),
				[]providers.Provider{primary, secondary},
				config.Config{Provider: config.ProviderConfig{
					MaxAttempts:     1,
					RetryBackoff:    time.Millisecond,
					FailoverEnabled: true,
				}},
			))
			defer server.Close()

			response := gatewayPost(t, server.URL+test.path, test.body, nil)
			body := readBody(t, response)
			if response.StatusCode == http.StatusOK {
				t.Fatalf("status = 200, want primary failure without cross-provider fallback; body=%s", body)
			}
			if primary.CallCount() != 1 {
				t.Fatalf("primary calls = %d, want 1", primary.CallCount())
			}
			if secondary.CallCount() != 0 {
				t.Fatalf("secondary calls = %d, want 0", secondary.CallCount())
			}
		})
	}
}

// TestClaudeCodeClientBetasForwardedToProvider verifies that the Anthropic
// "betas" field is normalised into the internal request and forwarded to the
// provider unchanged.
func TestClaudeCodeClientBetasForwardedToProvider(t *testing.T) {
	t.Parallel()

	var capturedReq types.ChatRequest
	provider := &recordingProvider{
		captured: &capturedReq,
		fakeProvider: fakeProvider{
			name: "anthropic",
			capabilities: providers.Capabilities{
				Name:         "anthropic",
				Kind:         providers.KindCloud,
				DefaultModel: "claude-sonnet-4-20250514",
				Models:       []string{"claude-sonnet-4-20250514"},
			},
			response: &types.ChatResponse{
				ID:    "msg_betas",
				Model: "claude-sonnet-4-20250514",
				Choices: []types.ChatChoice{{
					Message:      types.Message{Role: "assistant", Content: "thinking..."},
					FinishReason: "end_turn",
				}},
				Usage: types.Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12},
			},
		},
	}
	srv := newGatewayServer(t, provider, config.Config{
		Router: config.RouterConfig{DefaultModel: "claude-sonnet-4-20250514"},
	})
	defer srv.Close()

	reqBody := `{
		"model": "claude-sonnet-4-20250514",
		"max_tokens": 256,
		"betas": ["interleaved-thinking-2025-02-19"],
		"messages": [{"role": "user", "content": "Think step by step."}]
	}`
	resp := gatewayPost(t, srv.URL+"/v1/messages", reqBody, map[string]string{
		"x-api-key":         "sk-ant-unused",
		"anthropic-version": "2023-06-01",
	})
	raw := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body=%s", resp.StatusCode, raw)
	}

	foundBeta := false
	for _, b := range capturedReq.Betas {
		if b == "interleaved-thinking-2025-02-19" {
			foundBeta = true
		}
	}
	if !foundBeta {
		t.Errorf("beta not forwarded to provider; capturedReq.Betas = %v", capturedReq.Betas)
	}
}

// TestGatewayTraceEndpointShowsRequestSpans verifies that after a gateway
// request the trace endpoint returns the request's span tree, including the
// root gateway.request span with a service.name attribute and a
// gateway.response child span.
func TestGatewayTraceEndpointShowsRequestSpans(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		name: "openai",
		response: &types.ChatResponse{
			ID:    "chatcmpl-trace-int",
			Model: "gpt-4o-mini",
			Choices: []types.ChatChoice{{
				Message:      types.Message{Role: "assistant", Content: "hello"},
				FinishReason: "stop",
			}},
			Usage: types.Usage{PromptTokens: 6, CompletionTokens: 1, TotalTokens: 7},
		},
	}
	srv := newGatewayServer(t, provider, config.Config{})
	defer srv.Close()

	// Send a chat request and grab the request id from the response header.
	chatBody := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`
	chatResp := gatewayPost(t, srv.URL+"/v1/chat/completions", chatBody, nil)
	requestID := chatResp.Header.Get("X-Request-Id")
	readBody(t, chatResp) // drain
	if requestID == "" {
		t.Fatal("X-Request-Id = empty, cannot query trace")
	}

	// Query the trace endpoint.
	traceResp, err := http.Get(srv.URL + "/hecate/v1/traces?request_id=" + requestID)
	if err != nil {
		t.Fatalf("GET /hecate/v1/traces error = %v", err)
	}
	raw := readBody(t, traceResp)
	if traceResp.StatusCode != http.StatusOK {
		t.Fatalf("trace status = %d, body=%s", traceResp.StatusCode, raw)
	}

	var trace TraceResponse
	if err := json.Unmarshal(raw, &trace); err != nil {
		t.Fatalf("Unmarshal() error = %v, body=%s", err, raw)
	}
	if trace.Object != "trace" {
		t.Errorf("object = %q, want trace", trace.Object)
	}
	if trace.Data.RequestID == "" {
		t.Error("trace.data.request_id = empty")
	}
	if trace.Data.TraceID == "" {
		t.Error("trace.data.trace_id = empty")
	}
	if len(trace.Data.Spans) == 0 {
		t.Fatal("spans = empty, want span list")
	}

	// Root span must be gateway.request.
	if trace.Data.Spans[0].Name != "gateway.request" {
		t.Errorf("spans[0].name = %q, want gateway.request", trace.Data.Spans[0].Name)
	}

	// gateway.response span must be present.
	var foundResponse bool
	for _, s := range trace.Data.Spans {
		if s.Name == "gateway.response" {
			foundResponse = true
		}
	}
	if !foundResponse {
		names := make([]string, len(trace.Data.Spans))
		for i, s := range trace.Data.Spans {
			names[i] = s.Name
		}
		t.Errorf("gateway.response span missing; got: %v", names)
	}

	// Route info must name the provider.
	if trace.Data.Route.FinalProvider != "openai" {
		t.Errorf("route.final_provider = %q, want openai", trace.Data.Route.FinalProvider)
	}
}

func TestGatewayTraceEndpointShowsPolicyDeniedRouteCandidate(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		name:         "openai",
		defaultModel: "gpt-4o-mini",
		capabilities: providers.Capabilities{
			Name:         "openai",
			Kind:         providers.KindCloud,
			DefaultModel: "gpt-4o-mini",
			Models:       []string{"gpt-4o-mini"},
		},
		response: &types.ChatResponse{},
	}
	srv := newGatewayServer(t, provider, config.Config{
		Router: config.RouterConfig{DefaultModel: "gpt-4o-mini"},
		Governor: config.GovernorConfig{
			PolicyRules: []config.PolicyRuleConfig{{
				ID:            "deny-cloud",
				Action:        "deny",
				Reason:        "cloud denied from trace test",
				ProviderKinds: []string{"cloud"},
			}},
		},
	})
	defer srv.Close()

	body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`
	chatResp := gatewayPost(t, srv.URL+"/v1/chat/completions", body, nil)
	requestID := chatResp.Header.Get("X-Request-Id")
	raw := readBody(t, chatResp)
	if chatResp.StatusCode != http.StatusForbidden {
		t.Fatalf("chat status = %d, want 403, body=%s", chatResp.StatusCode, raw)
	}
	if requestID == "" {
		t.Fatal("X-Request-Id = empty, cannot query trace")
	}

	traceResp, err := http.Get(srv.URL + "/hecate/v1/traces?request_id=" + requestID)
	if err != nil {
		t.Fatalf("GET /hecate/v1/traces error = %v", err)
	}
	traceRaw := readBody(t, traceResp)
	if traceResp.StatusCode != http.StatusOK {
		t.Fatalf("trace status = %d, body=%s", traceResp.StatusCode, traceRaw)
	}

	var trace TraceResponse
	if err := json.Unmarshal(traceRaw, &trace); err != nil {
		t.Fatalf("Unmarshal() error = %v, body=%s", err, traceRaw)
	}
	foundDenied := false
	for _, candidate := range trace.Data.Route.Candidates {
		if candidate.Provider == "openai" && candidate.Outcome == "denied" && candidate.SkipReason == "policy_denied" {
			foundDenied = true
			if candidate.PolicyRuleID != "deny-cloud" {
				t.Fatalf("candidate.policy_rule_id = %q, want deny-cloud", candidate.PolicyRuleID)
			}
			if candidate.PolicyAction != "deny" {
				t.Fatalf("candidate.policy_action = %q, want deny", candidate.PolicyAction)
			}
			if candidate.PolicyReason != "cloud denied from trace test" {
				t.Fatalf("candidate.policy_reason = %q, want policy reason", candidate.PolicyReason)
			}
		}
	}
	if !foundDenied {
		t.Fatalf("missing denied openai policy_denied candidate: %+v", trace.Data.Route.Candidates)
	}
}

func TestGatewayTraceEndpointShowsConfiguredRouteModePolicyMetadata(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		name:         "ollama",
		defaultModel: "llama3.1:8b",
		capabilities: providers.Capabilities{
			Name:         "ollama",
			Kind:         providers.KindLocal,
			DefaultModel: "llama3.1:8b",
			Models:       []string{"llama3.1:8b"},
		},
		response: &types.ChatResponse{},
	}
	srv := newGatewayServer(t, provider, config.Config{
		Router: config.RouterConfig{DefaultModel: "llama3.1:8b"},
		Governor: config.GovernorConfig{
			RouteMode: "cloud_only",
		},
	})
	defer srv.Close()

	body := `{"messages":[{"role":"user","content":"hi"}]}`
	chatResp := gatewayPost(t, srv.URL+"/v1/chat/completions", body, nil)
	requestID := chatResp.Header.Get("X-Request-Id")
	raw := readBody(t, chatResp)
	if chatResp.StatusCode != http.StatusForbidden {
		t.Fatalf("chat status = %d, want 403, body=%s", chatResp.StatusCode, raw)
	}

	traceResp, err := http.Get(srv.URL + "/hecate/v1/traces?request_id=" + requestID)
	if err != nil {
		t.Fatalf("GET /hecate/v1/traces error = %v", err)
	}
	traceRaw := readBody(t, traceResp)
	if traceResp.StatusCode != http.StatusOK {
		t.Fatalf("trace status = %d, body=%s", traceResp.StatusCode, traceRaw)
	}

	var trace TraceResponse
	if err := json.Unmarshal(traceRaw, &trace); err != nil {
		t.Fatalf("Unmarshal() error = %v, body=%s", err, traceRaw)
	}
	foundDenied := false
	for _, candidate := range trace.Data.Route.Candidates {
		if candidate.Provider == "ollama" && candidate.Outcome == "denied" && candidate.SkipReason == "policy_denied" {
			foundDenied = true
			if candidate.PolicyAction != "deny" {
				t.Fatalf("candidate.policy_action = %q, want deny", candidate.PolicyAction)
			}
			if candidate.PolicyReason == "" {
				t.Fatal("candidate.policy_reason is empty")
			}
		}
	}
	if !foundDenied {
		t.Fatalf("missing denied ollama policy_denied candidate: %+v", trace.Data.Route.Candidates)
	}
}

func TestGatewayTraceEndpointShowsRewritePolicyMetadata(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		name:         "openai",
		defaultModel: "gpt-4o-mini",
		capabilities: providers.Capabilities{
			Name:         "openai",
			Kind:         providers.KindCloud,
			DefaultModel: "gpt-4o-mini",
			Models:       []string{"gpt-4o-mini"},
		},
		response: &types.ChatResponse{
			Model: "gpt-4o-mini",
			Choices: []types.ChatChoice{{
				Index:        0,
				Message:      types.Message{Role: "assistant", Content: "hi"},
				FinishReason: "stop",
			}},
			Usage: types.Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12},
		},
	}
	srv := newGatewayServer(t, provider, config.Config{
		Router: config.RouterConfig{DefaultModel: "gpt-4o-mini"},
		Governor: config.GovernorConfig{
			PolicyRules: []config.PolicyRuleConfig{{
				ID:             "downgrade-default",
				Action:         "rewrite_model",
				Reason:         "default downgrade",
				Models:         []string{"gpt-4o"},
				RewriteModelTo: "gpt-4o-mini",
			}},
		},
	})
	defer srv.Close()

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	chatResp := gatewayPost(t, srv.URL+"/v1/chat/completions", body, nil)
	requestID := chatResp.Header.Get("X-Request-Id")
	raw := readBody(t, chatResp)
	if chatResp.StatusCode != http.StatusOK {
		t.Fatalf("chat status = %d, want 200, body=%s", chatResp.StatusCode, raw)
	}

	traceResp, err := http.Get(srv.URL + "/hecate/v1/traces?request_id=" + requestID)
	if err != nil {
		t.Fatalf("GET /hecate/v1/traces error = %v", err)
	}
	traceRaw := readBody(t, traceResp)
	if traceResp.StatusCode != http.StatusOK {
		t.Fatalf("trace status = %d, body=%s", traceResp.StatusCode, traceRaw)
	}

	var trace TraceResponse
	if err := json.Unmarshal(traceRaw, &trace); err != nil {
		t.Fatalf("Unmarshal() error = %v, body=%s", err, traceRaw)
	}
	foundRewrite := false
	for _, span := range trace.Data.Spans {
		for _, event := range span.Events {
			if event.Name != "governor.model_rewrite" {
				continue
			}
			foundRewrite = true
			if event.Attributes["gen_ai.request.model.original"] != "gpt-4o" {
				t.Fatalf("rewrite original model = %v, want gpt-4o", event.Attributes["gen_ai.request.model.original"])
			}
			if event.Attributes["gen_ai.request.model.rewritten"] != "gpt-4o-mini" {
				t.Fatalf("rewrite rewritten model = %v, want gpt-4o-mini", event.Attributes["gen_ai.request.model.rewritten"])
			}
			if event.Attributes["hecate.policy.rule_id"] != "downgrade-default" {
				t.Fatalf("rewrite policy rule id = %v, want downgrade-default", event.Attributes["hecate.policy.rule_id"])
			}
			if event.Attributes["hecate.policy.action"] != "rewrite_model" {
				t.Fatalf("rewrite policy action = %v, want rewrite_model", event.Attributes["hecate.policy.action"])
			}
			if event.Attributes["hecate.policy.reason"] != "default downgrade" {
				t.Fatalf("rewrite policy reason = %v, want default downgrade", event.Attributes["hecate.policy.reason"])
			}
		}
	}
	if !foundRewrite {
		t.Fatal("missing governor.model_rewrite event in trace")
	}
}

// TestClaudeCodeClientTraceEndpointShowsAnthropicRequest verifies that a
// /v1/messages request is also traceable via the GET /hecate/v1/traces endpoint.
func TestClaudeCodeClientTraceEndpointShowsAnthropicRequest(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		name: "anthropic",
		capabilities: providers.Capabilities{
			Name:         "anthropic",
			Kind:         providers.KindCloud,
			DefaultModel: "claude-sonnet-4-20250514",
			Models:       []string{"claude-sonnet-4-20250514"},
		},
		response: &types.ChatResponse{
			ID:    "msg_trace",
			Model: "claude-sonnet-4-20250514",
			Choices: []types.ChatChoice{{
				Message:      types.Message{Role: "assistant", Content: "traced"},
				FinishReason: "end_turn",
			}},
			Usage: types.Usage{PromptTokens: 8, CompletionTokens: 1, TotalTokens: 9},
		},
	}
	srv := newGatewayServer(t, provider, config.Config{
		Router: config.RouterConfig{DefaultModel: "claude-sonnet-4-20250514"},
	})
	defer srv.Close()

	msgBody := `{"model":"claude-sonnet-4-20250514","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`
	msgResp := gatewayPost(t, srv.URL+"/v1/messages", msgBody, map[string]string{"x-api-key": "sk-ant-unused"})
	requestID := msgResp.Header.Get("X-Request-Id")
	readBody(t, msgResp)
	if requestID == "" {
		t.Fatal("X-Request-Id = empty on /v1/messages response")
	}

	traceResp, err := http.Get(srv.URL + "/hecate/v1/traces?request_id=" + requestID)
	if err != nil {
		t.Fatalf("GET /hecate/v1/traces error = %v", err)
	}
	raw := readBody(t, traceResp)
	if traceResp.StatusCode != http.StatusOK {
		t.Fatalf("trace status = %d, body=%s", traceResp.StatusCode, raw)
	}

	var trace TraceResponse
	if err := json.Unmarshal(raw, &trace); err != nil {
		t.Fatalf("Unmarshal() error = %v, body=%s", err, raw)
	}
	if trace.Data.Route.FinalProvider != "anthropic" {
		t.Errorf("route.final_provider = %q, want anthropic", trace.Data.Route.FinalProvider)
	}
	if len(trace.Data.Spans) == 0 {
		t.Error("spans = empty, want span list for /v1/messages request")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func assertOpenAIErrorType(t *testing.T, raw []byte, want string) {
	t.Helper()
	var payload struct {
		Error struct {
			Type string `json:"type"`
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode OpenAI error: %v; body=%s", err, raw)
	}
	if payload.Error.Type != want && payload.Error.Code != want {
		t.Fatalf("error type/code = %q/%q, want %s; body=%s", payload.Error.Type, payload.Error.Code, want, raw)
	}
}

// keys returns the keys of a string→int map for diagnostic messages.
func keys(m map[string]int) []string {
	result := make([]string, 0, len(m))
	for k := range m {
		result = append(result, k)
	}
	return result
}

// Compile-time assertion: sseStreamingProvider must satisfy providers.Streamer.
var _ providers.Streamer = (*sseStreamingProvider)(nil)

// Compile-time assertion: sseStreamingProvider must satisfy providers.Provider.
var _ providers.Provider = (*sseStreamingProvider)(nil)
