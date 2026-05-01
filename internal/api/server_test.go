package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hecate/agent-runtime/internal/auth"
	"github.com/hecate/agent-runtime/internal/billing"
	"github.com/hecate/agent-runtime/internal/cache"
	"github.com/hecate/agent-runtime/internal/catalog"
	"github.com/hecate/agent-runtime/internal/chatstate"
	"github.com/hecate/agent-runtime/internal/config"
	"github.com/hecate/agent-runtime/internal/controlplane"
	"github.com/hecate/agent-runtime/internal/gateway"
	"github.com/hecate/agent-runtime/internal/governor"
	"github.com/hecate/agent-runtime/internal/profiler"
	"github.com/hecate/agent-runtime/internal/providers"
	"github.com/hecate/agent-runtime/internal/ratelimit"
	"github.com/hecate/agent-runtime/internal/retention"
	"github.com/hecate/agent-runtime/internal/router"
	"github.com/hecate/agent-runtime/internal/telemetry"
	"github.com/hecate/agent-runtime/pkg/types"
)

func TestChatCompletionsCachesResponsesAndReturnsRuntimeHeaders(t *testing.T) {
	t.Parallel()

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))
	provider := &fakeProvider{
		name: "openai",
		response: &types.ChatResponse{
			ID:        "chatcmpl-123",
			Model:     "gpt-4o-mini-2024-07-18",
			CreatedAt: time.Unix(1_700_000_000, 0).UTC(),
			Choices: []types.ChatChoice{
				{
					Index: 0,
					Message: types.Message{
						Role:    "assistant",
						Content: "Hello!",
					},
					FinishReason: "stop",
				},
			},
			Usage: types.Usage{
				PromptTokens:     14,
				CompletionTokens: 2,
				TotalTokens:      16,
			},
		},
	}

	handler := newTestHTTPHandler(logger, provider)
	body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Say hello in one short sentence."}]}`

	first := performJSONRequest(t, handler, body)
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want %d, body=%s", first.Code, http.StatusOK, first.Body.String())
	}
	if got := first.Header().Get("X-Runtime-Cache"); got != "false" {
		t.Fatalf("first X-Runtime-Cache = %q, want false", got)
	}
	if got := first.Header().Get("X-Runtime-Provider"); got != "openai" {
		t.Fatalf("X-Runtime-Provider = %q, want openai", got)
	}
	if got := first.Header().Get("X-Runtime-Provider-Kind"); got != "cloud" {
		t.Fatalf("X-Runtime-Provider-Kind = %q, want cloud", got)
	}
	if got := first.Header().Get("X-Runtime-Requested-Model"); got != "gpt-4o-mini" {
		t.Fatalf("X-Runtime-Requested-Model = %q, want gpt-4o-mini", got)
	}
	if got := first.Header().Get("X-Runtime-Requested-Model-Canonical"); got != "gpt-4o-mini" {
		t.Fatalf("X-Runtime-Requested-Model-Canonical = %q, want gpt-4o-mini", got)
	}
	if got := first.Header().Get("X-Runtime-Model"); got != "gpt-4o-mini-2024-07-18" {
		t.Fatalf("X-Runtime-Model = %q, want dated model", got)
	}
	if got := first.Header().Get("X-Runtime-Model-Canonical"); got != "gpt-4o-mini" {
		t.Fatalf("X-Runtime-Model-Canonical = %q, want gpt-4o-mini", got)
	}
	if got := first.Header().Get("X-Runtime-Cost-USD"); got != "0.000003" {
		t.Fatalf("X-Runtime-Cost-USD = %q, want 0.000003", got)
	}
	if got := first.Header().Get("X-Request-Id"); got == "" {
		t.Fatal("X-Request-Id = empty, want generated request id")
	}
	if got := first.Header().Get("X-Trace-Id"); got == "" {
		t.Fatal("X-Trace-Id = empty, want trace id")
	}
	if got := first.Header().Get("X-Span-Id"); got == "" {
		t.Fatal("X-Span-Id = empty, want span id")
	}

	second := performJSONRequest(t, handler, body)
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d, want %d, body=%s", second.Code, http.StatusOK, second.Body.String())
	}
	if got := second.Header().Get("X-Runtime-Cache"); got != "true" {
		t.Fatalf("second X-Runtime-Cache = %q, want true", got)
	}
	if got := second.Header().Get("X-Runtime-Cache-Type"); got != "exact" {
		t.Fatalf("second X-Runtime-Cache-Type = %q, want exact", got)
	}

	if provider.CallCount() != 1 {
		t.Fatalf("provider call count = %d, want 1 due to cache hit", provider.CallCount())
	}

	var response OpenAIChatCompletionResponse
	if err := json.NewDecoder(bytes.NewReader(second.Body.Bytes())).Decode(&response); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if response.Model != "gpt-4o-mini-2024-07-18" {
		t.Fatalf("response model = %q, want resolved model", response.Model)
	}
	if got := response.Choices[0].Message.Content.AsString(); got != "Hello!" {
		t.Fatalf("response content = %q, want Hello!", got)
	}

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, `"msg":"gen_ai.gateway.request"`) {
		t.Fatalf("log output missing gen_ai.gateway.request entry: %s", logOutput)
	}
	if !strings.Contains(logOutput, `"gen_ai.request.model":"gpt-4o-mini"`) {
		t.Fatalf("log output missing gen_ai.request.model: %s", logOutput)
	}
	if !strings.Contains(logOutput, `"hecate.model.requested_canonical":"gpt-4o-mini"`) {
		t.Fatalf("log output missing hecate.model.requested_canonical: %s", logOutput)
	}
	if !strings.Contains(logOutput, `"gen_ai.response.model":"gpt-4o-mini-2024-07-18"`) {
		t.Fatalf("log output missing gen_ai.response.model: %s", logOutput)
	}
	if !strings.Contains(logOutput, `"hecate.model.resolved_canonical":"gpt-4o-mini"`) {
		t.Fatalf("log output missing hecate.model.resolved_canonical: %s", logOutput)
	}
	if !strings.Contains(logOutput, `"hecate.cache.hit":true`) {
		t.Fatalf("log output missing hecate.cache.hit true entry: %s", logOutput)
	}
}

func TestChatCompletionsSemanticCacheHitsSimilarPrompt(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{
		name: "ollama",
		capabilities: providers.Capabilities{
			Name:         "ollama",
			Kind:         providers.KindLocal,
			DefaultModel: "llama3.1:8b",
			Models:       []string{"llama3.1:8b"},
		},
		response: &types.ChatResponse{
			ID:        "chatcmpl-local-1",
			Model:     "llama3.1:8b",
			CreatedAt: time.Unix(1_700_000_000, 0).UTC(),
			Choices: []types.ChatChoice{{
				Index: 0,
				Message: types.Message{
					Role:    "assistant",
					Content: "Channels coordinate goroutines.",
				},
				FinishReason: "stop",
			}},
			Usage: types.Usage{PromptTokens: 20, CompletionTokens: 4, TotalTokens: 24},
		},
	}

	handler := newTestHTTPHandlerWithConfig(logger, provider, config.Config{
		Cache: config.CacheConfig{
			Semantic: config.SemanticCacheConfig{
				Enabled:       true,
				Backend:       "memory",
				DefaultTTL:    time.Hour,
				MinSimilarity: 0.6,
				MaxEntries:    100,
				MaxTextChars:  2048,
			},
		},
	})

	first := performJSONRequest(t, handler, `{"model":"llama3.1:8b","messages":[{"role":"user","content":"Explain Go channels and goroutines."}]}`)
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want %d, body=%s", first.Code, http.StatusOK, first.Body.String())
	}
	second := performJSONRequest(t, handler, `{"model":"llama3.1:8b","messages":[{"role":"user","content":"Explain goroutines and channels in Go."}]}`)
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d, want %d, body=%s", second.Code, http.StatusOK, second.Body.String())
	}
	if got := second.Header().Get("X-Runtime-Cache"); got != "true" {
		t.Fatalf("second X-Runtime-Cache = %q, want true", got)
	}
	if got := second.Header().Get("X-Runtime-Cache-Type"); got != "semantic" {
		t.Fatalf("second X-Runtime-Cache-Type = %q, want semantic", got)
	}
	if got := second.Header().Get("X-Runtime-Semantic-Strategy"); got != "memory_scan" {
		t.Fatalf("second X-Runtime-Semantic-Strategy = %q, want memory_scan", got)
	}
	if got := second.Header().Get("X-Runtime-Semantic-Similarity"); got == "" {
		t.Fatal("second X-Runtime-Semantic-Similarity = empty, want value")
	}
	if provider.CallCount() != 1 {
		t.Fatalf("provider call count = %d, want 1 due to semantic cache hit", provider.CallCount())
	}
}

func TestChatCompletionsExactCacheIsolatedByUserScope(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{
		name: "openai",
		response: &types.ChatResponse{
			ID:        "chatcmpl-tenant",
			Model:     "gpt-4o-mini",
			CreatedAt: time.Unix(1_700_000_000, 0).UTC(),
			Choices:   []types.ChatChoice{{Index: 0, Message: types.Message{Role: "assistant", Content: "Hello!"}, FinishReason: "stop"}},
			Usage:     types.Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12},
		},
	}

	handler := newTestHTTPHandler(logger, provider)

	first := performJSONRequest(t, handler, `{"model":"gpt-4o-mini","user":"team-a","messages":[{"role":"user","content":"Say hello."}]}`)
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want %d, body=%s", first.Code, http.StatusOK, first.Body.String())
	}
	second := performJSONRequest(t, handler, `{"model":"gpt-4o-mini","user":"team-b","messages":[{"role":"user","content":"Say hello."}]}`)
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d, want %d, body=%s", second.Code, http.StatusOK, second.Body.String())
	}
	if got := second.Header().Get("X-Runtime-Cache"); got != "false" {
		t.Fatalf("second X-Runtime-Cache = %q, want false due to user isolation", got)
	}
	if provider.CallCount() != 2 {
		t.Fatalf("provider call count = %d, want 2 due to isolated cache scope", provider.CallCount())
	}
}

func TestRetentionRunAndListEndpointsPersistHistory(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{
		name: "openai",
		response: &types.ChatResponse{
			ID:        "chatcmpl-123",
			Model:     "gpt-4o-mini",
			CreatedAt: time.Now().UTC(),
			Choices:   []types.ChatChoice{{Index: 0, Message: types.Message{Role: "assistant", Content: "Hello!"}, FinishReason: "stop"}},
			Usage:     types.Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12},
		},
	}

	handler := newTestHTTPHandlerWithConfig(logger, provider, config.Config{
		Server: config.ServerConfig{
			AuthToken: "admin-secret",
		},
	})
	admin := newAPITestClient(t, handler).withBearerToken("admin-secret")

	admin.mustRequest(http.MethodPost, "/admin/retention/run", `{"subsystems":["trace_snapshots"]}`)
	response := mustRequestJSON[RetentionRunsResponse](admin, http.MethodGet, "/admin/retention/runs?limit=5", "")
	if len(response.Data) != 1 {
		t.Fatalf("retention runs = %d, want 1", len(response.Data))
	}
	if response.Data[0].Trigger != "manual" {
		t.Fatalf("trigger = %q, want manual", response.Data[0].Trigger)
	}
	if response.Data[0].Actor == "" {
		t.Fatal("actor = empty, want populated admin actor")
	}
}

func TestChatCompletionsExactCacheIsolatedByExplicitProvider(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	openAI := &fakeProvider{
		name: "openai",
		response: &types.ChatResponse{
			ID:        "chatcmpl-openai",
			Model:     "gpt-4o-mini",
			CreatedAt: time.Unix(1_700_000_000, 0).UTC(),
			Choices:   []types.ChatChoice{{Index: 0, Message: types.Message{Role: "assistant", Content: "cloud"}, FinishReason: "stop"}},
			Usage:     types.Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12},
		},
	}
	anthropic := &fakeProvider{
		name: "anthropic",
		response: &types.ChatResponse{
			ID:        "chatcmpl-anthropic",
			Model:     "gpt-4o-mini",
			CreatedAt: time.Unix(1_700_000_000, 0).UTC(),
			Choices:   []types.ChatChoice{{Index: 0, Message: types.Message{Role: "assistant", Content: "other cloud"}, FinishReason: "stop"}},
			Usage:     types.Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12},
		},
	}

	handler := newTestHTTPHandlerForProviders(logger, []providers.Provider{openAI, anthropic}, config.Config{
		Router: config.RouterConfig{
			DefaultModel: "gpt-4o-mini",
		},
	})

	first := performJSONRequest(t, handler, `{"model":"gpt-4o-mini","provider":"openai","messages":[{"role":"user","content":"Say hello."}]}`)
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want %d, body=%s", first.Code, http.StatusOK, first.Body.String())
	}
	second := performJSONRequest(t, handler, `{"model":"gpt-4o-mini","provider":"anthropic","messages":[{"role":"user","content":"Say hello."}]}`)
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d, want %d, body=%s", second.Code, http.StatusOK, second.Body.String())
	}
	if got := second.Header().Get("X-Runtime-Cache"); got != "false" {
		t.Fatalf("second X-Runtime-Cache = %q, want false due to provider isolation", got)
	}
	if got := second.Header().Get("X-Runtime-Provider"); got != "anthropic" {
		t.Fatalf("second X-Runtime-Provider = %q, want anthropic", got)
	}
	if openAI.CallCount() != 1 {
		t.Fatalf("openai call count = %d, want 1", openAI.CallCount())
	}
	if anthropic.CallCount() != 1 {
		t.Fatalf("anthropic call count = %d, want 1", anthropic.CallCount())
	}
}

func TestChatCompletionsMapsUpstreamErrors(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{
		name: "openai",
		err: &providers.UpstreamError{
			StatusCode: http.StatusTooManyRequests,
			Message:    "rate limit exceeded",
			Type:       "rate_limit_error",
		},
	}

	handler := newTestHTTPHandler(logger, provider)
	response := performJSONRequest(t, handler, `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hello"}]}`)

	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d, body=%s", response.Code, http.StatusTooManyRequests, response.Body.String())
	}

	var payload map[string]map[string]any
	if err := json.NewDecoder(bytes.NewReader(response.Body.Bytes())).Decode(&payload); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if payload["error"]["type"] != errCodeProviderRateLimited {
		t.Fatalf("error type = %#v, want %s", payload["error"]["type"], errCodeProviderRateLimited)
	}
	if payload["error"]["message"] != "rate limit exceeded" {
		t.Fatalf("error message = %#v, want rate limit exceeded", payload["error"]["message"])
	}
}

func TestTraceEndpointReturnsRecordedRequestTimeline(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{
		name: "openai",
		response: &types.ChatResponse{
			ID:        "chatcmpl-123",
			Model:     "gpt-4o-mini",
			CreatedAt: time.Unix(1_700_000_000, 0).UTC(),
			Choices: []types.ChatChoice{{
				Index: 0,
				Message: types.Message{
					Role:    "assistant",
					Content: "Hello!",
				},
				FinishReason: "stop",
			}},
			Usage: types.Usage{
				PromptTokens:     14,
				CompletionTokens: 2,
				TotalTokens:      16,
			},
		},
	}

	handler := newTestHTTPHandler(logger, provider)
	chat := performJSONRequest(t, handler, `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hello"}]}`)
	if chat.Code != http.StatusOK {
		t.Fatalf("chat status = %d, want %d, body=%s", chat.Code, http.StatusOK, chat.Body.String())
	}
	client := newAPITestClient(t, handler)
	payload := mustRequestJSON[TraceResponse](client, http.MethodGet, "/v1/traces?request_id="+chat.Header().Get("X-Request-Id"), "")
	if payload.Object != "trace" {
		t.Fatalf("object = %q, want trace", payload.Object)
	}
	if payload.Data.RequestID == "" {
		t.Fatal("request_id = empty, want request id")
	}
	if payload.Data.TraceID == "" {
		t.Fatal("trace_id = empty, want trace id")
	}
	if len(payload.Data.Spans) == 0 {
		t.Fatal("spans = empty, want span list")
	}
	if payload.Data.Spans[0].Name != "gateway.request" {
		t.Fatalf("first span = %q, want gateway.request", payload.Data.Spans[0].Name)
	}
	if payload.Data.Spans[0].Attributes[telemetry.AttrServiceName] != "hecate-gateway" {
		t.Fatalf("root span %s = %#v, want hecate-gateway", telemetry.AttrServiceName, payload.Data.Spans[0].Attributes[telemetry.AttrServiceName])
	}
	foundResponseSpan := false
	for _, span := range payload.Data.Spans {
		if span.Name == "gateway.response" {
			foundResponseSpan = true
			break
		}
	}
	if !foundResponseSpan {
		t.Fatalf("missing gateway.response span: %#v", payload.Data.Spans)
	}
}

func TestChatCompletionsRetriesTransientProviderFailure(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{
		name: "openai",
		errSequence: []error{
			&providers.UpstreamError{StatusCode: http.StatusServiceUnavailable, Message: "temporary outage", Type: "server_error"},
			nil,
		},
		response: &types.ChatResponse{
			ID:        "chatcmpl-retry",
			Model:     "gpt-4o-mini",
			CreatedAt: time.Unix(1_700_000_000, 0).UTC(),
			Choices: []types.ChatChoice{{
				Index: 0,
				Message: types.Message{
					Role:    "assistant",
					Content: "Recovered after retry.",
				},
				FinishReason: "stop",
			}},
			Usage: types.Usage{PromptTokens: 10, CompletionTokens: 4, TotalTokens: 14},
		},
	}

	handler := newTestHTTPHandlerWithConfig(logger, provider, config.Config{
		Provider: config.ProviderConfig{
			MaxAttempts:     2,
			RetryBackoff:    time.Millisecond,
			FailoverEnabled: true,
		},
	})
	response := performJSONRequest(t, handler, `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hello"}]}`)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if got := response.Header().Get("X-Runtime-Attempts"); got != "2" {
		t.Fatalf("X-Runtime-Attempts = %q, want 2", got)
	}
	if got := response.Header().Get("X-Runtime-Retries"); got != "1" {
		t.Fatalf("X-Runtime-Retries = %q, want 1", got)
	}
	if got := response.Header().Get("X-Runtime-Fallback-From"); got != "" {
		t.Fatalf("X-Runtime-Fallback-From = %q, want empty", got)
	}
	if provider.CallCount() != 2 {
		t.Fatalf("provider call count = %d, want 2", provider.CallCount())
	}
}

func TestChatCompletionsFailsOverToConfiguredProvider(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
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
			&providers.UpstreamError{StatusCode: http.StatusBadGateway, Message: "local runtime unavailable", Type: "server_error"},
		},
		response: &types.ChatResponse{
			ID:        "chatcmpl-local",
			Model:     "llama3.1:8b",
			CreatedAt: time.Unix(1_700_000_000, 0).UTC(),
			Choices:   []types.ChatChoice{{Index: 0, Message: types.Message{Role: "assistant", Content: "local"}, FinishReason: "stop"}},
			Usage:     types.Usage{PromptTokens: 10, CompletionTokens: 4, TotalTokens: 14},
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
			ID:        "chatcmpl-cloud",
			Model:     "gpt-4o-mini",
			CreatedAt: time.Unix(1_700_000_000, 0).UTC(),
			Choices:   []types.ChatChoice{{Index: 0, Message: types.Message{Role: "assistant", Content: "cloud fallback"}, FinishReason: "stop"}},
			Usage:     types.Usage{PromptTokens: 12, CompletionTokens: 5, TotalTokens: 17},
		},
	}

	handler := newTestHTTPHandlerForProviders(logger, []providers.Provider{localProvider, cloudProvider}, config.Config{
		Provider: config.ProviderConfig{
			MaxAttempts:     1,
			RetryBackoff:    time.Millisecond,
			FailoverEnabled: true,
		},
		Router: config.RouterConfig{
			DefaultModel: "gpt-4o-mini",
		},
	})
	response := performJSONRequest(t, handler, `{"messages":[{"role":"user","content":"hello"}]}`)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if got := response.Header().Get("X-Runtime-Provider"); got != "openai" {
		t.Fatalf("X-Runtime-Provider = %q, want openai", got)
	}
	if got := response.Header().Get("X-Runtime-Provider-Kind"); got != "cloud" {
		t.Fatalf("X-Runtime-Provider-Kind = %q, want cloud", got)
	}
	if got := response.Header().Get("X-Runtime-Fallback-From"); got != "ollama" {
		t.Fatalf("X-Runtime-Fallback-From = %q, want ollama", got)
	}
	if got := response.Header().Get("X-Runtime-Attempts"); got != "2" {
		t.Fatalf("X-Runtime-Attempts = %q, want 2", got)
	}
	if got := response.Header().Get("X-Runtime-Retries"); got != "0" {
		t.Fatalf("X-Runtime-Retries = %q, want 0", got)
	}
	if got := response.Header().Get("X-Runtime-Route-Reason"); got != "provider_default_model_failover" {
		t.Fatalf("X-Runtime-Route-Reason = %q, want failover reason", got)
	}
	if localProvider.CallCount() != 1 {
		t.Fatalf("local provider call count = %d, want 1", localProvider.CallCount())
	}
	if cloudProvider.CallCount() != 1 {
		t.Fatalf("cloud provider call count = %d, want 1", cloudProvider.CallCount())
	}
}

func TestChatCompletionsSkipsDegradedProviderAfterTransientFailures(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
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
			&providers.UpstreamError{StatusCode: http.StatusBadGateway, Message: "local runtime unavailable", Type: "server_error"},
		},
		response: &types.ChatResponse{
			ID:        "chatcmpl-local",
			Model:     "llama3.1:8b",
			CreatedAt: time.Unix(1_700_000_000, 0).UTC(),
			Choices:   []types.ChatChoice{{Index: 0, Message: types.Message{Role: "assistant", Content: "local"}, FinishReason: "stop"}},
			Usage:     types.Usage{PromptTokens: 10, CompletionTokens: 4, TotalTokens: 14},
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
			ID:        "chatcmpl-cloud",
			Model:     "gpt-4o-mini",
			CreatedAt: time.Unix(1_700_000_000, 0).UTC(),
			Choices:   []types.ChatChoice{{Index: 0, Message: types.Message{Role: "assistant", Content: "cloud fallback"}, FinishReason: "stop"}},
			Usage:     types.Usage{PromptTokens: 12, CompletionTokens: 5, TotalTokens: 17},
		},
	}

	handler := newTestHTTPHandlerForProviders(logger, []providers.Provider{localProvider, cloudProvider}, config.Config{
		Provider: config.ProviderConfig{
			MaxAttempts:     1,
			RetryBackoff:    time.Millisecond,
			FailoverEnabled: true,
			HealthThreshold: 1,
			HealthCooldown:  time.Hour,
		},
		Router: config.RouterConfig{
			DefaultModel: "gpt-4o-mini",
		},
	})

	first := performJSONRequest(t, handler, `{"messages":[{"role":"user","content":"hello"}]}`)
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want %d, body=%s", first.Code, http.StatusOK, first.Body.String())
	}
	if got := first.Header().Get("X-Runtime-Fallback-From"); got != "ollama" {
		t.Fatalf("first X-Runtime-Fallback-From = %q, want ollama", got)
	}

	second := performJSONRequest(t, handler, `{"messages":[{"role":"user","content":"hello again"}]}`)
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d, want %d, body=%s", second.Code, http.StatusOK, second.Body.String())
	}
	if got := second.Header().Get("X-Runtime-Provider"); got != "openai" {
		t.Fatalf("second X-Runtime-Provider = %q, want openai", got)
	}
	if got := second.Header().Get("X-Runtime-Route-Reason"); got != "provider_default_model" {
		t.Fatalf("second X-Runtime-Route-Reason = %q, want provider_default_model", got)
	}
	if localProvider.CallCount() != 1 {
		t.Fatalf("local provider call count = %d, want 1 because degraded provider should be skipped", localProvider.CallCount())
	}
	if cloudProvider.CallCount() != 2 {
		t.Fatalf("cloud provider call count = %d, want 2", cloudProvider.CallCount())
	}
}

