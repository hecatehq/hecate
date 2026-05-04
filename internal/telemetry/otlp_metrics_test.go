package telemetry

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"
)

const testOTLPTimeout = time.Second

func TestOTelMetricExemplarFilterConfig(t *testing.T) {
	t.Parallel()

	sampled := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1},
		SpanID:     trace.SpanID{1},
		TraceFlags: trace.FlagsSampled,
	}))
	notSampled := context.Background()

	cases := []struct {
		name             string
		value            string
		wantConfigured   bool
		wantNotSampledOK bool
		wantSampledOK    bool
	}{
		{"unset uses SDK default", "", false, false, false},
		{"trace based", "trace_based", true, false, true},
		{"always on", "always_on", true, true, true},
		{"always off", "always_off", true, false, false},
		{"trace based alias", "sampled", true, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			filter, configured, err := otelMetricExemplarFilter(tc.value)
			if err != nil {
				t.Fatalf("otelMetricExemplarFilter(%q) error = %v", tc.value, err)
			}
			if configured != tc.wantConfigured {
				t.Fatalf("configured = %v, want %v", configured, tc.wantConfigured)
			}
			if !configured {
				return
			}
			if got := filter(notSampled); got != tc.wantNotSampledOK {
				t.Fatalf("not sampled filter = %v, want %v", got, tc.wantNotSampledOK)
			}
			if got := filter(sampled); got != tc.wantSampledOK {
				t.Fatalf("sampled filter = %v, want %v", got, tc.wantSampledOK)
			}
		})
	}
}

func TestOTelMetricExemplarFilterRejectsUnknownValue(t *testing.T) {
	t.Parallel()

	if _, _, err := otelMetricExemplarFilter("sometimes"); err == nil {
		t.Fatal("otelMetricExemplarFilter() error = nil, want invalid filter error")
	}
}

func TestNewMeterProviderRejectsInvalidExemplarFilter(t *testing.T) {
	t.Parallel()

	_, _, err := NewMeterProvider(context.Background(), OTelMetricOptions{
		ExemplarFilter: "sometimes",
	})
	if err == nil {
		t.Fatal("NewMeterProvider() error = nil, want invalid exemplar filter error")
	}
	if !strings.Contains(err.Error(), "invalid metric exemplar filter") {
		t.Fatalf("NewMeterProvider() error = %q, want invalid metric exemplar filter", err)
	}
}

func TestNewMeterProviderGRPCUsesDefaultEndpointWhenUnset(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), testOTLPTimeout)
	defer cancel()

	exporter, err := newMetricExporter(ctx, OTelMetricOptions{
		Transport: OTLPTransportGRPC,
	})
	if err != nil {
		t.Fatalf("newMetricExporter() error = %v", err)
	}
	if exporter == nil {
		t.Fatal("newMetricExporter() exporter = nil")
	}
}
