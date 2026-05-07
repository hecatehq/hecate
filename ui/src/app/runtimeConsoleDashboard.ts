import {
  ApiError,
  getAccountSummary,
  getAgentAdapters,
  getAgentChatSession,
  getAgentChatSessions,
  getBudget,
  getChatSession,
  getChatSessions,
  getSettingsConfig,
  getHealth,
  getModels,
  getProviderPresets,
  getProviders,
  getRequestLedger,
  getRetentionRuns,
  getRuntimeStats,
  getSession,
} from "../lib/api";
import type {
  AccountSummaryResponse,
  AgentAdapterRecord,
  AgentChatSessionRecord,
  AgentChatSessionsResponse,
  BudgetStatusResponse,
  ChatSessionRecord,
  ChatSessionsResponse,
  ConfiguredStateResponse,
  HealthResponse,
  ModelResponse,
  ProviderPresetRecord,
  ProviderStatusResponse,
  RequestLedgerResponse,
  RetentionRunData,
  RuntimeStatsResponse,
  SessionResponse,
} from "../types/runtime";

export type SessionState = {
  label: string;
};

export type DashboardPreviousState = {
  providers: ProviderStatusResponse["data"];
  agentAdapters: AgentAdapterRecord[];
  budget: BudgetStatusResponse["data"] | null;
  accountSummary: AccountSummaryResponse["data"] | null;
  chatSessions: ChatSessionsResponse["data"];
  activeChatSession: ChatSessionRecord | null;
  agentChatSessions: AgentChatSessionsResponse["data"];
  activeAgentChatSession: AgentChatSessionRecord | null;
  requestLedger: RequestLedgerResponse["data"];
  settingsConfig: ConfiguredStateResponse["data"] | null;
  retentionRuns: RetentionRunData[];
  retentionLastRun: RetentionRunData | null;
};

export type DashboardSnapshot = {
  health: HealthResponse;
  sessionInfo: SessionResponse["data"] | null;
  models: ModelResponse["data"];
  providers: ProviderStatusResponse["data"];
  providerPresets: ProviderPresetRecord[];
  agentAdapters: AgentAdapterRecord[];
  budget: BudgetStatusResponse["data"] | null;
  accountSummary: AccountSummaryResponse["data"] | null;
  chatSessions: ChatSessionsResponse["data"];
  chatSessionsHasMore: boolean;
  activeChatSessionID: string;
  activeChatSession: ChatSessionRecord | null;
  agentChatSessions: AgentChatSessionsResponse["data"];
  activeAgentChatSessionID: string;
  activeAgentChatSession: AgentChatSessionRecord | null;
  requestLedger: RequestLedgerResponse["data"];
  settingsConfig: ConfiguredStateResponse["data"] | null;
  retentionRuns: RetentionRunData[];
  retentionLastRun: RetentionRunData | null;
  agentAdapterApprovalMode: string;
};

type DashboardResults = {
  health: PromiseSettledResult<HealthResponse>;
  session: PromiseSettledResult<SessionResponse>;
  models: PromiseSettledResult<ModelResponse>;
  providers: PromiseSettledResult<ProviderStatusResponse>;
  providerPresets: PromiseSettledResult<{ object: string; data: ProviderPresetRecord[] }>;
  agentAdapters: PromiseSettledResult<{ object: string; data: AgentAdapterRecord[] }>;
  budget: PromiseSettledResult<BudgetStatusResponse>;
  accountSummary: PromiseSettledResult<AccountSummaryResponse>;
  chatSessions: PromiseSettledResult<ChatSessionsResponse>;
  agentChatSessions: PromiseSettledResult<AgentChatSessionsResponse>;
  requestLedger: PromiseSettledResult<RequestLedgerResponse>;
  settingsConfig: PromiseSettledResult<ConfiguredStateResponse>;
  retentionRuns: PromiseSettledResult<{ object: string; data: RetentionRunData[] }>;
  runtimeStats: PromiseSettledResult<RuntimeStatsResponse>;
};

export async function resolveDashboardSnapshot(args: {
  activeChatSessionID: string;
  activeAgentChatSessionID: string;
  previous: DashboardPreviousState;
}): Promise<DashboardSnapshot> {
  const results = await loadDashboardResults();
  const health = requireFulfilledDashboardResult(results.health);
  const sessionInfo = results.session.status === "fulfilled" ? results.session.value.data : null;
  const models = resolveModelsResult(results.models);
  const providers = resolveDashboardResult(results.providers, args.previous.providers);
  const providerPresets = results.providerPresets.status === "fulfilled" ? results.providerPresets.value.data : [];
  const agentAdapters = resolveDashboardResult(results.agentAdapters, args.previous.agentAdapters);
  const budget = resolveDashboardResult(results.budget, args.previous.budget);
  const accountSummary = resolveDashboardResult(results.accountSummary, args.previous.accountSummary);
  const requestLedger = resolveDashboardResult(results.requestLedger, args.previous.requestLedger);
  const settingsConfig = resolveDashboardResult(results.settingsConfig, args.previous.settingsConfig);
  const retentionRuns = resolveDashboardResult(results.retentionRuns, args.previous.retentionRuns);
  const retentionLastRun = retentionRuns[0] ?? null;
  const agentAdapterApprovalMode = results.runtimeStats.status === "fulfilled"
    ? (results.runtimeStats.value.data.agent_adapter_approval_mode ?? "")
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
    budget,
    accountSummary,
    chatSessions: chatState.sessions,
    chatSessionsHasMore: chatState.hasMore,
    activeChatSessionID: chatState.activeChatSessionID,
    activeChatSession: chatState.activeChatSession,
    agentChatSessions: agentChatState.sessions,
    activeAgentChatSessionID: agentChatState.activeSessionID,
    activeAgentChatSession: agentChatState.activeSession,
    requestLedger,
    settingsConfig,
    retentionRuns,
    retentionLastRun,
    agentAdapterApprovalMode,
  };
}

