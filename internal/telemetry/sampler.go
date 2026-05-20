package telemetry

import (
	"strings"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// SamplerName values are aligned with the OTEL_TRACES_SAMPLER spec so operators
// who already know the standard names can reuse them with the HECATE_OTEL_*
// equivalents. An empty or unrecognized name falls back to parentbased_always_on,
// which preserves upstream sampling decisions and matches the SDK default.
const (
	SamplerAlwaysOn                = "always_on"
	SamplerAlwaysOff               = "always_off"
	SamplerTraceIDRatio            = "traceidratio"
	SamplerParentBasedAlwaysOn     = "parentbased_always_on"
	SamplerParentBasedAlwaysOff    = "parentbased_always_off"
	SamplerParentBasedTraceIDRatio = "parentbased_traceidratio"
)

// BuildSampler resolves a sampler from the env-style name and arg pair. arg is
// only consulted by the ratio samplers; values outside [0, 1] clamp to the
// nearest endpoint inside TraceIDRatioBased itself.
func BuildSampler(name string, arg float64) sdktrace.Sampler {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case SamplerAlwaysOn:
		return sdktrace.AlwaysSample()
	case SamplerAlwaysOff:
		return sdktrace.NeverSample()
	case SamplerTraceIDRatio:
		return sdktrace.TraceIDRatioBased(arg)
	case SamplerParentBasedAlwaysOff:
		return sdktrace.ParentBased(sdktrace.NeverSample())
	case SamplerParentBasedTraceIDRatio:
		return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(arg))
	case "", SamplerParentBasedAlwaysOn:
		return sdktrace.ParentBased(sdktrace.AlwaysSample())
	default:
		return sdktrace.ParentBased(sdktrace.AlwaysSample())
	}
}
