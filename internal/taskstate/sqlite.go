package taskstate

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/runtimeevents"
	"github.com/hecatehq/hecate/internal/storage"
	"github.com/hecatehq/hecate/pkg/types"
)

// SQLiteStore mirrors the memory Store-interface surface with durable
// JSON-payload task-state tables.
//
// SQLite-specific choices that aren't accidental:
//   - payload columns are TEXT (SQLite has no JSONB; JSON1 functions still
//     work over plain TEXT for any future querying needs).
//   - run_events.sequence is `INTEGER PRIMARY KEY AUTOINCREMENT` instead of
//     BIGSERIAL — the SQLite idiom for monotonic row ids.
//   - placeholders are `?` rather than `$N`.
//   - status-set filtering uses `status IN (?, ?, ...)` because SQLite
//     lacks array params.
type SQLiteStore struct {
	runEventBus
	db             storage.DB
	client         storage.SQLClient
	backend        string
	tasksTable     string
	runsTable      string
	stepsTable     string
	approvalsTable string
	artifactsTable string
	eventsTable    string
}

func NewSQLiteStore(ctx context.Context, client *storage.SQLiteClient) (*SQLiteStore, error) {
	return newSQLStore(ctx, client)
}

func NewPostgresStore(ctx context.Context, client *storage.PostgresClient) (*SQLiteStore, error) {
	return newSQLStore(ctx, client)
}

func newSQLStore(ctx context.Context, client storage.SQLClient) (*SQLiteStore, error) {
	if client == nil || client.DB() == nil {
		return nil, fmt.Errorf("sql client is required")
	}
	store := &SQLiteStore{
		db:             client.DB(),
		client:         client,
		backend:        client.Backend(),
		tasksTable:     client.QualifiedTable("task_state_tasks"),
		runsTable:      client.QualifiedTable("task_state_runs"),
		stepsTable:     client.QualifiedTable("task_state_steps"),
		approvalsTable: client.QualifiedTable("task_state_approvals"),
		artifactsTable: client.QualifiedTable("task_state_artifacts"),
		eventsTable:    client.QualifiedTable("task_state_run_events"),
	}
	if err := store.migrate(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *SQLiteStore) Backend() string { return s.backend }

func (s *SQLiteStore) CreateTask(ctx context.Context, task types.Task) (types.Task, error) {
	if strings.TrimSpace(task.ID) == "" {
		return types.Task{}, fmt.Errorf("task id is required")
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now().UTC()
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = task.CreatedAt
	}
	payload, err := json.Marshal(task)
	if err != nil {
		return types.Task{}, err
	}
	_, err = s.db.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (id, status, updated_at, payload)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (id)
		DO UPDATE SET status = excluded.status, updated_at = excluded.updated_at, payload = excluded.payload
	`, s.tasksTable), task.ID, task.Status, task.UpdatedAt, string(payload))
	if err != nil {
		return types.Task{}, err
	}
	return task, nil
}

func (s *SQLiteStore) GetTask(ctx context.Context, id string) (types.Task, bool, error) {
	var payload string
	err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT payload FROM %s WHERE id = ?`, s.tasksTable), id).Scan(&payload)
	if err == sql.ErrNoRows {
		return types.Task{}, false, nil
	}
	if err != nil {
		return types.Task{}, false, err
	}
	var task types.Task
	if err := json.Unmarshal([]byte(payload), &task); err != nil {
		return types.Task{}, false, err
	}
	return task, true, nil
}

func (s *SQLiteStore) ListTasks(ctx context.Context, filter TaskFilter) ([]types.Task, error) {
	args := []any{}
	where := []string{"1=1"}
	if filter.Status != "" {
		args = append(args, filter.Status)
		where = append(where, "status = ?")
	}
	if filter.ProjectID != nil {
		if *filter.ProjectID == "" {
			where = append(where, s.projectIDWhereEmpty())
		} else {
			args = append(args, *filter.ProjectID)
			where = append(where, s.projectIDWhereEquals())
		}
	}
	limitSQL := ""
	if filter.Limit > 0 {
		args = append(args, filter.Limit)
		limitSQL = "LIMIT ?"
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT payload
		FROM %s
		WHERE %s
		ORDER BY updated_at DESC
		%s
	`, s.tasksTable, strings.Join(where, " AND "), limitSQL), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]types.Task, 0)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var task types.Task
		if err := json.Unmarshal([]byte(payload), &task); err != nil {
			return nil, err
		}
		items = append(items, task)
	}
	return items, rows.Err()
}

func (s *SQLiteStore) projectIDWhereEmpty() string {
	if s.client != nil && s.client.Dialect() == storage.DialectPostgres {
		return "((payload::jsonb ->> 'ProjectID') IS NULL OR (payload::jsonb ->> 'ProjectID') = '')"
	}
	return "(json_extract(payload, '$.ProjectID') IS NULL OR json_extract(payload, '$.ProjectID') = '')"
}

func (s *SQLiteStore) projectIDWhereEquals() string {
	if s.client != nil && s.client.Dialect() == storage.DialectPostgres {
		return "(payload::jsonb ->> 'ProjectID') = ?"
	}
	return "json_extract(payload, '$.ProjectID') = ?"
}

func (s *SQLiteStore) UpdateTask(ctx context.Context, task types.Task) (types.Task, error) {
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = time.Now().UTC()
	}
	return s.CreateTask(ctx, task)
}

func (s *SQLiteStore) DeleteTask(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("task id is required")
	}
	for _, table := range []string{s.eventsTable, s.artifactsTable, s.approvalsTable, s.stepsTable, s.runsTable} {
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE task_id = ?`, table), id); err != nil {
			return err
		}
	}
	_, err := s.db.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id = ?`, s.tasksTable), id)
	return err
}

func (s *SQLiteStore) CreateRun(ctx context.Context, run types.TaskRun) (types.TaskRun, error) {
	if strings.TrimSpace(run.ID) == "" {
		return types.TaskRun{}, fmt.Errorf("run id is required")
	}
	payload, err := json.Marshal(run)
	if err != nil {
		return types.TaskRun{}, err
	}
	_, err = s.db.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (id, task_id, number, status, started_at, payload)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (id)
		DO UPDATE SET status = excluded.status, started_at = excluded.started_at, payload = excluded.payload
	`, s.runsTable), run.ID, run.TaskID, run.Number, run.Status, run.StartedAt, string(payload))
	if err != nil {
		return types.TaskRun{}, err
	}
	s.signalRun(run.ID)
	return run, nil
}

