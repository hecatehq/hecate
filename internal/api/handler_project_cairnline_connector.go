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
	"projects.update",
	"projects.delete",
	"roots.list",
	"roots.create",
	"roots.update",
	"roots.delete",
	"context_sources.list",
	"context_sources.create",
	"context_sources.update",
	"context_sources.delete",
	"projects.operations_brief",
	"projects.setup_readiness",
	"projects.health",
	"projects.activity",
	"assistant.propose",
	"assistant.proposals.list",
	"assistant.proposals.get",
	"assistant.apply",
	"profiles.list",
	"profiles.create",
	"profiles.update",
	"profiles.delete",
	"execution_profiles.list",
	"execution_profiles.create",
	"execution_profiles.update",
	"execution_profiles.delete",
	"skills.list",
	"skills.discover",
	"skills.create",
	"skills.update",
	"roles.list",
	"roles.create",
	"roles.update",
	"roles.delete",
	"work_items.list",
	"work_items.get",
	"work_items.create",
	"work_items.update",
	"work_items.delete",
	"work_items.closeout_readiness",
	"assignments.list",
	"assignments.get",
	"assignments.next",
	"assignments.create",
	"assignments.update",
	"assignments.claim",
	"assignments.release",
	"assignments.update_status",
	"assignments.context",
	"assignments.launch_packet",
	"assignments.complete",
	"assignments.delete",
	"artifacts.list",
	"artifacts.get",
	"artifacts.create",
	"evidence.list",
	"evidence.get",
	"evidence.record",
	"reviews.list",
	"reviews.get",
	"reviews.record",
	"handoffs.create",
	"handoffs.list",
	"handoffs.get",
	"handoffs.update",
	"handoffs.update_status",
	"handoffs.delete",
	"memory_entries.list",
	"memory_entries.get",
	"memory_entries.create",
	"memory_entries.update",
	"memory_entries.delete",
	"memory_candidates.create",
	"memory_candidates.list",
	"memory_candidates.get",
	"memory_candidates.promote",
	"memory_candidates.reject",
	"memory_candidates.delete",
}

type projectCairnlineSidecarCoordinationTool struct {
	Name          string
	ProjectScoped bool
}

var projectCairnlineSidecarCoordinationListTools = []projectCairnlineSidecarCoordinationTool{
	{Name: "projects.list"},
	{Name: "profiles.list"},
	{Name: "execution_profiles.list"},
	{Name: "skills.list", ProjectScoped: true},
	{Name: "roles.list", ProjectScoped: true},
	{Name: "work_items.list", ProjectScoped: true},
	{Name: "assignments.list", ProjectScoped: true},
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
		return "Cairnline sidecar connector is configured and can be exercised through local-only probe/connect/read/detail/coordination/assignment-context/launch-packet/lifecycle/write/setup diagnostics, but Hecate does not yet route Projects reads or writes through the standalone Cairnline MCP client."
	default:
		return "Hecate is using the embedded Cairnline Go package bridge for replacement-readiness dogfood."
	}
}

