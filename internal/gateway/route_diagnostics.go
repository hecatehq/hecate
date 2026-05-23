package gateway

import (
	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
)

func recordRouteDeniedCandidate(trace *profiler.Trace, candidate types.RouteDecision, preflightErr *RoutePreflightError, index int) {
	if trace == nil || preflightErr == nil {
		return
	}
	providerKind := firstNonEmpty(preflightErr.ProviderKind, candidate.ProviderKind)
	reason := classifyRouteDenied(preflightErr.Err)
	recordTraceError(trace, "router.candidate.denied", "routing", reason, preflightErr, map[string]any{
		telemetry.AttrGenAIProviderName:            candidate.Provider,
		telemetry.AttrGenAIRequestModel:            candidate.Model,
		telemetry.AttrHecateProviderKind:           providerKind,
		telemetry.AttrHecateRouteReason:            candidate.Reason,
		telemetry.AttrHecateProviderIndex:          index,
		telemetry.AttrHecateRouteOutcome:           "denied",
		telemetry.AttrHecateRouteSkipReason:        reason,
		telemetry.AttrHecateCostEstimatedMicrosUSD: preflightErr.EstimatedCostMicros,
	})
}
