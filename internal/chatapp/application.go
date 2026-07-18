package chatapp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/agentcontrols"
	"github.com/hecatehq/hecate/internal/apperrors"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/chatattachments"
	"github.com/hecatehq/hecate/pkg/types"
)

var (
	ErrStoreNotConfigured       = errors.New("agent chat store is not configured")
	ErrRunnerNotConfigured      = errors.New("agent chat runner is not configured")
	ErrExternalSessionOnly      = errors.New("agent chat config options are only available for external-agent sessions")
	ErrHecateSessionOnly        = errors.New("Hecate Chat settings are not available for external-agent sessions")
	ErrNoSettingsProvided       = errors.New("no settings provided")
	ErrWorkspaceModeInvalid     = errors.New("workspace_mode must be persistent, ephemeral, or in_place")
	ErrWorkspaceModeLocked      = errors.New("workspace_mode cannot change after a task-backed chat segment starts")
	ErrContentRequired          = errors.New("content is required")
	ErrExecutionModeInvalid     = errors.New("execution_mode must be hecate_task or external_agent")
	ErrExternalCannotRunHecate  = errors.New("external agent sessions cannot run Hecate Chat turns")
	ErrHecateCannotRunExternal  = errors.New("Hecate Chat sessions cannot run external-agent turns")
	ErrSessionNotFound          = errors.New("agent chat session not found")
	ErrSessionIDRequired        = errors.New("session id is required")
	ErrNativeSessionIDRequired  = errors.New("native session ids are required")
	ErrNativeSessionChanged     = errors.New("native session changed before replacement could be persisted")
	ErrTitleRequired            = errors.New("request must include title")
	ErrTitleEmpty               = errors.New("title cannot be set to an empty string")
	ErrNothingToCompact         = errors.New("chat transcript has no older context to compact")
	ErrAttachmentStoreMissing   = errors.New("chat attachment store is not configured")
	ErrAttachmentNotFound       = errors.New("chat attachment not found")
	ErrAttachmentInUse          = errors.New("chat attachment is already used by a message")
	ErrAttachmentDraftQuota     = errors.New("chat attachment draft quota exceeded")
	ErrAttachmentSessionQuota   = errors.New("chat attachment session storage quota exceeded")
	ErrAttachmentTotalQuota     = errors.New("chat attachment total storage quota exceeded")
	ErrAttachmentMessageBytes   = errors.New("combined chat attachment size exceeds the per-message limit")
	ErrAttachmentMessageID      = errors.New("attachment claim message id is required")
	ErrAttachmentIDRequired     = errors.New("attachment id is required")
	ErrAttachmentRollback       = errors.New("chat attachment cleanup failed")
	ErrAttachmentSessionCleanup = errors.New("chat attachment session cleanup failed")
	ErrDuplicateAttachmentID    = errors.New("duplicate attachment id")
	ErrTooManyAttachments       = errors.New("a chat message supports at most 4 attachments")
	ErrClientRequestIDInvalid   = errors.New("client_request_id must be 1-128 ASCII letters, digits, dots, underscores, colons, or hyphens")
	ErrUserMessageRequired      = errors.New("message role must be user")
)

const (
	MaxMessageAttachments = agentadapters.MaxPromptFiles
	// MaxMessageAttachmentBytes leaves headroom for base64 expansion, prompt
	// text, and JSON framing under providers with 20 MiB inline envelopes.
	MaxMessageAttachmentBytes       = agentadapters.MaxPromptFilesBytes
	defaultAttachmentCleanupTimeout = 3 * time.Second
	minimumMessageRequestLeaseTTL   = 3 * time.Millisecond
)

type ValidationError = apperrors.ValidationError

type ConflictError = apperrors.ConflictError

func Conflict(err error) error { return apperrors.Conflict(err) }

func NormalizeWorkspaceMode(mode string) (string, error) {
	switch mode = strings.TrimSpace(mode); mode {
	case chat.WorkspaceModePersistent, chat.WorkspaceModeEphemeral, chat.WorkspaceModeInPlace:
		return mode, nil
	case "":
		return chat.WorkspaceModeInPlace, nil
	default:
		return "", Validation(ErrWorkspaceModeInvalid)
	}
}

type AttachmentTotalQuotaError struct {
	LimitBytes int64
}

func (e AttachmentTotalQuotaError) Error() string { return ErrAttachmentTotalQuota.Error() }

func (e AttachmentTotalQuotaError) Unwrap() error { return ErrAttachmentTotalQuota }

// AttachmentRollbackError keeps the storage and ownership causes available to
// callers without making their potentially sensitive details safe to render in
// an HTTP response.
type AttachmentRollbackError struct {
	cause error
}

func (e *AttachmentRollbackError) Error() string { return ErrAttachmentRollback.Error() }

func (e *AttachmentRollbackError) Unwrap() []error {
	return []error{ErrAttachmentRollback, e.cause}
}

// AttachmentSessionCleanupError preserves the durable-store cause for internal
// retry and diagnostics without making storage details safe for an HTTP body.
type AttachmentSessionCleanupError struct {
	cause error
}

func (e *AttachmentSessionCleanupError) Error() string { return ErrAttachmentSessionCleanup.Error() }

func (e *AttachmentSessionCleanupError) Unwrap() []error {
	return []error{ErrAttachmentSessionCleanup, e.cause}
}

func Validation(err error) error {
	return apperrors.Validation(err)
}

func IsValidationError(err error) bool {
	return apperrors.IsValidationError(err)
}

