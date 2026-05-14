package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/hecate/agent-runtime/internal/config"
	"github.com/hecate/agent-runtime/internal/providers"
	"github.com/hecate/agent-runtime/pkg/types"
)

// TestChatCompletionsRejectsMalformedJSON locks in the JSON-decode boundary:
// a body the standard library can't parse should produce a 400 with the
// invalid_request error code, never a 500. decodeJSON is shared with
// every other JSON endpoint, so this also documents the contract for them.
func TestChatCompletionsRejectsMalformedJSON(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{name: "openai", response: &types.ChatResponse{}}
	handler := newTestHTTPHandler(logger, provider)

	rec := performJSONRequest(t, handler, `{"model":"gpt-4o-mini","messages":[`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
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
	if payload.Error.Type != "invalid_request" {
		t.Errorf("error.type = %q, want invalid_request", payload.Error.Type)
	}
}

// TestChatCompletionsDeniedReturns403WithUserFacingMessage exercises the
// errDenied → 403 mapping and verifies the body has the "request denied: "
// classification prefix stripped (UserFacingMessage). Without this, the
// chat UI shows operators a confusing "request denied: requests are
// disabled by policy" string where the prefix is internal noise.
func TestChatCompletionsDeniedReturns403WithUserFacingMessage(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{name: "openai", response: &types.ChatResponse{}}

	// DenyAll trips the governor.Check path that wraps with errDenied.
	handler := newTestHTTPHandlerWithConfig(logger, provider, config.Config{
		Governor: config.GovernorConfig{DenyAll: true},
	})

	rec := performJSONRequest(t, handler, `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
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
	if strings.HasPrefix(payload.Error.Message, "request denied: ") {
		t.Errorf("error.message = %q, want classification prefix stripped", payload.Error.Message)
	}
	if !strings.Contains(payload.Error.Message, "disabled by policy") {
		t.Errorf("error.message = %q, want underlying reason to be visible", payload.Error.Message)
	}
	if provider.CallCount() != 0 {
		t.Fatalf("provider call count = %d, want 0 because governor denied pre-route", provider.CallCount())
	}
}

// TestChatCompletionsRateLimitedReturns429 covers the rate_limit path
// from the chat endpoint end-to-end. The unit test for checkRateLimit
// only exercises the helper in isolation; this proves the limiter is
// actually wired into the handler chain in front of HandleChat.
func TestChatCompletionsRateLimitedReturns429(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{
		name: "openai",
		response: &types.ChatResponse{
			ID: "chatcmpl-rl", Model: "gpt-4o-mini",
			Choices: []types.ChatChoice{{Message: types.Message{Role: "assistant", Content: "ok"}, FinishReason: "stop"}},
		},
	}
	// burst=1, RPM=60 → first request consumes the token, second returns 429.
	handler := newTestHTTPHandlerWithConfig(logger, provider, config.Config{
		Server: config.ServerConfig{
			RateLimit: config.RateLimitConfig{Enabled: true, RequestsPerMinute: 60, BurstSize: 1},
		},
	})

	body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`
	first := performJSONRequest(t, handler, body)
	if first.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200; body=%s", first.Code, first.Body.String())
	}
	second := performJSONRequest(t, handler, body)
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want 429; body=%s", second.Code, second.Body.String())
	}
	if got := second.Header().Get("X-RateLimit-Limit"); got != "1" {
		t.Errorf("X-RateLimit-Limit = %q, want \"1\" (the bucket capacity)", got)
	}
	if provider.CallCount() != 1 {
		t.Fatalf("provider call count = %d, want 1 because the second request was rate limited before routing", provider.CallCount())
	}
}
func TestChatCompletionsReturnsRouteImpossibleWhenNoProviderAvailable(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := newTestHTTPHandlerForProviders(logger, nil, config.Config{})

	rec := performJSONRequest(t, handler, `{"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
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
	if payload.Error.Type != errCodeRouteImpossible {
		t.Fatalf("error.type = %q, want %s", payload.Error.Type, errCodeRouteImpossible)
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

func TestChatCompletionsProviderAuthFailureReturnsStableCode(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{
		name: "openai",
		err: &providers.UpstreamError{
			StatusCode: http.StatusUnauthorized,
			Message:    "Incorrect API key provided",
			Type:       "invalid_request_error",
		},
	}
	handler := newTestHTTPHandler(logger, provider)

	rec := performJSONRequest(t, handler, `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload.Error.Type != errCodeProviderAuthFailed {
		t.Errorf("error.type = %q, want %s", payload.Error.Type, errCodeProviderAuthFailed)
	}
	if payload.Error.Message != "Incorrect API key provided" {
		t.Errorf("error.message = %q, want upstream diagnostic", payload.Error.Message)
	}
}

func TestChatCompletionsUnsupportedModelReturnsStableCode(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{name: "openai", response: &types.ChatResponse{}}
	handler := newTestHTTPHandler(logger, provider)

	rec := performJSONRequest(t, handler, `{"model":"llama3.1:8b","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload.Error.Type != errCodeUnsupportedModel {
		t.Errorf("error.type = %q, want %s", payload.Error.Type, errCodeUnsupportedModel)
	}
}

// failingStreamProvider implements ChatStream by returning a fixed error
// without writing any bytes — used to drive the mid-stream error path.
type failingStreamProvider struct {
	fakeProvider
	streamErr error
}

func (p *failingStreamProvider) ChatStream(_ context.Context, _ types.ChatRequest, _ io.Writer) error {
	return p.streamErr
}

// Compile-time assertions match the existing sseStreamingProvider pattern.
var (
	_ providers.Streamer = (*failingStreamProvider)(nil)
	_ providers.Provider = (*failingStreamProvider)(nil)
)

// TestChatCompletionsStreamMidStreamErrorEmitsSSEErrorEvent locks in the
// terminal SSE error format the handler writes when ChatStream errors
// after headers have already been committed. Headers are 200 OK and SSE
// Content-Type — the operator's SDK has no way to read a status code
// at this point, so the only signal is the embedded `data:` event with
// a JSON error and a final `data: [DONE]` to close the stream.
func TestChatCompletionsStreamMidStreamErrorEmitsSSEErrorEvent(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &failingStreamProvider{
		fakeProvider: fakeProvider{name: "openai", defaultModel: "gpt-4o-mini"},
		streamErr: &providers.UpstreamError{
			StatusCode: http.StatusBadGateway,
			Message:    "upstream connection reset",
			Type:       "server_error",
		},
	}
	handler := newTestHTTPHandler(logger, provider)

	rec := performJSONRequest(t, handler, `{"model":"gpt-4o-mini","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (mid-stream errors keep the headers we already sent); body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"error"`) || !strings.Contains(body, "upstream connection reset") {
		t.Errorf("body missing SSE error event; got=%q", body)
	}
	if !strings.Contains(body, "data: [DONE]") {
		t.Errorf("body missing terminal [DONE]; got=%q", body)
	}
}

// TestChatCompletionsStreamMidStreamErrorMessageIsValidJSON pins the
// SSE error-event JSON shape across messages with characters that need
// escaping. Go's %q verb produces a Go-syntax string literal that
// happens to be JSON-compatible for the common cases (quotes, newlines,
// backslashes); a regression that switched to a naive string-concat
// (`"...message: ` + errMsg + `"`) would silently corrupt the body any
// time upstream returned a quote-containing message — the client would
// see an SSE frame that fails JSON.parse() and the chat hangs.
func TestChatCompletionsStreamMidStreamErrorMessageIsValidJSON(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	// Upstream error containing every char that needs JSON escaping:
	// " (quote), \n (newline), \ (backslash). These are exactly the
	// characters a real provider error like
	//   `model "gpt-4o-mini" not found\non region "us"`
	// would carry.
	provider := &failingStreamProvider{
		fakeProvider: fakeProvider{name: "openai", defaultModel: "gpt-4o-mini"},
		streamErr: &providers.UpstreamError{
			StatusCode: http.StatusBadGateway,
			Message:    `upstream "rate limit" failed:` + "\n" + `details=\path\file`,
			Type:       "server_error",
		},
	}
	handler := newTestHTTPHandler(logger, provider)

	rec := performJSONRequest(t, handler, `{"model":"gpt-4o-mini","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	// Extract the data: line carrying the error and verify it's valid
	// JSON. SSE framing splits on \n\n; first frame is the error.
	lines := strings.Split(body, "\n")
	var dataLine string
	for _, ln := range lines {
		if strings.HasPrefix(ln, "data: {") {
			dataLine = strings.TrimPrefix(ln, "data: ")
			break
		}
	}
	if dataLine == "" {
		t.Fatalf("body missing JSON data line; got=%q", body)
	}
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(dataLine), &payload); err != nil {
		t.Fatalf("error frame is not valid JSON: %v\nframe=%q", err, dataLine)
	}
	want := `upstream "rate limit" failed:` + "\n" + `details=\path\file`
	if payload.Error.Message != want {
		t.Errorf("parsed message = %q, want %q", payload.Error.Message, want)
	}
}

func TestRenderChatCompletionResponseSurfacesCachedTokens(t *testing.T) {
	t.Parallel()
	resp := &types.ChatResponse{
		ID:    "chatcmpl-cache-1",
		Model: "claude-opus-4-5",
		Choices: []types.ChatChoice{{
			Index:        0,
			Message:      types.Message{Role: "assistant", Content: "ok"},
			FinishReason: "stop",
		}},
		Usage: types.Usage{
			PromptTokens:       100,
			CompletionTokens:   5,
			CachedPromptTokens: 80,
			TotalTokens:        185,
		},
	}
	got := renderChatCompletionResponse(resp)
	if got.Usage.PromptTokensDetails == nil {
		t.Fatalf("Usage.PromptTokensDetails is nil; want populated when CachedPromptTokens > 0")
	}
	if got.Usage.PromptTokensDetails.CachedTokens != 80 {
		t.Errorf("CachedTokens = %d, want 80", got.Usage.PromptTokensDetails.CachedTokens)
	}
	if got.Usage.PromptTokens != 100 || got.Usage.CompletionTokens != 5 || got.Usage.TotalTokens != 185 {
		t.Errorf("flat totals drifted: %+v", got.Usage)
	}
}

// TestRenderChatCompletionResponseOmitsDetailsWhenNoCacheTokens —
// pointer-omitempty contract: a response with no cache reads must
// not surface a `prompt_tokens_details` object at all (clients
// sniffing for `usage.prompt_tokens_details === undefined` should
// continue to see undefined).
func TestRenderChatCompletionResponseOmitsDetailsWhenNoCacheTokens(t *testing.T) {
	t.Parallel()
	resp := &types.ChatResponse{
		ID:      "chatcmpl-nocache",
		Model:   "gpt-4o-mini",
		Choices: []types.ChatChoice{{Index: 0, Message: types.Message{Role: "assistant", Content: "ok"}, FinishReason: "stop"}},
		Usage:   types.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}
	got := renderChatCompletionResponse(resp)
	if got.Usage.PromptTokensDetails != nil {
		t.Errorf("PromptTokensDetails should be nil when CachedPromptTokens == 0; got %+v", got.Usage.PromptTokensDetails)
	}
	wire, err := json.Marshal(got.Usage)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(wire), "prompt_tokens_details") {
		t.Errorf("wire JSON unexpectedly contains prompt_tokens_details: %s", wire)
	}
}

// TestOpenAIMessageContentUnmarshalsBothShapes pins the
// polymorphic content unmarshaller. The wire allows three shapes:
// JSON string, JSON array of blocks, JSON null. Each must round-
// trip cleanly into the typed Go struct so downstream parsers can
// branch on Text vs Blocks vs Null without re-decoding.
func TestOpenAIMessageContentUnmarshalsBothShapes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		input      string
		wantText   string
		wantBlocks int
		wantNull   bool
	}{
		{"plain string", `"hello"`, "hello", 0, false},
		{"empty string", `""`, "", 0, false},
		{"null", `null`, "", 0, true},
		{"text-only array", `[{"type":"text","text":"hi"}]`, "", 1, false},
		{"text+image array", `[{"type":"text","text":"describe this"},{"type":"image_url","image_url":{"url":"https://example.com/x.png","detail":"high"}}]`, "", 2, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got OpenAIMessageContent
			if err := json.Unmarshal([]byte(tc.input), &got); err != nil {
				t.Fatalf("Unmarshal(%q): %v", tc.input, err)
			}
			if got.Text != tc.wantText {
				t.Errorf("Text = %q, want %q", got.Text, tc.wantText)
			}
			if len(got.Blocks) != tc.wantBlocks {
				t.Errorf("Blocks len = %d, want %d", len(got.Blocks), tc.wantBlocks)
			}
			if got.Null != tc.wantNull {
				t.Errorf("Null = %v, want %v", got.Null, tc.wantNull)
			}
		})
	}
}

// TestOpenAIMessageContentMarshalRoundTrip confirms re-encoding
// produces the canonical wire shape: blocks → array, null → null,
// otherwise → string. Without this round-trip pin, a future field
// addition could accidentally drop text or blocks.
func TestOpenAIMessageContentMarshalRoundTrip(t *testing.T) {
	t.Parallel()
	imageURL := "data:image/png;base64,iVBOR"
	cases := []struct {
		name string
		in   OpenAIMessageContent
		want string
	}{
		{"text", OpenAIMessageContent{Text: "hi"}, `"hi"`},
		{"empty stays empty string", OpenAIMessageContent{}, `""`},
		{"null", OpenAIMessageContent{Null: true}, `null`},
		{"blocks override text",
			OpenAIMessageContent{
				Text: "ignored",
				Blocks: []OpenAIContentBlock{
					{Type: "text", Text: "a"},
					{Type: "image_url", ImageURL: &OpenAIContentImageURL{URL: imageURL, Detail: "low"}},
				},
			},
			`[{"type":"text","text":"a"},{"type":"image_url","image_url":{"url":"data:image/png;base64,iVBOR","detail":"low"}}]`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(tc.in)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("Marshal = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestNormalizeChatRequestParsesImageBlocks verifies that an
// OpenAI-shaped multi-modal request lands as a types.Message with
// ContentBlocks populated (text + image_url). The text-only string
// content path stays unchanged — backward compat for every
// existing single-modal caller.
func TestNormalizeChatRequestParsesImageBlocks(t *testing.T) {
	t.Parallel()
	body := `{
		"model":"gpt-4o-mini",
		"messages":[{"role":"user","content":[
			{"type":"text","text":"describe this"},
			{"type":"image_url","image_url":{"url":"https://example.com/cat.png","detail":"high"}}
		]}]
	}`
	var req OpenAIChatCompletionRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("decode wire request: %v", err)
	}
	internal, err := normalizeChatRequest(req, "req-1")
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(internal.Messages) != 1 {
		t.Fatalf("messages len = %d, want 1", len(internal.Messages))
	}
	m := internal.Messages[0]
	// Content (string flatten) gets the text part for legacy
	// code paths.
	if m.Content != "describe this" {
		t.Errorf("flattened Content = %q, want \"describe this\"", m.Content)
	}
	// ContentBlocks carries the structured form.
	if len(m.ContentBlocks) != 2 {
		t.Fatalf("ContentBlocks len = %d, want 2", len(m.ContentBlocks))
	}
	if m.ContentBlocks[0].Type != "text" || m.ContentBlocks[0].Text != "describe this" {
		t.Errorf("blocks[0] = %+v, want text/describe", m.ContentBlocks[0])
	}
	if m.ContentBlocks[1].Type != "image_url" {
		t.Errorf("blocks[1].Type = %q, want image_url", m.ContentBlocks[1].Type)
	}
	if m.ContentBlocks[1].Image == nil ||
		m.ContentBlocks[1].Image.URL != "https://example.com/cat.png" ||
		m.ContentBlocks[1].Image.Detail != "high" {
		t.Errorf("blocks[1].Image = %+v, want URL+Detail set", m.ContentBlocks[1].Image)
	}
}

// TestNormalizeChatRequestStringContentUnchanged is the backward-
// compat guard for the single-modal text path. Pre-multi-modal
// every request had `content: "..."` as a plain string; that
// shape must still produce a Message with Content set and no
// ContentBlocks (the old code path).
func TestNormalizeChatRequestStringContentUnchanged(t *testing.T) {
	t.Parallel()
	body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hello"}]}`
	var req OpenAIChatCompletionRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("decode: %v", err)
	}
	internal, err := normalizeChatRequest(req, "req-1")
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	m := internal.Messages[0]
	if m.Content != "hello" {
		t.Errorf("Content = %q, want hello", m.Content)
	}
	if len(m.ContentBlocks) != 0 {
		t.Errorf("ContentBlocks len = %d, want 0 (string-content path should not populate blocks)", len(m.ContentBlocks))
	}
}

// TestNormalizeChatRequestCapturesResponseFormat verifies the
// inbound parser preserves the structured-output knob onto
// ChatRequest. Without this, the OpenAI provider has nothing to
// pass through.
func TestNormalizeChatRequestCapturesResponseFormat(t *testing.T) {
	t.Parallel()
	rf := json.RawMessage(`{"type":"json_schema","json_schema":{"name":"r","schema":{"type":"object"}}}`)
	hi := "hi"
	req := OpenAIChatCompletionRequest{
		Model:          "gpt-4o-mini",
		Messages:       []OpenAIChatMessage{{Role: "user", Content: OpenAIMessageContent{Text: hi}}},
		ResponseFormat: rf,
	}
	internal, err := normalizeChatRequest(req, "req-1")
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if string(internal.ResponseFormat) != string(rf) {
		t.Errorf("ResponseFormat = %s, want %s", internal.ResponseFormat, rf)
	}
}
