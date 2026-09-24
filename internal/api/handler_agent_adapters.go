package api

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/remoteruntime"
)

func (h *Handler) HandleAgentAdapters(w http.ResponseWriter, r *http.Request) {
	items := agentadapters.ListCatalog(r.Context())
	data := make([]AgentAdapterResponseItem, 0, len(items))
	for _, item := range items {
		data = append(data, h.renderAgentAdapterCatalogItem(r.Context(), item))
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
	if h.agentAdapterProbe == nil && !agentadapters.DevOverrideActive(id) {
		if !h.requireAgentAdapterExecutableTrust(w, ctx, id) {
			return
		}
		ctx = agentadapters.WithExecutableTrust(ctx, h.executableTrust, id)
	}
	result := h.probeAgentAdapter(ctx, id)
	if writeAgentExecutableTrustError(w, result.Cause) {
		return
	}
	status, _ := agentadapters.StatusForAdapterAfterExplicitProbe(ctx, id, nil)
	status = agentadapters.ApplyProbeCapabilities(status, result)
	item := h.renderAgentAdapterItem(ctx, status)
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

func (h *Handler) HandleAgentAdapterLogout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "adapter id is required")
		return
	}
	adapter, ok := agentadapters.FindAdapter(id)
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "adapter not found")
		return
	}
	if h.agentAdapterLogout == nil {
		if !h.requireAgentAdapterExecutableTrust(w, ctx, id) {
			return
		}
		ctx = agentadapters.WithExecutableTrust(ctx, h.executableTrust, id)
	}
	result, err := h.logoutAgentAdapter(ctx, id)
	if err != nil {
		if writeAgentExecutableTrustError(w, err) {
			return
		}
		WriteError(w, http.StatusBadGateway, errCodeAgentAdapterUnavailable, agentadapters.NormalizeError(adapter.Name, err))
		return
	}
	WriteJSON(w, http.StatusOK, AgentAdapterLogoutResponse{
		Object: "agent_adapter_logout",
		Data:   result,
	})
}

func (h *Handler) HandleAgentAdapterAuthenticate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "adapter id is required")
		return
	}
	adapter, ok := agentadapters.FindAdapter(id)
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "adapter not found")
		return
	}
	if h.agentAdapterAuthenticate == nil {
		if !h.requireAgentAdapterExecutableTrust(w, ctx, id) {
			return
		}
		ctx = agentadapters.WithExecutableTrust(ctx, h.executableTrust, id)
	}
	result, err := h.authenticateAgentAdapter(ctx, id)
	if err != nil {
		if writeAgentExecutableTrustError(w, err) {
			return
		}
		WriteError(w, http.StatusBadGateway, errCodeAgentAdapterUnavailable, agentadapters.NormalizeError(adapter.Name, err))
		return
	}
	WriteJSON(w, http.StatusOK, AgentAdapterAuthenticateResponse{
		Object: "agent_adapter_authenticate",
		Data:   result,
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

type AgentAdapterLogoutResponse struct {
	Object string                     `json:"object"`
	Data   agentadapters.LogoutResult `json:"data"`
}

type AgentAdapterAuthenticateResponse struct {
	Object string                           `json:"object"`
	Data   agentadapters.AuthenticateResult `json:"data"`
}

func (h *Handler) renderAgentAdapterItem(ctx context.Context, item agentadapters.Status) AgentAdapterResponseItem {
	return h.renderAgentAdapterItemWithOptions(ctx, item, true)
}

func (h *Handler) renderAgentAdapterCatalogItem(ctx context.Context, item agentadapters.Status) AgentAdapterResponseItem {
	return h.renderAgentAdapterItemWithOptions(ctx, item, false)
}

func (h *Handler) renderAgentAdapterItemWithOptions(ctx context.Context, item agentadapters.Status, includeConfigOptions bool) AgentAdapterResponseItem {
	rendered := AgentAdapterResponseItem{
		ID:                   item.ID,
		Name:                 item.Name,
		Kind:                 item.Kind,
		Command:              item.Command,
		Args:                 item.Args,
		Embedded:             item.Embedded,
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
		SupportsAuthenticate: item.SupportsAuthenticate,
		SupportsLogout:       item.SupportsLogout,
		AuthStatus:           item.AuthStatus,
		AuthError:            item.AuthError,
		CredentialModes:      renderAgentAdapterCredentialModes(item.CredentialModes),
		RemoteCredentialMode: item.RemoteCredentialMode,
		RemoteCredentialHint: item.RemoteCredentialHint,
		Capabilities:         renderAgentAdapterCapabilities(item.Capabilities),
	}
	if h != nil && h.executableTrust != nil {
		trust := h.executableTrust.InspectPath(ctx, item.ID, item.Path)
		rendered.ExecutableTrust = &trust
	}
	if includeConfigOptions {
		rendered.ConfigOptions = agentadapters.LaunchConfigOptions(ctx, item)
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

type AgentAdapterExecutableTrustRequest struct {
	ExpectedIdentity string `json:"expected_identity"`
}

type AgentAdapterExecutableTrustResponse struct {
	Object string                              `json:"object"`
	Data   agentadapters.ExecutableTrustStatus `json:"data"`
}

func (h *Handler) HandleApproveAgentAdapterExecutable(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "adapter id is required")
		return
	}
	if _, ok := agentadapters.FindAdapter(id); !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "adapter not found")
		return
	}
	var request AgentAdapterExecutableTrustRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	request.ExpectedIdentity = strings.TrimSpace(request.ExpectedIdentity)
	if request.ExpectedIdentity == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "expected_identity is required")
		return
	}
	digest := strings.TrimPrefix(request.ExpectedIdentity, "sha256:")
	if len(request.ExpectedIdentity) != len("sha256:")+64 ||
		!strings.HasPrefix(request.ExpectedIdentity, "sha256:") ||
		strings.ToLower(digest) != digest {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "expected_identity must be a Hecate executable identity token")
		return
	}
	if _, err := hex.DecodeString(digest); err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "expected_identity must be a Hecate executable identity token")
		return
	}
	if h == nil || h.executableTrust == nil {
		WriteError(w, http.StatusServiceUnavailable, errCodeAgentExecutableIdentityUnavailable, "Executable approval is unavailable on this runtime.")
		return
	}
	if _, err := h.executableTrust.Approve(r.Context(), id, request.ExpectedIdentity, executableTrustApprovedBy(r)); err != nil {
		if errors.Is(err, agentadapters.ErrExecutableTrustConflict) {
			WriteError(w, http.StatusConflict, errCodeAgentExecutableIdentityChanged, "The app changed before approval. Review its current identity and try again.")
			return
		}
		WriteError(w, http.StatusServiceUnavailable, errCodeAgentExecutableIdentityUnavailable, "Hecate could not verify the app identity. Check the installation and try again.")
		return
	}
	status, _ := agentadapters.CatalogStatusForAdapter(r.Context(), id, nil)
	trust := h.executableTrust.InspectPath(r.Context(), id, status.Path)
	WriteJSON(w, http.StatusOK, AgentAdapterExecutableTrustResponse{Object: "agent_adapter_executable_trust", Data: trust})
}

