package governor

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/hecate/agent-runtime/internal/storage"
)

// maxUsageEventListLimit caps how many usage event rows a single List
// call can return. Bounds the gateway's memory growth and keeps the
// observability surface predictable; the retention worker prunes the
// table separately.
const maxUsageEventListLimit = 1_000

// SQLiteUsageStore is the usage tracker's persistent backing store.
//
// On-disk shape:
//   - id column is `INTEGER PRIMARY KEY AUTOINCREMENT` — the SQLite
//     idiom for monotonic row ids.
//   - placeholders are `?`.
//   - timestamps live in TEXT columns (RFC3339); SQLite has no native
//     TIMESTAMPTZ but the Go driver round-trips time.Time over TEXT.
//   - upserts use `ON CONFLICT (...) DO UPDATE SET ... = excluded.<col>`
//     (lowercase `excluded`, the SQLite spelling).
//   - prune-by-count uses a correlated subquery — simpler than
//     ROW_NUMBER() and the events table is bounded by maxCount.
//   - RecordUsage uses a single atomic UPDATE — SQLite
//     serializes writes via its file lock, so a single statement on a
//     row is atomic without needing an explicit transaction.
type SQLiteUsageStore struct {
	db          *sql.DB
	table       string
	eventsTable string
}

func NewSQLiteUsageStore(ctx context.Context, client *storage.SQLiteClient) (*SQLiteUsageStore, error) {
	if client == nil || client.DB() == nil {
		return nil, fmt.Errorf("sqlite client is required")
	}

	store := &SQLiteUsageStore{
		db:          client.DB(),
		table:       client.QualifiedTable("usage"),
		eventsTable: client.QualifiedTable("usage_events"),
	}
	if err := store.migrate(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *SQLiteUsageStore) Snapshot(ctx context.Context, key string) (UsageState, bool, error) {
	var state UsageState
	err := s.db.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT usage_key, used_micros_usd, updated_at FROM %s WHERE usage_key = ?`, s.table),
		key,
	).Scan(&state.Key, &state.UsedMicrosUSD, &state.UpdatedAt)
	if err == sql.ErrNoRows {
		return UsageState{}, false, nil
	}
	return state, err == nil, err
}

func (s *SQLiteUsageStore) RecordUsage(ctx context.Context, event UsageEvent) (UsageState, error) {
	if event.UsageKey == "" || event.CostMicros <= 0 {
		return UsageState{Key: event.UsageKey}, nil
	}
	now := time.Now().UTC()
	// Single-statement upsert: the WHERE-less arithmetic on the
	// existing row is atomic under SQLite's writer lock, so concurrent
	// usage records don't lose updates the way a read-modify-write would.
	_, err := s.db.ExecContext(ctx,
		fmt.Sprintf(`
			INSERT INTO %s (usage_key, used_micros_usd, updated_at)
			VALUES (?, ?, ?)
			ON CONFLICT (usage_key)
			DO UPDATE SET
				used_micros_usd = used_micros_usd + excluded.used_micros_usd,
				updated_at = excluded.updated_at
		`, s.table),
		event.UsageKey,
		event.CostMicros,
		now,
	)
	if err != nil {
		return UsageState{}, err
	}
	state, _, err := s.Snapshot(ctx, event.UsageKey)
	return state, err
}

func (s *SQLiteUsageStore) AppendEvent(ctx context.Context, event UsageHistoryEvent) error {
	if event.Key == "" {
		return nil
	}

	_, err := s.db.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (
			usage_key,
			event_type,
			scope,
			provider,
			model,
			request_id,
			actor,
			detail,
			amount_micros_usd,
			prompt_tokens,
			completion_tokens,
			total_tokens,
			occurred_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, s.eventsTable),
		event.Key,
		event.Type,
		event.Scope,
		event.Provider,
		event.Model,
		event.RequestID,
		event.Actor,
		event.Detail,
		event.AmountMicrosUSD,
		event.PromptTokens,
		event.CompletionTokens,
		event.TotalTokens,
		event.OccurredAt,
	)
	return err
}

func (s *SQLiteUsageStore) ListEvents(ctx context.Context, key string, limit int) ([]UsageHistoryEvent, error) {
	if limit <= 0 {
		limit = 20
	} else if limit > maxUsageEventListLimit {
		limit = maxUsageEventListLimit
	}

	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT
			usage_key,
			event_type,
			scope,
			provider,
			model,
			request_id,
			actor,
			detail,
			amount_micros_usd,
			prompt_tokens,
			completion_tokens,
			total_tokens,
			occurred_at
		FROM %s
		WHERE usage_key = ?
		ORDER BY occurred_at DESC, id DESC
		LIMIT ?
	`, s.eventsTable), key, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]UsageHistoryEvent, 0, maxUsageEventListLimit)
	for rows.Next() {
		var event UsageHistoryEvent
		if err := rows.Scan(
			&event.Key,
			&event.Type,
			&event.Scope,
			&event.Provider,
			&event.Model,
			&event.RequestID,
			&event.Actor,
			&event.Detail,
			&event.AmountMicrosUSD,
			&event.PromptTokens,
			&event.CompletionTokens,
			&event.TotalTokens,
			&event.OccurredAt,
		); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func (s *SQLiteUsageStore) ListRecentEvents(ctx context.Context, limit int) ([]UsageHistoryEvent, error) {
	if limit <= 0 {
		limit = 20
	} else if limit > maxUsageEventListLimit {
		limit = maxUsageEventListLimit
	}

	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT
			usage_key,
			event_type,
			scope,
			provider,
			model,
			request_id,
			actor,
			detail,
			amount_micros_usd,
			prompt_tokens,
			completion_tokens,
			total_tokens,
			occurred_at
		FROM %s
		ORDER BY occurred_at DESC, id DESC
		LIMIT ?
	`, s.eventsTable), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]UsageHistoryEvent, 0, maxUsageEventListLimit)
	for rows.Next() {
		var event UsageHistoryEvent
		if err := rows.Scan(
			&event.Key,
			&event.Type,
			&event.Scope,
			&event.Provider,
			&event.Model,
			&event.RequestID,
			&event.Actor,
			&event.Detail,
			&event.AmountMicrosUSD,
			&event.PromptTokens,
			&event.CompletionTokens,
			&event.TotalTokens,
			&event.OccurredAt,
		); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func (s *SQLiteUsageStore) PruneEvents(ctx context.Context, maxAge time.Duration, maxCount int) (int, error) {
	deleted := int64(0)

	if maxAge > 0 {
		result, err := s.db.ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE occurred_at < ?`, s.eventsTable),
			time.Now().Add(-maxAge).UTC(),
		)
		if err != nil {
			return 0, fmt.Errorf("delete aged sqlite usage events: %w", err)
		}
		count, _ := result.RowsAffected()
		deleted += count
	}

	if maxCount > 0 {
		// Keep the newest maxCount rows per usage_key, drop the rest.
		// SQLite supports window functions (3.25+), so ROW_NUMBER keeps
		// the max-count pruning deterministic.
		result, err := s.db.ExecContext(ctx, fmt.Sprintf(`
			DELETE FROM %s
			WHERE id IN (
				SELECT id
				FROM (
					SELECT id,
					       ROW_NUMBER() OVER (PARTITION BY usage_key ORDER BY occurred_at DESC, id DESC) AS row_num
					FROM %s
				) ranked
				WHERE ranked.row_num > ?
			)
		`, s.eventsTable, s.eventsTable), maxCount)
		if err != nil {
			return 0, fmt.Errorf("enforce sqlite usage event max count: %w", err)
		}
		count, _ := result.RowsAffected()
		deleted += count
	}

	return int(deleted), nil
}

