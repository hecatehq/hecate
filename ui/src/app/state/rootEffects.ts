// Root effects component. The retired useRuntimeConsole facade
// owned a cluster of useEffect blocks that don't belong in any
// individual view: dashboard load on mount, approvals catch-up on
// session change, RTK enabled sync on session change, notice toast
// auto-dismiss, provider/model defaults cascade, queued-message
// drain, external-agent session following. These effects coordinate across slices, so they stay
// outside the views — the AppShell mounts <RootEffects /> once
// at the top so the effects run for the whole session.
//
// Tests intentionally do NOT mount this — the test-only wrapper
// (runtime-console-render.tsx) only sets up slice providers and
// the coordinator override context. View tests assert against
// fixture state, not the dashboard-loaded result.

import { useCallback, useEffect, useMemo, useRef } from "react";

import {
  defaultModelForProvider,
  defaultProviderForChat,
  isModelValidForProvider,
  providerHasChatRouteEvidence,
} from "../runtimeConsoleChatHelpers";
import { useApprovals } from "./approvals";
import { useChat } from "./chat";
import { useChatTarget } from "./derived";
import { useProvidersAndModels } from "./providersAndModels";
import { useProjects } from "./projects";
import { useRuntime } from "./runtime";
import { useSettings } from "./settings";
import { useChatActions } from "./coordinators/chat";
import { useDashboardActions } from "./coordinators/dashboard";
import { useSettingsActions } from "./coordinators/settings";
import {
  externalAgentObserverFailureIsNonRetryable,
  externalAgentObserverFailureIsOrphanedTurn,
  externalAgentObserverRetryDelayMS,
  waitForExternalAgentObserverRetry,
} from "./externalAgentObserver";
import { ApiError, streamChatSession } from "../../lib/api";
import type { ConfiguredStateResponse } from "../../types/provider";

function chatSessionIsExternal(session: { agent_id?: string } | null): boolean {
  return Boolean(session?.agent_id && session.agent_id !== "hecate");
}

function chatSessionIsBusy(
  session: {
    status?: string;
    segments?: Array<{ status?: string }>;
    messages?: Array<{ role: string; status?: string }>;
  } | null,
): boolean {
  const busy = (status?: string) =>
    status === "queued" || status === "running" || status === "awaiting_approval";
  if (!session) return false;
  if (busy(session.status)) return true;
  if ((session.segments ?? []).some((segment) => busy(segment.status))) return true;
  return (session.messages ?? []).some(
    (message) => message.role === "assistant" && busy(message.status),
  );
}

function chatSessionIsTerminal(session: { status?: string } | null): boolean {
  return (
    session?.status === "completed" ||
    session?.status === "failed" ||
    session?.status === "cancelled"
  );
}

function chatSessionHasPendingExternalTurn(
  session: {
    messages?: Array<{ role: string }>;
  } | null,
): boolean {
  const messages = session?.messages ?? [];
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    const role = messages[index]?.role;
    if (role === "assistant") return false;
    if (role === "user") return true;
  }
  return false;
}

