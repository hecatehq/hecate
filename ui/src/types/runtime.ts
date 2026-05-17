export type HealthResponse = {
  status: string;
  time: string;
  // Build identifier of the gateway. "dev" for local builds; release
  // builds (via goreleaser) inject the git tag.
  version?: string;
};

export type ModelRecord = {
  id: string;
  owned_by: string;
  metadata?: {
    provider?: string;
    provider_kind?: string;
    default?: boolean;
    discovery_source?: string;
    capabilities?: ModelCapabilitiesRecord;
    readiness?: ModelReadinessRecord;
  };
};

export type ModelCapabilitiesRecord = {
  tool_calling?: "unknown" | "none" | "basic" | "parallel" | string;
  streaming?: boolean;
  max_context_tokens?: number;
  source?: "unknown" | "catalog" | "provider" | "probe" | "operator_override" | string;
  note?: string;
  updated_at?: string;
};

export type ModelCapabilityRecord = {
  provider: string;
  model: string;
  tool_calling: "unknown" | "none" | "basic" | "parallel" | string;
  streaming?: boolean;
  max_context_tokens?: number;
  source?: "unknown" | "catalog" | "provider" | "probe" | "operator_override" | string;
  note?: string;
  updated_at?: string;
};

export type ModelReadinessRecord = {
  provider?: string;
  matched_provider?: string;
  model?: string;
  ready: boolean;
  status?: ProviderReadinessStatus;
  reason?: string;
  message?: string;
  operator_action?: string;
  routing_ready?: boolean;
  provider_status?: string;
  provider_blocked_reason?: string;
  suggested_models?: string[];
};

export type ModelCapabilityResponse = {
  object: string;
  data: ModelCapabilityRecord;
};

export type ModelResponse = {
  object: string;
  data: ModelRecord[];
};

export type SessionResponse = {
  object: string;
  data: {
    role: string;
  };
};

export type ChatSessionSummaryRecord = {
  id: string;
  title: string;
  message_count: number;
  provider_call_count: number;
  created_at?: string;
  updated_at?: string;
  last_model?: string;
  last_provider?: string;
  last_cost_usd?: string;
  last_request_id?: string;
};

// PersistedContentBlock mirrors the Hecate-extension wire shape used to
// persist Anthropic-aware content (thinking, tool_use, image with
// cache_control). Replay paths emit it; SDK clients hitting the OpenAI
// proxy don't.
export type PersistedContentBlock = {
  type: string;
  text?: string;
  id?: string;
  name?: string;
  input?: unknown;
  tool_use_id?: string;
  cache_control?: unknown;
  thinking?: string;
  signature?: string;
  data?: string;
  image_url?: { url: string; detail?: string };
};

// ChatSessionMessageRecord is one entry in a session's flat message
// stream as returned by GET /hecate/v1/chat/sessions/{id}. The role/content/
// tool_calls/content_blocks fields are flattened onto the same object
// (the gateway side embeds OpenAIChatMessage inside ChatSessionMessageItem).
export type ChatSessionMessageRecord = {
  id: string;
  sequence: number;
  produced_by_call_id?: string;
  created_at?: string;
  role: string;
  content: string | null;
  name?: string;
  tool_call_id?: string;
  tool_calls?: ToolCall[];
  content_blocks?: PersistedContentBlock[];
  tool_error?: boolean;
};

// ChatProviderCallRecord is one upstream chat-completion request: its
// routing decision, model, tokens, and cost. Multiple messages can
// reference the same call via produced_by_call_id.
export type ChatProviderCallRecord = {
  id: string;
  request_id: string;
  requested_provider?: string;
  provider: string;
  provider_kind?: string;
  requested_model?: string;
  model: string;
  cost_micros_usd: number;
  cost_usd: string;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  created_at?: string;
};

export type ChatSessionRecord = {
  id: string;
  title: string;
  system_prompt?: string;
  created_at?: string;
  updated_at?: string;
  messages?: ChatSessionMessageRecord[];
  provider_calls?: ChatProviderCallRecord[];
};

export type ChatSessionsResponse = {
  object: string;
  data: ChatSessionSummaryRecord[];
  has_more?: boolean;
};

