package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hecate/agent-runtime/internal/config"
	"github.com/hecate/agent-runtime/pkg/types"
)

func TestAnthropicProviderChatMapsMessagesAPI(t *testing.T) {
	t.Parallel()

	provider := NewAnthropicProvider(config.OpenAICompatibleProviderConfig{
		Name:         "anthropic",
		Kind:         "cloud",
		Protocol:     "anthropic",
		BaseURL:      "https://api.anthropic.test",
		APIKey:       "secret",
		APIVersion:   "2023-06-01",
		Timeout:      5 * time.Second,
		DefaultModel: "claude-sonnet-4-20250514",
	}, nil)
	provider.cachedCaps = Capabilities{
		Name:         "anthropic",
		Kind:         KindCloud,
		DefaultModel: "claude-sonnet-4-20250514",
		Models:       []string{"claude-sonnet-4-20250514"},
	}
	provider.capsExpiry = time.Now().Add(time.Minute)
	provider.httpClient = &http.Client{
		Transport: testRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/v1/messages" {
				t.Fatalf("path = %q, want /v1/messages", r.URL.Path)
			}
			if got := r.Header.Get("x-api-key"); got != "secret" {
				t.Fatalf("x-api-key = %q, want secret", got)
			}
			if got := r.Header.Get("anthropic-version"); got != "2023-06-01" {
				t.Fatalf("anthropic-version = %q, want 2023-06-01", got)
			}

			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			if payload["model"] != "claude-sonnet-4-20250514" {
				t.Fatalf("model = %#v, want claude-sonnet-4-20250514", payload["model"])
			}
			if payload["max_tokens"] != float64(1024) {
				t.Fatalf("max_tokens = %#v, want 1024", payload["max_tokens"])
			}

			body, err := json.Marshal(map[string]any{
				"id":          "msg_123",
				"model":       "claude-sonnet-4-20250514",
				"role":        "assistant",
				"stop_reason": "end_turn",
				"content": []map[string]any{
					{"type": "text", "text": "Hello from Claude."},
				},
				"usage": map[string]any{
					"input_tokens":  14,
					"output_tokens": 5,
				},
			})
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(string(body))),
			}, nil
		}),
	}

	resp, err := provider.Chat(context.Background(), types.ChatRequest{
		Model: "claude-sonnet-4-20250514",
		Messages: []types.Message{
			{Role: "user", Content: "Hello"},
		},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if resp.ID != "msg_123" {
		t.Fatalf("id = %q, want msg_123", resp.ID)
	}
	if resp.Choices[0].Message.Content != "Hello from Claude." {
		t.Fatalf("content = %q, want Claude response", resp.Choices[0].Message.Content)
	}
	if resp.Usage.TotalTokens != 19 {
		t.Fatalf("total_tokens = %d, want 19", resp.Usage.TotalTokens)
	}
}

func TestAnthropicProviderCapabilitiesUsesModelsEndpoint(t *testing.T) {
	t.Parallel()

	provider := NewAnthropicProvider(config.OpenAICompatibleProviderConfig{
		Name:       "anthropic",
		Kind:       "cloud",
		Protocol:   "anthropic",
		BaseURL:    "https://api.anthropic.test",
		APIKey:     "secret",
		APIVersion: "2023-06-01",
		Timeout:    5 * time.Second,
	}, nil)
	provider.httpClient = &http.Client{
		Transport: testRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/v1/models" {
				t.Fatalf("path = %q, want /v1/models", r.URL.Path)
			}
			body, err := json.Marshal(map[string]any{
				"data": []map[string]any{
					{"id": "claude-sonnet-4-20250514"},
					{"id": "claude-haiku-3-5-20241022"},
				},
			})
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(string(body))),
			}, nil
		}),
	}

	caps, err := provider.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities() error = %v", err)
	}
	if caps.DefaultModel != "claude-sonnet-4-20250514" {
		t.Fatalf("default_model = %q, want first discovered model", caps.DefaultModel)
	}
	if len(caps.Models) != 2 {
		t.Fatalf("models = %d, want 2", len(caps.Models))
	}
}

