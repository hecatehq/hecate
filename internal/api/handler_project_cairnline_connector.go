package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/mcp"
	mcpclient "github.com/hecatehq/hecate/internal/mcp/client"
	"github.com/hecatehq/hecate/internal/orchestrator"
	"github.com/hecatehq/hecate/internal/version"
	"github.com/hecatehq/hecate/pkg/types"
)

const projectCairnlineSidecarMCPServerName = "cairnline"

var projectCairnlineSidecarRequiredTools = []string{
	"projects.list",
	"projects.get",
	"projects.create",
	"roles.list",
	"work_items.create",
	"assignments.claim",
	"assignments.context",
	"assignments.complete",
	"evidence.record",
	"reviews.record",
	"handoffs.create",
	"memory_candidates.create",
	"assistant.propose",
}

func (h *Handler) projectCairnlineConnectorMode() string {
	if h == nil {
		return "embedded"
	}
	return h.config.ProjectsCairnlineConnector()
}

func (h *Handler) projectCairnlineEmbeddedConnectorEnabled() bool {
	return h != nil &&
		h.config.ProjectsCoordinationBackend() == "cairnline" &&
		h.projectCairnlineConnectorMode() == "embedded"
}

func projectCairnlineConnectorReady(mode string) bool {
	return mode == "embedded"
}

func projectCairnlineConnectorDetail(mode string) string {
	switch mode {
	case "sidecar":
		return "Cairnline sidecar connector is configured and can be connected through the local-only sidecar connect endpoint, but Hecate does not yet route Projects reads or writes through the standalone Cairnline MCP client."
	default:
		return "Hecate is using the embedded Cairnline Go package bridge for replacement-readiness dogfood."
	}
}

func projectCairnlineConnectorWarning(mode string) string {
	if mode != "sidecar" {
		return ""
	}
	return "HECATE_PROJECTS_CAIRNLINE_CONNECTOR=sidecar enables standalone Cairnline MCP probe/connect/read-smoke surfaces only; Cairnline read/write routing stays disabled until Hecate has a sidecar Projects backend adapter."
}

func (h *Handler) HandleProjectCairnlineSidecarProbe(w http.ResponseWriter, r *http.Request) {
	if !requireLoopbackClient(w, r, "Cairnline sidecar probe") {
		return
	}
	WriteJSON(w, http.StatusOK, ProjectCairnlineSidecarProbeEnvelope{
		Object: "project_cairnline_sidecar_probe",
		Data:   h.projectCairnlineSidecarProbe(r.Context()),
	})
}

func (h *Handler) HandleProjectCairnlineSidecarConnect(w http.ResponseWriter, r *http.Request) {
	if !requireLoopbackClient(w, r, "Cairnline sidecar connect") {
		return
	}
	WriteJSON(w, http.StatusOK, ProjectCairnlineSidecarClientEnvelope{
		Object: "project_cairnline_sidecar_client",
		Data:   h.projectCairnlineSidecarConnect(r.Context()),
	})
}

func (h *Handler) HandleProjectCairnlineSidecarReadSmoke(w http.ResponseWriter, r *http.Request) {
	if !requireLoopbackClient(w, r, "Cairnline sidecar read smoke") {
		return
	}
	WriteJSON(w, http.StatusOK, ProjectCairnlineSidecarReadEnvelope{
		Object: "project_cairnline_sidecar_read",
		Data:   h.projectCairnlineSidecarReadSmoke(r.Context()),
	})
}

func (h *Handler) HandleProjectCairnlineSidecarDetailSmoke(w http.ResponseWriter, r *http.Request) {
	if !requireLoopbackClient(w, r, "Cairnline sidecar detail smoke") {
		return
	}
	var req ProjectCairnlineSidecarDetailRequest
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	WriteJSON(w, http.StatusOK, ProjectCairnlineSidecarDetailEnvelope{
		Object: "project_cairnline_sidecar_detail",
		Data:   h.projectCairnlineSidecarDetailSmoke(r.Context(), req),
	})
}

