package taskstate

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/hecatehq/hecate/pkg/types"
)

type scheduleConformanceStore interface {
	Store
	ScheduleStore
	ScheduledRunStore
}

type ScheduleStoreFactory func(t *testing.T) scheduleConformanceStore

func RunScheduleStoreConformanceTests(t *testing.T, name string, factory ScheduleStoreFactory) {
	t.Helper()
	t.Run(name+"/CRUDAndTaskUpsert", func(t *testing.T) {
		t.Parallel()
		runScheduleStoreCRUDAndTaskUpsert(t, factory(t))
	})
	t.Run(name+"/DueOrdering", func(t *testing.T) {
		t.Parallel()
		runScheduleStoreDueOrdering(t, factory(t))
	})
	t.Run(name+"/ExactSecondAndFractionalDueOrdering", func(t *testing.T) {
		t.Parallel()
		runScheduleStoreExactSecondAndFractionalDueOrdering(t, factory(t))
	})
	t.Run(name+"/BatchedTaskFilter", func(t *testing.T) {
		t.Parallel()
		runScheduleStoreBatchedTaskFilter(t, factory(t))
	})
	t.Run(name+"/ConcurrentClaimAndDisable", func(t *testing.T) {
		t.Parallel()
		runScheduleStoreConcurrentClaimAndDisable(t, factory(t))
	})
	t.Run(name+"/ClaimWinsStaleScheduleCAS", func(t *testing.T) {
		t.Parallel()
		runScheduleStoreClaimWinsStaleScheduleCAS(t, factory(t))
	})
	t.Run(name+"/DefinitionCASWinsStaleClaim", func(t *testing.T) {
		t.Parallel()
		runScheduleStoreDefinitionCASWinsStaleClaim(t, factory(t))
	})
	t.Run(name+"/StaleReclaimCompletionAndHistory", func(t *testing.T) {
		t.Parallel()
		runScheduleStoreStaleReclaimCompletionAndHistory(t, factory(t))
	})
	t.Run(name+"/ClaimRenewalFencesStaleRecovery", func(t *testing.T) {
		t.Parallel()
		runScheduleStoreClaimRenewalFencesStaleRecovery(t, factory(t))
	})
	t.Run(name+"/TaskDeleteCascades", func(t *testing.T) {
		t.Parallel()
		runScheduleStoreTaskDeleteCascades(t, factory(t))
	})
	t.Run(name+"/TaskDeleteConcurrentWithScheduledRunAdmission", func(t *testing.T) {
		t.Parallel()
		runScheduleStoreTaskDeleteConcurrentWithScheduledRunAdmission(t, factory(t))
	})
	t.Run(name+"/RunAdmissionReusesTerminalRun", func(t *testing.T) {
		t.Parallel()
		runScheduleStoreRunAdmissionReusesTerminalRun(t, factory(t))
	})
	t.Run(name+"/RunAdmissionConcurrentOwners", func(t *testing.T) {
		t.Parallel()
		runScheduleStoreRunAdmissionConcurrentOwners(t, factory(t))
	})
	t.Run(name+"/RunAdmissionPersistsInitialApprovalEffects", func(t *testing.T) {
		t.Parallel()
		runScheduleStoreRunAdmissionPersistsInitialApprovalEffects(t, factory(t))
	})
	t.Run(name+"/RunAdmissionApprovalFailureRollsBack", func(t *testing.T) {
		t.Parallel()
		runScheduleStoreRunAdmissionApprovalFailureRollsBack(t, factory(t))
	})
	t.Run(name+"/RunAdmissionOverlapIsCrashSafe", func(t *testing.T) {
		t.Parallel()
		runScheduleStoreRunAdmissionOverlapIsCrashSafe(t, factory(t))
	})
	t.Run(name+"/RunAdmissionRechecksAfterReady", func(t *testing.T) {
		t.Parallel()
		runScheduleStoreRunAdmissionRechecksAfterReady(t, factory(t))
	})
}

func runScheduleStoreCRUDAndTaskUpsert(t *testing.T, store scheduleConformanceStore) {
	t.Helper()
	ctx := context.Background()
	base := scheduleTestTime()
	mustCreateScheduleTask(t, store, "schedule-task-crud")

	created, applied, err := store.CompareAndSwapTaskSchedule(ctx, TaskScheduleCompareAndSwap{Schedule: TaskSchedule{
		ID:             "schedule-crud-original",
		TaskID:         "schedule-task-crud",
		Kind:           TaskScheduleKindCron,
		CronExpression: "0 9 * * 1-5",
		Timezone:       "Europe/Madrid",
		Enabled:        true,
		NextRunAt:      base,
		CreatedAt:      base.Add(-time.Hour),
		UpdatedAt:      base.Add(-time.Hour),
	}})
	if err != nil || !applied {
		t.Fatalf("CompareAndSwapTaskSchedule(create) = (%+v, %v, %v)", created, applied, err)
	}
	if created.ID != "schedule-crud-original" || created.TaskID != "schedule-task-crud" || !created.NextRunAt.Equal(base) {
		t.Fatalf("created schedule = %+v", created)
	}
	if created.Revision != 1 {
		t.Fatalf("created schedule revision = %d, want 1", created.Revision)
	}

	byID, ok, err := store.GetTaskSchedule(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("GetTaskSchedule = (%+v, %v, %v)", byID, ok, err)
	}
	byTask, ok, err := store.GetTaskScheduleByTask(ctx, created.TaskID)
	if err != nil || !ok || byTask.ID != created.ID {
		t.Fatalf("GetTaskScheduleByTask = (%+v, %v, %v)", byTask, ok, err)
	}

	updated, applied, err := store.CompareAndSwapTaskSchedule(ctx, TaskScheduleCompareAndSwap{Schedule: TaskSchedule{
		ID:        "schedule-crud-replacement-id",
		TaskID:    created.TaskID,
		Kind:      TaskScheduleKindOnce,
		Timezone:  "UTC",
		RunAt:     base.Add(2 * time.Hour),
		Enabled:   true,
		NextRunAt: base.Add(2 * time.Hour),
		CreatedAt: base,
		UpdatedAt: base.Add(time.Minute),
	}, ExpectedRevision: created.Revision})
	if err != nil || !applied {
		t.Fatalf("CompareAndSwapTaskSchedule(update by task) = (%+v, %v, %v)", updated, applied, err)
	}
	if updated.ID != created.ID {
		t.Fatalf("updated schedule ID = %q, want preserved %q", updated.ID, created.ID)
	}
	if updated.Kind != TaskScheduleKindOnce || updated.CronExpression != "" || !updated.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("updated schedule = %+v", updated)
	}
	if updated.Revision != created.Revision+1 {
		t.Fatalf("updated schedule revision = %d, want %d", updated.Revision, created.Revision+1)
	}
	if _, ok, err := store.GetTaskSchedule(ctx, "schedule-crud-replacement-id"); err != nil || ok {
		t.Fatalf("replacement ID lookup = (%v, %v), want absent", ok, err)
	}

	enabled := true
	items, err := store.ListTaskSchedules(ctx, TaskScheduleFilter{TaskIDs: []string{created.TaskID}, Enabled: &enabled, Limit: 1})
	if err != nil || len(items) != 1 || items[0].ID != created.ID {
		t.Fatalf("ListTaskSchedules = (%+v, %v)", items, err)
	}
	if err := store.DeleteTaskSchedule(ctx, created.ID); err != nil {
		t.Fatalf("DeleteTaskSchedule: %v", err)
	}
	if _, ok, err := store.GetTaskSchedule(ctx, created.ID); err != nil || ok {
		t.Fatalf("schedule after delete = (%v, %v), want absent", ok, err)
	}
}

