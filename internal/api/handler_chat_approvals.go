package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/hecate/agent-runtime/internal/agentadapters"
)

// HandleListChatApprovals lists approvals for a chat session.
// Filterable by status via ?status=pending. Returns oldest-first.
//
// GET /hecate/v1/chat/sessions/{id}/approvals[?status=pending]
func (h *Handler) HandleListChatApprovals(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sessionID := strings.TrimSpace(r.PathValue("id"))
	if sessionID == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "session id is required")
		return
	}
	if _, ok, err := h.agentChat.Get(ctx, sessionID); err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	} else if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "agent chat session not found")
		return
	}
	coord := h.agentApprovalCoordinator()
	if coord == nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, "approval coordinator not configured")
		return
	}
	status := agentadapters.ApprovalStatus(strings.TrimSpace(r.URL.Query().Get("status")))
	rows, err := coord.ListApprovals(ctx, sessionID, status)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	out := make([]agentApprovalItem, 0, len(rows))
	for _, row := range rows {
		out = append(out, renderAgentApproval(row))
	}
	WriteJSON(w, http.StatusOK, agentApprovalListResponse{Object: "list", Data: out})
}

// HandleGetChatApproval returns a single approval row.
//
// GET /hecate/v1/chat/sessions/{id}/approvals/{approval_id}
func (h *Handler) HandleGetChatApproval(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sessionID := strings.TrimSpace(r.PathValue("id"))
	if sessionID == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "session id is required")
		return
	}
	approvalID := strings.TrimSpace(r.PathValue("approval_id"))
	if approvalID == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "approval_id is required")
		return
	}
	coord := h.agentApprovalCoordinator()
	if coord == nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, "approval coordinator not configured")
		return
	}
	row, ok := h.loadAgentApprovalForSession(ctx, w, coord, sessionID, approvalID)
	if !ok {
		return
	}
	WriteJSON(w, http.StatusOK, agentApprovalResponse{Object: "chat_approval", Data: renderAgentApproval(row)})
}

// HandleResolveChatApproval applies an operator decision to a
// pending approval. Body: ResolveAgentApprovalRequest.
//
// Status codes:
//   - 200 OK with resolved row
//   - 400 invalid_request: malformed body, unknown decision/scope, unknown selected_option
//   - 404 not_found: unknown approval id
//   - 409 conflict: already_resolved | ambiguous_option | no_matching_option
//
// POST /hecate/v1/chat/sessions/{id}/approvals/{approval_id}/resolve
func (h *Handler) HandleResolveChatApproval(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sessionID := strings.TrimSpace(r.PathValue("id"))
	if sessionID == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "session id is required")
		return
	}
	approvalID := strings.TrimSpace(r.PathValue("approval_id"))
	if approvalID == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "approval_id is required")
		return
	}
	coord := h.agentApprovalCoordinator()
	if coord == nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, "approval coordinator not configured")
		return
	}
	if _, ok := h.loadAgentApprovalForSession(ctx, w, coord, sessionID, approvalID); !ok {
		return
	}
	var req ResolveAgentApprovalRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resolved, err := coord.Resolve(ctx, approvalID, agentadapters.ResolveRequest{
		Decision:       agentadapters.ApprovalDecision(strings.TrimSpace(req.Decision)),
		Scope:          agentadapters.ApprovalScope(strings.TrimSpace(req.Scope)),
		SelectedOption: strings.TrimSpace(req.SelectedOption),
		Note:           strings.TrimSpace(req.Note),
		GrantedBy:      "operator",
	})
	if err != nil {
		writeApprovalError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, agentApprovalResponse{Object: "chat_approval", Data: renderAgentApproval(resolved)})
}

// HandleCancelChatApproval cancels a pending approval (operator
// declines to decide; ACP Cancelled outcome).
//
// POST /hecate/v1/chat/sessions/{id}/approvals/{approval_id}/cancel
func (h *Handler) HandleCancelChatApproval(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sessionID := strings.TrimSpace(r.PathValue("id"))
	if sessionID == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "session id is required")
		return
	}
	approvalID := strings.TrimSpace(r.PathValue("approval_id"))
	if approvalID == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "approval_id is required")
		return
	}
	coord := h.agentApprovalCoordinator()
	if coord == nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, "approval coordinator not configured")
		return
	}
	if _, ok := h.loadAgentApprovalForSession(ctx, w, coord, sessionID, approvalID); !ok {
		return
	}
	resolved, err := coord.Cancel(ctx, approvalID)
	if err != nil {
		writeApprovalError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, agentApprovalResponse{Object: "chat_approval", Data: renderAgentApproval(resolved)})
}

