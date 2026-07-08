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

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/mcp"
	mcpclient "github.com/hecatehq/hecate/internal/mcp/client"
	"github.com/hecatehq/hecate/internal/orchestrator"
	"github.com/hecatehq/hecate/internal/version"
	"github.com/hecatehq/hecate/pkg/types"
)

const projectCairnlineSidecarMCPServerName = "cairnline"

var projectCairnlineSidecarRequiredResourceTemplates = []string{
	"cairnline://projects/{project_id}",
	"cairnline://projects/{project_id}/work-items/{work_item_id}",
	"cairnline://projects/{project_id}/work-items/{work_item_id}/closeout-readiness",
	"cairnline://projects/{project_id}/assignments/{assignment_id}",
	"cairnline://projects/{project_id}/assignments/{assignment_id}/launch-packet",
	"cairnline://projects/{project_id}/memory-candidates/{memory_candidate_id}",
}

var projectCairnlineSidecarRequiredTools = []string{
	"coordination.capabilities",
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
		return "Cairnline sidecar connector is configured and can be exercised through local-only probe/connect/read/detail/resource/coordination/assignment-context/launch-packet/lifecycle/write/setup/work/collaboration/memory/assistant diagnostics. HECATE_PROJECTS_CAIRNLINE_READ_SOURCE=sidecar routes only " + projectCairnlineReadRouteList(projectCairnlineSidecarReadRouteNames) + " through the standalone Cairnline MCP client; writes and migration remain on Hecate-native stores or embedded dogfood paths."
	default:
		return "Hecate is using the embedded Cairnline Go package bridge for replacement-readiness dogfood."
	}
}

func projectCairnlineConnectorWarning(mode string) string {
	if mode != "sidecar" {
		return ""
	}
	return "HECATE_PROJECTS_CAIRNLINE_CONNECTOR=sidecar enables standalone Cairnline MCP probe/connect/read/detail/resource/coordination/assignment-context/launch-packet/lifecycle/write/setup/work/collaboration/memory/assistant diagnostics; add HECATE_PROJECTS_CAIRNLINE_READ_SOURCE=sidecar to route only " + projectCairnlineReadRouteList(projectCairnlineSidecarReadRouteNames) + " through the sidecar."
}