func (s *SQLiteStore) GetRun(ctx context.Context, taskID, runID string) (types.TaskRun, bool, error) {
	var payload string
	args := []any{runID}
	query := fmt.Sprintf(`SELECT payload FROM %s WHERE id = ?`, s.runsTable)
	if taskID != "" {
		args = append(args, taskID)
		query += " AND task_id = ?"
	}
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&payload)
	if err == sql.ErrNoRows {
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

func (s *SQLiteStore) ListRuns(ctx context.Context, taskID string) ([]types.TaskRun, error) {
	return s.ListRunsByFilter(ctx, RunFilter{TaskID: taskID})
}

func (s *SQLiteStore) ListRunsByFilter(ctx context.Context, filter RunFilter) ([]types.TaskRun, error) {
	args := []any{}
	where := []string{"1=1"}
	if filter.TaskID != "" {
		args = append(args, filter.TaskID)
		where = append(where, "task_id = ?")
	}
	if len(filter.Statuses) > 0 {
		// SQLite has no array params, so expand the IN list with one
		// placeholder per status.
		placeholders := make([]string, 0, len(filter.Statuses))
		for _, status := range filter.Statuses {
			args = append(args, status)
			placeholders = append(placeholders, "?")
		}
		where = append(where, fmt.Sprintf("status IN (%s)", strings.Join(placeholders, ", ")))
	}
	if filter.OrderByID && filter.AfterID != "" {
		args = append(args, filter.AfterID)
		where = append(where, "id > ?")
	}
	limitSQL := ""
	if filter.Limit > 0 {
		args = append(args, filter.Limit)
		limitSQL = "LIMIT ?"
	}
	orderSQL := "ORDER BY number DESC, started_at DESC"
	if filter.OrderByID {
		orderSQL = "ORDER BY id ASC"
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
			SELECT payload
			FROM %s
			WHERE %s
			%s
			%s
		`, s.runsTable, strings.Join(where, " AND "), orderSQL, limitSQL), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]types.TaskRun, 0)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var run types.TaskRun
		if err := json.Unmarshal([]byte(payload), &run); err != nil {
			return nil, err
		}
		items = append(items, run)
	}
	return items, rows.Err()
}

func (s *SQLiteStore) UpdateRun(ctx context.Context, run types.TaskRun) (types.TaskRun, error) {
	return s.CreateRun(ctx, run)
}

func (s *SQLiteStore) AppendStep(ctx context.Context, step types.TaskStep) (types.TaskStep, error) {
	if strings.TrimSpace(step.ID) == "" {
		return types.TaskStep{}, fmt.Errorf("step id is required")
	}
	payload, err := json.Marshal(step)
	if err != nil {
		return types.TaskStep{}, err
	}
	_, err = s.db.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (id, task_id, run_id, step_index, status, started_at, payload)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id)
		DO UPDATE SET status = excluded.status, payload = excluded.payload
	`, s.stepsTable), step.ID, step.TaskID, step.RunID, step.Index, step.Status, step.StartedAt, string(payload))
	if err != nil {
		return types.TaskStep{}, err
	}
	s.signalRun(step.RunID)
	return step, nil
}

func (s *SQLiteStore) GetStep(ctx context.Context, runID, stepID string) (types.TaskStep, bool, error) {
	var payload string
	args := []any{stepID}
	query := fmt.Sprintf(`SELECT payload FROM %s WHERE id = ?`, s.stepsTable)
	if runID != "" {
		args = append(args, runID)
		query += " AND run_id = ?"
	}
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&payload)
	if err == sql.ErrNoRows {
		return types.TaskStep{}, false, nil
	}
	if err != nil {
		return types.TaskStep{}, false, err
	}
	var step types.TaskStep
	if err := json.Unmarshal([]byte(payload), &step); err != nil {
		return types.TaskStep{}, false, err
	}
	return step, true, nil
}

func (s *SQLiteStore) ListSteps(ctx context.Context, runID string) ([]types.TaskStep, error) {
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT payload
		FROM %s
		WHERE run_id = ?
		ORDER BY step_index ASC, id ASC
	`, s.stepsTable), runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]types.TaskStep, 0)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var step types.TaskStep
		if err := json.Unmarshal([]byte(payload), &step); err != nil {
			return nil, err
		}
		items = append(items, step)
	}
	return items, rows.Err()
}

func (s *SQLiteStore) UpdateStep(ctx context.Context, step types.TaskStep) (types.TaskStep, error) {
	return s.AppendStep(ctx, step)
}

func (s *SQLiteStore) CreateApproval(ctx context.Context, approval types.TaskApproval) (types.TaskApproval, error) {
	if strings.TrimSpace(approval.ID) == "" {
		return types.TaskApproval{}, fmt.Errorf("approval id is required")
	}
	if approval.CreatedAt.IsZero() {
		approval.CreatedAt = time.Now().UTC()
	}
	payload, err := json.Marshal(approval)
	if err != nil {
		return types.TaskApproval{}, err
	}
	_, err = s.db.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (id, task_id, run_id, status, created_at, payload)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (id)
		DO UPDATE SET status = excluded.status, payload = excluded.payload
	`, s.approvalsTable), approval.ID, approval.TaskID, approval.RunID, approval.Status, approval.CreatedAt, string(payload))
	if err != nil {
		return types.TaskApproval{}, err
	}
	s.signalRun(approval.RunID)
	return approval, nil
}

func (s *SQLiteStore) GetApproval(ctx context.Context, taskID, approvalID string) (types.TaskApproval, bool, error) {
	var payload string
	args := []any{approvalID}
	query := fmt.Sprintf(`SELECT payload FROM %s WHERE id = ?`, s.approvalsTable)
	if taskID != "" {
		args = append(args, taskID)
		query += " AND task_id = ?"
	}
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&payload)
	if err == sql.ErrNoRows {
		return types.TaskApproval{}, false, nil
	}
	if err != nil {
		return types.TaskApproval{}, false, err
	}
	var approval types.TaskApproval
	if err := json.Unmarshal([]byte(payload), &approval); err != nil {
		return types.TaskApproval{}, false, err
	}
	return approval, true, nil
}