func (h *Handler) projectCairnlineSidecarProbe(ctx context.Context) ProjectCairnlineSidecarProbeResponse {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, dbPath, timeout := h.projectCairnlineSidecarMCPConfig()
	response := ProjectCairnlineSidecarProbeResponse{
		Ready:          false,
		Status:         "sidecar_probe_not_run",
		Detail:         "Cairnline sidecar probe has not run.",
		Command:        cfg.Command,
		Args:           append([]string(nil), cfg.Args...),
		DatabasePath:   dbPath,
		ProbeTimeoutMS: timeout.Milliseconds(),
		RequiredTools:  append([]string(nil), projectCairnlineSidecarRequiredTools...),
	}
	if h == nil {
		response.Status = "sidecar_probe_failed"
		response.Detail = "Cairnline sidecar probe requires an API handler."
		return response
	}
	if h.projectCairnlineConnectorMode() != "sidecar" {
		response.Warnings = append(response.Warnings, "HECATE_PROJECTS_CAIRNLINE_CONNECTOR is not sidecar; this probe does not affect live Projects routing.")
	}
	if dbPath != "" && len(h.config.ProjectsCairnlineSidecarArgs()) > 0 {
		response.Warnings = append(response.Warnings, "HECATE_PROJECTS_CAIRNLINE_SIDECAR_ARGS is set, so HECATE_PROJECTS_CAIRNLINE_SIDECAR_DB is reported but not appended automatically.")
	}

	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := orchestrator.ProbeMCPServer(probeCtx, cfg, h.secretCipher)
	if err != nil {
		response.Status = "sidecar_probe_failed"
		response.Detail = err.Error()
		return response
	}
	response.Tools = renderMCPProbeTools(result.Tools)
	response.ToolCount = len(response.Tools)
	response.MissingTools = projectCairnlineSidecarMissingTools(projectCairnlineSidecarToolNames(response.Tools))
	if len(response.MissingTools) > 0 {
		response.Status = "sidecar_contract_incomplete"
		response.Detail = "Cairnline sidecar MCP server started, but it does not expose every tool Hecate needs for a future Projects backend connector."
		return response
	}
	response.Ready = true
	response.Status = "sidecar_probe_ready"
	response.Detail = "Cairnline sidecar MCP server started and exposes the required portable Projects tool contract. Hecate still keeps live Projects reads and writes on Hecate-native stores in sidecar mode."
	return response
}

