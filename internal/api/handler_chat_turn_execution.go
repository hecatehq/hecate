package api

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/gateway"
	"github.com/hecatehq/hecate/internal/safetext"
	"github.com/hecatehq/hecate/internal/telemetry"
)

type externalAgentTurnOutcome struct {
	Status      string
	Output      string
	DisplayErr  string
	ErrorText   string
	StartedAt   time.Time
	CompletedAt time.Time
	DurationMS  int64
	ResultLabel string
}

func newExternalAgentTurnOutcome(adapterName string, result agentadapters.RunResult, runErr, ctxErr error, startedAt, completedAt time.Time) externalAgentTurnOutcome {
	status := "completed"
	if runErr != nil {
		status = "failed"
	}
	if errors.Is(ctxErr, context.Canceled) {
		status = "cancelled"
	}

	output := strings.TrimSpace(result.Output)
	displayErr := ""
	if runErr != nil {
		displayErr = agentadapters.NormalizeError(adapterName, runErr)
	}
	if status != "cancelled" && runErr != nil {
		if output == "" {
			output = displayErr
		} else {
			output = output + "\n\n" + displayErr
		}
	}
	if status != "cancelled" && output == "" {
		output = "(agent completed without output)"
	}

	if !result.StartedAt.IsZero() {
		startedAt = result.StartedAt
	}
	if !result.CompletedAt.IsZero() {
		completedAt = result.CompletedAt
	}
	errorText := ""
	if runErr != nil && status != "cancelled" {
		errorText = displayErr
	}
	resultLabel := telemetry.ResultSuccess
	if runErr != nil || status == "cancelled" {
		resultLabel = telemetry.ResultError
	}

	return externalAgentTurnOutcome{
		Status:      status,
		Output:      output,
		DisplayErr:  displayErr,
		ErrorText:   errorText,
		StartedAt:   startedAt,
		CompletedAt: completedAt,
		DurationMS:  completedAt.Sub(startedAt).Milliseconds(),
		ResultLabel: resultLabel,
	}
}

type directModelTurnOutcome struct {
	Status    string
	Output    string
	ErrorText string
}

func newDirectModelTurnOutcome(result *gateway.ChatResult, runErr, ctxErr error) directModelTurnOutcome {
	status := "completed"
	output := ""
	errorText := ""
	if runErr != nil {
		status = "failed"
		errorText = safetext.ErrorMessage(runErr)
		output = errorText
	}
	if errors.Is(ctxErr, context.Canceled) {
		status = "cancelled"
		errorText = "cancelled"
		output = "model request cancelled"
	}
	if result != nil && result.Response != nil {
		if len(result.Response.Choices) > 0 {
			output = strings.TrimSpace(result.Response.Choices[0].Message.Content)
		}
		if output == "" {
			output = "(model completed without output)"
		}
	}
	return directModelTurnOutcome{Status: status, Output: output, ErrorText: errorText}
}
