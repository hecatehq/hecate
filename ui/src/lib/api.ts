import type {
  HealthResponse,
  MCPCacheStatsResponse,
  RuntimeHeaders,
  RuntimeStatsResponse,
  SessionResponse,
} from "../types/runtime";
import type { ModelResponse } from "../types/model";
import type {
  ConfiguredStateResponse,
  LocalProviderDiscoveryResponse,
  ProviderPresetResponse,
  ProviderStatusResponse,
} from "../types/provider";
import type { AgentAdapterProbeResponse, AgentAdapterResponse } from "../types/agent-adapter";
import type {
  ChatApprovalRequestedEvent,
  ChatApprovalResolvedEvent,
  ChatApprovalResponse,
  ChatApprovalsResponse,
  ChatChangedFileDiffResponse,
  ChatChangedFilesResponse,
  ChatGrantsResponse,
  ChatResponse,
  ChatRevertResponse,
  ChatSessionResponse,
  ChatSessionsResponse,
  ChatStreamEvent,
  WorkspaceDialogResponse,
} from "../types/chat";
import type {
  TaskApprovalsResponse,
  TaskArtifactsResponse,
  TaskPatchResponse,
  TaskResponse,
  TaskRunEventsResponse,
  TaskRunResponse,
  TaskRunStreamEventResponse,
  TaskRunsResponse,
  TaskStepsResponse,
  TasksResponse,
} from "../types/task";
import type { TraceListResponse, TraceResponse } from "../types/trace";
import type { UsageEventsResponse, UsageSummaryResponse } from "../types/usage";
import type { RetentionRunResponse, RetentionRunsResponse } from "../types/retention";
import type {
  CreateProjectPayload,
  ProjectResponse,
  ProjectsResponse,
  UpdateProjectPayload,
} from "../types/project";

type RequestOptions = {
  method?: "GET" | "POST" | "PATCH" | "PUT" | "DELETE";
  body?: unknown;
};

type ErrorPayload = {
  error?: {
    type?: string;
    message?: string;
    user_message?: string;
    operator_action?: string;
    request_id?: string;
    trace_id?: string;
    status?: string;
    stage?: string;
    hint?: string;
  };
};

const HECATE_API = "/hecate/v1";

// PersistedContentBlock mirrors internal/api.OpenAIPersistedContentBlock.
// Used on history-replay paths so Anthropic thinking / redacted_thinking /
// tool_use / cache_control survive the round-trip.
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

// Common shape for the persisted-block + tool_error extensions that
// every role can carry. Empty for plain string-content messages.
type ChatMessageExtensions = {
  content_blocks?: PersistedContentBlock[];
  tool_error?: boolean;
};

export type ChatMessage =
  | ({ role: "user" | "system"; content: string } & ChatMessageExtensions)
  | ({
      role: "assistant";
      content: string | null;
      tool_calls?: Array<{
        id: string;
        type: string;
        function: { name: string; arguments: string };
      }>;
    } & ChatMessageExtensions)
  | ({ role: "tool"; content: string; tool_call_id: string } & ChatMessageExtensions);

export type ChatCompletionPayload = {
  model: string;
  provider: string;
  session_id?: string;
  session_title?: string;
  user: string;
  messages: ChatMessage[];
};

// PolicyRuleUpsertPayload mirrors the gateway's
// SettingsPolicyRuleRecord wire shape exactly. Empty arrays /
// zero-valued thresholds match anything; the action field gates
// whether `rewrite_model_to` and `reason` are meaningful (rewrite vs
// deny). The handler accepts arrays / numbers / strings as-is — no
// normalization at the boundary, the operator is responsible for
// well-formed inputs.
export type PolicyRuleUpsertPayload = {
  id: string;
  action: "deny" | "rewrite_model";
  reason?: string;
  providers?: string[];
  provider_kinds?: string[];
  models?: string[];
  route_reasons?: string[];
  min_prompt_tokens?: number;
  min_estimated_cost_micros_usd?: number;
  rewrite_model_to?: string;
};

export type RetentionRunPayload = {
  subsystems: string[];
};

export type CreateChatSessionPayload = {
  title?: string;
  project_id?: string;
  agent_id?: string;
  provider?: string;
  model?: string;
  workspace?: string;
  rtk_enabled?: boolean;
};

export type CreateChatMessagePayload = {
  content: string;
  execution_mode?: "external_agent" | "hecate_task" | "direct_model";
  provider?: string;
  model?: string;
  system_prompt?: string;
  workspace?: string;
};

