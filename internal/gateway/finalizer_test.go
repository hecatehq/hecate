package gateway

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/hecate/agent-runtime/internal/config"
	"github.com/hecate/agent-runtime/internal/governor"
	"github.com/hecate/agent-runtime/internal/profiler"
	"github.com/hecate/agent-runtime/internal/providers"
	"github.com/hecate/agent-runtime/internal/telemetry"
	"github.com/hecate/agent-runtime/pkg/types"
)

func TestDefaultResponseFinalizerFinalizeExecution(t *testing.T) {
	t.Parallel()

	store := governor.NewMemoryUsageStore()
	finalizer := NewDefaultResponseFinalizer(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		governor.NewStaticGovernor(config.GovernorConfig{UsageKey: "global"}, store, store),
		telemetry.NewMetrics(),
	)

	trace := profiler.NewTrace("finalize-exec", nil)
	defer trace.Finalize()

	result, err := finalizer.FinalizeExecution(context.Background(), trace, &ExecutionPlan{
		OriginalRequest: types.ChatRequest{
			RequestID: "req-1",
			Model:     "gpt-4o-mini",
			Messages:  []types.Message{{Role: "user", Content: "hello"}},
		},
		Request: types.ChatRequest{
			RequestID: "req-1",
			Model:     "gpt-4o-mini",
			Messages:  []types.Message{{Role: "user", Content: "hello"}},
		},
	}, &providerCallResult{
		Response: &types.ChatResponse{
			Model: "gpt-4o-mini",
			Usage: types.Usage{
				PromptTokens:     10,
				CompletionTokens: 5,
				TotalTokens:      15,
			},
		},
		Decision:     types.RouteDecision{Provider: "openai", Model: "gpt-4o-mini", Reason: "test"},
		ProviderKind: string(providers.KindCloud),
	})
	if err != nil {
		t.Fatalf("FinalizeExecution() error = %v", err)
	}
	if result.Metadata.CostMicrosUSD != 0 {
		t.Fatalf("cost_micros_usd = %d, want 0 without cost estimation", result.Metadata.CostMicrosUSD)
	}

	events, err := store.ListRecentEvents(context.Background(), 5)
	if err != nil {
		t.Fatalf("ListRecentEvents() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("usage events = %d, want 1", len(events))
	}
	if events[0].TotalTokens != 15 {
		t.Fatalf("usage event total tokens = %d, want 15", events[0].TotalTokens)
	}
}

func TestDefaultResponseFinalizerFinalizeExecutionAllowsUnpricedResolvedModel(t *testing.T) {
	t.Parallel()

	store := governor.NewMemoryUsageStore()
	finalizer := NewDefaultResponseFinalizer(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		governor.NewStaticGovernor(config.GovernorConfig{UsageKey: "global"}, store, store),
		telemetry.NewMetrics(),
	)

	trace := profiler.NewTrace("finalize-unpriced", nil)
	defer trace.Finalize()

	result, err := finalizer.FinalizeExecution(context.Background(), trace, &ExecutionPlan{
		OriginalRequest: types.ChatRequest{
			RequestID: "req-1",
			Model:     "gpt-4o-mini",
			Messages:  []types.Message{{Role: "user", Content: "hello"}},
		},
		Request: types.ChatRequest{
			RequestID: "req-1",
			Model:     "gpt-4o-mini",
			Messages:  []types.Message{{Role: "user", Content: "hello"}},
		},
	}, &providerCallResult{
		Response: &types.ChatResponse{
			Model: "omni-moderation-2024-09-26",
			Usage: types.Usage{
				PromptTokens:     10,
				CompletionTokens: 5,
				TotalTokens:      15,
			},
		},
		Decision:     types.RouteDecision{Provider: "openai", Model: "gpt-4o-mini", Reason: "test"},
		ProviderKind: string(providers.KindCloud),
	})
	if err != nil {
		t.Fatalf("FinalizeExecution() error = %v, want degraded cost handling", err)
	}
	if result.Metadata.CostMicrosUSD != 0 {
		t.Fatalf("cost_micros_usd = %d, want 0 for unpriced resolved model", result.Metadata.CostMicrosUSD)
	}
}