// HandleListChatGrants returns persisted "always allow / always
// deny" grants. Filterable by adapter_id and scope.
//
// GET /hecate/v1/chat/grants[?adapter_id=&scope=]
func (h *Handler) HandleListChatGrants(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	coord := h.agentApprovalCoordinator()
	if coord == nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, "approval coordinator not configured")
		return
	}
	filter := agentadapters.GrantFilter{
		AdapterID: strings.TrimSpace(r.URL.Query().Get("adapter_id")),
		Scope:     agentadapters.ApprovalScope(strings.TrimSpace(r.URL.Query().Get("scope"))),
		ToolKind:  strings.TrimSpace(r.URL.Query().Get("tool_kind")),
	}
	grants, err := coord.ListGrants(ctx, filter)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	out := make([]agentGrantItem, 0, len(grants))
	for _, g := range grants {
		out = append(out, renderAgentGrant(g))
	}
	WriteJSON(w, http.StatusOK, agentGrantListResponse{Object: "list", Data: out})
}

// HandleDeleteChatGrant revokes a grant by id.
//
// DELETE /hecate/v1/chat/grants/{grant_id}
func (h *Handler) HandleDeleteChatGrant(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	grantID := strings.TrimSpace(r.PathValue("grant_id"))
	if grantID == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "grant_id is required")
		return
	}
	coord := h.agentApprovalCoordinator()
	if coord == nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, "approval coordinator not configured")
		return
	}
	if err := coord.DeleteGrant(ctx, grantID); err != nil {
		if errors.Is(err, agentadapters.ErrApprovalNotFound) {
			WriteError(w, http.StatusNotFound, errCodeNotFound, "grant not found")
			return
		}
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// agentApprovalCoordinator returns the approval coordinator installed
// on the session manager. Nil indicates the runtime was constructed
// without one (programmer error in NewHandler) — endpoints surface a
// 500 in that case.
func (h *Handler) agentApprovalCoordinator() *agentadapters.ApprovalCoordinator {
	if h.agentChatRunner == nil {
		return nil
	}
	mgr, ok := h.agentChatRunner.(*agentadapters.SessionManager)
	if !ok {
		return nil
	}
	return mgr.Coordinator()
}

func (h *Handler) loadAgentApprovalForSession(ctx context.Context, w http.ResponseWriter, coord *agentadapters.ApprovalCoordinator, sessionID, approvalID string) (agentadapters.Approval, bool) {
	row, err := coord.GetApproval(ctx, approvalID)
	if err != nil {
		if errors.Is(err, agentadapters.ErrApprovalNotFound) {
			WriteError(w, http.StatusNotFound, errCodeNotFound, "approval not found")
			return agentadapters.Approval{}, false
		}
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return agentadapters.Approval{}, false
	}
	if row.SessionID != sessionID {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "approval not found")
		return agentadapters.Approval{}, false
	}
	return row, true
}

// writeApprovalError translates coordinator-level errors into HTTP
// status codes. Errors that don't match a known sentinel become 500.
func writeApprovalError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, agentadapters.ErrApprovalNotFound):
		WriteError(w, http.StatusNotFound, errCodeNotFound, "approval not found")
	case errors.Is(err, agentadapters.ErrApprovalAlreadyResolved):
		WriteError(w, http.StatusConflict, errCodeConflict, "approval is already resolved")
	case errors.Is(err, agentadapters.ErrInvalidDecision):
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
	case errors.Is(err, agentadapters.ErrInvalidScope):
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
	case errors.Is(err, agentadapters.ErrUnknownOption):
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
	case errors.Is(err, agentadapters.ErrNoMatchingOption):
		WriteError(w, http.StatusConflict, errCodeConflict, err.Error())
	default:
		var ambiguous *agentadapters.AmbiguousOptionError
		if errors.As(err, &ambiguous) {
			WriteJSON(w, http.StatusConflict, map[string]any{
				"error": map[string]any{
					"type":    errCodeConflict,
					"message": ambiguous.Error(),
					"options": renderAmbiguousOptions(ambiguous.Options),
				},
			})
			return
		}
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
	}
}

// ─── Wire shapes ─────────────────────────────────────────────────────────────

