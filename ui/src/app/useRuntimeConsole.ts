import { useEffect, useMemo, useRef, useState, type SyntheticEvent } from "react";

import { buildLocalProviderIssue } from "../lib/provider-issues";
import type { LocalProviderIssue } from "../lib/provider-issues";
import { filterModelsByKind, filterModelsByProvider, parseCSV, usdToMicros } from "../lib/runtime-utils";
import {
  ApiError,
  type ChatMessage,
  chooseWorkspaceDirectory as chooseWorkspaceDirectoryRequest,
  chatCompletionsStream,
  updateChatSession as updateChatSessionRequest,
  deletePolicyRule as deletePolicyRuleRequest,
  getAccountSummary,
  getChatSession,
  getChatSessions,
  getModels,
  getProviders,
  getRequestLedger,
  setProviderAPIKey as setProviderAPIKeyRequest,
  upsertPricebookEntry as upsertPricebookEntryRequest,
  deletePricebookEntry as deletePricebookEntryRequest,
  previewPricebookImport as previewPricebookImportRequest,
  applyPricebookImport as applyPricebookImportRequest,
  cancelAgentChatApproval as cancelAgentChatApprovalRequest,
  cancelAgentChatSession as cancelAgentChatSessionRequest,
  deleteAgentChatGrant as deleteAgentChatGrantRequest,
  getAgentChatMessageFileDiff as getAgentChatMessageFileDiffRequest,
  getAgentChatApproval as getAgentChatApprovalRequest,
  listAgentChatMessageFiles as listAgentChatMessageFilesRequest,
  listAgentChatApprovals as listAgentChatApprovalsRequest,
  listAgentChatGrants as listAgentChatGrantsRequest,
  probeAgentAdapter as probeAgentAdapterRequest,
  revertAgentChatMessageFiles as revertAgentChatMessageFilesRequest,
  resolveTaskApproval as resolveTaskApprovalRequest,
  resolveAgentChatApproval as resolveAgentChatApprovalRequest,
  runRetention as runRetentionRequest,
  resetBudget as resetBudgetRequest,
  setBudgetLimit as setBudgetLimitRequest,
  topUpBudget as topUpBudgetRequest,
  upsertPolicyRule as upsertPolicyRuleRequest,
  createProvider as createProviderRequest,
  deleteModelCapabilityOverride as deleteModelCapabilityOverrideRequest,
  createAgentChatMessage as createAgentChatMessageRequest,
  createAgentChatSession as createAgentChatSessionRequest,
  deleteAgentChatSession as deleteAgentChatSessionRequest,
  deleteProvider as deleteProviderRequest,
  getAgentChatSession,
  recordModelCapabilityProbe as recordModelCapabilityProbeRequest,
  streamAgentChatSession,
  setProviderBaseURL as setProviderBaseURLRequest,
  setProviderName as setProviderNameRequest,
  setProviderCustomName as setProviderCustomNameRequest,
  upsertModelCapabilityOverride as upsertModelCapabilityOverrideRequest,
} from "../lib/api";
import type { PolicyRuleUpsertPayload, ResolveAgentChatApprovalPayload, ResolveTaskApprovalPayload, AgentChatGrantFilter, ModelCapabilityUpsertPayload } from "../lib/api";
import {
  approvalRecordToPending,
  buildAssistantToolCallMessage,
  buildSyntheticChatResult,
  defaultModelForProvider,
  deriveChatSessionTitle,
  humanizeChatError,
  isModelValidForProvider,
  renderAgentChatSessionSummary,
} from "./runtimeConsoleChatHelpers";
import { deriveSessionState, resolveDashboardSnapshot } from "./runtimeConsoleDashboard";
import type {
  BudgetStatusResponse,
  AccountSummaryResponse,
  AgentAdapterHealthRecord,
  AgentAdapterRecord,
  AgentChatApprovalRecord,
  AgentChatActivityRecord,
  AgentChatChangedFileDiffRecord,
  AgentChatChangedFileRecord,
  AgentChatGrantRecord,
  PendingAgentApproval,
  AgentChatSessionRecord,
  AgentChatSessionsResponse,
  ChatResponse,
  ChatSessionRecord,
  ChatSessionsResponse,
  ConfiguredStateResponse,
  HealthResponse,
  ModelFilter,
  ModelResponse,
  PricebookEntryUpsertPayload,
  PricebookImportDiff,
  ProviderPresetRecord,
  ProviderFilter,
  ProviderStatusResponse,
  RequestLedgerResponse,
  RuntimeHeaders,
  SessionResponse,
  RetentionRunData,
} from "../types/runtime";

type NoticeState = {
  kind: "success" | "error";
  message: string;
};

type ChatTarget = "model" | "agent" | "external_agent";
type HecateChatTarget = "model" | "agent";
type QueuedChatMessage = {
  id: string;
  session_id: string;
  content: string;
  runtime_kind: ChatTarget;
  provider_filter: ProviderFilter;
  model: string;
  workspace: string;
  system_prompt: string;
  adapter_id: string;
  created_at: string;
};

const queuedChatMessagesStorageKey = "hecate.queuedChatMessages";

function normalizeStoredChatTarget(value: string): ChatTarget {
  switch (value) {
    case "model":
    case "agent":
    case "external_agent":
      return value;
    default:
      return "agent";
  }
}

function normalizeStoredHecateChatTarget(value: string): HecateChatTarget | "" {
  switch (value) {
    case "model":
    case "agent":
      return value;
    default:
      return "";
  }
}

function readStoredQueuedChatMessages(): QueuedChatMessage[] {
  if (typeof window === "undefined") {
    return [];
  }
  try {
    const raw = window.localStorage.getItem(queuedChatMessagesStorageKey);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    return parsed.flatMap((item): QueuedChatMessage[] => {
      if (!item || typeof item !== "object") return [];
      const id = typeof item.id === "string" ? item.id : "";
      const sessionID = typeof item.session_id === "string" ? item.session_id : "";
      const content = typeof item.content === "string" ? item.content : "";
      if (!id || !sessionID || !content.trim()) return [];
      return [{
        id,
        session_id: sessionID,
        content,
        runtime_kind: normalizeStoredChatTarget(typeof item.runtime_kind === "string" ? item.runtime_kind : ""),
        provider_filter: typeof item.provider_filter === "string" ? item.provider_filter as ProviderFilter : "auto",
        model: typeof item.model === "string" ? item.model : "",
        workspace: typeof item.workspace === "string" ? item.workspace : "",
        system_prompt: typeof item.system_prompt === "string" ? item.system_prompt : "",
        adapter_id: typeof item.adapter_id === "string" ? item.adapter_id : "",
        created_at: typeof item.created_at === "string" ? item.created_at : new Date().toISOString(),
      }];
    });
  } catch {
    return [];
  }
}

function writeStoredQueuedChatMessages(items: QueuedChatMessage[]) {
  if (typeof window === "undefined") {
    return;
  }
  try {
    if (items.length === 0) {
      window.localStorage.removeItem(queuedChatMessagesStorageKey);
      return;
    }
    window.localStorage.setItem(queuedChatMessagesStorageKey, JSON.stringify(items));
  } catch {
    // Browser storage can be disabled, private, or quota-limited. Queued
    // prompts remain usable in memory even when draft persistence is unavailable.
  }
}

function readStoredChatTargetsBySessionID(): Map<string, HecateChatTarget> {
  if (typeof window === "undefined") {
    return new Map();
  }
  try {
    const raw = window.localStorage.getItem("hecate.chatTargetBySessionID");
    if (!raw) return new Map();
    const parsed = JSON.parse(raw) as Record<string, string>;
    const entries = Object.entries(parsed)
      .map(([sessionID, target]) => [sessionID, normalizeStoredHecateChatTarget(target)] as const)
      .filter((entry): entry is readonly [string, HecateChatTarget] => Boolean(entry[0] && entry[1]));
    return new Map(entries);
  } catch {
    return new Map();
  }
}

function serializeChatTargetsBySessionID(targets: Map<string, HecateChatTarget>): string {
  return JSON.stringify(Object.fromEntries(targets));
}

function agentChatSessionIsExternal(session: AgentChatSessionRecord | null): boolean {
  return Boolean(session?.runtime_kind === "external_agent" || session?.adapter_id);
}

function agentChatSessionIsBusy(session: AgentChatSessionRecord | null): boolean {
  const busy = (status?: string) => status === "queued" || status === "running" || status === "awaiting_approval";
  if (!session) return false;
  if (busy(session.status)) return true;
  if ((session.segments ?? []).some((segment) => busy(segment.status))) return true;
  return (session.messages ?? []).some((message) => message.role === "assistant" && busy(message.status));
}

function deriveHecateChatTargetFromSession(session: AgentChatSessionRecord | null): HecateChatTarget {
  if (!session) return "agent";
  const messages = session.messages ?? [];
  for (let i = messages.length - 1; i >= 0; i--) {
    const target = normalizeStoredHecateChatTarget(messages[i]?.runtime_kind ?? "");
    if (target) return target;
  }
  return normalizeStoredHecateChatTarget(session.runtime_kind ?? "") || "agent";
}

