import {
  ApiError,
  getAgentAdapters,
  getAgentChatSession,
  getAgentChatSessions,
  getChatSession,
  getChatSessions,
  getSettingsConfig,
  getHealth,
  getModels,
  getProviderPresets,
  getProviders,
  getRuntimeStats,
  getSession,
  getUsageEvents,
  getUsageSummary,
} from "../lib/api";
import type {
  AgentAdapterRecord,
  AgentChatSessionRecord,
  AgentChatSessionsResponse,
  ChatSessionRecord,
  ChatSessionsResponse,
  ConfiguredStateResponse,
  HealthResponse,
  ModelResponse,
  ProviderPresetRecord,
  ProviderStatusResponse,
  RuntimeStatsResponse,
  SessionResponse,
  UsageSummaryResponse,
  UsageEventsResponse,
} from "../types/runtime";

export type SessionState = {
  label: string;
};

export type DashboardPreviousState = {
  providers: ProviderStatusResponse["data"];
  agentAdapters: AgentAdapterRecord[];
  usageSummary: UsageSummaryResponse["data"] | null;
  chatSessions: ChatSessionsResponse["data"];
  activeChatSession: ChatSessionRecord | null;
  agentChatSessions: AgentChatSessionsResponse["data"];
  activeAgentChatSession: AgentChatSessionRecord | null;
  usageEvents: UsageEventsResponse["data"];
  settingsConfig: ConfiguredStateResponse["data"] | null;
};

// DashboardEssentials is the minimal slice of dashboard state the
// app shell needs to render its activity bar + status bar: health
// (gateway version + status), session label, model count, and
// configured-provider count. Emitting these early lets the
// AuthLoadingShell gate clear ~50–150 ms sooner on cold launches —
// the rest of the snapshot (chat sessions, usage, …) continues to
// load in the background and lands when ready. Retention runs are
// view-deferred: the SettingsView mounts and calls a dedicated
// action when it needs them; they're not in this snapshot at all.
export type DashboardEssentials = {
  health: HealthResponse;
  sessionInfo: SessionResponse["data"] | null;
  models: ModelResponse["data"];
  settingsConfig: ConfiguredStateResponse["data"] | null;
};

export type DashboardSnapshot = {
  health: HealthResponse;
  sessionInfo: SessionResponse["data"] | null;
  models: ModelResponse["data"];
  providers: ProviderStatusResponse["data"];
  providerPresets: ProviderPresetRecord[];
  agentAdapters: AgentAdapterRecord[];
  usageSummary: UsageSummaryResponse["data"] | null;
  chatSessions: ChatSessionsResponse["data"];
  chatSessionsHasMore: boolean;
  activeChatSessionID: string;
  activeChatSession: ChatSessionRecord | null;
  agentChatSessions: AgentChatSessionsResponse["data"];
  activeAgentChatSessionID: string;
  activeAgentChatSession: AgentChatSessionRecord | null;
  usageEvents: UsageEventsResponse["data"];
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
  providerPresets: PromiseSettledResult<{ object: string; data: ProviderPresetRecord[] }>;
  agentAdapters: PromiseSettledResult<{ object: string; data: AgentAdapterRecord[] }>;
  usageSummary: PromiseSettledResult<UsageSummaryResponse>;
  chatSessions: PromiseSettledResult<ChatSessionsResponse>;
  agentChatSessions: PromiseSettledResult<AgentChatSessionsResponse>;
  usageEvents: PromiseSettledResult<UsageEventsResponse>;
  settingsConfig: PromiseSettledResult<ConfiguredStateResponse>;
  runtimeStats: PromiseSettledResult<RuntimeStatsResponse>;
};

