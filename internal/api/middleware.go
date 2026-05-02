package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	"github.com/hecate/agent-runtime/internal/telemetry"
)

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
//   - The Origin's hostname resolves to a loopback address (dev: a Vite
//     dev server on http://localhost:5173 proxies to http://127.0.0.1:8765,
//     so Host and Origin disagree but both ends sit on loopback).
func SameOriginMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !sameOriginAllowed(r) {
			WriteError(w, http.StatusForbidden, "forbidden", "cross-origin browser request rejected")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func sameOriginAllowed(r *http.Request) bool {
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
	hostname := u.Hostname()
	if hostname == "" {
		return false
	}
	if hostname == "localhost" {
		return true
	}
	if ip := net.ParseIP(hostname); ip != nil && ip.IsLoopback() {
		return true
	}
	return false
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
					WriteError(w, http.StatusInternalServerError, "internal_error", "unexpected server error")
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
