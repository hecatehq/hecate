package gateway

import (
	"fmt"
	"sort"

	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
)

func buildRouteDecisionReport(spans []types.TraceSpan) types.RouteDecisionReport {
	events := flattenTraceEvents(spans)
	candidateIndex := map[string]int{}
	candidates := make([]types.RouteCandidateReport, 0, 8)
	failovers := make([]types.RouteFailoverReport, 0, 4)
	report := types.RouteDecisionReport{}

	for _, event := range events {
		switch event.Name {
		case "router.candidate.considered", "router.candidate.skipped", "router.candidate.denied", "router.candidate.selected",
			"provider.call.failed", "provider.call.finished", "provider.retry.scheduled", "provider.failover.selected", "provider.failover.triggered":
			candidate := routeCandidateFromEvent(event)
			if candidate.Provider == "" && candidate.Model == "" {
				break
			}
			key := routeCandidateKey(candidate.Provider, candidate.Model, candidate.Index)
			index, ok := candidateIndex[key]
			if !ok {
				candidateIndex[key] = len(candidates)
				candidates = append(candidates, candidate)
				index = len(candidates) - 1
			} else {
				mergeRouteCandidate(&candidates[index], candidate)
			}
			if event.Name == "router.candidate.selected" {
				report.FinalProvider = candidate.Provider
				report.FinalProviderKind = candidate.ProviderKind
				report.FinalModel = candidate.Model
				report.FinalReason = candidate.Reason
			}
		}

		if event.Name == "provider.failover.selected" || event.Name == "provider.failover.triggered" {
			failover := routeFailoverFromEvent(event)
			if failover.FromProvider != "" || failover.ToProvider != "" {
				failovers = append(failovers, failover)
			}
			if report.FallbackFrom == "" && failover.FromProvider != "" {
				report.FallbackFrom = failover.FromProvider
			}
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Index != candidates[j].Index {
			return candidates[i].Index < candidates[j].Index
		}
		if !candidates[i].Timestamp.Equal(candidates[j].Timestamp) {
			return candidates[i].Timestamp.Before(candidates[j].Timestamp)
		}
		return candidates[i].Provider < candidates[j].Provider
	})
	for i := range candidates {
		normalizeRouteCandidate(&candidates[i])
		if report.FinalProvider == "" && candidates[i].Outcome == "completed" {
			report.FinalProvider = candidates[i].Provider
			report.FinalProviderKind = candidates[i].ProviderKind
			report.FinalModel = candidates[i].Model
			report.FinalReason = candidates[i].Reason
		}
	}
	report.Candidates = candidates
	report.Failovers = failovers
	return report
}

func flattenTraceEvents(spans []types.TraceSpan) []types.TraceEvent {
	events := make([]types.TraceEvent, 0, 16)
	for _, span := range spans {
		events = append(events, span.Events...)
	}
	sort.SliceStable(events, func(i, j int) bool {
		return events[i].Timestamp.Before(events[j].Timestamp)
	})
	return events
}

func routeCandidateFromEvent(event types.TraceEvent) types.RouteCandidateReport {
	return types.RouteCandidateReport{
		Provider:           stringAttr(event.Attributes, telemetry.AttrGenAIProviderName),
		ProviderKind:       stringAttr(event.Attributes, telemetry.AttrHecateProviderKind),
		Model:              stringAttr(event.Attributes, telemetry.AttrGenAIRequestModel),
		Reason:             stringAttr(event.Attributes, telemetry.AttrHecateRouteReason),
		Outcome:            routeOutcomeFromEvent(event.Name, event.Attributes),
		SkipReason:         firstNonEmptyString(stringAttr(event.Attributes, telemetry.AttrHecateRouteSkipReason), stringAttr(event.Attributes, telemetry.AttrErrorMessage)),
		HealthStatus:       stringAttr(event.Attributes, telemetry.AttrHecateProviderHealthStatus),
		PolicyRuleID:       stringAttr(event.Attributes, telemetry.AttrHecatePolicyRuleID),
		PolicyAction:       stringAttr(event.Attributes, telemetry.AttrHecatePolicyAction),
		PolicyReason:       stringAttr(event.Attributes, telemetry.AttrHecatePolicyReason),
		EstimatedMicrosUSD: int64Attr(event.Attributes, telemetry.AttrHecateCostEstimatedMicrosUSD),
		Attempt:            int(int64Attr(event.Attributes, telemetry.AttrHecateRetryAttempt)),
		RetryCount:         retryCountFromEvent(event),
		Retryable:          boolAttr(event.Attributes, telemetry.AttrHecateRetryRetryable),
		Index:              int(int64Attr(event.Attributes, telemetry.AttrHecateProviderIndex)),
		LatencyMS:          int64Attr(event.Attributes, telemetry.AttrHecateProviderLatencyMS),
		FailoverFrom:       stringAttr(event.Attributes, telemetry.AttrHecateFailoverFromProvider),
		FailoverTo:         stringAttr(event.Attributes, telemetry.AttrHecateFailoverToProvider),
		Detail:             stringAttr(event.Attributes, telemetry.AttrErrorMessage),
		Timestamp:          event.Timestamp,
	}
}

