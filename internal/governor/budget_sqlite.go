package governor

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/hecate/agent-runtime/internal/storage"
)

// maxBudgetEventListLimit caps how many budget events a single List
// call can return. Bounds the gateway's memory growth and keeps the
// observability surface predictable; the retention worker prunes the
// table separately.
const maxBudgetEventListLimit = 1_000

// SQLiteBudgetStore is the budget tracker's persistent backing store.
// Single row per balance entry plus an append-only events table.
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
//   - Debit/Credit/SetBalance use a single atomic UPDATE — SQLite
//     serializes writes via its file lock, so a single statement on a
//     row is atomic without needing an explicit transaction.
type SQLiteBudgetStore struct {
	db          *sql.DB
	table       string
	eventsTable string
}

func NewSQLiteBudgetStore(ctx context.Context, client *storage.SQLiteClient) (*SQLiteBudgetStore, error) {
	if client == nil || client.DB() == nil {
		return nil, fmt.Errorf("sqlite client is required")
	}

	store := &SQLiteBudgetStore{
		db:          client.DB(),
		table:       client.QualifiedTable("budget"),
		eventsTable: client.QualifiedTable("budget_events"),
	}
	if err := store.migrate(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *SQLiteBudgetStore) Snapshot(ctx context.Context, key string) (AccountState, bool, error) {
	var account AccountState
	err := s.db.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT budget_key, balance_micros_usd, credited_micros_usd, debited_micros_usd, updated_at FROM %s WHERE budget_key = ?`, s.table),
		key,
	).Scan(&account.Key, &account.BalanceMicrosUSD, &account.CreditedMicrosUSD, &account.DebitedMicrosUSD, &account.UpdatedAt)
	if err == sql.ErrNoRows {
		return AccountState{}, false, nil
	}
	return account, err == nil, err
}

func (s *SQLiteBudgetStore) Debit(ctx context.Context, event UsageEvent) (AccountState, error) {
	if event.BudgetKey == "" || event.CostMicros <= 0 {
		return AccountState{Key: event.BudgetKey}, nil
	}
	now := time.Now().UTC()
	// Single-statement upsert: the WHERE-less arithmetic on the
	// existing row is atomic under SQLite's writer lock, so concurrent
	// Debits don't lose updates the way a read-modify-write would.
	_, err := s.db.ExecContext(ctx,
		fmt.Sprintf(`
			INSERT INTO %s (budget_key, balance_micros_usd, credited_micros_usd, debited_micros_usd, updated_at)
			VALUES (?, ? * -1, 0, ?, ?)
			ON CONFLICT (budget_key)
			DO UPDATE SET
				balance_micros_usd = balance_micros_usd - excluded.debited_micros_usd,
				debited_micros_usd = debited_micros_usd + excluded.debited_micros_usd,
				updated_at = excluded.updated_at
		`, s.table),
		event.BudgetKey,
		event.CostMicros,
		event.CostMicros,
		now,
	)
	if err != nil {
		return AccountState{}, err
	}
	account, _, err := s.Snapshot(ctx, event.BudgetKey)
	return account, err
}

func (s *SQLiteBudgetStore) Reset(ctx context.Context, key string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		fmt.Sprintf(`
			INSERT INTO %s (budget_key, balance_micros_usd, credited_micros_usd, debited_micros_usd, updated_at)
			VALUES (?, 0, 0, 0, ?)
			ON CONFLICT (budget_key)
			DO UPDATE SET balance_micros_usd = 0, credited_micros_usd = 0, debited_micros_usd = 0, updated_at = excluded.updated_at
		`, s.table),
		key,
		now,
	)
	return err
}

func (s *SQLiteBudgetStore) SetBalance(ctx context.Context, key string, value int64) (AccountState, error) {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		fmt.Sprintf(`
			INSERT INTO %s (budget_key, balance_micros_usd, credited_micros_usd, debited_micros_usd, updated_at)
			VALUES (?, ?, 0, 0, ?)
			ON CONFLICT (budget_key)
			DO UPDATE SET balance_micros_usd = excluded.balance_micros_usd, updated_at = excluded.updated_at
		`, s.table),
		key,
		value,
		now,
	)
	if err != nil {
		return AccountState{}, err
	}
	account, _, err := s.Snapshot(ctx, key)
	return account, err
}

func (s *SQLiteBudgetStore) Credit(ctx context.Context, key string, delta int64) (AccountState, error) {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		fmt.Sprintf(`
			INSERT INTO %s (budget_key, balance_micros_usd, credited_micros_usd, debited_micros_usd, updated_at)
			VALUES (?, ?, ?, 0, ?)
			ON CONFLICT (budget_key)
			DO UPDATE SET
				balance_micros_usd = balance_micros_usd + excluded.balance_micros_usd,
				credited_micros_usd = credited_micros_usd + excluded.credited_micros_usd,
				updated_at = excluded.updated_at
		`, s.table),
		key,
		delta,
		delta,
		now,
	)
	if err != nil {
		return AccountState{}, err
	}
	account, _, err := s.Snapshot(ctx, key)
	return account, err
}

func (s *SQLiteBudgetStore) AppendEvent(ctx context.Context, event BudgetEvent) error {
	if event.Key == "" {
		return nil
	}

	_, err := s.db.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (
			budget_key,
			event_type,
			scope,
			provider,
			model,
			request_id,
			actor,
			detail,
			amount_micros_usd,
			balance_micros_usd,
			credited_micros_usd,
			debited_micros_usd,
			prompt_tokens,
			completion_tokens,
			total_tokens,
			occurred_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
		event.BalanceMicrosUSD,
		event.CreditedMicrosUSD,
		event.DebitedMicrosUSD,
		event.PromptTokens,
		event.CompletionTokens,
		event.TotalTokens,
		event.OccurredAt,
	)
	return err
}

func (s *SQLiteBudgetStore) ListEvents(ctx context.Context, key string, limit int) ([]BudgetEvent, error) {
	if limit <= 0 {
		limit = 20
	} else if limit > maxBudgetEventListLimit {
		limit = maxBudgetEventListLimit
	}

	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT
			budget_key,
			event_type,
			scope,
			provider,
			model,
			request_id,
			actor,
			detail,
			amount_micros_usd,
			balance_micros_usd,
			credited_micros_usd,
			debited_micros_usd,
			prompt_tokens,
			completion_tokens,
			total_tokens,
			occurred_at
		FROM %s
		WHERE budget_key = ?
		ORDER BY occurred_at DESC, id DESC
		LIMIT ?
	`, s.eventsTable), key, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Pre-allocate to the constant cap rather than `limit` so the
	// user-controlled value never reaches make()'s size argument
	// (keeps user-controlled values out of make()'s size argument).
	events := make([]BudgetEvent, 0, maxBudgetEventListLimit)
	for rows.Next() {
		var event BudgetEvent
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
			&event.BalanceMicrosUSD,
			&event.CreditedMicrosUSD,
			&event.DebitedMicrosUSD,
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

func (s *SQLiteBudgetStore) ListRecentEvents(ctx context.Context, limit int) ([]BudgetEvent, error) {
	if limit <= 0 {
		limit = 20
	} else if limit > maxBudgetEventListLimit {
		limit = maxBudgetEventListLimit
	}

	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT
			budget_key,
			event_type,
			scope,
			provider,
			model,
			request_id,
			actor,
			detail,
			amount_micros_usd,
			balance_micros_usd,
			credited_micros_usd,
			debited_micros_usd,
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

	events := make([]BudgetEvent, 0, maxBudgetEventListLimit)
	for rows.Next() {
		var event BudgetEvent
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
			&event.BalanceMicrosUSD,
			&event.CreditedMicrosUSD,
			&event.DebitedMicrosUSD,
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

func (s *SQLiteBudgetStore) PruneEvents(ctx context.Context, maxAge time.Duration, maxCount int) (int, error) {
	deleted := int64(0)

	if maxAge > 0 {
		result, err := s.db.ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE occurred_at < ?`, s.eventsTable),
			time.Now().Add(-maxAge).UTC(),
		)
		if err != nil {
			return 0, fmt.Errorf("delete aged sqlite budget events: %w", err)
		}
		count, _ := result.RowsAffected()
		deleted += count
	}

	if maxCount > 0 {
		// Keep the newest maxCount rows per budget_key, drop the rest.
		// SQLite supports window functions (3.25+), so ROW_NUMBER keeps
		// the max-count pruning deterministic.
		result, err := s.db.ExecContext(ctx, fmt.Sprintf(`
			DELETE FROM %s
			WHERE id IN (
				SELECT id
				FROM (
					SELECT id,
					       ROW_NUMBER() OVER (PARTITION BY budget_key ORDER BY occurred_at DESC, id DESC) AS row_num
					FROM %s
				) ranked
				WHERE ranked.row_num > ?
			)
		`, s.eventsTable, s.eventsTable), maxCount)
		if err != nil {
			return 0, fmt.Errorf("enforce sqlite budget event max count: %w", err)
		}
		count, _ := result.RowsAffected()
		deleted += count
	}

	return int(deleted), nil
}

func (s *SQLiteBudgetStore) migrate(ctx context.Context) error {
	// updated_at/occurred_at use TIMESTAMP (not TEXT) so the
	// modernc.org/sqlite driver round-trips time.Time directly. With
	// TEXT, the driver returns a string and Scan into *time.Time fails.
	_, err := s.db.ExecContext(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			budget_key TEXT PRIMARY KEY,
			balance_micros_usd INTEGER NOT NULL DEFAULT 0,
			credited_micros_usd INTEGER NOT NULL DEFAULT 0,
			debited_micros_usd INTEGER NOT NULL DEFAULT 0,
			updated_at TIMESTAMP NOT NULL
		)
	`, s.table))
	if err != nil {
		return fmt.Errorf("migrate sqlite budget store: %w", err)
	}

	_, err = s.db.ExecContext(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			budget_key TEXT NOT NULL,
			event_type TEXT NOT NULL,
			scope TEXT NOT NULL DEFAULT '',
			provider TEXT NOT NULL DEFAULT '',
			tenant TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			request_id TEXT NOT NULL DEFAULT '',
			actor TEXT NOT NULL DEFAULT '',
			detail TEXT NOT NULL DEFAULT '',
			amount_micros_usd INTEGER NOT NULL DEFAULT 0,
			balance_micros_usd INTEGER NOT NULL DEFAULT 0,
			credited_micros_usd INTEGER NOT NULL DEFAULT 0,
			debited_micros_usd INTEGER NOT NULL DEFAULT 0,
			prompt_tokens INTEGER NOT NULL DEFAULT 0,
			completion_tokens INTEGER NOT NULL DEFAULT 0,
			total_tokens INTEGER NOT NULL DEFAULT 0,
			occurred_at TIMESTAMP NOT NULL
		)
	`, s.eventsTable))
	if err != nil {
		return fmt.Errorf("migrate sqlite budget event store: %w", err)
	}

	// Index name uses the unquoted table identifier (matches the
	// retention SQLite store convention).
	indexName := strings.Trim(s.eventsTable, `"`) + "_budget_key_occurred_at_idx"
	_, err = s.db.ExecContext(ctx, fmt.Sprintf(`
		CREATE INDEX IF NOT EXISTS "%s"
		ON %s (budget_key, occurred_at DESC)
	`, indexName, s.eventsTable))
	if err != nil {
		return fmt.Errorf("migrate sqlite budget event index: %w", err)
	}
	return nil
}
