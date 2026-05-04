package telemetry

import (
	"strings"
	"testing"
)

func TestNormalizeMetricLabelRejectsUnsafeFreeformValues(t *testing.T) {
	t.Parallel()

	if got := NormalizeMetricLabel("  gpt-4o-mini  "); got != "gpt-4o-mini" {
		t.Fatalf("trimmed label = %q, want gpt-4o-mini", got)
	}
	if got := NormalizeMetricLabel("bad\nlabel"); got != MetricLabelOther {
		t.Fatalf("control label = %q, want other", got)
	}
	if got := NormalizeMetricLabel(strings.Repeat("x", metricLabelMaxLength+1)); got != MetricLabelOther {
		t.Fatalf("long label = %q, want other", got)
	}
}

func TestMetricClosedSetNormalizersCollapseUnknownValues(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		fn   func(string) string
		want string
	}{
		{"provider kind known", "Cloud", NormalizeProviderKind, "cloud"},
		{"provider kind unknown", "edge", NormalizeProviderKind, MetricLabelOther},
		{"run status known", "COMPLETED", NormalizeRunStatus, "completed"},
		{"run status unknown", "done", NormalizeRunStatus, MetricLabelOther},
		{"execution kind known", "agent_loop", NormalizeExecutionKind, "agent_loop"},
		{"execution kind unknown", "agent", NormalizeExecutionKind, MetricLabelOther},
		{"step kind known", "tool", NormalizeStepKind, "tool"},
		{"step kind unknown", "browser", NormalizeStepKind, MetricLabelOther},
		{"approval kind known", "network_egress", NormalizeApprovalKind, "network_egress"},
		{"approval kind unknown", "danger", NormalizeApprovalKind, MetricLabelOther},
		{"queue backend known", "sqlite", NormalizeQueueBackend, "sqlite"},
		{"queue backend unknown", "postgres", NormalizeQueueBackend, MetricLabelOther},
		{"driver kind known", "acp", NormalizeAgentDriverKind, "acp"},
		{"driver kind unknown", "process", NormalizeAgentDriverKind, MetricLabelOther},
		{"mcp result known", MCPCallResultToolError, NormalizeMCPCallResult, MCPCallResultToolError},
		{"mcp result unknown", "timeout", NormalizeMCPCallResult, MetricLabelOther},
		{"mcp cache known", MCPCacheEventHit, NormalizeMCPCacheEvent, MCPCacheEventHit},
		{"mcp cache unknown", "warm", NormalizeMCPCacheEvent, MetricLabelOther},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.fn(tc.in); got != tc.want {
				t.Fatalf("%s(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
			}
		})
	}
}