func (h *Handler) HandleRevokeAgentAdapterExecutable(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "adapter id is required")
		return
	}
	if _, ok := agentadapters.FindAdapter(id); !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "adapter not found")
		return
	}
	if h == nil || h.executableTrust == nil {
		WriteError(w, http.StatusServiceUnavailable, errCodeAgentExecutableIdentityUnavailable, "Executable approval is unavailable on this runtime.")
		return
	}
	if err := h.executableTrust.Revoke(r.Context(), id); err != nil {
		WriteError(w, http.StatusServiceUnavailable, errCodeAgentExecutableIdentityUnavailable, "Hecate could not revoke the app approval. Try again.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func executableTrustApprovedBy(r *http.Request) string {
	// Authentication identifies an operator today, but the local API does not
	// yet expose a stable user principal. Keep the audit value honest rather
	// than persisting an address or bearer credential. Remote-runtime requests
	// do carry a verified actor identity, so retain it in the approval audit.
	return remoteruntime.ActorForAudit(r.Context(), "operator")
}

func (h *Handler) requireAgentAdapterExecutableTrust(w http.ResponseWriter, ctx context.Context, adapterID string) bool {
	if h == nil || h.executableTrust == nil {
		WriteError(w, http.StatusServiceUnavailable, errCodeAgentExecutableIdentityUnavailable, "Executable approval is unavailable on this runtime.")
		return false
	}
	status := h.executableTrust.InspectAdapter(ctx, adapterID)
	switch status.State {
	case agentadapters.ExecutableTrustStateApproved:
		return true
	case agentadapters.ExecutableTrustStateUnapproved:
		WriteError(w, http.StatusConflict, errCodeAgentExecutableTrustRequired, "Approve this app in Connections before Hecate runs it.")
	case agentadapters.ExecutableTrustStateChanged:
		WriteError(w, http.StatusConflict, errCodeAgentExecutableIdentityChanged, "This app changed after approval. Review and approve its current identity before Hecate runs it.")
	default:
		WriteError(w, http.StatusServiceUnavailable, errCodeAgentExecutableIdentityUnavailable, "Hecate could not verify the app identity. Check the installation and try again.")
	}
	return false
}

func writeAgentExecutableTrustError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, agentadapters.ErrExecutableTrustRequired):
		WriteError(w, http.StatusConflict, errCodeAgentExecutableTrustRequired, "Approve this app in Connections before Hecate runs it.")
	case errors.Is(err, agentadapters.ErrExecutableIdentityChanged), errors.Is(err, agentadapters.ErrExecutableTrustConflict):
		WriteError(w, http.StatusConflict, errCodeAgentExecutableIdentityChanged, "This app changed after approval. Review and approve its current identity before Hecate runs it.")
	case errors.Is(err, agentadapters.ErrExecutableIdentityUnavailable), errors.Is(err, agentadapters.ErrExecutableIdentityRaced):
		WriteError(w, http.StatusServiceUnavailable, errCodeAgentExecutableIdentityUnavailable, "Hecate could not verify the app identity. Check the installation and try again.")
	default:
		return false
	}
	return true
}

func renderAgentAdapterCapabilities(caps []agentadapters.Capability) []AgentAdapterCapabilityItem {
	if len(caps) == 0 {
		return nil
	}
	out := make([]AgentAdapterCapabilityItem, 0, len(caps))
	for _, cap := range caps {
		if strings.TrimSpace(cap.ID) == "" || strings.TrimSpace(cap.Status) == "" {
			continue
		}
		out = append(out, AgentAdapterCapabilityItem{
			ID:          cap.ID,
			Name:        cap.Name,
			Description: cap.Description,
			Status:      cap.Status,
		})
	}
	return out
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
	case agentadapters.ProbeStatusAuthRequired:
		return agentadapters.AuthStatusUnauthenticated, firstNonEmptyString(result.Hint, result.Error, fallbackError)
	case agentadapters.ProbeStatusError:
		if strings.Contains(strings.ToLower(result.Error+"\n"+result.Stderr), "credit balance") {
			return agentadapters.AuthStatusBilling, firstNonEmptyString(result.Hint, result.Error, fallbackError)
		}
	}
	return firstNonEmptyString(fallbackStatus, agentadapters.AuthStatusUnknown), fallbackError
}
