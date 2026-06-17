package pluginregistry

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
	client       storage.SQLClient
	backend      string
	plugins      string
	capabilities string
	auth         string
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
		client:       client,
		backend:      client.Backend(),
		plugins:      client.QualifiedTable("plugins"),
		capabilities: client.QualifiedTable("plugin_capabilities"),
		auth:         client.QualifiedTable("plugin_auth"),
	}
	if err := store.migrate(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *SQLiteStore) Backend() string { return s.backend }

func (s *SQLiteStore) migrate(ctx context.Context) error {
	timestampColumn := storage.TimestampColumnDefaultZero(s.client)
	for _, stmt := range []string{
		`
CREATE TABLE IF NOT EXISTS ` + s.plugins + ` (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL DEFAULT '',
	description TEXT NOT NULL DEFAULT '',
	version TEXT NOT NULL DEFAULT '',
	source_kind TEXT NOT NULL DEFAULT 'local_path',
	source_ref TEXT NOT NULL DEFAULT '',
	manifest_schema_version TEXT NOT NULL DEFAULT 'hecate.plugin.v0',
	manifest_digest TEXT NOT NULL DEFAULT '',
	manifest_json TEXT NOT NULL DEFAULT '{}',
	requested_permissions_json TEXT NOT NULL DEFAULT '[]',
	registry_state TEXT NOT NULL DEFAULT 'valid',
	enabled INTEGER NOT NULL DEFAULT 0,
	warnings TEXT NOT NULL DEFAULT '[]',
	installed_at ` + timestampColumn + `,
	updated_at ` + timestampColumn + `
)`,
		`
CREATE TABLE IF NOT EXISTS ` + s.capabilities + ` (
	plugin_id TEXT NOT NULL,
	capability_id TEXT NOT NULL,
	capability_kind TEXT NOT NULL,
	display_name TEXT NOT NULL DEFAULT '',
	requested_permissions_json TEXT NOT NULL DEFAULT '[]',
	enabled INTEGER NOT NULL DEFAULT 1,
	config_json TEXT NOT NULL DEFAULT '{}',
	warnings TEXT NOT NULL DEFAULT '[]',
	PRIMARY KEY(plugin_id, capability_id)
)`,
		`
CREATE TABLE IF NOT EXISTS ` + s.auth + ` (
	plugin_id TEXT NOT NULL,
	capability_id TEXT NOT NULL DEFAULT '',
	requested_name TEXT NOT NULL,
	auth_kind TEXT NOT NULL DEFAULT 'unknown',
	status TEXT NOT NULL DEFAULT 'unknown',
	secret_ref TEXT NOT NULL DEFAULT '',
	warnings TEXT NOT NULL DEFAULT '[]',
	PRIMARY KEY(plugin_id, capability_id, requested_name)
)`,
	} {
		if _, err := s.client.DB().ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) List(ctx context.Context) ([]Plugin, error) {
	rows, err := s.client.DB().QueryContext(ctx, selectPluginSQL(s.plugins)+`
ORDER BY name ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Plugin
	for rows.Next() {
		item, err := scanPlugin(rows)
		if err != nil {
			return nil, err
		}
		if err := s.loadChildren(ctx, &item); err != nil {
			return nil, err
		}
		items = append(items, NormalizePlugin(item, item.UpdatedAt))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *SQLiteStore) Get(ctx context.Context, id string) (Plugin, bool, error) {
	row := s.client.DB().QueryRowContext(ctx, selectPluginSQL(s.plugins)+"WHERE id = ?", normalizeID(id))
	item, err := scanPlugin(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Plugin{}, false, nil
	}
	if err != nil {
		return Plugin{}, false, err
	}
	if err := s.loadChildren(ctx, &item); err != nil {
		return Plugin{}, false, err
	}
	return NormalizePlugin(item, item.UpdatedAt), true, nil
}

func (s *SQLiteStore) Upsert(ctx context.Context, plugin Plugin) (Plugin, error) {
	now := time.Now().UTC()
	if existing, ok, err := s.Get(ctx, plugin.ID); err != nil {
		return Plugin{}, err
	} else if ok && !existing.InstalledAt.IsZero() {
		plugin.InstalledAt = existing.InstalledAt
	}
	plugin = NormalizePlugin(plugin, now)
	if err := ValidatePlugin(plugin); err != nil {
		return Plugin{}, err
	}
	tx, err := s.client.DB().BeginTx(ctx, nil)
	if err != nil {
		return Plugin{}, err
	}
	if err := s.upsertWith(ctx, tx, plugin); err != nil {
		_ = tx.Rollback()
		return Plugin{}, err
	}
	if err := tx.Commit(); err != nil {
		return Plugin{}, err
	}
	return clonePlugin(plugin), nil
}

func (s *SQLiteStore) Update(ctx context.Context, id string, update func(*Plugin)) (Plugin, error) {
	item, ok, err := s.Get(ctx, id)
	if err != nil {
		return Plugin{}, err
	}
	if !ok {
		return Plugin{}, ErrNotFound
	}
	originalID := item.ID
	installedAt := item.InstalledAt
	if update != nil {
		update(&item)
	}
	item.ID = originalID
	item.InstalledAt = installedAt
	item.UpdatedAt = time.Now().UTC()
	item = NormalizePlugin(item, item.UpdatedAt)
	if err := ValidatePlugin(item); err != nil {
		return Plugin{}, err
	}
	tx, err := s.client.DB().BeginTx(ctx, nil)
	if err != nil {
		return Plugin{}, err
	}
	if err := s.upsertWith(ctx, tx, item); err != nil {
		_ = tx.Rollback()
		return Plugin{}, err
	}
	if err := tx.Commit(); err != nil {
		return Plugin{}, err
	}
	return clonePlugin(item), nil
}

func (s *SQLiteStore) Clear(ctx context.Context) (int, error) {
	tx, err := s.client.DB().BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	deleted := 0
	for _, table := range []string{s.auth, s.capabilities, s.plugins} {
		res, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s`, table))
		if err != nil {
			_ = tx.Rollback()
			return deleted, err
		}
		if affected, err := res.RowsAffected(); err == nil && table == s.plugins {
			deleted = int(affected)
		}
	}
	if err := tx.Commit(); err != nil {
		return deleted, err
	}
	return deleted, nil
}

