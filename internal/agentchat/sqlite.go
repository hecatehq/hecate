package agentchat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hecate/agent-runtime/internal/storage"
)

type SQLiteStore struct {
	client        *storage.SQLiteClient
	sessionsTable string
	messagesTable string
}

func NewSQLiteStore(ctx context.Context, client *storage.SQLiteClient) (*SQLiteStore, error) {
	if client == nil || client.DB() == nil {
		return nil, fmt.Errorf("sqlite client is required")
	}
	store := &SQLiteStore{
		client:        client,
		sessionsTable: client.QualifiedTable("agent_chat_sessions"),
		messagesTable: client.QualifiedTable("agent_chat_messages"),
	}
	if err := store.migrate(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *SQLiteStore) Backend() string {
	return "sqlite"
}

func (s *SQLiteStore) Create(ctx context.Context, session Session) (Session, error) {
	if session.ID == "" {
		return Session{}, fmt.Errorf("session id is required")
	}
	now := time.Now().UTC()
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	}
	session.UpdatedAt = session.CreatedAt
	if session.Status == "" {
		session.Status = "idle"
	}

	_, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(
			`INSERT INTO %s (id, title, adapter_id, workspace, status, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(id) DO UPDATE SET
			   title = excluded.title,
			   adapter_id = excluded.adapter_id,
			   workspace = excluded.workspace,
			   status = excluded.status,
			   updated_at = excluded.updated_at`,
			s.sessionsTable,
		),
		session.ID,
		session.Title,
		session.AdapterID,
		session.Workspace,
		session.Status,
		session.CreatedAt.UTC(),
		session.UpdatedAt.UTC(),
	)
	if err != nil {
		return Session{}, fmt.Errorf("write sqlite agent chat session: %w", err)
	}
	return s.loadSession(ctx, session.ID)
}

func (s *SQLiteStore) Get(ctx context.Context, id string) (Session, bool, error) {
	session, err := s.loadSession(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, false, nil
		}
		return Session{}, false, err
	}
	return session, true, nil
}

func (s *SQLiteStore) List(ctx context.Context) ([]Session, error) {
	rows, err := s.client.DB().QueryContext(
		ctx,
		fmt.Sprintf(
			`SELECT id, title, adapter_id, workspace, status, created_at, updated_at
			 FROM %s
			 ORDER BY updated_at DESC, created_at DESC`,
			s.sessionsTable,
		),
	)
	if err != nil {
		return nil, fmt.Errorf("list sqlite agent chat sessions: %w", err)
	}
	defer rows.Close()

	var items []Session
	for rows.Next() {
		var session Session
		if err := rows.Scan(
			&session.ID,
			&session.Title,
			&session.AdapterID,
			&session.Workspace,
			&session.Status,
			&session.CreatedAt,
			&session.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan sqlite agent chat session: %w", err)
		}
		if count, err := s.messageCount(ctx, session.ID); err == nil && count > 0 {
			session.Messages = make([]Message, count)
		}
		items = append(items, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sqlite agent chat sessions: %w", err)
	}
	return items, nil
}

func (s *SQLiteStore) Delete(ctx context.Context, id string) error {
	if _, err := s.client.DB().ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE session_id = ?`, s.messagesTable), id); err != nil {
		return fmt.Errorf("delete sqlite agent chat messages: %w", err)
	}
	if _, err := s.client.DB().ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id = ?`, s.sessionsTable), id); err != nil {
		return fmt.Errorf("delete sqlite agent chat session: %w", err)
	}
	return nil
}

