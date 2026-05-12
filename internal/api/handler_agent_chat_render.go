package api

import (
	"time"

	"github.com/hecate/agent-runtime/internal/agentadapters"
	"github.com/hecate/agent-runtime/internal/agentchat"
	"github.com/hecate/agent-runtime/internal/config"
)

// agentChatSnapshotConfig captures the per-snapshot guardrail values
// that need to flow into the API response so clients can render the
// session's turn / wall-clock / idle-timeout limits without a second
// round-trip. Built once per handler request from the live ServerConfig.
type agentChatSnapshotConfig struct {
	MaxTurnsPerSession int
	MaxSessionDuration time.Duration
	IdleTimeout        time.Duration
}

func agentChatSnapshotConfigFromServer(cfg config.ServerConfig) agentChatSnapshotConfig {
	return agentChatSnapshotConfig{
		MaxTurnsPerSession: cfg.AgentChatMaxTurnsPerSession,
		MaxSessionDuration: cfg.AgentChatMaxSessionDuration,
		IdleTimeout:        cfg.AgentChatIdleTimeout,
	}
}

func (h *Handler) agentChatSnapshotConfig() agentChatSnapshotConfig {
	return agentChatSnapshotConfigFromServer(h.config.Server)
}

func renderAgentChatSessionSummary(session agentchat.Session) AgentChatSessionSummaryItem {
	return AgentChatSessionSummaryItem{
		ID:              session.ID,
		Title:           session.Title,
		RuntimeKind:     renderAgentChatRuntimeKind(session),
		AdapterID:       session.AdapterID,
		DriverKind:      session.DriverKind,
		NativeSessionID: session.NativeSessionID,
		TaskID:          session.TaskID,
		LatestRunID:     session.LatestRunID,
		Provider:        session.Provider,
		Model:           session.Model,
		Capabilities:    session.Capabilities,
		Workspace:       session.Workspace,
		WorkspaceBranch: session.WorkspaceBranch,
		Status:          session.Status,
		MessageCount:    len(session.Messages),
		CreatedAt:       formatOptionalTime(session.CreatedAt),
		UpdatedAt:       formatOptionalTime(session.UpdatedAt),
	}
}

func renderAgentChatSession(session agentchat.Session, limits agentChatSnapshotConfig) AgentChatSessionItem {
	messages := make([]AgentChatMessageItem, 0, len(session.Messages))
	for _, message := range session.Messages {
		messages = append(messages, AgentChatMessageItem{
			ID:              message.ID,
			RuntimeKind:     message.RuntimeKind,
			SegmentID:       message.SegmentID,
			TaskID:          message.TaskID,
			RunID:           message.RunID,
			RequestID:       message.RequestID,
			TraceID:         message.TraceID,
			SpanID:          message.SpanID,
			Role:            message.Role,
			Content:         message.Content,
			RawOutput:       message.RawOutput,
			AdapterID:       message.AdapterID,
			AdapterName:     message.AdapterName,
			DriverKind:      message.DriverKind,
			NativeSessionID: message.NativeSessionID,
			Status:          message.Status,
			ExitCode:        message.ExitCode,
			CostMode:        message.CostMode,
			Provider:        message.Provider,
			Model:           message.Model,
			Capabilities:    message.Capabilities,
			Workspace:       message.Workspace,
			DiffStat:        message.DiffStat,
			Diff:            message.Diff,
			CreatedAt:       formatOptionalTime(message.CreatedAt),
			StartedAt:       formatOptionalTime(message.StartedAt),
			CompletedAt:     formatOptionalTime(message.CompletedAt),
			DurationMS:      durationMillis(message.StartedAt, message.CompletedAt),
			Error:           message.Error,
			Activities:      renderAgentChatActivities(message.Activities),
			Usage:           renderAgentChatUsage(message.Usage),
			Timing:          renderAgentChatTiming(message.Timing),
		})
	}
	return AgentChatSessionItem{
		ID:                   session.ID,
		Title:                session.Title,
		RuntimeKind:          renderAgentChatRuntimeKind(session),
		AdapterID:            session.AdapterID,
		DriverKind:           session.DriverKind,
		NativeSessionID:      session.NativeSessionID,
		TaskID:               session.TaskID,
		LatestRunID:          session.LatestRunID,
		Provider:             session.Provider,
		Model:                session.Model,
		Capabilities:         session.Capabilities,
		Workspace:            session.Workspace,
		WorkspaceBranch:      session.WorkspaceBranch,
		Status:               session.Status,
		TurnsUsed:            session.TurnsUsed,
		MaxTurnsPerSession:   limits.MaxTurnsPerSession,
		SessionStartedAt:     formatOptionalTime(session.CreatedAt),
		MaxSessionDurationMS: limits.MaxSessionDuration.Milliseconds(),
		IdleTimeoutMS:        limits.IdleTimeout.Milliseconds(),
		ConfigOptions:        session.ConfigOptions,
		CreatedAt:            formatOptionalTime(session.CreatedAt),
		UpdatedAt:            formatOptionalTime(session.UpdatedAt),
		Segments:             renderAgentChatSegments(session),
		Messages:             messages,
	}
}

