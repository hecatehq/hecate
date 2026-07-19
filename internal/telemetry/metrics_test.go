package telemetry

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestMetricsRecordRequestOutcomeProducesOTelMetrics(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	metrics, err := NewMetricsWithMeterProvider(provider)
	if err != nil {
		t.Fatalf("NewMetricsWithMeterProvider() error = %v", err)
	}

	metrics.RecordRequestOutcome(context.Background(), ResultSuccess, 125*time.Millisecond)

	collected := collectMetrics(t, reader)
	requests := findMetric[metricdata.Sum[int64]](t, collected, "hecate.gateway.requests")
	if len(requests.DataPoints) != 1 {
		t.Fatalf("request data points = %d, want 1", len(requests.DataPoints))
	}
	if requests.DataPoints[0].Value != 1 {
		t.Fatalf("request count = %d, want 1", requests.DataPoints[0].Value)
	}
	if got := attrValue(requests.DataPoints[0].Attributes, AttrHecateResult); got != ResultSuccess {
		t.Fatalf("result attribute = %q, want %q", got, ResultSuccess)
	}

	duration := findMetric[metricdata.Histogram[int64]](t, collected, "hecate.gateway.request.duration")
	if len(duration.DataPoints) != 1 {
		t.Fatalf("duration data points = %d, want 1", len(duration.DataPoints))
	}
	if duration.DataPoints[0].Count != 1 {
		t.Fatalf("duration count = %d, want 1", duration.DataPoints[0].Count)
	}
	if duration.DataPoints[0].Sum != 125 {
		t.Fatalf("duration sum = %d, want 125", duration.DataPoints[0].Sum)
	}
}

func TestMetricsRecordChatTracksSemanticAndRetryDetails(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	metrics, err := NewMetricsWithMeterProvider(provider)
	if err != nil {
		t.Fatalf("NewMetricsWithMeterProvider() error = %v", err)
	}

	metrics.RecordChat(context.Background(), ChatMetricsRecord{
		Provider:             "ollama",
		ProviderKind:         "local",
		RequestedModel:       "llama3.1:8b",
		ResponseModel:        "llama3.1:8b",
		CostMicrosUSD:        123,
		PromptTokens:         12,
		CompletionTokens:     5,
		TotalTokens:          17,
		RetryCount:           2,
		FallbackFromProvider: "local-primary",
	})

	collected := collectMetrics(t, reader)

	chatRequests := findMetric[metricdata.Sum[int64]](t, collected, "gen_ai.gateway.chat.requests")
	if len(chatRequests.DataPoints) != 1 {
		t.Fatalf("chat request data points = %d, want 1", len(chatRequests.DataPoints))
	}
	if chatRequests.DataPoints[0].Value != 1 {
		t.Fatalf("chat request count = %d, want 1", chatRequests.DataPoints[0].Value)
	}
	cost := findMetric[metricdata.Sum[int64]](t, collected, "gen_ai.gateway.cost")
	if cost.DataPoints[0].Value != 123 {
		t.Fatalf("cost total = %d, want 123", cost.DataPoints[0].Value)
	}

	retries := findMetric[metricdata.Sum[int64]](t, collected, "hecate.gateway.retries")
	if retries.DataPoints[0].Value != 2 {
		t.Fatalf("retry total = %d, want 2", retries.DataPoints[0].Value)
	}

	failovers := findMetric[metricdata.Sum[int64]](t, collected, "hecate.gateway.failovers")
	if failovers.DataPoints[0].Value != 1 {
		t.Fatalf("failover total = %d, want 1", failovers.DataPoints[0].Value)
	}
	if got := attrValue(failovers.DataPoints[0].Attributes, AttrHecateFailoverFromProvider); got != "local-primary" {
		t.Fatalf("failover attribute = %q, want local-primary", got)
	}
}

