// Test-only composition of the slice + coordinator hooks into the
// legacy {state, actions} viewmodel. Production code does NOT use
// this — views call slice hooks and coordinator hooks directly. The
// composer survives only as a target for the historical
// useRuntimeConsole regression suite (renamed to
// runtime-console-composition.test.tsx) which exercises the same
// composed shape end-to-end.

import { useEffect, useMemo, useRef } from "react";

import { type ChatTarget } from "../app/state/_shared";
import { buildLocalProviderIssue } from "../lib/provider-issues";
import type { LocalProviderIssue } from "../lib/provider-issues";
import { filterModelsByKind, filterModelsByProvider } from "../lib/runtime-utils";
import {
  defaultModelForProvider,
  defaultProviderForChat,
  humanizeChatError,
  isModelValidForProvider,
  providerHasChatRouteEvidence,
} from "../app/runtimeConsoleChatHelpers";
import { deriveSessionState } from "../app/runtimeConsoleDashboard";
import { useApprovals } from "../app/state/approvals";
import { useChat } from "../app/state/chat";
import { useProvidersAndModels } from "../app/state/providersAndModels";
import { useRetention } from "../app/state/retention";
import { useRuntime } from "../app/state/runtime";
import { useSettings } from "../app/state/settings";
import { useUsage } from "../app/state/usage";
import { useAgentAdapterActions } from "../app/state/coordinators/agentAdapters";
import { useChatActions } from "../app/state/coordinators/chat";
import { useDashboardActions } from "../app/state/coordinators/dashboard";
import { usePolicyActions } from "../app/state/coordinators/policy";
import { useProviderActions } from "../app/state/coordinators/providers";
import { useRetentionActions } from "../app/state/coordinators/retention";
import { useSettingsActions } from "../app/state/coordinators/settings";
import type { ChatSessionRecord } from "../types/chat";
import type { ConfiguredStateResponse } from "../types/provider";

function chatSessionIsExternal(session: ChatSessionRecord | null): boolean {
  return Boolean(session?.agent_id && session.agent_id !== "hecate");
}

function chatSessionIsBusy(session: ChatSessionRecord | null): boolean {
  const busy = (status?: string) =>
    status === "queued" || status === "running" || status === "awaiting_approval";
  if (!session) return false;
  if (busy(session.status)) return true;
  if ((session.segments ?? []).some((segment) => busy(segment.status))) return true;
  return (session.messages ?? []).some(
    (message) => message.role === "assistant" && busy(message.status),
  );
}

export { humanizeChatError };

