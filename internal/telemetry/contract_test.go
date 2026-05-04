package telemetry

import (
	"context"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// ---------------------------------------------------------------------------
// Normalization helpers
// ---------------------------------------------------------------------------

func TestNormalizeErrorKindPassesThroughKnownValues(t *testing.T) {
	t.Parallel()

	known := []string{
		ErrorKindInvalidRequest,
		ErrorKindRequestDenied,
		ErrorKindRouterFailed,
		ErrorKindBudgetEstimate,
		ErrorKindRouteDenied,
		ErrorKindProviderCallFailed,
		ErrorKindRetryBackoff,
		ErrorKindProviderHealth,
		ErrorKindUsageRecord,
		ErrorKindOther,
	}
	for _, kind := range known {
		if got := NormalizeErrorKind(kind); got != kind {
			t.Errorf("NormalizeErrorKind(%q) = %q, want same", kind, got)
		}
	}
}

func TestNormalizeErrorKindClampsUnknownToOther(t *testing.T) {
	t.Parallel()

	cases := []string{
		"", "unknown_error", "INVALID_REQUEST", "some-new-kind", "sql_error",
	}
	for _, kind := range cases {
		if got := NormalizeErrorKind(kind); got != ErrorKindOther {
			t.Errorf("NormalizeErrorKind(%q) = %q, want %q", kind, got, ErrorKindOther)
		}
	}
}

func TestNormalizeResultPassesThroughKnownValues(t *testing.T) {
	t.Parallel()

	known := []string{ResultSuccess, ResultDenied, ResultError}
	for _, result := range known {
		if got := NormalizeResult(result); got != result {
			t.Errorf("NormalizeResult(%q) = %q, want same", result, got)
		}
	}
}

func TestNormalizeResultClampsUnknownToError(t *testing.T) {
	t.Parallel()

	cases := []string{"", "ok", "SUCCESS", "timeout", "rate_limited"}
	for _, result := range cases {
		if got := NormalizeResult(result); got != ResultError {
			t.Errorf("NormalizeResult(%q) = %q, want %q", result, got, ResultError)
		}
	}
}

// ---------------------------------------------------------------------------
// ValidateEventAttrs
// ---------------------------------------------------------------------------

func TestAllEventNamesReturnsStableUniqueCopy(t *testing.T) {
	t.Parallel()

	first := AllEventNames()
	if len(first) == 0 {
		t.Fatal("AllEventNames() returned no events")
	}
	seen := make(map[string]struct{}, len(first))
	for _, name := range first {
		if name == "" {
			t.Fatal("AllEventNames() returned an empty event name")
		}
		if _, exists := seen[name]; exists {
			t.Fatalf("AllEventNames() contains duplicate event %q", name)
		}
		seen[name] = struct{}{}
	}

	first[0] = "mutated"
	second := AllEventNames()
	if second[0] == "mutated" {
		t.Fatal("AllEventNames() returned the underlying slice; mutations leak")
	}
}

func TestValidateEventAttrsPassesWhenAllRequiredPresent(t *testing.T) {
	t.Parallel()

	for eventName, required := range requiredEventAttrs {
		attrs := make(map[string]any, len(required))
		for _, k := range required {
			attrs[k] = "value"
		}
		if missing := ValidateEventAttrs(eventName, attrs); len(missing) != 0 {
			t.Errorf("event %q: unexpected missing attrs with full set provided: %v", eventName, missing)
		}
	}
}

func TestValidateEventAttrsReturnsMissingKeys(t *testing.T) {
	t.Parallel()

	// EventRequestReceived requires message count + model; supply neither.
	missing := ValidateEventAttrs(EventRequestReceived, map[string]any{})
	if len(missing) == 0 {
		t.Fatal("expected missing keys for empty attrs, got none")
	}
	want := map[string]bool{
		AttrHecateRequestMessageCount: true,
		AttrGenAIRequestModel:         true,
	}
	for _, k := range missing {
		if !want[k] {
			t.Errorf("unexpected missing key %q", k)
		}
	}
	if len(missing) != len(want) {
		t.Errorf("missing count = %d, want %d", len(missing), len(want))
	}
}

func TestValidateEventAttrsReturnsMissingSubset(t *testing.T) {
	t.Parallel()

	// EventProviderCallStarted requires provider name, request model, attempt.
	// Supply provider name but omit the others.
	attrs := map[string]any{
		AttrGenAIProviderName: "openai",
	}
	missing := ValidateEventAttrs(EventProviderCallStarted, attrs)
	want := map[string]bool{
		AttrGenAIRequestModel:  true,
		AttrHecateRetryAttempt: true,
	}
	for _, k := range missing {
		if !want[k] {
			t.Errorf("unexpected missing key %q", k)
		}
	}
	if len(missing) != len(want) {
		t.Errorf("missing count = %d, want %d", len(missing), len(want))
	}
}

func TestValidateEventAttrsUnknownEventAlwaysPasses(t *testing.T) {
	t.Parallel()

	if missing := ValidateEventAttrs("totally.unknown.event", map[string]any{}); len(missing) != 0 {
		t.Errorf("unknown event with empty attrs returned missing keys: %v", missing)
	}
}

func TestRequiredAttrsForEventReturnsCopy(t *testing.T) {
	t.Parallel()

	// Mutating the returned slice must not affect future calls.
	first := RequiredAttrsForEvent(EventResponseReturned)
	if len(first) == 0 {
		t.Fatal("expected non-empty required attrs for EventResponseReturned")
	}
	first[0] = "mutated"

	second := RequiredAttrsForEvent(EventResponseReturned)
	for _, k := range second {
		if k == "mutated" {
			t.Error("RequiredAttrsForEvent returned the same underlying slice; mutations leak")
		}
	}
}

// ---------------------------------------------------------------------------
// Metric instrument name stability
// ---------------------------------------------------------------------------

// TestMetricNameConstantsMatchInstruments verifies that every metric name
// constant in the contract corresponds to an instrument actually registered by
// NewMetricsWithMeterProvider. A mismatch means the constant and the live
// instrument have drifted.
func TestMetricNameConstantsMatchInstruments(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	m, err := NewMetricsWithMeterProvider(provider)
	if err != nil {
		t.Fatalf("NewMetricsWithMeterProvider() error = %v", err)
	}

	om, err := NewOrchestratorMetricsWithMeterProvider(provider)
	if err != nil {
		t.Fatalf("NewOrchestratorMetricsWithMeterProvider() error = %v", err)
	}

	// Trigger every instrument so it appears in the collected output.
	ctx := context.Background()
	m.RecordRequestOutcome(ctx, ResultSuccess, 10*time.Millisecond)
	m.RecordChat(ctx, ChatMetricsRecord{
		Provider:             "openai",
		ProviderKind:         "cloud",
		RequestedModel:       "gpt-4o-mini",
		ResponseModel:        "gpt-4o-mini",
		CostMicrosUSD:        100,
		PromptTokens:         10,
		CompletionTokens:     5,
		TotalTokens:          15,
		RetryCount:           1,
		FallbackFromProvider: "prev-provider",
	})

	om.RecordRun(ctx, RunMetricsRecord{Status: "completed", ExecutionKind: "shell", Model: "gpt-4o", DurationMS: 500})
	om.RecordStep(ctx, StepMetricsRecord{StepKind: "shell", Result: ResultSuccess, DurationMS: 100})
	om.RecordApproval(ctx, ApprovalMetricsRecord{ApprovalKind: "shell_command", Decision: "approved", WaitMS: 2000})
	om.RecordQueueWait(ctx, QueueWaitRecord{QueueBackend: "memory", WaitMS: 50})
	om.RecordLeaseExtendFailed(ctx)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	recorded := make(map[string]bool)
	for _, scope := range rm.ScopeMetrics {
		for _, metric := range scope.Metrics {
			recorded[metric.Name] = true
		}
	}

	contractNames := []string{
		// Gateway
		MetricGatewayRequests,
		MetricGatewayRequestDuration,
		MetricChatRequestsTotal,
		MetricCostMicrosTotal,
		MetricInputTokensTotal,
		MetricOutputTokensTotal,
		MetricTotalTokensTotal,
		MetricRetriesTotal,
		MetricFailoversTotal,
		// Orchestrator
		MetricOrchestratorRunsTotal,
		MetricOrchestratorRunDuration,
		MetricOrchestratorQueueWaitDuration,
		MetricOrchestratorStepsTotal,
		MetricOrchestratorStepDuration,
		MetricOrchestratorApprovalsTotal,
		MetricOrchestratorApprovalWaitDuration,
		MetricOrchestratorLeaseExtendFailures,
	}
	for _, name := range contractNames {
		if !recorded[name] {
			t.Errorf("metric constant %q has no matching instrument; constant and implementation have drifted", name)
		}
	}
}

// ---------------------------------------------------------------------------
// Error kind constant completeness — every value in knownErrorKinds must have
// a corresponding exported constant so callers can use the typed form.
// ---------------------------------------------------------------------------

func TestErrorKindConstantsCoverKnownSet(t *testing.T) {
	t.Parallel()

	// Build the set of all exported error kind constants.
	exported := map[string]bool{
		ErrorKindInvalidRequest:     true,
		ErrorKindRequestDenied:      true,
		ErrorKindRouterFailed:       true,
		ErrorKindBudgetEstimate:     true,
		ErrorKindRouteDenied:        true,
		ErrorKindProviderCallFailed: true,
		ErrorKindRetryBackoff:       true,
		ErrorKindProviderHealth:     true,
		ErrorKindUsageRecord:        true,
		ErrorKindOther:              true,
	}

	// Every value in knownErrorKinds must be reachable via an exported constant.
	for kind := range knownErrorKinds {
		if !exported[kind] {
			t.Errorf("knownErrorKinds contains %q but no exported constant covers it", kind)
		}
	}

	// Every exported constant must be in knownErrorKinds so NormalizeErrorKind
	// passes it through.
	for kind := range exported {
		if _, ok := knownErrorKinds[kind]; !ok {
			t.Errorf("exported constant %q is not in knownErrorKinds; NormalizeErrorKind would clamp it to %q", kind, ErrorKindOther)
		}
	}
}
