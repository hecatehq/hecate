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

type ProviderCallMetricsRecord struct {
	Provider     string
	ProviderKind string
	Model        string
	Result       string
	Attempt      int
	HealthStatus string
	DurationMS   int64
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
	providerCallsTotal    otmetric.Int64Counter
	providerCallDuration  otmetric.Int64Histogram
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

	meter := provider.Meter("github.com/hecatehq/hecate/internal/telemetry")

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

	providerCallsTotal, err := meter.Int64Counter(
		MetricProviderCallsTotal,
		otmetric.WithDescription("Total upstream provider call attempts grouped by result."),
		otmetric.WithUnit("{call}"),
	)
	if err != nil {
		return nil, err
	}

	providerCallDuration, err := meter.Int64Histogram(
		MetricProviderCallDuration,
		otmetric.WithDescription("Upstream provider call latency."),
		otmetric.WithUnit("ms"),
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
		providerCallsTotal:    providerCallsTotal,
		providerCallDuration:  providerCallDuration,
	}, nil
}

func (m *Metrics) RecordRequestOutcome(ctx context.Context, result string, duration time.Duration) {
	if m == nil || result == "" {
		return
	}

	attrs := otmetric.WithAttributes(attribute.String(AttrHecateResult, NormalizeResult(result)))
	m.requestsTotal.Add(ctx, 1, attrs)
	m.requestDuration.Record(ctx, duration.Milliseconds(), attrs)
}

func (m *Metrics) RecordProviderCall(ctx context.Context, rec ProviderCallMetricsRecord) {
	if m == nil {
		return
	}
	provider := NormalizeMetricLabel(rec.Provider)
	if provider == "" {
		return
	}

	attrs := make([]attribute.KeyValue, 0, 7)
	attrs = append(attrs, attribute.String(AttrGenAIProviderName, provider))
	if rec.ProviderKind != "" {
		attrs = append(attrs, attribute.String(AttrHecateProviderKind, NormalizeProviderKind(rec.ProviderKind)))
	}
	if rec.Model != "" {
		attrs = append(attrs, attribute.String(AttrGenAIRequestModel, NormalizeMetricLabel(rec.Model)))
	}
	if rec.Result != "" {
		attrs = append(attrs, attribute.String(AttrHecateResult, NormalizeResult(rec.Result)))
	}
	if rec.Attempt > 0 {
		attrs = append(attrs, attribute.Int(AttrHecateRetryAttempt, rec.Attempt))
	}
	if rec.HealthStatus != "" {
		attrs = append(attrs, attribute.String(AttrHecateProviderHealthStatus, NormalizeProviderHealthStatus(rec.HealthStatus)))
	}
	opt := otmetric.WithAttributes(attrs...)
	m.providerCallsTotal.Add(ctx, 1, opt)
	if rec.DurationMS > 0 {
		m.providerCallDuration.Record(ctx, rec.DurationMS, opt)
	}
}

// ---------------------------------------------------------------------------
// AgentChatMetrics
// ---------------------------------------------------------------------------

type AgentChatRunMetricsRecord struct {
	AdapterID  string
	DriverKind string
	Status     string
	Result     string
	DurationMS int64
	Timing     AgentChatRunTimingRecord
}

type AgentChatRunTimingRecord struct {
	QueueMS        int64
	ModelMS        int64
	ToolMS         int64
	ApprovalWaitMS int64
	OverheadMS     int64
}

type AgentChatMetrics struct {
	runsTotal      otmetric.Int64Counter
	runDuration    otmetric.Int64Histogram
	runTiming      otmetric.Int64Histogram
	cancelledTotal otmetric.Int64Counter
}

// AgentChatCancelledRecord labels a single agent-chat cancellation
// event. Reason takes one of operator|request_cancelled|shutdown
// (see contract.go); unknown values collapse to "other" via the
// label normalizer. Fired once per cancellation, not once per
// retry / cleanup attempt.
type AgentChatCancelledRecord struct {
	AdapterID string
	Reason    string
}

func NewAgentChatMetrics() *AgentChatMetrics {
	m, err := NewAgentChatMetricsWithMeterProvider(otel.GetMeterProvider())
	if err != nil {
		return &AgentChatMetrics{}
	}
	return m
}

