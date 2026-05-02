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
		ExecutionKind: "agent",
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
	if got := attrValue(dp.Attributes, AttrHecateExecutionKind); got != "agent" {
		t.Errorf("%s = %q, want agent", AttrHecateExecutionKind, got)
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
	if got := attrValue(durDP.Attributes, AttrHecateExecutionKind); got != "agent" {
		t.Errorf("duration dp %s = %q, want agent", AttrHecateExecutionKind, got)
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

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	om, err := NewOrchestratorMetricsWithMeterProvider(provider)
	if err != nil {
		t.Fatalf("NewOrchestratorMetricsWithMeterProvider() error = %v", err)
	}

	om.RecordQueueWait(context.Background(), QueueWaitRecord{
		QueueBackend: "postgres",
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
	if got := attrValue(wait.DataPoints[0].Attributes, AttrHecateQueueBackend); got != "postgres" {
		t.Errorf("%s = %q, want postgres", AttrHecateQueueBackend, got)
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