type SessionStore interface {
	Create(ctx context.Context, session chat.Session) (chat.Session, error)
	Get(ctx context.Context, id string) (chat.Session, bool, error)
	List(ctx context.Context) ([]chat.Session, error)
	UpdateSession(ctx context.Context, id string, update func(*chat.Session)) (chat.Session, error)
	Delete(ctx context.Context, id string) error
}

type MessageStore interface {
	AppendMessage(ctx context.Context, sessionID string, message chat.Message) (chat.Session, error)
	MessageRequestLeaseTTL() time.Duration
	ClaimMessageRequest(ctx context.Context, sessionID, clientRequestID string, fingerprint chat.MessageRequestFingerprint) (chat.MessageRequestClaim, error)
	RenewMessageRequest(ctx context.Context, req chat.RenewMessageRequestRequest) error
	CommitMessageRequest(ctx context.Context, lease chat.MessageRequestLease, message chat.Message) (chat.Session, error)
	ReleaseMessageRequest(ctx context.Context, lease chat.MessageRequestLease) error
}

type AttachmentStore interface {
	Create(context.Context, chatattachments.StoredAttachment) (chatattachments.StoredAttachment, error)
	Get(context.Context, string, string) (chatattachments.StoredAttachment, bool, error)
	Claim(context.Context, chatattachments.ClaimRef) ([]chatattachments.StoredAttachment, error)
	ResolveClaim(context.Context, chatattachments.ClaimRef, chatattachments.ClaimResolution) error
	ListPendingClaims(context.Context) ([]chatattachments.PendingClaim, error)
	ListSessionIDs(context.Context) ([]string, error)
	DeleteDraft(context.Context, string, string) error
	DeleteBySessionID(context.Context, string) error
}

type TaskStore interface {
	GetTask(ctx context.Context, id string) (types.Task, bool, error)
	UpdateTask(ctx context.Context, task types.Task) (types.Task, error)
}

type AgentRunner interface {
	PrepareSession(context.Context, agentadapters.PrepareSessionRequest) (agentadapters.PrepareSessionResult, error)
	CloseSession(context.Context, string) error
	DeleteSession(context.Context, string) error
	SetSessionConfigOption(context.Context, agentadapters.SetSessionConfigOptionRequest) (agentadapters.SetSessionConfigOptionResult, error)
}

type Application struct {
	store                    SessionStore
	messages                 MessageStore
	taskStore                TaskStore
	runner                   AgentRunner
	attachments              AttachmentStore
	prepareTimeout           time.Duration
	configOptionTimeout      time.Duration
	attachmentCleanupTimeout time.Duration
	messageRequestRenewEvery time.Duration
}

type Options struct {
	Store                    SessionStore
	Messages                 MessageStore
	TaskStore                TaskStore
	Runner                   AgentRunner
	Attachments              AttachmentStore
	PrepareTimeout           time.Duration
	ConfigOptionTimeout      time.Duration
	AttachmentCleanupTimeout time.Duration
}

type CreateSessionCommand struct {
	Session         chat.Session
	PrepareExternal bool
}

type CreateSessionResult struct {
	Session chat.Session
}

type DeleteSessionCommand struct {
	SessionID    string
	DeleteNative bool
}

type CloseNativeSessionCommand struct {
	Session             chat.Session
	NativeAlreadyClosed bool
}

type CloseNativeSessionResult struct {
	Session chat.Session
}

type ReplaceNativeSessionCommand struct {
	SessionID               string
	PreviousNativeSessionID string
	NativeSessionID         string
}

type SetConfigOptionCommand struct {
	Session  chat.Session
	ConfigID string
	Value    any
}

type SetConfigOptionResult struct {
	Session chat.Session
}

type SetHecateSettingsCommand struct {
	Session       chat.Session
	RTKEnabled    *bool
	WorkspaceMode *string
}

type SetHecateSettingsResult struct {
	Session chat.Session
}

type RenameSessionCommand struct {
	ID    string
	Title *string
}

type CompactSessionCommand struct {
	ID               string
	RetainMessages   int
	MinMessages      int
	HecateOnly       bool
	RequireCompacted bool
	Now              time.Time
}

type CompactSessionSummaryFunc func(context.Context, chat.Session, chat.CompactTranscriptResult) (chat.ContextSummary, error)

type SessionResult struct {
	Session chat.Session
}

type ListSessionsResult struct {
	Sessions []chat.Session
}

type MessageLimits struct {
	MaxTurnsPerSession int
	MaxSessionDuration time.Duration
	IdleTimeout        time.Duration
}

type AdmitMessageCommand struct {
	Session         chat.Session
	Content         string
	ExecutionMode   string
	ToolsEnabled    *bool
	AttachmentCount int
	Limits          MessageLimits
	Now             time.Time
}

type CreateAttachmentCommand struct {
	Attachment chatattachments.StoredAttachment
}

type AttachmentCommand struct {
	SessionID    string
	AttachmentID string
}

type AdmitMessageResult struct {
	Content       string
	ExecutionMode string
	ToolsEnabled  bool
}

type MessageDispatchRoute string

const (
	MessageDispatchHecateTask    MessageDispatchRoute = "hecate_task"
	MessageDispatchDirectModel   MessageDispatchRoute = "direct_model"
	MessageDispatchExternalAgent MessageDispatchRoute = "external_agent"
)

type MessageDispatchPlan struct {
	Content       string
	ExecutionMode string
	ToolsEnabled  bool
	Route         MessageDispatchRoute
}