func (s *SQLiteStore) AppendMessage(ctx context.Context, sessionID string, message Message) (Session, error) {
	if message.ID == "" {
		return Session{}, fmt.Errorf("message id is required")
	}
	now := time.Now().UTC()
	if message.CreatedAt.IsZero() {
		message.CreatedAt = now
	}

	tx, err := s.client.DB().BeginTx(ctx, nil)
	if err != nil {
		return Session{}, fmt.Errorf("begin sqlite agent chat tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var nextSeq int
	if err := tx.QueryRowContext(
		ctx,
		fmt.Sprintf(`SELECT COALESCE(MAX(sequence), -1) + 1 FROM %s WHERE session_id = ?`, s.messagesTable),
		sessionID,
	).Scan(&nextSeq); err != nil {
		return Session{}, fmt.Errorf("read sqlite agent chat next sequence: %w", err)
	}

	if _, err := tx.ExecContext(
		ctx,
		fmt.Sprintf(
			`INSERT INTO %s (
				id, session_id, sequence, role, content, adapter_id, adapter_name, status, exit_code,
				cost_mode, workspace, diff_stat, diff, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			s.messagesTable,
		),
		message.ID,
		sessionID,
		nextSeq,
		message.Role,
		message.Content,
		message.AdapterID,
		message.AdapterName,
		message.Status,
		message.ExitCode,
		message.CostMode,
		message.Workspace,
		message.DiffStat,
		message.Diff,
		message.CreatedAt.UTC(),
	); err != nil {
		return Session{}, fmt.Errorf("insert sqlite agent chat message: %w", err)
	}

	if err := updateSessionAfterMessage(ctx, tx, s.sessionsTable, sessionID, message); err != nil {
		return Session{}, err
	}
	if err := tx.Commit(); err != nil {
		return Session{}, fmt.Errorf("commit sqlite agent chat tx: %w", err)
	}
	return s.loadSession(ctx, sessionID)
}

func (s *SQLiteStore) UpdateMessage(ctx context.Context, sessionID string, messageID string, update func(*Message)) (Session, error) {
	tx, err := s.client.DB().BeginTx(ctx, nil)
	if err != nil {
		return Session{}, fmt.Errorf("begin sqlite agent chat tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	message, err := loadMessage(ctx, tx, s.messagesTable, sessionID, messageID)
	if err != nil {
		return Session{}, err
	}
	update(&message)
	if _, err := tx.ExecContext(
		ctx,
		fmt.Sprintf(
			`UPDATE %s SET
			   role = ?, content = ?, adapter_id = ?, adapter_name = ?, status = ?, exit_code = ?,
			   cost_mode = ?, workspace = ?, diff_stat = ?, diff = ?, created_at = ?
			 WHERE id = ? AND session_id = ?`,
			s.messagesTable,
		),
		message.Role,
		message.Content,
		message.AdapterID,
		message.AdapterName,
		message.Status,
		message.ExitCode,
		message.CostMode,
		message.Workspace,
		message.DiffStat,
		message.Diff,
		message.CreatedAt.UTC(),
		messageID,
		sessionID,
	); err != nil {
		return Session{}, fmt.Errorf("update sqlite agent chat message: %w", err)
	}
	if err := updateSessionAfterMessage(ctx, tx, s.sessionsTable, sessionID, message); err != nil {
		return Session{}, err
	}
	if err := tx.Commit(); err != nil {
		return Session{}, fmt.Errorf("commit sqlite agent chat tx: %w", err)
	}
	return s.loadSession(ctx, sessionID)
}

func (s *SQLiteStore) migrate(ctx context.Context) error {
	if _, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS %s (
				id TEXT PRIMARY KEY,
				title TEXT NOT NULL,
				adapter_id TEXT NOT NULL,
				workspace TEXT NOT NULL,
				status TEXT NOT NULL,
				created_at TIMESTAMP NOT NULL,
				updated_at TIMESTAMP NOT NULL
			)`,
			s.sessionsTable,
		),
	); err != nil {
		return fmt.Errorf("migrate sqlite agent chat sessions: %w", err)
	}
	if _, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS %s (
				id TEXT PRIMARY KEY,
				session_id TEXT NOT NULL REFERENCES %s (id) ON DELETE CASCADE,
				sequence INTEGER NOT NULL,
				role TEXT NOT NULL,
				content TEXT NOT NULL,
				adapter_id TEXT NOT NULL,
				adapter_name TEXT NOT NULL,
				status TEXT NOT NULL,
				exit_code INTEGER NOT NULL,
				cost_mode TEXT NOT NULL,
				workspace TEXT NOT NULL,
				diff_stat TEXT NOT NULL,
				diff TEXT NOT NULL,
				created_at TIMESTAMP NOT NULL,
				UNIQUE (session_id, sequence)
			)`,
			s.messagesTable,
			s.sessionsTable,
		),
	); err != nil {
		return fmt.Errorf("migrate sqlite agent chat messages: %w", err)
	}

	messagesIndex := strings.Trim(s.messagesTable, `"`) + "_session_seq_idx"
	if _, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "%s" ON %s (session_id, sequence)`, messagesIndex, s.messagesTable),
	); err != nil {
		return fmt.Errorf("migrate sqlite agent chat messages index: %w", err)
	}
	sessionsIndex := strings.Trim(s.sessionsTable, `"`) + "_updated_idx"
	if _, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "%s" ON %s (updated_at)`, sessionsIndex, s.sessionsTable),
	); err != nil {
		return fmt.Errorf("migrate sqlite agent chat sessions index: %w", err)
	}
	return nil
}

func (s *SQLiteStore) loadSession(ctx context.Context, id string) (Session, error) {
	var session Session
	err := s.client.DB().QueryRowContext(
		ctx,
		fmt.Sprintf(`SELECT id, title, adapter_id, workspace, status, created_at, updated_at FROM %s WHERE id = ?`, s.sessionsTable),
		id,
	).Scan(&session.ID, &session.Title, &session.AdapterID, &session.Workspace, &session.Status, &session.CreatedAt, &session.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, sql.ErrNoRows
		}
		return Session{}, fmt.Errorf("read sqlite agent chat session: %w", err)
	}
	messages, err := s.loadMessages(ctx, id)
	if err != nil {
		return Session{}, err
	}
	session.Messages = messages
	return session, nil
}

func (s *SQLiteStore) loadMessages(ctx context.Context, sessionID string) ([]Message, error) {
	rows, err := s.client.DB().QueryContext(
		ctx,
		fmt.Sprintf(
			`SELECT id, role, content, adapter_id, adapter_name, status, exit_code, cost_mode,
			        workspace, diff_stat, diff, created_at
			 FROM %s
			 WHERE session_id = ?
			 ORDER BY sequence ASC`,
			s.messagesTable,
		),
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("list sqlite agent chat messages: %w", err)
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var message Message
		if err := rows.Scan(
			&message.ID,
			&message.Role,
			&message.Content,
			&message.AdapterID,
			&message.AdapterName,
			&message.Status,
			&message.ExitCode,
			&message.CostMode,
			&message.Workspace,
			&message.DiffStat,
			&message.Diff,
			&message.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan sqlite agent chat message: %w", err)
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sqlite agent chat messages: %w", err)
	}
	return messages, nil
}

func (s *SQLiteStore) messageCount(ctx context.Context, sessionID string) (int, error) {
	var count int
	if err := s.client.DB().QueryRowContext(
		ctx,
		fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE session_id = ?`, s.messagesTable),
		sessionID,
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("count sqlite agent chat messages: %w", err)
	}
	return count, nil
}