export async function resolveDashboardSnapshot(args: {
  activeChatSessionID: string;
  activeAgentChatSessionID: string;
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
    onEssentials: args.onEssentials
      ? (essentials) => args.onEssentials!(essentials)
      : undefined,
    previousSettingsConfig: args.previous.settingsConfig,
  });
  const health = requireFulfilledDashboardResult(results.health);
  const sessionInfo = results.session.status === "fulfilled" ? results.session.value.data : null;
  const models = resolveModelsResult(results.models);
  const providers = resolveDashboardResult(results.providers, args.previous.providers);
  const providerPresets = results.providerPresets.status === "fulfilled" ? results.providerPresets.value.data : [];
  const agentAdapters = resolveDashboardResult(results.agentAdapters, args.previous.agentAdapters);
  const usageSummary = resolveDashboardResult(results.usageSummary, args.previous.usageSummary);
  const usageEvents = resolveDashboardResult(results.usageEvents, args.previous.usageEvents);
  const settingsConfig = resolveDashboardResult(results.settingsConfig, args.previous.settingsConfig);
  const agentAdapterApprovalMode = results.runtimeStats.status === "fulfilled"
    ? (results.runtimeStats.value.data.agent_adapter_approval_mode ?? "")
    : "";
  const rtkAvailable = results.runtimeStats.status === "fulfilled"
    ? Boolean(results.runtimeStats.value.data.rtk_available)
    : false;
  const rtkPath = results.runtimeStats.status === "fulfilled"
    ? (results.runtimeStats.value.data.rtk_path ?? "")
    : "";
  const chatState = await resolveChatDashboardState({
    activeChatSessionID: args.activeChatSessionID,
    previousSessions: args.previous.chatSessions,
    previousActiveSession: args.previous.activeChatSession,
    result: results.chatSessions,
  });
  const agentChatState = await resolveAgentChatDashboardState({
    activeSessionID: args.activeAgentChatSessionID,
    previousSessions: args.previous.agentChatSessions,
    previousActiveSession: args.previous.activeAgentChatSession,
    result: results.agentChatSessions,
  });

  return {
    health,
    sessionInfo,
    models,
    providers,
    providerPresets,
    agentAdapters,
    usageSummary,
    chatSessions: chatState.sessions,
    chatSessionsHasMore: chatState.hasMore,
    activeChatSessionID: chatState.activeChatSessionID,
    activeChatSession: chatState.activeChatSession,
    agentChatSessions: agentChatState.sessions,
    activeAgentChatSessionID: agentChatState.activeSessionID,
    activeAgentChatSession: agentChatState.activeSession,
    usageEvents,
    settingsConfig,
    agentAdapterApprovalMode,
    rtkAvailable,
    rtkPath,
  };
}

export function deriveSessionState(_sessionInfo: SessionResponse["data"] | null): SessionState {
  return { label: "Local" };
}

