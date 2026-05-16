// modelChat slice: state for today's `/hecate/v1/chat/sessions`
// path. Pure data storage with one self-contained domain action
// (`loadMore` — pagination append). The cross-cut session actions
// (`selectChatSession`, `renameChatSession`, `deleteChatSession`)
// stay in the shim because they branch between this slice and the
// agent-chat slice based on session kind.
//
// Marked for removal: when the model-chat path retires, this
// entire file plus its Provider plus the matching shim plumbing
// gets deleted in a single PR. Keeping it as a separate slice
// (rather than merging into the canonical chat slice) is what
// makes that deletion mechanical.
//
// `activeID` is persisted via `usePersistedState` so a reload
// returns the operator to the same model-chat session. The
// reducer owns the non-persisted fields; the persisted ID lives
// in its own hook alongside the reducer, exposed through the
// same context value.

import { createContext, useCallback, useContext, useMemo, useReducer, type ReactNode } from "react";

import { getChatSessions } from "../../lib/api";
import { parseStoredString, usePersistedState } from "../../lib/persistedState";
import type { ChatSessionRecord, ChatSessionsResponse } from "../../types/runtime";

const ACTIVE_ID_STORAGE_KEY = "hecate.chatSessionID";
const LOAD_MORE_PAGE_SIZE = 20;

export type ModelChatState = {
  sessions: ChatSessionsResponse["data"];
  hasMore: boolean;
  loadingMore: boolean;
  activeID: string;
  activeSession: ChatSessionRecord | null;
};

type SetStateAction<T> = T | ((prev: T) => T);

export type ModelChatActions = {
  setSessions: (next: SetStateAction<ChatSessionsResponse["data"]>) => void;
  setHasMore: (value: boolean) => void;
  setActiveID: (value: string) => void;
  setActiveSession: (next: SetStateAction<ChatSessionRecord | null>) => void;
  loadMore: () => Promise<void>;
};

type ModelChatContextValue = {
  state: ModelChatState;
  actions: ModelChatActions;
};

type Action =
  | { type: "sessions/set"; next: SetStateAction<ChatSessionsResponse["data"]> }
  | { type: "hasMore/set"; value: boolean }
  | { type: "activeSession/set"; next: SetStateAction<ChatSessionRecord | null> }
  | { type: "loadMore/start" }
  | { type: "loadMore/done"; rows: ChatSessionsResponse["data"]; hasMore: boolean }
  | { type: "loadMore/fail" };

type ReducerState = Omit<ModelChatState, "activeID">;

const initialState: ReducerState = {
  sessions: [],
  hasMore: false,
  loadingMore: false,
  activeSession: null,
};

function resolve<T>(prev: T, next: SetStateAction<T>): T {
  return typeof next === "function" ? (next as (prev: T) => T)(prev) : next;
}

function reducer(state: ReducerState, action: Action): ReducerState {
  switch (action.type) {
    case "sessions/set":
      return { ...state, sessions: resolve(state.sessions, action.next) };
    case "hasMore/set":
      return { ...state, hasMore: action.value };
    case "activeSession/set":
      return { ...state, activeSession: resolve(state.activeSession, action.next) };
    case "loadMore/start":
      return { ...state, loadingMore: true };
    case "loadMore/done":
      return {
        ...state,
        loadingMore: false,
        sessions: [...state.sessions, ...action.rows],
        hasMore: action.hasMore,
      };
    case "loadMore/fail":
      return { ...state, loadingMore: false };
  }
}

const ModelChatContext = createContext<ModelChatContextValue | null>(null);

export function ModelChatProvider({ children }: { children: ReactNode }) {
  const [reducerState, dispatch] = useReducer(reducer, initialState);
  const [activeID, setActiveIDPersisted] = usePersistedState<string>(
    ACTIVE_ID_STORAGE_KEY,
    parseStoredString,
    "",
    { shouldRemove: (v) => v === "" },
  );

  const setSessions = useCallback(
    (next: SetStateAction<ChatSessionsResponse["data"]>) => dispatch({ type: "sessions/set", next }),
    [],
  );
  const setHasMore = useCallback((value: boolean) => dispatch({ type: "hasMore/set", value }), []);
  const setActiveSession = useCallback(
    (next: SetStateAction<ChatSessionRecord | null>) => dispatch({ type: "activeSession/set", next }),
    [],
  );
  const setActiveID = useCallback((value: string) => setActiveIDPersisted(value), [setActiveIDPersisted]);

  const loadMore = useCallback(async () => {
    if (reducerState.loadingMore || !reducerState.hasMore) return;
    dispatch({ type: "loadMore/start" });
    try {
      const result = await getChatSessions(LOAD_MORE_PAGE_SIZE, reducerState.sessions.length);
      dispatch({
        type: "loadMore/done",
        rows: result.data ?? [],
        hasMore: result.has_more ?? false,
      });
    } catch {
      // Keep sidebar responsive; silently skip failed page loads.
      dispatch({ type: "loadMore/fail" });
    }
  }, [reducerState.loadingMore, reducerState.hasMore, reducerState.sessions.length]);

  const actions = useMemo<ModelChatActions>(
    () => ({ setSessions, setHasMore, setActiveID, setActiveSession, loadMore }),
    [setSessions, setHasMore, setActiveID, setActiveSession, loadMore],
  );

  const state: ModelChatState = useMemo(() => ({ ...reducerState, activeID }), [reducerState, activeID]);
  const value = useMemo(() => ({ state, actions }), [state, actions]);

  return <ModelChatContext.Provider value={value}>{children}</ModelChatContext.Provider>;
}

export function useModelChat(): ModelChatContextValue {
  const ctx = useContext(ModelChatContext);
  if (!ctx) {
    throw new Error("useModelChat must be used inside a <ModelChatProvider>");
  }
  return ctx;
}
