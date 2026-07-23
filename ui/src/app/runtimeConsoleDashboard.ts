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
import type { HealthResponse, RuntimeStatsResponse, SessionResponse } from "../types/runtime";
import type { ModelResponse } from "../types/model";
import type { ConfiguredStateResponse, ProviderStatusResponse } from "../types/provider";
import type { AgentAdapterRecord, AgentAdapterResponse } from "../types/agent-adapter";
import type { ChatSessionRecord, ChatSessionsResponse } from "../types/chat";

export type SessionState = {
  label: string;
  title: string;
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
  chatSessionsFresh: boolean;
  activeChatSessionID: string;
  activeChatSession: ChatSessionRecord | null;
  activeChatSessionFresh: boolean;
  activeChatSessionMissing: boolean;
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
  agentAdapters: PromiseSettledResult<AgentAdapterResponse>;
  chatSessions: PromiseSettledResult<ChatSessionsResponse>;
  settingsConfig: PromiseSettledResult<ConfiguredStateResponse>;
  runtimeStats: PromiseSettledResult<RuntimeStatsResponse>;
};

export async function resolveDashboardSnapshot(args: {
  activeChatSessionID: string;
  previous: DashboardPreviousState;
  /**
   * Optional model-catalog loader. The providers/models slice supplies this
   * in production so every catalog response commits through its shared
   * freshness fence; standalone callers use the API client directly.
   */
  loadModels?: () => Promise<ModelResponse>;
  /**
   * Optional passive external-agent catalog loader. The providers/models
   * slice supplies this in production so dashboard hydration and explicit
   * refreshes share one request-order fence.
   */
  loadAgentAdapters?: () => Promise<AgentAdapterResponse>;
  /**
   * Fires once the essentials wave (health + session + settingsConfig)
   * resolves, before the secondary wave starts.
   * The hook uses this to commit just enough state to clear the
   * Connecting gate so the rest of the dashboard can load behind a
   * rendered shell rather than a blocking spinner.
   */
  onEssentials?: (essentials: DashboardEssentials) => void;
  onChatSessionsReadStart?: () => void;
  onActiveChatSessionReadStart?: (sessionID: string) => void;
}): Promise<DashboardSnapshot> {
  const results = await loadDashboardResults({
    onEssentials: args.onEssentials ? (essentials) => args.onEssentials!(essentials) : undefined,
    onChatSessionsReadStart: args.onChatSessionsReadStart,
    loadModels: args.loadModels,
    loadAgentAdapters: args.loadAgentAdapters,
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
    onActiveChatSessionReadStart: args.onActiveChatSessionReadStart,
  });

  return {
    health,
    sessionInfo,
    models,
    providers,
    agentAdapters,
    chatSessions: chatState.sessions,
    chatSessionsFresh: chatState.sessionsFresh,
    activeChatSessionID: chatState.activeSessionID,
    activeChatSession: chatState.activeSession,
    activeChatSessionFresh: chatState.activeSessionFresh,
    activeChatSessionMissing: chatState.activeSessionMissing,
    settingsConfig,
    agentAdapterApprovalMode,
    rtkAvailable,
    rtkPath,
  };
}

export function deriveSessionState(sessionInfo: SessionResponse["data"] | null): SessionState {
  if (!sessionInfo?.runtime_host) {
    return {
      label: "Runtime unavailable",
      title: "Runtime host identity did not load.",
    };
  }
  const host = sessionInfo.runtime_host;
  const remote = host.operator_access === "remote_supervision";
  const executionBoundary = remote
    ? `This browser supervises ${host.label}. Files, tasks, and External Agents run on that host.`
    : `Hecate is running on ${host.label}. Files, tasks, and External Agents run on this host.`;
  const localOnlyActions = host.local_only_actions_available
    ? "Host-local actions are available."
    : "Host-local actions are unavailable.";
  const publicURL = host.public_url ? ` Public URL: ${host.public_url}.` : "";
  return {
    label: remote ? `Supervising ${host.label}` : `On ${host.label}`,
    title: `${executionBoundary} ${localOnlyActions} Runtime ID: ${host.id}.${publicURL}`,
  };
}

async function loadDashboardResults(opts: {
  onEssentials?: (essentials: DashboardEssentials) => void;
  onChatSessionsReadStart?: () => void;
  loadModels?: () => Promise<ModelResponse>;
  loadAgentAdapters?: () => Promise<AgentAdapterResponse>;
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
  let agentAdapters: PromiseSettledResult<AgentAdapterResponse> = initialReject();
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
  const loadModels = opts.loadModels ?? getModels;
  const loadAgentAdapters = opts.loadAgentAdapters ?? getAgentAdapters;
  opts.onChatSessionsReadStart?.();
  const chatSessionsRequest = getChatSessions();
  const secondary: Promise<unknown>[] = [
    loadModels().then(
      (r) => {
        models = { status: "fulfilled", value: r };
      },
      (e) => {
        models = { status: "rejected", reason: e };
      },
    ),
    loadAgentAdapters().then(
      (r) => {
        agentAdapters = { status: "fulfilled", value: r };
      },
      (e) => {
        agentAdapters = { status: "rejected", reason: e };
      },
    ),
    chatSessionsRequest.then(
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
  onActiveChatSessionReadStart?: (sessionID: string) => void;
}): Promise<{
  sessions: ChatSessionsResponse["data"];
  sessionsFresh: boolean;
  activeSessionID: string;
  activeSession: ChatSessionRecord | null;
  activeSessionFresh: boolean;
  activeSessionMissing: boolean;
}> {
  if (args.result.status !== "fulfilled") {
    return {
      sessions: args.previousSessions,
      sessionsFresh: false,
      activeSessionID: args.activeSessionID,
      activeSession: args.previousActiveSession,
      activeSessionFresh: false,
      activeSessionMissing: false,
    };
  }

  const sessions = args.result.value.data ?? [];
  const activeSessionID =
    args.activeSessionID && sessions.some((entry) => entry.id === args.activeSessionID)
      ? args.activeSessionID
      : "";

  if (!activeSessionID) {
    return {
      sessions,
      sessionsFresh: true,
      activeSessionID,
      activeSession: null,
      activeSessionFresh: false,
      activeSessionMissing: Boolean(args.activeSessionID),
    };
  }

  try {
    args.onActiveChatSessionReadStart?.(activeSessionID);
    const sessionResult = await getChatSession(activeSessionID);
    return {
      sessions,
      sessionsFresh: true,
      activeSessionID,
      activeSession: sessionResult.data,
      activeSessionFresh: true,
      activeSessionMissing: false,
    };
  } catch (error) {
    if (!(error instanceof ApiError) || error.status !== 404) {
      return {
        sessions,
        sessionsFresh: true,
        activeSessionID,
        activeSession: args.previousActiveSession,
        activeSessionFresh: false,
        activeSessionMissing: false,
      };
    }
    return {
      sessions,
      sessionsFresh: true,
      activeSessionID: "",
      activeSession: null,
      activeSessionFresh: false,
      activeSessionMissing: true,
    };
  }
}
