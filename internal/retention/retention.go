package retention

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/hecate/agent-runtime/internal/config"
	"github.com/hecate/agent-runtime/internal/profiler"
	"github.com/hecate/agent-runtime/internal/telemetry"
)

const (
	SubsystemTraces          = "trace_snapshots"
	SubsystemUsageEvents     = "usage_events"
	SubsystemAuditEvents     = "audit_events"
	SubsystemProviderHistory = "provider_history"
	// SubsystemTurnEvents prunes the high-cardinality
	// `turn.completed` rows from the task-run events table.
	// Other event types (run.started / run.finished / approval.*) are
	// kept for forensics — turn events are bulk telemetry that's only
	// useful while the run is hot.
	SubsystemTurnEvents         = "turn_events"
	SubsystemAgentChatApprovals = "agent_chat_approvals"
)

// Pruner prunes old or excess records from a subsystem store.
type Pruner interface {
	Prune(ctx context.Context, maxAge time.Duration, maxCount int) (int, error)
}

// UsageEventPruner is implemented by usage stores that support event pruning.
type UsageEventPruner interface {
	PruneEvents(ctx context.Context, maxAge time.Duration, maxCount int) (int, error)
}

// AuditEventPruner is implemented by control-plane stores that support audit pruning.
type AuditEventPruner interface {
	PruneAuditEvents(ctx context.Context, maxAge time.Duration, maxCount int) (int, error)
}

// TurnEventPruner is implemented by task-state stores that support
// pruning `turn.completed` rows from the run-events table.
// Other event types are not touched.
type TurnEventPruner interface {
	PruneTurnEvents(ctx context.Context, maxAge time.Duration, maxCount int) (int, error)
}

// AgentChatApprovalPruner is implemented by approval stores that
// support pruning resolved external-adapter approvals + expired
// grants. Pending rows are never pruned. Grants ignore maxAge /
// maxCount; only their own ExpiresAt drives deletion.
type AgentChatApprovalPruner interface {
	PruneApprovals(ctx context.Context, now time.Time, maxAge time.Duration, maxCount int) (int64, error)
	PruneExpiredGrants(ctx context.Context, now time.Time) (int64, error)
}

type usagePrunerAdapter struct{ p UsageEventPruner }

func (a usagePrunerAdapter) Prune(ctx context.Context, maxAge time.Duration, maxCount int) (int, error) {
	return a.p.PruneEvents(ctx, maxAge, maxCount)
}

type auditPrunerAdapter struct{ p AuditEventPruner }

func (a auditPrunerAdapter) Prune(ctx context.Context, maxAge time.Duration, maxCount int) (int, error) {
	return a.p.PruneAuditEvents(ctx, maxAge, maxCount)
}

type turnEventPrunerAdapter struct{ p TurnEventPruner }

func (a turnEventPrunerAdapter) Prune(ctx context.Context, maxAge time.Duration, maxCount int) (int, error) {
	return a.p.PruneTurnEvents(ctx, maxAge, maxCount)
}

// agentChatApprovalPrunerAdapter wraps an AgentChatApprovalPruner so
// the retention worker can call Prune(maxAge, maxCount) uniformly.
// One pass deletes resolved approvals (subject to maxAge / maxCount)
// then deletes expired grants (independent of maxAge / maxCount —
// grants honor only their own ExpiresAt, never the retention window).
// Both deletion counts are summed in the returned `deleted` total so
// operators see total rows removed by this subsystem in one number.
type agentChatApprovalPrunerAdapter struct{ p AgentChatApprovalPruner }

func (a agentChatApprovalPrunerAdapter) Prune(ctx context.Context, maxAge time.Duration, maxCount int) (int, error) {
	now := time.Now().UTC()
	approvals, err := a.p.PruneApprovals(ctx, now, maxAge, maxCount)
	if err != nil {
		return int(approvals), err
	}
	grants, err := a.p.PruneExpiredGrants(ctx, now)
	if err != nil {
		return int(approvals + grants), err
	}
	return int(approvals + grants), nil
}

type subsystemEntry struct {
	name   string
	policy config.RetentionPolicy
	pruner Pruner
}

type RunRequest struct {
	Trigger    string
	Subsystems []string
	Actor      string
	RequestID  string
}

type SubsystemResult struct {
	Name     string        `json:"name"`
	Deleted  int           `json:"deleted"`
	MaxAge   time.Duration `json:"-"`
	MaxCount int           `json:"max_count"`
	Error    string        `json:"error,omitempty"`
	Skipped  bool          `json:"skipped,omitempty"`
}

type RunResult struct {
	StartedAt  time.Time         `json:"started_at"`
	FinishedAt time.Time         `json:"finished_at"`
	Trigger    string            `json:"trigger"`
	Results    []SubsystemResult `json:"results"`
}

type Manager struct {
	logger     *slog.Logger
	cfg        config.RetentionConfig
	tracer     profiler.Tracer
	subsystems []subsystemEntry
	history    HistoryStore
}