func NewAgentChatMetricsWithMeterProvider(provider otmetric.MeterProvider) (*AgentChatMetrics, error) {
	if provider == nil {
		provider = otel.GetMeterProvider()
	}
	meter := provider.Meter("github.com/hecatehq/hecate/internal/telemetry")

	runsTotal, err := meter.Int64Counter(
		MetricAgentChatRunsTotal,
		otmetric.WithDescription("Total agent chat runs grouped by adapter/runtime, driver, status, and result."),
		otmetric.WithUnit("{run}"),
	)
	if err != nil {
		return nil, err
	}

	runDuration, err := meter.Int64Histogram(
		MetricAgentChatRunDuration,
		otmetric.WithDescription("Agent chat run wall-clock duration."),
		otmetric.WithUnit("ms"),
	)
	if err != nil {
		return nil, err
	}

	runTiming, err := meter.Int64Histogram(
		MetricAgentChatRunTiming,
		otmetric.WithDescription("Agent chat run timing broken down by queue, model, tool, approval, and overhead buckets."),
		otmetric.WithUnit("ms"),
	)
	if err != nil {
		return nil, err
	}

	cancelledTotal, err := meter.Int64Counter(
		MetricAgentChatCancelledTotal,
		otmetric.WithDescription("Total agent chat run/turn endings that terminated via cancellation, labeled by reason (operator | request_cancelled | shutdown)."),
		otmetric.WithUnit("{cancellation}"),
	)
	if err != nil {
		return nil, err
	}

	return &AgentChatMetrics{
		runsTotal:      runsTotal,
		runDuration:    runDuration,
		runTiming:      runTiming,
		cancelledTotal: cancelledTotal,
	}, nil
}

func (m *AgentChatMetrics) RecordRun(ctx context.Context, rec AgentChatRunMetricsRecord) {
	if m == nil {
		return
	}
	attrs := make([]attribute.KeyValue, 0, 4)
	if rec.AdapterID != "" {
		attrs = append(attrs, attribute.String(AttrHecateAgentAdapterID, NormalizeMetricLabel(rec.AdapterID)))
	}
	if rec.DriverKind != "" {
		attrs = append(attrs, attribute.String(AttrHecateAgentDriverKind, NormalizeAgentDriverKind(rec.DriverKind)))
	}
	if rec.Status != "" {
		attrs = append(attrs, attribute.String(AttrHecateRunStatus, NormalizeRunStatus(rec.Status)))
	}
	if rec.Result != "" {
		attrs = append(attrs, attribute.String(AttrHecateResult, NormalizeResult(rec.Result)))
	}
	opt := otmetric.WithAttributes(attrs...)
	m.runsTotal.Add(ctx, 1, opt)
	if rec.DurationMS > 0 {
		m.runDuration.Record(ctx, rec.DurationMS, opt)
	}
	for _, bucket := range rec.Timing.buckets() {
		if bucket.ms <= 0 {
			continue
		}
		bucketAttrs := append([]attribute.KeyValue{}, attrs...)
		bucketAttrs = append(bucketAttrs, attribute.String(AttrHecateChatTimingBucket, bucket.name))
		m.runTiming.Record(ctx, bucket.ms, otmetric.WithAttributes(bucketAttrs...))
	}
}

func (t AgentChatRunTimingRecord) buckets() []struct {
	name string
	ms   int64
} {
	return []struct {
		name string
		ms   int64
	}{
		{name: "queue", ms: t.QueueMS},
		{name: "model", ms: t.ModelMS},
		{name: "tools", ms: t.ToolMS},
		{name: "approval", ms: t.ApprovalWaitMS},
		{name: "overhead", ms: t.OverheadMS},
	}
}

// RecordChatCancelled fires once per cancellation event. The
// adapter-id label is best-effort — if the cancellation lands
// before an adapter has been resolved it can be empty. The reason
// label is closed-set; unknown values collapse to "other" via the
// label normalizer.
func (m *AgentChatMetrics) RecordChatCancelled(ctx context.Context, rec AgentChatCancelledRecord) {
	if m == nil || m.cancelledTotal == nil {
		return
	}
	attrs := make([]attribute.KeyValue, 0, 2)
	if rec.AdapterID != "" {
		attrs = append(attrs, attribute.String(AttrHecateAgentAdapterID, NormalizeMetricLabel(rec.AdapterID)))
	}
	if rec.Reason != "" {
		attrs = append(attrs, attribute.String(AttrHecateChatCancelReason, NormalizeAgentChatCancelReason(rec.Reason)))
	}
	m.cancelledTotal.Add(ctx, 1, otmetric.WithAttributes(attrs...))
}