func (h *Handler) projectCairnlineSidecarConnect(ctx context.Context) ProjectCairnlineSidecarProbeResponse {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, dbPath, timeout := h.projectCairnlineSidecarMCPConfig()
	response := ProjectCairnlineSidecarProbeResponse{
		Ready:                 false,
		Status:                "sidecar_client_not_connected",
		Detail:                "Cairnline sidecar client has not connected.",
		Command:               cfg.Command,
		Args:                  append([]string(nil), cfg.Args...),
		DatabasePath:          dbPath,
		ProbeTimeoutMS:        timeout.Milliseconds(),
		RequiredTools:         append([]string(nil), projectCairnlineSidecarRequiredTools...),
		PersistentClient:      true,
		ClientCacheConfigured: h != nil,
	}
	if h == nil {
		response.Status = "sidecar_client_failed"
		response.Detail = "Cairnline sidecar client requires an API handler."
		return response
	}
	if h.projectCairnlineConnectorMode() != "sidecar" {
		response.Warnings = append(response.Warnings, "HECATE_PROJECTS_CAIRNLINE_CONNECTOR is not sidecar; this client does not affect live Projects routing.")
	}
	if dbPath != "" && len(h.config.ProjectsCairnlineSidecarArgs()) > 0 {
		response.Warnings = append(response.Warnings, "HECATE_PROJECTS_CAIRNLINE_SIDECAR_ARGS is set, so HECATE_PROJECTS_CAIRNLINE_SIDECAR_DB is reported but not appended automatically.")
	}

	cache := h.projectCairnlineSidecarMCPClientCache()
	connectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := orchestrator.ProbeCachedMCPServer(connectCtx, cfg, h.secretCipher, cache)
	if err != nil {
		response.Status = "sidecar_client_failed"
		response.Detail = err.Error()
		response.setSidecarCacheStats(cache.Stats())
		return response
	}
	response.Tools = renderMCPProbeTools(result.Tools)
	response.ToolCount = len(response.Tools)
	response.MissingTools = projectCairnlineSidecarMissingTools(projectCairnlineSidecarToolNames(response.Tools))
	response.setSidecarCacheStats(cache.Stats())
	if len(response.MissingTools) > 0 {
		response.Status = "sidecar_contract_incomplete"
		response.Detail = "Cairnline sidecar MCP client connected, but it does not expose every tool Hecate needs for a future Projects backend connector."
		return response
	}
	response.Ready = true
	response.Status = "sidecar_client_ready"
	response.Detail = "Cairnline sidecar MCP client connected and exposes the required portable Projects tool contract. Hecate still keeps live Projects reads and writes on Hecate-native stores in sidecar mode."
	return response
}

func (h *Handler) projectCairnlineSidecarReadSmoke(ctx context.Context) ProjectCairnlineSidecarReadResponse {
	const toolName = "projects.list"
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, dbPath, timeout := h.projectCairnlineSidecarMCPConfig()
	response := ProjectCairnlineSidecarReadResponse{
		Ready:                 false,
		Status:                "sidecar_read_not_run",
		Detail:                "Cairnline sidecar read smoke has not run.",
		Command:               cfg.Command,
		Args:                  append([]string(nil), cfg.Args...),
		DatabasePath:          dbPath,
		ProbeTimeoutMS:        timeout.Milliseconds(),
		PersistentClient:      true,
		ClientCacheConfigured: h != nil,
		Tool:                  toolName,
		ReadOnly:              true,
	}
	if h == nil {
		response.Status = "sidecar_read_failed"
		response.Detail = "Cairnline sidecar read smoke requires an API handler."
		return response
	}
	if h.projectCairnlineConnectorMode() != "sidecar" {
		response.Warnings = append(response.Warnings, "HECATE_PROJECTS_CAIRNLINE_CONNECTOR is not sidecar; this read smoke does not affect live Projects routing.")
	}
	if dbPath != "" && len(h.config.ProjectsCairnlineSidecarArgs()) > 0 {
		response.Warnings = append(response.Warnings, "HECATE_PROJECTS_CAIRNLINE_SIDECAR_ARGS is set, so HECATE_PROJECTS_CAIRNLINE_SIDECAR_DB is reported but not appended automatically.")
	}

	cache := h.projectCairnlineSidecarMCPClientCache()
	readCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := orchestrator.CallCachedMCPServerTool(readCtx, cfg, h.secretCipher, cache, toolName, json.RawMessage(`{}`))
	if err != nil {
		response.Status = "sidecar_read_failed"
		response.Detail = err.Error()
		response.setSidecarCacheStats(cache.Stats())
		return response
	}
	response.ToolText = result.Text
	response.ToolIsError = result.IsError
	response.StructuredContent = result.Result.StructuredContent
	response.Meta = result.Result.Meta
	response.setSidecarCacheStats(cache.Stats())
	if result.IsError {
		response.Status = "sidecar_read_tool_failed"
		response.Detail = "Cairnline sidecar projects.list returned a tool-level error. Hecate still keeps live Projects reads and writes on Hecate-native stores in sidecar mode."
		return response
	}
	structuredProjects, structuredReady, structuredErr := projectCairnlineSidecarStructuredProjects(result.Result.StructuredContent)
	response.StructuredReady = structuredReady
	response.StructuredProjectCount = len(structuredProjects)
	response.StructuredProjects = structuredProjects
	if structuredErr != nil {
		response.StructuredParseError = structuredErr.Error()
		response.Warnings = append(response.Warnings, "Cairnline sidecar projects.list returned structuredContent that Hecate could not parse as a project list.")
	} else if !structuredReady {
		response.Warnings = append(response.Warnings, "Cairnline sidecar projects.list did not return structuredContent; Hecate verified the tool call but not a typed project-list contract.")
	}
	response.Ready = true
	response.Status = "sidecar_read_ready"
	response.Detail = "Hecate called the read-only Cairnline sidecar projects.list tool through the persistent sidecar client. Hecate still keeps live Projects reads and writes on Hecate-native stores in sidecar mode."
	return response
}