func projectCairnlineSidecarLiveReadDetail() string {
	return "Project writes stay on Hecate-native stores in sidecar mode; HECATE_PROJECTS_CAIRNLINE_READ_SOURCE=sidecar routes only " + projectCairnlineReadRouteList(projectCairnlineSidecarReadRouteNames) + " through the standalone Cairnline MCP client."
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

func (h *Handler) HandleProjectCairnlineSidecarResourceSmoke(w http.ResponseWriter, r *http.Request) {
	if !requireLoopbackClient(w, r, "Cairnline sidecar resource smoke") {
		return
	}
	var req ProjectCairnlineSidecarResourceRequest
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	WriteJSON(w, http.StatusOK, ProjectCairnlineSidecarResourceEnvelope{
		Object: "project_cairnline_sidecar_resource",
		Data:   h.projectCairnlineSidecarResourceSmoke(r.Context(), req),
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

func (h *Handler) HandleProjectCairnlineSidecarWorkSmoke(w http.ResponseWriter, r *http.Request) {
	if !requireLoopbackClient(w, r, "Cairnline sidecar work smoke") {
		return
	}
	var req ProjectCairnlineSidecarWorkRequest
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	WriteJSON(w, http.StatusOK, ProjectCairnlineSidecarWorkEnvelope{
		Object: "project_cairnline_sidecar_work",
		Data:   h.projectCairnlineSidecarWorkSmoke(r.Context(), req),
	})
}

func (h *Handler) HandleProjectCairnlineSidecarCollaborationSmoke(w http.ResponseWriter, r *http.Request) {
	if !requireLoopbackClient(w, r, "Cairnline sidecar collaboration smoke") {
		return
	}
	var req ProjectCairnlineSidecarCollaborationRequest
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	WriteJSON(w, http.StatusOK, ProjectCairnlineSidecarCollaborationEnvelope{
		Object: "project_cairnline_sidecar_collaboration",
		Data:   h.projectCairnlineSidecarCollaborationSmoke(r.Context(), req),
	})
}

func (h *Handler) HandleProjectCairnlineSidecarMemorySmoke(w http.ResponseWriter, r *http.Request) {
	if !requireLoopbackClient(w, r, "Cairnline sidecar memory smoke") {
		return
	}
	var req ProjectCairnlineSidecarMemoryRequest
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	WriteJSON(w, http.StatusOK, ProjectCairnlineSidecarMemoryEnvelope{
		Object: "project_cairnline_sidecar_memory",
		Data:   h.projectCairnlineSidecarMemorySmoke(r.Context(), req),
	})
}

func (h *Handler) HandleProjectCairnlineSidecarAssistantSmoke(w http.ResponseWriter, r *http.Request) {
	if !requireLoopbackClient(w, r, "Cairnline sidecar assistant smoke") {
		return
	}
	var req ProjectCairnlineSidecarAssistantRequest
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	WriteJSON(w, http.StatusOK, ProjectCairnlineSidecarAssistantEnvelope{
		Object: "project_cairnline_sidecar_assistant",
		Data:   h.projectCairnlineSidecarAssistantSmoke(r.Context(), req),
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
		RequiredResourceTemplates: append(
			[]string(nil),
			projectCairnlineSidecarRequiredResourceTemplates...,
		),
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
	result, err := orchestrator.ProbeMCPServerWithResourceTemplates(probeCtx, cfg, h.secretCipher)
	if err != nil {
		response.Status = "sidecar_probe_failed"
		response.Detail = err.Error()
		return response
	}
	response.ServerName = result.ServerName
	response.ServerVersion = result.ServerVersion
	response.Tools = renderMCPProbeTools(result.Tools)
	response.ToolCount = len(response.Tools)
	response.ResourceTemplates = renderMCPProbeResourceTemplates(result.ResourceTemplates)
	response.ResourceTemplateCount = len(response.ResourceTemplates)
	response.ResourceTemplateError = result.ResourceTemplateError
	response.MissingTools = projectCairnlineSidecarMissingTools(projectCairnlineSidecarToolNames(response.Tools))
	response.MissingResourceTemplates = projectCairnlineSidecarMissingResourceTemplates(projectCairnlineSidecarResourceTemplateURIs(response.ResourceTemplates))
	if len(response.MissingTools) > 0 {
		response.Status = "sidecar_contract_incomplete"
		response.Detail = "Cairnline sidecar MCP server started, but it does not expose every tool Hecate needs for a future Projects backend connector."
		return response
	}
	if response.ResourceTemplateError != "" {
		response.Status = "sidecar_contract_incomplete"
		response.Detail = "Cairnline sidecar MCP server started, but resources/templates/list failed; install Cairnline v0.1.0-alpha.2 or newer for the full portable Projects resource contract."
		return response
	}
	if len(response.MissingResourceTemplates) > 0 {
		response.Status = "sidecar_contract_incomplete"
		response.Detail = "Cairnline sidecar MCP server started, but it does not expose every resource template Hecate expects for portable Projects diagnostics."
		return response
	}
	response.Ready = true
	response.Status = "sidecar_probe_ready"
	response.Detail = "Cairnline sidecar MCP server started and exposes the required portable Projects tool and resource-template contract. " + projectCairnlineSidecarLiveReadDetail()
	return response
}

func (h *Handler) projectCairnlineSidecarConnect(ctx context.Context) ProjectCairnlineSidecarProbeResponse {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, dbPath, timeout := h.projectCairnlineSidecarMCPConfig()
	response := ProjectCairnlineSidecarProbeResponse{
		Ready:          false,
		Status:         "sidecar_client_not_connected",
		Detail:         "Cairnline sidecar client has not connected.",
		Command:        cfg.Command,
		Args:           append([]string(nil), cfg.Args...),
		DatabasePath:   dbPath,
		ProbeTimeoutMS: timeout.Milliseconds(),
		RequiredTools:  append([]string(nil), projectCairnlineSidecarRequiredTools...),
		RequiredResourceTemplates: append(
			[]string(nil),
			projectCairnlineSidecarRequiredResourceTemplates...,
		),
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
	result, err := orchestrator.ProbeCachedMCPServerWithResourceTemplates(connectCtx, cfg, h.secretCipher, cache)
	if err != nil {
		response.Status = "sidecar_client_failed"
		response.Detail = err.Error()
		response.setSidecarCacheStats(cache.Stats())
		return response
	}
	response.ServerName = result.ServerName
	response.ServerVersion = result.ServerVersion
	response.Tools = renderMCPProbeTools(result.Tools)
	response.ToolCount = len(response.Tools)
	response.ResourceTemplates = renderMCPProbeResourceTemplates(result.ResourceTemplates)
	response.ResourceTemplateCount = len(response.ResourceTemplates)
	response.ResourceTemplateError = result.ResourceTemplateError
	response.MissingTools = projectCairnlineSidecarMissingTools(projectCairnlineSidecarToolNames(response.Tools))
	response.MissingResourceTemplates = projectCairnlineSidecarMissingResourceTemplates(projectCairnlineSidecarResourceTemplateURIs(response.ResourceTemplates))
	response.setSidecarCacheStats(cache.Stats())
	if len(response.MissingTools) > 0 {
		response.Status = "sidecar_contract_incomplete"
		response.Detail = "Cairnline sidecar MCP client connected, but it does not expose every tool Hecate needs for a future Projects backend connector."
		return response
	}
	if response.ResourceTemplateError != "" {
		response.Status = "sidecar_contract_incomplete"
		response.Detail = "Cairnline sidecar MCP client connected, but resources/templates/list failed; install Cairnline v0.1.0-alpha.2 or newer for the full portable Projects resource contract."
		return response
	}
	if len(response.MissingResourceTemplates) > 0 {
		response.Status = "sidecar_contract_incomplete"
		response.Detail = "Cairnline sidecar MCP client connected, but it does not expose every resource template Hecate expects for portable Projects diagnostics."
		return response
	}
	h.appendProjectCairnlineSidecarCoordinationCapabilities(connectCtx, cfg, cache, &response)
	response.setSidecarCacheStats(cache.Stats())
	response.Ready = true
	response.Status = "sidecar_client_ready"
	response.Detail = "Cairnline sidecar MCP client connected and exposes the required portable Projects tool and resource-template contract. " + projectCairnlineSidecarLiveReadDetail()
	return response
}

func (h *Handler) appendProjectCairnlineSidecarCoordinationCapabilities(ctx context.Context, cfg types.MCPServerConfig, cache *mcpclient.SharedClientCache, response *ProjectCairnlineSidecarProbeResponse) {
	if h == nil || response == nil {
		return
	}
	result, err := orchestrator.CallCachedMCPServerTool(ctx, cfg, h.secretCipher, cache, "coordination.capabilities", json.RawMessage(`{}`))
	if err != nil {
		response.Warnings = append(response.Warnings, "Cairnline sidecar coordination.capabilities call failed: "+err.Error())
		return
	}
	if result.IsError {
		detail := strings.TrimSpace(result.Text)
		if detail == "" {
			detail = "tool-level error"
		}
		response.Warnings = append(response.Warnings, "Cairnline sidecar coordination.capabilities returned a tool-level error: "+detail)
		return
	}
	capabilities, structuredReady, structuredErr := projectCairnlineSidecarStructuredCoordinationCapabilities(result.Result.StructuredContent)
	if structuredErr != nil {
		response.Warnings = append(response.Warnings, "Cairnline sidecar coordination.capabilities returned structuredContent that Hecate could not parse.")
		return
	}
	if !structuredReady {
		response.Warnings = append(response.Warnings, "Cairnline sidecar coordination.capabilities did not return structuredContent; Hecate verified the tool exists but not its typed self-description.")
		return
	}
	response.CoordinationCapabilities = &capabilities
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
		response.Detail = "Cairnline sidecar projects.list returned a tool-level error. " + projectCairnlineSidecarLiveReadDetail()
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
	response.Detail = "Hecate called the read-only Cairnline sidecar projects.list tool through the persistent sidecar client. " + projectCairnlineSidecarLiveReadDetail()
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
			response.Detail = "Cairnline sidecar projects.list returned a tool-level error before Hecate could select a project for projects.get. " + projectCairnlineSidecarLiveReadDetail()
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
		response.Detail = "Cairnline sidecar projects.get returned a tool-level error. " + projectCairnlineSidecarLiveReadDetail()
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
	response.Detail = "Hecate called the read-only Cairnline sidecar projects.get tool through the persistent sidecar client. " + projectCairnlineSidecarLiveReadDetail()
	return response
}

func (h *Handler) projectCairnlineSidecarResourceSmoke(ctx context.Context, req ProjectCairnlineSidecarResourceRequest) ProjectCairnlineSidecarResourceResponse {
	const listToolName = "projects.list"
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, dbPath, timeout := h.projectCairnlineSidecarMCPConfig()
	requestedProjectID := strings.TrimSpace(req.ProjectID)
	requestedResourceURI := strings.TrimSpace(req.ResourceURI)
	response := ProjectCairnlineSidecarResourceResponse{
		Ready:                 false,
		Status:                "sidecar_resource_not_run",
		Detail:                "Cairnline sidecar resource smoke has not run.",
		Command:               cfg.Command,
		Args:                  append([]string(nil), cfg.Args...),
		DatabasePath:          dbPath,
		ProbeTimeoutMS:        timeout.Milliseconds(),
		PersistentClient:      true,
		ClientCacheConfigured: h != nil,
		ReadOnly:              true,
		RequestedProjectID:    requestedProjectID,
		RequestedResourceURI:  requestedResourceURI,
	}
	if h == nil {
		response.Status = "sidecar_resource_failed"
		response.Detail = "Cairnline sidecar resource smoke requires an API handler."
		return response
	}
	if h.projectCairnlineConnectorMode() != "sidecar" {
		response.Warnings = append(response.Warnings, "HECATE_PROJECTS_CAIRNLINE_CONNECTOR is not sidecar; this resource smoke does not affect live Projects routing.")
	}
	if dbPath != "" && len(h.config.ProjectsCairnlineSidecarArgs()) > 0 {
		response.Warnings = append(response.Warnings, "HECATE_PROJECTS_CAIRNLINE_SIDECAR_ARGS is set, so HECATE_PROJECTS_CAIRNLINE_SIDECAR_DB is reported but not appended automatically.")
	}

	cache := h.projectCairnlineSidecarMCPClientCache()
	resourceCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resourceURI := requestedResourceURI
	projectID := requestedProjectID
	if resourceURI != "" {
		if !strings.HasPrefix(resourceURI, "cairnline://projects/") {
			response.Status = "sidecar_resource_invalid_uri"
			response.Detail = "Cairnline sidecar resource smoke only reads cairnline://projects/... resource URIs."
			response.setSidecarCacheStats(cache.Stats())
			return response
		}
		if projectID == "" {
			projectID = projectCairnlineSidecarProjectIDFromResourceURI(resourceURI)
			if projectID != "" {
				response.SelectedProjectSource = "resource_uri"
			}
		}
	} else {
		if projectID == "" {
			listResult, err := orchestrator.CallCachedMCPServerTool(resourceCtx, cfg, h.secretCipher, cache, listToolName, json.RawMessage(`{}`))
			if err != nil {
				response.Status = "sidecar_resource_failed"
				response.Detail = err.Error()
				response.setSidecarCacheStats(cache.Stats())
				return response
			}
			response.ListToolText = listResult.Text
			response.ListToolIsError = listResult.IsError
			response.ListStructuredContent = listResult.Result.StructuredContent
			response.ListMeta = listResult.Result.Meta
			if listResult.IsError {
				response.Status = "sidecar_resource_list_tool_failed"
				response.Detail = "Cairnline sidecar projects.list returned a tool-level error before Hecate could select a project resource. " + projectCairnlineSidecarLiveReadDetail()
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
				response.Warnings = append(response.Warnings, "Cairnline sidecar projects.list did not return structuredContent, so Hecate could not select a project resource.")
			} else if len(projects) > 0 {
				projectID = strings.TrimSpace(projects[0].ID)
				response.SelectedProjectSource = "projects.list"
			}
			if projectID == "" {
				response.Status = "sidecar_resource_no_project"
				response.Detail = "Hecate called Cairnline sidecar projects.list through the persistent sidecar client, but no typed project id was available for resources/read."
				response.setSidecarCacheStats(cache.Stats())
				return response
			}
		} else {
			response.SelectedProjectSource = "request"
		}
		resourceURI = "cairnline://projects/" + projectID
	}
	response.SelectedProjectID = projectID
	response.ResourceURI = resourceURI

	result, err := orchestrator.ReadCachedMCPServerResource(resourceCtx, cfg, h.secretCipher, cache, resourceURI)
	if err != nil {
		response.Status = "sidecar_resource_failed"
		response.Detail = err.Error()
		response.setSidecarCacheStats(cache.Stats())
		return response
	}
	response.Contents = renderMCPResourceContents(result.Result.Contents)
	response.ContentCount = len(response.Contents)
	response.setSidecarCacheStats(cache.Stats())
	if response.ContentCount == 0 {
		response.Status = "sidecar_resource_empty"
		response.Detail = "Cairnline sidecar resources/read returned no content for the selected resource."
		return response
	}
	if projectIDFromBody, ok, err := projectCairnlineSidecarProjectResourceID(response.Contents); err != nil {
		response.StructuredParseError = err.Error()
		response.Warnings = append(response.Warnings, "Cairnline sidecar project resource returned JSON that Hecate could not parse.")
	} else if ok {
		response.StructuredReady = true
		response.StructuredProjectID = projectIDFromBody
		if projectID != "" && projectIDFromBody != projectID {
			response.Warnings = append(response.Warnings, "Cairnline sidecar project resource returned a project id different from the selected id.")
		}
	}
	response.Ready = true
	response.Status = "sidecar_resource_ready"
	response.Detail = "Hecate read a Cairnline MCP resource through the persistent sidecar client. " + projectCairnlineSidecarLiveReadDetail()
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
			response.Detail = "Cairnline sidecar " + result.Tool + " returned a tool-level error. " + projectCairnlineSidecarLiveReadDetail()
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
	response.Detail = "Hecate called read-only Cairnline sidecar coordination list tools through the persistent sidecar client. " + projectCairnlineSidecarLiveReadDetail()
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
		response.Detail = "Cairnline sidecar assignments.context returned a tool-level error. " + projectCairnlineSidecarLiveReadDetail()
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
	response.Detail = "Hecate called the read-only Cairnline sidecar assignments.context tool through the persistent sidecar client. " + projectCairnlineSidecarLiveReadDetail()
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
		response.Detail = "Cairnline sidecar assignments.launch_packet returned a tool-level error. " + projectCairnlineSidecarLiveReadDetail()
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
	response.Detail = "Hecate called the read-only Cairnline sidecar assignments.launch_packet tool through the persistent sidecar client. " + projectCairnlineSidecarLiveReadDetail()
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

	// Cairnline's structured execution-ref contract rejects bare-string refs,
	// so the smoke's operator-supplied ref rides as a run id object.
	runningStep := h.callProjectCairnlineSidecarLifecycleTool(smokeCtx, cfg, cache, "mark_running", "assignments.update_status", false, map[string]any{
		"project_id":    projectID,
		"assignment_id": assignmentID,
		"status":        "running",
		"execution_ref": map[string]string{"run_id": executionRef},
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

	completeStep := h.callProjectCairnlineSidecarLifecycleTool(smokeCtx, cfg, cache, "complete", "assignments.complete", false, map[string]any{
		"project_id":    projectID,
		"assignment_id": assignmentID,
		"status":        completionStatus,
		"execution_ref": map[string]string{"run_id": executionRef},
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

func (h *Handler) projectCairnlineSidecarWorkSmoke(ctx context.Context, req ProjectCairnlineSidecarWorkRequest) ProjectCairnlineSidecarWorkResponse {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, dbPath, timeout := h.projectCairnlineSidecarMCPConfig()
	projectName := strings.TrimSpace(req.ProjectName)
	if projectName == "" {
		projectName = "Hecate sidecar work smoke " + time.Now().UTC().Format("20060102T150405.000000000Z")
	}
	roleName := "Sidecar work smoke operator"
	workTitle := "Sidecar work smoke task"
	response := ProjectCairnlineSidecarWorkResponse{
		Ready:                 false,
		Status:                "sidecar_work_not_run",
		Detail:                "Cairnline sidecar work smoke has not run.",
		Command:               cfg.Command,
		Args:                  append([]string(nil), cfg.Args...),
		DatabasePath:          dbPath,
		ProbeTimeoutMS:        timeout.Milliseconds(),
		PersistentClient:      true,
		ClientCacheConfigured: h != nil,
		ConfirmedMutation:     req.ConfirmMutation,
		ProjectName:           projectName,
	}
	if h == nil {
		response.Status = "sidecar_work_failed"
		response.Detail = "Cairnline sidecar work smoke requires an API handler."
		return response
	}
	if h.projectCairnlineConnectorMode() != "sidecar" {
		response.Warnings = append(response.Warnings, "HECATE_PROJECTS_CAIRNLINE_CONNECTOR is not sidecar; this work smoke does not affect live Projects routing.")
	}
	if dbPath != "" && len(h.config.ProjectsCairnlineSidecarArgs()) > 0 {
		response.Warnings = append(response.Warnings, "HECATE_PROJECTS_CAIRNLINE_SIDECAR_ARGS is set, so HECATE_PROJECTS_CAIRNLINE_SIDECAR_DB is reported but not appended automatically.")
	}
	if !req.ConfirmMutation {
		response.Status = "sidecar_work_confirmation_required"
		response.Detail = "Set confirm_mutation=true to let Hecate create, verify, read context for, and clean up temporary role/work/assignment records in the standalone Cairnline sidecar. Hecate-native Projects stores are not mutated."
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
				response.Warnings = append(response.Warnings, "Hecate deleted and verified removal of the temporary standalone Cairnline work project after the work smoke failed; inspect the reported steps before retrying.")
			} else {
				response.Warnings = append(response.Warnings, "Hecate deleted the temporary standalone Cairnline work project after the work smoke failed, but removal could not be verified; inspect the reported steps before retrying.")
			}
			response.Steps = append(response.Steps, verifyCleanupStep)
			return
		}
		response.Warnings = append(response.Warnings, "Hecate tried to delete the temporary standalone Cairnline work project after the work smoke failed, but cleanup did not succeed; inspect the standalone Cairnline sidecar before retrying.")
	}
	fail := func(status, detail string) ProjectCairnlineSidecarWorkResponse {
		response.Status = status
		response.Detail = detail
		cleanup()
		response.setSidecarCacheStats(cache.Stats())
		return response
	}
	appendStep := func(step ProjectCairnlineSidecarWriteStep) bool {
		response.Steps = append(response.Steps, step)
		if step.Status == "tool_failed" {
			response.Status = "sidecar_work_tool_failed"
			response.Detail = "Cairnline sidecar " + step.Tool + " returned a tool-level error. Review the step output before retrying."
			cleanup()
			response.setSidecarCacheStats(cache.Stats())
			return false
		}
		if step.Status == "failed" {
			response.Status = "sidecar_work_failed"
			response.Detail = firstNonEmpty(step.ToolText, "Cairnline sidecar "+step.Tool+" failed.")
			cleanup()
			response.setSidecarCacheStats(cache.Stats())
			return false
		}
		return true
	}

	createProject := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "create_project", "projects.create", false, map[string]string{
		"name":        projectName,
		"description": "Temporary Hecate sidecar work smoke project. It should be deleted by the same diagnostic run.",
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
		return fail("sidecar_work_created_project_not_listed", "Cairnline sidecar projects.create succeeded, but projects.list did not return the temporary work project by name.")
	}
	projectID = strings.TrimSpace(project.ID)
	response.SelectedProjectID = projectID

	createRole := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "create_role", "roles.create", false, map[string]any{
		"project_id":             projectID,
		"name":                   roleName,
		"description":            "Temporary role created by the Hecate sidecar work smoke.",
		"instructions":           "Coordinate the temporary work smoke assignment.",
		"default_execution_mode": "mcp_pull",
	})
	if !appendStep(createRole) {
		return response
	}
	listRoles := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "list_roles_after_create", "roles.list", true, map[string]string{"project_id": projectID})
	if !appendStep(listRoles) {
		return response
	}
	role, ok := projectCairnlineSidecarRoleByName(listRoles.StructuredRoles, roleName)
	if !ok || strings.TrimSpace(role.ID) == "" || role.DefaultExecutionMode != "mcp_pull" {
		return fail("sidecar_work_role_verification_failed", "Cairnline sidecar roles.list did not return the expected temporary mcp_pull role.")
	}
	response.RoleID = strings.TrimSpace(role.ID)
	response.CreatedRole = role

	createWork := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "create_work_item", "work_items.create", false, map[string]string{
		"project_id":    projectID,
		"title":         workTitle,
		"brief":         "Temporary reviewable work item created by the Hecate sidecar work smoke.",
		"owner_role_id": response.RoleID,
	})
	if !appendStep(createWork) {
		return response
	}
	listWork := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "list_work_items_after_create", "work_items.list", true, map[string]string{"project_id": projectID})
	if !appendStep(listWork) {
		return response
	}
	work, ok := projectCairnlineSidecarWorkItemByTitle(listWork.StructuredWorkItems, workTitle)
	if !ok || strings.TrimSpace(work.ID) == "" || strings.TrimSpace(work.OwnerRoleID) != response.RoleID {
		return fail("sidecar_work_item_verification_failed", "Cairnline sidecar work_items.list did not return the expected temporary work item.")
	}
	response.WorkItemID = strings.TrimSpace(work.ID)
	response.CreatedWorkItem = work

	createAssignment := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "create_assignment", "assignments.create", false, map[string]any{
		"project_id":         projectID,
		"work_item_id":       response.WorkItemID,
		"role_id":            response.RoleID,
		"execution_mode":     "mcp_pull",
		"desired_agent_kind": "any",
	})
	if !appendStep(createAssignment) {
		return response
	}
	listAssignments := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "list_assignments_after_create", "assignments.list", true, map[string]string{"project_id": projectID})
	if !appendStep(listAssignments) {
		return response
	}
	assignment, ok := projectCairnlineSidecarAssignmentByWorkAndRole(listAssignments.StructuredAssignments, response.WorkItemID, response.RoleID)
	if !ok || strings.TrimSpace(assignment.ID) == "" || assignment.ExecutionMode != "mcp_pull" {
		return fail("sidecar_work_assignment_verification_failed", "Cairnline sidecar assignments.list did not return the expected temporary mcp_pull assignment.")
	}
	response.AssignmentID = strings.TrimSpace(assignment.ID)
	response.CreatedAssignment = assignment

	contextStep := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "read_assignment_context", "assignments.context", true, map[string]string{"project_id": projectID, "assignment_id": response.AssignmentID})
	if !appendStep(contextStep) {
		return response
	}
	if contextStep.AssignmentContextIDs.AssignmentID != response.AssignmentID || contextStep.AssignmentContextIDs.WorkItemID != response.WorkItemID || contextStep.AssignmentContextIDs.RoleID != response.RoleID {
		return fail("sidecar_work_context_verification_failed", "Cairnline sidecar assignments.context did not return the expected assignment/work/role ids.")
	}
	response.ContextIDs = contextStep.AssignmentContextIDs

	launchStep := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "read_launch_packet", "assignments.launch_packet", true, map[string]string{"project_id": projectID, "assignment_id": response.AssignmentID})
	if !appendStep(launchStep) {
		return response
	}
	if launchStep.LaunchPacketIDs.AssignmentID != response.AssignmentID || launchStep.LaunchPacketIDs.WorkItemID != response.WorkItemID || launchStep.LaunchPacketIDs.RoleID != response.RoleID {
		return fail("sidecar_work_launch_packet_verification_failed", "Cairnline sidecar assignments.launch_packet did not return the expected assignment/work/role ids.")
	}
	response.LaunchPacketIDs = launchStep.LaunchPacketIDs

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
		response.Status = "sidecar_work_ready"
		response.Detail = "Hecate created and verified temporary standalone Cairnline role, work item, assignment, assignment context, and launch packet metadata through the persistent sidecar client, then deleted the temporary project. Hecate-native Projects stores were not mutated."
		response.setSidecarCacheStats(cache.Stats())
		return response
	}
	if !appendStep(verifyDelete) {
		return response
	}
	return fail("sidecar_work_project_delete_verification_failed", "Cairnline sidecar projects.delete succeeded, but projects.get still returned the temporary work project.")
}

func (h *Handler) projectCairnlineSidecarCollaborationSmoke(ctx context.Context, req ProjectCairnlineSidecarCollaborationRequest) ProjectCairnlineSidecarCollaborationResponse {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, dbPath, timeout := h.projectCairnlineSidecarMCPConfig()
	projectName := strings.TrimSpace(req.ProjectName)
	if projectName == "" {
		projectName = "Hecate sidecar collaboration smoke " + time.Now().UTC().Format("20060102T150405.000000000Z")
	}
	const (
		authorRoleName   = "Sidecar collaboration author"
		reviewerRoleName = "Sidecar collaboration reviewer"
		workTitle        = "Sidecar collaboration smoke task"
		artifactTitle    = "Sidecar collaboration artifact"
		evidenceTitle    = "Sidecar collaboration evidence"
		reviewTitle      = "Sidecar collaboration review"
		handoffTitle     = "Sidecar collaboration handoff"
	)
	response := ProjectCairnlineSidecarCollaborationResponse{
		Ready:                 false,
		Status:                "sidecar_collaboration_not_run",
		Detail:                "Cairnline sidecar collaboration smoke has not run.",
		Command:               cfg.Command,
		Args:                  append([]string(nil), cfg.Args...),
		DatabasePath:          dbPath,
		ProbeTimeoutMS:        timeout.Milliseconds(),
		PersistentClient:      true,
		ClientCacheConfigured: h != nil,
		ConfirmedMutation:     req.ConfirmMutation,
		ProjectName:           projectName,
	}
	if h == nil {
		response.Status = "sidecar_collaboration_failed"
		response.Detail = "Cairnline sidecar collaboration smoke requires an API handler."
		return response
	}
	if h.projectCairnlineConnectorMode() != "sidecar" {
		response.Warnings = append(response.Warnings, "HECATE_PROJECTS_CAIRNLINE_CONNECTOR is not sidecar; this collaboration smoke does not affect live Projects routing.")
	}
	if dbPath != "" && len(h.config.ProjectsCairnlineSidecarArgs()) > 0 {
		response.Warnings = append(response.Warnings, "HECATE_PROJECTS_CAIRNLINE_SIDECAR_ARGS is set, so HECATE_PROJECTS_CAIRNLINE_SIDECAR_DB is reported but not appended automatically.")
	}
	if !req.ConfirmMutation {
		response.Status = "sidecar_collaboration_confirmation_required"
		response.Detail = "Set confirm_mutation=true to let Hecate create, verify, and clean up temporary collaboration artifact, evidence, review, and handoff records in the standalone Cairnline sidecar. Hecate-native Projects stores are not mutated."
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
				response.Warnings = append(response.Warnings, "Hecate deleted and verified removal of the temporary standalone Cairnline collaboration project after the collaboration smoke failed; inspect the reported steps before retrying.")
			} else {
				response.Warnings = append(response.Warnings, "Hecate deleted the temporary standalone Cairnline collaboration project after the collaboration smoke failed, but removal could not be verified; inspect the reported steps before retrying.")
			}
			response.Steps = append(response.Steps, verifyCleanupStep)
			return
		}
		response.Warnings = append(response.Warnings, "Hecate tried to delete the temporary standalone Cairnline collaboration project after the collaboration smoke failed, but cleanup did not succeed; inspect the standalone Cairnline sidecar before retrying.")
	}
	fail := func(status, detail string) ProjectCairnlineSidecarCollaborationResponse {
		response.Status = status
		response.Detail = detail
		cleanup()
		response.setSidecarCacheStats(cache.Stats())
		return response
	}
	appendStep := func(step ProjectCairnlineSidecarWriteStep) bool {
		response.Steps = append(response.Steps, step)
		if step.Status == "tool_failed" {
			response.Status = "sidecar_collaboration_tool_failed"
			response.Detail = "Cairnline sidecar " + step.Tool + " returned a tool-level error. Review the step output before retrying."
			cleanup()
			response.setSidecarCacheStats(cache.Stats())
			return false
		}
		if step.Status == "failed" {
			response.Status = "sidecar_collaboration_failed"
			response.Detail = firstNonEmpty(step.ToolText, "Cairnline sidecar "+step.Tool+" failed.")
			cleanup()
			response.setSidecarCacheStats(cache.Stats())
			return false
		}
		return true
	}

	createProject := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "create_project", "projects.create", false, map[string]string{
		"name":        projectName,
		"description": "Temporary Hecate sidecar collaboration smoke project. It should be deleted by the same diagnostic run.",
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
		return fail("sidecar_collaboration_created_project_not_listed", "Cairnline sidecar projects.create succeeded, but projects.list did not return the temporary collaboration project by name.")
	}
	projectID = strings.TrimSpace(project.ID)
	response.SelectedProjectID = projectID

	createAuthorRole := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "create_author_role", "roles.create", false, map[string]any{
		"project_id":             projectID,
		"name":                   authorRoleName,
		"description":            "Temporary author role created by the Hecate sidecar collaboration smoke.",
		"instructions":           "Produce collaboration evidence for the temporary smoke.",
		"default_execution_mode": "mcp_pull",
	})
	if !appendStep(createAuthorRole) {
		return response
	}
	createReviewerRole := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "create_reviewer_role", "roles.create", false, map[string]any{
		"project_id":             projectID,
		"name":                   reviewerRoleName,
		"description":            "Temporary reviewer role created by the Hecate sidecar collaboration smoke.",
		"instructions":           "Review collaboration evidence for the temporary smoke.",
		"default_execution_mode": "manual",
	})
	if !appendStep(createReviewerRole) {
		return response
	}
	listRoles := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "list_roles_after_create", "roles.list", true, map[string]string{"project_id": projectID})
	if !appendStep(listRoles) {
		return response
	}
	authorRole, ok := projectCairnlineSidecarRoleByName(listRoles.StructuredRoles, authorRoleName)
	if !ok || strings.TrimSpace(authorRole.ID) == "" || authorRole.DefaultExecutionMode != "mcp_pull" {
		return fail("sidecar_collaboration_author_role_verification_failed", "Cairnline sidecar roles.list did not return the expected temporary author role.")
	}
	reviewerRole, ok := projectCairnlineSidecarRoleByName(listRoles.StructuredRoles, reviewerRoleName)
	if !ok || strings.TrimSpace(reviewerRole.ID) == "" || reviewerRole.DefaultExecutionMode != "manual" {
		return fail("sidecar_collaboration_reviewer_role_verification_failed", "Cairnline sidecar roles.list did not return the expected temporary reviewer role.")
	}
	response.AuthorRoleID = strings.TrimSpace(authorRole.ID)
	response.ReviewerRoleID = strings.TrimSpace(reviewerRole.ID)

	createWork := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "create_work_item", "work_items.create", false, map[string]string{
		"project_id":    projectID,
		"title":         workTitle,
		"brief":         "Temporary collaboration work item created by the Hecate sidecar collaboration smoke.",
		"owner_role_id": response.AuthorRoleID,
	})
	if !appendStep(createWork) {
		return response
	}
	listWork := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "list_work_items_after_create", "work_items.list", true, map[string]string{"project_id": projectID})
	if !appendStep(listWork) {
		return response
	}
	work, ok := projectCairnlineSidecarWorkItemByTitle(listWork.StructuredWorkItems, workTitle)
	if !ok || strings.TrimSpace(work.ID) == "" || strings.TrimSpace(work.OwnerRoleID) != response.AuthorRoleID {
		return fail("sidecar_collaboration_work_item_verification_failed", "Cairnline sidecar work_items.list did not return the expected temporary collaboration work item.")
	}
	response.WorkItemID = strings.TrimSpace(work.ID)

	createAssignment := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "create_assignment", "assignments.create", false, map[string]any{
		"project_id":         projectID,
		"work_item_id":       response.WorkItemID,
		"role_id":            response.AuthorRoleID,
		"execution_mode":     "mcp_pull",
		"desired_agent_kind": "any",
	})
	if !appendStep(createAssignment) {
		return response
	}
	listAssignments := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "list_assignments_after_create", "assignments.list", true, map[string]string{"project_id": projectID})
	if !appendStep(listAssignments) {
		return response
	}
	assignment, ok := projectCairnlineSidecarAssignmentByWorkAndRole(listAssignments.StructuredAssignments, response.WorkItemID, response.AuthorRoleID)
	if !ok || strings.TrimSpace(assignment.ID) == "" || assignment.ExecutionMode != "mcp_pull" {
		return fail("sidecar_collaboration_assignment_verification_failed", "Cairnline sidecar assignments.list did not return the expected temporary collaboration assignment.")
	}
	response.AssignmentID = strings.TrimSpace(assignment.ID)

	createArtifact := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "create_artifact", "artifacts.create", false, map[string]string{
		"project_id":      projectID,
		"work_item_id":    response.WorkItemID,
		"assignment_id":   response.AssignmentID,
		"kind":            "diagnostic_note",
		"title":           artifactTitle,
		"body":            "Temporary artifact created by the Hecate sidecar collaboration smoke.",
		"author_role_id":  response.AuthorRoleID,
		"provenance_kind": "hecate_sidecar_smoke",
		"trust_label":     "diagnostic",
	})
	if !appendStep(createArtifact) {
		return response
	}
	if createArtifact.StructuredArtifact.Title != artifactTitle || createArtifact.StructuredArtifact.AssignmentID != response.AssignmentID {
		return fail("sidecar_collaboration_artifact_create_verification_failed", "Cairnline sidecar artifacts.create did not return the expected temporary artifact.")
	}
	listArtifacts := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "list_artifacts_after_create", "artifacts.list", true, map[string]string{"project_id": projectID, "work_item_id": response.WorkItemID})
	if !appendStep(listArtifacts) {
		return response
	}
	artifact, ok := projectCairnlineSidecarArtifactByTitle(listArtifacts.StructuredArtifacts, artifactTitle)
	if !ok || strings.TrimSpace(artifact.ID) == "" || artifact.AssignmentID != response.AssignmentID || artifact.Kind != "diagnostic_note" {
		return fail("sidecar_collaboration_artifact_list_verification_failed", "Cairnline sidecar artifacts.list did not return the expected temporary artifact.")
	}
	getArtifact := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "get_artifact", "artifacts.get", true, map[string]string{"project_id": projectID, "work_item_id": response.WorkItemID, "artifact_id": artifact.ID})
	if !appendStep(getArtifact) {
		return response
	}
	if getArtifact.StructuredArtifact.ID != artifact.ID || getArtifact.StructuredArtifact.Title != artifactTitle {
		return fail("sidecar_collaboration_artifact_get_verification_failed", "Cairnline sidecar artifacts.get did not return the expected temporary artifact.")
	}
	response.CreatedArtifact = getArtifact.StructuredArtifact

	recordEvidence := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "record_evidence", "evidence.record", false, map[string]string{
		"project_id":    projectID,
		"work_item_id":  response.WorkItemID,
		"assignment_id": response.AssignmentID,
		"title":         evidenceTitle,
		"body":          "Temporary evidence created by the Hecate sidecar collaboration smoke.",
		"locator":       "file://hecate-sidecar-collaboration-smoke.md",
		"source_kind":   "diagnostic",
		"provider":      "hecate",
		"trust_label":   "diagnostic",
	})
	if !appendStep(recordEvidence) {
		return response
	}
	listEvidence := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "list_evidence_after_record", "evidence.list", true, map[string]string{"project_id": projectID, "work_item_id": response.WorkItemID})
	if !appendStep(listEvidence) {
		return response
	}
	evidence, ok := projectCairnlineSidecarEvidenceByTitle(listEvidence.StructuredEvidence, evidenceTitle)
	if !ok || strings.TrimSpace(evidence.ID) == "" || evidence.AssignmentID != response.AssignmentID || evidence.Locator == "" {
		return fail("sidecar_collaboration_evidence_list_verification_failed", "Cairnline sidecar evidence.list did not return the expected temporary evidence.")
	}
	getEvidence := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "get_evidence", "evidence.get", true, map[string]string{"project_id": projectID, "work_item_id": response.WorkItemID, "evidence_id": evidence.ID})
	if !appendStep(getEvidence) {
		return response
	}
	if getEvidence.StructuredEvidenceItem.ID != evidence.ID || getEvidence.StructuredEvidenceItem.Title != evidenceTitle {
		return fail("sidecar_collaboration_evidence_get_verification_failed", "Cairnline sidecar evidence.get did not return the expected temporary evidence.")
	}
	response.CreatedEvidence = getEvidence.StructuredEvidenceItem

	recordReview := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "record_review", "reviews.record", false, map[string]string{
		"project_id":       projectID,
		"work_item_id":     response.WorkItemID,
		"assignment_id":    response.AssignmentID,
		"reviewer_role_id": response.ReviewerRoleID,
		"title":            reviewTitle,
		"body":             "Temporary review created by the Hecate sidecar collaboration smoke.",
		"verdict":          "approved",
		"risk":             "low",
	})
	if !appendStep(recordReview) {
		return response
	}
	listReviews := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "list_reviews_after_record", "reviews.list", true, map[string]string{"project_id": projectID, "work_item_id": response.WorkItemID})
	if !appendStep(listReviews) {
		return response
	}
	review, ok := projectCairnlineSidecarReviewByTitle(listReviews.StructuredReviews, reviewTitle)
	if !ok || strings.TrimSpace(review.ID) == "" || review.AssignmentID != response.AssignmentID || review.ReviewerRoleID != response.ReviewerRoleID || review.Verdict != "approved" {
		return fail("sidecar_collaboration_review_list_verification_failed", "Cairnline sidecar reviews.list did not return the expected temporary review.")
	}
	getReview := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "get_review", "reviews.get", true, map[string]string{"project_id": projectID, "work_item_id": response.WorkItemID, "review_id": review.ID})
	if !appendStep(getReview) {
		return response
	}
	if getReview.StructuredReview.ID != review.ID || getReview.StructuredReview.Title != reviewTitle || getReview.StructuredReview.Verdict != "approved" {
		return fail("sidecar_collaboration_review_get_verification_failed", "Cairnline sidecar reviews.get did not return the expected temporary review.")
	}
	response.CreatedReview = getReview.StructuredReview

	createHandoff := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "create_handoff", "handoffs.create", false, map[string]any{
		"project_id":              projectID,
		"work_item_id":            response.WorkItemID,
		"source_assignment_id":    response.AssignmentID,
		"from_role_id":            response.AuthorRoleID,
		"to_role_id":              response.ReviewerRoleID,
		"title":                   handoffTitle,
		"body":                    "Temporary handoff created by the Hecate sidecar collaboration smoke.",
		"recommended_next_action": "Delete this smoke project after verification.",
		"linked_artifact_ids":     []string{response.CreatedArtifact.ID},
		"context_refs":            []string{response.CreatedEvidence.ID, response.CreatedReview.ID},
		"status":                  "open",
		"provenance_kind":         "hecate_sidecar_smoke",
		"trust_label":             "diagnostic",
	})
	if !appendStep(createHandoff) {
		return response
	}
	listHandoffs := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "list_handoffs_after_create", "handoffs.list", true, map[string]string{"project_id": projectID, "work_item_id": response.WorkItemID})
	if !appendStep(listHandoffs) {
		return response
	}
	handoff, ok := projectCairnlineSidecarHandoffByTitle(listHandoffs.StructuredHandoffs, handoffTitle)
	if !ok || strings.TrimSpace(handoff.ID) == "" || handoff.SourceAssignmentID != response.AssignmentID || handoff.FromRoleID != response.AuthorRoleID || handoff.ToRoleID != response.ReviewerRoleID || handoff.Status != "open" {
		return fail("sidecar_collaboration_handoff_list_verification_failed", "Cairnline sidecar handoffs.list did not return the expected temporary handoff.")
	}
	getHandoff := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "get_handoff", "handoffs.get", true, map[string]string{"project_id": projectID, "work_item_id": response.WorkItemID, "handoff_id": handoff.ID})
	if !appendStep(getHandoff) {
		return response
	}
	if getHandoff.StructuredHandoff.ID != handoff.ID || getHandoff.StructuredHandoff.Title != handoffTitle || getHandoff.StructuredHandoff.Status != "open" {
		return fail("sidecar_collaboration_handoff_get_verification_failed", "Cairnline sidecar handoffs.get did not return the expected temporary handoff.")
	}
	response.CreatedHandoff = getHandoff.StructuredHandoff

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
		response.Status = "sidecar_collaboration_ready"
		response.Detail = "Hecate created and verified temporary standalone Cairnline artifact, evidence, review, and handoff metadata through the persistent sidecar client, then deleted the temporary project. Hecate-native Projects stores were not mutated."
		response.setSidecarCacheStats(cache.Stats())
		return response
	}
	if !appendStep(verifyDelete) {
		return response
	}
	return fail("sidecar_collaboration_project_delete_verification_failed", "Cairnline sidecar projects.delete succeeded, but projects.get still returned the temporary collaboration project.")
}

