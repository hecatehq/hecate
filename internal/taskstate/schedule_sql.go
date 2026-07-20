package taskstate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/storage"
	"github.com/hecatehq/hecate/pkg/types"
)

func (s *SQLiteStore) CompareAndSwapTaskSchedule(ctx context.Context, mutation TaskScheduleCompareAndSwap) (TaskSchedule, bool, error) {
	if mutation.ExpectedRevision < 0 {
		return TaskSchedule{}, false, fmt.Errorf("expected task schedule revision must be non-negative")
	}
	schedule := normalizeTaskSchedule(mutation.Schedule, time.Now().UTC())
	if err := validateTaskSchedule(schedule); err != nil {
		return TaskSchedule{}, false, err
	}
	var row *sql.Row
	if mutation.ExpectedRevision == 0 {
		row = s.db.QueryRowContext(ctx, fmt.Sprintf(`
			INSERT INTO %s (
				id, task_id, kind, cron_expression, timezone, run_at, enabled,
				next_run_at, created_at, updated_at, revision
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)
			ON CONFLICT (task_id) DO NOTHING
			RETURNING id, task_id, kind, cron_expression, timezone, run_at,
				enabled, next_run_at, created_at, updated_at, revision
		`, s.schedulesTable),
			schedule.ID,
			schedule.TaskID,
			schedule.Kind,
			schedule.CronExpression,
			schedule.Timezone,
			s.taskScheduleTimeValue(schedule.RunAt),
			schedule.Enabled,
			s.taskScheduleTimeValue(schedule.NextRunAt),
			s.taskScheduleTimeValue(schedule.CreatedAt),
			s.taskScheduleTimeValue(schedule.UpdatedAt),
		)
	} else {
		row = s.db.QueryRowContext(ctx, fmt.Sprintf(`
			UPDATE %s
			SET kind = ?, cron_expression = ?, timezone = ?, run_at = ?, enabled = ?,
				next_run_at = ?, updated_at = ?, revision = revision + 1
			WHERE task_id = ? AND revision = ?
			RETURNING id, task_id, kind, cron_expression, timezone, run_at,
				enabled, next_run_at, created_at, updated_at, revision
		`, s.schedulesTable),
			schedule.Kind,
			schedule.CronExpression,
			schedule.Timezone,
			s.taskScheduleTimeValue(schedule.RunAt),
			schedule.Enabled,
			s.taskScheduleTimeValue(schedule.NextRunAt),
			s.taskScheduleTimeValue(schedule.UpdatedAt),
			schedule.TaskID,
			mutation.ExpectedRevision,
		)
	}
	stored, err := scanTaskSchedule(row)
	if err == nil {
		return stored, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return TaskSchedule{}, false, err
	}
	current, found, err := s.GetTaskScheduleByTask(ctx, schedule.TaskID)
	if err != nil {
		return TaskSchedule{}, false, err
	}
	if !found {
		return TaskSchedule{}, false, nil
	}
	return current, false, nil
}

func (s *SQLiteStore) GetTaskSchedule(ctx context.Context, id string) (TaskSchedule, bool, error) {
	return s.getTaskSchedule(ctx, s.db, "id = ?", strings.TrimSpace(id), false)
}

func (s *SQLiteStore) GetTaskScheduleByTask(ctx context.Context, taskID string) (TaskSchedule, bool, error) {
	return s.getTaskSchedule(ctx, s.db, "task_id = ?", strings.TrimSpace(taskID), false)
}

type taskScheduleQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *SQLiteStore) getTaskSchedule(
	ctx context.Context,
	querier taskScheduleQuerier,
	where string,
	arg any,
	forUpdate bool,
) (TaskSchedule, bool, error) {
	query := fmt.Sprintf(`
		SELECT id, task_id, kind, cron_expression, timezone, run_at,
			enabled, next_run_at, created_at, updated_at, revision
		FROM %s
		WHERE %s
	`, s.schedulesTable, where)
	if forUpdate && s.client.Dialect() == storage.DialectPostgres {
		query += " FOR UPDATE"
	}
	schedule, err := scanTaskSchedule(querier.QueryRowContext(ctx, query, arg))
	if errors.Is(err, sql.ErrNoRows) {
		return TaskSchedule{}, false, nil
	}
	if err != nil {
		return TaskSchedule{}, false, err
	}
	return schedule, true, nil
}