export function deriveSessionState(_sessionInfo: SessionResponse["data"] | null): SessionState {
  return { label: "Local" };
}

async function loadDashboardResults(): Promise<DashboardResults> {
  const [health, session] = await Promise.allSettled([
    getHealth(),
    getSession(),
  ]);

  const initialReject = <T,>(): PromiseSettledResult<T> => ({ status: "rejected", reason: new Error("uninitialized") });
  let models: PromiseSettledResult<ModelResponse> = initialReject();
  let providers: PromiseSettledResult<ProviderStatusResponse> = initialReject();
  let providerPresets: PromiseSettledResult<{ object: string; data: ProviderPresetRecord[] }> = initialReject();
  let agentAdapters: PromiseSettledResult<{ object: string; data: AgentAdapterRecord[] }> = initialReject();
  let budget: PromiseSettledResult<BudgetStatusResponse> = initialReject();
  let accountSummary: PromiseSettledResult<AccountSummaryResponse> = initialReject();
  let chatSessions: PromiseSettledResult<ChatSessionsResponse> = initialReject();
  let agentChatSessions: PromiseSettledResult<AgentChatSessionsResponse> = initialReject();
  let requestLedger: PromiseSettledResult<RequestLedgerResponse> = initialReject();
  let settingsConfig: PromiseSettledResult<ConfiguredStateResponse> = initialReject();
  let retentionRuns: PromiseSettledResult<{ object: string; data: RetentionRunData[] }> = initialReject();
  let runtimeStats: PromiseSettledResult<RuntimeStatsResponse> = initialReject();

  await Promise.all([
    getProviderPresets().then(r => { providerPresets = { status: "fulfilled", value: r }; }, e => { providerPresets = { status: "rejected", reason: e }; }),
    getAgentAdapters().then(r => { agentAdapters = { status: "fulfilled", value: r }; }, e => { agentAdapters = { status: "rejected", reason: e }; }),
    getChatSessions(20).then(r => { chatSessions = { status: "fulfilled", value: r }; }, e => { chatSessions = { status: "rejected", reason: e }; }),
    getAgentChatSessions().then(r => { agentChatSessions = { status: "fulfilled", value: r }; }, e => { agentChatSessions = { status: "rejected", reason: e }; }),
    getModels().then(r => { models = { status: "fulfilled", value: r }; }, e => { models = { status: "rejected", reason: e }; }),
    getBudget("").then(r => { budget = { status: "fulfilled", value: r }; }, e => { budget = { status: "rejected", reason: e }; }),
    getAccountSummary("").then(r => { accountSummary = { status: "fulfilled", value: r }; }, e => { accountSummary = { status: "rejected", reason: e }; }),
    getRequestLedger(20).then(r => { requestLedger = { status: "fulfilled", value: r }; }, e => { requestLedger = { status: "rejected", reason: e }; }),
    getSettingsConfig().then(r => { settingsConfig = { status: "fulfilled", value: r }; }, e => { settingsConfig = { status: "rejected", reason: e }; }),
    getRetentionRuns(10).then(r => { retentionRuns = { status: "fulfilled", value: r }; }, e => { retentionRuns = { status: "rejected", reason: e }; }),
    getRuntimeStats().then(r => { runtimeStats = { status: "fulfilled", value: r }; }, e => { runtimeStats = { status: "rejected", reason: e }; }),
  ]);

  const configured = settingsConfig.status === "fulfilled" ? (settingsConfig.value.data?.providers ?? []) : [];
  if (configured.length > 0) {
    await getProviders().then(
      r => { providers = { status: "fulfilled", value: r }; },
      e => { providers = { status: "rejected", reason: e }; },
    );
  } else {
    providers = { status: "fulfilled", value: { object: "list", data: [] } as ProviderStatusResponse };
  }

  return {
    health,
    session,
    models,
    providers,
    providerPresets,
    agentAdapters,
    budget,
    accountSummary,
    chatSessions,
    agentChatSessions,
    requestLedger,
    settingsConfig,
    retentionRuns,
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
