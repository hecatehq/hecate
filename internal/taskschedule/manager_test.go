package taskschedule

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/orchestrator"
	"github.com/hecatehq/hecate/internal/taskapp"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

type recordingStarter struct {
	calls    int
	commands []taskapp.ScheduledStartCommand
	err      error
}

type committingErrorStarter struct {
	store *taskstate.MemoryStore
	err   error
}

type blockingStarter struct {
	started chan struct{}
	release chan struct{}
}

type admittedBlockingStarter struct {
	store       taskstate.ScheduleStore
	admitted    chan struct{}
	release     chan struct{}
	completedAt func() time.Time
}

func (s *admittedBlockingStarter) StartScheduledTask(ctx context.Context, task types.Task, cmd taskapp.ScheduledStartCommand) (*orchestrator.StartTaskResult, error) {
	run := types.TaskRun{
		ID: "run_admitted_then_blocked", TaskID: task.ID,
		ScheduleID: cmd.ScheduleID, ScheduleOccurrenceID: cmd.ScheduleOccurrenceID,
		ScheduledFor: cmd.ScheduledFor,
	}
	if _, applied, err := s.store.CompleteTaskScheduleOccurrence(ctx, taskstate.TaskScheduleOccurrenceCompletion{
		ScheduleID: cmd.ScheduleID, ScheduledFor: cmd.ScheduledFor, ClaimOwner: cmd.ClaimOwner,
		Status: taskstate.TaskScheduleOccurrenceStarted, RunID: run.ID, CompletedAt: s.completedAt(),
	}); err != nil || !applied {
		return nil, fmt.Errorf("settle occurrence before blocking: applied=%v: %w", applied, err)
	}
	close(s.admitted)
	select {
	case <-s.release:
		return &orchestrator.StartTaskResult{Task: task, Run: run}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *blockingStarter) StartScheduledTask(ctx context.Context, task types.Task, cmd taskapp.ScheduledStartCommand) (*orchestrator.StartTaskResult, error) {
	select {
	case s.started <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case <-s.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &orchestrator.StartTaskResult{Task: task, Run: types.TaskRun{
		ID: "run_blocked_then_started", TaskID: task.ID,
		ScheduleID: cmd.ScheduleID, ScheduleOccurrenceID: cmd.ScheduleOccurrenceID,
		ScheduledFor: cmd.ScheduledFor,
	}}, nil
}

func (s *committingErrorStarter) StartScheduledTask(ctx context.Context, task types.Task, cmd taskapp.ScheduledStartCommand) (*orchestrator.StartTaskResult, error) {
	_, err := s.store.CreateRun(ctx, types.TaskRun{
		ID: "run_committed_before_error", TaskID: task.ID, Status: "queued",
		ScheduleID: cmd.ScheduleID, ScheduleOccurrenceID: cmd.ScheduleOccurrenceID,
		ScheduledFor: cmd.ScheduledFor,
	})
	if err != nil {
		return nil, err
	}
	return nil, s.err
}

func (s *recordingStarter) StartScheduledTask(_ context.Context, task types.Task, cmd taskapp.ScheduledStartCommand) (*orchestrator.StartTaskResult, error) {
	s.calls++
	s.commands = append(s.commands, cmd)
	if s.err != nil {
		return nil, s.err
	}
	return &orchestrator.StartTaskResult{
		Task: task,
		Run: types.TaskRun{
			ID:                   "run_started",
			TaskID:               task.ID,
			ScheduleID:           cmd.ScheduleID,
			ScheduleOccurrenceID: cmd.ScheduleOccurrenceID,
			ScheduledFor:         cmd.ScheduledFor,
		},
	}, nil
}

func TestManagerRunOnceClaimsDueCronAndCoalescesMissedFires(t *testing.T) {
	t.Parallel()

	store := taskstate.NewMemoryStore()
	task := types.Task{ID: "task_1", Status: types.TaskStatusNotStarted}
	if _, err := store.CreateTask(t.Context(), task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	scheduledFor := time.Date(2026, time.July, 20, 8, 5, 0, 0, time.UTC)
	mustCreateManagerSchedule(t, store, taskstate.TaskSchedule{
		ID:             "schedule_1",
		TaskID:         task.ID,
		Kind:           taskstate.TaskScheduleKindCron,
		CronExpression: "*/5 * * * *",
		Timezone:       "UTC",
		Enabled:        true,
		NextRunAt:      scheduledFor,
	})
	starter := &recordingStarter{}
	now := time.Date(2026, time.July, 20, 8, 17, 0, 0, time.UTC)
	manager := NewManager(ManagerOptions{
		Store: store, Tasks: store, Starter: starter, OwnerID: "worker_1",
		IDGenerator: func(string) string { return "occurrence_1" },
		Now:         func() time.Time { return now },
	})

	if err := manager.RunOnce(t.Context(), now); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if starter.calls != 1 || !starter.commands[0].ScheduledFor.Equal(scheduledFor) {
		t.Fatalf("starter calls/command = %d/%+v, want one original due occurrence", starter.calls, starter.commands)
	}
	if starter.commands[0].ClaimOwner != "worker_1" {
		t.Fatalf("starter claim owner = %q, want worker_1", starter.commands[0].ClaimOwner)
	}
	schedule, found, err := store.GetTaskSchedule(t.Context(), "schedule_1")
	if err != nil || !found {
		t.Fatalf("GetTaskSchedule() = found %v error %v", found, err)
	}
	wantNext := time.Date(2026, time.July, 20, 8, 20, 0, 0, time.UTC)
	if !schedule.NextRunAt.Equal(wantNext) {
		t.Fatalf("next_run_at = %v, want coalesced %v", schedule.NextRunAt, wantNext)
	}
	occurrences, err := store.ListTaskScheduleOccurrences(t.Context(), taskstate.TaskScheduleOccurrenceFilter{ScheduleID: schedule.ID})
	if err != nil || len(occurrences) != 1 {
		t.Fatalf("occurrences = %+v, error %v", occurrences, err)
	}
	if occurrences[0].Status != taskstate.TaskScheduleOccurrenceStarted || occurrences[0].RunID != "run_started" {
		t.Fatalf("occurrence = %+v, want started run", occurrences[0])
	}
}

func TestManagerRunOnceUsesFreshClaimTimeForEachBatchItem(t *testing.T) {
	t.Parallel()

	store := taskstate.NewMemoryStore()
	base := time.Date(2026, time.July, 20, 8, 0, 0, 0, time.UTC)
	for index := 1; index <= 2; index++ {
		taskID := fmt.Sprintf("task_batch_%d", index)
		if _, err := store.CreateTask(t.Context(), types.Task{ID: taskID, Status: types.TaskStatusNotStarted}); err != nil {
			t.Fatalf("CreateTask(%d): %v", index, err)
		}
		mustCreateManagerSchedule(t, store, taskstate.TaskSchedule{
			ID: fmt.Sprintf("schedule_batch_%d", index), TaskID: taskID,
			Kind: taskstate.TaskScheduleKindCron, CronExpression: "0 * * * *", Timezone: "UTC",
			Enabled: true, NextRunAt: base,
		})
	}
	var clockTick atomic.Int64
	var idCounter atomic.Int64
	manager := NewManager(ManagerOptions{
		Store: store, Tasks: store, Starter: &recordingStarter{}, OwnerID: "worker_batch",
		IDGenerator: func(string) string { return fmt.Sprintf("occurrence_batch_%d", idCounter.Add(1)) },
		Now: func() time.Time {
			return base.Add(time.Duration(clockTick.Add(1)) * time.Second)
		},
	})

	if err := manager.RunOnce(t.Context(), base); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	occurrences, err := store.ListTaskScheduleOccurrences(t.Context(), taskstate.TaskScheduleOccurrenceFilter{})
	if err != nil || len(occurrences) != 2 {
		t.Fatalf("occurrences = (%+v, %v), want two", occurrences, err)
	}
	claimedAt := make(map[string]time.Time, len(occurrences))
	for _, occurrence := range occurrences {
		claimedAt[occurrence.ID] = occurrence.ClaimedAt
	}
	if !claimedAt["occurrence_batch_2"].After(claimedAt["occurrence_batch_1"]) {
		t.Fatalf("batch claim times = %+v, want second item freshly timestamped", claimedAt)
	}
}

func TestManagerDispatchHeartbeatPreventsLiveClaimReclaim(t *testing.T) {
	t.Parallel()

	store := taskstate.NewMemoryStore()
	task := types.Task{ID: "task_heartbeat", Status: types.TaskStatusNotStarted}
	if _, err := store.CreateTask(t.Context(), task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	scheduledFor := time.Now().UTC().Add(-time.Minute)
	mustCreateManagerSchedule(t, store, taskstate.TaskSchedule{
		ID: "schedule_heartbeat", TaskID: task.ID, Kind: taskstate.TaskScheduleKindOnce,
		Timezone: "UTC", RunAt: scheduledFor, Enabled: true, NextRunAt: scheduledFor,
	})
	claimedAt := time.Now().UTC()
	occurrence, claimed, err := store.ClaimTaskScheduleOccurrence(t.Context(), taskstate.TaskScheduleOccurrenceClaim{
		OccurrenceID: "occurrence_heartbeat", ScheduleID: "schedule_heartbeat", ScheduledFor: scheduledFor,
		ExpectedScheduleRevision: 1, ClaimOwner: "live_worker", ClaimedAt: claimedAt,
	})
	if err != nil || !claimed {
		t.Fatalf("ClaimTaskScheduleOccurrence = (%+v, %v, %v)", occurrence, claimed, err)
	}
	starter := &blockingStarter{started: make(chan struct{}, 1), release: make(chan struct{})}
	const claimTTL = 120 * time.Millisecond
	manager := NewManager(ManagerOptions{
		Store: store, Tasks: store, Starter: starter, OwnerID: "live_worker",
		ClaimTTL: claimTTL, Now: func() time.Time { return time.Now().UTC() },
	})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	dispatchDone := make(chan error, 1)
	go func() { dispatchDone <- manager.dispatchClaim(ctx, occurrence, claimedAt) }()
	select {
	case <-starter.started:
	case <-ctx.Done():
		t.Fatalf("starter did not begin: %v", ctx.Err())
	}

	var current taskstate.TaskScheduleOccurrence
	for {
		items, listErr := store.ListTaskScheduleOccurrences(ctx, taskstate.TaskScheduleOccurrenceFilter{ScheduleID: occurrence.ScheduleID})
		if listErr != nil {
			t.Fatalf("ListTaskScheduleOccurrences: %v", listErr)
		}
		if len(items) == 1 {
			current = items[0]
		}
		cutoff := time.Now().UTC().Add(-claimTTL)
		if cutoff.After(claimedAt) && current.ClaimedAt.After(cutoff) {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("claim heartbeat did not advance: occurrence=%+v error=%v", current, ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	reclaimAt := time.Now().UTC()
	if _, applied, reclaimErr := store.ReclaimTaskScheduleOccurrence(ctx, taskstate.TaskScheduleOccurrenceReclaim{
		ScheduleID: occurrence.ScheduleID, ScheduledFor: occurrence.ScheduledFor,
		StaleBefore: reclaimAt.Add(-claimTTL), ClaimOwner: "recovery_worker", ClaimedAt: reclaimAt,
	}); reclaimErr != nil || applied {
		t.Fatalf("live heartbeat reclaim = (%v, %v), want fenced", applied, reclaimErr)
	}
	close(starter.release)
	select {
	case err := <-dispatchDone:
		if err != nil {
			t.Fatalf("dispatchClaim: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("dispatch did not finish: %v", ctx.Err())
	}
	items, err := store.ListTaskScheduleOccurrences(t.Context(), taskstate.TaskScheduleOccurrenceFilter{ScheduleID: occurrence.ScheduleID})
	if err != nil || len(items) != 1 || items[0].Status != taskstate.TaskScheduleOccurrenceStarted {
		t.Fatalf("settled occurrence = (%+v, %v), want started", items, err)
	}
}

func TestManagerDispatchHeartbeatStopsCleanlyAfterAdmissionSettlesClaim(t *testing.T) {
	t.Parallel()

	store := taskstate.NewMemoryStore()
	task := types.Task{ID: "task_heartbeat_settled", Status: types.TaskStatusNotStarted}
	if _, err := store.CreateTask(t.Context(), task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	scheduledFor := time.Now().UTC().Add(-time.Minute)
	mustCreateManagerSchedule(t, store, taskstate.TaskSchedule{
		ID: "schedule_heartbeat_settled", TaskID: task.ID, Kind: taskstate.TaskScheduleKindOnce,
		Timezone: "UTC", RunAt: scheduledFor, Enabled: true, NextRunAt: scheduledFor,
	})
	claimedAt := time.Now().UTC()
	occurrence, claimed, err := store.ClaimTaskScheduleOccurrence(t.Context(), taskstate.TaskScheduleOccurrenceClaim{
		OccurrenceID: "occurrence_heartbeat_settled", ScheduleID: "schedule_heartbeat_settled", ScheduledFor: scheduledFor,
		ExpectedScheduleRevision: 1, ClaimOwner: "settling_worker", ClaimedAt: claimedAt,
	})
	if err != nil || !claimed {
		t.Fatalf("ClaimTaskScheduleOccurrence = (%+v, %v, %v)", occurrence, claimed, err)
	}
	starter := &admittedBlockingStarter{
		store: store, admitted: make(chan struct{}), release: make(chan struct{}),
		completedAt: func() time.Time { return time.Now().UTC() },
	}
	const claimTTL = 90 * time.Millisecond
	manager := NewManager(ManagerOptions{
		Store: store, Tasks: store, Starter: starter, OwnerID: "settling_worker",
		ClaimTTL: claimTTL, Now: func() time.Time { return time.Now().UTC() },
	})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	dispatchDone := make(chan error, 1)
	go func() { dispatchDone <- manager.dispatchClaim(ctx, occurrence, claimedAt) }()
	select {
	case <-starter.admitted:
	case <-ctx.Done():
		t.Fatalf("starter did not settle occurrence: %v", ctx.Err())
	}
	select {
	case err := <-dispatchDone:
		t.Fatalf("dispatch ended while admitted starter was still blocked: %v", err)
	case <-time.After(claimTTL):
	}
	close(starter.release)
	select {
	case err := <-dispatchDone:
		if err != nil {
			t.Fatalf("dispatchClaim: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("dispatch did not finish: %v", ctx.Err())
	}
}

func TestManagerDispatchHeartbeatCancelsWhenAnotherOwnerSettlesClaim(t *testing.T) {
	t.Parallel()

	store := taskstate.NewMemoryStore()
	task := types.Task{ID: "task_heartbeat_displaced", Status: types.TaskStatusNotStarted}
	if _, err := store.CreateTask(t.Context(), task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	scheduledFor := time.Now().UTC().Add(-time.Minute)
	mustCreateManagerSchedule(t, store, taskstate.TaskSchedule{
		ID: "schedule_heartbeat_displaced", TaskID: task.ID, Kind: taskstate.TaskScheduleKindOnce,
		Timezone: "UTC", RunAt: scheduledFor, Enabled: true, NextRunAt: scheduledFor,
	})
	const claimTTL = 120 * time.Millisecond
	claimedAt := time.Now().UTC().Add(-2 * claimTTL)
	occurrence, claimed, err := store.ClaimTaskScheduleOccurrence(t.Context(), taskstate.TaskScheduleOccurrenceClaim{
		OccurrenceID: "occurrence_heartbeat_displaced", ScheduleID: "schedule_heartbeat_displaced", ScheduledFor: scheduledFor,
		ExpectedScheduleRevision: 1, ClaimOwner: "displaced_worker", ClaimedAt: claimedAt,
	})
	if err != nil || !claimed {
		t.Fatalf("ClaimTaskScheduleOccurrence = (%+v, %v, %v)", occurrence, claimed, err)
	}
	starter := &blockingStarter{started: make(chan struct{}, 1), release: make(chan struct{})}
	manager := NewManager(ManagerOptions{
		Store: store, Tasks: store, Starter: starter, OwnerID: "displaced_worker",
		ClaimTTL: claimTTL, Now: func() time.Time { return time.Now().UTC() },
	})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	dispatchDone := make(chan error, 1)
	go func() { dispatchDone <- manager.dispatchClaim(ctx, occurrence, claimedAt) }()
	select {
	case <-starter.started:
	case <-ctx.Done():
		t.Fatalf("starter did not begin: %v", ctx.Err())
	}

	reclaimedAt := time.Now().UTC()
	reclaimed, applied, err := store.ReclaimTaskScheduleOccurrence(ctx, taskstate.TaskScheduleOccurrenceReclaim{
		ScheduleID: occurrence.ScheduleID, ScheduledFor: occurrence.ScheduledFor,
		StaleBefore: reclaimedAt.Add(-claimTTL), ClaimOwner: "recovery_worker", ClaimedAt: reclaimedAt,
	})
	if err != nil || !applied {
		t.Fatalf("ReclaimTaskScheduleOccurrence = (%+v, %v, %v)", reclaimed, applied, err)
	}
	if _, applied, err := store.CompleteTaskScheduleOccurrence(ctx, taskstate.TaskScheduleOccurrenceCompletion{
		ScheduleID: occurrence.ScheduleID, ScheduledFor: occurrence.ScheduledFor,
		ClaimOwner: "recovery_worker", Status: taskstate.TaskScheduleOccurrenceSkipped,
		Error: "recovered elsewhere", CompletedAt: time.Now().UTC(),
	}); err != nil || !applied {
		t.Fatalf("CompleteTaskScheduleOccurrence = (%v, %v), want applied", applied, err)
	}

	select {
	case err := <-dispatchDone:
		if !errors.Is(err, taskstate.ErrScheduleOccurrenceClaimLost) {
			t.Fatalf("dispatchClaim error = %v, want ErrScheduleOccurrenceClaimLost", err)
		}
	case <-ctx.Done():
		t.Fatalf("displaced dispatch was not cancelled: %v", ctx.Err())
	}
}

func TestManagerStaleDefinitionSnapshotCannotClaimSameImmediateFire(t *testing.T) {
	t.Parallel()

	store := taskstate.NewMemoryStore()
	task := types.Task{ID: "task_stale_schedule", Status: types.TaskStatusNotStarted}
	if _, err := store.CreateTask(t.Context(), task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	scheduledFor := time.Date(2026, time.July, 20, 9, 0, 0, 0, time.UTC)
	stale := mustCreateManagerSchedule(t, store, taskstate.TaskSchedule{
		ID: "schedule_stale_definition", TaskID: task.ID, Kind: taskstate.TaskScheduleKindCron,
		CronExpression: "0 * * * *", Timezone: "UTC", Enabled: true, NextRunAt: scheduledFor,
	})
	fresh := stale
	fresh.CronExpression = "*/30 * * * *"
	updated, applied, err := store.CompareAndSwapTaskSchedule(t.Context(), taskstate.TaskScheduleCompareAndSwap{
		Schedule: fresh, ExpectedRevision: stale.Revision,
	})
	if err != nil || !applied || !updated.NextRunAt.Equal(stale.NextRunAt) {
		t.Fatalf("CompareAndSwapTaskSchedule(definition) = (%+v, %v, %v)", updated, applied, err)
	}
	starter := &recordingStarter{}
	manager := NewManager(ManagerOptions{
		Store: store, Tasks: store, Starter: starter, OwnerID: "worker_stale_schedule",
		IDGenerator: func(string) string { return "occurrence_stale_schedule" },
	})

	if err := manager.claimAndDispatch(t.Context(), stale, scheduledFor); err != nil {
		t.Fatalf("claimAndDispatch(stale) error = %v", err)
	}
	if starter.calls != 0 {
		t.Fatalf("starter calls = %d, want stale definition rejected before dispatch", starter.calls)
	}
	occurrences, err := store.ListTaskScheduleOccurrences(t.Context(), taskstate.TaskScheduleOccurrenceFilter{ScheduleID: stale.ID})
	if err != nil || len(occurrences) != 0 {
		t.Fatalf("occurrences = (%+v, %v), want none", occurrences, err)
	}
	current, found, err := store.GetTaskSchedule(t.Context(), stale.ID)
	if err != nil || !found || current.Revision != updated.Revision || current.CronExpression != fresh.CronExpression || !current.NextRunAt.Equal(scheduledFor) {
		t.Fatalf("current schedule = (%+v, %v, %v), want fresh definition unchanged", current, found, err)
	}
}

func TestManagerRunOnceSkipsOverlapWithoutQueueingFutureRun(t *testing.T) {
	t.Parallel()

	store := taskstate.NewMemoryStore()
	task := types.Task{ID: "task_1", Status: "running"}
	if _, err := store.CreateTask(t.Context(), task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	scheduledFor := time.Date(2026, time.July, 20, 9, 0, 0, 0, time.UTC)
	mustCreateManagerSchedule(t, store, taskstate.TaskSchedule{ID: "schedule_1", TaskID: task.ID, Kind: taskstate.TaskScheduleKindOnce, Timezone: "UTC", RunAt: scheduledFor, Enabled: true, NextRunAt: scheduledFor})
	manager := NewManager(ManagerOptions{
		Store: store, Tasks: store, Starter: &recordingStarter{err: taskapp.ErrActiveRun}, OwnerID: "worker_1",
		IDGenerator: func(string) string { return "occurrence_1" },
		Now:         func() time.Time { return scheduledFor },
	})
	if err := manager.RunOnce(t.Context(), scheduledFor); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	occurrences, err := store.ListTaskScheduleOccurrences(t.Context(), taskstate.TaskScheduleOccurrenceFilter{ScheduleID: "schedule_1"})
	if err != nil || len(occurrences) != 1 {
		t.Fatalf("occurrences = %+v, error %v", occurrences, err)
	}
	if occurrences[0].Status != taskstate.TaskScheduleOccurrenceSkipped {
		t.Fatalf("occurrence status = %q, want skipped", occurrences[0].Status)
	}
	schedule, _, _ := store.GetTaskSchedule(t.Context(), "schedule_1")
	if schedule.Enabled || !schedule.NextRunAt.IsZero() {
		t.Fatalf("once schedule after claim = %+v, want disabled with no future run", schedule)
	}
}

func TestManagerRunOnceRecoversClaimAfterRunCommit(t *testing.T) {
	t.Parallel()

	store := taskstate.NewMemoryStore()
	task := types.Task{ID: "task_1", Status: types.TaskStatusNotStarted}
	if _, err := store.CreateTask(t.Context(), task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	scheduledFor := time.Date(2026, time.July, 20, 9, 0, 0, 0, time.UTC)
	mustCreateManagerSchedule(t, store, taskstate.TaskSchedule{ID: "schedule_1", TaskID: task.ID, Kind: taskstate.TaskScheduleKindCron, CronExpression: "0 * * * *", Timezone: "UTC", Enabled: true, NextRunAt: scheduledFor})
	claimedAt := scheduledFor.Add(time.Minute)
	occurrence, claimed, err := store.ClaimTaskScheduleOccurrence(t.Context(), taskstate.TaskScheduleOccurrenceClaim{
		OccurrenceID: "occurrence_1", ScheduleID: "schedule_1", ScheduledFor: scheduledFor,
		ExpectedScheduleRevision: 1,
		NextRunAt:                scheduledFor.Add(time.Hour), ClaimOwner: "dead_worker", ClaimedAt: claimedAt,
	})
	if err != nil || !claimed {
		t.Fatalf("ClaimTaskScheduleOccurrence() = claimed %v error %v", claimed, err)
	}
	if _, err := store.CreateRun(t.Context(), types.TaskRun{
		ID: "run_committed", TaskID: task.ID, ScheduleID: occurrence.ScheduleID,
		ScheduleOccurrenceID: occurrence.ID, ScheduledFor: occurrence.ScheduledFor,
	}); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	starter := &recordingStarter{}
	recoveryNow := claimedAt.Add(10 * time.Minute)
	manager := NewManager(ManagerOptions{
		Store: store, Tasks: store, Starter: starter, OwnerID: "worker_2", ClaimTTL: 5 * time.Minute,
		Now: func() time.Time { return recoveryNow },
	})

	if err := manager.RunOnce(t.Context(), recoveryNow); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if starter.calls != 0 {
		t.Fatalf("starter calls = %d, want recovery to reuse committed run", starter.calls)
	}
	occurrences, err := store.ListTaskScheduleOccurrences(t.Context(), taskstate.TaskScheduleOccurrenceFilter{ScheduleID: "schedule_1"})
	if err != nil || len(occurrences) != 1 {
		t.Fatalf("occurrences = %+v, error %v", occurrences, err)
	}
	if occurrences[0].Status != taskstate.TaskScheduleOccurrenceStarted || occurrences[0].RunID != "run_committed" {
		t.Fatalf("recovered occurrence = %+v, want started committed run", occurrences[0])
	}
}

func TestManagerRunOnceSettlesStartedWhenStarterFailsAfterRunCommit(t *testing.T) {
	t.Parallel()

	store := taskstate.NewMemoryStore()
	task := types.Task{ID: "task_post_commit_error", Status: types.TaskStatusNotStarted}
	if _, err := store.CreateTask(t.Context(), task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	scheduledFor := time.Date(2026, time.July, 20, 11, 0, 0, 0, time.UTC)
	mustCreateManagerSchedule(t, store, taskstate.TaskSchedule{
		ID: "schedule_post_commit_error", TaskID: task.ID, Kind: taskstate.TaskScheduleKindOnce,
		Timezone: "UTC", RunAt: scheduledFor, Enabled: true, NextRunAt: scheduledFor,
	})
	postCommitErr := errors.New("post-commit dispatch failed")
	manager := NewManager(ManagerOptions{
		Store: store, Tasks: store,
		Starter: &committingErrorStarter{store: store, err: postCommitErr}, OwnerID: "worker_post_commit",
		IDGenerator: func(string) string { return "occurrence_post_commit_error" },
		Now:         func() time.Time { return scheduledFor },
	})

	if err := manager.RunOnce(t.Context(), scheduledFor); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	occurrences, err := store.ListTaskScheduleOccurrences(t.Context(), taskstate.TaskScheduleOccurrenceFilter{ScheduleID: "schedule_post_commit_error"})
	if err != nil || len(occurrences) != 1 {
		t.Fatalf("occurrences = %+v, error %v", occurrences, err)
	}
	if occurrences[0].Status != taskstate.TaskScheduleOccurrenceStarted || occurrences[0].RunID != "run_committed_before_error" {
		t.Fatalf("occurrence = %+v, want started committed run", occurrences[0])
	}
}

func mustCreateManagerSchedule(t *testing.T, store taskstate.ScheduleStore, schedule taskstate.TaskSchedule) taskstate.TaskSchedule {
	t.Helper()
	stored, applied, err := store.CompareAndSwapTaskSchedule(t.Context(), taskstate.TaskScheduleCompareAndSwap{Schedule: schedule})
	if err != nil || !applied {
		t.Fatalf("CompareAndSwapTaskSchedule(create) = (%+v, %v, %v)", stored, applied, err)
	}
	return stored
}
