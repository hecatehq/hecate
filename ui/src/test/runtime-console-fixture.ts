// Test fixture for the composed `{state, actions}` view-model
// shape. Production no longer composes a view-model — views consume
// slice + coordinator hooks directly. The fixture stays so per-view
// test files (and the composition regression suite) can keep the
// same setup ergonomics they had before the facade was retired.
//
// `createRuntimeConsoleFixture` returns the legacy state bag with
// sane defaults. `createRuntimeConsoleActions` returns no-op action
// stubs callers can spread + override. Both are consumed by
// `withRuntimeConsole` (in runtime-console-render.tsx), which fans
// the bag out into slice providers seeded with state and a
// coordinator-overrides context that intercepts action calls.

import type { ChatTarget, HecateChatTarget, QueuedChatMessage } from "../app/state/_shared";
import type { LocalProviderIssue } from "../lib/provider-issues";
import type { AgentAdapterHealthRecord, AgentAdapterRecord } from "../types/agent-adapter";
import type {
  ChatConfigOptionRecord,
  ChatGrantRecord,
  ChatResponse,
  ChatSessionRecord,
  ChatSessionSummaryRecord,
  PendingAgentApproval,
} from "../types/chat";
import type { ModelFilter, ModelRecord } from "../types/model";
import type {
  ConfiguredStateResponse,
  ProviderFilter,
  ProviderPresetRecord,
  ProviderRecord,
} from "../types/provider";
import type { CreateProjectPayload, ProjectDeleteRecord, ProjectRecord } from "../types/project";
import type { RetentionRunData } from "../types/retention";
import type { HealthResponse, RuntimeHeaders, SessionResponse } from "../types/runtime";
import type { UsageEventRecord, UsageSummaryResponse } from "../types/usage";
import type { PendingToolCall } from "../app/state/chat";
import type { NoticeState } from "../app/state/settings";
import type { ChatMessage } from "../lib/api";

type SessionState = { label: string };

export type RuntimeConsoleFixtureState = {
  usageSummary: UsageSummaryResponse["data"] | null;
  activeChatSession: ChatSessionRecord | null;
  activeChatSessionID: string;
  agentAdapterID: string;
  agentConfigOptions: ChatConfigOptionRecord[];
  agentAdapters: AgentAdapterRecord[];
  chatCancelling: boolean;
  chatSessions: ChatSessionSummaryRecord[];
  hecateRTKEnabled: boolean;
  hecateRTKAvailable: boolean;
  hecateRTKPath: string;
  newChatAgentID: string;
  agentWorkspace: string;
  agentWorkspaceBranch: string;
  chatError: string;
  chatErrorAction: string;
  chatErrorCode: string;
  chatErrorRequestID: string;
  chatErrorStatus: number | null;
  chatErrorTraceID: string;
  chatLoading: boolean;
  streamingContent: string | null;
  chatResult: ChatResponse | null;
  chatTarget: ChatTarget;
  pendingToolCalls: PendingToolCall[];
  queuedChatMessages: QueuedChatMessage[];
  cloudModels: ModelRecord[];
  cloudProviders: ProviderRecord[];
  settingsConfig: ConfiguredStateResponse["data"] | null;
  settingsError: string;
  copiedCommand: string;
  error: string;
  health: HealthResponse | null;
  healthyCloudProviders: number;
  healthyLocalProviders: number;
  healthyProviders: number;
  loading: boolean;
  localModels: ModelRecord[];
  localProviderIssues: LocalProviderIssue[];
  localProviders: ProviderRecord[];
  message: string;
  systemPrompt: string;
  model: string;
  modelFilter: ModelFilter;
  models: ModelRecord[];
  notice: NoticeState | null;
  sessionInfo: SessionResponse["data"] | null;
  session: SessionState;
  providerFilter: ProviderFilter;
  providerScopedModels: ModelRecord[];
  providers: ProviderRecord[];
  providerPresets: ProviderPresetRecord[];
  projects: ProjectRecord[];
  activeProjectID: string;
  usageEvents: UsageEventRecord[];
  retentionError: string;
  retentionLastRun: RetentionRunData | null;
  retentionLoading: boolean;
  retentionRuns: RetentionRunData[];
  retentionSubsystems: string;
  runtimeHeaders: RuntimeHeaders | null;
  visibleModels: ModelRecord[];
  pendingApprovalsBySessionID: Map<string, PendingAgentApproval[]>;
  chatGrants: ChatGrantRecord[];
  chatGrantsLoading: boolean;
  chatGrantsError: string;
  agentAdapterApprovalMode: string;
  agentAdapterHealthByID: Map<string, AgentAdapterHealthRecord>;
  agentAdapterHealthLoadingByID: Map<string, true>;
  pendingThread: ChatMessage[] | null;
  chatTargetBySessionID: Map<string, HecateChatTarget>;
  defaultChatTarget: ChatTarget;
  defaultChatToolsEnabled: boolean;
  chatToolsEnabledBySessionID: Map<string, boolean>;
};

