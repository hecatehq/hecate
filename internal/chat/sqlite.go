package chat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hecatehq/hecate/internal/agentcontrols"
	"github.com/hecatehq/hecate/internal/storage"
	"github.com/hecatehq/hecate/pkg/types"
)

type SQLiteStore struct {
	client                 storage.SQLClient
	backend                string
	sessionsTable          string
	messagesTable          string
	messageRequestsTable   string
	messageRequestInstance string
	messageRequestNow      func() time.Time
	messageUpdateMu        sync.Mutex
}

func NewSQLiteStore(ctx context.Context, client *storage.SQLiteClient) (*SQLiteStore, error) {
	return newSQLStore(ctx, client)
}

func NewPostgresStore(ctx context.Context, client *storage.PostgresClient) (*SQLiteStore, error) {
	return newSQLStore(ctx, client)
}

func newSQLStore(ctx context.Context, client storage.SQLClient) (*SQLiteStore, error) {
	if client == nil || client.DB() == nil {
		return nil, fmt.Errorf("sql client is required")
	}
	instanceID, err := newMessageRequestToken()
	if err != nil {
		return nil, err
	}
	store := &SQLiteStore{
		client:                 client,
		backend:                client.Backend(),
		sessionsTable:          client.QualifiedTable("chat_sessions"),
		messagesTable:          client.QualifiedTable("chat_messages"),
		messageRequestsTable:   client.QualifiedTable("chat_message_requests"),
		messageRequestInstance: instanceID,
		messageRequestNow:      time.Now,
	}
	if err := store.migrate(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *SQLiteStore) Backend() string {
	return s.backend
}

func (s *SQLiteStore) MessageRequestLeaseTTL() time.Duration {
	return MessageRequestLeaseStaleAfter
}

func (s *SQLiteStore) messageRequestNowUTC() time.Time {
	if s.messageRequestNow == nil {
		return time.Now().UTC()
	}
	return s.messageRequestNow().UTC()
}

func (s *SQLiteStore) setMessageRequestNow(now func() time.Time) {
	s.messageRequestNow = now
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
				id, title, project_id, agent_id, driver_kind, native_session_id, agent_info, workspace, workspace_mode, workspace_branch,
				status, task_id, latest_run_id, provider, model, capabilities, config_options, available_commands, available_commands_authoritative, mcp_servers, rtk_enabled, turns_used, context_summary, created_at, updated_at
			 )
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(id) DO UPDATE SET
			   title = excluded.title,
			   project_id = excluded.project_id,
			   agent_id = excluded.agent_id,
			   driver_kind = excluded.driver_kind,
			   native_session_id = excluded.native_session_id,
			   agent_info = excluded.agent_info,
			   workspace = excluded.workspace,
			   workspace_mode = excluded.workspace_mode,
			   workspace_branch = excluded.workspace_branch,
			   status = excluded.status,
			   task_id = excluded.task_id,
			   latest_run_id = excluded.latest_run_id,
			   provider = excluded.provider,
			   model = excluded.model,
			   capabilities = excluded.capabilities,
			   config_options = excluded.config_options,
			   available_commands = excluded.available_commands,
			   available_commands_authoritative = excluded.available_commands_authoritative,
			   mcp_servers = excluded.mcp_servers,
			   rtk_enabled = excluded.rtk_enabled,
			   context_summary = excluded.context_summary,
			   updated_at = excluded.updated_at`,
			s.sessionsTable,
		),
		session.ID,
		session.Title,
		session.ProjectID,
		normalizeAgentID(session),
		session.DriverKind,
		session.NativeSessionID,
		marshalImplementationInfo(session.AgentInfo),
		session.Workspace,
		session.WorkspaceMode,
		session.WorkspaceBranch,
		session.Status,
		session.TaskID,
		session.LatestRunID,
		session.Provider,
		session.Model,
		marshalModelCapabilities(session.Capabilities),
		marshalConfigOptions(session.ConfigOptions),
		marshalCommands(session.AvailableCommands),
		boolToSQLiteInt(session.AvailableCommandsAuthoritative),
		marshalMCPServers(session.MCPServers),
		boolToSQLiteInt(session.RTKEnabled),
		session.TurnsUsed,
		marshalContextSummary(session.ContextSummary),
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
			`SELECT s.id, s.title, s.project_id, s.agent_id, s.driver_kind, s.native_session_id, s.agent_info, s.workspace, s.workspace_mode, s.workspace_branch,
			        s.status, s.task_id, s.latest_run_id, s.provider, s.model, s.capabilities, s.config_options, s.available_commands, s.available_commands_authoritative, s.mcp_servers, s.rtk_enabled, s.turns_used, s.context_summary, s.created_at, s.updated_at,
			        COUNT(m.id) AS message_count
			 FROM %s AS s
			 LEFT JOIN %s AS m ON m.session_id = s.id
			 GROUP BY s.id, s.title, s.project_id, s.agent_id, s.driver_kind, s.native_session_id, s.agent_info, s.workspace, s.workspace_mode, s.workspace_branch,
			          s.status, s.task_id, s.latest_run_id, s.provider, s.model, s.capabilities, s.config_options, s.available_commands, s.available_commands_authoritative, s.mcp_servers, s.rtk_enabled, s.turns_used, s.context_summary, s.created_at, s.updated_at
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
		var availableCommands string
		var availableCommandsAuthoritative int
		var agentInfo string
		var mcpServers string
		var contextSummary string
		var rtkEnabled int
		if err := rows.Scan(
			&session.ID,
			&session.Title,
			&session.ProjectID,
			&session.AgentID,
			&session.DriverKind,
			&session.NativeSessionID,
			&agentInfo,
			&session.Workspace,
			&session.WorkspaceMode,
			&session.WorkspaceBranch,
			&session.Status,
			&session.TaskID,
			&session.LatestRunID,
			&session.Provider,
			&session.Model,
			&capabilities,
			&configOptions,
			&availableCommands,
			&availableCommandsAuthoritative,
			&mcpServers,
			&rtkEnabled,
			&session.TurnsUsed,
			&contextSummary,
			&session.CreatedAt,
			&session.UpdatedAt,
			&messageCount,
		); err != nil {
			return nil, fmt.Errorf("scan sqlite agent chat session: %w", err)
		}
		session.Capabilities = unmarshalModelCapabilities(capabilities)
		session.ConfigOptions = unmarshalConfigOptions(configOptions)
		session.AvailableCommands = unmarshalCommands(availableCommands)
		session.AvailableCommandsAuthoritative = availableCommandsAuthoritative != 0
		session.AgentInfo = unmarshalImplementationInfo(agentInfo)
		session.MCPServers = unmarshalMCPServers(mcpServers)
		session.ContextSummary = unmarshalContextSummary(contextSummary)
		session.RTKEnabled = rtkEnabled != 0
		if session.AgentID == "" {
			session.AgentID = DefaultAgentID
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
	tx, err := s.client.DB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin sqlite agent chat delete tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE session_id = ?`, s.messageRequestsTable), id); err != nil {
		return fmt.Errorf("delete sqlite agent chat message requests: %w", err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE session_id = ?`, s.messagesTable), id); err != nil {
		return fmt.Errorf("delete sqlite agent chat messages: %w", err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id = ?`, s.sessionsTable), id); err != nil {
		return fmt.Errorf("delete sqlite agent chat session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit sqlite agent chat delete tx: %w", err)
	}
	return nil
}

func (s *SQLiteStore) DeleteByProjectID(ctx context.Context, projectID string) error {
	tx, err := s.client.DB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin sqlite agent chat project delete tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(
		ctx,
		fmt.Sprintf(
			`DELETE FROM %s
			 WHERE session_id IN (SELECT id FROM %s WHERE project_id = ?)`,
			s.messageRequestsTable,
			s.sessionsTable,
		),
		projectID,
	); err != nil {
		return fmt.Errorf("delete sqlite agent chat project message requests: %w", err)
	}

	if _, err := tx.ExecContext(
		ctx,
		fmt.Sprintf(
			`DELETE FROM %s
			 WHERE session_id IN (SELECT id FROM %s WHERE project_id = ?)`,
			s.messagesTable,
			s.sessionsTable,
		),
		projectID,
	); err != nil {
		return fmt.Errorf("delete sqlite agent chat project messages: %w", err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE project_id = ?`, s.sessionsTable), projectID); err != nil {
		return fmt.Errorf("delete sqlite agent chat project sessions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit sqlite agent chat project delete tx: %w", err)
	}
	return nil
}

