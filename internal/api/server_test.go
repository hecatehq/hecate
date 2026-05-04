package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

	"github.com/hecate/agent-runtime/internal/agentadapters"
	"github.com/hecate/agent-runtime/internal/agentchat"
	"github.com/hecate/agent-runtime/internal/billing"
	"github.com/hecate/agent-runtime/internal/catalog"
	"github.com/hecate/agent-runtime/internal/chatstate"
	"github.com/hecate/agent-runtime/internal/config"
	"github.com/hecate/agent-runtime/internal/controlplane"
	"github.com/hecate/agent-runtime/internal/eventprotocol"
	"github.com/hecate/agent-runtime/internal/gateway"
	"github.com/hecate/agent-runtime/internal/governor"
	"github.com/hecate/agent-runtime/internal/mcp"
	mcpclient "github.com/hecate/agent-runtime/internal/mcp/client"
	"github.com/hecate/agent-runtime/internal/profiler"
	"github.com/hecate/agent-runtime/internal/providers"
	"github.com/hecate/agent-runtime/internal/ratelimit"
	"github.com/hecate/agent-runtime/internal/retention"
	"github.com/hecate/agent-runtime/internal/router"
	"github.com/hecate/agent-runtime/internal/telemetry"
	"github.com/hecate/agent-runtime/pkg/types"
)

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
		Server: config.ServerConfig{},
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
		Messages: []OpenAIChatMessage{
			{Role: "user", Content: OpenAIMessageContent{Text: "hello"}},
		},
	}

	got, err := normalizeChatRequest(req, "req-123")
	if err != nil {
		t.Fatalf("normalizeChatRequest() error = %v", err)
	}
	if got.Scope.ProviderHint != "ollama" {
		t.Fatalf("provider hint = %q, want ollama", got.Scope.ProviderHint)
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
	foundPerplexity := false
	foundXAI := false
	foundOllama := false
	for _, item := range response.Data {
		if item.ID == "anthropic" && item.Protocol == "anthropic" && item.EnvSnippet != "" {
			foundAnthropic = true
		}
		if item.ID == "xai" && item.Protocol == "openai" && strings.Contains(item.EnvSnippet, "PROVIDER_XAI_API_KEY") {
			foundXAI = true
		}
		if item.ID == "perplexity" && item.Protocol == "openai" && strings.Contains(item.EnvSnippet, "PROVIDER_PERPLEXITY_API_KEY") {
			foundPerplexity = true
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
	if !foundPerplexity {
		t.Fatalf("missing perplexity preset: %#v", response.Data)
	}
	if !foundOllama {
		t.Fatalf("missing ollama preset: %#v", response.Data)
	}
}

func TestAgentAdaptersReturnsBuiltIns(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := NewServer(logger, NewHandler(config.Config{}, logger, nil, nil, nil, nil))
	client := newAPITestClient(t, handler)
	response := mustRequestJSON[AgentAdapterResponse](client, http.MethodGet, "/v1/agent-adapters", "")
	if response.Object != "agent_adapters" {
		t.Fatalf("object = %q, want agent_adapters", response.Object)
	}
	if len(response.Data) != 3 {
		t.Fatalf("adapter count = %d, want 3", len(response.Data))
	}

	foundCodex := false
	foundClaude := false
	foundCursor := false
	for _, item := range response.Data {
		if item.ID == "codex" && item.Kind == "acp" && item.Command == "codex-acp" && item.Managed && item.ManagedPackage == "@zed-industries/codex-acp" && item.CostMode == "external" {
			foundCodex = true
		}
		if item.ID == "claude_code" && item.Kind == "acp" && item.Command == "claude-agent-acp" && item.Managed && item.ManagedPackage == "@agentclientprotocol/claude-agent-acp" && item.CostMode == "external" {
			foundClaude = true
		}
		if item.ID == "cursor_agent" && item.Kind == "acp" && item.Command == "cursor-agent" && item.CostMode == "external" {
			foundCursor = true
		}
		if item.Status == "" {
			t.Fatalf("adapter %q missing status: %#v", item.ID, item)
		}
	}
	if !foundCodex {
		t.Fatalf("missing codex adapter: %#v", response.Data)
	}
	if !foundClaude {
		t.Fatalf("missing claude_code adapter: %#v", response.Data)
	}
	if !foundCursor {
		t.Fatalf("missing cursor_agent adapter: %#v", response.Data)
	}
}

func TestAgentChatRunsExternalAdapter(t *testing.T) {
	dir := t.TempDir()
	if _, err := exec.LookPath("git"); err == nil {
		_ = exec.Command("git", "-C", dir, "init", "-b", "main").Run()
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithControlPlane(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatRunner(&fakeAgentChatRunner{
		output:          "agent saw: hello from hecate\n",
		diffStat:        "README.md | 1 +",
		diff:            "diff --git a/README.md b/README.md\n",
		nativeSessionID: "native_codex_1",
		sessionStarted:  true,
		usage: agentadapters.Usage{
			ContextSize:          200_000,
			ContextUsed:          42_000,
			ReportedCostAmount:   "0.1234",
			ReportedCostCurrency: "USD",
		},
	})
	handler := NewServer(logger, apiHandler)
	client := newAPITestClient(t, handler)
	created := mustRequestJSON[AgentChatSessionResponse](client, http.MethodPost, "/v1/agent-chat/sessions", fmt.Sprintf(`{"adapter_id":"codex","workspace":%q,"title":"Codex test"}`, dir))
	if created.Data.AdapterID != "codex" {
		t.Fatalf("adapter_id = %q, want codex", created.Data.AdapterID)
	}
	if got := created.Data.WorkspaceBranch; got != "" && got != "main" {
		t.Fatalf("workspace_branch = %q, want empty or main", got)
	}

	recorder := client.mustRequest(http.MethodPost, "/v1/agent-chat/sessions/"+created.Data.ID+"/messages", `{"content":"hello from hecate"}`)
	if recorder.Header().Get("X-Trace-Id") == "" {
		t.Fatal("X-Trace-Id = empty, want agent chat trace id")
	}
	if recorder.Header().Get("X-Span-Id") == "" {
		t.Fatal("X-Span-Id = empty, want agent chat span id")
	}
	updated := decodeRecorder[AgentChatSessionResponse](t, recorder)
	if len(updated.Data.Messages) != 2 {
		t.Fatalf("message count = %d, want 2: %#v", len(updated.Data.Messages), updated.Data.Messages)
	}
	assistant := updated.Data.Messages[1]
	if assistant.Role != "assistant" || assistant.AdapterID != "codex" || assistant.Status != "completed" {
		t.Fatalf("assistant message = %#v", assistant)
	}
	if assistant.DriverKind != "acp" || assistant.NativeSessionID != "native_codex_1" {
		t.Fatalf("assistant ACP metadata = kind %q native %q", assistant.DriverKind, assistant.NativeSessionID)
	}
	if !strings.Contains(assistant.Content, "hello from hecate") {
		t.Fatalf("assistant content = %q, want prompt echoed", assistant.Content)
	}
	if assistant.CostMode != "external" {
		t.Fatalf("cost_mode = %q, want external", assistant.CostMode)
	}
	if assistant.Usage == nil || assistant.Usage.ContextSize != 200_000 || assistant.Usage.ContextUsed != 42_000 {
		t.Fatalf("usage = %#v, want context 42000/200000", assistant.Usage)
	}
	if assistant.Usage.ReportedCostAmount != "0.1234" || assistant.Usage.ReportedCostCurrency != "USD" {
		t.Fatalf("reported cost = %#v, want 0.1234 USD", assistant.Usage)
	}
	if !strings.Contains(assistant.RawOutput, "hello from hecate") {
		t.Fatalf("raw_output = %q, want prompt echoed", assistant.RawOutput)
	}
	if assistant.RequestID == "" || assistant.TraceID == "" || assistant.SpanID == "" {
		t.Fatalf("assistant trace metadata missing: %#v", assistant)
	}
	if assistant.TraceID != recorder.Header().Get("X-Trace-Id") {
		t.Fatalf("assistant trace_id = %q, want response header %q", assistant.TraceID, recorder.Header().Get("X-Trace-Id"))
	}
	tracePayload := mustRequestJSON[TraceResponse](client, http.MethodGet, "/v1/traces?request_id="+assistant.RequestID, "")
	var agentSpan *TraceSpanRecord
	events := make(map[string]TraceEventRecord)
	for _, span := range tracePayload.Data.Spans {
		if span.Name == telemetry.SpanAgentChatRun {
			span := span
			agentSpan = &span
		}
		for _, event := range span.Events {
			events[event.Name] = event
		}
	}
	if agentSpan == nil {
		t.Fatalf("agent chat trace missing %s span: spans=%#v", telemetry.SpanAgentChatRun, tracePayload.Data.Spans)
	}
	if got := agentSpan.Attributes[telemetry.AttrHecatePhase]; got != "agent_chat" {
		t.Fatalf("agent span phase = %#v, want agent_chat", got)
	}
	wantSpanAttrs := map[string]any{
		telemetry.AttrHecateAgentChatSessionID:   created.Data.ID,
		telemetry.AttrHecateAgentChatMessageID:   assistant.ID,
		telemetry.AttrHecateRunID:                assistant.RunID,
		telemetry.AttrHecateExecutionKind:        "agent_chat",
		telemetry.AttrHecateAgentAdapterID:       "codex",
		telemetry.AttrHecateAgentAdapterName:     "Codex",
		telemetry.AttrHecateAgentAdapterCommand:  "codex-acp",
		telemetry.AttrHecateAgentDriverKind:      agentadapters.DriverKindACP,
		telemetry.AttrHecateAgentNativeSessionID: "native_codex_1",
		telemetry.AttrHecateWorkspacePath:        assistant.Workspace,
		telemetry.AttrHecateRunStatus:            "completed",
		telemetry.AttrHecateResult:               telemetry.ResultSuccess,
		telemetry.AttrHecateAgentDiffCaptured:    true,
	}
	for key, want := range wantSpanAttrs {
		if got := agentSpan.Attributes[key]; got != want {
			t.Fatalf("agent span attr %s = %#v, want %#v", key, got, want)
		}
	}
	for _, key := range []string{
		telemetry.AttrHecateRunDurationMS,
		telemetry.AttrHecateAgentOutputBytes,
		telemetry.AttrHecateAgentRawOutputBytes,
	} {
		if _, ok := agentSpan.Attributes[key]; !ok {
			t.Fatalf("agent span attr %s missing: %#v", key, agentSpan.Attributes)
		}
	}
	for _, eventName := range []string{
		telemetry.EventAgentChatRunStarted,
		telemetry.EventAgentChatOutputStarted,
		telemetry.EventAgentChatFilesChanged,
		telemetry.EventAgentChatRunFinished,
	} {
		event, ok := events[eventName]
		if !ok {
			t.Fatalf("agent chat trace missing event %s: %#v", eventName, events)
		}
		if missing := telemetry.ValidateEventAttrs(event.Name, event.Attributes); len(missing) != 0 {
			t.Fatalf("agent chat event %s missing attrs %v: %#v", event.Name, missing, event.Attributes)
		}
	}
	if len(assistant.Activities) == 0 {
		t.Fatalf("activities missing: %#v", assistant)
	}
	if !agentChatActivitiesContain(assistant.Activities, "started") {
		t.Fatalf("started activity missing for new native session: %#v", assistant.Activities)
	}
	if !agentChatActivitiesContain(assistant.Activities, "files_changed") {
		t.Fatalf("files_changed activity missing: %#v", assistant.Activities)
	}
	if assistant.RunID == "" || assistant.StartedAt == "" || assistant.CompletedAt == "" || assistant.DurationMS < 0 {
		t.Fatalf("assistant runtime metadata missing: %#v", assistant)
	}
	if got := updated.Data.WorkspaceBranch; got != "" && got != "main" {
		t.Fatalf("workspace_branch = %q, want empty or main", got)
	}
	if updated.Data.DriverKind != "acp" || updated.Data.NativeSessionID != "native_codex_1" {
		t.Fatalf("session ACP metadata = kind %q native %q", updated.Data.DriverKind, updated.Data.NativeSessionID)
	}
}

func TestAgentChatOmitsStartedActivityWhenNativeSessionReused(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithControlPlane(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatRunner(&fakeAgentChatRunner{
		output:          "agent saw: reused session\n",
		nativeSessionID: "native_codex_1",
		sessionStarted:  false,
	})
	handler := NewServer(logger, apiHandler)
	client := newAPITestClient(t, handler)
	created := mustRequestJSON[AgentChatSessionResponse](client, http.MethodPost, "/v1/agent-chat/sessions", fmt.Sprintf(`{"adapter_id":"codex","workspace":%q}`, dir))
	updated := mustRequestJSON[AgentChatSessionResponse](client, http.MethodPost, "/v1/agent-chat/sessions/"+created.Data.ID+"/messages", `{"content":"hello"}`)
	assistant := updated.Data.Messages[1]
	if agentChatActivitiesContain(assistant.Activities, "started") {
		t.Fatalf("started activity present for reused native session: %#v", assistant.Activities)
	}
}

func TestAgentChatMergesAdapterActivityUpdates(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithControlPlane(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatRunner(&fakeAgentChatRunner{
		output: "done",
		activities: []agentadapters.Activity{
			{ID: "tool:call_1", Type: "tool_call", Status: "running", Kind: "execute", Title: "git status", Detail: "README.md"},
			{ID: "tool:call_1", Type: "tool_call", Status: "completed", Kind: "execute", Title: "git status", Detail: "README.md"},
			{ID: "plan:0:Inspect", Type: "plan", Status: "completed", Kind: "high", Title: "Inspect"},
		},
	})
	client := newAPITestClient(t, NewServer(logger, apiHandler))

	created := mustRequestJSON[AgentChatSessionResponse](client, http.MethodPost, "/v1/agent-chat/sessions", fmt.Sprintf(`{"adapter_id":"codex","workspace":%q}`, dir))
	updated := mustRequestJSON[AgentChatSessionResponse](client, http.MethodPost, "/v1/agent-chat/sessions/"+created.Data.ID+"/messages", `{"content":"hello"}`)
	assistant := updated.Data.Messages[len(updated.Data.Messages)-1]

	var toolCount int
	var sawPlan bool
	for _, activity := range assistant.Activities {
		if activity.ID == "tool:call_1" {
			toolCount++
			if activity.Status != "completed" || activity.Kind != "execute" || activity.Detail != "README.md" {
				t.Fatalf("tool activity = %#v", activity)
			}
		}
		if activity.ID == "plan:0:Inspect" && activity.Type == "plan" && activity.Status == "completed" && activity.Kind == "high" {
			sawPlan = true
		}
	}
	if toolCount != 1 {
		t.Fatalf("tool activity count = %d, want 1 in %#v", toolCount, assistant.Activities)
	}
	if !sawPlan {
		t.Fatalf("plan activity missing in %#v", assistant.Activities)
	}
}

func TestAgentChatFinalOutputReplacesStreamedNarration(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithControlPlane(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatRunner(&fakeAgentChatRunner{
		chunks:      []string{"I will inspect the diff first."},
		finalOutput: "There is no current diff.",
	})
	client := newAPITestClient(t, NewServer(logger, apiHandler))

	created := mustRequestJSON[AgentChatSessionResponse](client, http.MethodPost, "/v1/agent-chat/sessions", fmt.Sprintf(`{"adapter_id":"codex","workspace":%q}`, dir))
	updated := mustRequestJSON[AgentChatSessionResponse](client, http.MethodPost, "/v1/agent-chat/sessions/"+created.Data.ID+"/messages", `{"content":"show diff"}`)
	assistant := updated.Data.Messages[len(updated.Data.Messages)-1]
	if assistant.Content != "There is no current diff." {
		t.Fatalf("assistant content = %q, want final output replacing streamed narration", assistant.Content)
	}
}

func TestAgentChatPassesPersistedNativeSessionForResume(t *testing.T) {
	dir := t.TempDir()
	store := agentchat.NewMemoryStore()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	firstRunner := &fakeAgentChatRunner{
		output:          "first answer",
		nativeSessionID: "native_persisted_1",
		sessionStarted:  true,
	}
	firstHandler := newTestAPIHandlerWithControlPlane(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	firstHandler.SetAgentChatStore(store)
	firstHandler.SetAgentChatRunner(firstRunner)
	firstClient := newAPITestClient(t, NewServer(logger, firstHandler))
	created := mustRequestJSON[AgentChatSessionResponse](firstClient, http.MethodPost, "/v1/agent-chat/sessions", fmt.Sprintf(`{"adapter_id":"codex","workspace":%q}`, dir))
	_ = mustRequestJSON[AgentChatSessionResponse](firstClient, http.MethodPost, "/v1/agent-chat/sessions/"+created.Data.ID+"/messages", `{"content":"first"}`)

	secondRunner := &fakeAgentChatRunner{
		output:          "second answer",
		nativeSessionID: "native_persisted_1",
		sessionStarted:  true,
		sessionResumed:  true,
	}
	secondHandler := newTestAPIHandlerWithControlPlane(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	secondHandler.SetAgentChatStore(store)
	secondHandler.SetAgentChatRunner(secondRunner)
	secondClient := newAPITestClient(t, NewServer(logger, secondHandler))
	updated := mustRequestJSON[AgentChatSessionResponse](secondClient, http.MethodPost, "/v1/agent-chat/sessions/"+created.Data.ID+"/messages", `{"content":"second"}`)
	if secondRunner.seenPreviousID != "native_persisted_1" {
		t.Fatalf("previous native session id = %q, want native_persisted_1", secondRunner.seenPreviousID)
	}
	assistant := updated.Data.Messages[len(updated.Data.Messages)-1]
	if !agentChatActivitiesContain(assistant.Activities, "resumed") {
		t.Fatalf("resumed activity missing: %#v", assistant.Activities)
	}
	if agentChatActivitiesContain(assistant.Activities, "started") {
		t.Fatalf("started activity present for resumed native session: %#v", assistant.Activities)
	}
}

func TestAgentChatShowsFreshSessionRecoveryActivity(t *testing.T) {
	dir := t.TempDir()
	store := agentchat.NewMemoryStore()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	apiHandler := newTestAPIHandlerWithControlPlane(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatStore(store)
	apiHandler.SetAgentChatRunner(&fakeAgentChatRunner{
		output:          "recovered",
		nativeSessionID: "native_fresh",
		sessionStarted:  true,
		sessionRecovery: "could not restore ACP session native_stale: not found",
	})
	client := newAPITestClient(t, NewServer(logger, apiHandler))

	created := mustRequestJSON[AgentChatSessionResponse](client, http.MethodPost, "/v1/agent-chat/sessions", fmt.Sprintf(`{"adapter_id":"codex","workspace":%q}`, dir))
	if _, err := store.UpdateSession(context.Background(), created.Data.ID, func(item *agentchat.Session) {
		item.NativeSessionID = "native_stale"
	}); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	updated := mustRequestJSON[AgentChatSessionResponse](client, http.MethodPost, "/v1/agent-chat/sessions/"+created.Data.ID+"/messages", `{"content":"second"}`)
	if updated.Data.NativeSessionID != "native_fresh" {
		t.Fatalf("native session = %q, want fresh session", updated.Data.NativeSessionID)
	}
	assistant := updated.Data.Messages[len(updated.Data.Messages)-1]
	if !agentChatActivitiesContain(assistant.Activities, "recovered") {
		t.Fatalf("activities = %+v, want recovered activity", assistant.Activities)
	}
}

func TestAgentChatHumanizesAdapterJSONRPCBillingError(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithControlPlane(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	rawErr := `{"code":-32603,"message":"Internal error: Credit balance is too low","data":{"errorKind":"billing_error"}}`
	apiHandler.SetAgentChatRunner(&fakeAgentChatRunner{
		err: errors.New(rawErr),
	})
	handler := NewServer(logger, apiHandler)
	client := newAPITestClient(t, handler)
	created := mustRequestJSON[AgentChatSessionResponse](client, http.MethodPost, "/v1/agent-chat/sessions", fmt.Sprintf(`{"adapter_id":"claude_code","workspace":%q}`, dir))
	updated := mustRequestJSON[AgentChatSessionResponse](client, http.MethodPost, "/v1/agent-chat/sessions/"+created.Data.ID+"/messages", `{"content":"hello"}`)
	assistant := updated.Data.Messages[1]
	if assistant.Status != "failed" {
		t.Fatalf("assistant status = %q, want failed", assistant.Status)
	}
	if !strings.Contains(assistant.Content, "Claude Code usage limit: credit balance is too low") {
		t.Fatalf("assistant content = %q, want humanized Claude usage-limit error", assistant.Content)
	}
	if strings.Contains(assistant.Content, `"code":-32603`) {
		t.Fatalf("assistant content leaked raw JSON-RPC error: %q", assistant.Content)
	}
	if !strings.Contains(assistant.RawOutput, `"errorKind":"billing_error"`) {
		t.Fatalf("raw_output = %q, want raw adapter diagnostics preserved", assistant.RawOutput)
	}
	if assistant.Error != assistant.Content {
		t.Fatalf("assistant error = %q, want content %q", assistant.Error, assistant.Content)
	}
}

func agentChatActivitiesContain(items []AgentChatActivityItem, kind string) bool {
	for _, item := range items {
		if item.Type == kind {
			return true
		}
	}
	return false
}

func TestAgentChatCreateRejectsInvalidWorkspace(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := NewServer(logger, NewHandler(config.Config{}, logger, nil, nil, nil, nil))
	client := newAPITestClient(t, handler)

	rec := client.mustRequestStatus(http.StatusBadRequest, http.MethodPost, "/v1/agent-chat/sessions", `{"adapter_id":"codex","workspace":"/path/that/does/not/exist"}`)
	if !strings.Contains(rec.Body.String(), "not accessible") {
		t.Fatalf("body = %s, want not accessible error", rec.Body.String())
	}
}

func TestAgentChatStoreAttachReconcilesInterruptedRun(t *testing.T) {
	store := agentchat.NewMemoryStore()
	ctx := context.Background()
	created, err := store.Create(ctx, agentchat.Session{
		ID:        "agent_chat_restart",
		Title:     "Restart",
		AdapterID: "codex",
		Workspace: t.TempDir(),
		Status:    "running",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.AppendMessage(ctx, created.ID, agentchat.Message{
		ID:        "msg_running",
		Role:      "assistant",
		Status:    "running",
		Content:   "working",
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	apiHandler.SetAgentChatStore(store)
	handler := NewServer(logger, apiHandler)
	client := newAPITestClient(t, handler)

	got := mustRequestJSON[AgentChatSessionResponse](client, http.MethodGet, "/v1/agent-chat/sessions/"+created.ID, "")
	if got.Data.Status != "cancelled" {
		t.Fatalf("session status = %q, want cancelled", got.Data.Status)
	}
	if got.Data.Messages[0].Status != "cancelled" || got.Data.Messages[0].Error != "interrupted by Hecate restart" {
		t.Fatalf("message = %+v, want interrupted cancellation", got.Data.Messages[0])
	}
	if !agentChatActivitiesContain(got.Data.Messages[0].Activities, "interrupted") {
		t.Fatalf("activities = %+v, want interrupted activity", got.Data.Messages[0].Activities)
	}
}

type fakeAgentChatRunner struct {
	output          string
	finalOutput     string
	chunks          []string
	activities      []agentadapters.Activity
	delay           time.Duration
	waitForCancel   bool
	nativeSessionID string
	sessionStarted  bool
	sessionResumed  bool
	sessionRecovery string
	seenPreviousID  string
	usage           agentadapters.Usage
	diffStat        string
	diff            string
	err             error
	closedSessions  []string
}

func (r *fakeAgentChatRunner) Run(ctx context.Context, req agentadapters.RunRequest) (agentadapters.RunResult, error) {
	started := time.Now().UTC()
	r.seenPreviousID = req.PreviousNativeSessionID
	output := r.output
	for _, activity := range r.activities {
		if req.OnActivity != nil {
			req.OnActivity(activity)
		}
	}
	for _, chunk := range r.chunks {
		select {
		case <-ctx.Done():
			return r.result(req, output, started, 1), context.Canceled
		default:
		}
		if req.OnOutput != nil {
			req.OnOutput(chunk)
		}
		output += chunk
		if r.delay > 0 {
			timer := time.NewTimer(r.delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return r.result(req, output, started, 1), context.Canceled
			case <-timer.C:
			}
		}
	}
	if r.waitForCancel {
		if req.OnOutput != nil {
			req.OnOutput("started\n")
		}
		output += "started\n"
		<-ctx.Done()
		return r.result(req, output, started, 1), context.Canceled
	}
	if req.OnOutput != nil && r.output != "" {
		req.OnOutput(r.output)
	}
	if r.err != nil {
		return r.result(req, output, started, 1), r.err
	}
	if r.finalOutput != "" {
		output = r.finalOutput
	}
	return r.result(req, output, started, 0), nil
}

func (r *fakeAgentChatRunner) result(req agentadapters.RunRequest, output string, started time.Time, exitCode int) agentadapters.RunResult {
	nativeSessionID := r.nativeSessionID
	if nativeSessionID == "" {
		nativeSessionID = "native_" + req.SessionID
	}
	adapter, _ := agentadapters.BuiltInByID(req.AdapterID)
	return agentadapters.RunResult{
		Adapter:         adapter,
		DriverKind:      agentadapters.DriverKindACP,
		NativeSessionID: nativeSessionID,
		SessionStarted:  r.sessionStarted,
		SessionResumed:  r.sessionResumed,
		SessionRecovery: r.sessionRecovery,
		Output:          output,
		RawOutput:       output,
		ExitCode:        exitCode,
		StartedAt:       started,
		CompletedAt:     time.Now().UTC(),
		DiffStat:        r.diffStat,
		Diff:            r.diff,
		Usage:           r.usage,
	}
}

func (r *fakeAgentChatRunner) CloseSession(_ context.Context, sessionID string) error {
	r.closedSessions = append(r.closedSessions, sessionID)
	return nil
}

func (r *fakeAgentChatRunner) Shutdown(context.Context) error { return nil }

func TestAgentChatStreamsExternalAdapterOutput(t *testing.T) {
	dir := t.TempDir()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithControlPlane(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatRunner(&fakeAgentChatRunner{
		chunks: []string{"first chunk\n", "second chunk\n"},
		delay:  100 * time.Millisecond,
	})
	handler := NewServer(logger, apiHandler)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	created := requestJSONToURL[AgentChatSessionResponse](t, http.MethodPost, server.URL+"/v1/agent-chat/sessions", fmt.Sprintf(`{"adapter_id":"codex","workspace":%q}`, dir))
	streamReq, err := http.NewRequest(http.MethodGet, server.URL+"/v1/agent-chat/sessions/"+created.Data.ID+"/stream", nil)
	if err != nil {
		t.Fatalf("new stream request: %v", err)
	}
	streamResp, err := http.DefaultClient.Do(streamReq)
	if err != nil {
		t.Fatalf("stream request: %v", err)
	}
	defer streamResp.Body.Close()
	if streamResp.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d, want 200", streamResp.StatusCode)
	}
	events := readSSEEvents(t, streamResp.Body)

	done := make(chan AgentChatSessionResponse, 1)
	go func() {
		done <- requestJSONToURL[AgentChatSessionResponse](t, http.MethodPost, server.URL+"/v1/agent-chat/sessions/"+created.Data.ID+"/messages", `{"content":"stream please"}`)
	}()

	seenFirst := false
	seenSecond := false
	timeout := time.After(3 * time.Second)
	for !(seenFirst && seenSecond) {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatalf("stream closed before both chunks")
			}
			if event.Event != "snapshot" && event.Event != "done" {
				continue
			}
			var payload AgentChatSessionResponse
			if err := json.Unmarshal([]byte(event.Data), &payload); err != nil {
				t.Fatalf("decode stream payload: %v", err)
			}
			if len(payload.Data.Messages) == 0 {
				continue
			}
			content := payload.Data.Messages[len(payload.Data.Messages)-1].Content
			seenFirst = seenFirst || strings.Contains(content, "first chunk")
			seenSecond = seenSecond || strings.Contains(content, "second chunk")
		case <-timeout:
			t.Fatalf("timed out waiting for streamed chunks")
		}
	}

	select {
	case updated := <-done:
		if got := updated.Data.Status; got != "completed" {
			t.Fatalf("final status = %q, want completed", got)
		}
		assistant := updated.Data.Messages[len(updated.Data.Messages)-1]
		if !strings.Contains(assistant.RawOutput, "first chunk") || !strings.Contains(assistant.RawOutput, "second chunk") {
			t.Fatalf("raw_output = %q, want both chunks", assistant.RawOutput)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for message POST")
	}
}

func TestAgentChatCancelsExternalAdapter(t *testing.T) {
	dir := t.TempDir()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithControlPlane(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatRunner(&fakeAgentChatRunner{waitForCancel: true})
	handler := NewServer(logger, apiHandler)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	created := requestJSONToURL[AgentChatSessionResponse](t, http.MethodPost, server.URL+"/v1/agent-chat/sessions", fmt.Sprintf(`{"adapter_id":"codex","workspace":%q}`, dir))
	done := make(chan AgentChatSessionResponse, 1)
	go func() {
		done <- requestJSONToURL[AgentChatSessionResponse](t, http.MethodPost, server.URL+"/v1/agent-chat/sessions/"+created.Data.ID+"/messages", `{"content":"please wait"}`)
	}()

	waitForAgentChatStatus(t, server.URL, created.Data.ID, "running")
	cancelResp := postJSONToURL(t, server.URL+"/v1/agent-chat/sessions/"+created.Data.ID+"/cancel", `{}`)
	defer cancelResp.Body.Close()
	if cancelResp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(cancelResp.Body)
		t.Fatalf("cancel status = %d, want 202, body=%s", cancelResp.StatusCode, string(body))
	}

	select {
	case updated := <-done:
		if got := updated.Data.Status; got != "cancelled" {
			t.Fatalf("final status = %q, want cancelled", got)
		}
		assistant := updated.Data.Messages[len(updated.Data.Messages)-1]
		if assistant.Status != "cancelled" {
			t.Fatalf("assistant status = %q, want cancelled", assistant.Status)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for cancelled message POST")
	}
}

func TestAgentChatDeleteCancelsRunBeforeDeletingSession(t *testing.T) {
	dir := t.TempDir()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithControlPlane(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	runner := &fakeAgentChatRunner{waitForCancel: true}
	apiHandler.SetAgentChatRunner(runner)
	handler := NewServer(logger, apiHandler)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	created := requestJSONToURL[AgentChatSessionResponse](t, http.MethodPost, server.URL+"/v1/agent-chat/sessions", fmt.Sprintf(`{"adapter_id":"codex","workspace":%q}`, dir))
	done := make(chan AgentChatSessionResponse, 1)
	go func() {
		done <- requestJSONToURL[AgentChatSessionResponse](t, http.MethodPost, server.URL+"/v1/agent-chat/sessions/"+created.Data.ID+"/messages", `{"content":"please wait"}`)
	}()

	waitForAgentChatStatus(t, server.URL, created.Data.ID, "running")
	req, err := http.NewRequest(http.MethodDelete, server.URL+"/v1/agent-chat/sessions/"+created.Data.ID, nil)
	if err != nil {
		t.Fatalf("new delete request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("delete status = %d, want 204, body=%s", resp.StatusCode, string(body))
	}
	if len(runner.closedSessions) != 1 || runner.closedSessions[0] != created.Data.ID {
		t.Fatalf("closed sessions = %#v, want %q", runner.closedSessions, created.Data.ID)
	}
	select {
	case updated := <-done:
		if got := updated.Data.Status; got != "cancelled" {
			t.Fatalf("post status = %q, want cancelled", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for cancelled message POST")
	}
	getResp, err := http.Get(server.URL + "/v1/agent-chat/sessions/" + created.Data.ID)
	if err != nil {
		t.Fatalf("get deleted session: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted session status = %d, want 404", getResp.StatusCode)
	}
}

func TestAgentChatCloseKeepsHistoryAndClosesNativeSession(t *testing.T) {
	dir := t.TempDir()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithControlPlane(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	runner := &fakeAgentChatRunner{output: "done", nativeSessionID: "native_close_1", sessionStarted: true}
	apiHandler.SetAgentChatRunner(runner)
	client := newAPITestClient(t, NewServer(logger, apiHandler))

	created := mustRequestJSON[AgentChatSessionResponse](client, http.MethodPost, "/v1/agent-chat/sessions", fmt.Sprintf(`{"adapter_id":"codex","workspace":%q}`, dir))
	updated := mustRequestJSON[AgentChatSessionResponse](client, http.MethodPost, "/v1/agent-chat/sessions/"+created.Data.ID+"/messages", `{"content":"hello"}`)
	if len(updated.Data.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(updated.Data.Messages))
	}
	closed := mustRequestJSON[AgentChatSessionResponse](client, http.MethodPost, "/v1/agent-chat/sessions/"+created.Data.ID+"/close", `{}`)
	if len(runner.closedSessions) != 1 || runner.closedSessions[0] != created.Data.ID {
		t.Fatalf("closed sessions = %#v, want %q", runner.closedSessions, created.Data.ID)
	}
	if len(closed.Data.Messages) != 2 {
		t.Fatalf("closed session messages = %d, want preserved history", len(closed.Data.Messages))
	}
	if closed.Data.DriverKind != "" || closed.Data.NativeSessionID != "" {
		t.Fatalf("closed session ACP metadata = kind %q native %q, want cleared", closed.Data.DriverKind, closed.Data.NativeSessionID)
	}
	got := mustRequestJSON[AgentChatSessionResponse](client, http.MethodGet, "/v1/agent-chat/sessions/"+created.Data.ID, "")
	if len(got.Data.Messages) != 2 {
		t.Fatalf("reloaded messages = %d, want preserved history", len(got.Data.Messages))
	}
	if got.Data.DriverKind != "" || got.Data.NativeSessionID != "" {
		t.Fatalf("reloaded closed session ACP metadata = kind %q native %q, want cleared", got.Data.DriverKind, got.Data.NativeSessionID)
	}
}

func TestAgentChatLiveCancelRunAndWaitTimesOutUntilRunDone(t *testing.T) {
	live := newAgentChatLive()
	cancelled := false
	if ok := live.registerRun("session_1", func() { cancelled = true }); !ok {
		t.Fatal("registerRun = false, want true")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	if live.cancelRunAndWait(ctx, "session_1") {
		t.Fatal("cancelRunAndWait = true before run done, want false")
	}
	if !cancelled {
		t.Fatal("cancel callback was not called")
	}

	live.clearRun("session_1")
	if !live.cancelRunAndWait(context.Background(), "session_1") {
		t.Fatal("cancelRunAndWait without active run = false, want true")
	}
}

func TestAgentChatWorkspaceGitBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	dir := t.TempDir()
	if err := exec.Command("git", "-C", dir, "init", "-b", "feature/agent-chat").Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if got := workspaceGitBranch(dir); got != "feature/agent-chat" {
		t.Fatalf("workspaceGitBranch = %q, want feature/agent-chat", got)
	}
}

func TestAgentChatWorkspaceGitBranchReturnsEmptyOutsideGit(t *testing.T) {
	dir := t.TempDir()
	if got := workspaceGitBranch(dir); got != "" {
		t.Fatalf("workspaceGitBranch = %q, want empty", got)
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

func TestMCPCacheStatsUnconfigured(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	// Handler with no MCP client cache wired → configured=false.
	handler := newTestHTTPHandlerForProviders(logger, nil, config.Config{})
	client := newAPITestClient(t, handler)

	res := mustRequestJSON[MCPCacheStatsResponse](client, http.MethodGet, "/admin/mcp/cache", "")
	if res.Object != "mcp_cache_stats" {
		t.Fatalf("object = %q, want mcp_cache_stats", res.Object)
	}
	if res.Data.Configured {
		t.Errorf("configured = true, want false when no cache is wired")
	}
	if res.Data.CheckedAt == "" {
		t.Errorf("checked_at = empty, want timestamp")
	}
	if res.Data.Entries != 0 || res.Data.InUse != 0 || res.Data.Idle != 0 {
		t.Errorf("counters = {entries:%d in_use:%d idle:%d}, want all zero for unconfigured cache",
			res.Data.Entries, res.Data.InUse, res.Data.Idle)
	}
}

func TestMCPCacheStatsConfiguredEmpty(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	// Wire an idle (empty) cache directly — bypasses newTestHTTPHandlerForProviders
	// so we can call SetMCPClientCache before the handler is used.
	c := mcpclient.NewSharedClientCache(time.Minute, mcp.ClientInfo{Name: "test", Version: "0"})
	defer c.Close()

	h := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	h.SetMCPClientCache(c)
	server := NewServer(logger, h)
	client := newAPITestClient(t, server)

	res := mustRequestJSON[MCPCacheStatsResponse](client, http.MethodGet, "/admin/mcp/cache", "")
	if res.Object != "mcp_cache_stats" {
		t.Fatalf("object = %q, want mcp_cache_stats", res.Object)
	}
	if !res.Data.Configured {
		t.Errorf("configured = false, want true when cache is wired")
	}
	if res.Data.Entries != 0 {
		t.Errorf("entries = %d, want 0 for empty cache", res.Data.Entries)
	}
	if res.Data.InUse != 0 {
		t.Errorf("in_use = %d, want 0 for empty cache", res.Data.InUse)
	}
	if res.Data.CheckedAt == "" {
		t.Errorf("checked_at = empty, want timestamp")
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
	if completed.Data.RequestID == "" {
		t.Fatal("completed run request_id = empty")
	}

	events := waitForRunEvent(t, handler, created.Data.ID, started.Data.ID, "run.finished")
	assertEventOrder(t, events.Data, []string{"run.created", "run.queued", "run.started", "run.finished"})
	assertEventSequencesIncrease(t, events.Data)

	for _, event := range events.Data {
		if event.EventID == "" {
			t.Fatalf("event %s event_id is empty", event.Type)
		}
		if event.OccurredAt == "" {
			t.Fatalf("event %s occurred_at is empty", event.Type)
		}
		if event.Type == "run.finished" {
			if status, _ := event.Data["final_status"].(string); status != "completed" {
				t.Fatalf("run.finished final_status payload = %q, want completed", status)
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

	runTrace := mustRequestJSON[TraceResponse](newAPITestClient(t, handler), http.MethodGet, "/v1/traces?request_id="+completed.Data.RequestID, "")
	runTraceEvents := make(map[string]TraceEventRecord)
	for _, span := range runTrace.Data.Spans {
		for _, event := range span.Events {
			runTraceEvents[event.Name] = event
		}
	}
	for _, eventName := range []string{
		telemetry.EventQueueClaimed,
		telemetry.EventOrchestratorRunStarted,
		telemetry.EventOrchestratorStepCompleted,
		telemetry.EventOrchestratorArtifactCreated,
		telemetry.EventOrchestratorRunFinished,
		telemetry.EventOrchestratorTaskFinished,
		telemetry.EventQueueAcked,
	} {
		event, ok := runTraceEvents[eventName]
		if !ok {
			t.Fatalf("run trace missing event %s: %#v", eventName, runTraceEvents)
		}
		if missing := telemetry.ValidateEventAttrs(event.Name, event.Attributes); len(missing) != 0 {
			t.Fatalf("run trace event %s missing attrs %v: %#v", event.Name, missing, event.Attributes)
		}
	}
}

func TestTaskStartShellExecutor(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := config.Config{Server: config.ServerConfig{TaskApprovalPolicies: []string{"shell_exec"}}}
	handler := newTestHTTPHandlerForProviders(logger, nil, cfg)
	tasks := newTaskTestClient(t, handler)

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/v1/tasks", `{"title":"Run shell","prompt":"Run a shell command.","execution_kind":"shell","shell_command":"printf 'hello '; printf 'diagnostic\n' >&2; sleep 0.2; printf 'from shell\n'","working_directory":".","timeout_ms":2000}`)
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
	eventsAfterApproval := mustTaskRequestJSON[TaskRunEventsResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/events", "")
	assertApprovalResolvedEvent(t, eventsAfterApproval.Data, approvals.Data[0].ID, "approved", "looks safe")

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
	foundStderr := false
	for _, artifact := range runArtifacts.Data {
		if artifact.Kind == "stdout" && strings.Contains(artifact.ContentText, "hello from shell") {
			foundStdout = true
		}
		if artifact.Kind == "stderr" && strings.Contains(artifact.ContentText, "diagnostic") {
			foundStderr = true
		}
	}
	if !foundStdout {
		t.Fatal("stdout artifact missing shell output")
	}
	if !foundStderr {
		t.Fatal("stderr artifact missing shell output")
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
	eventsAfterApproval := mustTaskRequestJSON[TaskRunEventsResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/events", "")
	assertApprovalResolvedEvent(t, eventsAfterApproval.Data, approvals.Data[0].ID, "rejected", "not safe")

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

	artifacts := mustTaskRequestJSON[TaskArtifactsResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/artifacts", "")
	if len(artifacts.Data) != 2 {
		t.Fatalf("artifacts = %d, want 2", len(artifacts.Data))
	}
	var patchArtifact TaskArtifactItem
	for _, artifact := range artifacts.Data {
		if artifact.Kind == "patch" {
			patchArtifact = artifact
			break
		}
	}
	if patchArtifact.ID == "" {
		t.Fatalf("patch artifact missing: %#v", artifacts.Data)
	}
	if patchArtifact.MimeType != "text/x-diff" {
		t.Fatalf("patch mime_type = %q, want text/x-diff", patchArtifact.MimeType)
	}
	if !strings.Contains(patchArtifact.ContentText, "+hello file") {
		t.Fatalf("patch content missing written line:\n%s", patchArtifact.ContentText)
	}

	events := waitForRunEvent(t, handler, created.Data.ID, started.Data.ID, "tool.file.patch")
	var patchEvent eventprotocol.Envelope
	for _, event := range events.Data {
		if event.Type == "tool.file.patch" {
			patchEvent = event
			break
		}
	}
	if got := patchEvent.Data["artifact_id"]; got != patchArtifact.ID {
		t.Fatalf("patch event artifact_id = %v, want %s", got, patchArtifact.ID)
	}
	if got := patchEvent.Data["tool_name"]; got != "file" {
		t.Fatalf("patch event tool_name = %v, want file", got)
	}

	patches := mustTaskRequestJSON[TaskPatchesResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/patches", "")
	if len(patches.Data) != 1 {
		t.Fatalf("patches = %d, want 1", len(patches.Data))
	}
	if patches.Data[0].Artifact.ID != patchArtifact.ID {
		t.Fatalf("patch list artifact id = %q, want %q", patches.Data[0].Artifact.ID, patchArtifact.ID)
	}
	if patches.Data[0].Status != "applied" {
		t.Fatalf("patch status = %q, want applied", patches.Data[0].Status)
	}
	fetchedPatch := mustTaskRequestJSON[TaskPatchResponse](tasks, http.MethodGet, "/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/patches/"+patchArtifact.ID, "")
	if !strings.Contains(fetchedPatch.Data.Diff, "+hello file") {
		t.Fatalf("patch detail missing diff:\n%s", fetchedPatch.Data.Diff)
	}
	tasks.mustRequestStatus(http.StatusConflict, http.MethodPost, "/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/patches/"+patchArtifact.ID+"/apply", "")
	reverted := mustTaskRequestJSON[TaskPatchResponse](tasks, http.MethodPost, "/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/patches/"+patchArtifact.ID+"/revert", "")
	if reverted.Data.Status != "reverted" {
		t.Fatalf("reverted patch status = %q, want reverted", reverted.Data.Status)
	}
	revertEvents := waitForRunEvent(t, handler, created.Data.ID, started.Data.ID, "tool.file.reverted")
	if countTaskRunEvents(revertEvents.Data, "tool.file.reverted") != 1 {
		t.Fatalf("tool.file.reverted event missing: %+v", revertEvents.Data)
	}
	if _, err := os.Stat(filepath.Join(started.Data.WorkspacePath, "note.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reverted new-file patch should remove file, stat err=%v", err)
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
	assertEventOrder(t, events.Data, []string{"run.created", "run.queued", "run.started", "run.cancelled"})
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
	var sawActivity bool
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
		for _, activity := range payload.Data.Activity {
			if activity.Type == "tool_call" && activity.Status != "" {
				sawActivity = true
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
	if !sawActivity {
		t.Fatal("did not observe normalized activity items in stream snapshot")
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
	//   1. Runner emits `turn.completed` to the run-event log
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

	// Inject a turn.completed event via the public events
	// endpoint. The endpoint always merges a `snapshot` key into
	// data — but the decoder's turn.completed branch is
	// checked BEFORE the snapshot branch, so the type-specific
	// path wins (which is what we're testing).
	eventBody := `{
		"type": "turn.completed",
		"data": {
			"turn_index": 2,
			"step_id": "step-injected",
			"cost_micros_usd": 4242,
			"run_cumulative_cost_micros_usd": 7777,
			"task_cumulative_cost_micros_usd": 12345,
			"tool_calls": 1
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
		if payload.Data.EventType != "turn.completed" {
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
			t.Fatal("snapshot.Turn is nil; overlay was not populated on turn.completed snapshot")
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
		t.Fatal("never observed a turn.completed snapshot with a populated Turn block")
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
		`{"type":"external.tool_result","step_id":"step_external","status":"ok","note":"client injected event","data":{"tool":"lint","result":"ok"}}`,
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
		if event.Type == "external.tool_result" {
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
		if event.Type != "run.resumed_from_event" {
			continue
		}
		foundResumedEvent = true
		if got, _ := event.Data["from_run_id"].(string); got != started.Data.ID {
			t.Fatalf("run.resumed_from_event from_run_id = %q, want %q", got, started.Data.ID)
		}
	}
	if !foundResumedEvent {
		t.Fatal("missing run.resumed_from_event event for resumed run")
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

func TestTaskCreateCodingAgentProfileAppliesDefaults(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := newTestHTTPHandlerForProviders(logger, nil, config.Config{})
	tasks := newTaskTestClient(t, handler)

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/v1/tasks", `{"title":"Coding profile","prompt":"Make a focused code change.","execution_profile":"coding_agent"}`)
	if created.Data.ExecutionKind != "agent_loop" {
		t.Fatalf("execution_kind = %q, want agent_loop", created.Data.ExecutionKind)
	}
	if created.Data.WorkspaceMode != "persistent" {
		t.Fatalf("workspace_mode = %q, want persistent", created.Data.WorkspaceMode)
	}
	if created.Data.TimeoutMS != 300000 {
		t.Fatalf("timeout_ms = %d, want 300000", created.Data.TimeoutMS)
	}
	if !strings.Contains(created.Data.SystemPrompt, "Prefer file_edit") {
		t.Fatalf("system_prompt missing coding-agent guidance: %q", created.Data.SystemPrompt)
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

func TestPatchContentExtractsBeforeAndAfter(t *testing.T) {
	t.Parallel()

	diff := strings.Join([]string{
		"--- a/main.go",
		"+++ b/main.go",
		"@@ -1,2 +1,3 @@",
		"-package main",
		"-",
		"+package main",
		"+",
		"+func main() {}",
		"",
	}, "\n")

	before, existed, err := patchBeforeContent(diff)
	if err != nil {
		t.Fatalf("patchBeforeContent() error = %v", err)
	}
	if !existed {
		t.Fatal("beforeExisted = false, want true")
	}
	if before != "package main\n\n" {
		t.Fatalf("before = %q", before)
	}
	after, err := patchAfterContent(diff)
	if err != nil {
		t.Fatalf("patchAfterContent() error = %v", err)
	}
	if after != "package main\n\nfunc main() {}\n" {
		t.Fatalf("after = %q", after)
	}
}

func TestVerifyPatchApplyPreconditionRejectsDrift(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(path, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyPatchApplyPrecondition(path, "original\n", true); err == nil {
		t.Fatal("verifyPatchApplyPrecondition() error = nil, want conflict")
	}
	if err := os.WriteFile(path, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyPatchApplyPrecondition(path, "original\n", true); err != nil {
		t.Fatalf("verifyPatchApplyPrecondition() error = %v", err)
	}
	if err := verifyPatchApplyPrecondition(path, "", false); err == nil {
		t.Fatal("verifyPatchApplyPrecondition(new file) error = nil for existing file, want conflict")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := verifyPatchApplyPrecondition(path, "", false); err != nil {
		t.Fatalf("verifyPatchApplyPrecondition(new file) error = %v", err)
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

func assertEventOrder(t *testing.T, events []eventprotocol.Envelope, want []string) {
	t.Helper()
	cursor := 0
	for _, event := range events {
		if cursor >= len(want) {
			return
		}
		if event.Type == want[cursor] {
			cursor++
		}
	}
	if cursor != len(want) {
		got := make([]string, 0, len(events))
		for _, event := range events {
			got = append(got, event.Type)
		}
		t.Fatalf("event order missing %v; got %v", want[cursor:], got)
	}
}

func assertEventSequencesIncrease(t *testing.T, events []eventprotocol.Envelope) {
	t.Helper()
	var previous int64
	for _, event := range events {
		if event.Sequence <= previous {
			t.Fatalf("event sequence %d after %d for %s; want strictly increasing", event.Sequence, previous, event.Type)
		}
		previous = event.Sequence
	}
}

func countTaskRunEvents(events []eventprotocol.Envelope, eventType string) int {
	count := 0
	for _, event := range events {
		if event.Type == eventType {
			count++
		}
	}
	return count
}

func assertApprovalResolvedEvent(t *testing.T, events []eventprotocol.Envelope, approvalID, decision, comment string) {
	t.Helper()
	for _, event := range events {
		if event.Type == "approval.approved" || event.Type == "approval.rejected" {
			t.Fatalf("legacy approval event %q emitted", event.Type)
		}
		if event.Type != "approval.resolved" {
			continue
		}
		if event.Data["approval_id"] != approvalID {
			continue
		}
		if event.Data["decision"] != decision {
			t.Fatalf("approval.resolved decision = %v, want %s", event.Data["decision"], decision)
		}
		if event.Data["comment"] != comment {
			t.Fatalf("approval.resolved comment = %v, want %s", event.Data["comment"], comment)
		}
		if event.Data["scope"] != "once" {
			t.Fatalf("approval.resolved scope = %v, want once", event.Data["scope"])
		}
		if event.Data["by"] != "operator" {
			t.Fatalf("approval.resolved by = %v, want operator", event.Data["by"])
		}
		return
	}
	t.Fatalf("approval.resolved event for %s not found", approvalID)
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

func requestJSONToURL[T any](t *testing.T, method, url, body string) T {
	t.Helper()
	var reqBody io.Reader
	if body != "" {
		reqBody = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		t.Fatalf("NewRequest(%s %s) error = %v", method, url, err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s error = %v", method, url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("%s %s status = %d, want 2xx, body=%s", method, url, resp.StatusCode, string(payload))
	}
	var out T
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode %s %s: %v", method, url, err)
	}
	return out
}

func waitForAgentChatStatus(t *testing.T, baseURL, sessionID, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		payload := requestJSONToURL[AgentChatSessionResponse](t, http.MethodGet, baseURL+"/v1/agent-chat/sessions/"+sessionID, "")
		if payload.Data.Status == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for agent chat status %q", want)
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
	return NewServer(logger, newTestAPIHandlerWithControlPlane(logger, items, cfg, cpStore))
}

func newTestAPIHandlerWithControlPlane(logger *slog.Logger, items []providers.Provider, cfg config.Config, cpStore controlplane.Store) *Handler {
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
	retentionManager := retention.NewManager(
		logger,
		retentionCfg,
		profiler.NewInMemoryTracer(nil),
		profiler.NewInMemoryTracer(nil),
		budgetStore,
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
		Logger: logger,
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
	return handler
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
