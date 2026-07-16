// chat slice: the canonical chat-domain state machine. Owns
// session lists (agent + active session), composer state
// (message body, model, filters, system prompt), in-flight chat
// machinery (loading flag, streaming content, chat result,
// pending tool calls + thread), the chat-error cluster, queued
// chat messages, target routing (default target + per-session
// override map), workspace + external-agent selection, and the
// pagination state for agent chat sessions.
//
// Most persisted fields use `usePersistedState`; the queued-message
// store uses per-item records so tabs cannot overwrite one another's
// work. The rest are `useState` since they're in-flight or session-bound. One
// field (providerFilter) keeps a legacy useState + mount-read
// effect pattern — see the inline comment for the e2e timing
// reason. The slice exposes raw setters; the shim coordinators
// (submitChat, createChatSession, applyChatSession, the SSE
// event handler, …) compose these setters with cross-cut work
// like dispatching notice banners and updating the approvals
// slice.
//
// Seven self-contained helpers live in the slice because they
// only touch chat-slice state: removeQueuedChatMessage,
// retryQueuedChatMessage, updateQueuedChatMessage,
// currentQueuedChatMessage, fenceChatSessionsMissingFromAuthoritativeSnapshot,
// clearChatErrorState, setChatErrorState.

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useLayoutEffect,
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
  ChatSessionSummaryRecord,
  ChatSessionsResponse,
  ChatWorkspaceMode,
} from "../../types/chat";
import {
  type ChatTarget,
  type HecateChatTarget,
  type PendingChatAttachment,
  type QueuedChatMessage,
  chatToolsEnabledBySessionIDStorageKey,
  chatToolsEnabledStorageKey,
  parseChatTargetsBySessionID,
  parseChatToolsEnabledBySessionID,
  parseStoredChatTarget,
  parseStoredChatToolsEnabled,
  serializeChatTargetsBySessionID,
  serializeChatToolsEnabledBySessionID,
} from "./_shared";
import { useQueuedChatMessageStore, type QueuedChatEnqueueResult } from "./queuedChatStorage";
import { humanizeChatError } from "../runtimeConsoleChatHelpers";

export type PendingToolCall = {
  id: string;
  name: string;
  arguments: string;
  result: string;
};

export type ComposerDraftScope = {
  projectID: string;
  agentID: string;
  provider: string;
  model: string;
  workspace: string;
};

export type RecoverableComposerDraft = {
  id: number;
  content: string;
  scope: ComposerDraftScope;
};

export function composerDraftScope({
  projectID = "",
  agentID = "hecate",
  provider = "",
  model = "",
  workspace = "",
}: Partial<ComposerDraftScope> = {}): ComposerDraftScope {
  const normalizedAgentID = agentID.trim() || "hecate";
  const hecateOwned = normalizedAgentID === "hecate";
  return {
    projectID: projectID.trim(),
    agentID: normalizedAgentID,
    provider: hecateOwned ? provider.trim() || "auto" : "",
    model: hecateOwned ? model.trim() : "",
    workspace: workspace.trim(),
  };
}

export function composerDraftScopesMatch(
  left: ComposerDraftScope,
  right: ComposerDraftScope,
): boolean {
  return (
    left.projectID === right.projectID &&
    left.agentID === right.agentID &&
    left.provider === right.provider &&
    left.model === right.model &&
    left.workspace === right.workspace
  );
}

export type ChatTurnKind = "direct_model" | "hecate_task" | "external_agent";

export type ChatCancellationOwner = Readonly<{
  token: number;
  sessionID: string;
  turnGeneration: number | null;
}>;

export type ChatStopFencePhase = "requesting" | "accepted" | "settled";

export type ChatStopFence = {
  token: number;
  sessionID: string;
  turnGeneration: number | null;
  phase: ChatStopFencePhase;
  terminalSeenBeforeAcceptance: boolean;
  settlementDeadline: number;
  settlement: Promise<void>;
  resolveSettlement: () => void;
  previousFence: ChatStopFence | null;
};

export type ChatSessionSnapshotSource =
  | { kind: "turn"; turnGeneration: number }
  | { kind: "stop_read"; stopToken: number }
  | { kind: "unscoped" };

const chatStopSettlementTimeoutMS = 2_000;

export type ChatStopFenceSessionSnapshot = ChatSessionRecord | ChatSessionSummaryRecord;

