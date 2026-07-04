package api

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/cairnlinebridge"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/projectworkapp"
)

const projectCairnlineWriteAuthorityProjectRoles = "project-roles"

func (h *Handler) projectRoleWritesUseCairnlineAuthority() bool {
	return h != nil &&
		h.projectCairnlineEmbeddedConnectorEnabled() &&
		h.config.ProjectsCairnlineWriteAuthorityEnabled(projectCairnlineWriteAuthorityProjectRoles)
}

func (h *Handler) createProjectWorkRoleWithCairnlineAuthority(ctx context.Context, projectID string, cmd projectworkapp.CreateRoleCommand) (projectwork.AgentRoleProfile, error) {
	project, err := h.projectForCairnlineWriteAuthority(ctx, projectID)
	if err != nil {
		return projectwork.AgentRoleProfile{}, err
	}
	role := projectwork.AgentRoleProfile{
		ID:                  firstNonEmptyString(strings.TrimSpace(cmd.ID), newOpaqueTaskResourceID("role")),
		ProjectID:           projectID,
		Name:                cmd.Name,
		Description:         cmd.Description,
		Instructions:        cmd.Instructions,
		DefaultDriverKind:   cmd.DefaultDriverKind,
		DefaultProvider:     cmd.DefaultProvider,
		DefaultModel:        cmd.DefaultModel,
		DefaultAgentProfile: cmd.DefaultAgentProfile,
		SkillIDs:            append([]string(nil), cmd.SkillIDs...),
	}
	role = normalizeProjectRoleForCairnlineAuthority(role)
	if err := validateProjectRoleForCairnlineAuthority(role); err != nil {
		return projectwork.AgentRoleProfile{}, err
	}

	var created projectwork.AgentRoleProfile
	err = h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		if _, err := cairnlinebridge.UpsertProjectMetadata(ctx, service, project); err != nil {
			return err
		}
		if _, err := getCairnlineProjectRoleForAuthority(ctx, service, projectID, role.ID); err == nil {
			return cairnline.ErrDuplicate
		} else if !errors.Is(err, cairnline.ErrNotFound) {
			return err
		}
		recorded, err := cairnlinebridge.UpsertRole(ctx, service, role)
		if err != nil {
			return err
		}
		created, err = h.projectWorkRoleFromCairnlineAuthority(ctx, service, recorded, role)
		return err
	})
	if err != nil {
		return projectwork.AgentRoleProfile{}, err
	}
	if err := h.upsertProjectRoleRuntimeDefaults(ctx, created); err != nil {
		return projectwork.AgentRoleProfile{}, err
	}
	if shadowed, ok := h.shadowProjectRoleToHecate(ctx, "project_role_cairnline_authority_create", created); ok {
		created = shadowed
	}
	return created, nil
}

func (h *Handler) updateProjectWorkRoleWithCairnlineAuthority(ctx context.Context, projectID, roleID string, cmd projectworkapp.UpdateRoleCommand) (projectwork.AgentRoleProfile, error) {
	if projectwork.IsBuiltInRoleID(roleID) {
		return projectwork.AgentRoleProfile{}, projectwork.ErrBuiltInRole
	}
	project, err := h.projectForCairnlineWriteAuthority(ctx, projectID)
	if err != nil {
		return projectwork.AgentRoleProfile{}, err
	}

	var updated projectwork.AgentRoleProfile
	err = h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		if _, err := cairnlinebridge.UpsertProjectMetadata(ctx, service, project); err != nil {
			return err
		}
		existing, err := getCairnlineProjectRoleForAuthority(ctx, service, projectID, roleID)
		if err != nil {
			return err
		}
		role := projectWorkRoleFromCairnline(existing, projectwork.AgentRoleProfile{})
		role, err = h.projectRoleWithHecateRuntimeOverlay(ctx, role)
		if err != nil {
			return err
		}
		applyProjectRoleUpdate(&role, cmd)
		role = normalizeProjectRoleForCairnlineAuthority(role)
		if err := validateProjectRoleForCairnlineAuthority(role); err != nil {
			return err
		}
		recorded, err := cairnlinebridge.UpsertRole(ctx, service, role)
		if err != nil {
			return err
		}
		updated, err = h.projectWorkRoleFromCairnlineAuthority(ctx, service, recorded, role)
		return err
	})
	if err != nil {
		return projectwork.AgentRoleProfile{}, err
	}
	if err := h.upsertProjectRoleRuntimeDefaults(ctx, updated); err != nil {
		return projectwork.AgentRoleProfile{}, err
	}
	if shadowed, ok := h.shadowProjectRoleToHecate(ctx, "project_role_cairnline_authority_update", updated); ok {
		updated = shadowed
	}
	return updated, nil
}

func (h *Handler) deleteProjectWorkRoleWithCairnlineAuthority(ctx context.Context, projectID, roleID string) error {
	if projectwork.IsBuiltInRoleID(roleID) {
		return projectwork.ErrBuiltInRole
	}
	var deleted projectwork.AgentRoleProfile
	if err := h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		existing, err := getCairnlineProjectRoleForAuthority(ctx, service, projectID, roleID)
		if err != nil {
			return err
		}
		deleted, err = h.projectWorkRoleFromCairnlineAuthority(ctx, service, existing, projectwork.AgentRoleProfile{})
		if err != nil {
			return err
		}
		return cairnlinebridge.DeleteRole(ctx, service, deleted)
	}); err != nil {
		return err
	}
	if err := h.deleteProjectRoleRuntimeDefaults(ctx, projectID, roleID); err != nil {
		return err
	}
	h.shadowProjectRoleDeleteToHecate(ctx, "project_role_cairnline_authority_delete", projectID, roleID)
	return nil
}

