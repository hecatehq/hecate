package chatstate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/hecate/agent-runtime/internal/storage"
	"github.com/hecate/agent-runtime/pkg/types"
)

type Filter struct {
	Tenant string
	Limit  int
	Offset int
}

// Store persists chat sessions, the flat message stream that makes up
// each conversation, and the provider calls that produced the assistant
// half of those messages. AppendExchange is the canonical write path:
// it adds new messages and the provider call that triggered them in a
// single atomic transaction, assigning monotonic per-session Sequence
// values to the messages.
type Store interface {
	Backend() string
	CreateSession(ctx context.Context, session types.ChatSession) (types.ChatSession, error)
	GetSession(ctx context.Context, id string) (types.ChatSession, bool, error)
	ListSessions(ctx context.Context, filter Filter) ([]types.ChatSession, error)
	AppendExchange(ctx context.Context, sessionID string, messages []types.ChatSessionMessage, call types.ChatProviderCall) (types.ChatSession, error)
	DeleteSession(ctx context.Context, id string) error
	UpdateSession(ctx context.Context, id string, title string) (types.ChatSession, error)
	UpdateSessionSystemPrompt(ctx context.Context, id string, prompt string) (types.ChatSession, error)
}

type MemoryStore struct {
	mu       sync.Mutex
	sessions map[string]types.ChatSession
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{sessions: make(map[string]types.ChatSession)}
}

func (s *MemoryStore) Backend() string {
	return "memory"
}

func (s *MemoryStore) CreateSession(_ context.Context, session types.ChatSession) (types.ChatSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if session.ID == "" {
		return types.ChatSession{}, fmt.Errorf("session id is required")
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = time.Now().UTC()
	}
	session.UpdatedAt = session.CreatedAt
	session.Messages = append([]types.ChatSessionMessage(nil), session.Messages...)
	session.ProviderCalls = append([]types.ChatProviderCall(nil), session.ProviderCalls...)
	s.sessions[session.ID] = session
	return cloneSession(session), nil
}

func (s *MemoryStore) GetSession(_ context.Context, id string) (types.ChatSession, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[id]
	if !ok {
		return types.ChatSession{}, false, nil
	}
	return cloneSession(session), true, nil
}

func (s *MemoryStore) ListSessions(_ context.Context, filter Filter) ([]types.ChatSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]types.ChatSession, 0, len(s.sessions))
	for _, session := range s.sessions {
		if filter.Tenant != "" && session.Tenant != filter.Tenant {
			continue
		}
		cloned := cloneSession(session)
		// List view doesn't carry message bodies — clients that need
		// them call GetSession. We DO keep ProviderCalls so the list
		// summary can show last-call metadata (model, cost, request_id).
		cloned.Messages = nil
		items = append(items, cloned)
	}
	sortSessionsDesc(items)
	if filter.Offset > 0 {
		if filter.Offset >= len(items) {
			return nil, nil
		}
		items = items[filter.Offset:]
	}
	if filter.Limit > 0 && len(items) > filter.Limit {
		items = items[:filter.Limit]
	}
	return items, nil
}

func (s *MemoryStore) AppendExchange(_ context.Context, sessionID string, messages []types.ChatSessionMessage, call types.ChatProviderCall) (types.ChatSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[sessionID]
	if !ok {
		return types.ChatSession{}, fmt.Errorf("chat session %q not found", sessionID)
	}
	now := time.Now().UTC()
	if call.CreatedAt.IsZero() {
		call.CreatedAt = now
	}
	// Sequence values are assigned monotonically per session. Existing
	// messages occupy [0, len(Messages)); the new batch picks up from
	// there.
	nextSeq := len(session.Messages)
	for i := range messages {
		if messages[i].CreatedAt.IsZero() {
			messages[i].CreatedAt = now
		}
		messages[i].Sequence = nextSeq + i
	}
	session.Messages = append(session.Messages, messages...)
	session.ProviderCalls = append(session.ProviderCalls, call)
	session.UpdatedAt = call.CreatedAt
	s.sessions[sessionID] = session
	return cloneSession(session), nil
}

func (s *MemoryStore) DeleteSession(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[id]; !ok {
		return fmt.Errorf("chat session %q not found", id)
	}
	delete(s.sessions, id)
	return nil
}

func (s *MemoryStore) UpdateSession(_ context.Context, id string, title string) (types.ChatSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[id]
	if !ok {
		return types.ChatSession{}, fmt.Errorf("chat session %q not found", id)
	}
	session.Title = title
	session.UpdatedAt = time.Now().UTC()
	s.sessions[id] = session
	return cloneSession(session), nil
}

