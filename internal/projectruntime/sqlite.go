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
	client               storage.SQLClient
	backend              string
	table                string
	projectDefaultsTable string
	roleDefaultsTable    string
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
		client:               client,
		backend:              client.Backend(),
		table:                client.QualifiedTable("project_assignment_runtime"),
		projectDefaultsTable: client.QualifiedTable("project_runtime_defaults"),
		roleDefaultsTable:    client.QualifiedTable("project_role_runtime_defaults"),
	}
	if err := store.migrate(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *SQLiteStore) Backend() string { return s.backend }

func (s *SQLiteStore) migrate(ctx context.Context) error {
	timestampColumn := storage.TimestampColumnDefaultZero(s.client)
	if _, err := s.client.DB().ExecContext(ctx, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
	project_id TEXT NOT NULL,
	assignment_id TEXT NOT NULL,
	execution_ref TEXT NOT NULL DEFAULT '{}',
	context_packet TEXT NOT NULL DEFAULT '',
	started_at %s,
	completed_at %s,
	updated_at %s,
	PRIMARY KEY(project_id, assignment_id)
)`, s.table, timestampColumn, timestampColumn, timestampColumn)); err != nil {
		return err
	}
	if _, err := s.client.DB().ExecContext(ctx, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
	project_id TEXT NOT NULL PRIMARY KEY,
	default_provider TEXT NOT NULL DEFAULT '',
	default_model TEXT NOT NULL DEFAULT '',
	default_agent_profile TEXT NOT NULL DEFAULT '',
	default_tools_enabled TEXT NOT NULL DEFAULT '',
	default_workspace_mode TEXT NOT NULL DEFAULT '',
	default_system_prompt TEXT NOT NULL DEFAULT '',
	default_compact_tool_output TEXT NOT NULL DEFAULT '',
	updated_at %s
)`, s.projectDefaultsTable, timestampColumn)); err != nil {
		return err
	}
	_, err := s.client.DB().ExecContext(ctx, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
	project_id TEXT NOT NULL,
	role_id TEXT NOT NULL,
	default_provider TEXT NOT NULL DEFAULT '',
	default_model TEXT NOT NULL DEFAULT '',
	default_agent_profile TEXT NOT NULL DEFAULT '',
	updated_at %s,
	PRIMARY KEY(project_id, role_id)
)`, s.roleDefaultsTable, timestampColumn))
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

func (s *SQLiteStore) GetProjectDefaults(ctx context.Context, projectID string) (ProjectDefaults, bool, error) {
	row := s.client.DB().QueryRowContext(ctx, selectProjectDefaultsSQL(s.projectDefaultsTable)+` WHERE project_id = ?`, strings.TrimSpace(projectID))
	defaults, err := scanProjectDefaults(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectDefaults{}, false, nil
	}
	if err != nil {
		return ProjectDefaults{}, false, err
	}
	return cloneProjectDefaults(defaults), true, nil
}

func (s *SQLiteStore) UpsertProjectDefaults(ctx context.Context, defaults ProjectDefaults) (ProjectDefaults, error) {
	defaults = normalizeProjectDefaults(defaults, time.Now().UTC())
	if err := validateProjectDefaults(defaults); err != nil {
		return ProjectDefaults{}, err
	}
	_, err := s.client.DB().ExecContext(ctx, `