func (s *SQLiteStore) UpdateSession(ctx context.Context, id string, update func(*Session)) (Session, error) {
	// Session updates are full-record read/modify/writes. Serialize them with
	// message/link transactions on SQLite, and lock the session row on
	// PostgreSQL, so a callback that read before LinkTaskRun cannot later erase
	// the committed task/workspace projection.
	s.messageUpdateMu.Lock()
	defer s.messageUpdateMu.Unlock()

	tx, err := s.client.DB().BeginTx(ctx, nil)
	if err != nil {
		return Session{}, fmt.Errorf("begin agent chat session update tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if s.client.Dialect() == storage.DialectPostgres {
		var lockedID string
		if err := tx.QueryRowContext(ctx, fmt.Sprintf(`SELECT id FROM %s WHERE id = ? FOR UPDATE`, s.sessionsTable), id).Scan(&lockedID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return Session{}, fmt.Errorf("agent chat session %q not found", id)
			}
			return Session{}, fmt.Errorf("lock agent chat session for update: %w", err)
		}
	}
	session, err := s.loadSessionFrom(ctx, tx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, fmt.Errorf("agent chat session %q not found", id)
		}
		return Session{}, err
	}
	update(&session)
	session.UpdatedAt = time.Now().UTC()
	if err := s.updateSessionRecord(ctx, tx, session); err != nil {
		return Session{}, fmt.Errorf("update sqlite agent chat session: %w", err)
	}
	committed, err := s.loadSessionFrom(ctx, tx, id)
	if err != nil {
		return Session{}, err
	}
	if err := tx.Commit(); err != nil {
		return Session{}, fmt.Errorf("commit agent chat session update tx: %w", err)
	}
	return committed, nil
}

