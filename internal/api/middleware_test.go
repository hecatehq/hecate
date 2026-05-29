package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func registerW3CPropagator() {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
}

// TestTraceContextMiddlewareExtractsTraceparent verifies that an inbound
// W3C traceparent header is parsed into the request context. Without this,
// distributed traces from upstream services lose their parent link the moment
// they enter the gateway.
func TestTraceContextMiddlewareExtractsTraceparent(t *testing.T) {
	registerW3CPropagator()

	const (
		wantTraceID = "0af7651916cd43dd8448eb211c80319c"
		wantSpanID  = "b7ad6b7169203331"
	)

	var captured oteltrace.SpanContext
	handler := TraceContextMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		captured = oteltrace.SpanContextFromContext(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("traceparent", "00-"+wantTraceID+"-"+wantSpanID+"-01")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !captured.IsValid() {
		t.Fatal("expected valid span context after extraction, got invalid")
	}
	if got := captured.TraceID().String(); got != wantTraceID {
		t.Errorf("trace id = %q, want %q", got, wantTraceID)
	}
	if got := captured.SpanID().String(); got != wantSpanID {
		t.Errorf("span id = %q, want %q", got, wantSpanID)
	}
	if !captured.IsRemote() {
		t.Error("extracted span context should be marked remote")
	}
}

// TestTraceContextMiddlewareNoHeaderPassesThrough verifies that requests
// without trace context don't trigger errors and yield an invalid (zero)
// span context downstream — the signal handlers use to start a fresh trace
// rather than parent off something fabricated.
func TestTraceContextMiddlewareNoHeaderPassesThrough(t *testing.T) {
	registerW3CPropagator()

	var captured oteltrace.SpanContext
	handler := TraceContextMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		captured = oteltrace.SpanContextFromContext(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if captured.IsValid() {
		t.Errorf("expected invalid span context when no traceparent header, got valid: %v", captured)
	}
}

// TestTraceContextMiddlewareExtractsBaggage verifies that W3C baggage entries
// flow into request context. Baggage carries cross-cutting key-value pairs
// like tenant id or experiment flags that downstream spans annotate themselves
// with, and dropping them at the edge would break that contract.
func TestTraceContextMiddlewareExtractsBaggage(t *testing.T) {
	registerW3CPropagator()

	var captured baggage.Baggage
	handler := TraceContextMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		captured = baggage.FromContext(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("baggage", "tenant=acme,env=staging")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := captured.Member("tenant").Value(); got != "acme" {
		t.Errorf("baggage tenant = %q, want %q", got, "acme")
	}
	if got := captured.Member("env").Value(); got != "staging" {
		t.Errorf("baggage env = %q, want %q", got, "staging")
	}
}

func TestOTelHTTPSpanMiddlewareEmitsRequestSpan(t *testing.T) {
	// Stand up an in-memory exporter wired to a fresh TracerProvider,
	// install it globally for this test, then drive an HTTP request
	// through the middleware to assert one `http.server.request` span
	// comes out the other end with the right attributes.
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(exporter)))
	defer tp.Shutdown(t.Context())

	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(previous) })

	httpServerTracer = tp.Tracer("github.com/hecatehq/hecate/internal/api")
	t.Cleanup(func() {
		httpServerTracer = otel.Tracer("github.com/hecatehq/hecate/internal/api")
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := Chain(
		mux,
		RequestIDMiddleware,
		OTelHTTPSpanMiddleware,
	)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	span := spans[0]
	if span.Name != "http.server.request" {
		t.Errorf("span name = %q, want http.server.request", span.Name)
	}
	attrs := map[string]string{}
	intAttrs := map[string]int64{}
	for _, kv := range span.Attributes {
		switch kv.Value.Type() {
		case attribute.STRING:
			attrs[string(kv.Key)] = kv.Value.AsString()
		case attribute.INT64:
			intAttrs[string(kv.Key)] = kv.Value.AsInt64()
		}
	}
	if got := attrs["http.request.method"]; got != "GET" {
		t.Errorf("http.request.method = %q, want GET", got)
	}
	if got := attrs["http.route"]; got != "GET /healthz" {
		t.Errorf("http.route = %q, want %q", got, "GET /healthz")
	}
	if got := intAttrs["http.response.status_code"]; got != 200 {
		t.Errorf("http.response.status_code = %d, want 200", got)
	}
	if got := attrs["hecate.request_id"]; got == "" {
		t.Error("hecate.request_id attribute missing")
	}
}

func TestSameOriginAllowed(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		origin  string
		allowed []string
		want    bool
	}{
		{
			name: "no origin header",
			host: "127.0.0.1:8765",
			want: true,
		},
		{
			name:   "same host",
			host:   "127.0.0.1:8765",
			origin: "http://127.0.0.1:8765",
			want:   true,
		},
		{
			name:   "localhost dev origin rejected by default",
			host:   "127.0.0.1:8765",
			origin: "http://localhost:5173",
			want:   false,
		},
		{
			name:   "loopback ip dev origin rejected by default",
			host:   "127.0.0.1:8765",
			origin: "http://127.0.0.1:5173",
			want:   false,
		},
		{
			name:    "configured dev origin allowed",
			host:    "127.0.0.1:8765",
			origin:  "http://127.0.0.1:5173",
			allowed: []string{"http://127.0.0.1:5173"},
			want:    true,
		},
		{
			name:    "configured origin matches scheme",
			host:    "127.0.0.1:8765",
			origin:  "https://127.0.0.1:5173",
			allowed: []string{"http://127.0.0.1:5173"},
			want:    false,
		},
		{
			name:    "allowed origin accepts trailing slash",
			host:    "127.0.0.1:8765",
			origin:  "http://localhost:5173",
			allowed: []string{"http://localhost:5173/"},
			want:    true,
		},
		{
			name:    "allowed origin with path ignored",
			host:    "127.0.0.1:8765",
			origin:  "http://localhost:5173",
			allowed: []string{"http://localhost:5173/app"},
			want:    false,
		},
		{
			name:   "malformed origin rejected",
			host:   "127.0.0.1:8765",
			origin: "://localhost:5173",
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://"+tt.host+"/v1/chat/completions", nil)
			req.Host = tt.host
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}

			got := sameOriginAllowed(req, normalizeAllowedOrigins(tt.allowed))
			if got != tt.want {
				t.Fatalf("sameOriginAllowed() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSameOriginMiddlewareWithAllowedOriginsRejectsCrossOriginBrowserRequest(t *testing.T) {
	handler := SameOriginMiddlewareWithAllowedOrigins(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8765/v1/chat/completions", nil)
	req.Host = "127.0.0.1:8765"
	req.Header.Set("Origin", "http://localhost:5173")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}
