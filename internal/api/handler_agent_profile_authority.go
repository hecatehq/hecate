package api

import (
	"context"
	"errors"
	"strings"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/agentprofiles"
	"github.com/hecatehq/hecate/internal/cairnlinebridge"
)

const projectCairnlineWriteAuthorityAgentProfiles = "agent-profiles"

func (h *Handler) agentProfileWritesUseCairnlineAuthority() bool {
	return h != nil &&
		h.config.ProjectsCoordinationBackend() == "cairnline" &&
		h.config.ProjectsCairnlineWriteAuthorityEnabled(projectCairnlineWriteAuthorityAgentProfiles)
}

func (h *Handler) createAgentProfileWithCairnlineAuthority(ctx context.Context, profile agentprofiles.Profile) (agentprofiles.Profile, error) {
	profile = normalizeAgentProfileForCairnlineAuthority(profile)
	if err := validateAgentProfileForCairnlineAuthority(profile); err != nil {
		return agentprofiles.Profile{}, err
	}
	var recorded agentprofiles.Profile
	err := h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		written, err := cairnlinebridge.UpsertAgentProfile(ctx, service, profile)
		if err != nil {
			return err
		}
		recorded = profile
		recorded.CreatedAt = written.CreatedAt
		recorded.UpdatedAt = written.UpdatedAt
		return nil
	})
	if err != nil {
		return agentprofiles.Profile{}, agentProfileCairnlineAuthorityError(err)
	}
	if shadowed, ok := h.shadowAgentProfileToHecate(ctx, "agent_profile_cairnline_authority_create", recorded); ok {
		recorded = shadowed
	}
	return recorded, nil
}

func (h *Handler) updateAgentProfileWithCairnlineAuthority(ctx context.Context, profileID string, req UpdateAgentProfileRequest) (agentprofiles.Profile, error) {
	profileID = strings.TrimSpace(profileID)
	if agentprofiles.IsBuiltInProfileID(profileID) {
		return agentprofiles.Profile{}, agentprofiles.ErrBuiltIn
	}
	var recorded agentprofiles.Profile
	err := h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		existing, err := h.loadAgentProfileForCairnlineAuthority(ctx, service, profileID)
		if err != nil {
			return err
		}
		applyAgentProfileUpdate(&existing, req)
		existing = normalizeAgentProfileForCairnlineAuthority(existing)
		if err := validateAgentProfileForCairnlineAuthority(existing); err != nil {
			return err
		}
		written, err := cairnlinebridge.UpsertAgentProfile(ctx, service, existing)
		if err != nil {
			return err
		}
		recorded = existing
		recorded.CreatedAt = written.CreatedAt
		recorded.UpdatedAt = written.UpdatedAt
		return nil
	})
	if err != nil {
		return agentprofiles.Profile{}, agentProfileCairnlineAuthorityError(err)
	}
	if shadowed, ok := h.shadowAgentProfileToHecate(ctx, "agent_profile_cairnline_authority_update", recorded); ok {
		recorded = shadowed
	}
	return recorded, nil
}

func (h *Handler) deleteAgentProfileWithCairnlineAuthority(ctx context.Context, profileID string) error {
	profileID = strings.TrimSpace(profileID)
	if agentprofiles.IsBuiltInProfileID(profileID) {
		return agentprofiles.ErrBuiltIn
	}
	err := h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		if _, err := getCairnlineAgentProfileForAuthority(ctx, service, profileID); err != nil {
			return err
		}
		return cairnlinebridge.DeleteAgentProfile(ctx, service, profileID)
	})
	if err != nil {
		return agentProfileCairnlineAuthorityError(err)
	}
	h.shadowAgentProfileDeleteToHecate(ctx, "agent_profile_cairnline_authority_delete", profileID)
	return nil
}

func (h *Handler) loadAgentProfileForCairnlineAuthority(ctx context.Context, service *cairnline.Service, profileID string) (agentprofiles.Profile, error) {
	if h != nil && h.agentProfiles != nil {
		if profile, ok, err := h.agentProfiles.Get(ctx, profileID); err != nil {
			return agentprofiles.Profile{}, err
		} else if ok {
			if profile.BuiltIn {
				return agentprofiles.Profile{}, agentprofiles.ErrBuiltIn
			}
			return profile, nil
		}
	}
	profile, err := getCairnlineAgentProfileForAuthority(ctx, service, profileID)
	if err != nil {
		return agentprofiles.Profile{}, err
	}
	execution := cairnline.ExecutionProfile{}
	if profile.ID != "" {
		execution = getCairnlineExecutionProfileForAgentProfileAuthority(ctx, service, profile.ID)
	}
	return agentProfileFromCairnlineAuthority(profile, execution), nil
}