func (h *Handler) projectCairnlineSidecarMemorySmoke(ctx context.Context, req ProjectCairnlineSidecarMemoryRequest) ProjectCairnlineSidecarMemoryResponse {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, dbPath, timeout := h.projectCairnlineSidecarMCPConfig()
	projectName := strings.TrimSpace(req.ProjectName)
	if projectName == "" {
		projectName = "Hecate sidecar memory smoke " + time.Now().UTC().Format("20060102T150405.000000000Z")
	}
	const (
		memoryTitle          = "Sidecar memory entry"
		updatedMemoryTitle   = "Sidecar memory entry updated"
		candidateTitle       = "Sidecar memory candidate"
		promotedMemoryTitle  = "Sidecar promoted memory"
		rejectCandidateTitle = "Sidecar rejected memory candidate"
	)
	response := ProjectCairnlineSidecarMemoryResponse{
		Ready:                 false,
		Status:                "sidecar_memory_not_run",
		Detail:                "Cairnline sidecar memory smoke has not run.",
		Command:               cfg.Command,
		Args:                  append([]string(nil), cfg.Args...),
		DatabasePath:          dbPath,
		ProbeTimeoutMS:        timeout.Milliseconds(),
		PersistentClient:      true,
		ClientCacheConfigured: h != nil,
		ConfirmedMutation:     req.ConfirmMutation,
		ProjectName:           projectName,
	}
	if h == nil {
		response.Status = "sidecar_memory_failed"
		response.Detail = "Cairnline sidecar memory smoke requires an API handler."
		return response
	}
	if h.projectCairnlineConnectorMode() != "sidecar" {
		response.Warnings = append(response.Warnings, "HECATE_PROJECTS_CAIRNLINE_CONNECTOR is not sidecar; this memory smoke does not affect live Projects routing.")
	}
	if dbPath != "" && len(h.config.ProjectsCairnlineSidecarArgs()) > 0 {
		response.Warnings = append(response.Warnings, "HECATE_PROJECTS_CAIRNLINE_SIDECAR_ARGS is set, so HECATE_PROJECTS_CAIRNLINE_SIDECAR_DB is reported but not appended automatically.")
	}
	if !req.ConfirmMutation {
		response.Status = "sidecar_memory_confirmation_required"
		response.Detail = "Set confirm_mutation=true to let Hecate create, verify, promote, reject, delete, and clean up temporary memory records in the standalone Cairnline sidecar. Hecate-native Projects stores are not mutated."
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
				response.Warnings = append(response.Warnings, "Hecate deleted and verified removal of the temporary standalone Cairnline memory project after the memory smoke failed; inspect the reported steps before retrying.")
			} else {
				response.Warnings = append(response.Warnings, "Hecate deleted the temporary standalone Cairnline memory project after the memory smoke failed, but removal could not be verified; inspect the reported steps before retrying.")
			}
			response.Steps = append(response.Steps, verifyCleanupStep)
			return
		}
		response.Warnings = append(response.Warnings, "Hecate tried to delete the temporary standalone Cairnline memory project after the memory smoke failed, but cleanup did not succeed; inspect the standalone Cairnline sidecar before retrying.")
	}
	fail := func(status, detail string) ProjectCairnlineSidecarMemoryResponse {
		response.Status = status
		response.Detail = detail
		cleanup()
		response.setSidecarCacheStats(cache.Stats())
		return response
	}
	appendStep := func(step ProjectCairnlineSidecarWriteStep) bool {
		response.Steps = append(response.Steps, step)
		if step.Status == "tool_failed" {
			response.Status = "sidecar_memory_tool_failed"
			response.Detail = "Cairnline sidecar " + step.Tool + " returned a tool-level error. Review the step output before retrying."
			cleanup()
			response.setSidecarCacheStats(cache.Stats())
			return false
		}
		if step.Status == "failed" {
			response.Status = "sidecar_memory_failed"
			response.Detail = firstNonEmpty(step.ToolText, "Cairnline sidecar "+step.Tool+" failed.")
			cleanup()
			response.setSidecarCacheStats(cache.Stats())
			return false
		}
		return true
	}

	createProject := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "create_project", "projects.create", false, map[string]string{
		"name":        projectName,
		"description": "Temporary Hecate sidecar memory smoke project. It should be deleted by the same diagnostic run.",
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
		return fail("sidecar_memory_created_project_not_listed", "Cairnline sidecar projects.create succeeded, but projects.list did not return the temporary memory project by name.")
	}
	projectID = strings.TrimSpace(project.ID)
	response.SelectedProjectID = projectID

	createMemory := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "create_memory_entry", "memory_entries.create", false, map[string]string{
		"project_id":  projectID,
		"title":       memoryTitle,
		"body":        "Temporary accepted memory created by the Hecate sidecar memory smoke.",
		"trust_label": "operator_memory",
		"source_kind": "operator",
		"source_id":   "hecate-sidecar-memory-smoke",
	})
	if !appendStep(createMemory) {
		return response
	}
	listMemory := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "list_memory_entries_after_create", "memory_entries.list", true, map[string]any{"project_id": projectID, "include_disabled": true})
	if !appendStep(listMemory) {
		return response
	}
	memoryEntry, ok := projectCairnlineSidecarMemoryEntryByTitle(listMemory.StructuredMemoryEntries, memoryTitle)
	if !ok || strings.TrimSpace(memoryEntry.ID) == "" || !memoryEntry.Enabled || memoryEntry.TrustLabel != "operator_memory" {
		return fail("sidecar_memory_entry_create_verification_failed", "Cairnline sidecar memory_entries.list did not return the expected temporary accepted memory entry.")
	}
	response.MemoryEntryID = strings.TrimSpace(memoryEntry.ID)
	response.CreatedMemoryEntry = memoryEntry

	getMemory := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "get_memory_entry", "memory_entries.get", true, map[string]string{"project_id": projectID, "memory_id": response.MemoryEntryID})
	if !appendStep(getMemory) {
		return response
	}
	if getMemory.StructuredMemoryEntry.ID != response.MemoryEntryID || getMemory.StructuredMemoryEntry.Title != memoryTitle {
		return fail("sidecar_memory_entry_get_verification_failed", "Cairnline sidecar memory_entries.get did not return the expected temporary accepted memory entry.")
	}

	updateMemory := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "update_memory_entry", "memory_entries.update", false, map[string]any{
		"project_id": projectID,
		"memory_id":  response.MemoryEntryID,
		"title":      updatedMemoryTitle,
		"body":       "Temporary accepted memory updated by the Hecate sidecar memory smoke.",
		"enabled":    true,
	})
	if !appendStep(updateMemory) {
		return response
	}
	getUpdatedMemory := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "get_updated_memory_entry", "memory_entries.get", true, map[string]string{"project_id": projectID, "memory_id": response.MemoryEntryID})
	if !appendStep(getUpdatedMemory) {
		return response
	}
	if getUpdatedMemory.StructuredMemoryEntry.ID != response.MemoryEntryID || getUpdatedMemory.StructuredMemoryEntry.Title != updatedMemoryTitle || !getUpdatedMemory.StructuredMemoryEntry.Enabled {
		return fail("sidecar_memory_entry_update_verification_failed", "Cairnline sidecar memory_entries.get did not return the expected updated accepted memory entry.")
	}
	response.UpdatedMemoryEntry = getUpdatedMemory.StructuredMemoryEntry

	createCandidate := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "create_memory_candidate", "memory_candidates.create", false, map[string]any{
		"project_id":            projectID,
		"title":                 candidateTitle,
		"body":                  "Temporary memory candidate created by the Hecate sidecar memory smoke.",
		"suggested_kind":        "diagnostic_note",
		"suggested_trust_label": "generated_summary",
		"suggested_source_kind": "generated",
		"suggested_source_id":   response.MemoryEntryID,
		"source_refs":           []map[string]string{{"kind": "memory_entry", "id": response.MemoryEntryID, "title": updatedMemoryTitle}},
	})
	if !appendStep(createCandidate) {
		return response
	}
	listCandidates := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "list_memory_candidates_after_create", "memory_candidates.list", true, map[string]string{"project_id": projectID})
	if !appendStep(listCandidates) {
		return response
	}
	candidate, ok := projectCairnlineSidecarMemoryCandidateByTitle(listCandidates.StructuredMemoryCandidates, candidateTitle)
	if !ok || strings.TrimSpace(candidate.ID) == "" || candidate.Status != "pending" || len(candidate.SourceRefs) != 1 {
		return fail("sidecar_memory_candidate_create_verification_failed", "Cairnline sidecar memory_candidates.list did not return the expected pending memory candidate with source provenance.")
	}
	response.PromoteCandidateID = strings.TrimSpace(candidate.ID)
	response.CreatedMemoryCandidate = candidate

	getCandidate := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "get_memory_candidate", "memory_candidates.get", true, map[string]string{"project_id": projectID, "candidate_id": response.PromoteCandidateID})
	if !appendStep(getCandidate) {
		return response
	}
	if getCandidate.StructuredMemoryCandidate.ID != response.PromoteCandidateID || getCandidate.StructuredMemoryCandidate.Status != "pending" {
		return fail("sidecar_memory_candidate_get_verification_failed", "Cairnline sidecar memory_candidates.get did not return the expected pending memory candidate.")
	}

	promoteCandidate := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "promote_memory_candidate", "memory_candidates.promote", false, map[string]string{
		"project_id":   projectID,
		"candidate_id": response.PromoteCandidateID,
		"title":        promotedMemoryTitle,
		"trust_label":  "operator_memory",
		"source_kind":  "operator",
		"source_id":    response.MemoryEntryID,
	})
	if !appendStep(promoteCandidate) {
		return response
	}
	if promoteCandidate.StructuredMemoryCandidate.ID != response.PromoteCandidateID || promoteCandidate.StructuredMemoryCandidate.Status != "promoted" || strings.TrimSpace(promoteCandidate.StructuredMemoryCandidate.PromotedMemoryID) == "" {
		return fail("sidecar_memory_candidate_promote_verification_failed", "Cairnline sidecar memory_candidates.promote did not return the expected promoted candidate with promoted memory id.")
	}
	response.PromotedMemoryCandidate = promoteCandidate.StructuredMemoryCandidate
	response.PromotedMemoryEntryID = strings.TrimSpace(promoteCandidate.StructuredMemoryCandidate.PromotedMemoryID)

	getPromotedMemory := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "get_promoted_memory_entry", "memory_entries.get", true, map[string]string{"project_id": projectID, "memory_id": response.PromotedMemoryEntryID})
	if !appendStep(getPromotedMemory) {
		return response
	}
	if getPromotedMemory.StructuredMemoryEntry.ID != response.PromotedMemoryEntryID || getPromotedMemory.StructuredMemoryEntry.Title != promotedMemoryTitle || getPromotedMemory.StructuredMemoryEntry.TrustLabel != "operator_memory" {
		return fail("sidecar_memory_candidate_promoted_entry_verification_failed", "Cairnline sidecar memory_entries.get did not return the expected accepted memory entry created by promotion.")
	}
	response.PromotedMemoryEntry = getPromotedMemory.StructuredMemoryEntry

	createRejectCandidate := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "create_reject_memory_candidate", "memory_candidates.create", false, map[string]string{
		"project_id": projectID,
		"title":      rejectCandidateTitle,
		"body":       "Temporary memory candidate that should be rejected by the Hecate sidecar memory smoke.",
	})
	if !appendStep(createRejectCandidate) {
		return response
	}
	listRejectCandidates := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "list_reject_memory_candidates", "memory_candidates.list", true, map[string]string{"project_id": projectID})
	if !appendStep(listRejectCandidates) {
		return response
	}
	rejectCandidate, ok := projectCairnlineSidecarMemoryCandidateByTitle(listRejectCandidates.StructuredMemoryCandidates, rejectCandidateTitle)
	if !ok || strings.TrimSpace(rejectCandidate.ID) == "" || rejectCandidate.Status != "pending" {
		return fail("sidecar_memory_reject_candidate_create_verification_failed", "Cairnline sidecar memory_candidates.list did not return the expected pending candidate to reject.")
	}
	response.RejectCandidateID = strings.TrimSpace(rejectCandidate.ID)

	rejectStep := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "reject_memory_candidate", "memory_candidates.reject", false, map[string]string{
		"project_id":   projectID,
		"candidate_id": response.RejectCandidateID,
		"reason":       "Temporary smoke candidate is not durable project memory.",
	})
	if !appendStep(rejectStep) {
		return response
	}
	if rejectStep.StructuredMemoryCandidate.ID != response.RejectCandidateID || rejectStep.StructuredMemoryCandidate.Status != "rejected" || rejectStep.StructuredMemoryCandidate.StatusReason == "" {
		return fail("sidecar_memory_candidate_reject_verification_failed", "Cairnline sidecar memory_candidates.reject did not return the expected rejected candidate.")
	}
	response.RejectedMemoryCandidate = rejectStep.StructuredMemoryCandidate

	deleteRejectCandidate := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "delete_rejected_memory_candidate", "memory_candidates.delete", false, map[string]string{"project_id": projectID, "candidate_id": response.RejectCandidateID})
	if !appendStep(deleteRejectCandidate) {
		return response
	}
	listAfterRejectDelete := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "list_memory_candidates_after_reject_delete", "memory_candidates.list", true, map[string]any{"project_id": projectID, "include_resolved": true})
	if !appendStep(listAfterRejectDelete) {
		return response
	}
	if _, ok := projectCairnlineSidecarMemoryCandidateByID(listAfterRejectDelete.StructuredMemoryCandidates, response.RejectCandidateID); ok {
		return fail("sidecar_memory_candidate_delete_verification_failed", "Cairnline sidecar memory_candidates.delete succeeded, but memory_candidates.list still returned the rejected temporary candidate.")
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
		response.Status = "sidecar_memory_ready"
		response.Detail = "Hecate created and verified temporary standalone Cairnline accepted memory, promoted one memory candidate, rejected and deleted another, then deleted the temporary project. Hecate-native Projects stores were not mutated."
		response.setSidecarCacheStats(cache.Stats())
		return response
	}
	if !appendStep(verifyDelete) {
		return response
	}
	return fail("sidecar_memory_project_delete_verification_failed", "Cairnline sidecar projects.delete succeeded, but projects.get still returned the temporary memory project.")
}

