package agentadapters

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// approvalTracer is the OTel tracer for the approval coordinator. The
// instrumentation name matches the Go module path so spans are easy
// to filter in operator dashboards.
var approvalTracer = otel.Tracer("github.com/hecate/agent-runtime/internal/agentadapters")

// ApprovalMode controls what the coordinator does with an incoming
// RequestPermission. The package default is ModeAuto, which preserves
// legacy behavior (auto-select the first allow option). Operator-facing
// runtime flips this default to ModePrompt — see internal/config.
//
// See docs/rfcs/external-adapter-approvals-v1.md.
type ApprovalMode string

const (
	// ModeAuto auto-resolves every approval with the first allow option.
	// Danger mode — preserved for batch / CI / local smoke and for
	// backward compat in tests that don't construct an approval store.
	ModeAuto ApprovalMode = "auto"

	// ModePrompt blocks waiting for an operator decision (or a matching
	// grant). The HTTP/SSE surface that operators interact with is
	// added in a follow-up slice; until that lands, prompt-mode
	// approvals time out at GATEWAY_AGENT_ADAPTER_APPROVAL_TIMEOUT and
	// resolve to a Cancelled outcome. Operators who need adapters to
	// keep working before the UI ships set GATEWAY_AGENT_ADAPTER_APPROVAL_MODE=auto.
	ModePrompt ApprovalMode = "prompt"

	// ModeDeny auto-rejects every approval. Audit / compliance
	// scenarios where adapter tool use must not proceed.
	ModeDeny ApprovalMode = "deny"
)

// ApprovalStatus is the lifecycle state of a single approval row.
type ApprovalStatus string

const (
	ApprovalStatusPending   ApprovalStatus = "pending"
	ApprovalStatusApproved  ApprovalStatus = "approved"
	ApprovalStatusDenied    ApprovalStatus = "denied"
	ApprovalStatusTimedOut  ApprovalStatus = "timed_out"
	ApprovalStatusCancelled ApprovalStatus = "cancelled"
)

// ApprovalScope is the breadth of an operator's "always" decision.
// `once` means no persistence; the others persist as a grant entry.
type ApprovalScope string

const (
	ApprovalScopeOnce          ApprovalScope = "once"
	ApprovalScopeSession       ApprovalScope = "session"
	ApprovalScopeWorkspaceTool ApprovalScope = "workspace_tool"
	ApprovalScopeAdapterTool   ApprovalScope = "adapter_tool"
)

// ApprovalDecision is the operator's high-level intent. Selected option
// is recorded separately so we don't lose adapter-named choices.
type ApprovalDecision string

const (
	ApprovalDecisionApprove ApprovalDecision = "approve"
	ApprovalDecisionDeny    ApprovalDecision = "deny"
)

// ApprovalResolutionPath labels how a pending approval got resolved.
// Telemetry uses this label to distinguish operator-driven decisions
// from grant-cache hits and from default-mode auto-resolutions.
type ApprovalResolutionPath string

const (
	PathOperator    ApprovalResolutionPath = "operator"
	PathGrant       ApprovalResolutionPath = "grant"
	PathDefaultMode ApprovalResolutionPath = "default_mode"
	PathTimeout     ApprovalResolutionPath = "timeout"
	// PathRequestCancelled labels approvals that resolved because
	// the request context was cancelled — session shutdown, adapter
	// teardown, HTTP context cancellation, or process stop. Distinct
	// from PathOperator (an explicit operator decision) so telemetry
	// and the SSE stream can distinguish "operator declined to act"
	// from "the request died under us."
	PathRequestCancelled ApprovalResolutionPath = "request_cancelled"
)