func (h *Handler) projectCairnlineSidecarDetailSmoke(ctx context.Context, req ProjectCairnlineSidecarDetailRequest) ProjectCairnlineSidecarDetailResponse {
	const (
		listToolName   = "projects.list"
		detailToolName = "projects.get"
	)
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, dbPath, timeout := h.projectCairnlineSidecarMCPConfig()
	requestedProjectID := strings.TrimSpace(req.ProjectID)
	response := ProjectCairnlineSidecarDetailResponse{
		Ready:                 false,
		Status:                "sidecar_detail_not_run",
		Detail:                "Cairnline sidecar detail smoke has not run.",
		Command:               cfg.Command,
		Args:                  append([]string(nil), cfg.Args...),
		DatabasePath:          dbPath,
		ProbeTimeoutMS:        timeout.Milliseconds(),
		PersistentClient:      true,
		ClientCacheConfigured: h != nil,
		Tool:                  detailToolName,
		ReadOnly:              true,
		RequestedProjectID:    requestedProjectID,
	}
	if h == nil {
		response.Status = "sidecar_detail_failed"
		response.Detail = "Cairnline sidecar detail smoke requires an API handler."
		return response
	}
	if h.projectCairnlineConnectorMode() != "sidecar" {
		response.Warnings = append(response.Warnings, "HECATE_PROJECTS_CAIRNLINE_CONNECTOR is not sidecar; this detail smoke does not affect live Projects routing.")
	}
	if dbPath != "" && len(h.config.ProjectsCairnlineSidecarArgs()) > 0 {
		response.Warnings = append(response.Warnings, "HECATE_PROJECTS_CAIRNLINE_SIDECAR_ARGS is set, so HECATE_PROJECTS_CAIRNLINE_SIDECAR_DB is reported but not appended automatically.")
	}

	cache := h.projectCairnlineSidecarMCPClientCache()
	detailCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	projectID := requestedProjectID
	if projectID == "" {
		listResult, err := orchestrator.CallCachedMCPServerTool(detailCtx, cfg, h.secretCipher, cache, listToolName, json.RawMessage(`{}`))
		if err != nil {
			response.Status = "sidecar_detail_failed"
			response.Detail = err.Error()
			response.setSidecarCacheStats(cache.Stats())
			return response
		}
		response.ListToolText = listResult.Text
		response.ListToolIsError = listResult.IsError
		response.ListStructuredContent = listResult.Result.StructuredContent
		response.ListMeta = listResult.Result.Meta
		if listResult.IsError {
			response.Status = "sidecar_detail_list_tool_failed"
			response.Detail = "Cairnline sidecar projects.list returned a tool-level error before Hecate could select a project for projects.get. Hecate still keeps live Projects reads and writes on Hecate-native stores in sidecar mode."
			response.setSidecarCacheStats(cache.Stats())
			return response
		}
		projects, structuredReady, structuredErr := projectCairnlineSidecarStructuredProjects(listResult.Result.StructuredContent)
		response.ListStructuredReady = structuredReady
		response.ListProjectCount = len(projects)
		if structuredErr != nil {
			response.ListStructuredParseError = structuredErr.Error()
			response.Warnings = append(response.Warnings, "Cairnline sidecar projects.list returned structuredContent that Hecate could not parse as a project list.")
		} else if !structuredReady {
			response.Warnings = append(response.Warnings, "Cairnline sidecar projects.list did not return structuredContent, so Hecate could not select a project for projects.get.")
		} else if len(projects) > 0 {
			projectID = strings.TrimSpace(projects[0].ID)
			response.SelectedProjectSource = "projects.list"
		}
		if projectID == "" {
			response.Status = "sidecar_detail_no_project"
			response.Detail = "Hecate called Cairnline sidecar projects.list through the persistent sidecar client, but no typed project id was available for projects.get."
			response.setSidecarCacheStats(cache.Stats())
			return response
		}
	} else {
		response.SelectedProjectSource = "request"
	}
	response.SelectedProjectID = projectID

	args, err := json.Marshal(map[string]string{"id": projectID})
	if err != nil {
		response.Status = "sidecar_detail_failed"
		response.Detail = err.Error()
		response.setSidecarCacheStats(cache.Stats())
		return response
	}
	result, err := orchestrator.CallCachedMCPServerTool(detailCtx, cfg, h.secretCipher, cache, detailToolName, args)
	if err != nil {
		response.Status = "sidecar_detail_failed"
		response.Detail = err.Error()
		response.setSidecarCacheStats(cache.Stats())
		return response
	}
	response.ToolText = result.Text
	response.ToolIsError = result.IsError
	response.StructuredContent = result.Result.StructuredContent
	response.Meta = result.Result.Meta
	response.setSidecarCacheStats(cache.Stats())
	if result.IsError {
		response.Status = "sidecar_detail_tool_failed"
		response.Detail = "Cairnline sidecar projects.get returned a tool-level error. Hecate still keeps live Projects reads and writes on Hecate-native stores in sidecar mode."
		return response
	}
	structuredProject, structuredReady, structuredErr := projectCairnlineSidecarStructuredProject(result.Result.StructuredContent)
	response.StructuredReady = structuredReady
	response.StructuredProject = structuredProject
	if structuredErr != nil {
		response.StructuredParseError = structuredErr.Error()
		response.Warnings = append(response.Warnings, "Cairnline sidecar projects.get returned structuredContent that Hecate could not parse as a project.")
	} else if !structuredReady {
		response.Warnings = append(response.Warnings, "Cairnline sidecar projects.get did not return structuredContent; Hecate verified the tool call but not a typed project-detail contract.")
	} else if structuredProject.ID != projectID {
		response.Warnings = append(response.Warnings, "Cairnline sidecar projects.get returned a project id different from the requested id.")
	}
	response.Ready = true
	response.Status = "sidecar_detail_ready"
	response.Detail = "Hecate called the read-only Cairnline sidecar projects.get tool through the persistent sidecar client. Hecate still keeps live Projects reads and writes on Hecate-native stores in sidecar mode."
	return response
}

