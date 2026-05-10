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
};

export function useDesktopUpdate(): {
  update: DesktopUpdate | null;
  installing: boolean;
  dismiss: () => void;
  installAndRestart: () => Promise<void>;
} {
  const [state, setState] = useState<State>({ update: null, installing: false });
  // The plugin's Update object holds the download/install methods. We
  // keep it in a ref-shaped state slot so the banner's button can
  // call back into the same instance the check() returned.
  const [pluginUpdate, setPluginUpdate] = useState<{
    downloadAndInstall: () => Promise<void>;
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
        setState({ update: { version: result.version }, installing: false });
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
    setState((prev) => ({ ...prev, installing: true }));
    try {
      await pluginUpdate.downloadAndInstall();
      // The plugin relaunches automatically on success; we never
      // reach the line below in practice.
    } catch {
      setState((prev) => ({ ...prev, installing: false }));
    }
  }, [pluginUpdate]);

  return { update: state.update, installing: state.installing, dismiss, installAndRestart };
}

function isTauriRuntime(): boolean {
  return typeof window !== "undefined"
    && (Object.prototype.hasOwnProperty.call(window, "__TAURI_INTERNALS__")
      || Object.prototype.hasOwnProperty.call(window, "__TAURI__"));
}