export type CreateTaskPayload = {
  title?: string;
  prompt: string;
  execution_kind?: string;
  shell_command?: string;
  git_command?: string;
  working_directory?: string;
  file_operation?: string;
  file_path?: string;
  file_content?: string;
  requested_model?: string;
  requested_provider?: string;
  // workspace_mode controls how the run's sandbox root is provisioned:
  //   * "persistent" / "ephemeral" / unset — provision an isolated
  //     clone or copy of the source directory (default). Writes don't
  //     touch the source.
  //   * "in_place" — run directly inside the source path. Sandbox
  //     AllowedRoot becomes that path, so writes from shell_exec /
  //     file / agent_loop tools land in the operator's actual repo.
  //     Requires an absolute, existing working_directory or repo.
  workspace_mode?: string;
};

export type ResolveTaskApprovalPayload = {
  decision: "approve" | "reject";
  note?: string;
};

export type AppendTaskRunEventPayload = {
  type: string;
  step_id?: string;
  status?: string;
  note?: string;
  data?: Record<string, unknown>;
};

export async function getHealth(): Promise<HealthResponse> {
  return fetchJSON<HealthResponse>("/healthz");
}

export async function getSession(): Promise<SessionResponse> {
  return fetchJSON<SessionResponse>(`${HECATE_API}/whoami`);
}

export async function getModels(): Promise<ModelResponse> {
  return fetchJSON<ModelResponse>("/v1/models");
}

export async function getProviders(): Promise<ProviderStatusResponse> {
  return fetchJSON<ProviderStatusResponse>(`${HECATE_API}/providers/status`);
}

export async function getRuntimeStats(): Promise<RuntimeStatsResponse> {
  return fetchJSON<RuntimeStatsResponse>(`${HECATE_API}/system/stats`);
}

export async function getMCPCacheStats(): Promise<MCPCacheStatsResponse> {
  return fetchJSON<MCPCacheStatsResponse>(`${HECATE_API}/system/mcp/cache`);
}

export async function getProviderPresets(): Promise<ProviderPresetResponse> {
  return fetchJSON<ProviderPresetResponse>(`${HECATE_API}/providers/presets`);
}

export async function discoverLocalProviders(): Promise<LocalProviderDiscoveryResponse> {
  return fetchJSON<LocalProviderDiscoveryResponse>(
    `${HECATE_API}/settings/providers/local-discovery`,
  );
}

export async function getAgentAdapters(): Promise<AgentAdapterResponse> {
  return fetchJSON<AgentAdapterResponse>(`${HECATE_API}/agent-adapters`);
}

// probeAgentAdapter re-runs discovery for one adapter and performs the
// end-to-end ACP health probe. The response includes both the fresh list row
// and the deeper handshake result so Settings can update in place.
export async function probeAgentAdapter(adapterID: string): Promise<AgentAdapterProbeResponse> {
  return fetchJSON<AgentAdapterProbeResponse>(
    `${HECATE_API}/agent-adapters/${encodeURIComponent(adapterID)}/probe`,
    { method: "POST" },
  );
}

export async function refreshAgentAdapterLauncher(
  adapterID: string,
): Promise<AgentAdapterResponse> {
  return fetchJSON<AgentAdapterResponse>(
    `${HECATE_API}/agent-adapters/${encodeURIComponent(adapterID)}/refresh-launcher`,
    { method: "POST" },
  );
}

export async function getTrace(requestID: string): Promise<TraceResponse> {
  return fetchJSON<TraceResponse>(
    `${HECATE_API}/traces?request_id=${encodeURIComponent(requestID)}`,
  );
}

export async function getRecentTraces(limit = 50): Promise<TraceListResponse> {
  return fetchJSON<TraceListResponse>(
    `${HECATE_API}/traces?limit=${encodeURIComponent(String(limit))}`,
  );
}

export async function getUsageSummary(query = ""): Promise<UsageSummaryResponse> {
  return fetchJSON<UsageSummaryResponse>(`${HECATE_API}/usage/summary${query}`);
}

export async function getProjects(): Promise<ProjectsResponse> {
  return fetchJSON<ProjectsResponse>(`${HECATE_API}/projects`);
}

export async function createProject(payload: CreateProjectPayload): Promise<ProjectResponse> {
  return fetchJSON<ProjectResponse>(`${HECATE_API}/projects`, {
    method: "POST",
    body: payload,
  });
}

export async function updateProject(
  id: string,
  patch: UpdateProjectPayload,
): Promise<ProjectResponse> {
  return fetchJSON<ProjectResponse>(`${HECATE_API}/projects/${encodeURIComponent(id)}`, {
    method: "PATCH",
    body: patch,
  });
}

