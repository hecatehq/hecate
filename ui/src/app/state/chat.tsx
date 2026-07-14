// chat slice: the canonical chat-domain state machine. Owns
// session lists (agent + active session), composer state
// (message body, model, filters, system prompt), in-flight chat
// machinery (loading flag, streaming content, chat result,
// pending tool calls + thread), the chat-error cluster, queued
// chat messages, target routing (default target + per-session
// override map), workspace + external-agent selection, and the
// pagination state for agent chat sessions.
//
// Seven fields are persisted via `usePersistedState`; the rest
// are `useState` since they're in-flight or session-bound. One
// field (providerFilter) keeps a legacy useState + mount-read
// effect pattern — see the inline comment for the e2e timing
// reason. The slice exposes raw setters; the shim coordinators
// (submitChat, createChatSession, applyChatSession, the SSE
// event handler, …) compose these setters with cross-cut work
// like dispatching notice banners and updating the approvals
// slice.
//
// Five self-contained helpers live in the slice because they
// only touch chat-slice state: removeQueuedChatMessage,
// updateQueuedChatMessage, pruneQueuedChatMessagesForSessions,
// clearChatErrorState, setChatErrorState.

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";

import { applyOverride, CoordinatorOverridesContext } from "./coordinators/overrides";
import { ApiError, type ChatMessage } from "../../lib/api";
import type { MCPServerFormEntry } from "../../lib/mcp-server-form";
import { parseStoredJSON, parseStoredString, usePersistedState } from "../../lib/persistedState";
import type { ModelFilter } from "../../types/model";
import type { ProviderFilter } from "../../types/provider";
import type {
  ChatConfigOptionRecord,
  ChatResponse,
  ChatSessionRecord,
  ChatSessionsResponse,
} from "../../types/chat";
import {
  type ChatTarget,
  type HecateChatTarget,
  type QueuedChatMessage,
  chatToolsEnabledBySessionIDStorageKey,
  chatToolsEnabledStorageKey,
  parseChatTargetsBySessionID,
  parseChatToolsEnabledBySessionID,
  parseQueuedChatMessageList,
  parseStoredChatTarget,
  parseStoredChatToolsEnabled,
  queuedChatMessagesStorageKey,
  serializeChatTargetsBySessionID,
  serializeChatToolsEnabledBySessionID,
} from "./_shared";
import { humanizeChatError } from "../runtimeConsoleChatHelpers";

export type PendingToolCall = {
  id: string;
  name: string;
  arguments: string;
  result: string;
};

export type ChatState = {
  defaultChatTarget: ChatTarget;
  chatTargetBySessionID: Map<string, HecateChatTarget>;
  // User-default tools-enabled preference for new Hecate chats. The
  // per-session map below overrides this when a session has its own
  // pinned tools state; the default applies when neither the session
  // map nor a derived signal exists. Persisted under
  // `hecate.chatToolsEnabled`.
  defaultChatToolsEnabled: boolean;
  // Per-session tools-enabled override. A missing entry falls back to
  // `defaultChatToolsEnabled`. Populated by the user toggling the tools
  // pill in ChatSettingsPanel. Persisted under
  // `hecate.chatToolsEnabledBySessionID`.
  chatToolsEnabledBySessionID: Map<string, boolean>;
  agentAdapterID: string;
  agentConfigOptions: ChatConfigOptionRecord[];
  agentMCPServers: MCPServerFormEntry[];
  agentWorkspace: string;
  agentWorkspaceBranch: string;
  chatSessions: ChatSessionsResponse["data"];
  activeChatSessionID: string;
  activeChatSession: ChatSessionRecord | null;
  // Transient unsent composer text keyed by chat. This is deliberately
  // session-memory only: it prevents drafts crossing between chats without
  // turning browser storage into another chat or project authority.
  composerDraftsBySessionID: Map<string, string>;
  queuedChatMessages: QueuedChatMessage[];
  model: string;
  systemPrompt: string;
  chatLoading: boolean;
  chatCancelling: boolean;
  streamingContent: string | null;
  chatResult: ChatResponse | null;
  pendingToolCalls: PendingToolCall[];
  pendingThread: ChatMessage[] | null;
  chatError: string;
  chatErrorCode: string;
  chatErrorStatus: number | null;
  chatErrorAction: string;
  chatErrorRequestID: string;
  chatErrorTraceID: string;
  modelFilter: ModelFilter;
  providerFilter: ProviderFilter;
};

