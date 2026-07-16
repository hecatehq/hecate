package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/pkg/types"
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

func TestDecodeUpstreamErrorRedactsInlineImagePayload(t *testing.T) {
	t.Parallel()

	payload := strings.Repeat("A", 128)
	body := `{"error":{"message":"invalid data:image/png;base64,` + payload + `","type":"invalid_request_error"}}`
	err := decodeUpstreamError(&http.Response{
		StatusCode: http.StatusBadRequest,
		Body:       io.NopCloser(strings.NewReader(body)),
	})
	upstreamErr, ok := err.(*UpstreamError)
	if !ok {
		t.Fatalf("error type = %T, want *UpstreamError", err)
	}
	if strings.Contains(upstreamErr.Message, payload) || !strings.Contains(upstreamErr.Message, "[redacted inline image]") {
		t.Fatalf("upstream message = %q", upstreamErr.Message)
	}
}

func TestDecodeUpstreamErrorRedactsRemoteImageURLCredentials(t *testing.T) {
	t.Parallel()

	const echoedURL = "https://operator:password@images.example.test/cat.png?X-Amz-Signature=private#access-token"
	body, err := json.Marshal(openAIErrorEnvelope{Error: openAIErrorDetail{
		Message: "image fetch failed: " + echoedURL,
		Type:    "invalid_request_error",
	}})
	if err != nil {
		t.Fatalf("marshal error envelope: %v", err)
	}
	decoded := decodeUpstreamError(&http.Response{
		StatusCode: http.StatusBadRequest,
		Body:       io.NopCloser(bytes.NewReader(body)),
	})
	upstreamErr, ok := decoded.(*UpstreamError)
	if !ok {
		t.Fatalf("error type = %T, want *UpstreamError", decoded)
	}
	want := "image fetch failed: https://[redacted]@images.example.test/cat.png?[redacted]#[redacted]"
	if upstreamErr.Message != want {
		t.Fatalf("upstream message = %q, want %q", upstreamErr.Message, want)
	}
}

func TestDecodeUpstreamErrorSanitizesUntrustedErrorType(t *testing.T) {
	t.Parallel()

	const secretType = "https://operator:password@errors.example.test/type?token=secret"
	body, err := json.Marshal(openAIErrorEnvelope{Error: openAIErrorDetail{
		Message: "request rejected",
		Type:    secretType,
	}})
	if err != nil {
		t.Fatalf("marshal error envelope: %v", err)
	}
	decoded := decodeUpstreamError(&http.Response{
		StatusCode: http.StatusBadRequest,
		Body:       io.NopCloser(bytes.NewReader(body)),
	})
	upstreamErr, ok := decoded.(*UpstreamError)
	if !ok {
		t.Fatalf("error type = %T, want *UpstreamError", decoded)
	}
	if upstreamErr.Type != "upstream_error" {
		t.Fatalf("upstream type = %q, want upstream_error", upstreamErr.Type)
	}
	if strings.Contains(upstreamErr.Error(), "password") || strings.Contains(upstreamErr.Error(), "secret") {
		t.Fatalf("UpstreamError.Error() leaked untrusted type: %q", upstreamErr.Error())
	}
}

func TestOpenAIProviderChatStreamReturnsSanitizedErrorFrame(t *testing.T) {
	t.Parallel()

	const (
		assistantURL = "https://assistant.example/image.png?visible=true"
		errorURL     = "https://operator:password@images.example/image.png?signature=secret"
	)
	transport := testRoundTripperFunc(func(*http.Request) (*http.Response, error) {
		stream := "data: {\"choices\":[{\"delta\":{\"content\":" + strconv.Quote(assistantURL) + "}}]}\n\n" +
			"data: {\"error\":{\"message\":" + strconv.Quote("image fetch failed: "+errorURL) + ",\"type\":" + strconv.Quote(errorURL) + "}}\n\n"
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(stream)),
		}, nil
	})
	provider := NewOpenAICompatibleProvider(config.OpenAICompatibleProviderConfig{
		Name: "custom", Kind: "cloud", BaseURL: "https://api.example.test", APIKey: "test-key", Timeout: time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	provider.httpClient.Transport = transport

	var output strings.Builder
	err := provider.ChatStream(context.Background(), types.ChatRequest{
		Model: "model-a", Messages: []types.Message{{Role: "user", Content: "describe"}},
	}, &output)
	var upstreamErr *UpstreamError
	if !errors.As(err, &upstreamErr) {
		t.Fatalf("ChatStream() error = %v, want *UpstreamError", err)
	}
	if !strings.Contains(output.String(), assistantURL) {
		t.Fatalf("ordinary assistant content was altered: %q", output.String())
	}
	if strings.Contains(output.String(), "password") || strings.Contains(output.String(), "signature=secret") {
		t.Fatalf("stream output leaked provider error frame: %q", output.String())
	}
	if upstreamErr.Type != "upstream_error" || strings.Contains(upstreamErr.Message, "password") || strings.Contains(upstreamErr.Message, "signature=secret") {
		t.Fatalf("sanitized stream error = %+v", upstreamErr)
	}
}

func TestProxySSEAcceptsProtocolValidOpenAITerminatorSpacing(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		terminal string
	}{
		{name: "no optional space", terminal: "data:[DONE]"},
		{name: "optional space", terminal: "data: [DONE]"},
		{name: "surrounding whitespace", terminal: "data: \t[DONE] \t"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var output strings.Builder
			if err := proxySSE(context.Background(), strings.NewReader(test.terminal+"\r\n"), &output); err != nil {
				t.Fatalf("proxySSE() error = %v", err)
			}
			if !strings.Contains(output.String(), test.terminal) {
				t.Fatalf("proxySSE() output = %q, want original terminal frame", output.String())
			}
		})
	}
}