export async function deleteProject(id: string): Promise<void> {
  return fetchJSON<void>(`${HECATE_API}/projects/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
}

export async function getChatSessions(): Promise<ChatSessionsResponse> {
  return fetchJSON<ChatSessionsResponse>(`${HECATE_API}/chat/sessions`);
}

export async function createChatSession(
  payload: CreateChatSessionPayload,
): Promise<ChatSessionResponse> {
  return fetchJSON<ChatSessionResponse>(`${HECATE_API}/chat/sessions`, {
    method: "POST",
    body: payload,
  });
}

export async function getChatSession(id: string): Promise<ChatSessionResponse> {
  return fetchJSON<ChatSessionResponse>(`${HECATE_API}/chat/sessions/${encodeURIComponent(id)}`);
}

export async function updateChatSession(id: string, title: string): Promise<ChatSessionResponse> {
  return fetchJSON<ChatSessionResponse>(`${HECATE_API}/chat/sessions/${encodeURIComponent(id)}`, {
    method: "PATCH",
    body: { title },
  });
}

export async function deleteChatSession(id: string): Promise<void> {
  await fetchJSON<unknown>(`${HECATE_API}/chat/sessions/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
}

export async function cancelChatSession(id: string): Promise<ChatSessionResponse> {
  return fetchJSON<ChatSessionResponse>(
    `${HECATE_API}/chat/sessions/${encodeURIComponent(id)}/cancel`,
    { method: "POST", body: {} },
  );
}

export async function createChatMessage(
  id: string,
  payload: string | CreateChatMessagePayload,
): Promise<ChatSessionResponse> {
  const body = typeof payload === "string" ? { content: payload } : payload;
  return fetchJSON<ChatSessionResponse>(
    `${HECATE_API}/chat/sessions/${encodeURIComponent(id)}/messages`,
    { method: "POST", body },
  );
}

export async function setChatConfigOption(
  id: string,
  configID: string,
  value: string | boolean,
): Promise<ChatSessionResponse> {
  return fetchJSON<ChatSessionResponse>(
    `${HECATE_API}/chat/sessions/${encodeURIComponent(id)}/config-options/${encodeURIComponent(configID)}`,
    { method: "POST", body: { value } },
  );
}

export async function setChatSettings(
  id: string,
  settings: { rtk_enabled?: boolean },
): Promise<ChatSessionResponse> {
  return fetchJSON<ChatSessionResponse>(
    `${HECATE_API}/chat/sessions/${encodeURIComponent(id)}/settings`,
    { method: "PATCH", body: settings },
  );
}

export async function listChatMessageFiles(
  sessionID: string,
  messageID: string,
): Promise<ChatChangedFilesResponse> {
  return fetchJSON<ChatChangedFilesResponse>(
    `${HECATE_API}/chat/sessions/${encodeURIComponent(sessionID)}/messages/${encodeURIComponent(messageID)}/files`,
  );
}

export async function getChatMessageFileDiff(
  sessionID: string,
  messageID: string,
  path: string,
): Promise<ChatChangedFileDiffResponse> {
  return fetchJSON<ChatChangedFileDiffResponse>(
    `${HECATE_API}/chat/sessions/${encodeURIComponent(sessionID)}/messages/${encodeURIComponent(messageID)}/files/${encodeURIComponent(path)}`,
  );
}

export async function revertChatMessageFiles(
  sessionID: string,
  messageID: string,
  paths: string[] = [],
): Promise<ChatRevertResponse> {
  return fetchJSON<ChatRevertResponse>(
    `${HECATE_API}/chat/sessions/${encodeURIComponent(sessionID)}/messages/${encodeURIComponent(messageID)}/revert`,
    { method: "POST", body: { paths } },
  );
}

// streamChatSession reads the per-session SSE feed and dispatches
// each event to the consumer as a typed ChatStreamEvent. The Type
// discriminator on the wire (`session_update`, `approval.requested`,
// `approval.resolved`) maps directly onto the union members. Unknown
// event names are silently ignored — frontends are forward-compatible
// with new event kinds added on the backend.
export async function streamChatSession(
  id: string,
  onEvent: (event: ChatStreamEvent) => void,
  signal?: AbortSignal,
): Promise<void> {
  const response = await fetchWithNetworkError(
    `${HECATE_API}/chat/sessions/${encodeURIComponent(id)}/stream`,
    { ...buildRequestOptions({}), signal },
  );
  if (!response.ok) {
    throw await apiError(response, "request failed");
  }
  if (!response.body) {
    throw new Error("stream response body is unavailable");
  }

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let currentEvent = "message";
  let currentData = "";

  const flushEvent = () => {
    if (!currentData.trim()) {
      currentEvent = "message";
      currentData = "";
      return;
    }
    const raw = currentData;
    currentData = "";
    const eventName = currentEvent;
    currentEvent = "message";

    // Default event name is "message" (no `event:` line). The backend
    // always sends an explicit `event: …` line for every typed event,
    // so unnamed events are treated as legacy session updates.
    const dispatched = dispatchChatStreamEvent(eventName, raw);
    if (dispatched) {
      onEvent(dispatched);
    }
  };

  while (true) {
    const { done, value } = await reader.read();
    if (done) {
      flushEvent();
      return;
    }
    buffer += decoder.decode(value, { stream: true });
    const lines = buffer.split("\n");
    buffer = lines.pop() ?? "";

    for (const line of lines) {
      const trimmed = line.replace(/\r$/, "");
      if (trimmed === "") {
        flushEvent();
        continue;
      }
      if (trimmed.startsWith(":")) {
        continue;
      }
      if (trimmed.startsWith("event: ")) {
        currentEvent = trimmed.slice(7).trim() || "message";
        continue;
      }
      if (trimmed.startsWith("data: ")) {
        currentData += trimmed.slice(6);
      }
    }
  }
}

// dispatchChatStreamEvent maps a wire SSE event name + JSON
// payload onto the typed ChatStreamEvent union. Returns null for
// unknown event types so the consumer doesn't see noise. Exported for
// unit tests.
export function dispatchChatStreamEvent(
  eventName: string,
  rawData: string,
): ChatStreamEvent | null {
  switch (eventName) {
    case "session_update":
    case "snapshot":
    case "done":
    case "message":
      return { type: "session_update", payload: JSON.parse(rawData) as ChatSessionResponse };
    case "approval.requested":
      return {
        type: "approval.requested",
        payload: JSON.parse(rawData) as ChatApprovalRequestedEvent,
      };
    case "approval.resolved":
      return {
        type: "approval.resolved",
        payload: JSON.parse(rawData) as ChatApprovalResolvedEvent,
      };
    default:
      return null;
  }
}

// ─── Agent-chat approvals ──────────────────────────────────────────────────────

// listChatApprovals fetches approvals for a session. Pass
// status="pending" to scope to the operator's review queue.
export async function listChatApprovals(
  sessionID: string,
  status?: string,
): Promise<ChatApprovalsResponse> {
  const qs = status ? `?status=${encodeURIComponent(status)}` : "";
  return fetchJSON<ChatApprovalsResponse>(
    `${HECATE_API}/chat/sessions/${encodeURIComponent(sessionID)}/approvals${qs}`,
  );
}

export async function getChatApproval(
  sessionID: string,
  approvalID: string,
): Promise<ChatApprovalResponse> {
  return fetchJSON<ChatApprovalResponse>(
    `${HECATE_API}/chat/sessions/${encodeURIComponent(sessionID)}/approvals/${encodeURIComponent(approvalID)}`,
  );
}

export type ResolveChatApprovalPayload = {
  decision: "approve" | "deny";
  scope: string;
  selected_option?: string;
  note?: string;
};

export async function resolveChatApproval(
  sessionID: string,
  approvalID: string,
  payload: ResolveChatApprovalPayload,
): Promise<ChatApprovalResponse> {
  return fetchJSON<ChatApprovalResponse>(
    `${HECATE_API}/chat/sessions/${encodeURIComponent(sessionID)}/approvals/${encodeURIComponent(approvalID)}/resolve`,
    { method: "POST", body: payload },
  );
}

export async function cancelChatApproval(
  sessionID: string,
  approvalID: string,
): Promise<ChatApprovalResponse> {
  return fetchJSON<ChatApprovalResponse>(
    `${HECATE_API}/chat/sessions/${encodeURIComponent(sessionID)}/approvals/${encodeURIComponent(approvalID)}/cancel`,
    { method: "POST", body: {} },
  );
}

export type ChatGrantFilter = {
  adapter_id?: string;
  scope?: string;
  tool_kind?: string;
};

export async function listChatGrants(filter: ChatGrantFilter = {}): Promise<ChatGrantsResponse> {
  const params = new URLSearchParams();
  if (filter.adapter_id) params.set("adapter_id", filter.adapter_id);
  if (filter.scope) params.set("scope", filter.scope);
  if (filter.tool_kind) params.set("tool_kind", filter.tool_kind);
  const qs = params.toString();
  return fetchJSON<ChatGrantsResponse>(`${HECATE_API}/chat/grants${qs ? `?${qs}` : ""}`);
}

export async function deleteChatGrant(grantID: string): Promise<void> {
  await fetchJSON<unknown>(`${HECATE_API}/chat/grants/${encodeURIComponent(grantID)}`, {
    method: "DELETE",
  });
}

export async function chooseWorkspaceDirectory(): Promise<WorkspaceDialogResponse> {
  return fetchJSON<WorkspaceDialogResponse>(`${HECATE_API}/workspace-dialog`, {
    method: "POST",
    body: {},
  });
}

export async function getUsageEvents(limit = 20): Promise<UsageEventsResponse> {
  return fetchJSON<UsageEventsResponse>(
    `${HECATE_API}/usage/events?limit=${encodeURIComponent(String(limit))}`,
  );
}

export async function getSettingsConfig(): Promise<ConfiguredStateResponse> {
  return fetchJSON<ConfiguredStateResponse>(`${HECATE_API}/settings`);
}

export async function upsertPolicyRule(payload: PolicyRuleUpsertPayload): Promise<unknown> {
  return fetchJSON(`${HECATE_API}/settings/policy-rules`, { method: "POST", body: payload });
}

export async function deletePolicyRule(id: string): Promise<unknown> {
  return fetchJSON(`${HECATE_API}/settings/policy-rules/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
}

// updateProvider applies a partial update to an existing provider record.
// Editable fields:
//   - base_url:    any provider (repoint endpoint)
//   - name:        custom providers only (preset names are fixed)
//   - custom_name: any provider (operator disambiguation label)
// Credentials live behind PUT /providers/{id}/api-key, not here.
export async function updateProvider(
  id: string,
  patch: { base_url?: string; name?: string; custom_name?: string },
): Promise<unknown> {
  return fetchJSON(`${HECATE_API}/settings/providers/${encodeURIComponent(id)}`, {
    method: "PATCH",
    body: patch,
  });
}

// setProviderAPIKey sets the provider's API key. An empty `key` clears it.
export async function setProviderAPIKey(id: string, key: string): Promise<unknown> {
  return fetchJSON(`${HECATE_API}/settings/providers/${encodeURIComponent(id)}/api-key`, {
    method: "PUT",
    body: { key },
  });
}

export async function createProvider(params: {
  name: string;
  preset_id?: string;
  custom_name?: string;
  base_url?: string;
  api_key?: string;
  kind: string;
  protocol: string;
}): Promise<unknown> {
  return fetchJSON(`${HECATE_API}/settings/providers`, { method: "POST", body: params });
}

export async function deleteProvider(id: string): Promise<unknown> {
  return fetchJSON(`${HECATE_API}/settings/providers/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
}

// setProviderBaseURL is a thin wrapper around updateProvider for the
// most common edit surface — local providers rotating their endpoint.
export async function setProviderBaseURL(id: string, baseURL: string): Promise<unknown> {
  return updateProvider(id, { base_url: baseURL });
}

// setProviderName renames a custom (non-preset) provider's display
// label. Rejected by the backend with 400 for preset providers — those
// keep their catalog name and reach for setProviderCustomName instead.
export async function setProviderName(id: string, name: string): Promise<unknown> {
  return updateProvider(id, { name });
}

// setProviderCustomName sets/clears the operator disambiguation label
// that appears alongside name in the providers table. Empty string
// clears it. Allowed for any provider, including presets.
export async function setProviderCustomName(id: string, customName: string): Promise<unknown> {
  return updateProvider(id, { custom_name: customName });
}

// createProvider params include the optional custom_name disambiguator.
// When two instances of the same preset are created, the second's
// custom_name lifts the slug off the colliding default.

export async function runRetention(payload: RetentionRunPayload): Promise<RetentionRunResponse> {
  return fetchJSON<RetentionRunResponse>(`${HECATE_API}/system/retention/run`, {
    method: "POST",
    body: payload,
  });
}

export async function getRetentionRuns(limit = 10): Promise<RetentionRunsResponse> {
  return fetchJSON<RetentionRunsResponse>(
    `${HECATE_API}/system/retention/runs?limit=${encodeURIComponent(String(limit))}`,
  );
}

export async function getTasks(limit = 20): Promise<TasksResponse> {
  return fetchJSON<TasksResponse>(`${HECATE_API}/tasks?limit=${encodeURIComponent(String(limit))}`);
}

export async function createTask(payload: CreateTaskPayload): Promise<TaskResponse> {
  return fetchJSON<TaskResponse>(`${HECATE_API}/tasks`, { method: "POST", body: payload });
}

export async function getTask(taskID: string): Promise<TaskResponse> {
  return fetchJSON<TaskResponse>(`${HECATE_API}/tasks/${encodeURIComponent(taskID)}`);
}

export async function getTaskRuns(taskID: string): Promise<TaskRunsResponse> {
  return fetchJSON<TaskRunsResponse>(`${HECATE_API}/tasks/${encodeURIComponent(taskID)}/runs`);
}

export async function deleteTask(taskID: string): Promise<void> {
  await fetchJSON(`${HECATE_API}/tasks/${encodeURIComponent(taskID)}`, { method: "DELETE" });
}

export async function startTask(taskID: string): Promise<TaskRunResponse> {
  return fetchJSON<TaskRunResponse>(`${HECATE_API}/tasks/${encodeURIComponent(taskID)}/start`, {
    method: "POST",
  });
}

export async function getTaskApprovals(taskID: string): Promise<TaskApprovalsResponse> {
  return fetchJSON<TaskApprovalsResponse>(
    `${HECATE_API}/tasks/${encodeURIComponent(taskID)}/approvals`,
  );
}

export async function getTaskRunSteps(taskID: string, runID: string): Promise<TaskStepsResponse> {
  return fetchJSON<TaskStepsResponse>(
    `${HECATE_API}/tasks/${encodeURIComponent(taskID)}/runs/${encodeURIComponent(runID)}/steps`,
  );
}

export async function getTaskRunArtifacts(
  taskID: string,
  runID: string,
): Promise<TaskArtifactsResponse> {
  return fetchJSON<TaskArtifactsResponse>(
    `${HECATE_API}/tasks/${encodeURIComponent(taskID)}/runs/${encodeURIComponent(runID)}/artifacts`,
  );
}

export async function applyTaskRunPatch(
  taskID: string,
  runID: string,
  artifactID: string,
): Promise<TaskPatchResponse> {
  return fetchJSON<TaskPatchResponse>(
    `${HECATE_API}/tasks/${encodeURIComponent(taskID)}/runs/${encodeURIComponent(runID)}/patches/${encodeURIComponent(artifactID)}/apply`,
    { method: "POST" },
  );
}

export async function revertTaskRunPatch(
  taskID: string,
  runID: string,
  artifactID: string,
): Promise<TaskPatchResponse> {
  return fetchJSON<TaskPatchResponse>(
    `${HECATE_API}/tasks/${encodeURIComponent(taskID)}/runs/${encodeURIComponent(runID)}/patches/${encodeURIComponent(artifactID)}/revert`,
    { method: "POST" },
  );
}

export async function getTaskRunEvents(
  taskID: string,
  runID: string,
  afterSequence = 0,
): Promise<TaskRunEventsResponse> {
  return fetchJSON<TaskRunEventsResponse>(
    `${HECATE_API}/tasks/${encodeURIComponent(taskID)}/runs/${encodeURIComponent(runID)}/events?after_sequence=${encodeURIComponent(String(afterSequence))}`,
  );
}

export async function resolveTaskApproval(
  taskID: string,
  approvalID: string,
  payload: ResolveTaskApprovalPayload,
): Promise<void> {
  await fetchJSON(
    `${HECATE_API}/tasks/${encodeURIComponent(taskID)}/approvals/${encodeURIComponent(approvalID)}/resolve`,
    { method: "POST", body: payload },
  );
}

export async function cancelTaskRun(taskID: string, runID: string): Promise<void> {
  await fetchJSON(
    `${HECATE_API}/tasks/${encodeURIComponent(taskID)}/runs/${encodeURIComponent(runID)}/cancel`,
    { method: "POST" },
  );
}

export async function retryTaskRun(taskID: string, runID: string): Promise<TaskRunResponse> {
  return fetchJSON<TaskRunResponse>(
    `${HECATE_API}/tasks/${encodeURIComponent(taskID)}/runs/${encodeURIComponent(runID)}/retry`,
    { method: "POST", body: {} },
  );
}

export async function resumeTaskRun(taskID: string, runID: string): Promise<TaskRunResponse> {
  return fetchJSON<TaskRunResponse>(
    `${HECATE_API}/tasks/${encodeURIComponent(taskID)}/runs/${encodeURIComponent(runID)}/resume`,
    { method: "POST", body: {} },
  );
}

// resumeTaskRunRaisingCeiling pairs a budget-update with a resume in
// one server-side transaction — used by the "Raise ceiling and
// resume" affordance after a cost_ceiling_exceeded failure. The
// gateway persists the new ceiling on the task before queueing the
// resumed run so the agent loop's budget check sees the raised
// value on its very first turn (no race between PATCH-task and
// POST-resume). budgetMicrosUSD must be >= the current ceiling;
// the gateway rejects lower values with a 400.
export async function resumeTaskRunRaisingCeiling(
  taskID: string,
  runID: string,
  budgetMicrosUSD: number,
): Promise<TaskRunResponse> {
  return fetchJSON<TaskRunResponse>(
    `${HECATE_API}/tasks/${encodeURIComponent(taskID)}/runs/${encodeURIComponent(runID)}/resume`,
    { method: "POST", body: { budget_micros_usd: budgetMicrosUSD } },
  );
}

// retryTaskRunFromTurn re-runs an agent_loop run starting at turn N
// with the prior conversation preserved up to (but not including)
// that turn's assistant message. Returns the newly-created run.
// The optional reason is stored in the run.resumed_from_event event so operators
// can annotate why they branched from a particular turn.
export async function retryTaskRunFromTurn(
  taskID: string,
  runID: string,
  turn: number,
  reason?: string,
): Promise<TaskRunResponse> {
  return fetchJSON<TaskRunResponse>(
    `${HECATE_API}/tasks/${encodeURIComponent(taskID)}/runs/${encodeURIComponent(runID)}/retry-from-turn`,
    {
      method: "POST",
      body: { turn, reason: reason ?? "" },
    },
  );
}

export async function appendTaskRunEvent(
  taskID: string,
  runID: string,
  payload: AppendTaskRunEventPayload,
): Promise<void> {
  await fetchJSON(
    `${HECATE_API}/tasks/${encodeURIComponent(taskID)}/runs/${encodeURIComponent(runID)}/events`,
    { method: "POST", body: payload },
  );
}

export async function streamTaskRun(
  taskID: string,
  runID: string,
  onEvent: (event: { event: string; payload: TaskRunStreamEventResponse }) => void,
  afterSequence = 0,
  signal?: AbortSignal,
): Promise<void> {
  const response = await fetchWithNetworkError(
    `${HECATE_API}/tasks/${encodeURIComponent(taskID)}/runs/${encodeURIComponent(runID)}/stream?after_sequence=${encodeURIComponent(String(afterSequence))}`,
    { ...buildRequestOptions({}), signal },
  );
  if (!response.ok) {
    throw await apiError(response, "request failed");
  }
  if (!response.body) {
    throw new Error("stream response body is unavailable");
  }

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let currentEvent = "message";
  let currentData = "";

  const flushEvent = () => {
    if (!currentData.trim()) {
      currentEvent = "message";
      currentData = "";
      return;
    }
    const payload = JSON.parse(currentData) as TaskRunStreamEventResponse;
    onEvent({ event: currentEvent, payload });
    currentEvent = "message";
    currentData = "";
  };

  while (true) {
    const { done, value } = await reader.read();
    if (done) {
      flushEvent();
      return;
    }
    buffer += decoder.decode(value, { stream: true });
    const lines = buffer.split("\n");
    buffer = lines.pop() ?? "";

    for (const line of lines) {
      const trimmed = line.replace(/\r$/, "");
      if (trimmed === "") {
        flushEvent();
        continue;
      }
      if (trimmed.startsWith(":")) {
        continue;
      }
      if (trimmed.startsWith("event: ")) {
        currentEvent = trimmed.slice(7).trim() || "message";
        continue;
      }
      if (trimmed.startsWith("data: ")) {
        currentData += trimmed.slice(6);
      }
    }
  }
}

type StreamedToolCall = { id: string; name: string; arguments: string };

export async function chatCompletionsStream(
  payload: ChatCompletionPayload,
  onChunk: (delta: string) => void,
): Promise<{ headers: RuntimeHeaders; finishReason: string; toolCalls: StreamedToolCall[] }> {
  const response = await fetchWithNetworkError(
    "/v1/chat/completions",
    buildRequestOptions({ method: "POST", body: { ...payload, stream: true } }),
  );
  if (!response.ok) {
    throw await apiError(response, "request failed");
  }

  const headers = readRuntimeHeaders(response);

  const reader = response.body!.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let finishReason = "stop";
  // Accumulate tool call argument deltas indexed by tool_call index.
  const tcAccum: Record<number, { id: string; name: string; arguments: string }> = {};

  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });

    const lines = buffer.split("\n");
    buffer = lines.pop() ?? "";

    for (const line of lines) {
      if (!line.startsWith("data: ")) continue;
      const raw = line.slice(6).trim();
      if (raw === "[DONE]") {
        const toolCalls = Object.values(tcAccum);
        return { headers, finishReason, toolCalls };
      }
      try {
        const chunk = JSON.parse(raw) as {
          choices?: Array<{
            delta?: {
              content?: string;
              tool_calls?: Array<{
                index: number;
                id?: string;
                type?: string;
                function?: { name?: string; arguments?: string };
              }>;
            };
            finish_reason?: string | null;
          }>;
          error?: { message?: string };
        };
        if (chunk.error?.message) throw new Error(chunk.error.message);
        const choice = chunk.choices?.[0];
        if (!choice) continue;

        if (choice.finish_reason) finishReason = choice.finish_reason;

        const delta = choice.delta;
        if (delta?.content) onChunk(delta.content);

        if (delta?.tool_calls) {
          for (const tc of delta.tool_calls) {
            if (!tcAccum[tc.index]) {
              tcAccum[tc.index] = { id: "", name: "", arguments: "" };
            }
            if (tc.id) tcAccum[tc.index].id = tc.id;
            if (tc.function?.name) tcAccum[tc.index].name = tc.function.name;
            if (tc.function?.arguments) tcAccum[tc.index].arguments += tc.function.arguments;
          }
        }
      } catch (parseError) {
        if (parseError instanceof Error && parseError.message !== "JSON") throw parseError;
      }
    }
  }

  const toolCalls = Object.values(tcAccum);
  return { headers, finishReason, toolCalls };
}