func (h *Handler) projectCairnlineSidecarAssistantSmoke(ctx context.Context, req ProjectCairnlineSidecarAssistantRequest) ProjectCairnlineSidecarAssistantResponse {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, dbPath, timeout := h.projectCairnlineSidecarMCPConfig()
	projectName := strings.TrimSpace(req.ProjectName)
	if projectName == "" {
		projectName = "Hecate sidecar assistant smoke " + time.Now().UTC().Format("20060102T150405.000000000Z")
	}
	const (
		roleName  = "Sidecar assistant operator"
		workTitle = "Sidecar assistant proposed work"
	)
	response := ProjectCairnlineSidecarAssistantResponse{
		Ready:                 false,
		Status:                "sidecar_assistant_not_run",
		Detail:                "Cairnline sidecar assistant smoke has not run.",
		Command:               cfg.Command,
		Args:                  append([]string(nil), cfg.Args...),
		DatabasePath:          dbPath,
		ProbeTimeoutMS:        timeout.Milliseconds(),
		PersistentClient:      true,
		ClientCacheConfigured: h != nil,
		ConfirmedMutation:     req.ConfirmMutation,
		ProjectName:           projectName,
	}
	if h == nil {
		response.Status = "sidecar_assistant_failed"
		response.Detail = "Cairnline sidecar assistant smoke requires an API handler."
		return response
	}
	if h.projectCairnlineConnectorMode() != "sidecar" {
		response.Warnings = append(response.Warnings, "HECATE_PROJECTS_CAIRNLINE_CONNECTOR is not sidecar; this assistant smoke does not affect live Projects routing.")
	}
	if dbPath != "" && len(h.config.ProjectsCairnlineSidecarArgs()) > 0 {
		response.Warnings = append(response.Warnings, "HECATE_PROJECTS_CAIRNLINE_SIDECAR_ARGS is set, so HECATE_PROJECTS_CAIRNLINE_SIDECAR_DB is reported but not appended automatically.")
	}
	if !req.ConfirmMutation {
		response.Status = "sidecar_assistant_confirmation_required"
		response.Detail = "Set confirm_mutation=true to let Hecate create, verify, apply, and clean up a temporary Project Assistant proposal in the standalone Cairnline sidecar. Hecate-native Projects stores are not mutated."
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
				response.Warnings = append(response.Warnings, "Hecate deleted and verified removal of the temporary standalone Cairnline assistant project after the assistant smoke failed; inspect the reported steps before retrying.")
			} else {
				response.Warnings = append(response.Warnings, "Hecate deleted the temporary standalone Cairnline assistant project after the assistant smoke failed, but removal could not be verified; inspect the reported steps before retrying.")
			}
			response.Steps = append(response.Steps, verifyCleanupStep)
			return
		}
		response.Warnings = append(response.Warnings, "Hecate tried to delete the temporary standalone Cairnline assistant project after the assistant smoke failed, but cleanup did not succeed; inspect the standalone Cairnline sidecar before retrying.")
	}
	fail := func(status, detail string) ProjectCairnlineSidecarAssistantResponse {
		response.Status = status
		response.Detail = detail
		cleanup()
		response.setSidecarCacheStats(cache.Stats())
		return response
	}
	appendStep := func(step ProjectCairnlineSidecarWriteStep) bool {
		response.Steps = append(response.Steps, step)
		if step.Status == "tool_failed" {
			response.Status = "sidecar_assistant_tool_failed"
			response.Detail = "Cairnline sidecar " + step.Tool + " returned a tool-level error. Review the step output before retrying."
			cleanup()
			response.setSidecarCacheStats(cache.Stats())
			return false
		}
		if step.Status == "failed" {
			response.Status = "sidecar_assistant_failed"
			response.Detail = firstNonEmpty(step.ToolText, "Cairnline sidecar "+step.Tool+" failed.")
			cleanup()
			response.setSidecarCacheStats(cache.Stats())
			return false
		}
		return true
	}

	createProject := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "create_project", "projects.create", false, map[string]string{
		"name":        projectName,
		"description": "Temporary Hecate sidecar assistant smoke project. It should be deleted by the same diagnostic run.",
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
		return fail("sidecar_assistant_created_project_not_listed", "Cairnline sidecar projects.create succeeded, but projects.list did not return the temporary assistant project by name.")
	}
	projectID = strings.TrimSpace(project.ID)
	response.SelectedProjectID = projectID
	response.ProposalID = "prop_" + projectID + "_assistant_smoke"
	response.RoleID = "role_" + projectID + "_assistant"
	response.WorkItemID = "work_" + projectID + "_assistant"
	response.AssignmentID = "asgn_" + projectID + "_assistant"

	proposal := map[string]any{
		"id":                    response.ProposalID,
		"project_id":            projectID,
		"title":                 "Queue sidecar assistant assignment",
		"summary":               "Temporary Project Assistant proposal created by the Hecate sidecar assistant smoke.",
		"source":                "assistant",
		"requires_confirmation": true,
		"warnings":              []string{"Temporary diagnostic proposal; it should be applied and cleaned up by the same smoke run."},
		"actions": []map[string]any{
			{
				"kind": "create_role",
				"role": map[string]any{
					"id":                     response.RoleID,
					"project_id":             projectID,
					"name":                   roleName,
					"description":            "Temporary role from the sidecar assistant smoke.",
					"default_execution_mode": "mcp_pull",
				},
			},
			{
				"kind": "create_work_item",
				"work_item": map[string]any{
					"id":            response.WorkItemID,
					"project_id":    projectID,
					"title":         workTitle,
					"brief":         "Temporary work item from the sidecar assistant smoke.",
					"owner_role_id": response.RoleID,
				},
			},
			{
				"kind": "create_assignment",
				"assignment": map[string]any{
					"id":             response.AssignmentID,
					"project_id":     projectID,
					"work_item_id":   response.WorkItemID,
					"role_id":        response.RoleID,
					"execution_mode": "mcp_pull",
				},
			},
		},
	}
	proposeStep := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "create_assistant_proposal", "assistant.propose", false, proposal)
	if !appendStep(proposeStep) {
		return response
	}
	if proposeStep.StructuredAssistantProposal.ID != response.ProposalID || proposeStep.StructuredAssistantProposal.Status != "proposed" || len(proposeStep.StructuredAssistantProposal.Proposal.Actions) != 3 {
		return fail("sidecar_assistant_proposal_create_verification_failed", "Cairnline sidecar assistant.propose did not return the expected proposed assistant record with three actions.")
	}
	response.CreatedProposal = proposeStep.StructuredAssistantProposal

	listProposals := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "list_assistant_proposals_after_create", "assistant.proposals.list", true, map[string]string{"project_id": projectID})
	if !appendStep(listProposals) {
		return response
	}
	if _, ok := projectCairnlineSidecarAssistantProposalByID(listProposals.StructuredAssistantProposals, response.ProposalID); !ok {
		return fail("sidecar_assistant_proposal_list_verification_failed", "Cairnline sidecar assistant.proposals.list did not return the expected proposed assistant record.")
	}

	getProposal := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "get_assistant_proposal", "assistant.proposals.get", true, map[string]string{"id": response.ProposalID})
	if !appendStep(getProposal) {
		return response
	}
	if getProposal.StructuredAssistantProposal.ID != response.ProposalID || getProposal.StructuredAssistantProposal.ProjectID != projectID {
		return fail("sidecar_assistant_proposal_get_verification_failed", "Cairnline sidecar assistant.proposals.get did not return the expected proposed assistant record.")
	}

	unconfirmedApplyStep := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "check_unconfirmed_assistant_apply", "assistant.apply", false, map[string]any{
		"proposal_id": response.ProposalID,
		"confirm":     false,
	})
	response.Steps = append(response.Steps, unconfirmedApplyStep)
	if unconfirmedApplyStep.Status != "tool_failed" || !unconfirmedApplyStep.ToolIsError || !unconfirmedApplyStep.StructuredReady || unconfirmedApplyStep.StructuredAssistantApplyResult.ProposalID != response.ProposalID || unconfirmedApplyStep.StructuredAssistantApplyResult.Status != "needs_confirmation" || unconfirmedApplyStep.StructuredAssistantApplyResult.Applied || unconfirmedApplyStep.StructuredAssistantApplyResult.Confirmed {
		return fail("sidecar_assistant_unconfirmed_apply_verification_failed", "Cairnline sidecar assistant.apply without confirm=true did not return the expected typed needs_confirmation result before side effects.")
	}
	response.UnconfirmedApplyResult = unconfirmedApplyStep.StructuredAssistantApplyResult

	getNeedsConfirmationProposal := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "get_needs_confirmation_assistant_proposal", "assistant.proposals.get", true, map[string]string{"id": response.ProposalID})
	if !appendStep(getNeedsConfirmationProposal) {
		return response
	}
	if getNeedsConfirmationProposal.StructuredAssistantProposal.ID != response.ProposalID || getNeedsConfirmationProposal.StructuredAssistantProposal.Status != "needs_confirmation" || getNeedsConfirmationProposal.StructuredAssistantProposal.LatestResult == nil || len(getNeedsConfirmationProposal.StructuredAssistantProposal.ApplyAttempts) == 0 {
		return fail("sidecar_assistant_unconfirmed_apply_ledger_verification_failed", "Cairnline sidecar assistant.proposals.get did not return the expected needs-confirmation proposal ledger state after unconfirmed apply.")
	}

	applyStep := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "apply_assistant_proposal", "assistant.apply", false, map[string]any{
		"proposal_id": response.ProposalID,
		"confirm":     true,
	})
	if !appendStep(applyStep) {
		return response
	}
	if applyStep.StructuredAssistantApplyResult.ProposalID != response.ProposalID || applyStep.StructuredAssistantApplyResult.Status != "applied" || !applyStep.StructuredAssistantApplyResult.Applied || applyStep.StructuredAssistantApplyResult.AppliedActionCount != 3 {
		return fail("sidecar_assistant_apply_verification_failed", "Cairnline sidecar assistant.apply did not return the expected applied result for all three proposal actions.")
	}
	response.ApplyResult = applyStep.StructuredAssistantApplyResult

	getAppliedProposal := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "get_applied_assistant_proposal", "assistant.proposals.get", true, map[string]string{"id": response.ProposalID})
	if !appendStep(getAppliedProposal) {
		return response
	}
	if getAppliedProposal.StructuredAssistantProposal.ID != response.ProposalID || getAppliedProposal.StructuredAssistantProposal.Status != "applied" || getAppliedProposal.StructuredAssistantProposal.LatestResult == nil || len(getAppliedProposal.StructuredAssistantProposal.ApplyAttempts) == 0 {
		return fail("sidecar_assistant_applied_proposal_get_verification_failed", "Cairnline sidecar assistant.proposals.get did not return the applied proposal ledger state with latest result and apply attempt.")
	}
	response.AppliedProposal = getAppliedProposal.StructuredAssistantProposal

	listRoles := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "list_roles_after_apply", "roles.list", true, map[string]string{"project_id": projectID})
	if !appendStep(listRoles) {
		return response
	}
	role, ok := projectCairnlineSidecarRoleByName(listRoles.StructuredRoles, roleName)
	if !ok || role.ID != response.RoleID {
		return fail("sidecar_assistant_role_apply_verification_failed", "Cairnline sidecar roles.list did not return the role created by assistant.apply.")
	}

	listWork := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "list_work_items_after_apply", "work_items.list", true, map[string]string{"project_id": projectID})
	if !appendStep(listWork) {
		return response
	}
	work, ok := projectCairnlineSidecarWorkItemByTitle(listWork.StructuredWorkItems, workTitle)
	if !ok || work.ID != response.WorkItemID || work.OwnerRoleID != response.RoleID {
		return fail("sidecar_assistant_work_apply_verification_failed", "Cairnline sidecar work_items.list did not return the work item created by assistant.apply.")
	}

	listAssignments := h.callProjectCairnlineSidecarWriteTool(smokeCtx, cfg, cache, "list_assignments_after_apply", "assignments.list", true, map[string]string{"project_id": projectID, "work_item_id": response.WorkItemID})
	if !appendStep(listAssignments) {
		return response
	}
	assignment, ok := projectCairnlineSidecarAssignmentByWorkAndRole(listAssignments.StructuredAssignments, response.WorkItemID, response.RoleID)
	if !ok || assignment.ID != response.AssignmentID || assignment.ExecutionMode != "mcp_pull" {
		return fail("sidecar_assistant_assignment_apply_verification_failed", "Cairnline sidecar assignments.list did not return the queued MCP-pull assignment created by assistant.apply.")
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
		response.Status = "sidecar_assistant_ready"
		response.Detail = "Hecate created, listed, fetched, applied, and verified a temporary standalone Cairnline Project Assistant proposal, then deleted the temporary project. Hecate-native Projects stores were not mutated."
		response.setSidecarCacheStats(cache.Stats())
		return response
	}
	if !appendStep(verifyDelete) {
		return response
	}
	return fail("sidecar_assistant_project_delete_verification_failed", "Cairnline sidecar projects.delete succeeded, but projects.get still returned the temporary assistant project.")
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
		if tool == "assistant.apply" && len(result.Result.StructuredContent) > 0 {
			applyResult, structuredReady, structuredErr := projectCairnlineSidecarStructuredAssistantApplyResult(result.Result.StructuredContent)
			step.StructuredReady = structuredReady
			step.StructuredAssistantApplyResult = applyResult
			if structuredErr != nil {
				step.StructuredParseError = structuredErr.Error()
				step.Status = "failed"
				step.ToolText = "Cairnline sidecar assistant.apply returned error structuredContent that Hecate could not parse as assistant apply result: " + structuredErr.Error()
				return step
			}
		}
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
	case "roles.list":
		roles, structuredReady, structuredErr := projectCairnlineSidecarStructuredRoles(result.Result.StructuredContent)
		step.StructuredReady = structuredReady
		step.StructuredRoles = roles
		step.StructuredRoleCount = len(roles)
		if structuredErr != nil {
			step.StructuredParseError = structuredErr.Error()
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar roles.list returned structuredContent that Hecate could not parse as a role list: " + structuredErr.Error()
			return step
		}
		if !structuredReady {
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar roles.list did not return structuredContent; the work smoke needs typed roles to verify coordination mutations."
			return step
		}
	case "work_items.list":
		items, structuredReady, structuredErr := projectCairnlineSidecarStructuredWorkItems(result.Result.StructuredContent)
		step.StructuredReady = structuredReady
		step.StructuredWorkItems = items
		step.StructuredWorkItemCount = len(items)
		if structuredErr != nil {
			step.StructuredParseError = structuredErr.Error()
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar work_items.list returned structuredContent that Hecate could not parse as a work item list: " + structuredErr.Error()
			return step
		}
		if !structuredReady {
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar work_items.list did not return structuredContent; the work smoke needs typed work items to verify coordination mutations."
			return step
		}
	case "assignments.list":
		assignments, structuredReady, structuredErr := projectCairnlineSidecarStructuredAssignments(result.Result.StructuredContent)
		step.StructuredReady = structuredReady
		step.StructuredAssignments = assignments
		step.StructuredAssignmentCount = len(assignments)
		if structuredErr != nil {
			step.StructuredParseError = structuredErr.Error()
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar assignments.list returned structuredContent that Hecate could not parse as an assignment list: " + structuredErr.Error()
			return step
		}
		if !structuredReady {
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar assignments.list did not return structuredContent; the work smoke needs typed assignments to verify coordination mutations."
			return step
		}
	case "artifacts.list":
		artifacts, structuredReady, structuredErr := projectCairnlineSidecarStructuredArtifacts(result.Result.StructuredContent)
		step.StructuredReady = structuredReady
		step.StructuredArtifacts = artifacts
		step.StructuredArtifactCount = len(artifacts)
		if structuredErr != nil {
			step.StructuredParseError = structuredErr.Error()
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar artifacts.list returned structuredContent that Hecate could not parse as an artifact list: " + structuredErr.Error()
			return step
		}
		if !structuredReady {
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar artifacts.list did not return structuredContent; the collaboration smoke needs typed artifacts to verify collaboration mutations."
			return step
		}
	case "artifacts.create", "artifacts.get":
		artifact, structuredReady, structuredErr := projectCairnlineSidecarStructuredArtifact(result.Result.StructuredContent)
		step.StructuredReady = structuredReady
		step.StructuredArtifact = artifact
		if structuredErr != nil {
			step.StructuredParseError = structuredErr.Error()
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar " + tool + " returned structuredContent that Hecate could not parse as an artifact: " + structuredErr.Error()
			return step
		}
		if !structuredReady {
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar " + tool + " did not return structuredContent; the collaboration smoke needs typed artifact detail to verify collaboration mutations."
			return step
		}
	case "evidence.list":
		evidence, structuredReady, structuredErr := projectCairnlineSidecarStructuredEvidence(result.Result.StructuredContent)
		step.StructuredReady = structuredReady
		step.StructuredEvidence = evidence
		step.StructuredEvidenceCount = len(evidence)
		if structuredErr != nil {
			step.StructuredParseError = structuredErr.Error()
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar evidence.list returned structuredContent that Hecate could not parse as an evidence list: " + structuredErr.Error()
			return step
		}
		if !structuredReady {
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar evidence.list did not return structuredContent; the collaboration smoke needs typed evidence to verify collaboration mutations."
			return step
		}
	case "evidence.get":
		evidence, structuredReady, structuredErr := projectCairnlineSidecarStructuredEvidenceItem(result.Result.StructuredContent)
		step.StructuredReady = structuredReady
		step.StructuredEvidenceItem = evidence
		if structuredErr != nil {
			step.StructuredParseError = structuredErr.Error()
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar evidence.get returned structuredContent that Hecate could not parse as evidence: " + structuredErr.Error()
			return step
		}
		if !structuredReady {
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar evidence.get did not return structuredContent; the collaboration smoke needs typed evidence detail to verify collaboration mutations."
			return step
		}
	case "reviews.list":
		reviews, structuredReady, structuredErr := projectCairnlineSidecarStructuredReviews(result.Result.StructuredContent)
		step.StructuredReady = structuredReady
		step.StructuredReviews = reviews
		step.StructuredReviewCount = len(reviews)
		if structuredErr != nil {
			step.StructuredParseError = structuredErr.Error()
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar reviews.list returned structuredContent that Hecate could not parse as a review list: " + structuredErr.Error()
			return step
		}
		if !structuredReady {
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar reviews.list did not return structuredContent; the collaboration smoke needs typed reviews to verify collaboration mutations."
			return step
		}
	case "reviews.get":
		review, structuredReady, structuredErr := projectCairnlineSidecarStructuredReview(result.Result.StructuredContent)
		step.StructuredReady = structuredReady
		step.StructuredReview = review
		if structuredErr != nil {
			step.StructuredParseError = structuredErr.Error()
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar reviews.get returned structuredContent that Hecate could not parse as a review: " + structuredErr.Error()
			return step
		}
		if !structuredReady {
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar reviews.get did not return structuredContent; the collaboration smoke needs typed review detail to verify collaboration mutations."
			return step
		}
	case "handoffs.list":
		handoffs, structuredReady, structuredErr := projectCairnlineSidecarStructuredHandoffs(result.Result.StructuredContent)
		step.StructuredReady = structuredReady
		step.StructuredHandoffs = handoffs
		step.StructuredHandoffCount = len(handoffs)
		if structuredErr != nil {
			step.StructuredParseError = structuredErr.Error()
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar handoffs.list returned structuredContent that Hecate could not parse as a handoff list: " + structuredErr.Error()
			return step
		}
		if !structuredReady {
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar handoffs.list did not return structuredContent; the collaboration smoke needs typed handoffs to verify collaboration mutations."
			return step
		}
	case "handoffs.get":
		handoff, structuredReady, structuredErr := projectCairnlineSidecarStructuredHandoff(result.Result.StructuredContent)
		step.StructuredReady = structuredReady
		step.StructuredHandoff = handoff
		if structuredErr != nil {
			step.StructuredParseError = structuredErr.Error()
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar handoffs.get returned structuredContent that Hecate could not parse as a handoff: " + structuredErr.Error()
			return step
		}
		if !structuredReady {
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar handoffs.get did not return structuredContent; the collaboration smoke needs typed handoff detail to verify collaboration mutations."
			return step
		}
	case "memory_entries.list":
		entries, structuredReady, structuredErr := projectCairnlineSidecarStructuredMemoryEntries(result.Result.StructuredContent)
		step.StructuredReady = structuredReady
		step.StructuredMemoryEntries = entries
		step.StructuredMemoryEntryCount = len(entries)
		if structuredErr != nil {
			step.StructuredParseError = structuredErr.Error()
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar memory_entries.list returned structuredContent that Hecate could not parse as memory entries: " + structuredErr.Error()
			return step
		}
		if !structuredReady {
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar memory_entries.list did not return structuredContent; the memory smoke needs typed memory entries to verify memory mutations."
			return step
		}
	case "memory_entries.get":
		entry, structuredReady, structuredErr := projectCairnlineSidecarStructuredMemoryEntry(result.Result.StructuredContent)
		step.StructuredReady = structuredReady
		step.StructuredMemoryEntry = entry
		if structuredErr != nil {
			step.StructuredParseError = structuredErr.Error()
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar memory_entries.get returned structuredContent that Hecate could not parse as memory: " + structuredErr.Error()
			return step
		}
		if !structuredReady {
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar memory_entries.get did not return structuredContent; the memory smoke needs typed memory detail to verify memory mutations."
			return step
		}
	case "memory_candidates.list":
		candidates, structuredReady, structuredErr := projectCairnlineSidecarStructuredMemoryCandidates(result.Result.StructuredContent)
		step.StructuredReady = structuredReady
		step.StructuredMemoryCandidates = candidates
		step.StructuredMemoryCandidateCount = len(candidates)
		if structuredErr != nil {
			step.StructuredParseError = structuredErr.Error()
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar memory_candidates.list returned structuredContent that Hecate could not parse as memory candidates: " + structuredErr.Error()
			return step
		}
		if !structuredReady {
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar memory_candidates.list did not return structuredContent; the memory smoke needs typed memory candidates to verify candidate mutations."
			return step
		}
	case "memory_candidates.get", "memory_candidates.promote", "memory_candidates.reject":
		candidate, structuredReady, structuredErr := projectCairnlineSidecarStructuredMemoryCandidate(result.Result.StructuredContent)
		step.StructuredReady = structuredReady
		step.StructuredMemoryCandidate = candidate
		if structuredErr != nil {
			step.StructuredParseError = structuredErr.Error()
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar " + tool + " returned structuredContent that Hecate could not parse as memory candidate: " + structuredErr.Error()
			return step
		}
		if !structuredReady {
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar " + tool + " did not return structuredContent; the memory smoke needs typed memory candidate detail to verify candidate mutations."
			return step
		}
	case "assistant.propose", "assistant.proposals.get":
		proposal, structuredReady, structuredErr := projectCairnlineSidecarStructuredAssistantProposal(result.Result.StructuredContent)
		step.StructuredReady = structuredReady
		step.StructuredAssistantProposal = proposal
		if structuredErr != nil {
			step.StructuredParseError = structuredErr.Error()
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar " + tool + " returned structuredContent that Hecate could not parse as assistant proposal: " + structuredErr.Error()
			return step
		}
		if !structuredReady {
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar " + tool + " did not return structuredContent; the assistant smoke needs typed proposal detail to verify proposal ledger mutations."
			return step
		}
	case "assistant.proposals.list":
		proposals, structuredReady, structuredErr := projectCairnlineSidecarStructuredAssistantProposals(result.Result.StructuredContent)
		step.StructuredReady = structuredReady
		step.StructuredAssistantProposals = proposals
		step.StructuredAssistantProposalCount = len(proposals)
		if structuredErr != nil {
			step.StructuredParseError = structuredErr.Error()
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar assistant.proposals.list returned structuredContent that Hecate could not parse as assistant proposals: " + structuredErr.Error()
			return step
		}
		if !structuredReady {
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar assistant.proposals.list did not return structuredContent; the assistant smoke needs typed proposal records to verify proposal ledger mutations."
			return step
		}
	case "assistant.apply":
		applyResult, structuredReady, structuredErr := projectCairnlineSidecarStructuredAssistantApplyResult(result.Result.StructuredContent)
		step.StructuredReady = structuredReady
		step.StructuredAssistantApplyResult = applyResult
		if structuredErr != nil {
			step.StructuredParseError = structuredErr.Error()
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar assistant.apply returned structuredContent that Hecate could not parse as assistant apply result: " + structuredErr.Error()
			return step
		}
		if !structuredReady {
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar assistant.apply did not return structuredContent; the assistant smoke needs typed apply result to verify confirmed apply."
			return step
		}
	case "assignments.context":
		contextIDs, structuredReady, structuredErr := projectCairnlineSidecarStructuredAssignmentContextIDs(result.Result.StructuredContent)
		step.StructuredReady = structuredReady
		step.AssignmentContextIDs = contextIDs
		if structuredErr != nil {
			step.StructuredParseError = structuredErr.Error()
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar assignments.context returned structuredContent that Hecate could not parse as assignment context: " + structuredErr.Error()
			return step
		}
		if !structuredReady {
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar assignments.context did not return structuredContent; the work smoke needs typed context metadata."
			return step
		}
	case "assignments.launch_packet":
		packetIDs, _, warnings, structuredReady, structuredErr := projectCairnlineSidecarStructuredLaunchPacket(result.Result.StructuredContent)
		step.StructuredReady = structuredReady
		step.LaunchPacketIDs = packetIDs
		step.LaunchPacketWarnings = warnings
		if structuredErr != nil {
			step.StructuredParseError = structuredErr.Error()
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar assignments.launch_packet returned structuredContent that Hecate could not parse as a launch packet: " + structuredErr.Error()
			return step
		}
		if !structuredReady {
			step.Status = "failed"
			step.ToolText = "Cairnline sidecar assignments.launch_packet did not return structuredContent; the work smoke needs typed launch-packet metadata."
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

func projectCairnlineSidecarStructuredCoordinationCapabilities(raw json.RawMessage) (ProjectCairnlineCoordinationCapabilities, bool, error) {
	if len(raw) == 0 {
		return ProjectCairnlineCoordinationCapabilities{}, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ProjectCairnlineCoordinationCapabilities{}, false, nil
	}
	var capabilities ProjectCairnlineCoordinationCapabilities
	if err := json.Unmarshal(trimmed, &capabilities); err != nil {
		return ProjectCairnlineCoordinationCapabilities{}, false, err
	}
	return capabilities, true, nil
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

func projectCairnlineSidecarRoleByName(roles []ProjectCairnlineSidecarRoleItem, name string) (ProjectCairnlineSidecarRoleItem, bool) {
	name = strings.TrimSpace(name)
	for _, role := range roles {
		if strings.TrimSpace(role.Name) == name {
			return role, true
		}
	}
	return ProjectCairnlineSidecarRoleItem{}, false
}

func projectCairnlineSidecarWorkItemByTitle(items []ProjectCairnlineSidecarWorkItem, title string) (ProjectCairnlineSidecarWorkItem, bool) {
	title = strings.TrimSpace(title)
	for _, item := range items {
		if strings.TrimSpace(item.Title) == title {
			return item, true
		}
	}
	return ProjectCairnlineSidecarWorkItem{}, false
}

func projectCairnlineSidecarAssignmentByWorkAndRole(assignments []ProjectCairnlineSidecarAssignmentItem, workItemID, roleID string) (ProjectCairnlineSidecarAssignmentItem, bool) {
	workItemID = strings.TrimSpace(workItemID)
	roleID = strings.TrimSpace(roleID)
	for _, assignment := range assignments {
		if strings.TrimSpace(assignment.WorkItemID) == workItemID && strings.TrimSpace(assignment.RoleID) == roleID {
			return assignment, true
		}
	}
	return ProjectCairnlineSidecarAssignmentItem{}, false
}

func projectCairnlineSidecarArtifactByTitle(artifacts []ProjectCairnlineSidecarArtifactItem, title string) (ProjectCairnlineSidecarArtifactItem, bool) {
	title = strings.TrimSpace(title)
	for _, artifact := range artifacts {
		if strings.TrimSpace(artifact.Title) == title {
			return artifact, true
		}
	}
	return ProjectCairnlineSidecarArtifactItem{}, false
}

func projectCairnlineSidecarEvidenceByTitle(items []ProjectCairnlineSidecarEvidenceItem, title string) (ProjectCairnlineSidecarEvidenceItem, bool) {
	title = strings.TrimSpace(title)
	for _, item := range items {
		if strings.TrimSpace(item.Title) == title {
			return item, true
		}
	}
	return ProjectCairnlineSidecarEvidenceItem{}, false
}

func projectCairnlineSidecarReviewByTitle(reviews []ProjectCairnlineSidecarReviewItem, title string) (ProjectCairnlineSidecarReviewItem, bool) {
	title = strings.TrimSpace(title)
	for _, review := range reviews {
		if strings.TrimSpace(review.Title) == title {
			return review, true
		}
	}
	return ProjectCairnlineSidecarReviewItem{}, false
}

func projectCairnlineSidecarHandoffByTitle(handoffs []ProjectCairnlineSidecarHandoffItem, title string) (ProjectCairnlineSidecarHandoffItem, bool) {
	title = strings.TrimSpace(title)
	for _, handoff := range handoffs {
		if strings.TrimSpace(handoff.Title) == title {
			return handoff, true
		}
	}
	return ProjectCairnlineSidecarHandoffItem{}, false
}

func projectCairnlineSidecarMemoryEntryByTitle(entries []ProjectCairnlineSidecarMemoryEntryItem, title string) (ProjectCairnlineSidecarMemoryEntryItem, bool) {
	title = strings.TrimSpace(title)
	for _, entry := range entries {
		if strings.TrimSpace(entry.Title) == title {
			return entry, true
		}
	}
	return ProjectCairnlineSidecarMemoryEntryItem{}, false
}

func projectCairnlineSidecarMemoryCandidateByTitle(candidates []ProjectCairnlineSidecarMemoryCandidateItem, title string) (ProjectCairnlineSidecarMemoryCandidateItem, bool) {
	title = strings.TrimSpace(title)
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.Title) == title {
			return candidate, true
		}
	}
	return ProjectCairnlineSidecarMemoryCandidateItem{}, false
}