export function createRuntimeConsoleFixture(
  overrides: Partial<RuntimeConsoleFixtureState> = {},
): RuntimeConsoleFixtureState {
  return {
    usageSummary: null,
    activeChatSession: null,
    activeChatSessionID: "",
    agentAdapterID: "codex",
    agentConfigOptions: [],
    agentAdapters: [],
    chatCancelling: false,
    chatSessions: [],
    hecateRTKEnabled: false,
    hecateRTKAvailable: false,
    hecateRTKPath: "",
    newChatAgentID: "hecate",
    agentWorkspace: "",
    agentWorkspaceBranch: "",
    chatError: "",
    chatErrorAction: "",
    chatErrorCode: "",
    chatErrorRequestID: "",
    chatErrorStatus: null,
    chatErrorTraceID: "",
    chatLoading: false,
    streamingContent: null,
    chatResult: null,
    chatTarget: "agent",
    pendingToolCalls: [],
    queuedChatMessages: [],
    cloudModels: [],
    cloudProviders: [],
    settingsConfig: null,
    settingsError: "",
    copiedCommand: "",
    error: "",
    health: { status: "ok", time: "2026-04-21T10:00:00Z" },
    healthyCloudProviders: 0,
    healthyLocalProviders: 0,
    healthyProviders: 0,
    loading: false,
    localModels: [],
    localProviderIssues: [],
    localProviders: [],
    message: "Say hello",
    systemPrompt: "",
    model: "gpt-4o-mini",
    modelFilter: "all",
    models: [],
    notice: null,
    sessionInfo: null,
    session: { label: "Local" },
    providerFilter: "auto",
    providerPresets: [],
    projects: [],
    activeProjectID: "",
    providerScopedModels: [],
    providers: [],
    usageEvents: [],
    retentionError: "",
    retentionLastRun: null,
    retentionLoading: false,
    retentionRuns: [],
    retentionSubsystems: "",
    runtimeHeaders: null,
    visibleModels: [],
    pendingApprovalsBySessionID: new Map(),
    chatGrants: [],
    chatGrantsLoading: false,
    chatGrantsError: "",
    agentAdapterApprovalMode: "",
    agentAdapterHealthByID: new Map(),
    agentAdapterHealthLoadingByID: new Map(),
    pendingThread: null,
    chatTargetBySessionID: new Map(),
    defaultChatTarget: "agent",
    defaultChatToolsEnabled: true,
    chatToolsEnabledBySessionID: new Map(),
    ...overrides,
  };
}

export type RuntimeConsoleFixtureActions = {
  copyCommand: (command: string) => Promise<void>;
  cancelAgentChat: () => Promise<void>;
  compactChatSession: (sessionID?: string) => Promise<boolean>;
  chooseAgentWorkspace: () => Promise<boolean>;
  createChatSession: (options?: {
    agentID?: string;
    projectID?: string;
    provider?: string;
    model?: string;
    title?: string;
    draft?: string;
  }) => Promise<void>;
  deleteChatSession: (id: string) => Promise<void>;
  deletePolicyRule: (id: string) => Promise<void>;
  loadDashboard: () => Promise<void>;
  loadRetentionRuns: () => Promise<void>;
  renameChatSession: (id: string, title: string) => Promise<void>;
  setAgentAdapterID: (id: string) => void;
  setNewChatAgent: (id: string) => void;
  setAgentWorkspace: (workspace: string) => void;
  setChatTarget: (target: ChatTarget) => void;
  setChatToolsEnabled: (enabled: boolean) => void;
  setMessage: (value: string) => void;
  removeQueuedChatMessage: (id: string) => void;
  updateQueuedChatMessage: (id: string, content: string) => void;
  setSystemPrompt: (prompt: string) => void;
  setModel: (model: string) => void;
  setModelFilter: (filter: ModelFilter) => void;
  setProviderFilter: (filter: ProviderFilter) => void;
  refreshProviders: () => Promise<void>;
  setRetentionSubsystems: (value: string) => void;
  submitChat: (event: unknown) => Promise<void>;
  submitToolResults: () => Promise<void>;
  runRetention: () => Promise<void>;
  selectChatSession: (id: string) => Promise<boolean>;
  startNewChat: () => void;
  upsertPolicyRule: (payload: unknown) => Promise<void>;
  updateToolResult: (index: number, result: string) => void;
  setProviderAPIKey: (id: string, key: string) => Promise<void>;
  createProvider: (params: unknown, options?: unknown) => Promise<void>;
  deleteProvider: (id: string) => Promise<void>;
  setProviderBaseURL: (id: string, baseURL: string) => Promise<void>;
  setProviderName: (id: string, name: string) => Promise<void>;
  setProviderCustomName: (id: string, customName: string) => Promise<void>;
  setProviderAccountID: (id: string, accountID: string) => Promise<void>;
  setActiveProjectID: (id: string) => void;
  loadProjects: () => Promise<void>;
  selectProject: (id: string) => Promise<void>;
  createProject: (payload: CreateProjectPayload) => Promise<ProjectRecord | null>;
  renameProject: (id: string, name: string) => Promise<void>;
  deleteProject: (id: string) => Promise<ProjectDeleteRecord | null>;
  getChatApproval: (sessionID: string, approvalID: string) => Promise<unknown>;
  listChatMessageFiles: (sessionID: string, messageID: string) => Promise<unknown[]>;
  getChatWorkspaceDiff: (sessionID: string) => Promise<unknown>;
  getChatWorkspaceFiles: (sessionID: string) => Promise<unknown>;
  getChatWorkspaceFileDiff: (sessionID: string, path: string) => Promise<unknown>;
  revertChatWorkspaceFiles: (sessionID: string, paths: string[]) => Promise<unknown>;
  getChatMessageFileDiff: (sessionID: string, messageID: string, path: string) => Promise<unknown>;
  revertChatMessageFiles: (
    sessionID: string,
    messageID: string,
    paths: string[],
  ) => Promise<boolean>;
  resolveTaskApproval: (taskID: string, approvalID: string, decision: unknown) => Promise<boolean>;
  resolveChatApproval: (
    sessionID: string,
    approvalID: string,
    decision: unknown,
  ) => Promise<boolean>;
  cancelChatApproval: (sessionID: string, approvalID: string) => Promise<boolean>;
  listChatGrants: (filter?: unknown) => Promise<void>;
  deleteChatGrant: (grantID: string) => Promise<boolean>;
  setChatConfigOption: (
    sessionID: string,
    configID: string,
    value: string | boolean,
  ) => Promise<boolean>;
  setHecateRTKEnabled: (enabled: boolean) => Promise<boolean>;
  probeAgentAdapter: (adapterID: string) => Promise<unknown>;
  authenticateAgentAdapter: (adapterID: string) => Promise<boolean>;
  logoutAgentAdapter: (adapterID: string) => Promise<boolean>;
  dismissNotice: () => void;
};