export async function chatCompletions(
  payload: ChatCompletionPayload,
): Promise<{ data: ChatResponse; headers: RuntimeHeaders }> {
  const response = await fetchWithNetworkError(
    "/v1/chat/completions",
    buildRequestOptions({ method: "POST", body: payload }),
  );
  if (!response.ok) {
    throw await apiError(response, "request failed");
  }

  const data = (await response.json()) as ChatResponse;
  return {
    data,
    headers: readRuntimeHeaders(response),
  };
}

function readRuntimeHeaders(response: Response): RuntimeHeaders {
  return {
    requestId: response.headers.get("X-Request-Id") ?? "",
    traceId: response.headers.get("X-Trace-Id") ?? "",
    spanId: response.headers.get("X-Span-Id") ?? "",
    provider: response.headers.get("X-Runtime-Provider") ?? "",
    providerKind: response.headers.get("X-Runtime-Provider-Kind") ?? "",
    routeReason: response.headers.get("X-Runtime-Route-Reason") ?? "",
    requestedModel: response.headers.get("X-Runtime-Requested-Model") ?? "",
    resolvedModel: response.headers.get("X-Runtime-Model") ?? "",
    attempts: response.headers.get("X-Runtime-Attempts") ?? "",
    retries: response.headers.get("X-Runtime-Retries") ?? "",
    fallbackFrom: response.headers.get("X-Runtime-Fallback-From") ?? "",
    costUsd: response.headers.get("X-Runtime-Cost-USD") ?? "",
  };
}

