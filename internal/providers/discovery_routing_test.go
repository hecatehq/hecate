package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestOpenAIProviderChatAcceptsDiscoveredModelWithoutConfiguredModelList(t *testing.T) {
	t.Parallel()

	provider := NewOpenAICompatibleProvider(config.OpenAICompatibleProviderConfig{
		Name:     "test",
		Kind:     "cloud",
		Protocol: "openai",
		BaseURL:  "https://example.test",
		APIKey:   "secret",
		Timeout:  2 * time.Second,
	}, nil)
	provider.cachedCaps = Capabilities{
		Name:         "test",
		Kind:         KindCloud,
		DefaultModel: "discovered-model",
		Models:       []string{"discovered-model"},
	}
	provider.capsExpiry = time.Now().Add(time.Minute)
	provider.httpClient = &http.Client{
		Transport: discoveryRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/v1/chat/completions" {
				t.Fatalf("path = %q, want /v1/chat/completions", r.URL.Path)
			}
			payload, _ := json.Marshal(map[string]any{
				"id":      "chatcmpl-test",
				"created": time.Now().Unix(),
				"model":   "discovered-model",
				"choices": []map[string]any{
					{
						"index":         0,
						"finish_reason": "stop",
						"message": map[string]string{
							"role":    "assistant",
							"content": "ok",
						},
					},
				},
				"usage": map[string]int{
					"prompt_tokens":     4,
					"completion_tokens": 1,
					"total_tokens":      5,
				},
			})
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(string(payload))),
				Header:     make(http.Header),
			}, nil
		}),
	}

	response, err := provider.Chat(context.Background(), types.ChatRequest{
		Model: "discovered-model",
		Messages: []types.Message{
			{Role: "user", Content: "hello"},
		},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if response.Model != "discovered-model" {
		t.Fatalf("response model = %q, want discovered-model", response.Model)
	}
}

type discoveryRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn discoveryRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
