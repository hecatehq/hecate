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
  deletePolicyRule as deletePolicyRuleRequest,
  getAccountSummary,
  getBudget,
  getChatSession,
  getChatSessions,
  getControlPlaneConfig,
  getHealth,
  getModels,
  getProviderPresets,
  getProviders,
  getRequestLedger,
  getRetentionRuns,
  getSession,
  setProviderAPIKey as setProviderAPIKeyRequest,
  upsertPricebookEntry as upsertPricebookEntryRequest,
  deletePricebookEntry as deletePricebookEntryRequest,
  previewPricebookImport as previewPricebookImportRequest,
  applyPricebookImport as applyPricebookImportRequest,
  runRetention as runRetentionRequest,
  resetBudget as resetBudgetRequest,
  setBudgetLimit as setBudgetLimitRequest,
  topUpBudget as topUpBudgetRequest,
  upsertPolicyRule as upsertPolicyRuleRequest,
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

// Single-user mode: the session shape is a fixed label. Kept around so
// the status bar has something to show without bespoke wiring.
type SessionState = {
  label: string;
};
type NoticeState = {
  kind: "success" | "error";
  message: string;
};

export function useRuntimeConsole() {
  const [health, setHealth] = useState<HealthResponse | null>(null);
  const [models, setModels] = useState<ModelResponse["data"]>([]);
  const [providers, setProviders] = useState<ProviderStatusResponse["data"]>([]);
  const [providerPresets, setProviderPresets] = useState<ProviderPresetRecord[]>([]);
  const [budget, setBudget] = useState<BudgetStatusResponse["data"] | null>(null);
  const [accountSummary, setAccountSummary] = useState<AccountSummaryResponse["data"] | null>(null);
  const [requestLedger, setRequestLedger] = useState<RequestLedgerResponse["data"]>([]);
  const [controlPlaneConfig, setControlPlaneConfig] = useState<ConfiguredStateResponse["data"] | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const [model, setModel] = useState("");
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

  const [sessionInfo, setSessionInfo] = useState<SessionResponse["data"] | null>(null);
  const [controlPlaneError, setControlPlaneError] = useState("");
  const [notice, setNotice] = useState<NoticeState | null>(null);

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
  }, []);

  useEffect(() => {
    window.localStorage.setItem("hecate.systemPrompt", systemPrompt);
  }, [systemPrompt]);

  useEffect(() => {
    void loadDashboard();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

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
    const payload = await createChatSessionRequest({ title });
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

  // refreshProviders re-fetches /admin/providers (runtime health) and
  // /v1/models (model catalog) for the ProvidersView auto-poll so local
  // provider model lists converge within ~30 s of starting Ollama / LM
  // Studio. Skipped when no providers are configured — the providers
  // tab renders its empty state, there's nothing to converge.
  async function refreshProviders() {
    if ((controlPlaneConfig?.providers?.length ?? 0) === 0) return;
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
    setControlPlaneError("");

    try {
      const snapshot = await resolveDashboardSnapshot({
        activeChatSessionID,
        previous: {
          providers,
          budget,
          accountSummary,
          chatSessions,
          activeChatSession,
          requestLedger,
          controlPlaneConfig,
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
      setControlPlaneConfig(snapshot.controlPlaneConfig);
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
          `?scope=provider&provider=${encodeURIComponent(headers.provider)}`,
        );
        setBudget(scopedBudget.data);
      } catch {
        // Best-effort; the gateway may not have a per-provider budget
        // record for this slice yet.
      }

      await refreshChatSessionState(sessionID);
      setStreamingContent(null);
      await refreshRuntimeState();
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
      await refreshRuntimeState();
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

  function resetControlPlaneFeedback() {
    setControlPlaneError("");
    setNotice(null);
  }

  async function runControlPlaneMutation(options: {
    action: () => Promise<void>;
    successMessage: string;
    errorMessage: string;
    failureDetail: string;
  }) {
    resetControlPlaneFeedback();
    try {
      await options.action();
      await loadDashboard();
      setNoticeMessage("success", options.successMessage);
    } catch (error) {
      setControlPlaneError(describeError(error, options.failureDetail));
      setNoticeMessage("error", options.errorMessage);
    }
  }

  // setProviderAPIKey is the single operation for managing a provider's API key.
  // An empty `key` clears the existing credential; non-empty sets/replaces it.
  async function setProviderAPIKey(id: string, key: string) {
    await runControlPlaneMutation({
      successMessage: key === "" ? "API key cleared." : "API key saved.",
      errorMessage: key === "" ? "Failed to clear API key." : "Failed to save API key.",
      failureDetail: key === "" ? "failed to clear provider api key" : "failed to save provider api key",
      action: async () => {
        await setProviderAPIKeyRequest(id, key);
      },
    });
  }

  async function createProvider(params: { name: string; preset_id?: string; custom_name?: string; base_url?: string; api_key?: string; kind: string; protocol: string }): Promise<void> {
    await createProviderRequest(params);
    await loadDashboard();
  }

  async function deleteProvider(id: string): Promise<void> {
    await deleteProviderRequest(id);
    await loadDashboard();
  }

  async function setProviderBaseURL(id: string, baseURL: string): Promise<void> {
    await setProviderBaseURLRequest(id, baseURL);
    // loadDashboard refreshes controlPlaneConfig (the source of truth for base_url
    // shown in the table), then refreshProviders re-runs model discovery
    // against the new endpoint so the model list updates immediately.
    await loadDashboard();
    await refreshProviders();
  }

  async function setProviderName(id: string, name: string): Promise<void> {
    await setProviderNameRequest(id, name);
    // The label change only affects controlPlaneConfig (table column) — no need
    // to rerun model discovery, so skip refreshProviders.
    await loadDashboard();
  }

  async function setProviderCustomName(id: string, customName: string): Promise<void> {
    await setProviderCustomNameRequest(id, customName);
    await loadDashboard();
  }

  // Policy rule mutations follow the same runControlPlaneMutation contract
  // as the tenant / API key flows: success populates the toast notice
  // + clears controlPlaneError; failure populates BOTH inline banner
  // and toast so an operator can't miss the error regardless of
  // viewport focus.
  async function upsertPolicyRule(payload: PolicyRuleUpsertPayload) {
    await runControlPlaneMutation({
      successMessage: "Policy rule saved.",
      errorMessage: "Failed to save policy rule.",
      failureDetail: "failed to save policy rule",
      action: async () => {
        await upsertPolicyRuleRequest(payload);
      },
    });
  }

  async function deletePolicyRule(id: string) {
    await runControlPlaneMutation({
      successMessage: "Policy rule deleted.",
      errorMessage: "Failed to delete policy rule.",
      failureDetail: "failed to delete policy rule",
      action: async () => {
        await deletePolicyRuleRequest(id);
      },
    });
  }

  async function upsertPricebookEntry(entry: PricebookEntryUpsertPayload) {
    await runControlPlaneMutation({
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
    resetControlPlaneFeedback();
    await runControlPlaneMutation({
      successMessage: "Price cleared.",
      errorMessage: "Failed to clear price.",
      failureDetail: "failed to clear pricebook entry",
      action: async () => {
        await deletePricebookEntryRequest(provider, model);
      },
    });
  }

  // previewPricebookImport intentionally does NOT call runControlPlaneMutation —
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
    setActiveChatSessionID(id);
    if (!id) {
      setActiveChatSession(null);
      return;
    }
    try {
      const payload = await getChatSession(id);
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
      await deleteChatSessionRequest(id);
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
      const payload = await updateChatSessionRequest(id, title);
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
      const result = await getChatSessions(20, chatSessions.length);
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
      controlPlaneConfig,
      controlPlaneError,
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
    },
    actions: {
      copyCommand,
      deletePolicyRule,
      createChatSession,
      deleteChatSession,
      renameChatSession,
      loadDashboard,
      resetBudget,
      setBudgetAmountUsd,
      setBudgetLimitUsd,
      setMessage,
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
      upsertPricebookEntry,
      deletePricebookEntry,
      previewPricebookImport,
      applyPricebookImport,
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
  controlPlaneConfig: PromiseSettledResult<ConfiguredStateResponse>;
  retentionRuns: PromiseSettledResult<{ object: string; data: RetentionRunData[] }>;
};

type DashboardPreviousState = {
  providers: ProviderStatusResponse["data"];
  budget: BudgetStatusResponse["data"] | null;
  accountSummary: AccountSummaryResponse["data"] | null;
  chatSessions: ChatSessionsResponse["data"];
  activeChatSession: ChatSessionRecord | null;
  requestLedger: RequestLedgerResponse["data"];
  controlPlaneConfig: ConfiguredStateResponse["data"] | null;
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
  controlPlaneConfig: ConfiguredStateResponse["data"] | null;
  retentionRuns: RetentionRunData[];
  retentionLastRun: RetentionRunData | null;
};

async function resolveDashboardSnapshot(args: {
  activeChatSessionID: string;
  previous: DashboardPreviousState;
}): Promise<DashboardSnapshot> {
  const results = await loadDashboardResults();
  const health = requireFulfilledDashboardResult(results.health);
  const sessionInfo = results.session.status === "fulfilled" ? results.session.value.data : null;
  const models = resolveModelsResult(results.models);
  const providers = resolveDashboardResult(results.providers, args.previous.providers);
  const providerPresets = results.providerPresets.status === "fulfilled" ? results.providerPresets.value.data : [];
  const budget = resolveDashboardResult(results.budget, args.previous.budget);
  const accountSummary = resolveDashboardResult(results.accountSummary, args.previous.accountSummary);
  const requestLedger = resolveDashboardResult(results.requestLedger, args.previous.requestLedger);
  const controlPlaneConfig = resolveDashboardResult(results.controlPlaneConfig, args.previous.controlPlaneConfig);
  const retentionRuns = resolveDashboardResult(results.retentionRuns, args.previous.retentionRuns);
  const retentionLastRun = retentionRuns[0] ?? null;
  const chatState = await resolveChatDashboardState({
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
    controlPlaneConfig,
    retentionRuns,
    retentionLastRun,
  };
}

async function loadDashboardResults(): Promise<DashboardResults> {
  // Single-user mode: no auth gate. /healthz + /v1/whoami still come
  // first so the gateway has time to surface its health state, then
  // every other endpoint fans out in parallel.
  const [health, session] = await Promise.allSettled([
    getHealth(),
    getSession(),
  ]);

  // Initialize each result as rejected so TS knows these are definitely
  // assigned before we read them; the inline .then handlers below
  // overwrite them with the real outcome.
  const initialReject = <T,>(): PromiseSettledResult<T> => ({ status: "rejected", reason: new Error("uninitialized") });
  let models: PromiseSettledResult<ModelResponse> = initialReject();
  let providers: PromiseSettledResult<ProviderStatusResponse> = initialReject();
  let providerPresets: PromiseSettledResult<{ object: string; data: ProviderPresetRecord[] }> = initialReject();
  let budget: PromiseSettledResult<BudgetStatusResponse> = initialReject();
  let accountSummary: PromiseSettledResult<AccountSummaryResponse> = initialReject();
  let chatSessions: PromiseSettledResult<ChatSessionsResponse> = initialReject();
  let requestLedger: PromiseSettledResult<RequestLedgerResponse> = initialReject();
  let controlPlaneConfig: PromiseSettledResult<ConfiguredStateResponse> = initialReject();
  let retentionRuns: PromiseSettledResult<{ object: string; data: RetentionRunData[] }> = initialReject();

  await Promise.all([
    getProviderPresets().then(r => { providerPresets = { status: "fulfilled", value: r }; }, e => { providerPresets = { status: "rejected", reason: e }; }),
    getChatSessions(20).then(r => { chatSessions = { status: "fulfilled", value: r }; }, e => { chatSessions = { status: "rejected", reason: e }; }),
    getModels().then(r => { models = { status: "fulfilled", value: r }; }, e => { models = { status: "rejected", reason: e }; }),
    getBudget("").then(r => { budget = { status: "fulfilled", value: r }; }, e => { budget = { status: "rejected", reason: e }; }),
    getAccountSummary("").then(r => { accountSummary = { status: "fulfilled", value: r }; }, e => { accountSummary = { status: "rejected", reason: e }; }),
    getRequestLedger(20).then(r => { requestLedger = { status: "fulfilled", value: r }; }, e => { requestLedger = { status: "rejected", reason: e }; }),
    getControlPlaneConfig().then(r => { controlPlaneConfig = { status: "fulfilled", value: r }; }, e => { controlPlaneConfig = { status: "rejected", reason: e }; }),
    getRetentionRuns(10).then(r => { retentionRuns = { status: "fulfilled", value: r }; }, e => { retentionRuns = { status: "rejected", reason: e }; }),
  ]);

  // /admin/providers probes upstream provider runtimes; only call it
  // when at least one provider has been configured, otherwise the call
  // returns nothing useful.
  const configured = controlPlaneConfig.status === "fulfilled" ? (controlPlaneConfig.value.data?.providers ?? []) : [];
  if (configured.length > 0) {
    await getProviders().then(
      r => { providers = { status: "fulfilled", value: r }; },
      e => { providers = { status: "rejected", reason: e }; },
    );
  } else {
    providers = { status: "fulfilled", value: { object: "list", data: [] } as ProviderStatusResponse };
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
    controlPlaneConfig,
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
  return [];
}

function resolveDashboardResult<T>(
  result: PromiseSettledResult<{ data: T }>,
  previous: T,
): T {
  if (result.status === "fulfilled") {
    return result.value.data;
  }
  return previous;
}

async function resolveChatDashboardState(args: {
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
    const sessionResult = await getChatSession(activeChatSessionID);
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

// Single-user mode: the session label is fixed. The whoami endpoint
// is still called to surface gateway features in the future, but we
// don't read role/tenant/auth from it.
function deriveSessionState(_sessionInfo: SessionResponse["data"] | null): SessionState {
  return { label: "Local" };
}