func TestAnthropicProviderCapabilitiesSkipsDiscoveryWhenCloudProviderUnconfigured(t *testing.T) {
	t.Parallel()

	var calls int
	provider := NewAnthropicProvider(config.OpenAICompatibleProviderConfig{
		Name:       "anthropic",
		Kind:       "cloud",
		Protocol:   "anthropic",
		BaseURL:    "https://api.anthropic.com",
		APIVersion: "2023-06-01",
		Timeout:    5 * time.Second,
	}, nil)
	provider.httpClient = &http.Client{
		Transport: testRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"data":[{"id":"claude-sonnet-4-20250514"}]}`)),
			}, nil
		}),
	}

	caps, err := provider.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities() error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("discovery call count = %d, want 0 for unconfigured cloud provider", calls)
	}
	if caps.DiscoverySource != "config_unconfigured" {
		t.Fatalf("discovery source = %q, want config_unconfigured", caps.DiscoverySource)
	}
}

// TestAnthropicMessagesFromTypesPreservesCacheControl verifies that ContentBlocks with
// cache_control survive the conversion to the Anthropic wire format.
func TestAnthropicMessagesFromTypesPreservesCacheControl(t *testing.T) {
	t.Parallel()

	cc := json.RawMessage(`{"type":"ephemeral"}`)
	messages := []types.Message{
		{
			Role:    "system",
			Content: "You are helpful. Be concise.",
			ContentBlocks: []types.ContentBlock{
				{Type: "text", Text: "You are helpful.", CacheControl: cc},
				{Type: "text", Text: "Be concise."},
			},
		},
		{
			Role:    "user",
			Content: "What is 2+2?",
			ContentBlocks: []types.ContentBlock{
				{Type: "text", Text: "What is 2+2?", CacheControl: cc},
			},
		},
	}

	systemRaw, wire := anthropicMessagesFromTypes(messages)

	// System should be a JSON array (has cache_control).
	var sysBlocks []map[string]any
	if err := json.Unmarshal(systemRaw, &sysBlocks); err != nil {
		t.Fatalf("system is not a JSON array: %v, raw=%s", err, systemRaw)
	}
	if len(sysBlocks) != 2 {
		t.Fatalf("system blocks count = %d, want 2", len(sysBlocks))
	}
	// First block has cache_control.
	if sysBlocks[0]["cache_control"] == nil {
		t.Fatalf("system[0] missing cache_control")
	}
	// Second block has no cache_control.
	if sysBlocks[1]["cache_control"] != nil {
		t.Fatalf("system[1] unexpected cache_control")
	}

	// Messages should have one user message with cache_control on the text block.
	if len(wire) != 1 {
		t.Fatalf("wire messages count = %d, want 1", len(wire))
	}
	if wire[0].Role != "user" {
		t.Fatalf("wire[0].role = %q, want user", wire[0].Role)
	}
	if len(wire[0].Content) != 1 {
		t.Fatalf("wire[0] content blocks = %d, want 1", len(wire[0].Content))
	}
	if wire[0].Content[0].Type != "text" {
		t.Fatalf("wire[0].content[0].type = %q, want text", wire[0].Content[0].Type)
	}
	if len(wire[0].Content[0].CacheControl) == 0 {
		t.Fatal("wire[0].content[0] missing cache_control")
	}
}

func TestAnthropicMessagesFromTypesSingleBlockNoArrayWrap(t *testing.T) {
	t.Parallel()

	// System with a single text block and no cache_control → plain string, not array.
	messages := []types.Message{
		{
			Role:          "system",
			Content:       "You are helpful.",
			ContentBlocks: []types.ContentBlock{{Type: "text", Text: "You are helpful."}},
		},
		{Role: "user", Content: "Hi", ContentBlocks: []types.ContentBlock{{Type: "text", Text: "Hi"}}},
	}
	systemRaw, _ := anthropicMessagesFromTypes(messages)

	var s string
	if err := json.Unmarshal(systemRaw, &s); err != nil {
		t.Fatalf("system should be a plain JSON string, got: %s", systemRaw)
	}
	if s != "You are helpful." {
		t.Fatalf("system = %q, want plain text", s)
	}
}

func TestAnthropicChatUpstreamSendsCacheControlBlocks(t *testing.T) {
	t.Parallel()

	cc := json.RawMessage(`{"type":"ephemeral"}`)
	var capturedBody map[string]any

	provider := NewAnthropicProvider(config.OpenAICompatibleProviderConfig{
		Name:         "anthropic",
		Kind:         "cloud",
		Protocol:     "anthropic",
		BaseURL:      "https://api.anthropic.test",
		APIKey:       "secret",
		APIVersion:   "2023-06-01",
		Timeout:      5 * time.Second,
		DefaultModel: "claude-opus-4-5",
	}, nil)
	provider.cachedCaps = Capabilities{
		Name:         "anthropic",
		Kind:         KindCloud,
		DefaultModel: "claude-opus-4-5",
		Models:       []string{"claude-opus-4-5"},
	}
	provider.capsExpiry = time.Now().Add(time.Minute)
	provider.httpClient = &http.Client{
		Transport: testRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
			json.NewDecoder(r.Body).Decode(&capturedBody) //nolint:errcheck
			body, _ := json.Marshal(map[string]any{
				"id":          "msg_cc",
				"model":       "claude-opus-4-5",
				"role":        "assistant",
				"stop_reason": "end_turn",
				"content":     []map[string]any{{"type": "text", "text": "4"}},
				"usage":       map[string]any{"input_tokens": 20, "output_tokens": 1},
			})
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(string(body))),
			}, nil
		}),
	}

	_, err := provider.Chat(context.Background(), types.ChatRequest{
		Model:     "claude-opus-4-5",
		MaxTokens: 32,
		Messages: []types.Message{
			{
				Role:    "system",
				Content: "Big system prompt.",
				ContentBlocks: []types.ContentBlock{
					{Type: "text", Text: "Big system prompt.", CacheControl: cc},
				},
			},
			{
				Role:    "user",
				Content: "What is 2+2?",
				ContentBlocks: []types.ContentBlock{
					{Type: "text", Text: "What is 2+2?", CacheControl: cc},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	// Verify the captured upstream body has cache_control in system and messages.
	systemRaw, ok := capturedBody["system"]
	if !ok {
		t.Fatal("upstream body missing system field")
	}
	sysBytes, _ := json.Marshal(systemRaw)
	if !strings.Contains(string(sysBytes), "ephemeral") {
		t.Fatalf("system missing cache_control, got: %s", sysBytes)
	}

	msgs, _ := capturedBody["messages"].([]any)
	if len(msgs) == 0 {
		t.Fatal("upstream body missing messages")
	}
	msgBytes, _ := json.Marshal(msgs[0])
	if !strings.Contains(string(msgBytes), "ephemeral") {
		t.Fatalf("messages[0] missing cache_control, got: %s", msgBytes)
	}
}

// TestAnthropicProviderCapturesCacheReadTokens pins the prompt-cache
// usage path: when the upstream returns cache_read_input_tokens, it
// must land in Usage.CachedPromptTokens (so the pricebook applies
// the cache rate). The prior adapter dropped the field entirely,
// which made cache hits silently bill at the full input rate.
func TestAnthropicProviderCapturesCacheReadTokens(t *testing.T) {
	t.Parallel()
	provider := newAnthropicTestProvider(t, func(r *http.Request) (*http.Response, error) {
		body, _ := json.Marshal(map[string]any{
			"id":          "msg_c1",
			"model":       "claude-opus-4-5",
			"role":        "assistant",
			"stop_reason": "end_turn",
			"content":     []map[string]any{{"type": "text", "text": "ok"}},
			"usage": map[string]any{
				"input_tokens":            10,
				"output_tokens":           3,
				"cache_read_input_tokens": 1000,
			},
		})
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(string(body))),
		}, nil
	})

	resp, err := provider.Chat(context.Background(), types.ChatRequest{
		Model:    "claude-opus-4-5",
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if got := resp.Usage.PromptTokens; got != 10 {
		t.Errorf("PromptTokens = %d, want 10 (input_tokens only)", got)
	}
	if got := resp.Usage.CachedPromptTokens; got != 1000 {
		t.Errorf("CachedPromptTokens = %d, want 1000 (mapped from cache_read_input_tokens)", got)
	}
	if got := resp.Usage.CompletionTokens; got != 3 {
		t.Errorf("CompletionTokens = %d, want 3", got)
	}
	// Total includes everything billed: fresh input + cache reads + output.
	if got := resp.Usage.TotalTokens; got != 1013 {
		t.Errorf("TotalTokens = %d, want 1013 (10 + 1000 + 3)", got)
	}
}

// TestAnthropicProviderFoldsCacheCreationIntoPromptTokens verifies
// the second cache bucket — cache writes — gets counted (folded
// into PromptTokens at the fresh rate). The prior adapter dropped
// these too. The fold trade-off is documented on anthropicUsage:
// when the pricebook gains a cache-write rate, this becomes a
// dedicated Usage field.
func TestAnthropicProviderFoldsCacheCreationIntoPromptTokens(t *testing.T) {
	t.Parallel()
	provider := newAnthropicTestProvider(t, func(r *http.Request) (*http.Response, error) {
		body, _ := json.Marshal(map[string]any{
			"id":          "msg_c2",
			"model":       "claude-opus-4-5",
			"role":        "assistant",
			"stop_reason": "end_turn",
			"content":     []map[string]any{{"type": "text", "text": "ok"}},
			"usage": map[string]any{
				"input_tokens":                100,
				"output_tokens":               5,
				"cache_creation_input_tokens": 500,
			},
		})
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(string(body))),
		}, nil
	})

	resp, err := provider.Chat(context.Background(), types.ChatRequest{
		Model:    "claude-opus-4-5",
		Messages: []types.Message{{Role: "user", Content: "write to cache"}},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if got := resp.Usage.PromptTokens; got != 600 {
		t.Errorf("PromptTokens = %d, want 600 (100 fresh + 500 cache_creation)", got)
	}
	if got := resp.Usage.CachedPromptTokens; got != 0 {
		t.Errorf("CachedPromptTokens = %d, want 0 (cache_creation is NOT a cache read)", got)
	}
	if got := resp.Usage.TotalTokens; got != 605 {
		t.Errorf("TotalTokens = %d, want 605 (600 input-side + 5 output)", got)
	}
}

// TestAnthropicProviderUsageBackwardCompat — a response with
// neither cache field present (the common case before prompt
// caching is enabled) must produce the same Usage shape as before
// the cache-fields change. Guards against accidentally requiring
// the new fields or shifting behavior on un-cached requests.
func TestAnthropicProviderUsageBackwardCompat(t *testing.T) {
	t.Parallel()
	provider := newAnthropicTestProvider(t, func(r *http.Request) (*http.Response, error) {
		body, _ := json.Marshal(map[string]any{
			"id":          "msg_b1",
			"model":       "claude-opus-4-5",
			"role":        "assistant",
			"stop_reason": "end_turn",
			"content":     []map[string]any{{"type": "text", "text": "ok"}},
			"usage":       map[string]any{"input_tokens": 14, "output_tokens": 5},
		})
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(string(body))),
		}, nil
	})
	resp, err := provider.Chat(context.Background(), types.ChatRequest{
		Model:    "claude-opus-4-5",
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if resp.Usage.PromptTokens != 14 || resp.Usage.CompletionTokens != 5 || resp.Usage.TotalTokens != 19 {
		t.Errorf("Usage = %+v, want {PromptTokens:14 CompletionTokens:5 TotalTokens:19}", resp.Usage)
	}
	if resp.Usage.CachedPromptTokens != 0 {
		t.Errorf("CachedPromptTokens = %d, want 0 (no cache fields present)", resp.Usage.CachedPromptTokens)
	}
}

// TestAnthropicProviderStreamForwardsCacheUsage verifies the
// streaming path carries cache token counts through to the final
// usage chunk. The prior adapter dropped the message_start usage
// entirely and emitted prompt_tokens=0 for every streamed
// response — invisible billing bug.
//
// We feed a synthetic SSE stream into translateAnthropicSSE
// directly (rather than wiring up an HTTP server) so the test
// stays focused on the translation contract.
func TestAnthropicProviderStreamForwardsCacheUsage(t *testing.T) {
	t.Parallel()

	// Anthropic SSE: message_start carries the input/cache buckets;
	// message_delta carries running output_tokens; message_stop
	// closes the stream.
	src := strings.NewReader(strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_s1","model":"claude-opus-4-5","usage":{"input_tokens":50,"output_tokens":0,"cache_read_input_tokens":2000,"cache_creation_input_tokens":300}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":12}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n"))

	var dst strings.Builder
	if err := translateAnthropicSSE(context.Background(), "claude-opus-4-5", src, &dst); err != nil {
		t.Fatalf("translateAnthropicSSE: %v", err)
	}

	// Find the usage chunk emitted on message_delta. It's the only
	// chunk with a non-empty `usage` object.
	var lastUsage map[string]any
	for _, line := range strings.Split(dst.String(), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		body := strings.TrimPrefix(line, "data: ")
		if body == "[DONE]" {
			continue
		}
		var chunk map[string]any
		if err := json.Unmarshal([]byte(body), &chunk); err != nil {
			continue
		}
		if u, ok := chunk["usage"].(map[string]any); ok && len(u) > 0 {
			lastUsage = u
		}
	}
	if lastUsage == nil {
		t.Fatalf("no usage chunk found in stream output: %s", dst.String())
	}

	// PromptTokens = input_tokens(50) + cache_creation(300) = 350.
	if got := lastUsage["prompt_tokens"]; got != float64(350) {
		t.Errorf("prompt_tokens = %v, want 350", got)
	}
	if got := lastUsage["completion_tokens"]; got != float64(12) {
		t.Errorf("completion_tokens = %v, want 12", got)
	}
	// Total = 350 prompt + 2000 cache reads + 12 output = 2362.
	if got := lastUsage["total_tokens"]; got != float64(2362) {
		t.Errorf("total_tokens = %v, want 2362", got)
	}
	// cached_tokens lives under prompt_tokens_details, mirroring
	// OpenAI's prompt-cache shape so downstream consumers don't
	// need a provider-specific accessor.
	details, ok := lastUsage["prompt_tokens_details"].(map[string]any)
	if !ok {
		t.Fatalf("prompt_tokens_details missing/not an object; got: %v", lastUsage["prompt_tokens_details"])
	}
	if got := details["cached_tokens"]; got != float64(2000) {
		t.Errorf("prompt_tokens_details.cached_tokens = %v, want 2000", got)
	}
}

// TestAnthropicProviderToolResultIsErrorRoundTrip verifies that a
// tool-role message with ToolError=true produces is_error=true on
// the wire. The model uses this to decide whether to retry or
// fall back; without it, errors are indistinguishable from
// successful results that happen to mention failure in their text.
func TestAnthropicProviderToolResultIsErrorRoundTrip(t *testing.T) {
	t.Parallel()
	var captured map[string]any
	provider := newAnthropicTestProvider(t, func(r *http.Request) (*http.Response, error) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		body, _ := json.Marshal(map[string]any{
			"id":          "msg_e1",
			"model":       "claude-opus-4-5",
			"role":        "assistant",
			"stop_reason": "end_turn",
			"content":     []map[string]any{{"type": "text", "text": "ack"}},
			"usage":       map[string]any{"input_tokens": 5, "output_tokens": 1},
		})
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(string(body))),
		}, nil
	})

	_, err := provider.Chat(context.Background(), types.ChatRequest{
		Model: "claude-opus-4-5",
		Messages: []types.Message{
			{Role: "user", Content: "do the thing"},
			{
				Role: "assistant",
				ToolCalls: []types.ToolCall{{
					ID:       "toolu_1",
					Type:     "function",
					Function: types.ToolCallFunction{Name: "shell_exec", Arguments: `{"cmd":"oops"}`},
				}},
			},
			{
				Role:       "tool",
				Content:    "command not found: oops",
				ToolCallID: "toolu_1",
				ToolError:  true,
			},
		},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	// Walk to the user message holding the tool_result and confirm is_error.
	msgs, _ := captured["messages"].([]any)
	if len(msgs) == 0 {
		t.Fatal("no messages on wire")
	}
	// tool_result lives on a user message (Anthropic's convention)
	// after the assistant tool_use turn.
	var found bool
	for _, m := range msgs {
		mm, _ := m.(map[string]any)
		if mm["role"] != "user" {
			continue
		}
		blocks, _ := mm["content"].([]any)
		for _, b := range blocks {
			bb, _ := b.(map[string]any)
			if bb["type"] != "tool_result" {
				continue
			}
			if bb["is_error"] != true {
				t.Errorf("tool_result is_error = %v, want true", bb["is_error"])
			}
			if bb["tool_use_id"] != "toolu_1" {
				t.Errorf("tool_use_id = %v, want toolu_1", bb["tool_use_id"])
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("did not find a tool_result block on the wire; messages=%+v", msgs)
	}
}

// TestAnthropicProviderToolResultMultiBlockContent verifies that
// when the source tool message carries structured ContentBlocks
// (e.g. text + image, the path inbound /v1/messages takes when a
// caller hands us multi-block tool results), they're emitted as a
// JSON array on the wire instead of being flattened to a string.
// The string form remains the default — only multi-block triggers
// the array path.
func TestAnthropicProviderToolResultMultiBlockContent(t *testing.T) {
	t.Parallel()
	var captured map[string]any
	provider := newAnthropicTestProvider(t, func(r *http.Request) (*http.Response, error) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		body, _ := json.Marshal(map[string]any{
			"id":          "msg_mb1",
			"model":       "claude-opus-4-5",
			"role":        "assistant",
			"stop_reason": "end_turn",
			"content":     []map[string]any{{"type": "text", "text": "got it"}},
			"usage":       map[string]any{"input_tokens": 5, "output_tokens": 1},
		})
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(string(body))),
		}, nil
	})

	cc := json.RawMessage(`{"type":"ephemeral"}`)
	_, err := provider.Chat(context.Background(), types.ChatRequest{
		Model: "claude-opus-4-5",
		Messages: []types.Message{
			{Role: "user", Content: "describe"},
			{
				Role: "assistant",
				ToolCalls: []types.ToolCall{{
					ID:       "toolu_2",
					Type:     "function",
					Function: types.ToolCallFunction{Name: "screenshot", Arguments: `{}`},
				}},
			},
			{
				Role:       "tool",
				Content:    "fallback string",
				ToolCallID: "toolu_2",
				ContentBlocks: []types.ContentBlock{
					{Type: "text", Text: "screenshot summary", CacheControl: cc},
					// "image" with a `source` blob (tested as raw JSON
					// pass-through; the gateway model puts image
					// source under the Input field for content blocks).
					{Type: "image", Input: json.RawMessage(`{"type":"base64","media_type":"image/png","data":"iVBORw0K"}`)},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	msgs, _ := captured["messages"].([]any)
	if len(msgs) == 0 {
		t.Fatal("no messages on wire")
	}
	var arr []any
	for _, m := range msgs {
		mm, _ := m.(map[string]any)
		if mm["role"] != "user" {
			continue
		}
		blocks, _ := mm["content"].([]any)
		for _, b := range blocks {
			bb, _ := b.(map[string]any)
			if bb["type"] != "tool_result" {
				continue
			}
			// content must be an array (not a string) since the
			// source had multi-block ContentBlocks.
			arr, _ = bb["content"].([]any)
		}
	}
	if arr == nil {
		t.Fatal("tool_result content not found / not an array; multi-block path didn't fire")
	}
	if len(arr) != 2 {
		t.Fatalf("nested content blocks = %d, want 2 (text + image)", len(arr))
	}
	first, _ := arr[0].(map[string]any)
	if first["type"] != "text" || first["text"] != "screenshot summary" {
		t.Errorf("first block = %+v, want text/screenshot summary", first)
	}
	if first["cache_control"] == nil {
		t.Errorf("first block dropped cache_control: %+v", first)
	}
	second, _ := arr[1].(map[string]any)
	if second["type"] != "image" {
		t.Errorf("second block = %+v, want image", second)
	}
	if second["source"] == nil {
		t.Errorf("image block missing source: %+v", second)
	}
}

// TestAnthropicProviderServiceTierPassthrough confirms the
// ServiceTier field on ChatRequest reaches the wire as
// service_tier, and that the empty default omits it (so older
// gateways and Anthropic clients see no behavioral change).
func TestAnthropicProviderServiceTierPassthrough(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		tier string
		want any // nil = field absent on wire
	}{
		{"empty omits field", "", nil},
		{"auto", "auto", "auto"},
		{"standard_only", "standard_only", "standard_only"},
		// Whitespace gets trimmed at the provider boundary so
		// operator config typos don't reach the upstream.
		{"trimmed", "  priority  ", "priority"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var captured map[string]any
			provider := newAnthropicTestProvider(t, func(r *http.Request) (*http.Response, error) {
				_ = json.NewDecoder(r.Body).Decode(&captured)
				body, _ := json.Marshal(map[string]any{
					"id":          "msg_t1",
					"model":       "claude-opus-4-5",
					"role":        "assistant",
					"stop_reason": "end_turn",
					"content":     []map[string]any{{"type": "text", "text": "ok"}},
					"usage":       map[string]any{"input_tokens": 1, "output_tokens": 1},
				})
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(string(body))),
				}, nil
			})
			_, err := provider.Chat(context.Background(), types.ChatRequest{
				Model:       "claude-opus-4-5",
				Messages:    []types.Message{{Role: "user", Content: "hi"}},
				ServiceTier: tc.tier,
			})
			if err != nil {
				t.Fatalf("Chat() error = %v", err)
			}
			got, present := captured["service_tier"]
			switch {
			case tc.want == nil && present:
				t.Errorf("service_tier present on wire (=%v) but should be omitted for empty input", got)
			case tc.want != nil && !present:
				t.Errorf("service_tier absent on wire; want %q", tc.want)
			case tc.want != nil && got != tc.want:
				t.Errorf("service_tier = %v, want %q", got, tc.want)
			}
		})
	}
}

// TestAnthropicProviderTranslatesURLImageBlock confirms a
// content block of type image_url with a public URL becomes an
// Anthropic image block on the wire with source.type=url.
// Catches the cross-provider routing case: caller hits
// /v1/chat/completions with multi-modal content but the router
// picks an Anthropic provider.
func TestAnthropicProviderTranslatesURLImageBlock(t *testing.T) {
	t.Parallel()
	var captured map[string]any
	provider := newAnthropicTestProvider(t, func(r *http.Request) (*http.Response, error) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		body, _ := json.Marshal(map[string]any{
			"id":          "msg_img_url",
			"model":       "claude-opus-4-5",
			"role":        "assistant",
			"stop_reason": "end_turn",
			"content":     []map[string]any{{"type": "text", "text": "It's a cat."}},
			"usage":       map[string]any{"input_tokens": 100, "output_tokens": 5},
		})
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(string(body))),
		}, nil
	})
	_, err := provider.Chat(context.Background(), types.ChatRequest{
		Model: "claude-opus-4-5",
		Messages: []types.Message{{
			Role:    "user",
			Content: "describe",
			ContentBlocks: []types.ContentBlock{
				{Type: "text", Text: "describe"},
				{Type: "image_url", Image: &types.ContentImage{URL: "https://example.com/cat.png"}},
			},
		}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	msgs, _ := captured["messages"].([]any)
	first, _ := msgs[0].(map[string]any)
	blocks, _ := first["content"].([]any)
	if len(blocks) != 2 {
		t.Fatalf("content blocks = %d, want 2", len(blocks))
	}
	imgBlock, _ := blocks[1].(map[string]any)
	if imgBlock["type"] != "image" {
		t.Errorf("blocks[1].type = %v, want image (translated)", imgBlock["type"])
	}
	source, _ := imgBlock["source"].(map[string]any)
	if source["type"] != "url" {
		t.Errorf("source.type = %v, want url", source["type"])
	}
	if source["url"] != "https://example.com/cat.png" {
		t.Errorf("source.url = %v, want passthrough URL", source["url"])
	}
}

// TestAnthropicProviderTranslatesDataURIImageBlock pins the
// data-URI translation: when the OpenAI caller sends
// `data:image/png;base64,...` (the common shape for client-side
// embedded images), we parse it and emit Anthropic's base64
// source form. Saves Anthropic from having to fetch a pseudo-URL.
func TestAnthropicProviderTranslatesDataURIImageBlock(t *testing.T) {
	t.Parallel()
	var captured map[string]any
	provider := newAnthropicTestProvider(t, func(r *http.Request) (*http.Response, error) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		body, _ := json.Marshal(map[string]any{
			"id":          "msg_img_b64",
			"model":       "claude-opus-4-5",
			"role":        "assistant",
			"stop_reason": "end_turn",
			"content":     []map[string]any{{"type": "text", "text": "ok"}},
			"usage":       map[string]any{"input_tokens": 100, "output_tokens": 1},
		})
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(string(body))),
		}, nil
	})
	_, err := provider.Chat(context.Background(), types.ChatRequest{
		Model: "claude-opus-4-5",
		Messages: []types.Message{{
			Role: "user",
			ContentBlocks: []types.ContentBlock{
				{Type: "image_url", Image: &types.ContentImage{URL: "data:image/jpeg;base64,/9j/4AAQ"}},
			},
		}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	msgs, _ := captured["messages"].([]any)
	first, _ := msgs[0].(map[string]any)
	blocks, _ := first["content"].([]any)
	imgBlock, _ := blocks[0].(map[string]any)
	source, _ := imgBlock["source"].(map[string]any)
	if source["type"] != "base64" {
		t.Errorf("source.type = %v, want base64 (data URI parsed)", source["type"])
	}
	if source["media_type"] != "image/jpeg" {
		t.Errorf("source.media_type = %v, want image/jpeg from URI", source["media_type"])
	}
	if source["data"] != "/9j/4AAQ" {
		t.Errorf("source.data = %v, want extracted base64 payload", source["data"])
	}
	// URL field must NOT be present alongside base64.
	if _, ok := source["url"]; ok {
		t.Errorf("source.url should be absent when type=base64; got %v", source["url"])
	}
}

// TestAnthropicProviderTier2FieldsAllDropped pins the wire
// invariant for the Tier-2 OpenAI passthroughs: every one of them
// must be absent from the body sent to Anthropic upstream. The
// gateway logs a warning per dropped field, but never injects the
// field onto the wire — that would either confuse Anthropic's
// strict parser or, worse, get silently ignored and mask the
// configuration bug.
//
// This is the dual of TestOpenAIProviderForwardsTier2Passthroughs:
// they pin the same fields' behavior on opposite sides of the
// router.
func TestAnthropicProviderTier2FieldsAllDropped(t *testing.T) {
	t.Parallel()
	intPtr := func(i int) *int { return &i }
	boolPtr := func(b bool) *bool { return &b }

	var captured map[string]any
	provider := newAnthropicTestProvider(t, func(r *http.Request) (*http.Response, error) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		body, _ := json.Marshal(map[string]any{
			"id":          "msg_t2drop",
			"model":       "claude-opus-4-5",
			"role":        "assistant",
			"stop_reason": "end_turn",
			"content":     []map[string]any{{"type": "text", "text": "ok"}},
			"usage":       map[string]any{"input_tokens": 1, "output_tokens": 1},
		})
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(string(body))),
		}, nil
	})
	_, err := provider.Chat(context.Background(), types.ChatRequest{
		Model:             "claude-opus-4-5",
		Messages:          []types.Message{{Role: "user", Content: "hi"}},
		Seed:              intPtr(7),
		PresencePenalty:   0.5,
		FrequencyPenalty:  -1,
		Logprobs:          true,
		TopLogprobs:       5,
		LogitBias:         json.RawMessage(`{"50256":-100}`),
		StreamOptions:     json.RawMessage(`{"include_usage":true}`),
		ParallelToolCalls: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	leaked := []string{}
	for _, key := range []string{
		"seed", "presence_penalty", "frequency_penalty",
		"logprobs", "top_logprobs", "logit_bias",
		"stream_options", "parallel_tool_calls",
	} {
		if _, ok := captured[key]; ok {
			leaked = append(leaked, key)
		}
	}
	if len(leaked) > 0 {
		t.Errorf("OpenAI-only fields leaked onto Anthropic wire: %v", leaked)
	}
}

// TestAnthropicProviderResponseFormatDroppedNotPropagated pins that
// the Anthropic provider does NOT forward an OpenAI-style
// response_format on the wire (Anthropic has no equivalent). The
// gateway logs a warning instead — verified separately via the
// captured slog output. This test is the wire-level guard so a
// future regression that accidentally adds the field (e.g. via a
// generic field-copy refactor) gets caught.
func TestAnthropicProviderResponseFormatDroppedNotPropagated(t *testing.T) {
	t.Parallel()
	var captured map[string]any
	provider := newAnthropicTestProvider(t, func(r *http.Request) (*http.Response, error) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		body, _ := json.Marshal(map[string]any{
			"id":          "msg_rf_drop",
			"model":       "claude-opus-4-5",
			"role":        "assistant",
			"stop_reason": "end_turn",
			"content":     []map[string]any{{"type": "text", "text": "ok"}},
			"usage":       map[string]any{"input_tokens": 1, "output_tokens": 1},
		})
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(string(body))),
		}, nil
	})
	_, err := provider.Chat(context.Background(), types.ChatRequest{
		Model:          "claude-opus-4-5",
		Messages:       []types.Message{{Role: "user", Content: "hi"}},
		ResponseFormat: json.RawMessage(`{"type":"json_object"}`),
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if _, present := captured["response_format"]; present {
		t.Errorf("response_format leaked onto Anthropic wire: %v", captured["response_format"])
	}
}

// newAnthropicCacheTestProvider builds an Anthropic provider with the
// given cache toggle and a transport that captures the outbound JSON
// body so tests can assert on the wire shape. Mirrors
// newAnthropicTestProvider's setup but threads AnthropicCacheDisabled
// through and stamps a fake-but-valid Messages-API response so
// provider.Chat returns cleanly.
func newAnthropicCacheTestProvider(t *testing.T, cacheDisabled bool, captured *map[string]any) *AnthropicProvider {
	t.Helper()
	p := NewAnthropicProvider(config.OpenAICompatibleProviderConfig{
		Name:                   "anthropic",
		Kind:                   "cloud",
		Protocol:               "anthropic",
		BaseURL:                "https://api.anthropic.test",
		APIKey:                 "secret",
		APIVersion:             "2023-06-01",
		Timeout:                5 * time.Second,
		DefaultModel:           "claude-opus-4-5",
		AnthropicCacheDisabled: cacheDisabled,
	}, nil)
	p.cachedCaps = Capabilities{
		Name:         "anthropic",
		Kind:         KindCloud,
		DefaultModel: "claude-opus-4-5",
		Models:       []string{"claude-opus-4-5"},
	}
	p.capsExpiry = time.Now().Add(time.Minute)
	p.httpClient = &http.Client{
		Transport: testRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
			_ = json.NewDecoder(r.Body).Decode(captured)
			body, _ := json.Marshal(map[string]any{
				"id":          "msg_cache_test",
				"model":       "claude-opus-4-5",
				"role":        "assistant",
				"stop_reason": "end_turn",
				"content":     []map[string]any{{"type": "text", "text": "ok"}},
				"usage":       map[string]any{"input_tokens": 10, "output_tokens": 1},
			})
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(string(body))),
			}, nil
		}),
	}
	return p
}

// chatRequestWithSystemAndTools is the canonical fixture for the
// cache-marker tests: one plain-string system prompt + a two-tool
// catalog. Cache markers should land on the system tail and the
// LAST tool only.
func chatRequestWithSystemAndTools() types.ChatRequest {
	return types.ChatRequest{
		Model: "claude-opus-4-5",
		Messages: []types.Message{
			{Role: "system", Content: "You are a careful operator-grade gateway."},
			{Role: "user", Content: "ping"},
		},
		Tools: []types.Tool{
			{Type: "function", Function: types.ToolFunction{
				Name:        "get_weather",
				Description: "Look up weather",
				Parameters:  json.RawMessage(`{"type":"object"}`),
			}},
			{Type: "function", Function: types.ToolFunction{
				Name:        "search_web",
				Description: "Search the web",
				Parameters:  json.RawMessage(`{"type":"object"}`),
			}},
		},
	}
}

func TestAnthropicProviderEmitsCacheMarkerOnSystemTail(t *testing.T) {
	t.Parallel()

	captured := map[string]any{}
	provider := newAnthropicCacheTestProvider(t, false, &captured)

	if _, err := provider.Chat(context.Background(), chatRequestWithSystemAndTools()); err != nil {
		t.Fatalf("Chat: %v", err)
	}

	systemRaw, ok := captured["system"]
	if !ok {
		t.Fatal("upstream body missing system field")
	}
	// String-form system must be lifted to a block list so the cache
	// marker can attach. Anything else (or a missing marker) means
	// the helper didn't run.
	blocks, ok := systemRaw.([]any)
	if !ok {
		t.Fatalf("system not block-list, got %T: %v", systemRaw, systemRaw)
	}
	if len(blocks) == 0 {
		t.Fatal("system block list is empty")
	}
	tail, ok := blocks[len(blocks)-1].(map[string]any)
	if !ok {
		t.Fatalf("system tail not an object, got %T", blocks[len(blocks)-1])
	}
	cc, ok := tail["cache_control"].(map[string]any)
	if !ok {
		t.Fatalf("system tail missing cache_control: %v", tail)
	}
	if cc["type"] != "ephemeral" {
		t.Fatalf("cache_control.type = %v, want ephemeral", cc["type"])
	}
}

func TestAnthropicProviderEmitsCacheMarkerOnToolsTail(t *testing.T) {
	t.Parallel()

	captured := map[string]any{}
	provider := newAnthropicCacheTestProvider(t, false, &captured)

	if _, err := provider.Chat(context.Background(), chatRequestWithSystemAndTools()); err != nil {
		t.Fatalf("Chat: %v", err)
	}

	tools, ok := captured["tools"].([]any)
	if !ok {
		t.Fatalf("tools not a list, got %T: %v", captured["tools"], captured["tools"])
	}
	if len(tools) != 2 {
		t.Fatalf("tools len = %d, want 2", len(tools))
	}
	// First tool must NOT have a marker — only the LAST entry of the
	// cacheable section is marked. Multiple markers fragment the
	// cache and are wasteful (and Anthropic caps the count anyway).
	first, _ := tools[0].(map[string]any)
	if _, present := first["cache_control"]; present {
		t.Errorf("first tool unexpectedly has cache_control: %v", first)
	}
	last, ok := tools[len(tools)-1].(map[string]any)
	if !ok {
		t.Fatalf("last tool not an object, got %T", tools[len(tools)-1])
	}
	cc, ok := last["cache_control"].(map[string]any)
	if !ok {
		t.Fatalf("last tool missing cache_control: %v", last)
	}
	if cc["type"] != "ephemeral" {
		t.Fatalf("cache_control.type = %v, want ephemeral", cc["type"])
	}
}

func TestAnthropicProviderOmitsCacheMarkersWhenDisabled(t *testing.T) {
	t.Parallel()

	captured := map[string]any{}
	provider := newAnthropicCacheTestProvider(t, true, &captured)

	if _, err := provider.Chat(context.Background(), chatRequestWithSystemAndTools()); err != nil {
		t.Fatalf("Chat: %v", err)
	}

	// system stays as a plain JSON string when caching is off — no
	// block-list lift, no cache_control anywhere on the wire.
	if s, ok := captured["system"].(string); !ok {
		t.Fatalf("system should be a string when cache disabled, got %T: %v", captured["system"], captured["system"])
	} else if s == "" {
		t.Fatal("system string is empty")
	}
	tools, _ := captured["tools"].([]any)
	for i, raw := range tools {
		obj, _ := raw.(map[string]any)
		if _, present := obj["cache_control"]; present {
			t.Errorf("tool[%d] has cache_control with caching disabled: %v", i, obj)
		}
	}
}

func TestAnthropicProviderRespectsCallerSuppliedToolCacheControl(t *testing.T) {
	t.Parallel()

	// Caller already attached cache_control to the FIRST tool — an
	// orchestrator's deliberate cache boundary. The auto-marker
	// should still attach to the last tool (Anthropic caches up to
	// 4 boundaries; both can coexist), but it must not overwrite
	// the caller's marker.
	captured := map[string]any{}
	provider := newAnthropicCacheTestProvider(t, false, &captured)

	req := chatRequestWithSystemAndTools()
	caller := json.RawMessage(`{"type":"ephemeral","tag":"caller"}`)
	req.Tools[0].CacheControl = caller

	if _, err := provider.Chat(context.Background(), req); err != nil {
		t.Fatalf("Chat: %v", err)
	}

	tools, _ := captured["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("tools len = %d, want 2", len(tools))
	}
	first, _ := tools[0].(map[string]any)
	cc, ok := first["cache_control"].(map[string]any)
	if !ok {
		t.Fatalf("caller marker dropped from first tool: %v", first)
	}
	if cc["tag"] != "caller" {
		t.Fatalf("first tool marker overwritten: %v", cc)
	}

	// The auto-marker must still attach to the last tool — that's the
	// "both can coexist" half of the contract this test exists to pin.
	// Without this assertion the caller-marker-preservation passes
	// even if the auto-marker were silently dropped, hiding a real
	// regression.
	last, _ := tools[len(tools)-1].(map[string]any)
	autoCC, ok := last["cache_control"].(map[string]any)
	if !ok {
		t.Fatalf("auto-marker missing from last tool: %v", last)
	}
	if autoCC["type"] != "ephemeral" {
		t.Fatalf("last tool marker = %v, want type=ephemeral", autoCC)
	}
	if _, hasTag := autoCC["tag"]; hasTag {
		t.Fatalf("last tool marker carries caller-only tag: %v", autoCC)
	}
}

func TestAnthropicProviderStreamEmitsCacheMarkers(t *testing.T) {
	t.Parallel()

	// Streaming path is a separate wireReq construction (see the
	// seven-step chain — common bug #1: forget to plumb a field
	// into both Chat and ChatStream). Pin that the cache helpers
	// run on the stream side too.
	var captured map[string]any
	provider := NewAnthropicProvider(config.OpenAICompatibleProviderConfig{
		Name:         "anthropic",
		Kind:         "cloud",
		Protocol:     "anthropic",
		BaseURL:      "https://api.anthropic.test",
		APIKey:       "secret",
		APIVersion:   "2023-06-01",
		Timeout:      5 * time.Second,
		DefaultModel: "claude-opus-4-5",
		// AnthropicCacheDisabled left zero-valued -> caching enabled.
	}, nil)
	provider.cachedCaps = Capabilities{
		Name:         "anthropic",
		Kind:         KindCloud,
		DefaultModel: "claude-opus-4-5",
		Models:       []string{"claude-opus-4-5"},
	}
	provider.capsExpiry = time.Now().Add(time.Minute)
	provider.httpClient = &http.Client{
		Transport: testRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
			_ = json.NewDecoder(r.Body).Decode(&captured)
			// Minimal valid SSE: message_start + message_stop. The
			// translator handles the empty stream and returns nil.
			sse := "event: message_start\n" +
				`data: {"type":"message_start","message":{"id":"m","model":"claude-opus-4-5","role":"assistant","content":[],"stop_reason":null,"usage":{"input_tokens":1,"output_tokens":0}}}` + "\n\n" +
				"event: message_stop\n" +
				`data: {"type":"message_stop"}` + "\n\n"
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(sse)),
			}, nil
		}),
	}

	if err := provider.ChatStream(context.Background(), chatRequestWithSystemAndTools(), io.Discard); err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	sysBlocks, ok := captured["system"].([]any)
	if !ok || len(sysBlocks) == 0 {
		t.Fatalf("stream system not block-list with marker: %T %v", captured["system"], captured["system"])
	}
	sysTail, _ := sysBlocks[len(sysBlocks)-1].(map[string]any)
	if _, present := sysTail["cache_control"]; !present {
		t.Errorf("stream system tail missing cache_control: %v", sysTail)
	}
	tools, _ := captured["tools"].([]any)
	if len(tools) == 0 {
		t.Fatal("stream tools missing")
	}
	toolsTail, _ := tools[len(tools)-1].(map[string]any)
	if _, present := toolsTail["cache_control"]; !present {
		t.Errorf("stream tools tail missing cache_control: %v", toolsTail)
	}
}

// TestApplyAnthropicSystemCacheMarkerPreservesCallerMarker pins the
// "caller intent wins" rule for the system helper directly — easier
// to debug than going through the full Chat path.
func TestApplyAnthropicSystemCacheMarkerPreservesCallerMarker(t *testing.T) {
	t.Parallel()

	in := json.RawMessage(`[{"type":"text","text":"sys","cache_control":{"type":"ephemeral","tag":"caller"}}]`)
	out := applyAnthropicSystemCacheMarker(in)

	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal(out, &blocks); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("blocks len = %d, want 1", len(blocks))
	}
	cc := string(blocks[0]["cache_control"])
	if !strings.Contains(cc, `"tag":"caller"`) {
		t.Fatalf("caller marker overwritten, got: %s", cc)
	}
}

// TestApplyAnthropicSystemCacheMarkerHandlesEmpty pins the empty-
// input fast-path: no system content means nothing to mark, return
// the input unchanged. Otherwise we'd emit a cache_control on a
// non-existent block, which Anthropic would 400.
func TestApplyAnthropicSystemCacheMarkerHandlesEmpty(t *testing.T) {
	t.Parallel()

	if got := applyAnthropicSystemCacheMarker(nil); got != nil {
		t.Errorf("nil input: got %s, want nil", got)
	}
	if got := applyAnthropicSystemCacheMarker(json.RawMessage(`""`)); string(got) != `""` {
		t.Errorf("empty-string input: got %s, want \"\"", got)
	}
}
