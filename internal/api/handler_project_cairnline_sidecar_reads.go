package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	mcpclient "github.com/hecatehq/hecate/internal/mcp/client"
	"github.com/hecatehq/hecate/internal/orchestrator"
)

var errProjectCairnlineSidecarReadFailed = errors.New("cairnline sidecar project read failed")

func (h *Handler) projectCairnlineSidecarProjectReadsEnabled() bool {
	return h != nil &&
		h.config.ProjectsCoordinationBackend() == "cairnline" &&
		h.projectCairnlineConnectorMode() == "sidecar" &&
		h.config.ProjectsCairnlineReadSource() == "sidecar"
}

func (h *Handler) renderCairnlineSidecarProjects(ctx context.Context) ([]ProjectResponseItem, error) {
	result, err := h.callProjectCairnlineSidecarProjectReadTool(ctx, "projects.list", map[string]string{})
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return nil, projectCairnlineSidecarReadFailure("projects.list returned a tool-level error: " + strings.TrimSpace(result.Text))
	}
	projects, structuredReady, structuredErr := projectCairnlineSidecarStructuredProjects(result.Result.StructuredContent)
	if structuredErr != nil {
		return nil, projectCairnlineSidecarReadFailure("projects.list structuredContent parse failed: " + structuredErr.Error())
	}
	if !structuredReady {
		return nil, projectCairnlineSidecarReadFailure("projects.list did not return typed structuredContent")
	}
	data := make([]ProjectResponseItem, 0, len(projects))
	for _, project := range projects {
		data = append(data, renderProjectFromCairnlineSidecar(project))
	}
	return data, nil
}

func (h *Handler) renderCairnlineSidecarProject(ctx context.Context, projectID string) (*ProjectResponseItem, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, nil
	}
	result, err := h.callProjectCairnlineSidecarProjectReadTool(ctx, "projects.get", map[string]string{"id": projectID})
	if err != nil {
		return nil, err
	}
	if result.IsError {
		if projectCairnlineSidecarToolErrorIsNotFound(result.Text) {
			return nil, nil
		}
		return nil, projectCairnlineSidecarReadFailure("projects.get returned a tool-level error: " + strings.TrimSpace(result.Text))
	}
	project, structuredReady, structuredErr := projectCairnlineSidecarStructuredProject(result.Result.StructuredContent)
	if structuredErr != nil {
		return nil, projectCairnlineSidecarReadFailure("projects.get structuredContent parse failed: " + structuredErr.Error())
	}
	if !structuredReady {
		return nil, projectCairnlineSidecarReadFailure("projects.get did not return typed structuredContent")
	}
	rendered := renderProjectFromCairnlineSidecar(project)
	return &rendered, nil
}

func (h *Handler) callProjectCairnlineSidecarProjectReadTool(ctx context.Context, toolName string, args any) (*orchestrator.CachedMCPToolCallResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if h == nil {
		return nil, projectCairnlineSidecarReadFailure("handler is not configured")
	}
	cfg, _, timeout := h.projectCairnlineSidecarMCPConfig()
	rawArgs, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	cache := h.projectCairnlineSidecarMCPClientCache()
	readCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := orchestrator.CallCachedMCPServerTool(readCtx, cfg, h.secretCipher, cache, toolName, rawArgs)
	if err != nil {
		if projectCairnlineSidecarReadErrShouldEvict(err) {
			cache.Evict(mcpclient.ServerConfig{
				Name:    cfg.Name,
				Command: cfg.Command,
				Args:    cfg.Args,
				Env:     cfg.Env,
				URL:     cfg.URL,
				Headers: cfg.Headers,
			})
			result, err = orchestrator.CallCachedMCPServerTool(readCtx, cfg, h.secretCipher, cache, toolName, rawArgs)
		}
	}
	if err != nil {
		return nil, projectCairnlineSidecarReadFailure(err.Error())
	}
	return result, nil
}

func renderProjectFromCairnlineSidecar(project ProjectCairnlineSidecarProjectItem) ProjectResponseItem {
	roots := make([]ProjectRootResponseItem, 0, len(project.Roots))
	for _, root := range project.Roots {
		roots = append(roots, ProjectRootResponseItem{
			ID:        root.ID,
			Path:      root.Path,
			Kind:      root.Kind,
			GitRemote: root.GitRemote,
			GitBranch: root.GitBranch,
			Active:    root.Active,
		})
	}
	contextSources := make([]ProjectContextSourceResponseItem, 0, len(project.ContextSources))
	for _, source := range project.ContextSources {
		contextSources = append(contextSources, ProjectContextSourceResponseItem{
			ID:             source.ID,
			Kind:           source.Kind,
			Title:          source.Title,
			Path:           source.Locator,
			Enabled:        source.Enabled,
			Format:         source.Format,
			Scope:          source.Scope,
			TrustLabel:     source.TrustLabel,
			SourceCategory: source.SourceCategory,
			Metadata:       cloneProjectContextMetadata(source.Metadata),
		})
	}
	return ProjectResponseItem{
		ID:                  project.ID,
		ReadBackend:         "cairnline",
		Name:                project.Name,
		Description:         project.Description,
		Roots:               roots,
		ContextSources:      contextSources,
		DefaultRootID:       project.DefaultRootID,
		DefaultAgentProfile: project.DefaultProfileID,
		CreatedAt:           project.CreatedAt,
		UpdatedAt:           project.UpdatedAt,
	}
}

func projectCairnlineSidecarToolErrorIsNotFound(text string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(text)), "not found")
}

func projectCairnlineSidecarReadErrShouldEvict(err error) bool {
	return errors.Is(err, mcpclient.ErrClientClosed) || mcpclient.IsTransportClosedErr(err)
}

func projectCairnlineSidecarReadFailure(message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		message = "unknown failure"
	}
	return fmt.Errorf("%w: %s", errProjectCairnlineSidecarReadFailed, message)
}