func (h *Handler) projectWorkRoleFromCairnlineAuthority(ctx context.Context, service *cairnline.Service, role cairnline.Role, native projectwork.AgentRoleProfile) (projectwork.AgentRoleProfile, error) {
	return projectWorkRoleFromCairnline(role, native), nil
}

func getCairnlineProjectRoleForAuthority(ctx context.Context, service *cairnline.Service, projectID, roleID string) (cairnline.Role, error) {
	roles, err := service.ListRoles(ctx, projectID)
	if err != nil {
		return cairnline.Role{}, err
	}
	roleID = strings.TrimSpace(roleID)
	for _, role := range roles {
		if role.ID == roleID {
			return role, nil
		}
	}
	return cairnline.Role{}, cairnline.ErrNotFound
}

func normalizeProjectRoleForCairnlineAuthority(role projectwork.AgentRoleProfile) projectwork.AgentRoleProfile {
	role.ID = strings.TrimSpace(role.ID)
	role.ProjectID = strings.TrimSpace(role.ProjectID)
	role.Name = strings.TrimSpace(role.Name)
	role.Description = strings.TrimSpace(role.Description)
	role.Instructions = strings.TrimSpace(role.Instructions)
	role.DefaultDriverKind = strings.TrimSpace(role.DefaultDriverKind)
	role.DefaultProvider = strings.TrimSpace(role.DefaultProvider)
	role.DefaultModel = strings.TrimSpace(role.DefaultModel)
	role.DefaultAgentProfile = strings.TrimSpace(role.DefaultAgentProfile)
	role.SkillIDs = compactProjectWorkAuthorityStrings(role.SkillIDs)
	return role
}

func validateProjectRoleForCairnlineAuthority(role projectwork.AgentRoleProfile) error {
	if projectwork.IsBuiltInRoleID(role.ID) || role.BuiltIn {
		return projectwork.ErrBuiltInRole
	}
	if role.ProjectID == "" {
		return fmt.Errorf("%w: project_id is required", projectwork.ErrInvalid)
	}
	if role.ID == "" {
		return fmt.Errorf("%w: role id is required", projectwork.ErrInvalid)
	}
	if role.Name == "" {
		return fmt.Errorf("%w: role name is required", projectwork.ErrInvalid)
	}
	if !validProjectRoleDefaultDriverForCairnlineAuthority(role.DefaultDriverKind) {
		return fmt.Errorf("%w: unsupported role default_driver_kind %q", projectwork.ErrInvalid, role.DefaultDriverKind)
	}
	return nil
}

func validProjectRoleDefaultDriverForCairnlineAuthority(driver string) bool {
	switch strings.TrimSpace(driver) {
	case "", projectwork.AssignmentDriverHecateTask, projectwork.AssignmentDriverExternalAgent:
		return true
	default:
		return false
	}
}

func applyProjectRoleUpdate(role *projectwork.AgentRoleProfile, cmd projectworkapp.UpdateRoleCommand) {
	if role == nil {
		return
	}
	if cmd.Name != nil {
		role.Name = *cmd.Name
	}
	if cmd.Description != nil {
		role.Description = *cmd.Description
	}
	if cmd.Instructions != nil {
		role.Instructions = *cmd.Instructions
	}
	if cmd.DefaultDriverKind != nil {
		role.DefaultDriverKind = *cmd.DefaultDriverKind
	}
	if cmd.DefaultProvider != nil {
		role.DefaultProvider = *cmd.DefaultProvider
	}
	if cmd.DefaultModel != nil {
		role.DefaultModel = *cmd.DefaultModel
	}
	if cmd.DefaultAgentProfile != nil {
		role.DefaultAgentProfile = *cmd.DefaultAgentProfile
	}
	if cmd.SkillIDs != nil {
		role.SkillIDs = append([]string(nil), cmd.SkillIDs...)
	}
}

func (h *Handler) shadowProjectRoleToHecate(ctx context.Context, operation string, role projectwork.AgentRoleProfile) (projectwork.AgentRoleProfile, bool) {
	if h == nil || h.projectWork == nil {
		return projectwork.AgentRoleProfile{}, false
	}
	if h.projectCairnlineEmbeddedReplacementModeArmed() {
		return projectwork.AgentRoleProfile{}, false
	}
	if updated, err := h.projectWork.UpdateRole(ctx, role.ProjectID, role.ID, func(existing *projectwork.AgentRoleProfile) {
		*existing = role
	}); err == nil {
		return updated, true
	} else if !errors.Is(err, projectwork.ErrNotFound) {
		h.logProjectRoleShadowError(ctx, operation, role.ProjectID, role.ID, err)
		return projectwork.AgentRoleProfile{}, false
	}
	created, err := h.projectWork.CreateRole(ctx, role)
	if err == nil {
		return created, true
	}
	if !errors.Is(err, projectwork.ErrDuplicateRole) {
		h.logProjectRoleShadowError(ctx, operation, role.ProjectID, role.ID, err)
	}
	return projectwork.AgentRoleProfile{}, false
}

func (h *Handler) shadowProjectRoleDeleteToHecate(ctx context.Context, operation, projectID, roleID string) {
	if h == nil || h.projectWork == nil {
		return
	}
	if err := h.projectWork.DeleteRole(ctx, projectID, roleID); err != nil && !errors.Is(err, projectwork.ErrNotFound) {
		h.logProjectRoleShadowError(ctx, operation, projectID, roleID, err)
	}
}

func (h *Handler) logProjectRoleShadowError(ctx context.Context, operation, projectID, roleID string, err error) {
	if err == nil || h == nil || h.logger == nil {
		return
	}
	h.logger.WarnContext(ctx, "project role Hecate shadow failed", "operation", operation, "project_id", projectID, "role_id", roleID, "error", err)
}
