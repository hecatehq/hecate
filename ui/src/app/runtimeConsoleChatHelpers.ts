import type { ChatMessage } from "../lib/api";
import type {
  AgentChatApprovalRecord,
  AgentChatSessionRecord,
  AgentChatSessionsResponse,
  ChatResponse,
  ChatSessionRecord,
  ChatSessionsResponse,
  ModelResponse,
  PendingAgentApproval,
  ProviderFilter,
  ProviderPresetRecord,
  ProviderStatusResponse,
  RuntimeHeaders,
} from "../types/runtime";

// humanizeChatError translates raw gateway/provider errors into something
// an operator can act on. The backend's "api key is required for cloud
// provider X when stub mode is disabled" carries internal vocabulary
// that's noise to the user; they just need to know they should add a key.
export function humanizeChatError(raw: string): string {
  const apiKeyPattern = /api key is required for cloud provider (\S+)/i;
  const m = raw.match(apiKeyPattern);
  if (m) {
    return `${m[1]} has no API key. Open the Providers tab and add one.`;
  }
  return raw;
}

export function deriveChatSessionTitle(message: string): string {
  const normalized = message.trim().replace(/\s+/g, " ");
  if (!normalized) {
    return "New chat";
  }
  if (normalized.length <= 48) {
    return normalized;
  }
  return `${normalized.slice(0, 45)}...`;
}

export function buildMessagesForSubmission(activeSession: ChatSessionRecord | null, message: string, systemPrompt = ""): ChatMessage[] {
  // Replay is now a near-trivial transform: the persisted message
  // stream is already in submission order. We carry content_blocks
  // and tool_error through verbatim so Anthropic-aware history
  // survives cross-provider resubmission.
  const history: ChatMessage[] = (activeSession?.messages ?? [])
    .filter((m) => m.id && !m.id.startsWith("pending-"))
    .map((m) => persistedMessageToChatMessage(m));
  const prefix: ChatMessage[] = systemPrompt.trim() ? [{ role: "system", content: systemPrompt.trim() }] : [];
  return [...prefix, ...history, { role: "user", content: message }];
}

export function buildAssistantToolCallMessage(
  content: string,
  toolCalls: Array<{ id: string; name: string; arguments: string }>,
): ChatMessage {
  return {
    role: "assistant",
    content: content || null,
    tool_calls: toolCalls.map((tc) => ({
      id: tc.id,
      type: "function",
      function: { name: tc.name, arguments: tc.arguments },
    })),
  };
}

export function buildSyntheticChatResult(headers: RuntimeHeaders, selectedModel: string, content: string): ChatResponse {
  return {
    id: headers.requestId || "stream",
    model: headers.resolvedModel || selectedModel,
    choices: [{ index: 0, message: { role: "assistant", content }, finish_reason: "stop" }],
    usage: { prompt_tokens: 0, completion_tokens: 0, total_tokens: 0 },
  };
}

export function defaultModelForProvider(provider: ProviderFilter, models: ModelResponse["data"], providers: ProviderStatusResponse["data"], presets: ProviderPresetRecord[]): string {
  if (provider === "auto") {
    return "";
  }

  const providerRecord = providers.find((entry) => entry.name === provider);
  const scopedModels = models.filter((entry) => entry.metadata?.provider === provider);
  const preset = presets.find((entry) => entry.id === provider);
  if (providerRecord?.default_model) {
    return providerRecord.default_model;
  }

  if (providerRecord) {
    return scopedModels.find((entry) => entry.metadata?.default)?.id ?? scopedModels[0]?.id ?? providerRecord.models?.[0] ?? "";
  }

  return scopedModels.find((entry) => entry.metadata?.default)?.id ?? scopedModels[0]?.id ?? preset?.default_model ?? "";
}

export function isModelValidForProvider(model: string, provider: ProviderFilter, models: ModelResponse["data"], providers: ProviderStatusResponse["data"], presets: ProviderPresetRecord[]): boolean {
  if (!model || provider === "auto") {
    return true;
  }

  if (models.some((entry) => entry.id === model && entry.metadata?.provider === provider)) {
    return true;
  }

  const providerRecord = providers.find((entry) => entry.name === provider);
  if (providerRecord?.default_model === model || providerRecord?.models?.includes(model)) {
    return true;
  }
  if (providerRecord) {
    return false;
  }

  const preset = presets.find((entry) => entry.id === provider);
  return preset?.default_model === model;
}

export function renderChatSessionSummary(session: ChatSessionRecord): ChatSessionsResponse["data"][number] {
  const messages = session.messages ?? [];
  const calls = session.provider_calls ?? [];
  const lastCall = calls[calls.length - 1];
  return {
    id: session.id,
    title: session.title,
    message_count: messages.length,
    provider_call_count: calls.length,
    created_at: session.created_at,
    updated_at: session.updated_at,
    last_model: lastCall?.model,
    last_provider: lastCall?.provider,
    last_cost_usd: lastCall?.cost_usd,
    last_request_id: lastCall?.request_id,
  };
}

export function approvalRecordToPending(row: AgentChatApprovalRecord): PendingAgentApproval {
  return {
    approval_id: row.id,
    session_id: row.session_id,
    adapter_id: row.adapter_id,
    tool_kind: row.tool_kind,
    tool_name: row.tool_name,
    scope_choices: row.scope_choices,
    created_at: row.created_at,
    expires_at: row.expires_at,
  };
}

export function renderAgentChatSessionSummary(session: AgentChatSessionRecord): AgentChatSessionsResponse["data"][number] {
  return {
    id: session.id,
    title: session.title,
    runtime_kind: session.runtime_kind,
    adapter_id: session.adapter_id,
    driver_kind: session.driver_kind,
    native_session_id: session.native_session_id,
    task_id: session.task_id,
    latest_run_id: session.latest_run_id,
    provider: session.provider,
    model: session.model,
    capabilities: session.capabilities,
    workspace: session.workspace,
    workspace_branch: session.workspace_branch,
    status: session.status,
    message_count: session.messages?.length ?? 0,
    created_at: session.created_at,
    updated_at: session.updated_at,
  };
}

function persistedMessageToChatMessage(m: ChatSessionRecord["messages"] extends (infer U)[] | undefined ? U : never): ChatMessage {
  const ext = {
    ...(m.content_blocks ? { content_blocks: m.content_blocks } : {}),
    ...(m.tool_error ? { tool_error: m.tool_error } : {}),
  };
  if (m.role === "assistant") {
    return {
      role: "assistant",
      content: m.content,
      ...(m.tool_calls && m.tool_calls.length > 0 ? { tool_calls: m.tool_calls } : {}),
      ...ext,
    } as ChatMessage;
  }
  if (m.role === "tool") {
    return {
      role: "tool",
      content: m.content ?? "",
      tool_call_id: m.tool_call_id ?? "",
      ...ext,
    } as ChatMessage;
  }
  return {
    role: m.role === "system" ? "system" : "user",
    content: m.content ?? "",
    ...ext,
  } as ChatMessage;
}
