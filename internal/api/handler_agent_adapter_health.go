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

// SetAgentAdapterProbe overrides the probe used by the explicit POST endpoint.
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

// HandleAgentAdapterHealth returns passive discovery state for compatibility.
// It intentionally does not execute the discovered app. Chat creation resolves
// it again and prepares the real ACP session; an embedded bridge may defer the
// prompt-serving vendor invocation and auth result until the first message,
// although session setup may run bounded provider discovery. POST
// /agent-adapters/{id}/probe remains an optional disposable diagnostic.
//
// GET /hecate/v1/agent-adapters/{id}/health
//
// Status codes:
//   - 200 OK with `unverified`, `not_installed`, or `auth_required` passive
//     state. The adapter's status lives in the body, not the HTTP code.
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

	status, _ := agentadapters.CatalogStatusForAdapter(ctx, id, nil)
	result := passiveAgentAdapterHealth(status)
	WriteJSON(w, http.StatusOK, AgentAdapterHealthResponse{
		Object: "agent_adapter_health",
		Data:   result,
	})
}

func passiveAgentAdapterHealth(status agentadapters.Status) agentadapters.ProbeResult {
	result := agentadapters.ProbeResult{
		AdapterID: status.ID,
		Stage:     agentadapters.ProbeStageLookup,
		Path:      status.Path,
		Error:     status.Error,
	}
	if status.Available {
		result.Status = agentadapters.ProbeStatusUnverified
		result.Error = ""
		result.Hint = "App found. New chat re-resolves it and prepares a fresh ACP session; the first message verifies any deferred prompt-serving vendor invocation and authentication. POST to the probe endpoint only for optional diagnostics."
		return result
	}
	if status.AuthStatus == agentadapters.AuthStatusUnauthenticated {
		result.Status = agentadapters.ProbeStatusAuthRequired
		result.Hint = status.AuthError
		return result
	}
	result.Status = agentadapters.ProbeStatusNotInstalled
	result.Hint = "Install the external agent app in a recognized location or make its command available on PATH."
	return result
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
