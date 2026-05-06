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
		CreatedAt:            formatOptionalTime(session.CreatedAt),
		UpdatedAt:            formatOptionalTime(session.UpdatedAt),
		Messages:             messages,
	}
}

func renderAgentChatRuntimeKind(session agentchat.Session) string {
	if session.RuntimeKind != "" {
		return session.RuntimeKind
	}
	if session.AdapterID != "" {
		return "external_agent"
	}
	return "hecate_agent"
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

func durationMillis(startedAt, completedAt time.Time) int64 {
	if startedAt.IsZero() || completedAt.IsZero() || completedAt.Before(startedAt) {
		return 0
	}
	return completedAt.Sub(startedAt).Milliseconds()
}
