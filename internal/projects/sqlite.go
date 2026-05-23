package projects

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
	mu          sync.Mutex
	db          *sql.DB
	projectsTbl string
	rootsTbl    string
}

func NewSQLiteStore(ctx context.Context, client *storage.SQLiteClient) (*SQLiteStore, error) {
	if client == nil || client.DB() == nil {
		return nil, fmt.Errorf("sqlite client is required")
	}
	store := &SQLiteStore{
		db:          client.DB(),
		projectsTbl: client.QualifiedTable("projects"),
		rootsTbl:    client.QualifiedTable("project_roots"),
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
	name TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	default_root_id TEXT NOT NULL DEFAULT '',
	default_provider TEXT NOT NULL DEFAULT '',
	default_model TEXT NOT NULL DEFAULT '',
	default_agent_profile TEXT NOT NULL DEFAULT '',
	default_tools_enabled INTEGER,
	default_workspace_mode TEXT NOT NULL DEFAULT '',
	default_system_prompt TEXT NOT NULL DEFAULT '',
	default_compact_tool_output INTEGER,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	last_opened_at TEXT NOT NULL DEFAULT ''
)`, s.projectsTbl)); err != nil {
		return fmt.Errorf("create projects table: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
	id TEXT NOT NULL,
	project_id TEXT NOT NULL,
	path TEXT NOT NULL,
	kind TEXT NOT NULL DEFAULT 'local',
	git_remote TEXT NOT NULL DEFAULT '',
	git_branch TEXT NOT NULL DEFAULT '',
	active INTEGER NOT NULL DEFAULT 1,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	PRIMARY KEY(project_id, id),
	FOREIGN KEY(project_id) REFERENCES %s(id) ON DELETE CASCADE
)`, s.rootsTbl, s.projectsTbl)); err != nil {
		return fmt.Errorf("create project roots table: %w", err)
	}
	rootsProjectIndex := strings.Trim(s.rootsTbl, `"`) + "_project_idx"
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "%s" ON %s (project_id)`, rootsProjectIndex, s.rootsTbl)); err != nil {
		return fmt.Errorf("create project roots project index: %w", err)
	}
	return nil
}

func (s *SQLiteStore) Create(ctx context.Context, project Project) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	project = normalizeProject(project, time.Now().UTC())
	if err := validateProject(project); err != nil {
		return Project{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Project{}, err
	}
	if err := s.upsertProject(ctx, tx, project); err != nil {
		_ = tx.Rollback()
		return Project{}, err
	}
	if err := tx.Commit(); err != nil {
		return Project{}, err
	}
	return s.GetRequired(ctx, project.ID)
}

func (s *SQLiteStore) Get(ctx context.Context, id string) (Project, bool, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Project{}, false, err
	}
	project, err := s.loadProjectWith(ctx, tx, strings.TrimSpace(id))
	if errors.Is(err, ErrNotFound) {
		_ = tx.Rollback()
		return Project{}, false, nil
	}
	if err != nil {
		_ = tx.Rollback()
		return Project{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Project{}, false, err
	}
	return project, true, nil
}

func (s *SQLiteStore) GetRequired(ctx context.Context, id string) (Project, error) {
	project, ok, err := s.Get(ctx, id)
	if err != nil {
		return Project{}, err
	}
	if !ok {
		return Project{}, ErrNotFound
	}
	return project, nil
}

func (s *SQLiteStore) List(ctx context.Context) ([]Project, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`
SELECT id FROM %s
ORDER BY
	CASE WHEN last_opened_at = '' THEN updated_at ELSE last_opened_at END DESC,
	name ASC,
	id ASC`, s.projectsTbl))
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	items := make([]Project, 0, len(ids))
	for _, id := range ids {
		project, err := s.loadProjectWith(ctx, tx, id)
		if err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		items = append(items, project)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *SQLiteStore) Update(ctx context.Context, id string, update func(*Project)) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id = strings.TrimSpace(id)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Project{}, err
	}
	project, err := s.loadProjectWith(ctx, tx, id)
	if err != nil {
		_ = tx.Rollback()
		return Project{}, err
	}
	originalID := project.ID
	originalCreatedAt := project.CreatedAt
	originalRoots := projectRootsByID(project.Roots)
	if update != nil {
		update(&project)
	}
	if strings.TrimSpace(project.ID) != originalID {
		_ = tx.Rollback()
		return Project{}, fmt.Errorf("%w: project id cannot be changed", ErrInvalid)
	}
	project.ID = originalID
	project.CreatedAt = originalCreatedAt
	now := time.Now().UTC()
	project.UpdatedAt = now
	project.Roots = preserveExistingRootTimestamps(project.Roots, originalRoots, now)
	project = normalizeProject(project, now)
	if err := validateProject(project); err != nil {
		_ = tx.Rollback()
		return Project{}, err
	}
	if err := s.upsertProject(ctx, tx, project); err != nil {
		_ = tx.Rollback()
		return Project{}, err
	}
	if err := tx.Commit(); err != nil {
		return Project{}, err
	}
	return s.GetRequired(ctx, id)
}

func (s *SQLiteStore) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE project_id = ?`, s.rootsTbl), strings.TrimSpace(id)); err != nil {
		_ = tx.Rollback()
		return err
	}
	res, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id = ?`, s.projectsTbl), strings.TrimSpace(id))
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if affected == 0 {
		_ = tx.Rollback()
		return ErrNotFound
	}
	return tx.Commit()
}

func (s *SQLiteStore) upsertProject(ctx context.Context, tx *sql.Tx, project Project) error {
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
INSERT INTO %s (
	id, name, description, default_root_id, default_provider, default_model,
	default_agent_profile, default_tools_enabled, default_workspace_mode,
	default_system_prompt, default_compact_tool_output, created_at, updated_at,
	last_opened_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	name = excluded.name,
	description = excluded.description,
	default_root_id = excluded.default_root_id,
	default_provider = excluded.default_provider,
	default_model = excluded.default_model,
	default_agent_profile = excluded.default_agent_profile,
	default_tools_enabled = excluded.default_tools_enabled,
	default_workspace_mode = excluded.default_workspace_mode,
	default_system_prompt = excluded.default_system_prompt,
	default_compact_tool_output = excluded.default_compact_tool_output,
	updated_at = excluded.updated_at,
	last_opened_at = excluded.last_opened_at`, s.projectsTbl),
		project.ID,
		project.Name,
		project.Description,
		project.DefaultRootID,
		project.DefaultProvider,
		project.DefaultModel,
		project.DefaultAgentProfile,
		boolPtrToDB(project.DefaultToolsEnabled),
		project.DefaultWorkspaceMode,
		project.DefaultSystemPrompt,
		boolPtrToDB(project.DefaultCompactToolOutput),
		formatTime(project.CreatedAt),
		formatTime(project.UpdatedAt),
		formatTime(project.LastOpenedAt),
	); err != nil {
		return err
	}
	return s.upsertProjectRoots(ctx, tx, project)
}

