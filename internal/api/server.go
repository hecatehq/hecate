package api

import (
	"log/slog"
	"net/http"
)

func NewServer(logger *slog.Logger, handler *Handler) http.Handler {
	mux := http.NewServeMux()

	registerHealthRoutes(mux, handler)
	registerProviderCompatibleRoutes(mux, handler)
	registerHecateRuntimeRoutes(mux, handler)
	registerHecateProjectRoutes(mux, handler)
	registerHecateAgentRoutes(mux, handler)
	registerHecateTaskRoutes(mux, handler)
	registerHecateOperationsRoutes(mux, handler)
	registerHecatePluginRoutes(mux, handler)
	registerHecateSettingsRoutes(mux, handler)
	registerAPINotFound(mux)

	// Embedded UI catch-all. Go 1.22+ mux selects the most specific pattern
	// first, so API routes continue to route to their handlers above.
	// Non-API paths fall through here and are served from ui/dist (or the
	// fallback page when the UI bundle isn't embedded).
	mux.Handle("GET /", staticUIHandler())

	return Chain(
		mux,
		TraceContextMiddleware,
		RequestIDMiddleware,
		RemoteRuntimeIdentityMiddleware(handler.config.Server.RemoteRuntimeMode, handler.config.Server.RemoteRuntimeSecret),
		OTelHTTPSpanMiddleware,
		SameOriginMiddlewareWithAllowedOrigins(handler.config.Server.AllowedOrigins),
		RemoteRuntimeLocalEndpointGuardMiddleware(handler.config.Server.RemoteRuntimeMode),
		RuntimeTokenMiddleware(handler.config.Server.RuntimeToken),
		InferenceTokenMiddleware(handler.config.Server.InferenceToken),
		LoggingMiddleware(logger),
		RecoveryMiddleware(logger),
	)
}

func registerHealthRoutes(mux *http.ServeMux, handler *Handler) {
	// Unversioned process liveness. This is intentionally separate from the
	// product API so load balancers and local scripts can keep using a tiny
	// stable probe.
	mux.HandleFunc("GET /healthz", handler.HandleHealth)
}

func registerProviderCompatibleRoutes(mux *http.ServeMux, handler *Handler) {
	// Provider-compatible ingress stays on /v1 because OpenAI/Anthropic-shaped
	// clients already expect these paths. These are protocol endpoints, not
	// Hecate product resources.
	mux.HandleFunc("GET /v1/models", handler.HandleModels)
	mux.HandleFunc("POST /v1/chat/completions", handler.HandleChatCompletions)
	mux.HandleFunc("POST /v1/messages", handler.HandleMessages)
}

func registerHecateRuntimeRoutes(mux *http.ServeMux, handler *Handler) {
	// Hecate-native runtime resources live under /hecate/v1 so they never
	// collide with provider protocol routes.
	mux.HandleFunc("GET /hecate/v1/whoami", handler.HandleSession)

	// Provider/model diagnostics power setup, routing, and Hecate Chat
	// tool-eligibility decisions.
	mux.HandleFunc("GET /hecate/v1/providers/presets", handler.HandleProviderPresets)
	mux.HandleFunc("GET /hecate/v1/providers/status", handler.HandleProviderStatus)
	mux.HandleFunc("GET /hecate/v1/providers/history", handler.HandleProviderHealthHistory)
}

