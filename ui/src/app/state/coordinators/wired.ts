// Wired coordinator hooks. The base coordinators
// (`useChatActions`, `useDashboardActions`, `useProviderActions`,
// `useSettingsActions`, etc.) accept their cross-slice dependencies
// as plumbing params — the test-only composer (and the production
// App composition that lived there) was responsible for wiring the
// graph together. Now that views consume coordinators directly,
// each call site would otherwise have to re-resolve the same param
// bag.
//
// These wrappers resolve the graph once and expose plain hook
// signatures the views can call without any params. They live in
// the coordinators directory because they own the dependency-graph
// shape, not the views.
//
// Forward-dependency note (the same one the retired facade carried):
// the dashboard coordinator's loadDashboard depends on the chat
// coordinator's applyChatSession + syncHecateSelectionFromSession +
// refreshRuntimeState; the chat coordinator depends on the
// settings coordinator's setNoticeMessage; the settings coordinator
// needs loadDashboard for runSettingsMutation. We resolve the chat
// coordinator first (it only needs setNoticeMessage, which we read
// off the settings slice's setNotice directly through
// useSettingsActions; the loadDashboard cycle is resolved through
// a lazy ref the way the shim did it).

import { useMemo, useRef } from "react";

import { useSettings } from "../settings";
import { useChatTarget } from "../derived";
import { useChatActions } from "./chat";
import { useDashboardActions } from "./dashboard";
import { useProviderActions } from "./providers";
import { usePolicyActions } from "./policy";
import { useSettingsActions } from "./settings";
import type { ConfiguredStateResponse } from "../../../types/provider";

// useWiredSettingsActions resolves the loadDashboard reference
// lazily so the chat → settings → dashboard cycle stays unbroken.
export function useWiredSettingsActions() {
  const settings = useSettings();
  const loadDashboardRef = useRef<() => Promise<void>>(() => Promise.resolve());
  const loadDashboardLazy = useMemo(
    () => async () => {
      await loadDashboardRef.current();
    },
    [],
  );
  const actions = useSettingsActions({
    setSettingsError: settings.actions.setError,
    setNotice: settings.actions.setNotice,
    loadDashboard: loadDashboardLazy,
  });
  return { actions, loadDashboardRef };
}

// useWiredChatActions consumes chatTarget + setNoticeMessage to mirror
// the shim's wiring. Re-exported separately because the dashboard
// coordinator needs the returned helpers passed in.
export function useWiredChatActions() {
  const chatTarget = useChatTarget();
  const { actions: settingsActions } = useWiredSettingsActions();
  return useChatActions({ chatTarget, setNoticeMessage: settingsActions.setNoticeMessage });
}

// useWiredDashboardActions builds the dashboard coordinator with
// chat coordinator helpers and the settings setSettingsConfig
// wrapper that mirrors the React useState polymorphic signature
// (the shim's setSettingsConfig).
type SettingsConfigSetter = (
  next:
    | ConfiguredStateResponse["data"]
    | null
    | ((current: ConfiguredStateResponse["data"] | null) => ConfiguredStateResponse["data"] | null),
) => void;

function useSettingsConfigSetter(): SettingsConfigSetter {
  const settings = useSettings();
  return useMemo(
    () => (next) => {
      if (typeof next === "function") {
        settings.actions.updateConfig(next as (current: ConfiguredStateResponse["data"] | null) => ConfiguredStateResponse["data"] | null);
      } else {
        settings.actions.setConfig(next);
      }
    },
    [settings.actions],
  );
}

export function useWiredDashboardActions() {
  const settings = useSettings();
  const setSettingsConfig = useSettingsConfigSetter();
  const { actions: settingsActions, loadDashboardRef } = useWiredSettingsActions();
  const chatTarget = useChatTarget();
  const chatActions = useChatActions({ chatTarget, setNoticeMessage: settingsActions.setNoticeMessage });
  const dashboardActions = useDashboardActions({
    settingsConfig: settings.state.config,
    setSettingsConfig,
    setSettingsError: settings.actions.setError,
    applyChatSession: chatActions.applyChatSession,
    syncHecateSelectionFromSession: chatActions.syncHecateSelectionFromSession,
    refreshRuntimeState: chatActions.refreshRuntimeState,
  });
  loadDashboardRef.current = dashboardActions.loadDashboard;
  return dashboardActions;
}

export function useWiredProviderActions() {
  const settings = useSettings();
  const setSettingsConfig = useSettingsConfigSetter();
  const { actions: settingsActions, loadDashboardRef } = useWiredSettingsActions();
  const chatTarget = useChatTarget();
  const chatActions = useChatActions({ chatTarget, setNoticeMessage: settingsActions.setNoticeMessage });
  const dashboardActions = useDashboardActions({
    settingsConfig: settings.state.config,
    setSettingsConfig,
    setSettingsError: settings.actions.setError,
    applyChatSession: chatActions.applyChatSession,
    syncHecateSelectionFromSession: chatActions.syncHecateSelectionFromSession,
    refreshRuntimeState: chatActions.refreshRuntimeState,
  });
  loadDashboardRef.current = dashboardActions.loadDashboard;
  return useProviderActions({
    settingsConfig: settings.state.config,
    setSettingsConfig,
    setSettingsError: settings.actions.setError,
    loadDashboard: dashboardActions.loadDashboard,
    refreshProviders: dashboardActions.refreshProviders,
    setNoticeMessage: settingsActions.setNoticeMessage,
    describeError: settingsActions.describeError,
    resetSettingsFeedback: settingsActions.resetSettingsFeedback,
    runSettingsMutation: settingsActions.runSettingsMutation,
  });
}

export function useWiredPolicyActions() {
  const { actions: settingsActions } = useWiredSettingsActions();
  return usePolicyActions({ runSettingsMutation: settingsActions.runSettingsMutation });
}

// useChat is just the slice — kept here as a re-export so views
// import from one barrel.
export { useChat } from "../chat";
