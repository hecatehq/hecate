// Desktop update hook. Fires once on mount in the Tauri runtime and
// asks the updater plugin whether a newer version is published. If
// the gateway UI is loaded outside Tauri (browser, Docker, bare
// binary), the hook is inert — there is no app to update from there.
//
// Failures (no updater configured, network down, signature
// mismatch) are swallowed — we treat "no update detected" the same
// regardless of why. The banner only appears on a positive answer.

import { useCallback, useEffect, useState } from "react";

const DISMISS_STORAGE_KEY = "hecate.update.dismissed";

export type DesktopUpdate = {
  version: string;
};

type State = {
  update: DesktopUpdate | null;
  installing: boolean;
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

export function useDesktopUpdate(): {
  update: DesktopUpdate | null;
  installing: boolean;
  /** 0..1 while downloading, null when not downloading or when total is unknown. */
  progress: number | null;
  dismiss: () => void;
  installAndRestart: () => Promise<void>;
} {
  const [state, setState] = useState<State>({ update: null, installing: false, downloaded: 0, total: 0 });
  // The plugin's Update object holds the download/install methods. We
  // keep it in a ref-shaped state slot so the banner's button can
  // call back into the same instance the check() returned.
  const [pluginUpdate, setPluginUpdate] = useState<{
    downloadAndInstall: (onEvent?: (e: DownloadEvent) => void) => Promise<void>;
  } | null>(null);

  useEffect(() => {
    if (!isTauriRuntime()) return;
    if (sessionStorage.getItem(DISMISS_STORAGE_KEY)) return;
    let cancelled = false;
    void (async () => {
      try {
        const mod = await import("@tauri-apps/plugin-updater");
        const result = await mod.check();
        if (cancelled || !result) return;
        setPluginUpdate(result);
        setState((prev) => ({ ...prev, update: { version: result.version } }));
      } catch {
        // Updater not configured (no pubkey, empty endpoints), or
        // a network/signature error. Treat all as "no update" —
        // the banner stays hidden.
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const dismiss = useCallback(() => {
    sessionStorage.setItem(DISMISS_STORAGE_KEY, "1");
    setState((prev) => ({ ...prev, update: null }));
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
    } catch {
      setState((prev) => ({ ...prev, installing: false, downloaded: 0, total: 0 }));
    }
  }, [pluginUpdate]);

  const progress = state.installing && state.total > 0
    ? Math.min(1, state.downloaded / state.total)
    : null;

  return { update: state.update, installing: state.installing, progress, dismiss, installAndRestart };
}

function isTauriRuntime(): boolean {
  return typeof window !== "undefined"
    && (Object.prototype.hasOwnProperty.call(window, "__TAURI_INTERNALS__")
      || Object.prototype.hasOwnProperty.call(window, "__TAURI__"));
}