func TestMetricsRecordProviderCallEmitsAttemptAndHealthSignals(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	metrics, err := NewMetricsWithMeterProvider(provider)
	if err != nil {
		t.Fatalf("NewMetricsWithMeterProvider() error = %v", err)
	}

	metrics.RecordProviderCall(context.Background(), ProviderCallMetricsRecord{
		Provider:     "openai",
		ProviderKind: "cloud",
		Model:        "gpt-4o-mini",
		Result:       ResultSuccess,
		Attempt:      2,
		HealthStatus: "half_open",
		DurationMS:   240,
	})

	collected := collectMetrics(t, reader)
	calls := findMetric[metricdata.Sum[int64]](t, collected, MetricProviderCallsTotal)
	if len(calls.DataPoints) != 1 {
		t.Fatalf("provider call data points = %d, want 1", len(calls.DataPoints))
	}
	if calls.DataPoints[0].Value != 1 {
		t.Fatalf("provider call count = %d, want 1", calls.DataPoints[0].Value)
	}
	for key, want := range map[string]string{
		AttrGenAIProviderName:          "openai",
		AttrHecateProviderKind:         "cloud",
		AttrGenAIRequestModel:          "gpt-4o-mini",
		AttrHecateResult:               ResultSuccess,
		AttrHecateProviderHealthStatus: "half_open",
	} {
		if got := attrValue(calls.DataPoints[0].Attributes, key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}

	duration := findMetric[metricdata.Histogram[int64]](t, collected, MetricProviderCallDuration)
	if len(duration.DataPoints) != 1 {
		t.Fatalf("provider call duration points = %d, want 1", len(duration.DataPoints))
	}
	if duration.DataPoints[0].Count != 1 || duration.DataPoints[0].Sum != 240 {
		t.Fatalf("provider call duration count/sum = %d/%d, want 1/240", duration.DataPoints[0].Count, duration.DataPoints[0].Sum)
	}
}

func TestMetricsRecordProviderCallNormalizesMetricLabels(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	metrics, err := NewMetricsWithMeterProvider(provider)
	if err != nil {
		t.Fatalf("NewMetricsWithMeterProvider() error = %v", err)
	}

	metrics.RecordProviderCall(context.Background(), ProviderCallMetricsRecord{
		Provider:     "  custom-provider  ",
		ProviderKind: "edge",
		Model:        "bad\nmodel",
		Result:       "surprise",
		Attempt:      1,
		HealthStatus: "burning",
		DurationMS:   10,
	})

	collected := collectMetrics(t, reader)
	calls := findMetric[metricdata.Sum[int64]](t, collected, MetricProviderCallsTotal)
	if len(calls.DataPoints) != 1 {
		t.Fatalf("provider call data points = %d, want 1", len(calls.DataPoints))
	}
	for key, want := range map[string]string{
		AttrGenAIProviderName:          "custom-provider",
		AttrHecateProviderKind:         "other",
		AttrGenAIRequestModel:          "other",
		AttrHecateResult:               ResultError,
		AttrHecateProviderHealthStatus: "other",
	} {
		if got := attrValue(calls.DataPoints[0].Attributes, key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestMetricsRecordProviderCallSkipsEmptyNormalizedProvider(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	metrics, err := NewMetricsWithMeterProvider(provider)
	if err != nil {
		t.Fatalf("NewMetricsWithMeterProvider() error = %v", err)
	}

	metrics.RecordProviderCall(context.Background(), ProviderCallMetricsRecord{
		Provider:     " \t ",
		ProviderKind: "cloud",
		Model:        "gpt-4o-mini",
		Result:       ResultSuccess,
		Attempt:      1,
		DurationMS:   10,
	})

	collected := collectMetrics(t, reader)
	assertMetricHasNoDataPoints(t, collected, MetricProviderCallsTotal)
	assertMetricHasNoDataPoints(t, collected, MetricProviderCallDuration)
}

func TestAgentChatMetricsRecordTurnEmitsCounterAndHistogram(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	metrics, err := NewAgentChatMetricsWithMeterProvider(provider)
	if err != nil {
		t.Fatalf("NewAgentChatMetricsWithMeterProvider() error = %v", err)
	}

	metrics.RecordTurn(context.Background(), AgentChatTurnMetricsRecord{
		AdapterID:  "codex",
		DriverKind: "acp",
		Status:     "completed",
		Result:     ResultSuccess,
		DurationMS: 1250,
		Timing: AgentChatTurnTimingRecord{
			QueueMS:        50,
			ModelMS:        900,
			ToolMS:         120,
			ApprovalWaitMS: 0,
			OverheadMS:     180,
		},
	})

	collected := collectMetrics(t, reader)
	turns := findMetric[metricdata.Sum[int64]](t, collected, MetricAgentChatTurnsTotal)
	if len(turns.DataPoints) != 1 {
		t.Fatalf("turn data points = %d, want 1", len(turns.DataPoints))
	}
	if turns.DataPoints[0].Value != 1 {
		t.Fatalf("turn count = %d, want 1", turns.DataPoints[0].Value)
	}
	if got := attrValue(turns.DataPoints[0].Attributes, AttrHecateAgentAdapterID); got != "codex" {
		t.Fatalf("%s = %q, want codex", AttrHecateAgentAdapterID, got)
	}
	if got := attrValue(turns.DataPoints[0].Attributes, AttrHecateAgentDriverKind); got != "acp" {
		t.Fatalf("%s = %q, want acp", AttrHecateAgentDriverKind, got)
	}
	if got := attrValue(turns.DataPoints[0].Attributes, AttrHecateChatTurnStatus); got != "completed" {
		t.Fatalf("%s = %q, want completed", AttrHecateChatTurnStatus, got)
	}
	if got := attrValue(turns.DataPoints[0].Attributes, AttrHecateResult); got != ResultSuccess {
		t.Fatalf("%s = %q, want %s", AttrHecateResult, got, ResultSuccess)
	}

	duration := findMetric[metricdata.Histogram[int64]](t, collected, MetricAgentChatTurnDuration)
	if len(duration.DataPoints) != 1 {
		t.Fatalf("duration data points = %d, want 1", len(duration.DataPoints))
	}
	if duration.DataPoints[0].Count != 1 || duration.DataPoints[0].Sum != 1250 {
		t.Fatalf("duration count/sum = %d/%d, want 1/1250", duration.DataPoints[0].Count, duration.DataPoints[0].Sum)
	}
	timing := findMetric[metricdata.Histogram[int64]](t, collected, MetricAgentChatTurnTiming)
	if len(timing.DataPoints) != 4 {
		t.Fatalf("timing data points = %d, want 4 non-zero buckets", len(timing.DataPoints))
	}
	gotBuckets := map[string]int64{}
	for _, point := range timing.DataPoints {
		gotBuckets[attrValue(point.Attributes, AttrHecateChatTimingBucket)] = point.Sum
	}
	for bucket, want := range map[string]int64{"queue": 50, "model": 900, "tools": 120, "overhead": 180} {
		if gotBuckets[bucket] != want {
			t.Fatalf("timing bucket %s = %d, want %d (all buckets %#v)", bucket, gotBuckets[bucket], want, gotBuckets)
		}
	}
}

func TestAgentAdapterApprovalMetricsTimeoutAndGrantCounters(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	apm, err := NewAgentAdapterApprovalMetricsWithMeterProvider(provider)
	if err != nil {
		t.Fatalf("NewAgentAdapterApprovalMetricsWithMeterProvider() error = %v", err)
	}

	apm.RecordTimedOut(context.Background(), AgentAdapterApprovalResolveRecord{
		AdapterID:  "codex",
		ToolKind:   "file_write",
		Mode:       "prompt",
		Path:       "timeout",
		Status:     "timed_out",
		DurationMS: 300_000,
	})
	apm.SeedGrantsActive(context.Background(), 2)
	apm.RecordGrantCreated(context.Background())
	apm.RecordGrantDeleted(context.Background())

	collected := collectMetrics(t, reader)

	timeouts := findMetric[metricdata.Sum[int64]](t, collected, MetricAgentAdapterApprovalTimedOutTotal)
	if len(timeouts.DataPoints) != 1 {
		t.Fatalf("timed-out data points = %d, want 1", len(timeouts.DataPoints))
	}
	if timeouts.DataPoints[0].Value != 1 {
		t.Fatalf("timed-out count = %d, want 1", timeouts.DataPoints[0].Value)
	}
	if got := attrValue(timeouts.DataPoints[0].Attributes, AttrHecateAgentAdapterID); got != "codex" {
		t.Errorf("%s = %q, want codex", AttrHecateAgentAdapterID, got)
	}
	if got := attrValue(timeouts.DataPoints[0].Attributes, AttrHecateAgentApprovalToolKind); got != "file_write" {
		t.Errorf("%s = %q, want file_write", AttrHecateAgentApprovalToolKind, got)
	}
	if got := attrValue(timeouts.DataPoints[0].Attributes, AttrHecateAgentApprovalMode); got != "prompt" {
		t.Errorf("%s = %q, want prompt", AttrHecateAgentApprovalMode, got)
	}

	grants := findMetric[metricdata.Sum[int64]](t, collected, MetricAgentAdapterApprovalGrantsActive)
	if len(grants.DataPoints) != 1 {
		t.Fatalf("grants-active data points = %d, want 1", len(grants.DataPoints))
	}
	if grants.DataPoints[0].Value != 2 {
		t.Fatalf("grants-active value = %d, want 2", grants.DataPoints[0].Value)
	}
	if grants.IsMonotonic {
		t.Fatal("grants-active must be non-monotonic because deletes decrement it")
	}
}

func collectMetrics(t *testing.T, reader *sdkmetric.ManualReader) metricdata.ResourceMetrics {
	t.Helper()

	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	return collected
}

func findMetric[T any](t *testing.T, collected metricdata.ResourceMetrics, name string) T {
	t.Helper()

	for _, scope := range collected.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name != name {
				continue
			}
			data, ok := metric.Data.(T)
			if !ok {
				t.Fatalf("metric %q type = %T, want requested type", name, metric.Data)
			}
			return data
		}
	}

	t.Fatalf("metric %q not found", name)
	var zero T
	return zero
}

func attrValue(set attribute.Set, key string) string {
	value, ok := set.Value(attribute.Key(key))
	if !ok {
		return ""
	}
	if value.Type() != attribute.STRING {
		return ""
	}
	return value.AsString()
}

func attrBool(set attribute.Set, key string) (bool, bool) {
	value, ok := set.Value(attribute.Key(key))
	if !ok || value.Type() != attribute.BOOL {
		return false, false
	}
	return value.AsBool(), true
}

func assertMetricHasNoDataPoints(t *testing.T, collected metricdata.ResourceMetrics, name string) {
	t.Helper()

	for _, scope := range collected.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name != name {
				continue
			}
			switch data := metric.Data.(type) {
			case metricdata.Sum[int64]:
				if len(data.DataPoints) != 0 {
					t.Fatalf("metric %q data points = %d, want 0", name, len(data.DataPoints))
				}
			case metricdata.Histogram[int64]:
				if len(data.DataPoints) != 0 {
					t.Fatalf("metric %q data points = %d, want 0", name, len(data.DataPoints))
				}
			default:
				t.Fatalf("metric %q type = %T, want sum or histogram", name, metric.Data)
			}
			return
		}
	}
}

