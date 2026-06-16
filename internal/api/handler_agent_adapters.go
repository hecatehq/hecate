package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/hecatehq/hecate/internal/agentadapters"
)

func (h *Handler) HandleAgentAdapters(w http.ResponseWriter, r *http.Request) {
	items := agentadapters.List(r.Context())
	data := make([]AgentAdapterResponseItem, 0, len(items))
	for _, item := range items {
		data = append(data, renderAgentAdapterItem(r.Context(), item))
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
	if _, ok := agentadapters.FindAdapter(id); !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "adapter not found")
		return
	}
	result := h.probeAgentAdapter(ctx, id)
	status, _ := agentadapters.StatusForAdapterAfterExplicitProbe(ctx, id, nil)
	item := renderAgentAdapterItem(ctx, status)
	if !agentadapters.DevOverrideActive(id) {
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
		Data:   []AgentAdapterResponseItem{renderAgentAdapterItem(r.Context(), status)},
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

func renderAgentAdapterItem(ctx context.Context, item agentadapters.Status) AgentAdapterResponseItem {
	rendered := AgentAdapterResponseItem{
		ID:                   item.ID,
		Name:                 item.Name,
		Kind:                 item.Kind,
		Command:              item.Command,
		Args:                 item.Args,
		Managed:              item.Managed.Package != "",
		ManagedPackage:       item.Managed.Package,
		Available:            item.Available,
		Status:               item.Status,
		Path:                 item.Path,
		Error:                item.Error,
		Description:          item.Description,
		CostMode:             item.CostMode,
		DocsURL:              item.DocsURL,
		AdapterVersion:       item.AdapterVersion,
		AgentVersion:         item.AgentVersion,
		SupportedRange:       item.SupportedRange,
		VersionOutsideRange:  item.VersionOutsideRange,
		AuthStatus:           item.AuthStatus,
		AuthError:            item.AuthError,
		CredentialModes:      renderAgentAdapterCredentialModes(item.CredentialModes),
		RemoteCredentialMode: item.RemoteCredentialMode,
		RemoteCredentialHint: item.RemoteCredentialHint,
		ConfigOptions:        agentadapters.LaunchConfigOptions(ctx, item),
	}
	if item.RemoteCredentialHint != "" || item.RemoteCredentialMode != "" {
		remoteCredentialOK := item.RemoteCredentialOK
		rendered.RemoteCredentialOK = &remoteCredentialOK
	}
	if item.ID == "claude_code" {
		rendered.ClaudeCodeCLI = &AgentAdapterSetupCommandStatusItem{
			Available:      item.ClaudeCodeCLI.Available,
			Command:        item.ClaudeCodeCLI.Command,
			ExecutablePath: item.ClaudeCodeCLI.ExecutablePath,
		}
	}
	return rendered
}

func renderAgentAdapterCredentialModes(modes []agentadapters.CredentialMode) []AgentAdapterCredentialModeItem {
	if len(modes) == 0 {
		return nil
	}
	out := make([]AgentAdapterCredentialModeItem, 0, len(modes))
	for _, mode := range modes {
		out = append(out, AgentAdapterCredentialModeItem{
			ID:            mode.ID,
			Name:          mode.Name,
			Description:   mode.Description,
			RemoteAllowed: mode.RemoteAllowed,
			EnvKeys:       append([]string(nil), mode.EnvKeys...),
		})
	}
	return out
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
