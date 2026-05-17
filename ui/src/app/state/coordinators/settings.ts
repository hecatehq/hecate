// Settings coordinator: tiny bundle of cross-cutting settings
// helpers. Owns the notice banner + settings error wiring used
// by the provider / policy / chat / adapter coordinators.
//
// runSettingsMutation needs loadDashboard, which lives in the
// dashboard coordinator. To break the dashboard → chat → settings
// → dashboard cycle, the caller passes loadDashboard as a thunk;
// useRuntimeConsole resolves it through a ref so we don't depend
// on creation order between hooks.

import type { NoticeState } from "../settings";

export type UseSettingsActionsParams = {
  setSettingsError: (value: string) => void;
  setNotice: (next: NoticeState | null) => void;
  loadDashboard: () => Promise<void>;
};

export type SettingsActions = {
  setNoticeMessage: (kind: NoticeState["kind"], message: string) => void;
  describeError: (error: unknown, fallback: string) => string;
  resetSettingsFeedback: () => void;
  runSettingsMutation: (options: {
    action: () => Promise<void>;
    successMessage: string;
    errorMessage: string;
    failureDetail: string;
  }) => Promise<void>;
};

export function useSettingsActions(params: UseSettingsActionsParams): SettingsActions {
  function setNoticeMessage(kind: NoticeState["kind"], message: string) {
    if (message) params.setNotice({ kind, message });
  }

  function describeError(error: unknown, fallback: string): string {
    return error instanceof Error ? error.message : fallback;
  }

  function resetSettingsFeedback() {
    params.setSettingsError("");
    params.setNotice(null);
  }

  async function runSettingsMutation(options: {
    action: () => Promise<void>;
    successMessage: string;
    errorMessage: string;
    failureDetail: string;
  }) {
    resetSettingsFeedback();
    try {
      await options.action();
      await params.loadDashboard();
      setNoticeMessage("success", options.successMessage);
    } catch (error) {
      params.setSettingsError(describeError(error, options.failureDetail));
      setNoticeMessage("error", options.errorMessage);
    }
  }

  return { setNoticeMessage, describeError, resetSettingsFeedback, runSettingsMutation };
}