export type ChatSessionResponse = {
  object: string;
  data: ChatSessionRecord;
};

export type ProviderRecord = {
  name: string;
  kind: string;
  base_url?: string;
  credential_state?: "configured" | "missing" | "not_required" | "unknown";
  credential_ready?: boolean;
  healthy: boolean;
  status: string;
  routing_ready?: boolean;
  routing_blocked_reason?: string;
  default_model?: string;
  models?: string[];
  model_count?: number;
  discovery_source?: string;
  refreshed_at?: string;
  last_checked_at?: string;
  last_error?: string;
  last_error_class?: string;
  open_until?: string;
  last_latency_ms?: number;
  consecutive_failures?: number;
  total_successes?: number;
  total_failures?: number;
  timeouts?: number;
  server_errors?: number;
  rate_limits?: number;
  readiness?: ProviderReadinessSummaryRecord;
  readiness_checks?: ProviderReadinessCheckRecord[];
};

export type ProviderReadinessStatus = "ok" | "warning" | "blocked" | "unknown";

export type ProviderReadinessSummaryRecord = {
  status?: ProviderReadinessStatus;
  reason?: string;
  message?: string;
  operator_action?: string;
};

export type ProviderReadinessCheckRecord = {
  name: string;
  status: ProviderReadinessStatus;
  reason?: string;
  message?: string;
  operator_action?: string;
};

export type ProviderStatusResponse = {
  object: string;
  data: ProviderRecord[];
};

export type ProviderPresetRecord = {
  id: string;
  name: string;
  kind: string;
  protocol: string;
  base_url: string;
  api_key_env?: string;
  api_version?: string;
  default_model?: string;
  docs_url?: string;
  description?: string;
  env_snippet?: string;
};

export type ProviderPresetResponse = {
  object: string;
  data: ProviderPresetRecord[];
};

export type LocalProviderDiscoveryRecord = {
  preset_id: string;
  name: string;
  base_url: string;
  probe_url: string;
  status: "running" | "installed" | "not_detected" | "error" | "unknown";
  command?: string;
  command_available: boolean;
  command_path?: string;
  http_available: boolean;
  model_count?: number;
  models?: string[];
  error?: string;
};

export type LocalProviderDiscoveryResponse = {
  object: string;
  data: LocalProviderDiscoveryRecord[];
};

export type AgentAdapterRecord = {
  id: string;
  name: string;
  kind: string;
  command: string;
  args?: string[];
  managed?: boolean;
  managed_package?: string;
  available: boolean;
  status: string;
  path?: string;
  error?: string;
  description?: string;
  cost_mode?: string;
  docs_url?: string;
  version?: string;
  supported_range?: string;
  version_outside_range?: boolean;
  auth_status?: "ok" | "unauthenticated" | "billing" | "unknown" | string;
  auth_error?: string;
  credential_configured?: boolean;
  credential_preview?: string;
  claude_code_cli?: AgentAdapterSetupCommandStatus;
};

export type AgentAdapterResponse = {
  object: string;
  data: AgentAdapterRecord[];
};

export type AgentAdapterSetupCommandStatus = {
  available: boolean;
  command?: string;
  executable_path?: string;
};

export type AgentChatSessionSummaryRecord = {
  id: string;
  title: string;
  runtime_kind?: "external_agent" | "agent" | "model" | string;
  adapter_id?: string;
  driver_kind?: string;
  native_session_id?: string;
  task_id?: string;
  latest_run_id?: string;
  provider?: string;
  model?: string;
  capabilities?: ModelCapabilitiesRecord;
  rtk_enabled?: boolean;
  workspace: string;
  workspace_branch?: string;
  status: string;
  message_count: number;
  created_at?: string;
  updated_at?: string;
};