func TestNormalizeChatRequestCarriesProviderHint(t *testing.T) {
	t.Parallel()

	req := OpenAIChatCompletionRequest{
		Model:    "llama3.1:8b",
		Provider: "ollama",
		User:     "team-a",
		Messages: []OpenAIChatMessage{
			{Role: "user", Content: OpenAIMessageContent{Text: "hello"}},
		},
	}

	got, err := normalizeChatRequest(req, "req-123", auth.Principal{})
	if err != nil {
		t.Fatalf("normalizeChatRequest() error = %v", err)
	}
	if got.Scope.ProviderHint != "ollama" {
		t.Fatalf("provider hint = %q, want ollama", got.Scope.ProviderHint)
	}
	if got.Scope.User != "team-a" {
		t.Fatalf("scope user = %q, want team-a", got.Scope.User)
	}
}

func TestNormalizeChatRequestBindsTenantFromPrincipal(t *testing.T) {
	t.Parallel()

	got, err := normalizeChatRequest(OpenAIChatCompletionRequest{
		Model: "gpt-4o-mini",
		User:  "team-a",
		Messages: []OpenAIChatMessage{
			{Role: "user", Content: OpenAIMessageContent{Text: "hello"}},
		},
	}, "req-123", auth.Principal{
		Role:   "tenant",
		Tenant: "team-a",
	})
	if err != nil {
		t.Fatalf("normalizeChatRequest() error = %v", err)
	}
	if got.Scope.Tenant != "team-a" {
		t.Fatalf("scope tenant = %q, want team-a", got.Scope.Tenant)
	}
}

func TestNormalizeChatRequestRejectsTenantImpersonation(t *testing.T) {
	t.Parallel()

	_, err := normalizeChatRequest(OpenAIChatCompletionRequest{
		Model: "gpt-4o-mini",
		User:  "team-b",
		Messages: []OpenAIChatMessage{
			{Role: "user", Content: OpenAIMessageContent{Text: "hello"}},
		},
	}, "req-123", auth.Principal{
		Role:   "tenant",
		Tenant: "team-a",
	})
	if err == nil {
		t.Fatal("normalizeChatRequest() error = nil, want tenant mismatch error")
	}
}

func TestModelsReturnsAggregatedProviderCapabilities(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cloudProvider := &fakeProvider{name: "openai"}
	localProvider := &fakeProvider{
		name: "ollama",
		capabilities: providers.Capabilities{
			Name:            "ollama",
			Kind:            providers.KindLocal,
			DefaultModel:    "llama3.1:8b",
			Models:          []string{"llama3.1:8b", "qwen2.5:7b"},
			DiscoverySource: "upstream_v1_models",
		},
	}

	registry := providers.NewRegistry(cloudProvider, localProvider)
	providerCatalog := catalog.NewRegistryCatalog(registry, nil)
	budgetStore := governor.NewMemoryBudgetStore()

	service := gateway.NewService(gateway.Dependencies{
		Logger:    logger,
		Cache:     cache.NewMemoryStore(time.Minute),
		Router:    router.NewRuleRouter("gpt-4o-mini", providerCatalog),
		Catalog:   providerCatalog,
		Governor:  governor.NewStaticGovernor(config.GovernorConfig{MaxPromptTokens: 64_000}, budgetStore, budgetStore),
		Providers: registry,
		Pricebook: billing.NewStaticPricebook(config.ProvidersConfig{
			OpenAICompatible: []config.OpenAICompatibleProviderConfig{
				{Name: "openai", Kind: "cloud"},
				{Name: "ollama", Kind: "local"},
			},
		}, defaultPricebookForTests()),
		Tracer: profiler.NewInMemoryTracer(nil),
	})
	handler := NewServer(logger, NewHandler(config.Config{}, logger, service, nil, nil, nil))
	client := newAPITestClient(t, handler)
	response := mustRequestJSON[OpenAIModelsResponse](client, http.MethodGet, "/v1/models", "")
	if response.Object != "list" {
		t.Fatalf("object = %q, want list", response.Object)
	}
	if len(response.Data) < 3 {
		t.Fatalf("model count = %d, want at least 3", len(response.Data))
	}

	foundLocalDefault := false
	foundCloud := false
	for _, item := range response.Data {
		if item.ID == "llama3.1:8b" && item.Metadata["provider_kind"] == "local" && item.Metadata["default"] == true {
			foundLocalDefault = true
		}
		if item.ID == "gpt-4o-mini" && item.Metadata["provider"] == "openai" {
			foundCloud = true
		}
	}
	if !foundLocalDefault {
		t.Fatalf("missing local default model in response: %#v", response.Data)
	}
	if !foundCloud {
		t.Fatalf("missing cloud model in response: %#v", response.Data)
	}
}

func TestProviderStatusReturnsHealthAndDiscoveryFreshness(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	healthyProvider := &fakeProvider{
		name: "openai",
		capabilities: providers.Capabilities{
			Name:            "openai",
			Kind:            providers.KindCloud,
			DefaultModel:    "gpt-4o-mini",
			Models:          []string{"gpt-4o-mini"},
			DiscoverySource: "upstream_v1_models",
			RefreshedAt:     time.Unix(1_700_000_000, 0).UTC(),
		},
	}
	degradedProvider := &fakeProvider{
		name:         "ollama",
		capsErr:      io.EOF,
		defaultModel: "llama3.1:8b",
		capabilities: providers.Capabilities{
			Name:            "ollama",
			Kind:            providers.KindLocal,
			DefaultModel:    "llama3.1:8b",
			Models:          []string{"llama3.1:8b"},
			DiscoverySource: "config_fallback",
		},
	}
	missingCredentialProvider := &fakeProvider{
		name:       "anthropic",
		credential: providers.CredentialStateMissing,
		capabilities: providers.Capabilities{
			Name:            "anthropic",
			Kind:            providers.KindCloud,
			DefaultModel:    "claude-sonnet-4-5",
			Models:          []string{"claude-sonnet-4-5"},
			DiscoverySource: "config_unconfigured",
		},
	}

	registry := providers.NewRegistry(healthyProvider, degradedProvider, missingCredentialProvider)
	providerCatalog := catalog.NewRegistryCatalog(registry, nil)
	budgetStore := governor.NewMemoryBudgetStore()
	service := gateway.NewService(gateway.Dependencies{
		Logger:    logger,
		Cache:     cache.NewMemoryStore(time.Minute),
		Router:    router.NewRuleRouter("gpt-4o-mini", providerCatalog),
		Catalog:   providerCatalog,
		Governor:  governor.NewStaticGovernor(config.GovernorConfig{MaxPromptTokens: 64_000}, budgetStore, budgetStore),
		Providers: registry,
		Pricebook: billing.NewStaticPricebook(config.ProvidersConfig{
			OpenAICompatible: []config.OpenAICompatibleProviderConfig{
				{Name: "openai", Kind: "cloud"},
				{Name: "ollama", Kind: "local"},
			},
		}, defaultPricebookForTests()),
		Tracer: profiler.NewInMemoryTracer(nil),
	})
	handler := NewServer(logger, NewHandler(config.Config{}, logger, service, nil, nil, nil))
	client := newAPITestClient(t, handler)
	response := mustRequestJSON[ProviderStatusResponse](client, http.MethodGet, "/admin/providers", "")
	if response.Object != "provider_status" {
		t.Fatalf("object = %q, want provider_status", response.Object)
	}
	if len(response.Data) != 3 {
		t.Fatalf("provider count = %d, want 3", len(response.Data))
	}

	foundHealthy := false
	foundDegraded := false
	foundCredentialBlocked := false
	for _, item := range response.Data {
		if item.Name == "openai" && item.Healthy && item.RefreshedAt != "" {
			if item.BaseURL == "" {
				t.Fatal("openai base_url is empty")
			}
			if item.CredentialState != "configured" {
				t.Fatalf("openai credential_state = %q, want configured", item.CredentialState)
			}
			if item.ModelCount != 1 {
				t.Fatalf("openai model_count = %d, want 1", item.ModelCount)
			}
			if item.LastCheckedAt == "" {
				t.Fatal("openai last_checked_at is empty")
			}
			if !item.CredentialReady {
				t.Fatal("openai credential_ready = false, want true")
			}
			if !item.RoutingReady {
				t.Fatalf("openai routing_ready = false, reason = %q", item.RoutingBlocked)
			}
			foundHealthy = true
		}
		if item.Name == "ollama" && !item.Healthy && item.Status == "degraded" && item.LastError != "" {
			if item.CredentialState != "not_required" {
				t.Fatalf("ollama credential_state = %q, want not_required", item.CredentialState)
			}
			if item.RoutingReady {
				t.Fatal("ollama routing_ready = true, want false for degraded capability failure")
			}
			if item.RoutingBlocked != "provider_unhealthy" {
				t.Fatalf("ollama routing_blocked_reason = %q, want provider_unhealthy", item.RoutingBlocked)
			}
			foundDegraded = true
		}
		if item.Name == "anthropic" {
			if item.CredentialReady {
				t.Fatal("anthropic credential_ready = true, want false")
			}
			if item.RoutingReady {
				t.Fatal("anthropic routing_ready = true, want false for missing credentials")
			}
			if item.RoutingBlocked != "credential_missing" {
				t.Fatalf("anthropic routing_blocked_reason = %q, want credential_missing", item.RoutingBlocked)
			}
			foundCredentialBlocked = true
		}
	}
	if !foundHealthy {
		t.Fatalf("missing healthy provider entry: %#v", response.Data)
	}
	if !foundDegraded {
		t.Fatalf("missing degraded provider entry: %#v", response.Data)
	}
	if !foundCredentialBlocked {
		t.Fatalf("missing credential-blocked provider entry: %#v", response.Data)
	}
}

func TestProviderStatusReturnsRateLimitedRoutingBlockReason(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	rateLimitedProvider := &fakeProvider{
		name: "openai",
		capabilities: providers.Capabilities{
			Name:            "openai",
			Kind:            providers.KindCloud,
			DefaultModel:    "gpt-4o-mini",
			Models:          []string{"gpt-4o-mini"},
			DiscoverySource: "upstream_v1_models",
			RefreshedAt:     time.Unix(1_700_000_000, 0).UTC(),
		},
	}
	registry := providers.NewRegistry(rateLimitedProvider)
	health := providers.NewMemoryHealthTracker(3, time.Minute)
	health.RecordFailure("openai", &providers.UpstreamError{StatusCode: http.StatusTooManyRequests, Type: "rate_limit"})
	providerCatalog := catalog.NewRegistryCatalog(registry, health)
	budgetStore := governor.NewMemoryBudgetStore()
	service := gateway.NewService(gateway.Dependencies{
		Logger:    logger,
		Cache:     cache.NewMemoryStore(time.Minute),
		Router:    router.NewRuleRouter("gpt-4o-mini", providerCatalog),
		Catalog:   providerCatalog,
		Governor:  governor.NewStaticGovernor(config.GovernorConfig{MaxPromptTokens: 64_000}, budgetStore, budgetStore),
		Providers: registry,
		Pricebook: billing.NewStaticPricebook(config.ProvidersConfig{
			OpenAICompatible: []config.OpenAICompatibleProviderConfig{{Name: "openai", Kind: "cloud"}},
		}, defaultPricebookForTests()),
		Tracer: profiler.NewInMemoryTracer(nil),
	})
	handler := NewServer(logger, NewHandler(config.Config{}, logger, service, nil, nil, nil))
	client := newAPITestClient(t, handler)
	response := mustRequestJSON[ProviderStatusResponse](client, http.MethodGet, "/admin/providers", "")
	if len(response.Data) != 1 {
		t.Fatalf("provider count = %d, want 1", len(response.Data))
	}
	item := response.Data[0]
	if item.Status != "open" {
		t.Fatalf("status = %q, want open", item.Status)
	}
	if item.RoutingReady {
		t.Fatal("routing_ready = true, want false for rate-limited provider")
	}
	if item.RoutingBlocked != "provider_rate_limited" {
		t.Fatalf("routing_blocked_reason = %q, want provider_rate_limited", item.RoutingBlocked)
	}
	if item.LastErrorClass != "rate_limit" {
		t.Fatalf("last_error_class = %q, want rate_limit", item.LastErrorClass)
	}
	if item.OpenUntil == "" {
		t.Fatal("open_until is empty, want cooldown deadline")
	}
	if item.RateLimits != 1 {
		t.Fatalf("rate_limits = %d, want 1", item.RateLimits)
	}
	if item.ConsecutiveFailures != 1 {
		t.Fatalf("consecutive_failures = %d, want 1", item.ConsecutiveFailures)
	}
}

func TestProviderHealthHistoryReturnsRecentEvents(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{
		name: "openai",
		capabilities: providers.Capabilities{
			Name:            "openai",
			Kind:            providers.KindCloud,
			DefaultModel:    "gpt-4o-mini",
			Models:          []string{"gpt-4o-mini"},
			DiscoverySource: "upstream_v1_models",
			RefreshedAt:     time.Unix(1_700_000_000, 0).UTC(),
		},
	}
	registry := providers.NewRegistry(provider)
	historyStore := providers.NewMemoryHealthHistoryStore()
	health := providers.NewMemoryHealthTrackerWithHistory(3, time.Minute, 0, historyStore)
	health.RecordFailure("openai", &providers.UpstreamError{StatusCode: http.StatusTooManyRequests, Type: "rate_limit", Message: "slow down"})
	providerCatalog := catalog.NewRegistryCatalog(registry, health)
	budgetStore := governor.NewMemoryBudgetStore()
	service := gateway.NewService(gateway.Dependencies{
		Logger:          logger,
		Cache:           cache.NewMemoryStore(time.Minute),
		Router:          router.NewRuleRouter("gpt-4o-mini", providerCatalog),
		Catalog:         providerCatalog,
		Governor:        governor.NewStaticGovernor(config.GovernorConfig{MaxPromptTokens: 64_000}, budgetStore, budgetStore),
		Providers:       registry,
		HealthTracker:   health,
		ProviderHistory: historyStore,
		Pricebook: billing.NewStaticPricebook(config.ProvidersConfig{
			OpenAICompatible: []config.OpenAICompatibleProviderConfig{{Name: "openai", Kind: "cloud"}},
		}, defaultPricebookForTests()),
		Tracer: profiler.NewInMemoryTracer(nil),
	})
	handler := NewServer(logger, NewHandler(config.Config{Provider: config.ProviderConfig{HistoryLimit: 10}}, logger, service, nil, nil, nil))
	client := newAPITestClient(t, handler)

	response := mustRequestJSON[ProviderHealthHistoryResponse](client, http.MethodGet, "/admin/providers/history?provider=openai&limit=1", "")
	if response.Object != "provider_health_history" {
		t.Fatalf("object = %q, want provider_health_history", response.Object)
	}
	if len(response.Data) != 1 {
		t.Fatalf("history count = %d, want 1", len(response.Data))
	}
	item := response.Data[0]
	if item.Provider != "openai" {
		t.Fatalf("provider = %q, want openai", item.Provider)
	}
	if item.ProviderKind != "cloud" {
		t.Fatalf("provider_kind = %q, want cloud", item.ProviderKind)
	}
	if item.Event != "cooldown_opened" {
		t.Fatalf("event = %q, want cooldown_opened", item.Event)
	}
	if item.Status != "open" {
		t.Fatalf("status = %q, want open", item.Status)
	}
	if item.ErrorClass != "rate_limit" {
		t.Fatalf("error_class = %q, want rate_limit", item.ErrorClass)
	}
	if item.RateLimits != 1 {
		t.Fatalf("rate_limits = %d, want 1", item.RateLimits)
	}
	if item.OpenUntil == "" {
		t.Fatal("open_until is empty, want cooldown deadline")
	}
	if item.Timestamp == "" {
		t.Fatal("timestamp is empty, want event time")
	}
}

func TestProviderHealthHistoryIncludesFailoverEvents(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	primary := &fakeProvider{
		name:         "anthropic",
		defaultModel: "claude-sonnet-4-20250514",
		capabilities: providers.Capabilities{
			Name:            "anthropic",
			Kind:            providers.KindCloud,
			DefaultModel:    "claude-sonnet-4-20250514",
			Models:          []string{"claude-sonnet-4-20250514"},
			DiscoverySource: "upstream_v1_models",
			RefreshedAt:     time.Unix(1_700_000_000, 0).UTC(),
		},
		errSequence: []error{
			&providers.UpstreamError{StatusCode: http.StatusServiceUnavailable, Type: "upstream_unavailable", Message: "primary unavailable"},
		},
	}
	fallback := &fakeProvider{
		name:         "openai",
		defaultModel: "gpt-4o-mini",
		capabilities: providers.Capabilities{
			Name:            "openai",
			Kind:            providers.KindCloud,
			DefaultModel:    "gpt-4o-mini",
			Models:          []string{"gpt-4o-mini"},
			DiscoverySource: "upstream_v1_models",
			RefreshedAt:     time.Unix(1_700_000_000, 0).UTC(),
		},
		response: &types.ChatResponse{
			ID:        "chatcmpl-fallback",
			Model:     "gpt-4o-mini",
			CreatedAt: time.Unix(1_700_000_100, 0).UTC(),
			Choices: []types.ChatChoice{{
				Index:        0,
				Message:      types.Message{Role: "assistant", Content: "fallback ok"},
				FinishReason: "stop",
			}},
			Usage: types.Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12},
		},
	}
	handler := newTestHTTPHandlerForProviders(logger, []providers.Provider{primary, fallback}, config.Config{
		Provider: config.ProviderConfig{
			MaxAttempts:     1,
			RetryBackoff:    time.Millisecond,
			FailoverEnabled: true,
			HistoryLimit:    20,
		},
	})
	client := newAPITestClient(t, handler)

	chat := decodeRecorder[OpenAIChatCompletionResponse](t, client.mustRequest(http.MethodPost, "/v1/chat/completions", `{
		"messages": [
			{"role":"user","content":"hello"}
		]
	}`))
	if chat.Model != "gpt-4o-mini" {
		t.Fatalf("response model = %q, want gpt-4o-mini from fallback provider", chat.Model)
	}

	response := mustRequestJSON[ProviderHealthHistoryResponse](client, http.MethodGet, "/admin/providers/history?limit=20", "")
	if len(response.Data) < 4 {
		t.Fatalf("history count = %d, want at least 4 rows for failure, failover, and success", len(response.Data))
	}

	var (
		sawFailoverTriggered bool
		sawFailoverSelected  bool
	)
	for _, item := range response.Data {
		switch item.Event {
		case "failover_triggered":
			if item.Provider != "anthropic" {
				t.Fatalf("failover_triggered provider = %q, want anthropic", item.Provider)
			}
			if item.PeerProvider != "openai" {
				t.Fatalf("failover_triggered peer_provider = %q, want openai", item.PeerProvider)
			}
			if item.Reason != "provider_retry_exhausted" {
				t.Fatalf("failover_triggered reason = %q, want provider_retry_exhausted", item.Reason)
			}
			if item.RouteReason != "provider_default_model" {
				t.Fatalf("failover_triggered route_reason = %q, want provider_default_model", item.RouteReason)
			}
			if item.PeerRouteReason != "provider_default_model_failover" {
				t.Fatalf("failover_triggered peer_route_reason = %q, want provider_default_model_failover", item.PeerRouteReason)
			}
			if item.ErrorClass != "server_error" {
				t.Fatalf("failover_triggered error_class = %q, want server_error", item.ErrorClass)
			}
			if item.HealthStatus == "" {
				t.Fatal("failover_triggered health_status is empty")
			}
			if item.PeerHealthStatus == "" {
				t.Fatal("failover_triggered peer_health_status is empty")
			}
			if item.AttemptCount != 1 {
				t.Fatalf("failover_triggered attempt_count = %d, want 1", item.AttemptCount)
			}
			if item.RequestID == "" {
				t.Fatal("failover_triggered request_id is empty")
			}
			if item.TraceID == "" {
				t.Fatal("failover_triggered trace_id is empty")
			}
			sawFailoverTriggered = true
		case "failover_selected":
			if item.Provider != "openai" {
				t.Fatalf("failover_selected provider = %q, want openai", item.Provider)
			}
			if item.PeerProvider != "anthropic" {
				t.Fatalf("failover_selected peer_provider = %q, want anthropic", item.PeerProvider)
			}
			if item.Reason != "candidate_selected" {
				t.Fatalf("failover_selected reason = %q, want candidate_selected", item.Reason)
			}
			if item.RouteReason != "provider_default_model_failover" {
				t.Fatalf("failover_selected route_reason = %q, want provider_default_model_failover", item.RouteReason)
			}
			if item.PeerRouteReason != "provider_default_model" {
				t.Fatalf("failover_selected peer_route_reason = %q, want provider_default_model", item.PeerRouteReason)
			}
			if item.EstimatedMicrosUSD < 0 {
				t.Fatalf("failover_selected estimated_micros_usd = %d, want non-negative", item.EstimatedMicrosUSD)
			}
			if item.RequestID == "" {
				t.Fatal("failover_selected request_id is empty")
			}
			if item.TraceID == "" {
				t.Fatal("failover_selected trace_id is empty")
			}
			sawFailoverSelected = true
		}
	}
	if !sawFailoverTriggered {
		t.Fatal("provider history missing failover_triggered event")
	}
	if !sawFailoverSelected {
		t.Fatal("provider history missing failover_selected event")
	}
}

