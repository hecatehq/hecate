package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hecatehq/hecate/internal/storage"
)

type SQLiteStore struct {
	mu      sync.Mutex
	db      *sql.DB
	entries string
}

func NewSQLiteStore(ctx context.Context, client *storage.SQLiteClient) (*SQLiteStore, error) {
	if client == nil || client.DB() == nil {
		return nil, fmt.Errorf("sqlite client is required")
	}
	store := &SQLiteStore{
		db:      client.DB(),
		entries: client.QualifiedTable("memory_entries"),
	}
	if err := store.migrate(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *SQLiteStore) Backend() string {
	return "sqlite"
}

func (s *SQLiteStore) migrate(ctx context.Context) error {
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
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
)`, s.entries)); err != nil {
		return fmt.Errorf("create memory entries table: %w", err)
	}
	indexName := strings.Trim(s.entries, `"`) + "_scope_idx"
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "%s" ON %s (scope, project_id, enabled, updated_at)`, indexName, s.entries)); err != nil {
		return fmt.Errorf("create memory scope index: %w", err)
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
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`
INSERT INTO %s (
	id, scope, project_id, title, body, trust_label, source_kind, source_id,
	enabled, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, s.entries),
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
		return Entry{}, err
	}
	return entry, nil
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
FROM %s
WHERE project_id = ?`, s.entries)
	args := []any{projectID}
	if !filter.IncludeDisabled {
		query += ` AND enabled != 0`
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
