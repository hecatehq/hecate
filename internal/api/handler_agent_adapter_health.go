package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/hecatehq/hecate/internal/agentadapters"
)

// AgentAdapterProbe is the function shape the handler calls to
// classify an adapter's health. Production wiring uses
// agentadapters.Probe directly; tests inject a fake to avoid spawning
// real adapter binaries.
type AgentAdapterProbe func(ctx context.Context, adapterID string) agentadapters.ProbeResult

// AgentAdapterLogout is the function shape the handler calls to ask an
// adapter to clear its own account/session state. Production wiring uses
// agentadapters.Logout directly; tests inject a fake to avoid spawning real
// adapter binaries.
type AgentAdapterLogout func(ctx context.Context, adapterID string) (agentadapters.LogoutResult, error)

// AgentAdapterAuthenticate is the function shape the handler calls to ask an
// adapter to run its own local login flow. Production wiring uses
// agentadapters.Authenticate directly; tests inject a fake to avoid spawning
// real adapter binaries.
type AgentAdapterAuthenticate func(ctx context.Context, adapterID string) (agentadapters.AuthenticateResult, error)

// SetAgentAdapterProbe overrides the probe used by HandleAgentAdapterHealth.
// Pass nil to restore the default (agentadapters.Probe). Test-only.
func (h *Handler) SetAgentAdapterProbe(p AgentAdapterProbe) {
	h.agentAdapterProbe = p
}

// SetAgentAdapterLogout overrides the logout call used by
// HandleAgentAdapterLogout. Pass nil to restore the default
// (agentadapters.Logout). Test-only.
func (h *Handler) SetAgentAdapterLogout(fn AgentAdapterLogout) {
	h.agentAdapterLogout = fn
}

// SetAgentAdapterAuthenticate overrides the authenticate call used by
// HandleAgentAdapterAuthenticate. Pass nil to restore the default
// (agentadapters.Authenticate). Test-only.
func (h *Handler) SetAgentAdapterAuthenticate(fn AgentAdapterAuthenticate) {
	h.agentAdapterAuthenticate = fn
}

// HandleAgentAdapterHealth probes a single adapter to confirm it can
// serve a chat turn today (binary on PATH, ACP handshake completes,
// session-create succeeds). The classification — `ready` /
// `not_installed` / `auth_required` / `error` — drives the operator
// UI's status chips and "why doesn't this work" diagnostics.
//
// GET /hecate/v1/agent-adapters/{id}/health
//
// Status codes:
//   - 200 OK with the typed ProbeResult on every classification,
//     including `not_installed` and `auth_required`. The probe
//     completing successfully is itself a 200 — the adapter's status
//     lives in the body, not the HTTP code.
//   - 400 invalid_request when {id} is empty.
//   - 404 not_found when {id} doesn't match any registered adapter.
func (h *Handler) HandleAgentAdapterHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "adapter id is required")
		return
	}
	if _, ok := agentadapters.FindAdapter(id); !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "adapter not found")
		return
	}

	result := h.probeAgentAdapter(ctx, id)
	WriteJSON(w, http.StatusOK, AgentAdapterHealthResponse{
		Object: "agent_adapter_health",
		Data:   result,
	})
}

func (h *Handler) probeAgentAdapter(ctx context.Context, id string) agentadapters.ProbeResult {
	if agentadapters.DevOverrideActive(id) {
		return agentadapters.Probe(ctx, id)
	}
	if h != nil && h.agentAdapterProbe != nil {
		return h.agentAdapterProbe(ctx, id)
	}
	return agentadapters.Probe(ctx, id)
}

func (h *Handler) logoutAgentAdapter(ctx context.Context, id string) (agentadapters.LogoutResult, error) {
	if h != nil && h.agentAdapterLogout != nil {
		return h.agentAdapterLogout(ctx, id)
	}
	return agentadapters.Logout(ctx, id)
}

func (h *Handler) authenticateAgentAdapter(ctx context.Context, id string) (agentadapters.AuthenticateResult, error) {
	if h != nil && h.agentAdapterAuthenticate != nil {
		return h.agentAdapterAuthenticate(ctx, id)
	}
	return agentadapters.Authenticate(ctx, id)
}

// AgentAdapterHealthResponse wraps the probe result. Object is the
// stable discriminator so generic clients can route on it the same
// way they route session / approval payloads.
type AgentAdapterHealthResponse struct {
	Object string                    `json:"object"`
	Data   agentadapters.ProbeResult `json:"data"`
}