export type AgentChatMessageRecord = {
  id: string;
  runtime_kind?: "external_agent" | "agent" | "model" | string;
  segment_id?: string;
  task_id?: string;
  run_id?: string;
  request_id?: string;
  trace_id?: string;
  span_id?: string;
  role: "user" | "assistant";
  content: string;
  raw_output?: string;
  adapter_id?: string;
  adapter_name?: string;
  driver_kind?: string;
  native_session_id?: string;
  status?: string;
  exit_code?: number;
  cost_mode?: string;
  provider?: string;
  model?: string;
  capabilities?: ModelCapabilitiesRecord;
  workspace?: string;
  diff_stat?: string;
  diff?: string;
  created_at?: string;
  started_at?: string;
  completed_at?: string;
  duration_ms?: number;
  error?: string;
  activities?: AgentChatActivityRecord[];
  usage?: AgentChatUsageRecord;
  timing?: AgentChatTimingRecord;
};

export type AgentChatSegmentRecord = {
  id: string;
  runtime_kind: "external_agent" | "agent" | "model" | string;
  provider?: string;
  model?: string;
  task_id?: string;
  latest_run_id?: string;
  workspace?: string;
  status?: string;
  message_count: number;
  started_at?: string;
  updated_at?: string;
};

export type AgentChatUsageRecord = {
  context_size?: number;
  context_used?: number;
  reported_cost_amount?: string;
  reported_cost_currency?: string;
};

export type AgentChatTimingRecord = {
  total_ms?: number;
  queue_ms?: number;
  model_ms?: number;
  tool_ms?: number;
  approval_wait_ms?: number;
  overhead_ms?: number;
  turn_count?: number;
  tool_count?: number;
  bottleneck?: string;
  bottleneck_ms?: number;
};

export type AgentChatActivityRecord = {
  id?: string;
  type: string;
  status?: string;
  kind?: string;
  title: string;
  detail?: string;
  created_at?: string;
  artifact_id?: string;
  artifact_size_bytes?: number;
  artifact_preview?: string;
  approval_id?: string;
  needs_action?: boolean;
  terminal?: boolean;
};

export type AgentChatConfigSelectOptionRecord = {
  value: string;
  name: string;
  description?: string;
  group?: string;
  group_name?: string;
};

export type AgentChatConfigOptionRecord = {
  id: string;
  name: string;
  description?: string;
  category?: string;
  type: "select" | "boolean" | (string & {});
  current_value?: string;
  current_bool?: boolean;
  options?: AgentChatConfigSelectOptionRecord[];
};

export type AgentChatSessionRecord = {
  id: string;
  title: string;
  runtime_kind?: "external_agent" | "agent" | "model" | string;
  adapter_id?: string;
  driver_kind?: string;
  native_session_id?: string;
  task_id?: string;
  latest_run_id?: string;
  provider?: string;
  model?: string;
  capabilities?: ModelCapabilitiesRecord;
  rtk_enabled?: boolean;
  workspace: string;
  workspace_branch?: string;
  status: string;
  turns_used?: number;
  max_turns_per_session?: number;
  session_started_at?: string;
  max_session_duration_ms?: number;
  idle_timeout_ms?: number;
  created_at?: string;
  updated_at?: string;
  config_options?: AgentChatConfigOptionRecord[];
  segments?: AgentChatSegmentRecord[];
  messages?: AgentChatMessageRecord[];
};

export type AgentChatSessionsResponse = {
  object: string;
  data: AgentChatSessionSummaryRecord[];
};

export type AgentChatSessionResponse = {
  object: string;
  data: AgentChatSessionRecord;
};

export type AgentChatChangedFileRecord = {
  path: string;
  additions: number;
  deletions: number;
  status: string;
};

export type AgentChatChangedFilesResponse = {
  object: string;
  data: AgentChatChangedFileRecord[];
};

export type AgentChatChangedFileDiffRecord = AgentChatChangedFileRecord & {
  diff: string;
};

export type AgentChatChangedFileDiffResponse = {
  object: string;
  data: AgentChatChangedFileDiffRecord;
};

export type AgentChatRevertResponse = {
  object: string;
  data: {
    reverted: boolean;
    paths: string[];
    diff_stat?: string;
    files: AgentChatChangedFileRecord[];
  };
};

// AgentChatApprovalOption mirrors agentApprovalOptionItem on the wire.
// One per ACP option offered by the adapter.
export type AgentChatApprovalOption = {
  option_id: string;
  kind: string;
  name: string;
};