export function createRuntimeConsoleActions(): RuntimeConsoleFixtureActions {
  return {
    copyCommand: async () => undefined,
    cancelAgentChat: async () => undefined,
    compactChatSession: async () => true,
    chooseAgentWorkspace: async () => true,
    createChatSession: async () => undefined,
    deleteChatSession: async () => undefined,
    deletePolicyRule: async () => undefined,
    loadDashboard: async () => undefined,
    loadRetentionRuns: async () => undefined,
    renameChatSession: async () => undefined,
    setAgentAdapterID: () => undefined,
    setNewChatAgent: () => undefined,
    setAgentWorkspace: () => undefined,
    setChatTarget: () => undefined,
    setChatToolsEnabled: () => undefined,
    setMessage: () => undefined,
    removeQueuedChatMessage: () => undefined,
    updateQueuedChatMessage: () => undefined,
    setSystemPrompt: () => undefined,
    setModel: () => undefined,
    setModelFilter: () => undefined,
    setProviderFilter: () => undefined,
    refreshProviders: async () => undefined,
    setRetentionSubsystems: () => undefined,
    submitChat: async () => undefined,
    submitToolResults: async () => undefined,
    runRetention: async () => undefined,
    selectChatSession: async () => true,
    startNewChat: () => undefined,
    upsertPolicyRule: async () => undefined,
    updateToolResult: () => undefined,
    setProviderAPIKey: async () => undefined,
    createProvider: async () => undefined,
    deleteProvider: async () => undefined,
    setProviderBaseURL: async () => undefined,
    setProviderName: async () => undefined,
    setProviderCustomName: async () => undefined,
    setProviderAccountID: async () => undefined,
    setActiveProjectID: () => undefined,
    loadProjects: async () => undefined,
    selectProject: async () => undefined,
    createProject: async () => null,
    renameProject: async () => undefined,
    deleteProject: async () => ({
      project_id: "",
      chat_sessions_deleted: 0,
      project_work_rows_deleted: 0,
      project_skills_deleted: 0,
      memory_entries_deleted: 0,
      memory_candidates_deleted: 0,
    }),
    getChatApproval: async () => null,
    listChatMessageFiles: async () => [],
    getChatWorkspaceDiff: async () => ({
      workspace: "",
      diff_stat: "",
      diff: "",
      has_changes: false,
      files: [],
    }),
    getChatWorkspaceFiles: async () => ({
      workspace: "",
      files: [],
    }),
    getChatWorkspaceFileDiff: async () => null,
    revertChatWorkspaceFiles: async () => ({
      workspace: "",
      diff_stat: "",
      diff: "",
      has_changes: false,
      files: [],
    }),
    getChatMessageFileDiff: async () => null,
    revertChatMessageFiles: async () => true,
    resolveTaskApproval: async () => true,
    resolveChatApproval: async () => true,
    cancelChatApproval: async () => true,
    listChatGrants: async () => undefined,
    deleteChatGrant: async () => true,
    setChatConfigOption: async () => true,
    setHecateRTKEnabled: async () => true,
    probeAgentAdapter: async () => null,
    authenticateAgentAdapter: async () => true,
    logoutAgentAdapter: async () => true,
    dismissNotice: () => undefined,
  };
}
