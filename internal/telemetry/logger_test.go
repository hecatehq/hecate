package telemetry

import (
	"context"
	"testing"

	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestContextAttrsIncludeRequestAndTrace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ctx = WithRequestID(ctx, "req-123")
	ctx = WithTraceIDs(ctx, "trace-123", "span-123")

	attrs := ContextAttrs(ctx)
	got := make(map[string]string, len(attrs))
	for _, attr := range attrs {
		if attr.Value.Kind() == 0 {
			continue
		}
		got[attr.Key] = attr.Value.String()
	}

	want := map[string]string{
		AttrRequestID: "req-123",
		AttrTraceID:   "trace-123",
		AttrSpanID:    "span-123",
	}

	for key, value := range want {
		if got[key] != value {
			t.Fatalf("ContextAttrs()[%q] = %q, want %q", key, got[key], value)
		}
	}
}

func TestContextAttrsFallsBackToOTelSpanContext(t *testing.T) {
	t.Parallel()

	traceID := oteltrace.TraceID{0x01}
	spanID := oteltrace.SpanID{0x02}
	spanCtx := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID: traceID,
		SpanID:  spanID,
	})
	ctx := oteltrace.ContextWithSpanContext(context.Background(), spanCtx)

	attrs := ContextAttrs(ctx)
	got := make(map[string]string, len(attrs))
	for _, attr := range attrs {
		if attr.Value.Kind() == 0 {
			continue
		}
		got[attr.Key] = attr.Value.String()
	}

	if got[AttrTraceID] != traceID.String() {
		t.Fatalf("ContextAttrs()[%q] = %q, want %q", AttrTraceID, got[AttrTraceID], traceID.String())
	}
	if got[AttrSpanID] != spanID.String() {
		t.Fatalf("ContextAttrs()[%q] = %q, want %q", AttrSpanID, got[AttrSpanID], spanID.String())
	}
}