func projectCairnlineSidecarStructuredProjects(raw json.RawMessage) ([]ProjectCairnlineSidecarProjectItem, bool, error) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, false, nil
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return []ProjectCairnlineSidecarProjectItem{}, true, nil
	}
	var projects []ProjectCairnlineSidecarProjectItem
	if err := json.Unmarshal(trimmed, &projects); err != nil {
		return nil, false, err
	}
	if projects == nil {
		projects = []ProjectCairnlineSidecarProjectItem{}
	}
	return projects, true, nil
}

func projectCairnlineSidecarStructuredProject(raw json.RawMessage) (ProjectCairnlineSidecarProjectItem, bool, error) {
	if len(raw) == 0 {
		return ProjectCairnlineSidecarProjectItem{}, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ProjectCairnlineSidecarProjectItem{}, false, nil
	}
	var project ProjectCairnlineSidecarProjectItem
	if err := json.Unmarshal(trimmed, &project); err != nil {
		return ProjectCairnlineSidecarProjectItem{}, false, err
	}
	return project, true, nil
}

func decodeOptionalJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "request body must be readable")
		return false
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return true
	}
	if err := json.Unmarshal(raw, v); err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "request body must be valid JSON")
		return false
	}
	return true
}

func (h *Handler) projectCairnlineSidecarMCPClientCache() *mcpclient.SharedClientCache {
	if h == nil {
		return nil
	}
	h.projectCairnlineSidecarMu.Lock()
	defer h.projectCairnlineSidecarMu.Unlock()
	if h.projectCairnlineSidecarCache == nil {
		h.projectCairnlineSidecarCache = mcpclient.NewSharedClientCacheWithOptions(mcpclient.SharedClientCacheOptions{
			MaxEntries: 1,
			Info: mcp.ClientInfo{
				Name:    "hecate-cairnline-sidecar",
				Version: version.Version,
			},
		})
	}
	return h.projectCairnlineSidecarCache
}

