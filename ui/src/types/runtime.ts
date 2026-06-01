export type HealthResponse = {
  status: string;
  time: string;
  // Build identifier of the gateway. "dev" for local builds; release
  // builds (via goreleaser) inject the git tag.
  version?: string;
};

export type SessionResponse = {
  object: string;
  data: {
    role: string;
  };
};

export type RuntimeStatsResponse = {
  object: string;
  data: {
    checked_at: string;
    queue_depth: number;
    queue_capacity: number;
    queue_backend?: string;
    worker_count: number;
    in_flight_jobs: number;
    queued_runs: number;
    running_runs: number;
    awaiting_approval_runs: number;
    oldest_queued_age_seconds: number;
    oldest_running_age_seconds: number;
    store_backend?: string;
    // Configured external-agent approval mode: "auto", "prompt", or
    // "deny". UI renders a danger banner when "auto". Empty when the
    // backend was built without an approval coordinator.
    agent_adapter_approval_mode?: string;
    // Optional command-output compaction helper. Hecate never enables it
    // automatically; the UI uses this only to show an opt-in setup hint.
    rtk_available?: boolean;
    rtk_path?: string;
    // Optional extension points.
    telemetry?: {
      checked_at?: string;
      signals?: Record<
        string,
        {
          enabled?: boolean;
          endpoint?: string;
          last_activity_at?: string;
          last_error?: string;
          last_error_at?: string;
          activity_count?: number;
          error_count?: number;
        }
      >;
    };
    slo?: {
      queue_wait_ms_p50?: number;
      queue_wait_ms_p95?: number;
      approval_wait_ms_p50?: number;
      approval_wait_ms_p95?: number;
      run_success_rate?: number;
      run_error_rate?: number;
    };
  };
};

export type RuntimeHeaders = {
  requestId: string;
  traceId: string;
  spanId: string;
  provider: string;
  providerKind: string;
  routeReason: string;
  requestedModel: string;
  resolvedModel: string;
  attempts: string;
  retries: string;
  fallbackFrom: string;
  costUsd: string;
};

// Local-models surface — Hecate-managed llama.cpp runtime. Wire shapes
// match internal/api/handler_local_models.go and the RFC at
// docs/rfcs/local-models-llamacpp.md.

export type LocalModelCapabilities = {
  tool_calling?: string;
  streaming: boolean;
  max_context_tokens?: number;
};

export type LocalModelCatalogEntry = {
  id: string;
  display_name: string;
  description?: string;
  huggingface_url: string;
  sha256?: string;
  size_bytes?: number;
  recommended_context?: number;
  capabilities?: LocalModelCapabilities;
  license?: string;
  installed: boolean;
};

export type LocalModelInstalled = {
  id: string;
  display_name?: string;
  file_path: string;
  source_url?: string;
  sha256?: string;
  size_bytes?: number;
  recommended_context?: number;
  capabilities?: LocalModelCapabilities;
  installed_at?: string;
  last_loaded_at?: string;
};

export type LocalModelRuntimeState = "idle" | "starting" | "running" | "stopping" | "failed";

export type LocalModelRuntimeStatus = {
  state: LocalModelRuntimeState;
  active_model_id?: string;
  port?: number;
  pid?: number;
  started_at?: string;
  last_error?: string;
  last_error_at?: string;
};

export type LocalModelFeatureAvailability = {
  available: boolean;
  reason?: string;
  binary_path?: string;
};

export type LocalModelCatalogResponse = {
  object: string;
  data: LocalModelCatalogEntry[];
};

export type LocalModelInstalledResponse = {
  object: string;
  data: LocalModelInstalled[];
};

export type LocalModelRuntimeResponse = {
  object: string;
  state: LocalModelRuntimeState;
  available: boolean;
  reason?: string;
  binary_path?: string;
  active?: LocalModelRuntimeStatus;
  availability: LocalModelFeatureAvailability;
};

export type LocalModelInstallResponse = {
  object: string;
  install_id: string;
  model_id: string;
};

// LocalModelProgressEvent is the parsed shape of one SSE event from
// GET /hecate/v1/local-models/install/{id}/events. The `kind` field is
// the SSE event name; the rest is the payload body.
export type LocalModelProgressKind =
  | "started"
  | "progress"
  | "completed"
  | "failed"
  | "cancelled";

export type LocalModelProgressEvent = {
  kind: LocalModelProgressKind;
  model_id?: string;
  bytes_downloaded?: number;
  bytes_total?: number;
  sha256?: string;
  expected_sha256?: string;
  actual_sha256?: string;
  error_kind?: string;
  message?: string;
  emitted_at: string;
};

// HuggingFaceModel is one row in the HF Hub search results.
export type HuggingFaceModel = {
  id: string;
  author?: string;
  downloads?: number;
  likes?: number;
  last_modified?: string;
  tags?: string[];
  pipeline_tag?: string;
  gated?: boolean;
};

// HuggingFaceFile is one GGUF file in an HF repo's tree, with the
// pre-computed download URL the install endpoint accepts as-is.
export type HuggingFaceFile = {
  path: string;
  size: number;
  sha256?: string;
  download_url: string;
};

export type HuggingFaceSearchResponse = {
  object: string;
  data: HuggingFaceModel[];
};

export type HuggingFaceRepoFilesResponse = {
  object: string;
  data: HuggingFaceFile[];
};

// MCPCacheStatsResponse is the wire shape for GET /hecate/v1/system/mcp/cache.
// `configured: false` means no cache is wired; the counters still
// render as zeros so the UI can show a "no cache" cell instead of
// error-handling a 4xx. See docs/mcp.md "Lifecycle and caching"
// for the underlying contract.
export type MCPCacheStatsResponse = {
  object: string;
  data: {
    checked_at: string;
    configured: boolean;
    entries: number;
    in_use: number;
    idle: number;
  };
};

export type SystemResetDataResponse = {
  object: string;
  data: {
    projects_deleted: number;
    chat_sessions_deleted: number;
    tasks_deleted: number;
    providers_deleted: number;
    policy_rules_deleted: number;
    agent_approval_grants_deleted: number;
    database_rows_deleted: number;
  };
};