func (s *SQLiteStore) upsertProjectRoots(ctx context.Context, tx *sql.Tx, project Project) error {
	if len(project.Roots) == 0 {
		_, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE project_id = ?`, s.rootsTbl), project.ID)
		return err
	}

	deleteArgs := make([]any, 0, len(project.Roots)+1)
	deleteArgs = append(deleteArgs, project.ID)
	placeholders := make([]string, 0, len(project.Roots))
	for _, root := range project.Roots {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
INSERT INTO %s (
	id, project_id, path, kind, git_remote, git_branch, active, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(project_id, id) DO UPDATE SET
	path = excluded.path,
	kind = excluded.kind,
	git_remote = excluded.git_remote,
	git_branch = excluded.git_branch,
	active = excluded.active,
	updated_at = excluded.updated_at`, s.rootsTbl),
			root.ID,
			project.ID,
			root.Path,
			root.Kind,
			root.GitRemote,
			root.GitBranch,
			root.Active,
			formatTime(root.CreatedAt),
			formatTime(root.UpdatedAt),
		); err != nil {
			return err
		}
		placeholders = append(placeholders, "?")
		deleteArgs = append(deleteArgs, root.ID)
	}
	_, err := tx.ExecContext(ctx, fmt.Sprintf(
		`DELETE FROM %s WHERE project_id = ? AND id NOT IN (%s)`,
		s.rootsTbl,
		strings.Join(placeholders, ", "),
	), deleteArgs...)
	return err
}

type sqliteQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *SQLiteStore) loadProjectWith(ctx context.Context, q sqliteQuerier, id string) (Project, error) {
	row := q.QueryRowContext(ctx, fmt.Sprintf(`
SELECT
	id, name, description, default_root_id, default_provider, default_model,
	default_agent_profile, default_tools_enabled, default_workspace_mode,
	default_system_prompt, default_compact_tool_output, created_at, updated_at,
	last_opened_at
FROM %s
WHERE id = ?`, s.projectsTbl), id)
	var project Project
	var toolsEnabled sql.NullInt64
	var compactOutput sql.NullInt64
	var createdAt, updatedAt, lastOpenedAt string
	if err := row.Scan(
		&project.ID,
		&project.Name,
		&project.Description,
		&project.DefaultRootID,
		&project.DefaultProvider,
		&project.DefaultModel,
		&project.DefaultAgentProfile,
		&toolsEnabled,
		&project.DefaultWorkspaceMode,
		&project.DefaultSystemPrompt,
		&compactOutput,
		&createdAt,
		&updatedAt,
		&lastOpenedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Project{}, ErrNotFound
		}
		return Project{}, err
	}
	project.DefaultToolsEnabled = dbBoolToPtr(toolsEnabled)
	project.DefaultCompactToolOutput = dbBoolToPtr(compactOutput)
	var err error
	if project.CreatedAt, err = parseTime(createdAt); err != nil {
		return Project{}, err
	}
	if project.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return Project{}, err
	}
	if lastOpenedAt != "" {
		if project.LastOpenedAt, err = parseTime(lastOpenedAt); err != nil {
			return Project{}, err
		}
	}
	roots, err := s.loadRootsWith(ctx, q, project.ID)
	if err != nil {
		return Project{}, err
	}
	project.Roots = roots
	return project, nil
}

func (s *SQLiteStore) loadRootsWith(ctx context.Context, q sqliteQuerier, projectID string) ([]Root, error) {
	rows, err := q.QueryContext(ctx, fmt.Sprintf(`
SELECT id, path, kind, git_remote, git_branch, active, created_at, updated_at
FROM %s
WHERE project_id = ?
ORDER BY active DESC, path ASC, id ASC`, s.rootsTbl), projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var roots []Root
	for rows.Next() {
		var root Root
		var active int
		var createdAt, updatedAt string
		if err := rows.Scan(
			&root.ID,
			&root.Path,
			&root.Kind,
			&root.GitRemote,
			&root.GitBranch,
			&active,
			&createdAt,
			&updatedAt,
		); err != nil {
			return nil, err
		}
		root.Active = active != 0
		var err error
		if root.CreatedAt, err = parseTime(createdAt); err != nil {
			return nil, err
		}
		if root.UpdatedAt, err = parseTime(updatedAt); err != nil {
			return nil, err
		}
		roots = append(roots, root)
	}
	return roots, rows.Err()
}

func boolPtrToDB(value *bool) any {
	if value == nil {
		return nil
	}
	if *value {
		return 1
	}
	return 0
}

func dbBoolToPtr(value sql.NullInt64) *bool {
	if !value.Valid {
		return nil
	}
	result := value.Int64 != 0
	return &result
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
