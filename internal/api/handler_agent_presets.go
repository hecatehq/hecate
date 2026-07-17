package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/hecatehq/hecate/internal/agentprofiles"
)

func (h *Handler) HandleAgentPresets(w http.ResponseWriter, r *http.Request) {
	items, err := h.agentProfiles.List(r.Context())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	data := make([]AgentPresetResponseItem, 0, len(items))
	for _, item := range items {
		data = append(data, renderAgentPreset(item))
	}
	WriteJSON(w, http.StatusOK, AgentPresetsResponse{Object: "agent_presets", Data: data})
}

func (h *Handler) HandleCreateAgentPreset(w http.ResponseWriter, r *http.Request) {
	var req CreateAgentPresetRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	profile := agentPresetFromCreate(req)
	if profile.ID == "" {
		profile.ID = newOpaqueTaskResourceID("prof")
	}
	profile, err := h.agentProfiles.Create(r.Context(), profile)
	if errors.Is(err, agentprofiles.ErrInvalid) {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "invalid agent preset")
		return
	}
	if errors.Is(err, agentprofiles.ErrBuiltIn) {
		WriteError(w, http.StatusConflict, errCodeConflict, "built-in agent preset cannot be created")
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusCreated, AgentPresetResponse{Object: "agent_preset", Data: renderAgentPreset(profile)})
}

func (h *Handler) HandleAgentPreset(w http.ResponseWriter, r *http.Request) {
	profile, ok, err := h.agentProfiles.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "agent preset not found")
		return
	}
	WriteJSON(w, http.StatusOK, AgentPresetResponse{Object: "agent_preset", Data: renderAgentPreset(profile)})
}

func (h *Handler) HandleUpdateAgentPreset(w http.ResponseWriter, r *http.Request) {
	var req UpdateAgentPresetRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	profile, err := h.agentProfiles.Update(r.Context(), r.PathValue("id"), func(item *agentprofiles.Profile) {
		applyAgentPresetUpdate(item, req)
	})
	if errors.Is(err, agentprofiles.ErrNotFound) {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "agent preset not found")
		return
	}
	if errors.Is(err, agentprofiles.ErrBuiltIn) {
		WriteError(w, http.StatusConflict, errCodeConflict, "built-in agent preset cannot be updated")
		return
	}
	if errors.Is(err, agentprofiles.ErrInvalid) {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "invalid agent preset")
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, AgentPresetResponse{Object: "agent_preset", Data: renderAgentPreset(profile)})
}

func (h *Handler) HandleDeleteAgentPreset(w http.ResponseWriter, r *http.Request) {
	profileID := r.PathValue("id")
	if err := h.agentProfiles.Delete(r.Context(), profileID); errors.Is(err, agentprofiles.ErrNotFound) {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "agent preset not found")
		return
	} else if errors.Is(err, agentprofiles.ErrBuiltIn) {
		WriteError(w, http.StatusConflict, errCodeConflict, "built-in agent preset cannot be deleted")
		return
	} else if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func agentPresetFromCreate(req CreateAgentPresetRequest) agentprofiles.Profile {
	return agentprofiles.Profile{
		ID:                    req.ID,
		Name:                  req.Name,
		Description:           req.Description,
		Instructions:          req.Instructions,
		Surface:               req.Surface,
		ProviderHint:          req.ProviderHint,
		ModelHint:             req.ModelHint,
		ExecutionProfile:      req.ExecutionProfile,
		ToolsEnabled:          req.ToolsEnabled,
		WritesAllowed:         req.WritesAllowed,
		NetworkAllowed:        req.NetworkAllowed,
		BrowserAllowed:        req.BrowserAllowed,
		BrowserAllowedOrigins: req.BrowserAllowedOrigins,
		ApprovalPolicy:        req.ApprovalPolicy,
		ProjectMemoryPolicy:   req.ProjectMemoryPolicy,
		ContextSourcePolicy:   req.ContextSourcePolicy,
		SkillIDs:              req.SkillIDs,
		ExternalAgentKind:     req.ExternalAgentKind,
		ExternalAgentOptions:  req.ExternalAgentOptions,
	}
}