// ===========================================================================
// Gateway metrics — additional dimension coverage
// ===========================================================================

// TestGatewayMetricsTokenCountersCarryProviderAndModelAttrs verifies that the
// three token counters (input, output, total) carry provider and model
// attributes, and that each counter receives the correct value.
func TestGatewayMetricsTokenCountersCarryProviderAndModelAttrs(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	m, err := NewMetricsWithMeterProvider(provider)
	if err != nil {
		t.Fatalf("NewMetricsWithMeterProvider() error = %v", err)
	}

	m.RecordChat(context.Background(), ChatMetricsRecord{
		Provider:         "anthropic",
		ProviderKind:     "cloud",
		RequestedModel:   "claude-sonnet-4-20250514",
		ResponseModel:    "claude-sonnet-4-20250514",
		PromptTokens:     100,
		CompletionTokens: 25,
		TotalTokens:      125,
	})

	collected := collectMetrics(t, reader)

	cases := []struct {
		name    string
		wantVal int64
	}{
		{MetricInputTokensTotal, 100},
		{MetricOutputTokensTotal, 25},
		{MetricTotalTokensTotal, 125},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data := findMetric[metricdata.Sum[int64]](t, collected, tc.name)
			if len(data.DataPoints) == 0 {
				t.Fatalf("no data points for %q", tc.name)
			}
			dp := data.DataPoints[0]
			if dp.Value != tc.wantVal {
				t.Errorf("value = %d, want %d", dp.Value, tc.wantVal)
			}
			if got := attrValue(dp.Attributes, AttrGenAIProviderName); got != "anthropic" {
				t.Errorf("%s = %q, want anthropic", AttrGenAIProviderName, got)
			}
			if got := attrValue(dp.Attributes, AttrGenAIRequestModel); got != "claude-sonnet-4-20250514" {
				t.Errorf("%s = %q, want claude-sonnet-4-20250514", AttrGenAIRequestModel, got)
			}
		})
	}
}

