package telemetry

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otmetric "go.opentelemetry.io/otel/metric"
)

type ChatMetricsRecord struct {
	Provider             string
	ProviderKind         string
	RequestedModel       string
	ResponseModel        string
	CostMicrosUSD        int64
	PromptTokens         int64
	CompletionTokens     int64
	TotalTokens          int64
	RetryCount           int
	FallbackFromProvider string
}

type Metrics struct {
	requestsTotal         otmetric.Int64Counter
	requestDuration       otmetric.Int64Histogram
	chatRequestsTotal     otmetric.Int64Counter
	costMicrosTotal       otmetric.Int64Counter
	promptTokensTotal     otmetric.Int64Counter
	completionTokensTotal otmetric.Int64Counter
	totalTokensTotal      otmetric.Int64Counter
	retriesTotal          otmetric.Int64Counter
	failoversTotal        otmetric.Int64Counter
}

func NewMetrics() *Metrics {
	metrics, err := NewMetricsWithMeterProvider(otel.GetMeterProvider())
	if err != nil {
		return &Metrics{}
	}
	return metrics
}

func NewMetricsWithMeterProvider(provider otmetric.MeterProvider) (*Metrics, error) {
	if provider == nil {
		provider = otel.GetMeterProvider()
	}

	meter := provider.Meter("github.com/hecate/agent-runtime/internal/telemetry")

	requestsTotal, err := meter.Int64Counter(
		"hecate.gateway.requests",
		otmetric.WithDescription("Total gateway requests grouped by result."),
		otmetric.WithUnit("{request}"),
	)
	if err != nil {
		return nil, err
	}

	requestDuration, err := meter.Int64Histogram(
		"hecate.gateway.request.duration",
		otmetric.WithDescription("Gateway request duration."),
		otmetric.WithUnit("ms"),
	)
	if err != nil {
		return nil, err
	}

	chatRequestsTotal, err := meter.Int64Counter(
		"gen_ai.gateway.chat.requests",
		otmetric.WithDescription("Total chat completion responses finalized by the gateway."),
		otmetric.WithUnit("{request}"),
	)
	if err != nil {
		return nil, err
	}

	costMicrosTotal, err := meter.Int64Counter(
		"gen_ai.gateway.cost",
		otmetric.WithDescription("Accumulated estimated cost for chat responses."),
		otmetric.WithUnit("1"),
	)
	if err != nil {
		return nil, err
	}

	promptTokensTotal, err := meter.Int64Counter(
		"gen_ai.client.tokens.input",
		otmetric.WithDescription("Accumulated prompt tokens."),
		otmetric.WithUnit("{token}"),
	)
	if err != nil {
		return nil, err
	}

	completionTokensTotal, err := meter.Int64Counter(
		"gen_ai.client.tokens.output",
		otmetric.WithDescription("Accumulated completion tokens."),
		otmetric.WithUnit("{token}"),
	)
	if err != nil {
		return nil, err
	}

	totalTokensTotal, err := meter.Int64Counter(
		"gen_ai.client.tokens.total",
		otmetric.WithDescription("Accumulated total tokens."),
		otmetric.WithUnit("{token}"),
	)
	if err != nil {
		return nil, err
	}

	retriesTotal, err := meter.Int64Counter(
		"hecate.gateway.retries",
		otmetric.WithDescription("Total provider retry attempts beyond the first request attempt."),
		otmetric.WithUnit("{retry}"),
	)
	if err != nil {
		return nil, err
	}

	failoversTotal, err := meter.Int64Counter(
		"hecate.gateway.failovers",
		otmetric.WithDescription("Total provider failover events."),
		otmetric.WithUnit("{failover}"),
	)
	if err != nil {
		return nil, err
	}

	return &Metrics{
		requestsTotal:         requestsTotal,
		requestDuration:       requestDuration,
		chatRequestsTotal:     chatRequestsTotal,
		costMicrosTotal:       costMicrosTotal,
		promptTokensTotal:     promptTokensTotal,
		completionTokensTotal: completionTokensTotal,
		totalTokensTotal:      totalTokensTotal,
		retriesTotal:          retriesTotal,
		failoversTotal:        failoversTotal,
	}, nil
}

func (m *Metrics) RecordRequestOutcome(ctx context.Context, result string, duration time.Duration) {
	if m == nil || result == "" {
		return
	}

	attrs := otmetric.WithAttributes(attribute.String(AttrHecateResult, result))
	m.requestsTotal.Add(ctx, 1, attrs)
	m.requestDuration.Record(ctx, duration.Milliseconds(), attrs)
}

// ---------------------------------------------------------------------------
// OrchestratorMetrics
// ---------------------------------------------------------------------------

