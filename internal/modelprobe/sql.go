package modelprobe

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/storage"
	"github.com/hecatehq/hecate/pkg/types"
)

type SQLStore struct {
	client  storage.SQLClient
	backend string
	table   string
}

func NewSQLiteStore(ctx context.Context, client *storage.SQLiteClient) (*SQLStore, error) {
	return newSQLStore(ctx, client)
}

func NewPostgresStore(ctx context.Context, client *storage.PostgresClient) (*SQLStore, error) {
	return newSQLStore(ctx, client)
}

func newSQLStore(ctx context.Context, client storage.SQLClient) (*SQLStore, error) {
	if client == nil || client.DB() == nil {
		return nil, errors.New("sql client is required")
	}
	store := &SQLStore{
		client:  client,
		backend: client.Backend(),
		table:   client.QualifiedTable("model_tool_probes"),
	}
	if err := store.migrate(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *SQLStore) Backend() string { return s.backend }

func (s *SQLStore) migrate(ctx context.Context) error {
	timestamp := storage.TimestampColumnDefaultZero(s.client)
	_, err := s.client.DB().ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS `+s.table+` (
    provider TEXT NOT NULL,
    model TEXT NOT NULL,
    instance_id TEXT NOT NULL,
    instance_kind TEXT NOT NULL,
    probe_version INTEGER NOT NULL,
    status TEXT NOT NULL,
    checked_at `+timestamp+`,
    expires_at `+timestamp+`,
    lease_until `+timestamp+`,
    lease_id TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (provider, model, instance_id, instance_kind, probe_version)
)`)
	if err != nil {
		return fmt.Errorf("migrate %s model tool probes: %w", s.backend, err)
	}
	return nil
}

func (s *SQLStore) Get(ctx context.Context, key Key) (Record, bool, error) {
	key, err := NormalizeKey(key)
	if err != nil {
		return Record{}, false, err
	}
	record, err := s.get(ctx, s.client.DB(), key, false)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, fmt.Errorf("read %s model tool probe: %w", s.backend, err)
	}
	return record, true, nil
}

const getManyBatchSize = 100

// GetMany reads bounded groups of exact generation keys. The predicate stays
// fully keyed rather than looking up by provider/model alone, so a same-name
// provider replacement can never project an old probe result.
func (s *SQLStore) GetMany(ctx context.Context, keys []Key) (map[Key]Record, error) {
	keys, err := normalizeKeys(keys)
	if err != nil {
		return nil, err
	}
	records := make(map[Key]Record, len(keys))
	for start := 0; start < len(keys); start += getManyBatchSize {
		end := start + getManyBatchSize
		if end > len(keys) {
			end = len(keys)
		}
		batch := keys[start:end]
		clauses := make([]string, 0, len(batch))
		args := make([]any, 0, len(batch)*5)
		for _, key := range batch {
			clauses = append(clauses, "(provider = ? AND model = ? AND instance_id = ? AND instance_kind = ? AND probe_version = ?)")
			args = append(args, key.Provider, key.Model, key.Instance.ID, string(key.Instance.Kind), key.Version)
		}
		rows, queryErr := s.client.DB().QueryContext(ctx, `
SELECT provider, model, instance_id, instance_kind, probe_version,
       status, checked_at, expires_at, lease_until, lease_id, reason
FROM `+s.table+`
WHERE `+strings.Join(clauses, " OR "), args...)
		if queryErr != nil {
			return nil, fmt.Errorf("batch read %s model tool probes: %w", s.backend, queryErr)
		}
		for rows.Next() {
			var (
				record       Record
				instanceKind string
			)
			if scanErr := rows.Scan(
				&record.Provider,
				&record.Model,
				&record.Instance.ID,
				&instanceKind,
				&record.Version,
				&record.Status,
				&record.CheckedAt,
				&record.ExpiresAt,
				&record.LeaseUntil,
				&record.LeaseID,
				&record.Reason,
			); scanErr != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("scan %s model tool probe batch: %w", s.backend, scanErr)
			}
			record.Instance.Kind = types.ProviderInstanceIdentityKind(instanceKind)
			normalized, normalizeErr := NormalizeRecord(record)
			if normalizeErr != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("normalize %s model tool probe batch: %w", s.backend, normalizeErr)
			}
			records[normalized.Key] = normalized
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("iterate %s model tool probe batch: %w", s.backend, rowsErr)
		}
		if closeErr := rows.Close(); closeErr != nil {
			return nil, fmt.Errorf("close %s model tool probe batch: %w", s.backend, closeErr)
		}
	}
	return records, nil
}

func (s *SQLStore) Acquire(ctx context.Context, key Key, now time.Time, leaseUntil time.Time, leaseID string) (Record, bool, error) {
	key, err := NormalizeKey(key)
	if err != nil {
		return Record{}, false, err
	}
	if now.IsZero() || leaseUntil.IsZero() || !leaseUntil.After(now) || leaseID == "" {
		return Record{}, false, ErrInvalid
	}
	now = now.UTC()
	leaseUntil = leaseUntil.UTC()
	tx, err := s.client.DB().BeginTx(ctx, nil)
	if err != nil {
		return Record{}, false, fmt.Errorf("begin %s model tool probe lease: %w", s.backend, err)
	}
	defer func() { _ = tx.Rollback() }()

	current, err := s.get(ctx, tx, key, s.client.Dialect() == storage.DialectPostgres)
	if err == nil {
		if current.Active(now) || current.LeaseActive(now) {
			if err := tx.Commit(); err != nil {
				return Record{}, false, fmt.Errorf("commit %s model tool probe lease read: %w", s.backend, err)
			}
			return current, false, nil
		}
		current.Status = StatusTesting
		current.CheckedAt = now
		current.ExpiresAt = leaseUntil
		current.LeaseUntil = leaseUntil
		current.LeaseID = leaseID
		current.Reason = ReasonNone
		if _, err := tx.ExecContext(ctx, `
UPDATE `+s.table+`
SET status = ?, checked_at = ?, expires_at = ?, lease_until = ?, lease_id = ?, reason = ?
WHERE provider = ? AND model = ? AND instance_id = ? AND instance_kind = ? AND probe_version = ?`,
			current.Status, current.CheckedAt, current.ExpiresAt, current.LeaseUntil, current.LeaseID, current.Reason,
			key.Provider, key.Model, key.Instance.ID, string(key.Instance.Kind), key.Version,
		); err != nil {
			return Record{}, false, fmt.Errorf("lease %s model tool probe: %w", s.backend, err)
		}
		if err := tx.Commit(); err != nil {
			return Record{}, false, fmt.Errorf("commit %s model tool probe lease: %w", s.backend, err)
		}
		return current, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Record{}, false, fmt.Errorf("read %s model tool probe lease: %w", s.backend, err)
	}
	record := Record{
		Key:        key,
		Status:     StatusTesting,
		CheckedAt:  now,
		ExpiresAt:  leaseUntil,
		LeaseUntil: leaseUntil,
		LeaseID:    leaseID,
	}
	result, err := tx.ExecContext(ctx, `
INSERT INTO `+s.table+` (
    provider, model, instance_id, instance_kind, probe_version,
    status, checked_at, expires_at, lease_until, lease_id, reason
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(provider, model, instance_id, instance_kind, probe_version) DO NOTHING`,
		key.Provider, key.Model, key.Instance.ID, string(key.Instance.Kind), key.Version,
		record.Status, record.CheckedAt, record.ExpiresAt, record.LeaseUntil, record.LeaseID, record.Reason,
	)
	if err != nil {
		return Record{}, false, fmt.Errorf("create %s model tool probe lease: %w", s.backend, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return Record{}, false, fmt.Errorf("inspect %s model tool probe lease: %w", s.backend, err)
	}
	if rows == 0 {
		if err := tx.Commit(); err != nil {
			return Record{}, false, fmt.Errorf("commit %s model tool probe lease collision: %w", s.backend, err)
		}
		current, _, err := s.Get(ctx, key)
		return current, false, err
	}
	if err := tx.Commit(); err != nil {
		return Record{}, false, fmt.Errorf("commit %s model tool probe lease: %w", s.backend, err)
	}
	return record, true, nil
}

func (s *SQLStore) Complete(ctx context.Context, record Record) (Record, error) {
	record, err := NormalizeRecord(record)
	if err != nil {
		return Record{}, err
	}
	if record.Status == StatusTesting || record.LeaseID == "" {
		return Record{}, ErrInvalid
	}
	result, err := s.client.DB().ExecContext(ctx, `
UPDATE `+s.table+`
SET status = ?, checked_at = ?, expires_at = ?, lease_until = ?, lease_id = '', reason = ?
WHERE provider = ? AND model = ? AND instance_id = ? AND instance_kind = ? AND probe_version = ?
  AND status = ? AND lease_id = ?`,
		record.Status, record.CheckedAt, record.ExpiresAt, time.Time{}, record.Reason,
		record.Provider, record.Model, record.Instance.ID, string(record.Instance.Kind), record.Version,
		StatusTesting, record.LeaseID,
	)
	if err != nil {
		return Record{}, fmt.Errorf("complete %s model tool probe: %w", s.backend, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return Record{}, fmt.Errorf("inspect %s model tool probe completion: %w", s.backend, err)
	}
	if rows != 1 {
		return Record{}, ErrLeaseLost
	}
	record.LeaseUntil = time.Time{}
	record.LeaseID = ""
	return record, nil
}

type probeQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *SQLStore) get(ctx context.Context, queryer probeQueryer, key Key, lock bool) (Record, error) {
	query := `
SELECT status, checked_at, expires_at, lease_until, lease_id, reason
FROM ` + s.table + `
WHERE provider = ? AND model = ? AND instance_id = ? AND instance_kind = ? AND probe_version = ?`
	if lock && s.client.Dialect() == storage.DialectPostgres {
		query += " FOR UPDATE"
	}
	record := Record{Key: key}
	err := queryer.QueryRowContext(ctx, query,
		key.Provider, key.Model, key.Instance.ID, string(key.Instance.Kind), key.Version,
	).Scan(&record.Status, &record.CheckedAt, &record.ExpiresAt, &record.LeaseUntil, &record.LeaseID, &record.Reason)
	if err != nil {
		return Record{}, err
	}
	return NormalizeRecord(record)
}