func projectCairnlineSidecarMemoryCandidateByID(candidates []ProjectCairnlineSidecarMemoryCandidateItem, id string) (ProjectCairnlineSidecarMemoryCandidateItem, bool) {
	id = strings.TrimSpace(id)
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.ID) == id {
			return candidate, true
		}
	}
	return ProjectCairnlineSidecarMemoryCandidateItem{}, false
}

func projectCairnlineSidecarAssistantProposalByID(proposals []ProjectCairnlineSidecarAssistantProposalRecordItem, id string) (ProjectCairnlineSidecarAssistantProposalRecordItem, bool) {
	id = strings.TrimSpace(id)
	for _, proposal := range proposals {
		if strings.TrimSpace(proposal.ID) == id {
			return proposal, true
		}
	}
	return ProjectCairnlineSidecarAssistantProposalRecordItem{}, false
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

func projectCairnlineSidecarStructuredRoles(raw json.RawMessage) ([]ProjectCairnlineSidecarRoleItem, bool, error) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, false, nil
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return []ProjectCairnlineSidecarRoleItem{}, true, nil
	}
	var roles []ProjectCairnlineSidecarRoleItem
	if err := json.Unmarshal(trimmed, &roles); err != nil {
		return nil, false, err
	}
	if roles == nil {
		roles = []ProjectCairnlineSidecarRoleItem{}
	}
	return roles, true, nil
}