func (s *SQLiteStore) ListTaskSchedules(ctx context.Context, filter TaskScheduleFilter) ([]TaskSchedule, error) {
	args := make([]any, 0, len(filter.TaskIDs)+2)
	where := []string{"1=1"}
	if len(filter.TaskIDs) > 0 {
		where = append(where, "task_id IN ("+sqlitePlaceholders(len(filter.TaskIDs))+")")
		for _, taskID := range filter.TaskIDs {
			args = append(args, taskID)
		}
	}
	if filter.Enabled != nil {
		args = append(args, *filter.Enabled)
		where = append(where, "enabled = ?")
	}
	limitSQL := ""
	if filter.Limit > 0 {
		args = append(args, filter.Limit)
		limitSQL = "LIMIT ?"
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT id, task_id, kind, cron_expression, timezone, run_at,
			enabled, next_run_at, created_at, updated_at, revision
		FROM %s
		WHERE %s
		ORDER BY created_at DESC, id ASC
		%s
	`, s.schedulesTable, strings.Join(where, " AND "), limitSQL), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTaskSchedules(rows)
}

func (s *SQLiteStore) DeleteTaskSchedule(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("task schedule id is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE schedule_id = ?`, s.occurrencesTable), id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id = ?`, s.schedulesTable), id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) ListDueTaskSchedules(ctx context.Context, dueAt time.Time, limit int) ([]TaskSchedule, error) {
	if dueAt.IsZero() {
		return nil, fmt.Errorf("due time is required")
	}
	args := []any{true, s.taskScheduleTimeValue(dueAt.UTC())}
	limitSQL := ""
	if limit > 0 {
		args = append(args, limit)
		limitSQL = "LIMIT ?"
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT id, task_id, kind, cron_expression, timezone, run_at,
			enabled, next_run_at, created_at, updated_at, revision
		FROM %s
		WHERE enabled = ? AND next_run_at IS NOT NULL AND next_run_at <= ?
		ORDER BY next_run_at ASC, id ASC
		%s
	`, s.schedulesTable, limitSQL), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTaskSchedules(rows)
}

func (s *SQLiteStore) ClaimTaskScheduleOccurrence(ctx context.Context, claim TaskScheduleOccurrenceClaim) (TaskScheduleOccurrence, bool, error) {
	claim = normalizeTaskScheduleOccurrenceClaim(claim)
	if err := validateTaskScheduleOccurrenceClaim(claim); err != nil {
		return TaskScheduleOccurrence{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TaskScheduleOccurrence{}, false, err
	}
	defer func() { _ = tx.Rollback() }()

	parentTaskID, parentFound, err := s.lockTaskScheduleClaimParentTx(ctx, tx, claim.ScheduleID)
	if err != nil {
		return TaskScheduleOccurrence{}, false, err
	}
	if !parentFound {
		return TaskScheduleOccurrence{}, false, nil
	}
	schedule, found, err := s.getTaskSchedule(ctx, tx, "id = ?", claim.ScheduleID, true)
	if err != nil {
		return TaskScheduleOccurrence{}, false, err
	}
	if parentTaskID != "" && found && schedule.TaskID != parentTaskID {
		return TaskScheduleOccurrence{}, false, nil
	}
	if !found || schedule.Revision != claim.ExpectedScheduleRevision || !schedule.Enabled || !schedule.NextRunAt.Equal(claim.ScheduledFor) {
		return TaskScheduleOccurrence{}, false, nil
	}
	var exists int
	err = tx.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT 1 FROM %s WHERE schedule_id = ? AND scheduled_for = ?
	`, s.occurrencesTable), claim.ScheduleID, s.taskScheduleTimeValue(claim.ScheduledFor)).Scan(&exists)
	if err == nil {
		return TaskScheduleOccurrence{}, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return TaskScheduleOccurrence{}, false, err
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
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (
			id, task_id, schedule_id, scheduled_for, status, claim_owner, claimed_at,
			run_id, error, completed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, '', '', NULL)
	`, s.occurrencesTable),
		occurrence.ID,
		occurrence.TaskID,
		occurrence.ScheduleID,
		s.taskScheduleTimeValue(occurrence.ScheduledFor),
		occurrence.Status,
		occurrence.ClaimOwner,
		s.taskScheduleTimeValue(occurrence.ClaimedAt),
	); err != nil {
		return TaskScheduleOccurrence{}, false, err
	}
	nextEnabled := !claim.NextRunAt.IsZero()
	result, err := tx.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s
		SET enabled = ?, next_run_at = ?, updated_at = ?, revision = revision + 1
		WHERE id = ? AND revision = ? AND enabled = ? AND next_run_at = ?
	`, s.schedulesTable),
		nextEnabled,
		s.taskScheduleTimeValue(claim.NextRunAt),
		s.taskScheduleTimeValue(claim.ClaimedAt),
		claim.ScheduleID,
		claim.ExpectedScheduleRevision,
		true,
		s.taskScheduleTimeValue(claim.ScheduledFor),
	)
	if err != nil {
		return TaskScheduleOccurrence{}, false, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return TaskScheduleOccurrence{}, false, err
	}
	if updated != 1 {
		return TaskScheduleOccurrence{}, false, nil
	}
	if err := tx.Commit(); err != nil {
		return TaskScheduleOccurrence{}, false, err
	}
	return occurrence, true, nil
}

// lockTaskScheduleClaimParentTx gives PostgreSQL the same Task-before-Schedule
// lock order used by Task deletion and Run admission. Reading the Schedule in
// the join does not lock it; FOR UPDATE OF targets only the parent Task. SQLite
// already serializes writers at BeginTx and must keep its existing query path.
func (s *SQLiteStore) lockTaskScheduleClaimParentTx(
	ctx context.Context,
	tx storage.Tx,
	scheduleID string,
) (string, bool, error) {
	if s.client == nil || s.client.Dialect() != storage.DialectPostgres {
		return "", true, nil
	}
	var taskID string
	err := tx.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT task_row.id
		FROM %s AS task_row
		JOIN %s AS schedule_row ON schedule_row.task_id = task_row.id
		WHERE schedule_row.id = ?
		FOR UPDATE OF task_row
	`, s.tasksTable, s.schedulesTable), scheduleID).Scan(&taskID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return taskID, true, nil
}