// BuildExternalPromptInput converts already-admitted Hecate attachment bodies
// into the short-lived provider-neutral contract used by the selected ACP
// session. Claim ownership and integrity validation must complete before this
// function is called; the adapter boundary validates the snapshot again.
func BuildExternalPromptInput(text string, attachments []chatattachments.StoredAttachment) agentadapters.PromptInput {
	input := agentadapters.PromptInput{Text: text}
	if len(attachments) == 0 {
		return input
	}
	input.Files = make([]agentadapters.PromptFile, 0, len(attachments))
	for _, attachment := range attachments {
		input.Files = append(input.Files, agentadapters.PromptFile{
			Filename:  attachment.Filename,
			MediaType: attachment.MediaType,
			SizeBytes: attachment.SizeBytes,
			SHA256:    attachment.SHA256,
			Data:      attachment.Data,
		})
	}
	return input
}

type MessageLimitError struct {
	Code      string
	Message   string
	Limit     int
	LimitMS   int64
	StartedAt time.Time
	UpdatedAt time.Time
	TurnsUsed int
}

func (e MessageLimitError) Error() string {
	return e.Message
}

type ExternalPrepareError struct {
	Err error
}

func (e ExternalPrepareError) Error() string {
	if e.Err == nil {
		return "external agent prepare failed"
	}
	return e.Err.Error()
}

func (e ExternalPrepareError) Unwrap() error {
	return e.Err
}

func New(opts Options) *Application {
	attachmentCleanupTimeout := opts.AttachmentCleanupTimeout
	if attachmentCleanupTimeout <= 0 {
		attachmentCleanupTimeout = defaultAttachmentCleanupTimeout
	}
	messages := opts.Messages
	if messages == nil {
		messages, _ = opts.Store.(MessageStore)
	}
	messageRequestLeaseTTL := chat.MessageRequestLeaseStaleAfter
	if messages != nil {
		if ttl := messages.MessageRequestLeaseTTL(); ttl > 0 {
			messageRequestLeaseTTL = ttl
		}
	}
	if messageRequestLeaseTTL < minimumMessageRequestLeaseTTL {
		messageRequestLeaseTTL = minimumMessageRequestLeaseTTL
	}
	messageRequestRenewEvery := messageRequestLeaseTTL / 3
	return &Application{
		store:                    opts.Store,
		messages:                 messages,
		taskStore:                opts.TaskStore,
		runner:                   opts.Runner,
		attachments:              opts.Attachments,
		prepareTimeout:           opts.PrepareTimeout,
		configOptionTimeout:      opts.ConfigOptionTimeout,
		attachmentCleanupTimeout: attachmentCleanupTimeout,
		messageRequestRenewEvery: messageRequestRenewEvery,
	}
}

func (app *Application) ListSessions(ctx context.Context) (*ListSessionsResult, error) {
	if app == nil || app.store == nil {
		return nil, ErrStoreNotConfigured
	}
	sessions, err := app.store.List(ctx)
	if err != nil {
		return nil, err
	}
	return &ListSessionsResult{Sessions: sessions}, nil
}

func (app *Application) GetSession(ctx context.Context, id string) (*SessionResult, error) {
	if app == nil || app.store == nil {
		return nil, ErrStoreNotConfigured
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, Validation(ErrSessionIDRequired)
	}
	session, ok, err := app.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrSessionNotFound
	}
	return &SessionResult{Session: session}, nil
}

func (app *Application) RenameSession(ctx context.Context, cmd RenameSessionCommand) (*SessionResult, error) {
	if app == nil || app.store == nil {
		return nil, ErrStoreNotConfigured
	}
	id := strings.TrimSpace(cmd.ID)
	if id == "" {
		return nil, Validation(ErrSessionIDRequired)
	}
	if cmd.Title == nil {
		return nil, Validation(ErrTitleRequired)
	}
	title := strings.TrimSpace(*cmd.Title)
	if title == "" {
		return nil, Validation(ErrTitleEmpty)
	}
	if _, ok, err := app.store.Get(ctx, id); err != nil {
		return nil, err
	} else if !ok {
		return nil, ErrSessionNotFound
	}
	updated, err := app.store.UpdateSession(ctx, id, func(item *chat.Session) {
		item.Title = title
	})
	if err != nil {
		return nil, err
	}
	return &SessionResult{Session: updated}, nil
}

// ReplaceNativeSession atomically fences a provider-native id replacement
// against the durable session value observed before recovery. The caller must
// complete this command before redisclosing the prompt to the fresh session.
func (app *Application) ReplaceNativeSession(ctx context.Context, cmd ReplaceNativeSessionCommand) (*SessionResult, error) {
	if app == nil || app.store == nil {
		return nil, ErrStoreNotConfigured
	}
	sessionID := strings.TrimSpace(cmd.SessionID)
	previousID := strings.TrimSpace(cmd.PreviousNativeSessionID)
	nativeID := strings.TrimSpace(cmd.NativeSessionID)
	if sessionID == "" {
		return nil, Validation(ErrSessionIDRequired)
	}
	if previousID == "" || nativeID == "" {
		return nil, Validation(ErrNativeSessionIDRequired)
	}
	var changed bool
	updated, err := app.store.UpdateSession(ctx, sessionID, func(item *chat.Session) {
		if strings.TrimSpace(item.NativeSessionID) != previousID {
			changed = true
			return
		}
		item.NativeSessionID = nativeID
		chat.ResetAvailableCommandsAuthority(item)
	})
	if err != nil {
		return nil, err
	}
	if changed {
		return nil, ErrNativeSessionChanged
	}
	return &SessionResult{Session: updated}, nil
}

