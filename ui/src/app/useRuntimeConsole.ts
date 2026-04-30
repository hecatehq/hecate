import { useEffect, useMemo, useState, type SyntheticEvent } from "react";

import { buildLocalProviderIssue } from "../lib/provider-issues";
import type { LocalProviderIssue } from "../lib/provider-issues";
import { filterModelsByKind, filterModelsByProvider, parseCSV, usdToMicros } from "../lib/runtime-utils";
import {
  ApiError,
  type ChatMessage,
  chatCompletionsStream,
  createChatSession as createChatSessionRequest,
  deleteChatSession as deleteChatSessionRequest,
  updateChatSession as updateChatSessionRequest,
  deleteAPIKey as deleteAPIKeyRequest,
  deletePolicyRule as deletePolicyRuleRequest,
  deleteTenant as deleteTenantRequest,
  getAccountSummary,
  getBootstrapToken,
  getBudget,
  getChatSession,
  getChatSessions,
  getAdminConfig,
  getHealth,
  getModels,
  getProviderPresets,
  getProviders,
  getRequestLedger,
  getRetentionRuns,
  getSession,
  rotateAPIKey as rotateAPIKeyRequest,
  setProviderAPIKey as setProviderAPIKeyRequest,
  upsertPricebookEntry as upsertPricebookEntryRequest,
  deletePricebookEntry as deletePricebookEntryRequest,
  previewPricebookImport as previewPricebookImportRequest,
  applyPricebookImport as applyPricebookImportRequest,
  runRetention as runRetentionRequest,
  resetBudget as resetBudgetRequest,
  setAPIKeyEnabled as setAPIKeyEnabledRequest,
  setBudgetLimit as setBudgetLimitRequest,
  setTenantEnabled as setTenantEnabledRequest,
  topUpBudget as topUpBudgetRequest,
  upsertAPIKey as upsertAPIKeyRequest,
  upsertPolicyRule as upsertPolicyRuleRequest,
  upsertTenant as upsertTenantRequest,
  createProvider as createProviderRequest,
  deleteProvider as deleteProviderRequest,
  setProviderBaseURL as setProviderBaseURLRequest,
  setProviderName as setProviderNameRequest,
  setProviderCustomName as setProviderCustomNameRequest,
} from "../lib/api";
import type { PolicyRuleUpsertPayload } from "../lib/api";
import type {
  BudgetStatusResponse,
  AccountSummaryResponse,
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

type SessionKind = "anonymous" | "tenant" | "admin" | "invalid";
type SessionState = {
  kind: SessionKind;
  label: string;
  capabilities: string[];
  isAdmin: boolean;
  isAuthenticated: boolean;
  role: string;
  name: string;
  tenant: string;
  source: string;
  keyID: string;
  allowedProviders: string[];
  allowedModels: string[];
  // multiTenant: when true, the operator console exposes Tenants and
  // Keys management surfaces. Mirrors the server's GATEWAY_MULTI_TENANT
  // flag via /v1/whoami's features object. Defaults to false for
  // clients talking to an older gateway that doesn't ship features yet.
  multiTenant: boolean;
  // authDisabled: when true, the gateway accepts unauthenticated
  // requests. The UI uses this to skip the TokenGate entirely.
  authDisabled: boolean;
};
type NoticeState = {
  kind: "success" | "error";
  message: string;
};

const invalidBearerTokenMessage = "missing or invalid bearer token";

export function useRuntimeConsole() {
  const [health, setHealth] = useState<HealthResponse | null>(null);
  const [models, setModels] = useState<ModelResponse["data"]>([]);
  const [providers, setProviders] = useState<ProviderStatusResponse["data"]>([]);
  const [providerPresets, setProviderPresets] = useState<ProviderPresetRecord[]>([]);
  const [budget, setBudget] = useState<BudgetStatusResponse["data"] | null>(null);
  const [accountSummary, setAccountSummary] = useState<AccountSummaryResponse["data"] | null>(null);
  const [requestLedger, setRequestLedger] = useState<RequestLedgerResponse["data"]>([]);
  const [adminConfig, setAdminConfig] = useState<ConfiguredStateResponse["data"] | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const [model, setModel] = useState("");
  const [tenant, setTenant] = useState("");
  const [message, setMessage] = useState("");
  const [systemPrompt, setSystemPrompt] = useState("");
  const [chatLoading, setChatLoading] = useState(false);
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
  const [modelFilter, setModelFilter] = useState<ModelFilter>("all");
  const [providerFilter, setProviderFilter] = useState<ProviderFilter>("auto");
  const [copiedCommand, setCopiedCommand] = useState("");

  const [budgetAmountUsd, setBudgetAmountUsd] = useState("1.00");
  const [budgetLimitUsd, setBudgetLimitUsd] = useState("5.00");
  const [budgetActionError, setBudgetActionError] = useState("");

  // Lazy-init from localStorage so the very first render already knows
  // whether we have a token. Otherwise the gate flashes the workspace
  // shell with stale data on every refresh before TokenGate can mount.
  const [authToken, setAuthToken] = useState<string>(() => {
    if (typeof window === "undefined") return "";
    return window.localStorage.getItem("hecate.authToken") ?? "";
  });
  // bootstrapAttempted gates the dashboard load + TokenGate render
  // until we've had a chance to ask the gateway for a loopback
  // bootstrap token. When a token is already in localStorage we treat
  // the bootstrap step as already done and skip the network call.
  const [bootstrapAttempted, setBootstrapAttempted] = useState<boolean>(() => {
    if (typeof window === "undefined") return true;
    return (window.localStorage.getItem("hecate.authToken") ?? "") !== "";
  });
  // staleTokenRetried prevents an infinite reprobe loop if the gateway
  // hands us a loopback token that's somehow ALSO rejected (shouldn't
  // happen — they come from the same source — but a defense in depth
  // beats spinning). Reset to false on every explicit setAuthToken
  // (operator paste), so a manual retry after a real-world fix
  // re-arms the auto-recovery for the next reset cycle.
  const [staleTokenRetried, setStaleTokenRetried] = useState(false);
  const [sessionInfo, setSessionInfo] = useState<SessionResponse["data"] | null>(null);
  const [adminConfigError, setAdminConfigError] = useState("");
  const [notice, setNotice] = useState<NoticeState | null>(null);

  const [tenantFormName, setTenantFormName] = useState("");
  const [tenantFormID, setTenantFormID] = useState("");
  // The allowed_* form fields are arrays of ids — they were
  // CSV-stringly-typed in an earlier iteration but are now backed by
  // ChipInput multi-selects, so the wire shape (string[]) and the
  // form shape match directly. parseCSV is no longer involved.
  const [tenantFormProviders, setTenantFormProviders] = useState<string[]>([]);
  const [tenantFormModels, setTenantFormModels] = useState<string[]>([]);
  // Tenant-level layer of the agent_loop system prompt. Optional;
  // empty falls back to the global / workspace / per-task layers.
  const [tenantFormSystemPrompt, setTenantFormSystemPrompt] = useState("");

  const [apiKeyFormName, setAPIKeyFormName] = useState("");
  const [apiKeyFormID, setAPIKeyFormID] = useState("");
  const [apiKeyFormSecret, setAPIKeyFormSecret] = useState("");
  const [apiKeyFormTenant, setAPIKeyFormTenant] = useState("");
  const [apiKeyFormRole, setAPIKeyFormRole] = useState("tenant");
  const [apiKeyFormProviders, setAPIKeyFormProviders] = useState<string[]>([]);
  const [apiKeyFormModels, setAPIKeyFormModels] = useState<string[]>([]);
  const [rotateAPIKeyID, setRotateAPIKeyID] = useState("");
  const [rotateAPIKeySecret, setRotateAPIKeySecret] = useState("");
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
    // authToken is hydrated synchronously above via the useState lazy init.
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
  }, []);

  useEffect(() => {
    window.localStorage.setItem("hecate.systemPrompt", systemPrompt);
  }, [systemPrompt]);

  // One-shot bootstrap probe: when no bearer is in localStorage, ask
  // the gateway for a loopback bootstrap token before falling through
  // to TokenGate. Single-user installs running on the same host as the
  // browser get a zero-config experience this way; everything else
  // (remote browsers, published images, cross-origin) is fenced
  // server-side and falls through to manual paste.
  useEffect(() => {
    if (bootstrapAttempted) return;
    let cancelled = false;
    void (async () => {
      const token = await getBootstrapToken();
      if (cancelled) return;
      if (token) {
        setAuthToken(token);
      }
      setBootstrapAttempted(true);
    })();
    return () => {
      cancelled = true;
    };
  }, [bootstrapAttempted]);

  // Auto-recover when the saved bearer was rejected. Common cause:
  // `make reset && make dev` regenerates the gateway's auto-managed
  // admin token, leaving the operator's localStorage token stale.
  // The bootstrap-token endpoint is server-fenced (loopback +
  // same-origin + gateway-managed token only), so re-probing it is
  // safe: in single-user dev it hands back the new token, in any
  // other config it returns 403 and the rejected gate still shows.
  // staleTokenRetried prevents looping if the freshly-probed token
  // is also somehow rejected.
  useEffect(() => {
    if (session.kind !== "invalid") return;
    if (staleTokenRetried) return;
    let cancelled = false;
    void (async () => {
      const token = await getBootstrapToken();
      if (cancelled) return;
      setStaleTokenRetried(true);
      if (token && token !== authToken) {
        setAuthToken(token);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [session.kind, staleTokenRetried, authToken]);

  useEffect(() => {
    // Wait until the bootstrap probe has resolved one way or the
    // other before deciding what to do — racing the probe risks
    // flashing TokenGate for a single frame on a loopback host that's
    // about to hand us a token.
    if (!bootstrapAttempted) {
      return;
    }
    // Even with no bearer we still load the dashboard once so whoami
    // can tell us whether the gateway runs in auth-disabled mode. The
    // load helper short-circuits per-endpoint based on the resolved
    // role, so an enabled-auth + empty-token combination still avoids
    // 401-spamming admin endpoints.
    void loadDashboard();
  }, [authToken, bootstrapAttempted]);

  useEffect(() => {
    window.localStorage.setItem("hecate.authToken", authToken);
    // A new bearer (operator paste OR loopback probe) re-arms the
    // stale-token auto-recovery so a future invalid transition gets a
    // fresh probe attempt. Without this, the operator only gets one
    // automatic recovery per page load.
    setStaleTokenRetried(false);
  }, [authToken]);

  useEffect(() => {
    if (model) {
      window.localStorage.setItem("hecate.model", model);
    }
  }, [model]);

  useEffect(() => {
    window.localStorage.setItem("hecate.providerFilter", providerFilter);
  }, [providerFilter]);

  useEffect(() => {
    if (activeChatSessionID) {
      window.localStorage.setItem("hecate.chatSessionID", activeChatSessionID);
      return;
    }
    window.localStorage.removeItem("hecate.chatSessionID");
  }, [activeChatSessionID]);

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
    if (session.kind !== "tenant" || !session.tenant) {
      return;
    }
    setTenant((current) => (current === session.tenant ? current : session.tenant));
  }, [session.kind, session.tenant]);

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

  useEffect(() => {
    if (providerFilter !== "auto" && session.allowedProviders.length > 0 && !session.allowedProviders.includes(providerFilter)) {
      setProviderFilter("auto");
      return;
    }

    if (session.allowedModels.length > 0 && model !== "" && !session.allowedModels.includes(model)) {
      const nextAllowedModel =
        models.find((entry) => session.allowedModels.includes(entry.id) && (providerFilter === "auto" || entry.metadata?.provider === providerFilter))?.id ??
        models.find((entry) => session.allowedModels.includes(entry.id))?.id ??
        "";
      setModel(nextAllowedModel);
    }
  }, [model, models, providerFilter, session.allowedModels, session.allowedProviders]);

  function clearPendingToolState() {
    setPendingToolCalls([]);
    setPendingThread(null);
  }

  function resetChatWorkspaceState() {
    setChatResult(null);
    setStreamingContent(null);
    setRuntimeHeaders(null);
    clearPendingToolState();
    setChatError("");
    setChatErrorCode("");
    setChatErrorStatus(null);
    setSystemPrompt("");
  }

  function activateChatSession(sessionRecord: ChatSessionRecord) {
    setActiveChatSessionID(sessionRecord.id);
    setActiveChatSession(sessionRecord);
  }

  function upsertChatSessionSummary(sessionRecord: ChatSessionRecord) {
    setChatSessions((current) => [renderChatSessionSummary(sessionRecord), ...current.filter((entry) => entry.id !== sessionRecord.id)]);
  }

  async function createChatSessionRecord(title: string): Promise<ChatSessionRecord> {
    const payload = await createChatSessionRequest({ title }, authToken);
    activateChatSession(payload.data);
    upsertChatSessionSummary(payload.data);
    return payload.data;
  }

  async function refreshChatSessionState(sessionID: string) {
    if (!sessionID) {
      return;
    }
    try {
      const [sessionsResult, sessionResult] = await Promise.all([
        getChatSessions(authToken, 20),
        getChatSession(sessionID, authToken),
      ]);
      setChatSessions(sessionsResult.data ?? []);
      setActiveChatSession(sessionResult.data);
    } catch {
      // Keep the primary request flow resilient.
    }
  }

  async function refreshAdminRuntimeState() {
    // Account summary remains admin-only (it's the cross-tenant rollup);
    // the request ledger is now tenant-readable via /v1/requests, so we
    // refresh it for any authenticated principal.
    if (session.isAdmin) {
      try {
        const accountSummaryResult = await getAccountSummary("", authToken);
        setAccountSummary(accountSummaryResult.data);
      } catch {
        // Keep chat responsive even if admin-only refresh paths fail.
      }
    }
    if (!session.isAuthenticated) return;
    try {
      const requestLedgerResult = await getRequestLedger(authToken, 20, session.isAdmin);
      setRequestLedger(requestLedgerResult.data ?? []);
    } catch {
      // Best-effort.
    }
  }

  // refreshProviders re-fetches /admin/providers (runtime health) and
  // /v1/models (model catalog) for the ProvidersView auto-poll so local
  // provider model lists converge within ~30 s of starting Ollama / LM
  // Studio. Skipped when no providers are configured — the providers
  // tab renders its empty state, there's nothing to converge.
  async function refreshProviders() {
    if (!session.isAdmin || !authToken) return;
    if ((adminConfig?.providers?.length ?? 0) === 0) return;
    try {
      const [pResult, mResult] = await Promise.allSettled([
        getProviders(authToken),
        getModels(authToken),
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
      user: tenant,
      messages,
    };
  }

  function resetTenantForm() {
    setTenantFormID("");
    setTenantFormName("");
    setTenantFormProviders([]);
    setTenantFormModels([]);
    setTenantFormSystemPrompt("");
  }

  function resetAPIKeyForm() {
    setAPIKeyFormID("");
    setAPIKeyFormName("");
    setAPIKeyFormSecret("");
    setAPIKeyFormTenant("");
    setAPIKeyFormProviders([]);
    setAPIKeyFormModels([]);
  }

  function resetRotateAPIKeyForm() {
    setRotateAPIKeyID("");
    setRotateAPIKeySecret("");
  }

  async function loadDashboard() {
    setLoading(true);
    setError("");
    setAdminConfigError("");

    try {
      const snapshot = await resolveDashboardSnapshot({
        authToken,
        activeChatSessionID,
        previous: {
          providers,
          budget,
          accountSummary,
          chatSessions,
          activeChatSession,
          requestLedger,
          adminConfig,
          retentionRuns,
          retentionLastRun,
        },
      });

      setHealth(snapshot.health);
      setSessionInfo(snapshot.sessionInfo);
      setModels(snapshot.models);
      setProviders(snapshot.providers);
      setProviderPresets(snapshot.providerPresets);
      setBudget(snapshot.budget);
      setAccountSummary(snapshot.accountSummary);
      setChatSessions(snapshot.chatSessions);
      setChatSessionsHasMore(snapshot.chatSessionsHasMore);
      setActiveChatSessionID(snapshot.activeChatSessionID);
      setActiveChatSession(snapshot.activeChatSession);
      setRequestLedger(snapshot.requestLedger);
      setAdminConfig(snapshot.adminConfig);
      setRetentionRuns(snapshot.retentionRuns);
      setRetentionLastRun(snapshot.retentionLastRun);
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

  async function submitChat(event: SyntheticEvent<HTMLFormElement>) {
    event.preventDefault();
    setChatLoading(true);
    setChatError("");
    setChatErrorCode("");
    setChatErrorStatus(null);
    setRuntimeHeaders(null);

    try {
      let sessionID = activeChatSessionID;
      if (!sessionID) {
        const createdSession = await createChatSessionRecord(deriveChatSessionTitle(message));
        sessionID = createdSession.id;
      }

      const messages = buildMessagesForSubmission(activeChatSession, message, systemPrompt);
      clearPendingToolState();

      // Show the user message immediately, before streaming starts.
      // The optimistic update appends a pending user message and a
      // placeholder assistant + call. Sequence numbers don't matter
      // here — the server overwrites the whole conversation on
      // refreshChatSessionState; sequence is only authoritative once
      // it round-trips.
      const optimisticMessage = message;
      const pendingCallID = `pending-call-${Date.now()}`;
      const pendingUserID = `pending-user-${Date.now()}`;
      const pendingAssistantID = `pending-assistant-${Date.now()}`;
      const pendingTimestamp = new Date().toISOString();
      setMessage("");
      setActiveChatSession((prev) =>
        prev
          ? {
              ...prev,
              messages: [
                ...(prev.messages ?? []),
                {
                  id: pendingUserID,
                  sequence: (prev.messages?.length ?? 0),
                  role: "user",
                  content: optimisticMessage,
                  created_at: pendingTimestamp,
                },
                {
                  id: pendingAssistantID,
                  sequence: (prev.messages?.length ?? 0) + 1,
                  produced_by_call_id: pendingCallID,
                  role: "assistant",
                  content: null,
                  created_at: pendingTimestamp,
                },
              ],
              provider_calls: [
                ...(prev.provider_calls ?? []),
                {
                  id: pendingCallID,
                  request_id: "",
                  provider: "",
                  model: "",
                  cost_micros_usd: 0,
                  cost_usd: "0",
                  prompt_tokens: 0,
                  completion_tokens: 0,
                  total_tokens: 0,
                  created_at: pendingTimestamp,
                },
              ],
            }
          : prev,
      );

      const chatExecution = await executeChatRequest(buildChatPayload(messages, sessionID), messages);
      if (chatExecution.kind === "tool_calls") {
        return;
      }
      const { headers } = chatExecution;

      // Patch the optimistic placeholders with the real assistant
      // content so the UI doesn't blink while waiting for the
      // session refresh round-trip.
      const assistantContent = chatExecution.chatResult.choices[0]?.message.content ?? "";
      setActiveChatSession((prev) => {
        if (!prev) return prev;
        const messages = (prev.messages ?? []).map((m) =>
          m.id === pendingAssistantID ? { ...m, content: assistantContent } : m,
        );
        const provider_calls = (prev.provider_calls ?? []).map((c) =>
          c.id === pendingCallID ? { ...c, model: headers.resolvedModel || model } : c,
        );
        return { ...prev, messages, provider_calls };
      });

      setChatResult(chatExecution.chatResult);

      try {
        const scopedBudget = await getBudget(
          `?scope=tenant_provider&tenant=${encodeURIComponent(tenant)}&provider=${encodeURIComponent(headers.provider)}`,
          authToken,
        );
        setBudget(scopedBudget.data);
      } catch {
        // Tenant-key users may not be authorized for admin budget views.
      }

      await refreshChatSessionState(sessionID);
      setStreamingContent(null);
      await refreshAdminRuntimeState();
    } catch (submitError) {
      const raw = submitError instanceof Error ? submitError.message : "unknown request error";
      const friendly = humanizeChatError(raw);
      setChatError(friendly);
      setChatErrorCode(submitError instanceof ApiError ? submitError.code : "");
      setChatErrorStatus(submitError instanceof ApiError ? submitError.status : null);
      // Inline chat error is the single source — no toast. The user is
      // already looking at the chat surface; mirroring the same string in
      // a corner toast just means they see the same message twice in
      // different fonts/positions.
    } finally {
      setChatLoading(false);
    }
  }

  function updateToolResult(index: number, result: string) {
    setPendingToolCalls((prev) => prev.map((tc, i) => (i === index ? { ...tc, result } : tc)));
  }

  async function submitToolResults() {
    if (!pendingThread || pendingToolCalls.length === 0) return;
    setChatLoading(true);
    setChatError("");
    setChatErrorCode("");
    setChatErrorStatus(null);

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
      await refreshAdminRuntimeState();
    } catch (err) {
      const raw = err instanceof Error ? err.message : "unknown error";
      setChatError(humanizeChatError(raw));
      setChatErrorCode(err instanceof ApiError ? err.code : "");
      setChatErrorStatus(err instanceof ApiError ? err.status : null);
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
    const response = await chatCompletionsStream(chatPayload, authToken, (delta) => {
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
          tenant: budget.tenant,
          key: budget.scope === "custom" ? budget.key : "",
        },
        authToken,
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
          tenant: budget.tenant,
          key: budget.scope === "custom" ? budget.key : "",
          amount_micros_usd: amountMicrosUSD,
        },
        authToken,
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
          tenant: budget.tenant,
          key: budget.scope === "custom" ? budget.key : "",
          balance_micros_usd: limitMicrosUSD,
        },
        authToken,
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

  function resetAdminFeedback() {
    setAdminConfigError("");
    setNotice(null);
  }

  async function runAdminMutation(options: {
    action: () => Promise<void>;
    successMessage: string;
    errorMessage: string;
    failureDetail: string;
  }) {
    resetAdminFeedback();
    try {
      await options.action();
      await loadDashboard();
      setNoticeMessage("success", options.successMessage);
    } catch (error) {
      setAdminConfigError(describeError(error, options.failureDetail));
      setNoticeMessage("error", options.errorMessage);
    }
  }

  async function upsertTenant() {
    await runAdminMutation({
      successMessage: "Tenant saved.",
      errorMessage: "Failed to save tenant.",
      failureDetail: "failed to save tenant",
      action: async () => {
        await upsertTenantRequest(
          {
            id: tenantFormID,
            name: tenantFormName,
            allowed_providers: tenantFormProviders,
            allowed_models: tenantFormModels,
            enabled: true,
            system_prompt: tenantFormSystemPrompt,
          },
          authToken,
        );
        resetTenantForm();
      },
    });
  }

  async function upsertAPIKey() {
    await upsertAPIKeyRequest(
      {
        id: apiKeyFormID,
        name: apiKeyFormName,
        key: apiKeyFormSecret,
        tenant: apiKeyFormTenant,
        role: apiKeyFormRole,
        allowed_providers: apiKeyFormProviders,
        allowed_models: apiKeyFormModels,
        enabled: true,
      },
      authToken,
    );
    resetAPIKeyForm();
    void loadDashboard();
  }

  // setProviderAPIKey is the single operation for managing a provider's API key.
  // An empty `key` clears the existing credential; non-empty sets/replaces it.
  async function setProviderAPIKey(id: string, key: string) {
    await runAdminMutation({
      successMessage: key === "" ? "API key cleared." : "API key saved.",
      errorMessage: key === "" ? "Failed to clear API key." : "Failed to save API key.",
      failureDetail: key === "" ? "failed to clear provider api key" : "failed to save provider api key",
      action: async () => {
        await setProviderAPIKeyRequest(id, key, authToken);
      },
    });
  }

  async function createProvider(params: { name: string; preset_id?: string; custom_name?: string; base_url?: string; api_key?: string; kind: string; protocol: string }): Promise<void> {
    await createProviderRequest(params, authToken);
    await loadDashboard();
  }

  async function deleteProvider(id: string): Promise<void> {
    await deleteProviderRequest(id, authToken);
    await loadDashboard();
  }

  async function setProviderBaseURL(id: string, baseURL: string): Promise<void> {
    await setProviderBaseURLRequest(id, baseURL, authToken);
    // loadDashboard refreshes adminConfig (the source of truth for base_url
    // shown in the table), then refreshProviders re-runs model discovery
    // against the new endpoint so the model list updates immediately.
    await loadDashboard();
    await refreshProviders();
  }

  async function setProviderName(id: string, name: string): Promise<void> {
    await setProviderNameRequest(id, name, authToken);
    // The label change only affects adminConfig (table column) — no need
    // to rerun model discovery, so skip refreshProviders.
    await loadDashboard();
  }

  async function setProviderCustomName(id: string, customName: string): Promise<void> {
    await setProviderCustomNameRequest(id, customName, authToken);
    await loadDashboard();
  }

  async function setTenantEnabled(id: string, enabled: boolean) {
    await runAdminMutation({
      successMessage: `Tenant ${enabled ? "enabled" : "disabled"}.`,
      errorMessage: "Failed to update tenant state.",
      failureDetail: "failed to update tenant state",
      action: async () => {
        await setTenantEnabledRequest({ id, enabled }, authToken);
      },
    });
  }

  async function deleteTenant(id: string) {
    resetAdminFeedback();
    if (!window.confirm(`Delete tenant "${id}"? This cannot be undone.`)) {
      return;
    }
    await runAdminMutation({
      successMessage: "Tenant deleted.",
      errorMessage: "Failed to delete tenant.",
      failureDetail: "failed to delete tenant",
      action: async () => {
        await deleteTenantRequest({ id }, authToken);
      },
    });
  }

  // Policy rule mutations follow the same runAdminMutation contract
  // as the tenant / API key flows: success populates the toast notice
  // + clears adminConfigError; failure populates BOTH inline banner
  // and toast so an operator can't miss the error regardless of
  // viewport focus.
  async function upsertPolicyRule(payload: PolicyRuleUpsertPayload) {
    await runAdminMutation({
      successMessage: "Policy rule saved.",
      errorMessage: "Failed to save policy rule.",
      failureDetail: "failed to save policy rule",
      action: async () => {
        await upsertPolicyRuleRequest(payload, authToken);
      },
    });
  }

  async function deletePolicyRule(id: string) {
    await runAdminMutation({
      successMessage: "Policy rule deleted.",
      errorMessage: "Failed to delete policy rule.",
      failureDetail: "failed to delete policy rule",
      action: async () => {
        await deletePolicyRuleRequest(id, authToken);
      },
    });
  }

  async function setAPIKeyEnabled(id: string, enabled: boolean) {
    await runAdminMutation({
      successMessage: `API key ${enabled ? "enabled" : "disabled"}.`,
      errorMessage: "Failed to update API key state.",
      failureDetail: "failed to update api key state",
      action: async () => {
        await setAPIKeyEnabledRequest({ id, enabled }, authToken);
      },
    });
  }

  async function rotateAPIKey() {
    await runAdminMutation({
      successMessage: "API key rotated.",
      errorMessage: "Failed to rotate API key.",
      failureDetail: "failed to rotate api key",
      action: async () => {
        await rotateAPIKeyRequest({ id: rotateAPIKeyID, key: rotateAPIKeySecret }, authToken);
        resetRotateAPIKeyForm();
      },
    });
  }

  async function deleteAPIKey(id: string) {
    resetAdminFeedback();
    if (!window.confirm(`Delete API key "${id}"? This cannot be undone.`)) {
      return;
    }
    await runAdminMutation({
      successMessage: "API key deleted.",
      errorMessage: "Failed to delete API key.",
      failureDetail: "failed to delete api key",
      action: async () => {
        await deleteAPIKeyRequest({ id }, authToken);
      },
    });
  }

  async function upsertPricebookEntry(entry: PricebookEntryUpsertPayload) {
    await runAdminMutation({
      successMessage: "Pricebook entry saved.",
      errorMessage: "Failed to save pricebook entry.",
      failureDetail: "failed to save pricebook entry",
      action: async () => {
        await upsertPricebookEntryRequest(entry, authToken);
      },
    });
  }

  async function deletePricebookEntry(provider: string, model: string) {
    // Confirmation is the caller's concern now (PricebookTab routes
    // this through a styled ConfirmModal). The action itself just
    // performs the deletion.
    resetAdminFeedback();
    await runAdminMutation({
      successMessage: "Price cleared.",
      errorMessage: "Failed to clear price.",
      failureDetail: "failed to clear pricebook entry",
      action: async () => {
        await deletePricebookEntryRequest(provider, model, authToken);
      },
    });
  }

  // previewPricebookImport intentionally does NOT call runAdminMutation —
  // it doesn't mutate anything. It just fetches the diff and lets the
  // caller (the import modal) render it.
  async function previewPricebookImport(): Promise<PricebookImportDiff> {
    const response = await previewPricebookImportRequest(authToken);
    return response.data;
  }

  async function applyPricebookImport(keys: string[]): Promise<PricebookImportDiff> {
    const response = await applyPricebookImportRequest(keys, authToken);
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
        authToken,
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
    setActiveChatSessionID(id);
    if (!id) {
      setActiveChatSession(null);
      return;
    }
    try {
      const payload = await getChatSession(id, authToken);
      setActiveChatSession(payload.data);
    } catch (error) {
      const msg = error instanceof Error ? error.message : "failed to load chat session";
      setChatError(msg);
      setNoticeMessage("error", msg);
    }
  }

  function startNewChat() {
    setActiveChatSessionID("");
    setActiveChatSession(null);
    resetChatWorkspaceState();
  }

  async function deleteChatSession(id: string) {
    try {
      await deleteChatSessionRequest(id, authToken);
      setChatSessions((current) => current.filter((s) => s.id !== id));
      if (activeChatSessionID === id) {
        startNewChat();
      }
      setNoticeMessage("success", "Session deleted.");
    } catch (error) {
      setNoticeMessage("error", error instanceof Error ? error.message : "Failed to delete session.");
    }
  }

  async function renameChatSession(id: string, title: string) {
    try {
      const payload = await updateChatSessionRequest(id, title, authToken);
      setChatSessions((current) =>
        current.map((s) => (s.id === id ? { ...s, title: payload.data.title } : s)),
      );
      if (activeChatSessionID === id) {
        setActiveChatSession((current) => (current ? { ...current, title: payload.data.title } : current));
      }
    } catch (error) {
      setNoticeMessage("error", error instanceof Error ? error.message : "Failed to rename session.");
    }
  }

  async function loadMoreChatSessions() {
    if (chatSessionsLoadingMore || !chatSessionsHasMore) return;
    setChatSessionsLoadingMore(true);
    try {
      const result = await getChatSessions(authToken, 20, chatSessions.length);
      setChatSessions((current) => [...current, ...(result.data ?? [])]);
      setChatSessionsHasMore(result.has_more ?? false);
    } catch {
      // Keep sidebar responsive; silently skip failed page loads.
    } finally {
      setChatSessionsLoadingMore(false);
    }
  }

  return {
    state: {
      apiKeyFormID,
      apiKeyFormModels,
      apiKeyFormName,
      apiKeyFormProviders,
      apiKeyFormRole,
      apiKeyFormSecret,
      apiKeyFormTenant,
      authToken,
      bootstrapAttempted,
      budget,
      accountSummary,
      requestLedger,
      budgetActionError,
      budgetAmountUsd,
      budgetLimitUsd,
      chatError,
      chatErrorCode,
      chatErrorStatus,
      chatLoading,
      streamingContent,
      chatResult,
      pendingToolCalls,
      chatSessions,
      cloudModels,
      cloudProviders,
      adminConfig,
      adminConfigError,
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
      rotateAPIKeyID,
      rotateAPIKeySecret,
      runtimeHeaders,
      chatSessionsHasMore,
      chatSessionsLoadingMore,
      tenant,
      tenantFormID,
      tenantFormModels,
      tenantFormName,
      tenantFormProviders,
      tenantFormSystemPrompt,
      visibleModels,
    },
    actions: {
      copyCommand,
      deleteAPIKey,
      deletePolicyRule,
      deleteTenant,
      createChatSession,
      deleteChatSession,
      renameChatSession,
      loadDashboard,
      resetBudget,
      rotateAPIKey,
      setAPIKeyEnabled,
      setAPIKeyFormID,
      setAPIKeyFormModels,
      setAPIKeyFormName,
      setAPIKeyFormProviders,
      setAPIKeyFormRole,
      setAPIKeyFormSecret,
      setAPIKeyFormTenant,
      setAuthToken,
      setBudgetAmountUsd,
      setBudgetLimitUsd,
      setMessage,
      setSystemPrompt,
      setModel,
      setModelFilter,
      setProviderFilter: selectProviderRoute,
      refreshProviders,
      setRetentionSubsystems,
      setRotateAPIKeyID,
      setRotateAPIKeySecret,
      setTenantEnabled,
      setTenant,
      setTenantFormID,
      setTenantFormModels,
      setTenantFormName,
      setTenantFormProviders,
      setTenantFormSystemPrompt,
      setBudgetLimit,
      runRetention,
      selectChatSession,
      startNewChat,
      submitChat,
      loadMoreChatSessions,
      submitToolResults,
      updateToolResult,
      topUpBudget,
      upsertAPIKey,
      upsertPolicyRule,
      setProviderAPIKey,
      createProvider,
      deleteProvider,
      setProviderBaseURL,
      setProviderName,
      setProviderCustomName,
      upsertPricebookEntry,
      deletePricebookEntry,
      previewPricebookImport,
      applyPricebookImport,
      upsertTenant,
      clearAuthToken: () => setAuthToken(""),
      dismissNotice: () => setNotice(null),
    },
  };
}

// humanizeChatError translates raw gateway/provider errors into something
// an operator can act on. The backend's "api key is required for cloud
// provider X when stub mode is disabled" carries internal vocabulary
// (stub mode) that's noise to the user — they just need to know they
// should add a key. Falls back to the raw string when no pattern matches.
export function humanizeChatError(raw: string): string {
  const apiKeyPattern = /api key is required for cloud provider (\S+)/i;
  const m = raw.match(apiKeyPattern);
  if (m) {
    return `${m[1]} has no API key. Open the Providers tab and add one.`;
  }
  return raw;
}

function deriveChatSessionTitle(message: string): string {
  const normalized = message.trim().replace(/\s+/g, " ");
  if (!normalized) {
    return "New chat";
  }
  if (normalized.length <= 48) {
    return normalized;
  }
  return `${normalized.slice(0, 45)}...`;
}

function buildMessagesForSubmission(activeSession: ChatSessionRecord | null, message: string, systemPrompt = ""): ChatMessage[] {
  // Replay is now a near-trivial transform: the persisted message
  // stream is already in submission order. We carry content_blocks
  // and tool_error through verbatim so Anthropic-aware history
  // (thinking blocks, failed tool results) survives cross-provider
  // resubmission.
  const history: ChatMessage[] = (activeSession?.messages ?? [])
    .filter((m) => m.id && !m.id.startsWith("pending-"))
    .map((m) => persistedMessageToChatMessage(m));
  const prefix: ChatMessage[] = systemPrompt.trim() ? [{ role: "system", content: systemPrompt.trim() }] : [];
  return [...prefix, ...history, { role: "user", content: message }];
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
  // user / system / unknown
  return {
    role: m.role === "system" ? "system" : "user",
    content: m.content ?? "",
    ...ext,
  } as ChatMessage;
}

function buildAssistantToolCallMessage(
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

function buildSyntheticChatResult(headers: RuntimeHeaders, selectedModel: string, content: string): ChatResponse {
  return {
    id: headers.requestId || "stream",
    model: headers.resolvedModel || selectedModel,
    choices: [{ index: 0, message: { role: "assistant", content }, finish_reason: "stop" }],
    usage: { prompt_tokens: 0, completion_tokens: 0, total_tokens: 0 },
  };
}

function defaultModelForProvider(provider: ProviderFilter, models: ModelResponse["data"], providers: ProviderStatusResponse["data"], presets: ProviderPresetRecord[]): string {
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

function isModelValidForProvider(model: string, provider: ProviderFilter, models: ModelResponse["data"], providers: ProviderStatusResponse["data"], presets: ProviderPresetRecord[]): boolean {
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

function renderChatSessionSummary(session: ChatSessionRecord): ChatSessionsResponse["data"][number] {
  const messages = session.messages ?? [];
  const calls = session.provider_calls ?? [];
  const lastCall = calls[calls.length - 1];
  return {
    id: session.id,
    title: session.title,
    tenant: session.tenant,
    user: session.user,
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

export type RuntimeConsoleViewModel = ReturnType<typeof useRuntimeConsole>;

type DashboardResults = {
  health: PromiseSettledResult<HealthResponse>;
  session: PromiseSettledResult<SessionResponse>;
  models: PromiseSettledResult<ModelResponse>;
  providers: PromiseSettledResult<ProviderStatusResponse>;
  providerPresets: PromiseSettledResult<{ object: string; data: ProviderPresetRecord[] }>;
  budget: PromiseSettledResult<BudgetStatusResponse>;
  accountSummary: PromiseSettledResult<AccountSummaryResponse>;
  chatSessions: PromiseSettledResult<ChatSessionsResponse>;
  requestLedger: PromiseSettledResult<RequestLedgerResponse>;
  adminConfig: PromiseSettledResult<ConfiguredStateResponse>;
  retentionRuns: PromiseSettledResult<{ object: string; data: RetentionRunData[] }>;
};

type DashboardPreviousState = {
  providers: ProviderStatusResponse["data"];
  budget: BudgetStatusResponse["data"] | null;
  accountSummary: AccountSummaryResponse["data"] | null;
  chatSessions: ChatSessionsResponse["data"];
  activeChatSession: ChatSessionRecord | null;
  requestLedger: RequestLedgerResponse["data"];
  adminConfig: ConfiguredStateResponse["data"] | null;
  retentionRuns: RetentionRunData[];
  retentionLastRun: RetentionRunData | null;
};

type DashboardSnapshot = {
  health: HealthResponse;
  sessionInfo: SessionResponse["data"] | null;
  models: ModelResponse["data"];
  providers: ProviderStatusResponse["data"];
  providerPresets: ProviderPresetRecord[];
  budget: BudgetStatusResponse["data"] | null;
  accountSummary: AccountSummaryResponse["data"] | null;
  chatSessions: ChatSessionsResponse["data"];
  chatSessionsHasMore: boolean;
  activeChatSessionID: string;
  activeChatSession: ChatSessionRecord | null;
  requestLedger: RequestLedgerResponse["data"];
  adminConfig: ConfiguredStateResponse["data"] | null;
  retentionRuns: RetentionRunData[];
  retentionLastRun: RetentionRunData | null;
};

async function resolveDashboardSnapshot(args: {
  authToken: string;
  activeChatSessionID: string;
  previous: DashboardPreviousState;
}): Promise<DashboardSnapshot> {
  const results = await loadDashboardResults(args.authToken);
  const health = requireFulfilledDashboardResult(results.health);
  const sessionInfo = results.session.status === "fulfilled" ? results.session.value.data : null;
  const models = resolveModelsResult(results.models);
  const providers = resolveAuthorizedDashboardResult(results.providers, {
    unauthorized: [],
    other: args.previous.providers,
  });
  const providerPresets = results.providerPresets.status === "fulfilled" ? results.providerPresets.value.data : [];
  const budget = resolveAuthorizedDashboardResult(results.budget, {
    unauthorized: null,
    other: args.previous.budget,
  });
  const accountSummary = resolveAuthorizedDashboardResult(results.accountSummary, {
    unauthorized: null,
    other: args.previous.accountSummary,
  });
  const requestLedger = resolveAuthorizedDashboardResult(results.requestLedger, {
    unauthorized: [],
    other: args.previous.requestLedger,
  });
  const adminConfig = resolveAuthorizedDashboardResult(results.adminConfig, {
    unauthorized: null,
    other: args.previous.adminConfig,
  });
  const retentionRuns = resolveAuthorizedDashboardResult(results.retentionRuns, {
    unauthorized: [],
    other: args.previous.retentionRuns,
  });
  const retentionLastRun = retentionRuns[0] ?? null;
  const chatState = await resolveChatDashboardState({
    authToken: args.authToken,
    activeChatSessionID: args.activeChatSessionID,
    previousSessions: args.previous.chatSessions,
    previousActiveSession: args.previous.activeChatSession,
    result: results.chatSessions,
  });

  return {
    health,
    sessionInfo,
    models,
    providers,
    providerPresets,
    budget,
    accountSummary,
    chatSessions: chatState.sessions,
    chatSessionsHasMore: chatState.hasMore,
    activeChatSessionID: chatState.activeChatSessionID,
    activeChatSession: chatState.activeChatSession,
    requestLedger,
    adminConfig,
    retentionRuns,
    retentionLastRun,
  };
}

async function loadDashboardResults(authToken: string): Promise<DashboardResults> {
  // Two-phase load to avoid the "401 storm" — every admin endpoint
  // would fail for a tenant or anonymous bearer, and the browser
  // network panel logs each as a console error. Phase 1 establishes
  // identity (open /healthz + bearer-only /v1/whoami); Phase 2 only
  // fires the endpoints the resolved role can actually reach.
  const [health, session] = await Promise.allSettled([
    getHealth(),
    getSession(authToken),
  ]);

  // Identity gate: an invalid bearer means /v1/* will also 401, so we
  // skip everything else and let TokenGate take over via the
  // `invalid_token` branch in deriveSessionState.
  const sessionData = session.status === "fulfilled" ? session.value.data : null;
  const invalidToken = sessionData?.invalid_token === true;
  const role = sessionData?.role ?? "anonymous";
  const isAdmin = role === "admin" || sessionData?.source === "auth_disabled";
  const isAuthenticated = sessionData?.authenticated === true;

  // Default each result to a fresh "rejected without firing" so the
  // existing resolveAuthorizedDashboardResult path treats it as a
  // 401-equivalent fallback. We only overwrite below for endpoints
  // the role is allowed to call.
  // A "skipped" fetch presents the same shape as a 401 from the
  // existing per-resolver helpers — so they fall back to the
  // unauthorized default (empty list / null) rather than throwing
  // "failed to load runtime console data". Reusing
  // invalidBearerTokenMessage keeps the resolvers single-branched.
  const skipped = <T,>(): PromiseSettledResult<T> => ({ status: "rejected", reason: new Error(invalidBearerTokenMessage) });

  let models: PromiseSettledResult<ModelResponse> = skipped();
  let providers: PromiseSettledResult<ProviderStatusResponse> = skipped();
  let providerPresets: PromiseSettledResult<{ object: string; data: ProviderPresetRecord[] }> = skipped();
  let budget: PromiseSettledResult<BudgetStatusResponse> = skipped();
  let accountSummary: PromiseSettledResult<AccountSummaryResponse> = skipped();
  let chatSessions: PromiseSettledResult<ChatSessionsResponse> = skipped();
  let requestLedger: PromiseSettledResult<RequestLedgerResponse> = skipped();
  let adminConfig: PromiseSettledResult<ConfiguredStateResponse> = skipped();
  let retentionRuns: PromiseSettledResult<{ object: string; data: RetentionRunData[] }> = skipped();

  // auth_disabled mode reports authenticated:false but still wants
  // the full dashboard load — treat it as authenticated for gating.
  const authDisabled = sessionData?.source === "auth_disabled";
  if (!invalidToken && (isAuthenticated || authDisabled)) {
    // Common-to-all-roles: preset catalog + chat sessions + provider presets
    // — these are static-ish reference data that doesn't probe upstream
    // providers, so they're cheap and always fetched.
    const baseFetches: Array<Promise<unknown>> = [
      getProviderPresets(authToken).then(r => { providerPresets = { status: "fulfilled", value: r }; }, e => { providerPresets = { status: "rejected", reason: e }; }),
      getChatSessions(authToken, 20).then(r => { chatSessions = { status: "fulfilled", value: r }; }, e => { chatSessions = { status: "rejected", reason: e }; }),
    ];

    if (isAdmin) {
      // Admin path: load /admin/control-plane (CP store, source of truth
      // for "what providers are configured") in parallel with the other
      // admin-only endpoints AND the model catalog (admin tabs like the
      // pricebook need the catalog regardless of configured-provider
      // count). /admin/providers (runtime health/status) is the only
      // discovery call we gate — when no providers are configured, the
      // providers tab renders its empty state and there's nothing for
      // the runtime status to feed.
      baseFetches.push(
        getModels(authToken).then(r => { models = { status: "fulfilled", value: r }; }, e => { models = { status: "rejected", reason: e }; }),
        getBudget("", authToken).then(r => { budget = { status: "fulfilled", value: r }; }, e => { budget = { status: "rejected", reason: e }; }),
        getAccountSummary("", authToken).then(r => { accountSummary = { status: "fulfilled", value: r }; }, e => { accountSummary = { status: "rejected", reason: e }; }),
        getRequestLedger(authToken, 20, true).then(r => { requestLedger = { status: "fulfilled", value: r }; }, e => { requestLedger = { status: "rejected", reason: e }; }),
        getAdminConfig(authToken).then(r => { adminConfig = { status: "fulfilled", value: r }; }, e => { adminConfig = { status: "rejected", reason: e }; }),
        getRetentionRuns(authToken, 10).then(r => { retentionRuns = { status: "fulfilled", value: r }; }, e => { retentionRuns = { status: "rejected", reason: e }; }),
      );
      await Promise.all(baseFetches);

      const configured = adminConfig.status === "fulfilled" ? (adminConfig.value.data?.providers ?? []) : [];
      if (configured.length > 0) {
        await new Promise<void>(resolve => {
          getProviders(authToken).then(
            r => { providers = { status: "fulfilled", value: r }; resolve(); },
            e => { providers = { status: "rejected", reason: e }; resolve(); },
          );
        });
      }
    } else {
      // Tenant path: no /admin/control-plane access, so the runtime
      // discovery endpoints are the only source of truth for what
      // provider/model the operator can pick. Always fetch.
      baseFetches.push(
        getModels(authToken).then(r => { models = { status: "fulfilled", value: r }; }, e => { models = { status: "rejected", reason: e }; }),
        getProviders(authToken).then(r => { providers = { status: "fulfilled", value: r }; }, e => { providers = { status: "rejected", reason: e }; }),
        // Tenant-readable ledger via /v1/requests. Currently un-scoped
        // (no key_id column on BudgetHistoryEntry) — tenants see what the
        // unscoped endpoint returns, same as the admin /admin/requests.
        getRequestLedger(authToken, 20, false).then(r => { requestLedger = { status: "fulfilled", value: r }; }, e => { requestLedger = { status: "rejected", reason: e }; }),
      );
      await Promise.all(baseFetches);
    }
  }

  return {
    health,
    session,
    models,
    providers,
    providerPresets,
    budget,
    accountSummary,
    chatSessions,
    requestLedger,
    adminConfig,
    retentionRuns,
  };
}

function requireFulfilledDashboardResult<T>(result: PromiseSettledResult<T>): T {
  if (result.status === "fulfilled") {
    return result.value;
  }
  throw new Error("failed to load runtime console data");
}

function resolveModelsResult(result: PromiseSettledResult<ModelResponse>): ModelResponse["data"] {
  if (result.status === "fulfilled") {
    return result.value.data;
  }
  if (isInvalidBearerTokenError(result.reason)) {
    return [];
  }
  throw new Error("failed to load runtime console data");
}

function resolveAuthorizedDashboardResult<T>(
  result: PromiseSettledResult<{ data: T }>,
  fallbacks: { unauthorized: T; other: T },
): T {
  if (result.status === "fulfilled") {
    return result.value.data;
  }
  if (isInvalidBearerTokenError(result.reason)) {
    return fallbacks.unauthorized;
  }
  return fallbacks.other;
}

async function resolveChatDashboardState(args: {
  authToken: string;
  activeChatSessionID: string;
  previousSessions: ChatSessionsResponse["data"];
  previousActiveSession: ChatSessionRecord | null;
  result: PromiseSettledResult<ChatSessionsResponse>;
}): Promise<{
  sessions: ChatSessionsResponse["data"];
  hasMore: boolean;
  activeChatSessionID: string;
  activeChatSession: ChatSessionRecord | null;
}> {
  if (args.result.status !== "fulfilled") {
    if (isInvalidBearerTokenError(args.result.reason)) {
      return {
        sessions: [],
        hasMore: false,
        activeChatSessionID: "",
        activeChatSession: null,
      };
    }
    return {
      sessions: args.previousSessions,
      hasMore: false,
      activeChatSessionID: args.activeChatSessionID,
      activeChatSession: args.previousActiveSession,
    };
  }

  const sessions = args.result.value.data ?? [];
  const hasMore = args.result.value.has_more ?? false;
  const activeChatSessionID = sessions.some((entry) => entry.id === args.activeChatSessionID)
    ? args.activeChatSessionID
    : sessions[0]?.id ?? "";

  if (!activeChatSessionID) {
    return {
      sessions,
      hasMore,
      activeChatSessionID,
      activeChatSession: null,
    };
  }

  try {
    const sessionResult = await getChatSession(activeChatSessionID, args.authToken);
    return {
      sessions,
      hasMore,
      activeChatSessionID,
      activeChatSession: sessionResult.data,
    };
  } catch {
    return {
      sessions,
      hasMore,
      activeChatSessionID,
      activeChatSession: null,
    };
  }
}

function isInvalidBearerTokenError(error: unknown): boolean {
  return error instanceof Error && error.message === invalidBearerTokenMessage;
}

function deriveSessionState(sessionInfo: SessionResponse["data"] | null): SessionState {
  const role = sessionInfo?.role ?? "anonymous";
  const authDisabled = sessionInfo?.source === "auth_disabled";
  const kind: SessionKind = sessionInfo?.invalid_token
    ? "invalid"
    : role === "admin" || authDisabled
      ? "admin"
      : sessionInfo?.authenticated
        ? "tenant"
        : "anonymous";

  const label =
    kind === "admin"
      ? "Admin"
      : kind === "tenant"
        ? `Tenant${sessionInfo?.tenant ? `: ${sessionInfo.tenant}` : ""}`
        : kind === "invalid"
          ? "Invalid token"
          : "Anonymous";

  const capabilities =
    kind === "admin"
      ? ["Chats access", "Model catalog", "Provider status", "Budget admin", "Control-plane admin"]
      : kind === "tenant"
        ? ["Chats access", "Model catalog"]
        : kind === "anonymous"
          ? ["Health view", "Authentication setup"]
          : ["No confirmed access"];

  return {
    kind,
    label,
    capabilities,
    isAdmin: kind === "admin",
    isAuthenticated: kind === "admin" || kind === "tenant",
    role,
    name: sessionInfo?.name ?? "",
    tenant: sessionInfo?.tenant ?? "",
    source: sessionInfo?.source ?? "",
    keyID: sessionInfo?.key_id ?? "",
    allowedProviders: sessionInfo?.allowed_providers ?? [],
    allowedModels: sessionInfo?.allowed_models ?? [],
    multiTenant: sessionInfo?.features?.multi_tenant === true,
    // Two paths to "auth disabled": the explicit features.auth_disabled
    // flag from a fresh gateway, and the legacy source==="auth_disabled"
    // signal from older builds. Either one means the TokenGate should
    // step out of the way.
    authDisabled: sessionInfo?.features?.auth_disabled === true || sessionInfo?.source === "auth_disabled",
  };
}
