// providersAndModels slice: provider status list, persisted-config
// provider presets, model catalog, agent adapter list, and the
// adapter-health / approval-mode bits that ride with them.
//
// Two API shapes inside `actions`:
//
//   - Low-level setters (set{Providers,ProviderPresets,Models,
//     AgentAdapters,AgentAdapterApprovalMode}) accept the same
//     `SetStateAction` shape as useState so the dashboard fan-out
//     and shim coordinators (deleteProvider's rollback,
//     credential-update merge, etc.) read identically to the
//     pre-slice code.
//   - Map mutators for the adapter-health map and the per-adapter
//     loading flag avoid the "rebuild the whole map in the
//     caller" boilerplate that every probe path used.
//   - Domain actions (refreshProviders, probeAgentAdapter,
//     setAgentAdapterCredential, deleteAgentAdapterCredential)
//     own the API call + the state update. They return Results
//     so the shim wires success / error to the global notice
//     banner without the slice importing cross-cut state.
//
// What stays in the shim: provider-CRUD coordinators
// (setProviderAPIKey / BaseURL / Name / CustomName, createProvider,
// deleteProvider, the three model-capability-override actions) —
// they all call loadDashboard() and cross several other slices'
// state (settingsConfig, providerFilter, model, settingsError).
// Moving them now would mean dragging those slices in too.

import { createContext, useCallback, useContext, useEffect, useMemo, useReducer, useRef, type ReactNode } from "react";

import { applyOverride, CoordinatorOverridesContext } from "./coordinators/overrides";
import {
  getProviders,
  getModels,
  getProviderPresets,
  probeAgentAdapter as probeAgentAdapterRequest,
  setAgentAdapterCredential as setAgentAdapterCredentialRequest,
  deleteAgentAdapterCredential as deleteAgentAdapterCredentialRequest,
} from "../../lib/api";
import { warn } from "../../lib/log";
import type { AgentAdapterHealthRecord, AgentAdapterRecord } from "../../types/agent-adapter";
import type { ModelResponse } from "../../types/model";
import type { ProviderPresetRecord, ProviderStatusResponse } from "../../types/provider";

export type ProvidersAndModelsState = {
  providers: ProviderStatusResponse["data"];
  providerPresets: ProviderPresetRecord[];
  /**
   * True once getProviderPresets() has resolved. Used by
   * useEnsureProviderPresetsLoaded to dedupe the lazy fetch.
   * Presets aren't loaded at boot; they're fetched on first
   * AddProviderModal mount + TasksView mount.
   */
  providerPresetsLoaded: boolean;
  models: ModelResponse["data"];
  agentAdapters: AgentAdapterRecord[];
  agentAdapterApprovalMode: string;
  agentAdapterHealthByID: Map<string, AgentAdapterHealthRecord>;
  agentAdapterHealthLoadingByID: Map<string, true>;
};

type SetStateAction<T> = T | ((prev: T) => T);

export type ProbeAdapterResult =
  | { ok: true; health: AgentAdapterHealthRecord }
  | { ok: false; error: string };

export type AdapterCredentialResult =
  | { ok: true; isClaudeCode: boolean }
  | { ok: false; error: string };

export type AdapterDeleteCredentialResult =
  | { ok: true }
  | { ok: false; error: string };

export type ProvidersAndModelsActions = {
  setProviders: (next: SetStateAction<ProviderStatusResponse["data"]>) => void;
  setProviderPresets: (next: SetStateAction<ProviderPresetRecord[]>) => void;
  markProviderPresetsLoaded: () => void;
  setModels: (next: SetStateAction<ModelResponse["data"]>) => void;
  setAgentAdapters: (next: SetStateAction<AgentAdapterRecord[]>) => void;
  setAgentAdapterApprovalMode: (value: string) => void;
  setAgentAdapterHealth: (adapterID: string, record: AgentAdapterHealthRecord) => void;
  clearAgentAdapterHealth: (adapterID: string) => void;
  setAgentAdapterHealthLoading: (adapterID: string, loading: boolean) => void;
  refreshProviders: () => Promise<void>;
  probeAgentAdapter: (adapterID: string) => Promise<ProbeAdapterResult>;
  setAgentAdapterCredential: (
    adapterID: string,
    value: string,
    name?: string,
  ) => Promise<AdapterCredentialResult>;
  deleteAgentAdapterCredential: (
    adapterID: string,
    name: string,
  ) => Promise<AdapterDeleteCredentialResult>;
};

