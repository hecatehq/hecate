package gateway

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/governor"
	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/pkg/types"
)

type sequenceProvider struct {
	name      string
	aliases   []string
	kind      providers.Kind
	responses []providerResponse
	callCount int
}

type providerResponse struct {
	response *types.ChatResponse
	err      error
}

func (p *sequenceProvider) Name() string { return p.name }

func (p *sequenceProvider) Aliases() []string { return append([]string(nil), p.aliases...) }

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

type routePreflightFunc func(context.Context, types.ChatRequest, types.RouteDecision) (*RoutePreflightResult, error)

func (f routePreflightFunc) Evaluate(ctx context.Context, req types.ChatRequest, decision types.RouteDecision) (*RoutePreflightResult, error) {
	return f(ctx, req, decision)
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
	providerInstance, _ := registry.GetInstance("openai")
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
		Model:        "model-a",
		Requirements: types.ChatRequestRequirements{NoProviderFailover: true},
		Messages: []types.Message{
			{Role: "user", Content: "hello"},
		},
	}, types.RouteDecision{Provider: "openai", ProviderInstance: providerInstance.Identity, Model: "model-a", Reason: "test"})
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

func TestResilientExecutorDoesNotCrossProviderBoundaryWhenFailoverIsDisabledForRequest(t *testing.T) {
	t.Parallel()

	primary := &sequenceProvider{
		name: "primary",
		kind: providers.KindCloud,
		responses: []providerResponse{
			{err: &providers.UpstreamError{StatusCode: http.StatusServiceUnavailable}},
		},
	}
	fallback := &sequenceProvider{
		name: "fallback",
		kind: providers.KindCloud,
		responses: []providerResponse{
			{response: &types.ChatResponse{Model: "model-b"}},
		},
	}
	registry := providers.NewRegistry(primary, fallback)
	primaryInstance, _ := registry.GetInstance("primary")
	store := governor.NewMemoryUsageStore()
	executor := NewResilientExecutor(
		staticFallbackRouter{fallbacks: []types.RouteDecision{{Provider: "fallback", Model: "model-b", Reason: "test_failover"}}},
		NewDefaultRoutePreflight(governor.NewStaticGovernor(config.GovernorConfig{}, store, store), registry),
		registry,
		nil,
		nil,
		nil,
		ResilienceOptions{MaxAttempts: 1, FailoverEnabled: true},
	)

	trace := profiler.NewTrace("req-provider-boundary", nil)
	defer trace.Finalize()
	_, err := executor.Execute(context.Background(), trace, types.ChatRequest{
		Model:        "model-a",
		Requirements: types.ChatRequestRequirements{ImageInput: true, NoProviderFailover: true},
		Messages:     []types.Message{{Role: "user", Content: "image"}},
	}, types.RouteDecision{Provider: "primary", ProviderInstance: primaryInstance.Identity, Model: "model-a", Reason: "test"})
	if err == nil {
		t.Fatal("Execute() error = nil, want primary failure without cross-provider fallback")
	}
	if primary.callCount != 1 || fallback.callCount != 0 {
		t.Fatalf("provider calls primary=%d fallback=%d, want 1/0", primary.callCount, fallback.callCount)
	}
}

func TestResilientExecutorRejectsSameNameProviderReplacementForProviderBoundRequest(t *testing.T) {
	t.Parallel()

	admitted := &sequenceProvider{name: "vision", kind: providers.KindCloud}
	replacement := &sequenceProvider{
		name:      "vision",
		kind:      providers.KindCloud,
		responses: []providerResponse{{response: &types.ChatResponse{Model: "model-a"}}},
	}
	registry := providers.NewMutableRegistry(admitted)
	admittedInstance, ok := registry.GetInstance("vision")
	if !ok {
		t.Fatal("admitted provider instance not found")
	}
	store := governor.NewMemoryUsageStore()
	executor := NewResilientExecutor(
		staticFallbackRouter{},
		NewDefaultRoutePreflight(governor.NewStaticGovernor(config.GovernorConfig{}, store, store), registry),
		registry,
		nil,
		nil,
		nil,
		ResilienceOptions{MaxAttempts: 1},
	)

	// Recreate the provider under the same routing name after the route has
	// admitted the old instance but before executor dispatch.
	registry.Replace(replacement)
	trace := profiler.NewTrace("req-provider-instance-replaced", nil)
	defer trace.Finalize()
	result, err := executor.Execute(context.Background(), trace, types.ChatRequest{
		Model: "model-a",
		Requirements: types.ChatRequestRequirements{
			NoProviderFailover: true,
			ProviderInstance:   admittedInstance.Identity,
		},
		Messages: []types.Message{{Role: "user", Content: "private image"}},
	}, types.RouteDecision{
		Provider:         "vision",
		ProviderInstance: admittedInstance.Identity,
		Model:            "model-a",
		Reason:           "auto_image",
	})
	if err == nil || !strings.Contains(err.Error(), "changed after bound route admission") {
		t.Fatalf("Execute() error = %v, want provider-instance replacement rejection", err)
	}
	if result != nil {
		t.Fatalf("Execute() result = %+v, want nil before any provider call", result)
	}
	if admitted.callCount != 0 || replacement.callCount != 0 {
		t.Fatalf("provider calls admitted=%d replacement=%d, want no disclosure", admitted.callCount, replacement.callCount)
	}
}