func routeFailoverFromEvent(event types.TraceEvent) types.RouteFailoverReport {
	return types.RouteFailoverReport{
		FromProvider: stringAttr(event.Attributes, telemetry.AttrHecateFailoverFromProvider),
		FromModel:    stringAttr(event.Attributes, telemetry.AttrHecateFailoverFromModel),
		ToProvider:   stringAttr(event.Attributes, telemetry.AttrHecateFailoverToProvider),
		ToModel:      stringAttr(event.Attributes, telemetry.AttrHecateFailoverToModel),
		Reason:       firstNonEmptyString(stringAttr(event.Attributes, telemetry.AttrHecateFailoverReason), stringAttr(event.Attributes, telemetry.AttrErrorMessage)),
		Timestamp:    event.Timestamp,
	}
}

func routeOutcomeFromEvent(name string, attrs map[string]any) string {
	switch name {
	case "router.candidate.considered":
		return "considered"
	case "router.candidate.skipped":
		return "skipped"
	case "router.candidate.denied":
		return "denied"
	case "router.candidate.selected":
		return "selected"
	case "provider.call.failed":
		return "failed"
	case "provider.call.finished":
		return "completed"
	default:
		if value := stringAttr(attrs, telemetry.AttrHecateRouteOutcome); value != "" {
			return value
		}
		return "unknown"
	}
}

func normalizeRouteCandidate(candidate *types.RouteCandidateReport) {
	switch candidate.Outcome {
	case "selected", "skipped", "denied", "failed", "completed":
		return
	case "", "unknown", "considered":
		candidate.Outcome = "skipped"
		if candidate.SkipReason == "" {
			candidate.SkipReason = "not_selected"
		}
	default:
		if candidate.SkipReason == "" {
			candidate.SkipReason = candidate.Outcome
		}
		candidate.Outcome = "skipped"
	}
}

func mergeRouteCandidate(target *types.RouteCandidateReport, incoming types.RouteCandidateReport) {
	if target.Provider == "" {
		target.Provider = incoming.Provider
	}
	if target.ProviderKind == "" {
		target.ProviderKind = incoming.ProviderKind
	}
	if target.Model == "" {
		target.Model = incoming.Model
	}
	if target.Reason == "" {
		target.Reason = incoming.Reason
	}
	if shouldReplaceOutcome(target.Outcome, incoming.Outcome) {
		target.Outcome = incoming.Outcome
	}
	if incoming.SkipReason != "" {
		target.SkipReason = incoming.SkipReason
	}
	if incoming.HealthStatus != "" {
		target.HealthStatus = incoming.HealthStatus
	}
	if incoming.PolicyRuleID != "" {
		target.PolicyRuleID = incoming.PolicyRuleID
	}
	if incoming.PolicyAction != "" {
		target.PolicyAction = incoming.PolicyAction
	}
	if incoming.PolicyReason != "" {
		target.PolicyReason = incoming.PolicyReason
	}
	if incoming.EstimatedMicrosUSD > 0 {
		target.EstimatedMicrosUSD = incoming.EstimatedMicrosUSD
	}
	if incoming.Attempt > 0 {
		target.Attempt = incoming.Attempt
	}
	if incoming.RetryCount > target.RetryCount {
		target.RetryCount = incoming.RetryCount
	}
	if incoming.Retryable {
		target.Retryable = incoming.Retryable
	}
	if incoming.LatencyMS > 0 {
		target.LatencyMS = incoming.LatencyMS
	}
	if incoming.FailoverFrom != "" {
		target.FailoverFrom = incoming.FailoverFrom
	}
	if incoming.FailoverTo != "" {
		target.FailoverTo = incoming.FailoverTo
	}
	if incoming.Detail != "" {
		target.Detail = incoming.Detail
	}
	if target.Timestamp.IsZero() || (!incoming.Timestamp.IsZero() && incoming.Timestamp.After(target.Timestamp)) {
		target.Timestamp = incoming.Timestamp
	}
}

func routeCandidateKey(provider, model string, index int) string {
	return fmt.Sprintf("%d:%s:%s", index, provider, model)
}

func stringAttr(attrs map[string]any, key string) string {
	if len(attrs) == 0 {
		return ""
	}
	value, ok := attrs[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return fmt.Sprint(typed)
	}
}

func int64Attr(attrs map[string]any, key string) int64 {
	if len(attrs) == 0 {
		return 0
	}
	value, ok := attrs[key]
	if !ok || value == nil {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case float32:
		return int64(typed)
	default:
		return 0
	}
}

func boolAttr(attrs map[string]any, key string) bool {
	if len(attrs) == 0 {
		return false
	}
	value, ok := attrs[key]
	if !ok || value == nil {
		return false
	}
	typed, ok := value.(bool)
	return ok && typed
}

func retryCountFromEvent(event types.TraceEvent) int {
	if event.Name != "provider.retry.scheduled" {
		return 0
	}
	nextAttempt := int(int64Attr(event.Attributes, telemetry.AttrHecateRetryNextAttempt))
	if nextAttempt <= 1 {
		return 0
	}
	return nextAttempt - 1
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func shouldReplaceOutcome(current, incoming string) bool {
	if incoming == "" || incoming == "unknown" {
		return false
	}
	if current == "" || current == "unknown" {
		return true
	}
	rank := map[string]int{
		"considered": 1,
		"skipped":    2,
		"denied":     2,
		"selected":   3,
		"failed":     4,
		"completed":  5,
	}
	return rank[incoming] >= rank[current]
}
