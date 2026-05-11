package gateway

import (
	"testing"
	"time"

	"github.com/hecate/agent-runtime/internal/telemetry"
	"github.com/hecate/agent-runtime/pkg/types"
)

func TestBuildRouteDecisionReport(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 21, 10, 0, 0, 0, time.UTC)
	report := buildRouteDecisionReport([]types.TraceSpan{
		{
			Name: "gateway.router",
			Events: []types.TraceEvent{
				{
					Name:      "router.candidate.considered",
					Timestamp: now,
					Attributes: map[string]any{
						telemetry.AttrGenAIProviderName:   "ollama",
						telemetry.AttrGenAIRequestModel:   "llama3.1:8b",
						telemetry.AttrHecateProviderKind:  "local",
						telemetry.AttrHecateRouteReason:   "provider_default_model",
						telemetry.AttrHecateProviderIndex: 0,
					},
				},
				{
					Name:      "router.candidate.denied",
					Timestamp: now.Add(time.Millisecond),
					Attributes: map[string]any{
						telemetry.AttrGenAIProviderName:            "ollama",
						telemetry.AttrGenAIRequestModel:            "llama3.1:8b",
						telemetry.AttrHecateProviderKind:           "local",
						telemetry.AttrHecateRouteReason:            "provider_default_model",
						telemetry.AttrHecateProviderIndex:          0,
						telemetry.AttrHecateRouteSkipReason:        "budget_denied",
						telemetry.AttrErrorMessage:                 "budget denied",
						telemetry.AttrHecatePolicyRuleID:           "deny-expensive-local",
						telemetry.AttrHecatePolicyAction:           "deny",
						telemetry.AttrHecatePolicyReason:           "local spillover blocked",
						telemetry.AttrHecateCostEstimatedMicrosUSD: int64(1200),
					},
				},
				{
					Name:      "provider.failover.triggered",
					Timestamp: now.Add(1500 * time.Microsecond),
					Attributes: map[string]any{
						telemetry.AttrGenAIProviderName:          "ollama",
						telemetry.AttrGenAIRequestModel:          "llama3.1:8b",
						telemetry.AttrHecateProviderIndex:        0,
						telemetry.AttrHecateFailoverFromProvider: "ollama",
						telemetry.AttrHecateFailoverFromModel:    "llama3.1:8b",
						telemetry.AttrHecateFailoverToProvider:   "openai",
						telemetry.AttrHecateFailoverToModel:      "gpt-4o-mini",
						telemetry.AttrHecateFailoverReason:       "provider_retry_exhausted",
					},
				},
				{
					Name:      "router.candidate.selected",
					Timestamp: now.Add(2 * time.Millisecond),
					Attributes: map[string]any{
						telemetry.AttrGenAIProviderName:            "openai",
						telemetry.AttrGenAIRequestModel:            "gpt-4o-mini",
						telemetry.AttrHecateProviderKind:           "cloud",
						telemetry.AttrHecateRouteReason:            "provider_default_model",
						telemetry.AttrHecateProviderIndex:          1,
						telemetry.AttrHecateCostEstimatedMicrosUSD: int64(2400),
					},
				},
				{
					Name:      "provider.retry.scheduled",
					Timestamp: now.Add(2500 * time.Microsecond),
					Attributes: map[string]any{
						telemetry.AttrGenAIProviderName:      "openai",
						telemetry.AttrGenAIRequestModel:      "gpt-4o-mini",
						telemetry.AttrHecateProviderIndex:    1,
						telemetry.AttrHecateRetryAttempt:     1,
						telemetry.AttrHecateRetryNextAttempt: 2,
						telemetry.AttrHecateRetryBackoffMS:   int64(200),
					},
				},
				{
					Name:      "provider.call.finished",
					Timestamp: now.Add(2750 * time.Microsecond),
					Attributes: map[string]any{
						telemetry.AttrGenAIProviderName:       "openai",
						telemetry.AttrGenAIRequestModel:       "gpt-4o-mini",
						telemetry.AttrHecateProviderIndex:     1,
						telemetry.AttrHecateRetryAttempt:      2,
						telemetry.AttrHecateProviderLatencyMS: int64(320),
					},
				},
				{
					Name:      "provider.failover.selected",
					Timestamp: now.Add(3 * time.Millisecond),
					Attributes: map[string]any{
						telemetry.AttrGenAIProviderName:          "openai",
						telemetry.AttrGenAIRequestModel:          "gpt-4o-mini",
						telemetry.AttrHecateProviderIndex:        1,
						telemetry.AttrHecateFailoverFromProvider: "ollama",
						telemetry.AttrHecateFailoverFromModel:    "llama3.1:8b",
						telemetry.AttrHecateFailoverToProvider:   "openai",
						telemetry.AttrHecateFailoverToModel:      "gpt-4o-mini",
						telemetry.AttrHecateFailoverReason:       "provider_default_model_failover",
					},
				},
			},
		},
	})

	if report.FinalProvider != "openai" {
		t.Fatalf("FinalProvider = %q, want openai", report.FinalProvider)
	}
	if report.FallbackFrom != "ollama" {
		t.Fatalf("FallbackFrom = %q, want ollama", report.FallbackFrom)
	}
	if len(report.Candidates) != 2 {
		t.Fatalf("candidate count = %d, want 2", len(report.Candidates))
	}
	if report.Candidates[0].Outcome != "denied" {
		t.Fatalf("candidate[0].Outcome = %q, want denied", report.Candidates[0].Outcome)
	}
	if report.Candidates[0].SkipReason != "budget_denied" {
		t.Fatalf("candidate[0].SkipReason = %q, want budget_denied", report.Candidates[0].SkipReason)
	}
	if report.Candidates[0].PolicyRuleID != "deny-expensive-local" {
		t.Fatalf("candidate[0].PolicyRuleID = %q, want deny-expensive-local", report.Candidates[0].PolicyRuleID)
	}
	if report.Candidates[0].PolicyReason != "local spillover blocked" {
		t.Fatalf("candidate[0].PolicyReason = %q, want local spillover blocked", report.Candidates[0].PolicyReason)
	}
	if report.Candidates[1].Outcome != "completed" {
		t.Fatalf("candidate[1].Outcome = %q, want completed", report.Candidates[1].Outcome)
	}
	if report.Candidates[1].RetryCount != 1 {
		t.Fatalf("candidate[1].RetryCount = %d, want 1", report.Candidates[1].RetryCount)
	}
	if report.Candidates[1].Attempt != 2 {
		t.Fatalf("candidate[1].Attempt = %d, want 2", report.Candidates[1].Attempt)
	}
	if report.Candidates[1].LatencyMS != 320 {
		t.Fatalf("candidate[1].LatencyMS = %d, want 320", report.Candidates[1].LatencyMS)
	}
	if report.Candidates[1].FailoverFrom != "ollama" {
		t.Fatalf("candidate[1].FailoverFrom = %q, want ollama", report.Candidates[1].FailoverFrom)
	}
	if len(report.Failovers) != 2 {
		t.Fatalf("failover count = %d, want 2", len(report.Failovers))
	}
	if report.Failovers[0].ToProvider != "openai" {
		t.Fatalf("failover[0].ToProvider = %q, want openai", report.Failovers[0].ToProvider)
	}
}