// ---------------------------------------------------------------------------
// AgentAdapterMetrics — adapter-runtime concerns sibling to
// AgentAdapterApprovalMetrics. probe is fired once per Probe call;
// terminal_rpc_unsupported is fired every time an adapter calls one
// of the five ACP terminal methods we don't implement.
// ---------------------------------------------------------------------------

// AgentAdapterProbeRecord labels the outcome of a single Probe.
// Status takes one of agentadapters.ProbeStatus*; unknown values
// collapse to "other".
type AgentAdapterProbeRecord struct {
	AdapterID string
	Status    string
}

type AgentAdapterMetrics struct {
	probeTotal                  otmetric.Int64Counter
	terminalRPCUnsupportedTotal otmetric.Int64Counter
}

func NewAgentAdapterMetrics() *AgentAdapterMetrics {
	m, err := NewAgentAdapterMetricsWithMeterProvider(otel.GetMeterProvider())
	if err != nil {
		return &AgentAdapterMetrics{}
	}
	return m
}

func NewAgentAdapterMetricsWithMeterProvider(provider otmetric.MeterProvider) (*AgentAdapterMetrics, error) {
	if provider == nil {
		provider = otel.GetMeterProvider()
	}
	meter := provider.Meter("github.com/hecatehq/hecate/internal/telemetry")

	probeTotal, err := meter.Int64Counter(
		MetricAgentAdapterProbeTotal,
		otmetric.WithDescription("Total agentadapters.Probe calls grouped by adapter and final status (ready | not_installed | auth_required | error)."),
		otmetric.WithUnit("{probe}"),
	)
	if err != nil {
		return nil, err
	}

	terminalRPCUnsupportedTotal, err := meter.Int64Counter(
		MetricAgentAdapterTerminalRPCUnsupportedTotal,
		otmetric.WithDescription("Total ACP terminal RPC calls received from external agent adapters that Hecate does not implement, grouped by adapter and method (create | kill | output | release | wait)."),
		otmetric.WithUnit("{call}"),
	)
	if err != nil {
		return nil, err
	}

	return &AgentAdapterMetrics{
		probeTotal:                  probeTotal,
		terminalRPCUnsupportedTotal: terminalRPCUnsupportedTotal,
	}, nil
}

// RecordProbe is the per-Probe-call hook. Fires once per Probe
// regardless of which stage the probe terminated in; the status
// label carries the operator-facing classification.
func (m *AgentAdapterMetrics) RecordProbe(ctx context.Context, rec AgentAdapterProbeRecord) {
	if m == nil || m.probeTotal == nil {
		return
	}
	attrs := make([]attribute.KeyValue, 0, 2)
	if rec.AdapterID != "" {
		attrs = append(attrs, attribute.String(AttrHecateAgentAdapterID, NormalizeMetricLabel(rec.AdapterID)))
	}
	if rec.Status != "" {
		attrs = append(attrs, attribute.String(AttrHecateAgentProbeStatus, NormalizeAgentAdapterProbeStatus(rec.Status)))
	}
	m.probeTotal.Add(ctx, 1, otmetric.WithAttributes(attrs...))
}

// RecordTerminalRPCUnsupported fires whenever an adapter calls one
// of the five ACP terminal methods Hecate does not implement. The
// matching error returned to the adapter is
// agentadapters.ErrTerminalRPCUnsupported (wrapping a JSON-RPC
// method-not-found RequestError); this counter exists so operators
// can dashboard "is this adapter being silently degraded?" without
// scanning logs for the typed error.
func (m *AgentAdapterMetrics) RecordTerminalRPCUnsupported(ctx context.Context, adapterID, method string) {
	if m == nil || m.terminalRPCUnsupportedTotal == nil {
		return
	}
	attrs := make([]attribute.KeyValue, 0, 2)
	if adapterID != "" {
		attrs = append(attrs, attribute.String(AttrHecateAgentAdapterID, NormalizeMetricLabel(adapterID)))
	}
	if method != "" {
		attrs = append(attrs, attribute.String(AttrHecateAgentTerminalMethod, NormalizeAgentAdapterTerminalMethod(method)))
	}
	m.terminalRPCUnsupportedTotal.Add(ctx, 1, otmetric.WithAttributes(attrs...))
}