func projectCairnlineConnectorWarning(mode string) string {
	if mode != "sidecar" {
		return ""
	}
	return "HECATE_PROJECTS_CAIRNLINE_CONNECTOR=sidecar enables standalone Cairnline MCP probe/connect/read/detail/coordination/assignment-context/launch-packet/lifecycle/write/setup diagnostic surfaces only; Cairnline read/write routing stays disabled until Hecate has a sidecar Projects backend adapter."
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

func (h *Handler) HandleProjectCairnlineSidecarCoordinationSmoke(w http.ResponseWriter, r *http.Request) {
	if !requireLoopbackClient(w, r, "Cairnline sidecar coordination smoke") {
		return
	}
	var req ProjectCairnlineSidecarCoordinationRequest
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	WriteJSON(w, http.StatusOK, ProjectCairnlineSidecarCoordinationEnvelope{
		Object: "project_cairnline_sidecar_coordination",
		Data:   h.projectCairnlineSidecarCoordinationSmoke(r.Context(), req),
	})
}

func (h *Handler) HandleProjectCairnlineSidecarAssignmentContextSmoke(w http.ResponseWriter, r *http.Request) {
	if !requireLoopbackClient(w, r, "Cairnline sidecar assignment context smoke") {
		return
	}
	var req ProjectCairnlineSidecarAssignmentContextRequest
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	WriteJSON(w, http.StatusOK, ProjectCairnlineSidecarAssignmentContextEnvelope{
		Object: "project_cairnline_sidecar_assignment_context",
		Data:   h.projectCairnlineSidecarAssignmentContextSmoke(r.Context(), req),
	})
}

func (h *Handler) HandleProjectCairnlineSidecarLaunchPacketSmoke(w http.ResponseWriter, r *http.Request) {
	if !requireLoopbackClient(w, r, "Cairnline sidecar launch packet smoke") {
		return
	}
	var req ProjectCairnlineSidecarLaunchPacketRequest
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	WriteJSON(w, http.StatusOK, ProjectCairnlineSidecarLaunchPacketEnvelope{
		Object: "project_cairnline_sidecar_launch_packet",
		Data:   h.projectCairnlineSidecarLaunchPacketSmoke(r.Context(), req),
	})
}

func (h *Handler) HandleProjectCairnlineSidecarLifecycleSmoke(w http.ResponseWriter, r *http.Request) {
	if !requireLoopbackClient(w, r, "Cairnline sidecar lifecycle smoke") {
		return
	}
	var req ProjectCairnlineSidecarLifecycleRequest
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	WriteJSON(w, http.StatusOK, ProjectCairnlineSidecarLifecycleEnvelope{
		Object: "project_cairnline_sidecar_lifecycle",
		Data:   h.projectCairnlineSidecarLifecycleSmoke(r.Context(), req),
	})
}

func (h *Handler) HandleProjectCairnlineSidecarWriteSmoke(w http.ResponseWriter, r *http.Request) {
	if !requireLoopbackClient(w, r, "Cairnline sidecar write smoke") {
		return
	}
	var req ProjectCairnlineSidecarWriteRequest
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	WriteJSON(w, http.StatusOK, ProjectCairnlineSidecarWriteEnvelope{
		Object: "project_cairnline_sidecar_write",
		Data:   h.projectCairnlineSidecarWriteSmoke(r.Context(), req),
	})
}

func (h *Handler) HandleProjectCairnlineSidecarSetupSmoke(w http.ResponseWriter, r *http.Request) {
	if !requireLoopbackClient(w, r, "Cairnline sidecar setup smoke") {
		return
	}
	var req ProjectCairnlineSidecarSetupRequest
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	WriteJSON(w, http.StatusOK, ProjectCairnlineSidecarSetupEnvelope{
		Object: "project_cairnline_sidecar_setup",
		Data:   h.projectCairnlineSidecarSetupSmoke(r.Context(), req),
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

func (h *Handler) projectCairnlineSidecarCoordinationSmoke(ctx context.Context, req ProjectCairnlineSidecarCoordinationRequest) ProjectCairnlineSidecarCoordinationResponse {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, dbPath, timeout := h.projectCairnlineSidecarMCPConfig()
	requestedProjectID := strings.TrimSpace(req.ProjectID)
	response := ProjectCairnlineSidecarCoordinationResponse{
		Ready:                 false,
		Status:                "sidecar_coordination_not_run",
		Detail:                "Cairnline sidecar coordination smoke has not run.",
		Command:               cfg.Command,
		Args:                  append([]string(nil), cfg.Args...),
		DatabasePath:          dbPath,
		ProbeTimeoutMS:        timeout.Milliseconds(),
		PersistentClient:      true,
		ClientCacheConfigured: h != nil,
		ReadOnly:              true,
		RequestedProjectID:    requestedProjectID,
		ToolCount:             len(projectCairnlineSidecarCoordinationListTools),
	}
	if h == nil {
		response.Status = "sidecar_coordination_failed"
		response.Detail = "Cairnline sidecar coordination smoke requires an API handler."
		return response
	}
	if h.projectCairnlineConnectorMode() != "sidecar" {
		response.Warnings = append(response.Warnings, "HECATE_PROJECTS_CAIRNLINE_CONNECTOR is not sidecar; this coordination smoke does not affect live Projects routing.")
	}
	if dbPath != "" && len(h.config.ProjectsCairnlineSidecarArgs()) > 0 {
		response.Warnings = append(response.Warnings, "HECATE_PROJECTS_CAIRNLINE_SIDECAR_ARGS is set, so HECATE_PROJECTS_CAIRNLINE_SIDECAR_DB is reported but not appended automatically.")
	}

	cache := h.projectCairnlineSidecarMCPClientCache()
	smokeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	projectID := requestedProjectID
	for index, tool := range projectCairnlineSidecarCoordinationListTools {
		if tool.ProjectScoped && projectID == "" {
			response.Status = "sidecar_coordination_no_project"
			response.Detail = "Hecate called Cairnline sidecar projects.list through the persistent sidecar client, but no project id was available for project-scoped coordination list tools."
			response.setSidecarCacheStats(cache.Stats())
			return response
		}
		result, err := h.callProjectCairnlineSidecarCoordinationTool(smokeCtx, cfg, cache, tool, projectID)
		if err != nil {
			response.Status = "sidecar_coordination_failed"
			response.Detail = err.Error()
			response.setSidecarCacheStats(cache.Stats())
			return response
		}
		response.Lists = append(response.Lists, result)
		if result.ToolIsError {
			response.Status = "sidecar_coordination_tool_failed"
			response.Detail = "Cairnline sidecar " + result.Tool + " returned a tool-level error. Hecate still keeps live Projects reads and writes on Hecate-native stores in sidecar mode."
			response.setSidecarCacheStats(cache.Stats())
			return response
		}
		if result.StructuredParseError != "" {
			response.Warnings = append(response.Warnings, "Cairnline sidecar "+result.Tool+" returned structuredContent that Hecate could not parse as a list.")
		} else if !result.StructuredReady {
			response.Warnings = append(response.Warnings, "Cairnline sidecar "+result.Tool+" did not return structuredContent; Hecate verified the tool call but not a typed list contract.")
		}
		if index == 0 && projectID == "" && result.StructuredReady && result.StructuredCount > 0 {
			projects, _, structuredErr := projectCairnlineSidecarStructuredProjects(result.StructuredContent)
			if structuredErr == nil && len(projects) > 0 {
				projectID = strings.TrimSpace(projects[0].ID)
				response.SelectedProjectID = projectID
				response.SelectedProjectSource = "projects.list"
			}
		}
	}
	if response.SelectedProjectID == "" && projectID != "" {
		response.SelectedProjectID = projectID
		if requestedProjectID != "" {
			response.SelectedProjectSource = "request"
		}
	}
	response.StructuredReady = projectCairnlineSidecarCoordinationStructuredReady(response.Lists)
	response.Ready = true
	response.Status = "sidecar_coordination_ready"
	response.Detail = "Hecate called read-only Cairnline sidecar coordination list tools through the persistent sidecar client. Hecate still keeps live Projects reads and writes on Hecate-native stores in sidecar mode."
	response.setSidecarCacheStats(cache.Stats())
	return response
}

func (h *Handler) projectCairnlineSidecarAssignmentContextSmoke(ctx context.Context, req ProjectCairnlineSidecarAssignmentContextRequest) ProjectCairnlineSidecarAssignmentContextResponse {
	const contextToolName = "assignments.context"
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, dbPath, timeout := h.projectCairnlineSidecarMCPConfig()
	requestedProjectID := strings.TrimSpace(req.ProjectID)
	requestedAssignmentID := strings.TrimSpace(req.AssignmentID)
	response := ProjectCairnlineSidecarAssignmentContextResponse{
		Ready:                 false,
		Status:                "sidecar_assignment_context_not_run",
		Detail:                "Cairnline sidecar assignment context smoke has not run.",
		Command:               cfg.Command,
		Args:                  append([]string(nil), cfg.Args...),
		DatabasePath:          dbPath,
		ProbeTimeoutMS:        timeout.Milliseconds(),
		PersistentClient:      true,
		ClientCacheConfigured: h != nil,
		Tool:                  contextToolName,
		ReadOnly:              true,
		RequestedProjectID:    requestedProjectID,
		RequestedAssignmentID: requestedAssignmentID,
	}
	if h == nil {
		response.Status = "sidecar_assignment_context_failed"
		response.Detail = "Cairnline sidecar assignment context smoke requires an API handler."
		return response
	}
	if h.projectCairnlineConnectorMode() != "sidecar" {
		response.Warnings = append(response.Warnings, "HECATE_PROJECTS_CAIRNLINE_CONNECTOR is not sidecar; this assignment context smoke does not affect live Projects routing.")
	}
	if dbPath != "" && len(h.config.ProjectsCairnlineSidecarArgs()) > 0 {
		response.Warnings = append(response.Warnings, "HECATE_PROJECTS_CAIRNLINE_SIDECAR_ARGS is set, so HECATE_PROJECTS_CAIRNLINE_SIDECAR_DB is reported but not appended automatically.")
	}

	cache := h.projectCairnlineSidecarMCPClientCache()
	smokeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	selection := h.projectCairnlineSidecarSelectAssignmentForTool(smokeCtx, cfg, cache, requestedProjectID, requestedAssignmentID, "sidecar_assignment_context", contextToolName)
	response.SelectedProjectID = selection.ProjectID
	response.SelectedProjectSource = selection.ProjectSource
	response.SelectedAssignmentID = selection.AssignmentID
	response.SelectedAssignmentSource = selection.AssignmentSource
	response.ProjectList = selection.ProjectList
	response.AssignmentList = selection.AssignmentList
	response.Warnings = append(response.Warnings, selection.Warnings...)
	if selection.Status != "" {
		response.Status = selection.Status
		response.Detail = selection.Detail
		response.setSidecarCacheStats(cache.Stats())
		return response
	}
	projectID := selection.ProjectID
	assignmentID := selection.AssignmentID

	args, err := json.Marshal(map[string]string{"project_id": projectID, "assignment_id": assignmentID})
	if err != nil {
		response.Status = "sidecar_assignment_context_failed"
		response.Detail = err.Error()
		response.setSidecarCacheStats(cache.Stats())
		return response
	}
	result, err := orchestrator.CallCachedMCPServerTool(smokeCtx, cfg, h.secretCipher, cache, contextToolName, args)
	if err != nil {
		response.Status = "sidecar_assignment_context_failed"
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
		response.Status = "sidecar_assignment_context_tool_failed"
		response.Detail = "Cairnline sidecar assignments.context returned a tool-level error. Hecate still keeps live Projects reads and writes on Hecate-native stores in sidecar mode."
		return response
	}
	contextIDs, structuredReady, structuredErr := projectCairnlineSidecarStructuredAssignmentContextIDs(result.Result.StructuredContent)
	response.StructuredReady = structuredReady
	response.StructuredIDs = contextIDs
	if structuredErr != nil {
		response.StructuredParseError = structuredErr.Error()
		response.Warnings = append(response.Warnings, "Cairnline sidecar assignments.context returned structuredContent that Hecate could not parse as assignment context.")
	} else if !structuredReady {
		response.Warnings = append(response.Warnings, "Cairnline sidecar assignments.context did not return structuredContent; Hecate verified the tool call but not a typed assignment-context contract.")
	} else if contextIDs.AssignmentID != assignmentID {
		response.Warnings = append(response.Warnings, "Cairnline sidecar assignments.context returned an assignment id different from the requested id.")
	} else if contextIDs.ProjectID != "" && contextIDs.ProjectID != projectID {
		response.Warnings = append(response.Warnings, "Cairnline sidecar assignments.context returned a project id different from the requested project id.")
	}
	response.Ready = true
	response.Status = "sidecar_assignment_context_ready"
	response.Detail = "Hecate called the read-only Cairnline sidecar assignments.context tool through the persistent sidecar client. Hecate still keeps live Projects reads and writes on Hecate-native stores in sidecar mode."
	return response
}

func (h *Handler) projectCairnlineSidecarLaunchPacketSmoke(ctx context.Context, req ProjectCairnlineSidecarLaunchPacketRequest) ProjectCairnlineSidecarLaunchPacketResponse {
	const toolName = "assignments.launch_packet"
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, dbPath, timeout := h.projectCairnlineSidecarMCPConfig()
	requestedProjectID := strings.TrimSpace(req.ProjectID)
	requestedAssignmentID := strings.TrimSpace(req.AssignmentID)
	response := ProjectCairnlineSidecarLaunchPacketResponse{
		Ready:                 false,
		Status:                "sidecar_launch_packet_not_run",
		Detail:                "Cairnline sidecar launch packet smoke has not run.",
		Command:               cfg.Command,
		Args:                  append([]string(nil), cfg.Args...),
		DatabasePath:          dbPath,
		ProbeTimeoutMS:        timeout.Milliseconds(),
		PersistentClient:      true,
		ClientCacheConfigured: h != nil,
		Tool:                  toolName,
		ReadOnly:              true,
		RequestedProjectID:    requestedProjectID,
		RequestedAssignmentID: requestedAssignmentID,
	}
	if h == nil {
		response.Status = "sidecar_launch_packet_failed"
		response.Detail = "Cairnline sidecar launch packet smoke requires an API handler."
		return response
	}
	if h.projectCairnlineConnectorMode() != "sidecar" {
		response.Warnings = append(response.Warnings, "HECATE_PROJECTS_CAIRNLINE_CONNECTOR is not sidecar; this launch packet smoke does not affect live Projects routing.")
	}
	if dbPath != "" && len(h.config.ProjectsCairnlineSidecarArgs()) > 0 {
		response.Warnings = append(response.Warnings, "HECATE_PROJECTS_CAIRNLINE_SIDECAR_ARGS is set, so HECATE_PROJECTS_CAIRNLINE_SIDECAR_DB is reported but not appended automatically.")
	}

	cache := h.projectCairnlineSidecarMCPClientCache()
	smokeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	selection := h.projectCairnlineSidecarSelectAssignmentForTool(smokeCtx, cfg, cache, requestedProjectID, requestedAssignmentID, "sidecar_launch_packet", toolName)
	response.SelectedProjectID = selection.ProjectID
	response.SelectedProjectSource = selection.ProjectSource
	response.SelectedAssignmentID = selection.AssignmentID
	response.SelectedAssignmentSource = selection.AssignmentSource
	response.ProjectList = selection.ProjectList
	response.AssignmentList = selection.AssignmentList
	response.Warnings = append(response.Warnings, selection.Warnings...)
	if selection.Status != "" {
		response.Status = selection.Status
		response.Detail = selection.Detail
		response.setSidecarCacheStats(cache.Stats())
		return response
	}

	args, err := json.Marshal(map[string]string{"project_id": selection.ProjectID, "assignment_id": selection.AssignmentID})
	if err != nil {
		response.Status = "sidecar_launch_packet_failed"
		response.Detail = err.Error()
		response.setSidecarCacheStats(cache.Stats())
		return response
	}
	result, err := orchestrator.CallCachedMCPServerTool(smokeCtx, cfg, h.secretCipher, cache, toolName, args)
	if err != nil {
		response.Status = "sidecar_launch_packet_failed"
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
		response.Status = "sidecar_launch_packet_tool_failed"
		response.Detail = "Cairnline sidecar assignments.launch_packet returned a tool-level error. Hecate still keeps live Projects reads and writes on Hecate-native stores in sidecar mode."
		return response
	}
	ids, counts, packetWarnings, structuredReady, structuredErr := projectCairnlineSidecarStructuredLaunchPacket(result.Result.StructuredContent)
	response.StructuredReady = structuredReady
	response.StructuredIDs = ids
	response.StructuredCounts = counts
	response.StructuredWarnings = packetWarnings
	if structuredErr != nil {
		response.StructuredParseError = structuredErr.Error()
		response.Warnings = append(response.Warnings, "Cairnline sidecar assignments.launch_packet returned structuredContent that Hecate could not parse as a launch packet.")
	} else if !structuredReady {
		response.Warnings = append(response.Warnings, "Cairnline sidecar assignments.launch_packet did not return structuredContent; Hecate verified the tool call but not a typed launch-packet contract.")
	} else if ids.AssignmentID != selection.AssignmentID {
		response.Warnings = append(response.Warnings, "Cairnline sidecar assignments.launch_packet returned an assignment id different from the requested id.")
	} else if ids.ProjectID != "" && ids.ProjectID != selection.ProjectID {
		response.Warnings = append(response.Warnings, "Cairnline sidecar assignments.launch_packet returned a project id different from the requested project id.")
	}
	response.Ready = true
	response.Status = "sidecar_launch_packet_ready"
	response.Detail = "Hecate called the read-only Cairnline sidecar assignments.launch_packet tool through the persistent sidecar client. Hecate still keeps live Projects reads and writes on Hecate-native stores in sidecar mode."
	return response
}

func (h *Handler) projectCairnlineSidecarLifecycleSmoke(ctx context.Context, req ProjectCairnlineSidecarLifecycleRequest) ProjectCairnlineSidecarLifecycleResponse {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, dbPath, timeout := h.projectCairnlineSidecarMCPConfig()
	requestedProjectID := strings.TrimSpace(req.ProjectID)
	requestedAssignmentID := strings.TrimSpace(req.AssignmentID)
	claimedBy := firstNonEmpty(strings.TrimSpace(req.ClaimedBy), "hecate-sidecar-smoke")
	executionRef := firstNonEmpty(strings.TrimSpace(req.ExecutionRef), "hecate-sidecar-smoke")
	completionStatus := firstNonEmpty(strings.TrimSpace(req.CompletionStatus), "completed")
	agentKind := firstNonEmpty(strings.TrimSpace(req.AgentKind), "any")
	executionModes := projectCairnlineSidecarCompactStrings(req.ExecutionModes)
	if len(executionModes) == 0 {
		executionModes = []string{"mcp_pull"}
	}
	response := ProjectCairnlineSidecarLifecycleResponse{
		Ready:                 false,
		Status:                "sidecar_lifecycle_not_run",
		Detail:                "Cairnline sidecar lifecycle smoke has not run.",
		Command:               cfg.Command,
		Args:                  append([]string(nil), cfg.Args...),
		DatabasePath:          dbPath,
		ProbeTimeoutMS:        timeout.Milliseconds(),
		PersistentClient:      true,
		ClientCacheConfigured: h != nil,
		ConfirmedMutation:     req.ConfirmMutation,
		RequestedProjectID:    requestedProjectID,
		RequestedAssignmentID: requestedAssignmentID,
		ClaimedBy:             claimedBy,
		ExecutionRef:          executionRef,
		CompletionStatus:      completionStatus,
		AgentKind:             agentKind,
		SkillIDs:              projectCairnlineSidecarCompactStrings(req.SkillIDs),
		ExecutionModes:        append([]string(nil), executionModes...),
	}
	if h == nil {
		response.Status = "sidecar_lifecycle_failed"
		response.Detail = "Cairnline sidecar lifecycle smoke requires an API handler."
		return response
	}
	if h.projectCairnlineConnectorMode() != "sidecar" {
		response.Warnings = append(response.Warnings, "HECATE_PROJECTS_CAIRNLINE_CONNECTOR is not sidecar; this lifecycle smoke does not affect live Projects routing.")
	}
	if dbPath != "" && len(h.config.ProjectsCairnlineSidecarArgs()) > 0 {
		response.Warnings = append(response.Warnings, "HECATE_PROJECTS_CAIRNLINE_SIDECAR_ARGS is set, so HECATE_PROJECTS_CAIRNLINE_SIDECAR_DB is reported but not appended automatically.")
	}
	if !req.ConfirmMutation {
		response.Status = "sidecar_lifecycle_confirmation_required"
		response.Detail = "Set confirm_mutation=true to let Hecate mutate the standalone Cairnline sidecar assignment through claim, update_status, launch_packet, and complete. Hecate-native Projects stores are not mutated."
		return response
	}

	cache := h.projectCairnlineSidecarMCPClientCache()
	smokeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	selection := h.projectCairnlineSidecarSelectNextAssignmentForLifecycle(smokeCtx, cfg, cache, requestedProjectID, requestedAssignmentID, agentKind, response.SkillIDs, executionModes)
	response.SelectedProjectID = selection.ProjectID
	response.SelectedProjectSource = selection.ProjectSource
	response.SelectedAssignmentID = selection.AssignmentID
	response.SelectedAssignmentSource = selection.AssignmentSource
	response.ProjectList = selection.ProjectList
	response.NextAssignmentList = selection.AssignmentList
	response.Warnings = append(response.Warnings, selection.Warnings...)
	if selection.Status != "" {
		response.Status = selection.Status
		response.Detail = selection.Detail
		response.setSidecarCacheStats(cache.Stats())
		return response
	}

	projectID := selection.ProjectID
	assignmentID := selection.AssignmentID
	releaseEarlyClaim := func() bool {
		if !projectCairnlineSidecarLifecycleShouldReleaseAfterFailure(response.Steps) {
			return false
		}
		releaseStep := h.callProjectCairnlineSidecarLifecycleTool(smokeCtx, cfg, cache, "release_after_failure", "assignments.release", false, map[string]string{
			"project_id":    projectID,
			"assignment_id": assignmentID,
			"claimed_by":    claimedBy,
		})
		response.Steps = append(response.Steps, releaseStep)
		if releaseStep.Status == "ready" {
			response.Warnings = append(response.Warnings, "Hecate released the standalone Cairnline sidecar assignment after the early lifecycle failure; inspect the reported steps before retrying.")
			return true
		}
		response.Warnings = append(response.Warnings, "Hecate tried to release the standalone Cairnline sidecar assignment after the early lifecycle failure, but the release step did not succeed; inspect the reported assignment before retrying.")
		return false
	}
	appendStep := func(step ProjectCairnlineSidecarLifecycleStep) bool {
		response.Steps = append(response.Steps, step)
		if step.Status == "tool_failed" {
			response.Status = "sidecar_lifecycle_tool_failed"
			response.Detail = "Cairnline sidecar " + step.Tool + " returned a tool-level error. Review the step output before retrying."
			if projectCairnlineSidecarLifecycleHasCommittedMutation(response.Steps) {
				if !releaseEarlyClaim() {
					response.Warnings = append(response.Warnings, "The standalone Cairnline sidecar assignment may have been mutated before this failure; inspect the reported assignment before retrying.")
				}
			}
			response.setSidecarCacheStats(cache.Stats())
			return false
		}
		if step.Status == "failed" {
			response.Status = "sidecar_lifecycle_failed"
			response.Detail = firstNonEmpty(step.ToolText, "Cairnline sidecar "+step.Tool+" failed.")
			if projectCairnlineSidecarLifecycleHasCommittedMutation(response.Steps) {
				if !releaseEarlyClaim() {
					response.Warnings = append(response.Warnings, "The standalone Cairnline sidecar assignment may have been mutated before this failure; inspect the reported assignment before retrying.")
				}
			}
			response.setSidecarCacheStats(cache.Stats())
			return false
		}
		return true
	}

	claimStep := h.callProjectCairnlineSidecarLifecycleTool(smokeCtx, cfg, cache, "claim", "assignments.claim", false, map[string]string{
		"project_id":    projectID,
		"assignment_id": assignmentID,
		"claimed_by":    claimedBy,
	})
	if !appendStep(claimStep) {
		return response
	}
	claimContextStep := h.callProjectCairnlineSidecarLifecycleTool(smokeCtx, cfg, cache, "context_after_claim", "assignments.context", true, map[string]string{
		"project_id":    projectID,
		"assignment_id": assignmentID,
	})
	if !appendStep(claimContextStep) {
		return response
	}

	runningStep := h.callProjectCairnlineSidecarLifecycleTool(smokeCtx, cfg, cache, "mark_running", "assignments.update_status", false, map[string]string{
		"project_id":    projectID,
		"assignment_id": assignmentID,
		"status":        "running",
		"execution_ref": executionRef,
	})
	if !appendStep(runningStep) {
		return response
	}
	runningContextStep := h.callProjectCairnlineSidecarLifecycleTool(smokeCtx, cfg, cache, "context_after_running", "assignments.context", true, map[string]string{
		"project_id":    projectID,
		"assignment_id": assignmentID,
	})
	if !appendStep(runningContextStep) {
		return response
	}

	launchStep := h.callProjectCairnlineSidecarLifecycleTool(smokeCtx, cfg, cache, "launch_packet", "assignments.launch_packet", true, map[string]string{
		"project_id":    projectID,
		"assignment_id": assignmentID,
	})
	response.LaunchPacketReady = launchStep.StructuredReady
	response.LaunchPacketIDs = launchStep.LaunchPacketIDs
	response.LaunchPacketCounts = launchStep.LaunchPacketCounts
	response.LaunchPacketWarnings = append([]string(nil), launchStep.LaunchPacketWarnings...)
	if !appendStep(launchStep) {
		return response
	}

	completeStep := h.callProjectCairnlineSidecarLifecycleTool(smokeCtx, cfg, cache, "complete", "assignments.complete", false, map[string]string{
		"project_id":    projectID,
		"assignment_id": assignmentID,
		"status":        completionStatus,
		"execution_ref": executionRef,
	})
	if !appendStep(completeStep) {
		return response
	}
	finalContextStep := h.callProjectCairnlineSidecarLifecycleTool(smokeCtx, cfg, cache, "context_after_complete", "assignments.context", true, map[string]string{
		"project_id":    projectID,
		"assignment_id": assignmentID,
	})
	if !appendStep(finalContextStep) {
		return response
	}
	response.FinalAssignment = finalContextStep.Assignment
	if response.FinalAssignment.ID == "" {
		response.Warnings = append(response.Warnings, "Cairnline sidecar assignments.context did not return typed final assignment state after completion.")
	}
	response.Ready = true
	response.Status = "sidecar_lifecycle_ready"
	response.Detail = "Hecate selected a compatible standalone Cairnline assignment, claimed it, marked it running, read its launch packet, and completed it through the persistent sidecar client. Hecate-native Projects stores were not mutated."
	response.setSidecarCacheStats(cache.Stats())
	return response
}

func (h *Handler) projectCairnlineSidecarWriteSmoke(ctx context.Context, req ProjectCairnlineSidecarWriteRequest) ProjectCairnlineSidecarWriteResponse {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, dbPath, timeout := h.projectCairnlineSidecarMCPConfig()
	projectName := strings.TrimSpace(req.ProjectName)
	if projectName == "" {
		projectName = "Hecate sidecar write smoke " + time.Now().UTC().Format("20060102T150405.000000000Z")
	}
	updatedProjectName := projectName + " updated"
	response := ProjectCairnlineSidecarWriteResponse{
		Ready:                 false,
		Status:                "sidecar_write_not_run",
		Detail:                "Cairnline sidecar write smoke has not run.",
		Command:               cfg.Command,
		Args:                  append([]string(nil), cfg.Args...),
		DatabasePath:          dbPath,
		ProbeTimeoutMS:        timeout.Milliseconds(),
		PersistentClient:      true,
		ClientCacheConfigured: h != nil,
		ConfirmedMutation:     req.ConfirmMutation,
		ProjectName:           projectName,
		UpdatedProjectName:    updatedProjectName,
	}
	if h == nil {
		response.Status = "sidecar_write_failed"
		response.Detail = "Cairnline sidecar write smoke requires an API handler."
		return response
	}
	if h.projectCairnlineConnectorMode() != "sidecar" {
		response.Warnings = append(response.Warnings, "HECATE_PROJECTS_CAIRNLINE_CONNECTOR is not sidecar; this write smoke does not affect live Projects routing.")
	}
	if dbPath != "" && len(h.config.ProjectsCairnlineSidecarArgs()) > 0 {
		response.Warnings = append(response.Warnings, "HECATE_PROJECTS_CAIRNLINE_SIDECAR_ARGS is set, so HECATE_PROJECTS_CAIRNLINE_SIDECAR_DB is reported but not appended automatically.")
	}
	if !req.ConfirmMutation {
		response.Status = "sidecar_write_confirmation_required"
		response.Detail = "Set confirm_mutation=true to let Hecate create, update, verify, delete, and re-check a temporary project in the standalone Cairnline sidecar. Hecate-native Projects stores are not mutated."
		return response
	}

	cache := h.projectCairnlineSidecarMCPClientCache()
	smokeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	projectID := ""
	cleanup := func() {
		if projectID == "" || response.CleanupVerified {
			return
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
		defer cleanupCancel()
		cleanupStep := h.callProjectCairnlineSidecarWriteTool(cleanupCtx, cfg, cache, "cleanup_delete", "projects.delete", false, map[string]string{"id": projectID})
		response.Steps = append(response.Steps, cleanupStep)
		if cleanupStep.Status == "ready" {
			verifyCleanupStep := h.callProjectCairnlineSidecarWriteTool(cleanupCtx, cfg, cache, "cleanup_get_after_delete", "projects.get", true, map[string]string{"id": projectID})
			if verifyCleanupStep.Status == "tool_failed" {
				verifyCleanupStep.Status = "expected_missing"
				response.CleanupVerified = true
				response.Warnings = append(response.Warnings, "Hecate deleted and verified removal of the temporary standalone Cairnline project after the write smoke failed; inspect the reported steps before retrying.")
			} else {
				response.Warnings = append(response.Warnings, "Hecate deleted the temporary standalone Cairnline project after the write smoke failed, but removal could not be verified; inspect the reported steps before retrying.")
			}
			response.Steps = append(response.Steps, verifyCleanupStep)
			return
		}
		response.Warnings = append(response.Warnings, "Hecate tried to delete the temporary standalone Cairnline project after the write smoke failed, but cleanup did not succeed; inspect the standalone Cairnline sidecar before retrying.")
	}
	fail := func(status, detail string) ProjectCairnlineSidecarWriteResponse {
		response.Status = status
		response.Detail = detail
		cleanup()
		response.setSidecarCacheStats(cache.Stats())
		return response
	}
	appendStep := func(step ProjectCairnlineSidecarWriteStep) bool {
		response.Steps = append(response.Steps, step)
		if step.Status == "tool_failed" {
			response.Status = "sidecar_write_tool_failed"
			response.Detail = "Cairnline sidecar " + step.Tool + " returned a tool-level error. Review the step output before retrying."
			cleanup()
			response.setSidecarCacheStats(cache.Stats())
			return false
		}
		if step.Status == "failed" {
			response.Status = "sidecar_write_failed"
			response.Detail = firstNonEmpty(step.ToolText, "Cairnline sidecar "+step.Tool+" failed.")
			cleanup()
			response.setSidecarCacheStats(cache.Stats())
			return false
		}
		return true
	}

	createStep := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "create_project", "projects.create", false, map[string]string{
		"name":        projectName,
		"description": "Temporary Hecate sidecar write smoke project. It should be deleted by the same diagnostic run.",
	})
	if !appendStep(createStep) {
		return response
	}

	listStep := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "list_after_create", "projects.list", true, map[string]string{})
	if !appendStep(listStep) {
		return response
	}
	project, ok := projectCairnlineSidecarProjectByName(listStep.StructuredProjects, projectName)
	if !ok || strings.TrimSpace(project.ID) == "" {
		return fail("sidecar_write_created_project_not_listed", "Cairnline sidecar projects.create succeeded, but projects.list did not return the temporary project by name.")
	}
	projectID = strings.TrimSpace(project.ID)
	response.SelectedProjectID = projectID
	response.CreatedProject = project

	updateStep := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "update_project", "projects.update", false, map[string]string{
		"id":          projectID,
		"name":        updatedProjectName,
		"description": "Temporary Hecate sidecar write smoke project updated through projects.update.",
	})
	if !appendStep(updateStep) {
		return response
	}

	getStep := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "get_after_update", "projects.get", true, map[string]string{"id": projectID})
	if !appendStep(getStep) {
		return response
	}
	updatedProject := getStep.StructuredProject
	if strings.TrimSpace(updatedProject.ID) != projectID || strings.TrimSpace(updatedProject.Name) != updatedProjectName {
		return fail("sidecar_write_update_verification_failed", "Cairnline sidecar projects.get did not return the expected project id and updated name after projects.update.")
	}
	response.UpdatedProject = updatedProject

	deleteStep := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "delete_project", "projects.delete", false, map[string]string{"id": projectID})
	if !appendStep(deleteStep) {
		return response
	}

	verifyDeleteStep := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "get_after_delete", "projects.get", true, map[string]string{"id": projectID})
	if verifyDeleteStep.Status == "tool_failed" {
		verifyDeleteStep.Status = "expected_missing"
		response.Steps = append(response.Steps, verifyDeleteStep)
		response.CleanupVerified = true
		response.Ready = true
		response.Status = "sidecar_write_ready"
		response.Detail = "Hecate created, listed, updated, verified, deleted, and confirmed removal of a temporary standalone Cairnline project through the persistent sidecar client. Hecate-native Projects stores were not mutated."
		response.setSidecarCacheStats(cache.Stats())
		return response
	}
	if !appendStep(verifyDeleteStep) {
		return response
	}
	return fail("sidecar_write_delete_verification_failed", "Cairnline sidecar projects.delete succeeded, but projects.get still returned the temporary project.")
}

