// Root effects component. The retired useRuntimeConsole facade
// owned a cluster of useEffect blocks that don't belong in any
// individual view: dashboard load on mount, approvals catch-up on
// session change, RTK enabled sync on session change, notice toast
// auto-dismiss, provider/model defaults cascade, queued-message
// drain. These effects coordinate across slices, so they stay
// outside the views — the AppShell mounts <RootEffects /> once
// at the top so the effects run for the whole session.
//
// Tests intentionally do NOT mount this — the test-only wrapper
// (runtime-console-render.tsx) only sets up slice providers and
// the coordinator override context. View tests assert against
// fixture state, not the dashboard-loaded result.

import { useEffect, useMemo, useRef } from "react";

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

  const {
    activeChatSession,
    activeChatSessionID,
    chatLoading,
    chatCancelling,
    queuedChatMessages,
    model,
    providerFilter,
  } = chat.state;
  const { setChatCancelling, setModel, setProviderFilter, setQueuedChatMessages } = chat.actions;
  const { setHecateRTKEnabled: setHecateRTKEnabledState } = runtime.actions;
  const { models, providers } = providersAndModels.state;
  const { agentAdapters } = providersAndModels.state;
  const { notice } = settings.state;
  const { dismissNoticeIfMatching } = settings.actions;
  const settingsConfig = settings.state.config;
  const probedAgentAdapterIDsRef = useRef<Set<string>>(new Set());

  // Mount-time dashboard load. The facade ran the same effect; the
  // dashboard coordinator's loadDashboard is stable, so this is a
  // one-shot regardless of re-renders.
  useEffect(() => {
    void dashboardActions.loadDashboard();
    void projects.actions.loadProjects();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    const adaptersToProbe = agentAdapters.filter((adapter) => {
      if (!adapter.id || probedAgentAdapterIDsRef.current.has(adapter.id)) return false;
      if (adapter.managed) return false;
      probedAgentAdapterIDsRef.current.add(adapter.id);
      return true;
    });
    if (adaptersToProbe.length === 0) return;

    // Probe direct adapters together so the agent picker converges as
    // soon as the dashboard knows which adapters exist. Managed
    // adapters can run package managers such as npx, so they only run
    // after an explicit operator check in Connections.
    void Promise.allSettled(
      adaptersToProbe.map((adapter) => providersAndModels.actions.probeAgentAdapter(adapter.id)),
    );
  }, [agentAdapters, providersAndModels.actions]);

  useEffect(() => {
    if (!chatLoading) {
      setChatCancelling(false);
    }
  }, [chatLoading, setChatCancelling]);

  // Reconnect catch-up: whenever the active agent-chat session
  // changes (initial mount, user-driven switch, post-load), refetch
  // pending approvals so anything created/resolved while we were
  // disconnected is reconciled.
  useEffect(() => {
    if (!activeChatSessionID) return;
    void approvals.actions.refetchPending(activeChatSessionID);
    // refetchPending is a stable callback from the slice.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeChatSessionID]);

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
      model && isModelValidForProvider(model, nextProvider, models, providers)
        ? model
        : defaultModelForProvider(nextProvider, models, providers);
    setModel(nextModel);
  }, [model, models, providerFilter, providers, settingsConfig, setProviderFilter, setModel]);

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
      setModel(defaultModelForProvider(nextProvider, models, providers));
      return;
    }
    const stillValid = isModelValidForProvider(model, providerFilter, models, providers);
    if (stillValid) return;
    const nextModel = defaultModelForProvider(providerFilter, models, providers);
    setModel(nextModel);
  }, [
    activeChatSession,
    model,
    models,
    providerFilter,
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
    if (queuedChatMessages.length === 0 || chatLoading || chatCancelling) {
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
    setQueuedChatMessages((current) => current.filter((item) => item.id !== next.id));
    void chatActions.submitAgentChat(next);
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
    chatLoading,
    queuedChatMessages,
  ]);

  return null;
}