// ---------------------------------------------------------------------------
// AgentAdapterApprovalMetrics — see docs/rfcs/external-adapter-approvals-v1.md.
// ---------------------------------------------------------------------------

// AgentAdapterApprovalRequestRecord labels an incoming RequestPermission.
type AgentAdapterApprovalRequestRecord struct {
	AdapterID string
	ToolKind  string
	Mode      string // configured HECATE_AGENT_ADAPTER_APPROVAL_MODE
}

// AgentAdapterApprovalResolveRecord labels a resolved approval.
type AgentAdapterApprovalResolveRecord struct {
	AdapterID  string
	ToolKind   string
	Mode       string
	Decision   string // approve | deny | "" (cancelled / timed_out)
	Scope      string // once | session | workspace_tool | adapter_tool
	Path       string // operator | grant | default_mode | timeout
	Status     string // approved | denied | timed_out | cancelled
	DurationMS int64
}

type AgentAdapterApprovalMetrics struct {
	requestedTotal otmetric.Int64Counter
	resolvedTotal  otmetric.Int64Counter
	durationMS     otmetric.Int64Histogram
	// timedOutTotal is the prompt-mode-timeout dedicated counter.
	// Operators alert on this when a chat session has nobody
	// reviewing approvals — RecordResolved already covers the
	// terminal transition, but a separate counter is materially
	// easier to dashboard.
	timedOutTotal otmetric.Int64Counter
	// grantsActive tracks the live durable-grant count. UpDownCounter
	// because we want both directions (create / delete) and the
	// running total. Seeded at process start via SeedGrantsActive so
	// a restart doesn't reset the line to zero.
	grantsActive otmetric.Int64UpDownCounter
}

func NewAgentAdapterApprovalMetrics() *AgentAdapterApprovalMetrics {
	m, err := NewAgentAdapterApprovalMetricsWithMeterProvider(otel.GetMeterProvider())
	if err != nil {
		return &AgentAdapterApprovalMetrics{}
	}
	return m
}

func NewAgentAdapterApprovalMetricsWithMeterProvider(provider otmetric.MeterProvider) (*AgentAdapterApprovalMetrics, error) {
	if provider == nil {
		provider = otel.GetMeterProvider()
	}
	meter := provider.Meter("github.com/hecatehq/hecate/internal/telemetry")

	requestedTotal, err := meter.Int64Counter(
		MetricAgentAdapterApprovalRequestedTotal,
		otmetric.WithDescription("Total ACP RequestPermission calls received from external agent adapters."),
		otmetric.WithUnit("{request}"),
	)
	if err != nil {
		return nil, err
	}

	resolvedTotal, err := meter.Int64Counter(
		MetricAgentAdapterApprovalResolvedTotal,
		otmetric.WithDescription("Total approvals resolved, labeled by decision, scope, and resolution path."),
		otmetric.WithUnit("{approval}"),
	)
	if err != nil {
		return nil, err
	}

	durationMS, err := meter.Int64Histogram(
		MetricAgentAdapterApprovalDurationMS,
		otmetric.WithDescription("Time from RequestPermission to resolution."),
		otmetric.WithUnit("ms"),
	)
	if err != nil {
		return nil, err
	}

	timedOutTotal, err := meter.Int64Counter(
		MetricAgentAdapterApprovalTimedOutTotal,
		otmetric.WithDescription("Total approvals that hit the prompt-mode timeout (path=timeout)."),
		otmetric.WithUnit("{approval}"),
	)
	if err != nil {
		return nil, err
	}

	grantsActive, err := meter.Int64UpDownCounter(
		MetricAgentAdapterApprovalGrantsActive,
		otmetric.WithDescription("Live count of durable approval grants. Incremented on grant create, decremented on grant delete; seeded from store contents at process start."),
		otmetric.WithUnit("{grant}"),
	)
	if err != nil {
		return nil, err
	}

	return &AgentAdapterApprovalMetrics{
		requestedTotal: requestedTotal,
		resolvedTotal:  resolvedTotal,
		durationMS:     durationMS,
		timedOutTotal:  timedOutTotal,
		grantsActive:   grantsActive,
	}, nil
}

