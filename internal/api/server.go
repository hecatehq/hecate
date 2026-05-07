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
	registerHecateAgentRoutes(mux, handler)
	registerHecateTaskRoutes(mux, handler)
	registerHecateChatRoutes(mux, handler)
	registerHecateOperationsRoutes(mux, handler)
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
		SameOriginMiddleware,
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

	// Provider/model diagnostics and capability overrides power setup, routing,
	// and Hecate Chat tool-eligibility decisions.
	mux.HandleFunc("GET /hecate/v1/providers/presets", handler.HandleProviderPresets)
	mux.HandleFunc("GET /hecate/v1/providers/status", handler.HandleProviderStatus)
	mux.HandleFunc("GET /hecate/v1/providers/history", handler.HandleProviderHealthHistory)
	mux.HandleFunc("PUT /hecate/v1/model-capabilities/overrides", handler.HandleUpsertModelCapabilityOverride)
	mux.HandleFunc("DELETE /hecate/v1/model-capabilities/overrides", handler.HandleDeleteModelCapabilityOverride)
	mux.HandleFunc("POST /hecate/v1/model-capabilities/probes", handler.HandleRecordModelCapabilityProbe)
}

func registerHecateAgentRoutes(mux *http.ServeMux, handler *Handler) {
	// External-agent adapters and agent-chat sessions are Hecate-native state:
	// approvals, grants, launcher refresh, diffs, and session streams.
	mux.HandleFunc("GET /hecate/v1/agent-adapters", handler.HandleAgentAdapters)
	mux.HandleFunc("POST /hecate/v1/agent-adapters/{id}/probe", handler.HandleAgentAdapterProbe)
	mux.HandleFunc("POST /hecate/v1/agent-adapters/{id}/refresh-launcher", handler.HandleAgentAdapterRefreshLauncher)
	mux.HandleFunc("GET /hecate/v1/agent-adapters/{id}/health", handler.HandleAgentAdapterHealth)
	mux.HandleFunc("GET /hecate/v1/agent-chat/sessions", handler.HandleAgentChatSessions)
	mux.HandleFunc("POST /hecate/v1/agent-chat/sessions", handler.HandleCreateAgentChatSession)
	mux.HandleFunc("GET /hecate/v1/agent-chat/sessions/{id}", handler.HandleAgentChatSession)
	mux.HandleFunc("DELETE /hecate/v1/agent-chat/sessions/{id}", handler.HandleDeleteAgentChatSession)
	mux.HandleFunc("GET /hecate/v1/agent-chat/sessions/{id}/stream", handler.HandleAgentChatSessionStream)
	mux.HandleFunc("POST /hecate/v1/agent-chat/sessions/{id}/cancel", handler.HandleCancelAgentChatSession)
	mux.HandleFunc("POST /hecate/v1/agent-chat/sessions/{id}/close", handler.HandleCloseAgentChatSession)
	mux.HandleFunc("POST /hecate/v1/agent-chat/sessions/{id}/messages", handler.HandleCreateAgentChatMessage)
	mux.HandleFunc("GET /hecate/v1/agent-chat/sessions/{id}/messages/{message_id}/files", handler.HandleAgentChatMessageFiles)
	mux.HandleFunc("GET /hecate/v1/agent-chat/sessions/{id}/messages/{message_id}/files/{path...}", handler.HandleAgentChatMessageFileDiff)
	mux.HandleFunc("POST /hecate/v1/agent-chat/sessions/{id}/messages/{message_id}/revert", handler.HandleRevertAgentChatMessageFiles)
	mux.HandleFunc("GET /hecate/v1/agent-chat/sessions/{id}/approvals", handler.HandleListAgentChatApprovals)
	mux.HandleFunc("GET /hecate/v1/agent-chat/sessions/{id}/approvals/{approval_id}", handler.HandleGetAgentChatApproval)
	mux.HandleFunc("POST /hecate/v1/agent-chat/sessions/{id}/approvals/{approval_id}/resolve", handler.HandleResolveAgentChatApproval)
	mux.HandleFunc("POST /hecate/v1/agent-chat/sessions/{id}/approvals/{approval_id}/cancel", handler.HandleCancelAgentChatApproval)
	mux.HandleFunc("GET /hecate/v1/agent-chat/grants", handler.HandleListAgentChatGrants)
	mux.HandleFunc("DELETE /hecate/v1/agent-chat/grants/{grant_id}", handler.HandleDeleteAgentChatGrant)
}

