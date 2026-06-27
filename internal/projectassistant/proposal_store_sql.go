package projectassistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/storage"
)

type SQLProposalStore struct {
	client    storage.SQLClient
	backend   string
	proposals string
	attempts  string
}

func NewSQLiteProposalStore(ctx context.Context, client *storage.SQLiteClient) (*SQLProposalStore, error) {
	return newSQLProposalStore(ctx, client)
}

func NewPostgresProposalStore(ctx context.Context, client *storage.PostgresClient) (*SQLProposalStore, error) {
	return newSQLProposalStore(ctx, client)
}

func newSQLProposalStore(ctx context.Context, client storage.SQLClient) (*SQLProposalStore, error) {
	if client == nil || client.DB() == nil {
		return nil, errors.New("sql client is required")
	}
	store := &SQLProposalStore{
		client:    client,
		backend:   client.Backend(),
		proposals: client.QualifiedTable("project_assistant_proposals"),
		attempts:  client.QualifiedTable("project_assistant_apply_attempts"),
	}
	if err := store.migrate(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *SQLProposalStore) Backend() string { return s.backend }

func (s *SQLProposalStore) migrate(ctx context.Context) error {
	if _, err := s.client.DB().ExecContext(ctx, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
	id TEXT PRIMARY KEY,
	project_id TEXT NOT NULL DEFAULT '',
	source TEXT NOT NULL DEFAULT '',
	source_id TEXT NOT NULL DEFAULT '',
	proposal TEXT NOT NULL,
	fingerprint TEXT NOT NULL,
	status TEXT NOT NULL DEFAULT 'proposed',
	latest_result TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	applied_at TEXT NOT NULL DEFAULT ''
)`, s.proposals)); err != nil {
		return fmt.Errorf("create project assistant proposals table: %w", err)
	}
	if _, err := s.client.DB().ExecContext(ctx, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
	id TEXT PRIMARY KEY,
	proposal_id TEXT NOT NULL,
	status TEXT NOT NULL,
	confirmed INTEGER NOT NULL DEFAULT 0,
	result TEXT NOT NULL DEFAULT '{}',
	error_type TEXT NOT NULL DEFAULT '',
	error_message TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL
)`, s.attempts)); err != nil {
		return fmt.Errorf("create project assistant apply attempts table: %w", err)
	}
	if _, err := s.client.DB().ExecContext(ctx, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "%s" ON %s (project_id)`, strings.Trim(s.proposals, `"`)+"_project_idx", s.proposals)); err != nil {
		return fmt.Errorf("create project assistant proposals project index: %w", err)
	}
	if _, err := s.client.DB().ExecContext(ctx, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "%s" ON %s (proposal_id, created_at, id)`, strings.Trim(s.attempts, `"`)+"_proposal_idx", s.attempts)); err != nil {
		return fmt.Errorf("create project assistant attempts proposal index: %w", err)
	}
	return nil
}

func (s *SQLProposalStore) UpsertProposal(ctx context.Context, record ProposalRecord) (ProposalRecord, error) {
	now := time.Now().UTC()
	if existing, ok, err := s.getProposalOnly(ctx, strings.TrimSpace(record.ID)); err != nil {
		return ProposalRecord{}, err
	} else if ok {
		record.CreatedAt = existing.CreatedAt
		if record.LatestResult == nil {
			record.LatestResult = cloneApplyResultPtr(existing.LatestResult)
		}
		if record.AppliedAt == nil {
			record.AppliedAt = cloneTimePtr(existing.AppliedAt)
		}
		if strings.TrimSpace(record.Status) == "" {
			record.Status = existing.Status
		}
	}
	record = normalizeProposalRecord(record, now)
	if err := validateProposalRecord(record); err != nil {
		return ProposalRecord{}, err
	}
	if err := s.upsertProposal(ctx, s.client.DB(), record); err != nil {
		return ProposalRecord{}, err
	}
	loaded, _, err := s.GetProposal(ctx, record.ID)
	return loaded, err
}

func (s *SQLProposalStore) GetProposal(ctx context.Context, id string) (ProposalRecord, bool, error) {
	record, ok, err := s.getProposalOnly(ctx, strings.TrimSpace(id))
	if err != nil || !ok {
		return ProposalRecord{}, ok, err
	}
	attempts, err := s.listAttempts(ctx, record.ID)
	if err != nil {
		return ProposalRecord{}, false, err
	}
	record.ApplyAttempts = attempts
	return cloneProposalRecord(record), true, nil
}

