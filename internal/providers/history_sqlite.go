package providers

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/storage"
)

type SQLiteHealthHistoryStore struct {
	db    *sql.DB
	table string
}

func NewSQLiteHealthHistoryStore(ctx context.Context, client *storage.SQLiteClient, tableName string) (*SQLiteHealthHistoryStore, error) {
	if client == nil || client.DB() == nil {
		return nil, fmt.Errorf("sqlite client is required")
	}
	tableName = strings.TrimSpace(tableName)
	if tableName == "" {
		tableName = "provider_health_history"
	}
	store := &SQLiteHealthHistoryStore{
		db:    client.DB(),
		table: client.QualifiedTable(tableName),
	}
	if err := store.migrate(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *SQLiteHealthHistoryStore) Append(ctx context.Context, record HealthHistoryRecord) error {
	_, err := s.db.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (
			provider,
			model,
			event,
			status,
			available,
			error_message,
			error_class,
			reason,
			route_reason,
			request_id,
			trace_id,
			peer_provider,
			peer_model,
			peer_route_reason,
			health_status,
			peer_health_status,
			latency_ms,
			consecutive_failures,
			total_successes,
			total_failures,
			timeouts,
			server_errors,
			rate_limits,
			attempt_count,
			estimated_micros_usd,
			open_until,
			timestamp_utc
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, s.table),
		record.Provider,
		record.Model,
		record.Event,
		record.Status,
		boolToInt(record.Available),
		record.Error,
		record.ErrorClass,
		record.Reason,
		record.RouteReason,
		record.RequestID,
		record.TraceID,
		record.PeerProvider,
		record.PeerModel,
		record.PeerRouteReason,
		record.HealthStatus,
		record.PeerHealthStatus,
		record.LatencyMS,
		record.ConsecutiveFailures,
		record.TotalSuccesses,
		record.TotalFailures,
		record.Timeouts,
		record.ServerErrors,
		record.RateLimits,
		record.AttemptCount,
		record.EstimatedMicrosUSD,
		record.OpenUntil,
		record.Timestamp,
	)
	if err != nil {
		return fmt.Errorf("insert sqlite provider health history: %w", err)
	}
	return nil
}