func (s *SQLiteStore) ListApprovals(ctx context.Context, taskID string) ([]types.TaskApproval, error) {
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT payload
		FROM %s
		WHERE task_id = ?
		ORDER BY created_at DESC, id DESC
	`, s.approvalsTable), taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]types.TaskApproval, 0)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var approval types.TaskApproval
		if err := json.Unmarshal([]byte(payload), &approval); err != nil {
			return nil, err
		}
		items = append(items, approval)
	}
	return items, rows.Err()
}

func (s *SQLiteStore) UpdateApproval(ctx context.Context, approval types.TaskApproval) (types.TaskApproval, error) {
	return s.CreateApproval(ctx, approval)
}

func (s *SQLiteStore) UpdatePendingApproval(ctx context.Context, approval types.TaskApproval) (types.TaskApproval, bool, error) {
	if strings.TrimSpace(approval.ID) == "" {
		return types.TaskApproval{}, false, fmt.Errorf("approval id is required")
	}
	if strings.TrimSpace(approval.TaskID) == "" {
		return types.TaskApproval{}, false, fmt.Errorf("approval task id is required")
	}
	payload, err := json.Marshal(approval)
	if err != nil {
		return types.TaskApproval{}, false, err
	}
	res, err := s.db.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s
		SET status = ?, payload = ?
		WHERE id = ? AND task_id = ? AND status = 'pending'
	`, s.approvalsTable), approval.Status, string(payload), approval.ID, approval.TaskID)
	if err != nil {
		return types.TaskApproval{}, false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return types.TaskApproval{}, false, err
	}
	if n == 0 {
		return types.TaskApproval{}, false, nil
	}
	s.signalRun(approval.RunID)
	return approval, true, nil
}

func (s *SQLiteStore) CreateArtifact(ctx context.Context, artifact types.TaskArtifact) (types.TaskArtifact, error) {
	if strings.TrimSpace(artifact.ID) == "" {
		return types.TaskArtifact{}, fmt.Errorf("artifact id is required")
	}
	payload, err := json.Marshal(artifact)
	if err != nil {
		return types.TaskArtifact{}, err
	}
	_, err = s.db.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (id, task_id, run_id, step_id, kind, status, created_at, payload)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id)
		DO UPDATE SET status = excluded.status, payload = excluded.payload
	`, s.artifactsTable), artifact.ID, artifact.TaskID, artifact.RunID, artifact.StepID, artifact.Kind, artifact.Status, artifact.CreatedAt, string(payload))
	if err != nil {
		return types.TaskArtifact{}, err
	}
	s.signalRun(artifact.RunID)
	return artifact, nil
}

func (s *SQLiteStore) GetArtifact(ctx context.Context, taskID, artifactID string) (types.TaskArtifact, bool, error) {
	var payload string
	args := []any{artifactID}
	query := fmt.Sprintf(`SELECT payload FROM %s WHERE id = ?`, s.artifactsTable)
	if taskID != "" {
		args = append(args, taskID)
		query += " AND task_id = ?"
	}
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&payload)
	if err == sql.ErrNoRows {
		return types.TaskArtifact{}, false, nil
	}
	if err != nil {
		return types.TaskArtifact{}, false, err
	}
	var artifact types.TaskArtifact
	if err := json.Unmarshal([]byte(payload), &artifact); err != nil {
		return types.TaskArtifact{}, false, err
	}
	return artifact, true, nil
}

func (s *SQLiteStore) ListArtifacts(ctx context.Context, filter ArtifactFilter) ([]types.TaskArtifact, error) {
	args := []any{}
	where := []string{"1=1"}
	if filter.TaskID != "" {
		args = append(args, filter.TaskID)
		where = append(where, "task_id = ?")
	}
	if filter.RunID != "" {
		args = append(args, filter.RunID)
		where = append(where, "run_id = ?")
	}
	if filter.StepID != "" {
		args = append(args, filter.StepID)
		where = append(where, "step_id = ?")
	}
	if filter.Kind != "" {
		args = append(args, filter.Kind)
		where = append(where, "kind = ?")
	}
	limitSQL := ""
	if filter.Limit > 0 {
		args = append(args, filter.Limit)
		limitSQL = "LIMIT ?"
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT payload
		FROM %s
		WHERE %s
		ORDER BY created_at DESC, id DESC
		%s
	`, s.artifactsTable, strings.Join(where, " AND "), limitSQL), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]types.TaskArtifact, 0)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var artifact types.TaskArtifact
		if err := json.Unmarshal([]byte(payload), &artifact); err != nil {
			return nil, err
		}
		items = append(items, artifact)
	}
	return items, rows.Err()
}

func (s *SQLiteStore) UpdateArtifact(ctx context.Context, artifact types.TaskArtifact) (types.TaskArtifact, error) {
	return s.CreateArtifact(ctx, artifact)
}