func (s *SQLiteStore) loadChildren(ctx context.Context, plugin *Plugin) error {
	capabilities, err := s.listCapabilities(ctx, plugin.ID)
	if err != nil {
		return err
	}
	auth, err := s.listAuth(ctx, plugin.ID)
	if err != nil {
		return err
	}
	plugin.Capabilities = capabilities
	plugin.Auth = auth
	return nil
}

func (s *SQLiteStore) listCapabilities(ctx context.Context, pluginID string) ([]Capability, error) {
	rows, err := s.client.DB().QueryContext(ctx, selectCapabilitySQL(s.capabilities)+`
WHERE plugin_id = ?
ORDER BY capability_kind ASC, capability_id ASC`, pluginID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Capability
	for rows.Next() {
		item, err := scanCapability(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *SQLiteStore) listAuth(ctx context.Context, pluginID string) ([]AuthBinding, error) {
	rows, err := s.client.DB().QueryContext(ctx, selectAuthSQL(s.auth)+`
WHERE plugin_id = ?
ORDER BY capability_id ASC, requested_name ASC`, pluginID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []AuthBinding
	for rows.Next() {
		item, err := scanAuthBinding(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *SQLiteStore) upsertWith(ctx context.Context, tx storage.Tx, plugin Plugin) error {
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
INSERT INTO %s (
	id, name, description, version, source_kind, source_ref, manifest_schema_version,
	manifest_digest, manifest_json, requested_permissions_json, registry_state,
	enabled, warnings, installed_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	name = excluded.name,
	description = excluded.description,
	version = excluded.version,
	source_kind = excluded.source_kind,
	source_ref = excluded.source_ref,
	manifest_schema_version = excluded.manifest_schema_version,
	manifest_digest = excluded.manifest_digest,
	manifest_json = excluded.manifest_json,
	requested_permissions_json = excluded.requested_permissions_json,
	registry_state = excluded.registry_state,
	enabled = excluded.enabled,
	warnings = excluded.warnings,
	updated_at = excluded.updated_at`, s.plugins),
		plugin.ID,
		plugin.Name,
		plugin.Description,
		plugin.Version,
		plugin.SourceKind,
		plugin.SourceRef,
		plugin.ManifestSchemaVersion,
		plugin.ManifestDigest,
		string(plugin.ManifestJSON),
		encodePermissions(plugin.RequestedPermissions),
		plugin.RegistryState,
		boolToDB(plugin.Enabled),
		encodeStringSlice(plugin.Warnings),
		formatTime(plugin.InstalledAt),
		formatTime(plugin.UpdatedAt),
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE plugin_id = ?`, s.capabilities), plugin.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE plugin_id = ?`, s.auth), plugin.ID); err != nil {
		return err
	}
	for _, capability := range plugin.Capabilities {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
INSERT INTO %s (
	plugin_id, capability_id, capability_kind, display_name,
	requested_permissions_json, enabled, config_json, warnings
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, s.capabilities),
			plugin.ID,
			capability.ID,
			capability.Kind,
			capability.DisplayName,
			encodePermissions(capability.RequestedPermissions),
			boolToDB(capability.Enabled),
			string(capability.ConfigJSON),
			encodeStringSlice(capability.Warnings),
		); err != nil {
			return err
		}
	}
	for _, binding := range plugin.Auth {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
INSERT INTO %s (
	plugin_id, capability_id, requested_name, auth_kind, status, secret_ref, warnings
) VALUES (?, ?, ?, ?, ?, ?, ?)`, s.auth),
			plugin.ID,
			binding.CapabilityID,
			binding.RequestedName,
			binding.Kind,
			binding.Status,
			binding.SecretRef,
			encodeStringSlice(binding.Warnings),
		); err != nil {
			return err
		}
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func selectPluginSQL(table string) string {
	return fmt.Sprintf(`
SELECT id, name, description, version, source_kind, source_ref,
	manifest_schema_version, manifest_digest, manifest_json,
	requested_permissions_json, registry_state, enabled, warnings,
	installed_at, updated_at
FROM %s
`, table)
}

func selectCapabilitySQL(table string) string {
	return fmt.Sprintf(`
SELECT plugin_id, capability_id, capability_kind, display_name,
	requested_permissions_json, enabled, config_json, warnings
FROM %s
`, table)
}

func selectAuthSQL(table string) string {
	return fmt.Sprintf(`
SELECT plugin_id, capability_id, requested_name, auth_kind, status, secret_ref, warnings
FROM %s
`, table)
}

func scanPlugin(row rowScanner) (Plugin, error) {
	var plugin Plugin
	var enabled int
	var manifestJSON, permissionsJSON, warningsJSON string
	var installedAt, updatedAt string
	if err := row.Scan(
		&plugin.ID,
		&plugin.Name,
		&plugin.Description,
		&plugin.Version,
		&plugin.SourceKind,
		&plugin.SourceRef,
		&plugin.ManifestSchemaVersion,
		&plugin.ManifestDigest,
		&manifestJSON,
		&permissionsJSON,
		&plugin.RegistryState,
		&enabled,
		&warningsJSON,
		&installedAt,
		&updatedAt,
	); err != nil {
		return Plugin{}, err
	}
	var err error
	plugin.ManifestJSON = json.RawMessage(strings.TrimSpace(manifestJSON))
	plugin.RequestedPermissions = decodePermissions(permissionsJSON)
	plugin.Enabled = enabled != 0
	plugin.Warnings = decodeStringSlice(warningsJSON)
	if plugin.InstalledAt, err = parseTime(installedAt); err != nil {
		return Plugin{}, err
	}
	if plugin.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return Plugin{}, err
	}
	return plugin, nil
}

func scanCapability(row rowScanner) (Capability, error) {
	var capability Capability
	var permissionsJSON, configJSON, warningsJSON string
	var enabled int
	if err := row.Scan(
		&capability.PluginID,
		&capability.ID,
		&capability.Kind,
		&capability.DisplayName,
		&permissionsJSON,
		&enabled,
		&configJSON,
		&warningsJSON,
	); err != nil {
		return Capability{}, err
	}
	capability.RequestedPermissions = decodePermissions(permissionsJSON)
	capability.Enabled = enabled != 0
	capability.ConfigJSON = json.RawMessage(strings.TrimSpace(configJSON))
	capability.Warnings = decodeStringSlice(warningsJSON)
	return NormalizeCapability(capability.PluginID, capability), nil
}

func scanAuthBinding(row rowScanner) (AuthBinding, error) {
	var binding AuthBinding
	var warningsJSON string
	if err := row.Scan(
		&binding.PluginID,
		&binding.CapabilityID,
		&binding.RequestedName,
		&binding.Kind,
		&binding.Status,
		&binding.SecretRef,
		&warningsJSON,
	); err != nil {
		return AuthBinding{}, err
	}
	binding.Warnings = decodeStringSlice(warningsJSON)
	return NormalizeAuthBinding(binding.PluginID, binding), nil
}

func boolToDB(value bool) int {
	if value {
		return 1
	}
	return 0
}

func encodePermissions(items []Permission) string {
	raw, err := json.Marshal(NormalizePermissions(items))
	if err != nil {
		return "[]"
	}
	return string(raw)
}

func decodePermissions(raw string) []Permission {
	var items []Permission
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &items); err != nil {
		return nil
	}
	return NormalizePermissions(items)
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