func registerHecateProjectRoutes(mux *http.ServeMux, handler *Handler) {
	// Projects are durable user/work identity. Chats and tasks can later attach
	// to them for shared defaults, memory, and context assembly.
	mux.HandleFunc("GET /hecate/v1/projects", handler.HandleProjects)
	mux.HandleFunc("POST /hecate/v1/projects", handler.HandleCreateProject)
	mux.HandleFunc("GET /hecate/v1/projects/backend-status", handler.HandleProjectCoordinationBackendStatus)
	mux.HandleFunc("POST /hecate/v1/project-assistant/context", handler.HandleProjectAssistantContext)
	mux.HandleFunc("POST /hecate/v1/project-assistant/draft", handler.HandleProjectAssistantDraft)
	mux.HandleFunc("POST /hecate/v1/project-assistant/propose", handler.HandleProjectAssistantPropose)
	mux.HandleFunc("POST /hecate/v1/project-assistant/apply", handler.HandleProjectAssistantApply)
	mux.HandleFunc("GET /hecate/v1/project-assistant/proposals/{id}", handler.HandleProjectAssistantProposal)
	mux.HandleFunc("GET /hecate/v1/projects/{id}", handler.HandleProject)
	mux.HandleFunc("PATCH /hecate/v1/projects/{id}", handler.HandleUpdateProject)
	mux.HandleFunc("DELETE /hecate/v1/projects/{id}", handler.HandleDeleteProject)
	mux.HandleFunc("GET /hecate/v1/projects/cairnline/mirror-parity", handler.HandleProjectCairnlineMirrorParity)
	mux.HandleFunc("POST /hecate/v1/projects/cairnline/sidecar-connect", handler.HandleProjectCairnlineSidecarConnect)
	mux.HandleFunc("POST /hecate/v1/projects/cairnline/sidecar-detail-smoke", handler.HandleProjectCairnlineSidecarDetailSmoke)
	mux.HandleFunc("POST /hecate/v1/projects/cairnline/sidecar-probe", handler.HandleProjectCairnlineSidecarProbe)
	mux.HandleFunc("POST /hecate/v1/projects/cairnline/sidecar-read-smoke", handler.HandleProjectCairnlineSidecarReadSmoke)
	mux.HandleFunc("POST /hecate/v1/projects/cairnline/sync", handler.HandleSyncProjectsToCairnline)
	mux.HandleFunc("GET /hecate/v1/projects/{id}/cairnline/parity-report", handler.HandleProjectCairnlineParityReport)
	mux.HandleFunc("GET /hecate/v1/projects/{id}/cairnline/read-model", handler.HandleProjectCairnlineReadModel)
	mux.HandleFunc("GET /hecate/v1/projects/{id}/cairnline/embedded-read-model", handler.HandleProjectCairnlineEmbeddedReadModel)
	mux.HandleFunc("GET /hecate/v1/projects/{id}/cairnline/embedded-parity-report", handler.HandleProjectCairnlineEmbeddedParityReport)
	mux.HandleFunc("POST /hecate/v1/projects/{id}/cairnline/export", handler.HandleExportProjectToCairnline)
	mux.HandleFunc("POST /hecate/v1/projects/{id}/roots", handler.HandleCreateProjectRoot)
	mux.HandleFunc("POST /hecate/v1/projects/{id}/roots/discover", handler.HandleDiscoverProjectRoots)
	mux.HandleFunc("POST /hecate/v1/projects/{id}/roots/worktrees", handler.HandleCreateProjectWorktreeRoot)
	mux.HandleFunc("PATCH /hecate/v1/projects/{id}/roots/{root_id}", handler.HandleUpdateProjectRoot)
	mux.HandleFunc("DELETE /hecate/v1/projects/{id}/roots/{root_id}", handler.HandleDeleteProjectRoot)
	mux.HandleFunc("POST /hecate/v1/projects/{id}/context-sources", handler.HandleCreateProjectContextSource)
	mux.HandleFunc("POST /hecate/v1/projects/{id}/context-sources/discover", handler.HandleDiscoverProjectContextSources)
	mux.HandleFunc("PATCH /hecate/v1/projects/{id}/context-sources/{source_id}", handler.HandleUpdateProjectContextSource)
	mux.HandleFunc("DELETE /hecate/v1/projects/{id}/context-sources/{source_id}", handler.HandleDeleteProjectContextSource)
	mux.HandleFunc("GET /hecate/v1/projects/{id}/skills", handler.HandleProjectSkills)
	mux.HandleFunc("POST /hecate/v1/projects/{id}/skills/discover", handler.HandleDiscoverProjectSkills)
	mux.HandleFunc("PATCH /hecate/v1/projects/{id}/skills/{skill_id}", handler.HandleUpdateProjectSkill)
	mux.HandleFunc("GET /hecate/v1/projects/{id}/memory", handler.HandleProjectMemoryEntries)
	mux.HandleFunc("POST /hecate/v1/projects/{id}/memory", handler.HandleCreateProjectMemoryEntry)
	mux.HandleFunc("GET /hecate/v1/projects/{id}/memory/candidates", handler.HandleProjectMemoryCandidates)
	mux.HandleFunc("POST /hecate/v1/projects/{id}/memory/candidates", handler.HandleCreateProjectMemoryCandidate)
	mux.HandleFunc("POST /hecate/v1/projects/{id}/memory/candidates/{candidate_id}/promote", handler.HandlePromoteProjectMemoryCandidate)
	mux.HandleFunc("POST /hecate/v1/projects/{id}/memory/candidates/{candidate_id}/reject", handler.HandleRejectProjectMemoryCandidate)
	mux.HandleFunc("PATCH /hecate/v1/projects/{id}/memory/{memory_id}", handler.HandleUpdateProjectMemoryEntry)
	mux.HandleFunc("DELETE /hecate/v1/projects/{id}/memory/{memory_id}", handler.HandleDeleteProjectMemoryEntry)
	mux.HandleFunc("GET /hecate/v1/projects/{id}/activity", handler.HandleProjectActivity)
	mux.HandleFunc("GET /hecate/v1/projects/{id}/health", handler.HandleProjectHealth)
	mux.HandleFunc("GET /hecate/v1/projects/{id}/operations/brief", handler.HandleProjectOperationsBrief)
	mux.HandleFunc("GET /hecate/v1/projects/{id}/setup-readiness", handler.HandleProjectSetupReadiness)
	mux.HandleFunc("GET /hecate/v1/projects/{id}/roles", handler.HandleProjectWorkRoles)
	mux.HandleFunc("POST /hecate/v1/projects/{id}/roles", handler.HandleCreateProjectWorkRole)
	mux.HandleFunc("PATCH /hecate/v1/projects/{id}/roles/{role_id}", handler.HandleUpdateProjectWorkRole)
	mux.HandleFunc("DELETE /hecate/v1/projects/{id}/roles/{role_id}", handler.HandleDeleteProjectWorkRole)
	mux.HandleFunc("GET /hecate/v1/projects/{id}/work-items", handler.HandleProjectWorkItems)
	mux.HandleFunc("POST /hecate/v1/projects/{id}/work-items", handler.HandleCreateProjectWorkItem)
	mux.HandleFunc("GET /hecate/v1/projects/{id}/work-items/{work_item_id}", handler.HandleProjectWorkItem)
	mux.HandleFunc("GET /hecate/v1/projects/{id}/work-items/{work_item_id}/readiness", handler.HandleProjectWorkItemReadiness)
	mux.HandleFunc("PATCH /hecate/v1/projects/{id}/work-items/{work_item_id}", handler.HandleUpdateProjectWorkItem)
	mux.HandleFunc("DELETE /hecate/v1/projects/{id}/work-items/{work_item_id}", handler.HandleDeleteProjectWorkItem)
	mux.HandleFunc("GET /hecate/v1/projects/{id}/work-items/{work_item_id}/assignments", handler.HandleProjectWorkAssignments)
	mux.HandleFunc("POST /hecate/v1/projects/{id}/work-items/{work_item_id}/assignments", handler.HandleCreateProjectWorkAssignment)
	mux.HandleFunc("PATCH /hecate/v1/projects/{id}/work-items/{work_item_id}/assignments/{assignment_id}", handler.HandleUpdateProjectWorkAssignment)
	mux.HandleFunc("DELETE /hecate/v1/projects/{id}/work-items/{work_item_id}/assignments/{assignment_id}", handler.HandleDeleteProjectWorkAssignment)
	mux.HandleFunc("GET /hecate/v1/projects/{id}/work-items/{work_item_id}/assignments/{assignment_id}/launch-readiness", handler.HandleProjectWorkAssignmentLaunchReadiness)
	mux.HandleFunc("GET /hecate/v1/projects/{id}/work-items/{work_item_id}/assignments/{assignment_id}/preflight", handler.HandleProjectWorkAssignmentPreflight)
	mux.HandleFunc("POST /hecate/v1/projects/{id}/work-items/{work_item_id}/assignments/{assignment_id}/start", handler.HandleStartProjectWorkAssignment)
	mux.HandleFunc("GET /hecate/v1/projects/{id}/work-items/{work_item_id}/assignments/{assignment_id}/context", handler.HandleProjectWorkAssignmentContext)
	mux.HandleFunc("GET /hecate/v1/projects/{id}/work-items/{work_item_id}/artifacts", handler.HandleProjectWorkArtifacts)
	mux.HandleFunc("POST /hecate/v1/projects/{id}/work-items/{work_item_id}/artifacts", handler.HandleCreateProjectWorkArtifact)
	mux.HandleFunc("GET /hecate/v1/projects/{id}/handoffs", handler.HandleProjectHandoffs)
	mux.HandleFunc("GET /hecate/v1/projects/{id}/work-items/{work_item_id}/handoffs", handler.HandleProjectWorkItemHandoffs)
	mux.HandleFunc("POST /hecate/v1/projects/{id}/work-items/{work_item_id}/handoffs", handler.HandleCreateProjectHandoff)
	mux.HandleFunc("PATCH /hecate/v1/projects/{id}/work-items/{work_item_id}/handoffs/{handoff_id}", handler.HandleUpdateProjectHandoff)
	mux.HandleFunc("POST /hecate/v1/projects/{id}/work-items/{work_item_id}/handoffs/{handoff_id}/status", handler.HandleUpdateProjectHandoffStatus)
	mux.HandleFunc("DELETE /hecate/v1/projects/{id}/work-items/{work_item_id}/handoffs/{handoff_id}", handler.HandleDeleteProjectHandoff)
}

