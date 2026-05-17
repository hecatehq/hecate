// Settings slice: the server-fetched configuration snapshot the
// settings page renders against, the settings-mutation error string
// shown next to settings forms, and the transient cross-page notice
// banner (success / error toast).
//
// `notice` is a global UI concern rather than a settings concern —
// it's set after settings mutations but also after chat actions and
// other operator-driven events. Lives here because it had no better
// home and shipping a one-field "notice" slice felt heavier than
// the seam was worth; if the slice grows a second non-settings
// concern, split.

import { createContext, useCallback, useContext, useMemo, useReducer, type ReactNode } from "react";

import type { ConfiguredStateResponse } from "../../types/provider";

export type NoticeState = {
  kind: "success" | "error";
  message: string;
};

export type SettingsState = {
  config: ConfiguredStateResponse["data"] | null;
  error: string;
  notice: NoticeState | null;
};

export type SettingsActions = {
  setConfig: (value: ConfiguredStateResponse["data"] | null) => void;
  updateConfig: (
    updater: (current: ConfiguredStateResponse["data"] | null) => ConfiguredStateResponse["data"] | null,
  ) => void;
  setError: (value: string) => void;
  setNotice: (value: NoticeState | null) => void;
  dismissNotice: () => void;
  dismissNoticeIfMatching: (notice: NoticeState) => void;
};

type SettingsContextValue = {
  state: SettingsState;
  actions: SettingsActions;
};

type Action =
  | { type: "config/set"; value: ConfiguredStateResponse["data"] | null }
  | {
      type: "config/update";
      updater: (current: ConfiguredStateResponse["data"] | null) => ConfiguredStateResponse["data"] | null;
    }
  | { type: "error/set"; value: string }
  | { type: "notice/set"; value: NoticeState | null }
  | { type: "notice/dismissIfMatching"; notice: NoticeState };

const initialState: SettingsState = {
  config: null,
  error: "",
  notice: null,
};

function reducer(state: SettingsState, action: Action): SettingsState {
  switch (action.type) {
    case "config/set":    return { ...state, config: action.value };
    case "config/update": return { ...state, config: action.updater(state.config) };
    case "error/set":     return { ...state, error: action.value };
    case "notice/set":    return { ...state, notice: action.value };
    case "notice/dismissIfMatching":
      return state.notice === action.notice ? { ...state, notice: null } : state;
  }
}

const SettingsContext = createContext<SettingsContextValue | null>(null);

export function SettingsProvider({ children }: { children: ReactNode }) {
  const [state, dispatch] = useReducer(reducer, initialState);

  const setConfig = useCallback((value: ConfiguredStateResponse["data"] | null) => {
    dispatch({ type: "config/set", value });
  }, []);
  const updateConfig = useCallback(
    (updater: (current: ConfiguredStateResponse["data"] | null) => ConfiguredStateResponse["data"] | null) => {
      dispatch({ type: "config/update", updater });
    },
    [],
  );
  const setError = useCallback((value: string) => {
    dispatch({ type: "error/set", value });
  }, []);
  const setNotice = useCallback((value: NoticeState | null) => {
    dispatch({ type: "notice/set", value });
  }, []);
  const dismissNotice = useCallback(() => {
    dispatch({ type: "notice/set", value: null });
  }, []);
  const dismissNoticeIfMatching = useCallback((notice: NoticeState) => {
    dispatch({ type: "notice/dismissIfMatching", notice });
  }, []);

  const actions = useMemo<SettingsActions>(
    () => ({ setConfig, updateConfig, setError, setNotice, dismissNotice, dismissNoticeIfMatching }),
    [setConfig, updateConfig, setError, setNotice, dismissNotice, dismissNoticeIfMatching],
  );
  const value = useMemo(() => ({ state, actions }), [state, actions]);

  return <SettingsContext.Provider value={value}>{children}</SettingsContext.Provider>;
}

export function useSettings(): SettingsContextValue {
  const ctx = useContext(SettingsContext);
  if (!ctx) {
    throw new Error("useSettings must be used inside a <SettingsProvider>");
  }
  return ctx;
}
