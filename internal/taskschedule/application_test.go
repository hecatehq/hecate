package taskschedule

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/apperrors"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

type claimBeforeFirstScheduleCASStore struct {
	*taskstate.MemoryStore
	once      sync.Once
	claimAt   time.Time
	claimErr  error
	claimMade bool
}

func (s *claimBeforeFirstScheduleCASStore) CompareAndSwapTaskSchedule(ctx context.Context, mutation taskstate.TaskScheduleCompareAndSwap) (taskstate.TaskSchedule, bool, error) {
	s.once.Do(func() {
		current, found, err := s.MemoryStore.GetTaskScheduleByTask(ctx, mutation.Schedule.TaskID)
		if err != nil {
			s.claimErr = err
			return
		}
		if !found {
			s.claimErr = errors.New("schedule missing before injected claim")
			return
		}
		_, s.claimMade, s.claimErr = s.MemoryStore.ClaimTaskScheduleOccurrence(ctx, taskstate.TaskScheduleOccurrenceClaim{
			OccurrenceID: "occurrence_injected_claim", ScheduleID: current.ID,
			ExpectedScheduleRevision: current.Revision, ScheduledFor: current.NextRunAt,
			ClaimOwner: "worker_injected_claim", ClaimedAt: s.claimAt,
		})
		if s.claimErr == nil && !s.claimMade {
			s.claimErr = errors.New("injected schedule claim was not applied")
		}
	})
	if s.claimErr != nil {
		return taskstate.TaskSchedule{}, false, s.claimErr
	}
	return s.MemoryStore.CompareAndSwapTaskSchedule(ctx, mutation)
}

type alwaysConflictingScheduleCASStore struct {
	*taskstate.MemoryStore
	mu    sync.Mutex
	calls int
}

func (s *alwaysConflictingScheduleCASStore) CompareAndSwapTaskSchedule(ctx context.Context, mutation taskstate.TaskScheduleCompareAndSwap) (taskstate.TaskSchedule, bool, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	current, _, err := s.MemoryStore.GetTaskScheduleByTask(ctx, mutation.Schedule.TaskID)
	return current, false, err
}

type deleteTaskBeforeScheduleCASStore struct {
	*taskstate.MemoryStore
	once      sync.Once
	writeErr  error
	deleteErr error
}

func (s *deleteTaskBeforeScheduleCASStore) CompareAndSwapTaskSchedule(ctx context.Context, mutation taskstate.TaskScheduleCompareAndSwap) (taskstate.TaskSchedule, bool, error) {
	s.once.Do(func() {
		s.deleteErr = s.MemoryStore.DeleteTask(ctx, mutation.Schedule.TaskID)
	})
	if s.deleteErr != nil {
		return taskstate.TaskSchedule{}, false, s.deleteErr
	}
	return taskstate.TaskSchedule{}, false, s.writeErr
}

type failingScheduleCASStore struct {
	*taskstate.MemoryStore
	writeErr error
}

func (s *failingScheduleCASStore) CompareAndSwapTaskSchedule(context.Context, taskstate.TaskScheduleCompareAndSwap) (taskstate.TaskSchedule, bool, error) {
	return taskstate.TaskSchedule{}, false, s.writeErr
}

func TestApplicationUpsertKeepsOneStableSchedulePerTask(t *testing.T) {
	t.Parallel()

	store := taskstate.NewMemoryStore()
	if _, err := store.CreateTask(t.Context(), types.Task{ID: "task_1", Status: types.TaskStatusNotStarted}); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	now := time.Date(2026, time.July, 20, 8, 0, 0, 0, time.UTC)
	app := New(Options{
		Store:       store,
		Tasks:       store,
		IDGenerator: func(prefix string) string { return prefix + "_1" },
		Now:         func() time.Time { return now },
	})

	created, err := app.Upsert(t.Context(), UpsertCommand{
		TaskID:   "task_1",
		Kind:     taskstate.TaskScheduleKindOnce,
		Timezone: "Europe/Madrid",
		RunAt:    now.Add(time.Hour),
		Enabled:  true,
	})
	if err != nil {
		t.Fatalf("Upsert(create) error = %v", err)
	}
	updated, err := app.Upsert(t.Context(), UpsertCommand{
		TaskID:         "task_1",
		Kind:           taskstate.TaskScheduleKindCron,
		CronExpression: "0 9 * * 1-5",
		Timezone:       "Europe/Madrid",
		Enabled:        false,
	})
	if err != nil {
		t.Fatalf("Upsert(update) error = %v", err)
	}
	if updated.ID != created.ID || updated.TaskID != created.TaskID {
		t.Fatalf("updated identity = %q/%q, want %q/%q", updated.ID, updated.TaskID, created.ID, created.TaskID)
	}
	if updated.Enabled || !updated.NextRunAt.IsZero() || updated.Kind != taskstate.TaskScheduleKindCron {
		t.Fatalf("updated schedule = %+v, want disabled cron with no next run", updated)
	}
	reenabled, err := app.Upsert(t.Context(), UpsertCommand{
		TaskID:         "task_1",
		Kind:           taskstate.TaskScheduleKindCron,
		CronExpression: "0 9 * * 1-5",
		Timezone:       "Europe/Madrid",
		Enabled:        true,
	})
	if err != nil {
		t.Fatalf("Upsert(reenable) error = %v", err)
	}
	if reenabled.ID != created.ID || !reenabled.Enabled || reenabled.NextRunAt.IsZero() {
		t.Fatalf("reenabled schedule = %+v, want stable enabled schedule with next run", reenabled)
	}
}

