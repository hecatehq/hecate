// Desktop update controller. It checks the Tauri updater plugin on launch,
// periodically while the app is open, and when the operator explicitly asks.
// Browser, Docker, and bare-binary builds keep it inert: there is no desktop
// package for those surfaces to update.

import { useCallback, useEffect, useRef, useState } from "react";

import { info as logInfo, warn as logWarn } from "./log";
import { isTauriRuntime } from "./tauri";

// Surface or clear the dock / taskbar "update available" badge. Tauri command
// `set_update_badge` is registered in lib.rs; outside Tauri this remains a
// cheap no-op.
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

function isSessionDismissed(): boolean {
  return typeof window !== "undefined" && sessionStorage.getItem(DISMISS_STORAGE_KEY) === "1";
}

// One background re-check per hour is polite to the release endpoint while
// still finding a release when the desktop app stays open all day.
export const DESKTOP_UPDATE_POLL_INTERVAL_MS = 60 * 60 * 1000;

// On macOS, downloadAndInstall can occasionally remain pending after all bytes
// have arrived. Preserve the watchdog so a staged update still gets a chance
// to restart instead of pinning the operator at 100% forever.
export const DESKTOP_UPDATE_INSTALL_STALL_MS = 30_000;

export type DesktopUpdate = {
  currentVersion: string;
  version: string;
  publishedAt?: string;
  notes?: string;
};

export type DesktopUpdateInstallPhase = "idle" | "downloading" | "finishing" | "restarting";
export type DesktopUpdateManualCheckPhase = "checking" | "update" | "up-to-date" | "error";
export type DesktopUpdateInstallFailure = "install" | "restart";

export type DesktopUpdateManualCheck = {
  id: number;
  phase: DesktopUpdateManualCheckPhase;
};

export type DesktopUpdateController = {
  update: DesktopUpdate | null;
  /** True while any update check is in flight. */
  checking: boolean;
  /** The durable outcome of the most recent explicit check, if any. */
  manualCheck: DesktopUpdateManualCheck | null;
  /** Local time at which the most recent successful check completed. */
  lastCheckedAt: number | null;
  /** The successful outcome associated with lastCheckedAt. */
  lastSuccessfulCheck: "update" | "up-to-date" | null;
  /** The available update was hidden only for this app session. */
  dismissed: boolean;
  installing: boolean;
  installPhase: DesktopUpdateInstallPhase;
  /** 0..1 while downloading, null when the package length is unknown. */
  progress: number | null;
  /** Whether download/install or the later relaunch failed. */
  installFailure: DesktopUpdateInstallFailure | null;
  /** True once downloadAndInstall has resolved successfully. */
  restartReady: boolean;
  dismiss: () => void;
  clearManualCheck: () => void;
  installAndRestart: () => Promise<void>;
  /** Retry a failed restart without downloading the package again. */
  retryRestart: () => Promise<void>;
  /** Re-run the updater check immediately, bypassing session dismissal. */
  checkNow: () => Promise<void>;
};

type State = {
  update: DesktopUpdate | null;
  checking: boolean;
  manualCheck: DesktopUpdateManualCheck | null;
  lastCheckedAt: number | null;
  lastSuccessfulCheck: "update" | "up-to-date" | null;
  dismissed: boolean;
  installing: boolean;
  downloaded: number;
  total: number;
  downloadFinished: boolean;
  relaunching: boolean;
  installFailure: DesktopUpdateInstallFailure | null;
  restartReady: boolean;
};

// Narrow the dynamically imported plugin surface so non-desktop bundles do
// not need its full type graph. Update is a Tauri Resource: `close` releases
// its native handle once a superseded or dismissed update is no longer useful.
type DownloadEvent =
  | { event: "Started"; data: { contentLength?: number } }
  | { event: "Progress"; data: { chunkLength: number } }
  | { event: "Finished" };