func registerHecateTaskRoutes(mux *http.ServeMux, handler *Handler) {
	// Native task/runtime API: tasks, runs, approvals, events, patches, and
	// artifacts. This is the canonical execution surface for Hecate Agent.
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

func registerHecateChatRoutes(mux *http.ServeMux, handler *Handler) {
	// Direct model-chat history is Hecate UI/runtime state. The actual model
	// invocation protocol remains /v1/chat/completions.
	mux.HandleFunc("GET /hecate/v1/chat/sessions", handler.HandleChatSessions)
	mux.HandleFunc("POST /hecate/v1/chat/sessions", handler.HandleCreateChatSession)
	mux.HandleFunc("GET /hecate/v1/chat/sessions/{id}", handler.HandleChatSession)
	mux.HandleFunc("PATCH /hecate/v1/chat/sessions/{id}", handler.HandleUpdateChatSession)
	mux.HandleFunc("DELETE /hecate/v1/chat/sessions/{id}", handler.HandleDeleteChatSession)
}

func registerHecateOperationsRoutes(mux *http.ServeMux, handler *Handler) {
	// Local bridge endpoint used by the desktop app / browser UI.
	mux.HandleFunc("POST /hecate/v1/workspace-dialog", handler.HandleWorkspaceDialog)

	// Observability and system operations: local traces, request ledger,
	// retention, runtime health, and MCP diagnostics.
	mux.HandleFunc("GET /hecate/v1/traces", handler.HandleTracesOrTrace)
	mux.HandleFunc("GET /hecate/v1/system/retention/runs", handler.HandleRetentionRuns)
	mux.HandleFunc("POST /hecate/v1/system/retention/run", handler.HandleRetentionRun)
	mux.HandleFunc("GET /hecate/v1/system/stats", handler.HandleRuntimeStats)
	mux.HandleFunc("GET /hecate/v1/system/mcp/cache", handler.HandleMCPCacheStats)
	mux.HandleFunc("POST /hecate/v1/mcp/probe", handler.HandleMCPProbe)
	mux.HandleFunc("GET /hecate/v1/observability/requests", handler.HandleRequestLedger)

	// Cost surfaces are operator accounting state, separated from settings so
	// budget actions and usage summaries read naturally.
	mux.HandleFunc("GET /hecate/v1/costs/budget", handler.HandleBudgetStatus)
	mux.HandleFunc("GET /hecate/v1/costs/summary", handler.HandleAccountSummary)
	mux.HandleFunc("POST /hecate/v1/costs/budget/topup", handler.HandleBudgetTopUp)
	mux.HandleFunc("POST /hecate/v1/costs/budget/limit", handler.HandleBudgetSetLimit)
	mux.HandleFunc("POST /hecate/v1/costs/budget/reset", handler.HandleBudgetReset)
}

func registerHecateSettingsRoutes(mux *http.ServeMux, handler *Handler) {
	// Operator settings: configured providers, local discovery, policy rules,
	// and pricebook management. These replace the old /admin/control-plane
	// action routes before the API becomes stable.
	mux.HandleFunc("GET /hecate/v1/settings", handler.HandleControlPlaneStatus)
	mux.HandleFunc("GET /hecate/v1/settings/providers/local-discovery", handler.HandleLocalProviderDiscovery)
	mux.HandleFunc("POST /hecate/v1/settings/providers", handler.HandleControlPlaneCreateProvider)
	mux.HandleFunc("PATCH /hecate/v1/settings/providers/{id}", handler.HandleControlPlaneUpdateProvider)
	mux.HandleFunc("DELETE /hecate/v1/settings/providers/{id}", handler.HandleControlPlaneDeleteProvider)
	mux.HandleFunc("PUT /hecate/v1/settings/providers/{id}/api-key", handler.HandleControlPlaneSetProviderAPIKey)
	mux.HandleFunc("POST /hecate/v1/settings/policy-rules", handler.HandleControlPlaneUpsertPolicyRule)
	mux.HandleFunc("DELETE /hecate/v1/settings/policy-rules/{id}", handler.HandleControlPlaneDeletePolicyRule)
	mux.HandleFunc("POST /hecate/v1/settings/pricebook", handler.HandleControlPlaneUpsertPricebookEntry)
	mux.HandleFunc("DELETE /hecate/v1/settings/pricebook/{provider}/{model}", handler.HandleControlPlaneDeletePricebookEntry)
	mux.HandleFunc("POST /hecate/v1/settings/pricebook/import/preview", handler.HandleControlPlanePricebookImportPreview)
	mux.HandleFunc("POST /hecate/v1/settings/pricebook/import/apply", handler.HandleControlPlanePricebookImportApply)
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
		mux.HandleFunc(method+" /admin", apiNotFound)
		mux.HandleFunc(method+" /admin/{path...}", apiNotFound)
		mux.HandleFunc(method+" /v1/{path...}", apiNotFound)
		mux.HandleFunc(method+" /hecate/v1/{path...}", apiNotFound)
	}
}
