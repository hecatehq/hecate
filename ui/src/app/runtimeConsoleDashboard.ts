import {
  ApiError,
  getAgentAdapters,
  getChatSession,
  getChatSessions,
  getSettingsConfig,
  getHealth,
  getModels,
  getProviders,
  getRuntimeStats,
  getSession,
} from "../lib/api";
import { isRemoteRuntimeSession } from "../lib/runtime-utils";
import type { HealthResponse, RuntimeStatsResponse, SessionResponse } from "../types/runtime";
import type { ModelResponse } from "../types/model";
import type { ConfiguredStateResponse, ProviderStatusResponse } from "../types/provider";
import type { AgentAdapterRecord } from "../types/agent-adapter";
import type { ChatSessionRecord, ChatSessionsResponse } from "../types/chat";

export type SessionState = {
  label: string;
};

export type DashboardPreviousState = {
  providers: ProviderStatusResponse["data"];
  agentAdapters: AgentAdapterRecord[];
  chatSessions: ChatSessionsResponse["data"];
  activeChatSession: ChatSessionRecord | null;
  settingsConfig: ConfiguredStateResponse["data"] | null;
};

// DashboardEssentials is the minimal slice of dashboard state the
// app shell needs to render its activity bar + status bar: health
// (gateway version + status), session label, and
// configured-provider count. Emitting these early lets the
// AuthLoadingShell gate clear ~50–150 ms sooner on cold launches —
// the rest of the snapshot (models, chat sessions, …) continues to
// load in the background and lands when ready. The status-bar model
// count starts at 0 and updates once wave 2 lands; the brief flash
// is an accepted trade-off for clearing the gate sooner. Retention
// runs, Usage events/summary, and provider presets are view-deferred:
// SettingsView / UsageView / AddProviderModal / TasksView mount and
// call dedicated actions when they need them; none are in this
// snapshot.
export type DashboardEssentials = {
  health: HealthResponse;
  sessionInfo: SessionResponse["data"] | null;
  settingsConfig: ConfiguredStateResponse["data"] | null;
};

export type DashboardSnapshot = {
  health: HealthResponse;
  sessionInfo: SessionResponse["data"] | null;
  models: ModelResponse["data"];
  providers: ProviderStatusResponse["data"];
  agentAdapters: AgentAdapterRecord[];
  chatSessions: ChatSessionsResponse["data"];
  activeChatSessionID: string;
  activeChatSession: ChatSessionRecord | null;
  settingsConfig: ConfiguredStateResponse["data"] | null;
  agentAdapterApprovalMode: string;
  rtkAvailable: boolean;
  rtkPath: string;
};

type DashboardResults = {
  health: PromiseSettledResult<HealthResponse>;
  session: PromiseSettledResult<SessionResponse>;
  models: PromiseSettledResult<ModelResponse>;
  providers: PromiseSettledResult<ProviderStatusResponse>;
  agentAdapters: PromiseSettledResult<{ object: string; data: AgentAdapterRecord[] }>;
  chatSessions: PromiseSettledResult<ChatSessionsResponse>;
  settingsConfig: PromiseSettledResult<ConfiguredStateResponse>;
  runtimeStats: PromiseSettledResult<RuntimeStatsResponse>;
};