// ApprovalOption mirrors acp.PermissionOption in a stable wire shape we
// own. We keep the original adapter-supplied option_id verbatim so the
// resolution can route back to the exact ACP option the adapter expects.
type ApprovalOption struct {
	OptionID string `json:"option_id"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
}

// Approval is one recorded approval row. The shape mirrors the wire
// shape in the RFC; endpoints and persistence backends in the next
// slice serialize this directly.
type Approval struct {
	ID             string                 `json:"id"`
	SessionID      string                 `json:"session_id"`
	AdapterID      string                 `json:"adapter_id"`
	Workspace      string                 `json:"workspace,omitempty"`
	ToolKind       string                 `json:"tool_kind"`
	ToolName       string                 `json:"tool_name,omitempty"`
	Status         ApprovalStatus         `json:"status"`
	ACPPayload     json.RawMessage        `json:"acp_payload,omitempty"`
	ACPOptions     []ApprovalOption       `json:"acp_options"`
	ScopeChoices   []ApprovalScope        `json:"scope_choices,omitempty"`
	SelectedOption string                 `json:"selected_option,omitempty"`
	Scope          ApprovalScope          `json:"scope,omitempty"`
	Decision       ApprovalDecision       `json:"decision,omitempty"`
	Path           ApprovalResolutionPath `json:"path,omitempty"`
	DecisionNote   string                 `json:"decision_note,omitempty"`
	CreatedAt      time.Time              `json:"created_at"`
	ResolvedAt     *time.Time             `json:"resolved_at,omitempty"`
	ExpiresAt      time.Time              `json:"expires_at"`
}

// Grant is a persisted "always" decision. v1 stores grants only in
// memory; sqlite persistence + the GET/DELETE /v1/agent-chat/grants
// endpoints land in the next slice.
type Grant struct {
	ID        string           `json:"id"`
	Scope     ApprovalScope    `json:"scope"`
	AdapterID string           `json:"adapter_id"`
	ToolKind  string           `json:"tool_kind"`
	Workspace string           `json:"workspace,omitempty"`
	SessionID string           `json:"session_id,omitempty"`
	Decision  ApprovalDecision `json:"decision"`
	GrantedBy string           `json:"granted_by,omitempty"`
	GrantedAt time.Time        `json:"granted_at"`
	ExpiresAt *time.Time       `json:"expires_at,omitempty"`
}

// ApprovalStore is the persistence interface. Memory and SQLite
// backends both implement it. Backend selection is wired in
// cmd/hecate, keyed off GATEWAY_CHAT_SESSIONS_BACKEND.
type ApprovalStore interface {
	// CreateApproval persists a pending approval and returns the row
	// with its assigned ID + timestamps filled in.
	CreateApproval(ctx context.Context, a Approval) (Approval, error)

	// ResolveApproval transitions a pending approval to a terminal
	// status. Returns the updated row. Returns ErrApprovalNotFound
	// when id is unknown and ErrApprovalAlreadyResolved when the row
	// is already terminal — the second writer always loses the race.
	ResolveApproval(ctx context.Context, id string, status ApprovalStatus, decision ApprovalDecision, selectedOption string, scope ApprovalScope, path ApprovalResolutionPath, note string, resolvedAt time.Time) (Approval, error)

	// GetApproval fetches an approval by id.
	GetApproval(ctx context.Context, id string) (Approval, error)

	// ListApprovals returns approvals for a session, oldest-first.
	// status="" returns all statuses; otherwise filters.
	ListApprovals(ctx context.Context, sessionID string, status ApprovalStatus) ([]Approval, error)

	// CreateGrant persists an operator-authored "always allow / always
	// deny" grant. Used by the coordinator when scope > once.
	CreateGrant(ctx context.Context, g Grant) (Grant, error)

	// ListGrants returns grants matching the filter, newest-first.
	// Expired grants (ExpiresAt <= now) are excluded.
	ListGrants(ctx context.Context, filter GrantFilter, now time.Time) ([]Grant, error)

	// DeleteGrant removes a grant by id. Returns ErrApprovalNotFound
	// when the id is unknown so the HTTP layer can surface 404.
	DeleteGrant(ctx context.Context, id string) error

	// FindMatchingGrant returns the most-specific live grant for the
	// given (sessionID, workspace, adapterID, toolKind) tuple, or
	// (Grant{}, false). Lookup walks scopes session → workspace_tool →
	// adapter_tool, returning the first match. Expired grants are
	// ignored. The returned grant carries the operator's decision.
	FindMatchingGrant(ctx context.Context, sessionID, workspace, adapterID, toolKind string, now time.Time) (Grant, bool, error)
}

// ApprovalRetentionStore extends ApprovalStore with maintenance
// operations the retention worker calls. Memory and SQLite both
// satisfy it; the type assertion in the worker keeps the smaller
// ApprovalStore interface clean for the coordinator.
type ApprovalRetentionStore interface {
	ApprovalStore

	// PruneApprovals deletes resolved (non-pending) approval rows
	// older than maxAge OR beyond maxCount, whichever fires.
	// Pending rows are never pruned. Returns total deleted.
	PruneApprovals(ctx context.Context, now time.Time, maxAge time.Duration, maxCount int) (int64, error)

	// PruneExpiredGrants removes grants whose ExpiresAt is in the
	// past. Live grants (no expiry, or future expiry) are never
	// touched. Returns total deleted.
	PruneExpiredGrants(ctx context.Context, now time.Time) (int64, error)

	// ReconcilePending sweeps any pending approval rows from a prior
	// process and marks them status=timed_out, path=startup_reconcile.
	// Process-local waiters can't be resurrected; callers must invoke
	// this at startup before serving requests. Returns rows
	// reconciled.
	ReconcilePending(ctx context.Context, now time.Time) (int64, error)
}

// ErrApprovalNotFound is returned by ApprovalStore.GetApproval and
// ResolveApproval when the id doesn't match a known row.
var ErrApprovalNotFound = errors.New("approval not found")

// ErrApprovalAlreadyResolved is returned by ResolveApproval when the
// row is already in a terminal state. Resolutions are append-only
// from a state-machine perspective even though they're a single row.
var ErrApprovalAlreadyResolved = errors.New("approval already resolved")

// ErrAmbiguousOption is returned by Coordinator helpers when the operator's
// decision could match multiple ACP options and no explicit
// selected_option was provided. Surfaced as 409 by the HTTP handler in
// slice 1B.
var ErrAmbiguousOption = errors.New("ambiguous adapter option; selected_option required")

// MemoryApprovalStore is a goroutine-safe in-process ApprovalStore.
// All state lives in maps and is discarded on process exit. Suitable
// for tests, dev, and anyone running with
// GATEWAY_AGENT_CHAT_BACKEND=memory (the default).
type MemoryApprovalStore struct {
	mu        sync.Mutex
	approvals map[string]Approval
	grants    map[string]Grant
}

// NewMemoryApprovalStore returns an empty in-memory store.
func NewMemoryApprovalStore() *MemoryApprovalStore {
	return &MemoryApprovalStore{
		approvals: make(map[string]Approval),
		grants:    make(map[string]Grant),
	}
}

func (s *MemoryApprovalStore) CreateApproval(_ context.Context, a Approval) (Approval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a.ID == "" {
		a.ID = newApprovalID()
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	s.approvals[a.ID] = a
	return a, nil
}

func (s *MemoryApprovalStore) ResolveApproval(_ context.Context, id string, status ApprovalStatus, decision ApprovalDecision, selectedOption string, scope ApprovalScope, path ApprovalResolutionPath, note string, resolvedAt time.Time) (Approval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.approvals[id]
	if !ok {
		return Approval{}, ErrApprovalNotFound
	}
	if row.Status != ApprovalStatusPending {
		return row, ErrApprovalAlreadyResolved
	}
	if resolvedAt.IsZero() {
		resolvedAt = time.Now().UTC()
	}
	row.Status = status
	row.Decision = decision
	row.SelectedOption = selectedOption
	row.Scope = scope
	row.Path = path
	row.DecisionNote = note
	row.ResolvedAt = &resolvedAt
	s.approvals[id] = row
	return row, nil
}

func (s *MemoryApprovalStore) GetApproval(_ context.Context, id string) (Approval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.approvals[id]
	if !ok {
		return Approval{}, ErrApprovalNotFound
	}
	return row, nil
}

func (s *MemoryApprovalStore) ListApprovals(_ context.Context, sessionID string, status ApprovalStatus) ([]Approval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Approval, 0)
	for _, row := range s.approvals {
		if row.SessionID != sessionID {
			continue
		}
		if status != "" && row.Status != status {
			continue
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// CreateGrant inserts a grant row. Used by tests + the resolve handler
// (slice 1B) when an operator picks a scope broader than `once`.
func (s *MemoryApprovalStore) CreateGrant(_ context.Context, g Grant) (Grant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g.ID == "" {
		g.ID = newGrantID()
	}
	if g.GrantedAt.IsZero() {
		g.GrantedAt = time.Now().UTC()
	}
	s.grants[g.ID] = g
	return g, nil
}

func (s *MemoryApprovalStore) FindMatchingGrant(_ context.Context, sessionID, workspace, adapterID, toolKind string, now time.Time) (Grant, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Lookup walks scopes from most-specific to broadest, matching the
	// operator's mental model. Within a scope, the most recently
	// granted entry wins on ties (later overrides earlier intent).
	type candidate struct {
		grant Grant
		rank  int // lower = more specific
	}
	scopeRank := map[ApprovalScope]int{
		ApprovalScopeSession:       0,
		ApprovalScopeWorkspaceTool: 1,
		ApprovalScopeAdapterTool:   2,
	}
	best := candidate{rank: 99}
	bestSet := false
	for _, g := range s.grants {
		if g.AdapterID != adapterID {
			continue
		}
		if g.ToolKind != toolKind {
			continue
		}
		if g.ExpiresAt != nil && !g.ExpiresAt.After(now) {
			continue
		}
		switch g.Scope {
		case ApprovalScopeSession:
			if g.SessionID != sessionID {
				continue
			}
		case ApprovalScopeWorkspaceTool:
			if g.Workspace != workspace {
				continue
			}
		case ApprovalScopeAdapterTool:
			// no extra constraint
		default:
			continue
		}
		rank, ok := scopeRank[g.Scope]
		if !ok {
			continue
		}
		if !bestSet || rank < best.rank || (rank == best.rank && g.GrantedAt.After(best.grant.GrantedAt)) {
			best = candidate{grant: g, rank: rank}
			bestSet = true
		}
	}
	if !bestSet {
		return Grant{}, false, nil
	}
	return best.grant, true, nil
}

// CoordinatorOptions configure an ApprovalCoordinator.
type CoordinatorOptions struct {
	Mode    ApprovalMode
	Timeout time.Duration
	Store   ApprovalStore
	Logger  *slog.Logger
	// NowFunc is used by tests; defaults to time.Now.UTC.
	NowFunc func() time.Time
	// IDFunc generates approval ids; defaults to ULID-shaped hex.
	IDFunc func() string
	// Hooks are optional callbacks for telemetry. Zero values are no-ops.
	Hooks CoordinatorHooks
}

// CoordinatorHooks are optional callbacks invoked by the coordinator at
// well-defined lifecycle points. The coordinator is the single place that
// knows the (adapter, tool_kind, mode, path) tuple, so all telemetry
// instrumentation goes through here. Implementations live in the
// telemetry package; the coordinator package keeps no metric dependencies
// of its own.
type CoordinatorHooks struct {
	OnRequested func(approval Approval)
	OnResolved  func(approval Approval, durationMS int64)
	OnTimedOut  func(approval Approval, durationMS int64)
	// OnGrantCreated fires after a successful CreateGrant inside the
	// coordinator's resolve path (i.e. when the operator's decision
	// scope is broader than `once`). Used to drive the
	// `approval.grants_active` UpDownCounter; nil-safe.
	OnGrantCreated func(grant Grant)
	// OnGrantDeleted fires after a successful DeleteGrant. Symmetric
	// with OnGrantCreated; nil-safe.
	OnGrantDeleted func()
}

// ApprovalCoordinator applies the configured ApprovalMode to incoming
// ACP RequestPermission calls. It records every request, looks up
// matching grants, applies the mode default, and produces the ACP
// response that gets sent back to the adapter. In prompt mode, a
// blocked RequestPermission registers a process-local waiter that
// the operator's Resolve / Cancel call wakes via the wake() helper.
//
// Waiters are intentionally not persisted: a Hecate restart cannot
// resurrect an in-flight ACP RequestPermission, so any pending row
// found in storage on startup is invalid as far as live blocking
// goes — slice 1C will mark such rows as `timed_out` during the
// startup-reconcile pass that the SQLite backend introduces.
type ApprovalCoordinator struct {
	opts CoordinatorOptions

	mu      sync.Mutex
	waiters map[string]*approvalWaiter
}

// approvalWaiter is a process-local handoff for a blocked prompt-mode
// RequestPermission. The buffered channel lets wake() deliver without
// holding the coordinator lock; the receive in the prompt-mode select
// races against the timeout timer and ctx.Done().
type approvalWaiter struct {
	ch chan acp.RequestPermissionResponse
}

// NewApprovalCoordinator constructs a coordinator with the given options.
// Sensible defaults are filled in for empty fields.
func NewApprovalCoordinator(opts CoordinatorOptions) *ApprovalCoordinator {
	if opts.Mode == "" {
		opts.Mode = ModeAuto
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Minute
	}
	if opts.Store == nil {
		opts.Store = NewMemoryApprovalStore()
	}
	if opts.NowFunc == nil {
		opts.NowFunc = func() time.Time { return time.Now().UTC() }
	}
	if opts.IDFunc == nil {
		opts.IDFunc = newApprovalID
	}
	return &ApprovalCoordinator{opts: opts}
}

// Mode returns the configured mode (test introspection).
func (c *ApprovalCoordinator) Mode() ApprovalMode { return c.opts.Mode }

// Timeout returns the configured timeout.
func (c *ApprovalCoordinator) Timeout() time.Duration { return c.opts.Timeout }

// Store returns the configured store (test introspection).
func (c *ApprovalCoordinator) Store() ApprovalStore { return c.opts.Store }

// RecordingContext bundles the per-request context that the session
// manager carries about an in-flight ACP RequestPermission. Kept
// separate from CoordinatorOptions so the coordinator is reusable across
// many sessions without rebuilding.
type RecordingContext struct {
	SessionID string
	AdapterID string
	Workspace string
}

// RequestPermission records the incoming ACP RequestPermission, applies the configured
// mode, and returns the ACP response to send back to the adapter.
//
// In slice 1A:
//   - All modes record the approval row.
//   - ModeAuto resolves immediately by selecting the first allow option
//     (preserves legacy behavior).
//   - ModeDeny resolves immediately with the first reject option, or
//     Cancelled if none.
//   - ModePrompt records a pending approval and waits for operator
//     resolve/cancel through the HTTP API, the configured timeout, or
//     context cancellation.
func (c *ApprovalCoordinator) RequestPermission(ctx context.Context, recCtx RecordingContext, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	now := c.opts.NowFunc()
	options := normalizeACPOptions(params.Options)
	toolName := extractToolName(params.ToolCall)
	toolKind := extractToolKind(params.ToolCall)

	// agent_adapter.approval.request span — covers the full
	// coordinator decision (grant short-circuit, mode default, prompt-
	// mode wait). The path is set on span end via the resolution
	// branch the request takes.
	ctx, span := approvalTracer.Start(ctx, "agent_adapter.approval.request",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("hecate.agent_adapter.id", recCtx.AdapterID),
			attribute.String("hecate.agent_adapter.session_id", recCtx.SessionID),
			attribute.String("hecate.agent_adapter.tool_kind", toolKind),
			attribute.String("hecate.agent_adapter.approval.mode", string(c.opts.Mode)),
		),
	)
	var spanPath ApprovalResolutionPath
	defer func() {
		if spanPath != "" {
			span.SetAttributes(attribute.String("hecate.agent_adapter.approval.path", string(spanPath)))
		}
		span.End()
	}()

	rawPayload, _ := json.Marshal(params)

	row := Approval{
		ID:           c.opts.IDFunc(),
		SessionID:    recCtx.SessionID,
		AdapterID:    recCtx.AdapterID,
		Workspace:    recCtx.Workspace,
		ToolKind:     toolKind,
		ToolName:     toolName,
		Status:       ApprovalStatusPending,
		ACPPayload:   rawPayload,
		ACPOptions:   options,
		ScopeChoices: defaultScopeChoices(),
		CreatedAt:    now,
		ExpiresAt:    now.Add(c.opts.Timeout),
	}
	created, err := c.opts.Store.CreateApproval(ctx, row)
	if err != nil {
		// Storage failure shouldn't deadlock the adapter; degrade by
		// applying the configured mode without persistence and
		// surfacing the error in the log.
		c.logf("approval_store_create_failed", err, recCtx, toolKind)
		created = row
	}
	// 1) Look for a live grant. A match short-circuits regardless of mode.
	if grant, ok, gerr := c.opts.Store.FindMatchingGrant(ctx, recCtx.SessionID, recCtx.Workspace, recCtx.AdapterID, toolKind, now); gerr == nil && ok {
		if c.opts.Hooks.OnRequested != nil {
			c.opts.Hooks.OnRequested(created)
		}
		response, status, decision, selected := c.applyDecision(grant.Decision, options, false)
		resolved, rerr := c.resolveStore(ctx, created.ID, status, decision, selected, grant.Scope, PathGrant, "", now)
		if rerr == nil {
			_ = c.notifyResolved(resolved, now, nil)
		}
		spanPath = PathGrant
		return response, nil
	}

	// 2) Apply the configured mode default.
	switch c.opts.Mode {
	case ModeAuto:
		if c.opts.Hooks.OnRequested != nil {
			c.opts.Hooks.OnRequested(created)
		}
		response, status, decision, selected := c.applyDecision(ApprovalDecisionApprove, options, true)
		resolved, rerr := c.resolveStore(ctx, created.ID, status, decision, selected, ApprovalScopeOnce, PathDefaultMode, "", now)
		if rerr == nil {
			_ = c.notifyResolved(resolved, now, nil)
		}
		spanPath = PathDefaultMode
		return response, nil

	case ModeDeny:
		if c.opts.Hooks.OnRequested != nil {
			c.opts.Hooks.OnRequested(created)
		}
		response, status, decision, selected := c.applyDecision(ApprovalDecisionDeny, options, false)
		resolved, rerr := c.resolveStore(ctx, created.ID, status, decision, selected, ApprovalScopeOnce, PathDefaultMode, "", now)
		if rerr == nil {
			_ = c.notifyResolved(resolved, now, nil)
		}
		spanPath = PathDefaultMode
		return response, nil

	case ModePrompt:
		// Block until the operator resolves/cancels via the HTTP API
		// (which calls Resolve/Cancel and wakes the waiter), the
		// configured timeout fires, or the request context is
		// cancelled. The waiter is process-local; on Hecate restart
		// any pending row in storage is unrecoverable for live
		// blocking — slice 1C marks them timed_out at startup.
		w := c.registerWaiter(created.ID)
		defer c.unregisterWaiter(created.ID)
		if c.opts.Hooks.OnRequested != nil {
			c.opts.Hooks.OnRequested(created)
		}
		if refreshed, rerr := c.opts.Store.GetApproval(ctx, created.ID); rerr == nil && refreshed.Status != ApprovalStatusPending {
			spanPath = refreshed.Path
			return responseForResolvedApproval(refreshed), nil
		}
		timer := time.NewTimer(c.opts.Timeout)
		defer timer.Stop()
		select {
		case resp := <-w.ch:
			// Operator resolved (or cancelled) via the API. The store
			// row was already updated and the OnResolved hook fired
			// inside Resolve/Cancel; just return the operator's chosen
			// ACP response.
			spanPath = PathOperator
			return resp, nil
		case <-timer.C:
			// Timeout fired. The store transition is the source of
			// truth for the timeout-vs-operator-resolve race; if the
			// operator's HTTP Resolve already won, resolveStore returns
			// AlreadyResolved and we suppress the notify so frontends
			// see exactly one approval.resolved event for this row.
			resolvedAt := c.opts.NowFunc()
			resolved, rerr := c.resolveStore(ctx, created.ID, ApprovalStatusTimedOut, "", "", ApprovalScopeOnce, PathTimeout, "operator did not respond before approval timeout", resolvedAt)
			if rerr == nil {
				c.notifyTimedOut(resolved, now, resolvedAt)
			}
			spanPath = PathTimeout
			span.SetStatus(codes.Error, "approval prompt timed out")
			return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}}}, nil
		case <-ctx.Done():
			// Request context cancelled — adapter teardown, session
			// shutdown, HTTP context cancellation, process stop. Same
			// store-race rule: only the winner publishes the SSE/metric
			// event. Path is request_cancelled (not operator) so
			// telemetry distinguishes "operator declined to act" from
			// "the request died under us."
			resolvedAt := c.opts.NowFunc()
			resolved, rerr := c.resolveStore(ctx, created.ID, ApprovalStatusCancelled, "", "", ApprovalScopeOnce, PathRequestCancelled, "request context cancelled before resolution", resolvedAt)
			if rerr == nil {
				_ = c.notifyResolved(resolved, now, nil)
			}
			spanPath = PathRequestCancelled
			span.SetStatus(codes.Error, "request context cancelled")
			return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}}}, nil
		}

	default:
		// Unknown mode: fail closed.
		resolved, rerr := c.resolveStore(ctx, created.ID, ApprovalStatusCancelled, "", "", ApprovalScopeOnce, PathDefaultMode, "unknown approval mode "+string(c.opts.Mode), now)
		if rerr == nil {
			_ = c.notifyResolved(resolved, now, nil)
		}
		spanPath = PathDefaultMode
		span.SetStatus(codes.Error, "unknown approval mode")
		return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}}}, nil
	}
}

// applyDecision picks the appropriate ACP option for an approve/deny
// decision, returning the response, terminal status, and recorded
// selected_option id. allowCustomFallback is true only for explicit
// ModeAuto, where preserving the old "pick first option" behavior is
// intentional. Grants and deny paths stay strict: if Hecate cannot
// identify a normalized allow/reject option, it cancels instead of
// guessing what a custom adapter option means.
func (c *ApprovalCoordinator) applyDecision(decision ApprovalDecision, options []ApprovalOption, allowCustomFallback bool) (acp.RequestPermissionResponse, ApprovalStatus, ApprovalDecision, string) {
	switch decision {
	case ApprovalDecisionApprove:
		if opt, ok := pickOption(options, "allow_once", "allow_always"); ok {
			return acp.RequestPermissionResponse{
				Outcome: acp.RequestPermissionOutcome{
					Selected: &acp.RequestPermissionOutcomeSelected{OptionId: acp.PermissionOptionId(opt.OptionID)},
				},
			}, ApprovalStatusApproved, ApprovalDecisionApprove, opt.OptionID
		}
		if allowCustomFallback && len(options) > 0 {
			opt := options[0]
			return acp.RequestPermissionResponse{
				Outcome: acp.RequestPermissionOutcome{
					Selected: &acp.RequestPermissionOutcomeSelected{OptionId: acp.PermissionOptionId(opt.OptionID)},
				},
			}, ApprovalStatusApproved, ApprovalDecisionApprove, opt.OptionID
		}
		return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}}}, ApprovalStatusCancelled, ApprovalDecisionApprove, ""

	case ApprovalDecisionDeny:
		if opt, ok := pickOption(options, "reject_once", "reject_always"); ok {
			return acp.RequestPermissionResponse{
				Outcome: acp.RequestPermissionOutcome{
					Selected: &acp.RequestPermissionOutcomeSelected{OptionId: acp.PermissionOptionId(opt.OptionID)},
				},
			}, ApprovalStatusDenied, ApprovalDecisionDeny, opt.OptionID
		}
		// No deny option — the only honest answer is Cancelled. The
		// adapter sees the same cancel outcome it would on timeout.
		return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}}}, ApprovalStatusCancelled, ApprovalDecisionDeny, ""

	default:
		return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}}}, ApprovalStatusCancelled, "", ""
	}
}

func responseForResolvedApproval(row Approval) acp.RequestPermissionResponse {
	switch row.Status {
	case ApprovalStatusApproved, ApprovalStatusDenied:
		if row.SelectedOption != "" {
			return acp.RequestPermissionResponse{
				Outcome: acp.RequestPermissionOutcome{
					Selected: &acp.RequestPermissionOutcomeSelected{OptionId: acp.PermissionOptionId(row.SelectedOption)},
				},
			}
		}
	}
	return acp.RequestPermissionResponse{
		Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}},
	}
}

// resolveStore is the internal helper that flips a row in the store
// to a terminal state. Different from the public Resolve method (slice
// 1B) which validates an operator's resolve request, builds the ACP
// response, and persists a grant when scope is broader than `once`.
//
// Returns the loaded row and any error. ErrApprovalAlreadyResolved is
// the load-bearing case: callers must skip notify hooks (telemetry +
// SSE) when this fires, or the same approval would publish two
// terminal events. The race shape: prompt-mode timeout/ctx-cancel
// and operator HTTP Resolve are both writers; the store atomic
// UPDATE picks one winner, the loser sees AlreadyResolved here, and
// must NOT publish a second SSE/metric event for the same row.
func (c *ApprovalCoordinator) resolveStore(ctx context.Context, id string, status ApprovalStatus, decision ApprovalDecision, selected string, scope ApprovalScope, path ApprovalResolutionPath, note string, now time.Time) (Approval, error) {
	row, err := c.opts.Store.ResolveApproval(ctx, id, status, decision, selected, scope, path, note, now)
	if err != nil {
		c.logf("approval_store_resolve_failed", err, RecordingContext{SessionID: row.SessionID, AdapterID: row.AdapterID, Workspace: row.Workspace}, row.ToolKind)
	}
	return row, err
}

func (c *ApprovalCoordinator) notifyResolved(row Approval, now time.Time, _ error) error {
	if c.opts.Hooks.OnResolved != nil {
		var dur int64
		if !row.CreatedAt.IsZero() {
			dur = now.Sub(row.CreatedAt).Milliseconds()
		}
		c.opts.Hooks.OnResolved(row, dur)
	}
	return nil
}

func (c *ApprovalCoordinator) notifyTimedOut(row Approval, createdAt, resolvedAt time.Time) {
	if c.opts.Hooks.OnTimedOut == nil {
		return
	}
	var dur int64
	if !createdAt.IsZero() && !resolvedAt.IsZero() {
		dur = resolvedAt.Sub(createdAt).Milliseconds()
	}
	c.opts.Hooks.OnTimedOut(row, dur)
}

func (c *ApprovalCoordinator) logf(event string, err error, recCtx RecordingContext, toolKind string) {
	if c.opts.Logger == nil {
		return
	}
	c.opts.Logger.Warn(event,
		slog.String("error", err.Error()),
		slog.String("session_id", recCtx.SessionID),
		slog.String("adapter_id", recCtx.AdapterID),
		slog.String("tool_kind", toolKind),
	)
}

// pickOption searches options for the first entry whose Kind matches
// any of the given kinds (in priority order). Used to pick the "allow"
// or "reject" option from an ACP option list. Empty kinds list returns
// the first option.
func pickOption(options []ApprovalOption, kinds ...string) (ApprovalOption, bool) {
	for _, want := range kinds {
		for _, opt := range options {
			if strings.EqualFold(opt.Kind, want) {
				return opt, true
			}
		}
	}
	return ApprovalOption{}, false
}

// normalizeACPOptions converts the ACP SDK option list into our
// stable wire shape. The original option_id is preserved so the
// resolution can route back to the adapter's exact option.
func normalizeACPOptions(in []acp.PermissionOption) []ApprovalOption {
	out := make([]ApprovalOption, 0, len(in))
	for _, opt := range in {
		out = append(out, ApprovalOption{
			OptionID: string(opt.OptionId),
			Kind:     string(opt.Kind),
			Name:     opt.Name,
		})
	}
	return out
}

func defaultScopeChoices() []ApprovalScope {
	return []ApprovalScope{ApprovalScopeOnce, ApprovalScopeSession, ApprovalScopeWorkspaceTool, ApprovalScopeAdapterTool}
}

// newApprovalID returns a stable id for an approval row. Format:
// "appr_" + 24 hex chars (96 bits of randomness). Not a strict ULID
// for now — switching to ULID is a follow-up if/when we want lexical
// ordering at the storage layer.
func newApprovalID() string {
	return prefixedID("appr_")
}

func newGrantID() string {
	return prefixedID("grnt_")
}

func prefixedID(prefix string) string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand.Read returning an error is exceptional in modern Go;
		// fall back to a time-based id so the system stays alive.
		return fmt.Sprintf("%s%x", prefix, time.Now().UnixNano())
	}
	return prefix + hex.EncodeToString(b[:])
}

// ─── Waiter primitive ────────────────────────────────────────────────────────

// registerWaiter allocates a process-local waiter for a pending
// approval and stores it under the approval id. Caller must invoke
// unregisterWaiter via defer once the wait completes — leaking a
// waiter doesn't deadlock anything (wake is a no-op when no receiver
// is reading) but it keeps the entry in the map until the next
// register replaces it.
func (c *ApprovalCoordinator) registerWaiter(id string) *approvalWaiter {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.waiters == nil {
		c.waiters = make(map[string]*approvalWaiter)
	}
	w := &approvalWaiter{ch: make(chan acp.RequestPermissionResponse, 1)}
	c.waiters[id] = w
	return w
}

func (c *ApprovalCoordinator) unregisterWaiter(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.waiters, id)
}

// wakeWaiter delivers the operator's ACP response to a registered
// waiter and returns whether a waiter was found. If none is
// registered (e.g. the approval already timed out, or the request
// was for a non-prompt mode) wake is a no-op.
func (c *ApprovalCoordinator) wakeWaiter(id string, resp acp.RequestPermissionResponse) bool {
	c.mu.Lock()
	w, ok := c.waiters[id]
	c.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case w.ch <- resp:
		return true
	default:
		// Buffer full — only happens on a redundant wake; safe to drop.
		return false
	}
}

// ─── Operator-facing API ─────────────────────────────────────────────────────

// ResolveRequest is the input to ApprovalCoordinator.Resolve. The
// HTTP layer parses POST bodies into this shape; coordinator-level
// callers (tests, future programmatic users) build it directly.
type ResolveRequest struct {
	Decision       ApprovalDecision `json:"decision"`
	Scope          ApprovalScope    `json:"scope"`
	SelectedOption string           `json:"selected_option,omitempty"`
	Note           string           `json:"note,omitempty"`
	GrantedBy      string           `json:"granted_by,omitempty"`
}

// ErrUnknownOption is returned by Resolve when SelectedOption was
// supplied but doesn't match any option_id on the recorded approval.
// Surfaces as 400 invalid_request.
var ErrUnknownOption = errors.New("selected_option does not match any option recorded for this approval")

// ErrNoMatchingOption is returned by Resolve when the operator's
// decision has no matching ACP option family on the recorded request
// (e.g. operator says deny but the adapter offered no reject_*
// option). Surfaces as 409 conflict; caller may retry as Cancel.
var ErrNoMatchingOption = errors.New("no ACP option matches the requested decision")

// ErrInvalidDecision / ErrInvalidScope are returned for malformed
// resolve requests. Surface as 400 invalid_request.
var ErrInvalidDecision = errors.New("decision must be approve or deny")
var ErrInvalidScope = errors.New("scope must be once, session, workspace_tool, or adapter_tool")

// AmbiguousOptionError carries the candidate options when Resolve
// can't pick one unambiguously. The HTTP layer surfaces this as 409
// conflict with the option list in the body so the operator UI can
// re-render the choices.
type AmbiguousOptionError struct {
	Decision ApprovalDecision
	Options  []ApprovalOption
}

func (e *AmbiguousOptionError) Error() string {
	return fmt.Sprintf("multiple options match decision=%s; selected_option required", e.Decision)
}

// Resolve transitions a pending approval to approved/denied per the
// operator's decision, persists a grant when scope > once, and (when
// a prompt-mode RequestPermission is blocked on this id) wakes the
// waiter so the adapter receives the operator's chosen ACP option
// without further delay.
//
// Returns the resolved row. Errors:
//   - ErrApprovalNotFound — id doesn't match a known row
//   - ErrApprovalAlreadyResolved — row is already terminal
//   - ErrInvalidDecision / ErrInvalidScope — malformed input
//   - ErrUnknownOption — selected_option not in row's options
//   - ErrNoMatchingOption — decision has no matching option family
//   - *AmbiguousOptionError — multiple options match, no selected_option supplied
func (c *ApprovalCoordinator) Resolve(ctx context.Context, id string, req ResolveRequest) (Approval, error) {
	// agent_adapter.approval.resolve span — wraps the operator's
	// decision-application path. Attributes pin the (decision, scope)
	// tuple so dashboards can split resolutions by operator intent.
	ctx, span := approvalTracer.Start(ctx, "agent_adapter.approval.resolve",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("hecate.agent_adapter.approval.id", id),
			attribute.String("hecate.agent_adapter.approval.decision", string(req.Decision)),
			attribute.String("hecate.agent_adapter.approval.scope", string(req.Scope)),
		),
	)
	defer span.End()

	if req.Decision != ApprovalDecisionApprove && req.Decision != ApprovalDecisionDeny {
		span.SetStatus(codes.Error, "invalid decision")
		return Approval{}, ErrInvalidDecision
	}
	if !validScope(req.Scope) {
		span.SetStatus(codes.Error, "invalid scope")
		return Approval{}, ErrInvalidScope
	}
	row, err := c.opts.Store.GetApproval(ctx, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "approval not found")
		return Approval{}, err
	}
	if row.Status != ApprovalStatusPending {
		span.SetStatus(codes.Error, "already resolved")
		return row, ErrApprovalAlreadyResolved
	}
	span.SetAttributes(
		attribute.String("hecate.agent_adapter.id", row.AdapterID),
		attribute.String("hecate.agent_adapter.session_id", row.SessionID),
		attribute.String("hecate.agent_adapter.tool_kind", row.ToolKind),
	)

	response, selected, perr := c.pickOperatorOption(row.ACPOptions, req.Decision, req.SelectedOption)
	if perr != nil {
		span.RecordError(perr)
		span.SetStatus(codes.Error, "approval option resolution failed")
		return row, perr
	}

	now := c.opts.NowFunc()
	terminalStatus := ApprovalStatusApproved
	if req.Decision == ApprovalDecisionDeny {
		terminalStatus = ApprovalStatusDenied
	}
	resolved, rerr := c.opts.Store.ResolveApproval(ctx, id, terminalStatus, req.Decision, selected, req.Scope, PathOperator, req.Note, now)
	if rerr != nil {
		// AlreadyResolved race against timeout: surface to caller.
		span.RecordError(rerr)
		span.SetStatus(codes.Error, "approval resolve failed")
		return resolved, rerr
	}

	// Persist a grant when scope > once. Memory-only in slice 1B; sqlite
	// backend lands in slice 1C. Failure to persist a grant should not
	// block the operator's intent on this turn; we log and continue.
	if req.Scope != ApprovalScopeOnce {
		grant := Grant{
			Scope:     req.Scope,
			AdapterID: row.AdapterID,
			ToolKind:  row.ToolKind,
			Decision:  req.Decision,
			GrantedBy: defaultGrantedBy(req.GrantedBy),
			GrantedAt: now,
		}
		if req.Scope == ApprovalScopeWorkspaceTool {
			grant.Workspace = row.Workspace
		}
		if req.Scope == ApprovalScopeSession {
			grant.SessionID = row.SessionID
		}
		stored, gerr := c.opts.Store.CreateGrant(ctx, grant)
		if gerr != nil {
			c.logf("approval_grant_create_failed", gerr, RecordingContext{SessionID: row.SessionID, AdapterID: row.AdapterID, Workspace: row.Workspace}, row.ToolKind)
		} else if c.opts.Hooks.OnGrantCreated != nil {
			c.opts.Hooks.OnGrantCreated(stored)
		}
	}

	c.wakeWaiter(id, response)
	_ = c.notifyResolved(resolved, now, nil)
	return resolved, nil
}

// Cancel transitions a pending approval to cancelled (ACP Cancelled
// outcome). Different from a deny resolution: deny selects a reject
// option (telling the adapter the action is forbidden); cancel says
// "the operator declined to decide; back off and ask again later."
func (c *ApprovalCoordinator) Cancel(ctx context.Context, id string) (Approval, error) {
	row, err := c.opts.Store.GetApproval(ctx, id)
	if err != nil {
		return Approval{}, err
	}
	if row.Status != ApprovalStatusPending {
		return row, ErrApprovalAlreadyResolved
	}
	now := c.opts.NowFunc()
	resolved, rerr := c.opts.Store.ResolveApproval(ctx, id, ApprovalStatusCancelled, "", "", ApprovalScopeOnce, PathOperator, "operator cancelled", now)
	if rerr != nil {
		return resolved, rerr
	}
	c.wakeWaiter(id, acp.RequestPermissionResponse{
		Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}},
	})
	_ = c.notifyResolved(resolved, now, nil)
	return resolved, nil
}

// GetApproval is a thin store-backed accessor for the HTTP GET handler.
func (c *ApprovalCoordinator) GetApproval(ctx context.Context, id string) (Approval, error) {
	return c.opts.Store.GetApproval(ctx, id)
}

// ListApprovals proxies to the store. Empty status returns all rows.
func (c *ApprovalCoordinator) ListApprovals(ctx context.Context, sessionID string, status ApprovalStatus) ([]Approval, error) {
	return c.opts.Store.ListApprovals(ctx, sessionID, status)
}

// ListGrants returns grants matching the filter. Empty filter returns
// all live grants. Expired grants are excluded.
func (c *ApprovalCoordinator) ListGrants(ctx context.Context, filter GrantFilter) ([]Grant, error) {
	return c.opts.Store.ListGrants(ctx, filter, c.opts.NowFunc())
}

// DeleteGrant removes a grant by id.
func (c *ApprovalCoordinator) DeleteGrant(ctx context.Context, id string) error {
	if err := c.opts.Store.DeleteGrant(ctx, id); err != nil {
		return err
	}
	if c.opts.Hooks.OnGrantDeleted != nil {
		c.opts.Hooks.OnGrantDeleted()
	}
	return nil
}

// pickOperatorOption resolves the operator's decision to a concrete
// ACP option_id. Strict semantics:
//   - If selected_option is supplied, it must exist on the row.
//   - Otherwise, exactly one option of the matching kind family
//     (allow_* for approve, reject_* for deny) must exist; multiple
//     return *AmbiguousOptionError, none returns ErrNoMatchingOption.
//
// Returns the constructed ACP response and the recorded option_id.
func (c *ApprovalCoordinator) pickOperatorOption(options []ApprovalOption, decision ApprovalDecision, selectedOption string) (acp.RequestPermissionResponse, string, error) {
	if selectedOption != "" {
		for _, opt := range options {
			if opt.OptionID == selectedOption {
				return acp.RequestPermissionResponse{
					Outcome: acp.RequestPermissionOutcome{
						Selected: &acp.RequestPermissionOutcomeSelected{OptionId: acp.PermissionOptionId(opt.OptionID)},
					},
				}, opt.OptionID, nil
			}
		}
		return acp.RequestPermissionResponse{}, "", ErrUnknownOption
	}
	wanted := []string{"allow_once", "allow_always"}
	if decision == ApprovalDecisionDeny {
		wanted = []string{"reject_once", "reject_always"}
	}
	matches := optionsByKinds(options, wanted)
	switch len(matches) {
	case 0:
		return acp.RequestPermissionResponse{}, "", ErrNoMatchingOption
	case 1:
		opt := matches[0]
		return acp.RequestPermissionResponse{
			Outcome: acp.RequestPermissionOutcome{
				Selected: &acp.RequestPermissionOutcomeSelected{OptionId: acp.PermissionOptionId(opt.OptionID)},
			},
		}, opt.OptionID, nil
	default:
		return acp.RequestPermissionResponse{}, "", &AmbiguousOptionError{Decision: decision, Options: matches}
	}
}

func optionsByKinds(options []ApprovalOption, kinds []string) []ApprovalOption {
	out := make([]ApprovalOption, 0, 1)
	for _, opt := range options {
		for _, k := range kinds {
			if strings.EqualFold(opt.Kind, k) {
				out = append(out, opt)
				break
			}
		}
	}
	return out
}

func validScope(s ApprovalScope) bool {
	switch s {
	case ApprovalScopeOnce, ApprovalScopeSession, ApprovalScopeWorkspaceTool, ApprovalScopeAdapterTool:
		return true
	}
	return false
}

func defaultGrantedBy(s string) string {
	if s == "" {
		return "operator"
	}
	return s
}

// ─── Grant query helpers (memory-only in slice 1B) ───────────────────────────

// GrantFilter narrows ListGrants results.
type GrantFilter struct {
	AdapterID string
	Scope     ApprovalScope
	ToolKind  string
}

// ListGrants returns grants matching the filter. Expired grants are
// dropped using the supplied now timestamp.
func (s *MemoryApprovalStore) ListGrants(_ context.Context, filter GrantFilter, now time.Time) ([]Grant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Grant, 0, len(s.grants))
	for _, g := range s.grants {
		if g.ExpiresAt != nil && !g.ExpiresAt.After(now) {
			continue
		}
		if filter.AdapterID != "" && g.AdapterID != filter.AdapterID {
			continue
		}
		if filter.Scope != "" && g.Scope != filter.Scope {
			continue
		}
		if filter.ToolKind != "" && g.ToolKind != filter.ToolKind {
			continue
		}
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GrantedAt.After(out[j].GrantedAt) })
	return out, nil
}

// DeleteGrant removes a grant by id. Returns ErrApprovalNotFound (the
// shared not-found sentinel) when the id is unknown.
func (s *MemoryApprovalStore) DeleteGrant(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.grants[id]; !ok {
		return ErrApprovalNotFound
	}
	delete(s.grants, id)
	return nil
}

// PruneApprovals deletes resolved approval rows older than maxAge or
// beyond maxCount. Mirrors the SQLite store's behavior so the
// retention worker can dispatch through ApprovalRetentionStore
// without caring which backend is wired. Pending rows are never
// auto-pruned.
func (s *MemoryApprovalStore) PruneApprovals(_ context.Context, now time.Time, maxAge time.Duration, maxCount int) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var deleted int64
	if maxAge > 0 {
		cutoff := now.Add(-maxAge)
		for id, row := range s.approvals {
			if row.Status == ApprovalStatusPending {
				continue
			}
			if row.CreatedAt.Before(cutoff) {
				delete(s.approvals, id)
				deleted++
			}
		}
	}
	if maxCount > 0 {
		// Collect non-pending rows, sort newest-first, drop the tail.
		resolved := make([]Approval, 0, len(s.approvals))
		for _, row := range s.approvals {
			if row.Status != ApprovalStatusPending {
				resolved = append(resolved, row)
			}
		}
		sort.Slice(resolved, func(i, j int) bool { return resolved[i].CreatedAt.After(resolved[j].CreatedAt) })
		for i := maxCount; i < len(resolved); i++ {
			delete(s.approvals, resolved[i].ID)
			deleted++
		}
	}
	return deleted, nil
}

// PruneExpiredGrants removes grants whose ExpiresAt has passed. Live
// grants are never touched; the retention worker must not erase
// operator-authored intent.
func (s *MemoryApprovalStore) PruneExpiredGrants(_ context.Context, now time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var deleted int64
	for id, g := range s.grants {
		if g.ExpiresAt != nil && !g.ExpiresAt.After(now) {
			delete(s.grants, id)
			deleted++
		}
	}
	return deleted, nil
}

// ReconcilePending sweeps pending rows and marks them timed_out
// with path=startup_reconcile. The memory backend never has rows
// surviving a restart in practice (the map is process-local), but
// the method exists so memory and sqlite share the
// ApprovalRetentionStore surface. Returns 0 on a normal startup;
// non-zero only if the same process is restarted in-place (rare;
// e.g. tests).
func (s *MemoryApprovalStore) ReconcilePending(_ context.Context, now time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	const note = "process-local waiter lost on restart; reconciled at startup"
	var rec int64
	for id, row := range s.approvals {
		if row.Status != ApprovalStatusPending {
			continue
		}
		row.Status = ApprovalStatusTimedOut
		row.Path = ApprovalResolutionPath("startup_reconcile")
		row.DecisionNote = note
		t := now.UTC()
		row.ResolvedAt = &t
		s.approvals[id] = row
		rec++
	}
	return rec, nil
}
