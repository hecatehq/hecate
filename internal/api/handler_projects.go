package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/projectapp"
	"github.com/hecatehq/hecate/internal/projects"
)

type projectRootRequest struct {
	ID        string `json:"id,omitempty"`
	Path      string `json:"path"`
	Kind      string `json:"kind,omitempty"`
	GitRemote string `json:"git_remote,omitempty"`
	GitBranch string `json:"git_branch,omitempty"`
	Active    *bool  `json:"active,omitempty"`
}

type projectContextSourceRequest struct {
	ID             string            `json:"id,omitempty"`
	Kind           string            `json:"kind,omitempty"`
	Title          string            `json:"title,omitempty"`
	Path           string            `json:"path"`
	Enabled        *bool             `json:"enabled,omitempty"`
	Format         string            `json:"format,omitempty"`
	Scope          string            `json:"scope,omitempty"`
	TrustLabel     string            `json:"trust_label,omitempty"`
	SourceCategory string            `json:"source_category,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

type createProjectRequest struct {
	Name                     string                        `json:"name"`
	Description              string                        `json:"description,omitempty"`
	WorkspacePath            string                        `json:"workspace_path,omitempty"`
	WorkspaceKind            string                        `json:"workspace_kind,omitempty"`
	Roots                    []projectRootRequest          `json:"roots,omitempty"`
	ContextSources           []projectContextSourceRequest `json:"context_sources,omitempty"`
	DefaultRootID            string                        `json:"default_root_id,omitempty"`
	DefaultProvider          string                        `json:"default_provider,omitempty"`
	DefaultModel             string                        `json:"default_model,omitempty"`
	DefaultAgentProfile      string                        `json:"default_agent_profile,omitempty"`
	DefaultToolsEnabled      *bool                         `json:"default_tools_enabled,omitempty"`
	DefaultWorkspaceMode     string                        `json:"default_workspace_mode,omitempty"`
	DefaultSystemPrompt      string                        `json:"default_system_prompt,omitempty"`
	DefaultCompactToolOutput *bool                         `json:"default_compact_tool_output,omitempty"`
}

type updateProjectRequest struct {
	Name                     *string                        `json:"name,omitempty"`
	Description              *string                        `json:"description,omitempty"`
	Roots                    *[]projectRootRequest          `json:"roots,omitempty"`
	ContextSources           *[]projectContextSourceRequest `json:"context_sources,omitempty"`
	DefaultRootID            *string                        `json:"default_root_id,omitempty"`
	DefaultProvider          *string                        `json:"default_provider,omitempty"`
	DefaultModel             *string                        `json:"default_model,omitempty"`
	DefaultAgentProfile      *string                        `json:"default_agent_profile,omitempty"`
	DefaultToolsEnabled      *bool                          `json:"default_tools_enabled,omitempty"`
	DefaultWorkspaceMode     *string                        `json:"default_workspace_mode,omitempty"`
	DefaultSystemPrompt      *string                        `json:"default_system_prompt,omitempty"`
	DefaultCompactToolOutput *bool                          `json:"default_compact_tool_output,omitempty"`
	LastOpenedAt             *string                        `json:"last_opened_at,omitempty"`
}

func (h *Handler) HandleProjects(w http.ResponseWriter, r *http.Request) {
	data, err := h.renderProjects(r.Context())
	if err != nil {
		writeProjectReadRenderError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, ProjectsResponse{Object: "projects", Data: data})
}

func (h *Handler) HandleCreateProject(w http.ResponseWriter, r *http.Request) {
	var req createProjectRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	project, err := projectFromCreateRequest(req)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	project.ID = newOpaqueTaskResourceID("proj")
	for idx := range project.Roots {
		if project.Roots[idx].ID == "" {
			project.Roots[idx].ID = newOpaqueTaskResourceID("root")
		}
	}
	for idx := range project.ContextSources {
		if project.ContextSources[idx].ID == "" {
			project.ContextSources[idx].ID = newOpaqueTaskResourceID("ctxsrc")
		}
	}
	if project.DefaultRootID == "" && len(project.Roots) > 0 {
		project.DefaultRootID = project.Roots[0].ID
	}
	if err := validateProjectDefaultRoot(project.DefaultRootID, project.Roots); err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	if h.projectIdentityWritesUseCairnlineAuthority() {
		project, err = h.createProjectWithCairnlineAuthority(r.Context(), project)
		if errors.Is(err, projects.ErrInvalid) || errors.Is(err, cairnline.ErrInvalid) {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
			return
		}
		if errors.Is(err, projects.ErrAlreadyExists) || errors.Is(err, cairnline.ErrDuplicate) {
			WriteError(w, http.StatusConflict, errCodeConflict, err.Error())
			return
		}
		if err != nil {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, ProjectResponse{Object: "project", Data: renderProjectWithBackend(project, "cairnline")})
		return
	}
	project, err = h.projects.Create(r.Context(), project)
	if errors.Is(err, projects.ErrInvalid) {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	if errors.Is(err, projects.ErrAlreadyExists) {
		WriteError(w, http.StatusConflict, errCodeConflict, err.Error())
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if err := h.upsertProjectRuntimeDefaults(r.Context(), project); err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	h.mirrorProjectIdentityToCairnline(r.Context(), "project_create", project)
	WriteJSON(w, http.StatusCreated, ProjectResponse{Object: "project", Data: renderProject(project)})
}

func (h *Handler) HandleProject(w http.ResponseWriter, r *http.Request) {
	project, err := h.renderProject(r.Context(), r.PathValue("id"))
	if err != nil {
		writeProjectReadRenderError(w, err)
		return
	}
	if project == nil {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project not found")
		return
	}
	WriteJSON(w, http.StatusOK, ProjectResponse{Object: "project", Data: *project})
}

func (h *Handler) HandleUpdateProject(w http.ResponseWriter, r *http.Request) {
	var req updateProjectRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	var name string
	if req.Name != nil {
		name = strings.TrimSpace(*req.Name)
		if name == "" {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "project name is required")
			return
		}
	}
	var roots []projects.Root
	if req.Roots != nil {
		var err error
		roots, err = rootsFromRequest(*req.Roots)
		if err != nil {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
			return
		}
		for idx := range roots {
			if roots[idx].ID == "" {
				roots[idx].ID = newOpaqueTaskResourceID("root")
			}
		}
	}
	var contextSources []projects.ContextSource
	if req.ContextSources != nil {
		var err error
		contextSources, err = contextSourcesFromRequest(*req.ContextSources)
		if err != nil {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
			return
		}
		for idx := range contextSources {
			if contextSources[idx].ID == "" {
				contextSources[idx].ID = newOpaqueTaskResourceID("ctxsrc")
			}
		}
	}
	var lastOpenedAt time.Time
	if req.LastOpenedAt != nil {
		var err error
		lastOpenedAt, err = parseProjectTime(*req.LastOpenedAt)
		if err != nil {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
			return
		}
	}
	usesCairnlineMetadataDefaultsAuthority := h.projectMetadataDefaultsWritesUseCairnlineAuthority() && projectUpdateCanUseCairnlineMetadataDefaultsAuthority(req)
	if req.DefaultRootID != nil && !usesCairnlineMetadataDefaultsAuthority {
		defaultRootID := strings.TrimSpace(*req.DefaultRootID)
		rootsToCheck := roots
		if req.Roots == nil {
			existing, ok, err := h.projects.Get(r.Context(), r.PathValue("id"))
			if err != nil {
				WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
				return
			}
			if !ok {
				WriteError(w, http.StatusNotFound, errCodeNotFound, "project not found")
				return
			}
			rootsToCheck = existing.Roots
		}
		if err := validateProjectDefaultRoot(defaultRootID, rootsToCheck); err != nil {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
			return
		}
	}
	if usesCairnlineMetadataDefaultsAuthority {
		project, err := h.updateProjectMetadataDefaultsWithCairnlineAuthority(r.Context(), r.PathValue("id"), req)
		if errors.Is(err, projects.ErrNotFound) || errors.Is(err, cairnline.ErrNotFound) {
			WriteError(w, http.StatusNotFound, errCodeNotFound, "project not found")
			return
		}
		if errors.Is(err, projects.ErrInvalid) || errors.Is(err, cairnline.ErrInvalid) {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
			return
		}
		if errors.Is(err, projects.ErrAlreadyExists) || errors.Is(err, cairnline.ErrDuplicate) {
			WriteError(w, http.StatusConflict, errCodeConflict, err.Error())
			return
		}
		if err != nil {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, ProjectResponse{Object: "project", Data: renderCairnlineAuthorityProject(project)})
		return
	}
	if h.projectRootSourceListUpdateWritesUseCairnlineAuthority(req) {
		project, err := h.updateProjectRootSourceListsWithCairnlineAuthority(r.Context(), r.PathValue("id"), req, roots, contextSources)
		writeProjectListReplaceCairnlineAuthorityResponse(w, project, err)
		return
	}
	project, err := h.projects.Update(r.Context(), r.PathValue("id"), func(item *projects.Project) {
		if req.Name != nil {
			item.Name = name
		}
		if req.Description != nil {
			item.Description = strings.TrimSpace(*req.Description)
		}
		if req.DefaultRootID != nil {
			item.DefaultRootID = strings.TrimSpace(*req.DefaultRootID)
		}
		if req.DefaultProvider != nil {
			item.DefaultProvider = strings.TrimSpace(*req.DefaultProvider)
		}
		if req.DefaultModel != nil {
			item.DefaultModel = strings.TrimSpace(*req.DefaultModel)
		}
		if req.DefaultAgentProfile != nil {
			item.DefaultAgentProfile = strings.TrimSpace(*req.DefaultAgentProfile)
		}
		if req.DefaultToolsEnabled != nil {
			item.DefaultToolsEnabled = cloneBool(req.DefaultToolsEnabled)
		}
		if req.DefaultWorkspaceMode != nil {
			item.DefaultWorkspaceMode = strings.TrimSpace(*req.DefaultWorkspaceMode)
		}
		if req.DefaultSystemPrompt != nil {
			item.DefaultSystemPrompt = strings.TrimSpace(*req.DefaultSystemPrompt)
		}
		if req.DefaultCompactToolOutput != nil {
			item.DefaultCompactToolOutput = cloneBool(req.DefaultCompactToolOutput)
		}
		if req.LastOpenedAt != nil {
			item.LastOpenedAt = lastOpenedAt
		}
		if req.Roots != nil {
			item.Roots = roots
			if req.DefaultRootID == nil && !projectRootIDExists(item.DefaultRootID, item.Roots) {
				item.DefaultRootID = ""
			}
			if item.DefaultRootID == "" && len(item.Roots) > 0 {
				item.DefaultRootID = item.Roots[0].ID
			}
		}
		if req.ContextSources != nil {
			item.ContextSources = contextSources
		}
	})
	if errors.Is(err, projects.ErrNotFound) {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project not found")
		return
	}
	if errors.Is(err, projects.ErrInvalid) {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	if errors.Is(err, projects.ErrAlreadyExists) {
		WriteError(w, http.StatusConflict, errCodeConflict, err.Error())
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if projectUpdateTouchesHecateRuntimeDefaults(req) {
		if err := h.upsertProjectRuntimeDefaults(r.Context(), project); err != nil {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
			return
		}
	}
	if req.Roots != nil {
		h.mirrorProjectRootListReplaceToCairnline(r.Context(), "project_roots_replace", project, project.Roots)
	}
	if req.ContextSources != nil {
		h.mirrorProjectContextSourceListReplaceToCairnline(r.Context(), "project_context_sources_replace", project, project.ContextSources)
	}
	if projectUpdateTouchesPortableMetadata(req) {
		h.mirrorProjectMetadataToCairnline(r.Context(), "project_metadata_update", project)
	}
	if projectUpdateTouchesPortableDefaults(req) {
		h.mirrorProjectDefaultsToCairnline(r.Context(), "project_defaults_update", project)
	}
	WriteJSON(w, http.StatusOK, ProjectResponse{Object: "project", Data: renderProject(project)})
}

func projectUpdateTouchesPortableMetadata(req updateProjectRequest) bool {
	return req.Name != nil || req.Description != nil
}

func projectUpdateTouchesPortableDefaults(req updateProjectRequest) bool {
	return req.DefaultRootID != nil
}

func projectUpdateTouchesHecateRuntimeDefaults(req updateProjectRequest) bool {
	return req.DefaultProvider != nil ||
		req.DefaultModel != nil ||
		req.DefaultAgentProfile != nil ||
		req.DefaultToolsEnabled != nil ||
		req.DefaultWorkspaceMode != nil ||
		req.DefaultSystemPrompt != nil ||
		req.DefaultCompactToolOutput != nil
}

func (h *Handler) HandleCreateProjectRoot(w http.ResponseWriter, r *http.Request) {
	var req projectRootRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	root, err := rootFromRequest(req)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	if root.ID == "" {
		root.ID = newOpaqueTaskResourceID("root")
	}
	if h.projectRootWritesUseCairnlineAuthority() {
		project, _, err := h.createProjectRootWithCairnlineAuthority(r.Context(), r.PathValue("id"), root)
		writeProjectRootCairnlineAuthorityResponse(w, http.StatusCreated, project, err)
		return
	}
	project, created, err := h.projectApplication().CreateRoot(r.Context(), r.PathValue("id"), root)
	h.writeProjectRootMutationResponse(r.Context(), w, http.StatusCreated, project, created, false, err)
}

func (h *Handler) HandleUpdateProjectRoot(w http.ResponseWriter, r *http.Request) {
	var req projectRootRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	root, err := rootFromRequest(req)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	if h.projectRootWritesUseCairnlineAuthority() {
		project, _, err := h.updateProjectRootWithCairnlineAuthority(r.Context(), r.PathValue("id"), r.PathValue("root_id"), root)
		writeProjectRootCairnlineAuthorityResponse(w, http.StatusOK, project, err)
		return
	}
	project, updated, err := h.projectApplication().UpdateRoot(r.Context(), r.PathValue("id"), r.PathValue("root_id"), root)
	h.writeProjectRootMutationResponse(r.Context(), w, http.StatusOK, project, updated, false, err)
}

func (h *Handler) HandleDeleteProjectRoot(w http.ResponseWriter, r *http.Request) {
	if h.projectRootWritesUseCairnlineAuthority() {
		project, _, err := h.deleteProjectRootWithCairnlineAuthority(r.Context(), r.PathValue("id"), r.PathValue("root_id"))
		writeProjectRootCairnlineAuthorityResponse(w, http.StatusOK, project, err)
		return
	}
	project, deleted, err := h.projectApplication().DeleteRoot(r.Context(), r.PathValue("id"), r.PathValue("root_id"))
	h.writeProjectRootMutationResponse(r.Context(), w, http.StatusOK, project, deleted, true, err)
}

func (h *Handler) HandleCreateProjectContextSource(w http.ResponseWriter, r *http.Request) {
	var req projectContextSourceRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	source, err := contextSourceFromRequest(req)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	if source.ID == "" {
		source.ID = newOpaqueTaskResourceID("ctxsrc")
	}
	if h.projectContextSourceWritesUseCairnlineAuthority() {
		project, _, err := h.createProjectContextSourceWithCairnlineAuthority(r.Context(), r.PathValue("id"), source)
		writeProjectContextSourceCairnlineAuthorityResponse(w, http.StatusCreated, project, err)
		return
	}
	project, created, err := h.projectApplication().CreateContextSource(r.Context(), r.PathValue("id"), source)
	h.writeProjectContextSourceMutationResponse(r.Context(), w, http.StatusCreated, project, created, false, err)
}

func (h *Handler) HandleUpdateProjectContextSource(w http.ResponseWriter, r *http.Request) {
	var req projectContextSourceRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	source, err := contextSourceFromRequest(req)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	if h.projectContextSourceWritesUseCairnlineAuthority() {
		project, _, err := h.updateProjectContextSourceWithCairnlineAuthority(r.Context(), r.PathValue("id"), r.PathValue("source_id"), source)
		writeProjectContextSourceCairnlineAuthorityResponse(w, http.StatusOK, project, err)
		return
	}
	project, updated, err := h.projectApplication().UpdateContextSource(r.Context(), r.PathValue("id"), r.PathValue("source_id"), source)
	h.writeProjectContextSourceMutationResponse(r.Context(), w, http.StatusOK, project, updated, false, err)
}

func (h *Handler) HandleDeleteProjectContextSource(w http.ResponseWriter, r *http.Request) {
	if h.projectContextSourceWritesUseCairnlineAuthority() {
		project, _, err := h.deleteProjectContextSourceWithCairnlineAuthority(r.Context(), r.PathValue("id"), r.PathValue("source_id"))
		writeProjectContextSourceCairnlineAuthorityResponse(w, http.StatusOK, project, err)
		return
	}
	project, deleted, err := h.projectApplication().DeleteContextSource(r.Context(), r.PathValue("id"), r.PathValue("source_id"))
	h.writeProjectContextSourceMutationResponse(r.Context(), w, http.StatusOK, project, deleted, true, err)
}

func (h *Handler) HandleDeleteProject(w http.ResponseWriter, r *http.Request) {
	if h.projectIdentityWritesUseCairnlineAuthority() {
		result, err := h.deleteProjectWithCairnlineAuthority(r.Context(), r.PathValue("id"))
		h.writeProjectDeleteResponse(w, result, err)
		return
	}
	result, err := h.projectApplication().DeleteProject(r.Context(), r.PathValue("id"))
	if err == nil {
		h.mirrorProjectDeleteToCairnline(r.Context(), "project_delete", result.Project)
	}
	h.writeProjectDeleteResponse(w, result, err)
}

func (h *Handler) writeProjectDeleteResponse(w http.ResponseWriter, result projectapp.DeleteProjectResult, err error) {
	if errors.Is(err, projectapp.ErrProjectNotFound) {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project not found")
		return
	}
	if errors.Is(err, projectapp.ErrProjectDeleteConflict) {
		WriteError(w, http.StatusConflict, errCodeConflict, err.Error())
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, ProjectDeleteResponse{Object: "project_delete", Data: renderProjectDeleteResult(result)})
}

func (h *Handler) writeProjectRootMutationResponse(ctx context.Context, w http.ResponseWriter, status int, project projects.Project, root projects.Root, deleting bool, err error) {
	if errors.Is(err, projectapp.ErrProjectNotFound) {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project not found")
		return
	}
	if errors.Is(err, projectapp.ErrProjectRootNotFound) {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project root not found")
		return
	}
	if errors.Is(err, projectapp.ErrProjectRootConflict) || errors.Is(err, projects.ErrAlreadyExists) {
		WriteError(w, http.StatusConflict, errCodeConflict, err.Error())
		return
	}
	if errors.Is(err, projects.ErrInvalid) {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if deleting {
		h.mirrorProjectRootDeleteToCairnline(ctx, "project_root_mutation", project.ID, root.ID)
	} else {
		h.mirrorProjectRootToCairnline(ctx, "project_root_mutation", project, root)
	}
	WriteJSON(w, status, ProjectResponse{Object: "project", Data: renderProject(project)})
}

func (h *Handler) writeProjectContextSourceMutationResponse(ctx context.Context, w http.ResponseWriter, status int, project projects.Project, source projects.ContextSource, deleting bool, err error) {
	if errors.Is(err, projectapp.ErrProjectNotFound) {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project not found")
		return
	}
	if errors.Is(err, projectapp.ErrProjectContextSourceNotFound) {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project context source not found")
		return
	}
	if errors.Is(err, projectapp.ErrProjectContextSourceConflict) || errors.Is(err, projects.ErrAlreadyExists) {
		WriteError(w, http.StatusConflict, errCodeConflict, err.Error())
		return
	}
	if errors.Is(err, projects.ErrInvalid) {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if deleting {
		h.mirrorProjectContextSourceDeleteToCairnline(ctx, "project_context_source_mutation", project.ID, source.ID)
	} else {
		h.mirrorProjectContextSourceToCairnline(ctx, "project_context_source_mutation", project, source)
	}
	WriteJSON(w, status, ProjectResponse{Object: "project", Data: renderProject(project)})
}

func (h *Handler) deleteProjectChatSession(ctx context.Context, session chat.Session) (bool, error) {
	stopping, err := h.deleteExistingChatSession(ctx, session)
	if errors.Is(err, errChatSessionDeleteConflict) {
		return stopping, fmt.Errorf("%w: %v", projectapp.ErrProjectDeleteConflict, err)
	}
	return stopping, err
}

func projectFromCreateRequest(req createProjectRequest) (projects.Project, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return projects.Project{}, errors.New("project name is required")
	}
	roots, err := rootsFromRequest(req.Roots)
	if err != nil {
		return projects.Project{}, err
	}
	workspacePath := strings.TrimSpace(req.WorkspacePath)
	workspaceKind := strings.TrimSpace(req.WorkspaceKind)
	if workspacePath != "" {
		if len(roots) > 0 {
			return projects.Project{}, errors.New("workspace_path cannot be combined with roots")
		}
		if strings.TrimSpace(req.DefaultRootID) != "" {
			return projects.Project{}, errors.New("default_root_id cannot be supplied with workspace_path")
		}
		roots = []projects.Root{{
			Path:   workspacePath,
			Kind:   workspaceKind,
			Active: true,
		}}
	} else if workspaceKind != "" {
		return projects.Project{}, errors.New("workspace_kind requires workspace_path")
	}
	contextSources, err := contextSourcesFromRequest(req.ContextSources)
	if err != nil {
		return projects.Project{}, err
	}
	return projects.Project{
		Name:                     name,
		Description:              strings.TrimSpace(req.Description),
		Roots:                    roots,
		ContextSources:           contextSources,
		DefaultRootID:            strings.TrimSpace(req.DefaultRootID),
		DefaultProvider:          strings.TrimSpace(req.DefaultProvider),
		DefaultModel:             strings.TrimSpace(req.DefaultModel),
		DefaultAgentProfile:      strings.TrimSpace(req.DefaultAgentProfile),
		DefaultToolsEnabled:      cloneBool(req.DefaultToolsEnabled),
		DefaultWorkspaceMode:     strings.TrimSpace(req.DefaultWorkspaceMode),
		DefaultSystemPrompt:      strings.TrimSpace(req.DefaultSystemPrompt),
		DefaultCompactToolOutput: cloneBool(req.DefaultCompactToolOutput),
	}, nil
}

func rootsFromRequest(req []projectRootRequest) ([]projects.Root, error) {
	roots := make([]projects.Root, 0, len(req))
	for _, item := range req {
		root, err := rootFromRequest(item)
		if err != nil {
			return nil, err
		}
		roots = append(roots, root)
	}
	return roots, nil
}

func rootFromRequest(item projectRootRequest) (projects.Root, error) {
	path := strings.TrimSpace(item.Path)
	if path == "" {
		return projects.Root{}, errors.New("project root path is required")
	}
	active := true
	if item.Active != nil {
		active = *item.Active
	}
	return projects.Root{
		ID:        strings.TrimSpace(item.ID),
		Path:      path,
		Kind:      strings.TrimSpace(item.Kind),
		GitRemote: strings.TrimSpace(item.GitRemote),
		GitBranch: strings.TrimSpace(item.GitBranch),
		Active:    active,
	}, nil
}

func contextSourcesFromRequest(req []projectContextSourceRequest) ([]projects.ContextSource, error) {
	sources := make([]projects.ContextSource, 0, len(req))
	for _, item := range req {
		source, err := contextSourceFromRequest(item)
		if err != nil {
			return nil, err
		}
		sources = append(sources, source)
	}
	return sources, nil
}

func contextSourceFromRequest(item projectContextSourceRequest) (projects.ContextSource, error) {
	path := strings.TrimSpace(item.Path)
	if path == "" {
		return projects.ContextSource{}, errors.New("project context source path is required")
	}
	enabled := true
	if item.Enabled != nil {
		enabled = *item.Enabled
	}
	return projects.ContextSource{
		ID:             strings.TrimSpace(item.ID),
		Kind:           strings.TrimSpace(item.Kind),
		Title:          strings.TrimSpace(item.Title),
		Path:           path,
		Enabled:        enabled,
		Format:         strings.TrimSpace(item.Format),
		Scope:          strings.TrimSpace(item.Scope),
		TrustLabel:     strings.TrimSpace(item.TrustLabel),
		SourceCategory: strings.TrimSpace(item.SourceCategory),
		Metadata:       item.Metadata,
	}, nil
}

func validateProjectDefaultRoot(defaultRootID string, roots []projects.Root) error {
	if strings.TrimSpace(defaultRootID) == "" {
		return nil
	}
	if projectRootIDExists(defaultRootID, roots) {
		return nil
	}
	return fmt.Errorf("default_root_id %q must match a project root%s", strings.TrimSpace(defaultRootID), availableProjectRootIDsHint(roots))
}

func projectRootIDExists(id string, roots []projects.Root) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	for _, root := range roots {
		if root.ID == id {
			return true
		}
	}
	return false
}

func availableProjectRootIDsHint(roots []projects.Root) string {
	if len(roots) == 0 {
		return " (no roots configured)"
	}
	ids := make([]string, 0, len(roots))
	for _, root := range roots {
		id := strings.TrimSpace(root.ID)
		if id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return " (no root ids configured)"
	}
	return " (available: " + strings.Join(ids, ", ") + ")"
}

func parseProjectTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, errors.New("last_opened_at must be RFC3339 timestamp")
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, errors.New("last_opened_at must be RFC3339 timestamp")
	}
	return parsed.UTC(), nil
}

func (h *Handler) renderProjects(ctx context.Context) ([]ProjectResponseItem, error) {
	if h.requiresEmbeddedCairnlineProjectReads() {
		return h.renderStrictEmbeddedCairnlineProjects(ctx)
	}
	items, err := h.projects.List(ctx)
	if err != nil {
		return nil, err
	}
	if h.projectReadRoutesUseCairnlineReadModel() {
		return h.renderCairnlineProjects(ctx, items)
	}
	data := make([]ProjectResponseItem, 0, len(items))
	for _, item := range items {
		data = append(data, renderProject(item))
	}
	return data, nil
}

func (h *Handler) renderStrictEmbeddedCairnlineProjects(ctx context.Context) ([]ProjectResponseItem, error) {
	_, service, store, err := h.openCairnlineEmbeddedService(ctx)
	if errors.Is(err, cairnline.ErrNotFound) {
		return []ProjectResponseItem{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer store.Close()
	items, err := service.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	data := make([]ProjectResponseItem, 0, len(items))
	for _, item := range items {
		project, err := h.renderCairnlineProjectFromService(ctx, service, item, projects.Project{})
		if err != nil {
			return nil, err
		}
		data = append(data, project)
	}
	return data, nil
}

func (h *Handler) renderCairnlineProjects(ctx context.Context, nativeProjects []projects.Project) ([]ProjectResponseItem, error) {
	data := make([]ProjectResponseItem, 0, len(nativeProjects))
	for _, native := range nativeProjects {
		project, err := h.renderCairnlineProject(ctx, native)
		if err != nil {
			return nil, err
		}
		data = append(data, project)
	}
	return data, nil
}

func (h *Handler) renderProject(ctx context.Context, projectID string) (*ProjectResponseItem, error) {
	if h.requiresEmbeddedCairnlineProjectReads() {
		return h.renderStrictEmbeddedCairnlineProject(ctx, projectID)
	}
	project, ok, err := h.projects.Get(ctx, projectID)
	if err != nil || !ok {
		return nil, err
	}
	if h.projectReadRoutesUseCairnlineReadModel() {
		rendered, err := h.renderCairnlineProject(ctx, project)
		if err != nil {
			return nil, err
		}
		return &rendered, nil
	}
	rendered := renderProject(project)
	return &rendered, nil
}

func (h *Handler) renderStrictEmbeddedCairnlineProject(ctx context.Context, projectID string) (*ProjectResponseItem, error) {
	_, service, store, err := h.openCairnlineEmbeddedService(ctx)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	item, err := service.GetProject(ctx, projectID)
	if errors.Is(err, cairnline.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	project, err := h.renderCairnlineProjectFromService(ctx, service, item, projects.Project{})
	if err != nil {
		return nil, err
	}
	return &project, nil
}

func writeProjectReadRenderError(w http.ResponseWriter, err error) {
	WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
}

func (h *Handler) renderCairnlineProject(ctx context.Context, native projects.Project) (ProjectResponseItem, error) {
	view, err := h.cairnlineProjectWorkView(ctx, native.ID)
	if err != nil {
		return ProjectResponseItem{}, err
	}
	defer view.Close()
	project, err := view.service.GetProject(ctx, view.snapshot.Project.ID)
	if err != nil {
		return ProjectResponseItem{}, err
	}
	return h.renderCairnlineProjectFromService(ctx, view.service, project, native)
}

func (h *Handler) renderCairnlineProjectFromService(ctx context.Context, service *cairnline.Service, project cairnline.Project, native projects.Project) (ProjectResponseItem, error) {
	projected, err := h.projectWithHecateRuntimeOverlay(ctx, projectFromCairnline(project, native))
	if err != nil {
		return ProjectResponseItem{}, err
	}
	rendered := renderProject(projected)
	rendered.ReadBackend = "cairnline"
	return rendered, nil
}

func renderProject(project projects.Project) ProjectResponseItem {
	return renderProjectWithBackend(project, "hecate")
}

func renderProjectWithBackend(project projects.Project, readBackend string) ProjectResponseItem {
	roots := make([]ProjectRootResponseItem, 0, len(project.Roots))
	for _, root := range project.Roots {
		roots = append(roots, ProjectRootResponseItem{
			ID:        root.ID,
			Path:      root.Path,
			Kind:      root.Kind,
			GitRemote: root.GitRemote,
			GitBranch: root.GitBranch,
			Active:    root.Active,
			CreatedAt: formatOptionalTime(root.CreatedAt),
			UpdatedAt: formatOptionalTime(root.UpdatedAt),
		})
	}
	contextSources := make([]ProjectContextSourceResponseItem, 0, len(project.ContextSources))
	for _, source := range project.ContextSources {
		contextSources = append(contextSources, ProjectContextSourceResponseItem{
			ID:             source.ID,
			Kind:           source.Kind,
			Title:          source.Title,
			Path:           source.Path,
			Enabled:        source.Enabled,
			Format:         source.Format,
			Scope:          source.Scope,
			TrustLabel:     source.TrustLabel,
			SourceCategory: source.SourceCategory,
			Metadata:       cloneProjectContextMetadata(source.Metadata),
			CreatedAt:      formatOptionalTime(source.CreatedAt),
			UpdatedAt:      formatOptionalTime(source.UpdatedAt),
		})
	}
	return ProjectResponseItem{
		ID:                       project.ID,
		ReadBackend:              readBackend,
		Name:                     project.Name,
		Description:              project.Description,
		Roots:                    roots,
		ContextSources:           contextSources,
		DefaultRootID:            project.DefaultRootID,
		DefaultProvider:          project.DefaultProvider,
		DefaultModel:             project.DefaultModel,
		DefaultAgentProfile:      project.DefaultAgentProfile,
		DefaultToolsEnabled:      cloneBool(project.DefaultToolsEnabled),
		DefaultWorkspaceMode:     project.DefaultWorkspaceMode,
		DefaultSystemPrompt:      project.DefaultSystemPrompt,
		DefaultCompactToolOutput: cloneBool(project.DefaultCompactToolOutput),
		CreatedAt:                formatOptionalTime(project.CreatedAt),
		UpdatedAt:                formatOptionalTime(project.UpdatedAt),
		LastOpenedAt:             formatOptionalTime(project.LastOpenedAt),
	}
}

func projectFromCairnline(item cairnline.Project, native projects.Project) projects.Project {
	project := projects.Project{
		ID:                       item.ID,
		Name:                     item.Name,
		Description:              item.Description,
		Roots:                    projectRootsFromCairnline(item.Roots, native.Roots),
		ContextSources:           projectContextSourcesFromCairnline(item.ContextSources),
		DefaultRootID:            item.DefaultRootID,
		DefaultProvider:          native.DefaultProvider,
		DefaultModel:             native.DefaultModel,
		DefaultAgentProfile:      native.DefaultAgentProfile,
		DefaultToolsEnabled:      cloneBool(native.DefaultToolsEnabled),
		DefaultWorkspaceMode:     native.DefaultWorkspaceMode,
		DefaultSystemPrompt:      native.DefaultSystemPrompt,
		DefaultCompactToolOutput: cloneBool(native.DefaultCompactToolOutput),
		CreatedAt:                firstNonZeroTime(native.CreatedAt, item.CreatedAt),
		UpdatedAt:                firstNonZeroTime(native.UpdatedAt, item.UpdatedAt),
		LastOpenedAt:             native.LastOpenedAt,
	}
	return project
}

func projectRootsFromCairnline(items []cairnline.Root, native []projects.Root) []projects.Root {
	nativeByID := projectRootsByID(native)
	out := make([]projects.Root, 0, len(items))
	for _, item := range items {
		prior := nativeByID[item.ID]
		out = append(out, projects.Root{
			ID:        item.ID,
			Path:      item.Path,
			Kind:      item.Kind,
			GitRemote: item.GitRemote,
			GitBranch: item.GitBranch,
			Active:    item.Active,
			CreatedAt: prior.CreatedAt,
			UpdatedAt: prior.UpdatedAt,
		})
	}
	return out
}

func projectRootsByID(items []projects.Root) map[string]projects.Root {
	out := make(map[string]projects.Root, len(items))
	for _, item := range items {
		out[item.ID] = item
	}
	return out
}

func projectContextSourcesFromCairnline(items []cairnline.Source) []projects.ContextSource {
	out := make([]projects.ContextSource, 0, len(items))
	for _, item := range items {
		out = append(out, projects.ContextSource{
			ID:             item.ID,
			Kind:           item.Kind,
			Title:          item.Title,
			Path:           item.Locator,
			Enabled:        item.Enabled,
			Format:         item.Format,
			Scope:          item.Scope,
			TrustLabel:     item.TrustLabel,
			SourceCategory: item.SourceCategory,
			Metadata:       cloneProjectContextMetadata(item.Metadata),
			CreatedAt:      item.CreatedAt,
			UpdatedAt:      item.UpdatedAt,
		})
	}
	return out
}

func renderProjectDeleteResult(result projectapp.DeleteProjectResult) ProjectDeleteResponseItem {
	return ProjectDeleteResponseItem{
		ProjectID:                        result.Project.ID,
		ProjectName:                      result.Project.Name,
		ChatSessionsDeleted:              result.ChatSessionsDeleted,
		ProjectWorkRowsDeleted:           result.ProjectWorkRowsDeleted,
		ProjectRuntimeRowsDeleted:        result.ProjectRuntimeRowsDeleted,
		ProjectSkillsDeleted:             result.ProjectSkillsDeleted,
		ProjectAssistantProposalsDeleted: result.ProjectAssistantProposalsDeleted,
		MemoryEntriesDeleted:             result.MemoryEntriesDeleted,
		MemoryCandidatesDeleted:          result.MemoryCandidatesDeleted,
	}
}

func cloneProjectContextMetadata(items map[string]string) map[string]string {
	if len(items) == 0 {
		return nil
	}
	out := make(map[string]string, len(items))
	for key, value := range items {
		out[key] = value
	}
	return out
}

func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}
