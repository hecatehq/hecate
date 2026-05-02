package telemetry

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

const ServiceName = "hecate-gateway"

type contextKey string

const (
	requestIDContextKey contextKey = "telemetry.request_id"
	traceIDsContextKey  contextKey = "telemetry.trace_ids"
)

type TraceIDs struct {
	TraceID string
	SpanID  string
}

func NewLogger(level string) *slog.Logger {
	options := &slog.HandlerOptions{Level: parseLevel(level)}
	return slog.New(slog.NewJSONHandler(os.Stdout, options)).With(
		slog.String(AttrServiceName, ServiceName),
	)
}

func parseLevel(level string) slog.Level {
	switch strings.ToUpper(level) {
	case "DEBUG":
		return slog.LevelDebug
	case "WARN":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func WithRequestID(ctx context.Context, requestID string) context.Context {
	if strings.TrimSpace(requestID) == "" {
		return ctx
	}
	return context.WithValue(ctx, requestIDContextKey, requestID)
}

func RequestIDFromContext(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDContextKey).(string)
	return requestID
}

func WithTraceIDs(ctx context.Context, traceID, spanID string) context.Context {
	if strings.TrimSpace(traceID) == "" && strings.TrimSpace(spanID) == "" {
		return ctx
	}
	return context.WithValue(ctx, traceIDsContextKey, TraceIDs{
		TraceID: strings.TrimSpace(traceID),
		SpanID:  strings.TrimSpace(spanID),
	})
}

func TraceIDsFromContext(ctx context.Context) TraceIDs {
	if ids, ok := ctx.Value(traceIDsContextKey).(TraceIDs); ok {
		return ids
	}
	spanCtx := trace.SpanContextFromContext(ctx)
	if !spanCtx.IsValid() {
		return TraceIDs{}
	}
	return TraceIDs{
		TraceID: spanCtx.TraceID().String(),
		SpanID:  spanCtx.SpanID().String(),
	}
}

func ContextAttrs(ctx context.Context) []slog.Attr {
	attrs := make([]slog.Attr, 0, 8)
	if requestID := RequestIDFromContext(ctx); requestID != "" {
		attrs = append(attrs, slog.String(AttrRequestID, requestID))
	}
	traceIDs := TraceIDsFromContext(ctx)
	if traceIDs.TraceID != "" {
		attrs = append(attrs, slog.String(AttrTraceID, traceIDs.TraceID))
	}
	if traceIDs.SpanID != "" {
		attrs = append(attrs, slog.String(AttrSpanID, traceIDs.SpanID))
	}
	return attrs
}

func Info(logger *slog.Logger, ctx context.Context, msg string, attrs ...slog.Attr) {
	logger.LogAttrs(ctx, slog.LevelInfo, msg, append(ContextAttrs(ctx), attrs...)...)
}

func Warn(logger *slog.Logger, ctx context.Context, msg string, attrs ...slog.Attr) {
	logger.LogAttrs(ctx, slog.LevelWarn, msg, append(ContextAttrs(ctx), attrs...)...)
}

func Error(logger *slog.Logger, ctx context.Context, msg string, attrs ...slog.Attr) {
	logger.LogAttrs(ctx, slog.LevelError, msg, append(ContextAttrs(ctx), attrs...)...)
}