func TestApplicationUpsertRejectsUnknownTaskAndInvalidSpec(t *testing.T) {
	t.Parallel()

	store := taskstate.NewMemoryStore()
	now := time.Date(2026, time.July, 20, 8, 0, 0, 0, time.UTC)
	app := New(Options{Store: store, Tasks: store, Now: func() time.Time { return now }})

	if _, err := app.Upsert(t.Context(), UpsertCommand{TaskID: "missing", Kind: taskstate.TaskScheduleKindCron, CronExpression: "0 9 * * *", Timezone: "UTC", Enabled: true}); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("unknown task error = %v, want ErrTaskNotFound", err)
	}
	if _, err := store.CreateTask(t.Context(), types.Task{ID: "task_1"}); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if _, err := app.Upsert(t.Context(), UpsertCommand{TaskID: "task_1", Kind: taskstate.TaskScheduleKindCron, CronExpression: "every morning", Timezone: "UTC", Enabled: true}); !errors.Is(err, ErrCronExpressionInvalid) || !apperrors.IsValidationError(err) {
		t.Fatalf("invalid cron error = %v, want validation ErrCronExpressionInvalid", err)
	}
}

func TestApplicationUpsertRejectsChatOwnedTask(t *testing.T) {
	t.Parallel()

	store := taskstate.NewMemoryStore()
	if _, err := store.CreateTask(t.Context(), types.Task{
		ID: "task_chat_owned", OriginKind: "chat", OriginID: "chat_1", Status: types.TaskStatusNotStarted,
	}); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	now := time.Date(2026, time.July, 20, 8, 0, 0, 0, time.UTC)
	app := New(Options{Store: store, Tasks: store, Now: func() time.Time { return now }})

	for _, enabled := range []bool{false, true} {
		_, err := app.Upsert(t.Context(), UpsertCommand{
			TaskID: "task_chat_owned", Kind: taskstate.TaskScheduleKindCron,
			CronExpression: "0 9 * * *", Timezone: "UTC", Enabled: enabled,
		})
		if !errors.Is(err, ErrChatOwnedTaskCannotBeScheduled) || !apperrors.IsValidationError(err) {
			t.Fatalf("enabled=%v Upsert() error = %v, want validation ErrChatOwnedTaskCannotBeScheduled", enabled, err)
		}
		if got := err.Error(); got != "chat-owned tasks cannot be scheduled" {
			t.Fatalf("enabled=%v Upsert() error message = %q, want stable chat-owned Task guidance", enabled, got)
		}
	}
	if _, found, err := store.GetTaskScheduleByTask(t.Context(), "task_chat_owned"); err != nil || found {
		t.Fatalf("GetTaskScheduleByTask() = found %v error %v, want no persisted Schedule", found, err)
	}
}

