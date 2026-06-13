package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/hecatehq/hecate/internal/storage"
	modernsqlite "modernc.org/sqlite"
)

type SQLiteStore struct {
	mu         sync.Mutex
	db         storage.DB
	client     storage.SQLClient
	backend    string
	entries    string
	candidates string
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
		db:         client.DB(),
		client:     client,
		backend:    client.Backend(),
		entries:    client.QualifiedTable("memory_entries"),
		candidates: client.QualifiedTable("memory_candidates"),
	}
	if err := store.migrate(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *SQLiteStore) Backend() string {
	return s.backend
}

func (s *SQLiteStore) migrate(ctx context.Context) error {
	timestampColumn := storage.TimestampColumnDefaultZero(s.client)
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
	id TEXT PRIMARY KEY,
	scope TEXT NOT NULL,
	project_id TEXT NOT NULL,
	title TEXT NOT NULL,
	body TEXT NOT NULL,
	trust_label TEXT NOT NULL,
	source_kind TEXT NOT NULL,
	source_id TEXT NOT NULL DEFAULT '',
	enabled INTEGER NOT NULL DEFAULT 1,
	created_at %s,
	updated_at %s
)`, s.entries, timestampColumn, timestampColumn)); err != nil {
		return fmt.Errorf("create memory entries table: %w", err)
	}
	indexName := strings.Trim(s.entries, `"`) + "_scope_idx"
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "%s" ON %s (scope, project_id, enabled, updated_at)`, indexName, s.entries)); err != nil {
		return fmt.Errorf("create memory scope index: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
	id TEXT PRIMARY KEY,
	project_id TEXT NOT NULL,
	title TEXT NOT NULL,
	body TEXT NOT NULL,
	suggested_kind TEXT NOT NULL DEFAULT '',
	suggested_trust_label TEXT NOT NULL,
	suggested_source_kind TEXT NOT NULL,
	suggested_source_id TEXT NOT NULL DEFAULT '',
	source_refs_json TEXT NOT NULL DEFAULT '[]',
	status TEXT NOT NULL,
	status_reason TEXT NOT NULL DEFAULT '',
	promoted_memory_id TEXT NOT NULL DEFAULT '',
	created_at %s,
	updated_at %s
)`, s.candidates, timestampColumn, timestampColumn)); err != nil {
		return fmt.Errorf("create memory candidates table: %w", err)
	}
	candidateIndex := strings.Trim(s.candidates, `"`) + "_project_status_idx"
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "%s" ON %s (project_id, status, updated_at)`, candidateIndex, s.candidates)); err != nil {
		return fmt.Errorf("create memory candidates index: %w", err)
	}
	return nil
}

func (s *SQLiteStore) Create(ctx context.Context, entry Entry) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry = normalizeEntry(entry, time.Now().UTC())
	if err := validateEntry(entry); err != nil {
		return Entry{}, err
	}
	if ok, err := s.idExists(ctx, entry.ID); err != nil {
		return Entry{}, err
	} else if ok {
		return Entry{}, ErrAlreadyExists
	}
	if err := insertEntry(ctx, s.db, s.entries, entry); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

func (s *SQLiteStore) idExists(ctx context.Context, id string) (bool, error) {
	var found string
	err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT id FROM %s WHERE id = ?`, s.entries), strings.TrimSpace(id)).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *SQLiteStore) Get(ctx context.Context, projectID, id string) (Entry, bool, error) {
	row := s.db.QueryRowContext(ctx, fmt.Sprintf(`
SELECT id, scope, project_id, title, body, trust_label, source_kind, source_id,
	enabled, created_at, updated_at
FROM %s
WHERE id = ? AND project_id = ?`, s.entries), strings.TrimSpace(id), strings.TrimSpace(projectID))
	entry, err := scanEntry(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, err
	}
	return entry, true, nil
}

