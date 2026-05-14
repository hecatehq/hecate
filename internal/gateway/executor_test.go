package gateway

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/hecate/agent-runtime/internal/config"
	"github.com/hecate/agent-runtime/internal/governor"
	"github.com/hecate/agent-runtime/internal/profiler"
	"github.com/hecate/agent-runtime/internal/providers"
	"github.com/hecate/agent-runtime/pkg/types"
)

type sequenceProvider struct {
	name      string
	kind      providers.Kind
	responses []providerResponse
	callCount int
}

type providerResponse struct {
	response *types.ChatResponse
	err      error
}

func (p *sequenceProvider) Name() string { return p.name }

func (p *sequenceProvider) Kind() providers.Kind { return p.kind }

func (p *sequenceProvider) DefaultModel() string { return "model-a" }

func (p *sequenceProvider) Capabilities(context.Context) (providers.Capabilities, error) {
	return providers.Capabilities{
		Name:         p.name,
		Kind:         p.kind,
		DefaultModel: p.DefaultModel(),
		Models:       []string{"model-a", "model-b"},
	}, nil
}

func (p *sequenceProvider) Chat(_ context.Context, _ types.ChatRequest) (*types.ChatResponse, error) {
	if p.callCount >= len(p.responses) {
		return nil, errors.New("unexpected call")
	}
	item := p.responses[p.callCount]
	p.callCount++
	return item.response, item.err
}

func (p *sequenceProvider) Supports(model string) bool {
	return model == "model-a" || model == "model-b"
}

type staticFallbackRouter struct {
	route     types.RouteDecision
	fallbacks []types.RouteDecision
}

func (r staticFallbackRouter) Route(context.Context, types.ChatRequest) (types.RouteDecision, error) {
	if r.route.Provider != "" {
		return r.route, nil
	}
	return types.RouteDecision{}, errors.New("not used")
}

func (r staticFallbackRouter) Fallbacks(context.Context, types.ChatRequest, types.RouteDecision) []types.RouteDecision {
	return append([]types.RouteDecision(nil), r.fallbacks...)
}

func TestResilientExecutorRetriesRetryableError(t *testing.T) {
	t.Parallel()

	provider := &sequenceProvider{
		name: "openai",
		kind: providers.KindCloud,
		responses: []providerResponse{
			{err: &providers.UpstreamError{StatusCode: http.StatusTooManyRequests}},
			{response: &types.ChatResponse{Model: "model-a"}},
		},
	}
	registry := providers.NewRegistry(provider)
	store := governor.NewMemoryUsageStore()
	preflight := NewDefaultRoutePreflight(
		governor.NewStaticGovernor(config.GovernorConfig{}, store, store),
		registry,
	)
	executor := NewResilientExecutor(
		staticFallbackRouter{},
		preflight,
		registry,
		nil,
		nil,
		nil,
		ResilienceOptions{MaxAttempts: 2, RetryBackoff: time.Millisecond},
	)
	executor.sleep = func(context.Context, time.Duration) error { return nil }

	trace := profiler.NewTrace("req-retry", nil)
	defer trace.Finalize()

	result, err := executor.Execute(context.Background(), trace, types.ChatRequest{
		Model: "model-a",
		Messages: []types.Message{
			{Role: "user", Content: "hello"},
		},
	}, types.RouteDecision{Provider: "openai", Model: "model-a", Reason: "test"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.AttemptCount != 2 {
		t.Fatalf("attempt_count = %d, want 2", result.AttemptCount)
	}
	if result.RetryCount != 1 {
		t.Fatalf("retry_count = %d, want 1", result.RetryCount)
	}
	if provider.callCount != 2 {
		t.Fatalf("provider call_count = %d, want 2", provider.callCount)
	}
}

func TestResilientExecutorFailsOverAfterRetryableFailure(t *testing.T) {
	t.Parallel()

	primary := &sequenceProvider{
		name: "openai",
		kind: providers.KindCloud,
		responses: []providerResponse{
			{err: &providers.UpstreamError{StatusCode: http.StatusServiceUnavailable}},
		},
	}
	fallback := &sequenceProvider{
		name: "ollama",
		kind: providers.KindLocal,
		responses: []providerResponse{
			{response: &types.ChatResponse{Model: "model-b"}},
		},
	}
	registry := providers.NewRegistry(primary, fallback)
	store := governor.NewMemoryUsageStore()
	preflight := NewDefaultRoutePreflight(
		governor.NewStaticGovernor(config.GovernorConfig{}, store, store),
		registry,
	)
	executor := NewResilientExecutor(
		staticFallbackRouter{
			fallbacks: []types.RouteDecision{
				{Provider: "ollama", Model: "model-b", Reason: "test_failover"},
			},
		},
		preflight,
		registry,
		nil,
		nil,
		nil,
		ResilienceOptions{MaxAttempts: 1, RetryBackoff: time.Millisecond, FailoverEnabled: true},
	)
	executor.sleep = func(context.Context, time.Duration) error { return nil }

	trace := profiler.NewTrace("req-failover", nil)
	defer trace.Finalize()

	result, err := executor.Execute(context.Background(), trace, types.ChatRequest{
		Model: "model-a",
		Messages: []types.Message{
			{Role: "user", Content: "hello"},
		},
	}, types.RouteDecision{Provider: "openai", Model: "model-a", Reason: "test"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Decision.Provider != "ollama" {
		t.Fatalf("provider = %q, want ollama", result.Decision.Provider)
	}
	if result.FallbackFromProvider != "openai" {
		t.Fatalf("fallback_from_provider = %q, want openai", result.FallbackFromProvider)
	}
	if primary.callCount != 1 {
		t.Fatalf("primary call_count = %d, want 1", primary.callCount)
	}
	if fallback.callCount != 1 {
		t.Fatalf("fallback call_count = %d, want 1", fallback.callCount)
	}

	report := buildRouteDecisionReport(trace.Spans())
	if len(report.Candidates) < 2 {
		t.Fatalf("candidate count = %d, want at least 2", len(report.Candidates))
	}
	if report.Candidates[0].Outcome != "failed" {
		t.Fatalf("primary outcome = %q, want failed", report.Candidates[0].Outcome)
	}
	if report.Candidates[0].Retryable != true {
		t.Fatalf("primary retryable = false, want true")
	}
	if report.Candidates[1].Outcome != "completed" {
		t.Fatalf("fallback outcome = %q, want completed", report.Candidates[1].Outcome)
	}
	if report.FinalProvider != "ollama" {
		t.Fatalf("final provider = %q, want ollama", report.FinalProvider)
	}
}

func TestClassifyRouteDenied(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "policy", err: errors.New("provider \"openai\" is not allowed by policy"), want: "policy_denied"},
		{name: "generic", err: errors.New("route unavailable"), want: "route_denied"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := classifyRouteDenied(tt.err); got != tt.want {
				t.Fatalf("classifyRouteDenied() = %q, want %q", got, tt.want)
			}
		})
	}
}
