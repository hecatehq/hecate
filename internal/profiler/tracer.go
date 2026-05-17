package profiler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/hecate/agent-runtime/internal/telemetry"
	"github.com/hecate/agent-runtime/pkg/types"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type Tracer interface {
	Start(requestID string) *Trace
	Get(requestID string) (*Trace, bool)
	List(limit int) []*Trace
	Prune(ctx context.Context, maxAge time.Duration, maxCount int) (int, error)
}

type Trace struct {
	RequestID string
	TraceID   string
	StartedAt time.Time

	mu         sync.Mutex
	events     []types.TraceEvent
	rootSpanID string
	spans      map[string]*types.TraceSpan
	spanOrder  []string
	liveSpans  map[string]*liveSpan
	tracer     oteltrace.Tracer
	rootCtx    context.Context
	finalized  bool
}

type liveSpan struct {
	ctx      context.Context
	span     oteltrace.Span
	snapshot *types.TraceSpan
}

type InMemoryTracer struct {
	mu     sync.Mutex
	traces []*Trace
	tracer oteltrace.Tracer
}

func NewInMemoryTracer(otelTracer oteltrace.Tracer) *InMemoryTracer {
	return &InMemoryTracer{
		traces: make([]*Trace, 0, 16),
		tracer: otelTracer,
	}
}

func (t *InMemoryTracer) Start(requestID string) *Trace {
	trace := NewTrace(requestID, t.tracer)

	t.mu.Lock()
	t.traces = append(t.traces, trace)
	t.mu.Unlock()

	return trace
}

func (t *InMemoryTracer) Get(requestID string) (*Trace, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for i := len(t.traces) - 1; i >= 0; i-- {
		if t.traces[i].RequestID == requestID {
			return t.traces[i], true
		}
	}
	return nil, false
}

func (t *InMemoryTracer) List(limit int) []*Trace {
	t.mu.Lock()
	defer t.mu.Unlock()

	n := len(t.traces)
	if limit > 0 && limit < n {
		n = limit
	}
	out := make([]*Trace, n)
	// Return most-recent first.
	for i := 0; i < n; i++ {
		out[i] = t.traces[len(t.traces)-1-i]
	}
	return out
}

func (t *InMemoryTracer) Prune(_ context.Context, maxAge time.Duration, maxCount int) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	deleted := 0
	kept := t.traces[:0]
	for _, trace := range t.traces {
		if maxAge > 0 && !trace.StartedAt.IsZero() && trace.StartedAt.Before(now.Add(-maxAge)) {
			deleted++
			continue
		}
		kept = append(kept, trace)
	}
	t.traces = kept

	if maxCount > 0 && len(t.traces) > maxCount {
		deleted += len(t.traces) - maxCount
		t.traces = append([]*Trace(nil), t.traces[len(t.traces)-maxCount:]...)
	}
	return deleted, nil
}

func NewTrace(requestID string, otelTracer oteltrace.Tracer) *Trace {
	if otelTracer == nil {
		otelTracer = sdktrace.NewTracerProvider().Tracer("hecate.profiler")
	}

	startedAt := time.Now().UTC()
	rootCtx, rootSpan := otelTracer.Start(
		context.Background(),
		"gateway.request",
		oteltrace.WithTimestamp(startedAt),
		oteltrace.WithSpanKind(oteltrace.SpanKindServer),
	)
	rootSpanID := rootSpan.SpanContext().SpanID().String()
	traceID := rootSpan.SpanContext().TraceID().String()
	rootSnapshot := &types.TraceSpan{
		TraceID:    traceID,
		SpanID:     rootSpanID,
		Name:       "gateway.request",
		Kind:       "server",
		StartTime:  startedAt,
		EndTime:    startedAt,
		Attributes: map[string]any{telemetry.AttrServiceName: "hecate-gateway"},
		Events:     make([]types.TraceEvent, 0, 8),
		StatusCode: "unset",
	}

	trace := &Trace{
		RequestID:  requestID,
		TraceID:    traceID,
		StartedAt:  startedAt,
		events:     make([]types.TraceEvent, 0, 16),
		rootSpanID: rootSpanID,
		spans:      map[string]*types.TraceSpan{rootSpanID: rootSnapshot},
		spanOrder:  []string{rootSpanID},
		liveSpans:  make(map[string]*liveSpan, 8),
		tracer:     otelTracer,
		rootCtx:    rootCtx,
	}
	trace.liveSpans[rootSpanID] = &liveSpan{
		ctx:      rootCtx,
		span:     rootSpan,
		snapshot: rootSnapshot,
	}
	return trace
}