function chatSessionSnapshotIsBusy(session: ChatStopFenceSessionSnapshot): boolean {
  const busy = (status?: string) =>
    status === "queued" || status === "running" || status === "awaiting_approval";
  if (busy(session.status)) return true;
  if ("segments" in session && (session.segments ?? []).some((segment) => busy(segment.status))) {
    return true;
  }
  return ("messages" in session ? (session.messages ?? []) : []).some(
    (message) => message.role === "assistant" && busy(message.status),
  );
}

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
  // Operator default for new Hecate chats without a project-owned default.
  // Individual sessions persist their own immutable execution posture once a
  // task-backed segment starts.
  agentWorkspaceMode: ChatWorkspaceMode;
  chatSessions: ChatSessionsResponse["data"];
  activeChatSessionID: string;
  activeChatSession: ChatSessionRecord | null;
  // Session-memory-only ownership for unsent text. These maps prevent a
  // draft or a rejected submission from crossing into another chat while the
  // server remains the transcript authority.
  composerDraftsBySessionID: Map<string, string>;
  savedComposerDraftsBySessionID: Map<string, string[]>;
  recoverableComposerDraft: RecoverableComposerDraft | null;
  activeRecoverableComposerDraftID: number | null;
  pendingChatAttachments: PendingChatAttachment[];
  // A tokenized reservation held while a destructive chat-ownership mutation
  // is awaiting its backend outcome. The visible state lets the UI disable
  // draft admission; the token itself stays private to the slice.
  chatOwnershipMutationInFlight: boolean;
  // Number of in-memory File drafts owned by the currently submitting
  // attachment turn. The visible composer clears those drafts before the
  // first async boundary, but this count keeps unload protection and queue
  // admission active until the turn settles.
  chatAttachmentTurnDraftCount: number;
  queuedChatMessages: QueuedChatMessage[];
  model: string;
  systemPrompt: string;
  chatCreating: boolean;
  chatTurnSessionID: string;
  chatTurnActive: boolean;
  chatTurnKind: ChatTurnKind | "";
  chatTurnCancellationAvailable: boolean;
  chatLoading: boolean;
  chatCancelling: boolean;
  chatCancellingSessionID: string;
  chatCancellingTurnKind: ChatTurnKind | "";
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
  beginChatCreation: () => number | null;
  completeChatCreation: (generation: number) => void;
  isChatCreationActive: () => boolean;
  beginChatTurn: (sessionID: string, kind: ChatTurnKind) => number | null;
  bindChatTurnSession: (generation: number, sessionID: string) => void;
  registerChatTurnPreAdmissionCancel: (
    generation: number,
    cancel: (owner: ChatCancellationOwner) => void,
  ) => boolean;
  startChatTurnAdmission: (generation: number) => boolean;
  confirmChatTurnServerCancellation: (generation: number) => boolean;
  cancelChatTurnBeforeAdmission: (owner: ChatCancellationOwner) => boolean;
  chatTurnServerCancellationReady: (owner: ChatCancellationOwner) => boolean;
  completeChatTurn: (generation: number) => void;
  isChatTurnActive: () => boolean;
  isCurrentChatTurn: (generation: number) => boolean;
  getActiveChatTurnSessionID: () => string;
  beginActiveChatTransition: () => number;
  captureActiveChatTransition: () => number | null;
  completeActiveChatTransition: (generation: number) => void;
  isCurrentActiveChatTransition: (generation: number) => boolean;
  setDefaultChatTarget: Setter<ChatTarget>;
  setChatTargetBySessionID: Setter<Map<string, HecateChatTarget>>;
  setDefaultChatToolsEnabled: Setter<boolean>;
  setChatToolsEnabledBySessionID: Setter<Map<string, boolean>>;
  setAgentAdapterID: Setter<string>;
  setAgentConfigOptions: Setter<ChatConfigOptionRecord[]>;
  setAgentMCPServers: Setter<MCPServerFormEntry[]>;
  setAgentWorkspace: Setter<string>;
  setAgentWorkspaceBranch: Setter<string>;
  setAgentWorkspaceMode: Setter<ChatWorkspaceMode>;
  setChatSessions: Setter<ChatSessionsResponse["data"]>;
  setActiveChatSessionID: Setter<string>;
  setActiveChatSession: Setter<ChatSessionRecord | null>;
  setComposerDraftsBySessionID: Setter<Map<string, string>>;
  setSavedComposerDraftsBySessionID: Setter<Map<string, string[]>>;
  saveRecoverableComposerDraft: (draft: {
    id?: number;
    content: string;
    scope: ComposerDraftScope;
  }) => number;
  setRecoverableComposerDraft: Setter<RecoverableComposerDraft | null>;
  setActiveRecoverableComposerDraftID: Setter<number | null>;
  setPendingChatAttachments: Setter<PendingChatAttachment[]>;
  setQueuedChatMessages: Setter<QueuedChatMessage[]>;
  deleteQueuedChatMessagesForSession: (sessionID: string) => boolean;
  enqueueQueuedChatMessage: (message: QueuedChatMessage) => QueuedChatEnqueueResult;
  setModel: Setter<string>;
  setSystemPrompt: Setter<string>;
  setChatLoading: Setter<boolean>;
  setChatCancelling: Setter<boolean>;
  setChatCancellingSessionID: Setter<string>;
  setChatCancellingTurnKind: Setter<ChatTurnKind | "">;
  beginChatCancellation: (sessionID: string) => ChatCancellationOwner | null;
  finishChatCancellation: (owner: ChatCancellationOwner) => boolean;
  hasChatCancellationOwner: () => boolean;
  chatCancellationOwnsSession: (sessionID: string) => boolean;
  currentChatCancellationEpoch: (sessionID: string) => number;
  waitForChatCancellationRelease: (sessionID: string) => Promise<void>;
  beginChatStopFence: (owner: ChatCancellationOwner) => ChatStopFence;
  clearChatStopFence: (fence: ChatStopFence, restorePrevious?: boolean) => ChatStopFence | null;
  acceptChatStopFence: (fence: ChatStopFence) => boolean;
  getChatStopFence: (sessionID: string) => ChatStopFence | null;
  stopReadTokenAtRequestStart: (sessionID: string) => number | null;
  chatStopFenceAllowsOmission: (sessionID: string, stopReadToken: number | null) => boolean;
  chatStopFenceAllowsSnapshot: (
    session: ChatStopFenceSessionSnapshot,
    source?: ChatSessionSnapshotSource,
  ) => boolean;
  chatStopFenceForTurnSettlement: (
    sessionID: string,
    turnGeneration: number,
  ) => ChatStopFence | null;
  chatStopFenceSuppressesApproval: (sessionID: string, turnGeneration: number) => boolean;
  chatStopFenceProtectsSession: (sessionID: string) => boolean;
  clearSettledChatStopFenceForNewTurn: (sessionID: string) => void;
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
  retryQueuedChatMessage: (id: string) => void;
  updateQueuedChatMessage: (id: string, content: string) => void;
  currentQueuedChatMessage: (id: string) => QueuedChatMessage | undefined;
  hasDurableQueuedChatSubmittingFence: (queued: QueuedChatMessage) => boolean;
  fenceChatSessionsMissingFromAuthoritativeSnapshot: (
    sessionIDs: Iterable<string>,
    stopReadTokensBySessionID?: ReadonlyMap<string, number>,
  ) => boolean;
  clearChatErrorState: () => void;
  setChatErrorState: (error: unknown, fallback?: string) => void;
  claimChatSessionIntent: () => number;
  currentChatSessionIntent: () => number;
  isCurrentChatSessionIntent: (intent: number) => boolean;
  tryBeginChatSessionCreate: () => number | null;
  finishChatSessionCreate: (intent: number) => void;
  isChatSessionCreateInFlight: () => boolean;
  currentChatResetGeneration: () => number;
  beginChatRequestOperation: (sessionID?: string) => number;
  bindChatRequestOperationSession: (token: number, sessionID: string) => boolean;
  finishChatRequestOperation: (token: number) => boolean;
  isCurrentChatRequestOperation: (token: number) => boolean;
  hasPendingChatAttachments: () => boolean;
  beginChatOwnershipMutation: () => number | null;
  finishChatOwnershipMutation: (token: number) => void;
  isChatOwnershipMutationInFlight: () => boolean;
  beginChatAttachmentTurn: (sessionID: string, draftCount: number) => number | null;
  bindChatAttachmentTurn: (token: number, sessionID: string) => boolean;
  finishChatAttachmentTurn: (token: number) => void;
  hasChatAttachmentTurn: () => boolean;
  chatAttachmentTurnSessionID: () => string;
  currentActiveChatSessionID: () => string;
  tombstoneDeletedChatSession: (sessionID: string) => void;
  isChatSessionDeleted: (sessionID: string, projectID?: string) => boolean;
  chatOwnershipMutationBlockReason: () => string;
  fenceDeletedChatProject: (projectID: string) => boolean;
  fenceAllChatSessionsDeleted: () => boolean;
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
  const [agentWorkspaceMode, setAgentWorkspaceMode] = usePersistedState<ChatWorkspaceMode>(
    "hecate.agentWorkspaceMode",
    (raw) => (raw === "persistent" || raw === "ephemeral" || raw === "in_place" ? raw : null),
    initialState?.agentWorkspaceMode ?? "persistent",
  );
  // Deletion tombstones fence every summary writer, including dashboard and
  // detail responses that began before a delete completed.
  const deletedChatSessionIDsRef = useRef(new Set<string>());
  const deletedChatProjectIDsRef = useRef(new Set<string>());
  const [chatSessions, setChatSessionsState] = useState<ChatSessionsResponse["data"]>(
    initialState?.chatSessions ?? [],
  );
  const setChatSessions = useCallback<Setter<ChatSessionsResponse["data"]>>((next) => {
    setChatSessionsState((current) => {
      const resolved =
        typeof next === "function"
          ? (next as (prev: ChatSessionsResponse["data"]) => ChatSessionsResponse["data"])(current)
          : next;
      return resolved.filter(
        (session) =>
          !deletedChatSessionIDsRef.current.has(session.id) &&
          !deletedChatProjectIDsRef.current.has((session.project_id ?? "").trim()),
      );
    });
  }, []);
  const [activeChatSessionID, setActiveChatSessionIDState] = usePersistedState(
    "hecate.chatSessionID",
    parseStoredString,
    initialState?.activeChatSessionID ?? "",
    { shouldRemove: (v) => v === "" },
  );
  const activeChatSessionIDRef = useRef(activeChatSessionID);
  activeChatSessionIDRef.current = activeChatSessionID;
  const setActiveChatSessionID = useCallback(
    (next: SetStateAction<string>) => {
      const resolved =
        typeof next === "function"
          ? (next as (prev: string) => string)(activeChatSessionIDRef.current)
          : next;
      activeChatSessionIDRef.current = resolved;
      setActiveChatSessionIDState(resolved);
    },
    [setActiveChatSessionIDState],
  );
  const [activeChatSession, setActiveChatSession] = useState<ChatSessionRecord | null>(
    initialState?.activeChatSession ?? null,
  );
  const [composerDraftsBySessionID, setComposerDraftsBySessionID] = useState(
    initialState?.composerDraftsBySessionID ?? new Map<string, string>(),
  );
  const [savedComposerDraftsBySessionID, setSavedComposerDraftsBySessionID] = useState(
    initialState?.savedComposerDraftsBySessionID ?? new Map<string, string[]>(),
  );
  const [recoverableComposerDraft, setRecoverableComposerDraft] = useState(
    initialState?.recoverableComposerDraft ?? null,
  );
  const [activeRecoverableComposerDraftID, setActiveRecoverableComposerDraftID] = useState<
    number | null
  >(initialState?.activeRecoverableComposerDraftID ?? null);
  const chatOwnershipMutationOwnerRef = useRef<number | null>(null);
  const nextChatOwnershipMutationTokenRef = useRef(0);
  const [chatOwnershipMutationInFlight, setChatOwnershipMutationInFlight] = useState(false);
  const [pendingChatAttachments, setPendingChatAttachmentsState] = useState<
    PendingChatAttachment[]
  >(initialState?.pendingChatAttachments ?? []);
  const pendingChatAttachmentsRef = useRef(pendingChatAttachments);
  useLayoutEffect(() => {
    pendingChatAttachmentsRef.current = pendingChatAttachments;
  }, [pendingChatAttachments]);
  const setPendingChatAttachments = useCallback<Setter<PendingChatAttachment[]>>((next) => {
    const current = pendingChatAttachmentsRef.current;
    const resolved =
      typeof next === "function"
        ? (next as (prev: PendingChatAttachment[]) => PendingChatAttachment[])(current)
        : next;
    // Reservation acquisition proves the draft list was empty. Deny any
    // later growth synchronously so a picker event cannot be replayed after
    // the destructive request settles and silently retarget its Files.
    if (chatOwnershipMutationOwnerRef.current !== null && resolved.length > current.length) {
      return;
    }
    pendingChatAttachmentsRef.current = resolved;
    setPendingChatAttachmentsState(resolved);
  }, []);
  const [chatAttachmentTurnDraftCount, setChatAttachmentTurnDraftCount] = useState(
    initialState?.chatAttachmentTurnDraftCount ?? 0,
  );
  useEffect(() => {
    if (pendingChatAttachments.length === 0 && chatAttachmentTurnDraftCount === 0) return;
    const warnAboutUnsavedAttachmentDrafts = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      // Browsers show their own confirmation copy, but still require
      // returnValue for compatibility with older beforeunload handling.
      event.returnValue = "File attachment drafts have not been sent.";
    };
    window.addEventListener("beforeunload", warnAboutUnsavedAttachmentDrafts);
    return () => window.removeEventListener("beforeunload", warnAboutUnsavedAttachmentDrafts);
  }, [chatAttachmentTurnDraftCount, pendingChatAttachments.length]);
  const {
    messages: queuedChatMessages,
    setMessages: setQueuedChatMessages,
    enqueueMessage: enqueueQueuedChatMessage,
    hasDurableSubmittingFence: hasDurableQueuedChatSubmittingFence,
    deleteWhere: deleteQueuedChatMessagesWhere,
    deleteSession: deleteQueuedChatMessagesForSession,
    deleteProjectWhere: deleteQueuedChatProjectMessagesWhere,
    clear: clearQueuedChatMessages,
  } = useQueuedChatMessageStore(initialState?.queuedChatMessages ?? []);
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
  const [chatCreating, setChatCreating] = useState(initialState?.chatCreating ?? false);
  const [chatTurnSessionID, setChatTurnSessionID] = useState(initialState?.chatTurnSessionID ?? "");
  const [chatTurnActive, setChatTurnActive] = useState(initialState?.chatTurnActive ?? false);
  const [chatTurnKind, setChatTurnKind] = useState<ChatTurnKind | "">(
    initialState?.chatTurnKind ?? "",
  );
  const [chatTurnCancellationAvailable, setChatTurnCancellationAvailable] = useState(
    initialState?.chatTurnCancellationAvailable ?? false,
  );
  const [chatLoading, setChatLoading] = useState(initialState?.chatLoading ?? false);
  const [chatCancelling, setChatCancelling] = useState(initialState?.chatCancelling ?? false);
  const [chatCancellingSessionID, setChatCancellingSessionID] = useState(
    initialState?.chatCancellingSessionID ??
      (initialState?.chatCancelling ? (initialState.activeChatSessionID ?? "") : ""),
  );
  const [chatCancellingTurnKind, setChatCancellingTurnKind] = useState<ChatTurnKind | "">(
    initialState?.chatCancellingTurnKind ??
      (initialState?.chatCancelling ? (initialState.chatTurnKind ?? "") : ""),
  );
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
  const hasUnpersistedQueuedMutation = queuedChatMessages.some(
    (queued) => queued.delivery_storage_failed === true,
  );
  useEffect(() => {
    const hasUnadmittedComposerPrompt =
      chatErrorCode === "chat.queue_storage_unavailable" ||
      chatErrorCode === "chat.queue_reset_observed" ||
      chatErrorCode === "chat.queue_item_conflict" ||
      chatErrorCode === "chat.queue_session_deleted" ||
      chatErrorCode === "chat.queue_project_deleted";
    if (!hasUnpersistedQueuedMutation && !hasUnadmittedComposerPrompt) return;
    const warnAboutUnpersistedQueue = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = "Queued chat changes could not be saved in browser storage.";
    };
    window.addEventListener("beforeunload", warnAboutUnpersistedQueue);
    return () => window.removeEventListener("beforeunload", warnAboutUnpersistedQueue);
  }, [chatErrorCode, hasUnpersistedQueuedMutation]);
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

  // Composer/session arbitration from the canonical chat state machine. Keep
  // these transition generations alongside the attachment/deletion fences:
  // the former owns draft projection, while the latter owns destructive and
  // durable-resource boundaries.
  const activeChatTransitionGenerationRef = useRef(0);
  const pendingActiveChatTransitionRef = useRef<number | null>(null);
  const chatCreationGenerationRef = useRef(0);
  const activeChatCreationRef = useRef<number | null>(null);
  const chatTurnGenerationRef = useRef(0);
  const activeChatTurnRef = useRef<{
    generation: number;
    sessionID: string;
    kind: ChatTurnKind;
    phase: "pre_admission" | "admitting" | "server_cancellable" | "cancelled";
    cancelBeforeAdmission: ((owner: ChatCancellationOwner) => void) | null;
  } | null>(null);
  const recoverableComposerDraftGenerationRef = useRef(recoverableComposerDraft?.id ?? 0);
  const chatSessionIntentRef = useRef(0);
  const chatSessionCreateOwnerRef = useRef<number | null>(null);
  const chatResetGenerationRef = useRef(0);
  const chatRequestOperationOwnerRef = useRef<{
    token: number;
    sessionID: string;
  } | null>(null);
  const nextChatRequestOperationTokenRef = useRef(0);
  const nextChatCancellationTokenRef = useRef(0);
  const chatCancellationOwnerRef = useRef<ChatCancellationOwner | null>(null);
  const chatCancellationEpochBySessionIDRef = useRef(new Map<string, number>());
  const chatCancellationWaitersBySessionIDRef = useRef(new Map<string, Set<() => void>>());
  const chatStopFencesBySessionIDRef = useRef(new Map<string, ChatStopFence>());

  const advanceChatCancellationEpoch = useCallback((sessionID: string) => {
    const id = sessionID.trim();
    const next = (chatCancellationEpochBySessionIDRef.current.get(id) ?? 0) + 1;
    chatCancellationEpochBySessionIDRef.current.set(id, next);
    return next;
  }, []);
  const currentChatCancellationEpoch = useCallback(
    (sessionID: string) => chatCancellationEpochBySessionIDRef.current.get(sessionID.trim()) ?? 0,
    [],
  );
  const chatCancellationOwnsSession = useCallback(
    (sessionID: string) => chatCancellationOwnerRef.current?.sessionID === sessionID.trim(),
    [],
  );
  const releaseChatCancellationWaiters = useCallback((sessionID: string) => {
    const id = sessionID.trim();
    const waiters = chatCancellationWaitersBySessionIDRef.current.get(id);
    if (!waiters) return;
    chatCancellationWaitersBySessionIDRef.current.delete(id);
    for (const resolve of waiters) resolve();
  }, []);
  const waitForChatCancellationRelease = useCallback((sessionID: string): Promise<void> => {
    const id = sessionID.trim();
    if (chatCancellationOwnerRef.current?.sessionID !== id) return Promise.resolve();
    return new Promise((resolve) => {
      const waiters = chatCancellationWaitersBySessionIDRef.current.get(id) ?? new Set();
      waiters.add(resolve);
      chatCancellationWaitersBySessionIDRef.current.set(id, waiters);
    });
  }, []);

  const beginChatStopFence = useCallback((owner: ChatCancellationOwner): ChatStopFence => {
    let resolveSettlement: () => void = () => undefined;
    const settlement = new Promise<void>((resolve) => {
      resolveSettlement = resolve;
    });
    const previousFence = chatStopFencesBySessionIDRef.current.get(owner.sessionID) ?? null;
    const fence: ChatStopFence = {
      token: owner.token,
      sessionID: owner.sessionID,
      turnGeneration: owner.turnGeneration,
      phase: "requesting",
      terminalSeenBeforeAcceptance: false,
      settlementDeadline:
        previousFence && previousFence.phase !== "requesting"
          ? Date.now() + chatStopSettlementTimeoutMS
          : 0,
      settlement,
      resolveSettlement,
      previousFence,
    };
    chatStopFencesBySessionIDRef.current.set(owner.sessionID, fence);
    return fence;
  }, []);

  const clearChatStopFence = useCallback(
    (fence: ChatStopFence, restorePrevious = false): ChatStopFence | null => {
      const current = chatStopFencesBySessionIDRef.current.get(fence.sessionID);
      if (current !== fence) return null;
      if (restorePrevious && fence.previousFence) {
        const previousFence = fence.previousFence;
        if (previousFence.phase === "accepted") {
          previousFence.settlementDeadline = Math.max(
            previousFence.settlementDeadline,
            Date.now() + chatStopSettlementTimeoutMS,
          );
        }
        chatStopFencesBySessionIDRef.current.set(fence.sessionID, previousFence);
        fence.resolveSettlement();
        return previousFence;
      }
      chatStopFencesBySessionIDRef.current.delete(fence.sessionID);
      fence.previousFence?.resolveSettlement();
      fence.resolveSettlement();
      return null;
    },
    [],
  );

  const settleChatStopFence = useCallback((fence: ChatStopFence) => {
    if (chatStopFencesBySessionIDRef.current.get(fence.sessionID) !== fence) return;
    if (fence.phase === "requesting") {
      fence.terminalSeenBeforeAcceptance = true;
      return;
    }
    if (fence.phase === "settled") return;
    fence.phase = "settled";
    fence.resolveSettlement();
  }, []);

  const acceptChatStopFence = useCallback(
    (fence: ChatStopFence): boolean => {
      if (
        chatStopFencesBySessionIDRef.current.get(fence.sessionID) !== fence ||
        fence.phase !== "requesting"
      ) {
        return false;
      }
      fence.previousFence?.resolveSettlement();
      fence.previousFence = null;
      fence.phase = "accepted";
      fence.settlementDeadline = Date.now() + chatStopSettlementTimeoutMS;
      if (fence.terminalSeenBeforeAcceptance) settleChatStopFence(fence);
      return true;
    },
    [settleChatStopFence],
  );

  const getChatStopFence = useCallback(
    (sessionID: string) => chatStopFencesBySessionIDRef.current.get(sessionID.trim()) ?? null,
    [],
  );

  const stopReadTokenAtRequestStart = useCallback((sessionID: string): number | null => {
    const fence = chatStopFencesBySessionIDRef.current.get(sessionID.trim());
    if (!fence) return null;
    if (fence.phase !== "requesting") return fence.token;
    return fence.previousFence && fence.previousFence.phase !== "requesting"
      ? fence.previousFence.token
      : null;
  }, []);

  const chatStopFenceAllowsOmission = useCallback(
    (sessionID: string, stopReadToken: number | null): boolean => {
      if (stopReadToken === null) return false;
      const fence = chatStopFencesBySessionIDRef.current.get(sessionID.trim());
      const protectingFence =
        fence?.phase === "requesting" && fence.previousFence?.phase !== "requesting"
          ? fence.previousFence
          : fence;
      return Boolean(
        protectingFence &&
        protectingFence.phase !== "requesting" &&
        protectingFence.token === stopReadToken,
      );
    },
    [],
  );

  const acceptedChatStopFenceForTurn = useCallback(
    (sessionID: string, turnGeneration: number): ChatStopFence | null => {
      const fence = chatStopFencesBySessionIDRef.current.get(sessionID.trim());
      const acceptedFence =
        fence?.phase === "requesting" && fence.previousFence?.phase !== "requesting"
          ? fence.previousFence
          : fence;
      if (
        !acceptedFence ||
        acceptedFence.phase === "requesting" ||
        acceptedFence.turnGeneration !== turnGeneration
      ) {
        return null;
      }
      return acceptedFence;
    },
    [],
  );

  const chatStopFenceSuppressesApproval = useCallback(
    (sessionID: string, turnGeneration: number) =>
      acceptedChatStopFenceForTurn(sessionID, turnGeneration) !== null,
    [acceptedChatStopFenceForTurn],
  );

  const chatStopFenceProtectsSession = useCallback((sessionID: string) => {
    const fence = chatStopFencesBySessionIDRef.current.get(sessionID.trim());
    if (!fence) return false;
    if (fence.phase !== "requesting") return true;
    return Boolean(fence.previousFence && fence.previousFence.phase !== "requesting");
  }, []);

  const clearSettledChatStopFenceForNewTurn = useCallback(
    (sessionID: string) => {
      const fence = chatStopFencesBySessionIDRef.current.get(sessionID.trim());
      if (fence?.phase === "settled") clearChatStopFence(fence);
    },
    [clearChatStopFence],
  );

  const chatStopFenceAllowsSnapshot = useCallback(
    (
      session: ChatStopFenceSessionSnapshot,
      source: ChatSessionSnapshotSource = { kind: "unscoped" },
    ): boolean => {
      const fence = chatStopFencesBySessionIDRef.current.get(session.id);
      if (!fence) return true;
      const terminal = !chatSessionSnapshotIsBusy(session);
      const matchingTurn =
        source.kind === "turn" &&
        fence.turnGeneration !== null &&
        source.turnGeneration === fence.turnGeneration;
      const matchingStopRead = source.kind === "stop_read" && source.stopToken === fence.token;
      if (fence.phase === "requesting") {
        const previousFence = fence.previousFence;
        if (previousFence && previousFence.phase !== "requesting") {
          const matchingPreviousTurn =
            source.kind === "turn" &&
            previousFence.turnGeneration !== null &&
            source.turnGeneration === previousFence.turnGeneration;
          const matchingPreviousRead =
            source.kind === "stop_read" && source.stopToken === previousFence.token;
          if (!terminal) return false;
          if (
            !matchingTurn &&
            !matchingStopRead &&
            !matchingPreviousTurn &&
            !matchingPreviousRead
          ) {
            return false;
          }
          fence.terminalSeenBeforeAcceptance = true;
          previousFence.phase = "settled";
          previousFence.resolveSettlement();
          fence.resolveSettlement();
          return true;
        }
        if (terminal && matchingTurn) fence.terminalSeenBeforeAcceptance = true;
        return true;
      }
      if (!terminal) return false;
      if (!matchingTurn && !matchingStopRead) return false;
      settleChatStopFence(fence);
      return true;
    },
    [settleChatStopFence],
  );

  const chatStopFenceForTurnSettlement = useCallback(
    (sessionID: string, turnGeneration: number): ChatStopFence | null => {
      const fence = chatStopFencesBySessionIDRef.current.get(sessionID.trim());
      if (
        fence?.phase === "requesting" &&
        fence.turnGeneration === turnGeneration &&
        fence.previousFence?.phase !== "requesting" &&
        fence.previousFence?.turnGeneration === turnGeneration
      ) {
        return fence;
      }
      return acceptedChatStopFenceForTurn(sessionID, turnGeneration);
    },
    [acceptedChatStopFenceForTurn],
  );

  const beginChatCancellation = useCallback(
    (sessionID: string) => {
      const activeTurn = activeChatTurnRef.current;
      const id = sessionID.trim();
      if (chatCancellationOwnerRef.current || (!id && activeTurn?.sessionID !== "")) return null;
      nextChatCancellationTokenRef.current += 1;
      const owner: ChatCancellationOwner = {
        token: nextChatCancellationTokenRef.current,
        sessionID: id,
        turnGeneration: activeTurn?.sessionID === id ? activeTurn.generation : null,
      };
      advanceChatCancellationEpoch(id);
      chatCancellationOwnerRef.current = owner;
      setChatCancellingSessionID(id);
      setChatCancellingTurnKind(activeTurn?.sessionID === id ? activeTurn.kind : "");
      setChatCancelling(true);
      return owner;
    },
    [advanceChatCancellationEpoch],
  );
  const finishChatCancellation = useCallback(
    (owner: ChatCancellationOwner) => {
      const activeOwner = chatCancellationOwnerRef.current;
      if (activeOwner?.token !== owner.token) return false;
      chatCancellationOwnerRef.current = null;
      setChatCancellingSessionID("");
      setChatCancellingTurnKind("");
      setChatCancelling(false);
      releaseChatCancellationWaiters(activeOwner.sessionID);
      if (owner.sessionID !== activeOwner.sessionID) {
        releaseChatCancellationWaiters(owner.sessionID);
      }
      return true;
    },
    [releaseChatCancellationWaiters],
  );
  const hasChatCancellationOwner = useCallback(() => chatCancellationOwnerRef.current !== null, []);

  const beginChatCreation = useCallback(() => {
    if (
      activeChatCreationRef.current !== null ||
      chatSessionCreateOwnerRef.current !== null ||
      chatOwnershipMutationOwnerRef.current !== null
    ) {
      return null;
    }
    chatCreationGenerationRef.current += 1;
    activeChatCreationRef.current = chatCreationGenerationRef.current;
    chatSessionIntentRef.current += 1;
    chatSessionCreateOwnerRef.current = chatSessionIntentRef.current;
    setChatCreating(true);
    return activeChatCreationRef.current;
  }, []);
  const completeChatCreation = useCallback((generation: number) => {
    if (activeChatCreationRef.current !== generation) return;
    activeChatCreationRef.current = null;
    chatSessionCreateOwnerRef.current = null;
    setChatCreating(false);
  }, []);
  const isChatCreationActive = useCallback(() => activeChatCreationRef.current !== null, []);
  const beginChatTurn = useCallback((sessionID: string, kind: ChatTurnKind) => {
    if (activeChatTurnRef.current !== null) return null;
    chatTurnGenerationRef.current += 1;
    const generation = chatTurnGenerationRef.current;
    activeChatTurnRef.current = {
      generation,
      sessionID,
      kind,
      phase: "pre_admission",
      cancelBeforeAdmission: null,
    };
    setChatTurnSessionID(sessionID);
    setChatTurnActive(true);
    setChatTurnKind(kind);
    setChatTurnCancellationAvailable(false);
    setChatLoading(true);
    return generation;
  }, []);
  const bindChatTurnSession = useCallback(
    (generation: number, sessionID: string) => {
      const activeTurn = activeChatTurnRef.current;
      if (activeTurn?.generation !== generation) return;
      const id = sessionID.trim();
      activeChatTurnRef.current = { ...activeTurn, sessionID: id };
      setChatTurnSessionID(id);
      const cancellationOwner = chatCancellationOwnerRef.current;
      if (
        cancellationOwner?.turnGeneration === generation &&
        cancellationOwner.sessionID === "" &&
        id
      ) {
        chatCancellationOwnerRef.current = { ...cancellationOwner, sessionID: id };
        advanceChatCancellationEpoch(id);
        setChatCancellingSessionID(id);
      }
    },
    [advanceChatCancellationEpoch],
  );
  const registerChatTurnPreAdmissionCancel = useCallback(
    (generation: number, cancel: (owner: ChatCancellationOwner) => void) => {
      const activeTurn = activeChatTurnRef.current;
      if (activeTurn?.generation !== generation || activeTurn.phase !== "pre_admission") {
        return false;
      }
      activeChatTurnRef.current = { ...activeTurn, cancelBeforeAdmission: cancel };
      setChatTurnCancellationAvailable(true);
      return true;
    },
    [],
  );
  const startChatTurnAdmission = useCallback((generation: number) => {
    const activeTurn = activeChatTurnRef.current;
    if (activeTurn?.generation !== generation || activeTurn.phase !== "pre_admission") {
      return false;
    }
    activeChatTurnRef.current = {
      ...activeTurn,
      phase: "admitting",
      cancelBeforeAdmission: null,
    };
    setChatTurnCancellationAvailable(false);
    return true;
  }, []);
  const confirmChatTurnServerCancellation = useCallback((generation: number) => {
    const activeTurn = activeChatTurnRef.current;
    if (
      activeTurn?.generation !== generation ||
      (activeTurn.phase !== "admitting" && activeTurn.phase !== "server_cancellable")
    ) {
      return false;
    }
    activeChatTurnRef.current = { ...activeTurn, phase: "server_cancellable" };
    setChatTurnCancellationAvailable(true);
    return true;
  }, []);
  const cancelChatTurnBeforeAdmission = useCallback((owner: ChatCancellationOwner) => {
    const activeTurn = activeChatTurnRef.current;
    const cancellationOwner = chatCancellationOwnerRef.current;
    if (
      cancellationOwner?.token !== owner.token ||
      owner.turnGeneration === null ||
      activeTurn?.generation !== owner.turnGeneration ||
      activeTurn.sessionID !== owner.sessionID ||
      activeTurn.phase !== "pre_admission" ||
      !activeTurn.cancelBeforeAdmission
    ) {
      return false;
    }
    const cancel = activeTurn.cancelBeforeAdmission;
    activeChatTurnRef.current = {
      ...activeTurn,
      phase: "cancelled",
      cancelBeforeAdmission: null,
    };
    setChatTurnCancellationAvailable(false);
    cancel(owner);
    return true;
  }, []);
  const completeChatTurn = useCallback((generation: number) => {
    if (activeChatTurnRef.current?.generation !== generation) return;
    activeChatTurnRef.current = null;
    setChatTurnSessionID("");
    setChatTurnActive(false);
    setChatTurnKind("");
    setChatTurnCancellationAvailable(false);
    setChatLoading(false);
  }, []);
  const isChatTurnActive = useCallback(() => activeChatTurnRef.current !== null, []);
  const isCurrentChatTurn = useCallback(
    (generation: number) => activeChatTurnRef.current?.generation === generation,
    [],
  );
  const getActiveChatTurnSessionID = useCallback(
    () => activeChatTurnRef.current?.sessionID ?? "",
    [],
  );
  const chatTurnServerCancellationReady = useCallback((owner: ChatCancellationOwner) => {
    if (chatCancellationOwnerRef.current?.token !== owner.token || !owner.sessionID) return false;
    const activeTurn = activeChatTurnRef.current;
    if (!activeTurn) return owner.turnGeneration === null;
    return (
      owner.turnGeneration === activeTurn.generation &&
      activeTurn.sessionID === owner.sessionID &&
      activeTurn.phase === "server_cancellable"
    );
  }, []);
  const saveRecoverableComposerDraft = useCallback(
    ({ id, content, scope }: { id?: number; content: string; scope: ComposerDraftScope }) => {
      const nextID = id ?? recoverableComposerDraftGenerationRef.current + 1;
      recoverableComposerDraftGenerationRef.current = Math.max(
        recoverableComposerDraftGenerationRef.current,
        nextID,
      );
      setRecoverableComposerDraft({ id: nextID, content, scope });
      return nextID;
    },
    [],
  );
  const beginActiveChatTransition = useCallback(() => {
    activeChatTransitionGenerationRef.current += 1;
    chatSessionIntentRef.current += 1;
    pendingActiveChatTransitionRef.current = activeChatTransitionGenerationRef.current;
    return pendingActiveChatTransitionRef.current;
  }, []);
  const captureActiveChatTransition = useCallback(
    () =>
      pendingActiveChatTransitionRef.current === null
        ? activeChatTransitionGenerationRef.current
        : null,
    [],
  );
  const completeActiveChatTransition = useCallback((generation: number) => {
    if (
      activeChatTransitionGenerationRef.current === generation &&
      pendingActiveChatTransitionRef.current === generation
    ) {
      pendingActiveChatTransitionRef.current = null;
    }
  }, []);
  const isCurrentActiveChatTransition = useCallback(
    (generation: number) => activeChatTransitionGenerationRef.current === generation,
    [],
  );

  // Mirror setters to refs so the helpers below don't re-bind every
  // render. Helpers are exposed in the actions bag and consumers
  // (the shim) destructure them once; keeping them referentially
  // stable avoids invalidating downstream useCallback deps.
  const setQueuedChatMessagesRef = useRef(setQueuedChatMessages);
  setQueuedChatMessagesRef.current = setQueuedChatMessages;
  // Coordinator hooks are mounted by more than one workspace surface.
  // Keep session-operation ownership in the slice so those hook instances
  // share one latest-intent fence instead of racing through private refs.
  const chatSessionsRef = useRef(chatSessions);
  chatSessionsRef.current = chatSessions;
  const activeChatSessionRef = useRef(activeChatSession);
  activeChatSessionRef.current = activeChatSession;
  const queuedChatMessagesRef = useRef(queuedChatMessages);
  queuedChatMessagesRef.current = queuedChatMessages;
  const chatTargetBySessionIDRef = useRef(chatTargetBySessionID);
  chatTargetBySessionIDRef.current = chatTargetBySessionID;
  const chatToolsEnabledBySessionIDRef = useRef(chatToolsEnabledBySessionID);
  chatToolsEnabledBySessionIDRef.current = chatToolsEnabledBySessionID;
  const chatAttachmentTurnOwnerRef = useRef<{
    token: number;
    sessionID: string;
  } | null>(null);
  const nextChatAttachmentTurnTokenRef = useRef(0);

  const removeQueuedChatMessage = useCallback((id: string) => {
    setQueuedChatMessagesRef.current((current) =>
      current.filter((item) => item.id !== id || item.delivery_state === "submitting"),
    );
  }, []);
  const retryQueuedChatMessage = useCallback((id: string) => {
    setQueuedChatMessagesRef.current((current) =>
      current.map((item) =>
        item.id === id && item.delivery_state === "retryable"
          ? {
              ...item,
              delivery_state: undefined,
              delivery_baseline_message_ids: undefined,
              delivery_error_code: undefined,
              delivery_idempotency_keyed: undefined,
            }
          : item,
      ),
    );
  }, []);
  const updateQueuedChatMessage = useCallback((id: string, content: string) => {
    setQueuedChatMessagesRef.current((current) =>
      current.map((item) =>
        item.id === id &&
        item.delivery_state !== "submitting" &&
        item.delivery_state !== "reconcile_required"
          ? { ...item, content }
          : item,
      ),
    );
  }, []);
  const currentQueuedChatMessage = useCallback(
    (id: string) => queuedChatMessagesRef.current.find((item) => item.id === id),
    [],
  );
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

  const claimChatSessionIntent = useCallback(() => {
    chatSessionIntentRef.current += 1;
    activeChatTransitionGenerationRef.current += 1;
    pendingActiveChatTransitionRef.current = null;
    return chatSessionIntentRef.current;
  }, []);
  const currentChatSessionIntent = useCallback(() => chatSessionIntentRef.current, []);
  const isCurrentChatSessionIntent = useCallback(
    (intent: number) => chatSessionIntentRef.current === intent,
    [],
  );
  const tryBeginChatSessionCreate = useCallback(() => {
    if (
      chatSessionCreateOwnerRef.current !== null ||
      activeChatCreationRef.current !== null ||
      chatOwnershipMutationOwnerRef.current !== null
    ) {
      return null;
    }
    chatSessionIntentRef.current += 1;
    chatSessionCreateOwnerRef.current = chatSessionIntentRef.current;
    activeChatCreationRef.current = chatSessionCreateOwnerRef.current;
    setChatCreating(true);
    return chatSessionCreateOwnerRef.current;
  }, []);
  const finishChatSessionCreate = useCallback((intent: number) => {
    if (chatSessionCreateOwnerRef.current === intent) {
      chatSessionCreateOwnerRef.current = null;
      activeChatCreationRef.current = null;
      setChatCreating(false);
    }
  }, []);
  const isChatSessionCreateInFlight = useCallback(
    () => chatSessionCreateOwnerRef.current !== null || activeChatCreationRef.current !== null,
    [],
  );
  const currentChatResetGeneration = useCallback(() => chatResetGenerationRef.current, []);
  const beginChatRequestOperation = useCallback((sessionID = "") => {
    nextChatRequestOperationTokenRef.current += 1;
    chatRequestOperationOwnerRef.current = {
      token: nextChatRequestOperationTokenRef.current,
      sessionID: sessionID.trim(),
    };
    return chatRequestOperationOwnerRef.current.token;
  }, []);
  const bindChatRequestOperationSession = useCallback((token: number, sessionID: string) => {
    const owner = chatRequestOperationOwnerRef.current;
    if (owner?.token !== token) return false;
    owner.sessionID = sessionID.trim();
    return true;
  }, []);
  const finishChatRequestOperation = useCallback((token: number) => {
    if (chatRequestOperationOwnerRef.current?.token !== token) return false;
    chatRequestOperationOwnerRef.current = null;
    return true;
  }, []);
  const isCurrentChatRequestOperation = useCallback(
    (token: number) => chatRequestOperationOwnerRef.current?.token === token,
    [],
  );
  const hasPendingChatAttachments = useCallback(
    () => pendingChatAttachmentsRef.current.length > 0,
    [],
  );
  const beginChatOwnershipMutation = useCallback(() => {
    if (
      chatOwnershipMutationOwnerRef.current !== null ||
      chatSessionCreateOwnerRef.current !== null ||
      activeChatCreationRef.current !== null ||
      chatAttachmentTurnOwnerRef.current !== null ||
      pendingChatAttachmentsRef.current.length > 0
    ) {
      return null;
    }
    nextChatOwnershipMutationTokenRef.current += 1;
    const token = nextChatOwnershipMutationTokenRef.current;
    chatOwnershipMutationOwnerRef.current = token;
    setChatOwnershipMutationInFlight(true);
    // Invalidate any selection/create response that began before the
    // destructive mutation reserved chat ownership.
    chatSessionIntentRef.current += 1;
    activeChatTransitionGenerationRef.current += 1;
    pendingActiveChatTransitionRef.current = null;
    return token;
  }, []);
  const finishChatOwnershipMutation = useCallback((token: number) => {
    if (chatOwnershipMutationOwnerRef.current !== token) return;
    chatOwnershipMutationOwnerRef.current = null;
    setChatOwnershipMutationInFlight(false);
  }, []);
  const isChatOwnershipMutationInFlight = useCallback(
    () => chatOwnershipMutationOwnerRef.current !== null,
    [],
  );
  const beginChatAttachmentTurn = useCallback((sessionID: string, draftCount: number) => {
    const count = Math.max(0, Math.trunc(draftCount));
    if (
      count === 0 ||
      chatAttachmentTurnOwnerRef.current ||
      chatOwnershipMutationOwnerRef.current !== null
    ) {
      return null;
    }
    nextChatAttachmentTurnTokenRef.current += 1;
    const token = nextChatAttachmentTurnTokenRef.current;
    chatAttachmentTurnOwnerRef.current = { token, sessionID: sessionID.trim() };
    setChatAttachmentTurnDraftCount(count);
    chatSessionIntentRef.current += 1;
    return token;
  }, []);
  const bindChatAttachmentTurn = useCallback((token: number, sessionID: string) => {
    const owner = chatAttachmentTurnOwnerRef.current;
    const id = sessionID.trim();
    if (!owner || owner.token !== token || !id) return false;
    owner.sessionID = id;
    return true;
  }, []);
  const finishChatAttachmentTurn = useCallback((token: number) => {
    if (chatAttachmentTurnOwnerRef.current?.token === token) {
      chatAttachmentTurnOwnerRef.current = null;
      setChatAttachmentTurnDraftCount(0);
      chatSessionIntentRef.current += 1;
    }
  }, []);
  const hasChatAttachmentTurn = useCallback(() => chatAttachmentTurnOwnerRef.current !== null, []);
  const chatAttachmentTurnSessionID = useCallback(
    () => chatAttachmentTurnOwnerRef.current?.sessionID ?? "",
    [],
  );
  const currentActiveChatSessionID = useCallback(() => activeChatSessionIDRef.current, []);
  const tombstoneDeletedChatSession = useCallback((sessionID: string) => {
    const id = sessionID.trim();
    if (id) deletedChatSessionIDsRef.current.add(id);
  }, []);
  const isChatSessionDeleted = useCallback(
    (sessionID?: string, projectID?: string) =>
      deletedChatSessionIDsRef.current.has((sessionID ?? "").trim()) ||
      deletedChatProjectIDsRef.current.has((projectID ?? "").trim()),
    [],
  );
  const chatOwnershipMutationBlockReason = useCallback(() => {
    if (chatOwnershipMutationOwnerRef.current !== null) {
      return "Wait for the current chat ownership change to finish before changing chat ownership.";
    }
    if (chatSessionCreateOwnerRef.current !== null) {
      return "Wait for the new chat to finish creating before changing or deleting chat ownership.";
    }
    if (activeChatCreationRef.current !== null) {
      return "Wait for the new chat to finish creating before changing or deleting chat ownership.";
    }
    if (chatAttachmentTurnOwnerRef.current) {
      return "Wait for the attachment response before changing or deleting chat ownership.";
    }
    if (pendingChatAttachmentsRef.current.length > 0) {
      return "Remove attached files before changing or deleting chat ownership.";
    }
    return "";
  }, []);

  const clearDeletedSessionState = useCallback(
    (
      sessionIDs: Set<string>,
      clearActive: boolean,
      queuedBelongsToDeletion: (message: QueuedChatMessage) => boolean = () => false,
      deletedProjectID = "",
      durableSessionFences = false,
    ) => {
      const queuedDeletionPredicate = (message: QueuedChatMessage) => {
        const shouldDelete = sessionIDs.has(message.session_id) || queuedBelongsToDeletion(message);
        if (shouldDelete) sessionIDs.add(message.session_id);
        return shouldDelete;
      };
      let queueCleanupSucceeded = true;
      if (durableSessionFences) {
        // Establish process-local fences before touching fallible browser
        // storage. Dashboard omission is authoritative even when there is no
        // queued row yet or cleanup must be retried.
        for (const sessionID of sessionIDs) {
          deletedChatSessionIDsRef.current.add(sessionID);
          queueCleanupSucceeded =
            deleteQueuedChatMessagesForSession(sessionID) && queueCleanupSucceeded;
        }
      } else {
        queueCleanupSucceeded = deletedProjectID
          ? deleteQueuedChatProjectMessagesWhere(deletedProjectID, queuedDeletionPredicate)
          : deleteQueuedChatMessagesWhere(queuedDeletionPredicate);
      }
      for (const sessionID of sessionIDs) {
        deletedChatSessionIDsRef.current.add(sessionID);
        const stopFence = chatStopFencesBySessionIDRef.current.get(sessionID);
        if (stopFence) clearChatStopFence(stopFence);
      }
      // Removing an unrelated session must not invalidate the selected
      // session's pending compact/create intent. A removed active or pending
      // selection still advances the shared intent, while source-owned request
      // and turn fences below invalidate work for a non-selected deleted chat.
      if (clearActive) chatSessionIntentRef.current += 1;
      const activeTurn = activeChatTurnRef.current;
      const activeTurnDeleted = Boolean(activeTurn && sessionIDs.has(activeTurn.sessionID));
      if (activeTurnDeleted) {
        // A destructive fence owns this turn now. Release the process-local
        // submit slot immediately so a replacement chat selected after the
        // fence can start, while generation/token invalidation keeps every
        // late callback from the deleted turn from touching replacement UI.
        chatTurnGenerationRef.current += 1;
        activeChatTurnRef.current = null;
        setChatTurnSessionID("");
        setChatTurnActive(false);
        setChatTurnKind("");
        setChatTurnCancellationAvailable(false);
      }
      const requestOwner = chatRequestOperationOwnerRef.current;
      const requestOwnerDeleted = Boolean(
        requestOwner?.sessionID && sessionIDs.has(requestOwner.sessionID),
      );
      if (requestOwnerDeleted) {
        chatRequestOperationOwnerRef.current = null;
      }
      if (
        (activeTurnDeleted || requestOwnerDeleted) &&
        activeChatTurnRef.current === null &&
        chatRequestOperationOwnerRef.current === null
      ) {
        setChatLoading(false);
        setStreamingContent(null);
      }
      const cancellationOwner = chatCancellationOwnerRef.current;
      if (cancellationOwner?.sessionID && sessionIDs.has(cancellationOwner.sessionID)) {
        chatCancellationOwnerRef.current = null;
        setChatCancelling(false);
        setChatCancellingSessionID("");
        setChatCancellingTurnKind("");
        releaseChatCancellationWaiters(cancellationOwner.sessionID);
      }
      if (clearActive) {
        activeChatTransitionGenerationRef.current += 1;
        pendingActiveChatTransitionRef.current = null;
      }
      setChatSessions((current) => current.filter((session) => !sessionIDs.has(session.id)));
      setChatTargetBySessionID((current) => {
        if (![...sessionIDs].some((sessionID) => current.has(sessionID))) return current;
        const next = new Map(current);
        for (const sessionID of sessionIDs) next.delete(sessionID);
        return next;
      });
      setChatToolsEnabledBySessionID((current) => {
        if (![...sessionIDs].some((sessionID) => current.has(sessionID))) return current;
        const next = new Map(current);
        for (const sessionID of sessionIDs) next.delete(sessionID);
        return next;
      });
      if (!clearActive) return queueCleanupSucceeded;
      setActiveChatSessionID("");
      setActiveChatSession(null);
      setAgentWorkspaceBranch("");
      if (!activeChatTurnRef.current && !chatRequestOperationOwnerRef.current) {
        setChatLoading(false);
        setStreamingContent(null);
      }
      setChatResult(null);
      setPendingToolCalls([]);
      setPendingThread(null);
      clearChatErrorState();
      return queueCleanupSucceeded;
    },
    [
      clearChatErrorState,
      setActiveChatSessionID,
      setChatSessions,
      setChatTargetBySessionID,
      setChatToolsEnabledBySessionID,
      deleteQueuedChatMessagesForSession,
      deleteQueuedChatProjectMessagesWhere,
      deleteQueuedChatMessagesWhere,
      releaseChatCancellationWaiters,
      clearChatStopFence,
    ],
  );

  const fenceChatSessionsMissingFromAuthoritativeSnapshot = useCallback(
    (
      sessionIDs: Iterable<string>,
      stopReadTokensBySessionID: ReadonlyMap<string, number> = new Map(),
    ) => {
      const valid = new Set(sessionIDs);
      const deletedSessionIDs = new Set<string>();
      const isMissingAndUnprotected = (sessionID: string) => {
        if (valid.has(sessionID)) return false;
        if (!chatStopFencesBySessionIDRef.current.has(sessionID)) return true;
        return chatStopFenceAllowsOmission(
          sessionID,
          stopReadTokensBySessionID.get(sessionID) ?? null,
        );
      };
      for (const session of chatSessionsRef.current) {
        if (isMissingAndUnprotected(session.id)) deletedSessionIDs.add(session.id);
      }
      const activeSessionID = activeChatSessionIDRef.current;
      if (activeSessionID && isMissingAndUnprotected(activeSessionID)) {
        deletedSessionIDs.add(activeSessionID);
      }
      const activeSession = activeChatSessionRef.current;
      if (activeSession?.id && isMissingAndUnprotected(activeSession.id)) {
        deletedSessionIDs.add(activeSession.id);
      }
      for (const queued of queuedChatMessagesRef.current) {
        if (isMissingAndUnprotected(queued.session_id)) deletedSessionIDs.add(queued.session_id);
      }
      for (const sessionID of chatTargetBySessionIDRef.current.keys()) {
        if (isMissingAndUnprotected(sessionID)) deletedSessionIDs.add(sessionID);
      }
      for (const sessionID of chatToolsEnabledBySessionIDRef.current.keys()) {
        if (isMissingAndUnprotected(sessionID)) deletedSessionIDs.add(sessionID);
      }
      if (deletedSessionIDs.size === 0) return true;
      return clearDeletedSessionState(
        deletedSessionIDs,
        deletedSessionIDs.has(activeSessionID),
        () => false,
        "",
        true,
      );
    },
    [chatStopFenceAllowsOmission, clearDeletedSessionState],
  );

  const fenceDeletedChatProject = useCallback(
    (projectID: string) => {
      const id = projectID.trim();
      if (!id) return true;
      deletedChatProjectIDsRef.current.add(id);
      const knownProjectBySessionID = new Map(
        chatSessionsRef.current.map((session) => [session.id, (session.project_id ?? "").trim()]),
      );
      const deletedSessionIDs = new Set(
        chatSessionsRef.current
          .filter((session) => (session.project_id ?? "").trim() === id)
          .map((session) => session.id),
      );
      const active = activeChatSessionRef.current;
      if (active) {
        knownProjectBySessionID.set(active.id, (active.project_id ?? "").trim());
        if ((active.project_id ?? "").trim() === id) deletedSessionIDs.add(active.id);
      }
      const activeSessionID = activeChatSessionIDRef.current;
      let queueOwnershipUncertain = false;
      const queueCleanupSucceeded = clearDeletedSessionState(
        deletedSessionIDs,
        deletedSessionIDs.has(activeSessionID) || (active?.project_id ?? "").trim() === id,
        (message) => {
          const queuedProjectID = (message.project_id ?? "").trim();
          const knownProjectID = knownProjectBySessionID.get(message.session_id);
          if (queuedProjectID === id || knownProjectID === id) return true;
          if (message.project_id === undefined && knownProjectID === undefined) {
            queueOwnershipUncertain = true;
          }
          return false;
        },
        id,
      );
      return queueCleanupSucceeded && !queueOwnershipUncertain;
    },
    [clearDeletedSessionState],
  );

  const fenceAllChatSessionsDeleted = useCallback(() => {
    chatResetGenerationRef.current += 1;
    const deletedSessionIDs = new Set<string>();
    for (const session of chatSessionsRef.current) deletedSessionIDs.add(session.id);
    if (activeChatSessionRef.current?.id) deletedSessionIDs.add(activeChatSessionRef.current.id);
    if (activeChatSessionIDRef.current) deletedSessionIDs.add(activeChatSessionIDRef.current);
    for (const queued of queuedChatMessagesRef.current) deletedSessionIDs.add(queued.session_id);
    for (const sessionID of chatTargetBySessionIDRef.current.keys())
      deletedSessionIDs.add(sessionID);
    for (const sessionID of chatToolsEnabledBySessionIDRef.current.keys()) {
      deletedSessionIDs.add(sessionID);
    }
    clearDeletedSessionState(deletedSessionIDs, true);
    if (chatCancellationOwnerRef.current) {
      const cancellationOwner = chatCancellationOwnerRef.current;
      chatCancellationOwnerRef.current = null;
      setChatCancelling(false);
      setChatCancellingSessionID("");
      setChatCancellingTurnKind("");
      releaseChatCancellationWaiters(cancellationOwner.sessionID);
    }
    return clearQueuedChatMessages();
  }, [clearDeletedSessionState, clearQueuedChatMessages, releaseChatCancellationWaiters]);

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
      agentWorkspaceMode,
      chatSessions,
      activeChatSessionID,
      activeChatSession,
      composerDraftsBySessionID,
      savedComposerDraftsBySessionID,
      recoverableComposerDraft,
      activeRecoverableComposerDraftID,
      pendingChatAttachments,
      chatOwnershipMutationInFlight,
      chatAttachmentTurnDraftCount,
      queuedChatMessages,
      model,
      systemPrompt,
      chatCreating,
      chatTurnSessionID,
      chatTurnActive,
      chatTurnKind,
      chatTurnCancellationAvailable,
      chatLoading,
      chatCancelling,
      chatCancellingSessionID,
      chatCancellingTurnKind,
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
      agentWorkspaceMode,
      chatSessions,
      activeChatSessionID,
      activeChatSession,
      composerDraftsBySessionID,
      savedComposerDraftsBySessionID,
      recoverableComposerDraft,
      activeRecoverableComposerDraftID,
      pendingChatAttachments,
      chatOwnershipMutationInFlight,
      chatAttachmentTurnDraftCount,
      queuedChatMessages,
      model,
      systemPrompt,
      chatCreating,
      chatTurnSessionID,
      chatTurnActive,
      chatTurnKind,
      chatTurnCancellationAvailable,
      chatLoading,
      chatCancelling,
      chatCancellingSessionID,
      chatCancellingTurnKind,
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
      beginChatCreation,
      completeChatCreation,
      isChatCreationActive,
      beginChatTurn,
      bindChatTurnSession,
      registerChatTurnPreAdmissionCancel,
      startChatTurnAdmission,
      confirmChatTurnServerCancellation,
      cancelChatTurnBeforeAdmission,
      chatTurnServerCancellationReady,
      completeChatTurn,
      isChatTurnActive,
      isCurrentChatTurn,
      getActiveChatTurnSessionID,
      beginActiveChatTransition,
      captureActiveChatTransition,
      completeActiveChatTransition,
      isCurrentActiveChatTransition,
      setDefaultChatTarget,
      setChatTargetBySessionID,
      setDefaultChatToolsEnabled,
      setChatToolsEnabledBySessionID,
      setAgentAdapterID,
      setAgentConfigOptions,
      setAgentMCPServers,
      setAgentWorkspace,
      setAgentWorkspaceBranch,
      setAgentWorkspaceMode,
      setChatSessions,
      setActiveChatSessionID,
      setActiveChatSession,
      setComposerDraftsBySessionID,
      setSavedComposerDraftsBySessionID,
      saveRecoverableComposerDraft,
      setRecoverableComposerDraft,
      setActiveRecoverableComposerDraftID,
      setPendingChatAttachments,
      setQueuedChatMessages,
      deleteQueuedChatMessagesForSession,
      enqueueQueuedChatMessage,
      setModel,
      setSystemPrompt,
      setChatLoading,
      setChatCancelling,
      setChatCancellingSessionID,
      setChatCancellingTurnKind,
      beginChatCancellation,
      finishChatCancellation,
      hasChatCancellationOwner,
      chatCancellationOwnsSession,
      currentChatCancellationEpoch,
      waitForChatCancellationRelease,
      beginChatStopFence,
      clearChatStopFence,
      acceptChatStopFence,
      getChatStopFence,
      stopReadTokenAtRequestStart,
      chatStopFenceAllowsOmission,
      chatStopFenceAllowsSnapshot,
      chatStopFenceForTurnSettlement,
      chatStopFenceSuppressesApproval,
      chatStopFenceProtectsSession,
      clearSettledChatStopFenceForNewTurn,
      setStreamingContent,
      setChatResult,
      setPendingToolCalls,
      setPendingThread,
      setModelFilter,
      setProviderFilter,
      setChatError,
      removeQueuedChatMessage,
      retryQueuedChatMessage,
      updateQueuedChatMessage,
      currentQueuedChatMessage,
      hasDurableQueuedChatSubmittingFence,
      fenceChatSessionsMissingFromAuthoritativeSnapshot,
      clearChatErrorState,
      setChatErrorState,
      claimChatSessionIntent,
      currentChatSessionIntent,
      isCurrentChatSessionIntent,
      tryBeginChatSessionCreate,
      finishChatSessionCreate,
      isChatSessionCreateInFlight,
      currentChatResetGeneration,
      beginChatRequestOperation,
      bindChatRequestOperationSession,
      finishChatRequestOperation,
      isCurrentChatRequestOperation,
      hasPendingChatAttachments,
      beginChatOwnershipMutation,
      finishChatOwnershipMutation,
      isChatOwnershipMutationInFlight,
      beginChatAttachmentTurn,
      bindChatAttachmentTurn,
      finishChatAttachmentTurn,
      hasChatAttachmentTurn,
      chatAttachmentTurnSessionID,
      currentActiveChatSessionID,
      tombstoneDeletedChatSession,
      isChatSessionDeleted,
      chatOwnershipMutationBlockReason,
      fenceDeletedChatProject,
      fenceAllChatSessionsDeleted,
    }),
    [
      beginChatCreation,
      beginChatCancellation,
      chatCancellationOwnsSession,
      completeChatCreation,
      currentChatCancellationEpoch,
      finishChatCancellation,
      hasChatCancellationOwner,
      beginChatStopFence,
      clearChatStopFence,
      acceptChatStopFence,
      getChatStopFence,
      stopReadTokenAtRequestStart,
      chatStopFenceAllowsOmission,
      chatStopFenceAllowsSnapshot,
      chatStopFenceForTurnSettlement,
      chatStopFenceSuppressesApproval,
      chatStopFenceProtectsSession,
      clearSettledChatStopFenceForNewTurn,
      isChatCreationActive,
      beginChatTurn,
      bindChatTurnSession,
      registerChatTurnPreAdmissionCancel,
      startChatTurnAdmission,
      confirmChatTurnServerCancellation,
      cancelChatTurnBeforeAdmission,
      chatTurnServerCancellationReady,
      completeChatTurn,
      isChatTurnActive,
      isCurrentChatTurn,
      waitForChatCancellationRelease,
      getActiveChatTurnSessionID,
      beginActiveChatTransition,
      captureActiveChatTransition,
      completeActiveChatTransition,
      isCurrentActiveChatTransition,
      saveRecoverableComposerDraft,
      setDefaultChatTarget,
      setChatTargetBySessionID,
      setDefaultChatToolsEnabled,
      setChatToolsEnabledBySessionID,
      setAgentAdapterID,
      setAgentConfigOptions,
      setAgentMCPServers,
      setAgentWorkspace,
      setAgentWorkspaceBranch,
      setAgentWorkspaceMode,
      setChatSessions,
      setActiveChatSessionID,
      setPendingChatAttachments,
      setQueuedChatMessages,
      deleteQueuedChatMessagesForSession,
      enqueueQueuedChatMessage,
      setModel,
      setSystemPrompt,
      setChatCancellingSessionID,
      setChatCancellingTurnKind,
      removeQueuedChatMessage,
      retryQueuedChatMessage,
      updateQueuedChatMessage,
      currentQueuedChatMessage,
      hasDurableQueuedChatSubmittingFence,
      fenceChatSessionsMissingFromAuthoritativeSnapshot,
      clearChatErrorState,
      setChatErrorState,
      claimChatSessionIntent,
      currentChatSessionIntent,
      isCurrentChatSessionIntent,
      tryBeginChatSessionCreate,
      finishChatSessionCreate,
      isChatSessionCreateInFlight,
      currentChatResetGeneration,
      beginChatRequestOperation,
      bindChatRequestOperationSession,
      finishChatRequestOperation,
      isCurrentChatRequestOperation,
      hasPendingChatAttachments,
      beginChatOwnershipMutation,
      finishChatOwnershipMutation,
      isChatOwnershipMutationInFlight,
      beginChatAttachmentTurn,
      bindChatAttachmentTurn,
      finishChatAttachmentTurn,
      hasChatAttachmentTurn,
      chatAttachmentTurnSessionID,
      currentActiveChatSessionID,
      tombstoneDeletedChatSession,
      isChatSessionDeleted,
      chatOwnershipMutationBlockReason,
      fenceDeletedChatProject,
      fenceAllChatSessionsDeleted,
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