func runScheduleStoreExactSecondAndFractionalDueOrdering(t *testing.T, store scheduleConformanceStore) {
	t.Helper()
	ctx := context.Background()
	base := scheduleTestTime()
	testCases := []struct {
		id      string
		nextRun time.Time
	}{
		{id: "schedule-exact-second", nextRun: base},
		{id: "schedule-fractional", nextRun: base.Add(500 * time.Millisecond)},
		{id: "schedule-after-cutoff", nextRun: base.Add(900 * time.Millisecond)},
	}
	for _, testCase := range testCases {
		taskID := "task-" + testCase.id
		mustCreateScheduleTask(t, store, taskID)
		mustCreateTaskSchedule(t, store, TaskSchedule{
			ID: testCase.id, TaskID: taskID, Kind: TaskScheduleKindCron,
			CronExpression: "* * * * *", Timezone: "UTC", Enabled: true,
			NextRunAt: testCase.nextRun, CreatedAt: base, UpdatedAt: base,
		})
	}

	due, err := store.ListDueTaskSchedules(ctx, base.Add(750*time.Millisecond), 10)
	if err != nil {
		t.Fatalf("ListDueTaskSchedules: %v", err)
	}
	if len(due) != 2 || due[0].ID != "schedule-exact-second" || due[1].ID != "schedule-fractional" {
		t.Fatalf("due schedules = %+v, want exact-second then fractional", due)
	}
}

func runScheduleStoreBatchedTaskFilter(t *testing.T, store scheduleConformanceStore) {
	t.Helper()
	ctx := context.Background()
	base := scheduleTestTime()
	for index, taskID := range []string{"schedule-filter-task-a", "schedule-filter-task-b", "schedule-filter-task-c"} {
		mustCreateScheduleTask(t, store, taskID)
		mustCreateTaskSchedule(t, store, TaskSchedule{
			ID: "schedule-filter-" + taskID, TaskID: taskID, Kind: TaskScheduleKindCron,
			CronExpression: "* * * * *", Timezone: "UTC", Enabled: true,
			NextRunAt: base.Add(time.Duration(index) * time.Minute),
			CreatedAt: base.Add(time.Duration(index) * time.Minute),
			UpdatedAt: base.Add(time.Duration(index) * time.Minute),
		})
	}

	items, err := store.ListTaskSchedules(ctx, TaskScheduleFilter{
		TaskIDs: []string{"schedule-filter-task-a", "schedule-filter-task-c", "schedule-filter-task-a"},
	})
	if err != nil {
		t.Fatalf("ListTaskSchedules: %v", err)
	}
	if len(items) != 2 || items[0].TaskID != "schedule-filter-task-c" || items[1].TaskID != "schedule-filter-task-a" {
		t.Fatalf("filtered schedules = %+v, want exact task A/C set", items)
	}

	items, err = store.ListTaskSchedules(ctx, TaskScheduleFilter{TaskIDs: []string{"schedule-filter-missing"}})
	if err != nil || len(items) != 0 {
		t.Fatalf("missing task filter = (%+v, %v), want no schedules", items, err)
	}
}

func runScheduleStoreDueOrdering(t *testing.T, store scheduleConformanceStore) {
	t.Helper()
	ctx := context.Background()
	base := scheduleTestTime()
	testCases := []struct {
		id      string
		nextRun time.Time
		enabled bool
	}{
		{id: "schedule-due-b", nextRun: base.Add(-time.Minute), enabled: true},
		{id: "schedule-due-a", nextRun: base.Add(-2 * time.Minute), enabled: true},
		{id: "schedule-future", nextRun: base.Add(time.Minute), enabled: true},
		{id: "schedule-paused", enabled: false},
	}
	for _, testCase := range testCases {
		taskID := "task-" + testCase.id
		mustCreateScheduleTask(t, store, taskID)
		mustCreateTaskSchedule(t, store, TaskSchedule{
			ID: testCase.id, TaskID: taskID, Kind: TaskScheduleKindCron,
			CronExpression: "* * * * *", Timezone: "UTC", Enabled: testCase.enabled,
			NextRunAt: testCase.nextRun, CreatedAt: base, UpdatedAt: base,
		})
	}

	due, err := store.ListDueTaskSchedules(ctx, base, 2)
	if err != nil {
		t.Fatalf("ListDueTaskSchedules: %v", err)
	}
	if len(due) != 2 || due[0].ID != "schedule-due-a" || due[1].ID != "schedule-due-b" {
		t.Fatalf("due schedules = %+v", due)
	}
}