export async function resolveDashboardSnapshot(args: {
  activeChatSessionID: string;
  previous: DashboardPreviousState;
  /**
   * Fires once the essentials wave (health + session + models +
   * settingsConfig) resolves, before the secondary wave starts.
   * The hook uses this to commit just enough state to clear the
   * Connecting gate so the rest of the dashboard can load behind a
   * rendered shell rather than a blocking spinner.
   */
  onEssentials?: (essentials: DashboardEssentials) => void;
}): Promise<DashboardSnapshot> {
  const results = await loadDashboardResults({
    onEssentials: args.onEssentials ? (essentials) => args.onEssentials!(essentials) : undefined,
    previousSettingsConfig: args.previous.settingsConfig,
  });
  const health = requireFulfilledDashboardResult(results.health);
  const sessionInfo = results.session.status === "fulfilled" ? results.session.value.data : null;
  const models = resolveModelsResult(results.models);
  const providers = resolveDashboardResult(results.providers, args.previous.providers);
  const agentAdapters = resolveDashboardResult(results.agentAdapters, args.previous.agentAdapters);
  const settingsConfig = resolveDashboardResult(
    results.settingsConfig,
    args.previous.settingsConfig,
  );
  const agentAdapterApprovalMode =
    results.runtimeStats.status === "fulfilled"
      ? (results.runtimeStats.value.data.agent_adapter_approval_mode ?? "")
      : "";
  const rtkAvailable =
    results.runtimeStats.status === "fulfilled"
      ? Boolean(results.runtimeStats.value.data.rtk_available)
      : false;
  const rtkPath =
    results.runtimeStats.status === "fulfilled"
      ? (results.runtimeStats.value.data.rtk_path ?? "")
      : "";
  const chatState = await resolveChatDashboardState({
    activeSessionID: args.activeChatSessionID,
    previousSessions: args.previous.chatSessions,
    previousActiveSession: args.previous.activeChatSession,
    result: results.chatSessions,
  });

  return {
    health,
    sessionInfo,
    models,
    providers,
    agentAdapters,
    chatSessions: chatState.sessions,
    activeChatSessionID: chatState.activeSessionID,
    activeChatSession: chatState.activeSession,
    settingsConfig,
    agentAdapterApprovalMode,
    rtkAvailable,
    rtkPath,
  };
}

export function deriveSessionState(sessionInfo: SessionResponse["data"] | null): SessionState {
  return { label: isRemoteRuntimeSession(sessionInfo) ? "Hosted" : "Local" };
}

async function loadDashboardResults(opts: {
  onEssentials?: (essentials: DashboardEssentials) => void;
  previousSettingsConfig: ConfiguredStateResponse["data"] | null;
}): Promise<DashboardResults> {
  // Wave 1 — essentials. Three parallel calls drive everything the
  // app shell needs to clear the Connecting gate: gateway health
  // (version + status), session label, and configured-provider
  // count. Keeping models in the chattier secondary wave saves
  // ~30-50 ms on cold cache; the status-bar model count starts at
  // 0 and updates once wave 2 resolves.
  const [health, session, settingsConfig] = await Promise.allSettled([
    getHealth(),
    getSession(),
    getSettingsConfig(),
  ]);

  if (opts.onEssentials) {
    opts.onEssentials({
      health:
        health.status === "fulfilled"
          ? health.value
          : // Surface a synthetic "down" health so the gate can still
            // render the shell with an error banner instead of hanging
            // on the loading state.
            ({ status: "down", time: new Date().toISOString() } as HealthResponse),
      sessionInfo: session.status === "fulfilled" ? session.value.data : null,
      settingsConfig: resolveDashboardResult(settingsConfig, opts.previousSettingsConfig),
    });
  }

  // Wave 2 — secondary. Everything else fires in parallel; the
  // shell is already on screen, so individual workspaces fill in
  // with their data as it arrives. Providers is conditional on
  // settingsConfig having at least one configured provider, which
  // we already know after wave 1 — no need for the prior third
  // sequential wave.
  const initialReject = <T>(): PromiseSettledResult<T> => ({
    status: "rejected",
    reason: new Error("uninitialized"),
  });
  let models: PromiseSettledResult<ModelResponse> = initialReject();
  let providers: PromiseSettledResult<ProviderStatusResponse> = initialReject();
  let agentAdapters: PromiseSettledResult<{ object: string; data: AgentAdapterRecord[] }> =
    initialReject();
  let chatSessions: PromiseSettledResult<ChatSessionsResponse> = initialReject();
  let runtimeStats: PromiseSettledResult<RuntimeStatsResponse> = initialReject();

  // Resolve settings-config the same way we publish it via onEssentials:
  // fall back to the previous snapshot when this wave's fetch rejected
  // so a transient failure doesn't make us drop providers (the secondary
  // wave below decides whether to refresh getProviders() based on this).
  const resolvedSettingsConfig = resolveDashboardResult(
    settingsConfig,
    opts.previousSettingsConfig,
  );
  const configured = resolvedSettingsConfig?.providers ?? [];
  const secondary: Promise<unknown>[] = [
    getModels().then(
      (r) => {
        models = { status: "fulfilled", value: r };
      },
      (e) => {
        models = { status: "rejected", reason: e };
      },
    ),
    getAgentAdapters().then(
      (r) => {
        agentAdapters = { status: "fulfilled", value: r };
      },
      (e) => {
        agentAdapters = { status: "rejected", reason: e };
      },
    ),
    getChatSessions().then(
      (r) => {
        chatSessions = { status: "fulfilled", value: r };
      },
      (e) => {
        chatSessions = { status: "rejected", reason: e };
      },
    ),
    getRuntimeStats().then(
      (r) => {
        runtimeStats = { status: "fulfilled", value: r };
      },
      (e) => {
        runtimeStats = { status: "rejected", reason: e };
      },
    ),
  ];
  if (configured.length > 0) {
    secondary.push(
      getProviders().then(
        (r) => {
          providers = { status: "fulfilled", value: r };
        },
        (e) => {
          providers = { status: "rejected", reason: e };
        },
      ),
    );
  } else if (settingsConfig.status === "fulfilled") {
    // We confirmed zero configured providers from a fresh fetch —
    // publish an empty list. On settingsConfig failure, leave
    // providers as initialReject() so resolveDashboardResult in
    // resolveDashboardSnapshot keeps previous.providers.
    providers = {
      status: "fulfilled",
      value: { object: "list", data: [] } as ProviderStatusResponse,
    };
  }
  await Promise.all(secondary);

  return {
    health,
    session,
    models,
    providers,
    agentAdapters,
    chatSessions,
    settingsConfig,
    runtimeStats,
  };
}

