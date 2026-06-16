package api

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"

	"github.com/hecatehq/hecate/internal/remoteruntime"
	"github.com/hecatehq/hecate/internal/telemetry"
)

var httpServerTracer = otel.Tracer("github.com/hecatehq/hecate/internal/api")

const runtimeTokenHeader = "X-Hecate-Runtime-Token"

type middleware func(http.Handler) http.Handler

func Chain(handler http.Handler, middleware ...middleware) http.Handler {
	wrapped := handler
	for i := len(middleware) - 1; i >= 0; i-- {
		wrapped = middleware[i](wrapped)
	}
	return wrapped
}

// TraceContextMiddleware extracts W3C trace context (traceparent, tracestate)
// and baggage from inbound request headers using the globally configured
// TextMapPropagator. Without this, upstream traces lose their parent link the
// moment a request enters the gateway, so it MUST run before any handler that
// starts a span.
func TraceContextMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-Id")
		if requestID == "" {
			requestID = newRequestID()
		}

		ctx := telemetry.WithRequestID(r.Context(), requestID)
		w.Header().Set("X-Request-Id", requestID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// OTelHTTPSpanMiddleware opens one `http.server.request` span per
// inbound request. Without it, the OTLP exporter pipeline sees the
// per-subsystem spans Hecate handlers create (router decision,
// provider call, governor check, etc.) but never the top-level
// request envelope they should hang off — operator dashboards that
// filter on `http.server.request` would never light up.
//
// Runs after TraceContextMiddleware (so an inbound traceparent is
// the parent of this span) and after RequestIDMiddleware (so the
// span carries the operator-visible hecate.request_id attribute).
//
// Span attributes follow OTel HTTP semconv:
//
//	http.request.method, http.route, http.response.status_code
//
// plus `hecate.request_id` for the operator UI / log correlation.
// 5xx responses set span.Status to Error so OTel-aware backends can
// surface them without re-deriving the threshold.
func OTelHTTPSpanMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, span := httpServerTracer.Start(r.Context(), "http.server.request")
		defer span.End()

		span.SetAttributes(semconv.HTTPRequestMethodKey.String(r.Method))
		if requestID := telemetry.RequestIDFromContext(ctx); requestID != "" {
			span.SetAttributes(attribute.String("hecate.request_id", requestID))
		}
		if identity, ok := remoteruntime.FromContext(ctx); ok {
			span.SetAttributes(
				attribute.String("hecate.cloud.actor_id", identity.ActorID),
				attribute.String("hecate.cloud.org_id", identity.OrgID),
				attribute.String("hecate.cloud.project_id", identity.ProjectID),
				attribute.String("hecate.cloud.runtime_id", identity.RuntimeID),
			)
		}

		rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		r = r.WithContext(ctx)
		next.ServeHTTP(rw, r)

		// r.Pattern is populated by http.ServeMux during dispatch
		// (Go 1.22+), so we can only read it AFTER next.ServeHTTP.
		// Fall back to URL.Path for routes that didn't go through a
		// pattern-aware mux (e.g. notfound handler).
		route := r.Pattern
		if route == "" {
			route = r.URL.Path
		}
		span.SetAttributes(
			semconv.HTTPRouteKey.String(route),
			semconv.HTTPResponseStatusCodeKey.Int(rw.status),
		)
		if rw.status >= 500 {
			span.SetStatus(codes.Error, "HTTP "+strconv.Itoa(rw.status))
		}
	})
}

func LoggingMiddleware(logger *slog.Logger) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rw, r)

			telemetry.Info(logger, r.Context(), "http.server.request",
				slog.String("event.name", "http.server.request"),
				slog.String(telemetry.AttrTraceID, rw.Header().Get("X-Trace-Id")),
				slog.String(telemetry.AttrSpanID, rw.Header().Get("X-Span-Id")),
				slog.String("http.request.method", r.Method),
				slog.String("url.path", r.URL.Path),
				slog.Int("http.response.status_code", rw.status),
				slog.Int64(telemetry.AttrHecateHTTPDurationMS, time.Since(start).Milliseconds()),
			)
		})
	}
}

// SameOriginMiddleware rejects browser-cross-origin requests with 403.
// The gateway runs without auth, so the only thing standing between a
// malicious page open in your browser and `fetch('http://127.0.0.1:8765/v1/...')`
// is the Origin header check. Requests without an Origin header (curl,
// SDKs, server-to-server) pass through — only browsers send Origin.
//
// Accepts when:
//   - The Origin host matches the request Host exactly (production: the
//     embedded UI is served by the gateway, so same-origin trivially).
//   - The full Origin is explicitly configured via HECATE_ALLOWED_ORIGINS
//     (dev: Vite on http://127.0.0.1:5173 proxies to the gateway, so Host and
//     Origin disagree even though both are local).
func SameOriginMiddleware(next http.Handler) http.Handler {
	return SameOriginMiddlewareWithAllowedOrigins(nil)(next)
}