func (s *SQLiteStore) AppendRunEvent(ctx context.Context, event types.TaskRunEvent) (types.TaskRunEvent, error) {
	if strings.TrimSpace(event.RunID) == "" {
		return types.TaskRunEvent{}, fmt.Errorf("run id is required")
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	payload, err := json.Marshal(event.Data)
	if err != nil {
		return types.TaskRunEvent{}, err
	}
	// modernc.org/sqlite ships SQLite >= 3.35, so RETURNING works. We
	// could also use LastInsertId() here, but RETURNING keeps the insert
	// and readback in one statement.
	//
	// We format the timestamp as RFC3339Nano text rather than binding a
	// time.Time directly: the driver's default time-to-text mapping
	// (`2006-01-02 15:04:05.999999999 -0700 MST`) doesn't lex-compare
	// with RFC3339Nano cutoffs, which breaks the retention sweep.
	// RFC3339Nano sorts chronologically when both sides use UTC, and
	// it round-trips through parseSQLiteTime cleanly.
	var id int64
	err = s.db.QueryRowContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (task_id, run_id, event_type, event_data, request_id, trace_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		RETURNING sequence
	`, s.eventsTable), event.TaskID, event.RunID, event.EventType, string(payload), event.RequestID, event.TraceID, event.CreatedAt.UTC().Format(time.RFC3339Nano)).Scan(&id)
	if err != nil {
		return types.TaskRunEvent{}, err
	}
	event.Sequence = id
	event.ID = fmt.Sprintf("%d", id)
	s.signalRun(event.RunID)
	return event, nil
}

func (s *SQLiteStore) ApplyRunTerminalTransition(ctx context.Context, tr TerminalRunTransition) (TerminalRunTransitionResult, error) {
	if err := validateTerminalTransition(tr); err != nil {
		return TerminalRunTransitionResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TerminalRunTransitionResult{}, err
	}
	defer tx.Rollback()

	storedTask, err := s.sqliteGetTaskTx(ctx, tx, tr.Task.ID)
	if err != nil {
		return TerminalRunTransitionResult{}, err
	}
	storedRun, err := s.sqliteGetRunTx(ctx, tx, tr.Task.ID, tr.Run.ID)
	if err != nil {
		return TerminalRunTransitionResult{}, err
	}
	storedResolutionApproval := types.TaskApproval{}
	if tr.ApprovalResolution != nil {
		storedResolutionApproval, err = s.sqliteGetApprovalTx(ctx, tx, tr.Task.ID, tr.Run.ID, tr.ApprovalResolution.ApprovalID)
		if err != nil {
			return TerminalRunTransitionResult{}, err
		}
		if storedRun.Status != "awaiting_approval" || storedResolutionApproval.Status != "pending" {
			steps, listErr := s.sqliteListStepsTx(ctx, tx, storedRun.ID)
			if listErr != nil {
				return TerminalRunTransitionResult{}, listErr
			}
			artifacts, listErr := s.sqliteListArtifactsTx(ctx, tx, ArtifactFilter{TaskID: tr.Task.ID, RunID: storedRun.ID}, "")
			if listErr != nil {
				return TerminalRunTransitionResult{}, listErr
			}
			return TerminalRunTransitionResult{
				Task: storedTask, Run: storedRun, Approval: storedResolutionApproval,
				Steps: steps, Artifacts: artifacts,
			}, nil
		}
	}
	storedRunTerminal := types.IsTerminalTaskRunStatus(storedRun.Status)
	trustedDifferentTerminalReplay := storedRunTerminal && storedRun.Status != tr.Run.Status && tr.TrustedSupplementalRunMetadata != nil
	if storedRunTerminal && storedRun.Status != tr.Run.Status && !trustedDifferentTerminalReplay {
		steps, listErr := s.sqliteListStepsTx(ctx, tx, storedRun.ID)
		if listErr != nil {
			return TerminalRunTransitionResult{}, listErr
		}
		artifacts, listErr := s.sqliteListArtifactsTx(ctx, tx, ArtifactFilter{TaskID: tr.Task.ID, RunID: storedRun.ID}, "")
		if listErr != nil {
			return TerminalRunTransitionResult{}, listErr
		}
		return TerminalRunTransitionResult{Task: storedTask, Run: storedRun, Steps: steps, Artifacts: artifacts}, nil
	}

	finishedAt := terminalTransitionFinishedAt(tr)
	task := tr.Task
	run := tr.Run
	terminalEvent := tr.TerminalEvent
	taskUpdatedEvent := tr.TaskUpdatedEvent
	activeStepError := tr.ActiveStepError
	activeStepResult := tr.ActiveStepResult
	activeStepErrorKind := tr.ActiveStepErrorKind
	pendingApprovalResolutionNote := tr.PendingApprovalResolutionNote
	cancelActiveSteps := tr.CancelActiveSteps
	cancelStreamingArtifacts := tr.CancelStreamingArtifacts
	cancelPendingApprovals := tr.CancelPendingApprovals
	pendingApprovalStatus := tr.PendingApprovalStatus
	pendingApprovalResolvedBy := tr.PendingApprovalResolvedBy
	if tr.ApprovalResolution != nil {
		terminalEvent = nil
		taskUpdatedEvent = nil
		cancelActiveSteps = true
		cancelStreamingArtifacts = true
		cancelPendingApprovals = true
		activeStepError = run.LastError
		activeStepResult = "error"
		activeStepErrorKind = "run_cancelled"
		pendingApprovalStatus = "cancelled"
		pendingApprovalResolvedBy = "system"
		pendingApprovalResolutionNote = run.LastError
	}
	sameTerminalReplay := storedRunTerminal && storedRun.Status == tr.Run.Status
	terminalReplay := sameTerminalReplay || trustedDifferentTerminalReplay
	if trustedDifferentTerminalReplay {
		task = storedTask
		run = mergeTrustedTerminalRunMetadata(storedRun, tr.TrustedSupplementalRunMetadata)
		terminalEvent = nil
		taskUpdatedEvent = nil
	} else if sameTerminalReplay {
		task = storedTask
		run = mergeTrustedTerminalRunMetadata(storedRun, tr.TrustedSupplementalRunMetadata)
		terminalEvent = nil
		taskUpdatedEvent = nil
		activeStepError = run.LastError
		pendingApprovalResolutionNote = run.LastError
	} else if tr.PreserveTaskProjection {
		task = storedTask
	} else {
		if task.UpdatedAt.IsZero() {
			task.UpdatedAt = finishedAt
		}
		if task.FinishedAt.IsZero() {
			task.FinishedAt = finishedAt
		}
	}
	if !terminalReplay && run.FinishedAt.IsZero() {
		run.FinishedAt = finishedAt
	}
	resolvedApproval := storedResolutionApproval
	if tr.ApprovalResolution != nil {
		resolvedApproval, err = mergeApprovalResolution(storedResolutionApproval, *tr.ApprovalResolution)
		if err != nil {
			return TerminalRunTransitionResult{}, err
		}
	}
	if !terminalReplay {
		applied, err := s.sqliteUpdateRunIfNonTerminalTx(ctx, tx, run)
		if err != nil {
			return TerminalRunTransitionResult{}, err
		}
		if !applied {
			currentTask, getErr := s.sqliteGetTaskTx(ctx, tx, tr.Task.ID)
			if getErr != nil {
				return TerminalRunTransitionResult{}, getErr
			}
			currentRun, getErr := s.sqliteGetRunTx(ctx, tx, tr.Task.ID, tr.Run.ID)
			if getErr != nil {
				return TerminalRunTransitionResult{}, getErr
			}
			if !types.IsTerminalTaskRunStatus(currentRun.Status) {
				return TerminalRunTransitionResult{Task: currentTask, Run: currentRun}, nil
			}
			if tr.ApprovalResolution != nil {
				return TerminalRunTransitionResult{Task: currentTask, Run: currentRun, Approval: storedResolutionApproval}, nil
			}
			sameTerminalReplay = currentRun.Status == tr.Run.Status
			trustedDifferentTerminalReplay = currentRun.Status != tr.Run.Status && tr.TrustedSupplementalRunMetadata != nil
			if !sameTerminalReplay && !trustedDifferentTerminalReplay {
				return TerminalRunTransitionResult{Task: currentTask, Run: currentRun}, nil
			}
			// A concurrent terminal transition won after the reads above. Keep
			// its authoritative run/task and events. Same-status replays may
			// clean up late children; different-status trusted executor replays
			// may only merge route/accounting metadata.
			terminalReplay = true
			task = currentTask
			run = mergeTrustedTerminalRunMetadata(currentRun, tr.TrustedSupplementalRunMetadata)
			terminalEvent = nil
			taskUpdatedEvent = nil
			activeStepError = run.LastError
			pendingApprovalResolutionNote = run.LastError
		}
		if !terminalReplay && !tr.PreserveTaskProjection {
			if err := s.sqliteUpdateTaskTx(ctx, tx, task); err != nil {
				return TerminalRunTransitionResult{}, err
			}
		}
	}
	if terminalReplay {
		if err := s.sqliteUpdateSameTerminalRunTx(ctx, tx, run); err != nil {
			return TerminalRunTransitionResult{}, err
		}
	}
	if tr.ApprovalResolution != nil {
		if err := s.sqliteUpdateApprovalTx(ctx, tx, resolvedApproval); err != nil {
			return TerminalRunTransitionResult{}, err
		}
	}

	cancelledApprovals := make([]types.TaskApproval, 0)
	if cancelPendingApprovals && !trustedDifferentTerminalReplay {
		pending, err := s.sqliteListApprovalsTx(ctx, tx, task.ID, run.ID, "pending")
		if err != nil {
			return TerminalRunTransitionResult{}, err
		}
		status := firstNonEmptyString(pendingApprovalStatus, "cancelled")
		resolvedBy := firstNonEmptyString(pendingApprovalResolvedBy, "system")
		note := firstNonEmptyString(pendingApprovalResolutionNote, run.LastError)
		for _, approval := range pending {
			approval.Status = status
			approval.ResolvedBy = resolvedBy
			approval.ResolutionNote = note
			approval.ResolvedAt = terminalChildSettlementTime(finishedAt, approval.CreatedAt)
			if err := s.sqliteUpdateApprovalTx(ctx, tx, approval); err != nil {
				return TerminalRunTransitionResult{}, err
			}
			cancelledApprovals = append(cancelledApprovals, approval)
		}
	}

	if cancelActiveSteps && !trustedDifferentTerminalReplay {
		active, err := s.sqliteListActiveStepsTx(ctx, tx, run.ID)
		if err != nil {
			return TerminalRunTransitionResult{}, err
		}
		result := firstNonEmptyString(activeStepResult, "error")
		errorKind := firstNonEmptyString(activeStepErrorKind, "run_cancelled")
		stepError := firstNonEmptyString(activeStepError, run.LastError)
		for _, step := range active {
			step.Status = "cancelled"
			step.Result = result
			step.Error = stepError
			step.ErrorKind = errorKind
			step.FinishedAt = terminalChildSettlementTime(finishedAt, step.StartedAt)
			if err := s.sqliteUpdateStepTx(ctx, tx, step); err != nil {
				return TerminalRunTransitionResult{}, err
			}
		}
	}

	if cancelStreamingArtifacts && !trustedDifferentTerminalReplay {
		streaming, err := s.sqliteListArtifactsTx(ctx, tx, ArtifactFilter{TaskID: task.ID, RunID: run.ID}, "streaming")
		if err != nil {
			return TerminalRunTransitionResult{}, err
		}
		for _, artifact := range streaming {
			artifact.Status = "cancelled"
			if err := s.sqliteUpdateArtifactTx(ctx, tx, artifact); err != nil {
				return TerminalRunTransitionResult{}, err
			}
		}
	}

	steps, err := s.sqliteListStepsTx(ctx, tx, run.ID)
	if err != nil {
		return TerminalRunTransitionResult{}, err
	}
	artifacts, err := s.sqliteListArtifactsTx(ctx, tx, ArtifactFilter{TaskID: task.ID, RunID: run.ID}, "")
	if err != nil {
		return TerminalRunTransitionResult{}, err
	}
	events := make([]types.TaskRunEvent, 0, len(cancelledApprovals)+3)
	approvalEventType := firstNonEmptyString(tr.ApprovalResolvedEventType, runtimeevents.EventApprovalResolved.String())
	if tr.ApprovalResolution != nil {
		approvalEventType = runtimeevents.EventApprovalResolved.String()
	}
	for _, approval := range cancelledApprovals {
		event := types.TaskRunEvent{
			TaskID:    task.ID,
			RunID:     run.ID,
			EventType: approvalEventType,
			Data:      runtimeevents.ApprovalResolved(approval),
			RequestID: run.RequestID,
			TraceID:   run.TraceID,
			CreatedAt: approval.ResolvedAt,
		}
		inserted, err := s.sqliteInsertRunEventTx(ctx, tx, event)
		if err != nil {
			return TerminalRunTransitionResult{}, err
		}
		events = append(events, inserted)
	}
	if tr.ApprovalResolution != nil {
		inserted, err := s.sqliteInsertRunEventTx(ctx, tx, rejectedApprovalTerminalEvent(task.ID, *tr.ApprovalResolution, run, finishedAt))
		if err != nil {
			return TerminalRunTransitionResult{}, err
		}
		events = append(events, inserted)
		inserted, err = s.sqliteInsertRunEventTx(ctx, tx, rejectedApprovalTaskUpdatedEvent(task.ID, *tr.ApprovalResolution, run.ID, finishedAt))
		if err != nil {
			return TerminalRunTransitionResult{}, err
		}
		events = append(events, inserted)
	} else if terminalEvent != nil {
		event := runStateEventFromSpec(*terminalEvent, task.ID, run, steps, artifacts, finishedAt)
		inserted, err := s.sqliteInsertRunEventTx(ctx, tx, event)
		if err != nil {
			return TerminalRunTransitionResult{}, err
		}
		events = append(events, inserted)
	}
	if tr.ApprovalResolution == nil && taskUpdatedEvent != nil {
		event := runStateEventFromSpec(*taskUpdatedEvent, task.ID, run, steps, artifacts, finishedAt)
		inserted, err := s.sqliteInsertRunEventTx(ctx, tx, event)
		if err != nil {
			return TerminalRunTransitionResult{}, err
		}
		events = append(events, inserted)
	}
	if tr.ApprovalResolution != nil {
		event := approvalResolutionEvent(task.ID, *tr.ApprovalResolution, resolvedApproval, run, steps, artifacts)
		inserted, err := s.sqliteInsertRunEventTx(ctx, tx, event)
		if err != nil {
			return TerminalRunTransitionResult{}, err
		}
		events = append(events, inserted)
	}

	if err := tx.Commit(); err != nil {
		return TerminalRunTransitionResult{}, err
	}
	// The transition writes through tx helpers that don't signal; wake
	// stream subscribers now that the commit succeeded.
	s.signalRun(run.ID)
	return TerminalRunTransitionResult{
		Task:               task,
		Run:                run,
		Approval:           resolvedApproval,
		Steps:              steps,
		Artifacts:          artifacts,
		CancelledApprovals: cancelledApprovals,
		Events:             events,
		Applied:            true,
	}, nil
}

func (s *SQLiteStore) ListRunEvents(ctx context.Context, taskID, runID string, afterSequence int64, limit int) ([]types.TaskRunEvent, error) {
	if limit <= 0 {
		limit = 200
	}
	args := []any{runID, afterSequence}
	query := fmt.Sprintf(`
		SELECT sequence, task_id, run_id, event_type, event_data, created_at, request_id, trace_id
		FROM %s
		WHERE run_id = ? AND sequence > ?
	`, s.eventsTable)
	if taskID != "" {
		args = append(args, taskID)
		query += " AND task_id = ?"
	}
	args = append(args, limit)
	query += " ORDER BY sequence ASC LIMIT ?"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]types.TaskRunEvent, 0)
	for rows.Next() {
		var event types.TaskRunEvent
		var payload sql.NullString
		// modernc.org/sqlite hands back TEXT-typed timestamps as Go
		// strings — scanning directly into time.Time fails. Take the
		// string and parse it ourselves.
		var createdAt sql.NullString
		if err := rows.Scan(&event.Sequence, &event.TaskID, &event.RunID, &event.EventType, &payload, &createdAt, &event.RequestID, &event.TraceID); err != nil {
			return nil, err
		}
		if payload.Valid && payload.String != "" {
			_ = json.Unmarshal([]byte(payload.String), &event.Data)
		}
		if createdAt.Valid && createdAt.String != "" {
			event.CreatedAt = parseSQLiteTime(createdAt.String)
		}
		event.ID = fmt.Sprintf("%d", event.Sequence)
		items = append(items, event)
	}
	return items, rows.Err()
}

func (s *SQLiteStore) ListEvents(ctx context.Context, filter EventFilter) ([]types.TaskRunEvent, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 200
	}
	args := []any{filter.AfterSequence}
	query := fmt.Sprintf(`
		SELECT sequence, task_id, run_id, event_type, event_data, created_at, request_id, trace_id
		FROM %s
		WHERE sequence > ?
	`, s.eventsTable)
	if len(filter.EventTypes) > 0 {
		query += " AND event_type IN (" + sqlitePlaceholders(len(filter.EventTypes)) + ")"
		for _, t := range filter.EventTypes {
			args = append(args, t)
		}
	}
	if len(filter.TaskIDs) > 0 {
		query += " AND task_id IN (" + sqlitePlaceholders(len(filter.TaskIDs)) + ")"
		for _, id := range filter.TaskIDs {
			args = append(args, id)
		}
	}
	args = append(args, limit)
	query += " ORDER BY sequence ASC LIMIT ?"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]types.TaskRunEvent, 0)
	for rows.Next() {
		var event types.TaskRunEvent
		var payload sql.NullString
		var createdAt sql.NullString
		if err := rows.Scan(&event.Sequence, &event.TaskID, &event.RunID, &event.EventType, &payload, &createdAt, &event.RequestID, &event.TraceID); err != nil {
			return nil, err
		}
		if payload.Valid && payload.String != "" {
			_ = json.Unmarshal([]byte(payload.String), &event.Data)
		}
		if createdAt.Valid && createdAt.String != "" {
			event.CreatedAt = parseSQLiteTime(createdAt.String)
		}
		event.ID = fmt.Sprintf("%d", event.Sequence)
		items = append(items, event)
	}
	return items, rows.Err()
}

// Prune drops `turn.completed` rows older than maxAge
// or, when maxCount > 0, beyond the most recent maxCount rows
// (ordered by sequence DESC). Other event types are preserved.
func (s *SQLiteStore) Prune(ctx context.Context, maxAge time.Duration, maxCount int) (int, error) {
	deleted := int64(0)

	if maxAge > 0 {
		cutoff := time.Now().UTC().Add(-maxAge).Format(time.RFC3339Nano)
		// created_at is stored as RFC3339Nano text; lexicographic
		// comparison matches chronological ordering within a single
		// timezone (we always write UTC).
		result, err := s.db.ExecContext(ctx, fmt.Sprintf(`
			DELETE FROM %s
			WHERE event_type = 'turn.completed' AND created_at < ?
		`, s.eventsTable), cutoff)
		if err != nil {
			return 0, fmt.Errorf("delete aged sqlite turn events: %w", err)
		}
		count, _ := result.RowsAffected()
		deleted += count
	}

	if maxCount > 0 {
		// Mirrors the cache_sqlite trick: keep the most-recent N rows
		// of this event type and delete the rest. LIMIT -1 OFFSET ?
		// returns "all rows starting at index maxCount" — the tail.
		limitOffset := "LIMIT -1 OFFSET ?"
		if s.client != nil && s.client.Dialect() == storage.DialectPostgres {
			limitOffset = "OFFSET ?"
		}
		result, err := s.db.ExecContext(ctx, fmt.Sprintf(`
			DELETE FROM %s
			WHERE event_type = 'turn.completed'
			  AND sequence IN (
			    SELECT sequence
			    FROM %s
			    WHERE event_type = 'turn.completed'
			    ORDER BY sequence DESC
			    `+limitOffset+`
			  )
		`, s.eventsTable, s.eventsTable), maxCount)
		if err != nil {
			return 0, fmt.Errorf("enforce %s turn-event max count: %w", s.backend, err)
		}
		count, _ := result.RowsAffected()
		deleted += count
	}

	return int(deleted), nil
}

func sqliteRequireRow(ctx context.Context, tx storage.Tx, query string, args ...any) error {
	var found int
	err := tx.QueryRowContext(ctx, query, args...).Scan(&found)
	if err == sql.ErrNoRows {
		return fmt.Errorf("required row not found")
	}
	return err
}

func (s *SQLiteStore) sqliteUpdateTaskTx(ctx context.Context, tx storage.Tx, task types.Task) error {
	payload, err := json.Marshal(task)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s
		SET status = ?, updated_at = ?, payload = ?
		WHERE id = ?
	`, s.tasksTable), task.Status, task.UpdatedAt, string(payload), task.ID)
	if err != nil {
		return err
	}
	return sqliteRequireRowsAffected(res, "task", task.ID)
}

