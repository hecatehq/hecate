package projectskills

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
	if client == nil {
		return nil, errors.New("sql client is required")
	}
	store := &SQLiteStore{
		client:  client,
		backend: client.Backend(),
		table:   client.QualifiedTable("project_skills"),
	}
	if err := store.migrate(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *SQLiteStore) Backend() string { return s.backend }

func (s *SQLiteStore) migrate(ctx context.Context) error {
	timestampColumn := storage.TimestampColumnDefaultZero(s.client)
	_, err := s.client.DB().ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS `+s.table+` (
	id TEXT NOT NULL,
	project_id TEXT NOT NULL,
	title TEXT NOT NULL DEFAULT '',
	description TEXT NOT NULL DEFAULT '',
	path TEXT NOT NULL DEFAULT '',
	root_id TEXT NOT NULL DEFAULT '',
	format TEXT NOT NULL DEFAULT 'skill_md',
	enabled INTEGER NOT NULL DEFAULT 1,
	status TEXT NOT NULL DEFAULT 'available',
	trust_label TEXT NOT NULL DEFAULT 'workspace_skill',
	source_context_source_ids TEXT NOT NULL DEFAULT '[]',
	warnings TEXT NOT NULL DEFAULT '[]',
	discovered_at `+timestampColumn+`,
	created_at `+timestampColumn+`,
	updated_at `+timestampColumn+`,
	PRIMARY KEY(project_id, id)
)`)
	return err
}

func (s *SQLiteStore) List(ctx context.Context, projectID string) ([]Skill, error) {
	rows, err := s.client.DB().QueryContext(ctx, selectProjectSkillSQL(s.table)+`
WHERE project_id = ?
ORDER BY enabled DESC,
	CASE status
		WHEN 'available' THEN 0
		WHEN 'conflict' THEN 1
		WHEN 'invalid' THEN 2
		WHEN 'missing' THEN 3
		ELSE 4
	END ASC,
	title ASC,
	path ASC,
	id ASC`, strings.TrimSpace(projectID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Skill
	for rows.Next() {
		item, err := scanSkill(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *SQLiteStore) UpsertDiscovered(ctx context.Context, projectID string, discovered []Skill) ([]Skill, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, ErrInvalid
	}
	existing, err := s.List(ctx, projectID)
	if err != nil {
		return nil, err
	}
	merged := mergeDiscoveredSkills(existing, discovered, projectID, time.Now().UTC())
	tx, err := s.client.DB().BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if len(merged) == 0 {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE project_id = ?`, s.table), projectID); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
	} else {
		deleteArgs := make([]any, 0, len(merged)+1)
		deleteArgs = append(deleteArgs, projectID)
		placeholders := make([]string, 0, len(merged))
		for _, item := range merged {
			if err := validateSkill(item); err != nil {
				_ = tx.Rollback()
				return nil, err
			}
			if err := s.upsertWith(ctx, tx, item); err != nil {
				_ = tx.Rollback()
				return nil, err
			}
			placeholders = append(placeholders, "?")
			deleteArgs = append(deleteArgs, item.ID)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(
			`DELETE FROM %s WHERE project_id = ? AND id NOT IN (%s)`,
			s.table,
			strings.Join(placeholders, ", "),
		), deleteArgs...); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.List(ctx, projectID)
}

func (s *SQLiteStore) Update(ctx context.Context, projectID, id string, update func(*Skill)) (Skill, error) {
	projectID = strings.TrimSpace(projectID)
	id = strings.TrimSpace(id)
	item, ok, err := s.get(ctx, projectID, id)
	if err != nil {
		return Skill{}, err
	}
	if !ok {
		return Skill{}, ErrNotFound
	}
	originalID := item.ID
	originalProjectID := item.ProjectID
	createdAt := item.CreatedAt
	discoveredAt := item.DiscoveredAt
	if update != nil {
		update(&item)
	}
	item.ID = originalID
	item.ProjectID = originalProjectID
	item.CreatedAt = createdAt
	item.DiscoveredAt = discoveredAt
	item.UpdatedAt = time.Now().UTC()
	item = normalizeSkill(item, item.UpdatedAt)
	if err := validateSkill(item); err != nil {
		return Skill{}, err
	}
	if err := s.upsert(ctx, item); err != nil {
		return Skill{}, err
	}
	return cloneSkill(item), nil
}