func TestProviderHealthHistoryIncludesPreflightFailoverEvents(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	primary := &fakeProvider{
		defaultModel: "claude-unpriced",
		name:         "anthropic",
		capabilities: providers.Capabilities{
			Name:            "anthropic",
			Kind:            providers.KindCloud,
			DefaultModel:    "claude-unpriced",
			Models:          []string{"claude-unpriced"},
			DiscoverySource: "upstream_v1_models",
			RefreshedAt:     time.Unix(1_700_000_000, 0).UTC(),
		},
	}
	fallback := &fakeProvider{
		name:         "openai",
		defaultModel: "gpt-4o-mini",
		capabilities: providers.Capabilities{
			Name:            "openai",
			Kind:            providers.KindCloud,
			DefaultModel:    "gpt-4o-mini",
			Models:          []string{"gpt-4o-mini"},
			DiscoverySource: "upstream_v1_models",
			RefreshedAt:     time.Unix(1_700_000_000, 0).UTC(),
		},
		response: &types.ChatResponse{
			ID:        "chatcmpl-preflight-fallback",
			Model:     "gpt-4o-mini",
			CreatedAt: time.Unix(1_700_000_100, 0).UTC(),
			Choices: []types.ChatChoice{{
				Index:        0,
				Message:      types.Message{Role: "assistant", Content: "fallback ok"},
				FinishReason: "stop",
			}},
			Usage: types.Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12},
		},
	}
	handler := newTestHTTPHandlerForProviders(logger, []providers.Provider{primary, fallback}, config.Config{
		Router: config.RouterConfig{
			DefaultModel: "claude-unpriced",
		},
		Provider: config.ProviderConfig{
			MaxAttempts:     1,
			RetryBackoff:    time.Millisecond,
			FailoverEnabled: true,
			HistoryLimit:    20,
		},
		Pricebook: config.PricebookConfig{
			UnknownModelPolicy: "error",
			Entries: []config.ModelPriceConfig{
				{
					Provider:                        "openai",
					Model:                           "gpt-4o-mini",
					InputMicrosUSDPerMillionTokens:  150_000,
					OutputMicrosUSDPerMillionTokens: 600_000,
				},
			},
		},
	})
	client := newAPITestClient(t, handler)

	chat := decodeRecorder[OpenAIChatCompletionResponse](t, client.mustRequest(http.MethodPost, "/v1/chat/completions", `{
		"messages": [
			{"role":"user","content":"hello"}
		]
	}`))
	if chat.Model != "gpt-4o-mini" {
		t.Fatalf("response model = %q, want gpt-4o-mini fallback", chat.Model)
	}

	response := mustRequestJSON[ProviderHealthHistoryResponse](client, http.MethodGet, "/admin/providers/history?limit=20", "")
	found := false
	for _, item := range response.Data {
		if item.Event != "failover_triggered" || item.Provider != "anthropic" {
			continue
		}
		if item.Reason != "preflight_price_missing" {
			continue
		}
		found = true
		if item.RouteReason != "provider_default_model" {
			t.Fatalf("route_reason = %q, want provider_default_model", item.RouteReason)
		}
		if item.PeerProvider != "openai" {
			t.Fatalf("peer_provider = %q, want openai", item.PeerProvider)
		}
		if item.PeerRouteReason != "provider_default_model_failover" {
			t.Fatalf("peer_route_reason = %q, want provider_default_model_failover", item.PeerRouteReason)
		}
		if item.EstimatedMicrosUSD != 0 {
			t.Fatalf("estimated_micros_usd = %d, want 0 for price-missing preflight", item.EstimatedMicrosUSD)
		}
	}
	if !found {
		t.Fatalf("missing preflight_price_missing failover history row: %+v", response.Data)
	}
}

func TestProviderPresetsReturnsCatalog(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := NewServer(logger, NewHandler(config.Config{}, logger, nil, nil, nil, nil))
	client := newAPITestClient(t, handler)
	response := mustRequestJSON[ProviderPresetResponse](client, http.MethodGet, "/v1/provider-presets", "")
	if response.Object != "provider_presets" {
		t.Fatalf("object = %q, want provider_presets", response.Object)
	}
	if len(response.Data) < 4 {
		t.Fatalf("preset count = %d, want at least 4", len(response.Data))
	}
	if len(response.Data) != len(config.BuiltInProviders()) {
		t.Fatalf("preset count = %d, want %d built-in presets", len(response.Data), len(config.BuiltInProviders()))
	}

	foundAnthropic := false
	foundXAI := false
	foundOllama := false
	for _, item := range response.Data {
		if item.ID == "anthropic" && item.Protocol == "anthropic" && item.EnvSnippet != "" {
			foundAnthropic = true
		}
		if item.ID == "xai" && item.Protocol == "openai" && strings.Contains(item.EnvSnippet, "PROVIDER_XAI_API_KEY") {
			foundXAI = true
		}
		if item.ID == "ollama" && item.Kind == "local" && item.EnvSnippet != "" {
			foundOllama = true
		}
	}
	if !foundAnthropic {
		t.Fatalf("missing anthropic preset: %#v", response.Data)
	}
	if !foundXAI {
		t.Fatalf("missing xai preset: %#v", response.Data)
	}
	if !foundOllama {
		t.Fatalf("missing ollama preset: %#v", response.Data)
	}
}

func TestRuntimeStatsReturnsQueueAndRunCounters(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := config.Config{Server: config.ServerConfig{TaskApprovalPolicies: []string{"shell_exec"}}}
	handler := newTestHTTPHandlerForProviders(logger, nil, cfg)
	client := newAPITestClient(t, handler)
	tasks := newTaskTestClient(t, handler)

	createdStub := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/v1/tasks", `{"title":"Stats stub","prompt":"Complete one stub task."}`)
	startedStub := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/v1/tasks/"+createdStub.Data.ID+"/start", "")
	waitForRunStatus(t, handler, createdStub.Data.ID, startedStub.Data.ID, "completed")

	createdShell := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/v1/tasks", `{"title":"Stats shell","prompt":"Await approval.","execution_kind":"shell","shell_command":"printf 'ok\n'","working_directory":"."}`)
	startedShell := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/v1/tasks/"+createdShell.Data.ID+"/start", "")
	if startedShell.Data.Status != "awaiting_approval" {
		t.Fatalf("shell run status = %q, want awaiting_approval", startedShell.Data.Status)
	}

	response := mustRequestJSON[RuntimeStatsResponse](client, http.MethodGet, "/admin/runtime/stats", "")
	assertRuntimeStatsCore(t, response)
	if response.Data.AwaitingApprovalRuns < 1 {
		t.Fatalf("awaiting_approval_runs = %d, want >= 1", response.Data.AwaitingApprovalRuns)
	}
	if response.Data.StoreBackend != "memory" {
		t.Fatalf("store_backend = %q, want memory", response.Data.StoreBackend)
	}
}

func TestRuntimeStatsPayloadShape(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := newTestHTTPHandlerForProviders(logger, nil, config.Config{})
	client := newAPITestClient(t, handler)

	recorder := client.mustRequest(http.MethodGet, "/admin/runtime/stats", "")

	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if object, _ := payload["object"].(string); object != "runtime_stats" {
		t.Fatalf("object = %v, want runtime_stats", payload["object"])
	}

	data, ok := payload["data"].(map[string]any)
	if !ok {
		t.Fatalf("data type = %T, want object", payload["data"])
	}

	requiredKeys := []string{
		"checked_at",
		"queue_depth",
		"queue_capacity",
		"worker_count",
		"in_flight_jobs",
		"queued_runs",
		"running_runs",
		"awaiting_approval_runs",
		"oldest_queued_age_seconds",
		"oldest_running_age_seconds",
	}
	for _, key := range requiredKeys {
		if _, exists := data[key]; !exists {
			t.Fatalf("runtime_stats.data missing required key %q", key)
		}
	}
	if _, ok := data["checked_at"].(string); !ok {
		t.Fatalf("checked_at type = %T, want string", data["checked_at"])
	}
	numericKeys := []string{
		"queue_depth",
		"queue_capacity",
		"worker_count",
		"in_flight_jobs",
		"queued_runs",
		"running_runs",
		"awaiting_approval_runs",
		"oldest_queued_age_seconds",
		"oldest_running_age_seconds",
	}
	for _, key := range numericKeys {
		if _, ok := data[key].(float64); !ok {
			t.Fatalf("%s type = %T, want number", key, data[key])
		}
	}

	// Optional extension fields from telemetry phases should be objects when present.
	if telemetryShape, exists := data["telemetry"]; exists {
		telemetryMap, ok := telemetryShape.(map[string]any)
		if !ok {
			t.Fatalf("telemetry type = %T, want object", telemetryShape)
		}
		if signals, hasSignals := telemetryMap["signals"]; hasSignals {
			if _, ok := signals.(map[string]any); !ok {
				t.Fatalf("telemetry.signals type = %T, want object", signals)
			}
		}
	}
	if sloShape, exists := data["slo"]; exists {
		if _, ok := sloShape.(map[string]any); !ok {
			t.Fatalf("slo type = %T, want object", sloShape)
		}
	}
}

func assertRuntimeStatsCore(t *testing.T, response RuntimeStatsResponse) {
	t.Helper()
	if response.Object != "runtime_stats" {
		t.Fatalf("object = %q, want runtime_stats", response.Object)
	}
	if response.Data.CheckedAt == "" {
		t.Fatal("checked_at = empty, want timestamp")
	}
	if response.Data.QueueCapacity <= 0 {
		t.Fatalf("queue_capacity = %d, want > 0", response.Data.QueueCapacity)
	}
	if response.Data.WorkerCount <= 0 {
		t.Fatalf("worker_count = %d, want > 0", response.Data.WorkerCount)
	}
}

func TestBudgetEndpointsRequireAdminWhenTenantKeysConfigured(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cpStore := controlplane.NewMemoryStore()
	if _, err := cpStore.UpsertTenant(context.Background(), controlplane.Tenant{ID: "team-a", Name: "Team A", Enabled: true}); err != nil {
		t.Fatalf("UpsertTenant() error = %v", err)
	}
	if _, err := cpStore.UpsertAPIKey(context.Background(), controlplane.APIKey{
		ID:      "team-a",
		Name:    "team-a",
		Key:     "tenant-secret",
		Tenant:  "team-a",
		Role:    "tenant",
		Enabled: true,
	}); err != nil {
		t.Fatalf("UpsertAPIKey() error = %v", err)
	}
	handler := newBudgetTestHandlerWithConfig(logger, config.Config{
		Server: config.ServerConfig{
			AuthToken: "admin-secret",
		},
		Governor: config.GovernorConfig{
			MaxPromptTokens:      64_000,
			MaxTotalBudgetMicros: 10_000_000,
			BudgetBackend:        "memory",
			BudgetKey:            "global",
			BudgetScope:          "global",
		},
	}, governor.NewMemoryBudgetStore(), cpStore)
	tenantClient := newAPITestClient(t, handler).withBearerToken("tenant-secret")
	tenantClient.mustRequestStatus(http.StatusUnauthorized, http.MethodGet, "/admin/budget", "")
}

