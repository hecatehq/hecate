package profiler

import (
	"testing"

	"github.com/hecate/agent-runtime/internal/telemetry"
)

func TestTraceRecordCreatesEvent(t *testing.T) {
	t.Parallel()

	trace := NewTrace("req-123", nil)
	trace.Record("cache.miss", map[string]any{"key": "abc"})

	events := trace.Events()
	if len(events) != 1 {
		t.Fatalf("Events() len = %d, want 1", len(events))
	}
	if events[0].Name != "cache.miss" {
		t.Fatalf("event name = %q, want %q", events[0].Name, "cache.miss")
	}
	if events[0].Attributes["key"] != "abc" {
		t.Fatalf("event attribute = %#v, want abc", events[0].Attributes["key"])
	}
	if spans := trace.Spans(); spans[1].Attributes[telemetry.AttrHecatePhase] != "cache" {
		t.Fatalf("span phase attribute = %#v, want cache", spans[1].Attributes[telemetry.AttrHecatePhase])
	}
}

// TestSpanMappingForEventGroups verifies that each defined event group is
// routed to the correct parent span. This guards against accidental
// regressions in spanSpecForEvent when new events are added.
func TestSpanMappingForEventGroups(t *testing.T) {
	t.Parallel()

	cases := []struct {
		event    string
		wantSpan string
	}{
		// Request parse
		{telemetry.EventRequestReceived, telemetry.SpanGatewayRequestParse},
		{telemetry.EventRequestInvalid, telemetry.SpanGatewayRequestParse},
		// Governor
		{telemetry.EventGovernorAllowed, telemetry.SpanGatewayGovernor},
		{telemetry.EventGovernorDenied, telemetry.SpanGatewayGovernor},
		{telemetry.EventGovernorModelRewrite, telemetry.SpanGatewayGovernor},
		{telemetry.EventGovernorBudgetEstimateFailed, telemetry.SpanGatewayGovernor},
		{telemetry.EventGovernorRouteDenied, telemetry.SpanGatewayGovernor},
		{telemetry.EventGovernorRouteAllowed, telemetry.SpanGatewayGovernor},
		// Router
		{telemetry.EventRouterFailed, telemetry.SpanGatewayRouter},
		{telemetry.EventRouterSelected, telemetry.SpanGatewayRouter},
		{telemetry.EventRouterCandidateConsidered, telemetry.SpanGatewayRouter},
		{telemetry.EventRouterCandidateSkipped, telemetry.SpanGatewayRouter},
		{telemetry.EventRouterCandidateDenied, telemetry.SpanGatewayRouter},
		{telemetry.EventRouterCandidateSelected, telemetry.SpanGatewayRouter},
		// Provider
		{telemetry.EventProviderCallStarted, telemetry.SpanGatewayProvider},
		{telemetry.EventProviderCallFinished, telemetry.SpanGatewayProvider},
		{telemetry.EventProviderCallFailed, telemetry.SpanGatewayProvider},
		{telemetry.EventProviderRetryScheduled, telemetry.SpanGatewayProvider},
		{telemetry.EventProviderFailoverSelected, telemetry.SpanGatewayProvider},
		// Usage / cost / response
		{telemetry.EventUsageNormalized, telemetry.SpanGatewayUsage},
		{telemetry.EventCostCalculated, telemetry.SpanGatewayCost},
		{telemetry.EventResponseReturned, telemetry.SpanGatewayResponse},
		// Orchestrator
		{telemetry.EventOrchestratorTaskStarted, telemetry.SpanOrchestratorTask},
		{telemetry.EventOrchestratorTaskFinished, telemetry.SpanOrchestratorTask},
		{telemetry.EventOrchestratorRunStarted, telemetry.SpanOrchestratorRun},
		{telemetry.EventOrchestratorRunFailed, telemetry.SpanOrchestratorRun},
		{telemetry.EventOrchestratorRunFinished, telemetry.SpanOrchestratorRun},
		{telemetry.EventOrchestratorStepCompleted, telemetry.SpanOrchestratorStep},
		{telemetry.EventOrchestratorStepFailed, telemetry.SpanOrchestratorStep},
		{telemetry.EventOrchestratorArtifactCreated, telemetry.SpanOrchestratorArtifact},
		{telemetry.EventOrchestratorApprovalRequested, telemetry.SpanOrchestratorApproval},
		{telemetry.EventOrchestratorApprovalResolved, telemetry.SpanOrchestratorApproval},
		// Queue lifecycle
		{telemetry.EventQueueEnqueued, telemetry.SpanOrchestratorQueue},
		{telemetry.EventQueueClaimed, telemetry.SpanOrchestratorQueue},
		{telemetry.EventQueueAcked, telemetry.SpanOrchestratorQueue},
		{telemetry.EventQueueNacked, telemetry.SpanOrchestratorQueue},
		{telemetry.EventQueueLeaseExtended, telemetry.SpanOrchestratorQueue},
		{telemetry.EventQueueLeaseExtendFailed, telemetry.SpanOrchestratorQueue},
		// Retention
		{telemetry.EventRetentionRunStarted, telemetry.SpanRetentionRun},
		{telemetry.EventRetentionRunFinished, telemetry.SpanRetentionRun},
		{telemetry.EventRetentionSubsystemFailed, telemetry.SpanRetentionRun},
		{telemetry.EventRetentionSubsystemFinished, telemetry.SpanRetentionRun},
		{telemetry.EventRetentionHistoryFailed, telemetry.SpanRetentionRun},
		{telemetry.EventRetentionHistoryPersisted, telemetry.SpanRetentionRun},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.event, func(t *testing.T) {
			t.Parallel()

			trace := NewTrace("req-span-map", nil)
			trace.Record(tc.event, map[string]any{})

			// spans[0] is always the root gateway.request span.
			// spans[1] (when present) is the child span created for this event.
			spans := trace.Spans()
			if len(spans) < 2 {
				t.Fatalf("event %q created no child span; spanSpecForEvent may be missing a case", tc.event)
			}
			var found bool
			for _, s := range spans[1:] {
				if s.Name == tc.wantSpan {
					found = true
					break
				}
			}
			if !found {
				names := make([]string, len(spans))
				for i, s := range spans {
					names[i] = s.Name
				}
				t.Errorf("event %q: want span %q, got spans %v", tc.event, tc.wantSpan, names)
			}
		})
	}
}