func (s *SQLiteStore) updateSessionRecord(ctx context.Context, runner execRunner, session Session) error {
	_, err := runner.ExecContext(
		ctx,
		fmt.Sprintf(
			`UPDATE %s SET
			   title = ?, project_id = ?, agent_id = ?, driver_kind = ?, native_session_id = ?, agent_info = ?, workspace = ?, workspace_mode = ?, workspace_branch = ?,
			   status = ?, task_id = ?, latest_run_id = ?, provider = ?, model = ?, capabilities = ?, config_options = ?, available_commands = ?, available_commands_authoritative = ?, mcp_servers = ?, rtk_enabled = ?, turns_used = ?, context_summary = ?, updated_at = ?
			 WHERE id = ?`,
			s.sessionsTable,
		),
		session.Title,
		session.ProjectID,
		normalizeAgentID(session),
		session.DriverKind,
		session.NativeSessionID,
		marshalImplementationInfo(session.AgentInfo),
		session.Workspace,
		session.WorkspaceMode,
		session.WorkspaceBranch,
		session.Status,
		session.TaskID,
		session.LatestRunID,
		session.Provider,
		session.Model,
		marshalModelCapabilities(session.Capabilities),
		marshalConfigOptions(session.ConfigOptions),
		marshalCommands(session.AvailableCommands),
		boolToSQLiteInt(session.AvailableCommandsAuthoritative),
		marshalMCPServers(session.MCPServers),
		boolToSQLiteInt(session.RTKEnabled),
		session.TurnsUsed,
		marshalContextSummary(session.ContextSummary),
		session.UpdatedAt.UTC(),
		session.ID,
	)
	return err
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
	if err := s.appendMessageTx(ctx, tx, sessionID, message); err != nil {
		return Session{}, err
	}
	if err := tx.Commit(); err != nil {
		return Session{}, fmt.Errorf("commit sqlite agent chat tx: %w", err)
	}
	return s.loadSession(ctx, sessionID)
}