func (s *SQLiteStore) ReclaimTaskScheduleOccurrence(ctx context.Context, reclaim TaskScheduleOccurrenceReclaim) (TaskScheduleOccurrence, bool, error) {
	reclaim = normalizeTaskScheduleOccurrenceReclaim(reclaim)
	if err := validateTaskScheduleOccurrenceReclaim(reclaim); err != nil {
		return TaskScheduleOccurrence{}, false, err
	}
	row := s.db.QueryRowContext(ctx, fmt.Sprintf(`
		UPDATE %s
		SET claim_owner = ?, claimed_at = ?
		WHERE schedule_id = ?
			AND scheduled_for = ?
			AND status = ?
			AND claimed_at <= ?
		RETURNING id, task_id, schedule_id, scheduled_for, status, claim_owner, claimed_at,
			run_id, error, completed_at
	`, s.occurrencesTable),
		reclaim.ClaimOwner,
		s.taskScheduleTimeValue(reclaim.ClaimedAt),
		reclaim.ScheduleID,
		s.taskScheduleTimeValue(reclaim.ScheduledFor),
		TaskScheduleOccurrenceClaimed,
		s.taskScheduleTimeValue(reclaim.StaleBefore),
	)
	occurrence, err := scanTaskScheduleOccurrence(row)
	if errors.Is(err, sql.ErrNoRows) {
		return TaskScheduleOccurrence{}, false, nil
	}
	if err != nil {
		return TaskScheduleOccurrence{}, false, err
	}
	return occurrence, true, nil
}