type ProvidersAndModelsContextValue = {
  state: ProvidersAndModelsState;
  actions: ProvidersAndModelsActions;
};

type Action =
  | { type: "providers/set"; next: SetStateAction<ProviderStatusResponse["data"]> }
  | { type: "providerPresets/set"; next: SetStateAction<ProviderPresetRecord[]> }
  | { type: "providerPresetsLoaded/mark" }
  | { type: "models/set"; next: SetStateAction<ModelResponse["data"]> }
  | { type: "agentAdapters/set"; next: SetStateAction<AgentAdapterRecord[]> }
  | { type: "agentAdapterApprovalMode/set"; value: string }
  | { type: "agentAdapterHealth/set"; adapterID: string; record: AgentAdapterHealthRecord }
  | { type: "agentAdapterHealth/clear"; adapterID: string }
  | { type: "agentAdapterHealthLoading/set"; adapterID: string; loading: boolean };

const initialState: ProvidersAndModelsState = {
  providers: [],
  providerPresets: [],
  providerPresetsLoaded: false,
  models: [],
  agentAdapters: [],
  agentAdapterApprovalMode: "",
  agentAdapterHealthByID: new Map(),
  agentAdapterHealthLoadingByID: new Map(),
};

function resolve<T>(prev: T, next: SetStateAction<T>): T {
  return typeof next === "function" ? (next as (prev: T) => T)(prev) : next;
}

function reducer(state: ProvidersAndModelsState, action: Action): ProvidersAndModelsState {
  switch (action.type) {
    case "providers/set":
      return { ...state, providers: resolve(state.providers, action.next) };
    case "providerPresets/set":
      return { ...state, providerPresets: resolve(state.providerPresets, action.next) };
    case "providerPresetsLoaded/mark":
      return state.providerPresetsLoaded ? state : { ...state, providerPresetsLoaded: true };
    case "models/set":
      return { ...state, models: resolve(state.models, action.next) };
    case "agentAdapters/set":
      return { ...state, agentAdapters: resolve(state.agentAdapters, action.next) };
    case "agentAdapterApprovalMode/set":
      return { ...state, agentAdapterApprovalMode: action.value };
    case "agentAdapterHealth/set": {
      const next = new Map(state.agentAdapterHealthByID);
      next.set(action.adapterID, action.record);
      return { ...state, agentAdapterHealthByID: next };
    }
    case "agentAdapterHealth/clear": {
      if (!state.agentAdapterHealthByID.has(action.adapterID)) return state;
      const next = new Map(state.agentAdapterHealthByID);
      next.delete(action.adapterID);
      return { ...state, agentAdapterHealthByID: next };
    }
    case "agentAdapterHealthLoading/set": {
      const map = state.agentAdapterHealthLoadingByID;
      if (action.loading) {
        const next = new Map(map);
        next.set(action.adapterID, true);
        return { ...state, agentAdapterHealthLoadingByID: next };
      }
      if (!map.has(action.adapterID)) return state;
      const next = new Map(map);
      next.delete(action.adapterID);
      return { ...state, agentAdapterHealthLoadingByID: next };
    }
  }
}

const ProvidersAndModelsContext = createContext<ProvidersAndModelsContextValue | null>(null);