func (s *SQLiteStore) appendMessageTx(ctx context.Context, tx storage.Tx, sessionID string, message Message) error {
	var nextSeq int
	if err := tx.QueryRowContext(
		ctx,
		fmt.Sprintf(`SELECT COALESCE(MAX(sequence), -1) + 1 FROM %s WHERE session_id = ?`, s.messagesTable),
		sessionID,
	).Scan(&nextSeq); err != nil {
		return fmt.Errorf("read sqlite agent chat next sequence: %w", err)
	}

	if _, err := tx.ExecContext(
		ctx,
		fmt.Sprintf(
			`INSERT INTO %s (
				id, session_id, sequence, execution_mode, tools_enabled, segment_id, task_id, run_id, request_id, trace_id, span_id,
				role, content, attachments, raw_output, agent_id, agent_name, driver_kind, native_session_id, agent_info, status, exit_code,
				cost_mode, provider, provider_instance, model, capabilities, workspace, diff_stat, diff, created_at, started_at, completed_at,
				error, activities, usage, timing, context_packet
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			s.messagesTable,
		),
		message.ID,
		sessionID,
		nextSeq,
		message.ExecutionMode,
		boolToSQLiteInt(message.ToolsEnabled),
		message.SegmentID,
		message.TaskID,
		message.RunID,
		message.RequestID,
		message.TraceID,
		message.SpanID,
		message.Role,
		message.Content,
		marshalMessageAttachments(message.Attachments),
		message.RawOutput,
		message.AgentID,
		message.AgentName,
		message.DriverKind,
		message.NativeSessionID,
		marshalImplementationInfo(message.AgentInfo),
		message.Status,
		message.ExitCode,
		message.CostMode,
		message.Provider,
		marshalProviderInstanceIdentity(message.ProviderInstance),
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
		marshalContextPacket(message.Context),
	); err != nil {
		return fmt.Errorf("insert sqlite agent chat message: %w", err)
	}

	if err := updateSessionAfterMessage(ctx, tx, s.sessionsTable, sessionID, message, true); err != nil {
		return err
	}
	return nil
}

func (s *SQLiteStore) UpdateMessage(ctx context.Context, sessionID string, messageID string, update func(*Message)) (Session, error) {
	// SQLite has no SELECT ... FOR UPDATE and begins deferred transactions.
	// Serialize in-process message RMWs so two callbacks cannot both read the
	// same row and then overwrite each other's independent fields. PostgreSQL
	// locks the session before the message so this path has the same cross-
	// instance order as LinkTaskRun.
	s.messageUpdateMu.Lock()
	defer s.messageUpdateMu.Unlock()

	tx, err := s.client.DB().BeginTx(ctx, nil)
	if err != nil {
		return Session{}, fmt.Errorf("begin sqlite agent chat tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	forUpdate := s.client.Dialect() == storage.DialectPostgres
	if forUpdate {
		var lockedID string
		if err := tx.QueryRowContext(ctx, fmt.Sprintf(`SELECT id FROM %s WHERE id = ? FOR UPDATE`, s.sessionsTable), sessionID).Scan(&lockedID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return Session{}, fmt.Errorf("agent chat session %q not found", sessionID)
			}
			return Session{}, fmt.Errorf("lock agent chat session for message update: %w", err)
		}
	}
	message, err := loadMessage(ctx, tx, s.messagesTable, sessionID, messageID, forUpdate)
	if err != nil {
		return Session{}, err
	}
	previousStatus := message.Status
	update(&message)
	if err := s.updateMessageRecord(ctx, tx, sessionID, message); err != nil {
		return Session{}, fmt.Errorf("update sqlite agent chat message: %w", err)
	}
	if err := updateSessionAfterMessage(ctx, tx, s.sessionsTable, sessionID, message, message.Status != previousStatus); err != nil {
		return Session{}, err
	}
	if err := tx.Commit(); err != nil {
		return Session{}, fmt.Errorf("commit sqlite agent chat tx: %w", err)
	}
	return s.loadSession(ctx, sessionID)
}

func (s *SQLiteStore) updateMessageRecord(ctx context.Context, runner execRunner, sessionID string, message Message) error {
	_, err := runner.ExecContext(
		ctx,
		fmt.Sprintf(
			`UPDATE %s SET
			   execution_mode = ?, tools_enabled = ?, segment_id = ?, task_id = ?, run_id = ?, request_id = ?, trace_id = ?, span_id = ?, role = ?, content = ?, attachments = ?, raw_output = ?, agent_id = ?, agent_name = ?,
			   driver_kind = ?, native_session_id = ?, agent_info = ?, status = ?, exit_code = ?,
			   cost_mode = ?, provider = ?, provider_instance = ?, model = ?, capabilities = ?, workspace = ?, diff_stat = ?, diff = ?, created_at = ?,
			   started_at = ?, completed_at = ?, error = ?, activities = ?, usage = ?, timing = ?, context_packet = ?
			 WHERE id = ? AND session_id = ?`,
			s.messagesTable,
		),
		message.ExecutionMode,
		boolToSQLiteInt(message.ToolsEnabled),
		message.SegmentID,
		message.TaskID,
		message.RunID,
		message.RequestID,
		message.TraceID,
		message.SpanID,
		message.Role,
		message.Content,
		marshalMessageAttachments(message.Attachments),
		message.RawOutput,
		message.AgentID,
		message.AgentName,
		message.DriverKind,
		message.NativeSessionID,
		marshalImplementationInfo(message.AgentInfo),
		message.Status,
		message.ExitCode,
		message.CostMode,
		message.Provider,
		marshalProviderInstanceIdentity(message.ProviderInstance),
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
		marshalContextPacket(message.Context),
		message.ID,
		sessionID,
	)
	return err
}

func (s *SQLiteStore) LinkTaskRun(ctx context.Context, sessionID, userMessageID, assistantMessageID string, update func(*Session, *Message, *Message)) (Session, error) {
	if strings.TrimSpace(userMessageID) == "" || strings.TrimSpace(assistantMessageID) == "" || userMessageID == assistantMessageID {
		return Session{}, fmt.Errorf("distinct task-run message ids are required")
	}
	// Serialize SQLite RMW transactions with UpdateMessage. PostgreSQL also
	// takes row locks below, preserving the same atomic contract across
	// multiple gateway instances.
	s.messageUpdateMu.Lock()
	defer s.messageUpdateMu.Unlock()

	tx, err := s.client.DB().BeginTx(ctx, nil)
	if err != nil {
		return Session{}, fmt.Errorf("begin agent chat task-run link tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	forUpdate := s.client.Dialect() == storage.DialectPostgres
	if forUpdate {
		var lockedID string
		if err := tx.QueryRowContext(ctx, fmt.Sprintf(`SELECT id FROM %s WHERE id = ? FOR UPDATE`, s.sessionsTable), sessionID).Scan(&lockedID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return Session{}, fmt.Errorf("agent chat session %q not found", sessionID)
			}
			return Session{}, fmt.Errorf("lock agent chat session for task-run link: %w", err)
		}
	}
	session, err := s.loadSessionFrom(ctx, tx, sessionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, fmt.Errorf("agent chat session %q not found", sessionID)
		}
		return Session{}, err
	}
	userMessage, err := loadMessage(ctx, tx, s.messagesTable, sessionID, userMessageID, forUpdate)
	if err != nil {
		return Session{}, err
	}
	assistantMessage, err := loadMessage(ctx, tx, s.messagesTable, sessionID, assistantMessageID, forUpdate)
	if err != nil {
		return Session{}, err
	}
	update(&session, &userMessage, &assistantMessage)
	session.UpdatedAt = time.Now().UTC()
	if err := s.updateSessionRecord(ctx, tx, session); err != nil {
		return Session{}, fmt.Errorf("update agent chat task-run session link: %w", err)
	}
	if err := s.updateMessageRecord(ctx, tx, sessionID, userMessage); err != nil {
		return Session{}, fmt.Errorf("update agent chat task-run user link: %w", err)
	}
	if err := s.updateMessageRecord(ctx, tx, sessionID, assistantMessage); err != nil {
		return Session{}, fmt.Errorf("update agent chat task-run assistant link: %w", err)
	}
	committed, err := s.loadSessionFrom(ctx, tx, sessionID)
	if err != nil {
		return Session{}, err
	}
	if err := tx.Commit(); err != nil {
		return Session{}, fmt.Errorf("commit agent chat task-run link tx: %w", err)
	}
	return committed, nil
}

func (s *SQLiteStore) migrate(ctx context.Context) error {
	if _, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS %s (
				id TEXT PRIMARY KEY,
				title TEXT NOT NULL,
				project_id TEXT NOT NULL DEFAULT '',
				agent_id TEXT NOT NULL DEFAULT 'hecate',
				driver_kind TEXT NOT NULL DEFAULT '',
				native_session_id TEXT NOT NULL DEFAULT '',
				agent_info TEXT NOT NULL DEFAULT '{}',
				workspace TEXT NOT NULL,
				workspace_mode TEXT NOT NULL DEFAULT '',
				workspace_branch TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL,
				task_id TEXT NOT NULL DEFAULT '',
				latest_run_id TEXT NOT NULL DEFAULT '',
				provider TEXT NOT NULL DEFAULT '',
				model TEXT NOT NULL DEFAULT '',
				capabilities TEXT NOT NULL DEFAULT '{}',
				config_options TEXT NOT NULL DEFAULT '[]',
				available_commands TEXT NOT NULL DEFAULT '[]',
				available_commands_authoritative INTEGER NOT NULL DEFAULT 0,
				mcp_servers TEXT NOT NULL DEFAULT '[]',
				context_summary TEXT NOT NULL DEFAULT '{}',
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
				execution_mode TEXT NOT NULL DEFAULT '',
				segment_id TEXT NOT NULL DEFAULT '',
				task_id TEXT NOT NULL DEFAULT '',
				role TEXT NOT NULL,
				content TEXT NOT NULL,
				attachments TEXT NOT NULL DEFAULT '[]',
				raw_output TEXT NOT NULL DEFAULT '',
				agent_id TEXT NOT NULL,
				agent_name TEXT NOT NULL,
				driver_kind TEXT NOT NULL DEFAULT '',
				native_session_id TEXT NOT NULL DEFAULT '',
				agent_info TEXT NOT NULL DEFAULT '{}',
				status TEXT NOT NULL,
				exit_code INTEGER NOT NULL,
				run_id TEXT NOT NULL DEFAULT '',
				request_id TEXT NOT NULL DEFAULT '',
				trace_id TEXT NOT NULL DEFAULT '',
				span_id TEXT NOT NULL DEFAULT '',
				cost_mode TEXT NOT NULL,
				provider TEXT NOT NULL DEFAULT '',
				provider_instance TEXT NOT NULL DEFAULT '{}',
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
				context_packet TEXT NOT NULL DEFAULT '{}',
				UNIQUE (session_id, sequence)
			)`,
			s.messagesTable,
			s.sessionsTable,
		),
	); err != nil {
		return fmt.Errorf("migrate sqlite agent chat messages: %w", err)
	}
	if _, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS %s (
				session_id TEXT NOT NULL REFERENCES %s (id) ON DELETE CASCADE,
				client_request_id TEXT NOT NULL,
				payload_fingerprint TEXT NOT NULL,
				state TEXT NOT NULL,
				owner_instance TEXT NOT NULL,
				owner_token TEXT NOT NULL,
				message_id TEXT NOT NULL DEFAULT '',
				created_at TIMESTAMP NOT NULL,
				updated_at TIMESTAMP NOT NULL,
				PRIMARY KEY (session_id, client_request_id)
			)`,
			s.messageRequestsTable,
			s.sessionsTable,
		),
	); err != nil {
		return fmt.Errorf("migrate sqlite agent chat message requests: %w", err)
	}
	if err := s.ensureSessionColumn(ctx, "workspace_branch", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureSessionColumn(ctx, "workspace_mode", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureSessionColumn(ctx, "project_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureSessionColumn(ctx, "driver_kind", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureSessionColumn(ctx, "native_session_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureSessionColumn(ctx, "agent_info", "TEXT NOT NULL DEFAULT '{}'"); err != nil {
		return err
	}
	if err := s.ensureSessionColumn(ctx, "turns_used", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	for _, column := range []struct {
		name       string
		definition string
	}{
		{name: "agent_id", definition: "TEXT NOT NULL DEFAULT 'hecate'"},
		{name: "task_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "latest_run_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "provider", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "model", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "capabilities", definition: "TEXT NOT NULL DEFAULT '{}'"},
		{name: "config_options", definition: "TEXT NOT NULL DEFAULT '[]'"},
		{name: "available_commands", definition: "TEXT NOT NULL DEFAULT '[]'"},
		{name: "available_commands_authoritative", definition: "INTEGER NOT NULL DEFAULT 0"},
		{name: "mcp_servers", definition: "TEXT NOT NULL DEFAULT '[]'"},
		{name: "context_summary", definition: "TEXT NOT NULL DEFAULT '{}'"},
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
		{name: "execution_mode", definition: "TEXT NOT NULL DEFAULT ''"},
		// Boolean stored as INTEGER (0/1). DEFAULT 1 so existing rows
		// land on "tools on" — the agent path is the safe assumption.
		// Tools-off direct model turns are represented as hecate_task
		// messages with tools_enabled=0.
		{name: "tools_enabled", definition: "INTEGER NOT NULL DEFAULT 1"},
		{name: "segment_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "task_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "request_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "trace_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "span_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "driver_kind", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "native_session_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "agent_info", definition: "TEXT NOT NULL DEFAULT '{}'"},
		{name: "started_at", definition: "TIMESTAMP"},
		{name: "completed_at", definition: "TIMESTAMP"},
		{name: "error", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "raw_output", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "attachments", definition: "TEXT NOT NULL DEFAULT '[]'"},
		{name: "provider", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "provider_instance", definition: "TEXT NOT NULL DEFAULT '{}'"},
		{name: "model", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "capabilities", definition: "TEXT NOT NULL DEFAULT '{}'"},
		{name: "activities", definition: "TEXT NOT NULL DEFAULT '[]'"},
		{name: "usage", definition: "TEXT NOT NULL DEFAULT '{}'"},
		{name: "timing", definition: "TEXT NOT NULL DEFAULT '{}'"},
		{name: "context_packet", definition: "TEXT NOT NULL DEFAULT '{}'"},
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
	return s.loadSessionFrom(ctx, s.client.DB(), id)
}

func (s *SQLiteStore) loadSessionFrom(ctx context.Context, runner queryRunner, id string) (Session, error) {
	var session Session
	var capabilities string
	var configOptions string
	var availableCommands string
	var availableCommandsAuthoritative int
	var agentInfo string
	var mcpServers string
	var contextSummary string
	var rtkEnabled int
	err := runner.QueryRowContext(
		ctx,
		fmt.Sprintf(
			`SELECT id, title, project_id, agent_id, driver_kind, native_session_id, agent_info, workspace, workspace_mode, workspace_branch,
			        status, task_id, latest_run_id, provider, model, capabilities, config_options, available_commands, available_commands_authoritative, mcp_servers, rtk_enabled, turns_used, context_summary, created_at, updated_at
			 FROM %s WHERE id = ?`,
			s.sessionsTable,
		),
		id,
	).Scan(
		&session.ID,
		&session.Title,
		&session.ProjectID,
		&session.AgentID,
		&session.DriverKind,
		&session.NativeSessionID,
		&agentInfo,
		&session.Workspace,
		&session.WorkspaceMode,
		&session.WorkspaceBranch,
		&session.Status,
		&session.TaskID,
		&session.LatestRunID,
		&session.Provider,
		&session.Model,
		&capabilities,
		&configOptions,
		&availableCommands,
		&availableCommandsAuthoritative,
		&mcpServers,
		&rtkEnabled,
		&session.TurnsUsed,
		&contextSummary,
		&session.CreatedAt,
		&session.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, sql.ErrNoRows
		}
		return Session{}, fmt.Errorf("read sqlite agent chat session: %w", err)
	}
	if session.AgentID == "" {
		session.AgentID = DefaultAgentID
	}
	session.Capabilities = unmarshalModelCapabilities(capabilities)
	session.ConfigOptions = unmarshalConfigOptions(configOptions)
	session.AvailableCommands = unmarshalCommands(availableCommands)
	session.AvailableCommandsAuthoritative = availableCommandsAuthoritative != 0
	session.AgentInfo = unmarshalImplementationInfo(agentInfo)
	session.MCPServers = unmarshalMCPServers(mcpServers)
	session.ContextSummary = unmarshalContextSummary(contextSummary)
	session.RTKEnabled = rtkEnabled != 0
	messages, err := s.loadMessagesFrom(ctx, runner, id)
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
	return s.loadMessagesFrom(ctx, s.client.DB(), sessionID)
}

func (s *SQLiteStore) loadMessagesFrom(ctx context.Context, runner queryRunner, sessionID string) ([]Message, error) {
	rows, err := runner.QueryContext(
		ctx,
		fmt.Sprintf(
			`SELECT id, execution_mode, tools_enabled, segment_id, task_id, run_id, request_id, trace_id, span_id, role, content, attachments, raw_output, agent_id, agent_name, driver_kind, native_session_id, agent_info, status, exit_code, cost_mode,
				        provider, provider_instance, model, capabilities, workspace, diff_stat, diff, created_at, started_at, completed_at, error, activities, usage, timing, context_packet
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
		var contextPacket string
		var agentInfo string
		var attachments string
		var providerInstance string
		var toolsEnabledInt int64
		if err := rows.Scan(
			&message.ID,
			&message.ExecutionMode,
			&toolsEnabledInt,
			&message.SegmentID,
			&message.TaskID,
			&message.RunID,
			&message.RequestID,
			&message.TraceID,
			&message.SpanID,
			&message.Role,
			&message.Content,
			&attachments,
			&message.RawOutput,
			&message.AgentID,
			&message.AgentName,
			&message.DriverKind,
			&message.NativeSessionID,
			&agentInfo,
			&message.Status,
			&message.ExitCode,
			&message.CostMode,
			&message.Provider,
			&providerInstance,
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
			&contextPacket,
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
		message.Attachments = unmarshalMessageAttachments(attachments)
		message.Usage = unmarshalUsage(usage)
		message.Timing = unmarshalTiming(timing)
		message.Capabilities = unmarshalModelCapabilities(capabilities)
		message.ProviderInstance = unmarshalProviderInstanceIdentity(providerInstance)
		message.Context = unmarshalContextPacket(contextPacket)
		message.AgentInfo = unmarshalImplementationInfo(agentInfo)
		message.ToolsEnabled = toolsEnabledInt != 0
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sqlite agent chat messages: %w", err)
	}
	return messages, nil
}

