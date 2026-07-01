package projectruntime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/storage"
)

type SQLiteStore struct {
	client  storage.SQLClient
	backend string
	table   string
}

func NewSQLiteStore(ctx context.Context, client *storage.SQLiteClient) (*SQLiteStore, error) {
	return newSQLStore(ctx, client)
}

func NewPostgresStore(ctx context.Context, client *storage.PostgresClient) (*SQLiteStore, error) {
	return newSQLStore(ctx, client)
}

func newSQLStore(ctx context.Context, client storage.SQLClient) (*SQLiteStore, error) {
	if client == nil || client.DB() == nil {
		return nil, errors.New("sql client is required")
	}
	store := &SQLiteStore{
		client:  client,
		backend: client.Backend(),
		table:   client.QualifiedTable("project_assignment_runtime"),
	}
	if err := store.migrate(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *SQLiteStore) Backend() string { return s.backend }

func (s *SQLiteStore) migrate(ctx context.Context) error {
	timestampColumn := storage.TimestampColumnDefaultZero(s.client)
	_, err := s.client.DB().ExecContext(ctx, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
	project_id TEXT NOT NULL,
	assignment_id TEXT NOT NULL,
	execution_ref TEXT NOT NULL DEFAULT '{}',
	context_packet TEXT NOT NULL DEFAULT '',
	started_at %s,
	completed_at %s,
	updated_at %s,
	PRIMARY KEY(project_id, assignment_id)
)`, s.table, timestampColumn, timestampColumn, timestampColumn))
	return err
}

func (s *SQLiteStore) Get(ctx context.Context, projectID, assignmentID string) (AssignmentRuntime, bool, error) {
	row := s.client.DB().QueryRowContext(ctx, selectRuntimeSQL(s.table)+` WHERE project_id = ? AND assignment_id = ?`, strings.TrimSpace(projectID), strings.TrimSpace(assignmentID))
	runtime, err := scanRuntime(row)
	if errors.Is(err, sql.ErrNoRows) {
		return AssignmentRuntime{}, false, nil
	}
	if err != nil {
		return AssignmentRuntime{}, false, err
	}
	return cloneRuntime(runtime), true, nil
}

func (s *SQLiteStore) Upsert(ctx context.Context, runtime AssignmentRuntime) (AssignmentRuntime, error) {
	runtime = normalizeRuntime(runtime, time.Now().UTC())
	if err := validateRuntime(runtime); err != nil {
		return AssignmentRuntime{}, err
	}
	encodedRef, err := encodeRuntimeExecutionRef(runtime.ExecutionRef)
	if err != nil {
		return AssignmentRuntime{}, err
	}
	_, err = s.client.DB().ExecContext(ctx, `
INSERT INTO `+s.table+` (
	project_id, assignment_id, execution_ref, context_packet, started_at, completed_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(project_id, assignment_id) DO UPDATE SET
	execution_ref = excluded.execution_ref,
	context_packet = excluded.context_packet,
	started_at = excluded.started_at,
	completed_at = excluded.completed_at,
	updated_at = excluded.updated_at`,
		runtime.ProjectID,
		runtime.AssignmentID,
		encodedRef,
		string(runtime.ContextPacket),
		formatRuntimeTime(runtime.StartedAt),
		formatRuntimeTime(runtime.CompletedAt),
		formatRuntimeTime(runtime.UpdatedAt),
	)
	if err != nil {
		return AssignmentRuntime{}, err
	}
	stored, ok, err := s.Get(ctx, runtime.ProjectID, runtime.AssignmentID)
	if err != nil {
		return AssignmentRuntime{}, err
	}
	if !ok {
		return AssignmentRuntime{}, ErrNotFound
	}
	return stored, nil
}

func (s *SQLiteStore) Delete(ctx context.Context, projectID, assignmentID string) error {
	res, err := s.client.DB().ExecContext(ctx, `DELETE FROM `+s.table+` WHERE project_id = ? AND assignment_id = ?`, strings.TrimSpace(projectID), strings.TrimSpace(assignmentID))
	if err != nil {
		return err
	}
	deleted, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if deleted == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLiteStore) DeleteProject(ctx context.Context, projectID string) (int, error) {
	res, err := s.client.DB().ExecContext(ctx, `DELETE FROM `+s.table+` WHERE project_id = ?`, strings.TrimSpace(projectID))
	if err != nil {
		return 0, err
	}
	deleted, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(deleted), nil
}

func (s *SQLiteStore) Clear(ctx context.Context) (int, error) {
	res, err := s.client.DB().ExecContext(ctx, `DELETE FROM `+s.table)
	if err != nil {
		return 0, err
	}
	deleted, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(deleted), nil
}

func selectRuntimeSQL(table string) string {
	return `SELECT project_id, assignment_id, execution_ref, context_packet, started_at, completed_at, updated_at FROM ` + table
}

type scanner interface {
	Scan(dest ...any) error
}

func scanRuntime(row scanner) (AssignmentRuntime, error) {
	var runtime AssignmentRuntime
	var executionRef, contextPacket, startedAt, completedAt, updatedAt string
	if err := row.Scan(&runtime.ProjectID, &runtime.AssignmentID, &executionRef, &contextPacket, &startedAt, &completedAt, &updatedAt); err != nil {
		return AssignmentRuntime{}, err
	}
	ref, err := decodeRuntimeExecutionRef(executionRef)
	if err != nil {
		return AssignmentRuntime{}, err
	}
	runtime.ExecutionRef = ref
	runtime.ContextPacket = []byte(contextPacket)
	if runtime.StartedAt, err = parseRuntimeTime(startedAt); err != nil {
		return AssignmentRuntime{}, err
	}
	if runtime.CompletedAt, err = parseRuntimeTime(completedAt); err != nil {
		return AssignmentRuntime{}, err
	}
	if runtime.UpdatedAt, err = parseRuntimeTime(updatedAt); err != nil {
		return AssignmentRuntime{}, err
	}
	return cloneRuntime(runtime), nil
}

func encodeRuntimeExecutionRef(ref projectwork.AssignmentExecutionRef) (string, error) {
	ref = projectwork.NormalizeAssignmentExecutionRef(ref)
	data, err := json.Marshal(ref)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func decodeRuntimeExecutionRef(value string) (projectwork.AssignmentExecutionRef, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return projectwork.AssignmentExecutionRef{}, nil
	}
	var ref projectwork.AssignmentExecutionRef
	if err := json.Unmarshal([]byte(value), &ref); err != nil {
		return projectwork.AssignmentExecutionRef{}, err
	}
	return projectwork.NormalizeAssignmentExecutionRef(ref), nil
}

func formatRuntimeTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseRuntimeTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}