export function useRuntimeConsole() {
  // Slice hooks. These own the canonical state for each domain;
  // coordinator hooks below compose them into the action surface
  // that lands in the returned viewmodel.
  const runtime = useRuntime();
  const usage = useUsage();
  const providersAndModels = useProvidersAndModels();
  const chat = useChat();
  const approvals = useApprovals();
  const retention = useRetention();

  // Slice state aliases — preserve the legacy identifier-stable
  // destructure so the rest of the hook body reads the same as
  // the pre-extraction form.
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
  const { setMessage, copyCommand } = runtime.actions;

  const usageSummary = usage.state.summary;
  const usageEvents = usage.state.events;

  const {
    providers,
    providerPresets,
    models,
    agentAdapters,
    agentAdapterApprovalMode,
    agentAdapterHealthByID,
    agentAdapterHealthLoadingByID,
  } = providersAndModels.state;

  const {
    defaultChatTarget,
    chatTargetBySessionID,
    defaultChatToolsEnabled,
    chatToolsEnabledBySessionID,
    agentAdapterID,
    agentWorkspace,
    agentWorkspaceBranch,
    chatSessions,
    activeChatSessionID,
    activeChatSession,
    workspaceModeMutationsBySessionID,
    pendingChatAttachments,
    savedComposerDraftsBySessionID,
    recoverableComposerDraft,
    activeRecoverableComposerDraftID,
    queuedChatMessages,
    model,
    systemPrompt,
    chatCreating,
    chatTurnSessionID,
    chatTurnActive,
    chatLoading,
    chatCancelling,
    chatOwnershipMutationInFlight,
    chatAttachmentTurnDraftCount,
    streamingContent,
    chatResult,
    pendingToolCalls,
    chatError,
    chatErrorCode,
    chatErrorStatus,
    chatErrorAction,
    chatErrorRequestID,
    chatErrorTraceID,
    modelFilter,
    providerFilter,
  } = chat.state;
  const workspaceModeMutation = activeChatSessionID
    ? (workspaceModeMutationsBySessionID.get(activeChatSessionID) ?? null)
    : null;
  const {
    setAgentAdapterID,
    setChatCancelling,
    setChatCancellingSessionID,
    setChatCancellingTurnKind,
    hasChatCancellationOwner,
    setModel,
    setModelFilter,
    setProviderFilter,
    setSystemPrompt,
    setPendingChatAttachments,
    setQueuedChatMessages,
    removeQueuedChatMessage,
    retryQueuedChatMessage,
    updateQueuedChatMessage,
    hasChatAttachmentTurn,
    hasDurableQueuedChatSubmittingFence,
    setChatErrorState,
    chatCancellationOwnsSession,
    currentChatCancellationEpoch,
    chatStopFenceProtectsSession,
  } = chat.actions;
  const setHecateRTKEnabledState = runtime.actions.setHecateRTKEnabled;

  // Settings slice: server config snapshot, settings-mutation
  // error, transient notice banner. Aliased below to the legacy
  // identifiers so the coordinator-hook params can stay shaped
  // around the React-setter signature without each hook needing
  // to know about the slice.
  const settings = useSettings();
  const settingsConfig = settings.state.config;
  const settingsError = settings.state.error;
  const notice = settings.state.notice;
  const setSettingsError = settings.actions.setError;
  const setNotice = settings.actions.setNotice;
  // setSettingsConfig keeps the React useState-setter polymorphism
  // (value | updater) the coordinator hook params were designed
  // around: dashboard.tsx replaces wholesale; providers.tsx uses
  // the functional updater form for optimistic insert/rollback.
  const setSettingsConfig = useMemo(
    () =>
      (
        next:
          | ConfiguredStateResponse["data"]
          | null
          | ((
              current: ConfiguredStateResponse["data"] | null,
            ) => ConfiguredStateResponse["data"] | null),
      ) => {
        if (typeof next === "function") {
          settings.actions.updateConfig(
            next as (
              current: ConfiguredStateResponse["data"] | null,
            ) => ConfiguredStateResponse["data"] | null,
          );
        } else {
          settings.actions.setConfig(next);
        }
      },
    [settings.actions],
  );

  // chatTarget — read by several coordinators (submitAgentChat
  // routes by it, setChatTarget mutates it) and the queued-chat
  // useEffect below. Kept here so the coordinator hooks can read
  // it as a stable parameter rather than each recomputing.
  const chatTarget: ChatTarget =
    activeChatSessionID && activeChatSession
      ? chatSessionIsExternal(activeChatSession)
        ? "external_agent"
        : "agent"
      : defaultChatTarget;

  // Forward-dependency ref. The cycle is dashboard.loadDashboard
  // (settings → providers / policy use it) → chat (loadDashboard
  // calls applyChatSession + syncHecateSelectionFromSession) →
  // settings (chat uses setNoticeMessage). Resolved by letting
  // useSettingsActions resolve loadDashboard lazily through this
  // ref, populated after the dashboard coordinator is constructed.
  const loadDashboardRef = useRef<() => Promise<void>>(() => Promise.resolve());
  const loadDashboardLazy = useMemo(
    () => async () => {
      await loadDashboardRef.current();
    },
    [],
  );

  // Settings coordinator: tiny bundle of helpers (notice, error,
  // mutation template) all backed by the local state setters.
  const settingsActions = useSettingsActions({
    setSettingsError,
    setNotice,
    loadDashboard: loadDashboardLazy,
  });
  const { setNoticeMessage, runSettingsMutation } = settingsActions;

  // Chat coordinator: the biggest bundle (submission, lifecycle,
  // approvals, files, ...). Exposes a few internal helpers
  // (applyChatSession, syncHecateSelectionFromSession,
  // refreshRuntimeState, submitAgentChat) the dashboard
  // coordinator and the queued-chat useEffect below consume.
  const chatActions = useChatActions({
    chatTarget,
    setNoticeMessage,
  });
  const queuedMessageAttemptSignatureRef = useRef("");

  // Dashboard coordinator: loadDashboard fans the resolved
  // snapshot across the slices. refreshProviders + refreshRuntimeState
  // are re-exposed for symmetry. Constructed after chatActions so it
  // can compose them; the loadDashboardRef gets populated below.
  const dashboardActions = useDashboardActions({
    settingsConfig,
    setSettingsConfig,
    setSettingsError,
    applyChatSession: chatActions.applyChatSession,
    syncHecateSelectionFromSession: chatActions.syncHecateSelectionFromSession,
    refreshRuntimeState: chatActions.refreshRuntimeState,
  });

  // Provider CRUD. Composes settings (notice / mutation template)
  // and reads chat + providersAndModels slice state internally for
  // the optimistic-update + rollback paths.
  const providerActions = useProviderActions({
    settingsConfig,
    setSettingsConfig,
    setSettingsError,
    loadDashboard: dashboardActions.loadDashboard,
    refreshProviders: dashboardActions.refreshProviders,
    setNoticeMessage: settingsActions.setNoticeMessage,
    describeError: settingsActions.describeError,
    resetSettingsFeedback: settingsActions.resetSettingsFeedback,
    runSettingsMutation,
  });

  // Policy mutations follow the runSettingsMutation contract.
  const policyActions = usePolicyActions({ runSettingsMutation });

  // Adapter credential + probe operations. The slice returns
  // Results; this coordinator routes failures to the notice banner.
  const adapterActions = useAgentAdapterActions({ setNoticeMessage });

  // Retention coordinator. Slice owns state; this coordinator
  // wires success / failure to the notice banner.
  const retentionActions = useRetentionActions({ setNotice });

  // Populate the forward-dependency ref so runSettingsMutation
  // resolves loadDashboard through the live dashboard coordinator
  // by the time any coordinator-triggered settings mutation
  // actually fires.
  loadDashboardRef.current = dashboardActions.loadDashboard;

  // Derived state - identical to pre-extraction.
  const healthyProviders = providers.filter((provider) => provider.healthy).length;
  const localProviders = providers.filter((provider) => provider.kind === "local");
  const cloudProviders = providers.filter((provider) => provider.kind === "cloud");
  const localModels = models.filter((entry) => entry.metadata?.provider_kind === "local");
  const cloudModels = models.filter((entry) => entry.metadata?.provider_kind === "cloud");
  const healthyLocalProviders = localProviders.filter((provider) => provider.healthy).length;
  const healthyCloudProviders = cloudProviders.filter((provider) => provider.healthy).length;

  const visibleModels = useMemo(
    () => filterModelsByKind(models, modelFilter),
    [modelFilter, models],
  );
  const providerScopedModels = useMemo(
    () => filterModelsByProvider(visibleModels, providerFilter, settingsConfig?.providers ?? []),
    [providerFilter, settingsConfig?.providers, visibleModels],
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
    void dashboardActions.loadDashboard();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (!chatCancelling || hasChatCancellationOwner()) return;
    setChatCancelling(false);
    setChatCancellingSessionID("");
    setChatCancellingTurnKind("");
  }, [
    chatCancelling,
    hasChatCancellationOwner,
    setChatCancelling,
    setChatCancellingSessionID,
    setChatCancellingTurnKind,
  ]);

  // Reconnect catch-up: whenever the active agent-chat session
  // changes (initial mount with a persisted id, user-driven switch,
  // post-loadDashboard hydration), refetch the pending approvals so
  // anything created/resolved while we were disconnected is
  // reconciled. Subsequent SSE events mutate this same map.
  useEffect(() => {
    if (!activeChatSessionID) return;
    const sessionID = activeChatSessionID;
    const cancellationEpoch = currentChatCancellationEpoch(sessionID);
    const isCurrent = () =>
      currentChatCancellationEpoch(sessionID) === cancellationEpoch &&
      !chatCancellationOwnsSession(sessionID) &&
      !chatStopFenceProtectsSession(sessionID);
    if (!isCurrent()) return;
    void approvals.actions.refetchPending(sessionID, isCurrent);
    // refetchPending is a stable callback from the slice; no need
    // to include it in deps.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeChatSessionID]);

  useEffect(() => {
    if (!activeChatSession || chatSessionIsExternal(activeChatSession)) {
      return;
    }
    setHecateRTKEnabledState(Boolean(activeChatSession.rtk_enabled));
  }, [activeChatSession?.id, activeChatSession?.rtk_enabled]);

  useEffect(() => {
    if (!notice) {
      return;
    }
    const timeout = window.setTimeout(() => {
      settings.actions.dismissNoticeIfMatching(notice);
    }, 3000);
    return () => window.clearTimeout(timeout);
  }, [notice, settings.actions]);

  useEffect(() => {
    if (providerFilter !== "auto") {
      return;
    }
    if (!settingsConfig) {
      return;
    }

    const configuredProviders = settingsConfig?.providers ?? [];
    const nextProvider = defaultProviderForChat(models, configuredProviders, providers);
    if (nextProvider === providerFilter) {
      return;
    }
    setProviderFilter(nextProvider);
    const nextModel =
      model &&
      isModelValidForProvider(
        model,
        nextProvider,
        models,
        providers,
        configuredProviders,
        providerPresets,
      )
        ? model
        : defaultModelForProvider(
            nextProvider,
            models,
            providers,
            configuredProviders,
            providerPresets,
          );
    setModel(nextModel);
  }, [model, models, providerFilter, providerPresets, providers, settingsConfig]);

  useEffect(() => {
    if (providerFilter === "auto") {
      return;
    }
    if (!settingsConfig) {
      return;
    }
    const configuredProviders = settingsConfig.providers ?? [];
    const hasProviderEvidence = providerHasChatRouteEvidence(
      providerFilter,
      models,
      configuredProviders,
      providers,
    );
    const activeHecateSessionUsesProvider =
      activeChatSession?.agent_id === "hecate" && activeChatSession.provider === providerFilter;
    if (!hasProviderEvidence) {
      if (activeHecateSessionUsesProvider) {
        return;
      }
      const nextProvider = defaultProviderForChat(models, configuredProviders, providers);
      setProviderFilter(nextProvider);
      setModel(
        defaultModelForProvider(
          nextProvider,
          models,
          providers,
          configuredProviders,
          providerPresets,
        ),
      );
      return;
    }
    const stillValid = isModelValidForProvider(
      model,
      providerFilter,
      models,
      providers,
      configuredProviders,
      providerPresets,
    );
    if (stillValid) {
      return;
    }
    const nextModel = defaultModelForProvider(
      providerFilter,
      models,
      providers,
      configuredProviders,
      providerPresets,
    );
    setModel(nextModel);
  }, [
    activeChatSession,
    model,
    models,
    providerFilter,
    providerPresets,
    providers,
    settingsConfig,
  ]);

  // When models load, validate the selected model. If it's not in the list
  // (e.g. stale localStorage), clear it and let the operator pick.
  useEffect(() => {
    if (providerFilter !== "auto") return;
    if (model === "" || models.length === 0) return;
    if (models.some((m) => m.id === model)) return;
    setModel("");
  }, [model, models, providerFilter]);

  useEffect(() => {
    if (queuedChatMessages.length === 0) {
      queuedMessageAttemptSignatureRef.current = "";
      return;
    }
    if (chatCreating || chatLoading || chatCancelling) {
      return;
    }
    if (chatOwnershipMutationInFlight) {
      return;
    }
    if (workspaceModeMutation?.sessionID === activeChatSessionID) {
      return;
    }
    if (hasChatAttachmentTurn() || pendingChatAttachments.length > 0) {
      return;
    }
    if (chatSessionIsBusy(activeChatSession)) {
      return;
    }
    if (!activeChatSessionID) {
      return;
    }
    if (activeChatSession?.id !== activeChatSessionID) {
      return;
    }
    const next = queuedChatMessages.find((item) => item.session_id === activeChatSessionID);
    if (!next) {
      return;
    }
    if (!next.content.trim()) {
      setQueuedChatMessages((current) => current.filter((item) => item.id !== next.id));
      return;
    }
    if (next.delivery_state) {
      queuedMessageAttemptSignatureRef.current = "";
      return;
    }
    const attemptSignature = `${next.id}\u0000${next.content}`;
    if (queuedMessageAttemptSignatureRef.current === attemptSignature) {
      return;
    }
    queuedMessageAttemptSignatureRef.current = attemptSignature;
    const submitting = {
      ...next,
      delivery_state: "submitting" as const,
      delivery_idempotency_keyed: true,
      delivery_baseline_message_ids: (activeChatSession.messages ?? []).map(
        (message) => message.id,
      ),
    };
    let marked = false;
    setQueuedChatMessages((current) =>
      current.map((item) => {
        if (item.id !== next.id || item.content !== next.content) return item;
        marked = true;
        return submitting;
      }),
    );
    if (!marked) {
      queuedMessageAttemptSignatureRef.current = "";
      return;
    }
    if (!hasDurableQueuedChatSubmittingFence(submitting)) {
      setQueuedChatMessages((current) =>
        current.map((item) =>
          item.id === next.id && item.content === next.content
            ? { ...item, delivery_state: "retryable" }
            : item,
        ),
      );
      queuedMessageAttemptSignatureRef.current = "";
      const persistenceError =
        "Queued delivery is paused because browser storage could not persist its submission fence. Free browser storage, then choose Retry or remove the queued message.";
      setChatErrorState(new Error(persistenceError));
      setNoticeMessage("error", persistenceError);
      return;
    }
    void chatActions.submitAgentChat(submitting);
    // submitAgentChat deliberately stays out of the dependency list: it
    // reads the queued snapshot passed above, not the live composer state.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    activeChatSession?.id,
    activeChatSession?.latest_run_id,
    activeChatSession?.status,
    activeChatSession?.updated_at,
    activeChatSessionID,
    chatCancelling,
    chatOwnershipMutationInFlight,
    workspaceModeMutation,
    chatAttachmentTurnDraftCount,
    chatCreating,
    chatLoading,
    hasDurableQueuedChatSubmittingFence,
    pendingChatAttachments.length,
    queuedChatMessages,
    setChatErrorState,
    setNoticeMessage,
  ]);

  return {
    state: {
      activeChatSession,
      activeChatSessionID,
      workspaceModeMutation,
      recoverableComposerDraft,
      activeRecoverableComposerDraftID,
      savedComposerDraftsBySessionID,
      usageSummary,
      agentAdapterID,
      agentAdapters,
      chatCancelling,
      chatSessions,
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
      chatAttachmentTurnDraftCount,
      chatCreating,
      chatTurnSessionID,
      chatTurnActive,
      streamingContent,
      chatResult,
      chatTarget,
      chatTargetBySessionID,
      defaultChatToolsEnabled,
      chatToolsEnabledBySessionID,
      pendingToolCalls,
      pendingChatAttachments,
      queuedChatMessages,
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
      retentionError: retention.state.error,
      retentionLastRun: retention.state.lastRun,
      retentionLoading: retention.state.loading,
      retentionRuns: retention.state.runs,
      retentionSubsystems: retention.state.subsystems,
      runtimeHeaders,
      visibleModels,
      pendingApprovalsBySessionID: approvals.state.pendingBySessionID,
      chatGrants: approvals.state.grants,
      chatGrantsLoading: approvals.state.grantsLoading,
      chatGrantsError: approvals.state.grantsError,
      agentAdapterApprovalMode,
      agentAdapterHealthByID,
      agentAdapterHealthLoadingByID,
    },
    actions: {
      copyCommand,
      cancelAgentChat: chatActions.cancelAgentChat,
      compactChatSession: chatActions.compactChatSession,
      deletePolicyRule: policyActions.deletePolicyRule,
      chooseAgentWorkspace: chatActions.chooseAgentWorkspace,
      createChatSession: chatActions.createChatSession,
      deleteChatSession: chatActions.deleteChatSession,
      renameChatSession: chatActions.renameChatSession,
      loadDashboard: dashboardActions.loadDashboard,
      loadRetentionRuns: retention.actions.loadRuns,
      setAgentAdapterID,
      setNewChatAgent: chatActions.setNewChatAgent,
      setAgentWorkspace: chatActions.updateAgentWorkspace,
      setChatTarget: chatActions.setChatTarget,
      setChatToolsEnabled: chatActions.setChatToolsEnabled,
      setMessage,
      setPendingChatAttachments,
      removeQueuedChatMessage,
      reconcileQueuedChatMessage: chatActions.reconcileQueuedChatMessage,
      retryQueuedChatMessage,
      updateQueuedChatMessage,
      setSystemPrompt,
      setModel: chatActions.selectChatModel,
      setModelFilter,
      setProviderFilter: chatActions.selectProviderRoute,
      refreshProviders: dashboardActions.refreshProviders,
      setRetentionSubsystems: retention.actions.setSubsystems,
      runRetention: retentionActions.runRetention,
      restoreSavedComposerDraft: chatActions.restoreSavedComposerDraft,
      selectChatSession: chatActions.selectChatSession,
      startNewChat: chatActions.startNewChat,
      submitChat: chatActions.submitChat,
      submitToolResults: chatActions.submitToolResults,
      updateToolResult: chatActions.updateToolResult,
      upsertPolicyRule: policyActions.upsertPolicyRule,
      setProviderAPIKey: providerActions.setProviderAPIKey,
      createProvider: providerActions.createProvider,
      deleteProvider: providerActions.deleteProvider,
      setProviderBaseURL: providerActions.setProviderBaseURL,
      setProviderName: providerActions.setProviderName,
      setProviderCustomName: providerActions.setProviderCustomName,
      setProviderAccountID: providerActions.setProviderAccountID,
      getChatApproval: chatActions.getChatApproval,
      listChatMessageFiles: chatActions.listChatMessageFiles,
      getChatWorkspaceDiff: chatActions.getChatWorkspaceDiff,
      getChatWorkspaceFileDiff: chatActions.getChatWorkspaceFileDiff,
      revertChatWorkspaceFiles: chatActions.revertChatWorkspaceFiles,
      getChatMessageFileDiff: chatActions.getChatMessageFileDiff,
      resolveTaskApproval: chatActions.resolveTaskApproval,
      resolveChatApproval: chatActions.resolveChatApproval,
      cancelChatApproval: chatActions.cancelChatApproval,
      listChatGrants: approvals.actions.loadGrants,
      deleteChatGrant: chatActions.deleteChatGrant,
      setChatConfigOption: chatActions.setChatConfigOption,
      setHecateRTKEnabled: chatActions.setHecateRTKEnabled,
      setHecateWorkspaceMode: chatActions.setHecateWorkspaceMode,
      refreshAgentAdapters: adapterActions.refreshAgentAdapters,
      probeAgentAdapter: adapterActions.probeAgentAdapter,
      dismissNotice: () => setNotice(null),
    },
  };
}

export type RuntimeConsoleViewModel = ReturnType<typeof useRuntimeConsole>;