func (s *SQLiteStore) sqliteUpdateRunTx(ctx context.Context, tx storage.Tx, run types.TaskRun) error {
	payload, err := json.Marshal(run)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s
		SET status = ?, started_at = ?, payload = ?
		WHERE id = ? AND task_id = ?
	`, s.runsTable), run.Status, run.StartedAt, string(payload), run.ID, run.TaskID)
	if err != nil {
		return err
	}
	return sqliteRequireRowsAffected(res, "run", run.ID)
}

func (s *SQLiteStore) sqliteUpdateRunIfNonTerminalTx(ctx context.Context, tx storage.Tx, run types.TaskRun) (bool, error) {
	payload, err := json.Marshal(run)
	if err != nil {
		return false, err
	}
	res, err := tx.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s
		SET status = ?, started_at = ?, payload = ?
		WHERE id = ? AND task_id = ?
		  AND status NOT IN ('completed', 'failed', 'cancelled')
	`, s.runsTable), run.Status, run.StartedAt, string(payload), run.ID, run.TaskID)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (s *SQLiteStore) sqliteUpdateSameTerminalRunTx(ctx context.Context, tx storage.Tx, run types.TaskRun) error {
	payload, err := json.Marshal(run)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s
		SET started_at = ?, payload = ?
		WHERE id = ? AND task_id = ? AND status = ?
	`, s.runsTable), run.StartedAt, string(payload), run.ID, run.TaskID, run.Status)
	if err != nil {
		return err
	}
	return sqliteRequireRowsAffected(res, "run", run.ID)
}

func (s *SQLiteStore) sqliteUpdateStepTx(ctx context.Context, tx storage.Tx, step types.TaskStep) error {
	payload, err := json.Marshal(step)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s
		SET status = ?, payload = ?
		WHERE id = ? AND run_id = ?
	`, s.stepsTable), step.Status, string(payload), step.ID, step.RunID)
	if err != nil {
		return err
	}
	return sqliteRequireRowsAffected(res, "step", step.ID)
}

