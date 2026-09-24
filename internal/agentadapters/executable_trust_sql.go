package agentadapters

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hecatehq/hecate/internal/storage"
)

// SQLExecutableTrustStore is the shared SQLite/Postgres implementation of
// ExecutableTrustStore. storage.SQLClient owns dialect-specific placeholder,
// binary-column, timestamp, and table-name behavior.
type SQLExecutableTrustStore struct {
	client  storage.SQLClient
	backend string
	table   string
}

func NewSQLiteExecutableTrustStore(ctx context.Context, client *storage.SQLiteClient) (*SQLExecutableTrustStore, error) {
	return newSQLExecutableTrustStore(ctx, client)
}

func NewPostgresExecutableTrustStore(ctx context.Context, client *storage.PostgresClient) (*SQLExecutableTrustStore, error) {
	return newSQLExecutableTrustStore(ctx, client)
}

func newSQLExecutableTrustStore(ctx context.Context, client storage.SQLClient) (*SQLExecutableTrustStore, error) {
	if client == nil || client.DB() == nil {
		return nil, fmt.Errorf("sql client is required")
	}
	store := &SQLExecutableTrustStore{
		client:  client,
		backend: client.Backend(),
		table:   client.QualifiedTable("external_agent_executable_trust"),
	}
	if err := store.migrate(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *SQLExecutableTrustStore) Backend() string { return s.backend }

func (s *SQLExecutableTrustStore) Get(ctx context.Context, runtimeHostID, adapterID string) (ExecutableTrustRecord, error) {
	var (
		record       ExecutableTrustRecord
		identityJSON []byte
	)
	err := s.client.DB().QueryRowContext(
		ctx,
		fmt.Sprintf(
			`SELECT runtime_host_id, adapter_id, identity, approved_by, approved_at
			 FROM %s
			 WHERE runtime_host_id = ? AND adapter_id = ?`,
			s.table,
		),
		runtimeHostID,
		adapterID,
	).Scan(
		&record.RuntimeHostID,
		&record.AdapterID,
		&identityJSON,
		&record.ApprovedBy,
		&record.ApprovedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ExecutableTrustRecord{}, ErrExecutableTrustNotFound
	}
	if err != nil {
		return ExecutableTrustRecord{}, fmt.Errorf("get executable trust: %w", err)
	}
	if err := json.Unmarshal(identityJSON, &record.Identity); err != nil {
		return ExecutableTrustRecord{}, fmt.Errorf("decode executable trust identity: %w", err)
	}
	record.ApprovedAt = record.ApprovedAt.UTC()
	return cloneExecutableTrustRecord(record), nil
}

func (s *SQLExecutableTrustStore) Approve(ctx context.Context, record ExecutableTrustRecord) (ExecutableTrustRecord, error) {
	record = cloneExecutableTrustRecord(record)
	if record.ApprovedAt.IsZero() {
		record.ApprovedAt = time.Now().UTC()
	} else {
		record.ApprovedAt = record.ApprovedAt.UTC()
	}
	identityJSON, err := json.Marshal(record.Identity)
	if err != nil {
		return ExecutableTrustRecord{}, fmt.Errorf("encode executable trust identity: %w", err)
	}
	_, err = s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(
			`INSERT INTO %s (
				runtime_host_id, adapter_id, identity, approved_by, approved_at
			 ) VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT (runtime_host_id, adapter_id) DO UPDATE SET
				identity = excluded.identity,
				approved_by = excluded.approved_by,
				approved_at = excluded.approved_at`,
			s.table,
		),
		record.RuntimeHostID,
		record.AdapterID,
		identityJSON,
		record.ApprovedBy,
		record.ApprovedAt,
	)
	if err != nil {
		return ExecutableTrustRecord{}, fmt.Errorf("approve executable trust: %w", err)
	}
	return cloneExecutableTrustRecord(record), nil
}

func (s *SQLExecutableTrustStore) Revoke(ctx context.Context, runtimeHostID, adapterID string) error {
	_, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(
			`DELETE FROM %s WHERE runtime_host_id = ? AND adapter_id = ?`,
			s.table,
		),
		runtimeHostID,
		adapterID,
	)
	if err != nil {
		return fmt.Errorf("revoke executable trust: %w", err)
	}
	return nil
}

func (s *SQLExecutableTrustStore) migrate(ctx context.Context) error {
	_, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS %s (
				runtime_host_id TEXT NOT NULL,
				adapter_id TEXT NOT NULL,
				identity %s NOT NULL,
				approved_by TEXT NOT NULL DEFAULT '',
				approved_at %s NOT NULL,
				PRIMARY KEY (runtime_host_id, adapter_id)
			)`,
			s.table,
			storage.BinaryColumn(s.client),
			storage.TimestampColumn(s.client),
		),
	)
	if err != nil {
		return fmt.Errorf("migrate external agent executable trust: %w", err)
	}
	return nil
}

var _ ExecutableTrustStore = (*SQLExecutableTrustStore)(nil)