func (app *Application) CompactSession(ctx context.Context, cmd CompactSessionCommand) (*SessionResult, error) {
	return app.CompactSessionWithSummary(ctx, cmd, nil)
}

func (app *Application) CompactSessionWithSummary(ctx context.Context, cmd CompactSessionCommand, summarize CompactSessionSummaryFunc) (*SessionResult, error) {
	if app == nil || app.store == nil {
		return nil, ErrStoreNotConfigured
	}
	id := strings.TrimSpace(cmd.ID)
	if id == "" {
		return nil, Validation(ErrSessionIDRequired)
	}
	session, ok, err := app.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrSessionNotFound
	}
	if cmd.HecateOnly && isExternalSession(session) {
		return nil, ErrHecateSessionOnly
	}
	result := chat.CompactTranscriptSummary(session, chat.CompactTranscriptOptions{
		Now:            cmd.Now,
		RetainMessages: cmd.RetainMessages,
		MinMessages:    cmd.MinMessages,
	})
	if !result.Compacted {
		if cmd.RequireCompacted {
			return nil, ErrNothingToCompact
		}
		return &SessionResult{Session: session}, nil
	}
	summary := result.Summary
	if summarize != nil {
		customSummary, err := summarize(ctx, session, result)
		if err != nil {
			return nil, err
		}
		summary = customSummary
	}
	updated, err := app.store.UpdateSession(ctx, id, func(item *chat.Session) {
		item.ContextSummary = summary
	})
	if err != nil {
		return nil, err
	}
	return &SessionResult{Session: updated}, nil
}

func (app *Application) CreateSession(ctx context.Context, cmd CreateSessionCommand) (*CreateSessionResult, error) {
	if app == nil || app.store == nil {
		return nil, ErrStoreNotConfigured
	}
	if cmd.PrepareExternal && app.runner == nil {
		return nil, ErrRunnerNotConfigured
	}
	session, err := app.store.Create(ctx, cmd.Session)
	if err != nil {
		return nil, err
	}
	if !cmd.PrepareExternal {
		return &CreateSessionResult{Session: session}, nil
	}

	prepareCtx := ctx
	cancel := func() {}
	if app.prepareTimeout > 0 {
		prepareCtx, cancel = context.WithTimeout(ctx, app.prepareTimeout)
	}
	prepared, prepareErr := app.runner.PrepareSession(prepareCtx, agentadapters.PrepareSessionRequest{
		SessionID:               session.ID,
		AdapterID:               session.AgentID,
		Workspace:               session.Workspace,
		PreviousNativeSessionID: session.NativeSessionID,
		ConfigOptions:           session.ConfigOptions,
		MCPServers:              session.MCPServers,
	})
	cancel()
	if prepareErr != nil {
		_ = app.store.Delete(context.Background(), session.ID)
		return &CreateSessionResult{Session: session}, ExternalPrepareError{Err: prepareErr}
	}

	sessionID := session.ID
	session, err = app.store.UpdateSession(ctx, session.ID, func(item *chat.Session) {
		item.DriverKind = prepared.DriverKind
		item.NativeSessionID = prepared.NativeSessionID
		item.AgentInfo = prepared.AgentInfo
		item.ConfigOptions = prepared.ConfigOptions
		chat.ApplyAvailableCommandsBootstrap(item, prepared.AvailableCommands, prepared.AvailableCommandsKnown)
	})
	if err != nil {
		app.cleanupExternalSession(sessionID)
		return &CreateSessionResult{Session: session}, err
	}
	return &CreateSessionResult{Session: session}, nil
}

func (app *Application) DeleteSession(ctx context.Context, cmd DeleteSessionCommand) error {
	if app == nil || app.store == nil {
		return ErrStoreNotConfigured
	}
	sessionID := strings.TrimSpace(cmd.SessionID)
	if sessionID == "" {
		return Validation(ErrSessionIDRequired)
	}
	if cmd.DeleteNative && app.runner != nil {
		_ = app.runner.DeleteSession(ctx, sessionID)
	}
	// Session stores delete idempotently. Keeping the transcript boundary first
	// prevents a failed transcript commit from leaving live messages that refer
	// to bodies already removed from the independent attachment store. Direct
	// delete retries and the project-delete orphan sweep finish partial cleanup.
	if err := app.store.Delete(ctx, sessionID); err != nil {
		return err
	}
	if app.attachments == nil {
		return nil
	}
	cleanupCtx, cancel := app.attachmentCleanupContext(ctx)
	defer cancel()
	if err := app.attachments.DeleteBySessionID(cleanupCtx, sessionID); err != nil {
		return &AttachmentSessionCleanupError{cause: err}
	}
	return nil
}

// SweepOrphanedAttachments removes bodies whose authoritative transcript is
// already gone without touching pending claims owned by live sessions. Project
// deletion calls this before listing project chats so a retry can repair a
// partial cross-store session delete.
func (app *Application) SweepOrphanedAttachments(ctx context.Context) error {
	if app == nil || app.store == nil {
		return ErrStoreNotConfigured
	}
	if app.attachments == nil {
		return nil
	}
	_, err := SweepOrphanedChatAttachments(ctx, app.store, app.attachments)
	return err
}

