package taskstate

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/storage"
	"github.com/hecatehq/hecate/pkg/types"
)

func validateRunStateTransition(tr RunStateTransition) error {
	if strings.TrimSpace(tr.Task.ID) == "" {
		return fmt.Errorf("task id is required")
	}
	if strings.TrimSpace(tr.Run.ID) == "" {
		return fmt.Errorf("run id is required")
	}
	if tr.Run.TaskID != "" && tr.Run.TaskID != tr.Task.ID {
		return fmt.Errorf("run %q does not belong to task %q", tr.Run.ID, tr.Task.ID)
	}
	if len(tr.ExpectedRunStatuses) == 0 {
		return fmt.Errorf("expected run status is required")
	}
	if err := validatePendingApprovalResolution(tr.ApprovalResolution, "approved"); err != nil {
		return err
	}
	if tr.ApprovalResolution != nil {
		if !runStatusExpected("awaiting_approval", tr.ExpectedRunStatuses) {
			return fmt.Errorf("approval resolution requires awaiting_approval expected run status")
		}
		if tr.Run.Status != "queued" || tr.Task.Status != "queued" {
			return fmt.Errorf("approved approval resolution requires queued task and run candidates")
		}
		if err := validateApprovalResolutionProjection(*tr.ApprovalResolution, tr.Task, tr.Run); err != nil {
			return err
		}
		if len(tr.Events) != 0 {
			return fmt.Errorf("approved approval resolution events are store-derived")
		}
	}
	return nil
}

func runStatusExpected(status string, expected []string) bool {
	for _, candidate := range expected {
		if status == candidate {
			return true
		}
	}
	return false
}

