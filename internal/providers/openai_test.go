package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hecate/agent-runtime/internal/config"
	"github.com/hecate/agent-runtime/pkg/types"
)

func jsonHTTPResponse(v any) (*http.Response, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
	}, nil
}

func TestOpenAIProviderChatUpstream(t *testing.T) {
	t.Parallel()

	transport := testRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			return nil, fmt.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v1/chat/completions" {
			return nil, fmt.Errorf("path = %s, want /v1/chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			return nil, fmt.Errorf("Authorization = %q, want %q", got, "Bearer test-key")
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, fmt.Errorf("ReadAll() error = %w", err)
		}

		var wireReq openAIChatCompletionRequest
		if err := json.Unmarshal(body, &wireReq); err != nil {
			return nil, fmt.Errorf("Unmarshal() error = %w", err)
		}
		if wireReq.Model != "gpt-4o-mini" {
			return nil, fmt.Errorf("model = %q, want %q", wireReq.Model, "gpt-4o-mini")
		}
		if len(wireReq.Messages) != 1 || wireReq.Messages[0].Content.AsString() != "hello" {
			return nil, fmt.Errorf("messages = %#v, want one hello message", wireReq.Messages)
		}

		responseBody, err := json.Marshal(openAIChatCompletionResponse{
			ID:      "chatcmpl-123",
			Created: 1_700_000_000,
			Model:   "gpt-4o-mini",
			Choices: []openAIChatCompletionChoice{
				{
					Index: 0,
					Message: openAIChatMessage{
						Role:    "assistant",
						Content: openAIMessageContent{Text: "world"},
					},
					FinishReason: "stop",
				},
			},
			Usage: openAIUsage{
				PromptTokens:     10,
				CompletionTokens: 4,
				TotalTokens:      14,
			},
		})
		if err != nil {
			return nil, err
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader(responseBody)),
		}, nil
	})

	provider := NewOpenAIProvider(config.OpenAICompatibleProviderConfig{
		Name:         "openai",
		Kind:         "cloud",
		BaseURL:      "https://example.test",
		APIKey:       "test-key",
		Timeout:      time.Second,
		StubMode:     false,
		DefaultModel: "gpt-4o-mini",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	provider.httpClient.Transport = transport

	resp, err := provider.Chat(context.Background(), types.ChatRequest{
		Model: "gpt-4o-mini",
		Messages: []types.Message{
			{Role: "user", Content: "hello"},
		},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if resp.ID != "chatcmpl-123" {
		t.Fatalf("response id = %q, want %q", resp.ID, "chatcmpl-123")
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "world" {
		t.Fatalf("choices = %#v, want assistant world", resp.Choices)
	}
	if resp.Usage.TotalTokens != 14 {
		t.Fatalf("total tokens = %d, want 14", resp.Usage.TotalTokens)
	}
}

func TestOpenAIProviderCustomEndpointPaths(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}
	transport := testRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		seen[r.URL.Path] = true
		switch r.URL.Path {
		case "/v1/models":
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"object":"list","data":[{"id":"sonar","object":"model"}]}`)),
			}, nil
		case "/chat/completions":
			responseBody, err := json.Marshal(openAIChatCompletionResponse{
				ID:      "chatcmpl-sonar",
				Created: 1_700_000_000,
				Model:   "sonar",
				Choices: []openAIChatCompletionChoice{
					{
						Index: 0,
						Message: openAIChatMessage{
							Role:    "assistant",
							Content: openAIMessageContent{Text: "grounded answer"},
						},
						FinishReason: "stop",
					},
				},
				Usage: openAIUsage{PromptTokens: 2, CompletionTokens: 2, TotalTokens: 4},
			})
			if err != nil {
				return nil, err
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(bytes.NewReader(responseBody)),
			}, nil
		default:
			return nil, fmt.Errorf("unexpected path %s", r.URL.Path)
		}
	})

	provider := NewOpenAIProvider(config.OpenAICompatibleProviderConfig{
		Name:         "perplexity",
		Kind:         "cloud",
		BaseURL:      "https://api.perplexity.test",
		APIKey:       "test-key",
		ChatPath:     "/chat/completions",
		ModelsPath:   "/v1/models",
		DefaultModel: "sonar",
		Timeout:      time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	provider.httpClient.Transport = transport

	caps, err := provider.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities() error = %v", err)
	}
	if len(caps.Models) != 1 || caps.Models[0] != "sonar" {
		t.Fatalf("models = %#v, want sonar", caps.Models)
	}
	if _, err := provider.Chat(context.Background(), types.ChatRequest{
		Model:    "sonar",
		Messages: []types.Message{{Role: "user", Content: "hello"}},
	}); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if !seen["/v1/models"] || !seen["/chat/completions"] {
		t.Fatalf("seen paths = %#v, want /v1/models and /chat/completions", seen)
	}
}

func TestOpenAIProviderChatUpstreamError(t *testing.T) {
	t.Parallel()

	transport := testRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		responseBody, err := json.Marshal(openAIErrorEnvelope{
			Error: openAIErrorDetail{
				Message: "invalid api key",
				Type:    "invalid_request_error",
			},
		})
		if err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader(responseBody)),
		}, nil
	})

	provider := NewOpenAIProvider(config.OpenAICompatibleProviderConfig{
		Name:         "openai",
		Kind:         "cloud",
		BaseURL:      "https://example.test",
		APIKey:       "bad-key",
		Timeout:      time.Second,
		StubMode:     false,
		DefaultModel: "gpt-4o-mini",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	provider.httpClient.Transport = transport

	_, err := provider.Chat(context.Background(), types.ChatRequest{
		Model: "gpt-4o-mini",
		Messages: []types.Message{
			{Role: "user", Content: "hello"},
		},
	})
	if err == nil {
		t.Fatal("Chat() error = nil, want upstream error")
	}

	upstreamErr, ok := err.(*UpstreamError)
	if !ok {
		t.Fatalf("error type = %T, want *UpstreamError", err)
	}
	if upstreamErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", upstreamErr.StatusCode, http.StatusUnauthorized)
	}
}

func TestOpenAIProviderChatStreamUsesPortableOpenAICompatiblePayload(t *testing.T) {
	t.Parallel()

	transport := testRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			return nil, fmt.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v1/chat/completions" {
			return nil, fmt.Errorf("path = %s, want /v1/chat/completions", r.URL.Path)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, fmt.Errorf("ReadAll() error = %w", err)
		}
		var wireReq map[string]any
		if err := json.Unmarshal(body, &wireReq); err != nil {
			return nil, fmt.Errorf("Unmarshal() error = %w", err)
		}
		if wireReq["stream"] != true {
			return nil, fmt.Errorf("stream = %#v, want true", wireReq["stream"])
		}
		if _, ok := wireReq["stream_options"]; ok {
			return nil, fmt.Errorf("stream_options was sent; generic OpenAI-compatible local runtimes may reject it")
		}

		body = []byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\ndata: [DONE]\n\n")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(bytes.NewReader(body)),
		}, nil
	})

	provider := NewOpenAICompatibleProvider(config.OpenAICompatibleProviderConfig{
		Name:         "ollama",
		Kind:         "local",
		BaseURL:      "http://127.0.0.1:11434/v1",
		Timeout:      time.Second,
		DefaultModel: "llama3.1:8b",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	provider.httpClient.Transport = transport

	var out bytes.Buffer
	err := provider.ChatStream(context.Background(), types.ChatRequest{
		Model: "llama3.1:8b",
		Messages: []types.Message{
			{Role: "user", Content: "hello"},
		},
	}, &out)
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	if got := out.String(); got == "" {
		t.Fatal("stream output = empty, want proxied SSE")
	}
}

func TestOpenAIProviderCapabilitiesDiscovery(t *testing.T) {
	t.Parallel()

	var calls int
	transport := testRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodGet {
			return nil, fmt.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/v1/models" {
			return nil, fmt.Errorf("path = %s, want /v1/models", r.URL.Path)
		}

		responseBody, err := json.Marshal(openAIModelsResponse{
			Data: []openAIModel{
				{ID: "llama3.1:8b"},
				{ID: "qwen2.5:7b"},
			},
		})
		if err != nil {
			return nil, err
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader(responseBody)),
		}, nil
	})

	provider := NewOpenAICompatibleProvider(config.OpenAICompatibleProviderConfig{
		Name:         "localai",
		Kind:         "local",
		BaseURL:      "http://127.0.0.1:8080/v1",
		Timeout:      time.Second,
		DefaultModel: "configured-default",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	provider.httpClient.Transport = transport

	caps, err := provider.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities() error = %v", err)
	}
	if caps.DefaultModel != "configured-default" {
		t.Fatalf("default model = %q, want configured-default", caps.DefaultModel)
	}
	if len(caps.Models) != 2 || caps.Models[0] != "llama3.1:8b" {
		t.Fatalf("models = %#v, want discovered model list", caps.Models)
	}
	if caps.DiscoverySource != "upstream_v1_models" {
		t.Fatalf("discovery source = %q, want upstream_v1_models", caps.DiscoverySource)
	}

	_, err = provider.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities() cached error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("discovery call count = %d, want 1 due to cache", calls)
	}
}

func TestOpenAIProviderOllamaDiscoveryReadsNativeToolCapabilities(t *testing.T) {
	t.Parallel()

	var showCalls atomic.Int64
	transport := testRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/v1/models":
			if r.Method != http.MethodGet {
				return nil, fmt.Errorf("models method = %s, want GET", r.Method)
			}
			return jsonHTTPResponse(openAIModelsResponse{
				Data: []openAIModel{
					{ID: "qwen2.5-coder:7b"},
					{ID: "smollm2:135m"},
				},
			})
		case "/api/show":
			if r.Method != http.MethodPost {
				return nil, fmt.Errorf("show method = %s, want POST", r.Method)
			}
			showCalls.Add(1)
			var body ollamaShowRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				return nil, err
			}
			switch body.Model {
			case "qwen2.5-coder:7b":
				return jsonHTTPResponse(ollamaShowResponse{Capabilities: []string{"completion", "tools"}})
			case "smollm2:135m":
				return jsonHTTPResponse(ollamaShowResponse{Capabilities: []string{"completion"}})
			default:
				return nil, fmt.Errorf("unexpected show model %q", body.Model)
			}
		default:
			return nil, fmt.Errorf("path = %s, want /v1/models or /api/show", r.URL.Path)
		}
	})

	provider := NewOpenAICompatibleProvider(config.OpenAICompatibleProviderConfig{
		Name:         "ollama",
		Kind:         "local",
		BaseURL:      "http://127.0.0.1:11434/v1",
		Timeout:      time.Second,
		DefaultModel: "qwen2.5-coder:7b",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	provider.httpClient.Transport = transport

	caps, err := provider.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities() error = %v", err)
	}
	if got := showCalls.Load(); got != 2 {
		t.Fatalf("show call count = %d, want 2", got)
	}
	if got := caps.ModelCapabilities["qwen2.5-coder:7b"].ToolCalling; got != "basic" {
		t.Fatalf("qwen tool calling = %q, want basic", got)
	}
	if got := caps.ModelCapabilities["smollm2:135m"].ToolCalling; got != "none" {
		t.Fatalf("smollm2 tool calling = %q, want none", got)
	}
	if got := caps.ModelCapabilities["qwen2.5-coder:7b"].Source; got != "provider" {
		t.Fatalf("capability source = %q, want provider", got)
	}
}

func TestOpenAIProviderOllamaDiscoveryRunsShowRequestsConcurrently(t *testing.T) {
	t.Parallel()

	models := make([]openAIModel, 8)
	for i := range models {
		models[i] = openAIModel{ID: fmt.Sprintf("model-%d", i)}
	}
	var inFlight atomic.Int64
	var maxInFlight atomic.Int64
	transport := testRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/v1/models":
			return jsonHTTPResponse(openAIModelsResponse{Data: models})
		case "/api/show":
			current := inFlight.Add(1)
			for {
				max := maxInFlight.Load()
				if current <= max || maxInFlight.CompareAndSwap(max, current) {
					break
				}
			}
			defer inFlight.Add(-1)
			time.Sleep(20 * time.Millisecond)
			return jsonHTTPResponse(ollamaShowResponse{Capabilities: []string{"completion", "tools"}})
		default:
			return nil, fmt.Errorf("path = %s, want /v1/models or /api/show", r.URL.Path)
		}
	})

	provider := NewOpenAICompatibleProvider(config.OpenAICompatibleProviderConfig{
		Name:         "ollama",
		Kind:         "local",
		BaseURL:      "http://127.0.0.1:11434/v1",
		Timeout:      time.Second,
		DefaultModel: "model-0",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	provider.httpClient.Transport = transport

	if _, err := provider.Capabilities(context.Background()); err != nil {
		t.Fatalf("Capabilities() error = %v", err)
	}
	if got := maxInFlight.Load(); got < 2 {
		t.Fatalf("max concurrent /api/show calls = %d, want at least 2", got)
	}
	if got := maxInFlight.Load(); got > ollamaCapabilityDiscoveryConcurrency {
		t.Fatalf("max concurrent /api/show calls = %d, want <= %d", got, ollamaCapabilityDiscoveryConcurrency)
	}
}

func TestOpenAIProviderOllamaDiscoveryCapsShowRequestVolume(t *testing.T) {
	t.Parallel()

	models := make([]openAIModel, ollamaCapabilityDiscoveryMaxModels+5)
	for i := range models {
		models[i] = openAIModel{ID: fmt.Sprintf("model-%d", i)}
	}
	var showCalls atomic.Int64
	transport := testRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/v1/models":
			return jsonHTTPResponse(openAIModelsResponse{Data: models})
		case "/api/show":
			showCalls.Add(1)
			return jsonHTTPResponse(ollamaShowResponse{Capabilities: []string{"completion"}})
		default:
			return nil, fmt.Errorf("path = %s, want /v1/models or /api/show", r.URL.Path)
		}
	})

	provider := NewOpenAICompatibleProvider(config.OpenAICompatibleProviderConfig{
		Name:         "ollama",
		Kind:         "local",
		BaseURL:      "http://127.0.0.1:11434/v1",
		Timeout:      time.Second,
		DefaultModel: "model-0",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	provider.httpClient.Transport = transport

	if _, err := provider.Capabilities(context.Background()); err != nil {
		t.Fatalf("Capabilities() error = %v", err)
	}
	if got := showCalls.Load(); got != ollamaCapabilityDiscoveryMaxModels {
		t.Fatalf("show call count = %d, want capped at %d", got, ollamaCapabilityDiscoveryMaxModels)
	}
}

func TestOpenAIProviderCapabilitiesFallbackToConfig(t *testing.T) {
	t.Parallel()

	var calls int
	transport := testRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return nil, fmt.Errorf("network unavailable")
	})

	provider := NewOpenAICompatibleProvider(config.OpenAICompatibleProviderConfig{
		Name:         "localai",
		Kind:         "local",
		BaseURL:      "http://127.0.0.1:8080/v1",
		Timeout:      time.Second,
		DefaultModel: "llama3",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	provider.httpClient.Transport = transport

	caps, err := provider.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities() error = %v, want nil (fallback to config)", err)
	}
	if caps.DefaultModel != "llama3" {
		t.Fatalf("default model = %q, want llama3", caps.DefaultModel)
	}
	if len(caps.Models) != 1 || caps.Models[0] != "llama3" {
		t.Fatalf("models = %#v, want default model fallback only", caps.Models)
	}
	if caps.DiscoverySource != "config_fallback" {
		t.Fatalf("discovery source = %q, want config_fallback", caps.DiscoverySource)
	}
	if caps.LastError == "" {
		t.Fatal("last error is empty, want discovery failure diagnostic")
	}

	_, err = provider.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities() cached fallback error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("discovery call count = %d, want 1 due to fallback cache", calls)
	}
}

func TestOpenAIProviderCapabilitiesSkipsDiscoveryWhenCloudProviderUnconfigured(t *testing.T) {
	t.Parallel()

	var calls int
	transport := testRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"data":[{"id":"gpt-4o-mini"}]}`)),
		}, nil
	})

	provider := NewOpenAICompatibleProvider(config.OpenAICompatibleProviderConfig{
		Name:         "openai",
		Kind:         "cloud",
		BaseURL:      "https://api.openai.com/v1",
		Timeout:      time.Second,
		DefaultModel: "gpt-4o-mini",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	provider.httpClient.Transport = transport

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

func strPtr(s string) *string { return &s }

// TestOpenAIProviderCapturesCachedTokens verifies the prompt-cache
// usage path: when the upstream returns prompt_tokens_details with
// cached_tokens, the provider lifts that into Usage.CachedPromptTokens
// so cache hits stay visible in usage reporting.
func TestOpenAIProviderCapturesCachedTokens(t *testing.T) {
	t.Parallel()
	transport := testRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := json.Marshal(openAIChatCompletionResponse{
			ID:      "chatcmpl-cached",
			Created: 1_700_000_000,
			Model:   "gpt-4o-mini",
			Choices: []openAIChatCompletionChoice{{
				Index:        0,
				Message:      openAIChatMessage{Role: "assistant", Content: openAIMessageContent{Text: "ok"}},
				FinishReason: "stop",
			}},
			Usage: openAIUsage{
				PromptTokens:        100,
				CompletionTokens:    5,
				TotalTokens:         105,
				PromptTokensDetails: &openAIPromptTokensDetails{CachedTokens: 80},
			},
		})
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader(body)),
		}, nil
	})
	provider := NewOpenAIProvider(config.OpenAICompatibleProviderConfig{
		Name: "openai", Kind: "cloud", BaseURL: "https://example.test",
		APIKey: "k", Timeout: time.Second, DefaultModel: "gpt-4o-mini",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	provider.httpClient.Transport = transport
	provider.cachedCaps = Capabilities{
		Name:         "openai",
		Kind:         KindCloud,
		DefaultModel: "gpt-4o-mini",
		Models:       []string{"gpt-4o-mini"},
	}
	provider.capsExpiry = time.Now().Add(time.Minute)

	resp, err := provider.Chat(context.Background(), types.ChatRequest{
		Model:    "gpt-4o-mini",
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got := resp.Usage.CachedPromptTokens; got != 80 {
		t.Errorf("CachedPromptTokens = %d, want 80 (lifted from prompt_tokens_details.cached_tokens)", got)
	}
	if got := resp.Usage.PromptTokens; got != 100 {
		t.Errorf("PromptTokens = %d, want 100 (unchanged)", got)
	}
}

// TestOpenAIProviderForwardsImageBlocks pins multi-modal
// passthrough on the outbound OpenAI wire. When a ChatRequest's
// Message has ContentBlocks containing an image_url block, the
// outbound `content` field is the array form (not flattened to
// string) so the upstream sees the structured payload.
func TestOpenAIProviderForwardsImageBlocks(t *testing.T) {
	t.Parallel()
	var captured map[string]any
	transport := testRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/chat/completions" || r.Body == nil {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader([]byte(`{}`)))}, nil
		}
		_ = json.NewDecoder(r.Body).Decode(&captured)
		body, _ := json.Marshal(openAIChatCompletionResponse{
			ID: "x", Model: "gpt-4o-mini",
			Choices: []openAIChatCompletionChoice{{
				Message:      openAIChatMessage{Role: "assistant", Content: openAIMessageContent{Text: "ok"}},
				FinishReason: "stop",
			}},
			Usage: openAIUsage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
		})
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader(body)),
		}, nil
	})
	provider := NewOpenAIProvider(config.OpenAICompatibleProviderConfig{
		Name: "openai", Kind: "cloud", BaseURL: "https://example.test",
		APIKey: "k", Timeout: time.Second, DefaultModel: "gpt-4o-mini",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	provider.httpClient.Transport = transport
	provider.cachedCaps = Capabilities{
		Name: "openai", Kind: KindCloud,
		DefaultModel: "gpt-4o-mini",
		Models:       []string{"gpt-4o-mini"},
	}
	provider.capsExpiry = time.Now().Add(time.Minute)

	_, err := provider.Chat(context.Background(), types.ChatRequest{
		Model: "gpt-4o-mini",
		Messages: []types.Message{{
			Role:    "user",
			Content: "describe this",
			ContentBlocks: []types.ContentBlock{
				{Type: "text", Text: "describe this"},
				{Type: "image_url", Image: &types.ContentImage{URL: "https://example.com/x.png", Detail: "low"}},
			},
		}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	msgs, _ := captured["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages len = %d, want 1", len(msgs))
	}
	first, _ := msgs[0].(map[string]any)
	contentArr, ok := first["content"].([]any)
	if !ok {
		t.Fatalf("content was not an array (multi-modal path didn't trigger): %v", first["content"])
	}
	if len(contentArr) != 2 {
		t.Fatalf("content blocks = %d, want 2", len(contentArr))
	}
	imgBlock, _ := contentArr[1].(map[string]any)
	if imgBlock["type"] != "image_url" {
		t.Errorf("blocks[1].type = %v, want image_url", imgBlock["type"])
	}
	if imgURL, _ := imgBlock["image_url"].(map[string]any); imgURL["url"] != "https://example.com/x.png" || imgURL["detail"] != "low" {
		t.Errorf("blocks[1].image_url = %+v, want URL+detail", imgBlock["image_url"])
	}
}

// TestOpenAIProviderTextOnlyBlocksFlattenToString pins the compact
// behavior: when ContentBlocks contains only text blocks (no
// images), the outbound wire form is the plain string content,
// NOT the array form. This keeps payloads small and avoids
// surprising downstream OpenAI-compat endpoints (older Ollama,
// llama.cpp) that only accept string content.
func TestOpenAIProviderTextOnlyBlocksFlattenToString(t *testing.T) {
	t.Parallel()
	var captured map[string]any
	transport := testRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/chat/completions" || r.Body == nil {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader([]byte(`{}`)))}, nil
		}
		_ = json.NewDecoder(r.Body).Decode(&captured)
		body, _ := json.Marshal(openAIChatCompletionResponse{
			ID: "x", Model: "gpt-4o-mini",
			Choices: []openAIChatCompletionChoice{{
				Message:      openAIChatMessage{Role: "assistant", Content: openAIMessageContent{Text: "ok"}},
				FinishReason: "stop",
			}},
			Usage: openAIUsage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
		})
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader(body)),
		}, nil
	})
	provider := NewOpenAIProvider(config.OpenAICompatibleProviderConfig{
		Name: "openai", Kind: "cloud", BaseURL: "https://example.test",
		APIKey: "k", Timeout: time.Second, DefaultModel: "gpt-4o-mini",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	provider.httpClient.Transport = transport
	provider.cachedCaps = Capabilities{
		Name: "openai", Kind: KindCloud,
		DefaultModel: "gpt-4o-mini",
		Models:       []string{"gpt-4o-mini"},
	}
	provider.capsExpiry = time.Now().Add(time.Minute)

	_, err := provider.Chat(context.Background(), types.ChatRequest{
		Model: "gpt-4o-mini",
		Messages: []types.Message{{
			Role:          "user",
			Content:       "hello",
			ContentBlocks: []types.ContentBlock{{Type: "text", Text: "hello"}},
		}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	msgs, _ := captured["messages"].([]any)
	first, _ := msgs[0].(map[string]any)
	if got, ok := first["content"].(string); !ok || got != "hello" {
		t.Errorf("content was not the string form: %T %v", first["content"], first["content"])
	}
}

// TestOpenAIProviderForwardsTier2Passthroughs pins the Tier-2
// passthrough bundle: seed, penalties, logprobs/top_logprobs,
// logit_bias, stream_options, parallel_tool_calls. Each field
// must arrive on the wire exactly as the caller set it; an
// "absent" case verifies omitempty actually drops the field
// (otherwise we'd be sending the API a default the caller didn't
// ask for, e.g. seed=0 when the caller passed nothing).
//
// Pattern: build a ChatRequest with the field set, capture the
// upstream body, assert the JSON key/value matches.
func TestOpenAIProviderForwardsTier2Passthroughs(t *testing.T) {
	t.Parallel()

	intPtr := func(i int) *int { return &i }
	boolPtr := func(b bool) *bool { return &b }

	cases := []struct {
		name      string
		mutate    func(*types.ChatRequest)
		assertKey string
		want      any // nil = field absent on wire
	}{
		// Each row sets one field; we want a clean signal that
		// just that field arrived. Bundle-wide regression checks
		// happen in the all-fields-set case at the end.
		{"seed_set", func(r *types.ChatRequest) { r.Seed = intPtr(42) }, "seed", float64(42)},
		{"seed_zero_still_sent", func(r *types.ChatRequest) { r.Seed = intPtr(0) }, "seed", float64(0)},
		{"seed_unset_omits", func(r *types.ChatRequest) {}, "seed", nil},

		{"presence_penalty_set", func(r *types.ChatRequest) { r.PresencePenalty = 0.5 }, "presence_penalty", 0.5},
		{"presence_penalty_zero_omits", func(r *types.ChatRequest) { r.PresencePenalty = 0 }, "presence_penalty", nil},
		{"frequency_penalty_set", func(r *types.ChatRequest) { r.FrequencyPenalty = -1.2 }, "frequency_penalty", -1.2},

		{"logprobs_true", func(r *types.ChatRequest) { r.Logprobs = true }, "logprobs", true},
		{"logprobs_false_omits", func(r *types.ChatRequest) { r.Logprobs = false }, "logprobs", nil},
		{"top_logprobs_set", func(r *types.ChatRequest) { r.TopLogprobs = 5 }, "top_logprobs", float64(5)},

		{"logit_bias_passes_through", func(r *types.ChatRequest) {
			r.LogitBias = json.RawMessage(`{"50256":-100}`)
		}, "logit_bias", map[string]any{"50256": float64(-100)}},

		{"stream_options_passes_through", func(r *types.ChatRequest) {
			r.StreamOptions = json.RawMessage(`{"include_usage":true}`)
		}, "stream_options", map[string]any{"include_usage": true}},

		{"parallel_tool_calls_explicit_false", func(r *types.ChatRequest) {
			r.ParallelToolCalls = boolPtr(false)
		}, "parallel_tool_calls", false},
		{"parallel_tool_calls_explicit_true", func(r *types.ChatRequest) {
			r.ParallelToolCalls = boolPtr(true)
		}, "parallel_tool_calls", true},
		{"parallel_tool_calls_unset_omits", func(r *types.ChatRequest) {}, "parallel_tool_calls", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var captured map[string]any
			transport := testRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/v1/chat/completions" || r.Body == nil {
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader([]byte(`{}`)))}, nil
				}
				_ = json.NewDecoder(r.Body).Decode(&captured)
				body, _ := json.Marshal(openAIChatCompletionResponse{
					ID: "x", Model: "gpt-4o-mini",
					Choices: []openAIChatCompletionChoice{{
						Message:      openAIChatMessage{Role: "assistant", Content: openAIMessageContent{Text: "ok"}},
						FinishReason: "stop",
					}},
					Usage: openAIUsage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
				})
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(bytes.NewReader(body)),
				}, nil
			})
			provider := NewOpenAIProvider(config.OpenAICompatibleProviderConfig{
				Name: "openai", Kind: "cloud", BaseURL: "https://example.test",
				APIKey: "k", Timeout: time.Second, DefaultModel: "gpt-4o-mini",
			}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			provider.httpClient.Transport = transport
			provider.cachedCaps = Capabilities{
				Name: "openai", Kind: KindCloud,
				DefaultModel: "gpt-4o-mini",
				Models:       []string{"gpt-4o-mini"},
			}
			provider.capsExpiry = time.Now().Add(time.Minute)

			req := types.ChatRequest{
				Model:    "gpt-4o-mini",
				Messages: []types.Message{{Role: "user", Content: "hi"}},
			}
			tc.mutate(&req)
			if _, err := provider.Chat(context.Background(), req); err != nil {
				t.Fatalf("Chat: %v", err)
			}
			got, present := captured[tc.assertKey]
			switch {
			case tc.want == nil && present:
				t.Errorf("%s present on wire (=%v) but should be omitted", tc.assertKey, got)
			case tc.want != nil && !present:
				t.Errorf("%s absent on wire; want %v", tc.assertKey, tc.want)
			case tc.want != nil:
				wantBytes, _ := json.Marshal(tc.want)
				gotBytes, _ := json.Marshal(got)
				if string(wantBytes) != string(gotBytes) {
					t.Errorf("%s = %s, want %s", tc.assertKey, gotBytes, wantBytes)
				}
			}
		})
	}
}