func projectCairnlineSidecarStructuredWorkItems(raw json.RawMessage) ([]ProjectCairnlineSidecarWorkItem, bool, error) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, false, nil
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return []ProjectCairnlineSidecarWorkItem{}, true, nil
	}
	var items []ProjectCairnlineSidecarWorkItem
	if err := json.Unmarshal(trimmed, &items); err != nil {
		return nil, false, err
	}
	if items == nil {
		items = []ProjectCairnlineSidecarWorkItem{}
	}
	return items, true, nil
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

func projectCairnlineSidecarStructuredArtifacts(raw json.RawMessage) ([]ProjectCairnlineSidecarArtifactItem, bool, error) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, false, nil
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return []ProjectCairnlineSidecarArtifactItem{}, true, nil
	}
	var artifacts []ProjectCairnlineSidecarArtifactItem
	if err := json.Unmarshal(trimmed, &artifacts); err != nil {
		return nil, false, err
	}
	if artifacts == nil {
		artifacts = []ProjectCairnlineSidecarArtifactItem{}
	}
	return artifacts, true, nil
}

func projectCairnlineSidecarStructuredArtifact(raw json.RawMessage) (ProjectCairnlineSidecarArtifactItem, bool, error) {
	if len(raw) == 0 {
		return ProjectCairnlineSidecarArtifactItem{}, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ProjectCairnlineSidecarArtifactItem{}, false, nil
	}
	var artifact ProjectCairnlineSidecarArtifactItem
	if err := json.Unmarshal(trimmed, &artifact); err != nil {
		return ProjectCairnlineSidecarArtifactItem{}, false, err
	}
	return artifact, true, nil
}