func runScheduleStoreConcurrentClaimAndDisable(t *testing.T, store scheduleConformanceStore) {
	t.Helper()
	ctx := context.Background()
	base := scheduleTestTime()
	mustCreateScheduleTask(t, store, "schedule-task-claim")
	mustCreateTaskSchedule(t, store, TaskSchedule{
		ID: "schedule-claim", TaskID: "schedule-task-claim", Kind: TaskScheduleKindCron,
		CronExpression: "0 * * * *", Timezone: "UTC", Enabled: true, NextRunAt: base,
	})

	start := make(chan struct{})
	claimed := make([]bool, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for index := range claimed {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			_, claimed[index], errs[index] = store.ClaimTaskScheduleOccurrence(ctx, TaskScheduleOccurrenceClaim{
				OccurrenceID:             fmt.Sprintf("occurrence-claim-%d", index),
				ScheduleID:               "schedule-claim",
				ExpectedScheduleRevision: 1,
				ScheduledFor:             base,
				NextRunAt:                base.Add(time.Hour),
				ClaimOwner:               fmt.Sprintf("worker-%d", index),
				ClaimedAt:                base.Add(time.Minute),
			})
		}(index)
	}
	close(start)
	wg.Wait()
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("concurrent claim errors = %v", errs)
	}
	if claimed[0] == claimed[1] {
		t.Fatalf("concurrent claims = %v, want exactly one winner", claimed)
	}
	occurrences, err := store.ListTaskScheduleOccurrences(ctx, TaskScheduleOccurrenceFilter{ScheduleID: "schedule-claim"})
	if err != nil || len(occurrences) != 1 {
		t.Fatalf("occurrences after concurrent claim = (%+v, %v)", occurrences, err)
	}
	if occurrences[0].TaskID != "schedule-task-claim" || occurrences[0].ID == "" {
		t.Fatalf("claimed occurrence provenance = %+v", occurrences[0])
	}
	advanced, ok, err := store.GetTaskSchedule(ctx, "schedule-claim")
	if err != nil || !ok || !advanced.Enabled || !advanced.NextRunAt.Equal(base.Add(time.Hour)) {
		t.Fatalf("advanced schedule = (%+v, %v, %v)", advanced, ok, err)
	}
	if _, applied, err := store.ClaimTaskScheduleOccurrence(ctx, TaskScheduleOccurrenceClaim{
		OccurrenceID: "occurrence-duplicate", ScheduleID: "schedule-claim", ScheduledFor: base,
		ExpectedScheduleRevision: 2,
		ClaimOwner:               "duplicate", ClaimedAt: base.Add(2 * time.Minute),
	}); err != nil || applied {
		t.Fatalf("duplicate claim = (%v, %v), want not applied", applied, err)
	}

	mustCreateScheduleTask(t, store, "schedule-task-once")
	mustCreateTaskSchedule(t, store, TaskSchedule{
		ID: "schedule-once", TaskID: "schedule-task-once", Kind: TaskScheduleKindOnce,
		Timezone: "UTC", RunAt: base, Enabled: true, NextRunAt: base,
	})
	if _, applied, err := store.ClaimTaskScheduleOccurrence(ctx, TaskScheduleOccurrenceClaim{
		OccurrenceID: "occurrence-once", ScheduleID: "schedule-once", ScheduledFor: base,
		ExpectedScheduleRevision: 1,
		ClaimOwner:               "worker-once", ClaimedAt: base.Add(time.Minute),
	}); err != nil || !applied {
		t.Fatalf("once claim = (%v, %v)", applied, err)
	}
	disabled, ok, err := store.GetTaskSchedule(ctx, "schedule-once")
	if err != nil || !ok || disabled.Enabled || !disabled.NextRunAt.IsZero() {
		t.Fatalf("disabled once schedule = (%+v, %v, %v)", disabled, ok, err)
	}
}

func runScheduleStoreClaimWinsStaleScheduleCAS(t *testing.T, store scheduleConformanceStore) {
	t.Helper()
	ctx := t.Context()
	base := scheduleTestTime()
	mustCreateScheduleTask(t, store, "schedule-task-cas-claim")
	created := mustCreateTaskSchedule(t, store, TaskSchedule{
		ID: "schedule-cas-claim", TaskID: "schedule-task-cas-claim", Kind: TaskScheduleKindCron,
		CronExpression: "0 * * * *", Timezone: "UTC", Enabled: true, NextRunAt: base,
		CreatedAt: base.Add(-time.Hour), UpdatedAt: base.Add(-time.Hour),
	})
	stale := created
	stale.UpdatedAt = base.Add(-time.Minute)
	if _, applied, err := store.ClaimTaskScheduleOccurrence(ctx, TaskScheduleOccurrenceClaim{
		OccurrenceID: "occurrence-cas-claim", ScheduleID: created.ID, ScheduledFor: base,
		ExpectedScheduleRevision: created.Revision,
		NextRunAt:                base.Add(time.Hour), ClaimOwner: "worker-cas-claim", ClaimedAt: base,
	}); err != nil || !applied {
		t.Fatalf("ClaimTaskScheduleOccurrence = (%v, %v)", applied, err)
	}

	current, applied, err := store.CompareAndSwapTaskSchedule(ctx, TaskScheduleCompareAndSwap{
		Schedule: stale, ExpectedRevision: created.Revision,
	})
	if err != nil {
		t.Fatalf("CompareAndSwapTaskSchedule(stale): %v", err)
	}
	if applied || current.Revision != created.Revision+1 || !current.NextRunAt.Equal(base.Add(time.Hour)) {
		t.Fatalf("stale CAS result = (%+v, %v), want claimed schedule unchanged", current, applied)
	}
	stored, found, err := store.GetTaskSchedule(ctx, created.ID)
	if err != nil || !found || stored.Revision != current.Revision || !stored.NextRunAt.Equal(base.Add(time.Hour)) {
		t.Fatalf("stored schedule after stale CAS = (%+v, %v, %v)", stored, found, err)
	}

	fresh := current
	fresh.NextRunAt = base.Add(2 * time.Hour)
	fresh.UpdatedAt = base.Add(time.Minute)
	updated, applied, err := store.CompareAndSwapTaskSchedule(ctx, TaskScheduleCompareAndSwap{
		Schedule: fresh, ExpectedRevision: current.Revision,
	})
	if err != nil || !applied {
		t.Fatalf("CompareAndSwapTaskSchedule(fresh) = (%+v, %v, %v)", updated, applied, err)
	}
	if updated.Revision != current.Revision+1 || !updated.NextRunAt.Equal(fresh.NextRunAt) {
		t.Fatalf("fresh CAS result = %+v", updated)
	}
}

func runScheduleStoreDefinitionCASWinsStaleClaim(t *testing.T, store scheduleConformanceStore) {
	t.Helper()
	ctx := t.Context()
	base := scheduleTestTime()
	mustCreateScheduleTask(t, store, "schedule-task-cas-definition")
	stale := mustCreateTaskSchedule(t, store, TaskSchedule{
		ID: "schedule-cas-definition", TaskID: "schedule-task-cas-definition", Kind: TaskScheduleKindCron,
		CronExpression: "0 * * * *", Timezone: "UTC", Enabled: true, NextRunAt: base,
		CreatedAt: base.Add(-time.Hour), UpdatedAt: base.Add(-time.Hour),
	})
	fresh := stale
	fresh.CronExpression = "*/30 * * * *"
	fresh.UpdatedAt = base.Add(-time.Minute)
	updated, applied, err := store.CompareAndSwapTaskSchedule(ctx, TaskScheduleCompareAndSwap{
		Schedule: fresh, ExpectedRevision: stale.Revision,
	})
	if err != nil || !applied {
		t.Fatalf("CompareAndSwapTaskSchedule(definition) = (%+v, %v, %v)", updated, applied, err)
	}
	if !updated.NextRunAt.Equal(stale.NextRunAt) || updated.Revision != stale.Revision+1 {
		t.Fatalf("updated definition = %+v, want same immediate fire and advanced revision", updated)
	}

	if _, applied, err := store.ClaimTaskScheduleOccurrence(ctx, TaskScheduleOccurrenceClaim{
		OccurrenceID: "occurrence-stale-definition", ScheduleID: stale.ID,
		ExpectedScheduleRevision: stale.Revision, ScheduledFor: stale.NextRunAt,
		NextRunAt: base.Add(time.Hour), ClaimOwner: "worker-stale-definition", ClaimedAt: base,
	}); err != nil || applied {
		t.Fatalf("stale definition claim = (%v, %v), want not applied", applied, err)
	}
	occurrences, err := store.ListTaskScheduleOccurrences(ctx, TaskScheduleOccurrenceFilter{ScheduleID: stale.ID})
	if err != nil || len(occurrences) != 0 {
		t.Fatalf("occurrences after stale claim = (%+v, %v), want none", occurrences, err)
	}
	current, found, err := store.GetTaskSchedule(ctx, stale.ID)
	if err != nil || !found || current.Revision != updated.Revision || current.CronExpression != fresh.CronExpression || !current.NextRunAt.Equal(base) {
		t.Fatalf("schedule after stale claim = (%+v, %v, %v), want fresh definition unchanged", current, found, err)
	}

	if _, applied, err := store.ClaimTaskScheduleOccurrence(ctx, TaskScheduleOccurrenceClaim{
		OccurrenceID: "occurrence-fresh-definition", ScheduleID: current.ID,
		ExpectedScheduleRevision: current.Revision, ScheduledFor: current.NextRunAt,
		NextRunAt: base.Add(30 * time.Minute), ClaimOwner: "worker-fresh-definition", ClaimedAt: base,
	}); err != nil || !applied {
		t.Fatalf("fresh definition claim = (%v, %v), want applied", applied, err)
	}
}

