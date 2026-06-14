package api

import (
	"bufio"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/cloudruntime"
	"github.com/hecatehq/hecate/internal/config"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestStatusRecorderForwardsHijacker(t *testing.T) {
	recorder := &hijackableResponseWriter{header: http.Header{}}
	wrapped := &statusRecorder{ResponseWriter: recorder, status: http.StatusOK}

	conn, _, err := wrapped.Hijack()
	if err != nil {
		t.Fatalf("Hijack returned error: %v", err)
	}
	if conn == nil {
		t.Fatal("Hijack returned nil connection")
	}
	_ = conn.Close()
	if !recorder.hijacked {
		t.Fatal("wrapped response writer was not hijacked")
	}
}

type hijackableResponseWriter struct {
	header   http.Header
	hijacked bool
}

func (w *hijackableResponseWriter) Header() http.Header {
	return w.header
}

func (w *hijackableResponseWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

func (w *hijackableResponseWriter) WriteHeader(int) {}

func (w *hijackableResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	client, server := net.Pipe()
	_ = client.Close()
	w.hijacked = true
	return server, bufio.NewReadWriter(bufio.NewReader(server), bufio.NewWriter(server)), nil
}

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

func TestRuntimeTokenMiddleware(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		token  string
		header string
		want   int
	}{
		{
			name:   "disabled allows hecate api",
			method: http.MethodGet,
			path:   "/hecate/v1/whoami",
			want:   http.StatusNoContent,
		},
		{
			name:   "requires token for hecate api",
			method: http.MethodGet,
			path:   "/hecate/v1/whoami",
			token:  "local-runtime-token-123456",
			want:   http.StatusUnauthorized,
		},
		{
			name:   "allows matching token",
			method: http.MethodGet,
			path:   "/hecate/v1/whoami",
			token:  "local-runtime-token-123456",
			header: "local-runtime-token-123456",
			want:   http.StatusNoContent,
		},
		{
			name:   "rejects wrong token",
			method: http.MethodGet,
			path:   "/hecate/v1/whoami",
			token:  "local-runtime-token-123456",
			header: "local-runtime-token-abcdef",
			want:   http.StatusUnauthorized,
		},
		{
			name:   "leaves provider compatible api alone",
			method: http.MethodGet,
			path:   "/v1/models",
			token:  "local-runtime-token-123456",
			want:   http.StatusNoContent,
		},
		{
			name:   "leaves healthz alone",
			method: http.MethodGet,
			path:   "/healthz",
			token:  "local-runtime-token-123456",
			want:   http.StatusNoContent,
		},
		{
			name:   "terminal websocket consumes ticket instead of runtime header",
			method: http.MethodGet,
			path:   "/hecate/v1/terminal?workspace=/tmp&token=ticket",
			token:  "local-runtime-token-123456",
			want:   http.StatusNoContent,
		},
		{
			name:   "terminal ticket creation remains runtime-token protected",
			method: http.MethodPost,
			path:   "/hecate/v1/terminal/sessions",
			token:  "local-runtime-token-123456",
			want:   http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := RuntimeTokenMiddleware(tt.token)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))
			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.header != "" {
				req.Header.Set(runtimeTokenHeader, tt.header)
			}
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}

func TestRuntimeTokenMiddlewareAllowsCloudIdentity(t *testing.T) {
	handler := RuntimeTokenMiddleware("local-runtime-token-123456")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/hecate/v1/whoami", nil)
	req = req.WithContext(cloudruntime.WithIdentity(req.Context(), cloudruntime.Identity{ActorID: "actor_1"}))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestInferenceTokenMiddleware(t *testing.T) {
	tests := []struct {
		name          string
		method        string
		path          string
		token         string
		authorization string
		apiKey        string
		want          int
		wantBody      string
	}{
		{
			name:   "disabled allows provider compatible route",
			method: http.MethodPost,
			path:   "/v1/chat/completions",
			want:   http.StatusNoContent,
		},
		{
			name:     "requires token for chat completions",
			method:   http.MethodPost,
			path:     "/v1/chat/completions",
			token:    "local-inference-token-123456",
			want:     http.StatusUnauthorized,
			wantBody: `"type":"unauthorized"`,
		},
		{
			name:          "allows bearer token",
			method:        http.MethodPost,
			path:          "/v1/chat/completions",
			token:         "local-inference-token-123456",
			authorization: "Bearer local-inference-token-123456",
			want:          http.StatusNoContent,
		},
		{
			name:   "allows x api key token",
			method: http.MethodPost,
			path:   "/v1/messages",
			token:  "local-inference-token-123456",
			apiKey: "local-inference-token-123456",
			want:   http.StatusNoContent,
		},
		{
			name:          "allows one matching header when both are present",
			method:        http.MethodPost,
			path:          "/v1/messages",
			token:         "local-inference-token-123456",
			authorization: "Bearer wrong-token",
			apiKey:        "local-inference-token-123456",
			want:          http.StatusNoContent,
		},
		{
			name:     "messages use anthropic error envelope",
			method:   http.MethodPost,
			path:     "/v1/messages",
			token:    "local-inference-token-123456",
			want:     http.StatusUnauthorized,
			wantBody: `"type":"error"`,
		},
		{
			name:   "leaves healthz alone",
			method: http.MethodGet,
			path:   "/healthz",
			token:  "local-inference-token-123456",
			want:   http.StatusNoContent,
		},
		{
			name:   "leaves hecate native api alone",
			method: http.MethodGet,
			path:   "/hecate/v1/whoami",
			token:  "local-inference-token-123456",
			want:   http.StatusNoContent,
		},
		{
			name:   "leaves otlp traces alone",
			method: http.MethodPost,
			path:   "/v1/traces",
			token:  "local-inference-token-123456",
			want:   http.StatusNoContent,
		},
		{
			name:   "does not gate wrong method on models",
			method: http.MethodPost,
			path:   "/v1/models",
			token:  "local-inference-token-123456",
			want:   http.StatusNoContent,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := InferenceTokenMiddleware(tt.token)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))
			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.authorization != "" {
				req.Header.Set("Authorization", tt.authorization)
			}
			if tt.apiKey != "" {
				req.Header.Set("x-api-key", tt.apiKey)
			}
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d, body=%s", rec.Code, tt.want, rec.Body.String())
			}
			if tt.wantBody != "" && !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Fatalf("body = %s, want substring %s", rec.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestInferenceTokenMiddlewareAllowsCloudIdentity(t *testing.T) {
	handler := InferenceTokenMiddleware("local-inference-token-123456")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req = req.WithContext(cloudruntime.WithIdentity(req.Context(), cloudruntime.Identity{ActorID: "actor_1"}))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
}

func TestCloudRuntimeIdentityMiddleware(t *testing.T) {
	const secret = "cloud-runtime-secret-123456"
	tests := []struct {
		name        string
		path        string
		secret      string
		setIdentity bool
		want        int
	}{
		{name: "disabled allows request", path: "/hecate/v1/whoami", want: http.StatusNoContent},
		{name: "healthz bypasses cloud identity", path: "/healthz", want: http.StatusNoContent},
		{name: "enabled requires secret", path: "/hecate/v1/whoami", want: http.StatusUnauthorized},
		{name: "enabled requires identity", path: "/hecate/v1/whoami", secret: secret, want: http.StatusUnauthorized},
		{name: "enabled accepts complete identity", path: "/hecate/v1/whoami", secret: secret, setIdentity: true, want: http.StatusNoContent},
		{name: "enabled protects static ui too", path: "/", want: http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enabled := tt.name != "disabled allows request"
			handler := CloudRuntimeIdentityMiddleware(enabled, secret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.setIdentity {
					identity, ok := cloudruntime.FromContext(r.Context())
					if !ok {
						t.Fatal("cloud identity missing from context")
					}
					if identity.ActorID != "actor_1" || identity.OrgID != "org_1" || identity.ProjectID != "proj_1" || identity.RuntimeID != "rt_1" {
						t.Fatalf("identity = %+v", identity)
					}
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			if tt.secret != "" {
				req.Header.Set(cloudruntime.HeaderRuntimeSecret, tt.secret)
			}
			if tt.setIdentity {
				req.Header.Set(cloudruntime.HeaderActorID, "actor_1")
				req.Header.Set(cloudruntime.HeaderOrgID, "org_1")
				req.Header.Set(cloudruntime.HeaderProjectID, "proj_1")
				req.Header.Set(cloudruntime.HeaderRuntimeID, "rt_1")
			}
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d, body=%s", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}

func TestCloudRuntimeLocalEndpointGuardMiddleware(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		method  string
		path    string
		want    int
	}{
		{name: "disabled allows local endpoint", method: http.MethodPost, path: "/hecate/v1/system/shutdown", want: http.StatusNoContent},
		{name: "blocks workspace dialog", enabled: true, method: http.MethodPost, path: "/hecate/v1/workspace-dialog", want: http.StatusForbidden},
		{name: "blocks workspace open", enabled: true, method: http.MethodPost, path: "/hecate/v1/workspace-open", want: http.StatusForbidden},
		{name: "blocks terminal session tickets", enabled: true, method: http.MethodPost, path: "/hecate/v1/terminal/sessions", want: http.StatusForbidden},
		{name: "blocks terminal", enabled: true, method: http.MethodGet, path: "/hecate/v1/terminal?workspace=/tmp", want: http.StatusForbidden},
		{name: "blocks reset data", enabled: true, method: http.MethodPost, path: "/hecate/v1/system/reset-data", want: http.StatusForbidden},
		{name: "blocks shutdown", enabled: true, method: http.MethodPost, path: "/hecate/v1/system/shutdown", want: http.StatusForbidden},
		{name: "blocks mcp probe", enabled: true, method: http.MethodPost, path: "/hecate/v1/mcp/probe", want: http.StatusForbidden},
		{name: "blocks mcp registry discovery", enabled: true, method: http.MethodGet, path: "/hecate/v1/mcp/registry/servers", want: http.StatusForbidden},
		{name: "blocks local provider discovery", enabled: true, method: http.MethodGet, path: "/hecate/v1/settings/providers/local-discovery", want: http.StatusForbidden},
		{name: "blocks unclassified hecate endpoint", enabled: true, method: http.MethodPost, path: "/hecate/v1/future-local-only", want: http.StatusForbidden},
		{name: "allows normal endpoint", enabled: true, method: http.MethodGet, path: "/hecate/v1/whoami", want: http.StatusNoContent},
		{name: "allows static ui", enabled: true, method: http.MethodGet, path: "/", want: http.StatusNoContent},
		{name: "allows provider-compatible endpoint", enabled: true, method: http.MethodPost, path: "/v1/chat/completions", want: http.StatusNoContent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := CloudRuntimeLocalEndpointGuardMiddleware(tt.enabled)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d, body=%s", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}

func TestCloudRuntimeEndpointPolicyCoversRegisteredHecateRoutes(t *testing.T) {
	source, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("read server.go: %v", err)
	}
	matches := regexp.MustCompile(`mux\.HandleFunc\("([^"]+)"`).FindAllStringSubmatch(string(source), -1)
	if len(matches) == 0 {
		t.Fatal("no mux.HandleFunc route patterns found")
	}
	var missing []string
	for _, match := range matches {
		pattern := match[1]
		_, path, ok := splitRoutePattern(pattern)
		if !ok || !isHecateAPIPath(path) {
			continue
		}
		if !cloudRuntimeRoutePatternKnown(pattern) {
			missing = append(missing, pattern)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("cloud runtime route policy missing classifications: %s", strings.Join(missing, ", "))
	}
}

func TestNewServerWiresCloudRuntimeIdentity(t *testing.T) {
	const secret = "cloud-runtime-secret-123456"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewServer(logger, NewHandler(config.Config{
		Server: config.ServerConfig{
			CloudRuntimeMode:   true,
			CloudRuntimeSecret: secret,
			RuntimeToken:       "local-runtime-token-123456",
		},
	}, logger, nil, nil, nil, nil))

	req := httptest.NewRequest(http.MethodGet, "/hecate/v1/whoami", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status without cloud identity = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	req = httptest.NewRequest(http.MethodGet, "/hecate/v1/whoami", nil)
	req.Header.Set(cloudruntime.HeaderRuntimeSecret, secret)
	req.Header.Set(cloudruntime.HeaderActorID, "actor_1")
	req.Header.Set(cloudruntime.HeaderOrgID, "org_1")
	req.Header.Set(cloudruntime.HeaderProjectID, "proj_1")
	req.Header.Set(cloudruntime.HeaderRuntimeID, "rt_1")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status with cloud identity = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"cloud_identity"`) || !strings.Contains(rec.Body.String(), `"actor_id":"actor_1"`) {
		t.Fatalf("whoami body = %s, want cloud identity", rec.Body.String())
	}
}

func TestSessionAdvertisesEmbeddedTerminalCapability(t *testing.T) {
	const secret = "cloud-runtime-secret-123456"

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewServer(logger, NewHandler(config.Config{}, logger, nil, nil, nil, nil))

	req := httptest.NewRequest(http.MethodGet, "/hecate/v1/whoami", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("local status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), `"embedded_terminal":true`) {
		t.Fatalf("local whoami body = %s, want embedded terminal capability", rec.Body.String())
	}

	handler = NewServer(logger, NewHandler(config.Config{
		Server: config.ServerConfig{
			CloudRuntimeMode:   true,
			CloudRuntimeSecret: secret,
		},
	}, logger, nil, nil, nil, nil))
	req = httptest.NewRequest(http.MethodGet, "/hecate/v1/whoami", nil)
	req.Header.Set(cloudruntime.HeaderRuntimeSecret, secret)
	req.Header.Set(cloudruntime.HeaderActorID, "actor_1")
	req.Header.Set(cloudruntime.HeaderOrgID, "org_1")
	req.Header.Set(cloudruntime.HeaderProjectID, "proj_1")
	req.Header.Set(cloudruntime.HeaderRuntimeID, "rt_1")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("cloud status = %d, want %d", rec.Code, http.StatusOK)
	}
	if strings.Contains(rec.Body.String(), `"embedded_terminal":true`) {
		t.Fatalf("cloud whoami body = %s, want terminal hidden in cloud runtime", rec.Body.String())
	}
}

func TestNewServerWiresRuntimeTokenMiddleware(t *testing.T) {
	token := "local-runtime-token-123456"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewServer(logger, NewHandler(config.Config{
		Server: config.ServerConfig{RuntimeToken: token},
	}, logger, nil, nil, nil, nil))

	req := httptest.NewRequest(http.MethodGet, "/hecate/v1/whoami", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status without token = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	req = httptest.NewRequest(http.MethodGet, "/hecate/v1/whoami", nil)
	req.Header.Set(runtimeTokenHeader, token)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status with token = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestNewServerWiresInferenceTokenMiddleware(t *testing.T) {
	token := "local-inference-token-123456"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewServer(logger, NewHandler(config.Config{
		Server: config.ServerConfig{InferenceToken: token},
	}, logger, nil, nil, nil, nil))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status without token = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}
