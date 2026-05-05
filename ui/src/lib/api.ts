import type {
  BudgetStatusResponse,
  AccountSummaryResponse,
  ChatResponse,
  ChatSessionResponse,
  ChatSessionsResponse,
  ConfiguredStateResponse,
  HealthResponse,
  MCPCacheStatsResponse,
  ModelResponse,
  PricebookEntryUpsertPayload,
  PricebookImportDiffResponse,
  ProviderPresetResponse,
  AgentAdapterProbeResponse,
  AgentAdapterResponse,
  AgentChatApprovalRequestedEvent,
  AgentChatApprovalResolvedEvent,
  AgentChatApprovalResponse,
  AgentChatApprovalsResponse,
  AgentChatChangedFileDiffResponse,
  AgentChatChangedFilesResponse,
  AgentChatGrantsResponse,
  AgentChatRevertResponse,
  AgentChatSessionResponse,
  AgentChatSessionsResponse,
  AgentChatStreamEvent,
  WorkspaceDialogResponse,
  ProviderStatusResponse,
  RuntimeStatsResponse,
  RequestLedgerResponse,
  RuntimeHeaders,
  SessionResponse,
  TaskApprovalsResponse,
  TaskArtifactsResponse,
  TaskResponse,
  TaskRunResponse,
  TaskRunEventsResponse,
  TaskRunStreamEventResponse,
  TaskPatchResponse,
  TaskRunsResponse,
  TaskStepsResponse,
  TasksResponse,
  TraceResponse,
  TraceListResponse,
  RetentionRunResponse,
  RetentionRunsResponse,
  SemanticCacheStatusResponse,
  SemanticCacheEntriesResponse,
} from "../types/runtime";

type RequestOptions = {
  method?: "GET" | "POST" | "PATCH" | "PUT" | "DELETE";
  body?: unknown;
};

type ErrorPayload = {
  error?: {
    type?: string;
    message?: string;
  };
};

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
      tool_calls?: Array<{ id: string; type: string; function: { name: string; arguments: string } }>;
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
// ControlPlanePolicyRuleRecord wire shape exactly. Empty arrays /
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
  title: string;
};

export type CreateAgentChatSessionPayload = {
  title?: string;
  adapter_id: string;
  workspace: string;
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
  return fetchJSON<SessionResponse>("/v1/whoami");
}

export async function getModels(): Promise<ModelResponse> {
  return fetchJSON<ModelResponse>("/v1/models");
}

export async function getProviders(): Promise<ProviderStatusResponse> {
  return fetchJSON<ProviderStatusResponse>("/admin/providers");
}

export async function getRuntimeStats(): Promise<RuntimeStatsResponse> {
  return fetchJSON<RuntimeStatsResponse>("/admin/runtime/stats");
}

export async function getMCPCacheStats(): Promise<MCPCacheStatsResponse> {
  return fetchJSON<MCPCacheStatsResponse>("/admin/mcp/cache");
}

export async function getSemanticCacheStatus(): Promise<SemanticCacheStatusResponse> {
  return fetchJSON<SemanticCacheStatusResponse>("/admin/semantic-cache");
}

export async function listSemanticCacheEntries(
  params: { limit?: number; offset?: number },
): Promise<SemanticCacheEntriesResponse> {
  const q = new URLSearchParams();
  if (params.limit !== undefined) q.set("limit", String(params.limit));
  if (params.offset !== undefined) q.set("offset", String(params.offset));
  const qs = q.toString();
  return fetchJSON<SemanticCacheEntriesResponse>(
    `/admin/semantic-cache/entries${qs ? `?${qs}` : ""}`,
  );
}

export async function getProviderPresets(): Promise<ProviderPresetResponse> {
  return fetchJSON<ProviderPresetResponse>("/v1/provider-presets");
}

export async function getAgentAdapters(): Promise<AgentAdapterResponse> {
  return fetchJSON<AgentAdapterResponse>("/v1/agent-adapters");
}

// probeAgentAdapter re-runs discovery for one adapter and performs the
// end-to-end ACP health probe. The response includes both the fresh list row
// and the deeper handshake result so Settings can update in place.
export async function probeAgentAdapter(adapterID: string): Promise<AgentAdapterProbeResponse> {
  return fetchJSON<AgentAdapterProbeResponse>(
    `/v1/agent-adapters/${encodeURIComponent(adapterID)}/probe`,
    { method: "POST" },
  );
}