func (h *Handler) projectCairnlineSidecarSetupSmoke(ctx context.Context, req ProjectCairnlineSidecarSetupRequest) ProjectCairnlineSidecarSetupResponse {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, dbPath, timeout := h.projectCairnlineSidecarMCPConfig()
	projectName := strings.TrimSpace(req.ProjectName)
	if projectName == "" {
		projectName = "Hecate sidecar setup smoke " + time.Now().UTC().Format("20060102T150405.000000000Z")
	}
	response := ProjectCairnlineSidecarSetupResponse{
		Ready:                 false,
		Status:                "sidecar_setup_not_run",
		Detail:                "Cairnline sidecar setup smoke has not run.",
		Command:               cfg.Command,
		Args:                  append([]string(nil), cfg.Args...),
		DatabasePath:          dbPath,
		ProbeTimeoutMS:        timeout.Milliseconds(),
		PersistentClient:      true,
		ClientCacheConfigured: h != nil,
		ConfirmedMutation:     req.ConfirmMutation,
		ProjectName:           projectName,
		RootID:                "root_setup_smoke",
		ContextSourceID:       "src_setup_smoke",
	}
	if h == nil {
		response.Status = "sidecar_setup_failed"
		response.Detail = "Cairnline sidecar setup smoke requires an API handler."
		return response
	}
	if h.projectCairnlineConnectorMode() != "sidecar" {
		response.Warnings = append(response.Warnings, "HECATE_PROJECTS_CAIRNLINE_CONNECTOR is not sidecar; this setup smoke does not affect live Projects routing.")
	}
	if dbPath != "" && len(h.config.ProjectsCairnlineSidecarArgs()) > 0 {
		response.Warnings = append(response.Warnings, "HECATE_PROJECTS_CAIRNLINE_SIDECAR_ARGS is set, so HECATE_PROJECTS_CAIRNLINE_SIDECAR_DB is reported but not appended automatically.")
	}
	if !req.ConfirmMutation {
		response.Status = "sidecar_setup_confirmation_required"
		response.Detail = "Set confirm_mutation=true to let Hecate create, update, verify, delete, and re-check temporary roots and context sources in the standalone Cairnline sidecar. Hecate-native Projects stores are not mutated."
		return response
	}

	cache := h.projectCairnlineSidecarMCPClientCache()
	smokeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	projectID := ""
	cleanup := func() {
		if projectID == "" || response.CleanupVerified {
			return
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
		defer cleanupCancel()
		cleanupStep := h.callProjectCairnlineSidecarWriteTool(cleanupCtx, cfg, cache, "cleanup_delete_project", "projects.delete", false, map[string]string{"id": projectID})
		response.Steps = append(response.Steps, cleanupStep)
		if cleanupStep.Status == "ready" {
			verifyCleanupStep := h.callProjectCairnlineSidecarWriteTool(cleanupCtx, cfg, cache, "cleanup_get_after_project_delete", "projects.get", true, map[string]string{"id": projectID})
			if verifyCleanupStep.Status == "tool_failed" {
				verifyCleanupStep.Status = "expected_missing"
				response.CleanupVerified = true
				response.Warnings = append(response.Warnings, "Hecate deleted and verified removal of the temporary standalone Cairnline setup project after the setup smoke failed; inspect the reported steps before retrying.")
			} else {
				response.Warnings = append(response.Warnings, "Hecate deleted the temporary standalone Cairnline setup project after the setup smoke failed, but removal could not be verified; inspect the reported steps before retrying.")
			}
			response.Steps = append(response.Steps, verifyCleanupStep)
			return
		}
		response.Warnings = append(response.Warnings, "Hecate tried to delete the temporary standalone Cairnline setup project after the setup smoke failed, but cleanup did not succeed; inspect the standalone Cairnline sidecar before retrying.")
	}
	fail := func(status, detail string) ProjectCairnlineSidecarSetupResponse {
		response.Status = status
		response.Detail = detail
		cleanup()
		response.setSidecarCacheStats(cache.Stats())
		return response
	}
	appendStep := func(step ProjectCairnlineSidecarWriteStep) bool {
		response.Steps = append(response.Steps, step)
		if step.Status == "tool_failed" {
			response.Status = "sidecar_setup_tool_failed"
			response.Detail = "Cairnline sidecar " + step.Tool + " returned a tool-level error. Review the step output before retrying."
			cleanup()
			response.setSidecarCacheStats(cache.Stats())
			return false
		}
		if step.Status == "failed" {
			response.Status = "sidecar_setup_failed"
			response.Detail = firstNonEmpty(step.ToolText, "Cairnline sidecar "+step.Tool+" failed.")
			cleanup()
			response.setSidecarCacheStats(cache.Stats())
			return false
		}
		return true
	}

	createProject := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "create_project", "projects.create", false, map[string]string{
		"name":        projectName,
		"description": "Temporary Hecate sidecar setup smoke project. It should be deleted by the same diagnostic run.",
	})
	if !appendStep(createProject) {
		return response
	}
	listProjects := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "list_after_project_create", "projects.list", true, map[string]string{})
	if !appendStep(listProjects) {
		return response
	}
	project, ok := projectCairnlineSidecarProjectByName(listProjects.StructuredProjects, projectName)
	if !ok || strings.TrimSpace(project.ID) == "" {
		return fail("sidecar_setup_created_project_not_listed", "Cairnline sidecar projects.create succeeded, but projects.list did not return the temporary setup project by name.")
	}
	projectID = strings.TrimSpace(project.ID)
	response.SelectedProjectID = projectID

	createRoot := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "create_root", "roots.create", false, map[string]any{
		"project_id": projectID,
		"id":         response.RootID,
		"path":       "/tmp/hecate-sidecar-setup-smoke",
		"kind":       "local",
		"active":     true,
	})
	if !appendStep(createRoot) {
		return response
	}
	if createRoot.StructuredRoot.ID != response.RootID || createRoot.StructuredRoot.Path != "/tmp/hecate-sidecar-setup-smoke" {
		return fail("sidecar_setup_root_create_verification_failed", "Cairnline sidecar roots.create did not return the expected root id and path.")
	}
	response.CreatedRoot = createRoot.StructuredRoot

	updateRoot := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "update_root", "roots.update", false, map[string]any{
		"project_id": projectID,
		"root_id":    response.RootID,
		"path":       "/tmp/hecate-sidecar-setup-smoke-updated",
		"kind":       "git_worktree",
		"git_branch": "setup-smoke",
		"active":     false,
	})
	if !appendStep(updateRoot) {
		return response
	}
	if updateRoot.StructuredRoot.ID != response.RootID || updateRoot.StructuredRoot.Path != "/tmp/hecate-sidecar-setup-smoke-updated" || updateRoot.StructuredRoot.Active {
		return fail("sidecar_setup_root_update_verification_failed", "Cairnline sidecar roots.update did not return the expected inactive updated root.")
	}
	response.UpdatedRoot = updateRoot.StructuredRoot

	listRoots := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "list_roots_after_update", "roots.list", true, map[string]string{"project_id": projectID})
	if !appendStep(listRoots) {
		return response
	}
	if root, ok := projectCairnlineSidecarRootByID(listRoots.StructuredRoots, response.RootID); !ok || root.Path != "/tmp/hecate-sidecar-setup-smoke-updated" || root.Active {
		return fail("sidecar_setup_root_list_verification_failed", "Cairnline sidecar roots.list did not return the updated inactive root.")
	}

	createSource := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "create_context_source", "context_sources.create", false, map[string]any{
		"project_id":      projectID,
		"id":              response.ContextSourceID,
		"kind":            "workspace_instruction",
		"title":           "Setup smoke guidance",
		"locator":         "AGENTS.md",
		"enabled":         true,
		"format":          "agents_md",
		"scope":           "workspace",
		"trust_label":     "workspace_guidance",
		"source_category": "instructions",
		"metadata":        map[string]string{"root_id": response.RootID},
	})
	if !appendStep(createSource) {
		return response
	}
	if createSource.StructuredSource.ID != response.ContextSourceID || createSource.StructuredSource.Title != "Setup smoke guidance" || !createSource.StructuredSource.Enabled {
		return fail("sidecar_setup_source_create_verification_failed", "Cairnline sidecar context_sources.create did not return the expected enabled source.")
	}
	response.CreatedSource = createSource.StructuredSource

	updateSource := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "update_context_source", "context_sources.update", false, map[string]any{
		"project_id": projectID,
		"source_id":  response.ContextSourceID,
		"title":      "Setup smoke guidance updated",
		"enabled":    false,
		"metadata":   map[string]string{"root_id": response.RootID, "updated": "true"},
	})
	if !appendStep(updateSource) {
		return response
	}
	if updateSource.StructuredSource.ID != response.ContextSourceID || updateSource.StructuredSource.Title != "Setup smoke guidance updated" || updateSource.StructuredSource.Enabled {
		return fail("sidecar_setup_source_update_verification_failed", "Cairnline sidecar context_sources.update did not return the expected disabled updated source.")
	}
	response.UpdatedSource = updateSource.StructuredSource

	listSources := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "list_sources_after_update", "context_sources.list", true, map[string]string{"project_id": projectID})
	if !appendStep(listSources) {
		return response
	}
	if source, ok := projectCairnlineSidecarSourceByID(listSources.StructuredSources, response.ContextSourceID); !ok || source.Title != "Setup smoke guidance updated" || source.Enabled {
		return fail("sidecar_setup_source_list_verification_failed", "Cairnline sidecar context_sources.list did not return the updated disabled source.")
	}

	deleteSource := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "delete_context_source", "context_sources.delete", false, map[string]string{"project_id": projectID, "source_id": response.ContextSourceID})
	if !appendStep(deleteSource) {
		return response
	}
	listSourcesAfterDelete := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "list_sources_after_delete", "context_sources.list", true, map[string]string{"project_id": projectID})
	if !appendStep(listSourcesAfterDelete) {
		return response
	}
	if _, ok := projectCairnlineSidecarSourceByID(listSourcesAfterDelete.StructuredSources, response.ContextSourceID); ok {
		return fail("sidecar_setup_source_delete_verification_failed", "Cairnline sidecar context_sources.delete succeeded, but context_sources.list still returned the temporary source.")
	}

	deleteRoot := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "delete_root", "roots.delete", false, map[string]string{"project_id": projectID, "root_id": response.RootID})
	if !appendStep(deleteRoot) {
		return response
	}
	listRootsAfterDelete := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "list_roots_after_delete", "roots.list", true, map[string]string{"project_id": projectID})
	if !appendStep(listRootsAfterDelete) {
		return response
	}
	if _, ok := projectCairnlineSidecarRootByID(listRootsAfterDelete.StructuredRoots, response.RootID); ok {
		return fail("sidecar_setup_root_delete_verification_failed", "Cairnline sidecar roots.delete succeeded, but roots.list still returned the temporary root.")
	}

	deleteProject := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "delete_project", "projects.delete", false, map[string]string{"id": projectID})
	if !appendStep(deleteProject) {
		return response
	}
	verifyDelete := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "get_after_project_delete", "projects.get", true, map[string]string{"id": projectID})
	if verifyDelete.Status == "tool_failed" {
		verifyDelete.Status = "expected_missing"
		response.Steps = append(response.Steps, verifyDelete)
		response.CleanupVerified = true
		response.Ready = true
		response.Status = "sidecar_setup_ready"
		response.Detail = "Hecate created, updated, listed, deleted, and verified removal of temporary standalone Cairnline root and context-source setup metadata through the persistent sidecar client. Hecate-native Projects stores were not mutated."
		response.setSidecarCacheStats(cache.Stats())
		return response
	}
	if !appendStep(verifyDelete) {
		return response
	}
	return fail("sidecar_setup_project_delete_verification_failed", "Cairnline sidecar projects.delete succeeded, but projects.get still returned the temporary setup project.")
}