func projectCairnlineSidecarStructuredEvidence(raw json.RawMessage) ([]ProjectCairnlineSidecarEvidenceItem, bool, error) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, false, nil
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return []ProjectCairnlineSidecarEvidenceItem{}, true, nil
	}
	var items []ProjectCairnlineSidecarEvidenceItem
	if err := json.Unmarshal(trimmed, &items); err != nil {
		return nil, false, err
	}
	if items == nil {
		items = []ProjectCairnlineSidecarEvidenceItem{}
	}
	return items, true, nil
}

func projectCairnlineSidecarStructuredEvidenceItem(raw json.RawMessage) (ProjectCairnlineSidecarEvidenceItem, bool, error) {
	if len(raw) == 0 {
		return ProjectCairnlineSidecarEvidenceItem{}, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ProjectCairnlineSidecarEvidenceItem{}, false, nil
	}
	var item ProjectCairnlineSidecarEvidenceItem
	if err := json.Unmarshal(trimmed, &item); err != nil {
		return ProjectCairnlineSidecarEvidenceItem{}, false, err
	}
	return item, true, nil
}

func projectCairnlineSidecarStructuredReviews(raw json.RawMessage) ([]ProjectCairnlineSidecarReviewItem, bool, error) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, false, nil
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return []ProjectCairnlineSidecarReviewItem{}, true, nil
	}
	var reviews []ProjectCairnlineSidecarReviewItem
	if err := json.Unmarshal(trimmed, &reviews); err != nil {
		return nil, false, err
	}
	if reviews == nil {
		reviews = []ProjectCairnlineSidecarReviewItem{}
	}
	return reviews, true, nil
}