func (app *Application) CreateAttachment(ctx context.Context, cmd CreateAttachmentCommand) (chatattachments.StoredAttachment, error) {
	if app == nil || app.attachments == nil {
		return chatattachments.StoredAttachment{}, ErrAttachmentStoreMissing
	}
	cmd.Attachment.SessionID = strings.TrimSpace(cmd.Attachment.SessionID)
	cmd.Attachment.ID = strings.TrimSpace(cmd.Attachment.ID)
	if cmd.Attachment.SessionID == "" {
		return chatattachments.StoredAttachment{}, Validation(ErrSessionIDRequired)
	}
	if cmd.Attachment.ID == "" {
		return chatattachments.StoredAttachment{}, Validation(ErrAttachmentIDRequired)
	}
	_, err := app.GetSession(ctx, cmd.Attachment.SessionID)
	if err != nil {
		return chatattachments.StoredAttachment{}, err
	}
	created, err := app.attachments.Create(ctx, cmd.Attachment)
	if errors.Is(err, chatattachments.ErrDraftQuota) {
		return chatattachments.StoredAttachment{}, ErrAttachmentDraftQuota
	}
	if errors.Is(err, chatattachments.ErrSessionQuota) {
		return chatattachments.StoredAttachment{}, ErrAttachmentSessionQuota
	}
	if errors.Is(err, chatattachments.ErrTotalQuota) {
		var quota chatattachments.TotalQuotaError
		if errors.As(err, &quota) {
			return chatattachments.StoredAttachment{}, AttachmentTotalQuotaError{LimitBytes: quota.LimitBytes}
		}
		return chatattachments.StoredAttachment{}, ErrAttachmentTotalQuota
	}
	if err != nil {
		return chatattachments.StoredAttachment{}, err
	}
	_, ownerErr := app.GetSession(ctx, cmd.Attachment.SessionID)
	if ownerErr == nil {
		return created, nil
	}
	cleanupCtx, cancel := app.attachmentCleanupContext(ctx)
	cleanupErr := app.attachments.DeleteDraft(cleanupCtx, cmd.Attachment.SessionID, cmd.Attachment.ID)
	cancel()
	if errors.Is(cleanupErr, chatattachments.ErrNotFound) {
		cleanupErr = nil
	}
	if cleanupErr != nil {
		var cause error = ErrAttachmentNotFound
		if ownerErr != nil {
			cause = ownerErr
		}
		return chatattachments.StoredAttachment{}, &AttachmentRollbackError{cause: errors.Join(cause, cleanupErr)}
	}
	if ownerErr != nil {
		return chatattachments.StoredAttachment{}, ownerErr
	}
	return chatattachments.StoredAttachment{}, ErrAttachmentNotFound
}

func (app *Application) GetAttachment(ctx context.Context, cmd AttachmentCommand) (chatattachments.StoredAttachment, error) {
	if app == nil || app.attachments == nil {
		return chatattachments.StoredAttachment{}, ErrAttachmentStoreMissing
	}
	sessionID := strings.TrimSpace(cmd.SessionID)
	attachmentID := strings.TrimSpace(cmd.AttachmentID)
	if sessionID == "" {
		return chatattachments.StoredAttachment{}, Validation(ErrSessionIDRequired)
	}
	if attachmentID == "" {
		return chatattachments.StoredAttachment{}, Validation(ErrAttachmentIDRequired)
	}
	if err := app.requireLiveAttachmentOwner(ctx, sessionID); err != nil {
		return chatattachments.StoredAttachment{}, err
	}
	attachment, ok, err := app.attachments.Get(ctx, sessionID, attachmentID)
	if err != nil {
		return chatattachments.StoredAttachment{}, err
	}
	if !ok {
		return chatattachments.StoredAttachment{}, ErrAttachmentNotFound
	}
	if err := app.requireLiveAttachmentOwner(ctx, sessionID); err != nil {
		return chatattachments.StoredAttachment{}, err
	}
	return attachment, nil
}

func (app *Application) ClaimAttachments(ctx context.Context, ref chatattachments.ClaimRef) ([]chatattachments.StoredAttachment, error) {
	if len(ref.AttachmentIDs) == 0 {
		return nil, nil
	}
	if app == nil || app.attachments == nil {
		return nil, ErrAttachmentStoreMissing
	}
	if len(ref.AttachmentIDs) > MaxMessageAttachments {
		return nil, Validation(ErrTooManyAttachments)
	}
	ref.SessionID = strings.TrimSpace(ref.SessionID)
	ref.MessageID = strings.TrimSpace(ref.MessageID)
	if ref.MessageID == "" {
		return nil, Validation(ErrAttachmentMessageID)
	}
	_, err := app.GetSession(ctx, ref.SessionID)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return nil, ErrAttachmentNotFound
		}
		return nil, err
	}
	seen := make(map[string]struct{}, len(ref.AttachmentIDs))
	normalizedIDs := make([]string, 0, len(ref.AttachmentIDs))
	for _, rawID := range ref.AttachmentIDs {
		id := strings.TrimSpace(rawID)
		if id == "" {
			return nil, Validation(ErrAttachmentIDRequired)
		}
		if _, ok := seen[id]; ok {
			return nil, Validation(ErrDuplicateAttachmentID)
		}
		seen[id] = struct{}{}
		normalizedIDs = append(normalizedIDs, id)
	}
	ref.AttachmentIDs = normalizedIDs
	attachments, err := app.attachments.Claim(ctx, ref)
	switch {
	case errors.Is(err, chatattachments.ErrNotFound):
		return nil, ErrAttachmentNotFound
	case errors.Is(err, chatattachments.ErrNotDraft):
		return nil, ErrAttachmentInUse
	case err != nil:
		return nil, err
	}
	if err := app.requireLiveAttachmentOwner(ctx, ref.SessionID); err != nil {
		app.releaseAttachmentClaim(ctx, ref)
		return nil, err
	}
	remaining := MaxMessageAttachmentBytes
	for _, attachment := range attachments {
		size := int64(len(attachment.Data))
		if size > remaining {
			app.releaseAttachmentClaim(ctx, ref)
			return nil, Validation(ErrAttachmentMessageBytes)
		}
		remaining -= size
	}
	return attachments, nil
}