func projectCairnlineSidecarLifecycleShouldReleaseAfterFailure(steps []ProjectCairnlineSidecarLifecycleStep) bool {
	claimed := false
	for _, step := range steps {
		if step.Status != "ready" || step.ReadOnly {
			continue
		}
		switch step.Tool {
		case "assignments.claim":
			claimed = true
		case "assignments.update_status", "assignments.complete", "assignments.release":
			return false
		}
	}
	return claimed
}

func projectCairnlineSidecarLifecycleHasCommittedMutation(steps []ProjectCairnlineSidecarLifecycleStep) bool {
	for _, step := range steps {
		if !step.ReadOnly && step.Status == "ready" {
			return true
		}
	}
	return false
}

type projectCairnlineSidecarAssignmentSelection struct {
	ProjectID        string
	ProjectSource    string
	AssignmentID     string
	AssignmentSource string
	ProjectList      *ProjectCairnlineSidecarCoordinationListResult
	AssignmentList   *ProjectCairnlineSidecarCoordinationListResult
	Status           string
	Detail           string
	Warnings         []string
}

func projectCairnlineSidecarCompactStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func (h *Handler) projectCairnlineSidecarSelectAssignmentForTool(ctx context.Context, cfg types.MCPServerConfig, cache *mcpclient.SharedClientCache, requestedProjectID, requestedAssignmentID, statusPrefix, targetTool string) projectCairnlineSidecarAssignmentSelection {
	const (
		projectListToolName    = "projects.list"
		assignmentListToolName = "assignments.list"
	)
	var selection projectCairnlineSidecarAssignmentSelection
	projectID := requestedProjectID
	if projectID == "" {
		listResult, err := h.callProjectCairnlineSidecarCoordinationTool(ctx, cfg, cache, projectCairnlineSidecarCoordinationTool{Name: projectListToolName}, "")
		if err != nil {
			selection.Status = statusPrefix + "_failed"
			selection.Detail = err.Error()
			return selection
		}
		selection.ProjectList = &listResult
		if listResult.ToolIsError {
			selection.Status = statusPrefix + "_project_list_tool_failed"
			selection.Detail = "Cairnline sidecar projects.list returned a tool-level error before Hecate could select a project for " + targetTool + "."
			return selection
		}
		projects, structuredReady, structuredErr := projectCairnlineSidecarStructuredProjects(listResult.StructuredContent)
		selection.ProjectList.StructuredReady = structuredReady
		selection.ProjectList.StructuredCount = len(projects)
		if structuredErr != nil {
			selection.ProjectList.StructuredParseError = structuredErr.Error()
			selection.Warnings = append(selection.Warnings, "Cairnline sidecar projects.list returned structuredContent that Hecate could not parse as a project list.")
		} else if !structuredReady {
			selection.Warnings = append(selection.Warnings, "Cairnline sidecar projects.list did not return structuredContent, so Hecate could not select a project for "+targetTool+".")
		} else if len(projects) > 0 {
			projectID = strings.TrimSpace(projects[0].ID)
			selection.ProjectID = projectID
			selection.ProjectSource = "projects.list"
		}
		if projectID == "" {
			selection.Status = statusPrefix + "_no_project"
			selection.Detail = "Hecate called Cairnline sidecar projects.list through the persistent sidecar client, but no typed project id was available for " + targetTool + "."
			return selection
		}
	} else {
		selection.ProjectID = projectID
		selection.ProjectSource = "request"
	}
	if selection.ProjectID == "" {
		selection.ProjectID = projectID
	}

	assignmentID := requestedAssignmentID
	if assignmentID == "" {
		listResult, err := h.callProjectCairnlineSidecarCoordinationTool(ctx, cfg, cache, projectCairnlineSidecarCoordinationTool{Name: assignmentListToolName, ProjectScoped: true}, projectID)
		if err != nil {
			selection.Status = statusPrefix + "_failed"
			selection.Detail = err.Error()
			return selection
		}
		selection.AssignmentList = &listResult
		if listResult.ToolIsError {
			selection.Status = statusPrefix + "_assignment_list_tool_failed"
			selection.Detail = "Cairnline sidecar assignments.list returned a tool-level error before Hecate could select an assignment for " + targetTool + "."
			return selection
		}
		assignments, structuredReady, structuredErr := projectCairnlineSidecarStructuredAssignments(listResult.StructuredContent)
		selection.AssignmentList.StructuredReady = structuredReady
		selection.AssignmentList.StructuredCount = len(assignments)
		if structuredErr != nil {
			selection.AssignmentList.StructuredParseError = structuredErr.Error()
			selection.Warnings = append(selection.Warnings, "Cairnline sidecar assignments.list returned structuredContent that Hecate could not parse as an assignment list.")
		} else if !structuredReady {
			selection.Warnings = append(selection.Warnings, "Cairnline sidecar assignments.list did not return structuredContent, so Hecate could not select an assignment for "+targetTool+".")
		} else if len(assignments) > 0 {
			assignmentID = strings.TrimSpace(assignments[0].ID)
			selection.AssignmentID = assignmentID
			selection.AssignmentSource = "assignments.list"
		}
		if assignmentID == "" {
			selection.Status = statusPrefix + "_no_assignment"
			selection.Detail = "Hecate called Cairnline sidecar assignments.list through the persistent sidecar client, but no typed assignment id was available for " + targetTool + "."
			return selection
		}
	} else {
		selection.AssignmentID = assignmentID
		selection.AssignmentSource = "request"
	}
	if selection.AssignmentID == "" {
		selection.AssignmentID = assignmentID
	}
	return selection
}