// RunMetricsRecord carries the labels and measurements for one completed run.
type RunMetricsRecord struct {
	TaskID        string
	RunID         string
	Status        string // completed | failed | cancelled
	ExecutionKind string
	Model         string
	DurationMS    int64
}

// StepMetricsRecord carries the labels and measurements for one completed step.
type StepMetricsRecord struct {
	TaskID     string
	RunID      string
	StepKind   string
	Result     string // success | error
	DurationMS int64
}

// ApprovalMetricsRecord carries the labels and measurements for one resolved
// approval gate.
type ApprovalMetricsRecord struct {
	TaskID       string
	RunID        string
	ApprovalKind string
	Decision     string // approved | rejected
	WaitMS       int64
}

// QueueWaitRecord carries the labels and measurements for the time a run spent
// sitting in the queue before being claimed by a worker.
type QueueWaitRecord struct {
	TaskID       string
	RunID        string
	QueueBackend string
	WaitMS       int64
}

// OrchestratorMetrics records SLO-critical signals for the orchestrator
// subsystem: run/step throughput and latency, approval gate wait, queue wait,
// and lease-extend failure counts.
type OrchestratorMetrics struct {
	runsTotal            otmetric.Int64Counter
	runDuration          otmetric.Int64Histogram
	queueWaitDuration    otmetric.Int64Histogram
	stepsTotal           otmetric.Int64Counter
	stepDuration         otmetric.Int64Histogram
	approvalsTotal       otmetric.Int64Counter
	approvalWaitDuration otmetric.Int64Histogram
	leaseExtendFailures  otmetric.Int64Counter
	mcpToolCallsTotal    otmetric.Int64Counter
	mcpToolCallDuration  otmetric.Int64Histogram
	mcpCacheEventsTotal  otmetric.Int64Counter
}

// NewOrchestratorMetrics registers all orchestrator instruments against
// the global MeterProvider.
func NewOrchestratorMetrics() *OrchestratorMetrics {
	m, err := NewOrchestratorMetricsWithMeterProvider(otel.GetMeterProvider())
	if err != nil {
		return &OrchestratorMetrics{}
	}
	return m
}

// NewOrchestratorMetricsWithMeterProvider registers instruments against the
// supplied provider. Used in tests where a ManualReader is injected.
func NewOrchestratorMetricsWithMeterProvider(provider otmetric.MeterProvider) (*OrchestratorMetrics, error) {
	if provider == nil {
		provider = otel.GetMeterProvider()
	}
	meter := provider.Meter("github.com/hecate/agent-runtime/internal/telemetry")

	runsTotal, err := meter.Int64Counter(
		MetricOrchestratorRunsTotal,
		otmetric.WithDescription("Total orchestrator runs grouped by status."),
		otmetric.WithUnit("{run}"),
	)
	if err != nil {
		return nil, err
	}

	runDuration, err := meter.Int64Histogram(
		MetricOrchestratorRunDuration,
		otmetric.WithDescription("Orchestrator run wall-clock duration."),
		otmetric.WithUnit("ms"),
	)
	if err != nil {
		return nil, err
	}

	queueWaitDuration, err := meter.Int64Histogram(
		MetricOrchestratorQueueWaitDuration,
		otmetric.WithDescription("Time a run spent waiting in the queue before being claimed."),
		otmetric.WithUnit("ms"),
	)
	if err != nil {
		return nil, err
	}

	stepsTotal, err := meter.Int64Counter(
		MetricOrchestratorStepsTotal,
		otmetric.WithDescription("Total orchestrator steps grouped by kind and result."),
		otmetric.WithUnit("{step}"),
	)
	if err != nil {
		return nil, err
	}

	stepDuration, err := meter.Int64Histogram(
		MetricOrchestratorStepDuration,
		otmetric.WithDescription("Orchestrator step wall-clock duration."),
		otmetric.WithUnit("ms"),
	)
	if err != nil {
		return nil, err
	}

	approvalsTotal, err := meter.Int64Counter(
		MetricOrchestratorApprovalsTotal,
		otmetric.WithDescription("Total approval gates resolved, grouped by kind and decision."),
		otmetric.WithUnit("{approval}"),
	)
	if err != nil {
		return nil, err
	}

	approvalWaitDuration, err := meter.Int64Histogram(
		MetricOrchestratorApprovalWaitDuration,
		otmetric.WithDescription("Time a run spent waiting for an approval gate to be resolved."),
		otmetric.WithUnit("ms"),
	)
	if err != nil {
		return nil, err
	}

	leaseExtendFailures, err := meter.Int64Counter(
		MetricOrchestratorLeaseExtendFailures,
		otmetric.WithDescription("Total queue lease extension failures."),
		otmetric.WithUnit("{failure}"),
	)
	if err != nil {
		return nil, err
	}

	mcpToolCallsTotal, err := meter.Int64Counter(
		MetricOrchestratorMCPToolCallsTotal,
		otmetric.WithDescription("Total MCP tool dispatches grouped by server, tool, and result (dispatched | tool_error | failed | blocked)."),
		otmetric.WithUnit("{call}"),
	)
	if err != nil {
		return nil, err
	}

	mcpToolCallDuration, err := meter.Int64Histogram(
		MetricOrchestratorMCPToolCallDuration,
		otmetric.WithDescription("MCP tool dispatch wall-clock duration."),
		otmetric.WithUnit("ms"),
	)
	if err != nil {
		return nil, err
	}

	mcpCacheEventsTotal, err := meter.Int64Counter(
		MetricOrchestratorMCPCacheEventsTotal,
		otmetric.WithDescription("MCP shared-client cache events grouped by event (hit | miss | evicted) and server."),
		otmetric.WithUnit("{event}"),
	)
	if err != nil {
		return nil, err
	}

	return &OrchestratorMetrics{
		runsTotal:            runsTotal,
		runDuration:          runDuration,
		queueWaitDuration:    queueWaitDuration,
		stepsTotal:           stepsTotal,
		stepDuration:         stepDuration,
		approvalsTotal:       approvalsTotal,
		approvalWaitDuration: approvalWaitDuration,
		leaseExtendFailures:  leaseExtendFailures,
		mcpToolCallsTotal:    mcpToolCallsTotal,
		mcpToolCallDuration:  mcpToolCallDuration,
		mcpCacheEventsTotal:  mcpCacheEventsTotal,
	}, nil
}

