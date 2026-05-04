package telemetry

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
)

type OTelMetricOptions struct {
	Enabled   bool
	Endpoint  string
	Headers   map[string]string
	Resource  *resource.Resource
	Timeout   time.Duration
	Interval  time.Duration
	Transport string
}

func NewMeterProvider(ctx context.Context, opts OTelMetricOptions) (*sdkmetric.MeterProvider, func(context.Context) error, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Second
	}
	if opts.Interval <= 0 {
		opts.Interval = 30 * time.Second
	}

	providerOpts := []sdkmetric.Option{}
	if opts.Resource != nil {
		providerOpts = append(providerOpts, sdkmetric.WithResource(opts.Resource))
	}

	if opts.Enabled {
		exporter, err := newMetricExporter(ctx, opts)
		if err != nil {
			return nil, nil, fmt.Errorf("create otlp metric exporter: %w", err)
		}
		providerOpts = append(providerOpts, sdkmetric.WithReader(
			sdkmetric.NewPeriodicReader(
				exporter,
				sdkmetric.WithInterval(opts.Interval),
				sdkmetric.WithTimeout(opts.Timeout),
			),
		))
	}

	provider := sdkmetric.NewMeterProvider(providerOpts...)
	shutdown := func(ctx context.Context) error {
		return provider.Shutdown(ctx)
	}
	return provider, shutdown, nil
}

func newMetricExporter(ctx context.Context, opts OTelMetricOptions) (sdkmetric.Exporter, error) {
	if NormalizeOTLPTransport(opts.Transport) == OTLPTransportGRPC {
		exporterOpts := []otlpmetricgrpc.Option{
			otlpmetricgrpc.WithEndpoint(OTLPGRPCEndpoint(opts.Endpoint)),
			otlpmetricgrpc.WithHeaders(opts.Headers),
			otlpmetricgrpc.WithTimeout(opts.Timeout),
		}
		if IsOTLPGRPCInsecure(opts.Endpoint) {
			exporterOpts = append(exporterOpts, otlpmetricgrpc.WithInsecure())
		}
		return otlpmetricgrpc.New(ctx, exporterOpts...)
	}

	exporterOpts := []otlpmetrichttp.Option{
		otlpmetrichttp.WithHeaders(opts.Headers),
		otlpmetrichttp.WithTimeout(opts.Timeout),
	}
	if endpoint := strings.TrimSpace(opts.Endpoint); endpoint != "" {
		exporterOpts = append(exporterOpts, otlpmetrichttp.WithEndpointURL(endpoint))
	}
	if strings.HasPrefix(strings.TrimSpace(opts.Endpoint), "http://") {
		exporterOpts = append(exporterOpts, otlpmetrichttp.WithInsecure())
	}
	return otlpmetrichttp.New(ctx, exporterOpts...)
}