func (h *Handler) projectCairnlineSidecarSelectNextAssignmentForLifecycle(ctx context.Context, cfg types.MCPServerConfig, cache *mcpclient.SharedClientCache, requestedProjectID, requestedAssignmentID, agentKind string, skillIDs, executionModes []string) projectCairnlineSidecarAssignmentSelection {
	const (
		projectListToolName = "projects.list"
		nextToolName        = "assignments.next"
	)
	var selection projectCairnlineSidecarAssignmentSelection
	projectID := requestedProjectID
	if projectID == "" {
		listResult, err := h.callProjectCairnlineSidecarCoordinationTool(ctx, cfg, cache, projectCairnlineSidecarCoordinationTool{Name: projectListToolName}, "")
		if err != nil {
			selection.Status = "sidecar_lifecycle_failed"
			selection.Detail = err.Error()
			return selection
		}
		selection.ProjectList = &listResult
		if listResult.ToolIsError {
			selection.Status = "sidecar_lifecycle_project_list_tool_failed"
			selection.Detail = "Cairnline sidecar projects.list returned a tool-level error before Hecate could select a project for assignments.next."
			return selection
		}
		projects, structuredReady, structuredErr := projectCairnlineSidecarStructuredProjects(listResult.StructuredContent)
		selection.ProjectList.StructuredReady = structuredReady
		selection.ProjectList.StructuredCount = len(projects)
		if structuredErr != nil {
			selection.ProjectList.StructuredParseError = structuredErr.Error()
			selection.Warnings = append(selection.Warnings, "Cairnline sidecar projects.list returned structuredContent that Hecate could not parse as a project list.")
		} else if !structuredReady {
			selection.Warnings = append(selection.Warnings, "Cairnline sidecar projects.list did not return structuredContent, so Hecate could not select a project for assignments.next.")
		} else if len(projects) > 0 {
			projectID = strings.TrimSpace(projects[0].ID)
			selection.ProjectID = projectID
			selection.ProjectSource = "projects.list"
		}
		if projectID == "" {
			selection.Status = "sidecar_lifecycle_no_project"
			selection.Detail = "Hecate called Cairnline sidecar projects.list through the persistent sidecar client, but no typed project id was available for assignments.next."
			return selection
		}
	} else {
		selection.ProjectID = projectID
		selection.ProjectSource = "request"
	}
	if selection.ProjectID == "" {
		selection.ProjectID = projectID
	}

	assignmentID := requestedAssignmentID
	if assignmentID == "" {
		args := map[string]any{
			"project_id":      projectID,
			"agent_kind":      agentKind,
			"execution_modes": executionModes,
			"limit":           1,
			"status":          "queued",
		}
		if len(skillIDs) > 0 {
			args["skill_ids"] = skillIDs
		}
		rawArgs, err := json.Marshal(args)
		if err != nil {
			selection.Status = "sidecar_lifecycle_failed"
			selection.Detail = err.Error()
			return selection
		}
		result, err := orchestrator.CallCachedMCPServerTool(ctx, cfg, h.secretCipher, cache, nextToolName, rawArgs)
		if err != nil {
			selection.Status = "sidecar_lifecycle_failed"
			selection.Detail = err.Error()
			return selection
		}
		nextResult := ProjectCairnlineSidecarCoordinationListResult{
			Tool:              nextToolName,
			ReadOnly:          true,
			ProjectScoped:     true,
			ProjectID:         projectID,
			ToolText:          result.Text,
			ToolIsError:       result.IsError,
			StructuredContent: result.Result.StructuredContent,
			Meta:              result.Result.Meta,
		}
		selection.AssignmentList = &nextResult
		if result.IsError {
			selection.Status = "sidecar_lifecycle_next_tool_failed"
			selection.Detail = "Cairnline sidecar assignments.next returned a tool-level error before Hecate could select an assignment for the lifecycle smoke."
			return selection
		}
		assignments, structuredReady, structuredErr := projectCairnlineSidecarStructuredAssignments(result.Result.StructuredContent)
		selection.AssignmentList.StructuredReady = structuredReady
		selection.AssignmentList.StructuredCount = len(assignments)
		if structuredErr != nil {
			selection.AssignmentList.StructuredParseError = structuredErr.Error()
			selection.Warnings = append(selection.Warnings, "Cairnline sidecar assignments.next returned structuredContent that Hecate could not parse as an assignment list.")
		} else if !structuredReady {
			selection.Warnings = append(selection.Warnings, "Cairnline sidecar assignments.next did not return structuredContent, so Hecate could not select an assignment for the lifecycle smoke.")
		} else if len(assignments) > 0 {
			assignmentID = strings.TrimSpace(assignments[0].ID)
			selection.AssignmentID = assignmentID
			selection.AssignmentSource = "assignments.next"
		}
		if assignmentID == "" {
			selection.Status = "sidecar_lifecycle_no_assignment"
			selection.Detail = "Hecate called Cairnline sidecar assignments.next through the persistent sidecar client, but no compatible queued MCP-pull assignment was available for the lifecycle smoke."
			return selection
		}
	} else {
		selection.AssignmentID = assignmentID
		selection.AssignmentSource = "request"
	}
	if selection.AssignmentID == "" {
		selection.AssignmentID = assignmentID
	}
	return selection
}

