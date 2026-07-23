// providersAndModels slice: provider status list, persisted-config
// provider presets, model catalog, agent adapter list, and the
// adapter-health / approval-mode bits that ride with them.
//
// Two API shapes inside `actions`:
//
//   - Low-level setters (set{Providers,ProviderPresets,Models,
//     AgentAdapters,AgentAdapterApprovalMode}) accept the same
//     `SetStateAction` shape as useState so the dashboard fan-out
//     and shim coordinators read identically to the pre-slice code.
//   - Map mutators for the adapter-health map and the per-adapter
//     loading flag avoid the "rebuild the whole map in the
//     caller" boilerplate that every probe path used.
//   - Domain actions (refreshProviders, probeAgentAdapter,
//     verifyModelToolSupport) own the
//     API call + the state update. They return Results so the shim
//     wires success / error to the global notice banner without the
//     slice importing cross-cut state.
//
// What stays in the shim: provider-CRUD coordinators
// (setProviderAPIKey / BaseURL / Name / CustomName, createProvider,
// deleteProvider, the three model-capability-override actions) —
// they all call loadDashboard() and cross several other slices'
// state (settingsConfig, providerFilter, model, settingsError).
// Moving them now would mean dragging those slices in too.

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useReducer,
  useRef,
  type ReactNode,
} from "react";

import { applyOverride, CoordinatorOverridesContext } from "./coordinators/overrides";
import {
  getAgentAdapters,
  getProviders,
  getModels,
  getProviderPresets,
  probeAgentAdapter as probeAgentAdapterRequest,
  verifyModelToolSupport as verifyModelToolSupportRequest,
} from "../../lib/api";
import { warn } from "../../lib/log";
import type {
  AgentAdapterHealthRecord,
  AgentAdapterRecord,
  AgentAdapterResponse,
} from "../../types/agent-adapter";
import type { ModelResponse, ModelToolCapabilityProbeResponse } from "../../types/model";
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
  modelToolSupportLoadingByKey: Map<string, true>;
};

type SetStateAction<T> = T | ((prev: T) => T);

export type ProbeAdapterResult =
  | { ok: true; health: AgentAdapterHealthRecord }
  | { ok: false; error: string };

export type RefreshAgentAdaptersResult =
  | { ok: true; adapters: AgentAdapterRecord[] }
  | { ok: false; error: string };

export type VerifyModelToolSupportResult =
  | { ok: true; probe: ModelToolCapabilityProbeResponse }
  | { ok: false; error: string };

export function modelToolSupportKey(provider: string, model: string): string {
  return `${provider.trim().toLowerCase()}\0${model.trim()}`;
}

