// Runtime slice: gateway health, session info, top-level
// loading/error/message banners, runtime response headers
// (request_id / trace_id), the copy-to-clipboard transient flag,
// and the Hecate "rich tool kit" availability bits used by the
// Chat composer.
//
// The slice exposes granular setters because the dashboard
// loader, agent-chat session reapply path, chat-completion
// post-processing, and operator-driven actions all set
// individual fields at different times. The coordinator that
// flips `hecateRTKEnabled` and PATCHes the session settings
// stays in `useRuntimeConsole` for now — it couples runtime
// state to the chats slice (`activeAgentChatSession`) which
// hasn't been carved out yet.
//
// `copyCommand` lives in the slice because the clipboard
// side-effect is self-contained (no cross-slice state); a
// 1.5 s timeout clears the indicator so the consumer doesn't
// have to thread one in.

import { createContext, useCallback, useContext, useMemo, useReducer, type ReactNode } from "react";

import type { HealthResponse, RuntimeHeaders, SessionResponse } from "../../types/runtime";

export type RuntimeState = {
  health: HealthResponse | null;
  sessionInfo: SessionResponse["data"] | null;
  loading: boolean;
  error: string;
  message: string;
  runtimeHeaders: RuntimeHeaders | null;
  copiedCommand: string;
  hecateRTKEnabled: boolean;
  hecateRTKAvailable: boolean;
  hecateRTKPath: string;
};

export type RuntimeActions = {
  setHealth: (value: HealthResponse | null) => void;
  setSessionInfo: (value: SessionResponse["data"] | null) => void;
  setLoading: (value: boolean) => void;
  setError: (value: string) => void;
  setMessage: (value: string) => void;
  setRuntimeHeaders: (value: RuntimeHeaders | null) => void;
  setHecateRTKEnabled: (value: boolean) => void;
  setHecateRTKAvailable: (value: boolean) => void;
  setHecateRTKPath: (value: string) => void;
  copyCommand: (command: string) => Promise<void>;
};

type RuntimeContextValue = {
  state: RuntimeState;
  actions: RuntimeActions;
};

type Action =
  | { type: "health/set"; value: HealthResponse | null }
  | { type: "sessionInfo/set"; value: SessionResponse["data"] | null }
  | { type: "loading/set"; value: boolean }
  | { type: "error/set"; value: string }
  | { type: "message/set"; value: string }
  | { type: "runtimeHeaders/set"; value: RuntimeHeaders | null }
  | { type: "copiedCommand/set"; value: string }
  | { type: "copiedCommand/clearIf"; matching: string }
  | { type: "hecateRTKEnabled/set"; value: boolean }
  | { type: "hecateRTKAvailable/set"; value: boolean }
  | { type: "hecateRTKPath/set"; value: string };

const initialState: RuntimeState = {
  health: null,
  sessionInfo: null,
  loading: true,
  error: "",
  message: "",
  runtimeHeaders: null,
  copiedCommand: "",
  hecateRTKEnabled: false,
  hecateRTKAvailable: false,
  hecateRTKPath: "",
};

function reducer(state: RuntimeState, action: Action): RuntimeState {
  switch (action.type) {
    case "health/set":          return { ...state, health: action.value };
    case "sessionInfo/set":     return { ...state, sessionInfo: action.value };
    case "loading/set":         return { ...state, loading: action.value };
    case "error/set":           return { ...state, error: action.value };
    case "message/set":         return { ...state, message: action.value };
    case "runtimeHeaders/set":  return { ...state, runtimeHeaders: action.value };
    case "copiedCommand/set":   return { ...state, copiedCommand: action.value };
    case "copiedCommand/clearIf":
      return state.copiedCommand === action.matching ? { ...state, copiedCommand: "" } : state;
    case "hecateRTKEnabled/set":   return { ...state, hecateRTKEnabled: action.value };
    case "hecateRTKAvailable/set": return { ...state, hecateRTKAvailable: action.value };
    case "hecateRTKPath/set":      return { ...state, hecateRTKPath: action.value };
  }
}

const RuntimeContext = createContext<RuntimeContextValue | null>(null);

const COPY_INDICATOR_MS = 1500;

export function RuntimeProvider({ children }: { children: ReactNode }) {
  const [state, dispatch] = useReducer(reducer, initialState);

  const setHealth = useCallback((value: HealthResponse | null) => dispatch({ type: "health/set", value }), []);
  const setSessionInfo = useCallback((value: SessionResponse["data"] | null) => dispatch({ type: "sessionInfo/set", value }), []);
  const setLoading = useCallback((value: boolean) => dispatch({ type: "loading/set", value }), []);
  const setError = useCallback((value: string) => dispatch({ type: "error/set", value }), []);
  const setMessage = useCallback((value: string) => dispatch({ type: "message/set", value }), []);
  const setRuntimeHeaders = useCallback((value: RuntimeHeaders | null) => dispatch({ type: "runtimeHeaders/set", value }), []);
  const setHecateRTKEnabled = useCallback((value: boolean) => dispatch({ type: "hecateRTKEnabled/set", value }), []);
  const setHecateRTKAvailable = useCallback((value: boolean) => dispatch({ type: "hecateRTKAvailable/set", value }), []);
  const setHecateRTKPath = useCallback((value: string) => dispatch({ type: "hecateRTKPath/set", value }), []);

  const copyCommand = useCallback(async (command: string) => {
    try {
      await navigator.clipboard.writeText(command);
      dispatch({ type: "copiedCommand/set", value: command });
      window.setTimeout(() => {
        dispatch({ type: "copiedCommand/clearIf", matching: command });
      }, COPY_INDICATOR_MS);
    } catch {
      dispatch({ type: "copiedCommand/set", value: "" });
    }
  }, []);

  const actions = useMemo<RuntimeActions>(() => ({
    setHealth,
    setSessionInfo,
    setLoading,
    setError,
    setMessage,
    setRuntimeHeaders,
    setHecateRTKEnabled,
    setHecateRTKAvailable,
    setHecateRTKPath,
    copyCommand,
  }), [
    setHealth, setSessionInfo, setLoading, setError, setMessage,
    setRuntimeHeaders, setHecateRTKEnabled, setHecateRTKAvailable,
    setHecateRTKPath, copyCommand,
  ]);

  const value = useMemo(() => ({ state, actions }), [state, actions]);

  return <RuntimeContext.Provider value={value}>{children}</RuntimeContext.Provider>;
}

export function useRuntime(): RuntimeContextValue {
  const ctx = useContext(RuntimeContext);
  if (!ctx) {
    throw new Error("useRuntime must be used inside a <RuntimeProvider>");
  }
  return ctx;
}