export function ProvidersAndModelsProvider({ children, initialState: seededState }: {
  children: ReactNode;
  initialState?: Partial<ProvidersAndModelsState>;
}) {
  const [state, dispatch] = useReducer(
    reducer,
    seededState ? { ...initialState, ...seededState } : initialState,
  );

  const setProviders = useCallback(
    (next: SetStateAction<ProviderStatusResponse["data"]>) => dispatch({ type: "providers/set", next }),
    [],
  );
  const setProviderPresets = useCallback(
    (next: SetStateAction<ProviderPresetRecord[]>) => dispatch({ type: "providerPresets/set", next }),
    [],
  );
  const markProviderPresetsLoaded = useCallback(
    () => dispatch({ type: "providerPresetsLoaded/mark" }),
    [],
  );
  const setModels = useCallback(
    (next: SetStateAction<ModelResponse["data"]>) => dispatch({ type: "models/set", next }),
    [],
  );
  const setAgentAdapters = useCallback(
    (next: SetStateAction<AgentAdapterRecord[]>) => dispatch({ type: "agentAdapters/set", next }),
    [],
  );
  const setAgentAdapterApprovalMode = useCallback(
    (value: string) => dispatch({ type: "agentAdapterApprovalMode/set", value }),
    [],
  );
  const setAgentAdapterHealth = useCallback(
    (adapterID: string, record: AgentAdapterHealthRecord) =>
      dispatch({ type: "agentAdapterHealth/set", adapterID, record }),
    [],
  );
  const clearAgentAdapterHealth = useCallback(
    (adapterID: string) => dispatch({ type: "agentAdapterHealth/clear", adapterID }),
    [],
  );
  const setAgentAdapterHealthLoading = useCallback(
    (adapterID: string, loading: boolean) =>
      dispatch({ type: "agentAdapterHealthLoading/set", adapterID, loading }),
    [],
  );

  const refreshProviders = useCallback(async () => {
    try {
      const [pResult, mResult] = await Promise.allSettled([getProviders(), getModels()]);
      if (pResult.status === "fulfilled") {
        dispatch({ type: "providers/set", next: pResult.value.data ?? [] });
      }
      if (mResult.status === "fulfilled") {
        dispatch({ type: "models/set", next: mResult.value.data ?? [] });
      }
    } catch {
      // Best-effort background refresh — ignore errors.
    }
  }, []);

  const probeAgentAdapter = useCallback(async (adapterID: string): Promise<ProbeAdapterResult> => {
    if (!adapterID) return { ok: false, error: "Adapter id required to probe." };
    dispatch({ type: "agentAdapterHealthLoading/set", adapterID, loading: true });
    try {
      const payload = await probeAgentAdapterRequest(adapterID);
      dispatch({ type: "agentAdapterHealth/set", adapterID, record: payload.data.health });
      dispatch({
        type: "agentAdapters/set",
        next: (current) => current.map((item) => (item.id === adapterID ? payload.data.adapter : item)),
      });
      return { ok: true, health: payload.data.health };
    } catch (error) {
      return { ok: false, error: error instanceof Error ? error.message : "Failed to probe adapter." };
    } finally {
      dispatch({ type: "agentAdapterHealthLoading/set", adapterID, loading: false });
    }
  }, []);

  const setAgentAdapterCredential = useCallback(async (
    adapterID: string,
    value: string,
    name?: string,
  ): Promise<AdapterCredentialResult> => {
    try {
      const payload = await setAgentAdapterCredentialRequest(adapterID, value, name);
      dispatch({
        type: "agentAdapters/set",
        next: (current) => current.map((item) => (item.id === adapterID
          ? { ...item, credential_configured: payload.data.configured, credential_preview: payload.data.preview }
          : item)),
      });
      const isClaudeCode = adapterID === "claude_code";
      if (isClaudeCode && payload.data.configured) {
        // Claude Code's credential check IS the readiness probe — the
        // adapter validates the token at credential-set time, so a
        // success here doubles as a healthy probe result and surfaces
        // the green chip without a separate user action.
        dispatch({
          type: "agentAdapterHealth/set",
          adapterID,
          record: { adapter_id: adapterID, status: "ready", stage: "ready", duration_ms: 0 },
        });
      }
      return { ok: true, isClaudeCode };
    } catch (error) {
      const fallback = adapterID === "claude_code"
        ? "Failed to validate adapter credential."
        : "Failed to save adapter credential.";
      return { ok: false, error: error instanceof Error ? error.message : fallback };
    }
  }, []);

  const deleteAgentAdapterCredential = useCallback(async (
    adapterID: string,
    name: string,
  ): Promise<AdapterDeleteCredentialResult> => {
    try {
      await deleteAgentAdapterCredentialRequest(adapterID, name);
      dispatch({
        type: "agentAdapters/set",
        next: (current) => current.map((item) => (item.id === adapterID
          ? { ...item, credential_configured: false, credential_preview: undefined }
          : item)),
      });
      dispatch({ type: "agentAdapterHealth/clear", adapterID });
      return { ok: true };
    } catch (error) {
      return { ok: false, error: error instanceof Error ? error.message : "Failed to remove adapter credential." };
    }
  }, []);

  const actions = useMemo<ProvidersAndModelsActions>(() => ({
    setProviders,
    setProviderPresets,
    markProviderPresetsLoaded,
    setModels,
    setAgentAdapters,
    setAgentAdapterApprovalMode,
    setAgentAdapterHealth,
    clearAgentAdapterHealth,
    setAgentAdapterHealthLoading,
    refreshProviders,
    probeAgentAdapter,
    setAgentAdapterCredential,
    deleteAgentAdapterCredential,
  }), [
    setProviders,
    setProviderPresets,
    markProviderPresetsLoaded,
    setModels,
    setAgentAdapters,
    setAgentAdapterApprovalMode,
    setAgentAdapterHealth,
    clearAgentAdapterHealth,
    setAgentAdapterHealthLoading,
    refreshProviders,
    probeAgentAdapter,
    setAgentAdapterCredential,
    deleteAgentAdapterCredential,
  ]);

  const value = useMemo(() => ({ state, actions }), [state, actions]);

  return <ProvidersAndModelsContext.Provider value={value}>{children}</ProvidersAndModelsContext.Provider>;
}