func TestResilientExecutorRejectsImageProviderReplacementDuringPreflight(t *testing.T) {
	t.Parallel()

	admitted := &sequenceProvider{
		name:      "vision",
		kind:      providers.KindCloud,
		responses: []providerResponse{{response: &types.ChatResponse{Model: "model-a"}}},
	}
	replacement := &sequenceProvider{
		name:      "vision",
		kind:      providers.KindCloud,
		responses: []providerResponse{{response: &types.ChatResponse{Model: "model-a"}}},
	}
	registry := providers.NewMutableRegistry(admitted)
	admittedInstance, ok := registry.GetInstance("vision")
	if !ok {
		t.Fatal("admitted provider instance not found")
	}
	store := governor.NewMemoryUsageStore()
	defaultPreflight := NewDefaultRoutePreflight(
		governor.NewStaticGovernor(config.GovernorConfig{}, store, store),
		registry,
	)
	preflight := routePreflightFunc(func(ctx context.Context, req types.ChatRequest, decision types.RouteDecision) (*RoutePreflightResult, error) {
		result, err := defaultPreflight.Evaluate(ctx, req, decision)
		if err == nil {
			registry.Replace(replacement)
		}
		return result, err
	})
	executor := NewResilientExecutor(
		staticFallbackRouter{},
		preflight,
		registry,
		nil,
		nil,
		nil,
		ResilienceOptions{MaxAttempts: 1},
	)

	trace := profiler.NewTrace("req-provider-instance-preflight-replaced", nil)
	defer trace.Finalize()
	result, err := executor.Execute(context.Background(), trace, types.ChatRequest{
		Model: "model-a",
		Requirements: types.ChatRequestRequirements{
			ImageInput:         true,
			NoProviderFailover: true,
			ProviderInstance:   admittedInstance.Identity,
		},
		Messages: []types.Message{{Role: "user", Content: "private image"}},
	}, types.RouteDecision{
		Provider:         "vision",
		ProviderInstance: admittedInstance.Identity,
		Model:            "model-a",
		Reason:           "auto_image",
	})
	if err == nil || !strings.Contains(err.Error(), "changed after bound route admission") {
		t.Fatalf("Execute() error = %v, want replacement during preflight rejection", err)
	}
	if result != nil {
		t.Fatalf("Execute() result = %+v, want nil before any provider call", result)
	}
	if admitted.callCount != 0 || replacement.callCount != 0 {
		t.Fatalf("provider calls admitted=%d replacement=%d, want no disclosure", admitted.callCount, replacement.callCount)
	}
}