export function buildRequestOptions(options: RequestOptions): RequestInit {
  const headers = new Headers();
  if (options.body !== undefined) {
    headers.set("Content-Type", "application/json");
  }

  return {
    method: options.method ?? "GET",
    headers,
    body: options.body !== undefined ? JSON.stringify(options.body) : undefined,
  };
}

// ApiError preserves the HTTP status alongside the error message so
// callers can react differently to 404s (stale resource — refresh
// the list), 401/403 (re-auth), and 5xx (transient — retry/notify).
// Without this, every fetchJSON failure looked the same and we
// couldn't distinguish "task you clicked is gone" from "network is
// down" — both surfaced as an opaque message and silent UI states.
export class ApiError extends Error {
  status: number;
  code: string;
  userMessage: string;
  operatorAction: string;
  requestId: string;
  traceId: string;
  constructor(
    message: string,
    status: number,
    code = "",
    details: Partial<
      Pick<ApiError, "userMessage" | "operatorAction" | "requestId" | "traceId">
    > = {},
  ) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.userMessage = details.userMessage ?? "";
    this.operatorAction = details.operatorAction ?? "";
    this.requestId = details.requestId ?? "";
    this.traceId = details.traceId ?? "";
  }
}

export async function fetchJSON<T>(url: string, options: RequestOptions = {}): Promise<T> {
  const response = await fetchWithNetworkError(url, buildRequestOptions(options));
  if (!response.ok) {
    throw await apiError(response, "request failed");
  }
  if (response.status === 204) {
    return undefined as T;
  }
  return (await response.json()) as T;
}