export async function refreshAgentAdapterLauncher(adapterID: string): Promise<AgentAdapterResponse> {
  return fetchJSON<AgentAdapterResponse>(
    `/v1/agent-adapters/${encodeURIComponent(adapterID)}/refresh-launcher`,
    { method: "POST" },
  );
}

export async function getTrace(requestID: string): Promise<TraceResponse> {
  return fetchJSON<TraceResponse>(`/v1/traces?request_id=${encodeURIComponent(requestID)}`);
}

export async function getRecentTraces(limit = 50): Promise<TraceListResponse> {
  return fetchJSON<TraceListResponse>(`/admin/traces?limit=${encodeURIComponent(String(limit))}`);
}

export async function getBudget(query = ""): Promise<BudgetStatusResponse> {
  return fetchJSON<BudgetStatusResponse>(`/admin/budget${query}`);
}

export async function getAccountSummary(query = ""): Promise<AccountSummaryResponse> {
  return fetchJSON<AccountSummaryResponse>(`/admin/accounts/summary${query}`);
}

export async function getChatSessions(limit = 20, offset = 0): Promise<ChatSessionsResponse> {
  const params = new URLSearchParams({ limit: String(limit) });
  if (offset > 0) params.set("offset", String(offset));
  return fetchJSON<ChatSessionsResponse>(`/v1/chat/sessions?${params.toString()}`);
}

export async function createChatSession(payload: CreateChatSessionPayload): Promise<ChatSessionResponse> {
  return fetchJSON<ChatSessionResponse>("/v1/chat/sessions", { method: "POST", body: payload });
}

export async function getChatSession(id: string): Promise<ChatSessionResponse> {
  return fetchJSON<ChatSessionResponse>(`/v1/chat/sessions/${encodeURIComponent(id)}`);
}

export async function deleteChatSession(id: string): Promise<void> {
  await fetchJSON<unknown>(`/v1/chat/sessions/${encodeURIComponent(id)}`, { method: "DELETE" });
}

export async function updateChatSession(id: string, title: string): Promise<ChatSessionResponse> {
  return fetchJSON<ChatSessionResponse>(`/v1/chat/sessions/${encodeURIComponent(id)}`, { method: "PATCH", body: { title } });
}

export async function getAgentChatSessions(): Promise<AgentChatSessionsResponse> {
  return fetchJSON<AgentChatSessionsResponse>("/v1/agent-chat/sessions");
}

export async function createAgentChatSession(payload: CreateAgentChatSessionPayload): Promise<AgentChatSessionResponse> {
  return fetchJSON<AgentChatSessionResponse>("/v1/agent-chat/sessions", { method: "POST", body: payload });
}

export async function getAgentChatSession(id: string): Promise<AgentChatSessionResponse> {
  return fetchJSON<AgentChatSessionResponse>(`/v1/agent-chat/sessions/${encodeURIComponent(id)}`);
}

export async function deleteAgentChatSession(id: string): Promise<void> {
  await fetchJSON<unknown>(`/v1/agent-chat/sessions/${encodeURIComponent(id)}`, { method: "DELETE" });
}

export async function cancelAgentChatSession(id: string): Promise<AgentChatSessionResponse> {
  return fetchJSON<AgentChatSessionResponse>(`/v1/agent-chat/sessions/${encodeURIComponent(id)}/cancel`, { method: "POST", body: {} });
}

export async function createAgentChatMessage(id: string, content: string): Promise<AgentChatSessionResponse> {
  return fetchJSON<AgentChatSessionResponse>(`/v1/agent-chat/sessions/${encodeURIComponent(id)}/messages`, { method: "POST", body: { content } });
}

export async function listAgentChatMessageFiles(sessionID: string, messageID: string): Promise<AgentChatChangedFilesResponse> {
  return fetchJSON<AgentChatChangedFilesResponse>(
    `/v1/agent-chat/sessions/${encodeURIComponent(sessionID)}/messages/${encodeURIComponent(messageID)}/files`,
  );
}

