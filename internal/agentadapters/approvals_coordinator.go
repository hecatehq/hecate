package agentadapters

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

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
// goes — the SQLite backend's startup reconcile pass marks such rows
// as `timed_out`.
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
// Behavior by mode:
//   - All modes record the approval row.
//   - ModeAuto resolves immediately by selecting the first allow option
//     (the original auto-approve behavior, opt-in only).
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
		// blocking — the SQLite backend marks them timed_out at startup.
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
// to a terminal state. Different from the public Resolve method which
// validates an operator's resolve request, builds the ACP response,
// and persists a grant when scope is broader than `once`.
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
