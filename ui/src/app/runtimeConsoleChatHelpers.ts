import type { ChatMessage } from "../lib/api";
import type { RuntimeHeaders } from "../types/runtime";
import type { ModelResponse } from "../types/model";
import type {
  ConfiguredStateResponse,
  ProviderFilter,
  ProviderPresetRecord,
  ProviderStatusResponse,
} from "../types/provider";
import type {
  ChatApprovalRecord,
  ChatResponse,
  ChatSessionRecord,
  ChatSessionsResponse,
  PendingAgentApproval,
} from "../types/chat";

// humanizeChatError translates raw gateway/provider errors into something
// an operator can act on. The backend's "api key is required for cloud
// provider X when stub mode is disabled" carries internal vocabulary
// that's noise to the user; they just need to know they should add a key.
export function humanizeChatError(raw: string): string {
  const apiKeyPattern = /api key is required for cloud provider (\S+)/i;
  const m = raw.match(apiKeyPattern);
  if (m) {
    return `${m[1]} has no API key. Open Connections and add one.`;
  }
  if (/agent_session_busy|already running for this chat session/i.test(raw)) {
    return "Hecate Chat is still working on this task. Open the task, resolve approval, or stop it before sending another message.";
  }
  if (/workspace (is )?(required|missing)|choose a workspace|workspace path/i.test(raw)) {
    return "Choose a workspace before using Hecate Chat tools or External Agent.";
  }
  if (/tool.?calling.*(unknown|none|unavailable|not supported)|model.*does not support.*tools?/i.test(raw)) {
    return "This model is not marked as tool-capable. Turn tools off, test it, or enable tools in Connections → Model capabilities.";
  }
  const explicitModel = raw.match(/no provider supports explicit model ["“]?([^"”]+)["”]?/i);
  if (explicitModel) {
    return `No configured provider can route to ${explicitModel[1]}. Choose another model or open Connections to repair provider readiness.`;
  }
  if (/no routable model|no route/i.test(raw)) {
    return "No routable model is available. Choose another model or open Connections to add a provider, discover models, or check provider health.";
  }
  if (/authentication required|please (run .*login|log in)|not signed in|unauthenticated/i.test(raw)) {
    return "The selected runtime is not signed in. Open Connections to repair or test readiness.";
  }
  if (/credit balance is too low|billing|payment required|insufficient credits/i.test(raw)) {
    return "The selected runtime reported a billing or credit problem. Check its account, subscription, or API key balance.";
  }
  if (/connection refused|econnrefused|connect: connection refused/i.test(raw)) {
    return "The selected provider is not reachable. Start the local provider app or check its endpoint URL.";
  }
  const upstreamStatus = raw.match(/upstream returned (\d{3})/i);
  if (upstreamStatus) {
    if (upstreamStatus[1] === "401" || upstreamStatus[1] === "403") {
      return `The selected provider rejected the request with HTTP ${upstreamStatus[1]}. Check credentials and account access.`;
    }
    return `The selected provider returned HTTP ${upstreamStatus[1]}. Check that the provider is running and reachable.`;
  }
  if (/upstream timeout|context deadline exceeded/i.test(raw)) {
    return "The selected provider did not respond before the timeout. Check that it is running, reachable, and not overloaded.";
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

export function defaultProviderForChat(
  models: ModelResponse["data"],
  configuredProviders: ConfiguredStateResponse["data"]["providers"],
  providers: ProviderStatusResponse["data"],
): ProviderFilter {
  const configuredUsable = configuredProviders.filter((provider) => provider.kind !== "cloud" || provider.credential_configured);
  const configuredSource = configuredUsable.length > 0 ? configuredUsable : configuredProviders;
  const configuredIDs = new Set(configuredSource.map((provider) => provider.id));

  const preferredModelProvider = models.find((entry) => {
    const provider = entry.metadata?.provider;
    return Boolean(provider && configuredIDs.has(provider) && entry.metadata?.default);
  })?.metadata?.provider;
  if (preferredModelProvider) return preferredModelProvider;

  const firstModelProvider = models.find((entry) => {
    const provider = entry.metadata?.provider;
    return Boolean(provider && (configuredIDs.size === 0 || configuredIDs.has(provider)));
  })?.metadata?.provider;
  if (firstModelProvider) return firstModelProvider;

  const providerWithReportedModels = providers.find((provider) =>
    (configuredIDs.size === 0 || configuredIDs.has(provider.name)) && (provider.models?.length ?? 0) > 0
  )?.name;
  if (providerWithReportedModels) return providerWithReportedModels;

  return configuredSource[0]?.id ?? providers[0]?.name ?? "auto";
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

export function approvalRecordToPending(row: ChatApprovalRecord): PendingAgentApproval {
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

export function renderChatSessionSummary(session: ChatSessionRecord): ChatSessionsResponse["data"][number] {
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

