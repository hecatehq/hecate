package api

import (
	"errors"
	"net/http"

	"github.com/hecatehq/hecate/internal/agentprofiles"
)

func (h *Handler) HandleAgentProfiles(w http.ResponseWriter, r *http.Request) {
	items, err := h.agentProfiles.List(r.Context())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	data := make([]AgentProfileResponseItem, 0, len(items))
	for _, item := range items {
		data = append(data, renderAgentProfile(item))
	}
	WriteJSON(w, http.StatusOK, AgentProfilesResponse{Object: "agent_presets", Data: data})
}

func (h *Handler) HandleCreateAgentProfile(w http.ResponseWriter, r *http.Request) {
	var req CreateAgentProfileRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	profile := agentProfileFromCreate(req)
	if profile.ID == "" {
		profile.ID = newOpaqueTaskResourceID("prof")
	}
	if h.agentProfileWritesUseCairnlineAuthority() {
		created, err := h.createAgentProfileWithCairnlineAuthority(r.Context(), profile)
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
		WriteJSON(w, http.StatusCreated, AgentProfileResponse{Object: "agent_preset", Data: renderAgentProfile(created)})
		return
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
	h.mirrorAgentProfileToCairnline(r.Context(), "agent_preset_create", profile)
	WriteJSON(w, http.StatusCreated, AgentProfileResponse{Object: "agent_preset", Data: renderAgentProfile(profile)})
}

func (h *Handler) HandleAgentProfile(w http.ResponseWriter, r *http.Request) {
	profile, ok, err := h.agentProfiles.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "agent preset not found")
		return
	}
	WriteJSON(w, http.StatusOK, AgentProfileResponse{Object: "agent_preset", Data: renderAgentProfile(profile)})
}

func (h *Handler) HandleUpdateAgentProfile(w http.ResponseWriter, r *http.Request) {
	var req UpdateAgentProfileRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if h.agentProfileWritesUseCairnlineAuthority() {
		profile, err := h.updateAgentProfileWithCairnlineAuthority(r.Context(), r.PathValue("id"), req)
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
		WriteJSON(w, http.StatusOK, AgentProfileResponse{Object: "agent_preset", Data: renderAgentProfile(profile)})
		return
	}
	profile, err := h.agentProfiles.Update(r.Context(), r.PathValue("id"), func(item *agentprofiles.Profile) {
		applyAgentProfileUpdate(item, req)
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
	h.mirrorAgentProfileToCairnline(r.Context(), "agent_preset_update", profile)
	WriteJSON(w, http.StatusOK, AgentProfileResponse{Object: "agent_preset", Data: renderAgentProfile(profile)})
}

func (h *Handler) HandleDeleteAgentProfile(w http.ResponseWriter, r *http.Request) {
	profileID := r.PathValue("id")
	if h.agentProfileWritesUseCairnlineAuthority() {
		if err := h.deleteAgentProfileWithCairnlineAuthority(r.Context(), profileID); errors.Is(err, agentprofiles.ErrNotFound) {
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
		return
	}
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
	h.mirrorAgentProfileDeleteToCairnline(r.Context(), "agent_preset_delete", profileID)
	w.WriteHeader(http.StatusNoContent)
}

func agentProfileFromCreate(req CreateAgentProfileRequest) agentprofiles.Profile {
	return agentprofiles.Profile{
		ID:                   req.ID,
		Name:                 req.Name,
		Description:          req.Description,
		Instructions:         req.Instructions,
		Surface:              req.Surface,
		ProviderHint:         req.ProviderHint,
		ModelHint:            req.ModelHint,
		ExecutionProfile:     req.ExecutionProfile,
		ToolsEnabled:         req.ToolsEnabled,
		WritesAllowed:        req.WritesAllowed,
		NetworkAllowed:       req.NetworkAllowed,
		ApprovalPolicy:       req.ApprovalPolicy,
		ProjectMemoryPolicy:  req.ProjectMemoryPolicy,
		ContextSourcePolicy:  req.ContextSourcePolicy,
		SkillIDs:             req.SkillIDs,
		ExternalAgentKind:    req.ExternalAgentKind,
		ExternalAgentOptions: req.ExternalAgentOptions,
	}
}

func applyAgentProfileUpdate(profile *agentprofiles.Profile, req UpdateAgentProfileRequest) {
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
}

func renderAgentProfile(profile agentprofiles.Profile) AgentProfileResponseItem {
	return AgentProfileResponseItem{
		ID:                   profile.ID,
		Name:                 profile.Name,
		Description:          profile.Description,
		Instructions:         profile.Instructions,
		Surface:              profile.Surface,
		ProviderHint:         profile.ProviderHint,
		ModelHint:            profile.ModelHint,
		ExecutionProfile:     profile.ExecutionProfile,
		ToolsEnabled:         profile.ToolsEnabled,
		WritesAllowed:        profile.WritesAllowed,
		NetworkAllowed:       profile.NetworkAllowed,
		ApprovalPolicy:       profile.ApprovalPolicy,
		ProjectMemoryPolicy:  profile.ProjectMemoryPolicy,
		ContextSourcePolicy:  profile.ContextSourcePolicy,
		SkillIDs:             append([]string(nil), profile.SkillIDs...),
		ExternalAgentKind:    profile.ExternalAgentKind,
		ExternalAgentOptions: cloneAgentProfileOptions(profile.ExternalAgentOptions),
		BuiltIn:              profile.BuiltIn,
		CreatedAt:            formatOptionalTime(profile.CreatedAt),
		UpdatedAt:            formatOptionalTime(profile.UpdatedAt),
	}
}

func cloneAgentProfileOptions(items map[string]string) map[string]string {
	if len(items) == 0 {
		return nil
	}
	out := make(map[string]string, len(items))
	for key, value := range items {
		out[key] = value
	}
	return out
}
