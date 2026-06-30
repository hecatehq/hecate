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
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/agentcontrols"
	"github.com/hecatehq/hecate/internal/catalog"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/controlplane"
	"github.com/hecatehq/hecate/internal/eventprotocol"
	"github.com/hecatehq/hecate/internal/gateway"
	"github.com/hecatehq/hecate/internal/governor"
	"github.com/hecatehq/hecate/internal/mcp"
	mcpclient "github.com/hecatehq/hecate/internal/mcp/client"
	"github.com/hecatehq/hecate/internal/modelapp"
	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/internal/ratelimit"
	"github.com/hecatehq/hecate/internal/retention"
	"github.com/hecatehq/hecate/internal/router"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/internal/workspacefs"
	"github.com/hecatehq/hecate/pkg/types"
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

	admin.mustRequest(http.MethodPost, "/hecate/v1/system/retention/run", `{"subsystems":["trace_snapshots"]}`)
	response := mustRequestJSON[RetentionRunsResponse](admin, http.MethodGet, "/hecate/v1/system/retention/runs?limit=5", "")
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
	payload := mustRequestJSON[TraceResponse](client, http.MethodGet, "/hecate/v1/traces?request_id="+chat.Header().Get("X-Request-Id"), "")
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
	if payload.Data.Spans[0].Attributes[telemetry.AttrServiceName] != telemetry.ServiceName {
		t.Fatalf("root span %s = %#v, want %s", telemetry.AttrServiceName, payload.Data.Spans[0].Attributes[telemetry.AttrServiceName], telemetry.ServiceName)
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

func TestServerRejectsLegacyNativePathsButKeepsProviderCompatibleV1(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := newTestHTTPHandler(logger, &fakeProvider{name: "openai"})
	client := newAPITestClient(t, handler)

	client.mustRequestStatus(http.StatusOK, http.MethodGet, "/v1/models", "")

	for _, tc := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/v1/tasks"},
		{method: http.MethodPost, path: "/v1/tasks"},
		{method: http.MethodGet, path: "/v1/agent-chat/sessions"},
		{method: http.MethodGet, path: "/admin/control-plane"},
		{method: http.MethodPost, path: "/admin/control-plane/providers"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			client.mustRequestStatus(http.StatusNotFound, tc.method, tc.path, "")
		})
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
	defaultOnlyProvider := &fakeProvider{
		name: "openrouter",
		capabilities: providers.Capabilities{
			Name:            "openrouter",
			Kind:            providers.KindCloud,
			DefaultModel:    "openrouter-default",
			DiscoverySource: "provider_default",
		},
	}

	registry := providers.NewRegistry(healthyProvider, degradedProvider, missingCredentialProvider, defaultOnlyProvider)
	providerCatalog := catalog.NewRegistryCatalog(registry, nil)
	usageStore := governor.NewMemoryUsageStore()
	service := gateway.NewService(gateway.Dependencies{
		Logger:    logger,
		Router:    router.NewRuleRouter("gpt-4o-mini", providerCatalog),
		Catalog:   providerCatalog,
		Governor:  governor.NewStaticGovernor(config.GovernorConfig{MaxPromptTokens: 64_000}, usageStore, usageStore),
		Providers: registry,
		Tracer:    profiler.NewInMemoryTracer(nil),
	})
	handler := NewServer(logger, NewHandler(config.Config{}, logger, service, nil, nil, nil))
	client := newAPITestClient(t, handler)
	response := mustRequestJSON[ProviderStatusResponse](client, http.MethodGet, "/hecate/v1/providers/status", "")
	if response.Object != "provider_status" {
		t.Fatalf("object = %q, want provider_status", response.Object)
	}
	if len(response.Data) != 4 {
		t.Fatalf("provider count = %d, want 4", len(response.Data))
	}

	foundHealthy := false
	foundDegraded := false
	foundCredentialBlocked := false
	foundDefaultOnly := false
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
			if item.Readiness.Status != "ok" || item.Readiness.Reason != "ready" {
				t.Fatalf("openai readiness = %#v, want ok/ready", item.Readiness)
			}
			assertReadinessSummary(t, item, "ok", "ready", false)
			assertProviderReadinessCheck(t, item, "credentials", "ok", "configured")
			assertProviderReadinessCheck(t, item, "models", "ok", "models_discovered")
			assertProviderReadinessCheck(t, item, "health", "ok", "healthy")
			assertProviderReadinessCheck(t, item, "routing", "ok", "routable")
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
			if item.Readiness.Status != "blocked" || item.Readiness.Reason != "provider_unhealthy" {
				t.Fatalf("ollama readiness = %#v, want blocked/provider_unhealthy", item.Readiness)
			}
			assertReadinessSummary(t, item, "blocked", "provider_unhealthy", true)
			assertProviderReadinessCheck(t, item, "credentials", "ok", "not_required")
			assertProviderReadinessCheck(t, item, "health", "blocked", "provider_unhealthy")
			assertProviderReadinessCheck(t, item, "routing", "blocked", "provider_unhealthy")
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
			if item.Readiness.Status != "blocked" || item.Readiness.Reason != "credential_missing" {
				t.Fatalf("anthropic readiness = %#v, want blocked/credential_missing", item.Readiness)
			}
			assertReadinessSummary(t, item, "blocked", "credential_missing", true)
			assertProviderReadinessCheck(t, item, "credentials", "blocked", "credential_missing")
			assertProviderReadinessCheck(t, item, "routing", "blocked", "credential_missing")
			foundCredentialBlocked = true
		}
		if item.Name == "openrouter" {
			if item.ModelCount != 1 {
				t.Fatalf("openrouter model_count = %d, want 1 default-model fallback", item.ModelCount)
			}
			if !item.RoutingReady {
				t.Fatalf("openrouter routing_ready = false, reason = %q", item.RoutingBlocked)
			}
			if item.Readiness.Status != "warning" || item.Readiness.Reason != "default_model_only" {
				t.Fatalf("openrouter readiness = %#v, want warning/default_model_only", item.Readiness)
			}
			assertReadinessSummary(t, item, "warning", "default_model_only", true)
			assertProviderReadinessCheck(t, item, "models", "warning", "default_model_only")
			assertProviderReadinessCheck(t, item, "routing", "ok", "routable")
			foundDefaultOnly = true
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
	if !foundDefaultOnly {
		t.Fatalf("missing default-only provider entry: %#v", response.Data)
	}
}

func TestProviderStatusReadinessContractCoversRepairReasons(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	disabled := false
	noModelsProvider := &fakeProvider{
		name:      "emptylocal",
		noDefault: true,
		capabilities: providers.Capabilities{
			Name:            "emptylocal",
			Kind:            providers.KindLocal,
			DiscoverySource: "upstream_v1_models",
		},
	}
	selfReferentialProvider := &fakeProvider{
		name:      "loopback",
		noDefault: true,
		baseURL:   "http://127.0.0.1:8765/v1",
		capabilities: providers.Capabilities{
			Name: "loopback",
			Kind: providers.KindLocal,
		},
	}
	disabledProvider := &fakeProvider{
		name:      "disabled",
		noDefault: true,
		enabled:   &disabled,
		capabilities: providers.Capabilities{
			Name: "disabled",
			Kind: providers.KindLocal,
		},
	}

	registry := providers.NewRegistry(noModelsProvider, selfReferentialProvider, disabledProvider)
	providerCatalog := catalog.NewRegistryCatalogWithSelfAddr(registry, nil, "127.0.0.1:8765")
	usageStore := governor.NewMemoryUsageStore()
	service := gateway.NewService(gateway.Dependencies{
		Logger:    logger,
		Router:    router.NewRuleRouter("", providerCatalog),
		Catalog:   providerCatalog,
		Governor:  governor.NewStaticGovernor(config.GovernorConfig{MaxPromptTokens: 64_000}, usageStore, usageStore),
		Providers: registry,
		Tracer:    profiler.NewInMemoryTracer(nil),
	})
	handler := NewServer(logger, NewHandler(config.Config{}, logger, service, nil, nil, nil))
	client := newAPITestClient(t, handler)

	response := mustRequestJSON[ProviderStatusResponse](client, http.MethodGet, "/hecate/v1/providers/status", "")
	emptyLocal := findProviderStatusItem(t, response.Data, "emptylocal")
	assertReadinessSummary(t, emptyLocal, "blocked", "no_models", true)
	assertProviderReadinessCheck(t, emptyLocal, "models", "blocked", "no_models")
	assertProviderReadinessCheck(t, emptyLocal, "routing", "blocked", "no_models")

	loopback := findProviderStatusItem(t, response.Data, "loopback")
	assertReadinessSummary(t, loopback, "blocked", "provider_unhealthy", true)
	assertProviderReadinessCheck(t, loopback, "models", "blocked", "self_referential")
	assertProviderReadinessCheck(t, loopback, "routing", "blocked", "provider_unhealthy")

	disabledItem := findProviderStatusItem(t, response.Data, "disabled")
	assertReadinessSummary(t, disabledItem, "blocked", "provider_disabled", true)
	assertProviderReadinessCheck(t, disabledItem, "models", "blocked", "provider_disabled")
	assertProviderReadinessCheck(t, disabledItem, "routing", "blocked", "provider_disabled")
}

func findProviderStatusItem(t *testing.T, items []ProviderStatusResponseItem, name string) ProviderStatusResponseItem {
	t.Helper()
	for _, item := range items {
		if item.Name == name {
			return item
		}
	}
	t.Fatalf("missing provider status item %q in %#v", name, items)
	return ProviderStatusResponseItem{}
}

func assertReadinessSummary(t *testing.T, item ProviderStatusResponseItem, status, reason string, wantAction bool) {
	t.Helper()
	if item.Readiness.Status != status {
		t.Fatalf("%s readiness status = %q, want %q", item.Name, item.Readiness.Status, status)
	}
	if item.Readiness.Reason != reason {
		t.Fatalf("%s readiness reason = %q, want %q", item.Name, item.Readiness.Reason, reason)
	}
	if item.Readiness.Message == "" {
		t.Fatalf("%s readiness message is empty", item.Name)
	}
	if wantAction && item.Readiness.OperatorAction == "" {
		t.Fatalf("%s readiness operator_action is empty for status %q", item.Name, status)
	}
	if !wantAction && item.Readiness.OperatorAction != "" {
		t.Fatalf("%s readiness operator_action = %q, want empty", item.Name, item.Readiness.OperatorAction)
	}
}

func assertProviderReadinessCheck(t *testing.T, item ProviderStatusResponseItem, name, status, reason string) {
	t.Helper()
	for _, check := range item.ReadinessChecks {
		if check.Name != name {
			continue
		}
		if check.Status != status {
			t.Fatalf("%s readiness check %q status = %q, want %q", item.Name, name, check.Status, status)
		}
		if check.Reason != reason {
			t.Fatalf("%s readiness check %q reason = %q, want %q", item.Name, name, check.Reason, reason)
		}
		if check.Message == "" {
			t.Fatalf("%s readiness check %q message is empty", item.Name, name)
		}
		if check.Status != "ok" && check.OperatorAction == "" {
			t.Fatalf("%s readiness check %q operator_action is empty for status %q", item.Name, name, check.Status)
		}
		return
	}
	t.Fatalf("%s missing readiness check %q in %#v", item.Name, name, item.ReadinessChecks)
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
	usageStore := governor.NewMemoryUsageStore()
	service := gateway.NewService(gateway.Dependencies{
		Logger:    logger,
		Router:    router.NewRuleRouter("gpt-4o-mini", providerCatalog),
		Catalog:   providerCatalog,
		Governor:  governor.NewStaticGovernor(config.GovernorConfig{MaxPromptTokens: 64_000}, usageStore, usageStore),
		Providers: registry,
		Tracer:    profiler.NewInMemoryTracer(nil),
	})
	handler := NewServer(logger, NewHandler(config.Config{}, logger, service, nil, nil, nil))
	client := newAPITestClient(t, handler)
	response := mustRequestJSON[ProviderStatusResponse](client, http.MethodGet, "/hecate/v1/providers/status", "")
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
	assertProviderReadinessCheck(t, item, "health", "blocked", "provider_rate_limited")
	assertProviderReadinessCheck(t, item, "routing", "blocked", "provider_rate_limited")
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
	usageStore := governor.NewMemoryUsageStore()
	service := gateway.NewService(gateway.Dependencies{
		Logger:          logger,
		Router:          router.NewRuleRouter("gpt-4o-mini", providerCatalog),
		Catalog:         providerCatalog,
		Governor:        governor.NewStaticGovernor(config.GovernorConfig{MaxPromptTokens: 64_000}, usageStore, usageStore),
		Providers:       registry,
		HealthTracker:   health,
		ProviderHistory: historyStore,
		Tracer:          profiler.NewInMemoryTracer(nil),
	})
	handler := NewServer(logger, NewHandler(config.Config{Provider: config.ProviderConfig{HistoryLimit: 10}}, logger, service, nil, nil, nil))
	client := newAPITestClient(t, handler)

	response := mustRequestJSON[ProviderHealthHistoryResponse](client, http.MethodGet, "/hecate/v1/providers/history?provider=openai&limit=1", "")
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

	response := mustRequestJSON[ProviderHealthHistoryResponse](client, http.MethodGet, "/hecate/v1/providers/history?limit=20", "")
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
func TestProviderPresetsReturnsCatalog(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := NewServer(logger, NewHandler(config.Config{}, logger, nil, nil, nil, nil))
	client := newAPITestClient(t, handler)
	response := mustRequestJSON[ProviderPresetResponse](client, http.MethodGet, "/hecate/v1/providers/presets", "")
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

func TestProviderPresetsHideLocalProvidersInRemoteRuntimeByDefault(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := NewHandler(config.Config{Server: config.ServerConfig{RemoteRuntimeMode: true}}, logger, nil, nil, nil, nil)
	response := requestProviderPresetsDirect(t, handler)
	if response.Object != "provider_presets" {
		t.Fatalf("object = %q, want provider_presets", response.Object)
	}
	for _, item := range response.Data {
		if item.Kind == "local" {
			t.Fatalf("remote runtime preset list contains local provider: %+v", item)
		}
	}
}

func TestProviderPresetsAllowLocalProvidersInRemoteRuntimeWithOptIn(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := NewHandler(config.Config{Server: config.ServerConfig{
		RemoteRuntimeMode:         true,
		RemoteAllowLocalProviders: true,
	}}, logger, nil, nil, nil, nil)
	response := requestProviderPresetsDirect(t, handler)
	foundOllama := false
	for _, item := range response.Data {
		if item.ID == "ollama" && item.Kind == "local" {
			foundOllama = true
		}
	}
	if !foundOllama {
		t.Fatalf("missing ollama preset with cloud local-provider opt-in: %#v", response.Data)
	}
}

func requestProviderPresetsDirect(t *testing.T, handler *Handler) ProviderPresetResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/hecate/v1/providers/presets", nil)
	handler.HandleProviderPresets(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("provider presets status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var response ProviderPresetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode provider presets: %v", err)
	}
	return response
}

func TestAgentAdaptersReturnsBuiltIns(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := NewServer(logger, NewHandler(config.Config{}, logger, nil, nil, nil, nil))
	client := newAPITestClient(t, handler)
	response := mustRequestJSON[AgentAdapterResponse](client, http.MethodGet, "/hecate/v1/agent-adapters", "")
	if response.Object != "agent_adapters" {
		t.Fatalf("object = %q, want agent_adapters", response.Object)
	}
	if len(response.Data) != 4 {
		t.Fatalf("adapter count = %d, want 4", len(response.Data))
	}

	foundCodex := false
	foundClaude := false
	foundCursor := false
	foundGrok := false
	for _, item := range response.Data {
		if item.ID == "codex" && item.Kind == "acp" && item.Command == "codex-acp-adapter" && item.CostMode == "external" {
			foundCodex = true
			if !item.SupportsAuthenticate {
				t.Fatalf("codex supports_authenticate = false, want true")
			}
			if !item.SupportsLogout {
				t.Fatalf("codex supports_logout = false, want true")
			}
		}
		if item.ID == "claude_code" && item.Kind == "acp" && item.Command == "claude-code-acp-adapter" && item.CostMode == "external" {
			foundClaude = true
			if !item.SupportsAuthenticate {
				t.Fatalf("claude_code supports_authenticate = false, want true")
			}
			if !item.SupportsLogout {
				t.Fatalf("claude_code supports_logout = false, want true")
			}
		}
		if item.ID == "cursor_agent" && item.Kind == "acp" && item.Command == "cursor-agent" && item.CostMode == "external" {
			foundCursor = true
			if item.SupportsAuthenticate {
				t.Fatalf("cursor_agent supports_authenticate = true, want false")
			}
			if item.SupportsLogout {
				t.Fatalf("cursor_agent supports_logout = true, want false")
			}
		}
		if item.ID == "grok_build" && item.Kind == "acp" && item.Command == "grok" && item.CostMode == "external" {
			foundGrok = true
			if item.SupportsAuthenticate {
				t.Fatalf("grok_build supports_authenticate = true, want false")
			}
			if item.SupportsLogout {
				t.Fatalf("grok_build supports_logout = true, want false")
			}
		}
		if item.Status == "" {
			t.Fatalf("adapter %q missing status: %#v", item.ID, item)
		}
		if item.SupportedRange == "" {
			t.Fatalf("adapter %q missing supported_range: %#v", item.ID, item)
		}
		if len(item.Capabilities) == 0 {
			t.Fatalf("adapter %q missing capabilities: %#v", item.ID, item)
		}
		assertAgentAdapterResponseCapability(t, item, agentadapters.CapabilityPromptSession, agentadapters.CapabilityStatusSupported)
		assertAgentAdapterResponseCapability(t, item, agentadapters.CapabilityCancel, agentadapters.CapabilityStatusSupported)
		assertAgentAdapterResponseCapability(t, item, agentadapters.CapabilityPermissions, agentadapters.CapabilityStatusSupported)
		assertAgentAdapterResponseCapability(t, item, agentadapters.CapabilityTerminalRPC, agentadapters.CapabilityStatusOperatorOptIn)
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
	if !foundGrok {
		t.Fatalf("missing grok_build adapter: %#v", response.Data)
	}
}

func assertAgentAdapterResponseCapability(t *testing.T, item AgentAdapterResponseItem, id, status string) {
	t.Helper()
	for _, cap := range item.Capabilities {
		if cap.ID != id {
			continue
		}
		if cap.Status != status {
			t.Fatalf("adapter %q capability %q status = %q, want %q", item.ID, id, cap.Status, status)
		}
		if cap.Name == "" || cap.Description == "" {
			t.Fatalf("adapter %q capability %q missing copy: %#v", item.ID, id, cap)
		}
		return
	}
	t.Fatalf("adapter %q missing capability %q in %#v", item.ID, id, item.Capabilities)
}

func TestAgentAdaptersListDoesNotRunAdapterDiagnostics(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("shell-script subprocess fixture is POSIX-only")
	}
	dir := t.TempDir()
	countFile := filepath.Join(dir, "executed")
	for _, name := range []string{
		"codex-acp-adapter",
		"codex",
		"claude-code-acp-adapter",
		"claude",
		"cursor-agent",
		"grok",
	} {
		path := filepath.Join(dir, name)
		body := "#!/bin/sh\n" +
			"echo \"$0 $@\" >> " + strconv.Quote(countFile) + "\n" +
			"echo " + strconv.Quote(name+" 9.9.9") + "\n"
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatalf("write fake executable %s: %v", name, err)
		}
	}
	t.Setenv("PATH", dir)
	t.Setenv("HOME", t.TempDir())

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := NewServer(logger, NewHandler(config.Config{}, logger, nil, nil, nil, nil))
	client := newAPITestClient(t, handler)
	response := mustRequestJSON[AgentAdapterResponse](client, http.MethodGet, "/hecate/v1/agent-adapters", "")

	if len(response.Data) != 4 {
		t.Fatalf("adapter count = %d, want 4", len(response.Data))
	}
	for _, item := range response.Data {
		if item.AdapterVersion != "" || item.AgentVersion != "" {
			t.Fatalf("adapter %q versions = %q/%q, want lazy empty versions", item.ID, item.AdapterVersion, item.AgentVersion)
		}
		if item.AuthStatus != agentadapters.AuthStatusUnknown {
			t.Fatalf("adapter %q auth_status = %q, want lazy unknown", item.ID, item.AuthStatus)
		}
		if len(item.ConfigOptions) != 0 {
			t.Fatalf("adapter %q config_options = %#v, want lazy empty options", item.ID, item.ConfigOptions)
		}
	}
	if raw, err := os.ReadFile(countFile); err == nil {
		t.Fatalf("adapter list executed diagnostics:\n%s", string(raw))
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read diagnostics count file: %v", err)
	}
}

func TestAgentAdaptersOmitsCatalogConfigOptionsForGrok(t *testing.T) {
	t.Setenv("HECATE_AGENT_ADAPTER_DEV_OVERRIDES", "grok_build=ready")

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := NewServer(logger, NewHandler(config.Config{}, logger, nil, nil, nil, nil))
	client := newAPITestClient(t, handler)
	response := mustRequestJSON[AgentAdapterResponse](client, http.MethodGet, "/hecate/v1/agent-adapters", "")

	var grok *AgentAdapterResponseItem
	for i := range response.Data {
		if response.Data[i].ID == "grok_build" {
			grok = &response.Data[i]
			break
		}
	}
	if grok == nil {
		t.Fatalf("missing grok_build adapter: %#v", response.Data)
	}
	if len(grok.ConfigOptions) != 0 {
		t.Fatalf("grok config options = %#v, want ACP-owned controls only", grok.ConfigOptions)
	}
}

func TestAgentAdaptersHonorsDiscoveryOverride(t *testing.T) {
	t.Setenv("HECATE_AGENT_ADAPTER_DISCOVERY_OVERRIDES", "all=missing")

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := NewServer(logger, NewHandler(config.Config{}, logger, nil, nil, nil, nil))
	client := newAPITestClient(t, handler)
	response := mustRequestJSON[AgentAdapterResponse](client, http.MethodGet, "/hecate/v1/agent-adapters", "")
	if len(response.Data) != 4 {
		t.Fatalf("adapter count = %d, want 4", len(response.Data))
	}
	for _, item := range response.Data {
		if item.Available || item.Status != agentadapters.StatusMissing || item.Path != "" {
			t.Fatalf("adapter %q = %#v, want forced missing", item.ID, item)
		}
		if !strings.Contains(item.Error, "HECATE_AGENT_ADAPTER_DISCOVERY_OVERRIDES") {
			t.Fatalf("adapter %q error = %q, want discovery override marker", item.ID, item.Error)
		}
	}
}