func (s *MemoryStore) UpdateSessionSystemPrompt(_ context.Context, id string, prompt string) (types.ChatSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[id]
	if !ok {
		return types.ChatSession{}, fmt.Errorf("chat session %q not found", id)
	}
	session.SystemPrompt = prompt
	session.UpdatedAt = time.Now().UTC()
	s.sessions[id] = session
	return cloneSession(session), nil
}

type PostgresStore struct {
	client             *storage.PostgresClient
	sessionsTable      string
	messagesTable      string
	providerCallsTable string
}

func NewPostgresStore(ctx context.Context, client *storage.PostgresClient) (*PostgresStore, error) {
	if client == nil {
		return nil, fmt.Errorf("postgres client is required")
	}
	store := &PostgresStore{
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

func (s *PostgresStore) Backend() string {
	return "postgres"
}

func (s *PostgresStore) CreateSession(ctx context.Context, session types.ChatSession) (types.ChatSession, error) {
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
			`INSERT INTO %s (id, title, system_prompt, tenant, user_name, created_at, updated_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)
			 ON CONFLICT (id) DO UPDATE
			 SET title = EXCLUDED.title,
			     system_prompt = EXCLUDED.system_prompt,
			     tenant = EXCLUDED.tenant,
			     user_name = EXCLUDED.user_name,
			     updated_at = EXCLUDED.updated_at`,
			s.sessionsTable,
		),
		session.ID,
		session.Title,
		session.SystemPrompt,
		session.Tenant,
		session.User,
		session.CreatedAt.UTC(),
		session.UpdatedAt.UTC(),
	)
	if err != nil {
		return types.ChatSession{}, fmt.Errorf("write postgres chat session: %w", err)
	}
	return s.loadSession(ctx, session.ID)
}

func (s *PostgresStore) GetSession(ctx context.Context, id string) (types.ChatSession, bool, error) {
	session, err := s.loadSession(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return types.ChatSession{}, false, nil
		}
		return types.ChatSession{}, false, err
	}
	return session, true, nil
}