func (h *Handler) callProjectCairnlineSidecarLifecycleTool(ctx context.Context, cfg types.MCPServerConfig, cache *mcpclient.SharedClientCache, name, tool string, readOnly bool, args any) ProjectCairnlineSidecarLifecycleStep {
	step := ProjectCairnlineSidecarLifecycleStep{
		Name:     name,
		Tool:     tool,
		ReadOnly: readOnly,
		Status:   "not_run",
	}
	rawArgs, err := json.Marshal(args)
	if err != nil {
		step.Status = "failed"
		step.ToolText = err.Error()
		return step
	}
	result, err := orchestrator.CallCachedMCPServerTool(ctx, cfg, h.secretCipher, cache, tool, rawArgs)
	if err != nil {
		step.Status = "failed"
		step.ToolText = err.Error()
		return step
	}
	step.ToolText = result.Text
	step.ToolIsError = result.IsError
	step.StructuredContent = result.Result.StructuredContent
	step.Meta = result.Result.Meta
	if result.IsError {
		step.Status = "tool_failed"
		return step
	}
	switch tool {
	case "assignments.context":
		assignment, structuredReady, structuredErr := projectCairnlineSidecarStructuredAssignmentContextAssignment(result.Result.StructuredContent)
		step.StructuredReady = structuredReady
		step.Assignment = assignment
		if structuredErr != nil {
			step.StructuredParseError = structuredErr.Error()
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar assignments.context returned structuredContent that Hecate could not parse as assignment state: " + structuredErr.Error()
			return step
		}
	case "assignments.launch_packet":
		ids, counts, warnings, structuredReady, structuredErr := projectCairnlineSidecarStructuredLaunchPacket(result.Result.StructuredContent)
		step.StructuredReady = structuredReady
		step.LaunchPacketIDs = ids
		step.LaunchPacketCounts = counts
		step.LaunchPacketWarnings = warnings
		if structuredErr != nil {
			step.StructuredParseError = structuredErr.Error()
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar assignments.launch_packet returned structuredContent that Hecate could not parse as a launch packet: " + structuredErr.Error()
			return step
		}
	}
	step.Status = "ready"
	return step
}