func (s *SQLProposalStore) ListProposals(ctx context.Context, projectID string) ([]ProposalRecord, error) {
	query := fmt.Sprintf(`
SELECT id, project_id, source, source_id, proposal, fingerprint, status, latest_result,
	created_at, updated_at, applied_at
FROM %s`, s.proposals)
	var args []any
	projectID = strings.TrimSpace(projectID)
	if projectID != "" {
		query += ` WHERE project_id = ?`
		args = append(args, projectID)
	}
	query += ` ORDER BY updated_at DESC, id ASC`
	rows, err := s.client.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []ProposalRecord
	for rows.Next() {
		record, err := scanProposalRecord(rows)
		if err != nil {
			return nil, err
		}
		attempts, err := s.listAttempts(ctx, record.ID)
		if err != nil {
			return nil, err
		}
		record.ApplyAttempts = attempts
		records = append(records, cloneProposalRecord(record))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func (s *SQLProposalStore) UpdateProposalApplyState(ctx context.Context, proposalID string, result ApplyResult) (ProposalRecord, error) {
	proposalID = strings.TrimSpace(proposalID)
	record, ok, err := s.getProposalOnly(ctx, proposalID)
	if err != nil {
		return ProposalRecord{}, err
	}
	if !ok {
		return ProposalRecord{}, ErrNotFound
	}
	record = applyResultToProposalRecord(record, result, time.Now().UTC())
	if err := s.upsertProposal(ctx, s.client.DB(), record); err != nil {
		return ProposalRecord{}, err
	}
	loaded, _, err := s.GetProposal(ctx, proposalID)
	return loaded, err
}

func (s *SQLProposalStore) RecordApplyAttempt(ctx context.Context, attempt ApplyAttempt) (ProposalRecord, error) {
	proposalID := strings.TrimSpace(attempt.ProposalID)
	tx, err := s.client.DB().BeginTx(ctx, nil)
	if err != nil {
		return ProposalRecord{}, err
	}
	record, ok, err := s.getProposalOnlyWith(ctx, tx, proposalID)
	if err != nil {
		_ = tx.Rollback()
		return ProposalRecord{}, err
	}
	if !ok {
		_ = tx.Rollback()
		return ProposalRecord{}, ErrNotFound
	}
	now := time.Now().UTC()
	attempt = normalizeApplyAttempt(attempt, now)
	if err := validateApplyAttempt(attempt); err != nil {
		_ = tx.Rollback()
		return ProposalRecord{}, err
	}
	record = applyResultToProposalRecord(record, attempt.Result, now)
	if err := s.upsertProposal(ctx, tx, record); err != nil {
		_ = tx.Rollback()
		return ProposalRecord{}, err
	}
	if err := s.insertAttempt(ctx, tx, attempt); err != nil {
		_ = tx.Rollback()
		return ProposalRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return ProposalRecord{}, err
	}
	loaded, _, err := s.GetProposal(ctx, proposalID)
	return loaded, err
}

func (s *SQLProposalStore) DeleteProject(ctx context.Context, projectID string) (int, error) {
	projectID = strings.TrimSpace(projectID)
	rows, err := s.client.DB().QueryContext(ctx, fmt.Sprintf(`SELECT id FROM %s WHERE project_id = ?`, s.proposals), projectID)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	tx, err := s.client.DB().BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	args := make([]any, len(ids))
	for idx, id := range ids {
		args[idx] = id
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE proposal_id IN (%s)`, s.attempts, storage.Placeholders(len(ids))), args...); err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id IN (%s)`, s.proposals, storage.Placeholders(len(ids))), args...); err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(ids), nil
}

func (s *SQLProposalStore) Clear(ctx context.Context) (int, error) {
	tx, err := s.client.DB().BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s`, s.proposals))
	if err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s`, s.attempts)); err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(affected), nil
}

type proposalSQLExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *SQLProposalStore) upsertProposal(ctx context.Context, execer proposalSQLExecer, record ProposalRecord) error {
	proposalRaw, err := encodeProposalJSON(record.Proposal)
	if err != nil {
		return err
	}
	latestResult := ""
	if record.LatestResult != nil {
		raw, err := encodeApplyResultJSON(*record.LatestResult)
		if err != nil {
			return err
		}
		latestResult = string(raw)
	}
	_, err = execer.ExecContext(ctx, fmt.Sprintf(`
INSERT INTO %s (
	id, project_id, source, source_id, proposal, fingerprint, status, latest_result,
	created_at, updated_at, applied_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	project_id = excluded.project_id,
	source = excluded.source,
	source_id = excluded.source_id,
	proposal = excluded.proposal,
	fingerprint = excluded.fingerprint,
	status = excluded.status,
	latest_result = excluded.latest_result,
	updated_at = excluded.updated_at,
	applied_at = excluded.applied_at`, s.proposals),
		record.ID,
		record.ProjectID,
		record.Source,
		record.SourceID,
		string(proposalRaw),
		record.Fingerprint,
		record.Status,
		latestResult,
		formatProposalStoreTime(record.CreatedAt),
		formatProposalStoreTime(record.UpdatedAt),
		formatProposalStoreTimePtr(record.AppliedAt),
	)
	return err
}

