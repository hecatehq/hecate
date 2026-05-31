import type { ModelCapabilitiesRecord } from "./model";

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

export type ChatSessionSummaryRecord = {
  id: string;
  title: string;
  project_id?: string;
  agent_id?: string;
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

export type ChatMessageRecord = {
  id: string;
  execution_mode?: "external_agent" | "hecate_task" | string;
  // tools_enabled is the per-turn tools-on/off signal the gateway
  // recorded when this message was appended.
  tools_enabled?: boolean;
  segment_id?: string;
  task_id?: string;
  run_id?: string;
  request_id?: string;
  trace_id?: string;
  span_id?: string;
  role: "user" | "assistant";
  content: string;
  raw_output?: string;
  agent_id?: string;
  agent_name?: string;
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
  activities?: ChatActivityRecord[];
  usage?: ChatUsageRecord;
  timing?: ChatTimingRecord;
  context_packet?: ChatContextPacketRecord;
};

export type ChatContextPacketRecord = {
  version?: string;
  execution_mode?: string;
  provider?: string;
  model?: string;
  workspace?: string;
  system_prompt_included?: boolean;
  message_count?: number;
  sources?: ChatContextSourceRecord[];
};

export type ChatContextSourceRecord = {
  kind: string;
  label: string;
  detail?: string;
  trust?: string;
};

export type ChatSegmentRecord = {
  id: string;
  execution_mode: "external_agent" | "hecate_task" | string;
  tools_enabled?: boolean;
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

export type ChatUsageRecord = {
  context_size?: number;
  context_used?: number;
  reported_cost_amount?: string;
  reported_cost_currency?: string;
};

export type ChatTimingRecord = {
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

export type ChatActivityRecord = {
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
  children?: ChatActivityRecord[];
};

export type ChatConfigSelectOptionRecord = {
  value: string;
  name: string;
  description?: string;
  group?: string;
  group_name?: string;
};

export type ChatConfigOptionRecord = {
  id: string;
  name: string;
  description?: string;
  category?: string;
  source?: "launch" | (string & {});
  type: "select" | "boolean" | (string & {});
  current_value?: string;
  current_bool?: boolean;
  options?: ChatConfigSelectOptionRecord[];
};

export type ChatSessionRecord = {
  id: string;
  title: string;
  project_id?: string;
  agent_id?: string;
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
  config_options?: ChatConfigOptionRecord[];
  segments?: ChatSegmentRecord[];
  messages?: ChatMessageRecord[];
};

export type ChatSessionsResponse = {
  object: string;
  data: ChatSessionSummaryRecord[];
};

export type ChatSessionResponse = {
  object: string;
  data: ChatSessionRecord;
};

export type ChatChangedFileRecord = {
  path: string;
  additions: number;
  deletions: number;
  status: string;
};

export type ChatChangedFilesResponse = {
  object: string;
  data: ChatChangedFileRecord[];
};

export type ChatWorkspaceDiffRecord = {
  workspace?: string;
  diff_stat?: string;
  diff?: string;
  has_changes: boolean;
  files: ChatChangedFileRecord[];
};

export type ChatWorkspaceDiffResponse = {
  object: string;
  data: ChatWorkspaceDiffRecord;
};

export type ChatChangedFileDiffRecord = ChatChangedFileRecord & {
  diff: string;
};

export type ChatChangedFileDiffResponse = {
  object: string;
  data: ChatChangedFileDiffRecord;
};

export type ChatRevertResponse = {
  object: string;
  data: {
    reverted: boolean;
    paths: string[];
    diff_stat?: string;
    files: ChatChangedFileRecord[];
  };
};

// ChatApprovalOption mirrors agentApprovalOptionItem on the wire.
// One per ACP option offered by the adapter.
export type ChatApprovalOption = {
  option_id: string;
  kind: string;
  name: string;
};

// ChatApprovalRecord is the full row returned by GET
// /hecate/v1/chat/sessions/{id}/approvals[/{approval_id}]. The
// renderAgentApproval function on the backend is the source of truth
// for field names and optionality.
export type ChatApprovalRecord = {
  id: string;
  session_id: string;
  adapter_id: string;
  workspace?: string;
  tool_kind: string;
  tool_name?: string;
  status: string;
  acp_options: ChatApprovalOption[];
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

// ChatApprovalsResponse is the list-endpoint wire shape.
export type ChatApprovalsResponse = {
  object: string;
  data: ChatApprovalRecord[];
};

// ChatApprovalResponse is the single-row wire shape.
export type ChatApprovalResponse = {
  object: string;
  data: ChatApprovalRecord;
};

// ChatGrantRecord is the wire shape for an "always allow / always
// deny" grant. Returned by GET /hecate/v1/chat/grants.
export type ChatGrantRecord = {
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

export type ChatGrantsResponse = {
  object: string;
  data: ChatGrantRecord[];
};

// ChatApprovalRequestedEvent is the SSE payload published when a
// new approval is recorded. Minimal — the full row is reachable via
// GET /hecate/v1/chat/sessions/{id}/approvals/{approval_id}.
//
// Mirror of api.ChatApprovalRequestedEvent (Go).
export type ChatApprovalRequestedEvent = {
  approval_id: string;
  session_id: string;
  adapter_id: string;
  tool_kind: string;
  tool_name?: string;
  scope_choices?: string[];
  created_at: string;
  expires_at: string;
};

// ChatApprovalResolvedEvent is the SSE payload published on every
// terminal transition. The Path field discriminates how the approval
// resolved: operator | grant | default_mode | timeout | request_cancelled.
//
// Mirror of api.ChatApprovalResolvedEvent (Go).
export type ChatApprovalResolvedEvent = {
  approval_id: string;
  session_id: string;
  status: string;
  decision?: string;
  scope?: string;
  path: string;
  selected_option?: string;
  resolved_at?: string;
};

// ChatStreamEvent is the discriminated union surfaced by
// streamChatSession. Consumers switch on `type` and tolerate
// unknown values (forward-compat for new event kinds).
export type ChatStreamEvent =
  | { type: "session_update"; payload: ChatSessionResponse }
  | { type: "approval.requested"; payload: ChatApprovalRequestedEvent }
  | { type: "approval.resolved"; payload: ChatApprovalResolvedEvent };

// PendingAgentApproval is the banner-essentials projection of an
// approval row. Stored in `pendingApprovalsBySessionID` and consumed
// by the Chats banner / modal trigger. Field shape is identical to
// the SSE `approval.requested` event payload — both the catch-up
// refetch and the streamed event project down to this — but the alias
// keeps UI components decoupled from the SSE wire vocabulary.
export type PendingAgentApproval = ChatApprovalRequestedEvent;

export type WorkspaceDialogResponse = {
  object: string;
  data: {
    path: string;
    branch?: string;
  };
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
