// Providers coordinator: provider CRUD operations. These coordinate
// across the settings local state
// (settingsConfig, settingsError, notice) AND the providersAndModels
// + chat slices, so the hook accepts the settings layer's bridges
// as parameters and reads the slice state directly.
//
// deleteProvider is the heaviest path: it optimistically removes
// the provider from settingsConfig + providers list, repaints the
// provider picker / model to a sensible fallback, then rolls back
// on failure if the server delete didn't land. The rollback
// captures the pre-delete record by index so a subsequent
// loadDashboard race can't blow it away.

import { useContext } from "react";

import { applyOverride, CoordinatorOverridesContext } from "./overrides";
import {
  createProvider as createProviderRequest,
  deleteProvider as deleteProviderRequest,
  setProviderAccountID as setProviderAccountIDRequest,
  setProviderAPIKey as setProviderAPIKeyRequest,
  setProviderBaseURL as setProviderBaseURLRequest,
  setProviderCustomName as setProviderCustomNameRequest,
  setProviderName as setProviderNameRequest,
} from "../../../lib/api";
import { defaultModelForProvider, defaultProviderForChat } from "../../runtimeConsoleChatHelpers";
import { useChat } from "../chat";
import { useProvidersAndModels } from "../providersAndModels";
import type { ConfiguredStateResponse } from "../../../types/provider";
import type { SettingsActions } from "./settings";

type SetStateAction<T> = T | ((prev: T) => T);

export type UseProviderActionsParams = {
  settingsConfig: ConfiguredStateResponse["data"] | null;
  setSettingsConfig: (next: SetStateAction<ConfiguredStateResponse["data"] | null>) => void;
  setSettingsError: (value: string) => void;
  loadDashboard: () => Promise<void>;
  refreshProviders: () => Promise<void>;
  setNoticeMessage: SettingsActions["setNoticeMessage"];
  describeError: SettingsActions["describeError"];
  resetSettingsFeedback: SettingsActions["resetSettingsFeedback"];
  runSettingsMutation: SettingsActions["runSettingsMutation"];
};

export function useProviderActions(params: UseProviderActionsParams) {
  const providersAndModels = useProvidersAndModels();
  const chat = useChat();
  const { providers, models, providerPresets } = providersAndModels.state;
  const { setProviders } = providersAndModels.actions;
  const { providerFilter, model } = chat.state;
  const { setProviderFilter, setModel } = chat.actions;

  // setProviderAPIKey is the single operation for managing a provider's API key.
  // An empty `key` clears the existing credential; non-empty sets/replaces it.
  async function setProviderAPIKey(id: string, key: string) {
    await params.runSettingsMutation({
      successMessage: key === "" ? "API key cleared." : "API key saved.",
      errorMessage: key === "" ? "Failed to clear API key." : "Failed to save API key.",
      failureDetail:
        key === "" ? "failed to clear provider api key" : "failed to save provider api key",
      action: async () => {
        await setProviderAPIKeyRequest(id, key);
      },
    });
  }

  async function createProvider(
    createParams: {
      name: string;
      preset_id?: string;
      account_id?: string;
      custom_name?: string;
      base_url?: string;
      api_key?: string;
      kind: string;
      protocol: string;
    },
    options: { refresh?: boolean } = {},
  ): Promise<void> {
    await createProviderRequest(createParams);
    if (options.refresh !== false) {
      await params.loadDashboard();
    }
  }

  async function deleteProvider(id: string): Promise<void> {
    params.resetSettingsFeedback();
    const removedConfiguredProviderIndex =
      params.settingsConfig?.providers.findIndex((provider) => provider.id === id) ?? -1;
    const removedProviderStatusIndex = providers.findIndex((provider) => provider.name === id);
    const removedConfiguredProvider =
      removedConfiguredProviderIndex >= 0
        ? params.settingsConfig?.providers[removedConfiguredProviderIndex]
        : undefined;
    const removedProviderStatus =
      removedProviderStatusIndex >= 0 ? providers[removedProviderStatusIndex] : undefined;
    const previousProviderFilter = providerFilter;
    const previousModel = model;

    params.setSettingsConfig((current) =>
      current
        ? { ...current, providers: current.providers.filter((provider) => provider.id !== id) }
        : current,
    );
    setProviders((current) => current.filter((provider) => provider.name !== id));
    if (providerFilter === id) {
      const remainingProviders = providers.filter((provider) => provider.name !== id);
      const remainingConfigured =
        params.settingsConfig?.providers.filter((provider) => provider.id !== id) ?? [];
      const nextProvider = defaultProviderForChat(models, remainingConfigured, remainingProviders);
      setProviderFilter(nextProvider);
      setModel(
        defaultModelForProvider(
          nextProvider,
          models,
          remainingProviders,
          remainingConfigured,
          providerPresets,
        ),
      );
    }

    try {
      await deleteProviderRequest(id);
      params.setNoticeMessage("success", "Provider removed.");
      void params.loadDashboard();
    } catch (error) {
      params.setSettingsConfig((current) => {
        if (!removedConfiguredProvider) return current;
        if (!current) return current;
        if (current.providers.some((provider) => provider.id === id)) return current;
        return {
          ...current,
          providers: insertAtIndex(
            current.providers,
            removedConfiguredProvider,
            removedConfiguredProviderIndex,
          ),
        };
      });
      setProviders((current) => {
        if (!removedProviderStatus || current.some((provider) => provider.name === id))
          return current;
        return insertAtIndex(current, removedProviderStatus, removedProviderStatusIndex);
      });
      setProviderFilter(previousProviderFilter);
      setModel(previousModel);
      params.setSettingsError(params.describeError(error, "failed to delete provider"));
      params.setNoticeMessage("error", "Failed to remove provider.");
      void params.refreshProviders();
    }
  }

  async function setProviderBaseURL(id: string, baseURL: string): Promise<void> {
    await setProviderBaseURLRequest(id, baseURL);
    // loadDashboard refreshes settingsConfig (the source of truth for base_url
    // shown in the table), then refreshProviders re-runs model discovery
    // against the new endpoint so the model list updates immediately.
    await params.loadDashboard();
    await params.refreshProviders();
  }

  async function setProviderName(id: string, name: string): Promise<void> {
    await setProviderNameRequest(id, name);
    // The label change only affects settingsConfig (table column) — no need
    // to rerun model discovery, so skip refreshProviders.
    await params.loadDashboard();
  }

  async function setProviderCustomName(id: string, customName: string): Promise<void> {
    await setProviderCustomNameRequest(id, customName);
    await params.loadDashboard();
  }

  async function setProviderAccountID(id: string, accountID: string): Promise<void> {
    await setProviderAccountIDRequest(id, accountID);
    await params.loadDashboard();
    await params.refreshProviders();
  }

  const overrides = useContext(CoordinatorOverridesContext);
  return applyOverride(
    {
      setProviderAPIKey,
      createProvider,
      deleteProvider,
      setProviderBaseURL,
      setProviderName,
      setProviderCustomName,
      setProviderAccountID,
    },
    overrides?.providers,
  );
}

function insertAtIndex<T>(items: T[], item: T, index: number): T[] {
  const next = items.slice();
  const boundedIndex = Math.max(0, Math.min(index, next.length));
  next.splice(boundedIndex, 0, item);
  return next;
}