func (s *SQLiteStore) RenewTaskScheduleOccurrence(ctx context.Context, renewal TaskScheduleOccurrenceRenewal) (TaskScheduleOccurrence, bool, error) {
	renewal = normalizeTaskScheduleOccurrenceRenewal(renewal)
	if err := validateTaskScheduleOccurrenceRenewal(renewal); err != nil {
		return TaskScheduleOccurrence{}, false, err
	}
	row := s.db.QueryRowContext(ctx, fmt.Sprintf(`
		UPDATE %s
		SET claimed_at = ?
		WHERE id = ?
			AND schedule_id = ?
			AND scheduled_for = ?
			AND status = ?
			AND claim_owner = ?
			AND claimed_at <= ?
		RETURNING id, task_id, schedule_id, scheduled_for, status, claim_owner, claimed_at,
			run_id, error, completed_at
	`, s.occurrencesTable),
		s.taskScheduleTimeValue(renewal.ClaimedAt),
		renewal.OccurrenceID,
		renewal.ScheduleID,
		s.taskScheduleTimeValue(renewal.ScheduledFor),
		TaskScheduleOccurrenceClaimed,
		renewal.ClaimOwner,
		s.taskScheduleTimeValue(renewal.ClaimedAt),
	)
	occurrence, err := scanTaskScheduleOccurrence(row)
	if errors.Is(err, sql.ErrNoRows) {
		return TaskScheduleOccurrence{}, false, nil
	}
	if err != nil {
		return TaskScheduleOccurrence{}, false, err
	}
	return occurrence, true, nil
}

func (s *SQLiteStore) CompleteTaskScheduleOccurrence(ctx context.Context, completion TaskScheduleOccurrenceCompletion) (TaskScheduleOccurrence, bool, error) {
	completion = normalizeTaskScheduleOccurrenceCompletion(completion)
	if err := validateTaskScheduleOccurrenceCompletion(completion); err != nil {
		return TaskScheduleOccurrence{}, false, err
	}
	row := s.db.QueryRowContext(ctx, fmt.Sprintf(`
		UPDATE %s
		SET status = ?, run_id = ?, error = ?, completed_at = ?
		WHERE schedule_id = ?
			AND scheduled_for = ?
			AND status = ?
			AND claim_owner = ?
		RETURNING id, task_id, schedule_id, scheduled_for, status, claim_owner, claimed_at,
			run_id, error, completed_at
	`, s.occurrencesTable),
		completion.Status,
		completion.RunID,
		completion.Error,
		s.taskScheduleTimeValue(completion.CompletedAt),
		completion.ScheduleID,
		s.taskScheduleTimeValue(completion.ScheduledFor),
		TaskScheduleOccurrenceClaimed,
		completion.ClaimOwner,
	)
	occurrence, err := scanTaskScheduleOccurrence(row)
	if errors.Is(err, sql.ErrNoRows) {
		return TaskScheduleOccurrence{}, false, nil
	}
	if err != nil {
		return TaskScheduleOccurrence{}, false, err
	}
	return occurrence, true, nil
}