// AgentChatApprovalRecord is the full row returned by GET
// /hecate/v1/agent-chat/sessions/{id}/approvals[/{approval_id}]. The
// renderAgentApproval function on the backend is the source of truth
// for field names and optionality.
export type AgentChatApprovalRecord = {
  id: string;
  session_id: string;
  adapter_id: string;
  workspace?: string;
  tool_kind: string;
  tool_name?: string;
  status: string;
  acp_options: AgentChatApprovalOption[];
  scope_choices?: string[];
  selected_option?: string;
  scope?: string;
  decision?: string;
  path?: string;
  decision_note?: string;
  created_at: string;
  resolved_at?: string;
  expires_at: string;
};

// AgentChatApprovalsResponse is the list-endpoint wire shape.
export type AgentChatApprovalsResponse = {
  object: string;
  data: AgentChatApprovalRecord[];
};

// AgentChatApprovalResponse is the single-row wire shape.
export type AgentChatApprovalResponse = {
  object: string;
  data: AgentChatApprovalRecord;
};

// AgentChatGrantRecord is the wire shape for an "always allow / always
// deny" grant. Returned by GET /hecate/v1/agent-chat/grants.
export type AgentChatGrantRecord = {
  id: string;
  scope: string;
  adapter_id: string;
  tool_kind: string;
  workspace?: string;
  session_id?: string;
  decision: string;
  granted_by?: string;
  granted_at: string;
  expires_at?: string;
};

export type AgentChatGrantsResponse = {
  object: string;
  data: AgentChatGrantRecord[];
};

// AgentChatApprovalRequestedEvent is the SSE payload published when a
// new approval is recorded. Minimal — the full row is reachable via
// GET /hecate/v1/agent-chat/sessions/{id}/approvals/{approval_id}.
//
// Mirror of api.AgentChatApprovalRequestedEvent (Go).
export type AgentChatApprovalRequestedEvent = {
  approval_id: string;
  session_id: string;
  adapter_id: string;
  tool_kind: string;
  tool_name?: string;
  scope_choices?: string[];
  created_at: string;
  expires_at: string;
};

// AgentChatApprovalResolvedEvent is the SSE payload published on every
// terminal transition. The Path field discriminates how the approval
// resolved: operator | grant | default_mode | timeout | request_cancelled.
//
// Mirror of api.AgentChatApprovalResolvedEvent (Go).
export type AgentChatApprovalResolvedEvent = {
  approval_id: string;
  session_id: string;
  status: string;
  decision?: string;
  scope?: string;
  path: string;
  selected_option?: string;
  resolved_at?: string;
};

// AgentChatStreamEvent is the discriminated union surfaced by
// streamAgentChatSession. Consumers switch on `type` and tolerate
// unknown values (forward-compat for new event kinds).
export type AgentChatStreamEvent =
  | { type: "session_update"; payload: AgentChatSessionResponse }
  | { type: "approval.requested"; payload: AgentChatApprovalRequestedEvent }
  | { type: "approval.resolved"; payload: AgentChatApprovalResolvedEvent };

// PendingAgentApproval is the banner-essentials projection of an
// approval row. Stored in `pendingApprovalsBySessionID` and consumed
// by the Chats banner / modal trigger. Field shape is identical to
// the SSE `approval.requested` event payload — both the catch-up
// refetch and the streamed event project down to this — but the alias
// keeps UI components decoupled from the SSE wire vocabulary.
export type PendingAgentApproval = AgentChatApprovalRequestedEvent;

// AgentAdapterHealthRecord mirrors agentadapters.ProbeResult. Returned
// by GET /hecate/v1/agent-adapters/{id}/health. The status string is one of
// "ready" | "not_installed" | "auth_required" | "error"; the UI uses
// it to colour status chips (green / amber / red / red) and to drive
// the adapter status panel in Connections.
export type AgentAdapterHealthRecord = {
  adapter_id: string;
  status: string;
  stage: string;
  path?: string;
  error?: string;
  stderr?: string;
  hint?: string;
  duration_ms: number;
};

export type AgentAdapterHealthResponse = {
  object: string;
  data: AgentAdapterHealthRecord;
};