async function loadDashboardResults(opts: {
  onEssentials?: (essentials: DashboardEssentials) => void;
  previousSettingsConfig: ConfiguredStateResponse["data"] | null;
}): Promise<DashboardResults> {
  // Wave 1 — essentials. Four parallel calls drive everything the
  // app shell needs to clear the Connecting gate: gateway health
  // (version + status), session label, model count for the status
  // bar, and configured-provider count. Folding models +
  // settingsConfig in here (vs. the prior 2-call wave that only
  // covered health + session) means the shell can render before
  // the much chattier secondary wave finishes.
  const [health, session, models, settingsConfig] = await Promise.allSettled([
    getHealth(),
    getSession(),
    getModels(),
    getSettingsConfig(),
  ]);

  if (opts.onEssentials) {
    opts.onEssentials({
      health: health.status === "fulfilled"
        ? health.value
        // Surface a synthetic "down" health so the gate can still
        // render the shell with an error banner instead of hanging
        // on the loading state.
        : { status: "down", time: new Date().toISOString() } as HealthResponse,
      sessionInfo: session.status === "fulfilled" ? session.value.data : null,
      models: resolveModelsResult(models),
      settingsConfig: resolveDashboardResult(settingsConfig, opts.previousSettingsConfig),
    });
  }

  // Wave 2 — secondary. Everything else fires in parallel; the
  // shell is already on screen, so individual workspaces fill in
  // with their data as it arrives. Providers is conditional on
  // settingsConfig having at least one configured provider, which
  // we already know after wave 1 — no need for the prior third
  // sequential wave.
  const initialReject = <T,>(): PromiseSettledResult<T> => ({ status: "rejected", reason: new Error("uninitialized") });
  let providers: PromiseSettledResult<ProviderStatusResponse> = initialReject();
  let providerPresets: PromiseSettledResult<{ object: string; data: ProviderPresetRecord[] }> = initialReject();
  let agentAdapters: PromiseSettledResult<{ object: string; data: AgentAdapterRecord[] }> = initialReject();
  let usageSummary: PromiseSettledResult<UsageSummaryResponse> = initialReject();
  let chatSessions: PromiseSettledResult<ChatSessionsResponse> = initialReject();
  let agentChatSessions: PromiseSettledResult<AgentChatSessionsResponse> = initialReject();
  let usageEvents: PromiseSettledResult<UsageEventsResponse> = initialReject();
  let runtimeStats: PromiseSettledResult<RuntimeStatsResponse> = initialReject();

  // Resolve settings-config the same way we publish it via onEssentials:
  // fall back to the previous snapshot when this wave's fetch rejected
  // so a transient failure doesn't make us drop providers (the secondary
  // wave below decides whether to refresh getProviders() based on this).
  const resolvedSettingsConfig = resolveDashboardResult(settingsConfig, opts.previousSettingsConfig);
  const configured = resolvedSettingsConfig?.providers ?? [];
  const secondary: Promise<unknown>[] = [
    getProviderPresets().then(r => { providerPresets = { status: "fulfilled", value: r }; }, e => { providerPresets = { status: "rejected", reason: e }; }),
    getAgentAdapters().then(r => { agentAdapters = { status: "fulfilled", value: r }; }, e => { agentAdapters = { status: "rejected", reason: e }; }),
    getChatSessions(20).then(r => { chatSessions = { status: "fulfilled", value: r }; }, e => { chatSessions = { status: "rejected", reason: e }; }),
    getAgentChatSessions().then(r => { agentChatSessions = { status: "fulfilled", value: r }; }, e => { agentChatSessions = { status: "rejected", reason: e }; }),
    getUsageSummary("").then(r => { usageSummary = { status: "fulfilled", value: r }; }, e => { usageSummary = { status: "rejected", reason: e }; }),
    getUsageEvents(20).then(r => { usageEvents = { status: "fulfilled", value: r }; }, e => { usageEvents = { status: "rejected", reason: e }; }),
    getRuntimeStats().then(r => { runtimeStats = { status: "fulfilled", value: r }; }, e => { runtimeStats = { status: "rejected", reason: e }; }),
  ];
  if (configured.length > 0) {
    secondary.push(getProviders().then(
      r => { providers = { status: "fulfilled", value: r }; },
      e => { providers = { status: "rejected", reason: e }; },
    ));
  } else if (settingsConfig.status === "fulfilled") {
    // We confirmed zero configured providers from a fresh fetch —
    // publish an empty list. On settingsConfig failure, leave
    // providers as initialReject() so resolveDashboardResult in
    // resolveDashboardSnapshot keeps previous.providers.
    providers = { status: "fulfilled", value: { object: "list", data: [] } as ProviderStatusResponse };
  }
  await Promise.all(secondary);

  return {
    health,
    session,
    models,
    providers,
    providerPresets,
    agentAdapters,
    usageSummary,
    chatSessions,
    agentChatSessions,
    usageEvents,
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

function resolveDashboardResult<T>(
  result: PromiseSettledResult<{ data: T }>,
  previous: T,
): T {
  if (result.status === "fulfilled") {
    return result.value.data;
  }
  return previous;
}

async function resolveChatDashboardState(args: {
  activeChatSessionID: string;
  previousSessions: ChatSessionsResponse["data"];
  previousActiveSession: ChatSessionRecord | null;
  result: PromiseSettledResult<ChatSessionsResponse>;
}): Promise<{
  sessions: ChatSessionsResponse["data"];
  hasMore: boolean;
  activeChatSessionID: string;
  activeChatSession: ChatSessionRecord | null;
}> {
  if (args.result.status !== "fulfilled") {
    return {
      sessions: args.previousSessions,
      hasMore: false,
      activeChatSessionID: args.activeChatSessionID,
      activeChatSession: args.previousActiveSession,
    };
  }

  const sessions = args.result.value.data ?? [];
  const hasMore = args.result.value.has_more ?? false;
  const activeChatSessionID = sessions.some((entry) => entry.id === args.activeChatSessionID)
    ? args.activeChatSessionID
    : sessions[0]?.id ?? "";

  if (!activeChatSessionID) {
    return {
      sessions,
      hasMore,
      activeChatSessionID,
      activeChatSession: null,
    };
  }

  try {
    const sessionResult = await getChatSession(activeChatSessionID);
    return {
      sessions,
      hasMore,
      activeChatSessionID,
      activeChatSession: sessionResult.data,
    };
  } catch {
    return {
      sessions,
      hasMore,
      activeChatSessionID,
      activeChatSession: null,
    };
  }
}

async function resolveAgentChatDashboardState(args: {
  activeSessionID: string;
  previousSessions: AgentChatSessionsResponse["data"];
  previousActiveSession: AgentChatSessionRecord | null;
  result: PromiseSettledResult<AgentChatSessionsResponse>;
}): Promise<{
  sessions: AgentChatSessionsResponse["data"];
  activeSessionID: string;
  activeSession: AgentChatSessionRecord | null;
}> {
  if (args.result.status !== "fulfilled") {
    return {
      sessions: args.previousSessions,
      activeSessionID: args.activeSessionID,
      activeSession: args.previousActiveSession,
    };
  }

  const sessions = args.result.value.data ?? [];
  const activeSessionID = args.activeSessionID && sessions.some((entry) => entry.id === args.activeSessionID)
    ? args.activeSessionID
    : "";

  if (!activeSessionID) {
    return { sessions, activeSessionID, activeSession: null };
  }

  try {
    const sessionResult = await getAgentChatSession(activeSessionID);
    return { sessions, activeSessionID, activeSession: sessionResult.data };
  } catch (error) {
    if (!(error instanceof ApiError) || error.status !== 404) {
      return { sessions, activeSessionID, activeSession: args.previousActiveSession };
    }
    return { sessions, activeSessionID: "", activeSession: null };
  }
}