// TestGatewayMetricsZeroTokensNotRecorded verifies that token counters are not
// incremented when the corresponding token fields are zero.
func TestGatewayMetricsZeroTokensNotRecorded(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	m, err := NewMetricsWithMeterProvider(provider)
	if err != nil {
		t.Fatalf("NewMetricsWithMeterProvider() error = %v", err)
	}

	// PromptTokens, CompletionTokens, TotalTokens all zero → counters must stay
	// at zero and therefore produce no data points.
	m.RecordChat(context.Background(), ChatMetricsRecord{
		Provider:       "openai",
		RequestedModel: "gpt-4o-mini",
	})

	collected := collectMetrics(t, reader)

	for _, name := range []string{MetricInputTokensTotal, MetricOutputTokensTotal, MetricTotalTokensTotal} {
		for _, scope := range collected.ScopeMetrics {
			for _, metric := range scope.Metrics {
				if metric.Name != name {
					continue
				}
				data, ok := metric.Data.(metricdata.Sum[int64])
				if !ok {
					continue
				}
				for _, dp := range data.DataPoints {
					if dp.Value > 0 {
						t.Errorf("metric %q: unexpected non-zero data point %d with zero input", name, dp.Value)
					}
				}
			}
		}
	}
}

// TestGatewayMetricsMultipleResultsBucketedSeparately verifies that recording
// requests with different result values produces separate data points —
// one bucket per unique attribute set.
func TestGatewayMetricsMultipleResultsBucketedSeparately(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	m, err := NewMetricsWithMeterProvider(provider)
	if err != nil {
		t.Fatalf("NewMetricsWithMeterProvider() error = %v", err)
	}

	ctx := context.Background()
	m.RecordRequestOutcome(ctx, ResultSuccess, 10*time.Millisecond)
	m.RecordRequestOutcome(ctx, ResultSuccess, 20*time.Millisecond)
	m.RecordRequestOutcome(ctx, ResultError, 5*time.Millisecond)

	collected := collectMetrics(t, reader)
	data := findMetric[metricdata.Sum[int64]](t, collected, MetricGatewayRequests)

	// Must have exactly 2 buckets: one for success, one for error.
	if len(data.DataPoints) != 2 {
		t.Fatalf("data points = %d, want 2 (one per result value)", len(data.DataPoints))
	}

	buckets := make(map[string]int64, 2)
	for _, dp := range data.DataPoints {
		buckets[attrValue(dp.Attributes, AttrHecateResult)] = dp.Value
	}
	if buckets[ResultSuccess] != 2 {
		t.Errorf("success count = %d, want 2", buckets[ResultSuccess])
	}
	if buckets[ResultError] != 1 {
		t.Errorf("error count = %d, want 1", buckets[ResultError])
	}
}

