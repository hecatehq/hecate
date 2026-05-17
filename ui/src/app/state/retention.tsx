// Retention slice: state + actions for the operator-side retention
// worker control surface. Owns five fields (subsystems CSV input,
// in-flight flag, last error, last run record, recent runs list) and
// three actions (set subsystems, load recent runs, trigger a run).
//
// Design: Context + useReducer. Mounted once at App.tsx top so the
// state survives workspace switches; consumed via `useRetention()`
// from `useRuntimeConsole`'s composition layer for now. A later
// refactor will let SettingsView read it directly without the
// shim pass-through; for now the surface stability is what lets
// the migration happen one slice at a time.
//
// Cross-slice concerns: a successful or failed `runRetention()`
// also flips the global `notice` banner. That cross-cut stays in
// `useRuntimeConsole`'s shim — the slice's `runRetention` returns a
// `RetentionRunResult` so the caller can route success / failure
// without the slice importing notice state.

import { createContext, useCallback, useContext, useMemo, useReducer, useRef, type ReactNode } from "react";

import { getRetentionRuns, runRetention as runRetentionRequest } from "../../lib/api";
import { warn as logWarn } from "../../lib/log";
import { parseCSV } from "../../lib/runtime-utils";
import type { RetentionRunData } from "../../types/retention";

const RECENT_RUNS_LIMIT = 10;

export type RetentionState = {
  subsystems: string;
  loading: boolean;
  error: string;
  lastRun: RetentionRunData | null;
  runs: RetentionRunData[];
};

export type RetentionRunResult =
  | { ok: true; run: RetentionRunData }
  | { ok: false; error: string };

export type RetentionActions = {
  setSubsystems: (value: string) => void;
  loadRuns: () => Promise<void>;
  runRetention: () => Promise<RetentionRunResult>;
};

type RetentionContextValue = {
  state: RetentionState;
  actions: RetentionActions;
};

type Action =
  | { type: "subsystems/set"; value: string }
  | { type: "runs/loaded"; runs: RetentionRunData[] }
  | { type: "run/started" }
  | { type: "run/succeeded"; run: RetentionRunData }
  | { type: "run/failed"; error: string };

const initialState: RetentionState = {
  subsystems: "",
  loading: false,
  error: "",
  lastRun: null,
  runs: [],
};

function reducer(state: RetentionState, action: Action): RetentionState {
  switch (action.type) {
    case "subsystems/set":
      return { ...state, subsystems: action.value };
    case "runs/loaded":
      return {
        ...state,
        runs: action.runs,
        lastRun: action.runs[0] ?? null,
      };
    case "run/started":
      return { ...state, loading: true, error: "" };
    case "run/succeeded": {
      // Prepend the new run, dedupe by finished_at (the worker
      // sometimes returns the same record twice if the caller
      // hammered the trigger), cap at RECENT_RUNS_LIMIT.
      const runs = [
        action.run,
        ...state.runs.filter((run) => run.finished_at !== action.run.finished_at),
      ].slice(0, RECENT_RUNS_LIMIT);
      return { ...state, loading: false, lastRun: action.run, runs };
    }
    case "run/failed":
      return { ...state, loading: false, error: action.error };
  }
}

const RetentionContext = createContext<RetentionContextValue | null>(null);

export function RetentionProvider({ children }: { children: ReactNode }) {
  const [state, dispatch] = useReducer(reducer, initialState);
  // Re-entrancy guard outside the reducer because SettingsView mounts
  // can race and we don't want to dispatch a "started" action that
  // would otherwise be harmless but adds noise to the trace.
  const loadingRef = useRef(false);

  const setSubsystems = useCallback((value: string) => {
    dispatch({ type: "subsystems/set", value });
  }, []);

  const loadRuns = useCallback(async () => {
    if (loadingRef.current) return;
    loadingRef.current = true;
    try {
      const payload = await getRetentionRuns(RECENT_RUNS_LIMIT);
      dispatch({ type: "runs/loaded", runs: payload.data ?? [] });
    } catch (loadError) {
      // SettingsView shows an empty list rather than an error
      // banner; surfacing this would compete with the rest of
      // the settings UI for attention. Log for debugging.
      logWarn("loadRetentionRuns failed:", loadError);
    } finally {
      loadingRef.current = false;
    }
  }, []);

  const runRetention = useCallback(async (): Promise<RetentionRunResult> => {
    dispatch({ type: "run/started" });
    try {
      const payload = await runRetentionRequest({
        subsystems: parseCSV(state.subsystems),
      });
      dispatch({ type: "run/succeeded", run: payload.data });
      return { ok: true, run: payload.data };
    } catch (error) {
      const message = error instanceof Error ? error.message : "failed to run retention";
      dispatch({ type: "run/failed", error: message });
      return { ok: false, error: message };
    }
  }, [state.subsystems]);

  const actions = useMemo(
    () => ({ setSubsystems, loadRuns, runRetention }),
    [setSubsystems, loadRuns, runRetention],
  );

  const value = useMemo(() => ({ state, actions }), [state, actions]);

  return <RetentionContext.Provider value={value}>{children}</RetentionContext.Provider>;
}

export function useRetention(): RetentionContextValue {
  const ctx = useContext(RetentionContext);
  if (!ctx) {
    throw new Error("useRetention must be used inside a <RetentionProvider>");
  }
  return ctx;
}