export function useProvidersAndModels(): ProvidersAndModelsContextValue {
  const ctx = useContext(ProvidersAndModelsContext);
  if (!ctx) {
    throw new Error("useProvidersAndModels must be used inside a <ProvidersAndModelsProvider>");
  }
  const overrides = useContext(CoordinatorOverridesContext);
  return { state: ctx.state, actions: applyOverride(ctx.actions, overrides?.providersAndModelsSlice) };
}

// useEnsureProviderPresetsLoaded fetches the provider preset
// catalog on first call if the slice's providerPresetsLoaded flag
// is false. Used by AddProviderModal and TasksView; the dashboard
// loader no longer pulls presets at boot. Dedupes parallel callers
// via an inflight ref; tolerates failures by leaving the loaded
// flag false so a later mount can retry.
//
// The optional `when` gate lets callers that always-mount the
// component (e.g. <AddProviderModal open={…} /> in ChatView) skip
// the fetch until the modal actually opens — otherwise the
// always-mounted modal would defeat the lazy-fetch contract.
export function useEnsureProviderPresetsLoaded(when: boolean = true): void {
  const { state, actions } = useProvidersAndModels();
  const inFlight = useRef(false);

  useEffect(() => {
    if (!when || state.providerPresetsLoaded || inFlight.current) return;
    inFlight.current = true;
    void (async () => {
      try {
        const res = await getProviderPresets();
        actions.setProviderPresets(res.data ?? []);
        actions.markProviderPresetsLoaded();
      } catch (err) {
        warn("providerPresets.ensureLoaded.failed", { err: err instanceof Error ? err.message : String(err) });
      } finally {
        inFlight.current = false;
      }
    })();
  }, [when, state.providerPresetsLoaded, actions]);
}