func (s *SQLiteHealthHistoryStore) List(ctx context.Context, filter HealthHistoryFilter) ([]HealthHistoryRecord, error) {
	limit := normalizeHealthHistoryLimit(filter.Limit)
	args := make([]any, 0, 2)
	query := fmt.Sprintf(`
		SELECT provider, model, event, status, available, error_message, error_class,
		       reason, route_reason, request_id, trace_id, peer_provider, peer_model,
		       peer_route_reason, health_status, peer_health_status,
		       latency_ms, consecutive_failures, total_successes, total_failures,
		       timeouts, server_errors, rate_limits, attempt_count, estimated_micros_usd,
		       open_until, timestamp_utc
		FROM %s
	`, s.table)
	if strings.TrimSpace(filter.Provider) != "" {
		query += ` WHERE provider = ?`
		args = append(args, filter.Provider)
	}
	query += ` ORDER BY timestamp_utc DESC, id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list sqlite provider health history: %w", err)
	}
	defer rows.Close()

	records := make([]HealthHistoryRecord, 0, maxHealthHistoryListLimit)
	for rows.Next() {
		var record HealthHistoryRecord
		var available int
		if err := rows.Scan(
			&record.Provider,
			&record.Model,
			&record.Event,
			&record.Status,
			&available,
			&record.Error,
			&record.ErrorClass,
			&record.Reason,
			&record.RouteReason,
			&record.RequestID,
			&record.TraceID,
			&record.PeerProvider,
			&record.PeerModel,
			&record.PeerRouteReason,
			&record.HealthStatus,
			&record.PeerHealthStatus,
			&record.LatencyMS,
			&record.ConsecutiveFailures,
			&record.TotalSuccesses,
			&record.TotalFailures,
			&record.Timeouts,
			&record.ServerErrors,
			&record.RateLimits,
			&record.AttemptCount,
			&record.EstimatedMicrosUSD,
			&record.OpenUntil,
			&record.Timestamp,
		); err != nil {
			return nil, fmt.Errorf("scan sqlite provider health history: %w", err)
		}
		record.Available = available != 0
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sqlite provider health history: %w", err)
	}
	return records, nil
}

func (s *SQLiteHealthHistoryStore) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			provider TEXT NOT NULL,
			model TEXT NOT NULL DEFAULT '',
			event TEXT NOT NULL,
			status TEXT NOT NULL,
			available INTEGER NOT NULL,
			error_message TEXT NOT NULL DEFAULT '',
			error_class TEXT NOT NULL DEFAULT '',
			reason TEXT NOT NULL DEFAULT '',
			route_reason TEXT NOT NULL DEFAULT '',
			request_id TEXT NOT NULL DEFAULT '',
			trace_id TEXT NOT NULL DEFAULT '',
			peer_provider TEXT NOT NULL DEFAULT '',
			peer_model TEXT NOT NULL DEFAULT '',
			peer_route_reason TEXT NOT NULL DEFAULT '',
			health_status TEXT NOT NULL DEFAULT '',
			peer_health_status TEXT NOT NULL DEFAULT '',
			latency_ms INTEGER NOT NULL DEFAULT 0,
			consecutive_failures INTEGER NOT NULL DEFAULT 0,
			total_successes INTEGER NOT NULL DEFAULT 0,
			total_failures INTEGER NOT NULL DEFAULT 0,
			timeouts INTEGER NOT NULL DEFAULT 0,
			server_errors INTEGER NOT NULL DEFAULT 0,
			rate_limits INTEGER NOT NULL DEFAULT 0,
			attempt_count INTEGER NOT NULL DEFAULT 0,
			estimated_micros_usd INTEGER NOT NULL DEFAULT 0,
			open_until TEXT NOT NULL DEFAULT '',
			timestamp_utc TEXT NOT NULL
		)
	`, s.table))
	if err != nil {
		return fmt.Errorf("migrate sqlite provider health history store: %w", err)
	}
	for _, column := range []struct {
		name       string
		definition string
	}{
		{"route_reason", "TEXT NOT NULL DEFAULT ''"},
		{"peer_route_reason", "TEXT NOT NULL DEFAULT ''"},
		{"health_status", "TEXT NOT NULL DEFAULT ''"},
		{"peer_health_status", "TEXT NOT NULL DEFAULT ''"},
		{"attempt_count", "INTEGER NOT NULL DEFAULT 0"},
		{"estimated_micros_usd", "INTEGER NOT NULL DEFAULT 0"},
	} {
		exists, err := s.columnExists(ctx, column.name)
		if err != nil {
			return fmt.Errorf("inspect sqlite provider health history columns: %w", err)
		}
		if exists {
			continue
		}
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, s.table, column.name, column.definition)); err != nil {
			return fmt.Errorf("migrate sqlite provider health history %s: %w", column.name, err)
		}
	}
	indexName := strings.Trim(s.table, `"`) + "_provider_timestamp_idx"
	_, err = s.db.ExecContext(ctx, fmt.Sprintf(`
		CREATE INDEX IF NOT EXISTS "%s"
		ON %s (provider, timestamp_utc DESC, id DESC)
	`, indexName, s.table))
	if err != nil {
		return fmt.Errorf("migrate sqlite provider health history index: %w", err)
	}
	return nil
}

func (s *SQLiteHealthHistoryStore) columnExists(ctx context.Context, column string) (bool, error) {
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s)`, s.table))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid        int
			name       string
			typ        string
			notNull    int
			defaultVal sql.NullString
			pk         int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultVal, &pk); err != nil {
			return false, err
		}
		if strings.EqualFold(name, column) {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (s *SQLiteHealthHistoryStore) Prune(ctx context.Context, maxAge time.Duration, maxCount int) (int, error) {
	deleted := 0
	if maxAge > 0 {
		cutoff := time.Now().UTC().Add(-maxAge).Format(time.RFC3339Nano)
		result, err := s.db.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE timestamp_utc < ?`, s.table), cutoff)
		if err != nil {
			return deleted, fmt.Errorf("prune sqlite provider health history by age: %w", err)
		}
		if n, err := result.RowsAffected(); err == nil {
			deleted += int(n)
		}
	}
	if maxCount > 0 {
		result, err := s.db.ExecContext(ctx, fmt.Sprintf(`
			DELETE FROM %s
			WHERE id NOT IN (
				SELECT id FROM %s ORDER BY timestamp_utc DESC, id DESC LIMIT ?
			)
		`, s.table, s.table), maxCount)
		if err != nil {
			return deleted, fmt.Errorf("prune sqlite provider health history by count: %w", err)
		}
		if n, err := result.RowsAffected(); err == nil {
			deleted += int(n)
		}
	}
	return deleted, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