// ===========================================================================
// Orchestrator metrics — full attribute dimension coverage
// ===========================================================================

// TestOrchestratorMetricsRunAttributeDimensions verifies that RecordRun emits
// hecate.run.status, hecate.execution.kind, and gen_ai.request.model on both
// the runs counter and the run-duration histogram.
func TestOrchestratorMetricsRunAttributeDimensions(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	om, err := NewOrchestratorMetricsWithMeterProvider(provider)
	if err != nil {
		t.Fatalf("NewOrchestratorMetricsWithMeterProvider() error = %v", err)
	}

	om.RecordRun(context.Background(), RunMetricsRecord{
		Status:        "completed",
		ExecutionKind: "agent_loop",
		Model:         "claude-sonnet-4-20250514",
		DurationMS:    1500,
	})

	collected := collectMetrics(t, reader)

	// Counter
	runs := findMetric[metricdata.Sum[int64]](t, collected, MetricOrchestratorRunsTotal)
	if len(runs.DataPoints) != 1 {
		t.Fatalf("runs data points = %d, want 1", len(runs.DataPoints))
	}
	if runs.DataPoints[0].Value != 1 {
		t.Errorf("runs count = %d, want 1", runs.DataPoints[0].Value)
	}
	dp := runs.DataPoints[0]
	if got := attrValue(dp.Attributes, AttrHecateRunStatus); got != "completed" {
		t.Errorf("%s = %q, want completed", AttrHecateRunStatus, got)
	}
	if got := attrValue(dp.Attributes, AttrHecateExecutionKind); got != "agent_loop" {
		t.Errorf("%s = %q, want agent_loop", AttrHecateExecutionKind, got)
	}
	if got := attrValue(dp.Attributes, AttrGenAIRequestModel); got != "claude-sonnet-4-20250514" {
		t.Errorf("%s = %q, want claude-sonnet-4-20250514", AttrGenAIRequestModel, got)
	}

	// Histogram
	dur := findMetric[metricdata.Histogram[int64]](t, collected, MetricOrchestratorRunDuration)
	if len(dur.DataPoints) != 1 {
		t.Fatalf("run duration data points = %d, want 1", len(dur.DataPoints))
	}
	if dur.DataPoints[0].Sum != 1500 {
		t.Errorf("run duration sum = %d, want 1500", dur.DataPoints[0].Sum)
	}
	durDP := dur.DataPoints[0]
	if got := attrValue(durDP.Attributes, AttrHecateRunStatus); got != "completed" {
		t.Errorf("duration dp %s = %q, want completed", AttrHecateRunStatus, got)
	}
	if got := attrValue(durDP.Attributes, AttrHecateExecutionKind); got != "agent_loop" {
		t.Errorf("duration dp %s = %q, want agent_loop", AttrHecateExecutionKind, got)
	}
}

// TestOrchestratorMetricsRunZeroDurationCountsButSkipsHistogram verifies the
// guard: RecordRun with DurationMS=0 increments the counter but does not add a
// histogram observation.
func TestOrchestratorMetricsRunZeroDurationCountsButSkipsHistogram(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	om, err := NewOrchestratorMetricsWithMeterProvider(provider)
	if err != nil {
		t.Fatalf("NewOrchestratorMetricsWithMeterProvider() error = %v", err)
	}

	om.RecordRun(context.Background(), RunMetricsRecord{
		Status:     "completed",
		DurationMS: 0, // zero — must not be recorded in histogram
	})

	collected := collectMetrics(t, reader)

	// Counter must have one point.
	runs := findMetric[metricdata.Sum[int64]](t, collected, MetricOrchestratorRunsTotal)
	if len(runs.DataPoints) == 0 || runs.DataPoints[0].Value != 1 {
		t.Errorf("runs counter not incremented for zero-duration run")
	}

	// Histogram must have no observations.
	for _, scope := range collected.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name != MetricOrchestratorRunDuration {
				continue
			}
			data, ok := metric.Data.(metricdata.Histogram[int64])
			if !ok {
				continue
			}
			for _, dp := range data.DataPoints {
				if dp.Count > 0 {
					t.Errorf("run duration histogram has %d observation(s), want 0 for DurationMS=0", dp.Count)
				}
			}
		}
	}
}