func (app *Application) releaseAttachmentClaim(ctx context.Context, ref chatattachments.ClaimRef) {
	cleanupCtx, cancel := app.attachmentCleanupContext(ctx)
	defer cancel()
	// A timeout leaves the token-fenced claim intact for startup reconciliation.
	// The admission error remains authoritative for the caller.
	_ = app.attachments.ResolveClaim(cleanupCtx, ref, chatattachments.ClaimReleased)
}

func (app *Application) attachmentCleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := app.attachmentCleanupTimeout
	if timeout <= 0 {
		timeout = defaultAttachmentCleanupTimeout
	}
	return context.WithTimeout(context.WithoutCancel(ctx), timeout)
}

func (app *Application) ResolveAttachmentClaim(ctx context.Context, ref chatattachments.ClaimRef, resolution chatattachments.ClaimResolution) error {
	if len(ref.AttachmentIDs) == 0 {
		return nil
	}
	if app == nil || app.attachments == nil {
		return ErrAttachmentStoreMissing
	}
	ref.SessionID = strings.TrimSpace(ref.SessionID)
	ref.MessageID = strings.TrimSpace(ref.MessageID)
	ref.AttachmentIDs = normalizedAttachmentIDs(ref.AttachmentIDs)
	err := app.attachments.ResolveClaim(ctx, ref, resolution)
	switch {
	case errors.Is(err, chatattachments.ErrNotFound):
		return ErrAttachmentNotFound
	case errors.Is(err, chatattachments.ErrNotClaimed), errors.Is(err, chatattachments.ErrClaimLost):
		return ErrAttachmentInUse
	default:
		return err
	}
}

func normalizedAttachmentIDs(ids []string) []string {
	normalized := make([]string, 0, len(ids))
	for _, id := range ids {
		normalized = append(normalized, strings.TrimSpace(id))
	}
	return normalized
}

func (app *Application) DeleteAttachment(ctx context.Context, cmd AttachmentCommand) error {
	if app == nil || app.attachments == nil {
		return ErrAttachmentStoreMissing
	}
	sessionID := strings.TrimSpace(cmd.SessionID)
	attachmentID := strings.TrimSpace(cmd.AttachmentID)
	if sessionID == "" {
		return Validation(ErrSessionIDRequired)
	}
	if attachmentID == "" {
		return Validation(ErrAttachmentIDRequired)
	}
	if err := app.requireLiveAttachmentOwner(ctx, sessionID); err != nil {
		return err
	}
	err := app.attachments.DeleteDraft(ctx, sessionID, attachmentID)
	switch {
	case errors.Is(err, chatattachments.ErrNotFound):
		return ErrAttachmentNotFound
	case errors.Is(err, chatattachments.ErrNotDraft):
		return ErrAttachmentInUse
	default:
		return err
	}
}

func (app *Application) requireLiveAttachmentOwner(ctx context.Context, sessionID string) error {
	_, err := app.GetSession(ctx, sessionID)
	if errors.Is(err, ErrSessionNotFound) {
		return ErrAttachmentNotFound
	}
	if err != nil {
		return err
	}
	return nil
}

func (app *Application) CloseNativeSession(ctx context.Context, cmd CloseNativeSessionCommand) (*CloseNativeSessionResult, error) {
	if app == nil || app.store == nil {
		return nil, ErrStoreNotConfigured
	}
	if app.runner != nil && !cmd.NativeAlreadyClosed {
		_ = app.runner.CloseSession(ctx, cmd.Session.ID)
	}
	session, err := app.store.UpdateSession(ctx, cmd.Session.ID, func(item *chat.Session) {
		item.DriverKind = ""
		item.NativeSessionID = ""
		item.AgentInfo = nil
		chat.ResetAvailableCommandsAuthority(item)
	})
	if err != nil {
		return nil, err
	}
	return &CloseNativeSessionResult{Session: session}, nil
}

func (app *Application) SetConfigOption(ctx context.Context, cmd SetConfigOptionCommand) (*SetConfigOptionResult, error) {
	if app == nil || app.store == nil {
		return nil, ErrStoreNotConfigured
	}
	if !isExternalSession(cmd.Session) {
		return nil, ErrExternalSessionOnly
	}
	if app.runner == nil {
		return nil, ErrRunnerNotConfigured
	}
	setReq, err := configOptionSetRequest(cmd.Session.ID, cmd.ConfigID, cmd.Value)
	if err != nil {
		return nil, Validation(err)
	}

	setCtx := ctx
	cancel := func() {}
	if app.configOptionTimeout > 0 {
		setCtx, cancel = context.WithTimeout(ctx, app.configOptionTimeout)
	}
	result, err := app.runner.SetSessionConfigOption(setCtx, setReq)
	cancel()
	if err != nil {
		allowStoredOption := errors.Is(err, agentadapters.ErrSessionNotActive) ||
			agentadapters.IsLaunchConfigOption(cmd.Session.AgentID, setReq.ConfigID)
		configOptions, updateErr := updateStoredConfigOption(
			seedLaunchConfigOptionForSet(cmd.Session.ConfigOptions, cmd.Session.AgentID, setReq),
			setReq,
			allowStoredOption,
		)
		if updateErr == nil {
			session, updateErr := app.store.UpdateSession(ctx, cmd.Session.ID, func(item *chat.Session) {
				item.ConfigOptions = configOptions
			})
			if updateErr != nil {
				return nil, updateErr
			}
			return &SetConfigOptionResult{Session: session}, nil
		}
		return nil, err
	}
	session, err := app.store.UpdateSession(ctx, cmd.Session.ID, func(item *chat.Session) {
		item.ConfigOptions = result.ConfigOptions
		chat.ApplyAvailableCommandsBootstrap(item, result.AvailableCommands, result.AvailableCommandsKnown)
	})
	if err != nil {
		return nil, err
	}
	return &SetConfigOptionResult{Session: session}, nil
}

