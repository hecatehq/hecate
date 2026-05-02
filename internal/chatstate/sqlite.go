package chatstate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hecate/agent-runtime/internal/storage"
	"github.com/hecate/agent-runtime/pkg/types"
)

// SQLiteStore mirrors PostgresStore — same Store-interface surface, same
// sessions / messages / provider_calls table shape — so the gateway can
// swap chat-session backends purely via config without touching call
// sites.
//
// Differences from the Postgres flavor that aren't accidental:
//   - placeholders are `?` rather than `$N`.
//   - timestamp columns are TEXT (SQLite has no TIMESTAMPTZ); the driver
//     handles time.Time round-tripping via RFC3339-style encoding.
//   - cascade-on-delete is declared on the foreign keys
//     (PRAGMA foreign_keys = ON is set by SQLiteClient on every conn).
//   - no schema namespacing — QualifiedTable returns "hecate_<name>".
type SQLiteStore struct {
	client             *storage.SQLiteClient
	sessionsTable      string
	messagesTable      string
	providerCallsTable string
}

func NewSQLiteStore(ctx context.Context, client *storage.SQLiteClient) (*SQLiteStore, error) {
	if client == nil || client.DB() == nil {
		return nil, fmt.Errorf("sqlite client is required")
	}
	store := &SQLiteStore{
		client:             client,
		sessionsTable:      client.QualifiedTable("chat_sessions"),
		messagesTable:      client.QualifiedTable("chat_session_messages"),
		providerCallsTable: client.QualifiedTable("chat_session_provider_calls"),
	}
	if err := store.migrate(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *SQLiteStore) Backend() string {
	return "sqlite"
}

func (s *SQLiteStore) CreateSession(ctx context.Context, session types.ChatSession) (types.ChatSession, error) {
	if session.ID == "" {
		return types.ChatSession{}, fmt.Errorf("session id is required")
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = time.Now().UTC()
	}
	session.UpdatedAt = session.CreatedAt
	_, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(
			`INSERT INTO %s (id, title, system_prompt, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT(id) DO UPDATE
			 SET title = excluded.title,
			     system_prompt = excluded.system_prompt,
			     updated_at = excluded.updated_at`,
			s.sessionsTable,
		),
		session.ID,
		session.Title,
		session.SystemPrompt,
		session.CreatedAt.UTC(),
		session.UpdatedAt.UTC(),
	)
	if err != nil {
		return types.ChatSession{}, fmt.Errorf("write sqlite chat session: %w", err)
	}
	return s.loadSession(ctx, session.ID)
}

func (s *SQLiteStore) GetSession(ctx context.Context, id string) (types.ChatSession, bool, error) {
	session, err := s.loadSession(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return types.ChatSession{}, false, nil
		}
		return types.ChatSession{}, false, err
	}
	return session, true, nil
}

