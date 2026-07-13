export type TaskRecord = {
  id: string;
  title: string;
  prompt: string;
  project_id?: string;
  work_item_id?: string;
  assignment_id?: string;
  // Per-task agent_loop system prompt — narrowest layer in the
  // composition (global → workspace CLAUDE.md/AGENTS.md → this).
  system_prompt?: string;
  repo?: string;
  base_branch?: string;
  workspace_mode?: string;
  execution_kind?: string;
  execution_profile?: string;
  agent_preset_id?: string;
  agent_preset_tools_enabled?: boolean;
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
  project_id?: string;
  work_item_id?: string;
  assignment_id?: string;
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

export type TaskArtifactResponse = {
  object: string;
  data: TaskArtifactRecord;
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