func (h *Handler) callProjectCairnlineSidecarWriteTool(ctx context.Context, cfg types.MCPServerConfig, cache *mcpclient.SharedClientCache, name, tool string, readOnly bool, args any) ProjectCairnlineSidecarWriteStep {
	step := ProjectCairnlineSidecarWriteStep{
		Name:     name,
		Tool:     tool,
		ReadOnly: readOnly,
		Status:   "not_run",
	}
	rawArgs, err := json.Marshal(args)
	if err != nil {
		step.Status = "failed"
		step.ToolText = err.Error()
		return step
	}
	result, err := orchestrator.CallCachedMCPServerTool(ctx, cfg, h.secretCipher, cache, tool, rawArgs)
	if err != nil {
		step.Status = "failed"
		step.ToolText = err.Error()
		return step
	}
	step.ToolText = result.Text
	step.ToolIsError = result.IsError
	step.StructuredContent = result.Result.StructuredContent
	step.Meta = result.Result.Meta
	if result.IsError {
		step.Status = "tool_failed"
		return step
	}
	switch tool {
	case "projects.list":
		projects, structuredReady, structuredErr := projectCairnlineSidecarStructuredProjects(result.Result.StructuredContent)
		step.StructuredReady = structuredReady
		step.StructuredProjects = projects
		step.StructuredProjectCount = len(projects)
		if structuredErr != nil {
			step.StructuredParseError = structuredErr.Error()
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar projects.list returned structuredContent that Hecate could not parse as a project list: " + structuredErr.Error()
			return step
		}
		if !structuredReady {
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar projects.list did not return structuredContent; the write smoke needs a typed project list to find the temporary project."
			return step
		}
	case "projects.get":
		project, structuredReady, structuredErr := projectCairnlineSidecarStructuredProject(result.Result.StructuredContent)
		step.StructuredReady = structuredReady
		step.StructuredProject = project
		if structuredErr != nil {
			step.StructuredParseError = structuredErr.Error()
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar projects.get returned structuredContent that Hecate could not parse as a project: " + structuredErr.Error()
			return step
		}
		if !structuredReady {
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar projects.get did not return structuredContent; the write smoke needs typed project detail to verify the temporary project."
			return step
		}
	case "roots.list":
		roots, structuredReady, structuredErr := projectCairnlineSidecarStructuredRoots(result.Result.StructuredContent)
		step.StructuredReady = structuredReady
		step.StructuredRoots = roots
		step.StructuredRootCount = len(roots)
		if structuredErr != nil {
			step.StructuredParseError = structuredErr.Error()
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar roots.list returned structuredContent that Hecate could not parse as a root list: " + structuredErr.Error()
			return step
		}
		if !structuredReady {
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar roots.list did not return structuredContent; the setup smoke needs typed roots to verify setup mutations."
			return step
		}
	case "roots.create", "roots.update", "roots.delete":
		root, structuredReady, structuredErr := projectCairnlineSidecarStructuredRoot(result.Result.StructuredContent)
		step.StructuredReady = structuredReady
		step.StructuredRoot = root
		if structuredErr != nil {
			step.StructuredParseError = structuredErr.Error()
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar " + tool + " returned structuredContent that Hecate could not parse as a root: " + structuredErr.Error()
			return step
		}
		if !structuredReady {
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar " + tool + " did not return structuredContent; the setup smoke needs typed root detail to verify setup mutations."
			return step
		}
	case "context_sources.list":
		sources, structuredReady, structuredErr := projectCairnlineSidecarStructuredSources(result.Result.StructuredContent)
		step.StructuredReady = structuredReady
		step.StructuredSources = sources
		step.StructuredSourceCount = len(sources)
		if structuredErr != nil {
			step.StructuredParseError = structuredErr.Error()
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar context_sources.list returned structuredContent that Hecate could not parse as a context source list: " + structuredErr.Error()
			return step
		}
		if !structuredReady {
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar context_sources.list did not return structuredContent; the setup smoke needs typed context sources to verify setup mutations."
			return step
		}
	case "context_sources.create", "context_sources.update", "context_sources.delete":
		source, structuredReady, structuredErr := projectCairnlineSidecarStructuredSource(result.Result.StructuredContent)
		step.StructuredReady = structuredReady
		step.StructuredSource = source
		if structuredErr != nil {
			step.StructuredParseError = structuredErr.Error()
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar " + tool + " returned structuredContent that Hecate could not parse as a context source: " + structuredErr.Error()
			return step
		}
		if !structuredReady {
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar " + tool + " did not return structuredContent; the setup smoke needs typed context source detail to verify setup mutations."
			return step
		}
	}
	step.Status = "ready"
	return step
}

func (h *Handler) callProjectCairnlineSidecarCoordinationTool(ctx context.Context, cfg types.MCPServerConfig, cache *mcpclient.SharedClientCache, tool projectCairnlineSidecarCoordinationTool, projectID string) (ProjectCairnlineSidecarCoordinationListResult, error) {
	args := json.RawMessage(`{}`)
	if tool.ProjectScoped {
		raw, err := json.Marshal(map[string]string{"project_id": projectID})
		if err != nil {
			return ProjectCairnlineSidecarCoordinationListResult{}, err
		}
		args = raw
	}
	result, err := orchestrator.CallCachedMCPServerTool(ctx, cfg, h.secretCipher, cache, tool.Name, args)
	if err != nil {
		return ProjectCairnlineSidecarCoordinationListResult{}, err
	}
	count, structuredReady, structuredErr := projectCairnlineSidecarStructuredArrayCount(result.Result.StructuredContent)
	item := ProjectCairnlineSidecarCoordinationListResult{
		Tool:                 tool.Name,
		ReadOnly:             true,
		ProjectScoped:        tool.ProjectScoped,
		ProjectID:            projectID,
		ToolText:             result.Text,
		ToolIsError:          result.IsError,
		StructuredContent:    result.Result.StructuredContent,
		Meta:                 result.Result.Meta,
		StructuredReady:      structuredReady,
		StructuredCount:      count,
		StructuredParseError: "",
	}
	if structuredErr != nil {
		item.StructuredParseError = structuredErr.Error()
	}
	return item, nil
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

func projectCairnlineSidecarProjectByName(projects []ProjectCairnlineSidecarProjectItem, name string) (ProjectCairnlineSidecarProjectItem, bool) {
	name = strings.TrimSpace(name)
	for _, project := range projects {
		if strings.TrimSpace(project.Name) == name {
			return project, true
		}
	}
	return ProjectCairnlineSidecarProjectItem{}, false
}

func projectCairnlineSidecarRootByID(roots []ProjectCairnlineSidecarRootItem, id string) (ProjectCairnlineSidecarRootItem, bool) {
	id = strings.TrimSpace(id)
	for _, root := range roots {
		if strings.TrimSpace(root.ID) == id {
			return root, true
		}
	}
	return ProjectCairnlineSidecarRootItem{}, false
}

func projectCairnlineSidecarSourceByID(sources []ProjectCairnlineSidecarSourceItem, id string) (ProjectCairnlineSidecarSourceItem, bool) {
	id = strings.TrimSpace(id)
	for _, source := range sources {
		if strings.TrimSpace(source.ID) == id {
			return source, true
		}
	}
	return ProjectCairnlineSidecarSourceItem{}, false
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

func projectCairnlineSidecarStructuredRoots(raw json.RawMessage) ([]ProjectCairnlineSidecarRootItem, bool, error) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, false, nil
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return []ProjectCairnlineSidecarRootItem{}, true, nil
	}
	var roots []ProjectCairnlineSidecarRootItem
	if err := json.Unmarshal(trimmed, &roots); err != nil {
		return nil, false, err
	}
	if roots == nil {
		roots = []ProjectCairnlineSidecarRootItem{}
	}
	return roots, true, nil
}