func runScheduleStoreStaleReclaimCompletionAndHistory(t *testing.T, store scheduleConformanceStore) {
	t.Helper()
	ctx := context.Background()
	base := scheduleTestTime()
	for index := 0; index < 2; index++ {
		taskID := fmt.Sprintf("schedule-task-stale-%d", index)
		scheduleID := fmt.Sprintf("schedule-stale-%d", index)
		mustCreateScheduleTask(t, store, taskID)
		mustCreateTaskSchedule(t, store, TaskSchedule{
			ID: scheduleID, TaskID: taskID, Kind: TaskScheduleKindOnce,
			Timezone: "UTC", RunAt: base.Add(time.Duration(index) * time.Hour), Enabled: true,
			NextRunAt: base.Add(time.Duration(index) * time.Hour),
		})
		if _, applied, err := store.ClaimTaskScheduleOccurrence(ctx, TaskScheduleOccurrenceClaim{
			OccurrenceID:             fmt.Sprintf("occurrence-stale-%d", index),
			ScheduleID:               scheduleID,
			ExpectedScheduleRevision: 1,
			ScheduledFor:             base.Add(time.Duration(index) * time.Hour),
			ClaimOwner:               "original-owner",
			ClaimedAt:                base.Add(time.Duration(index) * time.Minute),
		}); err != nil || !applied {
			t.Fatalf("ClaimTaskScheduleOccurrence(%d) = (%v, %v)", index, applied, err)
		}
	}

	stale, err := store.ListTaskScheduleOccurrences(ctx, TaskScheduleOccurrenceFilter{
		Status: TaskScheduleOccurrenceClaimed, ClaimedBefore: base.Add(30 * time.Second), Limit: 1,
	})
	if err != nil || len(stale) != 1 || stale[0].ID != "occurrence-stale-0" {
		t.Fatalf("global stale occurrence scan = (%+v, %v)", stale, err)
	}
	if _, applied, err := store.ReclaimTaskScheduleOccurrence(ctx, TaskScheduleOccurrenceReclaim{
		ScheduleID: "schedule-stale-0", ScheduledFor: base, StaleBefore: base.Add(-time.Second),
		ClaimOwner: "new-owner", ClaimedAt: base.Add(2 * time.Hour),
	}); err != nil || applied {
		t.Fatalf("early reclaim = (%v, %v), want not applied", applied, err)
	}
	reclaimed, applied, err := store.ReclaimTaskScheduleOccurrence(ctx, TaskScheduleOccurrenceReclaim{
		ScheduleID: "schedule-stale-0", ScheduledFor: base, StaleBefore: base,
		ClaimOwner: "new-owner", ClaimedAt: base.Add(2 * time.Hour),
	})
	if err != nil || !applied || reclaimed.ID != "occurrence-stale-0" || reclaimed.ClaimOwner != "new-owner" {
		t.Fatalf("stale reclaim = (%+v, %v, %v)", reclaimed, applied, err)
	}
	if _, applied, err := store.CompleteTaskScheduleOccurrence(ctx, TaskScheduleOccurrenceCompletion{
		ScheduleID: "schedule-stale-0", ScheduledFor: base, ClaimOwner: "original-owner",
		Status: TaskScheduleOccurrenceStarted, RunID: "run-stale", CompletedAt: base.Add(3 * time.Hour),
	}); err != nil || applied {
		t.Fatalf("stale owner completion = (%v, %v), want not applied", applied, err)
	}
	completed, applied, err := store.CompleteTaskScheduleOccurrence(ctx, TaskScheduleOccurrenceCompletion{
		ScheduleID: "schedule-stale-0", ScheduledFor: base, ClaimOwner: "new-owner",
		Status: TaskScheduleOccurrenceStarted, RunID: "run-stale", CompletedAt: base.Add(3 * time.Hour),
	})
	if err != nil || !applied || completed.Status != TaskScheduleOccurrenceStarted || completed.RunID != "run-stale" || completed.CompletedAt.IsZero() {
		t.Fatalf("occurrence completion = (%+v, %v, %v)", completed, applied, err)
	}
	if _, applied, err := store.CompleteTaskScheduleOccurrence(ctx, TaskScheduleOccurrenceCompletion{
		ScheduleID: "schedule-stale-0", ScheduledFor: base, ClaimOwner: "new-owner",
		Status: TaskScheduleOccurrenceFailed, Error: "duplicate", CompletedAt: base.Add(4 * time.Hour),
	}); err != nil || applied {
		t.Fatalf("duplicate completion = (%v, %v), want not applied", applied, err)
	}

	claimed, err := store.ListTaskScheduleOccurrences(ctx, TaskScheduleOccurrenceFilter{Status: TaskScheduleOccurrenceClaimed})
	if err != nil || len(claimed) != 1 || claimed[0].ID != "occurrence-stale-1" {
		t.Fatalf("claimed occurrence filter = (%+v, %v)", claimed, err)
	}
}