type SetStateAction<T> = T | ((prev: T) => T);
type Setter<T> = (next: SetStateAction<T>) => void;

export type ChatActions = {
  setDefaultChatTarget: Setter<ChatTarget>;
  setChatTargetBySessionID: Setter<Map<string, HecateChatTarget>>;
  setDefaultChatToolsEnabled: Setter<boolean>;
  setChatToolsEnabledBySessionID: Setter<Map<string, boolean>>;
  setAgentAdapterID: Setter<string>;
  setAgentConfigOptions: Setter<ChatConfigOptionRecord[]>;
  setAgentMCPServers: Setter<MCPServerFormEntry[]>;
  setAgentWorkspace: Setter<string>;
  setAgentWorkspaceBranch: Setter<string>;
  setChatSessions: Setter<ChatSessionsResponse["data"]>;
  setActiveChatSessionID: Setter<string>;
  setActiveChatSession: Setter<ChatSessionRecord | null>;
  setComposerDraftsBySessionID: Setter<Map<string, string>>;
  setQueuedChatMessages: Setter<QueuedChatMessage[]>;
  setModel: Setter<string>;
  setSystemPrompt: Setter<string>;
  setChatLoading: Setter<boolean>;
  setChatCancelling: Setter<boolean>;
  setStreamingContent: Setter<string | null>;
  setChatResult: Setter<ChatResponse | null>;
  setPendingToolCalls: Setter<PendingToolCall[]>;
  setPendingThread: Setter<ChatMessage[] | null>;
  setModelFilter: Setter<ModelFilter>;
  setProviderFilter: Setter<ProviderFilter>;
  // Raw chat-error setter — three shim call sites need to write
  // `chatError` directly (two with string literals for client-side
  // validation messages, one with a `(current) => current || msg`
  // updater that only writes if no earlier error landed). The
  // bundled `setChatErrorState` helper below wraps with
  // humanizeChatError + ApiError extraction; this setter doesn't.
  setChatError: Setter<string>;
  removeQueuedChatMessage: (id: string) => void;
  updateQueuedChatMessage: (id: string, content: string) => void;
  pruneQueuedChatMessagesForSessions: (sessionIDs: Iterable<string>) => void;
  clearChatErrorState: () => void;
  setChatErrorState: (error: unknown, fallback?: string) => void;
};

type ChatContextValue = {
  state: ChatState;
  actions: ChatActions;
};

const ChatContext = createContext<ChatContextValue | null>(null);

