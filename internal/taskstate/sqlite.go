package taskstate

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hecate/agent-runtime/internal/storage"
	"github.com/hecate/agent-runtime/pkg/types"
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
	db             *sql.DB
	tasksTable     string
	runsTable      string
	stepsTable     string
	approvalsTable string
	artifactsTable string
	eventsTable    string
}

func NewSQLiteStore(ctx context.Context, client *storage.SQLiteClient) (*SQLiteStore, error) {
	if client == nil || client.DB() == nil {
		return nil, fmt.Errorf("sqlite client is required")
	}
	store := &SQLiteStore{
		db:             client.DB(),
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

func (s *SQLiteStore) Backend() string { return "sqlite" }

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
	limitSQL := ""
	if filter.Limit > 0 {
		args = append(args, filter.Limit)
		limitSQL = "LIMIT ?"
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT payload
		FROM %s
		WHERE %s
		ORDER BY number DESC, started_at DESC
		%s
	`, s.runsTable, strings.Join(where, " AND "), limitSQL), args...)
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
	return approval, true, nil
}

func (s *SQLiteStore) UpdatePendingApprovalForAwaitingRun(ctx context.Context, approval types.TaskApproval) (types.TaskApproval, bool, error) {
	if strings.TrimSpace(approval.ID) == "" {
		return types.TaskApproval{}, false, fmt.Errorf("approval id is required")
	}
	if strings.TrimSpace(approval.TaskID) == "" {
		return types.TaskApproval{}, false, fmt.Errorf("approval task id is required")
	}
	if strings.TrimSpace(approval.RunID) == "" {
		return types.TaskApproval{}, false, fmt.Errorf("approval run id is required")
	}
	payload, err := json.Marshal(approval)
	if err != nil {
		return types.TaskApproval{}, false, err
	}
	res, err := s.db.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s
		SET status = ?, payload = ?
		WHERE id = ?
		  AND task_id = ?
		  AND status = 'pending'
		  AND EXISTS (
		    SELECT 1
		    FROM %s
		    WHERE id = ?
		      AND task_id = ?
		      AND status = 'awaiting_approval'
		  )
	`, s.approvalsTable, s.runsTable), approval.Status, string(payload), approval.ID, approval.TaskID, approval.RunID, approval.TaskID)
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
	return event, nil
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

// PruneTurnEvents drops `turn.completed` rows older than maxAge
// or, when maxCount > 0, beyond the most recent maxCount rows
// (ordered by sequence DESC). Other event types are preserved.
func (s *SQLiteStore) PruneTurnEvents(ctx context.Context, maxAge time.Duration, maxCount int) (int, error) {
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
		result, err := s.db.ExecContext(ctx, fmt.Sprintf(`
			DELETE FROM %s
			WHERE event_type = 'turn.completed'
			  AND sequence IN (
			    SELECT sequence
			    FROM %s
			    WHERE event_type = 'turn.completed'
			    ORDER BY sequence DESC
			    LIMIT -1 OFFSET ?
			  )
		`, s.eventsTable, s.eventsTable), maxCount)
		if err != nil {
			return 0, fmt.Errorf("enforce sqlite turn-event max count: %w", err)
		}
		count, _ := result.RowsAffected()
		deleted += count
	}

	return int(deleted), nil
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

	statements := []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (id TEXT PRIMARY KEY, status TEXT NOT NULL DEFAULT '', updated_at TEXT NOT NULL DEFAULT '', payload TEXT NOT NULL)`, s.tasksTable),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "%s_status_updated_idx" ON %s (status, updated_at DESC)`, tasksUnquoted, s.tasksTable),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (id TEXT PRIMARY KEY, task_id TEXT NOT NULL, number INTEGER NOT NULL DEFAULT 0, status TEXT NOT NULL DEFAULT '', started_at TEXT NOT NULL DEFAULT '', payload TEXT NOT NULL)`, s.runsTable),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "%s_task_number_started_idx" ON %s (task_id, number DESC, started_at DESC)`, runsUnquoted, s.runsTable),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "%s_status_started_idx" ON %s (status, started_at DESC)`, runsUnquoted, s.runsTable),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (id TEXT PRIMARY KEY, task_id TEXT NOT NULL, run_id TEXT NOT NULL, step_index INTEGER NOT NULL DEFAULT 0, status TEXT NOT NULL DEFAULT '', started_at TEXT NOT NULL DEFAULT '', payload TEXT NOT NULL)`, s.stepsTable),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "%s_run_idx_idx" ON %s (run_id, step_index ASC, id ASC)`, stepsUnquoted, s.stepsTable),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (id TEXT PRIMARY KEY, task_id TEXT NOT NULL, run_id TEXT NOT NULL, status TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL DEFAULT '', payload TEXT NOT NULL)`, s.approvalsTable),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "%s_task_created_idx" ON %s (task_id, created_at DESC, id DESC)`, approvalsUnquoted, s.approvalsTable),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (id TEXT PRIMARY KEY, task_id TEXT NOT NULL, run_id TEXT NOT NULL, step_id TEXT NOT NULL DEFAULT '', kind TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL DEFAULT '', payload TEXT NOT NULL)`, s.artifactsTable),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "%s_task_run_created_idx" ON %s (task_id, run_id, created_at DESC, id DESC)`, artifactsUnquoted, s.artifactsTable),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (sequence INTEGER PRIMARY KEY AUTOINCREMENT, task_id TEXT NOT NULL, run_id TEXT NOT NULL, event_type TEXT NOT NULL DEFAULT '', event_data TEXT NOT NULL DEFAULT '{}', request_id TEXT NOT NULL DEFAULT '', trace_id TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL DEFAULT '')`, s.eventsTable),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "%s_run_sequence_idx" ON %s (run_id, sequence ASC)`, eventsUnquoted, s.eventsTable),
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migrate sqlite task store: %w", err)
		}
	}
	return nil
}
