package gateway

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/hecate/agent-runtime/internal/cache"
	"github.com/hecate/agent-runtime/internal/config"
	"github.com/hecate/agent-runtime/internal/governor"
	"github.com/hecate/agent-runtime/internal/profiler"
	"github.com/hecate/agent-runtime/internal/providers"
	"github.com/hecate/agent-runtime/pkg/types"
)

func TestGatewayCacheRuntimeLookupReturnsExactHit(t *testing.T) {
	t.Parallel()

	exact := cache.NewMemoryStore(time.Minute)
	resp := &types.ChatResponse{
		Model: "gpt-4o-mini",
		Route: types.RouteDecision{Provider: "openai", Model: "gpt-4o-mini", Reason: "cached"},
	}
	if err := exact.Set(context.Background(), "cache-key", resp); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	store := governor.NewMemoryBudgetStore()
	runtime := NewGatewayCacheRuntime(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		exact,
		nil,
		SemanticOptions{},
		governor.NewStaticGovernor(config.GovernorConfig{}, store, store),
		providers.NewRegistry(&sequenceProvider{name: "openai", kind: providers.KindCloud}),
	)

	trace := profiler.NewTrace("exact-hit", nil)
	defer trace.Finalize()

	result, ok, err := runtime.Lookup(context.Background(), trace, &ExecutionPlan{
		Request:  types.ChatRequest{Model: "gpt-4o-mini"},
		CacheKey: "cache-key",
	})
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if !ok {
		t.Fatal("Lookup() ok = false, want true")
	}
	if result.CacheType != "exact" {
		t.Fatalf("cache_type = %q, want exact", result.CacheType)
	}
	if result.Route.Provider != "openai" {
		t.Fatalf("provider = %q, want openai", result.Route.Provider)
	}
}

func TestGatewayCacheRuntimeLookupRejectsExactHitWhenRouteDenied(t *testing.T) {
	t.Parallel()

	exact := cache.NewMemoryStore(time.Minute)
	resp := &types.ChatResponse{
		Model: "gpt-4o-mini",
		Route: types.RouteDecision{Provider: "openai", Model: "gpt-4o-mini", Reason: "cached"},
	}
	if err := exact.Set(context.Background(), "cache-key", resp); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	store := governor.NewMemoryBudgetStore()
	runtime := NewGatewayCacheRuntime(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		exact,
		nil,
		SemanticOptions{},
		governor.NewStaticGovernor(config.GovernorConfig{
			DeniedProviders: []string{"openai"},
		}, store, store),
		providers.NewRegistry(&sequenceProvider{name: "openai", kind: providers.KindCloud}),
	)

	trace := profiler.NewTrace("exact-denied", nil)
	defer trace.Finalize()

	_, ok, err := runtime.Lookup(context.Background(), trace, &ExecutionPlan{
		Request:  types.ChatRequest{Model: "gpt-4o-mini"},
		CacheKey: "cache-key",
	})
	if err == nil {
		t.Fatal("Lookup() error = nil, want denial")
	}
	if ok {
		t.Fatal("Lookup() ok = true, want false")
	}
	if !IsDeniedError(err) {
		t.Fatalf("Lookup() error = %v, want denied error", err)
	}
}

func TestGatewayCacheRuntimeLookupReturnsSemanticHit(t *testing.T) {
	t.Parallel()

	semantic := cache.NewMemorySemanticStore(time.Minute, 8, cache.LocalSimpleEmbedder{})
	resp := &types.ChatResponse{
		Model: "llama3.1:8b",
		Route: types.RouteDecision{Provider: "ollama", Model: "llama3.1:8b", Reason: "semantic"},
	}
	namespace := "model:llama3.1:8b|provider:ollama|tenant:anonymous"
	if err := semantic.Set(context.Background(), cache.SemanticEntry{
		Namespace: namespace,
		Text:      "user: hello there",
		Response:  resp,
	}); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	store := governor.NewMemoryBudgetStore()
	runtime := NewGatewayCacheRuntime(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		cache.NewMemoryStore(time.Minute),
		semantic,
		SemanticOptions{Enabled: true, MaxTextChars: 8_000},
		governor.NewStaticGovernor(config.GovernorConfig{}, store, store),
		providers.NewRegistry(&sequenceProvider{name: "ollama", kind: providers.KindLocal}),
	)

	trace := profiler.NewTrace("semantic-hit", nil)
	defer trace.Finalize()

	result, ok, err := runtime.Lookup(context.Background(), trace, &ExecutionPlan{
		Request:          types.ChatRequest{Model: "llama3.1:8b"},
		CacheKey:         "missing",
		Route:            types.RouteDecision{Provider: "ollama", Model: "llama3.1:8b", Reason: "planned"},
		ProviderKind:     "local",
		SemanticEligible: true,
		SemanticScope:    namespace,
		SemanticQuery: cache.SemanticQuery{
			Namespace:     namespace,
			Text:          "user: hello there",
			MinSimilarity: 0.90,
			MaxTextChars:  8_000,
		},
	})
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if !ok {
		t.Fatal("Lookup() ok = false, want true")
	}
	if result.CacheType != "semantic" {
		t.Fatalf("cache_type = %q, want semantic", result.CacheType)
	}
	if result.Semantic == nil {
		t.Fatal("semantic match = nil, want value")
	}
}

func TestGatewayCacheRuntimeStoreWritesExactAndSemantic(t *testing.T) {
	t.Parallel()

	exact := cache.NewMemoryStore(time.Minute)
	semantic := cache.NewMemorySemanticStore(time.Minute, 8, cache.LocalSimpleEmbedder{})
	store := governor.NewMemoryBudgetStore()
	runtime := NewGatewayCacheRuntime(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		exact,
		semantic,
		SemanticOptions{Enabled: true, MaxTextChars: 8_000},
		governor.NewStaticGovernor(config.GovernorConfig{}, store, store),
		providers.NewRegistry(&sequenceProvider{name: "ollama", kind: providers.KindLocal}),
	)

	trace := profiler.NewTrace("store", nil)
	defer trace.Finalize()

	plan := &ExecutionPlan{
		Request: types.ChatRequest{
			Model: "llama3.1:8b",
			Messages: []types.Message{
				{Role: "user", Content: "hello there"},
			},
		},
		CacheKey:         "cache-key",
		SemanticEligible: true,
	}
	resp := &types.ChatResponse{Model: "llama3.1:8b"}
	decision := types.RouteDecision{Provider: "ollama", Model: "llama3.1:8b", Reason: "test"}

	runtime.Store(context.Background(), trace, plan, decision, resp)

	if _, ok := exact.Get(context.Background(), "cache-key"); !ok {
		t.Fatal("exact cache missing stored response")
	}

	namespace := cache.BuildSemanticNamespace(plan.Request, decision)
	if _, ok := semantic.Search(context.Background(), cache.SemanticQuery{
		Namespace:     namespace,
		Text:          cache.BuildSemanticText(plan.Request, 8_000),
		MinSimilarity: 0.90,
		MaxTextChars:  8_000,
	}); !ok {
		t.Fatal("semantic cache missing stored response")
	}
}