// boolToSQLiteInt converts a Go bool to the 0/1 integer that the
// `tools_enabled` column stores. SQLite has no native boolean type
// and modernc.org/sqlite does not auto-bind bool to INTEGER, so the
// translation has to be explicit at every write site.
func boolToSQLiteInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func (s *SQLiteStore) ensureMessageColumn(ctx context.Context, column, definition string) error {
	exists, err := s.columnExists(ctx, s.messagesTable, column)
	if err != nil {
		return fmt.Errorf("inspect %s agent chat messages columns: %w", s.backend, err)
	}
	if exists {
		return nil
	}
	if _, err := s.client.DB().ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, s.messagesTable, column, definition)); err != nil {
		return fmt.Errorf("migrate %s agent chat messages %s: %w", s.backend, column, err)
	}
	return nil
}

func (s *SQLiteStore) ensureSessionColumn(ctx context.Context, column, definition string) error {
	exists, err := s.columnExists(ctx, s.sessionsTable, column)
	if err != nil {
		return fmt.Errorf("inspect %s agent chat sessions columns: %w", s.backend, err)
	}
	if exists {
		return nil
	}
	if _, err := s.client.DB().ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, s.sessionsTable, column, definition)); err != nil {
		return fmt.Errorf("migrate %s agent chat sessions %s: %w", s.backend, column, err)
	}
	return nil
}

