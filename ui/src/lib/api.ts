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
  TaskRunsResponse,
  TaskStepsResponse,
  TasksResponse,
  TraceResponse,
  TraceListResponse,
  RetentionRunResponse,
  RetentionRunsResponse,
} from "../types/runtime";

type RequestOptions = {
  authToken?: string;
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

export type TenantUpsertPayload = {
  id: string;
  name: string;
  allowed_providers: string[];
  allowed_models: string[];
  enabled: boolean;
  // Tenant-level layer of the agent_loop system prompt. Empty string
  // is sent over the wire to clear an existing tenant prompt; the
  // backend treats missing/empty the same.
  system_prompt?: string;
};

export type APIKeyUpsertPayload = {
  id: string;
  name: string;
  key: string;
  tenant: string;
  role: string;
  allowed_providers: string[];
  allowed_models: string[];
  enabled: boolean;
};

export type EnabledPayload = {
  id: string;
  enabled: boolean;
};

export type DeletePayload = {
  id: string;
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
  roles?: string[];
  tenants?: string[];
  providers?: string[];
  provider_kinds?: string[];
  models?: string[];
  route_reasons?: string[];
  min_prompt_tokens?: number;
  min_estimated_cost_micros_usd?: number;
  rewrite_model_to?: string;
};

export type RotateAPIKeyPayload = {
  id: string;
  key: string;
};


export type RetentionRunPayload = {
  subsystems: string[];
};

export type CreateChatSessionPayload = {
  title: string;
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
  event_type: string;
  step_id?: string;
  status?: string;
  note?: string;
  data?: Record<string, unknown>;
};

export async function getHealth(): Promise<HealthResponse> {
  return fetchJSON<HealthResponse>("/healthz");
}

export async function getSession(authToken?: string): Promise<SessionResponse> {
  return fetchJSON<SessionResponse>("/v1/whoami", { authToken });
}

// getBootstrapToken asks the gateway to hand back its admin bearer over
// the loopback interface. The endpoint is fenced server-side: it only
// returns 200 when the request comes from a loopback address, the
// origin matches the request host, and the gateway is exposing a
// gateway-managed token. Any other condition yields 403/404 and we
// fall through to the manual TokenGate. Returns the token string on
// success or null on any failure (network, 4xx, empty body) — the
// caller never throws.
export async function getBootstrapToken(): Promise<string | null> {
  try {
    const response = await fetch("/v1/bootstrap-token", { method: "GET" });
    if (!response.ok) return null;
    // Wire shape: { object: "bootstrap_token", data: { token: "…" } }.
    const payload = (await response.json()) as { data?: { token?: string } };
    const token = typeof payload?.data?.token === "string" ? payload.data.token.trim() : "";
    return token === "" ? null : token;
  } catch {
    return null;
  }
}

export async function getModels(authToken?: string): Promise<ModelResponse> {
  return fetchJSON<ModelResponse>("/v1/models", { authToken });
}

export async function getProviders(authToken?: string): Promise<ProviderStatusResponse> {
  return fetchJSON<ProviderStatusResponse>("/admin/providers", { authToken });
}

export async function getRuntimeStats(authToken?: string, isAdmin = false): Promise<RuntimeStatsResponse> {
  const path = isAdmin ? "/admin/runtime/stats" : "/v1/runtime/stats";
  return fetchJSON<RuntimeStatsResponse>(path, { authToken });
}

export async function getMCPCacheStats(authToken?: string): Promise<MCPCacheStatsResponse> {
  return fetchJSON<MCPCacheStatsResponse>("/admin/mcp/cache", { authToken });
}

export async function getProviderPresets(authToken?: string): Promise<ProviderPresetResponse> {
  return fetchJSON<ProviderPresetResponse>("/v1/provider-presets", { authToken });
}

export async function getTrace(requestID: string, authToken?: string): Promise<TraceResponse> {
  return fetchJSON<TraceResponse>(`/v1/traces?request_id=${encodeURIComponent(requestID)}`, { authToken });
}

export async function getRecentTraces(authToken?: string, limit = 50, isAdmin = false): Promise<TraceListResponse> {
  const base = isAdmin ? "/admin/traces" : "/v1/traces";
  return fetchJSON<TraceListResponse>(`${base}?limit=${encodeURIComponent(String(limit))}`, { authToken });
}

export async function getBudget(query = "", authToken?: string): Promise<BudgetStatusResponse> {
  return fetchJSON<BudgetStatusResponse>(`/admin/budget${query}`, { authToken });
}

export async function getAccountSummary(query = "", authToken?: string): Promise<AccountSummaryResponse> {
  return fetchJSON<AccountSummaryResponse>(`/admin/accounts/summary${query}`, { authToken });
}

export async function getChatSessions(authToken?: string, limit = 20, offset = 0): Promise<ChatSessionsResponse> {
  const params = new URLSearchParams({ limit: String(limit) });
  if (offset > 0) params.set("offset", String(offset));
  return fetchJSON<ChatSessionsResponse>(`/v1/chat/sessions?${params.toString()}`, { authToken });
}

export async function createChatSession(payload: CreateChatSessionPayload, authToken?: string): Promise<ChatSessionResponse> {
  return fetchJSON<ChatSessionResponse>("/v1/chat/sessions", { authToken, method: "POST", body: payload });
}

export async function getChatSession(id: string, authToken?: string): Promise<ChatSessionResponse> {
  return fetchJSON<ChatSessionResponse>(`/v1/chat/sessions/${encodeURIComponent(id)}`, { authToken });
}

export async function deleteChatSession(id: string, authToken?: string): Promise<void> {
  await fetchJSON<unknown>(`/v1/chat/sessions/${encodeURIComponent(id)}`, { authToken, method: "DELETE" });
}

export async function updateChatSession(id: string, title: string, authToken?: string): Promise<ChatSessionResponse> {
  return fetchJSON<ChatSessionResponse>(`/v1/chat/sessions/${encodeURIComponent(id)}`, { authToken, method: "PATCH", body: { title } });
}

export async function getRequestLedger(authToken?: string, limit = 20, isAdmin = false): Promise<RequestLedgerResponse> {
  const base = isAdmin ? "/admin/requests" : "/v1/requests";
  return fetchJSON<RequestLedgerResponse>(`${base}?limit=${encodeURIComponent(String(limit))}`, { authToken });
}

export async function resetBudget(payload: Record<string, unknown>, authToken?: string): Promise<BudgetStatusResponse> {
  return fetchJSON<BudgetStatusResponse>("/admin/budget/reset", { authToken, method: "POST", body: payload });
}

export async function topUpBudget(payload: Record<string, unknown>, authToken?: string): Promise<BudgetStatusResponse> {
  return fetchJSON<BudgetStatusResponse>("/admin/budget/topup", { authToken, method: "POST", body: payload });
}

export async function setBudgetLimit(payload: Record<string, unknown>, authToken?: string): Promise<BudgetStatusResponse> {
  return fetchJSON<BudgetStatusResponse>("/admin/budget/limit", { authToken, method: "POST", body: payload });
}

export async function getAdminConfig(authToken?: string): Promise<ConfiguredStateResponse> {
  return fetchJSON<ConfiguredStateResponse>("/admin/control-plane", { authToken });
}

export async function upsertTenant(payload: TenantUpsertPayload, authToken?: string): Promise<unknown> {
  return fetchJSON("/admin/control-plane/tenants", { authToken, method: "POST", body: payload });
}

export async function upsertAPIKey(payload: APIKeyUpsertPayload, authToken?: string): Promise<unknown> {
  return fetchJSON("/admin/control-plane/api-keys", { authToken, method: "POST", body: payload });
}

export async function setTenantEnabled(payload: EnabledPayload, authToken?: string): Promise<unknown> {
  return fetchJSON("/admin/control-plane/tenants/enabled", { authToken, method: "POST", body: payload });
}

export async function deleteTenant(payload: DeletePayload, authToken?: string): Promise<unknown> {
  return fetchJSON("/admin/control-plane/tenants/delete", { authToken, method: "POST", body: payload });
}

export async function setAPIKeyEnabled(payload: EnabledPayload, authToken?: string): Promise<unknown> {
  return fetchJSON("/admin/control-plane/api-keys/enabled", { authToken, method: "POST", body: payload });
}

export async function rotateAPIKey(payload: RotateAPIKeyPayload, authToken?: string): Promise<unknown> {
  return fetchJSON("/admin/control-plane/api-keys/rotate", { authToken, method: "POST", body: payload });
}

export async function deleteAPIKey(payload: DeletePayload, authToken?: string): Promise<unknown> {
  return fetchJSON("/admin/control-plane/api-keys/delete", { authToken, method: "POST", body: payload });
}

export async function upsertPolicyRule(payload: PolicyRuleUpsertPayload, authToken?: string): Promise<unknown> {
  return fetchJSON("/admin/control-plane/policy-rules", { authToken, method: "POST", body: payload });
}

export async function deletePolicyRule(id: string, authToken?: string): Promise<unknown> {
  return fetchJSON("/admin/control-plane/policy-rules/delete", { authToken, method: "POST", body: { id } });
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
  authToken?: string,
): Promise<unknown> {
  return fetchJSON(`/admin/control-plane/providers/${encodeURIComponent(id)}`, { authToken, method: "PATCH", body: patch });
}

export async function upsertPricebookEntry(entry: PricebookEntryUpsertPayload, authToken?: string): Promise<unknown> {
  return fetchJSON("/admin/control-plane/pricebook", { authToken, method: "POST", body: entry });
}

export async function deletePricebookEntry(provider: string, model: string, authToken?: string): Promise<unknown> {
  return fetchJSON("/admin/control-plane/pricebook/delete", { authToken, method: "POST", body: { provider, model } });
}

export async function previewPricebookImport(authToken?: string): Promise<PricebookImportDiffResponse> {
  return fetchJSON<PricebookImportDiffResponse>("/admin/control-plane/pricebook/import/preview", { authToken, method: "POST", body: {} });
}

export async function applyPricebookImport(keys: string[], authToken?: string): Promise<PricebookImportDiffResponse> {
  return fetchJSON<PricebookImportDiffResponse>("/admin/control-plane/pricebook/import/apply", { authToken, method: "POST", body: { keys } });
}

// setProviderAPIKey sets the provider's API key. An empty `key` clears it.
export async function setProviderAPIKey(id: string, key: string, authToken?: string): Promise<unknown> {
  return fetchJSON(`/admin/control-plane/providers/${encodeURIComponent(id)}/api-key`, { authToken, method: "PUT", body: { key } });
}

export async function createProvider(
  params: { name: string; preset_id?: string; custom_name?: string; base_url?: string; api_key?: string; kind: string; protocol: string },
  authToken?: string,
): Promise<unknown> {
  return fetchJSON("/admin/control-plane/providers", { authToken, method: "POST", body: params });
}

export async function deleteProvider(id: string, authToken?: string): Promise<unknown> {
  return fetchJSON(`/admin/control-plane/providers/${encodeURIComponent(id)}`, { authToken, method: "DELETE" });
}

// setProviderBaseURL is a thin wrapper around updateProvider for the
// most common edit surface — local providers rotating their endpoint.
export async function setProviderBaseURL(id: string, baseURL: string, authToken?: string): Promise<unknown> {
  return updateProvider(id, { base_url: baseURL }, authToken);
}

// setProviderName renames a custom (non-preset) provider's display
// label. Rejected by the backend with 400 for preset providers — those
// keep their catalog name and reach for setProviderCustomName instead.
export async function setProviderName(id: string, name: string, authToken?: string): Promise<unknown> {
  return updateProvider(id, { name }, authToken);
}

// setProviderCustomName sets/clears the operator disambiguation label
// that appears alongside name in the providers table. Empty string
// clears it. Allowed for any provider, including presets.
export async function setProviderCustomName(id: string, customName: string, authToken?: string): Promise<unknown> {
  return updateProvider(id, { custom_name: customName }, authToken);
}

// createProvider params include the optional custom_name disambiguator.
// When two instances of the same preset are created, the second's
// custom_name lifts the slug off the colliding default.

export async function runRetention(payload: RetentionRunPayload, authToken?: string): Promise<RetentionRunResponse> {
  return fetchJSON<RetentionRunResponse>("/admin/retention/run", { authToken, method: "POST", body: payload });
}

export async function getRetentionRuns(authToken?: string, limit = 10): Promise<RetentionRunsResponse> {
  return fetchJSON<RetentionRunsResponse>(`/admin/retention/runs?limit=${encodeURIComponent(String(limit))}`, { authToken });
}

export async function getTasks(authToken?: string, limit = 20): Promise<TasksResponse> {
  return fetchJSON<TasksResponse>(`/v1/tasks?limit=${encodeURIComponent(String(limit))}`, { authToken });
}

export async function createTask(payload: CreateTaskPayload, authToken?: string): Promise<TaskResponse> {
  return fetchJSON<TaskResponse>("/v1/tasks", { authToken, method: "POST", body: payload });
}

export async function getTask(taskID: string, authToken?: string): Promise<TaskResponse> {
  return fetchJSON<TaskResponse>(`/v1/tasks/${encodeURIComponent(taskID)}`, { authToken });
}

export async function getTaskRuns(taskID: string, authToken?: string): Promise<TaskRunsResponse> {
  return fetchJSON<TaskRunsResponse>(`/v1/tasks/${encodeURIComponent(taskID)}/runs`, { authToken });
}

export async function deleteTask(taskID: string, authToken?: string): Promise<void> {
  await fetchJSON(`/v1/tasks/${encodeURIComponent(taskID)}`, { authToken, method: "DELETE" });
}

export async function startTask(taskID: string, authToken?: string): Promise<TaskRunResponse> {
  return fetchJSON<TaskRunResponse>(`/v1/tasks/${encodeURIComponent(taskID)}/start`, { authToken, method: "POST" });
}

export async function getTaskApprovals(taskID: string, authToken?: string): Promise<TaskApprovalsResponse> {
  return fetchJSON<TaskApprovalsResponse>(`/v1/tasks/${encodeURIComponent(taskID)}/approvals`, { authToken });
}

export async function getTaskRunSteps(taskID: string, runID: string, authToken?: string): Promise<TaskStepsResponse> {
  return fetchJSON<TaskStepsResponse>(`/v1/tasks/${encodeURIComponent(taskID)}/runs/${encodeURIComponent(runID)}/steps`, { authToken });
}

export async function getTaskRunArtifacts(taskID: string, runID: string, authToken?: string): Promise<TaskArtifactsResponse> {
  return fetchJSON<TaskArtifactsResponse>(`/v1/tasks/${encodeURIComponent(taskID)}/runs/${encodeURIComponent(runID)}/artifacts`, { authToken });
}

export async function getTaskRunEvents(taskID: string, runID: string, afterSequence = 0, authToken?: string): Promise<TaskRunEventsResponse> {
  return fetchJSON<TaskRunEventsResponse>(
    `/v1/tasks/${encodeURIComponent(taskID)}/runs/${encodeURIComponent(runID)}/events?after_sequence=${encodeURIComponent(String(afterSequence))}`,
    { authToken },
  );
}

export async function resolveTaskApproval(taskID: string, approvalID: string, payload: ResolveTaskApprovalPayload, authToken?: string): Promise<void> {
  await fetchJSON(`/v1/tasks/${encodeURIComponent(taskID)}/approvals/${encodeURIComponent(approvalID)}/resolve`, {
    authToken,
    method: "POST",
    body: payload,
  });
}

export async function cancelTaskRun(taskID: string, runID: string, authToken?: string): Promise<void> {
  await fetchJSON(`/v1/tasks/${encodeURIComponent(taskID)}/runs/${encodeURIComponent(runID)}/cancel`, {
    authToken,
    method: "POST",
  });
}

export async function retryTaskRun(taskID: string, runID: string, authToken?: string): Promise<TaskRunResponse> {
  return fetchJSON<TaskRunResponse>(`/v1/tasks/${encodeURIComponent(taskID)}/runs/${encodeURIComponent(runID)}/retry`, {
    authToken,
    method: "POST",
    body: {},
  });
}

export async function resumeTaskRun(taskID: string, runID: string, authToken?: string): Promise<TaskRunResponse> {
  return fetchJSON<TaskRunResponse>(`/v1/tasks/${encodeURIComponent(taskID)}/runs/${encodeURIComponent(runID)}/resume`, {
    authToken,
    method: "POST",
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
  budgetMicrosUSD: number,
  authToken?: string,
): Promise<TaskRunResponse> {
  return fetchJSON<TaskRunResponse>(`/v1/tasks/${encodeURIComponent(taskID)}/runs/${encodeURIComponent(runID)}/resume`, {
    authToken,
    method: "POST",
    body: { budget_micros_usd: budgetMicrosUSD },
  });
}

// retryTaskRunFromTurn re-runs an agent_loop run starting at turn N
// with the prior conversation preserved up to (but not including)
// that turn's assistant message. Returns the newly-created run.
export async function retryTaskRunFromTurn(taskID: string, runID: string, turn: number, authToken?: string): Promise<TaskRunResponse> {
  return fetchJSON<TaskRunResponse>(`/v1/tasks/${encodeURIComponent(taskID)}/runs/${encodeURIComponent(runID)}/retry-from-turn`, {
    authToken,
    method: "POST",
    body: { turn },
  });
}

export async function appendTaskRunEvent(taskID: string, runID: string, payload: AppendTaskRunEventPayload, authToken?: string): Promise<void> {
  await fetchJSON(`/v1/tasks/${encodeURIComponent(taskID)}/runs/${encodeURIComponent(runID)}/events`, {
    authToken,
    method: "POST",
    body: payload,
  });
}

export async function streamTaskRun(
  taskID: string,
  runID: string,
  authToken: string | undefined,
  onEvent: (event: { event: string; payload: TaskRunStreamEventResponse }) => void,
  afterSequence = 0,
  signal?: AbortSignal,
): Promise<void> {
  const response = await fetchWithNetworkError(
    `/v1/tasks/${encodeURIComponent(taskID)}/runs/${encodeURIComponent(runID)}/stream?after_sequence=${encodeURIComponent(String(afterSequence))}`,
    { ...buildRequestOptions({ authToken }), signal },
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
  authToken: string | undefined,
  onChunk: (delta: string) => void,
): Promise<{ headers: RuntimeHeaders; finishReason: string; toolCalls: StreamedToolCall[] }> {
  const response = await fetchWithNetworkError(
    "/v1/chat/completions",
    buildRequestOptions({ authToken, method: "POST", body: { ...payload, stream: true } }),
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
  authToken?: string,
): Promise<{ data: ChatResponse; headers: RuntimeHeaders }> {
  const response = await fetchWithNetworkError("/v1/chat/completions", buildRequestOptions({ authToken, method: "POST", body: payload }));
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
  if (options.authToken) {
    headers.set("Authorization", `Bearer ${options.authToken}`);
  }
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
