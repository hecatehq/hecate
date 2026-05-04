package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otlploggrpc "go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	otlplog "go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type OTelLogOptions struct {
	Enabled   bool
	Endpoint  string
	Headers   map[string]string
	Resource  *resource.Resource
	Timeout   time.Duration
	Transport string
}

func NewLoggerWithOTLP(ctx context.Context, level string, opts OTelLogOptions) (*slog.Logger, func(context.Context) error, error) {
	jsonHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: parseLevel(level)})

	serviceName := serviceNameFromResource(opts.Resource)

	if !opts.Enabled {
		return slog.New(jsonHandler).With(slog.String(AttrServiceName, serviceName)), func(context.Context) error { return nil }, nil
	}

	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Second
	}

	exporter, err := newLogExporter(ctx, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("create otlp log exporter: %w", err)
	}

	providerOpts := []sdklog.LoggerProviderOption{
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)),
	}
	if opts.Resource != nil {
		providerOpts = append(providerOpts, sdklog.WithResource(opts.Resource))
	}
	provider := sdklog.NewLoggerProvider(providerOpts...)

	otelHandler := newOTLPHandler(provider.Logger("hecate.telemetry"), parseLevel(level))
	handler := newMultiHandler(jsonHandler, otelHandler)
	shutdown := func(ctx context.Context) error {
		return provider.Shutdown(ctx)
	}

	return slog.New(handler).With(slog.String(AttrServiceName, serviceName)), shutdown, nil
}

func newLogExporter(ctx context.Context, opts OTelLogOptions) (sdklog.Exporter, error) {
	if NormalizeOTLPTransport(opts.Transport) == OTLPTransportGRPC {
		exporterOpts := []otlploggrpc.Option{
			otlploggrpc.WithHeaders(opts.Headers),
			otlploggrpc.WithTimeout(opts.Timeout),
		}
		if endpoint := strings.TrimSpace(opts.Endpoint); endpoint != "" {
			exporterOpts = append(exporterOpts, otlploggrpc.WithEndpoint(OTLPGRPCEndpoint(endpoint)))
			if IsOTLPGRPCInsecure(endpoint) {
				exporterOpts = append(exporterOpts, otlploggrpc.WithInsecure())
			}
		}
		return otlploggrpc.New(ctx, exporterOpts...)
	}

	exporterOpts := []otlplog.Option{
		otlplog.WithHeaders(opts.Headers),
		otlplog.WithTimeout(opts.Timeout),
	}
	if endpoint := strings.TrimSpace(opts.Endpoint); endpoint != "" {
		exporterOpts = append(exporterOpts, otlplog.WithEndpointURL(endpoint))
	}
	if strings.HasPrefix(strings.TrimSpace(opts.Endpoint), "http://") {
		exporterOpts = append(exporterOpts, otlplog.WithInsecure())
	}
	return otlplog.New(ctx, exporterOpts...)
}

// serviceNameFromResource extracts the service.name attribute from the supplied
// Resource so the slog handler tags every record with it. Falls back to the
// package default when the resource is missing or has no service.name set.
func serviceNameFromResource(res *resource.Resource) string {
	if res == nil {
		return ServiceName
	}
	for _, kv := range res.Attributes() {
		if string(kv.Key) == AttrServiceName {
			if v := kv.Value.AsString(); v != "" {
				return v
			}
		}
	}
	return ServiceName
}

type multiHandler struct {
	handlers []slog.Handler
}

func newMultiHandler(handlers ...slog.Handler) slog.Handler {
	out := make([]slog.Handler, 0, len(handlers))
	for _, handler := range handlers {
		if handler != nil {
			out = append(out, handler)
		}
	}
	return &multiHandler{handlers: out}
}

