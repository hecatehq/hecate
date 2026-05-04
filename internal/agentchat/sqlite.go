package agentchat

import (
	"context"
	"database/sql"
	"encoding/json"
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
			`INSERT INTO %s (id, title, adapter_id, driver_kind, native_session_id, workspace, workspace_branch, status, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(id) DO UPDATE SET
			   title = excluded.title,
			   adapter_id = excluded.adapter_id,
			   driver_kind = excluded.driver_kind,
			   native_session_id = excluded.native_session_id,
			   workspace = excluded.workspace,
			   workspace_branch = excluded.workspace_branch,
			   status = excluded.status,
			   updated_at = excluded.updated_at`,
			s.sessionsTable,
		),
		session.ID,
		session.Title,
		session.AdapterID,
		session.DriverKind,
		session.NativeSessionID,
		session.Workspace,
		session.WorkspaceBranch,
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
			`SELECT s.id, s.title, s.adapter_id, s.driver_kind, s.native_session_id, s.workspace, s.workspace_branch, s.status, s.created_at, s.updated_at,
			        COUNT(m.id) AS message_count
			 FROM %s AS s
			 LEFT JOIN %s AS m ON m.session_id = s.id
			 GROUP BY s.id, s.title, s.adapter_id, s.driver_kind, s.native_session_id, s.workspace, s.workspace_branch, s.status, s.created_at, s.updated_at
			 ORDER BY s.updated_at DESC, s.created_at DESC`,
			s.sessionsTable,
			s.messagesTable,
		),
	)
	if err != nil {
		return nil, fmt.Errorf("list sqlite agent chat sessions: %w", err)
	}
	defer rows.Close()

	var items []Session
	for rows.Next() {
		var session Session
		var messageCount int
		if err := rows.Scan(
			&session.ID,
			&session.Title,
			&session.AdapterID,
			&session.DriverKind,
			&session.NativeSessionID,
			&session.Workspace,
			&session.WorkspaceBranch,
			&session.Status,
			&session.CreatedAt,
			&session.UpdatedAt,
			&messageCount,
		); err != nil {
			return nil, fmt.Errorf("scan sqlite agent chat session: %w", err)
		}
		if messageCount > 0 {
			session.Messages = make([]Message, messageCount)
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

func (s *SQLiteStore) UpdateSession(ctx context.Context, id string, update func(*Session)) (Session, error) {
	session, err := s.loadSession(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, fmt.Errorf("agent chat session %q not found", id)
		}
		return Session{}, err
	}
	update(&session)
	session.UpdatedAt = time.Now().UTC()
	if _, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(
			`UPDATE %s SET
			   title = ?, adapter_id = ?, driver_kind = ?, native_session_id = ?, workspace = ?, workspace_branch = ?,
			   status = ?, updated_at = ?
			 WHERE id = ?`,
			s.sessionsTable,
		),
		session.Title,
		session.AdapterID,
		session.DriverKind,
		session.NativeSessionID,
		session.Workspace,
		session.WorkspaceBranch,
		session.Status,
		session.UpdatedAt.UTC(),
		id,
	); err != nil {
		return Session{}, fmt.Errorf("update sqlite agent chat session: %w", err)
	}
	return s.loadSession(ctx, id)
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
				id, session_id, sequence, run_id, request_id, trace_id, span_id, role, content, raw_output, adapter_id, adapter_name, driver_kind, native_session_id, status, exit_code,
				cost_mode, workspace, diff_stat, diff, created_at, started_at, completed_at, error, activities
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			s.messagesTable,
		),
		message.ID,
		sessionID,
		nextSeq,
		message.RunID,
		message.RequestID,
		message.TraceID,
		message.SpanID,
		message.Role,
		message.Content,
		message.RawOutput,
		message.AdapterID,
		message.AdapterName,
		message.DriverKind,
		message.NativeSessionID,
		message.Status,
		message.ExitCode,
		message.CostMode,
		message.Workspace,
		message.DiffStat,
		message.Diff,
		message.CreatedAt.UTC(),
		nullableTime(message.StartedAt),
		nullableTime(message.CompletedAt),
		message.Error,
		marshalActivities(message.Activities),
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
			   run_id = ?, request_id = ?, trace_id = ?, span_id = ?, role = ?, content = ?, raw_output = ?, adapter_id = ?, adapter_name = ?,
			   driver_kind = ?, native_session_id = ?, status = ?, exit_code = ?,
			   cost_mode = ?, workspace = ?, diff_stat = ?, diff = ?, created_at = ?,
			   started_at = ?, completed_at = ?, error = ?, activities = ?
			 WHERE id = ? AND session_id = ?`,
			s.messagesTable,
		),
		message.RunID,
		message.RequestID,
		message.TraceID,
		message.SpanID,
		message.Role,
		message.Content,
		message.RawOutput,
		message.AdapterID,
		message.AdapterName,
		message.DriverKind,
		message.NativeSessionID,
		message.Status,
		message.ExitCode,
		message.CostMode,
		message.Workspace,
		message.DiffStat,
		message.Diff,
		message.CreatedAt.UTC(),
		nullableTime(message.StartedAt),
		nullableTime(message.CompletedAt),
		message.Error,
		marshalActivities(message.Activities),
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
				driver_kind TEXT NOT NULL DEFAULT '',
				native_session_id TEXT NOT NULL DEFAULT '',
				workspace TEXT NOT NULL,
				workspace_branch TEXT NOT NULL DEFAULT '',
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
				raw_output TEXT NOT NULL DEFAULT '',
				adapter_id TEXT NOT NULL,
				adapter_name TEXT NOT NULL,
				driver_kind TEXT NOT NULL DEFAULT '',
				native_session_id TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL,
				exit_code INTEGER NOT NULL,
				run_id TEXT NOT NULL DEFAULT '',
				request_id TEXT NOT NULL DEFAULT '',
				trace_id TEXT NOT NULL DEFAULT '',
				span_id TEXT NOT NULL DEFAULT '',
				cost_mode TEXT NOT NULL,
				workspace TEXT NOT NULL,
				diff_stat TEXT NOT NULL,
				diff TEXT NOT NULL,
				created_at TIMESTAMP NOT NULL,
				started_at TIMESTAMP,
				completed_at TIMESTAMP,
				error TEXT NOT NULL DEFAULT '',
				activities TEXT NOT NULL DEFAULT '[]',
				UNIQUE (session_id, sequence)
			)`,
			s.messagesTable,
			s.sessionsTable,
		),
	); err != nil {
		return fmt.Errorf("migrate sqlite agent chat messages: %w", err)
	}
	if err := s.ensureSessionColumn(ctx, "workspace_branch", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureSessionColumn(ctx, "driver_kind", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureSessionColumn(ctx, "native_session_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	for _, column := range []struct {
		name       string
		definition string
	}{
		{name: "run_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "request_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "trace_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "span_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "driver_kind", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "native_session_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "started_at", definition: "TIMESTAMP"},
		{name: "completed_at", definition: "TIMESTAMP"},
		{name: "error", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "raw_output", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "activities", definition: "TEXT NOT NULL DEFAULT '[]'"},
	} {
		if err := s.ensureMessageColumn(ctx, column.name, column.definition); err != nil {
			return err
		}
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
		fmt.Sprintf(`SELECT id, title, adapter_id, driver_kind, native_session_id, workspace, workspace_branch, status, created_at, updated_at FROM %s WHERE id = ?`, s.sessionsTable),
		id,
	).Scan(&session.ID, &session.Title, &session.AdapterID, &session.DriverKind, &session.NativeSessionID, &session.Workspace, &session.WorkspaceBranch, &session.Status, &session.CreatedAt, &session.UpdatedAt)
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
			`SELECT id, run_id, request_id, trace_id, span_id, role, content, raw_output, adapter_id, adapter_name, driver_kind, native_session_id, status, exit_code, cost_mode,
			        workspace, diff_stat, diff, created_at, started_at, completed_at, error, activities
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
		var startedAt, completedAt sql.NullTime
		var activities string
		if err := rows.Scan(
			&message.ID,
			&message.RunID,
			&message.RequestID,
			&message.TraceID,
			&message.SpanID,
			&message.Role,
			&message.Content,
			&message.RawOutput,
			&message.AdapterID,
			&message.AdapterName,
			&message.DriverKind,
			&message.NativeSessionID,
			&message.Status,
			&message.ExitCode,
			&message.CostMode,
			&message.Workspace,
			&message.DiffStat,
			&message.Diff,
			&message.CreatedAt,
			&startedAt,
			&completedAt,
			&message.Error,
			&activities,
		); err != nil {
			return nil, fmt.Errorf("scan sqlite agent chat message: %w", err)
		}
		if startedAt.Valid {
			message.StartedAt = startedAt.Time
		}
		if completedAt.Valid {
			message.CompletedAt = completedAt.Time
		}
		message.Activities = unmarshalActivities(activities)
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sqlite agent chat messages: %w", err)
	}
	return messages, nil
}

func (s *SQLiteStore) ensureMessageColumn(ctx context.Context, column, definition string) error {
	exists, err := s.columnExists(ctx, s.messagesTable, column)
	if err != nil {
		return fmt.Errorf("inspect sqlite agent chat messages columns: %w", err)
	}
	if exists {
		return nil
	}
	if _, err := s.client.DB().ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, s.messagesTable, column, definition)); err != nil {
		return fmt.Errorf("migrate sqlite agent chat messages %s: %w", column, err)
	}
	return nil
}

func (s *SQLiteStore) ensureSessionColumn(ctx context.Context, column, definition string) error {
	exists, err := s.columnExists(ctx, s.sessionsTable, column)
	if err != nil {
		return fmt.Errorf("inspect sqlite agent chat sessions columns: %w", err)
	}
	if exists {
		return nil
	}
	if _, err := s.client.DB().ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, s.sessionsTable, column, definition)); err != nil {
		return fmt.Errorf("migrate sqlite agent chat sessions %s: %w", column, err)
	}
	return nil
}

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

type txRunner interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func loadMessage(ctx context.Context, tx txRunner, table string, sessionID string, messageID string) (Message, error) {
	var message Message
	var startedAt, completedAt sql.NullTime
	var activities string
	err := tx.QueryRowContext(
		ctx,
		fmt.Sprintf(
			`SELECT id, run_id, request_id, trace_id, span_id, role, content, raw_output, adapter_id, adapter_name, driver_kind, native_session_id, status, exit_code, cost_mode,
			        workspace, diff_stat, diff, created_at, started_at, completed_at, error, activities
			 FROM %s
			 WHERE id = ? AND session_id = ?`,
			table,
		),
		messageID,
		sessionID,
	).Scan(
		&message.ID,
		&message.RunID,
		&message.RequestID,
		&message.TraceID,
		&message.SpanID,
		&message.Role,
		&message.Content,
		&message.RawOutput,
		&message.AdapterID,
		&message.AdapterName,
		&message.DriverKind,
		&message.NativeSessionID,
		&message.Status,
		&message.ExitCode,
		&message.CostMode,
		&message.Workspace,
		&message.DiffStat,
		&message.Diff,
		&message.CreatedAt,
		&startedAt,
		&completedAt,
		&message.Error,
		&activities,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Message{}, fmt.Errorf("agent chat message %q not found", messageID)
		}
		return Message{}, fmt.Errorf("read sqlite agent chat message: %w", err)
	}
	if startedAt.Valid {
		message.StartedAt = startedAt.Time
	}
	if completedAt.Valid {
		message.CompletedAt = completedAt.Time
	}
	message.Activities = unmarshalActivities(activities)
	return message, nil
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC()
}

func marshalActivities(items []Activity) string {
	if len(items) == 0 {
		return "[]"
	}
	data, err := json.Marshal(items)
	if err != nil {
		return "[]"
	}
	return string(data)
}

func unmarshalActivities(raw string) []Activity {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var items []Activity
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil
	}
	return items
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