type PluginUpdate = {
  currentVersion: string;
  version: string;
  date?: string;
  body?: string;
  close?: () => Promise<void> | void;
  downloadAndInstall: (onEvent?: (event: DownloadEvent) => void) => Promise<void>;
};

type ProcessPlugin = {
  relaunch: () => Promise<void>;
};

type DesktopUpdateOptions = {
  /** Opens the shell-owned details dialog for a native/manual check. */
  onManualCheck?: () => void;
};

export function useDesktopUpdate(options: DesktopUpdateOptions = {}): DesktopUpdateController {
  const [state, setState] = useState<State>({
    update: null,
    checking: false,
    manualCheck: null,
    lastCheckedAt: null,
    lastSuccessfulCheck: null,
    dismissed: isSessionDismissed(),
    installing: false,
    downloaded: 0,
    total: 0,
    downloadFinished: false,
    relaunching: false,
    installFailure: null,
    restartReady: false,
  });

  // Keep the updater resource outside render state. The visible metadata lives
  // in State; the resource is used only to download/install the exact update
  // returned by check().
  const pluginUpdateRef = useRef<PluginUpdate | null>(null);
  const inflightCheckRef = useRef<Promise<void> | null>(null);
  const pendingManualCheckIDRef = useRef<number | null>(null);
  const manualCheckSequenceRef = useRef(0);
  const mountedRef = useRef(false);
  const installingRef = useRef(false);
  const relaunchingRef = useRef(false);
  const restartAttemptedRef = useRef(false);
  // Once downloadAndInstall resolves, the package is installed and the only
  // safe recovery from a failed relaunch is another restart—not a new check
  // that would discard the restart-only state and closed updater resource.
  const restartReadyRef = useRef(false);
  // A watchdog can attempt a restart before downloadAndInstall settles. Keep
  // its failed-restart recovery isolated too: the late install result decides
  // whether it becomes a verified restart retry or a normal install retry.
  const restartRecoveryRef = useRef(false);
  // `Finished` means bytes arrived, not that Tauri verified and installed the
  // package. A late downloadAndInstall rejection must therefore win over a
  // watchdog restart failure, including when process.relaunch is still pending.
  const installFailedRef = useRef(false);
  const hasUpdateRef = useRef(false);
  const installWatchdogRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const downloadedRef = useRef(0);
  const totalRef = useRef(0);
  const onManualCheckRef = useRef(options.onManualCheck);
  onManualCheckRef.current = options.onManualCheck;
  hasUpdateRef.current = state.update !== null;

  const closeUpdateResource = useCallback((update: PluginUpdate | null) => {
    if (!update?.close) return;
    try {
      void Promise.resolve(update.close()).catch((err) => {
        logWarn("[hecate] desktop updater resource close failed:", err);
      });
    } catch (err) {
      logWarn("[hecate] desktop updater resource close failed:", err);
    }
  }, []);

  const replacePluginUpdate = useCallback(
    (next: PluginUpdate | null) => {
      const previous = pluginUpdateRef.current;
      pluginUpdateRef.current = next;
      if (previous && previous !== next) closeUpdateResource(previous);
    },
    [closeUpdateResource],
  );

  const clearInstallWatchdog = useCallback(() => {
    if (installWatchdogRef.current === null) return;
    clearTimeout(installWatchdogRef.current);
    installWatchdogRef.current = null;
  }, []);

  const relaunchApp = useCallback(
    async (reason: string): Promise<void> => {
      if (relaunchingRef.current || restartAttemptedRef.current) return;
      restartAttemptedRef.current = true;
      relaunchingRef.current = true;
      clearInstallWatchdog();
      setState((previous) => ({
        ...previous,
        installing: true,
        relaunching: true,
        downloadFinished: true,
        installFailure: null,
      }));
      try {
        logInfo("[hecate] desktop updater relaunch requested:", reason);
        const process = (await import("@tauri-apps/plugin-process")) as ProcessPlugin;
        await process.relaunch();
      } catch (err) {
        logWarn("[hecate] desktop updater relaunch failed:", err);
        if (installFailedRef.current) return;
        relaunchingRef.current = false;
        installingRef.current = false;
        restartRecoveryRef.current = true;
        setState((previous) => ({
          ...previous,
          installing: false,
          relaunching: false,
          downloadFinished: true,
          installFailure: "restart",
        }));
      }
    },
    [clearInstallWatchdog],
  );

  const scheduleInstallWatchdog = useCallback(() => {
    if (installWatchdogRef.current !== null) return;
    installWatchdogRef.current = setTimeout(() => {
      installWatchdogRef.current = null;
      if (!installingRef.current || relaunchingRef.current) return;
      logWarn(
        "[hecate] desktop updater install did not resolve after download; relaunching to finish",
      );
      void relaunchApp("install stalled after download");
    }, DESKTOP_UPDATE_INSTALL_STALL_MS);
  }, [relaunchApp]);

  const runCheck = useCallback(
    async (manual: boolean): Promise<void> => {
      if (!isTauriRuntime() || installingRef.current) return;

      if (restartReadyRef.current || restartRecoveryRef.current) {
        // Keep restart recovery intact. A native menu request still opens its
        // details dialog, where retryRestart is the only valid next action.
        if (manual) onManualCheckRef.current?.();
        return;
      }

      if (manual) {
        const id = ++manualCheckSequenceRef.current;
        pendingManualCheckIDRef.current = id;
        sessionStorage.removeItem(DISMISS_STORAGE_KEY);
        setState((previous) => ({
          ...previous,
          manualCheck: { id, phase: "checking" },
          dismissed: false,
          installFailure: null,
        }));
        // This may originate at the native menu, so open the shell-owned
        // dialog immediately while the check itself runs asynchronously.
        onManualCheckRef.current?.();
      } else if (isSessionDismissed() || hasUpdateRef.current) {
        // A shown update already holds the exact resource the installer needs.
        // Rechecking in the background would only risk replacing it mid-flow.
        return;
      }

      if (inflightCheckRef.current) return inflightCheckRef.current;

      const run = (async () => {
        setState((previous) => ({ ...previous, checking: true }));
        try {
          const updater = await import("@tauri-apps/plugin-updater");
          const result = (await updater.check()) as PluginUpdate | null;
          const manualID = pendingManualCheckIDRef.current;

          if (!mountedRef.current) {
            closeUpdateResource(result);
            return;
          }

          // An install may begin while a manual refresh is awaiting the
          // network. Never replace or close the resource the installer owns.
          if (installingRef.current) {
            closeUpdateResource(result);
            setState((previous) => ({ ...previous, checking: false }));
            return;
          }

          // A dismissal can happen while any check awaits the network. Its
          // promise must not resurrect an update the operator hid this session.
          if (isSessionDismissed()) {
            closeUpdateResource(result);
            setState((previous) => ({ ...previous, checking: false, dismissed: true }));
            return;
          }

          if (result) {
            replacePluginUpdate(result);
            setState((previous) => ({
              ...previous,
              checking: false,
              update: {
                currentVersion: result.currentVersion,
                version: result.version,
                publishedAt: result.date,
                notes: result.body,
              },
              manualCheck: manualID ? { id: manualID, phase: "update" } : previous.manualCheck,
              lastCheckedAt: Date.now(),
              lastSuccessfulCheck: "update",
              dismissed: false,
              installFailure: null,
            }));
          } else {
            replacePluginUpdate(null);
            setState((previous) => ({
              ...previous,
              checking: false,
              update: null,
              manualCheck: manualID ? { id: manualID, phase: "up-to-date" } : previous.manualCheck,
              lastCheckedAt: Date.now(),
              lastSuccessfulCheck: "up-to-date",
              dismissed: false,
              installFailure: null,
            }));
          }
        } catch (err) {
          if (!mountedRef.current) return;
          // Configuration, network, and signature failures are useful in the
          // app log. The UI deliberately exposes only a stable safe summary.
          logWarn("[hecate] desktop updater check failed:", err);
          const manualID = pendingManualCheckIDRef.current;
          setState((previous) => ({
            ...previous,
            checking: false,
            manualCheck: manualID ? { id: manualID, phase: "error" } : previous.manualCheck,
          }));
        } finally {
          inflightCheckRef.current = null;
          pendingManualCheckIDRef.current = null;
        }
      })();
      inflightCheckRef.current = run;
      return run;
    },
    [replacePluginUpdate],
  );

  // Initial plus periodic checks. The in-flight ref intentionally survives
  // StrictMode's setup/cleanup/setup sequence, preventing a second fetch.
  useEffect(() => {
    if (!isTauriRuntime()) return;
    void runCheck(false);
    const intervalID = setInterval(() => {
      void runCheck(false);
    }, DESKTOP_UPDATE_POLL_INTERVAL_MS);
    return () => clearInterval(intervalID);
  }, [runCheck]);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      clearInstallWatchdog();
      // Never release an update resource while its installer owns it. On a
      // normal app exit the process tears down the resource with the webview.
      if (!installingRef.current) {
        const update = pluginUpdateRef.current;
        pluginUpdateRef.current = null;
        closeUpdateResource(update);
      }
    };
  }, [clearInstallWatchdog, closeUpdateResource]);

  // Rust emits this when the native menu's “Check for Updates…” item is used.
  // The callback above opens the details dialog before the check completes.
  useEffect(() => {
    if (!isTauriRuntime()) return;
    let cancelled = false;
    let unlisten: (() => void) | undefined;
    const consumePendingNativeCheck = async () => {
      try {
        const core = await import("@tauri-apps/api/core");
        const pending = await core.invoke<boolean>("take_pending_desktop_update_check");
        if (pending && !cancelled) void runCheck(true);
      } catch (err) {
        logWarn("[hecate] desktop updater pending native check failed:", err);
      }
    };
    void (async () => {
      try {
        const events = await import("@tauri-apps/api/event");
        if (cancelled) return;
        unlisten = await events.listen("hecate:check-for-updates", () => {
          void consumePendingNativeCheck();
        });
        if (cancelled) {
          unlisten();
          unlisten = undefined;
          return;
        }
        // The native menu records its intent before emitting the event. This
        // catches a request made while the splash page was still active, when
        // there was no listener to receive the transient event.
        void consumePendingNativeCheck();
      } catch (err) {
        logWarn("[hecate] desktop updater event listener failed:", err);
      }
    })();
    return () => {
      cancelled = true;
      unlisten?.();
    };
  }, [runCheck]);

  // Keep passive native notification in sync even if the app is minimized.
  const hasUpdate = state.update !== null;
  useEffect(() => {
    void setUpdateBadge(hasUpdate);
  }, [hasUpdate]);

  const dismiss = useCallback(() => {
    if (installingRef.current) return;
    sessionStorage.setItem(DISMISS_STORAGE_KEY, "1");
    pendingManualCheckIDRef.current = null;
    replacePluginUpdate(null);
    setState((previous) => ({
      ...previous,
      update: null,
      manualCheck: null,
      dismissed: true,
      installFailure: null,
    }));
  }, [replacePluginUpdate]);

  const clearManualCheck = useCallback(() => {
    pendingManualCheckIDRef.current = null;
    setState((previous) => ({ ...previous, manualCheck: null }));
  }, []);

  const installAndRestart = useCallback(async () => {
    const update = pluginUpdateRef.current;
    if (!update || installingRef.current) return;

    // This guard must be synchronous: two quick clicks can both happen before
    // React commits the `installing` state update.
    installingRef.current = true;
    relaunchingRef.current = false;
    restartAttemptedRef.current = false;
    restartReadyRef.current = false;
    restartRecoveryRef.current = false;
    installFailedRef.current = false;
    downloadedRef.current = 0;
    totalRef.current = 0;
    setState((previous) => ({
      ...previous,
      installing: true,
      downloaded: 0,
      total: 0,
      downloadFinished: false,
      relaunching: false,
      installFailure: null,
      restartReady: false,
    }));

    try {
      logInfo("[hecate] desktop updater install started:", update.version);
      await update.downloadAndInstall((event) => {
        if (event.event === "Started") {
          const total = event.data.contentLength ?? 0;
          downloadedRef.current = 0;
          totalRef.current = total;
          logInfo("[hecate] desktop updater download started:", { total });
          setState((previous) => ({
            ...previous,
            total,
            downloaded: 0,
            downloadFinished: false,
          }));
        } else if (event.event === "Progress") {
          downloadedRef.current += event.data.chunkLength;
          if (totalRef.current > 0 && downloadedRef.current >= totalRef.current) {
            scheduleInstallWatchdog();
          }
          setState((previous) => ({ ...previous, downloaded: downloadedRef.current }));
        } else if (event.event === "Finished") {
          logInfo("[hecate] desktop updater download finished");
          scheduleInstallWatchdog();
          setState((previous) => ({
            ...previous,
            downloaded: previous.total || previous.downloaded,
            downloadFinished: true,
          }));
        }
      });
      // The installed payload no longer needs its Tauri resource. Preserve
      // display metadata in state for a failed restart, but release the
      // native handle before asking the process plugin to relaunch.
      replacePluginUpdate(null);
      restartReadyRef.current = true;
      setState((previous) => ({ ...previous, restartReady: true }));
      // A watchdog-initiated relaunch is already in progress; do not request a
      // second one after the install promise eventually resolves.
      if (!restartAttemptedRef.current) {
        logInfo("[hecate] desktop updater install finished; relaunching");
        await relaunchApp("install completed");
      }
    } catch (err) {
      // `Finished` only means the payload bytes arrived. Verification or the
      // install step can still reject after the watchdog asked for a restart,
      // so this durable failure is more informative than a failed restart and
      // restores the safe download retry path.
      installFailedRef.current = true;
      clearInstallWatchdog();
      logWarn("[hecate] desktop updater install failed:", err);
      installingRef.current = false;
      relaunchingRef.current = false;
      restartAttemptedRef.current = false;
      restartReadyRef.current = false;
      restartRecoveryRef.current = false;
      setState((previous) => ({
        ...previous,
        installing: false,
        relaunching: false,
        downloaded: 0,
        total: 0,
        downloadFinished: false,
        installFailure: "install",
        restartReady: false,
      }));
    }
  }, [clearInstallWatchdog, relaunchApp, replacePluginUpdate, scheduleInstallWatchdog]);

  const retryRestart = useCallback(async () => {
    if (state.installFailure !== "restart" || installingRef.current) return;
    installingRef.current = true;
    relaunchingRef.current = false;
    restartAttemptedRef.current = false;
    await relaunchApp("operator retry after restart failure");
  }, [relaunchApp, state.installFailure]);

  const checkNow = useCallback(async () => {
    await runCheck(true);
  }, [runCheck]);

  const progress = state.installing
    ? state.downloadFinished
      ? 1
      : state.total > 0
        ? Math.min(1, state.downloaded / state.total)
        : null
    : null;
  const installPhase: DesktopUpdateInstallPhase = state.installing
    ? state.relaunching
      ? "restarting"
      : state.downloadFinished || (state.total > 0 && state.downloaded >= state.total)
        ? "finishing"
        : "downloading"
    : "idle";

  return {
    update: state.update,
    checking: state.checking,
    manualCheck: state.manualCheck,
    lastCheckedAt: state.lastCheckedAt,
    lastSuccessfulCheck: state.lastSuccessfulCheck,
    dismissed: state.dismissed,
    installing: state.installing,
    installPhase,
    progress,
    installFailure: state.installFailure,
    restartReady: state.restartReady,
    dismiss,
    clearManualCheck,
    installAndRestart,
    retryRestart,
    checkNow,
  };
}
