// Package agentadapters: approvals.go declares the wire types,
// constants, store interfaces, and shared sentinel errors for the
// adapter approval system. The coordinator implementation lives in
// approvals_coordinator.go; the operator-facing Resolve/Cancel/list
// API lives in approvals_resolve.go; ID helpers live in approvals_ids.go;
// the in-memory store lives in approvals_memory.go; the SQLite-backed
// store lives in approvals_sqlite.go.
package agentadapters

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"go.opentelemetry.io/otel"
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
	// grant). If no operator resolves the request before
	// GATEWAY_AGENT_ADAPTER_APPROVAL_TIMEOUT, it resolves to a Cancelled
	// outcome. Operators who need fully unattended adapters can set
	// GATEWAY_AGENT_ADAPTER_APPROVAL_MODE=auto.
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
// shape in the RFC; endpoints and persistence backends serialize this
// directly.
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

// Grant is a persisted "always" decision. Memory and SQLite backends
// both implement it.
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

// GrantFilter narrows ListGrants results.
type GrantFilter struct {
	AdapterID string
	Scope     ApprovalScope
	ToolKind  string
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

	// Prune implements retention.Pruner — one call that runs both
	// the resolved-approval sweep (subject to maxAge / maxCount)
	// and the expired-grant sweep (grants honor only ExpiresAt;
	// maxAge / maxCount don't apply). Returns the sum so operators
	// see total rows removed by this subsystem in one number.
	// Captures `now := time.Now().UTC()` internally; the
	// per-deletion `now`-tolerant methods stay on the interface for
	// tests and ad-hoc callers.
	Prune(ctx context.Context, maxAge time.Duration, maxCount int) (int, error)
}

func pruneApprovalsAndGrants(ctx context.Context, store ApprovalRetentionStore, maxAge time.Duration, maxCount int) (int, error) {
	now := time.Now().UTC()
	approvals, err := store.PruneApprovals(ctx, now, maxAge, maxCount)
	if err != nil {
		return int(approvals), err
	}
	grants, err := store.PruneExpiredGrants(ctx, now)
	if err != nil {
		return int(approvals + grants), err
	}
	return int(approvals + grants), nil
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
// selected_option was provided. Surfaced as 409 by the HTTP handler.
var ErrAmbiguousOption = errors.New("ambiguous adapter option; selected_option required")