async function fetchWithNetworkError(url: string, options: RequestInit): Promise<Response> {
  try {
    return await fetch(url, options);
  } catch (error) {
    throw new Error(networkErrorMessage(url, error));
  }
}

function networkErrorMessage(url: string, error: unknown): string {
  const message = error instanceof Error ? error.message : String(error);
  if (
    message === "Load failed" ||
    message === "Failed to fetch" ||
    message.includes("NetworkError")
  ) {
    return `Gateway request failed to load (${url}). Check that the gateway is running on http://127.0.0.1:8765 and that the Vite dev proxy is active.`;
  }
  return `Gateway request failed (${url}): ${message}`;
}

async function apiError(response: Response, fallback: string): Promise<ApiError> {
  const payload = await errorPayload(response, fallback);
  return new ApiError(payload.userMessage || payload.message, response.status, payload.code, {
    userMessage: payload.userMessage,
    operatorAction: payload.operatorAction,
    requestId: payload.requestId || response.headers.get("X-Request-Id") || "",
    traceId: payload.traceId || response.headers.get("X-Trace-Id") || "",
  });
}

async function errorPayload(
  response: Response,
  fallback: string,
): Promise<{
  message: string;
  code: string;
  userMessage: string;
  operatorAction: string;
  requestId: string;
  traceId: string;
}> {
  try {
    const payload = (await response.json()) as ErrorPayload;
    return {
      message: payload.error?.message ?? fallback,
      code: payload.error?.type ?? "",
      userMessage: payload.error?.user_message ?? "",
      operatorAction: payload.error?.operator_action ?? "",
      requestId: payload.error?.request_id ?? "",
      traceId: payload.error?.trace_id ?? "",
    };
  } catch {
    return {
      message: fallback,
      code: "",
      userMessage: "",
      operatorAction: "",
      requestId: "",
      traceId: "",
    };
  }
}
