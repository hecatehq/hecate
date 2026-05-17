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
	SubsystemTurnEvents    = "turn_events"
	SubsystemChatApprovals = "chat_approvals"
)

// Pruner prunes old or excess records from a subsystem store. Each
// subsystem store implements this directly; the retention worker
// holds a flat []Pruner keyed by subsystem name, without per-store
// adapter shims. The approval store handles the (now, approvals +
// grants) pair internally via the helper in internal/agentadapters
// — see ApprovalRetentionStore.Prune for the contract.
type Pruner interface {
	Prune(ctx context.Context, maxAge time.Duration, maxCount int) (int, error)
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
	usage Pruner,
	audit Pruner,
	providerHistory Pruner,
	turnEvents Pruner,
	approvals Pruner,
	history HistoryStore,
) *Manager {
	return &Manager{
		logger: logger,
		cfg:    cfg,
		tracer: tracer,
		subsystems: []subsystemEntry{
			{SubsystemTraces, cfg.TraceSnapshots, traces},
			{SubsystemUsageEvents, cfg.UsageEvents, usage},
			{SubsystemAuditEvents, cfg.AuditEvents, audit},
			{SubsystemProviderHistory, cfg.ProviderHistory, providerHistory},
			{SubsystemTurnEvents, cfg.TurnEvents, turnEvents},
			{SubsystemChatApprovals, cfg.ChatApprovals, approvals},
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
