package api

import (
	"time"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/config"
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
		MaxTurnsPerSession: cfg.ChatMaxTurnsPerSession,
		MaxSessionDuration: cfg.ChatMaxSessionDuration,
		IdleTimeout:        cfg.ChatIdleTimeout,
	}
}

func (h *Handler) agentChatSnapshotConfig() agentChatSnapshotConfig {
	return agentChatSnapshotConfigFromServer(h.config.Server)
}

func renderChatSessionSummary(session chat.Session) ChatSessionSummaryItem {
	return ChatSessionSummaryItem{
		ID:              session.ID,
		Title:           session.Title,
		ProjectID:       session.ProjectID,
		AgentID:         renderChatAgentID(session),
		DriverKind:      session.DriverKind,
		NativeSessionID: session.NativeSessionID,
		TaskID:          session.TaskID,
		LatestRunID:     session.LatestRunID,
		Provider:        session.Provider,
		Model:           session.Model,
		Capabilities:    session.Capabilities,
		RTKEnabled:      session.RTKEnabled,
		Workspace:       session.Workspace,
		WorkspaceBranch: session.WorkspaceBranch,
		Status:          session.Status,
		MessageCount:    len(session.Messages),
		CreatedAt:       formatOptionalTime(session.CreatedAt),
		UpdatedAt:       formatOptionalTime(session.UpdatedAt),
	}
}

func renderChatSession(session chat.Session, limits agentChatSnapshotConfig) ChatSessionItem {
	messages := make([]ChatMessageItem, 0, len(session.Messages))
	for _, message := range session.Messages {
		messages = append(messages, ChatMessageItem{
			ID:              message.ID,
			ExecutionMode:   message.ExecutionMode,
			SegmentID:       message.SegmentID,
			TaskID:          message.TaskID,
			RunID:           message.RunID,
			RequestID:       message.RequestID,
			TraceID:         message.TraceID,
			SpanID:          message.SpanID,
			Role:            message.Role,
			Content:         message.Content,
			RawOutput:       message.RawOutput,
			AgentID:         message.AgentID,
			AgentName:       message.AgentName,
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
			Usage:           renderChatUsage(message.Usage),
			Timing:          renderChatTiming(message.Timing),
			ContextPacket:   renderChatContextPacket(message.Context),
		})
	}
	return ChatSessionItem{
		ID:                   session.ID,
		Title:                session.Title,
		ProjectID:            session.ProjectID,
		AgentID:              renderChatAgentID(session),
		DriverKind:           session.DriverKind,
		NativeSessionID:      session.NativeSessionID,
		TaskID:               session.TaskID,
		LatestRunID:          session.LatestRunID,
		Provider:             session.Provider,
		Model:                session.Model,
		Capabilities:         session.Capabilities,
		RTKEnabled:           session.RTKEnabled,
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
		Segments:             renderChatSegments(session),
		Messages:             messages,
	}
}

func renderChatContextPacket(packet chat.ContextPacket) *ChatContextPacketItem {
	if packet.Empty() {
		return nil
	}
	sources := make([]ChatContextSourceItem, 0, len(packet.Sources))
	for _, source := range packet.Sources {
		sources = append(sources, ChatContextSourceItem{
			Kind:   source.Kind,
			Label:  source.Label,
			Detail: source.Detail,
			Trust:  source.Trust,
		})
	}
	return &ChatContextPacketItem{
		Version:              packet.Version,
		ExecutionMode:        packet.ExecutionMode,
		Provider:             packet.Provider,
		Model:                packet.Model,
		Workspace:            packet.Workspace,
		SystemPromptIncluded: packet.SystemPromptIncluded,
		MessageCount:         packet.MessageCount,
		Sources:              sources,
	}
}

type agentChatSegmentBuilder struct {
	item      ChatSegmentItem
	startedAt time.Time
	updatedAt time.Time
}

func renderChatSegments(session chat.Session) []ChatSegmentItem {
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
				item: ChatSegmentItem{
					ID:            segmentID,
					ExecutionMode: firstNonEmpty(message.ExecutionMode, defaultMessageExecutionModeForRender(session)),
					Provider:      firstNonEmpty(message.Provider, session.Provider),
					Model:         firstNonEmpty(message.Model, session.Model),
					TaskID:        message.TaskID,
					Workspace:     firstNonEmpty(message.Workspace, session.Workspace),
				},
			})
		}
		builder := &builders[idx]
		builder.item.MessageCount++
		if builder.item.ExecutionMode == "" {
			builder.item.ExecutionMode = firstNonEmpty(message.ExecutionMode, defaultMessageExecutionModeForRender(session))
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
	segments := make([]ChatSegmentItem, 0, len(builders))
	for _, builder := range builders {
		item := builder.item
		item.StartedAt = formatOptionalTime(builder.startedAt)
		item.UpdatedAt = formatOptionalTime(builder.updatedAt)
		segments = append(segments, item)
	}
	return segments
}

func agentChatMessageSegmentID(session chat.Session, message chat.Message) string {
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

func agentChatMessageSegmentTime(message chat.Message) time.Time {
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

func renderChatAgentID(session chat.Session) string {
	if session.AgentID != "" {
		return session.AgentID
	}
	return chat.DefaultAgentID
}

func defaultMessageExecutionModeForRender(session chat.Session) string {
	if session.AgentID != "" && session.AgentID != chat.DefaultAgentID {
		return chat.ExecutionModeExternalAgent
	}
	if session.TaskID != "" {
		return chat.ExecutionModeHecateTask
	}
	return chat.ExecutionModeDirectModel
}

func agentChatUsageFromResult(usage agentadapters.Usage) chat.Usage {
	return chat.Usage{
		ContextSize:          usage.ContextSize,
		ContextUsed:          usage.ContextUsed,
		ReportedCostAmount:   usage.ReportedCostAmount,
		ReportedCostCurrency: usage.ReportedCostCurrency,
	}
}

func renderChatUsage(usage chat.Usage) *ChatUsageItem {
	if usage.Empty() {
		return nil
	}
	return &ChatUsageItem{
		ContextSize:          usage.ContextSize,
		ContextUsed:          usage.ContextUsed,
		ReportedCostAmount:   usage.ReportedCostAmount,
		ReportedCostCurrency: usage.ReportedCostCurrency,
	}
}

func renderChatTiming(timing chat.Timing) *ChatTimingItem {
	if timing.Empty() {
		return nil
	}
	return &ChatTimingItem{
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
