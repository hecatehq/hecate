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
)

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

// ApprovalStore is the persistence interface. Slice 1A ships an
// in-memory implementation; slice 1B adds sqlite.
type ApprovalStore interface {
	// CreateApproval persists a pending approval and returns the row
	// with its assigned ID + timestamps filled in.
	CreateApproval(ctx context.Context, a Approval) (Approval, error)

	// ResolveApproval transitions a pending approval to a terminal
	// status. Returns the updated row.
	ResolveApproval(ctx context.Context, id string, status ApprovalStatus, decision ApprovalDecision, selectedOption string, scope ApprovalScope, path ApprovalResolutionPath, note string, resolvedAt time.Time) (Approval, error)

	// GetApproval fetches an approval by id.
	GetApproval(ctx context.Context, id string) (Approval, error)

	// ListApprovals returns approvals for a session, oldest-first.
	// status="" returns all statuses; otherwise filters.
	ListApprovals(ctx context.Context, sessionID string, status ApprovalStatus) ([]Approval, error)

	// FindMatchingGrant returns the most-specific live grant for the
	// given (sessionID, workspace, adapterID, toolKind) tuple, or
	// (Grant{}, false). Lookup walks scopes session → workspace_tool →
	// adapter_tool, returning the first match. Expired grants are
	// ignored. The returned grant carries the operator's decision.
	FindMatchingGrant(ctx context.Context, sessionID, workspace, adapterID, toolKind string, now time.Time) (Grant, bool, error)
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
		resolved := c.resolveStore(ctx, created.ID, status, decision, selected, grant.Scope, PathGrant, "", now)
		return response, c.notifyResolved(resolved, now, nil)
	}

	// 2) Apply the configured mode default.
	switch c.opts.Mode {
	case ModeAuto:
		if c.opts.Hooks.OnRequested != nil {
			c.opts.Hooks.OnRequested(created)
		}
		response, status, decision, selected := c.applyDecision(ApprovalDecisionApprove, options, true)
		resolved := c.resolveStore(ctx, created.ID, status, decision, selected, ApprovalScopeOnce, PathDefaultMode, "", now)
		return response, c.notifyResolved(resolved, now, nil)

	case ModeDeny:
		if c.opts.Hooks.OnRequested != nil {
			c.opts.Hooks.OnRequested(created)
		}
		response, status, decision, selected := c.applyDecision(ApprovalDecisionDeny, options, false)
		resolved := c.resolveStore(ctx, created.ID, status, decision, selected, ApprovalScopeOnce, PathDefaultMode, "", now)
		return response, c.notifyResolved(resolved, now, nil)

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
			return resp, nil
		case <-timer.C:
			resolvedAt := c.opts.NowFunc()
			resolved := c.resolveStore(ctx, created.ID, ApprovalStatusTimedOut, "", "", ApprovalScopeOnce, PathTimeout, "operator did not respond before approval timeout", resolvedAt)
			c.notifyTimedOut(resolved, now, resolvedAt)
			return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}}}, nil
		case <-ctx.Done():
			resolvedAt := c.opts.NowFunc()
			resolved := c.resolveStore(ctx, created.ID, ApprovalStatusCancelled, "", "", ApprovalScopeOnce, PathOperator, "request context cancelled before resolution", resolvedAt)
			_ = c.notifyResolved(resolved, now, nil)
			return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}}}, nil
		}

	default:
		// Unknown mode: fail closed.
		resolved := c.resolveStore(ctx, created.ID, ApprovalStatusCancelled, "", "", ApprovalScopeOnce, PathDefaultMode, "unknown approval mode "+string(c.opts.Mode), now)
		_ = c.notifyResolved(resolved, now, nil)
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
func (c *ApprovalCoordinator) resolveStore(ctx context.Context, id string, status ApprovalStatus, decision ApprovalDecision, selected string, scope ApprovalScope, path ApprovalResolutionPath, note string, now time.Time) Approval {
	row, err := c.opts.Store.ResolveApproval(ctx, id, status, decision, selected, scope, path, note, now)
	if err != nil {
		c.logf("approval_store_resolve_failed", err, RecordingContext{SessionID: row.SessionID, AdapterID: row.AdapterID, Workspace: row.Workspace}, row.ToolKind)
	}
	return row
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
	if req.Decision != ApprovalDecisionApprove && req.Decision != ApprovalDecisionDeny {
		return Approval{}, ErrInvalidDecision
	}
	if !validScope(req.Scope) {
		return Approval{}, ErrInvalidScope
	}
	row, err := c.opts.Store.GetApproval(ctx, id)
	if err != nil {
		return Approval{}, err
	}
	if row.Status != ApprovalStatusPending {
		return row, ErrApprovalAlreadyResolved
	}

	response, selected, perr := c.pickOperatorOption(row.ACPOptions, req.Decision, req.SelectedOption)
	if perr != nil {
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
		if memStore, ok := c.opts.Store.(*MemoryApprovalStore); ok {
			if _, gerr := memStore.CreateGrant(ctx, grant); gerr != nil {
				c.logf("approval_grant_create_failed", gerr, RecordingContext{SessionID: row.SessionID, AdapterID: row.AdapterID, Workspace: row.Workspace}, row.ToolKind)
			}
		} else {
			c.logf("approval_grant_unsupported_backend", fmt.Errorf("store does not implement CreateGrant"), RecordingContext{SessionID: row.SessionID, AdapterID: row.AdapterID, Workspace: row.Workspace}, row.ToolKind)
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

// ListGrants returns grants matching the filter (memory-only in
// slice 1B). Empty filter returns all live grants. Expired grants
// are excluded.
func (c *ApprovalCoordinator) ListGrants(ctx context.Context, filter GrantFilter) ([]Grant, error) {
	memStore, ok := c.opts.Store.(*MemoryApprovalStore)
	if !ok {
		return nil, fmt.Errorf("store does not support grant listing")
	}
	return memStore.ListGrants(ctx, filter, c.opts.NowFunc())
}

// DeleteGrant removes a grant by id (memory-only in slice 1B).
func (c *ApprovalCoordinator) DeleteGrant(ctx context.Context, id string) error {
	memStore, ok := c.opts.Store.(*MemoryApprovalStore)
	if !ok {
		return fmt.Errorf("store does not support grant deletion")
	}
	return memStore.DeleteGrant(ctx, id)
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