func (h *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (h *multiHandler) Handle(ctx context.Context, record slog.Record) error {
	var firstErr error
	for _, handler := range h.handlers {
		if !handler.Enabled(ctx, record.Level) {
			continue
		}
		if err := handler.Handle(ctx, record.Clone()); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (h *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handlers := make([]slog.Handler, 0, len(h.handlers))
	for _, handler := range h.handlers {
		handlers = append(handlers, handler.WithAttrs(attrs))
	}
	return &multiHandler{handlers: handlers}
}

func (h *multiHandler) WithGroup(name string) slog.Handler {
	handlers := make([]slog.Handler, 0, len(h.handlers))
	for _, handler := range h.handlers {
		handlers = append(handlers, handler.WithGroup(name))
	}
	return &multiHandler{handlers: handlers}
}

type otlpHandler struct {
	logger   otellog.Logger
	minLevel slog.Level
	attrs    []slog.Attr
	groups   []string
}

func newOTLPHandler(logger otellog.Logger, minLevel slog.Level) slog.Handler {
	return &otlpHandler{
		logger:   logger,
		minLevel: minLevel,
		attrs:    make([]slog.Attr, 0, 8),
		groups:   make([]string, 0, 4),
	}
}

func (h *otlpHandler) Enabled(ctx context.Context, level slog.Level) bool {
	if level < h.minLevel {
		return false
	}
	return h.logger.Enabled(ctx, otellog.EnabledParameters{
		Severity:  severityFromLevel(level),
		EventName: "",
	})
}

func (h *otlpHandler) Handle(ctx context.Context, record slog.Record) error {
	ctx = withSpanContext(ctx)

	var otelRecord otellog.Record
	otelRecord.SetTimestamp(record.Time)
	otelRecord.SetObservedTimestamp(time.Now().UTC())
	otelRecord.SetSeverity(severityFromLevel(record.Level))
	otelRecord.SetSeverityText(strings.ToUpper(record.Level.String()))
	otelRecord.SetEventName(record.Message)
	otelRecord.SetBody(otellog.StringValue(record.Message))

	attrs := make([]otellog.KeyValue, 0, len(h.attrs)+8)
	for _, attr := range h.attrs {
		appendAttr(&attrs, h.groups, attr)
	}
	record.Attrs(func(attr slog.Attr) bool {
		appendAttr(&attrs, h.groups, attr)
		return true
	})
	otelRecord.AddAttributes(attrs...)
	h.logger.Emit(ctx, otelRecord)
	return nil
}

func (h *otlpHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	merged := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	merged = append(merged, h.attrs...)
	merged = append(merged, attrs...)
	return &otlpHandler{
		logger:   h.logger,
		minLevel: h.minLevel,
		attrs:    merged,
		groups:   append([]string(nil), h.groups...),
	}
}

func (h *otlpHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	groups := append([]string(nil), h.groups...)
	groups = append(groups, name)
	return &otlpHandler{
		logger:   h.logger,
		minLevel: h.minLevel,
		attrs:    append([]slog.Attr(nil), h.attrs...),
		groups:   groups,
	}
}

func appendAttr(out *[]otellog.KeyValue, groups []string, attr slog.Attr) {
	attr.Value = attr.Value.Resolve()
	if attr.Equal(slog.Attr{}) {
		return
	}
	if attr.Value.Kind() == slog.KindGroup {
		nextGroups := append([]string(nil), groups...)
		if attr.Key != "" {
			nextGroups = append(nextGroups, attr.Key)
		}
		for _, nested := range attr.Value.Group() {
			appendAttr(out, nextGroups, nested)
		}
		return
	}
	if attr.Key == "" {
		return
	}
	key := attr.Key
	if len(groups) > 0 {
		key = strings.Join(append(append([]string(nil), groups...), attr.Key), ".")
	}
	*out = append(*out, otellog.KeyValue{
		Key:   key,
		Value: otelValue(attr.Value),
	})
}

func otelValue(value slog.Value) otellog.Value {
	switch value.Kind() {
	case slog.KindString:
		return otellog.StringValue(value.String())
	case slog.KindBool:
		return otellog.BoolValue(value.Bool())
	case slog.KindInt64:
		return otellog.Int64Value(value.Int64())
	case slog.KindUint64:
		return otellog.Int64Value(int64(value.Uint64()))
	case slog.KindFloat64:
		return otellog.Float64Value(value.Float64())
	case slog.KindDuration:
		return otellog.StringValue(value.Duration().String())
	case slog.KindTime:
		return otellog.StringValue(value.Time().UTC().Format(time.RFC3339Nano))
	case slog.KindGroup:
		kvs := make([]otellog.KeyValue, 0, len(value.Group()))
		for _, attr := range value.Group() {
			attr.Value = attr.Value.Resolve()
			if attr.Key == "" {
				continue
			}
			kvs = append(kvs, otellog.KeyValue{Key: attr.Key, Value: otelValue(attr.Value)})
		}
		return otellog.MapValue(kvs...)
	case slog.KindAny:
		return anyToOTelValue(value.Any())
	default:
		return otellog.StringValue(value.String())
	}
}

func anyToOTelValue(value any) otellog.Value {
	switch v := value.(type) {
	case nil:
		return otellog.StringValue("")
	case string:
		return otellog.StringValue(v)
	case bool:
		return otellog.BoolValue(v)
	case int:
		return otellog.IntValue(v)
	case int64:
		return otellog.Int64Value(v)
	case uint:
		return otellog.Int64Value(int64(v))
	case uint64:
		return otellog.Int64Value(int64(v))
	case float64:
		return otellog.Float64Value(v)
	case float32:
		return otellog.Float64Value(float64(v))
	case time.Time:
		return otellog.StringValue(v.UTC().Format(time.RFC3339Nano))
	case time.Duration:
		return otellog.StringValue(v.String())
	case error:
		return otellog.StringValue(v.Error())
	case []string:
		values := make([]otellog.Value, 0, len(v))
		for _, item := range v {
			values = append(values, otellog.StringValue(item))
		}
		return otellog.SliceValue(values...)
	case []any:
		values := make([]otellog.Value, 0, len(v))
		for _, item := range v {
			values = append(values, anyToOTelValue(item))
		}
		return otellog.SliceValue(values...)
	case map[string]string:
		kvs := make([]otellog.KeyValue, 0, len(v))
		for key, item := range v {
			kvs = append(kvs, otellog.String(key, item))
		}
		return otellog.MapValue(kvs...)
	case map[string]any:
		kvs := make([]otellog.KeyValue, 0, len(v))
		for key, item := range v {
			kvs = append(kvs, otellog.KeyValue{Key: key, Value: anyToOTelValue(item)})
		}
		return otellog.MapValue(kvs...)
	case attribute.KeyValue:
		return otellog.ValueFromAttribute(v.Value)
	default:
		return otellog.StringValue(fmt.Sprintf("%v", v))
	}
}

func severityFromLevel(level slog.Level) otellog.Severity {
	switch {
	case level <= slog.LevelDebug:
		return otellog.SeverityDebug
	case level < slog.LevelWarn:
		return otellog.SeverityInfo
	case level < slog.LevelError:
		return otellog.SeverityWarn
	default:
		return otellog.SeverityError
	}
}

func withSpanContext(ctx context.Context) context.Context {
	spanCtx := oteltrace.SpanContextFromContext(ctx)
	if spanCtx.IsValid() {
		return ctx
	}

	ids := TraceIDsFromContext(ctx)
	if ids.TraceID == "" || ids.SpanID == "" {
		return ctx
	}

	traceID, err := oteltrace.TraceIDFromHex(ids.TraceID)
	if err != nil {
		return ctx
	}
	spanID, err := oteltrace.SpanIDFromHex(ids.SpanID)
	if err != nil {
		return ctx
	}

	spanCtx = oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: oteltrace.FlagsSampled,
		Remote:     true,
	})
	return oteltrace.ContextWithSpanContext(ctx, spanCtx)
}