func NewManager(
	logger *slog.Logger,
	cfg config.RetentionConfig,
	tracer profiler.Tracer,
	traces Pruner,
	usage UsageEventPruner,
	audit AuditEventPruner,
	providerHistory Pruner,
	turnEvents TurnEventPruner,
	approvals AgentChatApprovalPruner,
	history HistoryStore,
) *Manager {
	var usagePruner Pruner
	if usage != nil {
		usagePruner = usagePrunerAdapter{usage}
	}
	var auditPruner Pruner
	if audit != nil {
		auditPruner = auditPrunerAdapter{audit}
	}
	var turnEventsPruner Pruner
	if turnEvents != nil {
		turnEventsPruner = turnEventPrunerAdapter{turnEvents}
	}
	var approvalsPruner Pruner
	if approvals != nil {
		approvalsPruner = agentChatApprovalPrunerAdapter{approvals}
	}
	return &Manager{
		logger: logger,
		cfg:    cfg,
		tracer: tracer,
		subsystems: []subsystemEntry{
			{SubsystemTraces, cfg.TraceSnapshots, traces},
			{SubsystemUsageEvents, cfg.UsageEvents, usagePruner},
			{SubsystemAuditEvents, cfg.AuditEvents, auditPruner},
			{SubsystemProviderHistory, cfg.ProviderHistory, providerHistory},
			{SubsystemTurnEvents, cfg.TurnEvents, turnEventsPruner},
			{SubsystemAgentChatApprovals, cfg.AgentChatApprovals, approvalsPruner},
		},
		history: history,
	}
}

func (m *Manager) Enabled() bool {
	return m != nil && m.cfg.Enabled
}

func (m *Manager) Run(ctx context.Context, req RunRequest) RunResult {
	startedAt := time.Now().UTC()
	trigger := req.Trigger
	if trigger == "" {
		trigger = "manual"
	}

	traceRequestID := fmt.Sprintf("retention:%s:%d", trigger, startedAt.UnixNano())
	trace := m.tracer.Start(traceRequestID)
	defer trace.Finalize()
	trace.Record(telemetry.EventRetentionRunStarted, map[string]any{
		telemetry.AttrRetentionTrigger: trigger,
	})

	results := make([]SubsystemResult, 0, len(m.subsystems))
	for _, sub := range m.subsystems {
		result := SubsystemResult{
			Name:     sub.name,
			MaxAge:   sub.policy.MaxAge,
			MaxCount: sub.policy.MaxCount,
		}
		if !shouldRun(req.Subsystems, sub.name) || sub.pruner == nil {
			result.Skipped = true
			results = append(results, result)
			continue
		}
		deleted, err := sub.pruner.Prune(ctx, sub.policy.MaxAge, sub.policy.MaxCount)
		result.Deleted = deleted
		if err != nil {
			result.Error = err.Error()
			trace.Record(telemetry.EventRetentionSubsystemFailed, map[string]any{
				telemetry.AttrRetentionSubsystem: sub.name,
				telemetry.AttrErrorMessage:       err.Error(),
			})
		} else {
			trace.Record(telemetry.EventRetentionSubsystemFinished, map[string]any{
				telemetry.AttrRetentionSubsystem: sub.name,
				telemetry.AttrRetentionDeleted:   deleted,
			})
			telemetry.Info(m.logger, ctx, "retention subsystem finished",
				slog.String("subsystem", sub.name),
				slog.Int("deleted", deleted),
				slog.Duration("max_age", sub.policy.MaxAge),
				slog.Int("max_count", sub.policy.MaxCount),
				slog.String("trigger", trigger),
			)
		}
		results = append(results, result)
	}

	finishedAt := time.Now().UTC()
	trace.Record(telemetry.EventRetentionRunFinished, map[string]any{
		telemetry.AttrRetentionTrigger: trigger,
		telemetry.AttrRetentionResults: len(results),
	})

	run := RunResult{
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
		Trigger:    trigger,
		Results:    results,
	}
	if m.history != nil {
		record := HistoryRecord{
			StartedAt:  run.StartedAt.UTC().Format(time.RFC3339Nano),
			FinishedAt: run.FinishedAt.UTC().Format(time.RFC3339Nano),
			Trigger:    run.Trigger,
			Actor:      req.Actor,
			RequestID:  req.RequestID,
			Results:    cloneSubsystemResults(run.Results),
		}
		if err := m.history.AppendRun(ctx, record); err != nil {
			trace.Record(telemetry.EventRetentionHistoryFailed, map[string]any{
				telemetry.AttrErrorMessage: err.Error(),
			})
			telemetry.Warn(m.logger, ctx, "retention history append failed", slog.Any("error", err))
		} else {
			trace.Record(telemetry.EventRetentionHistoryPersisted, map[string]any{
				telemetry.AttrRetentionTrigger: run.Trigger,
			})
		}
	}
	return run
}

func (m *Manager) ListRuns(ctx context.Context, limit int) ([]HistoryRecord, error) {
	if m == nil || m.history == nil {
		return nil, nil
	}
	return m.history.ListRuns(ctx, limit)
}

func (m *Manager) RunLoop(ctx context.Context) {
	if m == nil || !m.cfg.Enabled || m.cfg.Interval <= 0 {
		return
	}

	ticker := time.NewTicker(m.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.Run(ctx, RunRequest{Trigger: "scheduled"})
		}
	}
}

func shouldRun(selected []string, subsystem string) bool {
	if len(selected) == 0 {
		return true
	}
	return slices.Contains(selected, subsystem)
}
