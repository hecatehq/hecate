package agentprofiles

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
		table:   client.QualifiedTable("agent_profiles"),
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
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    instructions TEXT NOT NULL DEFAULT '',
    surface TEXT NOT NULL DEFAULT 'any',
    provider_hint TEXT NOT NULL DEFAULT '',
    model_hint TEXT NOT NULL DEFAULT '',
    execution_profile TEXT NOT NULL DEFAULT '',
    tools_enabled INTEGER NOT NULL DEFAULT 0,
    writes_allowed INTEGER NOT NULL DEFAULT 0,
    network_allowed INTEGER NOT NULL DEFAULT 0,
    approval_policy TEXT NOT NULL DEFAULT 'inherit',
    project_memory_policy TEXT NOT NULL DEFAULT 'inherit',
    context_source_policy TEXT NOT NULL DEFAULT 'inherit',
    skill_ids TEXT NOT NULL DEFAULT '[]',
    external_agent_kind TEXT NOT NULL DEFAULT '',
    external_agent_options TEXT NOT NULL DEFAULT '{}',
    created_at `+timestampColumn+`,
    updated_at `+timestampColumn+`
)`)
	return err
}

func (s *SQLiteStore) Create(ctx context.Context, profile Profile) (Profile, error) {
	profile = normalizeProfile(profile, time.Now().UTC())
	if IsBuiltInProfileID(profile.ID) || profile.BuiltIn {
		return Profile{}, ErrBuiltIn
	}
	if err := validateProfile(profile); err != nil {
		return Profile{}, err
	}
	if err := s.upsert(ctx, profile); err != nil {
		return Profile{}, err
	}
	return cloneProfile(profile), nil
}

func (s *SQLiteStore) Get(ctx context.Context, id string) (Profile, bool, error) {
	id = strings.TrimSpace(id)
	if profile, ok := BuiltInProfile(id); ok {
		return profile, true, nil
	}
	row := s.client.DB().QueryRowContext(ctx, selectAgentProfileSQL(s.table)+" WHERE id = ?", id)
	profile, err := scanAgentProfile(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Profile{}, false, nil
	}
	if err != nil {
		return Profile{}, false, err
	}
	return cloneProfile(profile), true, nil
}

func (s *SQLiteStore) List(ctx context.Context) ([]Profile, error) {
	rows, err := s.client.DB().QueryContext(ctx, selectAgentProfileSQL(s.table)+" ORDER BY updated_at DESC, name ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := BuiltInProfiles()
	for rows.Next() {
		item, err := scanAgentProfile(rows)
		if err != nil {
			return nil, err
		}
		if IsBuiltInProfileID(item.ID) {
			continue
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortProfiles(items)
	return items, nil
}

func (s *SQLiteStore) Update(ctx context.Context, id string, update func(*Profile)) (Profile, error) {
	if IsBuiltInProfileID(id) {
		return Profile{}, ErrBuiltIn
	}
	profile, ok, err := s.Get(ctx, id)
	if err != nil {
		return Profile{}, err
	}
	if !ok {
		return Profile{}, ErrNotFound
	}
	createdAt := profile.CreatedAt
	originalID := profile.ID
	if update != nil {
		update(&profile)
	}
	profile.ID = originalID
	profile.CreatedAt = createdAt
	profile.UpdatedAt = time.Now().UTC()
	profile = normalizeProfile(profile, profile.UpdatedAt)
	if err := validateProfile(profile); err != nil {
		return Profile{}, err
	}
	if err := s.upsert(ctx, profile); err != nil {
		return Profile{}, err
	}
	return cloneProfile(profile), nil
}

func (s *SQLiteStore) Delete(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if IsBuiltInProfileID(id) {
		return ErrBuiltIn
	}
	res, err := s.client.DB().ExecContext(ctx, `DELETE FROM `+s.table+` WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLiteStore) upsert(ctx context.Context, profile Profile) error {
	skills, err := json.Marshal(profile.SkillIDs)
	if err != nil {
		return err
	}
	options, err := json.Marshal(profile.ExternalAgentOptions)
	if err != nil {
		return err
	}
	_, err = s.client.DB().ExecContext(ctx, `
INSERT INTO `+s.table+` (
    id, name, description, instructions, surface, provider_hint, model_hint,
    execution_profile, tools_enabled, writes_allowed, network_allowed,
    approval_policy, project_memory_policy, context_source_policy, skill_ids,
    external_agent_kind, external_agent_options, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    name = excluded.name,
    description = excluded.description,
    instructions = excluded.instructions,
    surface = excluded.surface,
    provider_hint = excluded.provider_hint,
    model_hint = excluded.model_hint,
    execution_profile = excluded.execution_profile,
    tools_enabled = excluded.tools_enabled,
    writes_allowed = excluded.writes_allowed,
    network_allowed = excluded.network_allowed,
    approval_policy = excluded.approval_policy,
    project_memory_policy = excluded.project_memory_policy,
    context_source_policy = excluded.context_source_policy,
    skill_ids = excluded.skill_ids,
    external_agent_kind = excluded.external_agent_kind,
    external_agent_options = excluded.external_agent_options,
    updated_at = excluded.updated_at`,
		profile.ID,
		profile.Name,
		profile.Description,
		profile.Instructions,
		profile.Surface,
		profile.ProviderHint,
		profile.ModelHint,
		profile.ExecutionProfile,
		boolInt(profile.ToolsEnabled),
		boolInt(profile.WritesAllowed),
		boolInt(profile.NetworkAllowed),
		profile.ApprovalPolicy,
		profile.ProjectMemoryPolicy,
		profile.ContextSourcePolicy,
		string(skills),
		profile.ExternalAgentKind,
		string(options),
		formatTime(profile.CreatedAt),
		formatTime(profile.UpdatedAt),
	)
	return err
}

func selectAgentProfileSQL(table string) string {
	return `SELECT id, name, description, instructions, surface, provider_hint, model_hint,
execution_profile, tools_enabled, writes_allowed, network_allowed, approval_policy,
project_memory_policy, context_source_policy, skill_ids, external_agent_kind,
external_agent_options, created_at, updated_at FROM ` + table
}

type scanner interface {
	Scan(dest ...any) error
}

func scanAgentProfile(row scanner) (Profile, error) {
	var profile Profile
	var tools, writes, network int
	var skillsRaw, optionsRaw string
	var createdAt, updatedAt string
	if err := row.Scan(
		&profile.ID,
		&profile.Name,
		&profile.Description,
		&profile.Instructions,
		&profile.Surface,
		&profile.ProviderHint,
		&profile.ModelHint,
		&profile.ExecutionProfile,
		&tools,
		&writes,
		&network,
		&profile.ApprovalPolicy,
		&profile.ProjectMemoryPolicy,
		&profile.ContextSourcePolicy,
		&skillsRaw,
		&profile.ExternalAgentKind,
		&optionsRaw,
		&createdAt,
		&updatedAt,
	); err != nil {
		return Profile{}, err
	}
	profile.ToolsEnabled = tools != 0
	profile.WritesAllowed = writes != 0
	profile.NetworkAllowed = network != 0
	_ = json.Unmarshal([]byte(skillsRaw), &profile.SkillIDs)
	_ = json.Unmarshal([]byte(optionsRaw), &profile.ExternalAgentOptions)
	profile.CreatedAt = parseTime(createdAt)
	profile.UpdatedAt = parseTime(updatedAt)
	return normalizeProfile(profile, profile.UpdatedAt), nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(raw string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, raw)
	return t
}
