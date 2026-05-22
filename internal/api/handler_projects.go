package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/hecate/agent-runtime/internal/projects"
)

type projectRootRequest struct {
	ID        string `json:"id,omitempty"`
	Path      string `json:"path"`
	Kind      string `json:"kind,omitempty"`
	GitRemote string `json:"git_remote,omitempty"`
	GitBranch string `json:"git_branch,omitempty"`
	Active    *bool  `json:"active,omitempty"`
}

type createProjectRequest struct {
	Name                     string               `json:"name"`
	Description              string               `json:"description,omitempty"`
	Roots                    []projectRootRequest `json:"roots,omitempty"`
	DefaultRootID            string               `json:"default_root_id,omitempty"`
	DefaultProvider          string               `json:"default_provider,omitempty"`
	DefaultModel             string               `json:"default_model,omitempty"`
	DefaultAgentProfile      string               `json:"default_agent_profile,omitempty"`
	DefaultToolsEnabled      *bool                `json:"default_tools_enabled,omitempty"`
	DefaultWorkspaceMode     string               `json:"default_workspace_mode,omitempty"`
	DefaultSystemPrompt      string               `json:"default_system_prompt,omitempty"`
	DefaultCompactToolOutput *bool                `json:"default_compact_tool_output,omitempty"`
}

type updateProjectRequest struct {
	Name                     *string               `json:"name,omitempty"`
	Description              *string               `json:"description,omitempty"`
	Roots                    *[]projectRootRequest `json:"roots,omitempty"`
	DefaultRootID            *string               `json:"default_root_id,omitempty"`
	DefaultProvider          *string               `json:"default_provider,omitempty"`
	DefaultModel             *string               `json:"default_model,omitempty"`
	DefaultAgentProfile      *string               `json:"default_agent_profile,omitempty"`
	DefaultToolsEnabled      *bool                 `json:"default_tools_enabled,omitempty"`
	DefaultWorkspaceMode     *string               `json:"default_workspace_mode,omitempty"`
	DefaultSystemPrompt      *string               `json:"default_system_prompt,omitempty"`
	DefaultCompactToolOutput *bool                 `json:"default_compact_tool_output,omitempty"`
	LastOpenedAt             *string               `json:"last_opened_at,omitempty"`
}

func (h *Handler) HandleProjects(w http.ResponseWriter, r *http.Request) {
	items, err := h.projects.List(r.Context())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	data := make([]ProjectResponseItem, 0, len(items))
	for _, item := range items {
		data = append(data, renderProject(item))
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
	if project.DefaultRootID == "" && len(project.Roots) > 0 {
		project.DefaultRootID = project.Roots[0].ID
	}
	if err := validateProjectDefaultRoot(project.DefaultRootID, project.Roots); err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	project, err = h.projects.Create(r.Context(), project)
	if errors.Is(err, projects.ErrInvalid) {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusCreated, ProjectResponse{Object: "project", Data: renderProject(project)})
}

func (h *Handler) HandleProject(w http.ResponseWriter, r *http.Request) {
	project, ok, err := h.projects.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project not found")
		return
	}
	WriteJSON(w, http.StatusOK, ProjectResponse{Object: "project", Data: renderProject(project)})
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
	var lastOpenedAt time.Time
	if req.LastOpenedAt != nil {
		var err error
		lastOpenedAt, err = parseProjectTime(*req.LastOpenedAt)
		if err != nil {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
			return
		}
	}
	if req.DefaultRootID != nil {
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
	})
	if errors.Is(err, projects.ErrNotFound) {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project not found")
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
	WriteJSON(w, http.StatusOK, ProjectResponse{Object: "project", Data: renderProject(project)})
}

func (h *Handler) HandleDeleteProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok, err := h.projects.Get(r.Context(), id); err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	} else if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project not found")
		return
	}
	if err := h.deleteProjectChats(r.Context(), id); err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	err := h.projects.Delete(r.Context(), id)
	if errors.Is(err, projects.ErrNotFound) {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project not found")
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) deleteProjectChats(ctx context.Context, projectID string) error {
	if h.agentChat == nil {
		return nil
	}
	return h.agentChat.DeleteByProjectID(ctx, projectID)
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
	return projects.Project{
		Name:                     name,
		Description:              strings.TrimSpace(req.Description),
		Roots:                    roots,
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
		path := strings.TrimSpace(item.Path)
		if path == "" {
			return nil, errors.New("project root path is required")
		}
		active := true
		if item.Active != nil {
			active = *item.Active
		}
		roots = append(roots, projects.Root{
			ID:        strings.TrimSpace(item.ID),
			Path:      path,
			Kind:      strings.TrimSpace(item.Kind),
			GitRemote: strings.TrimSpace(item.GitRemote),
			GitBranch: strings.TrimSpace(item.GitBranch),
			Active:    active,
		})
	}
	return roots, nil
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

func renderProject(project projects.Project) ProjectResponseItem {
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
	return ProjectResponseItem{
		ID:                       project.ID,
		Name:                     project.Name,
		Description:              project.Description,
		Roots:                    roots,
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

func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}
