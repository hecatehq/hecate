package browserapp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/hecatehq/hecate/internal/storage"
)

// SQLStore shares one small host-keyed selection table across SQLite/Postgres.
// Browser paths do not enter the portable/provider control-plane snapshot.
type SQLStore struct {
	client storage.SQLClient
	table  string
}

func NewSQLiteStore(ctx context.Context, client *storage.SQLiteClient) (*SQLStore, error) {
	return newSQLStore(ctx, client)
}

func NewPostgresStore(ctx context.Context, client *storage.PostgresClient) (*SQLStore, error) {
	return newSQLStore(ctx, client)
}

func newSQLStore(ctx context.Context, client storage.SQLClient) (*SQLStore, error) {
	if client == nil || client.DB() == nil {
		return nil, errors.New("SQL client is required")
	}
	s := &SQLStore{client: client, table: client.QualifiedTable("browser_runtime_settings")}
	_, err := client.DB().ExecContext(ctx, fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		runtime_host_id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		executable_path TEXT NOT NULL,
		canonical_path TEXT NOT NULL
	)`, s.table))
	if err != nil {
		return nil, fmt.Errorf("initialize browser settings: %w", err)
	}
	return s, nil
}

func (s *SQLStore) Backend() string { return s.client.Backend() }

func (s *SQLStore) Get(ctx context.Context, hostID string) (Selection, error) {
	var selection Selection
	err := s.client.DB().QueryRowContext(ctx, fmt.Sprintf(`SELECT runtime_host_id, name, executable_path, canonical_path
		FROM %s WHERE runtime_host_id = ?`, s.table), hostID).Scan(
		&selection.RuntimeHostID, &selection.Name, &selection.Path, &selection.CanonicalPath,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Selection{}, ErrNotFound
	}
	if err != nil {
		return Selection{}, fmt.Errorf("read browser settings: %w", err)
	}
	return selection, nil
}

func (s *SQLStore) Put(ctx context.Context, selection Selection) error {
	_, err := s.client.DB().ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s
		(runtime_host_id, name, executable_path, canonical_path) VALUES (?, ?, ?, ?)
		ON CONFLICT (runtime_host_id) DO UPDATE SET name = excluded.name,
		executable_path = excluded.executable_path, canonical_path = excluded.canonical_path`, s.table),
		selection.RuntimeHostID, selection.Name, selection.Path, selection.CanonicalPath,
	)
	if err != nil {
		return fmt.Errorf("save browser settings: %w", err)
	}
	return nil
}

func (s *SQLStore) Delete(ctx context.Context, hostID string) error {
	_, err := s.client.DB().ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE runtime_host_id = ?`, s.table), hostID)
	if err != nil {
		return fmt.Errorf("delete browser settings: %w", err)
	}
	return nil
}