func (r *ProjectCairnlineSidecarProbeResponse) setSidecarCacheStats(stats mcpclient.CacheStats) {
	if r == nil {
		return
	}
	r.ClientCacheEntries = stats.Entries
	r.ClientCacheInUse = stats.InUse
	r.ClientCacheIdle = stats.Idle
}

func (r *ProjectCairnlineSidecarReadResponse) setSidecarCacheStats(stats mcpclient.CacheStats) {
	if r == nil {
		return
	}
	r.ClientCacheEntries = stats.Entries
	r.ClientCacheInUse = stats.InUse
	r.ClientCacheIdle = stats.Idle
}

func (r *ProjectCairnlineSidecarDetailResponse) setSidecarCacheStats(stats mcpclient.CacheStats) {
	if r == nil {
		return
	}
	r.ClientCacheEntries = stats.Entries
	r.ClientCacheInUse = stats.InUse
	r.ClientCacheIdle = stats.Idle
}

func (h *Handler) projectCairnlineSidecarMCPConfig() (types.MCPServerConfig, string, time.Duration) {
	cfg := types.MCPServerConfig{
		Name:    projectCairnlineSidecarMCPServerName,
		Command: "cairnline",
	}
	timeout := 10 * time.Second
	if h == nil {
		return cfg, "", timeout
	}
	cfg.Command = h.config.ProjectsCairnlineSidecarCommand()
	cfg.Args = h.config.ProjectsCairnlineSidecarArgs()
	dbPath := h.cairnlineSidecarDatabasePath()
	if len(cfg.Args) == 0 && dbPath != "" {
		cfg.Args = []string{"-db", dbPath}
	}
	return cfg, dbPath, h.config.ProjectsCairnlineSidecarProbeTimeout()
}

func (h *Handler) cairnlineSidecarDatabasePath() string {
	if h == nil {
		return ""
	}
	path := h.config.ProjectsCairnlineSidecarDatabasePath()
	if path == "" {
		return ""
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	dataDir := strings.TrimSpace(h.config.Server.DataDir)
	if dataDir == "" {
		dataDir = ".data"
	}
	path = filepath.Join(dataDir, path)
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return path
}

func projectCairnlineSidecarToolNames(tools []MCPProbeToolDescriptor) []string {
	out := make([]string, 0, len(tools))
	seen := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func projectCairnlineSidecarMissingTools(toolNames []string) []string {
	seen := make(map[string]struct{}, len(toolNames))
	for _, name := range toolNames {
		seen[name] = struct{}{}
	}
	var missing []string
	for _, name := range projectCairnlineSidecarRequiredTools {
		if _, ok := seen[name]; !ok {
			missing = append(missing, name)
		}
	}
	return missing
}