func registerHecateAgentRoutes(mux *http.ServeMux, handler *Handler) {
	// External-agent adapters and agent-chat sessions are Hecate-native state:
	// approvals, grants, diffs, and session streams.
	mux.HandleFunc("GET /hecate/v1/agent-profiles", handler.HandleAgentProfiles)
	mux.HandleFunc("POST /hecate/v1/agent-profiles", handler.HandleCreateAgentProfile)
	mux.HandleFunc("GET /hecate/v1/agent-profiles/{id}", handler.HandleAgentProfile)
	mux.HandleFunc("PATCH /hecate/v1/agent-profiles/{id}", handler.HandleUpdateAgentProfile)
	mux.HandleFunc("DELETE /hecate/v1/agent-profiles/{id}", handler.HandleDeleteAgentProfile)
	mux.HandleFunc("GET /hecate/v1/agent-adapters", handler.HandleAgentAdapters)
	mux.HandleFunc("POST /hecate/v1/agent-adapters/{id}/probe", handler.HandleAgentAdapterProbe)
	mux.HandleFunc("GET /hecate/v1/agent-adapters/{id}/health", handler.HandleAgentAdapterHealth)
	mux.HandleFunc("POST /hecate/v1/agent-adapters/{id}/authenticate", handler.HandleAgentAdapterAuthenticate)
	mux.HandleFunc("POST /hecate/v1/agent-adapters/{id}/logout", handler.HandleAgentAdapterLogout)
	mux.HandleFunc("GET /hecate/v1/chat/sessions", handler.HandleChatSessions)
	mux.HandleFunc("POST /hecate/v1/chat/sessions", handler.HandleCreateChatSession)
	mux.HandleFunc("GET /hecate/v1/chat/sessions/{id}", handler.HandleChatSession)
	mux.HandleFunc("PATCH /hecate/v1/chat/sessions/{id}", handler.HandleUpdateChatSession)
	mux.HandleFunc("DELETE /hecate/v1/chat/sessions/{id}", handler.HandleDeleteChatSession)
	mux.HandleFunc("GET /hecate/v1/chat/sessions/{id}/stream", handler.HandleChatSessionStream)
	mux.HandleFunc("POST /hecate/v1/chat/sessions/{id}/cancel", handler.HandleCancelChatSession)
	mux.HandleFunc("POST /hecate/v1/chat/sessions/{id}/close", handler.HandleCloseChatSession)
	mux.HandleFunc("POST /hecate/v1/chat/sessions/{id}/compact", handler.HandleCompactChatSession)
	mux.HandleFunc("PATCH /hecate/v1/chat/sessions/{id}/settings", handler.HandleSetAgentChatSettings)
	mux.HandleFunc("POST /hecate/v1/chat/sessions/{id}/config-options/{config_id}", handler.HandleSetAgentChatConfigOption)
	mux.HandleFunc("POST /hecate/v1/chat/sessions/{id}/messages", handler.HandleCreateChatMessage)
	mux.HandleFunc("POST /hecate/v1/chat/sessions/{id}/project-assistant/draft", handler.HandleChatProjectAssistantDraft)
	mux.HandleFunc("GET /hecate/v1/chat/sessions/{id}/workspace-diff", handler.HandleChatWorkspaceDiff)
	mux.HandleFunc("GET /hecate/v1/chat/sessions/{id}/workspace-files", handler.HandleChatWorkspaceFiles)
	mux.HandleFunc("GET /hecate/v1/chat/sessions/{id}/workspace-diff/files/{path...}", handler.HandleChatWorkspaceFileDiff)
	mux.HandleFunc("POST /hecate/v1/chat/sessions/{id}/workspace-diff/revert", handler.HandleRevertChatWorkspaceFiles)
	mux.HandleFunc("GET /hecate/v1/chat/sessions/{id}/messages/{message_id}/files", handler.HandleChatMessageFiles)
	mux.HandleFunc("GET /hecate/v1/chat/sessions/{id}/messages/{message_id}/files/{path...}", handler.HandleChatMessageFileDiff)
	mux.HandleFunc("GET /hecate/v1/chat/sessions/{id}/messages/{message_id}/context", handler.HandleChatMessageContext)
	mux.HandleFunc("POST /hecate/v1/chat/sessions/{id}/messages/{message_id}/revert", handler.HandleRevertChatMessageFiles)
	mux.HandleFunc("GET /hecate/v1/chat/sessions/{id}/approvals", handler.HandleListChatApprovals)
	mux.HandleFunc("GET /hecate/v1/chat/sessions/{id}/approvals/{approval_id}", handler.HandleGetChatApproval)
	mux.HandleFunc("POST /hecate/v1/chat/sessions/{id}/approvals/{approval_id}/resolve", handler.HandleResolveChatApproval)
	mux.HandleFunc("POST /hecate/v1/chat/sessions/{id}/approvals/{approval_id}/cancel", handler.HandleCancelChatApproval)
	mux.HandleFunc("GET /hecate/v1/chat/grants", handler.HandleListChatGrants)
	mux.HandleFunc("DELETE /hecate/v1/chat/grants/{grant_id}", handler.HandleDeleteChatGrant)
}