func TestApplicationUpsertMapsConcurrentTaskDeletionToNotFound(t *testing.T) {
	t.Parallel()

	underlying := taskstate.NewMemoryStore()
	if _, err := underlying.CreateTask(t.Context(), types.Task{
		ID: "task_deleted_during_schedule_save", Status: types.TaskStatusNotStarted,
	}); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	writeErr := errors.New("foreign key constraint failed")
	store := &deleteTaskBeforeScheduleCASStore{MemoryStore: underlying, writeErr: writeErr}
	app := New(Options{
		Store: store,
		Tasks: store,
		Now: func() time.Time {
			return time.Date(2026, time.July, 20, 8, 0, 0, 0, time.UTC)
		},
	})

	_, err := app.Upsert(t.Context(), UpsertCommand{
		TaskID: "task_deleted_during_schedule_save", Kind: taskstate.TaskScheduleKindCron,
		CronExpression: "0 9 * * *", Timezone: "UTC", Enabled: true,
	})
	if !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("Upsert() error = %v, want ErrTaskNotFound", err)
	}
	if errors.Is(err, writeErr) {
		t.Fatalf("Upsert() exposed storage write error %v after Task deletion", writeErr)
	}
}

func TestApplicationUpsertPreservesWriteErrorWhenTaskStillExists(t *testing.T) {
	t.Parallel()

	underlying := taskstate.NewMemoryStore()
	if _, err := underlying.CreateTask(t.Context(), types.Task{
		ID: "task_existing_after_schedule_error", Status: types.TaskStatusNotStarted,
	}); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	writeErr := errors.New("schedule storage unavailable")
	store := &failingScheduleCASStore{MemoryStore: underlying, writeErr: writeErr}
	app := New(Options{
		Store: store,
		Tasks: store,
		Now: func() time.Time {
			return time.Date(2026, time.July, 20, 8, 0, 0, 0, time.UTC)
		},
	})

	_, err := app.Upsert(t.Context(), UpsertCommand{
		TaskID: "task_existing_after_schedule_error", Kind: taskstate.TaskScheduleKindCron,
		CronExpression: "0 9 * * *", Timezone: "UTC", Enabled: true,
	})
	if !errors.Is(err, writeErr) {
		t.Fatalf("Upsert() error = %v, want original write error", err)
	}
}

func TestApplicationUpsertElapsedOnceAfterClaimReturnsConflictWithoutResurrection(t *testing.T) {
	t.Parallel()

	underlying := taskstate.NewMemoryStore()
	if _, err := underlying.CreateTask(t.Context(), types.Task{ID: "task_once_elapsed", Status: types.TaskStatusNotStarted}); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	runAt := time.Date(2026, time.July, 20, 10, 0, 0, 0, time.UTC)
	mustCreateApplicationSchedule(t, underlying, taskstate.TaskSchedule{
		ID: "schedule_once_elapsed", TaskID: "task_once_elapsed", Kind: taskstate.TaskScheduleKindOnce,
		Timezone: "UTC", RunAt: runAt, Enabled: true, NextRunAt: runAt,
	})
	store := &claimBeforeFirstScheduleCASStore{MemoryStore: underlying, claimAt: runAt}
	nowCalls := 0
	app := New(Options{
		Store: store, Tasks: store,
		Now: func() time.Time {
			nowCalls++
			if nowCalls == 1 {
				return runAt.Add(-time.Second)
			}
			return runAt.Add(time.Second)
		},
	})

	_, err := app.Upsert(t.Context(), UpsertCommand{
		TaskID: "task_once_elapsed", Kind: taskstate.TaskScheduleKindOnce,
		Timezone: "UTC", RunAt: runAt, Enabled: true,
	})
	if !errors.Is(err, ErrOnceScheduleElapsed) || !apperrors.IsConflictError(err) || apperrors.IsValidationError(err) {
		t.Fatalf("Upsert() error = %v, want conflict ErrOnceScheduleElapsed", err)
	}
	if got := err.Error(); got != "once schedule fired or changed while updating; choose a future run_at" {
		t.Fatalf("Upsert() error message = %q, want actionable elapsed-once guidance", got)
	}
	current, found, getErr := underlying.GetTaskScheduleByTask(t.Context(), "task_once_elapsed")
	if getErr != nil || !found || current.Enabled || !current.NextRunAt.IsZero() || current.Revision != 2 {
		t.Fatalf("schedule after lost PUT = (%+v, %v, %v), want claimed once disabled at revision 2", current, found, getErr)
	}
	occurrences, listErr := underlying.ListTaskScheduleOccurrences(t.Context(), taskstate.TaskScheduleOccurrenceFilter{ScheduleID: current.ID})
	if listErr != nil || len(occurrences) != 1 || !occurrences[0].ScheduledFor.Equal(runAt) {
		t.Fatalf("occurrences after lost PUT = (%+v, %v), want one consumed fire", occurrences, listErr)
	}
}