// RecordRun records a completed run counter increment and duration sample.
func (m *OrchestratorMetrics) RecordRun(ctx context.Context, rec RunMetricsRecord) {
	if m == nil {
		return
	}
	attrs := make([]attribute.KeyValue, 0, 4)
	if rec.Status != "" {
		attrs = append(attrs, attribute.String(AttrHecateRunStatus, rec.Status))
	}
	if rec.ExecutionKind != "" {
		attrs = append(attrs, attribute.String(AttrHecateExecutionKind, rec.ExecutionKind))
	}
	if rec.Model != "" {
		attrs = append(attrs, attribute.String(AttrGenAIRequestModel, rec.Model))
	}
	opt := otmetric.WithAttributes(attrs...)
	m.runsTotal.Add(ctx, 1, opt)
	if rec.DurationMS > 0 {
		m.runDuration.Record(ctx, rec.DurationMS, opt)
	}
}

// RecordStep records a completed step counter increment and duration sample.
func (m *OrchestratorMetrics) RecordStep(ctx context.Context, rec StepMetricsRecord) {
	if m == nil {
		return
	}
	attrs := make([]attribute.KeyValue, 0, 2)
	if rec.StepKind != "" {
		attrs = append(attrs, attribute.String(AttrHecateStepKind, rec.StepKind))
	}
	if rec.Result != "" {
		attrs = append(attrs, attribute.String(AttrHecateResult, rec.Result))
	}
	opt := otmetric.WithAttributes(attrs...)
	m.stepsTotal.Add(ctx, 1, opt)
	if rec.DurationMS > 0 {
		m.stepDuration.Record(ctx, rec.DurationMS, opt)
	}
}

// RecordApproval records a resolved approval gate counter increment and wait
// duration sample.
func (m *OrchestratorMetrics) RecordApproval(ctx context.Context, rec ApprovalMetricsRecord) {
	if m == nil {
		return
	}
	attrs := make([]attribute.KeyValue, 0, 2)
	if rec.ApprovalKind != "" {
		attrs = append(attrs, attribute.String(AttrHecateApprovalKind, rec.ApprovalKind))
	}
	if rec.Decision != "" {
		attrs = append(attrs, attribute.String(AttrHecateApprovalDecision, rec.Decision))
	}
	opt := otmetric.WithAttributes(attrs...)
	m.approvalsTotal.Add(ctx, 1, opt)
	if rec.WaitMS > 0 {
		m.approvalWaitDuration.Record(ctx, rec.WaitMS, opt)
	}
}

// RecordQueueWait records the time a run spent waiting in the queue before
// being claimed.
func (m *OrchestratorMetrics) RecordQueueWait(ctx context.Context, rec QueueWaitRecord) {
	if m == nil || rec.WaitMS <= 0 {
		return
	}
	attrs := make([]attribute.KeyValue, 0, 1)
	if rec.QueueBackend != "" {
		attrs = append(attrs, attribute.String(AttrHecateQueueBackend, rec.QueueBackend))
	}
	m.queueWaitDuration.Record(ctx, rec.WaitMS, otmetric.WithAttributes(attrs...))
}

