package taskstate

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hecatehq/hecate/internal/storage"
	"github.com/hecatehq/hecate/pkg/types"
)

func validateRunStartTransition(tr RunStartTransition) error {
	if strings.TrimSpace(tr.Task.ID) == "" {
		return fmt.Errorf("task id is required")
	}
	if strings.TrimSpace(tr.Run.ID) == "" {
		return fmt.Errorf("run id is required")
	}
	if tr.Run.TaskID != tr.Task.ID {
		return fmt.Errorf("run %q does not belong to task %q", tr.Run.ID, tr.Task.ID)
	}
	if tr.Task.LatestRunID != tr.Run.ID {
		return fmt.Errorf("task latest run %q does not match run %q", tr.Task.LatestRunID, tr.Run.ID)
	}
	if tr.Task.Status != tr.Run.Status {
		return fmt.Errorf("task status %q does not match run status %q", tr.Task.Status, tr.Run.Status)
	}
	if tr.Run.Status != "queued" && tr.Run.Status != "awaiting_approval" {
		return fmt.Errorf("run start status %q is invalid", tr.Run.Status)
	}
	if tr.BudgetMicrosUSD < 0 {
		return fmt.Errorf("budget_micros_usd must be non-negative")
	}
	return nil
}

func mergeRunStartTask(stored, candidate types.Task, budgetMicrosUSD int64) (types.Task, error) {
	if budgetMicrosUSD > 0 && budgetMicrosUSD < stored.BudgetMicrosUSD {
		return types.Task{}, ErrBudgetLower
	}
	task := cloneTask(stored)
	task.Status = candidate.Status
	task.LatestRunID = candidate.LatestRunID
	if task.StartedAt.IsZero() {
		task.StartedAt = candidate.StartedAt
	}
	task.FinishedAt = candidate.FinishedAt
	task.UpdatedAt = candidate.UpdatedAt
	task.RootTraceID = candidate.RootTraceID
	task.LatestTraceID = candidate.LatestTraceID
	task.LatestRequestID = candidate.LatestRequestID
	if budgetMicrosUSD > 0 {
		task.BudgetMicrosUSD = budgetMicrosUSD
	}
	return task, nil
}

func (s *MemoryStore) ApplyRunStartTransition(_ context.Context, tr RunStartTransition) (RunStartTransitionResult, error) {
	if err := validateRunStartTransition(tr); err != nil {
		return RunStartTransitionResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	storedTask, ok := s.tasks[tr.Task.ID]
	if !ok {
		return RunStartTransitionResult{}, fmt.Errorf("task %q not found", tr.Task.ID)
	}
	nextRunNumber := 1
	for _, storedRun := range s.runs {
		if storedRun.TaskID != tr.Task.ID {
			continue
		}
		if !types.IsTerminalTaskRunStatus(storedRun.Status) {
			return RunStartTransitionResult{}, ErrActiveRun
		}
		if storedRun.Number >= nextRunNumber {
			nextRunNumber = storedRun.Number + 1
		}
	}
	if _, exists := s.runs[tr.Run.ID]; exists {
		return RunStartTransitionResult{}, fmt.Errorf("run %q already exists", tr.Run.ID)
	}
	task, err := mergeRunStartTask(storedTask, tr.Task, tr.BudgetMicrosUSD)
	if err != nil {
		return RunStartTransitionResult{}, err
	}
	run := tr.Run
	run.Number = nextRunNumber
	s.tasks[task.ID] = cloneTask(task)
	s.runs[run.ID] = run
	s.signalRun(run.ID)
	return RunStartTransitionResult{Task: cloneTask(task), Run: run}, nil
}

func (s *SQLiteStore) ApplyRunStartTransition(ctx context.Context, tr RunStartTransition) (RunStartTransitionResult, error) {
	if err := validateRunStartTransition(tr); err != nil {
		return RunStartTransitionResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RunStartTransitionResult{}, err
	}
	defer tx.Rollback()

	storedTask, err := s.getTaskForRunStartTx(ctx, tx, tr.Task.ID)
	if err != nil {
		return RunStartTransitionResult{}, err
	}
	var activeRuns int
	if err := tx.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT COUNT(1)
		FROM %s
		WHERE task_id = ? AND status NOT IN ('completed', 'failed', 'cancelled')
	`, s.runsTable), tr.Task.ID).Scan(&activeRuns); err != nil {
		return RunStartTransitionResult{}, err
	}
	if activeRuns > 0 {
		return RunStartTransitionResult{}, ErrActiveRun
	}
	task, err := mergeRunStartTask(storedTask, tr.Task, tr.BudgetMicrosUSD)
	if err != nil {
		return RunStartTransitionResult{}, err
	}
	var maxRunNumber int
	if err := tx.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT COALESCE(MAX(number), 0)
		FROM %s
		WHERE task_id = ?
	`, s.runsTable), tr.Task.ID).Scan(&maxRunNumber); err != nil {
		return RunStartTransitionResult{}, err
	}
	run := tr.Run
	run.Number = maxRunNumber + 1
	payload, err := json.Marshal(run)
	if err != nil {
		return RunStartTransitionResult{}, err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (id, task_id, number, status, started_at, payload)
		VALUES (?, ?, ?, ?, ?, ?)
	`, s.runsTable), run.ID, run.TaskID, run.Number, run.Status, run.StartedAt, string(payload)); err != nil {
		return RunStartTransitionResult{}, err
	}
	if err := s.sqliteUpdateTaskTx(ctx, tx, task); err != nil {
		return RunStartTransitionResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return RunStartTransitionResult{}, err
	}
	s.signalRun(run.ID)
	return RunStartTransitionResult{Task: task, Run: run}, nil
}

func (s *SQLiteStore) getTaskForRunStartTx(ctx context.Context, tx storage.Tx, taskID string) (types.Task, error) {
	query := fmt.Sprintf(`SELECT payload FROM %s WHERE id = ?`, s.tasksTable)
	if s.client != nil && s.client.Dialect() == storage.DialectPostgres {
		query += " FOR UPDATE"
	}
	var payload string
	if err := tx.QueryRowContext(ctx, query, taskID).Scan(&payload); err != nil {
		if err == sql.ErrNoRows {
			return types.Task{}, fmt.Errorf("task %q not found", taskID)
		}
		return types.Task{}, err
	}
	var task types.Task
	if err := json.Unmarshal([]byte(payload), &task); err != nil {
		return types.Task{}, err
	}
	return task, nil
}