func (s *SQLiteStore) ListTaskScheduleOccurrences(ctx context.Context, filter TaskScheduleOccurrenceFilter) ([]TaskScheduleOccurrence, error) {
	args := make([]any, 0, 2)
	where := []string{"1=1"}
	if filter.ScheduleID != "" {
		args = append(args, strings.TrimSpace(filter.ScheduleID))
		where = append(where, "schedule_id = ?")
	}
	if filter.Status != "" {
		args = append(args, strings.TrimSpace(filter.Status))
		where = append(where, "status = ?")
	}
	if !filter.ClaimedBefore.IsZero() {
		args = append(args, s.taskScheduleTimeValue(filter.ClaimedBefore.UTC()))
		where = append(where, "claimed_at <= ?")
	}
	limitSQL := ""
	if filter.Limit > 0 {
		args = append(args, filter.Limit)
		limitSQL = "LIMIT ?"
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT id, task_id, schedule_id, scheduled_for, status, claim_owner, claimed_at,
			run_id, error, completed_at
		FROM %s
		WHERE %s
		ORDER BY %s
		%s
	`, s.occurrencesTable, strings.Join(where, " AND "), taskScheduleOccurrenceOrder(filter), limitSQL), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]TaskScheduleOccurrence, 0)
	for rows.Next() {
		occurrence, err := scanTaskScheduleOccurrence(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, occurrence)
	}
	return items, rows.Err()
}

func (s *SQLiteStore) PreflightTaskScheduleRunAdmission(ctx context.Context, preflight TaskScheduleRunPreflight) (TaskScheduleRunPreflightResult, error) {
	preflight = normalizeTaskScheduleRunPreflight(preflight)
	if err := validateTaskScheduleRunPreflight(preflight); err != nil {
		return TaskScheduleRunPreflightResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TaskScheduleRunPreflightResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	storedTask, err := s.getTaskForRunStartTx(ctx, tx, preflight.TaskID)
	if err != nil {
		return TaskScheduleRunPreflightResult{}, err
	}
	occurrence, found, err := s.getTaskScheduleOccurrenceForAdmissionTx(
		ctx, tx, preflight.ScheduleID, preflight.ScheduledFor,
	)
	if err != nil {
		return TaskScheduleRunPreflightResult{}, err
	}
	if !found || occurrence.ID != preflight.ScheduleOccurrenceID || occurrence.TaskID != preflight.TaskID {
		return TaskScheduleRunPreflightResult{}, ErrScheduleOccurrenceClaimLost
	}
	if occurrence.Status == TaskScheduleOccurrenceStarted {
		run, found, err := s.getTaskRunForStartTx(ctx, tx, occurrence.TaskID, occurrence.RunID)
		if err != nil {
			return TaskScheduleRunPreflightResult{}, err
		}
		if !found {
			return TaskScheduleRunPreflightResult{}, fmt.Errorf("started task schedule occurrence %q references missing run %q", occurrence.ID, occurrence.RunID)
		}
		if err := validateRunMatchesScheduleOccurrence(run, occurrence); err != nil {
			return TaskScheduleRunPreflightResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return TaskScheduleRunPreflightResult{}, err
		}
		return TaskScheduleRunPreflightResult{
			Task: storedTask, Run: run, Occurrence: occurrence, ExistingRun: true,
		}, nil
	}
	if occurrence.Status == TaskScheduleOccurrenceSkipped {
		if err := tx.Commit(); err != nil {
			return TaskScheduleRunPreflightResult{}, err
		}
		return TaskScheduleRunPreflightResult{
			Task: storedTask, Occurrence: occurrence, Skipped: true,
		}, nil
	}
	if occurrence.Status != TaskScheduleOccurrenceClaimed || occurrence.ClaimOwner != preflight.ClaimOwner {
		return TaskScheduleRunPreflightResult{}, ErrScheduleOccurrenceClaimLost
	}
	if preflight.CompletedAt.Before(occurrence.ClaimedAt) {
		preflight.CompletedAt = occurrence.ClaimedAt
	}

	taskRuns, err := s.listTaskRunsForStartTx(ctx, tx, preflight.TaskID)
	if err != nil {
		return TaskScheduleRunPreflightResult{}, err
	}
	candidate := types.TaskRun{
		TaskID: preflight.TaskID, ScheduleID: preflight.ScheduleID,
		ScheduleOccurrenceID: preflight.ScheduleOccurrenceID, ScheduledFor: preflight.ScheduledFor,
	}
	if existing, found, err := findRunByScheduleOccurrence(taskRuns, candidate); err != nil {
		return TaskScheduleRunPreflightResult{}, err
	} else if found {
		occurrence = startTaskScheduleOccurrence(occurrence, existing.ID, preflight.CompletedAt)
		if err := s.updateTaskScheduleOccurrenceAdmissionTx(ctx, tx, occurrence, preflight.ClaimOwner); err != nil {
			return TaskScheduleRunPreflightResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return TaskScheduleRunPreflightResult{}, err
		}
		return TaskScheduleRunPreflightResult{
			Task: storedTask, Run: existing, Occurrence: occurrence, ExistingRun: true,
		}, nil
	}
	for _, storedRun := range taskRuns {
		if !types.IsTerminalTaskRunStatus(storedRun.Status) {
			occurrence = skipTaskScheduleOccurrence(occurrence, preflight.CompletedAt)
			if err := s.updateTaskScheduleOccurrenceAdmissionTx(ctx, tx, occurrence, preflight.ClaimOwner); err != nil {
				return TaskScheduleRunPreflightResult{}, err
			}
			if err := tx.Commit(); err != nil {
				return TaskScheduleRunPreflightResult{}, err
			}
			return TaskScheduleRunPreflightResult{
				Task: storedTask, Occurrence: occurrence, Skipped: true,
			}, nil
		}
	}
	if err := tx.Commit(); err != nil {
		return TaskScheduleRunPreflightResult{}, err
	}
	return TaskScheduleRunPreflightResult{
		Task: storedTask, Occurrence: occurrence, Ready: true,
	}, nil
}

func (s *SQLiteStore) ApplyTaskScheduleRunAdmission(ctx context.Context, admission TaskScheduleRunAdmission) (TaskScheduleRunAdmissionResult, error) {
	admission = normalizeTaskScheduleRunAdmission(admission)
	if err := validateTaskScheduleRunAdmission(admission); err != nil {
		return TaskScheduleRunAdmissionResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TaskScheduleRunAdmissionResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	storedTask, err := s.getTaskForRunStartTx(ctx, tx, admission.Task.ID)
	if err != nil {
		return TaskScheduleRunAdmissionResult{}, err
	}
	occurrence, found, err := s.getTaskScheduleOccurrenceForAdmissionTx(
		ctx, tx, admission.Run.ScheduleID, admission.Run.ScheduledFor,
	)
	if err != nil {
		return TaskScheduleRunAdmissionResult{}, err
	}
	if !found || occurrence.ID != admission.Run.ScheduleOccurrenceID || occurrence.TaskID != admission.Task.ID {
		return TaskScheduleRunAdmissionResult{}, ErrScheduleOccurrenceClaimLost
	}
	if occurrence.Status == TaskScheduleOccurrenceStarted {
		run, found, err := s.getTaskRunForStartTx(ctx, tx, occurrence.TaskID, occurrence.RunID)
		if err != nil {
			return TaskScheduleRunAdmissionResult{}, err
		}
		if !found {
			return TaskScheduleRunAdmissionResult{}, fmt.Errorf("started task schedule occurrence %q references missing run %q", occurrence.ID, occurrence.RunID)
		}
		if err := validateRunMatchesScheduleOccurrence(run, occurrence); err != nil {
			return TaskScheduleRunAdmissionResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return TaskScheduleRunAdmissionResult{}, err
		}
		return TaskScheduleRunAdmissionResult{
			Task: storedTask, Run: run, Occurrence: occurrence, ExistingRun: true,
		}, nil
	}
	if occurrence.Status == TaskScheduleOccurrenceSkipped {
		if err := tx.Commit(); err != nil {
			return TaskScheduleRunAdmissionResult{}, err
		}
		return TaskScheduleRunAdmissionResult{
			Task: storedTask, Occurrence: occurrence, Skipped: true,
		}, nil
	}
	if occurrence.Status != TaskScheduleOccurrenceClaimed || occurrence.ClaimOwner != admission.ClaimOwner {
		return TaskScheduleRunAdmissionResult{}, ErrScheduleOccurrenceClaimLost
	}
	admission = alignTaskScheduleRunAdmissionTime(admission, occurrence)

	taskRuns, err := s.listTaskRunsForStartTx(ctx, tx, admission.Task.ID)
	if err != nil {
		return TaskScheduleRunAdmissionResult{}, err
	}
	if existing, found, err := findRunByScheduleOccurrence(taskRuns, admission.Run); err != nil {
		return TaskScheduleRunAdmissionResult{}, err
	} else if found {
		occurrence = startTaskScheduleOccurrence(occurrence, existing.ID, admission.CompletedAt)
		if err := s.updateTaskScheduleOccurrenceAdmissionTx(ctx, tx, occurrence, admission.ClaimOwner); err != nil {
			return TaskScheduleRunAdmissionResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return TaskScheduleRunAdmissionResult{}, err
		}
		return TaskScheduleRunAdmissionResult{
			Task: storedTask, Run: existing, Occurrence: occurrence, ExistingRun: true,
		}, nil
	}
	maxRunNumber := 0
	for _, storedRun := range taskRuns {
		if !types.IsTerminalTaskRunStatus(storedRun.Status) {
			occurrence = skipTaskScheduleOccurrence(occurrence, admission.CompletedAt)
			if err := s.updateTaskScheduleOccurrenceAdmissionTx(ctx, tx, occurrence, admission.ClaimOwner); err != nil {
				return TaskScheduleRunAdmissionResult{}, err
			}
			if err := tx.Commit(); err != nil {
				return TaskScheduleRunAdmissionResult{}, err
			}
			return TaskScheduleRunAdmissionResult{
				Task: storedTask, Occurrence: occurrence, Skipped: true,
			}, nil
		}
		if storedRun.Number > maxRunNumber {
			maxRunNumber = storedRun.Number
		}
	}
	task, err := mergeRunStartTask(storedTask, admission.Task, admission.BudgetMicrosUSD)
	if err != nil {
		return TaskScheduleRunAdmissionResult{}, err
	}
	run := admission.Run
	run.Number = maxRunNumber + 1
	payload, err := json.Marshal(run)
	if err != nil {
		return TaskScheduleRunAdmissionResult{}, err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (id, task_id, number, status, started_at, payload)
		VALUES (?, ?, ?, ?, ?, ?)
	`, s.runsTable), run.ID, run.TaskID, run.Number, run.Status, run.StartedAt, string(payload)); err != nil {
		return TaskScheduleRunAdmissionResult{}, err
	}
	if err := s.sqliteUpdateTaskTx(ctx, tx, task); err != nil {
		return TaskScheduleRunAdmissionResult{}, err
	}
	occurrence = startTaskScheduleOccurrence(occurrence, run.ID, admission.CompletedAt)
	if err := s.updateTaskScheduleOccurrenceAdmissionTx(ctx, tx, occurrence, admission.ClaimOwner); err != nil {
		return TaskScheduleRunAdmissionResult{}, err
	}
	if admission.Approval != nil {
		approvalPayload, err := json.Marshal(admission.Approval)
		if err != nil {
			return TaskScheduleRunAdmissionResult{}, err
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
			INSERT INTO %s (id, task_id, run_id, status, created_at, payload)
			VALUES (?, ?, ?, ?, ?, ?)
		`, s.approvalsTable),
			admission.Approval.ID,
			admission.Approval.TaskID,
			admission.Approval.RunID,
			admission.Approval.Status,
			admission.Approval.CreatedAt,
			string(approvalPayload),
		); err != nil {
			return TaskScheduleRunAdmissionResult{}, err
		}
	}
	for _, event := range taskScheduleRunInitialEvents(admission, run) {
		if _, err := s.sqliteInsertRunEventTx(ctx, tx, event); err != nil {
			return TaskScheduleRunAdmissionResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return TaskScheduleRunAdmissionResult{}, err
	}
	s.signalRun(run.ID)
	return TaskScheduleRunAdmissionResult{
		Task: task, Run: run, Occurrence: occurrence, Applied: true,
	}, nil
}

func (s *SQLiteStore) getTaskScheduleOccurrenceForAdmissionTx(
	ctx context.Context,
	tx storage.Tx,
	scheduleID string,
	scheduledFor time.Time,
) (TaskScheduleOccurrence, bool, error) {
	query := fmt.Sprintf(`
		SELECT id, task_id, schedule_id, scheduled_for, status, claim_owner, claimed_at,
			run_id, error, completed_at
		FROM %s
		WHERE schedule_id = ? AND scheduled_for = ?
	`, s.occurrencesTable)
	if s.client.Dialect() == storage.DialectPostgres {
		query += " FOR UPDATE"
	}
	occurrence, err := scanTaskScheduleOccurrence(tx.QueryRowContext(
		ctx, query, scheduleID, s.taskScheduleTimeValue(scheduledFor),
	))
	if errors.Is(err, sql.ErrNoRows) {
		return TaskScheduleOccurrence{}, false, nil
	}
	if err != nil {
		return TaskScheduleOccurrence{}, false, err
	}
	return occurrence, true, nil
}

func (s *SQLiteStore) getTaskRunForStartTx(ctx context.Context, tx storage.Tx, taskID, runID string) (types.TaskRun, bool, error) {
	var payload string
	err := tx.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT payload FROM %s WHERE id = ? AND task_id = ?
	`, s.runsTable), runID, taskID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return types.TaskRun{}, false, nil
	}
	if err != nil {
		return types.TaskRun{}, false, err
	}
	var run types.TaskRun
	if err := json.Unmarshal([]byte(payload), &run); err != nil {
		return types.TaskRun{}, false, err
	}
	return run, true, nil
}