// RecordLeaseExtendFailed increments the lease-extend failure counter.
func (m *OrchestratorMetrics) RecordLeaseExtendFailed(ctx context.Context) {
	if m == nil {
		return
	}
	m.leaseExtendFailures.Add(ctx, 1)
}

// MCPToolCallRecord carries the labels and measurements for one MCP
// tool dispatch attempt. Result takes one of MCPCallResult* (see
// contract.go); duration is wall-clock from the moment the agent loop
// decided to dispatch (or block) until the result was in hand.
type MCPToolCallRecord struct {
	Server     string
	Tool       string
	Result     string // dispatched | tool_error | failed | blocked
	DurationMS int64
}

// RecordMCPToolCall records one MCP tool dispatch outcome. Counter
// increments by attribute set; histogram records duration when > 0
// (Blocked outcomes typically record sub-millisecond durations, which
// would skew the histogram floor — we still record them so operators
// can see "block path is fast" rather than guessing).
func (m *OrchestratorMetrics) RecordMCPToolCall(ctx context.Context, rec MCPToolCallRecord) {
	if m == nil {
		return
	}
	attrs := make([]attribute.KeyValue, 0, 3)
	if rec.Server != "" {
		attrs = append(attrs, attribute.String(AttrHecateMCPServer, rec.Server))
	}
	if rec.Tool != "" {
		attrs = append(attrs, attribute.String(AttrHecateMCPTool, rec.Tool))
	}
	if rec.Result != "" {
		attrs = append(attrs, attribute.String(AttrHecateMCPCallResult, rec.Result))
	}
	opt := otmetric.WithAttributes(attrs...)
	m.mcpToolCallsTotal.Add(ctx, 1, opt)
	if rec.DurationMS > 0 {
		m.mcpToolCallDuration.Record(ctx, rec.DurationMS, opt)
	}
}

// MCPCacheEventRecord carries the labels for a single
// SharedClientCache event. Server is the operator-chosen alias from
// the per-task config (not part of the cache key, but useful as a
// telemetry attribute so operators can see which upstream is being
// hit/missed); blank when the event is server-agnostic.
type MCPCacheEventRecord struct {
	Server string
	Event  string // hit | miss | evicted
}

// RecordMCPCacheEvent records one cache hit/miss/eviction. Cheap
// enough to call from inside the cache's lock — only an Add against
// a counter. Counters with no Server attribute are still useful for
// answering "is the cache doing useful work?" via the hit:miss ratio.
func (m *OrchestratorMetrics) RecordMCPCacheEvent(ctx context.Context, rec MCPCacheEventRecord) {
	if m == nil {
		return
	}
	attrs := make([]attribute.KeyValue, 0, 2)
	if rec.Server != "" {
		attrs = append(attrs, attribute.String(AttrHecateMCPServer, rec.Server))
	}
	if rec.Event != "" {
		attrs = append(attrs, attribute.String(AttrHecateMCPCacheEvent, rec.Event))
	}
	m.mcpCacheEventsTotal.Add(ctx, 1, otmetric.WithAttributes(attrs...))
}

// ---------------------------------------------------------------------------

func (m *Metrics) RecordChat(ctx context.Context, record ChatMetricsRecord) {
	if m == nil {
		return
	}

	attrs := make([]attribute.KeyValue, 0, 9)
	if record.Provider != "" {
		attrs = append(attrs, attribute.String(AttrGenAIProviderName, record.Provider))
	}
	if record.ProviderKind != "" {
		attrs = append(attrs, attribute.String(AttrHecateProviderKind, record.ProviderKind))
	}
	if record.RequestedModel != "" {
		attrs = append(attrs, attribute.String(AttrGenAIRequestModel, record.RequestedModel))
	}
	if record.ResponseModel != "" {
		attrs = append(attrs, attribute.String(AttrGenAIResponseModel, record.ResponseModel))
	}

	options := otmetric.WithAttributes(attrs...)
	m.chatRequestsTotal.Add(ctx, 1, options)

	if record.CostMicrosUSD > 0 {
		m.costMicrosTotal.Add(ctx, record.CostMicrosUSD, options)
	}
	if record.PromptTokens > 0 {
		m.promptTokensTotal.Add(ctx, record.PromptTokens, options)
	}
	if record.CompletionTokens > 0 {
		m.completionTokensTotal.Add(ctx, record.CompletionTokens, options)
	}
	if record.TotalTokens > 0 {
		m.totalTokensTotal.Add(ctx, record.TotalTokens, options)
	}
	if record.RetryCount > 0 {
		m.retriesTotal.Add(ctx, int64(record.RetryCount), options)
	}
	if record.FallbackFromProvider != "" {
		m.failoversTotal.Add(ctx, 1, otmetric.WithAttributes(append(attrs,
			attribute.String(AttrHecateFailoverFromProvider, record.FallbackFromProvider),
		)...))
	}
}
