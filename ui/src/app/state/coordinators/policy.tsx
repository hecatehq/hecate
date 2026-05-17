// Policy coordinator: upsert + delete operations on policy rules.
// Both call runSettingsMutation (success populates toast, failure
// also populates the inline settings banner) for the same
// can't-miss feedback contract the tenant + API-key flows use.

import {
  type PolicyRuleUpsertPayload,
  deletePolicyRule as deletePolicyRuleRequest,
  upsertPolicyRule as upsertPolicyRuleRequest,
} from "../../../lib/api";
import type { SettingsActions } from "./settings";

export type UsePolicyActionsParams = {
  runSettingsMutation: SettingsActions["runSettingsMutation"];
};

export function usePolicyActions(params: UsePolicyActionsParams) {
  async function upsertPolicyRule(payload: PolicyRuleUpsertPayload) {
    await params.runSettingsMutation({
      successMessage: "Policy rule saved.",
      errorMessage: "Failed to save policy rule.",
      failureDetail: "failed to save policy rule",
      action: async () => {
        await upsertPolicyRuleRequest(payload);
      },
    });
  }

  async function deletePolicyRule(id: string) {
    await params.runSettingsMutation({
      successMessage: "Policy rule deleted.",
      errorMessage: "Failed to delete policy rule.",
      failureDetail: "failed to delete policy rule",
      action: async () => {
        await deletePolicyRuleRequest(id);
      },
    });
  }

  return { upsertPolicyRule, deletePolicyRule };
}