func (app *Application) SetHecateSettings(ctx context.Context, cmd SetHecateSettingsCommand) (*SetHecateSettingsResult, error) {
	if app == nil || app.store == nil {
		return nil, ErrStoreNotConfigured
	}
	if isExternalSession(cmd.Session) {
		return nil, ErrHecateSessionOnly
	}
	if cmd.RTKEnabled == nil && cmd.WorkspaceMode == nil {
		return nil, ErrNoSettingsProvided
	}

	workspaceMode := chat.EffectiveWorkspaceMode(cmd.Session.WorkspaceMode)
	if cmd.WorkspaceMode != nil {
		var err error
		workspaceMode, err = NormalizeWorkspaceMode(*cmd.WorkspaceMode)
		if err != nil {
			return nil, err
		}
		if cmd.Session.TaskID != "" && workspaceMode != chat.EffectiveWorkspaceMode(cmd.Session.WorkspaceMode) {
			return nil, Conflict(ErrWorkspaceModeLocked)
		}
	}

	// Update the task row first, then the session row. The two writes
	// are NOT atomic because chat/task stores do not share a transaction
	// boundary today. Task-first keeps existing continuations aligned
	// with the executor's sandbox-arg construction.
	if cmd.RTKEnabled != nil && cmd.Session.TaskID != "" && app.taskStore != nil {
		task, found, err := app.taskStore.GetTask(ctx, cmd.Session.TaskID)
		if err != nil {
			return nil, err
		}
		if found {
			task.RTKEnabled = *cmd.RTKEnabled
			if _, err := app.taskStore.UpdateTask(ctx, task); err != nil {
				return nil, err
			}
		}
	}

	session, err := app.store.UpdateSession(ctx, cmd.Session.ID, func(item *chat.Session) {
		if cmd.RTKEnabled != nil {
			item.RTKEnabled = *cmd.RTKEnabled
		}
		if cmd.WorkspaceMode != nil {
			item.WorkspaceMode = workspaceMode
		}
	})
	if err != nil {
		return nil, err
	}
	return &SetHecateSettingsResult{Session: session}, nil
}

func (app *Application) AdmitMessage(cmd AdmitMessageCommand) (*AdmitMessageResult, error) {
	content := strings.TrimSpace(cmd.Content)
	if content == "" && cmd.AttachmentCount == 0 {
		return nil, Validation(ErrContentRequired)
	}
	now := cmd.Now
	if now.IsZero() {
		now = time.Now()
	}
	limits := cmd.Limits
	if limits.MaxTurnsPerSession > 0 && cmd.Session.TurnsUsed >= limits.MaxTurnsPerSession {
		return nil, MessageLimitError{
			Code:      "turns",
			Message:   fmt.Sprintf("session has reached the %d-turn limit; start a new session to continue", limits.MaxTurnsPerSession),
			Limit:     limits.MaxTurnsPerSession,
			TurnsUsed: cmd.Session.TurnsUsed,
		}
	}
	if limits.MaxSessionDuration > 0 && !cmd.Session.CreatedAt.IsZero() && now.Sub(cmd.Session.CreatedAt) >= limits.MaxSessionDuration {
		return nil, MessageLimitError{
			Code:      "duration",
			Message:   fmt.Sprintf("session has reached the %s wall-clock limit; start a new session to continue", limits.MaxSessionDuration),
			LimitMS:   limits.MaxSessionDuration.Milliseconds(),
			StartedAt: cmd.Session.CreatedAt,
			TurnsUsed: cmd.Session.TurnsUsed,
		}
	}
	if limits.IdleTimeout > 0 && !cmd.Session.UpdatedAt.IsZero() && now.Sub(cmd.Session.UpdatedAt) >= limits.IdleTimeout {
		return nil, MessageLimitError{
			Code:      "idle",
			Message:   fmt.Sprintf("session was idle for at least %s; start a new session to continue", limits.IdleTimeout),
			LimitMS:   limits.IdleTimeout.Milliseconds(),
			UpdatedAt: cmd.Session.UpdatedAt,
			TurnsUsed: cmd.Session.TurnsUsed,
		}
	}

	executionMode := normalizeExecutionMode(cmd.ExecutionMode, cmd.Session)
	switch executionMode {
	case chat.ExecutionModeHecateTask:
		if isExternalSession(cmd.Session) {
			return nil, ErrExternalCannotRunHecate
		}
	case chat.ExecutionModeExternalAgent:
		if !isExternalSession(cmd.Session) {
			return nil, ErrHecateCannotRunExternal
		}
	default:
		return nil, Validation(ErrExecutionModeInvalid)
	}

	toolsEnabled := true
	if cmd.ToolsEnabled != nil {
		toolsEnabled = *cmd.ToolsEnabled
	}
	if cmd.AttachmentCount < 0 || cmd.AttachmentCount > MaxMessageAttachments {
		return nil, Validation(ErrTooManyAttachments)
	}
	return &AdmitMessageResult{
		Content:       content,
		ExecutionMode: executionMode,
		ToolsEnabled:  toolsEnabled,
	}, nil
}