func (s *SQLiteStore) List(ctx context.Context, filter Filter) ([]Entry, error) {
	projectID := strings.TrimSpace(filter.ProjectID)
	query := fmt.Sprintf(`
SELECT id, scope, project_id, title, body, trust_label, source_kind, source_id,
	enabled, created_at, updated_at
FROM %s`, s.entries)
	args := []any{}
	clauses := []string{}
	if projectID != "" {
		clauses = append(clauses, `project_id = ?`)
		args = append(args, projectID)
	}
	if !filter.IncludeDisabled {
		clauses = append(clauses, `enabled != 0`)
	}
	if len(clauses) > 0 {
		query += ` WHERE ` + strings.Join(clauses, ` AND `)
	}
	query += ` ORDER BY enabled DESC, updated_at DESC, title ASC, id ASC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Entry
	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, entry)
	}
	return items, rows.Err()
}

func (s *SQLiteStore) Update(ctx context.Context, projectID, id string, update func(*Entry)) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	projectID = strings.TrimSpace(projectID)
	id = strings.TrimSpace(id)
	entry, ok, err := s.Get(ctx, projectID, id)
	if err != nil {
		return Entry{}, err
	}
	if !ok {
		return Entry{}, ErrNotFound
	}
	originalID := entry.ID
	originalProjectID := entry.ProjectID
	originalScope := entry.Scope
	originalCreatedAt := entry.CreatedAt
	if update != nil {
		update(&entry)
	}
	if strings.TrimSpace(entry.ID) != originalID {
		return Entry{}, fmt.Errorf("%w: memory entry id cannot be changed", ErrInvalid)
	}
	if strings.TrimSpace(entry.ProjectID) != originalProjectID {
		return Entry{}, fmt.Errorf("%w: project_id cannot be changed", ErrInvalid)
	}
	if strings.TrimSpace(entry.Scope) != originalScope {
		return Entry{}, fmt.Errorf("%w: scope cannot be changed", ErrInvalid)
	}
	entry.ID = originalID
	entry.ProjectID = originalProjectID
	entry.Scope = originalScope
	entry.CreatedAt = originalCreatedAt
	entry.UpdatedAt = time.Now().UTC()
	entry = normalizeEntry(entry, entry.UpdatedAt)
	if err := validateEntry(entry); err != nil {
		return Entry{}, err
	}
	res, err := s.db.ExecContext(ctx, fmt.Sprintf(`
UPDATE %s
SET title = ?, body = ?, trust_label = ?, source_kind = ?, source_id = ?,
	enabled = ?, updated_at = ?
WHERE id = ? AND project_id = ?`, s.entries),
		entry.Title,
		entry.Body,
		entry.TrustLabel,
		entry.SourceKind,
		entry.SourceID,
		boolToDB(entry.Enabled),
		formatTime(entry.UpdatedAt),
		entry.ID,
		entry.ProjectID,
	)
	if err != nil {
		return Entry{}, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return Entry{}, err
	}
	if affected == 0 {
		return Entry{}, ErrNotFound
	}
	return entry, nil
}

func (s *SQLiteStore) Delete(ctx context.Context, projectID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id = ? AND project_id = ?`, s.entries), strings.TrimSpace(id), strings.TrimSpace(projectID))
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLiteStore) DeleteByProjectID(ctx context.Context, projectID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE project_id = ?`, s.entries), strings.TrimSpace(projectID))
	if err != nil {
		return 0, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(affected), nil
}

func insertEntry(ctx context.Context, execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, table string, entry Entry) error {
	if _, err := execer.ExecContext(ctx, fmt.Sprintf(`
INSERT INTO %s (
	id, scope, project_id, title, body, trust_label, source_kind, source_id,
	enabled, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, table),
		entry.ID,
		entry.Scope,
		entry.ProjectID,
		entry.Title,
		entry.Body,
		entry.TrustLabel,
		entry.SourceKind,
		entry.SourceID,
		boolToDB(entry.Enabled),
		formatTime(entry.CreatedAt),
		formatTime(entry.UpdatedAt),
	); err != nil {
		if isSQLiteConstraintError(err) {
			return ErrAlreadyExists
		}
		return err
	}
	return nil
}