function deriveHecateChatSelectionFromSession(session: AgentChatSessionRecord | null): { provider: string; model: string } {
  if (!session || agentChatSessionIsExternal(session)) {
    return { provider: "", model: "" };
  }
  const segments = [...(session.segments ?? [])].reverse();
  const segment = segments.find((item) => item.runtime_kind === "agent" || item.runtime_kind === "model");
  if (segment?.provider || segment?.model) {
    return { provider: segment.provider ?? "", model: segment.model ?? "" };
  }
  const messages = [...(session.messages ?? [])].reverse();
  const message = messages.find((item) => item.runtime_kind === "agent" || item.runtime_kind === "model");
  if (message?.provider || message?.model) {
    return { provider: message.provider ?? "", model: message.model ?? "" };
  }
  return { provider: session.provider ?? "", model: session.model ?? "" };
}

function readLocalStorage(key: string): string {
  if (typeof window === "undefined") {
    return "";
  }
  return window.localStorage.getItem(key) ?? "";
}

export { humanizeChatError };

export function useRuntimeConsole() {
  const [health, setHealth] = useState<HealthResponse | null>(null);
  const [models, setModels] = useState<ModelResponse["data"]>([]);
  const [providers, setProviders] = useState<ProviderStatusResponse["data"]>([]);
  const [providerPresets, setProviderPresets] = useState<ProviderPresetRecord[]>([]);
  const [agentAdapters, setAgentAdapters] = useState<AgentAdapterRecord[]>([]);
  const [defaultChatTarget, setDefaultChatTarget] = useState<ChatTarget>(() => normalizeStoredChatTarget(readLocalStorage("hecate.chatTarget")));
  const [chatTargetBySessionID, setChatTargetBySessionID] = useState<Map<string, HecateChatTarget>>(() => readStoredChatTargetsBySessionID());
  const [agentAdapterID, setAgentAdapterID] = useState(() => readLocalStorage("hecate.agentAdapterID") || "codex");
  const [agentWorkspace, setAgentWorkspace] = useState(() => readLocalStorage("hecate.agentWorkspace"));
  const [agentWorkspaceBranch, setAgentWorkspaceBranch] = useState("");
  const [agentChatSessions, setAgentChatSessions] = useState<AgentChatSessionsResponse["data"]>([]);
  const [activeAgentChatSessionID, setActiveAgentChatSessionID] = useState(() => readLocalStorage("hecate.agentChatSessionID"));
  const [activeAgentChatSession, setActiveAgentChatSession] = useState<AgentChatSessionRecord | null>(null);
  // pendingApprovalsBySessionID stores the banner-essentials view of
  // pending approvals, keyed by session id. Values are projected to
  // the same shape carried by the SSE `approval.requested` event so the
  // banner doesn't need to know whether a row came from the initial
  // GET-list refetch or from a streamed event. The full row (ACP
  // options, scope choices, diff preview, …) is fetched on modal open
  // — keeping the map lean.
  //
  // The Map instance is always replaced on update (never mutated in
  // place); React reference equality is the re-render trigger.
  const [pendingApprovalsBySessionID, setPendingApprovalsBySessionID] = useState<
    Map<string, PendingAgentApproval[]>
  >(() => new Map());
  const pendingApprovalsVersionBySessionID = useRef<Map<string, number>>(new Map());
  // agentChatGrants holds the most recent listAgentChatGrants result
  // for the Settings → External Agents tab. Lazy-loaded by the action,
  // not on hook mount.
  const [agentChatGrants, setAgentChatGrants] = useState<AgentChatGrantRecord[]>([]);
  const [agentChatGrantsLoading, setAgentChatGrantsLoading] = useState(false);
  const [agentChatGrantsError, setAgentChatGrantsError] = useState("");
  // agentAdapterApprovalMode mirrors the runtime-stats field of the
  // same name. Empty until the dashboard fan-out resolves. The Chats
  // workspace surfaces a danger banner when this is "auto" — every
  // adapter RequestPermission is permitted without operator review.
  const [agentAdapterApprovalMode, setAgentAdapterApprovalMode] = useState<string>("");
  // agentAdapterHealthByID stores the most recent probe result per
  // adapter, keyed by adapter id. Operators trigger a probe via the
  // "Test" button in Settings → External agents and the result is
  // cached here so the picker dropdown can show a status chip without
  // re-running the probe. Map instance is replaced on update — same
  // invariant as pendingApprovalsBySessionID.
  const [agentAdapterHealthByID, setAgentAdapterHealthByID] = useState<
    Map<string, AgentAdapterHealthRecord>
  >(() => new Map());
  const [agentAdapterHealthLoadingByID, setAgentAdapterHealthLoadingByID] = useState<
    Map<string, true>
  >(() => new Map());
  const [budget, setBudget] = useState<BudgetStatusResponse["data"] | null>(null);
  const [accountSummary, setAccountSummary] = useState<AccountSummaryResponse["data"] | null>(null);
  const [requestLedger, setRequestLedger] = useState<RequestLedgerResponse["data"]>([]);
  const [settingsConfig, setSettingsConfig] = useState<ConfiguredStateResponse["data"] | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const [model, setModel] = useState("");
  const [message, setMessage] = useState("");
  const [queuedChatMessages, setQueuedChatMessages] = useState<QueuedChatMessage[]>(() => readStoredQueuedChatMessages());
  const queuedChatMessagesRef = useRef(queuedChatMessages);
  const [systemPrompt, setSystemPrompt] = useState("");
  const [chatLoading, setChatLoading] = useState(false);
  const [agentChatCancelling, setAgentChatCancelling] = useState(false);
  const [streamingContent, setStreamingContent] = useState<string | null>(null);
  const [chatResult, setChatResult] = useState<ChatResponse | null>(null);
  // pendingToolCalls: model responded with tool_calls; waiting for user to fill results.
  const [pendingToolCalls, setPendingToolCalls] = useState<Array<{ id: string; name: string; arguments: string; result: string }>>([]);
  // Thread of messages that preceded the pending tool calls (history + user message + assistant tool_calls message).
  const [pendingThread, setPendingThread] = useState<import("../lib/api").ChatMessage[] | null>(null);
  const [chatSessions, setChatSessions] = useState<ChatSessionsResponse["data"]>([]);
  const [chatSessionsHasMore, setChatSessionsHasMore] = useState(false);
  const [chatSessionsLoadingMore, setChatSessionsLoadingMore] = useState(false);
  const [activeChatSessionID, setActiveChatSessionID] = useState("");
  const [activeChatSession, setActiveChatSession] = useState<ChatSessionRecord | null>(null);
  const [runtimeHeaders, setRuntimeHeaders] = useState<RuntimeHeaders | null>(null);
  const [chatError, setChatError] = useState("");
  const [chatErrorCode, setChatErrorCode] = useState("");
  const [chatErrorStatus, setChatErrorStatus] = useState<number | null>(null);
  const [chatErrorAction, setChatErrorAction] = useState("");
  const [chatErrorRequestID, setChatErrorRequestID] = useState("");
  const [chatErrorTraceID, setChatErrorTraceID] = useState("");
  const [modelFilter, setModelFilter] = useState<ModelFilter>("all");
  const [providerFilter, setProviderFilter] = useState<ProviderFilter>("auto");
  const [copiedCommand, setCopiedCommand] = useState("");

  const [budgetAmountUsd, setBudgetAmountUsd] = useState("1.00");
  const [budgetLimitUsd, setBudgetLimitUsd] = useState("5.00");
  const [budgetActionError, setBudgetActionError] = useState("");

  const [sessionInfo, setSessionInfo] = useState<SessionResponse["data"] | null>(null);
  const [settingsError, setSettingsError] = useState("");
  const [notice, setNotice] = useState<NoticeState | null>(null);

  const chatTarget = activeAgentChatSessionID && activeAgentChatSession
    ? (agentChatSessionIsExternal(activeAgentChatSession)
        ? "external_agent"
        : (chatTargetBySessionID.get(activeAgentChatSessionID) ?? deriveHecateChatTargetFromSession(activeAgentChatSession)))
    : defaultChatTarget;

  const [retentionSubsystems, setRetentionSubsystems] = useState("");
  const [retentionLoading, setRetentionLoading] = useState(false);
  const [retentionError, setRetentionError] = useState("");
  const [retentionLastRun, setRetentionLastRun] = useState<RetentionRunData | null>(null);
  const [retentionRuns, setRetentionRuns] = useState<RetentionRunData[]>([]);

  const healthyProviders = providers.filter((provider) => provider.healthy).length;
  const localProviders = providers.filter((provider) => provider.kind === "local");
  const cloudProviders = providers.filter((provider) => provider.kind === "cloud");
  const localModels = models.filter((entry) => entry.metadata?.provider_kind === "local");
  const cloudModels = models.filter((entry) => entry.metadata?.provider_kind === "cloud");
  const healthyLocalProviders = localProviders.filter((provider) => provider.healthy).length;
  const healthyCloudProviders = cloudProviders.filter((provider) => provider.healthy).length;

  const visibleModels = useMemo(() => filterModelsByKind(models, modelFilter), [modelFilter, models]);
  const providerScopedModels = useMemo(
    () => filterModelsByProvider(visibleModels, providerFilter),
    [providerFilter, visibleModels],
  );
  const localProviderIssues = useMemo(
    () =>
      localProviders
        .map((provider) => buildLocalProviderIssue(provider))
        .filter((issue): issue is LocalProviderIssue => issue !== null),
    [localProviders],
  );
  const session = useMemo(() => {
    return deriveSessionState(sessionInfo);
  }, [sessionInfo]);

  useEffect(() => {
    const storedChatSessionID = window.localStorage.getItem("hecate.chatSessionID");
    if (storedChatSessionID) {
      setActiveChatSessionID(storedChatSessionID);
    }
    const storedModel = window.localStorage.getItem("hecate.model");
    if (storedModel) {
      setModel(storedModel);
    }
    const storedProvider = window.localStorage.getItem("hecate.providerFilter");
    if (storedProvider) {
      setProviderFilter(storedProvider as ProviderFilter);
    }
    const storedSystemPrompt = window.localStorage.getItem("hecate.systemPrompt");
    if (storedSystemPrompt) {
      setSystemPrompt(storedSystemPrompt);
    }
    const storedTarget = window.localStorage.getItem("hecate.chatTarget");
    if (storedTarget) {
      setDefaultChatTarget(normalizeStoredChatTarget(storedTarget));
    }
    const storedAgent = window.localStorage.getItem("hecate.agentAdapterID");
    if (storedAgent) setAgentAdapterID(storedAgent);
    const storedWorkspace = window.localStorage.getItem("hecate.agentWorkspace");
    if (storedWorkspace) setAgentWorkspace(storedWorkspace);
    const storedAgentSession = window.localStorage.getItem("hecate.agentChatSessionID");
    if (storedAgentSession) setActiveAgentChatSessionID(storedAgentSession);
  }, []);

  useEffect(() => {
    window.localStorage.setItem("hecate.systemPrompt", systemPrompt);
  }, [systemPrompt]);

  useEffect(() => {
    void loadDashboard();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (!chatLoading) {
      setAgentChatCancelling(false);
    }
  }, [chatLoading]);

  // Reconnect catch-up: whenever the active agent-chat session
  // changes (initial mount with a persisted id, user-driven switch,
  // post-loadDashboard hydration), refetch the pending approvals so
  // anything created/resolved while we were disconnected is
  // reconciled. Subsequent SSE events mutate this same map.
  useEffect(() => {
    if (!activeAgentChatSessionID) return;
    void refetchPendingApprovals(activeAgentChatSessionID);
    // refetchPendingApprovals is a stable closure declared in the
    // hook body; no need to include it in deps.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeAgentChatSessionID]);

  useEffect(() => {
    if (model) {
      window.localStorage.setItem("hecate.model", model);
    }
  }, [model]);

  useEffect(() => {
    window.localStorage.setItem("hecate.providerFilter", providerFilter);
  }, [providerFilter]);

  useEffect(() => {
    window.localStorage.setItem("hecate.chatTarget", defaultChatTarget);
  }, [defaultChatTarget]);

  useEffect(() => {
    window.localStorage.setItem("hecate.chatTargetBySessionID", serializeChatTargetsBySessionID(chatTargetBySessionID));
  }, [chatTargetBySessionID]);

  useEffect(() => {
    window.localStorage.setItem("hecate.agentAdapterID", agentAdapterID);
  }, [agentAdapterID]);

  useEffect(() => {
    window.localStorage.setItem("hecate.agentWorkspace", agentWorkspace);
  }, [agentWorkspace]);

  useEffect(() => {
    if (activeChatSessionID) {
      window.localStorage.setItem("hecate.chatSessionID", activeChatSessionID);
      return;
    }
    window.localStorage.removeItem("hecate.chatSessionID");
  }, [activeChatSessionID]);

  useEffect(() => {
    if (activeAgentChatSessionID) {
      window.localStorage.setItem("hecate.agentChatSessionID", activeAgentChatSessionID);
      return;
    }
    window.localStorage.removeItem("hecate.agentChatSessionID");
  }, [activeAgentChatSessionID]);

  useEffect(() => {
    queuedChatMessagesRef.current = queuedChatMessages;
    const timeout = window.setTimeout(() => {
      writeStoredQueuedChatMessages(queuedChatMessages);
    }, 200);
    return () => window.clearTimeout(timeout);
  }, [queuedChatMessages]);

  useEffect(() => {
    const flushQueuedMessages = () => writeStoredQueuedChatMessages(queuedChatMessagesRef.current);
    window.addEventListener("pagehide", flushQueuedMessages);
    return () => {
      window.removeEventListener("pagehide", flushQueuedMessages);
      flushQueuedMessages();
    };
  }, []);

  useEffect(() => {
    if (!notice) {
      return;
    }
    const timeout = window.setTimeout(() => {
      setNotice((current) => (current === notice ? null : current));
    }, 3000);
    return () => window.clearTimeout(timeout);
  }, [notice]);

  useEffect(() => {
    if (providerFilter === "auto") {
      return;
    }
    const stillValid = isModelValidForProvider(model, providerFilter, models, providers, providerPresets);
    if (stillValid) {
      return;
    }
    const nextModel = defaultModelForProvider(providerFilter, models, providers, providerPresets);
    setModel(nextModel);
  }, [model, models, providerFilter, providers, providerPresets]);

  useEffect(() => {
    if (providerFilter === "auto" || model !== "" || models.length === 0) {
      return;
    }
    const scopedModels = models.filter((m) => m.metadata?.provider === providerFilter);
    if (scopedModels.length === 0) return;
    setModel(defaultModelForProvider(providerFilter, models, providers, providerPresets));
  }, [model, models, providers, providerFilter, providerPresets]);

  // When models load, validate the selected model. If it's not in the list (e.g. stale localStorage),
  // fall back to the gateway default. If no model is set at all, pick the default.
  //
  // Only fires when no provider scope is active (providerFilter === "auto").
  // When a specific provider is scoped, the effect above (lines 279-286) owns
  // the scoped-default behavior and correctly leaves the model empty when the
  // provider has no discovered models. Without this guard, picking a local
  // provider whose runtime isn't running (Ollama / LM Studio without the
  // process up) caused an infinite loop: this effect set model to the
  // gateway-wide default (e.g. gpt-4o-mini from openai), the
  // provider-scoped-validity effect above cleared it as invalid for the
  // current provider, and the cycle repeated — visibly blinking the
  // ModelPicker trigger label every render.
  useEffect(() => {
    if (providerFilter !== "auto") return;
    if (models.length === 0) return;
    if (model !== "" && models.some((m) => m.id === model)) return;
    const defaultM = models.find((m) => m.metadata?.default)?.id ?? models[0]?.id ?? "";
    if (defaultM) setModel(defaultM);
  }, [model, models, providerFilter]);

  function clearPendingToolState() {
    setPendingToolCalls([]);
    setPendingThread(null);
  }

  function resetChatWorkspaceState() {
    setMessage("");
    setChatResult(null);
    setStreamingContent(null);
    setRuntimeHeaders(null);
    clearPendingToolState();
    clearChatErrorState();
    setSystemPrompt("");
  }

  async function refreshChatSessionState(sessionID: string) {
    if (!sessionID) {
      return;
    }
    try {
      const [sessionsResult, sessionResult] = await Promise.all([
        getChatSessions(20),
        getChatSession(sessionID),
      ]);
      setChatSessions(sessionsResult.data ?? []);
      setActiveChatSession(sessionResult.data);
    } catch {
      // Keep the primary request flow resilient.
    }
  }

  async function refreshRuntimeState() {
    try {
      const accountSummaryResult = await getAccountSummary("");
      setAccountSummary(accountSummaryResult.data);
    } catch {
      // Keep chat responsive even if refresh paths fail.
    }
    try {
      const requestLedgerResult = await getRequestLedger(20);
      setRequestLedger(requestLedgerResult.data ?? []);
    } catch {
      // Best-effort.
    }
  }

  // refreshProviders re-fetches /hecate/v1/providers/status (runtime health) and
  // /v1/models (model catalog) for the ProvidersView auto-poll so local
  // provider model lists converge within ~30 s of starting Ollama / LM
  // Studio. Skipped when no providers are configured — the providers
  // tab renders its empty state, there's nothing to converge.
  async function refreshProviders() {
    if ((settingsConfig?.providers?.length ?? 0) === 0) return;
    try {
      const [pResult, mResult] = await Promise.allSettled([
        getProviders(),
        getModels(),
      ]);
      if (pResult.status === "fulfilled") setProviders(pResult.value.data ?? []);
      if (mResult.status === "fulfilled") setModels(mResult.value.data ?? []);
    } catch {
      // Best-effort background refresh — ignore errors.
    }
  }

  function buildChatPayload(messages: ChatMessage[], sessionID?: string) {
    return {
      model,
      provider: providerFilter === "auto" ? "" : providerFilter,
      session_id: sessionID,
      user: "",
      messages,
    };
  }

  async function loadDashboard() {
    setLoading(true);
    setError("");
    setSettingsError("");

    try {
      const snapshot = await resolveDashboardSnapshot({
        activeChatSessionID,
        activeAgentChatSessionID,
        previous: {
          providers,
          agentAdapters,
          budget,
          accountSummary,
          chatSessions,
          activeChatSession,
          agentChatSessions,
          activeAgentChatSession,
          requestLedger,
          settingsConfig,
          retentionRuns,
          retentionLastRun,
        },
      });

      setHealth(snapshot.health);
      setSessionInfo(snapshot.sessionInfo);
      setModels(snapshot.models);
      setProviders(snapshot.providers);
      setProviderPresets(snapshot.providerPresets);
      setAgentAdapters(snapshot.agentAdapters);
      setBudget(snapshot.budget);
      setAccountSummary(snapshot.accountSummary);
      setChatSessions(snapshot.chatSessions);
      setChatSessionsHasMore(snapshot.chatSessionsHasMore);
      setActiveChatSessionID(snapshot.activeChatSessionID);
      setActiveChatSession(snapshot.activeChatSession);
      setAgentChatSessions(snapshot.agentChatSessions);
      pruneQueuedChatMessagesForSessions(snapshot.agentChatSessions.map((session) => session.id));
      setActiveAgentChatSessionID(snapshot.activeAgentChatSessionID);
      setActiveAgentChatSession(snapshot.activeAgentChatSession);
      setRequestLedger(snapshot.requestLedger);
      setSettingsConfig(snapshot.settingsConfig);
      setRetentionRuns(snapshot.retentionRuns);
      setRetentionLastRun(snapshot.retentionLastRun);
      setAgentAdapterApprovalMode(snapshot.agentAdapterApprovalMode);
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "unknown load error");
    } finally {
      setLoading(false);
    }
  }

  function selectProviderRoute(nextProvider: ProviderFilter) {
    setProviderFilter(nextProvider);
    setModel(defaultModelForProvider(nextProvider, models, providers, providerPresets));
  }

  function updateAgentWorkspace(nextWorkspace: string) {
    setAgentWorkspace(nextWorkspace);
    setAgentWorkspaceBranch("");
  }

  function setChatTarget(nextTarget: ChatTarget) {
    if (activeAgentChatSessionID && activeAgentChatSession) {
      const currentExternal = agentChatSessionIsExternal(activeAgentChatSession);
      const nextExternal = nextTarget === "external_agent";
      if (currentExternal !== nextExternal) {
        setActiveAgentChatSessionID("");
        setActiveAgentChatSession(null);
        setAgentWorkspaceBranch("");
        setDefaultChatTarget(nextTarget);
        return;
      }
      if (!nextExternal) {
        setChatTargetBySessionID((current) => {
          const next = new Map(current);
          next.set(activeAgentChatSessionID, nextTarget);
          return next;
        });
        return;
      }
    }
    setDefaultChatTarget(nextTarget);
  }

  async function submitChat(event: SyntheticEvent<HTMLFormElement>) {
    event.preventDefault();
    await submitAgentChat();
  }

  function removeQueuedChatMessage(id: string) {
    setQueuedChatMessages((current) => current.filter((item) => item.id !== id));
  }

  function updateQueuedChatMessage(id: string, content: string) {
    setQueuedChatMessages((current) => current.map((item) => (
      item.id === id ? { ...item, content } : item
    )));
  }

  function pruneQueuedChatMessagesForSessions(sessionIDs: Iterable<string>) {
    const valid = new Set(sessionIDs);
    setQueuedChatMessages((current) => current.filter((item) => valid.has(item.session_id)));
  }

  function buildQueuedChatMessage(content: string, runtimeKind: ChatTarget, sessionID: string): QueuedChatMessage {
    return {
      id: `queued-chat-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
      session_id: sessionID,
      content,
      runtime_kind: runtimeKind,
      provider_filter: providerFilter,
      model,
      workspace: agentWorkspace.trim(),
      system_prompt: systemPrompt,
      adapter_id: agentAdapterID,
      created_at: new Date().toISOString(),
    };
  }

  function queueChatMessage(content: string, runtimeKind: ChatTarget, sessionID: string) {
    setQueuedChatMessages((current) => [...current, buildQueuedChatMessage(content, runtimeKind, sessionID)]);
    setMessage("");
  }

  function applyAgentChatSession(session: AgentChatSessionRecord) {
    setActiveAgentChatSession(session);
    setAgentWorkspaceBranch(session.workspace_branch ?? "");
    setAgentChatSessions((current) => [renderAgentChatSessionSummary(session), ...current.filter((entry) => entry.id !== session.id)]);
  }

  async function refreshAgentChatSession(sessionID: string): Promise<void> {
    const payload = await getAgentChatSession(sessionID);
    applyAgentChatSession(payload.data);
  }

  // setPendingApprovalsForSession atomically replaces the pending list
  // for a session. The catch-up path only calls this when no live SSE
  // or optimistic local update landed while the request was in flight;
  // otherwise the GET result may be stale relative to the stream.
  function setPendingApprovalsForSession(
    sessionID: string,
    rows: PendingAgentApproval[],
  ) {
    setPendingApprovalsBySessionID((current) => {
      const next = new Map(current);
      if (rows.length === 0) {
        next.delete(sessionID);
      } else {
        next.set(sessionID, rows);
      }
      return next;
    });
  }

  function bumpPendingApprovalsVersion(sessionID: string) {
    const current = pendingApprovalsVersionBySessionID.current.get(sessionID) ?? 0;
    pendingApprovalsVersionBySessionID.current.set(sessionID, current + 1);
  }

  function upsertPendingApproval(event: PendingAgentApproval) {
    bumpPendingApprovalsVersion(event.session_id);
    setPendingApprovalsBySessionID((current) => {
      const next = new Map(current);
      const existing = next.get(event.session_id) ?? [];
      const filtered = existing.filter((row) => row.approval_id !== event.approval_id);
      filtered.push(event);
      next.set(event.session_id, filtered);
      return next;
    });
  }

  function removePendingApproval(sessionID: string, approvalID: string) {
    bumpPendingApprovalsVersion(sessionID);
    setPendingApprovalsBySessionID((current) => {
      const existing = current.get(sessionID);
      if (!existing) return current;
      const filtered = existing.filter((row) => row.approval_id !== approvalID);
      if (filtered.length === existing.length) return current;
      const next = new Map(current);
      if (filtered.length === 0) {
        next.delete(sessionID);
      } else {
        next.set(sessionID, filtered);
      }
      return next;
    });
  }

  // refetchPendingApprovals is the catch-up path. Called when an
  // operator selects a session: any approvals created or resolved while
  // the SSE stream was disconnected (or before this session was
  // active) are reconciled in one round trip. Subsequent SSE events
  // mutate this same map.
  async function refetchPendingApprovals(sessionID: string) {
    if (!sessionID) return;
    const startedAtVersion = pendingApprovalsVersionBySessionID.current.get(sessionID) ?? 0;
    try {
      const result = await listAgentChatApprovalsRequest(sessionID, "pending");
      const currentVersion = pendingApprovalsVersionBySessionID.current.get(sessionID) ?? 0;
      if (currentVersion !== startedAtVersion) {
        // A live SSE update or optimistic local action landed while
        // this catch-up request was in flight. Ignore the stale GET
        // result rather than clearing a newer pending approval or
        // re-adding one that was just resolved.
        return;
      }
      const rows = (result.data ?? []).map(approvalRecordToPending);
      setPendingApprovalsForSession(sessionID, rows);
    } catch {
      // Banner is best-effort; failure here just means the operator
      // doesn't see the catch-up state until the next reconnect.
    }
  }

  function clearChatErrorState() {
    setChatError("");
    setChatErrorCode("");
    setChatErrorStatus(null);
    setChatErrorAction("");
    setChatErrorRequestID("");
    setChatErrorTraceID("");
  }

  function setChatErrorState(error: unknown, fallback = "unknown request error") {
    const raw = error instanceof Error ? error.message : fallback;
    setChatError(humanizeChatError(raw));
    setChatErrorCode(error instanceof ApiError ? error.code : "");
    setChatErrorStatus(error instanceof ApiError ? error.status : null);
    setChatErrorAction(error instanceof ApiError ? error.operatorAction : "");
    setChatErrorRequestID(error instanceof ApiError ? error.requestId : "");
    setChatErrorTraceID(error instanceof ApiError ? error.traceId : "");
  }

  async function submitAgentChat(queued?: QueuedChatMessage) {
    const content = (queued?.content ?? message).trim();
    if (!content) return;

    const turnRuntimeKind = queued?.runtime_kind ?? (chatTarget === "external_agent" ? "external_agent" : chatTarget === "agent" ? "agent" : "model");
    if (!queued && activeAgentChatSessionID && agentChatSessionIsBusy(activeAgentChatSession)) {
      queueChatMessage(content, turnRuntimeKind, activeAgentChatSessionID);
      return;
    }

    setChatLoading(true);
    clearChatErrorState();
    setRuntimeHeaders(null);
    const isExternalAgent = turnRuntimeKind === "external_agent";
    const isModelTurn = turnRuntimeKind === "model";
    const turnProviderFilter = queued?.provider_filter ?? providerFilter;
    const turnModel = queued?.model ?? model;
    const turnWorkspace = queued?.workspace ?? agentWorkspace.trim();
    const turnSystemPrompt = queued?.system_prompt ?? systemPrompt;
    const turnAdapterID = queued?.adapter_id ?? agentAdapterID;
    setStreamingContent(isExternalAgent ? "Starting external agent..." : isModelTurn ? "Waiting for model output..." : "Starting Hecate Agent...");
    let streamAbort: AbortController | null = null;
    let streamPromise: Promise<void> | null = null;

    try {
      if (!isModelTurn && !turnWorkspace) {
        setChatError("Choose a workspace path before starting an agent chat.");
        return;
      }

      let sessionID = queued?.session_id ?? activeAgentChatSessionID;
      if (sessionID && !activeAgentChatSession) {
        // The server owns chat persistence. If localStorage points at a
        // deleted or unavailable session, start clean instead of making the
        // next prompt fail with a stale 404.
        sessionID = "";
        setActiveAgentChatSessionID("");
      }
      if (sessionID && activeAgentChatSession?.runtime_kind) {
        const activeExternal = activeAgentChatSession.runtime_kind === "external_agent";
        if (activeExternal !== isExternalAgent) {
          sessionID = "";
          setActiveAgentChatSessionID("");
          setActiveAgentChatSession(null);
        }
      }
      if (!sessionID) {
        const created = await createAgentChatSessionRequest({
          title: deriveChatSessionTitle(content),
          runtime_kind: turnRuntimeKind,
          ...(isExternalAgent
            ? { adapter_id: turnAdapterID }
            : { provider: turnProviderFilter === "auto" ? "" : turnProviderFilter, model: turnModel }),
          ...(!isModelTurn ? { workspace: turnWorkspace } : {}),
        });
        sessionID = created.data.id;
        setActiveAgentChatSessionID(sessionID);
        applyAgentChatSession(created.data);
      }

      const pendingContent = content;
      setMessage("");
      setActiveAgentChatSession((prev) =>
        prev
          ? {
              ...prev,
              messages: [
                ...(prev.messages ?? []),
                {
                  id: `pending-agent-user-${Date.now()}`,
                  runtime_kind: turnRuntimeKind,
                  provider: !isExternalAgent ? (turnProviderFilter === "auto" ? "" : turnProviderFilter) : undefined,
                  model: !isExternalAgent ? turnModel : undefined,
                  role: "user",
                  content: pendingContent,
                  created_at: new Date().toISOString(),
                },
              ],
            }
          : prev,
      );

      streamAbort = new AbortController();
      streamPromise = streamAgentChatSession(
        sessionID,
        (event) => {
          switch (event.type) {
            case "session_update": {
              applyAgentChatSession(event.payload.data);
              const last = [...(event.payload.data.messages ?? [])]
                .reverse()
                .find((m) => m.role === "assistant");
              if (last?.status === "running") {
                setStreamingContent(last.content || (isExternalAgent ? "External agent is running..." : isModelTurn ? "Model is responding..." : "Hecate Agent is running..."));
              }
              return;
            }
            case "approval.requested": {
              upsertPendingApproval(event.payload);
              return;
            }
            case "approval.resolved": {
              removePendingApproval(event.payload.session_id, event.payload.approval_id);
              return;
            }
          }
        },
        streamAbort.signal,
      ).catch((streamError) => {
        if (streamAbort?.signal.aborted) {
          return;
        }
        const msg = streamError instanceof Error ? streamError.message : "agent chat stream failed";
        setChatError((current) => current || msg);
      });
      const updated = await createAgentChatMessageRequest(sessionID, {
        content: pendingContent,
        runtime_kind: turnRuntimeKind,
        ...(!isExternalAgent ? { provider: turnProviderFilter === "auto" ? "" : turnProviderFilter, model: turnModel } : {}),
        ...(isModelTurn ? { system_prompt: turnSystemPrompt } : {}),
        ...(turnRuntimeKind === "agent" ? { workspace: turnWorkspace } : {}),
      });
      applyAgentChatSession(updated.data);
    } catch (submitError) {
      setChatErrorState(submitError);
    } finally {
      streamAbort?.abort();
      await streamPromise?.catch(() => undefined);
      setStreamingContent(null);
      setChatLoading(false);
    }
  }

  useEffect(() => {
    if (queuedChatMessages.length === 0 || chatLoading || agentChatCancelling) {
      return;
    }
    if (agentChatSessionIsBusy(activeAgentChatSession)) {
      return;
    }
    if (!activeAgentChatSessionID) {
      return;
    }
    if (activeAgentChatSession?.id !== activeAgentChatSessionID) {
      return;
    }
    const next = queuedChatMessages.find((item) => item.session_id === activeAgentChatSessionID);
    if (!next) {
      return;
    }
    if (!next.content.trim()) {
      setQueuedChatMessages((current) => current.filter((item) => item.id !== next.id));
      return;
    }
    setQueuedChatMessages((current) => current.filter((item) => item.id !== next.id));
    void submitAgentChat(next);
  // submitAgentChat deliberately stays out of the dependency list: it
  // reads the queued snapshot passed above, not the live composer state.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    activeAgentChatSession?.id,
    activeAgentChatSession?.latest_run_id,
    activeAgentChatSession?.status,
    activeAgentChatSession?.updated_at,
    activeAgentChatSessionID,
    agentChatCancelling,
    chatLoading,
    queuedChatMessages,
  ]);

  async function cancelAgentChat() {
    if (!activeAgentChatSessionID || agentChatCancelling) {
      return;
    }
    setAgentChatCancelling(true);
    setStreamingContent("Stopping external agent...");
    try {
      await cancelAgentChatSessionRequest(activeAgentChatSessionID);
    } catch (error) {
      setAgentChatCancelling(false);
      setChatErrorState(error, "failed to cancel agent chat");
    }
  }

  function updateToolResult(index: number, result: string) {
    setPendingToolCalls((prev) => prev.map((tc, i) => (i === index ? { ...tc, result } : tc)));
  }

  async function submitToolResults() {
    if (!pendingThread || pendingToolCalls.length === 0) return;
    setChatLoading(true);
    clearChatErrorState();

    const toolMessages: ChatMessage[] = pendingToolCalls.map((tc) => ({
      role: "tool" as const,
      content: tc.result,
      tool_call_id: tc.id,
    }));

    const messages: ChatMessage[] = [...pendingThread, ...toolMessages];

    try {
      const chatExecution = await executeChatRequest(buildChatPayload(messages, activeChatSessionID || undefined), messages);
      if (chatExecution.kind === "tool_calls") {
        return;
      }

      clearPendingToolState();
      setChatResult(chatExecution.chatResult);
      await refreshChatSessionState(activeChatSessionID);
      setStreamingContent(null);
      await refreshRuntimeState();
    } catch (err) {
      setChatErrorState(err, "unknown error");
    } finally {
      setChatLoading(false);
    }
  }

  async function executeChatRequest(
    chatPayload: {
      model: string;
      provider: string;
      session_id?: string;
      user: string;
      messages: ChatMessage[];
    },
    toolCallBaseMessages: ChatMessage[],
  ): Promise<
    | { kind: "tool_calls" }
    | { kind: "completed"; headers: RuntimeHeaders; chatResult: ChatResponse }
  > {
    let fullContent = "";
    setStreamingContent("");
    const response = await chatCompletionsStream(chatPayload, (delta) => {
      fullContent += delta;
      setStreamingContent(fullContent);
    });
    setRuntimeHeaders(response.headers);

    if (response.finishReason === "tool_calls" && response.toolCalls.length > 0) {
      setStreamingContent(null);
      const assistantMsg = buildAssistantToolCallMessage(fullContent, response.toolCalls);
      setPendingThread([...toolCallBaseMessages, assistantMsg]);
      setPendingToolCalls(response.toolCalls.map((tc) => ({ ...tc, result: "" })));
      return { kind: "tool_calls" };
    }

    return {
      kind: "completed",
      headers: response.headers,
      chatResult: buildSyntheticChatResult(response.headers, model, fullContent),
    };
  }

  async function resetBudget() {
    if (!budget) {
      return;
    }
    setBudgetActionError("");
    setNotice(null);

    if (!window.confirm("Reset tracked budget usage for the current scope?")) {
      return;
    }

    try {
      await resetBudgetRequest(
        {
          scope: budget.scope,
          provider: budget.provider,
          key: budget.scope === "custom" ? budget.key : "",
        },
      );
      await loadDashboard();
      setNotice({ kind: "success", message: "Budget usage reset." });
      return;
    } catch {
      setBudgetActionError("failed to reset budget usage");
      setNotice({ kind: "error", message: "Failed to reset budget usage." });
    }
  }

  async function topUpBudget() {
    if (!budget) {
      return;
    }
    setBudgetActionError("");

    const amountMicrosUSD = usdToMicros(budgetAmountUsd);
    if (!Number.isFinite(amountMicrosUSD) || amountMicrosUSD <= 0) {
      setBudgetActionError("top-up amount must be greater than zero");
      return;
    }

    try {
      await topUpBudgetRequest(
        {
          scope: budget.scope,
          provider: budget.provider,
          key: budget.scope === "custom" ? budget.key : "",
          amount_micros_usd: amountMicrosUSD,
        },
      );
      await loadDashboard();
      setNotice({ kind: "success", message: "Budget topped up." });
      return;
    } catch (error) {
      setBudgetActionError(error instanceof Error ? error.message : "failed to top up budget");
      setNotice({ kind: "error", message: "Failed to top up budget." });
    }
  }

  async function setBudgetLimit() {
    if (!budget) {
      return;
    }
    setBudgetActionError("");

    const limitMicrosUSD = usdToMicros(budgetLimitUsd);
    if (!Number.isFinite(limitMicrosUSD) || limitMicrosUSD < 0) {
      setBudgetActionError("limit must be zero or greater");
      return;
    }

    try {
      await setBudgetLimitRequest(
        {
          scope: budget.scope,
          provider: budget.provider,
          key: budget.scope === "custom" ? budget.key : "",
          balance_micros_usd: limitMicrosUSD,
        },
      );
      await loadDashboard();
      setNotice({ kind: "success", message: "Budget limit updated." });
      return;
    } catch (error) {
      setBudgetActionError(error instanceof Error ? error.message : "failed to set budget limit");
      setNotice({ kind: "error", message: "Failed to update budget limit." });
    }
  }

  function setNoticeMessage(kind: NoticeState["kind"], message: string) {
    if (message) setNotice({ kind, message });
  }

  function describeError(error: unknown, fallback: string): string {
    return error instanceof Error ? error.message : fallback;
  }

  function resetSettingsFeedback() {
    setSettingsError("");
    setNotice(null);
  }

  async function runSettingsMutation(options: {
    action: () => Promise<void>;
    successMessage: string;
    errorMessage: string;
    failureDetail: string;
  }) {
    resetSettingsFeedback();
    try {
      await options.action();
      await loadDashboard();
      setNoticeMessage("success", options.successMessage);
    } catch (error) {
      setSettingsError(describeError(error, options.failureDetail));
      setNoticeMessage("error", options.errorMessage);
    }
  }

  // setProviderAPIKey is the single operation for managing a provider's API key.
  // An empty `key` clears the existing credential; non-empty sets/replaces it.
  async function setProviderAPIKey(id: string, key: string) {
    await runSettingsMutation({
      successMessage: key === "" ? "API key cleared." : "API key saved.",
      errorMessage: key === "" ? "Failed to clear API key." : "Failed to save API key.",
      failureDetail: key === "" ? "failed to clear provider api key" : "failed to save provider api key",
      action: async () => {
        await setProviderAPIKeyRequest(id, key);
      },
    });
  }

  async function createProvider(
    params: { name: string; preset_id?: string; custom_name?: string; base_url?: string; api_key?: string; kind: string; protocol: string },
    options: { refresh?: boolean } = {},
  ): Promise<void> {
    await createProviderRequest(params);
    if (options.refresh !== false) {
      await loadDashboard();
    }
  }

  async function deleteProvider(id: string): Promise<void> {
    resetSettingsFeedback();
    const removedConfiguredProviderIndex = settingsConfig?.providers.findIndex(provider => provider.id === id) ?? -1;
    const removedProviderStatusIndex = providers.findIndex(provider => provider.name === id);
    const removedConfiguredProvider = removedConfiguredProviderIndex >= 0
      ? settingsConfig?.providers[removedConfiguredProviderIndex]
      : undefined;
    const removedProviderStatus = removedProviderStatusIndex >= 0
      ? providers[removedProviderStatusIndex]
      : undefined;
    const previousProviderFilter = providerFilter;
    const previousModel = model;

    setSettingsConfig(current => current
      ? { ...current, providers: current.providers.filter(provider => provider.id !== id) }
      : current);
    setProviders(current => current.filter(provider => provider.name !== id));
    if (providerFilter === id) {
      setProviderFilter("auto");
      setModel(defaultModelForProvider("auto", models, providers.filter(provider => provider.name !== id), providerPresets));
    }

    try {
      await deleteProviderRequest(id);
      setNoticeMessage("success", "Provider removed.");
      void loadDashboard();
    } catch (error) {
      setSettingsConfig(current => {
        if (!removedConfiguredProvider) return current;
        if (!current) return current;
        if (current.providers.some(provider => provider.id === id)) return current;
        return {
          ...current,
          providers: insertAtIndex(current.providers, removedConfiguredProvider, removedConfiguredProviderIndex),
        };
      });
      setProviders(current => {
        if (!removedProviderStatus || current.some(provider => provider.name === id)) return current;
        return insertAtIndex(current, removedProviderStatus, removedProviderStatusIndex);
      });
      setProviderFilter(previousProviderFilter);
      setModel(previousModel);
      setSettingsError(describeError(error, "failed to delete provider"));
      setNoticeMessage("error", "Failed to remove provider.");
      void refreshProviders();
    }
  }

  async function setProviderBaseURL(id: string, baseURL: string): Promise<void> {
    await setProviderBaseURLRequest(id, baseURL);
    // loadDashboard refreshes settingsConfig (the source of truth for base_url
    // shown in the table), then refreshProviders re-runs model discovery
    // against the new endpoint so the model list updates immediately.
    await loadDashboard();
    await refreshProviders();
  }

  async function setProviderName(id: string, name: string): Promise<void> {
    await setProviderNameRequest(id, name);
    // The label change only affects settingsConfig (table column) — no need
    // to rerun model discovery, so skip refreshProviders.
    await loadDashboard();
  }

  async function setProviderCustomName(id: string, customName: string): Promise<void> {
    await setProviderCustomNameRequest(id, customName);
    await loadDashboard();
  }

  async function upsertModelCapabilityOverride(payload: ModelCapabilityUpsertPayload): Promise<boolean> {
    try {
      await upsertModelCapabilityOverrideRequest(payload);
      await loadDashboard();
      setNoticeMessage("success", "Model capability override saved.");
      return true;
    } catch (error) {
      setNoticeMessage("error", error instanceof Error ? error.message : "Failed to save model capability override.");
      return false;
    }
  }

  async function recordModelCapabilityProbe(payload: ModelCapabilityUpsertPayload): Promise<boolean> {
    try {
      await recordModelCapabilityProbeRequest(payload);
      await loadDashboard();
      setNoticeMessage("success", "Manual capability result recorded.");
      return true;
    } catch (error) {
      setNoticeMessage("error", error instanceof Error ? error.message : "Failed to record capability result.");
      return false;
    }
  }

  async function deleteModelCapabilityOverride(provider: string, modelName: string): Promise<boolean> {
    try {
      await deleteModelCapabilityOverrideRequest(provider, modelName);
      await loadDashboard();
      setNoticeMessage("success", "Model capability override cleared.");
      return true;
    } catch (error) {
      setNoticeMessage("error", error instanceof Error ? error.message : "Failed to clear model capability override.");
      return false;
    }
  }

  // Policy rule mutations follow the same runSettingsMutation contract
  // as the tenant / API key flows: success populates the toast notice
  // + clears settingsError; failure populates BOTH inline banner
  // and toast so an operator can't miss the error regardless of
  // viewport focus.
  async function upsertPolicyRule(payload: PolicyRuleUpsertPayload) {
    await runSettingsMutation({
      successMessage: "Policy rule saved.",
      errorMessage: "Failed to save policy rule.",
      failureDetail: "failed to save policy rule",
      action: async () => {
        await upsertPolicyRuleRequest(payload);
      },
    });
  }

  async function deletePolicyRule(id: string) {
    await runSettingsMutation({
      successMessage: "Policy rule deleted.",
      errorMessage: "Failed to delete policy rule.",
      failureDetail: "failed to delete policy rule",
      action: async () => {
        await deletePolicyRuleRequest(id);
      },
    });
  }

  async function upsertPricebookEntry(entry: PricebookEntryUpsertPayload) {
    await runSettingsMutation({
      successMessage: "Pricebook entry saved.",
      errorMessage: "Failed to save pricebook entry.",
      failureDetail: "failed to save pricebook entry",
      action: async () => {
        await upsertPricebookEntryRequest(entry);
      },
    });
  }

  async function deletePricebookEntry(provider: string, model: string) {
    // Confirmation is the caller's concern now (PricebookTab routes
    // this through a styled ConfirmModal). The action itself just
    // performs the deletion.
    resetSettingsFeedback();
    await runSettingsMutation({
      successMessage: "Price cleared.",
      errorMessage: "Failed to clear price.",
      failureDetail: "failed to clear pricebook entry",
      action: async () => {
        await deletePricebookEntryRequest(provider, model);
      },
    });
  }

  // previewPricebookImport intentionally does NOT call runSettingsMutation —
  // it doesn't mutate anything. It just fetches the diff and lets the
  // caller (the import modal) render it.
  async function previewPricebookImport(): Promise<PricebookImportDiff> {
    const response = await previewPricebookImportRequest();
    return response.data;
  }

  async function applyPricebookImport(keys: string[]): Promise<PricebookImportDiff> {
    const response = await applyPricebookImportRequest(keys);
    await loadDashboard();
    // Notice text varies with the partial-success outcome so the
    // operator sees the exact tally — silent "import applied" was
    // misleading when one or more rows actually failed.
    const data = response.data;
    const appliedCount = data.applied?.length ?? 0;
    const failedCount = data.failed?.length ?? 0;
    if (failedCount > 0 && appliedCount > 0) {
      setNoticeMessage("error", `Imported ${appliedCount}, ${failedCount} failed.`);
    } else if (failedCount > 0) {
      setNoticeMessage("error", `Import failed for ${failedCount} ${failedCount === 1 ? "row" : "rows"}.`);
    } else {
      setNoticeMessage("success", `Imported ${appliedCount} ${appliedCount === 1 ? "row" : "rows"}.`);
    }
    return data;
  }

  async function copyCommand(command: string) {
    try {
      await navigator.clipboard.writeText(command);
      setCopiedCommand(command);
      window.setTimeout(() => {
        setCopiedCommand((current) => (current === command ? "" : current));
      }, 1500);
    } catch {
      setCopiedCommand("");
    }
  }

  async function runRetention() {
    setRetentionError("");
    setNotice(null);
    setRetentionLoading(true);
    try {
      const payload = await runRetentionRequest(
        {
          subsystems: parseCSV(retentionSubsystems),
        },
      );
      setRetentionLastRun(payload.data);
      setRetentionRuns((current) => [payload.data, ...current.filter((run) => run.finished_at !== payload.data.finished_at)].slice(0, 10));
      setNotice({ kind: "success", message: "Retention run completed." });
    } catch (error) {
      setRetentionError(error instanceof Error ? error.message : "failed to run retention");
      setNotice({ kind: "error", message: "Failed to run retention." });
    } finally {
      setRetentionLoading(false);
    }
  }

  function createChatSession() {
    startNewChat();
  }

  async function selectChatSession(id: string) {
    await selectAgentChatSession(id);
  }

  async function selectAgentChatSession(id: string) {
    setActiveAgentChatSessionID(id);
    if (!id) {
      setActiveAgentChatSession(null);
      return;
    }
    try {
      const payload = await getAgentChatSession(id);
      setActiveAgentChatSession(payload.data);
      if (payload.data.adapter_id) {
        setAgentAdapterID(payload.data.adapter_id);
      }
      const selection = deriveHecateChatSelectionFromSession(payload.data);
      if (selection.provider) {
        setProviderFilter(selection.provider as ProviderFilter);
      }
      if (selection.model) {
        setModel(selection.model);
      }
      if (payload.data.workspace) {
        setAgentWorkspace(payload.data.workspace);
        setAgentWorkspaceBranch(payload.data.workspace_branch ?? "");
      }
    } catch (error) {
      const msg = error instanceof Error ? error.message : "failed to load agent chat";
      setActiveAgentChatSessionID("");
      setActiveAgentChatSession(null);
      setAgentWorkspaceBranch("");
      setChatErrorState(error, "failed to load agent chat");
      setNoticeMessage("error", msg);
    }
  }

  function startNewChat() {
    if (activeAgentChatSessionID) {
      setQueuedChatMessages((current) => current.filter((item) => item.session_id !== activeAgentChatSessionID));
    }
    setActiveAgentChatSessionID("");
    setActiveAgentChatSession(null);
    setAgentWorkspaceBranch("");
    setDefaultChatTarget("agent");
    resetChatWorkspaceState();
  }

  async function deleteChatSession(id: string) {
    await deleteAgentChatSession(id);
  }

  async function deleteAgentChatSession(id: string) {
    try {
      await deleteAgentChatSessionRequest(id);
      setAgentChatSessions((current) => current.filter((s) => s.id !== id));
      setQueuedChatMessages((current) => current.filter((item) => item.session_id !== id));
      setChatTargetBySessionID((current) => {
        if (!current.has(id)) return current;
        const next = new Map(current);
        next.delete(id);
        return next;
      });
      if (activeAgentChatSessionID === id) {
        startNewChat();
      }
      setNoticeMessage("success", "Agent chat deleted.");
    } catch (error) {
      setNoticeMessage("error", error instanceof Error ? error.message : "Failed to delete agent chat.");
    }
  }

  // getAgentChatApproval is the modal-open path: fetches the full
  // approval row (ACP options, scope choices, decision_note, …).
  // Returns null on failure so the caller can render an error state.
  async function getAgentChatApproval(
    sessionID: string,
    approvalID: string,
  ): Promise<AgentChatApprovalRecord | null> {
    try {
      const payload = await getAgentChatApprovalRequest(sessionID, approvalID);
      return payload.data;
    } catch (error) {
      setNoticeMessage("error", error instanceof Error ? error.message : "Failed to load approval.");
      return null;
    }
  }

  async function resolveAgentChatApproval(
    sessionID: string,
    approvalID: string,
    decision: ResolveAgentChatApprovalPayload,
  ): Promise<boolean> {
    try {
      await resolveAgentChatApprovalRequest(sessionID, approvalID, decision);
      // Optimistic removal: the SSE `approval.resolved` event will
      // also fire and remove the row, but updating immediately keeps
      // the banner snappy when the operator closes the modal.
      removePendingApproval(sessionID, approvalID);
      return true;
    } catch (error) {
      setNoticeMessage("error", error instanceof Error ? error.message : "Failed to resolve approval.");
      return false;
    }
  }

  async function cancelAgentChatApproval(sessionID: string, approvalID: string): Promise<boolean> {
    try {
      await cancelAgentChatApprovalRequest(sessionID, approvalID);
      removePendingApproval(sessionID, approvalID);
      return true;
    } catch (error) {
      setNoticeMessage("error", error instanceof Error ? error.message : "Failed to cancel approval.");
      return false;
    }
  }

  async function resolveTaskApproval(
    taskID: string,
    approvalID: string,
    decision: ResolveTaskApprovalPayload,
  ): Promise<boolean> {
    const status = decision.decision === "approve" ? "approved" : "rejected";
    // Capture the pre-resolve session synchronously from closure so
    // we can roll back if the API call fails. We can't capture inside
    // the state updater function because React invokes it
    // asynchronously (and may invoke it twice under StrictMode); by
    // the time the catch branch runs, the closure variable would
    // either still be null or hold the already-patched state. Same
    // pattern as deleteProvider above.
    //
    // Optimistic-update-before-call means the banner row disappears
    // the moment the operator clicks; before this, the row hung
    // around for the full network round-trip (50–500 ms), which
    // looked unresponsive on slow links and let an operator
    // double-click a duplicate request through.
    const snapshot: AgentChatSessionRecord | null =
      activeAgentChatSession && activeAgentChatSession.task_id === taskID
        ? activeAgentChatSession
        : null;
    if (snapshot) {
      setActiveAgentChatSession((current) => {
        if (!current || current.task_id !== taskID) return current;
        return {
          ...current,
          messages: (current.messages ?? []).map((message) => ({
            ...message,
            activities: message.activities?.map((activity) => {
              if (activity.approval_id !== approvalID && activity.id !== `task:approval:${approvalID}`) return activity;
              return { ...activity, status, needs_action: false };
            }),
          })),
        };
      });
    }
    // rollbackOptimisticApproval restores the specific approval
    // activity from the pre-resolve snapshot, while leaving every
    // other field of the active session untouched. Two concurrency
    // hazards force this surgical shape rather than
    // `setActiveAgentChatSession(snapshot)`:
    //
    //   1. The operator may have navigated to a different session
    //      while the request was in flight. The functional updater
    //      bails when the active session id has changed.
    //   2. A stream `session_update` or a refresh may have applied
    //      newer messages/activities on top of the optimistic
    //      state. Restoring only the specific approval activity
    //      preserves them.
    //
    // Reused by both the generic-failure path AND the
    // not-pending+refresh-failed path so both cases produce the
    // same operator-visible state ("we're not sure what the
    // server thinks; show the row as still pending so the
    // operator can retry") instead of leaving a possibly-wrong
    // optimistic decision on screen.
    const rollbackOptimisticApproval = () => {
      if (!snapshot) return;
      const snapshotForRollback = snapshot;
      // Predicate matches the activity by approval_id (or the
      // projected `task:approval:<id>` id). Using the SAME
      // predicate on both sides matters because Activity.id is
      // optional — matching by id alone could (a) fail to restore
      // when the current row has no id and (b) wrongly match the
      // first id-less row if both sides have undefined ids.
      const matchesTargetApproval = (activity: AgentChatActivityRecord) =>
        activity.approval_id === approvalID || activity.id === `task:approval:${approvalID}`;
      setActiveAgentChatSession((current) => {
        if (!current || current.id !== snapshotForRollback.id) return current;
        return {
          ...current,
          messages: (current.messages ?? []).map((message) => {
            const originalMessage = snapshotForRollback.messages?.find((m) => m.id === message.id);
            if (!originalMessage) return message;
            return {
              ...message,
              activities: message.activities?.map((activity) => {
                if (!matchesTargetApproval(activity)) return activity;
                const originalActivity = originalMessage.activities?.find(matchesTargetApproval);
                return originalActivity ?? activity;
              }),
            };
          }),
        };
      });
    };

    try {
      await resolveTaskApprovalRequest(taskID, approvalID, decision);
      if (activeAgentChatSessionID) {
        try {
          await refreshAgentChatSession(activeAgentChatSessionID);
        } catch {
          // The local approval state above already removes the action;
          // a follow-up session refresh is best-effort because the run
          // may still be transitioning after the operator decision.
        }
      }
      return true;
    } catch (error) {
      if (error instanceof Error && /not pending/i.test(error.message)) {
        // Server says the approval is already resolved. The
        // resolution may NOT match the operator's chosen decision —
        // another tab could have approved while this one tried to
        // reject, the run might have timed out into auto-rejection,
        // or the run could have been cancelled. Refresh to pull
        // server-truth and let it overwrite our optimistic patch.
        if (activeAgentChatSessionID) {
          try {
            await refreshAgentChatSession(activeAgentChatSessionID);
            return true;
          } catch {
            // Refresh failed — we cannot trust our optimistic patch
            // (it might claim a decision the server didn't make).
            // Fall through to rollback so the row reflects "still
            // pending" rather than a possibly-wrong final state.
          }
        }
        rollbackOptimisticApproval();
        setNoticeMessage("error", "Approval was already resolved upstream and the session refresh failed; reload to see the current state.");
        return false;
      }
      // Genuine failure — roll back so the row reappears and the
      // operator can retry.
      rollbackOptimisticApproval();
      setNoticeMessage("error", error instanceof Error ? error.message : "Failed to resolve task approval.");
      return false;
    }
  }

  async function listAgentChatGrants(filter: AgentChatGrantFilter = {}): Promise<void> {
    setAgentChatGrantsLoading(true);
    setAgentChatGrantsError("");
    try {
      const payload = await listAgentChatGrantsRequest(filter);
      setAgentChatGrants(payload.data ?? []);
    } catch (error) {
      const msg = error instanceof Error ? error.message : "failed to load grants";
      setAgentChatGrantsError(msg);
    } finally {
      setAgentChatGrantsLoading(false);
    }
  }

  async function deleteAgentChatGrant(grantID: string): Promise<boolean> {
    try {
      await deleteAgentChatGrantRequest(grantID);
      setAgentChatGrants((current) => current.filter((g) => g.id !== grantID));
      setNoticeMessage("success", "Grant revoked.");
      return true;
    } catch (error) {
      setNoticeMessage("error", error instanceof Error ? error.message : "Failed to revoke grant.");
      return false;
    }
  }

  async function listAgentChatMessageFiles(sessionID: string, messageID: string): Promise<AgentChatChangedFileRecord[]> {
    try {
      const payload = await listAgentChatMessageFilesRequest(sessionID, messageID);
      return payload.data ?? [];
    } catch (error) {
      setNoticeMessage("error", error instanceof Error ? error.message : "Failed to load changed files.");
      return [];
    }
  }

  async function getAgentChatMessageFileDiff(sessionID: string, messageID: string, path: string): Promise<AgentChatChangedFileDiffRecord | null> {
    try {
      const payload = await getAgentChatMessageFileDiffRequest(sessionID, messageID, path);
      return payload.data;
    } catch (error) {
      setNoticeMessage("error", error instanceof Error ? error.message : "Failed to load file diff.");
      return null;
    }
  }

  async function revertAgentChatMessageFiles(sessionID: string, messageID: string, paths: string[]): Promise<boolean> {
    try {
      await revertAgentChatMessageFilesRequest(sessionID, messageID, paths);
      await refreshAgentChatSession(sessionID);
      setNoticeMessage("success", paths.length > 0 ? "Selected files reverted." : "Captured diff reverted.");
      return true;
    } catch (error) {
      setNoticeMessage("error", error instanceof Error ? error.message : "Failed to revert changed files.");
      return false;
    }
  }

  // probeAgentAdapter exercises the configured adapter and caches the
  // typed result keyed by adapter id. Operators trigger this via the
  // "Test" button in Settings → External agents; the result drives
  // the status chip + the picker dropdown's inline diagnostic. The
  // loading map is keyed by id so two adapters can be probing
  // concurrently without confusing the UI.
  async function probeAgentAdapter(adapterID: string): Promise<AgentAdapterHealthRecord | null> {
    if (!adapterID) return null;
    setAgentAdapterHealthLoadingByID((current) => {
      const next = new Map(current);
      next.set(adapterID, true);
      return next;
    });
    try {
      const payload = await probeAgentAdapterRequest(adapterID);
      setAgentAdapterHealthByID((current) => {
        const next = new Map(current);
        next.set(adapterID, payload.data.health);
        return next;
      });
      setAgentAdapters((current) => current.map((item) => item.id === adapterID ? payload.data.adapter : item));
      return payload.data.health;
    } catch (error) {
      setNoticeMessage("error", error instanceof Error ? error.message : "Failed to probe adapter.");
      return null;
    } finally {
      setAgentAdapterHealthLoadingByID((current) => {
        if (!current.has(adapterID)) return current;
        const next = new Map(current);
        next.delete(adapterID);
        return next;
      });
    }
  }

  async function renameChatSession(id: string, title: string) {
    try {
      const payload = await updateChatSessionRequest(id, title);
      setChatSessions((current) =>
        current.map((s) => (s.id === id ? { ...s, title: payload.data.title } : s)),
      );
      if (activeChatSessionID === id) {
        setActiveChatSession((current) => (current ? { ...current, title: payload.data.title } : current));
      }
    } catch (error) {
      setNoticeMessage("error", error instanceof Error ? error.message : "Failed to rename chat.");
    }
  }

  async function loadMoreChatSessions() {
    if (chatSessionsLoadingMore || !chatSessionsHasMore) return;
    setChatSessionsLoadingMore(true);
    try {
      const result = await getChatSessions(20, chatSessions.length);
      setChatSessions((current) => [...current, ...(result.data ?? [])]);
      setChatSessionsHasMore(result.has_more ?? false);
    } catch {
      // Keep sidebar responsive; silently skip failed page loads.
    } finally {
      setChatSessionsLoadingMore(false);
    }
  }

  async function chooseAgentWorkspace(): Promise<boolean> {
    clearChatErrorState();
    try {
      const payload = await chooseWorkspaceDirectoryRequest();
      if (payload.data.path) {
        setAgentWorkspace(payload.data.path);
        setAgentWorkspaceBranch(payload.data.branch ?? "");
      }
      return true;
    } catch (error) {
      setChatErrorState(error, "workspace folder dialog is unavailable");
      return false;
    }
  }

  return {
    state: {
      activeAgentChatSession,
      activeAgentChatSessionID,
      budget,
      accountSummary,
      agentAdapterID,
      agentAdapters,
      agentChatCancelling,
      agentChatSessions,
      agentWorkspace,
      agentWorkspaceBranch,
      requestLedger,
      budgetActionError,
      budgetAmountUsd,
      budgetLimitUsd,
      chatError,
      chatErrorAction,
      chatErrorCode,
      chatErrorRequestID,
      chatErrorStatus,
      chatErrorTraceID,
      chatLoading,
      streamingContent,
      chatResult,
      chatTarget,
      pendingToolCalls,
      queuedChatMessages,
      chatSessions,
      cloudModels,
      cloudProviders,
      settingsConfig,
      settingsError,
      copiedCommand,
      error,
      health,
      healthyCloudProviders,
      healthyLocalProviders,
      healthyProviders,
      loading,
      localModels,
      localProviderIssues,
      localProviders,
      message,
      systemPrompt,
      model,
      modelFilter,
      models,
      notice,
      session,
      providerFilter,
      providerScopedModels,
      providers,
      providerPresets,
      activeChatSession,
      activeChatSessionID,
      retentionError,
      retentionLastRun,
      retentionLoading,
      retentionRuns,
      retentionSubsystems,
      runtimeHeaders,
      chatSessionsHasMore,
      chatSessionsLoadingMore,
      visibleModels,
      pendingApprovalsBySessionID,
      agentChatGrants,
      agentChatGrantsLoading,
      agentChatGrantsError,
      agentAdapterApprovalMode,
      agentAdapterHealthByID,
      agentAdapterHealthLoadingByID,
    },
    actions: {
      copyCommand,
      cancelAgentChat,
      deletePolicyRule,
      chooseAgentWorkspace,
      createChatSession,
      deleteChatSession,
      renameChatSession,
      loadDashboard,
      resetBudget,
      setBudgetAmountUsd,
      setBudgetLimitUsd,
      setAgentAdapterID,
      setAgentWorkspace: updateAgentWorkspace,
      setChatTarget,
      setMessage,
      removeQueuedChatMessage,
      updateQueuedChatMessage,
      setSystemPrompt,
      setModel,
      setModelFilter,
      setProviderFilter: selectProviderRoute,
      refreshProviders,
      setRetentionSubsystems,
      setBudgetLimit,
      runRetention,
      selectChatSession,
      startNewChat,
      submitChat,
      loadMoreChatSessions,
      submitToolResults,
      updateToolResult,
      topUpBudget,
      upsertPolicyRule,
      setProviderAPIKey,
      createProvider,
      deleteProvider,
      setProviderBaseURL,
      setProviderName,
      setProviderCustomName,
      upsertModelCapabilityOverride,
      recordModelCapabilityProbe,
      deleteModelCapabilityOverride,
      upsertPricebookEntry,
      deletePricebookEntry,
      previewPricebookImport,
      applyPricebookImport,
      getAgentChatApproval,
      listAgentChatMessageFiles,
      getAgentChatMessageFileDiff,
      revertAgentChatMessageFiles,
      resolveTaskApproval,
      resolveAgentChatApproval,
      cancelAgentChatApproval,
      listAgentChatGrants,
      deleteAgentChatGrant,
      probeAgentAdapter,
      dismissNotice: () => setNotice(null),
    },
  };
}

function insertAtIndex<T>(items: T[], item: T, index: number): T[] {
  const next = items.slice();
  const boundedIndex = Math.max(0, Math.min(index, next.length));
  next.splice(boundedIndex, 0, item);
  return next;
}

export type RuntimeConsoleViewModel = ReturnType<typeof useRuntimeConsole>;