func (s *SQLiteStore) columnExists(ctx context.Context, quotedTable, column string) (bool, error) {
	return storage.ColumnExists(ctx, s.client, strings.Trim(quotedTable, `"`), column)
}

type queryRunner interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type execRunner interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type txRunner interface {
	execRunner
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func loadMessage(ctx context.Context, tx txRunner, table string, sessionID string, messageID string, forUpdate bool) (Message, error) {
	var message Message
	var startedAt, completedAt sql.NullTime
	var activities string
	var usage string
	var timing string
	var capabilities string
	var contextPacket string
	var agentInfo string
	var attachments string
	var providerInstance string
	var toolsEnabledInt int64
	lockClause := ""
	if forUpdate {
		lockClause = " FOR UPDATE"
	}
	err := tx.QueryRowContext(
		ctx,
		fmt.Sprintf(
			`SELECT id, execution_mode, tools_enabled, segment_id, task_id, run_id, request_id, trace_id, span_id, role, content, attachments, raw_output, agent_id, agent_name, driver_kind, native_session_id, agent_info, status, exit_code, cost_mode,
			        provider, provider_instance, model, capabilities, workspace, diff_stat, diff, created_at, started_at, completed_at, error, activities, usage, timing, context_packet
			 FROM %s
			 WHERE id = ? AND session_id = ?%s`,
			table,
			lockClause,
		),
		messageID,
		sessionID,
	).Scan(
		&message.ID,
		&message.ExecutionMode,
		&toolsEnabledInt,
		&message.SegmentID,
		&message.TaskID,
		&message.RunID,
		&message.RequestID,
		&message.TraceID,
		&message.SpanID,
		&message.Role,
		&message.Content,
		&attachments,
		&message.RawOutput,
		&message.AgentID,
		&message.AgentName,
		&message.DriverKind,
		&message.NativeSessionID,
		&agentInfo,
		&message.Status,
		&message.ExitCode,
		&message.CostMode,
		&message.Provider,
		&providerInstance,
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
		&contextPacket,
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
	message.Attachments = unmarshalMessageAttachments(attachments)
	message.Usage = unmarshalUsage(usage)
	message.Timing = unmarshalTiming(timing)
	message.Capabilities = unmarshalModelCapabilities(capabilities)
	message.ProviderInstance = unmarshalProviderInstanceIdentity(providerInstance)
	message.Context = unmarshalContextPacket(contextPacket)
	message.AgentInfo = unmarshalImplementationInfo(agentInfo)
	message.ToolsEnabled = toolsEnabledInt != 0
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

func marshalMessageAttachments(items []MessageAttachment) string {
	if len(items) == 0 {
		return "[]"
	}
	data, err := json.Marshal(items)
	if err != nil {
		return "[]"
	}
	return string(data)
}

func unmarshalMessageAttachments(raw string) []MessageAttachment {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var items []MessageAttachment
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

func marshalContextPacket(packet ContextPacket) string {
	if packet.Empty() {
		return "{}"
	}
	data, err := json.Marshal(packet)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func unmarshalContextPacket(raw string) ContextPacket {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ContextPacket{}
	}
	var packet ContextPacket
	if err := json.Unmarshal([]byte(raw), &packet); err != nil {
		return ContextPacket{}
	}
	return packet
}

func marshalContextSummary(summary ContextSummary) string {
	if summary.Empty() {
		return "{}"
	}
	data, err := json.Marshal(summary)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func unmarshalContextSummary(raw string) ContextSummary {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ContextSummary{}
	}
	var summary ContextSummary
	if err := json.Unmarshal([]byte(raw), &summary); err != nil {
		return ContextSummary{}
	}
	return cloneContextSummary(summary)
}

func normalizeAgentID(session Session) string {
	if strings.TrimSpace(session.AgentID) != "" {
		return strings.TrimSpace(session.AgentID)
	}
	return DefaultAgentID
}

func marshalModelCapabilities(capabilities types.ModelCapabilities) string {
	if capabilities.ToolCalling == "" && capabilities.ImageInput == "" && !capabilities.Streaming && capabilities.MaxContextTokens == 0 && capabilities.Source == "" {
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

func marshalProviderInstanceIdentity(identity types.ProviderInstanceIdentity) string {
	if !identity.Valid() {
		return "{}"
	}
	data, err := json.Marshal(identity)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func unmarshalProviderInstanceIdentity(raw string) types.ProviderInstanceIdentity {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return types.ProviderInstanceIdentity{}
	}
	var identity types.ProviderInstanceIdentity
	if err := json.Unmarshal([]byte(raw), &identity); err != nil || !identity.Valid() {
		return types.ProviderInstanceIdentity{}
	}
	return identity
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

func marshalCommands(commands []agentcontrols.Command) string {
	if len(commands) == 0 {
		return "[]"
	}
	data, err := json.Marshal(commands)
	if err != nil {
		return "[]"
	}
	return string(data)
}

func unmarshalCommands(raw string) []agentcontrols.Command {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var commands []agentcontrols.Command
	if err := json.Unmarshal([]byte(raw), &commands); err != nil {
		return nil
	}
	return commands
}

func marshalImplementationInfo(info *agentcontrols.ImplementationInfo) string {
	if info == nil || (info.Name == "" && info.Title == "" && info.Version == "") {
		return "{}"
	}
	data, err := json.Marshal(info)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func unmarshalImplementationInfo(raw string) *agentcontrols.ImplementationInfo {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return nil
	}
	var info agentcontrols.ImplementationInfo
	if err := json.Unmarshal([]byte(raw), &info); err != nil {
		return nil
	}
	return cloneImplementationInfo(&info)
}

func marshalMCPServers(configs []types.MCPServerConfig) string {
	if len(configs) == 0 {
		return "[]"
	}
	data, err := json.Marshal(configs)
	if err != nil {
		return "[]"
	}
	return string(data)
}

func unmarshalMCPServers(raw string) []types.MCPServerConfig {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var configs []types.MCPServerConfig
	if err := json.Unmarshal([]byte(raw), &configs); err != nil {
		return nil
	}
	return configs
}

func updateSessionAfterMessage(ctx context.Context, tx txRunner, table string, sessionID string, message Message, projectStatus bool) error {
	status := ""
	if projectStatus && message.Role == "assistant" {
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