func (s *SQLiteStore) CreateCandidate(ctx context.Context, candidate Candidate) (Candidate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	candidate = normalizeCandidate(candidate, time.Now().UTC())
	if err := validateCandidate(candidate); err != nil {
		return Candidate{}, err
	}
	if ok, err := s.candidateIDExists(ctx, candidate.ID); err != nil {
		return Candidate{}, err
	} else if ok {
		return Candidate{}, ErrAlreadyExists
	}
	refsJSON, err := encodeSourceRefs(candidate.SourceRefs)
	if err != nil {
		return Candidate{}, err
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`
INSERT INTO %s (
	id, project_id, title, body, suggested_kind, suggested_trust_label,
	suggested_source_kind, suggested_source_id, source_refs_json, status,
	status_reason, promoted_memory_id, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, s.candidates),
		candidate.ID,
		candidate.ProjectID,
		candidate.Title,
		candidate.Body,
		candidate.SuggestedKind,
		candidate.SuggestedTrustLabel,
		candidate.SuggestedSourceKind,
		candidate.SuggestedSourceID,
		refsJSON,
		candidate.Status,
		candidate.StatusReason,
		candidate.PromotedMemoryID,
		formatTime(candidate.CreatedAt),
		formatTime(candidate.UpdatedAt),
	); err != nil {
		if isSQLiteConstraintError(err) {
			return Candidate{}, ErrAlreadyExists
		}
		return Candidate{}, err
	}
	return candidate, nil
}

func (s *SQLiteStore) candidateIDExists(ctx context.Context, id string) (bool, error) {
	var found string
	err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT id FROM %s WHERE id = ?`, s.candidates), strings.TrimSpace(id)).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *SQLiteStore) GetCandidate(ctx context.Context, projectID, id string) (Candidate, bool, error) {
	row := s.db.QueryRowContext(ctx, fmt.Sprintf(`
SELECT id, project_id, title, body, suggested_kind, suggested_trust_label,
	suggested_source_kind, suggested_source_id, source_refs_json, status,
	status_reason, promoted_memory_id, created_at, updated_at
FROM %s
WHERE id = ? AND project_id = ?`, s.candidates), strings.TrimSpace(id), strings.TrimSpace(projectID))
	candidate, err := scanCandidate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Candidate{}, false, nil
	}
	if err != nil {
		return Candidate{}, false, err
	}
	return candidate, true, nil
}

