package taskschedule

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/hecatehq/hecate/internal/orchestrator"
	"github.com/hecatehq/hecate/internal/taskapp"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

const (
	defaultPollInterval       = 15 * time.Second
	defaultClaimTTL           = 5 * time.Minute
	defaultBatchSize          = 100
	committedRunLookupTimeout = 3 * time.Second
)

type Starter interface {
	StartScheduledTask(context.Context, types.Task, taskapp.ScheduledStartCommand) (*orchestrator.StartTaskResult, error)
}

type ManagerOptions struct {
	Store        taskstate.ScheduleStore
	Tasks        taskstate.Store
	Starter      Starter
	Logger       *slog.Logger
	OwnerID      string
	IDGenerator  func(string) string
	Now          func() time.Time
	PollInterval time.Duration
	ClaimTTL     time.Duration
	BatchSize    int
}

// Manager durably claims schedule occurrences and turns each accepted claim
// into an ordinary Task Run. It is deliberately a trigger loop, not a second
// execution runtime: approval, sandbox, queue, retry, and resume semantics stay
// owned by the task application and orchestrator.
type Manager struct {
	store        taskstate.ScheduleStore
	tasks        taskstate.Store
	starter      Starter
	logger       *slog.Logger
	ownerID      string
	idgen        func(string) string
	now          func() time.Time
	pollInterval time.Duration
	claimTTL     time.Duration
	batchSize    int

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func NewManager(opts ManagerOptions) *Manager {
	m := &Manager{
		store:        opts.Store,
		tasks:        opts.Tasks,
		starter:      opts.Starter,
		logger:       opts.Logger,
		ownerID:      opts.OwnerID,
		idgen:        opts.IDGenerator,
		now:          opts.Now,
		pollInterval: opts.PollInterval,
		claimTTL:     opts.ClaimTTL,
		batchSize:    opts.BatchSize,
	}
	if m.idgen == nil {
		m.idgen = newResourceID
	}
	if m.ownerID == "" {
		m.ownerID = newResourceID("scheduler")
	}
	if m.now == nil {
		m.now = func() time.Time { return time.Now().UTC() }
	}
	if m.pollInterval <= 0 {
		m.pollInterval = defaultPollInterval
	}
	if m.claimTTL <= 0 {
		m.claimTTL = defaultClaimTTL
	}
	if m.batchSize <= 0 {
		m.batchSize = defaultBatchSize
	}
	return m
}

func (m *Manager) Start(parent context.Context) error {
	if err := m.validate(); err != nil {
		return err
	}
	if parent == nil {
		parent = context.Background()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.done != nil {
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	m.cancel = cancel
	m.done = make(chan struct{})
	go m.loop(ctx, m.done)
	return nil
}

func (m *Manager) Shutdown(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	cancel, done := m.cancel, m.done
	m.mu.Unlock()
	if cancel == nil || done == nil {
		return nil
	}
	cancel()
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) loop(ctx context.Context, done chan struct{}) {
	defer close(done)
	m.runAndLog(ctx)
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.runAndLog(ctx)
		}
	}
}

func (m *Manager) runAndLog(ctx context.Context) {
	if err := m.RunOnce(ctx, m.now().UTC()); err != nil && !errors.Is(err, context.Canceled) && m.logger != nil {
		m.logger.Error("task schedule dispatch failed", slog.Any("error", err))
	}
}

// RunOnce is the deterministic worker seam used by tests and startup recovery.
// Stale claims are recovered before new due schedules are admitted.
func (m *Manager) RunOnce(ctx context.Context, now time.Time) error {
	if err := m.validate(); err != nil {
		return err
	}
	now = now.UTC()
	var runErrors []error
	if err := m.recoverStaleClaims(ctx, now); err != nil {
		runErrors = append(runErrors, err)
	}
	due, err := m.store.ListDueTaskSchedules(ctx, now, m.batchSize)
	if err != nil {
		runErrors = append(runErrors, fmt.Errorf("list due task schedules: %w", err))
		return errors.Join(runErrors...)
	}
	for _, schedule := range due {
		claimNow := m.now().UTC()
		if claimNow.Before(now) {
			claimNow = now
		}
		if err := m.claimAndDispatch(ctx, schedule, claimNow); err != nil {
			runErrors = append(runErrors, fmt.Errorf("dispatch schedule %q: %w", schedule.ID, err))
		}
	}
	return errors.Join(runErrors...)
}

func (m *Manager) validate() error {
	if m == nil || m.store == nil {
		return ErrStoreNotConfigured
	}
	if m.tasks == nil {
		return ErrTaskStoreNotConfigured
	}
	if m.starter == nil {
		return taskapp.ErrRunnerNotConfigured
	}
	return nil
}

func (m *Manager) claimAndDispatch(ctx context.Context, schedule taskstate.TaskSchedule, now time.Time) error {
	nextRunAt, err := NextAfter(schedule, now)
	if err != nil {
		return err
	}
	occurrence, claimed, err := m.store.ClaimTaskScheduleOccurrence(ctx, taskstate.TaskScheduleOccurrenceClaim{
		OccurrenceID:             m.idgen("occurrence"),
		ScheduleID:               schedule.ID,
		ExpectedScheduleRevision: schedule.Revision,
		ScheduledFor:             schedule.NextRunAt,
		NextRunAt:                nextRunAt,
		ClaimOwner:               m.ownerID,
		ClaimedAt:                now,
	})
	if err != nil || !claimed {
		return err
	}
	return m.dispatchClaim(ctx, occurrence, now)
}

func (m *Manager) recoverStaleClaims(ctx context.Context, now time.Time) error {
	stale, err := m.store.ListTaskScheduleOccurrences(ctx, taskstate.TaskScheduleOccurrenceFilter{
		Status:        taskstate.TaskScheduleOccurrenceClaimed,
		ClaimedBefore: now.Add(-m.claimTTL),
		Limit:         m.batchSize,
	})
	if err != nil {
		return fmt.Errorf("list stale task schedule occurrences: %w", err)
	}
	var recoveryErrors []error
	for _, occurrence := range stale {
		reclaimNow := m.nowAtLeast(now)
		existingRun, found, err := m.findOccurrenceRun(ctx, occurrence)
		if err != nil {
			recoveryErrors = append(recoveryErrors, fmt.Errorf("find run for occurrence %q: %w", occurrence.ID, err))
			continue
		}
		reclaimed, applied, err := m.store.ReclaimTaskScheduleOccurrence(ctx, taskstate.TaskScheduleOccurrenceReclaim{
			ScheduleID:   occurrence.ScheduleID,
			ScheduledFor: occurrence.ScheduledFor,
			StaleBefore:  now.Add(-m.claimTTL),
			ClaimOwner:   m.ownerID,
			ClaimedAt:    reclaimNow,
		})
		if err != nil {
			recoveryErrors = append(recoveryErrors, fmt.Errorf("reclaim occurrence %q: %w", occurrence.ID, err))
			continue
		}
		if !applied {
			continue
		}
		if found {
			if err := m.completeClaim(ctx, reclaimed, taskstate.TaskScheduleOccurrenceStarted, existingRun.ID, "", m.nowAtLeast(reclaimNow)); err != nil {
				recoveryErrors = append(recoveryErrors, err)
			}
			continue
		}
		if err := m.dispatchClaim(ctx, reclaimed, reclaimNow); err != nil {
			recoveryErrors = append(recoveryErrors, fmt.Errorf("dispatch reclaimed occurrence %q: %w", occurrence.ID, err))
		}
	}
	return errors.Join(recoveryErrors...)
}

func (m *Manager) dispatchClaim(ctx context.Context, occurrence taskstate.TaskScheduleOccurrence, now time.Time) error {
	dispatchCtx, cancelDispatch := context.WithCancel(ctx)
	stopHeartbeat := make(chan struct{})
	heartbeatDone := make(chan error, 1)
	go func() {
		heartbeatDone <- m.heartbeatClaim(dispatchCtx, cancelDispatch, stopHeartbeat, occurrence)
	}()

	err := m.dispatchOwnedClaim(dispatchCtx, occurrence, now)
	close(stopHeartbeat)
	cancelDispatch()
	heartbeatErr := <-heartbeatDone
	if err == nil {
		return nil
	}
	return errors.Join(err, heartbeatErr)
}

func (m *Manager) dispatchOwnedClaim(ctx context.Context, occurrence taskstate.TaskScheduleOccurrence, now time.Time) error {
	task, found, err := m.tasks.GetTask(ctx, occurrence.TaskID)
	if err != nil {
		return err
	}
	if !found {
		return m.completeClaim(ctx, occurrence, taskstate.TaskScheduleOccurrenceFailed, "", ErrTaskNotFound.Error(), m.nowAtLeast(now))
	}
	result, err := m.starter.StartScheduledTask(ctx, task, taskapp.ScheduledStartCommand{
		ScheduleID:           occurrence.ScheduleID,
		ScheduleOccurrenceID: occurrence.ID,
		ScheduledFor:         occurrence.ScheduledFor,
		ClaimOwner:           occurrence.ClaimOwner,
	})
	if err == nil {
		return m.completeClaim(ctx, occurrence, taskstate.TaskScheduleOccurrenceStarted, result.Run.ID, "", m.nowAtLeast(now))
	}
	lookupCtx := ctx
	cancelLookup := func() {}
	if ctx.Err() != nil {
		lookupCtx, cancelLookup = context.WithTimeout(context.WithoutCancel(ctx), committedRunLookupTimeout)
	}
	defer cancelLookup()
	if run, found, lookupErr := m.findOccurrenceRun(lookupCtx, occurrence); lookupErr != nil {
		return lookupErr
	} else if found {
		return m.completeClaim(lookupCtx, occurrence, taskstate.TaskScheduleOccurrenceStarted, run.ID, "", m.nowAtLeast(now))
	}
	if ctx.Err() != nil {
		// Without a committed Run, leave the durable claim for recovery rather
		// than terminalizing an occurrence during scheduler shutdown.
		return ctx.Err()
	}
	if errors.Is(err, taskstate.ErrScheduleOccurrenceClaimLost) {
		return nil
	}
	if errors.Is(err, taskapp.ErrActiveRun) {
		return m.completeClaim(ctx, occurrence, taskstate.TaskScheduleOccurrenceSkipped, "", "task already has an active run", m.nowAtLeast(now))
	}
	return m.completeClaim(ctx, occurrence, taskstate.TaskScheduleOccurrenceFailed, "", err.Error(), m.nowAtLeast(now))
}

func (m *Manager) heartbeatClaim(ctx context.Context, cancelDispatch context.CancelFunc, stop <-chan struct{}, occurrence taskstate.TaskScheduleOccurrence) error {
	interval := m.claimTTL / 3
	if interval <= 0 {
		interval = time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-stop:
			return nil
		case <-ticker.C:
			renewed, applied, err := m.store.RenewTaskScheduleOccurrence(ctx, taskstate.TaskScheduleOccurrenceRenewal{
				OccurrenceID: occurrence.ID,
				ScheduleID:   occurrence.ScheduleID,
				ScheduledFor: occurrence.ScheduledFor,
				ClaimOwner:   occurrence.ClaimOwner,
				ClaimedAt:    m.nowAtLeast(occurrence.ClaimedAt),
			})
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				cancelDispatch()
				return fmt.Errorf("renew task schedule occurrence %q: %w", occurrence.ID, err)
			}
			if applied {
				occurrence = renewed
				continue
			}
			select {
			case <-stop:
				return nil
			default:
			}
			if ctx.Err() != nil {
				return nil
			}
			settled, err := m.claimIsSettled(ctx, occurrence)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				cancelDispatch()
				return fmt.Errorf("inspect task schedule occurrence %q after renewal loss: %w", occurrence.ID, err)
			}
			if settled {
				return nil
			}
			cancelDispatch()
			return taskstate.ErrScheduleOccurrenceClaimLost
		}
	}
}