func projectCairnlineSidecarStructuredRoot(raw json.RawMessage) (ProjectCairnlineSidecarRootItem, bool, error) {
	if len(raw) == 0 {
		return ProjectCairnlineSidecarRootItem{}, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ProjectCairnlineSidecarRootItem{}, false, nil
	}
	var root ProjectCairnlineSidecarRootItem
	if err := json.Unmarshal(trimmed, &root); err != nil {
		return ProjectCairnlineSidecarRootItem{}, false, err
	}
	return root, true, nil
}

func projectCairnlineSidecarStructuredSources(raw json.RawMessage) ([]ProjectCairnlineSidecarSourceItem, bool, error) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, false, nil
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return []ProjectCairnlineSidecarSourceItem{}, true, nil
	}
	var sources []ProjectCairnlineSidecarSourceItem
	if err := json.Unmarshal(trimmed, &sources); err != nil {
		return nil, false, err
	}
	if sources == nil {
		sources = []ProjectCairnlineSidecarSourceItem{}
	}
	return sources, true, nil
}

func projectCairnlineSidecarStructuredSource(raw json.RawMessage) (ProjectCairnlineSidecarSourceItem, bool, error) {
	if len(raw) == 0 {
		return ProjectCairnlineSidecarSourceItem{}, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ProjectCairnlineSidecarSourceItem{}, false, nil
	}
	var source ProjectCairnlineSidecarSourceItem
	if err := json.Unmarshal(trimmed, &source); err != nil {
		return ProjectCairnlineSidecarSourceItem{}, false, err
	}
	return source, true, nil
}

func projectCairnlineSidecarStructuredAssignments(raw json.RawMessage) ([]ProjectCairnlineSidecarAssignmentItem, bool, error) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, false, nil
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return []ProjectCairnlineSidecarAssignmentItem{}, true, nil
	}
	var assignments []ProjectCairnlineSidecarAssignmentItem
	if err := json.Unmarshal(trimmed, &assignments); err != nil {
		return nil, false, err
	}
	if assignments == nil {
		assignments = []ProjectCairnlineSidecarAssignmentItem{}
	}
	return assignments, true, nil
}

func projectCairnlineSidecarStructuredAssignmentContextIDs(raw json.RawMessage) (ProjectCairnlineSidecarAssignmentContextIDs, bool, error) {
	if len(raw) == 0 {
		return ProjectCairnlineSidecarAssignmentContextIDs{}, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ProjectCairnlineSidecarAssignmentContextIDs{}, false, nil
	}
	var context struct {
		Assignment struct {
			ID        string `json:"id"`
			ProjectID string `json:"project_id"`
		} `json:"assignment"`
		WorkItem struct {
			ID string `json:"id"`
		} `json:"work_item"`
		Role struct {
			ID string `json:"id"`
		} `json:"role"`
	}
	if err := json.Unmarshal(trimmed, &context); err != nil {
		return ProjectCairnlineSidecarAssignmentContextIDs{}, false, err
	}
	return ProjectCairnlineSidecarAssignmentContextIDs{
		AssignmentID: strings.TrimSpace(context.Assignment.ID),
		ProjectID:    strings.TrimSpace(context.Assignment.ProjectID),
		WorkItemID:   strings.TrimSpace(context.WorkItem.ID),
		RoleID:       strings.TrimSpace(context.Role.ID),
	}, true, nil
}

func projectCairnlineSidecarStructuredAssignmentContextAssignment(raw json.RawMessage) (ProjectCairnlineSidecarAssignmentItem, bool, error) {
	if len(raw) == 0 {
		return ProjectCairnlineSidecarAssignmentItem{}, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ProjectCairnlineSidecarAssignmentItem{}, false, nil
	}
	var context struct {
		Assignment ProjectCairnlineSidecarAssignmentItem `json:"assignment"`
	}
	if err := json.Unmarshal(trimmed, &context); err != nil {
		return ProjectCairnlineSidecarAssignmentItem{}, false, err
	}
	return context.Assignment, true, nil
}

func projectCairnlineSidecarStructuredLaunchPacket(raw json.RawMessage) (ProjectCairnlineSidecarLaunchPacketIDs, ProjectCairnlineSidecarLaunchPacketCounts, []string, bool, error) {
	if len(raw) == 0 {
		return ProjectCairnlineSidecarLaunchPacketIDs{}, ProjectCairnlineSidecarLaunchPacketCounts{}, nil, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ProjectCairnlineSidecarLaunchPacketIDs{}, ProjectCairnlineSidecarLaunchPacketCounts{}, nil, false, nil
	}
	var packet struct {
		ID      string `json:"id"`
		Kind    string `json:"kind"`
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
		WorkItem struct {
			ID string `json:"id"`
		} `json:"work_item"`
		Role struct {
			ID string `json:"id"`
		} `json:"role"`
		Profile struct {
			ID string `json:"id"`
		} `json:"profile"`
		ExecutionProfile struct {
			ID string `json:"id"`
		} `json:"execution_profile"`
		Assignment struct {
			ID        string `json:"id"`
			ProjectID string `json:"project_id"`
		} `json:"assignment"`
		Skills           []json.RawMessage `json:"skills"`
		Artifacts        []json.RawMessage `json:"artifacts"`
		Evidence         []json.RawMessage `json:"evidence"`
		Reviews          []json.RawMessage `json:"reviews"`
		Handoffs         []json.RawMessage `json:"handoffs"`
		Memory           []json.RawMessage `json:"memory"`
		MemoryCandidates []json.RawMessage `json:"memory_candidates"`
		Warnings         []string          `json:"warnings"`
	}
	if err := json.Unmarshal(trimmed, &packet); err != nil {
		return ProjectCairnlineSidecarLaunchPacketIDs{}, ProjectCairnlineSidecarLaunchPacketCounts{}, nil, false, err
	}
	ids := ProjectCairnlineSidecarLaunchPacketIDs{
		LaunchPacketID:     strings.TrimSpace(packet.ID),
		Kind:               strings.TrimSpace(packet.Kind),
		ProjectID:          firstNonEmpty(strings.TrimSpace(packet.Project.ID), strings.TrimSpace(packet.Assignment.ProjectID)),
		AssignmentID:       strings.TrimSpace(packet.Assignment.ID),
		WorkItemID:         strings.TrimSpace(packet.WorkItem.ID),
		RoleID:             strings.TrimSpace(packet.Role.ID),
		ProfileID:          strings.TrimSpace(packet.Profile.ID),
		ExecutionProfileID: strings.TrimSpace(packet.ExecutionProfile.ID),
	}
	counts := ProjectCairnlineSidecarLaunchPacketCounts{
		Skills:           len(packet.Skills),
		Artifacts:        len(packet.Artifacts),
		Evidence:         len(packet.Evidence),
		Reviews:          len(packet.Reviews),
		Handoffs:         len(packet.Handoffs),
		Memory:           len(packet.Memory),
		MemoryCandidates: len(packet.MemoryCandidates),
		Warnings:         len(packet.Warnings),
	}
	return ids, counts, append([]string(nil), packet.Warnings...), true, nil
}

func projectCairnlineSidecarStructuredArrayCount(raw json.RawMessage) (int, bool, error) {
	if len(raw) == 0 {
		return 0, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return 0, false, nil
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return 0, true, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(trimmed, &items); err != nil {
		return 0, false, err
	}
	return len(items), true, nil
}

func projectCairnlineSidecarCoordinationStructuredReady(items []ProjectCairnlineSidecarCoordinationListResult) bool {
	if len(items) != len(projectCairnlineSidecarCoordinationListTools) {
		return false
	}
	for _, item := range items {
		if item.ToolIsError || !item.StructuredReady || item.StructuredParseError != "" {
			return false
		}
	}
	return true
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

func (r *ProjectCairnlineSidecarCoordinationResponse) setSidecarCacheStats(stats mcpclient.CacheStats) {
	if r == nil {
		return
	}
	r.ClientCacheEntries = stats.Entries
	r.ClientCacheInUse = stats.InUse
	r.ClientCacheIdle = stats.Idle
}

func (r *ProjectCairnlineSidecarAssignmentContextResponse) setSidecarCacheStats(stats mcpclient.CacheStats) {
	if r == nil {
		return
	}
	r.ClientCacheEntries = stats.Entries
	r.ClientCacheInUse = stats.InUse
	r.ClientCacheIdle = stats.Idle
}

func (r *ProjectCairnlineSidecarLaunchPacketResponse) setSidecarCacheStats(stats mcpclient.CacheStats) {
	if r == nil {
		return
	}
	r.ClientCacheEntries = stats.Entries
	r.ClientCacheInUse = stats.InUse
	r.ClientCacheIdle = stats.Idle
}

func (r *ProjectCairnlineSidecarLifecycleResponse) setSidecarCacheStats(stats mcpclient.CacheStats) {
	if r == nil {
		return
	}
	r.ClientCacheEntries = stats.Entries
	r.ClientCacheInUse = stats.InUse
	r.ClientCacheIdle = stats.Idle
}

func (r *ProjectCairnlineSidecarWriteResponse) setSidecarCacheStats(stats mcpclient.CacheStats) {
	if r == nil {
		return
	}
	r.ClientCacheEntries = stats.Entries
	r.ClientCacheInUse = stats.InUse
	r.ClientCacheIdle = stats.Idle
}

func (r *ProjectCairnlineSidecarSetupResponse) setSidecarCacheStats(stats mcpclient.CacheStats) {
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
