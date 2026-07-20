package taskstate

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hecatehq/hecate/pkg/types"
)

func (s *MemoryStore) CompareAndSwapTaskSchedule(_ context.Context, mutation TaskScheduleCompareAndSwap) (TaskSchedule, bool, error) {
	if mutation.ExpectedRevision < 0 {
		return TaskSchedule{}, false, fmt.Errorf("expected task schedule revision must be non-negative")
	}
	schedule := normalizeTaskSchedule(mutation.Schedule, time.Now().UTC())
	if err := validateTaskSchedule(schedule); err != nil {
		return TaskSchedule{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tasks[schedule.TaskID]; !ok {
		return TaskSchedule{}, false, fmt.Errorf("task %q not found", schedule.TaskID)
	}
	var existing TaskSchedule
	found := false
	for _, item := range s.schedules {
		if item.TaskID == schedule.TaskID {
			existing = item
			found = true
			break
		}
	}
	if !found {
		if mutation.ExpectedRevision != 0 {
			return TaskSchedule{}, false, nil
		}
		if conflicting, ok := s.schedules[schedule.ID]; ok && conflicting.TaskID != schedule.TaskID {
			return TaskSchedule{}, false, fmt.Errorf("task schedule %q already belongs to task %q", schedule.ID, conflicting.TaskID)
		}
		schedule.Revision = 1
		s.schedules[schedule.ID] = schedule
		return schedule, true, nil
	}
	if mutation.ExpectedRevision == 0 || existing.Revision != mutation.ExpectedRevision {
		return existing, false, nil
	}
	schedule.ID = existing.ID
	schedule.CreatedAt = existing.CreatedAt
	schedule.Revision = existing.Revision + 1
	s.schedules[schedule.ID] = schedule
	return schedule, true, nil
}

func (s *MemoryStore) GetTaskSchedule(_ context.Context, id string) (TaskSchedule, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	schedule, ok := s.schedules[id]
	return schedule, ok, nil
}

func (s *MemoryStore) GetTaskScheduleByTask(_ context.Context, taskID string) (TaskSchedule, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, schedule := range s.schedules {
		if schedule.TaskID == taskID {
			return schedule, true, nil
		}
	}
	return TaskSchedule{}, false, nil
}

func (s *MemoryStore) ListTaskSchedules(_ context.Context, filter TaskScheduleFilter) ([]TaskSchedule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var taskIDs map[string]struct{}
	if len(filter.TaskIDs) > 0 {
		taskIDs = make(map[string]struct{}, len(filter.TaskIDs))
		for _, taskID := range filter.TaskIDs {
			taskIDs[taskID] = struct{}{}
		}
	}
	items := make([]TaskSchedule, 0, len(s.schedules))
	for _, schedule := range s.schedules {
		if taskIDs != nil {
			if _, ok := taskIDs[schedule.TaskID]; !ok {
				continue
			}
		}
		if filter.Enabled != nil && schedule.Enabled != *filter.Enabled {
			continue
		}
		items = append(items, schedule)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	if filter.Limit > 0 && len(items) > filter.Limit {
		items = items[:filter.Limit]
	}
	return items, nil
}

func (s *MemoryStore) DeleteTaskSchedule(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteTaskScheduleLocked(id)
	return nil
}

func (s *MemoryStore) deleteTaskScheduleLocked(id string) {
	delete(s.schedules, id)
	for key, occurrence := range s.occurrences {
		if occurrence.ScheduleID == id {
			delete(s.occurrences, key)
		}
	}
}

func (s *MemoryStore) ListDueTaskSchedules(_ context.Context, dueAt time.Time, limit int) ([]TaskSchedule, error) {
	if dueAt.IsZero() {
		return nil, fmt.Errorf("due time is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dueAt = dueAt.UTC()
	items := make([]TaskSchedule, 0)
	for _, schedule := range s.schedules {
		if !schedule.Enabled || schedule.NextRunAt.IsZero() || schedule.NextRunAt.After(dueAt) {
			continue
		}
		items = append(items, schedule)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].NextRunAt.Equal(items[j].NextRunAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].NextRunAt.Before(items[j].NextRunAt)
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (s *MemoryStore) ClaimTaskScheduleOccurrence(_ context.Context, claim TaskScheduleOccurrenceClaim) (TaskScheduleOccurrence, bool, error) {
	claim = normalizeTaskScheduleOccurrenceClaim(claim)
	if err := validateTaskScheduleOccurrenceClaim(claim); err != nil {
		return TaskScheduleOccurrence{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	schedule, ok := s.schedules[claim.ScheduleID]
	if !ok || schedule.Revision != claim.ExpectedScheduleRevision || !schedule.Enabled || !schedule.NextRunAt.Equal(claim.ScheduledFor) {
		return TaskScheduleOccurrence{}, false, nil
	}
	key := taskScheduleOccurrenceKey(claim.ScheduleID, claim.ScheduledFor)
	if _, exists := s.occurrences[key]; exists {
		return TaskScheduleOccurrence{}, false, nil
	}
	occurrence := TaskScheduleOccurrence{
		ID:           claim.OccurrenceID,
		TaskID:       schedule.TaskID,
		ScheduleID:   claim.ScheduleID,
		ScheduledFor: claim.ScheduledFor,
		Status:       TaskScheduleOccurrenceClaimed,
		ClaimOwner:   claim.ClaimOwner,
		ClaimedAt:    claim.ClaimedAt,
	}
	for _, existing := range s.occurrences {
		if existing.ID == occurrence.ID {
			return TaskScheduleOccurrence{}, false, fmt.Errorf("task schedule occurrence %q already exists", occurrence.ID)
		}
	}
	s.occurrences[key] = occurrence
	schedule.NextRunAt = claim.NextRunAt
	schedule.Enabled = !claim.NextRunAt.IsZero()
	schedule.UpdatedAt = claim.ClaimedAt
	schedule.Revision++
	s.schedules[schedule.ID] = schedule
	return occurrence, true, nil
}

func (s *MemoryStore) ReclaimTaskScheduleOccurrence(_ context.Context, reclaim TaskScheduleOccurrenceReclaim) (TaskScheduleOccurrence, bool, error) {
	reclaim = normalizeTaskScheduleOccurrenceReclaim(reclaim)
	if err := validateTaskScheduleOccurrenceReclaim(reclaim); err != nil {
		return TaskScheduleOccurrence{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	key := taskScheduleOccurrenceKey(reclaim.ScheduleID, reclaim.ScheduledFor)
	occurrence, ok := s.occurrences[key]
	if !ok || occurrence.Status != TaskScheduleOccurrenceClaimed || occurrence.ClaimedAt.After(reclaim.StaleBefore) {
		return TaskScheduleOccurrence{}, false, nil
	}
	occurrence.ClaimOwner = reclaim.ClaimOwner
	occurrence.ClaimedAt = reclaim.ClaimedAt
	s.occurrences[key] = occurrence
	return occurrence, true, nil
}

func (s *MemoryStore) RenewTaskScheduleOccurrence(_ context.Context, renewal TaskScheduleOccurrenceRenewal) (TaskScheduleOccurrence, bool, error) {
	renewal = normalizeTaskScheduleOccurrenceRenewal(renewal)
	if err := validateTaskScheduleOccurrenceRenewal(renewal); err != nil {
		return TaskScheduleOccurrence{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := taskScheduleOccurrenceKey(renewal.ScheduleID, renewal.ScheduledFor)
	occurrence, ok := s.occurrences[key]
	if !ok || occurrence.ID != renewal.OccurrenceID || occurrence.Status != TaskScheduleOccurrenceClaimed || occurrence.ClaimOwner != renewal.ClaimOwner {
		return TaskScheduleOccurrence{}, false, nil
	}
	if renewal.ClaimedAt.Before(occurrence.ClaimedAt) {
		return TaskScheduleOccurrence{}, false, nil
	}
	occurrence.ClaimedAt = renewal.ClaimedAt
	s.occurrences[key] = occurrence
	return occurrence, true, nil
}

func (s *MemoryStore) CompleteTaskScheduleOccurrence(_ context.Context, completion TaskScheduleOccurrenceCompletion) (TaskScheduleOccurrence, bool, error) {
	completion = normalizeTaskScheduleOccurrenceCompletion(completion)
	if err := validateTaskScheduleOccurrenceCompletion(completion); err != nil {
		return TaskScheduleOccurrence{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	key := taskScheduleOccurrenceKey(completion.ScheduleID, completion.ScheduledFor)
	occurrence, ok := s.occurrences[key]
	if !ok || occurrence.Status != TaskScheduleOccurrenceClaimed || occurrence.ClaimOwner != completion.ClaimOwner {
		return TaskScheduleOccurrence{}, false, nil
	}
	occurrence.Status = completion.Status
	occurrence.RunID = completion.RunID
	occurrence.Error = completion.Error
	occurrence.CompletedAt = completion.CompletedAt
	s.occurrences[key] = occurrence
	return occurrence, true, nil
}

func (s *MemoryStore) ListTaskScheduleOccurrences(_ context.Context, filter TaskScheduleOccurrenceFilter) ([]TaskScheduleOccurrence, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]TaskScheduleOccurrence, 0)
	for _, occurrence := range s.occurrences {
		if filter.ScheduleID != "" && occurrence.ScheduleID != filter.ScheduleID {
			continue
		}
		if filter.Status != "" && occurrence.Status != filter.Status {
			continue
		}
		if !filter.ClaimedBefore.IsZero() && occurrence.ClaimedAt.After(filter.ClaimedBefore.UTC()) {
			continue
		}
		items = append(items, occurrence)
	}
	sort.Slice(items, func(i, j int) bool {
		if !filter.ClaimedBefore.IsZero() {
			if items[i].ClaimedAt.Equal(items[j].ClaimedAt) {
				return items[i].ID < items[j].ID
			}
			return items[i].ClaimedAt.Before(items[j].ClaimedAt)
		}
		if items[i].ScheduledFor.Equal(items[j].ScheduledFor) {
			return items[i].ID < items[j].ID
		}
		return items[i].ScheduledFor.After(items[j].ScheduledFor)
	})
	if filter.Limit > 0 && len(items) > filter.Limit {
		items = items[:filter.Limit]
	}
	return items, nil
}

func (s *MemoryStore) PreflightTaskScheduleRunAdmission(_ context.Context, preflight TaskScheduleRunPreflight) (TaskScheduleRunPreflightResult, error) {
	preflight = normalizeTaskScheduleRunPreflight(preflight)
	if err := validateTaskScheduleRunPreflight(preflight); err != nil {
		return TaskScheduleRunPreflightResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	storedTask, ok := s.tasks[preflight.TaskID]
	if !ok {
		return TaskScheduleRunPreflightResult{}, fmt.Errorf("%w: %q", ErrTaskNotFound, preflight.TaskID)
	}
	key := taskScheduleOccurrenceKey(preflight.ScheduleID, preflight.ScheduledFor)
	occurrence, ok := s.occurrences[key]
	if !ok || occurrence.ID != preflight.ScheduleOccurrenceID || occurrence.TaskID != preflight.TaskID {
		return TaskScheduleRunPreflightResult{}, ErrScheduleOccurrenceClaimLost
	}
	if occurrence.Status == TaskScheduleOccurrenceStarted {
		run, ok := s.runs[occurrence.RunID]
		if !ok {
			return TaskScheduleRunPreflightResult{}, fmt.Errorf("started task schedule occurrence %q references missing run %q", occurrence.ID, occurrence.RunID)
		}
		if err := validateRunMatchesScheduleOccurrence(run, occurrence); err != nil {
			return TaskScheduleRunPreflightResult{}, err
		}
		return TaskScheduleRunPreflightResult{
			Task: cloneTask(storedTask), Run: run, Occurrence: occurrence, ExistingRun: true,
		}, nil
	}
	if occurrence.Status == TaskScheduleOccurrenceSkipped {
		return TaskScheduleRunPreflightResult{
			Task: cloneTask(storedTask), Occurrence: occurrence, Skipped: true,
		}, nil
	}
	if occurrence.Status != TaskScheduleOccurrenceClaimed || occurrence.ClaimOwner != preflight.ClaimOwner {
		return TaskScheduleRunPreflightResult{}, ErrScheduleOccurrenceClaimLost
	}
	if preflight.CompletedAt.Before(occurrence.ClaimedAt) {
		preflight.CompletedAt = occurrence.ClaimedAt
	}

	candidate := types.TaskRun{
		TaskID: preflight.TaskID, ScheduleID: preflight.ScheduleID,
		ScheduleOccurrenceID: preflight.ScheduleOccurrenceID, ScheduledFor: preflight.ScheduledFor,
	}
	allRuns := make([]types.TaskRun, 0, len(s.runs))
	for _, storedRun := range s.runs {
		allRuns = append(allRuns, storedRun)
	}
	if existing, found, err := findRunByScheduleOccurrence(allRuns, candidate); err != nil {
		return TaskScheduleRunPreflightResult{}, err
	} else if found {
		occurrence = startTaskScheduleOccurrence(occurrence, existing.ID, preflight.CompletedAt)
		s.occurrences[key] = occurrence
		return TaskScheduleRunPreflightResult{
			Task: cloneTask(storedTask), Run: existing, Occurrence: occurrence, ExistingRun: true,
		}, nil
	}
	for _, storedRun := range allRuns {
		if storedRun.TaskID == preflight.TaskID && !types.IsTerminalTaskRunStatus(storedRun.Status) {
			occurrence = skipTaskScheduleOccurrence(occurrence, preflight.CompletedAt)
			s.occurrences[key] = occurrence
			return TaskScheduleRunPreflightResult{
				Task: cloneTask(storedTask), Occurrence: occurrence, Skipped: true,
			}, nil
		}
	}
	return TaskScheduleRunPreflightResult{
		Task: cloneTask(storedTask), Occurrence: occurrence, Ready: true,
	}, nil
}

func (s *MemoryStore) ApplyTaskScheduleRunAdmission(_ context.Context, admission TaskScheduleRunAdmission) (TaskScheduleRunAdmissionResult, error) {
	admission = normalizeTaskScheduleRunAdmission(admission)
	if err := validateTaskScheduleRunAdmission(admission); err != nil {
		return TaskScheduleRunAdmissionResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	storedTask, ok := s.tasks[admission.Task.ID]
	if !ok {
		return TaskScheduleRunAdmissionResult{}, fmt.Errorf("%w: %q", ErrTaskNotFound, admission.Task.ID)
	}
	key := taskScheduleOccurrenceKey(admission.Run.ScheduleID, admission.Run.ScheduledFor)
	occurrence, ok := s.occurrences[key]
	if !ok || occurrence.ID != admission.Run.ScheduleOccurrenceID || occurrence.TaskID != admission.Task.ID {
		return TaskScheduleRunAdmissionResult{}, ErrScheduleOccurrenceClaimLost
	}
	if occurrence.Status == TaskScheduleOccurrenceStarted {
		run, ok := s.runs[occurrence.RunID]
		if !ok {
			return TaskScheduleRunAdmissionResult{}, fmt.Errorf("started task schedule occurrence %q references missing run %q", occurrence.ID, occurrence.RunID)
		}
		if err := validateRunMatchesScheduleOccurrence(run, occurrence); err != nil {
			return TaskScheduleRunAdmissionResult{}, err
		}
		return TaskScheduleRunAdmissionResult{
			Task: cloneTask(storedTask), Run: run, Occurrence: occurrence, ExistingRun: true,
		}, nil
	}
	if occurrence.Status == TaskScheduleOccurrenceSkipped {
		return TaskScheduleRunAdmissionResult{
			Task: cloneTask(storedTask), Occurrence: occurrence, Skipped: true,
		}, nil
	}
	if occurrence.Status != TaskScheduleOccurrenceClaimed || occurrence.ClaimOwner != admission.ClaimOwner {
		return TaskScheduleRunAdmissionResult{}, ErrScheduleOccurrenceClaimLost
	}
	admission = alignTaskScheduleRunAdmissionTime(admission, occurrence)

	allRuns := make([]types.TaskRun, 0, len(s.runs))
	taskRuns := make([]types.TaskRun, 0)
	maxRunNumber := 0
	for _, storedRun := range s.runs {
		allRuns = append(allRuns, storedRun)
		if storedRun.TaskID != admission.Task.ID {
			continue
		}
		taskRuns = append(taskRuns, storedRun)
		if storedRun.Number > maxRunNumber {
			maxRunNumber = storedRun.Number
		}
	}
	if existing, found, err := findRunByScheduleOccurrence(allRuns, admission.Run); err != nil {
		return TaskScheduleRunAdmissionResult{}, err
	} else if found {
		occurrence = startTaskScheduleOccurrence(occurrence, existing.ID, admission.CompletedAt)
		s.occurrences[key] = occurrence
		return TaskScheduleRunAdmissionResult{
			Task: cloneTask(storedTask), Run: existing, Occurrence: occurrence, ExistingRun: true,
		}, nil
	}
	for _, storedRun := range taskRuns {
		if !types.IsTerminalTaskRunStatus(storedRun.Status) {
			occurrence = skipTaskScheduleOccurrence(occurrence, admission.CompletedAt)
			s.occurrences[key] = occurrence
			return TaskScheduleRunAdmissionResult{
				Task: cloneTask(storedTask), Occurrence: occurrence, Skipped: true,
			}, nil
		}
	}
	if _, exists := s.runs[admission.Run.ID]; exists {
		return TaskScheduleRunAdmissionResult{}, fmt.Errorf("run %q already exists", admission.Run.ID)
	}
	task, err := mergeRunStartTask(storedTask, admission.Task, admission.BudgetMicrosUSD)
	if err != nil {
		return TaskScheduleRunAdmissionResult{}, err
	}
	run := admission.Run
	run.Number = maxRunNumber + 1
	if admission.Approval != nil {
		if _, exists := s.approvals[admission.Approval.ID]; exists {
			return TaskScheduleRunAdmissionResult{}, fmt.Errorf("approval %q already exists", admission.Approval.ID)
		}
	}
	occurrence = startTaskScheduleOccurrence(occurrence, run.ID, admission.CompletedAt)
	s.tasks[task.ID] = cloneTask(task)
	s.runs[run.ID] = run
	if admission.Approval != nil {
		s.approvals[admission.Approval.ID] = cloneApproval(*admission.Approval)
	}
	for _, event := range taskScheduleRunInitialEvents(admission, run) {
		s.appendRunEventLocked(event)
	}
	s.occurrences[key] = occurrence
	s.signalRun(run.ID)
	return TaskScheduleRunAdmissionResult{
		Task: cloneTask(task), Run: run, Occurrence: occurrence, Applied: true,
	}, nil
}

func normalizeTaskScheduleRunPreflight(preflight TaskScheduleRunPreflight) TaskScheduleRunPreflight {
	preflight.TaskID = strings.TrimSpace(preflight.TaskID)
	preflight.ScheduleID = strings.TrimSpace(preflight.ScheduleID)
	preflight.ScheduleOccurrenceID = strings.TrimSpace(preflight.ScheduleOccurrenceID)
	preflight.ClaimOwner = strings.TrimSpace(preflight.ClaimOwner)
	preflight.ScheduledFor = preflight.ScheduledFor.UTC()
	if preflight.CompletedAt.IsZero() {
		preflight.CompletedAt = time.Now().UTC()
	} else {
		preflight.CompletedAt = preflight.CompletedAt.UTC()
	}
	return preflight
}

func validateTaskScheduleRunPreflight(preflight TaskScheduleRunPreflight) error {
	if preflight.TaskID == "" || preflight.ScheduleID == "" || preflight.ScheduleOccurrenceID == "" ||
		preflight.ScheduledFor.IsZero() || preflight.ClaimOwner == "" {
		return fmt.Errorf("task id, schedule id, occurrence id, scheduled time, and claim owner are required")
	}
	return nil
}

func normalizeTaskScheduleRunAdmission(admission TaskScheduleRunAdmission) TaskScheduleRunAdmission {
	admission.ClaimOwner = strings.TrimSpace(admission.ClaimOwner)
	admission.Run.ScheduledFor = admission.Run.ScheduledFor.UTC()
	if admission.CompletedAt.IsZero() {
		admission.CompletedAt = time.Now().UTC()
	} else {
		admission.CompletedAt = admission.CompletedAt.UTC()
	}
	if admission.Approval != nil {
		approval := cloneApproval(*admission.Approval)
		approval.ID = strings.TrimSpace(approval.ID)
		approval.TaskID = strings.TrimSpace(approval.TaskID)
		approval.RunID = strings.TrimSpace(approval.RunID)
		approval.Status = strings.TrimSpace(approval.Status)
		if approval.CreatedAt.IsZero() {
			approval.CreatedAt = admission.CompletedAt
		} else {
			approval.CreatedAt = approval.CreatedAt.UTC()
		}
		admission.Approval = &approval
	}
	return admission
}

func validateTaskScheduleRunAdmission(admission TaskScheduleRunAdmission) error {
	if admission.ClaimOwner == "" {
		return fmt.Errorf("task schedule occurrence claim owner is required")
	}
	if err := validateRunStartTransition(RunStartTransition{
		Task: admission.Task, Run: admission.Run, BudgetMicrosUSD: admission.BudgetMicrosUSD,
	}); err != nil {
		return err
	}
	return validateTaskScheduleRunInitialEffects(admission)
}

func validateRunMatchesScheduleOccurrence(run types.TaskRun, occurrence TaskScheduleOccurrence) error {
	if err := validateRunScheduleProvenance(run); err != nil {
		return fmt.Errorf("stored run %q has invalid schedule provenance: %w", run.ID, err)
	}
	if run.TaskID != occurrence.TaskID ||
		run.ScheduleID != occurrence.ScheduleID ||
		run.ScheduleOccurrenceID != occurrence.ID ||
		!run.ScheduledFor.Equal(occurrence.ScheduledFor) {
		return fmt.Errorf("run %q does not match task schedule occurrence %q", run.ID, occurrence.ID)
	}
	return nil
}

func startTaskScheduleOccurrence(occurrence TaskScheduleOccurrence, runID string, completedAt time.Time) TaskScheduleOccurrence {
	occurrence.Status = TaskScheduleOccurrenceStarted
	occurrence.RunID = runID
	occurrence.Error = ""
	occurrence.CompletedAt = completedAt
	return occurrence
}

func skipTaskScheduleOccurrence(occurrence TaskScheduleOccurrence, completedAt time.Time) TaskScheduleOccurrence {
	occurrence.Status = TaskScheduleOccurrenceSkipped
	occurrence.RunID = ""
	occurrence.Error = ErrActiveRun.Error()
	occurrence.CompletedAt = completedAt
	return occurrence
}

func normalizeTaskSchedule(schedule TaskSchedule, now time.Time) TaskSchedule {
	schedule.ID = strings.TrimSpace(schedule.ID)
	schedule.TaskID = strings.TrimSpace(schedule.TaskID)
	schedule.Kind = strings.TrimSpace(schedule.Kind)
	schedule.CronExpression = strings.TrimSpace(schedule.CronExpression)
	schedule.Timezone = strings.TrimSpace(schedule.Timezone)
	schedule.RunAt = schedule.RunAt.UTC()
	schedule.NextRunAt = schedule.NextRunAt.UTC()
	if schedule.CreatedAt.IsZero() {
		schedule.CreatedAt = now
	} else {
		schedule.CreatedAt = schedule.CreatedAt.UTC()
	}
	if schedule.UpdatedAt.IsZero() {
		schedule.UpdatedAt = now
	} else {
		schedule.UpdatedAt = schedule.UpdatedAt.UTC()
	}
	if schedule.Revision < 1 {
		schedule.Revision = 1
	}
	return schedule
}

func validateTaskSchedule(schedule TaskSchedule) error {
	if schedule.ID == "" {
		return fmt.Errorf("task schedule id is required")
	}
	if schedule.TaskID == "" {
		return fmt.Errorf("task schedule task id is required")
	}
	if schedule.Kind != TaskScheduleKindOnce && schedule.Kind != TaskScheduleKindCron {
		return fmt.Errorf("task schedule kind must be %q or %q", TaskScheduleKindOnce, TaskScheduleKindCron)
	}
	if schedule.Enabled && schedule.NextRunAt.IsZero() {
		return fmt.Errorf("enabled task schedule next run time is required")
	}
	if schedule.Revision < 1 {
		return fmt.Errorf("task schedule revision must be positive")
	}
	return nil
}

func normalizeTaskScheduleOccurrenceClaim(claim TaskScheduleOccurrenceClaim) TaskScheduleOccurrenceClaim {
	claim.OccurrenceID = strings.TrimSpace(claim.OccurrenceID)
	claim.ScheduleID = strings.TrimSpace(claim.ScheduleID)
	claim.ClaimOwner = strings.TrimSpace(claim.ClaimOwner)
	claim.ScheduledFor = claim.ScheduledFor.UTC()
	claim.NextRunAt = claim.NextRunAt.UTC()
	if claim.ClaimedAt.IsZero() {
		claim.ClaimedAt = time.Now().UTC()
	} else {
		claim.ClaimedAt = claim.ClaimedAt.UTC()
	}
	return claim
}

func validateTaskScheduleOccurrenceClaim(claim TaskScheduleOccurrenceClaim) error {
	if claim.OccurrenceID == "" {
		return fmt.Errorf("task schedule occurrence id is required")
	}
	if claim.ScheduleID == "" {
		return fmt.Errorf("task schedule id is required")
	}
	if claim.ExpectedScheduleRevision < 1 {
		return fmt.Errorf("expected task schedule revision must be positive")
	}
	if claim.ScheduledFor.IsZero() {
		return fmt.Errorf("scheduled time is required")
	}
	if claim.ClaimOwner == "" {
		return fmt.Errorf("claim owner is required")
	}
	return nil
}

func normalizeTaskScheduleOccurrenceReclaim(reclaim TaskScheduleOccurrenceReclaim) TaskScheduleOccurrenceReclaim {
	reclaim.ScheduleID = strings.TrimSpace(reclaim.ScheduleID)
	reclaim.ClaimOwner = strings.TrimSpace(reclaim.ClaimOwner)
	reclaim.ScheduledFor = reclaim.ScheduledFor.UTC()
	reclaim.StaleBefore = reclaim.StaleBefore.UTC()
	if reclaim.ClaimedAt.IsZero() {
		reclaim.ClaimedAt = time.Now().UTC()
	} else {
		reclaim.ClaimedAt = reclaim.ClaimedAt.UTC()
	}
	return reclaim
}

func validateTaskScheduleOccurrenceReclaim(reclaim TaskScheduleOccurrenceReclaim) error {
	if reclaim.ScheduleID == "" || reclaim.ScheduledFor.IsZero() || reclaim.StaleBefore.IsZero() || reclaim.ClaimOwner == "" {
		return fmt.Errorf("schedule id, scheduled time, stale cutoff, and claim owner are required")
	}
	if !reclaim.ClaimedAt.After(reclaim.StaleBefore) {
		return fmt.Errorf("reclaimed claim time must be after the stale cutoff")
	}
	return nil
}

func normalizeTaskScheduleOccurrenceRenewal(renewal TaskScheduleOccurrenceRenewal) TaskScheduleOccurrenceRenewal {
	renewal.OccurrenceID = strings.TrimSpace(renewal.OccurrenceID)
	renewal.ScheduleID = strings.TrimSpace(renewal.ScheduleID)
	renewal.ClaimOwner = strings.TrimSpace(renewal.ClaimOwner)
	renewal.ScheduledFor = renewal.ScheduledFor.UTC()
	if renewal.ClaimedAt.IsZero() {
		renewal.ClaimedAt = time.Now().UTC()
	} else {
		renewal.ClaimedAt = renewal.ClaimedAt.UTC()
	}
	return renewal
}

func validateTaskScheduleOccurrenceRenewal(renewal TaskScheduleOccurrenceRenewal) error {
	if renewal.OccurrenceID == "" || renewal.ScheduleID == "" || renewal.ScheduledFor.IsZero() || renewal.ClaimOwner == "" {
		return fmt.Errorf("occurrence id, schedule id, scheduled time, and claim owner are required")
	}
	return nil
}

func normalizeTaskScheduleOccurrenceCompletion(completion TaskScheduleOccurrenceCompletion) TaskScheduleOccurrenceCompletion {
	completion.ScheduleID = strings.TrimSpace(completion.ScheduleID)
	completion.ClaimOwner = strings.TrimSpace(completion.ClaimOwner)
	completion.Status = strings.TrimSpace(completion.Status)
	completion.RunID = strings.TrimSpace(completion.RunID)
	completion.ScheduledFor = completion.ScheduledFor.UTC()
	if completion.CompletedAt.IsZero() {
		completion.CompletedAt = time.Now().UTC()
	} else {
		completion.CompletedAt = completion.CompletedAt.UTC()
	}
	return completion
}

func validateTaskScheduleOccurrenceCompletion(completion TaskScheduleOccurrenceCompletion) error {
	if completion.ScheduleID == "" || completion.ScheduledFor.IsZero() || completion.ClaimOwner == "" {
		return fmt.Errorf("schedule id, scheduled time, and claim owner are required")
	}
	switch completion.Status {
	case TaskScheduleOccurrenceStarted, TaskScheduleOccurrenceSkipped, TaskScheduleOccurrenceFailed:
	default:
		return fmt.Errorf("task schedule occurrence completion status is invalid")
	}
	if completion.Status == TaskScheduleOccurrenceStarted && completion.RunID == "" {
		return fmt.Errorf("started task schedule occurrence run id is required")
	}
	return nil
}

func taskScheduleOccurrenceKey(scheduleID string, scheduledFor time.Time) string {
	return scheduleID + "\x00" + scheduledFor.UTC().Format(time.RFC3339Nano)
}