func (s *SQLProposalStore) insertAttempt(ctx context.Context, execer proposalSQLExecer, attempt ApplyAttempt) error {
	raw, err := encodeApplyResultJSON(attempt.Result)
	if err != nil {
		return err
	}
	_, err = execer.ExecContext(ctx, fmt.Sprintf(`
INSERT INTO %s (
	id, proposal_id, status, confirmed, result, error_type, error_message, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, s.attempts),
		attempt.ID,
		attempt.ProposalID,
		attempt.Status,
		boolToProposalStoreDB(attempt.Confirmed),
		string(raw),
		attempt.ErrorType,
		attempt.ErrorMessage,
		formatProposalStoreTime(attempt.CreatedAt),
	)
	return err
}

func (s *SQLProposalStore) getProposalOnly(ctx context.Context, id string) (ProposalRecord, bool, error) {
	return s.getProposalOnlyWith(ctx, s.client.DB(), id)
}

func (s *SQLProposalStore) getProposalOnlyWith(ctx context.Context, queryer proposalSQLQueryer, id string) (ProposalRecord, bool, error) {
	row := queryer.QueryRowContext(ctx, fmt.Sprintf(`
SELECT id, project_id, source, source_id, proposal, fingerprint, status, latest_result,
	created_at, updated_at, applied_at
FROM %s
WHERE id = ?`, s.proposals), strings.TrimSpace(id))
	record, err := scanProposalRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ProposalRecord{}, false, nil
	}
	if err != nil {
		return ProposalRecord{}, false, err
	}
	return record, true, nil
}

type proposalSQLQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *SQLProposalStore) listAttempts(ctx context.Context, proposalID string) ([]ApplyAttempt, error) {
	rows, err := s.client.DB().QueryContext(ctx, fmt.Sprintf(`
SELECT id, proposal_id, status, confirmed, result, error_type, error_message, created_at
FROM %s
WHERE proposal_id = ?
ORDER BY created_at ASC, id ASC`, s.attempts), strings.TrimSpace(proposalID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var attempts []ApplyAttempt
	for rows.Next() {
		attempt, err := scanApplyAttempt(rows)
		if err != nil {
			return nil, err
		}
		attempts = append(attempts, attempt)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return attempts, nil
}

type proposalRecordScanner interface {
	Scan(dest ...any) error
}

func scanProposalRecord(row proposalRecordScanner) (ProposalRecord, error) {
	var record ProposalRecord
	var proposalRaw, latestRaw, createdAt, updatedAt, appliedAt string
	if err := row.Scan(
		&record.ID,
		&record.ProjectID,
		&record.Source,
		&record.SourceID,
		&proposalRaw,
		&record.Fingerprint,
		&record.Status,
		&latestRaw,
		&createdAt,
		&updatedAt,
		&appliedAt,
	); err != nil {
		return ProposalRecord{}, err
	}
	if err := json.Unmarshal([]byte(proposalRaw), &record.Proposal); err != nil {
		return ProposalRecord{}, fmt.Errorf("%w: decode proposal record: %v", ErrInvalid, err)
	}
	if strings.TrimSpace(latestRaw) != "" {
		var result ApplyResult
		if err := json.Unmarshal([]byte(latestRaw), &result); err != nil {
			return ProposalRecord{}, fmt.Errorf("%w: decode proposal latest result: %v", ErrInvalid, err)
		}
		record.LatestResult = &result
	}
	var err error
	if record.CreatedAt, err = parseProposalStoreTime(createdAt); err != nil {
		return ProposalRecord{}, err
	}
	if record.UpdatedAt, err = parseProposalStoreTime(updatedAt); err != nil {
		return ProposalRecord{}, err
	}
	if parsed, err := parseProposalStoreTime(appliedAt); err != nil {
		return ProposalRecord{}, err
	} else if !parsed.IsZero() {
		record.AppliedAt = &parsed
	}
	return normalizeProposalRecord(record, record.UpdatedAt), nil
}

type applyAttemptScanner interface {
	Scan(dest ...any) error
}

func scanApplyAttempt(row applyAttemptScanner) (ApplyAttempt, error) {
	var attempt ApplyAttempt
	var confirmed int
	var resultRaw, createdAt string
	if err := row.Scan(
		&attempt.ID,
		&attempt.ProposalID,
		&attempt.Status,
		&confirmed,
		&resultRaw,
		&attempt.ErrorType,
		&attempt.ErrorMessage,
		&createdAt,
	); err != nil {
		return ApplyAttempt{}, err
	}
	attempt.Confirmed = confirmed != 0
	if strings.TrimSpace(resultRaw) != "" {
		if err := json.Unmarshal([]byte(resultRaw), &attempt.Result); err != nil {
			return ApplyAttempt{}, fmt.Errorf("%w: decode proposal apply attempt result: %v", ErrInvalid, err)
		}
	}
	var err error
	if attempt.CreatedAt, err = parseProposalStoreTime(createdAt); err != nil {
		return ApplyAttempt{}, err
	}
	return normalizeApplyAttempt(attempt, attempt.CreatedAt), nil
}

func boolToProposalStoreDB(value bool) int {
	if value {
		return 1
	}
	return 0
}

func formatProposalStoreTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func formatProposalStoreTimePtr(value *time.Time) string {
	if value == nil {
		return ""
	}
	return formatProposalStoreTime(*value)
}

func parseProposalStoreTime(value string) (time.Time, error) {
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