func (s *SQLiteStore) ListCandidates(ctx context.Context, filter CandidateFilter) ([]Candidate, error) {
	projectID := strings.TrimSpace(filter.ProjectID)
	status := strings.TrimSpace(filter.Status)
	query := fmt.Sprintf(`
SELECT id, project_id, title, body, suggested_kind, suggested_trust_label,
	suggested_source_kind, suggested_source_id, source_refs_json, status,
	status_reason, promoted_memory_id, created_at, updated_at
FROM %s`, s.candidates)
	args := []any{}
	clauses := []string{}
	if projectID != "" {
		clauses = append(clauses, `project_id = ?`)
		args = append(args, projectID)
	}
	if status != "" {
		clauses = append(clauses, `status = ?`)
		args = append(args, status)
	}
	if len(clauses) > 0 {
		query += ` WHERE ` + strings.Join(clauses, ` AND `)
	}
	query += ` ORDER BY CASE status WHEN 'pending' THEN 0 WHEN 'promoted' THEN 1 WHEN 'rejected' THEN 2 ELSE 3 END, updated_at DESC, title ASC, id ASC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Candidate
	for rows.Next() {
		candidate, err := scanCandidate(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, candidate)
	}
	return items, rows.Err()
}

func (s *SQLiteStore) UpdateCandidate(ctx context.Context, projectID, id string, update func(*Candidate)) (Candidate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	projectID = strings.TrimSpace(projectID)
	id = strings.TrimSpace(id)
	candidate, ok, err := s.GetCandidate(ctx, projectID, id)
	if err != nil {
		return Candidate{}, err
	}
	if !ok {
		return Candidate{}, ErrNotFound
	}
	originalID := candidate.ID
	originalProjectID := candidate.ProjectID
	originalCreatedAt := candidate.CreatedAt
	if update != nil {
		update(&candidate)
	}
	if strings.TrimSpace(candidate.ID) != originalID {
		return Candidate{}, fmt.Errorf("%w: memory candidate id cannot be changed", ErrInvalid)
	}
	if strings.TrimSpace(candidate.ProjectID) != originalProjectID {
		return Candidate{}, fmt.Errorf("%w: project_id cannot be changed", ErrInvalid)
	}
	candidate.ID = originalID
	candidate.ProjectID = originalProjectID
	candidate.CreatedAt = originalCreatedAt
	candidate.UpdatedAt = time.Now().UTC()
	candidate = normalizeCandidate(candidate, candidate.UpdatedAt)
	if err := validateCandidate(candidate); err != nil {
		return Candidate{}, err
	}
	refsJSON, err := encodeSourceRefs(candidate.SourceRefs)
	if err != nil {
		return Candidate{}, err
	}
	res, err := s.db.ExecContext(ctx, fmt.Sprintf(`
UPDATE %s
SET title = ?, body = ?, suggested_kind = ?, suggested_trust_label = ?,
	suggested_source_kind = ?, suggested_source_id = ?, source_refs_json = ?,
	status = ?, status_reason = ?, promoted_memory_id = ?, updated_at = ?
WHERE id = ? AND project_id = ?`, s.candidates),
		candidate.Title,
		candidate.Body,
		candidate.SuggestedKind,
		candidate.SuggestedTrustLabel,
		candidate.SuggestedSourceKind,
		candidate.SuggestedSourceID,
		refsJSON,
		candidate.Status,
		candidate.StatusReason,
		candidate.PromotedMemoryID,
		formatTime(candidate.UpdatedAt),
		candidate.ID,
		candidate.ProjectID,
	)
	if err != nil {
		return Candidate{}, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return Candidate{}, err
	}
	if affected == 0 {
		return Candidate{}, ErrNotFound
	}
	return candidate, nil
}

func (s *SQLiteStore) PromoteCandidate(ctx context.Context, projectID, id string, entry Entry) (Candidate, Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	projectID = strings.TrimSpace(projectID)
	id = strings.TrimSpace(id)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Candidate{}, Entry{}, err
	}
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRowContext(ctx, fmt.Sprintf(`
SELECT id, project_id, title, body, suggested_kind, suggested_trust_label,
	suggested_source_kind, suggested_source_id, source_refs_json, status,
	status_reason, promoted_memory_id, created_at, updated_at
FROM %s
WHERE id = ? AND project_id = ?`, s.candidates), id, projectID)
	candidate, err := scanCandidate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Candidate{}, Entry{}, ErrNotFound
	}
	if err != nil {
		return Candidate{}, Entry{}, err
	}
	if candidate.Status == CandidateStatusPromoted && candidate.PromotedMemoryID != "" {
		row := tx.QueryRowContext(ctx, fmt.Sprintf(`
SELECT id, scope, project_id, title, body, trust_label, source_kind, source_id,
	enabled, created_at, updated_at
FROM %s
WHERE id = ? AND project_id = ?`, s.entries), candidate.PromotedMemoryID, projectID)
		promoted, err := scanEntry(row)
		if errors.Is(err, sql.ErrNoRows) {
			return Candidate{}, Entry{}, ErrConflict
		}
		if err != nil {
			return Candidate{}, Entry{}, err
		}
		return candidate, promoted, nil
	}
	if candidate.Status != CandidateStatusPending {
		return Candidate{}, Entry{}, ErrConflict
	}

	entry.ProjectID = projectID
	entry.Scope = ScopeProject
	entry = normalizeEntry(entry, now)
	if err := validateEntry(entry); err != nil {
		return Candidate{}, Entry{}, err
	}
	if err := insertEntry(ctx, tx, s.entries, entry); err != nil {
		return Candidate{}, Entry{}, err
	}

	candidate.Status = CandidateStatusPromoted
	candidate.StatusReason = ""
	candidate.PromotedMemoryID = entry.ID
	candidate.UpdatedAt = now
	candidate = normalizeCandidate(candidate, now)
	if err := validateCandidate(candidate); err != nil {
		return Candidate{}, Entry{}, err
	}
	res, err := tx.ExecContext(ctx, fmt.Sprintf(`
UPDATE %s
SET status = ?, status_reason = ?, promoted_memory_id = ?, updated_at = ?
WHERE id = ? AND project_id = ? AND status = ?`, s.candidates),
		candidate.Status,
		candidate.StatusReason,
		candidate.PromotedMemoryID,
		formatTime(candidate.UpdatedAt),
		candidate.ID,
		candidate.ProjectID,
		CandidateStatusPending,
	)
	if err != nil {
		return Candidate{}, Entry{}, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return Candidate{}, Entry{}, err
	}
	if affected == 0 {
		return Candidate{}, Entry{}, ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return Candidate{}, Entry{}, err
	}
	return candidate, entry, nil
}

func (s *SQLiteStore) DeleteCandidatesByProjectID(ctx context.Context, projectID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE project_id = ?`, s.candidates), strings.TrimSpace(projectID))
	if err != nil {
		return 0, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(affected), nil
}