func TestAgentChatRunsExternalAdapter(t *testing.T) {
	dir := t.TempDir()
	if _, err := exec.LookPath("git"); err == nil {
		_ = exec.Command("git", "-C", dir, "init", "-b", "main").Run()
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
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
	created := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"codex","workspace":%q,"title":"Codex test"}`, dir))
	if created.Data.AgentID != "codex" {
		t.Fatalf("adapter_id = %q, want codex", created.Data.AgentID)
	}
	if got := created.Data.WorkspaceBranch; got != "" && got != "main" {
		t.Fatalf("workspace_branch = %q, want empty or main", got)
	}

	recorder := client.mustRequest(http.MethodPost, "/hecate/v1/chat/sessions/"+created.Data.ID+"/messages", `{"content":"hello from hecate"}`)
	if recorder.Header().Get("X-Trace-Id") == "" {
		t.Fatal("X-Trace-Id = empty, want agent chat trace id")
	}
	if recorder.Header().Get("X-Span-Id") == "" {
		t.Fatal("X-Span-Id = empty, want agent chat span id")
	}
	updated := decodeRecorder[ChatSessionResponse](t, recorder)
	if len(updated.Data.Messages) != 2 {
		t.Fatalf("message count = %d, want 2: %#v", len(updated.Data.Messages), updated.Data.Messages)
	}
	assistant := updated.Data.Messages[1]
	if assistant.Role != "assistant" || assistant.AgentID != "codex" || assistant.Status != "completed" {
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
	tracePayload := mustRequestJSON[TraceResponse](client, http.MethodGet, "/hecate/v1/traces?request_id="+assistant.RequestID, "")
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
	if got := agentSpan.Attributes[telemetry.AttrHecatePhase]; got != "chat" {
		t.Fatalf("agent span phase = %#v, want agent_chat", got)
	}
	wantSpanAttrs := map[string]any{
		telemetry.AttrHecateChatSessionID:        created.Data.ID,
		telemetry.AttrHecateChatMessageID:        assistant.ID,
		telemetry.AttrHecateRunID:                assistant.RunID,
		telemetry.AttrHecateExecutionKind:        "chat",
		telemetry.AttrHecateAgentAdapterID:       "codex",
		telemetry.AttrHecateAgentAdapterName:     "Codex",
		telemetry.AttrHecateAgentAdapterCommand:  "codex-acp-adapter",
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

func TestAgentChatSurfacesExternalAgentStopReason(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatRunner(&fakeAgentChatRunner{
		output:     "partial answer\n",
		stopReason: "max_tokens",
	})
	handler := NewServer(logger, apiHandler)
	client := newAPITestClient(t, handler)

	created := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"codex","workspace":%q}`, dir))
	updated := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+created.Data.ID+"/messages", `{"content":"answer until the context limit"}`)
	if len(updated.Data.Messages) != 2 {
		t.Fatalf("message count = %d, want 2", len(updated.Data.Messages))
	}
	assistant := updated.Data.Messages[1]
	var found *ChatActivityItem
	for i := range assistant.Activities {
		if assistant.Activities[i].Type == "stop_reason" {
			found = &assistant.Activities[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("activities = %#v, want stop_reason activity", assistant.Activities)
	}
	if found.Status != "completed" || found.Title != "Agent stop reason" || !strings.Contains(found.Detail, "token limit") {
		t.Fatalf("stop reason activity = %#v, want completed token-limit detail", *found)
	}
}

func TestUpdateChatSessionRenamesSession(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatRunner(&fakeAgentChatRunner{nativeSessionID: "native_codex_rename"})
	handler := NewServer(logger, apiHandler)
	client := newAPITestClient(t, handler)

	created := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"codex","workspace":%q,"title":"Original"}`, dir))
	updated := mustRequestJSON[ChatSessionResponse](client, http.MethodPatch, "/hecate/v1/chat/sessions/"+created.Data.ID, `{"title":"Renamed chat"}`)
	if updated.Data.Title != "Renamed chat" {
		t.Fatalf("title = %q, want Renamed chat", updated.Data.Title)
	}
	list := mustRequestJSON[ChatSessionsResponse](client, http.MethodGet, "/hecate/v1/chat/sessions", "")
	if len(list.Data) != 1 || list.Data[0].Title != "Renamed chat" {
		t.Fatalf("list title = %#v, want renamed session", list.Data)
	}
}

func TestUpdateChatSessionRejectsEmptyTitle(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	handler := NewServer(logger, apiHandler)
	client := newAPITestClient(t, handler)

	client.mustRequestStatus(http.StatusBadRequest, http.MethodPatch, "/hecate/v1/chat/sessions/missing", `{"title":"   "}`)
}

func TestAgentChatOmitsStartedActivityWhenNativeSessionReused(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatRunner(&fakeAgentChatRunner{
		output:          "agent saw: reused session\n",
		nativeSessionID: "native_codex_1",
		sessionStarted:  false,
	})
	handler := NewServer(logger, apiHandler)
	client := newAPITestClient(t, handler)
	created := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"codex","workspace":%q}`, dir))
	updated := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+created.Data.ID+"/messages", `{"content":"hello"}`)
	assistant := updated.Data.Messages[1]
	if agentChatActivitiesContain(assistant.Activities, "started") {
		t.Fatalf("started activity present for reused native session: %#v", assistant.Activities)
	}
}

func TestAgentChatMergesAdapterActivityUpdates(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatRunner(&fakeAgentChatRunner{
		output: "done",
		activities: []agentadapters.Activity{
			{ID: "tool:call_1", Type: "tool_call", Status: "running", Kind: "execute", Title: "git status", Detail: "README.md"},
			{ID: "tool:call_1", Type: "tool_call", Status: "completed", Kind: "execute", Title: "git status", Detail: "README.md"},
			{ID: "plan:0:Inspect", Type: "plan", Status: "completed", Kind: "high", Title: "Inspect"},
		},
	})
	client := newAPITestClient(t, NewServer(logger, apiHandler))

	created := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"codex","workspace":%q}`, dir))
	updated := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+created.Data.ID+"/messages", `{"content":"hello"}`)
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
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatRunner(&fakeAgentChatRunner{
		chunks:      []string{"I will inspect the diff first."},
		finalOutput: "There is no current diff.",
	})
	client := newAPITestClient(t, NewServer(logger, apiHandler))

	created := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"codex","workspace":%q}`, dir))
	updated := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+created.Data.ID+"/messages", `{"content":"show diff"}`)
	assistant := updated.Data.Messages[len(updated.Data.Messages)-1]
	if assistant.Content != "There is no current diff." {
		t.Fatalf("assistant content = %q, want final output replacing streamed narration", assistant.Content)
	}
}