func registerHecateTaskRoutes(mux *http.ServeMux, handler *Handler) {
	// Native task/runtime API: tasks, runs, approvals, events, patches, and
	// artifacts. This is the canonical execution surface for task-backed Hecate Chat.
	mux.HandleFunc("GET /hecate/v1/tasks", handler.HandleTasks)
	mux.HandleFunc("POST /hecate/v1/tasks", handler.HandleCreateTask)
	mux.HandleFunc("GET /hecate/v1/tasks/{id}", handler.HandleTask)
	mux.HandleFunc("DELETE /hecate/v1/tasks/{id}", handler.HandleDeleteTask)
	mux.HandleFunc("POST /hecate/v1/tasks/{id}/start", handler.HandleStartTask)
	mux.HandleFunc("GET /hecate/v1/tasks/{id}/approvals", handler.HandleTaskApprovals)
	mux.HandleFunc("GET /hecate/v1/tasks/{id}/approvals/{approval_id}", handler.HandleTaskApproval)
	mux.HandleFunc("POST /hecate/v1/tasks/{id}/approvals/{approval_id}/resolve", handler.HandleResolveTaskApproval)
	mux.HandleFunc("GET /hecate/v1/tasks/{id}/runs", handler.HandleTaskRuns)
	mux.HandleFunc("GET /hecate/v1/tasks/{id}/runs/{run_id}", handler.HandleTaskRun)
	mux.HandleFunc("GET /hecate/v1/tasks/{id}/runs/{run_id}/context", handler.HandleTaskRunContext)
	mux.HandleFunc("GET /hecate/v1/tasks/{id}/runs/{run_id}/stream", handler.HandleTaskRunStream)
	mux.HandleFunc("GET /hecate/v1/tasks/{id}/runs/{run_id}/events", handler.HandleTaskRunEvents)
	mux.HandleFunc("POST /hecate/v1/tasks/{id}/runs/{run_id}/events", handler.HandleAppendTaskRunEvent)
	mux.HandleFunc("POST /hecate/v1/tasks/{id}/runs/{run_id}/retry", handler.HandleRetryTaskRun)
	mux.HandleFunc("POST /hecate/v1/tasks/{id}/runs/{run_id}/retry-from-turn", handler.HandleRetryTaskRunFromTurn)
	mux.HandleFunc("POST /hecate/v1/tasks/{id}/runs/{run_id}/resume", handler.HandleResumeTaskRun)
	mux.HandleFunc("POST /hecate/v1/tasks/{id}/runs/{run_id}/continue", handler.HandleContinueTaskRun)
	mux.HandleFunc("POST /hecate/v1/tasks/{id}/runs/{run_id}/cancel", handler.HandleCancelTaskRun)
	mux.HandleFunc("GET /hecate/v1/tasks/{id}/runs/{run_id}/steps", handler.HandleTaskRunSteps)
	mux.HandleFunc("GET /hecate/v1/tasks/{id}/runs/{run_id}/steps/{step_id}", handler.HandleTaskRunStep)
	mux.HandleFunc("POST /hecate/v1/tasks/{id}/runs/{run_id}/patches/{artifact_id}/revert", handler.HandleRevertTaskRunPatch)
	mux.HandleFunc("POST /hecate/v1/tasks/{id}/runs/{run_id}/patches/{artifact_id}/apply", handler.HandleApplyTaskRunPatch)
	mux.HandleFunc("GET /hecate/v1/tasks/{id}/runs/{run_id}/patches/{artifact_id}", handler.HandleTaskRunPatch)
	mux.HandleFunc("GET /hecate/v1/tasks/{id}/runs/{run_id}/patches", handler.HandleTaskRunPatches)
	mux.HandleFunc("GET /hecate/v1/tasks/{id}/runs/{run_id}/artifacts/{artifact_id}", handler.HandleTaskRunArtifact)
	mux.HandleFunc("GET /hecate/v1/tasks/{id}/runs/{run_id}/artifacts", handler.HandleTaskRunArtifacts)
	mux.HandleFunc("GET /hecate/v1/tasks/{id}/artifacts", handler.HandleTaskArtifacts)
	mux.HandleFunc("GET /hecate/v1/events", handler.HandleEvents)
	mux.HandleFunc("GET /hecate/v1/events/stream", handler.HandleEventsStream)
}

