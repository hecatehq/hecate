package telemetry

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/exemplar"
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
	// ExemplarFilter overrides the SDK's default exemplar filter when set.
	// Accepted values are trace_based, always_on, and always_off.
	ExemplarFilter string
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
	if filter, ok, err := otelMetricExemplarFilter(opts.ExemplarFilter); err != nil {
		return nil, nil, err
	} else if ok {
		providerOpts = append(providerOpts, sdkmetric.WithExemplarFilter(filter))
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

func otelMetricExemplarFilter(value string) (exemplar.Filter, bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return nil, false, nil
	case "trace_based", "tracebased", "sampled":
		return exemplar.TraceBasedFilter, true, nil
	case "always_on", "alwayson":
		return exemplar.AlwaysOnFilter, true, nil
	case "always_off", "alwaysoff":
		return exemplar.AlwaysOffFilter, true, nil
	default:
		return nil, false, fmt.Errorf("invalid metric exemplar filter %q: must be one of trace_based, always_on, or always_off", value)
	}
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