func (s *SQLiteStore) sqliteUpdateApprovalTx(ctx context.Context, tx storage.Tx, approval types.TaskApproval) error {
	payload, err := json.Marshal(approval)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s
		SET status = ?, payload = ?
		WHERE id = ? AND task_id = ?
	`, s.approvalsTable), approval.Status, string(payload), approval.ID, approval.TaskID)
	if err != nil {
		return err
	}
	return sqliteRequireRowsAffected(res, "approval", approval.ID)
}

func (s *SQLiteStore) sqliteUpdateArtifactTx(ctx context.Context, tx storage.Tx, artifact types.TaskArtifact) error {
	payload, err := json.Marshal(artifact)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s
		SET status = ?, payload = ?
		WHERE id = ? AND task_id = ?
	`, s.artifactsTable), artifact.Status, string(payload), artifact.ID, artifact.TaskID)
	if err != nil {
		return err
	}
	return sqliteRequireRowsAffected(res, "artifact", artifact.ID)
}

func sqliteRequireRowsAffected(res sql.Result, kind, id string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%s %q not found", kind, id)
	}
	return nil
}

func (s *SQLiteStore) sqliteListApprovalsTx(ctx context.Context, tx storage.Tx, taskID, runID, status string) ([]types.TaskApproval, error) {
	args := []any{taskID, runID}
	query := fmt.Sprintf(`
		SELECT payload
		FROM %s
		WHERE task_id = ? AND run_id = ?
	`, s.approvalsTable)
	if status != "" {
		args = append(args, status)
		query += " AND status = ?"
	}
	query += " ORDER BY created_at ASC, id ASC"
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]types.TaskApproval, 0)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var approval types.TaskApproval
		if err := json.Unmarshal([]byte(payload), &approval); err != nil {
			return nil, err
		}
		items = append(items, approval)
	}
	return items, rows.Err()
}