func (m *Manager) claimIsSettled(ctx context.Context, occurrence taskstate.TaskScheduleOccurrence) (bool, error) {
	items, err := m.store.ListTaskScheduleOccurrences(ctx, taskstate.TaskScheduleOccurrenceFilter{
		ScheduleID: occurrence.ScheduleID,
	})
	if err != nil {
		return false, err
	}
	for _, current := range items {
		if current.ID != occurrence.ID || !current.ScheduledFor.Equal(occurrence.ScheduledFor) {
			continue
		}
		if current.Status == taskstate.TaskScheduleOccurrenceClaimed {
			return false, nil
		}
		// A terminal occurrence is only a clean heartbeat stop when this
		// worker settled its own claim. Another owner may have reclaimed and
		// completed the occurrence while dispatch was still provisioning a
		// workspace; in that case the displaced work must be cancelled early
		// instead of continuing until the final admission fence.
		return current.ClaimOwner == occurrence.ClaimOwner, nil
	}
	return false, nil
}

func (m *Manager) nowAtLeast(floor time.Time) time.Time {
	now := m.now().UTC()
	if now.Before(floor) {
		return floor
	}
	return now
}

func (m *Manager) findOccurrenceRun(ctx context.Context, occurrence taskstate.TaskScheduleOccurrence) (types.TaskRun, bool, error) {
	runs, err := m.tasks.ListRuns(ctx, occurrence.TaskID)
	if err != nil {
		return types.TaskRun{}, false, err
	}
	for _, run := range runs {
		if run.ScheduleOccurrenceID != occurrence.ID {
			continue
		}
		if run.ScheduleID != occurrence.ScheduleID || !run.ScheduledFor.Equal(occurrence.ScheduledFor) {
			return types.TaskRun{}, false, fmt.Errorf("run %q has conflicting provenance for occurrence %q", run.ID, occurrence.ID)
		}
		return run, true, nil
	}
	return types.TaskRun{}, false, nil
}

func (m *Manager) completeClaim(ctx context.Context, occurrence taskstate.TaskScheduleOccurrence, status, runID, message string, now time.Time) error {
	_, _, err := m.store.CompleteTaskScheduleOccurrence(ctx, taskstate.TaskScheduleOccurrenceCompletion{
		ScheduleID:   occurrence.ScheduleID,
		ScheduledFor: occurrence.ScheduledFor,
		ClaimOwner:   m.ownerID,
		Status:       status,
		RunID:        runID,
		Error:        message,
		CompletedAt:  now,
	})
	return err
}