func TestApplicationUpsertFutureOnceReplacementSucceedsAfterClaim(t *testing.T) {
	t.Parallel()

	underlying := taskstate.NewMemoryStore()
	if _, err := underlying.CreateTask(t.Context(), types.Task{ID: "task_once_replacement", Status: types.TaskStatusNotStarted}); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	consumedRunAt := time.Date(2026, time.July, 20, 10, 0, 0, 0, time.UTC)
	replacementRunAt := consumedRunAt.Add(2 * time.Hour)
	mustCreateApplicationSchedule(t, underlying, taskstate.TaskSchedule{
		ID: "schedule_once_replacement", TaskID: "task_once_replacement", Kind: taskstate.TaskScheduleKindOnce,
		Timezone: "UTC", RunAt: consumedRunAt, Enabled: true, NextRunAt: consumedRunAt,
	})
	store := &claimBeforeFirstScheduleCASStore{MemoryStore: underlying, claimAt: consumedRunAt}
	nowCalls := 0
	app := New(Options{
		Store: store, Tasks: store,
		Now: func() time.Time {
			nowCalls++
			if nowCalls == 1 {
				return consumedRunAt.Add(-time.Second)
			}
			return consumedRunAt.Add(time.Second)
		},
	})

	updated, err := app.Upsert(t.Context(), UpsertCommand{
		TaskID: "task_once_replacement", Kind: taskstate.TaskScheduleKindOnce,
		Timezone: "UTC", RunAt: replacementRunAt, Enabled: true,
	})
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if updated.Revision != 3 || !updated.Enabled || !updated.RunAt.Equal(replacementRunAt) || !updated.NextRunAt.Equal(replacementRunAt) {
		t.Fatalf("replacement schedule = %+v, want future once at revision 3", updated)
	}
	occurrences, listErr := underlying.ListTaskScheduleOccurrences(t.Context(), taskstate.TaskScheduleOccurrenceFilter{ScheduleID: updated.ID})
	if listErr != nil || len(occurrences) != 1 || !occurrences[0].ScheduledFor.Equal(consumedRunAt) {
		t.Fatalf("occurrences after replacement = (%+v, %v), want consumed prior fire preserved", occurrences, listErr)
	}
}

func TestApplicationUpsertExhaustedCASReturnsConflict(t *testing.T) {
	t.Parallel()

	underlying := taskstate.NewMemoryStore()
	if _, err := underlying.CreateTask(t.Context(), types.Task{ID: "task_conflict", Status: types.TaskStatusNotStarted}); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	base := time.Date(2026, time.July, 20, 10, 0, 0, 0, time.UTC)
	mustCreateApplicationSchedule(t, underlying, taskstate.TaskSchedule{
		ID: "schedule_conflict", TaskID: "task_conflict", Kind: taskstate.TaskScheduleKindCron,
		CronExpression: "0 * * * *", Timezone: "UTC", Enabled: true, NextRunAt: base.Add(time.Hour),
	})
	store := &alwaysConflictingScheduleCASStore{MemoryStore: underlying}
	app := New(Options{Store: store, Tasks: store, Now: func() time.Time { return base }})

	_, err := app.Upsert(t.Context(), UpsertCommand{
		TaskID: "task_conflict", Kind: taskstate.TaskScheduleKindCron,
		CronExpression: "*/30 * * * *", Timezone: "UTC", Enabled: true,
	})
	if !errors.Is(err, ErrScheduleUpdateConflict) || !apperrors.IsConflictError(err) {
		t.Fatalf("Upsert() error = %v, want conflict ErrScheduleUpdateConflict", err)
	}
	if got := err.Error(); got != "task schedule changed while updating; reload and try again" {
		t.Fatalf("Upsert() error message = %q, want actionable retry guidance", got)
	}
	store.mu.Lock()
	calls := store.calls
	store.mu.Unlock()
	if calls != maxScheduleUpdateAttempts {
		t.Fatalf("CAS attempts = %d, want %d", calls, maxScheduleUpdateAttempts)
	}
}

func mustCreateApplicationSchedule(t *testing.T, store taskstate.ScheduleStore, schedule taskstate.TaskSchedule) taskstate.TaskSchedule {
	t.Helper()
	stored, applied, err := store.CompareAndSwapTaskSchedule(t.Context(), taskstate.TaskScheduleCompareAndSwap{Schedule: schedule})
	if err != nil || !applied {
		t.Fatalf("CompareAndSwapTaskSchedule(create) = (%+v, %v, %v)", stored, applied, err)
	}
	return stored
}
