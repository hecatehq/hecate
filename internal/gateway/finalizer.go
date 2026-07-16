package gateway

import (
	"context"
	"log/slog"

	"github.com/hecatehq/hecate/internal/governor"
	"github.com/hecatehq/hecate/internal/models"
	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
)

// ResponseFinalizer turns a successful provider call into a ChatResult.
// Every request goes straight through to the upstream; the finalizer
// records cost, metrics, and logs.
type ResponseFinalizer interface {
	FinalizeExecution(ctx context.Context, trace *profiler.Trace, plan *ExecutionPlan, callResult *providerCallResult) (*ChatResult, error)
}

type DefaultResponseFinalizer struct {
	logger   *slog.Logger
	governor governor.Governor
	metrics  *telemetry.Metrics
}

func NewDefaultResponseFinalizer(
	logger *slog.Logger,
	governor governor.Governor,
	metrics *telemetry.Metrics,
) *DefaultResponseFinalizer {
	return &DefaultResponseFinalizer{
		logger:   logger,
		governor: governor,
		metrics:  metrics,
	}
}

func (f *DefaultResponseFinalizer) FinalizeExecution(ctx context.Context, trace *profiler.Trace, plan *ExecutionPlan, callResult *providerCallResult) (*ChatResult, error) {
	resp := callResult.Response
	decision := callResult.Decision

	resp.Route = decision
	recordTrace(trace, "usage.normalized", "response", map[string]any{
		telemetry.AttrGenAIUsageInputTokens:  resp.Usage.PromptTokens,
		telemetry.AttrGenAIUsageOutputTokens: resp.Usage.CompletionTokens,
		telemetry.AttrGenAIUsageTotalTokens:  resp.Usage.TotalTokens,
	})

	cost := types.CostBreakdown{Currency: "USD"}
	resp.Cost = cost
	recordTrace(trace, "usage.recorded", "response", map[string]any{
		telemetry.AttrHecateCostTotalMicrosUSD: cost.TotalMicrosUSD,
	})

	if err := f.governor.RecordUsage(ctx, plan.Request, decision, resp.Usage, cost.TotalMicrosUSD); err != nil {
		telemetry.Warn(f.logger, ctx, "gateway.usage.record.failed",
			slog.String("event.name", "gateway.usage.record.failed"),
			slog.Any("error", err),
		)
		recordTraceError(trace, "governor.usage_record_failed", "governor", errorKindUsageRecordFailed, err, nil)
	}

	identity := models.BuildIdentity(plan.OriginalRequest.Model, resp.Model)
	recordTrace(trace, "response.returned", "response", map[string]any{
		telemetry.AttrGenAIProviderName:             decision.Provider,
		telemetry.AttrGenAIResponseModel:            resp.Model,
		telemetry.AttrGenAIRequestModel:             identity.Requested,
		telemetry.AttrHecateModelRequestedCanonical: identity.CanonicalRequested,
		telemetry.AttrHecateModelResolvedCanonical:  identity.CanonicalResolved,
	})

	metadata := buildResponseMetadata(plan, callResult, trace, identity, resp.Usage, cost.TotalMicrosUSD)

	return f.completeResult(ctx, trace, resp, metadata), nil
}

func buildResponseMetadata(
	plan *ExecutionPlan,
	callResult *providerCallResult,
	trace *profiler.Trace,
	identity models.Identity,
	usage types.Usage,
	costMicrosUSD int64,
) ResponseMetadata {
	decision := callResult.Decision
	return ResponseMetadata{
		RequestID:               plan.OriginalRequest.RequestID,
		Provider:                decision.Provider,
		ProviderKind:            callResult.ProviderKind,
		ProviderInstance:        decision.ProviderInstance,
		RouteReason:             decision.Reason,
		RequestedModel:          identity.Requested,
		CanonicalRequestedModel: identity.CanonicalRequested,
		Model:                   identity.Resolved,
		CanonicalResolvedModel:  identity.CanonicalResolved,
		PromptTokens:            usage.PromptTokens,
		CompletionTokens:        usage.CompletionTokens,
		TotalTokens:             usage.TotalTokens,
		CostMicrosUSD:           costMicrosUSD,
		AttemptCount:            callResult.AttemptCount,
		RetryCount:              callResult.RetryCount,
		FallbackFromProvider:    callResult.FallbackFromProvider,
		TraceID:                 trace.TraceID,
		SpanID:                  trace.RootSpanID(),
	}
}

func (f *DefaultResponseFinalizer) completeResult(ctx context.Context, trace *profiler.Trace, resp *types.ChatResponse, metadata ResponseMetadata) *ChatResult {
	if f.metrics != nil {
		f.metrics.RecordChat(ctx, telemetry.ChatMetricsRecord{
			Provider:             metadata.Provider,
			ProviderKind:         metadata.ProviderKind,
			RequestedModel:       metadata.RequestedModel,
			ResponseModel:        metadata.Model,
			CostMicrosUSD:        metadata.CostMicrosUSD,
			PromptTokens:         int64(metadata.PromptTokens),
			CompletionTokens:     int64(metadata.CompletionTokens),
			TotalTokens:          int64(metadata.TotalTokens),
			RetryCount:           metadata.RetryCount,
			FallbackFromProvider: metadata.FallbackFromProvider,
		})
	}

	telemetry.Info(f.logger, ctx, "gen_ai.gateway.request",
		slog.String("event.name", "gen_ai.gateway.request"),
		slog.String(telemetry.AttrHecateResult, telemetry.ResultSuccess),
		slog.String(telemetry.AttrGenAIProviderName, metadata.Provider),
		slog.String(telemetry.AttrHecateProviderKind, metadata.ProviderKind),
		slog.String(telemetry.AttrHecateRouteReason, metadata.RouteReason),
		slog.String(telemetry.AttrGenAIRequestModel, metadata.RequestedModel),
		slog.String(telemetry.AttrHecateModelRequestedCanonical, metadata.CanonicalRequestedModel),
		slog.String(telemetry.AttrGenAIResponseModel, metadata.Model),
		slog.String(telemetry.AttrHecateModelResolvedCanonical, metadata.CanonicalResolvedModel),
		slog.Int(telemetry.AttrGenAIUsageInputTokens, metadata.PromptTokens),
		slog.Int(telemetry.AttrGenAIUsageOutputTokens, metadata.CompletionTokens),
		slog.Int(telemetry.AttrGenAIUsageTotalTokens, metadata.TotalTokens),
		slog.Int64(telemetry.AttrHecateCostTotalMicrosUSD, metadata.CostMicrosUSD),
		slog.Int(telemetry.AttrHecateRetryAttemptCount, metadata.AttemptCount),
		slog.Int(telemetry.AttrHecateRetryCount, metadata.RetryCount),
		slog.String(telemetry.AttrHecateFailoverFromProvider, metadata.FallbackFromProvider),
	)

	return &ChatResult{
		Response: resp,
		Metadata: metadata,
		Trace:    trace,
	}
}