func (s *SQLiteStore) ListSessions(ctx context.Context, filter Filter) ([]types.ChatSession, error) {
	query := fmt.Sprintf(`SELECT id, title, system_prompt, created_at, updated_at FROM %s`, s.sessionsTable)
	args := make([]any, 0, 2)
	query += ` ORDER BY updated_at DESC, created_at DESC`
	if filter.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, filter.Limit)
	}
	if filter.Offset > 0 {
		// SQLite requires LIMIT before OFFSET; if no explicit limit was
		// requested but an offset is, fall back to a sentinel large limit
		// (matching SQLite docs' recommended `LIMIT -1` for "all rows").
		if filter.Limit <= 0 {
			query += ` LIMIT -1`
		}
		query += ` OFFSET ?`
		args = append(args, filter.Offset)
	}
	rows, err := s.client.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list sqlite chat sessions: %w", err)
	}
	defer rows.Close()

	var items []types.ChatSession
	for rows.Next() {
		var session types.ChatSession
		if err := rows.Scan(&session.ID, &session.Title, &session.SystemPrompt, &session.CreatedAt, &session.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan sqlite chat session: %w", err)
		}
		items = append(items, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sqlite chat sessions: %w", err)
	}
	if len(items) == 0 {
		return items, nil
	}
	if err := s.attachLatestCalls(ctx, items); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *SQLiteStore) AppendExchange(ctx context.Context, sessionID string, messages []types.ChatSessionMessage, call types.ChatProviderCall) (types.ChatSession, error) {
	now := time.Now().UTC()
	if call.CreatedAt.IsZero() {
		call.CreatedAt = now
	}

	tx, err := s.client.DB().BeginTx(ctx, nil)
	if err != nil {
		return types.ChatSession{}, fmt.Errorf("begin sqlite tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var nextSeq sql.NullInt64
	if err := tx.QueryRowContext(
		ctx,
		fmt.Sprintf(`SELECT COALESCE(MAX(sequence), -1) + 1 FROM %s WHERE session_id = ?`, s.messagesTable),
		sessionID,
	).Scan(&nextSeq); err != nil {
		return types.ChatSession{}, fmt.Errorf("read next message sequence: %w", err)
	}

	if _, err := tx.ExecContext(
		ctx,
		fmt.Sprintf(
			`INSERT INTO %s (
				id, session_id, request_id, requested_provider, provider, provider_kind,
				requested_model, model, cost_micros_usd, prompt_tokens, completion_tokens, total_tokens, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			s.providerCallsTable,
		),
		call.ID,
		sessionID,
		call.RequestID,
		call.RequestedProvider,
		call.Provider,
		call.ProviderKind,
		call.RequestedModel,
		call.Model,
		call.CostMicrosUSD,
		call.PromptTokens,
		call.CompletionTokens,
		call.TotalTokens,
		call.CreatedAt.UTC(),
	); err != nil {
		return types.ChatSession{}, fmt.Errorf("insert sqlite provider call: %w", err)
	}

	for i := range messages {
		if messages[i].CreatedAt.IsZero() {
			messages[i].CreatedAt = now
		}
		messages[i].Sequence = int(nextSeq.Int64) + i
		messageJSON, err := json.Marshal(messages[i].Message)
		if err != nil {
			return types.ChatSession{}, fmt.Errorf("marshal chat message: %w", err)
		}
		var producedBy sql.NullString
		if messages[i].ProducedByCallID != "" {
			producedBy = sql.NullString{String: messages[i].ProducedByCallID, Valid: true}
		}
		if _, err := tx.ExecContext(
			ctx,
			fmt.Sprintf(
				`INSERT INTO %s (id, session_id, sequence, produced_by_call_id, message_json, created_at)
				 VALUES (?, ?, ?, ?, ?, ?)`,
				s.messagesTable,
			),
			messages[i].ID,
			sessionID,
			messages[i].Sequence,
			producedBy,
			string(messageJSON),
			messages[i].CreatedAt.UTC(),
		); err != nil {
			return types.ChatSession{}, fmt.Errorf("insert sqlite chat message: %w", err)
		}
	}

	if _, err := tx.ExecContext(
		ctx,
		fmt.Sprintf(`UPDATE %s SET updated_at = ? WHERE id = ?`, s.sessionsTable),
		call.CreatedAt.UTC(),
		sessionID,
	); err != nil {
		return types.ChatSession{}, fmt.Errorf("update sqlite chat session timestamp: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return types.ChatSession{}, fmt.Errorf("commit sqlite tx: %w", err)
	}
	return s.loadSession(ctx, sessionID)
}

func (s *SQLiteStore) DeleteSession(ctx context.Context, id string) error {
	if _, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(`DELETE FROM %s WHERE session_id = ?`, s.messagesTable),
		id,
	); err != nil {
		return fmt.Errorf("delete sqlite chat messages: %w", err)
	}
	if _, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(`DELETE FROM %s WHERE session_id = ?`, s.providerCallsTable),
		id,
	); err != nil {
		return fmt.Errorf("delete sqlite chat provider calls: %w", err)
	}
	if _, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(`DELETE FROM %s WHERE id = ?`, s.sessionsTable),
		id,
	); err != nil {
		return fmt.Errorf("delete sqlite chat session: %w", err)
	}
	return nil
}

func (s *SQLiteStore) UpdateSession(ctx context.Context, id string, title string) (types.ChatSession, error) {
	now := time.Now().UTC()
	if _, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(`UPDATE %s SET title = ?, updated_at = ? WHERE id = ?`, s.sessionsTable),
		title, now, id,
	); err != nil {
		return types.ChatSession{}, fmt.Errorf("update sqlite chat session: %w", err)
	}
	return s.loadSession(ctx, id)
}

func (s *SQLiteStore) UpdateSessionSystemPrompt(ctx context.Context, id string, prompt string) (types.ChatSession, error) {
	now := time.Now().UTC()
	if _, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(`UPDATE %s SET system_prompt = ?, updated_at = ? WHERE id = ?`, s.sessionsTable),
		prompt, now, id,
	); err != nil {
		return types.ChatSession{}, fmt.Errorf("update sqlite chat session system prompt: %w", err)
	}
	return s.loadSession(ctx, id)
}

func (s *SQLiteStore) migrate(ctx context.Context) error {
	if _, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS %s (
				id TEXT PRIMARY KEY,
				title TEXT NOT NULL,
				system_prompt TEXT NOT NULL DEFAULT '',
				created_at TIMESTAMP NOT NULL,
				updated_at TIMESTAMP NOT NULL
			)`,
			s.sessionsTable,
		),
	); err != nil {
		return fmt.Errorf("migrate sqlite chat sessions: %w", err)
	}
	// Backfill column for databases that pre-date system_prompt. SQLite
	// has no `ADD COLUMN IF NOT EXISTS`, so we probe PRAGMA table_info
	// and ALTER only when missing.
	hasSystemPrompt, err := s.columnExists(ctx, s.sessionsTable, "system_prompt")
	if err != nil {
		return fmt.Errorf("inspect sqlite chat sessions columns: %w", err)
	}
	if !hasSystemPrompt {
		if _, err := s.client.DB().ExecContext(
			ctx,
			fmt.Sprintf(`ALTER TABLE %s ADD COLUMN system_prompt TEXT NOT NULL DEFAULT ''`, s.sessionsTable),
		); err != nil {
			return fmt.Errorf("migrate sqlite chat sessions system_prompt: %w", err)
		}
	}
	// Drop the legacy chat_session_turns table on upgrade. The new
	// schema is incompatible row-shape; we are intentionally not
	// carrying turn data forward (session metadata survives).
	if _, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(`DROP TABLE IF EXISTS %s`, s.client.QualifiedTable("chat_session_turns")),
	); err != nil {
		return fmt.Errorf("drop legacy sqlite chat session turns: %w", err)
	}
	if _, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS %s (
				id TEXT PRIMARY KEY,
				session_id TEXT NOT NULL REFERENCES %s (id) ON DELETE CASCADE,
				request_id TEXT NOT NULL,
				requested_provider TEXT NOT NULL,
				provider TEXT NOT NULL,
				provider_kind TEXT NOT NULL,
				requested_model TEXT NOT NULL,
				model TEXT NOT NULL,
				cost_micros_usd INTEGER NOT NULL,
				prompt_tokens INTEGER NOT NULL,
				completion_tokens INTEGER NOT NULL,
				total_tokens INTEGER NOT NULL,
				created_at TIMESTAMP NOT NULL
			)`,
			s.providerCallsTable,
			s.sessionsTable,
		),
	); err != nil {
		return fmt.Errorf("migrate sqlite chat session provider calls: %w", err)
	}
	if _, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS %s (
				id TEXT PRIMARY KEY,
				session_id TEXT NOT NULL REFERENCES %s (id) ON DELETE CASCADE,
				sequence INTEGER NOT NULL,
				produced_by_call_id TEXT REFERENCES %s (id) ON DELETE SET NULL,
				message_json TEXT NOT NULL,
				created_at TIMESTAMP NOT NULL,
				UNIQUE (session_id, sequence)
			)`,
			s.messagesTable,
			s.sessionsTable,
			s.providerCallsTable,
		),
	); err != nil {
		return fmt.Errorf("migrate sqlite chat session messages: %w", err)
	}

	// Index names use unquoted identifiers paired with quoted target tables.
	messagesIndex := strings.Trim(s.messagesTable, `"`) + "_session_seq_idx"
	if _, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "%s" ON %s (session_id, sequence)`, messagesIndex, s.messagesTable),
	); err != nil {
		return fmt.Errorf("migrate sqlite chat session messages index: %w", err)
	}
	callsIndex := strings.Trim(s.providerCallsTable, `"`) + "_session_created_idx"
	if _, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "%s" ON %s (session_id, created_at)`, callsIndex, s.providerCallsTable),
	); err != nil {
		return fmt.Errorf("migrate sqlite chat session provider calls index: %w", err)
	}
	sessionsIndex := strings.Trim(s.sessionsTable, `"`) + "_updated_idx"
	if _, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "%s" ON %s (updated_at)`, sessionsIndex, s.sessionsTable),
	); err != nil {
		return fmt.Errorf("migrate sqlite chat sessions index: %w", err)
	}
	return nil
}

// columnExists checks PRAGMA table_info for the given column. The
// quotedTable argument is the already-quoted identifier as stored on the
// store (e.g. `"hecate_chat_sessions"`); we strip the quotes for the
// pragma form.
func (s *SQLiteStore) columnExists(ctx context.Context, quotedTable, column string) (bool, error) {
	bare := strings.Trim(quotedTable, `"`)
	rows, err := s.client.DB().QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info("%s")`, bare))
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid       int
			name      string
			ctype     string
			notnull   int
			dfltValue sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return false, nil
}

func (s *SQLiteStore) loadSession(ctx context.Context, id string) (types.ChatSession, error) {
	var session types.ChatSession
	err := s.client.DB().QueryRowContext(
		ctx,
		fmt.Sprintf(`SELECT id, title, system_prompt, created_at, updated_at FROM %s WHERE id = ?`, s.sessionsTable),
		id,
	).Scan(&session.ID, &session.Title, &session.SystemPrompt, &session.CreatedAt, &session.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return types.ChatSession{}, sql.ErrNoRows
		}
		return types.ChatSession{}, fmt.Errorf("read sqlite chat session: %w", err)
	}

	calls, err := s.loadProviderCalls(ctx, id)
	if err != nil {
		return types.ChatSession{}, err
	}
	session.ProviderCalls = calls

	messages, err := s.loadMessages(ctx, id)
	if err != nil {
		return types.ChatSession{}, err
	}
	session.Messages = messages
	return session, nil
}

func (s *SQLiteStore) loadProviderCalls(ctx context.Context, sessionID string) ([]types.ChatProviderCall, error) {
	rows, err := s.client.DB().QueryContext(
		ctx,
		fmt.Sprintf(
			`SELECT id, request_id, requested_provider, provider, provider_kind,
			        requested_model, model, cost_micros_usd, prompt_tokens, completion_tokens, total_tokens, created_at
			 FROM %s
			 WHERE session_id = ?
			 ORDER BY created_at ASC, id ASC`,
			s.providerCallsTable,
		),
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("list sqlite chat provider calls: %w", err)
	}
	defer rows.Close()

	var calls []types.ChatProviderCall
	for rows.Next() {
		var call types.ChatProviderCall
		if err := rows.Scan(
			&call.ID,
			&call.RequestID,
			&call.RequestedProvider,
			&call.Provider,
			&call.ProviderKind,
			&call.RequestedModel,
			&call.Model,
			&call.CostMicrosUSD,
			&call.PromptTokens,
			&call.CompletionTokens,
			&call.TotalTokens,
			&call.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan sqlite chat provider call: %w", err)
		}
		calls = append(calls, call)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sqlite chat provider calls: %w", err)
	}
	return calls, nil
}

func (s *SQLiteStore) loadMessages(ctx context.Context, sessionID string) ([]types.ChatSessionMessage, error) {
	rows, err := s.client.DB().QueryContext(
		ctx,
		fmt.Sprintf(
			`SELECT id, sequence, produced_by_call_id, message_json, created_at
			 FROM %s
			 WHERE session_id = ?
			 ORDER BY sequence ASC`,
			s.messagesTable,
		),
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("list sqlite chat messages: %w", err)
	}
	defer rows.Close()

	var messages []types.ChatSessionMessage
	for rows.Next() {
		var (
			msg         types.ChatSessionMessage
			producedBy  sql.NullString
			messageJSON string
		)
		if err := rows.Scan(&msg.ID, &msg.Sequence, &producedBy, &messageJSON, &msg.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan sqlite chat message: %w", err)
		}
		if producedBy.Valid {
			msg.ProducedByCallID = producedBy.String
		}
		if err := json.Unmarshal([]byte(messageJSON), &msg.Message); err != nil {
			return nil, fmt.Errorf("decode sqlite chat message body (id=%s): %w", msg.ID, err)
		}
		messages = append(messages, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sqlite chat messages: %w", err)
	}
	return messages, nil
}

// attachLatestCalls populates the most-recent ProviderCall on each
// session in items with a single query (avoiding N+1). SQLite's
// equivalent of Postgres's DISTINCT ON is a correlated subquery.
func (s *SQLiteStore) attachLatestCalls(ctx context.Context, items []types.ChatSession) error {
	if len(items) == 0 {
		return nil
	}
	placeholders := make([]string, len(items))
	args := make([]any, len(items))
	for i, item := range items {
		placeholders[i] = "?"
		args[i] = item.ID
	}
	query := fmt.Sprintf(
		`SELECT c.session_id, c.id, c.request_id, c.requested_provider, c.provider, c.provider_kind,
		        c.requested_model, c.model, c.cost_micros_usd, c.prompt_tokens, c.completion_tokens, c.total_tokens, c.created_at
		 FROM %s c
		 INNER JOIN (
		     SELECT session_id, MAX(created_at) AS max_created
		     FROM %s
		     WHERE session_id IN (%s)
		     GROUP BY session_id
		 ) latest ON latest.session_id = c.session_id AND latest.max_created = c.created_at`,
		s.providerCallsTable,
		s.providerCallsTable,
		strings.Join(placeholders, ", "),
	)
	rows, err := s.client.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("list sqlite chat session latest calls: %w", err)
	}
	defer rows.Close()

	latestBySession := make(map[string]types.ChatProviderCall, len(items))
	for rows.Next() {
		var (
			sessionID string
			call      types.ChatProviderCall
		)
		if err := rows.Scan(
			&sessionID,
			&call.ID,
			&call.RequestID,
			&call.RequestedProvider,
			&call.Provider,
			&call.ProviderKind,
			&call.RequestedModel,
			&call.Model,
			&call.CostMicrosUSD,
			&call.PromptTokens,
			&call.CompletionTokens,
			&call.TotalTokens,
			&call.CreatedAt,
		); err != nil {
			return fmt.Errorf("scan sqlite chat session latest call: %w", err)
		}
		// In the rare event of two calls with identical created_at,
		// the join above can return multiple rows per session — keep
		// the lexicographically-greatest id to stay deterministic.
		if existing, ok := latestBySession[sessionID]; !ok || call.ID > existing.ID {
			latestBySession[sessionID] = call
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate sqlite chat session latest calls: %w", err)
	}
	for i := range items {
		if call, ok := latestBySession[items[i].ID]; ok {
			items[i].ProviderCalls = []types.ChatProviderCall{call}
		}
	}
	return nil
}