// TestOpenAIProviderForwardsResponseFormat pins that the
// structured-output knob reaches the wire verbatim. Three cases:
// json_schema (most common), json_object (legacy), and the
// no-format default (field absent on wire — backward compat).
func TestOpenAIProviderForwardsResponseFormat(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		raw  json.RawMessage
		want any // nil = field absent on wire
	}{
		{"absent", nil, nil},
		{"json_object", json.RawMessage(`{"type":"json_object"}`), map[string]any{"type": "json_object"}},
		{"json_schema", json.RawMessage(`{"type":"json_schema","json_schema":{"name":"reply","schema":{"type":"object"}}}`), map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "reply",
				"schema": map[string]any{"type": "object"},
			},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var captured map[string]any
			transport := testRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/v1/chat/completions" || r.Body == nil {
					// Discovery / capabilities calls land here too;
					// they're irrelevant to this test, return an
					// empty 200 instead of trying to decode.
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader([]byte(`{}`)))}, nil
				}
				_ = json.NewDecoder(r.Body).Decode(&captured)
				body, _ := json.Marshal(openAIChatCompletionResponse{
					ID:    "chatcmpl-rf",
					Model: "gpt-4o-mini",
					Choices: []openAIChatCompletionChoice{{
						Index:        0,
						Message:      openAIChatMessage{Role: "assistant", Content: openAIMessageContent{Text: "{}"}},
						FinishReason: "stop",
					}},
					Usage: openAIUsage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
				})
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(bytes.NewReader(body)),
				}, nil
			})
			provider := NewOpenAIProvider(config.OpenAICompatibleProviderConfig{
				Name: "openai", Kind: "cloud", BaseURL: "https://example.test",
				APIKey: "k", Timeout: time.Second, DefaultModel: "gpt-4o-mini",
			}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			provider.httpClient.Transport = transport
			provider.cachedCaps = Capabilities{
				Name: "openai", Kind: KindCloud,
				DefaultModel: "gpt-4o-mini",
				Models:       []string{"gpt-4o-mini"},
			}
			provider.capsExpiry = time.Now().Add(time.Minute)
			_, err := provider.Chat(context.Background(), types.ChatRequest{
				Model:          "gpt-4o-mini",
				Messages:       []types.Message{{Role: "user", Content: "hi"}},
				ResponseFormat: tc.raw,
			})
			if err != nil {
				t.Fatalf("Chat: %v", err)
			}
			got, present := captured["response_format"]
			switch {
			case tc.want == nil && present:
				t.Errorf("response_format present on wire (=%v) but should be omitted for empty input", got)
			case tc.want != nil && !present:
				t.Errorf("response_format absent on wire; want %v", tc.want)
			case tc.want != nil:
				wantBytes, _ := json.Marshal(tc.want)
				gotBytes, _ := json.Marshal(got)
				if string(wantBytes) != string(gotBytes) {
					t.Errorf("response_format = %s, want %s", gotBytes, wantBytes)
				}
			}
		})
	}
}