// Optional seed for tests. The slice's persisted fields are still
// initialized through usePersistedState (so localStorage-driven boot
// paths stay covered), but every other slice field can be preloaded
// with deterministic test data without exercising the dashboard loader.
export function ChatProvider({
  children,
  initialState,
}: {
  children: ReactNode;
  initialState?: Partial<ChatState>;
}) {
  const [defaultChatTarget, setDefaultChatTarget] = usePersistedState<ChatTarget>(
    "hecate.chatTarget",
    parseStoredChatTarget,
    initialState?.defaultChatTarget ?? "agent",
  );
  const [chatTargetBySessionID, setChatTargetBySessionID] = usePersistedState<
    Map<string, HecateChatTarget>
  >(
    "hecate.chatTargetBySessionID",
    parseStoredJSON(parseChatTargetsBySessionID),
    initialState?.chatTargetBySessionID ?? new Map(),
    { serialize: serializeChatTargetsBySessionID },
  );
  const [defaultChatToolsEnabled, setDefaultChatToolsEnabled] = usePersistedState<boolean>(
    chatToolsEnabledStorageKey,
    parseStoredChatToolsEnabled,
    initialState?.defaultChatToolsEnabled ?? true,
  );
  const [chatToolsEnabledBySessionID, setChatToolsEnabledBySessionID] = usePersistedState<
    Map<string, boolean>
  >(
    chatToolsEnabledBySessionIDStorageKey,
    parseStoredJSON(parseChatToolsEnabledBySessionID),
    initialState?.chatToolsEnabledBySessionID ?? new Map(),
    { serialize: serializeChatToolsEnabledBySessionID },
  );
  const [agentAdapterID, setAgentAdapterID] = usePersistedState(
    "hecate.agentAdapterID",
    parseStoredString,
    initialState?.agentAdapterID ?? "codex",
  );
  const [agentConfigOptions, setAgentConfigOptions] = useState<ChatConfigOptionRecord[]>(
    initialState?.agentConfigOptions ?? [],
  );
  const [agentMCPServers, setAgentMCPServers] = useState<MCPServerFormEntry[]>(
    initialState?.agentMCPServers ?? [],
  );
  const [agentWorkspace, setAgentWorkspace] = useState(initialState?.agentWorkspace ?? "");
  const [agentWorkspaceBranch, setAgentWorkspaceBranch] = useState(
    initialState?.agentWorkspaceBranch ?? "",
  );
  const [chatSessions, setChatSessions] = useState<ChatSessionsResponse["data"]>(
    initialState?.chatSessions ?? [],
  );
  const [activeChatSessionID, setActiveChatSessionID] = usePersistedState(
    "hecate.chatSessionID",
    parseStoredString,
    initialState?.activeChatSessionID ?? "",
    { shouldRemove: (v) => v === "" },
  );
  const [activeChatSession, setActiveChatSession] = useState<ChatSessionRecord | null>(
    initialState?.activeChatSession ?? null,
  );
  const [composerDraftsBySessionID, setComposerDraftsBySessionID] = useState(
    initialState?.composerDraftsBySessionID ?? new Map<string, string>(),
  );
  const [queuedChatMessages, setQueuedChatMessages] = usePersistedState<QueuedChatMessage[]>(
    queuedChatMessagesStorageKey,
    parseStoredJSON(parseQueuedChatMessageList),
    initialState?.queuedChatMessages ?? [],
    { shouldRemove: (v) => v.length === 0 },
  );
  const [model, setModel] = usePersistedState(
    "hecate.model",
    parseStoredString,
    initialState?.model ?? "",
    { shouldRemove: (v) => v === "" },
  );
  const [systemPrompt, setSystemPrompt] = usePersistedState(
    "hecate.systemPrompt",
    parseStoredString,
    initialState?.systemPrompt ?? "",
  );
  const [chatLoading, setChatLoading] = useState(initialState?.chatLoading ?? false);
  const [chatCancelling, setChatCancelling] = useState(initialState?.chatCancelling ?? false);
  const [streamingContent, setStreamingContent] = useState<string | null>(
    initialState?.streamingContent ?? null,
  );
  const [chatResult, setChatResult] = useState<ChatResponse | null>(
    initialState?.chatResult ?? null,
  );
  const [pendingToolCalls, setPendingToolCalls] = useState<PendingToolCall[]>(
    initialState?.pendingToolCalls ?? [],
  );
  const [pendingThread, setPendingThread] = useState<ChatMessage[] | null>(
    initialState?.pendingThread ?? null,
  );
  const [chatError, setChatError] = useState(initialState?.chatError ?? "");
  const [chatErrorCode, setChatErrorCode] = useState(initialState?.chatErrorCode ?? "");
  const [chatErrorStatus, setChatErrorStatus] = useState<number | null>(
    initialState?.chatErrorStatus ?? null,
  );
  const [chatErrorAction, setChatErrorAction] = useState(initialState?.chatErrorAction ?? "");
  const [chatErrorRequestID, setChatErrorRequestID] = useState(
    initialState?.chatErrorRequestID ?? "",
  );
  const [chatErrorTraceID, setChatErrorTraceID] = useState(initialState?.chatErrorTraceID ?? "");
  const [modelFilter, setModelFilter] = useState<ModelFilter>(initialState?.modelFilter ?? "all");
  // providerFilter is the lone holdout from the usePersistedState
  // migration. Three e2e scenarios broke when it was migrated — the
  // `test("...")` blocks that begin at chat.spec.ts:617 ("Hecate
  // Agent local-provider onboarding…"), :767 ("…tools on, tools
  // off, then tools on again…"), and :1288 ("selected-model
  // readiness can switch to the backend-suggested fallback model").
  // None of those tests set `hecate.providerFilter` directly; they
  // exercise the auto-default cascade in useRuntimeConsole that
  // reads providerFilter through a first-render closure and only
  // fires its setProviderFilter when it sees "auto" there. With
  // the legacy mount-read effect providerFilter is "auto" on
  // render 1 and transitions on render 2, which is the window the
  // cascade expects. Seeding the persisted value directly into the
  // lazy initializer (what usePersistedState does) shifts the
  // transition out from under that cascade.
  //
  // Restructuring the auto-default + scoped-validity effects so
  // they do not depend on render-cycle timing is separate cleanup.
  // Until that lands, providerFilter stays on the original useState
  // + mount-read + write-on-change pattern.
  const [providerFilter, setProviderFilter] = useState<ProviderFilter>(
    initialState?.providerFilter ?? "auto",
  );
  useEffect(() => {
    const stored = window.localStorage.getItem("hecate.providerFilter");
    if (stored) setProviderFilter(stored as ProviderFilter);
  }, []);
  useEffect(() => {
    window.localStorage.setItem("hecate.providerFilter", providerFilter);
  }, [providerFilter]);

  // Mirror setters to refs so the helpers below don't re-bind every
  // render. Helpers are exposed in the actions bag and consumers
  // (the shim) destructure them once; keeping them referentially
  // stable avoids invalidating downstream useCallback deps.
  const setQueuedChatMessagesRef = useRef(setQueuedChatMessages);
  setQueuedChatMessagesRef.current = setQueuedChatMessages;

  const removeQueuedChatMessage = useCallback((id: string) => {
    setQueuedChatMessagesRef.current((current) => current.filter((item) => item.id !== id));
  }, []);
  const updateQueuedChatMessage = useCallback((id: string, content: string) => {
    setQueuedChatMessagesRef.current((current) =>
      current.map((item) => (item.id === id ? { ...item, content } : item)),
    );
  }, []);
  const pruneQueuedChatMessagesForSessions = useCallback((sessionIDs: Iterable<string>) => {
    const valid = new Set(sessionIDs);
    setQueuedChatMessagesRef.current((current) =>
      current.filter((item) => valid.has(item.session_id)),
    );
  }, []);

  const clearChatErrorState = useCallback(() => {
    setChatError("");
    setChatErrorCode("");
    setChatErrorStatus(null);
    setChatErrorAction("");
    setChatErrorRequestID("");
    setChatErrorTraceID("");
  }, []);

  const setChatErrorState = useCallback((error: unknown, fallback = "unknown request error") => {
    const raw = error instanceof Error ? error.message : fallback;
    setChatError(humanizeChatError(raw));
    setChatErrorCode(error instanceof ApiError ? error.code : "");
    setChatErrorStatus(error instanceof ApiError ? error.status : null);
    setChatErrorAction(error instanceof ApiError ? error.operatorAction : "");
    setChatErrorRequestID(error instanceof ApiError ? error.requestId : "");
    setChatErrorTraceID(error instanceof ApiError ? error.traceId : "");
  }, []);

  const state = useMemo<ChatState>(
    () => ({
      defaultChatTarget,
      chatTargetBySessionID,
      defaultChatToolsEnabled,
      chatToolsEnabledBySessionID,
      agentAdapterID,
      agentConfigOptions,
      agentMCPServers,
      agentWorkspace,
      agentWorkspaceBranch,
      chatSessions,
      activeChatSessionID,
      activeChatSession,
      composerDraftsBySessionID,
      queuedChatMessages,
      model,
      systemPrompt,
      chatLoading,
      chatCancelling,
      streamingContent,
      chatResult,
      pendingToolCalls,
      pendingThread,
      chatError,
      chatErrorCode,
      chatErrorStatus,
      chatErrorAction,
      chatErrorRequestID,
      chatErrorTraceID,
      modelFilter,
      providerFilter,
    }),
    [
      defaultChatTarget,
      chatTargetBySessionID,
      defaultChatToolsEnabled,
      chatToolsEnabledBySessionID,
      agentAdapterID,
      agentConfigOptions,
      agentMCPServers,
      agentWorkspace,
      agentWorkspaceBranch,
      chatSessions,
      activeChatSessionID,
      activeChatSession,
      composerDraftsBySessionID,
      queuedChatMessages,
      model,
      systemPrompt,
      chatLoading,
      chatCancelling,
      streamingContent,
      chatResult,
      pendingToolCalls,
      pendingThread,
      chatError,
      chatErrorCode,
      chatErrorStatus,
      chatErrorAction,
      chatErrorRequestID,
      chatErrorTraceID,
      modelFilter,
      providerFilter,
    ],
  );

  const actions = useMemo<ChatActions>(
    () => ({
      setDefaultChatTarget,
      setChatTargetBySessionID,
      setDefaultChatToolsEnabled,
      setChatToolsEnabledBySessionID,
      setAgentAdapterID,
      setAgentConfigOptions,
      setAgentMCPServers,
      setAgentWorkspace,
      setAgentWorkspaceBranch,
      setChatSessions,
      setActiveChatSessionID,
      setActiveChatSession,
      setComposerDraftsBySessionID,
      setQueuedChatMessages,
      setModel,
      setSystemPrompt,
      setChatLoading,
      setChatCancelling,
      setStreamingContent,
      setChatResult,
      setPendingToolCalls,
      setPendingThread,
      setModelFilter,
      setProviderFilter,
      setChatError,
      removeQueuedChatMessage,
      updateQueuedChatMessage,
      pruneQueuedChatMessagesForSessions,
      clearChatErrorState,
      setChatErrorState,
    }),
    [
      setDefaultChatTarget,
      setChatTargetBySessionID,
      setDefaultChatToolsEnabled,
      setChatToolsEnabledBySessionID,
      setAgentAdapterID,
      setAgentConfigOptions,
      setAgentMCPServers,
      setAgentWorkspace,
      setChatSessions,
      setActiveChatSessionID,
      setQueuedChatMessages,
      setModel,
      setSystemPrompt,
      removeQueuedChatMessage,
      updateQueuedChatMessage,
      pruneQueuedChatMessagesForSessions,
      clearChatErrorState,
      setChatErrorState,
    ],
  );

  const value = useMemo(() => ({ state, actions }), [state, actions]);
  return <ChatContext.Provider value={value}>{children}</ChatContext.Provider>;
}

export function useChat(): ChatContextValue {
  const ctx = useContext(ChatContext);
  if (!ctx) {
    throw new Error("useChat must be used inside a <ChatProvider>");
  }
  const overrides = useContext(CoordinatorOverridesContext);
  return { state: ctx.state, actions: applyOverride(ctx.actions, overrides?.chatSlice) };
}
