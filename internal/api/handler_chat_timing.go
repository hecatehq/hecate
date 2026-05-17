package api

import (
	"context"
	"strings"
	"time"

	"github.com/hecate/agent-runtime/internal/chat"
	"github.com/hecate/agent-runtime/internal/telemetry"
	"github.com/hecate/agent-runtime/pkg/types"
)

func (h *Handler) hecateAgentTiming(ctx context.Context, run types.TaskRun, chatStartedAt, chatCompletedAt time.Time) chat.Timing {
	if h == nil || h.taskStore == nil || run.ID == "" {
		return hecateAgentTimingFromRunState(run, nil, nil, nil, chatStartedAt, chatCompletedAt)
	}
	steps, _ := h.taskStore.ListSteps(ctx, run.ID)
	approvals, _ := h.taskStore.ListApprovals(ctx, run.TaskID)
	events, _ := h.taskStore.ListRunEvents(ctx, run.TaskID, run.ID, 0, 1000)
	return hecateAgentTimingFromRunState(run, steps, approvals, events, chatStartedAt, chatCompletedAt)
}

func hecateAgentTimingFromRunState(run types.TaskRun, steps []types.TaskStep, approvals []types.TaskApproval, events []types.TaskRunEvent, chatStartedAt, chatCompletedAt time.Time) chat.Timing {
	total := durationBetween(chatStartedAt, chatCompletedAt)
	if total == 0 {
		total = durationBetween(run.StartedAt, run.FinishedAt)
	}

	firstWorkAt := firstRunWorkTime(run, steps, events, chatStartedAt)
	queue := durationBetween(chatStartedAt, firstWorkAt)

	var modelMS int64
	var toolMS int64
	var turnCount int
	var toolCount int
	for _, step := range steps {
		if step.RunID != "" && step.RunID != run.ID {
			continue
		}
		durationMS := durationBetween(step.StartedAt, step.FinishedAt)
		switch {
		case isModelTimingStep(step):
			modelMS += durationMS
			turnCount++
		case isToolTimingStep(step):
			toolMS += durationMS
			toolCount++
		}
	}

	var approvalWaitMS int64
	for _, approval := range approvals {
		if approval.RunID != run.ID {
			continue
		}
		approvalWaitMS += durationBetween(approval.CreatedAt, approval.ResolvedAt)
	}

	overheadMS := total - queue - modelMS - toolMS - approvalWaitMS
	if overheadMS < 0 {
		overheadMS = 0
	}
	timing := chat.Timing{
		TotalMS:        total,
		QueueMS:        queue,
		ModelMS:        modelMS,
		ToolMS:         toolMS,
		ApprovalWaitMS: approvalWaitMS,
		OverheadMS:     overheadMS,
		TurnCount:      turnCount,
		ToolCount:      toolCount,
	}
	timing.Bottleneck, timing.BottleneckMS = timingBottleneck(timing)
	return timing
}

func firstRunWorkTime(run types.TaskRun, steps []types.TaskStep, events []types.TaskRunEvent, chatStartedAt time.Time) time.Time {
	var first time.Time
	for _, event := range events {
		if event.EventType != "run.started" || event.CreatedAt.IsZero() {
			continue
		}
		first = earlierNonZero(first, event.CreatedAt)
	}
	for _, step := range steps {
		if step.RunID != "" && step.RunID != run.ID {
			continue
		}
		first = earlierNonZero(first, step.StartedAt)
	}
	if first.IsZero() {
		first = run.StartedAt
	}
	if first.IsZero() {
		return chatStartedAt
	}
	return first
}

func earlierNonZero(current, candidate time.Time) time.Time {
	if candidate.IsZero() {
		return current
	}
	if current.IsZero() || candidate.Before(current) {
		return candidate
	}
	return current
}

func durationBetween(start, end time.Time) int64 {
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return 0
	}
	return end.Sub(start).Milliseconds()
}

func isModelTimingStep(step types.TaskStep) bool {
	return step.Kind == "model" || strings.Contains(step.ToolName, "agent_loop_llm")
}

func isToolTimingStep(step types.TaskStep) bool {
	if step.Kind == "approval" || isModelTimingStep(step) {
		return false
	}
	return step.Kind == "tool" || (step.ToolName != "" && !strings.HasPrefix(step.ToolName, "builtin.agent_loop_"))
}

func timingBottleneck(timing chat.Timing) (string, int64) {
	items := []struct {
		label string
		ms    int64
	}{
		{label: "model", ms: timing.ModelMS},
		{label: "tools", ms: timing.ToolMS},
		{label: "approval", ms: timing.ApprovalWaitMS},
		{label: "queue", ms: timing.QueueMS},
		{label: "overhead", ms: timing.OverheadMS},
	}
	var label string
	var value int64
	for _, item := range items {
		if item.ms > value {
			label = item.label
			value = item.ms
		}
	}
	return label, value
}

func agentChatRunTimingMetrics(timing chat.Timing) telemetry.AgentChatRunTimingRecord {
	if timing.Empty() {
		return telemetry.AgentChatRunTimingRecord{}
	}
	return telemetry.AgentChatRunTimingRecord{
		QueueMS:        timing.QueueMS,
		ModelMS:        timing.ModelMS,
		ToolMS:         timing.ToolMS,
		ApprovalWaitMS: timing.ApprovalWaitMS,
		OverheadMS:     timing.OverheadMS,
	}
}

func addHecateAgentTimingTraceAttrs(attrs map[string]any, timing chat.Timing) {
	if attrs == nil || timing.Empty() {
		return
	}
	attrs[telemetry.AttrHecateChatTimingTotalMS] = timing.TotalMS
	attrs[telemetry.AttrHecateChatTimingQueueMS] = timing.QueueMS
	attrs[telemetry.AttrHecateChatTimingModelMS] = timing.ModelMS
	attrs[telemetry.AttrHecateChatTimingToolMS] = timing.ToolMS
	attrs[telemetry.AttrHecateChatTimingApprovalMS] = timing.ApprovalWaitMS
	attrs[telemetry.AttrHecateChatTimingOverheadMS] = timing.OverheadMS
	if timing.Bottleneck != "" {
		attrs[telemetry.AttrHecateChatTimingBottleneck] = timing.Bottleneck
		attrs[telemetry.AttrHecateChatTimingBottleneckMS] = timing.BottleneckMS
	}
}