type agentChatSegmentBuilder struct {
	item      AgentChatSegmentItem
	startedAt time.Time
	updatedAt time.Time
}

func renderAgentChatSegments(session agentchat.Session) []AgentChatSegmentItem {
	if len(session.Messages) == 0 {
		return nil
	}
	builders := make([]agentChatSegmentBuilder, 0, len(session.Messages)/2+1)
	positions := make(map[string]int)
	for _, message := range session.Messages {
		segmentID := agentChatMessageSegmentID(session, message)
		if segmentID == "" {
			continue
		}
		idx, ok := positions[segmentID]
		if !ok {
			idx = len(builders)
			positions[segmentID] = idx
			builders = append(builders, agentChatSegmentBuilder{
				item: AgentChatSegmentItem{
					ID:          segmentID,
					RuntimeKind: firstNonEmpty(message.RuntimeKind, renderAgentChatRuntimeKind(session)),
					Provider:    firstNonEmpty(message.Provider, session.Provider),
					Model:       firstNonEmpty(message.Model, session.Model),
					TaskID:      message.TaskID,
					Workspace:   firstNonEmpty(message.Workspace, session.Workspace),
				},
			})
		}
		builder := &builders[idx]
		builder.item.MessageCount++
		if builder.item.RuntimeKind == "" {
			builder.item.RuntimeKind = firstNonEmpty(message.RuntimeKind, renderAgentChatRuntimeKind(session))
		}
		if builder.item.Provider == "" {
			builder.item.Provider = message.Provider
		}
		if builder.item.Model == "" {
			builder.item.Model = message.Model
		}
		if builder.item.TaskID == "" {
			builder.item.TaskID = message.TaskID
		}
		if builder.item.Workspace == "" {
			builder.item.Workspace = message.Workspace
		}
		if message.RunID != "" {
			builder.item.LatestRunID = message.RunID
		}
		if message.Status != "" {
			builder.item.Status = message.Status
		}
		if t := agentChatMessageSegmentTime(message); !t.IsZero() {
			if builder.startedAt.IsZero() || t.Before(builder.startedAt) {
				builder.startedAt = t
			}
			if builder.updatedAt.IsZero() || t.After(builder.updatedAt) {
				builder.updatedAt = t
			}
		}
	}
	segments := make([]AgentChatSegmentItem, 0, len(builders))
	for _, builder := range builders {
		item := builder.item
		item.StartedAt = formatOptionalTime(builder.startedAt)
		item.UpdatedAt = formatOptionalTime(builder.updatedAt)
		segments = append(segments, item)
	}
	return segments
}

func agentChatMessageSegmentID(session agentchat.Session, message agentchat.Message) string {
	if message.SegmentID != "" {
		return message.SegmentID
	}
	if message.TaskID != "" {
		return "task:" + message.TaskID
	}
	if session.NativeSessionID != "" {
		return "external:" + session.NativeSessionID
	}
	if session.ID != "" {
		return "session:" + session.ID
	}
	return ""
}

func agentChatMessageSegmentTime(message agentchat.Message) time.Time {
	switch {
	case !message.CreatedAt.IsZero():
		return message.CreatedAt
	case !message.StartedAt.IsZero():
		return message.StartedAt
	case !message.CompletedAt.IsZero():
		return message.CompletedAt
	default:
		return time.Time{}
	}
}

func renderAgentChatRuntimeKind(session agentchat.Session) string {
	if session.RuntimeKind != "" {
		return session.RuntimeKind
	}
	if session.AdapterID != "" {
		return "external_agent"
	}
	return "agent"
}

func agentChatUsageFromResult(usage agentadapters.Usage) agentchat.Usage {
	return agentchat.Usage{
		ContextSize:          usage.ContextSize,
		ContextUsed:          usage.ContextUsed,
		ReportedCostAmount:   usage.ReportedCostAmount,
		ReportedCostCurrency: usage.ReportedCostCurrency,
	}
}

func renderAgentChatUsage(usage agentchat.Usage) *AgentChatUsageItem {
	if usage.Empty() {
		return nil
	}
	return &AgentChatUsageItem{
		ContextSize:          usage.ContextSize,
		ContextUsed:          usage.ContextUsed,
		ReportedCostAmount:   usage.ReportedCostAmount,
		ReportedCostCurrency: usage.ReportedCostCurrency,
	}
}

func renderAgentChatTiming(timing agentchat.Timing) *AgentChatTimingItem {
	if timing.Empty() {
		return nil
	}
	return &AgentChatTimingItem{
		TotalMS:        timing.TotalMS,
		QueueMS:        timing.QueueMS,
		ModelMS:        timing.ModelMS,
		ToolMS:         timing.ToolMS,
		ApprovalWaitMS: timing.ApprovalWaitMS,
		OverheadMS:     timing.OverheadMS,
		TurnCount:      timing.TurnCount,
		ToolCount:      timing.ToolCount,
		Bottleneck:     timing.Bottleneck,
		BottleneckMS:   timing.BottleneckMS,
	}
}

func durationMillis(startedAt, completedAt time.Time) int64 {
	if startedAt.IsZero() || completedAt.IsZero() || completedAt.Before(startedAt) {
		return 0
	}
	return completedAt.Sub(startedAt).Milliseconds()
}
