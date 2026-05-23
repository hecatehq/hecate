package profiler

import (
	"context"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/telemetry"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// TracerProviderOptions carries the inputs to NewTracerProvider. Resource and
// Sampler are required for first-class OpenTelemetry behavior — Resource is
// the shared service identity reused across signals, Sampler controls trace
// volume, and an explicit nil-or-default fallback keeps tests trivial.
type TracerProviderOptions struct {
	Enabled   bool
	Endpoint  string
	Headers   map[string]string
	Timeout   time.Duration
	Transport string
	Resource  *resource.Resource
	Sampler   sdktrace.Sampler
}

func NewTracerProvider(ctx context.Context, opts TracerProviderOptions) (*sdktrace.TracerProvider, error) {
	if opts.Sampler == nil {
		opts.Sampler = sdktrace.ParentBased(sdktrace.AlwaysSample())
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Second
	}
	tpOpts := []sdktrace.TracerProviderOption{
		sdktrace.WithSampler(opts.Sampler),
	}
	if opts.Resource != nil {
		tpOpts = append(tpOpts, sdktrace.WithResource(opts.Resource))
	}

	if opts.Enabled && strings.TrimSpace(opts.Endpoint) != "" {
		exporter, err := newTraceExporter(ctx, opts)
		if err != nil {
			return nil, err
		}
		tpOpts = append(tpOpts, sdktrace.WithBatcher(exporter))
	}

	return sdktrace.NewTracerProvider(tpOpts...), nil
}

func newTraceExporter(ctx context.Context, opts TracerProviderOptions) (sdktrace.SpanExporter, error) {
	if telemetry.NormalizeOTLPTransport(opts.Transport) == telemetry.OTLPTransportGRPC {
		exporterOpts := []otlptracegrpc.Option{
			otlptracegrpc.WithEndpoint(telemetry.OTLPGRPCEndpoint(opts.Endpoint)),
			otlptracegrpc.WithHeaders(opts.Headers),
			otlptracegrpc.WithTimeout(opts.Timeout),
		}
		if telemetry.IsOTLPGRPCInsecure(opts.Endpoint) {
			exporterOpts = append(exporterOpts, otlptracegrpc.WithInsecure())
		}
		return otlptracegrpc.New(ctx, exporterOpts...)
	}

	return otlptracehttp.New(
		ctx,
		otlptracehttp.WithEndpointURL(opts.Endpoint),
		otlptracehttp.WithHeaders(opts.Headers),
		otlptracehttp.WithTimeout(opts.Timeout),
	)
}

func NewOTelTracer(provider oteltrace.TracerProvider) oteltrace.Tracer {
	if provider == nil {
		provider = sdktrace.NewTracerProvider()
	}
	return provider.Tracer("hecate.profiler")
}