func (s *PostgresStore) ListSessions(ctx context.Context, filter Filter) ([]types.ChatSession, error) {
	query := fmt.Sprintf(`SELECT id, title, system_prompt, tenant, user_name, created_at, updated_at FROM %s`, s.sessionsTable)
	args := make([]any, 0, 2)
	if filter.Tenant != "" {
		query += ` WHERE tenant = $1`
		args = append(args, filter.Tenant)
	}
	query += ` ORDER BY updated_at DESC, created_at DESC`
	if filter.Limit > 0 {
		args = append(args, filter.Limit)
		query += fmt.Sprintf(` LIMIT $%d`, len(args))
	}
	if filter.Offset > 0 {
		args = append(args, filter.Offset)
		query += fmt.Sprintf(` OFFSET $%d`, len(args))
	}
	rows, err := s.client.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list postgres chat sessions: %w", err)
	}
	defer rows.Close()

	var items []types.ChatSession
	for rows.Next() {
		var session types.ChatSession
		if err := rows.Scan(&session.ID, &session.Title, &session.SystemPrompt, &session.Tenant, &session.User, &session.CreatedAt, &session.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan postgres chat session: %w", err)
		}
		items = append(items, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres chat sessions: %w", err)
	}
	if len(items) == 0 {
		return items, nil
	}
	// Pull the latest provider_call per session in one round-trip so
	// the list summary can render last-model / last-cost without
	// fetching every message body.
	if err := s.attachLatestCalls(ctx, items); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *PostgresStore) AppendExchange(ctx context.Context, sessionID string, messages []types.ChatSessionMessage, call types.ChatProviderCall) (types.ChatSession, error) {
	now := time.Now().UTC()
	if call.CreatedAt.IsZero() {
		call.CreatedAt = now
	}

	tx, err := s.client.DB().BeginTx(ctx, nil)
	if err != nil {
		return types.ChatSession{}, fmt.Errorf("begin postgres tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Determine the next sequence number under the same transaction so
	// concurrent appends serialize cleanly.
	var nextSeq sql.NullInt64
	if err := tx.QueryRowContext(
		ctx,
		fmt.Sprintf(`SELECT COALESCE(MAX(sequence), -1) + 1 FROM %s WHERE session_id = $1`, s.messagesTable),
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
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
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
		return types.ChatSession{}, fmt.Errorf("insert postgres provider call: %w", err)
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
				 VALUES ($1, $2, $3, $4, $5, $6)`,
				s.messagesTable,
			),
			messages[i].ID,
			sessionID,
			messages[i].Sequence,
			producedBy,
			string(messageJSON),
			messages[i].CreatedAt.UTC(),
		); err != nil {
			return types.ChatSession{}, fmt.Errorf("insert postgres chat message: %w", err)
		}
	}

	if _, err := tx.ExecContext(
		ctx,
		fmt.Sprintf(`UPDATE %s SET updated_at = $2 WHERE id = $1`, s.sessionsTable),
		sessionID,
		call.CreatedAt.UTC(),
	); err != nil {
		return types.ChatSession{}, fmt.Errorf("update postgres chat session timestamp: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return types.ChatSession{}, fmt.Errorf("commit postgres tx: %w", err)
	}
	return s.loadSession(ctx, sessionID)
}

func (s *PostgresStore) DeleteSession(ctx context.Context, id string) error {
	// Foreign keys with ON DELETE CASCADE handle the children; we
	// issue explicit DELETEs first as a belt-and-braces guard for
	// environments where the cascade might be disabled.
	if _, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(`DELETE FROM %s WHERE session_id = $1`, s.messagesTable),
		id,
	); err != nil {
		return fmt.Errorf("delete postgres chat messages: %w", err)
	}
	if _, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(`DELETE FROM %s WHERE session_id = $1`, s.providerCallsTable),
		id,
	); err != nil {
		return fmt.Errorf("delete postgres chat provider calls: %w", err)
	}
	if _, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(`DELETE FROM %s WHERE id = $1`, s.sessionsTable),
		id,
	); err != nil {
		return fmt.Errorf("delete postgres chat session: %w", err)
	}
	return nil
}

func (s *PostgresStore) UpdateSession(ctx context.Context, id string, title string) (types.ChatSession, error) {
	now := time.Now().UTC()
	if _, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(`UPDATE %s SET title = $1, updated_at = $2 WHERE id = $3`, s.sessionsTable),
		title, now, id,
	); err != nil {
		return types.ChatSession{}, fmt.Errorf("update postgres chat session: %w", err)
	}
	return s.loadSession(ctx, id)
}

func (s *PostgresStore) UpdateSessionSystemPrompt(ctx context.Context, id string, prompt string) (types.ChatSession, error) {
	now := time.Now().UTC()
	if _, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(`UPDATE %s SET system_prompt = $1, updated_at = $2 WHERE id = $3`, s.sessionsTable),
		prompt, now, id,
	); err != nil {
		return types.ChatSession{}, fmt.Errorf("update postgres chat session system prompt: %w", err)
	}
	return s.loadSession(ctx, id)
}

func (s *PostgresStore) migrate(ctx context.Context) error {
	if err := s.client.EnsureSchema(ctx); err != nil {
		return err
	}
	if _, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS %s (
				id TEXT PRIMARY KEY,
				title TEXT NOT NULL,
				system_prompt TEXT NOT NULL DEFAULT '',
				tenant TEXT NOT NULL,
				user_name TEXT NOT NULL,
				created_at TIMESTAMPTZ NOT NULL,
				updated_at TIMESTAMPTZ NOT NULL
			)`,
			s.sessionsTable,
		),
	); err != nil {
		return fmt.Errorf("migrate postgres chat sessions: %w", err)
	}
	// The legacy chat_session_turns table is dropped on upgrade — the
	// new schema (chat_session_messages + chat_session_provider_calls)
	// is incompatible row-shape, and we are intentionally not carrying
	// data forward. Operators upgrading lose stored turn rows; session
	// metadata (title, system_prompt) survives.
	if _, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(`DROP TABLE IF EXISTS %s`, s.client.QualifiedTable("chat_session_turns")),
	); err != nil {
		return fmt.Errorf("drop legacy postgres chat session turns: %w", err)
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
				cost_micros_usd BIGINT NOT NULL,
				prompt_tokens INTEGER NOT NULL,
				completion_tokens INTEGER NOT NULL,
				total_tokens INTEGER NOT NULL,
				created_at TIMESTAMPTZ NOT NULL
			)`,
			s.providerCallsTable,
			s.sessionsTable,
		),
	); err != nil {
		return fmt.Errorf("migrate postgres chat session provider calls: %w", err)
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
				created_at TIMESTAMPTZ NOT NULL,
				UNIQUE (session_id, sequence)
			)`,
			s.messagesTable,
			s.sessionsTable,
			s.providerCallsTable,
		),
	); err != nil {
		return fmt.Errorf("migrate postgres chat session messages: %w", err)
	}
	if _, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s (session_id, sequence)`, s.client.TableName("chat_session_messages_session_seq_idx"), s.messagesTable),
	); err != nil {
		return fmt.Errorf("migrate postgres chat session messages index: %w", err)
	}
	if _, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s (session_id, created_at)`, s.client.TableName("chat_session_provider_calls_session_created_idx"), s.providerCallsTable),
	); err != nil {
		return fmt.Errorf("migrate postgres chat session provider calls index: %w", err)
	}
	if _, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s (tenant, updated_at)`, s.client.TableName("chat_sessions_tenant_updated_idx"), s.sessionsTable),
	); err != nil {
		return fmt.Errorf("migrate postgres chat sessions tenant index: %w", err)
	}
	return nil
}

func (s *PostgresStore) loadSession(ctx context.Context, id string) (types.ChatSession, error) {
	var session types.ChatSession
	err := s.client.DB().QueryRowContext(
		ctx,
		fmt.Sprintf(`SELECT id, title, system_prompt, tenant, user_name, created_at, updated_at FROM %s WHERE id = $1`, s.sessionsTable),
		id,
	).Scan(&session.ID, &session.Title, &session.SystemPrompt, &session.Tenant, &session.User, &session.CreatedAt, &session.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return types.ChatSession{}, sql.ErrNoRows
		}
		return types.ChatSession{}, fmt.Errorf("read postgres chat session: %w", err)
	}

	calls, err := s.loadProviderCalls(ctx, s.client.DB(), id)
	if err != nil {
		return types.ChatSession{}, err
	}
	session.ProviderCalls = calls

	messages, err := s.loadMessages(ctx, s.client.DB(), id)
	if err != nil {
		return types.ChatSession{}, err
	}
	session.Messages = messages
	return session, nil
}

func (s *PostgresStore) loadProviderCalls(ctx context.Context, db *sql.DB, sessionID string) ([]types.ChatProviderCall, error) {
	rows, err := db.QueryContext(
		ctx,
		fmt.Sprintf(
			`SELECT id, request_id, requested_provider, provider, provider_kind,
			        requested_model, model, cost_micros_usd, prompt_tokens, completion_tokens, total_tokens, created_at
			 FROM %s
			 WHERE session_id = $1
			 ORDER BY created_at ASC, id ASC`,
			s.providerCallsTable,
		),
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("list postgres chat provider calls: %w", err)
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
			return nil, fmt.Errorf("scan postgres chat provider call: %w", err)
		}
		calls = append(calls, call)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres chat provider calls: %w", err)
	}
	return calls, nil
}

func (s *PostgresStore) loadMessages(ctx context.Context, db *sql.DB, sessionID string) ([]types.ChatSessionMessage, error) {
	rows, err := db.QueryContext(
		ctx,
		fmt.Sprintf(
			`SELECT id, sequence, produced_by_call_id, message_json, created_at
			 FROM %s
			 WHERE session_id = $1
			 ORDER BY sequence ASC`,
			s.messagesTable,
		),
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("list postgres chat messages: %w", err)
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
			return nil, fmt.Errorf("scan postgres chat message: %w", err)
		}
		if producedBy.Valid {
			msg.ProducedByCallID = producedBy.String
		}
		if err := json.Unmarshal([]byte(messageJSON), &msg.Message); err != nil {
			return nil, fmt.Errorf("decode postgres chat message body (id=%s): %w", msg.ID, err)
		}
		messages = append(messages, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres chat messages: %w", err)
	}
	return messages, nil
}

// attachLatestCalls populates the most-recent ProviderCall on each
// session in items, in one query rather than N. The list view uses it
// to render last_model / last_cost / last_request_id without fetching
// the message body. Sessions without any calls have no ProviderCalls.
func (s *PostgresStore) attachLatestCalls(ctx context.Context, items []types.ChatSession) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]any, 0, len(items))
	placeholders := make([]string, 0, len(items))
	for i, item := range items {
		ids = append(ids, item.ID)
		placeholders = append(placeholders, fmt.Sprintf("$%d", i+1))
	}
	query := fmt.Sprintf(
		`SELECT DISTINCT ON (session_id) session_id, id, request_id, requested_provider, provider, provider_kind,
		        requested_model, model, cost_micros_usd, prompt_tokens, completion_tokens, total_tokens, created_at
		 FROM %s
		 WHERE session_id IN (%s)
		 ORDER BY session_id, created_at DESC, id DESC`,
		s.providerCallsTable,
		joinPlaceholders(placeholders),
	)
	rows, err := s.client.DB().QueryContext(ctx, query, ids...)
	if err != nil {
		return fmt.Errorf("list postgres chat session latest calls: %w", err)
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
			return fmt.Errorf("scan postgres chat session latest call: %w", err)
		}
		latestBySession[sessionID] = call
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate postgres chat session latest calls: %w", err)
	}
	for i := range items {
		if call, ok := latestBySession[items[i].ID]; ok {
			items[i].ProviderCalls = []types.ChatProviderCall{call}
		}
	}
	return nil
}

func joinPlaceholders(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}

func cloneSession(session types.ChatSession) types.ChatSession {
	cloned := session
	cloned.Messages = append([]types.ChatSessionMessage(nil), session.Messages...)
	cloned.ProviderCalls = append([]types.ChatProviderCall(nil), session.ProviderCalls...)
	return cloned
}

func sortSessionsDesc(items []types.ChatSession) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].CreatedAt.After(items[j].CreatedAt)
		}
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
}