func (m *AgentAdapterApprovalMetrics) RecordRequested(ctx context.Context, rec AgentAdapterApprovalRequestRecord) {
	if m == nil || m.requestedTotal == nil {
		return
	}
	attrs := make([]attribute.KeyValue, 0, 3)
	if rec.AdapterID != "" {
		attrs = append(attrs, attribute.String(AttrHecateAgentAdapterID, NormalizeMetricLabel(rec.AdapterID)))
	}
	if rec.ToolKind != "" {
		attrs = append(attrs, attribute.String(AttrHecateAgentApprovalToolKind, NormalizeMetricLabel(rec.ToolKind)))
	}
	if rec.Mode != "" {
		attrs = append(attrs, attribute.String(AttrHecateAgentApprovalMode, NormalizeMetricLabel(rec.Mode)))
	}
	m.requestedTotal.Add(ctx, 1, otmetric.WithAttributes(attrs...))
}

func (m *AgentAdapterApprovalMetrics) RecordResolved(ctx context.Context, rec AgentAdapterApprovalResolveRecord) {
	if m == nil || m.resolvedTotal == nil {
		return
	}
	attrs := make([]attribute.KeyValue, 0, 7)
	if rec.AdapterID != "" {
		attrs = append(attrs, attribute.String(AttrHecateAgentAdapterID, NormalizeMetricLabel(rec.AdapterID)))
	}
	if rec.ToolKind != "" {
		attrs = append(attrs, attribute.String(AttrHecateAgentApprovalToolKind, NormalizeMetricLabel(rec.ToolKind)))
	}
	if rec.Mode != "" {
		attrs = append(attrs, attribute.String(AttrHecateAgentApprovalMode, NormalizeMetricLabel(rec.Mode)))
	}
	if rec.Decision != "" {
		attrs = append(attrs, attribute.String(AttrHecateAgentApprovalDecision, NormalizeMetricLabel(rec.Decision)))
	}
	if rec.Scope != "" {
		attrs = append(attrs, attribute.String(AttrHecateAgentApprovalScope, NormalizeMetricLabel(rec.Scope)))
	}
	if rec.Path != "" {
		attrs = append(attrs, attribute.String(AttrHecateAgentApprovalPath, NormalizeMetricLabel(rec.Path)))
	}
	if rec.Status != "" {
		attrs = append(attrs, attribute.String(AttrHecateAgentApprovalStatus, NormalizeMetricLabel(rec.Status)))
	}
	opt := otmetric.WithAttributes(attrs...)
	m.resolvedTotal.Add(ctx, 1, opt)
	if rec.DurationMS >= 0 {
		m.durationMS.Record(ctx, rec.DurationMS, opt)
	}
}

// RecordTimedOut increments the dedicated timeout counter. Called
// alongside RecordResolved when the prompt-mode timeout fires; the
// labels mirror RecordResolved so a dashboard can split timed_out by
// adapter / tool_kind without joining against the resolved counter.
func (m *AgentAdapterApprovalMetrics) RecordTimedOut(ctx context.Context, rec AgentAdapterApprovalResolveRecord) {
	if m == nil || m.timedOutTotal == nil {
		return
	}
	attrs := make([]attribute.KeyValue, 0, 3)
	if rec.AdapterID != "" {
		attrs = append(attrs, attribute.String(AttrHecateAgentAdapterID, NormalizeMetricLabel(rec.AdapterID)))
	}
	if rec.ToolKind != "" {
		attrs = append(attrs, attribute.String(AttrHecateAgentApprovalToolKind, NormalizeMetricLabel(rec.ToolKind)))
	}
	if rec.Mode != "" {
		attrs = append(attrs, attribute.String(AttrHecateAgentApprovalMode, NormalizeMetricLabel(rec.Mode)))
	}
	m.timedOutTotal.Add(ctx, 1, otmetric.WithAttributes(attrs...))
}