func (s *SQLiteStore) DeleteProject(ctx context.Context, projectID string) (int, error) {
	res, err := s.client.DB().ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE project_id = ?`, s.table), strings.TrimSpace(projectID))
	if err != nil {
		return 0, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(affected), nil
}

func (s *SQLiteStore) Clear(ctx context.Context) (int, error) {
	res, err := s.client.DB().ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s`, s.table))
	if err != nil {
		return 0, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(affected), nil
}

func (s *SQLiteStore) get(ctx context.Context, projectID, id string) (Skill, bool, error) {
	row := s.client.DB().QueryRowContext(ctx, selectProjectSkillSQL(s.table)+"WHERE project_id = ? AND id = ?", projectID, id)
	item, err := scanSkill(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Skill{}, false, nil
	}
	if err != nil {
		return Skill{}, false, err
	}
	return item, true, nil
}

func (s *SQLiteStore) upsert(ctx context.Context, item Skill) error {
	return s.upsertWith(ctx, s.client.DB(), item)
}

type sqliteExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (s *SQLiteStore) upsertWith(ctx context.Context, execer sqliteExecer, item Skill) error {
	_, err := execer.ExecContext(ctx, fmt.Sprintf(`
INSERT INTO %s (
	id, project_id, title, description, path, root_id, format, enabled, status,
	trust_label, source_context_source_ids, warnings, discovered_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(project_id, id) DO UPDATE SET
	title = excluded.title,
	description = excluded.description,
	path = excluded.path,
	root_id = excluded.root_id,
	format = excluded.format,
	enabled = excluded.enabled,
	status = excluded.status,
	trust_label = excluded.trust_label,
	source_context_source_ids = excluded.source_context_source_ids,
	warnings = excluded.warnings,
	discovered_at = excluded.discovered_at,
	updated_at = excluded.updated_at`, s.table),
		item.ID,
		item.ProjectID,
		item.Title,
		item.Description,
		item.Path,
		item.RootID,
		item.Format,
		boolToDB(item.Enabled),
		item.Status,
		item.TrustLabel,
		encodeStringSlice(item.SourceContextSourceIDs),
		encodeStringSlice(item.Warnings),
		formatTime(item.DiscoveredAt),
		formatTime(item.CreatedAt),
		formatTime(item.UpdatedAt),
	)
	return err
}

type skillScanner interface {
	Scan(dest ...any) error
}

func selectProjectSkillSQL(table string) string {
	return fmt.Sprintf(`
SELECT id, project_id, title, description, path, root_id, format, enabled, status,
	trust_label, source_context_source_ids, warnings, discovered_at, created_at, updated_at
FROM %s
`, table)
}

func scanSkill(row skillScanner) (Skill, error) {
	var item Skill
	var enabled int
	var sourceIDsRaw, warningsRaw string
	var discoveredAt, createdAt, updatedAt string
	if err := row.Scan(
		&item.ID,
		&item.ProjectID,
		&item.Title,
		&item.Description,
		&item.Path,
		&item.RootID,
		&item.Format,
		&enabled,
		&item.Status,
		&item.TrustLabel,
		&sourceIDsRaw,
		&warningsRaw,
		&discoveredAt,
		&createdAt,
		&updatedAt,
	); err != nil {
		return Skill{}, err
	}
	item.Enabled = enabled != 0
	item.SourceContextSourceIDs = decodeStringSlice(sourceIDsRaw)
	item.Warnings = decodeStringSlice(warningsRaw)
	var err error
	if item.DiscoveredAt, err = parseTime(discoveredAt); err != nil {
		return Skill{}, err
	}
	if item.CreatedAt, err = parseTime(createdAt); err != nil {
		return Skill{}, err
	}
	if item.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return Skill{}, err
	}
	return normalizeSkill(item, item.UpdatedAt), nil
}

func boolToDB(value bool) int {
	if value {
		return 1
	}
	return 0
}

func encodeStringSlice(items []string) string {
	raw, err := json.Marshal(normalizeStringSlice(items))
	if err != nil {
		return "[]"
	}
	return string(raw)
}

func decodeStringSlice(raw string) []string {
	var items []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &items); err != nil {
		return nil
	}
	return normalizeStringSlice(items)
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
