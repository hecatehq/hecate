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
  };
};