function requireFulfilledDashboardResult<T>(result: PromiseSettledResult<T>): T {
  if (result.status === "fulfilled") {
    return result.value;
  }
  throw new Error("failed to load runtime console data");
}

function resolveModelsResult(result: PromiseSettledResult<ModelResponse>): ModelResponse["data"] {
  if (result.status === "fulfilled") {
    return result.value.data;
  }
  return [];
}

function resolveDashboardResult<T>(result: PromiseSettledResult<{ data: T }>, previous: T): T {
  if (result.status === "fulfilled") {
    return result.value.data;
  }
  return previous;
}

async function resolveChatDashboardState(args: {
  activeSessionID: string;
  previousSessions: ChatSessionsResponse["data"];
  previousActiveSession: ChatSessionRecord | null;
  result: PromiseSettledResult<ChatSessionsResponse>;
}): Promise<{
  sessions: ChatSessionsResponse["data"];
  activeSessionID: string;
  activeSession: ChatSessionRecord | null;
}> {
  if (args.result.status !== "fulfilled") {
    return {
      sessions: args.previousSessions,
      activeSessionID: args.activeSessionID,
      activeSession: args.previousActiveSession,
    };
  }

  const sessions = args.result.value.data ?? [];
  const activeSessionID =
    args.activeSessionID && sessions.some((entry) => entry.id === args.activeSessionID)
      ? args.activeSessionID
      : "";

  if (!activeSessionID) {
    return { sessions, activeSessionID, activeSession: null };
  }

  try {
    const sessionResult = await getChatSession(activeSessionID);
    return { sessions, activeSessionID, activeSession: sessionResult.data };
  } catch (error) {
    if (!(error instanceof ApiError) || error.status !== 404) {
      return { sessions, activeSessionID, activeSession: args.previousActiveSession };
    }
    return { sessions, activeSessionID: "", activeSession: null };
  }
}