func (t *Trace) Record(name string, attrs map[string]any) {
	t.mu.Lock()
	defer t.mu.Unlock()

	timestamp := time.Now().UTC()
	event := types.TraceEvent{
		Name:       name,
		Timestamp:  timestamp,
		Attributes: cloneAttributes(attrs),
	}
	t.events = append(t.events, event)

	span := t.ensureSpan(spanSpecForEvent(name), timestamp)
	span.snapshot.Events = append(span.snapshot.Events, event)
	if span.snapshot.Attributes == nil {
		span.snapshot.Attributes = map[string]any{}
	}
	normalizedAttrs := otelAttributesForEvent(name, event.Attributes)
	mergeAttributes(span.snapshot.Attributes, normalizedAttrs)
	span.span.SetAttributes(toOTelAttributes(normalizedAttrs)...)
	span.span.AddEvent(name, oteltrace.WithTimestamp(timestamp), oteltrace.WithAttributes(toOTelAttributes(event.Attributes)...))
	if timestamp.After(span.snapshot.EndTime) {
		span.snapshot.EndTime = timestamp
	}
	updateSpanStatus(span.snapshot, span.span, name, event.Attributes)

	root := t.liveSpans[t.rootSpanID]
	root.snapshot.Events = append(root.snapshot.Events, event)
	root.span.AddEvent(name, oteltrace.WithTimestamp(timestamp), oteltrace.WithAttributes(toOTelAttributes(event.Attributes)...))
	if timestamp.After(root.snapshot.EndTime) {
		root.snapshot.EndTime = timestamp
	}
	updateSpanStatus(root.snapshot, root.span, name, event.Attributes)
}

func (t *Trace) Finalize() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.finalized {
		return
	}
	for _, spanID := range t.spanOrder {
		live := t.liveSpans[spanID]
		if live == nil {
			continue
		}
		live.span.End(oteltrace.WithTimestamp(live.snapshot.EndTime))
	}
	t.finalized = true
}

func (t *Trace) Events() []types.TraceEvent {
	t.mu.Lock()
	defer t.mu.Unlock()

	out := make([]types.TraceEvent, len(t.events))
	copy(out, t.events)
	return out
}

func (t *Trace) Spans() []types.TraceSpan {
	t.mu.Lock()
	defer t.mu.Unlock()

	out := make([]types.TraceSpan, 0, len(t.spanOrder))
	for _, spanID := range t.spanOrder {
		span := t.spans[spanID]
		cloned := *span
		cloned.Attributes = cloneAttributes(span.Attributes)
		cloned.Events = append([]types.TraceEvent(nil), span.Events...)
		out = append(out, cloned)
	}
	return out
}

func (t *Trace) RootSpanID() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.rootSpanID
}