func runScheduleStoreClaimRenewalFencesStaleRecovery(t *testing.T, store scheduleConformanceStore) {
	t.Helper()
	ctx := t.Context()
	base := scheduleTestTime()
	taskID := "schedule-task-renewal"
	scheduleID := "schedule-renewal"
	mustCreateScheduleTask(t, store, taskID)
	occurrence := mustClaimScheduleOccurrence(t, store, taskID, scheduleID, "occurrence-renewal", "live-owner", base)

	if _, applied, err := store.RenewTaskScheduleOccurrence(ctx, TaskScheduleOccurrenceRenewal{
		OccurrenceID: occurrence.ID, ScheduleID: scheduleID, ScheduledFor: base,
		ClaimOwner: "wrong-owner", ClaimedAt: base.Add(4 * time.Minute),
	}); err != nil || applied {
		t.Fatalf("wrong-owner renewal = (%v, %v), want fenced", applied, err)
	}
	renewed, applied, err := store.RenewTaskScheduleOccurrence(ctx, TaskScheduleOccurrenceRenewal{
		OccurrenceID: occurrence.ID, ScheduleID: scheduleID, ScheduledFor: base,
		ClaimOwner: "live-owner", ClaimedAt: base.Add(4 * time.Minute),
	})
	if err != nil || !applied || !renewed.ClaimedAt.Equal(base.Add(4*time.Minute)) {
		t.Fatalf("live-owner renewal = (%+v, %v, %v)", renewed, applied, err)
	}
	if _, applied, err := store.RenewTaskScheduleOccurrence(ctx, TaskScheduleOccurrenceRenewal{
		OccurrenceID: occurrence.ID, ScheduleID: scheduleID, ScheduledFor: base,
		ClaimOwner: "live-owner", ClaimedAt: base.Add(3 * time.Minute),
	}); err != nil || applied {
		t.Fatalf("backward renewal = (%v, %v), want fenced", applied, err)
	}
	if _, applied, err := store.ReclaimTaskScheduleOccurrence(ctx, TaskScheduleOccurrenceReclaim{
		ScheduleID: scheduleID, ScheduledFor: base, StaleBefore: base.Add(3 * time.Minute),
		ClaimOwner: "recovery-owner", ClaimedAt: base.Add(5 * time.Minute),
	}); err != nil || applied {
		t.Fatalf("reclaim before renewed timestamp = (%v, %v), want not applied", applied, err)
	}
	reclaimed, applied, err := store.ReclaimTaskScheduleOccurrence(ctx, TaskScheduleOccurrenceReclaim{
		ScheduleID: scheduleID, ScheduledFor: base, StaleBefore: base.Add(4 * time.Minute),
		ClaimOwner: "recovery-owner", ClaimedAt: base.Add(5 * time.Minute),
	})
	if err != nil || !applied || reclaimed.ClaimOwner != "recovery-owner" {
		t.Fatalf("reclaim after renewal expires = (%+v, %v, %v)", reclaimed, applied, err)
	}
	if _, applied, err := store.RenewTaskScheduleOccurrence(ctx, TaskScheduleOccurrenceRenewal{
		OccurrenceID: occurrence.ID, ScheduleID: scheduleID, ScheduledFor: base,
		ClaimOwner: "live-owner", ClaimedAt: base.Add(6 * time.Minute),
	}); err != nil || applied {
		t.Fatalf("displaced-owner renewal = (%v, %v), want fenced", applied, err)
	}
}

func runScheduleStoreTaskDeleteCascades(t *testing.T, store scheduleConformanceStore) {
	t.Helper()
	ctx := context.Background()
	base := scheduleTestTime()
	mustCreateScheduleTask(t, store, "schedule-task-cascade")
	mustCreateTaskSchedule(t, store, TaskSchedule{
		ID: "schedule-cascade", TaskID: "schedule-task-cascade", Kind: TaskScheduleKindOnce,
		Timezone: "UTC", RunAt: base, Enabled: true, NextRunAt: base,
	})
	if _, applied, err := store.ClaimTaskScheduleOccurrence(ctx, TaskScheduleOccurrenceClaim{
		OccurrenceID: "occurrence-cascade", ScheduleID: "schedule-cascade", ScheduledFor: base,
		ExpectedScheduleRevision: 1,
		ClaimOwner:               "cascade-owner", ClaimedAt: base,
	}); err != nil || !applied {
		t.Fatalf("ClaimTaskScheduleOccurrence = (%v, %v)", applied, err)
	}
	if err := store.DeleteTask(ctx, "schedule-task-cascade"); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}
	if _, ok, err := store.GetTaskSchedule(ctx, "schedule-cascade"); err != nil || ok {
		t.Fatalf("schedule after task delete = (%v, %v), want absent", ok, err)
	}
	occurrences, err := store.ListTaskScheduleOccurrences(ctx, TaskScheduleOccurrenceFilter{ScheduleID: "schedule-cascade"})
	if err != nil || len(occurrences) != 0 {
		t.Fatalf("occurrences after task delete = (%+v, %v)", occurrences, err)
	}
}

func runScheduleStoreTaskDeleteConcurrentWithScheduledRunAdmission(t *testing.T, store scheduleConformanceStore) {
	t.Helper()
	ctx := t.Context()
	base := scheduleTestTime()
	const taskID = "schedule-task-delete-admission-race"
	const runID = "schedule-run-delete-admission-race"
	const owner = "schedule-delete-admission-owner"

	mustCreateScheduleTask(t, store, taskID)
	occurrence := mustClaimScheduleOccurrence(
		t,
		store,
		taskID,
		"schedule-delete-admission-race",
		"occurrence-delete-admission-race",
		owner,
		base,
	)
	admission := taskScheduleRunAdmission(taskID, runID, occurrence, owner, base.Add(time.Minute))

	start := make(chan struct{})
	var deleteErr, admissionErr error
	var admissionResult TaskScheduleRunAdmissionResult
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		deleteErr = store.DeleteTask(ctx, taskID)
	}()
	go func() {
		defer wg.Done()
		<-start
		admissionResult, admissionErr = store.ApplyTaskScheduleRunAdmission(ctx, admission)
	}()
	close(start)
	wg.Wait()

	switch {
	case deleteErr == nil:
		if !errors.Is(admissionErr, ErrTaskNotFound) {
			t.Fatalf("delete won but scheduled Run admission error = %v, want ErrTaskNotFound", admissionErr)
		}
		if _, found, err := store.GetTask(ctx, taskID); err != nil || found {
			t.Fatalf("Task after delete winner = (found %v, error %v), want absent", found, err)
		}
		runs, err := store.ListRuns(ctx, taskID)
		if err != nil || len(runs) != 0 {
			t.Fatalf("Runs after delete winner = (%+v, %v), want none", runs, err)
		}
		if _, found, err := store.GetTaskSchedule(ctx, occurrence.ScheduleID); err != nil || found {
			t.Fatalf("Schedule after delete winner = (found %v, error %v), want absent", found, err)
		}
	case admissionErr == nil:
		if !admissionResult.Applied || admissionResult.Run.ID != runID {
			t.Fatalf("scheduled Run admission winner = %+v, want applied %q", admissionResult, runID)
		}
		if !errors.Is(deleteErr, ErrActiveRun) {
			t.Fatalf("scheduled Run admission won but delete error = %v, want ErrActiveRun", deleteErr)
		}
		if _, found, err := store.GetTask(ctx, taskID); err != nil || !found {
			t.Fatalf("Task after admission winner = (found %v, error %v), want present", found, err)
		}
		if _, found, err := store.GetRun(ctx, taskID, runID); err != nil || !found {
			t.Fatalf("Run after admission winner = (found %v, error %v), want present", found, err)
		}
	default:
		t.Fatalf("delete/scheduled admission race = (%v, %v), want exactly one winner", deleteErr, admissionErr)
	}
}