INSERT INTO `+s.projectDefaultsTable+` (
	project_id, default_provider, default_model, default_agent_profile, default_tools_enabled,
	default_workspace_mode, default_system_prompt, default_compact_tool_output, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(project_id) DO UPDATE SET
	default_provider = excluded.default_provider,
	default_model = excluded.default_model,
	default_agent_profile = excluded.default_agent_profile,
	default_tools_enabled = excluded.default_tools_enabled,
	default_workspace_mode = excluded.default_workspace_mode,
	default_system_prompt = excluded.default_system_prompt,
	default_compact_tool_output = excluded.default_compact_tool_output,
	updated_at = excluded.updated_at`,
		defaults.ProjectID,
		defaults.DefaultProvider,
		defaults.DefaultModel,
		defaults.DefaultAgentProfile,
		formatRuntimeBoolPtr(defaults.DefaultToolsEnabled),
		defaults.DefaultWorkspaceMode,
		defaults.DefaultSystemPrompt,
		formatRuntimeBoolPtr(defaults.DefaultCompactToolOutput),
		formatRuntimeTime(defaults.UpdatedAt),
	)
	if err != nil {
		return ProjectDefaults{}, err
	}
	stored, ok, err := s.GetProjectDefaults(ctx, defaults.ProjectID)
	if err != nil {
		return ProjectDefaults{}, err
	}
	if !ok {
		return ProjectDefaults{}, ErrNotFound
	}
	return stored, nil
}

func (s *SQLiteStore) DeleteProjectDefaults(ctx context.Context, projectID string) error {
	res, err := s.client.DB().ExecContext(ctx, `DELETE FROM `+s.projectDefaultsTable+` WHERE project_id = ?`, strings.TrimSpace(projectID))
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

func (s *SQLiteStore) GetRoleDefaults(ctx context.Context, projectID, roleID string) (RoleDefaults, bool, error) {
	row := s.client.DB().QueryRowContext(ctx, selectRoleDefaultsSQL(s.roleDefaultsTable)+` WHERE project_id = ? AND role_id = ?`, strings.TrimSpace(projectID), strings.TrimSpace(roleID))
	defaults, err := scanRoleDefaults(row)
	if errors.Is(err, sql.ErrNoRows) {
		return RoleDefaults{}, false, nil
	}
	if err != nil {
		return RoleDefaults{}, false, err
	}
	return cloneRoleDefaults(defaults), true, nil
}

func (s *SQLiteStore) UpsertRoleDefaults(ctx context.Context, defaults RoleDefaults) (RoleDefaults, error) {
	defaults = normalizeRoleDefaults(defaults, time.Now().UTC())
	if err := validateRoleDefaults(defaults); err != nil {
		return RoleDefaults{}, err
	}
	_, err := s.client.DB().ExecContext(ctx, `
INSERT INTO `+s.roleDefaultsTable+` (
	project_id, role_id, default_provider, default_model, default_agent_profile, updated_at
) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(project_id, role_id) DO UPDATE SET
	default_provider = excluded.default_provider,
	default_model = excluded.default_model,
	default_agent_profile = excluded.default_agent_profile,
	updated_at = excluded.updated_at`,
		defaults.ProjectID,
		defaults.RoleID,
		defaults.DefaultProvider,
		defaults.DefaultModel,
		defaults.DefaultAgentProfile,
		formatRuntimeTime(defaults.UpdatedAt),
	)
	if err != nil {
		return RoleDefaults{}, err
	}
	stored, ok, err := s.GetRoleDefaults(ctx, defaults.ProjectID, defaults.RoleID)
	if err != nil {
		return RoleDefaults{}, err
	}
	if !ok {
		return RoleDefaults{}, ErrNotFound
	}
	return stored, nil
}

func (s *SQLiteStore) DeleteRoleDefaults(ctx context.Context, projectID, roleID string) error {
	res, err := s.client.DB().ExecContext(ctx, `DELETE FROM `+s.roleDefaultsTable+` WHERE project_id = ? AND role_id = ?`, strings.TrimSpace(projectID), strings.TrimSpace(roleID))
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
	projectID = strings.TrimSpace(projectID)
	deleted := 0
	res, err := s.client.DB().ExecContext(ctx, `DELETE FROM `+s.table+` WHERE project_id = ?`, projectID)
	if err != nil {
		return 0, err
	}
	count, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	deleted += int(count)
	res, err = s.client.DB().ExecContext(ctx, `DELETE FROM `+s.projectDefaultsTable+` WHERE project_id = ?`, projectID)
	if err != nil {
		return 0, err
	}
	count, err = res.RowsAffected()
	if err != nil {
		return 0, err
	}
	deleted += int(count)
	res, err = s.client.DB().ExecContext(ctx, `DELETE FROM `+s.roleDefaultsTable+` WHERE project_id = ?`, projectID)
	if err != nil {
		return 0, err
	}
	count, err = res.RowsAffected()
	if err != nil {
		return 0, err
	}
	deleted += int(count)
	return deleted, nil
}

func (s *SQLiteStore) Clear(ctx context.Context) (int, error) {
	res, err := s.client.DB().ExecContext(ctx, `DELETE FROM `+s.table)
	if err != nil {
		return 0, err
	}
	deleted := 0
	count, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	deleted += int(count)
	res, err = s.client.DB().ExecContext(ctx, `DELETE FROM `+s.projectDefaultsTable)
	if err != nil {
		return 0, err
	}
	count, err = res.RowsAffected()
	if err != nil {
		return 0, err
	}
	deleted += int(count)
	res, err = s.client.DB().ExecContext(ctx, `DELETE FROM `+s.roleDefaultsTable)
	if err != nil {
		return 0, err
	}
	count, err = res.RowsAffected()
	if err != nil {
		return 0, err
	}
	deleted += int(count)
	return deleted, nil
}

func selectRuntimeSQL(table string) string {
	return `SELECT project_id, assignment_id, execution_ref, context_packet, started_at, completed_at, updated_at FROM ` + table
}

func selectProjectDefaultsSQL(table string) string {
	return `SELECT project_id, default_provider, default_model, default_agent_profile, default_tools_enabled, default_workspace_mode, default_system_prompt, default_compact_tool_output, updated_at FROM ` + table
}

func selectRoleDefaultsSQL(table string) string {
	return `SELECT project_id, role_id, default_provider, default_model, default_agent_profile, updated_at FROM ` + table
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

func scanProjectDefaults(row scanner) (ProjectDefaults, error) {
	var defaults ProjectDefaults
	var toolsEnabled, compactToolOutput, updatedAt string
	if err := row.Scan(
		&defaults.ProjectID,
		&defaults.DefaultProvider,
		&defaults.DefaultModel,
		&defaults.DefaultAgentProfile,
		&toolsEnabled,
		&defaults.DefaultWorkspaceMode,
		&defaults.DefaultSystemPrompt,
		&compactToolOutput,
		&updatedAt,
	); err != nil {
		return ProjectDefaults{}, err
	}
	var err error
	if defaults.DefaultToolsEnabled, err = parseRuntimeBoolPtr(toolsEnabled); err != nil {
		return ProjectDefaults{}, err
	}
	if defaults.DefaultCompactToolOutput, err = parseRuntimeBoolPtr(compactToolOutput); err != nil {
		return ProjectDefaults{}, err
	}
	if defaults.UpdatedAt, err = parseRuntimeTime(updatedAt); err != nil {
		return ProjectDefaults{}, err
	}
	return cloneProjectDefaults(defaults), nil
}

func scanRoleDefaults(row scanner) (RoleDefaults, error) {
	var defaults RoleDefaults
	var updatedAt string
	if err := row.Scan(
		&defaults.ProjectID,
		&defaults.RoleID,
		&defaults.DefaultProvider,
		&defaults.DefaultModel,
		&defaults.DefaultAgentProfile,
		&updatedAt,
	); err != nil {
		return RoleDefaults{}, err
	}
	var err error
	if defaults.UpdatedAt, err = parseRuntimeTime(updatedAt); err != nil {
		return RoleDefaults{}, err
	}
	return cloneRoleDefaults(defaults), nil
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

func formatRuntimeBoolPtr(value *bool) string {
	if value == nil {
		return ""
	}
	if *value {
		return "true"
	}
	return "false"
}

func parseRuntimeBoolPtr(value string) (*bool, error) {
	switch strings.TrimSpace(value) {
	case "":
		return nil, nil
	case "true":
		result := true
		return &result, nil
	case "false":
		result := false
		return &result, nil
	default:
		return nil, fmt.Errorf("%w: invalid bool value %q", ErrInvalid, value)
	}
}
