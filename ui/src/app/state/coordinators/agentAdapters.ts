// Agent-adapter coordinator: bounded session checks for external agent adapters.

import { useContext } from "react";

import { applyOverride, CoordinatorOverridesContext } from "./overrides";
import { useProvidersAndModels } from "../providersAndModels";
import {
  authenticateAgentAdapter as authenticateAgentAdapterRequest,
  logoutAgentAdapter as logoutAgentAdapterRequest,
} from "../../../lib/api";
import type { AgentAdapterHealthRecord } from "../../../types/agent-adapter";
import type { SettingsActions } from "./settings";

export type UseAgentAdapterActionsParams = {
  setNoticeMessage: SettingsActions["setNoticeMessage"];
};

export type AgentAdapterCheckOptions = {
  notify?: boolean;
  refreshCatalog?: boolean;
};

export type AgentAdapterCheckResult =
  | { ok: true; health: AgentAdapterHealthRecord | null }
  | { ok: false; error: string };

type AgentAdapterRefreshOptions = {
  notify?: boolean;
};

export function useAgentAdapterActions(params: UseAgentAdapterActionsParams) {
  const providersAndModels = useProvidersAndModels();

  async function refreshAgentAdapters(options: AgentAdapterRefreshOptions = {}): Promise<boolean> {
    const result = await providersAndModels.actions.refreshAgentAdapters();
    if (!result.ok) {
      if (options.notify !== false) params.setNoticeMessage("error", result.error);
      return false;
    }
    return true;
  }

  // probeAgentAdapter opens a short-lived ACP session and caches the typed
  // result by adapter id. Connections runs it automatically without a global
  // error notice; an operator retry keeps the same session check explicit.
  // It annotates status but never gates a later chat.
  async function probeAgentAdapter(
    adapterID: string,
    options: AgentAdapterCheckOptions = {},
  ): Promise<AgentAdapterCheckResult> {
    const result = await providersAndModels.actions.probeAgentAdapter(adapterID, {
      refreshCatalog: options.refreshCatalog,
    });
    if (!result.ok) {
      if (options.notify !== false) params.setNoticeMessage("error", result.error);
    }
    return result;
  }

  async function logoutAgentAdapter(adapterID: string): Promise<boolean> {
    if (!adapterID) {
      params.setNoticeMessage("error", "Adapter id required to sign out.");
      return false;
    }
    try {
      await logoutAgentAdapterRequest(adapterID);
      providersAndModels.actions.applyAgentAdapterAuthResult(adapterID, "unauthenticated");
      params.setNoticeMessage("success", "External agent signed out.");
      return true;
    } catch (error) {
      params.setNoticeMessage(
        "error",
        error instanceof Error ? error.message : "Failed to sign out external agent.",
      );
      return false;
    }
  }

  async function authenticateAgentAdapter(adapterID: string): Promise<boolean> {
    if (!adapterID) {
      params.setNoticeMessage("error", "Adapter id required to sign in.");
      return false;
    }
    try {
      await authenticateAgentAdapterRequest(adapterID);
      providersAndModels.actions.applyAgentAdapterAuthResult(adapterID, "ok");
      params.setNoticeMessage("success", "External agent sign-in completed.");
      return true;
    } catch (error) {
      params.setNoticeMessage(
        "error",
        error instanceof Error ? error.message : "Failed to sign in external agent.",
      );
      return false;
    }
  }

  const overrides = useContext(CoordinatorOverridesContext);
  return applyOverride(
    { refreshAgentAdapters, probeAgentAdapter, authenticateAgentAdapter, logoutAgentAdapter },
    overrides?.agentAdapters,
  );
}