func getCairnlineAgentProfileForAuthority(ctx context.Context, service *cairnline.Service, profileID string) (cairnline.AgentProfile, error) {
	profiles, err := service.ListAgentProfiles(ctx)
	if err != nil {
		return cairnline.AgentProfile{}, err
	}
	profileID = strings.TrimSpace(profileID)
	for _, profile := range profiles {
		if strings.TrimSpace(profile.ID) == profileID {
			return profile, nil
		}
	}
	return cairnline.AgentProfile{}, cairnline.ErrNotFound
}

func getCairnlineExecutionProfileForAgentProfileAuthority(ctx context.Context, service *cairnline.Service, profileID string) cairnline.ExecutionProfile {
	profiles, err := service.ListExecutionProfiles(ctx)
	if err != nil {
		return cairnline.ExecutionProfile{}
	}
	profileID = strings.TrimSpace(profileID)
	for _, profile := range profiles {
		if strings.TrimSpace(profile.ID) == profileID {
			return profile
		}
	}
	return cairnline.ExecutionProfile{}
}

func agentProfileFromCairnlineAuthority(profile cairnline.AgentProfile, execution cairnline.ExecutionProfile) agentprofiles.Profile {
	return normalizeAgentProfileForCairnlineAuthority(agentprofiles.Profile{
		ID:                   profile.ID,
		Name:                 profile.Name,
		Description:          profile.Description,
		Instructions:         profile.Instructions,
		Surface:              agentProfileSurfaceFromExecutionAgentKind(execution.AgentKind),
		ProviderHint:         execution.ProviderHint,
		ModelHint:            execution.ModelHint,
		ExecutionProfile:     execution.ID,
		ToolsEnabled:         execution.ToolsPolicy == "allow",
		WritesAllowed:        execution.WritesPolicy == "allow",
		NetworkAllowed:       execution.NetworkPolicy == "allow",
		ApprovalPolicy:       firstNonEmptyString(execution.ApprovalPolicy, agentprofiles.ApprovalInherit),
		ProjectMemoryPolicy:  firstNonEmptyString(profile.MemoryPolicy, agentprofiles.MemoryInherit),
		ContextSourcePolicy:  firstNonEmptyString(profile.SourcePolicy, agentprofiles.ContextInherit),
		SkillIDs:             append([]string(nil), profile.SkillIDs...),
		ExternalAgentKind:    agentProfileExternalAgentKindFromExecution(execution),
		ExternalAgentOptions: stringAgentProfileAdapterOptions(execution.AdapterOptions),
		CreatedAt:            profile.CreatedAt,
		UpdatedAt:            profile.UpdatedAt,
	})
}

func normalizeAgentProfileForCairnlineAuthority(profile agentprofiles.Profile) agentprofiles.Profile {
	profile.ID = strings.TrimSpace(profile.ID)
	profile.Name = strings.TrimSpace(profile.Name)
	profile.Description = strings.TrimSpace(profile.Description)
	profile.Instructions = strings.TrimSpace(profile.Instructions)
	profile.Surface = strings.TrimSpace(profile.Surface)
	profile.ProviderHint = strings.TrimSpace(profile.ProviderHint)
	profile.ModelHint = strings.TrimSpace(profile.ModelHint)
	profile.ExecutionProfile = strings.TrimSpace(profile.ExecutionProfile)
	profile.ApprovalPolicy = strings.TrimSpace(profile.ApprovalPolicy)
	profile.ProjectMemoryPolicy = strings.TrimSpace(profile.ProjectMemoryPolicy)
	profile.ContextSourcePolicy = strings.TrimSpace(profile.ContextSourcePolicy)
	profile.ExternalAgentKind = strings.TrimSpace(profile.ExternalAgentKind)
	if profile.Surface == "" {
		profile.Surface = agentprofiles.SurfaceAny
	}
	if profile.ApprovalPolicy == "" {
		profile.ApprovalPolicy = agentprofiles.ApprovalInherit
	}
	if profile.ProjectMemoryPolicy == "" {
		profile.ProjectMemoryPolicy = agentprofiles.MemoryInherit
	}
	if profile.ContextSourcePolicy == "" {
		profile.ContextSourcePolicy = agentprofiles.ContextInherit
	}
	profile.SkillIDs = compactProjectWorkAuthorityStrings(profile.SkillIDs)
	profile.ExternalAgentOptions = normalizeAgentProfileAuthorityOptions(profile.ExternalAgentOptions)
	return profile
}