// TestOrchestratorMetricsStepAttributeDimensions verifies that RecordStep emits
// hecate.step.kind and hecate.result on both the step counter and step-duration
// histogram.
func TestOrchestratorMetricsStepAttributeDimensions(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	om, err := NewOrchestratorMetricsWithMeterProvider(provider)
	if err != nil {
		t.Fatalf("NewOrchestratorMetricsWithMeterProvider() error = %v", err)
	}

	om.RecordStep(context.Background(), StepMetricsRecord{
		StepKind:   "shell",
		Result:     ResultSuccess,
		DurationMS: 300,
	})

	collected := collectMetrics(t, reader)

	steps := findMetric[metricdata.Sum[int64]](t, collected, MetricOrchestratorStepsTotal)
	if len(steps.DataPoints) != 1 {
		t.Fatalf("steps data points = %d, want 1", len(steps.DataPoints))
	}
	if steps.DataPoints[0].Value != 1 {
		t.Errorf("steps count = %d, want 1", steps.DataPoints[0].Value)
	}
	dp := steps.DataPoints[0]
	if got := attrValue(dp.Attributes, AttrHecateStepKind); got != "shell" {
		t.Errorf("%s = %q, want shell", AttrHecateStepKind, got)
	}
	if got := attrValue(dp.Attributes, AttrHecateResult); got != ResultSuccess {
		t.Errorf("%s = %q, want %s", AttrHecateResult, got, ResultSuccess)
	}

	dur := findMetric[metricdata.Histogram[int64]](t, collected, MetricOrchestratorStepDuration)
	if len(dur.DataPoints) != 1 || dur.DataPoints[0].Sum != 300 {
		t.Errorf("step duration sum = %d, want 300", dur.DataPoints[0].Sum)
	}
	if got := attrValue(dur.DataPoints[0].Attributes, AttrHecateStepKind); got != "shell" {
		t.Errorf("duration dp %s = %q, want shell", AttrHecateStepKind, got)
	}
}

// TestOrchestratorMetricsApprovalAttributeDimensions verifies that RecordApproval
// emits hecate.approval.kind and hecate.approval.decision on both the approvals
// counter and the approval-wait-duration histogram.
func TestOrchestratorMetricsApprovalAttributeDimensions(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	om, err := NewOrchestratorMetricsWithMeterProvider(provider)
	if err != nil {
		t.Fatalf("NewOrchestratorMetricsWithMeterProvider() error = %v", err)
	}

	om.RecordApproval(context.Background(), ApprovalMetricsRecord{
		ApprovalKind: "shell_command",
		Decision:     "approved",
		WaitMS:       4500,
	})

	collected := collectMetrics(t, reader)

	approvals := findMetric[metricdata.Sum[int64]](t, collected, MetricOrchestratorApprovalsTotal)
	if len(approvals.DataPoints) != 1 {
		t.Fatalf("approvals data points = %d, want 1", len(approvals.DataPoints))
	}
	dp := approvals.DataPoints[0]
	if got := attrValue(dp.Attributes, AttrHecateApprovalKind); got != "shell_command" {
		t.Errorf("%s = %q, want shell_command", AttrHecateApprovalKind, got)
	}
	if got := attrValue(dp.Attributes, AttrHecateApprovalDecision); got != "approved" {
		t.Errorf("%s = %q, want approved", AttrHecateApprovalDecision, got)
	}

	wait := findMetric[metricdata.Histogram[int64]](t, collected, MetricOrchestratorApprovalWaitDuration)
	if len(wait.DataPoints) != 1 || wait.DataPoints[0].Sum != 4500 {
		t.Errorf("approval wait duration sum = %d, want 4500", wait.DataPoints[0].Sum)
	}
	waitDP := wait.DataPoints[0]
	if got := attrValue(waitDP.Attributes, AttrHecateApprovalDecision); got != "approved" {
		t.Errorf("wait dp %s = %q, want approved", AttrHecateApprovalDecision, got)
	}
}