func TestAgentChatPassesPersistedNativeSessionForResume(t *testing.T) {
	dir := t.TempDir()
	store := chat.NewMemoryStore()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	firstRunner := &fakeAgentChatRunner{
		output:          "first answer",
		nativeSessionID: "native_persisted_1",
		sessionStarted:  true,
	}
	firstHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	firstHandler.SetAgentChatStore(store)
	firstHandler.SetAgentChatRunner(firstRunner)
	firstClient := newAPITestClient(t, NewServer(logger, firstHandler))
	created := mustRequestJSON[ChatSessionResponse](firstClient, http.MethodPost, "/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"codex","workspace":%q}`, dir))
	_ = mustRequestJSON[ChatSessionResponse](firstClient, http.MethodPost, "/hecate/v1/chat/sessions/"+created.Data.ID+"/messages", `{"content":"first"}`)

	secondRunner := &fakeAgentChatRunner{
		output:          "second answer",
		nativeSessionID: "native_persisted_1",
		sessionStarted:  true,
		sessionResumed:  true,
	}
	secondHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	secondHandler.SetAgentChatStore(store)
	secondHandler.SetAgentChatRunner(secondRunner)
	secondClient := newAPITestClient(t, NewServer(logger, secondHandler))
	updated := mustRequestJSON[ChatSessionResponse](secondClient, http.MethodPost, "/hecate/v1/chat/sessions/"+created.Data.ID+"/messages", `{"content":"second"}`)
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

func TestAgentChatReadDoesNotStartExternalNativeSession(t *testing.T) {
	dir := t.TempDir()
	store := chat.NewMemoryStore()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	storedBool := false

	created, err := store.Create(context.Background(), chat.Session{
		ID:              "chat_external_load",
		Title:           "Codex",
		AgentID:         "codex",
		DriverKind:      agentadapters.DriverKindACP,
		NativeSessionID: "native_existing",
		Workspace:       dir,
		ConfigOptions: []agentcontrols.ConfigOption{
			{ID: "auto_approve", Name: "Auto approve", Type: agentcontrols.ConfigOptionTypeBoolean, CurrentBool: &storedBool},
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	runner := &fakeAgentChatRunner{
		nativeSessionID: "native_existing",
		sessionResumed:  true,
	}
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatStore(store)
	apiHandler.SetAgentChatRunner(runner)
	client := newAPITestClient(t, NewServer(logger, apiHandler))

	got := mustRequestJSON[ChatSessionResponse](client, http.MethodGet, "/hecate/v1/chat/sessions/"+created.ID, "")
	if len(runner.prepareRequests) != 0 {
		t.Fatalf("prepare requests = %d, want 0 for passive read", len(runner.prepareRequests))
	}
	if got.Data.NativeSessionID != "native_existing" || got.Data.DriverKind != agentadapters.DriverKindACP {
		t.Fatalf("read session metadata = kind %q native %q", got.Data.DriverKind, got.Data.NativeSessionID)
	}
	if len(got.Data.ConfigOptions) != 1 || got.Data.ConfigOptions[0].CurrentBool == nil || *got.Data.ConfigOptions[0].CurrentBool {
		t.Fatalf("config options = %#v, want stored controls on passive read", got.Data.ConfigOptions)
	}
}

func TestAgentChatStreamDoesNotStartExternalNativeSessionOnSubscribe(t *testing.T) {
	dir := t.TempDir()
	store := chat.NewMemoryStore()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	storedBool := false

	created, err := store.Create(context.Background(), chat.Session{
		ID:              "chat_external_stream_load",
		Title:           "Codex",
		AgentID:         "codex",
		DriverKind:      agentadapters.DriverKindACP,
		NativeSessionID: "native_existing",
		Workspace:       dir,
		ConfigOptions: []agentcontrols.ConfigOption{
			{ID: "auto_approve", Name: "Auto approve", Type: agentcontrols.ConfigOptionTypeBoolean, CurrentBool: &storedBool},
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	runner := &fakeAgentChatRunner{
		nativeSessionID: "native_existing",
		sessionResumed:  true,
	}
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatStore(store)
	apiHandler.SetAgentChatRunner(runner)
	server := httptest.NewServer(NewServer(logger, apiHandler))
	t.Cleanup(server.Close)

	streamCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	streamReq, err := http.NewRequestWithContext(streamCtx, http.MethodGet, server.URL+"/hecate/v1/chat/sessions/"+created.ID+"/stream", nil)
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
	var snapshot ChatSessionResponse
	timeout := time.After(3 * time.Second)
	gotSnapshot := false
	for !gotSnapshot {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatal("stream closed before initial snapshot")
			}
			if event.Event != "snapshot" {
				continue
			}
			if err := json.Unmarshal([]byte(event.Data), &snapshot); err != nil {
				t.Fatalf("decode stream snapshot: %v", err)
			}
			cancel()
			gotSnapshot = true
		case <-timeout:
			t.Fatal("timed out waiting for stream snapshot")
		}
	}

	if len(runner.prepareRequests) != 0 {
		t.Fatalf("prepare requests = %d, want 0 for passive stream subscribe", len(runner.prepareRequests))
	}
	if snapshot.Data.NativeSessionID != "native_existing" || snapshot.Data.DriverKind != agentadapters.DriverKindACP {
		t.Fatalf("stream snapshot metadata = kind %q native %q", snapshot.Data.DriverKind, snapshot.Data.NativeSessionID)
	}
	if len(snapshot.Data.ConfigOptions) != 1 || snapshot.Data.ConfigOptions[0].CurrentBool == nil || *snapshot.Data.ConfigOptions[0].CurrentBool {
		t.Fatalf("stream snapshot config options = %#v, want stored controls", snapshot.Data.ConfigOptions)
	}
}

func TestAgentChatShowsFreshSessionRecoveryActivity(t *testing.T) {
	dir := t.TempDir()
	store := chat.NewMemoryStore()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatStore(store)
	apiHandler.SetAgentChatRunner(&fakeAgentChatRunner{
		output:          "recovered",
		nativeSessionID: "native_fresh",
		sessionStarted:  true,
		sessionRecovery: "could not restore ACP session native_stale: not found",
	})
	client := newAPITestClient(t, NewServer(logger, apiHandler))

	created := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"codex","workspace":%q}`, dir))
	if _, err := store.UpdateSession(context.Background(), created.Data.ID, func(item *chat.Session) {
		item.NativeSessionID = "native_stale"
	}); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	updated := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+created.Data.ID+"/messages", `{"content":"second"}`)
	if updated.Data.NativeSessionID != "native_fresh" {
		t.Fatalf("native session = %q, want fresh session", updated.Data.NativeSessionID)
	}
	assistant := updated.Data.Messages[len(updated.Data.Messages)-1]
	if !agentChatActivitiesContain(assistant.Activities, "recovered") {
		t.Fatalf("activities = %+v, want recovered activity", assistant.Activities)
	}
}

func TestAgentChatTagsAdapterJSONRPCBillingError(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	rawErr := `{"code":-32603,"message":"Internal error: Credit balance is too low","data":{"errorKind":"billing_error"}}`
	apiHandler.SetAgentChatRunner(&fakeAgentChatRunner{
		err: errors.New(rawErr),
	})
	handler := NewServer(logger, apiHandler)
	client := newAPITestClient(t, handler)
	created := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"claude_code","workspace":%q}`, dir))
	updated := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+created.Data.ID+"/messages", `{"content":"hello"}`)
	assistant := updated.Data.Messages[1]
	if assistant.Status != "failed" {
		t.Fatalf("assistant status = %q, want failed", assistant.Status)
	}
	if !strings.Contains(assistant.Content, "Claude Code error (billing_error): Credit balance is too low") {
		t.Fatalf("assistant content = %q, want errorKind-tagged adapter error", assistant.Content)
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

func TestChatMessageFilesReturnsStructuredDiff(t *testing.T) {
	store := chat.NewMemoryStore()
	sessionID := "chat_diff"
	messageID := "msg_diff"
	diff := `diff --git a/README.md b/README.md
--- a/README.md
+++ b/README.md
@@ -1 +1,2 @@
 hello
+world
diff --git a/src/app.go b/src/app.go
--- a/src/app.go
+++ b/src/app.go
@@ -1,2 +1,2 @@
-old
+new
 keep`
	if _, err := store.Create(context.Background(), chat.Session{
		ID:        sessionID,
		Title:     "Diff",
		AgentID:   "codex",
		Workspace: t.TempDir(),
		Status:    "completed",
		Messages: []chat.Message{
			{ID: messageID, Role: "assistant", Status: "completed", Diff: diff, DiffStat: "2 files changed, 2 insertions(+), 1 deletion(-)"},
		},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	apiHandler.SetAgentChatStore(store)
	client := newAPITestClient(t, NewServer(logger, apiHandler))

	resp := mustRequestJSON[ChatChangedFilesResponse](client, http.MethodGet, "/hecate/v1/chat/sessions/"+sessionID+"/messages/"+messageID+"/files", "")
	if resp.Object != "chat_changed_files" {
		t.Fatalf("object = %q, want agent_chat_changed_files", resp.Object)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("file count = %d, want 2: %#v", len(resp.Data), resp.Data)
	}
	if resp.Data[0].Path != "README.md" || resp.Data[0].Additions != 1 || resp.Data[0].Deletions != 0 || resp.Data[0].Status != "modified" {
		t.Fatalf("first file = %#v", resp.Data[0])
	}
	if resp.Data[1].Path != "src/app.go" || resp.Data[1].Additions != 1 || resp.Data[1].Deletions != 1 {
		t.Fatalf("second file = %#v", resp.Data[1])
	}
}

func TestChatMessageFileDiffReturnsSingleFileBlock(t *testing.T) {
	store := chat.NewMemoryStore()
	sessionID := "chat_file_diff"
	messageID := "msg_diff"
	diff := `diff --git a/README.md b/README.md
--- a/README.md
+++ b/README.md
@@ -1 +1,2 @@
 hello
+world
diff --git a/src/app.go b/src/app.go
--- a/src/app.go
+++ b/src/app.go
@@ -1,2 +1,2 @@
-old
+new
 keep`
	if _, err := store.Create(context.Background(), chat.Session{
		ID:        sessionID,
		Title:     "Diff",
		AgentID:   "codex",
		Workspace: t.TempDir(),
		Status:    "completed",
		Messages:  []chat.Message{{ID: messageID, Role: "assistant", Status: "completed", Diff: diff}},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	apiHandler.SetAgentChatStore(store)
	client := newAPITestClient(t, NewServer(logger, apiHandler))

	encodedPath := url.PathEscape("src/app.go")
	resp := mustRequestJSON[ChatChangedFileDiffResponse](client, http.MethodGet, "/hecate/v1/chat/sessions/"+sessionID+"/messages/"+messageID+"/files/"+encodedPath, "")
	if resp.Object != "chat_changed_file_diff" {
		t.Fatalf("object = %q, want agent_chat_changed_file_diff", resp.Object)
	}
	if resp.Data.Path != "src/app.go" || !strings.Contains(resp.Data.Diff, "diff --git a/src/app.go") {
		t.Fatalf("data = %#v, want src/app.go diff", resp.Data)
	}
	if strings.Contains(resp.Data.Diff, "diff --git a/README.md") {
		t.Fatalf("file diff included unrelated block: %q", resp.Data.Diff)
	}

	rec := client.mustRequestStatus(http.StatusNotFound, http.MethodGet, "/hecate/v1/chat/sessions/"+sessionID+"/messages/"+messageID+"/files/"+url.PathEscape("missing.go"), "")
	if !strings.Contains(rec.Body.String(), "changed file not found") {
		t.Fatalf("missing body = %s", rec.Body.String())
	}
}

func TestChatWorkspaceDiffReturnsCurrentGitDiff(t *testing.T) {
	workspace := t.TempDir()
	runTestGit(t, workspace, "init")
	runTestGit(t, workspace, "config", "user.email", "hecate@example.test")
	runTestGit(t, workspace, "config", "user.name", "Hecate Test")
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "src", "app.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write src/app.go: %v", err)
	}
	runTestGit(t, workspace, "add", "README.md")
	runTestGit(t, workspace, "add", "src/app.go")
	runTestGit(t, workspace, "commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("modify README: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "src", "app.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("modify src/app.go: %v", err)
	}
	store := chat.NewMemoryStore()
	sessionID := "chat_workspace_diff"
	if _, err := store.Create(context.Background(), chat.Session{
		ID:        sessionID,
		Title:     "Diff",
		AgentID:   "codex",
		Workspace: workspace,
		Status:    "completed",
		Messages: []chat.Message{
			{
				ID:       "captured",
				Role:     "assistant",
				Status:   "completed",
				DiffStat: "old.txt | 1 +",
				Diff:     "diff --git a/old.txt b/old.txt\n+old",
			},
		},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	apiHandler.SetAgentChatStore(store)
	client := newAPITestClient(t, NewServer(logger, apiHandler))

	resp := mustRequestJSON[ChatWorkspaceDiffResponse](client, http.MethodGet, "/hecate/v1/chat/sessions/"+sessionID+"/workspace-diff", "")
	if resp.Object != "chat_workspace_diff" {
		t.Fatalf("object = %q, want chat_workspace_diff", resp.Object)
	}
	if !resp.Data.HasChanges {
		t.Fatalf("has_changes = false, want true")
	}
	if !strings.Contains(resp.Data.DiffStat, "README.md") {
		t.Fatalf("diff_stat = %q, want current README diff", resp.Data.DiffStat)
	}
	if strings.Contains(resp.Data.Diff, "old.txt") {
		t.Fatalf("workspace diff used captured message diff: %q", resp.Data.Diff)
	}
	if len(resp.Data.Files) != 2 || !chatChangedFilesContain(resp.Data.Files, "README.md") || !chatChangedFilesContain(resp.Data.Files, "src/app.go") {
		t.Fatalf("files = %#v, want current README and nested src/app.go files", resp.Data.Files)
	}

	fileDiff := mustRequestJSON[ChatChangedFileDiffResponse](client, http.MethodGet, "/hecate/v1/chat/sessions/"+sessionID+"/workspace-diff/files/"+url.PathEscape("README.md"), "")
	if fileDiff.Object != "chat_workspace_file_diff" {
		t.Fatalf("file diff object = %q, want chat_workspace_file_diff", fileDiff.Object)
	}
	if fileDiff.Data.Path != "README.md" || !strings.Contains(fileDiff.Data.Diff, "+world") {
		t.Fatalf("file diff = %#v, want current README diff", fileDiff.Data)
	}
	if strings.Contains(fileDiff.Data.Diff, "old.txt") {
		t.Fatalf("file diff used captured message diff: %q", fileDiff.Data.Diff)
	}
	nestedDiff := mustRequestJSON[ChatChangedFileDiffResponse](client, http.MethodGet, "/hecate/v1/chat/sessions/"+sessionID+"/workspace-diff/files/"+url.PathEscape("src/app.go"), "")
	if nestedDiff.Data.Path != "src/app.go" || !strings.Contains(nestedDiff.Data.Diff, "func main") {
		t.Fatalf("nested file diff = %#v, want src/app.go diff", nestedDiff.Data)
	}
}

func TestChatWorkspaceDiffRejectsInvalidWorkspaces(t *testing.T) {
	for _, tc := range []struct {
		name      string
		workspace string
	}{
		{name: "non_git", workspace: t.TempDir()},
		{name: "missing", workspace: filepath.Join(t.TempDir(), "missing")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := chat.NewMemoryStore()
			sessionID := "chat_workspace_diff_" + tc.name
			if _, err := store.Create(context.Background(), chat.Session{
				ID:        sessionID,
				Title:     "Diff",
				AgentID:   "codex",
				Workspace: tc.workspace,
				Status:    "completed",
			}); err != nil {
				t.Fatalf("Create: %v", err)
			}
			logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
			apiHandler := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
			apiHandler.SetAgentChatStore(store)
			client := newAPITestClient(t, NewServer(logger, apiHandler))

			rec := client.mustRequestStatus(http.StatusBadRequest, http.MethodGet, "/hecate/v1/chat/sessions/"+sessionID+"/workspace-diff", "")
			if !strings.Contains(rec.Body.String(), "chat workspace is not a git worktree") {
				t.Fatalf("body = %s, want git worktree error", rec.Body.String())
			}
		})
	}
}

func TestChatWorkspaceFilesReturnsTreeWithGitStatus(t *testing.T) {
	workspace := t.TempDir()
	runTestGit(t, workspace, "init")
	runTestGit(t, workspace, "config", "user.email", "hecate@example.test")
	runTestGit(t, workspace, "config", "user.name", "Hecate Test")
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "src", "app.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write src/app.go: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, "node_modules", "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir node_modules: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "node_modules", "pkg", "index.js"), []byte("ignored\n"), 0o644); err != nil {
		t.Fatalf("write ignored node module: %v", err)
	}
	runTestGit(t, workspace, "add", ".")
	runTestGit(t, workspace, "commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("modify README: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "src", "new.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write src/new.go: %v", err)
	}

	store := chat.NewMemoryStore()
	sessionID := "chat_workspace_files"
	if _, err := store.Create(context.Background(), chat.Session{
		ID:        sessionID,
		Title:     "Files",
		AgentID:   "codex",
		Workspace: workspace,
		Status:    "completed",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	apiHandler.SetAgentChatStore(store)
	client := newAPITestClient(t, NewServer(logger, apiHandler))

	resp := mustRequestJSON[ChatWorkspaceFilesResponse](client, http.MethodGet, "/hecate/v1/chat/sessions/"+sessionID+"/workspace-files", "")
	if resp.Object != "chat_workspace_files" {
		t.Fatalf("object = %q, want chat_workspace_files", resp.Object)
	}
	if resp.Data.Workspace != workspace {
		t.Fatalf("workspace = %q, want %q", resp.Data.Workspace, workspace)
	}
	if resp.Data.Truncated {
		t.Fatalf("truncated = true, want false")
	}
	if file := chatWorkspaceFileByPath(resp.Data.Files, "README.md"); file == nil || file.Kind != "file" || file.Status != "modified" {
		t.Fatalf("README file = %#v, want modified file", file)
	}
	if file := chatWorkspaceFileByPath(resp.Data.Files, "src"); file == nil || file.Kind != "directory" {
		t.Fatalf("src file = %#v, want directory", file)
	}
	if file := chatWorkspaceFileByPath(resp.Data.Files, "src/new.go"); file == nil || file.Status != "untracked" {
		t.Fatalf("src/new.go file = %#v, want untracked status", file)
	}
	if file := chatWorkspaceFileByPath(resp.Data.Files, "node_modules/pkg/index.js"); file != nil {
		t.Fatalf("node_modules file = %#v, want skipped", file)
	}
}

func TestChatWorkspaceFilesReturnsEmptyWithoutWorkspace(t *testing.T) {
	store := chat.NewMemoryStore()
	sessionID := "chat_workspace_files_empty"
	if _, err := store.Create(context.Background(), chat.Session{
		ID:      sessionID,
		Title:   "Files",
		AgentID: "codex",
		Status:  "completed",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	apiHandler.SetAgentChatStore(store)
	client := newAPITestClient(t, NewServer(logger, apiHandler))

	resp := mustRequestJSON[ChatWorkspaceFilesResponse](client, http.MethodGet, "/hecate/v1/chat/sessions/"+sessionID+"/workspace-files", "")
	if resp.Object != "chat_workspace_files" {
		t.Fatalf("object = %q, want chat_workspace_files", resp.Object)
	}
	if resp.Data.Workspace != "" {
		t.Fatalf("workspace = %q, want empty", resp.Data.Workspace)
	}
	if len(resp.Data.Files) != 0 {
		t.Fatalf("files = %#v, want empty", resp.Data.Files)
	}
}

func TestParseWorkspaceGitStatusSkipsRenameAndCopySources(t *testing.T) {
	// In porcelain v1 -z output Git reverses rename/copy paths:
	// status + destination, then a NUL-separated source path.
	statuses := parseWorkspaceGitStatus(strings.Join([]string{
		"R  src/new.go",
		"src/old.go",
		"C  docs/copy.md",
		"docs/source.md",
		" M README.md",
		"?? scratch.txt",
		"",
	}, "\x00"))

	expected := map[string]string{
		"src/new.go":   "renamed",
		"docs/copy.md": "copied",
		"README.md":    "modified",
		"scratch.txt":  "untracked",
	}
	for path, want := range expected {
		if got := statuses[path]; got != want {
			t.Fatalf("status[%q] = %q, want %q; all statuses = %#v", path, got, want, statuses)
		}
	}
	if len(statuses) != len(expected) {
		t.Fatalf("statuses = %#v, want exactly %#v", statuses, expected)
	}
	if _, ok := statuses["src/old.go"]; ok {
		t.Fatalf("old rename path was parsed as a status: %#v", statuses)
	}
	if _, ok := statuses["docs/source.md"]; ok {
		t.Fatalf("copy source path was parsed as a status: %#v", statuses)
	}
}

func chatChangedFilesContain(files []ChatChangedFileItem, path string) bool {
	for _, file := range files {
		if file.Path == path {
			return true
		}
	}
	return false
}

func chatWorkspaceFileByPath(files []ChatWorkspaceFileItem, path string) *ChatWorkspaceFileItem {
	for i := range files {
		if files[i].Path == path {
			return &files[i]
		}
	}
	return nil
}

func TestRevertChatWorkspaceFilesRestoresSelectedCurrentPaths(t *testing.T) {
	workspace := t.TempDir()
	runTestGit(t, workspace, "init")
	runTestGit(t, workspace, "config", "user.email", "hecate@example.test")
	runTestGit(t, workspace, "config", "user.name", "Hecate Test")
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "notes.md"), []byte("note\n"), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
	}
	runTestGit(t, workspace, "add", ".")
	runTestGit(t, workspace, "commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("modify README: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "notes.md"), []byte("note\nmore\n"), 0o644); err != nil {
		t.Fatalf("modify notes: %v", err)
	}
	store := chat.NewMemoryStore()
	sessionID := "chat_workspace_revert"
	if _, err := store.Create(context.Background(), chat.Session{
		ID:        sessionID,
		Title:     "Diff",
		AgentID:   "codex",
		Workspace: workspace,
		Status:    "completed",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	apiHandler.SetAgentChatStore(store)
	client := newAPITestClient(t, NewServer(logger, apiHandler))

	resp := mustRequestJSON[ChatWorkspaceDiffResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+sessionID+"/workspace-diff/revert", `{"paths":["README.md"]}`)
	if resp.Object != "chat_workspace_diff" {
		t.Fatalf("object = %q, want chat_workspace_diff", resp.Object)
	}
	if strings.Contains(resp.Data.DiffStat, "README.md") {
		t.Fatalf("diff_stat still contains reverted path: %q", resp.Data.DiffStat)
	}
	if !strings.Contains(resp.Data.DiffStat, "notes.md") {
		t.Fatalf("diff_stat = %q, want remaining notes.md diff", resp.Data.DiffStat)
	}
	readme, err := os.ReadFile(filepath.Join(workspace, "README.md"))
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	if got := string(readme); got != "hello\n" {
		t.Fatalf("README after revert = %q, want original", got)
	}
	notes, err := os.ReadFile(filepath.Join(workspace, "notes.md"))
	if err != nil {
		t.Fatalf("read notes: %v", err)
	}
	if got := string(notes); got != "note\nmore\n" {
		t.Fatalf("notes after revert = %q, want untouched modification", got)
	}
}

func TestRevertChatMessageFilesRestoresSelectedPaths(t *testing.T) {
	workspace := t.TempDir()
	runTestGit(t, workspace, "init")
	runTestGit(t, workspace, "config", "user.email", "hecate@example.test")
	runTestGit(t, workspace, "config", "user.name", "Hecate Test")
	if err := os.MkdirAll(filepath.Join(workspace, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "src", "app.go"), []byte("package main\nvar value = 1\n"), 0o644); err != nil {
		t.Fatalf("write app: %v", err)
	}
	runTestGit(t, workspace, "add", ".")
	runTestGit(t, workspace, "commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("modify README: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "src", "app.go"), []byte("package main\nvar value = 2\n"), 0o644); err != nil {
		t.Fatalf("modify app: %v", err)
	}
	diff := runTestGit(t, workspace, "diff", "--binary")
	diffStat := runTestGit(t, workspace, "diff", "--stat")

	store := chat.NewMemoryStore()
	sessionID := "chat_revert"
	messageID := "msg_revert"
	if _, err := store.Create(context.Background(), chat.Session{
		ID:        sessionID,
		Title:     "Revert",
		AgentID:   "codex",
		Workspace: workspace,
		Status:    "completed",
		Messages:  []chat.Message{{ID: messageID, Role: "assistant", Status: "completed", Diff: diff, DiffStat: diffStat}},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	apiHandler.SetAgentChatStore(store)
	client := newAPITestClient(t, NewServer(logger, apiHandler))

	resp := mustRequestJSON[ChatRevertResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+sessionID+"/messages/"+messageID+"/revert", `{"paths":["src/app.go"]}`)
	if !resp.Data.Reverted || len(resp.Data.Paths) != 1 || resp.Data.Paths[0] != "src/app.go" {
		t.Fatalf("revert response = %#v", resp.Data)
	}
	appContent, err := os.ReadFile(filepath.Join(workspace, "src", "app.go"))
	if err != nil {
		t.Fatalf("read app: %v", err)
	}
	if string(appContent) != "package main\nvar value = 1\n" {
		t.Fatalf("app content = %q, want reverted", string(appContent))
	}
	readmeContent, err := os.ReadFile(filepath.Join(workspace, "README.md"))
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	if string(readmeContent) != "hello\nworld\n" {
		t.Fatalf("README content = %q, want still modified", string(readmeContent))
	}
	if len(resp.Data.Files) != 1 || resp.Data.Files[0].Path != "README.md" {
		t.Fatalf("remaining files = %#v, want README only", resp.Data.Files)
	}
	got := mustRequestJSON[ChatSessionResponse](client, http.MethodGet, "/hecate/v1/chat/sessions/"+sessionID, "")
	assistant := got.Data.Messages[0]
	if !agentChatActivitiesContain(assistant.Activities, "files_reverted") {
		t.Fatalf("activities = %#v, want files_reverted", assistant.Activities)
	}
	if strings.Contains(assistant.Diff, "src/app.go") {
		t.Fatalf("updated message diff still contains reverted path: %q", assistant.Diff)
	}
}

func TestRevertChatMessageFilesRequiresGitWorkspace(t *testing.T) {
	store := chat.NewMemoryStore()
	sessionID := "chat_revert_nongit"
	messageID := "msg_revert"
	if _, err := store.Create(context.Background(), chat.Session{
		ID:        sessionID,
		Title:     "Revert",
		AgentID:   "codex",
		Workspace: t.TempDir(),
		Status:    "completed",
		Messages:  []chat.Message{{ID: messageID, Role: "assistant", Status: "completed", DiffStat: "README.md | 1 +"}},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	apiHandler.SetAgentChatStore(store)
	client := newAPITestClient(t, NewServer(logger, apiHandler))

	rec := client.mustRequestStatus(http.StatusBadRequest, http.MethodPost, "/hecate/v1/chat/sessions/"+sessionID+"/messages/"+messageID+"/revert", `{"paths":["README.md"]}`)
	if !strings.Contains(rec.Body.String(), "requires a git workspace") {
		t.Fatalf("body = %s, want git workspace error", rec.Body.String())
	}
}

func runTestGit(t *testing.T, workspace string, args ...string) string {
	t.Helper()
	cmdArgs := append([]string{"-C", workspace}, args...)
	out, err := exec.Command("git", cmdArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func agentChatActivitiesContain(items []ChatActivityItem, kind string) bool {
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

	tests := []struct {
		name string
		body string
	}{
		{
			name: "external agent",
			body: `{"agent_id":"codex","workspace":"/path/that/does/not/exist"}`,
		},
		{
			name: "hecate chat",
			body: `{"agent_id":"hecate","provider":"openai","model":"gpt-4o-mini","workspace":"/path/that/does/not/exist"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := client.mustRequestStatus(http.StatusBadRequest, http.MethodPost, "/hecate/v1/chat/sessions", tt.body)
			if !strings.Contains(rec.Body.String(), "not accessible") {
				t.Fatalf("body = %s, want not accessible error", rec.Body.String())
			}
		})
	}
}

func TestAgentChatCreateUsesStableErrorContracts(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := NewServer(logger, NewHandler(config.Config{}, logger, nil, nil, nil, nil))
	client := newAPITestClient(t, handler)

	tests := []struct {
		name       string
		body       string
		statusCode int
		wantType   string
	}{
		{
			name:       "workspace required for external agent",
			body:       `{"agent_id":"codex"}`,
			statusCode: http.StatusBadRequest,
			wantType:   errCodeWorkspaceRequired,
		},
		{
			name:       "agent id invalid",
			body:       fmt.Sprintf(`{"agent_id":"missing","workspace":%q}`, t.TempDir()),
			statusCode: http.StatusBadRequest,
			wantType:   errCodeAgentIDInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := client.mustRequestStatus(tt.statusCode, http.MethodPost, "/hecate/v1/chat/sessions", tt.body)
			var payload struct {
				Error struct {
					Type           string `json:"type"`
					UserMessage    string `json:"user_message"`
					OperatorAction string `json:"operator_action"`
				} `json:"error"`
			}
			payload = decodeRecorder[struct {
				Error struct {
					Type           string `json:"type"`
					UserMessage    string `json:"user_message"`
					OperatorAction string `json:"operator_action"`
				} `json:"error"`
			}](t, rec)
			if payload.Error.Type != tt.wantType {
				t.Fatalf("error.type = %q, want %q", payload.Error.Type, tt.wantType)
			}
			if payload.Error.UserMessage == "" || payload.Error.OperatorAction == "" {
				t.Fatalf("error missing operator metadata: %+v", payload.Error)
			}
		})
	}
}

func TestAgentChatCreateAllowsEmptyHecateShellAndRequiresModelOnSend(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := NewServer(logger, NewHandler(config.Config{}, logger, nil, nil, nil, nil))
	client := newAPITestClient(t, handler)

	created := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions", `{"agent_id":"hecate","title":"Hecate shell"}`)
	if created.Data.AgentID != chat.DefaultAgentID {
		t.Fatalf("agent_id = %q, want %q", created.Data.AgentID, chat.DefaultAgentID)
	}
	if created.Data.Model != "" {
		t.Fatalf("model = %q, want empty shell model", created.Data.Model)
	}

	rec := client.mustRequestStatus(http.StatusBadRequest, http.MethodPost, "/hecate/v1/chat/sessions/"+created.Data.ID+"/messages", `{"content":"hello","tools_enabled":false}`)
	payload := decodeRecorder[struct {
		Error struct {
			Type           string `json:"type"`
			UserMessage    string `json:"user_message"`
			OperatorAction string `json:"operator_action"`
		} `json:"error"`
	}](t, rec)
	if payload.Error.Type != errCodeModelRequired {
		t.Fatalf("error.type = %q, want %q", payload.Error.Type, errCodeModelRequired)
	}
	if payload.Error.UserMessage == "" || payload.Error.OperatorAction == "" {
		t.Fatalf("error missing operator metadata: %+v", payload.Error)
	}
}

func TestAgentChatModelResolutionRequiredErrorUsesValidationContract(t *testing.T) {
	rec := httptest.NewRecorder()
	writeAgentChatModelResolutionError(rec, errors.New("model is required"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	var payload struct {
		Error struct {
			Type           string `json:"type"`
			UserMessage    string `json:"user_message"`
			OperatorAction string `json:"operator_action"`
		} `json:"error"`
	}
	payload = decodeRecorder[struct {
		Error struct {
			Type           string `json:"type"`
			UserMessage    string `json:"user_message"`
			OperatorAction string `json:"operator_action"`
		} `json:"error"`
	}](t, rec)
	if payload.Error.Type != errCodeModelRequired {
		t.Fatalf("error.type = %q, want %q", payload.Error.Type, errCodeModelRequired)
	}
	if payload.Error.UserMessage == "" || payload.Error.OperatorAction == "" {
		t.Fatalf("error missing operator metadata: %+v", payload.Error)
	}
}

func TestAgentChatModelResolutionErrorIncludesReadinessFields(t *testing.T) {
	rec := httptest.NewRecorder()
	writeAgentChatModelResolutionError(rec, fmt.Errorf("resolve model: %w", modelapp.ReadinessError{
		Cause: errors.New("model \"gpt-5.4-mini\" is not available from provider \"ollama\""),
		Readiness: types.ModelReadiness{
			Provider:              "ollama",
			MatchedProvider:       "ollama",
			Model:                 "gpt-5.4-mini",
			Status:                "blocked",
			Reason:                "model_not_discovered",
			Message:               "Provider \"ollama\" does not report model \"gpt-5.4-mini\".",
			OperatorAction:        "Pull or load the model locally.",
			RoutingReady:          true,
			ProviderStatus:        "healthy",
			ProviderBlockedReason: "",
			SuggestedModels:       []string{"llama3.1:8b"},
		},
	}))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}
	var payload struct {
		Error struct {
			Type            string   `json:"type"`
			UserMessage     string   `json:"user_message"`
			OperatorAction  string   `json:"operator_action"`
			Reason          string   `json:"reason"`
			SuggestedModels []string `json:"suggested_models"`
		} `json:"error"`
	}
	payload = decodeRecorder[struct {
		Error struct {
			Type            string   `json:"type"`
			UserMessage     string   `json:"user_message"`
			OperatorAction  string   `json:"operator_action"`
			Reason          string   `json:"reason"`
			SuggestedModels []string `json:"suggested_models"`
		} `json:"error"`
	}](t, rec)
	if payload.Error.Type != errCodeModelNotConfigured {
		t.Fatalf("error.type = %q, want %q", payload.Error.Type, errCodeModelNotConfigured)
	}
	if payload.Error.OperatorAction != "Pull or load the model locally." {
		t.Fatalf("operator_action = %q", payload.Error.OperatorAction)
	}
	if payload.Error.Reason != "model_not_discovered" {
		t.Fatalf("error.reason = %q, want model_not_discovered", payload.Error.Reason)
	}
	if len(payload.Error.SuggestedModels) != 1 || payload.Error.SuggestedModels[0] != "llama3.1:8b" {
		t.Fatalf("error.suggested_models = %#v, want llama3.1:8b", payload.Error.SuggestedModels)
	}
}

func TestAgentChatStoreAttachReconcilesInterruptedRun(t *testing.T) {
	store := chat.NewMemoryStore()
	ctx := context.Background()
	created, err := store.Create(ctx, chat.Session{
		ID:        "chat_restart",
		Title:     "Restart",
		AgentID:   "codex",
		Workspace: t.TempDir(),
		Status:    "running",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.AppendMessage(ctx, created.ID, chat.Message{
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

	got := mustRequestJSON[ChatSessionResponse](client, http.MethodGet, "/hecate/v1/chat/sessions/"+created.ID, "")
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

func TestAgentChatTurnLimitReturns422(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := config.Config{Server: config.ServerConfig{ChatMaxTurnsPerSession: 2}}
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, cfg, nil)
	apiHandler.SetAgentChatRunner(&fakeAgentChatRunner{output: "done"})
	handler := NewServer(logger, apiHandler)
	client := newAPITestClient(t, handler)

	created := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"codex","workspace":%q}`, dir))
	sessionID := created.Data.ID

	// Two turns should succeed and increment TurnsUsed.
	for i := 0; i < 2; i++ {
		resp := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+sessionID+"/messages", `{"content":"turn"}`)
		if resp.Data.MaxTurnsPerSession != 2 {
			t.Fatalf("turn %d: max_turns_per_session = %d, want 2", i+1, resp.Data.MaxTurnsPerSession)
		}
	}

	// Third turn should return 422.
	rec := client.mustRequestStatus(http.StatusUnprocessableEntity, http.MethodPost, "/hecate/v1/chat/sessions/"+sessionID+"/messages", `{"content":"one too many"}`)
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode 422 body: %v", err)
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj["type"] != errCodeSessionLimitExceeded {
		t.Fatalf("error.type = %v, want %q", errObj["type"], errCodeSessionLimitExceeded)
	}
	if limit, _ := errObj["limit"].(float64); int(limit) != 2 {
		t.Fatalf("error.limit = %v, want 2", errObj["limit"])
	}
	if used, _ := errObj["turns_used"].(float64); int(used) != 2 {
		t.Fatalf("error.turns_used = %v, want 2", errObj["turns_used"])
	}
}

func TestAgentChatTurnsUsedIncrementsAndIsReturned(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := config.Config{Server: config.ServerConfig{ChatMaxTurnsPerSession: 5}}
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, cfg, nil)
	apiHandler.SetAgentChatRunner(&fakeAgentChatRunner{output: "ok"})
	handler := NewServer(logger, apiHandler)
	client := newAPITestClient(t, handler)

	created := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"codex","workspace":%q}`, dir))
	sessionID := created.Data.ID

	for turn := 1; turn <= 3; turn++ {
		resp := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+sessionID+"/messages", `{"content":"hi"}`)
		if resp.Data.TurnsUsed != turn {
			t.Fatalf("after turn %d: turns_used = %d, want %d", turn, resp.Data.TurnsUsed, turn)
		}
	}
}

func TestAgentChatNoLimitWhenMaxTurnsIsZero(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	// Default config: ChatMaxTurnsPerSession = 0 (unlimited).
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatRunner(&fakeAgentChatRunner{output: "ok"})
	handler := NewServer(logger, apiHandler)
	client := newAPITestClient(t, handler)

	created := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"codex","workspace":%q}`, dir))
	sessionID := created.Data.ID

	for i := 0; i < 5; i++ {
		resp := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+sessionID+"/messages", `{"content":"hi"}`)
		if resp.Data.TurnsUsed != i+1 {
			t.Fatalf("turn %d: turns_used = %d, want %d", i+1, resp.Data.TurnsUsed, i+1)
		}
		if resp.Data.MaxTurnsPerSession != 0 {
			t.Fatalf("turn %d: max_turns_per_session = %d, want 0", i+1, resp.Data.MaxTurnsPerSession)
		}
	}
}

func TestAgentChatDurationLimitReturns422(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := config.Config{Server: config.ServerConfig{ChatMaxSessionDuration: time.Hour}}
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, cfg, nil)
	apiHandler.SetAgentChatRunner(&fakeAgentChatRunner{output: "ok"})
	handler := NewServer(logger, apiHandler)
	client := newAPITestClient(t, handler)

	created := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"codex","workspace":%q}`, dir))
	oldStartedAt := time.Now().UTC().Add(-2 * time.Hour)
	if _, err := apiHandler.agentChat.UpdateSession(context.Background(), created.Data.ID, func(item *chat.Session) {
		item.CreatedAt = oldStartedAt
	}); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}

	rec := client.mustRequestStatus(http.StatusUnprocessableEntity, http.MethodPost, "/hecate/v1/chat/sessions/"+created.Data.ID+"/messages", `{"content":"still there?"}`)
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode 422 body: %v", err)
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj["type"] != errCodeSessionDurationLimit {
		t.Fatalf("error.type = %v, want %q", errObj["type"], errCodeSessionDurationLimit)
	}
	if limit, _ := errObj["limit_ms"].(float64); int64(limit) != time.Hour.Milliseconds() {
		t.Fatalf("error.limit_ms = %v, want %d", errObj["limit_ms"], time.Hour.Milliseconds())
	}
	if started, _ := errObj["started_at"].(string); started == "" {
		t.Fatalf("error.started_at = empty, want session start timestamp")
	}
}

func TestAgentChatSnapshotIncludesDurationAndIdleLimits(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := config.Config{Server: config.ServerConfig{
		ChatMaxTurnsPerSession: 10,
		ChatMaxSessionDuration: 2 * time.Hour,
		ChatIdleTimeout:        30 * time.Minute,
	}}
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, cfg, nil)
	apiHandler.SetAgentChatRunner(&fakeAgentChatRunner{output: "ok"})
	client := newAPITestClient(t, NewServer(logger, apiHandler))

	created := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"codex","workspace":%q}`, dir))
	if created.Data.MaxTurnsPerSession != 10 {
		t.Fatalf("max_turns_per_session = %d, want 10", created.Data.MaxTurnsPerSession)
	}
	if created.Data.SessionStartedAt == "" {
		t.Fatalf("session_started_at = empty")
	}
	if created.Data.MaxSessionDurationMS != (2 * time.Hour).Milliseconds() {
		t.Fatalf("max_session_duration_ms = %d, want %d", created.Data.MaxSessionDurationMS, (2 * time.Hour).Milliseconds())
	}
	if created.Data.IdleTimeoutMS != (30 * time.Minute).Milliseconds() {
		t.Fatalf("idle_timeout_ms = %d, want %d", created.Data.IdleTimeoutMS, (30 * time.Minute).Milliseconds())
	}
}

func TestAgentChatIdleLimitReturns422(t *testing.T) {
	store := chat.NewMemoryStore()
	now := time.Now().UTC()
	old := now.Add(-2 * time.Hour)
	workspace := t.TempDir()
	session, err := store.Create(context.Background(), chat.Session{
		ID:        "chat_idle_limit",
		Title:     "Idle limit",
		AgentID:   "codex",
		Workspace: workspace,
		Status:    "completed",
		CreatedAt: old,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := config.Config{Server: config.ServerConfig{ChatIdleTimeout: time.Hour}}
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, cfg, nil)
	apiHandler.SetAgentChatStore(store)
	apiHandler.SetAgentChatRunner(&fakeAgentChatRunner{output: "ok"})
	client := newAPITestClient(t, NewServer(logger, apiHandler))

	rec := client.mustRequestStatus(http.StatusUnprocessableEntity, http.MethodPost, "/hecate/v1/chat/sessions/"+session.ID+"/messages", `{"content":"still there?"}`)
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode 422 body: %v", err)
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj["type"] != errCodeSessionIdleTimeout {
		t.Fatalf("error.type = %v, want %q", errObj["type"], errCodeSessionIdleTimeout)
	}
	if limit, _ := errObj["limit_ms"].(float64); int64(limit) != time.Hour.Milliseconds() {
		t.Fatalf("error.limit_ms = %v, want %d", errObj["limit_ms"], time.Hour.Milliseconds())
	}
	if updated, _ := errObj["updated_at"].(string); updated == "" {
		t.Fatalf("error.updated_at = empty, want last update timestamp")
	}
}

func TestAgentChatIdleSweepCancelsStaleSession(t *testing.T) {
	store := chat.NewMemoryStore()
	now := time.Now().UTC()
	old := now.Add(-2 * time.Hour)
	workspace := t.TempDir()
	session, err := store.Create(context.Background(), chat.Session{
		ID:        "chat_idle",
		Title:     "Idle chat",
		AgentID:   "codex",
		Workspace: workspace,
		Status:    "completed",
		CreatedAt: old,
		Messages: []chat.Message{
			{
				ID:        "msg_assistant",
				Role:      "assistant",
				Content:   "previous answer",
				Status:    "completed",
				CreatedAt: old,
			},
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	runner := &fakeAgentChatRunner{}
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatStore(store)
	apiHandler.SetAgentChatRunner(runner)
	client := newAPITestClient(t, NewServer(logger, apiHandler))

	apiHandler.closeIdleChatSessions(context.Background(), time.Hour, now)

	got := mustRequestJSON[ChatSessionResponse](client, http.MethodGet, "/hecate/v1/chat/sessions/"+session.ID, "")
	if got.Data.Status != "cancelled" {
		t.Fatalf("session status = %q, want cancelled", got.Data.Status)
	}
	if got.Data.DriverKind != "" || got.Data.NativeSessionID != "" {
		t.Fatalf("runtime handles = kind %q native %q, want cleared", got.Data.DriverKind, got.Data.NativeSessionID)
	}
	if len(runner.closedSessions) != 1 || runner.closedSessions[0] != session.ID {
		t.Fatalf("closed sessions = %#v, want %q", runner.closedSessions, session.ID)
	}
	assistant := got.Data.Messages[0]
	if assistant.Status != "cancelled" || assistant.Error != "idle timeout" {
		t.Fatalf("assistant = %#v, want idle-timeout cancellation", assistant)
	}
	if !agentChatActivitiesContain(assistant.Activities, "interrupted") {
		t.Fatalf("activities = %+v, want interrupted activity", assistant.Activities)
	}
}

func TestAgentChatCreateExternalSessionCleansUpPreparedSessionOnPersistFailure(t *testing.T) {
	dir := t.TempDir()
	baseStore := chat.NewMemoryStore()
	store := &failingUpdateSessionStore{Store: baseStore, err: errors.New("update failed")}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	runner := &fakeAgentChatRunner{nativeSessionID: "native_cleanup"}
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatStore(store)
	apiHandler.SetAgentChatRunner(runner)
	client := newAPITestClient(t, NewServer(logger, apiHandler))

	rec := client.mustRequestStatus(http.StatusInternalServerError, http.MethodPost, "/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"codex","workspace":%q}`, dir))
	if !strings.Contains(rec.Body.String(), "update failed") {
		t.Fatalf("response body = %s, want update error", rec.Body.String())
	}
	if len(runner.deletedSessions) != 1 {
		t.Fatalf("deleted sessions = %#v, want one prepared session deleted", runner.deletedSessions)
	}
	if len(store.deletedIDs) != 1 || store.deletedIDs[0] != runner.deletedSessions[0] {
		t.Fatalf("deleted ids = %#v, native deletes = %#v, want persisted session deleted after native delete", store.deletedIDs, runner.deletedSessions)
	}
	if _, ok, err := baseStore.Get(context.Background(), store.deletedIDs[0]); err != nil || ok {
		t.Fatalf("base store Get after cleanup: ok=%v err=%v, want missing", ok, err)
	}
}

type failingUpdateSessionStore struct {
	chat.Store
	err        error
	deletedIDs []string
}

func (s *failingUpdateSessionStore) UpdateSession(context.Context, string, func(*chat.Session)) (chat.Session, error) {
	return chat.Session{}, s.err
}

func (s *failingUpdateSessionStore) Delete(ctx context.Context, id string) error {
	s.deletedIDs = append(s.deletedIDs, id)
	return s.Store.Delete(ctx, id)
}

type fakeAgentChatRunner struct {
	output                 string
	finalOutput            string
	chunks                 []string
	activities             []agentadapters.Activity
	delay                  time.Duration
	waitForCancel          bool
	nativeSessionID        string
	sessionStarted         bool
	sessionResumed         bool
	sessionRecovery        string
	agentInfo              *agentcontrols.ImplementationInfo
	stopReason             string
	seenPreviousID         string
	usage                  agentadapters.Usage
	diffStat               string
	diff                   string
	err                    error
	prepareErr             error
	setConfigErr           error
	prepareRequests        []agentadapters.PrepareSessionRequest
	runRequests            []agentadapters.RunRequest
	prepareDeadline        time.Time
	prepareHasDeadline     bool
	closedSessions         []string
	deletedSessions        []string
	closeErr               error
	configOptions          []agentcontrols.ConfigOption
	availableCommands      []agentcontrols.Command
	availableCommandsKnown bool
	// activitiesAfterCancel are emitted via OnActivity after ctx is
	// cancelled (waitForCancel only), so they sit in the stream
	// coalescer when the run returns and exercise the trailing
	// flush/finalize persistence path under an already-done runCtx.
	activitiesAfterCancel []agentadapters.Activity
}

func (r *fakeAgentChatRunner) PrepareSession(ctx context.Context, req agentadapters.PrepareSessionRequest) (agentadapters.PrepareSessionResult, error) {
	r.prepareRequests = append(r.prepareRequests, req)
	r.prepareDeadline, r.prepareHasDeadline = ctx.Deadline()
	if r.prepareErr != nil {
		return agentadapters.PrepareSessionResult{}, r.prepareErr
	}
	nativeSessionID := r.nativeSessionID
	if nativeSessionID == "" {
		nativeSessionID = "native_" + req.SessionID
	}
	adapter, _ := agentadapters.BuiltInByID(req.AdapterID)
	return agentadapters.PrepareSessionResult{
		Adapter:                adapter,
		DriverKind:             agentadapters.DriverKindACP,
		NativeSessionID:        nativeSessionID,
		AgentInfo:              r.agentInfoResult(),
		SessionStarted:         r.sessionStarted,
		SessionResumed:         r.sessionResumed,
		SessionRecovery:        r.sessionRecovery,
		ConfigOptions:          r.configOptions,
		AvailableCommands:      r.availableCommands,
		AvailableCommandsKnown: r.availableCommandsKnown,
	}, nil
}

func (r *fakeAgentChatRunner) Run(ctx context.Context, req agentadapters.RunRequest) (agentadapters.RunResult, error) {
	started := time.Now().UTC()
	r.runRequests = append(r.runRequests, req)
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
		for _, activity := range r.activitiesAfterCancel {
			if req.OnActivity != nil {
				req.OnActivity(activity)
			}
		}
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
		Adapter:                adapter,
		DriverKind:             agentadapters.DriverKindACP,
		NativeSessionID:        nativeSessionID,
		AgentInfo:              r.agentInfoResult(),
		StopReason:             r.stopReason,
		SessionStarted:         r.sessionStarted,
		SessionResumed:         r.sessionResumed,
		SessionRecovery:        r.sessionRecovery,
		Output:                 output,
		RawOutput:              output,
		ExitCode:               exitCode,
		StartedAt:              started,
		CompletedAt:            time.Now().UTC(),
		DiffStat:               r.diffStat,
		Diff:                   r.diff,
		Usage:                  r.usage,
		ConfigOptions:          r.configOptions,
		AvailableCommands:      r.availableCommands,
		AvailableCommandsKnown: r.availableCommandsKnown,
	}
}

func (r *fakeAgentChatRunner) agentInfoResult() *agentcontrols.ImplementationInfo {
	if r.agentInfo != nil {
		out := *r.agentInfo
		return &out
	}
	return &agentcontrols.ImplementationInfo{
		Name:    "codex-acp-adapter",
		Title:   "Codex ACP Adapter",
		Version: "0.1.0-alpha.28",
	}
}

func (r *fakeAgentChatRunner) SetSessionConfigOption(_ context.Context, req agentadapters.SetSessionConfigOptionRequest) (agentadapters.SetSessionConfigOptionResult, error) {
	if r.setConfigErr != nil {
		return agentadapters.SetSessionConfigOptionResult{}, r.setConfigErr
	}
	options := append([]agentcontrols.ConfigOption(nil), r.configOptions...)
	for i := range options {
		if options[i].ID != req.ConfigID {
			continue
		}
		if req.BoolValue != nil {
			options[i].CurrentBool = req.BoolValue
		} else {
			options[i].CurrentValue = req.Value
		}
	}
	r.configOptions = options
	return agentadapters.SetSessionConfigOptionResult{
		ConfigOptions:          options,
		AvailableCommands:      r.availableCommands,
		AvailableCommandsKnown: r.availableCommandsKnown,
	}, nil
}

func (r *fakeAgentChatRunner) CloseSession(_ context.Context, sessionID string) error {
	r.closedSessions = append(r.closedSessions, sessionID)
	return r.closeErr
}

func (r *fakeAgentChatRunner) DeleteSession(_ context.Context, sessionID string) error {
	r.deletedSessions = append(r.deletedSessions, sessionID)
	return nil
}

func (r *fakeAgentChatRunner) Shutdown(context.Context) error { return nil }

func TestAgentChatAvailableCommandsUpdateHookPersistsAndPublishes(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	session, err := apiHandler.agentChat.Create(context.Background(), chat.Session{
		ID:         "chat_commands",
		Title:      "Codex chat",
		AgentID:    "codex",
		DriverKind: agentadapters.DriverKindACP,
		Workspace:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	updates, unsubscribe := apiHandler.agentChatLive.subscribe(session.ID)
	defer unsubscribe()

	apiHandler.handleAgentChatAvailableCommandsUpdate(agentadapters.AvailableCommandsUpdate{
		SessionID: session.ID,
		AdapterID: "codex",
		Commands: []agentcontrols.Command{
			{Name: "web", Description: "Search the web"},
			{Name: "plan", Description: "Create a plan"},
		},
	})

	got, ok, err := apiHandler.agentChat.Get(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if !ok {
		t.Fatal("session not found")
	}
	if got := got.AvailableCommands; len(got) != 2 || got[0].Name != "web" || got[1].Name != "plan" {
		t.Fatalf("stored commands = %#v, want web and plan", got)
	}
	select {
	case event := <-updates:
		if event.Type != AgentChatLiveEventSessionUpdate || event.SessionUpdate == nil {
			t.Fatalf("event = %#v, want session update", event)
		}
		if got := event.SessionUpdate.Data.AvailableCommands; len(got) != 2 || got[0].Name != "web" || got[1].Name != "plan" {
			t.Fatalf("event commands = %#v, want web and plan", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for session update")
	}
}

func TestAgentChatExternalConfigOptionsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	autoApprove := false
	runner := &fakeAgentChatRunner{
		output: "ok",
		configOptions: []agentcontrols.ConfigOption{
			{
				ID:           "model",
				Name:         "Model",
				Category:     "model",
				Type:         agentcontrols.ConfigOptionTypeSelect,
				CurrentValue: "fast",
				Options: []agentcontrols.ConfigSelectOption{
					{Value: "fast", Name: "Fast"},
					{Value: "smart", Name: "Smart"},
				},
			},
			{
				ID:          "auto_approve",
				Name:        "Auto approve",
				Category:    "mode",
				Type:        agentcontrols.ConfigOptionTypeBoolean,
				CurrentBool: &autoApprove,
			},
		},
		availableCommandsKnown: true,
		availableCommands: []agentcontrols.Command{
			{Name: "web", Description: "Search the web", InputHint: "query"},
			{Name: "plan", Description: "Create a plan"},
		},
	}
	apiHandler.SetAgentChatRunner(runner)
	handler := NewServer(logger, apiHandler)

	created := decodeRecorder[ChatSessionResponse](t, performRequest(t, handler, http.MethodPost, "/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"codex","workspace":%q,"config_options":[{"id":"model","type":"select","current_value":"smart"}],"mcp_servers":[{"name":"weather","url":"https://example.com/mcp","headers":{"Authorization":"$MCP_TOKEN"}}]}`, dir)))
	if len(runner.prepareRequests) != 1 {
		t.Fatalf("prepare requests = %d, want 1", len(runner.prepareRequests))
	}
	gotWorkspace, err := filepath.EvalSymlinks(runner.prepareRequests[0].Workspace)
	if err != nil {
		t.Fatalf("canonicalize prepare workspace: %v", err)
	}
	wantWorkspace, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("canonicalize temp workspace: %v", err)
	}
	if gotWorkspace != wantWorkspace {
		t.Fatalf("prepare workspace = %q, want %q", runner.prepareRequests[0].Workspace, dir)
	}
	if got := runner.prepareRequests[0].AdapterID; got != "codex" {
		t.Fatalf("prepare adapter = %q, want codex", got)
	}
	if got := runner.prepareRequests[0].ConfigOptions; len(got) != 1 || got[0].ID != "model" || got[0].CurrentValue != "smart" {
		t.Fatalf("prepare config options = %#v, want selected draft model", got)
	}
	if got := runner.prepareRequests[0].MCPServers; len(got) != 1 || got[0].Name != "weather" || got[0].URL != "https://example.com/mcp" || got[0].Headers["Authorization"] != "$MCP_TOKEN" {
		t.Fatalf("prepare MCP servers = %#v, want weather server", got)
	}
	if got := created.Data.ConfigOptions; len(got) != 2 || got[0].CurrentValue != "fast" || got[1].CurrentBool == nil || *got[1].CurrentBool {
		t.Fatalf("config options after create = %#v, want fast model and auto_approve false", got)
	}
	if got := created.Data.MCPServers; len(got) != 1 || got[0].Name != "weather" || got[0].Headers["Authorization"] != "$MCP_TOKEN" {
		t.Fatalf("MCP servers after create = %#v, want rendered weather server", got)
	}
	if got := created.Data.AvailableCommands; len(got) != 2 || got[0].Name != "web" || got[0].InputHint != "query" {
		t.Fatalf("available commands after create = %#v, want web and plan", got)
	}
	if created.Data.AgentInfo == nil || created.Data.AgentInfo.Title != "Codex ACP Adapter" || created.Data.AgentInfo.Version != "0.1.0-alpha.28" {
		t.Fatalf("agent info after create = %#v, want prepared adapter metadata", created.Data.AgentInfo)
	}
	afterRun := decodeRecorder[ChatSessionResponse](t, performRequest(t, handler, http.MethodPost, "/hecate/v1/chat/sessions/"+created.Data.ID+"/messages", `{"content":"hello"}`))
	if got := afterRun.Data.ConfigOptions; len(got) != 2 || got[0].CurrentValue != "fast" || got[1].CurrentBool == nil || *got[1].CurrentBool {
		t.Fatalf("config options after run = %#v, want fast model and auto_approve false", got)
	}
	if got := afterRun.Data.AvailableCommands; len(got) != 2 || got[1].Name != "plan" {
		t.Fatalf("available commands after run = %#v, want persisted commands", got)
	}
	if afterRun.Data.AgentInfo == nil || afterRun.Data.AgentInfo.Name != "codex-acp-adapter" {
		t.Fatalf("agent info after run = %#v, want adapter metadata", afterRun.Data.AgentInfo)
	}
	if len(afterRun.Data.Messages) < 2 || afterRun.Data.Messages[1].AgentInfo == nil || afterRun.Data.Messages[1].AgentInfo.Version != "0.1.0-alpha.28" {
		t.Fatalf("assistant message agent info = %#v, want adapter metadata", afterRun.Data.Messages)
	}
	if got := runner.runRequests[0].ConfigOptions; len(got) != 2 || got[0].CurrentValue != "fast" {
		t.Fatalf("run request config options = %#v, want fast model", got)
	}
	if got := runner.runRequests[0].MCPServers; len(got) != 1 || got[0].Name != "weather" || got[0].URL != "https://example.com/mcp" {
		t.Fatalf("run request MCP servers = %#v, want weather server", got)
	}

	updated := decodeRecorder[ChatSessionResponse](t, performRequest(t, handler, http.MethodPost, "/hecate/v1/chat/sessions/"+created.Data.ID+"/config-options/model", `{"value":"smart"}`))
	if got := updated.Data.ConfigOptions; len(got) != 2 || got[0].CurrentValue != "smart" {
		t.Fatalf("config options after select set = %#v, want smart option", got)
	}
	if got := updated.Data.AvailableCommands; len(got) != 2 || got[0].Name != "web" {
		t.Fatalf("available commands after select set = %#v, want preserved commands", got)
	}

	updated = decodeRecorder[ChatSessionResponse](t, performRequest(t, handler, http.MethodPost, "/hecate/v1/chat/sessions/"+created.Data.ID+"/config-options/auto_approve", `{"value":true}`))
	if got := updated.Data.ConfigOptions; len(got) != 2 || got[1].CurrentBool == nil || !*got[1].CurrentBool {
		t.Fatalf("config options after boolean set = %#v, want auto_approve true", got)
	}
	afterSecondRun := decodeRecorder[ChatSessionResponse](t, performRequest(t, handler, http.MethodPost, "/hecate/v1/chat/sessions/"+created.Data.ID+"/messages", `{"content":"again"}`))
	if got := afterSecondRun.Data.ConfigOptions; len(got) != 2 || got[0].CurrentValue != "smart" || got[1].CurrentBool == nil || !*got[1].CurrentBool {
		t.Fatalf("config options after second run = %#v, want smart model and auto_approve true", got)
	}
	if len(runner.runRequests) != 2 {
		t.Fatalf("run requests = %d, want 2", len(runner.runRequests))
	}
	if got := runner.runRequests[1].ConfigOptions; len(got) != 2 || got[0].CurrentValue != "smart" || got[1].CurrentBool == nil || !*got[1].CurrentBool {
		t.Fatalf("second run request config options = %#v, want updated options", got)
	}
	if got := runner.runRequests[1].MCPServers; len(got) != 1 || got[0].Name != "weather" || got[0].Headers["Authorization"] != "$MCP_TOKEN" {
		t.Fatalf("second run request MCP servers = %#v, want persisted weather server", got)
	}
}

func TestAgentChatExternalLaunchModelRequired(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatRunner(&fakeAgentChatRunner{prepareErr: agentadapters.ErrLaunchModelRequired})
	handler := NewServer(logger, apiHandler)

	recorder := performRequest(t, handler, http.MethodPost, "/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"grok_build","workspace":%q,"config_options":[{"id":"model","type":"select","current_value":"__hecate_no_model_selected__"}]}`, dir))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Error struct {
			Type           string `json:"type"`
			UserMessage    string `json:"user_message"`
			OperatorAction string `json:"operator_action"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Type != errCodeModelRequired {
		t.Fatalf("error type = %q, want %q", body.Error.Type, errCodeModelRequired)
	}
	if !strings.Contains(body.Error.UserMessage, "Choose a model") {
		t.Fatalf("user message = %q, want choose-model guidance", body.Error.UserMessage)
	}
}

func TestAgentChatExternalConfigOptionSessionNotActive(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatRunner(&fakeAgentChatRunner{setConfigErr: fmt.Errorf("%w: %q", agentadapters.ErrSessionNotActive, "chat_1")})
	handler := NewServer(logger, apiHandler)

	created := decodeRecorder[ChatSessionResponse](t, performRequest(t, handler, http.MethodPost, "/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"codex","workspace":%q}`, dir)))
	recorder := performRequest(t, handler, http.MethodPost, "/hecate/v1/chat/sessions/"+created.Data.ID+"/config-options/model", `{"value":"fast"}`)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body: %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if payload.Error.Type != errCodeSessionNotRunning {
		t.Fatalf("error type = %q, want %q", payload.Error.Type, errCodeSessionNotRunning)
	}
}

func TestAgentChatExternalStoredConfigOptionUpdatesWhenSessionInactive(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	autoApprove := false
	runner := &fakeAgentChatRunner{
		configOptions: []agentcontrols.ConfigOption{
			{
				ID:           "model",
				Name:         "Model",
				Category:     "model",
				Type:         agentcontrols.ConfigOptionTypeSelect,
				CurrentValue: "fast",
				Options: []agentcontrols.ConfigSelectOption{
					{Value: "fast", Name: "Fast"},
					{Value: "smart", Name: "Smart"},
				},
			},
			{
				ID:          "auto_approve",
				Name:        "Auto approve",
				Category:    "mode",
				Type:        agentcontrols.ConfigOptionTypeBoolean,
				CurrentBool: &autoApprove,
			},
		},
	}
	apiHandler.SetAgentChatRunner(runner)
	handler := NewServer(logger, apiHandler)

	created := decodeRecorder[ChatSessionResponse](t, performRequest(t, handler, http.MethodPost, "/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"codex","workspace":%q}`, dir)))
	runner.setConfigErr = fmt.Errorf("%w: %q", agentadapters.ErrSessionNotActive, created.Data.ID)

	updated := decodeRecorder[ChatSessionResponse](t, performRequest(t, handler, http.MethodPost, "/hecate/v1/chat/sessions/"+created.Data.ID+"/config-options/model", `{"value":"smart"}`))
	if got := updated.Data.ConfigOptions; len(got) != 2 || got[0].CurrentValue != "smart" {
		t.Fatalf("config options after inactive model set = %#v, want smart", got)
	}

	updated = decodeRecorder[ChatSessionResponse](t, performRequest(t, handler, http.MethodPost, "/hecate/v1/chat/sessions/"+created.Data.ID+"/config-options/auto_approve", `{"value":true}`))
	if got := updated.Data.ConfigOptions; len(got) != 2 || got[1].CurrentBool == nil || !*got[1].CurrentBool {
		t.Fatalf("config options after inactive boolean set = %#v, want auto_approve true", got)
	}
}

func TestAgentChatExternalConfigOptionAdapterFailure(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatRunner(&fakeAgentChatRunner{setConfigErr: errors.New("adapter rejected option")})
	handler := NewServer(logger, apiHandler)

	created := decodeRecorder[ChatSessionResponse](t, performRequest(t, handler, http.MethodPost, "/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"codex","workspace":%q}`, dir)))
	recorder := performRequest(t, handler, http.MethodPost, "/hecate/v1/chat/sessions/"+created.Data.ID+"/config-options/model", `{"value":"fast"}`)
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body: %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if payload.Error.Type != errCodeAgentAdapterUnavailable {
		t.Fatalf("error type = %q, want %q", payload.Error.Type, errCodeAgentAdapterUnavailable)
	}
}

func TestAgentChatExternalConfigOptionMissingRunner(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	store := chat.NewMemoryStore()
	now := time.Now().UTC()
	session, err := store.Create(context.Background(), chat.Session{
		ID:        "chat_missing_runner",
		AgentID:   "codex",
		Workspace: dir,
		Status:    "idle",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatStore(store)
	apiHandler.agentChatRunner = nil
	handler := NewServer(logger, apiHandler)

	recorder := performRequest(t, handler, http.MethodPost, "/hecate/v1/chat/sessions/"+session.ID+"/config-options/model", `{"value":"fast"}`)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body: %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if payload.Error.Type != errCodeGatewayError {
		t.Fatalf("error type = %q, want %q", payload.Error.Type, errCodeGatewayError)
	}
	if !strings.Contains(payload.Error.Message, "agent chat runner is not configured") {
		t.Fatalf("error message = %q, want missing runner detail", payload.Error.Message)
	}
}

func TestAgentChatExternalCreateCleansUpWhenPrepareFails(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	store := chat.NewMemoryStore()
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatStore(store)
	apiHandler.SetAgentChatRunner(&fakeAgentChatRunner{prepareErr: errors.New("adapter handshake failed")})
	handler := NewServer(logger, apiHandler)

	recorder := performRequest(t, handler, http.MethodPost, "/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"codex","workspace":%q}`, dir))
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body: %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if payload.Error.Type != errCodeAgentAdapterUnavailable {
		t.Fatalf("error type = %q, want %q", payload.Error.Type, errCodeAgentAdapterUnavailable)
	}
	list, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("sessions after failed prepare = %#v, want none", list)
	}
}

func TestAgentChatExternalCreateMissingRunner(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	store := chat.NewMemoryStore()
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatStore(store)
	apiHandler.agentChatRunner = nil
	handler := NewServer(logger, apiHandler)

	recorder := performRequest(t, handler, http.MethodPost, "/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"codex","workspace":%q}`, dir))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body: %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if payload.Error.Type != errCodeGatewayError {
		t.Fatalf("error type = %q, want %q", payload.Error.Type, errCodeGatewayError)
	}
	if !strings.Contains(payload.Error.Message, "agent chat runner is not configured") {
		t.Fatalf("error message = %q, want missing runner detail", payload.Error.Message)
	}
	list, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("sessions after missing runner = %#v, want none", list)
	}
}

func TestAgentChatExternalCreatePrepareTimeout(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	runner := &fakeAgentChatRunner{prepareErr: context.DeadlineExceeded}
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatRunner(runner)
	handler := NewServer(logger, apiHandler)
	started := time.Now()

	recorder := performRequest(t, handler, http.MethodPost, "/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"codex","workspace":%q}`, dir))
	if recorder.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504; body: %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if payload.Error.Type != errCodeAgentAdapterUnavailable {
		t.Fatalf("error type = %q, want %q", payload.Error.Type, errCodeAgentAdapterUnavailable)
	}
	if !runner.prepareHasDeadline {
		t.Fatal("prepare context did not have a deadline")
	}
	maxDeadline := started.Add(agentChatPrepareTimeout + time.Second)
	if runner.prepareDeadline.After(maxDeadline) {
		t.Fatalf("prepare deadline = %s, want at most %s", runner.prepareDeadline, maxDeadline)
	}
}

func TestChatStreamsExternalAdapterOutput(t *testing.T) {
	dir := t.TempDir()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatRunner(&fakeAgentChatRunner{
		chunks: []string{"first chunk\n", "second chunk\n"},
		delay:  100 * time.Millisecond,
	})
	handler := NewServer(logger, apiHandler)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	created := requestJSONToURL[ChatSessionResponse](t, http.MethodPost, server.URL+"/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"codex","workspace":%q}`, dir))
	streamReq, err := http.NewRequest(http.MethodGet, server.URL+"/hecate/v1/chat/sessions/"+created.Data.ID+"/stream", nil)
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

	done := make(chan ChatSessionResponse, 1)
	go func() {
		done <- requestJSONToURL[ChatSessionResponse](t, http.MethodPost, server.URL+"/hecate/v1/chat/sessions/"+created.Data.ID+"/messages", `{"content":"stream please"}`)
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
			var payload ChatSessionResponse
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
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	apiHandler.SetAgentChatRunner(&fakeAgentChatRunner{waitForCancel: true})
	handler := NewServer(logger, apiHandler)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	created := requestJSONToURL[ChatSessionResponse](t, http.MethodPost, server.URL+"/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"codex","workspace":%q}`, dir))
	done := make(chan ChatSessionResponse, 1)
	go func() {
		done <- requestJSONToURL[ChatSessionResponse](t, http.MethodPost, server.URL+"/hecate/v1/chat/sessions/"+created.Data.ID+"/messages", `{"content":"please wait"}`)
	}()

	waitForAgentChatStatus(t, server.URL, created.Data.ID, "running")
	cancelResp := postJSONToURL(t, server.URL+"/hecate/v1/chat/sessions/"+created.Data.ID+"/cancel", `{}`)
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
		if assistant.Content != "started" {
			t.Fatalf("assistant content = %q, want streamed content preserved after cancellation", assistant.Content)
		}
		if assistant.Error != "" {
			t.Fatalf("assistant error = %q, want empty cancellation detail", assistant.Error)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for cancelled message POST")
	}
}

// cancelAwareChatStore wraps a chat.Store so UpdateMessage fails when
// its context is already cancelled, the way the production SQLite store
// does (it runs the write under ctx). The in-memory test store ignores
// ctx, so without this wrapper a flush under an already-cancelled
// runCtx would silently succeed and hide the persistence-context
// regression this test guards.
type cancelAwareChatStore struct {
	chat.Store
}

func (s cancelAwareChatStore) UpdateMessage(ctx context.Context, sessionID, messageID string, update func(*chat.Message)) (chat.Session, error) {
	if err := ctx.Err(); err != nil {
		return chat.Session{}, err
	}
	return s.Store.UpdateMessage(ctx, sessionID, messageID, update)
}

func TestAgentChatPersistsCoalescedActivityWhenRunCancelled(t *testing.T) {
	dir := t.TempDir()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	// Honor ctx on writes so the post-cancel flush fails on an
	// already-cancelled runCtx, as it would against SQLite.
	apiHandler.SetAgentChatStore(cancelAwareChatStore{Store: chat.NewMemoryStore()})
	apiHandler.SetAgentChatRunner(&fakeAgentChatRunner{
		waitForCancel: true,
		// Emitted after ctx.Done(): the coalescer buffers this activity
		// and only flushes it once the run has returned, when runCtx is
		// already cancelled. It must persist under a context that
		// outlives the run cancel, not be dropped before the finalize
		// (which only appends terminal rows and would not restore it).
		activitiesAfterCancel: []agentadapters.Activity{
			{ID: "tool:held", Type: "tool_call", Status: "completed", Kind: "execute", Title: "held tool"},
		},
	})
	handler := NewServer(logger, apiHandler)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	created := requestJSONToURL[ChatSessionResponse](t, http.MethodPost, server.URL+"/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"codex","workspace":%q}`, dir))
	done := make(chan ChatSessionResponse, 1)
	go func() {
		done <- requestJSONToURL[ChatSessionResponse](t, http.MethodPost, server.URL+"/hecate/v1/chat/sessions/"+created.Data.ID+"/messages", `{"content":"please wait"}`)
	}()

	waitForAgentChatStatus(t, server.URL, created.Data.ID, "running")
	cancelResp := postJSONToURL(t, server.URL+"/hecate/v1/chat/sessions/"+created.Data.ID+"/cancel", `{}`)
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
		found := false
		for _, activity := range assistant.Activities {
			if activity.ID != "tool:held" {
				continue
			}
			found = true
			if activity.Status != "completed" || activity.Title != "held tool" {
				t.Fatalf("held activity = %#v, want completed 'held tool'", activity)
			}
		}
		if !found {
			t.Fatalf("coalesced activity dropped on cancel; activities = %#v", assistant.Activities)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for cancelled message POST")
	}
}

func TestAgentChatDeleteCancelsRunBeforeDeletingSession(t *testing.T) {
	dir := t.TempDir()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	runner := &fakeAgentChatRunner{waitForCancel: true}
	apiHandler.SetAgentChatRunner(runner)
	handler := NewServer(logger, apiHandler)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	created := requestJSONToURL[ChatSessionResponse](t, http.MethodPost, server.URL+"/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"codex","workspace":%q}`, dir))
	done := make(chan ChatSessionResponse, 1)
	go func() {
		done <- requestJSONToURL[ChatSessionResponse](t, http.MethodPost, server.URL+"/hecate/v1/chat/sessions/"+created.Data.ID+"/messages", `{"content":"please wait"}`)
	}()

	waitForAgentChatStatus(t, server.URL, created.Data.ID, "running")
	req, err := http.NewRequest(http.MethodDelete, server.URL+"/hecate/v1/chat/sessions/"+created.Data.ID, nil)
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
	if len(runner.deletedSessions) != 1 || runner.deletedSessions[0] != created.Data.ID {
		t.Fatalf("deleted sessions = %#v, want %q", runner.deletedSessions, created.Data.ID)
	}
	if len(runner.closedSessions) != 0 {
		t.Fatalf("closed sessions = %#v, want delete path not close", runner.closedSessions)
	}
	select {
	case updated := <-done:
		if got := updated.Data.Status; got != "cancelled" {
			t.Fatalf("post status = %q, want cancelled", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for cancelled message POST")
	}
	getResp, err := http.Get(server.URL + "/hecate/v1/chat/sessions/" + created.Data.ID)
	if err != nil {
		t.Fatalf("get deleted session: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted session status = %d, want 404", getResp.StatusCode)
	}
}

func TestAgentChatDeleteClosesIdleExternalSession(t *testing.T) {
	dir := t.TempDir()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	runner := &fakeAgentChatRunner{nativeSessionID: "native_idle_delete", sessionStarted: true}
	apiHandler.SetAgentChatRunner(runner)
	client := newAPITestClient(t, NewServer(logger, apiHandler))

	created := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"codex","workspace":%q}`, dir))
	if created.Data.NativeSessionID != "native_idle_delete" {
		t.Fatalf("native session id = %q, want prepared idle session", created.Data.NativeSessionID)
	}

	client.mustRequestStatus(http.StatusNoContent, http.MethodDelete, "/hecate/v1/chat/sessions/"+created.Data.ID, "")
	if len(runner.deletedSessions) != 1 || runner.deletedSessions[0] != created.Data.ID {
		t.Fatalf("deleted sessions = %#v, want %q", runner.deletedSessions, created.Data.ID)
	}
	if len(runner.closedSessions) != 0 {
		t.Fatalf("closed sessions = %#v, want delete path not close", runner.closedSessions)
	}
	client.mustRequestStatus(http.StatusNotFound, http.MethodGet, "/hecate/v1/chat/sessions/"+created.Data.ID, "")
}

func TestHecateChatDeleteCancelsBackingTaskRun(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	client := newAPITestClient(t, NewServer(logger, apiHandler))
	workspace := t.TempDir()

	created := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		fmt.Sprintf(`{"agent_id":"hecate","workspace":%q,"provider":"openai","model":"gpt-4o-mini"}`, workspace))
	now := time.Now().UTC()
	task, err := apiHandler.taskStore.CreateTask(context.Background(), types.Task{
		ID:                 "task_chat_delete",
		Title:              "Delete backing task",
		Prompt:             "keep running",
		ExecutionKind:      "agent_loop",
		ExecutionProfile:   "chat_agent",
		OriginKind:         "chat",
		OriginID:           created.Data.ID,
		WorkingDirectory:   workspace,
		SandboxAllowedRoot: workspace,
		Status:             "running",
		Priority:           "normal",
		RequestedProvider:  "openai",
		RequestedModel:     "gpt-4o-mini",
		CreatedAt:          now,
		UpdatedAt:          now,
		StartedAt:          now,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	run, err := apiHandler.taskStore.CreateRun(context.Background(), types.TaskRun{
		ID:            "run_chat_delete",
		TaskID:        task.ID,
		Number:        1,
		Status:        "running",
		Model:         "gpt-4o-mini",
		Provider:      "openai",
		WorkspacePath: workspace,
		StartedAt:     now,
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := apiHandler.agentChat.UpdateSession(context.Background(), created.Data.ID, func(item *chat.Session) {
		item.TaskID = task.ID
		item.LatestRunID = run.ID
		item.Status = "running"
	}); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}

	client.mustRequestStatus(http.StatusNoContent, http.MethodDelete, "/hecate/v1/chat/sessions/"+created.Data.ID, "")
	gotRun, found, err := apiHandler.taskStore.GetRun(context.Background(), task.ID, run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if !found || gotRun.Status != "cancelled" {
		t.Fatalf("run = %+v found=%v, want cancelled", gotRun, found)
	}
	if !strings.Contains(gotRun.LastError, "operator") {
		t.Fatalf("run last_error = %q, want operator cancellation reason", gotRun.LastError)
	}
	gotTask, found, err := apiHandler.taskStore.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if !found || gotTask.Status != "cancelled" {
		t.Fatalf("task = %+v found=%v, want cancelled", gotTask, found)
	}
	client.mustRequestStatus(http.StatusNotFound, http.MethodGet, "/hecate/v1/chat/sessions/"+created.Data.ID, "")
}

func TestAgentChatCloseKeepsHistoryAndClosesNativeSession(t *testing.T) {
	dir := t.TempDir()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{&fakeProvider{}}, config.Config{}, nil)
	runner := &fakeAgentChatRunner{output: "done", nativeSessionID: "native_close_1", sessionStarted: true}
	apiHandler.SetAgentChatRunner(runner)
	client := newAPITestClient(t, NewServer(logger, apiHandler))

	created := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions", fmt.Sprintf(`{"agent_id":"codex","workspace":%q}`, dir))
	updated := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+created.Data.ID+"/messages", `{"content":"hello"}`)
	if len(updated.Data.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(updated.Data.Messages))
	}
	closed := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+created.Data.ID+"/close", `{}`)
	if len(runner.closedSessions) != 1 || runner.closedSessions[0] != created.Data.ID {
		t.Fatalf("closed sessions = %#v, want %q", runner.closedSessions, created.Data.ID)
	}
	if len(runner.deletedSessions) != 0 {
		t.Fatalf("deleted sessions = %#v, want close path not delete", runner.deletedSessions)
	}
	if len(closed.Data.Messages) != 2 {
		t.Fatalf("closed session messages = %d, want preserved history", len(closed.Data.Messages))
	}
	if closed.Data.DriverKind != "" || closed.Data.NativeSessionID != "" {
		t.Fatalf("closed session ACP metadata = kind %q native %q, want cleared", closed.Data.DriverKind, closed.Data.NativeSessionID)
	}
	got := mustRequestJSON[ChatSessionResponse](client, http.MethodGet, "/hecate/v1/chat/sessions/"+created.Data.ID, "")
	if len(got.Data.Messages) != 2 {
		t.Fatalf("reloaded messages = %d, want preserved history", len(got.Data.Messages))
	}
	if got.Data.DriverKind != "" || got.Data.NativeSessionID != "" {
		t.Fatalf("reloaded closed session ACP metadata = kind %q native %q, want cleared", got.Data.DriverKind, got.Data.NativeSessionID)
	}
}

func TestAgentChatLiveCancelRunAndWaitTimesOutUntilRunDone(t *testing.T) {
	live := newAgentChatLive(agentChatSnapshotConfig{})
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

// TestAgentChatLiveCancelReasonForOperatorPath pins the reason
// classification used by the agent-chat-cancelled counter:
// cancelRun and cancelRunAndWait both stamp "operator", and a
// session that never had cancel called against it surfaces empty
// (the handler maps empty -> "request_cancelled").
func TestAgentChatLiveCancelReasonForOperatorPath(t *testing.T) {
	live := newAgentChatLive(agentChatSnapshotConfig{})
	live.registerRun("session_explicit_cancel", func() {})
	if !live.cancelRun("session_explicit_cancel") {
		t.Fatal("cancelRun = false, want true")
	}
	if got := live.cancelReasonFor("session_explicit_cancel"); got != "operator" {
		t.Errorf("cancelReasonFor after cancelRun = %q, want %q", got, "operator")
	}

	live.registerRun("session_wait_cancel", func() {})
	go func() { _ = live.cancelRunAndWait(context.Background(), "session_wait_cancel") }()
	// Wait briefly for cancelRunAndWait to mark the reason; clearRun
	// closes done so the goroutine returns. The reason itself must
	// be set before cancel(), which the live impl does, so a small
	// sleep here is safe.
	time.Sleep(10 * time.Millisecond)
	if got := live.cancelReasonFor("session_wait_cancel"); got != "operator" {
		t.Errorf("cancelReasonFor after cancelRunAndWait = %q, want %q", got, "operator")
	}
	live.clearRun("session_wait_cancel")

	live.registerRun("session_never_cancelled", func() {})
	if got := live.cancelReasonFor("session_never_cancelled"); got != "" {
		t.Errorf("cancelReasonFor on uncancelled session = %q, want empty (handler maps to request_cancelled)", got)
	}

	// Unknown session: empty, not a panic.
	if got := live.cancelReasonFor("session_unknown"); got != "" {
		t.Errorf("cancelReasonFor unknown session = %q, want empty", got)
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

	createdStub := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/hecate/v1/tasks", `{"title":"Stats stub","prompt":"Complete one stub task."}`)
	startedStub := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/hecate/v1/tasks/"+createdStub.Data.ID+"/start", "")
	waitForRunStatus(t, handler, createdStub.Data.ID, startedStub.Data.ID, "completed")

	createdShell := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/hecate/v1/tasks", `{"title":"Stats shell","prompt":"Await approval.","execution_kind":"shell","shell_command":"printf 'ok\n'","working_directory":"."}`)
	startedShell := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/hecate/v1/tasks/"+createdShell.Data.ID+"/start", "")
	if startedShell.Data.Status != "awaiting_approval" {
		t.Fatalf("shell run status = %q, want awaiting_approval", startedShell.Data.Status)
	}

	response := mustRequestJSON[RuntimeStatsResponse](client, http.MethodGet, "/hecate/v1/system/stats", "")
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

	recorder := client.mustRequest(http.MethodGet, "/hecate/v1/system/stats", "")

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

// TestRuntimeStatsAgentAdapterApprovalMode covers the operator-UI
// signal: the configured external-agent approval mode is surfaced on
// /hecate/v1/system/stats so the client can render a danger banner when
// "auto" is set (every adapter call permitted without review).
//
// Asserts both the default (NewHandler folds an empty mode to "prompt"
// at construction) and the explicit "auto" override.
func TestRuntimeStatsAgentAdapterApprovalMode(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	t.Run("defaults to prompt", func(t *testing.T) {
		t.Parallel()
		handler := newTestHTTPHandlerForProviders(logger, nil, config.Config{})
		client := newAPITestClient(t, handler)
		response := mustRequestJSON[RuntimeStatsResponse](client, http.MethodGet, "/hecate/v1/system/stats", "")
		if response.Data.AgentAdapterApprovalMode != "prompt" {
			t.Fatalf("agent_adapter_approval_mode = %q, want prompt", response.Data.AgentAdapterApprovalMode)
		}
	})

	t.Run("surfaces auto when configured", func(t *testing.T) {
		t.Parallel()
		cfg := config.Config{Server: config.ServerConfig{AgentAdapterApprovalMode: "auto"}}
		handler := newTestHTTPHandlerForProviders(logger, nil, cfg)
		client := newAPITestClient(t, handler)
		response := mustRequestJSON[RuntimeStatsResponse](client, http.MethodGet, "/hecate/v1/system/stats", "")
		if response.Data.AgentAdapterApprovalMode != "auto" {
			t.Fatalf("agent_adapter_approval_mode = %q, want auto", response.Data.AgentAdapterApprovalMode)
		}
	})
}

func TestMCPCacheStatsUnconfigured(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	// Handler with no MCP client cache wired → configured=false.
	handler := newTestHTTPHandlerForProviders(logger, nil, config.Config{})
	client := newAPITestClient(t, handler)

	res := mustRequestJSON[MCPCacheStatsResponse](client, http.MethodGet, "/hecate/v1/system/mcp/cache", "")
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

	res := mustRequestJSON[MCPCacheStatsResponse](client, http.MethodGet, "/hecate/v1/system/mcp/cache", "")
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

// TestSystemShutdownReturns503WhenNotWired asserts the endpoint is
// harmless when a Handler is built without SetQuitFunc — the path
// reached by test harnesses and custom embedders. cmd/hecate/main.go
// wires SetQuitFunc unconditionally, so shipped deployments never see
// this 503; the test pins the contract so a refactor doesn't replace
// it with a panic or a silent 200 that does nothing.
func TestSystemShutdownReturns503WhenNotWired(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := newTestHTTPHandlerForProviders(logger, nil, config.Config{})
	client := newAPITestClient(t, handler)

	recorder := client.mustRequestStatus(http.StatusServiceUnavailable, http.MethodPost, "/hecate/v1/system/shutdown", "")
	if !strings.Contains(recorder.Body.String(), "shutdown endpoint not wired") {
		t.Errorf("response body = %q, want a 'not wired' explanation", recorder.Body.String())
	}
}

func TestSystemShutdownRejectsNonLoopbackClients(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	h := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	h.SetQuitFunc(func() {})
	server := NewServer(logger, h)

	req := httptest.NewRequest(http.MethodPost, "/hecate/v1/system/shutdown", nil)
	req.RemoteAddr = "203.0.113.10:1234"
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestMCPProbeRejectsNonLoopbackClientsBeforeCommandHandling(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	h := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	server := NewServer(logger, h)

	req := httptest.NewRequest(http.MethodPost, "/hecate/v1/mcp/probe", strings.NewReader(`{"name":"x","command":"sh","args":["-c","echo hi"]}`))
	req.RemoteAddr = "203.0.113.10:1234"
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestProjectCairnlineSidecarProbeRejectsNonLoopbackClientsBeforeCommandHandling(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	h := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	server := NewServer(logger, h)

	req := httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/cairnline/sidecar-probe", nil)
	req.RemoteAddr = "203.0.113.10:1234"
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestProjectCairnlineSidecarConnectRejectsNonLoopbackClientsBeforeCommandHandling(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	h := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	server := NewServer(logger, h)

	req := httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/cairnline/sidecar-connect", nil)
	req.RemoteAddr = "203.0.113.10:1234"
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestProjectCairnlineSidecarReadSmokeRejectsNonLoopbackClientsBeforeCommandHandling(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	h := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	server := NewServer(logger, h)

	req := httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/cairnline/sidecar-read-smoke", nil)
	req.RemoteAddr = "203.0.113.10:1234"
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestProjectCairnlineSidecarDetailSmokeRejectsNonLoopbackClientsBeforeCommandHandling(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	h := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	server := NewServer(logger, h)

	req := httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/cairnline/sidecar-detail-smoke", nil)
	req.RemoteAddr = "203.0.113.10:1234"
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestProjectCairnlineSidecarCoordinationSmokeRejectsNonLoopbackClientsBeforeCommandHandling(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	h := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	server := NewServer(logger, h)

	req := httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/cairnline/sidecar-coordination-smoke", nil)
	req.RemoteAddr = "203.0.113.10:1234"
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestProjectCairnlineSidecarAssignmentContextSmokeRejectsNonLoopbackClientsBeforeCommandHandling(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	h := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	server := NewServer(logger, h)

	req := httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/cairnline/sidecar-assignment-context-smoke", nil)
	req.RemoteAddr = "203.0.113.10:1234"
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestProjectCairnlineSidecarLaunchPacketSmokeRejectsNonLoopbackClientsBeforeCommandHandling(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	h := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	server := NewServer(logger, h)

	req := httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/cairnline/sidecar-launch-packet-smoke", nil)
	req.RemoteAddr = "203.0.113.10:1234"
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestProjectCairnlineSidecarLifecycleSmokeRejectsNonLoopbackClientsBeforeCommandHandling(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	h := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	server := NewServer(logger, h)

	req := httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/cairnline/sidecar-lifecycle-smoke", nil)
	req.RemoteAddr = "203.0.113.10:1234"
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestMCPRegistryServersDiscoversCustomRegistry(t *testing.T) {
	t.Parallel()

	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0.1/servers" {
			t.Errorf("registry path = %q, want /v0.1/servers", r.URL.Path)
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		if got := r.URL.Query().Get("search"); got != "weather" {
			t.Errorf("registry search = %q, want weather", got)
			http.Error(w, "wrong search", http.StatusBadRequest)
			return
		}
		if got := r.URL.Query().Get("limit"); got != "10" {
			t.Errorf("registry limit = %q, want 10", got)
			http.Error(w, "wrong limit", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"servers":[{
				"server":{
					"name":"io.github/example/weather",
					"title":"Weather",
					"description":"Forecasts",
					"version":"1.0.0",
					"remotes":[{"type":"streamable-http","url":"https://weather.example/mcp","headers":[{"name":"Authorization","isRequired":true,"isSecret":true}]}],
					"packages":[{"registryType":"npm","identifier":"@example/weather","runtimeHint":"npx","transport":{"type":"stdio"}}],
					"_meta":{"publisher":"example"}
				},
				"_meta":{"rank":1}
			}],
			"metadata":{"nextCursor":"next","count":1}
		}`))
	}))
	defer registry.Close()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	server := NewServer(logger, NewHandler(config.Config{}, logger, nil, nil, nil, nil))
	client := newAPITestClient(t, server)

	path := "/hecate/v1/mcp/registry/servers?registry_url=" + url.QueryEscape(registry.URL) + "&search=weather&limit=10"
	res := mustRequestJSON[MCPRegistryServersResponse](client, http.MethodGet, path, "")
	if res.Object != "mcp_registry_servers" {
		t.Fatalf("object = %q, want mcp_registry_servers", res.Object)
	}
	if res.Data.RegistryURL != registry.URL {
		t.Fatalf("registry_url = %q, want %q", res.Data.RegistryURL, registry.URL)
	}
	if res.Data.NextCursor != "next" || res.Data.Count != 1 {
		t.Fatalf("metadata = next_cursor:%q count:%d", res.Data.NextCursor, res.Data.Count)
	}
	if len(res.Data.Servers) != 1 {
		t.Fatalf("servers len = %d, want 1", len(res.Data.Servers))
	}
	item := res.Data.Servers[0]
	if item.Server.Name != "io.github/example/weather" {
		t.Fatalf("server name = %q", item.Server.Name)
	}
	if string(item.Server.Meta) != `{"publisher":"example"}` {
		t.Fatalf("server _meta = %s", item.Server.Meta)
	}
	if string(item.Meta) != `{"rank":1}` {
		t.Fatalf("item _meta = %s", item.Meta)
	}
	if len(item.InstallHints) != 2 {
		t.Fatalf("install_hints len = %d, want 2", len(item.InstallHints))
	}
	remote := item.InstallHints[0]
	if !remote.Supported || remote.HecateConfig == nil {
		t.Fatalf("remote hint = %#v", remote)
	}
	if remote.HecateConfig.URL != "https://weather.example/mcp" {
		t.Fatalf("hecate_config.url = %q", remote.HecateConfig.URL)
	}
	if remote.HecateConfig.Headers["Authorization"] != "$MCP_AUTHORIZATION" {
		t.Fatalf("Authorization header = %q", remote.HecateConfig.Headers["Authorization"])
	}
	if item.InstallHints[1].Supported {
		t.Fatalf("package hint supported = true, want false")
	}
}

func TestMCPRegistryServersRejectsNonLoopbackClients(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	h := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	server := NewServer(logger, h)

	req := httptest.NewRequest(http.MethodGet, "/hecate/v1/mcp/registry/servers?search=weather", nil)
	req.RemoteAddr = "203.0.113.10:1234"
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body=%s", recorder.Code, recorder.Body.String())
	}
}

// TestSystemShutdownTriggersQuitFunc asserts the wired path: the
// endpoint responds 202 and then invokes quitFunc asynchronously. Both
// are important — the 202 lets the desktop app know the request was
// accepted before the HTTP server tears down; the async fire lets the
// response flush before main.go's drain begins.
func TestSystemShutdownTriggersQuitFunc(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	h := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	fired := make(chan struct{}, 1)
	h.SetQuitFunc(func() {
		select {
		case fired <- struct{}{}:
		default:
		}
	})
	server := NewServer(logger, h)
	client := newAPITestClient(t, server)

	recorder := client.mustRequestStatus(http.StatusAccepted, http.MethodPost, "/hecate/v1/system/shutdown", "")
	if !strings.Contains(recorder.Body.String(), `"object":"system_shutdown"`) {
		t.Errorf("response body = %q, want object=system_shutdown", recorder.Body.String())
	}

	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("quitFunc was not invoked within 2s after /system/shutdown returned 202")
	}
}

// TestSystemShutdownDoesNotBlockOnFullQuitChannel asserts the endpoint
// stays non-blocking when the production buffered=1 quit channel is
// already full (i.e. the first signal hasn't been drained yet). The
// Tauri close path can repost — a stuck dialog retry, a slow drain
// poll deciding to send a second nudge — and a blocking handler would
// pin a goroutine indefinitely. We mirror main.go's exact channel
// shape so the test reflects the production wiring.
func TestSystemShutdownDoesNotBlockOnFullQuitChannel(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	h := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	quit := make(chan struct{}, 1)
	h.SetQuitFunc(func() {
		select {
		case quit <- struct{}{}:
		default:
		}
	})
	server := NewServer(logger, h)
	client := newAPITestClient(t, server)

	// First POST: 202 + handler eventually fires quitFunc. We deliberately
	// do NOT drain the channel here so the second POST's send hits a full
	// buffer.
	client.mustRequestStatus(http.StatusAccepted, http.MethodPost, "/hecate/v1/system/shutdown", "")

	// Wait for the async quitFunc fire (it sleeps 50ms before sending)
	// so the channel is observably full before the second POST.
	require := func() {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if len(quit) == 1 {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("first POST: quit channel never reached len=1")
	}
	require()

	// Second POST against the already-full channel: must still return 202
	// without blocking the handler's goroutine. If select-default were
	// missing, the async quitFunc goroutine would park forever; the
	// 202 itself is fine (we return before firing) but the leak would
	// matter.
	client.mustRequestStatus(http.StatusAccepted, http.MethodPost, "/hecate/v1/system/shutdown", "")

	// Settle the async fire and confirm the channel still holds exactly
	// one signal — the second send was dropped, not buffered or blocked.
	time.Sleep(150 * time.Millisecond)
	if got := len(quit); got != 1 {
		t.Fatalf("quit channel length after double-POST = %d, want 1 (second send must be dropped)", got)
	}
}

func TestUsageSummaryReturnsCurrentUsage(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	usageStore := governor.NewMemoryUsageStore()
	if _, err := usageStore.RecordUsage(context.Background(), governor.UsageEvent{UsageKey: "global", CostMicros: 3_000}); err != nil {
		t.Fatalf("RecordUsage() error = %v", err)
	}

	handler := newUsageTestHandler(logger, config.GovernorConfig{
		MaxPromptTokens: 64_000,
		UsageBackend:    "memory",
		UsageKey:        "global",
		UsageScope:      "global",
	}, usageStore)

	client := newAPITestClient(t, handler)
	response := mustRequestJSON[UsageSummaryResponse](client, http.MethodGet, "/hecate/v1/usage/summary", "")
	if response.Object != "usage_summary" {
		t.Fatalf("object = %q, want usage_summary", response.Object)
	}
	if response.Data.Key != "global" {
		t.Fatalf("key = %q, want global", response.Data.Key)
	}
	if response.Data.UsedMicrosUSD != 3_000 {
		t.Fatalf("used_micros_usd = %d, want 3000", response.Data.UsedMicrosUSD)
	}
}

func TestUsageEventsReturnsRecentUsageEvents(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	usageStore := governor.NewMemoryUsageStore()
	now := time.Now().UTC()
	if err := usageStore.AppendEvent(context.Background(), governor.UsageHistoryEvent{
		Key:              "global:tenant:team-a:provider:openai",
		Type:             "usage",
		Scope:            "tenant_provider",
		Provider:         "openai",
		Model:            "gpt-4o-mini",
		RequestID:        "req-newer",
		AmountMicrosUSD:  3200,
		PromptTokens:     12,
		CompletionTokens: 4,
		TotalTokens:      16,
		OccurredAt:       now,
	}); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
	if err := usageStore.AppendEvent(context.Background(), governor.UsageHistoryEvent{
		Key:              "global:tenant:team-b:provider:ollama",
		Type:             "usage",
		Scope:            "tenant_provider",
		Provider:         "ollama",
		Model:            "llama3.1:8b",
		RequestID:        "req-older",
		AmountMicrosUSD:  0,
		PromptTokens:     20,
		CompletionTokens: 5,
		TotalTokens:      25,
		OccurredAt:       now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}

	handler := newUsageTestHandler(logger, config.GovernorConfig{
		MaxPromptTokens: 64_000,
		UsageBackend:    "memory",
		UsageKey:        "global",
		UsageScope:      "global",
	}, usageStore)

	client := newAPITestClient(t, handler)
	response := mustRequestJSON[UsageEventsResponse](client, http.MethodGet, "/hecate/v1/usage/events?limit=1", "")
	if response.Object != "usage_events" {
		t.Fatalf("object = %q, want usage_events", response.Object)
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

func TestUsageEndpointsStayDocumented(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(filepath.Join("..", "..", "docs", "runtime", "runtime-api.md"))
	if err != nil {
		t.Fatalf("read runtime-api docs: %v", err)
	}
	text := string(body)
	for _, want := range []string{
		"GET /hecate/v1/usage/summary",
		"GET /hecate/v1/usage/events",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("docs/runtime/runtime-api.md missing %q", want)
		}
	}
}

func TestTasksCreateListAndGet(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, nil, config.Config{}, nil)
	project, err := apiHandler.projects.Create(context.Background(), projects.Project{
		ID:   "proj_ui",
		Name: "UI",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	handler := NewServer(logger, apiHandler)
	tasks := newTaskTestClient(t, handler)

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/hecate/v1/tasks", `{"title":"Upgrade TypeScript","prompt":"Upgrade the UI workspace to TypeScript 7 beta.","project_id":"proj_ui","repo":"hecate","base_branch":"main","workspace_mode":"ephemeral","requested_model":"gpt-5.4-mini","requested_provider":"openai","budget_micros_usd":500000}`)
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
	if created.Data.ProjectID != project.ID {
		t.Fatalf("project_id = %q, want %q", created.Data.ProjectID, project.ID)
	}

	listed := mustTaskRequestJSON[TasksResponse](tasks, http.MethodGet, "/hecate/v1/tasks?limit=10", "")
	if listed.Object != "tasks" {
		t.Fatalf("object = %q, want tasks", listed.Object)
	}
	if len(listed.Data) != 1 {
		t.Fatalf("tasks = %d, want 1", len(listed.Data))
	}
	if listed.Data[0].ID != created.Data.ID {
		t.Fatalf("listed task id = %q, want %q", listed.Data[0].ID, created.Data.ID)
	}
	if listed.Data[0].ProjectID != project.ID {
		t.Fatalf("listed project_id = %q, want %q", listed.Data[0].ProjectID, project.ID)
	}

	projectListed := mustTaskRequestJSON[TasksResponse](tasks, http.MethodGet, "/hecate/v1/tasks?limit=10&project_id=proj_ui", "")
	if len(projectListed.Data) != 1 || projectListed.Data[0].ID != created.Data.ID {
		t.Fatalf("project-scoped tasks = %#v, want only created task", projectListed.Data)
	}

	unprojectedListed := mustTaskRequestJSON[TasksResponse](tasks, http.MethodGet, "/hecate/v1/tasks?limit=10&project_id=", "")
	if len(unprojectedListed.Data) != 0 {
		t.Fatalf("unprojected task list = %#v, want none", unprojectedListed.Data)
	}

	fetched := mustTaskRequestJSON[TaskResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID, "")
	if fetched.Data.ID != created.Data.ID {
		t.Fatalf("fetched task id = %q, want %q", fetched.Data.ID, created.Data.ID)
	}
	if fetched.Data.Prompt != "Upgrade the UI workspace to TypeScript 7 beta." {
		t.Fatalf("prompt = %q, want original prompt", fetched.Data.Prompt)
	}
	if fetched.Data.ProjectID != project.ID {
		t.Fatalf("fetched project_id = %q, want %q", fetched.Data.ProjectID, project.ID)
	}

	startRecorder := tasks.mustRequest(http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/start", "")
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

	runs := mustTaskRequestJSON[TaskRunsResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/runs", "")
	if len(runs.Data) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs.Data))
	}
	if runs.Data[0].ID != started.Data.ID {
		t.Fatalf("run id = %q, want %q", runs.Data[0].ID, started.Data.ID)
	}

	fetchedRun := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID, "")
	if fetchedRun.Data.ID != started.Data.ID {
		t.Fatalf("fetched run id = %q, want %q", fetchedRun.Data.ID, started.Data.ID)
	}
	if fetchedRun.Data.Status != "completed" {
		t.Fatalf("fetched run status = %q, want completed", fetchedRun.Data.Status)
	}

	steps := mustTaskRequestJSON[TaskStepsResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/steps", "")
	if len(steps.Data) != 1 {
		t.Fatalf("steps = %d, want 1", len(steps.Data))
	}
	if steps.Data[0].Kind != "model" {
		t.Fatalf("step kind = %q, want model", steps.Data[0].Kind)
	}

	step := mustTaskRequestJSON[TaskStepResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/steps/"+steps.Data[0].ID, "")
	if step.Data.ID != steps.Data[0].ID {
		t.Fatalf("step id = %q, want %q", step.Data.ID, steps.Data[0].ID)
	}

	artifacts := mustTaskRequestJSON[TaskArtifactsResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/artifacts", "")
	if len(artifacts.Data) != 1 {
		t.Fatalf("artifacts = %d, want 1", len(artifacts.Data))
	}
	if artifacts.Data[0].Kind != "summary" {
		t.Fatalf("artifact kind = %q, want summary", artifacts.Data[0].Kind)
	}

	runArtifacts := mustTaskRequestJSON[TaskArtifactsResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/artifacts", "")
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

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/hecate/v1/tasks",
		fmt.Sprintf(`{"title":"Lifecycle","prompt":"Pin lifecycle events.","execution_kind":"file","file_operation":"write","file_path":"lifecycle.txt","file_content":"ok","working_directory":%q}`, tempDir))
	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/start", "")
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

	task := mustTaskRequestJSON[TaskResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID, "")
	if task.Data.Status != "completed" {
		t.Fatalf("task status = %q, want completed", task.Data.Status)
	}
	if task.Data.LatestRunID != started.Data.ID {
		t.Fatalf("latest_run_id = %q, want %q", task.Data.LatestRunID, started.Data.ID)
	}

	runTrace := mustRequestJSON[TraceResponse](newAPITestClient(t, handler), http.MethodGet, "/hecate/v1/traces?request_id="+completed.Data.RequestID, "")
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

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/hecate/v1/tasks", `{"title":"Run shell","prompt":"Run a shell command.","execution_kind":"shell","shell_command":"printf 'hello '; printf 'diagnostic\n' >&2; sleep 0.2; printf 'from shell\n'","working_directory":".","timeout_ms":2000}`)
	if created.Data.ExecutionKind != "shell" {
		t.Fatalf("execution_kind = %q, want shell", created.Data.ExecutionKind)
	}

	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/start", "")
	if started.Data.Status != "awaiting_approval" {
		t.Fatalf("run status = %q, want awaiting_approval", started.Data.Status)
	}
	if started.Data.ApprovalCount != 1 {
		t.Fatalf("approval_count = %d, want 1", started.Data.ApprovalCount)
	}

	approvals := mustTaskRequestJSON[TaskApprovalsResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/approvals", "")
	if len(approvals.Data) != 1 {
		t.Fatalf("approvals = %d, want 1", len(approvals.Data))
	}
	if approvals.Data[0].Status != "pending" {
		t.Fatalf("approval status = %q, want pending", approvals.Data[0].Status)
	}
	if approvals.Data[0].Kind != "shell_command" {
		t.Fatalf("approval kind = %q, want shell_command", approvals.Data[0].Kind)
	}

	approval := mustTaskRequestJSON[TaskApprovalResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/approvals/"+approvals.Data[0].ID, "")
	if approval.Data.ID != approvals.Data[0].ID {
		t.Fatalf("approval id = %q, want %q", approval.Data.ID, approvals.Data[0].ID)
	}

	resolved := mustTaskRequestJSON[TaskApprovalResponse](tasks, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/approvals/"+approvals.Data[0].ID+"/resolve", `{"decision":"approve","note":"looks safe"}`)
	if resolved.Data.Status != "approved" {
		t.Fatalf("approval status = %q, want approved", resolved.Data.Status)
	}
	eventsAfterApproval := mustTaskRequestJSON[TaskRunEventsResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/events", "")
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

	steps := mustTaskRequestJSON[TaskStepsResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/steps", "")
	if len(steps.Data) != 1 {
		t.Fatalf("steps = %d, want 1", len(steps.Data))
	}
	if steps.Data[0].Kind != "shell" {
		t.Fatalf("step kind = %q, want shell", steps.Data[0].Kind)
	}
	if steps.Data[0].ExitCode != 0 {
		t.Fatalf("exit_code = %d, want 0", steps.Data[0].ExitCode)
	}

	runArtifacts := mustTaskRequestJSON[TaskArtifactsResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/artifacts", "")
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

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/hecate/v1/tasks", `{"title":"Approve once","prompt":"Resolve one approval once.","execution_kind":"shell","shell_command":"printf 'approved-once\n'","working_directory":".","timeout_ms":2000}`)
	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/start", "")
	if started.Data.Status != "awaiting_approval" {
		t.Fatalf("run status = %q, want awaiting_approval", started.Data.Status)
	}
	approvals := mustTaskRequestJSON[TaskApprovalsResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/approvals", "")
	if len(approvals.Data) != 1 {
		t.Fatalf("approvals = %d, want 1", len(approvals.Data))
	}

	resolved := mustTaskRequestJSON[TaskApprovalResponse](tasks, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/approvals/"+approvals.Data[0].ID+"/resolve", `{"decision":"approve","note":"first approval wins"}`)
	if resolved.Data.Status != "approved" {
		t.Fatalf("approval status = %q, want approved", resolved.Data.Status)
	}

	conflict := tasks.mustRequestStatus(http.StatusConflict, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/approvals/"+approvals.Data[0].ID+"/resolve", `{"decision":"approve","note":"duplicate"}`)
	if !strings.Contains(conflict.Body.String(), "not pending") {
		t.Fatalf("conflict body = %s, want mention of not pending", conflict.Body.String())
	}

	waitForRunStatus(t, handler, created.Data.ID, started.Data.ID, "completed")
	runs := mustTaskRequestJSON[TaskRunsResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/runs", "")
	if len(runs.Data) != 1 {
		t.Fatalf("runs = %d, want 1 (duplicate approval must not create another run)", len(runs.Data))
	}
	runArtifacts := mustTaskRequestJSON[TaskArtifactsResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/artifacts", "")
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

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/hecate/v1/tasks", `{"title":"Reject shell","prompt":"Reject a shell command.","execution_kind":"shell","shell_command":"printf 'should not run\n'","working_directory":".","timeout_ms":2000}`)
	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/start", "")
	if started.Data.Status != "awaiting_approval" {
		t.Fatalf("run status = %q, want awaiting_approval", started.Data.Status)
	}

	approvals := mustTaskRequestJSON[TaskApprovalsResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/approvals", "")
	if len(approvals.Data) != 1 {
		t.Fatalf("approvals = %d, want 1", len(approvals.Data))
	}

	resolveRecorder := tasks.mustRequest(http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/approvals/"+approvals.Data[0].ID+"/resolve", `{"decision":"reject","note":"not safe"}`)
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
	eventsAfterApproval := mustTaskRequestJSON[TaskRunEventsResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/events", "")
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

	steps := mustTaskRequestJSON[TaskStepsResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/steps", "")
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

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/hecate/v1/tasks", fmt.Sprintf(`{"title":"Write file","prompt":"Write a file.","execution_kind":"file","file_operation":"write","file_path":"note.txt","file_content":"hello file","working_directory":%q}`, tempDir))
	if created.Data.ExecutionKind != "file" {
		t.Fatalf("execution_kind = %q, want file", created.Data.ExecutionKind)
	}

	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/start", "")
	if started.Data.Status != "queued" {
		t.Fatalf("run status = %q, want queued", started.Data.Status)
	}
	if started.Data.WorkspacePath == "" {
		t.Fatal("workspace_path is empty")
	}
	waitForRunStatus(t, handler, created.Data.ID, started.Data.ID, "completed")

	steps := mustTaskRequestJSON[TaskStepsResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/steps", "")
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

	artifacts := mustTaskRequestJSON[TaskArtifactsResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/artifacts", "")
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

	patches := mustTaskRequestJSON[TaskPatchesResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/patches", "")
	if len(patches.Data) != 1 {
		t.Fatalf("patches = %d, want 1", len(patches.Data))
	}
	if patches.Data[0].Artifact.ID != patchArtifact.ID {
		t.Fatalf("patch list artifact id = %q, want %q", patches.Data[0].Artifact.ID, patchArtifact.ID)
	}
	if patches.Data[0].Status != "applied" {
		t.Fatalf("patch status = %q, want applied", patches.Data[0].Status)
	}
	fetchedPatch := mustTaskRequestJSON[TaskPatchResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/patches/"+patchArtifact.ID, "")
	if !strings.Contains(fetchedPatch.Data.Diff, "+hello file") {
		t.Fatalf("patch detail missing diff:\n%s", fetchedPatch.Data.Diff)
	}
	tasks.mustRequestStatus(http.StatusConflict, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/patches/"+patchArtifact.ID+"/apply", "")
	reverted := mustTaskRequestJSON[TaskPatchResponse](tasks, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/patches/"+patchArtifact.ID+"/revert", "")
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

func TestTaskRunPatchRevertSucceedsWhenCurrentMatchesAfterContent(t *testing.T) {
	handler, tasks, fixture := newTaskPatchRevertFixture(t, "applied\n", "applied")

	reverted := mustTaskRequestJSON[TaskPatchResponse](tasks, http.MethodPost, fixture.revertPath(), "")
	if reverted.Data.Status != "reverted" {
		t.Fatalf("reverted patch status = %q, want reverted", reverted.Data.Status)
	}
	content := readTaskPatchFixtureFile(t, fixture)
	if string(content) != "original\n" {
		t.Fatalf("file content = %q, want original", string(content))
	}
	revertEvents := waitForRunEvent(t, NewServer(slog.New(slog.NewJSONHandler(io.Discard, nil)), handler), fixture.taskID, fixture.runID, "tool.file.reverted")
	if countTaskRunEvents(revertEvents.Data, "tool.file.reverted") != 1 {
		t.Fatalf("tool.file.reverted event missing: %+v", revertEvents.Data)
	}
}

func TestTaskRunPatchRevertConflictsWhenCurrentContentDiverged(t *testing.T) {
	_, tasks, fixture := newTaskPatchRevertFixture(t, "operator edit\n", "applied")

	tasks.mustRequestStatus(http.StatusConflict, http.MethodPost, fixture.revertPath(), "")
	content := readTaskPatchFixtureFile(t, fixture)
	if string(content) != "operator edit\n" {
		t.Fatalf("file content changed on conflict: %q", string(content))
	}
}

func TestTaskRunPatchRevertConflictsWhenTargetMissing(t *testing.T) {
	_, tasks, fixture := newTaskPatchRevertFixture(t, "", "applied")

	if err := os.Remove(fixture.absPath); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	tasks.mustRequestStatus(http.StatusConflict, http.MethodPost, fixture.revertPath(), "")
	if _, err := os.Stat(fixture.absPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file changed on conflict, stat err=%v", err)
	}
}

func TestTaskRunPatchRevertRepeatedRequestConflictsAndLeavesFile(t *testing.T) {
	_, tasks, fixture := newTaskPatchRevertFixture(t, "applied\n", "applied")

	reverted := mustTaskRequestJSON[TaskPatchResponse](tasks, http.MethodPost, fixture.revertPath(), "")
	if reverted.Data.Status != "reverted" {
		t.Fatalf("reverted patch status = %q, want reverted", reverted.Data.Status)
	}
	tasks.mustRequestStatus(http.StatusConflict, http.MethodPost, fixture.revertPath(), "")
	content := readTaskPatchFixtureFile(t, fixture)
	if string(content) != "original\n" {
		t.Fatalf("file content changed after repeated revert: %q", string(content))
	}
}

func TestTaskRunPatchRevertRestoresDeletedFile(t *testing.T) {
	handler, tasks, fixture := newTaskPatchRevertFixture(t, "", "applied")
	if err := os.Remove(fixture.absPath); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	artifact, found, err := handler.taskStore.GetArtifact(context.Background(), fixture.taskID, fixture.artifactID)
	if err != nil {
		t.Fatalf("GetArtifact() error = %v", err)
	}
	if !found {
		t.Fatal("patch artifact not found")
	}
	artifact.ContentText = strings.Join([]string{
		"--- a/src/app.go",
		"+++ /dev/null",
		"@@ -1,1 +0,0 @@",
		"-original",
		"",
	}, "\n")
	if _, err := handler.taskStore.UpdateArtifact(context.Background(), artifact); err != nil {
		t.Fatalf("UpdateArtifact() error = %v", err)
	}

	reverted := mustTaskRequestJSON[TaskPatchResponse](tasks, http.MethodPost, fixture.revertPath(), "")
	if reverted.Data.Status != "reverted" {
		t.Fatalf("reverted patch status = %q, want reverted", reverted.Data.Status)
	}
	content := readTaskPatchFixtureFile(t, fixture)
	if string(content) != "original\n" {
		t.Fatalf("file content = %q, want original", string(content))
	}
}

func TestTaskPatchRevertRejectsMissingTarget(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := newTestHTTPHandlerForProviders(logger, nil, config.Config{})
	tempDir := t.TempDir()
	tasks := newTaskTestClient(t, handler)

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/hecate/v1/tasks", fmt.Sprintf(`{"title":"Write file","prompt":"Write a file.","execution_kind":"file","file_operation":"write","file_path":"note.txt","file_content":"hello file","working_directory":%q}`, tempDir))
	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/start", "")
	waitForRunStatus(t, handler, created.Data.ID, started.Data.ID, "completed")

	artifacts := mustTaskRequestJSON[TaskArtifactsResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/artifacts", "")
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

	target := filepath.Join(started.Data.WorkspacePath, "note.txt")
	if err := os.Remove(target); err != nil {
		t.Fatalf("remove patch target: %v", err)
	}

	tasks.mustRequestStatus(http.StatusConflict, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/patches/"+patchArtifact.ID+"/revert", "")
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("revert should not recreate missing target, stat err=%v", err)
	}

	fetchedPatch := mustTaskRequestJSON[TaskPatchResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/patches/"+patchArtifact.ID, "")
	if fetchedPatch.Data.Status != "applied" {
		t.Fatalf("patch status = %q, want applied after rejected revert", fetchedPatch.Data.Status)
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

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/hecate/v1/tasks", fmt.Sprintf(`{"title":"Run git","prompt":"Run a git command.","execution_kind":"git","git_command":"status --short","working_directory":%q,"timeout_ms":2000}`, tempDir))
	if created.Data.ExecutionKind != "git" {
		t.Fatalf("execution_kind = %q, want git", created.Data.ExecutionKind)
	}

	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/start", "")
	if started.Data.Status != "queued" {
		t.Fatalf("run status = %q, want queued", started.Data.Status)
	}
	if started.Data.WorkspacePath == "" {
		t.Fatal("workspace_path is empty")
	}
	waitForRunStatus(t, handler, created.Data.ID, started.Data.ID, "completed")

	steps := mustTaskRequestJSON[TaskStepsResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/steps", "")
	if len(steps.Data) != 1 || steps.Data[0].Kind != "git" {
		t.Fatalf("steps = %#v, want one git step", steps.Data)
	}

	artifacts := mustTaskRequestJSON[TaskArtifactsResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/artifacts", "")
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

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/hecate/v1/tasks", `{"title":"Denied shell","prompt":"Attempt a write.","execution_kind":"shell","shell_command":"touch denied.txt","working_directory":".","sandbox_read_only":true,"timeout_ms":2000}`)
	if !created.Data.SandboxReadOnly {
		t.Fatal("sandbox_read_only = false, want true")
	}

	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/start", "")
	if started.Data.Status != "awaiting_approval" {
		t.Fatalf("run status = %q, want awaiting_approval", started.Data.Status)
	}

	approvals := mustTaskRequestJSON[TaskApprovalsResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/approvals", "")
	if len(approvals.Data) != 1 {
		t.Fatalf("approvals = %d, want 1", len(approvals.Data))
	}

	tasks.mustRequest(http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/approvals/"+approvals.Data[0].ID+"/resolve", `{"decision":"approve"}`)

	failedRun := waitForRunStatus(t, handler, created.Data.ID, started.Data.ID, "failed")
	if failedRun.Data.Status != "failed" {
		t.Fatalf("run status = %q, want failed", failedRun.Data.Status)
	}

	steps := mustTaskRequestJSON[TaskStepsResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/steps", "")
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

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/hecate/v1/tasks", fmt.Sprintf(`{"title":"Escape root","prompt":"Try escaping allowed root.","execution_kind":"file","file_operation":"write","file_path":"../outside.txt","file_content":"blocked","working_directory":%q,"sandbox_allowed_root":%q}`, workingDirectory, workingDirectory))
	if created.Data.SandboxAllowedRoot != workingDirectory {
		t.Fatalf("sandbox_allowed_root = %q, want %q", created.Data.SandboxAllowedRoot, workingDirectory)
	}

	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/start", "")
	if started.Data.Status != "queued" {
		t.Fatalf("run status = %q, want queued", started.Data.Status)
	}
	waitForRunStatus(t, handler, created.Data.ID, started.Data.ID, "failed")

	steps := mustTaskRequestJSON[TaskStepsResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/steps", "")
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

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/hecate/v1/tasks", `{"title":"Cancel shell","prompt":"Cancel a long shell run.","execution_kind":"shell","shell_command":"printf 'starting\n'; sleep 5; printf 'done\n'","working_directory":".","timeout_ms":10000}`)
	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/start", "")
	approvals := mustTaskRequestJSON[TaskApprovalsResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/approvals", "")
	tasks.mustRequest(http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/approvals/"+approvals.Data[0].ID+"/resolve", `{"decision":"approve"}`)

	waitForRunStatus(t, handler, created.Data.ID, started.Data.ID, "running")
	waitForRunEvent(t, handler, created.Data.ID, started.Data.ID, "run.started")

	tasks.mustRequest(http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/cancel", "")

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

	again := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/cancel", "")
	if again.Data.Status != "cancelled" {
		t.Fatalf("second cancel status = %q, want cancelled", again.Data.Status)
	}
	afterDuplicate := mustTaskRequestJSON[TaskRunEventsResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/events", "")
	duplicateCancelledCount := countTaskRunEvents(afterDuplicate.Data, "run.cancelled")
	if duplicateCancelledCount != 1 {
		t.Fatalf("run.cancelled event count after duplicate cancel = %d, want 1", duplicateCancelledCount)
	}
}

func TestTaskRunCancellationCancelsPendingApproval(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := config.Config{Server: config.ServerConfig{TaskApprovalPolicies: []string{"shell_exec"}}}
	handler := newTestHTTPHandlerForProviders(logger, nil, cfg)
	tasks := newTaskTestClient(t, handler)

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/hecate/v1/tasks", `{"title":"Cancel approval","prompt":"Cancel before approval.","execution_kind":"shell","shell_command":"printf 'should-not-run\n'","working_directory":".","timeout_ms":1000}`)
	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/start", "")
	if started.Data.Status != "awaiting_approval" {
		t.Fatalf("run status = %q, want awaiting_approval", started.Data.Status)
	}
	approvals := mustTaskRequestJSON[TaskApprovalsResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/approvals", "")
	if len(approvals.Data) != 1 {
		t.Fatalf("approvals = %d, want 1", len(approvals.Data))
	}

	cancelled := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/cancel", `{"reason":"operator stop"}`)
	if cancelled.Data.Status != "cancelled" {
		t.Fatalf("cancelled run status = %q, want cancelled", cancelled.Data.Status)
	}

	afterCancel := mustTaskRequestJSON[TaskApprovalsResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/approvals", "")
	if len(afterCancel.Data) != 1 {
		t.Fatalf("approvals after cancel = %d, want 1", len(afterCancel.Data))
	}
	if afterCancel.Data[0].Status != "cancelled" {
		t.Fatalf("approval status after cancel = %q, want cancelled", afterCancel.Data[0].Status)
	}
	if afterCancel.Data[0].ResolvedBy != "system" {
		t.Fatalf("approval resolved_by = %q, want system", afterCancel.Data[0].ResolvedBy)
	}
	if !strings.Contains(afterCancel.Data[0].ResolutionNote, "operator stop") {
		t.Fatalf("approval resolution_note = %q, want operator stop", afterCancel.Data[0].ResolutionNote)
	}

	staleResolve := tasks.mustRequestStatus(http.StatusConflict, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/approvals/"+approvals.Data[0].ID+"/resolve", `{"decision":"approve"}`)
	if !strings.Contains(staleResolve.Body.String(), "not pending") {
		t.Fatalf("stale resolve body = %s, want mention of not pending", staleResolve.Body.String())
	}

	events := mustTaskRequestJSON[TaskRunEventsResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/events", "")
	assertApprovalResolvedEventBy(t, events.Data, approvals.Data[0].ID, "cancelled", "run cancelled: operator stop", "system")
	task := mustTaskRequestJSON[TaskResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID, "")
	if task.Data.PendingApprovalCount != 0 {
		t.Fatalf("pending approval count = %d, want 0", task.Data.PendingApprovalCount)
	}
}

func TestTaskApprovalResolveReturnsConflictWhenRunNoLongerAwaiting(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, nil, config.Config{}, nil)
	handler := NewServer(logger, apiHandler)
	tasks := newTaskTestClient(t, handler)
	now := time.Now().UTC()
	task := types.Task{
		ID:        "task_stale_approval",
		Title:     "Stale approval",
		Prompt:    "Approval should not resolve after run exits.",
		Status:    "cancelled",
		CreatedAt: now,
		UpdatedAt: now,
	}
	run := types.TaskRun{
		ID:         "run_stale_approval",
		TaskID:     task.ID,
		Number:     1,
		Status:     "cancelled",
		StartedAt:  now,
		FinishedAt: now,
	}
	approval := types.TaskApproval{
		ID:        "approval_stale",
		TaskID:    task.ID,
		RunID:     run.ID,
		Kind:      "shell_exec",
		Status:    "pending",
		Reason:    "legacy pending row",
		CreatedAt: now,
	}
	if _, err := apiHandler.taskStore.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := apiHandler.taskStore.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := apiHandler.taskStore.CreateApproval(context.Background(), approval); err != nil {
		t.Fatalf("CreateApproval: %v", err)
	}

	rec := tasks.mustRequestStatus(http.StatusConflict, http.MethodPost, "/hecate/v1/tasks/"+task.ID+"/approvals/"+approval.ID+"/resolve", `{"decision":"approve"}`)
	if !strings.Contains(rec.Body.String(), "no longer actionable") {
		t.Fatalf("conflict body = %s, want no longer actionable", rec.Body.String())
	}
	got, found, err := apiHandler.taskStore.GetApproval(context.Background(), task.ID, approval.ID)
	if err != nil || !found {
		t.Fatalf("GetApproval: found=%t err=%v", found, err)
	}
	if got.Status != "pending" {
		t.Fatalf("stale approval status = %q, want pending (handler must not mutate)", got.Status)
	}
}

func TestTaskRunStreamSSE(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := config.Config{Server: config.ServerConfig{TaskApprovalPolicies: []string{"shell_exec"}}}
	handler := newTestHTTPHandlerForProviders(logger, nil, cfg)
	server := httptest.NewServer(handler)
	defer server.Close()

	createResp := postJSONToURL(t, server.URL+"/hecate/v1/tasks", `{"title":"Stream shell","prompt":"Stream a shell command.","execution_kind":"shell","shell_command":"printf 'hello '; sleep 0.3; printf 'stream\n'","working_directory":".","timeout_ms":3000}`)
	if createResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(createResp.Body)
		t.Fatalf("create status = %d, want %d, body=%s", createResp.StatusCode, http.StatusOK, string(body))
	}
	var created TaskResponse
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	createResp.Body.Close()

	startResp := postJSONToURL(t, server.URL+"/hecate/v1/tasks/"+created.Data.ID+"/start", "")
	if startResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(startResp.Body)
		t.Fatalf("start status = %d, want %d, body=%s", startResp.StatusCode, http.StatusOK, string(body))
	}
	var started TaskRunResponse
	if err := json.NewDecoder(startResp.Body).Decode(&started); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	startResp.Body.Close()

	approvalsResp, err := http.Get(server.URL + "/hecate/v1/tasks/" + created.Data.ID + "/approvals")
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

	streamReq, err := http.NewRequest(http.MethodGet, server.URL+"/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/stream", nil)
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
		resolveResp, err := http.Post(server.URL+"/hecate/v1/tasks/"+created.Data.ID+"/approvals/"+approvals.Data[0].ID+"/resolve", "application/json", strings.NewReader(`{"decision":"approve"}`))
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
	// /hecate/v1/tasks/{id}/approvals and could drift from the run state —
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

	createResp := postJSONToURL(t, server.URL+"/hecate/v1/tasks", `{"title":"Approval stream","prompt":"Stream test","execution_kind":"shell","shell_command":"echo hi","working_directory":".","timeout_ms":3000}`)
	if createResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(createResp.Body)
		t.Fatalf("create status = %d, body=%s", createResp.StatusCode, string(body))
	}
	var created TaskResponse
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	createResp.Body.Close()

	startResp := postJSONToURL(t, server.URL+"/hecate/v1/tasks/"+created.Data.ID+"/start", "")
	if startResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(startResp.Body)
		t.Fatalf("start status = %d, body=%s", startResp.StatusCode, string(body))
	}
	var started TaskRunResponse
	if err := json.NewDecoder(startResp.Body).Decode(&started); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	startResp.Body.Close()

	streamReq, err := http.NewRequest(http.MethodGet, server.URL+"/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/stream", nil)
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

func TestTaskRunStream_CancelledSnapshotClearsPendingApproval(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := config.Config{Server: config.ServerConfig{TaskApprovalPolicies: []string{"shell_exec"}}}
	handler := newTestHTTPHandlerForProviders(logger, nil, cfg)
	server := httptest.NewServer(handler)
	defer server.Close()

	created := requestJSONToURL[TaskResponse](t, http.MethodPost, server.URL+"/hecate/v1/tasks", `{"title":"Cancel approval stream","prompt":"Stream cancelled approval state.","execution_kind":"shell","shell_command":"echo should-not-run","working_directory":".","timeout_ms":3000}`)
	started := requestJSONToURL[TaskRunResponse](t, http.MethodPost, server.URL+"/hecate/v1/tasks/"+created.Data.ID+"/start", "")
	if started.Data.Status != "awaiting_approval" {
		t.Fatalf("started status = %q, want awaiting_approval", started.Data.Status)
	}

	streamReq, err := http.NewRequest(http.MethodGet, server.URL+"/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/stream", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	streamCtx, cancelStream := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelStream()
	streamReq = streamReq.WithContext(streamCtx)
	streamResp, err := http.DefaultClient.Do(streamReq)
	if err != nil {
		t.Fatalf("stream request: %v", err)
	}
	defer streamResp.Body.Close()
	if got := streamResp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream", got)
	}

	cancelErrCh := make(chan error, 1)
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancelResp, err := http.Post(server.URL+"/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/cancel", "application/json", strings.NewReader(`{"reason":"operator stop"}`))
		if err != nil {
			cancelErrCh <- err
			return
		}
		defer cancelResp.Body.Close()
		io.Copy(io.Discard, cancelResp.Body)
		if cancelResp.StatusCode != http.StatusOK {
			cancelErrCh <- fmt.Errorf("cancel status = %d", cancelResp.StatusCode)
			return
		}
		cancelErrCh <- nil
	}()

	var sawTerminal bool
	for event := range readSSEEvents(t, streamResp.Body) {
		if event.Event != "snapshot" && event.Event != "done" {
			continue
		}
		var payload TaskRunStreamEventResponse
		if err := json.Unmarshal([]byte(event.Data), &payload); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !payload.Data.Terminal && !types.IsTerminalTaskRunStatus(payload.Data.Run.Status) {
			continue
		}
		sawTerminal = true
		if payload.Data.Run.Status != "cancelled" {
			t.Fatalf("terminal run status = %q, want cancelled", payload.Data.Run.Status)
		}
		var sawRunApproval bool
		for _, approval := range payload.Data.Approvals {
			if approval.RunID != started.Data.ID {
				continue
			}
			sawRunApproval = true
			if approval.Status == "pending" {
				t.Fatalf("terminal snapshot carried pending approval %#v", approval)
			}
			if approval.Status != "cancelled" {
				t.Fatalf("approval status in terminal snapshot = %q, want cancelled", approval.Status)
			}
		}
		if !sawRunApproval {
			t.Fatal("terminal snapshot did not carry the cancelled approval")
		}
		break
	}
	cancelStream()

	if !sawTerminal {
		t.Fatal("did not observe terminal stream snapshot")
	}
	if err := <-cancelErrCh; err != nil {
		t.Fatalf("cancel request error = %v", err)
	}
}

func TestTaskRunStream_AgentTurnCompletedFlowsTurnOverlayIntoSnapshot(t *testing.T) {
	// End-to-end check on the Turn overlay path:
	//
	//   1. A `turn.completed` event lands in the run-event log
	//   2. SSE projector reads the event, treats it as Turn-only
	//      (ok=false)
	//   3. Projector preserves the overlay across live-state rebuild
	//   4. Final snapshot carries BOTH the rebuilt Run/Steps/Artifacts
	//      AND the Turn block
	//
	// The unit tests in turn_cost_stream_test.go pin steps 2-3 in
	// isolation. This test pins the wire-up: a regression that, say,
	// rebuilt live state without preserving overlayTurn
	// would silently swallow per-turn cost on the SSE feed without
	// any unit test failing. We POST the event via the public
	// /events endpoint so we don't need a real LLM.
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, nil, config.Config{}, nil)
	handler := NewServer(logger, apiHandler)
	server := httptest.NewServer(handler)
	defer server.Close()

	ctx := context.Background()
	now := time.Now().UTC()
	taskID := "task-turn-overlay"
	runID := "run-turn-overlay"
	if _, err := apiHandler.taskStore.CreateTask(ctx, types.Task{
		ID:          taskID,
		Title:       "Turn overlay",
		Prompt:      "Test turn overlay flow",
		Status:      "running",
		LatestRunID: runID,
		CreatedAt:   now,
		UpdatedAt:   now,
		StartedAt:   now,
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := apiHandler.taskStore.CreateRun(ctx, types.TaskRun{
		ID:        runID,
		TaskID:    taskID,
		Number:    1,
		Status:    "running",
		StartedAt: now,
	}); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

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
	eventResp := postJSONToURL(t, server.URL+"/hecate/v1/tasks/"+taskID+"/runs/"+runID+"/events", eventBody)
	if eventResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(eventResp.Body)
		t.Fatalf("post event status = %d, body=%s", eventResp.StatusCode, string(body))
	}
	eventResp.Body.Close()

	streamReq, err := http.NewRequest(http.MethodGet, server.URL+"/hecate/v1/tasks/"+taskID+"/runs/"+runID+"/stream", nil)
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
		//      AFTER the projector rebuilt full state —
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
		if payload.Data.Run.ID != runID {
			t.Errorf("Run.ID = %q, want %q (overlay should merge AFTER full state rebuild, not replace it)", payload.Data.Run.ID, runID)
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

	createResp := postJSONToURL(t, server.URL+"/hecate/v1/tasks", `{"title":"Resume stream","prompt":"Create resumable stream task."}`)
	var created TaskResponse
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	createResp.Body.Close()

	started := mustRequestJSON[TaskRunResponse](newAPITestClient(t, handler), http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/start", "")
	waitForRunStatus(t, handler, created.Data.ID, started.Data.ID, "completed")

	events := mustRequestJSON[TaskRunEventsResponse](newAPITestClient(t, handler), http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/events", "")
	if len(events.Data) == 0 {
		t.Fatal("events = 0, want at least one")
	}
	afterSequence := events.Data[len(events.Data)-1].Sequence

	streamReq, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/hecate/v1/tasks/%s/runs/%s/stream?after_sequence=%d", server.URL, created.Data.ID, started.Data.ID, afterSequence), nil)
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
		if payload.Data.Sequence != int(afterSequence) {
			t.Fatalf("sequence = %d, want projected cursor %d", payload.Data.Sequence, afterSequence)
		}
		if event.ID != strconv.FormatInt(afterSequence, 10) {
			t.Fatalf("SSE id = %q, want %d", event.ID, afterSequence)
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
	afterStream := mustRequestJSON[TaskRunEventsResponse](newAPITestClient(t, handler), http.MethodGet, fmt.Sprintf("/hecate/v1/tasks/%s/runs/%s/events?after_sequence=%d", created.Data.ID, started.Data.ID, afterSequence), "")
	if len(afterStream.Data) != 0 {
		t.Fatalf("stream appended %d run events after resume cursor, want read-only stream", len(afterStream.Data))
	}
}

func TestTaskRunStreamResumeWithLastEventID(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := newTestHTTPHandlerForProviders(logger, nil, config.Config{})
	server := httptest.NewServer(handler)
	defer server.Close()

	createResp := postJSONToURL(t, server.URL+"/hecate/v1/tasks", `{"title":"Resume stream header","prompt":"Use Last-Event-ID."}`)
	var created TaskResponse
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	createResp.Body.Close()

	started := mustRequestJSON[TaskRunResponse](newAPITestClient(t, handler), http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/start", "")
	waitForRunStatus(t, handler, created.Data.ID, started.Data.ID, "completed")

	events := mustRequestJSON[TaskRunEventsResponse](newAPITestClient(t, handler), http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/events", "")
	if len(events.Data) == 0 {
		t.Fatal("events = 0, want at least one")
	}
	last := events.Data[len(events.Data)-1].Sequence

	streamReq, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/hecate/v1/tasks/%s/runs/%s/stream", server.URL, created.Data.ID, started.Data.ID), nil)
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
		if payload.Data.Sequence != int(last) {
			t.Fatalf("sequence = %d, want projected cursor %d", payload.Data.Sequence, last)
		}
		if event.ID != strconv.FormatInt(last, 10) {
			t.Fatalf("SSE id = %q, want %d", event.ID, last)
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
	apiHandler := newTestAPIHandlerWithSettings(logger, nil, config.Config{}, nil)
	handler := NewServer(logger, apiHandler)
	tasks := newTaskTestClient(t, handler)

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/hecate/v1/tasks", `{"title":"Event run","prompt":"Run with events."}`)
	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/start", "")
	waitForRunStatus(t, handler, created.Data.ID, started.Data.ID, "completed")

	initial := mustTaskRequestJSON[TaskRunEventsResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/events", "")
	if len(initial.Data) == 0 {
		t.Fatal("events = 0, want at least one event")
	}
	baseSequence := initial.Data[len(initial.Data)-1].Sequence

	appendRecorder := tasks.mustRequest(
		http.MethodPost,
		"/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/events",
		`{"type":"external.tool_result","step_id":"step_external","status":"ok","note":"client injected event","data":{"tool":"lint","result":"ok"}}`,
	)
	var appended map[string]any
	if err := json.NewDecoder(appendRecorder.Body).Decode(&appended); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	after := mustTaskRequestJSON[TaskRunEventsResponse](tasks, http.MethodGet, fmt.Sprintf("/hecate/v1/tasks/%s/runs/%s/events?after_sequence=%d", created.Data.ID, started.Data.ID, baseSequence), "")
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

	rawEvents, err := apiHandler.taskStore.ListRunEvents(context.Background(), created.Data.ID, started.Data.ID, baseSequence, 10)
	if err != nil {
		t.Fatalf("ListRunEvents() error = %v", err)
	}
	if len(rawEvents) != 1 {
		t.Fatalf("raw events after append = %d, want 1", len(rawEvents))
	}
	snapshot, ok := rawEvents[0].Data["snapshot"].(map[string]any)
	if !ok {
		t.Fatalf("raw appended event snapshot = %#v, want persisted stream snapshot", rawEvents[0].Data["snapshot"])
	}
	run, ok := snapshot["run"].(map[string]any)
	if !ok || run["id"] != started.Data.ID {
		t.Fatalf("raw appended event snapshot run = %#v, want id %q", snapshot["run"], started.Data.ID)
	}
}

func TestTaskRunRetryCreatesNewAttempt(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := newTestHTTPHandlerForProviders(logger, nil, config.Config{})
	tasks := newTaskTestClient(t, handler)

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/hecate/v1/tasks", `{"title":"Retry run","prompt":"Trigger retry flow."}`)
	first := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/start", "")
	waitForRunStatus(t, handler, created.Data.ID, first.Data.ID, "completed")

	retried := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+first.Data.ID+"/retry", `{}`)
	if retried.Data.ID == first.Data.ID {
		t.Fatal("retry run id matches original run id")
	}
	waitForRunStatus(t, handler, created.Data.ID, retried.Data.ID, "completed")

	runs := mustTaskRequestJSON[TaskRunsResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/runs", "")
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

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/hecate/v1/tasks", `{"title":"Active start","prompt":"Leave this run awaiting approval.","execution_kind":"shell","shell_command":"printf 'active\n'","working_directory":".","timeout_ms":1000}`)
	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/start", "")
	if started.Data.Status != "awaiting_approval" {
		t.Fatalf("run status = %q, want awaiting_approval", started.Data.Status)
	}

	rec := tasks.mustRequestStatus(http.StatusConflict, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/start", "")
	if !strings.Contains(rec.Body.String(), "active run") {
		t.Fatalf("error body = %s, want mention of active run", rec.Body.String())
	}
}

func TestTaskStartAndDeleteCheckLatestRunWhenTaskSummaryIsStale(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := config.Config{Server: config.ServerConfig{TaskApprovalPolicies: []string{"shell_exec"}}}
	apiHandler := newTestAPIHandlerWithSettings(logger, nil, cfg, nil)
	handler := NewServer(logger, apiHandler)
	tasks := newTaskTestClient(t, handler)

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/hecate/v1/tasks", `{"title":"Stale summary","prompt":"Leave this run awaiting approval.","execution_kind":"shell","shell_command":"printf 'active\n'","working_directory":".","timeout_ms":1000}`)
	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/start", "")
	if started.Data.Status != "awaiting_approval" {
		t.Fatalf("run status = %q, want awaiting_approval", started.Data.Status)
	}

	staleTask, found, err := apiHandler.taskStore.GetTask(context.Background(), created.Data.ID)
	if err != nil || !found {
		t.Fatalf("GetTask: found=%t err=%v", found, err)
	}
	staleTask.Status = "completed"
	staleTask.FinishedAt = time.Now().UTC()
	if _, err := apiHandler.taskStore.UpdateTask(context.Background(), staleTask); err != nil {
		t.Fatalf("UpdateTask stale summary: %v", err)
	}

	startAgain := tasks.mustRequestStatus(http.StatusConflict, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/start", "")
	if !strings.Contains(startAgain.Body.String(), "active run") {
		t.Fatalf("start error body = %s, want mention of active run", startAgain.Body.String())
	}

	deleteActive := tasks.mustRequestStatus(http.StatusConflict, http.MethodDelete, "/hecate/v1/tasks/"+created.Data.ID, "")
	if !strings.Contains(deleteActive.Body.String(), "active run") {
		t.Fatalf("delete error body = %s, want mention of active run", deleteActive.Body.String())
	}
}

func TestTaskRunnerPreflightBeforeLoadingResources(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, nil, config.Config{}, nil)
	apiHandler.taskRunner = nil
	handler := NewServer(logger, apiHandler)
	tasks := newTaskTestClient(t, handler)

	cases := []struct {
		name string
		path string
		body string
	}{
		{
			name: "start missing task",
			path: "/hecate/v1/tasks/missing/start",
		},
		{
			name: "resolve missing approval",
			path: "/hecate/v1/tasks/missing/approvals/missing/resolve",
			body: `{"decision":"approve"}`,
		},
		{
			name: "cancel missing run",
			path: "/hecate/v1/tasks/missing/runs/missing/cancel",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			rec := tasks.mustRequestStatus(http.StatusBadRequest, http.MethodPost, tc.path, tc.body)
			if !strings.Contains(rec.Body.String(), "task runner is not configured") {
				t.Fatalf("error body = %s, want task runner preflight error", rec.Body.String())
			}
		})
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

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/hecate/v1/tasks", `{"title":"Active retry","prompt":"Leave this run awaiting approval.","execution_kind":"shell","shell_command":"printf 'active\n'","working_directory":".","timeout_ms":1000}`)
	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/start", "")
	if started.Data.Status != "awaiting_approval" {
		t.Fatalf("run status = %q, want awaiting_approval", started.Data.Status)
	}

	rec := tasks.mustRequestStatus(http.StatusConflict, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/retry", `{}`)
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

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/hecate/v1/tasks", `{"title":"Active resume","prompt":"Leave this run awaiting approval.","execution_kind":"shell","shell_command":"printf 'active\n'","working_directory":".","timeout_ms":1000}`)
	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/start", "")
	if started.Data.Status != "awaiting_approval" {
		t.Fatalf("run status = %q, want awaiting_approval", started.Data.Status)
	}

	rec := tasks.mustRequestStatus(http.StatusConflict, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/resume", `{}`)
	if !strings.Contains(rec.Body.String(), "not resumable") {
		t.Fatalf("error body = %s, want mention of not resumable", rec.Body.String())
	}
}

func TestTaskRunMutationsReturnConflictWhenAnotherLatestRunActive(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, nil, config.Config{}, nil)
	handler := NewServer(logger, apiHandler)
	tasks := newTaskTestClient(t, handler)
	now := time.Now().UTC()

	task := types.Task{
		ID:            "task-other-active-run",
		Title:         "other active run",
		Prompt:        "old run should not fork while latest is active",
		ExecutionKind: "agent_loop",
		Status:        "completed", // Deliberately stale; latest run row is authoritative.
		LatestRunID:   "run-active-latest",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if _, err := apiHandler.taskStore.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	oldRun := types.TaskRun{
		ID:         "run-old-terminal",
		TaskID:     task.ID,
		Number:     1,
		Status:     "failed",
		StartedAt:  now.Add(-2 * time.Minute),
		FinishedAt: now.Add(-time.Minute),
	}
	if _, err := apiHandler.taskStore.CreateRun(context.Background(), oldRun); err != nil {
		t.Fatalf("CreateRun(old): %v", err)
	}
	activeRun := types.TaskRun{
		ID:        task.LatestRunID,
		TaskID:    task.ID,
		Number:    2,
		Status:    "awaiting_approval",
		StartedAt: now,
	}
	if _, err := apiHandler.taskStore.CreateRun(context.Background(), activeRun); err != nil {
		t.Fatalf("CreateRun(active): %v", err)
	}

	cases := []struct {
		name string
		path string
		body string
	}{
		{
			name: "retry",
			path: "/hecate/v1/tasks/" + task.ID + "/runs/" + oldRun.ID + "/retry",
			body: `{}`,
		},
		{
			name: "resume",
			path: "/hecate/v1/tasks/" + task.ID + "/runs/" + oldRun.ID + "/resume",
			body: `{}`,
		},
		{
			name: "continue",
			path: "/hecate/v1/tasks/" + task.ID + "/runs/" + oldRun.ID + "/continue",
			body: `{"prompt":"continue anyway"}`,
		},
		{
			name: "retry from turn",
			path: "/hecate/v1/tasks/" + task.ID + "/runs/" + oldRun.ID + "/retry-from-turn",
			body: `{"turn":1}`,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			rec := tasks.mustRequestStatus(http.StatusConflict, http.MethodPost, tc.path, tc.body)
			if !strings.Contains(rec.Body.String(), "another active run") {
				t.Fatalf("error body = %s, want mention of another active run", rec.Body.String())
			}
		})
	}
}

func TestTaskRunRetryFromTurnInvalidTurnReturnsBadRequest(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, nil, config.Config{}, nil)
	apiHandler.taskRunner = nil
	handler := NewServer(logger, apiHandler)
	tasks := newTaskTestClient(t, handler)
	ctx := context.Background()
	now := time.Now().UTC()

	task := types.Task{
		ID:          "task-invalid-turn",
		Title:       "invalid turn",
		Prompt:      "retry should validate turn before runner dispatch",
		Status:      "failed",
		LatestRunID: "run-invalid-turn",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if _, err := apiHandler.taskStore.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	run := types.TaskRun{
		ID:         task.LatestRunID,
		TaskID:     task.ID,
		Number:     1,
		Status:     "failed",
		StartedAt:  now.Add(-time.Minute),
		FinishedAt: now,
	}
	if _, err := apiHandler.taskStore.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	rec := tasks.mustRequestStatus(http.StatusBadRequest, http.MethodPost,
		"/hecate/v1/tasks/"+task.ID+"/runs/"+run.ID+"/retry-from-turn",
		`{"turn":0}`)
	payload := decodeRecorder[struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}](t, rec)
	if payload.Error.Type != errCodeInvalidRequest || payload.Error.Message != "turn must be >= 1" {
		t.Fatalf("error = %#v, want invalid_request turn validation", payload.Error)
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

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/hecate/v1/tasks", `{"title":"Resume shell","prompt":"Resume cancelled shell run.","execution_kind":"shell","shell_command":"printf 'resume'\n","working_directory":".","timeout_ms":1000}`)
	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/start", "")
	approvals := mustTaskRequestJSON[TaskApprovalsResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/approvals", "")
	tasks.mustRequest(http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/approvals/"+approvals.Data[0].ID+"/resolve", `{"decision":"reject","note":"force cancellation for resume test"}`)
	waitForRunStatus(t, handler, created.Data.ID, started.Data.ID, "cancelled")

	resumed := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/resume", `{"reason":"continue after cancellation"}`)
	if resumed.Data.ID == started.Data.ID {
		t.Fatal("resume returned original run id, want new run id")
	}
	if resumed.Data.Status != "awaiting_approval" && resumed.Data.Status != "queued" {
		t.Fatalf("resume status = %q, want awaiting_approval or queued", resumed.Data.Status)
	}
	if started.Data.WorkspacePath != "" && resumed.Data.WorkspacePath != started.Data.WorkspacePath {
		t.Fatalf("resumed workspace path = %q, want %q", resumed.Data.WorkspacePath, started.Data.WorkspacePath)
	}
	events := mustTaskRequestJSON[TaskRunEventsResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+resumed.Data.ID+"/events", "")
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
	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/hecate/v1/tasks",
		`{"title":"Raise ceiling","prompt":"x","execution_kind":"file","file_operation":"write","file_path":"x.txt","file_content":"hi","working_directory":".","sandbox_read_only":true,"budget_micros_usd":100000}`)
	if created.Data.BudgetMicrosUSD != 100000 {
		t.Fatalf("initial budget = %d, want 100000", created.Data.BudgetMicrosUSD)
	}
	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/start", "")
	waitForRunStatus(t, handler, created.Data.ID, started.Data.ID, "failed")

	// Resume with a doubled ceiling.
	resumed := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost,
		"/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/resume",
		`{"budget_micros_usd":200000,"reason":"raise ceiling"}`)
	if resumed.Data.ID == started.Data.ID {
		t.Fatal("resume returned original run id, want new run id")
	}

	// Task ceiling must now reflect the raised value.
	got := mustTaskRequestJSON[TaskResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID, "")
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

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/hecate/v1/tasks",
		`{"title":"Lower ceiling","prompt":"x","execution_kind":"file","file_operation":"write","file_path":"x.txt","file_content":"hi","working_directory":".","sandbox_read_only":true,"budget_micros_usd":500000}`)
	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/start", "")
	waitForRunStatus(t, handler, created.Data.ID, started.Data.ID, "failed")

	rec := tasks.mustRequestStatus(http.StatusBadRequest, http.MethodPost,
		"/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/resume",
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

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/hecate/v1/tasks", `{"title":"Resume checkpoint","prompt":"Resume failed file run.","execution_kind":"file","file_operation":"write","file_path":"checkpoint.txt","file_content":"hello","working_directory":".","sandbox_read_only":true}`)
	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/start", "")
	waitForRunStatus(t, handler, created.Data.ID, started.Data.ID, "failed")

	resumed := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/resume", `{"reason":"continue from latest checkpoint"}`)
	waitForRunStatus(t, handler, created.Data.ID, resumed.Data.ID, "failed")

	steps := mustTaskRequestJSON[TaskStepsResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+resumed.Data.ID+"/steps", "")
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

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/hecate/v1/tasks", `{"title":"Repo local profile","prompt":"Profile defaults","execution_profile":"repo_local"}`)
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

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/hecate/v1/tasks", `{"title":"Coding profile","prompt":"Make a focused code change.","execution_profile":"coding_agent"}`)
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
	// agent_loop is unconditionally available. Without an LLM configured
	// the run still fails, but it does so inside the run with an actionable
	// error step the operator can see in the timeline, not at the queue
	// boundary where the run never even appears.
	//
	// A model must be specified (or a default configured) — the start
	// preflight rejects agent_loop tasks with no model before creating
	// the run. Here we supply a model so the preflight passes and the
	// test exercises the "LLM client not wired" failure path.
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := newTestHTTPHandlerForProviders(logger, nil, config.Config{})
	tasks := newTaskTestClient(t, handler)

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/hecate/v1/tasks", `{"title":"Agent loop no LLM","prompt":"No LLM wired","execution_kind":"agent_loop","requested_model":"gpt-4o-mini"}`)
	// Start succeeds — model is set so the preflight passes.
	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/start", "")
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
	// When no model is configured on the task, starting an agent_loop run should return
	// 422 immediately — no run is created, no tokens are spent.
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	// config.Config{} has DefaultModel == "" — no default configured.
	handler := newTestHTTPHandlerForProviders(logger, nil, config.Config{})
	tasks := newTaskTestClient(t, handler)

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/hecate/v1/tasks", `{"title":"Agent loop no model","prompt":"No model","execution_kind":"agent_loop"}`)

	rec := tasks.mustRequestStatus(http.StatusUnprocessableEntity, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/start", "")
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

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/hecate/v1/tasks", `{"title":"File write","prompt":"Execute file step","execution_kind":"file","execution_profile":"repo_local","file_operation":"write","file_path":"agent-loop.txt","file_content":"hello"}`)
	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/start", "")
	waitForRunStatus(t, handler, created.Data.ID, started.Data.ID, "completed")

	steps := mustTaskRequestJSON[TaskStepsResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/steps", "")
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

	created := mustTaskRequestJSON[TaskResponse](tasks, http.MethodPost, "/hecate/v1/tasks", `{"title":"Artifact fetch","prompt":"Produce an artifact."}`)
	started := mustTaskRequestJSON[TaskRunResponse](tasks, http.MethodPost, "/hecate/v1/tasks/"+created.Data.ID+"/start", "")
	waitForRunStatus(t, handler, created.Data.ID, started.Data.ID, "completed")

	runArtifacts := mustTaskRequestJSON[TaskArtifactsResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/artifacts", "")
	if len(runArtifacts.Data) == 0 {
		t.Fatal("run artifacts = 0, want at least one")
	}
	artifactID := runArtifacts.Data[0].ID
	fetched := mustTaskRequestJSON[TaskArtifactResponse](tasks, http.MethodGet, "/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/artifacts/"+artifactID, "")
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
	after, afterExisted, err := patchAfterContent(diff)
	if err != nil {
		t.Fatalf("patchAfterContent() error = %v", err)
	}
	if !afterExisted {
		t.Fatal("afterExisted = false, want true")
	}
	if after != "package main\n\nfunc main() {}\n" {
		t.Fatalf("after = %q", after)
	}
}

func TestPatchContentMatchesAllowsOnlyFinalNewlineAmbiguity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		current string
		expect  string
		want    bool
	}{
		{name: "exact", current: "hello\n", expect: "hello\n", want: true},
		{name: "expected has final newline current omitted", current: "hello", expect: "hello\n", want: true},
		{name: "current has final newline expected omitted", current: "hello\n", expect: "hello", want: false},
		{name: "different content", current: "hello\n", expect: "goodbye\n", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := patchContentMatches([]byte(tc.current), tc.expect); got != tc.want {
				t.Fatalf("patchContentMatches(%q, %q) = %v, want %v", tc.current, tc.expect, got, tc.want)
			}
		})
	}
}

func TestVerifyPatchApplyPreconditionRejectsDrift(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	fsys, err := workspacefs.New(root)
	if err != nil {
		t.Fatalf("New workspacefs: %v", err)
	}
	if err := os.WriteFile(path, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyPatchApplyPrecondition(fsys, "main.go", "original\n", true); err == nil {
		t.Fatal("verifyPatchApplyPrecondition() error = nil, want conflict")
	}
	if err := os.WriteFile(path, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyPatchApplyPrecondition(fsys, "main.go", "original\n", true); err != nil {
		t.Fatalf("verifyPatchApplyPrecondition() error = %v", err)
	}
	if err := verifyPatchApplyPrecondition(fsys, "main.go", "", false); err == nil {
		t.Fatal("verifyPatchApplyPrecondition(new file) error = nil for existing file, want conflict")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := verifyPatchApplyPrecondition(fsys, "main.go", "", false); err != nil {
		t.Fatalf("verifyPatchApplyPrecondition(new file) error = %v", err)
	}
}

func TestVerifyPatchRevertPreconditionRejectsDrift(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	fsys, err := workspacefs.New(root)
	if err != nil {
		t.Fatalf("New workspacefs: %v", err)
	}
	if err := os.WriteFile(path, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyPatchRevertPrecondition(fsys, "main.go", "applied\n", true); err == nil {
		t.Fatal("verifyPatchRevertPrecondition() error = nil, want conflict")
	}
	if err := os.WriteFile(path, []byte("applied\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyPatchRevertPrecondition(fsys, "main.go", "applied\n", true); err != nil {
		t.Fatalf("verifyPatchRevertPrecondition() error = %v", err)
	}
	if err := verifyPatchRevertPrecondition(fsys, "main.go", "", false); err == nil {
		t.Fatal("verifyPatchRevertPrecondition(deleted file) error = nil for existing file, want conflict")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := verifyPatchRevertPrecondition(fsys, "main.go", "applied\n", true); err == nil {
		t.Fatal("verifyPatchRevertPrecondition(missing file) error = nil, want conflict")
	}
	if err := verifyPatchRevertPrecondition(fsys, "main.go", "", false); err != nil {
		t.Fatalf("verifyPatchRevertPrecondition(deleted file) error = %v", err)
	}
}

type taskPatchRevertFixture struct {
	taskID     string
	runID      string
	artifactID string
	absPath    string
}

func (f taskPatchRevertFixture) revertPath() string {
	return "/hecate/v1/tasks/" + f.taskID + "/runs/" + f.runID + "/patches/" + f.artifactID + "/revert"
}

func newTaskPatchRevertFixture(t *testing.T, currentContent, artifactStatus string) (*Handler, taskTestClient, taskPatchRevertFixture) {
	t.Helper()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := newTestAPIHandlerWithSettings(logger, nil, config.Config{}, nil)
	tasks := newTaskTestClient(t, NewServer(logger, apiHandler))
	workspace := t.TempDir()
	relPath := filepath.Join("src", "app.go")
	absPath := filepath.Join(workspace, relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(absPath, []byte(currentContent), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	now := time.Now().UTC()
	task, err := apiHandler.taskStore.CreateTask(context.Background(), types.Task{
		ID:                 "task_patch_revert",
		Title:              "Patch revert",
		Prompt:             "Patch revert",
		ExecutionKind:      "agent_loop",
		WorkingDirectory:   workspace,
		SandboxAllowedRoot: workspace,
		Status:             "completed",
		Priority:           "normal",
		CreatedAt:          now,
		UpdatedAt:          now,
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	run, err := apiHandler.taskStore.CreateRun(context.Background(), types.TaskRun{
		ID:            "run_patch_revert",
		TaskID:        task.ID,
		Number:        1,
		Status:        "completed",
		WorkspacePath: workspace,
		StartedAt:     now,
		FinishedAt:    now,
	})
	if err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	artifact, err := apiHandler.taskStore.CreateArtifact(context.Background(), types.TaskArtifact{
		ID:       "artifact_patch_revert",
		TaskID:   task.ID,
		RunID:    run.ID,
		Kind:     "patch",
		Name:     "app.go.patch",
		MimeType: "text/x-diff",
		Path:     absPath,
		ContentText: strings.Join([]string{
			"--- a/src/app.go",
			"+++ b/src/app.go",
			"@@ -1,1 +1,1 @@",
			"-original",
			"+applied",
			"",
		}, "\n"),
		Status:    artifactStatus,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateArtifact() error = %v", err)
	}
	return apiHandler, tasks, taskPatchRevertFixture{
		taskID:     task.ID,
		runID:      run.ID,
		artifactID: artifact.ID,
		absPath:    absPath,
	}
}

func readTaskPatchFixtureFile(t *testing.T, fixture taskPatchRevertFixture) []byte {
	t.Helper()
	content, err := os.ReadFile(fixture.absPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	return content
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
		recorder := performRequest(t, handler, http.MethodGet, "/hecate/v1/tasks/"+taskID+"/runs/"+runID, "")
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
		recorder := client.mustRequest(http.MethodGet, "/hecate/v1/tasks/"+taskID+"/runs/"+runID, "")
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
		recorder := performRequest(t, handler, http.MethodGet, "/hecate/v1/tasks/"+taskID, "")
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
		recorder := performRequest(t, handler, http.MethodGet, "/hecate/v1/tasks/"+taskID+"/runs/"+runID+"/artifacts", "")
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
		recorder := performRequest(t, handler, http.MethodGet, "/hecate/v1/tasks/"+taskID+"/runs/"+runID+"/steps", "")
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
		recorder := performRequest(t, handler, http.MethodGet, "/hecate/v1/tasks/"+taskID+"/runs/"+runID+"/events", "")
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
	assertApprovalResolvedEventBy(t, events, approvalID, decision, comment, "operator")
}

func assertApprovalResolvedEventBy(t *testing.T, events []eventprotocol.Envelope, approvalID, decision, comment, by string) {
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
		if event.Data["by"] != by {
			t.Fatalf("approval.resolved by = %v, want %s", event.Data["by"], by)
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
		payload := requestJSONToURL[ChatSessionResponse](t, http.MethodGet, baseURL+"/hecate/v1/chat/sessions/"+sessionID, "")
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
	return newTestHTTPHandlerWithSettings(logger, items, cfg, nil)
}

func newTestHTTPHandlerWithSettings(logger *slog.Logger, items []providers.Provider, cfg config.Config, cpStore controlplane.Store) http.Handler {
	return NewServer(logger, newTestAPIHandlerWithSettings(logger, items, cfg, cpStore))
}

func newTestAPIHandlerWithSettings(logger *slog.Logger, items []providers.Provider, cfg config.Config, cpStore controlplane.Store) *Handler {
	registry := providers.NewRegistry(items...)
	providerHistoryStore := providers.NewMemoryHealthHistoryStore()
	healthTracker := providers.NewMemoryHealthTrackerWithHistory(
		cfg.Provider.HealthThreshold,
		cfg.Provider.HealthCooldown,
		cfg.Provider.HealthLatencyDegradedThreshold,
		providerHistoryStore,
	)
	providerCatalog := catalog.NewRegistryCatalog(registry, healthTracker)
	usageStore := governor.NewMemoryUsageStore()
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
	if retentionCfg.UsageEvents.MaxCount == 0 {
		retentionCfg.UsageEvents = config.RetentionPolicy{MaxAge: 30 * 24 * time.Hour, MaxCount: 200}
	}
	if retentionCfg.AuditEvents.MaxCount == 0 {
		retentionCfg.AuditEvents = config.RetentionPolicy{MaxAge: 30 * 24 * time.Hour, MaxCount: 500}
	}
	retentionManager := retention.NewManager(
		logger,
		retentionCfg,
		profiler.NewInMemoryTracer(nil),
		profiler.NewInMemoryTracer(nil),
		usageStore,
		nil,
		providerHistoryStore,
		nil,
		nil,
		retention.NewMemoryHistoryStore(),
	)
	service := gateway.NewService(gateway.Dependencies{
		Logger: logger,
		Resilience: gateway.ResilienceOptions{
			MaxAttempts:     cfg.Provider.MaxAttempts,
			RetryBackoff:    cfg.Provider.RetryBackoff,
			FailoverEnabled: cfg.Provider.FailoverEnabled,
		},
		Router:          routerEngine,
		Catalog:         providerCatalog,
		Governor:        governor.NewStaticGovernor(governorCfg, usageStore, usageStore),
		Providers:       registry,
		HealthTracker:   healthTracker,
		ProviderHistory: providerHistoryStore,
		Tracer:          profiler.NewInMemoryTracer(nil),
		Metrics:         telemetry.NewMetrics(),
		Retention:       retentionManager,
	})

	cfg.Governor = governorCfg
	handler := NewHandler(cfg, logger, service, cpStore, nil, nil)
	return handler
}

func newUsageTestHandler(logger *slog.Logger, governorCfg config.GovernorConfig, usageStore governor.UsageRepository) http.Handler {
	return newUsageTestHandlerWithConfig(logger, config.Config{Governor: governorCfg}, usageStore, nil)
}

func newUsageTestHandlerWithConfig(logger *slog.Logger, cfg config.Config, usageStore governor.UsageRepository, cpStore controlplane.Store) http.Handler {
	provider := &fakeProvider{name: "openai"}
	registry := providers.NewRegistry(provider)
	providerCatalog := catalog.NewRegistryCatalog(registry, nil)
	governorCfg := mergeGovernorDefaults(cfg.Governor)
	service := gateway.NewService(gateway.Dependencies{
		Logger:    logger,
		Router:    router.NewRuleRouter("gpt-4o-mini", providerCatalog),
		Catalog:   providerCatalog,
		Governor:  governor.NewStaticGovernor(governorCfg, usageStore, usageStore),
		Providers: registry,
		Tracer:    profiler.NewInMemoryTracer(nil),
		Metrics:   telemetry.NewMetrics(),
	})

	handler := NewHandler(cfg, logger, service, cpStore, nil, nil)
	return NewServer(logger, handler)
}

func mergeGovernorDefaults(cfg config.GovernorConfig) config.GovernorConfig {
	if cfg.MaxPromptTokens == 0 {
		cfg.MaxPromptTokens = 64_000
	}
	if cfg.UsageBackend == "" {
		cfg.UsageBackend = "memory"
	}
	if cfg.UsageKey == "" {
		cfg.UsageKey = "global"
	}
	if cfg.UsageScope == "" {
		cfg.UsageScope = "global"
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
	req.RemoteAddr = "127.0.0.1:1234"
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
	responses    []*types.ChatResponse
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
	enabled      *bool
	noDefault    bool
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
	if p.noDefault {
		return ""
	}
	if p.defaultModel != "" {
		return p.defaultModel
	}
	if p.capabilities.DefaultModel != "" {
		return p.capabilities.DefaultModel
	}
	return "gpt-4o-mini"
}

func (p *fakeProvider) Enabled() bool {
	if p.enabled != nil {
		return *p.enabled
	}
	return true
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

	response := p.response
	if len(p.responses) > 0 {
		response = p.responses[0]
		p.responses = p.responses[1:]
	}
	cloned := *response
	cloned.Choices = append([]types.ChatChoice(nil), response.Choices...)
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