func (s *SQLiteStore) sqliteListActiveStepsTx(ctx context.Context, tx storage.Tx, runID string) ([]types.TaskStep, error) {
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`
		SELECT payload
		FROM %s
		WHERE run_id = ? AND status IN ('running', 'awaiting_approval')
		ORDER BY step_index ASC, id ASC
	`, s.stepsTable), runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSQLiteSteps(rows)
}

func (s *SQLiteStore) sqliteListStepsTx(ctx context.Context, tx storage.Tx, runID string) ([]types.TaskStep, error) {
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`
		SELECT payload
		FROM %s
		WHERE run_id = ?
		ORDER BY step_index ASC, id ASC
	`, s.stepsTable), runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSQLiteSteps(rows)
}

func scanSQLiteSteps(rows *sql.Rows) ([]types.TaskStep, error) {
	items := make([]types.TaskStep, 0)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var step types.TaskStep
		if err := json.Unmarshal([]byte(payload), &step); err != nil {
			return nil, err
		}
		items = append(items, step)
	}
	return items, rows.Err()
}

func (s *SQLiteStore) sqliteListArtifactsTx(ctx context.Context, tx storage.Tx, filter ArtifactFilter, status string) ([]types.TaskArtifact, error) {
	args := []any{}
	where := []string{"1=1"}
	if filter.TaskID != "" {
		args = append(args, filter.TaskID)
		where = append(where, "task_id = ?")
	}
	if filter.RunID != "" {
		args = append(args, filter.RunID)
		where = append(where, "run_id = ?")
	}
	if filter.StepID != "" {
		args = append(args, filter.StepID)
		where = append(where, "step_id = ?")
	}
	if filter.Kind != "" {
		args = append(args, filter.Kind)
		where = append(where, "kind = ?")
	}
	if status != "" {
		args = append(args, status)
		where = append(where, "status = ?")
	}
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`
		SELECT payload
		FROM %s
		WHERE %s
		ORDER BY created_at DESC, id DESC
	`, s.artifactsTable, strings.Join(where, " AND ")), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]types.TaskArtifact, 0)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var artifact types.TaskArtifact
		if err := json.Unmarshal([]byte(payload), &artifact); err != nil {
			return nil, err
		}
		items = append(items, artifact)
	}
	return items, rows.Err()
}