func registerHecateOperationsRoutes(mux *http.ServeMux, handler *Handler) {
	// Local bridge endpoint used by the desktop app / browser UI.
	mux.HandleFunc("POST /hecate/v1/workspace-dialog", handler.HandleWorkspaceDialog)
	mux.HandleFunc("POST /hecate/v1/workspace-open", handler.HandleWorkspaceOpen)
	mux.HandleFunc("POST /hecate/v1/terminals", handler.HandleCreateTerminal)
	mux.HandleFunc("GET /hecate/v1/terminals/{terminal_id}/output", handler.HandleTerminalOutput)
	mux.HandleFunc("POST /hecate/v1/terminals/{terminal_id}/input", handler.HandleWriteTerminalInput)
	mux.HandleFunc("POST /hecate/v1/terminals/{terminal_id}/wait", handler.HandleWaitTerminal)
	mux.HandleFunc("POST /hecate/v1/terminals/{terminal_id}/kill", handler.HandleKillTerminal)
	mux.HandleFunc("DELETE /hecate/v1/terminals/{terminal_id}", handler.HandleReleaseTerminal)

	// Observability and system operations: local traces, request history,
	// retention, runtime health, and MCP diagnostics.
	mux.HandleFunc("GET /hecate/v1/traces", handler.HandleTracesOrTrace)
	mux.HandleFunc("GET /hecate/v1/system/retention/runs", handler.HandleRetentionRuns)
	mux.HandleFunc("POST /hecate/v1/system/retention/run", handler.HandleRetentionRun)
	mux.HandleFunc("GET /hecate/v1/system/stats", handler.HandleRuntimeStats)
	mux.HandleFunc("GET /hecate/v1/system/mcp/cache", handler.HandleMCPCacheStats)
	mux.HandleFunc("POST /hecate/v1/system/reset-data", handler.HandleSystemResetData)
	mux.HandleFunc("POST /hecate/v1/system/shutdown", handler.HandleSystemShutdown)
	mux.HandleFunc("GET /hecate/v1/mcp/registry/servers", handler.HandleMCPRegistryServers)
	mux.HandleFunc("POST /hecate/v1/mcp/probe", handler.HandleMCPProbe)
	mux.HandleFunc("GET /hecate/v1/usage/events", handler.HandleUsageEvents)
	mux.HandleFunc("GET /hecate/v1/usage/summary", handler.HandleUsageSummary)
}