func (s *MemoryStore) ApplyRunStateTransition(_ context.Context, tr RunStateTransition) (RunStateTransitionResult, error) {
	if err := validateRunStateTransition(tr); err != nil {
		return RunStateTransitionResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	storedTask, ok := s.tasks[tr.Task.ID]
	if !ok {
		return RunStateTransitionResult{}, fmt.Errorf("task %q not found", tr.Task.ID)
	}
	storedRun, ok := s.runs[tr.Run.ID]
	if !ok || storedRun.TaskID != tr.Task.ID {
		return RunStateTransitionResult{}, fmt.Errorf("run %q not found", tr.Run.ID)
	}
	storedApproval := types.TaskApproval{}
	if tr.ApprovalResolution != nil {
		var found bool
		storedApproval, found = s.approvals[tr.ApprovalResolution.ApprovalID]
		if !found || storedApproval.TaskID != tr.Task.ID || storedApproval.RunID != tr.Run.ID {
			return RunStateTransitionResult{}, fmt.Errorf("approval %q not found", tr.ApprovalResolution.ApprovalID)
		}
	}
	if !runStatusExpected(storedRun.Status, tr.ExpectedRunStatuses) ||
		(tr.ApprovalResolution != nil && (storedRun.Status != "awaiting_approval" || storedApproval.Status != "pending")) {
		return RunStateTransitionResult{Task: cloneTask(storedTask), Run: storedRun, Approval: storedApproval}, nil
	}
	resolvedApproval := storedApproval
	if tr.ApprovalResolution != nil {
		var err error
		resolvedApproval, err = mergeApprovalResolution(storedApproval, *tr.ApprovalResolution)
		if err != nil {
			return RunStateTransitionResult{}, err
		}
	}

	task := cloneTask(tr.Task)
	run := tr.Run
	s.tasks[task.ID] = task
	s.runs[run.ID] = run
	if tr.ApprovalResolution != nil {
		s.approvals[resolvedApproval.ID] = resolvedApproval
	}
	var steps []types.TaskStep
	var artifacts []types.TaskArtifact
	if tr.ApprovalResolution != nil || runEventSpecsNeedSnapshot(tr.Events) {
		steps = s.listStepsLocked(run.ID)
		artifacts = s.listArtifactsLocked(ArtifactFilter{TaskID: task.ID, RunID: run.ID})
	}
	events := make([]types.TaskRunEvent, 0, len(tr.Events)+2)
	if tr.ApprovalResolution != nil {
		event := approvalResolutionEvent(task.ID, *tr.ApprovalResolution, resolvedApproval, run, steps, artifacts)
		events = append(events, s.appendRunEventLocked(event))
		events = append(events, s.appendRunEventLocked(approvalRunQueuedEvent(task.ID, *tr.ApprovalResolution, run, steps, artifacts)))
	}
	for _, spec := range tr.Events {
		event := runStateEventFromSpec(spec, task.ID, run, steps, artifacts, time.Now().UTC())
		events = append(events, s.appendRunEventLocked(event))
	}
	s.signalRun(run.ID)
	return RunStateTransitionResult{Task: cloneTask(task), Run: run, Approval: resolvedApproval, Events: events, Applied: true}, nil
}

func (s *SQLiteStore) ApplyRunStateTransition(ctx context.Context, tr RunStateTransition) (RunStateTransitionResult, error) {
	if err := validateRunStateTransition(tr); err != nil {
		return RunStateTransitionResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RunStateTransitionResult{}, err
	}
	defer tx.Rollback()

	storedTask, err := s.sqliteGetTaskTx(ctx, tx, tr.Task.ID)
	if err != nil {
		return RunStateTransitionResult{}, err
	}
	storedRun, err := s.sqliteGetRunTx(ctx, tx, tr.Task.ID, tr.Run.ID)
	if err != nil {
		return RunStateTransitionResult{}, err
	}
	storedApproval := types.TaskApproval{}
	if tr.ApprovalResolution != nil {
		storedApproval, err = s.sqliteGetApprovalTx(ctx, tx, tr.Task.ID, tr.Run.ID, tr.ApprovalResolution.ApprovalID)
		if err != nil {
			return RunStateTransitionResult{}, err
		}
	}
	if !runStatusExpected(storedRun.Status, tr.ExpectedRunStatuses) ||
		(tr.ApprovalResolution != nil && (storedRun.Status != "awaiting_approval" || storedApproval.Status != "pending")) {
		return RunStateTransitionResult{Task: storedTask, Run: storedRun, Approval: storedApproval}, nil
	}
	resolvedApproval := storedApproval
	if tr.ApprovalResolution != nil {
		resolvedApproval, err = mergeApprovalResolution(storedApproval, *tr.ApprovalResolution)
		if err != nil {
			return RunStateTransitionResult{}, err
		}
	}

	payload, err := json.Marshal(tr.Run)
	if err != nil {
		return RunStateTransitionResult{}, err
	}
	args := []any{tr.Run.Status, tr.Run.StartedAt, string(payload), tr.Run.ID, tr.Task.ID}
	for _, status := range tr.ExpectedRunStatuses {
		args = append(args, status)
	}
	result, err := tx.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s
		SET status = ?, started_at = ?, payload = ?
		WHERE id = ? AND task_id = ? AND status IN (%s)
	`, s.runsTable, sqlitePlaceholders(len(tr.ExpectedRunStatuses))), args...)
	if err != nil {
		return RunStateTransitionResult{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return RunStateTransitionResult{}, err
	}
	if affected == 0 {
		currentTask, taskErr := s.sqliteGetTaskTx(ctx, tx, tr.Task.ID)
		if taskErr != nil {
			return RunStateTransitionResult{}, taskErr
		}
		currentRun, runErr := s.sqliteGetRunTx(ctx, tx, tr.Task.ID, tr.Run.ID)
		if runErr != nil {
			return RunStateTransitionResult{}, runErr
		}
		return RunStateTransitionResult{Task: currentTask, Run: currentRun, Approval: storedApproval}, nil
	}
	if err := s.sqliteUpdateTaskTx(ctx, tx, tr.Task); err != nil {
		return RunStateTransitionResult{}, err
	}
	if tr.ApprovalResolution != nil {
		if err := s.sqliteUpdateApprovalTx(ctx, tx, resolvedApproval); err != nil {
			return RunStateTransitionResult{}, err
		}
	}
	var steps []types.TaskStep
	var artifacts []types.TaskArtifact
	if tr.ApprovalResolution != nil || runEventSpecsNeedSnapshot(tr.Events) {
		steps, err = s.sqliteListStepsTx(ctx, tx, tr.Run.ID)
		if err != nil {
			return RunStateTransitionResult{}, err
		}
		artifacts, err = s.sqliteListArtifactsTx(ctx, tx, ArtifactFilter{TaskID: tr.Task.ID, RunID: tr.Run.ID}, "")
		if err != nil {
			return RunStateTransitionResult{}, err
		}
	}
	events := make([]types.TaskRunEvent, 0, len(tr.Events)+2)
	if tr.ApprovalResolution != nil {
		event := approvalResolutionEvent(tr.Task.ID, *tr.ApprovalResolution, resolvedApproval, tr.Run, steps, artifacts)
		inserted, err := s.sqliteInsertRunEventTx(ctx, tx, event)
		if err != nil {
			return RunStateTransitionResult{}, err
		}
		events = append(events, inserted)
		queued := approvalRunQueuedEvent(tr.Task.ID, *tr.ApprovalResolution, tr.Run, steps, artifacts)
		inserted, err = s.sqliteInsertRunEventTx(ctx, tx, queued)
		if err != nil {
			return RunStateTransitionResult{}, err
		}
		events = append(events, inserted)
	}
	for _, spec := range tr.Events {
		event := runStateEventFromSpec(spec, tr.Task.ID, tr.Run, steps, artifacts, time.Now().UTC())
		inserted, err := s.sqliteInsertRunEventTx(ctx, tx, event)
		if err != nil {
			return RunStateTransitionResult{}, err
		}
		events = append(events, inserted)
	}
	if err := tx.Commit(); err != nil {
		return RunStateTransitionResult{}, err
	}
	s.signalRun(tr.Run.ID)
	return RunStateTransitionResult{Task: tr.Task, Run: tr.Run, Approval: resolvedApproval, Events: events, Applied: true}, nil
}

func (s *SQLiteStore) sqliteGetTaskTx(ctx context.Context, tx interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, taskID string) (types.Task, error) {
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

func (s *SQLiteStore) sqliteGetRunTx(ctx context.Context, tx interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, taskID, runID string) (types.TaskRun, error) {
	query := fmt.Sprintf(`SELECT payload FROM %s WHERE id = ? AND task_id = ?`, s.runsTable)
	if s.client != nil && s.client.Dialect() == storage.DialectPostgres {
		query += " FOR UPDATE"
	}
	var payload string
	if err := tx.QueryRowContext(ctx, query, runID, taskID).Scan(&payload); err != nil {
		if err == sql.ErrNoRows {
			return types.TaskRun{}, fmt.Errorf("run %q not found", runID)
		}
		return types.TaskRun{}, err
	}
	var run types.TaskRun
	if err := json.Unmarshal([]byte(payload), &run); err != nil {
		return types.TaskRun{}, err
	}
	return run, nil
}

func (s *SQLiteStore) sqliteGetApprovalTx(ctx context.Context, tx interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, taskID, runID, approvalID string) (types.TaskApproval, error) {
	query := fmt.Sprintf(`SELECT payload FROM %s WHERE id = ? AND task_id = ? AND run_id = ?`, s.approvalsTable)
	if s.client != nil && s.client.Dialect() == storage.DialectPostgres {
		query += " FOR UPDATE"
	}
	var payload string
	if err := tx.QueryRowContext(ctx, query, approvalID, taskID, runID).Scan(&payload); err != nil {
		if err == sql.ErrNoRows {
			return types.TaskApproval{}, fmt.Errorf("approval %q not found", approvalID)
		}
		return types.TaskApproval{}, err
	}
	var approval types.TaskApproval
	if err := json.Unmarshal([]byte(payload), &approval); err != nil {
		return types.TaskApproval{}, err
	}
	return approval, nil
}