func projectCairnlineSidecarStructuredReview(raw json.RawMessage) (ProjectCairnlineSidecarReviewItem, bool, error) {
	if len(raw) == 0 {
		return ProjectCairnlineSidecarReviewItem{}, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ProjectCairnlineSidecarReviewItem{}, false, nil
	}
	var review ProjectCairnlineSidecarReviewItem
	if err := json.Unmarshal(trimmed, &review); err != nil {
		return ProjectCairnlineSidecarReviewItem{}, false, err
	}
	return review, true, nil
}

func projectCairnlineSidecarStructuredHandoffs(raw json.RawMessage) ([]ProjectCairnlineSidecarHandoffItem, bool, error) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, false, nil
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return []ProjectCairnlineSidecarHandoffItem{}, true, nil
	}
	var handoffs []ProjectCairnlineSidecarHandoffItem
	if err := json.Unmarshal(trimmed, &handoffs); err != nil {
		return nil, false, err
	}
	if handoffs == nil {
		handoffs = []ProjectCairnlineSidecarHandoffItem{}
	}
	return handoffs, true, nil
}

func projectCairnlineSidecarStructuredHandoff(raw json.RawMessage) (ProjectCairnlineSidecarHandoffItem, bool, error) {
	if len(raw) == 0 {
		return ProjectCairnlineSidecarHandoffItem{}, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ProjectCairnlineSidecarHandoffItem{}, false, nil
	}
	var handoff ProjectCairnlineSidecarHandoffItem
	if err := json.Unmarshal(trimmed, &handoff); err != nil {
		return ProjectCairnlineSidecarHandoffItem{}, false, err
	}
	return handoff, true, nil
}

func projectCairnlineSidecarStructuredMemoryEntries(raw json.RawMessage) ([]ProjectCairnlineSidecarMemoryEntryItem, bool, error) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, false, nil
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return []ProjectCairnlineSidecarMemoryEntryItem{}, true, nil
	}
	var entries []ProjectCairnlineSidecarMemoryEntryItem
	if err := json.Unmarshal(trimmed, &entries); err != nil {
		return nil, false, err
	}
	if entries == nil {
		entries = []ProjectCairnlineSidecarMemoryEntryItem{}
	}
	return entries, true, nil
}

func projectCairnlineSidecarStructuredMemoryEntry(raw json.RawMessage) (ProjectCairnlineSidecarMemoryEntryItem, bool, error) {
	if len(raw) == 0 {
		return ProjectCairnlineSidecarMemoryEntryItem{}, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ProjectCairnlineSidecarMemoryEntryItem{}, false, nil
	}
	var entry ProjectCairnlineSidecarMemoryEntryItem
	if err := json.Unmarshal(trimmed, &entry); err != nil {
		return ProjectCairnlineSidecarMemoryEntryItem{}, false, err
	}
	return entry, true, nil
}

func projectCairnlineSidecarStructuredMemoryCandidates(raw json.RawMessage) ([]ProjectCairnlineSidecarMemoryCandidateItem, bool, error) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, false, nil
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return []ProjectCairnlineSidecarMemoryCandidateItem{}, true, nil
	}
	var candidates []ProjectCairnlineSidecarMemoryCandidateItem
	if err := json.Unmarshal(trimmed, &candidates); err != nil {
		return nil, false, err
	}
	if candidates == nil {
		candidates = []ProjectCairnlineSidecarMemoryCandidateItem{}
	}
	return candidates, true, nil
}

func projectCairnlineSidecarStructuredMemoryCandidate(raw json.RawMessage) (ProjectCairnlineSidecarMemoryCandidateItem, bool, error) {
	if len(raw) == 0 {
		return ProjectCairnlineSidecarMemoryCandidateItem{}, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ProjectCairnlineSidecarMemoryCandidateItem{}, false, nil
	}
	var candidate ProjectCairnlineSidecarMemoryCandidateItem
	if err := json.Unmarshal(trimmed, &candidate); err != nil {
		return ProjectCairnlineSidecarMemoryCandidateItem{}, false, err
	}
	return candidate, true, nil
}

func projectCairnlineSidecarStructuredAssistantProposals(raw json.RawMessage) ([]ProjectCairnlineSidecarAssistantProposalRecordItem, bool, error) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, false, nil
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return []ProjectCairnlineSidecarAssistantProposalRecordItem{}, true, nil
	}
	var proposals []ProjectCairnlineSidecarAssistantProposalRecordItem
	if err := json.Unmarshal(trimmed, &proposals); err != nil {
		return nil, false, err
	}
	if proposals == nil {
		proposals = []ProjectCairnlineSidecarAssistantProposalRecordItem{}
	}
	return proposals, true, nil
}

func projectCairnlineSidecarStructuredAssistantProposal(raw json.RawMessage) (ProjectCairnlineSidecarAssistantProposalRecordItem, bool, error) {
	if len(raw) == 0 {
		return ProjectCairnlineSidecarAssistantProposalRecordItem{}, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ProjectCairnlineSidecarAssistantProposalRecordItem{}, false, nil
	}
	var proposal ProjectCairnlineSidecarAssistantProposalRecordItem
	if err := json.Unmarshal(trimmed, &proposal); err != nil {
		return ProjectCairnlineSidecarAssistantProposalRecordItem{}, false, err
	}
	return proposal, true, nil
}

func projectCairnlineSidecarStructuredAssistantApplyResult(raw json.RawMessage) (ProjectCairnlineSidecarAssistantApplyResultItem, bool, error) {
	if len(raw) == 0 {
		return ProjectCairnlineSidecarAssistantApplyResultItem{}, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ProjectCairnlineSidecarAssistantApplyResultItem{}, false, nil
	}
	var result ProjectCairnlineSidecarAssistantApplyResultItem
	if err := json.Unmarshal(trimmed, &result); err != nil {
		return ProjectCairnlineSidecarAssistantApplyResultItem{}, false, err
	}
	return result, true, nil
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

func projectCairnlineSidecarStructuredAssignmentContext(raw json.RawMessage) (cairnline.AssignmentContext, bool, error) {
	if len(raw) == 0 {
		return cairnline.AssignmentContext{}, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return cairnline.AssignmentContext{}, false, nil
	}
	var context cairnline.AssignmentContext
	if err := json.Unmarshal(trimmed, &context); err != nil {
		return cairnline.AssignmentContext{}, false, err
	}
	return context, true, nil
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

func projectCairnlineSidecarStructuredProjectActivity(raw json.RawMessage) (cairnline.ProjectActivity, bool, error) {
	if len(raw) == 0 {
		return cairnline.ProjectActivity{}, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return cairnline.ProjectActivity{}, false, nil
	}
	var activity cairnline.ProjectActivity
	if err := json.Unmarshal(trimmed, &activity); err != nil {
		return cairnline.ProjectActivity{}, false, err
	}
	return activity, true, nil
}

func projectCairnlineSidecarStructuredProjectOperationsBrief(raw json.RawMessage) (cairnline.ProjectOperationsBrief, bool, error) {
	if len(raw) == 0 {
		return cairnline.ProjectOperationsBrief{}, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return cairnline.ProjectOperationsBrief{}, false, nil
	}
	var brief cairnline.ProjectOperationsBrief
	if err := json.Unmarshal(trimmed, &brief); err != nil {
		return cairnline.ProjectOperationsBrief{}, false, err
	}
	return brief, true, nil
}

func projectCairnlineSidecarStructuredAssignmentLaunchPacket(raw json.RawMessage) (cairnline.AssignmentLaunchPacket, bool, error) {
	if len(raw) == 0 {
		return cairnline.AssignmentLaunchPacket{}, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return cairnline.AssignmentLaunchPacket{}, false, nil
	}
	var packet cairnline.AssignmentLaunchPacket
	if err := json.Unmarshal(trimmed, &packet); err != nil {
		return cairnline.AssignmentLaunchPacket{}, false, err
	}
	return packet, true, nil
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
		LaunchPacketID: strings.TrimSpace(packet.ID),
		Kind:           strings.TrimSpace(packet.Kind),
		ProjectID:      firstNonEmpty(strings.TrimSpace(packet.Project.ID), strings.TrimSpace(packet.Assignment.ProjectID)),
		AssignmentID:   strings.TrimSpace(packet.Assignment.ID),
		WorkItemID:     strings.TrimSpace(packet.WorkItem.ID),
		RoleID:         strings.TrimSpace(packet.Role.ID),
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

func (r *ProjectCairnlineSidecarResourceResponse) setSidecarCacheStats(stats mcpclient.CacheStats) {
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

func (r *ProjectCairnlineSidecarWorkResponse) setSidecarCacheStats(stats mcpclient.CacheStats) {
	if r == nil {
		return
	}
	r.ClientCacheEntries = stats.Entries
	r.ClientCacheInUse = stats.InUse
	r.ClientCacheIdle = stats.Idle
}

func (r *ProjectCairnlineSidecarCollaborationResponse) setSidecarCacheStats(stats mcpclient.CacheStats) {
	if r == nil {
		return
	}
	r.ClientCacheEntries = stats.Entries
	r.ClientCacheInUse = stats.InUse
	r.ClientCacheIdle = stats.Idle
}

func (r *ProjectCairnlineSidecarMemoryResponse) setSidecarCacheStats(stats mcpclient.CacheStats) {
	if r == nil {
		return
	}
	r.ClientCacheEntries = stats.Entries
	r.ClientCacheInUse = stats.InUse
	r.ClientCacheIdle = stats.Idle
}

func (r *ProjectCairnlineSidecarAssistantResponse) setSidecarCacheStats(stats mcpclient.CacheStats) {
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

func renderMCPResourceContents(contents []mcp.ResourceContents) []ProjectCairnlineSidecarResourceContent {
	out := make([]ProjectCairnlineSidecarResourceContent, 0, len(contents))
	for _, content := range contents {
		out = append(out, ProjectCairnlineSidecarResourceContent{
			URI:      content.URI,
			MIMEType: content.MIMEType,
			Text:     content.Text,
			Blob:     content.Blob,
			Meta:     append(json.RawMessage(nil), content.Meta...),
		})
	}
	return out
}

func projectCairnlineSidecarProjectIDFromResourceURI(uri string) string {
	const prefix = "cairnline://projects/"
	rest := strings.TrimPrefix(strings.TrimSpace(uri), prefix)
	if rest == "" || rest == uri || strings.Contains(rest, "/") {
		return ""
	}
	return rest
}

func projectCairnlineSidecarProjectResourceID(contents []ProjectCairnlineSidecarResourceContent) (string, bool, error) {
	for _, content := range contents {
		text := strings.TrimSpace(content.Text)
		if text == "" {
			continue
		}
		var payload struct {
			Project struct {
				ID string `json:"id"`
			} `json:"project"`
		}
		if err := json.Unmarshal([]byte(text), &payload); err != nil {
			return "", false, err
		}
		id := strings.TrimSpace(payload.Project.ID)
		return id, id != "", nil
	}
	return "", false, nil
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

func projectCairnlineSidecarResourceTemplateURIs(templates []MCPProbeResourceTemplateDescriptor) []string {
	out := make([]string, 0, len(templates))
	seen := make(map[string]struct{}, len(templates))
	for _, template := range templates {
		uri := strings.TrimSpace(template.URITemplate)
		if uri == "" {
			continue
		}
		if _, ok := seen[uri]; ok {
			continue
		}
		seen[uri] = struct{}{}
		out = append(out, uri)
	}
	sort.Strings(out)
	return out
}

func projectCairnlineSidecarMissingResourceTemplates(templateURIs []string) []string {
	seen := make(map[string]struct{}, len(templateURIs))
	for _, uri := range templateURIs {
		seen[uri] = struct{}{}
	}
	var missing []string
	for _, uri := range projectCairnlineSidecarRequiredResourceTemplates {
		if _, ok := seen[uri]; !ok {
			missing = append(missing, uri)
		}
	}
	return missing
}