// TestOrchestratorMetricsQueueWaitAttributeDimension verifies that RecordQueueWait
// emits hecate.queue.backend on the queue-wait histogram.
func TestOrchestratorMetricsQueueWaitAttributeDimension(t *testing.T) {
	t.Parallel()

	cases := []string{"sqlite", "postgres"}
	for _, backend := range cases {
		t.Run(backend, func(t *testing.T) {
			t.Parallel()

			reader := sdkmetric.NewManualReader()
			provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			om, err := NewOrchestratorMetricsWithMeterProvider(provider)
			if err != nil {
				t.Fatalf("NewOrchestratorMetricsWithMeterProvider() error = %v", err)
			}

			om.RecordQueueWait(context.Background(), QueueWaitRecord{
				QueueBackend: backend,
				WaitMS:       250,
			})

			collected := collectMetrics(t, reader)

			wait := findMetric[metricdata.Histogram[int64]](t, collected, MetricOrchestratorQueueWaitDuration)
			if len(wait.DataPoints) != 1 {
				t.Fatalf("queue wait data points = %d, want 1", len(wait.DataPoints))
			}
			if wait.DataPoints[0].Sum != 250 {
				t.Errorf("queue wait sum = %d, want 250", wait.DataPoints[0].Sum)
			}
			if got := attrValue(wait.DataPoints[0].Attributes, AttrHecateQueueBackend); got != backend {
				t.Errorf("%s = %q, want %s", AttrHecateQueueBackend, got, backend)
			}
		})
	}
}

// TestOrchestratorMetricsQueueWaitZeroSkipped verifies that RecordQueueWait
// with WaitMS=0 records nothing (the guard `if m == nil || rec.WaitMS <= 0`).
func TestOrchestratorMetricsQueueWaitZeroSkipped(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	om, err := NewOrchestratorMetricsWithMeterProvider(provider)
	if err != nil {
		t.Fatalf("NewOrchestratorMetricsWithMeterProvider() error = %v", err)
	}

	om.RecordQueueWait(context.Background(), QueueWaitRecord{QueueBackend: "memory", WaitMS: 0})

	collected := collectMetrics(t, reader)

	for _, scope := range collected.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name != MetricOrchestratorQueueWaitDuration {
				continue
			}
			data, ok := metric.Data.(metricdata.Histogram[int64])
			if !ok {
				continue
			}
			for _, dp := range data.DataPoints {
				if dp.Count > 0 {
					t.Errorf("queue wait histogram has %d observation(s), want 0 for WaitMS=0", dp.Count)
				}
			}
		}
	}
}

// TestOrchestratorMetricsLeaseExtendFailuresIncrements verifies that
// RecordLeaseExtendFailed increments the counter.
func TestOrchestratorMetricsLeaseExtendFailuresIncrements(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	om, err := NewOrchestratorMetricsWithMeterProvider(provider)
	if err != nil {
		t.Fatalf("NewOrchestratorMetricsWithMeterProvider() error = %v", err)
	}

	ctx := context.Background()
	om.RecordLeaseExtendFailed(ctx)
	om.RecordLeaseExtendFailed(ctx)

	collected := collectMetrics(t, reader)

	failures := findMetric[metricdata.Sum[int64]](t, collected, MetricOrchestratorLeaseExtendFailures)
	if len(failures.DataPoints) == 0 {
		t.Fatal("no data points for lease extend failures counter")
	}
	total := int64(0)
	for _, dp := range failures.DataPoints {
		total += dp.Value
	}
	if total != 2 {
		t.Errorf("lease extend failure count = %d, want 2", total)
	}
}

// TestOrchestratorMetricsNilSafe verifies that all OrchestratorMetrics methods
// are safe to call on a nil receiver without panicking.
func TestOrchestratorMetricsNilSafe(t *testing.T) {
	t.Parallel()

	var om *OrchestratorMetrics
	ctx := context.Background()

	// These must not panic.
	om.RecordRun(ctx, RunMetricsRecord{Status: "completed", DurationMS: 1})
	om.RecordStep(ctx, StepMetricsRecord{StepKind: "shell", Result: ResultSuccess, DurationMS: 1})
	om.RecordApproval(ctx, ApprovalMetricsRecord{ApprovalKind: "shell_command", Decision: "approved", WaitMS: 1})
	om.RecordQueueWait(ctx, QueueWaitRecord{QueueBackend: "memory", WaitMS: 1})
	om.RecordLeaseExtendFailed(ctx)
}