export function RootEffects() {
  const runtime = useRuntime();
  const settings = useSettings();
  const approvals = useApprovals();
  const chat = useChat();
  const providersAndModels = useProvidersAndModels();
  const projects = useProjects();
  const chatTarget = useChatTarget();

  // Wire the coordinator graph inline so the loadDashboardRef plumbed
  // through settingsActions actually points at the dashboard
  // coordinator instance we create below. Each `use*Actions` call
  // composes its own slice references; calling the wiring helpers
  // here would create parallel coordinator instances that don't share
  // the ref the way the original shim did.
  const loadDashboardRef = useRef<() => Promise<void>>(() => Promise.resolve());
  const loadDashboardLazy = useMemo(
    () => async () => {
      await loadDashboardRef.current();
    },
    [],
  );
  const settingsActions = useSettingsActions({
    setSettingsError: settings.actions.setError,
    setNotice: settings.actions.setNotice,
    loadDashboard: loadDashboardLazy,
  });

  const chatActions = useChatActions({
    chatTarget,
    setNoticeMessage: settingsActions.setNoticeMessage,
  });
  const externalAgentObserverActionsRef = useRef({
    applyChatSession: chatActions.applyChatSession,
    stopReadTokenAtRequestStart: chat.actions.stopReadTokenAtRequestStart,
    chatStopFenceProtectsSession: chat.actions.chatStopFenceProtectsSession,
    upsertPendingApproval: approvals.actions.upsertPending,
    removePendingApproval: approvals.actions.removePending,
    invalidatePendingApprovals: approvals.actions.invalidatePendingForSession,
    refetchPendingApprovals: approvals.actions.refetchPending,
    fenceDeletedChatSession: chat.actions.fenceDeletedChatSession,
    setChatErrorState: chat.actions.setChatErrorState,
  });
  externalAgentObserverActionsRef.current = {
    applyChatSession: chatActions.applyChatSession,
    stopReadTokenAtRequestStart: chat.actions.stopReadTokenAtRequestStart,
    chatStopFenceProtectsSession: chat.actions.chatStopFenceProtectsSession,
    upsertPendingApproval: approvals.actions.upsertPending,
    removePendingApproval: approvals.actions.removePending,
    invalidatePendingApprovals: approvals.actions.invalidatePendingForSession,
    refetchPendingApprovals: approvals.actions.refetchPending,
    fenceDeletedChatSession: chat.actions.fenceDeletedChatSession,
    setChatErrorState: chat.actions.setChatErrorState,
  };
  const queuedMessageAttemptSignatureRef = useRef("");

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

  const dashboardActions = useDashboardActions({
    settingsConfig: settings.state.config,
    setSettingsConfig,
    setSettingsError: settings.actions.setError,
    applyChatSession: chatActions.applyChatSession,
    syncHecateSelectionFromSession: chatActions.syncHecateSelectionFromSession,
    refreshRuntimeState: chatActions.refreshRuntimeState,
  });
  loadDashboardRef.current = dashboardActions.loadDashboard;
  const projectCatalogRetryAfterOwnershipRef = useRef(false);
  const projectCatalogRecoveryInFlightRef = useRef(false);
  const projectCatalogRecoveryRequestRef = useRef(0);

  const {
    activeChatSession,
    activeChatSessionID,
    chatCreating,
    chatLoading,
    chatTurnActive,
    chatTurnSessionID,
    chatCancelling,
    chatOwnershipMutationInFlight,
    chatAttachmentTurnDraftCount,
    pendingChatAttachments,
    queuedChatMessages,
    model,
    providerFilter,
  } = chat.state;
  const {
    hasChatCancellationOwner,
    setChatCancelling,
    setChatCancellingSessionID,
    setChatCancellingTurnKind,
    setModel,
    setProviderFilter,
    setQueuedChatMessages,
    chatOwnershipMutationBlockReason,
    hasChatAttachmentTurn,
    hasDurableQueuedChatSubmittingFence,
    setChatErrorState,
    chatCancellationOwnsSession,
    currentChatCancellationEpoch,
    chatStopFenceProtectsSession,
  } = chat.actions;
  const { setHecateRTKEnabled: setHecateRTKEnabledState } = runtime.actions;
  const { models, providers, providerPresets } = providersAndModels.state;
  const { notice } = settings.state;
  const { dismissNoticeIfMatching } = settings.actions;
  const settingsConfig = settings.state.config;
  const projectCatalogOwnershipBlocked = Boolean(chatOwnershipMutationBlockReason());
  const selectedObservableExternalSessionID =
    activeChatSession?.id === activeChatSessionID &&
    chatSessionIsExternal(activeChatSession) &&
    (chatSessionIsBusy(activeChatSession) || chatSessionHasPendingExternalTurn(activeChatSession))
      ? activeChatSessionID
      : "";
  const localSubmitOwnsSelectedExternalSession =
    chatTurnActive &&
    chatTurnSessionID !== "" &&
    chatTurnSessionID === selectedObservableExternalSessionID;
  const selectedExternalObservationStartsBusyRef = useRef(false);
  selectedExternalObservationStartsBusyRef.current =
    selectedObservableExternalSessionID !== "" && chatSessionIsBusy(activeChatSession);
  const selectedExternalObservationStartsPendingRef = useRef(false);
  selectedExternalObservationStartsPendingRef.current =
    selectedObservableExternalSessionID !== "" &&
    chatSessionHasPendingExternalTurn(activeChatSession);
  const projectCatalogOwnershipWasBlockedRef = useRef(projectCatalogOwnershipBlocked);
  const recoverProjectCatalogAfterOwnership = useCallback(
    async function recoverProjectCatalogAfterOwnership(): Promise<void> {
      projectCatalogRecoveryRequestRef.current += 1;
      if (projectCatalogRecoveryInFlightRef.current) return;
      projectCatalogRecoveryInFlightRef.current = true;
      try {
        let handledRequest = 0;
        while (handledRequest < projectCatalogRecoveryRequestRef.current) {
          if (chatOwnershipMutationBlockReason()) {
            projectCatalogRetryAfterOwnershipRef.current = true;
            return;
          }
          let request = projectCatalogRecoveryRequestRef.current;
          let result = await projects.actions.loadProjects({
            shouldApply: () => !chatOwnershipMutationBlockReason(),
          });
          if (chatOwnershipMutationBlockReason()) {
            projectCatalogRetryAfterOwnershipRef.current = true;
            return;
          }
          if (result.status === "superseded") {
            // The first attempt may have joined an older request whose
            // participant had already lost apply authority. Make one fresh
            // attempt, then defer instead of spinning on another participant.
            request = projectCatalogRecoveryRequestRef.current;
            result = await projects.actions.loadProjects({
              shouldApply: () => !chatOwnershipMutationBlockReason(),
            });
            if (chatOwnershipMutationBlockReason()) {
              projectCatalogRetryAfterOwnershipRef.current = true;
              return;
            }
          }
          handledRequest = request;
          if (result.status === "superseded") {
            if (handledRequest < projectCatalogRecoveryRequestRef.current) {
              continue;
            }
            projectCatalogRetryAfterOwnershipRef.current = true;
            return;
          }
          // A second ownership-clear request may arrive while a recovery is
          // pending. Drain it with one fresh read even if the older read was
          // applied or failed.
        }
      } finally {
        projectCatalogRecoveryInFlightRef.current = false;
      }
    },
    [chatOwnershipMutationBlockReason, projects.actions],
  );
  const loadProjectCatalog = useCallback(
    async function loadProjectCatalog(): Promise<void> {
      let ownershipDeniedApply = false;
      const result = await projects.actions.loadProjects({
        shouldApply: () => {
          ownershipDeniedApply = Boolean(chatOwnershipMutationBlockReason());
          return !ownershipDeniedApply;
        },
      });
      if (result.status !== "superseded" || !ownershipDeniedApply) return;
      projectCatalogRetryAfterOwnershipRef.current = true;
      if (chatOwnershipMutationBlockReason()) return;
      // Ownership may clear after the predicate denied the apply but before
      // this continuation runs. Retry immediately so mount hydration is not
      // stranded waiting for another render.
      projectCatalogRetryAfterOwnershipRef.current = false;
      void recoverProjectCatalogAfterOwnership();
    },
    [chatOwnershipMutationBlockReason, projects.actions, recoverProjectCatalogAfterOwnership],
  );

  // Mount-time dashboard load. The facade ran the same effect; the
  // dashboard coordinator's loadDashboard is stable, so this is a
  // one-shot regardless of re-renders.
  useEffect(() => {
    void dashboardActions.loadDashboard();
    void loadProjectCatalog();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    const ownershipWasBlocked = projectCatalogOwnershipWasBlockedRef.current;
    projectCatalogOwnershipWasBlockedRef.current = projectCatalogOwnershipBlocked;
    if (projectCatalogOwnershipBlocked) return;
    if (!ownershipWasBlocked && !projectCatalogRetryAfterOwnershipRef.current) return;
    projectCatalogRetryAfterOwnershipRef.current = false;
    // Project Assistant and other project reads can be superseded while an
    // attachment turn owns chat scope, then outlive the Projects surface itself.
    // Reconcile once at the app lifetime when that ownership window closes.
    void recoverProjectCatalogAfterOwnership();
  }, [projectCatalogOwnershipBlocked, recoverProjectCatalogAfterOwnership]);

  useEffect(() => {
    if (!chatCancelling || hasChatCancellationOwner()) return;
    // Fixture/rehydration compatibility for state projected without a live
    // process-local cancellation owner. Live owners release their own exact
    // token; chatLoading is not an ownership signal.
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
  // changes (initial mount, user-driven switch, post-load), refetch
  // pending approvals so anything created/resolved while we were
  // disconnected is reconciled.
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
    // refetchPending is a stable callback from the slice.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeChatSessionID]);

  useEffect(() => {
    const sessionID = selectedObservableExternalSessionID;
    if (!sessionID || localSubmitOwnsSelectedExternalSession) return;

    const abortController = new AbortController();
    let disposed = false;

    const observe = async () => {
      let retryAttempt = 0;
      let observedCurrentTurn = selectedExternalObservationStartsBusyRef.current;
      const beganFromAdmissionGap = selectedExternalObservationStartsPendingRef.current;
      let catchUpApprovalsOnNextEvent = false;
      while (!disposed && !abortController.signal.aborted) {
        let acceptedTerminalSnapshot = false;
        const actions = externalAgentObserverActionsRef.current;
        const stopToken = actions.stopReadTokenAtRequestStart(sessionID);
        const snapshotSource =
          stopToken === null
            ? ({ kind: "unscoped" } as const)
            : ({ kind: "stop_read", stopToken } as const);

        try {
          await streamChatSession(
            sessionID,
            (event) => {
              if (disposed || abortController.signal.aborted) return;
              const currentActions = externalAgentObserverActionsRef.current;
              if (catchUpApprovalsOnNextEvent) {
                catchUpApprovalsOnNextEvent = false;
                void currentActions.refetchPendingApprovals(
                  sessionID,
                  () =>
                    !disposed &&
                    !abortController.signal.aborted &&
                    !currentActions.chatStopFenceProtectsSession(sessionID),
                );
              }
              switch (event.type) {
                case "session_update": {
                  if (event.payload.data.id !== sessionID) return;
                  const busy = chatSessionIsBusy(event.payload.data);
                  if (busy) observedCurrentTurn = true;
                  const terminal =
                    chatSessionIsTerminal(event.payload.data) &&
                    (observedCurrentTurn || beganFromAdmissionGap);
                  const applied = currentActions.applyChatSession(
                    event.payload.data,
                    snapshotSource,
                  );
                  if (terminal && applied) {
                    // A terminal session cannot retain an actionable approval.
                    // Resolve events are deliberately droppable under SSE
                    // backpressure, so the accepted terminal snapshot is the
                    // authoritative fallback. An unaccepted Stop-fenced
                    // snapshot cannot clear approval state.
                    currentActions.invalidatePendingApprovals(sessionID);
                    acceptedTerminalSnapshot = true;
                  }
                  return;
                }
                case "approval.requested":
                  if (
                    event.payload.session_id !== sessionID ||
                    currentActions.chatStopFenceProtectsSession(sessionID)
                  ) {
                    return;
                  }
                  currentActions.upsertPendingApproval(event.payload);
                  return;
                case "approval.resolved":
                  if (event.payload.session_id !== sessionID) return;
                  currentActions.removePendingApproval(
                    event.payload.session_id,
                    event.payload.approval_id,
                  );
                  return;
              }
            },
            abortController.signal,
          );
        } catch (error) {
          if (disposed || abortController.signal.aborted) return;
          if (error instanceof ApiError && error.status === 404) {
            const currentActions = externalAgentObserverActionsRef.current;
            currentActions.invalidatePendingApprovals(sessionID);
            if (!currentActions.fenceDeletedChatSession(sessionID)) {
              currentActions.setChatErrorState(
                new Error(
                  "This chat no longer exists on the runtime, but Hecate could not safely remove its queued prompts from browser storage. Free browser storage or clear Hecate site data before continuing.",
                ),
              );
            }
            return;
          }
          if (externalAgentObserverFailureIsOrphanedTurn(error)) {
            externalAgentObserverActionsRef.current.setChatErrorState(
              error,
              "This External Agent turn is no longer active.",
            );
            return;
          }
          if (externalAgentObserverFailureIsNonRetryable(error)) {
            externalAgentObserverActionsRef.current.setChatErrorState(
              error,
              "Failed to follow the External Agent chat.",
            );
            return;
          }
        }

        if (disposed || abortController.signal.aborted || acceptedTerminalSnapshot) return;
        const retryDelay = externalAgentObserverRetryDelayMS(retryAttempt);
        retryAttempt += 1;
        if (!(await waitForExternalAgentObserverRetry(abortController.signal, retryDelay))) return;
        catchUpApprovalsOnNextEvent = true;
      }
    };

    void observe();
    return () => {
      disposed = true;
      abortController.abort();
    };
  }, [localSubmitOwnsSelectedExternalSession, selectedObservableExternalSessionID]);

  useEffect(() => {
    if (!activeChatSession || chatSessionIsExternal(activeChatSession)) {
      return;
    }
    setHecateRTKEnabledState(Boolean(activeChatSession.rtk_enabled));
  }, [
    activeChatSession?.id,
    activeChatSession?.rtk_enabled,
    setHecateRTKEnabledState,
    activeChatSession,
  ]);

  useEffect(() => {
    if (!notice) return;
    const timeout = window.setTimeout(() => {
      dismissNoticeIfMatching(notice);
    }, 3000);
    return () => window.clearTimeout(timeout);
  }, [notice, dismissNoticeIfMatching]);

  // Provider auto-default cascade.
  useEffect(() => {
    if (!settingsConfig) return;
    if (providerFilter !== "auto") return;
    const configuredProviders = settingsConfig?.providers ?? [];
    const nextProvider = defaultProviderForChat(models, configuredProviders, providers);
    if (nextProvider === providerFilter) return;
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
  }, [
    model,
    models,
    providerFilter,
    providerPresets,
    providers,
    settingsConfig,
    setProviderFilter,
    setModel,
  ]);

  useEffect(() => {
    if (!settingsConfig) return;
    if (providerFilter === "auto") return;
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
      if (activeHecateSessionUsesProvider) return;
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
    if (stillValid) return;
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
    setModel,
    setProviderFilter,
  ]);

  // When models load, validate the selected model. If localStorage points at
  // a stale model, fall back to a discovered default instead of leaving the
  // composer in a broken "model required" state.
  useEffect(() => {
    if (providerFilter !== "auto") return;
    if (model === "" || models.length === 0) return;
    if (models.some((m) => m.id === model)) return;
    setModel(models.find((entry) => entry.metadata?.default)?.id ?? models[0]?.id ?? "");
  }, [model, models, providerFilter, setModel]);

  // Queued-message drain — sends the next queued message once the
  // active chat is idle.
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
    // The queue setter is configured for synchronous write-through. If a
    // newer mutation removed or edited this snapshot, do not dispatch stale
    // work that was never fenced as submitting in localStorage.
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
      settingsActions.setNoticeMessage("error", persistenceError);
      return;
    }
    void chatActions.submitAgentChat(submitting);
    // submitAgentChat reads queued snapshot from its argument, not from
    // closure state, so it stays out of the dep array.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    activeChatSession?.id,
    activeChatSession?.latest_run_id,
    activeChatSession?.status,
    activeChatSession?.updated_at,
    activeChatSessionID,
    chatCancelling,
    chatOwnershipMutationInFlight,
    chatAttachmentTurnDraftCount,
    chatCreating,
    chatLoading,
    hasDurableQueuedChatSubmittingFence,
    pendingChatAttachments.length,
    queuedChatMessages,
    setChatErrorState,
  ]);

  return null;
}
