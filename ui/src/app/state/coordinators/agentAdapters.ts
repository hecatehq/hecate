// Agent-adapter coordinator: bounded session checks for external agent adapters.

import { useContext } from "react";

import { applyOverride, CoordinatorOverridesContext } from "./overrides";
import { useProvidersAndModels } from "../providersAndModels";
import {
  approveAgentAdapterExecutable as approveAgentAdapterExecutableRequest,
  authenticateAgentAdapter as authenticateAgentAdapterRequest,
  logoutAgentAdapter as logoutAgentAdapterRequest,
  revokeAgentAdapterExecutable as revokeAgentAdapterExecutableRequest,
} from "../../../lib/api";
import type {
  AgentAdapterExecutableTrust,
  AgentAdapterHealthRecord,
} from "../../../types/agent-adapter";
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

  // probeAgentAdapter opens a short-lived ACP session only after an explicit
  // operator action. It annotates status but never grants executable trust.
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

  async function approveAgentAdapterExecutable(
    adapterID: string,
    expectedIdentity: string,
  ): Promise<boolean> {
    if (!adapterID || !expectedIdentity) {
      params.setNoticeMessage("error", "Review the current app identity before approving it.");
      return false;
    }
    try {
      const response = await approveAgentAdapterExecutableRequest(adapterID, expectedIdentity);
      providersAndModels.actions.applyAgentAdapterExecutableTrust(adapterID, response.data);
      await refreshAgentAdapters({ notify: false });
      params.setNoticeMessage("success", "External agent app approved.");
      return true;
    } catch (error) {
      // The reviewed token can become stale between catalog read and approval.
      // Refresh passively so the operator sees the newly measured identity
      // instead of repeatedly submitting the obsolete one.
      await refreshAgentAdapters({ notify: false });
      params.setNoticeMessage(
        "error",
        error instanceof Error ? error.message : "Failed to approve external agent app.",
      );
      return false;
    }
  }

  async function revokeAgentAdapterExecutable(adapterID: string): Promise<boolean> {
    if (!adapterID) {
      params.setNoticeMessage("error", "Adapter id required to revoke app approval.");
      return false;
    }
    try {
      await revokeAgentAdapterExecutableRequest(adapterID);
      const adapter = providersAndModels.state.agentAdapters.find((item) => item.id === adapterID);
      const current = adapter?.executable_trust?.current;
      const revoked: AgentAdapterExecutableTrust = {
        schema_version:
          adapter?.executable_trust?.schema_version ?? "hecate.external-agent-executable.v1",
        state: current ? "unapproved" : "unavailable",
        reason: current ? "approval_required" : "executable_not_found",
        current,
      };
      providersAndModels.actions.applyAgentAdapterExecutableTrust(adapterID, revoked);
      await refreshAgentAdapters({ notify: false });
      params.setNoticeMessage("success", "External agent app approval revoked.");
      return true;
    } catch (error) {
      params.setNoticeMessage(
        "error",
        error instanceof Error ? error.message : "Failed to revoke external agent app approval.",
      );
      return false;
    }
  }

  const overrides = useContext(CoordinatorOverridesContext);
  return applyOverride(
    {
      refreshAgentAdapters,
      probeAgentAdapter,
      authenticateAgentAdapter,
      logoutAgentAdapter,
      approveAgentAdapterExecutable,
      revokeAgentAdapterExecutable,
    },
    overrides?.agentAdapters,
  );
}
