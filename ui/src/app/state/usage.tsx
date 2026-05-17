// Usage slice: cost summary + recent events. Owned entirely by
// UsageView — neither field is in the dashboard snapshot any more.
// useEnsureUsageLoaded fetches both lazily on first UsageView mount
// and caches via the `loaded` flag so in-session navigations don't
// re-fetch. A full page reload resets the slice and the next mount
// re-fetches.

import { createContext, useCallback, useContext, useEffect, useMemo, useReducer, useRef, type ReactNode } from "react";

import { applyOverride, CoordinatorOverridesContext } from "./coordinators/overrides";
import { getUsageEvents, getUsageSummary } from "../../lib/api";
import { warn } from "../../lib/log";
import type { UsageEventsResponse, UsageSummaryResponse } from "../../types/usage";

export type UsageState = {
  summary: UsageSummaryResponse["data"] | null;
  events: UsageEventsResponse["data"];
  loaded: boolean;
};

export type UsageActions = {
  setSummary: (summary: UsageSummaryResponse["data"] | null) => void;
  setEvents: (events: UsageEventsResponse["data"]) => void;
  markLoaded: () => void;
};

type UsageContextValue = {
  state: UsageState;
  actions: UsageActions;
};

type Action =
  | { type: "summary/set"; summary: UsageSummaryResponse["data"] | null }
  | { type: "events/set"; events: UsageEventsResponse["data"] }
  | { type: "loaded/set" };

const initialState: UsageState = {
  summary: null,
  events: [],
  loaded: false,
};

function reducer(state: UsageState, action: Action): UsageState {
  switch (action.type) {
    case "summary/set":
      return { ...state, summary: action.summary };
    case "events/set":
      return { ...state, events: action.events };
    case "loaded/set":
      return state.loaded ? state : { ...state, loaded: true };
  }
}

const UsageContext = createContext<UsageContextValue | null>(null);

export function UsageProvider({ children, initialState: seededState }: {
  children: ReactNode;
  initialState?: Partial<UsageState>;
}) {
  const [state, dispatch] = useReducer(
    reducer,
    seededState ? { ...initialState, ...seededState } : initialState,
  );

  const setSummary = useCallback((summary: UsageSummaryResponse["data"] | null) => {
    dispatch({ type: "summary/set", summary });
  }, []);

  const setEvents = useCallback((events: UsageEventsResponse["data"]) => {
    dispatch({ type: "events/set", events });
  }, []);

  const markLoaded = useCallback(() => {
    dispatch({ type: "loaded/set" });
  }, []);

  const actions = useMemo<UsageActions>(
    () => ({ setSummary, setEvents, markLoaded }),
    [setSummary, setEvents, markLoaded],
  );
  const value = useMemo(() => ({ state, actions }), [state, actions]);

  return <UsageContext.Provider value={value}>{children}</UsageContext.Provider>;
}

export function useUsage(): UsageContextValue {
  const ctx = useContext(UsageContext);
  if (!ctx) {
    throw new Error("useUsage must be used inside a <UsageProvider>");
  }
  const overrides = useContext(CoordinatorOverridesContext);
  return { state: ctx.state, actions: applyOverride(ctx.actions, overrides?.usageSlice) };
}

// useEnsureUsageLoaded fetches the usage summary + recent events on
// first call if the slice's `loaded` flag is false. Used by
// UsageView.
//
// Dedup behavior:
//   - The `inFlight` ref is per hook instance — it blocks the same
//     hook from re-firing while its first fetch pair is pending.
//   - Cross-surface dedup happens AFTER the first fetch resolves,
//     when `loaded` flips to true: subsequent calls early-return.
//     If two surfaces mounted the hook concurrently before either
//     flip lands, both fetch pairs would fire — acceptable today
//     because UsageView is the only consumer.
//
// Tolerates failed fetches by leaving `loaded` false so the next
// mount retries.
export function useEnsureUsageLoaded(): void {
  const { state, actions } = useUsage();
  const inFlight = useRef(false);

  useEffect(() => {
    if (state.loaded || inFlight.current) return;
    inFlight.current = true;
    void (async () => {
      try {
        const [summary, events] = await Promise.all([
          getUsageSummary(""),
          getUsageEvents(20),
        ]);
        actions.setSummary(summary.data);
        actions.setEvents(events.data);
        actions.markLoaded();
      } catch (err) {
        warn("usage.ensureLoaded.failed", { err: err instanceof Error ? err.message : String(err) });
      } finally {
        inFlight.current = false;
      }
    })();
  }, [state.loaded, actions]);
}
