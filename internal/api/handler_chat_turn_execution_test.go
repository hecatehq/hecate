package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/gateway"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestExternalAgentTurnOutcomeCompletedUsesAdapterTimesAndPlaceholder(t *testing.T) {
	t.Parallel()

	started := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	completed := started.Add(2 * time.Second)

	outcome := newExternalAgentTurnOutcome("Codex", agentadapters.RunResult{
		StartedAt:   started,
		CompletedAt: completed,
	}, nil, nil, time.Time{}, time.Time{})

	if outcome.Status != "completed" {
		t.Fatalf("status = %q, want completed", outcome.Status)
	}
	if outcome.Output != "(agent completed without output)" {
		t.Fatalf("output = %q, want empty-output placeholder", outcome.Output)
	}
	if outcome.ResultLabel != telemetry.ResultSuccess || outcome.DurationMS != 2000 {
		t.Fatalf("result/duration = %q/%d, want success/2000", outcome.ResultLabel, outcome.DurationMS)
	}
}

func TestExternalAgentTurnOutcomeFailureAppendsNormalizedError(t *testing.T) {
	t.Parallel()

	err := errors.New(`{"message":"Internal error: tool failed","data":{"errorKind":"tool"}}`)
	outcome := newExternalAgentTurnOutcome("Codex", agentadapters.RunResult{
		Output: "partial output",
	}, err, nil, time.Unix(10, 0).UTC(), time.Unix(11, 0).UTC())

	wantErr := "Codex error (tool): tool failed"
	if outcome.Status != "failed" || outcome.ErrorText != wantErr || outcome.DisplayErr != wantErr {
		t.Fatalf("outcome = %+v, want failed with normalized error %q", outcome, wantErr)
	}
	if outcome.Output != "partial output\n\n"+wantErr {
		t.Fatalf("output = %q, want partial output plus normalized error", outcome.Output)
	}
	if outcome.ResultLabel != telemetry.ResultError {
		t.Fatalf("result label = %q, want error", outcome.ResultLabel)
	}
}

func TestExternalAgentTurnOutcomeCancelledDoesNotInventOutput(t *testing.T) {
	t.Parallel()

	outcome := newExternalAgentTurnOutcome("Codex", agentadapters.RunResult{}, errors.New("boom"), context.Canceled, time.Unix(10, 0).UTC(), time.Unix(11, 0).UTC())

	if outcome.Status != "cancelled" {
		t.Fatalf("status = %q, want cancelled", outcome.Status)
	}
	if outcome.Output != "" || outcome.ErrorText != "" {
		t.Fatalf("output/error = %q/%q, want empty output/error on cancellation", outcome.Output, outcome.ErrorText)
	}
	if outcome.ResultLabel != telemetry.ResultError {
		t.Fatalf("result label = %q, want error", outcome.ResultLabel)
	}
}

func TestDirectModelTurnOutcomeCompletedUsesChoiceOrPlaceholder(t *testing.T) {
	t.Parallel()

	outcome := newDirectModelTurnOutcome(&gateway.ChatResult{
		Response: &types.ChatResponse{Choices: []types.ChatChoice{{Message: types.Message{Content: "  hello  "}}}},
	}, nil, nil)
	if outcome.Status != "completed" || outcome.Output != "hello" || outcome.ErrorText != "" {
		t.Fatalf("outcome = %+v, want completed hello", outcome)
	}

	outcome = newDirectModelTurnOutcome(&gateway.ChatResult{Response: &types.ChatResponse{}}, nil, nil)
	if outcome.Output != "(model completed without output)" {
		t.Fatalf("empty output = %q, want model placeholder", outcome.Output)
	}
}

func TestDirectModelTurnOutcomeFailureAndCancellation(t *testing.T) {
	t.Parallel()

	err := errors.New("provider failed")
	outcome := newDirectModelTurnOutcome(nil, err, nil)
	if outcome.Status != "failed" || outcome.Output != "provider failed" || outcome.ErrorText != "provider failed" {
		t.Fatalf("failure outcome = %+v, want failed provider error", outcome)
	}

	outcome = newDirectModelTurnOutcome(nil, err, context.Canceled)
	if outcome.Status != "cancelled" || outcome.Output != "model request cancelled" || outcome.ErrorText != "cancelled" {
		t.Fatalf("cancel outcome = %+v, want cancellation result", outcome)
	}
}