// ResolveAgentApprovalRequest is the JSON body of POST /resolve.
type ResolveAgentApprovalRequest struct {
	Decision       string `json:"decision"`
	Scope          string `json:"scope"`
	SelectedOption string `json:"selected_option,omitempty"`
	Note           string `json:"note,omitempty"`
}

type agentApprovalListResponse struct {
	Object string              `json:"object"`
	Data   []agentApprovalItem `json:"data"`
}

type agentApprovalResponse struct {
	Object string            `json:"object"`
	Data   agentApprovalItem `json:"data"`
}

type agentApprovalItem struct {
	ID             string                    `json:"id"`
	SessionID      string                    `json:"session_id"`
	AdapterID      string                    `json:"adapter_id"`
	Workspace      string                    `json:"workspace,omitempty"`
	ToolKind       string                    `json:"tool_kind"`
	ToolName       string                    `json:"tool_name,omitempty"`
	Status         string                    `json:"status"`
	Options        []agentApprovalOptionItem `json:"acp_options"`
	ScopeChoices   []string                  `json:"scope_choices,omitempty"`
	SelectedOption string                    `json:"selected_option,omitempty"`
	Scope          string                    `json:"scope,omitempty"`
	Decision       string                    `json:"decision,omitempty"`
	Path           string                    `json:"path,omitempty"`
	DecisionNote   string                    `json:"decision_note,omitempty"`
	CreatedAt      time.Time                 `json:"created_at"`
	ResolvedAt     *time.Time                `json:"resolved_at,omitempty"`
	ExpiresAt      time.Time                 `json:"expires_at"`
}

type agentApprovalOptionItem struct {
	OptionID string `json:"option_id"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
}

type agentGrantListResponse struct {
	Object string           `json:"object"`
	Data   []agentGrantItem `json:"data"`
}

type agentGrantItem struct {
	ID        string     `json:"id"`
	Scope     string     `json:"scope"`
	AdapterID string     `json:"adapter_id"`
	ToolKind  string     `json:"tool_kind"`
	Workspace string     `json:"workspace,omitempty"`
	SessionID string     `json:"session_id,omitempty"`
	Decision  string     `json:"decision"`
	GrantedBy string     `json:"granted_by,omitempty"`
	GrantedAt time.Time  `json:"granted_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

func renderAgentApproval(a agentadapters.Approval) agentApprovalItem {
	options := make([]agentApprovalOptionItem, 0, len(a.ACPOptions))
	for _, opt := range a.ACPOptions {
		options = append(options, agentApprovalOptionItem{OptionID: opt.OptionID, Kind: opt.Kind, Name: opt.Name})
	}
	scopes := make([]string, 0, len(a.ScopeChoices))
	for _, s := range a.ScopeChoices {
		scopes = append(scopes, string(s))
	}
	return agentApprovalItem{
		ID:             a.ID,
		SessionID:      a.SessionID,
		AdapterID:      a.AdapterID,
		Workspace:      a.Workspace,
		ToolKind:       a.ToolKind,
		ToolName:       a.ToolName,
		Status:         string(a.Status),
		Options:        options,
		ScopeChoices:   scopes,
		SelectedOption: a.SelectedOption,
		Scope:          string(a.Scope),
		Decision:       string(a.Decision),
		Path:           string(a.Path),
		DecisionNote:   a.DecisionNote,
		CreatedAt:      a.CreatedAt,
		ResolvedAt:     a.ResolvedAt,
		ExpiresAt:      a.ExpiresAt,
	}
}

func renderAgentGrant(g agentadapters.Grant) agentGrantItem {
	return agentGrantItem{
		ID:        g.ID,
		Scope:     string(g.Scope),
		AdapterID: g.AdapterID,
		ToolKind:  g.ToolKind,
		Workspace: g.Workspace,
		SessionID: g.SessionID,
		Decision:  string(g.Decision),
		GrantedBy: g.GrantedBy,
		GrantedAt: g.GrantedAt,
		ExpiresAt: g.ExpiresAt,
	}
}

func renderAmbiguousOptions(opts []agentadapters.ApprovalOption) []agentApprovalOptionItem {
	out := make([]agentApprovalOptionItem, 0, len(opts))
	for _, opt := range opts {
		out = append(out, agentApprovalOptionItem{OptionID: opt.OptionID, Kind: opt.Kind, Name: opt.Name})
	}
	return out
}
