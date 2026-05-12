package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/hecate/agent-runtime/internal/agentadapters"
)

func (h *Handler) HandleAgentAdapters(w http.ResponseWriter, r *http.Request) {
	items := agentadapters.List(r.Context())
	data := make([]AgentAdapterResponseItem, 0, len(items))
	for _, item := range items {
		data = append(data, h.renderAgentAdapterItem(r.Context(), item))
	}

	WriteJSON(w, http.StatusOK, AgentAdapterResponse{
		Object: "agent_adapters",
		Data:   data,
	})
}

func (h *Handler) HandleAgentAdapterProbe(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "adapter id is required")
		return
	}
	status, ok := agentadapters.StatusForAdapter(ctx, id, nil)
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "adapter not found")
		return
	}
	result := h.probeAgentAdapter(ctx, id, h.agentAdapterCredentialEnv(ctx, id))
	item := h.renderAgentAdapterItem(ctx, status)
	if id == "claude_code" && result.Status == agentadapters.ProbeStatusReady && !item.CredentialConfigured && status.AuthStatus != agentadapters.AuthStatusOK {
		// Claude Code can complete the ACP handshake with a normal CLI login,
		// but chat turns still require an adapter-visible credential in
		// Hecate's environment. Do not let a bare ready probe erase the
		// onboarding state.
		item.AuthStatus, item.AuthError = status.AuthStatus, status.AuthError
	} else {
		item.AuthStatus, item.AuthError = authStatusFromProbe(result, item.AuthStatus, item.AuthError)
	}
	WriteJSON(w, http.StatusOK, AgentAdapterProbeResponse{
		Object: "agent_adapter_probe",
		Data: AgentAdapterProbeData{
			Adapter: item,
			Health:  result,
		},
	})
}

func (h *Handler) HandleAgentAdapterRefreshLauncher(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "adapter id is required")
		return
	}
	status, err := agentadapters.RefreshManagedLauncher(r.Context(), id, nil)
	if err != nil {
		if _, ok := agentadapters.FindAdapter(id); !ok {
			WriteError(w, http.StatusNotFound, errCodeNotFound, "adapter not found")
			return
		}
		WriteError(w, http.StatusConflict, errCodeConflict, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, AgentAdapterResponse{
		Object: "agent_adapters",
		Data:   []AgentAdapterResponseItem{h.renderAgentAdapterItem(r.Context(), status)},
	})
}

type AgentAdapterProbeResponse struct {
	Object string                `json:"object"`
	Data   AgentAdapterProbeData `json:"data"`
}

type AgentAdapterProbeData struct {
	Adapter AgentAdapterResponseItem  `json:"adapter"`
	Health  agentadapters.ProbeResult `json:"health"`
}

func renderAgentAdapterItem(item agentadapters.Status) AgentAdapterResponseItem {
	rendered := AgentAdapterResponseItem{
		ID:                  item.ID,
		Name:                item.Name,
		Kind:                item.Kind,
		Command:             item.Command,
		Args:                item.Args,
		Managed:             item.Managed.Package != "",
		ManagedPackage:      item.Managed.Package,
		Available:           item.Available,
		Status:              item.Status,
		Path:                item.Path,
		Error:               item.Error,
		Description:         item.Description,
		CostMode:            item.CostMode,
		DocsURL:             item.DocsURL,
		Version:             item.Version,
		SupportedRange:      item.SupportedRange,
		VersionOutsideRange: item.VersionOutsideRange,
		AuthStatus:          item.AuthStatus,
		AuthError:           item.AuthError,
	}
	if item.ID == "claude_code" {
		rendered.ClaudeCodeCLI = &AgentAdapterSetupCommandStatusItem{
			Available: item.ClaudeCodeCLI.Available,
			Path:      item.ClaudeCodeCLI.Path,
		}
	}
	return rendered
}

func (h *Handler) renderAgentAdapterItem(ctx context.Context, item agentadapters.Status) AgentAdapterResponseItem {
	rendered := renderAgentAdapterItem(item)
	if h == nil || h.controlPlane == nil {
		return rendered
	}
	state, err := h.controlPlane.Snapshot(ctx)
	if err != nil {
		return rendered
	}
	for _, credential := range state.AgentAdapterCredentials {
		if credential.AdapterID == item.ID {
			rendered.CredentialConfigured = true
			rendered.CredentialPreview = credential.ValuePreview
			break
		}
	}
	return rendered
}

func authStatusFromProbe(result agentadapters.ProbeResult, fallbackStatus, fallbackError string) (string, string) {
	switch result.Status {
	case agentadapters.ProbeStatusReady:
		return agentadapters.AuthStatusOK, ""
	case agentadapters.ProbeStatusAuthRequired:
		return agentadapters.AuthStatusUnauthenticated, firstNonEmptyString(result.Hint, result.Error, fallbackError)
	case agentadapters.ProbeStatusError:
		if strings.Contains(strings.ToLower(result.Error+"\n"+result.Stderr), "credit balance") {
			return agentadapters.AuthStatusBilling, firstNonEmptyString(result.Hint, result.Error, fallbackError)
		}
	}
	return firstNonEmptyString(fallbackStatus, agentadapters.AuthStatusUnknown), fallbackError
}