func ResolveMessageDispatch(session chat.Session, admission AdmitMessageResult, hecateToolsUnavailable bool) MessageDispatchPlan {
	toolsEnabled := admission.ToolsEnabled
	route := MessageDispatchHecateTask
	switch admission.ExecutionMode {
	case chat.ExecutionModeExternalAgent:
		route = MessageDispatchExternalAgent
	case chat.ExecutionModeHecateTask:
		if toolsEnabled && !isExternalSession(session) && hecateToolsUnavailable {
			toolsEnabled = false
		}
		if !toolsEnabled {
			route = MessageDispatchDirectModel
		}
	}
	return MessageDispatchPlan{
		Content:       admission.Content,
		ExecutionMode: admission.ExecutionMode,
		ToolsEnabled:  toolsEnabled,
		Route:         route,
	}
}

func (app *Application) cleanupExternalSession(sessionID string) {
	cleanupCtx := context.Background()
	cancel := func() {}
	if app.prepareTimeout > 0 {
		cleanupCtx, cancel = context.WithTimeout(cleanupCtx, app.prepareTimeout)
	}
	_ = app.runner.DeleteSession(cleanupCtx, sessionID)
	cancel()
	_ = app.store.Delete(context.Background(), sessionID)
}

func isExternalSession(session chat.Session) bool {
	return session.AgentID != "" && session.AgentID != chat.DefaultAgentID
}

func normalizeExecutionMode(mode string, session chat.Session) string {
	mode = strings.TrimSpace(mode)
	if mode != "" {
		return mode
	}
	if isExternalSession(session) {
		return chat.ExecutionModeExternalAgent
	}
	return chat.ExecutionModeHecateTask
}

func configOptionSetRequest(sessionID, configID string, rawValue any) (agentadapters.SetSessionConfigOptionRequest, error) {
	if strings.TrimSpace(sessionID) == "" {
		return agentadapters.SetSessionConfigOptionRequest{}, fmt.Errorf("agent chat session id is required")
	}
	configID = strings.TrimSpace(configID)
	if configID == "" {
		return agentadapters.SetSessionConfigOptionRequest{}, fmt.Errorf("config option id is required")
	}
	switch value := rawValue.(type) {
	case string:
		value = strings.TrimSpace(value)
		if value == "" {
			return agentadapters.SetSessionConfigOptionRequest{}, fmt.Errorf("value is required")
		}
		return agentadapters.SetSessionConfigOptionRequest{SessionID: sessionID, ConfigID: configID, Value: value}, nil
	case bool:
		return agentadapters.SetSessionConfigOptionRequest{SessionID: sessionID, ConfigID: configID, BoolValue: &value}, nil
	default:
		return agentadapters.SetSessionConfigOptionRequest{}, fmt.Errorf("value must be a string or boolean")
	}
}

func seedLaunchConfigOptionForSet(options []agentcontrols.ConfigOption, agentID string, req agentadapters.SetSessionConfigOptionRequest) []agentcontrols.ConfigOption {
	if req.BoolValue != nil {
		return options
	}
	seed, ok := agentadapters.LaunchConfigOptionForSet(agentID, req.ConfigID, req.Value)
	if !ok {
		return options
	}
	out := append([]agentcontrols.ConfigOption(nil), options...)
	for i := range out {
		if out[i].ID != req.ConfigID {
			continue
		}
		if out[i].Source == "" {
			out[i].Source = agentcontrols.ConfigOptionSourceLaunch
		}
		if out[i].Type == agentcontrols.ConfigOptionTypeSelect && !storedConfigOptionAllowsValue(out[i], req.Value) {
			out[i].Options = seed.Options
		}
		return out
	}
	return append(out, seed)
}

func updateStoredConfigOption(options []agentcontrols.ConfigOption, req agentadapters.SetSessionConfigOptionRequest, allowInactiveAdapterOption bool) ([]agentcontrols.ConfigOption, error) {
	out := append([]agentcontrols.ConfigOption(nil), options...)
	for i := range out {
		if out[i].ID != req.ConfigID {
			continue
		}
		if !allowInactiveAdapterOption && out[i].Source != agentcontrols.ConfigOptionSourceLaunch {
			return nil, fmt.Errorf("config option %q is not launch-managed", req.ConfigID)
		}
		switch {
		case req.BoolValue != nil:
			if out[i].Type != agentcontrols.ConfigOptionTypeBoolean {
				return nil, fmt.Errorf("config option %q is not boolean", req.ConfigID)
			}
			value := *req.BoolValue
			out[i].CurrentBool = &value
		default:
			value := strings.TrimSpace(req.Value)
			if value == "" {
				return nil, fmt.Errorf("value is required")
			}
			if out[i].Type == agentcontrols.ConfigOptionTypeSelect && !storedConfigOptionAllowsValue(out[i], value) {
				return nil, fmt.Errorf("value %q is not available for %s", value, out[i].Name)
			}
			out[i].CurrentValue = value
		}
		return out, nil
	}
	return nil, fmt.Errorf("config option %q not found", req.ConfigID)
}

func storedConfigOptionAllowsValue(option agentcontrols.ConfigOption, value string) bool {
	if len(option.Options) == 0 {
		return true
	}
	for _, candidate := range option.Options {
		if candidate.Value == value {
			return true
		}
	}
	return false
}