export type ProvidersAndModelsActions = {
  setProviders: (next: SetStateAction<ProviderStatusResponse["data"]>) => void;
  setProviderPresets: (next: SetStateAction<ProviderPresetRecord[]>) => void;
  markProviderPresetsLoaded: () => void;
  setModels: (next: SetStateAction<ModelResponse["data"]>) => void;
  setAgentAdapters: (next: SetStateAction<AgentAdapterRecord[]>) => void;
  setAgentAdapterApprovalMode: (value: string) => void;
  setAgentAdapterHealth: (adapterID: string, record: AgentAdapterHealthRecord) => void;
  applyAgentAdapterAuthResult: (adapterID: string, authStatus: "ok" | "unauthenticated") => void;
  setAgentAdapterHealthLoading: (adapterID: string, loading: boolean) => void;
  loadModelCatalog: () => Promise<ModelResponse>;
  loadAgentAdapterCatalog: () => Promise<AgentAdapterResponse>;
  refreshProviders: () => Promise<void>;
  refreshAgentAdapters: () => Promise<RefreshAgentAdaptersResult>;
  probeAgentAdapter: (adapterID: string) => Promise<ProbeAdapterResult>;
  verifyModelToolSupport: (
    provider: string,
    model: string,
  ) => Promise<VerifyModelToolSupportResult>;
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
  | { type: "agentAdapters/catalogSet"; next: AgentAdapterRecord[] }
  | { type: "agentAdapterApprovalMode/set"; value: string }
  | { type: "agentAdapterHealth/set"; adapterID: string; record: AgentAdapterHealthRecord }
  | {
      type: "agentAdapterAuth/apply";
      adapterID: string;
      authStatus: "ok" | "unauthenticated";
    }
  | { type: "agentAdapterHealthLoading/set"; adapterID: string; loading: boolean }
  | { type: "modelToolSupportLoading/set"; key: string; loading: boolean };

const initialState: ProvidersAndModelsState = {
  providers: [],
  providerPresets: [],
  providerPresetsLoaded: false,
  models: [],
  agentAdapters: [],
  agentAdapterApprovalMode: "",
  agentAdapterHealthByID: new Map(),
  agentAdapterHealthLoadingByID: new Map(),
  modelToolSupportLoadingByKey: new Map(),
};

function resolve<T>(prev: T, next: SetStateAction<T>): T {
  return typeof next === "function" ? (next as (prev: T) => T)(prev) : next;
}

function applyAgentAdapterDiagnostic(
  current: AgentAdapterRecord,
  diagnostic: AgentAdapterRecord,
): AgentAdapterRecord {
  return {
    ...diagnostic,
    // A probe can enrich auth, version, and capability metadata, but its
    // disposable process result must not replace the passive catalog fields
    // that control and disclose a later launch. Chat creation resolves these
    // fields again and performs the authoritative ACP handshake.
    available: current.available,
    status: current.status,
    path: current.path,
    error: current.error,
    remote_credential_mode: current.remote_credential_mode,
    remote_credential_ok: current.remote_credential_ok,
    remote_credential_hint: current.remote_credential_hint,
  };
}

function applyAgentAdapterCatalog(
  current: AgentAdapterRecord[],
  catalog: AgentAdapterRecord[],
  healthByID: Map<string, AgentAdapterHealthRecord>,
): AgentAdapterRecord[] {
  const currentByID = new Map(current.map((item) => [item.id, item]));
  return catalog.map((item) => {
    const diagnostic = currentByID.get(item.id);
    if (!diagnostic || !healthByID.has(item.id)) return item;
    return {
      ...item,
      // Passive discovery owns the adapter catalog and every field that can
      // gate or describe a future launch. Keep only evidence produced by the
      // operator's last explicit diagnostic; the cheap catalog deliberately
      // omits these process-derived fields.
      adapter_version: diagnostic.adapter_version,
      agent_version: diagnostic.agent_version,
      version_outside_range: diagnostic.version_outside_range,
      auth_status: diagnostic.auth_status,
      auth_error: diagnostic.auth_error,
      supports_authenticate: diagnostic.supports_authenticate,
      supports_logout: diagnostic.supports_logout,
      config_options: diagnostic.config_options,
    };
  });
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
    case "agentAdapters/catalogSet":
      return {
        ...state,
        agentAdapters: applyAgentAdapterCatalog(
          state.agentAdapters,
          action.next,
          state.agentAdapterHealthByID,
        ),
      };
    case "agentAdapterApprovalMode/set":
      return { ...state, agentAdapterApprovalMode: action.value };
    case "agentAdapterHealth/set": {
      const next = new Map(state.agentAdapterHealthByID);
      next.set(action.adapterID, action.record);
      return { ...state, agentAdapterHealthByID: next };
    }
    case "agentAdapterAuth/apply": {
      const nextHealth = new Map(state.agentAdapterHealthByID);
      nextHealth.delete(action.adapterID);
      return {
        ...state,
        agentAdapters: state.agentAdapters.map((item) =>
          item.id === action.adapterID
            ? { ...item, auth_status: action.authStatus, auth_error: undefined }
            : item,
        ),
        // The explicit auth result supersedes auth evidence from the previous
        // disposable diagnostic. Publish the row and health invalidation in
        // one reducer transition so the UI cannot render a contradictory
        // intermediate state.
        agentAdapterHealthByID: nextHealth,
      };
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
    case "modelToolSupportLoading/set": {
      const map = state.modelToolSupportLoadingByKey;
      if (action.loading) {
        const next = new Map(map);
        next.set(action.key, true);
        return { ...state, modelToolSupportLoadingByKey: next };
      }
      if (!map.has(action.key)) return state;
      const next = new Map(map);
      next.delete(action.key);
      return { ...state, modelToolSupportLoadingByKey: next };
    }
  }
}

const ProvidersAndModelsContext = createContext<ProvidersAndModelsContextValue | null>(null);

export function ProvidersAndModelsProvider({
  children,
  initialState: seededState,
}: {
  children: ReactNode;
  initialState?: Partial<ProvidersAndModelsState>;
}) {
  const [state, dispatch] = useReducer(
    reducer,
    seededState ? { ...initialState, ...seededState } : initialState,
  );
  const probeAgentAdapterInFlightRef = useRef(new Map<string, Promise<ProbeAdapterResult>>());
  const verifyModelToolSupportInFlightRef = useRef(
    new Map<string, Promise<VerifyModelToolSupportResult>>(),
  );
  // A catalog response can be older than an explicit model mutation (notably a
  // completed tool-support verification). Track those local writes separately
  // from catalog refreshes: a catalog result must neither erase newer evidence
  // nor prevent a newer refresh from replacing an older refresh.
  const modelsMutationRevisionRef = useRef(0);
  const latestModelsRefreshRef = useRef(0);
  const latestAgentAdaptersRefreshRef = useRef(0);

  const setProviders = useCallback(
    (next: SetStateAction<ProviderStatusResponse["data"]>) =>
      dispatch({ type: "providers/set", next }),
    [],
  );
  const setProviderPresets = useCallback(
    (next: SetStateAction<ProviderPresetRecord[]>) =>
      dispatch({ type: "providerPresets/set", next }),
    [],
  );
  const markProviderPresetsLoaded = useCallback(
    () => dispatch({ type: "providerPresetsLoaded/mark" }),
    [],
  );
  const applyModels = useCallback((next: SetStateAction<ModelResponse["data"]>) => {
    modelsMutationRevisionRef.current += 1;
    dispatch({ type: "models/set", next });
  }, []);
  const setModels = applyModels;
  const loadModelCatalog = useCallback(async (): Promise<ModelResponse> => {
    const modelsMutationRevisionAtStart = modelsMutationRevisionRef.current;
    const refreshID = ++latestModelsRefreshRef.current;
    const response = await getModels();
    if (
      modelsMutationRevisionRef.current === modelsMutationRevisionAtStart &&
      latestModelsRefreshRef.current === refreshID
    ) {
      dispatch({ type: "models/set", next: response.data ?? [] });
    }
    return response;
  }, []);
  const setAgentAdapters = useCallback((next: SetStateAction<AgentAdapterRecord[]>) => {
    // This low-level projection setter exists for test fixtures and explicit
    // local mutations. Fence older catalog reads so even those writes cannot
    // be rolled back by an in-flight dashboard refresh. Production catalog
    // reads must use loadAgentAdapterCatalog instead.
    latestAgentAdaptersRefreshRef.current += 1;
    dispatch({ type: "agentAdapters/set", next });
  }, []);
  const setAgentAdapterApprovalMode = useCallback(
    (value: string) => dispatch({ type: "agentAdapterApprovalMode/set", value }),
    [],
  );
  const setAgentAdapterHealth = useCallback(
    (adapterID: string, record: AgentAdapterHealthRecord) =>
      dispatch({ type: "agentAdapterHealth/set", adapterID, record }),
    [],
  );
  const applyAgentAdapterAuthResult = useCallback(
    (adapterID: string, authStatus: "ok" | "unauthenticated") => {
      // An auth action starts the adapter and is newer evidence than any
      // passive read already in flight. A later operator refresh may replace
      // it with catalog auth=unknown, but an older response must not.
      latestAgentAdaptersRefreshRef.current += 1;
      dispatch({ type: "agentAdapterAuth/apply", adapterID, authStatus });
    },
    [],
  );
  const setAgentAdapterHealthLoading = useCallback(
    (adapterID: string, loading: boolean) =>
      dispatch({ type: "agentAdapterHealthLoading/set", adapterID, loading }),
    [],
  );

  const refreshProviders = useCallback(async () => {
    try {
      const [pResult, mResult] = await Promise.allSettled([getProviders(), loadModelCatalog()]);
      if (pResult.status === "fulfilled") {
        dispatch({ type: "providers/set", next: pResult.value.data ?? [] });
      }
      if (pResult.status === "rejected" || mResult.status === "rejected") {
        warn("providersAndModels.refresh.failed", {
          providers:
            pResult.status === "rejected"
              ? pResult.reason instanceof Error
                ? pResult.reason.message
                : String(pResult.reason)
              : undefined,
          models:
            mResult.status === "rejected"
              ? mResult.reason instanceof Error
                ? mResult.reason.message
                : String(mResult.reason)
              : undefined,
        });
      }
    } catch (error) {
      // Best-effort background refresh — report the failure without making
      // a completed provider operation look unsuccessful.
      warn("providersAndModels.refresh.failed", {
        err: error instanceof Error ? error.message : String(error),
      });
    }
  }, [loadModelCatalog]);

  const loadAgentAdapterCatalog = useCallback(async (): Promise<AgentAdapterResponse> => {
    const refreshID = ++latestAgentAdaptersRefreshRef.current;
    const response = await getAgentAdapters();
    if (latestAgentAdaptersRefreshRef.current === refreshID) {
      dispatch({ type: "agentAdapters/catalogSet", next: response.data ?? [] });
    }
    return response;
  }, []);

  const refreshAgentAdapters = useCallback(async (): Promise<RefreshAgentAdaptersResult> => {
    try {
      const payload = await loadAgentAdapterCatalog();
      return { ok: true, adapters: payload.data ?? [] };
    } catch (error) {
      return {
        ok: false,
        error:
          error instanceof Error ? error.message : "Failed to refresh external-agent discovery.",
      };
    }
  }, [loadAgentAdapterCatalog]);

  const probeAgentAdapter = useCallback(
    async (adapterID: string): Promise<ProbeAdapterResult> => {
      if (!adapterID) return { ok: false, error: "Adapter id required to probe." };
      const inFlight = probeAgentAdapterInFlightRef.current.get(adapterID);
      if (inFlight) return inFlight;
      const probe: Promise<ProbeAdapterResult> = (async (): Promise<ProbeAdapterResult> => {
        dispatch({ type: "agentAdapterHealthLoading/set", adapterID, loading: true });
        try {
          const payload = await probeAgentAdapterRequest(adapterID);
          dispatch({ type: "agentAdapterHealth/set", adapterID, record: payload.data.health });
          dispatch({
            type: "agentAdapters/set",
            next: (current) =>
              current.map((item) =>
                item.id === adapterID
                  ? applyAgentAdapterDiagnostic(item, payload.data.adapter)
                  : item,
              ),
          });
          const catalogRefresh = await refreshAgentAdapters();
          if (!catalogRefresh.ok) {
            warn("agentAdapters.refreshAfterDiagnostic.failed", {
              adapterID,
              err: catalogRefresh.error,
            });
          }
          return { ok: true, health: payload.data.health };
        } catch (error) {
          return {
            ok: false,
            error: error instanceof Error ? error.message : "Failed to probe adapter.",
          };
        } finally {
          probeAgentAdapterInFlightRef.current.delete(adapterID);
          dispatch({ type: "agentAdapterHealthLoading/set", adapterID, loading: false });
        }
      })();
      probeAgentAdapterInFlightRef.current.set(adapterID, probe);
      return probe;
    },
    [refreshAgentAdapters],
  );

  const verifyModelToolSupport = useCallback(
    async (provider: string, model: string): Promise<VerifyModelToolSupportResult> => {
      const normalizedProvider = provider.trim();
      const normalizedModel = model.trim();
      if (!normalizedProvider || !normalizedModel) {
        return { ok: false, error: "Provider and model are required to verify tool support." };
      }
      const key = modelToolSupportKey(normalizedProvider, normalizedModel);
      const inFlight = verifyModelToolSupportInFlightRef.current.get(key);
      if (inFlight) return inFlight;

      const verification: Promise<VerifyModelToolSupportResult> = (async () => {
        dispatch({ type: "modelToolSupportLoading/set", key, loading: true });
        try {
          const probe = await verifyModelToolSupportRequest(normalizedProvider, normalizedModel);
          // Apply the returned projection first so the operator sees the
          // result even if the best-effort catalog refresh is delayed.
          applyModels((current) =>
            current.map((entry) =>
              entry.id === probe.data.model && entry.metadata?.provider === probe.data.provider
                ? {
                    ...entry,
                    metadata: {
                      ...entry.metadata,
                      capabilities: {
                        ...probe.data.capabilities,
                        tool_verification:
                          probe.data.verification ?? probe.data.capabilities.tool_verification,
                      },
                    },
                  }
                : entry,
            ),
          );
          // The proof is provider-generation-bound. Reload both model catalog
          // and provider status after the bounded explicit action rather than
          // guessing at any other affected route. This refresh is strictly
          // best-effort: the probe result above remains authoritative until a
          // later catalog response replaces it.
          try {
            await refreshProviders();
          } catch (error) {
            // refreshProviders normally handles its own transport failures,
            // but keep the completed diagnostic independent if that contract
            // ever changes.
            warn("modelToolSupport.refresh.failed", {
              provider: normalizedProvider,
              model: normalizedModel,
              err: error instanceof Error ? error.message : String(error),
            });
          }
          return { ok: true, probe };
        } catch (error) {
          return {
            ok: false,
            error: error instanceof Error ? error.message : "Failed to verify tool support.",
          };
        } finally {
          verifyModelToolSupportInFlightRef.current.delete(key);
          dispatch({ type: "modelToolSupportLoading/set", key, loading: false });
        }
      })();
      verifyModelToolSupportInFlightRef.current.set(key, verification);
      return verification;
    },
    [applyModels, refreshProviders],
  );

  const actions = useMemo<ProvidersAndModelsActions>(
    () => ({
      setProviders,
      setProviderPresets,
      markProviderPresetsLoaded,
      setModels,
      setAgentAdapters,
      setAgentAdapterApprovalMode,
      setAgentAdapterHealth,
      applyAgentAdapterAuthResult,
      setAgentAdapterHealthLoading,
      loadModelCatalog,
      loadAgentAdapterCatalog,
      refreshProviders,
      refreshAgentAdapters,
      probeAgentAdapter,
      verifyModelToolSupport,
    }),
    [
      setProviders,
      setProviderPresets,
      markProviderPresetsLoaded,
      setModels,
      setAgentAdapters,
      setAgentAdapterApprovalMode,
      setAgentAdapterHealth,
      applyAgentAdapterAuthResult,
      setAgentAdapterHealthLoading,
      loadModelCatalog,
      loadAgentAdapterCatalog,
      refreshProviders,
      refreshAgentAdapters,
      probeAgentAdapter,
      verifyModelToolSupport,
    ],
  );

  const value = useMemo(() => ({ state, actions }), [state, actions]);

  return (
    <ProvidersAndModelsContext.Provider value={value}>
      {children}
    </ProvidersAndModelsContext.Provider>
  );
}

export function useProvidersAndModels(): ProvidersAndModelsContextValue {
  const ctx = useContext(ProvidersAndModelsContext);
  if (!ctx) {
    throw new Error("useProvidersAndModels must be used inside a <ProvidersAndModelsProvider>");
  }
  const overrides = useContext(CoordinatorOverridesContext);
  return {
    state: ctx.state,
    actions: applyOverride(ctx.actions, overrides?.providersAndModelsSlice),
  };
}

// useEnsureProviderPresetsLoaded fetches the provider preset
// catalog on first call if the slice's providerPresetsLoaded flag
// is false. Used by AddProviderModal and TasksView; the dashboard
// loader no longer pulls presets at boot.
//
// Dedup behavior:
//   - The `inFlight` ref is per hook instance — it blocks the same
//     hook from re-firing while its first fetch is pending.
//   - Cross-surface dedup happens AFTER the first fetch resolves,
//     when `providerPresetsLoaded` flips to true: subsequent calls
//     (whether from a re-mount of the same surface or a different
//     surface) early-return on the loaded check. If two surfaces
//     mount the hook simultaneously before either flip lands, both
//     fetches would fire — acceptable today (only AddProviderModal
//     and TasksView call it, and they don't mount concurrently).
//
// Tolerates failures by leaving `providerPresetsLoaded` false so a
// later mount retries.
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
        warn("providerPresets.ensureLoaded.failed", {
          err: err instanceof Error ? err.message : String(err),
        });
      } finally {
        inFlight.current = false;
      }
    })();
  }, [when, state.providerPresetsLoaded, actions]);
}
