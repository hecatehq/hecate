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
	registerHecateSettingsRoutes(mux, handler)
	registerLocalModelsRoutes(mux, handler)
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
		OTelHTTPSpanMiddleware,
		SameOriginMiddlewareWithAllowedOrigins(handler.config.Server.AllowedOrigins),
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
	mux.HandleFunc("GET /hecate/v1/projects/{id}", handler.HandleProject)
	mux.HandleFunc("PATCH /hecate/v1/projects/{id}", handler.HandleUpdateProject)
	mux.HandleFunc("DELETE /hecate/v1/projects/{id}", handler.HandleDeleteProject)
}

func registerHecateAgentRoutes(mux *http.ServeMux, handler *Handler) {
	// External-agent adapters and agent-chat sessions are Hecate-native state:
	// approvals, grants, launcher refresh, diffs, and session streams.
	mux.HandleFunc("GET /hecate/v1/agent-adapters", handler.HandleAgentAdapters)
	mux.HandleFunc("POST /hecate/v1/agent-adapters/{id}/probe", handler.HandleAgentAdapterProbe)
	mux.HandleFunc("POST /hecate/v1/agent-adapters/{id}/refresh-launcher", handler.HandleAgentAdapterRefreshLauncher)
	mux.HandleFunc("GET /hecate/v1/agent-adapters/{id}/health", handler.HandleAgentAdapterHealth)
	mux.HandleFunc("GET /hecate/v1/chat/sessions", handler.HandleChatSessions)
	mux.HandleFunc("POST /hecate/v1/chat/sessions", handler.HandleCreateChatSession)
	mux.HandleFunc("GET /hecate/v1/chat/sessions/{id}", handler.HandleChatSession)
	mux.HandleFunc("PATCH /hecate/v1/chat/sessions/{id}", handler.HandleUpdateChatSession)
	mux.HandleFunc("DELETE /hecate/v1/chat/sessions/{id}", handler.HandleDeleteChatSession)
	mux.HandleFunc("GET /hecate/v1/chat/sessions/{id}/stream", handler.HandleChatSessionStream)
	mux.HandleFunc("POST /hecate/v1/chat/sessions/{id}/cancel", handler.HandleCancelChatSession)
	mux.HandleFunc("POST /hecate/v1/chat/sessions/{id}/close", handler.HandleCloseChatSession)
	mux.HandleFunc("PATCH /hecate/v1/chat/sessions/{id}/settings", handler.HandleSetAgentChatSettings)
	mux.HandleFunc("POST /hecate/v1/chat/sessions/{id}/config-options/{config_id}", handler.HandleSetAgentChatConfigOption)
	mux.HandleFunc("POST /hecate/v1/chat/sessions/{id}/messages", handler.HandleCreateChatMessage)
	mux.HandleFunc("GET /hecate/v1/chat/sessions/{id}/workspace-diff", handler.HandleChatWorkspaceDiff)
	mux.HandleFunc("GET /hecate/v1/chat/sessions/{id}/workspace-diff/files/{path...}", handler.HandleChatWorkspaceFileDiff)
	mux.HandleFunc("POST /hecate/v1/chat/sessions/{id}/workspace-diff/revert", handler.HandleRevertChatWorkspaceFiles)
	mux.HandleFunc("GET /hecate/v1/chat/sessions/{id}/messages/{message_id}/files", handler.HandleChatMessageFiles)
	mux.HandleFunc("GET /hecate/v1/chat/sessions/{id}/messages/{message_id}/files/{path...}", handler.HandleChatMessageFileDiff)
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

	// Observability and system operations: local traces, request history,
	// retention, runtime health, and MCP diagnostics.
	mux.HandleFunc("GET /hecate/v1/traces", handler.HandleTracesOrTrace)
	mux.HandleFunc("GET /hecate/v1/system/retention/runs", handler.HandleRetentionRuns)
	mux.HandleFunc("POST /hecate/v1/system/retention/run", handler.HandleRetentionRun)
	mux.HandleFunc("GET /hecate/v1/system/stats", handler.HandleRuntimeStats)
	mux.HandleFunc("GET /hecate/v1/system/mcp/cache", handler.HandleMCPCacheStats)
	mux.HandleFunc("POST /hecate/v1/system/reset-data", handler.HandleSystemResetData)
	mux.HandleFunc("POST /hecate/v1/system/shutdown", handler.HandleSystemShutdown)
	mux.HandleFunc("POST /hecate/v1/mcp/probe", handler.HandleMCPProbe)
	mux.HandleFunc("GET /hecate/v1/usage/events", handler.HandleUsageEvents)
	mux.HandleFunc("GET /hecate/v1/usage/summary", handler.HandleUsageSummary)
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

// registerLocalModelsRoutes mounts the public local-models API and
// the gateway-internal reverse-proxy. The proxy mount is only added
// when the service is wired (i.e. the bundled binary resolved); the
// public API is always mounted because the dormant-state handlers
// surface a useful 503 / availability=false response that the UI
// renders as "not available in this build". See
// docs/rfcs/local-models-llamacpp.md.
func registerLocalModelsRoutes(mux *http.ServeMux, handler *Handler) {
	mux.HandleFunc("GET /hecate/v1/local-models/catalog", handler.HandleLocalModelsCatalog)
	mux.HandleFunc("GET /hecate/v1/local-models/installed", handler.HandleLocalModelsInstalled)
	mux.HandleFunc("POST /hecate/v1/local-models/install", handler.HandleLocalModelsInstall)
	mux.HandleFunc("GET /hecate/v1/local-models/install/{install_id}/events", handler.HandleLocalModelsInstallEvents)
	mux.HandleFunc("DELETE /hecate/v1/local-models/install/{install_id}", handler.HandleLocalModelsCancelInstall)
	mux.HandleFunc("DELETE /hecate/v1/local-models/installed/{model_id}", handler.HandleLocalModelsUninstall)
	mux.HandleFunc("GET /hecate/v1/local-models/runtime", handler.HandleLocalModelsRuntimeStatus)
	mux.HandleFunc("POST /hecate/v1/local-models/runtime/start", handler.HandleLocalModelsRuntimeStart)
	mux.HandleFunc("POST /hecate/v1/local-models/runtime/stop", handler.HandleLocalModelsRuntimeStop)
	mux.HandleFunc("GET /hecate/v1/local-models/huggingface/search", handler.HandleLocalModelsHFSearch)
	mux.HandleFunc("GET /hecate/v1/local-models/huggingface/repos/{owner}/{name}", handler.HandleLocalModelsHFRepoFiles)

	// Gateway-internal reverse-proxy. Only mounted when the
	// service is wired — without it the proxy struct doesn't
	// exist. Catch-all method match via the {path...} wildcard.
	if svc := handler.LocalModelsService(); svc != nil {
		mux.Handle("/hecate/internal/llamacpp/v1/{path...}", svc.Proxy())
	}
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