func (s *SQLiteStore) updateTaskScheduleOccurrenceAdmissionTx(
	ctx context.Context,
	tx storage.Tx,
	occurrence TaskScheduleOccurrence,
	claimOwner string,
) error {
	result, err := tx.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s
		SET status = ?, run_id = ?, error = ?, completed_at = ?
		WHERE id = ? AND status = ? AND claim_owner = ?
	`, s.occurrencesTable),
		occurrence.Status,
		occurrence.RunID,
		occurrence.Error,
		s.taskScheduleTimeValue(occurrence.CompletedAt),
		occurrence.ID,
		TaskScheduleOccurrenceClaimed,
		claimOwner,
	)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return ErrScheduleOccurrenceClaimLost
	}
	return nil
}

type taskScheduleScanner interface {
	Scan(dest ...any) error
}

func scanTaskSchedule(scanner taskScheduleScanner) (TaskSchedule, error) {
	var schedule TaskSchedule
	var runAt, nextRunAt, createdAt, updatedAt any
	if err := scanner.Scan(
		&schedule.ID,
		&schedule.TaskID,
		&schedule.Kind,
		&schedule.CronExpression,
		&schedule.Timezone,
		&runAt,
		&schedule.Enabled,
		&nextRunAt,
		&createdAt,
		&updatedAt,
		&schedule.Revision,
	); err != nil {
		return TaskSchedule{}, err
	}
	var err error
	if schedule.RunAt, err = parseTaskScheduleTime(runAt); err != nil {
		return TaskSchedule{}, err
	}
	if schedule.NextRunAt, err = parseTaskScheduleTime(nextRunAt); err != nil {
		return TaskSchedule{}, err
	}
	if schedule.CreatedAt, err = parseTaskScheduleTime(createdAt); err != nil {
		return TaskSchedule{}, err
	}
	if schedule.UpdatedAt, err = parseTaskScheduleTime(updatedAt); err != nil {
		return TaskSchedule{}, err
	}
	return schedule, nil
}

func scanTaskSchedules(rows *sql.Rows) ([]TaskSchedule, error) {
	items := make([]TaskSchedule, 0)
	for rows.Next() {
		schedule, err := scanTaskSchedule(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, schedule)
	}
	return items, rows.Err()
}

func scanTaskScheduleOccurrence(scanner taskScheduleScanner) (TaskScheduleOccurrence, error) {
	var occurrence TaskScheduleOccurrence
	var scheduledFor, claimedAt, completedAt any
	if err := scanner.Scan(
		&occurrence.ID,
		&occurrence.TaskID,
		&occurrence.ScheduleID,
		&scheduledFor,
		&occurrence.Status,
		&occurrence.ClaimOwner,
		&claimedAt,
		&occurrence.RunID,
		&occurrence.Error,
		&completedAt,
	); err != nil {
		return TaskScheduleOccurrence{}, err
	}
	var err error
	if occurrence.ScheduledFor, err = parseTaskScheduleTime(scheduledFor); err != nil {
		return TaskScheduleOccurrence{}, err
	}
	if occurrence.ClaimedAt, err = parseTaskScheduleTime(claimedAt); err != nil {
		return TaskScheduleOccurrence{}, err
	}
	if occurrence.CompletedAt, err = parseTaskScheduleTime(completedAt); err != nil {
		return TaskScheduleOccurrence{}, err
	}
	return occurrence, nil
}

func taskScheduleOccurrenceOrder(filter TaskScheduleOccurrenceFilter) string {
	if !filter.ClaimedBefore.IsZero() {
		return "claimed_at ASC, id ASC"
	}
	return "scheduled_for DESC, id ASC"
}

func (s *SQLiteStore) taskScheduleTimeValue(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	value = value.UTC()
	if s.client.Dialect() == storage.DialectPostgres {
		return value
	}
	return value.Format(taskScheduleSQLiteTimeLayout)
}

// SQLite compares and orders timestamp TEXT lexically. RFC3339Nano omits the
// fractional part for exact-second values, which makes an exact second sort
// after fractional values from that same second. A fixed nine-digit fraction
// preserves chronological ordering while retaining nanosecond precision.
const taskScheduleSQLiteTimeLayout = "2006-01-02T15:04:05.000000000Z"

func parseTaskScheduleTime(value any) (time.Time, error) {
	switch typed := value.(type) {
	case nil:
		return time.Time{}, nil
	case time.Time:
		return typed.UTC(), nil
	case string:
		return parseTaskScheduleTimeString(typed)
	case []byte:
		return parseTaskScheduleTimeString(string(typed))
	default:
		return time.Time{}, fmt.Errorf("unsupported task schedule timestamp %T", value)
	}
}

func parseTaskScheduleTimeString(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(taskScheduleSQLiteTimeLayout, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid task schedule timestamp %q", value)
	}
	return parsed.UTC(), nil
}