func runScheduleStoreRunAdmissionReusesTerminalRun(t *testing.T, store scheduleConformanceStore) {
	t.Helper()
	ctx := t.Context()
	base := scheduleTestTime()
	taskID := "schedule-task-admission-terminal"
	scheduleID := "schedule-admission-terminal"
	occurrenceID := "occurrence-admission-terminal"
	mustCreateScheduleTask(t, store, taskID)
	occurrence := mustClaimScheduleOccurrence(t, store, taskID, scheduleID, occurrenceID, "worker-terminal", base)
	existing := types.TaskRun{
		ID: "run-admission-terminal-existing", TaskID: taskID, Number: 1, Status: "completed",
		ScheduleID: scheduleID, ScheduleOccurrenceID: occurrenceID, ScheduledFor: base,
		StartedAt: base, FinishedAt: base.Add(time.Minute),
	}
	if _, err := store.CreateRun(ctx, existing); err != nil {
		t.Fatalf("CreateRun(existing): %v", err)
	}

	result, err := store.PreflightTaskScheduleRunAdmission(ctx, taskScheduleRunPreflight(
		taskID, occurrence, "worker-terminal", base.Add(2*time.Minute),
	))
	if err != nil {
		t.Fatalf("PreflightTaskScheduleRunAdmission: %v", err)
	}
	if !result.ExistingRun || result.Ready || result.Run.ID != existing.ID || result.Run.Status != "completed" {
		t.Fatalf("admission result = %+v, want existing terminal run", result)
	}
	if result.Occurrence.Status != TaskScheduleOccurrenceStarted || result.Occurrence.RunID != existing.ID {
		t.Fatalf("linked occurrence = %+v, want started existing run", result.Occurrence)
	}
	runs, err := store.ListRuns(ctx, taskID)
	if err != nil || len(runs) != 1 {
		t.Fatalf("ListRuns = (%+v, %v), want one existing run", runs, err)
	}
}

func runScheduleStoreRunAdmissionConcurrentOwners(t *testing.T, store scheduleConformanceStore) {
	t.Helper()
	ctx := t.Context()
	base := scheduleTestTime()
	taskID := "schedule-task-admission-owners"
	scheduleID := "schedule-admission-owners"
	occurrenceID := "occurrence-admission-owners"
	mustCreateScheduleTask(t, store, taskID)
	occurrence := mustClaimScheduleOccurrence(t, store, taskID, scheduleID, occurrenceID, "old-owner", base)
	reclaimed, applied, err := store.ReclaimTaskScheduleOccurrence(ctx, TaskScheduleOccurrenceReclaim{
		ScheduleID: scheduleID, ScheduledFor: base, StaleBefore: base,
		ClaimOwner: "new-owner", ClaimedAt: base.Add(time.Minute),
	})
	if err != nil || !applied {
		t.Fatalf("ReclaimTaskScheduleOccurrence = (%+v, %v, %v)", reclaimed, applied, err)
	}
	occurrence = reclaimed

	start := make(chan struct{})
	owners := []string{"old-owner", "new-owner"}
	results := make([]TaskScheduleRunAdmissionResult, len(owners))
	errs := make([]error, len(owners))
	var wg sync.WaitGroup
	for index, owner := range owners {
		wg.Add(1)
		go func(index int, owner string) {
			defer wg.Done()
			<-start
			results[index], errs[index] = store.ApplyTaskScheduleRunAdmission(ctx, taskScheduleRunAdmission(
				taskID, fmt.Sprintf("run-admission-owner-%d", index), occurrence, owner, base.Add(2*time.Minute),
			))
		}(index, owner)
	}
	close(start)
	wg.Wait()
	if errs[1] != nil || !results[1].Applied {
		t.Fatalf("authoritative owner admission = (%+v, %v), want applied", results[1], errs[1])
	}
	if errs[0] != nil && !errors.Is(errs[0], ErrScheduleOccurrenceClaimLost) {
		t.Fatalf("stale owner error = %v, want claim lost or existing replay", errs[0])
	}
	if results[0].Applied {
		t.Fatalf("stale owner admission = %+v, must not create a run", results[0])
	}
	if errs[0] == nil && (!results[0].ExistingRun || results[0].Run.ID != results[1].Run.ID) {
		t.Fatalf("late stale-owner replay = %+v, want authoritative existing run", results[0])
	}
	runs, err := store.ListRuns(ctx, taskID)
	if err != nil || len(runs) != 1 || runs[0].ID != results[1].Run.ID {
		t.Fatalf("ListRuns = (%+v, %v), want one new-owner run", runs, err)
	}
}

func runScheduleStoreRunAdmissionPersistsInitialApprovalEffects(t *testing.T, store scheduleConformanceStore) {
	t.Helper()
	ctx := t.Context()
	base := scheduleTestTime()
	taskID := "schedule-task-admission-approval"
	runID := "run-admission-approval"
	owner := "worker-admission-approval"
	mustCreateScheduleTask(t, store, taskID)
	occurrence := mustClaimScheduleOccurrence(
		t, store, taskID, "schedule-admission-approval", "occurrence-admission-approval", owner, base,
	)
	renewed, applied, err := store.RenewTaskScheduleOccurrence(ctx, TaskScheduleOccurrenceRenewal{
		OccurrenceID: occurrence.ID, ScheduleID: occurrence.ScheduleID, ScheduledFor: occurrence.ScheduledFor,
		ClaimOwner: owner, ClaimedAt: base.Add(2 * time.Minute),
	})
	if err != nil || !applied {
		t.Fatalf("RenewTaskScheduleOccurrence = (%+v, %v, %v)", renewed, applied, err)
	}
	occurrence = renewed
	admission := taskScheduleRunAdmission(taskID, runID, occurrence, owner, base.Add(time.Minute))
	admission.Task.Status = "awaiting_approval"
	admission.Run.Status = "awaiting_approval"
	admission.Run.ApprovalCount = 1
	admission.Run.RequestID = "request-admission-approval"
	admission.Run.TraceID = "trace-admission-approval"
	admission.Approval = &types.TaskApproval{
		ID: "approval-admission-approval", TaskID: taskID, RunID: runID,
		Kind: "shell_exec", Status: "pending", Reason: "approval required",
		RequestedBy: "operator", CreatedAt: base.Add(time.Minute),
		RequestID: admission.Run.RequestID, TraceID: admission.Run.TraceID,
	}

	result, err := store.ApplyTaskScheduleRunAdmission(ctx, admission)
	if err != nil || !result.Applied || result.Run.ApprovalCount != 1 {
		t.Fatalf("ApplyTaskScheduleRunAdmission = (%+v, %v), want applied approval Run", result, err)
	}
	if result.Occurrence.CompletedAt.Before(occurrence.ClaimedAt) {
		t.Fatalf("occurrence completed_at = %s, before renewed claimed_at %s", result.Occurrence.CompletedAt, occurrence.ClaimedAt)
	}
	assertScheduledAdmissionApprovalEffects(t, store, taskID, runID)

	replay, err := store.ApplyTaskScheduleRunAdmission(ctx, admission)
	if err != nil || !replay.ExistingRun || replay.Applied || replay.Run.ID != runID {
		t.Fatalf("ApplyTaskScheduleRunAdmission(replay) = (%+v, %v), want existing", replay, err)
	}
	assertScheduledAdmissionApprovalEffects(t, store, taskID, runID)
}