func SameOriginMiddlewareWithAllowedOrigins(allowedOrigins []string) middleware {
	allowed := normalizeAllowedOrigins(allowedOrigins)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !sameOriginAllowed(r, allowed) {
				WriteError(w, http.StatusForbidden, errCodeForbidden, "cross-origin browser request rejected")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func RuntimeTokenMiddleware(token string) middleware {
	token = strings.TrimSpace(token)
	return func(next http.Handler) http.Handler {
		if token == "" {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Browsers cannot attach custom headers during a WebSocket upgrade.
			// The protected POST /terminal/sessions route mints a short-lived,
			// one-use ticket; the GET upgrade only consumes that ticket.
			if isTerminalWebSocketTicketRequest(r) {
				next.ServeHTTP(w, r)
				return
			}
			if _, ok := remoteruntime.FromContext(r.Context()); ok || !isHecateAPIPath(r.URL.Path) || runtimeTokenMatches(r.Header.Get(runtimeTokenHeader), token) {
				next.ServeHTTP(w, r)
				return
			}
			WriteError(w, http.StatusUnauthorized, errCodeUnauthorized, "runtime token is required")
		})
	}
}

func isTerminalWebSocketTicketRequest(r *http.Request) bool {
	return r.Method == http.MethodGet && r.URL.Path == "/hecate/v1/terminal" && strings.TrimSpace(r.URL.Query().Get("token")) != ""
}

func RemoteRuntimeIdentityMiddleware(enabled bool, secret string) middleware {
	secret = strings.TrimSpace(secret)
	return func(next http.Handler) http.Handler {
		if !enabled {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" {
				next.ServeHTTP(w, r)
				return
			}
			// Browsers cannot attach the cloud-runtime identity headers during
			// a WebSocket upgrade. The protected POST /terminal/sessions route
			// mints a one-use ticket; the GET upgrade only consumes it.
			if isTerminalWebSocketTicketRequest(r) {
				next.ServeHTTP(w, r)
				return
			}
			if !tokenMatches(r.Header.Get(remoteruntime.HeaderRuntimeSecret), secret) {
				WriteError(w, http.StatusUnauthorized, errCodeUnauthorized, "remote runtime identity is required")
				return
			}
			identity, err := remoteruntime.FromHeaders(r.Header)
			if err != nil {
				WriteError(w, http.StatusUnauthorized, errCodeUnauthorized, "remote runtime identity headers are required")
				return
			}
			next.ServeHTTP(w, r.WithContext(remoteruntime.WithIdentity(r.Context(), identity)))
		})
	}
}

func RemoteRuntimeLocalEndpointGuardMiddleware(enabled bool) middleware {
	return func(next http.Handler) http.Handler {
		if !enabled {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if reason := remoteRuntimeEndpointBlockReason(r.Method, r.URL.Path); reason != "" {
				WriteError(w, http.StatusForbidden, errCodeForbidden, reason)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func InferenceTokenMiddleware(token string) middleware {
	token = strings.TrimSpace(token)
	return func(next http.Handler) http.Handler {
		if token == "" {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := remoteruntime.FromContext(r.Context()); ok || !isProviderInferenceRoute(r.Method, r.URL.Path) || inferenceTokenMatches(r, token) {
				next.ServeHTTP(w, r)
				return
			}
			writeInferenceTokenError(w, r)
		})
	}
}

func isHecateAPIPath(path string) bool {
	return path == "/hecate/v1" || strings.HasPrefix(path, "/hecate/v1/")
}

func runtimeTokenMatches(got, want string) bool {
	return tokenMatches(got, want)
}

func tokenMatches(got, want string) bool {
	got = strings.TrimSpace(got)
	if got == "" || len(got) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func inferenceTokenMatches(r *http.Request, want string) bool {
	if tokenMatches(bearerToken(r.Header.Get("Authorization")), want) {
		return true
	}
	return tokenMatches(r.Header.Get("x-api-key"), want)
}

func bearerToken(value string) string {
	value = strings.TrimSpace(value)
	if len(value) < len("Bearer ") || !strings.EqualFold(value[:len("Bearer")], "Bearer") {
		return ""
	}
	if value[len("Bearer")] != ' ' && value[len("Bearer")] != '\t' {
		return ""
	}
	return strings.TrimSpace(value[len("Bearer"):])
}

func isProviderInferenceRoute(method, path string) bool {
	switch method + " " + path {
	case http.MethodGet + " /v1/models",
		http.MethodPost + " /v1/chat/completions",
		http.MethodPost + " /v1/messages":
		return true
	default:
		return false
	}
}

func writeInferenceTokenError(w http.ResponseWriter, r *http.Request) {
	classified := gatewayHTTPError{
		Status:        http.StatusUnauthorized,
		OpenAIType:    errCodeUnauthorized,
		AnthropicType: "authentication_error",
		Message:       "inference token is required",
	}
	if r.URL.Path == "/v1/messages" {
		writeAnthropicGatewayError(w, classified, ErrorDetails{})
		return
	}
	writeOpenAIGatewayError(w, classified, ErrorDetails{})
}

func sameOriginAllowed(r *http.Request, allowedOrigins map[string]struct{}) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if u.Host == r.Host {
		return true
	}
	key, ok := originKey(origin)
	if !ok {
		return false
	}
	_, ok = allowedOrigins[key]
	return ok
}

func normalizeAllowedOrigins(origins []string) map[string]struct{} {
	allowed := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		key, ok := originKey(origin)
		if ok {
			allowed[key] = struct{}{}
		}
	}
	return allowed
}

func originKey(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", false
	}
	if u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	if u.Path != "" && u.Path != "/" {
		return "", false
	}
	return scheme + "://" + strings.ToLower(u.Host), true
}

func RecoveryMiddleware(logger *slog.Logger) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if recovered := recover(); recovered != nil {
					telemetry.Error(logger, r.Context(), "http.server.panic",
						slog.String("event.name", "http.server.panic"),
						slog.String("exception.message", stringifyPanic(recovered)),
					)
					WriteError(w, http.StatusInternalServerError, errCodeInternalError, "unexpected server error")
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}

func RequestIDFromContext(ctx context.Context) string {
	return telemetry.RequestIDFromContext(ctx)
}

func newRequestID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return time.Now().UTC().Format("20060102150405.000000000")
	}
	return hex.EncodeToString(buf)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusRecorder) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return h.Hijack()
}

func stringifyPanic(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case error:
		return v.Error()
	default:
		return "panic"
	}
}
