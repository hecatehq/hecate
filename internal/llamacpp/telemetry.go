package llamacpp

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/hecatehq/hecate/internal/telemetry"
)

// llamacppTracer is the per-package OTel tracer. Backs the install /
// runtime / proxy span events documented in
// docs/rfcs/local-models-llamacpp.md and registered in
// internal/telemetry/contract.go.
//
// We use a package-level tracer (the same pattern internal/agentadapters
// uses for the approval coordinator) rather than threading a tracer
// through every constructor — the local-models subsystem is a
// single-tenant background runtime, so a global is the simplest shape
// without compromising testability (the OTel SDK's NoopTracerProvider
// turns every Start into a no-op).
var llamacppTracer = otel.Tracer("github.com/hecatehq/hecate/internal/llamacpp")

// installSpanName is the span the install events attach to. One span
// per install_id — Start at POST /install, End on terminal event
// (completed / failed / cancelled).
const installSpanName = "local_model.install"

// runtimeSpanName covers a single Start → Stop / crash cycle. The
// state machine emits events on each transition; the span itself
// closes when the active session ends.
const runtimeSpanName = "local_model.runtime"

// proxySpanName covers one inbound proxy request. The proxy event
// fires on the way out so traces can correlate the spawn with the
// reverse-proxy hop.
const proxySpanName = "local_model.proxy"

// startInstallSpan begins the install span and stamps the
// canonical attributes. The caller must End the returned span via the
// closure, typically in defer; calling End with a non-empty error sets
// the span error status and adds an "install.failed" event.
func startInstallSpan(ctx context.Context, modelID, installID, sourceURL string, bytesTotal int64) (context.Context, trace.Span) {
	return llamacppTracer.Start(ctx, installSpanName,
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String(telemetry.AttrHecateLocalModelEngine, "llamacpp"),
			attribute.String(telemetry.AttrHecateLocalModelID, modelID),
			attribute.String(telemetry.AttrHecateLocalModelInstallID, installID),
			attribute.String(telemetry.AttrHecateLocalModelInstallSourceURL, sourceURL),
			attribute.Int64(telemetry.AttrHecateLocalModelInstallBytesTotal, bytesTotal),
		),
	)
}

// recordInstallEvent maps a ProgressEvent onto the corresponding span
// event. Called by the install run loop after every emit so the span
// timeline mirrors the SSE stream. Progress events are already
// rate-limited by the installer's byte/time-step sampler, so we don't
// re-throttle here.
func recordInstallEvent(span trace.Span, ev ProgressEvent) {
	if span == nil || !span.IsRecording() {
		return
	}
	switch ev.Kind {
	case ProgressStarted:
		span.AddEvent(telemetry.EventLocalModelInstallStarted,
			trace.WithAttributes(installEventAttrs(ev)...))
	case ProgressProgress:
		span.AddEvent(telemetry.EventLocalModelInstallProgress,
			trace.WithAttributes(installEventAttrs(ev)...))
	case ProgressCompleted:
		span.AddEvent(telemetry.EventLocalModelInstallCompleted,
			trace.WithAttributes(installEventAttrs(ev)...))
	case ProgressFailed:
		span.AddEvent(telemetry.EventLocalModelInstallFailed,
			trace.WithAttributes(installEventAttrs(ev)...))
	case ProgressCancelled:
		span.AddEvent(telemetry.EventLocalModelInstallCancelled,
			trace.WithAttributes(installEventAttrs(ev)...))
	}
}

func installEventAttrs(ev ProgressEvent) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(telemetry.AttrHecateLocalModelID, ev.ModelID),
	}
	if ev.BytesDownloaded > 0 {
		attrs = append(attrs, attribute.Int64(telemetry.AttrHecateLocalModelInstallBytesDone, ev.BytesDownloaded))
	}
	if ev.BytesTotal > 0 {
		attrs = append(attrs, attribute.Int64(telemetry.AttrHecateLocalModelInstallBytesTotal, ev.BytesTotal))
	}
	if ev.ErrorKind != "" {
		attrs = append(attrs, attribute.String(telemetry.AttrHecateLocalModelInstallErrorKind, ev.ErrorKind))
	}
	if ev.ExpectedSHA256 != "" {
		attrs = append(attrs, attribute.String(telemetry.AttrHecateLocalModelInstallExpectedSHA256, ev.ExpectedSHA256))
	}
	if ev.ActualSHA256 != "" {
		attrs = append(attrs, attribute.String(telemetry.AttrHecateLocalModelInstallActualSHA256, ev.ActualSHA256))
	}
	if ev.Message != "" {
		attrs = append(attrs, attribute.String("hecate.local_model.install.message", ev.Message))
	}
	return attrs
}