type txRunner interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func loadMessage(ctx context.Context, tx txRunner, table string, sessionID string, messageID string) (Message, error) {
	var message Message
	err := tx.QueryRowContext(
		ctx,
		fmt.Sprintf(
			`SELECT id, role, content, adapter_id, adapter_name, status, exit_code, cost_mode,
			        workspace, diff_stat, diff, created_at
			 FROM %s
			 WHERE id = ? AND session_id = ?`,
			table,
		),
		messageID,
		sessionID,
	).Scan(
		&message.ID,
		&message.Role,
		&message.Content,
		&message.AdapterID,
		&message.AdapterName,
		&message.Status,
		&message.ExitCode,
		&message.CostMode,
		&message.Workspace,
		&message.DiffStat,
		&message.Diff,
		&message.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Message{}, fmt.Errorf("agent chat message %q not found", messageID)
		}
		return Message{}, fmt.Errorf("read sqlite agent chat message: %w", err)
	}
	return message, nil
}

func updateSessionAfterMessage(ctx context.Context, tx txRunner, table string, sessionID string, message Message) error {
	status := ""
	if message.Role == "assistant" {
		status = message.Status
	}
	if status != "" {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET status = ?, updated_at = ? WHERE id = ?`, table), status, time.Now().UTC(), sessionID); err != nil {
			return fmt.Errorf("update sqlite agent chat session status: %w", err)
		}
		return nil
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET updated_at = ? WHERE id = ?`, table), time.Now().UTC(), sessionID); err != nil {
		return fmt.Errorf("update sqlite agent chat session timestamp: %w", err)
	}
	return nil
}