// RecordGrantCreated increments the active-grants UpDownCounter.
// Coordinator hooks fire this after a successful CreateGrant.
func (m *AgentAdapterApprovalMetrics) RecordGrantCreated(ctx context.Context) {
	if m == nil || m.grantsActive == nil {
		return
	}
	m.grantsActive.Add(ctx, 1)
}

// RecordGrantDeleted decrements the active-grants UpDownCounter.
// Coordinator hooks fire this after a successful DeleteGrant.
func (m *AgentAdapterApprovalMetrics) RecordGrantDeleted(ctx context.Context) {
	if m == nil || m.grantsActive == nil {
		return
	}
	m.grantsActive.Add(ctx, -1)
}

// SeedGrantsActive is the startup-reconcile hook for the active-grants
// counter. The handler calls this once after wiring the store with the
// count of currently-live grants so a SQLite restart doesn't reset
// the dashboard line to zero. Safe to call repeatedly — each call
// adds delta to the counter. Pass a negative value to subtract.
func (m *AgentAdapterApprovalMetrics) SeedGrantsActive(ctx context.Context, delta int64) {
	if m == nil || m.grantsActive == nil || delta == 0 {
		return
	}
	m.grantsActive.Add(ctx, delta)
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
	Decision     string // approved | rejected | cancelled
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
	meter := provider.Meter("github.com/hecatehq/hecate/internal/telemetry")

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
		attrs = append(attrs, attribute.String(AttrHecateRunStatus, NormalizeRunStatus(rec.Status)))
	}
	if rec.ExecutionKind != "" {
		attrs = append(attrs, attribute.String(AttrHecateExecutionKind, NormalizeExecutionKind(rec.ExecutionKind)))
	}
	if rec.Model != "" {
		attrs = append(attrs, attribute.String(AttrGenAIRequestModel, NormalizeMetricLabel(rec.Model)))
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
		attrs = append(attrs, attribute.String(AttrHecateStepKind, NormalizeStepKind(rec.StepKind)))
	}
	if rec.Result != "" {
		attrs = append(attrs, attribute.String(AttrHecateResult, NormalizeResult(rec.Result)))
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
		attrs = append(attrs, attribute.String(AttrHecateApprovalKind, NormalizeApprovalKind(rec.ApprovalKind)))
	}
	if rec.Decision != "" {
		attrs = append(attrs, attribute.String(AttrHecateApprovalDecision, NormalizeApprovalDecision(rec.Decision)))
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
		attrs = append(attrs, attribute.String(AttrHecateQueueBackend, NormalizeQueueBackend(rec.QueueBackend)))
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
		attrs = append(attrs, attribute.String(AttrHecateMCPServer, NormalizeMetricLabel(rec.Server)))
	}
	if rec.Tool != "" {
		attrs = append(attrs, attribute.String(AttrHecateMCPTool, NormalizeMetricLabel(rec.Tool)))
	}
	if rec.Result != "" {
		attrs = append(attrs, attribute.String(AttrHecateMCPCallResult, NormalizeMCPCallResult(rec.Result)))
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
		attrs = append(attrs, attribute.String(AttrHecateMCPServer, NormalizeMetricLabel(rec.Server)))
	}
	if rec.Event != "" {
		attrs = append(attrs, attribute.String(AttrHecateMCPCacheEvent, NormalizeMCPCacheEvent(rec.Event)))
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
		attrs = append(attrs, attribute.String(AttrGenAIProviderName, NormalizeMetricLabel(record.Provider)))
	}
	if record.ProviderKind != "" {
		attrs = append(attrs, attribute.String(AttrHecateProviderKind, NormalizeProviderKind(record.ProviderKind)))
	}
	if record.RequestedModel != "" {
		attrs = append(attrs, attribute.String(AttrGenAIRequestModel, NormalizeMetricLabel(record.RequestedModel)))
	}
	if record.ResponseModel != "" {
		attrs = append(attrs, attribute.String(AttrGenAIResponseModel, NormalizeMetricLabel(record.ResponseModel)))
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
			attribute.String(AttrHecateFailoverFromProvider, NormalizeMetricLabel(record.FallbackFromProvider)),
		)...))
	}
}