func validateAgentProfileForCairnlineAuthority(profile agentprofiles.Profile) error {
	if profile.ID == "" || profile.Name == "" {
		return agentprofiles.ErrInvalid
	}
	if agentprofiles.IsBuiltInProfileID(profile.ID) || profile.BuiltIn {
		return agentprofiles.ErrBuiltIn
	}
	if !oneOfAgentProfileAuthority(profile.Surface, agentprofiles.SurfaceAny, agentprofiles.SurfaceHecateChat, agentprofiles.SurfaceHecateTask, agentprofiles.SurfaceExternalAgent) {
		return agentprofiles.ErrInvalid
	}
	if !oneOfAgentProfileAuthority(profile.ApprovalPolicy, agentprofiles.ApprovalInherit, agentprofiles.ApprovalRequire, agentprofiles.ApprovalBlock, agentprofiles.ApprovalAllow) {
		return agentprofiles.ErrInvalid
	}
	if !oneOfAgentProfileAuthority(profile.ProjectMemoryPolicy, agentprofiles.MemoryInherit, agentprofiles.MemoryInclude, agentprofiles.MemoryVisibleOnly, agentprofiles.MemoryExclude) {
		return agentprofiles.ErrInvalid
	}
	if !oneOfAgentProfileAuthority(profile.ContextSourcePolicy, agentprofiles.ContextInherit, agentprofiles.ContextIncludeEnabled, agentprofiles.ContextVisibleOnly, agentprofiles.ContextExclude) {
		return agentprofiles.ErrInvalid
	}
	return nil
}

func normalizeAgentProfileAuthorityOptions(options map[string]string) map[string]string {
	if len(options) == 0 {
		return nil
	}
	out := make(map[string]string, len(options))
	for key, value := range options {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func oneOfAgentProfileAuthority(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}

func agentProfileSurfaceFromExecutionAgentKind(kind string) string {
	switch strings.TrimSpace(kind) {
	case "hecate":
		return agentprofiles.SurfaceHecateTask
	case "":
		return agentprofiles.SurfaceAny
	default:
		return agentprofiles.SurfaceExternalAgent
	}
}

func agentProfileExternalAgentKindFromExecution(profile cairnline.ExecutionProfile) string {
	switch strings.TrimSpace(profile.AgentKind) {
	case "", "hecate", cairnline.DesiredAgentAny:
		return ""
	default:
		return strings.TrimSpace(profile.AgentKind)
	}
}

func stringAgentProfileAdapterOptions(options map[string]any) map[string]string {
	if len(options) == 0 {
		return nil
	}
	out := make(map[string]string, len(options))
	for key, value := range options {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			out[key] = strings.TrimSpace(text)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func agentProfileCairnlineAuthorityError(err error) error {
	if errors.Is(err, cairnline.ErrNotFound) {
		return errors.Join(agentprofiles.ErrNotFound, err)
	}
	if errors.Is(err, cairnline.ErrInvalid) {
		return errors.Join(agentprofiles.ErrInvalid, err)
	}
	return err
}

func (h *Handler) shadowAgentProfileToHecate(ctx context.Context, operation string, profile agentprofiles.Profile) (agentprofiles.Profile, bool) {
	if h == nil || h.agentProfiles == nil {
		return profile, false
	}
	if existing, ok, err := h.agentProfiles.Get(ctx, profile.ID); err != nil {
		h.logCairnlineAgentProfileMirrorError(ctx, operation, profile.ID, err)
		return profile, false
	} else if ok && !existing.BuiltIn {
		updated, err := h.agentProfiles.Update(ctx, profile.ID, func(item *agentprofiles.Profile) {
			*item = profile
		})
		if err != nil {
			h.logCairnlineAgentProfileMirrorError(ctx, operation, profile.ID, err)
			return profile, false
		}
		return updated, true
	}
	created, err := h.agentProfiles.Create(ctx, profile)
	if err != nil {
		h.logCairnlineAgentProfileMirrorError(ctx, operation, profile.ID, err)
		return profile, false
	}
	return created, true
}

func (h *Handler) shadowAgentProfileDeleteToHecate(ctx context.Context, operation, profileID string) {
	if h == nil || h.agentProfiles == nil {
		return
	}
	if err := h.agentProfiles.Delete(ctx, profileID); err != nil && !errors.Is(err, agentprofiles.ErrNotFound) {
		h.logCairnlineAgentProfileMirrorError(ctx, operation, profileID, err)
	}
}
