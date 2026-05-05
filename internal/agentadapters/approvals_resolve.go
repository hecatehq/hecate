package agentadapters

import (
	"context"
	"errors"
	"fmt"
	"strings"

	acp "github.com/coder/acp-go-sdk"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

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

	// Persist a grant when scope > once. Failure to persist a grant
	// should not block the operator's intent on this turn; we log and
	// continue.
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
//   - If selected_option is supplied, it must exist on the row and
//     match the selected decision family.
//   - Otherwise, exactly one option of the matching kind family
//     (allow_* for approve, reject_* for deny) must exist; multiple
//     return *AmbiguousOptionError, none returns ErrNoMatchingOption.
//
// Returns the constructed ACP response and the recorded option_id.
func (c *ApprovalCoordinator) pickOperatorOption(options []ApprovalOption, decision ApprovalDecision, selectedOption string) (acp.RequestPermissionResponse, string, error) {
	wanted := []string{"allow_once", "allow_always"}
	if decision == ApprovalDecisionDeny {
		wanted = []string{"reject_once", "reject_always"}
	}
	if selectedOption != "" {
		for _, opt := range options {
			if opt.OptionID == selectedOption {
				if !optionKindIn(opt.Kind, wanted) {
					return acp.RequestPermissionResponse{}, "", ErrNoMatchingOption
				}
				return acp.RequestPermissionResponse{
					Outcome: acp.RequestPermissionOutcome{
						Selected: &acp.RequestPermissionOutcomeSelected{OptionId: acp.PermissionOptionId(opt.OptionID)},
					},
				}, opt.OptionID, nil
			}
		}
		return acp.RequestPermissionResponse{}, "", ErrUnknownOption
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

func optionKindIn(got string, allowed []string) bool {
	for _, want := range allowed {
		if got == want {
			return true
		}
	}
	return false
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