func (t *Trace) ensureSpan(spec spanSpec, timestamp time.Time) *liveSpan {
	for _, spanID := range t.spanOrder {
		existing := t.spans[spanID]
		if existing.Name == spec.name {
			if timestamp.Before(existing.StartTime) {
				existing.StartTime = timestamp
			}
			return t.liveSpans[spanID]
		}
	}

	ctx, span := t.tracer.Start(
		t.rootCtx,
		spec.name,
		oteltrace.WithTimestamp(timestamp),
		oteltrace.WithSpanKind(toOTelSpanKind(spec.kind)),
		oteltrace.WithAttributes(toOTelAttributes(spec.attributes)...),
	)
	snapshot := &types.TraceSpan{
		TraceID:      span.SpanContext().TraceID().String(),
		SpanID:       span.SpanContext().SpanID().String(),
		ParentSpanID: t.rootSpanID,
		Name:         spec.name,
		Kind:         spec.kind,
		StartTime:    timestamp,
		EndTime:      timestamp,
		Attributes:   cloneAttributes(spec.attributes),
		Events:       make([]types.TraceEvent, 0, 4),
		StatusCode:   "unset",
	}
	t.spans[snapshot.SpanID] = snapshot
	t.spanOrder = append(t.spanOrder, snapshot.SpanID)
	live := &liveSpan{
		ctx:      ctx,
		span:     span,
		snapshot: snapshot,
	}
	t.liveSpans[snapshot.SpanID] = live
	return live
}

type spanSpec struct {
	name       string
	kind       string
	attributes map[string]any
}

func spanSpecForEvent(name string) spanSpec {
	switch {
	case name == telemetry.EventRequestReceived || name == telemetry.EventRequestInvalid || name == telemetry.EventRequestBodyCaptured:
		return spanSpec{name: telemetry.SpanGatewayRequestParse, kind: "internal"}
	case name == telemetry.EventResponseReturned || name == telemetry.EventResponseBodyCaptured:
		return spanSpec{name: telemetry.SpanGatewayResponse, kind: "internal"}
	case hasPrefix(name, "orchestrator.task."):
		return spanSpec{name: telemetry.SpanOrchestratorTask, kind: "internal"}
	case hasPrefix(name, "orchestrator.run."):
		return spanSpec{name: telemetry.SpanOrchestratorRun, kind: "internal"}
	case hasPrefix(name, "orchestrator.step.") || hasPrefix(name, "tool."):
		return spanSpec{name: telemetry.SpanOrchestratorStep, kind: "internal"}
	case hasPrefix(name, "orchestrator.artifact."):
		return spanSpec{name: telemetry.SpanOrchestratorArtifact, kind: "internal"}
	case hasPrefix(name, "orchestrator.approval.") || hasPrefix(name, "policy."):
		return spanSpec{name: telemetry.SpanOrchestratorApproval, kind: "internal"}
	case hasPrefix(name, "queue."):
		return spanSpec{name: telemetry.SpanOrchestratorQueue, kind: "internal"}
	case hasPrefix(name, "retention."):
		return spanSpec{name: telemetry.SpanRetentionRun, kind: "internal"}
	case hasPrefix(name, "chat."):
		return spanSpec{name: telemetry.SpanAgentChatRun, kind: "internal"}
	case hasPrefix(name, "governor."):
		return spanSpec{name: telemetry.SpanGatewayGovernor, kind: "internal"}
	case hasPrefix(name, "router."):
		return spanSpec{name: telemetry.SpanGatewayRouter, kind: "internal"}
	case hasPrefix(name, "provider.call.") || hasPrefix(name, "provider.retry.") || hasPrefix(name, "provider.failover.") || hasPrefix(name, "provider.health."):
		return spanSpec{name: telemetry.SpanGatewayProvider, kind: "client"}
	case name == telemetry.EventUsageNormalized || name == telemetry.EventUsageRecorded:
		return spanSpec{name: telemetry.SpanGatewayUsage, kind: "internal"}
	default:
		return spanSpec{name: telemetry.SpanGatewayRuntime, kind: "internal"}
	}
}

