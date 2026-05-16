// Usage slice: cost summary + recent events. Owns two state
// fields and exposes a single applySnapshot action; the dashboard
// loader fans the snapshot into here. Future direct fetches
// (refresh button, filter changes) get their own actions when the
// consuming views ask for them.

import { createContext, useCallback, useContext, useMemo, useReducer, type ReactNode } from "react";

import type { UsageEventsResponse, UsageSummaryResponse } from "../../types/runtime";

export type UsageState = {
  summary: UsageSummaryResponse["data"] | null;
  events: UsageEventsResponse["data"];
};

export type UsageActions = {
  setSummary: (summary: UsageSummaryResponse["data"] | null) => void;
  setEvents: (events: UsageEventsResponse["data"]) => void;
};

type UsageContextValue = {
  state: UsageState;
  actions: UsageActions;
};

type Action =
  | { type: "summary/set"; summary: UsageSummaryResponse["data"] | null }
  | { type: "events/set"; events: UsageEventsResponse["data"] };

const initialState: UsageState = {
  summary: null,
  events: [],
};

function reducer(state: UsageState, action: Action): UsageState {
  switch (action.type) {
    case "summary/set":
      return { ...state, summary: action.summary };
    case "events/set":
      return { ...state, events: action.events };
  }
}

const UsageContext = createContext<UsageContextValue | null>(null);

export function UsageProvider({ children }: { children: ReactNode }) {
  const [state, dispatch] = useReducer(reducer, initialState);

  const setSummary = useCallback((summary: UsageSummaryResponse["data"] | null) => {
    dispatch({ type: "summary/set", summary });
  }, []);

  const setEvents = useCallback((events: UsageEventsResponse["data"]) => {
    dispatch({ type: "events/set", events });
  }, []);

  const actions = useMemo<UsageActions>(() => ({ setSummary, setEvents }), [setSummary, setEvents]);
  const value = useMemo(() => ({ state, actions }), [state, actions]);

  return <UsageContext.Provider value={value}>{children}</UsageContext.Provider>;
}

export function useUsage(): UsageContextValue {
  const ctx = useContext(UsageContext);
  if (!ctx) {
    throw new Error("useUsage must be used inside a <UsageProvider>");
  }
  return ctx;
}