func applyAgentPresetUpdate(profile *agentprofiles.Profile, req UpdateAgentPresetRequest) {
	if req.Name != nil {
		profile.Name = *req.Name
	}
	if req.Description != nil {
		profile.Description = *req.Description
	}
	if req.Instructions != nil {
		profile.Instructions = *req.Instructions
	}
	if req.Surface != nil {
		profile.Surface = *req.Surface
	}
	if req.ProviderHint != nil {
		profile.ProviderHint = *req.ProviderHint
	}
	if req.ModelHint != nil {
		profile.ModelHint = *req.ModelHint
	}
	if req.ExecutionProfile != nil {
		profile.ExecutionProfile = *req.ExecutionProfile
	}
	if req.ToolsEnabled != nil {
		profile.ToolsEnabled = *req.ToolsEnabled
	}
	if req.WritesAllowed != nil {
		profile.WritesAllowed = *req.WritesAllowed
	}
	if req.NetworkAllowed != nil {
		profile.NetworkAllowed = *req.NetworkAllowed
	}
	if req.BrowserAllowed != nil {
		profile.BrowserAllowed = *req.BrowserAllowed
	}
	if req.BrowserAllowedOrigins != nil {
		profile.BrowserAllowedOrigins = req.BrowserAllowedOrigins
	}
	if req.ApprovalPolicy != nil {
		profile.ApprovalPolicy = *req.ApprovalPolicy
	}
	if req.ProjectMemoryPolicy != nil {
		profile.ProjectMemoryPolicy = *req.ProjectMemoryPolicy
	}
	if req.ContextSourcePolicy != nil {
		profile.ContextSourcePolicy = *req.ContextSourcePolicy
	}
	if req.SkillIDs != nil {
		profile.SkillIDs = req.SkillIDs
	}
	if req.ExternalAgentKind != nil {
		profile.ExternalAgentKind = *req.ExternalAgentKind
	}
	if req.ExternalAgentOptions != nil {
		profile.ExternalAgentOptions = req.ExternalAgentOptions
	}
	clearIneligibleBrowserEvidence(profile)
}

// clearIneligibleBrowserEvidence makes a partial PATCH behave like the
// operator console: changing a prerequisite clears the narrower browser
// capability instead of preserving an invalid combination that storage must
// reject. Creation remains strict, so a client cannot silently request an
// invalid browser-enabled preset in the first place.
func clearIneligibleBrowserEvidence(profile *agentprofiles.Profile) {
	if profile == nil {
		return
	}
	surface := strings.TrimSpace(profile.Surface)
	if surface == "" {
		surface = agentprofiles.SurfaceAny
	}
	if profile.BrowserAllowed && profile.ToolsEnabled && (surface == agentprofiles.SurfaceAny || surface == agentprofiles.SurfaceHecateTask) {
		return
	}
	profile.BrowserAllowed = false
	profile.BrowserAllowedOrigins = nil
}

func renderAgentPreset(profile agentprofiles.Profile) AgentPresetResponseItem {
	return AgentPresetResponseItem{
		ID:                    profile.ID,
		Name:                  profile.Name,
		Description:           profile.Description,
		Instructions:          profile.Instructions,
		Surface:               profile.Surface,
		ProviderHint:          profile.ProviderHint,
		ModelHint:             profile.ModelHint,
		ExecutionProfile:      profile.ExecutionProfile,
		ToolsEnabled:          profile.ToolsEnabled,
		WritesAllowed:         profile.WritesAllowed,
		NetworkAllowed:        profile.NetworkAllowed,
		BrowserAllowed:        profile.BrowserAllowed,
		BrowserAllowedOrigins: append([]string(nil), profile.BrowserAllowedOrigins...),
		ApprovalPolicy:        profile.ApprovalPolicy,
		ProjectMemoryPolicy:   profile.ProjectMemoryPolicy,
		ContextSourcePolicy:   profile.ContextSourcePolicy,
		SkillIDs:              append([]string(nil), profile.SkillIDs...),
		ExternalAgentKind:     profile.ExternalAgentKind,
		ExternalAgentOptions:  cloneAgentPresetOptions(profile.ExternalAgentOptions),
		BuiltIn:               profile.BuiltIn,
		CreatedAt:             formatOptionalTime(profile.CreatedAt),
		UpdatedAt:             formatOptionalTime(profile.UpdatedAt),
	}
}

func cloneAgentPresetOptions(items map[string]string) map[string]string {
	if len(items) == 0 {
		return nil
	}
	out := make(map[string]string, len(items))
	for key, value := range items {
		out[key] = value
	}
	return out
}