func TestBuildRouteDecisionReportNormalizesUnfinishedCandidate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 21, 11, 0, 0, 0, time.UTC)
	report := buildRouteDecisionReport([]types.TraceSpan{
		{
			Name: "gateway.router",
			Events: []types.TraceEvent{
				{
					Name:      "router.candidate.considered",
					Timestamp: now,
					Attributes: map[string]any{
						telemetry.AttrGenAIProviderName:   "ollama",
						telemetry.AttrGenAIRequestModel:   "llama3.1:8b",
						telemetry.AttrHecateProviderKind:  "local",
						telemetry.AttrHecateRouteReason:   "provider_default_model",
						telemetry.AttrHecateProviderIndex: 0,
					},
				},
			},
		},
	})

	if len(report.Candidates) != 1 {
		t.Fatalf("candidate count = %d, want 1", len(report.Candidates))
	}
	if report.Candidates[0].Outcome != "skipped" {
		t.Fatalf("candidate outcome = %q, want skipped", report.Candidates[0].Outcome)
	}
	if report.Candidates[0].SkipReason != "not_selected" {
		t.Fatalf("candidate skip reason = %q, want not_selected", report.Candidates[0].SkipReason)
	}
}

func TestBuildRouteDecisionReportInfersFinalRouteFromCompletedCandidate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	report := buildRouteDecisionReport([]types.TraceSpan{
		{
			Name: "gateway.provider",
			Events: []types.TraceEvent{
				{
					Name:      "provider.call.finished",
					Timestamp: now,
					Attributes: map[string]any{
						telemetry.AttrGenAIProviderName:       "openai",
						telemetry.AttrGenAIRequestModel:       "gpt-4o-mini",
						telemetry.AttrHecateProviderKind:      "cloud",
						telemetry.AttrHecateRouteReason:       "provider_default_model",
						telemetry.AttrHecateProviderIndex:     0,
						telemetry.AttrHecateProviderLatencyMS: int64(100),
					},
				},
			},
		},
	})

	if report.FinalProvider != "openai" {
		t.Fatalf("FinalProvider = %q, want openai", report.FinalProvider)
	}
	if report.FinalModel != "gpt-4o-mini" {
		t.Fatalf("FinalModel = %q, want gpt-4o-mini", report.FinalModel)
	}
	if len(report.Candidates) != 1 || report.Candidates[0].Outcome != "completed" {
		t.Fatalf("candidate = %#v, want one completed candidate", report.Candidates)
	}
}