export async function getAgentChatMessageFileDiff(sessionID: string, messageID: string, path: string): Promise<AgentChatChangedFileDiffResponse> {
  return fetchJSON<AgentChatChangedFileDiffResponse>(
    `/v1/agent-chat/sessions/${encodeURIComponent(sessionID)}/messages/${encodeURIComponent(messageID)}/files/${encodeURIComponent(path)}`,
  );
}

export async function revertAgentChatMessageFiles(sessionID: string, messageID: string, paths: string[] = []): Promise<AgentChatRevertResponse> {
  return fetchJSON<AgentChatRevertResponse>(
    `/v1/agent-chat/sessions/${encodeURIComponent(sessionID)}/messages/${encodeURIComponent(messageID)}/revert`,
    { method: "POST", body: { paths } },
  );
}

// streamAgentChatSession reads the per-session SSE feed and dispatches
// each event to the consumer as a typed AgentChatStreamEvent. The Type
// discriminator on the wire (`session_update`, `approval.requested`,
// `approval.resolved`) maps directly onto the union members. Unknown
// event names are silently ignored — frontends are forward-compatible
// with new event kinds added on the backend.
export async function streamAgentChatSession(
  id: string,
  onEvent: (event: AgentChatStreamEvent) => void,
  signal?: AbortSignal,
): Promise<void> {
  const response = await fetchWithNetworkError(
    `/v1/agent-chat/sessions/${encodeURIComponent(id)}/stream`,
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
    const dispatched = dispatchAgentChatStreamEvent(eventName, raw);
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

// dispatchAgentChatStreamEvent maps a wire SSE event name + JSON
// payload onto the typed AgentChatStreamEvent union. Returns null for
// unknown event types so the consumer doesn't see noise. Exported for
// unit tests.
export function dispatchAgentChatStreamEvent(
  eventName: string,
  rawData: string,
): AgentChatStreamEvent | null {
  switch (eventName) {
    case "session_update":
    case "snapshot":
    case "done":
    case "message":
      return { type: "session_update", payload: JSON.parse(rawData) as AgentChatSessionResponse };
    case "approval.requested":
      return { type: "approval.requested", payload: JSON.parse(rawData) as AgentChatApprovalRequestedEvent };
    case "approval.resolved":
      return { type: "approval.resolved", payload: JSON.parse(rawData) as AgentChatApprovalResolvedEvent };
    default:
      return null;
  }
}

// ─── Agent-chat approvals ──────────────────────────────────────────────────────

// listAgentChatApprovals fetches approvals for a session. Pass
// status="pending" to scope to the operator's review queue.
export async function listAgentChatApprovals(
  sessionID: string,
  status?: string,
): Promise<AgentChatApprovalsResponse> {
  const qs = status ? `?status=${encodeURIComponent(status)}` : "";
  return fetchJSON<AgentChatApprovalsResponse>(
    `/v1/agent-chat/sessions/${encodeURIComponent(sessionID)}/approvals${qs}`,
  );
}

export async function getAgentChatApproval(
  sessionID: string,
  approvalID: string,
): Promise<AgentChatApprovalResponse> {
  return fetchJSON<AgentChatApprovalResponse>(
    `/v1/agent-chat/sessions/${encodeURIComponent(sessionID)}/approvals/${encodeURIComponent(approvalID)}`,
  );
}

export type ResolveAgentChatApprovalPayload = {
  decision: "approve" | "deny";
  scope: string;
  selected_option?: string;
  note?: string;
};

export async function resolveAgentChatApproval(
  sessionID: string,
  approvalID: string,
  payload: ResolveAgentChatApprovalPayload,
): Promise<AgentChatApprovalResponse> {
  return fetchJSON<AgentChatApprovalResponse>(
    `/v1/agent-chat/sessions/${encodeURIComponent(sessionID)}/approvals/${encodeURIComponent(approvalID)}/resolve`,
    { method: "POST", body: payload },
  );
}

export async function cancelAgentChatApproval(
  sessionID: string,
  approvalID: string,
): Promise<AgentChatApprovalResponse> {
  return fetchJSON<AgentChatApprovalResponse>(
    `/v1/agent-chat/sessions/${encodeURIComponent(sessionID)}/approvals/${encodeURIComponent(approvalID)}/cancel`,
    { method: "POST", body: {} },
  );
}

export type AgentChatGrantFilter = {
  adapter_id?: string;
  scope?: string;
  tool_kind?: string;
};

export async function listAgentChatGrants(
  filter: AgentChatGrantFilter = {},
): Promise<AgentChatGrantsResponse> {
  const params = new URLSearchParams();
  if (filter.adapter_id) params.set("adapter_id", filter.adapter_id);
  if (filter.scope) params.set("scope", filter.scope);
  if (filter.tool_kind) params.set("tool_kind", filter.tool_kind);
  const qs = params.toString();
  return fetchJSON<AgentChatGrantsResponse>(`/v1/agent-chat/grants${qs ? `?${qs}` : ""}`);
}

export async function deleteAgentChatGrant(grantID: string): Promise<void> {
  await fetchJSON<unknown>(`/v1/agent-chat/grants/${encodeURIComponent(grantID)}`, { method: "DELETE" });
}

export async function chooseWorkspaceDirectory(): Promise<WorkspaceDialogResponse> {
  return fetchJSON<WorkspaceDialogResponse>("/v1/workspace-dialog", { method: "POST", body: {} });
}

export async function getRequestLedger(limit = 20): Promise<RequestLedgerResponse> {
  return fetchJSON<RequestLedgerResponse>(`/admin/requests?limit=${encodeURIComponent(String(limit))}`);
}

export async function resetBudget(payload: Record<string, unknown>): Promise<BudgetStatusResponse> {
  return fetchJSON<BudgetStatusResponse>("/admin/budget/reset", { method: "POST", body: payload });
}

export async function topUpBudget(payload: Record<string, unknown>): Promise<BudgetStatusResponse> {
  return fetchJSON<BudgetStatusResponse>("/admin/budget/topup", { method: "POST", body: payload });
}

export async function setBudgetLimit(payload: Record<string, unknown>): Promise<BudgetStatusResponse> {
  return fetchJSON<BudgetStatusResponse>("/admin/budget/limit", { method: "POST", body: payload });
}

export async function getControlPlaneConfig(): Promise<ConfiguredStateResponse> {
  return fetchJSON<ConfiguredStateResponse>("/admin/control-plane");
}

export async function upsertPolicyRule(payload: PolicyRuleUpsertPayload): Promise<unknown> {
  return fetchJSON("/admin/control-plane/policy-rules", { method: "POST", body: payload });
}

export async function deletePolicyRule(id: string): Promise<unknown> {
  return fetchJSON("/admin/control-plane/policy-rules/delete", { method: "POST", body: { id } });
}

// updateProvider applies a partial update to an existing provider record.
// Editable fields:
//   - base_url:    any provider (repoint endpoint)
//   - name:        custom providers only (preset names are fixed)
//   - custom_name: any provider (operator disambiguation label)
// Credentials live behind PUT /providers/{id}/api-key, not here.
export async function updateProvider(
  id: string,
  patch: { base_url?: string; name?: string; custom_name?: string }
): Promise<unknown> {
  return fetchJSON(`/admin/control-plane/providers/${encodeURIComponent(id)}`, { method: "PATCH", body: patch });
}

export async function upsertPricebookEntry(entry: PricebookEntryUpsertPayload): Promise<unknown> {
  return fetchJSON("/admin/control-plane/pricebook", { method: "POST", body: entry });
}

export async function deletePricebookEntry(provider: string, model: string): Promise<unknown> {
  return fetchJSON("/admin/control-plane/pricebook/delete", { method: "POST", body: { provider, model } });
}

export async function previewPricebookImport(): Promise<PricebookImportDiffResponse> {
  return fetchJSON<PricebookImportDiffResponse>("/admin/control-plane/pricebook/import/preview", { method: "POST", body: {} });
}

export async function applyPricebookImport(keys: string[]): Promise<PricebookImportDiffResponse> {
  return fetchJSON<PricebookImportDiffResponse>("/admin/control-plane/pricebook/import/apply", { method: "POST", body: { keys } });
}

// setProviderAPIKey sets the provider's API key. An empty `key` clears it.
export async function setProviderAPIKey(id: string, key: string): Promise<unknown> {
  return fetchJSON(`/admin/control-plane/providers/${encodeURIComponent(id)}/api-key`, { method: "PUT", body: { key } });
}

export async function createProvider(
  params: { name: string; preset_id?: string; custom_name?: string; base_url?: string; api_key?: string; kind: string; protocol: string }
): Promise<unknown> {
  return fetchJSON("/admin/control-plane/providers", { method: "POST", body: params });
}

export async function deleteProvider(id: string): Promise<unknown> {
  return fetchJSON(`/admin/control-plane/providers/${encodeURIComponent(id)}`, { method: "DELETE" });
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
  return fetchJSON<RetentionRunResponse>("/admin/retention/run", { method: "POST", body: payload });
}

export async function getRetentionRuns(limit = 10): Promise<RetentionRunsResponse> {
  return fetchJSON<RetentionRunsResponse>(`/admin/retention/runs?limit=${encodeURIComponent(String(limit))}`);
}

export async function getTasks(limit = 20): Promise<TasksResponse> {
  return fetchJSON<TasksResponse>(`/v1/tasks?limit=${encodeURIComponent(String(limit))}`);
}

export async function createTask(payload: CreateTaskPayload): Promise<TaskResponse> {
  return fetchJSON<TaskResponse>("/v1/tasks", { method: "POST", body: payload });
}

export async function getTask(taskID: string): Promise<TaskResponse> {
  return fetchJSON<TaskResponse>(`/v1/tasks/${encodeURIComponent(taskID)}`);
}

export async function getTaskRuns(taskID: string): Promise<TaskRunsResponse> {
  return fetchJSON<TaskRunsResponse>(`/v1/tasks/${encodeURIComponent(taskID)}/runs`);
}

export async function deleteTask(taskID: string): Promise<void> {
  await fetchJSON(`/v1/tasks/${encodeURIComponent(taskID)}`, { method: "DELETE" });
}

export async function startTask(taskID: string): Promise<TaskRunResponse> {
  return fetchJSON<TaskRunResponse>(`/v1/tasks/${encodeURIComponent(taskID)}/start`, { method: "POST" });
}

export async function getTaskApprovals(taskID: string): Promise<TaskApprovalsResponse> {
  return fetchJSON<TaskApprovalsResponse>(`/v1/tasks/${encodeURIComponent(taskID)}/approvals`);
}

export async function getTaskRunSteps(taskID: string, runID: string): Promise<TaskStepsResponse> {
  return fetchJSON<TaskStepsResponse>(`/v1/tasks/${encodeURIComponent(taskID)}/runs/${encodeURIComponent(runID)}/steps`);
}

export async function getTaskRunArtifacts(taskID: string, runID: string): Promise<TaskArtifactsResponse> {
  return fetchJSON<TaskArtifactsResponse>(`/v1/tasks/${encodeURIComponent(taskID)}/runs/${encodeURIComponent(runID)}/artifacts`);
}

export async function applyTaskRunPatch(taskID: string, runID: string, artifactID: string): Promise<TaskPatchResponse> {
  return fetchJSON<TaskPatchResponse>(`/v1/tasks/${encodeURIComponent(taskID)}/runs/${encodeURIComponent(runID)}/patches/${encodeURIComponent(artifactID)}/apply`, { method: "POST" });
}

export async function revertTaskRunPatch(taskID: string, runID: string, artifactID: string): Promise<TaskPatchResponse> {
  return fetchJSON<TaskPatchResponse>(`/v1/tasks/${encodeURIComponent(taskID)}/runs/${encodeURIComponent(runID)}/patches/${encodeURIComponent(artifactID)}/revert`, { method: "POST" });
}

export async function getTaskRunEvents(taskID: string, runID: string, afterSequence = 0): Promise<TaskRunEventsResponse> {
  return fetchJSON<TaskRunEventsResponse>(
    `/v1/tasks/${encodeURIComponent(taskID)}/runs/${encodeURIComponent(runID)}/events?after_sequence=${encodeURIComponent(String(afterSequence))}`,
  );
}

export async function resolveTaskApproval(taskID: string, approvalID: string, payload: ResolveTaskApprovalPayload): Promise<void> {
  await fetchJSON(`/v1/tasks/${encodeURIComponent(taskID)}/approvals/${encodeURIComponent(approvalID)}/resolve`, { method: "POST",
    body: payload,
  });
}

export async function cancelTaskRun(taskID: string, runID: string): Promise<void> {
  await fetchJSON(`/v1/tasks/${encodeURIComponent(taskID)}/runs/${encodeURIComponent(runID)}/cancel`, { method: "POST",
  });
}

export async function retryTaskRun(taskID: string, runID: string): Promise<TaskRunResponse> {
  return fetchJSON<TaskRunResponse>(`/v1/tasks/${encodeURIComponent(taskID)}/runs/${encodeURIComponent(runID)}/retry`, { method: "POST",
    body: {},
  });
}

export async function resumeTaskRun(taskID: string, runID: string): Promise<TaskRunResponse> {
  return fetchJSON<TaskRunResponse>(`/v1/tasks/${encodeURIComponent(taskID)}/runs/${encodeURIComponent(runID)}/resume`, { method: "POST",
    body: {},
  });
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
  budgetMicrosUSD: number
): Promise<TaskRunResponse> {
  return fetchJSON<TaskRunResponse>(`/v1/tasks/${encodeURIComponent(taskID)}/runs/${encodeURIComponent(runID)}/resume`, { method: "POST",
    body: { budget_micros_usd: budgetMicrosUSD },
  });
}

// retryTaskRunFromTurn re-runs an agent_loop run starting at turn N
// with the prior conversation preserved up to (but not including)
// that turn's assistant message. Returns the newly-created run.
// The optional reason is stored in the run.resumed_from_event event so operators
// can annotate why they branched from a particular turn.
export async function retryTaskRunFromTurn(taskID: string, runID: string, turn: number, reason?: string): Promise<TaskRunResponse> {
  return fetchJSON<TaskRunResponse>(`/v1/tasks/${encodeURIComponent(taskID)}/runs/${encodeURIComponent(runID)}/retry-from-turn`, {
    method: "POST",
    body: { turn, reason: reason ?? "" },
  });
}

export async function appendTaskRunEvent(taskID: string, runID: string, payload: AppendTaskRunEventPayload): Promise<void> {
  await fetchJSON(`/v1/tasks/${encodeURIComponent(taskID)}/runs/${encodeURIComponent(runID)}/events`, { method: "POST",
    body: payload,
  });
}

export async function streamTaskRun(
  taskID: string,
  runID: string,
  onEvent: (event: { event: string; payload: TaskRunStreamEventResponse }) => void,
  afterSequence = 0,
  signal?: AbortSignal,
): Promise<void> {
  const response = await fetchWithNetworkError(
    `/v1/tasks/${encodeURIComponent(taskID)}/runs/${encodeURIComponent(runID)}/stream?after_sequence=${encodeURIComponent(String(afterSequence))}`,
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
  payload: ChatCompletionPayload
): Promise<{ data: ChatResponse; headers: RuntimeHeaders }> {
  const response = await fetchWithNetworkError("/v1/chat/completions", buildRequestOptions({ method: "POST", body: payload }));
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
    cache: response.headers.get("X-Runtime-Cache") ?? "",
    cacheType: response.headers.get("X-Runtime-Cache-Type") ?? "",
    semanticStrategy: response.headers.get("X-Runtime-Semantic-Strategy") ?? "",
    semanticIndex: response.headers.get("X-Runtime-Semantic-Index") ?? "",
    semanticSimilarity: response.headers.get("X-Runtime-Semantic-Similarity") ?? "",
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
  constructor(message: string, status: number, code = "") {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
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
  if (message === "Load failed" || message === "Failed to fetch" || message.includes("NetworkError")) {
    return `Gateway request failed to load (${url}). Check that the gateway is running on http://127.0.0.1:8765 and that the Vite dev proxy is active.`;
  }
  return `Gateway request failed (${url}): ${message}`;
}

async function apiError(response: Response, fallback: string): Promise<ApiError> {
  const payload = await errorPayload(response, fallback);
  return new ApiError(payload.message, response.status, payload.code);
}

async function errorPayload(response: Response, fallback: string): Promise<{ message: string; code: string }> {
  try {
    const payload = (await response.json()) as ErrorPayload;
    return {
      message: payload.error?.message ?? fallback,
      code: payload.error?.type ?? "",
    };
  } catch {
    return { message: fallback, code: "" };
  }
}
