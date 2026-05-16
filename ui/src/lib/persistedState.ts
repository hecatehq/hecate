// usePersistedState: localStorage-backed state hook with a narrow
// shape guard. Replaces the ten inline `useEffect(() =>
// localStorage.setItem(k, v), [v])` patterns in
// app/useRuntimeConsole.ts and the bespoke read/write helpers for
// queued chat messages and chatTargetBySessionID.
//
// On read, `parse(raw)` decides what to do with the stored string:
//
//   - return T  → use it as the initial value
//   - return null → wipe the key, log via lib/log.ts, fall back
//
// The "wipe and fall back" behaviour is deliberate. The previous
// per-field `typeof item.foo === "string" ? item.foo : ""` pattern
// silently massaged corrupt records into half-zeroed objects, which
// then re-persisted on the next mutation and outlived their original
// version. Loud failure replaces silent shape-drift fallback so
// regressions surface during development instead of compounding.
//
// On write, the hook serialises (default `JSON.stringify`) and
// writes to localStorage. Pass `shouldRemove` to delete the key when
// the value reaches a "cleared" state (empty string, empty array)
// instead of persisting the empty representation.
//
// Browser storage failures (quota, private mode, blocked by policy)
// are caught and logged but never thrown — the in-memory state
// remains usable.

import { useEffect, useRef, useState } from "react";

import { warn as logWarn } from "./log";

export type PersistedStateOptions<T> = {
  /** Serialise T → storage string. Defaults to `JSON.stringify`. */
  serialize?: (value: T) => string;
  /** When true, removeItem(key) instead of writing. Useful for
   *  "" / [] / null sentinel values that previously cleared the
   *  key via removeItem in the per-field code. */
  shouldRemove?: (value: T) => boolean;
};

/** Pass-through parse for raw string storage (the most common case). */
export const parseStoredString = (raw: string): string | null => raw;

/** Parse a JSON-stringified value with a structural guard.
 *
 *  Returns null on JSON parse error or when guard rejects — the
 *  hook will wipe the key and fall back. */
export function parseStoredJSON<T>(guard: (parsed: unknown) => T | null): (raw: string) => T | null {
  return (raw) => {
    let parsed: unknown;
    try {
      parsed = JSON.parse(raw);
    } catch {
      return null;
    }
    return guard(parsed);
  };
}

const isBrowser = typeof window !== "undefined" && typeof window.localStorage !== "undefined";

function readInitial<T>(key: string, parse: (raw: string) => T | null, fallback: T): T {
  if (!isBrowser) return fallback;
  let raw: string | null;
  try {
    raw = window.localStorage.getItem(key);
  } catch (err) {
    logWarn(`usePersistedState: read failed for ${key}:`, err);
    return fallback;
  }
  if (raw === null) return fallback;
  const value = parse(raw);
  if (value === null) {
    // Shape mismatch — wipe the key so the next render starts clean.
    try {
      window.localStorage.removeItem(key);
    } catch {
      // Best-effort.
    }
    logWarn(`usePersistedState: dropped malformed ${key}`);
    return fallback;
  }
  return value;
}

export function usePersistedState<T>(
  key: string,
  parse: (raw: string) => T | null,
  fallback: T,
  options: PersistedStateOptions<T> = {},
): [T, React.Dispatch<React.SetStateAction<T>>] {
  const serialize = options.serialize ?? defaultSerialize;
  const shouldRemove = options.shouldRemove;
  // Mirror callbacks to refs so the write effect doesn't re-bind
  // each render when a caller passes a fresh closure.
  const serializeRef = useRef(serialize);
  serializeRef.current = serialize;
  const shouldRemoveRef = useRef(shouldRemove);
  shouldRemoveRef.current = shouldRemove;

  const [value, setValue] = useState<T>(() => readInitial(key, parse, fallback));

  // Skip the very first effect run — `useState`'s init function
  // already reflected what was in localStorage (or wiped + fell
  // back). Writing on mount would re-persist the fallback after a
  // rejection, defeating the wipe. Only writes triggered by
  // setValue() should propagate to storage.
  const hasMountedRef = useRef(false);
  useEffect(() => {
    if (!isBrowser) return;
    if (!hasMountedRef.current) {
      hasMountedRef.current = true;
      return;
    }
    try {
      if (shouldRemoveRef.current?.(value)) {
        window.localStorage.removeItem(key);
        return;
      }
      window.localStorage.setItem(key, serializeRef.current(value));
    } catch (err) {
      // Quota, private mode, browser policy — keep state usable
      // even when persistence isn't.
      logWarn(`usePersistedState: write failed for ${key}:`, err);
    }
  }, [key, value]);

  return [value, setValue];
}

function defaultSerialize<T>(value: T): string {
  return typeof value === "string" ? value : JSON.stringify(value);
}
