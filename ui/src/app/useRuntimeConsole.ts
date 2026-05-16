import { useEffect, useMemo, useRef, useState, type SyntheticEvent } from "react";

import { parseStoredJSON, parseStoredString, usePersistedState } from "../lib/persistedState";
import {
  type ChatTarget,
  type HecateChatTarget,
  type QueuedChatMessage,
  normalizeStoredHecateChatTarget,
  parseChatTargetsBySessionID,
  parseQueuedChatMessageList,
  parseStoredChatTarget,
  queuedChatMessagesStorageKey,
  serializeChatTargetsBySessionID,
} from "./state/_shared";
import { buildLocalProviderIssue } from "../lib/provider-issues";
import type { LocalProviderIssue } from "../lib/provider-issues";
import { filterModelsByKind, filterModelsByProvider } from "../lib/runtime-utils";
import {
  ApiError,
  type ChatMessage,
  chooseWorkspaceDirectory as chooseWorkspaceDirectoryRequest,
  chatCompletionsStream,
  updateChatSession as updateChatSessionRequest,
  deletePolicyRule as deletePolicyRuleRequest,
  getChatSession,
  getChatSessions,
  getModels,
  getProviders,
  getUsageEvents,
  getUsageSummary,
  setProviderAPIKey as setProviderAPIKeyRequest,
  cancelAgentChatSession as cancelAgentChatSessionRequest,
  getAgentChatMessageFileDiff as getAgentChatMessageFileDiffRequest,
  listAgentChatMessageFiles as listAgentChatMessageFilesRequest,
  probeAgentAdapter as probeAgentAdapterRequest,
  setAgentAdapterCredential as setAgentAdapterCredentialRequest,
  deleteAgentAdapterCredential as deleteAgentAdapterCredentialRequest,
  revertAgentChatMessageFiles as revertAgentChatMessageFilesRequest,
  resolveTaskApproval as resolveTaskApprovalRequest,
  upsertPolicyRule as upsertPolicyRuleRequest,
  createProvider as createProviderRequest,
  deleteModelCapabilityOverride as deleteModelCapabilityOverrideRequest,
  createAgentChatMessage as createAgentChatMessageRequest,
  createAgentChatSession as createAgentChatSessionRequest,
  deleteAgentChatSession as deleteAgentChatSessionRequest,
  deleteProvider as deleteProviderRequest,
  getAgentChatSession,
  updateAgentChatSession as updateAgentChatSessionRequest,
  recordModelCapabilityProbe as recordModelCapabilityProbeRequest,
  setAgentChatConfigOption as setAgentChatConfigOptionRequest,
  setAgentChatSettings as setAgentChatSettingsRequest,
  streamAgentChatSession,
  setProviderBaseURL as setProviderBaseURLRequest,
  setProviderName as setProviderNameRequest,
  setProviderCustomName as setProviderCustomNameRequest,
  upsertModelCapabilityOverride as upsertModelCapabilityOverrideRequest,
} from "../lib/api";
import type { PolicyRuleUpsertPayload, ResolveAgentChatApprovalPayload, ResolveTaskApprovalPayload, ModelCapabilityUpsertPayload } from "../lib/api";
import {
  buildAssistantToolCallMessage,
  buildSyntheticChatResult,
  defaultModelForProvider,
  defaultProviderForChat,
  deriveChatSessionTitle,
  humanizeChatError,
  isModelValidForProvider,
  renderAgentChatSessionSummary,
} from "./runtimeConsoleChatHelpers";
import { deriveSessionState, resolveDashboardSnapshot } from "./runtimeConsoleDashboard";
import { useApprovals } from "./state/approvals";
import { useRetention } from "./state/retention";
import { useRuntime } from "./state/runtime";
import { useUsage } from "./state/usage";
import type {
  AgentAdapterHealthRecord,
  AgentAdapterRecord,
  AgentChatApprovalRecord,
  AgentChatActivityRecord,
  AgentChatChangedFileDiffRecord,
  AgentChatChangedFileRecord,
  AgentChatSessionRecord,
  AgentChatSessionsResponse,
  ChatResponse,
  ChatSessionRecord,
  ChatSessionsResponse,
  ConfiguredStateResponse,
  ModelFilter,
  ModelResponse,
  ProviderPresetRecord,
  ProviderFilter,
  ProviderStatusResponse,
  RuntimeHeaders,
} from "../types/runtime";