// startRuntimeSpan begins the runtime lifecycle span. End is called on
// runtime stop (operator or crash) so the span covers one Start →
// stop / crash cycle.
func startRuntimeSpan(ctx context.Context, modelID string, port, contextSize int) (context.Context, trace.Span) {
	return llamacppTracer.Start(ctx, runtimeSpanName,
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String(telemetry.AttrHecateLocalModelEngine, "llamacpp"),
			attribute.String(telemetry.AttrHecateLocalModelID, modelID),
			attribute.Int(telemetry.AttrHecateLocalModelRuntimePort, port),
			attribute.Int(telemetry.AttrHecateLocalModelRuntimeContextSize, contextSize),
		),
	)
}

// recordRuntimeStarted fires once the child reports healthy. Carries
// the time-to-first-healthy so dashboards can spot a regression in
// cold-load times across model sizes.
func recordRuntimeStarted(span trace.Span, modelID string, pid int, ttfhMS int64) {
	if span == nil || !span.IsRecording() {
		return
	}
	span.AddEvent(telemetry.EventLocalModelRuntimeStarted, trace.WithAttributes(
		attribute.String(telemetry.AttrHecateLocalModelID, modelID),
		attribute.Int(telemetry.AttrHecateLocalModelRuntimePID, pid),
		attribute.Int64(telemetry.AttrHecateLocalModelRuntimeTTFHMS, ttfhMS),
	))
}

// recordRuntimeStopped fires on operator stop or switch. reason is one
// of "operator", "switch", or "shutdown".
func recordRuntimeStopped(span trace.Span, modelID, reason string, uptimeMS int64) {
	if span == nil || !span.IsRecording() {
		return
	}
	span.AddEvent(telemetry.EventLocalModelRuntimeStopped, trace.WithAttributes(
		attribute.String(telemetry.AttrHecateLocalModelID, modelID),
		attribute.String(telemetry.AttrHecateLocalModelRuntimeReason, reason),
		attribute.Int64(telemetry.AttrHecateLocalModelRuntimeUptimeMS, uptimeMS),
	))
}

// recordRuntimeCrashed fires on an unexpected child exit. The exit
// code (or -1 for signal) is the only field the operator can act on.
func recordRuntimeCrashed(span trace.Span, modelID string, exitCode int, signal string) {
	if span == nil || !span.IsRecording() {
		return
	}
	span.AddEvent(telemetry.EventLocalModelRuntimeCrashed, trace.WithAttributes(
		attribute.String(telemetry.AttrHecateLocalModelID, modelID),
		attribute.Int(telemetry.AttrHecateLocalModelRuntimeExitCode, exitCode),
		attribute.String("hecate.local_model.runtime.signal", signal),
	))
}

// recordProxyRouted fires once per inbound proxy request, on the way
// out. Per-request — emit count tracks chat-completion throughput
// against the local runtime. Kept on the existing request span if
// one is in scope; otherwise a short-lived span scoped to the
// proxied request.
func recordProxyRouted(ctx context.Context, modelID string) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		// Fall back to a short span so the event still lands somewhere.
		_, span = llamacppTracer.Start(ctx, proxySpanName,
			trace.WithSpanKind(trace.SpanKindInternal))
		defer span.End()
	}
	span.AddEvent(telemetry.EventLocalModelProxyRouted, trace.WithAttributes(
		attribute.String(telemetry.AttrHecateLocalModelEngine, "llamacpp"),
		attribute.String(telemetry.AttrHecateLocalModelID, modelID),
	))
}