func registerHecatePluginRoutes(mux *http.ServeMux, handler *Handler) {
	// Plugin records are registry-only metadata in this slice. The routes are
	// local-only in remote-runtime mode until package trust, auth binding, and
	// capability execution policies exist.
	mux.HandleFunc("GET /hecate/v1/plugins", handler.HandlePlugins)
	mux.HandleFunc("POST /hecate/v1/plugins/install-local", handler.HandleInstallLocalPlugin)
	mux.HandleFunc("GET /hecate/v1/plugins/{id}", handler.HandlePlugin)
	mux.HandleFunc("PATCH /hecate/v1/plugins/{id}", handler.HandleUpdatePlugin)
	mux.HandleFunc("GET /hecate/v1/plugins/{id}/health", handler.HandlePluginHealth)
}

func registerHecateSettingsRoutes(mux *http.ServeMux, handler *Handler) {
	// Operator settings: configured providers, local discovery, and policy
	// rules. These replace the old /admin/control-plane action routes before
	// the API becomes stable.
	mux.HandleFunc("GET /hecate/v1/settings", handler.HandleSettingsStatus)
	mux.HandleFunc("GET /hecate/v1/settings/providers/local-discovery", handler.HandleLocalProviderDiscovery)
	mux.HandleFunc("POST /hecate/v1/settings/providers", handler.HandleSettingsCreateProvider)
	mux.HandleFunc("PATCH /hecate/v1/settings/providers/{id}", handler.HandleSettingsUpdateProvider)
	mux.HandleFunc("DELETE /hecate/v1/settings/providers/{id}", handler.HandleSettingsDeleteProvider)
	mux.HandleFunc("PUT /hecate/v1/settings/providers/{id}/api-key", handler.HandleSettingsSetProviderAPIKey)
	mux.HandleFunc("POST /hecate/v1/settings/policy-rules", handler.HandleSettingsUpsertPolicyRule)
	mux.HandleFunc("DELETE /hecate/v1/settings/policy-rules/{id}", handler.HandleSettingsDeletePolicyRule)
}

func apiNotFound(w http.ResponseWriter, r *http.Request) {
	http.NotFound(w, r)
}

func registerAPINotFound(mux *http.ServeMux) {
	for _, method := range []string{
		http.MethodGet,
		http.MethodPost,
		http.MethodPut,
		http.MethodPatch,
		http.MethodDelete,
		http.MethodOptions,
	} {
		for _, prefix := range apiPathPrefixes {
			mux.HandleFunc(method+" "+prefix, apiNotFound)
			mux.HandleFunc(method+" "+prefix+"/{path...}", apiNotFound)
		}
	}
}