type NoticeState = {
  kind: "success" | "error";
  message: string;
};

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

export { humanizeChatError };

export function useRuntimeConsole() {
  // Runtime slice: gateway health, session info, loading/error/
  // message banners, runtime response headers, copy-to-clipboard
  // transient, and the Hecate RTK availability bits. Aliased to
  // legacy identifiers below so the rest of the hook body reads
  // identically to its pre-slice form.
  const runtime = useRuntime();
  const {
    health,
    sessionInfo,
    loading,
    error,
    message,
    runtimeHeaders,
    copiedCommand,
    hecateRTKEnabled,
    hecateRTKAvailable,
    hecateRTKPath,
  } = runtime.state;
  const {
    setHealth,
    setSessionInfo,
    setLoading,
    setError,
    setMessage,
    setRuntimeHeaders,
    setHecateRTKAvailable,
    setHecateRTKPath,
    copyCommand,
  } = runtime.actions;
  // Legacy alias: the previous code used `setHecateRTKEnabledState`
  // for the raw state setter and reserved `setHecateRTKEnabled` for
  // the coordinator below (PATCHes the session and rolls back on
  // failure). Preserving the naming keeps the coordinator's body
  // unchanged.
  const setHecateRTKEnabledState = runtime.actions.setHecateRTKEnabled;

  // Usage slice: cost summary + recent events. Aliased to legacy
  // identifiers so the dashboard fan-out call sites read unchanged.
  const usage = useUsage();
  const usageSummary = usage.state.summary;
  const usageEvents = usage.state.events;
  const setUsageSummary = usage.actions.setSummary;
  const setUsageEvents = usage.actions.setEvents;

  const [models, setModels] = useState<ModelResponse["data"]>([]);
  const [providers, setProviders] = useState<ProviderStatusResponse["data"]>([]);
  const [providerPresets, setProviderPresets] = useState<ProviderPresetRecord[]>([]);
  const [agentAdapters, setAgentAdapters] = useState<AgentAdapterRecord[]>([]);
  const [defaultChatTarget, setDefaultChatTarget] = usePersistedState<ChatTarget>(
    "hecate.chatTarget",
    parseStoredChatTarget,
    "agent",
  );
  const [chatTargetBySessionID, setChatTargetBySessionID] = usePersistedState<Map<string, HecateChatTarget>>(
    "hecate.chatTargetBySessionID",
    parseStoredJSON(parseChatTargetsBySessionID),
    new Map(),
    { serialize: serializeChatTargetsBySessionID },
  );
  const [agentAdapterID, setAgentAdapterID] = usePersistedState(
    "hecate.agentAdapterID",
    parseStoredString,
    "codex",
  );
  const [agentWorkspace, setAgentWorkspace] = usePersistedState(
    "hecate.agentWorkspace",
    parseStoredString,
    "",
  );
  const [agentWorkspaceBranch, setAgentWorkspaceBranch] = useState("");
  const [agentChatSessions, setAgentChatSessions] = useState<AgentChatSessionsResponse["data"]>([]);
  const [activeAgentChatSessionID, setActiveAgentChatSessionID] = usePersistedState(
    "hecate.agentChatSessionID",
    parseStoredString,
    "",
    { shouldRemove: (v) => v === "" },
  );
  const [activeAgentChatSession, setActiveAgentChatSession] = useState<AgentChatSessionRecord | null>(null);
  // Approvals state + actions live in the slice (app/state/approvals.tsx).
  // The slice owns the pending-banner map, the per-session mutation
  // version that protects the catch-up GET against races, the grant
  // list, and the request actions. The shim below re-exports the
  // state under the legacy `state.{pendingApprovalsBySessionID,
  // agentChatGrants,agentChatGrantsLoading,agentChatGrantsError}`
  // keys and the actions under their legacy identifiers.
  const approvals = useApprovals();
  // agentAdapterApprovalMode mirrors the runtime-stats field of the
  // same name. Empty until the dashboard fan-out resolves. The Chats
  // workspace surfaces a danger banner when this is "auto" — every
  // adapter RequestPermission is permitted without operator review.
  const [agentAdapterApprovalMode, setAgentAdapterApprovalMode] = useState<string>("");
  // agentAdapterHealthByID stores the most recent probe result per
  // adapter, keyed by adapter id. Operators trigger a probe via the
  // readiness probe in Connections and the result is
  // cached here so the picker dropdown can show a status chip without
  // re-running the probe. Map instance is replaced on update — same
  // invariant as pendingApprovalsBySessionID.
  const [agentAdapterHealthByID, setAgentAdapterHealthByID] = useState<
    Map<string, AgentAdapterHealthRecord>
  >(() => new Map());
  const [agentAdapterHealthLoadingByID, setAgentAdapterHealthLoadingByID] = useState<
    Map<string, true>
  >(() => new Map());
  const [settingsConfig, setSettingsConfig] = useState<ConfiguredStateResponse["data"] | null>(null);

  const [model, setModel] = usePersistedState(
    "hecate.model",
    parseStoredString,
    "",
    { shouldRemove: (v) => v === "" },
  );
  const [queuedChatMessages, setQueuedChatMessages] = usePersistedState<QueuedChatMessage[]>(
    queuedChatMessagesStorageKey,
    parseStoredJSON(parseQueuedChatMessageList),
    [],
    { shouldRemove: (v) => v.length === 0 },
  );
  const queuedChatMessagesRef = useRef(queuedChatMessages);
  const [systemPrompt, setSystemPrompt] = usePersistedState(
    "hecate.systemPrompt",
    parseStoredString,
    "",
  );
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
  const [activeChatSessionID, setActiveChatSessionID] = usePersistedState(
    "hecate.chatSessionID",
    parseStoredString,
    "",
    { shouldRemove: (v) => v === "" },
  );
  const [activeChatSession, setActiveChatSession] = useState<ChatSessionRecord | null>(null);
  const [chatError, setChatError] = useState("");
  const [chatErrorCode, setChatErrorCode] = useState("");
  const [chatErrorStatus, setChatErrorStatus] = useState<number | null>(null);
  const [chatErrorAction, setChatErrorAction] = useState("");
  const [chatErrorRequestID, setChatErrorRequestID] = useState("");
  const [chatErrorTraceID, setChatErrorTraceID] = useState("");
  const [modelFilter, setModelFilter] = useState<ModelFilter>("all");
  // FIXME: providerFilter is the lone holdout from the
  // usePersistedState migration. Three e2e scenarios broke when it
  // was migrated — the `test("...")` blocks that begin at
  // chat.spec.ts:617 ("Hecate Agent local-provider onboarding…"),
  // :767 ("…tools on, tools off, then tools on again…"), and :1288
  // ("selected-model readiness can switch to the backend-suggested
  // fallback model"). None of those tests set
  // `hecate.providerFilter` directly; they exercise the
  // auto-default cascade (lines 450+) that reads providerFilter
  // through a first-render closure and only fires its
  // setProviderFilter when it sees "auto" there. With the legacy
  // mount-read effect providerFilter is "auto" on render 1 and
  // transitions on render 2, which is the window the cascade
  // expects. Seeding the persisted value directly into the lazy
  // initializer (what usePersistedState does) shifts the
  // transition out from under that cascade.
  //
  // Restructuring the auto-default + scoped-validity effects so
  // they do not depend on render-cycle timing is separate cleanup.
  // Until that lands here, keep providerFilter on the original
  // useState + mount-read + write-on-change pattern.
  const [providerFilter, setProviderFilter] = useState<ProviderFilter>("auto");
  useEffect(() => {
    const stored = window.localStorage.getItem("hecate.providerFilter");
    if (stored) setProviderFilter(stored as ProviderFilter);
  }, []);
  useEffect(() => {
    window.localStorage.setItem("hecate.providerFilter", providerFilter);
  }, [providerFilter]);

  const [settingsError, setSettingsError] = useState("");
  const [notice, setNotice] = useState<NoticeState | null>(null);

  const chatTarget = activeAgentChatSessionID && activeAgentChatSession
    ? (agentChatSessionIsExternal(activeAgentChatSession)
        ? "external_agent"
        : (chatTargetBySessionID.get(activeAgentChatSessionID) ?? deriveHecateChatTargetFromSession(activeAgentChatSession)))
    : defaultChatTarget;

  // Retention state + actions live in the slice (app/state/retention.tsx).
  // The shim below re-exports them under the legacy `state.retention*` /
  // `actions.{loadRetentionRuns,setRetentionSubsystems,runRetention}`
  // keys to keep the external surface stable for SettingsView; a later
  // refactor will retire the pass-through once SettingsView reads the
  // slice directly.
  const retention = useRetention();

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
    if (!activeAgentChatSession || agentChatSessionIsExternal(activeAgentChatSession)) {
      return;
    }
    setHecateRTKEnabledState(Boolean(activeAgentChatSession.rtk_enabled));
  }, [activeAgentChatSession?.id, activeAgentChatSession?.rtk_enabled]);

  // Mirror queuedChatMessages into a ref so non-React callers
  // (background fetches that resolve after unmount) can read the
  // latest snapshot without going through state.
  useEffect(() => {
    queuedChatMessagesRef.current = queuedChatMessages;
  }, [queuedChatMessages]);

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
    if (providerFilter !== "auto") {
      return;
    }

    const configuredProviders = settingsConfig?.providers ?? [];
    const nextProvider = defaultProviderForChat(models, configuredProviders, providers);
    if (nextProvider === providerFilter) {
      return;
    }
    setProviderFilter(nextProvider);
    setModel(defaultModelForProvider(nextProvider, models, providers, providerPresets));
  }, [models, providerFilter, providerPresets, providers, settingsConfig]);

  useEffect(() => {
    if (providerFilter === "auto") {
      return;
    }
    const hasProviderEvidence =
      models.some((entry) => entry.metadata?.provider === providerFilter) ||
      providers.some((entry) => entry.name === providerFilter) ||
      providerPresets.some((entry) => entry.id === providerFilter);
    if (model && !hasProviderEvidence) {
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
      const usageSummaryResult = await getUsageSummary("");
      setUsageSummary(usageSummaryResult.data);
    } catch {
      // Keep chat responsive even if refresh paths fail.
    }
    try {
      const usageEventsResult = await getUsageEvents(20);
      setUsageEvents(usageEventsResult.data ?? []);
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
          usageSummary,
          chatSessions,
          activeChatSession,
          agentChatSessions,
          activeAgentChatSession,
          usageEvents,
          settingsConfig,
        },
        // Commit just enough state to drop the AuthLoadingShell as
        // soon as wave 1 resolves — the activity bar + status bar
        // can render with what's here while the secondary wave
        // (chats, providers, usage, retention, …) finishes in the
        // background. The big batch update below then idempotently
        // re-applies these slots alongside the secondary results.
        onEssentials: (essentials) => {
          setHealth(essentials.health);
          setSessionInfo(essentials.sessionInfo);
          setModels(essentials.models);
          setSettingsConfig(essentials.settingsConfig);
        },
      });

      setHealth(snapshot.health);
      setSessionInfo(snapshot.sessionInfo);
      setModels(snapshot.models);
      setProviders(snapshot.providers);
      setProviderPresets(snapshot.providerPresets);
      setAgentAdapters(snapshot.agentAdapters);
      setUsageSummary(snapshot.usageSummary);
      setChatSessions(snapshot.chatSessions);
      setChatSessionsHasMore(snapshot.chatSessionsHasMore);
      setActiveChatSessionID(snapshot.activeChatSessionID);
      setActiveChatSession(snapshot.activeChatSession);
      setAgentChatSessions(snapshot.agentChatSessions);
      pruneQueuedChatMessagesForSessions(snapshot.agentChatSessions.map((session) => session.id));
      setActiveAgentChatSessionID(snapshot.activeAgentChatSessionID);
      setActiveAgentChatSession(snapshot.activeAgentChatSession);
      syncHecateSelectionFromSession(snapshot.activeAgentChatSession);
      setUsageEvents(snapshot.usageEvents);
      setSettingsConfig(snapshot.settingsConfig);
      setAgentAdapterApprovalMode(snapshot.agentAdapterApprovalMode);
      setHecateRTKAvailable(snapshot.rtkAvailable);
      setHecateRTKPath(snapshot.rtkPath);
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

  function setNewChatAgent(nextAgentID: string) {
    if (nextAgentID === "hecate") {
      setDefaultChatTarget("agent");
      return;
    }
    setAgentAdapterID(nextAgentID);
    setDefaultChatTarget("external_agent");
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
    syncHecateSelectionFromSession(session);
    setAgentWorkspaceBranch(session.workspace_branch ?? "");
    setAgentChatSessions((current) => [renderAgentChatSessionSummary(session), ...current.filter((entry) => entry.id !== session.id)]);
  }

  function syncHecateSelectionFromSession(session: AgentChatSessionRecord | null) {
    const selection = deriveHecateChatSelectionFromSession(session);
    if (selection.provider) {
      setProviderFilter(selection.provider as ProviderFilter);
    }
    if (selection.model) {
      setModel(selection.model);
    }
  }

  async function refreshAgentChatSession(sessionID: string): Promise<void> {
    const payload = await getAgentChatSession(sessionID);
    applyAgentChatSession(payload.data);
  }

  // Mutation API thin-aliases for the SSE stream handler + catch-up
  // path below. The slice owns the state + version-ref machinery;
  // these names stay stable so the call sites read the same way as
  // before the carve-out.
  const upsertPendingApproval = approvals.actions.upsertPending;
  const removePendingApproval = approvals.actions.removePending;
  const refetchPendingApprovals = approvals.actions.refetchPending;

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
      if (!isExternalAgent && !turnModel) {
        setChatError("Choose a model before sending through Hecate.");
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
          ...(!isExternalAgent ? { rtk_enabled: hecateRTKEnabled } : {}),
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
        ...(!isExternalAgent ? { system_prompt: turnSystemPrompt } : {}),
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
      const remainingProviders = providers.filter(provider => provider.name !== id);
      const remainingConfigured = settingsConfig?.providers.filter(provider => provider.id !== id) ?? [];
      const nextProvider = defaultProviderForChat(models, remainingConfigured, remainingProviders);
      setProviderFilter(nextProvider);
      setModel(defaultModelForProvider(nextProvider, models, remainingProviders, providerPresets));
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

  const loadRetentionRuns = retention.actions.loadRuns;

  // Coordinator: the slice owns the retention state machine; the
  // cross-cutting `notice` banner is set here so the slice stays
  // independent of unrelated global UI state. A later refactor will
  // move this composition into a dedicated coordinator hook
  // alongside other "slice + global side-effect" pairs.
  async function runRetention() {
    setNotice(null);
    const result = await retention.actions.runRetention();
    setNotice(result.ok
      ? { kind: "success", message: "Retention run completed." }
      : { kind: "error", message: "Failed to run retention." });
  }

  async function createChatSession() {
    if (defaultChatTarget === "external_agent") {
      const workspace = agentWorkspace.trim();
      if (!workspace) {
        startNewChat();
        return;
      }
      setChatLoading(true);
      clearChatErrorState();
      try {
        const adapter = agentAdapters.find((item) => item.id === agentAdapterID);
        const created = await createAgentChatSessionRequest({
          title: adapter ? `${adapter.name} chat` : "External agent chat",
          runtime_kind: "external_agent",
          adapter_id: agentAdapterID,
          workspace,
        });
        setActiveAgentChatSessionID(created.data.id);
        applyAgentChatSession(created.data);
      } catch (error) {
        setChatErrorState(error, "failed to create external agent chat");
        setNoticeMessage("error", error instanceof Error ? error.message : "Failed to create external agent chat.");
      } finally {
        setChatLoading(false);
      }
      return;
    }

    const runtimeKind = defaultChatTarget === "model" ? "model" : "agent";
    const workspace = agentWorkspace.trim();
    if (runtimeKind === "agent" && !workspace) {
      startNewChat();
      return;
    }
    if (!model) {
      startNewChat();
      return;
    }
    setChatLoading(true);
    clearChatErrorState();
    try {
      const created = await createAgentChatSessionRequest({
        runtime_kind: runtimeKind,
        provider: providerFilter === "auto" ? "" : providerFilter,
        model,
        ...(runtimeKind === "agent" ? {
          workspace,
          rtk_enabled: hecateRTKEnabled,
        } : {}),
      });
      setActiveAgentChatSessionID(created.data.id);
      applyAgentChatSession(created.data);
    } catch (error) {
      setChatErrorState(error, "failed to create Hecate chat");
      setNoticeMessage("error", error instanceof Error ? error.message : "Failed to create Hecate chat.");
    } finally {
      setChatLoading(false);
    }
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
  // Returns null on failure so the caller can render an error state;
  // the slice's getApproval returns a discriminated Result that the
  // shim unwraps into the legacy `record | null` shape and routes
  // the error string to the global notice banner.
  async function getAgentChatApproval(
    sessionID: string,
    approvalID: string,
  ): Promise<AgentChatApprovalRecord | null> {
    const result = await approvals.actions.getApproval(sessionID, approvalID);
    if (!result.ok) {
      setNoticeMessage("error", result.error);
      return null;
    }
    return result.record;
  }

  async function resolveAgentChatApproval(
    sessionID: string,
    approvalID: string,
    decision: ResolveAgentChatApprovalPayload,
  ): Promise<boolean> {
    const result = await approvals.actions.resolveApproval(sessionID, approvalID, decision);
    if (!result.ok) setNoticeMessage("error", result.error);
    return result.ok;
  }

  async function cancelAgentChatApproval(sessionID: string, approvalID: string): Promise<boolean> {
    const result = await approvals.actions.cancelApproval(sessionID, approvalID);
    if (!result.ok) setNoticeMessage("error", result.error);
    return result.ok;
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

  const listAgentChatGrants = approvals.actions.loadGrants;

  async function deleteAgentChatGrant(grantID: string): Promise<boolean> {
    const result = await approvals.actions.deleteGrant(grantID);
    if (result.ok) {
      setNoticeMessage("success", "Grant revoked.");
    } else {
      setNoticeMessage("error", result.error);
    }
    return result.ok;
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

  async function setAgentChatConfigOption(sessionID: string, configID: string, value: string | boolean): Promise<boolean> {
    try {
      const payload = await setAgentChatConfigOptionRequest(sessionID, configID, value);
      applyAgentChatSession(payload.data);
      return true;
    } catch (error) {
      setNoticeMessage("error", error instanceof Error ? error.message : "Failed to update adapter control.");
      return false;
    }
  }

  async function setHecateRTKEnabled(enabled: boolean): Promise<boolean> {
    setHecateRTKEnabledState(enabled);
    if (!activeAgentChatSessionID || !activeAgentChatSession || agentChatSessionIsExternal(activeAgentChatSession)) {
      return true;
    }
    try {
      const payload = await setAgentChatSettingsRequest(activeAgentChatSessionID, { rtk_enabled: enabled });
      applyAgentChatSession(payload.data);
      return true;
    } catch (error) {
      setHecateRTKEnabledState(Boolean(activeAgentChatSession.rtk_enabled));
      setNoticeMessage("error", error instanceof Error ? error.message : "Failed to update chat settings.");
      return false;
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
  // readiness probe in Connections; the result drives
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

  async function setAgentAdapterCredential(adapterID: string, value: string, name?: string): Promise<boolean> {
    try {
      const payload = await setAgentAdapterCredentialRequest(adapterID, value, name);
      setAgentAdapters((current) => current.map((item) => item.id === adapterID
        ? { ...item, credential_configured: payload.data.configured, credential_preview: payload.data.preview }
        : item));
      if (adapterID === "claude_code" && payload.data.configured) {
        setAgentAdapterHealthByID((current) => {
          const next = new Map(current);
          next.set(adapterID, { adapter_id: adapterID, status: "ready", stage: "ready", duration_ms: 0 });
          return next;
        });
      }
      setNoticeMessage("success", adapterID === "claude_code" ? "Claude Code verified." : "Adapter credential saved.");
      return true;
    } catch (error) {
      const fallback = adapterID === "claude_code" ? "Failed to validate adapter credential." : "Failed to save adapter credential.";
      setNoticeMessage("error", error instanceof Error ? error.message : fallback);
      return false;
    }
  }

  async function deleteAgentAdapterCredential(adapterID: string, name: string): Promise<boolean> {
    try {
      await deleteAgentAdapterCredentialRequest(adapterID, name);
      setAgentAdapters((current) => current.map((item) => item.id === adapterID
        ? { ...item, credential_configured: false, credential_preview: undefined }
        : item));
      setAgentAdapterHealthByID((current) => {
        if (!current.has(adapterID)) return current;
        const next = new Map(current);
        next.delete(adapterID);
        return next;
      });
      setNoticeMessage("success", "Adapter credential removed.");
      return true;
    } catch (error) {
      setNoticeMessage("error", error instanceof Error ? error.message : "Failed to remove adapter credential.");
      return false;
    }
  }

  async function renameChatSession(id: string, title: string) {
    try {
      const nextTitle = title.trim();
      if (!nextTitle) {
        setNoticeMessage("error", "Chat title cannot be empty.");
        return;
      }
      const isAgentSession = activeAgentChatSessionID === id || agentChatSessions.some((session) => session.id === id);
      if (isAgentSession) {
        const payload = await updateAgentChatSessionRequest(id, nextTitle);
        setAgentChatSessions((current) =>
          current.map((s) => (s.id === id ? { ...s, title: payload.data.title, updated_at: payload.data.updated_at ?? s.updated_at } : s)),
        );
        if (activeAgentChatSessionID === id) {
          setActiveAgentChatSession((current) => (current ? { ...current, title: payload.data.title, updated_at: payload.data.updated_at ?? current.updated_at } : current));
        }
        return;
      }

      const payload = await updateChatSessionRequest(id, nextTitle);
      setChatSessions((current) =>
        current.map((s) => (s.id === id ? { ...s, title: payload.data.title, updated_at: payload.data.updated_at ?? s.updated_at } : s)),
      );
      if (activeChatSessionID === id) {
        setActiveChatSession((current) => (current ? { ...current, title: payload.data.title, updated_at: payload.data.updated_at ?? current.updated_at } : current));
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
      usageSummary,
      agentAdapterID,
      agentAdapters,
      agentChatCancelling,
      agentChatSessions,
      hecateRTKEnabled,
      hecateRTKAvailable,
      hecateRTKPath,
      newChatAgentID: defaultChatTarget === "external_agent" ? agentAdapterID : "hecate",
      agentWorkspace,
      agentWorkspaceBranch,
      usageEvents,
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
      retentionError: retention.state.error,
      retentionLastRun: retention.state.lastRun,
      retentionLoading: retention.state.loading,
      retentionRuns: retention.state.runs,
      retentionSubsystems: retention.state.subsystems,
      runtimeHeaders,
      chatSessionsHasMore,
      chatSessionsLoadingMore,
      visibleModels,
      pendingApprovalsBySessionID: approvals.state.pendingBySessionID,
      agentChatGrants: approvals.state.grants,
      agentChatGrantsLoading: approvals.state.grantsLoading,
      agentChatGrantsError: approvals.state.grantsError,
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
      loadRetentionRuns,
      setAgentAdapterID,
      setNewChatAgent,
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
      setRetentionSubsystems: retention.actions.setSubsystems,
      runRetention,
      selectChatSession,
      startNewChat,
      submitChat,
      loadMoreChatSessions,
      submitToolResults,
      updateToolResult,
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
      getAgentChatApproval,
      listAgentChatMessageFiles,
      getAgentChatMessageFileDiff,
      revertAgentChatMessageFiles,
      resolveTaskApproval,
      resolveAgentChatApproval,
      cancelAgentChatApproval,
      listAgentChatGrants,
      deleteAgentChatGrant,
      setAgentChatConfigOption,
      setHecateRTKEnabled,
      probeAgentAdapter,
      setAgentAdapterCredential,
      deleteAgentAdapterCredential,
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