func (s *SQLiteUsageStore) migrate(ctx context.Context) error {
	// updated_at/occurred_at use TIMESTAMP (not TEXT) so the
	// modernc.org/sqlite driver round-trips time.Time directly. With
	// TEXT, the driver returns a string and Scan into *time.Time fails.
	_, err := s.db.ExecContext(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			usage_key TEXT PRIMARY KEY,
			used_micros_usd INTEGER NOT NULL DEFAULT 0,
			updated_at TIMESTAMP NOT NULL
		)
	`, s.table))
	if err != nil {
		return fmt.Errorf("migrate sqlite usage store: %w", err)
	}

	_, err = s.db.ExecContext(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			usage_key TEXT NOT NULL,
			event_type TEXT NOT NULL,
			scope TEXT NOT NULL DEFAULT '',
			provider TEXT NOT NULL DEFAULT '',
			tenant TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			request_id TEXT NOT NULL DEFAULT '',
			actor TEXT NOT NULL DEFAULT '',
			detail TEXT NOT NULL DEFAULT '',
			amount_micros_usd INTEGER NOT NULL DEFAULT 0,
			prompt_tokens INTEGER NOT NULL DEFAULT 0,
			completion_tokens INTEGER NOT NULL DEFAULT 0,
			total_tokens INTEGER NOT NULL DEFAULT 0,
			occurred_at TIMESTAMP NOT NULL
		)
	`, s.eventsTable))
	if err != nil {
		return fmt.Errorf("migrate sqlite usage events: %w", err)
	}

	// Index name uses the unquoted table identifier (matches the
	// retention SQLite store convention).
	indexName := strings.Trim(s.eventsTable, `"`) + "_usage_key_occurred_at_idx"
	_, err = s.db.ExecContext(ctx, fmt.Sprintf(`
		CREATE INDEX IF NOT EXISTS "%s"
		ON %s (usage_key, occurred_at DESC)
	`, indexName, s.eventsTable))
	if err != nil {
		return fmt.Errorf("migrate sqlite usage events index: %w", err)
	}
	return nil
}
