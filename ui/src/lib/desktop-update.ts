// Desktop update hook. Runs the Tauri updater plugin's check() on
// mount, on a slow timer thereafter, and on demand via checkNow().
// Outside the Tauri runtime (browser, Docker, bare binary) the hook
// is inert — there's no app to update.
//
// Failures surface as logWarn so a developer with devtools open
// can diagnose them; the banner only ever appears on a positive
// answer. Earlier versions swallowed errors silently, which made
// "no update banner appeared" indistinguishable from "manifest
// fetch failed" — see PR #107 history for the reasoning.

import { useCallback, useEffect, useRef, useState } from "react";

import { warn as logWarn } from "./log";
import { isTauriRuntime } from "./tauri";

// Surface or clear the dock / taskbar "update available" badge.
// Tauri command `set_update_badge` is registered in lib.rs;
// outside Tauri this is a no-op so the hook stays cheap.
async function setUpdateBadge(visible: boolean): Promise<void> {
  if (!isTauriRuntime()) return;
  try {
    const mod = await import("@tauri-apps/api/core");
    await mod.invoke("set_update_badge", { visible });
  } catch (err) {
    logWarn("set_update_badge invoke failed:", err);
  }
}

const DISMISS_STORAGE_KEY = "hecate.update.dismissed";

// 1h between background re-checks. Long enough to be polite to the
// hecate.sh CDN and to GitHub's release endpoint; short enough that
// an app left open all day still picks up the next alpha within an
// hour. The first check fires immediately on mount; this only
// governs the steady-state cadence.
export const DESKTOP_UPDATE_POLL_INTERVAL_MS = 60 * 60 * 1000;

// How long "up to date" feedback from a manual check lingers in
// state before clearing itself. Just long enough for a transient
// banner to read naturally.
const UP_TO_DATE_FEEDBACK_MS = 4_000;

export type DesktopUpdate = {
  version: string;
};

// Result of the most recent check. "update" rides on top of the
// `update` field below; "up-to-date" is surfaced transiently after a
// manual checkNow() returns nothing so the UI can confirm the user's
// action. "error" mirrors a thrown check() — we still log to
// console, but exposing it lets a future surface (settings, native
// menu indicator) show that the check itself failed.
export type DesktopUpdateCheckResult = "update" | "up-to-date" | "error";

type State = {
  update: DesktopUpdate | null;
  installing: boolean;
  lastCheckResult: DesktopUpdateCheckResult | null;
  // downloaded/total drive a 0..1 progress fraction surfaced as
  // `progress` below. total may be 0 if the Started event never
  // fires (some Tauri builds skip it on small payloads); the
  // banner falls back to indeterminate "Downloading…" in that case.
  downloaded: number;
  total: number;
};

// Plugin event shape — narrowed locally so the dynamic-import path
// doesn't drag the whole @tauri-apps/plugin-updater type surface
// into the web build.
type DownloadEvent =
  | { event: "Started"; data: { contentLength?: number } }
  | { event: "Progress"; data: { chunkLength: number } }
  | { event: "Finished" };

type PluginUpdate = {
  version: string;
  downloadAndInstall: (onEvent?: (e: DownloadEvent) => void) => Promise<void>;
};

