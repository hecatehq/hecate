// Dashboard coordinator: bulk loaders that fan a resolved
// dashboard snapshot across the slices. loadDashboard is invoked
// at mount + by the settings / provider mutations (refresh after
// write); refreshProviders is the polling refresh used by the
// providers tab; refreshRuntimeState (alias to chat's helper) is
// re-exposed for symmetry.
//
// The hook composes chat for applyChatSession +
// syncHecateSelectionFromSession (snapshot commit reuses chat's
// state-merge logic) so dashboard and chat agree on session shape
// without duplicating the renderChatSessionSummary chain.

import { useContext } from "react";

import { applyOverride, CoordinatorOverridesContext } from "./overrides";
import { resolveDashboardSnapshot } from "../../runtimeConsoleDashboard";
import { useChat } from "../chat";
import { useProvidersAndModels } from "../providersAndModels";
import { useRuntime } from "../runtime";
import { useUsage } from "../usage";
import type { ChatSessionRecord } from "../../../types/chat";
import type { ConfiguredStateResponse } from "../../../types/provider";
import type { ChatActions } from "./chat";

type SetStateAction<T> = T | ((prev: T) => T);

export type UseDashboardActionsParams = {
  settingsConfig: ConfiguredStateResponse["data"] | null;
  setSettingsConfig: (next: SetStateAction<ConfiguredStateResponse["data"] | null>) => void;
  setSettingsError: (value: string) => void;
  applyChatSession: ChatActions["applyChatSession"];
  syncHecateSelectionFromSession: ChatActions["syncHecateSelectionFromSession"];
  refreshRuntimeState: ChatActions["refreshRuntimeState"];
};

export function useDashboardActions(params: UseDashboardActionsParams) {
  const runtime = useRuntime();
  const usage = useUsage();
  const providersAndModels = useProvidersAndModels();
  const chat = useChat();

  const {
    setHealth,
    setSessionInfo,
    setLoading,
    setError,
    setHecateRTKAvailable,
    setHecateRTKPath,
  } = runtime.actions;
  const { setSummary: setUsageSummary, setEvents: setUsageEvents } = usage.actions;
  const { providers, agentAdapters } = providersAndModels.state;
  const {
    setProviders,
    setProviderPresets,
    setModels,
    setAgentAdapters,
    setAgentAdapterApprovalMode,
  } = providersAndModels.actions;
  const { activeChatSessionID, activeChatSession, chatSessions } = chat.state;
  const {
    setChatSessions,
    setActiveChatSessionID,
    setActiveChatSession,
    pruneQueuedChatMessagesForSessions,
  } = chat.actions;
  const usageSummary = usage.state.summary;
  const usageEvents = usage.state.events;

  async function loadDashboard() {
    setLoading(true);
    setError("");
    params.setSettingsError("");

    try {
      const snapshot = await resolveDashboardSnapshot({
        activeChatSessionID,
        previous: {
          providers,
          agentAdapters,
          usageSummary,
          chatSessions,
          activeChatSession,
          usageEvents,
          settingsConfig: params.settingsConfig,
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
          params.setSettingsConfig(essentials.settingsConfig);
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
      pruneQueuedChatMessagesForSessions(snapshot.chatSessions.map((session: ChatSessionRecord) => session.id));
      setActiveChatSessionID(snapshot.activeChatSessionID);
      setActiveChatSession(snapshot.activeChatSession);
      params.syncHecateSelectionFromSession(snapshot.activeChatSession);
      setUsageEvents(snapshot.usageEvents);
      params.setSettingsConfig(snapshot.settingsConfig);
      setAgentAdapterApprovalMode(snapshot.agentAdapterApprovalMode);
      setHecateRTKAvailable(snapshot.rtkAvailable);
      setHecateRTKPath(snapshot.rtkPath);
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "unknown load error");
    } finally {
      setLoading(false);
    }
  }

  // refreshProviders re-fetches /hecate/v1/providers/status (runtime health) and
  // /v1/models (model catalog) for the ProvidersView auto-poll so local
  // provider model lists converge within ~30 s of starting Ollama / LM
  // Studio. Skipped when no providers are configured — the providers
  // tab renders its empty state, there's nothing to converge.
  async function refreshProviders() {
    // Gate the fetch on settingsConfig so a zero-provider boot
    // doesn't trip a 200-with-empty-list round trip on every chat-
    // workspace mount. The slice's refreshProviders unconditionally
    // fetches once called.
    if ((params.settingsConfig?.providers?.length ?? 0) === 0) return;
    await providersAndModels.actions.refreshProviders();
  }

  const overrides = useContext(CoordinatorOverridesContext);
  return applyOverride(
    {
      loadDashboard,
      refreshProviders,
      refreshRuntimeState: params.refreshRuntimeState,
    },
    overrides?.dashboard,
  );
}
