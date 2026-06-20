// Agent-adapter coordinator: readiness probes for external agent adapters.

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

export function useAgentAdapterActions(params: UseAgentAdapterActionsParams) {
  const providersAndModels = useProvidersAndModels();

  // probeAgentAdapter exercises the configured adapter and caches the
  // typed result keyed by adapter id. Operators trigger this via the
  // readiness probe in Connections; the result drives
  // the status chip + the picker dropdown's inline diagnostic. The
  // loading map is keyed by id so two adapters can be probing
  // concurrently without confusing the UI.
  async function probeAgentAdapter(adapterID: string): Promise<AgentAdapterHealthRecord | null> {
    const result = await providersAndModels.actions.probeAgentAdapter(adapterID);
    if (!result.ok) {
      params.setNoticeMessage("error", result.error);
      return null;
    }
    return result.health;
  }

  async function logoutAgentAdapter(adapterID: string): Promise<boolean> {
    if (!adapterID) {
      params.setNoticeMessage("error", "Adapter id required to sign out.");
      return false;
    }
    try {
      await logoutAgentAdapterRequest(adapterID);
      providersAndModels.actions.clearAgentAdapterHealth(adapterID);
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
      providersAndModels.actions.clearAgentAdapterHealth(adapterID);
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
    { probeAgentAdapter, authenticateAgentAdapter, logoutAgentAdapter },
    overrides?.agentAdapters,
  );
}