export type AgentAdapterProbeResponse = {
  object: string;
  data: {
    adapter: AgentAdapterRecord;
    health: AgentAdapterHealthRecord;
  };
};

export type AgentAdapterCredentialResponse = {
  object: string;
  data: {
    adapter_id: string;
    name: string;
    configured: boolean;
    preview?: string;
  };
};

export type WorkspaceDialogResponse = {
  object: string;
  data: {
    path: string;
    branch?: string;
  };
};

export type TraceEventRecord = {
  name: string;
  timestamp: string;
  attributes?: Record<string, unknown>;
};

export type TraceSpanRecord = {
  trace_id: string;
  span_id: string;
  parent_span_id?: string;
  name: string;
  kind?: string;
  start_time?: string;
  end_time?: string;
  attributes?: Record<string, unknown>;
  status_code?: string;
  status_message?: string;
  events?: TraceEventRecord[];
};

export type TraceResponse = {
  object: string;
  data: {
    request_id: string;
    trace_id?: string;
    started_at?: string;
    spans?: TraceSpanRecord[];
    route?: {
      final_provider?: string;
      final_provider_kind?: string;
      final_model?: string;
      final_reason?: string;
      fallback_from?: string;
      candidates?: Array<{
        provider?: string;
        provider_kind?: string;
        model?: string;
        reason?: string;
        outcome?: string;
        skip_reason?: string;
        health_status?: string;
        policy_rule_id?: string;
        policy_action?: string;
        policy_reason?: string;
        estimated_micros_usd?: number;
        estimated_usd?: string;
        attempt?: number;
        retry_count?: number;
        retryable?: boolean;
        index?: number;
        latency_ms?: number;
        failover_from?: string;
        failover_to?: string;
        detail?: string;
        timestamp?: string;
      }>;
      failovers?: Array<{
        from_provider?: string;
        from_model?: string;
        to_provider?: string;
        to_model?: string;
        reason?: string;
        timestamp?: string;
      }>;
    };
  };
};

export type UsageEventRecord = {
  type: string;
  scope?: string;
  provider?: string;
  model?: string;
  request_id?: string;
  actor?: string;
  detail?: string;
  amount_micros_usd: number;
  amount_usd: string;
  prompt_tokens?: number;
  completion_tokens?: number;
  total_tokens?: number;
  timestamp?: string;
};

export type UsageSummaryRecord = {
  key: string;
  scope: string;
  provider?: string;
  backend: string;
  used_micros_usd: number;
  used_usd: string;
};

export type UsageSummaryResponse = {
  object: string;
  data: UsageSummaryRecord;
};

export type UsageEventsResponse = {
  object: string;
  data: UsageEventRecord[];
};

export type TraceListItem = {
  request_id: string;
  trace_id?: string;
  started_at?: string;
  span_count: number;
  duration_ms?: number;
  status_code?: string;
  status_message?: string;
  route?: {
    final_provider?: string;
    final_provider_kind?: string;
    final_model?: string;
    final_reason?: string;
    fallback_from?: string;
    candidates?: NonNullable<TraceResponse["data"]["route"]>["candidates"];
  };
};