func TestResilientExecutorKeepsAdmittedProviderForTextTurn(t *testing.T) {
	t.Parallel()

	admitted := &sequenceProvider{
		name:      "text",
		kind:      providers.KindCloud,
		responses: []providerResponse{{response: &types.ChatResponse{Model: "model-a"}}},
	}
	replacement := &sequenceProvider{
		name:      "text",
		kind:      providers.KindCloud,
		responses: []providerResponse{{response: &types.ChatResponse{Model: "model-a"}}},
	}
	registry := providers.NewMutableRegistry(admitted)
	store := governor.NewMemoryUsageStore()
	defaultPreflight := NewDefaultRoutePreflight(governor.NewStaticGovernor(config.GovernorConfig{}, store, store), registry)
	executor := NewResilientExecutor(
		staticFallbackRouter{},
		routePreflightFunc(func(ctx context.Context, req types.ChatRequest, decision types.RouteDecision) (*RoutePreflightResult, error) {
			result, err := defaultPreflight.Evaluate(ctx, req, decision)
			if err == nil {
				registry.Replace(replacement)
			}
			return result, err
		}),
		registry,
		nil,
		nil,
		nil,
		ResilienceOptions{MaxAttempts: 1},
	)

	trace := profiler.NewTrace("req-text-provider-replaced", nil)
	defer trace.Finalize()
	_, err := executor.Execute(context.Background(), trace, types.ChatRequest{
		Model:    "model-a",
		Messages: []types.Message{{Role: "user", Content: "hello"}},
	}, types.RouteDecision{Provider: "text", Model: "model-a", Reason: "explicit"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if admitted.callCount != 1 || replacement.callCount != 0 {
		t.Fatalf("provider calls admitted=%d replacement=%d, want 1/0", admitted.callCount, replacement.callCount)
	}
}

func TestResilientExecutorRejectsImageProviderReplacementDuringRetryBackoff(t *testing.T) {
	t.Parallel()

	admitted := &sequenceProvider{
		name: "vision",
		kind: providers.KindCloud,
		responses: []providerResponse{
			{err: &providers.UpstreamError{StatusCode: http.StatusServiceUnavailable}},
		},
	}
	replacement := &sequenceProvider{
		name:      "vision",
		kind:      providers.KindCloud,
		responses: []providerResponse{{response: &types.ChatResponse{Model: "model-a"}}},
	}
	registry := providers.NewMutableRegistry(admitted)
	admittedInstance, ok := registry.GetInstance("vision")
	if !ok {
		t.Fatal("admitted provider instance not found")
	}
	store := governor.NewMemoryUsageStore()
	executor := NewResilientExecutor(
		staticFallbackRouter{},
		NewDefaultRoutePreflight(governor.NewStaticGovernor(config.GovernorConfig{}, store, store), registry),
		registry,
		nil,
		nil,
		nil,
		ResilienceOptions{MaxAttempts: 2, RetryBackoff: time.Millisecond},
	)
	executor.sleep = func(context.Context, time.Duration) error {
		registry.Replace(replacement)
		return nil
	}

	trace := profiler.NewTrace("req-provider-instance-backoff-replaced", nil)
	defer trace.Finalize()
	result, err := executor.Execute(context.Background(), trace, types.ChatRequest{
		Model: "model-a",
		Requirements: types.ChatRequestRequirements{
			ImageInput:         true,
			NoProviderFailover: true,
			ProviderInstance:   admittedInstance.Identity,
		},
		Messages: []types.Message{{Role: "user", Content: "private image"}},
	}, types.RouteDecision{
		Provider:         "vision",
		ProviderInstance: admittedInstance.Identity,
		Model:            "model-a",
		Reason:           "auto_image",
	})
	if err == nil || !strings.Contains(err.Error(), "changed after bound route admission") {
		t.Fatalf("Execute() error = %v, want replacement during retry backoff rejection", err)
	}
	if result == nil || result.AttemptCount != 1 || result.RetryCount != 1 {
		t.Fatalf("Execute() result = %+v, want metadata for one provider call and one scheduled retry", result)
	}
	if admitted.callCount != 1 || replacement.callCount != 0 {
		t.Fatalf("provider calls admitted=%d replacement=%d, want 1/0", admitted.callCount, replacement.callCount)
	}
}

func TestResilientExecutorFailsOverAfterRetryableFailure(t *testing.T) {
	t.Parallel()

	imagePayload := strings.Repeat("A", 256)
	primary := &sequenceProvider{
		name: "openai",
		kind: providers.KindCloud,
		responses: []providerResponse{
			{err: &providers.UpstreamError{StatusCode: http.StatusServiceUnavailable, Message: "invalid data:image/png;base64," + imagePayload}},
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
	history := providers.NewMemoryHealthHistoryStore()
	executor := NewResilientExecutor(
		staticFallbackRouter{
			fallbacks: []types.RouteDecision{
				{Provider: "ollama", Model: "model-b", Reason: "test_failover"},
			},
		},
		preflight,
		registry,
		nil,
		history,
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
	records, err := history.List(context.Background(), providers.HealthHistoryFilter{Limit: 10})
	if err != nil || len(records) == 0 {
		t.Fatalf("health history = %+v, err=%v", records, err)
	}
	redactedFailoverError := ""
	for _, record := range records {
		if strings.Contains(record.Error, imagePayload) {
			t.Fatalf("health history persisted inline image payload: %q", record.Error)
		}
		if record.Event == "failover_triggered" {
			redactedFailoverError = record.Error
		}
	}
	if !strings.Contains(redactedFailoverError, "[redacted inline image]") {
		t.Fatalf("failover history error = %q, want redaction marker", redactedFailoverError)
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