func otelAttributesForEvent(name string, attrs map[string]any) map[string]any {
	out := make(map[string]any, len(attrs))
	for key, value := range attrs {
		out[key] = value
	}

	if _, ok := out[telemetry.AttrHecatePhase]; ok {
		return out
	}

	switch {
	case name == telemetry.EventRequestReceived || name == telemetry.EventRequestInvalid || name == telemetry.EventRequestBodyCaptured:
		out[telemetry.AttrHecatePhase] = "request"
	case name == telemetry.EventResponseReturned || name == telemetry.EventResponseBodyCaptured:
		out[telemetry.AttrHecatePhase] = "response"
	case hasPrefix(name, "governor."):
		out[telemetry.AttrHecatePhase] = "governor"
	case hasPrefix(name, "router."):
		out[telemetry.AttrHecatePhase] = "routing"
	case hasPrefix(name, "provider."):
		out[telemetry.AttrHecatePhase] = "provider"
	case name == telemetry.EventUsageNormalized || name == telemetry.EventUsageRecorded:
		out[telemetry.AttrHecatePhase] = "usage"
	case hasPrefix(name, "orchestrator.task.") || hasPrefix(name, "orchestrator.run."):
		out[telemetry.AttrHecatePhase] = "orchestration"
	case hasPrefix(name, "queue."):
		out[telemetry.AttrHecatePhase] = "queue"
	case hasPrefix(name, "orchestrator.step."):
		out[telemetry.AttrHecatePhase] = "tool"
	case hasPrefix(name, "orchestrator.artifact."):
		out[telemetry.AttrHecatePhase] = "artifact"
	case hasPrefix(name, "orchestrator.approval."):
		out[telemetry.AttrHecatePhase] = "approval"
	case hasPrefix(name, "tool."):
		out[telemetry.AttrHecatePhase] = "tool"
	case hasPrefix(name, "policy."):
		out[telemetry.AttrHecatePhase] = "approval"
	case hasPrefix(name, "retention."):
		out[telemetry.AttrHecatePhase] = "retention"
	case hasPrefix(name, "chat."):
		out[telemetry.AttrHecatePhase] = "chat"
	}

	return out
}

func updateSpanStatus(snapshot *types.TraceSpan, span oteltrace.Span, eventName string, attrs map[string]any) {
	if errText, ok := attrs[telemetry.AttrErrorMessage].(string); ok && errText != "" {
		snapshot.StatusCode = "error"
		snapshot.StatusMessage = errText
		span.SetStatus(codes.Error, errText)
		return
	}
	if snapshot.StatusCode == "error" {
		return
	}
	if eventName == "response.returned" || hasSuffix(eventName, ".finished") || eventName == "governor.allowed" || eventName == "governor.route_allowed" {
		snapshot.StatusCode = "ok"
		span.SetStatus(codes.Ok, "")
	}
}

func cloneAttributes(attrs map[string]any) map[string]any {
	if len(attrs) == 0 {
		return nil
	}
	out := make(map[string]any, len(attrs))
	for k, v := range attrs {
		out[k] = v
	}
	return out
}

func mergeAttributes(dst map[string]any, src map[string]any) {
	for k, v := range src {
		dst[k] = v
	}
}

func hasPrefix(value, prefix string) bool {
	return len(value) >= len(prefix) && value[:len(prefix)] == prefix
}

func hasSuffix(value, suffix string) bool {
	return len(value) >= len(suffix) && value[len(value)-len(suffix):] == suffix
}

func toOTelAttributes(attrs map[string]any) []attribute.KeyValue {
	if len(attrs) == 0 {
		return nil
	}
	out := make([]attribute.KeyValue, 0, len(attrs))
	for key, value := range attrs {
		k := attribute.Key(key)
		switch v := value.(type) {
		case string:
			out = append(out, k.String(v))
		case bool:
			out = append(out, k.Bool(v))
		case int:
			out = append(out, k.Int(v))
		case int64:
			out = append(out, k.Int64(v))
		case float64:
			out = append(out, k.Float64(v))
		default:
			out = append(out, k.String(fmt.Sprintf("%v", v)))
		}
	}
	return out
}

func toOTelSpanKind(kind string) oteltrace.SpanKind {
	switch kind {
	case "server":
		return oteltrace.SpanKindServer
	case "client":
		return oteltrace.SpanKindClient
	default:
		return oteltrace.SpanKindInternal
	}
}
