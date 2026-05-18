// Agent-adapter coordinator: probe + credential operations on
// external agent adapters. The providersAndModels slice owns the
// underlying state machine and returns Results; this coordinator
// unwraps Result → boolean / record and routes errors through the
// global notice banner.

import { useContext } from "react";

import { applyOverride, CoordinatorOverridesContext } from "./overrides";
import { useProvidersAndModels } from "../providersAndModels";
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

  async function setAgentAdapterCredential(
    adapterID: string,
    value: string,
    name?: string,
  ): Promise<boolean> {
    const result = await providersAndModels.actions.setAgentAdapterCredential(
      adapterID,
      value,
      name,
    );
    if (!result.ok) {
      params.setNoticeMessage("error", result.error);
      return false;
    }
    params.setNoticeMessage(
      "success",
      result.isClaudeCode ? "Claude Code verified." : "Adapter credential saved.",
    );
    return true;
  }

  async function deleteAgentAdapterCredential(adapterID: string, name: string): Promise<boolean> {
    const result = await providersAndModels.actions.deleteAgentAdapterCredential(adapterID, name);
    if (!result.ok) {
      params.setNoticeMessage("error", result.error);
      return false;
    }
    params.setNoticeMessage("success", "Adapter credential removed.");
    return true;
  }

  const overrides = useContext(CoordinatorOverridesContext);
  return applyOverride(
    { probeAgentAdapter, setAgentAdapterCredential, deleteAgentAdapterCredential },
    overrides?.agentAdapters,
  );
}