func (s *SQLiteStore) sqliteInsertRunEventTx(ctx context.Context, tx storage.Tx, event types.TaskRunEvent) (types.TaskRunEvent, error) {
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	payload, err := json.Marshal(event.Data)
	if err != nil {
		return types.TaskRunEvent{}, err
	}
	var id int64
	err = tx.QueryRowContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (task_id, run_id, event_type, event_data, request_id, trace_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		RETURNING sequence
	`, s.eventsTable), event.TaskID, event.RunID, event.EventType, string(payload), event.RequestID, event.TraceID, event.CreatedAt.UTC().Format(time.RFC3339Nano)).Scan(&id)
	if err != nil {
		return types.TaskRunEvent{}, err
	}
	event.Sequence = id
	event.ID = fmt.Sprintf("%d", id)
	return event, nil
}

// sqlitePlaceholders returns "?, ?, ?" for n placeholders. Keeps the
// query builder concise without pulling in a third-party SQL helper.
func sqlitePlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	out := strings.Repeat("?, ", n)
	return out[:len(out)-2]
}

// parseSQLiteTime accepts the timestamp shapes that database/sql can
// produce when round-tripping a time.Time through a TEXT column on
// modernc.org/sqlite. The default driver writes RFC3339Nano-ish; we also
// accept the SQLite-canonical "2006-01-02 15:04:05.999999999-07:00"
// form and a plain RFC3339 fallback. A failed parse returns the zero
// value rather than an error — listings should not blow up over a
// stale row with a malformed timestamp.
func parseSQLiteTime(value string) time.Time {
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(layout, value); err == nil {
			return t
		}
	}
	return time.Time{}
}

func (s *SQLiteStore) migrate(ctx context.Context) error {
	// Index names use the unquoted table identifier (SQLite tolerates
	// quoted index names but the convention in internal/retention/
	// history_sqlite.go is unquoted index identifiers paired with a
	// quoted target table).
	tasksUnquoted := strings.Trim(s.tasksTable, `"`)
	runsUnquoted := strings.Trim(s.runsTable, `"`)
	stepsUnquoted := strings.Trim(s.stepsTable, `"`)
	approvalsUnquoted := strings.Trim(s.approvalsTable, `"`)
	artifactsUnquoted := strings.Trim(s.artifactsTable, `"`)
	eventsUnquoted := strings.Trim(s.eventsTable, `"`)
	timestampColumn := storage.TimestampColumnDefaultZero(s.client)

	statements := []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (id TEXT PRIMARY KEY, status TEXT NOT NULL DEFAULT '', updated_at %s, payload TEXT NOT NULL)`, s.tasksTable, timestampColumn),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "%s_status_updated_idx" ON %s (status, updated_at DESC)`, tasksUnquoted, s.tasksTable),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (id TEXT PRIMARY KEY, task_id TEXT NOT NULL, number INTEGER NOT NULL DEFAULT 0, status TEXT NOT NULL DEFAULT '', started_at %s, payload TEXT NOT NULL)`, s.runsTable, timestampColumn),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "%s_task_number_started_idx" ON %s (task_id, number DESC, started_at DESC)`, runsUnquoted, s.runsTable),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "%s_status_started_idx" ON %s (status, started_at DESC)`, runsUnquoted, s.runsTable),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (id TEXT PRIMARY KEY, task_id TEXT NOT NULL, run_id TEXT NOT NULL, step_index INTEGER NOT NULL DEFAULT 0, status TEXT NOT NULL DEFAULT '', started_at %s, payload TEXT NOT NULL)`, s.stepsTable, timestampColumn),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "%s_run_idx_idx" ON %s (run_id, step_index ASC, id ASC)`, stepsUnquoted, s.stepsTable),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (id TEXT PRIMARY KEY, task_id TEXT NOT NULL, run_id TEXT NOT NULL, status TEXT NOT NULL DEFAULT '', created_at %s, payload TEXT NOT NULL)`, s.approvalsTable, timestampColumn),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "%s_task_created_idx" ON %s (task_id, created_at DESC, id DESC)`, approvalsUnquoted, s.approvalsTable),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (id TEXT PRIMARY KEY, task_id TEXT NOT NULL, run_id TEXT NOT NULL, step_id TEXT NOT NULL DEFAULT '', kind TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT '', created_at %s, payload TEXT NOT NULL)`, s.artifactsTable, timestampColumn),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "%s_task_run_created_idx" ON %s (task_id, run_id, created_at DESC, id DESC)`, artifactsUnquoted, s.artifactsTable),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (sequence %s, task_id TEXT NOT NULL, run_id TEXT NOT NULL, event_type TEXT NOT NULL DEFAULT '', event_data TEXT NOT NULL DEFAULT '{}', request_id TEXT NOT NULL DEFAULT '', trace_id TEXT NOT NULL DEFAULT '', created_at %s)`, s.eventsTable, storage.AutoIDColumn(s.client), timestampColumn),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "%s_run_sequence_idx" ON %s (run_id, sequence ASC)`, eventsUnquoted, s.eventsTable),
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migrate %s task store: %w", s.backend, err)
		}
	}
	return nil
}