export type TraceListResponse = {
  object: string;
  data: TraceListItem[];
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

export type ConfiguredProviderRecord = {
  id: string;
  name: string;
  preset_id?: string;
  // custom_name is an optional operator-supplied disambiguator that
  // appears alongside name in the providers table. Used to tell two
  // instances of the same preset apart ("Anthropic" + "Prod" vs
  // "Anthropic" + "Dev"). Empty when not set.
  custom_name?: string;
  kind: string;
  protocol: string;
  base_url: string;
  api_version?: string;
  default_model?: string;
  explicit_fields?: string[];
  inherited_fields?: string[];
  credential_configured: boolean;
  credential_source?: "env" | "vault";
};

export type ConfiguredPolicyRuleRecord = {
  id: string;
  action: string;
  reason?: string;
  providers?: string[];
  provider_kinds?: string[];
  models?: string[];
  route_reasons?: string[];
  min_prompt_tokens?: number;
  min_estimated_cost_micros_usd?: number;
  rewrite_model_to?: string;
};

export type ConfiguredAuditEventRecord = {
  timestamp?: string;
  actor: string;
  action: string;
  target_type: string;
  target_id: string;
  detail?: string;
};

export type ConfiguredStateResponse = {
  object: string;
  data: {
    backend: string;
    providers: ConfiguredProviderRecord[];
    policy_rules: ConfiguredPolicyRuleRecord[];
    events: ConfiguredAuditEventRecord[];
  };
};

export type RetentionRunResultRecord = {
  name: string;
  deleted: number;
  max_age?: string;
  max_count: number;
  error?: string;
  skipped?: boolean;
};

export type RetentionRunData = {
  started_at: string;
  finished_at: string;
  trigger: string;
  actor?: string;
  request_id?: string;
  results: RetentionRunResultRecord[];
};

export type RetentionRunResponse = {
  object: string;
  data: RetentionRunData;
};

export type RetentionRunsResponse = {
  object: string;
  data: RetentionRunData[];
};

export type ToolCallFunction = {
  name: string;
  arguments: string;
};

export type ToolCall = {
  id: string;
  type: string;
  function: ToolCallFunction;
};

export type ChatResponse = {
  id: string;
  model: string;
  choices: Array<{
    index: number;
    finish_reason: string;
    message: {
      role: string;
      content: string | null;
      tool_calls?: ToolCall[];
    };
  }>;
  usage?: {
    prompt_tokens: number;
    completion_tokens: number;
    total_tokens: number;
  };
};

export type TaskRecord = {
  id: string;
  title: string;
  prompt: string;
  // Per-task agent_loop system prompt — narrowest layer in the
  // composition (global → workspace CLAUDE.md/AGENTS.md → this).
  system_prompt?: string;
  repo?: string;
  base_branch?: string;
  workspace_mode?: string;
  execution_kind?: string;
  execution_profile?: string;
  origin_kind?: string;
  origin_id?: string;
  shell_command?: string;
  git_command?: string;
  working_directory?: string;
  file_operation?: string;
  file_path?: string;
  file_content?: string;
  sandbox_allowed_root?: string;
  sandbox_read_only?: boolean;
  sandbox_network?: boolean;
  timeout_ms?: number;
  status: string;
  priority?: string;
  requested_model?: string;
  requested_provider?: string;
  budget_micros_usd?: number;
  latest_run_id?: string;
  // What the most recent run actually used after routing (may
  // differ from requested_* when the operator picked "auto" or
  // the router substituted). Surfaced in the task list.
  latest_model?: string;
  latest_provider?: string;
  pending_approval_count?: number;
  step_count?: number;
  artifact_count?: number;
  last_error?: string;
  created_at?: string;
  updated_at?: string;
  started_at?: string;
  finished_at?: string;
  root_trace_id?: string;
  latest_trace_id?: string;
  latest_request_id?: string;
  // MCPServers echoes the configured external MCP servers (if
  // any). Used by the task list to show an "MCP × N" chip and the
  // task detail to render the per-server configuration. Mirrors
  // the wire shape — see api.MCPServerConfigItem on the gateway
  // side. Secret values (env, headers) come back redacted unless
  // they're $VAR_NAME references; approval_policy and url/command
  // are surfaced verbatim.
  mcp_servers?: Array<{
    name: string;
    command?: string;
    args?: string[];
    env?: Record<string, string>;
    url?: string;
    headers?: Record<string, string>;
    approval_policy?: string;
  }>;
};

export type TasksResponse = {
  object: string;
  data: TaskRecord[];
};

export type TaskResponse = {
  object: string;
  data: TaskRecord;
};

export type TaskRunRecord = {
  id: string;
  task_id: string;
  number: number;
  status: string;
  orchestrator?: string;
  model?: string;
  provider?: string;
  provider_kind?: string;
  workspace_id?: string;
  workspace_path?: string;
  step_count?: number;
  approval_count?: number;
  artifact_count?: number;
  total_cost_micros_usd?: number;
  // prior_cost_micros_usd is the cumulative LLM spend of earlier
  // runs in this run's resume chain. Cumulative = total + prior;
  // useful when a task has been resumed/retried multiple times.
  prior_cost_micros_usd?: number;
  last_error?: string;
  started_at?: string;
  finished_at?: string;
  request_id?: string;
  trace_id?: string;
  root_span_id?: string;
  otel_status_code?: string;
  otel_status_message?: string;
};

export type TaskRunsResponse = {
  object: string;
  data: TaskRunRecord[];
};

export type TaskRunResponse = {
  object: string;
  data: TaskRunRecord;
};

export type TaskStepRecord = {
  id: string;
  task_id: string;
  run_id: string;
  parent_step_id?: string;
  index: number;
  kind: string;
  title?: string;
  status: string;
  phase?: string;
  result?: string;
  tool_name?: string;
  input?: Record<string, unknown>;
  output_summary?: Record<string, unknown>;
  exit_code?: number;
  error?: string;
  error_kind?: string;
  approval_id?: string;
  started_at?: string;
  finished_at?: string;
  request_id?: string;
  trace_id?: string;
  span_id?: string;
  parent_span_id?: string;
};

export type TaskStepsResponse = {
  object: string;
  data: TaskStepRecord[];
};

export type TaskArtifactRecord = {
  id: string;
  task_id: string;
  run_id: string;
  step_id?: string;
  kind: string;
  name?: string;
  description?: string;
  mime_type?: string;
  storage_kind?: string;
  path?: string;
  content_text?: string;
  object_ref?: string;
  size_bytes?: number;
  sha256?: string;
  status?: string;
  created_at?: string;
  request_id?: string;
  trace_id?: string;
  span_id?: string;
};

export type TaskArtifactsResponse = {
  object: string;
  data: TaskArtifactRecord[];
};

export type TaskPatchRecord = {
  artifact: TaskArtifactRecord;
  diff: string;
  status: string;
  path?: string;
  before_existed: boolean;
};

export type TaskPatchResponse = {
  object: string;
  data: TaskPatchRecord;
};

export type TaskApprovalRecord = {
  id: string;
  task_id: string;
  run_id: string;
  step_id?: string;
  kind: string;
  status: string;
  reason?: string;
  requested_by?: string;
  resolved_by?: string;
  resolution_note?: string;
  created_at?: string;
  resolved_at?: string;
  request_id?: string;
  trace_id?: string;
  span_id?: string;
};

export type TaskApprovalsResponse = {
  object: string;
  data: TaskApprovalRecord[];
};

// TaskRunStreamTurnCost mirrors the backend `Turn` block on
// TaskRunStreamEventData. Populated only on snapshots driven by an
// `turn.completed` event, so the UI can render a live per-turn
// cost/tokens summary without subscribing to the public events stream.
export type TaskRunStreamTurnCost = {
  turn_index: number;
  step_id?: string;
  cost_micros_usd: number;
  run_cumulative_cost_micros_usd: number;
  task_cumulative_cost_micros_usd: number;
  tool_calls?: number;
};

export type TaskRunStreamEventData = {
  sequence: number;
  terminal?: boolean;
  event_type?: string;
  run: TaskRunRecord;
  steps?: TaskStepRecord[];
  approvals?: TaskApprovalRecord[];
  artifacts?: TaskArtifactRecord[];
  activity?: TaskActivityRecord[];
  turn?: TaskRunStreamTurnCost;
};

export type TaskActivityRecord = {
  id: string;
  type: string;
  status?: string;
  title?: string;
  step_id?: string;
  artifact_id?: string;
  approval_id?: string;
  tool_name?: string;
  kind?: string;
  path?: string;
  summary?: Record<string, unknown>;
  occurred_at?: string;
  terminal?: boolean;
  needs_action?: boolean;
};

export type TaskRunStreamEventResponse = {
  object: string;
  data: TaskRunStreamEventData;
};

export type TaskRunEventRecord = {
  schema_version: string;
  event_id: string;
  task_id: string;
  run_id: string;
  sequence: number;
  occurred_at: string;
  type: string;
  data: Record<string, unknown>;
};

export type TaskRunEventsResponse = {
  object: string;
  data: TaskRunEventRecord[];
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

export type ModelFilter = "all" | "local" | "cloud";
export type ProviderFilter = "auto" | string;

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