// TestOrchestratorMetricsRecordMCPToolCallEmitsCounterAndHistogram pins
// that the new MCP-tool-call instrument records both the counter
// increment and a duration sample with the expected attribute set.
// Operators chart this to spot slow upstream servers and to count
// dispatches per server / tool / outcome.
func TestOrchestratorMetricsRecordMCPToolCallEmitsCounterAndHistogram(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	om, err := NewOrchestratorMetricsWithMeterProvider(provider)
	if err != nil {
		t.Fatalf("NewOrchestratorMetricsWithMeterProvider: %v", err)
	}

	om.RecordMCPToolCall(context.Background(), MCPToolCallRecord{
		Server:     "github",
		Tool:       "create_pr",
		Result:     MCPCallResultDispatched,
		DurationMS: 250,
	})

	collected := collectMetrics(t, reader)

	calls := findMetric[metricdata.Sum[int64]](t, collected, MetricOrchestratorMCPToolCallsTotal)
	if len(calls.DataPoints) != 1 {
		t.Fatalf("call data points = %d, want 1", len(calls.DataPoints))
	}
	if calls.DataPoints[0].Value != 1 {
		t.Fatalf("call count = %d, want 1", calls.DataPoints[0].Value)
	}
	for k, want := range map[string]string{
		AttrHecateMCPServer:     "github",
		AttrHecateMCPTool:       "create_pr",
		AttrHecateMCPCallResult: MCPCallResultDispatched,
	} {
		if got := attrValue(calls.DataPoints[0].Attributes, k); got != want {
			t.Errorf("attr %s = %q, want %q", k, got, want)
		}
	}

	dur := findMetric[metricdata.Histogram[int64]](t, collected, MetricOrchestratorMCPToolCallDuration)
	if len(dur.DataPoints) != 1 {
		t.Fatalf("duration data points = %d, want 1", len(dur.DataPoints))
	}
	if dur.DataPoints[0].Sum != 250 {
		t.Errorf("duration sum = %d, want 250", dur.DataPoints[0].Sum)
	}
}

// TestOrchestratorMetricsRecordMCPCacheEvent pins the cache-events
// counter: hit/miss/evicted each land on a distinct attribute set.
// Operators read the hit:miss ratio to tell whether the cache is
// doing useful work; eviction count answers "is the TTL too short?".
func TestOrchestratorMetricsRecordMCPCacheEvent(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	om, err := NewOrchestratorMetricsWithMeterProvider(provider)
	if err != nil {
		t.Fatalf("NewOrchestratorMetricsWithMeterProvider: %v", err)
	}

	om.RecordMCPCacheEvent(context.Background(), MCPCacheEventRecord{Server: "github", Event: MCPCacheEventHit})
	om.RecordMCPCacheEvent(context.Background(), MCPCacheEventRecord{Server: "github", Event: MCPCacheEventMiss})
	om.RecordMCPCacheEvent(context.Background(), MCPCacheEventRecord{Server: "", Event: MCPCacheEventEvicted})

	collected := collectMetrics(t, reader)
	events := findMetric[metricdata.Sum[int64]](t, collected, MetricOrchestratorMCPCacheEventsTotal)
	if len(events.DataPoints) != 3 {
		t.Fatalf("cache event data points = %d, want 3 (hit/miss/evicted)", len(events.DataPoints))
	}
	// Verify each event kind is present exactly once.
	seen := map[string]int{}
	for _, dp := range events.DataPoints {
		ev := attrValue(dp.Attributes, AttrHecateMCPCacheEvent)
		seen[ev] += int(dp.Value)
	}
	for _, want := range []string{MCPCacheEventHit, MCPCacheEventMiss, MCPCacheEventEvicted} {
		if seen[want] != 1 {
			t.Errorf("event %q count = %d, want 1", want, seen[want])
		}
	}
}

// TestOrchestratorMetricsRecordMCPToolCallSkipsHistogramOnZeroDuration:
// a 0-duration record (which can happen on extremely-fast block paths
// when the wall clock hasn't ticked) increments the counter but does
// NOT add a 0 to the histogram — that would skew the latency
// distribution toward zero in dashboards.
func TestOrchestratorMetricsRecordMCPToolCallSkipsHistogramOnZeroDuration(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	om, err := NewOrchestratorMetricsWithMeterProvider(provider)
	if err != nil {
		t.Fatalf("NewOrchestratorMetricsWithMeterProvider: %v", err)
	}

	om.RecordMCPToolCall(context.Background(), MCPToolCallRecord{
		Server: "fs", Tool: "read", Result: MCPCallResultBlocked, DurationMS: 0,
	})
	collected := collectMetrics(t, reader)

	calls := findMetric[metricdata.Sum[int64]](t, collected, MetricOrchestratorMCPToolCallsTotal)
	if calls.DataPoints[0].Value != 1 {
		t.Errorf("counter not incremented: %d", calls.DataPoints[0].Value)
	}
	// The histogram metric may or may not be registered depending on
	// SDK behavior on 0 samples; what we DO require is that if it's
	// present, it carries 0 data points (no sample recorded).
	for _, scope := range collected.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != MetricOrchestratorMCPToolCallDuration {
				continue
			}
			if h, ok := m.Data.(metricdata.Histogram[int64]); ok && len(h.DataPoints) > 0 {
				t.Errorf("histogram has %d data point(s), want 0 on 0-ms record", len(h.DataPoints))
			}
		}
	}
}
