package telemetry

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

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