func TestTaskRunPerTenantConcurrencyLimitQueuesSecondRun(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cpStore := controlplane.NewMemoryStore()
	if _, err := cpStore.UpsertTenant(context.Background(), controlplane.Tenant{ID: "team-a", Name: "Team A", Enabled: true}); err != nil {
		t.Fatalf("UpsertTenant() error = %v", err)
	}
	if _, err := cpStore.UpsertAPIKey(context.Background(), controlplane.APIKey{
		ID:      "team-a-key",
		Name:    "team-a-key",
		Key:     "tenant-a-secret",
		Tenant:  "team-a",
		Role:    "tenant",
		Enabled: true,
	}); err != nil {
		t.Fatalf("UpsertAPIKey() error = %v", err)
	}

	handler := newTestHTTPHandlerWithControlPlane(logger, nil, config.Config{
		Server: config.ServerConfig{
			AuthToken:                  "admin-secret",
			TaskMaxConcurrentPerTenant: 1,
			TaskApprovalPolicies:       []string{"shell_exec"},
		},
	}, cpStore)
	tasks := newTaskTestClient(t, handler).withBearerToken("tenant-a-secret")

	firstTask := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/v1/tasks", `{"title":"first","prompt":"first","execution_kind":"shell","shell_command":"sleep 5","working_directory":".","timeout_ms":10000}`)
	firstRun := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/v1/tasks/"+firstTask.Data.ID+"/start", "")
	firstApprovals := mustTaskRequestJSON[TaskApprovalsResponse](tasks, http.MethodGet, "/v1/tasks/"+firstTask.Data.ID+"/approvals", "")
	tasks.mustRequest(http.MethodPost, "/v1/tasks/"+firstTask.Data.ID+"/approvals/"+firstApprovals.Data[0].ID+"/resolve", `{"decision":"approve"}`)
	firstRunState := waitForRunStatusWithClient(tasks, firstTask.Data.ID, firstRun.Data.ID, "running", "failed")
	if firstRunState.Data.Status == "failed" {
		t.Fatalf("first run failed unexpectedly before concurrency check: %s", firstRunState.Data.LastError)
	}

	secondTask := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/v1/tasks", `{"title":"second","prompt":"second","execution_kind":"shell","shell_command":"printf 'second\n'","working_directory":".","timeout_ms":5000}`)
	secondRun := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/v1/tasks/"+secondTask.Data.ID+"/start", "")
	secondApprovals := mustTaskRequestJSON[TaskApprovalsResponse](tasks, http.MethodGet, "/v1/tasks/"+secondTask.Data.ID+"/approvals", "")
	tasks.mustRequest(http.MethodPost, "/v1/tasks/"+secondTask.Data.ID+"/approvals/"+secondApprovals.Data[0].ID+"/resolve", `{"decision":"approve"}`)

	deadline := time.Now().Add(500 * time.Millisecond)
	observedQueued := false
	for time.Now().Before(deadline) {
		run := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodGet, "/v1/tasks/"+secondTask.Data.ID+"/runs/"+secondRun.Data.ID, "")
		if run.Data.Status == "queued" {
			observedQueued = true
			break
		}
		if run.Data.Status == "running" || run.Data.Status == "completed" {
			t.Fatalf("second run status = %q, want queued while first tenant run is active", run.Data.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !observedQueued {
		t.Fatal("did not observe second run in queued status under tenant concurrency limit")
	}

	tasks.mustRequest(http.MethodPost, "/v1/tasks/"+firstTask.Data.ID+"/runs/"+firstRun.Data.ID+"/cancel", "")
	waitForRunStatusWithClient(tasks, firstTask.Data.ID, firstRun.Data.ID, "cancelled")
	waitForRunStatusWithClient(tasks, secondTask.Data.ID, secondRun.Data.ID, "completed")
}

func TestChatCompletionAPIKeyRejectsTenantImpersonation(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{name: "openai"}
	cpStore := controlplane.NewMemoryStore()
	if _, err := cpStore.UpsertTenant(context.Background(), controlplane.Tenant{ID: "team-a", Name: "Team A", Enabled: true}); err != nil {
		t.Fatalf("UpsertTenant() error = %v", err)
	}
	if _, err := cpStore.UpsertAPIKey(context.Background(), controlplane.APIKey{
		ID:      "team-a",
		Name:    "team-a",
		Key:     "tenant-secret",
		Tenant:  "team-a",
		Role:    "tenant",
		Enabled: true,
	}); err != nil {
		t.Fatalf("UpsertAPIKey() error = %v", err)
	}
	handler := newTestHTTPHandlerWithControlPlane(logger, []providers.Provider{provider}, config.Config{}, cpStore)
	tenantClient := newAPITestClient(t, handler).withBearerToken("tenant-secret")
	tenantClient.mustRequestStatus(http.StatusForbidden, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-4o-mini","user":"team-b","messages":[{"role":"user","content":"hello"}]}`)
}

func TestModelsFilteredForTenantAPIKeyAllowlist(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cloudProvider := &fakeProvider{name: "openai"}
	localProvider := &fakeProvider{
		name: "ollama",
		capabilities: providers.Capabilities{
			Name:            "ollama",
			Kind:            providers.KindLocal,
			DefaultModel:    "llama3.1:8b",
			Models:          []string{"llama3.1:8b"},
			DiscoverySource: "upstream_v1_models",
		},
	}
	registry := providers.NewRegistry(cloudProvider, localProvider)
	providerCatalog := catalog.NewRegistryCatalog(registry, nil)
	budgetStore := governor.NewMemoryBudgetStore()
	cpStore := controlplane.NewMemoryStore()
	if _, err := cpStore.UpsertTenant(context.Background(), controlplane.Tenant{ID: "team-a", Name: "Team A", Enabled: true}); err != nil {
		t.Fatalf("UpsertTenant() error = %v", err)
	}
	if _, err := cpStore.UpsertAPIKey(context.Background(), controlplane.APIKey{
		ID:               "team-a",
		Name:             "team-a",
		Key:              "tenant-secret",
		Tenant:           "team-a",
		Role:             "tenant",
		AllowedProviders: []string{"ollama"},
		AllowedModels:    []string{"llama3.1:8b"},
		Enabled:          true,
	}); err != nil {
		t.Fatalf("UpsertAPIKey() error = %v", err)
	}
	service := gateway.NewService(gateway.Dependencies{
		Logger:    logger,
		Cache:     cache.NewMemoryStore(time.Minute),
		Router:    router.NewRuleRouter("gpt-4o-mini", providerCatalog),
		Catalog:   providerCatalog,
		Governor:  governor.NewStaticGovernor(config.GovernorConfig{MaxPromptTokens: 64_000}, budgetStore, budgetStore),
		Providers: registry,
		Pricebook: billing.NewStaticPricebook(config.ProvidersConfig{
			OpenAICompatible: []config.OpenAICompatibleProviderConfig{
				{Name: "openai", Kind: "cloud"},
				{Name: "ollama", Kind: "local"},
			},
		}, defaultPricebookForTests()),
		Tracer: profiler.NewInMemoryTracer(nil),
	})
	handler := NewServer(logger, NewHandler(config.Config{}, logger, service, cpStore, nil, nil))
	tenantClient := newAPITestClient(t, handler).withBearerToken("tenant-secret")
	response := mustRequestJSON[OpenAIModelsResponse](tenantClient, http.MethodGet, "/v1/models", "")
	if len(response.Data) != 1 {
		t.Fatalf("model count = %d, want 1", len(response.Data))
	}
	if response.Data[0].ID != "llama3.1:8b" {
		t.Fatalf("model id = %q, want llama3.1:8b", response.Data[0].ID)
	}
}

func TestSessionEndpointReturnsAnonymousTenantAndAdminStates(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	store := controlplane.NewMemoryStore()
	tenant, err := store.UpsertTenant(context.Background(), controlplane.Tenant{Name: "Team A"})
	if err != nil {
		t.Fatalf("UpsertTenant() error = %v", err)
	}
	if _, err := store.UpsertAPIKey(context.Background(), controlplane.APIKey{
		Name:   "Team A Dev",
		Key:    "tenant-secret",
		Tenant: tenant.ID,
		Role:   "tenant",
	}); err != nil {
		t.Fatalf("UpsertAPIKey() error = %v", err)
	}

	handler := newBudgetTestHandlerWithConfig(logger, config.Config{
		Server: config.ServerConfig{
			AuthToken: "admin-secret",
		},
	}, governor.NewMemoryBudgetStore(), store)

	cases := []struct {
		name        string
		token       string
		wantStatus  int
		wantRole    string
		wantTenant  string
		wantSource  string
		wantKeyID   string
		wantAuth    bool
		wantInvalid bool
	}{
		{name: "anonymous", wantStatus: http.StatusOK, wantRole: "anonymous", wantSource: "no_token", wantAuth: false},
		{name: "tenant", token: "tenant-secret", wantStatus: http.StatusOK, wantRole: "tenant", wantTenant: "team-a", wantSource: "control_plane_api_key", wantKeyID: "team-a-dev", wantAuth: true},
		{name: "admin", token: "admin-secret", wantStatus: http.StatusOK, wantRole: "admin", wantSource: "admin_token", wantAuth: true},
		{name: "invalid", token: "bad-secret", wantStatus: http.StatusOK, wantRole: "invalid", wantSource: "invalid_token", wantAuth: false, wantInvalid: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newAPITestClient(t, handler)
			if tc.token != "" {
				client = client.withBearerToken(tc.token)
			}
			response := mustRequestJSONStatus[SessionResponse](client, tc.wantStatus, http.MethodGet, "/v1/whoami", "")
			if response.Data.Role != tc.wantRole {
				t.Fatalf("role = %q, want %q", response.Data.Role, tc.wantRole)
			}
			if response.Data.Tenant != tc.wantTenant {
				t.Fatalf("tenant = %q, want %q", response.Data.Tenant, tc.wantTenant)
			}
			if response.Data.Source != tc.wantSource {
				t.Fatalf("source = %q, want %q", response.Data.Source, tc.wantSource)
			}
			if response.Data.KeyID != tc.wantKeyID {
				t.Fatalf("key_id = %q, want %q", response.Data.KeyID, tc.wantKeyID)
			}
			if response.Data.Authenticated != tc.wantAuth {
				t.Fatalf("authenticated = %t, want %t", response.Data.Authenticated, tc.wantAuth)
			}
			if response.Data.InvalidToken != tc.wantInvalid {
				t.Fatalf("invalid_token = %t, want %t", response.Data.InvalidToken, tc.wantInvalid)
			}
			// features mirrors server config; this fixture configures
			// neither flag, so both should be false on every role.
			if response.Data.Features.MultiTenant {
				t.Fatalf("features.multi_tenant = true, want false (not configured in fixture)")
			}
			if response.Data.Features.AuthDisabled {
				t.Fatalf("features.auth_disabled = true, want false (admin token is set)")
			}
		})
	}
}

// TestSessionFeaturesReflectServerConfig pins that /v1/whoami's features
// object surfaces GATEWAY_MULTI_TENANT and GATEWAY_AUTH_DISABLED so the
// UI can decide whether to render multi-tenant tabs and whether to skip
// the TokenGate.
func TestSessionFeaturesReflectServerConfig(t *testing.T) {
	t.Parallel()

	t.Run("multi_tenant=true surfaces in features", func(t *testing.T) {
		logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
		store := controlplane.NewMemoryStore()
		handler := newBudgetTestHandlerWithConfig(logger, config.Config{
			Server: config.ServerConfig{
				AuthToken:   "admin-secret",
				MultiTenant: true,
			},
		}, governor.NewMemoryBudgetStore(), store)

		client := newAPITestClient(t, handler).withBearerToken("admin-secret")
		response := mustRequestJSON[SessionResponse](client, http.MethodGet, "/v1/whoami", "")
		if !response.Data.Features.MultiTenant {
			t.Fatalf("features.multi_tenant = false, want true")
		}
		if response.Data.Features.AuthDisabled {
			t.Fatalf("features.auth_disabled = true, want false")
		}
	})

	t.Run("auth_disabled=true surfaces in features and grants anonymous access", func(t *testing.T) {
		logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
		store := controlplane.NewMemoryStore()
		handler := newBudgetTestHandlerWithConfig(logger, config.Config{
			Server: config.ServerConfig{
				AuthToken:    "admin-secret",
				AuthDisabled: true,
			},
		}, governor.NewMemoryBudgetStore(), store)

		// No token — auth-disabled means the gateway accepts anonymous.
		client := newAPITestClient(t, handler)
		response := mustRequestJSON[SessionResponse](client, http.MethodGet, "/v1/whoami", "")
		if !response.Data.Features.AuthDisabled {
			t.Fatalf("features.auth_disabled = false, want true")
		}
		if response.Data.Source != "auth_disabled" {
			t.Fatalf("source = %q, want auth_disabled", response.Data.Source)
		}
	})
}

func TestClientEndpointsAcceptXAPIKeyAuth(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := newTestHTTPHandlerWithConfig(logger, &fakeProvider{
		name: "openai",
		response: &types.ChatResponse{
			ID:    "chatcmpl-x-api-key",
			Model: "gpt-4o-mini",
			Choices: []types.ChatChoice{
				{Message: types.Message{Role: "assistant", Content: "ok"}, FinishReason: "stop"},
			},
			Usage: types.Usage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
		},
	}, config.Config{
		Server: config.ServerConfig{
			AuthToken: "admin-secret",
		},
	})
	client := newAPITestClient(t, handler).withAPIKey("admin-secret")

	models := mustRequestJSON[OpenAIModelsResponse](client, http.MethodGet, "/v1/models", "")
	if len(models.Data) == 0 {
		t.Fatal("models = 0, want at least one model")
	}
	chat := mustRequestJSON[OpenAIChatCompletionResponse](client, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hello"}]}`)
	if len(chat.Choices) == 0 {
		t.Fatal("chat choices = 0, want at least one")
	}
	msg := mustRequestJSON[AnthropicMessagesResponse](client, http.MethodPost, "/v1/messages", `{"model":"gpt-4o-mini","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`)
	if len(msg.Content) == 0 {
		t.Fatal("anthropic content = 0, want at least one block")
	}
}

func TestClientEndpointAuthPrecedencePrefersAuthorization(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := newTestHTTPHandlerWithConfig(logger, &fakeProvider{
		name: "openai",
		response: &types.ChatResponse{
			ID:    "chatcmpl-precedence",
			Model: "gpt-4o-mini",
			Choices: []types.ChatChoice{
				{Message: types.Message{Role: "assistant", Content: "ok"}, FinishReason: "stop"},
			},
			Usage: types.Usage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
		},
	}, config.Config{
		Server: config.ServerConfig{
			AuthToken: "admin-secret",
		},
	})
	client := newAPITestClient(t, handler).withAPIKey("admin-secret").withBearerToken("wrong-secret")
	client.mustRequestStatus(http.StatusUnauthorized, http.MethodGet, "/v1/models", "")
}

func TestControlPlaneAdminEndpointsPersistAndListState(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	store := controlplane.NewMemoryStore()

	handler := newBudgetTestHandlerWithConfig(logger, config.Config{
		Server: config.ServerConfig{
			AuthToken: "admin-secret",
		},
		Governor: config.GovernorConfig{
			MaxPromptTokens:      64_000,
			MaxTotalBudgetMicros: 10_000_000,
			BudgetBackend:        "memory",
			BudgetKey:            "global",
			BudgetScope:          "global",
		},
	}, governor.NewMemoryBudgetStore(), store)
	admin := newAPITestClient(t, handler).withBearerToken("admin-secret")
	admin.mustRequest(http.MethodPost, "/admin/control-plane/tenants", `{"name":"Team A","description":"Primary tenant","allowed_providers":["ollama"],"enabled":true}`)
	admin.mustRequest(http.MethodPost, "/admin/control-plane/api-keys", `{"name":"Team A Dev","key":"hecate-team-a-dev","tenant":"team-a","role":"tenant","allowed_models":["llama3.1:8b"],"enabled":true}`)
	response := mustRequestJSON[ControlPlaneResponse](admin, http.MethodGet, "/admin/control-plane", "")
	if response.Data.Backend != "memory" {
		t.Fatalf("backend = %q, want memory", response.Data.Backend)
	}
	if len(response.Data.Tenants) != 1 {
		t.Fatalf("tenant count = %d, want 1", len(response.Data.Tenants))
	}
	if len(response.Data.APIKeys) != 1 {
		t.Fatalf("api key count = %d, want 1", len(response.Data.APIKeys))
	}
	if response.Data.APIKeys[0].KeyPreview == "" {
		t.Fatal("expected redacted key preview")
	}
	if len(response.Data.Events) != 2 {
		t.Fatalf("event count = %d, want 2", len(response.Data.Events))
	}
}

func TestControlPlanePolicyAndPricebookCRUD(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	store := controlplane.NewMemoryStore()

	handler := newBudgetTestHandlerWithConfig(logger, config.Config{
		Server: config.ServerConfig{
			AuthToken: "admin-secret",
		},
	}, governor.NewMemoryBudgetStore(), store)
	admin := newAPITestClient(t, handler).withBearerToken("admin-secret")
	admin.mustRequest(http.MethodPost, "/admin/control-plane/policy-rules", `{"id":"deny-cloud","action":"deny","reason":"cloud denied","provider_kinds":["cloud"]}`)
	admin.mustRequest(http.MethodPost, "/admin/control-plane/pricebook", `{"provider":"openai","model":"custom-model","input_micros_usd_per_million_tokens":100000,"output_micros_usd_per_million_tokens":200000}`)
	response := mustRequestJSON[ControlPlaneResponse](admin, http.MethodGet, "/admin/control-plane", "")
	if len(response.Data.PolicyRules) != 1 {
		t.Fatalf("policy rule count = %d, want 1", len(response.Data.PolicyRules))
	}
	if response.Data.PolicyRules[0].ID != "deny-cloud" {
		t.Fatalf("policy rule id = %q, want deny-cloud", response.Data.PolicyRules[0].ID)
	}
	if len(response.Data.Pricebook) != 1 {
		t.Fatalf("pricebook count = %d, want 1", len(response.Data.Pricebook))
	}
	if response.Data.Pricebook[0].Provider != "openai" || response.Data.Pricebook[0].Model != "custom-model" {
		t.Fatalf("pricebook entry = %s/%s, want openai/custom-model", response.Data.Pricebook[0].Provider, response.Data.Pricebook[0].Model)
	}

	admin.mustRequest(http.MethodPost, "/admin/control-plane/policy-rules/delete", `{"id":"deny-cloud"}`)
	admin.mustRequest(http.MethodPost, "/admin/control-plane/pricebook/delete", `{"provider":"openai","model":"custom-model"}`)
}

func TestControlPlaneLifecycleEndpoints(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	store := controlplane.NewMemoryStore()

	handler := newBudgetTestHandlerWithConfig(logger, config.Config{
		Server: config.ServerConfig{
			AuthToken: "admin-secret",
		},
		Governor: config.GovernorConfig{
			MaxPromptTokens:      64_000,
			MaxTotalBudgetMicros: 10_000_000,
			BudgetBackend:        "memory",
			BudgetKey:            "global",
			BudgetScope:          "global",
		},
	}, governor.NewMemoryBudgetStore(), store)
	admin := newAPITestClient(t, handler).withBearerToken("admin-secret")
	admin.mustRequest(http.MethodPost, "/admin/control-plane/tenants", `{"name":"Team A","enabled":true}`)
	admin.mustRequest(http.MethodPost, "/admin/control-plane/api-keys", `{"name":"Team A Dev","key":"secret","tenant":"team-a","role":"tenant","enabled":true}`)
	admin.mustRequest(http.MethodPost, "/admin/control-plane/tenants/enabled", `{"id":"team-a","enabled":false}`)
	admin.mustRequest(http.MethodPost, "/admin/control-plane/api-keys/enabled", `{"id":"team-a-dev","enabled":false}`)
	admin.mustRequest(http.MethodPost, "/admin/control-plane/api-keys/rotate", `{"id":"team-a-dev","key":"new-secret"}`)
	admin.mustRequestStatus(http.StatusBadRequest, http.MethodPost, "/admin/control-plane/tenants/delete", `{"id":"team-a"}`)
	admin.mustRequest(http.MethodPost, "/admin/control-plane/api-keys/delete", `{"id":"team-a-dev"}`)
	admin.mustRequest(http.MethodPost, "/admin/control-plane/tenants/delete", `{"id":"team-a"}`)
	response := mustRequestJSON[ControlPlaneResponse](admin, http.MethodGet, "/admin/control-plane", "")
	if len(response.Data.Events) != 7 {
		t.Fatalf("event count = %d, want 7", len(response.Data.Events))
	}
	if response.Data.Events[0].Actor == "" {
		t.Fatal("expected control plane audit actor to be populated")
	}
	if response.Data.Events[len(response.Data.Events)-1].Action != "tenant.deleted" {
		t.Fatalf("last event action = %q, want tenant.deleted", response.Data.Events[len(response.Data.Events)-1].Action)
	}
}

func TestBudgetStatusReturnsCurrentBalance(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	budgetStore := governor.NewMemoryBudgetStore()
	if _, err := budgetStore.Credit(context.Background(), "global", 5_000_000); err != nil {
		t.Fatalf("Credit() error = %v", err)
	}
	if _, err := budgetStore.Debit(context.Background(), governor.UsageEvent{BudgetKey: "global", CostMicros: 3_000}); err != nil {
		t.Fatalf("Debit() error = %v", err)
	}

	handler := newBudgetTestHandler(logger, config.GovernorConfig{
		MaxPromptTokens:      64_000,
		MaxTotalBudgetMicros: 5_000_000,
		BudgetBackend:        "memory",
		BudgetKey:            "global",
		BudgetScope:          "global",
	}, budgetStore)

	client := newAPITestClient(t, handler)
	response := mustRequestJSON[BudgetStatusResponse](client, http.MethodGet, "/admin/budget", "")
	if response.Object != "budget_status" {
		t.Fatalf("object = %q, want budget_status", response.Object)
	}
	if response.Data.Key != "global" {
		t.Fatalf("key = %q, want global", response.Data.Key)
	}
	if response.Data.BalanceMicrosUSD != 4_997_000 {
		t.Fatalf("balance_micros_usd = %d, want 4997000", response.Data.BalanceMicrosUSD)
	}
	if response.Data.DebitedMicrosUSD != 3_000 {
		t.Fatalf("debited_micros_usd = %d, want 3000", response.Data.DebitedMicrosUSD)
	}
	if len(response.Data.Warnings) == 0 {
		t.Fatal("warnings = empty, want configured default warnings")
	}
}

func TestBudgetResetSupportsExplicitKey(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	budgetStore := governor.NewMemoryBudgetStore()
	if _, err := budgetStore.Credit(context.Background(), "team-a", 20_000); err != nil {
		t.Fatalf("Credit() error = %v", err)
	}
	if _, err := budgetStore.Debit(context.Background(), governor.UsageEvent{BudgetKey: "team-a", CostMicros: 9_999}); err != nil {
		t.Fatalf("Debit() error = %v", err)
	}

	handler := newBudgetTestHandler(logger, config.GovernorConfig{
		MaxPromptTokens:      64_000,
		MaxTotalBudgetMicros: 10_000_000,
		BudgetBackend:        "memory",
		BudgetKey:            "global",
		BudgetScope:          "global",
	}, budgetStore)

	client := newAPITestClient(t, handler)
	response := mustRequestJSON[BudgetStatusResponse](client, http.MethodPost, "/admin/budget/reset", `{"key":"team-a"}`)
	if response.Data.Key != "team-a" {
		t.Fatalf("key = %q, want team-a", response.Data.Key)
	}
	if response.Data.BalanceMicrosUSD != 0 {
		t.Fatalf("balance_micros_usd = %d, want 0", response.Data.BalanceMicrosUSD)
	}
}

func TestBudgetStatusSupportsTenantProviderScope(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	budgetStore := governor.NewMemoryBudgetStore()
	if _, err := budgetStore.Credit(context.Background(), "global:tenant:team-a:provider:ollama", 10_000); err != nil {
		t.Fatalf("Credit() error = %v", err)
	}
	if _, err := budgetStore.Debit(context.Background(), governor.UsageEvent{
		BudgetKey:  "global:tenant:team-a:provider:ollama",
		CostMicros: 7_500,
	}); err != nil {
		t.Fatalf("Debit() error = %v", err)
	}

	handler := newBudgetTestHandler(logger, config.GovernorConfig{
		MaxPromptTokens:      64_000,
		MaxTotalBudgetMicros: 10_000_000,
		BudgetBackend:        "memory",
		BudgetKey:            "global",
		BudgetScope:          "tenant_provider",
		BudgetTenantFallback: "anonymous",
	}, budgetStore)

	client := newAPITestClient(t, handler)
	response := mustRequestJSON[BudgetStatusResponse](client, http.MethodGet, "/admin/budget?scope=tenant_provider&tenant=team-a&provider=ollama", "")
	if response.Data.Scope != "tenant_provider" {
		t.Fatalf("scope = %q, want tenant_provider", response.Data.Scope)
	}
	if response.Data.Provider != "ollama" {
		t.Fatalf("provider = %q, want ollama", response.Data.Provider)
	}
	if response.Data.Tenant != "team-a" {
		t.Fatalf("tenant = %q, want team-a", response.Data.Tenant)
	}
	if response.Data.BalanceMicrosUSD != 2_500 {
		t.Fatalf("balance_micros_usd = %d, want 2500", response.Data.BalanceMicrosUSD)
	}
}

func TestBudgetTopUpAndSetLimitEndpoints(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	budgetStore := governor.NewMemoryBudgetStore()

	handler := newBudgetTestHandler(logger, config.GovernorConfig{
		MaxPromptTokens:      64_000,
		MaxTotalBudgetMicros: 5_000_000,
		BudgetBackend:        "memory",
		BudgetKey:            "global",
		BudgetScope:          "tenant_provider",
		BudgetTenantFallback: "anonymous",
	}, budgetStore)

	client := newAPITestClient(t, handler)
	topUpResponse := mustRequestJSON[BudgetStatusResponse](client, http.MethodPost, "/admin/budget/topup", `{"scope":"tenant_provider","tenant":"team-a","provider":"ollama","amount_micros_usd":2000000}`)
	if topUpResponse.Data.BalanceMicrosUSD != 2_000_000 {
		t.Fatalf("topup balance_micros_usd = %d, want 2000000", topUpResponse.Data.BalanceMicrosUSD)
	}
	if topUpResponse.Data.BalanceSource != "store" {
		t.Fatalf("topup balance_source = %q, want store", topUpResponse.Data.BalanceSource)
	}

	limitResponse := mustRequestJSON[BudgetStatusResponse](client, http.MethodPost, "/admin/budget/limit", `{"scope":"tenant_provider","tenant":"team-a","provider":"ollama","balance_micros_usd":500000}`)
	if limitResponse.Data.BalanceMicrosUSD != 500_000 {
		t.Fatalf("limit balance_micros_usd = %d, want 500000", limitResponse.Data.BalanceMicrosUSD)
	}
	if len(limitResponse.Data.History) != 2 {
		t.Fatalf("limit history length = %d, want 2", len(limitResponse.Data.History))
	}
	if limitResponse.Data.History[0].Type != "set_balance" {
		t.Fatalf("latest history type = %q, want set_balance", limitResponse.Data.History[0].Type)
	}
	if limitResponse.Data.History[1].Type != "top_up" {
		t.Fatalf("older history type = %q, want top_up", limitResponse.Data.History[1].Type)
	}
}

func TestAccountSummaryReturnsModelEstimates(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	budgetStore := governor.NewMemoryBudgetStore()
	if _, err := budgetStore.Credit(context.Background(), "global", 1_000_000); err != nil {
		t.Fatalf("Credit() error = %v", err)
	}

	handler := newBudgetTestHandler(logger, config.GovernorConfig{
		MaxPromptTokens:      64_000,
		MaxTotalBudgetMicros: 1_000_000,
		BudgetBackend:        "memory",
		BudgetKey:            "global",
		BudgetScope:          "global",
	}, budgetStore)

	client := newAPITestClient(t, handler)
	response := mustRequestJSON[AccountSummaryResponse](client, http.MethodGet, "/admin/accounts/summary", "")
	if response.Object != "account_summary" {
		t.Fatalf("object = %q, want account_summary", response.Object)
	}
	if response.Data.Account.BalanceMicrosUSD != 1_000_000 {
		t.Fatalf("balance_micros_usd = %d, want 1000000", response.Data.Account.BalanceMicrosUSD)
	}
	if len(response.Data.Estimates) == 0 {
		t.Fatal("estimates = empty, want model estimates")
	}
}

func TestRequestLedgerReturnsRecentBudgetEvents(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	budgetStore := governor.NewMemoryBudgetStore()
	now := time.Now().UTC()
	if err := budgetStore.AppendEvent(context.Background(), governor.BudgetEvent{
		Key:               "global:tenant:team-a:provider:openai",
		Type:              "debit",
		Scope:             "tenant_provider",
		Provider:          "openai",
		Tenant:            "team-a",
		Model:             "gpt-4o-mini",
		RequestID:         "req-newer",
		AmountMicrosUSD:   3200,
		BalanceMicrosUSD:  996800,
		CreditedMicrosUSD: 1_000_000,
		DebitedMicrosUSD:  3200,
		PromptTokens:      12,
		CompletionTokens:  4,
		TotalTokens:       16,
		OccurredAt:        now,
	}); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
	if err := budgetStore.AppendEvent(context.Background(), governor.BudgetEvent{
		Key:               "global:tenant:team-b:provider:ollama",
		Type:              "debit",
		Scope:             "tenant_provider",
		Provider:          "ollama",
		Tenant:            "team-b",
		Model:             "llama3.1:8b",
		RequestID:         "req-older",
		AmountMicrosUSD:   0,
		BalanceMicrosUSD:  500_000,
		CreditedMicrosUSD: 500_000,
		DebitedMicrosUSD:  0,
		PromptTokens:      20,
		CompletionTokens:  5,
		TotalTokens:       25,
		OccurredAt:        now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}

	handler := newBudgetTestHandler(logger, config.GovernorConfig{
		MaxPromptTokens:      64_000,
		MaxTotalBudgetMicros: 1_000_000,
		BudgetBackend:        "memory",
		BudgetKey:            "global",
		BudgetScope:          "global",
	}, budgetStore)

	client := newAPITestClient(t, handler)
	response := mustRequestJSON[RequestLedgerResponse](client, http.MethodGet, "/admin/requests?limit=1", "")
	if response.Object != "request_ledger" {
		t.Fatalf("object = %q, want request_ledger", response.Object)
	}
	if len(response.Data) != 1 {
		t.Fatalf("entries = %d, want 1", len(response.Data))
	}
	if response.Data[0].RequestID != "req-newer" {
		t.Fatalf("request_id = %q, want req-newer", response.Data[0].RequestID)
	}
	if response.Data[0].Model != "gpt-4o-mini" {
		t.Fatalf("model = %q, want gpt-4o-mini", response.Data[0].Model)
	}
}

func TestChatSessionsPersistMessagesAndProviderCalls(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{
		name: "anthropic",
		capabilities: providers.Capabilities{
			Name:         "anthropic",
			Kind:         providers.KindCloud,
			DefaultModel: "claude-sonnet-4-20250514",
			Models:       []string{"claude-sonnet-4-20250514"},
		},
		response: &types.ChatResponse{
			ID:        "msg_123",
			Model:     "claude-sonnet-4-20250514",
			CreatedAt: time.Now().UTC(),
			Choices:   []types.ChatChoice{{Index: 0, Message: types.Message{Role: "assistant", Content: "Hello from Claude."}, FinishReason: "end_turn"}},
			Usage:     types.Usage{PromptTokens: 12, CompletionTokens: 4, TotalTokens: 16},
		},
	}

	handler := newTestHTTPHandlerForProviders(logger, []providers.Provider{provider}, config.Config{
		Router: config.RouterConfig{
			DefaultModel: "claude-sonnet-4-20250514",
		},
	})
	client := newAPITestClient(t, handler)
	created := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/v1/chat/sessions", `{"title":"Claude debugging"}`)
	if created.Data.ID == "" {
		t.Fatal("session id = empty, want session id")
	}

	chatBody := fmt.Sprintf(`{"model":"claude-sonnet-4-20250514","provider":"anthropic","session_id":"%s","messages":[{"role":"user","content":"Say hello."}]}`, created.Data.ID)
	chatRecorder := performJSONRequest(t, handler, chatBody)
	if chatRecorder.Code != http.StatusOK {
		t.Fatalf("chat status = %d, want %d, body=%s", chatRecorder.Code, http.StatusOK, chatRecorder.Body.String())
	}

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodGet, "/v1/chat/sessions/"+created.Data.ID, "")
	if len(session.Data.Messages) != 2 {
		t.Fatalf("messages = %d, want 2 (user + assistant)", len(session.Data.Messages))
	}
	if len(session.Data.ProviderCalls) != 1 {
		t.Fatalf("provider_calls = %d, want 1", len(session.Data.ProviderCalls))
	}
	user := session.Data.Messages[0]
	assistant := session.Data.Messages[1]
	if user.Role != "user" || user.Content.AsString() != "Say hello." {
		t.Fatalf("user message = {%q, %q}, want user/Say hello.", user.Role, user.Content.AsString())
	}
	if user.Sequence != 0 || user.ProducedByCallID != "" {
		t.Fatalf("user metadata = {seq=%d, produced_by=%q}, want seq=0/produced_by=\"\"", user.Sequence, user.ProducedByCallID)
	}
	if assistant.Role != "assistant" || assistant.Content.AsString() != "Hello from Claude." {
		t.Fatalf("assistant message = {%q, %q}", assistant.Role, assistant.Content.AsString())
	}
	if assistant.Sequence != 1 || assistant.ProducedByCallID == "" {
		t.Fatalf("assistant metadata = {seq=%d, produced_by=%q}, want seq=1/produced_by != empty", assistant.Sequence, assistant.ProducedByCallID)
	}
	call := session.Data.ProviderCalls[0]
	if call.Provider != "anthropic" || call.Model != "claude-sonnet-4-20250514" {
		t.Fatalf("provider_call = {provider=%q, model=%q}, want anthropic/Claude", call.Provider, call.Model)
	}
	if assistant.ProducedByCallID != call.ID {
		t.Fatalf("assistant.produced_by_call_id = %q, want %q (call.id)", assistant.ProducedByCallID, call.ID)
	}
	if call.PromptTokens != 12 || call.CompletionTokens != 4 || call.TotalTokens != 16 {
		t.Fatalf("token usage on provider_call = {%d, %d, %d}, want 12/4/16", call.PromptTokens, call.CompletionTokens, call.TotalTokens)
	}
}

// TestChatSessionSystemPromptIsPrepended is the end-to-end check for B1:
// PATCH a session's system_prompt, GET it back, then make a chat call
// targeting that session and verify the gateway prepended the prompt as a
// system-role message before forwarding to the provider.
func TestChatSessionSystemPromptIsPrepended(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{
		name: "openai",
		capabilities: providers.Capabilities{
			Name:         "openai",
			Kind:         providers.KindCloud,
			DefaultModel: "gpt-4o-mini",
			Models:       []string{"gpt-4o-mini"},
		},
		response: &types.ChatResponse{
			ID:        "msg_1",
			Model:     "gpt-4o-mini",
			CreatedAt: time.Now().UTC(),
			Choices:   []types.ChatChoice{{Index: 0, Message: types.Message{Role: "assistant", Content: "ok"}, FinishReason: "stop"}},
			Usage:     types.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
		},
	}
	handler := newTestHTTPHandlerForProviders(logger, []providers.Provider{provider}, config.Config{
		Router: config.RouterConfig{DefaultModel: "gpt-4o-mini"},
	})
	client := newAPITestClient(t, handler)

	created := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/v1/chat/sessions", `{"title":"with system"}`)
	if created.Data.SystemPrompt != "" {
		t.Fatalf("freshly-created session SystemPrompt = %q, want empty", created.Data.SystemPrompt)
	}

	const prompt = "you are a terse assistant"
	patched := mustRequestJSON[ChatSessionResponse](client, http.MethodPatch, "/v1/chat/sessions/"+created.Data.ID, `{"system_prompt":"`+prompt+`"}`)
	if patched.Data.SystemPrompt != prompt {
		t.Fatalf("PATCH response SystemPrompt = %q, want %q", patched.Data.SystemPrompt, prompt)
	}
	// PATCH must not clobber the title.
	if patched.Data.Title != "with system" {
		t.Fatalf("PATCH cleared Title: got %q, want %q", patched.Data.Title, "with system")
	}

	// GET round-trip — confirms persistence (memory store, but exercises
	// the API response shape too).
	roundTripped := mustRequestJSON[ChatSessionResponse](client, http.MethodGet, "/v1/chat/sessions/"+created.Data.ID, "")
	if roundTripped.Data.SystemPrompt != prompt {
		t.Fatalf("GET SystemPrompt = %q, want %q", roundTripped.Data.SystemPrompt, prompt)
	}

	// Chat completion targeting the session — the prompt should be
	// prepended so the provider sees [system, user] instead of [user].
	body := fmt.Sprintf(`{"model":"gpt-4o-mini","provider":"openai","session_id":"%s","messages":[{"role":"user","content":"hi"}]}`, created.Data.ID)
	rec := performJSONRequest(t, handler, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("chat status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	got := provider.LastRequest()
	if len(got.Messages) != 2 {
		t.Fatalf("provider received %d messages, want 2 (prepended system + user); got %+v", len(got.Messages), got.Messages)
	}
	if got.Messages[0].Role != "system" || got.Messages[0].Content != prompt {
		t.Fatalf("first message = {%q, %q}, want {system, %q}", got.Messages[0].Role, got.Messages[0].Content, prompt)
	}
	if got.Messages[1].Role != "user" || got.Messages[1].Content != "hi" {
		t.Fatalf("second message = {%q, %q}, want {user, hi}", got.Messages[1].Role, got.Messages[1].Content)
	}
}

// TestChatSessionSystemPromptDoesNotOverrideExplicit covers the "client
// already sends a system message" branch — the session's stored prompt
// must NOT be prepended in that case, otherwise per-call overrides become
// impossible.
func TestChatSessionSystemPromptDoesNotOverrideExplicit(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{
		name: "openai",
		capabilities: providers.Capabilities{
			Name:         "openai",
			Kind:         providers.KindCloud,
			DefaultModel: "gpt-4o-mini",
			Models:       []string{"gpt-4o-mini"},
		},
		response: &types.ChatResponse{
			ID:        "msg_1",
			Model:     "gpt-4o-mini",
			CreatedAt: time.Now().UTC(),
			Choices:   []types.ChatChoice{{Index: 0, Message: types.Message{Role: "assistant", Content: "ok"}, FinishReason: "stop"}},
			Usage:     types.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
		},
	}
	handler := newTestHTTPHandlerForProviders(logger, []providers.Provider{provider}, config.Config{
		Router: config.RouterConfig{DefaultModel: "gpt-4o-mini"},
	})
	client := newAPITestClient(t, handler)

	created := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/v1/chat/sessions", `{"title":"override test"}`)
	mustRequestJSON[ChatSessionResponse](client, http.MethodPatch, "/v1/chat/sessions/"+created.Data.ID, `{"system_prompt":"session-level prompt"}`)

	body := fmt.Sprintf(`{"model":"gpt-4o-mini","provider":"openai","session_id":"%s","messages":[{"role":"system","content":"per-call override"},{"role":"user","content":"hi"}]}`, created.Data.ID)
	rec := performJSONRequest(t, handler, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("chat status = %d, want 200", rec.Code)
	}

	got := provider.LastRequest()
	if len(got.Messages) != 2 {
		t.Fatalf("provider received %d messages, want exactly 2 (no double system); got %+v", len(got.Messages), got.Messages)
	}
	if got.Messages[0].Content != "per-call override" {
		t.Fatalf("first message Content = %q, want per-call override (session prompt should NOT have been prepended on top)", got.Messages[0].Content)
	}
}

func TestTasksCreateListAndGet(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := newTestHTTPHandlerForProviders(logger, nil, config.Config{})
	tasks := newTaskTestClient(t, handler)

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/v1/tasks", `{"title":"Upgrade TypeScript","prompt":"Upgrade the UI workspace to TypeScript 7 beta.","repo":"hecate","base_branch":"main","workspace_mode":"ephemeral","requested_model":"gpt-5.4-mini","requested_provider":"openai","budget_micros_usd":500000}`)
	if created.Object != "task" {
		t.Fatalf("object = %q, want task", created.Object)
	}
	if created.Data.ID == "" {
		t.Fatal("task id = empty, want task id")
	}
	if created.Data.Status != "queued" {
		t.Fatalf("status = %q, want queued", created.Data.Status)
	}
	if created.Data.Repo != "hecate" {
		t.Fatalf("repo = %q, want hecate", created.Data.Repo)
	}

	listed := mustTaskRequestJSON[TasksResponse](tasks, http.MethodGet, "/v1/tasks?limit=10", "")
	if listed.Object != "tasks" {
		t.Fatalf("object = %q, want tasks", listed.Object)
	}
	if len(listed.Data) != 1 {
		t.Fatalf("tasks = %d, want 1", len(listed.Data))
	}
	if listed.Data[0].ID != created.Data.ID {
		t.Fatalf("listed task id = %q, want %q", listed.Data[0].ID, created.Data.ID)
	}

	fetched := mustTaskRequestJSON[TaskResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID, "")
	if fetched.Data.ID != created.Data.ID {
		t.Fatalf("fetched task id = %q, want %q", fetched.Data.ID, created.Data.ID)
	}
	if fetched.Data.Prompt != "Upgrade the UI workspace to TypeScript 7 beta." {
		t.Fatalf("prompt = %q, want original prompt", fetched.Data.Prompt)
	}

	startRecorder := tasks.mustRequest(http.MethodPost, "/v1/tasks/"+created.Data.ID+"/start", "")
	if got := startRecorder.Header().Get("X-Trace-Id"); got == "" {
		t.Fatal("X-Trace-Id = empty, want trace id")
	}
	if got := startRecorder.Header().Get("X-Span-Id"); got == "" {
		t.Fatal("X-Span-Id = empty, want span id")
	}

	started := decodeRecorder[TaskRunResponse](t, startRecorder)
	if started.Object != "task_run" {
		t.Fatalf("object = %q, want task_run", started.Object)
	}
	if started.Data.ID == "" {
		t.Fatal("run id = empty, want run id")
	}
	if started.Data.Status != "queued" {
		t.Fatalf("run status = %q, want queued", started.Data.Status)
	}
	completedRun := waitForRunStatus(t, handler, created.Data.ID, started.Data.ID, "completed")

	runs := mustTaskRequestJSON[TaskRunsResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID+"/runs", "")
	if len(runs.Data) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs.Data))
	}
	if runs.Data[0].ID != started.Data.ID {
		t.Fatalf("run id = %q, want %q", runs.Data[0].ID, started.Data.ID)
	}

	fetchedRun := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID, "")
	if fetchedRun.Data.ID != started.Data.ID {
		t.Fatalf("fetched run id = %q, want %q", fetchedRun.Data.ID, started.Data.ID)
	}
	if fetchedRun.Data.Status != "completed" {
		t.Fatalf("fetched run status = %q, want completed", fetchedRun.Data.Status)
	}

	steps := mustTaskRequestJSON[TaskStepsResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/steps", "")
	if len(steps.Data) != 1 {
		t.Fatalf("steps = %d, want 1", len(steps.Data))
	}
	if steps.Data[0].Kind != "model" {
		t.Fatalf("step kind = %q, want model", steps.Data[0].Kind)
	}

	step := mustTaskRequestJSON[TaskStepResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/steps/"+steps.Data[0].ID, "")
	if step.Data.ID != steps.Data[0].ID {
		t.Fatalf("step id = %q, want %q", step.Data.ID, steps.Data[0].ID)
	}

	artifacts := mustTaskRequestJSON[TaskArtifactsResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID+"/artifacts", "")
	if len(artifacts.Data) != 1 {
		t.Fatalf("artifacts = %d, want 1", len(artifacts.Data))
	}
	if artifacts.Data[0].Kind != "summary" {
		t.Fatalf("artifact kind = %q, want summary", artifacts.Data[0].Kind)
	}

	runArtifacts := mustTaskRequestJSON[TaskArtifactsResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/artifacts", "")
	if len(runArtifacts.Data) != 1 {
		t.Fatalf("run artifacts = %d, want 1", len(runArtifacts.Data))
	}
	if runArtifacts.Data[0].ID != artifacts.Data[0].ID {
		t.Fatalf("run artifact id = %q, want %q", runArtifacts.Data[0].ID, artifacts.Data[0].ID)
	}

	fetchedAfterStart := waitForTaskStatus(t, handler, created.Data.ID, "completed")
	if fetchedAfterStart.Data.LatestRunID != started.Data.ID {
		t.Fatalf("latest_run_id = %q, want %q", fetchedAfterStart.Data.LatestRunID, started.Data.ID)
	}
	if fetchedAfterStart.Data.StepCount != 1 {
		t.Fatalf("task step_count = %d, want 1", fetchedAfterStart.Data.StepCount)
	}
	if fetchedAfterStart.Data.ArtifactCount != 1 {
		t.Fatalf("task artifact_count = %d, want 1", fetchedAfterStart.Data.ArtifactCount)
	}
	if completedRun.Data.StepCount != 1 {
		t.Fatalf("step_count = %d, want 1", completedRun.Data.StepCount)
	}
	if completedRun.Data.ArtifactCount != 1 {
		t.Fatalf("artifact_count = %d, want 1", completedRun.Data.ArtifactCount)
	}
}

func TestTaskRunLifecycleEventsForSuccessfulRun(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := newTestHTTPHandlerForProviders(logger, nil, config.Config{})
	tempDir := t.TempDir()
	tasks := newTaskTestClient(t, handler)

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/v1/tasks",
		fmt.Sprintf(`{"title":"Lifecycle","prompt":"Pin lifecycle events.","execution_kind":"file","file_operation":"write","file_path":"lifecycle.txt","file_content":"ok","working_directory":%q}`, tempDir))
	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/v1/tasks/"+created.Data.ID+"/start", "")
	if started.Data.Status != "queued" {
		t.Fatalf("start status = %q, want queued", started.Data.Status)
	}

	completed := waitForRunStatus(t, handler, created.Data.ID, started.Data.ID, "completed")
	if completed.Data.Status != "completed" {
		t.Fatalf("completed status = %q, want completed", completed.Data.Status)
	}

	events := waitForRunEvent(t, handler, created.Data.ID, started.Data.ID, "run.completed")
	assertEventOrder(t, events.Data, []string{"run.created", "run.queued", "run.running", "run.completed"})
	assertEventSequencesIncrease(t, events.Data)

	for _, event := range events.Data {
		if event.RequestID == "" {
			t.Fatalf("event %s request_id is empty", event.EventType)
		}
		if event.TraceID == "" {
			t.Fatalf("event %s trace_id is empty", event.EventType)
		}
		if event.EventType == "run.completed" {
			if status, _ := event.Data["status"].(string); status != "completed" {
				t.Fatalf("run.completed status payload = %q, want completed", status)
			}
		}
	}

	task := mustTaskRequestJSON[TaskResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID, "")
	if task.Data.Status != "completed" {
		t.Fatalf("task status = %q, want completed", task.Data.Status)
	}
	if task.Data.LatestRunID != started.Data.ID {
		t.Fatalf("latest_run_id = %q, want %q", task.Data.LatestRunID, started.Data.ID)
	}
}

func TestTaskStartShellExecutor(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := config.Config{Server: config.ServerConfig{TaskApprovalPolicies: []string{"shell_exec"}}}
	handler := newTestHTTPHandlerForProviders(logger, nil, cfg)
	tasks := newTaskTestClient(t, handler)

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/v1/tasks", `{"title":"Run shell","prompt":"Run a shell command.","execution_kind":"shell","shell_command":"printf 'hello '; sleep 0.2; printf 'from shell\n'","working_directory":".","timeout_ms":2000}`)
	if created.Data.ExecutionKind != "shell" {
		t.Fatalf("execution_kind = %q, want shell", created.Data.ExecutionKind)
	}

	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/v1/tasks/"+created.Data.ID+"/start", "")
	if started.Data.Status != "awaiting_approval" {
		t.Fatalf("run status = %q, want awaiting_approval", started.Data.Status)
	}
	if started.Data.ApprovalCount != 1 {
		t.Fatalf("approval_count = %d, want 1", started.Data.ApprovalCount)
	}

	approvals := mustTaskRequestJSON[TaskApprovalsResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID+"/approvals", "")
	if len(approvals.Data) != 1 {
		t.Fatalf("approvals = %d, want 1", len(approvals.Data))
	}
	if approvals.Data[0].Status != "pending" {
		t.Fatalf("approval status = %q, want pending", approvals.Data[0].Status)
	}
	if approvals.Data[0].Kind != "shell_command" {
		t.Fatalf("approval kind = %q, want shell_command", approvals.Data[0].Kind)
	}

	approval := mustTaskRequestJSON[TaskApprovalResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID+"/approvals/"+approvals.Data[0].ID, "")
	if approval.Data.ID != approvals.Data[0].ID {
		t.Fatalf("approval id = %q, want %q", approval.Data.ID, approvals.Data[0].ID)
	}

	resolved := mustTaskRequestJSON[TaskApprovalResponse](tasks, http.MethodPost, "/v1/tasks/"+created.Data.ID+"/approvals/"+approvals.Data[0].ID+"/resolve", `{"decision":"approve","note":"looks safe"}`)
	if resolved.Data.Status != "approved" {
		t.Fatalf("approval status = %q, want approved", resolved.Data.Status)
	}

	partialArtifacts := waitForRunArtifactsContaining(t, handler, created.Data.ID, started.Data.ID, "stdout", "hello ")
	foundPartial := false
	for _, artifact := range partialArtifacts.Data {
		if artifact.Kind == "stdout" && strings.Contains(artifact.ContentText, "hello ") {
			foundPartial = true
		}
	}
	if !foundPartial {
		t.Fatal("stdout artifact missing streamed partial output")
	}

	completedRun := waitForRunStatus(t, handler, created.Data.ID, started.Data.ID, "completed")
	if completedRun.Data.WorkspacePath == "" {
		t.Fatal("workspace_path is empty")
	}
	if completedRun.Data.ArtifactCount != 2 {
		t.Fatalf("artifact_count = %d, want 2", completedRun.Data.ArtifactCount)
	}

	steps := mustTaskRequestJSON[TaskStepsResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/steps", "")
	if len(steps.Data) != 1 {
		t.Fatalf("steps = %d, want 1", len(steps.Data))
	}
	if steps.Data[0].Kind != "shell" {
		t.Fatalf("step kind = %q, want shell", steps.Data[0].Kind)
	}
	if steps.Data[0].ExitCode != 0 {
		t.Fatalf("exit_code = %d, want 0", steps.Data[0].ExitCode)
	}

	runArtifacts := mustTaskRequestJSON[TaskArtifactsResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/artifacts", "")
	if len(runArtifacts.Data) != 2 {
		t.Fatalf("run artifacts = %d, want 2", len(runArtifacts.Data))
	}
	foundStdout := false
	for _, artifact := range runArtifacts.Data {
		if artifact.Kind == "stdout" && strings.Contains(artifact.ContentText, "hello from shell") {
			foundStdout = true
		}
	}
	if !foundStdout {
		t.Fatal("stdout artifact missing shell output")
	}
}

func TestTaskApprovalResolveReturnsConflictWhenAlreadyResolved(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := config.Config{Server: config.ServerConfig{TaskApprovalPolicies: []string{"shell_exec"}}}
	handler := newTestHTTPHandlerForProviders(logger, nil, cfg)
	tasks := newTaskTestClient(t, handler)

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/v1/tasks", `{"title":"Approve once","prompt":"Resolve one approval once.","execution_kind":"shell","shell_command":"printf 'approved-once\n'","working_directory":".","timeout_ms":2000}`)
	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/v1/tasks/"+created.Data.ID+"/start", "")
	if started.Data.Status != "awaiting_approval" {
		t.Fatalf("run status = %q, want awaiting_approval", started.Data.Status)
	}
	approvals := mustTaskRequestJSON[TaskApprovalsResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID+"/approvals", "")
	if len(approvals.Data) != 1 {
		t.Fatalf("approvals = %d, want 1", len(approvals.Data))
	}

	resolved := mustTaskRequestJSON[TaskApprovalResponse](tasks, http.MethodPost, "/v1/tasks/"+created.Data.ID+"/approvals/"+approvals.Data[0].ID+"/resolve", `{"decision":"approve","note":"first approval wins"}`)
	if resolved.Data.Status != "approved" {
		t.Fatalf("approval status = %q, want approved", resolved.Data.Status)
	}

	conflict := tasks.mustRequestStatus(http.StatusConflict, http.MethodPost, "/v1/tasks/"+created.Data.ID+"/approvals/"+approvals.Data[0].ID+"/resolve", `{"decision":"approve","note":"duplicate"}`)
	if !strings.Contains(conflict.Body.String(), "not pending") {
		t.Fatalf("conflict body = %s, want mention of not pending", conflict.Body.String())
	}

	waitForRunStatus(t, handler, created.Data.ID, started.Data.ID, "completed")
	runs := mustTaskRequestJSON[TaskRunsResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID+"/runs", "")
	if len(runs.Data) != 1 {
		t.Fatalf("runs = %d, want 1 (duplicate approval must not create another run)", len(runs.Data))
	}
	runArtifacts := mustTaskRequestJSON[TaskArtifactsResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/artifacts", "")
	stdoutCount := 0
	for _, artifact := range runArtifacts.Data {
		if artifact.Kind == "stdout" && strings.Contains(artifact.ContentText, "approved-once") {
			stdoutCount++
		}
	}
	if stdoutCount != 1 {
		t.Fatalf("stdout artifact count = %d, want 1 (duplicate approval must not execute twice)", stdoutCount)
	}
}

func TestTaskRejectApprovalCancelsRun(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := config.Config{Server: config.ServerConfig{TaskApprovalPolicies: []string{"shell_exec"}}}
	handler := newTestHTTPHandlerForProviders(logger, nil, cfg)
	tasks := newTaskTestClient(t, handler)

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/v1/tasks", `{"title":"Reject shell","prompt":"Reject a shell command.","execution_kind":"shell","shell_command":"printf 'should not run\n'","working_directory":".","timeout_ms":2000}`)
	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/v1/tasks/"+created.Data.ID+"/start", "")
	if started.Data.Status != "awaiting_approval" {
		t.Fatalf("run status = %q, want awaiting_approval", started.Data.Status)
	}

	approvals := mustTaskRequestJSON[TaskApprovalsResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID+"/approvals", "")
	if len(approvals.Data) != 1 {
		t.Fatalf("approvals = %d, want 1", len(approvals.Data))
	}

	resolveRecorder := tasks.mustRequest(http.MethodPost, "/v1/tasks/"+created.Data.ID+"/approvals/"+approvals.Data[0].ID+"/resolve", `{"decision":"reject","note":"not safe"}`)
	if got := resolveRecorder.Header().Get("X-Trace-Id"); got == "" {
		t.Fatal("X-Trace-Id = empty, want trace id")
	}
	if got := resolveRecorder.Header().Get("X-Span-Id"); got == "" {
		t.Fatal("X-Span-Id = empty, want span id")
	}

	resolved := decodeRecorder[TaskApprovalResponse](t, resolveRecorder)
	if resolved.Data.Status != "rejected" {
		t.Fatalf("approval status = %q, want rejected", resolved.Data.Status)
	}
	if resolved.Data.ResolutionNote != "not safe" {
		t.Fatalf("resolution_note = %q, want not safe", resolved.Data.ResolutionNote)
	}

	cancelledRun := waitForRunStatus(t, handler, created.Data.ID, started.Data.ID, "cancelled")
	if cancelledRun.Data.LastError != "approval rejected" {
		t.Fatalf("run last_error = %q, want approval rejected", cancelledRun.Data.LastError)
	}

	cancelledTask := waitForTaskStatus(t, handler, created.Data.ID, "cancelled")
	if cancelledTask.Data.LastError != "approval rejected" {
		t.Fatalf("task last_error = %q, want approval rejected", cancelledTask.Data.LastError)
	}
	if cancelledTask.Data.LatestRunID != started.Data.ID {
		t.Fatalf("latest_run_id = %q, want %q", cancelledTask.Data.LatestRunID, started.Data.ID)
	}

	steps := mustTaskRequestJSON[TaskStepsResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/steps", "")
	if len(steps.Data) != 0 {
		t.Fatalf("steps = %d, want 0", len(steps.Data))
	}
}

func TestTaskStartFileExecutor(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := newTestHTTPHandlerForProviders(logger, nil, config.Config{})
	tempDir := t.TempDir()
	tasks := newTaskTestClient(t, handler)

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/v1/tasks", fmt.Sprintf(`{"title":"Write file","prompt":"Write a file.","execution_kind":"file","file_operation":"write","file_path":"note.txt","file_content":"hello file","working_directory":%q}`, tempDir))
	if created.Data.ExecutionKind != "file" {
		t.Fatalf("execution_kind = %q, want file", created.Data.ExecutionKind)
	}

	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/v1/tasks/"+created.Data.ID+"/start", "")
	if started.Data.Status != "queued" {
		t.Fatalf("run status = %q, want queued", started.Data.Status)
	}
	if started.Data.WorkspacePath == "" {
		t.Fatal("workspace_path is empty")
	}
	waitForRunStatus(t, handler, created.Data.ID, started.Data.ID, "completed")

	steps := mustTaskRequestJSON[TaskStepsResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/steps", "")
	if len(steps.Data) != 1 || steps.Data[0].Kind != "file" {
		t.Fatalf("steps = %#v, want one file step", steps.Data)
	}

	content, err := os.ReadFile(filepath.Join(started.Data.WorkspacePath, "note.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(content) != "hello file" {
		t.Fatalf("file contents = %q, want hello file", string(content))
	}
}

func TestTaskStartGitExecutor(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := newTestHTTPHandlerForProviders(logger, nil, config.Config{})
	tempDir := t.TempDir()
	tasks := newTaskTestClient(t, handler)

	initCmd := exec.Command("git", "init")
	initCmd.Dir = tempDir
	if output, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v, output=%s", err, string(output))
	}

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/v1/tasks", fmt.Sprintf(`{"title":"Run git","prompt":"Run a git command.","execution_kind":"git","git_command":"status --short","working_directory":%q,"timeout_ms":2000}`, tempDir))
	if created.Data.ExecutionKind != "git" {
		t.Fatalf("execution_kind = %q, want git", created.Data.ExecutionKind)
	}

	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/v1/tasks/"+created.Data.ID+"/start", "")
	if started.Data.Status != "queued" {
		t.Fatalf("run status = %q, want queued", started.Data.Status)
	}
	if started.Data.WorkspacePath == "" {
		t.Fatal("workspace_path is empty")
	}
	waitForRunStatus(t, handler, created.Data.ID, started.Data.ID, "completed")

	steps := mustTaskRequestJSON[TaskStepsResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/steps", "")
	if len(steps.Data) != 1 || steps.Data[0].Kind != "git" {
		t.Fatalf("steps = %#v, want one git step", steps.Data)
	}

	artifacts := mustTaskRequestJSON[TaskArtifactsResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/artifacts", "")
	if len(artifacts.Data) != 2 {
		t.Fatalf("artifacts = %d, want 2", len(artifacts.Data))
	}
}

func TestTaskApprovedShellExecutorRespectsReadOnlyPolicy(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := config.Config{Server: config.ServerConfig{TaskApprovalPolicies: []string{"shell_exec"}}}
	handler := newTestHTTPHandlerForProviders(logger, nil, cfg)
	tasks := newTaskTestClient(t, handler)

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/v1/tasks", `{"title":"Denied shell","prompt":"Attempt a write.","execution_kind":"shell","shell_command":"touch denied.txt","working_directory":".","sandbox_read_only":true,"timeout_ms":2000}`)
	if !created.Data.SandboxReadOnly {
		t.Fatal("sandbox_read_only = false, want true")
	}

	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/v1/tasks/"+created.Data.ID+"/start", "")
	if started.Data.Status != "awaiting_approval" {
		t.Fatalf("run status = %q, want awaiting_approval", started.Data.Status)
	}

	approvals := mustTaskRequestJSON[TaskApprovalsResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID+"/approvals", "")
	if len(approvals.Data) != 1 {
		t.Fatalf("approvals = %d, want 1", len(approvals.Data))
	}

	tasks.mustRequest(http.MethodPost, "/v1/tasks/"+created.Data.ID+"/approvals/"+approvals.Data[0].ID+"/resolve", `{"decision":"approve"}`)

	failedRun := waitForRunStatus(t, handler, created.Data.ID, started.Data.ID, "failed")
	if failedRun.Data.Status != "failed" {
		t.Fatalf("run status = %q, want failed", failedRun.Data.Status)
	}

	steps := mustTaskRequestJSON[TaskStepsResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/steps", "")
	if len(steps.Data) != 1 {
		t.Fatalf("steps = %d, want 1", len(steps.Data))
	}
	if steps.Data[0].ErrorKind != "sandbox_policy_denied" {
		t.Fatalf("error_kind = %q, want sandbox_policy_denied", steps.Data[0].ErrorKind)
	}
}

func TestTaskStartFileExecutorRespectsAllowedRoot(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := newTestHTTPHandlerForProviders(logger, nil, config.Config{})
	tempDir := t.TempDir()
	workingDirectory := filepath.Join(tempDir, "workspace")
	if err := os.MkdirAll(workingDirectory, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	tasks := newTaskTestClient(t, handler)

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/v1/tasks", fmt.Sprintf(`{"title":"Escape root","prompt":"Try escaping allowed root.","execution_kind":"file","file_operation":"write","file_path":"../outside.txt","file_content":"blocked","working_directory":%q,"sandbox_allowed_root":%q}`, workingDirectory, workingDirectory))
	if created.Data.SandboxAllowedRoot != workingDirectory {
		t.Fatalf("sandbox_allowed_root = %q, want %q", created.Data.SandboxAllowedRoot, workingDirectory)
	}

	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/v1/tasks/"+created.Data.ID+"/start", "")
	if started.Data.Status != "queued" {
		t.Fatalf("run status = %q, want queued", started.Data.Status)
	}
	waitForRunStatus(t, handler, created.Data.ID, started.Data.ID, "failed")

	steps := mustTaskRequestJSON[TaskStepsResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/steps", "")
	if len(steps.Data) != 1 {
		t.Fatalf("steps = %d, want 1", len(steps.Data))
	}
	if steps.Data[0].ErrorKind != "sandbox_policy_denied" {
		t.Fatalf("error_kind = %q, want sandbox_policy_denied", steps.Data[0].ErrorKind)
	}
	if _, err := os.Stat(filepath.Join(tempDir, "outside.txt")); !os.IsNotExist(err) {
		t.Fatalf("outside.txt exists or unexpected stat error: %v", err)
	}
}

func TestTaskRunCancellation(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := config.Config{Server: config.ServerConfig{TaskApprovalPolicies: []string{"shell_exec"}}}
	handler := newTestHTTPHandlerForProviders(logger, nil, cfg)
	tasks := newTaskTestClient(t, handler)

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/v1/tasks", `{"title":"Cancel shell","prompt":"Cancel a long shell run.","execution_kind":"shell","shell_command":"printf 'starting\n'; sleep 5; printf 'done\n'","working_directory":".","timeout_ms":10000}`)
	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/v1/tasks/"+created.Data.ID+"/start", "")
	approvals := mustTaskRequestJSON[TaskApprovalsResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID+"/approvals", "")
	tasks.mustRequest(http.MethodPost, "/v1/tasks/"+created.Data.ID+"/approvals/"+approvals.Data[0].ID+"/resolve", `{"decision":"approve"}`)

	waitForRunStatus(t, handler, created.Data.ID, started.Data.ID, "running")

	tasks.mustRequest(http.MethodPost, "/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/cancel", "")

	cancelledRun := waitForRunStatus(t, handler, created.Data.ID, started.Data.ID, "cancelled")
	if cancelledRun.Data.Status != "cancelled" {
		t.Fatalf("run status = %q, want cancelled", cancelledRun.Data.Status)
	}

	steps := waitForRunStepStatus(t, handler, created.Data.ID, started.Data.ID, "cancelled")
	if len(steps.Data) != 1 {
		t.Fatalf("steps = %d, want 1", len(steps.Data))
	}
	if steps.Data[0].Status != "cancelled" {
		t.Fatalf("step status = %q, want cancelled", steps.Data[0].Status)
	}
	if steps.Data[0].ErrorKind != "run_cancelled" {
		t.Fatalf("step error_kind = %q, want run_cancelled", steps.Data[0].ErrorKind)
	}

	events := waitForRunEvent(t, handler, created.Data.ID, started.Data.ID, "run.cancelled")
	assertEventOrder(t, events.Data, []string{"run.created", "run.queued", "run.running", "run.cancelled"})
	cancelledCount := countTaskRunEvents(events.Data, "run.cancelled")
	if cancelledCount != 1 {
		t.Fatalf("run.cancelled event count = %d, want 1", cancelledCount)
	}

	again := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/cancel", "")
	if again.Data.Status != "cancelled" {
		t.Fatalf("second cancel status = %q, want cancelled", again.Data.Status)
	}
	afterDuplicate := mustTaskRequestJSON[TaskRunEventsResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/events", "")
	duplicateCancelledCount := countTaskRunEvents(afterDuplicate.Data, "run.cancelled")
	if duplicateCancelledCount != 1 {
		t.Fatalf("run.cancelled event count after duplicate cancel = %d, want 1", duplicateCancelledCount)
	}
}

func TestTaskRunStreamSSE(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := config.Config{Server: config.ServerConfig{TaskApprovalPolicies: []string{"shell_exec"}}}
	handler := newTestHTTPHandlerForProviders(logger, nil, cfg)
	server := httptest.NewServer(handler)
	defer server.Close()

	createResp := postJSONToURL(t, server.URL+"/v1/tasks", `{"title":"Stream shell","prompt":"Stream a shell command.","execution_kind":"shell","shell_command":"printf 'hello '; sleep 0.3; printf 'stream\n'","working_directory":".","timeout_ms":3000}`)
	if createResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(createResp.Body)
		t.Fatalf("create status = %d, want %d, body=%s", createResp.StatusCode, http.StatusOK, string(body))
	}
	var created TaskResponse
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	createResp.Body.Close()

	startResp := postJSONToURL(t, server.URL+"/v1/tasks/"+created.Data.ID+"/start", "")
	if startResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(startResp.Body)
		t.Fatalf("start status = %d, want %d, body=%s", startResp.StatusCode, http.StatusOK, string(body))
	}
	var started TaskRunResponse
	if err := json.NewDecoder(startResp.Body).Decode(&started); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	startResp.Body.Close()

	approvalsResp, err := http.Get(server.URL + "/v1/tasks/" + created.Data.ID + "/approvals")
	if err != nil {
		t.Fatalf("Get approvals error = %v", err)
	}
	if approvalsResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(approvalsResp.Body)
		t.Fatalf("approvals status = %d, want %d, body=%s", approvalsResp.StatusCode, http.StatusOK, string(body))
	}
	var approvals TaskApprovalsResponse
	if err := json.NewDecoder(approvalsResp.Body).Decode(&approvals); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	approvalsResp.Body.Close()

	streamReq, err := http.NewRequest(http.MethodGet, server.URL+"/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/stream", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	streamCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	streamReq = streamReq.WithContext(streamCtx)
	streamResp, err := http.DefaultClient.Do(streamReq)
	if err != nil {
		t.Fatalf("stream request error = %v", err)
	}
	defer streamResp.Body.Close()
	if got := streamResp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream", got)
	}

	resolveErrCh := make(chan error, 1)
	go func() {
		time.Sleep(100 * time.Millisecond)
		resolveResp, err := http.Post(server.URL+"/v1/tasks/"+created.Data.ID+"/approvals/"+approvals.Data[0].ID+"/resolve", "application/json", strings.NewReader(`{"decision":"approve"}`))
		if err != nil {
			resolveErrCh <- err
			return
		}
		defer resolveResp.Body.Close()
		io.Copy(io.Discard, resolveResp.Body)
		if resolveResp.StatusCode != http.StatusOK {
			resolveErrCh <- fmt.Errorf("resolve status = %d", resolveResp.StatusCode)
			return
		}
		resolveErrCh <- nil
	}()

	var sawAwaitingApproval bool
	var sawPartialStdout bool
	var sawTerminal bool
	for event := range readSSEEvents(t, streamResp.Body) {
		if event.Event != "snapshot" && event.Event != "done" {
			continue
		}
		var payload TaskRunStreamEventResponse
		if err := json.Unmarshal([]byte(event.Data), &payload); err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}
		if payload.Data.Run.Status == "awaiting_approval" {
			sawAwaitingApproval = true
		}
		for _, artifact := range payload.Data.Artifacts {
			if artifact.Kind == "stdout" && strings.Contains(artifact.ContentText, "hello ") && !strings.Contains(artifact.ContentText, "stream\n") {
				sawPartialStdout = true
			}
		}
		if payload.Data.Terminal || types.IsTerminalTaskRunStatus(payload.Data.Run.Status) {
			sawTerminal = true
		}
		if event.Event == "done" {
			break
		}
	}

	if !sawAwaitingApproval {
		t.Fatal("did not observe awaiting_approval stream snapshot")
	}
	if !sawPartialStdout {
		t.Fatal("did not observe partial stdout in stream snapshot")
	}
	if !sawTerminal {
		t.Fatal("did not observe terminal stream snapshot")
	}
	if err := <-resolveErrCh; err != nil {
		t.Fatalf("approval resolve error = %v", err)
	}
}

func TestTaskRunStream_PendingApprovalRidesAlongInSnapshot(t *testing.T) {
	// Pre-fix: the SSE payload carried only run/steps/artifacts. The
	// approval banner had to be loaded out-of-band via
	// /v1/tasks/{id}/approvals and could drift from the run state —
	// observed symptom: "the modal window for approval appears and
	// disappears in a moment". Now every snapshot includes Approvals
	// scoped to the streamed run, so the UI can drive the banner
	// directly off the SSE without a separate refetch.
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := config.Config{Server: config.ServerConfig{TaskApprovalPolicies: []string{"shell_exec"}}}
	handler := newTestHTTPHandlerForProviders(logger, nil, cfg)
	server := httptest.NewServer(handler)
	defer server.Close()

	createResp := postJSONToURL(t, server.URL+"/v1/tasks", `{"title":"Approval stream","prompt":"Stream test","execution_kind":"shell","shell_command":"echo hi","working_directory":".","timeout_ms":3000}`)
	if createResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(createResp.Body)
		t.Fatalf("create status = %d, body=%s", createResp.StatusCode, string(body))
	}
	var created TaskResponse
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	createResp.Body.Close()

	startResp := postJSONToURL(t, server.URL+"/v1/tasks/"+created.Data.ID+"/start", "")
	if startResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(startResp.Body)
		t.Fatalf("start status = %d, body=%s", startResp.StatusCode, string(body))
	}
	var started TaskRunResponse
	if err := json.NewDecoder(startResp.Body).Decode(&started); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	startResp.Body.Close()

	streamReq, err := http.NewRequest(http.MethodGet, server.URL+"/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/stream", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	streamCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	streamReq = streamReq.WithContext(streamCtx)
	streamResp, err := http.DefaultClient.Do(streamReq)
	if err != nil {
		t.Fatalf("stream request: %v", err)
	}
	defer streamResp.Body.Close()

	// Walk snapshots until we see one with the run in awaiting_approval
	// AND a pending approval embedded. Both pieces of state must
	// arrive together — the whole point of the fix.
	var sawApprovalInSnapshot bool
	for event := range readSSEEvents(t, streamResp.Body) {
		if event.Event != "snapshot" {
			continue
		}
		var payload TaskRunStreamEventResponse
		if err := json.Unmarshal([]byte(event.Data), &payload); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if payload.Data.Run.Status != "awaiting_approval" {
			continue
		}
		// Must find a pending approval scoped to this run in the
		// snapshot's Approvals slice. The "scoped to this run"
		// contract is what lets the banner toggle cleanly when the
		// user switches between runs of the same task.
		for _, a := range payload.Data.Approvals {
			if a.RunID == started.Data.ID && a.Status == "pending" {
				sawApprovalInSnapshot = true
				break
			}
		}
		if sawApprovalInSnapshot {
			break
		}
	}
	cancel()

	if !sawApprovalInSnapshot {
		t.Fatal("no snapshot carried run.status=awaiting_approval together with a pending approval")
	}
}

func TestTaskRunStream_AgentTurnCompletedFlowsTurnOverlayIntoSnapshot(t *testing.T) {
	// End-to-end check on the Turn overlay path:
	//
	//   1. Runner emits `agent.turn.completed` to the run-event log
	//   2. SSE handler reads the event, decodeTaskRunEventData treats
	//      it as Turn-only (ok=false)
	//   3. Handler preserves the overlay across buildTaskRunStreamState
	//   4. Final snapshot carries BOTH the rebuilt Run/Steps/Artifacts
	//      AND the Turn block
	//
	// The unit tests in turn_cost_stream_test.go pin steps 2-3 in
	// isolation. This test pins the wire-up: a regression that, say,
	// ran buildTaskRunStreamState without preserving overlayTurn
	// would silently swallow per-turn cost on the SSE feed without
	// any unit test failing. We POST the event via the public
	// /events endpoint so we don't need a real LLM.
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := newTestHTTPHandlerForProviders(logger, nil, config.Config{})
	server := httptest.NewServer(handler)
	defer server.Close()

	createResp := postJSONToURL(t, server.URL+"/v1/tasks", `{"title":"Turn overlay","prompt":"Test turn overlay flow","execution_kind":"shell","shell_command":"echo hi","working_directory":".","timeout_ms":3000}`)
	if createResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(createResp.Body)
		t.Fatalf("create status = %d, body=%s", createResp.StatusCode, string(body))
	}
	var created TaskResponse
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	createResp.Body.Close()

	startResp := postJSONToURL(t, server.URL+"/v1/tasks/"+created.Data.ID+"/start", "")
	if startResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(startResp.Body)
		t.Fatalf("start status = %d, body=%s", startResp.StatusCode, string(body))
	}
	var started TaskRunResponse
	if err := json.NewDecoder(startResp.Body).Decode(&started); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	startResp.Body.Close()

	// Inject an agent.turn.completed event via the public events
	// endpoint. The endpoint always merges a `snapshot` key into
	// data — but the decoder's agent.turn.completed branch is
	// checked BEFORE the snapshot branch, so the type-specific
	// path wins (which is what we're testing).
	eventBody := `{
		"event_type": "agent.turn.completed",
		"data": {
			"turn": 2,
			"step_id": "step-injected",
			"cost_micros_usd": 4242,
			"run_cumulative_cost_micros_usd": 7777,
			"task_cumulative_cost_micros_usd": 12345,
			"tool_call_count": 1
		}
	}`
	eventResp := postJSONToURL(t, server.URL+"/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/events", eventBody)
	if eventResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(eventResp.Body)
		t.Fatalf("post event status = %d, body=%s", eventResp.StatusCode, string(body))
	}
	eventResp.Body.Close()

	streamReq, err := http.NewRequest(http.MethodGet, server.URL+"/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/stream", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	streamCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	streamReq = streamReq.WithContext(streamCtx)
	streamResp, err := http.DefaultClient.Do(streamReq)
	if err != nil {
		t.Fatalf("stream request: %v", err)
	}
	defer streamResp.Body.Close()

	// Walk snapshots until we see one carrying our Turn block.
	// SSE may emit several intervening snapshots (run.queued,
	// run.awaiting_approval, etc.) before reaching ours; the
	// stream handler tags every payload with its event_type, so
	// we filter on that.
	var sawTurn bool
	for event := range readSSEEvents(t, streamResp.Body) {
		if event.Event != "snapshot" {
			continue
		}
		var payload TaskRunStreamEventResponse
		if err := json.Unmarshal([]byte(event.Data), &payload); err != nil {
			t.Fatalf("unmarshal snapshot: %v", err)
		}
		if payload.Data.EventType != "agent.turn.completed" {
			continue
		}
		// This is the snapshot we drove. Three assertions:
		//
		//   a) Turn is populated (the decoder did its job)
		//   b) Turn fields match what we POSTed (no key rename
		//      regression in decodeTurnCostFromEventData)
		//   c) Run.ID is also set (proves the overlay was merged
		//      AFTER buildTaskRunStreamState rebuilt full state —
		//      not a Turn-only payload that lost the rest of the
		//      run context)
		if payload.Data.Turn == nil {
			t.Fatal("snapshot.Turn is nil; overlay was not populated on agent.turn.completed snapshot")
		}
		if got := payload.Data.Turn.CostMicrosUSD; got != 4242 {
			t.Errorf("Turn.CostMicrosUSD = %d, want 4242", got)
		}
		if got := payload.Data.Turn.TaskCumulativeMicrosUSD; got != 12345 {
			t.Errorf("Turn.TaskCumulativeMicrosUSD = %d, want 12345", got)
		}
		if got := payload.Data.Turn.StepID; got != "step-injected" {
			t.Errorf("Turn.StepID = %q, want step-injected", got)
		}
		if got := payload.Data.Turn.Turn; got != 2 {
			t.Errorf("Turn.Turn = %d, want 2", got)
		}
		if payload.Data.Run.ID != started.Data.ID {
			t.Errorf("Run.ID = %q, want %q (overlay should merge AFTER full state rebuild, not replace it)", payload.Data.Run.ID, started.Data.ID)
		}
		sawTurn = true
		break
	}
	cancel()

	if !sawTurn {
		t.Fatal("never observed an agent.turn.completed snapshot with a populated Turn block")
	}
}

func TestTaskRunStreamResumeWithAfterSequence(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := newTestHTTPHandlerForProviders(logger, nil, config.Config{})
	server := httptest.NewServer(handler)
	defer server.Close()

	createResp := postJSONToURL(t, server.URL+"/v1/tasks", `{"title":"Resume stream","prompt":"Create resumable stream task."}`)
	var created TaskResponse
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	createResp.Body.Close()

	started := mustRequestJSON[TaskRunResponse](newAPITestClient(t, handler), http.MethodPost, "/v1/tasks/"+created.Data.ID+"/start", "")
	waitForRunStatus(t, handler, created.Data.ID, started.Data.ID, "completed")

	events := mustRequestJSON[TaskRunEventsResponse](newAPITestClient(t, handler), http.MethodGet, "/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/events", "")
	if len(events.Data) == 0 {
		t.Fatal("events = 0, want at least one")
	}
	afterSequence := events.Data[len(events.Data)-1].Sequence

	streamReq, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/v1/tasks/%s/runs/%s/stream?after_sequence=%d", server.URL, created.Data.ID, started.Data.ID, afterSequence), nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	streamCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	streamReq = streamReq.WithContext(streamCtx)

	streamResp, err := http.DefaultClient.Do(streamReq)
	if err != nil {
		t.Fatalf("stream request error = %v", err)
	}
	defer streamResp.Body.Close()

	var sawDone bool
	for event := range readSSEEvents(t, streamResp.Body) {
		if event.Event != "snapshot" && event.Event != "done" {
			continue
		}
		var payload TaskRunStreamEventResponse
		if err := json.Unmarshal([]byte(event.Data), &payload); err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}
		if payload.Data.Sequence <= int(afterSequence) {
			t.Fatalf("sequence = %d, want > %d", payload.Data.Sequence, afterSequence)
		}
		if event.Event == "done" {
			sawDone = true
			if !payload.Data.Terminal {
				t.Fatal("done payload terminal = false, want true")
			}
			break
		}
	}
	if !sawDone {
		t.Fatal("did not observe done event after stream resume")
	}
}

func TestTaskRunStreamResumeWithLastEventID(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := newTestHTTPHandlerForProviders(logger, nil, config.Config{})
	server := httptest.NewServer(handler)
	defer server.Close()

	createResp := postJSONToURL(t, server.URL+"/v1/tasks", `{"title":"Resume stream header","prompt":"Use Last-Event-ID."}`)
	var created TaskResponse
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	createResp.Body.Close()

	started := mustRequestJSON[TaskRunResponse](newAPITestClient(t, handler), http.MethodPost, "/v1/tasks/"+created.Data.ID+"/start", "")
	waitForRunStatus(t, handler, created.Data.ID, started.Data.ID, "completed")

	events := mustRequestJSON[TaskRunEventsResponse](newAPITestClient(t, handler), http.MethodGet, "/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/events", "")
	if len(events.Data) == 0 {
		t.Fatal("events = 0, want at least one")
	}
	last := events.Data[len(events.Data)-1].Sequence

	streamReq, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/v1/tasks/%s/runs/%s/stream", server.URL, created.Data.ID, started.Data.ID), nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	streamReq.Header.Set("Last-Event-ID", strconv.FormatInt(last, 10))
	streamCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	streamReq = streamReq.WithContext(streamCtx)

	streamResp, err := http.DefaultClient.Do(streamReq)
	if err != nil {
		t.Fatalf("stream request error = %v", err)
	}
	defer streamResp.Body.Close()

	for event := range readSSEEvents(t, streamResp.Body) {
		if event.Event != "snapshot" && event.Event != "done" {
			continue
		}
		var payload TaskRunStreamEventResponse
		if err := json.Unmarshal([]byte(event.Data), &payload); err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}
		if payload.Data.Sequence <= int(last) {
			t.Fatalf("sequence = %d, want > %d", payload.Data.Sequence, last)
		}
		if event.Event == "done" {
			return
		}
	}
	t.Fatal("did not observe done event for Last-Event-ID resume")
}

func TestTaskRunEventsAppendAndList(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := newTestHTTPHandlerForProviders(logger, nil, config.Config{})
	tasks := newTaskTestClient(t, handler)

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/v1/tasks", `{"title":"Event run","prompt":"Run with events."}`)
	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/v1/tasks/"+created.Data.ID+"/start", "")
	waitForRunStatus(t, handler, created.Data.ID, started.Data.ID, "completed")

	initial := mustTaskRequestJSON[TaskRunEventsResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/events", "")
	if len(initial.Data) == 0 {
		t.Fatal("events = 0, want at least one event")
	}
	baseSequence := initial.Data[len(initial.Data)-1].Sequence

	appendRecorder := tasks.mustRequest(
		http.MethodPost,
		"/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/events",
		`{"event_type":"external.tool_result","step_id":"step_external","status":"ok","note":"client injected event","data":{"tool":"lint","result":"ok"}}`,
	)
	var appended map[string]any
	if err := json.NewDecoder(appendRecorder.Body).Decode(&appended); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	after := mustTaskRequestJSON[TaskRunEventsResponse](tasks, http.MethodGet, fmt.Sprintf("/v1/tasks/%s/runs/%s/events?after_sequence=%d", created.Data.ID, started.Data.ID, baseSequence), "")
	foundExternal := false
	for _, event := range after.Data {
		if event.Sequence <= baseSequence {
			t.Fatalf("event sequence = %d, want > %d", event.Sequence, baseSequence)
		}
		if event.EventType == "external.tool_result" {
			foundExternal = true
		}
	}
	if !foundExternal {
		t.Fatal("missing appended external.tool_result event")
	}
}

func TestTaskRunRetryCreatesNewAttempt(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := newTestHTTPHandlerForProviders(logger, nil, config.Config{})
	tasks := newTaskTestClient(t, handler)

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/v1/tasks", `{"title":"Retry run","prompt":"Trigger retry flow."}`)
	first := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/v1/tasks/"+created.Data.ID+"/start", "")
	waitForRunStatus(t, handler, created.Data.ID, first.Data.ID, "completed")

	retried := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/v1/tasks/"+created.Data.ID+"/runs/"+first.Data.ID+"/retry", `{}`)
	if retried.Data.ID == first.Data.ID {
		t.Fatal("retry run id matches original run id")
	}
	waitForRunStatus(t, handler, created.Data.ID, retried.Data.ID, "completed")

	runs := mustTaskRequestJSON[TaskRunsResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID+"/runs", "")
	if len(runs.Data) < 2 {
		t.Fatalf("runs = %d, want at least 2", len(runs.Data))
	}
}

func TestTaskStartReturnsConflictWhileRunActive(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := config.Config{Server: config.ServerConfig{TaskApprovalPolicies: []string{"shell_exec"}}}
	handler := newTestHTTPHandlerForProviders(logger, nil, cfg)
	tasks := newTaskTestClient(t, handler)

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/v1/tasks", `{"title":"Active start","prompt":"Leave this run awaiting approval.","execution_kind":"shell","shell_command":"printf 'active\n'","working_directory":".","timeout_ms":1000}`)
	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/v1/tasks/"+created.Data.ID+"/start", "")
	if started.Data.Status != "awaiting_approval" {
		t.Fatalf("run status = %q, want awaiting_approval", started.Data.Status)
	}

	rec := tasks.mustRequestStatus(http.StatusConflict, http.MethodPost, "/v1/tasks/"+created.Data.ID+"/start", "")
	if !strings.Contains(rec.Body.String(), "active run") {
		t.Fatalf("error body = %s, want mention of active run", rec.Body.String())
	}
}

func TestTaskRunRetryReturnsConflictForActiveRun(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	// shell_exec policy is required so the shell run lands in awaiting_approval
	// rather than queued — the conflict check only fires for non-terminal runs.
	cfg := config.Config{Server: config.ServerConfig{TaskApprovalPolicies: []string{"shell_exec"}}}
	handler := newTestHTTPHandlerForProviders(logger, nil, cfg)
	tasks := newTaskTestClient(t, handler)

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/v1/tasks", `{"title":"Active retry","prompt":"Leave this run awaiting approval.","execution_kind":"shell","shell_command":"printf 'active\n'","working_directory":".","timeout_ms":1000}`)
	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/v1/tasks/"+created.Data.ID+"/start", "")
	if started.Data.Status != "awaiting_approval" {
		t.Fatalf("run status = %q, want awaiting_approval", started.Data.Status)
	}

	rec := tasks.mustRequestStatus(http.StatusConflict, http.MethodPost, "/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/retry", `{}`)
	if !strings.Contains(rec.Body.String(), "not retryable") {
		t.Fatalf("error body = %s, want mention of not retryable", rec.Body.String())
	}
}

func TestTaskRunResumeReturnsConflictForActiveRun(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	// shell_exec policy is required so the shell run lands in awaiting_approval
	// rather than queued — the conflict check only fires for non-terminal runs.
	cfg := config.Config{Server: config.ServerConfig{TaskApprovalPolicies: []string{"shell_exec"}}}
	handler := newTestHTTPHandlerForProviders(logger, nil, cfg)
	tasks := newTaskTestClient(t, handler)

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/v1/tasks", `{"title":"Active resume","prompt":"Leave this run awaiting approval.","execution_kind":"shell","shell_command":"printf 'active\n'","working_directory":".","timeout_ms":1000}`)
	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/v1/tasks/"+created.Data.ID+"/start", "")
	if started.Data.Status != "awaiting_approval" {
		t.Fatalf("run status = %q, want awaiting_approval", started.Data.Status)
	}

	rec := tasks.mustRequestStatus(http.StatusConflict, http.MethodPost, "/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/resume", `{}`)
	if !strings.Contains(rec.Body.String(), "not resumable") {
		t.Fatalf("error body = %s, want mention of not resumable", rec.Body.String())
	}
}

func TestTaskRunResumeFromCancelledRun(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	// shell_exec policy puts the shell run in awaiting_approval so the test
	// can reject the approval to force a cancelled state before resuming.
	cfg := config.Config{Server: config.ServerConfig{TaskApprovalPolicies: []string{"shell_exec"}}}
	handler := newTestHTTPHandlerForProviders(logger, nil, cfg)
	tasks := newTaskTestClient(t, handler)

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/v1/tasks", `{"title":"Resume shell","prompt":"Resume cancelled shell run.","execution_kind":"shell","shell_command":"printf 'resume'\n","working_directory":".","timeout_ms":1000}`)
	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/v1/tasks/"+created.Data.ID+"/start", "")
	approvals := mustTaskRequestJSON[TaskApprovalsResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID+"/approvals", "")
	tasks.mustRequest(http.MethodPost, "/v1/tasks/"+created.Data.ID+"/approvals/"+approvals.Data[0].ID+"/resolve", `{"decision":"reject","note":"force cancellation for resume test"}`)
	waitForRunStatus(t, handler, created.Data.ID, started.Data.ID, "cancelled")

	resumed := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/resume", `{"reason":"continue after cancellation"}`)
	if resumed.Data.ID == started.Data.ID {
		t.Fatal("resume returned original run id, want new run id")
	}
	if resumed.Data.Status != "awaiting_approval" && resumed.Data.Status != "queued" {
		t.Fatalf("resume status = %q, want awaiting_approval or queued", resumed.Data.Status)
	}
	if started.Data.WorkspacePath != "" && resumed.Data.WorkspacePath != started.Data.WorkspacePath {
		t.Fatalf("resumed workspace path = %q, want %q", resumed.Data.WorkspacePath, started.Data.WorkspacePath)
	}
	events := mustTaskRequestJSON[TaskRunEventsResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID+"/runs/"+resumed.Data.ID+"/events", "")
	foundResumedEvent := false
	for _, event := range events.Data {
		if event.EventType != "run.resumed" {
			continue
		}
		foundResumedEvent = true
		if got, _ := event.Data["resumed_from_run_id"].(string); got != started.Data.ID {
			t.Fatalf("run.resumed resumed_from_run_id = %q, want %q", got, started.Data.ID)
		}
	}
	if !foundResumedEvent {
		t.Fatal("missing run.resumed event for resumed run")
	}
}

func TestTaskRunResume_RaisesCeilingBeforeQueueing(t *testing.T) {
	// "Raise ceiling and resume" affordance: passing budget_micros_usd
	// in the resume body persists the new ceiling on the task BEFORE
	// the resumed run is enqueued. The agent loop's next budget check
	// (priorCost + costSpent vs Task.BudgetMicrosUSD) sees the raised
	// value, so a run that originally hit the ceiling can continue
	// without two roundtrips (PATCH-task + POST-resume) and without
	// a race between them.
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := newTestHTTPHandlerForProviders(logger, nil, config.Config{})
	tasks := newTaskTestClient(t, handler)

	// Use a sandbox-policy-denied file run as a deterministic way
	// to land in `failed` quickly. The ceiling-raise behavior is
	// the same regardless of why the source run failed; we only
	// need a terminal run to resume.
	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/v1/tasks",
		`{"title":"Raise ceiling","prompt":"x","execution_kind":"file","file_operation":"write","file_path":"x.txt","file_content":"hi","working_directory":".","sandbox_read_only":true,"budget_micros_usd":100000}`)
	if created.Data.BudgetMicrosUSD != 100000 {
		t.Fatalf("initial budget = %d, want 100000", created.Data.BudgetMicrosUSD)
	}
	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/v1/tasks/"+created.Data.ID+"/start", "")
	waitForRunStatus(t, handler, created.Data.ID, started.Data.ID, "failed")

	// Resume with a doubled ceiling.
	resumed := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost,
		"/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/resume",
		`{"budget_micros_usd":200000,"reason":"raise ceiling"}`)
	if resumed.Data.ID == started.Data.ID {
		t.Fatal("resume returned original run id, want new run id")
	}

	// Task ceiling must now reflect the raised value.
	got := mustTaskRequestJSON[TaskResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID, "")
	if got.Data.BudgetMicrosUSD != 200000 {
		t.Errorf("task budget after resume = %d, want 200000 (raised)", got.Data.BudgetMicrosUSD)
	}
}

func TestTaskRunResume_RejectsLoweredCeiling(t *testing.T) {
	// Lowering the ceiling on resume is rejected with 400 — silently
	// stranding a run below its already-spent prior cost would be a
	// surprising failure mode.
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := newTestHTTPHandlerForProviders(logger, nil, config.Config{})
	tasks := newTaskTestClient(t, handler)

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/v1/tasks",
		`{"title":"Lower ceiling","prompt":"x","execution_kind":"file","file_operation":"write","file_path":"x.txt","file_content":"hi","working_directory":".","sandbox_read_only":true,"budget_micros_usd":500000}`)
	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/v1/tasks/"+created.Data.ID+"/start", "")
	waitForRunStatus(t, handler, created.Data.ID, started.Data.ID, "failed")

	rec := tasks.mustRequestStatus(http.StatusBadRequest, http.MethodPost,
		"/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/resume",
		`{"budget_micros_usd":100000}`)
	if !strings.Contains(rec.Body.String(), "cannot be lower") {
		t.Errorf("error body should mention 'cannot be lower'; got: %s", rec.Body.String())
	}
}

func TestTaskRunResumeBuildsCheckpointStepContext(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := newTestHTTPHandlerForProviders(logger, nil, config.Config{})
	tasks := newTaskTestClient(t, handler)

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/v1/tasks", `{"title":"Resume checkpoint","prompt":"Resume failed file run.","execution_kind":"file","file_operation":"write","file_path":"checkpoint.txt","file_content":"hello","working_directory":".","sandbox_read_only":true}`)
	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/v1/tasks/"+created.Data.ID+"/start", "")
	waitForRunStatus(t, handler, created.Data.ID, started.Data.ID, "failed")

	resumed := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/resume", `{"reason":"continue from latest checkpoint"}`)
	waitForRunStatus(t, handler, created.Data.ID, resumed.Data.ID, "failed")

	steps := mustTaskRequestJSON[TaskStepsResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID+"/runs/"+resumed.Data.ID+"/steps", "")
	if len(steps.Data) == 0 {
		t.Fatal("resumed run steps = 0, want at least one step")
	}
	step := steps.Data[0]
	if step.Index <= 1 {
		t.Fatalf("resumed step index = %d, want > 1", step.Index)
	}
	if got, _ := step.Input["resume_from_run_id"].(string); got != started.Data.ID {
		t.Fatalf("resume_from_run_id = %q, want %q", got, started.Data.ID)
	}
}

func TestTaskCreateRepoLocalProfileAppliesDefaults(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := newTestHTTPHandlerForProviders(logger, nil, config.Config{})
	tasks := newTaskTestClient(t, handler)

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/v1/tasks", `{"title":"Repo local profile","prompt":"Profile defaults","execution_profile":"repo_local"}`)
	if created.Data.ExecutionKind != "agent_loop" {
		t.Fatalf("execution_kind = %q, want agent_loop", created.Data.ExecutionKind)
	}
	if created.Data.WorkspaceMode != "persistent" {
		t.Fatalf("workspace_mode = %q, want persistent", created.Data.WorkspaceMode)
	}
	if created.Data.TimeoutMS != 120000 {
		t.Fatalf("timeout_ms = %d, want 120000", created.Data.TimeoutMS)
	}
}

func TestTaskStartAgentLoopWithoutLLM_FailsInRunNotAtQueue(t *testing.T) {
	// agent_loop is unconditionally available — there used to be a
	// feature flag (GATEWAY_TASK_ENABLE_AGENT_EXECUTOR) gating it at
	// the queue boundary, but it was removed once the runtime
	// stabilized. Without an LLM configured the run still fails, but
	// it does so inside the run with an actionable error step the
	// operator can see in the timeline, not at the queue boundary
	// where the run never even appears.
	//
	// A model must be specified (or a default configured) — the start
	// preflight rejects agent_loop tasks with no model before creating
	// the run. Here we supply a model so the preflight passes and the
	// test exercises the "LLM client not wired" failure path.
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := newTestHTTPHandlerForProviders(logger, nil, config.Config{})
	tasks := newTaskTestClient(t, handler)

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/v1/tasks", `{"title":"Agent loop no LLM","prompt":"No LLM wired","execution_kind":"agent_loop","requested_model":"gpt-4o-mini"}`)
	// Start succeeds — model is set so the preflight passes.
	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/v1/tasks/"+created.Data.ID+"/start", "")
	if started.Data.Status != "queued" {
		t.Fatalf("started run status = %q, want queued", started.Data.Status)
	}
	// Run terminates failed; the failure surfaces in last_error so
	// operators see why directly on the run record.
	finished := waitForRunStatusWithClient(tasks, created.Data.ID, started.Data.ID, "failed")
	if !strings.Contains(finished.Data.LastError, "LLM") {
		t.Fatalf("LastError = %q, want mention of LLM (no client configured)", finished.Data.LastError)
	}
}

func TestTaskStartAgentLoopWithoutModel_FailsAtStart(t *testing.T) {
	// When no model is configured (neither task.RequestedModel nor
	// GATEWAY_DEFAULT_MODEL), starting an agent_loop run should return
	// 422 immediately — no run is created, no tokens are spent.
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	// config.Config{} has DefaultModel == "" — no default configured.
	handler := newTestHTTPHandlerForProviders(logger, nil, config.Config{})
	tasks := newTaskTestClient(t, handler)

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/v1/tasks", `{"title":"Agent loop no model","prompt":"No model","execution_kind":"agent_loop"}`)

	rec := tasks.mustRequestStatus(http.StatusUnprocessableEntity, http.MethodPost, "/v1/tasks/"+created.Data.ID+"/start", "")
	if !strings.Contains(rec.Body.String(), "model_not_configured") {
		t.Fatalf("body = %s, want model_not_configured error code", rec.Body.String())
	}
}

func TestTaskStartFileExecutesFileStep(t *testing.T) {
	// File-execution tasks (execution_kind=file) write a file and
	// produce a "file" step. agent_loop used to also run this path
	// deterministically as a fallback, but agent_loop now means
	// "LLM-driven" — without an LLM it fails fast. Tests that need a
	// no-LLM path use the explicit kinds, like this one.
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := newTestHTTPHandlerForProviders(logger, nil, config.Config{})
	tasks := newTaskTestClient(t, handler)

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/v1/tasks", `{"title":"File write","prompt":"Execute file step","execution_kind":"file","execution_profile":"repo_local","file_operation":"write","file_path":"agent-loop.txt","file_content":"hello"}`)
	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/v1/tasks/"+created.Data.ID+"/start", "")
	waitForRunStatus(t, handler, created.Data.ID, started.Data.ID, "completed")

	steps := mustTaskRequestJSON[TaskStepsResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/steps", "")
	if len(steps.Data) < 1 {
		t.Fatalf("steps = %d, want >= 1", len(steps.Data))
	}
	foundFileStep := false
	for _, step := range steps.Data {
		if step.Kind == "file" {
			foundFileStep = true
			break
		}
	}
	if !foundFileStep {
		t.Fatal("missing file step")
	}
}

func TestTaskRunArtifactFetchByID(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := newTestHTTPHandlerForProviders(logger, nil, config.Config{})
	tasks := newTaskTestClient(t, handler)

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/v1/tasks", `{"title":"Artifact fetch","prompt":"Produce an artifact."}`)
	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/v1/tasks/"+created.Data.ID+"/start", "")
	waitForRunStatus(t, handler, created.Data.ID, started.Data.ID, "completed")

	runArtifacts := mustTaskRequestJSON[TaskArtifactsResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/artifacts", "")
	if len(runArtifacts.Data) == 0 {
		t.Fatal("run artifacts = 0, want at least one")
	}
	artifactID := runArtifacts.Data[0].ID
	fetched := mustTaskRequestJSON[TaskArtifactResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/artifacts/"+artifactID, "")
	if fetched.Data.ID != artifactID {
		t.Fatalf("artifact id = %q, want %q", fetched.Data.ID, artifactID)
	}
}

type apiTestClient struct {
	t       *testing.T
	handler http.Handler
	headers map[string]string
}

func newAPITestClient(t *testing.T, handler http.Handler) apiTestClient {
	t.Helper()
	return apiTestClient{t: t, handler: handler}
}

func (c apiTestClient) withBearerToken(token string) apiTestClient {
	c.t.Helper()
	if strings.TrimSpace(token) == "" {
		return c
	}
	if c.headers == nil {
		c.headers = make(map[string]string, 1)
	}
	c.headers["Authorization"] = "Bearer " + token
	return c
}

func (c apiTestClient) withAPIKey(token string) apiTestClient {
	c.t.Helper()
	if strings.TrimSpace(token) == "" {
		return c
	}
	if c.headers == nil {
		c.headers = make(map[string]string, 1)
	}
	c.headers["x-api-key"] = token
	return c
}

func (c apiTestClient) mustRequest(method, path, body string) *httptest.ResponseRecorder {
	return c.mustRequestStatus(http.StatusOK, method, path, body)
}

func (c apiTestClient) mustRequestStatus(status int, method, path, body string) *httptest.ResponseRecorder {
	c.t.Helper()
	recorder := performRequestWithHeaders(c.t, c.handler, method, path, body, c.headers)
	if recorder.Code != status {
		c.t.Fatalf("%s %s status = %d, want %d, body=%s", method, path, recorder.Code, status, recorder.Body.String())
	}
	return recorder
}

func mustRequestJSON[T any](client apiTestClient, method, path, body string) T {
	client.t.Helper()
	return decodeRecorder[T](client.t, client.mustRequest(method, path, body))
}

func mustRequestJSONStatus[T any](client apiTestClient, status int, method, path, body string) T {
	client.t.Helper()
	return decodeRecorder[T](client.t, client.mustRequestStatus(status, method, path, body))
}

type taskTestClient = apiTestClient

// asyncWaitTimeout caps how long the test waits for an orchestrator-driven
// task to reach a desired state. The task itself completes in well under
// a second under any real load, but on GitHub's 2-core runners the
// combination of -race overhead and t.Parallel() across many tests in
// this package can starve the orchestrator goroutine for several seconds.
// 60s gives generous headroom while still failing fast on real
// regressions — a stuck task hits the same fatal whether the cap is 10s
// or 60s, the higher number just stops blaming the CPU scheduler.
const asyncWaitTimeout = 60 * time.Second

func newTaskTestClient(t *testing.T, handler http.Handler) taskTestClient {
	t.Helper()
	return newAPITestClient(t, handler)
}

func mustTaskRequestJSON[T any](client taskTestClient, method, path, body string) T {
	client.t.Helper()
	return mustRequestJSON[T](client, method, path, body)
}

func waitForRunStatus(t *testing.T, handler http.Handler, taskID, runID string, statuses ...string) TaskRunResponse {
	t.Helper()
	deadline := time.Now().Add(asyncWaitTimeout)
	for time.Now().Before(deadline) {
		recorder := performRequest(t, handler, http.MethodGet, "/v1/tasks/"+taskID+"/runs/"+runID, "")
		if recorder.Code == http.StatusOK {
			run, ok := tryDecodeRecorder[TaskRunResponse](recorder)
			if ok && containsStatus(run.Data.Status, statuses...) {
				return run
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for run %s to reach one of %v", runID, statuses)
	return TaskRunResponse{}
}

func waitForRunStatusWithClient(client apiTestClient, taskID, runID string, statuses ...string) TaskRunResponse {
	client.t.Helper()
	deadline := time.Now().Add(asyncWaitTimeout)
	for time.Now().Before(deadline) {
		recorder := client.mustRequest(http.MethodGet, "/v1/tasks/"+taskID+"/runs/"+runID, "")
		run, ok := tryDecodeRecorder[TaskRunResponse](recorder)
		if ok && containsStatus(run.Data.Status, statuses...) {
			return run
		}
		time.Sleep(20 * time.Millisecond)
	}
	client.t.Fatalf("timed out waiting for run %s to reach one of %v", runID, statuses)
	return TaskRunResponse{}
}

func waitForTaskStatus(t *testing.T, handler http.Handler, taskID string, statuses ...string) TaskResponse {
	t.Helper()
	deadline := time.Now().Add(asyncWaitTimeout)
	for time.Now().Before(deadline) {
		recorder := performRequest(t, handler, http.MethodGet, "/v1/tasks/"+taskID, "")
		if recorder.Code == http.StatusOK {
			task, ok := tryDecodeRecorder[TaskResponse](recorder)
			if ok && containsStatus(task.Data.Status, statuses...) {
				return task
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for task %s to reach one of %v", taskID, statuses)
	return TaskResponse{}
}

func waitForRunArtifactsContaining(t *testing.T, handler http.Handler, taskID, runID, kind, contains string) TaskArtifactsResponse {
	t.Helper()
	deadline := time.Now().Add(asyncWaitTimeout)
	for time.Now().Before(deadline) {
		recorder := performRequest(t, handler, http.MethodGet, "/v1/tasks/"+taskID+"/runs/"+runID+"/artifacts", "")
		if recorder.Code == http.StatusOK {
			artifacts, ok := tryDecodeRecorder[TaskArtifactsResponse](recorder)
			if ok {
				for _, artifact := range artifacts.Data {
					if artifact.Kind == kind && strings.Contains(artifact.ContentText, contains) {
						return artifacts
					}
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s artifact to contain %q", kind, contains)
	return TaskArtifactsResponse{}
}

func waitForRunStepStatus(t *testing.T, handler http.Handler, taskID, runID string, status string) TaskStepsResponse {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		recorder := performRequest(t, handler, http.MethodGet, "/v1/tasks/"+taskID+"/runs/"+runID+"/steps", "")
		if recorder.Code == http.StatusOK {
			steps, ok := tryDecodeRecorder[TaskStepsResponse](recorder)
			if ok && len(steps.Data) > 0 && steps.Data[0].Status == status {
				return steps
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for run %s step to reach status %q", runID, status)
	return TaskStepsResponse{}
}

func waitForRunEvent(t *testing.T, handler http.Handler, taskID, runID, eventType string) TaskRunEventsResponse {
	t.Helper()
	deadline := time.Now().Add(asyncWaitTimeout)
	for time.Now().Before(deadline) {
		recorder := performRequest(t, handler, http.MethodGet, "/v1/tasks/"+taskID+"/runs/"+runID+"/events", "")
		if recorder.Code == http.StatusOK {
			events, ok := tryDecodeRecorder[TaskRunEventsResponse](recorder)
			if ok && countTaskRunEvents(events.Data, eventType) > 0 {
				return events
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for run %s event %q", runID, eventType)
	return TaskRunEventsResponse{}
}

func assertEventOrder(t *testing.T, events []TaskRunEventItem, want []string) {
	t.Helper()
	cursor := 0
	for _, event := range events {
		if cursor >= len(want) {
			return
		}
		if event.EventType == want[cursor] {
			cursor++
		}
	}
	if cursor != len(want) {
		got := make([]string, 0, len(events))
		for _, event := range events {
			got = append(got, event.EventType)
		}
		t.Fatalf("event order missing %v; got %v", want[cursor:], got)
	}
}

func assertEventSequencesIncrease(t *testing.T, events []TaskRunEventItem) {
	t.Helper()
	var previous int64
	for _, event := range events {
		if event.Sequence <= previous {
			t.Fatalf("event sequence %d after %d for %s; want strictly increasing", event.Sequence, previous, event.EventType)
		}
		previous = event.Sequence
	}
}

func countTaskRunEvents(events []TaskRunEventItem, eventType string) int {
	count := 0
	for _, event := range events {
		if event.EventType == eventType {
			count++
		}
	}
	return count
}

func containsStatus(status string, statuses ...string) bool {
	for _, candidate := range statuses {
		if status == candidate {
			return true
		}
	}
	return false
}

type sseEvent struct {
	ID    string
	Event string
	Data  string
}

func readSSEEvents(t *testing.T, body io.Reader) <-chan sseEvent {
	t.Helper()
	events := make(chan sseEvent)
	go func() {
		defer close(events)
		scanner := bufio.NewScanner(body)
		scanner.Buffer(make([]byte, 0, 4096), 1024*1024)
		var current sseEvent
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				if current.Event != "" || current.Data != "" {
					events <- current
					current = sseEvent{}
				}
				continue
			}
			if strings.HasPrefix(line, "event: ") {
				current.Event = strings.TrimPrefix(line, "event: ")
				continue
			}
			if strings.HasPrefix(line, "id: ") {
				current.ID = strings.TrimPrefix(line, "id: ")
				continue
			}
			if strings.HasPrefix(line, "data: ") {
				if current.Data != "" {
					current.Data += "\n"
				}
				current.Data += strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	return events
}

func postJSONToURL(t *testing.T, url, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("Post(%s) error = %v", url, err)
	}
	return resp
}

func newTestHTTPHandler(logger *slog.Logger, provider providers.Provider) http.Handler {
	return newTestHTTPHandlerWithConfig(logger, provider, config.Config{})
}

func newTestHTTPHandlerWithConfig(logger *slog.Logger, provider providers.Provider, cfg config.Config) http.Handler {
	return newTestHTTPHandlerForProviders(logger, []providers.Provider{provider}, cfg)
}

func newTestHTTPHandlerForProviders(logger *slog.Logger, items []providers.Provider, cfg config.Config) http.Handler {
	return newTestHTTPHandlerWithControlPlane(logger, items, cfg, nil)
}

func newTestHTTPHandlerWithControlPlane(logger *slog.Logger, items []providers.Provider, cfg config.Config, cpStore controlplane.Store) http.Handler {
	registry := providers.NewRegistry(items...)
	providerHistoryStore := providers.NewMemoryHealthHistoryStore()
	healthTracker := providers.NewMemoryHealthTrackerWithHistory(
		cfg.Provider.HealthThreshold,
		cfg.Provider.HealthCooldown,
		cfg.Provider.HealthLatencyDegradedThreshold,
		providerHistoryStore,
	)
	providerCatalog := catalog.NewRegistryCatalog(registry, healthTracker)
	budgetStore := governor.NewMemoryBudgetStore()
	governorCfg := mergeGovernorDefaults(cfg.Governor)
	routerCfg := cfg.Router
	if routerCfg.DefaultModel == "" && len(items) > 0 {
		routerCfg.DefaultModel = items[0].DefaultModel()
	}
	routerEngine := router.NewRuleRouter(routerCfg.DefaultModel, providerCatalog)
	retentionCfg := cfg.Retention
	if retentionCfg.TraceSnapshots.MaxCount == 0 {
		retentionCfg.TraceSnapshots = config.RetentionPolicy{MaxAge: time.Hour, MaxCount: 2000}
	}
	if retentionCfg.BudgetEvents.MaxCount == 0 {
		retentionCfg.BudgetEvents = config.RetentionPolicy{MaxAge: 30 * 24 * time.Hour, MaxCount: 200}
	}
	if retentionCfg.AuditEvents.MaxCount == 0 {
		retentionCfg.AuditEvents = config.RetentionPolicy{MaxAge: 30 * 24 * time.Hour, MaxCount: 500}
	}
	if retentionCfg.ExactCache.MaxCount == 0 {
		retentionCfg.ExactCache = config.RetentionPolicy{MaxAge: 24 * time.Hour, MaxCount: 10000}
	}
	if retentionCfg.SemanticCache.MaxCount == 0 {
		retentionCfg.SemanticCache = config.RetentionPolicy{MaxAge: 7 * 24 * time.Hour, MaxCount: 10000}
	}
	retentionManager := retention.NewManager(
		logger,
		retentionCfg,
		profiler.NewInMemoryTracer(nil),
		profiler.NewInMemoryTracer(nil),
		budgetStore,
		nil,
		nil,
		nil,
		providerHistoryStore,
		nil,
		retention.NewMemoryHistoryStore(),
	)
	pricebookCfg := pricebookConfigForTests(items)
	if cfg.Pricebook.UnknownModelPolicy != "" || len(cfg.Pricebook.Entries) > 0 {
		pricebookCfg = cfg.Pricebook
	}
	service := gateway.NewService(gateway.Dependencies{
		Logger:   logger,
		Cache:    cache.NewMemoryStore(time.Minute),
		Semantic: buildTestSemanticStore(cfg),
		SemanticOptions: gateway.SemanticOptions{
			Enabled:       cfg.Cache.Semantic.Enabled,
			MinSimilarity: cfg.Cache.Semantic.MinSimilarity,
			MaxTextChars:  cfg.Cache.Semantic.MaxTextChars,
		},
		Resilience: gateway.ResilienceOptions{
			MaxAttempts:     cfg.Provider.MaxAttempts,
			RetryBackoff:    cfg.Provider.RetryBackoff,
			FailoverEnabled: cfg.Provider.FailoverEnabled,
		},
		Router:          routerEngine,
		Catalog:         providerCatalog,
		Governor:        governor.NewStaticGovernor(governorCfg, budgetStore, budgetStore),
		Providers:       registry,
		HealthTracker:   healthTracker,
		ProviderHistory: providerHistoryStore,
		Pricebook: billing.NewStaticPricebook(config.ProvidersConfig{
			OpenAICompatible: providerConfigsForTests(items),
		}, pricebookCfg),
		Tracer:       profiler.NewInMemoryTracer(nil),
		Metrics:      telemetry.NewMetrics(),
		Retention:    retentionManager,
		ChatSessions: chatstate.NewMemoryStore(),
	})

	cfg.Governor = governorCfg
	handler := NewHandler(cfg, logger, service, cpStore, nil, nil)
	return NewServer(logger, handler)
}

func providerConfigsForTests(items []providers.Provider) []config.OpenAICompatibleProviderConfig {
	configs := make([]config.OpenAICompatibleProviderConfig, 0, len(items))
	for _, provider := range items {
		configs = append(configs, config.OpenAICompatibleProviderConfig{
			Name:         provider.Name(),
			Kind:         string(provider.Kind()),
			DefaultModel: provider.DefaultModel(),
		})
	}
	return configs
}

func pricebookConfigForTests(items []providers.Provider) config.PricebookConfig {
	entries := make([]config.ModelPriceConfig, 0, len(items)+4)
	for _, provider := range items {
		if provider.Kind() != providers.KindCloud || provider.DefaultModel() == "" {
			continue
		}
		entries = append(entries, config.ModelPriceConfig{
			Provider:                             provider.Name(),
			Model:                                provider.DefaultModel(),
			InputMicrosUSDPerMillionTokens:       150_000,
			OutputMicrosUSDPerMillionTokens:      600_000,
			CachedInputMicrosUSDPerMillionTokens: 75_000,
		})
	}
	entries = append(entries, defaultPricebookForTests().Entries...)
	return config.PricebookConfig{Entries: entries}
}

func defaultPricebookForTests() config.PricebookConfig {
	return config.PricebookConfig{
		Entries: []config.ModelPriceConfig{
			{Provider: "openai", Model: "gpt-4o-mini", InputMicrosUSDPerMillionTokens: 150_000, OutputMicrosUSDPerMillionTokens: 600_000, CachedInputMicrosUSDPerMillionTokens: 75_000},
			{Provider: "openai", Model: "gpt-4.1-mini", InputMicrosUSDPerMillionTokens: 400_000, OutputMicrosUSDPerMillionTokens: 1_600_000, CachedInputMicrosUSDPerMillionTokens: 100_000},
			{Provider: "openai", Model: "omni-moderation", InputMicrosUSDPerMillionTokens: 0, OutputMicrosUSDPerMillionTokens: 0, CachedInputMicrosUSDPerMillionTokens: 0},
			{Provider: "openai", Model: "omni-moderation-latest", InputMicrosUSDPerMillionTokens: 0, OutputMicrosUSDPerMillionTokens: 0, CachedInputMicrosUSDPerMillionTokens: 0},
		},
	}
}

func buildTestSemanticStore(cfg config.Config) cache.SemanticStore {
	if !cfg.Cache.Semantic.Enabled {
		return cache.NoopSemanticStore{}
	}
	return cache.NewMemorySemanticStore(
		cfg.Cache.Semantic.DefaultTTL,
		cfg.Cache.Semantic.MaxEntries,
		cache.LocalSimpleEmbedder{MaxTextChars: cfg.Cache.Semantic.MaxTextChars},
	)
}

func newBudgetTestHandler(logger *slog.Logger, governorCfg config.GovernorConfig, budgetStore governor.BudgetStore) http.Handler {
	return newBudgetTestHandlerWithConfig(logger, config.Config{Governor: governorCfg}, budgetStore, nil)
}

func newBudgetTestHandlerWithConfig(logger *slog.Logger, cfg config.Config, budgetStore governor.BudgetStore, cpStore controlplane.Store) http.Handler {
	provider := &fakeProvider{name: "openai"}
	registry := providers.NewRegistry(provider)
	providerCatalog := catalog.NewRegistryCatalog(registry, nil)
	governorCfg := mergeGovernorDefaults(cfg.Governor)
	service := gateway.NewService(gateway.Dependencies{
		Logger:    logger,
		Cache:     cache.NewMemoryStore(time.Minute),
		Router:    router.NewRuleRouter("gpt-4o-mini", providerCatalog),
		Catalog:   providerCatalog,
		Governor:  governor.NewStaticGovernor(governorCfg, budgetStore, budgetStore),
		Providers: registry,
		Pricebook: billing.NewStaticPricebook(config.ProvidersConfig{
			OpenAICompatible: []config.OpenAICompatibleProviderConfig{
				{Name: provider.Name(), Kind: string(provider.Kind())},
			},
		}, pricebookConfigForTests([]providers.Provider{provider})),
		Tracer:       profiler.NewInMemoryTracer(nil),
		Metrics:      telemetry.NewMetrics(),
		ChatSessions: chatstate.NewMemoryStore(),
	})

	handler := NewHandler(cfg, logger, service, cpStore, nil, nil)
	return NewServer(logger, handler)
}

func mergeGovernorDefaults(cfg config.GovernorConfig) config.GovernorConfig {
	if cfg.MaxPromptTokens == 0 {
		cfg.MaxPromptTokens = 64_000
	}
	if cfg.BudgetBackend == "" {
		cfg.BudgetBackend = "memory"
	}
	if cfg.BudgetKey == "" {
		cfg.BudgetKey = "global"
	}
	if cfg.BudgetScope == "" {
		cfg.BudgetScope = "global"
	}
	return cfg
}

func performJSONRequest(t *testing.T, handler http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	return performRequest(t, handler, http.MethodPost, "/v1/chat/completions", body)
}

func performRequest(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return performRequestWithHeaders(t, handler, method, path, body, nil)
}

func performRequestWithHeaders(t *testing.T, handler http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var requestBody io.Reader
	if body != "" {
		requestBody = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, requestBody)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func decodeRecorder[T any](t *testing.T, recorder *httptest.ResponseRecorder) T {
	t.Helper()
	payload, ok := tryDecodeRecorder[T](recorder)
	if !ok {
		t.Fatalf("Decode() error for body %q", recorder.Body.String())
	}
	return payload
}

func tryDecodeRecorder[T any](recorder *httptest.ResponseRecorder) (T, bool) {
	var payload T
	if err := json.NewDecoder(bytes.NewReader(recorder.Body.Bytes())).Decode(&payload); err != nil {
		return payload, false
	}
	return payload, true
}

type fakeProvider struct {
	mu           sync.Mutex
	name         string
	defaultModel string
	response     *types.ChatResponse
	err          error
	errSequence  []error
	calls        int
	// lastRequest is the most recent ChatRequest the provider was asked to
	// handle. Tests that need to assert what the gateway forwarded
	// (system-prompt prepending, model rewrites, etc.) read this. The
	// stored value is a copy — the slice headers are independent so test
	// code mutating it can't race with concurrent Chat calls.
	lastRequest  types.ChatRequest
	capabilities providers.Capabilities
	capsErr      error
	baseURL      string
	credential   providers.CredentialState
}

func (p *fakeProvider) Name() string {
	if p.name == "" {
		return "openai"
	}
	return p.name
}

func (p *fakeProvider) Kind() providers.Kind {
	if p.capabilities.Kind != "" {
		return p.capabilities.Kind
	}
	return providers.KindCloud
}

func (p *fakeProvider) DefaultModel() string {
	if p.defaultModel != "" {
		return p.defaultModel
	}
	if p.capabilities.DefaultModel != "" {
		return p.capabilities.DefaultModel
	}
	return "gpt-4o-mini"
}

func (p *fakeProvider) BaseURL() string {
	if p.baseURL != "" {
		return p.baseURL
	}
	return "https://example.invalid"
}

func (p *fakeProvider) CredentialState() providers.CredentialState {
	if p.credential != "" {
		return p.credential
	}
	if p.Kind() == providers.KindLocal {
		return providers.CredentialStateNotRequired
	}
	return providers.CredentialStateConfigured
}

func (p *fakeProvider) Capabilities(_ context.Context) (providers.Capabilities, error) {
	if p.capsErr != nil {
		return p.capabilities, p.capsErr
	}
	if p.capabilities.Name != "" || len(p.capabilities.Models) > 0 || p.capabilities.DefaultModel != "" {
		return p.capabilities, nil
	}
	return providers.Capabilities{
		Name:         p.Name(),
		Kind:         p.Kind(),
		DefaultModel: p.DefaultModel(),
		Models:       []string{"gpt-4o-mini", "gpt-4o-mini-2024-07-18"},
	}, nil
}

func (p *fakeProvider) Chat(_ context.Context, req types.ChatRequest) (*types.ChatResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.calls++
	// Defensive-copy the messages slice so a later test mutation can't
	// race with another Chat call appending to the same backing array.
	p.lastRequest = req
	p.lastRequest.Messages = append([]types.Message(nil), req.Messages...)
	if len(p.errSequence) > 0 {
		err := p.errSequence[0]
		p.errSequence = p.errSequence[1:]
		if err != nil {
			return nil, err
		}
	}
	if p.err != nil {
		return nil, p.err
	}

	cloned := *p.response
	cloned.Choices = append([]types.ChatChoice(nil), p.response.Choices...)
	return &cloned, nil
}

// LastRequest returns a snapshot of the most recently received chat
// request. Safe to call from a different goroutine than Chat.
func (p *fakeProvider) LastRequest() types.ChatRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.lastRequest
	out.Messages = append([]types.Message(nil), p.lastRequest.Messages...)
	return out
}

func (p *fakeProvider) Supports(model string) bool {
	if p.capabilities.DefaultModel == model {
		return true
	}
	for _, candidate := range p.capabilities.Models {
		if candidate == model {
			return true
		}
	}
	if p.defaultModel == model {
		return true
	}
	if strings.HasPrefix(model, "gpt-") && p.Kind() == providers.KindCloud {
		return true
	}
	if strings.HasPrefix(model, "llama") && p.Kind() == providers.KindLocal {
		return true
	}
	return false
}

func (p *fakeProvider) CallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func strPtr(s string) *string { return &s }

func TestRateLimitHeadersSetOnSuccess(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{
		name: "openai",
		response: &types.ChatResponse{
			ID:    "chatcmpl-rl1",
			Model: "gpt-4o-mini",
			Choices: []types.ChatChoice{
				{Message: types.Message{Role: "assistant", Content: "hi"}, FinishReason: "stop"},
			},
			Usage: types.Usage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
		},
	}
	handler := newTestHTTPHandlerWithConfig(logger, provider, config.Config{
		Server: config.ServerConfig{
			RateLimit: config.RateLimitConfig{
				Enabled:           true,
				RequestsPerMinute: 10,
				BurstSize:         10,
			},
		},
	})

	rec := performJSONRequest(t, handler, `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if h := rec.Header().Get("X-RateLimit-Limit"); h != "10" {
		t.Errorf("X-RateLimit-Limit = %q, want \"10\"", h)
	}
	remaining, err := strconv.Atoi(rec.Header().Get("X-RateLimit-Remaining"))
	if err != nil {
		t.Fatalf("X-RateLimit-Remaining not numeric: %v", err)
	}
	if remaining < 0 || remaining > 9 {
		t.Errorf("X-RateLimit-Remaining = %d, want 0-9", remaining)
	}
	if rec.Header().Get("X-RateLimit-Reset") == "" {
		t.Error("X-RateLimit-Reset header missing")
	}
}

func TestRateLimitReturns429WhenExhausted(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{
		name: "openai",
		response: &types.ChatResponse{
			ID:    "chatcmpl-rl2",
			Model: "gpt-4o-mini",
			Choices: []types.ChatChoice{
				{Message: types.Message{Role: "assistant", Content: "ok"}, FinishReason: "stop"},
			},
			Usage: types.Usage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7},
		},
	}
	handler := newTestHTTPHandlerWithConfig(logger, provider, config.Config{
		Server: config.ServerConfig{
			RateLimit: config.RateLimitConfig{
				Enabled:           true,
				RequestsPerMinute: 2,
				BurstSize:         2,
			},
		},
	})

	body := `{"model":"gpt-4o-mini","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`
	// Drain the bucket.
	for i := 0; i < 2; i++ {
		rec := performJSONRequest(t, handler, body)
		if rec.Code != http.StatusOK {
			t.Fatalf("call %d: status = %d, want 200", i+1, rec.Code)
		}
	}
	// Third call should be rate-limited.
	rec := performJSONRequest(t, handler, body)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", rec.Code)
	}
}

func TestRateLimitDisabledByDefault(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{
		name: "openai",
		response: &types.ChatResponse{
			ID:    "chatcmpl-rl3",
			Model: "gpt-4o-mini",
			Choices: []types.ChatChoice{
				{Message: types.Message{Role: "assistant", Content: "ok"}, FinishReason: "stop"},
			},
			Usage: types.Usage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7},
		},
	}
	// No rate limit config — RateLimit.Enabled defaults to false.
	handler := newTestHTTPHandlerWithConfig(logger, provider, config.Config{})

	body := `{"model":"gpt-4o-mini","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`
	for i := 0; i < 5; i++ {
		rec := performJSONRequest(t, handler, body)
		if rec.Code != http.StatusOK {
			t.Fatalf("call %d: status = %d, want 200 (rate limit should be disabled)", i+1, rec.Code)
		}
	}
}

func TestHandleChatReturns402OnBudgetExceeded(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{name: "openai", defaultModel: "gpt-4o-mini"}

	// 1 µUSD budget — any real request estimate will exceed it immediately.
	handler := newTestHTTPHandlerWithConfig(logger, provider, config.Config{
		Governor: config.GovernorConfig{
			MaxTotalBudgetMicros:    1,
			MaxPromptTokens:         100_000,
			BudgetWarningThresholds: []int{50, 80, 95},
			BudgetHistoryLimit:      20,
		},
	})

	// max_tokens drives the cost estimate; without it the estimate is ~0 µUSD and
	// wouldn't exceed the 1 µUSD budget.
	rec := performJSONRequest(t, handler, `{"model":"gpt-4o-mini","max_tokens":1024,"messages":[{"role":"user","content":"hello"}]}`)
	if rec.Code != http.StatusPaymentRequired {
		t.Errorf("status = %d, want 402\nbody: %s", rec.Code, rec.Body.String())
	}
}

func TestCheckRateLimitSetsHeaders(t *testing.T) {
	t.Parallel()
	h := &Handler{
		rateLimiter: ratelimit.NewStore(5, 5),
	}
	w := httptest.NewRecorder()
	ok := h.checkRateLimit(w, "test-key")
	if !ok {
		t.Fatal("checkRateLimit returned false on first call")
	}
	if w.Header().Get("X-RateLimit-Limit") != "5" {
		t.Errorf("X-RateLimit-Limit = %q, want \"5\"", w.Header().Get("X-RateLimit-Limit"))
	}
	rem, err := strconv.Atoi(w.Header().Get("X-RateLimit-Remaining"))
	if err != nil {
		t.Fatalf("X-RateLimit-Remaining not numeric: %s", w.Header().Get("X-RateLimit-Remaining"))
	}
	if rem != 4 {
		t.Errorf("X-RateLimit-Remaining = %d, want 4", rem)
	}
}

func TestCheckRateLimitReturns429WhenExhausted(t *testing.T) {
	t.Parallel()
	h := &Handler{rateLimiter: ratelimit.NewStore(1, 60)}
	// Consume the single token.
	w1 := httptest.NewRecorder()
	h.checkRateLimit(w1, "k")
	// Second call should be rejected.
	w2 := httptest.NewRecorder()
	ok := h.checkRateLimit(w2, "k")
	if ok {
		t.Fatal("checkRateLimit should return false when bucket is empty")
	}
	if w2.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", w2.Code)
	}
}

func TestCheckRateLimitNilLimiterAlwaysAllows(t *testing.T) {
	t.Parallel()
	h := &Handler{rateLimiter: nil}
	w := httptest.NewRecorder()
	if !h.checkRateLimit(w, "anything") {
		t.Error("nil rateLimiter should always allow")
	}
}