export function useDesktopUpdate(): {
  update: DesktopUpdate | null;
  installing: boolean;
  /** 0..1 while downloading, null when not downloading or when total is unknown. */
  progress: number | null;
  /** Result of the most recent check, transient. */
  lastCheckResult: DesktopUpdateCheckResult | null;
  dismiss: () => void;
  installAndRestart: () => Promise<void>;
  /** Re-run the updater check immediately, bypassing the dismissed flag. */
  checkNow: () => Promise<void>;
} {
  const [state, setState] = useState<State>({
    update: null,
    installing: false,
    lastCheckResult: null,
    downloaded: 0,
    total: 0,
  });
  // The plugin's Update object holds the download/install methods. We
  // keep it in a ref-shaped state slot so the banner's button can
  // call back into the same instance the check() returned.
  const [pluginUpdate, setPluginUpdate] = useState<PluginUpdate | null>(null);

  // Track in-flight check() so concurrent triggers (mount + interval
  // + manual) don't stack network calls. The promise is reused so
  // every caller awaits the same resolution.
  const inflightRef = useRef<Promise<void> | null>(null);
  const installingRef = useRef(false);
  installingRef.current = state.installing;

  const runCheck = useCallback(async (opts: { manual: boolean }) => {
    if (!isTauriRuntime()) return;
    if (installingRef.current) return;
    if (opts.manual) {
      // Manual triggers (menu item, programmatic checkNow) bypass
      // and clear the dismissed flag — the user is explicitly
      // asking, so the next auto-check should fire too.
      sessionStorage.removeItem(DISMISS_STORAGE_KEY);
    } else if (sessionStorage.getItem(DISMISS_STORAGE_KEY)) {
      return;
    }
    if (inflightRef.current) {
      return inflightRef.current;
    }
    const run = (async () => {
      try {
        const mod = await import("@tauri-apps/plugin-updater");
        const result = (await mod.check()) as PluginUpdate | null;
        if (result) {
          setPluginUpdate(result);
          setState((prev) => ({
            ...prev,
            update: { version: result.version },
            lastCheckResult: "update",
          }));
        } else {
          // No update available. Only surface the transient
          // "up-to-date" state for manual checks — automatic
          // checks shouldn't put up a banner that disappears
          // four seconds later for no reason the user can see.
          setState((prev) => ({
            ...prev,
            lastCheckResult: opts.manual ? "up-to-date" : null,
          }));
        }
      } catch (err) {
        // Updater not configured (no pubkey, empty endpoints),
        // signature mismatch, network down — all land here.
        // We log so the failure isn't invisible, but keep the
        // banner off so a transient hiccup doesn't pester the
        // user. Manual triggers get the "error" state so the
        // UI can give feedback that something went wrong.
        logWarn("[hecate] desktop updater check failed:", err);
        setState((prev) => ({
          ...prev,
          lastCheckResult: opts.manual ? "error" : null,
        }));
      } finally {
        inflightRef.current = null;
      }
    })();
    inflightRef.current = run;
    return run;
  }, []);

  // Initial check + periodic re-check. inflightRef inside runCheck
  // dedupes concurrent calls, so StrictMode's intentional double-
  // mount doesn't issue two simultaneous check()s — the second
  // setup run finds the in-flight promise and awaits it instead.
  // We deliberately don't add an outer "did mount" guard: that
  // pattern leaves the interval cleared after StrictMode's first
  // cleanup, with no replacement scheduled on the second setup.
  useEffect(() => {
    if (!isTauriRuntime()) return;
    void runCheck({ manual: false });
    const intervalID = setInterval(() => {
      void runCheck({ manual: false });
    }, DESKTOP_UPDATE_POLL_INTERVAL_MS);
    return () => {
      clearInterval(intervalID);
    };
  }, [runCheck]);

  // Listen for the native "Check for Updates…" menu item. The Rust
  // side emits `hecate:check-for-updates` (see lib.rs); we re-run
  // the check as a manual trigger so dismissal is bypassed and the
  // user gets transient "up to date" / "error" feedback when the
  // check finds nothing or fails.
  useEffect(() => {
    if (!isTauriRuntime()) return;
    let cancelled = false;
    let unlisten: (() => void) | undefined;
    void (async () => {
      try {
        const mod = await import("@tauri-apps/api/event");
        if (cancelled) return;
        unlisten = await mod.listen("hecate:check-for-updates", () => {
          void runCheck({ manual: true });
        });
        if (cancelled) {
          unlisten();
          unlisten = undefined;
        }
      } catch (err) {
        logWarn("[hecate] desktop updater event listener failed:", err);
      }
    })();
    return () => {
      cancelled = true;
      unlisten?.();
    };
  }, [runCheck]);

  // Sync dock / taskbar badge with the update-available state.
  // The banner is the primary surface but the badge gives the
  // user a passive notification even when the app is minimized.
  const hasUpdate = state.update !== null;
  useEffect(() => {
    void setUpdateBadge(hasUpdate);
  }, [hasUpdate]);

  // Auto-clear transient feedback (up-to-date, error) after a few
  // seconds. Active update state stays put — the banner is the
  // permanent surface for that.
  useEffect(() => {
    const transient = state.lastCheckResult;
    if (transient !== "up-to-date" && transient !== "error") return;
    const timeoutID = setTimeout(() => {
      setState((prev) => {
        if (prev.lastCheckResult !== transient) return prev;
        return { ...prev, lastCheckResult: null };
      });
    }, UP_TO_DATE_FEEDBACK_MS);
    return () => clearTimeout(timeoutID);
  }, [state.lastCheckResult]);

  const dismiss = useCallback(() => {
    sessionStorage.setItem(DISMISS_STORAGE_KEY, "1");
    setState((prev) => ({ ...prev, update: null, lastCheckResult: null }));
  }, []);

  const installAndRestart = useCallback(async () => {
    if (!pluginUpdate) return;
    setState((prev) => ({ ...prev, installing: true, downloaded: 0, total: 0 }));
    try {
      await pluginUpdate.downloadAndInstall((event) => {
        if (event.event === "Started") {
          const total = event.data.contentLength ?? 0;
          setState((prev) => ({ ...prev, total, downloaded: 0 }));
        } else if (event.event === "Progress") {
          setState((prev) => ({ ...prev, downloaded: prev.downloaded + event.data.chunkLength }));
        }
        // "Finished" needs no UI change — the plugin proceeds to
        // install + relaunch immediately, terminating the renderer.
      });
    } catch (err) {
      logWarn("[hecate] desktop updater install failed:", err);
      setState((prev) => ({ ...prev, installing: false, downloaded: 0, total: 0 }));
    }
  }, [pluginUpdate]);

  const checkNow = useCallback(async () => {
    await runCheck({ manual: true });
  }, [runCheck]);

  const progress = state.installing && state.total > 0
    ? Math.min(1, state.downloaded / state.total)
    : null;

  return {
    update: state.update,
    installing: state.installing,
    progress,
    lastCheckResult: state.lastCheckResult,
    dismiss,
    installAndRestart,
    checkNow,
  };
}