func TestOpenAIProviderChatStreamRejectsCleanEOFMissingTerminator(t *testing.T) {
	t.Parallel()

	transport := testRoundTripperFunc(func(*http.Request) (*http.Response, error) {
		stream := `data: {"id":"chatcmpl-partial","choices":[{"delta":{"content":"partial"},"finish_reason":null}]}` + "\n\n"
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(stream)),
		}, nil
	})
	provider := NewOpenAICompatibleProvider(config.OpenAICompatibleProviderConfig{
		Name: "custom", Kind: "cloud", BaseURL: "https://api.example.test", APIKey: "test-key", Timeout: time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	provider.httpClient.Transport = transport

	var output strings.Builder
	err := provider.ChatStream(context.Background(), types.ChatRequest{
		Model: "model-a", Messages: []types.Message{{Role: "user", Content: "describe"}},
	}, &output)
	var upstreamErr *UpstreamError
	if !errors.As(err, &upstreamErr) {
		t.Fatalf("ChatStream() error = %v, want *UpstreamError", err)
	}
	if upstreamErr.StatusCode != http.StatusBadGateway || upstreamErr.Type != "upstream_error" ||
		upstreamErr.Message != "OpenAI-compatible stream ended before [DONE]" {
		t.Fatalf("ChatStream() error = %+v, want sanitized premature-EOF contract", upstreamErr)
	}
	if !strings.Contains(output.String(), "partial") {
		t.Fatalf("ChatStream() output = %q, want already-delivered partial frame preserved", output.String())
	}
}

func TestDecodeOpenAIStreamErrorFailsClosedForNoncanonicalVariants(t *testing.T) {
	t.Parallel()

	const secretURL = "https://operator:password@images.example/image.png?signature=secret"
	tests := []struct {
		name      string
		line      string
		wantError bool
	}{
		{name: "string", line: `data: {"error":"fetch failed: ` + secretURL + `"}`, wantError: true},
		{name: "malformed object", line: `data: {"error":{"message":42,"debug":"` + secretURL + `"}}`, wantError: true},
		{name: "array", line: `data: {"error":["` + secretURL + `"]}`, wantError: true},
		{name: "null", line: `data: {"error":null}`, wantError: false},
		{name: "ordinary chunk", line: `data: {"choices":[{"delta":{"content":"ok"}}]}`, wantError: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstreamErr := decodeOpenAIStreamError(test.line)
			if (upstreamErr != nil) != test.wantError {
				t.Fatalf("decodeOpenAIStreamError() = %v, wantError=%v", upstreamErr, test.wantError)
			}
			if upstreamErr != nil && (strings.Contains(upstreamErr.Error(), "password") || strings.Contains(upstreamErr.Error(), "signature=secret")) {
				t.Fatalf("stream error leaked noncanonical payload: %q", upstreamErr.Error())
			}
		})
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

func TestOpenAIProviderFireworksDiscoveryUsesModelsEndpoint(t *testing.T) {
	t.Parallel()

	var calls int
	pageTokens := make([]string, 0, 2)
	transport := testRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodGet {
			return nil, fmt.Errorf("method = %s, want GET", r.Method)
		}
		if got := r.URL.Scheme + "://" + r.URL.Host + r.URL.Path; got != "https://models.proxy.example/v1/accounts/fireworks/models" {
			return nil, fmt.Errorf("url path = %s, want Fireworks models endpoint", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer fireworks-key" {
			return nil, fmt.Errorf("Authorization = %q, want Bearer fireworks-key", got)
		}
		query := r.URL.Query()
		if got := query.Get("pageSize"); got != "200" {
			return nil, fmt.Errorf("pageSize = %q, want 200", got)
		}
		pageToken := query.Get("pageToken")
		pageTokens = append(pageTokens, pageToken)
		switch pageToken {
		case "":
			return jsonHTTPResponse(fireworksModelsResponse{
				Models: []fireworksModel{
					{
						Name:               "accounts/fireworks/models/deepseek-v3p1",
						SupportsTools:      true,
						SupportsImageInput: true,
						ContextLength:      131072,
					},
				},
				NextPageToken: "next-page",
			})
		case "next-page":
			return jsonHTTPResponse(fireworksModelsResponse{Models: []fireworksModel{
				{ID: "accounts/fireworks/models/llama-v3p1"},
			}})
		default:
			return nil, fmt.Errorf("unexpected pageToken = %q", pageToken)
		}
	})

	provider := NewOpenAICompatibleProvider(config.OpenAICompatibleProviderConfig{
		Name:           "Fireworks Production",
		ProviderFamily: "fireworks",
		Kind:           "cloud",
		BaseURL:        "https://inference.proxy.example/v1",
		ModelsPath:     "https://models.proxy.example/v1/accounts/fireworks/models",
		APIKey:         "fireworks-key",
		Timeout:        time.Second,
		DefaultModel:   "accounts/fireworks/models/deepseek-v3p1",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	provider.httpClient.Transport = transport

	caps, err := provider.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("discovery call count = %d, want 2", calls)
	}
	if len(pageTokens) != 2 || pageTokens[0] != "" || pageTokens[1] != "next-page" {
		t.Fatalf("page tokens = %#v, want first page then next-page", pageTokens)
	}
	if len(caps.Models) != 2 || caps.Models[0] != "accounts/fireworks/models/deepseek-v3p1" {
		t.Fatalf("models = %#v, want Fireworks model names", caps.Models)
	}
	if caps.DiscoverySource != "fireworks_models" {
		t.Fatalf("discovery source = %q, want fireworks_models", caps.DiscoverySource)
	}
	capability := caps.ModelCapabilities["accounts/fireworks/models/deepseek-v3p1"]
	if capability.ToolCalling != "basic" || capability.ImageInput != "supported" || capability.MaxContextTokens != 131072 {
		t.Fatalf("model capability = %+v, want tools, images, and context length", capability)
	}
}

func TestOpenAIProviderFireworksDiscoveryStopsOnRepeatedPageToken(t *testing.T) {
	t.Parallel()

	var calls int
	transport := testRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		switch r.URL.Query().Get("pageToken") {
		case "":
			return jsonHTTPResponse(fireworksModelsResponse{
				Models:        []fireworksModel{{Name: "accounts/fireworks/models/first"}},
				NextPageToken: "repeat-token",
			})
		case "repeat-token":
			return jsonHTTPResponse(fireworksModelsResponse{
				Models:        []fireworksModel{{Name: "accounts/fireworks/models/second"}},
				NextPageToken: "repeat-token",
			})
		default:
			return nil, fmt.Errorf("unexpected pageToken = %q", r.URL.Query().Get("pageToken"))
		}
	})

	provider := NewOpenAICompatibleProvider(config.OpenAICompatibleProviderConfig{
		Name:       "fireworks-repeated-cursor",
		Kind:       "cloud",
		BaseURL:    "https://api.fireworks.ai/inference/v1",
		ModelsPath: "https://api.fireworks.ai/v1/accounts/fireworks/models",
		APIKey:     "fireworks-key",
		Timeout:    time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	provider.httpClient.Transport = transport

	caps, err := provider.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("discovery call count = %d, want 2 before repeated cursor stops pagination", calls)
	}
	if len(caps.Models) != 2 {
		t.Fatalf("models = %#v, want both pages before repeated cursor", caps.Models)
	}
}

func TestOpenAIProviderLMStudioDiscoveryUsesNativeModelsEndpoint(t *testing.T) {
	t.Parallel()

	var calls int
	transport := testRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodGet {
			return nil, fmt.Errorf("method = %s, want GET", r.Method)
		}
		if got := r.URL.String(); got != "http://127.0.0.1:1234/api/v1/models" {
			return nil, fmt.Errorf("url = %s, want LM Studio native models endpoint", got)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{
				"models": [
					{
						"key": "qwen/qwen3-4b",
						"type": "llm",
						"max_context_length": 32768,
						"capabilities": {"trained_for_tool_use": true}
					},
					{"key": "text-embedding-nomic", "type": "embedding"}
				]
			}`)),
		}, nil
	})

	provider := NewOpenAICompatibleProvider(config.OpenAICompatibleProviderConfig{
		Name:           "LM Studio Production",
		ProviderFamily: "lmstudio",
		Kind:           "local",
		BaseURL:        "http://127.0.0.1:1234/v1",
		Timeout:        time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	provider.httpClient.Transport = transport

	caps, err := provider.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("discovery call count = %d, want 1", calls)
	}
	if len(caps.Models) != 1 || caps.Models[0] != "qwen/qwen3-4b" {
		t.Fatalf("models = %#v, want LM Studio LLM model keys", caps.Models)
	}
	if caps.DefaultModel != "qwen/qwen3-4b" {
		t.Fatalf("default model = %q, want first discovered LM Studio model", caps.DefaultModel)
	}
	if caps.DiscoverySource != "lmstudio_api_models" {
		t.Fatalf("discovery source = %q, want lmstudio_api_models", caps.DiscoverySource)
	}
	capability := caps.ModelCapabilities["qwen/qwen3-4b"]
	if capability.ToolCalling != "basic" || capability.MaxContextTokens != 32768 || !capability.Streaming {
		t.Fatalf("model capability = %+v, want native LM Studio tools/context/streaming", capability)
	}
}

func TestOpenAIProviderLMStudioDiscoveryFallsBackToOpenAIModelsEndpoint(t *testing.T) {
	t.Parallel()

	var paths []string
	transport := testRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/api/v1/models":
			return nil, fmt.Errorf("LM Studio native endpoint unavailable")
		case "/v1/models":
			return jsonHTTPResponse(openAIModelsResponse{Data: []openAIModel{{ID: "fallback-model"}}})
		default:
			return nil, fmt.Errorf("path = %s, want /api/v1/models or /v1/models", r.URL.Path)
		}
	})

	provider := NewOpenAICompatibleProvider(config.OpenAICompatibleProviderConfig{
		Name:    "LM Studio",
		Kind:    "local",
		BaseURL: "http://127.0.0.1:1234/v1",
		Timeout: time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	provider.httpClient.Transport = transport

	caps, err := provider.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities() error = %v", err)
	}
	if strings.Join(paths, ",") != "/api/v1/models,/v1/models" {
		t.Fatalf("request paths = %#v, want native LM Studio then OpenAI fallback", paths)
	}
	if len(caps.Models) != 1 || caps.Models[0] != "fallback-model" {
		t.Fatalf("models = %#v, want fallback OpenAI model list", caps.Models)
	}
	if caps.DiscoverySource != "upstream_v1_models" {
		t.Fatalf("discovery source = %q, want upstream_v1_models fallback", caps.DiscoverySource)
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
				return jsonHTTPResponse(ollamaShowResponse{Capabilities: []string{"completion", "tools", "vision"}})
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
		Name:           "Team Ollama",
		ProviderFamily: "ollama",
		Kind:           "local",
		BaseURL:        "http://127.0.0.1:11434/v1",
		Timeout:        time.Second,
		DefaultModel:   "qwen2.5-coder:7b",
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
	if got := caps.ModelCapabilities["qwen2.5-coder:7b"].ImageInput; got != "supported" {
		t.Fatalf("qwen image input = %q, want supported", got)
	}
	if got := caps.ModelCapabilities["smollm2:135m"].ImageInput; got != "none" {
		t.Fatalf("smollm2 image input = %q, want none", got)
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
		Name:           "ollama",
		ProviderFamily: "ollama",
		Kind:           "local",
		BaseURL:        "http://127.0.0.1:11434/v1",
		Timeout:        time.Second,
		DefaultModel:   "model-0",
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
		Name:           "ollama",
		ProviderFamily: "ollama",
		Kind:           "local",
		BaseURL:        "http://127.0.0.1:11434/v1",
		Timeout:        time.Second,
		DefaultModel:   "model-0",
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

func TestBuildOpenAIWireContentAcceptsCanonicalImageShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		block   types.ContentBlock
		wantURL string
		detail  string
	}{
		{
			name: "image_url with URL",
			block: types.ContentBlock{
				Type:  "image_url",
				Image: &types.ContentImage{URL: "https://example.com/cat.png", Detail: "high"},
			},
			wantURL: "https://example.com/cat.png",
			detail:  "high",
		},
		{
			name: "image with inline data",
			block: types.ContentBlock{
				Type: "image",
				Image: &types.ContentImage{
					Data:      "iVBORw0KGgo=",
					MediaType: "image/png",
				},
			},
			wantURL: "data:image/png;base64,iVBORw0KGgo=",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildOpenAIWireContent(types.Message{
				Role:          "user",
				ContentBlocks: []types.ContentBlock{tt.block},
			})
			if len(got.Blocks) != 1 || got.Blocks[0].Type != "image_url" || got.Blocks[0].ImageURL == nil {
				t.Fatalf("wire content = %+v, want one OpenAI image_url block", got)
			}
			if got.Blocks[0].ImageURL.URL != tt.wantURL || got.Blocks[0].ImageURL.Detail != tt.detail {
				t.Fatalf("image_url = %+v, want URL %q detail %q", got.Blocks[0].ImageURL, tt.wantURL, tt.detail)
			}
		})
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
