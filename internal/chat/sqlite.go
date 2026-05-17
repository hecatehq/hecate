package chat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hecate/agent-runtime/internal/agentcontrols"
	"github.com/hecate/agent-runtime/internal/storage"
	"github.com/hecate/agent-runtime/pkg/types"
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
		sessionsTable: client.QualifiedTable("chat_sessions"),
		messagesTable: client.QualifiedTable("chat_messages"),
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
			`INSERT INTO %s (
				id, title, runtime_kind, adapter_id, driver_kind, native_session_id, workspace, workspace_branch,
				status, task_id, latest_run_id, provider, model, capabilities, config_options, rtk_enabled, turns_used, created_at, updated_at
			 )
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(id) DO UPDATE SET
			   title = excluded.title,
			   runtime_kind = excluded.runtime_kind,
			   adapter_id = excluded.adapter_id,
			   driver_kind = excluded.driver_kind,
			   native_session_id = excluded.native_session_id,
			   workspace = excluded.workspace,
			   workspace_branch = excluded.workspace_branch,
			   status = excluded.status,
			   task_id = excluded.task_id,
			   latest_run_id = excluded.latest_run_id,
			   provider = excluded.provider,
			   model = excluded.model,
			   capabilities = excluded.capabilities,
			   config_options = excluded.config_options,
			   rtk_enabled = excluded.rtk_enabled,
			   updated_at = excluded.updated_at`,
			s.sessionsTable,
		),
		session.ID,
		session.Title,
		normalizeRuntimeKind(session),
		session.AdapterID,
		session.DriverKind,
		session.NativeSessionID,
		session.Workspace,
		session.WorkspaceBranch,
		session.Status,
		session.TaskID,
		session.LatestRunID,
		session.Provider,
		session.Model,
		marshalModelCapabilities(session.Capabilities),
		marshalConfigOptions(session.ConfigOptions),
		session.RTKEnabled,
		session.TurnsUsed,
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
			`SELECT s.id, s.title, s.runtime_kind, s.adapter_id, s.driver_kind, s.native_session_id, s.workspace, s.workspace_branch,
			        s.status, s.task_id, s.latest_run_id, s.provider, s.model, s.capabilities, s.config_options, s.rtk_enabled, s.turns_used, s.created_at, s.updated_at,
			        COUNT(m.id) AS message_count
			 FROM %s AS s
			 LEFT JOIN %s AS m ON m.session_id = s.id
			 GROUP BY s.id, s.title, s.runtime_kind, s.adapter_id, s.driver_kind, s.native_session_id, s.workspace, s.workspace_branch,
			          s.status, s.task_id, s.latest_run_id, s.provider, s.model, s.capabilities, s.config_options, s.rtk_enabled, s.turns_used, s.created_at, s.updated_at
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
		var capabilities string
		var configOptions string
		if err := rows.Scan(
			&session.ID,
			&session.Title,
			&session.RuntimeKind,
			&session.AdapterID,
			&session.DriverKind,
			&session.NativeSessionID,
			&session.Workspace,
			&session.WorkspaceBranch,
			&session.Status,
			&session.TaskID,
			&session.LatestRunID,
			&session.Provider,
			&session.Model,
			&capabilities,
			&configOptions,
			&session.RTKEnabled,
			&session.TurnsUsed,
			&session.CreatedAt,
			&session.UpdatedAt,
			&messageCount,
		); err != nil {
			return nil, fmt.Errorf("scan sqlite agent chat session: %w", err)
		}
		session.Capabilities = unmarshalModelCapabilities(capabilities)
		session.ConfigOptions = unmarshalConfigOptions(configOptions)
		if session.RuntimeKind == "" {
			session.RuntimeKind = defaultRuntimeKind(session)
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
			   title = ?, runtime_kind = ?, adapter_id = ?, driver_kind = ?, native_session_id = ?, workspace = ?, workspace_branch = ?,
			   status = ?, task_id = ?, latest_run_id = ?, provider = ?, model = ?, capabilities = ?, config_options = ?, rtk_enabled = ?, turns_used = ?, updated_at = ?
			 WHERE id = ?`,
			s.sessionsTable,
		),
		session.Title,
		normalizeRuntimeKind(session),
		session.AdapterID,
		session.DriverKind,
		session.NativeSessionID,
		session.Workspace,
		session.WorkspaceBranch,
		session.Status,
		session.TaskID,
		session.LatestRunID,
		session.Provider,
		session.Model,
		marshalModelCapabilities(session.Capabilities),
		marshalConfigOptions(session.ConfigOptions),
		session.RTKEnabled,
		session.TurnsUsed,
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
	session, err := s.loadSession(ctx, sessionID)
	if err != nil {
		return Session{}, err
	}
	hydrateMessageRuntimeFromSession(&message, session)

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
				id, session_id, sequence, runtime_kind, segment_id, task_id, run_id, request_id, trace_id, span_id,
				role, content, raw_output, adapter_id, adapter_name, driver_kind, native_session_id, status, exit_code,
				cost_mode, provider, model, capabilities, workspace, diff_stat, diff, created_at, started_at, completed_at,
				error, activities, usage, timing
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			s.messagesTable,
		),
		message.ID,
		sessionID,
		nextSeq,
		message.RuntimeKind,
		message.SegmentID,
		message.TaskID,
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
		message.Provider,
		message.Model,
		marshalModelCapabilities(message.Capabilities),
		message.Workspace,
		message.DiffStat,
		message.Diff,
		message.CreatedAt.UTC(),
		nullableTime(message.StartedAt),
		nullableTime(message.CompletedAt),
		message.Error,
		marshalActivities(message.Activities),
		marshalUsage(message.Usage),
		marshalTiming(message.Timing),
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
			   runtime_kind = ?, segment_id = ?, task_id = ?, run_id = ?, request_id = ?, trace_id = ?, span_id = ?, role = ?, content = ?, raw_output = ?, adapter_id = ?, adapter_name = ?,
			   driver_kind = ?, native_session_id = ?, status = ?, exit_code = ?,
			   cost_mode = ?, provider = ?, model = ?, capabilities = ?, workspace = ?, diff_stat = ?, diff = ?, created_at = ?,
			   started_at = ?, completed_at = ?, error = ?, activities = ?, usage = ?, timing = ?
			 WHERE id = ? AND session_id = ?`,
			s.messagesTable,
		),
		message.RuntimeKind,
		message.SegmentID,
		message.TaskID,
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
		message.Provider,
		message.Model,
		marshalModelCapabilities(message.Capabilities),
		message.Workspace,
		message.DiffStat,
		message.Diff,
		message.CreatedAt.UTC(),
		nullableTime(message.StartedAt),
		nullableTime(message.CompletedAt),
		message.Error,
		marshalActivities(message.Activities),
		marshalUsage(message.Usage),
		marshalTiming(message.Timing),
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
				runtime_kind TEXT NOT NULL DEFAULT 'external_agent',
				adapter_id TEXT NOT NULL,
				driver_kind TEXT NOT NULL DEFAULT '',
				native_session_id TEXT NOT NULL DEFAULT '',
				workspace TEXT NOT NULL,
				workspace_branch TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL,
				task_id TEXT NOT NULL DEFAULT '',
				latest_run_id TEXT NOT NULL DEFAULT '',
				provider TEXT NOT NULL DEFAULT '',
				model TEXT NOT NULL DEFAULT '',
				capabilities TEXT NOT NULL DEFAULT '{}',
				config_options TEXT NOT NULL DEFAULT '[]',
				rtk_enabled INTEGER NOT NULL DEFAULT 0,
				turns_used INTEGER NOT NULL DEFAULT 0,
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
				runtime_kind TEXT NOT NULL DEFAULT '',
				segment_id TEXT NOT NULL DEFAULT '',
				task_id TEXT NOT NULL DEFAULT '',
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
				provider TEXT NOT NULL DEFAULT '',
				model TEXT NOT NULL DEFAULT '',
				capabilities TEXT NOT NULL DEFAULT '{}',
				workspace TEXT NOT NULL,
				diff_stat TEXT NOT NULL,
				diff TEXT NOT NULL,
				created_at TIMESTAMP NOT NULL,
				started_at TIMESTAMP,
				completed_at TIMESTAMP,
				error TEXT NOT NULL DEFAULT '',
				activities TEXT NOT NULL DEFAULT '[]',
				usage TEXT NOT NULL DEFAULT '{}',
				timing TEXT NOT NULL DEFAULT '{}',
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
	if err := s.ensureSessionColumn(ctx, "turns_used", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	for _, column := range []struct {
		name       string
		definition string
	}{
		{name: "runtime_kind", definition: "TEXT NOT NULL DEFAULT 'external_agent'"},
		{name: "task_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "latest_run_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "provider", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "model", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "capabilities", definition: "TEXT NOT NULL DEFAULT '{}'"},
		{name: "config_options", definition: "TEXT NOT NULL DEFAULT '[]'"},
		{name: "rtk_enabled", definition: "INTEGER NOT NULL DEFAULT 0"},
	} {
		if err := s.ensureSessionColumn(ctx, column.name, column.definition); err != nil {
			return err
		}
	}
	for _, column := range []struct {
		name       string
		definition string
	}{
		{name: "run_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "runtime_kind", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "segment_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "task_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "request_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "trace_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "span_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "driver_kind", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "native_session_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "started_at", definition: "TIMESTAMP"},
		{name: "completed_at", definition: "TIMESTAMP"},
		{name: "error", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "raw_output", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "provider", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "model", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "capabilities", definition: "TEXT NOT NULL DEFAULT '{}'"},
		{name: "activities", definition: "TEXT NOT NULL DEFAULT '[]'"},
		{name: "usage", definition: "TEXT NOT NULL DEFAULT '{}'"},
		{name: "timing", definition: "TEXT NOT NULL DEFAULT '{}'"},
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
	var capabilities string
	var configOptions string
	err := s.client.DB().QueryRowContext(
		ctx,
		fmt.Sprintf(
			`SELECT id, title, runtime_kind, adapter_id, driver_kind, native_session_id, workspace, workspace_branch,
			        status, task_id, latest_run_id, provider, model, capabilities, config_options, rtk_enabled, turns_used, created_at, updated_at
			 FROM %s WHERE id = ?`,
			s.sessionsTable,
		),
		id,
	).Scan(
		&session.ID,
		&session.Title,
		&session.RuntimeKind,
		&session.AdapterID,
		&session.DriverKind,
		&session.NativeSessionID,
		&session.Workspace,
		&session.WorkspaceBranch,
		&session.Status,
		&session.TaskID,
		&session.LatestRunID,
		&session.Provider,
		&session.Model,
		&capabilities,
		&configOptions,
		&session.RTKEnabled,
		&session.TurnsUsed,
		&session.CreatedAt,
		&session.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, sql.ErrNoRows
		}
		return Session{}, fmt.Errorf("read sqlite agent chat session: %w", err)
	}
	if session.RuntimeKind == "" {
		session.RuntimeKind = defaultRuntimeKind(session)
	}
	session.Capabilities = unmarshalModelCapabilities(capabilities)
	session.ConfigOptions = unmarshalConfigOptions(configOptions)
	messages, err := s.loadMessages(ctx, id)
	if err != nil {
		return Session{}, err
	}
	session.Messages = messages
	for i := range session.Messages {
		hydrateMessageRuntimeFromSession(&session.Messages[i], session)
	}
	return session, nil
}

func (s *SQLiteStore) loadMessages(ctx context.Context, sessionID string) ([]Message, error) {
	rows, err := s.client.DB().QueryContext(
		ctx,
		fmt.Sprintf(
			`SELECT id, runtime_kind, segment_id, task_id, run_id, request_id, trace_id, span_id, role, content, raw_output, adapter_id, adapter_name, driver_kind, native_session_id, status, exit_code, cost_mode,
			        provider, model, capabilities, workspace, diff_stat, diff, created_at, started_at, completed_at, error, activities, usage, timing
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
		var usage string
		var timing string
		var capabilities string
		if err := rows.Scan(
			&message.ID,
			&message.RuntimeKind,
			&message.SegmentID,
			&message.TaskID,
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
			&message.Provider,
			&message.Model,
			&capabilities,
			&message.Workspace,
			&message.DiffStat,
			&message.Diff,
			&message.CreatedAt,
			&startedAt,
			&completedAt,
			&message.Error,
			&activities,
			&usage,
			&timing,
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
		message.Usage = unmarshalUsage(usage)
		message.Timing = unmarshalTiming(timing)
		message.Capabilities = unmarshalModelCapabilities(capabilities)
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
	var usage string
	var timing string
	var capabilities string
	err := tx.QueryRowContext(
		ctx,
		fmt.Sprintf(
			`SELECT id, runtime_kind, segment_id, task_id, run_id, request_id, trace_id, span_id, role, content, raw_output, adapter_id, adapter_name, driver_kind, native_session_id, status, exit_code, cost_mode,
			        provider, model, capabilities, workspace, diff_stat, diff, created_at, started_at, completed_at, error, activities, usage, timing
			 FROM %s
			 WHERE id = ? AND session_id = ?`,
			table,
		),
		messageID,
		sessionID,
	).Scan(
		&message.ID,
		&message.RuntimeKind,
		&message.SegmentID,
		&message.TaskID,
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
		&message.Provider,
		&message.Model,
		&capabilities,
		&message.Workspace,
		&message.DiffStat,
		&message.Diff,
		&message.CreatedAt,
		&startedAt,
		&completedAt,
		&message.Error,
		&activities,
		&usage,
		&timing,
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
	message.Usage = unmarshalUsage(usage)
	message.Timing = unmarshalTiming(timing)
	message.Capabilities = unmarshalModelCapabilities(capabilities)
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

func marshalUsage(usage Usage) string {
	if usage.Empty() {
		return "{}"
	}
	data, err := json.Marshal(usage)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func unmarshalUsage(raw string) Usage {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Usage{}
	}
	var usage Usage
	if err := json.Unmarshal([]byte(raw), &usage); err != nil {
		return Usage{}
	}
	return usage
}

func marshalTiming(timing Timing) string {
	if timing.Empty() {
		return "{}"
	}
	data, err := json.Marshal(timing)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func unmarshalTiming(raw string) Timing {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Timing{}
	}
	var timing Timing
	if err := json.Unmarshal([]byte(raw), &timing); err != nil {
		return Timing{}
	}
	return timing
}

func normalizeRuntimeKind(session Session) string {
	if session.RuntimeKind != "" {
		return session.RuntimeKind
	}
	return defaultRuntimeKind(session)
}

func marshalModelCapabilities(capabilities types.ModelCapabilities) string {
	if capabilities.ToolCalling == "" && !capabilities.Streaming && capabilities.MaxContextTokens == 0 && capabilities.Source == "" {
		return "{}"
	}
	data, err := json.Marshal(capabilities)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func unmarshalModelCapabilities(raw string) types.ModelCapabilities {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return types.ModelCapabilities{}
	}
	var capabilities types.ModelCapabilities
	if err := json.Unmarshal([]byte(raw), &capabilities); err != nil {
		return types.ModelCapabilities{}
	}
	return capabilities
}

func marshalConfigOptions(options []agentcontrols.ConfigOption) string {
	if len(options) == 0 {
		return "[]"
	}
	data, err := json.Marshal(options)
	if err != nil {
		return "[]"
	}
	return string(data)
}

func unmarshalConfigOptions(raw string) []agentcontrols.ConfigOption {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var options []agentcontrols.ConfigOption
	if err := json.Unmarshal([]byte(raw), &options); err != nil {
		return nil
	}
	return options
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