type entryScanner interface {
	Scan(dest ...any) error
}

func scanEntry(scanner entryScanner) (Entry, error) {
	var entry Entry
	var enabled int
	var createdAt, updatedAt string
	if err := scanner.Scan(
		&entry.ID,
		&entry.Scope,
		&entry.ProjectID,
		&entry.Title,
		&entry.Body,
		&entry.TrustLabel,
		&entry.SourceKind,
		&entry.SourceID,
		&enabled,
		&createdAt,
		&updatedAt,
	); err != nil {
		return Entry{}, err
	}
	entry.Enabled = enabled != 0
	var err error
	if entry.CreatedAt, err = parseTime(createdAt); err != nil {
		return Entry{}, err
	}
	if entry.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

func scanCandidate(scanner entryScanner) (Candidate, error) {
	var candidate Candidate
	var refsJSON, createdAt, updatedAt string
	if err := scanner.Scan(
		&candidate.ID,
		&candidate.ProjectID,
		&candidate.Title,
		&candidate.Body,
		&candidate.SuggestedKind,
		&candidate.SuggestedTrustLabel,
		&candidate.SuggestedSourceKind,
		&candidate.SuggestedSourceID,
		&refsJSON,
		&candidate.Status,
		&candidate.StatusReason,
		&candidate.PromotedMemoryID,
		&createdAt,
		&updatedAt,
	); err != nil {
		return Candidate{}, err
	}
	refs, err := decodeSourceRefs(refsJSON)
	if err != nil {
		return Candidate{}, err
	}
	candidate.SourceRefs = refs
	if candidate.CreatedAt, err = parseTime(createdAt); err != nil {
		return Candidate{}, err
	}
	if candidate.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return Candidate{}, err
	}
	return candidate, nil
}

func isSQLiteConstraintError(err error) bool {
	var sqliteErr *modernsqlite.Error
	if errors.As(err, &sqliteErr) {
		return sqliteErr.Code()&0xff == 19
	}
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func encodeSourceRefs(refs []CandidateSourceRef) (string, error) {
	refs = normalizeSourceRefs(refs)
	if refs == nil {
		return "[]", nil
	}
	data, err := json.Marshal(refs)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func decodeSourceRefs(value string) ([]CandidateSourceRef, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	var refs []CandidateSourceRef
	if err := json.Unmarshal([]byte(value), &refs); err != nil {
		return nil, err
	}
	return normalizeSourceRefs(refs), nil
}

func boolToDB(value bool) int {
	if value {
		return 1
	}
	return 0
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
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