func runScheduleStoreRunAdmissionApprovalFailureRollsBack(t *testing.T, store scheduleConformanceStore) {
	t.Helper()
	ctx := t.Context()
	base := scheduleTestTime()
	taskID := "schedule-task-admission-approval-rollback"
	runID := "run-admission-approval-rollback"
	owner := "worker-admission-approval-rollback"
	approvalID := "approval-admission-conflict"
	mustCreateScheduleTask(t, store, taskID)
	occurrence := mustClaimScheduleOccurrence(
		t, store, taskID, "schedule-admission-approval-rollback", "occurrence-admission-approval-rollback", owner, base,
	)
	if _, err := store.CreateApproval(ctx, types.TaskApproval{
		ID: approvalID, TaskID: taskID, RunID: "unrelated-run", Status: "pending", CreatedAt: base,
	}); err != nil {
		t.Fatalf("CreateApproval(conflict): %v", err)
	}
	admission := taskScheduleRunAdmission(taskID, runID, occurrence, owner, base.Add(time.Minute))
	admission.Task.Status = "awaiting_approval"
	admission.Run.Status = "awaiting_approval"
	admission.Run.ApprovalCount = 1
	admission.Approval = &types.TaskApproval{
		ID: approvalID, TaskID: taskID, RunID: runID, Kind: "shell_exec",
		Status: "pending", CreatedAt: base.Add(time.Minute),
	}

	if result, err := store.ApplyTaskScheduleRunAdmission(ctx, admission); err == nil || result.Applied {
		t.Fatalf("conflicting approval admission = (%+v, %v), want rollback error", result, err)
	}
	if _, found, err := store.GetRun(ctx, taskID, runID); err != nil || found {
		t.Fatalf("Run after rolled-back admission = (found %v, error %v), want absent", found, err)
	}
	storedTask, found, err := store.GetTask(ctx, taskID)
	if err != nil || !found || storedTask.Status != types.TaskStatusNotStarted || storedTask.LatestRunID != "" {
		t.Fatalf("Task after rolled-back admission = (%+v, %v, %v)", storedTask, found, err)
	}
	occurrences, err := store.ListTaskScheduleOccurrences(ctx, TaskScheduleOccurrenceFilter{ScheduleID: occurrence.ScheduleID})
	if err != nil || len(occurrences) != 1 || occurrences[0].Status != TaskScheduleOccurrenceClaimed || occurrences[0].RunID != "" {
		t.Fatalf("occurrence after rolled-back admission = (%+v, %v), want claimed", occurrences, err)
	}
	events, err := store.ListRunEvents(ctx, taskID, runID, 0, 20)
	if err != nil || len(events) != 0 {
		t.Fatalf("events after rolled-back admission = (%+v, %v), want none", events, err)
	}

	admission.Approval.ID = "approval-admission-retry"
	result, err := store.ApplyTaskScheduleRunAdmission(ctx, admission)
	if err != nil || !result.Applied || result.Run.ID != runID {
		t.Fatalf("retry admission = (%+v, %v), want applied", result, err)
	}
	approvals, err := store.ListApprovals(ctx, taskID)
	if err != nil {
		t.Fatalf("ListApprovals: %v", err)
	}
	pendingForRun := 0
	for _, approval := range approvals {
		if approval.RunID == runID && approval.Status == "pending" {
			pendingForRun++
		}
	}
	if pendingForRun != 1 {
		t.Fatalf("approvals after retry = %+v, want exactly one pending for scheduled Run", approvals)
	}
}

func assertScheduledAdmissionApprovalEffects(t *testing.T, store scheduleConformanceStore, taskID, runID string) {
	t.Helper()
	approvals, err := store.ListApprovals(t.Context(), taskID)
	if err != nil || len(approvals) != 1 || approvals[0].RunID != runID || approvals[0].Status != "pending" {
		t.Fatalf("scheduled approvals = (%+v, %v), want exactly one pending", approvals, err)
	}
	run, found, err := store.GetRun(t.Context(), taskID, runID)
	if err != nil || !found {
		t.Fatalf("GetRun = (%+v, %v, %v)", run, found, err)
	}
	occurrences, err := store.ListTaskScheduleOccurrences(t.Context(), TaskScheduleOccurrenceFilter{ScheduleID: run.ScheduleID})
	if err != nil || len(occurrences) != 1 || approvals[0].CreatedAt.Before(occurrences[0].ClaimedAt) {
		t.Fatalf("approval/occurrence timestamps = (%s, %+v, %v), want approval at or after claim", approvals[0].CreatedAt, occurrences, err)
	}
	events, err := store.ListRunEvents(t.Context(), taskID, runID, 0, 20)
	if err != nil {
		t.Fatalf("ListRunEvents: %v", err)
	}
	counts := make(map[string]int)
	for _, event := range events {
		if event.CreatedAt.Before(occurrences[0].ClaimedAt) {
			t.Fatalf("event %s created_at = %s, before renewed claimed_at %s", event.EventType, event.CreatedAt, occurrences[0].ClaimedAt)
		}
		counts[event.EventType]++
	}
	for _, eventType := range []string{"run.created", "approval.requested", "run.awaiting_approval"} {
		if counts[eventType] != 1 {
			t.Fatalf("event counts = %+v, want exactly one %s", counts, eventType)
		}
	}
	if counts["run.queued"] != 0 {
		t.Fatalf("event counts = %+v, awaiting approval must not be queued", counts)
	}
}

func runScheduleStoreRunAdmissionOverlapIsCrashSafe(t *testing.T, store scheduleConformanceStore) {
	t.Helper()
	ctx := t.Context()
	base := scheduleTestTime()
	taskID := "schedule-task-admission-overlap"
	scheduleID := "schedule-admission-overlap"
	occurrenceID := "occurrence-admission-overlap"
	task, err := store.CreateTask(ctx, types.Task{
		ID: taskID, Status: "running", LatestRunID: "run-admission-manual", CreatedAt: base, UpdatedAt: base,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	manualRun := types.TaskRun{
		ID: task.LatestRunID, TaskID: taskID, Number: 1, Status: "running", StartedAt: base.Add(-time.Minute),
	}
	if _, err := store.CreateRun(ctx, manualRun); err != nil {
		t.Fatalf("CreateRun(manual): %v", err)
	}
	occurrence := mustClaimScheduleOccurrence(t, store, taskID, scheduleID, occurrenceID, "worker-overlap", base)

	first, err := store.PreflightTaskScheduleRunAdmission(ctx, taskScheduleRunPreflight(
		taskID, occurrence, "worker-overlap", base.Add(time.Minute),
	))
	if err != nil {
		t.Fatalf("PreflightTaskScheduleRunAdmission(overlap): %v", err)
	}
	if !first.Skipped || first.Ready || first.Occurrence.Status != TaskScheduleOccurrenceSkipped {
		t.Fatalf("overlap admission = %+v, want durable skip", first)
	}
	manualRun.Status = "completed"
	manualRun.FinishedAt = base.Add(2 * time.Minute)
	if _, err := store.UpdateRun(ctx, manualRun); err != nil {
		t.Fatalf("UpdateRun(manual terminal): %v", err)
	}
	replay, err := store.ApplyTaskScheduleRunAdmission(ctx, taskScheduleRunAdmission(
		taskID, "run-admission-after-overlap", occurrence, "worker-overlap", base.Add(3*time.Minute),
	))
	if err != nil {
		t.Fatalf("ApplyTaskScheduleRunAdmission(replay): %v", err)
	}
	if !replay.Skipped || replay.Applied {
		t.Fatalf("overlap replay = %+v, want already-skipped occurrence", replay)
	}
	runs, err := store.ListRuns(ctx, taskID)
	if err != nil || len(runs) != 1 || runs[0].ID != manualRun.ID {
		t.Fatalf("ListRuns after overlap recovery = (%+v, %v), want only manual run", runs, err)
	}
}

func runScheduleStoreRunAdmissionRechecksAfterReady(t *testing.T, store scheduleConformanceStore) {
	t.Helper()
	ctx := t.Context()
	base := scheduleTestTime()
	taskID := "schedule-task-admission-ready-race"
	scheduleID := "schedule-admission-ready-race"
	occurrenceID := "occurrence-admission-ready-race"
	task, err := store.CreateTask(ctx, types.Task{
		ID: taskID, Status: "failed", CreatedAt: base, UpdatedAt: base,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	occurrence := mustClaimScheduleOccurrence(t, store, taskID, scheduleID, occurrenceID, "worker-ready-race", base)
	preflight, err := store.PreflightTaskScheduleRunAdmission(ctx, taskScheduleRunPreflight(
		taskID, occurrence, "worker-ready-race", base.Add(time.Minute),
	))
	if err != nil || !preflight.Ready {
		t.Fatalf("PreflightTaskScheduleRunAdmission = (%+v, %v), want ready", preflight, err)
	}
	manual := types.TaskRun{
		ID: "run-admission-ready-race-manual", TaskID: taskID, Number: 1,
		Status: "running", StartedAt: base.Add(time.Minute),
	}
	if _, err := store.CreateRun(ctx, manual); err != nil {
		t.Fatalf("CreateRun(manual): %v", err)
	}
	task.Status = "running"
	task.LatestRunID = manual.ID
	task.UpdatedAt = base.Add(time.Minute)
	if _, err := store.UpdateTask(ctx, task); err != nil {
		t.Fatalf("UpdateTask(manual projection): %v", err)
	}

	result, err := store.ApplyTaskScheduleRunAdmission(ctx, taskScheduleRunAdmission(
		taskID, "run-admission-ready-race-scheduled", occurrence, "worker-ready-race", base.Add(2*time.Minute),
	))
	if err != nil {
		t.Fatalf("ApplyTaskScheduleRunAdmission: %v", err)
	}
	if !result.Skipped || result.Applied || result.Occurrence.Status != TaskScheduleOccurrenceSkipped {
		t.Fatalf("post-preflight admission = %+v, want overlap skip", result)
	}
	runs, err := store.ListRuns(ctx, taskID)
	if err != nil || len(runs) != 1 || runs[0].ID != manual.ID {
		t.Fatalf("ListRuns = (%+v, %v), want only racing manual run", runs, err)
	}
}

func mustClaimScheduleOccurrence(
	t *testing.T,
	store scheduleConformanceStore,
	taskID, scheduleID, occurrenceID, owner string,
	scheduledFor time.Time,
) TaskScheduleOccurrence {
	t.Helper()
	mustCreateTaskSchedule(t, store, TaskSchedule{
		ID: scheduleID, TaskID: taskID, Kind: TaskScheduleKindOnce,
		Timezone: "UTC", RunAt: scheduledFor, Enabled: true, NextRunAt: scheduledFor,
	})
	occurrence, applied, err := store.ClaimTaskScheduleOccurrence(t.Context(), TaskScheduleOccurrenceClaim{
		OccurrenceID: occurrenceID, ScheduleID: scheduleID, ScheduledFor: scheduledFor,
		ExpectedScheduleRevision: 1,
		ClaimOwner:               owner, ClaimedAt: scheduledFor,
	})
	if err != nil || !applied {
		t.Fatalf("ClaimTaskScheduleOccurrence = (%+v, %v, %v)", occurrence, applied, err)
	}
	return occurrence
}

func taskScheduleRunAdmission(
	taskID, runID string,
	occurrence TaskScheduleOccurrence,
	owner string,
	completedAt time.Time,
) TaskScheduleRunAdmission {
	return TaskScheduleRunAdmission{
		Task: types.Task{ID: taskID, Status: "queued", LatestRunID: runID, UpdatedAt: completedAt},
		Run: types.TaskRun{
			ID: runID, TaskID: taskID, Status: "queued", StartedAt: completedAt,
			ScheduleID: occurrence.ScheduleID, ScheduleOccurrenceID: occurrence.ID,
			ScheduledFor: occurrence.ScheduledFor,
		},
		ClaimOwner: owner, CompletedAt: completedAt,
	}
}

func taskScheduleRunPreflight(
	taskID string,
	occurrence TaskScheduleOccurrence,
	owner string,
	completedAt time.Time,
) TaskScheduleRunPreflight {
	return TaskScheduleRunPreflight{
		TaskID: taskID, ScheduleID: occurrence.ScheduleID,
		ScheduleOccurrenceID: occurrence.ID, ScheduledFor: occurrence.ScheduledFor,
		ClaimOwner: owner, CompletedAt: completedAt,
	}
}

func mustCreateScheduleTask(t *testing.T, store Store, id string) {
	t.Helper()
	if _, err := store.CreateTask(context.Background(), types.Task{ID: id, Status: types.TaskStatusNotStarted}); err != nil {
		t.Fatalf("CreateTask(%s): %v", id, err)
	}
}

func mustCreateTaskSchedule(t *testing.T, store ScheduleStore, schedule TaskSchedule) TaskSchedule {
	t.Helper()
	stored, applied, err := store.CompareAndSwapTaskSchedule(t.Context(), TaskScheduleCompareAndSwap{Schedule: schedule})
	if err != nil || !applied {
		t.Fatalf("CompareAndSwapTaskSchedule(create) = (%+v, %v, %v)", stored, applied, err)
	}
	return stored
}

func scheduleTestTime() time.Time {
	return time.Date(2026, time.July, 20, 10, 0, 0, 0, time.UTC)
}
